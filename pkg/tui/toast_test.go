package tui

import (
	"strings"
	"testing"
)

func toastTestModel() *model {
	m := &model{}
	m.st = newStyles(builtinThemes["dark"])
	m.themeName = "dark"
	return m
}

func TestNotifySetsToastAndReturnsClearCmd(t *testing.T) {
	m := toastTestModel()
	cmd := m.notify(toastSuccess, "theme: dark")
	if m.toast == nil || m.toast.text != "theme: dark" || m.toast.severity != toastSuccess {
		t.Fatalf("toast not set correctly: %+v", m.toast)
	}
	if cmd == nil {
		t.Fatal("notify should return a clear command")
	}
	// The clear command clears exactly this toast's sequence when it fires.
	// (The real tea.Tick waits the TTL, so exercise the handler directly.)
	m.clearToast(m.toast.seq)
	if m.toast != nil {
		t.Errorf("clearToast on the current sequence should drop the toast: %+v", m.toast)
	}
}

func TestClearToastRespectsSequence(t *testing.T) {
	m := toastTestModel()
	m.notify(toastInfo, "first")
	firstSeq := m.toast.seq
	m.notify(toastWarn, "second") // supersedes; seq bumps

	// A stale timer for the first toast must not clear the second.
	m.clearToast(firstSeq)
	if m.toast == nil || m.toast.text != "second" {
		t.Errorf("stale clear dropped the current toast: %+v", m.toast)
	}
	// The matching timer clears it.
	m.clearToast(m.toast.seq)
	if m.toast != nil {
		t.Errorf("matching clear left a toast: %+v", m.toast)
	}
}

func TestToastViewRendersOneLine(t *testing.T) {
	m := toastTestModel()
	if m.toastView(40) != "" {
		t.Error("no toast should render empty")
	}
	m.notify(toastError, "boom\nsecond line that is quite long and should be collapsed and truncated")
	out := m.toastView(30)
	if strings.Contains(out, "\n") {
		t.Errorf("toast must collapse to one line: %q", out)
	}
	if !strings.Contains(out, glyphFail) {
		t.Errorf("error toast should carry the fail glyph: %q", out)
	}
}

func TestWithToastBatchesTimerWhenPending(t *testing.T) {
	m := toastTestModel()
	// No toast: withToast passes the command through untouched.
	if got := m.withToast(nil); got != nil {
		t.Error("withToast(nil) with no toast should stay nil")
	}
	m.notify(toastInfo, "hi")
	if got := m.withToast(nil); got == nil {
		t.Error("withToast should attach a clear timer when a toast is pending")
	}
}
