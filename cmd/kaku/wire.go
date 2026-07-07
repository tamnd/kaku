package main

import (
	"context"
	"fmt"
	"os"
	"strings"

	"github.com/tamnd/kaku/pkg/agentdef"
	"github.com/tamnd/kaku/pkg/auth"
	"github.com/tamnd/kaku/pkg/checkpoint"
	"github.com/tamnd/kaku/pkg/compact"
	"github.com/tamnd/kaku/pkg/config"
	"github.com/tamnd/kaku/pkg/engine"
	"github.com/tamnd/kaku/pkg/hook"
	"github.com/tamnd/kaku/pkg/mcp"
	"github.com/tamnd/kaku/pkg/memory"
	"github.com/tamnd/kaku/pkg/mention"
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
	dir            string
	model          string
	provider       string
	baseURL        string
	apiKeyEnv      string
	thinking       string
	hideThinking   bool
	mode           string
	sessionID      string
	fork           string
	resume         bool
	noSession      bool
	title          string
	outputFormat   string
	maxTurns       int
	noMCP          bool
	sandbox        bool
	tools          string
	excludeTools   string
	noTools        bool
	noBuiltinTools bool
	skipPerm       bool
}

type runtime struct {
	cfg       *config.Config
	agent     *engine.Agent
	sess      *session.Session
	skills    []skill.Skill
	agents    []agentdef.Def
	setModel  func(string) error // switches the active model, set in build
	compactor *compact.Compactor
	mcpClose  func()
	mcpErrs   map[string]error
	dir       string
	cost      *config.Cost // price of the active model, nil when unpriced
	summary   string       // one-line resource summary for the TUI header
}

// modelCost returns the active model's per-million token prices and whether one
// is configured, so the footer can estimate spend.
func (r *runtime) modelCost() (in, out float64, ok bool) {
	if r.cost == nil {
		return 0, 0, false
	}
	return r.cost.Input, r.cost.Output, true
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
	// A stored credential fills in for a provider whose env var is unset, so
	// `kaku auth login` works without exporting a key.
	if store, err := auth.New(); err == nil {
		cfg.AuthLookup = store.Get
	}
	// Flag overrides apply to the flat default provider, before resolution.
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
	if o.baseURL != "" {
		cfg.BaseURL = o.baseURL
	}
	if o.apiKeyEnv != "" {
		cfg.APIKeyEnv = o.apiKeyEnv
	}
	if o.mode != "" {
		cfg.Permissions.Mode = o.mode
	}
	if o.skipPerm {
		cfg.Permissions.Mode = "auto"
	}
	if o.maxTurns > 0 {
		cfg.MaxTurns = o.maxTurns
	}

	// Resolve the model reference (may name a provider and a reasoning level)
	// into concrete settings.
	res, err := cfg.Resolve(o.model)
	if err != nil {
		return nil, err
	}
	if res.Reasoning == "" {
		res.Reasoning = cfg.Reasoning
	}
	if o.thinking != "" {
		res.Reasoning = o.thinking
	}

	prov, err := newProvider(res)
	if err != nil {
		return nil, err
	}

	base := builtin.All(dir, buildFormatter(dir, cfg.Formatter))
	reg := tool.NewRegistry(base...)
	builtinNames := map[string]bool{}
	for _, t := range base {
		builtinNames[t.Name()] = true
	}
	if o.sandbox {
		reg.Add(builtin.BashSandboxed(dir))
	}

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
	case o.noSession:
		rt.sess = st.Ephemeral()
	case o.fork != "":
		rt.sess, err = st.Fork(o.fork)
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
	if o.title != "" {
		rt.sess.SetTitle(o.title)
	}

	system := engine.DefaultSystem(dir)
	inst := memory.Instructions(dir, cfg.Instructions...)
	if inst != "" {
		system += "\n\n" + inst
	}

	smallModel := cfg.SmallModel
	if smallModel == "" {
		smallModel = res.Model
	}
	compactor := &compact.Compactor{Provider: prov, Model: smallModel, Budget: 150000, Keep: 20}
	rt.compactor = compactor

	a := &engine.Agent{
		Provider:  prov,
		Model:     res.Model,
		MaxTokens: res.MaxTokens,
		MaxTurns:  cfg.MaxTurns,
		Reasoning: res.Reasoning,
		System:    system,
		Tools:     reg,
		Perm:      pe,
		Hooks:     hookAdapter{&hook.Runner{Hooks: cfg.Hooks, Dir: dir}},
		Store:     rt.sess,
		Compact:   compactor.Maybe,
		Messages:  rt.sess.Messages(),
	}

	// Snapshot the tree before the first mutating tool call of each turn,
	// when the directory is a git repository.
	if cm, err := checkpoint.New(dir); err == nil {
		a.Snapshot = func(label string) error {
			_, err := cm.Save(oneLine(label, 72))
			return err
		}
	}

	agents := agentdef.Discover(dir)
	reg.Add(agentdef.Tool(agents, agentdef.Parent{
		Provider:  prov,
		Model:     res.Model,
		MaxTokens: res.MaxTokens,
		MaxTurns:  cfg.MaxTurns,
		Tools:     reg,
		Perm:      pe,
	}))

	// Gate tools last, so the allowlist and denylist can also reach the MCP
	// and agent tools registered above.
	gateTools(reg, builtinNames, cfg.Tools, o)

	rt.skills, _ = skill.Discover(dir, "")
	rt.agents = agents
	rt.cost = res.Cost
	rt.summary = resourceSummary(len(rt.skills), len(agents), len(cfg.MCPServers), strings.Count(inst, "# Instructions from "))
	rt.agent = a
	rt.setModel = rt.switchModel(o)
	return rt, nil
}

