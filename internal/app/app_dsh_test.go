package app

import (
	"bytes"
	"encoding/json"
	"log"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"myworktree/internal/framework"
	"myworktree/internal/instance/dsh_web"
	"myworktree/internal/instance/opencode_web"
	"myworktree/internal/instance/pty"
	"myworktree/internal/instance/reasonix"
	"myworktree/internal/store"
)

// newIsolatedTestServerWithDsh is newIsolatedTestServer plus the
// dsh-web driver / tracker wiring (mirrors app.New).
func newIsolatedTestServerWithDsh(t *testing.T, st store.State) (*Server, store.FileStore) {
	t.Helper()
	dataDir := t.TempDir()
	fs := store.FileStore{Path: filepath.Join(dataDir, "state.json")}
	if err := fs.Save(st); err != nil {
		t.Fatal(err)
	}
	reg := framework.NewRegistry()
	reg.Register(pty.Driver{})
	reg.Register(opencode_web.Driver{})
	rxDriver := &reasonix.Driver{DataDir: dataDir}
	reg.Register(reasonix.NewKind(rxDriver))
	dshDrv := &dsh_web.Driver{DataDir: dataDir}
	dshScope := dsh_web.NewScopeTracker()
	dshDrv.Tracker = dshScope
	dshDrv.Proxy = dshProxyConfig(Config{ListenAddr: "127.0.0.1:0"}, false)
	dshDrv.ProxyStarter = dshDrv.ProxyStarterFn()
	reg.Register(dshDrv)
	m := framework.NewManager(reg, fs, log.New(os.Stderr, "", 0))
	m.DataDir = dataDir
	m.Root = dataDir
	return &Server{
		cfg:         Config{ListenAddr: "127.0.0.1:0"},
		logger:      log.New(os.Stderr, "", 0),
		dataDir:     dataDir,
		root:        dataDir,
		store:       fs,
		instanceMgr: m,
		rxDriver:    rxDriver,
		dshDrv:      dshDrv,
		dshScope:    dshScope,
		mux:         http.NewServeMux(),
		authFails:   map[string]authFail{},
	}, fs
}

func TestHandleInstanceDshInfo(t *testing.T) {
	wt := t.TempDir()
	blob := dsh_web.Blob{
		Host: "127.0.0.1", Port: "40001",
		ProxyHost: "127.0.0.1", ProxyPort: "40002",
		IframeURL:        "http://127.0.0.1:40002/",
		WorktreeAbs:      wt,
		Version:          "0.1.0",
		VersionSupported: true,
		OverlayVerified:  true,
	}
	raw, _ := json.Marshal(blob)
	srv, _ := newIsolatedTestServerWithDsh(t, store.State{
		Worktrees: []store.ManagedWorktree{{ID: "wt1", Name: "wt1", Path: wt}},
		Instances: []store.ManagedInstance{
			managedTestInstance("inst-dsh", store.KindDsh, wt, "running", raw),
			{ID: "inst-oc", WorktreeID: "wt1", Name: "oc", Kind: store.KindOpenCodeWeb, Cwd: wt, Status: "running"},
		},
	})

	// Non-dsh instance → 404.
	req := httptest.NewRequest(http.MethodGet, "/api/instances/dsh?id=inst-oc", nil)
	w := httptest.NewRecorder()
	srv.handleInstanceDshInfo(w, req)
	if w.Code != http.StatusNotFound {
		t.Fatalf("non-dsh instance status = %d, want 404", w.Code)
	}

	// dsh instance → full metadata; local loopback host is kept as-is.
	req = httptest.NewRequest(http.MethodGet, "/api/instances/dsh?id=inst-dsh", nil)
	req.Host = "127.0.0.1:0"
	w = httptest.NewRecorder()
	srv.handleInstanceDshInfo(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200: %s", w.Code, w.Body.String())
	}
	var resp map[string]any
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatal(err)
	}
	if resp["iframe_src"] != "http://127.0.0.1:40002/" {
		t.Errorf("iframe_src = %v", resp["iframe_src"])
	}
	if resp["proxy_host"] != "127.0.0.1" || resp["proxy_port"] != "40002" {
		t.Errorf("proxy = %v:%v", resp["proxy_host"], resp["proxy_port"])
	}
	if resp["version"] != "0.1.0" || resp["version_supported"] != true || resp["overlay_verified"] != true {
		t.Errorf("advisory fields = %v %v %v", resp["version"], resp["version_supported"], resp["overlay_verified"])
	}
	if _, has := resp["missing_dsh"]; has {
		t.Error("missing_dsh present for a running instance")
	}
}

