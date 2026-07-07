package builtin

import (
	"context"
	"os/exec"
	"path/filepath"
	"time"
)

// Formatter rewrites a file in place after a write or edit, matched by
// extension, so the model reads canonical files on its next read. It is
// best-effort: a formatter whose binary is not on PATH is a silent skip, and a
// formatter that fails leaves the file as written.
type Formatter struct {
	workdir string
	byExt   map[string][]string // extension -> argv with a $FILE placeholder
}

// FormatSpec overrides or adds one formatter. Disabled turns a builtin off.
// Command and Extensions register a custom formatter under its name.
type FormatSpec struct {
	Disabled   bool
	Command    []string
	Extensions []string
}

type builtinFmt struct {
	name string
	exts []string
	cmd  []string
}

// builtinFormatters are the defaults, keyed by name so config can disable one
// or override its command.
var builtinFormatters = []builtinFmt{
	{"gofmt", []string{".go"}, []string{"gofmt", "-w", "$FILE"}},
	{"rustfmt", []string{".rs"}, []string{"rustfmt", "$FILE"}},
	{"prettier", []string{".ts", ".tsx", ".js", ".jsx", ".mjs", ".css", ".html", ".json", ".md", ".yaml", ".yml"}, []string{"prettier", "--write", "$FILE"}},
	{"ruff", []string{".py"}, []string{"ruff", "format", "$FILE"}},
}

// NewFormatter builds a formatter rooted at workdir. When enabled is false it
// returns nil, which callers treat as "do not format". specs override builtins
// by name (disable or swap the command) and register custom formatters.
func NewFormatter(workdir string, enabled bool, specs map[string]FormatSpec) *Formatter {
	if !enabled {
		return nil
	}
	f := &Formatter{workdir: workdir, byExt: map[string][]string{}}
	for _, b := range builtinFormatters {
		spec, ok := specs[b.name]
		if ok && spec.Disabled {
			continue
		}
		cmd := b.cmd
		if ok && len(spec.Command) > 0 {
			cmd = spec.Command
		}
		exts := b.exts
		if ok && len(spec.Extensions) > 0 {
			exts = spec.Extensions
		}
		for _, e := range exts {
			f.byExt[e] = cmd
		}
	}
	// Custom formatters: any spec name that is not a builtin.
	for name, spec := range specs {
		if isBuiltinFormatter(name) || spec.Disabled || len(spec.Command) == 0 {
			continue
		}
		for _, e := range spec.Extensions {
			f.byExt[e] = spec.Command
		}
	}
	if len(f.byExt) == 0 {
		return nil
	}
	return f
}

func isBuiltinFormatter(name string) bool {
	for _, b := range builtinFormatters {
		if b.name == name {
			return true
		}
	}
	return false
}

// Format runs the matching formatter on path if one is configured and its
// binary is on PATH. It is a no-op on a nil formatter, an unmatched extension,
// or a missing binary, and swallows a formatter's own failure.
func (f *Formatter) Format(path string) {
	if f == nil {
		return
	}
	argv, ok := f.byExt[filepath.Ext(path)]
	if !ok || len(argv) == 0 {
		return
	}
	if _, err := exec.LookPath(argv[0]); err != nil {
		return // formatter not installed: silent skip
	}
	args := make([]string, len(argv)-1)
	for i, a := range argv[1:] {
		if a == "$FILE" {
			a = path
		}
		args[i] = a
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	c := exec.CommandContext(ctx, argv[0], args...)
	c.Dir = f.workdir
	_ = c.Run()
}
