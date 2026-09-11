package dsh_web

import (
	"bufio"
	"bytes"
	"encoding/json"
	"io"
	"log"
	"net"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"myworktree/internal/framework"
)

// newProxyFixture wires a proxyHandler against a mock upstream that
// echoes request facts (Host, Origin, query, body) so tests can assert
// the Director's rewriting.
func newProxyFixture(t *testing.T, cfg ProxyConfig, worktree string) (*proxyHandler, *httptest.Server, *ScopeTracker, *Handle) {
	t.Helper()
	tracker := NewScopeTracker()
	h := &Handle{instanceID: "inst-1", cwd: worktree}

	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		echo := map[string]any{
			"host":    r.Host,
			"origin":  r.Header.Get("Origin"),
			"referer": r.Header.Get("Referer"),
			"query":   r.URL.RawQuery,
			"path":    r.URL.Path,
			"token":   r.URL.Query().Get("token"),
			"cookie":  r.Header.Get("Cookie"),
		}
		if r.Method == http.MethodPost {
			b, _ := io.ReadAll(r.Body)
			echo["body"] = string(b)
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(echo)
	}))
	t.Cleanup(upstream.Close)

	u, _, _ := net.SplitHostPort(strings.TrimPrefix(upstream.URL, "http://"))
	ph := &proxyHandler{
		upstreamHost: u,
		upstreamPort: portOf(t, upstream.URL),
		worktree:     worktree,
		instanceID:   "inst-1",
		cfg:          cfg,
		tracker:      tracker,
		h:            h,
	}
	return ph, upstream, tracker, h
}

func portOf(t *testing.T, rawURL string) string {
	t.Helper()
	_, port, err := net.SplitHostPort(strings.TrimPrefix(rawURL, "http://"))
	if err != nil {
		t.Fatal(err)
	}
	return port
}

func doProxy(t *testing.T, ph *proxyHandler, method, path string, body []byte, token string) (*http.Response, map[string]any) {
	t.Helper()
	var rd io.Reader
	if body != nil {
		rd = bytes.NewReader(body)
	}
	req := httptest.NewRequest(method, "http://127.0.0.1:39999"+path, rd)
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	req.Header.Set("Origin", "http://127.0.0.1:39999")
	if token != "" {
		req.URL.RawQuery = "token=" + token
	}
	rec := httptest.NewRecorder()
	ph.ServeHTTP(rec, req)
	resp := rec.Result()
	var echo map[string]any
	if resp.StatusCode == http.StatusOK {
		_ = json.NewDecoder(resp.Body).Decode(&echo)
	}
	return resp, echo
}

func TestProxyLocalModeForwardsAndRewrites(t *testing.T) {
	ph, _, _, _ := newProxyFixture(t, ProxyConfig{BindHost: "127.0.0.1"}, "/wt")

	resp, echo := doProxy(t, ph, http.MethodGet, "/api/some/path", nil, "")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
	if echo["host"] == "127.0.0.1:39999" {
		t.Errorf("upstream Host not rewritten: %v", echo["host"])
	}
	if echo["origin"] != "" {
		t.Errorf("Origin not deleted: %q", echo["origin"])
	}
	if echo["token"] != "" {
		t.Errorf("token leaked upstream: %q", echo["token"])
	}
}

func TestProxyTokenGate(t *testing.T) {
	cfg := ProxyConfig{BindHost: "127.0.0.1", RequireToken: true, AuthToken: "sekret"}
	ph, _, _, _ := newProxyFixture(t, cfg, "/wt")

	// No token, non-loopback client → 401, nothing forwarded.
	resp, _ := doProxy(t, ph, http.MethodGet, "/", nil, "")
	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("status without token = %d, want 401", resp.StatusCode)
	}

	// Wrong token → 401.
	resp, _ = doProxy(t, ph, http.MethodGet, "/", nil, "wrong")
	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("status with wrong token = %d, want 401", resp.StatusCode)
	}

	// Query token (first navigation) → 302 to the token-free URL, cookie
	// synced, nothing forwarded upstream. The embedded document must
	// never retain the token in its own location.search.
	resp, _ = doProxy(t, ph, http.MethodGet, "/", nil, "sekret")
	if resp.StatusCode != http.StatusFound {
		t.Fatalf("status with token = %d, want 302", resp.StatusCode)
	}
	if loc := resp.Header.Get("Location"); loc != "/" {
		t.Errorf("redirect Location = %q, want token-free %q", loc, "/")
	}
	cookies := resp.Cookies()
	found := false
	for _, c := range cookies {
		if c.Name == "mw_token" && c.Value == "sekret" {
			found = true
		}
	}
	if !found {
		t.Error("mw_token cookie not set on query-token success")
	}

	// Cookie → 200 without query token (the redirected navigation).
	req := httptest.NewRequest(http.MethodGet, "http://127.0.0.1:39999/", nil)
	req.AddCookie(&http.Cookie{Name: "mw_token", Value: "sekret"})
	rec := httptest.NewRecorder()
	ph.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status with cookie = %d, want 200", rec.Code)
	}

	// Loopback client bypasses the gate entirely — a local browser must
	// reach the embed even when the proxy binds the main listener's
	// non-loopback host (default 0.0.0.0 listen + remote token gate).
	req = httptest.NewRequest(http.MethodGet, "http://127.0.0.1:39999/", nil)
	req.RemoteAddr = "127.0.0.1:53123"
	rec = httptest.NewRecorder()
	ph.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status for loopback client = %d, want 200 (gate bypass)", rec.Code)
	}
}

