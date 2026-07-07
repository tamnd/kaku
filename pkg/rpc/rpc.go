// Package rpc exposes one agent conversation over a newline-delimited JSON
// protocol on stdin/stdout. It is the surface an editor embeds: the caller
// sends command lines and reads back event and response lines, and permission
// prompts round-trip instead of degrading to deny.
//
// Commands (one JSON object per line on stdin):
//
//	{"type":"prompt","id":1,"text":"..."}   run the agent on text
//	{"type":"abort","id":2}                  cancel the running prompt
//	{"type":"new_session","id":3}            reset the conversation
//	{"type":"get_messages","id":4}           return the conversation
//	{"type":"set_model","id":5,"model":"x"}  switch the active model
//	{"type":"get_state","id":6}              return model, mode, cwd, count
//	{"type":"permission_response","id":7,"allow":true,"always":false}
//
// Output (one JSON object per line on stdout): a ready line first, then for a
// prompt the same event shapes as the headless JSON mode (text, thinking,
// tool_start, tool_end, turn, info), a permission_request when a tool needs an
// answer, and a response line that echoes the command id when a command
// finishes. Failures are error lines carrying the command id.
package rpc

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"maps"
	"sync"

	"github.com/tamnd/kaku/pkg/engine"
)

// Options wires the RPC server to one built agent and its lifecycle hooks.
type Options struct {
	Agent      *engine.Agent
	Expand     func(string) string // skill/mention expansion, may be nil
	NewSession func() error        // reset the conversation, may be nil
	SetModel   func(string) error  // switch the active model, may be nil
	Mode       string              // permission mode, for get_state
	Dir        string              // working directory, for get_state
}

// Server runs the protocol loop. It is single-use per Serve call.
type Server struct {
	opt Options

	enc   *json.Encoder
	encMu sync.Mutex

	mu       sync.Mutex
	running  bool
	cancel   context.CancelFunc
	permSeq  int
	permWait map[int]chan engine.Answer
}

// New returns a server over the given options.
func New(opt Options) *Server {
	return &Server{opt: opt, permWait: map[int]chan engine.Answer{}}
}

type command struct {
	Type   string `json:"type"`
	ID     int    `json:"id"`
	Text   string `json:"text"`
	Model  string `json:"model"`
	Allow  bool   `json:"allow"`
	Always bool   `json:"always"`
}

// event is one streamed line. Zero-value fields drop off the wire so a text
// event and a tool event share the struct without leaking empty keys.
type event struct {
	Type         string          `json:"type"`
	ID           int             `json:"id,omitempty"`
	Text         string          `json:"text,omitempty"`
	Tool         string          `json:"tool,omitempty"`
	Arg          string          `json:"arg,omitempty"`
	Input        json.RawMessage `json:"input,omitempty"`
	Output       string          `json:"output,omitempty"`
	IsError      bool            `json:"is_error,omitempty"`
	InputTokens  int             `json:"input_tokens,omitempty"`
	OutputTokens int             `json:"output_tokens,omitempty"`
}

// Serve reads commands from in and writes events and responses to out until in
// reaches EOF or ctx is done. It returns the read error, if any.
func (s *Server) Serve(ctx context.Context, in io.Reader, out io.Writer) error {
	s.enc = json.NewEncoder(out)
	s.emitReady()

	sc := bufio.NewScanner(in)
	sc.Buffer(make([]byte, 0, 64*1024), 8*1024*1024)
	for sc.Scan() {
		line := bytes.TrimSpace(sc.Bytes())
		if len(line) == 0 {
			continue
		}
		var cmd command
		if err := json.Unmarshal(line, &cmd); err != nil {
			s.send(map[string]any{"type": "error", "error": "bad command: " + err.Error()})
			continue
		}
		if err := ctx.Err(); err != nil {
			return err
		}
		s.dispatch(ctx, cmd)
	}
	return sc.Err()
}

func (s *Server) dispatch(ctx context.Context, cmd command) {
	switch cmd.Type {
	case "prompt":
		s.startPrompt(ctx, cmd)
	case "abort":
		s.abort()
		s.respond(cmd.ID, map[string]any{"aborted": true})
	case "new_session":
		if s.opt.NewSession != nil {
			if err := s.opt.NewSession(); err != nil {
				s.fail(cmd.ID, err)
				return
			}
		}
		s.respond(cmd.ID, map[string]any{"ok": true})
	case "get_messages":
		s.respond(cmd.ID, map[string]any{"messages": s.opt.Agent.Messages})
	case "set_model":
		if s.opt.SetModel != nil {
			if err := s.opt.SetModel(cmd.Model); err != nil {
				s.fail(cmd.ID, err)
				return
			}
		}
		s.respond(cmd.ID, map[string]any{"model": s.opt.Agent.Model})
	case "get_state":
		s.respond(cmd.ID, s.state())
	case "permission_response":
		s.answerPermission(cmd.ID, engine.Answer{Allow: cmd.Allow, Always: cmd.Always})
	default:
		s.fail(cmd.ID, errors.New("unknown command "+cmd.Type))
	}
}