func TestHandleInstanceDshInfoRemoteHostMapping(t *testing.T) {
	// A proxy bound to 0.0.0.0 must be reported through the caller's
	// host (the browser cannot reach 0.0.0.0).
	wt := t.TempDir()
	raw, _ := json.Marshal(dsh_web.Blob{
		ProxyHost: "0.0.0.0", ProxyPort: "40002",
		IframeURL: "http://0.0.0.0:40002/",
	})
	srv, _ := newIsolatedTestServerWithDsh(t, store.State{
		Worktrees: []store.ManagedWorktree{{ID: "wt1", Name: "wt1", Path: wt}},
		Instances: []store.ManagedInstance{managedTestInstance("inst-dsh", store.KindDsh, wt, "running", raw)},
	})
	req := httptest.NewRequest(http.MethodGet, "/api/instances/dsh?id=inst-dsh", nil)
	req.Host = "192.168.1.5:50099"
	w := httptest.NewRecorder()
	srv.handleInstanceDshInfo(w, req)
	var resp map[string]any
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatal(err)
	}
	if resp["iframe_src"] != "http://192.168.1.5:40002/" {
		t.Errorf("remote iframe_src = %v", resp["iframe_src"])
	}
}

func TestHandleInstanceDshInfoHostValidation(t *testing.T) {
	// The r.Host-derived iframe host is client-controlled: anything
	// that is not a plain DNS name / IP must yield no iframe_src
	// (REVIEW-2026-08-15.md #5).
	wt := t.TempDir()
	raw, _ := json.Marshal(dsh_web.Blob{
		ProxyHost: "0.0.0.0", ProxyPort: "40002",
		IframeURL: "http://0.0.0.0:40002/",
	})
	srv, _ := newIsolatedTestServerWithDsh(t, store.State{
		Worktrees: []store.ManagedWorktree{{ID: "wt1", Name: "wt1", Path: wt}},
		Instances: []store.ManagedInstance{managedTestInstance("inst-dsh", store.KindDsh, wt, "running", raw)},
	})

	cases := []struct {
		name    string
		host    string
		wantSrc string
	}{
		{"plain hostname without port", "myhost.local", "http://myhost.local:40002/"},
		{"IPv4 with port", "10.0.0.7:50099", "http://10.0.0.7:40002/"},
		{"path smuggling rejected", "evil.com/path", ""},
		{"scheme smuggling rejected", "https://evil.com", ""},
		{"userinfo rejected", "user@evil.com", ""},
		{"empty rejected", "", ""},
	}
	for _, c := range cases {
		req := httptest.NewRequest(http.MethodGet, "/api/instances/dsh?id=inst-dsh", nil)
		req.Host = c.host
		w := httptest.NewRecorder()
		srv.handleInstanceDshInfo(w, req)
		var resp map[string]any
		if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
			t.Fatalf("%s: invalid JSON: %v", c.name, err)
		}
		src, _ := resp["iframe_src"].(string)
		if src != c.wantSrc {
			t.Errorf("%s: iframe_src = %q, want %q", c.name, src, c.wantSrc)
		}
	}
}

func TestHandleInstanceDshScope(t *testing.T) {
	wt := t.TempDir()
	srv, _ := newIsolatedTestServerWithDsh(t, store.State{
		Worktrees: []store.ManagedWorktree{{ID: "wt1", Name: "wt1", Path: wt}},
		Instances: []store.ManagedInstance{managedTestInstance("inst-dsh", store.KindDsh, wt, "running", nil)},
	})
	srv.dshScope.Record("inst-dsh", dsh_web.ScopeState{
		Scope: dsh_web.ScopeOutOfScope, Directory: "/other",
	})

	req := httptest.NewRequest(http.MethodGet, "/api/instances/dsh/scope?id=inst-dsh", nil)
	w := httptest.NewRecorder()
	srv.handleInstanceDshScope(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d", w.Code)
	}
	var resp dsh_web.ScopeState
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatal(err)
	}
	if resp.Scope != dsh_web.ScopeOutOfScope || resp.Directory != "/other" {
		t.Errorf("scope = %+v", resp)
	}
	if len(resp.ForeignActiveSessions) != 0 {
		t.Errorf("foreign_active_sessions = %v, want empty without a watch", resp.ForeignActiveSessions)
	}

	// Unknown instance → default in-scope.
	req = httptest.NewRequest(http.MethodGet, "/api/instances/dsh/scope?id=inst-other", nil)
	w = httptest.NewRecorder()
	srv.handleInstanceDshScope(w, req)
	if w.Code != http.StatusNotFound {
		t.Errorf("unknown instance status = %d, want 404", w.Code)
	}
}