// TestProxyStripsMwTokenCookieUpstream pins the credential-leak fix:
// checkToken syncs mw_token onto the PROXY origin, and the Director
// must strip that cookie before forwarding so the unauthenticated dsh
// subprocess never sees the main token — while other cookies (set by
// the SPA itself) still ride through.
func TestProxyStripsMwTokenCookieUpstream(t *testing.T) {
	cfg := ProxyConfig{BindHost: "127.0.0.1", RequireToken: true, AuthToken: "sekret"}
	ph, _, _, _ := newProxyFixture(t, cfg, "/wt")

	// Loopback client + mixed cookies: mw_token must not reach the
	// upstream; a foreign cookie (e.g. the dsh SPA's own) must.
	req := httptest.NewRequest(http.MethodGet, "http://127.0.0.1:39999/", nil)
	req.RemoteAddr = "127.0.0.1:53123"
	req.AddCookie(&http.Cookie{Name: "mw_token", Value: "sekret"})
	req.AddCookie(&http.Cookie{Name: "spa_pref", Value: "dark"})
	rec := httptest.NewRecorder()
	ph.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	var echo map[string]any
	_ = json.NewDecoder(rec.Body).Decode(&echo)
	upstreamCookie, _ := echo["cookie"].(string)
	if strings.Contains(upstreamCookie, "mw_token") {
		t.Errorf("mw_token leaked upstream in Cookie header %q", upstreamCookie)
	}
	if !strings.Contains(upstreamCookie, "spa_pref=dark") {
		t.Errorf("non-auth cookie dropped upstream: %q", upstreamCookie)
	}

	// mw_token alone → no Cookie header at all upstream.
	req = httptest.NewRequest(http.MethodGet, "http://127.0.0.1:39999/", nil)
	req.RemoteAddr = "127.0.0.1:53123"
	req.AddCookie(&http.Cookie{Name: "mw_token", Value: "sekret"})
	rec = httptest.NewRecorder()
	ph.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	_ = json.NewDecoder(rec.Body).Decode(&echo)
	if c, _ := echo["cookie"].(string); c != "" {
		t.Errorf("Cookie header %q still forwarded upstream, want empty", c)
	}
}

// TestProxyStripsRefererUpstream pins the Referer credential-leak fix:
// after the token-strip 302, same-origin follow-ups carry
// `Referer: http://<proxy>/?token=<mw_token>` — the Director must
// delete the header so the dsh subprocess never sees the mw token.
func TestProxyStripsRefererUpstream(t *testing.T) {
	cfg := ProxyConfig{BindHost: "127.0.0.1", RequireToken: true, AuthToken: "sekret"}
	ph, _, _, _ := newProxyFixture(t, cfg, "/wt")

	req := httptest.NewRequest(http.MethodGet, "http://127.0.0.1:39999/api/some/path", nil)
	req.RemoteAddr = "127.0.0.1:53123" // loopback: gate bypassed, request forwarded
	req.Header.Set("Referer", "http://127.0.0.1:39999/?token=sekret")
	rec := httptest.NewRecorder()
	ph.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	var echo map[string]any
	_ = json.NewDecoder(rec.Body).Decode(&echo)
	if ref, _ := echo["referer"].(string); ref != "" {
		t.Errorf("Referer leaked upstream: %q", ref)
	}
}

func TestProxyScopeRecording(t *testing.T) {
	ph, _, tracker, _ := newProxyFixture(t, ProxyConfig{BindHost: "127.0.0.1"}, "/wt")

	envelope := func(method string, payload any) []byte {
		b, _ := json.Marshal(map[string]any{
			"type": "client-request", "rpcId": "r1", "method": method, "payload": payload,
		})
		return b
	}

	// Out-of-scope session.create {cwd} → recorded out-of-scope.
	doProxy(t, ph, http.MethodPost, "/api", envelope("session.create", map[string]string{"cwd": "/other"}), "")
	st, ok := tracker.Get("inst-1")
	if !ok || st.Scope != ScopeOutOfScope || st.Directory != "/other" {
		t.Errorf("out-of-scope record = %+v ok=%v", st, ok)
	}

	// In-scope workspace.create {path} → recorded in-scope.
	doProxy(t, ph, http.MethodPost, "/api", envelope("workspace.create", map[string]string{"path": "/wt"}), "")
	st, ok = tracker.Get("inst-1")
	if !ok || st.Scope != ScopeInScope {
		t.Errorf("in-scope record = %+v ok=%v", st, ok)
	}

	// GET (not POST) → no record (the observation is body-based).
	doProxy(t, ph, http.MethodGet, "/api/whatever", nil, "")
	st, _ = tracker.Get("inst-1")
	if st.Scope != ScopeInScope {
		t.Errorf("GET clobbered scope = %+v", st)
	}

	// Non-RPC body → no record.
	doProxy(t, ph, http.MethodPost, "/api", []byte(`{"hello":"world"}`), "")
	st, _ = tracker.Get("inst-1")
	if st.Scope != ScopeInScope {
		t.Errorf("non-RPC body clobbered scope = %+v", st)
	}
}

