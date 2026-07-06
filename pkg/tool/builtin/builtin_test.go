package builtin

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/tamnd/kaku/pkg/tool"
)

func run(t *testing.T, tl tool.Tool, input string) (string, error) {
	t.Helper()
	return tl.Run(context.Background(), json.RawMessage(input))
}

func mustRun(t *testing.T, tl tool.Tool, input string) string {
	t.Helper()
	out, err := tl.Run(context.Background(), json.RawMessage(input))
	if err != nil {
		t.Fatalf("%s: %v", tl.Name(), err)
	}
	return out
}

func writeFile(t *testing.T, dir, name, content string) string {
	t.Helper()
	path := filepath.Join(dir, name)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestAllRegisters(t *testing.T) {
	tools := All(t.TempDir())
	if len(tools) != 8 {
		t.Fatalf("len = %d", len(tools))
	}
	names := map[string]bool{}
	for _, tl := range tools {
		names[tl.Name()] = true
	}
	for _, want := range []string{"read", "write", "edit", "bash", "grep", "glob", "ls", "fetch"} {
		if !names[want] {
			t.Errorf("missing tool %q", want)
		}
	}
}

func TestRead(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "a.txt", "one\ntwo\nthree\n")

	out := mustRun(t, readTool(dir), `{"file_path":"a.txt"}`)
	if !strings.Contains(out, "1\tone") || !strings.Contains(out, "3\tthree") {
		t.Errorf("out = %q", out)
	}

	out = mustRun(t, readTool(dir), `{"file_path":"a.txt","offset":2,"limit":1}`)
	if strings.Contains(out, "one") || !strings.Contains(out, "2\ttwo") || strings.Contains(out, "three") {
		t.Errorf("paged out = %q", out)
	}

	writeFile(t, dir, "empty.txt", "")
	out = mustRun(t, readTool(dir), `{"file_path":"empty.txt"}`)
	if !strings.Contains(out, "empty file") {
		t.Errorf("empty out = %q", out)
	}

	if _, err := run(t, readTool(dir), `{"file_path":"missing.txt"}`); err == nil {
		t.Error("expected error for missing file")
	}
	if _, err := run(t, readTool(dir), `{"file_path":""}`); err == nil {
		t.Error("expected error for empty path")
	}
	if _, err := run(t, readTool(dir), fmt.Sprintf(`{"file_path":%q}`, dir)); err == nil {
		t.Error("expected error for directory")
	}
}

func TestWrite(t *testing.T) {
	dir := t.TempDir()

	out := mustRun(t, writeTool(dir), `{"file_path":"sub/deep/b.txt","content":"hello"}`)
	if !strings.Contains(out, "5 bytes") {
		t.Errorf("out = %q", out)
	}
	data, err := os.ReadFile(filepath.Join(dir, "sub/deep/b.txt"))
	if err != nil || string(data) != "hello" {
		t.Errorf("data = %q, err = %v", data, err)
	}

	// Overwrite.
	mustRun(t, writeTool(dir), `{"file_path":"sub/deep/b.txt","content":"bye"}`)
	data, _ = os.ReadFile(filepath.Join(dir, "sub/deep/b.txt"))
	if string(data) != "bye" {
		t.Errorf("after overwrite = %q", data)
	}

	if _, err := run(t, writeTool(dir), `{"content":"x"}`); err == nil {
		t.Error("expected error for missing file_path")
	}
}

