package app

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"myworktree/internal/instance"
	"myworktree/internal/instance/reasonix"
	"myworktree/internal/store"
)

// fakeHTTPServeBin writes a python3 script that emulates `reasonix serve`
// closely enough for proxy tests: it binds 127.0.0.1:0, writes the real
// address to --port-file and its pid to --pid-file, then serves HTTP:
//
//	GET /page        → text/html (for the injection test)
//	other GET        → "path=<path>;cookie=<Cookie header>"
//	POST             → 200 {}
func fakeHTTPServeBin(t *testing.T) string {
	t.Helper()
	bin := filepath.Join(t.TempDir(), "fake-http-serve.py")
	script := `#!/usr/bin/env python3
import os, sys
from http.server import BaseHTTPRequestHandler, HTTPServer
args = sys.argv[1:]
def val(flag):
    try:
        i = args.index(flag)
        return args[i + 1]
    except (ValueError, IndexError):
        return None
pidfile = val("--pid-file")
portfile = val("--port-file")
class H(BaseHTTPRequestHandler):
    def _send(self, code, ctype, body):
        self.send_response(code)
        self.send_header("Content-Type", ctype)
        self.send_header("Content-Length", str(len(body)))
        self.end_headers()
        self.wfile.write(body)
    def do_GET(self):
        if self.path == "/page":
            self._send(200, "text/html; charset=utf-8",
                       b"<html><head><title>t</title></head><body>ok</body></html>")
        else:
            self._send(200, "text/plain",
                       ("path=" + self.path + ";cookie=" + self.headers.get("Cookie", "")).encode())
    def do_POST(self):
        n = int(self.headers.get("Content-Length", "0"))
        if n:
            self.rfile.read(n)
        self._send(200, "application/json", b"{}")
    def log_message(self, *a):
        pass
srv = HTTPServer(("127.0.0.1", 0), H)
with open(portfile, "w") as f:
    f.write("127.0.0.1:%d" % srv.server_address[1])
with open(pidfile, "w") as f:
    f.write(str(os.getpid()))
srv.serve_forever()
`
	if err := os.WriteFile(bin, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	return bin
}

func newProxyTestEnv(t *testing.T) (*reasonixProxy, *instance.Manager, store.FileStore) {
	t.Helper()
	dataDir := t.TempDir()
	workDir := t.TempDir()
	fs := store.FileStore{Path: filepath.Join(dataDir, "state.json")}
	if err := fs.Save(store.State{
		Worktrees: []store.ManagedWorktree{
			{ID: "wt1", Name: "wt1", Path: workDir},
		},
	}); err != nil {
		t.Fatalf("seed state failed: %v", err)
	}
	m := &instance.Manager{
		DataDir: dataDir,
		Store:   fs,
		Reasonix: &reasonix.Driver{
			DataDir:     dataDir,
			ReasonixBin: fakeHTTPServeBin(t),
		},
	}
	return &reasonixProxy{manager: m}, m, fs
}

// startReasonixViaManager starts a reasonix instance through the manager and
// returns its id.
func startReasonixViaManager(t *testing.T, m *instance.Manager) string {
	t.Helper()
	inst, err := m.Start(instance.StartInput{WorktreeID: "wt1", Kind: store.KindReasonix, Name: "chat"})
	if err != nil {
		t.Fatalf("Start reasonix failed: %v", err)
	}
	return inst.ID
}

func TestReasonixProxyProxiesAndInjectsCookie(t *testing.T) {
	p, m, _ := newProxyTestEnv(t)
	id := startReasonixViaManager(t, m)
	defer func() { _ = m.Stop(id) }()

	req := httptest.NewRequest(http.MethodGet, "/rx/"+id+"/history", nil)
	rec := httptest.NewRecorder()
	p.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 (body: %s)", rec.Code, rec.Body.String())
	}
	body := rec.Body.String()
	if !strings.Contains(body, "path=/history") {
		t.Fatalf("backend did not receive stripped path: %q", body)
	}
	if !strings.Contains(body, "cookie=reasonix_token=") {
		t.Fatalf("backend did not receive reasonix_token cookie: %q", body)
	}
}

func TestReasonixProxyInjectPrefixIntoHTML(t *testing.T) {
	p, m, _ := newProxyTestEnv(t)
	id := startReasonixViaManager(t, m)
	defer func() { _ = m.Stop(id) }()

	req := httptest.NewRequest(http.MethodGet, "/rx/"+id+"/page", nil)
	rec := httptest.NewRecorder()
	p.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	body := rec.Body.String()
	if !strings.Contains(body, "<script>") {
		t.Fatalf("no injection script in html response: %q", body)
	}
	if !strings.Contains(body, "/rx/"+id) {
		t.Fatalf("injection script missing mount prefix /rx/%s: %q", id, body)
	}
	if !strings.Contains(body, "EventSource") || !strings.Contains(body, "window.fetch") {
		t.Fatalf("injection script missing fetch/EventSource rewrite: %q", body)
	}
}

func TestReasonixProxyUnknownInstance(t *testing.T) {
	p, m, _ := newProxyTestEnv(t)
	_ = m
	req := httptest.NewRequest(http.MethodGet, "/rx/doesnotexist/history", nil)
	rec := httptest.NewRecorder()
	p.ServeHTTP(rec, req)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404", rec.Code)
	}
}

func TestReasonixProxyStoppedInstance(t *testing.T) {
	p, m, _ := newProxyTestEnv(t)
	id := startReasonixViaManager(t, m)
	if err := m.Stop(id); err != nil {
		t.Fatalf("Stop failed: %v", err)
	}
	req := httptest.NewRequest(http.MethodGet, "/rx/"+id+"/history", nil)
	rec := httptest.NewRecorder()
	p.ServeHTTP(rec, req)
	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want 503", rec.Code)
	}
}

func TestSplitReasonixPath(t *testing.T) {
	cases := []struct {
		path, id, rest string
	}{
		{"/rx/abc123/history", "abc123", "history"},
		{"/rx/abc123/", "abc123", ""},
		{"/rx/abc123", "abc123", ""},
		{"/api/instances", "", ""},
		{"", "", ""},
	}
	for _, c := range cases {
		id, rest := splitReasonixPath(c.path)
		if id != c.id || rest != c.rest {
			t.Fatalf("splitReasonixPath(%q) = (%q, %q), want (%q, %q)", c.path, id, rest, c.id, c.rest)
		}
	}
}