func TestClassifyRPCBody(t *testing.T) {
	env := func(method string, payload any) []byte {
		b, _ := json.Marshal(map[string]any{
			"type": "client-request", "rpcId": "r1", "method": method, "payload": payload,
		})
		return b
	}

	cases := []struct {
		name     string
		body     []byte
		worktree string
		wsID     string // worktree's own workspace id (from bootstrap)
		want     Scope
		dir      string
		ok       bool
	}{
		{"session cwd in-scope", env("session.create", map[string]string{"cwd": "/wt"}), "/wt", "w1", ScopeInScope, "/wt", true},
		{"session cwd out-of-scope", env("session.create", map[string]string{"cwd": "/other"}), "/wt", "w1", ScopeOutOfScope, "/other", true},
		{"workspace.create in-scope", env("workspace.create", map[string]string{"path": "/wt"}), "/wt", "w1", ScopeInScope, "/wt", true},
		{"workspace.create out-of-scope", env("workspace.create", map[string]string{"path": "/x"}), "/wt", "w1", ScopeOutOfScope, "/x", true},
		{"session workspaceId in-scope", env("session.create", map[string]string{"workspaceId": "w1"}), "/wt", "w1", ScopeInScope, "w1", true},
		{"session workspaceId out-of-scope", env("session.create", map[string]string{"workspaceId": "w2"}), "/wt", "w1", ScopeOutOfScope, "w2", true},
		{"session workspaceId unknown worktree ws", env("session.create", map[string]string{"workspaceId": "w1"}), "/wt", "", ScopeInScope, "", false},
		{"non-RPC body", []byte(`{"foo":1}`), "/wt", "w1", ScopeInScope, "", false},
		{"wrong type", env("server-response", nil), "/wt", "w1", ScopeInScope, "", false},
		{"unknown method", env("settings.get", map[string]string{}), "/wt", "w1", ScopeInScope, "", false},
		{"empty payload", env("session.create", map[string]string{}), "/wt", "w1", ScopeInScope, "", false},
	}
	for _, c := range cases {
		st, ok := classifyRPCBody(c.body, c.worktree, c.wsID)
		if ok != c.ok {
			t.Errorf("%s: ok = %v, want %v", c.name, ok, c.ok)
			continue
		}
		if ok {
			if st.Scope != c.want || st.Directory != c.dir {
				t.Errorf("%s: state = %+v, want scope=%s dir=%s", c.name, st, c.want, c.dir)
			}
		}
	}
}

// TestProxyBridgeGatedOnRemoteCapable pins the remote bridge gate:
// with cfg.RequireToken=true but blob.RemoteCapable=false (a dsh
// 0.1.2-0.1.4 shape — token-bearing but below the remote floor),
// startProxyListener must NOT construct the bridge. The shim URL, the
// bridge endpoint and HTML injection all belong to the bridge, so each
// falls through to the upstream untouched.
func TestProxyBridgeGatedOnRemoteCapable(t *testing.T) {
	var mu sync.Mutex
	seen := map[string]int{}
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		seen[r.URL.Path]++
		mu.Unlock()
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		_, _ = io.WriteString(w, "<html><head><title>x</title></head></html>")
	}))
	t.Cleanup(upstream.Close)
	u, _, _ := net.SplitHostPort(strings.TrimPrefix(upstream.URL, "http://"))

	// RequireToken=true but RemoteCapable zero-value false: no bridge.
	h := &Handle{instanceID: "inst-1", cwd: "/wt"}
	host, port, closeFn, err := startProxyListener(h, ProxyConfig{
		BindHost:     "127.0.0.1",
		RequireToken: true,
		AuthToken:    "sekret",
	}, u, portOf(t, upstream.URL), NewScopeTracker(), nil)
	if err != nil {
		t.Fatal(err)
	}
	defer closeFn()
	base := "http://" + net.JoinHostPort(host, port)

	// Loopback client: the token gate bypasses, so every request is
	// forwarded — what matters is WHO answers.
	resp, err := http.Get(base + "/__mw/dsh-bridge.js")
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if strings.Contains(string(body), "MWWebSocket") {
		t.Error("bridge shim served by mw despite RemoteCapable=false")
	}
	resp, err = http.Get(base + "/api/remote.mux?mwbridge=1")
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	resp, err = http.Get(base + "/")
	if err != nil {
		t.Fatal(err)
	}
	body, _ = io.ReadAll(resp.Body)
	resp.Body.Close()
	if strings.Contains(string(body), `<script src="/__mw/dsh-bridge.js"></script>`) {
		t.Error("shim injected into HTML despite RemoteCapable=false")
	}

	mu.Lock()
	defer mu.Unlock()
	if seen["/__mw/dsh-bridge.js"] != 1 {
		t.Errorf("upstream saw /__mw/dsh-bridge.js %d times, want 1 (fell through)", seen["/__mw/dsh-bridge.js"])
	}
	if seen["/api/remote.mux"] != 1 {
		t.Errorf("upstream saw /api/remote.mux %d times, want 1 (fell through)", seen["/api/remote.mux"])
	}
}

