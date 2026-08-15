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
	fail     bool // respond 500 (network-ish failure → retry)
	okAfter  int  // number of failures before succeeding
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

func TestCreateWorkspaceAdoptsAndParsesID(t *testing.T) {
	d, _, base := newBootstrapDriver(t)
	wsID, ok := d.createWorkspace(context.Background(), base, "/wt")
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
	wsID, ok := d.createWorkspace(context.Background(), base, "/wt")
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
	wsID, ok := d.createWorkspace(context.Background(), base, "/wt")
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
