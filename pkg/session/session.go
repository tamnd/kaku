// Package session persists conversations as JSONL transcripts under
// ~/.kaku/sessions/<project>/<id>.jsonl.
package session

import (
	"bufio"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/tamnd/kaku/pkg/provider"
)

// Store locates the sessions of one project.
type Store struct {
	Root    string // defaults to ~/.kaku/sessions
	Project string // the project's absolute path
}

// NewStore builds a store rooted at ~/.kaku/sessions.
func NewStore(project string) *Store {
	home, _ := os.UserHomeDir()
	return &Store{Root: filepath.Join(home, ".kaku", "sessions"), Project: project}
}

// Meta summarizes one stored session.
type Meta struct {
	ID        string
	Path      string
	CreatedAt time.Time
	Title     string
	Messages  int
	Parent    string // the session this one was forked from, empty for a root
}

type line struct {
	Type    string             `json:"type"` // meta, message, usage, compact
	TS      time.Time          `json:"ts"`
	Project string             `json:"project,omitempty"`
	Title   string             `json:"title,omitempty"`
	Parent  string             `json:"parent,omitempty"`
	Message *provider.Message  `json:"message,omitempty"`
	Usage   *provider.Usage    `json:"usage,omitempty"`
	Replace []provider.Message `json:"replace,omitempty"`
}

func (st *Store) dir() string {
	name := strings.ReplaceAll(st.Project, string(os.PathSeparator), "-")
	name = strings.ReplaceAll(name, "/", "-")
	name = strings.TrimLeft(name, "-")
	if name == "" {
		name = "root"
	}
	return filepath.Join(st.Root, name)
}

// List returns session metadata, newest first. Unreadable files are skipped.
func (st *Store) List() ([]Meta, error) {
	entries, err := os.ReadDir(st.dir())
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var out []Meta
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".jsonl") {
			continue
		}
		s, err := st.Open(strings.TrimSuffix(e.Name(), ".jsonl"))
		if err != nil {
			continue
		}
		out = append(out, s.Meta())
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID > out[j].ID })
	return out, nil
}

// Latest returns the newest session's metadata.
func (st *Store) Latest() (Meta, error) {
	metas, err := st.List()
	if err != nil {
		return Meta{}, err
	}
	if len(metas) == 0 {
		return Meta{}, errors.New("no sessions for this project")
	}
	return metas[0], nil
}

// New creates a session file with a meta line.
func (st *Store) New() (*Session, error) {
	if err := os.MkdirAll(st.dir(), 0o755); err != nil {
		return nil, err
	}
	var suffix [2]byte
	if _, err := rand.Read(suffix[:]); err != nil {
		return nil, err
	}
	now := time.Now().UTC()
	id := now.Format("20060102-150405") + "-" + hex.EncodeToString(suffix[:])
	path := filepath.Join(st.dir(), id+".jsonl")
	f, err := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		return nil, err
	}
	s := &Session{id: id, path: path, f: f, createdAt: now}
	if err := s.write(line{Type: "meta", TS: now, Project: st.Project}); err != nil {
		f.Close()
		return nil, err
	}
	return s, nil
}

// Fork seeds a brand-new session with src's messages and returns it. The new
// session gets a fresh id and its own file; src is left byte-identical. The
// title starts as "fork of <srcID>" so the copy is easy to spot in the list.
func (st *Store) Fork(srcID string) (*Session, error) {
	src, err := st.Open(srcID)
	if err != nil {
		return nil, err
	}
	msgs := src.Messages()
	src.Close()

	ns, err := st.New()
	if err != nil {
		return nil, err
	}
	if err := ns.setParent(srcID); err != nil {
		ns.Close()
		return nil, err
	}
	if len(msgs) > 0 {
		if err := ns.ReplaceMessages(msgs); err != nil {
			ns.Close()
			return nil, err
		}
	}
	if err := ns.SetTitle("fork of " + srcID); err != nil {
		ns.Close()
		return nil, err
	}
	return ns, nil
}

// Rename records a new title on a stored session without opening it for a run.
func (st *Store) Rename(id, title string) error {
	s, err := st.Open(id)
	if err != nil {
		return err
	}
	defer s.Close()
	return s.SetTitle(title)
}

// Delete removes a session file. A missing file is not an error.
func (st *Store) Delete(id string) error {
	err := os.Remove(filepath.Join(st.dir(), id+".jsonl"))
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	return err
}

// Ephemeral returns a session that lives only in memory: it records messages
// and usage for the run but never writes a file, so it leaves no trace on disk.
// It is what --no-session builds.
func (st *Store) Ephemeral() *Session {
	now := time.Now().UTC()
	var suffix [2]byte
	_, _ = rand.Read(suffix[:])
	id := now.Format("20060102-150405") + "-" + hex.EncodeToString(suffix[:])
	return &Session{id: id, createdAt: now}
}

