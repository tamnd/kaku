// Package responses implements provider.Provider over the OpenAI Responses
// API with streaming. This is the wire protocol of the newer OpenAI models
// and of agent-oriented OpenAI-compatible servers.
package responses

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/tamnd/kaku/pkg/provider"
)

const defaultBaseURL = "https://api.openai.com/v1"

// retryDelays is a var so tests can shorten the backoff.
var retryDelays = []time.Duration{time.Second, 4 * time.Second}

// Client talks to an OpenAI Responses API endpoint.
type Client struct {
	apiKey  string
	baseURL string
	name    string
	headers map[string]string
	http    *http.Client
}

// SetHeaders adds extra HTTP headers sent on every request, for providers that
// need a custom header. Fixed headers still win.
func (c *Client) SetHeaders(h map[string]string) { c.headers = h }

// New returns a client. An empty baseURL means the public OpenAI endpoint,
// an empty name means "responses".
func New(apiKey, baseURL, name string) *Client {
	if baseURL == "" {
		baseURL = defaultBaseURL
	}
	if name == "" {
		name = "responses"
	}
	return &Client{
		apiKey:  apiKey,
		baseURL: strings.TrimRight(baseURL, "/"),
		name:    name,
		// No client timeout: streams can be long-lived, ctx governs.
		http: &http.Client{},
	}
}

// Name implements provider.Provider.
func (c *Client) Name() string { return c.name }

// Responses tools are flat: name and parameters sit at the top level.
type apiTool struct {
	Type        string          `json:"type"`
	Name        string          `json:"name"`
	Description string          `json:"description,omitempty"`
	Parameters  json.RawMessage `json:"parameters"`
}

type contentPart struct {
	Type     string `json:"type"`
	Text     string `json:"text,omitempty"`
	ImageURL string `json:"image_url,omitempty"` // input_image: a data URL
}

// item is one entry in the request input or the response output.
type item struct {
	Type      string        `json:"type"`
	Role      string        `json:"role,omitempty"`
	Content   []contentPart `json:"content,omitempty"`
	CallID    string        `json:"call_id,omitempty"`
	Name      string        `json:"name,omitempty"`
	Arguments string        `json:"arguments,omitempty"`
	Output    string        `json:"output,omitempty"`
}

type apiReasoning struct {
	Effort string `json:"effort"`
}

type apiRequest struct {
	Model           string        `json:"model"`
	Instructions    string        `json:"instructions,omitempty"`
	Input           []item        `json:"input"`
	Tools           []apiTool     `json:"tools,omitempty"`
	MaxOutputTokens int           `json:"max_output_tokens,omitempty"`
	Reasoning       *apiReasoning `json:"reasoning,omitempty"`
	Temperature     *float64      `json:"temperature,omitempty"`
	TopP            *float64      `json:"top_p,omitempty"`
	Stream          bool          `json:"stream"`
	Store           bool          `json:"store"`
}

// dataURL builds the "data:<mime>;base64,<data>" URL an input_image expects.
func dataURL(mediaType, data string) string {
	if mediaType == "" {
		mediaType = "image/png"
	}
	return "data:" + mediaType + ";base64," + data
}

func buildRequest(req provider.Request) apiRequest {
	out := apiRequest{
		Model:           req.Model,
		Instructions:    req.System,
		MaxOutputTokens: req.MaxTokens,
		Stream:          true,
		Store:           false,
	}
	if req.Reasoning != "" && req.Reasoning != "off" {
		out.Reasoning = &apiReasoning{Effort: req.Reasoning}
	} else {
		// Reasoning models reject the sampling knobs; only send them when
		// reasoning is off.
		if req.Temperature != 0 {
			out.Temperature = &req.Temperature
		}
		if req.TopP != 0 {
			out.TopP = &req.TopP
		}
	}

	for _, t := range req.Tools {
		out.Tools = append(out.Tools, apiTool{
			Type:        "function",
			Name:        t.Name,
			Description: t.Description,
			Parameters:  t.Schema,
		})
	}

	for _, m := range req.Messages {
		switch m.Role {
		case provider.RoleAssistant:
			var text strings.Builder
			for _, b := range m.Content {
				switch b.Type {
				case provider.BlockText:
					text.WriteString(b.Text)
				case provider.BlockToolUse:
					args := string(b.Input)
					if args == "" {
						args = "{}"
					}
					out.Input = append(out.Input, item{
						Type:      "function_call",
						CallID:    b.ID,
						Name:      b.Name,
						Arguments: args,
					})
				}
			}
			if text.Len() > 0 {
				out.Input = append(out.Input, item{
					Type:    "message",
					Role:    "assistant",
					Content: []contentPart{{Type: "output_text", Text: text.String()}},
				})
			}
		default:
			var text strings.Builder
			var images []provider.Block
			hasToolResult := false
			for _, b := range m.Content {
				switch b.Type {
				case provider.BlockToolResult:
					hasToolResult = true
					out.Input = append(out.Input, item{
						Type:   "function_call_output",
						CallID: b.ToolUseID,
						Output: b.Text,
					})
				case provider.BlockText:
					text.WriteString(b.Text)
				case provider.BlockImage:
					images = append(images, b)
				}
			}
			if text.Len() > 0 || len(images) > 0 || !hasToolResult {
				parts := []contentPart{{Type: "input_text", Text: text.String()}}
				for _, img := range images {
					parts = append(parts, contentPart{
						Type:     "input_image",
						ImageURL: dataURL(img.MediaType, img.Data),
					})
				}
				out.Input = append(out.Input, item{Type: "message", Role: "user", Content: parts})
			}
		}
	}
	return out
}

