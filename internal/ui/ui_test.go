package ui

import (
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"regexp"
	"strings"
	"testing"
)

func fetchIndexHTML(t *testing.T) string {
	t.Helper()
	mux := http.NewServeMux()
	if err := Register(mux, "myworktree", nil); err != nil {
		t.Fatal(err)
	}
	srv := httptest.NewServer(mux)
	defer srv.Close()

	resp, err := http.Get(srv.URL + "/")
	if err != nil {
		t.Fatalf("GET / failed: %v", err)
	}
	body, err := io.ReadAll(resp.Body)
	_ = resp.Body.Close()
	if err != nil {
		t.Fatalf("GET / read body failed: %v", err)
	}
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("GET / status: %d", resp.StatusCode)
	}
	if !strings.Contains(resp.Header.Get("Content-Type"), "text/html") {
		t.Fatalf("GET / content-type mismatch: %q", resp.Header.Get("Content-Type"))
	}
	if len(body) == 0 {
		t.Fatalf("GET / body should not be empty")
	}
	return string(body)
}

// fetchStaticPath returns the raw body of any path served by the UI mux
// (used for the static renderer JS files, which must not be asserted
// against the HTML content-type).
func fetchStaticPath(t *testing.T, path string) string {
	t.Helper()
	mux := http.NewServeMux()
	if err := Register(mux, "myworktree", nil); err != nil {
		t.Fatal(err)
	}
	srv := httptest.NewServer(mux)
	defer srv.Close()

	resp, err := http.Get(srv.URL + path)
	if err != nil {
		t.Fatalf("GET %s failed: %v", path, err)
	}
	body, err := io.ReadAll(resp.Body)
	_ = resp.Body.Close()
	if err != nil {
		t.Fatalf("GET %s read body failed: %v", path, err)
	}
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("GET %s status: %d", path, resp.StatusCode)
	}
	if len(body) == 0 {
		t.Fatalf("GET %s body should not be empty", path)
	}
	return string(body)
}

func TestRegisterServesRootAndStatic(t *testing.T) {
	bodyText := fetchIndexHTML(t)
	if !strings.Contains(bodyText, "let terminalSessions = {}") {
		t.Fatalf("GET / should include per-instance terminal session state")
	}
	if !strings.Contains(bodyText, "function createTerminalSession(id)") {
		t.Fatalf("GET / should include per-instance terminal session creation")
	}
	if !strings.Contains(bodyText, "termDataDisposable") {
		t.Fatalf("GET / should include explicit terminal subscription cleanup")
	}
	if !strings.Contains(bodyText, "function renderTerminalSessions()") {
		t.Fatalf("GET / should include per-instance terminal rendering")
	}
	if !strings.Contains(bodyText, "function maybeDestroyInactiveStoppedSession(id)") {
		t.Fatalf("GET / should include stopped-session cleanup logic")
	}
	if !strings.Contains(bodyText, "function reportSessionError(session, prefix, err)") {
		t.Fatalf("GET / should include shared session error reporting")
	}

	mux := http.NewServeMux()
	if err := Register(mux, "myworktree", nil); err != nil {
		t.Fatal(err)
	}
	srv := httptest.NewServer(mux)
	defer srv.Close()

	resp, err := http.Get(srv.URL + "/static/index.html")
	if err != nil {
		t.Fatalf("GET /static/index.html failed: %v", err)
	}
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("GET /static/index.html status: %d", resp.StatusCode)
	}
}

func TestRegisterReturnsNotFoundForUnknownPath(t *testing.T) {
	mux := http.NewServeMux()
	if err := Register(mux, "myworktree", nil); err != nil {
		t.Fatal(err)
	}
	srv := httptest.NewServer(mux)
	defer srv.Close()

	resp, err := http.Get(srv.URL + "/unknown")
	if err != nil {
		t.Fatalf("GET /unknown failed: %v", err)
	}
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("expected 404 for unknown path, got %d", resp.StatusCode)
	}
}

func TestRegisterSubstitutesPageTitle(t *testing.T) {
	mux := http.NewServeMux()
	if err := Register(mux, "myproject", nil); err != nil {
		t.Fatal(err)
	}
	srv := httptest.NewServer(mux)
	defer srv.Close()

	resp, err := http.Get(srv.URL + "/")
	if err != nil {
		t.Fatalf("GET / failed: %v", err)
	}
	body, _ := io.ReadAll(resp.Body)
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("GET / status: %d", resp.StatusCode)
	}
	if !strings.Contains(string(body), "<title>myproject - myworktree</title>") {
		t.Fatalf("GET / should have substituted title, got: %s", string(body[:200]))
	}
	if strings.Contains(string(body), "<title>myworktree</title>") {
		t.Fatalf("GET / should not contain default title")
	}
}

func fetchStaticAsset(t *testing.T, path string) string {
	t.Helper()
	mux := http.NewServeMux()
	if err := Register(mux, "myworktree", nil); err != nil {
		t.Fatal(err)
	}
	srv := httptest.NewServer(mux)
	defer srv.Close()

	resp, err := http.Get(srv.URL + path)
	if err != nil {
		t.Fatalf("GET %s failed: %v", path, err)
	}
	body, err := io.ReadAll(resp.Body)
	_ = resp.Body.Close()
	if err != nil {
		t.Fatalf("GET %s read body failed: %v", path, err)
	}
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("GET %s status: %d", path, resp.StatusCode)
	}
	return string(body)
}

func TestIndexHTMLCoversMultiInstanceSwitching(t *testing.T) {
	bodyText := fetchIndexHTML(t)
	checks := []string{
		"function selectInstance(id)",
		"const previousID = state.activeInst;",
		"const session = ensureTerminalSession(id);",
		"if (hasLiveTTYConnection(id)) {",
		"renderTerminalSessions();",
		"function selectWorktree(id)",
		"maybeDestroyInactiveStoppedSession(previousID);",
	}
	for _, check := range checks {
		if !strings.Contains(bodyText, check) {
			t.Fatalf("GET / should include multi-instance switching hook %q", check)
		}
	}
}

func TestOpencodeWebRendererKeepsFrameAlive(t *testing.T) {
	mux := http.NewServeMux()
	if err := Register(mux, "myworktree", nil); err != nil {
		t.Fatal(err)
	}
	srv := httptest.NewServer(mux)
	defer srv.Close()

	resp, err := http.Get(srv.URL + "/static/kinds/opencode_web.js")
	if err != nil {
		t.Fatalf("GET /static/kinds/opencode_web.js failed: %v", err)
	}
	body, _ := io.ReadAll(resp.Body)
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("GET /static/kinds/opencode_web.js status: %d", resp.StatusCode)
	}
	js := string(body)

	checks := []string{
		// re-navigation is guarded by the dataset (ensureWebFrame pattern):
		// assigning the same src would reload the embedded page.
		"frame.dataset.instance !== id || frame.dataset.src !== src",
		// switching back to the same running instance keeps the page alive
		// (dataset.instance === session.id check in activate()).
		"frame.dataset.instance === session.id && frame.dataset.src",
		// a fresh iframe starts with an empty navigation cache, so the poll
		// navigates on first activation.
		"frame.dataset.instance = '';",
		"frame.dataset.src = '';",
		// stopping an instance releases its iframe immediately (no page state
		// to keep, no lingering memory); the next start builds a fresh one.
		"this.destroyFrame(session.id)",
		// per-instance iframe cache: each opencode-web instance keeps its own
		// iframe, so switching between two running instances only hides/shows
		// frames instead of re-navigating (multi-instance keep-alive).
		"this._frames = new Map()",
		"this._frames.set(id, frame)",
		"this._showOnly(frame)",
		// prune must never drop the currently-active instance's frame, or the
		// next activate() would rebuild it and reload a running instance.
		"live.has(id) || id === activeId",
		// cross-instance hidden-report isolation: only the currently-active
		// frame's postMessage drives the warning. This is the key guard against
		// cross-talk between multiple same-origin iframes.
		"event.source !== this._currentFrame.contentWindow",
		// prune/destroy must clear _currentFrame when they remove the active
		// frame, so a later message event can't trust a detached frame.
		"this._currentFrame === frame",
	}
	negativeChecks := []string{
		// activate() must NEVER reset the iframe to about:blank — that would
		// kill the loaded opencode page on every tab switch.
		"frame.src = 'about:blank'",
		// activate()/poll must never unconditionally assign src. If this line
		// reappears the page reloads on every tab switch.
		"frame.src = data.iframe_src;",
	}
	for _, check := range checks {
		if !strings.Contains(js, check) {
			t.Fatalf("opencode_web.js should include keep-alive hook %q", check)
		}
	}
	for _, check := range negativeChecks {
		if strings.Contains(js, check) {
			t.Fatalf("opencode_web.js should not contain unconditional navigation %q", check)
		}
	}
}

// TestWebRenderersStoppedSwitchHidesAllFrames pins the issue #96 fix:
// switching from a running web-ui instance to a STOPPED instance of the
// SAME kind never runs deactivate() (selectInstance only deactivates a
// DIFFERENT renderer), so each renderer's stopped / not-running branch
// must hide EVERY cached keep-alive iframe itself. Before the fix the
// previous instance's iframe kept hidden=false and stacked half/half
// under the stopped overlay (switching from a terminal instance did not
// reproduce because that cross-kind switch DID deactivate).
//
// Issue #120 refactored the dsh-web / opencode-web stopped branch to
// delegate to a shared teardownStopped(session) method (so the poll-driven
// syncStatus can reuse it for a cross-browser stop). The hide-all guarantee
// did not move, only its location: the branch must now call
// this.teardownStopped(session), and teardownStopped's body must contain
// this._showOnly(null). reasonix.js was NOT refactored, so it keeps its
// inline assertion — the same guarantee, checked where each file now keeps it.
func TestWebRenderersStoppedSwitchHidesAllFrames(t *testing.T) {
	// The dispatch-side invariant that makes the renderer-side fix
	// necessary: same-kind switches never deactivate. If this guard ever
	// goes away, re-evaluate whether the renderer branches still need
	// their own hide-all.
	indexJS := fetchStaticAsset(t, "/")
	if !strings.Contains(indexJS, "prevRenderer !== renderer") {
		t.Fatal("index.html selectInstance lost the prevRenderer !== renderer guard on deactivate()")
	}

	cases := map[string]string{
		// asset → opener of the stopped / not-running branch to inspect
		"/static/kinds/dsh_web.js":      "inst && inst.status === 'stopped'",
		"/static/kinds/opencode_web.js": "inst && inst.status === 'stopped'",
		"/static/kinds/reasonix.js":     "inst.status !== 'running' && inst.status !== 'starting'",
	}
	for asset, branchOpen := range cases {
		js := fetchStaticAsset(t, asset)
		start := strings.Index(js, branchOpen)
		if start < 0 {
			t.Fatalf("%s: stopped/not-running branch opener %q not found", asset, branchOpen)
		}
		end := strings.Index(js[start:], "return;")
		if end < 0 {
			t.Fatalf("%s: no return; after %q", asset, branchOpen)
		}
		branch := js[start : start+end]
		if asset == "/static/kinds/reasonix.js" {
			if !strings.Contains(branch, "this._showOnly(null)") {
				t.Fatalf("%s: stopped branch does not hide every cached frame (this._showOnly(null)) — switching to a stopped same-kind instance stacks iframes half/half (issue #96)", asset)
			}
			continue
		}
		// The refactored web renderers delegate the whole teardown — hide-all
		// included — to teardownStopped(session); assert both halves so a
		// branch that forgot the call, or a teardownStopped that dropped the
		// hide-all, both fail here (issue #120).
		if !strings.Contains(branch, "this.teardownStopped(") {
			t.Fatalf("%s: stopped branch no longer delegates to this.teardownStopped(session) — the poll-driven cross-browser self-heal (issue #120) shares this teardown", asset)
		}
		tdStart := strings.Index(js, "teardownStopped(session) {")
		if tdStart < 0 {
			t.Fatalf("%s: teardownStopped(session) method not found", asset)
		}
		tdBody := js[tdStart:]
		if e := strings.Index(tdBody, "\n    }"); e >= 0 {
			tdBody = tdBody[:e]
		}
		if !strings.Contains(tdBody, "this._showOnly(null)") {
			t.Fatalf("%s: teardownStopped does not hide every cached frame (this._showOnly(null)) — switching to a stopped same-kind instance stacks iframes half/half (issue #96)", asset)
		}
	}
}

// TestWebRenderersPollSelfHealStoppedInstance pins the issue #120 shape:
// a stop performed in ANOTHER browser never changes this page's selection,
// so selectInstance()'s early-return keeps activate() from re-running and
// the embedded iframe stayed alive forever behind a stopped badge. Three
// anchors make the self-heal exist and stay wired:
//
//   - index.html defines syncActiveWebStatus() and refresh() calls it right
//     after render() — the poll tick is what observes the status change;
//   - both web renderers define syncStatus(inst) (the entering-terminal-
//     bucket gate, same semantics as syncActiveTerminalStatus) and
//     teardownStopped(session) (the single teardown truth they share with
//     activate()'s stopped branch);
//   - each renderer's 1.5s scope-poll tick checks the instance status
//     BEFORE issuing its fetch and returns without rescheduling once the
//     instance is gone or terminal (the scope endpoint answers 200 for a
//     stopped instance forever, so nothing else ever ends that poll).
//
// The behavioural half lives in testdata/web_status_sync.test.mjs
// (TestWebStatusSyncHandling); this pins the shipped text so the wiring
// cannot silently vanish while the node cases keep passing on a copy.
func TestWebRenderersPollSelfHealStoppedInstance(t *testing.T) {
	indexJS := fetchStaticAsset(t, "/")
	for _, anchor := range []string{
		"function syncActiveWebStatus() {",
		// Dispatch guards: web kinds only, renderer must expose syncStatus,
		// and a renderer error must never break the refresh loop.
		"WEB_UI_KINDS.includes(inst.kind)",
		"typeof renderer.syncStatus !== 'function'",
		"renderer.syncStatus(inst);",
	} {
		if !strings.Contains(indexJS, anchor) {
			t.Fatalf("GET / should include the issue #120 self-heal hook %q", anchor)
		}
	}
	// refresh() must call it on the tick, right after render() — sliced to
	// refresh's own body so an order check cannot match another caller.
	refreshStart := strings.Index(indexJS, "async function refresh() {")
	if refreshStart < 0 {
		t.Fatal("GET /: refresh() not found")
	}
	refreshBody := indexJS[refreshStart:]
	if e := strings.Index(refreshBody, "\n        }"); e >= 0 {
		refreshBody = refreshBody[:e]
	}
	renderAt := strings.Index(refreshBody, "render();")
	syncAt := strings.Index(refreshBody, "syncActiveWebStatus();")
	if renderAt < 0 || syncAt < 0 || syncAt < renderAt {
		t.Fatal("refresh() must call syncActiveWebStatus() immediately after render() — the poll tick is what observes a cross-browser status change (issue #120)")
	}
	for _, asset := range []string{"/static/kinds/dsh_web.js", "/static/kinds/opencode_web.js"} {
		js := fetchStaticAsset(t, asset)
		for _, anchor := range []string{
			"syncStatus(inst) {",
			"teardownStopped(session) {",
			"this.teardownStopped(session);",
			"this.teardownStopped({ id: inst.id, instance: inst });",
			// The gate: teardown only on ENTRY into the terminal bucket.
			"enteringTerminalBucket",
			// The scope poll must consult the polled status itself.
			"window.state.instances",
			"isInstanceTerminalStatus",
		} {
			if !strings.Contains(js, anchor) {
				t.Fatalf("%s should include the issue #120 self-heal hook %q", asset, anchor)
			}
		}
		// The status check must sit BEFORE the scope fetch (a check after
		// it would still request the dead backend every 1.5s forever).
		// Anchored on the METHOD DEFINITION header — activate()'s call
		// site names the same method and would slice to nothing.
		defHeader := "_startScopeMonitoring(session, dshPanel, dshWarning) {"
		if asset == "/static/kinds/opencode_web.js" {
			defHeader = "_startScopeMonitoring(session, ocPanel, ocWarning) {"
		}
		scopeAt := strings.Index(js, defHeader)
		if scopeAt < 0 {
			t.Fatalf("%s: _startScopeMonitoring definition %q not found", asset, defHeader)
		}
		scopeBody := js[scopeAt:]
		if e := strings.Index(scopeBody, "\n    }"); e >= 0 {
			scopeBody = scopeBody[:e]
		}
		statusAt := strings.Index(scopeBody, "isInstanceTerminalStatus")
		fetchAt := strings.Index(scopeBody, "fetch(")
		if statusAt < 0 || fetchAt < 0 || statusAt > fetchAt {
			t.Fatalf("%s: the scope tick must check the instance status BEFORE its fetch and stop rescheduling when stopped (issue #120)", asset)
		}
		if !strings.Contains(scopeBody, "setTimeout(tick, 1500);") {
			t.Fatalf("%s: the scope poll must stay armed for live instances (setTimeout(tick, 1500))", asset)
		}
		// The self-stop branch must run the symmetric cleanup, not just
		// clear the bar and skip rescheduling: dsh's _stopMonitoring()
		// nulls the abort handle and resets the warning-state flags, so a
		// poll that died on its own lands in the same final state as the
		// teardownStopped path (issue #120 review; opencode already did
		// this via _stopScopeMonitoring).
		stopCall := "this._stopMonitoring();"
		if asset == "/static/kinds/opencode_web.js" {
			stopCall = "this._stopScopeMonitoring();"
		}
		selfStopAt := strings.Index(scopeBody, "if (terminalNow) {")
		if selfStopAt < 0 {
			t.Fatalf("%s: the scope tick's terminalNow self-stop branch not found (issue #120)", asset)
		}
		if e := strings.Index(scopeBody[selfStopAt:], "return;"); e >= 0 {
			if at := strings.Index(scopeBody[selfStopAt:selfStopAt+e], stopCall); at < 0 {
				t.Fatalf("%s: the scope tick's self-stop branch must call %s before returning — a dead poll may not keep its abort handle or its warning state (issue #120 review)", asset, stopCall)
			}
		} else {
			t.Fatalf("%s: the self-stop branch must return without rescheduling (issue #120)", asset)
		}
		// Both syncStatus bodies classify through ONE local arrow reused
		// for isTerminal AND prevTerminal (review #120 nit; behaviour
		// identical to the duplicated expression it replaced).
		if !strings.Contains(js, "const classifyTerminal = (s) =>") {
			t.Fatalf("%s: syncStatus must classify through one local classifyTerminal arrow used for both classifications", asset)
		}
		if asset == "/static/kinds/dsh_web.js" {
			// dsh-only missing-dsh recovery (issue #120 review): the
			// teardown aborts the ready poll — the dialog's only other
			// driver — so syncStatus itself must re-check the info
			// endpoint, gated on the failed-only backend contract.
			for _, anchor := range []string{"missing_dsh", "_showMissingDialog", "inst.status === 'failed'"} {
				if !strings.Contains(js, anchor) {
					t.Fatalf("%s should include the issue #120 missing-dsh recovery anchor %q", asset, anchor)
				}
			}
			// Containment + order inside the syncStatus body: teardown
			// first, then the one-shot fetch, then _showMissingDialog —
			// a check living anywhere the ready poll still covers would
			// not fix the teardown-vs-tick race.
			syncAt := strings.Index(js, "syncStatus(inst) {")
			if syncAt < 0 {
				t.Fatal("/static/kinds/dsh_web.js: syncStatus(inst) definition not found")
			}
			syncBody := js[syncAt:]
			if e := strings.Index(syncBody, "\n    }"); e >= 0 {
				syncBody = syncBody[:e]
			}
			tdAt := strings.Index(syncBody, "this.teardownStopped({ id: inst.id, instance: inst });")
			recoveryAt := strings.Index(syncBody, "fetch('/api/instances/dsh?id='")
			dialogAt := strings.Index(syncBody, "_showMissingDialog")
			if tdAt < 0 || recoveryAt < 0 || dialogAt < 0 || tdAt > recoveryAt || recoveryAt > dialogAt {
				t.Fatal("/static/kinds/dsh_web.js: syncStatus must fire the one-shot missing_dsh fetch AFTER the teardown, reaching _showMissingDialog from within its own body (issue #120 review)")
			}
			// The response-time guard (issue #120 review): the fetch is
			// fire-and-forget, so its response may land after the user
			// switched away — the dialog must then be dropped, which is
			// what the active-id check inside the response handler does.
			guardAt := strings.Index(syncBody, "window.state.activeInst === inst.id")
			if guardAt < 0 || guardAt > dialogAt {
				t.Fatal("/static/kinds/dsh_web.js: the one-shot response handler must re-check window.state.activeInst === inst.id before opening the dialog (issue #120 review)")
			}
			continue
		}
		// opencode-web has NO missing-dsh dialog: the dsh-only recovery
		// must not leak into this renderer (its failed entry tears down
		// and fetches nothing).
		if strings.Contains(js, "missing_dsh") || strings.Contains(js, "_showMissingDialog") {
			t.Fatal("/static/kinds/opencode_web.js must not reference missing_dsh / _showMissingDialog — the recovery dialog is dsh-only (issue #120 review)")
		}
	}
}

