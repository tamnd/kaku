// Package skill loads markdown skill files, the bodies behind slash
// commands. A skill is a .md file with optional YAML frontmatter fenced
// by "---" lines at the top.
package skill

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

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

// Expand renders the skill body with args: every "$ARGUMENTS" occurrence
// is replaced; if the body has no $ARGUMENTS and args is non-empty, the
// args are appended after an "Arguments:" label.
func (s Skill) Expand(args string) string {
	if strings.Contains(s.Body, "$ARGUMENTS") {
		return strings.ReplaceAll(s.Body, "$ARGUMENTS", args)
	}
	if args != "" {
		return s.Body + "\n\nArguments: " + args
	}
	return s.Body
}