// TestProxyWebSocketPassthrough drives a raw Upgrade handshake through
// the proxy and round-trips bytes — the dsh event channel
// (/api/events.mux, /api/events.host) depends on this path.
func TestProxyWebSocketPassthrough(t *testing.T) {
	// Upstream: an echo "websocket" that hijacks the connection.
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Upgrade") != "websocket" {
			http.Error(w, "not an upgrade", http.StatusBadRequest)
			return
		}
		conn, bufrw, err := w.(http.Hijacker).Hijack()
		if err != nil {
			t.Errorf("hijack: %v", err)
			return
		}
		defer conn.Close()
		_, _ = bufrw.WriteString("HTTP/1.1 101 Switching Protocols\r\nUpgrade: websocket\r\nConnection: Upgrade\r\n\r\n")
		_ = bufrw.Flush()
		// Echo one line.
		line, err := bufrw.ReadString('\n')
		if err != nil {
			return
		}
		_, _ = bufrw.WriteString("echo:" + line)
		_ = bufrw.Flush()
	}))
	t.Cleanup(upstream.Close)
	u, _, _ := net.SplitHostPort(strings.TrimPrefix(upstream.URL, "http://"))
	ph := &proxyHandler{
		upstreamHost: u,
		upstreamPort: portOf(t, upstream.URL),
		worktree:     "/wt",
		instanceID:   "inst-1",
		cfg:          ProxyConfig{BindHost: "127.0.0.1"},
	}

	// Serve the proxy handler on a real listener.
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	srv := &http.Server{Handler: ph}
	go func() { _ = srv.Serve(ln) }()
	defer srv.Close()

	conn, err := net.DialTimeout("tcp", ln.Addr().String(), 3*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	req := "GET /api/events.mux HTTP/1.1\r\nHost: " + ln.Addr().String() + "\r\nUpgrade: websocket\r\nConnection: Upgrade\r\n\r\n"
	if _, err := conn.Write([]byte(req)); err != nil {
		t.Fatal(err)
	}
	br := bufio.NewReader(conn)
	status, err := br.ReadString('\n')
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(status, "101") {
		t.Fatalf("handshake status = %q, want 101", strings.TrimSpace(status))
	}
	// Drain the remaining handshake headers.
	for {
		line, err := br.ReadString('\n')
		if err != nil {
			t.Fatal(err)
		}
		if line == "\r\n" {
			break
		}
	}
	if _, err := conn.Write([]byte("ping\n")); err != nil {
		t.Fatal(err)
	}
	echo, err := br.ReadString('\n')
	if err != nil {
		t.Fatal(err)
	}
	if strings.TrimSpace(echo) != "echo:ping" {
		t.Errorf("echo = %q, want echo:ping", strings.TrimSpace(echo))
	}
}

// fakePublisher is a minimal framework.Publisher for unit tests.
type fakePublisher struct {
	id         string
	markFailed func(reason string)
	updateBlob func(b json.RawMessage)
}

func (f *fakePublisher) InstanceID() string { return f.id }
func (f *fakePublisher) MarkRunning() error { return nil }
func (f *fakePublisher) MarkFailed(r string) error {
	if f.markFailed != nil {
		f.markFailed(r)
	}
	return nil
}
func (f *fakePublisher) MarkExited(c int) error { return nil }
func (f *fakePublisher) SetPID(p int) error     { return nil }
func (f *fakePublisher) UpdateKindBlob(b json.RawMessage) error {
	if f.updateBlob != nil {
		f.updateBlob(b)
	}
	return nil
}

