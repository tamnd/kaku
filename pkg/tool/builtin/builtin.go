// Package builtin provides the standard toolset every kaku agent starts with.
package builtin

import (
	"context"
	"path/filepath"

	"github.com/tamnd/kaku/pkg/tool"
)

// Diagnoser reports language-server diagnostics for a file just written, as a
// short text block to append to the tool result, or "" when there is nothing to
// add. A nil Diagnoser attaches no diagnostics.
type Diagnoser interface {
	Report(ctx context.Context, path string) string
}

// All returns the builtin toolset rooted at workdir. A non-nil formatter runs
// after a successful write or edit to canonicalize the touched file, and a
// non-nil diagnoser attaches diagnostics for the touched file to the result.
func All(workdir string, fmtr *Formatter, diag Diagnoser) []tool.Tool {
	return []tool.Tool{
		readTool(workdir),
		writeTool(workdir, fmtr, diag),
		editTool(workdir, fmtr, diag),
		bashTool(workdir, false),
		grepTool(workdir),
		globTool(workdir),
		lsTool(workdir),
		fetchTool(),
	}
}

// withDiagnostics appends a diagnoser's report for path to result. A nil
// diagnoser or an empty report leaves result unchanged.
func withDiagnostics(ctx context.Context, diag Diagnoser, path, result string) string {
	if diag == nil {
		return result
	}
	report := diag.Report(ctx, path)
	if report == "" {
		return result
	}
	return result + "\n\n" + report
}

// resolve maps a path from tool input onto workdir. Absolute paths pass
// through untouched; empty means workdir itself.
func resolve(workdir, p string) string {
	if p == "" {
		return workdir
	}
	if filepath.IsAbs(p) {
		return p
	}
	return filepath.Join(workdir, p)
}

// skipDirName reports whether grep and glob should skip a directory.
func skipDirName(name string) bool {
	switch name {
	case ".git", "node_modules", "vendor", "dist":
		return true
	}
	return false
}
