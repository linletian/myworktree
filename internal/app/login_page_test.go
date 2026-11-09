package app

import (
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"myworktree/internal/config"
)

// The login page's failure UX (issue #137 follow-up): a bad token must keep the
// user ON the form — error text, cleared + re-focused field, a shake for
// decoration — instead of landing on the bare 401 body the pre-existing flow
// produced (no form, no retry, browser Back only).
//
// The daemon's byte-level contract is deliberately unchanged and pinned below:
// POST /login still answers 401 on a bad token and 302 + Set-Cookie on a good
// one. The new feedback is a progressive enhancement inside the page's own
// inline script: it submits the same form-encoded body with fetch, so with JS
// off (or the script failing) the native submit still works exactly as before.
// That is also why nothing in this file asserts a new endpoint: there is none.

// configWithToken points config.Load at a temp auth.json holding token.
func configWithToken(t *testing.T, token string) {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "auth.json")
	if err := os.WriteFile(path, []byte(`{"auth_token":"`+token+`"}`), 0o600); err != nil {
		t.Fatalf("write auth.json: %v", err)
	}
	restore := config.SetPathForTest(func() (string, error) { return path, nil })
	t.Cleanup(restore)
}

func getLoginPage(t *testing.T, target string) string {
	t.Helper()
	req := httptest.NewRequest(http.MethodGet, target, nil)
	rec := httptest.NewRecorder()
	(&Server{}).handleLogin(rec, req)
	res := rec.Result()
	body, err := io.ReadAll(res.Body)
	if err != nil {
		t.Fatalf("read login body: %v", err)
	}
	if res.StatusCode != http.StatusOK {
		t.Fatalf("GET %s = %d, want 200", target, res.StatusCode)
	}
	return string(body)
}

// TestLoginPageFailureUxMarkers pins the shipped inline feedback: the error
// region, the shake, the copy, and the two behaviors that make it non-navigating
// (fetch submit + location.replace on success). The selectors and ids are what
// the script below depends on, so they are asserted together, not separately.
func TestLoginPageFailureUxMarkers(t *testing.T) {
	page := getLoginPage(t, "/login?next=%2Fworktrees%2Fwt-1")

	for _, want := range []struct{ marker, why string }{
		{`id="login-error" class="login-error" role="alert" aria-live="polite"`, "an announced error region (text is the real signal, the shake is decoration)"},
		{`@keyframes mw-login-shake`, "the shared shake keyframe (same name in the Portal dashboard, pinned by the parity test)"},
		{`@media (prefers-reduced-motion: reduce)`, "the shake must be droppable for reduced-motion users"},
		{`Invalid token. Try again.`, "the shared wrong-token copy"},
		{`Could not reach the server. Try again.`, "the shared network-failure copy"},
		{`Signing in…`, "the busy button label"},
		{`button.disabled = true`, "double-submit guard while the request is in flight"},
		{`event.preventDefault()`, "the submit is intercepted, so the page does not navigate away on a failure"},
		{`location.replace(next)`, "success replaces the history entry instead of pushing a back-to-login step"},
		{`new URLSearchParams(new FormData(form))`, "the enhanced path posts the SAME form-encoded body the native path does"},
		{`method="post" action="/login"`, "progressive enhancement: with JS off the native form still submits"},
		{`autocomplete="current-password"`, "password-manager friendly, and it matches the Portal dashboard"},
	} {
		if !strings.Contains(page, want.marker) {
			t.Errorf("login page is missing %q — %s", want.marker, want.why)
		}
	}

	if !strings.Contains(page, `value="/worktrees/wt-1"`) {
		t.Error("login page must still carry the validated next path in the hidden field")
	}
	if strings.Contains(page, "{{NEXT}}") {
		t.Error("the {{NEXT}} placeholder must be substituted")
	}
}

