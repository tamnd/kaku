package tui

import (
	"sort"
	"strings"

	"github.com/charmbracelet/lipgloss"
)

// logoLines is the kaku wordmark shown on the landing screen. A plain block
// banner for now; 07's logo work refines it (2087/ux/04, 07).
var logoLines = []string{
	" _         _         ",
	"| | ____ _| | ___   _",
	"| |/ / _` | |/ / | | |",
	"|   < (_| |   <| |_| |",
	"|_|\\_\\__,_|_|\\_\\\\__,_|",
}

// transcriptEmpty reports whether the session has no real turn yet, so the
// landing screen should show. Info and error entries do not count; only a user,
// assistant, or tool turn clears the landing (2087/ux/04).
func (m *model) transcriptEmpty() bool {
	for i := range m.entries {
		switch m.entries[i].kind {
		case "user", "assistant", "tool", "thinking":
			return false
		}
	}
	return true
}

// landing renders the empty-state screen: the logo over a two-column info block
// reusing the sidebar's section layout, then the resume note and any MCP
// failures (2087/ux/04).
func (m *model) landing(width int) string {
	logo := m.st.user.Render(strings.Join(logoLines, "\n"))
	tagline := m.st.dim.Render("ask kaku anything · /help for commands")

	// Left column: where and how. Right column: what is available.
	left := m.sidebarSection("Session", []string{
		"cwd " + m.prettyCwd(),
		"model ◇ " + m.rt.Model,
		"reasoning " + m.reasoningLabel(),
		"mode " + m.rt.Mode,
	})

	var skills []string
	for _, s := range m.rt.Skills {
		skills = append(skills, "• "+s.Name)
	}
	sort.Strings(skills)
	right := m.sidebarSection("Skills", skills)
	if len(m.rt.MCPFailures) > 0 {
		var mcp []string
		for name, err := range m.rt.MCPFailures {
			mcp = append(mcp, m.st.err.Render(glyphFail+" "+name)+" "+m.st.dim.Render(oneLine(err.Error(), 16)))
		}
		sort.Strings(mcp)
		right += m.sidebarSection("MCP", mcp)
	}

	cols := lipgloss.JoinHorizontal(lipgloss.Top, left, "  ", right)

	var parts []string
	parts = append(parts, logo, tagline, "", cols)
	if m.resumeNote != "" {
		parts = append(parts, m.st.dim.Render(m.resumeNote))
	}
	block := strings.Join(parts, "\n")

	// Center the block in the available width so the empty state feels composed.
	return lipgloss.NewStyle().Width(max(width, 1)).Align(lipgloss.Center).Render(block)
}
