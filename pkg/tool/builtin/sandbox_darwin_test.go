package builtin

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestBashSandboxed(t *testing.T) {
	if _, err := os.Stat("/usr/bin/sandbox-exec"); err != nil {
		t.Skip("sandbox-exec not available")
	}
	dir := t.TempDir()
	tl := BashSandboxed(dir)

	// Writes inside the working directory work.
	out := mustRun(t, tl, `{"command":"echo confined > inside.txt && cat inside.txt"}`)
	if !strings.Contains(out, "confined") {
		t.Errorf("out = %q", out)
	}

	// Writes outside are denied by the profile. A sibling TempDir lives
	// under /var/folders, which the profile allows for temp files, so the
	// home directory is the escape target.
	outside := filepath.Join(os.Getenv("HOME"), fmt.Sprintf(".kaku-sandbox-test-%d", os.Getpid()))
	defer os.Remove(outside)
	if _, err := run(t, tl, fmt.Sprintf(`{"command":"echo escaped > %s"}`, outside)); err == nil {
		t.Errorf("write outside the sandbox succeeded")
	}
	if _, err := os.Stat(outside); err == nil {
		t.Errorf("file %s exists after sandboxed write", outside)
	}

	// Reads outside the working directory still work.
	out = mustRun(t, tl, `{"command":"head -1 /etc/hosts"}`)
	if out == "" {
		t.Error("read outside the workdir failed")
	}
}
