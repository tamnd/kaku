package mcp

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/tamnd/kaku/pkg/config"
)

// ringBuffer keeps the tail of the child's stderr so a crash comes back
// with some context attached.
type ringBuffer struct {
	mu  sync.Mutex
	buf []byte
	max int
}

func (r *ringBuffer) Write(p []byte) (int, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.buf = append(r.buf, p...)
	if len(r.buf) > r.max {
		r.buf = r.buf[len(r.buf)-r.max:]
	}
	return len(p), nil
}

func (r *ringBuffer) String() string {
	r.mu.Lock()
	defer r.mu.Unlock()
	return string(r.buf)
}

type stdioTransport struct {
	cmd    *exec.Cmd
	stdin  io.WriteCloser
	stderr *ringBuffer

	writeMu sync.Mutex

	mu      sync.Mutex
	pending map[int64]chan *rpcResponse
	dead    chan struct{}
	readErr error

	closeOnce sync.Once
	closeErr  error
}

func newStdioTransport(cfg config.MCPServer) (*stdioTransport, error) {
	cmd := exec.Command(cfg.Command, cfg.Args...)
	cmd.Env = os.Environ()
	for k, v := range cfg.Env {
		cmd.Env = append(cmd.Env, k+"="+v)
	}
	stdin, err := cmd.StdinPipe()
	if err != nil {
		return nil, err
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return nil, err
	}
	t := &stdioTransport{
		cmd:     cmd,
		stdin:   stdin,
		stderr:  &ringBuffer{max: 4096},
		pending: map[int64]chan *rpcResponse{},
		dead:    make(chan struct{}),
	}
	cmd.Stderr = t.stderr
	if err := cmd.Start(); err != nil {
		return nil, err
	}
	go t.readLoop(stdout)
	return t, nil
}

func (t *stdioTransport) readLoop(stdout io.Reader) {
	sc := bufio.NewScanner(stdout)
	sc.Buffer(make([]byte, 0, 64*1024), 16<<20)
	for sc.Scan() {
		line := sc.Bytes()
		if len(line) == 0 {
			continue
		}
		var resp rpcResponse
		if err := json.Unmarshal(line, &resp); err != nil || resp.ID == nil {
			// Server notification or noise. Nothing waits on it.
			continue
		}
		t.mu.Lock()
		ch, ok := t.pending[*resp.ID]
		if ok {
			delete(t.pending, *resp.ID)
		}
		t.mu.Unlock()
		if ok {
			ch <- &resp
		}
	}
	t.mu.Lock()
	t.readErr = sc.Err()
	t.mu.Unlock()
	close(t.dead)
}

func (t *stdioTransport) write(msg *rpcRequest) error {
	data, err := json.Marshal(msg)
	if err != nil {
		return err
	}
	data = append(data, '\n')
	t.writeMu.Lock()
	defer t.writeMu.Unlock()
	_, err = t.stdin.Write(data)
	return err
}

func (t *stdioTransport) deadErr() error {
	t.mu.Lock()
	readErr := t.readErr
	t.mu.Unlock()
	msg := "server exited"
	if readErr != nil {
		msg = fmt.Sprintf("server exited: %v", readErr)
	}
	if tail := strings.TrimSpace(t.stderr.String()); tail != "" {
		msg += "; stderr: " + tail
	}
	return fmt.Errorf("%s", msg)
}

func (t *stdioTransport) roundTrip(ctx context.Context, req *rpcRequest) (*rpcResponse, error) {
	ch := make(chan *rpcResponse, 1)
	t.mu.Lock()
	t.pending[*req.ID] = ch
	t.mu.Unlock()
	drop := func() {
		t.mu.Lock()
		delete(t.pending, *req.ID)
		t.mu.Unlock()
	}
	if err := t.write(req); err != nil {
		drop()
		return nil, err
	}
	select {
	case resp := <-ch:
		return resp, nil
	case <-ctx.Done():
		drop()
		return nil, ctx.Err()
	case <-t.dead:
		drop()
		// The reader may have delivered the response just before exiting.
		select {
		case resp := <-ch:
			return resp, nil
		default:
		}
		return nil, t.deadErr()
	}
}

func (t *stdioTransport) notify(_ context.Context, req *rpcRequest) error {
	return t.write(req)
}

func (t *stdioTransport) close() error {
	t.closeOnce.Do(func() {
		t.stdin.Close()
		if t.cmd.Process != nil {
			_ = t.cmd.Process.Signal(syscall.SIGTERM)
		}
		waited := make(chan error, 1)
		go func() { waited <- t.cmd.Wait() }()
		var err error
		select {
		case err = <-waited:
		case <-time.After(2 * time.Second):
			_ = t.cmd.Process.Kill()
			err = <-waited
		}
		// We asked the process to die, so a signal exit is not a failure.
		if _, ok := errors.AsType[*exec.ExitError](err); ok {
			err = nil
		}
		t.closeErr = err
	})
	return t.closeErr
}
