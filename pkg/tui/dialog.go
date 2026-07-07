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
	Current   bool
}

// Dialog kinds. A picker is a selectable list; the rest are read-and-dismiss.
const (
	dlgError  = "error"
	dlgHelp   = "help"
	dlgPicker = "picker"
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

var (
	dialogBox        = lipgloss.NewStyle().Border(lipgloss.RoundedBorder()).Padding(0, 2)
	dialogTitleStyle = lipgloss.NewStyle().Bold(true)
	dialogHintStyle  = lipgloss.NewStyle().Foreground(lipgloss.Color("8"))
	dialogDescStyle  = lipgloss.NewStyle().Foreground(lipgloss.Color("8"))
	pickSelectStyle  = lipgloss.NewStyle().Foreground(lipgloss.Color("6")).Bold(true)
)

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
	b.WriteString(dialogTitleStyle.Render(d.title))
	b.WriteString("\n\n")

	switch d.kind {
	case dlgPicker:
		for i, it := range d.items {
			label := it.label
			if it.desc != "" {
				label += "  " + dialogDescStyle.Render(it.desc)
			}
			if i == d.cursor {
				b.WriteString(pickSelectStyle.Render("› " + it.label))
				if it.desc != "" {
					b.WriteString("  " + dialogDescStyle.Render(it.desc))
				}
			} else {
				b.WriteString("  " + label)
			}
			b.WriteString("\n")
		}
		b.WriteString("\n")
		b.WriteString(dialogHintStyle.Render("↑/↓ move · enter select · esc cancel"))
	default:
		b.WriteString(d.body)
		b.WriteString("\n\n")
		b.WriteString(dialogHintStyle.Render("esc or enter to dismiss"))
	}

	color := lipgloss.Color("5")
	if d.kind == dlgError {
		color = lipgloss.Color("1")
	}
	box := dialogBox.BorderForeground(color).Width(width).Render(b.String())
	return lipgloss.Place(m.width, m.vpHeight(), lipgloss.Center, lipgloss.Center, box)
}

// renderAsk draws the permission prompt as a centered confirm dialog.
func (m *model) renderAsk() string {
	width := min(max(m.width-8, 24), 80)
	content := dialogTitleStyle.Render("Run "+m.ask.tool+"?") + "\n\n" +
		toolStyle.Render(oneLine(m.ask.arg, width-6)) + "\n\n" +
		dialogHintStyle.Render("[y] once   [a] always allow   [n] deny")
	box := dialogBox.BorderForeground(lipgloss.Color("3")).Width(width).Render(content)
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
		m.entries = append(m.entries, entry{kind: "info", text: "model set to " + ref})
		return nil
	}
	if err := m.rt.SwitchModel(ref); err != nil {
		title, body := cleanError(err)
		m.showError("Could not switch to "+ref, title+"\n\n"+body)
		return nil
	}
	m.rt.Model = m.rt.Agent.Model
	m.entries = append(m.entries, entry{kind: "info", text: "model set to " + m.rt.Agent.Model})
	return nil
}

const helpBody = `/help              show this help
/model [name]      switch model, or open the picker with no name
/skills            list available skills
/compact           summarize history to save tokens
/new               start a fresh session
/name <title>      rename the current session
/export [file]     write the session to md, html, or json
/clear             clear the conversation (the transcript file is kept)
/quit              exit kaku

enter send · esc interrupt · y/a/n answer permission prompts`

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
