// Package tui is the interactive terminal interface over the engine.
package tui

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"time"

	"github.com/charmbracelet/bubbles/spinner"
	"github.com/charmbracelet/bubbles/textarea"
	"github.com/charmbracelet/bubbles/viewport"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/glamour"
	"github.com/charmbracelet/lipgloss"

	"github.com/tamnd/kaku/pkg/compact"
	"github.com/tamnd/kaku/pkg/engine"
	"github.com/tamnd/kaku/pkg/provider"
	"github.com/tamnd/kaku/pkg/session"
	"github.com/tamnd/kaku/pkg/skill"
)

// Runtime is everything the TUI drives, assembled by the caller.
type Runtime struct {
	Agent       *engine.Agent
	Session     *session.Session
	Skills      []skill.Skill
	Expand      func(string) string
	Close       func()
	Model       string
	Mode        string
	Dir         string
	MCPFailures map[string]error

	// Summary is the one-line resource count shown under the header (skills,
	// agents, MCP servers, memory files). Cost returns the active model's per-
	// million token prices for the footer estimate; nil or ok=false hides it.
	Summary string
	Cost    func() (in, out float64, ok bool)

	// Themes is the palette set /theme chooses from; Theme is the selected
	// name. When Themes is empty the builtin dark theme is used.
	Themes map[string]Theme
	Theme  string

	// Keybinds overrides composer action keys by name (model_cycle,
	// reasoning_cycle, paste_image, editor). Unset actions keep their defaults.
	Keybinds map[string]string

	// ModelCycle is the ordered list of model refs ctrl+n steps through. Empty
	// means cycle every entry in Models. Reasoning is the starting reasoning
	// level and SetReasoning applies a new one live; nil disables /thinking.
	ModelCycle   []string
	Reasoning    string
	SetReasoning func(level string) error

	// Models is the list the /model picker offers. SwitchModel applies a
	// choice, rebuilding the provider so a cross-provider switch works and a
	// bad name fails loudly instead of poisoning the next request.
	Models      []ModelChoice
	SwitchModel func(ref string) error

	// Compact summarizes history on demand for /compact. Nil disables
	// the command.
	Compact func(ctx context.Context, msgs []provider.Message) ([]provider.Message, bool, error)

	// Session lifecycle hooks, each nil when the command is unavailable.
	// NewSession starts a fresh session and returns it; Rename sets the current
	// title; Export writes the current session and returns a note for the UI.
	NewSession func() (*session.Session, error)
	Rename     func(title string) error
	Export     func(arg string) (string, error)

	// Session picker hooks for /sessions, each nil when unavailable. Sessions
	// lists what to choose from, SwitchSession adopts a chosen one and returns
	// it, and DeleteSession removes one.
	Sessions      func() ([]session.Meta, error)
	SwitchSession func(id string) (*session.Session, error)
	DeleteSession func(id string) error
}

// Options configures Run.
type Options struct {
	Build func(ctx context.Context) (Runtime, error)
}

// Run assembles the runtime and blocks in the TUI until the user quits.
func Run(ctx context.Context, o Options) error {
	rt, err := o.Build(ctx)
	if err != nil {
		return err
	}
	if rt.Close != nil {
		defer rt.Close()
	}
	m := newModel(ctx, rt)
	_, err = tea.NewProgram(m, tea.WithContext(ctx), tea.WithAltScreen()).Run()
	return err
}

const (
	stateIdle = iota
	stateRunning
	stateAsking
)

// entry is one item in the transcript. Simple kinds (user, info, error) use
// only kind and text; assistant and thinking carry streamed markdown and
// timing; tool carries the call detail and a status the glyph reflects. The
// render cache (2087/ux/08) keeps a finished entry from re-rendering markdown on
// every spinner tick.
type entry struct {
	kind string // user, assistant, thinking, tool, info, error
	text string // raw text, markdown source, or info message

	// Tool entries.
	tool     string          // tool name, humanized at render time
	input    json.RawMessage // tool input, for the header main param
	output   string          // tool result body
	isError  bool            // result was an error
	status   toolStatus      // running, success, error, canceled
	callID   string          // matches a tool_start to its tool_end
	expanded bool            // body shows all lines rather than the budget

	// Timing, for the thinking footer and per-turn duration.
	start, end time.Time

	// Render cache, keyed so a finished entry is rendered once.
	cacheKey string
	cache    string
}

// toolStatus is the lifecycle state a tool entry's glyph reflects.
type toolStatus int

const (
	toolPending toolStatus = iota
	toolRunning
	toolSuccess
	toolFail
	toolCanceled
)

// Messages flowing from the engine goroutine into the program.
type engineEventMsg engine.Event

type askMsg struct {
	tool  string
	arg   string
	reply chan engine.Answer
}

