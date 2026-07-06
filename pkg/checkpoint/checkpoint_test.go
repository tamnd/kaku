package checkpoint

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func initRepo(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	if _, err := git(dir, nil, "init", "-q"); err != nil {
		t.Fatal(err)
	}
	return dir
}

func write(t *testing.T, dir, name, content string) {
	t.Helper()
	path := filepath.Join(dir, name)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func readBack(t *testing.T, dir, name string) string {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(dir, name))
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}

func TestNewOutsideRepo(t *testing.T) {
	if _, err := New(t.TempDir()); err == nil {
		t.Fatal("expected error outside a repository")
	}
}

func TestSaveListRestore(t *testing.T) {
	dir := initRepo(t)
	m, err := New(dir)
	if err != nil {
		t.Fatal(err)
	}

	write(t, dir, "a.txt", "v1")
	first, err := m.Save("first")
	if err != nil {
		t.Fatal(err)
	}

	// Saving again with no changes reuses the checkpoint.
	again, err := m.Save("noop")
	if err != nil {
		t.Fatal(err)
	}
	if again != first {
		t.Errorf("noop save made a new checkpoint: %s vs %s", again, first)
	}

	write(t, dir, "a.txt", "v2")
	write(t, dir, "sub/b.txt", "new file")
	second, err := m.Save("second")
	if err != nil {
		t.Fatal(err)
	}
	if second == first {
		t.Fatal("second save did not advance")
	}

	infos, err := m.List()
	if err != nil {
		t.Fatal(err)
	}
	if len(infos) != 2 || infos[0].SHA != second || infos[0].Label != "second" {
		t.Fatalf("infos = %+v", infos)
	}
	latest, err := m.Latest()
	if err != nil || latest.SHA != second {
		t.Fatalf("latest = %+v, err = %v", latest, err)
	}

	// Rewind to the first checkpoint: a.txt reverts, sub/b.txt vanishes.
	if err := m.Restore(first); err != nil {
		t.Fatal(err)
	}
	if got := readBack(t, dir, "a.txt"); got != "v1" {
		t.Errorf("a.txt = %q", got)
	}
	if _, err := os.Stat(filepath.Join(dir, "sub")); !os.IsNotExist(err) {
		t.Errorf("sub/ still exists: %v", err)
	}

	// The tree matched the second checkpoint when we rewound, so no auto
	// checkpoint was needed and the rewind can be undone via second.
	infos, _ = m.List()
	if len(infos) != 2 {
		t.Fatalf("infos = %+v", infos)
	}
	if err := m.Restore(second); err != nil {
		t.Fatal(err)
	}
	if got := readBack(t, dir, "a.txt"); got != "v2" {
		t.Errorf("after undo a.txt = %q", got)
	}
	if got := readBack(t, dir, "sub/b.txt"); got != "new file" {
		t.Errorf("after undo sub/b.txt = %q", got)
	}

	// A tree the ref head does not cover gets an auto checkpoint first.
	write(t, dir, "a.txt", "v3 not yet saved")
	if err := m.Restore(first); err != nil {
		t.Fatal(err)
	}
	infos, _ = m.List()
	if len(infos) == 2 || !strings.Contains(infos[0].Label, "before rewind") {
		t.Fatalf("infos = %+v", infos)
	}
	if err := m.Restore(infos[0].SHA); err != nil {
		t.Fatal(err)
	}
	if got := readBack(t, dir, "a.txt"); got != "v3 not yet saved" {
		t.Errorf("after undo a.txt = %q", got)
	}
}

func TestRestoreKeepsIgnoredFiles(t *testing.T) {
	dir := initRepo(t)
	m, _ := New(dir)

	write(t, dir, ".gitignore", "scratch/\n")
	write(t, dir, "a.txt", "v1")
	first, err := m.Save("first")
	if err != nil {
		t.Fatal(err)
	}

	write(t, dir, "a.txt", "v2")
	write(t, dir, "scratch/notes.txt", "keep me")
	if _, err := m.Save("second"); err != nil {
		t.Fatal(err)
	}

	if err := m.Restore(first); err != nil {
		t.Fatal(err)
	}
	if got := readBack(t, dir, "scratch/notes.txt"); got != "keep me" {
		t.Errorf("ignored file lost: %q", got)
	}
}

func TestRestoreUnknownSHA(t *testing.T) {
	dir := initRepo(t)
	m, _ := New(dir)
	write(t, dir, "a.txt", "x")
	if _, err := m.Save("s"); err != nil {
		t.Fatal(err)
	}
	if err := m.Restore("deadbeef"); err == nil {
		t.Fatal("expected error for unknown sha")
	}
}

func TestListEmpty(t *testing.T) {
	dir := initRepo(t)
	m, _ := New(dir)
	infos, err := m.List()
	if err != nil || infos != nil {
		t.Fatalf("infos = %v, err = %v", infos, err)
	}
	if _, err := m.Latest(); err == nil {
		t.Fatal("expected error with no checkpoints")
	}
}
