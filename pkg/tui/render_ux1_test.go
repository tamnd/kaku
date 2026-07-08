package tui

import (
	"encoding/json"
	"strings"
	"testing"
)

// newTestModel builds a minimal model with styles and a working dir for the
// render helpers, without standing up the full TUI.
func newTestModel(dir string) *model {
	m := &model{}
	m.rt.Dir = dir
	m.st = newStyles(builtinThemes["dark"])
	m.themeName = "dark"
	return m
}

func TestPrettyPath(t *testing.T) {
	m := newTestModel("/work/project")
	cases := map[string]string{
		"/work/project/pkg/main.go": "pkg/main.go",
		"/work/other/file.go":       "/work/other/file.go",
	}
	for in, want := range cases {
		if got := m.prettyPath(in); got != want {
			t.Errorf("prettyPath(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestLooksLikePath(t *testing.T) {
	yes := []string{"/abs/path", "./rel", "~/home", "pkg/main.go"}
	no := []string{"echo hi", "https://x.y/z", "SELECT * FROM t", "plainname"}
	for _, s := range yes {
		if !looksLikePath(s) {
			t.Errorf("looksLikePath(%q) = false, want true", s)
		}
	}
	for _, s := range no {
		if looksLikePath(s) {
			t.Errorf("looksLikePath(%q) = true, want false", s)
		}
	}
}

func TestDiffCountHeader(t *testing.T) {
	m := newTestModel("/w")
	diff := "@@ -1,2 +1,2 @@\n-old line\n+new line\n context\n+another add"
	got := m.st.diffAdd // ensure styles built
	_ = got
	h := diffCountHeader(m, diff)
	if !strings.Contains(h, "+2") || !strings.Contains(h, "-1") {
		t.Errorf("diffCountHeader = %q, want +2 and -1", h)
	}
	if diffCountHeader(m, "no changes here\nplain text") != "" {
		t.Error("expected empty header for a body with no +/- lines")
	}
	// The ---/+++ file markers must not be counted as content changes.
	fileHdr := "--- a/x\n+++ b/x\n+real add"
	h2 := diffCountHeader(m, fileHdr)
	if !strings.Contains(h2, "+1") || !strings.Contains(h2, "-0") {
		t.Errorf("diffCountHeader(fileHdr) = %q, want +1 -0", h2)
	}
}

func TestEditDiff(t *testing.T) {
	edit := &entry{tool: "edit", input: json.RawMessage(`{"old_string":"here","new_string":"everywhere"}`)}
	d := editDiff(edit)
	if !strings.Contains(d, "-here") || !strings.Contains(d, "+everywhere") {
		t.Errorf("edit diff = %q, want -here and +everywhere", d)
	}
	write := &entry{tool: "write", input: json.RawMessage(`{"content":"line one\nline two"}`)}
	dw := editDiff(write)
	if !strings.Contains(dw, "+line one") || !strings.Contains(dw, "+line two") {
		t.Errorf("write diff = %q, want both lines as additions", dw)
	}
	if editDiff(&entry{tool: "edit", input: nil}) != "" {
		t.Error("empty input should yield empty diff")
	}
}

// TestToolBodyEditShowsDiff checks an edit tool renders a colored diff with a
// count header even though the tool output is only a status line.
func TestToolBodyEditShowsDiff(t *testing.T) {
	m := newTestModel("/w")
	e := &entry{
		kind:   "tool",
		tool:   "edit",
		status: toolSuccess,
		input:  json.RawMessage(`{"file_path":"/w/hello.txt","old_string":"here","new_string":"everywhere"}`),
		output: "replaced 1 occurrence(s) in /w/hello.txt",
	}
	body := m.toolBody(e, 80)
	if !strings.Contains(body, "+1") || !strings.Contains(body, "-1") {
		t.Errorf("edit body missing +/- count header:\n%s", body)
	}
	if !strings.Contains(body, "everywhere") {
		t.Errorf("edit body missing the new text:\n%s", body)
	}
}

func TestLooksLikeJSONAndPretty(t *testing.T) {
	if !looksLikeJSON(`{"a":1}`) {
		t.Error("object should look like JSON")
	}
	if !looksLikeJSON(`[1,2,3]`) {
		t.Error("array should look like JSON")
	}
	if looksLikeJSON(`not json`) || looksLikeJSON(`{bad`) {
		t.Error("non-JSON should not pass")
	}
	out := prettyJSON(`{"b":2,"a":1}`)
	if !strings.Contains(out, "\n") {
		t.Errorf("prettyJSON did not indent: %q", out)
	}
	var v map[string]int
	if err := json.Unmarshal([]byte(out), &v); err != nil {
		t.Errorf("prettyJSON output not valid JSON: %v", err)
	}
}

func TestMatchCount(t *testing.T) {
	cases := []struct {
		tool   string
		status toolStatus
		output string
		want   int
	}{
		{"grep", toolSuccess, "a.go:1: x\nb.go:2: y", 2},
		{"glob", toolSuccess, "one.txt", 1},
		{"ls", toolSuccess, "", 0},
		{"grep", toolRunning, "partial", -1},
		{"read", toolSuccess, "file body", -1},
	}
	for _, c := range cases {
		e := &entry{kind: "tool", tool: c.tool, status: c.status, output: c.output}
		if got := matchCount(e); got != c.want {
			t.Errorf("matchCount(%s,%v,%q) = %d, want %d", c.tool, c.status, c.output, got, c.want)
		}
	}
}

func TestIsDenial(t *testing.T) {
	for _, s := range []string{"permission denied", "Rejected by user", "operation not permitted"} {
		if !isDenial(s) {
			t.Errorf("isDenial(%q) = false, want true", s)
		}
	}
	if isDenial("file not found") {
		t.Error("a real error should not read as a denial")
	}
}

// TestToolBodyDenialUsesWarn checks a denied tool result renders a WARN tag, not
// ERROR, so an expected outcome does not look like a crash.
func TestToolBodyDenialUsesWarn(t *testing.T) {
	m := newTestModel("/w")
	e := &entry{kind: "tool", tool: "edit", status: toolFail, isError: true, output: "permission denied by user"}
	body := m.toolBody(e, 80)
	if !strings.Contains(body, "WARN") {
		t.Errorf("denied tool body missing WARN tag:\n%s", body)
	}
	if strings.Contains(body, "ERROR") {
		t.Errorf("denied tool body should not carry ERROR tag:\n%s", body)
	}
}

// TestRenderToolHeaderMatchCount checks a grep header carries its match count.
func TestRenderToolHeaderMatchCount(t *testing.T) {
	m := newTestModel("/w")
	e := &entry{kind: "tool", tool: "grep", status: toolSuccess, input: json.RawMessage(`{"pattern":"foo"}`), output: "a.go:1: foo\nb.go:9: foo\nc.go:3: foo"}
	out := m.renderTool(e, 100)
	if !strings.Contains(out, "(3)") {
		t.Errorf("grep header missing match count:\n%s", out)
	}
}