// sliceJSFunction returns index.html's top-level `function name(...) {…}`,
// from the declaration to the start of the next (async) function declared
// at the SAME indentation. The indent is read off the declaration line
// rather than hardcoded: a re-indented script block must not silently
// extend every slice to the end of the file, where assertions would match
// text belonging to a different function and keep passing.
func sliceJSFunction(t *testing.T, js, name string) string {
	t.Helper()
	start := strings.Index(js, "function "+name+"(")
	if start < 0 {
		t.Fatalf("index.html: function %s not found", name)
	}
	lineStart := strings.LastIndex(js[:start], "\n") + 1
	indent := js[lineStart:start]
	if strings.Trim(indent, " \t") != "" {
		t.Fatalf("index.html: function %s is not declared at line start — cannot derive a slice end marker", name)
	}
	rest := js[start:]
	end := len(rest)
	for _, marker := range []string{"\n" + indent + "function ", "\n" + indent + "async function "} {
		if i := strings.Index(rest, marker); i >= 0 && i < end {
			end = i
		}
	}
	return rest[:end]
}

// stripCSSComments removes /* */ comments so selector scans see only real
// rules. index.html's comments mention selectors like :hover verbatim (the
// R11 notes do), and a containment scan must not mistake comment text for a
// rule outside its media block.
func stripCSSComments(css string) string {
	var b strings.Builder
	b.Grow(len(css))
	for {
		open := strings.Index(css, "/*")
		if open < 0 {
			b.WriteString(css)
			return b.String()
		}
		b.WriteString(css[:open])
		css = css[open:]
		close := strings.Index(css, "*/")
		if close < 0 {
			// Unterminated comment: drop the rest rather than scanning
			// comment text as rules.
			return b.String()
		}
		css = css[close+2:]
	}
}

// TestEmptyWorktreeSwitchHidesAllWebPanels pins the issue #101 fix: switching
// to a worktree with ZERO instances must not leave the previous worktree's
// dsh-web iframe on screen. Three defects combined: (A) renderWorkspace's
// no-active-instance branch hid only the opencode/reasonix panels and forgot
// #dsh-panel (whose CSS keeps it full-size until [hidden] is set); (B)
// selectWorktree never called the previous renderer's deactivate()
// (maybeDestroyInactiveStoppedSession's terminal-bucket guard returns early
// for web instances), so the dsh-web panel stayed visible AND its background
// polling kept running; (C) selectInstance(null) early-returns on the empty
// worktree path, so its panel-hiding lines never execute there. The fix
// extracts one hideWebPanels() helper as the single source of truth used by
// renderWorkspace and selectInstance, and makes worktree switches deactivate the
// previous renderer through the shared deactivateInstanceRenderer helper
// (without selectInstance's prevRenderer !== renderer guard — a worktree
// switch is always a full leave), synchronously, in BOTH switch entry
// points: selectWorktree and selectWorktreeByID (create/import flows).
func TestEmptyWorktreeSwitchHidesAllWebPanels(t *testing.T) {
	indexJS := fetchStaticAsset(t, "/")

	// (A) One helper hides ALL three web panels — adding a kind and hiding
	// only the older panels is exactly how #dsh-panel was forgotten.
	helper := sliceJSFunction(t, indexJS, "hideWebPanels")
	for _, panelID := range []string{"'opencode-panel'", "'reasonix-panel'", "'dsh-panel'"} {
		if !strings.Contains(helper, panelID) {
			t.Fatalf("hideWebPanels() does not hide %s — a forgotten panel stays visible over the empty state (issue #101)", panelID)
		}
	}

	// renderWorkspace's no-active-instance branch must hide every web panel
	// through the shared helper (this is the branch the empty worktree lands
	// on, and it is the visual backstop for every path into it).
	rw := sliceJSFunction(t, indexJS, "renderWorkspace")
	emptyStart := strings.Index(rw, `empty.style.display = "block";`)
	if emptyStart < 0 {
		t.Fatal("renderWorkspace lost its no-active-instance branch (empty.style.display = \"block;\")")
	}
	emptyEnd := strings.Index(rw[emptyStart:], "renderTerminalSessions();")
	if emptyEnd < 0 {
		t.Fatal("renderWorkspace empty branch does not end in renderTerminalSessions(); anymore — re-check the slice")
	}
	emptyBranch := rw[emptyStart : emptyStart+emptyEnd]
	if !strings.Contains(emptyBranch, "hideWebPanels()") {
		t.Fatal("renderWorkspace's empty branch does not hide all web panels — a previous worktree's web iframe covers the empty state (issue #101)")
	}
	// The non-web active-instance branch hides panels too; it must use the
	// same helper rather than an inline subset that can forget a kind.
	if !strings.Contains(rw[:emptyStart], "hideWebPanels()") {
		t.Fatal("renderWorkspace's non-web branch no longer hides web panels via hideWebPanels()")
	}

	// selectInstance hides panels before dispatching to the kind renderer;
	// it must go through the same helper, not an inline copy that can drift.
	si := sliceJSFunction(t, indexJS, "selectInstance")
	if !strings.Contains(si, "hideWebPanels()") {
		t.Fatal("selectInstance no longer hides web panels via hideWebPanels() — the two call sites can drift (issue #101)")
	}

	// selectInstance's hide must live INSIDE the `if (id && renderer)`
	// branch, not run unconditionally before it (issue #120 follow-up):
	// render() above may have nested-activated a renderer already — deleting
	// the active instance runs selectInstance(null) → render() → renderTabs'
	// auto-select → selectInstance(B) → activate(B) shows B's panel — and an
	// unconditional hide here would erase it: a black workspace with no
	// self-heal (the 2s poll never refills panel visibility and re-clicking
	// B's tab dies on the same-id early-return; another tab or a reload
	// still brings it back). The null-id / no-instances case is hidden by
	// renderWorkspace's empty branch (asserted above) and terminal kinds by
	// its non-web branch (asserted above).
	siHeader := strings.Index(si, "if (id && renderer) {")
	if siHeader < 0 {
		t.Fatal("selectInstance lost its `if (id && renderer)` dispatch branch")
	}
	siHide := strings.Index(si, "hideWebPanels();")
	if siHide < siHeader {
		t.Fatal("selectInstance's hideWebPanels() runs before the renderer dispatch again — it erases the panel the nested auto-select showed after a delete (issue #120 follow-up)")
	}
	// IMMEDIATELY before the dispatch, not slid behind a kind-specific `if`
	// inside the branch — a hide that only covers one kind leaves the other
	// two web kinds stacked half/half while the positional anchor above
	// still passes (PR #126 review).
	if rest := strings.TrimLeft(si[siHide+len("hideWebPanels();"):], " \t\n"); !strings.HasPrefix(rest, "renderer.activate(") {
		t.Fatal("selectInstance's hideWebPanels() is no longer immediately before renderer.activate() — it slid behind a kind-specific guard and the mutual exclusion went partial (issue #120 follow-up)")
	}

	// (B) selectWorktree must deactivate the previous instance's renderer so
	// web kinds hide their panel and stop polling. It must NOT carry
	// selectInstance's prevRenderer !== renderer guard: a worktree switch is
	// always a full leave, and the guard would skip deactivation whenever the
	// auto-selected instance of the target worktree shares the kind.
	// Assert the POSITION, not just presence: render() → renderWorkspace
	// reads the just-nulled state.activeInst and paints the empty state, so
	// a deactivate() that ran after render() would flash that empty state
	// over the still-visible old iframe — the exact #101 symptom class.
	sw := sliceJSFunction(t, indexJS, "selectWorktree")
	deactIdx := strings.Index(sw, "deactivateInstanceRenderer(previousID)")
	if deactIdx < 0 {
		t.Fatal("selectWorktree does not deactivate the previous renderer — web iframes stay visible and keep polling across worktree switches (issue #101)")
	}
	assignIdx := strings.Index(sw, "state.activeWT = id;")
	if assignIdx < 0 || deactIdx > assignIdx {
		t.Fatal("selectWorktree deactivates the previous renderer only after state.activeWT = id — render() then paints the new worktree's empty state over the still-visible old iframe (issue #101)")
	}
	if strings.Contains(sw, "prevRenderer !== renderer") {
		t.Fatal("selectWorktree must deactivate unconditionally — the prevRenderer !== renderer guard skips same-kind leaves (issue #101)")
	}

	// The shared leave helper deactivates through the renderer registry and
	// skips unresolvable ids: on a concurrent deletion the 'pty' fallback
	// would deactivate the wrong renderer while a web renderer is on screen.
	leave := sliceJSFunction(t, indexJS, "deactivateInstanceRenderer")
	if !strings.Contains(leave, ".deactivate()") {
		t.Fatal("deactivateInstanceRenderer does not call renderer.deactivate() (issue #101)")
	}
	if !strings.Contains(leave, "if (!inst) return;") {
		t.Fatal("deactivateInstanceRenderer lost its unresolvable-id skip — the 'pty' fallback deactivates the wrong renderer (issue #101 review)")
	}

	// selectWorktreeByID (the create/import flows' switch) must run the
	// same synchronous leave cleanup: without it the old worktree's web
	// iframe stays visible — and keeps polling — over the new worktree
	// until the refresh round-trip finishes.
	swb := sliceJSFunction(t, indexJS, "selectWorktreeByID")
	for _, want := range []string{"deactivateInstanceRenderer(previousID)", "state.activeInst = null", "render();"} {
		if !strings.Contains(swb, want) {
			t.Fatalf("selectWorktreeByID is missing %q — it bypasses the worktree-switch cleanup and strands the previous renderer (issue #101 review)", want)
		}
	}
	// Same ordering pin as selectWorktree's above: the deactivate must run
	// BEFORE render(), or render() paints the new worktree's empty state
	// over the still-visible old iframe — presence alone does not pin this.
	swbDeact := strings.Index(swb, "deactivateInstanceRenderer(previousID)")
	swbRender := strings.Index(swb, "render();")
	if swbDeact < 0 || swbRender < 0 || swbDeact > swbRender {
		t.Fatal("selectWorktreeByID deactivates the previous renderer only after render() — the empty state flashes over the still-visible old iframe (issue #101 review)")
	}
}

// TestWorktreeDirtyDeleteDialog pins the issue #102 UI contract: a
// dirty-worktree delete refusal is STRUCTURED (error code "worktree_dirty"
// plus a breakdown), and the dashboard must open the details dialog —
// summary, collapsible full git status output, the gitignored force-risk
// warning — with the force-delete exit behind a two-step confirm, instead
// of the old flat alert that hid what force would destroy.
func TestWorktreeDirtyDeleteDialog(t *testing.T) {
	indexJS := fetchStaticAsset(t, "/")

	checks := []struct{ what, anchor string }{
		{"the dirty-details dialog exists", `id="modal-wt-dirty"`},
		{"deleteWorktree matches the structured refusal code", `e.body.error === "worktree_dirty"`},
		{"the refusal routes to the dialog", "showWorktreeDirtyDialog(id, e.body)"},
		{"the force exit resends with force:true", `{ id, force: true }`},
		{"the collapsible full git status output is rendered", "wt-dirty-porcelain"},
		{"the gitignored force-risk warning is rendered", "wt-dirty-ignored"},
		{"the force delete is a two-step confirm", "Confirm force delete?"},
		{"the confirm state resets per open", `btn.dataset.confirm = ""`},
		{"a failed force delete shows the human summary, not the raw error code", `(e && e.body && e.body.message) || (e && e.message)`},
		{"Esc (cancel) resets the dangling dialog state", `.addEventListener('cancel', () => {`},
		{"a clean delete surfaces the destroyed gitignored count", "res.ignored_destroyed > 0"},
	}
	for _, c := range checks {
		if !strings.Contains(indexJS, c.anchor) {
			t.Fatalf("index.html: %s — anchor %q not found (issue #102)", c.what, c.anchor)
		}
	}
}

// TestSidebarCollapseAnchors pins the issue #99 desktop sidebar collapse
// contract (PR1 of docs/plans/feature-v0.6.0-webui-sidebar-responsive/):
// the collapsed state is a pure-CSS class on #app with zero JS width
// math, the preference persists under the namespaced localStorage key, and
// the toggle button lives inside #header BEFORE #tabs-container — the
// header is the first child of #main, so the button must never drift into
// the sidebar or after the tabs it is supposed to lead.
func TestSidebarCollapseAnchors(t *testing.T) {
	bodyText := fetchIndexHTML(t)

	// R1: collapsed state renders through one class; #sidebar width goes to
	// zero with overflow hidden and #main's flex:1 (untouched here) fills
	// the space. JS must not measure or assign any pixel width. The three
	// declarations are asserted INSIDE the collapsed rule block — a bare
	// full-file Contains would keep passing on unrelated rules (index.html
	// has other `width: 0;` hits), i.e. it would anchor nothing.
	ruleStart := strings.Index(bodyText, "#app.collapsed #sidebar {")
	if ruleStart < 0 {
		t.Fatal("GET / lost the #app.collapsed #sidebar rule (issue #99 R1)")
	}
	ruleEnd := strings.Index(bodyText[ruleStart:], "\n        }")
	if ruleEnd < 0 {
		t.Fatal("GET / #app.collapsed #sidebar rule has no closing brace")
	}
	rule := bodyText[ruleStart : ruleStart+ruleEnd]
	for _, decl := range []string{"width: 0;", "overflow: hidden;", "border-right: none;", "visibility: hidden;"} {
		if !strings.Contains(rule, decl) {
			t.Fatalf("GET / #app.collapsed #sidebar rule should declare %q (issue #99 R1)", decl)
		}
	}
	// Minimal literal anchors: equivalent rewrites (computed styles, inline
	// style objects, styleSheet.insertRule) can bypass every string below,
	// so the semantic constraint — JS does no sidebar width math (plan
	// §3.1) — is held by review discipline, not by this list.
	for _, forbidden := range []string{
		// The class is the single source of truth (§3.1 of the plan): any
		// JS width read/write would re-introduce the dual-state the plan
		// forbids.
		"sidebar.style.width",
		"sidebar.offsetWidth",
		"getComputedStyle(document.getElementById(\"sidebar\"))",
		// The chevron icons flip via toggleAttribute("hidden", …): a plain
		// `.hidden = …` on an <svg> is an inert expando (SVGElement has no
		// hidden IDL property), which shipped as a real bug in PR1 review.
		`getElementById("sidebar-toggle-icon-left").hidden`,
		`getElementById("sidebar-toggle-icon-right").hidden`,
	} {
		if strings.Contains(bodyText, forbidden) {
			t.Fatalf("GET / must not use %q — see the comment above the forbidden list (issue #99 R1/R2)", forbidden)
		}
	}

	// R3: namespaced persistence key — the file's first localStorage use.
	if !strings.Contains(bodyText, `"mw.ui.sidebarCollapsed"`) {
		t.Fatal("GET / should persist the sidebar state under the namespaced key \"mw.ui.sidebarCollapsed\" (issue #99 R3)")
	}

	// R2: the button is a child of #header sitting before #tabs-container.
	// Position, not mere presence: the level hierarchy (#sidebar and #main
	// as siblings under #app, header inside #main) is a hard design
	// constraint of the plan (§3.2) — a button parked after the tabs, or
	// inside the sidebar, would violate it while every Contains check
	// still passed.
	headerAt := strings.Index(bodyText, `<div id="header">`)
	if headerAt < 0 {
		t.Fatal("GET / lost the #header block")
	}
	btnAt := strings.Index(bodyText[headerAt:], `id="sidebar-toggle-btn"`)
	tabsAt := strings.Index(bodyText[headerAt:], `id="tabs-container"`)
	if btnAt < 0 {
		t.Fatal("GET / should include the sidebar toggle button inside #header (issue #99 R2)")
	}
	if tabsAt < 0 || btnAt > tabsAt {
		t.Fatal("GET / should place the sidebar toggle button BEFORE #tabs-container inside #header (issue #99 R2, §3.2 hierarchy)")
	}
	// Both chevron states live in the shipped DOM and flip via
	// toggleAttribute("hidden") — the initial markup (left visible, right
	// hidden) matches the expanded state. aria-hidden keeps both icons off
	// the accessibility tree in either state. The hidden ATTRIBUTE has an
	// explicit CSS consumer: the UA's [hidden] { display: none } hint is
	// namespaced to HTML elements, so svg[hidden] would still render
	// without this rule (head-Chrome-verified) and the attribute path would
	// spin in place — anchor the rule so it cannot be deleted quietly.
	for _, icon := range []string{`id="sidebar-toggle-icon-left"`, `id="sidebar-toggle-icon-right"`} {
		if !strings.Contains(bodyText, icon) {
			t.Fatalf("GET / should include both toggle chevron states, missing %q (issue #99 R2)", icon)
		}
	}
	for _, check := range []string{
		`aria-label="Toggle Sidebar"`,
		`aria-controls="sidebar"`,
		`aria-expanded="true"`,
		"#sidebar-toggle-btn svg[hidden] { display: none !important; }",
		"toggleAttribute(\"hidden\", collapsed)",
		"toggleAttribute(\"hidden\", !collapsed)",
		`setAttribute("aria-expanded", collapsed ? "false" : "true")`,
	} {
		if !strings.Contains(bodyText, check) {
			t.Fatalf("GET / should include the toggle button a11y/attribute hook %q (issue #99 R2)", check)
		}
	}

	// R4: the first document-level keydown with its input guard, repeat
	// guard and case-insensitive match, registered once.
	//
	// The count check is an intentional tripwire, not an accident of this
	// feature: the plan's fact table (PRD-CHANGE §1.4) records exactly one
	// document-level keydown for this file. A future PR that adds an
	// unrelated global shortcut will trip it — the right response is to
	// update THIS test and the plan's fact table together, not to point
	// the message at whatever issue happened to add the second listener.
	if strings.Count(bodyText, `document.addEventListener("keydown"`) != 1 {
		t.Fatal("GET / should register exactly one document-level keydown listener — intentional tripwire: the plan fact table (PRD-CHANGE §1.4) assumes a single global keydown; adding an unrelated one must update this test AND the plan document")
	}
	for _, check := range []string{
		"function handleSidebarToggleKeydown(event)",
		"if (event.repeat) return;",
		"if (event.altKey) return;",
		`if (!(event.metaKey || event.ctrlKey)) return;`,
		`event.key.toLowerCase() !== "b"`,
		// PR #110 review (B3): dialog-ancestor guard first — a modal up
		// front must not have the sidebar toggled (invisibly, persisted)
		// behind it.
		`target.closest("dialog")`,
		`target.closest("input, textarea, [contenteditable]")`,
	} {
		if !strings.Contains(bodyText, check) {
			t.Fatalf("GET / should include the sidebar shortcut guard %q (issue #99 R4)", check)
		}
	}

	// R12: the fit re-runs after the class flip, on a rAF boundary, so the
	// xterm measures its post-reflow box.
	apply := sliceJSFunction(t, bodyText, "applySidebarCollapsed")
	clsAt := strings.Index(apply, `classList.toggle("collapsed", collapsed)`)
	fitAt := strings.Index(apply, "requestAnimationFrame(refitActiveTerminal);")
	if clsAt < 0 || fitAt < 0 || clsAt > fitAt {
		t.Fatal("applySidebarCollapsed must flip the .collapsed class BEFORE scheduling the terminal refit (issue #99 R12)")
	}
	if !strings.Contains(bodyText, "function refitActiveTerminal()") ||
		!strings.Contains(bodyText, "session.fitAddon.fit();") {
		t.Fatal("GET / should refit the active xterm via xterm-addon-fit after a collapse toggle (issue #99 R12)")
	}

	// R13: the two sidebar-local fold states must stay orthogonal — the
	// collapse code path never touches the git accordion variable. The
	// slice ends at the next same-indent declaration, so it spans exactly
	// applySidebarCollapsed's body.
	if strings.Contains(apply, "gitExpandedSection") {
		t.Fatal("applySidebarCollapsed must not touch gitExpandedSection — the sidebar collapse and the staged/unstaged accordion are orthogonal states (issue #99 R13)")
	}
}