// Open replays an existing session file and reopens it for appending.
func (st *Store) Open(id string) (*Session, error) {
	path := filepath.Join(st.dir(), id+".jsonl")
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	s := &Session{id: id, path: path}
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 0, 64*1024), 16*1024*1024)
	for sc.Scan() {
		raw := sc.Bytes()
		if len(raw) == 0 {
			continue
		}
		var l line
		if err := json.Unmarshal(raw, &l); err != nil {
			continue // tolerate a torn trailing line
		}
		switch l.Type {
		case "meta":
			s.createdAt = l.TS
			if l.Parent != "" {
				s.parent = l.Parent
			}
		case "message":
			if l.Message != nil {
				s.messages = append(s.messages, *l.Message)
			}
		case "compact":
			s.messages = append([]provider.Message(nil), l.Replace...)
		case "usage":
			if l.Usage != nil {
				s.usage.Add(*l.Usage)
			}
		}
		if l.Title != "" {
			s.title = l.Title
		}
	}
	if err := sc.Err(); err != nil {
		f.Close()
		return nil, err
	}
	f.Close()
	af, err := os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		return nil, err
	}
	s.f = af
	return s, nil
}

// Session is one open transcript.
type Session struct {
	id        string
	path      string
	f         *os.File
	createdAt time.Time
	title     string
	parent    string
	messages  []provider.Message
	usage     provider.Usage
}

// ID returns the session id.
func (s *Session) ID() string { return s.id }

// Meta summarizes the session.
func (s *Session) Meta() Meta {
	return Meta{ID: s.id, Path: s.path, CreatedAt: s.createdAt, Title: s.title, Messages: len(s.messages), Parent: s.parent}
}

// Parent returns the id of the session this one was forked from, or "" for a
// root session.
func (s *Session) Parent() string { return s.parent }

// setParent records the fork source in a meta line. It runs once at fork time.
func (s *Session) setParent(parent string) error {
	if err := s.write(line{Type: "meta", TS: time.Now().UTC(), Parent: parent}); err != nil {
		return err
	}
	s.parent = parent
	return nil
}

// Messages returns the current in-memory history.
func (s *Session) Messages() []provider.Message { return s.messages }

// Usage returns the accumulated token usage.
func (s *Session) Usage() provider.Usage { return s.usage }

func (s *Session) write(l line) error {
	if s.f == nil {
		return nil // ephemeral session: keep in memory only
	}
	data, err := json.Marshal(l)
	if err != nil {
		return err
	}
	_, err = s.f.Write(append(data, '\n'))
	return err
}

// Append records one message.
func (s *Session) Append(m provider.Message) error {
	if err := s.write(line{Type: "message", TS: time.Now().UTC(), Message: &m}); err != nil {
		return err
	}
	s.messages = append(s.messages, m)
	return nil
}

// ReplaceMessages records a compaction: the full new history in one line.
func (s *Session) ReplaceMessages(ms []provider.Message) error {
	if err := s.write(line{Type: "compact", TS: time.Now().UTC(), Replace: ms}); err != nil {
		return err
	}
	s.messages = append([]provider.Message(nil), ms...)
	return nil
}

// AddUsage records one completion's token usage.
func (s *Session) AddUsage(u provider.Usage) error {
	if err := s.write(line{Type: "usage", TS: time.Now().UTC(), Usage: &u}); err != nil {
		return err
	}
	s.usage.Add(u)
	return nil
}

// SetTitle records the session title. Kept to one line and 80 chars.
func (s *Session) SetTitle(t string) error {
	t = strings.SplitN(strings.TrimSpace(t), "\n", 2)[0]
	if len(t) > 80 {
		t = t[:80]
	}
	if err := s.write(line{Type: "meta", TS: time.Now().UTC(), Title: t}); err != nil {
		return err
	}
	s.title = t
	return nil
}

// Close releases the file handle.
func (s *Session) Close() error {
	if s.f == nil {
		return nil
	}
	return s.f.Close()
}

// String renders a short human label.
func (m Meta) String() string {
	title := m.Title
	if title == "" {
		title = "(untitled)"
	}
	return fmt.Sprintf("%s  %3d msgs  %s", m.ID, m.Messages, title)
}

// Node is one session in the fork tree, with the sessions forked from it.
type Node struct {
	Meta
	Children []*Node
}

// Tree returns the project's sessions as a forest of fork lineages. A session
// is a child of the session named by its Parent; a session whose parent is
// missing or empty is a root. Roots and children are each ordered newest first.
func (st *Store) Tree() ([]*Node, error) {
	metas, err := st.List()
	if err != nil {
		return nil, err
	}
	byID := make(map[string]*Node, len(metas))
	for _, m := range metas {
		byID[m.ID] = &Node{Meta: m}
	}
	var roots []*Node
	for _, m := range metas {
		n := byID[m.ID]
		if parent, ok := byID[m.Parent]; ok && m.Parent != "" {
			parent.Children = append(parent.Children, n)
		} else {
			roots = append(roots, n)
		}
	}
	return roots, nil
}
