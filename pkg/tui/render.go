package tui

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/charmbracelet/glamour"
	"github.com/charmbracelet/lipgloss"
)

// Named glyphs (2087/ux/07). One place to swap the aesthetic or drop to ASCII.
const (
	glyphRunning  = "●"
	glyphSuccess  = "✓"
	glyphFail     = "✗"
	glyphCanceled = "⊘"
	glyphPending  = "◌"
	glyphThink    = "…"
)

// collapsedLines is the body budget a tool entry shows before the
// "N lines hidden" affordance (2087/ux/02).
const collapsedLines = 10

// markdown renders src as markdown at the given width, caching the renderer so a
// stream of deltas reuses one glamour instance. On any render error it returns
// the source unchanged so text is never lost.
func (m *model) markdown(src string, width int) string {
	if width < 10 {
		width = 10
	}
	if m.md == nil || m.mdWidth != width || m.mdStyle != m.glamourStyle() {
		r, err := glamour.NewTermRenderer(
			glamour.WithStandardStyle(m.glamourStyle()),
			glamour.WithWordWrap(width),
		)
		if err != nil {
			return src
		}
		m.md = r
		m.mdWidth = width
		m.mdStyle = m.glamourStyle()
	}
	out, err := m.md.Render(src)
	if err != nil {
		return src
	}
	return strings.Trim(out, "\n")
}

// glamourStyle picks a markdown style from the active theme: light themes get
// the light palette, everything else the dark one.
func (m *model) glamourStyle() string {
	if m.themeName == "light" {
		return "light"
	}
	return "dark"
}

// renderEntry renders entry i at the given width, updating its cache in place so
// a finished entry is rendered once and reused across spinner ticks. Returns the
// rendered block, without a trailing newline.
func (m *model) renderEntry(i, width int) string {
	e := &m.entries[i]
	key := e.cacheFor(width)
	if key != "" && key == e.cacheKey {
		return e.cache
	}
	out := m.paintEntry(e, width)
	// Only cache a stable entry. A running tool or a still-streaming turn keeps
	// re-rendering; caching it would freeze a spinner or a partial stream.
	if key != "" {
		e.cacheKey = key
		e.cache = out
	}
	return out
}

// cacheFor returns a cache key for an entry, or "" when the entry is still
// changing and must not be cached (2087/ux/08). The key folds in every input
// that changes the render: width, content length, expansion, status.
func (e *entry) cacheFor(width int) string {
	switch e.kind {
	case "tool":
		if e.status == toolRunning || e.status == toolPending {
			return "" // glyph animates; never freeze
		}
		exp := 0
		if e.expanded {
			exp = 1
		}
		return fmt.Sprintf("tool|%d|%d|%d|%d|%d", width, e.status, len(e.output), len(e.text), exp)
	case "assistant", "thinking":
		if e.end.IsZero() {
			return "" // still streaming
		}
		return fmt.Sprintf("%s|%d|%d", e.kind, width, len(e.text))
	default:
		return fmt.Sprintf("%s|%d|%d", e.kind, width, len(e.text))
	}
}

// paintEntry renders one entry by kind. This is the per-type dispatch the item
// model calls for (2087/ux/01); simple kinds stay one styled line, assistant and
// thinking render markdown, tool renders a header plus a collapsible body.
func (m *model) paintEntry(e *entry, width int) string {
	switch e.kind {
	case "user":
		return m.st.user.Render("you ") + e.text
	case "assistant":
		if strings.TrimSpace(e.text) == "" {
			return ""
		}
		return m.markdown(e.text, width)
	case "thinking":
		return m.renderThinking(e, width)
	case "tool":
		return m.renderTool(e, width)
	case "info":
		return m.st.dim.Render(e.text)
	case "error":
		return m.renderError(e.text, width)
	}
	return e.text
}

// renderThinking renders a reasoning block in a bordered box with a quieter
// style, plus a "Thought for Xs" footer once it finishes (2087/ux/03).
func (m *model) renderThinking(e *entry, width int) string {
	body := strings.TrimSpace(e.text)
	if body == "" {
		body = "thinking" + glyphThink
	}
	// Keep the box narrower than the content width; the border eats a column.
	box := m.st.thinkBox.Width(width - 3).Render(body)
	if !e.end.IsZero() && !e.start.IsZero() {
		d := e.end.Sub(e.start).Seconds()
		box += "\n" + m.st.thinkFoot.Render(fmt.Sprintf("Thought for %.1fs", d))
	}
	return box
}