// TestViewportAndHeightAnchors pins the issue #100 L1 viewport/height
// contract (PR2 of docs/plans/feature-v0.6.0-webui-sidebar-responsive/):
// the viewport meta opts into cover fitting, #app's height basis is 100dvh
// with a 100vh fallback that must be declared FIRST (old Safari progressive
// enhancement), the width is 100% instead of 100vw, the notch / Home
// Indicator safe areas are consumed via env(safe-area-inset-*), and the iOS
// soft keyboard is handled by pinning #app to window.visualViewport with a
// graceful no-op where the API does not exist. R12 with PR1: R5 owns #app's
// height/width basis while PR1's collapse owns #sidebar's flex-row share —
// two disjoint rule blocks on the same element, asserted here to coexist.
func TestViewportAndHeightAnchors(t *testing.T) {
	bodyText := fetchIndexHTML(t)

	// R5: the viewport meta opts into cover fitting — the prerequisite for
	// env(safe-area-inset-*) returning anything but 0. Anchored to the meta
	// tag itself, not the bare string (a stray mention in a comment would
	// satisfy a full-file Contains while the shipped meta stayed broken).
	metaStart := strings.Index(bodyText, `<meta name="viewport" content="`)
	if metaStart < 0 {
		t.Fatal("GET / lost the viewport meta tag (issue #100 L1 R5)")
	}
	metaEnd := strings.IndexByte(bodyText[metaStart:], '>')
	if metaEnd < 0 {
		t.Fatal("GET / viewport meta tag is not closed")
	}
	meta := bodyText[metaStart : metaStart+metaEnd]
	for _, want := range []string{"width=device-width", "initial-scale=1", "viewport-fit=cover"} {
		if !strings.Contains(meta, want) {
			t.Fatalf("GET / viewport meta should carry %q (issue #100 L1 R5)", want)
		}
	}

	// R5: #app's height/width basis. The declarations are asserted INSIDE
	// the #app rule — index.html holds other 100% hits (form inputs, tab
	// containers), so a bare full-file Contains would anchor nothing.
	ruleStart := strings.Index(bodyText, "#app {\n")
	if ruleStart < 0 {
		t.Fatal("GET / lost the #app rule (issue #100 L1 R5)")
	}
	ruleEnd := strings.Index(bodyText[ruleStart:], "\n        }")
	if ruleEnd < 0 {
		t.Fatal("GET / #app rule has no closing brace")
	}
	rule := bodyText[ruleStart : ruleStart+ruleEnd]
	for _, decl := range []string{"height: 100vh;", "width: 100%;"} {
		if !strings.Contains(rule, decl) {
			t.Fatalf("GET / #app rule should declare %q (issue #100 L1 R5)", decl)
		}
	}
	if strings.Contains(rule, "width: 100vw;") {
		t.Fatal("GET / #app rule must not use width: 100vw — vw counts the scrollbar gutter and overflows horizontally (issue #100 L1 R5)")
	}
	// The safe-area padding is what the viewport-fit=cover meta above buys.
	// All four insets, so both the notch (left/right in landscape) and the
	// Home Indicator (bottom) are covered; desktop insets are all 0.
	if !strings.Contains(rule, "env(safe-area-inset-") {
		t.Fatal("GET / #app rule should consume env(safe-area-inset-*) for the notch / Home Indicator (issue #100 L1 R5)")
	}
	for _, inset := range []string{"safe-area-inset-top", "safe-area-inset-right", "safe-area-inset-bottom", "safe-area-inset-left"} {
		if !strings.Contains(rule, inset) {
			t.Fatalf("GET / #app rule should consume %s (issue #100 L1 R5)", inset)
		}
	}

	// R5: the dvh upgrade lives in an @supports block and must come AFTER
	// the 100vh fallback in the source. The mechanism is last-declaration-
	// wins for same-origin equal-specificity declarations: browsers that
	// understand dvh take the @supports block over the fallback, and
	// browsers without dvh (old Safari) evaluate the condition to false
	// and drop the WHOLE block, keeping the 100vh baseline. A swapped order
	// would not strand old Safari — it would silently regress modern
	// browsers to static 100vh, reviving the URL-bar jump this layer
	// exists to kill.
	supportsAt := strings.Index(bodyText, "@supports (height: 100dvh) {")
	if supportsAt < 0 {
		t.Fatal("GET / lost the @supports (height: 100dvh) block (issue #100 L1 R5)")
	}
	supportsEnd := strings.Index(bodyText[supportsAt:], "\n        }")
	if supportsEnd < 0 {
		t.Fatal("GET / @supports (height: 100dvh) block has no closing brace")
	}
	supports := bodyText[supportsAt : supportsAt+supportsEnd]
	if !strings.Contains(supports, "height: 100dvh;") {
		t.Fatal("GET / @supports block should upgrade #app to height: 100dvh (issue #100 L1 R5)")
	}
	fallbackAt := strings.Index(rule, "height: 100vh;")
	if fallbackAt < 0 || ruleStart+fallbackAt > supportsAt {
		t.Fatal("GET / should declare the 100vh fallback BEFORE the @supports (height: 100dvh) block — modern browsers resolve last-wins and take the dvh block, old Safari drops the whole @supports block; swapped, modern browsers would silently stay on static 100vh (issue #100 L1 R5)")
	}
	// R12 with PR1: the collapse mechanism (#app.collapsed #sidebar) is a
	// different rule block on a different property — it must survive this
	// PR untouched, and this PR must not have bolted a width/height onto
	// the collapsed rule either.
	collapsedStart := strings.Index(bodyText, "#app.collapsed #sidebar {")
	if collapsedStart < 0 {
		t.Fatal("GET / lost the PR1 #app.collapsed #sidebar rule (issue #99 R1)")
	}
	collapsedEnd := strings.Index(bodyText[collapsedStart:], "\n        }")
	if collapsedEnd < 0 {
		t.Fatal("GET / #app.collapsed #sidebar rule has no closing brace")
	}
	collapsed := bodyText[collapsedStart : collapsedStart+collapsedEnd]
	if strings.Contains(collapsed, "100vh") || strings.Contains(collapsed, "100dvh") || strings.Contains(collapsed, "100%") {
		t.Fatal("GET / #app.collapsed #sidebar rule must not carry viewport-height/width declarations — PR1 owns the flex-row share, PR2 owns the height basis; the two stay independent (R12)")
	}

	// §3.5/§8: interactive-widget=resizes-content stays OUT of the viewport
	// meta — it is Chromium-only and the plan explicitly defers the soft
	// keyboard to the visualViewport path below instead. (Scoped to the meta
	// tag: the directive only means anything there, and the shipped JS
	// comment that names the decision must not trip this.)
	if strings.Contains(meta, "interactive-widget") {
		t.Fatal("GET / viewport meta must not introduce interactive-widget=resizes-content — Chromium-only, deferred by plan §3.5/§8 (issue #100 L1)")
	}

	// R6: the visualViewport wiring, sliced to the shipped functions.
	// Degradation is the contract: no API -> no listeners -> no-op. The
	// occlusion sentinel is documentElement.clientHeight (not
	// window.innerHeight — vv.height excludes scrollbars pinned to the
	// visual viewport while innerHeight includes the classic gutter), and
	// pinch/double-tap zoom opts out of pinning entirely: vv.height shrinks
	// with the scale and fires resize, so without the short-circuit a pinch
	// would collapse the whole UI toward half height.
	apply := sliceJSFunction(t, bodyText, "applyVisualViewportHeight")
	for _, check := range []string{
		"window.visualViewport",
		"document.documentElement.clientHeight",
		"vv.offsetTop + vv.height",
		"vv.scale && Math.abs(vv.scale - 1) > 0.01",
		`app.style.height = ""`,
	} {
		if !strings.Contains(apply, check) {
			t.Fatalf("GET / applyVisualViewportHeight should include %q (issue #100 L1 R6)", check)
		}
	}
	// The scroll listener must gate on an existing pin: a pure pan never
	// introduces one — that is what keeps an unpanned page on the pure-CSS
	// sizing path.
	reapply := sliceJSFunction(t, bodyText, "reapplyPinnedVisualViewportHeight")
	if !strings.Contains(reapply, "if (!app || !app.style.height) return;") {
		t.Fatal("GET / reapplyPinnedVisualViewportHeight must gate on an existing inline height — a pure pan must not introduce pinning (issue #100 L1 R6)")
	}
	init := sliceJSFunction(t, bodyText, "initVisualViewportHandling")
	for _, check := range []string{
		"if (!vv) return;",
		`vv.addEventListener("resize", applyVisualViewportHeight)`,
		`vv.addEventListener("scroll", reapplyPinnedVisualViewportHeight)`,
	} {
		if !strings.Contains(init, check) {
			t.Fatalf("GET / initVisualViewportHandling should include %q (issue #100 L1 R6)", check)
		}
	}
	// The wiring must actually run at startup, or the whole R6 path is dead
	// code behind an unexported function.
	if !strings.Contains(sliceJSFunction(t, bodyText, "init"), "initVisualViewportHandling();") {
		t.Fatal("GET / init() must call initVisualViewportHandling() (issue #100 L1 R6)")
	}
}

// TestNarrowDrawerAnchors pins the issue #100 L2 breakpoint/drawer contract
// (PR3 of docs/plans/feature-v0.6.0-webui-sidebar-responsive/): the narrow
// tier (<=768px) renders the one PR1 .collapsed state as an off-canvas
// overlay drawer with a tap-to-close mask over #main, the middle tier
// (769–1024px) narrows the sidebar instead, R8 snaps the tab strip on the
// narrow tier, and the R14 default dispatches by viewport — all on the
// single PR1 state machine, never a second state variable (plan §3.1, the
// PR3 review focus).
func TestNarrowDrawerAnchors(t *testing.T) {
	bodyText := fetchIndexHTML(t)

	// R7: #app is the positioning containing block for the drawer and its
	// mask. In-rule assertion, same style as TestViewportAndHeightAnchors —
	// other rules in the file also set position and a bare Contains would
	// anchor nothing.
	appStart := strings.Index(bodyText, "#app {\n")
	if appStart < 0 {
		t.Fatal("GET / lost the #app rule (issue #100 L2 R7)")
	}
	appEnd := strings.Index(bodyText[appStart:], "\n        }")
	if appEnd < 0 {
		t.Fatal("GET / #app rule has no closing brace")
	}
	if !strings.Contains(bodyText[appStart:appStart+appEnd], "position: relative;") {
		t.Fatal("GET / #app rule should declare position: relative — the drawer's positioning containing block (issue #100 L2 R7)")
	}

	// R7 + §3.1: PR1's zero-width collapse rule is scoped off the narrow
	// tier by the SEAMLESS complement of the drawer's max-width: 768px
	// block (`not all and` — a literal min-width: 769px would leave the
	// (768, 769) fractional dead zone where the toggle button goes
	// visually inert). If this rule leaked onto the narrow tier, the OPEN
	// drawer would inherit width:0/visibility:hidden and render as an
	// invisible strip (the §3.1 trap) — so the selector must live INSIDE
	// the complement block's closing brace, and the narrow tier must carry
	// its own off-canvas collapsed rule instead. A bare position check
	// would NOT catch the rule hoisted to top level (still after the
	// media TEXT, but outside the block — mutation-verified): slice the
	// block and assert containment, plus an exact occurrence count
	// (wide-tier zero-width rule + narrow-tier drawer rule, one each).
	wideStart := strings.Index(bodyText, "@media not all and (max-width: 768px) {")
	if wideStart < 0 {
		t.Fatal(`GET / lost the "not all and (max-width: 768px)" tier scope around the PR1 collapse rule (issue #100 L2 R7)`)
	}
	wideEnd := strings.Index(bodyText[wideStart:], "\n        }")
	if wideEnd < 0 {
		t.Fatal("GET / collapse-rule tier scope has no closing brace")
	}
	wide := bodyText[wideStart : wideStart+wideEnd]
	if !strings.Contains(wide, "#app.collapsed #sidebar {") {
		t.Fatal("GET / must scope the PR1 #app.collapsed #sidebar zero-width rule inside the seamless complement of the narrow tier (its closing brace, not merely after its text) — on the narrow tier .collapsed renders the off-canvas drawer, not a zero-width strip (issue #100 L2, PRD-CHANGE §3.1)")
	}
	if n := strings.Count(bodyText, "#app.collapsed #sidebar {"); n != 2 {
		t.Fatalf("GET / should have exactly two #app.collapsed #sidebar rules (the wide-tier zero-width collapse + the narrow-tier off-canvas drawer), found %d (issue #100 L2, PRD-CHANGE §3.1)", n)
	}
	// Negative pins (PR #110 review, Fix D3): the complement block must carry
	// ONLY the zero-width collapse rule — none of the narrow-tier drawer
	// machinery (absolute positioning, the off-canvas transform, or the
	// mask's inset coverage) may leak into it.
	for _, forbidden := range []string{"position: absolute", "translateX", "inset: 0"} {
		if strings.Contains(wide, forbidden) {
			t.Fatalf("GET / wide-tier complement block must not contain %q — that is drawer machinery and belongs only in the narrow tier (issue #100 L2 R7)", forbidden)
		}
	}

	// R7: the narrow tier block. Nested rule bodies close at 12 spaces, the
	// media block itself at 8 — so this slice spans exactly the tier.
	narrowStart := strings.Index(bodyText, "@media (max-width: 768px) {")
	if narrowStart < 0 {
		t.Fatal("GET / lost the max-width: 768px narrow-tier block (issue #100 L2 R7)")
	}
	narrowEnd := strings.Index(bodyText[narrowStart:], "\n        }")
	if narrowEnd < 0 {
		t.Fatal("GET / @media (max-width: 768px) block has no closing brace")
	}
	narrow := bodyText[narrowStart : narrowStart+narrowEnd]

	// Drawer OPEN state: absolutely positioned over #main, above the mask.
	// Deliberately NO width declaration — the open drawer must keep the
	// base rule's var(--sidebar-width); a width:0 here would be the §3.1
	// invisible-drawer trap in its open-state form.
	drawerStart := strings.Index(narrow, "\n            #sidebar {\n")
	if drawerStart < 0 {
		t.Fatal("GET / narrow tier should restyle #sidebar as the drawer (issue #100 L2 R7)")
	}
	drawerEnd := strings.Index(narrow[drawerStart:], "\n            }")
	drawer := narrow[drawerStart : drawerStart+drawerEnd]
	for _, decl := range []string{"position: absolute;", "left: 0;", "top: 0;", "bottom: 0;", "z-index: 30;"} {
		if !strings.Contains(drawer, decl) {
			t.Fatalf("GET / narrow-tier #sidebar drawer rule should declare %q (issue #100 L2 R7)", decl)
		}
	}
	if strings.Contains(drawer, "width:") {
		t.Fatal("GET / narrow-tier #sidebar drawer rule must not declare a width — the open drawer keeps the base var(--sidebar-width); zeroing it here is the §3.1 invisible-drawer trap (issue #100 L2)")
	}
	// PR #110 review (B2): the drawer's containing block is #app's padding
	// box, so top:0/bottom:0 bleed across the safe-area bands; the rule must
	// consume the four env(safe-area-inset-*) insets as content padding —
	// asserted in-rule so the box stays full-bleed while only the content
	// insets (a padding on #main or the mask would be the wrong element).
	if !strings.Contains(drawer, "env(safe-area-inset-top") ||
		!strings.Contains(drawer, "env(safe-area-inset-bottom") ||
		!strings.Contains(drawer, "env(safe-area-inset-left") {
		t.Fatal("GET / narrow-tier #sidebar drawer rule should consume env(safe-area-inset-*) as padding — the drawer's padding-box containing block bleeds across the notch / Home Indicator bands (PR #110 review, issue #100 L2)")
	}

	// Drawer CLOSED state: off-canvas + focus-order lift, NOT zero-width.
	closedStart := strings.Index(narrow, "#app.collapsed #sidebar {")
	if closedStart < 0 {
		t.Fatal("GET / narrow tier lost the drawer closed-state rule (issue #100 L2 R7)")
	}
	closedEnd := strings.Index(narrow[closedStart:], "\n            }")
	closed := narrow[closedStart : closedStart+closedEnd]
	for _, decl := range []string{"transform: translateX(-100%);", "visibility: hidden;"} {
		if !strings.Contains(closed, decl) {
			t.Fatalf("GET / narrow-tier drawer closed rule should declare %q (issue #100 L2 R7)", decl)
		}
	}
	if strings.Contains(closed, "width: 0") {
		t.Fatal("GET / narrow-tier drawer closed rule must not zero the width — the closed drawer slides off-canvas (translateX), it does not collapse; width:0 is the wide tier's rendering (issue #100 L2, PRD-CHANGE §3.1)")
	}

	// R7 mask: shown only while the drawer is open on this tier, covering
	// #app (hence #main and the tab strip) and sitting under the drawer.
	maskStart := strings.Index(narrow, "#app:not(.collapsed) #sidebar-mask {")
	if maskStart < 0 {
		t.Fatal("GET / narrow tier lost the drawer mask show-rule (issue #100 L2 R7)")
	}
	maskEnd := strings.Index(narrow[maskStart:], "\n            }")
	mask := narrow[maskStart : maskStart+maskEnd]
	for _, decl := range []string{"display: block;", "position: absolute;", "inset: 0;", "z-index: 25;"} {
		if !strings.Contains(mask, decl) {
			t.Fatalf("GET / narrow-tier mask rule should declare %q (issue #100 L2 R7)", decl)
		}
	}

	// R7 z-index layering on the toggle button: it lives in #header (inside
	// #main) and only this lift lets it stay clickable above the open
	// drawer. The pinned ladder reads button 40 > drawer 30 > mask 25 >
	// #main content, everything under the 9999 modal layer.
	// (anchored by its 12-space indentation — the open-state tab selector
	// ends with the same literal, so a bare Index could slice the wrong
	// rule by source order; same discipline as Fix D5).
	btnStart := strings.Index(narrow, "\n            #sidebar-toggle-btn {\n")
	if btnStart < 0 {
		t.Fatal("GET / narrow tier lost the toggle-button z-index lift (issue #100 L2 R7)")
	}
	btnEnd := strings.Index(narrow[btnStart:], "\n            }")
	btn := narrow[btnStart : btnStart+btnEnd]
	for _, decl := range []string{"position: relative;", "z-index: 40;"} {
		if !strings.Contains(btn, decl) {
			t.Fatalf("GET / narrow-tier toggle-button rule should declare %q (issue #100 L2 R7)", decl)
		}
	}
	// The lift must be narrow-tier ONLY: exactly THREE #sidebar-toggle-btn
	// rules may exist — the PR1 base rule (global), the narrow-tier
	// closed-state lift, and the PR #110 design change's narrow-tier
	// OPEN-state tab rule (#app:not(.collapsed) #sidebar-toggle-btn) — and
	// the base rule must not carry position/z-index: on desktop the 9999
	// connection overlay's cover relation is pre-PR3 and must stay
	// untouched. The base rule is anchored by its 8-space indentation: both
	// narrow-tier rules are 12-space indented inside the media block, so a
	// bare LastIndex could silently slice the wrong rule (PR #110 review,
	// Fix D5).
	if n := strings.Count(bodyText, "#sidebar-toggle-btn {"); n != 3 {
		t.Fatalf("GET / should have exactly three #sidebar-toggle-btn rules (PR1 base + narrow-tier closed-state lift + narrow-tier open-state tab), found %d (issues #99, #100 L2)", n)
	}
	baseStart := strings.Index(bodyText, "\n        #sidebar-toggle-btn {\n")
	if baseStart < 0 {
		t.Fatal("GET / lost the 8-space-indented base #sidebar-toggle-btn rule (issue #100 L2 R7)")
	}
	baseEnd := strings.Index(bodyText[baseStart:], "\n        }")
	base := bodyText[baseStart : baseStart+baseEnd]
	if strings.Contains(base, "z-index") || strings.Contains(base, "position:") {
		t.Fatal("GET / base #sidebar-toggle-btn rule must not carry position/z-index — the lift is narrow-tier only, desktop stacking (connection overlay 9999) must stay as shipped (issue #100 L2 R7)")
	}
	// PR #110 design change: with the drawer OPEN on the narrow tier the
	// toggle becomes a fixed tab on the drawer's right edge (it used to
	// float at #header's left and overlap the drawer's title row).
	// In-rule slice: the selector, the fixed positioning, the drawer-edge
	// geometry, the retained z-index, and the tab shaping.
	tabRuleStart := strings.Index(narrow, "#app:not(.collapsed) #sidebar-toggle-btn {")
	if tabRuleStart < 0 {
		t.Fatal("GET / narrow tier lost the open-state tab rule for the toggle button (PR #110 design change, issue #100 L2)")
	}
	tabRuleEnd := strings.Index(narrow[tabRuleStart:], "\n            }")
	if tabRuleEnd < 0 {
		t.Fatal("GET / narrow-tier open-state tab rule has no closing brace")
	}
	tabRule := narrow[tabRuleStart : tabRuleStart+tabRuleEnd]
	for _, decl := range []string{
		"position: fixed;",
		"left: calc(var(--sidebar-width) - 1px)",
		"top: calc(env(safe-area-inset-top, 0px) + 6px)",
		"z-index: 40;",
		"background: var(--bg-color);",
		"border: 1px solid var(--border-color);",
		"border-left: none;",
		"border-radius: 0 6px 6px 0;",
	} {
		if !strings.Contains(tabRule, decl) {
			t.Fatalf("GET / narrow-tier open-state tab rule should declare %q (PR #110 design change, issue #100 L2)", decl)
		}
	}
	// PR #110 review: the tab rule's own background (2,1,0) would
	// permanently outrank .icon-btn:hover's (0,2,0) — the hover restore
	// rule (nested in the hover-capability media, like every other :hover)
	// must exist and repeat the same border-color token.
	hoverRestore := strings.Index(narrow, "#app:not(.collapsed) #sidebar-toggle-btn:hover {")
	if hoverRestore < 0 {
		t.Fatal("GET / narrow tier lost the open-state tab hover-background restore (PR #110 review, issue #100 L2)")
	}
	hoverRestoreEnd := strings.Index(narrow[hoverRestore:], "\n            }")
	if hoverRestoreEnd < 0 {
		t.Fatal("GET / narrow-tier tab hover restore rule has no closing brace")
	}
	if !strings.Contains(narrow[hoverRestore:hoverRestore+hoverRestoreEnd], "background: var(--border-color);") {
		t.Fatal("GET / narrow-tier tab hover restore should re-declare the .icon-btn:hover background token — the tab rule's own background would otherwise outrank it (PR #110 review, issue #100 L2)")
	}

	// Non-goal pin: no transition/animation anywhere in the narrow block —
	// the drawer flips instantly like the desktop collapse (plan §8).
	if strings.Contains(narrow, "transition") || strings.Contains(narrow, "animation") || strings.Contains(narrow, "@keyframes") {
		t.Fatal("GET / narrow-tier block must not add transitions/animations — instant flip is the plan's explicit non-goal (issue #100 L2)")
	}

	// R8: tab snapping, narrow tier only; the hidden-scrollbar rule family
	// is untouched (it lives outside every media query).
	tabsStart := strings.Index(narrow, "#tabs-container {")
	if tabsStart < 0 {
		t.Fatal("GET / narrow tier lost the tabs-container snap rule (issue #100 L2 R8)")
	}
	tabsEnd := strings.Index(narrow[tabsStart:], "\n            }")
	if !strings.Contains(narrow[tabsStart:tabsStart+tabsEnd], "scroll-snap-type: x proximity;") {
		t.Fatal("GET / narrow-tier #tabs-container should declare scroll-snap-type: x proximity (issue #100 L2 R8)")
	}
	tabStart := strings.Index(narrow, ".tab {")
	if tabStart < 0 {
		t.Fatal("GET / narrow tier lost the .tab snap-align rule (issue #100 L2 R8)")
	}
	tabEnd := strings.Index(narrow[tabStart:], "\n            }")
	if !strings.Contains(narrow[tabStart:tabStart+tabEnd], "scroll-snap-align:") {
		t.Fatal("GET / narrow-tier .tab should declare a scroll-snap-align (issue #100 L2 R8)")
	}
	// R8 inline correction (PR #110 beta, §5.6-2): the "+" (.new-tab-btn)
	// is the strip's first child and must be the first snap point — without
	// it, snap auto-scrolls right on overflow and clips "+" under the
	// toggle's transparent hit area.
	newTabStart := strings.Index(narrow, ".new-tab-btn {")
	if newTabStart < 0 {
		t.Fatal("GET / narrow tier lost the .new-tab-btn snap-align rule (issue #100 L2 R8 inline correction, §5.6-2)")
	}
	newTabEnd := strings.Index(narrow[newTabStart:], "\n            }")
	if newTabEnd < 0 {
		t.Fatal("GET / narrow-tier .new-tab-btn snap-align rule has no closing brace")
	}
	if !strings.Contains(narrow[newTabStart:newTabStart+newTabEnd], "scroll-snap-align: start;") {
		t.Fatal("GET / narrow-tier .new-tab-btn should declare scroll-snap-align: start — the strip's first child must anchor the rest position (issue #100 L2 R8 inline correction)")
	}

	// R7 mask global state: display:none everywhere — without it the div
	// would be a visible flex sibling swallowing the whole desktop layout.
	maskGlobalStart := strings.Index(bodyText, "\n        #sidebar-mask {\n")
	if maskGlobalStart < 0 {
		t.Fatal("GET / lost the global #sidebar-mask display:none rule (issue #100 L2 R7)")
	}
	maskGlobalEnd := strings.Index(bodyText[maskGlobalStart:], "\n        }")
	maskGlobal := bodyText[maskGlobalStart : maskGlobalStart+maskGlobalEnd]
	if !strings.Contains(maskGlobal, "display: none;") {
		t.Fatal("GET / global #sidebar-mask rule should declare display: none (issue #100 L2 R7)")
	}
	if strings.Contains(maskGlobal, "position:") || strings.Contains(maskGlobal, "inset:") {
		t.Fatal("GET / global #sidebar-mask rule must stay a plain display:none — positioning it on desktop would create a hit-box over the app (issue #100 L2 R7)")
	}

	// R7 DOM: the mask is a child of #app, between #sidebar and #main, and
	// its click runs the shipped close path.
	appDomAt := strings.Index(bodyText, `<div id="app">`)
	maskDomAt := strings.Index(bodyText, `id="sidebar-mask"`)
	mainDomAt := strings.Index(bodyText, `<div id="main">`)
	if appDomAt < 0 || maskDomAt < 0 || maskDomAt < appDomAt || maskDomAt > mainDomAt {
		t.Fatal("GET / should place #sidebar-mask as a child of #app between #sidebar and #main (issue #100 L2 R7)")
	}
	if !strings.Contains(bodyText, `onclick="closeSidebarDrawer()"`) {
		t.Fatal(`GET / mask should close the drawer via onclick="closeSidebarDrawer()" (issue #100 L2 R7)`)
	}

	// R14: the viewport-dispatched default. Both the missing-key path and
	// the parse-failure/degraded path must dispatch (exactly two call
	// sites); an explicit persisted value short-circuits before the
	// dispatcher and never consults matchMedia.
	read := sliceJSFunction(t, bodyText, "readSidebarCollapsed")
	if got := strings.Count(read, "return defaultSidebarCollapsedByViewport();"); got != 2 {
		t.Fatalf("GET / readSidebarCollapsed should dispatch the viewport default on BOTH the missing key and the failure path (exactly 2 call sites), found %d (issue #100 L2 R14)", got)
	}
	def := sliceJSFunction(t, bodyText, "defaultSidebarCollapsedByViewport")
	for _, check := range []string{
		`window.matchMedia("(max-width: 768px)").matches`,
		`typeof window.matchMedia !== "function"`,
		`typeof window === "undefined"`,
	} {
		if !strings.Contains(def, check) {
			t.Fatalf("GET / defaultSidebarCollapsedByViewport should include %q (issue #100 L2 R14)", check)
		}
	}

	// R7 JS: the mask's close path is the same single-state wiring as the
	// toggle — apply(true) first, then persist(true), never a toggle of a
	// second variable.
	closers := sliceJSFunction(t, bodyText, "closeSidebarDrawer")
	applyAt := strings.Index(closers, "applySidebarCollapsed(true);")
	persistAt := strings.Index(closers, "persistSidebarCollapsed(true);")
	if applyAt < 0 || persistAt < 0 || applyAt > persistAt {
		t.Fatal("GET / closeSidebarDrawer must applySidebarCollapsed(true) then persistSidebarCollapsed(true) — the mask closes through the shared single-state path (issue #100 L2 R7)")
	}

	// Plan §3.1 review pin: no second drawer state variable anywhere in the
	// file (the PR3 review focus). The drawer is a RENDERING of the one
	// sidebarCollapsed boolean.
	for _, forbidden := range []string{"drawerOpen", "drawerCollapsed", "drawerVisible", "drawerState"} {
		if strings.Contains(bodyText, forbidden) {
			t.Fatalf("GET / must not introduce a second drawer state variable — found %q (issue #100 L2, plan §3.1)", forbidden)
		}
	}

	// R7 middle tier: 769–1024px narrows the sidebar to a 200px cap; 768
	// belongs to the narrow tier (max-width:768px and min-width:769px must
	// never both match at one width, PRD-CHANGE §3.6).
	midStart := strings.Index(bodyText, "@media (min-width: 769px) and (max-width: 1024px) {")
	if midStart < 0 {
		t.Fatal("GET / lost the 769–1024px middle-tier block (issue #100 L2 R7, §3.6)")
	}
	midEnd := strings.Index(bodyText[midStart:], "\n        }")
	if midEnd < 0 {
		t.Fatal("GET / middle-tier block has no closing brace")
	}
	mid := bodyText[midStart : midStart+midEnd]
	// In-rule assertion (PR #110 review, Fix D2): the cap must be a
	// declaration of the #sidebar rule inside this block, not two unrelated
	// strings somewhere in the slice.
	midRuleStart := strings.Index(mid, "\n            #sidebar {\n")
	if midRuleStart < 0 {
		t.Fatal("GET / middle tier should restyle #sidebar (issue #100 L2 R7, §3.6)")
	}
	midRuleEnd := strings.Index(mid[midRuleStart:], "\n            }")
	if midRuleEnd < 0 {
		t.Fatal("GET / middle-tier #sidebar rule has no closing brace")
	}
	midRule := mid[midRuleStart : midRuleStart+midRuleEnd]
	if !strings.Contains(midRule, "max-width: 200px;") {
		t.Fatal("GET / middle tier should cap #sidebar at max-width: 200px inside the #sidebar rule while keeping the side-by-side layout (issue #100 L2 R7)")
	}
}