// TestProxyListenerDeathFailsLoud pins the fail-loud contract for an
// unexpected proxy-listener death: the health loop probes only the
// UPSTREAM, so a dead proxy must mark the instance failed, clear the
// dead iframe URL from the blob, and tear the handle down (no silent
// white iframe, no orphaned "running" state).
func TestProxyListenerDeathFailsLoud(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte("up"))
	}))
	t.Cleanup(upstream.Close)
	u, _, _ := net.SplitHostPort(strings.TrimPrefix(upstream.URL, "http://"))

	h := &Handle{instanceID: "inst-1", cwd: "/wt"}
	h.blob = Blob{IframeURL: "http://127.0.0.1:39998/"}

	marked := make(chan string, 1)
	var pp framework.Publisher = &fakePublisher{
		markFailed: func(r string) { marked <- r },
	}
	h.publisher.Store(&pp)

	// ServeTLS with a certificate that cannot load fails immediately
	// inside the serve goroutine → the death path must fire.
	dir := t.TempDir()
	_, _, closeFn, err := startProxyListener(h, ProxyConfig{
		BindHost: "127.0.0.1",
		TLSCert:  filepath.Join(dir, "missing-cert.pem"),
		TLSKey:   filepath.Join(dir, "missing-key.pem"),
	}, u, portOf(t, upstream.URL), NewScopeTracker(), log.New(io.Discard, "", 0))
	if err != nil {
		t.Fatal(err)
	}
	defer closeFn()

	select {
	case reason := <-marked:
		if reason == "" {
			t.Error("MarkFailed called with empty reason")
		}
	case <-time.After(2 * time.Second):
		t.Fatal("proxy death did not mark the instance failed within 2s")
	}

	h.mu.Lock()
	dead := h.proxyDead
	iframe := h.blob.IframeURL
	h.mu.Unlock()
	if !dead {
		t.Error("proxyDead flag not set")
	}
	if iframe != "" {
		t.Errorf("blob.IframeURL = %q after listener death, want cleared", iframe)
	}
}

// TestProxyUpstreamMidBodyFailureAbortsConnection pins the fail-loud
// behavior when the upstream dies AFTER response headers were written
// (REVIEW-2026-08-15.md #4): a 502 is impossible at that point, so the
// proxy must abort the connection — the client sees an error, not a
// truncated response that looks like a success.
func TestProxyUpstreamMidBodyFailureAbortsConnection(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Declare 5 bytes, write 2, then return: the stdlib server
		// closes the connection mid-body.
		w.Header().Set("Content-Length", "5")
		_, _ = w.Write([]byte("ab"))
	}))
	t.Cleanup(upstream.Close)
	u, _, _ := net.SplitHostPort(strings.TrimPrefix(upstream.URL, "http://"))

	h := &Handle{instanceID: "inst-1", cwd: "/wt"}
	host, port, closeFn, err := startProxyListener(h, ProxyConfig{BindHost: "127.0.0.1"}, u, portOf(t, upstream.URL), NewScopeTracker(), nil)
	if err != nil {
		t.Fatal(err)
	}
	defer closeFn()

	resp, err := http.Get("http://" + net.JoinHostPort(host, port) + "/")
	if err != nil {
		t.Fatalf("GET via proxy: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200 (the failure is in the body stream)", resp.StatusCode)
	}
	body, err := io.ReadAll(resp.Body)
	if err == nil {
		t.Fatalf("mid-body upstream failure produced a clean body %q; want connection abort", body)
	}
	if strings.Contains(string(body), "dsh server unreachable") {
		t.Errorf("body contains the 502 JSON, but headers were already sent: %q", body)
	}
}

// TestProxySessionOwnMarking pins the write-driving RPC attribution:
// a session.prompt (mutating the log) marks its sessionId as owned by
// this instance in the session watch, while a read-only session.history
// must NOT (opening a session is not writing — marking it would
// suppress the foreign-activity warning exactly when it matters).
func TestProxySessionOwnMarking(t *testing.T) {
	ph, _, _, _ := newProxyFixture(t, ProxyConfig{BindHost: "127.0.0.1"}, "/wt")
	watch := NewSessionWatch(t.TempDir(), nil)
	ph.h.watch = watch

	envelope := func(method, payload string) []byte {
		return []byte(`{"type":"client-request","rpcId":"r1","method":"` + method + `","payload":` + payload + `}`)
	}
	ownCount := func() int {
		watch.mu.Lock()
		defer watch.mu.Unlock()
		return len(watch.own)
	}

	doProxy(t, ph, http.MethodPost, "/api", envelope("session.prompt", `{"sessionId":"s-1"}`), "")
	if ownCount() != 1 {
		t.Fatalf("own sessions = %d, want 1 after session.prompt", ownCount())
	}
	doProxy(t, ph, http.MethodPost, "/api", envelope("subagent.prompt", `{"parentSessionId":"s-p","childSessionId":"s-c"}`), "")
	if ownCount() != 3 {
		t.Fatalf("own sessions = %d, want 3 after subagent.prompt", ownCount())
	}
	doProxy(t, ph, http.MethodPost, "/api", envelope("session.history", `{"sessionId":"s-readonly"}`), "")
	if ownCount() != 3 {
		t.Fatalf("own sessions = %d, want 3 (session.history is read-only)", ownCount())
	}
	doProxy(t, ph, http.MethodPost, "/api", envelope("session.cancel", `{"sessionId":"s-1"}`), "")
	if ownCount() != 3 {
		t.Fatalf("own sessions = %d, want 3 (s-1 already marked)", ownCount())
	}
}

