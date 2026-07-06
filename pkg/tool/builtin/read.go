package builtin

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"strings"

	"github.com/tamnd/kaku/pkg/tool"
)

const (
	readDefaultLimit = 2000
	readMaxLineLen   = 2000
)

func readTool(workdir string) tool.Tool {
	return tool.Func{
		ToolName: "read",
		Desc:     "Read a file and return its contents with 1-based line numbers. Use offset and limit to page through large files; by default the first 2000 lines are returned and lines over 2000 characters are truncated.",
		Safe:     true,
		InputSchema: json.RawMessage(`{
  "type": "object",
  "properties": {
    "file_path": {"type": "string", "description": "Path to the file to read. Relative paths resolve against the working directory."},
    "offset": {"type": "integer", "description": "1-based line number to start reading from. Defaults to 1."},
    "limit": {"type": "integer", "description": "Maximum number of lines to return. Defaults to 2000."}
  },
  "required": ["file_path"]
}`),
		Fn: func(ctx context.Context, input json.RawMessage) (string, error) {
			var in struct {
				FilePath string `json:"file_path"`
				Offset   int    `json:"offset"`
				Limit    int    `json:"limit"`
			}
			if err := json.Unmarshal(input, &in); err != nil {
				return "", fmt.Errorf("read: bad input: %w", err)
			}
			if in.FilePath == "" {
				return "", errors.New("read: file_path is required")
			}
			path := resolve(workdir, in.FilePath)
			info, err := os.Stat(path)
			if err != nil {
				return "", fmt.Errorf("read: %w", err)
			}
			if info.IsDir() {
				return "", fmt.Errorf("read: %s is a directory", path)
			}
			if info.Size() == 0 {
				return fmt.Sprintf("%s is an empty file", path), nil
			}

			offset := in.Offset
			if offset < 1 {
				offset = 1
			}
			limit := in.Limit
			if limit < 1 {
				limit = readDefaultLimit
			}

			f, err := os.Open(path)
			if err != nil {
				return "", fmt.Errorf("read: %w", err)
			}
			defer f.Close()

			sc := bufio.NewScanner(f)
			sc.Buffer(make([]byte, 0, 64*1024), 10*1024*1024)
			var b strings.Builder
			lineno := 0
			shown := 0
			for sc.Scan() {
				lineno++
				if lineno < offset {
					continue
				}
				if shown >= limit {
					break
				}
				line := sc.Text()
				if len(line) > readMaxLineLen {
					line = line[:readMaxLineLen] + "..."
				}
				fmt.Fprintf(&b, "%6d\t%s\n", lineno, line)
				shown++
			}
			if err := sc.Err(); err != nil {
				return "", fmt.Errorf("read: %w", err)
			}
			if shown == 0 {
				return fmt.Sprintf("no lines to show: file has %d lines, offset was %d", lineno, offset), nil
			}
			return b.String(), nil
		},
	}
}
