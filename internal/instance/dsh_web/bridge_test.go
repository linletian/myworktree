package dsh_web

import (
	"bufio"
	"context"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os/exec"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"myworktree/internal/ws"
)

func TestBridge_rejects_unknown_conn(t *testing.T) {
	bridge := newMuxBridge("127.0.0.1:1", nil, nil)
	req := httptest.NewRequest(http.MethodPost, "/api/remote.mux?mwbridge=1&conn=abcdefgh", strings.NewReader("hello"))
	rec := httptest.NewRecorder()

	bridge.ServeHTTP(rec, req)

	if rec.Code != http.StatusConflict || !strings.Contains(rec.Body.String(), "unknown bridge conn") {
		t.Fatalf("response = %d %q, want 409 unknown bridge conn", rec.Code, rec.Body.String())
	}
}

func TestBridge_rejects_malformed_conn(t *testing.T) {
	bridge := newMuxBridge("127.0.0.1:1", nil, nil)
	for _, connID := range []string{"short", "bad!conn", strings.Repeat("a", 65)} {
		req := httptest.NewRequest(http.MethodPost, "/api/remote.mux?conn="+connID, nil)
		rec := httptest.NewRecorder()
		bridge.ServeHTTP(rec, req)
		if rec.Code != http.StatusBadRequest {
			t.Fatalf("conn %q status = %d, want 400", connID, rec.Code)
		}
	}
}

func TestBridge_relays_text_multiline_ping_and_close(t *testing.T) {
	upstream, pongSeen, closeSeen := newBridgeUpstream(t)
	bridge := newMuxBridge(authorityOf(upstream), nil, nil)
	server := httptest.NewServer(bridge)
	t.Cleanup(server.Close)

	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, server.URL+"/api/remote.mux?mwbridge=1&conn=abcdefgh12345678", nil)
	if err != nil {
		t.Fatal(err)
	}
	resp, err := server.Client().Do(req)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = resp.Body.Close() })
	reader := bufio.NewReader(resp.Body)
	if got := readSSEEvent(t, reader); got != "retry: 300000\n\n" {
		t.Fatalf("first SSE bytes = %q", got)
	}
	if got := readSSEEvent(t, reader); got != "event: open\ndata: {}\n\n" {
		t.Fatalf("open event = %q", got)
	}

	post, err := http.Post(server.URL+"/api/remote.mux?mwbridge=1&conn=abcdefgh12345678", "text/plain", strings.NewReader("hello\nworld"))
	if err != nil {
		t.Fatal(err)
	}
	_ = post.Body.Close()
	if post.StatusCode != http.StatusAccepted {
		t.Fatalf("POST status = %d, want 202", post.StatusCode)
	}
	if got := readSSEEvent(t, reader); got != "data: upstream\n\n" {
		t.Fatalf("upstream event = %q", got)
	}
	if got := readSSEEvent(t, reader); got != "data: hello\ndata: world\n\n" {
		t.Fatalf("echo event = %q", got)
	}
	awaitSignal(t, pongSeen, "upstream pong")

	closeResp, err := http.Post(server.URL+"/api/remote.mux?mwbridge=1&conn=abcdefgh12345678&close=1", "text/plain", nil)
	if err != nil {
		t.Fatal(err)
	}
	_ = closeResp.Body.Close()
	if closeResp.StatusCode != http.StatusAccepted {
		t.Fatalf("close status = %d, want 202", closeResp.StatusCode)
	}
	awaitSignal(t, closeSeen, "upstream close")
}

// Given a live bridge conn, When a bin=1 uplink carries malformed
// base64, Then the answer is a request-scoped 400 and the conn stays
// open — a subsequent VALID uplink still relays upstream and echoes.
func TestBridge_malformed_base64_is_request_scoped(t *testing.T) {
	upstream, _, _ := newBridgeUpstream(t)
	bridge := newMuxBridge(authorityOf(upstream), nil, nil)
	server := httptest.NewServer(bridge)
	t.Cleanup(server.Close)

	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, server.URL+"/api/remote.mux?mwbridge=1&conn=base64bad1234567", nil)
	if err != nil {
		t.Fatal(err)
	}
	resp, err := server.Client().Do(req)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = resp.Body.Close() })
	reader := bufio.NewReader(resp.Body)
	if got := readSSEEvent(t, reader); got != "retry: 300000\n\n" {
		t.Fatalf("first SSE bytes = %q", got)
	}
	if got := readSSEEvent(t, reader); got != "event: open\ndata: {}\n\n" {
		t.Fatalf("open event = %q", got)
	}
	if got := readSSEEvent(t, reader); got != "data: upstream\n\n" {
		t.Fatalf("upstream event = %q", got)
	}

	bad, err := http.Post(server.URL+"/api/remote.mux?mwbridge=1&conn=base64bad1234567&bin=1", "text/plain", strings.NewReader("%%%not-base64%%%"))
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(bad.Body)
	_ = bad.Body.Close()
	if bad.StatusCode != http.StatusBadRequest {
		t.Fatalf("malformed base64 status = %d, want 400", bad.StatusCode)
	}
	if !strings.Contains(string(body), "invalid base64") {
		t.Fatalf("malformed base64 body = %q, want invalid base64 error", body)
	}

	// Conn still alive: a valid binary uplink relays and echoes back.
	good, err := http.Post(server.URL+"/api/remote.mux?mwbridge=1&conn=base64bad1234567&bin=1", "text/plain", strings.NewReader("aGk="))
	if err != nil {
		t.Fatal(err)
	}
	_ = good.Body.Close()
	if good.StatusCode != http.StatusAccepted {
		t.Fatalf("valid uplink after 400 status = %d, want 202 (conn must stay open)", good.StatusCode)
	}
	if got := readSSEEvent(t, reader); got != "event: bin\ndata: aGk=\n\n" {
		t.Fatalf("bin echo = %q, want base64 of \"hi\"", got)
	}
}

