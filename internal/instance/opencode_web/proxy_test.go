package opencode_web

import (
	"io"
	"net/http"
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
	s := buildInjectScript("/__opencode/abc123", "d3Q") // base64url("wt")
	for _, want := range []string{
		"history.replaceState",
		"opencode.settings.dat:defaultServerUrl",
		"opencode.global.dat:server", // server-list normalization
		"data-project",               // hide foreign projects
		"home-add-project",           // hide add-project entry
		"MutationObserver",
		"d3Q", // the worktree base64url is embedded
		"mw-oc/hidden-report", // L2/L3 hide-effectiveness report
		"sidebar-rail",        // L2 structural anchor
	} {
		if !strings.Contains(s, want) {
			t.Fatalf("buildInjectScript missing %q", want)
		}
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