type doneMsg struct {
	out string
	err error
}

type compactMsg struct {
	msgs    []provider.Message
	changed bool
	before  int
	err     error
}

type model struct {
	rt      Runtime
	rootCtx context.Context

	vp      viewport.Model
	ta      textarea.Model
	spin    spinner.Model
	entries []entry
	state   int
	width   int
	height  int
	ready   bool

	events chan tea.Msg
	cancel context.CancelFunc
	ask    *askMsg
	dialog *dialogState

	themes    map[string]Theme
	themeName string
	st        styles
	reasoning string
	keys      keymap

	// pendingCtx holds !cmd shell output to prepend to the next prompt.
	pendingCtx []string

	// pendingImages holds image blocks attached to the next prompt, with a
	// parallel label per image for the composer chip line.
	pendingImages []provider.Block
	pendingLabels []string

	// files is the repo file list for the @ picker, scanned on first use;
	// mention is the open picker, nil when the composer has no active @token.
	files   []string
	mention *mentionPicker

	// md renders assistant and thinking markdown, cached by width and style so a
	// stream of deltas reuses one glamour instance (2087/ux/01).
	md      *glamour.TermRenderer
	mdWidth int
	mdStyle string

	// turnStart marks when the active turn began, for the per-turn duration
	// footer (2087/ux/01).
	turnStart time.Time

	// toast is the transient notice shown over the footer, nil when none.
	// toastSeq guards its clear timer against a stale fire (2087/ux/04).
	toast    *toastState
	toastSeq int

	// showSidebar toggles the right-hand resource column (2087/ux/04). Off by
	// default and auto-hidden below a width breakpoint.
	showSidebar bool
}

func newModel(ctx context.Context, rt Runtime) *model {
	themes := rt.Themes
	if len(themes) == 0 {
		themes = LoadThemes()
	}
	name := rt.Theme
	if name == "" {
		name = "dark"
	}
	st := newStyles(pickTheme(themes, name))

	ta := textarea.New()
	ta.Placeholder = "ask kaku anything, /help for commands"
	ta.SetHeight(2)
	ta.ShowLineNumbers = false
	ta.Focus()

	sp := spinner.New()
	sp.Spinner = spinner.MiniDot

	m := &model{
		rt:        rt,
		rootCtx:   ctx,
		ta:        ta,
		spin:      sp,
		events:    make(chan tea.Msg, 256),
		themes:    themes,
		themeName: name,
		st:        st,
		reasoning: rt.Reasoning,
		keys:      newKeymap(rt.Keybinds),
	}
	m.applyReasoningPrompt()

	m.entries = append(m.entries, entry{kind: "info",
		text: fmt.Sprintf("kaku · %s · %s mode · %s", rt.Model, rt.Mode, rt.Dir)})
	if rt.Summary != "" {
		m.entries = append(m.entries, entry{kind: "info", text: rt.Summary})
	}
	for name, err := range rt.MCPFailures {
		m.entries = append(m.entries, entry{kind: "error", text: fmt.Sprintf("mcp %s: %v", name, err)})
	}
	if n := len(rt.Agent.Messages); n > 0 {
		m.entries = append(m.entries, entry{kind: "info",
			text: fmt.Sprintf("resumed session %s (%d messages)", rt.Session.ID(), n)})
	}

	rt.Agent.OnEvent = func(e engine.Event) {
		m.events <- engineEventMsg(e)
	}
	rt.Agent.Ask = func(tool, arg string) engine.Answer {
		reply := make(chan engine.Answer)
		m.events <- askMsg{tool: tool, arg: arg, reply: reply}
		return <-reply
	}
	return m
}

func (m *model) Init() tea.Cmd {
	return tea.Batch(textarea.Blink, m.spin.Tick)
}

func (m *model) waitEvent() tea.Cmd {
	return func() tea.Msg { return <-m.events }
}

func (m *model) submit(raw string) tea.Cmd {
	// Pull any @image mentions out first so they attach as image blocks rather
	// than getting inlined as binary text by the @file expander.
	cleaned := m.attachImageMentions(raw)
	input := cleaned
	if m.rt.Expand != nil {
		input = m.rt.Expand(cleaned)
	}
	if len(m.pendingCtx) > 0 {
		input = strings.Join(m.pendingCtx, "\n\n") + "\n\n" + input
		m.pendingCtx = nil
	}
	images := m.pendingImages
	display := raw
	if len(images) > 0 {
		display = strings.TrimSpace(raw + " " + imageChips(m.pendingLabels))
	}
	m.pendingImages, m.pendingLabels = nil, nil
	return m.runInput(display, input, images)
}

