// Package engine runs the agent loop: model completions, tool dispatch,
// permission checks, and history maintenance.
package engine

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/tamnd/kaku/pkg/perm"
	"github.com/tamnd/kaku/pkg/provider"
	"github.com/tamnd/kaku/pkg/tool"
)

// Event is what the engine reports to its interface while running.
type Event struct {
	Type       string // "text", "thinking", "tool_start", "tool_end", "turn", "info"
	Text       string // delta for text/thinking, message for info
	Tool       string
	ToolInput  json.RawMessage
	ToolOutput string
	IsError    bool
	Usage      *provider.Usage
}

// Answer is the user's reply to a permission prompt.
type Answer struct {
	Allow  bool
	Always bool
}

// Store persists the conversation. Session satisfies it; nil disables
// persistence.
type Store interface {
	Append(provider.Message) error
	ReplaceMessages([]provider.Message) error
	AddUsage(provider.Usage) error
}

// HookResult mirrors pkg/hook's Result without importing it, so the engine
// stays free of extension packages.
type HookResult struct {
	Block   bool
	Message string
}

// Hooks runs lifecycle hooks. Nil disables them.
type Hooks interface {
	Run(ctx context.Context, event, toolName string, payload any) (HookResult, error)
}

const maxToolOutput = 30000

// Agent is one conversation loop over a provider and a toolset.
type Agent struct {
	Provider  provider.Provider
	Model     string
	MaxTokens int
	MaxTurns  int
	System    string
	Tools     *tool.Registry
	Perm      *perm.Engine

	// Ask resolves perm.Ask decisions. Nil means such calls are denied,
	// which is what headless runs want.
	Ask func(toolName, arg string) Answer

	Hooks Hooks
	Store Store

	// Compact may rewrite history before a completion, returning the new
	// slice and whether it changed anything.
	Compact func(ctx context.Context, msgs []provider.Message) ([]provider.Message, bool, error)

	// Snapshot, when set, runs once per Run before the first tool call
	// that is not read-only. A failed snapshot is reported and the run
	// continues.
	Snapshot func(label string) error

	OnEvent func(Event)

	Messages []provider.Message
	Usage    provider.Usage
}

func (a *Agent) emit(e Event) {
	if a.OnEvent != nil {
		a.OnEvent(e)
	}
}

func (a *Agent) hooks(ctx context.Context, event, toolName string, payload any) HookResult {
	if a.Hooks == nil {
		return HookResult{}
	}
	res, err := a.Hooks.Run(ctx, event, toolName, payload)
	if err != nil {
		a.emit(Event{Type: "info", Text: "hook error: " + err.Error()})
		return HookResult{}
	}
	if res.Message != "" && !res.Block {
		a.emit(Event{Type: "info", Text: res.Message})
	}
	return res
}

func (a *Agent) append(m provider.Message) error {
	a.Messages = append(a.Messages, m)
	if a.Store != nil {
		return a.Store.Append(m)
	}
	return nil
}

