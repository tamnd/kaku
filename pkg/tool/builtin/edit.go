package builtin

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"strings"

	"github.com/tamnd/kaku/pkg/tool"
)

func editTool(workdir string, fmtr *Formatter, diag Diagnoser) tool.Tool {
	return tool.Func{
		ToolName: "edit",
		Desc:     "Replace an exact string in a file. old_string must match the file exactly and, unless replace_all is true, must appear exactly once; include surrounding lines to disambiguate.",
		InputSchema: json.RawMessage(`{
  "type": "object",
  "properties": {
    "file_path": {"type": "string", "description": "Path of the file to edit. Relative paths resolve against the working directory."},
    "old_string": {"type": "string", "description": "Exact text to replace, including whitespace and indentation."},
    "new_string": {"type": "string", "description": "Text to replace old_string with. Must differ from old_string."},
    "replace_all": {"type": "boolean", "description": "Replace every occurrence of old_string instead of requiring a unique match. Defaults to false."}
  },
  "required": ["file_path", "old_string", "new_string"]
}`),
		Fn: func(ctx context.Context, input json.RawMessage) (string, error) {
			var in struct {
				FilePath   string `json:"file_path"`
				OldString  string `json:"old_string"`
				NewString  string `json:"new_string"`
				ReplaceAll bool   `json:"replace_all"`
			}
			if err := json.Unmarshal(input, &in); err != nil {
				return "", fmt.Errorf("edit: bad input: %w", err)
			}
			if in.FilePath == "" {
				return "", errors.New("edit: file_path is required")
			}
			if in.OldString == in.NewString {
				return "", errors.New("edit: old_string and new_string are identical")
			}
			path := resolve(workdir, in.FilePath)
			info, err := os.Stat(path)
			if err != nil {
				return "", fmt.Errorf("edit: %w", err)
			}
			data, err := os.ReadFile(path)
			if err != nil {
				return "", fmt.Errorf("edit: %w", err)
			}
			content := string(data)
			count := strings.Count(content, in.OldString)
			if count == 0 {
				return "", fmt.Errorf("edit: old_string not found in %s", path)
			}
			if count > 1 && !in.ReplaceAll {
				return "", fmt.Errorf("edit: old_string matches %d times in %s; make it unique or set replace_all", count, path)
			}
			replaced := count
			if !in.ReplaceAll {
				replaced = 1
			}
			content = strings.ReplaceAll(content, in.OldString, in.NewString)
			if err := os.WriteFile(path, []byte(content), info.Mode().Perm()); err != nil {
				return "", fmt.Errorf("edit: %w", err)
			}
			fmtr.Format(path)
			result := fmt.Sprintf("replaced %d occurrence(s) in %s", replaced, path)
			return withDiagnostics(ctx, diag, path, result), nil
		},
	}
}