func TestBridge_client_disconnect_closes_upstream(t *testing.T) {
	upstream, _, closeSeen := newBridgeUpstream(t)
	bridge := newMuxBridge(authorityOf(upstream), nil, nil)
	server := httptest.NewServer(bridge)
	t.Cleanup(server.Close)
	ctx, cancel := context.WithCancel(context.Background())
	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, server.URL+"/api/remote.mux?mwbridge=1&conn=disconnect123456", nil)
	req.Close = true
	resp, err := server.Client().Do(req)
	if err != nil {
		t.Fatal(err)
	}
	reader := bufio.NewReader(resp.Body)
	_ = readSSEEvent(t, reader)
	_ = readSSEEvent(t, reader)

	cancel()
	_ = resp.Body.Close()
	awaitSignal(t, closeSeen, "close after SSE disconnect")
}

func TestBridge_duplicate_conn_replaces_old(t *testing.T) {
	upstream, _, closeSeen := newBridgeUpstream(t)
	bridge := newMuxBridge(authorityOf(upstream), nil, nil)
	server := httptest.NewServer(bridge)
	t.Cleanup(server.Close)
	open := func() (*http.Response, context.CancelFunc) {
		ctx, cancel := context.WithCancel(context.Background())
		req, _ := http.NewRequestWithContext(ctx, http.MethodGet, server.URL+"/api/remote.mux?mwbridge=1&conn=duplicate1234567", nil)
		resp, err := server.Client().Do(req)
		if err != nil {
			cancel()
			t.Fatal(err)
		}
		reader := bufio.NewReader(resp.Body)
		_ = readSSEEvent(t, reader)
		_ = readSSEEvent(t, reader)
		return resp, cancel
	}
	first, cancelFirst := open()
	defer cancelFirst()
	second, cancelSecond := open()
	defer cancelSecond()
	defer func() { _ = first.Body.Close(); _ = second.Body.Close() }()

	awaitSignal(t, closeSeen, "replaced connection close")
	bridge.mu.Lock()
	count := len(bridge.conns)
	bridge.mu.Unlock()
	if count != 1 {
		t.Fatalf("registered conns = %d, want 1", count)
	}
}

func TestBridge_retries_401_with_fresh_cookie(t *testing.T) {
	var mints atomic.Int32
	var upgrades atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/" {
			n := mints.Add(1)
			w.Header().Set("Location", "/")
			w.Header().Set("Set-Cookie", "dsh-auth-test=v"+string(rune('0'+n))+"; Path=/")
			w.WriteHeader(http.StatusSeeOther)
			return
		}
		if upgrades.Add(1) == 1 {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		conn, err := ws.Upgrade(w, r)
		if err == nil {
			defer conn.Close()
			_, _, _ = conn.ReadMessage()
		}
	}))
	t.Cleanup(server.Close)
	auth := newUpstreamAuth("secret", authorityOf(server))
	bridge := newMuxBridge(authorityOf(server), auth, nil)
	proxy := httptest.NewServer(bridge)
	t.Cleanup(proxy.Close)
	ctx, cancel := context.WithCancel(context.Background())
	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, proxy.URL+"/api/remote.mux?conn=freshcookie12345", nil)
	resp, err := proxy.Client().Do(req)
	if err != nil {
		t.Fatal(err)
	}
	reader := bufio.NewReader(resp.Body)
	_ = readSSEEvent(t, reader)
	if got := readSSEEvent(t, reader); !strings.Contains(got, "event: open") {
		t.Fatalf("event = %q, want open", got)
	}
	cancel()
	_ = resp.Body.Close()
	if mints.Load() != 2 {
		t.Fatalf("mint count = %d, want 2", mints.Load())
	}
}

func TestBridgeShim_node_syntax(t *testing.T) {
	cmd := exec.Command("node", "--check", "static/dsh-bridge.js")
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("node --check: %v\n%s", err, out)
	}
}

func TestInject_adds_shim_to_html_navigation(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		_, _ = io.WriteString(w, "<html><head><title>x</title></head></html>")
	}))
	t.Cleanup(upstream.Close)
	host, port, _ := net.SplitHostPort(authorityOf(upstream))
	ph := &proxyHandler{upstreamHost: host, upstreamPort: port, bridge: newMuxBridge(authorityOf(upstream), nil, nil), h: &Handle{}}
	req := httptest.NewRequest(http.MethodGet, "http://proxy/", nil)
	rec := httptest.NewRecorder()

	ph.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), `<script src="/__mw/dsh-bridge.js"></script></head>`) {
		t.Fatalf("response = %d %q, want injected script", rec.Code, rec.Body.String())
	}
	if rec.Header().Get("Content-Length") != "" {
		t.Fatalf("Content-Length = %q, want empty", rec.Header().Get("Content-Length"))
	}
}

