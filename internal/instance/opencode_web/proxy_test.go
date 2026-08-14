package opencode_web

import (
	"io"
	"net/http"
	"os"
	"os/exec"
	"strings"
	"testing"
)

func TestRewriteRootAttrs(t *testing.T) {
	const mount = "/__opencode/abc123"

	tests := []struct {
		name string
		in   string
		want string
	}{
		{
			"src and href",
			`<link rel="stylesheet" href="/assets/index.css"><script src="/assets/index.js"></script>`,
			`<link rel="stylesheet" href="/__opencode/abc123/assets/index.css"><script src="/__opencode/abc123/assets/index.js"></script>`,
		},
		{
			"action and poster",
			`<form action="/submit"><video src="/v.mp4" poster="/p.jpg"></video></form>`,
			`<form action="/__opencode/abc123/submit"><video src="/__opencode/abc123/v.mp4" poster="/__opencode/abc123/p.jpg"></video></form>`,
		},
		{
			"protocol-relative untouched",
			`<script src="//cdn.example.com/x.js"></script>`,
			`<script src="//cdn.example.com/x.js"></script>`,
		},
		{
			"already prefixed is idempotent",
			`<script src="/__opencode/abc123/x.js"></script>`,
			`<script src="/__opencode/abc123/x.js"></script>`,
		},
		{
			"data-src and xlink:href untouched",
			`<img data-src="/lazy.png"><use xlink:href="/sprite.svg">`,
			`<img data-src="/lazy.png"><use xlink:href="/sprite.svg">`,
		},
		{
			"unquoted attribute value",
			`<img src=/x.png>`,
			`<img src=/__opencode/abc123/x.png>`,
		},
		{
			"no attributes",
			`<div>hello</div>`,
			`<div>hello</div>`,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := string(rewriteRootAttrs([]byte(tt.in), mount))
			if got != tt.want {
				t.Fatalf("rewriteRootAttrs(%q) =\n  %q\nwant\n  %q", tt.in, got, tt.want)
			}
		})
	}
}

func TestFixProxyHTMLSkipsCompressed(t *testing.T) {
	// A compressed HTML body must be passed through untouched (no splice).
	resp := newHTMLResponse(t, `<html><head></head></html>`)
	resp.Header.Set("Content-Encoding", "gzip")
	fixProxyHTML(resp, "/__opencode/x", "<base>", "/tmp/wt")
	if strings.Contains(readBody(t, resp), "<base>") {
		t.Fatal("compressed HTML must not be injected")
	}
}

func TestFixProxyHTMLCSPAnchor(t *testing.T) {
	// Anchor present → hash appended, no drift flag.
	resp := newHTMLResponse(t, `<html><head></head></html>`)
	resp.Header.Set("Content-Security-Policy", "script-src 'self' 'wasm-unsafe-eval'")
	missing := fixProxyHTML(resp, "/__opencode/x", "<base>", "/tmp/wt")
	if missing {
		t.Fatal("anchor present but reported missing")
	}
	if csp := resp.Header.Get("Content-Security-Policy"); !strings.Contains(csp, "'sha256-") {
		t.Fatalf("hash not appended: %q", csp)
	}

	// Anchor absent → drift flagged, CSP left unchanged.
	resp = newHTMLResponse(t, `<html><head></head></html>`)
	resp.Header.Set("Content-Security-Policy", "script-src 'self'")
	missing = fixProxyHTML(resp, "/__opencode/x", "<base>", "/tmp/wt")
	if !missing {
		t.Fatal("anchor absent but not reported missing")
	}
	if csp := resp.Header.Get("Content-Security-Policy"); strings.Contains(csp, "'sha256-") {
		t.Fatalf("hash appended without anchor: %q", csp)
	}
}

func TestBuildInjectScript(t *testing.T) {
	s := buildInjectScript("/__opencode/abc123", "d3Q", "/tmp/wt") // base64url("wt")
	for _, want := range []string{
		"history.replaceState",
		"opencode.settings.dat:defaultServerUrl",
		"opencode.global.dat:server", // server-list normalization
		"data-project",               // disable foreign projects (visible but inert)
		"home-add-project",           // L2 structural anchor (add-project entry)
		"MutationObserver",
		"d3Q", // the worktree base64url is embedded
		"mw-oc/hidden-report", // L2/L3 disable-effectiveness report
		"sidebar-rail",        // L2 structural anchor (session page)
		"home-session-search", // L2 structural anchor (home page, full-page embed lands here)
		// URL rewriting must also cover root-relative paths and URL objects,
		// otherwise opencode's promise client (which fetches `new URL(path,
		// baseUrl)`) hits the myworktree origin instead of the proxy and 404s.
		"u.charAt(0)==='/'",    // root-relative path rewrite
		"i.href",               // URL-object branch
		"return new URL(h)",    // rewrite a URL object's href
		// Preseed the persisted project list with the worktree so opencode's
		// "new session" button has a project to open (empty projects → the
		// home page's create button silently no-ops).
		"worktree:wtp",         // project list preseed
		"expanded:true",        // project list preseed
		"/tmp/wt",              // raw worktree path is embedded
		`dn="wt"`,              // server displayName literal = worktree basename
		"http:{url:pu}",        // server entry carries the proxy URL
		"displayName:dn",       // …and a display name (no raw-URL search placeholder)
		"var OW=Worker",        // rewrite Worker() script URLs (markdown highlight worker)
		"home-projects-scroll", // disable the home page project list (visible but inert)
		"project-switch",       // disable session-page project switcher
		"pointer-events:none",  // disable = block pointer events (keep visible)
		"aria-disabled",        // disable = mark aria-disabled
		"disable-failed",       // L3 reports when a foreign entry is still enabled
		// The project-switch rule must disable only NON-current worktree
		// entries: the current entry doubles as the sidebar expand/collapse
		// toggle and must stay interactive (WORKTREE-ISOLATION.md §2.4 entry
		// 2: keep current).
		`[data-action="project-switch"]:not([data-project="`,
	} {
		if !strings.Contains(s, want) {
			t.Fatalf("buildInjectScript missing %q", want)
		}
	}
}

// TestBuildInjectScriptSyntax verifies the injected inline script parses as
// valid JavaScript (it is spliced into a CSP-hashed <script> tag — a syntax
// error would white-screen the embedded opencode UI). Requires node on PATH;
// skipped otherwise so the suite still runs in node-less environments.
func TestBuildInjectScriptSyntax(t *testing.T) {
	s := buildInjectScript("/__opencode/abc123", "d3Q", "/tmp/wt")
	node, err := exec.LookPath("node")
	if err != nil {
		t.Skip("node not available; skipping inject-script syntax check")
	}
	tmp, err := os.CreateTemp(t.TempDir(), "inject-*.js")
	if err != nil {
		t.Fatal(err)
	}
	defer tmp.Close()
	if _, err := tmp.WriteString(s); err != nil {
		t.Fatal(err)
	}
	if out, err := exec.Command(node, "--check", tmp.Name()).CombinedOutput(); err != nil {
		t.Fatalf("inject script has a syntax error: %v\n%s\n--- script ---\n%s", err, out, s)
	}
}

func newHTMLResponse(t *testing.T, body string) *http.Response {
	t.Helper()
	resp := &http.Response{
		Header:     http.Header{},
		Body:       io.NopCloser(strings.NewReader(body)),
		StatusCode: 200,
	}
	resp.Header.Set("Content-Type", "text/html; charset=utf-8")
	return resp
}

func readBody(t *testing.T, resp *http.Response) string {
	t.Helper()
	b, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}