// runInput shows display in the transcript and sends input to the agent. The
// two differ when a command (like /init) or shell context stands in for what
// the model actually receives.
func (m *model) runInput(display, input string, images []provider.Block) tea.Cmd {
	m.entries = append(m.entries, entry{kind: "user", text: display})
	m.state = stateRunning
	m.turnStart = time.Now()
	if len(m.rt.Agent.Messages) == 0 && m.rt.Session != nil {
		m.rt.Session.SetTitle(display)
	}

	ctx, cancel := context.WithCancel(m.rootCtx)
	m.cancel = cancel
	agent := m.rt.Agent
	events := m.events
	go func() {
		out, err := agent.RunWith(ctx, input, images)
		events <- doneMsg{out: out, err: err}
	}()
	return m.waitEvent()
}

func (m *model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width, m.height = msg.Width, msg.Height
		if !m.ready {
			m.vp = viewport.New(m.transcriptWidth(), m.vpHeight())
			m.ready = true
		} else {
			m.vp.Width = m.transcriptWidth()
			m.vp.Height = m.vpHeight()
		}
		m.ta.SetWidth(msg.Width - 2)
		m.refresh()
		return m, nil

	case tea.KeyMsg:
		// An open dialog takes every key until it closes.
		if m.dialog != nil {
			cmd := m.dialogKey(msg)
			return m, cmd
		}
		// Toggling the sidebar works in any state. ctrl+o is used rather than
		// ctrl+s, which many terminals swallow as flow control (2087/ux/04).
		if msg.String() == "ctrl+o" {
			m.toggleSidebar()
			return m, nil
		}
		switch m.state {
		case stateAsking:
			switch msg.String() {
			case "y", "Y", "enter":
				m.answer(engine.Answer{Allow: true})
			case "a", "A":
				m.answer(engine.Answer{Allow: true, Always: true})
			case "n", "N", "esc":
				m.answer(engine.Answer{})
			case "ctrl+c":
				m.answer(engine.Answer{})
				if m.cancel != nil {
					m.cancel()
				}
			}
			return m, m.waitEvent()
		case stateRunning:
			switch msg.String() {
			case "esc", "ctrl+c":
				if m.cancel != nil {
					m.cancel()
					m.entries = append(m.entries, entry{kind: "info", text: "interrupting..."})
					m.refresh()
				}
			}
			return m, nil
		default: // idle
			// The @file picker, when open, takes the navigation keys.
			if m.mention != nil {
				switch msg.String() {
				case "up", "ctrl+p":
					if m.mention.cursor > 0 {
						m.mention.cursor--
					}
					m.refresh()
					return m, nil
				case "down", "ctrl+n":
					if m.mention.cursor < len(m.mention.matches)-1 {
						m.mention.cursor++
					}
					m.refresh()
					return m, nil
				case "enter", "tab":
					m.acceptMention()
					m.refresh()
					return m, nil
				case "esc":
					m.mention = nil
					m.refresh()
					return m, nil
				}
			}
			// Rebindable composer actions dispatch by name, so a config keymap
			// can move them without touching the fixed core keys below.
			switch m.keys.action(msg.String()) {
			case "model_cycle":
				cmd := m.cycleModel(1)
				m.refresh()
				return m, m.withToast(cmd)
			case "reasoning_cycle":
				m.cycleReasoning()
				m.refresh()
				return m, m.withToast(nil)
			case "paste_image":
				if note := m.pasteImage(); note != "" {
					m.notify(toastInfo, note)
				}
				m.refresh()
				return m, m.withToast(nil)
			case "editor":
				// Hand the draft to $EDITOR for a real editing buffer, then load
				// whatever comes back into the composer.
				return m, openEditor(m.ta.Value())
			}
			switch msg.String() {
			case "ctrl+c":
				return m, tea.Quit
			case "enter":
				raw := strings.TrimSpace(m.ta.Value())
				if raw == "" {
					return m, nil
				}
				m.ta.Reset()
				if strings.HasPrefix(raw, "!") {
					m.runShell(raw)
					m.refresh()
					return m, nil
				}
				if cmd, handled := m.slash(raw); handled {
					m.refresh()
					return m, m.withToast(cmd)
				}
				cmd := m.submit(raw)
				m.refresh()
				return m, m.withToast(cmd)
			}
		}
		var cmd tea.Cmd
		m.ta, cmd = m.ta.Update(msg)
		// Only the idle composer drives the @file picker.
		if m.state == stateIdle {
			m.updateMention()
			m.refresh()
		}
		return m, cmd

	case toastMsg:
		m.clearToast(msg.seq)
		return m, nil

	case engineEventMsg:
		m.applyEvent(engine.Event(msg))
		m.refresh()
		return m, m.waitEvent()

	case editorDoneMsg:
		if msg.path != "" {
			defer os.Remove(msg.path)
		}
		if msg.err != nil {
			m.entries = append(m.entries, entry{kind: "error", text: "editor: " + msg.err.Error()})
			m.refresh()
			return m, nil
		}
		data, err := os.ReadFile(msg.path)
		if err != nil {
			m.entries = append(m.entries, entry{kind: "error", text: "editor: " + err.Error()})
			m.refresh()
			return m, nil
		}
		m.ta.SetValue(strings.TrimRight(string(data), "\n"))
		m.ta.CursorEnd()
		m.refresh()
		return m, nil

	case askMsg:
		ask := msg
		m.ask = &ask
		m.state = stateAsking
		return m, nil

	case doneMsg:
		m.state = stateIdle
		m.cancel = nil
		m.closeThinking()
		m.closeAssistant()
		if msg.err != nil {
			title, body := cleanError(msg.err)
			m.showError(title, body)
			// Keep a one-line trace in scrollback for when the dialog closes.
			m.entries = append(m.entries, entry{kind: "error", text: oneLine(title+": "+body, 120)})
		} else {
			m.appendTurnFooter()
		}
		m.refresh()
		return m, nil

	case compactMsg:
		m.state = stateIdle
		m.cancel = nil
		switch {
		case msg.err != nil:
			title, body := cleanError(msg.err)
			m.showError("Compaction failed", title+"\n\n"+body)
		case !msg.changed:
			m.entries = append(m.entries, entry{kind: "info", text: "nothing to compact"})
		default:
			m.rt.Agent.Messages = msg.msgs
			if m.rt.Session != nil {
				if err := m.rt.Session.ReplaceMessages(msg.msgs); err != nil {
					m.entries = append(m.entries, entry{kind: "error", text: "saving compacted history: " + err.Error()})
				}
			}
			m.entries = append(m.entries, entry{kind: "info",
				text: fmt.Sprintf("compacted: ~%d to ~%d tokens", msg.before, compact.EstimateTokens(msg.msgs))})
		}
		m.refresh()
		return m, nil

	case spinner.TickMsg:
		var cmd tea.Cmd
		m.spin, cmd = m.spin.Update(msg)
		// A running tool's glyph is the live spinner, so repaint the transcript
		// each tick while one is in flight. A finished transcript stays quiet.
		if m.state == stateRunning {
			m.refresh()
		}
		return m, cmd
	}

	var cmd tea.Cmd
	m.vp, cmd = m.vp.Update(msg)
	return m, cmd
}

