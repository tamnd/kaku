package builtin

import (
	"fmt"
	"path/filepath"
)

// sandboxArgv wraps a bash command in sandbox-exec with a Seatbelt profile:
// reads and network stay open, writes are confined to the working directory
// and the usual temp locations.
func sandboxArgv(workdir, command string) ([]string, error) {
	root := workdir
	if r, err := filepath.EvalSymlinks(workdir); err == nil {
		root = r
	}
	profile := fmt.Sprintf(`(version 1)
(allow default)
(deny file-write*)
(allow file-write*
  (subpath %q)
  (subpath "/tmp")
  (subpath "/private/tmp")
  (subpath "/private/var/folders")
  (subpath "/dev"))
`, root)
	return []string{"/usr/bin/sandbox-exec", "-p", profile, "bash", "-c", command}, nil
}

// SandboxExec is the Linux landlock shim; macOS confines through
// sandbox-exec instead.
func SandboxExec(workdir, command string) error {
	return fmt.Errorf("the sandbox-exec shim is Linux-only")
}
