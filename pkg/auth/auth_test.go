package auth

import (
	"os"
	"path/filepath"
	"testing"
)

func TestSetGetDeleteRoundTrip(t *testing.T) {
	s := NewAt(filepath.Join(t.TempDir(), "auth.json"))
	if _, ok := s.Get("anthropic"); ok {
		t.Fatal("empty store should not report a key")
	}
	if err := s.Set("anthropic", "sk-abc"); err != nil {
		t.Fatal(err)
	}
	got, ok := s.Get("anthropic")
	if !ok || got != "sk-abc" {
		t.Fatalf("Get = %q, %v; want sk-abc, true", got, ok)
	}
	if err := s.Delete("anthropic"); err != nil {
		t.Fatal(err)
	}
	if _, ok := s.Get("anthropic"); ok {
		t.Fatal("key should be gone after delete")
	}
}

func TestSetPersistsAcrossStores(t *testing.T) {
	path := filepath.Join(t.TempDir(), "auth.json")
	if err := NewAt(path).Set("zen", "z-key"); err != nil {
		t.Fatal(err)
	}
	got, ok := NewAt(path).Get("zen")
	if !ok || got != "z-key" {
		t.Fatalf("reopened store Get = %q, %v; want z-key, true", got, ok)
	}
}

func TestFileModeIs0600(t *testing.T) {
	path := filepath.Join(t.TempDir(), "auth.json")
	if err := NewAt(path).Set("openai", "sk-1"); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if perm := info.Mode().Perm(); perm != 0o600 {
		t.Errorf("file mode = %o, want 600", perm)
	}
}

func TestListReturnsNamesNotValues(t *testing.T) {
	s := NewAt(filepath.Join(t.TempDir(), "auth.json"))
	s.Set("anthropic", "sk-secret")
	s.Set("openai", "sk-other")
	names := s.List()
	if len(names) != 2 || names[0] != "anthropic" || names[1] != "openai" {
		t.Fatalf("List = %v, want sorted [anthropic openai]", names)
	}
	for _, n := range names {
		if n == "sk-secret" || n == "sk-other" {
			t.Errorf("List leaked a key value: %q", n)
		}
	}
}

func TestDeleteAbsentIsNoError(t *testing.T) {
	s := NewAt(filepath.Join(t.TempDir(), "auth.json"))
	if err := s.Delete("nobody"); err != nil {
		t.Errorf("deleting an absent key should not error, got %v", err)
	}
}