// TestProxySessionCreateResponseMarking pins the response tee: the
// session id of a session.create lives in the response VALUE
// ({sessionId}), not the request — the proxy must parse the streamed
// envelope and mark it owned.
func TestProxySessionCreateResponseMarking(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"type":"server-response","rpcId":"r1","result":{"ok":true,"value":{"sessionId":"s-created"}}}`))
	}))
	t.Cleanup(upstream.Close)
	u, _, _ := net.SplitHostPort(strings.TrimPrefix(upstream.URL, "http://"))

	h := &Handle{instanceID: "inst-1", cwd: "/wt"}
	watch := NewSessionWatch(t.TempDir(), nil)
	h.watch = watch
	ph := &proxyHandler{
		upstreamHost: u,
		upstreamPort: portOf(t, upstream.URL),
		worktree:     "/wt",
		instanceID:   "inst-1",
		cfg:          ProxyConfig{BindHost: "127.0.0.1"},
		h:            h,
	}

	body := []byte(`{"type":"client-request","rpcId":"r1","method":"session.create","payload":{"cwd":"/wt"}}`)
	req := httptest.NewRequest(http.MethodPost, "http://127.0.0.1:39999/api/session.create", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	ph.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200: %s", rec.Code, rec.Body.String())
	}

	watch.mu.Lock()
	_, marked := watch.own["s-created"]
	watch.mu.Unlock()
	if !marked {
		t.Error("session.create response id not marked as owned")
	}
}

// TestStartProxyListenerLifecycle starts the listener and verifies
// closeFn releases the port.
func TestStartProxyListenerLifecycle(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte("up"))
	}))
	t.Cleanup(upstream.Close)
	u, _, _ := net.SplitHostPort(strings.TrimPrefix(upstream.URL, "http://"))

	h := &Handle{instanceID: "inst-1", cwd: "/wt"}
	host, port, closeFn, err := startProxyListener(h, ProxyConfig{BindHost: "127.0.0.1"}, u, portOf(t, upstream.URL), NewScopeTracker(), nil)
	if err != nil {
		t.Fatal(err)
	}
	addr := net.JoinHostPort(host, port)

	resp, err := http.Get("http://" + addr + "/")
	if err != nil {
		t.Fatalf("GET via proxy: %v", err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}

	closeFn()
	// The port must be released (idempotent close is exercised by
	// calling twice).
	closeFn()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		conn, err := net.DialTimeout("tcp", addr, 200*time.Millisecond)
		if err != nil {
			return // closed — good
		}
		conn.Close()
		time.Sleep(100 * time.Millisecond)
	}
	t.Error("proxy port still accepting after closeFn")
}

// --- upstream browser-auth relay (auth.go; proxy Director/ModifyResponse) ---

// relayUpstream is a fake dsh >=0.1.2 server: the browser-auth token
// exchange (GET /?token=tok → 303 + `Set-Cookie: dsh-auth-x=y;
// HttpOnly`) and an /api surface gated on the minted cookie (401
// without it). It records what the proxy actually forwarded so tests
// assert on the wire truth, not on internals. (auth_test.go's
// newAuthUpstream fakes the exchange alone; this one also gates /api.)
type relayUpstream struct {
	srv *httptest.Server

	exchangeHits atomic.Int32 // token-exchange invocations
	slowExchange atomic.Bool  // exchange hangs until the caller's deadline fires
	plantCookie  atomic.Bool  // plant Set-Cookie: dsh-auth-planted=zzz on every response

	mu         sync.Mutex
	lastCookie string // last Cookie header seen on /api/*
}

func newRelayUpstream(t *testing.T) *relayUpstream {
	t.Helper()
	up := &relayUpstream{}
	up.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/" && r.URL.Query().Get("token") != "" {
			up.exchangeHits.Add(1)
			if up.slowExchange.Load() {
				// A wedged upstream: hang until the caller's own
				// deadline (the Director's 3 s cap) kills the request,
				// then return fast so httptest cleanup never waits.
				<-r.Context().Done()
				return
			}
			if r.URL.Query().Get("token") != "tok" {
				w.WriteHeader(http.StatusUnauthorized)
				return
			}
			w.Header().Add("Set-Cookie", "dsh-auth-x=y; Path=/; HttpOnly")
			w.WriteHeader(http.StatusSeeOther)
			return
		}
		if strings.HasPrefix(r.URL.Path, "/api") {
			if up.plantCookie.Load() {
				// The response-side fence probe: the browser-facing
				// answer carries an upstream credential cookie that
				// must be stripped, plus a benign one that must survive.
				w.Header().Add("Set-Cookie", "dsh-auth-planted=zzz; Path=/; HttpOnly")
				w.Header().Add("Set-Cookie", "theme=light; Path=/")
			}
			up.mu.Lock()
			up.lastCookie = r.Header.Get("Cookie")
			up.mu.Unlock()
			if r.URL.Path == "/api/expire" {
				// Simulates upstream secret rotation: the relayed
				// cookie is suddenly rejected — the proxy must
				// invalidate and the NEXT request must re-mint.
				w.WriteHeader(http.StatusUnauthorized)
				return
			}
			if !strings.Contains(r.Header.Get("Cookie"), "dsh-auth-x=y") {
				w.WriteHeader(http.StatusUnauthorized)
				return
			}
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(map[string]any{"ok": true})
			return
		}
		w.WriteHeader(http.StatusNotFound)
	}))
	t.Cleanup(up.srv.Close)
	return up
}

