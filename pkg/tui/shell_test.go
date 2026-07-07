package tui

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/tamnd/kaku/pkg/engine"
	"github.com/tamnd/kaku/pkg/session"
)

func bareModel() *model {
	m := &model{
		rootCtx:   context.Background(),
		rt:        Runtime{Agent: &engine.Agent{}},
		themes:    LoadThemes(),
		themeName: "dark",
	}
	m.st = newStyles(builtinThemes["dark"])
	return m
}

func TestRunShellFeedsContext(t *testing.T) {
	m := bareModel()
	m.runShell("!echo hello")
	if len(m.entries) != 1 || !strings.Contains(m.entries[0].text, "hello") {
		t.Fatalf("expected an entry with the output, got %+v", m.entries)
	}
	if len(m.pendingCtx) != 1 || !strings.Contains(m.pendingCtx[0], "hello") {
		t.Fatalf("single ! should feed output to the next prompt, got %+v", m.pendingCtx)
	}
}

func TestRunShellQuietDoesNotFeed(t *testing.T) {
	m := bareModel()
	m.runShell("!!echo hush")
	if len(m.entries) != 1 || !strings.Contains(m.entries[0].text, "hush") {
		t.Fatalf("!! should still show output, got %+v", m.entries)
	}
	if len(m.pendingCtx) != 0 {
		t.Fatalf("!! should not feed the next prompt, got %+v", m.pendingCtx)
	}
}

func TestRunShellEmptyShowsUsage(t *testing.T) {
	m := bareModel()
	m.runShell("!")
	if len(m.entries) != 1 || !strings.Contains(m.entries[0].text, "usage") {
		t.Fatalf("bare ! should show usage, got %+v", m.entries)
	}
}

func tmpSession(t *testing.T) (*session.Store, *session.Session) {
	t.Helper()
	st := &session.Store{Root: t.TempDir(), Project: "/proj"}
	s, err := st.New()
	if err != nil {
		t.Fatalf("new session: %v", err)
	}
	return st, s
}

func TestSessionDialogMarksCurrent(t *testing.T) {
	st, s := tmpSession(t)
	m := bareModel()
	m.rt.Session = s
	metas, err := st.List()
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	d := m.sessionDialog(metas)
	if d.kind != dlgSessions || len(d.items) == 0 {
		t.Fatalf("expected a sessions picker with items, got %+v", d)
	}
	if !strings.Contains(d.items[d.cursor].desc, "current") {
		t.Errorf("cursor should rest on the current session, desc = %q", d.items[d.cursor].desc)
	}
}

func TestDeleteFromPickerRefusesActive(t *testing.T) {
	st, s := tmpSession(t)
	m := bareModel()
	m.rt.Session = s
	m.rt.Sessions = st.List
	m.rt.DeleteSession = st.Delete
	metas, _ := st.List()
	m.dialog = m.sessionDialog(metas)
	m.deleteFromPicker()
	if m.dialog == nil {
		t.Fatal("dialog should stay open after refusing to delete the active session")
	}
	last := m.entries[len(m.entries)-1].text
	if !strings.Contains(last, "cannot delete the active session") {
		t.Errorf("expected a refusal message, got %q", last)
	}
}

// keep the time import honest: a fast smoke that the shell helper respects a
// context deadline without hanging the suite.
func TestShellTimeoutBounded(t *testing.T) {
	m := bareModel()
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	m.rootCtx = ctx
	out := m.shell("echo bounded")
	if !strings.Contains(out, "bounded") {
		t.Errorf("shell output = %q, want it to contain bounded", out)
	}
}