// renderError renders an error entry with a bold ERROR tag and the message
// wrapped below at full width (2087/ux/02).
func (m *model) renderError(text string, width int) string {
	tag := m.st.errTag.Render("ERROR")
	wrap := lipgloss.NewStyle().Width(width)
	return tag + " " + wrap.Render(m.st.err.Render(text))
}

// renderTool renders a tool call: a status glyph, a humanized header with the
// main parameter, and a collapsible body (2087/ux/02).
func (m *model) renderTool(e *entry, width int) string {
	glyph, gs := m.toolGlyph(e)
	header := gs.Render(glyph) + " " + m.st.tool.Render(prettyToolName(e.tool))
	if main := toolMainParam(e.tool, e.input); main != "" {
		if looksLikePath(main) {
			main = m.prettyPath(main)
		}
		header += " " + oneLine(main, width-len(glyph)-len(e.tool)-4)
	}
	// Match-list tools carry their count in the header so the row reads at a
	// glance without expanding (2087/ux/02).
	if n := matchCount(e); n >= 0 {
		header += " " + m.st.dim.Render(fmt.Sprintf("(%d)", n))
	}

	body := m.toolBody(e, width)
	if body == "" {
		return header
	}
	return header + "\n" + body
}

// prettyPath shortens a path for a header: cwd-relative when under the working
// dir, else home-relative with a leading ~ (2087/ux/02).
func (m *model) prettyPath(p string) string {
	if dir := m.rt.Dir; dir != "" {
		if rel, err := filepath.Rel(dir, p); err == nil && !strings.HasPrefix(rel, "..") {
			return rel
		}
	}
	if home, err := os.UserHomeDir(); err == nil && home != "" && strings.HasPrefix(p, home) {
		return "~" + strings.TrimPrefix(p, home)
	}
	return p
}

// looksLikePath reports whether a header param reads as a filesystem path rather
// than a command, url, or query, so only paths get home-relativized.
func looksLikePath(s string) bool {
	if s == "" || strings.Contains(s, "://") || strings.ContainsAny(s, " \t\n") {
		return false
	}
	return strings.HasPrefix(s, "/") || strings.HasPrefix(s, "./") ||
		strings.HasPrefix(s, "~") || strings.Contains(s, "/")
}

// matchCount returns the number of result lines for a match-list tool (glob,
// grep, ls) once it has finished, or -1 when the count does not apply.
func matchCount(e *entry) int {
	if e.status != toolSuccess {
		return -1
	}
	switch e.tool {
	case "glob", "grep", "ls":
		out := strings.TrimRight(e.output, "\n")
		if out == "" {
			return 0
		}
		return strings.Count(out, "\n") + 1
	}
	return -1
}

// toolGlyph returns the status glyph and its style for a tool entry.
func (m *model) toolGlyph(e *entry) (string, lipgloss.Style) {
	switch e.status {
	case toolSuccess:
		return glyphSuccess, m.st.toolOK
	case toolFail:
		return glyphFail, m.st.toolErr
	case toolCanceled:
		return glyphCanceled, m.st.dim
	case toolPending:
		return glyphPending, m.st.toolWarn
	default: // running
		return m.spin.View(), m.st.toolRunning
	}
}

// toolBody renders a tool result body, collapsed to a line budget with a
// "N lines hidden" affordance unless expanded. A running tool with no output
// yet shows a placeholder so the row never looks stuck.
func (m *model) toolBody(e *entry, width int) string {
	out := strings.TrimRight(e.output, "\n")
	if out == "" {
		switch e.status {
		case toolPending:
			return m.st.dim.Render("  Requesting permission…")
		case toolRunning:
			return m.st.dim.Render("  Waiting for tool response…")
		case toolCanceled:
			return m.st.dim.Render("  Canceled.")
		}
		return ""
	}

	// Pick a body renderer by tool and by detected content type: edits and
	// writes build a colored diff from their input so the change is visible even
	// though the tool only returns a status line; diff-shaped output is colored
	// directly; reads are line-numbered code; JSON is pretty-printed; markdown is
	// rendered; everything else is plain (2087/ux/02).
	var lines []string
	switch {
	case e.tool == "edit" || e.tool == "write":
		diff := editDiff(e)
		if diff == "" {
			// No parseable input; fall back to the status line.
			lines = m.plainLines(out, width)
			break
		}
		lines = m.diffLines(diff, width)
		if h := diffCountHeader(m, diff); h != "" {
			lines = append([]string{h}, lines...)
		}
	case looksLikeDiff(out):
		lines = m.diffLines(out, width)
		if h := diffCountHeader(m, out); h != "" {
			lines = append([]string{h}, lines...)
		}
	case e.tool == "read" || e.tool == "view":
		lines = m.codeLines(out, width)
	case looksLikeJSON(out):
		lines = m.codeLines(prettyJSON(out), width)
	case looksLikeMarkdown(e.tool, out):
		lines = strings.Split(m.markdown(out, width-2), "\n")
	default:
		lines = m.plainLines(out, width)
	}

	if e.isError {
		// A user-denied permission is an expected outcome, so it reads as a
		// softer WARN rather than an ERROR (2087/ux/02).
		if isDenial(out) {
			tag := m.st.warnTag.Render("WARN")
			lines = append([]string{tag + " " + m.st.dim.Render(oneLine(out, width-5))}, lines...)
		} else {
			tag := m.st.errTag.Render("ERROR")
			lines = append([]string{tag + " " + m.st.err.Render(oneLine(out, width-6))}, lines...)
		}
	}

	hidden := 0
	if !e.expanded && len(lines) > collapsedLines {
		hidden = len(lines) - collapsedLines
		lines = lines[:collapsedLines]
	}
	body := strings.Join(lines, "\n")
	if hidden > 0 {
		body += "\n" + m.st.dim.Render(fmt.Sprintf("  … (%d lines hidden, space to expand)", hidden))
	}
	return body
}

