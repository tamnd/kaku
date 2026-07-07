package skill

import (
	"os"
	"path/filepath"
	"strings"
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
	dir := t.TempDir()
	withVar := Skill{Body: "run $ARGUMENTS twice: $ARGUMENTS"}
	if got := withVar.Expand("tests", dir); got != "run tests twice: tests" {
		t.Errorf("Expand = %q", got)
	}
	plain := Skill{Body: "just do it"}
	if got := plain.Expand("now", dir); got != "just do it\n\nArguments: now" {
		t.Errorf("Expand append = %q", got)
	}
	if got := plain.Expand("", dir); got != "just do it" {
		t.Errorf("Expand empty args = %q", got)
	}
}

func TestExpandPositional(t *testing.T) {
	dir := t.TempDir()
	s := Skill{Body: "first=$1 second=$2 rest=$@"}
	if got := s.Expand("alpha beta gamma", dir); got != "first=alpha second=beta rest=alpha beta gamma" {
		t.Errorf("Expand positional = %q", got)
	}
	// Quoted phrase stays one argument.
	q := Skill{Body: "msg=$1"}
	if got := q.Expand(`"hello world" extra`, dir); got != "msg=hello world" {
		t.Errorf("Expand quoted = %q", got)
	}
}

func TestExpandDefault(t *testing.T) {
	dir := t.TempDir()
	s := Skill{Body: "branch=${1:-main}"}
	if got := s.Expand("", dir); got != "branch=main" {
		t.Errorf("Expand default = %q", got)
	}
	if got := s.Expand("feature", dir); got != "branch=feature" {
		t.Errorf("Expand override = %q", got)
	}
}

func TestExpandShell(t *testing.T) {
	dir := t.TempDir()
	s := Skill{Body: "out=!`echo hi`"}
	if got := s.Expand("", dir); got != "out=hi" {
		t.Errorf("Expand shell = %q", got)
	}
}

func TestExpandFile(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "note.txt"), []byte("body text"), 0o644); err != nil {
		t.Fatal(err)
	}
	s := Skill{Body: "see @note.txt"}
	got := s.Expand("", dir)
	if !strings.Contains(got, `<file path="note.txt">`) || !strings.Contains(got, "body text") {
		t.Errorf("Expand file = %q", got)
	}
}
