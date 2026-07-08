package tui

import (
	"encoding/json"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

// ModelChoice is one row in the model picker. Ref is a reference the runtime
// can resolve, Label is what the user reads, Reasoning is the model's default
// thinking level, and Current marks the active model.
type ModelChoice struct {
	Ref       string
	Label     string
	Reasoning string
	Context   int // context-window size in tokens, 0 when unknown
	Current   bool
}

// Dialog kinds. A picker is a selectable list; the rest are read-and-dismiss.
const (
	dlgError    = "error"
	dlgHelp     = "help"
	dlgPicker   = "picker"
	dlgSessions = "sessions"
)

// dialogItem is one row of a picker.
type dialogItem struct {
	label string
	desc  string
	value string // carried to onPick when the row is chosen
}

// dialogState is the currently open modal, or nil when none is open. It sits
// over the transcript, takes every key while open, and closes on esc.
type dialogState struct {
	kind   string
	title  string
	body   string // error and help text; the box wraps it
	items  []dialogItem
	cursor int
	onPick func(string) tea.Cmd // picker selection, receives the row value
}

var dialogBox = lipgloss.NewStyle().Border(lipgloss.RoundedBorder()).Padding(0, 2)

// showError opens a red error dialog with a cleaned title and body.
func (m *model) showError(title, body string) {
	m.dialog = &dialogState{kind: dlgError, title: title, body: body}
}

// dialogKey routes a keypress to the open dialog and returns any command it
// produced. A picker moves its cursor and selects; other dialogs just dismiss.
func (m *model) dialogKey(msg tea.KeyMsg) tea.Cmd {
	d := m.dialog
	switch d.kind {
	case dlgPicker:
		switch msg.String() {
		case "up", "k", "ctrl+p":
			if d.cursor > 0 {
				d.cursor--
			}
		case "down", "j", "ctrl+n":
			if d.cursor < len(d.items)-1 {
				d.cursor++
			}
		case "enter":
			it := d.items[d.cursor]
			m.dialog = nil
			if d.onPick != nil {
				return d.onPick(it.value)
			}
		case "esc", "ctrl+c", "q":
			m.dialog = nil
		}
	case dlgSessions:
		switch msg.String() {
		case "up", "k", "ctrl+p":
			if d.cursor > 0 {
				d.cursor--
			}
		case "down", "j", "ctrl+n":
			if d.cursor < len(d.items)-1 {
				d.cursor++
			}
		case "enter":
			it := d.items[d.cursor]
			m.dialog = nil
			if d.onPick != nil {
				return d.onPick(it.value)
			}
		case "d":
			return m.deleteFromPicker()
		case "esc", "ctrl+c", "q":
			m.dialog = nil
		}
	default: // error, help
		switch msg.String() {
		case "esc", "enter", "q", "ctrl+c":
			m.dialog = nil
		}
	}
	return nil
}

// renderDialog draws the open dialog centered over the transcript area.
func (m *model) renderDialog() string {
	d := m.dialog
	width := min(max(m.width-8, 24), 80)

	var b strings.Builder
	b.WriteString(m.st.dialogTitle.Render(d.title))
	b.WriteString("\n\n")

	switch d.kind {
	case dlgPicker, dlgSessions:
		for i, it := range d.items {
			label := it.label
			if it.desc != "" {
				label += "  " + m.st.dialogDesc.Render(it.desc)
			}
			if i == d.cursor {
				b.WriteString(m.st.pick.Render("› " + it.label))
				if it.desc != "" {
					b.WriteString("  " + m.st.dialogDesc.Render(it.desc))
				}
			} else {
				b.WriteString("  " + label)
			}
			b.WriteString("\n")
		}
		b.WriteString("\n")
		hint := "↑/↓ move · enter select · esc cancel"
		if d.kind == dlgSessions {
			hint = "↑/↓ move · enter switch · d delete · esc cancel"
		}
		b.WriteString(m.st.dialogHint.Render(hint))
	default:
		b.WriteString(d.body)
		b.WriteString("\n\n")
		b.WriteString(m.st.dialogHint.Render("esc or enter to dismiss"))
	}

	color := m.st.borderAccent
	if d.kind == dlgError {
		color = m.st.borderError
	}
	box := dialogBox.BorderForeground(color).Width(width).Render(b.String())
	return lipgloss.Place(m.width, m.vpHeight(), lipgloss.Center, lipgloss.Center, box)
}

// renderAsk draws the permission prompt as a centered confirm dialog.
func (m *model) renderAsk() string {
	width := min(max(m.width-8, 24), 80)
	content := m.st.dialogTitle.Render("Run "+m.ask.tool+"?") + "\n\n" +
		m.st.tool.Render(oneLine(m.ask.arg, width-6)) + "\n\n" +
		m.st.dialogHint.Render("[y] once   [a] always allow   [n] deny")
	box := dialogBox.BorderForeground(m.st.borderWarn).Width(width).Render(content)
	return lipgloss.Place(m.width, m.vpHeight(), lipgloss.Center, lipgloss.Center, box)
}

// openModelPicker builds the model picker from the runtime's model list.
func (m *model) openModelPicker() {
	if len(m.rt.Models) == 0 {
		m.entries = append(m.entries, entry{kind: "info",
			text: "no models configured; add providers to ~/.kaku/config.json"})
		return
	}
	items := make([]dialogItem, 0, len(m.rt.Models))
	cursor := 0
	for i, mc := range m.rt.Models {
		desc := mc.Reasoning
		if mc.Current {
			cursor = i
			if desc != "" {
				desc += " · current"
			} else {
				desc = "current"
			}
		}
		items = append(items, dialogItem{label: mc.Label, desc: desc, value: mc.Ref})
	}
	m.dialog = &dialogState{
		kind:   dlgPicker,
		title:  "Switch model",
		items:  items,
		cursor: cursor,
		onPick: m.switchModel,
	}
}

// switchModel asks the runtime to switch, surfacing a clean error dialog on
// failure so a bad model name never silently poisons the next request.
func (m *model) switchModel(ref string) tea.Cmd {
	if m.rt.SwitchModel == nil {
		m.rt.Agent.Model = ref
		m.rt.Model = ref
		return m.notify(toastSuccess, "model set to "+ref)
	}
	if err := m.rt.SwitchModel(ref); err != nil {
		title, body := cleanError(err)
		m.showError("Could not switch to "+ref, title+"\n\n"+body)
		return nil
	}
	m.rt.Model = m.rt.Agent.Model
	return m.notify(toastSuccess, "model set to "+m.rt.Agent.Model)
}

const helpBody = `/help              show this help
/model [name]      switch model, or open the picker with no name
/skills            list available skills
/compact           summarize history to save tokens
/init              scan the repo and write a starter KAKU.md
/new               start a fresh session
/sessions          switch between saved sessions
/name <title>      rename the current session
/theme [name]      switch the color theme, or list the choices
/thinking [level]  show or set the reasoning level
/export [file]     write the session to md, html, or json
/clear             clear the conversation (the transcript file is kept)
/quit              exit kaku

!cmd runs a shell command and feeds the output back · !!cmd runs it quietly
enter send · esc interrupt · ctrl+n cycle model · shift+tab cycle thinking
y/a/n answer permission prompts`

// cleanError turns a provider error string into a short title and a readable
// body. Provider errors arrive as "openai: 404 Not Found: [{...json...}]"; we
// lift the human message out of the JSON and keep the prefix as the title.
func cleanError(err error) (title, body string) {
	raw := strings.TrimSpace(err.Error())
	if i := strings.IndexAny(raw, "{["); i >= 0 {
		head := strings.TrimRight(strings.TrimSpace(raw[:i]), ": ")
		if msg := jsonMessage(raw[i:]); msg != "" {
			if head == "" {
				head = "Error"
			}
			return head, msg
		}
	}
	return "Error", raw
}

// jsonMessage pulls a ".error.message" out of a JSON object or a one-element
// JSON array, the two shapes providers use. It returns "" when neither fits.
func jsonMessage(s string) string {
	s = strings.TrimSpace(s)
	type apiErr struct {
		Error struct {
			Message string `json:"message"`
		} `json:"error"`
		Message string `json:"message"`
	}
	if strings.HasPrefix(s, "[") {
		var arr []apiErr
		if err := json.Unmarshal([]byte(s), &arr); err == nil && len(arr) > 0 {
			if arr[0].Error.Message != "" {
				return arr[0].Error.Message
			}
			if arr[0].Message != "" {
				return arr[0].Message
			}
		}
		return ""
	}
	var obj apiErr
	if err := json.Unmarshal([]byte(s), &obj); err == nil {
		if obj.Error.Message != "" {
			return obj.Error.Message
		}
		if obj.Message != "" {
			return obj.Message
		}
	}
	return ""
}