func TestEdit(t *testing.T) {
	dir := t.TempDir()
	path := writeFile(t, dir, "c.txt", "aaa bbb aaa\n")

	// Ambiguous match fails.
	if _, err := run(t, editTool(dir), `{"file_path":"c.txt","old_string":"aaa","new_string":"xxx"}`); err == nil {
		t.Error("expected error for ambiguous old_string")
	}

	// replace_all.
	mustRun(t, editTool(dir), `{"file_path":"c.txt","old_string":"aaa","new_string":"xxx","replace_all":true}`)
	data, _ := os.ReadFile(path)
	if string(data) != "xxx bbb xxx\n" {
		t.Errorf("data = %q", data)
	}

	// Unique replace.
	mustRun(t, editTool(dir), `{"file_path":"c.txt","old_string":"bbb","new_string":"yyy"}`)
	data, _ = os.ReadFile(path)
	if string(data) != "xxx yyy xxx\n" {
		t.Errorf("data = %q", data)
	}

	if _, err := run(t, editTool(dir), `{"file_path":"c.txt","old_string":"zzz","new_string":"q"}`); err == nil {
		t.Error("expected error for old_string not found")
	}
	if _, err := run(t, editTool(dir), `{"file_path":"c.txt","old_string":"same","new_string":"same"}`); err == nil {
		t.Error("expected error for identical strings")
	}
}

func TestBash(t *testing.T) {
	dir := t.TempDir()

	out := mustRun(t, bashTool(dir, false), `{"command":"echo hello && pwd"}`)
	if !strings.Contains(out, "hello") {
		t.Errorf("out = %q", out)
	}
	// Runs in workdir. macOS tempdirs may print with or without the
	// /private prefix, so compare against both spellings.
	real, _ := filepath.EvalSymlinks(dir)
	if !strings.Contains(out, dir) && !strings.Contains(out, real) {
		t.Errorf("out = %q, want workdir %q", out, dir)
	}

	out = mustRun(t, bashTool(dir, false), `{"command":"true"}`)
	if out != "(no output)" {
		t.Errorf("out = %q", out)
	}

	// Failures carry the exit status and output in the error.
	_, err := run(t, bashTool(dir, false), `{"command":"echo oops >&2; exit 3"}`)
	if err == nil || !strings.Contains(err.Error(), "exit status 3") || !strings.Contains(err.Error(), "oops") {
		t.Errorf("err = %v", err)
	}

	// Timeout kills the command.
	_, err = run(t, bashTool(dir, false), `{"command":"sleep 5","timeout_ms":200}`)
	if err == nil || !strings.Contains(err.Error(), "timed out") {
		t.Errorf("err = %v", err)
	}
}

func TestTruncateOutput(t *testing.T) {
	long := strings.Repeat("x", bashMaxOutput+1000)
	out := truncateOutput(long)
	if len(out) >= len(long) || !strings.Contains(out, "[truncated]") {
		t.Errorf("len = %d", len(out))
	}
	if truncateOutput("short") != "short" {
		t.Error("short output must pass through")
	}
}

func TestGrep(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "x.go", "package x\nfunc Hello() {}\n")
	writeFile(t, dir, "sub/y.go", "package y\nfunc Hello2() {}\n")
	writeFile(t, dir, "z.txt", "Hello text\n")
	writeFile(t, dir, "node_modules/dep.go", "func Hello3() {}\n")
	writeFile(t, dir, "bin.dat", "Hello\x00binary")

	out := mustRun(t, grepTool(dir), `{"pattern":"func Hello"}`)
	if !strings.Contains(out, "x.go:2:") || !strings.Contains(out, "sub/y.go:2:") {
		t.Errorf("out = %q", out)
	}
	if strings.Contains(out, "node_modules") || strings.Contains(out, "bin.dat") {
		t.Errorf("skips not applied: %q", out)
	}

	out = mustRun(t, grepTool(dir), `{"pattern":"Hello","glob":"*.txt"}`)
	if !strings.Contains(out, "z.txt") || strings.Contains(out, "x.go") {
		t.Errorf("glob filter: %q", out)
	}

	out = mustRun(t, grepTool(dir), `{"pattern":"Hello","max_results":1}`)
	if !strings.Contains(out, "(truncated at 1 results)") {
		t.Errorf("truncation marker: %q", out)
	}

	out = mustRun(t, grepTool(dir), `{"pattern":"nomatchanywhere"}`)
	if out != "no matches" {
		t.Errorf("out = %q", out)
	}

	if _, err := run(t, grepTool(dir), `{"pattern":"["}`); err == nil {
		t.Error("expected error for bad pattern")
	}
}