// resourceSummary renders the header line that shows what a session loaded, so
// a user can confirm at a glance that skills, agents, MCP servers, and memory
// files took effect.
func resourceSummary(skills, agents, mcp, memory int) string {
	return fmt.Sprintf("%s · %s · %s · %s",
		plural(skills, "skill"), plural(agents, "agent"),
		plural(mcp, "MCP server"), plural(memory, "memory file"))
}

// plural renders a count with a naive singular or plural noun.
func plural(n int, noun string) string {
	if n == 1 {
		return fmt.Sprintf("1 %s", noun)
	}
	return fmt.Sprintf("%d %ss", n, noun)
}

// buildFormatter turns the config's formatter settings into a builtin
// formatter, or nil when formatting is off.
func buildFormatter(dir string, fc config.FormatterConfig) *builtin.Formatter {
	if !fc.Enabled {
		return nil
	}
	specs := map[string]builtin.FormatSpec{}
	for name, s := range fc.Specs {
		specs[name] = builtin.FormatSpec{Disabled: s.Disabled, Command: s.Command, Extensions: s.Extensions}
	}
	return builtin.NewFormatter(dir, true, specs)
}

// headerSetter is implemented by the providers that accept extra HTTP headers.
type headerSetter interface{ SetHeaders(map[string]string) }

// newProvider constructs a provider from resolved settings, applying any
// per-provider headers.
func newProvider(res config.Resolved) (provider.Provider, error) {
	var prov provider.Provider
	switch res.API {
	case "anthropic":
		if res.APIKey == "" {
			return nil, fmt.Errorf("no API key: export the key env or set api_key in the provider config")
		}
		prov = anthropic.New(res.APIKey, res.BaseURL)
	case "openai":
		prov = openai.New(res.APIKey, res.BaseURL, "openai")
	case "responses":
		prov = responses.New(res.APIKey, res.BaseURL, "responses")
	default:
		return nil, fmt.Errorf("unknown provider %q (want anthropic, openai, or responses)", res.API)
	}
	if len(res.Headers) > 0 {
		if hs, ok := prov.(headerSetter); ok {
			hs.SetHeaders(res.Headers)
		}
	}
	return prov, nil
}

// switchModel returns the function the TUI calls to change model at runtime. It
// re-resolves the reference so a switch can cross providers, rebuilds the
// provider, and returns an error for a reference that does not resolve, so a
// typo surfaces at switch time instead of poisoning the next request.
func (r *runtime) switchModel(o options) func(string) error {
	return func(ref string) error {
		res, err := r.cfg.Resolve(ref)
		if err != nil {
			return err
		}
		if res.Reasoning == "" {
			res.Reasoning = r.cfg.Reasoning
		}
		if o.thinking != "" {
			res.Reasoning = o.thinking
		}
		prov, err := newProvider(res)
		if err != nil {
			return err
		}
		r.agent.Provider = prov
		r.agent.Model = res.Model
		r.agent.MaxTokens = res.MaxTokens
		r.agent.Reasoning = res.Reasoning
		r.cost = res.Cost
		return nil
	}
}

