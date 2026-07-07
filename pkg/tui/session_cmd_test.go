package tui

import (
	"strings"
	"testing"

	"github.com/tamnd/kaku/pkg/engine"
	"github.com/tamnd/kaku/pkg/session"
)

func TestSlashNameCallsRename(t *testing.T) {
	var got string
	m := &model{rt: Runtime{Agent: &engine.Agent{}, Rename: func(t string) error { got = t; return nil }}}
	if _, ok := m.slash("/name release prep"); !ok {
		t.Fatal("/name not handled")
	}
	if got != "release prep" {
		t.Errorf("rename got %q", got)
	}
	if len(m.entries) == 0 || !strings.Contains(m.entries[len(m.entries)-1].text, "release prep") {
		t.Errorf("missing confirmation entry: %+v", m.entries)
	}
}

func TestSlashExportRoutesArg(t *testing.T) {
	var got string
	m := &model{rt: Runtime{Agent: &engine.Agent{}, Export: func(a string) (string, error) { got = a; return "exported to " + a, nil }}}
	if _, ok := m.slash("/export out.md"); !ok {
		t.Fatal("/export not handled")
	}
	if got != "out.md" {
		t.Errorf("export arg = %q", got)
	}
}

func TestSlashNewSwapsSession(t *testing.T) {
	st := &session.Store{Root: t.TempDir(), Project: "/p"}
	fresh, err := st.New()
	if err != nil {
		t.Fatal(err)
	}
	m := &model{rt: Runtime{
		Agent:      &engine.Agent{Messages: nil},
		NewSession: func() (*session.Session, error) { return fresh, nil },
	}}
	m.entries = []entry{{kind: "user", text: "old"}}
	if _, ok := m.slash("/new"); !ok {
		t.Fatal("/new not handled")
	}
	if m.rt.Session != fresh {
		t.Error("session not swapped")
	}
	if len(m.entries) != 1 || !strings.Contains(m.entries[0].text, fresh.ID()) {
		t.Errorf("entries not reset: %+v", m.entries)
	}
}
