package main

import (
	"context"
	"encoding/json"
	"sort"
	"testing"

	"github.com/tamnd/kaku/pkg/tool"
)

func stubTool(name string) tool.Tool {
	return tool.Func{
		ToolName:    name,
		Desc:        name,
		InputSchema: json.RawMessage(`{"type":"object"}`),
		Fn:          func(context.Context, json.RawMessage) (string, error) { return "", nil },
	}
}

func names(reg *tool.Registry) []string {
	var out []string
	for _, t := range reg.List() {
		out = append(out, t.Name())
	}
	sort.Strings(out)
	return out
}

func newReg() (*tool.Registry, map[string]bool) {
	builtin := map[string]bool{"read": true, "write": true, "edit": true, "bash": true, "grep": true, "glob": true, "ls": true, "fetch": true}
	reg := tool.NewRegistry()
	for n := range builtin {
		reg.Add(stubTool(n))
	}
	reg.Add(stubTool("mcp__docs__search"))
	reg.Add(stubTool("agent"))
	return reg, builtin
}

func eq(t *testing.T, got, want []string) {
	t.Helper()
	if len(got) != len(want) {
		t.Fatalf("got %v, want %v", got, want)
	}
	for i := range got {
		if got[i] != want[i] {
			t.Fatalf("got %v, want %v", got, want)
		}
	}
}

func TestGateNoTools(t *testing.T) {
	reg, bn := newReg()
	gateTools(reg, bn, nil, options{noTools: true})
	if got := names(reg); len(got) != 0 {
		t.Fatalf("expected no tools, got %v", got)
	}
}

func TestGateAllowlist(t *testing.T) {
	reg, bn := newReg()
	gateTools(reg, bn, nil, options{tools: "read, grep ,glob"})
	eq(t, names(reg), []string{"glob", "grep", "read"})
}

func TestGateExclude(t *testing.T) {
	reg, bn := newReg()
	gateTools(reg, bn, nil, options{excludeTools: "bash,fetch"})
	eq(t, names(reg), []string{"agent", "edit", "glob", "grep", "ls", "mcp__docs__search", "read", "write"})
}

func TestGateNoBuiltin(t *testing.T) {
	reg, bn := newReg()
	gateTools(reg, bn, nil, options{noBuiltinTools: true})
	eq(t, names(reg), []string{"agent", "mcp__docs__search"})
}

func TestGateConfigGlobDisable(t *testing.T) {
	reg, bn := newReg()
	gateTools(reg, bn, map[string]bool{"mcp__*": false, "fetch": false}, options{})
	eq(t, names(reg), []string{"agent", "bash", "edit", "glob", "grep", "ls", "read", "write"})
}

func TestGateFlagBeatsConfigEnable(t *testing.T) {
	// Config enables everything implicitly; the exclude flag still removes bash.
	reg, bn := newReg()
	gateTools(reg, bn, map[string]bool{"bash": true}, options{excludeTools: "bash"})
	if got := names(reg); matchAny([]string{"bash"}, "bash") && contains(got, "bash") {
		t.Fatalf("exclude flag should remove bash, got %v", got)
	}
}

func contains(ss []string, s string) bool {
	for _, x := range ss {
		if x == s {
			return true
		}
	}
	return false
}
