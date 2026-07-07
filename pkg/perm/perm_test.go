package perm

import (
	"encoding/json"
	"testing"
)

func TestParseRule(t *testing.T) {
	cases := []struct {
		in   string
		want Rule
	}{
		{"bash", Rule{Tool: "bash"}},
		{"bash(go test*)", Rule{Tool: "bash", Arg: "go test*"}},
		{" read(*.md) ", Rule{Tool: "read", Arg: "*.md"}},
		{"weird(", Rule{Tool: "weird("}},
	}
	for _, c := range cases {
		if got := ParseRule(c.in); got != c.want {
			t.Errorf("ParseRule(%q) = %+v, want %+v", c.in, got, c.want)
		}
	}
}

func TestParseRulesExpandsCategories(t *testing.T) {
	// The "edit" category covers both edit and write, keeping the arg glob.
	rules := ParseRules([]string{"edit(docs/*)"})
	if len(rules) != 2 {
		t.Fatalf("edit category should expand to 2 rules, got %+v", rules)
	}
	want := map[string]bool{"edit": true, "write": true}
	for _, r := range rules {
		if !want[r.Tool] {
			t.Errorf("unexpected tool %q", r.Tool)
		}
		if r.Arg != "docs/*" {
			t.Errorf("arg not preserved on %q: %q", r.Tool, r.Arg)
		}
	}

	// "read" fans out to every read-only file tool.
	if got := ParseRules([]string{"read"}); len(got) != 4 {
		t.Errorf("read category should expand to 4 rules, got %+v", got)
	}

	// A concrete tool name passes through untouched, and "bash" maps to itself.
	if got := ParseRules([]string{"bash", "fetch"}); len(got) != 2 {
		t.Errorf("concrete tools should not expand, got %+v", got)
	}
}

func TestCategoryRuleGatesBothTools(t *testing.T) {
	// A single deny on the edit category blocks both edit and write, even in
	// auto mode where everything else is allowed.
	e := &Engine{Mode: ModeAuto, Deny: ParseRules([]string{"edit"})}
	if got := e.Check("edit", input("file_path", "main.go")); got != Deny {
		t.Errorf("edit should be denied, got %v", got)
	}
	if got := e.Check("write", input("file_path", "main.go")); got != Deny {
		t.Errorf("write should be denied via the edit category, got %v", got)
	}
	if got := e.Check("bash", input("command", "ls")); got != Allow {
		t.Errorf("bash stays allowed in auto mode, got %v", got)
	}
}

func input(kv ...string) json.RawMessage {
	m := map[string]string{}
	for i := 0; i+1 < len(kv); i += 2 {
		m[kv[i]] = kv[i+1]
	}
	b, _ := json.Marshal(m)
	return b
}

func TestCheck(t *testing.T) {
	e := &Engine{
		Mode:     ModeAsk,
		Allow:    ParseRules([]string{"bash(go test*)", "write(/tmp/*)"}),
		Deny:     ParseRules([]string{"bash(rm *)"}),
		ReadOnly: func(tool string) bool { return tool == "read" },
	}
	cases := []struct {
		tool string
		in   json.RawMessage
		want Decision
	}{
		{"read", input("file_path", "main.go"), Allow},
		{"bash", input("command", "go test ./..."), Allow},
		{"bash", input("command", "rm -rf /"), Deny},
		{"bash", input("command", "make"), Ask},
		{"write", input("file_path", "/tmp/x.txt"), Allow},
		{"write", input("file_path", "main.go"), Ask},
	}
	for _, c := range cases {
		if got := e.Check(c.tool, c.in); got != c.want {
			t.Errorf("Check(%s, %s) = %v, want %v", c.tool, c.in, got, c.want)
		}
	}

	e.Mode = ModePlan
	if got := e.Check("bash", input("command", "make")); got != Deny {
		t.Errorf("plan mode should deny bash, got %v", got)
	}
	if got := e.Check("read", input("file_path", "x")); got != Allow {
		t.Errorf("plan mode should allow read, got %v", got)
	}

	e.Mode = ModeAuto
	if got := e.Check("bash", input("command", "make")); got != Allow {
		t.Errorf("auto mode should allow, got %v", got)
	}
	if got := e.Check("bash", input("command", "rm -rf /")); got != Deny {
		t.Errorf("deny rules apply even in auto mode, got %v", got)
	}
}