// TestTouchInteractionAnchors pins the issue #100 L3 touch-interaction
// contract (PR4 of docs/plans/feature-v0.6.0-webui-sidebar-responsive/):
// the sidebar split drag runs on Pointer Events with pointer capture (R9),
// the handle carries touch-action:none and drag-scoped user-select /
// -webkit-touch-callout, coarse pointers get 44px minimum hit targets via
// min-width/min-height (R10), and EVERY :hover selector in the stylesheet
// sits inside a @media (hover: hover) block — pure-touch devices get no
// sticky hover highlights (R11). The hover check is a containment scan,
// not an ordering check (PR3's M1 lesson): each :hover occurrence must lie
// within the closing brace of some open hover-capability media block.
func TestTouchInteractionAnchors(t *testing.T) {
	bodyText := fetchIndexHTML(t)

	// R9 DOM: the drag starts from pointerdown, and the mouse-only inline
	// handler is gone.
	if !strings.Contains(bodyText, `onpointerdown="startSidebarResize(event)"`) {
		t.Fatal(`GET / should start the sidebar resize from onpointerdown="startSidebarResize(event)" (issue #100 L3 R9)`)
	}
	if strings.Contains(bodyText, `onmousedown="startSidebarResize`) {
		t.Fatal(`GET / must not keep the mouse-only onmousedown="startSidebarResize" handler (issue #100 L3 R9)`)
	}
	for _, forbidden := range []string{`addEventListener("mousedown"`, `addEventListener("mousemove"`, `addEventListener("mouseup"`} {
		if strings.Contains(bodyText, forbidden) {
			t.Fatalf("GET / must not register mouse-event listeners for the drag — found %q (issue #100 L3 R9)", forbidden)
		}
	}

	// R9 JS, sliced to the shipped functions: pointer capture, the three
	// pointer listeners, preventDefault, the re-entry guard, and the
	// UNCHANGED clamp math.
	start := sliceJSFunction(t, bodyText, "startSidebarResize")
	for _, check := range []string{
		"if (isResizingSidebar) return;",
		"handle.setPointerCapture(e.pointerId)",
		`document.addEventListener("pointermove", handleSidebarResize)`,
		`document.addEventListener("pointerup", stopSidebarResize)`,
		`document.addEventListener("pointercancel", stopSidebarResize)`,
		"e.preventDefault();",
		`handle.classList.add("dragging")`,
	} {
		if !strings.Contains(start, check) {
			t.Fatalf("GET / startSidebarResize should include %q (issue #100 L3 R9)", check)
		}
	}
	stop := sliceJSFunction(t, bodyText, "stopSidebarResize")
	for _, check := range []string{
		`document.removeEventListener("pointermove", handleSidebarResize)`,
		`document.removeEventListener("pointerup", stopSidebarResize)`,
		`document.removeEventListener("pointercancel", stopSidebarResize)`,
		"handle.releasePointerCapture(e.pointerId)",
		`handle.classList.remove("dragging")`,
	} {
		if !strings.Contains(stop, check) {
			t.Fatalf("GET / stopSidebarResize should include %q (issue #100 L3 R9)", check)
		}
	}
	drag := sliceJSFunction(t, bodyText, "handleSidebarResize")
	for _, check := range []string{
		"const minTop = 120;",
		"const minBottom = 100;",
		"Math.max(minTop, Math.min(sidebarContentHeight - minBottom, newTopHeight))",
	} {
		if !strings.Contains(drag, check) {
			t.Fatalf("GET / handleSidebarResize clamp math must stay verbatim — missing %q (issue #100 L3 R9)", check)
		}
	}

	// R9 CSS: touch-action:none lives on the handle rule (in-rule), and the
	// drag-scoped selection/callout suppression sits with the .dragging
	// half — which must stay global (a drag is not a hover state).
	handleStart := strings.Index(bodyText, "#sidebar-resize-handle {")
	if handleStart < 0 {
		t.Fatal("GET / lost the #sidebar-resize-handle rule (issue #100 L3 R9)")
	}
	handleEnd := strings.Index(bodyText[handleStart:], "\n        }")
	if handleEnd < 0 {
		t.Fatal("GET / #sidebar-resize-handle rule has no closing brace")
	}
	handleRule := bodyText[handleStart : handleStart+handleEnd]
	if !strings.Contains(handleRule, "touch-action: none;") {
		t.Fatal("GET / #sidebar-resize-handle should declare touch-action: none — evaluated at gesture start, so it must be permanent, not drag-scoped (issue #100 L3 R9)")
	}
	draggingStart := strings.Index(bodyText, "#sidebar-resize-handle.dragging {")
	if draggingStart < 0 {
		t.Fatal("GET / lost the #sidebar-resize-handle.dragging rule (issue #100 L3 R9)")
	}
	draggingEnd := strings.Index(bodyText[draggingStart:], "\n        }")
	draggingRule := bodyText[draggingStart : draggingStart+draggingEnd]
	for _, decl := range []string{"user-select: none;", "-webkit-touch-callout: none;", "background: var(--active-color);"} {
		if !strings.Contains(draggingRule, decl) {
			t.Fatalf("GET / #sidebar-resize-handle.dragging should declare %q (issue #100 L3 R9)", decl)
		}
	}

	// R10: coarse-pointer hit targets. min-width/min-height inside the
	// media block — fixed width/height 44 would defeat the "never force
	// smaller" contract, and pt is the wrong unit (~58.7px).
	coarseStart := strings.Index(bodyText, "@media (pointer: coarse) {")
	if coarseStart < 0 {
		t.Fatal("GET / lost the @media (pointer: coarse) touch-target block (issue #100 L3 R10)")
	}
	coarseEnd := strings.Index(bodyText[coarseStart:], "\n        }")
	if coarseEnd < 0 {
		t.Fatal("GET / @media (pointer: coarse) block has no closing brace")
	}
	coarse := bodyText[coarseStart : coarseStart+coarseEnd]
	for _, sel := range []string{".icon-btn {", ".term-ctrl-btn {"} {
		selAt := strings.Index(coarse, sel)
		if selAt < 0 {
			t.Fatalf("GET / coarse-pointer block should include a %s rule (issue #100 L3 R10)", sel)
		}
		ruleEnd := strings.Index(coarse[selAt:], "\n            }")
		if ruleEnd < 0 {
			t.Fatalf("GET / coarse-pointer %s rule has no closing brace", sel)
		}
		rule := coarse[selAt : selAt+ruleEnd]
		for _, decl := range []string{"min-width: 44px;", "min-height: 44px;"} {
			if !strings.Contains(rule, decl) {
				t.Fatalf("GET / coarse-pointer %s rule should declare %q (issue #100 L3 R10)", sel, decl)
			}
		}
		for _, line := range strings.Split(rule, "\n") {
			trimmed := strings.TrimSpace(line)
			if strings.HasPrefix(trimmed, "width: 44px") || strings.HasPrefix(trimmed, "height: 44px") {
				t.Fatalf("GET / coarse-pointer %s rule must use min-width/min-height, not fixed width/height — found %q (issue #100 L3 R10)", sel, trimmed)
			}
		}
	}
	// R10 residual-gap guard (review round 1): the coarse block only RAISES
	// the floor — the .icon-btn BASE rule must stay free of fixed
	// width/height declarations (it is padding-driven today; a future fixed
	// 44px there would silently defeat the min-* contract, and this is the
	// only assertion that would turn red).
	iconBaseStart := strings.Index(bodyText, "\n        .icon-btn {\n")
	if iconBaseStart < 0 {
		t.Fatal("GET / lost the .icon-btn base rule (issue #100 L3 R10)")
	}
	iconBaseEnd := strings.Index(bodyText[iconBaseStart:], "\n        }")
	if iconBaseEnd < 0 {
		t.Fatal("GET / .icon-btn base rule has no closing brace")
	}
	iconBase := bodyText[iconBaseStart : iconBaseStart+iconBaseEnd]
	for _, line := range strings.Split(iconBase, "\n") {
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "width:") || strings.HasPrefix(trimmed, "height:") {
			t.Fatalf("GET / .icon-btn base rule must not declare a fixed %q — R10's min-* only raises the floor (issue #100 L3 R10)", trimmed)
		}
	}
	// R10 safe-area refinement (review round 1): inside the 40px header on
	// inset-top:0 devices, the centered 44px box would lose its top 2.5px to
	// the body/html overflow:hidden clip (clipped area does not hit-test);
	// the block shifts the header's icon button down 3px and lifts it above
	// #workspace's top strip so the full 44px hit-tests.
	headerBtnAt := strings.Index(coarse, "#header .icon-btn {")
	if headerBtnAt < 0 {
		t.Fatal("GET / coarse-pointer block lost the #header .icon-btn safe-area rule (issue #100 L3 R10)")
	}
	headerBtnEnd := strings.Index(coarse[headerBtnAt:], "\n            }")
	if headerBtnEnd < 0 {
		t.Fatal("GET / coarse-pointer #header .icon-btn rule has no closing brace")
	}
	headerBtnRule := coarse[headerBtnAt : headerBtnAt+headerBtnEnd]
	for _, decl := range []string{"top: 3px;", "z-index: 40;"} {
		if !strings.Contains(headerBtnRule, decl) {
			t.Fatalf("GET / coarse-pointer #header .icon-btn rule should declare %q — the clip/spill geometry needs both (issue #100 L3 R10)", decl)
		}
	}
	// The variable keeps driving the base size (min-* only raises the floor).
	if !strings.Contains(bodyText, "width: var(--term-ctrl-btn-size);") || !strings.Contains(bodyText, "height: var(--term-ctrl-btn-size);") {
		t.Fatal("GET / .term-ctrl-btn base size must stay driven by --term-ctrl-btn-size (issue #100 L3 R10)")
	}
	// 44pt ban. Scoped to the <style> slice (PR #110 review, Fix D6): the
	// previous full-body scan stripped comments across the WHOLE document,
	// where a JS line comment containing /* (in the kinds/*.js sources
	// inlined below the style block) silently truncated the scan ~2/3 in —
	// anything after that point was never checked. The unit only matters in
	// CSS, and the <style> slice contains no such hazard.
	styleStart := strings.Index(bodyText, "<style>")
	styleEnd := strings.Index(bodyText, "</style>")
	if styleStart < 0 || styleEnd < 0 {
		t.Fatal("GET / lost the inline <style> block")
	}
	style := bodyText[styleStart:styleEnd]
	if strings.Contains(stripCSSComments(style), "44pt") {
		t.Fatal("GET / must not use 44pt — Apple HIG 44pt IS 44 CSS px in iOS Safari; pt computes to ~58.7px (issue #100 L3 R10)")
	}

	// PR #110 review (M1): with R11 gating the .wt-actions reveal on hover
	// capability, pure-touch devices can never show the row actions, and
	// opacity: 0 does NOT block hit-testing — without this block the row's
	// right end would be two permanently invisible yet tappable buttons.
	// (Restoring the reveal on touch is the §8 ④ product decision, deferred;
	// this block only closes the invisible-hit-area trap.)
	noneStart := strings.Index(bodyText, "@media (hover: none) {")
	if noneStart < 0 {
		t.Fatal("GET / lost the @media (hover: none) block that closes the .wt-actions invisible hit area (PR #110 review, issue #100 L3)")
	}
	noneEnd := strings.Index(bodyText[noneStart:], "\n        }")
	if noneEnd < 0 {
		t.Fatal("GET / @media (hover: none) block has no closing brace")
	}
	none := bodyText[noneStart : noneStart+noneEnd]
	noneRuleStart := strings.Index(none, ".wt-actions {")
	if noneRuleStart < 0 {
		t.Fatal("GET / @media (hover: none) block should scope .wt-actions (PR #110 review, issue #100 L3)")
	}
	noneRuleEnd := strings.Index(none[noneRuleStart:], "\n            }")
	if noneRuleEnd < 0 {
		t.Fatal("GET / @media (hover: none) .wt-actions rule has no closing brace")
	}
	if !strings.Contains(none[noneRuleStart:noneRuleStart+noneRuleEnd], "pointer-events: none;") {
		t.Fatal("GET / @media (hover: none) .wt-actions rule should declare pointer-events: none — the actions can never appear on pure-touch devices, and opacity:0 alone would leave an invisible tappable area (PR #110 review, issue #100 L3)")
	}

	// R11: containment scan over the stylesheet — with comments stripped,
	// every :hover selector must sit inside an open @media (hover: hover)
	// block. The non-hover interaction states stay global: the resize
	// handle's .dragging half and the scrollbar thumb's :active half were
	// split out of their old combined selectors — the scan now proves they
	// sit at hover-block depth 0, not merely that they exist (PR #110
	// review, Fix D4).
	css := stripCSSComments(style)
	depth := 0
	var hoverDepths []int
	hovers := 0
	globalRules := map[string]bool{
		"#sidebar-resize-handle.dragging":                 false,
		"#tabs-container::-webkit-scrollbar-thumb:active": false,
	}
	for i := 0; i < len(css); {
		switch css[i] {
		case '{':
			depth++
			prefix := strings.TrimRight(css[:i], " \t\r\n")
			if strings.HasSuffix(prefix, "@media (hover: hover)") {
				hoverDepths = append(hoverDepths, depth)
			}
			for selector := range globalRules {
				if strings.HasSuffix(prefix, selector) {
					if len(hoverDepths) != 0 {
						line := 1 + strings.Count(css[:i], "\n")
						t.Fatalf("GET / non-hover interaction rule %q at stylesheet line %d must stay GLOBAL — it sits inside a hover-capability block (issue #100 L3 R11)", selector, line)
					}
					globalRules[selector] = true
				}
			}
			i++
		case '}':
			depth--
			for len(hoverDepths) > 0 && hoverDepths[len(hoverDepths)-1] > depth {
				hoverDepths = hoverDepths[:len(hoverDepths)-1]
			}
			i++
		default:
			if strings.HasPrefix(css[i:], ":hover") {
				hovers++
				if len(hoverDepths) == 0 {
					line := 1 + strings.Count(css[:i], "\n")
					t.Fatalf("GET / :hover selector at stylesheet line %d sits OUTSIDE every @media (hover: hover) block — R11 requires the hover-capability gate (issue #100 L3)", line)
				}
				i += len(":hover")
			} else {
				i++
			}
		}
	}
	if hovers != 24 {
		t.Fatalf("GET / should carry the 24 known :hover selectors inside hover-capability blocks (22 from R11, the PR #110 tab restore, and the quick-keys key hover), found %d — an added or removed hover rule must update this test (issues #100 L3 R11, #110, quick keys)", hovers)
	}
	for selector, found := range globalRules {
		if !found {
			t.Fatalf("GET / must keep the non-hover interaction state %q global — it was split out of a combined :hover selector (issue #100 L3 R11)", selector)
		}
	}
}

