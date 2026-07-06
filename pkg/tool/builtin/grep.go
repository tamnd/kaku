package builtin

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/tamnd/kaku/pkg/tool"
)

const (
	grepDefaultMax = 100
	grepMaxFile    = 2 * 1024 * 1024
	grepMaxLine    = 400
	grepSniffLen   = 8192
)

func grepTool(workdir string) tool.Tool {
	return tool.Func{
		ToolName: "grep",
		Desc:     "Search file contents under a directory with a Go regular expression and return matching lines as path:line: text. Skips .git, node_modules, vendor, dist, binaries, and files over 2MB; use glob to restrict by file name.",
		Safe:     true,
		InputSchema: json.RawMessage(`{
  "type": "object",
  "properties": {
    "pattern": {"type": "string", "description": "Go regexp to search for in file contents."},
    "path": {"type": "string", "description": "Directory to search. Defaults to the working directory."},
    "glob": {"type": "string", "description": "Only search files whose base name matches this glob, e.g. *.go."},
    "max_results": {"type": "integer", "description": "Stop after this many matching lines. Defaults to 100."}
  },
  "required": ["pattern"]
}`),
		Fn: func(ctx context.Context, input json.RawMessage) (string, error) {
			var in struct {
				Pattern    string `json:"pattern"`
				Path       string `json:"path"`
				Glob       string `json:"glob"`
				MaxResults int    `json:"max_results"`
			}
			if err := json.Unmarshal(input, &in); err != nil {
				return "", fmt.Errorf("grep: bad input: %w", err)
			}
			if in.Pattern == "" {
				return "", errors.New("grep: pattern is required")
			}
			re, err := regexp.Compile(in.Pattern)
			if err != nil {
				return "", fmt.Errorf("grep: bad pattern: %w", err)
			}
			max := in.MaxResults
			if max < 1 {
				max = grepDefaultMax
			}
			root := resolve(workdir, in.Path)

			var results []string
			truncated := false
			err = fs.WalkDir(os.DirFS(root), ".", func(p string, d fs.DirEntry, walkErr error) error {
				if walkErr != nil {
					return nil
				}
				if d.IsDir() {
					if p != "." && skipDirName(d.Name()) {
						return fs.SkipDir
					}
					return nil
				}
				if !d.Type().IsRegular() {
					return nil
				}
				if in.Glob != "" {
					ok, _ := filepath.Match(in.Glob, d.Name())
					if !ok {
						return nil
					}
				}
				info, err := d.Info()
				if err != nil || info.Size() > grepMaxFile {
					return nil
				}
				data, err := os.ReadFile(filepath.Join(root, p))
				if err != nil {
					return nil
				}
				sniff := data
				if len(sniff) > grepSniffLen {
					sniff = sniff[:grepSniffLen]
				}
				if bytes.IndexByte(sniff, 0) >= 0 {
					return nil
				}
				for i, line := range strings.Split(string(data), "\n") {
					if !re.MatchString(line) {
						continue
					}
					if len(line) > grepMaxLine {
						line = line[:grepMaxLine]
					}
					results = append(results, fmt.Sprintf("%s:%d: %s", p, i+1, line))
					if len(results) >= max {
						truncated = true
						return fs.SkipAll
					}
				}
				return nil
			})
			if err != nil {
				return "", fmt.Errorf("grep: %w", err)
			}
			if len(results) == 0 {
				return "no matches", nil
			}
			out := strings.Join(results, "\n")
			if truncated {
				out += fmt.Sprintf("\n(truncated at %d results)", max)
			}
			return out, nil
		},
	}
}
