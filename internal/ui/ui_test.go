package ui

import (
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
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

// TestUICopyIsEnglishOnly pins the single-language English UI direction
// (issue #73) across the whole served UI, not just index.html: the web-kind
// renderer JS files (dsh_web.js / opencode_web.js / reasonix.js) and
// framework.js must contain no CJK characters at all — warning bars,
// loading titles, the missing-dependency dialog and any future copy are
// covered even when they live in JS. Key user-visible strings are also
// asserted positively per file so a reword cannot silently hide a CJK
// regression behind a full-file rewrite.
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
		"if (inst && inst.status === 'running' && !hasLiveTTYConnection(session.id)) {",
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
		"session.ttyReconnectTimer = setTimeout(() => connectTTY(session), 5000);",
	}
	for _, check := range checks {
		if !strings.Contains(bodyText, check) {
			t.Fatalf("GET / should include per-session connection management hook %q", check)
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