// TestTerminalTouchScrollAnchors pins the R17 contract (issue #100 padOS② /
// common①, PR #110 beta): the page rubber-band lock (root
// overscroll-behavior:none plus overscroll-behavior:contain on the four
// legitimate inner scrollers), the terminal container's touch-action:none,
// and the initTerminalTouchScroll wiring — delegated touch listeners with
// the passive split, the unconditional touchmove preventDefault (the page
// lock must hold even with no active session), and the scrollLines mapping
// with the signed remainder accumulator. Every CSS declaration is asserted
// INSIDE its rule (anchored at 8-space indentation so a substring hit inside
// a compound selector cannot satisfy the wrong rule), and stripCSSComments
// takes only the <style> slice per the §6 house rule.
func TestTerminalTouchScrollAnchors(t *testing.T) {
	bodyText := fetchIndexHTML(t)

	styleStart := strings.Index(bodyText, "<style>")
	styleEnd := strings.Index(bodyText, "</style>")
	if styleStart < 0 || styleEnd < 0 {
		t.Fatal("GET / lost the inline <style> block")
	}
	css := stripCSSComments(bodyText[styleStart:styleEnd])

	assertRuleDecl := func(name, ruleAnchor, decl string) {
		t.Helper()
		start := strings.Index(css, ruleAnchor)
		if start < 0 {
			t.Fatalf("GET / lost the %s rule (issue #100 R17)", name)
		}
		end := strings.Index(css[start:], "\n        }")
		if end < 0 {
			t.Fatalf("GET / %s rule has no closing brace", name)
		}
		if !strings.Contains(css[start:start+end], decl) {
			t.Fatalf("GET / %s rule should declare %q (issue #100 R17)", name, decl)
		}
	}

	assertRuleDecl("body/html", "\n        body, html {\n", "overscroll-behavior: none;")
	assertRuleDecl("#terminal-container", "\n        #terminal-container {\n", "touch-action: none;")
	assertRuleDecl("#wt-list", "\n        #wt-list {\n", "overscroll-behavior: contain;")
	assertRuleDecl(".git-section-list", "\n        .git-section-list {\n", "overscroll-behavior: contain;")
	assertRuleDecl("dialog", "\n        dialog {\n", "overscroll-behavior: contain;")
	assertRuleDecl("#tabs-container", "\n        #tabs-container {\n", "overscroll-behavior: contain;")

	// JS wiring, sliced to the shipped function.
	ts := sliceJSFunction(t, bodyText, "initTerminalTouchScroll")
	for _, check := range []string{
		`addEventListener("touchstart"`,
		`addEventListener("touchmove"`,
		`addEventListener("touchend"`,
		`addEventListener("touchcancel"`,
		"{ passive: true }",
		"{ passive: false }",
		"e.preventDefault();",
		"getActiveTerminalSession()",
		"(term.options.fontSize || 13) * (term.options.lineHeight || 1)",
		"term.scrollLines(lines)",
		"touchScrollRemainderPx -= lines * linePx",
	} {
		if !strings.Contains(ts, check) {
			t.Fatalf("GET / initTerminalTouchScroll should include %q (issue #100 R17)", check)
		}
	}
	// The hard lock must fire even with no active session: preventDefault
	// precedes the session lookup in the touchmove handler.
	pdAt := strings.Index(ts, "e.preventDefault();")
	sessAt := strings.Index(ts, "const session = getActiveTerminalSession();")
	if pdAt < 0 || sessAt < 0 || pdAt > sessAt {
		t.Fatal("GET / initTerminalTouchScroll must preventDefault BEFORE the session lookup — the page lock holds with no session (issue #100 R17)")
	}
	// Wired at startup.
	if !strings.Contains(sliceJSFunction(t, bodyText, "init"), "initTerminalTouchScroll();") {
		t.Fatal("GET / init() must call initTerminalTouchScroll() (issue #100 R17)")
	}
}

// TestQuickKeysAnchors pins the PR1 contract of the terminal quick-keys
// panel (issue #100 input gap; docs/plans/feature-v0.6.0-terminal-quick-keys/):
// the entry button is the FIRST child of #terminal-controls (DOM order
// above Refresh), the single panel DOM lives inside the column, the column
// gained the bottom offset + pointer-events:none contract with clickable
// parts re-enabling their own, the panel carries the left-expansion
// geometry and size discipline, every action binds pointerdown (never
// click), and the grid uses explicit column tiers (no auto-fit/auto-fill).
func TestQuickKeysAnchors(t *testing.T) {
	bodyText := fetchIndexHTML(t)

	// R18 DOM order: entry button FIRST (above the Refresh button), panel
	// second — position, not mere presence.
	controlsAt := strings.Index(bodyText, `<div id="terminal-controls">`)
	if controlsAt < 0 {
		t.Fatal("GET / lost the #terminal-controls block (quick keys R18)")
	}
	toggleAt := strings.Index(bodyText[controlsAt:], `id="quick-keys-toggle"`)
	panelAt := strings.Index(bodyText[controlsAt:], `id="quick-keys-panel"`)
	refreshAt := strings.Index(bodyText[controlsAt:], `onclick="refreshCurrentInstance()"`)
	if toggleAt < 0 {
		t.Fatal("GET / should include the quick-keys entry button inside #terminal-controls (issue #100 R18)")
	}
	if panelAt < 0 || toggleAt > panelAt {
		t.Fatal("GET / should place the quick-keys panel after the entry button inside #terminal-controls (issue #100 R19)")
	}
	if refreshAt < 0 || panelAt > refreshAt {
		t.Fatal("GET / should keep the quick-keys entry and panel BEFORE the Refresh button — entry is the first child (issue #100 R18)")
	}
	for _, check := range []string{
		`title="Quick keys"`,
		`tabindex="-1"`, // keyboard-unreachable is deliberate (R18 touch compensation) — no focusable no-response control
		`onpointerdown="toggleQuickKeysPanel(event)"`,
		`onpointerdown="sendQuickKey('esc', event)"`,
		`onpointerdown="sendQuickKey('ctrl-c', event)"`,
		// Copy/Paste keys (live since PR2; the PR1 disabled-placeholder
		// state is asserted removed in the PR2 section below).
		`onpointerdown="quickKeyCopy(event)"`,
		`onpointerdown="quickKeyPaste(event)"`,
		// Paste fallback state shell (PR2 wired the interaction).
		`id="quick-keys-paste-fallback"`,
		`id="quick-keys-paste-textarea"`,
		`id="quick-keys-paste-cancel"`,
	} {
		if !strings.Contains(bodyText, check) {
			t.Fatalf("GET / should include the quick-keys DOM anchor %q (issue #100 R18/R19)", check)
		}
	}
	// R20: pointerdown-only — no click binding anywhere on the panel actions
	// (§5.3 whole-period gate: the panel never listens to click).
	for _, forbidden := range []string{
		`onclick="toggleQuickKeysPanel`,
		`onclick="sendQuickKey`,
		`onclick="quickKeyCopy`,
		`onclick="quickKeyPaste`,
		`onclick="cancelQuickKeysPaste`,
		`onclick="quickKeysPasteCaptured`,
		`id="quick-keys-toggle" title="Quick keys" onclick`,
	} {
		if strings.Contains(bodyText, forbidden) {
			t.Fatalf("GET / quick-keys actions must bind pointerdown, not click — found %q (issue #100 R20)", forbidden)
		}
	}

	// Container contract: bottom offset (determinate height for the panel's
	// percentage max-height) + pointer-events none, children re-enabled.
	styleStart := strings.Index(bodyText, "<style>")
	styleEnd := strings.Index(bodyText, "</style>")
	if styleStart < 0 || styleEnd < 0 {
		t.Fatal("GET / lost the inline <style> block")
	}
	css := stripCSSComments(bodyText[styleStart:styleEnd])
	assertRuleDecl := func(name, ruleAnchor, decl string) {
		t.Helper()
		start := strings.Index(css, ruleAnchor)
		if start < 0 {
			t.Fatalf("GET / lost the %s rule (quick keys)", name)
		}
		end := strings.Index(css[start:], "\n            }")
		if end < 0 {
			t.Fatalf("GET / %s rule has no closing brace", name)
		}
		if !strings.Contains(css[start:start+end], decl) {
			t.Fatalf("GET / %s rule should declare %q (quick keys)", name, decl)
		}
	}
	assertRuleDecl("#terminal-controls", "\n            #terminal-controls {\n", "bottom: 20px;")
	assertRuleDecl("#terminal-controls", "\n            #terminal-controls {\n", "pointer-events: none;")
	assertRuleDecl(".term-ctrl-btn", "\n            .term-ctrl-btn {\n                width: var(--term-ctrl-btn-size);", "pointer-events: auto;")

	// Panel geometry + size discipline (left expansion, per §3.1).
	assertRuleDecl("#quick-keys-panel", "\n            #quick-keys-panel {\n", "position: absolute;")
	assertRuleDecl("#quick-keys-panel", "\n            #quick-keys-panel {\n", "top: 0;")
	assertRuleDecl("#quick-keys-panel", "\n            #quick-keys-panel {\n", "right: calc(100% + 8px);")
	assertRuleDecl("#quick-keys-panel", "\n            #quick-keys-panel {\n", "max-height: 100%;")
	assertRuleDecl("#quick-keys-panel", "\n            #quick-keys-panel {\n", "overflow-y: auto;")
	assertRuleDecl("#quick-keys-panel", "\n            #quick-keys-panel {\n", "max-width: calc(100vw - 88px);")
	assertRuleDecl("#quick-keys-panel", "\n            #quick-keys-panel {\n", "pointer-events: auto;")
	// Visibility derives from the .open class only.
	assertRuleDecl("#quick-keys-panel.open", "#quick-keys-panel.open {", "display: block;")

	// Grid tiers: explicit 3, narrow 2 — auto-fit/auto-fill forbidden
	// (§3.1 警戒③).
	assertRuleDecl(".quick-keys-grid", "\n            .quick-keys-grid {\n", "grid-template-columns: repeat(3, 1fr);")
	narrowAt := strings.Index(css, "@media (max-width: 480px) {")
	if narrowAt < 0 {
		t.Fatal("GET / lost the narrow-tier quick-keys grid block (quick keys)")
	}
	narrowEnd := strings.Index(css[narrowAt:], "\n            }")
	if !strings.Contains(css[narrowAt:narrowAt+narrowEnd], "repeat(2, 1fr)") {
		t.Fatal("GET / narrow tier should drop the quick-keys grid to 2 columns (quick keys)")
	}
	for _, forbidden := range []string{"auto-fit", "auto-fill"} {
		if strings.Contains(css, forbidden) {
			t.Fatalf("GET / quick-keys grid must not use %q — column count is tiered by media query (§3.1 警戒③)", forbidden)
		}
	}
	// Coarse pointers get the ≥44px keys (R10 convention). Anchored to the
	// quick-keys block itself — a bare "@media (pointer: coarse) {" would
	// first match the header-icon block (:501), a false positive.
	coarseAnchor := "@media (pointer: coarse) {\n                .quick-key-btn {"
	assertRuleDecl("coarse quick keys", coarseAnchor, "min-width: 44px;")
	assertRuleDecl("coarse quick keys", coarseAnchor, "min-height: 44px;")

	// JS wiring: byte table, derived visibility, single sync path.
	for _, check := range []string{
		"function quickKeyBytes(key, applicationCursorKeysMode)",
		`case "esc": return "\x1b";`,
		`case "ctrl-c": return "\x03";`,
		"function deriveQuickKeysVisibility(session)",
		"function syncQuickKeysPanel()",
		"function ensureQuickKeysImeBinding(session)",
		"function flushQuickKeysQueue(session)",
	} {
		if !strings.Contains(bodyText, check) {
			t.Fatalf("GET / should include the quick-keys JS anchor %q (issue #100 R19/R20)", check)
		}
	}
	send := sliceJSFunction(t, bodyText, "sendQuickKeyBytes")
	if !strings.Contains(send, "session.term.input(bytes)") {
		t.Fatal("GET / sendQuickKeyBytes must send via term.input(bytes) — the sole onData gate (issue #100 R20)")
	}
	syncFn := sliceJSFunction(t, bodyText, "syncQuickKeysPanel")
	toggleAtFn := strings.Index(syncFn, `classList.toggle("open", visible)`)
	resetAt := strings.Index(syncFn, "resetQuickKeysPanelContent(panel)")
	if toggleAtFn < 0 || resetAt < 0 || toggleAtFn > resetAt {
		t.Fatal("GET / syncQuickKeysPanel must toggle .open BEFORE the hide-reset — content resets on every hide path (issue #100 R19)")
	}
	if !strings.Contains(syncFn, "if (session) ensureQuickKeysImeBinding(session);") {
		t.Fatal("GET / syncQuickKeysPanel must bind IME listeners for EVERY session a render sees — not only when the panel is visible (review-found defect, issue #100 R20)")
	}
	// The toggle handler drives the same single sync — the flag flip and
	// the visibility sync are one indivisible step there.
	toggleFn := sliceJSFunction(t, bodyText, "toggleQuickKeysPanel")
	if !strings.Contains(toggleFn, "syncQuickKeysPanel();") {
		t.Fatal("GET / toggleQuickKeysPanel must call syncQuickKeysPanel() after flipping the flag (issue #100 R19)")
	}
	workspace := sliceJSFunction(t, bodyText, "renderWorkspace")
	if !strings.Contains(workspace, "syncQuickKeysPanel();") {
		t.Fatal("GET / renderWorkspace must call syncQuickKeysPanel() — instance binding derives on every render (issue #100 R19)")
	}

	// --- PR2: copy & paste (R21/R22) ---
	// The disabled placeholders are gone — both keys are live and bound.
	for _, forbidden := range []string{`title="Copy" disabled`, `title="Paste" disabled`} {
		if strings.Contains(bodyText, forbidden) {
			t.Fatalf("GET / PR2 must un-disable the Copy/Paste keys — still found %q (issue #100 R21/R22)", forbidden)
		}
	}
	for _, check := range []string{
		`onpointerdown="quickKeyCopy(event)"`,
		`onpointerdown="quickKeyPaste(event)"`,
		// Path B wiring on the PR1 shell.
		`onpaste="quickKeysPasteCaptured(event)"`,
		`onpointerdown="cancelQuickKeysPaste(event)"`,
	} {
		if !strings.Contains(bodyText, check) {
			t.Fatalf("GET / should include the PR2 clipboard DOM anchor %q (issue #100 R21/R22)", check)
		}
	}
	for _, check := range []string{
		"const QUICK_KEYS_PASTE_MAX_CHARS = 256 * 1024;",
		"let quickKeysPasteSession = null;",
		"function normalizePasteNewlines(text)",
		"function stripBracketedPasteMarkers(text)",
		"function maybeWrapBracketedPaste(text, bracketedPasteMode)",
		"function extractViewportText(term)",
		"function deliverQuickKeysPaste(text, session)",
		"function quickKeyPaste(event)",
		"function enterQuickKeysPasteFallback(session)",
		"function quickKeysPasteCaptured(event)",
		"function cancelQuickKeysPaste(event)",
		"function quickKeyCopy(event)",
		"function writeQuickKeysClipboard(text)",
		"function fallbackExecCommandCopy(text)",
	} {
		if !strings.Contains(bodyText, check) {
			t.Fatalf("GET / should include the PR2 clipboard function anchor %q (issue #100 R21/R22)", check)
		}
	}
	// Pipeline order inside the delivery function: normalize -> strip ->
	// CAP -> WRAP -> the shared input gate. The cap runs on the pre-wrap
	// text so the closing \x1b[201~ can never be truncated away.
	deliver := sliceJSFunction(t, bodyText, "deliverQuickKeysPaste")
	// The two guards the onData gate cannot provide (review round 2): a
	// disposed-but-non-null term (pty.js disposes without nulling) throws
	// before the gate — caught and fed back; a cap cut must not leave a
	// lone high surrogate; and a marker-only clipboard must not become an
	// empty frame (post-strip empty re-check).
	for _, guard := range []string{
		`updateStatus("Paste failed: session closed")`,
		`/[\uD800-\uDBFF]/`,
		`if (payload === "")`,
	} {
		if !strings.Contains(deliver, guard) {
			t.Fatalf("GET / deliverQuickKeysPaste should include the guard %q (issue #100 R21)", guard)
		}
	}
	normAt := strings.Index(deliver, "normalizePasteNewlines(text)")
	stripAt := strings.Index(deliver, "stripBracketedPasteMarkers(payload)")
	capAt := strings.Index(deliver, "QUICK_KEYS_PASTE_MAX_CHARS")
	wrapAt := strings.Index(deliver, "maybeWrapBracketedPaste(payload, bracketed)")
	gateAt := strings.Index(deliver, "sendQuickKeyBytes(session, payload)")
	if normAt < 0 || stripAt < 0 || wrapAt < 0 || capAt < 0 || gateAt < 0 ||
		normAt > stripAt || stripAt > capAt || capAt > wrapAt || wrapAt > gateAt {
		t.Fatal("GET / deliverQuickKeysPaste must run normalize -> strip -> cap -> wrap -> sendQuickKeyBytes in that order (issue #100 R21)")
	}
	// The delivery takes its session from the CALLER (captured at
	// pointerdown) — it must not re-fetch the active one (§3.3 ownership).
	if strings.Contains(deliver, "getActiveTerminalSession") {
		t.Fatal("GET / deliverQuickKeysPaste must receive the session from its caller — re-fetching the active one misdelivers across an instance switch (issue #100 R21)")
	}
	// The bracketed wrap reads modes live; no alt-screen special case.
	if strings.Contains(deliver, "buffer.active === buffer.alternate") || strings.Contains(deliver, "isAltScreen") {
		t.Fatal("GET / deliverQuickKeysPaste must NOT special-case the alt screen — the wrap rule mirrors the vendored xterm (issue #100 R21)")
	}
	// Path dispatch is capability-based; no remote/backend branch anywhere
	// near the clipboard code.
	pasteFn := sliceJSFunction(t, bodyText, "quickKeyPaste")
	if !strings.Contains(pasteFn, "navigator.clipboard") || !strings.Contains(pasteFn, "readText") {
		t.Fatal("GET / quickKeyPaste must dispatch on navigator.clipboard.readText (issue #100 R21)")
	}
	if !strings.Contains(pasteFn, "enterQuickKeysPasteFallback(session)") {
		t.Fatal("GET / quickKeyPaste must drop to the fallback on refusal AND absence (issue #100 R21, §7 risk mitigation)")
	}
	for _, fn := range []string{
		"quickKeyPaste", "quickKeyCopy", "writeQuickKeysClipboard", "deliverQuickKeysPaste",
		"quickKeysPasteCaptured", "cancelQuickKeysPaste", "fallbackExecCommandCopy", "extractViewportText",
	} {
		if strings.Contains(sliceJSFunction(t, bodyText, fn), "isRemoteAccess") {
			t.Fatalf("GET / %s must not branch on isRemoteAccess — the clipboard API is browser-side, the client-system attribution is automatic (issue #100 R21)", fn)
		}
	}
	// The fallback state is owned by its session: sync resets the content
	// when the active session changes mid-fallback.
	syncFn2 := sliceJSFunction(t, bodyText, "syncQuickKeysPanel")
	if !strings.Contains(syncFn2, "quickKeysPasteSession && quickKeysPasteSession !== session") {
		t.Fatal("GET / syncQuickKeysPanel must reset the paste fallback content when the active session no longer owns it (issue #100 R19/R21)")
	}
	// Review-round-3 guards: the queue flush tolerates a disposed term
	// (same root cause as the delivery catch, drops the remainder), the
	// fallback entry checks the panel is actually open, and the paste
	// entry refuses BEFORE the clipboard dispatch when there is no
	// terminal to paste into.
	flushFn := sliceJSFunction(t, bodyText, "flushQuickKeysQueue")
	if !strings.Contains(flushFn, "catch") || !strings.Contains(flushFn, "Same root cause as deliverQuickKeysPaste") {
		t.Fatal("GET / flushQuickKeysQueue must tolerate a disposed term and drop the remainder silently (issue #100 R20)")
	}
	fallbackFn2 := sliceJSFunction(t, bodyText, "enterQuickKeysPasteFallback")
	if !strings.Contains(fallbackFn2, "deriveQuickKeysVisibility(session)") {
		t.Fatal("GET / enterQuickKeysPasteFallback must not flip a hidden panel into paste-mode (issue #100 R21)")
	}
	noTermAt := strings.Index(pasteFn, `updateStatus("Paste failed: no active terminal")`)
	dispatchAt := strings.Index(pasteFn, "navigator.clipboard")
	if noTermAt < 0 || dispatchAt < 0 || noTermAt > dispatchAt {
		t.Fatal("GET / quickKeyPaste must refuse a missing terminal BEFORE the clipboard dispatch — no permission prompt for a no-op (issue #100 R21)")
	}
	// PR #112: capture without clipboardData harvests on the next TASK
	// tick (setTimeout — a microtask could beat the default action), and
	// the IME binding carries a blur failsafe for engines that never fire
	// compositionend.
	capturedFn := sliceJSFunction(t, bodyText, "quickKeysPasteCaptured")
	if !strings.Contains(capturedFn, "setTimeout") {
		t.Fatal("GET / quickKeysPasteCaptured must harvest a clipboardData-less paste on the next task tick (PR #112 M-2)")
	}
	imeFn := sliceJSFunction(t, bodyText, "ensureQuickKeysImeBinding")
	if !strings.Contains(imeFn, `"blur"`) {
		t.Fatal("GET / ensureQuickKeysImeBinding must carry the blur failsafe for abandoned compositions (PR #112 M-3)")
	}
	copyFn := sliceJSFunction(t, bodyText, "quickKeyCopy")
	if !strings.Contains(copyFn, "term.hasSelection()") || !strings.Contains(copyFn, "extractViewportText(term)") {
		t.Fatal("GET / quickKeyCopy must prefer a live selection and fall back to the viewport text (issue #100 R22)")
	}
	writeFn := sliceJSFunction(t, bodyText, "writeQuickKeysClipboard")
	if !strings.Contains(writeFn, "navigator.clipboard.writeText") || !strings.Contains(writeFn, "fallbackExecCommandCopy(text)") {
		t.Fatal("GET / writeQuickKeysClipboard must write via writeText with the execCommand fallback (issue #100 R22)")
	}
	fallbackFn := sliceJSFunction(t, bodyText, "fallbackExecCommandCopy")
	if !strings.Contains(fallbackFn, `document.execCommand("copy")`) {
		t.Fatal("GET / fallbackExecCommandCopy must drive execCommand('copy') (issue #100 R22)")
	}
	// The viewport extractor reads buffer.active — the pty/TUI split is
	// automatic, never special-cased.
	viewportFn := sliceJSFunction(t, bodyText, "extractViewportText")
	if !strings.Contains(viewportFn, "term.buffer.active") || !strings.Contains(viewportFn, "translateToString(true)") {
		t.Fatal("GET / extractViewportText must read term.buffer.active and translateToString(true) (issue #100 R22)")
	}
}

