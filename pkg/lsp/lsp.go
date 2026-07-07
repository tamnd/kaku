// Package lsp runs language servers on the side so the agent sees diagnostics
// for a file it just wrote without shelling out to a build. A Manager starts a
// server on first touch of a matching file, keeps it warm, opens the file, and
// collects the diagnostics the server publishes. It is best-effort throughout:
// a language server that is not installed, or one that never answers, is a
// silent skip that leaves the tool result unchanged.
package lsp

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
)

// Diagnostic is one problem a language server reported for a file.
type Diagnostic struct {
	Line     int // 1-based
	Col      int // 1-based
	Severity string
	Message  string
}

// serverSpec describes one language server: how to launch it, which extensions
// it owns, and the LSP languageId to tag opened files with.
type serverSpec struct {
	name    string
	command []string
	exts    []string
	langID  string
}

// Spec overrides a builtin server or registers a custom one. Disabled turns a
// builtin off; Command and Extensions register a server under its config name.
type Spec struct {
	Disabled   bool
	Command    []string
	Extensions []string
	LangID     string
}

// builtinServers are the defaults, keyed by name so config can disable one or
// swap its command.
var builtinServers = []serverSpec{
	{"gopls", []string{"gopls"}, []string{".go"}, "go"},
	{"pyright", []string{"pyright-langserver", "--stdio"}, []string{".py"}, "python"},
	{"rust-analyzer", []string{"rust-analyzer"}, []string{".rs"}, "rust"},
	{"typescript", []string{"typescript-language-server", "--stdio"}, []string{".ts", ".tsx", ".js", ".jsx", ".mjs", ".cjs"}, "typescript"},
	{"clangd", []string{"clangd"}, []string{".c", ".cc", ".cpp", ".h", ".hpp"}, "c"},
}

func isBuiltinServer(name string) bool {
	for _, b := range builtinServers {
		if b.name == name {
			return true
		}
	}
	return false
}

// Manager owns the running servers for one workspace.
type Manager struct {
	workdir string
	byExt   map[string]serverSpec // extension -> server that owns it

	mu      sync.Mutex
	running map[string]*server // spec name -> live server
	wait    time.Duration      // how long to wait for diagnostics after opening
}

// New builds a manager rooted at workdir. When enabled is false it returns nil,
// which callers treat as "no diagnostics". specs override builtins by name
// (disable or swap the command) and register custom servers.
func New(workdir string, enabled bool, specs map[string]Spec) *Manager {
	if !enabled {
		return nil
	}
	m := &Manager{workdir: workdir, byExt: map[string]serverSpec{}, running: map[string]*server{}, wait: 3 * time.Second}
	for _, b := range builtinServers {
		spec, ok := specs[b.name]
		if ok && spec.Disabled {
			continue
		}
		s := b
		if ok && len(spec.Command) > 0 {
			s.command = spec.Command
		}
		if ok && len(spec.Extensions) > 0 {
			s.exts = spec.Extensions
		}
		if ok && spec.LangID != "" {
			s.langID = spec.LangID
		}
		for _, e := range s.exts {
			m.byExt[e] = s
		}
	}
	for name, spec := range specs {
		if isBuiltinServer(name) || spec.Disabled || len(spec.Command) == 0 {
			continue
		}
		s := serverSpec{name: name, command: spec.Command, exts: spec.Extensions, langID: spec.LangID}
		if s.langID == "" {
			s.langID = name
		}
		for _, e := range spec.Extensions {
			m.byExt[e] = s
		}
	}
	if len(m.byExt) == 0 {
		return nil
	}
	return m
}

// Diagnostics opens path in its language server and returns the diagnostics the
// server publishes for it within the wait window. It returns nil when no server
// owns the extension, the server binary is missing, or nothing was reported.
func (m *Manager) Diagnostics(ctx context.Context, path string) []Diagnostic {
	if m == nil {
		return nil
	}
	abs, err := filepath.Abs(path)
	if err != nil {
		return nil
	}
	spec, ok := m.byExt[filepath.Ext(abs)]
	if !ok {
		return nil
	}
	srv := m.ensure(ctx, spec)
	if srv == nil {
		return nil
	}
	return srv.diagnose(ctx, abs, spec.langID, m.wait)
}

// Report renders the diagnostics for path as a short text block to append to a
// tool result, or "" when there is nothing worth adding.
func (m *Manager) Report(ctx context.Context, path string) string {
	ds := m.Diagnostics(ctx, path)
	if len(ds) == 0 {
		return ""
	}
	sort.Slice(ds, func(i, j int) bool {
		if ds[i].Line != ds[j].Line {
			return ds[i].Line < ds[j].Line
		}
		return ds[i].Col < ds[j].Col
	})
	rel := path
	if r, err := filepath.Rel(m.workdir, path); err == nil && !strings.HasPrefix(r, "..") {
		rel = r
	}
	var b strings.Builder
	fmt.Fprintf(&b, "diagnostics for %s:\n", rel)
	for _, d := range ds {
		fmt.Fprintf(&b, "  %d:%d %s: %s\n", d.Line, d.Col, d.Severity, d.Message)
	}
	return strings.TrimRight(b.String(), "\n")
}

