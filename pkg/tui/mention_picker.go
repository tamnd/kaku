package tui

import (
	"io/fs"
	"path/filepath"
	"sort"
	"strings"
)

// completionKind marks what the popup is completing so one widget serves both
// @file and /command entry (2087/ux/05).
type completionKind int

const (
	compFile    completionKind = iota // @file paths
	compCommand                       // /slash commands
)

// completionItem is one row: value is inserted on accept, label is shown, desc
// is a dimmed right-hand hint (a command description; empty for files).
type completionItem struct {
	value string
	label string
	desc  string
}

// mentionPicker is the open completion overlay shared by @file and /command: the
// query being typed, the ranked items, and the highlighted row.
type mentionPicker struct {
	kind   completionKind
	query  string
	items  []completionItem
	cursor int
}

// mentionMax caps how many rows the overlay shows at once (2087/ux/05 keeps the
// popup to 1-10 rows; 8 leaves room for the header line).
const mentionMax = 8

// slashCommand is one entry in the /command completion source.
type slashCommand struct {
	name string
	desc string
}

// slashCommands is the completion source for /command entry, mirroring the
// switch in slash and the help body (2087/ux/05).
var slashCommands = []slashCommand{
	{"/help", "show the command help"},
	{"/model", "switch model or open the picker"},
	{"/skills", "list available skills"},
	{"/compact", "summarize history to save tokens"},
	{"/init", "scan the repo and write KAKU.md"},
	{"/new", "start a fresh session"},
	{"/sessions", "switch between saved sessions"},
	{"/sidebar", "toggle the resource sidebar"},
	{"/name", "rename the current session"},
	{"/theme", "switch the color theme"},
	{"/thinking", "show or set the reasoning level"},
	{"/export", "write the session to md, html, or json"},
	{"/clear", "clear the conversation"},
	{"/quit", "exit kaku"},
}

// activeCommand returns the /command token being typed when the composer holds a
// single leading-slash word, or "" otherwise. A slash mid-message (a path) or a
// space after the command does not trigger the popup (2087/ux/05).
func activeCommand(s string) string {
	if !strings.HasPrefix(s, "/") {
		return ""
	}
	if strings.ContainsAny(s, " \t\n") {
		return ""
	}
	return s
}

// rankCommands filters the command list by prefix then subsequence against q
// (the leading slash included), exact-prefix first (2087/ux/05).
func rankCommands(q string) []completionItem {
	ql := strings.ToLower(q)
	type scored struct {
		cmd  slashCommand
		tier int
	}
	var res []scored
	for _, c := range slashCommands {
		name := strings.ToLower(c.name)
		switch {
		case ql == "/" || strings.HasPrefix(name, ql):
			res = append(res, scored{c, 0})
		default:
			if _, ok := subseqScore(name, ql); ok {
				res = append(res, scored{c, 1})
			}
		}
	}
	sort.SliceStable(res, func(i, j int) bool {
		if res[i].tier != res[j].tier {
			return res[i].tier < res[j].tier
		}
		return res[i].cmd.name < res[j].cmd.name
	})
	out := make([]completionItem, len(res))
	for i, r := range res {
		out[i] = completionItem{value: r.cmd.name, label: r.cmd.name, desc: r.cmd.desc}
	}
	return out
}

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

// Ranking tiers, best first. A match lands in the first tier that fits, so the
// file being typed floats to the top before looser subsequence hits
// (2087/ux/05).
const (
	tierExactBase = iota // basename equals the query
	tierPrefixBase       // basename starts with the query
	tierSegment          // any path segment starts with the query
	tierContains         // basename contains the query
	tierFallback         // subsequence match only
)

// rankMentions filters files against q and orders the hits in explicit tiers:
// exact basename, basename prefix, path-segment prefix, basename contains, then
// a fuzzy subsequence fallback. Within a tier the order is stable by path
// length then lexical, so the shortest, closest path wins. An empty q returns
// the files sorted by path (2087/ux/05).
func rankMentions(files []string, q string) []string {
	if q == "" {
		out := append([]string(nil), files...)
		sort.Strings(out)
		return out
	}
	ql := strings.ToLower(q)
	type scored struct {
		path string
		tier int
	}
	var res []scored
	for _, f := range files {
		lf := strings.ToLower(f)
		if _, ok := subseqScore(lf, ql); !ok {
			continue
		}
		res = append(res, scored{f, mentionTier(lf, ql)})
	}
	sort.SliceStable(res, func(i, j int) bool {
		if res[i].tier != res[j].tier {
			return res[i].tier < res[j].tier
		}
		if len(res[i].path) != len(res[j].path) {
			return len(res[i].path) < len(res[j].path)
		}
		return res[i].path < res[j].path
	})
	out := make([]string, len(res))
	for i, r := range res {
		out[i] = r.path
	}
	return out
}

// mentionTier classifies a lowercased path against a lowercased query. The
// caller has already confirmed a subsequence match, so the worst case is the
// fallback tier.
func mentionTier(lf, ql string) int {
	base := filepath.Base(lf)
	switch {
	case base == ql:
		return tierExactBase
	case strings.HasPrefix(base, ql):
		return tierPrefixBase
	}
	for _, seg := range strings.Split(lf, "/") {
		if strings.HasPrefix(seg, ql) {
			return tierSegment
		}
	}
	if strings.Contains(base, ql) {
		return tierContains
	}
	return tierFallback
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
