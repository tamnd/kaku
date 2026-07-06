package builtin

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path"
	"sort"
	"strings"
	"time"

	"github.com/tamnd/kaku/pkg/tool"
)

const globMaxResults = 200

func globTool(workdir string) tool.Tool {
	return tool.Func{
		ToolName: "glob",
		Desc:     "Find files by name pattern under a directory, with ** matching any number of path segments (e.g. pkg/**/*.go). A pattern without a slash matches base names anywhere in the tree; results come back newest first by modification time.",
		Safe:     true,
		InputSchema: json.RawMessage(`{
  "type": "object",
  "properties": {
    "pattern": {"type": "string", "description": "Glob pattern to match against paths relative to the search root. Supports ** across directories."},
    "path": {"type": "string", "description": "Directory to search. Defaults to the working directory."}
  },
  "required": ["pattern"]
}`),
		Fn: func(ctx context.Context, input json.RawMessage) (string, error) {
			var in struct {
				Pattern string `json:"pattern"`
				Path    string `json:"path"`
			}
			if err := json.Unmarshal(input, &in); err != nil {
				return "", fmt.Errorf("glob: bad input: %w", err)
			}
			if in.Pattern == "" {
				return "", errors.New("glob: pattern is required")
			}
			root := resolve(workdir, in.Path)

			type hit struct {
				path  string
				mtime time.Time
			}
			var hits []hit
			err := fs.WalkDir(os.DirFS(root), ".", func(p string, d fs.DirEntry, walkErr error) error {
				if walkErr != nil {
					return nil
				}
				if d.IsDir() {
					if p != "." && skipDirName(d.Name()) {
						return fs.SkipDir
					}
					return nil
				}
				if !globMatch(in.Pattern, p) {
					return nil
				}
				info, err := d.Info()
				if err != nil {
					return nil
				}
				hits = append(hits, hit{path: p, mtime: info.ModTime()})
				return nil
			})
			if err != nil {
				return "", fmt.Errorf("glob: %w", err)
			}
			if len(hits) == 0 {
				return "no matches", nil
			}
			sort.SliceStable(hits, func(i, j int) bool { return hits[i].mtime.After(hits[j].mtime) })
			if len(hits) > globMaxResults {
				hits = hits[:globMaxResults]
			}
			var b strings.Builder
			for i, h := range hits {
				if i > 0 {
					b.WriteByte('\n')
				}
				b.WriteString(h.path)
			}
			return b.String(), nil
		},
	}
}

// globMatch matches pattern against a slash-separated relative path.
// A pattern with no slash matches the base name of any file in the tree.
func globMatch(pattern, p string) bool {
	if !strings.Contains(pattern, "/") {
		ok, _ := path.Match(pattern, path.Base(p))
		return ok
	}
	return matchSegments(strings.Split(pattern, "/"), strings.Split(p, "/"))
}

// matchSegments matches pattern segments against path segments, letting a
// ** segment span any number of path segments including zero.
func matchSegments(pat, segs []string) bool {
	if len(pat) == 0 {
		return len(segs) == 0
	}
	if pat[0] == "**" {
		for i := 0; i <= len(segs); i++ {
			if matchSegments(pat[1:], segs[i:]) {
				return true
			}
		}
		return false
	}
	if len(segs) == 0 {
		return false
	}
	ok, err := path.Match(pat[0], segs[0])
	if err != nil || !ok {
		return false
	}
	return matchSegments(pat[1:], segs[1:])
}
