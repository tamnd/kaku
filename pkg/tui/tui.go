// Package tui is the interactive terminal interface over the engine.
package tui

import (
	"context"
	"fmt"
	"os/exec"
	"strings"
	"time"

	"github.com/charmbracelet/bubbles/spinner"
	"github.com/charmbracelet/bubbles/textarea"
	"github.com/charmbracelet/bubbles/viewport"
	tea "github.com/charmbracelet/bubbletea"
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

	// Themes is the palette set /theme chooses from; Theme is the selected
	// name. When Themes is empty the builtin dark theme is used.
	Themes map[string]Theme
	Theme  string

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

type entry struct {
	kind string // user, assistant, tool, info, error
	text string
}

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

	// pendingCtx holds !cmd shell output to prepend to the next prompt.
	pendingCtx []string
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
	}
	m.applyReasoningPrompt()

	m.entries = append(m.entries, entry{kind: "info",
		text: fmt.Sprintf("kaku · %s · %s mode · %s", rt.Model, rt.Mode, rt.Dir)})
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
	input := raw
	if m.rt.Expand != nil {
		input = m.rt.Expand(raw)
	}
	if len(m.pendingCtx) > 0 {
		input = strings.Join(m.pendingCtx, "\n\n") + "\n\n" + input
		m.pendingCtx = nil
	}
	return m.runInput(raw, input)
}

// runInput shows display in the transcript and sends input to the agent. The
// two differ when a command (like /init) or shell context stands in for what
// the model actually receives.
func (m *model) runInput(display, input string) tea.Cmd {
	m.entries = append(m.entries, entry{kind: "user", text: display})
	m.state = stateRunning
	if len(m.rt.Agent.Messages) == 0 && m.rt.Session != nil {
		m.rt.Session.SetTitle(display)
	}

	ctx, cancel := context.WithCancel(m.rootCtx)
	m.cancel = cancel
	agent := m.rt.Agent
	events := m.events
	go func() {
		out, err := agent.Run(ctx, input)
		events <- doneMsg{out: out, err: err}
	}()
	return m.waitEvent()
}

func (m *model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width, m.height = msg.Width, msg.Height
		if !m.ready {
			m.vp = viewport.New(msg.Width, m.vpHeight())
			m.ready = true
		} else {
			m.vp.Width = msg.Width
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
			switch msg.String() {
			case "ctrl+c":
				return m, tea.Quit
			case "ctrl+n":
				cmd := m.cycleModel(1)
				m.refresh()
				return m, cmd
			case "shift+tab":
				m.cycleReasoning()
				m.refresh()
				return m, nil
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
					return m, cmd
				}
				cmd := m.submit(raw)
				m.refresh()
				return m, cmd
			}
		}
		var cmd tea.Cmd
		m.ta, cmd = m.ta.Update(msg)
		return m, cmd

	case engineEventMsg:
		m.applyEvent(engine.Event(msg))
		m.refresh()
		return m, m.waitEvent()

	case askMsg:
		ask := msg
		m.ask = &ask
		m.state = stateAsking
		return m, nil

	case doneMsg:
		m.state = stateIdle
		m.cancel = nil
		if msg.err != nil {
			title, body := cleanError(msg.err)
			m.showError(title, body)
			// Keep a one-line trace in scrollback for when the dialog closes.
			m.entries = append(m.entries, entry{kind: "error", text: oneLine(title+": "+body, 120)})
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
		return m.runInput("/init", engine.InitPrompt), true
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
		m.entries = append(m.entries, entry{kind: "info", text: "session named: " + rest})
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
	m.entries = append(m.entries, entry{kind: "info", text: "theme: " + name})
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
	m.entries = append(m.entries, entry{kind: "info", text: "thinking: " + level})
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
		if n := len(m.entries); n > 0 && m.entries[n-1].kind == "assistant" {
			m.entries[n-1].text += e.Text
		} else {
			m.entries = append(m.entries, entry{kind: "assistant", text: e.Text})
		}
	case "tool_start":
		arg := oneLine(string(e.ToolInput), 100)
		m.entries = append(m.entries, entry{kind: "tool", text: fmt.Sprintf("%s(%s)", e.Tool, arg)})
	case "tool_end":
		first := oneLine(e.ToolOutput, 120)
		kind := "info"
		if e.IsError {
			kind = "error"
			first = "! " + first
		} else {
			first = "  " + first
		}
		m.entries = append(m.entries, entry{kind: kind, text: first})
	case "info":
		m.entries = append(m.entries, entry{kind: "info", text: e.Text})
	}
}

func (m *model) vpHeight() int {
	h := m.height - m.ta.Height() - 2 // footer + spacing
	return max(h, 3)
}

func (m *model) refresh() {
	if !m.ready {
		return
	}
	wrap := lipgloss.NewStyle().Width(m.width)
	var b strings.Builder
	for _, e := range m.entries {
		var line string
		switch e.kind {
		case "user":
			line = m.st.user.Render("you ") + e.text
		case "assistant":
			line = e.text
		case "tool":
			line = m.st.tool.Render("● ") + e.text
		case "info":
			line = m.st.dim.Render(e.text)
		case "error":
			line = m.st.err.Render(e.text)
		}
		b.WriteString(wrap.Render(line))
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
	// The content area shows an open dialog, then a pending permission ask,
	// otherwise the scrollable transcript.
	switch {
	case m.dialog != nil:
		parts = append(parts, m.renderDialog())
	case m.state == stateAsking && m.ask != nil:
		parts = append(parts, m.renderAsk())
	default:
		parts = append(parts, m.vp.View())
	}

	parts = append(parts, m.ta.View())

	u := m.rt.Agent.Usage
	think := ""
	if m.reasoning != "" && m.reasoning != "off" {
		think = " · think:" + m.reasoning
	}
	status := fmt.Sprintf("%s · %s%s · %d in / %d out tokens", m.rt.Model, m.rt.Mode, think, u.InputTokens, u.OutputTokens)
	if m.state == stateRunning {
		status = m.spin.View() + " working, esc interrupts · " + status
	}
	parts = append(parts, m.st.foot.Render(status))

	return strings.Join(parts, "\n")
}

func oneLine(s string, n int) string {
	s = strings.Join(strings.Fields(s), " ")
	if len(s) > n {
		s = s[:n] + "..."
	}
	return s
}
