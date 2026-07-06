// Package perm decides whether a tool call may run.
package perm

import (
	"encoding/json"
	"path"
	"strings"
)

// Mode is the overall permission posture.
type Mode string

const (
	// ModePlan allows only read-only tools; everything else is denied.
	ModePlan Mode = "plan"
	// ModeAsk allows read-only tools and prompts for the rest.
	ModeAsk Mode = "ask"
	// ModeAuto allows everything not matched by a deny rule.
	ModeAuto Mode = "auto"
)

// Decision is the outcome of a permission check.
type Decision int

const (
	Allow Decision = iota
	Deny
	Ask
)

// Rule matches a tool call. Arg is a glob matched against the call's
// primary argument (command for bash, path for file tools, url for fetch).
// An empty Arg matches any call of the tool.
type Rule struct {
	Tool string
	Arg  string
}

// ParseRule reads the "tool" or "tool(arg-glob)" settings form.
func ParseRule(s string) Rule {
	s = strings.TrimSpace(s)
	open := strings.IndexByte(s, '(')
	if open < 0 || !strings.HasSuffix(s, ")") {
		return Rule{Tool: s}
	}
	return Rule{Tool: s[:open], Arg: s[open+1 : len(s)-1]}
}

// ParseRules reads a list of settings rule strings.
func ParseRules(ss []string) []Rule {
	out := make([]Rule, 0, len(ss))
	for _, s := range ss {
		if s = strings.TrimSpace(s); s != "" {
			out = append(out, ParseRule(s))
		}
	}
	return out
}

func (r Rule) matches(tool, arg string) bool {
	if r.Tool != tool && r.Tool != "*" {
		return false
	}
	if r.Arg == "" {
		return true
	}
	if ok, err := path.Match(r.Arg, arg); err == nil && ok {
		return true
	}
	// A trailing * should match across separators too, which path.Match
	// does not do, so fall back to a prefix check.
	if strings.HasSuffix(r.Arg, "*") && strings.HasPrefix(arg, strings.TrimSuffix(r.Arg, "*")) {
		return true
	}
	return false
}

// Engine evaluates tool calls against the mode and rules.
type Engine struct {
	Mode  Mode
	Allow []Rule
	Deny  []Rule

	// ReadOnly reports whether a tool is safe to run without asking.
	ReadOnly func(tool string) bool
}

// PrimaryArg extracts the argument rules match against. It is exported so
// interfaces can show the same value they gate on.
func PrimaryArg(input json.RawMessage) string {
	var m map[string]any
	if err := json.Unmarshal(input, &m); err != nil {
		return ""
	}
	for _, k := range []string{"command", "file_path", "path", "url", "pattern"} {
		if v, ok := m[k].(string); ok {
			return v
		}
	}
	return ""
}

// Check decides one tool call.
func (e *Engine) Check(tool string, input json.RawMessage) Decision {
	arg := PrimaryArg(input)
	for _, r := range e.Deny {
		if r.matches(tool, arg) {
			return Deny
		}
	}
	for _, r := range e.Allow {
		if r.matches(tool, arg) {
			return Allow
		}
	}
	readonly := e.ReadOnly != nil && e.ReadOnly(tool)
	switch e.Mode {
	case ModeAuto:
		return Allow
	case ModePlan:
		if readonly {
			return Allow
		}
		return Deny
	default: // ModeAsk
		if readonly {
			return Allow
		}
		return Ask
	}
}
