package builtin

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os/exec"
	"time"

	"github.com/tamnd/kaku/pkg/tool"
)

const (
	bashDefaultTimeout = 120 * time.Second
	bashMaxTimeout     = 600 * time.Second
	bashMaxOutput      = 30000
	bashHeadOutput     = 15000
	bashTailOutput     = 10000
)

// BashSandboxed is the bash tool with OS-level write confinement: Seatbelt
// on macOS, landlock on Linux. Registering it replaces the plain bash tool.
func BashSandboxed(workdir string) tool.Tool {
	return bashTool(workdir, true)
}

func bashTool(workdir string, sandbox bool) tool.Tool {
	return tool.Func{
		ToolName: "bash",
		Desc:     "Run a bash command in the working directory and return its combined stdout and stderr. Commands time out after 120 seconds by default (timeout_ms caps at 600000); long output is truncated in the middle.",
		InputSchema: json.RawMessage(`{
  "type": "object",
  "properties": {
    "command": {"type": "string", "description": "The bash command to run."},
    "timeout_ms": {"type": "integer", "description": "Timeout in milliseconds. Defaults to 120000, capped at 600000."}
  },
  "required": ["command"]
}`),
		Fn: func(ctx context.Context, input json.RawMessage) (string, error) {
			var in struct {
				Command   string `json:"command"`
				TimeoutMS int    `json:"timeout_ms"`
			}
			if err := json.Unmarshal(input, &in); err != nil {
				return "", fmt.Errorf("bash: bad input: %w", err)
			}
			if in.Command == "" {
				return "", errors.New("bash: command is required")
			}
			timeout := bashDefaultTimeout
			if in.TimeoutMS > 0 {
				timeout = time.Duration(in.TimeoutMS) * time.Millisecond
			}
			if timeout > bashMaxTimeout {
				timeout = bashMaxTimeout
			}
			ctx2, cancel := context.WithTimeout(ctx, timeout)
			defer cancel()

			argv := []string{"bash", "-c", in.Command}
			if sandbox {
				var err error
				if argv, err = sandboxArgv(workdir, in.Command); err != nil {
					return "", fmt.Errorf("bash: sandbox unavailable: %w", err)
				}
			}
			cmd := exec.CommandContext(ctx2, argv[0], argv[1:]...)
			cmd.Dir = workdir
			setupProcessGroup(cmd)

			out, err := cmd.CombinedOutput()
			text := truncateOutput(string(out))
			if err != nil {
				if ctx2.Err() == context.DeadlineExceeded {
					return "", fmt.Errorf("bash: command timed out after %s\n%s", timeout, text)
				}
				// The engine feeds error text back to the model, so the
				// exit status and output both have to live in the message.
				return "", fmt.Errorf("bash: %v\n%s", err, text)
			}
			if text == "" {
				return "(no output)", nil
			}
			return text, nil
		},
	}
}

func truncateOutput(s string) string {
	if len(s) <= bashMaxOutput {
		return s
	}
	return s[:bashHeadOutput] + "\n... [truncated] ...\n" + s[len(s)-bashTailOutput:]
}