// Complete implements provider.Provider.
func (c *Client) Complete(ctx context.Context, req provider.Request, on func(provider.Event)) (*provider.Response, error) {
	body, err := json.Marshal(buildRequest(req))
	if err != nil {
		return nil, fmt.Errorf("%s: encode request: %w", c.name, err)
	}

	for attempt := 0; ; attempt++ {
		hreq, err := http.NewRequestWithContext(ctx, http.MethodPost, c.baseURL+"/responses", bytes.NewReader(body))
		if err != nil {
			return nil, fmt.Errorf("%s: %w", c.name, err)
		}
		for k, v := range c.headers {
			hreq.Header.Set(k, v)
		}
		hreq.Header.Set("Content-Type", "application/json")
		if c.apiKey != "" {
			hreq.Header.Set("Authorization", "Bearer "+c.apiKey)
		}

		resp, err := c.http.Do(hreq)
		if err != nil {
			return nil, fmt.Errorf("%s: %w", c.name, err)
		}
		if resp.StatusCode/100 == 2 {
			// Once the stream starts we never retry.
			r, err := c.readStream(resp.Body, on)
			resp.Body.Close()
			return r, err
		}

		msg, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		resp.Body.Close()
		if (resp.StatusCode == http.StatusTooManyRequests || resp.StatusCode >= 500) && attempt < len(retryDelays) {
			select {
			case <-ctx.Done():
				return nil, ctx.Err()
			case <-time.After(retryDelays[attempt]):
			}
			continue
		}
		return nil, fmt.Errorf("%s: %s: %s", c.name, resp.Status, msg)
	}
}

// event is a streamed Responses API event. Only the fields we act on.
type event struct {
	Type  string `json:"type"`
	Delta string `json:"delta"`
	Item  *item  `json:"item"`

	Response *struct {
		Status            string `json:"status"`
		Output            []item `json:"output"`
		IncompleteDetails *struct {
			Reason string `json:"reason"`
		} `json:"incomplete_details"`
		Usage *struct {
			InputTokens  int `json:"input_tokens"`
			OutputTokens int `json:"output_tokens"`
		} `json:"usage"`
	} `json:"response"`

	Error *struct {
		Message string `json:"message"`
	} `json:"error"`
}

func (c *Client) readStream(body io.Reader, on func(provider.Event)) (*provider.Response, error) {
	emit := func(ev provider.Event) {
		if on != nil {
			on(ev)
		}
	}

	res := &provider.Response{Message: provider.Message{Role: provider.RoleAssistant}}
	done := false

	sc := bufio.NewScanner(body)
	sc.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	for sc.Scan() {
		line := sc.Text()
		if !strings.HasPrefix(line, "data:") {
			continue
		}
		payload := strings.TrimSpace(strings.TrimPrefix(line, "data:"))
		if payload == "" || payload == "[DONE]" {
			continue
		}
		var ev event
		if err := json.Unmarshal([]byte(payload), &ev); err != nil {
			return nil, fmt.Errorf("%s: decode stream event: %w", c.name, err)
		}
		switch ev.Type {
		case "response.output_text.delta":
			if ev.Delta != "" {
				emit(provider.Event{Type: "text", Text: ev.Delta})
			}
		case "response.output_item.added":
			if ev.Item != nil && ev.Item.Type == "function_call" {
				emit(provider.Event{Type: "tool_start", Tool: ev.Item.Name})
			}
		case "response.completed", "response.incomplete":
			if ev.Response == nil {
				return nil, fmt.Errorf("%s: %s event without response", c.name, ev.Type)
			}
			done = true
			for _, it := range ev.Response.Output {
				switch it.Type {
				case "message":
					var text strings.Builder
					for _, p := range it.Content {
						if p.Type == "output_text" {
							text.WriteString(p.Text)
						}
					}
					if text.Len() > 0 {
						res.Message.Content = append(res.Message.Content, provider.Block{
							Type: provider.BlockText,
							Text: text.String(),
						})
					}
				case "function_call":
					args := it.Arguments
					if args == "" {
						args = "{}"
					}
					res.Message.Content = append(res.Message.Content, provider.Block{
						Type:  provider.BlockToolUse,
						ID:    it.CallID,
						Name:  it.Name,
						Input: json.RawMessage(args),
					})
					res.StopReason = provider.StopToolUse
				}
			}
			if res.StopReason == "" {
				res.StopReason = provider.StopEndTurn
				if d := ev.Response.IncompleteDetails; d != nil && d.Reason == "max_output_tokens" {
					res.StopReason = provider.StopMaxTokens
				}
			}
			if u := ev.Response.Usage; u != nil {
				res.Usage.InputTokens = u.InputTokens
				res.Usage.OutputTokens = u.OutputTokens
			}
		case "response.failed", "error":
			if ev.Error != nil {
				return nil, fmt.Errorf("%s: %s", c.name, ev.Error.Message)
			}
			return nil, fmt.Errorf("%s: response failed", c.name)
		}
	}
	if err := sc.Err(); err != nil {
		return nil, fmt.Errorf("%s: read stream: %w", c.name, err)
	}
	if !done {
		return nil, fmt.Errorf("%s: stream ended without response.completed", c.name)
	}
	return res, nil
}
