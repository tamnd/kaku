package main

import (
	"path"
	"strings"

	"github.com/tamnd/kaku/pkg/tool"
)

// gateTools filters the registry down to the tools the user allows. Precedence,
// strongest first:
//
//   - --no-tools removes everything (a pure chat run).
//   - --no-builtin-tools removes the builtin tools but keeps MCP and agent.
//   - --tools is an allowlist: only tools whose name matches one of its globs
//     survive. Empty means "all".
//   - --exclude-tools and any config `tools` entry set to false remove tools by
//     name glob.
//
// Flags win over config. Gating happens before the engine ever sees the
// registry, so a gated-out tool is not offered to the model and cannot be
// asked for.
func gateTools(reg *tool.Registry, builtinNames map[string]bool, cfgTools map[string]bool, o options) {
	if o.noTools {
		for _, t := range reg.List() {
			reg.Remove(t.Name())
		}
		return
	}

	allow := splitList(o.tools)
	exclude := splitList(o.excludeTools)
	for name, on := range cfgTools {
		if !on {
			exclude = append(exclude, name)
		}
	}

	for _, t := range reg.List() {
		name := t.Name()
		switch {
		case o.noBuiltinTools && builtinNames[name]:
			reg.Remove(name)
		case len(allow) > 0 && !matchAny(allow, name):
			reg.Remove(name)
		case matchAny(exclude, name):
			reg.Remove(name)
		}
	}
}

// splitList parses a comma-separated flag value into trimmed, non-empty names.
func splitList(s string) []string {
	var out []string
	for _, p := range strings.Split(s, ",") {
		if p = strings.TrimSpace(p); p != "" {
			out = append(out, p)
		}
	}
	return out
}

// matchAny reports whether name matches any of the glob patterns. Tool names
// carry no slashes, so path.Match's glob syntax (* and ?) is a clean fit.
func matchAny(patterns []string, name string) bool {
	for _, p := range patterns {
		if p == name {
			return true
		}
		if ok, err := path.Match(p, name); err == nil && ok {
			return true
		}
	}
	return false
}
