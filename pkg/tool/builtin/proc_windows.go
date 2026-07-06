//go:build windows

package builtin

import (
	"os/exec"
	"time"
)

// setupProcessGroup is a no-op on Windows beyond the kill grace period;
// CommandContext kills the direct child.
func setupProcessGroup(cmd *exec.Cmd) {
	cmd.WaitDelay = 5 * time.Second
}