func (up *relayUpstream) lastAPICookie() string {
	up.mu.Lock()
	defer up.mu.Unlock()
	return up.lastCookie
}

// newAuthProxyFixture wires a proxyHandler whose Handle carries the
// SINGLE shared relay — built exactly like pumpAndWatch builds it — and
// the handler reads it back through the locking accessor, the same path
// startProxyListener uses.
func newAuthProxyFixture(t *testing.T, up *relayUpstream) (*proxyHandler, *Handle) {
	t.Helper()
	host, port, err := net.SplitHostPort(authorityOf(up.srv))
	if err != nil {
		t.Fatal(err)
	}
	h := &Handle{instanceID: "inst-1", cwd: "/wt"}
	h.auth = newUpstreamAuth("tok", authorityOf(up.srv))
	ph := &proxyHandler{
		upstreamHost: host,
		upstreamPort: port,
		worktree:     "/wt",
		instanceID:   "inst-1",
		cfg:          ProxyConfig{BindHost: "127.0.0.1"},
		h:            h,
		auth:         h.upstreamAuthRelay(),
	}
	return ph, h
}

// getViaProxy drives a browser-style GET through the handler; each
// cookieHdrs entry becomes its own Cookie header line (browsers send
// one, but the filter must cope with several).
func getViaProxy(t *testing.T, ph *proxyHandler, path string, cookieHdrs ...string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodGet, "http://127.0.0.1:39999"+path, nil)
	for _, c := range cookieHdrs {
		req.Header.Add("Cookie", c)
	}
	rec := httptest.NewRecorder()
	ph.ServeHTTP(rec, req)
	return rec
}

// TestProxyAuthRelayInjectsCookie pins the happy path: a browser-style
// request carrying NO cookies gets the relay-minted dsh-auth-* pair
// injected (the upstream gate passes), and the mint is cached — the
// exchange stays off the per-request hot path.
func TestProxyAuthRelayInjectsCookie(t *testing.T) {
	up := newRelayUpstream(t)
	ph, _ := newAuthProxyFixture(t, up)

	rec := getViaProxy(t, ph, "/api/whoami")
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 (relay cookie must satisfy the upstream gate)", rec.Code)
	}
	if got := up.lastAPICookie(); got != "dsh-auth-x=y" {
		t.Errorf("upstream Cookie = %q, want exactly the relay pair %q", got, "dsh-auth-x=y")
	}
	if n := up.exchangeHits.Load(); n != 1 {
		t.Fatalf("exchange hits = %d, want 1 (first mint)", n)
	}

	// Cached: a second request must not re-mint.
	if rec := getViaProxy(t, ph, "/api/whoami"); rec.Code != http.StatusOK {
		t.Fatalf("second status = %d, want 200", rec.Code)
	}
	if n := up.exchangeHits.Load(); n != 1 {
		t.Errorf("exchange hits = %d after second request, want 1 (cached)", n)
	}
}

// TestProxyAuthRelayDropsBrowserForgedCookies pins the credential
// fence: browser-sent dsh-auth-* cookies (forged or stale) must NEVER
// reach the upstream — upstream credentials come only from the relay.
// Probes malformed/edge placements: forged cookie alone in its own
// header line, forged name mid-header with weird spacing, mw_token
// riding along, and a lookalike dsh-auth- VALUE under a benign name
// (only names are stripped).
func TestProxyAuthRelayDropsBrowserForgedCookies(t *testing.T) {
	up := newRelayUpstream(t)
	ph, _ := newAuthProxyFixture(t, up)

	rec := getViaProxy(t, ph, "/api/whoami",
		"dsh-auth-fake=1",
		"spa_pref=dark; dsh-auth-mid=2 ; mw_token=zzz",
		"pref=dsh-auth-just-a-value",
	)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	want := "spa_pref=dark; pref=dsh-auth-just-a-value; dsh-auth-x=y"
	if got := up.lastAPICookie(); got != want {
		t.Errorf("upstream Cookie = %q, want %q (forged dsh-auth-* and mw_token stripped, relay pair appended)", got, want)
	}
}