func TestGlob(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "main.go", "x")
	writeFile(t, dir, "pkg/a/a.go", "x")
	writeFile(t, dir, "pkg/a/deep/b.go", "x")
	writeFile(t, dir, "docs/readme.md", "x")

	// Base-name pattern matches anywhere.
	out := mustRun(t, globTool(dir), `{"pattern":"*.go"}`)
	for _, want := range []string{"main.go", "pkg/a/a.go", "pkg/a/deep/b.go"} {
		if !strings.Contains(out, want) {
			t.Errorf("missing %q in %q", want, out)
		}
	}

	// ** spans segments, including zero.
	out = mustRun(t, globTool(dir), `{"pattern":"pkg/**/*.go"}`)
	if !strings.Contains(out, "pkg/a/a.go") || !strings.Contains(out, "pkg/a/deep/b.go") {
		t.Errorf("out = %q", out)
	}
	if strings.Contains(out, "main.go") {
		t.Errorf("out = %q", out)
	}

	out = mustRun(t, globTool(dir), `{"pattern":"*.rs"}`)
	if out != "no matches" {
		t.Errorf("out = %q", out)
	}
}

func TestLs(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "b.txt", "x")
	if err := os.Mkdir(filepath.Join(dir, "adir"), 0o755); err != nil {
		t.Fatal(err)
	}

	out := mustRun(t, lsTool(dir), `{}`)
	if !strings.Contains(out, "adir/") || !strings.Contains(out, "b.txt") {
		t.Errorf("out = %q", out)
	}

	empty := t.TempDir()
	out = mustRun(t, lsTool(dir), fmt.Sprintf(`{"path":%q}`, empty))
	if !strings.Contains(out, "is empty") {
		t.Errorf("out = %q", out)
	}

	if _, err := run(t, lsTool(dir), `{"path":"missing-dir"}`); err == nil {
		t.Error("expected error for missing dir")
	}
}

func TestFetch(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/html":
			w.Header().Set("Content-Type", "text/html")
			fmt.Fprint(w, "<html><head><script>bad()</script><style>x{}</style></head><body><h1>Title</h1><p>Body &amp; more</p></body></html>")
		case "/missing":
			http.Error(w, "gone", http.StatusNotFound)
		default:
			fmt.Fprint(w, "plain body")
		}
	}))
	defer srv.Close()

	out := mustRun(t, fetchTool(), fmt.Sprintf(`{"url":%q}`, srv.URL+"/plain"))
	if out != "plain body" {
		t.Errorf("out = %q", out)
	}

	out = mustRun(t, fetchTool(), fmt.Sprintf(`{"url":%q}`, srv.URL+"/html"))
	if strings.Contains(out, "bad()") || strings.Contains(out, "<h1>") {
		t.Errorf("html not stripped: %q", out)
	}
	if !strings.Contains(out, "Title") || !strings.Contains(out, "Body & more") {
		t.Errorf("out = %q", out)
	}

	out = mustRun(t, fetchTool(), fmt.Sprintf(`{"url":%q}`, srv.URL+"/missing"))
	if !strings.HasPrefix(out, "status 404") {
		t.Errorf("out = %q", out)
	}

	if _, err := run(t, fetchTool(), `{"url":""}`); err == nil {
		t.Error("expected error for empty url")
	}
}

func TestResolve(t *testing.T) {
	if got := resolve("/w", "a/b"); got != "/w/a/b" {
		t.Errorf("got %q", got)
	}
	if got := resolve("/w", "/abs"); got != "/abs" {
		t.Errorf("got %q", got)
	}
	if got := resolve("/w", ""); got != "/w" {
		t.Errorf("got %q", got)
	}
}
