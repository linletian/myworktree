package app

import (
	"bytes"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
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
			{ID: "inst-oc", WorktreeID: "wt1", Name: "oc", Kind: "opencode-web", Cwd: wt, Status: "running"},
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

func TestHandleInstanceDshInfoTokenizesRemoteSrc(t *testing.T) {
	// Remote mode (token gate on): the handler must append ?token= to
	// iframe_src ITSELF. The mw_token cookie is HttpOnly — page JS can
	// never read it — and portal/login flows carry no ?token= in the
	// address bar, so the frontend has no way to tokenize the
	// cross-origin iframe URL (the embed would 401). The server owns
	// both facts (gate on + token value) and this handler sits behind
	// withAuth, so only authenticated clients receive it.
	wt := t.TempDir()
	raw, _ := json.Marshal(dsh_web.Blob{
		ProxyHost: "0.0.0.0", ProxyPort: "40002",
		IframeURL: "http://0.0.0.0:40002/",
	})
	srv, _ := newIsolatedTestServerWithDsh(t, store.State{
		Worktrees: []store.ManagedWorktree{{ID: "wt1", Name: "wt1", Path: wt}},
		Instances: []store.ManagedInstance{managedTestInstance("inst-dsh", store.KindDsh, wt, "running", raw)},
	})
	// The handler must read the PROXY's own token (that is what
	// checkToken validates against). Give cfg a DIFFERENT value so the
	// assertion pins the source: if the handler ever falls back to
	// cfg.AuthToken, this test fails.
	srv.dshDrv.Proxy = dsh_web.ProxyConfig{RequireToken: true, AuthToken: "sekret-proxy"}
	srv.cfg = Config{ListenAddr: "0.0.0.0:0", AuthToken: "sekret-cfg"}

	req := httptest.NewRequest(http.MethodGet, "/api/instances/dsh?id=inst-dsh", nil)
	req.Host = "192.168.1.5:50099"
	w := httptest.NewRecorder()
	srv.handleInstanceDshInfo(w, req)
	var resp map[string]any
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatal(err)
	}
	want := "http://192.168.1.5:40002/?token=sekret-proxy"
	if resp["iframe_src"] != want {
		t.Errorf("iframe_src = %v, want %q", resp["iframe_src"], want)
	}
	if strings.Contains(fmt.Sprint(resp["iframe_src"]), "sekret-cfg") {
		t.Error("iframe_src carries cfg.AuthToken; the handler must use Proxy.AuthToken")
	}

	// Gate off (local loopback bind): the iframe must stay token-free
	// (src rebuilt from the caller's host, still without a token).
	srv.dshDrv.Proxy = dsh_web.ProxyConfig{RequireToken: false}
	req = httptest.NewRequest(http.MethodGet, "/api/instances/dsh?id=inst-dsh", nil)
	req.Host = "127.0.0.1:50099"
	w = httptest.NewRecorder()
	srv.handleInstanceDshInfo(w, req)
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatal(err)
	}
	if src, _ := resp["iframe_src"].(string); src != "http://127.0.0.1:40002/" || strings.Contains(src, "token=") {
		t.Errorf("local iframe_src = %q, want token-free", src)
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

// TestDshWriteOriginGuard pins the CSRF guard (REVIEW-2026-08-16
// MED-6): withAuth skips Origin checks for loopback clients, so the
// install/launch handlers must reject cross-origin browser writes
// themselves — a hostile webpage can otherwise drive a no-cors
// loopback fetch. Requests WITHOUT Origin (curl/CLI) stay allowed.
// The body always names a NON-EXISTENT instance so guard-passing
// requests stop at requireDshInstance (404) and never reach npm.
func TestDshWriteOriginGuard(t *testing.T) {
	wt := t.TempDir()
	srv, _ := newIsolatedTestServerWithDsh(t, store.State{
		Worktrees: []store.ManagedWorktree{{ID: "wt1", Name: "wt1", Path: wt}},
	})

	post := func(origin string) *httptest.ResponseRecorder {
		req := httptest.NewRequest(http.MethodPost, "/api/instances/dsh/install", bytes.NewReader([]byte(`{"id":"inst-nonexistent"}`)))
		req.RemoteAddr = "127.0.0.1:53123" // loopback trust zone
		req.Host = "127.0.0.1:39999"       // same-origin host the browser would use
		if origin != "" {
			req.Header.Set("Origin", origin)
		}
		w := httptest.NewRecorder()
		srv.handleInstanceDshInstall(w, req)
		return w
	}

	// Cross-origin browser request → 403 before any instance/npm work.
	if w := post("https://evil.example"); w.Code != http.StatusForbidden {
		t.Fatalf("cross-origin status = %d, want 403", w.Code)
	}
	// Same-origin browser request → guard passes, then 404 (unknown
	// instance; proves the guard did not block it).
	if w := post("http://127.0.0.1:39999"); w.Code != http.StatusNotFound {
		t.Fatalf("same-origin status = %d, want 404 (guard passed)", w.Code)
	}
	// No Origin (curl) → guard passes, then 404.
	if w := post(""); w.Code != http.StatusNotFound {
		t.Fatalf("origin-less status = %d, want 404 (guard passed)", w.Code)
	}
}

func TestHandleInstanceDshInstallKindGuard(t *testing.T) {
	wt := t.TempDir()
	srv, _ := newIsolatedTestServerWithDsh(t, store.State{
		Worktrees: []store.ManagedWorktree{{ID: "wt1", Name: "wt1", Path: wt}},
		Instances: []store.ManagedInstance{
			{ID: "inst-oc", WorktreeID: "wt1", Name: "oc", Kind: "opencode-web", Cwd: wt, Status: "running"},
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
