package tui

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/tamnd/kaku/pkg/engine"
)

func TestLoadThemesBuiltins(t *testing.T) {
	themes := LoadThemes()
	for _, name := range []string{"dark", "light"} {
		if _, ok := themes[name]; !ok {
			t.Errorf("builtin theme %q missing", name)
		}
	}
}

func TestLoadThemesFromDir(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "solar.json"),
		[]byte(`{"primary":"#b58900","accent":"#268bd2"}`), 0o644); err != nil {
		t.Fatal(err)
	}
	themes := LoadThemes(dir)
	got, ok := themes["solar"]
	if !ok {
		t.Fatal("custom theme not loaded")
	}
	if got.Primary != "#b58900" || got.Name != "solar" {
		t.Errorf("custom theme = %+v", got)
	}
}

func TestSetThemeSwitches(t *testing.T) {
	m := &model{rt: Runtime{Agent: &engine.Agent{}}, themes: LoadThemes(), themeName: "dark"}
	m.st = newStyles(builtinThemes["dark"])
	m.setTheme("light")
	if m.themeName != "light" {
		t.Errorf("themeName = %q, want light", m.themeName)
	}
}

func TestSetThemeUnknownLists(t *testing.T) {
	m := &model{rt: Runtime{Agent: &engine.Agent{}}, themes: LoadThemes(), themeName: "dark"}
	m.st = newStyles(builtinThemes["dark"])
	m.setTheme("nope")
	if m.themeName != "dark" {
		t.Errorf("themeName changed to %q on a bad name", m.themeName)
	}
	last := m.entries[len(m.entries)-1].text
	if !strings.Contains(last, "no theme nope") || !strings.Contains(last, "light") {
		t.Errorf("expected a theme list, got %q", last)
	}
}
