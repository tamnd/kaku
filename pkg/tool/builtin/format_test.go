package builtin

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

// fakeFormatter writes a shell script on PATH that uppercases nothing but
// appends a marker line to the file it is given, so a test can prove the
// formatter ran on the written path.
func fakeFormatter(t *testing.T, name string) string {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("fake formatter uses a shell script")
	}
	dir := t.TempDir()
	script := filepath.Join(dir, name)
	// gofmt is invoked as "gofmt -w <file>", so append to the last argument.
	body := "#!/bin/sh\nfor f in \"$@\"; do :; done\nprintf 'formatted\\n' >> \"$f\"\n"
	if err := os.WriteFile(script, []byte(body), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
	return dir
}

func TestFormatterRewritesAfterWrite(t *testing.T) {
	fakeFormatter(t, "gofmt")
	work := t.TempDir()
	f := NewFormatter(work, true, nil)
	if f == nil {
		t.Fatal("formatter should be enabled")
	}
	mustRun(t, writeTool(work, f), `{"file_path":"main.go","content":"package main\n"}`)
	got, err := os.ReadFile(filepath.Join(work, "main.go"))
	if err != nil {
		t.Fatal(err)
	}
	if want := "package main\nformatted\n"; string(got) != want {
		t.Errorf("file = %q, want %q", got, want)
	}
}

func TestFormatterSkipsUnmatchedExtension(t *testing.T) {
	fakeFormatter(t, "gofmt")
	work := t.TempDir()
	f := NewFormatter(work, true, nil)
	mustRun(t, writeTool(work, f), `{"file_path":"notes.txt","content":"hello\n"}`)
	got, _ := os.ReadFile(filepath.Join(work, "notes.txt"))
	if string(got) != "hello\n" {
		t.Errorf("a .txt file should be left alone, got %q", got)
	}
}

func TestFormatterDisabledWhenOff(t *testing.T) {
	if NewFormatter("/tmp", false, nil) != nil {
		t.Error("disabled formatter should be nil")
	}
	// A nil formatter is a safe no-op.
	var f *Formatter
	f.Format("/tmp/x.go")
}

func TestFormatterDisableAndCustom(t *testing.T) {
	f := NewFormatter("/tmp", true, map[string]FormatSpec{
		"gofmt": {Disabled: true},
		"deno":  {Command: []string{"deno", "fmt", "$FILE"}, Extensions: []string{".md"}},
	})
	if _, ok := f.byExt[".go"]; ok {
		t.Error("gofmt should be disabled, so .go is unregistered")
	}
	argv, ok := f.byExt[".md"]
	if !ok || argv[0] != "deno" {
		t.Errorf("custom deno formatter should own .md, got %v", argv)
	}
}
