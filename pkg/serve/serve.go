// Package serve exposes one agent conversation over HTTP with SSE
// streaming. Runs are serialized: like the TUI, this surface is a single
// session, not a multi-tenant server.
package serve

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"sync"
	"time"

	"github.com/tamnd/kaku/pkg/engine"
)

type server struct {
	mu     sync.Mutex
	agent  *engine.Agent
	expand func(string) string
}

// Handler builds the HTTP API over one agent.
//
//	GET  /healthz       liveness
//	GET  /v1/history    conversation so far, JSON
//	POST /v1/messages   {"prompt": "..."} then an SSE stream of
//	                    text / tool_start / tool_end / info events,
//	                    closed by done or error
func Handler(a *engine.Agent, expand func(string) string) http.Handler {
	s := &server{agent: a, expand: expand}
	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprintln(w, "ok")
	})
	mux.HandleFunc("GET /v1/history", s.history)
	mux.HandleFunc("POST /v1/messages", s.message)
	return mux
}

// Run serves handler on addr until ctx is done.
func Run(ctx context.Context, addr string, handler http.Handler) error {
	srv := &http.Server{Addr: addr, Handler: handler}
	errc := make(chan error, 1)
	go func() { errc <- srv.ListenAndServe() }()
	select {
	case <-ctx.Done():
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		srv.Shutdown(shutdownCtx)
		return nil
	case err := <-errc:
		if errors.Is(err, http.ErrServerClosed) {
			return nil
		}
		return err
	}
}

func (s *server) history(w http.ResponseWriter, r *http.Request) {
	s.mu.Lock()
	defer s.mu.Unlock()
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(s.agent.Messages)
}

func (s *server) message(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Prompt string `json:"prompt"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.Prompt == "" {
		http.Error(w, `{"error":"prompt is required"}`, http.StatusBadRequest)
		return
	}
	fl, ok := w.(http.Flusher)
	if !ok {
		http.Error(w, `{"error":"streaming unsupported"}`, http.StatusInternalServerError)
		return
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	send := func(event string, v any) {
		data, _ := json.Marshal(v)
		fmt.Fprintf(w, "event: %s\ndata: %s\n\n", event, data)
		fl.Flush()
	}

	prev := s.agent.OnEvent
	defer func() { s.agent.OnEvent = prev }()
	s.agent.OnEvent = func(e engine.Event) {
		switch e.Type {
		case "text":
			send("text", map[string]string{"text": e.Text})
		case "tool_start":
			input := json.RawMessage(e.ToolInput)
			if len(input) == 0 {
				input = json.RawMessage("null")
			}
			send("tool_start", map[string]any{"tool": e.Tool, "input": input})
		case "tool_end":
			send("tool_end", map[string]any{"tool": e.Tool, "output": e.ToolOutput, "is_error": e.IsError})
		case "info":
			send("info", map[string]string{"text": e.Text})
		}
	}

	input := req.Prompt
	if s.expand != nil {
		input = s.expand(input)
	}
	out, err := s.agent.Run(r.Context(), input)
	if err != nil {
		send("error", map[string]string{"error": err.Error()})
		return
	}
	send("done", map[string]any{"output": out, "usage": s.agent.Usage})
}
