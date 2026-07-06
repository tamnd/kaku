package hook

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/tamnd/kaku/pkg/config"
)

func runner(hooks map[string][]config.Hook) *Runner {
	return &Runner{Hooks: hooks}
}

func TestMatchFiltering(t *testing.T) {
	dir := t.TempDir()
	bashOut := filepath.Join(dir, "bash.txt")
	editOut := filepath.Join(dir, "edit.txt")
	r := runner(map[string][]config.Hook{
		EventPreTool: {
			{Match: "Bash*", Command: fmt.Sprintf("echo ran > %s", bashOut)},
			{Match: "Edit", Command: fmt.Sprintf("echo ran > %s", editOut)},
		},
	})
	res, err := r.Run(context.Background(), EventPreTool, "Bash", nil)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if res.Block || res.Message != "" {
		t.Errorf("unexpected result: %+v", res)
	}
	if _, err := os.Stat(bashOut); err != nil {
		t.Error("matching hook did not run")
	}
	if _, err := os.Stat(editOut); err == nil {
		t.Error("non-matching hook ran")
	}
}

func TestStdinPayload(t *testing.T) {
	out := filepath.Join(t.TempDir(), "payload.json")
	r := runner(map[string][]config.Hook{
		EventPostTool: {{Command: fmt.Sprintf("cat > %s", out)}},
	})
	payload := map[string]string{"tool": "Bash", "output": "ok"}
	if _, err := r.Run(context.Background(), EventPostTool, "Bash", payload); err != nil {
		t.Fatalf("Run: %v", err)
	}
	data, err := os.ReadFile(out)
	if err != nil {
		t.Fatalf("hook did not write payload: %v", err)
	}
	var got map[string]string
	if err := json.Unmarshal(data, &got); err != nil {
		t.Fatalf("payload is not JSON: %v (%q)", err, data)
	}
	if got["tool"] != "Bash" || got["output"] != "ok" {
		t.Errorf("payload = %v", got)
	}
}

func TestExit2Blocks(t *testing.T) {
	after := filepath.Join(t.TempDir(), "after.txt")
	r := runner(map[string][]config.Hook{
		EventPreTool: {
			{Command: "echo not allowed >&2; exit 2"},
			{Command: fmt.Sprintf("echo ran > %s", after)},
		},
	})
	res, err := r.Run(context.Background(), EventPreTool, "Bash", nil)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if !res.Block {
		t.Fatal("exit 2 should block")
	}
	if res.Message != "not allowed" {
		t.Errorf("Message = %q, want blocking hook's stderr", res.Message)
	}
	if _, err := os.Stat(after); err == nil {
		t.Error("hooks after a block should not run")
	}
}

func TestNonBlockingFailureWarns(t *testing.T) {
	r := runner(map[string][]config.Hook{
		EventStop: {
			{Command: "echo oops >&2; exit 1"},
			{Command: "true"},
		},
	})
	res, err := r.Run(context.Background(), EventStop, "", nil)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if res.Block {
		t.Error("exit 1 should not block")
	}
	if !strings.Contains(res.Message, "hook echo oops >&2; exit 1:") ||
		!strings.Contains(res.Message, "exit status 1") ||
		!strings.Contains(res.Message, "oops") {
		t.Errorf("Message = %q", res.Message)
	}
	if strings.Contains(res.Message, "\n") {
		t.Errorf("single warning should be one line: %q", res.Message)
	}
}

func TestTimeout(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 300*time.Millisecond)
	defer cancel()
	r := runner(map[string][]config.Hook{
		EventPreTool: {{Command: "sleep 30"}},
	})
	start := time.Now()
	res, err := r.Run(ctx, EventPreTool, "Bash", nil)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if elapsed := time.Since(start); elapsed > 5*time.Second {
		t.Fatalf("hook was not killed by context deadline (took %v)", elapsed)
	}
	if res.Block {
		t.Error("timeout should warn, not block")
	}
	if !strings.Contains(res.Message, "hook sleep 30:") {
		t.Errorf("Message = %q, want timeout warning", res.Message)
	}
}

func TestUserPromptEmptyTool(t *testing.T) {
	out := filepath.Join(t.TempDir(), "prompt.txt")
	r := runner(map[string][]config.Hook{
		EventUserPrompt: {{Command: fmt.Sprintf("cat > %s", out)}},
	})
	if _, err := r.Run(context.Background(), EventUserPrompt, "", map[string]string{"prompt": "hi"}); err != nil {
		t.Fatalf("Run: %v", err)
	}
	if _, err := os.Stat(out); err != nil {
		t.Error("empty-Match hook should run for user_prompt with empty tool")
	}
}

func TestNoHooksForEvent(t *testing.T) {
	r := runner(nil)
	res, err := r.Run(context.Background(), EventStop, "", nil)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if res.Block || res.Message != "" {
		t.Errorf("unexpected result: %+v", res)
	}
}

func TestMarshalError(t *testing.T) {
	r := runner(map[string][]config.Hook{
		EventStop: {{Command: "true"}},
	})
	if _, err := r.Run(context.Background(), EventStop, "", make(chan int)); err == nil {
		t.Error("unmarshallable payload should return an error")
	}
}

func TestStderrTrimmed(t *testing.T) {
	r := runner(map[string][]config.Hook{
		EventPreTool: {{Command: "yes x 2>/dev/null | head -c 5000 >&2; exit 2"}},
	})
	res, err := r.Run(context.Background(), EventPreTool, "Bash", nil)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if !res.Block {
		t.Fatal("expected block")
	}
	if len(res.Message) > 2000 {
		t.Errorf("stderr not trimmed: %d chars", len(res.Message))
	}
}

func TestHookRunsInDir(t *testing.T) {
	dir := t.TempDir()
	r := &Runner{
		Dir: dir,
		Hooks: map[string][]config.Hook{
			EventPostTool: {{Command: "pwd > here.txt"}},
		},
	}
	if _, err := r.Run(context.Background(), EventPostTool, "Edit", nil); err != nil {
		t.Fatalf("Run: %v", err)
	}
	data, err := os.ReadFile(filepath.Join(dir, "here.txt"))
	if err != nil {
		t.Fatalf("hook did not run in Dir: %v", err)
	}
	got := strings.TrimSpace(string(data))
	want, _ := filepath.EvalSymlinks(dir)
	gotResolved, _ := filepath.EvalSymlinks(got)
	if gotResolved != want {
		t.Errorf("hook cwd = %q, want %q", got, want)
	}
}
