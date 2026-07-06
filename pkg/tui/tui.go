// Package tui is the interactive terminal interface over the engine.
package tui

import (
	"context"
	"fmt"
	"strings"

	"github.com/charmbracelet/bubbles/spinner"
	"github.com/charmbracelet/bubbles/textarea"
	"github.com/charmbracelet/bubbles/viewport"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/tamnd/kaku/pkg/engine"
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
}

var (
	userStyle   = lipgloss.NewStyle().Foreground(lipgloss.Color("6")).Bold(true)
	toolStyle   = lipgloss.NewStyle().Foreground(lipgloss.Color("3"))
	dimStyle    = lipgloss.NewStyle().Foreground(lipgloss.Color("8"))
	errStyle    = lipgloss.NewStyle().Foreground(lipgloss.Color("1"))
	footStyle   = lipgloss.NewStyle().Foreground(lipgloss.Color("8"))
	askStyle    = lipgloss.NewStyle().Border(lipgloss.RoundedBorder()).BorderForeground(lipgloss.Color("3")).Padding(0, 1)
	promptStyle = lipgloss.NewStyle().Foreground(lipgloss.Color("5"))
)

func newModel(ctx context.Context, rt Runtime) *model {
	ta := textarea.New()
	ta.Placeholder = "ask kaku anything, /help for commands"
	ta.Prompt = promptStyle.Render("> ")
	ta.SetHeight(2)
	ta.ShowLineNumbers = false
	ta.Focus()

	sp := spinner.New()
	sp.Spinner = spinner.MiniDot

	m := &model{
		rt:      rt,
		rootCtx: ctx,
		ta:      ta,
		spin:    sp,
		events:  make(chan tea.Msg, 256),
	}

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
	m.entries = append(m.entries, entry{kind: "user", text: raw})
	m.state = stateRunning
	if len(m.rt.Agent.Messages) == 0 && m.rt.Session != nil {
		m.rt.Session.SetTitle(raw)
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
			case "enter":
				raw := strings.TrimSpace(m.ta.Value())
				if raw == "" {
					return m, nil
				}
				m.ta.Reset()
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
			m.entries = append(m.entries, entry{kind: "error", text: msg.err.Error()})
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
	name, _, _ := strings.Cut(strings.TrimPrefix(raw, "/"), " ")
	switch name {
	case "quit", "exit", "q":
		return tea.Quit, true
	case "help":
		m.entries = append(m.entries, entry{kind: "info", text: "commands: /help, /skills, /clear, /quit · " +
			"enter sends, esc interrupts, y/a/n answer permission prompts"})
		return nil, true
	case "clear":
		m.rt.Agent.Messages = nil
		m.entries = append(m.entries, entry{kind: "info", text: "conversation cleared (transcript file keeps the history)"})
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
	if m.state == stateAsking {
		h -= 4
	}
	if h < 3 {
		h = 3
	}
	return h
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
			line = userStyle.Render("you ") + e.text
		case "assistant":
			line = e.text
		case "tool":
			line = toolStyle.Render("● ") + e.text
		case "info":
			line = dimStyle.Render(e.text)
		case "error":
			line = errStyle.Render(e.text)
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
	parts = append(parts, m.vp.View())

	if m.state == stateAsking && m.ask != nil {
		q := fmt.Sprintf("run %s(%s)?\n[y] once   [a] always allow %s   [n] deny",
			m.ask.tool, oneLine(m.ask.arg, 80), m.ask.tool)
		parts = append(parts, askStyle.Render(q))
	}

	parts = append(parts, m.ta.View())

	u := m.rt.Agent.Usage
	status := fmt.Sprintf("%s · %s · %d in / %d out tokens", m.rt.Model, m.rt.Mode, u.InputTokens, u.OutputTokens)
	if m.state == stateRunning {
		status = m.spin.View() + " working, esc interrupts · " + status
	}
	parts = append(parts, footStyle.Render(status))

	return strings.Join(parts, "\n")
}

func oneLine(s string, n int) string {
	s = strings.Join(strings.Fields(s), " ")
	if len(s) > n {
		s = s[:n] + "..."
	}
	return s
}
