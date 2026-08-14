package opencode_web

import (
	"io"
	"net/http"
	"os"
	"os/exec"
	"strconv"
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

// TestFixProxyHTMLInjectsCaseInsensitiveHead pins the <head> injection
// to case-insensitive / attribute-tolerant matching: HTML permits
// <HEAD lang="en"> and <head >, and the single-worktree isolation
// depends on the injection landing. Only the first head tag receives
// the injection.
func TestFixProxyHTMLInjectsCaseInsensitiveHead(t *testing.T) {
	for _, html := range []string{
		`<html><HEAD lang="en"></HEAD><body></body></html>`,
		`<html><head ></head><body></body></html>`,
		`<html><HeAd></HeAd><body></body></html>`,
	} {
		resp := newHTMLResponse(t, html)
		fixProxyHTML(resp, "/__opencode/x", "<base>", "/tmp/wt")
		body := readBody(t, resp)
		if !strings.Contains(body, "<base>") || !strings.Contains(body, "<script>") {
			t.Fatalf("injection missing for %q, body: %s", html, body)
		}
		if !strings.Contains(body, "</HEAD") && !strings.Contains(body, "</HeAd") && !strings.Contains(body, "</head") {
			t.Fatalf("head tag mangled for %q, body: %s", html, body)
		}
	}

	// No head at all → body passes through without injection.
	resp := newHTMLResponse(t, `<html><body>no head</body></html>`)
	fixProxyHTML(resp, "/__opencode/x", "<base>", "/tmp/wt")
	if strings.Contains(readBody(t, resp), "<base>") {
		t.Fatal("injection should not apply without a head tag")
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
		"d3Q",                 // the worktree base64url is embedded
		"mw-oc/hidden-report", // L2/L3 disable-effectiveness report
		"sidebar-rail",        // L2 structural anchor (session page)
		"home-session-search", // L2 structural anchor (home page, full-page embed lands here)
		// URL rewriting must also cover root-relative paths and URL objects,
		// otherwise opencode's promise client (which fetches `new URL(path,
		// baseUrl)`) hits the myworktree origin instead of the proxy and 404s.
		"u.charAt(0)==='/'", // root-relative path rewrite
		"i.href",            // URL-object branch
		"return new URL(h)", // rewrite a URL object's href
		// Preseed the persisted project list with the worktree so opencode's
		// "new session" button has a project to open (empty projects → the
		// home page's create button silently no-ops).
		"worktree:wtp",               // project list preseed
		"expanded:true",              // project list preseed
		"/tmp/wt",                    // raw worktree path is embedded
		`dn="wt"`,                    // server displayName literal = worktree basename
		"http:{url:location.origin}", // server entry uses the bare origin (single-server
		// mode: same key as opencode's canonical server, so resolveServerList
		// merges them and the home page renders like a native launch)
		"displayName:dn",                    // …and a display name (no raw-URL search placeholder)
		"defaultServerUrl',location.origin", // default server must be the origin too, else
		// state.active keys a server absent from the merged list and the
		// preseeded project (scoped under canonical "local") is never read
		"projects['local']", // project preseed under the canonical scope key
		// The preseed is self-healing, not fill-if-empty: projects[local],
		// lastProject[local] and the server displayName are compared against
		// the current worktree and rewritten when they differ, so an
		// instance-scoped store polluted by an earlier worktree (or by the
		// SPA navigating to another directory) is repaired on the next load.
		"displayName===dn",              // stale same-origin displayName is repaired
		"prj[0].worktree!==wtp",         // stale project preseed is repaired
		"sd.lastProject['local']!==wtp", // stale autoselect target is repaired
		"var OW=Worker",                 // rewrite Worker() script URLs (markdown highlight worker)
		"home-projects-scroll",          // disable the home page project list (visible but inert)
		"project-switch",                // disable session-page project switcher
		"pointer-events:none",           // disable = block pointer events (keep visible)
		"aria-disabled",                 // disable = mark aria-disabled
		"disable-failed",                // L3 reports when a foreign entry is still enabled
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

// TestBuildInjectScriptSingleServer runs the injected script inside a node
// VM with a mocked browser environment and asserts the preseed yields the
// single-server state a native `opencode web` launch has: the persisted
// server entry carries the bare origin (so opencode's resolveServerList
// merges it with the canonical entry.tsx server), the project preseed lands
// under the canonical "local" scope, and the merged server list has exactly
// one entry with the worktree displayName preserved.
//
// It also simulates TWO instances sharing the same origin (as the myworktree
// portal does — iframes differ only by /__opencode/<id> path prefix) and
// asserts the instance-scoped server store redirects the shared
// 'opencode.global.dat:server' key to a per-instance key, so instance B
// loading after instance A neither clobbers A's preseed nor adopts A's
// project (the cross-instance localStorage clash the single-server preseed
// would otherwise introduce).
//
// A third phase pre-populates instance A's store with STALE worktree data
// (as a browser that once loaded the instance in a previous worktree, or an
// SPA session-open in another directory, leaves behind) and asserts the
// self-healing preseed rewrites projects/local, lastProject/local and the
// server displayName back to A's worktree on the next load.
func TestBuildInjectScriptSingleServer(t *testing.T) {
	node, err := exec.LookPath("node")
	if err != nil {
		t.Skip("node not available")
	}
	scriptA := buildInjectScript("/__opencode/A", "d3RB", "/wtA")
	scriptB := buildInjectScript("/__opencode/B", "d3RC", "/wtB")
	probe := `
const assert = require("node:assert");
const storage = new Map();
const origin = "http://opencode.test";
global.localStorage = {
  getItem: (k) => (storage.has(k) ? storage.get(k) : null),
  setItem: (k, v) => storage.set(k, String(v)),
  removeItem: (k) => storage.delete(k),
};
global.history = { replaceState() {} };
global.location = { origin, hostname: "opencode.test", pathname: "/__opencode/A" };
global.document = {
  readyState: "complete",
  addEventListener() {},
  querySelector: () => null,
  querySelectorAll: () => [],
  createElement: () => ({ appendChild() {}, set textContent(_) {} }),
  head: { appendChild() {} },
};
global.window = global;
global.XMLHttpRequest = { prototype: { open() {} } };
global.Worker = function () {};
global.EventSource = function () {};
global.MutationObserver = function () { this.observe = function () {} };

function mergeServers(stored) {
  // Mirror opencode's resolveServerList (context/server.tsx): props carries
  // the canonical server from entry.tsx (url = location.origin), stored is
  // what the injected script persisted. They must merge into ONE server.
  const canonical = { type: "http", http: { url: origin } };
  const deduped = new Map([[canonical.http.url, canonical]]);
  for (const value of stored) {
    const conn = value.type === "http" ? value : { type: "http", http: value };
    const key = conn.http.url;
    const existing = deduped.get(key);
    if (existing) deduped.set(key, { ...existing, ...conn, http: { ...existing.http, ...conn.http } });
    else deduped.set(key, conn);
  }
  return [...deduped.values()];
}

// --- Instance A loads first ---
const __scriptA = __SCRIPT_A__;
eval(__scriptA);
assert.strictEqual(storage.get("opencode.settings.dat:defaultServerUrl"), origin);
assert.strictEqual(storage.has("opencode.global.dat:server"), false,
  "the shared key must never be written (instance-scoped redirect)");
const keyA = "opencode.global.dat:server/__opencode/A";
const sdA = JSON.parse(storage.get(keyA));
assert.strictEqual(sdA.list.length, 1, "server list must be a single entry");
assert.strictEqual(sdA.list[0].http.url, origin, "server url must be the bare origin");
assert.strictEqual(sdA.list[0].displayName, "wtA");
assert.deepStrictEqual(sdA.projects["local"], [{ worktree: "/wtA", expanded: true }]);
const serversA = mergeServers(sdA.list);
assert.strictEqual(serversA.length, 1, "canonical + stored must merge into a single server");
assert.strictEqual(serversA[0].displayName, "wtA", "displayName survives the merge");
const storedA = storage.get(keyA);

// --- Instance B loads afterwards (same origin, different instance id) ---
global.location = { origin, hostname: "opencode.test", pathname: "/__opencode/B" };
const __scriptB = __SCRIPT_B__;
eval(__scriptB);
const keyB = "opencode.global.dat:server/__opencode/B";
const sdB = JSON.parse(storage.get(keyB));
assert.deepStrictEqual(sdB.projects["local"], [{ worktree: "/wtB", expanded: true }],
  "B must preseed its own worktree");
assert.strictEqual(sdB.list[0].displayName, "wtB");
assert.strictEqual(storage.get(keyA), storedA,
  "A's store must not be clobbered by B loading later");
const serversB = mergeServers(sdB.list);
assert.strictEqual(serversB.length, 1, "B also presents a single server");
assert.strictEqual(serversB[0].displayName, "wtB");

// --- Phase 3: stale-era repair (self-healing preseed) ---
// Simulate a browser whose instance-scoped store still carries data from an
// earlier worktree (or from an SPA navigation to another directory): the
// next load must rewrite the preseed back to A's worktree instead of
// leaving the stale (possibly deleted) directory in place.
storage.set(keyA, JSON.stringify({
  list: [{ type: "http", http: { url: origin }, displayName: "stale" }],
  projects: { local: [{ worktree: "/stale", expanded: false }] },
  lastProject: { local: "/stale" },
}));
global.location = { origin, hostname: "opencode.test", pathname: "/__opencode/A" };
eval(__scriptA);
const sdA2 = JSON.parse(storage.get(keyA));
assert.strictEqual(sdA2.list.length, 1, "stale list must be collapsed to a single server");
assert.strictEqual(sdA2.list[0].displayName, "wtA", "stale displayName must be repaired");
assert.deepStrictEqual(sdA2.projects["local"], [{ worktree: "/wtA", expanded: true }],
  "stale project preseed must be repaired to the current worktree");
assert.strictEqual(sdA2.lastProject["local"], "/wtA",
  "stale lastProject must be repaired so autoselect lands on the current worktree");
assert.strictEqual(storage.has("opencode.global.dat:server"), false,
  "the shared key must never be written (instance-scoped redirect)");
console.log("SINGLE_SERVER_SIM_OK");
process.exit(0);
`
	cmd := exec.Command(node, "-")
	probe = strings.Replace(probe, "__SCRIPT_A__", strconv.Quote(scriptA), 1)
	probe = strings.Replace(probe, "__SCRIPT_B__", strconv.Quote(scriptB), 1)
	cmd.Stdin = strings.NewReader(probe)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("simulation failed: %v\n%s", err, out)
	}
	if !strings.Contains(string(out), "SINGLE_SERVER_SIM_OK") {
		t.Fatalf("simulation did not complete: %s", out)
	}
}

// TestBuildInjectScriptEscapes guards against script-tag breakout: the
// injected script is spliced into an HTML <script> element, so any value
// interpolated into it must not contain a raw '<' (a worktree path like
// /tmp/a<b could otherwise terminate the element early). Values are emitted
// as JS string literals with '<' escaped to \u003c.
func TestBuildInjectScriptEscapes(t *testing.T) {
	s := buildInjectScript("/__opencode/abc123", "d3Q", "/tmp/a<b>c</script>")
	// HTML script data ends only at `</script` (case-insensitive) or `<!--`;
	// bare '<' operators in JS (i<len) are inert in that context, but any
	// interpolated value must not be able to form the terminator.
	if strings.Contains(strings.ToLower(s), "</script") || strings.Contains(s, "<!--") {
		t.Fatalf("injected script can terminate its <script> element early:\n%s", s)
	}
	for _, want := range []string{`/tmp/a\u003cb>c\u003c/script>`, `\u003c`, `dn="script>"`} {
		if !strings.Contains(s, want) {
			t.Fatalf("injected script missing escaped literal %q", want)
		}
	}
}

// TestInjectURLRewriteBehavior executes the injected script in a node VM and
// drives every network channel the SDK can use, asserting the r() rewrite
// actually prefixes same-origin/root-relative URLs with the instance prefix
// at runtime (not just textual presence): fetch (string + URL object), XHR,
// EventSource, navigator.sendBeacon and WebSocket. Remote URLs must pass
// through untouched.
func TestInjectURLRewriteBehavior(t *testing.T) {
	node, err := exec.LookPath("node")
	if err != nil {
		t.Skip("node not available")
	}
	script := buildInjectScript("/__opencode/A", "d3RB", "/wtA")
	probe := `
const assert = require("node:assert");
const origin = "http://opencode.test";
const calls = [];
global.localStorage = { getItem: () => null, setItem() {}, removeItem() {} };
global.history = { replaceState() {} };
global.location = { origin, hostname: "opencode.test", pathname: "/__opencode/A" };
global.document = {
  readyState: "complete",
  addEventListener() {},
  querySelector: () => null,
  querySelectorAll: () => [],
  createElement: () => ({ appendChild() {}, set textContent(_) {} }),
  head: { appendChild() {} },
};
global.window = global;
global.fetch = async (u) => {
  calls.push("fetch:" + (u && u.url ? u.url : String(u)));
  if (u instanceof Request) {
    calls.push("fetch-duplex:" + u.duplex + ":" + u.method);
    // Read the rewritten body back: it must still be a readable
    // ReadableStream carrying the original payload (duplex:'half'
    // alone is not enough — a rebuild that drops/clones the stream
    // would pass the duplex check but lose the message). The guard is
    // written so a MISSING body still lands in the call log instead of
    // silently skipping the read.
    const reader = u.body && u.body.getReader ? u.body.getReader() : null;
    if (reader) {
      const chunks = [];
      for (;;) {
        const { done, value } = await reader.read();
        if (done) break;
        chunks.push(new TextDecoder().decode(value));
      }
      calls.push("fetch-body:" + chunks.join(""));
    } else {
      calls.push("fetch-body:MISSING");
    }
  }
  return Promise.resolve({ ok: true, status: 200, json: () => Promise.resolve({}), text: () => Promise.resolve("") });
};
global.EventSource = function (u) { calls.push("es:" + u); };
global.XMLHttpRequest = function () {};
global.XMLHttpRequest.prototype.open = function (m, u) { calls.push("xhr:" + u); };
global.Worker = function () {};
global.MutationObserver = function () { this.observe = function () {} };
Object.defineProperty(global, "navigator", {
  value: { sendBeacon: (u) => { calls.push("beacon:" + u); return true; } },
  configurable: true,
  writable: true,
});
global.WebSocket = function (u) { calls.push("ws:" + u); };
const __script = __SCRIPT__;
eval(__script);

const P = "/__opencode/A";
const encoder = new TextEncoder();
function streamBody() {
  return new ReadableStream({ start(c) { c.enqueue(encoder.encode("hello")); c.close(); } });
}
// Rewrite-at-construction: the SDK builds every request via
// new Request(url, init); after the inject script runs, that
// constructor must prefix bare-origin URLs itself (so fetch() gets an
// already-prefixed Request and ri() returns it unchanged).
const built = new Request(origin + "/session/2/message", { method: "POST", body: JSON.stringify({ content: "hi" }), headers: { "content-type": "application/json" } });
assert.strictEqual(built.url, origin + P + "/session/2/message", "Request constructor must prefix bare-origin URLs");
assert.strictEqual(built.method, "POST");
// Request copy construction (new Request(existing)) must also keep the
// prefix: the SDK's rewrite() uses new Request(url, request), and a pure
// copy (init omitted) exercises the u.url branch of the wrapper.
const copied = new Request(built);
assert.strictEqual(copied.url, origin + P + "/session/2/message", "copied Request must keep the prefix");
assert.strictEqual(copied.method, "POST");
// Copy with an override init must apply the override on the prefixed URL.
const copied2 = new Request(built, { method: "GET" });
assert.strictEqual(copied2.url, origin + P + "/session/2/message", "copied Request with override init must keep the prefix");
assert.strictEqual(copied2.method, "GET");

Promise.resolve()
  .then(async () => {
    // Copy construction must keep the body (u.url branch passes the source
    // Request as init when no init is given). Use a dedicated source here:
    // undici does not tee the body on Request copy (browsers do), so
    // reading the copy would lock the shared stream and break fetch(built)
    // below.
    const bodySrc = new Request(origin + "/session/9/message", { method: "POST", body: JSON.stringify({ content: "hi" }) });
    const bodyCopy = new Request(bodySrc);
    const cbody = await bodyCopy.text();
    assert.strictEqual(cbody, JSON.stringify({ content: "hi" }), "copied Request must keep the body");
  })
  .then(() => fetch(origin + "/session"))
  .then(() => fetch(new URL("/config", origin)))
  .then(() => fetch("https://example.com/remote"))
  .then(() => fetch(built))
  .then(() => fetch(new Request(origin + "/session/1/message", { method: "POST", body: streamBody(), duplex: "half" })))
  .then(() => { new XMLHttpRequest().open("GET", "/api/event"); })
  .then(() => { new EventSource("/api/health"); })
  .then(() => { navigator.sendBeacon("/telemetry", "{}"); })
  .then(() => { new WebSocket("ws://opencode.test/event"); })
  .then(() => {
    assert.deepStrictEqual(calls, [
      "fetch:" + origin + P + "/session",
      "fetch:" + origin + P + "/config",
      "fetch:https://example.com/remote",
      "fetch:" + origin + P + "/session/2/message",
      "fetch-duplex:half:POST",
      "fetch-body:" + JSON.stringify({ content: "hi" }),
      "fetch:" + origin + P + "/session/1/message",
      // Streaming upload (chat message) must keep duplex:'half', the
      // method, AND the body stream itself after the rewrite — otherwise
      // Chromium rejects the Request with "ReadableStream uploading is not
      // supported" or the message payload silently vanishes.
      "fetch-duplex:half:POST",
      "fetch-body:hello",
      "xhr:" + origin + P + "/api/event",
      "es:" + origin + P + "/api/health",
      "beacon:" + origin + P + "/telemetry",
      "ws:ws://opencode.test" + P + "/event",
    ]);
    console.log("REWRITE_BEHAVIOR_OK");
    process.exit(0);
  })
  .catch((e) => { console.error(e); process.exit(1); });
`
	cmd := exec.Command(node, "-")
	probe = strings.Replace(probe, "__SCRIPT__", strconv.Quote(script), 1)
	cmd.Stdin = strings.NewReader(probe)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("rewrite simulation failed: %v\n%s", err, out)
	}
	if !strings.Contains(string(out), "REWRITE_BEHAVIOR_OK") {
		t.Fatalf("rewrite simulation did not complete: %s", out)
	}
}