// startNewSession closes the current session and opens a fresh one, resetting
// the agent's history. It returns the new session so the TUI can adopt it.
func (r *runtime) startNewSession() (*session.Session, error) {
	s, err := session.NewStore(r.dir).New()
	if err != nil {
		return nil, err
	}
	if r.sess != nil {
		r.sess.Close()
	}
	r.sess = s
	r.agent.Store = s
	r.agent.Messages = nil
	return s, nil
}

// listSessions returns the saved sessions for the /sessions picker.
func (r *runtime) listSessions() ([]session.Meta, error) {
	return session.NewStore(r.dir).List()
}

// openSession closes the current session and switches to an existing one,
// loading its history into the agent. It returns the adopted session.
func (r *runtime) openSession(id string) (*session.Session, error) {
	s, err := session.NewStore(r.dir).Open(id)
	if err != nil {
		return nil, err
	}
	if r.sess != nil {
		r.sess.Close()
	}
	r.sess = s
	r.agent.Store = s
	r.agent.Messages = s.Messages()
	return s, nil
}

// deleteSession removes a saved session file.
func (r *runtime) deleteSession(id string) error {
	return session.NewStore(r.dir).Delete(id)
}

// renameSession sets the current session's title.
func (r *runtime) renameSession(title string) error {
	if r.sess == nil {
		return fmt.Errorf("no session to rename")
	}
	return r.sess.SetTitle(title)
}

// exportSession writes the current session to a file. arg is the raw command
// tail: an optional path whose extension picks the format (md, html, json),
// defaulting to <id>.md in the working directory. It returns a note for the UI.
func (r *runtime) exportSession(arg string) (string, error) {
	if r.sess == nil || r.sess.ID() == "" {
		return "", fmt.Errorf("no session to export")
	}
	file := strings.TrimSpace(arg)
	format := "md"
	if file == "" {
		file = r.sess.ID() + ".md"
	} else {
		switch {
		case strings.HasSuffix(file, ".json"):
			format = "json"
		case strings.HasSuffix(file, ".html"):
			format = "html"
		}
	}
	if err := session.NewStore(r.dir).Export(r.sess.ID(), format, file); err != nil {
		return "", err
	}
	return "exported to " + file, nil
}

// validReasoning holds the accepted reasoning levels.
var validReasoning = map[string]bool{
	"off": true, "minimal": true, "low": true, "medium": true, "high": true, "xhigh": true,
}

// setReasoning changes the live reasoning level. The provider reads it per
// request from Agent.Reasoning, so no rebuild is needed.
func (r *runtime) setReasoning(level string) error {
	if !validReasoning[level] {
		return fmt.Errorf("unknown reasoning level %q (want off, minimal, low, medium, high, xhigh)", level)
	}
	r.agent.Reasoning = level
	return nil
}

// expandSkills rewrites a leading /name invocation using the loaded skills,
// then inlines any @file mentions. A plain prompt with no leading slash still
// gets its @file mentions expanded.
func (r *runtime) expandSkills(input string) string {
	if strings.HasPrefix(input, "/") {
		name, args, _ := strings.Cut(strings.TrimPrefix(input, "/"), " ")
		if s, ok := skill.Find(r.skills, name); ok {
			r.applySkillTarget(s)
			// Skill.Expand already inlines @file mentions in the body.
			return s.Expand(strings.TrimSpace(args), r.dir)
		}
	}
	out, _ := mention.Expand(r.dir, input)
	return out
}

// applySkillTarget honors a command's model or agent frontmatter by switching
// the active model for the run. A skill can name a model directly, or an agent
// whose model override the command borrows, so /review can run on the reviewer
// agent's model in one keystroke. A reference that does not resolve is left
// alone rather than aborting the command.
func (r *runtime) applySkillTarget(s skill.Skill) {
	ref := s.Model
	if ref == "" && s.Agent != "" {
		for _, d := range r.agents {
			if d.Name == s.Agent {
				ref = d.Model
				break
			}
		}
	}
	if ref != "" && r.setModel != nil {
		_ = r.setModel(ref)
	}
}
