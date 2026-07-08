package tui

import (
	"fmt"
	"sort"
	"strings"

	"github.com/charmbracelet/lipgloss"
)

// Sidebar layout constants (2087/ux/04). The sidebar is a fixed column that
// only appears above a width breakpoint, so a narrow terminal keeps the full
// width for the transcript.
const (
	sidebarWidth      = 32
	sidebarBreakpoint = 90 // below this total width the sidebar stays hidden
	sidebarGap        = 2  // columns between the transcript and the sidebar
	sectionCap        = 6  // rows a section shows before "…and N more"
)

// sidebarVisible reports whether the sidebar should draw: toggled on and the
// terminal wide enough to spare the column.
func (m *model) sidebarVisible() bool {
	return m.showSidebar && m.width >= sidebarBreakpoint
}

// transcriptWidth is the width left for the transcript once the sidebar takes
// its column. Equal to the full width when the sidebar is hidden.
func (m *model) transcriptWidth() int {
	if m.sidebarVisible() {
		return max(m.width-sidebarWidth-sidebarGap, 20)
	}
	return max(m.width, 10)
}

// modFile is one row of the Modified Files section.
type modFile struct {
	path       string
	adds, dels int
}

// modifiedFiles derives the files changed this session from the successful
// edit and write tool calls in the transcript, aggregating add/del counts per
// path and dropping any net-zero file (2087/ux/04).
func (m *model) modifiedFiles() []modFile {
	agg := map[string]*modFile{}
	var order []string
	for i := range m.entries {
		e := &m.entries[i]
		if e.kind != "tool" || e.status != toolSuccess {
			continue
		}
		if e.tool != "edit" && e.tool != "write" {
			continue
		}
		path := toolMainParam(e.tool, e.input)
		if path == "" {
			continue
		}
		path = m.prettyPath(path)
		a, d := diffCounts(editDiff(e))
		if _, ok := agg[path]; !ok {
			agg[path] = &modFile{path: path}
			order = append(order, path)
		}
		agg[path].adds += a
		agg[path].dels += d
	}
	out := make([]modFile, 0, len(order))
	for _, p := range order {
		f := agg[p]
		if f.adds == 0 && f.dels == 0 {
			continue
		}
		out = append(out, *f)
	}
	return out
}

// diffCounts counts the add and del lines in a diff body built by editDiff.
func diffCounts(diff string) (adds, dels int) {
	if diff == "" {
		return 0, 0
	}
	for l := range strings.SplitSeq(diff, "\n") {
		switch {
		case strings.HasPrefix(l, "+"):
			adds++
		case strings.HasPrefix(l, "-"):
			dels++
		}
	}
	return adds, dels
}

// sidebar renders the right-hand column to a fixed width and the given height.
// Sections stack: session, model, then Modified Files, MCP, Skills. Each caps
// its rows and notes how many it dropped (2087/ux/04).
func (m *model) sidebar(height int) string {
	title := "untitled session"
	if m.rt.Session != nil {
		if t := m.rt.Session.Meta().Title; t != "" {
			title = t
		}
	}
	var b strings.Builder
	b.WriteString(m.st.dialogTitle.Render(oneLine(title, sidebarWidth)) + "\n")
	b.WriteString(m.st.dim.Render(oneLine(m.prettyCwd(), sidebarWidth)) + "\n\n")

	// Model block.
	b.WriteString(m.sidebarSection("Model", []string{
		"◇ " + m.rt.Model,
		m.st.dim.Render("reasoning " + m.reasoningLabel()),
	}))

	// Modified files with per-file counts.
	var files []string
	for _, f := range m.modifiedFiles() {
		counts := m.st.diffAdd.Render(fmt.Sprintf("+%d", f.adds)) + " " +
			m.st.diffDel.Render(fmt.Sprintf("-%d", f.dels))
		files = append(files, oneLine(f.path, sidebarWidth-10)+" "+counts)
	}
	b.WriteString(m.sidebarSection("Modified Files", files))

	// MCP servers, red when the runtime recorded a startup failure.
	var mcp []string
	for name, err := range m.rt.MCPFailures {
		mcp = append(mcp, m.st.err.Render(glyphFail+" "+name)+" "+m.st.dim.Render(oneLine(err.Error(), 12)))
	}
	sort.Strings(mcp)
	b.WriteString(m.sidebarSection("MCP", mcp))

	// Skills by name.
	var skills []string
	for _, s := range m.rt.Skills {
		skills = append(skills, "• "+s.Name)
	}
	sort.Strings(skills)
	b.WriteString(m.sidebarSection("Skills", skills))

	body := strings.TrimRight(b.String(), "\n")
	return lipgloss.NewStyle().Width(sidebarWidth).Height(max(height, 1)).Render(body)
}

// sidebarSection renders a titled section: a heading with a rule filling the
// width, then up to sectionCap rows, a "None" placeholder when empty, and an
// "…and N more" line when it overflows (2087/ux/04).
func (m *model) sidebarSection(title string, rows []string) string {
	head := m.st.dialogHint.Render(title)
	rule := ""
	if gap := sidebarWidth - lipgloss.Width(title) - 1; gap > 0 {
		rule = " " + m.st.dim.Render(strings.Repeat("─", gap))
	}
	var b strings.Builder
	b.WriteString(head + rule + "\n")
	if len(rows) == 0 {
		b.WriteString(m.st.dim.Render("None") + "\n\n")
		return b.String()
	}
	shown := rows
	extra := 0
	if len(rows) > sectionCap {
		shown = rows[:sectionCap]
		extra = len(rows) - sectionCap
	}
	for _, r := range shown {
		b.WriteString(r + "\n")
	}
	if extra > 0 {
		b.WriteString(m.st.dim.Render(fmt.Sprintf("…and %d more", extra)) + "\n")
	}
	b.WriteString("\n")
	return b.String()
}
