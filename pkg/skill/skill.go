// Package skill loads markdown skill files, the bodies behind slash
// commands. A skill is a .md file with optional YAML frontmatter fenced
// by "---" lines at the top.
package skill

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/tamnd/kaku/pkg/mention"
	"gopkg.in/yaml.v3"
)

// Skill is one parsed skill file.
type Skill struct {
	Name        string
	Description string
	Model       string
	Source      string // file path
	Body        string // markdown after frontmatter
}

type frontmatter struct {
	Name        string `yaml:"name"`
	Description string `yaml:"description"`
	Model       string `yaml:"model"`
}

// Parse reads one skill file's content. The name defaults to the filename
// without .md and can be overridden in frontmatter.
func Parse(path string, data []byte) (Skill, error) {
	s := Skill{
		Name:   strings.TrimSuffix(filepath.Base(path), ".md"),
		Source: path,
	}
	raw, body, hasFM, err := splitFrontmatter(string(data))
	if err != nil {
		return Skill{}, fmt.Errorf("%s: %w", path, err)
	}
	if hasFM {
		var fm frontmatter
		if err := yaml.Unmarshal([]byte(raw), &fm); err != nil {
			return Skill{}, fmt.Errorf("%s: bad frontmatter: %w", path, err)
		}
		if fm.Name != "" {
			s.Name = fm.Name
		}
		s.Description = fm.Description
		s.Model = fm.Model
	}
	s.Body = body
	return s, nil
}

// splitFrontmatter separates the YAML block from the markdown body. An
// opening "---" with no closing line is an error rather than silently
// swallowing the whole file as frontmatter.
func splitFrontmatter(s string) (fm, body string, ok bool, err error) {
	first, rest, _ := strings.Cut(s, "\n")
	if strings.TrimRight(first, "\r") != "---" {
		return "", s, false, nil
	}
	var lines []string
	for {
		line, r, found := strings.Cut(rest, "\n")
		if strings.TrimRight(line, "\r") == "---" {
			return strings.Join(lines, "\n"), r, true, nil
		}
		if !found {
			return "", "", false, errors.New("unterminated frontmatter")
		}
		lines = append(lines, line)
		rest = r
	}
}

// Discover loads project skills from <dir>/.kaku/skills/*.md and user
// skills from <userDir>/*.md (pass "" to use ~/.kaku/skills). On a name
// clash the project skill wins. Files that fail to read or parse are
// skipped. The result is sorted by name.
func Discover(dir, userDir string) ([]Skill, error) {
	if userDir == "" {
		if home, err := os.UserHomeDir(); err == nil {
			userDir = filepath.Join(home, ".kaku", "skills")
		}
	}
	byName := map[string]Skill{}
	// User first so a project skill with the same name overwrites it.
	for _, d := range []string{userDir, filepath.Join(dir, ".kaku", "skills")} {
		if d == "" {
			continue
		}
		paths, err := filepath.Glob(filepath.Join(d, "*.md"))
		if err != nil {
			continue
		}
		for _, p := range paths {
			data, err := os.ReadFile(p)
			if err != nil {
				continue
			}
			s, err := Parse(p, data)
			if err != nil {
				continue
			}
			byName[s.Name] = s
		}
	}
	out := make([]Skill, 0, len(byName))
	for _, s := range byName {
		out = append(out, s)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out, nil
}

// Find looks a skill up by name.
func Find(skills []Skill, name string) (Skill, bool) {
	for _, s := range skills {
		if s.Name == name {
			return s, true
		}
	}
	return Skill{}, false
}

// Expand renders the skill body with args and interpolations, resolving files
// and shell commands relative to dir:
//
//   - $ARGUMENTS and $@ expand to all args.
//   - $1, $2, ... expand to positional args (quote-aware split).
//   - ${1:-default} uses a default when the positional arg is missing.
//   - !`command` runs the command and substitutes its output.
//   - @path inlines a file's contents.
//
// When the body references none of the argument placeholders and args is
// non-empty, the args are appended after an "Arguments:" label, preserving the
// original behavior for simple skills.
func (s Skill) Expand(args, dir string) string {
	body := s.Body
	positional := splitArgs(args)

	if !referencesArgs(body) && args != "" {
		body += "\n\nArguments: " + args
	}
	body = expandArgs(body, args, positional)
	body = expandShell(body, dir)
	body, _ = mention.Expand(dir, body)
	return body
}

// referencesArgs reports whether the body uses any argument placeholder.
func referencesArgs(body string) bool {
	if strings.Contains(body, "$ARGUMENTS") || strings.Contains(body, "$@") {
		return true
	}
	return argRef.MatchString(body)
}

var argRef = regexp.MustCompile(`\$\{?\d`)

// expandArgs substitutes the argument placeholders.
func expandArgs(body, all string, pos []string) string {
	body = strings.ReplaceAll(body, "$ARGUMENTS", all)
	body = strings.ReplaceAll(body, "$@", all)
	// ${N:-default}
	body = argDefault.ReplaceAllStringFunc(body, func(m string) string {
		g := argDefault.FindStringSubmatch(m)
		n, _ := strconv.Atoi(g[1])
		if n >= 1 && n <= len(pos) && pos[n-1] != "" {
			return pos[n-1]
		}
		return g[2]
	})
	// $N
	body = argPlain.ReplaceAllStringFunc(body, func(m string) string {
		n, _ := strconv.Atoi(m[1:])
		if n >= 1 && n <= len(pos) {
			return pos[n-1]
		}
		return ""
	})
	return body
}

var (
	argDefault = regexp.MustCompile(`\$\{(\d+):-([^}]*)\}`)
	argPlain   = regexp.MustCompile(`\$(\d+)`)
	shellRef   = regexp.MustCompile("!`([^`]*)`")
)

const shellCap = 30000

// expandShell runs each !`command` and substitutes its trimmed output. A
// failing command substitutes its output plus a note rather than aborting.
// Command bodies are user-authored, at the same trust level as a hook, and run
// unsandboxed in dir.
func expandShell(body, dir string) string {
	return shellRef.ReplaceAllStringFunc(body, func(m string) string {
		cmd := shellRef.FindStringSubmatch(m)[1]
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		c := exec.CommandContext(ctx, "bash", "-c", cmd)
		c.Dir = dir
		out, err := c.CombinedOutput()
		text := strings.TrimRight(string(out), "\n")
		if len(text) > shellCap {
			text = text[:shellCap] + "\n... [output truncated] ..."
		}
		if err != nil {
			return text + "\n[command failed: " + err.Error() + "]"
		}
		return text
	})
}

// splitArgs splits an argument string on whitespace, honoring single and
// double quotes so a quoted phrase stays one positional argument.
func splitArgs(s string) []string {
	var out []string
	var cur strings.Builder
	var quote rune
	inWord := false
	flush := func() {
		if inWord {
			out = append(out, cur.String())
			cur.Reset()
			inWord = false
		}
	}
	for _, r := range s {
		switch {
		case quote != 0:
			if r == quote {
				quote = 0
			} else {
				cur.WriteRune(r)
			}
		case r == '\'' || r == '"':
			quote = r
			inWord = true
		case r == ' ' || r == '\t' || r == '\n':
			flush()
		default:
			cur.WriteRune(r)
			inWord = true
		}
	}
	flush()
	return out
}
