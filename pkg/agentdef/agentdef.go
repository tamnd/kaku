// Package agentdef turns markdown agent definitions into a tool the main
// loop can call to fan work out to subagents.
package agentdef

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"gopkg.in/yaml.v3"

	"github.com/tamnd/kaku/pkg/engine"
	"github.com/tamnd/kaku/pkg/perm"
	"github.com/tamnd/kaku/pkg/provider"
	"github.com/tamnd/kaku/pkg/tool"
)

// Def is one agent type.
type Def struct {
	Name        string
	Description string
	Model       string      // optional model override
	Tools       []string    // optional allowlist of tool names
	Allow       []perm.Rule // extra allow rules from the permission block
	Deny        []perm.Rule // extra deny rules from the permission block
	System      string      // markdown body
}

type frontmatter struct {
	Name        string            `yaml:"name"`
	Description string            `yaml:"description"`
	Model       string            `yaml:"model"`
	Tools       string            `yaml:"tools"`      // comma separated
	Permission  map[string]string `yaml:"permission"` // tool or category -> allow|ask|deny
}

// General is the builtin fallback agent type.
var General = Def{
	Name:        "general",
	Description: "General-purpose subagent for research and multi-step side tasks.",
	System: "You are a subagent of kaku, a coding agent. Complete the task you were " +
		"given using your tools, then reply with your findings. Your final message is " +
		"returned verbatim to the caller, so make it the complete answer: include " +
		"paths, code, and conclusions, and skip pleasantries.",
}

// Discover loads agent definitions from dir/.kaku/agents/*.md plus the
// builtin general type. Bad files are skipped.
func Discover(dir string) []Def {
	defs := []Def{General}
	entries, err := os.ReadDir(filepath.Join(dir, ".kaku", "agents"))
	if err != nil {
		return defs
	}
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".md") {
			continue
		}
		path := filepath.Join(dir, ".kaku", "agents", e.Name())
		data, err := os.ReadFile(path)
		if err != nil {
			continue
		}
		d, err := parse(strings.TrimSuffix(e.Name(), ".md"), data)
		if err != nil {
			continue
		}
		defs = append(defs, d)
	}
	sort.Slice(defs, func(i, j int) bool { return defs[i].Name < defs[j].Name })
	return defs
}

func parse(defaultName string, data []byte) (Def, error) {
	d := Def{Name: defaultName}
	body := data
	if bytes.HasPrefix(data, []byte("---\n")) {
		rest := data[4:]
		end := bytes.Index(rest, []byte("\n---"))
		if end >= 0 {
			var fm frontmatter
			if err := yaml.Unmarshal(rest[:end], &fm); err != nil {
				return Def{}, err
			}
			if fm.Name != "" {
				d.Name = fm.Name
			}
			d.Description = fm.Description
			d.Model = fm.Model
			for _, t := range strings.Split(fm.Tools, ",") {
				if t = strings.TrimSpace(t); t != "" {
					d.Tools = append(d.Tools, t)
				}
			}
			for tool, action := range fm.Permission {
				rules := perm.ParseRules([]string{tool})
				switch strings.ToLower(strings.TrimSpace(action)) {
				case "deny", "false":
					d.Deny = append(d.Deny, rules...)
				case "allow", "true":
					d.Allow = append(d.Allow, rules...)
				}
				// "ask" adds no rule: a subagent has nobody to ask, so it denies.
			}
			body = rest[end+4:]
		}
	}
	d.System = strings.TrimSpace(string(body))
	if d.System == "" {
		d.System = General.System
	}
	return d, nil
}

// Parent supplies everything a subagent inherits.
type Parent struct {
	Provider  provider.Provider
	Model     string
	MaxTokens int
	MaxTurns  int
	Tools     *tool.Registry
	Perm      *perm.Engine
	OnEvent   func(engine.Event)
}

// Tool builds the "agent" tool over the discovered definitions.
func Tool(defs []Def, p Parent) tool.Tool {
	var names []string
	for _, d := range defs {
		desc := d.Description
		if desc == "" {
			desc = "custom agent"
		}
		names = append(names, fmt.Sprintf("%q (%s)", d.Name, desc))
	}
	schema := fmt.Sprintf(`{
		"type": "object",
		"properties": {
			"prompt": {"type": "string", "description": "The complete task for the subagent, with all context it needs."},
			"agent_type": {"type": "string", "description": "One of: %s. Defaults to general."}
		},
		"required": ["prompt"]
	}`, strings.ReplaceAll(strings.Join(names, ", "), `"`, `\"`))

	return tool.Func{
		ToolName: "agent",
		Desc: "Delegate a self-contained task to a subagent with its own context " +
			"window and tools. Use for research or side work whose details you do not " +
			"need, only the conclusion. The subagent cannot ask questions, so the " +
			"prompt must carry everything.",
		InputSchema: json.RawMessage(schema),
		Fn: func(ctx context.Context, input json.RawMessage) (string, error) {
			var in struct {
				Prompt    string `json:"prompt"`
				AgentType string `json:"agent_type"`
			}
			if err := json.Unmarshal(input, &in); err != nil {
				return "", err
			}
			if in.AgentType == "" {
				in.AgentType = "general"
			}
			var def *Def
			for i := range defs {
				if defs[i].Name == in.AgentType {
					def = &defs[i]
					break
				}
			}
			if def == nil {
				return "", fmt.Errorf("unknown agent type %q", in.AgentType)
			}
			return runSub(ctx, *def, p, in.Prompt)
		},
	}
}

func runSub(ctx context.Context, def Def, p Parent, prompt string) (string, error) {
	// Subagents never get the agent tool back, so fan-out stays one level.
	reg := tool.NewRegistry()
	for _, t := range p.Tools.List() {
		if t.Name() == "agent" {
			continue
		}
		if len(def.Tools) > 0 && !contains(def.Tools, t.Name()) {
			continue
		}
		reg.Add(t)
	}
	model := def.Model
	if model == "" {
		model = p.Model
	}
	sub := &engine.Agent{
		Provider:  p.Provider,
		Model:     model,
		MaxTokens: p.MaxTokens,
		MaxTurns:  p.MaxTurns,
		System:    def.System,
		Tools:     reg,
		// Inherit the parent rules, but with nobody to ask, Ask denies. The
		// agent's own permission block is layered in front so its rules win;
		// deny beats allow, so an agent deny overrides an inherited allow.
		Perm: &perm.Engine{
			Mode:     p.Perm.Mode,
			Allow:    append(append([]perm.Rule{}, def.Allow...), p.Perm.Allow...),
			Deny:     append(append([]perm.Rule{}, def.Deny...), p.Perm.Deny...),
			ReadOnly: reg.ReadOnly,
		},
		OnEvent: p.OnEvent,
	}
	out, err := sub.Run(ctx, prompt)
	if err != nil {
		return "", fmt.Errorf("subagent %s: %w", def.Name, err)
	}
	return out, nil
}

func contains(ss []string, s string) bool {
	for _, x := range ss {
		if x == s {
			return true
		}
	}
	return false
}
