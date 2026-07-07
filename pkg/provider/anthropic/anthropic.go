// Package anthropic implements provider.Provider over the Anthropic
// Messages API with streaming.
package anthropic

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

const (
	defaultBaseURL = "https://api.anthropic.com"
	apiVersion     = "2023-06-01"
)

// retryDelays is a var so tests can shorten the backoff.
var retryDelays = []time.Duration{time.Second, 4 * time.Second}

// Client talks to the Anthropic Messages API.
type Client struct {
	apiKey  string
	baseURL string
	headers map[string]string
	http    *http.Client
}

// SetHeaders adds extra HTTP headers sent on every request, for providers that
// need a custom header. Fixed headers still win.
func (c *Client) SetHeaders(h map[string]string) { c.headers = h }

// New returns a client. An empty baseURL means the public API endpoint.
func New(apiKey, baseURL string) *Client {
	if baseURL == "" {
		baseURL = defaultBaseURL
	}
	return &Client{
		apiKey:  apiKey,
		baseURL: strings.TrimRight(baseURL, "/"),
		// No client timeout: streams can be long-lived, ctx governs.
		http: &http.Client{},
	}
}

// Name implements provider.Provider.
func (c *Client) Name() string { return "anthropic" }

type apiTool struct {
	Name        string          `json:"name"`
	Description string          `json:"description,omitempty"`
	InputSchema json.RawMessage `json:"input_schema"`
}

type apiBlock struct {
	Type      string          `json:"type"`
	Text      string          `json:"text,omitempty"`
	Thinking  string          `json:"thinking,omitempty"`
	Signature string          `json:"signature,omitempty"`
	ID        string          `json:"id,omitempty"`
	Name      string          `json:"name,omitempty"`
	Input     json.RawMessage `json:"input,omitempty"`
	ToolUseID string          `json:"tool_use_id,omitempty"`
	Content   string          `json:"content,omitempty"`
	IsError   bool            `json:"is_error,omitempty"`
}

type apiMessage struct {
	Role    string     `json:"role"`
	Content []apiBlock `json:"content"`
}

type apiThinking struct {
	Type         string `json:"type"`
	BudgetTokens int    `json:"budget_tokens"`
}

type apiRequest struct {
	Model     string       `json:"model"`
	MaxTokens int          `json:"max_tokens"`
	Stream    bool         `json:"stream"`
	System    string       `json:"system,omitempty"`
	Thinking  *apiThinking `json:"thinking,omitempty"`
	Tools     []apiTool    `json:"tools,omitempty"`
	Messages  []apiMessage `json:"messages"`
}

// thinkingBudget maps a reasoning level to an extended-thinking token budget.
// An empty or "off" level returns 0, meaning no thinking.
func thinkingBudget(level string) int {
	switch level {
	case "minimal":
		return 1024
	case "low":
		return 4096
	case "medium":
		return 8192
	case "high":
		return 16384
	case "xhigh":
		return 32768
	default:
		return 0
	}
}

func buildRequest(req provider.Request) apiRequest {
	out := apiRequest{
		Model:     req.Model,
		MaxTokens: req.MaxTokens,
		Stream:    true,
		System:    req.System,
	}
	thinkOn := false
	if budget := thinkingBudget(req.Reasoning); budget > 0 {
		out.Thinking = &apiThinking{Type: "enabled", BudgetTokens: budget}
		// max_tokens must exceed the thinking budget; leave headroom for the
		// visible answer on top of the thinking allowance.
		headroom := max(req.MaxTokens, 4096)
		if out.MaxTokens <= budget {
			out.MaxTokens = budget + headroom
		}
		thinkOn = true
	}
	for _, t := range req.Tools {
		out.Tools = append(out.Tools, apiTool{
			Name:        t.Name,
			Description: t.Description,
			InputSchema: t.Schema,
		})
	}
	for _, m := range req.Messages {
		am := apiMessage{Role: m.Role}
		for _, b := range m.Content {
			switch b.Type {
			case provider.BlockText:
				am.Content = append(am.Content, apiBlock{Type: "text", Text: b.Text})
			case provider.BlockToolUse:
				input := b.Input
				if len(input) == 0 {
					input = json.RawMessage("{}")
				}
				am.Content = append(am.Content, apiBlock{Type: "tool_use", ID: b.ID, Name: b.Name, Input: input})
			case provider.BlockToolResult:
				am.Content = append(am.Content, apiBlock{Type: "tool_result", ToolUseID: b.ToolUseID, Content: b.Text, IsError: b.IsError})
			case provider.BlockThinking:
				// With extended thinking on, the API requires the prior
				// thinking block, with its signature, to be sent back ahead
				// of the tool_use it led to. With thinking off we drop it.
				if thinkOn && b.Signature != "" {
					am.Content = append(am.Content, apiBlock{Type: "thinking", Thinking: b.Text, Signature: b.Signature})
				}
			}
		}
		out.Messages = append(out.Messages, am)
	}
	return out
}

func normalizeStop(reason string) string {
	switch reason {
	case "end_turn", "stop_sequence":
		return provider.StopEndTurn
	case "tool_use":
		return provider.StopToolUse
	case "max_tokens":
		return provider.StopMaxTokens
	default:
		return reason
	}
}