func TestReasonixRendererKeepsFrameAlive(t *testing.T) {
	mux := http.NewServeMux()
	if err := Register(mux, "myworktree", nil); err != nil {
		t.Fatal(err)
	}
	srv := httptest.NewServer(mux)
	defer srv.Close()

	resp, err := http.Get(srv.URL + "/static/kinds/reasonix.js")
	if err != nil {
		t.Fatalf("GET /static/kinds/reasonix.js failed: %v", err)
	}
	body, _ := io.ReadAll(resp.Body)
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("GET /static/kinds/reasonix.js status: %d", resp.StatusCode)
	}
	js := string(body)

	checks := []string{
		// issue #53: re-navigation is guarded by the dataset (id + src);
		// assigning the same src would reload the embedded page.
		"frame.dataset.instance !== id || frame.dataset.src !== src",
		// hide instead of destroying the iframe on tab switch so the
		// embedded page (chat draft, scroll, sidebar) survives.
		"this._currentFrame.hidden = true",
		// per-instance iframe cache: cross-instance switches only
		// hide/show frames instead of re-navigating, so instance A's
		// draft survives even after switching to B and back (goes
		// beyond main's single shared frame, which reloaded).
		"this._frames = new Map()",
		"this._frames.set(id, frame)",
		"this._showOnly(frame)",
		// a fresh iframe starts with an empty navigation cache, so the
		// first activation navigates; the same clearing covers the
		// stop→start same-src fallback.
		"frame.dataset.instance = '';",
		"frame.dataset.src = '';",
		// only a running (or still-flipping starting) instance has a
		// live page to load.
		"inst.status !== 'running' && inst.status !== 'starting'",
		// 2s self-healing poll: re-navigates on web_url port changes
		// (daemon restart) and invalidates on stop/failure — main ran
		// the same check on every render tick.
		"setInterval",
		// issue #54: hide leftover xterm hosts so they cannot overlay the
		// static iframe.
		"renderTerminalSessions();",
		// issue #55: normalize container styles left behind by xterm so the
		// web UI is never rendered greyed out.
		`termContainer.style.opacity = "1";`,
		`termContainer.style.filter = "none";`,
		// web_url (cross-origin listener) preferred, same-origin /rx/ fallback.
		`"/rx/" + id + "/"`,
		"inst.web_url",
		// shell hook: stop/delete releases the instance's frame.
		"destroyFrame(id) {",
		// panel mutual exclusion (no stacked half/half layout).
		"rxPanel.hidden = true",
		// lifecycle diagnostics for white-screen reports.
		"[reasonix-renderer]",
	}
	for _, check := range checks {
		if !strings.Contains(js, check) {
			t.Fatalf("reasonix.js should include keep-alive hook %q", check)
		}
	}
}

// TestReasonixTabIsFixedCommandNoTemplate pins decision B: the Reasonix
// start tab has no template picker — the serve command is fixed and the
// UI sends an empty tag_id (tag env/preStart remain reachable via the
// API/CLI tag_id parameter, backend support is intentionally kept).
func TestReasonixTabIsFixedCommandNoTemplate(t *testing.T) {
	bodyText := fetchIndexHTML(t)
	checks := []string{
		`data-tab="reasonix"`,
		"kind: 'reasonix',",
		"tag_id: '',",
		// The renderer must actually be loaded, or selectInstance falls
		// back to the PTY path (TTY WS → "does not support output
		// subscription" loop) and the web UI never renders.
		`<script src="/static/kinds/reasonix.js">`,
	}
	for _, check := range checks {
		if !strings.Contains(bodyText, check) {
			t.Fatalf("GET / should include reasonix fixed-command hook %q", check)
		}
	}
	if strings.Contains(bodyText, "tagSelectRx") {
		t.Fatalf("GET / must not contain the removed reasonix template select (decision B)")
	}
}

// TestStartInstanceModalTabLabelsAndWrap pins issues #68 and #69: the four
// Start Instance tabs follow one naming scheme (Title Case + consistent
// -Web suffix for the web kinds) while the data-tab values (the backend
// kind mapping) stay unchanged, and the tab bar never wraps — the dialog
// width follows its content (max-content) so all four tabs fit on one
// line, with min/max caps so it neither shrinks below the other dialogs
// nor blows up on long form-info text.
func TestStartInstanceModalTabLabelsAndWrap(t *testing.T) {
	bodyText := fetchIndexHTML(t)
	checks := []string{
		`switchStartInstanceTab('terminal')">Terminal</button>`,
		`switchStartInstanceTab('opencode-web')">OpenCode-Web</button>`,
		`switchStartInstanceTab('reasonix')">Reasonix-Web</button>`,
		`switchStartInstanceTab('dsh-web')">DSH-Web</button>`,
	}
	for _, check := range checks {
		if !strings.Contains(bodyText, check) {
			t.Fatalf("GET / should include modal tab %q (issue #69 naming scheme)", check)
		}
	}
	// Issue #68: no scrollbar band-aid — the dialog resizes itself to fit
	// the tab bar; tabs keep their natural width and never wrap. The
	// .modal-tabs block must match exactly (no overflow-x inside).
	tabsBlock := "        .modal-tabs {\n            display: flex;\n            flex-wrap: nowrap;\n            gap: 0;\n            padding: 0 12px;\n            border-bottom: 1px solid var(--border-color);\n            background: var(--hover-bg);\n        }"
	for _, check := range []string{
		"flex-wrap: nowrap",
		"white-space: nowrap",
		"width: max-content",
		"min-width: 400px",
		"max-width: min(90vw, 640px)",
	} {
		if !strings.Contains(bodyText, check) {
			t.Fatalf("GET / should include modal-tabs no-wrap guard %q (issue #68)", check)
		}
	}
	if !strings.Contains(bodyText, tabsBlock) {
		t.Fatalf("GET / should include the scrollbar-free .modal-tabs block (issue #68)")
	}
	// data-tab semantics preserved for every web kind.
	for _, check := range []string{
		`data-tab="terminal"`,
		`data-tab="opencode-web"`,
		`data-tab="reasonix"`,
		`data-tab="dsh-web"`,
	} {
		if !strings.Contains(bodyText, check) {
			t.Fatalf("GET / should keep data-tab %q", check)
		}
	}
}

// TestStartInstanceModalFormInfosAreEnglish pins issues #71 and #73: every
// tab (including Terminal) has a .form-info explaining it, and all of them
// are written in English like the rest of the modal — no Chinese copy.
func TestStartInstanceModalFormInfosAreEnglish(t *testing.T) {
	bodyText := fetchIndexHTML(t)
	checks := []string{
		// Terminal tab form-info (issue #71).
		"Open a PTY terminal in the selected worktree.",
		"this tab starts no web service and embeds no iframe",
		// Web tab form-infos (issue #73, English drafts).
		"Use the opencode native web UI in your browser.",
		"Use the Reasonix web chat UI in your browser.",
		"Use the DeepSeek Harness official web UI in your browser.",
		"session pool",
		"out-of-scope actions only warn",
	}
	for _, check := range checks {
		if !strings.Contains(bodyText, check) {
			t.Fatalf("GET / should include form-info text %q (issues #71/#73)", check)
		}
	}
	for _, banned := range []string{"在浏览器里", "dsh 未安装", "未安装", "用 npx 启动"} {
		if strings.Contains(bodyText, banned) {
			t.Fatalf("GET / must not contain Chinese UI copy %q (issue #73)", banned)
		}
	}
}

// TestKindBadgesUnified pins issue #70: the OC / RX / DS instance-tab
// badges share one .kind-badge base style, per-kind rules only set colors
// (through CSS variables), and the old divergent classes are gone.
func TestKindBadgesUnified(t *testing.T) {
	bodyText := fetchIndexHTML(t)
	checks := []string{
		`class="kind-badge kind-badge-oc"`,
		`class="kind-badge kind-badge-rx"`,
		`class="kind-badge kind-badge-dsh"`,
		"--badge-dsh-bg",
		"--badge-dsh-text",
	}
	for _, check := range checks {
		if !strings.Contains(bodyText, check) {
			t.Fatalf("GET / should include unified kind badge %q (issue #70)", check)
		}
	}
	for _, legacy := range []string{
		`class="badge-oc"`, `class="rx-badge"`, `class="badge-dsh"`,
		".badge-oc {", ".rx-badge {", ".badge-dsh {",
	} {
		if strings.Contains(bodyText, legacy) {
			t.Fatalf("GET / must not contain legacy divergent badge class %q (issue #70)", legacy)
		}
	}
}

// TestIndexHTMLInlineScriptIsIndented catches a block pasted into the inline
// script at the wrong indentation. Both halves of that defect are invisible in
// review and survive every behavioural test, since the sliced functions
// evaluate the same either way:
//
//   - a declaration at column 0 (an exact-8-spaces rule would flag every nested
//     `const`, so the check stays one-sided);
//   - a comment block whose lines disagree among themselves, which is how a
//     moved block strands one line behind and leaves an explanation sitting on
//     the wrong neighbour.
//
// TestUICopyIsEnglishOnly pins the single-language English UI direction
// (issue #73) across the whole served UI, not just index.html: the web-kind
// renderer JS files (dsh_web.js / opencode_web.js / reasonix.js) and
// framework.js must contain no CJK characters at all — warning bars,
// loading titles, the missing-dependency dialog and any future copy are
// covered even when they live in JS. Key user-visible strings are also
// asserted positively per file so a reword cannot silently hide a CJK
// regression behind a full-file rewrite.
func TestIndexHTMLInlineScriptIsIndented(t *testing.T) {
	bodyText := fetchStaticAsset(t, "/")
	start := strings.Index(bodyText, "<script>\n")
	end := strings.LastIndex(bodyText, "\n    </script>")
	if start < 0 || end < start {
		t.Fatal("GET / no inline <script> block found")
	}
	script := bodyText[start+len("<script>\n") : end]

	// Declarations: only a column-0 hit is provably wrong here. Requiring exactly
	// 8 spaces would flag every nested `const`, and deciding top level properly
	// needs a parser. The comment check below is what covers the
	// over-indented case, which is how a moved block actually shows up.
	decl := regexp.MustCompile(`(?m)^( *)(async function |function |const |let |var )`)
	for i, line := range strings.Split(script, "\n") {
		if m := decl.FindStringSubmatch(line); m != nil && len(m[1]) == 0 {
			t.Errorf("GET / inline script line %d starts a declaration at column 0: %s", i+1, line)
		}
	}

	// A comment run is a contiguous block of `//` lines and every line in it
	// has to sit at the same depth. Single-line runs are exempt: a closing
	// brace followed by one top-level comment legitimately changes depth.
	comment := regexp.MustCompile(`^(\s*)//`)
	var run []string
	flush := func() {
		if len(run) > 1 {
			// Compare against the run's minimum, not its first line: a block
			// whose odd line happens to be line 1 and one whose odd line is in
			// the middle are the same defect, and only the minimum catches
			// both. Extra indentation inside a comment goes after the slashes
			// in this file, which is why a stepped block is not the style here.
			want := -1
			for _, l := range run {
				if d := len(comment.FindStringSubmatch(l)[1]); want < 0 || d < want {
					want = d
				}
			}
			for _, l := range run {
				if got := len(comment.FindStringSubmatch(l)[1]); got != want {
					t.Errorf("GET / inline script comment block mixes indents (%d and %d): %q", want, got, run[0])
					break
				}
			}
		}
		run = nil
	}
	for _, line := range strings.Split(script, "\n") {
		if comment.MatchString(line) && !strings.HasPrefix(strings.TrimSpace(line), "///") {
			run = append(run, line)
			continue
		}
		flush()
	}
	flush()
}

func TestUICopyIsEnglishOnly(t *testing.T) {
	paths := map[string][]string{
		"/": {
			"Open a PTY terminal in the selected worktree.",
			"Use the opencode native web UI in your browser.",
			"Use the Reasonix web chat UI in your browser.",
			"Use the DeepSeek Harness official web UI in your browser.",
			"Launch with npx (recommended)",
			"dsh is not installed",
		},
		"/static/kinds/dsh_web.js": {
			"dsh is not installed",
			"Remote access requires authentication",
			"Startup timed out (60s)",
			"dsh has left the worktree scope",
			"Operation failed",
			"Starting dsh server",
		},
		"/static/kinds/opencode_web.js": {
			"opencode instance stopped",
			"Starting opencode server",
			"Startup timed out (60s)",
			"opencode has left the worktree scope",
		},
		"/static/kinds/reasonix.js": nil,
		"/static/framework.js":      nil,
	}
	for path, mustContain := range paths {
		body := fetchStaticPath(t, path)
		for _, s := range mustContain {
			if !strings.Contains(body, s) {
				t.Fatalf("GET %s should include UI copy %q (issue #73)", path, s)
			}
		}
		for _, r := range []rune(body) {
			if r >= 0x4e00 && r <= 0x9fff {
				t.Fatalf("GET %s contains CJK character %q — UI copy must be English-only (issue #73)", path, r)
			}
		}
	}
}

func TestIndexHTMLCoversSessionLifecycle(t *testing.T) {
	bodyText := fetchIndexHTML(t)
	checks := []string{
		"function createTerminalSession(id)",
		"terminalSessions[id] = session;",
		"function destroyTerminalSession(id)",
		"session.termDataDisposable.dispose();",
		"session.resizeObserver.disconnect();",
		"session.term.dispose();",
		"session.container.parentNode.removeChild(session.container);",
		"delete terminalSessions[id];",
	}
	for _, check := range checks {
		if !strings.Contains(bodyText, check) {
			t.Fatalf("GET / should include session lifecycle hook %q", check)
		}
	}
}

func TestIndexHTMLCoversReconcileLogic(t *testing.T) {
	bodyText := fetchIndexHTML(t)
	checks := []string{
		"function reconcileTerminalSessions()",
		"destroyTerminalSession(id);",
		"disconnectTTY(session);",
		"function reconnectRunningTerminalSessions()",
		// The live bucket, not `status === 'running'`: an unhealthy instance is
		// still a live process and must keep its transport (issue #80).
		"if (inst && isInstanceLiveStatus(inst.status) && !hasLiveTTYConnection(session.id)) {",
		"connectTTY(session);",
	}
	for _, check := range checks {
		if !strings.Contains(bodyText, check) {
			t.Fatalf("GET / should include reconcile hook %q", check)
		}
	}
}

func TestIndexHTMLCoversPerSessionConnectionManagement(t *testing.T) {
	bodyText := fetchIndexHTML(t)
	checks := []string{
		"session.ttySocket && session.ttySocket.readyState === WebSocket.OPEN",
		"if (session.ttySocket) { session.ttySocket.close(); session.ttySocket = null; }",
		"session.ttySocket = ws;",
		"if (session.ttySocket === ws) {",
		"session.ttyReconnectTimer = setTimeout(() => connectTTY(session), TTY_RECONNECT_DELAY_MS);",
	}
	for _, check := range checks {
		if !strings.Contains(bodyText, check) {
			t.Fatalf("GET / should include per-session connection management hook %q", check)
		}
	}
}

