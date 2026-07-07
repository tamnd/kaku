package tui

import (
	"testing"
)

func TestEditorCommand(t *testing.T) {
	t.Setenv("VISUAL", "")
	t.Setenv("EDITOR", "")
	if name, args := editorCommand(); name != "vi" || len(args) != 0 {
		t.Errorf("default = %q %v, want vi with no args", name, args)
	}

	t.Setenv("EDITOR", "nano")
	if name, args := editorCommand(); name != "nano" || len(args) != 0 {
		t.Errorf("EDITOR = %q %v, want nano", name, args)
	}

	// VISUAL wins over EDITOR and its arguments are split off.
	t.Setenv("VISUAL", "code --wait")
	name, args := editorCommand()
	if name != "code" || len(args) != 1 || args[0] != "--wait" {
		t.Errorf("VISUAL = %q %v, want code [--wait]", name, args)
	}
}
