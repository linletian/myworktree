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

func dshTestInstance(id, worktreePath, status string, blob []byte) store.ManagedInstance {
	return store.ManagedInstance{
		ID:         id,
		WorktreeID: "wt1",
		Name:       id,
		Kind:       store.KindDsh,
		Cwd:        worktreePath,
		Status:     status,
		KindBlob:   blob,
	}
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
			dshTestInstance("inst-dsh", wt, "running", raw),
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
		Instances: []store.ManagedInstance{dshTestInstance("inst-dsh", wt, "running", raw)},
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

func TestHandleInstanceDshScope(t *testing.T) {
	wt := t.TempDir()
	srv, _ := newIsolatedTestServerWithDsh(t, store.State{
		Worktrees: []store.ManagedWorktree{{ID: "wt1", Name: "wt1", Path: wt}},
		Instances: []store.ManagedInstance{dshTestInstance("inst-dsh", wt, "running", nil)},
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

	// Unknown instance → default in-scope.
	req = httptest.NewRequest(http.MethodGet, "/api/instances/dsh/scope?id=inst-other", nil)
	w = httptest.NewRecorder()
	srv.handleInstanceDshScope(w, req)
	if w.Code != http.StatusNotFound {
		t.Errorf("unknown instance status = %d, want 404", w.Code)
	}
}

func TestHandleInstanceDshLaunch(t *testing.T) {
	wt := t.TempDir()
	srv, _ := newIsolatedTestServerWithDsh(t, store.State{
		Worktrees: []store.ManagedWorktree{{ID: "wt1", Name: "wt1", Path: wt}},
		Instances: []store.ManagedInstance{dshTestInstance("inst-dsh", wt, "failed", nil)},
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