// TestLoginPostContractUnchanged pins the server side, which this change does
// NOT touch: 401 + no cookie on a bad token, 302 + cookie on a good one. If a
// future change starts returning HTML/JSON on failure, this test is the tripwire
// for every non-browser client that parses the plain body.
func TestLoginPostContractUnchanged(t *testing.T) {
	configWithToken(t, "right-token")

	postForm := func(target, body string) *http.Request {
		req := httptest.NewRequest(http.MethodPost, target, strings.NewReader(body))
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		return req
	}

	bad := httptest.NewRecorder()
	(&Server{}).handleLogin(bad, postForm("/login", url.Values{"token": {"wrong-token"}, "next": {"/"}}.Encode()))
	if bad.Code != http.StatusUnauthorized {
		t.Fatalf("POST /login with a bad token = %d, want 401", bad.Code)
	}
	if body := strings.TrimSpace(bad.Body.String()); body != "unauthorized" {
		t.Fatalf("POST /login bad-token body = %q, want %q (the contract non-browser clients parse)", body, "unauthorized")
	}
	if cookies := bad.Result().Cookies(); len(cookies) != 0 {
		t.Fatalf("a rejected login must not set cookies, got %v", cookies)
	}

	good := httptest.NewRecorder()
	(&Server{}).handleLogin(good, postForm("/login", url.Values{"token": {"right-token"}, "next": {"/worktrees"}}.Encode()))
	if good.Code != http.StatusFound {
		t.Fatalf("POST /login with a good token = %d, want 302", good.Code)
	}
	if loc := good.Header().Get("Location"); loc != "/worktrees" {
		t.Fatalf("redirect target = %q, want /worktrees", loc)
	}
	var found bool
	for _, c := range good.Result().Cookies() {
		if c.Name == "mw_token" && c.Value == "right-token" {
			found = true
		}
	}
	if !found {
		t.Fatalf("a successful login must set mw_token, got %v", good.Result().Cookies())
	}
}

// TestLoginFailureUxParityWithPortalDashboard keeps the daemon's /login page and
// the Portal dashboard on ONE failure-UX spec. They are two implementations on
// purpose — the dashboard's inline blocks are pinned by a CSP hash
// (internal/portal/csp_gen.go), so sharing code by injection would defeat that
// check — and this test is what makes the duplication safe: any drift in the
// copy, the shake, the busy label or the a11y wiring fails here.
func TestLoginFailureUxParityWithPortalDashboard(t *testing.T) {
	dashboard, err := os.ReadFile(filepath.Join("..", "portal", "dashboard.html"))
	if err != nil {
		t.Fatalf("read portal dashboard.html: %v", err)
	}
	daemonPage := getLoginPage(t, "/login")
	portalPage := string(dashboard)

	for _, shared := range []struct{ marker, why string }{
		{`@keyframes mw-login-shake`, "same keyframe name on both pages"},
		{`animation: mw-login-shake 0.4s ease-in-out;`, "same shake timing on both pages"},
		{`.mw-login-shake { animation: none; }`, "both pages drop the shake under prefers-reduced-motion"},
		{`@media (prefers-reduced-motion: reduce)`, "both pages honour reduced motion"},
		{`Invalid token. Try again.`, "same wrong-token copy"},
		{`Could not reach the server. Try again.`, "same network-failure copy"},
		{`Signing in…`, "same busy label"},
		{`role="alert" aria-live="polite"`, "same announced error region"},
		{`aria-invalid`, "both mark the field invalid"},
		{`input.value = `, "both clear the field after a failure"},
		{`input.focus()`, "both return focus to the field"},
		{`void card.offsetWidth`, "both restart the animation the same way on a repeat failure"},
	} {
		if !strings.Contains(daemonPage, shared.marker) {
			t.Errorf("daemon /login is missing shared marker %q — %s", shared.marker, shared.why)
		}
		if !strings.Contains(portalPage, shared.marker) {
			t.Errorf("portal dashboard is missing shared marker %q — %s", shared.marker, shared.why)
		}
	}

	// The failure text must not be injected as markup on either side: both use
	// textContent, which keeps this path free of an innerHTML sink.
	if strings.Contains(portalPage, `error.innerHTML = '<svg`) {
		t.Error("portal dashboard still injects the error as markup; both pages must use textContent")
	}
}