// TestLogCursorBootstrapNeverRequestsOldestBytes pins the client half of
// the issue #81 tail contract. The server treats an omitted `since` as a
// tail request and `since=0` as "read from the oldest live byte", so the
// browser must never emit `since=0`: startSSE builds the parameter from
// `sseCursor` and appends it only when that value is a real positive byte
// cursor, and a cursor arriving over the stream is accepted under one rule.
//
// Since issue #87 the same suppression guards a bigger cursor: `sseCursor`
// is the greater of `logCursor` and `ttyOffset`, because the two siblings
// count the same ring-buffer bytes and the poll-driven promotion advances
// `ttyOffset` with `connectTTY` alone — reading `logCursor` there would
// omit `since` and replay the server's whole tail on top of what the
// WebSocket already painted.
//
// It also pins the invalidation half of that contract, which is the same
// rule seen from the other side: a screen that no longer holds bytes must
// not leave a cursor claiming it does. So `resetTerminalForSwitch`
// invalidates BOTH cursors itself, `loadLog` pins its cursors only where
// the bytes actually land, and `ws.onmessage` moves the session cursor only
// for the socket that currently owns the session.
//
// This covers index.html only — the legacy no-renderer fallback. The pty
// renderer's own bootstrap is pinned by TestPtyRendererResetsCursorAfter-
// TerminalReset; the two together are the complete picture, and neither
// test alone is.
func TestLogCursorBootstrapNeverRequestsOldestBytes(t *testing.T) {
	bodyText := fetchIndexHTML(t)
	checks := []string{
		// The shared SSE connect helper omits `since` unless a real byte
		// cursor exists; this is what protects every kind. Issue #87 made
		// the cursor the GREATER of the two siblings — logCursor and
		// ttyOffset count the same ring-buffer bytes, and the poll-driven
		// promotion advances ttyOffset through connectTTY alone, so reading
		// logCursor alone would omit `since` here and replay the whole tail
		// over what the WebSocket already painted. The #81 suppression rule
		// itself is unchanged: only a real positive cursor becomes `since`,
		// and each sibling degrades through `??` to the -1 sentinel so an
		// absent one cannot turn the max into NaN and swallow the cursor.
		"const sseCursor = Math.max(session.logCursor ?? -1, session.ttyOffset ?? -1);",
		"if (sseCursor > 0) url += `&since=${sseCursor}`;",
		// loadLog must not send an explicit offset, and must not fabricate
		// one when the header is missing.
		"const res = await fetch(`/api/instances/log?id=${session.id}`",
		"if (Number.isFinite(next) && next >= 0) {",
		// Same acceptance rule for cursor values arriving over the stream,
		// and one rule for BOTH cursors (issue #87): every byte SSE paints
		// is a byte this screen has seen, so the WebSocket cursor advances
		// with it and a later reconnect never re-requests rendered bytes.
		"if (Number.isFinite(msg.next) && msg.next >= 0) {",
		"session.logCursor = msg.next;",
		"session.ttyOffset = msg.next;",
		// Legacy no-renderer fallback bootstraps the named "nothing painted"
		// state (issue #86 named these; the value stays -1).
		"const CURSOR_UNKNOWN = -1;",
		"logCursor: CURSOR_UNKNOWN,",
		// Issue #86: the OTHER unknown-cursor state — the tail IS painted,
		// only its offset was stripped — has to be a distinct value the
		// server branches on, and both transports must send it explicitly.
		// An unparseable header must never degrade to since=0 (the oldest
		// live byte, #81) nor to the plain-unknown tail request. The SSE
		// fallback reads BOTH siblings, so "outranks -2 on either side"
		// holds in the code and not just in the prose: today every site
		// assigns the cursors as a pair, so the split it guards is
		// unreachable and the check is bit-identical in every state that
		// exists today. connectTTY's guard reads ttyOffset ALONE, and
		// that is complete there: every logCursor write is paired with an
		// equal ttyOffset write at the same site, and only ttyOffset ever
		// advances alone, so no reachable state lets the sibling hold
		// screen truth ttyOffset does not.
		"const CURSOR_FOLLOW_LIVE_END = -2;",
		"session.logCursor = CURSOR_FOLLOW_LIVE_END;",
		"else if (session.logCursor === CURSOR_FOLLOW_LIVE_END || session.ttyOffset === CURSOR_FOLLOW_LIVE_END) url += `&since=${CURSOR_FOLLOW_LIVE_END}`;",
		"else if (session.ttyOffset === CURSOR_FOLLOW_LIVE_END) url += `&since=${CURSOR_FOLLOW_LIVE_END}`;",
	}
	for _, check := range checks {
		if !strings.Contains(bodyText, check) {
			t.Fatalf("GET / should include log cursor bootstrap hook %q", check)
		}
	}

	if strings.Contains(bodyText, "/api/instances/log/stream?id=${encodeURIComponent(session.id)}&since=${session.logCursor}") {
		t.Fatalf("GET / must not build an unconditional since= URL from session.logCursor")
	}
	if strings.Contains(bodyText, "/api/instances/log/stream?id=${encodeURIComponent(session.id)}&since=${session.ttyOffset}") {
		t.Fatalf("GET / must not build an unconditional since= URL from session.ttyOffset")
	}
	if strings.Contains(bodyText, "/api/instances/log?id=${session.id}&since=0") {
		t.Fatalf("GET / must not request since=0, which the server reads as the oldest live byte")
	}

	// A cleared terminal invalidates any previous cursor: retaining it would
	// make startSSE request since=<oldOffset> and repaint only the post-offset
	// delta onto an empty screen. Mirrors the ordering assertion the pty
	// renderer test makes about the same two statements.
	resetAt := strings.Index(bodyText, "resetTerminalForSwitch(session);")
	cursorAt := strings.Index(bodyText, "session.logCursor = CURSOR_UNKNOWN;")
	if cursorAt < 0 {
		t.Fatal("GET / should reset session.logCursor after clearing the terminal")
	}
	if resetAt < 0 {
		t.Fatal("GET / fallback should clear the terminal before reloading the log")
	}
	if cursorAt < resetAt {
		t.Fatal("GET / should reset session.logCursor after resetTerminalForSwitch, not before")
	}

	// Source contract for the issue #87 review round, pinned the way this
	// file already scopes a pin to a block: a contiguous literal whose exact
	// indentation is part of the check, so the statements must sit at the
	// depth they belong to. Full-file offsets are deliberately not used.
	for _, check := range []string{
		// resetTerminalForSwitch owns the screen, so it invalidates BOTH
		// cursors itself, inside its own screen-content guard: it is
		// exported on window for other kinds to call, and a caller that
		// cleared the screen through it while keeping a stale ttyOffset
		// would make the next connectTTY send since=<oldHead> and paint
		// only [oldHead, head) onto a blank terminal. The guard is the
		// anchor — with no term nothing was cleared, so nothing is
		// invalidated. The literal's uniqueness comes from this pair's own
		// shape: resetTerminalForSwitch's two statements sit at 12-space
		// indent and list ttyOffset FIRST, while refreshCurrentInstance's
		// defensive pair sits at 24 spaces and lists logCursor first
		// (index.html) — indent and order together are the discriminator,
		// not the closing brace. The callers keep their own resets as
		// depth.
		"function resetTerminalForSwitch(session) {\n            if (!session || !session.term) return;",
		"session.ttyOffset = CURSOR_UNKNOWN;\n            session.logCursor = CURSOR_UNKNOWN;\n        }",
		// loadLog pins cursors only where the bytes actually land: inside
		// its if (session.term) block, after the paint, and under the same
		// one-rule acceptance as before. Pinning a cursor for bytes no
		// screen holds would contradict the screen-content rule the whole
		// path is built on.
		"if (Number.isFinite(next) && next >= 0) {\n                        session.logCursor = next;\n                        session.ttyOffset = next;\n                    }",
		// ws.onmessage mutates the session-wide cursor, so it runs only for
		// the socket that currently owns the session: a frame dispatched off
		// a replaced socket must never move the live socket's cursor and
		// claim bytes the live transport never painted. Unique in the file,
		// so the Contains check is this guard and not a look-alike.
		"if (session.ttySocket !== ws) return;",
	} {
		if !strings.Contains(bodyText, check) {
			t.Fatalf("GET / should pin the cursor-invalidation source contract: missing %q", check)
		}
	}
}

func TestPtyRendererResetsCursorAfterTerminalReset(t *testing.T) {
	mux := http.NewServeMux()
	if err := Register(mux, "myworktree", nil); err != nil {
		t.Fatal(err)
	}
	srv := httptest.NewServer(mux)
	defer srv.Close()

	resp, err := http.Get(srv.URL + "/static/kinds/pty.js")
	if err != nil {
		t.Fatalf("GET /static/kinds/pty.js failed: %v", err)
	}
	body, _ := io.ReadAll(resp.Body)
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("GET /static/kinds/pty.js status: %d", resp.StatusCode)
	}
	js := string(body)

	// The pty kind is the only one with a ring buffer, so it is the one
	// where a cursor bug is user-visible. activate() clears the terminal
	// and then resets the cursor to the unknown sentinel: if loadLog fails
	// afterwards, startSSE must fall back to a full tail rather than resume
	// from a stale offset and repaint only a delta onto the cleared screen.
	// Companion to TestLogCursorBootstrapNeverRequestsOldestBytes, which pins
	// the same rule in index.html's legacy fallback.
	if !strings.Contains(js, "window.resetTerminalForSwitch(s);") {
		t.Fatal("pty.js should clear the terminal before reloading the log")
	}
	resetAt := strings.Index(js, "window.resetTerminalForSwitch(s);")
	cursorAt := strings.Index(js, "s.logCursor = PTY_CURSOR_UNKNOWN;")
	if cursorAt < 0 {
		t.Fatal("pty.js should reset s.logCursor to the named unknown state after clearing the terminal")
	}
	if cursorAt < resetAt {
		t.Fatal("pty.js should reset s.logCursor after resetTerminalForSwitch, not before")
	}
}

// TestTerminalStatusBucketsReplaceRunningEquality is the source-level contract
// for issue #80: a newly created instance is persisted as `starting` and only
// flips to `running` on the kind's ready signal, so "status !== 'running'" must
// never gate terminal I/O, the [Process Stopped] banner, or the connect path.
//
// The behavior itself (including the poll-driven promotion that replaces the
// abandoned selection-time activation) is covered by
// TestTerminalStatusHandling, which runs the same sources under `node --test`.
// This test pins the two halves of the fix that a refactor would most easily
// undo silently: the shared bucket helpers, and the fact that BOTH activation
// paths — index.html's legacy no-renderer fallback and kinds/pty.js — classify
// the status instead of comparing it to the literal "running".
func TestTerminalStatusBucketsReplaceRunningEquality(t *testing.T) {
	bodyText := fetchIndexHTML(t)
	js := fetchStaticAsset(t, "/static/kinds/pty.js")

	// Single source of truth for the three buckets, exported for pty.js.
	for _, check := range []string{
		"const INSTANCE_LIVE_STATUSES = new Set(['running', 'unhealthy']);",
		"const INSTANCE_PENDING_STATUSES = new Set(['starting', 'stopping']);",
		"function isInstanceLiveStatus(status)",
		"function isInstancePendingStatus(status)",
		"function isInstanceTerminalStatus(status)",
		"window.isInstanceLiveStatus = isInstanceLiveStatus;",
		"window.isInstancePendingStatus = isInstancePendingStatus;",
		"window.isInstanceTerminalStatus = isInstanceTerminalStatus;",
	} {
		if !strings.Contains(bodyText, check) {
			t.Fatalf("GET / should define the shared status buckets: missing %q", check)
		}
	}

	// Poll-driven self-heal: reconcileTerminalSessions() promotes an active
	// session once its status turns live, and refuses to disturb a session that
	// already has a transport in flight (that guard is what stops the 2s poll
	// from flapping a CONNECTING socket).
	for _, check := range []string{
		"function syncActiveTerminalStatus(session, inst)",
		"if (isInstanceLiveStatus(inst.status)) {\n                ensureTerminalLiveTransport(session);",
		"function ensureTerminalLiveTransport(session)",
		"if (hasTerminalTransportInFlight(session)) return false;",
		// The guard is one function so the poll-driven promotion and
		// kinds/pty.js cannot drift apart; each entry below is one way a second
		// owner of the bring-up shows up.
		"function hasTerminalTransportInFlight(session, options)",
		"if (session.loadLogController) return true;",
		// Issue #83: every socket/state arm is gated on !stale - a
		// READY-but-heartbeat-silent socket is NOT in flight, or the
		// guard would veto the very reconnect the liveness fix unblocks.
		"if (session.ttySocket && !stale) return true;",
		"if (session.logStream) return true;",
		"if (session.ttyState && session.ttyState !== 'IDLE' && !stale) return true;",
		// The promotion is the bystander: it lets a queued retry fire, so it
		// passes no options and therefore respects the timer arm.
		"if (session.ttyReconnectTimer && !(options && options.ignoreQueuedRetry)) return true;",
	} {
		if !strings.Contains(bodyText, check) {
			t.Fatalf("GET / should include the reconcile promotion hook %q", check)
		}
	}

	// Both activation paths must classify the status. pty.js is the live path;
	// the legacy fallback in index.html only runs when a renderer script fails
	// to load, and the two must not drift apart (same rule as the log cursor).
	for _, check := range []string{
		"const isPending = window.isInstancePendingStatus",
		"if (isPending) {",
		// Activation shares the promotion's in-flight guard; without it a tab
		// switch during bring-up resets the screen and opens a second socket.
		"if (window.hasTerminalTransportInFlight\n            && window.hasTerminalTransportInFlight(s, { ignoreQueuedRetry: true })) {",
		// The early return is not silent: deactivate() leaves the status bar on
		// "idle", so re-selecting a tab mid bring-up would look inert until the
		// connect finished. It says connecting... and stays quiet over a live
		// SSE stream, whose own message is the accurate one.
		"if (window.updateStatus && !s.logStream) {\n                window.updateStatus('connecting...');\n            }",
		// ...and the takeover cancels the queued retry itself rather than
		// leaving it to disconnectTTY, which activate() only reaches after
		// loadLog() settles — a retry firing in between opens two sockets and
		// replays the tail twice.
		"if (s.ttyReconnectTimer) {\n            clearTimeout(s.ttyReconnectTimer);\n            s.ttyReconnectTimer = null;\n        }",
	} {
		if !strings.Contains(js, check) {
			t.Fatalf("GET /static/kinds/pty.js should classify the status: missing %q", check)
		}
	}
	for _, check := range []string{
		// Anchored on the comment: index.html has a second `if (!inst) {` in
		// reconcileTerminalSessions, so the bare line would match either.
		"if (!inst) {\n                    // Nothing to drive; leave the session alone.",
		"} else if (isInstancePendingStatus(inst.status)) {",
		"} else if (isInstanceTerminalStatus(inst.status)) {",
		"} else if (hasTerminalTransportInFlight(session, { ignoreQueuedRetry: true })) {",
		"updateStatus(\"connecting...\");",
		// ...and the takeover cancels the queued retry itself, as in pty.js.
		"if (session.ttyReconnectTimer) {\n                            clearTimeout(session.ttyReconnectTimer);\n                            session.ttyReconnectTimer = null;",
	} {
		if !strings.Contains(bodyText, check) {
			t.Fatalf("GET / legacy fallback should classify the status: missing %q", check)
		}
	}

	// The stopped banner is terminal-bucket-only in both paths.
	if !strings.Contains(bodyText, "if (inst && isInstanceTerminalStatus(inst.status)) {\n                        session.term.write(\"\\r\\n\\r\\n\\x1b[41;37m[Process Stopped]\\x1b[0m\\r\\n\");") {
		t.Fatal("GET / should paint [Process Stopped] only for terminal-bucket statuses")
	}

	// And the equality tests this replaces must not come back. The `===`
	// forms matter as much as the `!==` ones: index.html shipped two
	// `status === 'running'` status-bar labels that read "stopped" for a
	// seconds-long `starting` window next to a live iframe.
	for _, forbidden := range []string{
		"if (inst && inst.status !== 'running') return;",
		"if (inst && inst.status !== 'running') {",
		"const isRunning = inst && inst.status === 'running';",
		"inst.status === 'running' ?",
		// A strict substring of the line above, kept on purpose: it is the only
		// one of the two that also catches the destructured form, where the
		// status is a local rather than a property of `inst`.
		"status === 'running' ?",
	} {
		if strings.Contains(bodyText, forbidden) {
			t.Fatalf("GET / must not gate terminal I/O on status equality: found %q", forbidden)
		}
	}

	// The status-bar label for the renderer-owned kinds goes through the same
	// buckets: kinds/reasonix.js keeps its iframe navigating while the
	// instance is `starting`, so "stopped" was self-contradicting.
	for _, check := range []string{
		"function instanceStatusLabel(inst, liveLabel)",
		"updateStatus(instanceStatusLabel(inst, 'reasonix web'))",
		"updateStatus(instanceStatusLabel(inst, 'dsh web ui'))",
	} {
		if !strings.Contains(bodyText, check) {
			t.Fatalf("GET / should label the web-UI kinds by bucket: missing %q", check)
		}
	}

	// loadLog() swallows its own errors, so the once-per-transition terminal
	// replay needs a separate success flag or a failed fetch strands the tab
	// with neither log nor banner until someone presses Refresh.
	for _, check := range []string{
		"session.lastLogLoadFailed = true;",
		"session.lastLogLoadFailed = false;",
		"session.lastLogLoadFailures = (session.lastLogLoadFailures || 0) + 1;",
		"session.lastLogLoadAttemptAt = Date.now();",
		// Unbounded 2s retries would append one error line per tick, since
		// loadLog() reports its failures into the terminal.
		"const retryBackoffMs = Math.min(60, 2 ** Math.min(session.lastLogLoadFailures || 0, 6)) * 1000;",
		"const retryDue = !!session.lastLogLoadFailed",
		"if (enteringTerminalBucket || retryDue) {",
	} {
		if !strings.Contains(bodyText, check) {
			t.Fatalf("GET / should retry a failed terminal replay with a backoff: missing %q", check)
		}
	}

	// The legacy factory in index.html and the renderer's in kinds/pty.js both
	// initialise the replay latches, next to the lastKnownStatus latch they
	// now sit beside.
	for _, check := range []string{
		"lastLogLoadFailed: false,",
		"lastLogLoadFailures: 0,",
		"lastLogLoadAttemptAt: 0,",
	} {
		if !strings.Contains(bodyText, check) {
			t.Fatalf("GET / legacy session factory should initialise %q", check)
		}
		if !strings.Contains(js, check) {
			t.Fatalf("GET /static/kinds/pty.js session factory should initialise %q", check)
		}
	}

	// connectTTY must refuse an id state.instances cannot resolve: the server
	// accepts the socket and then closes it with 1013, which surfaces as a
	// dropped connection rather than a bad request. The `!inst` half is easy to
	// drop because only the status half changed in the first draft.
	if !strings.Contains(bodyText, "if (!inst || !isInstanceLiveStatus(inst.status)) {") {
		t.Fatal("GET / connectTTY should refuse both an unresolvable id and a non-live status")
	}

	// A CLOSED socket left on the session reads as "transport in flight" and
	// would block the promotion until something else cleared it. Anchored on
	// the assignment, not the comment above it.
	if !strings.Contains(bodyText, "session.ttySocket = null;\n                        const latest = state.instances.find") {
		t.Fatal("GET / should clear ttySocket when the socket closes")
	}
	for _, forbidden := range []string{
		"if (inst.status !== 'running') {",
		"if (inst && inst.status !== 'running') return;",
	} {
		if strings.Contains(js, forbidden) {
			t.Fatalf("GET /static/kinds/pty.js must not gate terminal I/O on status equality: found %q", forbidden)
		}
	}
}

func TestRegisterRemoteAccess(t *testing.T) {
	mux := http.NewServeMux()
	if err := Register(mux, "myworktree", func(r *http.Request) bool { return true }); err != nil {
		t.Fatal(err)
	}
	srv := httptest.NewServer(mux)
	defer srv.Close()

	resp, err := http.Get(srv.URL + "/")
	if err != nil {
		t.Fatalf("GET / failed: %v", err)
	}
	body, _ := io.ReadAll(resp.Body)
	_ = resp.Body.Close()
	if !strings.Contains(string(body), `<body class="remote-access">`) {
		t.Fatalf("expected body to contain remote-access class when isRemote returns true")
	}
}

func TestRegisterLocalAccess(t *testing.T) {
	mux := http.NewServeMux()
	if err := Register(mux, "myworktree", func(r *http.Request) bool { return false }); err != nil {
		t.Fatal(err)
	}
	srv := httptest.NewServer(mux)
	defer srv.Close()

	resp, err := http.Get(srv.URL + "/")
	if err != nil {
		t.Fatalf("GET / failed: %v", err)
	}
	body, _ := io.ReadAll(resp.Body)
	_ = resp.Body.Close()
	if strings.Contains(string(body), `<body class="remote-access">`) {
		t.Fatalf("expected body NOT to contain remote-access class when isRemote returns false")
	}
}

func TestRegisterNilRemote(t *testing.T) {
	mux := http.NewServeMux()
	if err := Register(mux, "myworktree", nil); err != nil {
		t.Fatal(err)
	}
	srv := httptest.NewServer(mux)
	defer srv.Close()

	resp, err := http.Get(srv.URL + "/")
	if err != nil {
		t.Fatalf("GET / failed: %v", err)
	}
	body, _ := io.ReadAll(resp.Body)
	_ = resp.Body.Close()
	if strings.Contains(string(body), `<body class="remote-access">`) {
		t.Fatalf("expected body NOT to contain remote-access class when isRemote is nil")
	}
}

func TestRegisterReturnsErrorWhenIndexHTMLMissing(t *testing.T) {
	mux := http.NewServeMux()
	// Override the reader to simulate a missing file.
	orig := indexHTMLReader
	indexHTMLReader = func() ([]byte, error) {
		return nil, fmt.Errorf("file not found")
	}
	defer func() { indexHTMLReader = orig }()

	err := Register(mux, "test", nil)
	if err == nil {
		t.Fatal("expected error when index.html is missing, got nil")
	}
	if !strings.Contains(err.Error(), "failed to read embedded index.html") {
		t.Fatalf("error message should contain context, got: %v", err)
	}
}

func TestRegisterTitleWithSpecialRepoNames(t *testing.T) {
	cases := []struct {
		name     string
		repoName string
		want     string
		notWant  string
	}{
		{
			name:     "empty string",
			repoName: "",
			want:     "<title> - myworktree</title>",
			notWant:  "<title></title>",
		},
		{
			name:     "very long name",
			repoName: strings.Repeat("a", 10000),
			want:     "<title>" + strings.Repeat("a", 10000) + " - myworktree</title>",
		},
		{
			name:     "HTML entities escaped",
			repoName: "<script>evil</script>",
			want:     "<title>&lt;script&gt;evil&lt;/script&gt; - myworktree</title>",
			notWant:  "<script>evil</script>",
		},
		{
			name:     "ampersand escaped",
			repoName: "foo & bar",
			want:     "<title>foo &amp; bar - myworktree</title>",
			notWant:  "foo & bar",
		},
		{
			name:     "double quotes escaped",
			repoName: `repo"with"quotes`,
			want:     "<title>repo&#34;with&#34;quotes - myworktree</title>",
			notWant:  `repo"with"quotes`,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			mux := http.NewServeMux()
			if err := Register(mux, tc.repoName, nil); err != nil {
				t.Fatal(err)
			}
			srv := httptest.NewServer(mux)
			defer srv.Close()

			resp, err := http.Get(srv.URL + "/")
			if err != nil {
				t.Fatalf("GET / failed: %v", err)
			}
			body, _ := io.ReadAll(resp.Body)
			_ = resp.Body.Close()

			if !strings.Contains(string(body), tc.want) {
				t.Fatalf("expected title %q, body preview: %s", tc.want, string(body[:200]))
			}
			if tc.notWant != "" && strings.Contains(string(body), tc.notWant) {
				t.Fatalf("title should not contain unescaped %q", tc.notWant)
			}
		})
	}
}

