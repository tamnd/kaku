//go:build unix

package builtin

import (
	"os/exec"
	"syscall"
	"time"
)

// setupProcessGroup runs the command in its own process group so a timeout
// kills the whole tree, not just the shell.
func setupProcessGroup(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	cmd.Cancel = func() error {
		return syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
	}
	cmd.WaitDelay = 5 * time.Second
}