// TestProxyAuthRelayInvalidatesOnUpstream401 pins the rotation
// recovery: a 401 means the relayed cookie died (secret rotation,
// restart, expiry) — ModifyResponse must drop the cached cookie so the
// NEXT request re-mints.
func TestProxyAuthRelayInvalidatesOnUpstream401(t *testing.T) {
	up := newRelayUpstream(t)
	ph, _ := newAuthProxyFixture(t, up)

	if rec := getViaProxy(t, ph, "/api/ok"); rec.Code != http.StatusOK {
		t.Fatalf("first status = %d, want 200", rec.Code)
	}
	if n := up.exchangeHits.Load(); n != 1 {
		t.Fatalf("exchange hits = %d, want 1 (first mint)", n)
	}
	if rec := getViaProxy(t, ph, "/api/expire"); rec.Code != http.StatusUnauthorized {
		t.Fatalf("rotation status = %d, want 401", rec.Code)
	}
	if rec := getViaProxy(t, ph, "/api/ok"); rec.Code != http.StatusOK {
		t.Fatalf("post-rotation status = %d, want 200 (re-mint must recover)", rec.Code)
	}
	if n := up.exchangeHits.Load(); n != 2 {
		t.Errorf("exchange hits = %d after rotation + retry, want 2 (invalidate forced a re-mint)", n)
	}
}

// TestProxyAuthRelayLegacyPassthrough pins the legacy contract: dsh
// <0.1.2 has no browser auth, so the relay is nil (Handle built outside
// Spawn) or disabled (empty token). Requests must pass with NO Cookie
// header added, and an upstream 401 must not produce invalidate side
// effects — no exchange is ever attempted.
func TestProxyAuthRelayLegacyPassthrough(t *testing.T) {
	up := newRelayUpstream(t)
	host, port, err := net.SplitHostPort(authorityOf(up.srv))
	if err != nil {
		t.Fatal(err)
	}

	for name, auth := range map[string]*upstreamAuth{
		"nil relay":      nil,
		"disabled relay": newUpstreamAuth("", authorityOf(up.srv)),
	} {
		t.Run(name, func(t *testing.T) {
			h := &Handle{instanceID: "inst-1", cwd: "/wt", auth: auth}
			ph := &proxyHandler{
				upstreamHost: host,
				upstreamPort: port,
				worktree:     "/wt",
				instanceID:   "inst-1",
				cfg:          ProxyConfig{BindHost: "127.0.0.1"},
				h:            h,
				auth:         auth,
			}
			rec := getViaProxy(t, ph, "/api/ok")
			if rec.Code != http.StatusUnauthorized {
				t.Fatalf("status = %d, want 401 (no relay cookie minted for legacy)", rec.Code)
			}
			if got := up.lastAPICookie(); got != "" {
				t.Errorf("upstream Cookie = %q, want empty (no header added)", got)
			}
			// The 401 invalidated nothing — a legacy relay never mints.
			if n := up.exchangeHits.Load(); n != 0 {
				t.Errorf("exchange hits = %d, want 0 (legacy relay never mints)", n)
			}
		})
	}
}

// TestProxyAuthRelayStripsUpstreamSetCookie pins the response-side
// fence: an upstream-planted dsh-auth-* Set-Cookie must never reach the
// browser — the proxy is the credential holder. Benign cookies survive.
func TestProxyAuthRelayStripsUpstreamSetCookie(t *testing.T) {
	up := newRelayUpstream(t)
	up.plantCookie.Store(true)
	ph, _ := newAuthProxyFixture(t, up)

	rec := getViaProxy(t, ph, "/api/ok")
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	got := rec.Result().Header.Values("Set-Cookie")
	for _, v := range got {
		name, _, _ := strings.Cut(v, "=")
		if strings.HasPrefix(name, "dsh-auth-") {
			t.Errorf("upstream credential cookie reached the browser: %q", v)
		}
	}
	if len(got) != 1 || !strings.HasPrefix(got[0], "theme=light") {
		t.Errorf("Set-Cookie = %v, want only the benign cookie to survive", got)
	}
}

// TestProxyAuthRelaySlowExchange pins the hung-upstream bound: a wedged
// token exchange must not wedge the proxied request — the Director mint
// is capped and the request proceeds WITHOUT the cookie (the upstream
// 401 + SPA retry recovers). Healing the upstream must restore 200s.
func TestProxyAuthRelaySlowExchange(t *testing.T) {
	up := newRelayUpstream(t)
	up.slowExchange.Store(true)
	ph, _ := newAuthProxyFixture(t, up)

	start := time.Now()
	rec := getViaProxy(t, ph, "/api/ok")
	elapsed := time.Since(start)
	if elapsed > 8*time.Second {
		t.Fatalf("request took %s — the mint timeout failed to bound a wedged exchange", elapsed)
	}
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401 (forwarded without cookie)", rec.Code)
	}
	if got := up.lastAPICookie(); got != "" {
		t.Errorf("upstream Cookie = %q, want empty after mint timeout", got)
	}

	up.slowExchange.Store(false)
	if rec := getViaProxy(t, ph, "/api/ok"); rec.Code != http.StatusOK {
		t.Fatalf("post-heal status = %d, want 200 (retry recovers)", rec.Code)
	}
}
