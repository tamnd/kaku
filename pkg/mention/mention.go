// Package mention expands @file references in a prompt into the file's
// contents, so "explain @main.go" reaches the model with the file inlined.
package mention

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

// Caps keep a stray @bigfile or a prompt full of mentions from blowing the
// context. Per file and across all files in one prompt.
const (
	maxPerFile = 100 * 1024
	maxTotal   = 256 * 1024
)

// token matches @ followed by a path: letters, digits, and the punctuation
// that appears in real paths. A trailing dot is trimmed so "@main.go." at the
// end of a sentence still resolves.
var token = regexp.MustCompile(`@([A-Za-z0-9._~/-]+)`)

// Expand replaces every @path token whose target is a readable file with the
// file's contents wrapped in a <file path="..."> block, and returns the
// rewritten text plus the list of paths it inlined. Paths resolve relative to
// dir (or absolute, or ~-rooted). A token that does not resolve to a readable
// file is left untouched, so an email address or a decorator survives.
func Expand(dir, text string) (string, []string) {
	total := 0
	var inlined []string
	out := token.ReplaceAllStringFunc(text, func(m string) string {
		raw := strings.TrimSuffix(m[1:], ".")
		path := resolve(dir, raw)
		if path == "" {
			return m
		}
		info, err := os.Stat(path)
		if err != nil || info.IsDir() || info.Size() > maxPerFile {
			return m
		}
		if total+int(info.Size()) > maxTotal {
			return m
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return m
		}
		total += len(data)
		inlined = append(inlined, raw)
		return "<file path=\"" + raw + "\">\n" + string(data) + "\n</file>"
	})
	return out, inlined
}

// resolve turns a mention target into an absolute path, or "" when it does not
// point at an existing file.
func resolve(dir, raw string) string {
	p := raw
	if strings.HasPrefix(p, "~/") {
		if home, err := os.UserHomeDir(); err == nil {
			p = filepath.Join(home, p[2:])
		}
	}
	if !filepath.IsAbs(p) {
		p = filepath.Join(dir, p)
	}
	if _, err := os.Stat(p); err != nil {
		return ""
	}
	return p
}