func (m *model) answer(a engine.Answer) {
	if m.ask != nil {
		m.ask.reply <- a
		m.ask = nil
	}
	m.state = stateRunning
}

// slash handles local commands. Unknown names fall through to skills, then
// to the model as a normal prompt.
func (m *model) slash(raw string) (tea.Cmd, bool) {
	if !strings.HasPrefix(raw, "/") {
		return nil, false
	}
	name, rest, _ := strings.Cut(strings.TrimPrefix(raw, "/"), " ")
	rest = strings.TrimSpace(rest)
	switch name {
	case "quit", "exit", "q":
		return tea.Quit, true
	case "help":
		m.dialog = &dialogState{kind: dlgHelp, title: "kaku commands", body: helpBody}
		return nil, true
	case "model":
		if rest == "" {
			m.openModelPicker()
			return nil, true
		}
		return m.switchModel(rest), true
	case "sidebar":
		m.toggleSidebar()
		return nil, true
	case "compact":
		return m.startCompact(), true
	case "clear":
		m.rt.Agent.Messages = nil
		m.entries = append(m.entries, entry{kind: "info", text: "conversation cleared (transcript file keeps the history)"})
		return nil, true
	case "new":
		m.newSession()
		return nil, true
	case "init":
		return m.runInput("/init", engine.InitPrompt, nil), true
	case "sessions":
		m.openSessionPicker()
		return nil, true
	case "theme":
		m.setTheme(rest)
		return nil, true
	case "thinking", "think":
		m.setReasoning(rest)
		return nil, true
	case "name", "rename":
		if rest == "" {
			m.entries = append(m.entries, entry{kind: "info", text: "usage: /name <title>"})
			return nil, true
		}
		if m.rt.Rename == nil {
			m.entries = append(m.entries, entry{kind: "info", text: "renaming is not available"})
			return nil, true
		}
		if err := m.rt.Rename(rest); err != nil {
			m.showError("Could not rename", err.Error())
			return nil, true
		}
		m.notify(toastSuccess, "session named: "+rest)
		return nil, true
	case "export":
		if m.rt.Export == nil {
			m.entries = append(m.entries, entry{kind: "info", text: "export is not available"})
			return nil, true
		}
		note, err := m.rt.Export(rest)
		if err != nil {
			m.showError("Could not export", err.Error())
			return nil, true
		}
		m.entries = append(m.entries, entry{kind: "info", text: note})
		return nil, true
	case "skills":
		if len(m.rt.Skills) == 0 {
			m.entries = append(m.entries, entry{kind: "info", text: "no skills found (.kaku/skills/*.md)"})
			return nil, true
		}
		var b strings.Builder
		b.WriteString("skills:")
		for _, s := range m.rt.Skills {
			fmt.Fprintf(&b, "\n  /%s  %s", s.Name, s.Description)
		}
		m.entries = append(m.entries, entry{kind: "info", text: b.String()})
		return nil, true
	}
	return nil, false
}

