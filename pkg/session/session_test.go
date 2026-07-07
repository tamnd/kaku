package session

import (
	"testing"

	"github.com/tamnd/kaku/pkg/provider"
)

func testStore(t *testing.T) *Store {
	t.Helper()
	return &Store{Root: t.TempDir(), Project: "/home/x/proj"}
}

func TestRoundTrip(t *testing.T) {
	st := testStore(t)
	s, err := st.New()
	if err != nil {
		t.Fatal(err)
	}
	if err := s.Append(provider.Text(provider.RoleUser, "hello")); err != nil {
		t.Fatal(err)
	}
	if err := s.Append(provider.Text(provider.RoleAssistant, "hi")); err != nil {
		t.Fatal(err)
	}
	if err := s.AddUsage(provider.Usage{InputTokens: 10, OutputTokens: 3}); err != nil {
		t.Fatal(err)
	}
	if err := s.SetTitle("hello\nsecond line ignored"); err != nil {
		t.Fatal(err)
	}
	s.Close()

	r, err := st.Open(s.ID())
	if err != nil {
		t.Fatal(err)
	}
	defer r.Close()
	if len(r.Messages()) != 2 {
		t.Fatalf("messages = %d", len(r.Messages()))
	}
	if r.Messages()[1].TextContent() != "hi" {
		t.Fatalf("second message = %q", r.Messages()[1].TextContent())
	}
	if u := r.Usage(); u.InputTokens != 10 || u.OutputTokens != 3 {
		t.Fatalf("usage = %+v", u)
	}
	if r.Meta().Title != "hello" {
		t.Fatalf("title = %q", r.Meta().Title)
	}
}

func TestCompactLineReplay(t *testing.T) {
	st := testStore(t)
	s, _ := st.New()
	s.Append(provider.Text(provider.RoleUser, "one"))
	s.Append(provider.Text(provider.RoleAssistant, "two"))
	if err := s.ReplaceMessages([]provider.Message{provider.Text(provider.RoleUser, "summary")}); err != nil {
		t.Fatal(err)
	}
	s.Append(provider.Text(provider.RoleAssistant, "three"))
	s.Close()

	r, err := st.Open(s.ID())
	if err != nil {
		t.Fatal(err)
	}
	defer r.Close()
	msgs := r.Messages()
	if len(msgs) != 2 || msgs[0].TextContent() != "summary" || msgs[1].TextContent() != "three" {
		t.Fatalf("replay wrong: %+v", msgs)
	}
}

func TestListNewestFirstAndLatest(t *testing.T) {
	st := testStore(t)
	a, _ := st.New()
	a.Append(provider.Text(provider.RoleUser, "a"))
	a.Close()
	b, _ := st.New()
	b.Append(provider.Text(provider.RoleUser, "b"))
	b.Close()

	metas, err := st.List()
	if err != nil {
		t.Fatal(err)
	}
	if len(metas) != 2 {
		t.Fatalf("list = %d", len(metas))
	}
	if metas[0].ID < metas[1].ID {
		t.Fatal("not newest first")
	}
	latest, err := st.Latest()
	if err != nil {
		t.Fatal(err)
	}
	if latest.ID != metas[0].ID {
		t.Fatal("latest mismatch")
	}
}

func TestEmptyStore(t *testing.T) {
	st := testStore(t)
	metas, err := st.List()
	if err != nil || metas != nil {
		t.Fatalf("expected empty list, got %v, %v", metas, err)
	}
	if _, err := st.Latest(); err == nil {
		t.Fatal("latest on empty store should error")
	}
}

func TestEphemeralWritesNothing(t *testing.T) {
	st := testStore(t)
	s := st.Ephemeral()
	if err := s.Append(provider.Text(provider.RoleUser, "hello")); err != nil {
		t.Fatal(err)
	}
	if err := s.AddUsage(provider.Usage{InputTokens: 5}); err != nil {
		t.Fatal(err)
	}
	if err := s.SetTitle("secret"); err != nil {
		t.Fatal(err)
	}
	// The in-memory view still works.
	if len(s.Messages()) != 1 || s.Usage().InputTokens != 5 {
		t.Fatalf("in-memory state lost: %d msgs, usage %+v", len(s.Messages()), s.Usage())
	}
	s.Close()
	// Nothing was persisted, so the store lists no sessions.
	metas, err := st.List()
	if err != nil {
		t.Fatal(err)
	}
	if len(metas) != 0 {
		t.Fatalf("ephemeral session left %d files on disk", len(metas))
	}
}
