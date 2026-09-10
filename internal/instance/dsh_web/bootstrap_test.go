package dsh_web

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"myworktree/internal/framework"
)

// rpcUpstream is a configurable RPC-envelope upstream for bootstrap
// tests.
type rpcUpstream struct {
	mu       sync.Mutex
	calls    []string
	paths    []string
	cookies  []string // Cookie header seen on each request
	fail     bool     // respond 500 (network-ish failure → retry)
	okAfter  int      // number of failures before succeeding
	failures int
}

func (u *rpcUpstream) handler(w http.ResponseWriter, r *http.Request) {
	var env struct {
		Type   string `json:"type"`
		RPCID  string `json:"rpcId"`
		Method string `json:"method"`
	}
	_ = json.NewDecoder(r.Body).Decode(&env)
	u.mu.Lock()
	u.calls = append(u.calls, env.Method)
	u.paths = append(u.paths, r.URL.Path)
	u.cookies = append(u.cookies, r.Header.Get("Cookie"))
	fail := u.fail
	if !fail && u.failures < u.okAfter {
		fail = true
	}
	if fail {
		u.failures++
		u.mu.Unlock()
		w.WriteHeader(http.StatusInternalServerError)
		return
	}
	u.mu.Unlock()

	var value any
	switch env.Method {
	case "workspace.create":
		value = map[string]any{
			"workspace": map[string]any{"workspaceId": "ws-1", "path": "/wt", "title": "/wt"},
			"created":   true,
		}
	case "session.create":
		value = map[string]any{"session": map[string]any{"id": "s-1"}}
	default:
		w.WriteHeader(http.StatusBadRequest)
		return
	}
	resp := map[string]any{
		"type":   "server-response",
		"rpcId":  env.RPCID,
		"result": map[string]any{"ok": true, "value": value},
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(resp)
}

func newBootstrapDriver(t *testing.T) (*Driver, *rpcUpstream, string) {
	t.Helper()
	up := &rpcUpstream{}
	srv := httptest.NewServer(http.HandlerFunc(up.handler))
	t.Cleanup(srv.Close)
	base := strings.TrimPrefix(srv.URL, "http://")
	host, port, _ := splitHostPort(base)
	return &Driver{DataDir: t.TempDir()}, up, "http://" + host + ":" + port
}

func splitHostPort(addr string) (string, string, error) {
	idx := strings.LastIndex(addr, ":")
	return addr[:idx], addr[idx+1:], nil
}

// bootstrapHandle builds the minimal Handle the bootstrap RPC path
// needs: the shared upstream auth relay (nil = legacy). Tests assign
// h.auth directly — safe per the write-once discipline in driver.go.
func bootstrapHandle(auth *upstreamAuth) *Handle {
	h := &Handle{}
	h.auth = auth
	return h
}

func TestCreateWorkspaceAdoptsAndParsesID(t *testing.T) {
	d, _, base := newBootstrapDriver(t)
	wsID, ok := d.createWorkspace(context.Background(), bootstrapHandle(nil), base, "/wt")
	if !ok {
		t.Fatal("createWorkspace failed")
	}
	if wsID != "ws-1" {
		t.Errorf("workspaceId = %q, want ws-1", wsID)
	}
}

func TestCreateWorkspaceRetriesTransientFailures(t *testing.T) {
	d, up, base := newBootstrapDriver(t)
	up.okAfter = 2 // fail twice, then succeed
	wsID, ok := d.createWorkspace(context.Background(), bootstrapHandle(nil), base, "/wt")
	if !ok {
		t.Fatal("createWorkspace failed after retries")
	}
	if wsID != "ws-1" {
		t.Errorf("workspaceId = %q, want ws-1", wsID)
	}
	up.mu.Lock()
	calls := len(up.calls)
	paths := append([]string{}, up.paths...)
	up.mu.Unlock()
	if calls != 3 {
		t.Errorf("attempts = %d, want 3 (2 failures + 1 success)", calls)
	}
	// Wire contract: the RPC goes to /api/<method> (the endpoint is
	// derived from the URL path, not the body).
	for _, p := range paths {
		if p != "/api/workspace.create" {
			t.Errorf("request path = %q, want /api/workspace.create", p)
		}
	}
}

func TestCreateWorkspaceGivesUpWarnOnly(t *testing.T) {
	d, up, base := newBootstrapDriver(t)
	up.fail = true
	start := time.Now()
	wsID, ok := d.createWorkspace(context.Background(), bootstrapHandle(nil), base, "/wt")
	if ok {
		t.Error("createWorkspace succeeded against a failing upstream")
	}
	if wsID != "" {
		t.Errorf("workspaceId = %q, want empty", wsID)
	}
	if elapsed := time.Since(start); elapsed > 12*time.Second {
		t.Errorf("give-up took %v, want ~3 attempts with 2s backoff", elapsed)
	}
}

func TestBootstrapUpdatesBlobWorkspaceID(t *testing.T) {
	d, _, base := newBootstrapDriver(t)
	host, port, _ := splitHostPort(strings.TrimPrefix(base, "http://"))
	h := &Handle{
		instanceID: "inst-1",
		cwd:        "/wt",
		ready:      newReadySignalClosed(),
		blob:       Blob{WorktreeAbs: "/wt"},
	}
	h.host, h.port = host, port
	d.bootstrap(context.Background(), h)
	h.mu.Lock()
	wsID := h.blob.WorkspaceID
	h.mu.Unlock()
	if wsID != "ws-1" {
		t.Errorf("blob WorkspaceID = %q, want ws-1", wsID)
	}
}

func newReadySignalClosed() *framework.ReadySignal {
	rs := framework.NewReadySignal()
	rs.Close()
	return rs
}

// gatedAuthUpstream fakes a browser-auth-gated dsh upstream: the token
// exchange GET /?token=<tok> answers 303 + Set-Cookie, and RPC posts
// without the minted Cookie header get 401.
type gatedAuthUpstream struct {
	mu        sync.Mutex
	exchanges int      // token-exchange requests seen
	rpcCalls  int      // RPC posts seen
	rpcCookie []string // Cookie header per RPC attempt
}

const (
	gateToken      = "tok"
	gateCookiePair = "dsh-auth-x=y"
)

func (u *gatedAuthUpstream) handler(w http.ResponseWriter, r *http.Request) {
	if r.Method == http.MethodGet && r.URL.Path == "/" {
		u.mu.Lock()
		u.exchanges++
		u.mu.Unlock()
		if r.URL.Query().Get("token") != gateToken {
			w.WriteHeader(http.StatusSeeOther) // no cookie → mint error
			return
		}
		w.Header().Set("Set-Cookie", gateCookiePair+"; HttpOnly")
		w.WriteHeader(http.StatusSeeOther)
		return
	}
	var env struct {
		RPCID  string `json:"rpcId"`
		Method string `json:"method"`
	}
	_ = json.NewDecoder(r.Body).Decode(&env)
	u.mu.Lock()
	u.rpcCalls++
	u.rpcCookie = append(u.rpcCookie, r.Header.Get("Cookie"))
	u.mu.Unlock()
	if r.Header.Get("Cookie") != gateCookiePair {
		w.WriteHeader(http.StatusUnauthorized)
		return
	}
	resp := map[string]any{
		"type":  "server-response",
		"rpcId": env.RPCID,
		"result": map[string]any{"ok": true, "value": map[string]any{
			"workspace": map[string]any{"workspaceId": "ws-1", "path": "/wt", "title": "/wt"},
			"created":   true,
		}},
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(resp)
}

func newGatedAuthUpstream(t *testing.T) (*gatedAuthUpstream, *httptest.Server) {
	t.Helper()
	up := &gatedAuthUpstream{}
	srv := httptest.NewServer(http.HandlerFunc(up.handler))
	t.Cleanup(srv.Close)
	return up, srv
}

func (u *gatedAuthUpstream) stats() (exchanges, rpcCalls int, rpcCookies []string) {
	u.mu.Lock()
	defer u.mu.Unlock()
	return u.exchanges, u.rpcCalls, append([]string{}, u.rpcCookie...)
}

// The bootstrap RPC path relays the minted dsh-auth-* cookie through
// the SHARED upstreamAuth on the Handle (the proxy's instance).
func TestCreateWorkspaceRelaysAuthCookie(t *testing.T) {
	up, srv := newGatedAuthUpstream(t)
	d := &Driver{DataDir: t.TempDir()}
	h := bootstrapHandle(newUpstreamAuth(gateToken, authorityOf(srv)))
	base := srv.URL

	wsID, ok := d.createWorkspace(context.Background(), h, base, "/wt")
	if !ok {
		t.Fatal("createWorkspace failed against auth-gated upstream")
	}
	if wsID != "ws-1" {
		t.Errorf("workspaceId = %q, want ws-1", wsID)
	}
	exchanges, rpcCalls, rpcCookies := up.stats()
	if exchanges != 1 {
		t.Errorf("token exchanges = %d, want 1 (single mint, cached)", exchanges)
	}
	if rpcCalls != 1 {
		t.Errorf("rpc attempts = %d, want 1", rpcCalls)
	}
	for i, c := range rpcCookies {
		if c != gateCookiePair {
			t.Errorf("attempt %d Cookie = %q, want %q", i, c, gateCookiePair)
		}
	}
	// Shared-instance cache: a second Cookie call is served from the
	// relay's cache without another exchange.
	if _, err := h.auth.Cookie(context.Background()); err != nil {
		t.Fatalf("Cookie after bootstrap: %v", err)
	}
	if exchanges, _, _ := up.stats(); exchanges != 1 {
		t.Errorf("token exchanges after cached Cookie = %d, want 1", exchanges)
	}
}

// A 401 to the relayed cookie invalidates the shared relay's cache; the
// retry loop re-mints and succeeds.
func TestCreateWorkspaceRemintsAfter401(t *testing.T) {
	up, srv := newGatedAuthUpstream(t)
	d := &Driver{DataDir: t.TempDir()}
	h := bootstrapHandle(newUpstreamAuth(gateToken, authorityOf(srv)))
	// Pre-seed a stale cached cookie: attempt 1 relays it, gets 401.
	h.auth.mu.Lock()
	h.auth.cookie = "dsh-auth-x=stale"
	h.auth.mintedAt = time.Now()
	h.auth.mu.Unlock()

	wsID, ok := d.createWorkspace(context.Background(), h, srv.URL, "/wt")
	if !ok {
		t.Fatal("createWorkspace failed after 401 re-mint")
	}
	if wsID != "ws-1" {
		t.Errorf("workspaceId = %q, want ws-1", wsID)
	}
	exchanges, rpcCalls, rpcCookies := up.stats()
	if exchanges != 1 {
		t.Errorf("token exchanges = %d, want 1 (re-mint after Invalidate)", exchanges)
	}
	if rpcCalls != 2 {
		t.Errorf("rpc attempts = %d, want 2 (401 then success)", rpcCalls)
	}
	if len(rpcCookies) != 2 || rpcCookies[0] != "dsh-auth-x=stale" || rpcCookies[1] != gateCookiePair {
		t.Errorf("rpc cookies = %v, want [dsh-auth-x=stale %s]", rpcCookies, gateCookiePair)
	}
}

// Legacy upstream (no auth gate, relay disabled via empty token): the
// wire stays byte-identical — no Cookie header is ever sent.
func TestCreateWorkspaceLegacyUpstreamSendsNoCookie(t *testing.T) {
	d, up, base := newBootstrapDriver(t)
	h := bootstrapHandle(newUpstreamAuth("", strings.TrimPrefix(base, "http://")))

	wsID, ok := d.createWorkspace(context.Background(), h, base, "/wt")
	if !ok {
		t.Fatal("createWorkspace failed against legacy upstream")
	}
	if wsID != "ws-1" {
		t.Errorf("workspaceId = %q, want ws-1", wsID)
	}
	up.mu.Lock()
	cookies := append([]string{}, up.cookies...)
	up.mu.Unlock()
	if len(cookies) == 0 {
		t.Fatal("upstream saw no requests")
	}
	for i, c := range cookies {
		if c != "" {
			t.Errorf("attempt %d Cookie = %q, want empty (legacy path sends none)", i, c)
		}
	}
}