// setTheme switches the active theme, or lists the available names when the
// argument is empty or does not match one.
func (m *model) setTheme(name string) {
	if name == "" {
		m.entries = append(m.entries, entry{kind: "info", text: m.themeList()})
		return
	}
	if _, ok := m.themes[name]; !ok {
		m.entries = append(m.entries, entry{kind: "info",
			text: "no theme " + name + "\n" + m.themeList()})
		return
	}
	m.themeName = name
	m.st = newStyles(m.themes[name])
	m.ta.Prompt = m.st.prompt.Render("> ")
	m.notify(toastSuccess, "theme: "+name)
}

// themeList renders the available themes with the current one marked.
func (m *model) themeList() string {
	var b strings.Builder
	b.WriteString("themes:")
	for _, n := range themeNames(m.themes) {
		mark := "  "
		if n == m.themeName {
			mark = "› "
		}
		fmt.Fprintf(&b, "\n%s%s", mark, n)
	}
	return b.String()
}

// reasoningLevels is the cycle order for shift+tab and /thinking.
var reasoningLevels = []string{"off", "minimal", "low", "medium", "high", "xhigh"}

// applyReasoningPrompt tints the editor's left gutter by the reasoning level, a
// cheap glance at how hard the model is set to think.
func (m *model) applyReasoningPrompt() {
	c := m.reasoningColor()
	m.ta.Prompt = lipgloss.NewStyle().Foreground(c).Render("▎") + " "
}

// reasoningColor maps the current level to a theme color: muted when off,
// warmer as the level climbs.
func (m *model) reasoningColor() lipgloss.Color {
	t := m.themes[m.themeName]
	switch m.reasoning {
	case "off", "":
		return color(t.Muted)
	case "minimal", "low":
		return color(t.Secondary)
	case "medium":
		return color(t.Primary)
	default: // high, xhigh
		return color(t.Warning)
	}
}

// cycleModel steps to the next (dir=1) or previous (dir=-1) model in the cycle
// list, or the full model list when no cycle is configured.
func (m *model) cycleModel(dir int) tea.Cmd {
	cycle := m.rt.ModelCycle
	if len(cycle) == 0 {
		for _, mc := range m.rt.Models {
			cycle = append(cycle, mc.Ref)
		}
	}
	if len(cycle) == 0 {
		m.entries = append(m.entries, entry{kind: "info", text: "no models to cycle"})
		return nil
	}
	idx := -1
	for i, ref := range cycle {
		if ref == m.rt.Model || strings.HasSuffix(ref, "/"+m.rt.Model) {
			idx = i
			break
		}
	}
	next := ((idx+dir)%len(cycle) + len(cycle)) % len(cycle)
	return m.switchModel(cycle[next])
}

// cycleReasoning advances the reasoning level by one step.
func (m *model) cycleReasoning() {
	idx := 0
	for i, l := range reasoningLevels {
		if l == m.reasoning {
			idx = i + 1
			break
		}
	}
	m.setReasoning(reasoningLevels[idx%len(reasoningLevels)])
}

// setReasoning applies a reasoning level live and retints the gutter.
func (m *model) setReasoning(level string) {
	if level == "" {
		m.entries = append(m.entries, entry{kind: "info", text: "thinking: " + m.reasoningLabel()})
		return
	}
	if m.rt.SetReasoning == nil {
		m.entries = append(m.entries, entry{kind: "info", text: "changing reasoning is not available"})
		return
	}
	if err := m.rt.SetReasoning(level); err != nil {
		m.showError("Could not set reasoning", err.Error())
		return
	}
	m.reasoning = level
	m.applyReasoningPrompt()
	m.notify(toastInfo, "thinking: "+level)
}

// reasoningLabel is the current level for display, "default" when unset.
func (m *model) reasoningLabel() string {
	if m.reasoning == "" {
		return "default"
	}
	return m.reasoning
}

// newSession swaps in a fresh session and clears the transcript view.
func (m *model) newSession() {
	if m.rt.NewSession == nil {
		m.entries = append(m.entries, entry{kind: "info", text: "starting a new session is not available"})
		return
	}
	s, err := m.rt.NewSession()
	if err != nil {
		m.showError("Could not start a new session", err.Error())
		return
	}
	m.rt.Session = s
	m.entries = []entry{{kind: "info", text: "new session " + s.ID()}}
}