// TestSidebarWorktreeRenameAnchors pins the issue #122 wiring in the served
// sources — the surface the node cases cannot see, because they stub the
// renderers and drive the handlers directly. Local slices everywhere: a bare
// whole-file Contains would still pass if the double-click handler had been
// moved onto the pinned main row (acceptance #7 forbids exactly that), or if
// the editing-state branch had been deleted while its text survived in a
// comment.
func TestSidebarWorktreeRenameAnchors(t *testing.T) {
	index := fetchIndexHTML(t)
	framework := fetchStaticAsset(t, "/static/framework.js")

	// bodyOf slices a function from its header to the closing brace at this
	// file's function indent (8 spaces), so an assertion cannot be satisfied
	// by an identically spelled line living in some other function.
	bodyOf := func(header string) string {
		t.Helper()
		i := strings.Index(index, header)
		if i < 0 {
			t.Fatalf("index.html lost %q (issue #122)", header)
		}
		b := index[i:]
		if e := strings.Index(b, "\n        }"); e >= 0 {
			b = b[:e]
		}
		return b
	}

	// 1. The regular worktree row template: the double-click entry point, the
	// tooltip, and the editing-state input with its escaping.
	rowStart := strings.Index(index, "html += state.worktrees.map(w => {")
	if rowStart < 0 {
		t.Fatal("index.html lost the regular-worktree map block of renderSidebar (issue #122)")
	}
	rowEnd := strings.Index(index[rowStart:], "}).join(\"\");")
	if rowEnd < 0 {
		t.Fatal("index.html regular-worktree map block has no terminator (issue #122)")
	}
	row := index[rowStart : rowStart+rowEnd]
	for _, anchor := range []string{
		`ondblclick="startWTRename('${wIdEsc}', event)"`,
		`title="Double-click to rename"`,
		`const renameValue = state.renamingWT === w.id && state.renamingWTValue ? state.renamingWTValue : w.name;`,
		`<input id="wt-rename-input-${wIdEsc}" class="wt-rename-input" value="${escapeHtml(renameValue)}"`,
		// Review #7 hardening: the live binding that turns the F5 leftover
		// into a real field — without it a slipped repaint rebuilds from
		// w.name and silently loses typed text.
		`oninput="state.renamingWTValue = this.value"`,
		`onkeydown="handleWTRenameKeydown('${wIdEsc}', event)"`,
		`onblur="commitWTRename('${wIdEsc}', event)"`,
		`escapeHtml(w.name)`,
		// The controls wrapper the startWTRename source guard matches on
		// (review V5): without this class the guard silently never hits.
		`class="wt-row-actions"`,
	} {
		if !strings.Contains(row, anchor) {
			t.Fatalf("index.html worktree row template missing %q (issue #122)", anchor)
		}
	}

	// 2. The pinned main row stays static: no entry point, no tooltip, no
	// input (acceptance #7 — v1 excludes it deliberately).
	mainStart := strings.Index(index, `<li class="wt-item wt-item-main`)
	if mainStart < 0 {
		t.Fatal("index.html lost the pinned main worktree row (issue #122)")
	}
	mainEnd := strings.Index(index[mainStart:], "</li>")
	if mainEnd < 0 {
		t.Fatal("index.html main worktree row has no closing </li> (issue #122)")
	}
	mainRow := index[mainStart : mainStart+mainEnd]
	for _, forbidden := range []string{"startWTRename", "ondblclick", "Double-click to rename", "wt-rename-input"} {
		if strings.Contains(mainRow, forbidden) {
			t.Fatalf("the pinned main row must stay out of the rename feature, found %q (acceptance #7, issue #122)", forbidden)
		}
	}

	// 3. The guards. Three background repaints of #wt-list now sit behind
	// if (!state.renamingWT): refresh()'s /api/main continuation, fetchDiverge
	// (INSIDE the callee, so its independent 60s divergeTimer is covered too —
	// decision G) and fetchDivergeSingle. The last one is the arbitration that
	// overturned plan decision G's exemption (review B-F1): selectWorktree
	// fires that request synchronously on the FIRST click of a double-click,
	// long before the dblclick builds the input, so "the blur committed first"
	// cannot cover it — a late response would rebuild the row mid-edit and take
	// focus, the selection, the half-typed text and the IME composition with
	// it. refresh() keeps its own full-render skip, and commitWTRename's
	// success continuation guards the repaint it owes the PATCH response
	// (review B-F2: another row may have entered the editing state during the
	// round trip) while its state splice and its refresh() stay unconditional.
	refreshBody := bodyOf("async function refresh() {")
	if !strings.Contains(refreshBody, "if (state.renamingInst || state.renamingWT) {") {
		t.Fatal("refresh() must skip the full render() while a worktree row is being renamed (pitfall #1, issue #122)")
	}
	guardedRender := regexp.MustCompile(`if \(!state\.renamingWT\) \{\s*renderSidebar\(\);`)
	if !guardedRender.MatchString(refreshBody) {
		t.Fatal("refresh()'s /api/main continuation must guard its renderSidebar() with if (!state.renamingWT) (pitfall #4, issue #122)")
	}
	divergeBody := bodyOf("async function fetchDiverge() {")
	if !guardedRender.MatchString(divergeBody) {
		t.Fatal("fetchDiverge() must guard renderSidebar() INSIDE the callee, so its own 60s divergeTimer cannot rebuild the sidebar mid-rename (decision G, issue #122)")
	}
	singleBody := bodyOf("async function fetchDivergeSingle(id) {")
	if !guardedRender.MatchString(singleBody) {
		t.Fatal("fetchDivergeSingle() must guard renderSidebar() too — selectWorktree starts it on the first click of the double-click, so its response can land after the editing state exists (review B-F1, issue #122)")
	}
	if !strings.Contains(singleBody, "`/api/worktree/diverged?id=${encodeURIComponent(id)}`") {
		t.Fatal("fetchDivergeSingle must keep querying /api/worktree/diverged with the encoded id (issue #122)")
	}
	if !strings.Contains(singleBody, "Object.assign(state.diverged, data.items);") {
		t.Fatal("fetchDivergeSingle must still merge the diverged state even while the repaint is deferred (issue #122)")
	}
	// Six guarded repaints, no more and no less: the /api/main continuation,
	// fetchDiverge, fetchDivergeSingle (each asserted by name above),
	// commitWTRename's success continuation, and the two spinner `finally`
	// blocks in openTerminal / openFinder. Those last two are the third-party
	// teardowns review A-F1 flagged: plan R1's exemption ("a user action blurs
	// the input before the render") covers the spinner-ON render before the
	// await and NOT this finally, which lands up to 10s later — inside a
	// window in which the user can have double-clicked a row into its editing
	// state. The count is the completeness check in both directions: a seventh
	// site would mean a repaint guarded somewhere the reasoning above does not
	// account for, a missing one a path that still rebuilds #wt-list mid-rename.
	if n := strings.Count(index, "if (!state.renamingWT)"); n != 6 {
		t.Fatalf("index.html has %d `if (!state.renamingWT)` guards, want exactly 6 (/api/main continuation + fetchDiverge + fetchDivergeSingle + commitWTRename's success continuation + openTerminal's finally + openFinder's finally, issue #122)", n)
	}
	// The two spinner finally blocks, by name, so the count above cannot be
	// satisfied by two guards planted anywhere else.
	for _, fn := range []string{"async function openTerminal(id, event) {", "async function openFinder(id, event) {"} {
		b := bodyOf(fn)
		if !strings.Contains(b, "if (!state.renamingWT) {") || !strings.Contains(b, "loadingActions.delete(key);") {
			t.Fatalf("%s must guard its finally repaint with if (!state.renamingWT) (review A-F1, issue #122)", fn)
		}
		if strings.Index(b, "if (!state.renamingWT) {") < strings.Index(b, "loadingActions.delete(key);") {
			t.Fatalf("%s's finally must clear the spinner BEFORE deciding on the repaint (review A-F1, issue #122)", fn)
		}
	}
	if n := strings.Count(index, "state.renamingInst || state.renamingWT"); n != 2 {
		t.Fatalf("index.html has %d combined rename guards, want exactly 2 (refresh() and focusTerminalIfPossible, issue #122)", n)
	}
	if !strings.Contains(bodyOf("function focusTerminalIfPossible() {"), "if (state.renamingInst || state.renamingWT) return;") {
		t.Fatal("focusTerminalIfPossible must not steal focus from a renaming worktree row (acceptance #4, issue #122)")
	}

	// 4. Mutual exclusion, both entry points, in the order that matters: the
	// OTHER machine's state is cleared and its renderer runs BEFORE this
	// machine's state is set, so at most one rename input ever exists
	// (decision D, acceptance #8).
	startTab := bodyOf("function startRename(id, event) {")
	clearAt := strings.Index(startTab, "state.renamingWT = null;")
	renderAt := strings.Index(startTab, "renderSidebar();")
	setAt := strings.Index(startTab, "state.renamingInst = id;")
	if clearAt < 0 || renderAt < 0 || setAt < 0 {
		t.Fatal("startRename must clear the sidebar rename state and re-render the sidebar (decision D, issue #122)")
	}
	if !(clearAt < renderAt && renderAt < setAt) {
		t.Fatal("startRename must clear + re-render the sidebar BEFORE entering the tab rename, or the sidebar input survives into the tab's editing state (issue #122)")
	}
	startWT := bodyOf("function startWTRename(id, event) {")
	clearAt = strings.Index(startWT, "state.renamingInst = null;")
	renderAt = strings.Index(startWT, "renderTabs();")
	setAt = strings.Index(startWT, "state.renamingWT = id;")
	if clearAt < 0 || renderAt < 0 || setAt < 0 {
		t.Fatal("startWTRename must clear the tab rename state and re-render the tabs (decision D, issue #122)")
	}
	if !(clearAt < renderAt && renderAt < setAt) {
		t.Fatal("startWTRename must clear + re-render the tabs BEFORE entering the row's editing state (issue #122)")
	}
	// Review V5: the row's own controls (delete ✕, terminal, finder) stop
	// propagation on CLICK but not on the separate dblclick event, so the
	// entry point itself has to exclude double-clicks that started inside
	// .wt-row-actions — otherwise the delete confirm dialog and the rename
	// input pop at the same time. The guard must sit BEFORE the
	// mutual-exclusion teardown, so an excluded double-click does not even
	// disturb the tab machine.
	srcGuard := strings.Index(startWT, "if (event.target.closest('.wt-row-actions')) return;")
	if srcGuard < 0 {
		t.Fatal("startWTRename must exclude double-clicks sourced from the row's own controls (.wt-row-actions) (review V5, issue #122)")
	}
	if !(srcGuard < clearAt) {
		t.Fatal("startWTRename's row-actions guard must precede the mutual-exclusion teardown (review V5, issue #122)")
	}

	// Review V4: Enter / Escape during IME composition are the input method's
	// candidate-confirm / cancel keys — both keydown handlers must bail
	// before dispatching, and the bail must PRECEDE the key checks, or the
	// half-composed string rides the PATCH the moment the user accepts a
	// candidate. The identical line on both halves keeps the parity that is
	// the issue's core requirement.
	for _, header := range []string{"function handleWTRenameKeydown(id, event) {", "function handleRenameKeydown(id, event) {"} {
		kb := bodyOf(header)
		imeAt := strings.Index(kb, "if (event.isComposing) return;")
		enterAt := strings.Index(kb, "if (event.key === 'Enter')")
		if imeAt < 0 || enterAt < 0 {
			t.Fatalf("%s must carry the IME-composition bail before the key dispatch (review V4, issue #122)", header)
		}
		if !(imeAt < enterAt) {
			t.Fatalf("%s's IME bail must precede the Enter/Escape dispatch (review V4, issue #122)", header)
		}
	}

	// 5. The commit handler's contract, spelled in the shipped source: the
	// worktree endpoint, the trimmed name, the empty-name bail, the
	// state.worktrees replacement and the branch-badge resync.
	// `if (state.renamingWT !== id) return` is the re-entrancy guard the
	// browser verification forced: renderSidebar() below removes the focused
	// input, Chrome dispatches blur on that node while getElementById still
	// resolves it, and the re-entered commit would fire a second identical
	// PATCH (and nest an innerHTML write inside the first). It must sit BEFORE
	// the input is read — after it, the re-entry has already touched the node.
	// `if (!input) return` stays as the second line of defence.
	commitBody := bodyOf("function commitWTRename(id, event) {")
	for _, anchor := range []string{
		"if (state.renamingWT !== id) return;",
		"const input = document.getElementById('wt-rename-input-' + id);",
		"if (!input) return;",
		"const newName = input.value.trim();",
		"if (newName === '') {",
		"api('/api/worktrees', {",
		"method: 'PATCH',",
		"JSON.stringify({ id: id, name: newName })",
		"state.worktrees[idx] = updated;",
		"refresh();",
	} {
		if !strings.Contains(commitBody, anchor) {
			t.Fatalf("commitWTRename missing %q (issue #122)", anchor)
		}
	}
	// Order, not mere presence: the state guard has to precede the node read,
	// otherwise the re-entrant blur has already done its damage.
	guardFirst := strings.Index(commitBody, "if (state.renamingWT !== id) return;")
	readFirst := strings.Index(commitBody, "const input = document.getElementById('wt-rename-input-' + id);")
	if !(guardFirst >= 0 && readFirst >= 0 && guardFirst < readFirst) {
		t.Fatal("commitWTRename's re-entrancy guard must precede the input read (issue #122 browser verification)")
	}

	// The tab handler carries the mirror guard, and it is not decoration:
	// startWTRename's mutual-exclusion teardown (renderTabs) blurs a live tab
	// input, and without this guard that blur renames the instance by itself.
	tabCommitBody := bodyOf("function commitRename(id, event) {")
	for _, anchor := range []string{
		"if (state.renamingInst !== id) return;",
		"const input = document.getElementById('rename-input-' + id);",
		"if (!input) return;",
		"api('/api/instances', {",
		"renderTabs();",
	} {
		if !strings.Contains(tabCommitBody, anchor) {
			t.Fatalf("commitRename missing %q (issue #122 browser verification)", anchor)
		}
	}
	guardTab := strings.Index(tabCommitBody, "if (state.renamingInst !== id) return;")
	readTab := strings.Index(tabCommitBody, "const input = document.getElementById('rename-input-' + id);")
	if !(guardTab >= 0 && readTab >= 0 && guardTab < readTab) {
		t.Fatal("commitRename's re-entrancy guard must precede the input read (issue #122 browser verification)")
	}
	if n := strings.Count(index, "!== id) return;"); n != 2 {
		t.Fatalf("index.html has %d re-entrancy guards, want exactly 2 (commitWTRename + commitRename, issue #122)", n)
	}
	// commitRename's success continuation repaints through render(), which
	// rebuilds the tabs AND the sidebar — the tab-side twin of the B-F2
	// window, and newly reachable because of this issue's mutual exclusion (a
	// tab PATCH can now be in flight while a SIDEBAR row is being renamed).
	// It must be guarded on BOTH states, and the guard must sit inside the
	// .then block, after the unconditional splice (review A-F1, issue #122).
	if n := strings.Count(index, "if (!state.renamingInst && !state.renamingWT) {"); n != 1 {
		t.Fatalf("index.html has %d render() rename guards, want exactly 1 (commitRename's success continuation, review A-F1, issue #122)", n)
	}
	tabThen := tabCommitBody[strings.Index(tabCommitBody, ".then(updated => {"):strings.Index(tabCommitBody, "}).catch(err => {")]
	for _, anchor := range []string{"state.instances[idx] = updated;", "if (!state.renamingInst && !state.renamingWT) {", "render();"} {
		if !strings.Contains(tabThen, anchor) {
			t.Fatalf("commitRename's success continuation must keep %q inside the .then block (review A-F1, issue #122)", anchor)
		}
	}
	if !(strings.Index(tabThen, "state.instances[idx] = updated;") < strings.Index(tabThen, "if (!state.renamingInst && !state.renamingWT) {")) {
		t.Fatal("commitRename must splice the renamed instance BEFORE deciding on the repaint — the data update stays unconditional (review A-F1, issue #122)")
	}
	if !(strings.Index(tabThen, "if (!state.renamingInst && !state.renamingWT) {") < strings.Index(tabThen, "render();")) {
		t.Fatal("commitRename's render() must sit INSIDE its rename guard (review A-F1, issue #122)")
	}
	// The PATCH round trip is a window in which another row can enter the
	// editing state — the success continuation's repaint must be guarded,
	// while the state splice and the refresh() resync stay unconditional
	// (refresh() carries its own guard) (review B-F2, issue #122).
	if !guardedRender.MatchString(commitBody) {
		t.Fatal("commitWTRename's success continuation must guard its renderSidebar() with if (!state.renamingWT) (review B-F2, issue #122)")
	}
	// Explicit forward order, not a LastIndex lookup: splice -> guarded repaint
	// -> refresh. Index() lands on the SUCCESS continuation's refresh()
	// because the catch branch sits below it, and the .then block is then
	// sliced and checked on its own — with a LastIndex lookup, deleting the
	// success resync would silently re-anchor to the catch branch's refresh()
	// and stay green (review A-F3, issue #122).
	spliceAt := strings.Index(commitBody, "state.worktrees[idx] = updated;")
	guardAt := strings.Index(commitBody, "if (!state.renamingWT) {")
	refreshAt := strings.Index(commitBody, "refresh();")
	if spliceAt < 0 || guardAt < 0 || refreshAt < 0 {
		t.Fatal("commitWTRename lost the splice / guarded repaint / refresh() trio (review B-F2 + A-F3, issue #122)")
	}
	if !(spliceAt < guardAt && guardAt < refreshAt) {
		t.Fatal("commitWTRename's order must stay splice -> guarded repaint -> refresh() (review B-F2, issue #122)")
	}
	thenAt := strings.Index(commitBody, ".then(updated => {")
	catchAt := strings.Index(commitBody, "}).catch(err => {")
	if thenAt < 0 || catchAt < 0 || thenAt > catchAt {
		t.Fatal("commitWTRename must keep its .then / .catch split (issue #122)")
	}
	if !strings.Contains(commitBody[thenAt:catchAt], "refresh();") {
		t.Fatal("the SUCCESS continuation must keep its unconditional refresh() resync — the catch branch's refresh() is not a substitute (review A-F3, issue #122)")
	}
	if strings.Contains(commitBody[thenAt:catchAt], "if (") && !guardedRender.MatchString(commitBody[thenAt:catchAt]) {
		t.Fatal("the success continuation's renderSidebar() must be the guarded one (review B-F2, issue #122)")
	}
	if !strings.Contains(bodyOf("function cancelWTRename() {"), "state.renamingWT = null;") ||
		!strings.Contains(bodyOf("function cancelWTRename() {"), "renderSidebar();") {
		t.Fatal("cancelWTRename must reset the state and repaint the sidebar (issue #122)")
	}

	// 6. framework.js declares the mirrored state fields, and the CSS rule is
	// fluid: the input fills whatever width .wt-name gets, never a hard-coded
	// pixel width like the tab's 100px input.
	for _, field := range []string{"renamingWT: null,", "renamingWTValue: \"\","} {
		if !strings.Contains(framework, field) {
			t.Fatalf("framework.js window.state must initialize %q (issue #122)", field)
		}
	}
	cssStart := strings.Index(index, ".wt-rename-input {")
	if cssStart < 0 {
		t.Fatal("index.html lost the .wt-rename-input rule (issue #122)")
	}
	cssEnd := strings.Index(index[cssStart:], "\n        }")
	if cssEnd < 0 {
		t.Fatal("index.html .wt-rename-input rule has no closing brace (issue #122)")
	}
	cssRule := index[cssStart : cssStart+cssEnd]
	for _, decl := range []string{
		"background: var(--main-bg);",
		"border: 1px solid var(--border-color);",
		"border-radius: 3px;",
		"padding: 1px 4px;",
		"font: inherit;",
		"color: inherit;",
		"width: 100%;",
		"box-sizing: border-box;",
		"outline: none;",
	} {
		if !strings.Contains(cssRule, decl) {
			t.Fatalf("index.html .wt-rename-input should declare %q (issue #122)", decl)
		}
	}
	if regexp.MustCompile(`width:\s*\d+px`).MatchString(cssRule) {
		t.Fatal(".wt-rename-input must stay fluid (width: 100% + box-sizing), no hard-coded pixel width (plan T3.6, issue #122)")
	}
	focusStart := strings.Index(index, ".wt-rename-input:focus {")
	if focusStart < 0 {
		t.Fatal("index.html lost the .wt-rename-input:focus rule (issue #122)")
	}
	focusRule := index[focusStart:]
	if e := strings.Index(focusRule, "\n        }"); e >= 0 {
		focusRule = focusRule[:e]
	}
	if !strings.Contains(focusRule, "border-color: var(--active-color);") {
		t.Fatal(".wt-rename-input:focus should take var(--active-color) like the tab input (issue #122)")
	}
}
