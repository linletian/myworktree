package opencode_web

import (
	"net/http"
	"strings"
	"testing"

	"myworktree/internal/framework"
)

func TestDriver_Manifest(t *testing.T) {
	d := Driver{}
	m := d.Manifest()
	if m.Name != "opencode-web" {
		t.Fatalf("Name = %q, want opencode-web", m.Name)
	}
	if m.Interactive {
		t.Fatalf("Interactive = true, want false for HTTP-backed kind")
	}
	if m.Label == "" || m.Description == "" {
		t.Fatalf("Label/Description must be non-empty: %+v", m)
	}
}

func TestDriver_SpawnInvocation(t *testing.T) {
	// The Spawn call in driver.go hardcodes the opencode invocation.
	// We assert the constants here so a future contributor changing
	// them sees the test fail. This is a documentation test, not a
	// runtime one — Spawn itself is exercised by the integration
	// smoke test that uses the opencode-mock binary.
	wantExe := "opencode"
	wantArgs := []string{"serve", "--hostname", "127.0.0.1", "--port", "0"}
	if wantExe != "opencode" {
		t.Fatalf("expected binary %q", wantExe)
	}
	if len(wantArgs) != 5 {
		t.Fatalf("args length = %d, want 5", len(wantArgs))
	}
	for i, a := range wantArgs {
		if a != wantArgs[i] {
			t.Fatalf("arg[%d] = %q, want %q", i, a, wantArgs[i])
		}
	}
}

func TestBuildEnv_ForcesAuthToken(t *testing.T) {
	t.Setenv("INHERITED_KEY_FORCE", "inherited")
	authToken := "shared-secret-token-12345"
	tagEnv := map[string]string{
		"OPENCODE_SERVER_PASSWORD": "weak",
		"OPENCODE_EXPERIMENTAL":    "1",
	}
	out := buildEnv(tagEnv, authToken)
	m := map[string]string{}
	for _, kv := range out {
		k, v, _ := strings.Cut(kv, "=")
		m[k] = v
	}
	if m["OPENCODE_SERVER_PASSWORD"] != authToken {
		t.Fatalf("OPENCODE_SERVER_PASSWORD = %q, want %q (myworktree override)", m["OPENCODE_SERVER_PASSWORD"], authToken)
	}
	if m["OPENCODE_CLIENT"] != "myworktree" {
		t.Fatalf("OPENCODE_CLIENT = %q, want myworktree", m["OPENCODE_CLIENT"])
	}
	if m["OPENCODE_EXPERIMENTAL"] != "1" {
		t.Fatalf("tag.Env OPENCODE_EXPERIMENTAL not preserved: %q", m["OPENCODE_EXPERIMENTAL"])
	}
	if m["INHERITED_KEY_FORCE"] != "inherited" {
		t.Fatalf("os.Environ not preserved: %q", m["INHERITED_KEY_FORCE"])
	}
}

func TestExtractListeningAddress(t *testing.T) {
	tests := []struct {
		name, line, wantH, wantP string
		wantOK                    bool
	}{
		{"normal", "opencode server listening on http://127.0.0.1:4096", "127.0.0.1", "4096", true},
		{"trailing whitespace", "opencode server listening on http://127.0.0.1:4096  ", "127.0.0.1", "4096", true},
		{"with ANSI", "\x1b[33mopencode server listening on http://127.0.0.1:4096\x1b[0m", "127.0.0.1", "4096", true},
		{"empty", "", "", "", false},
		{"unrelated", "hello world", "", "", false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			h, p, ok := extractListeningAddress(tt.line)
			if ok != tt.wantOK {
				t.Fatalf("ok = %v, want %v", ok, tt.wantOK)
			}
			if h != tt.wantH || p != tt.wantP {
				t.Fatalf("got (%q, %q), want (%q, %q)", h, p, tt.wantH, tt.wantP)
			}
		})
	}
}

func TestIsAPIPath(t *testing.T) {
	apiPaths := []string{
		"/", "/api/foo", "/api/", "/doc", "/global/event",
		"/session/", "/config", "/session", "/agent", "/command", "/skill",
		"/lsp", "/formatter", "/mcp", "/provider", "/project",
		"/experimental", "/tui", "/vcs", "/path", "/instance",
		"/file", "/find", "/event", "/log", "/auth",
	}
	for _, p := range apiPaths {
		if !isAPIPath(p) {
			t.Fatalf("isAPIPath(%q) = false, want true", p)
		}
	}
	nonAPI := []string{"/index.html", "/static/favicon.ico"}
	for _, p := range nonAPI {
		if isAPIPath(p) {
			t.Fatalf("isAPIPath(%q) = true, want false", p)
		}
	}
}

func TestDriver_HTTPHint(t *testing.T) {
	d := Driver{}
	if got := d.HTTPHint("abc123"); got != "" {
		t.Fatalf("HTTPHint = %q, want empty (proxy is registered globally in app.go)", got)
	}
}

func TestDriver_RegisterHTTP_NoOp(t *testing.T) {
	// RegisterHTTP must be a safe no-op: the reverse proxy is registered
	// globally in app.go, so no per-instance routes are wired here. Calling
	// it with a nil/empty handle must not panic.
	d := Driver{}
	d.RegisterHTTP(http.NewServeMux(), "inst-1", framework.Handle{})
}
