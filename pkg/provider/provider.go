// Package provider defines the model provider SPI and the message types
// shared across the agent.
package provider

import (
	"context"
	"encoding/json"
	"strings"
)

// Message roles.
const (
	RoleUser      = "user"
	RoleAssistant = "assistant"
)

// Block types.
const (
	BlockText       = "text"
	BlockThinking   = "thinking"
	BlockToolUse    = "tool_use"
	BlockToolResult = "tool_result"
	BlockImage      = "image"
)

// Block is one content block inside a message.
type Block struct {
	Type string `json:"type"`

	// Text carries the content for text and thinking blocks, and the
	// result payload for tool_result blocks.
	Text string `json:"text,omitempty"`

	// Signature is the opaque token that authenticates a thinking block so
	// it can be sent back in a later turn. Only set on thinking blocks.
	Signature string `json:"signature,omitempty"`

	// Tool use fields.
	ID    string          `json:"id,omitempty"`
	Name  string          `json:"name,omitempty"`
	Input json.RawMessage `json:"input,omitempty"`

	// Tool result fields.
	ToolUseID string `json:"tool_use_id,omitempty"`
	IsError   bool   `json:"is_error,omitempty"`

	// Image fields. MediaType is a MIME type like "image/png"; Data is the raw
	// image bytes encoded as standard base64, with no "data:" prefix.
	MediaType string `json:"media_type,omitempty"`
	Data      string `json:"data,omitempty"`
}

// Message is one turn in the conversation.
type Message struct {
	Role    string  `json:"role"`
	Content []Block `json:"content"`
}

// Text builds a plain text message.
func Text(role, text string) Message {
	return Message{Role: role, Content: []Block{{Type: BlockText, Text: text}}}
}

// Image builds an image content block from a MIME type and base64-encoded data.
func Image(mediaType, base64Data string) Block {
	return Block{Type: BlockImage, MediaType: mediaType, Data: base64Data}
}

// TextContent returns the concatenated text blocks of the message.
func (m Message) TextContent() string {
	var s strings.Builder
	for _, b := range m.Content {
		if b.Type == BlockText {
			s.WriteString(b.Text)
		}
	}
	return s.String()
}

// ToolUses returns the tool_use blocks of the message.
func (m Message) ToolUses() []Block {
	var out []Block
	for _, b := range m.Content {
		if b.Type == BlockToolUse {
			out = append(out, b)
		}
	}
	return out
}

// ToolDef describes a tool offered to the model.
type ToolDef struct {
	Name        string
	Description string
	Schema      json.RawMessage // JSON Schema for the input object
}

// Request is a single completion call.
type Request struct {
	Model       string
	System      string
	Messages    []Message
	Tools       []ToolDef
	MaxTokens   int
	Reasoning   string  // off|minimal|low|medium|high|xhigh; "" means provider default
	Temperature float64 // sampling temperature; 0 means leave it to the provider
	TopP        float64 // nucleus sampling; 0 means leave it to the provider
}

// Usage reports token counts for one completion.
type Usage struct {
	InputTokens  int `json:"input_tokens"`
	OutputTokens int `json:"output_tokens"`
}

// Add accumulates another usage sample.
func (u *Usage) Add(o Usage) {
	u.InputTokens += o.InputTokens
	u.OutputTokens += o.OutputTokens
}

// Stop reasons, normalized across providers.
const (
	StopEndTurn   = "end_turn"
	StopToolUse   = "tool_use"
	StopMaxTokens = "max_tokens"
)

// Response is the final result of one completion.
type Response struct {
	Message    Message
	StopReason string
	Usage      Usage
}

// Event is a streaming callback payload.
type Event struct {
	Type string // "text", "thinking", "tool_start"
	Text string // delta for text and thinking events
	Tool string // tool name for tool_start events
}

// Provider turns a Request into a Response, streaming deltas through the
// callback when it is non-nil.
type Provider interface {
	Name() string
	Complete(ctx context.Context, req Request, on func(Event)) (*Response, error)
}
