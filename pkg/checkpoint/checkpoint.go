// Package checkpoint snapshots the working tree into a hidden git ref so a
// turn that went wrong can be rewound. Snapshots use a temporary index and
// live under refs/kaku/checkpoint, so the user's branches, index, and HEAD
// are never touched.
package checkpoint

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

const ref = "refs/kaku/checkpoint"

// Manager saves and restores snapshots for one repository.
type Manager struct {
	root string // repository top level
}

// Info describes one saved checkpoint.
type Info struct {
	SHA   string
	Time  time.Time
	Label string
}

func (i Info) String() string {
	return fmt.Sprintf("%s  %s  %s", i.SHA[:10], i.Time.Format("2006-01-02 15:04:05"), i.Label)
}

// New returns a manager for the repository containing dir, or an error if
// dir is not inside a git work tree.
func New(dir string) (*Manager, error) {
	root, err := git(dir, nil, "rev-parse", "--show-toplevel")
	if err != nil {
		return nil, fmt.Errorf("not a git repository: %s", dir)
	}
	return &Manager{root: root}, nil
}

// Save snapshots the working tree and returns the checkpoint sha. When
// nothing changed since the last checkpoint, the previous sha is returned
// and no new commit is written.
func (m *Manager) Save(label string) (string, error) {
	tree, err := m.writeTree()
	if err != nil {
		return "", err
	}

	parent, _ := git(m.root, nil, "rev-parse", "-q", "--verify", ref)
	if parent != "" {
		parentTree, err := git(m.root, nil, "rev-parse", parent+"^{tree}")
		if err == nil && parentTree == tree {
			return parent, nil
		}
	}

	if label == "" {
		label = "checkpoint"
	}
	args := []string{
		"-c", "user.name=kaku", "-c", "user.email=kaku@localhost",
		"commit-tree", tree, "-m", label,
	}
	if parent != "" {
		args = append(args, "-p", parent)
	}
	sha, err := git(m.root, nil, args...)
	if err != nil {
		return "", err
	}
	if _, err := git(m.root, nil, "update-ref", ref, sha); err != nil {
		return "", err
	}
	return sha, nil
}

// List returns all checkpoints, newest first.
func (m *Manager) List() ([]Info, error) {
	if _, err := git(m.root, nil, "rev-parse", "-q", "--verify", ref); err != nil {
		return nil, nil
	}
	out, err := git(m.root, nil, "log", "--format=%H%x1f%ct%x1f%s", ref)
	if err != nil {
		return nil, err
	}
	var infos []Info
	for line := range strings.SplitSeq(out, "\n") {
		parts := strings.SplitN(line, "\x1f", 3)
		if len(parts) != 3 {
			continue
		}
		sec, err := strconv.ParseInt(parts[1], 10, 64)
		if err != nil {
			continue
		}
		infos = append(infos, Info{SHA: parts[0], Time: time.Unix(sec, 0), Label: parts[2]})
	}
	return infos, nil
}

// Latest returns the newest checkpoint.
func (m *Manager) Latest() (Info, error) {
	infos, err := m.List()
	if err != nil {
		return Info{}, err
	}
	if len(infos) == 0 {
		return Info{}, fmt.Errorf("no checkpoints yet")
	}
	return infos[0], nil
}

// Restore puts the working tree back to the given checkpoint. The current
// state is checkpointed first, so a rewind is itself undoable. Files that
// appeared after the checkpoint are removed; ignored files are untouched.
func (m *Manager) Restore(sha string) error {
	target, err := git(m.root, nil, "rev-parse", "-q", "--verify", sha+"^{commit}")
	if err != nil {
		return fmt.Errorf("no such checkpoint: %s", sha)
	}

	current, err := m.Save("auto: before rewind")
	if err != nil {
		return err
	}
	curTree, _ := git(m.root, nil, "rev-parse", current+"^{tree}")
	targetTree, _ := git(m.root, nil, "rev-parse", target+"^{tree}")
	if curTree == targetTree {
		return nil
	}

	// Files present now but absent from the target get deleted.
	gone, err := git(m.root, nil, "diff-tree", "-r", "--name-only", "--diff-filter=D", current, target)
	if err != nil {
		return err
	}

	// Materialize the target tree through a temporary index.
	idx, cleanup, err := tempIndex()
	if err != nil {
		return err
	}
	defer cleanup()
	env := []string{"GIT_INDEX_FILE=" + idx}
	if _, err := git(m.root, env, "read-tree", target); err != nil {
		return err
	}
	if _, err := git(m.root, env, "checkout-index", "-a", "-f"); err != nil {
		return err
	}

	for name := range strings.SplitSeq(gone, "\n") {
		if name == "" {
			continue
		}
		path := filepath.Join(m.root, name)
		os.Remove(path)
		// Drop directories the deletion emptied out.
		for d := filepath.Dir(path); d != m.root; d = filepath.Dir(d) {
			if os.Remove(d) != nil {
				break
			}
		}
	}
	return nil
}

// writeTree stages the whole working tree in a temporary index and returns
// the resulting tree sha.
func (m *Manager) writeTree() (string, error) {
	idx, cleanup, err := tempIndex()
	if err != nil {
		return "", err
	}
	defer cleanup()
	env := []string{"GIT_INDEX_FILE=" + idx}
	if _, err := git(m.root, env, "add", "-A", "."); err != nil {
		return "", err
	}
	return git(m.root, env, "write-tree")
}

func tempIndex() (string, func(), error) {
	f, err := os.CreateTemp("", "kaku-index-*")
	if err != nil {
		return "", nil, err
	}
	path := f.Name()
	f.Close()
	os.Remove(path) // git wants to create it itself
	return path, func() { os.Remove(path) }, nil
}

func git(dir string, env []string, args ...string) (string, error) {
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	if env != nil {
		cmd.Env = append(os.Environ(), env...)
	}
	out, err := cmd.CombinedOutput()
	if err != nil {
		return "", fmt.Errorf("git %s: %v: %s", args[0], err, strings.TrimSpace(string(out)))
	}
	return strings.TrimSpace(string(out)), nil
}
