// Package builtin provides the standard toolset every kaku agent starts with.
package builtin

import (
	"path/filepath"

	"github.com/tamnd/kaku/pkg/tool"
)

// All returns the builtin toolset rooted at workdir.
func All(workdir string) []tool.Tool {
	return []tool.Tool{
		readTool(workdir),
		writeTool(workdir),
		editTool(workdir),
		bashTool(workdir, false),
		grepTool(workdir),
		globTool(workdir),
		lsTool(workdir),
		fetchTool(),
	}
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
