package main

import (
	"context"
	"fmt"
	"os"
	"strings"

	"github.com/tamnd/kaku/pkg/agentdef"
	"github.com/tamnd/kaku/pkg/compact"
	"github.com/tamnd/kaku/pkg/config"
	"github.com/tamnd/kaku/pkg/engine"
	"github.com/tamnd/kaku/pkg/hook"
	"github.com/tamnd/kaku/pkg/mcp"
	"github.com/tamnd/kaku/pkg/memory"
	"github.com/tamnd/kaku/pkg/perm"
	"github.com/tamnd/kaku/pkg/provider"
	"github.com/tamnd/kaku/pkg/provider/anthropic"
	"github.com/tamnd/kaku/pkg/provider/openai"
	"github.com/tamnd/kaku/pkg/provider/responses"
	"github.com/tamnd/kaku/pkg/session"
	"github.com/tamnd/kaku/pkg/skill"
	"github.com/tamnd/kaku/pkg/tool"
	"github.com/tamnd/kaku/pkg/tool/builtin"
)

type options struct {
	dir       string
	model     string
	provider  string
	baseURL   string
	apiKeyEnv string
	mode      string
	sessionID string
	resume    bool
	maxTurns  int
	noMCP     bool
}

type runtime struct {
	cfg      *config.Config
	agent    *engine.Agent
	sess     *session.Session
	skills   []skill.Skill
	mcpClose func()
	mcpErrs  map[string]error
	dir      string
}

func (r *runtime) close() {
	if r.mcpClose != nil {
		r.mcpClose()
	}
	if r.sess != nil {
		r.sess.Close()
	}
}

type hookAdapter struct{ r *hook.Runner }

func (h hookAdapter) Run(ctx context.Context, event, toolName string, payload any) (engine.HookResult, error) {
	res, err := h.r.Run(ctx, event, toolName, payload)
	return engine.HookResult{Block: res.Block, Message: res.Message}, err
}

func build(ctx context.Context, o options) (*runtime, error) {
	dir := o.dir
	if dir == "" {
		var err error
		if dir, err = os.Getwd(); err != nil {
			return nil, err
		}
	}

	cfg, err := config.Load(dir)
	if err != nil {
		return nil, err
	}
	if o.provider != "" {
		cfg.Provider = o.provider
		// Switching provider by flag without an explicit key env should
		// not keep the other provider's default.
		if o.apiKeyEnv == "" {
			switch o.provider {
			case "openai", "responses":
				cfg.APIKeyEnv = "OPENAI_API_KEY"
			case "anthropic":
				cfg.APIKeyEnv = "ANTHROPIC_API_KEY"
			}
		}
	}
	if o.model != "" {
		cfg.Model = o.model
	}
	if o.baseURL != "" {
		cfg.BaseURL = o.baseURL
	}
	if o.apiKeyEnv != "" {
		cfg.APIKeyEnv = o.apiKeyEnv
	}
	if o.mode != "" {
		cfg.Permissions.Mode = o.mode
	}
	if o.maxTurns > 0 {
		cfg.MaxTurns = o.maxTurns
	}

	var prov provider.Provider
	switch cfg.Provider {
	case "anthropic":
		if cfg.APIKey() == "" {
			return nil, fmt.Errorf("no API key: export %s or set api_key_env in ~/.kaku/config.json", cfg.APIKeyEnv)
		}
		prov = anthropic.New(cfg.APIKey(), cfg.BaseURL)
	case "openai":
		prov = openai.New(cfg.APIKey(), cfg.BaseURL, "openai")
	case "responses":
		prov = responses.New(cfg.APIKey(), cfg.BaseURL, "responses")
	default:
		return nil, fmt.Errorf("unknown provider %q (want anthropic, openai, or responses)", cfg.Provider)
	}

	reg := tool.NewRegistry(builtin.All(dir)...)

	rt := &runtime{cfg: cfg, dir: dir}
	if !o.noMCP && len(cfg.MCPServers) > 0 {
		rt.mcpClose, rt.mcpErrs = mcp.Register(ctx, cfg.MCPServers, reg)
	}

	pe := &perm.Engine{
		Mode:     perm.Mode(cfg.Permissions.Mode),
		Allow:    perm.ParseRules(cfg.Permissions.Allow),
		Deny:     perm.ParseRules(cfg.Permissions.Deny),
		ReadOnly: reg.ReadOnly,
	}

	st := session.NewStore(dir)
	switch {
	case o.sessionID != "":
		rt.sess, err = st.Open(o.sessionID)
	case o.resume:
		var meta session.Meta
		if meta, err = st.Latest(); err == nil {
			rt.sess, err = st.Open(meta.ID)
		}
	default:
		rt.sess, err = st.New()
	}
	if err != nil {
		return nil, err
	}

	system := engine.DefaultSystem(dir)
	if inst := memory.Instructions(dir); inst != "" {
		system += "\n\n" + inst
	}

	smallModel := cfg.SmallModel
	if smallModel == "" {
		smallModel = cfg.Model
	}
	compactor := &compact.Compactor{Provider: prov, Model: smallModel, Budget: 150000, Keep: 20}

	a := &engine.Agent{
		Provider:  prov,
		Model:     cfg.Model,
		MaxTokens: cfg.MaxTokens,
		MaxTurns:  cfg.MaxTurns,
		System:    system,
		Tools:     reg,
		Perm:      pe,
		Hooks:     hookAdapter{&hook.Runner{Hooks: cfg.Hooks, Dir: dir}},
		Store:     rt.sess,
		Compact:   compactor.Maybe,
		Messages:  rt.sess.Messages(),
	}

	reg.Add(agentdef.Tool(agentdef.Discover(dir), agentdef.Parent{
		Provider:  prov,
		Model:     cfg.Model,
		MaxTokens: cfg.MaxTokens,
		MaxTurns:  cfg.MaxTurns,
		Tools:     reg,
		Perm:      pe,
	}))

	rt.skills, _ = skill.Discover(dir, "")
	rt.agent = a
	return rt, nil
}

// expandSkills rewrites a leading /name invocation using the loaded skills.
func (r *runtime) expandSkills(input string) string {
	if !strings.HasPrefix(input, "/") {
		return input
	}
	name, args, _ := strings.Cut(strings.TrimPrefix(input, "/"), " ")
	if s, ok := skill.Find(r.skills, name); ok {
		return s.Expand(strings.TrimSpace(args))
	}
	return input
}