// Close shuts every running server down. It is safe on a nil manager.
func (m *Manager) Close() {
	if m == nil {
		return
	}
	m.mu.Lock()
	servers := make([]*server, 0, len(m.running))
	for _, s := range m.running {
		servers = append(servers, s)
	}
	m.running = map[string]*server{}
	m.mu.Unlock()
	for _, s := range servers {
		s.close()
	}
}

// ensure returns a live server for spec, starting and initializing it the first
// time. A start or handshake failure is cached as a nil so we do not retry a
// missing binary on every write.
func (m *Manager) ensure(ctx context.Context, spec serverSpec) *server {
	m.mu.Lock()
	if s, ok := m.running[spec.name]; ok {
		m.mu.Unlock()
		return s
	}
	m.mu.Unlock()

	if _, err := exec.LookPath(spec.command[0]); err != nil {
		m.mu.Lock()
		m.running[spec.name] = nil
		m.mu.Unlock()
		return nil
	}
	s, err := startServer(m.workdir, spec.command)
	if err != nil {
		s = nil
	} else if err := s.initialize(ctx, m.workdir); err != nil {
		s.close()
		s = nil
	}
	m.mu.Lock()
	m.running[spec.name] = s
	m.mu.Unlock()
	return s
}

// server is one running language server process speaking LSP over stdio.
type server struct {
	cmd    *exec.Cmd
	stdin  io.WriteCloser
	writeM sync.Mutex

	mu      sync.Mutex
	nextID  int
	pending map[string]chan json.RawMessage
	diags   map[string][]Diagnostic // uri -> latest
	seen    map[string]bool         // uri -> a publish has arrived
	opened  map[string]bool         // uri -> didOpen sent
	closed  bool
}

// startServer spawns the language server and starts its reader loop.
func startServer(workdir string, command []string) (*server, error) {
	cmd := exec.Command(command[0], command[1:]...)
	cmd.Dir = workdir
	stdin, err := cmd.StdinPipe()
	if err != nil {
		return nil, err
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return nil, err
	}
	cmd.Stderr = nil
	if err := cmd.Start(); err != nil {
		return nil, err
	}
	s := &server{
		cmd:     cmd,
		stdin:   stdin,
		pending: map[string]chan json.RawMessage{},
		diags:   map[string][]Diagnostic{},
		seen:    map[string]bool{},
		opened:  map[string]bool{},
	}
	go s.readLoop(stdout)
	return s, nil
}

type rpcMessage struct {
	ID     json.RawMessage `json:"id,omitempty"`
	Method string          `json:"method,omitempty"`
	Result json.RawMessage `json:"result,omitempty"`
	Params json.RawMessage `json:"params,omitempty"`
}

func (s *server) readLoop(stdout io.Reader) {
	r := bufio.NewReader(stdout)
	for {
		length, err := readContentLength(r)
		if err != nil {
			return
		}
		body := make([]byte, length)
		if _, err := io.ReadFull(r, body); err != nil {
			return
		}
		s.handle(body)
	}
}

func readContentLength(r *bufio.Reader) (int, error) {
	length := -1
	for {
		line, err := r.ReadString('\n')
		if err != nil {
			return 0, err
		}
		line = strings.TrimRight(line, "\r\n")
		if line == "" {
			if length < 0 {
				return 0, fmt.Errorf("lsp: header with no content-length")
			}
			return length, nil
		}
		if strings.HasPrefix(strings.ToLower(line), "content-length:") {
			v := strings.TrimSpace(line[len("content-length:"):])
			n, err := strconv.Atoi(v)
			if err != nil {
				return 0, err
			}
			length = n
		}
	}
}

func (s *server) handle(body []byte) {
	var msg rpcMessage
	if err := json.Unmarshal(body, &msg); err != nil {
		return
	}
	hasID := len(msg.ID) > 0 && string(msg.ID) != "null"
	switch {
	case msg.Method == "textDocument/publishDiagnostics":
		s.recordDiagnostics(msg.Params)
	case msg.Method != "" && hasID:
		// A server-to-client request. Answer with an empty result so servers
		// that block on registration (gopls) keep going.
		s.reply(msg.ID)
	case msg.Method == "" && hasID:
		s.mu.Lock()
		ch := s.pending[string(msg.ID)]
		delete(s.pending, string(msg.ID))
		s.mu.Unlock()
		if ch != nil {
			ch <- msg.Result
		}
	}
}

func (s *server) recordDiagnostics(params json.RawMessage) {
	var p struct {
		URI         string `json:"uri"`
		Diagnostics []struct {
			Range struct {
				Start struct {
					Line int `json:"line"`
					Char int `json:"character"`
				} `json:"start"`
			} `json:"range"`
			Severity int    `json:"severity"`
			Message  string `json:"message"`
		} `json:"diagnostics"`
	}
	if err := json.Unmarshal(params, &p); err != nil {
		return
	}
	ds := make([]Diagnostic, 0, len(p.Diagnostics))
	for _, d := range p.Diagnostics {
		ds = append(ds, Diagnostic{
			Line:     d.Range.Start.Line + 1,
			Col:      d.Range.Start.Char + 1,
			Severity: severityName(d.Severity),
			Message:  d.Message,
		})
	}
	s.mu.Lock()
	s.diags[p.URI] = ds
	s.seen[p.URI] = true
	s.mu.Unlock()
}