func (s *Server) startPrompt(ctx context.Context, cmd command) {
	s.mu.Lock()
	if s.running {
		s.mu.Unlock()
		s.fail(cmd.ID, errors.New("a prompt is already running"))
		return
	}
	runCtx, cancel := context.WithCancel(ctx)
	s.running = true
	s.cancel = cancel
	s.mu.Unlock()

	input := cmd.Text
	if s.opt.Expand != nil {
		input = s.opt.Expand(input)
	}

	a := s.opt.Agent
	go func() {
		prevEvent, prevAsk := a.OnEvent, a.Ask
		a.OnEvent = s.emit
		a.Ask = func(tool, arg string) engine.Answer { return s.ask(runCtx, tool, arg) }
		defer func() {
			a.OnEvent, a.Ask = prevEvent, prevAsk
			cancel()
			s.mu.Lock()
			s.running = false
			s.cancel = nil
			s.mu.Unlock()
		}()

		out, err := a.Run(runCtx, input)
		if err != nil {
			s.fail(cmd.ID, err)
			return
		}
		s.respond(cmd.ID, map[string]any{"text": out, "usage": a.Usage})
	}()
}

func (s *Server) abort() {
	s.mu.Lock()
	cancel := s.cancel
	s.mu.Unlock()
	if cancel != nil {
		cancel()
	}
}

// ask emits a permission_request and blocks until the caller answers it with a
// permission_response or the run is aborted, which denies.
func (s *Server) ask(ctx context.Context, tool, arg string) engine.Answer {
	s.mu.Lock()
	s.permSeq++
	id := s.permSeq
	ch := make(chan engine.Answer, 1)
	s.permWait[id] = ch
	s.mu.Unlock()

	s.send(event{Type: "permission_request", ID: id, Tool: tool, Arg: arg})

	select {
	case ans := <-ch:
		return ans
	case <-ctx.Done():
		s.mu.Lock()
		delete(s.permWait, id)
		s.mu.Unlock()
		return engine.Answer{}
	}
}

func (s *Server) answerPermission(id int, ans engine.Answer) {
	s.mu.Lock()
	ch := s.permWait[id]
	delete(s.permWait, id)
	s.mu.Unlock()
	if ch != nil {
		ch <- ans
	}
}

func (s *Server) emit(e engine.Event) {
	switch e.Type {
	case "text":
		s.send(event{Type: "text", Text: e.Text})
	case "thinking":
		s.send(event{Type: "thinking", Text: e.Text})
	case "tool_start":
		in := json.RawMessage(e.ToolInput)
		if len(in) == 0 {
			in = json.RawMessage("null")
		}
		s.send(event{Type: "tool_start", Tool: e.Tool, Input: in})
	case "tool_end":
		s.send(event{Type: "tool_end", Tool: e.Tool, Output: e.ToolOutput, IsError: e.IsError})
	case "turn":
		if e.Usage != nil {
			s.send(event{Type: "turn", InputTokens: e.Usage.InputTokens, OutputTokens: e.Usage.OutputTokens})
		}
	case "info":
		s.send(event{Type: "info", Text: e.Text})
	}
}

func (s *Server) emitReady() {
	s.send(map[string]any{
		"type":  "ready",
		"model": s.opt.Agent.Model,
		"mode":  s.opt.Mode,
		"cwd":   s.opt.Dir,
	})
}

func (s *Server) state() map[string]any {
	return map[string]any{
		"model":    s.opt.Agent.Model,
		"mode":     s.opt.Mode,
		"cwd":      s.opt.Dir,
		"messages": len(s.opt.Agent.Messages),
	}
}

func (s *Server) respond(id int, fields map[string]any) {
	m := map[string]any{"type": "response", "id": id}
	maps.Copy(m, fields)
	s.send(m)
}

func (s *Server) fail(id int, err error) {
	s.send(map[string]any{"type": "error", "id": id, "error": err.Error()})
}

func (s *Server) send(v any) {
	s.encMu.Lock()
	defer s.encMu.Unlock()
	s.enc.Encode(v)
}
