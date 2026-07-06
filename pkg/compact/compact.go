// Package compact keeps conversation history inside the context budget by
// summarizing its older part.
package compact

import (
	"context"
	"fmt"
	"strings"

	"github.com/tamnd/kaku/pkg/provider"
)

// EstimateTokens is a chars/4 heuristic over every block, tool inputs and
// results included.
func EstimateTokens(msgs []provider.Message) int {
	chars := 0
	for _, m := range msgs {
		for _, b := range m.Content {
			chars += len(b.Text) + len(b.Input) + len(b.Name)
		}
	}
	return chars / 4
}

// Compactor summarizes history once the estimate crosses Budget.
type Compactor struct {
	Provider provider.Provider
	Model    string
	Budget   int // token estimate that triggers compaction
	Keep     int // minimum trailing messages kept verbatim
}

// Maybe returns the history unchanged while under budget. Over budget it
// replaces everything before a safe boundary with one summary message. The
// boundary must be a user message without tool_result blocks so no
// tool_use/tool_result pair is torn; when no such boundary exists the
// history is returned unchanged.
func (c *Compactor) Maybe(ctx context.Context, msgs []provider.Message) ([]provider.Message, bool, error) {
	if c.Budget <= 0 || EstimateTokens(msgs) <= c.Budget {
		return msgs, false, nil
	}
	keep := c.Keep
	if keep < 2 {
		keep = 2
	}
	cut := -1
	for i := len(msgs) - keep; i > 0; i-- {
		if isSafeBoundary(msgs[i]) {
			cut = i
			break
		}
	}
	if cut <= 0 {
		return msgs, false, nil
	}

	summary, err := c.summarize(ctx, msgs[:cut])
	if err != nil {
		return msgs, false, err
	}
	out := make([]provider.Message, 0, len(msgs)-cut+1)
	out = append(out, provider.Text(provider.RoleUser, "[conversation summary]\n"+summary))
	out = append(out, msgs[cut:]...)
	return out, true, nil
}

func isSafeBoundary(m provider.Message) bool {
	if m.Role != provider.RoleUser {
		return false
	}
	for _, b := range m.Content {
		if b.Type == provider.BlockToolResult {
			return false
		}
	}
	return true
}

func (c *Compactor) summarize(ctx context.Context, msgs []provider.Message) (string, error) {
	var b strings.Builder
	for _, m := range msgs {
		for _, blk := range m.Content {
			switch blk.Type {
			case provider.BlockText:
				fmt.Fprintf(&b, "%s: %s\n", m.Role, trim(blk.Text, 2000))
			case provider.BlockToolUse:
				fmt.Fprintf(&b, "%s used tool %s(%s)\n", m.Role, blk.Name, trim(string(blk.Input), 400))
			case provider.BlockToolResult:
				fmt.Fprintf(&b, "tool result: %s\n", trim(blk.Text, 400))
			}
		}
	}

	resp, err := c.Provider.Complete(ctx, provider.Request{
		Model: c.Model,
		System: "You compress a coding agent's conversation history. Write a dense summary " +
			"covering: the user's goal, decisions made, files created or changed, commands run " +
			"and their outcomes, the current state, and open work. Plain text, no preamble.",
		Messages:  []provider.Message{provider.Text(provider.RoleUser, b.String())},
		MaxTokens: 2000,
	}, nil)
	if err != nil {
		return "", fmt.Errorf("compact: %w", err)
	}
	return resp.Message.TextContent(), nil
}

func trim(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "..."
}