// runShell handles a !cmd line: it runs the rest under the shell in the
// workdir and shows the output. A single ! also feeds the output to the next
// prompt as context; a leading !! runs quietly and feeds nothing.
func (m *model) runShell(raw string) {
	body := strings.TrimPrefix(raw, "!")
	quiet := strings.HasPrefix(body, "!")
	body = strings.TrimSpace(strings.TrimPrefix(body, "!"))
	if body == "" {
		m.entries = append(m.entries, entry{kind: "info", text: "usage: !command (or !!command to run without feeding the output back)"})
		return
	}
	out := m.shell(body)
	text := "$ " + body
	if out != "" {
		text += "\n" + out
	}
	m.entries = append(m.entries, entry{kind: "info", text: text})
	if !quiet {
		m.pendingCtx = append(m.pendingCtx, text)
	}
}

// shell runs one command line under bash with a timeout and returns its
// combined output, appending the error when the command fails.
func (m *model) shell(cmdline string) string {
	ctx, cancel := context.WithTimeout(m.rootCtx, 30*time.Second)
	defer cancel()
	c := exec.CommandContext(ctx, "bash", "-lc", cmdline)
	c.Dir = m.rt.Dir
	out, err := c.CombinedOutput()
	s := strings.TrimRight(string(out), "\n")
	if err != nil {
		if s != "" {
			s += "\n"
		}
		s += err.Error()
	}
	return s
}

// openSessionPicker lists the saved sessions in a picker: enter switches, d
// deletes, esc closes.
func (m *model) openSessionPicker() {
	if m.rt.Sessions == nil {
		m.entries = append(m.entries, entry{kind: "info", text: "sessions are not available"})
		return
	}
	metas, err := m.rt.Sessions()
	if err != nil {
		m.showError("Could not list sessions", err.Error())
		return
	}
	if len(metas) == 0 {
		m.entries = append(m.entries, entry{kind: "info", text: "no sessions yet"})
		return
	}
	m.dialog = m.sessionDialog(metas)
}

// sessionDialog builds the picker state from session metadata, marking the
// active session and starting the cursor on it.
func (m *model) sessionDialog(metas []session.Meta) *dialogState {
	cur := ""
	if m.rt.Session != nil {
		cur = m.rt.Session.ID()
	}
	items := make([]dialogItem, 0, len(metas))
	cursor := 0
	for i, meta := range metas {
		title := meta.Title
		if title == "" {
			title = "(untitled)"
		}
		desc := fmt.Sprintf("%d msgs · %s", meta.Messages, meta.CreatedAt.Format("Jan 2 15:04"))
		if meta.ID == cur {
			desc += " · current"
			cursor = i
		}
		items = append(items, dialogItem{label: title, desc: desc, value: meta.ID})
	}
	return &dialogState{kind: dlgSessions, title: "Sessions", items: items, cursor: cursor, onPick: m.switchToSession}
}

// switchToSession adopts the chosen session and resets the transcript view.
func (m *model) switchToSession(id string) tea.Cmd {
	if m.rt.SwitchSession == nil {
		return nil
	}
	if m.rt.Session != nil && m.rt.Session.ID() == id {
		m.entries = append(m.entries, entry{kind: "info", text: "already on that session"})
		return nil
	}
	s, err := m.rt.SwitchSession(id)
	if err != nil {
		m.showError("Could not switch session", err.Error())
		return nil
	}
	m.rt.Session = s
	m.entries = []entry{{kind: "info",
		text: fmt.Sprintf("switched to session %s (%d messages)", s.ID(), len(m.rt.Agent.Messages))}}
	return nil
}

// deleteFromPicker removes the highlighted session and rebuilds the picker. It
// refuses to delete the active session, which would orphan the live view.
func (m *model) deleteFromPicker() tea.Cmd {
	d := m.dialog
	if d == nil || len(d.items) == 0 {
		return nil
	}
	id := d.items[d.cursor].value
	if m.rt.Session != nil && m.rt.Session.ID() == id {
		m.entries = append(m.entries, entry{kind: "info", text: "cannot delete the active session; /new first"})
		return nil
	}
	if m.rt.DeleteSession != nil {
		if err := m.rt.DeleteSession(id); err != nil {
			m.showError("Could not delete session", err.Error())
			return nil
		}
	}
	metas, err := m.rt.Sessions()
	if err != nil || len(metas) == 0 {
		m.dialog = nil
		return nil
	}
	nd := m.sessionDialog(metas)
	nd.cursor = min(d.cursor, len(nd.items)-1)
	m.dialog = nd
	return nil
}

