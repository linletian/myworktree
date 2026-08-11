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
# Avoid DNS reverse-lookup (socket.getfqdn) hanging on CI hosts with broken
# DNS (e.g. GitHub macOS runners): HTTPServer.server_bind calls getfqdn.
import socket
socket.getfqdn = lambda host="": host if host else "localhost"
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
                       b"<html><head><title>t</title></head><body><img src=\"/assets/logo.svg\"><a href=\"/sessions/x\">s</a></body></html>")
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
	// The initial-page assets must be rewritten server-side (not by a
	// client-side DOM pass, which would be too late): the img/a attributes
	// from the upstream HTML must carry the mount prefix.
	if !strings.Contains(body, "src=\"/rx/"+id+"/assets/logo.svg\"") {
		t.Fatalf("server-side attribute rewrite missing: %q", body)
	}
	if !strings.Contains(body, "href=\"/rx/"+id+"/sessions/x\"") {
		t.Fatalf("server-side href rewrite missing: %q", body)
	}
	// The injected script keeps a MutationObserver fallback for dynamically
	// inserted nodes (chat message images), and the network-layer shims.
	if !strings.Contains(body, "MutationObserver") {
		t.Fatalf("injection script missing MutationObserver fallback: %q", body)
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

func TestRewriteRootAttrs(t *testing.T) {
	const mount = "/rx/abc123"
	cases := []struct {
		name, in, want string
	}{
		{
			name: "img src",
			in:   `<html><body><img src="/assets/logo.svg"></body></html>`,
			want: `<html><body><img src="/rx/abc123/assets/logo.svg"></body></html>`,
		},
		{
			name: "a href and form action",
			in:   `<a href="/sessions/x">s</a><form action="/submit"></form>`,
			want: `<a href="/rx/abc123/sessions/x">s</a><form action="/rx/abc123/submit"></form>`,
		},
		{
			name: "script src and source",
			in:   `<script src="/app.js"></script><source src="/a.webm">`,
			want: `<script src="/rx/abc123/app.js"></script><source src="/rx/abc123/a.webm">`,
		},
		{
			name: "already-prefixed is left alone",
			in:   `<img src="/rx/abc123/assets/logo.svg"><img src="https://cdn.example/x.png">`,
			want: `<img src="/rx/abc123/assets/logo.svg"><img src="https://cdn.example/x.png">`,
		},
		{
			name: "js string literal is not rewritten",
			in:   `<script>var x = "src=\"/assets/js-only\"";</script>`,
			want: `<script>var x = "src=\"/assets/js-only\"";</script>`,
		},
		{
			name: "protocol-relative URL is left alone",
			in:   `<img src="//cdn.example/x.png">`,
			want: `<img src="//cdn.example/x.png">`,
		},
		{
			name: "data-src is not rewritten",
			in:   `<div data-src="/keep-me"></div>`,
			want: `<div data-src="/keep-me"></div>`,
		},
		{
			name: "xlink:href is not rewritten",
			in:   `<use xlink:href="/icon.svg">`,
			want: `<use xlink:href="/icon.svg">`,
		},
		{
			// Pins the current behaviour: property-style JS assignments
			// (`el.src=`, `location.href=`) are NOT rewritten because the
			// [\s"'] prefix anchor excludes "." — the old \b anchor would
			// have rewritten these after a "<" comparison. See rootAttrRe's
			// comment.
			name: "js property assignment is not rewritten",
			in:   `<script>for(var i=0;i<n;i++){el.src="/assets/x.png";}</script>`,
			want: `<script>for(var i=0;i<n;i++){el.src="/assets/x.png";}</script>`,
		},
		{
			name: "poster and action",
			in:   `<video poster="/thumb.jpg"></video><form action="/submit"></form>`,
			want: `<video poster="/rx/abc123/thumb.jpg"></video><form action="/rx/abc123/submit"></form>`,
		},
		{
			// F: multiple rewriteable attributes in ONE tag must all be
			// rewritten (a single regex pass over the whole tag only catches
			// the last attribute).
			name: "multiple attrs in one tag",
			in:   `<video src="/v.mp4" poster="/t.jpg"></video>`,
			want: `<video src="/rx/abc123/v.mp4" poster="/rx/abc123/t.jpg"></video>`,
		},
		{
			// G: attribute names are case-insensitive in HTML.
			name: "uppercase attribute name",
			in:   `<IMG SRC="/x.png">`,
			want: `<IMG SRC="/rx/abc123/x.png">`,
		},
		{
			// H: unquoted attribute values are valid HTML5.
			name: "unquoted attribute value",
			in:   `<img src=/x.png>`,
			want: `<img src=/rx/abc123/x.png>`,
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := string(rewriteRootAttrs([]byte(c.in), mount))
			if got != c.want {
				t.Fatalf("rewriteRootAttrs(%q) = %q, want %q", c.in, got, c.want)
			}
		})
	}
}
