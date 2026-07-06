// Package openai implements provider.Provider over the OpenAI chat
// completions API with streaming. It also works with OpenAI-compatible
// servers such as llama.cpp, vLLM, and MLX.
package openai

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"sort"
	"strings"
	"time"

	"github.com/tamnd/kaku/pkg/provider"
)

const defaultBaseURL = "https://api.openai.com/v1"

// retryDelays is a var so tests can shorten the backoff.
var retryDelays = []time.Duration{time.Second, 4 * time.Second}

// Client talks to an OpenAI-compatible chat completions endpoint.
type Client struct {
	apiKey  string
	baseURL string
	name    string
	http    *http.Client
}

// New returns a client. An empty baseURL means the public OpenAI endpoint,
// an empty name means "openai".
func New(apiKey, baseURL, name string) *Client {
	if baseURL == "" {
		baseURL = defaultBaseURL
	}
	if name == "" {
		name = "openai"
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

type apiFunction struct {
	Name        string          `json:"name"`
	Description string          `json:"description,omitempty"`
	Parameters  json.RawMessage `json:"parameters"`
}

type apiTool struct {
	Type     string      `json:"type"`
	Function apiFunction `json:"function"`
}

type apiToolCall struct {
	ID       string `json:"id"`
	Type     string `json:"type"`
	Function struct {
		Name      string `json:"name"`
		Arguments string `json:"arguments"`
	} `json:"function"`
}

type apiMessage struct {
	Role       string        `json:"role"`
	Content    any           `json:"content"`
	ToolCallID string        `json:"tool_call_id,omitempty"`
	ToolCalls  []apiToolCall `json:"tool_calls,omitempty"`
}

type apiRequest struct {
	Model         string       `json:"model"`
	Messages      []apiMessage `json:"messages"`
	Tools         []apiTool    `json:"tools,omitempty"`
	MaxTokens     int          `json:"max_tokens,omitempty"`
	Stream        bool         `json:"stream"`
	StreamOptions struct {
		IncludeUsage bool `json:"include_usage"`
	} `json:"stream_options"`
}

func buildRequest(req provider.Request) apiRequest {
	out := apiRequest{
		Model:     req.Model,
		MaxTokens: req.MaxTokens,
		Stream:    true,
	}
	out.StreamOptions.IncludeUsage = true

	for _, t := range req.Tools {
		out.Tools = append(out.Tools, apiTool{
			Type: "function",
			Function: apiFunction{
				Name:        t.Name,
				Description: t.Description,
				Parameters:  t.Schema,
			},
		})
	}

	if req.System != "" {
		out.Messages = append(out.Messages, apiMessage{Role: "system", Content: req.System})
	}
	for _, m := range req.Messages {
		switch m.Role {
		case provider.RoleAssistant:
			am := apiMessage{Role: "assistant"}
			var text strings.Builder
			for _, b := range m.Content {
				switch b.Type {
				case provider.BlockText:
					text.WriteString(b.Text)
				case provider.BlockToolUse:
					tc := apiToolCall{ID: b.ID, Type: "function"}
					tc.Function.Name = b.Name
					tc.Function.Arguments = string(b.Input)
					if tc.Function.Arguments == "" {
						tc.Function.Arguments = "{}"
					}
					am.ToolCalls = append(am.ToolCalls, tc)
				}
			}
			if text.Len() > 0 {
				am.Content = text.String()
			}
			out.Messages = append(out.Messages, am)
		default:
			// Tool results become role:tool messages, one per block, then
			// any text blocks follow as a plain user message.
			var text strings.Builder
			hasToolResult := false
			for _, b := range m.Content {
				switch b.Type {
				case provider.BlockToolResult:
					hasToolResult = true
					out.Messages = append(out.Messages, apiMessage{
						Role:       "tool",
						ToolCallID: b.ToolUseID,
						Content:    b.Text,
					})
				case provider.BlockText:
					text.WriteString(b.Text)
				}
			}
			if text.Len() > 0 || !hasToolResult {
				out.Messages = append(out.Messages, apiMessage{Role: "user", Content: text.String()})
			}
		}
	}
	return out
}

func normalizeStop(reason string) string {
	switch reason {
	case "stop":
		return provider.StopEndTurn
	case "tool_calls":
		return provider.StopToolUse
	case "length":
		return provider.StopMaxTokens
	default:
		return reason
	}
}

// Complete implements provider.Provider.
func (c *Client) Complete(ctx context.Context, req provider.Request, on func(provider.Event)) (*provider.Response, error) {
	body, err := json.Marshal(buildRequest(req))
	if err != nil {
		return nil, fmt.Errorf("%s: encode request: %w", c.name, err)
	}

	for attempt := 0; ; attempt++ {
		hreq, err := http.NewRequestWithContext(ctx, http.MethodPost, c.baseURL+"/chat/completions", bytes.NewReader(body))
		if err != nil {
			return nil, fmt.Errorf("%s: %w", c.name, err)
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

type chunk struct {
	Choices []struct {
		Delta struct {
			Content   *string `json:"content"`
			ToolCalls []struct {
				Index    int    `json:"index"`
				ID       string `json:"id"`
				Function struct {
					Name      string `json:"name"`
					Arguments string `json:"arguments"`
				} `json:"function"`
			} `json:"tool_calls"`
		} `json:"delta"`
		FinishReason *string `json:"finish_reason"`
	} `json:"choices"`
	Usage *struct {
		PromptTokens     int `json:"prompt_tokens"`
		CompletionTokens int `json:"completion_tokens"`
	} `json:"usage"`
}

type toolAcc struct {
	id   string
	name string
	args strings.Builder
}

func (c *Client) readStream(body io.Reader, on func(provider.Event)) (*provider.Response, error) {
	emit := func(ev provider.Event) {
		if on != nil {
			on(ev)
		}
	}

	res := &provider.Response{Message: provider.Message{Role: provider.RoleAssistant}}
	var text strings.Builder
	tools := map[int]*toolAcc{}

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
		if payload == "[DONE]" {
			break
		}
		var ck chunk
		if err := json.Unmarshal([]byte(payload), &ck); err != nil {
			return nil, fmt.Errorf("%s: decode stream chunk: %w", c.name, err)
		}
		if ck.Usage != nil {
			res.Usage.InputTokens = ck.Usage.PromptTokens
			res.Usage.OutputTokens = ck.Usage.CompletionTokens
		}
		for _, ch := range ck.Choices {
			if ch.Delta.Content != nil && *ch.Delta.Content != "" {
				text.WriteString(*ch.Delta.Content)
				emit(provider.Event{Type: "text", Text: *ch.Delta.Content})
			}
			for _, tc := range ch.Delta.ToolCalls {
				acc := tools[tc.Index]
				if acc == nil {
					acc = &toolAcc{}
					tools[tc.Index] = acc
				}
				if tc.ID != "" {
					acc.id = tc.ID
				}
				if tc.Function.Name != "" {
					if acc.name == "" {
						emit(provider.Event{Type: "tool_start", Tool: tc.Function.Name})
					}
					acc.name = tc.Function.Name
				}
				acc.args.WriteString(tc.Function.Arguments)
			}
			if ch.FinishReason != nil && *ch.FinishReason != "" {
				res.StopReason = normalizeStop(*ch.FinishReason)
			}
		}
	}
	if err := sc.Err(); err != nil {
		return nil, fmt.Errorf("%s: read stream: %w", c.name, err)
	}

	if text.Len() > 0 {
		res.Message.Content = append(res.Message.Content, provider.Block{
			Type: provider.BlockText,
			Text: text.String(),
		})
	}
	idxs := make([]int, 0, len(tools))
	for i := range tools {
		idxs = append(idxs, i)
	}
	sort.Ints(idxs)
	for _, i := range idxs {
		acc := tools[i]
		args := acc.args.String()
		if args == "" {
			args = "{}"
		}
		res.Message.Content = append(res.Message.Content, provider.Block{
			Type:  provider.BlockToolUse,
			ID:    acc.id,
			Name:  acc.name,
			Input: json.RawMessage(args),
		})
	}
	if len(tools) > 0 {
		res.StopReason = provider.StopToolUse
	}
	return res, nil
}
