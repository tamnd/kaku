package agentdef

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/tamnd/kaku/pkg/perm"
	"github.com/tamnd/kaku/pkg/tool"
)

func TestParsePermissionBlock(t *testing.T) {
	src := []byte(`---
name: reviewer
description: reads and comments, never edits
permission:
  edit: deny
  bash: allow
  read: ask
---
You review code.`)
	d, err := parse("reviewer", src)
	if err != nil {
		t.Fatal(err)
	}
	// edit: deny expands to deny rules for edit and write.
	denies := map[string]bool{}
	for _, r := range d.Deny {
		denies[r.Tool] = true
	}
	if !denies["edit"] || !denies["write"] {
		t.Errorf("edit deny should cover edit and write, got %+v", d.Deny)
	}
	// bash: allow lands in Allow.
	if len(d.Allow) != 1 || d.Allow[0].Tool != "bash" {
		t.Errorf("bash allow missing, got %+v", d.Allow)
	}
	// read: ask adds no rule.
	for _, r := range append(d.Allow, d.Deny...) {
		if r.Tool == "read" || r.Tool == "ls" {
			t.Errorf("ask action should add no rule, got %+v", r)
		}
	}
}

func TestParseSubagentParityFields(t *testing.T) {
	src := []byte(`---
name: tuner
temperature: 0.2
top_p: 0.9
hidden: true
steps: 5
---
You are tuned.`)
	d, err := parse("tuner", src)
	if err != nil {
		t.Fatal(err)
	}
	if d.Temperature != 0.2 {
		t.Errorf("Temperature = %v, want 0.2", d.Temperature)
	}
	if d.TopP != 0.9 {
		t.Errorf("TopP = %v, want 0.9", d.TopP)
	}
	if !d.Hidden {
		t.Error("Hidden should be true")
	}
	if d.Steps != 5 {
		t.Errorf("Steps = %d, want 5", d.Steps)
	}
}

func TestHiddenAgentAbsentFromToolList(t *testing.T) {
	defs := []Def{
		{Name: "visible", Description: "shows up"},
		{Name: "secret", Description: "hidden helper", Hidden: true},
	}
	tf, ok := Tool(defs, Parent{}).(tool.Func)
	if !ok {
		t.Fatal("agent tool should be a tool.Func")
	}
	schema := string(tf.InputSchema)
	if !strings.Contains(schema, "visible") {
		t.Errorf("visible agent should be listed: %s", schema)
	}
	if strings.Contains(schema, "secret") {
		t.Errorf("hidden agent should not be listed: %s", schema)
	}
}

func TestSubagentPermissionDeniesUnderAuto(t *testing.T) {
	// A reviewer that denies edits must be denied even when the parent runs in
	// auto mode where everything is otherwise allowed.
	def := Def{Deny: perm.ParseRules([]string{"edit"})}
	parent := &perm.Engine{Mode: perm.ModeAuto}
	eng := &perm.Engine{
		Mode:  parent.Mode,
		Allow: append(append([]perm.Rule{}, def.Allow...), parent.Allow...),
		Deny:  append(append([]perm.Rule{}, def.Deny...), parent.Deny...),
	}
	if got := eng.Check("edit", json.RawMessage(`{"file_path":"main.go"}`)); got != perm.Deny {
		t.Errorf("subagent edit should be denied under auto, got %v", got)
	}
	if got := eng.Check("write", json.RawMessage(`{"file_path":"main.go"}`)); got != perm.Deny {
		t.Errorf("subagent write should be denied via edit category, got %v", got)
	}
	if got := eng.Check("bash", json.RawMessage(`{"command":"ls"}`)); got != perm.Allow {
		t.Errorf("bash stays allowed for the subagent, got %v", got)
	}
}