// plainLines splits and indents a plain-text body, truncating each line to the
// width so a wide result never breaks the layout.
func (m *model) plainLines(out string, width int) []string {
	raw := strings.Split(out, "\n")
	lines := make([]string, len(raw))
	for i, l := range raw {
		lines[i] = m.st.dim.Render("  " + oneLine(l, width-2))
	}
	return lines
}

// codeLines renders a file body with right-aligned line numbers.
func (m *model) codeLines(out string, width int) []string {
	raw := strings.Split(out, "\n")
	gutter := len(strconv.Itoa(len(raw)))
	lines := make([]string, len(raw))
	for i, l := range raw {
		num := m.st.lineNum.Render(fmt.Sprintf("%*d ", gutter, i+1))
		lines[i] = num + oneLine(l, width-gutter-2)
	}
	return lines
}

// diffLines colors the +/- lines of a unified diff body.
func (m *model) diffLines(out string, width int) []string {
	raw := strings.Split(out, "\n")
	lines := make([]string, 0, len(raw))
	for _, l := range raw {
		t := oneLine(l, width-1)
		switch {
		case strings.HasPrefix(l, "+"):
			lines = append(lines, m.st.diffAdd.Render(t))
		case strings.HasPrefix(l, "-"):
			lines = append(lines, m.st.diffDel.Render(t))
		case strings.HasPrefix(l, "@@") || strings.HasPrefix(l, "diff "):
			lines = append(lines, m.st.diffHead.Render(t))
		default:
			lines = append(lines, m.st.dim.Render(t))
		}
	}
	return lines
}

// editDiff builds a unified-style diff body from an edit or write tool's input:
// edit shows old_string lines as removals and new_string lines as additions;
// write diffs the whole content against nothing. Returns "" when the input has
// no diffable fields so the caller falls back to the status line (2087/ux/02).
func editDiff(e *entry) string {
	if len(e.input) == 0 {
		return ""
	}
	var in struct {
		OldString string `json:"old_string"`
		NewString string `json:"new_string"`
		Content   string `json:"content"`
	}
	if json.Unmarshal(e.input, &in) != nil {
		return ""
	}
	var b strings.Builder
	if e.tool == "write" {
		for line := range strings.SplitSeq(strings.TrimRight(in.Content, "\n"), "\n") {
			b.WriteString("+" + line + "\n")
		}
		return strings.TrimRight(b.String(), "\n")
	}
	if in.OldString == "" && in.NewString == "" {
		return ""
	}
	for line := range strings.SplitSeq(strings.TrimRight(in.OldString, "\n"), "\n") {
		b.WriteString("-" + line + "\n")
	}
	for line := range strings.SplitSeq(strings.TrimRight(in.NewString, "\n"), "\n") {
		b.WriteString("+" + line + "\n")
	}
	return strings.TrimRight(b.String(), "\n")
}

// diffCountHeader returns a styled "+adds -dels" line for a diff body, or ""
// when nothing was added or removed (2087/ux/02).
func diffCountHeader(m *model, out string) string {
	adds, dels := 0, 0
	for l := range strings.SplitSeq(out, "\n") {
		switch {
		case strings.HasPrefix(l, "+") && !strings.HasPrefix(l, "+++"):
			adds++
		case strings.HasPrefix(l, "-") && !strings.HasPrefix(l, "---"):
			dels++
		}
	}
	if adds == 0 && dels == 0 {
		return ""
	}
	return m.st.diffAdd.Render(fmt.Sprintf("+%d", adds)) + " " +
		m.st.diffDel.Render(fmt.Sprintf("-%d", dels))
}