// updateMention recomputes the @file overlay from the composer's tail token.
// It opens the picker while an @token is being typed and closes it otherwise.
func (m *model) updateMention() {
	tok := activeMention(m.ta.Value())
	if tok == "" {
		m.mention = nil
		return
	}
	if m.files == nil {
		m.files = scanFiles(m.rt.Dir)
	}
	matches := rankMentions(m.files, tok[1:])
	if len(matches) == 0 {
		m.mention = nil
		return
	}
	if len(matches) > mentionMax {
		matches = matches[:mentionMax]
	}
	if m.mention == nil {
		m.mention = &mentionPicker{}
	}
	m.mention.query = tok[1:]
	m.mention.matches = matches
	if m.mention.cursor >= len(matches) {
		m.mention.cursor = len(matches) - 1
	}
	if m.mention.cursor < 0 {
		m.mention.cursor = 0
	}
}

// acceptMention replaces the trailing @token with the highlighted path.
func (m *model) acceptMention() {
	if m.mention == nil || len(m.mention.matches) == 0 {
		return
	}
	sel := m.mention.matches[m.mention.cursor]
	val := m.ta.Value()
	i := strings.LastIndexAny(val, " \t\n")
	m.ta.SetValue(val[:i+1] + "@" + sel + " ")
	m.ta.CursorEnd()
	m.mention = nil
}

// mentionView renders the open @file overlay, or "" when the picker is closed.
func (m *model) mentionView() string {
	if m.mention == nil {
		return ""
	}
	var b strings.Builder
	b.WriteString(m.st.dialogHint.Render("@file · ↑/↓ move · enter insert · esc cancel"))
	for i, f := range m.mention.matches {
		b.WriteString("\n")
		if i == m.mention.cursor {
			b.WriteString(m.st.pick.Render("› " + f))
		} else {
			b.WriteString("  " + m.st.dim.Render(f))
		}
	}
	return b.String()
}

// startCompact summarizes the history off the UI goroutine.
func (m *model) startCompact() tea.Cmd {
	if m.rt.Compact == nil {
		m.entries = append(m.entries, entry{kind: "info", text: "compaction is not available"})
		return nil
	}
	msgs := m.rt.Agent.Messages
	if len(msgs) < 3 {
		m.entries = append(m.entries, entry{kind: "info", text: "nothing worth compacting yet"})
		return nil
	}
	m.entries = append(m.entries, entry{kind: "info", text: "compacting..."})
	m.state = stateRunning

	ctx, cancel := context.WithCancel(m.rootCtx)
	m.cancel = cancel
	compactFn := m.rt.Compact
	events := m.events
	go func() {
		out, changed, err := compactFn(ctx, msgs)
		events <- compactMsg{msgs: out, changed: changed, before: compact.EstimateTokens(msgs), err: err}
	}()
	return m.waitEvent()
}

func (m *model) applyEvent(e engine.Event) {
	switch e.Type {
	case "text":
		// A text delta closes any open thinking block and merges into the
		// current assistant turn.
		m.closeThinking()
		if n := len(m.entries); n > 0 && m.entries[n-1].kind == "assistant" && m.entries[n-1].end.IsZero() {
			m.entries[n-1].text += e.Text
		} else {
			m.entries = append(m.entries, entry{kind: "assistant", text: e.Text, start: time.Now()})
		}
	case "thinking":
		// Merge reasoning deltas into an open thinking block (2087/ux/03).
		if n := len(m.entries); n > 0 && m.entries[n-1].kind == "thinking" && m.entries[n-1].end.IsZero() {
			m.entries[n-1].text += e.Text
		} else {
			m.entries = append(m.entries, entry{kind: "thinking", text: e.Text, start: time.Now()})
		}
	case "tool_start":
		m.closeThinking()
		m.closeAssistant()
		m.entries = append(m.entries, entry{
			kind:   "tool",
			tool:   e.Tool,
			input:  e.ToolInput,
			callID: e.ToolCallID,
			status: toolRunning,
			start:  time.Now(),
		})
	case "tool_end":
		if i := m.findTool(e.ToolCallID); i >= 0 {
			m.entries[i].output = e.ToolOutput
			m.entries[i].isError = e.IsError
			m.entries[i].end = time.Now()
			if e.IsError {
				m.entries[i].status = toolFail
			} else {
				m.entries[i].status = toolSuccess
			}
		}
	case "info":
		m.entries = append(m.entries, entry{kind: "info", text: e.Text})
	}
}

// appendTurnFooter adds a muted one-line footer after a finished turn: the
// model, provider, and wall-clock duration (2087/ux/01).
func (m *model) appendTurnFooter() {
	if m.turnStart.IsZero() {
		return
	}
	dur := entryDuration(m.turnStart, time.Now())
	m.turnStart = time.Time{}
	if dur == "" {
		return
	}
	m.entries = append(m.entries, entry{kind: "info", text: glyphSuccess + " " + m.rt.Model + " · " + dur})
}