func severityName(n int) string {
	switch n {
	case 1:
		return "error"
	case 2:
		return "warning"
	case 3:
		return "info"
	case 4:
		return "hint"
	default:
		return "info"
	}
}

// initialize runs the LSP handshake: an initialize request, then the
// initialized notification. It returns once the server has answered initialize.
func (s *server) initialize(ctx context.Context, workdir string) error {
	root := fileURI(workdir)
	params := map[string]any{
		"processId": os.Getpid(),
		"rootUri":   root,
		"capabilities": map[string]any{
			"textDocument": map[string]any{
				"publishDiagnostics": map[string]any{},
			},
		},
		"workspaceFolders": []map[string]any{{"uri": root, "name": filepath.Base(workdir)}},
	}
	if _, err := s.request(ctx, "initialize", params, 10*time.Second); err != nil {
		return err
	}
	return s.notify("initialized", map[string]any{})
}

// diagnose opens (or re-opens) path and waits for the server to publish
// diagnostics for it, up to wait. A file that opens clean returns nil.
func (s *server) diagnose(ctx context.Context, path, langID string, wait time.Duration) []Diagnostic {
	uri := fileURI(path)
	text, err := os.ReadFile(path)
	if err != nil {
		return nil
	}

	s.mu.Lock()
	s.seen[uri] = false
	s.diags[uri] = nil
	first := !s.opened[uri]
	s.opened[uri] = true
	version := len(s.diags) // any changing number is fine per re-open
	s.mu.Unlock()

	if first {
		_ = s.notify("textDocument/didOpen", map[string]any{
			"textDocument": map[string]any{
				"uri": uri, "languageId": langID, "version": 1, "text": string(text),
			},
		})
	} else {
		_ = s.notify("textDocument/didChange", map[string]any{
			"textDocument":   map[string]any{"uri": uri, "version": version + 1},
			"contentChanges": []map[string]any{{"text": string(text)}},
		})
	}

	deadline := time.Now().Add(wait)
	for time.Now().Before(deadline) {
		if err := ctx.Err(); err != nil {
			break
		}
		s.mu.Lock()
		got := s.seen[uri]
		ds := s.diags[uri]
		s.mu.Unlock()
		if got {
			return ds
		}
		time.Sleep(50 * time.Millisecond)
	}
	s.mu.Lock()
	ds := s.diags[uri]
	s.mu.Unlock()
	return ds
}

func (s *server) request(ctx context.Context, method string, params any, wait time.Duration) (json.RawMessage, error) {
	s.mu.Lock()
	s.nextID++
	id := s.nextID
	key := strconv.Itoa(id)
	ch := make(chan json.RawMessage, 1)
	s.pending[key] = ch
	s.mu.Unlock()

	if err := s.send(map[string]any{"jsonrpc": "2.0", "id": id, "method": method, "params": params}); err != nil {
		s.mu.Lock()
		delete(s.pending, key)
		s.mu.Unlock()
		return nil, err
	}

	select {
	case res := <-ch:
		return res, nil
	case <-ctx.Done():
		return nil, ctx.Err()
	case <-time.After(wait):
		s.mu.Lock()
		delete(s.pending, key)
		s.mu.Unlock()
		return nil, fmt.Errorf("lsp: %s timed out", method)
	}
}

func (s *server) notify(method string, params any) error {
	return s.send(map[string]any{"jsonrpc": "2.0", "method": method, "params": params})
}

func (s *server) reply(id json.RawMessage) {
	_ = s.send(map[string]any{"jsonrpc": "2.0", "id": json.RawMessage(id), "result": nil})
}

func (s *server) send(v any) error {
	data, err := json.Marshal(v)
	if err != nil {
		return err
	}
	s.writeM.Lock()
	defer s.writeM.Unlock()
	if s.closed {
		return fmt.Errorf("lsp: server closed")
	}
	if _, err := fmt.Fprintf(s.stdin, "Content-Length: %d\r\n\r\n", len(data)); err != nil {
		return err
	}
	_, err = s.stdin.Write(data)
	return err
}

func (s *server) close() {
	if s == nil {
		return
	}
	s.writeM.Lock()
	s.closed = true
	s.writeM.Unlock()
	_ = s.stdin.Close()
	if s.cmd.Process != nil {
		_ = s.cmd.Process.Kill()
	}
	_ = s.cmd.Wait()
}

// fileURI turns an absolute path into a file:// URI.
func fileURI(path string) string {
	p := filepath.ToSlash(path)
	if !strings.HasPrefix(p, "/") {
		p = "/" + p
	}
	return "file://" + p
}