// looksLikeJSON reports whether a body is a single JSON object or array, so it
// can be pretty-printed rather than shown as one dense line.
func looksLikeJSON(s string) bool {
	t := strings.TrimSpace(s)
	if len(t) < 2 {
		return false
	}
	if !(t[0] == '{' || t[0] == '[') {
		return false
	}
	return json.Valid([]byte(t))
}

// prettyJSON indents a JSON body two spaces. It assumes looksLikeJSON already
// passed, so a re-marshal error just returns the input unchanged.
func prettyJSON(s string) string {
	var v any
	if json.Unmarshal([]byte(s), &v) != nil {
		return s
	}
	b, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return s
	}
	return string(b)
}

// looksLikeMarkdown reports whether a fetch/web body should render as markdown.
// Only the fetch family opts in; a heuristic on arbitrary output would wrongly
// reflow plain tool text.
func looksLikeMarkdown(tool, s string) bool {
	if tool != "fetch" && tool != "web" {
		return false
	}
	return strings.Contains(s, "# ") || strings.Contains(s, "](") ||
		strings.Contains(s, "\n- ") || strings.Contains(s, "\n* ")
}

// isDenial reports whether a tool error is a user permission denial rather than
// a real failure, so it can render as a WARN.
func isDenial(s string) bool {
	l := strings.ToLower(s)
	return strings.Contains(l, "denied") || strings.Contains(l, "not permitted") ||
		strings.Contains(l, "rejected by user")
}

// looksLikeDiff reports whether a body reads as a unified diff.
func looksLikeDiff(s string) bool {
	return strings.Contains(s, "\n@@ ") || strings.HasPrefix(s, "@@ ") ||
		strings.HasPrefix(s, "--- ") || strings.HasPrefix(s, "diff --git")
}

// prettyToolName maps a raw tool name to a human label. Unknown and MCP names
// are humanized by replacing separators and title-casing.
func prettyToolName(name string) string {
	switch name {
	case "bash":
		return "Bash"
	case "ls":
		return "List"
	case "read":
		return "Read"
	case "view":
		return "View"
	case "edit":
		return "Edit"
	case "write":
		return "Write"
	case "multiedit":
		return "Multi-Edit"
	case "glob":
		return "Glob"
	case "grep":
		return "Grep"
	case "fetch":
		return "Fetch"
	case "todos", "todo":
		return "To-Do"
	}
	if strings.HasPrefix(name, "mcp_") || strings.HasPrefix(name, "mcp__") {
		parts := strings.FieldsFunc(strings.TrimPrefix(strings.TrimPrefix(name, "mcp__"), "mcp_"), func(r rune) bool {
			return r == '_' || r == '-'
		})
		if len(parts) >= 2 {
			return titleCase(parts[0]) + " → " + titleCase(strings.Join(parts[1:], " "))
		}
	}
	return titleCase(strings.NewReplacer("_", " ", "-", " ").Replace(name))
}

func titleCase(s string) string {
	fields := strings.Fields(s)
	for i, f := range fields {
		if f == "" {
			continue
		}
		fields[i] = strings.ToUpper(f[:1]) + f[1:]
	}
	return strings.Join(fields, " ")
}

// toolMainParam pulls the primary parameter out of a tool's JSON input: a path,
// a command, a url, a pattern. Falls back to a compact one-line of the input.
func toolMainParam(tool string, input json.RawMessage) string {
	if len(input) == 0 {
		return ""
	}
	var m map[string]any
	if err := json.Unmarshal(input, &m); err != nil {
		return oneLine(string(input), 100)
	}
	for _, k := range []string{"path", "file", "file_path", "command", "cmd", "url", "pattern", "query", "name"} {
		if v, ok := m[k]; ok {
			if s, ok := v.(string); ok && s != "" {
				return s
			}
		}
	}
	return oneLine(string(input), 100)
}

// entryDuration formats a turn's wall-clock for the per-turn footer.
func entryDuration(start, end time.Time) string {
	if start.IsZero() || end.IsZero() {
		return ""
	}
	d := end.Sub(start)
	if d < time.Second {
		return fmt.Sprintf("%dms", d.Milliseconds())
	}
	return fmt.Sprintf("%.1fs", d.Seconds())
}
