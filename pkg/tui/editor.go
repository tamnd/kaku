package tui

import (
	"os"
	"os/exec"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
)

// editorDoneMsg reports that the external editor closed. Path is the temp file
// the composer content was edited in; Err is non-nil when the editor failed to
// launch or exited non-zero.
type editorDoneMsg struct {
	path string
	err  error
}

// editorCommand returns the editor to launch, honoring $VISUAL then $EDITOR and
// falling back to a sensible default. The second value splits any arguments the
// variable carries, e.g. "code --wait".
func editorCommand() (string, []string) {
	ed := os.Getenv("VISUAL")
	if ed == "" {
		ed = os.Getenv("EDITOR")
	}
	if ed == "" {
		ed = "vi"
	}
	fields := strings.Fields(ed)
	return fields[0], fields[1:]
}

// openEditor writes the current composer text to a temp file and opens it in
// the external editor, suspending the TUI while it runs. On close the composer
// is replaced with the edited file's contents. A failure to create the temp
// file surfaces as an editorDoneMsg with the error and no path.
func openEditor(seed string) tea.Cmd {
	f, err := os.CreateTemp("", "kaku-compose-*.md")
	if err != nil {
		return func() tea.Msg { return editorDoneMsg{err: err} }
	}
	path := f.Name()
	if _, err := f.WriteString(seed); err != nil {
		f.Close()
		os.Remove(path)
		return func() tea.Msg { return editorDoneMsg{err: err} }
	}
	f.Close()

	name, args := editorCommand()
	args = append(args, path)
	c := exec.Command(name, args...)
	return tea.ExecProcess(c, func(err error) tea.Msg {
		return editorDoneMsg{path: path, err: err}
	})
}
