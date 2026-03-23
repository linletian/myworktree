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
	if err := Register(mux, "myworktree"); err != nil {
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
	if !strings.Contains(resp.Header.Get("Content-Type"), "text/html") {
		t.Fatalf("GET / content-type mismatch: %q", resp.Header.Get("Content-Type"))
	}
	if len(body) == 0 {
		t.Fatalf("GET / body should not be empty")
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
	if err := Register(mux, "myworktree"); err != nil {
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
	if err := Register(mux, "myworktree"); err != nil {
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
	if err := Register(mux, "myproject"); err != nil {
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
		"disconnectTTY(terminalSessions[id]);",
		"function reconnectRunningTerminalSessions()",
		"if (inst && inst.status === 'running' && !hasLiveTTYConnection(id)) {",
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
		"session.ttyReconnectTimer = setTimeout(() => connectTTY(session), 1000);",
	}
	for _, check := range checks {
		if !strings.Contains(bodyText, check) {
			t.Fatalf("GET / should include per-session connection management hook %q", check)
		}
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

	err := Register(mux, "test")
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
			if err := Register(mux, tc.repoName); err != nil {
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
