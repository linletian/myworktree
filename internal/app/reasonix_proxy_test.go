package app

import (
	"encoding/json"
	"io"
	"log"
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
if args[:1] == ["--version"]:
    print("reasonix v1.22.0")
    sys.exit(0)
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
	// Issue #48 layout injection must be present: collapsed-by-default class,
	// our own toggle button, the desktop breakpoint override, and the
	// configurable sidebar width.
	if !strings.Contains(body, "mw-rx") || !strings.Contains(body, "#mw-sidebar-toggle") {
		t.Fatalf("layout injection (issue #48) missing: %q", body)
	}
	if !strings.Contains(body, "grid-template-columns:0 1fr") {
		t.Fatalf("collapsed sidebar CSS missing: %q", body)
	}
	if !strings.Contains(body, "--mw-sidebar-w") {
		t.Fatalf("configurable sidebar width missing: %q", body)
	}
}

// TestInjectReasonixLayoutDefaults pins the issue #48 layout injection:
// collapsed by default, desktop-only (native mobile behaviour untouched), and
// the expanded state restoring the upstream grid with a configurable width.
func TestInjectReasonixLayoutDefaults(t *testing.T) {
	const mount = "/rx/abc123"
	page := []byte("<html><head><style>.app{display:grid;grid-template-columns:220px 1fr}</style></head><body></body></html>")
	got := string(injectReasonixPrefix(page, mount))
	for _, want := range []string{
		"classList.add('mw-rx')", // collapsed by default
		"#mw-sidebar-toggle",
		"@media(min-width:769px)", // desktop-only override
		".mw-rx .app{grid-template-columns:0 1fr}",
		".mw-rx .transcript{grid-column:2;grid-row:1}",              // pin chat to column 2 (auto-placement would push it into the 0px column)
		".mw-rx .footer{grid-column:2;grid-row:2}",                  // pin input bar to row 2
		".app{grid-template-columns:var(--mw-sidebar-w,220px) 1fr}", // expanded restores grid
		"--mw-sidebar-w:220px",
		"document.addEventListener('DOMContentLoaded'", // button created once body exists
		"@media(max-width:768px)",                      // narrow screens keep native mobile UI
		"#mw-sidebar-toggle{display:none!important}",   // ...so our toggle is hidden there
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("layout injection missing %q\n--- injected page ---\n%s", want, got)
		}
	}
	// The layout injection must not disturb the URL-prefix shim.
	if !strings.Contains(got, "window.fetch") || !strings.Contains(got, "MutationObserver") {
		t.Fatalf("url-prefix shim broken by layout injection: %q", got)
	}
	// Injection must precede </head> so our <style> wins over the upstream
	// same-specificity rules (e.g. .app grid-template-columns).
	headEnd := strings.Index(got, "</head>")
	layoutStyle := strings.Index(got, ".app{grid-template-columns:var(--mw-sidebar-w")
	if headEnd < 0 || layoutStyle < 0 || layoutStyle > headEnd {
		t.Fatalf("layout <style> must be injected before </head> (layoutStyle=%d headEnd=%d)", layoutStyle, headEnd)
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

// TestReasonixIndependentListener verifies the issue #44 independent listener:
// a separate loopback port serving ONLY /rx/ (no myworktree API), so the
// embedded reasonix page is cross-origin with the management API. The
// independent mux must proxy the reasonix UI with cookie injection but must
// not answer /api/* — a cross-origin iframe script calling /api/instances
// gets 404, never the user's data.
func TestReasonixIndependentListener(t *testing.T) {
	_, m, fs := newProxyTestEnv(t)
	id := startReasonixViaManager(t, m)
	defer func() { _ = m.Stop(id) }()

	srv := &Server{
		cfg:         Config{ListenAddr: "127.0.0.1:0"},
		logger:      log.New(io.Discard, "", 0),
		dataDir:     filepath.Dir(fs.Path),
		store:       fs,
		instanceMgr: m,
		mux:         http.NewServeMux(),
		authFails:   map[string]authFail{},
	}
	if _, err := srv.Start(); err != nil {
		t.Fatalf("Start failed: %v", err)
	}
	defer srv.Shutdown()

	if srv.rxAddr == "" {
		t.Fatalf("independent listener not enabled (rxAddr empty)")
	}

	// 1) The independent listener proxies /rx/<id>/ with cookie injection.
	resp, err := http.Get("http://" + srv.rxAddr + "/rx/" + id + "/history")
	if err != nil {
		t.Fatalf("GET /rx via independent listener: %v", err)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK || !strings.Contains(string(body), "cookie=reasonix_token=") {
		t.Fatalf("independent /rx request: status=%d body=%q", resp.StatusCode, body)
	}

	// 2) The independent listener does NOT serve the myworktree API.
	resp2, err := http.Get("http://" + srv.rxAddr + "/api/instances")
	if err != nil {
		t.Fatalf("GET /api/instances on independent listener: %v", err)
	}
	resp2.Body.Close()
	if resp2.StatusCode != http.StatusNotFound {
		t.Fatalf("independent listener served /api/instances: status=%d, want 404", resp2.StatusCode)
	}
}

// TestReasonixWebURL verifies /api/instances reports web_url (pointing at the
// independent listener) for reasonix instances and nothing for tty instances.
func TestReasonixWebURL(t *testing.T) {
	_, m, fs := newProxyTestEnv(t)
	id := startReasonixViaManager(t, m)
	defer func() { _ = m.Stop(id) }()
	st, err := fs.Load()
	if err != nil {
		t.Fatal(err)
	}
	st.Instances = append(st.Instances, store.ManagedInstance{ID: "tty1", WorktreeID: "wt1", Kind: "", Status: "stopped"})
	if err := fs.Save(st); err != nil {
		t.Fatal(err)
	}

	srv := &Server{
		cfg:         Config{ListenAddr: "127.0.0.1:0"},
		logger:      log.New(io.Discard, "", 0),
		dataDir:     filepath.Dir(fs.Path),
		store:       fs,
		instanceMgr: m,
		mux:         http.NewServeMux(),
		authFails:   map[string]authFail{},
	}
	if _, err := srv.Start(); err != nil {
		t.Fatalf("Start failed: %v", err)
	}
	defer srv.Shutdown()

	req := httptest.NewRequest(http.MethodGet, "/api/instances", nil)
	rec := httptest.NewRecorder()
	srv.handleInstances(rec, req)
	var resp map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("invalid JSON: %v", err)
	}
	insts, ok := resp["instances"].([]any)
	if !ok {
		t.Fatalf("instances not an array: %v", resp["instances"])
	}
	rxSeen := false
	for _, raw := range insts {
		m2 := raw.(map[string]any)
		switch m2["id"] {
		case id:
			rxSeen = true
			u, _ := m2["web_url"].(string)
			want := "http://" + srv.rxAddr + "/rx/" + id + "/"
			if u != want {
				t.Fatalf("reasonix web_url = %q, want %q", u, want)
			}
		case "tty1":
			if u, has := m2["web_url"].(string); has && u != "" {
				t.Fatalf("tty instance must not have web_url, got %q", u)
			}
		}
	}
	if !rxSeen {
		t.Fatalf("reasonix instance %q missing from instances list", id)
	}
}

// TestReasonixNonLoopbackFallsBack verifies the scheme-A fallback: when the
// main listener is open to the network (default 0.0.0.0, or an explicit LAN
// IP), the independent listener must NOT be started — an absolute
// http://127.0.0.1:<port> web_url would be resolved by a remote browser to
// ITSELF and the iframe would fail. Instead web_url stays empty and the
// frontend uses the same-origin /rx/<id>/ relative path, which follows the
// browser's current origin and works over LAN.
func TestReasonixNonLoopbackFallsBack(t *testing.T) {
	p, m, fs := newProxyTestEnv(t)
	id := startReasonixViaManager(t, m)
	defer func() { _ = m.Stop(id) }()

	srv := &Server{
		cfg:         Config{ListenAddr: "0.0.0.0:0", AuthToken: "test-token"}, // open to the network
		logger:      log.New(io.Discard, "", 0),
		dataDir:     filepath.Dir(fs.Path),
		store:       fs,
		instanceMgr: m,
		mux:         http.NewServeMux(),
		authFails:   map[string]authFail{},
	}
	if _, err := srv.Start(); err != nil {
		t.Fatalf("Start failed: %v", err)
	}
	defer srv.Shutdown()

	if srv.rxAddr != "" {
		t.Fatalf("independent listener must NOT be enabled on non-loopback listen (rxAddr=%q)", srv.rxAddr)
	}
	if u := srv.reasonixWebURL(id); u != "" {
		t.Fatalf("web_url must be empty on non-loopback listen, got %q", u)
	}

	// The same-origin /rx/ route still works through the main mux (this is
	// the relative path the frontend falls back to over LAN).
	srv.mux.Handle("/rx/", p)
	req := httptest.NewRequest(http.MethodGet, "/rx/"+id+"/history", nil)
	rec := httptest.NewRecorder()
	srv.mux.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("same-origin /rx/ over non-loopback listen: status=%d, want 200 (body: %s)", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), "cookie=reasonix_token=") {
		t.Fatalf("same-origin /rx/ did not inject the reasonix cookie: %q", rec.Body.String())
	}
}

// TestServerShutdownStopsReasonix verifies myworktree shutdown stops running
// reasonix instances (behavior parity with tty instances, which die when
// their PTY hangs up on process exit).
func TestServerShutdownStopsReasonix(t *testing.T) {
	_, m, fs := newProxyTestEnv(t)
	id := startReasonixViaManager(t, m)

	srv := &Server{
		cfg:         Config{ListenAddr: "127.0.0.1:0"},
		logger:      log.New(io.Discard, "", 0),
		dataDir:     filepath.Dir(fs.Path),
		store:       fs,
		instanceMgr: m,
		mux:         http.NewServeMux(),
		authFails:   map[string]authFail{},
	}
	if _, err := srv.Start(); err != nil {
		t.Fatalf("Start failed: %v", err)
	}
	if _, ok, herr := m.Reasonix.Health(id); herr != nil || !ok {
		t.Fatalf("reasonix serve should be healthy before Shutdown (ok=%v err=%v)", ok, herr)
	}

	srv.Shutdown()

	if _, ok, herr := m.Reasonix.Health(id); herr != nil || ok {
		t.Fatalf("reasonix serve must be stopped by Shutdown (parity with tty; ok=%v err=%v)", ok, herr)
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
