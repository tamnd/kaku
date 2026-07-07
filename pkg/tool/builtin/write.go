package builtin

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"github.com/tamnd/kaku/pkg/tool"
)

func writeTool(workdir string, fmtr *Formatter, diag Diagnoser) tool.Tool {
	return tool.Func{
		ToolName: "write",
		Desc:     "Write content to a file, creating parent directories as needed and overwriting any existing file. For small changes to an existing file prefer the edit tool.",
		InputSchema: json.RawMessage(`{
  "type": "object",
  "properties": {
    "file_path": {"type": "string", "description": "Path of the file to write. Relative paths resolve against the working directory."},
    "content": {"type": "string", "description": "Full content to write to the file."}
  },
  "required": ["file_path", "content"]
}`),
		Fn: func(ctx context.Context, input json.RawMessage) (string, error) {
			var in struct {
				FilePath string `json:"file_path"`
				Content  string `json:"content"`
			}
			if err := json.Unmarshal(input, &in); err != nil {
				return "", fmt.Errorf("write: bad input: %w", err)
			}
			if in.FilePath == "" {
				return "", errors.New("write: file_path is required")
			}
			path := resolve(workdir, in.FilePath)
			if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
				return "", fmt.Errorf("write: %w", err)
			}
			if err := os.WriteFile(path, []byte(in.Content), 0o644); err != nil {
				return "", fmt.Errorf("write: %w", err)
			}
			fmtr.Format(path)
			result := fmt.Sprintf("wrote %d bytes to %s", len(in.Content), path)
			return withDiagnostics(ctx, diag, path, result), nil
		},
	}
}
