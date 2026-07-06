// Package hook runs user-configured lifecycle shell commands. Hooks get
// the event payload as JSON on stdin and can veto an action by exiting 2.
package hook

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os/exec"
	"path"
	"strings"
	"time"

	"github.com/tamnd/kaku/pkg/config"
)

// Lifecycle events hooks can register for.
const (
	EventPreTool    = "pre_tool"
	EventPostTool   = "post_tool"
	EventUserPrompt = "user_prompt"
	EventStop       = "stop"
)

const (
	commandTimeout = 30 * time.Second
	maxStderr      = 2000
)

// Result is the outcome of running the hooks for one event.
type Result struct {
	Block   bool   // a hook vetoed the action
	Message string // stderr of the blocking hook, or accumulated warnings
}

// Runner executes configured hooks.
type Runner struct {
	Hooks map[string][]config.Hook // keyed by event
	Dir   string                   // working directory for hook commands
}

// Run executes every hook registered for event whose Match glob matches
// tool. An empty Match matches everything; tool is "" for non-tool
// events. The payload is marshalled to JSON and piped to each command's
// stdin. Exit 0 is fine, exit 2 blocks immediately, anything else becomes
// a warning appended to Message. The error return is for marshalling
// failures only.
func (r *Runner) Run(ctx context.Context, event, tool string, payload any) (Result, error) {
	hooks := r.Hooks[event]
	if len(hooks) == 0 {
		return Result{}, nil
	}
	data, err := json.Marshal(payload)
	if err != nil {
		return Result{}, err
	}
	var warnings []string
	for _, h := range hooks {
		if !matches(h.Match, tool) {
			continue
		}
		code, stderr, runErr := r.exec(ctx, h.Command, data)
		if runErr == nil {
			continue
		}
		if code == 2 {
			return Result{Block: true, Message: stderr}, nil
		}
		warnings = append(warnings, fmt.Sprintf("hook %s: %v: %s", h.Command, runErr, oneLine(stderr)))
	}
	return Result{Message: strings.Join(warnings, "\n")}, nil
}

func matches(pattern, tool string) bool {
	if pattern == "" {
		return true
	}
	ok, err := path.Match(pattern, tool)
	return err == nil && ok
}

// exec runs one hook command under bash with the payload on stdin and a
// hard per-command timeout on top of the caller's context.
func (r *Runner) exec(ctx context.Context, command string, stdin []byte) (code int, stderr string, err error) {
	cctx, cancel := context.WithTimeout(ctx, commandTimeout)
	defer cancel()

	cmd := exec.CommandContext(cctx, "bash", "-c", command)
	cmd.Dir = r.Dir
	cmd.Stdin = bytes.NewReader(stdin)
	var errBuf bytes.Buffer
	cmd.Stderr = &errBuf
	err = cmd.Run()

	stderr = errBuf.String()
	if len(stderr) > maxStderr {
		stderr = stderr[:maxStderr]
	}
	stderr = strings.TrimSpace(stderr)

	code = -1
	if err == nil {
		code = 0
	} else {
		var ee *exec.ExitError
		if errors.As(err, &ee) {
			code = ee.ExitCode()
		}
	}
	return code, stderr, err
}

func oneLine(s string) string {
	return strings.Join(strings.Fields(s), " ")
}