// Complete implements provider.Provider.
func (c *Client) Complete(ctx context.Context, req provider.Request, on func(provider.Event)) (*provider.Response, error) {
	body, err := json.Marshal(buildRequest(req))
	if err != nil {
		return nil, fmt.Errorf("anthropic: encode request: %w", err)
	}

	for attempt := 0; ; attempt++ {
		hreq, err := http.NewRequestWithContext(ctx, http.MethodPost, c.baseURL+"/v1/messages", bytes.NewReader(body))
		if err != nil {
			return nil, fmt.Errorf("anthropic: %w", err)
		}
		for k, v := range c.headers {
			hreq.Header.Set(k, v)
		}
		hreq.Header.Set("Content-Type", "application/json")
		hreq.Header.Set("x-api-key", c.apiKey)
		hreq.Header.Set("anthropic-version", apiVersion)

		resp, err := c.http.Do(hreq)
		if err != nil {
			return nil, fmt.Errorf("anthropic: %w", err)
		}
		if resp.StatusCode/100 == 2 {
			// Once the stream starts we never retry.
			r, err := readStream(resp.Body, on)
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
		return nil, fmt.Errorf("anthropic: %s: %s", resp.Status, msg)
	}
}

type sseData struct {
	Type    string `json:"type"`
	Message *struct {
		Usage struct {
			InputTokens int `json:"input_tokens"`
		} `json:"usage"`
	} `json:"message"`
	ContentBlock *struct {
		Type string `json:"type"`
		ID   string `json:"id"`
		Name string `json:"name"`
	} `json:"content_block"`
	Delta *struct {
		Type        string `json:"type"`
		Text        string `json:"text"`
		Thinking    string `json:"thinking"`
		Signature   string `json:"signature"`
		PartialJSON string `json:"partial_json"`
		StopReason  string `json:"stop_reason"`
	} `json:"delta"`
	Usage *struct {
		OutputTokens int `json:"output_tokens"`
	} `json:"usage"`
	Error *struct {
		Type    string `json:"type"`
		Message string `json:"message"`
	} `json:"error"`
}

func readStream(body io.Reader, on func(provider.Event)) (*provider.Response, error) {
	emit := func(ev provider.Event) {
		if on != nil {
			on(ev)
		}
	}

	res := &provider.Response{Message: provider.Message{Role: provider.RoleAssistant}}

	// Current content block being streamed.
	var (
		curType string
		curID   string
		curName string
		curSig  string
		curBuf  strings.Builder
	)

	sc := bufio.NewScanner(body)
	sc.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	for sc.Scan() {
		line := sc.Text()
		if !strings.HasPrefix(line, "data:") {
			continue
		}
		payload := strings.TrimSpace(strings.TrimPrefix(line, "data:"))
		if payload == "" {
			continue
		}
		var ev sseData
		if err := json.Unmarshal([]byte(payload), &ev); err != nil {
			return nil, fmt.Errorf("anthropic: decode stream event: %w", err)
		}
		switch ev.Type {
		case "message_start":
			if ev.Message != nil {
				res.Usage.InputTokens = ev.Message.Usage.InputTokens
			}
		case "content_block_start":
			if ev.ContentBlock == nil {
				continue
			}
			curType = ev.ContentBlock.Type
			curID = ev.ContentBlock.ID
			curName = ev.ContentBlock.Name
			curSig = ""
			curBuf.Reset()
			if curType == "tool_use" {
				emit(provider.Event{Type: "tool_start", Tool: curName})
			}
		case "content_block_delta":
			if ev.Delta == nil {
				continue
			}
			switch ev.Delta.Type {
			case "text_delta":
				curBuf.WriteString(ev.Delta.Text)
				emit(provider.Event{Type: "text", Text: ev.Delta.Text})
			case "thinking_delta":
				curBuf.WriteString(ev.Delta.Thinking)
				emit(provider.Event{Type: "thinking", Text: ev.Delta.Thinking})
			case "signature_delta":
				curSig += ev.Delta.Signature
			case "input_json_delta":
				curBuf.WriteString(ev.Delta.PartialJSON)
			}
		case "content_block_stop":
			switch curType {
			case "text":
				res.Message.Content = append(res.Message.Content, provider.Block{
					Type: provider.BlockText,
					Text: curBuf.String(),
				})
			case "thinking":
				res.Message.Content = append(res.Message.Content, provider.Block{
					Type:      provider.BlockThinking,
					Text:      curBuf.String(),
					Signature: curSig,
				})
			case "tool_use":
				input := curBuf.String()
				if input == "" {
					input = "{}"
				}
				res.Message.Content = append(res.Message.Content, provider.Block{
					Type:  provider.BlockToolUse,
					ID:    curID,
					Name:  curName,
					Input: json.RawMessage(input),
				})
			}
			curType = ""
		case "message_delta":
			if ev.Delta != nil && ev.Delta.StopReason != "" {
				res.StopReason = normalizeStop(ev.Delta.StopReason)
			}
			if ev.Usage != nil {
				res.Usage.OutputTokens = ev.Usage.OutputTokens
			}
		case "message_stop", "ping":
		case "error":
			if ev.Error != nil {
				return nil, fmt.Errorf("anthropic: %s: %s", ev.Error.Type, ev.Error.Message)
			}
			return nil, fmt.Errorf("anthropic: stream error")
		}
	}
	if err := sc.Err(); err != nil {
		return nil, fmt.Errorf("anthropic: read stream: %w", err)
	}
	return res, nil
}
