//go:build !darwin && !linux

package builtin

import "fmt"

func sandboxArgv(workdir, command string) ([]string, error) {
	return nil, fmt.Errorf("the bash sandbox is not supported on this platform")
}

// SandboxExec backs the hidden kaku sandbox-exec command on Linux.
func SandboxExec(workdir, command string) error {
	return fmt.Errorf("the bash sandbox is not supported on this platform")
}
