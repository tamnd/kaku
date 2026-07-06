package builtin

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strings"

	"github.com/tamnd/kaku/pkg/tool"
)

const lsMaxEntries = 500

func lsTool(workdir string) tool.Tool {
	return tool.Func{
		ToolName: "ls",
		Desc:     "List the entries of a directory in sorted order, with a trailing / on directories. Use it to orient yourself before reading or editing files.",
		Safe:     true,
		InputSchema: json.RawMessage(`{
  "type": "object",
  "properties": {
    "path": {"type": "string", "description": "Directory to list. Defaults to the working directory."}
  },
  "required": []
}`),
		Fn: func(ctx context.Context, input json.RawMessage) (string, error) {
			var in struct {
				Path string `json:"path"`
			}
			if err := json.Unmarshal(input, &in); err != nil {
				return "", fmt.Errorf("ls: bad input: %w", err)
			}
			dir := resolve(workdir, in.Path)
			entries, err := os.ReadDir(dir)
			if err != nil {
				return "", fmt.Errorf("ls: %w", err)
			}
			if len(entries) == 0 {
				return fmt.Sprintf("%s is empty", dir), nil
			}
			total := len(entries)
			capped := false
			if total > lsMaxEntries {
				entries = entries[:lsMaxEntries]
				capped = true
			}
			var b strings.Builder
			for i, e := range entries {
				if i > 0 {
					b.WriteByte('\n')
				}
				b.WriteString(e.Name())
				if e.IsDir() {
					b.WriteByte('/')
				}
			}
			if capped {
				fmt.Fprintf(&b, "\n(showing first %d of %d entries)", lsMaxEntries, total)
			}
			return b.String(), nil
		},
	}
}
