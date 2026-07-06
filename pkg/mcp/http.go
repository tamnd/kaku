package mcp

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"mime"
	"net/http"
	"strings"
	"sync"
	"time"
)

type httpTransport struct {
	url    string
	client *http.Client

	mu        sync.Mutex
	sessionID string
}

func newHTTPTransport(url string) *httpTransport {
	return &httpTransport{
		url:    url,
		client: &http.Client{Timeout: 5 * time.Minute},
	}
}

func (t *httpTransport) post(ctx context.Context, req *rpcRequest) (*http.Response, error) {
	body, err := json.Marshal(req)
	if err != nil {
		return nil, err
	}
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, t.url, bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("Accept", "application/json, text/event-stream")
	t.mu.Lock()
	if t.sessionID != "" {
		httpReq.Header.Set("Mcp-Session-Id", t.sessionID)
	}
	t.mu.Unlock()
	resp, err := t.client.Do(httpReq)
	if err != nil {
		return nil, err
	}
	if sid := resp.Header.Get("Mcp-Session-Id"); sid != "" {
		t.mu.Lock()
		t.sessionID = sid
		t.mu.Unlock()
	}
	return resp, nil
}

func (t *httpTransport) roundTrip(ctx context.Context, req *rpcRequest) (*rpcResponse, error) {
	resp, err := t.post(ctx, req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		snippet, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
		return nil, fmt.Errorf("http %d: %s", resp.StatusCode, strings.TrimSpace(string(snippet)))
	}
	ct, _, _ := mime.ParseMediaType(resp.Header.Get("Content-Type"))
	switch ct {
	case "application/json":
		var out rpcResponse
		if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
			return nil, err
		}
		return &out, nil
	case "text/event-stream":
		return readSSE(resp.Body, *req.ID)
	default:
		return nil, fmt.Errorf("unexpected content type %q", resp.Header.Get("Content-Type"))
	}
}

// readSSE scans an event stream and returns the first data payload whose
// id matches the request. Other events on the stream are skipped.
func readSSE(r io.Reader, wantID int64) (*rpcResponse, error) {
	sc := bufio.NewScanner(r)
	sc.Buffer(make([]byte, 0, 64*1024), 16<<20)
	var data []string
	handle := func() (*rpcResponse, bool) {
		if len(data) == 0 {
			return nil, false
		}
		payload := strings.Join(data, "\n")
		data = nil
		var out rpcResponse
		if err := json.Unmarshal([]byte(payload), &out); err != nil {
			return nil, false
		}
		if out.ID == nil || *out.ID != wantID {
			return nil, false
		}
		return &out, true
	}
	for sc.Scan() {
		line := sc.Text()
		switch {
		case line == "":
			if out, ok := handle(); ok {
				return out, nil
			}
		case strings.HasPrefix(line, "data:"):
			data = append(data, strings.TrimPrefix(strings.TrimPrefix(line, "data:"), " "))
		}
	}
	if err := sc.Err(); err != nil {
		return nil, err
	}
	if out, ok := handle(); ok {
		return out, nil
	}
	return nil, fmt.Errorf("event stream ended without a response for id %d", wantID)
}

func (t *httpTransport) notify(ctx context.Context, req *rpcRequest) error {
	resp, err := t.post(ctx, req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	io.Copy(io.Discard, io.LimitReader(resp.Body, 4096))
	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		return fmt.Errorf("http %d on notification", resp.StatusCode)
	}
	return nil
}

func (t *httpTransport) close() error {
	t.client.CloseIdleConnections()
	return nil
}
