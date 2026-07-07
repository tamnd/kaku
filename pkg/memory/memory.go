// Package memory gathers project instructions for the system prompt.
package memory

import (
	"os"
	"path/filepath"
	"sort"
	"strings"
)

const maxTotal = 48 * 1024

// instruction file names, first match per directory wins.
var names = []string{"KAKU.md", "AGENTS.md", "CLAUDE.md"}

// Instructions walks from dir up to the filesystem root collecting the
// nearest instruction files, then appends every .md fact file from
// dir/.kaku/memory/, then every file matched by the extra globs (resolved
// relative to dir). Returns "" when there is nothing. The 48KB budget is
// shared across all sources; once it is hit the rest are dropped with a note.
func Instructions(dir string, extra ...string) string {
	var b strings.Builder
	add := func(path string) bool {
		data, err := os.ReadFile(path)
		if err != nil || len(data) == 0 {
			return true
		}
		if b.Len()+len(data) > maxTotal {
			b.WriteString("\n[instructions truncated at 48KB]\n")
			return false
		}
		b.WriteString("# Instructions from ")
		b.WriteString(path)
		b.WriteString("\n\n")
		b.Write(data)
		b.WriteString("\n")
		return true
	}

	home, _ := os.UserHomeDir()
	stop := ""
	if home != "" {
		stop = filepath.Dir(home)
	}
	var files []string
	for d := dir; ; d = filepath.Dir(d) {
		for _, n := range names {
			p := filepath.Join(d, n)
			if st, err := os.Stat(p); err == nil && !st.IsDir() {
				files = append(files, p)
				break
			}
		}
		parent := filepath.Dir(d)
		if parent == d || d == stop {
			break
		}
	}
	// Nearest first is already the collection order.
	for _, f := range files {
		if !add(f) {
			return b.String()
		}
	}

	memDir := filepath.Join(dir, ".kaku", "memory")
	entries, err := os.ReadDir(memDir)
	if err == nil {
		var mds []string
		for _, e := range entries {
			if !e.IsDir() && strings.HasSuffix(e.Name(), ".md") {
				mds = append(mds, filepath.Join(memDir, e.Name()))
			}
		}
		sort.Strings(mds)
		for _, f := range mds {
			if !add(f) {
				return b.String()
			}
		}
	}

	for _, g := range extra {
		if !filepath.IsAbs(g) {
			g = filepath.Join(dir, g)
		}
		matches, err := filepath.Glob(g)
		if err != nil {
			continue
		}
		sort.Strings(matches)
		for _, f := range matches {
			if st, err := os.Stat(f); err != nil || st.IsDir() {
				continue
			}
			if !add(f) {
				return b.String()
			}
		}
	}
	return b.String()
}
