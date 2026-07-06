package skill

import (
	"os"
	"path/filepath"
	"testing"
)

func TestParseWithFrontmatter(t *testing.T) {
	data := []byte(`---
name: deploy
description: Ship it
model: claude-sonnet-5
---
Do the deploy for $ARGUMENTS.
`)
	s, err := Parse("/tmp/skills/other.md", data)
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if s.Name != "deploy" {
		t.Errorf("Name = %q, want deploy", s.Name)
	}
	if s.Description != "Ship it" {
		t.Errorf("Description = %q", s.Description)
	}
	if s.Model != "claude-sonnet-5" {
		t.Errorf("Model = %q", s.Model)
	}
	if s.Source != "/tmp/skills/other.md" {
		t.Errorf("Source = %q", s.Source)
	}
	if s.Body != "Do the deploy for $ARGUMENTS.\n" {
		t.Errorf("Body = %q", s.Body)
	}
}

func TestParseWithoutFrontmatter(t *testing.T) {
	s, err := Parse("/home/u/.kaku/skills/review.md", []byte("Just review the diff.\n"))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if s.Name != "review" {
		t.Errorf("Name = %q, want review (from filename)", s.Name)
	}
	if s.Body != "Just review the diff.\n" {
		t.Errorf("Body = %q", s.Body)
	}
	if s.Description != "" || s.Model != "" {
		t.Errorf("unexpected frontmatter fields: %+v", s)
	}
}

func TestParsePartialFrontmatter(t *testing.T) {
	s, err := Parse("greet.md", []byte("---\ndescription: says hi\n---\nhi\n"))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if s.Name != "greet" {
		t.Errorf("Name = %q, want filename fallback greet", s.Name)
	}
	if s.Description != "says hi" {
		t.Errorf("Description = %q", s.Description)
	}
}

func TestParseMalformedFrontmatter(t *testing.T) {
	cases := map[string]string{
		"bad yaml":     "---\nname: [unclosed\n---\nbody\n",
		"unterminated": "---\nname: x\nbody with no closing fence\n",
	}
	for name, in := range cases {
		if _, err := Parse("x.md", []byte(in)); err == nil {
			t.Errorf("%s: Parse succeeded, want error", name)
		}
	}
}

func TestDiscover(t *testing.T) {
	proj := t.TempDir()
	user := t.TempDir()
	projSkills := filepath.Join(proj, ".kaku", "skills")
	if err := os.MkdirAll(projSkills, 0o755); err != nil {
		t.Fatal(err)
	}

	write := func(dir, name, body string) {
		t.Helper()
		if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write(projSkills, "clash.md", "project version")
	write(projSkills, "alpha.md", "alpha body")
	write(user, "clash.md", "user version")
	write(user, "zeta.md", "zeta body")
	write(user, "broken.md", "---\n: [::\n---\nnope")

	skills, err := Discover(proj, user)
	if err != nil {
		t.Fatalf("Discover: %v", err)
	}
	var names []string
	for _, s := range skills {
		names = append(names, s.Name)
	}
	want := []string{"alpha", "clash", "zeta"}
	if len(names) != len(want) {
		t.Fatalf("names = %v, want %v", names, want)
	}
	for i := range want {
		if names[i] != want[i] {
			t.Fatalf("names = %v, want %v (sorted)", names, want)
		}
	}

	clash, ok := Find(skills, "clash")
	if !ok {
		t.Fatal("Find(clash) missed")
	}
	if clash.Body != "project version" {
		t.Errorf("clash body = %q, want project to win", clash.Body)
	}
	if _, ok := Find(skills, "broken"); ok {
		t.Error("broken.md should have been skipped")
	}
	if _, ok := Find(skills, "missing"); ok {
		t.Error("Find(missing) should report false")
	}
}

func TestDiscoverMissingDirs(t *testing.T) {
	skills, err := Discover(filepath.Join(t.TempDir(), "nope"), filepath.Join(t.TempDir(), "also-nope"))
	if err != nil {
		t.Fatalf("Discover: %v", err)
	}
	if len(skills) != 0 {
		t.Errorf("got %d skills from empty dirs", len(skills))
	}
}

func TestExpand(t *testing.T) {
	withVar := Skill{Body: "run $ARGUMENTS twice: $ARGUMENTS"}
	if got := withVar.Expand("tests"); got != "run tests twice: tests" {
		t.Errorf("Expand = %q", got)
	}
	plain := Skill{Body: "just do it"}
	if got := plain.Expand("now"); got != "just do it\n\nArguments: now" {
		t.Errorf("Expand append = %q", got)
	}
	if got := plain.Expand(""); got != "just do it" {
		t.Errorf("Expand empty args = %q", got)
	}
}
