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
		// Legacy no-renderer fallback bootstraps "unknown" as -1.
		"logCursor: -1,",
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
	cursorAt := strings.Index(bodyText, "session.logCursor = -1;")
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
		"session.ttyOffset = -1;\n            session.logCursor = -1;\n        }",
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
	cursorAt := strings.Index(js, "s.logCursor = -1;")
	if cursorAt < 0 {
		t.Fatal("pty.js should reset s.logCursor to the unknown sentinel after clearing the terminal")
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