// Run feeds one user input through the loop and returns the final
// assistant text.
func (a *Agent) Run(ctx context.Context, input string) (string, error) {
	if res := a.hooks(ctx, "user_prompt", "", map[string]any{"prompt": input}); res.Block {
		return "", fmt.Errorf("blocked by hook: %s", res.Message)
	}
	if err := a.append(provider.Text(provider.RoleUser, input)); err != nil {
		return "", err
	}

	maxTurns := a.MaxTurns
	if maxTurns <= 0 {
		maxTurns = 80
	}
	snapped := false
	for turn := 0; turn < maxTurns; turn++ {
		if a.Compact != nil {
			msgs, changed, err := a.Compact(ctx, a.Messages)
			if err != nil {
				a.emit(Event{Type: "info", Text: "compaction failed: " + err.Error()})
			} else if changed {
				a.Messages = msgs
				if a.Store != nil {
					if err := a.Store.ReplaceMessages(msgs); err != nil {
						return "", err
					}
				}
				a.emit(Event{Type: "info", Text: "compacted conversation history"})
			}
		}

		resp, err := a.Provider.Complete(ctx, provider.Request{
			Model:     a.Model,
			System:    a.System,
			Messages:  a.Messages,
			Tools:     a.Tools.Defs(),
			MaxTokens: a.MaxTokens,
		}, func(ev provider.Event) {
			switch ev.Type {
			case "text":
				a.emit(Event{Type: "text", Text: ev.Text})
			case "thinking":
				a.emit(Event{Type: "thinking", Text: ev.Text})
			case "tool_start":
				// The definitive tool_start fires from runTool with input.
			}
		})
		if err != nil {
			return "", err
		}

		a.Usage.Add(resp.Usage)
		if a.Store != nil {
			if err := a.Store.AddUsage(resp.Usage); err != nil {
				return "", err
			}
		}
		a.emit(Event{Type: "turn", Usage: &resp.Usage})

		if err := a.append(resp.Message); err != nil {
			return "", err
		}

		uses := resp.Message.ToolUses()
		if resp.StopReason != provider.StopToolUse || len(uses) == 0 {
			a.hooks(ctx, "stop", "", map[string]any{"text": resp.Message.TextContent()})
			return resp.Message.TextContent(), nil
		}

		results := make([]provider.Block, 0, len(uses))
		for _, use := range uses {
			if ctx.Err() != nil {
				return "", ctx.Err()
			}
			if a.Snapshot != nil && !snapped && !a.Tools.ReadOnly(use.Name) {
				snapped = true
				if err := a.Snapshot(input); err != nil {
					a.emit(Event{Type: "info", Text: "checkpoint failed: " + err.Error()})
				}
			}
			out, isErr := a.runTool(ctx, use)
			results = append(results, provider.Block{
				Type:      provider.BlockToolResult,
				ToolUseID: use.ID,
				Text:      out,
				IsError:   isErr,
			})
		}
		if err := a.append(provider.Message{Role: provider.RoleUser, Content: results}); err != nil {
			return "", err
		}
	}
	return "", fmt.Errorf("gave up after %d turns", maxTurns)
}

func (a *Agent) runTool(ctx context.Context, use provider.Block) (out string, isErr bool) {
	t, ok := a.Tools.Get(use.Name)
	if !ok {
		return fmt.Sprintf("unknown tool %q", use.Name), true
	}

	arg := perm.PrimaryArg(use.Input)
	switch a.Perm.Check(use.Name, use.Input) {
	case perm.Deny:
		return fmt.Sprintf("permission denied for %s(%s)", use.Name, arg), true
	case perm.Ask:
		if a.Ask == nil {
			return fmt.Sprintf("permission required for %s(%s) and no way to ask", use.Name, arg), true
		}
		ans := a.Ask(use.Name, arg)
		if !ans.Allow {
			return fmt.Sprintf("user denied %s(%s)", use.Name, arg), true
		}
		if ans.Always {
			a.Perm.Allow = append(a.Perm.Allow, perm.Rule{Tool: use.Name})
		}
	}

	if res := a.hooks(ctx, "pre_tool", use.Name, map[string]any{
		"tool": use.Name, "input": use.Input,
	}); res.Block {
		return "blocked by hook: " + res.Message, true
	}

	a.emit(Event{Type: "tool_start", Tool: use.Name, ToolInput: use.Input})

	out, isErr = a.safeRun(ctx, t, use.Input)
	if len(out) > maxToolOutput {
		out = out[:maxToolOutput/2] + "\n... [output truncated] ...\n" + out[len(out)-maxToolOutput/3:]
	}
	if out == "" && !isErr {
		out = "(no output)"
	}

	a.hooks(ctx, "post_tool", use.Name, map[string]any{
		"tool": use.Name, "input": use.Input, "output": out, "is_error": isErr,
	})
	a.emit(Event{Type: "tool_end", Tool: use.Name, ToolOutput: out, IsError: isErr})
	return out, isErr
}

func (a *Agent) safeRun(ctx context.Context, t tool.Tool, input json.RawMessage) (out string, isErr bool) {
	defer func() {
		if r := recover(); r != nil {
			out = fmt.Sprintf("tool panicked: %v", r)
			isErr = true
		}
	}()
	out, err := t.Run(ctx, input)
	if err != nil {
		return err.Error(), true
	}
	return out, false
}