// closeThinking stamps an open thinking block finished so its footer shows and
// its render freezes.
func (m *model) closeThinking() {
	if n := len(m.entries); n > 0 && m.entries[n-1].kind == "thinking" && m.entries[n-1].end.IsZero() {
		m.entries[n-1].end = time.Now()
	}
}

// closeAssistant stamps an open assistant turn finished so its markdown gets a
// final clean render and freezes.
func (m *model) closeAssistant() {
	if n := len(m.entries); n > 0 && m.entries[n-1].kind == "assistant" && m.entries[n-1].end.IsZero() {
		m.entries[n-1].end = time.Now()
	}
}

// findTool returns the index of the running tool entry with the given call id,
// or the most recent running tool when the id is empty (providers that do not
// surface a call id), or -1.
func (m *model) findTool(callID string) int {
	for i := len(m.entries) - 1; i >= 0; i-- {
		if m.entries[i].kind != "tool" || m.entries[i].status != toolRunning {
			continue
		}
		if callID == "" || m.entries[i].callID == callID {
			return i
		}
	}
	return -1
}

func (m *model) vpHeight() int {
	h := m.height - m.ta.Height() - 2 // footer + spacing
	if m.header(m.width) != "" {
		h-- // the header takes one row above the transcript
	}
	if m.toast != nil {
		h-- // the toast bar takes one row above the footer
	}
	if m.mention != nil {
		h -= len(m.mention.matches) + 1 // overlay hint + rows
	}
	return max(h, 3)
}

// toggleSidebar flips the sidebar and resizes the transcript viewport to match,
// notifying when it cannot show because the terminal is too narrow.
func (m *model) toggleSidebar() {
	m.showSidebar = !m.showSidebar
	if m.showSidebar && m.width < sidebarBreakpoint {
		m.notify(toastInfo, "terminal too narrow for the sidebar")
	}
	if m.ready {
		m.vp.Width = m.transcriptWidth()
		m.refresh()
	}
}

func (m *model) refresh() {
	if !m.ready {
		return
	}
	width := m.transcriptWidth()
	var b strings.Builder
	for i := range m.entries {
		block := m.renderEntry(i, width)
		if block == "" {
			continue
		}
		b.WriteString(block)
		b.WriteString("\n\n")
	}
	m.vp.Height = m.vpHeight()
	m.vp.SetContent(b.String())
	m.vp.GotoBottom()
}

func (m *model) View() string {
	if !m.ready {
		return "loading..."
	}
	var parts []string
	// A header line above everything carries the wordmark and live session
	// stats; it is suppressed on a very narrow terminal (2087/ux/04).
	if h := m.header(m.width); h != "" {
		parts = append(parts, h)
	}
	// The content area shows an open dialog, then a pending permission ask,
	// otherwise the scrollable transcript, optionally beside the sidebar.
	switch {
	case m.dialog != nil:
		parts = append(parts, m.renderDialog())
	case m.state == stateAsking && m.ask != nil:
		parts = append(parts, m.renderAsk())
	case m.sidebarVisible():
		gap := strings.Repeat(" ", sidebarGap)
		row := lipgloss.JoinHorizontal(lipgloss.Top, m.vp.View(), gap, m.sidebar(m.vp.Height))
		parts = append(parts, row)
	default:
		parts = append(parts, m.vp.View())
	}

	if mv := m.mentionView(); mv != "" {
		parts = append(parts, mv)
	}
	if len(m.pendingLabels) > 0 {
		parts = append(parts, m.st.foot.Render(imageChips(m.pendingLabels)))
	}
	parts = append(parts, m.ta.View())

	// A transient toast, when present, paints a bar just above the footer.
	if tv := m.toastView(m.width); tv != "" {
		parts = append(parts, tv)
	}
	parts = append(parts, m.st.foot.Render(m.footerStatus(m.rt.Agent.Usage)))

	return strings.Join(parts, "\n")
}

// estimatedCost renders the accumulated spend from the active model's prices,
// or "" when no cost is configured. Prices are USD per million tokens.
func (m *model) estimatedCost(u provider.Usage) string {
	if m.rt.Cost == nil {
		return ""
	}
	in, out, ok := m.rt.Cost()
	if !ok {
		return ""
	}
	total := float64(u.InputTokens)/1e6*in + float64(u.OutputTokens)/1e6*out
	return formatCost(total)
}

func oneLine(s string, n int) string {
	s = strings.Join(strings.Fields(s), " ")
	if len(s) > n {
		s = s[:n] + "..."
	}
	return s
}