func TestInject_skips_encoded_html_and_forces_identity(t *testing.T) {
	seenEncoding := make(chan string, 1)
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		seenEncoding <- r.Header.Get("Accept-Encoding")
		w.Header().Set("Content-Type", "text/html")
		w.Header().Set("Content-Encoding", "gzip")
		_, _ = w.Write([]byte("compressed-bytes"))
	}))
	t.Cleanup(upstream.Close)
	host, port, _ := net.SplitHostPort(authorityOf(upstream))
	ph := &proxyHandler{upstreamHost: host, upstreamPort: port, bridge: newMuxBridge(authorityOf(upstream), nil, nil), h: &Handle{}}
	req := httptest.NewRequest(http.MethodGet, "http://proxy/", nil)
	req.Header.Set("Accept-Encoding", "gzip")
	rec := httptest.NewRecorder()

	ph.ServeHTTP(rec, req)

	if got := <-seenEncoding; got != "identity" {
		t.Fatalf("Accept-Encoding upstream = %q, want identity", got)
	}
	if strings.Contains(rec.Body.String(), "dsh-bridge.js") {
		t.Fatalf("encoded body was modified: %q", rec.Body.String())
	}
}

func TestInject_leaves_json_untouched(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"head":"</head>"}`)
	}))
	t.Cleanup(upstream.Close)
	host, port, _ := net.SplitHostPort(authorityOf(upstream))
	ph := &proxyHandler{upstreamHost: host, upstreamPort: port, bridge: newMuxBridge(authorityOf(upstream), nil, nil), h: &Handle{}}
	req := httptest.NewRequest(http.MethodGet, "http://proxy/api/value", nil)
	rec := httptest.NewRecorder()

	ph.ServeHTTP(rec, req)

	if got := rec.Body.String(); got != `{"head":"</head>"}` {
		t.Fatalf("JSON body = %q, want untouched", got)
	}
}

func TestInject_serves_embedded_shim(t *testing.T) {
	ph := &proxyHandler{bridge: newMuxBridge("127.0.0.1:1", nil, nil), h: &Handle{}}
	req := httptest.NewRequest(http.MethodGet, "http://proxy/__mw/dsh-bridge.js", nil)
	rec := httptest.NewRecorder()

	ph.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), "MWWebSocket") {
		t.Fatalf("shim response = %d %q", rec.Code, rec.Body.String())
	}
	if got := rec.Header().Get("Content-Type"); got != "text/javascript; charset=utf-8" {
		t.Fatalf("Content-Type = %q", got)
	}
}

func newBridgeUpstream(t *testing.T) (*httptest.Server, <-chan struct{}, <-chan struct{}) {
	t.Helper()
	pongSeen := make(chan struct{}, 4)
	closeSeen := make(chan struct{}, 4)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := ws.Upgrade(w, r)
		if err != nil {
			t.Errorf("upgrade: %v", err)
			return
		}
		defer conn.Close()
		if err := conn.WriteText([]byte("upstream")); err != nil {
			return
		}
		if err := writeServerFrame(conn, 0x9, []byte("probe")); err != nil {
			return
		}
		for {
			op, payload, err := conn.ReadMessage()
			if err != nil {
				return
			}
			switch op {
			case 0x1:
				_ = conn.WriteText(payload)
			case 0x2:
				_ = conn.WriteBinary(payload)
			case 0xA:
				pongSeen <- struct{}{}
			case 0x8:
				closeSeen <- struct{}{}
				return
			}
		}
	}))
	t.Cleanup(server.Close)
	return server, pongSeen, closeSeen
}

func writeServerFrame(conn net.Conn, opcode byte, payload []byte) error {
	_, err := conn.Write(append([]byte{0x80 | opcode, byte(len(payload))}, payload...))
	return err
}

func readSSEEvent(t *testing.T, reader *bufio.Reader) string {
	t.Helper()
	type result struct {
		text string
		err  error
	}
	done := make(chan result, 1)
	go func() {
		var out strings.Builder
		for {
			line, err := reader.ReadString('\n')
			out.WriteString(line)
			if err != nil || line == "\n" {
				done <- result{text: out.String(), err: err}
				return
			}
		}
	}()
	select {
	case got := <-done:
		if got.err != nil && got.err != io.EOF {
			t.Fatalf("read SSE: %v", got.err)
		}
		return got.text
	case <-time.After(5 * time.Second):
		t.Fatal("timed out reading SSE event")
		return ""
	}
}

func awaitSignal(t *testing.T, signal <-chan struct{}, name string) {
	t.Helper()
	select {
	case <-signal:
	case <-time.After(5 * time.Second):
		t.Fatalf("timed out waiting for %s", name)
	}
}
