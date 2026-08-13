package opencode_web

import (
	"encoding/json"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"path/filepath"
	"strings"
	"testing"

	"myworktree/internal/framework"
	"myworktree/internal/store"
)

// TestProxyHandlerScopeAndInject exercises the full proxy path: full-page
// HTML injection (base tag + asset rewrite + CSP hash) and the out-of-scope
// directory classification recorded in the ScopeTracker.
func TestProxyHandlerScopeAndInject(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.Contains(r.URL.Path, "/session") {
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"sessions":[]}`))
			return
		}
		w.Header().Set("Content-Type", "text/html")
		w.Header().Set("Content-Security-Policy", "default-src 'self'; script-src 'self' 'wasm-unsafe-eval'")
		_, _ = w.Write([]byte(`<html><head><script src="/assets/x.js"></script></head><body></body></html>`))
	}))
	defer upstream.Close()

	u, _ := url.Parse(upstream.URL)
	host, port, _ := net.SplitHostPort(u.Host)

	wt := t.TempDir()
	other := t.TempDir()
	dir := t.TempDir()
	fs := store.FileStore{Path: filepath.Join(dir, "state.json")}
	blob, _ := json.Marshal(Blob{Host: host, Port: port, WorktreeAbs: wt})
	if err := fs.Save(store.State{
		Instances: []store.ManagedInstance{{ID: "inst1", Kind: "opencode-web", Status: "running", KindBlob: blob}},
	}); err != nil {
		t.Fatal(err)
	}

	mgr := framework.NewManager(framework.NewRegistry(), fs, nil)
	tracker := NewScopeTracker()
	// app.go mounts the handler via http.StripPrefix("/__opencode", …).
	handler := http.StripPrefix("/__opencode", ProxyHandler(mgr, "tok", tracker))

	// Full-page navigation: base tag + asset rewrite + CSP hash.
	req := httptest.NewRequest(http.MethodGet, "http://x/__opencode/inst1/", nil)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	body := rec.Body.String()
	if !strings.Contains(body, `<base href="/__opencode/inst1/">`) {
		t.Fatalf("missing base tag: %s", body)
	}
	if !strings.Contains(body, `src="/__opencode/inst1/assets/x.js"`) {
		t.Fatalf("asset not rewritten through proxy: %s", body)
	}
	if !strings.Contains(rec.Header().Get("Content-Security-Policy"), "'sha256-") {
		t.Fatal("CSP hash not injected")
	}

	// In-scope request: directory == worktree → in-scope.
	req = httptest.NewRequest(http.MethodGet, "http://x/__opencode/inst1/session?directory="+url.QueryEscape(wt), nil)
	rec = httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if st, _ := tracker.Get("inst1"); st.Scope != ScopeInScope {
		t.Fatalf("in-scope request recorded scope=%q, want in-scope", st.Scope)
	}

	// Out-of-scope request: directory == other → out-of-scope, recorded.
	req = httptest.NewRequest(http.MethodGet, "http://x/__opencode/inst1/session?directory="+url.QueryEscape(other), nil)
	rec = httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	st, ok := tracker.Get("inst1")
	if !ok || st.Scope != ScopeOutOfScope || st.Directory == "" {
		t.Fatalf("out-of-scope request recorded (%+v, %v), want out-of-scope with directory", st, ok)
	}

	// Directory-less static asset must NOT clobber the out-of-scope state.
	req = httptest.NewRequest(http.MethodGet, "http://x/__opencode/inst1/assets/y.js", nil)
	rec = httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if st, _ := tracker.Get("inst1"); st.Scope != ScopeOutOfScope {
		t.Fatalf("static asset clobbered state: scope=%q, want out-of-scope preserved", st.Scope)
	}
}
