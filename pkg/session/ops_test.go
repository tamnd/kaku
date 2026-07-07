package session

import (
	"os"
	"strings"
	"testing"

	"github.com/tamnd/kaku/pkg/provider"
)

// seed creates a session with two messages and returns its id.
func seed(t *testing.T, st *Store) string {
	t.Helper()
	s, err := st.New()
	if err != nil {
		t.Fatal(err)
	}
	if err := s.Append(provider.Text(provider.RoleUser, "hello")); err != nil {
		t.Fatal(err)
	}
	if err := s.Append(provider.Text(provider.RoleAssistant, "hi there")); err != nil {
		t.Fatal(err)
	}
	if err := s.SetTitle("original"); err != nil {
		t.Fatal(err)
	}
	s.Close()
	return s.ID()
}

func TestFork(t *testing.T) {
	st := testStore(t)
	src := seed(t, st)

	before, err := os.ReadFile(st.dir() + "/" + src + ".jsonl")
	if err != nil {
		t.Fatal(err)
	}

	f, err := st.Fork(src)
	if err != nil {
		t.Fatal(err)
	}
	if f.ID() == src {
		t.Fatal("fork reused the source id")
	}
	if len(f.Messages()) != 2 {
		t.Fatalf("fork messages = %d, want 2", len(f.Messages()))
	}
	if got := f.Meta().Title; got != "fork of "+src {
		t.Errorf("fork title = %q", got)
	}
	f.Close()

	after, err := os.ReadFile(st.dir() + "/" + src + ".jsonl")
	if err != nil {
		t.Fatal(err)
	}
	if string(before) != string(after) {
		t.Error("source file changed after fork")
	}
}

func TestForkRecordsParent(t *testing.T) {
	st := testStore(t)
	src := seed(t, st)
	f, err := st.Fork(src)
	if err != nil {
		t.Fatal(err)
	}
	if f.Parent() != src {
		t.Errorf("fork parent = %q, want %q", f.Parent(), src)
	}
	f.Close()

	// The parent survives a reopen.
	r, err := st.Open(f.ID())
	if err != nil {
		t.Fatal(err)
	}
	defer r.Close()
	if r.Meta().Parent != src {
		t.Errorf("reopened parent = %q, want %q", r.Meta().Parent, src)
	}
}

func TestTreeLineage(t *testing.T) {
	st := testStore(t)
	root := seed(t, st)
	child, err := st.Fork(root)
	if err != nil {
		t.Fatal(err)
	}
	childID := child.ID()
	child.Close()
	grand, err := st.Fork(childID)
	if err != nil {
		t.Fatal(err)
	}
	grandID := grand.ID()
	grand.Close()

	roots, err := st.Tree()
	if err != nil {
		t.Fatal(err)
	}
	if len(roots) != 1 {
		t.Fatalf("roots = %d, want 1 (only %s has no parent)", len(roots), root)
	}
	if roots[0].ID != root {
		t.Fatalf("root id = %q, want %q", roots[0].ID, root)
	}
	if len(roots[0].Children) != 1 || roots[0].Children[0].ID != childID {
		t.Fatalf("root child = %+v, want %s", roots[0].Children, childID)
	}
	gc := roots[0].Children[0].Children
	if len(gc) != 1 || gc[0].ID != grandID {
		t.Fatalf("grandchild = %+v, want %s", gc, grandID)
	}
}

func TestRename(t *testing.T) {
	st := testStore(t)
	id := seed(t, st)
	if err := st.Rename(id, "release prep"); err != nil {
		t.Fatal(err)
	}
	r, err := st.Open(id)
	if err != nil {
		t.Fatal(err)
	}
	defer r.Close()
	if got := r.Meta().Title; got != "release prep" {
		t.Errorf("title = %q, want release prep", got)
	}
}

func TestDelete(t *testing.T) {
	st := testStore(t)
	id := seed(t, st)
	if err := st.Delete(id); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(st.dir() + "/" + id + ".jsonl"); !os.IsNotExist(err) {
		t.Errorf("file still present: %v", err)
	}
	// Deleting a missing session is not an error.
	if err := st.Delete(id); err != nil {
		t.Errorf("second delete = %v", err)
	}
}

func TestExportMarkdown(t *testing.T) {
	st := testStore(t)
	id := seed(t, st)
	out := t.TempDir() + "/x.md"
	if err := st.Export(id, "md", out); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(out)
	if err != nil {
		t.Fatal(err)
	}
	md := string(data)
	for _, want := range []string{"# original", "## user", "hello", "## kaku", "hi there"} {
		if !strings.Contains(md, want) {
			t.Errorf("markdown missing %q\n%s", want, md)
		}
	}
}

func TestExportJSONRoundTrip(t *testing.T) {
	st := testStore(t)
	id := seed(t, st)
	out := t.TempDir() + "/x.json"
	if err := st.Export(id, "json", out); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(out)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), "hello") || !strings.Contains(string(data), "hi there") {
		t.Errorf("json missing messages\n%s", data)
	}
}

func TestShareWritesSelfContainedHTML(t *testing.T) {
	st := testStore(t)
	id := seed(t, st)
	out := t.TempDir() + "/share.html"
	path, err := st.Share(id, out)
	if err != nil {
		t.Fatal(err)
	}
	if path != out {
		t.Errorf("share path = %q, want %q", path, out)
	}
	data, err := os.ReadFile(out)
	if err != nil {
		t.Fatal(err)
	}
	page := string(data)
	if !strings.Contains(page, "<!doctype html>") {
		t.Error("share output is not an HTML page")
	}
	if strings.Contains(page, "http://") || strings.Contains(page, "https://") {
		t.Error("share page pulls in an external asset")
	}
	for _, want := range []string{"original", "hello", "hi there"} {
		if !strings.Contains(page, want) {
			t.Errorf("share page missing %q", want)
		}
	}
}

func TestExportUnknownFormat(t *testing.T) {
	st := testStore(t)
	id := seed(t, st)
	if err := st.Export(id, "pdf", t.TempDir()+"/x.pdf"); err == nil {
		t.Error("expected an error for an unknown format")
	}
}