func TestHandleInstanceDshScopeForeignActiveSessions(t *testing.T) {
	wt := t.TempDir()
	srv, _ := newIsolatedTestServerWithDsh(t, store.State{
		Worktrees: []store.ManagedWorktree{{ID: "wt1", Name: "wt1", Path: wt}},
		Instances: []store.ManagedInstance{managedTestInstance("inst-dsh", store.KindDsh, wt, "running", nil)},
	})

	// A watch with a freshly-written session log (foreign: no own
	// attribution) must surface in the scope response.
	sessRoot := t.TempDir()
	dir := filepath.Join(sessRoot, "--wt--", "session-x")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "session.jsonl.zstd"), []byte("fake"), 0o600); err != nil {
		t.Fatal(err)
	}
	watch := dsh_web.NewSessionWatch(sessRoot, nil)
	if err := watch.ScanNow(); err != nil {
		t.Fatal(err)
	}
	srv.dshWatch = watch

	req := httptest.NewRequest(http.MethodGet, "/api/instances/dsh/scope?id=inst-dsh", nil)
	w := httptest.NewRecorder()
	srv.handleInstanceDshScope(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d", w.Code)
	}
	var resp dsh_web.ScopeState
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatal(err)
	}
	if len(resp.ForeignActiveSessions) != 1 || resp.ForeignActiveSessions[0] != "session-x" {
		t.Errorf("foreign_active_sessions = %v, want [session-x]", resp.ForeignActiveSessions)
	}
}

func TestHandleInstanceDshLaunch(t *testing.T) {
	wt := t.TempDir()
	srv, _ := newIsolatedTestServerWithDsh(t, store.State{
		Worktrees: []store.ManagedWorktree{{ID: "wt1", Name: "wt1", Path: wt}},
		Instances: []store.ManagedInstance{managedTestInstance("inst-dsh", store.KindDsh, wt, "failed", nil)},
	})

	post := func(body string) *httptest.ResponseRecorder {
		req := httptest.NewRequest(http.MethodPost, "/api/instances/dsh/launch", bytes.NewReader([]byte(body)))
		req.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()
		srv.handleInstanceDshLaunch(w, req)
		return w
	}

	if w := post(`{"id":"inst-dsh","mode":"npx"}`); w.Code != http.StatusOK {
		t.Fatalf("npx mode status = %d: %s", w.Code, w.Body.String())
	}
	if w := post(`{"id":"inst-dsh","mode":"bogus"}`); w.Code != http.StatusBadRequest {
		t.Fatalf("bogus mode status = %d, want 400", w.Code)
	}
	if w := post(`{"id":"inst-oc","mode":"npx"}`); w.Code != http.StatusNotFound {
		t.Fatalf("non-dsh instance status = %d, want 404", w.Code)
	}
}

func TestHandleInstanceDshInstallKindGuard(t *testing.T) {
	wt := t.TempDir()
	srv, _ := newIsolatedTestServerWithDsh(t, store.State{
		Worktrees: []store.ManagedWorktree{{ID: "wt1", Name: "wt1", Path: wt}},
		Instances: []store.ManagedInstance{
			{ID: "inst-oc", WorktreeID: "wt1", Name: "oc", Kind: store.KindOpenCodeWeb, Cwd: wt, Status: "running"},
		},
	})
	// The install handler must 404 before touching npm for non-dsh
	// instances (the real npm run is exercised manually — it mutates the
	// global npm prefix).
	req := httptest.NewRequest(http.MethodPost, "/api/instances/dsh/install", bytes.NewReader([]byte(`{"id":"inst-oc"}`)))
	w := httptest.NewRecorder()
	srv.handleInstanceDshInstall(w, req)
	if w.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404", w.Code)
	}
}
