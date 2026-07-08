package tui

import (
	"encoding/json"
	"maps"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/charmbracelet/lipgloss"
)

// Theme colors the TUI. Every field is a hex string ("#8be9fd"), an ANSI
// palette index ("6"), or "" for the terminal's own default. The set is
// deliberately small: kaku's TUI has far fewer surfaces than a full editor, so
// a dozen roles cover it.
type Theme struct {
	Name      string `json:"name,omitempty"`
	Primary   string `json:"primary,omitempty"`   // the user's own turns, selections
	Secondary string `json:"secondary,omitempty"` // reserved for future accents
	Accent    string `json:"accent,omitempty"`    // prompt caret, dialog border
	Error     string `json:"error,omitempty"`
	Warning   string `json:"warning,omitempty"` // tool activity, permission prompts
	Success   string `json:"success,omitempty"`
	Text      string `json:"text,omitempty"`  // assistant output ("" = default fg)
	Muted     string `json:"muted,omitempty"` // hints, footer, info lines
	Border    string `json:"border,omitempty"`

	// Diff colors the add/del lines of a diff body and permission preview. Empty
	// fields fall back to Success and Error, so existing themes need no change.
	Diff struct {
		Add string `json:"add,omitempty"`
		Del string `json:"del,omitempty"`
	} `json:"diff,omitzero"`
}

// builtinThemes are always available by name, even with no theme files on disk.
// dark matches kaku's original hardcoded palette so existing users see no change.
var builtinThemes = map[string]Theme{
	"dark": {
		Name: "dark", Primary: "6", Secondary: "4", Accent: "5",
		Error: "1", Warning: "3", Success: "2", Text: "", Muted: "8", Border: "8",
	},
	"light": {
		Name: "light", Primary: "4", Secondary: "6", Accent: "5",
		Error: "1", Warning: "3", Success: "2", Text: "0", Muted: "8", Border: "8",
	},
}

// LoadThemes returns the builtin themes plus any *.json themes found under the
// given directories. A file's stem is its name unless the JSON sets one, and a
// custom theme may shadow a builtin of the same name.
func LoadThemes(dirs ...string) map[string]Theme {
	out := make(map[string]Theme, len(builtinThemes))
	maps.Copy(out, builtinThemes)
	for _, dir := range dirs {
		entries, err := os.ReadDir(dir)
		if err != nil {
			continue
		}
		for _, e := range entries {
			if e.IsDir() || !strings.HasSuffix(e.Name(), ".json") {
				continue
			}
			data, err := os.ReadFile(filepath.Join(dir, e.Name()))
			if err != nil {
				continue
			}
			var t Theme
			if json.Unmarshal(data, &t) != nil {
				continue
			}
			name := t.Name
			if name == "" {
				name = strings.TrimSuffix(e.Name(), ".json")
			}
			t.Name = name
			out[name] = t
		}
	}
	return out
}

// themeNames lists the available theme names in a stable order.
func themeNames(themes map[string]Theme) []string {
	names := make([]string, 0, len(themes))
	for n := range themes {
		names = append(names, n)
	}
	sort.Strings(names)
	return names
}

// styles are the concrete lipgloss styles the render code uses, derived from a
// theme so a switch recolors everything at once.
type styles struct {
	user, tool, dim, err, foot, prompt        lipgloss.Style
	dialogTitle, dialogHint, dialogDesc, pick lipgloss.Style
	borderAccent, borderError, borderWarn     lipgloss.Color

	// Rich transcript styles (2087/ux). Tool status glyphs, the thinking box,
	// the error tag, and diff add/del lines.
	toolRunning, toolOK, toolErr, toolWarn lipgloss.Style
	thinkBox, thinkFoot                    lipgloss.Style
	errTag, warnTag                        lipgloss.Style
	diffAdd, diffDel, diffHead             lipgloss.Style
	lineNum                                lipgloss.Style
}

func color(s string) lipgloss.Color { return lipgloss.Color(s) }

// newStyles builds the style set for a theme.
func newStyles(t Theme) styles {
	return styles{
		user:         lipgloss.NewStyle().Foreground(color(t.Primary)).Bold(true),
		tool:         lipgloss.NewStyle().Foreground(color(t.Warning)),
		dim:          lipgloss.NewStyle().Foreground(color(t.Muted)),
		err:          lipgloss.NewStyle().Foreground(color(t.Error)),
		foot:         lipgloss.NewStyle().Foreground(color(t.Muted)),
		prompt:       lipgloss.NewStyle().Foreground(color(t.Accent)),
		dialogTitle:  lipgloss.NewStyle().Bold(true).Foreground(color(t.Text)),
		dialogHint:   lipgloss.NewStyle().Foreground(color(t.Muted)),
		dialogDesc:   lipgloss.NewStyle().Foreground(color(t.Muted)),
		pick:         lipgloss.NewStyle().Foreground(color(t.Primary)).Bold(true),
		borderAccent: color(t.Accent),
		borderError:  color(t.Error),
		borderWarn:   color(t.Warning),

		toolRunning: lipgloss.NewStyle().Foreground(color(t.Warning)),
		toolOK:      lipgloss.NewStyle().Foreground(color(t.Success)),
		toolErr:     lipgloss.NewStyle().Foreground(color(t.Error)),
		toolWarn:    lipgloss.NewStyle().Foreground(color(t.Warning)),
		thinkBox: lipgloss.NewStyle().Foreground(color(t.Muted)).
			Border(lipgloss.RoundedBorder(), false, false, false, true).
			BorderForeground(color(t.Muted)).PaddingLeft(1),
		thinkFoot: lipgloss.NewStyle().Foreground(color(t.Muted)).Italic(true),
		errTag:    lipgloss.NewStyle().Foreground(color(t.Error)).Bold(true),
		warnTag:   lipgloss.NewStyle().Foreground(color(t.Warning)).Bold(true),
		diffAdd:   lipgloss.NewStyle().Foreground(color(diffOr(t.Diff.Add, t.Success))),
		diffDel:   lipgloss.NewStyle().Foreground(color(diffOr(t.Diff.Del, t.Error))),
		diffHead:  lipgloss.NewStyle().Foreground(color(t.Muted)).Bold(true),
		lineNum:   lipgloss.NewStyle().Foreground(color(t.Muted)),
	}
}

// diffOr returns the first non-empty of the two colors, so a theme without
// explicit diff roles falls back to its success/error colors.
func diffOr(a, b string) string {
	if a != "" {
		return a
	}
	return b
}

// pickTheme returns the theme for name, falling back to dark.
func pickTheme(themes map[string]Theme, name string) Theme {
	if t, ok := themes[name]; ok {
		return t
	}
	return builtinThemes["dark"]
}
