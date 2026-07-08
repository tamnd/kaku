package tui

import (
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

// toastTTL is how long a transient notice stays on screen before it clears
// itself (2087/ux/04).
const toastTTL = 5 * time.Second

// toastSeverity picks a toast's glyph and color.
type toastSeverity int

const (
	toastInfo toastSeverity = iota
	toastSuccess
	toastWarn
	toastError
)

// toastState is the one transient notice currently shown, or nil when none.
// seq guards the clear timer: a newer toast bumps seq so a stale timer for an
// older toast does not clear the newer one.
type toastState struct {
	severity toastSeverity
	text     string
	seq      int
}

// toastMsg clears the toast with the matching sequence when its timer fires.
type toastMsg struct{ seq int }

// notify raises a transient toast and returns the command that clears it after
// the TTL. Callers that already return a tea.Cmd should batch this in; the
// idle-key path does so through withToast (2087/ux/04).
func (m *model) notify(sev toastSeverity, text string) tea.Cmd {
	m.toastSeq++
	m.toast = &toastState{severity: sev, text: text, seq: m.toastSeq}
	seq := m.toastSeq
	return tea.Tick(toastTTL, func(time.Time) tea.Msg { return toastMsg{seq: seq} })
}

// withToast batches a pending toast's clear timer into cmd, so a notice raised
// by a helper that could not return a command still auto-clears.
func (m *model) withToast(cmd tea.Cmd) tea.Cmd {
	if m.toast == nil {
		return cmd
	}
	seq := m.toast.seq
	tick := tea.Tick(toastTTL, func(time.Time) tea.Msg { return toastMsg{seq: seq} })
	if cmd == nil {
		return tick
	}
	return tea.Batch(cmd, tick)
}

// clearToast drops the toast if the fired timer matches the current one.
func (m *model) clearToast(seq int) {
	if m.toast != nil && m.toast.seq == seq {
		m.toast = nil
	}
}

// toastStyle returns the glyph and style for a severity.
func (m *model) toastStyle(sev toastSeverity) (string, lipgloss.Style) {
	switch sev {
	case toastSuccess:
		return glyphSuccess, m.st.toolOK
	case toastWarn:
		return "!", m.st.toolWarn
	case toastError:
		return glyphFail, m.st.toolErr
	default:
		return "•", m.st.dim
	}
}

// toastView renders the active toast as a one-line bar, collapsed to a single
// line, truncated to width, then padded so it paints a full-width strip over
// the footer. Returns "" when no toast is showing (2087/ux/04).
func (m *model) toastView(width int) string {
	if m.toast == nil {
		return ""
	}
	glyph, st := m.toastStyle(m.toast.severity)
	text := strings.Join(strings.Fields(m.toast.text), " ")
	line := glyph + " " + text
	if width > 0 {
		line = oneLine(line, width)
	}
	return st.Render(line)
}
