package tui

import (
	"io/fs"
	"path/filepath"
	"sort"
	"strings"
)

// mentionPicker is the open @file overlay: the query typed after @, the ranked
// matches, and the highlighted row.
type mentionPicker struct {
	query   string
	matches []string
	cursor  int
}

// mentionMax caps how many matches the overlay shows at once.
const mentionMax = 8

// activeMention returns the trailing @token being typed, or "" when the tail of
// the input is not an open mention. The token must start the line or follow
// whitespace, so an email address like a@b does not trigger the picker.
func activeMention(s string) string {
	i := strings.LastIndexAny(s, " \t\n")
	tok := s[i+1:]
	if strings.HasPrefix(tok, "@") {
		return tok
	}
	return ""
}

// mentionSkipDir mirrors the glob tool's ignore set so the picker walks the
// same tree the model would search.
func mentionSkipDir(name string) bool {
	switch name {
	case ".git", "node_modules", "vendor", "dist":
		return true
	}
	return false
}

// scanFiles walks dir and returns repo-relative file paths, skipping the
// ignored directories. It caps the list so a huge tree cannot stall the UI.
func scanFiles(dir string) []string {
	const cap = 5000
	var out []string
	filepath.WalkDir(dir, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		if d.IsDir() {
			if path != dir && mentionSkipDir(d.Name()) {
				return filepath.SkipDir
			}
			return nil
		}
		rel, err := filepath.Rel(dir, path)
		if err != nil {
			return nil
		}
		out = append(out, rel)
		if len(out) >= cap {
			return filepath.SkipAll
		}
		return nil
	})
	return out
}

// rankMentions filters files by a fuzzy subsequence match against q and orders
// the hits: basename matches first, then shorter paths. An empty q returns the
// files sorted by path.
func rankMentions(files []string, q string) []string {
	if q == "" {
		out := append([]string(nil), files...)
		sort.Strings(out)
		return out
	}
	ql := strings.ToLower(q)
	type scored struct {
		path  string
		score int
	}
	var res []scored
	for _, f := range files {
		span, ok := subseqScore(strings.ToLower(f), ql)
		if !ok {
			continue
		}
		base := strings.ToLower(filepath.Base(f))
		s := span
		switch {
		case strings.HasPrefix(base, ql):
			s -= 1000
		case strings.Contains(base, ql):
			s -= 500
		}
		s += len(f)
		res = append(res, scored{f, s})
	}
	sort.SliceStable(res, func(i, j int) bool { return res[i].score < res[j].score })
	out := make([]string, len(res))
	for i, r := range res {
		out[i] = r.path
	}
	return out
}

// subseqScore reports whether every rune of q appears in s in order, and the
// index one past the last match as a rough compactness score (lower is better).
func subseqScore(s, q string) (int, bool) {
	i := 0
	last := 0
	for _, c := range q {
		j := strings.IndexRune(s[i:], c)
		if j < 0 {
			return 0, false
		}
		i += j + 1
		last = i
	}
	return last, true
}
