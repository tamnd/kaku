package builtin

import (
	"fmt"
	"os"
	"os/exec"
	"syscall"

	"github.com/landlock-lsm/go-landlock/landlock"
)

// sandboxArgv re-execs kaku as a hidden shim: landlock has to be applied
// inside the child process, before bash starts.
func sandboxArgv(workdir, command string) ([]string, error) {
	self, err := os.Executable()
	if err != nil {
		return nil, err
	}
	return []string{self, "sandbox-exec", workdir, command}, nil
}

// SandboxExec applies a landlock policy that confines writes to the working
// directory and temp locations, then replaces the process with bash. It
// backs the hidden kaku sandbox-exec command. On kernels without landlock
// the restriction degrades to nothing (best effort).
func SandboxExec(workdir, command string) error {
	rw := []string{workdir, "/tmp"}
	for _, p := range []string{"/var/tmp", "/dev/shm"} {
		if _, err := os.Stat(p); err == nil {
			rw = append(rw, p)
		}
	}
	err := landlock.V5.BestEffort().RestrictPaths(
		landlock.RODirs("/"),
		landlock.RWDirs(rw...),
		landlock.RWFiles("/dev/null"),
	)
	if err != nil {
		return fmt.Errorf("landlock: %w", err)
	}
	bash, err := exec.LookPath("bash")
	if err != nil {
		return err
	}
	return syscall.Exec(bash, []string{"bash", "-c", command}, os.Environ())
}
