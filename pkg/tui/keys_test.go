package tui

import "testing"

func TestKeymapDefaults(t *testing.T) {
	km := newKeymap(nil)
	if km.action("ctrl+n") != "model_cycle" {
		t.Errorf("ctrl+n should default to model_cycle, got %q", km.action("ctrl+n"))
	}
	if km.action("ctrl+g") != "editor" {
		t.Errorf("ctrl+g should default to editor, got %q", km.action("ctrl+g"))
	}
	if km.action("ctrl+z") != "" {
		t.Errorf("an unbound key should return no action, got %q", km.action("ctrl+z"))
	}
}

func TestKeymapOverride(t *testing.T) {
	km := newKeymap(map[string]string{"editor": "ctrl+e"})
	if km.action("ctrl+e") != "editor" {
		t.Errorf("override should bind ctrl+e to editor, got %q", km.action("ctrl+e"))
	}
	// The default key for the overridden action no longer resolves to it.
	if km.action("ctrl+g") == "editor" {
		t.Error("ctrl+g should no longer trigger editor after the override")
	}
	// Other defaults are untouched.
	if km.action("ctrl+n") != "model_cycle" {
		t.Errorf("ctrl+n should still cycle models, got %q", km.action("ctrl+n"))
	}
}

func TestKeymapIgnoresUnknownAction(t *testing.T) {
	km := newKeymap(map[string]string{"bogus": "ctrl+x", "editor": ""})
	if km.action("ctrl+x") != "" {
		t.Errorf("an unknown action should not bind, got %q", km.action("ctrl+x"))
	}
	// An empty key leaves the default in place.
	if km.action("ctrl+g") != "editor" {
		t.Errorf("empty override should keep the default, got %q", km.action("ctrl+g"))
	}
}
