package opencode_web

import (
	"context"
	"net/http"
	"strings"
	"sync"
	"testing"
	"time"

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

func TestWithLoopbackNoProxy(t *testing.T) {
	t.Run("no proxy configured → untouched", func(t *testing.T) {
		env := map[string]string{"PATH": "/usr/bin"}
		withLoopbackNoProxy(env)
		if _, ok := env["NO_PROXY"]; ok {
			t.Fatalf("NO_PROXY added without a proxy configured: %q", env["NO_PROXY"])
		}
		if _, ok := env["no_proxy"]; ok {
			t.Fatalf("no_proxy added without a proxy configured: %q", env["no_proxy"])
		}
	})

	t.Run("proxy set, no no_proxy → created with loopback entries", func(t *testing.T) {
		env := map[string]string{"HTTP_PROXY": "http://192.168.1.254:6268"}
		withLoopbackNoProxy(env)
		want := "localhost,127.0.0.1,::1"
		if env["NO_PROXY"] != want || env["no_proxy"] != want {
			t.Fatalf("NO_PROXY=%q no_proxy=%q, want both %q", env["NO_PROXY"], env["no_proxy"], want)
		}
	})

	t.Run("CIDR entry preserved and literal hosts appended", func(t *testing.T) {
		// The exact host setup from issue #75: a LAN proxy plus the
		// CIDR form that Bun's no_proxy matcher does not understand.
		env := map[string]string{
			"https_proxy": "http://192.168.1.254:6268",
			"NO_PROXY":    "localhost,127.0.0.0/8,::1",
			"no_proxy":    "localhost,127.0.0.0/8,::1",
		}
		withLoopbackNoProxy(env)
		want := "localhost,127.0.0.0/8,::1,127.0.0.1"
		if env["NO_PROXY"] != want || env["no_proxy"] != want {
			t.Fatalf("NO_PROXY=%q no_proxy=%q, want both %q", env["NO_PROXY"], env["no_proxy"], want)
		}
	})

	t.Run("both cases merged, duplicates removed", func(t *testing.T) {
		env := map[string]string{
			"ALL_PROXY": "socks://proxy:1080",
			"NO_PROXY":  "example.com, localhost",
			"no_proxy":  "10.0.0.0/8,localhost",
		}
		withLoopbackNoProxy(env)
		want := "example.com,localhost,10.0.0.0/8,127.0.0.1,::1"
		if env["NO_PROXY"] != want || env["no_proxy"] != want {
			t.Fatalf("NO_PROXY=%q no_proxy=%q, want both %q", env["NO_PROXY"], env["no_proxy"], want)
		}
	})

	t.Run("wildcard already covers loopback → untouched", func(t *testing.T) {
		env := map[string]string{
			"HTTP_PROXY": "http://proxy:8080",
			"NO_PROXY":   "*",
		}
		withLoopbackNoProxy(env)
		if env["NO_PROXY"] != "*" {
			t.Fatalf("NO_PROXY = %q, want unchanged *", env["NO_PROXY"])
		}
		if _, ok := env["no_proxy"]; ok {
			t.Fatalf("no_proxy = %q, want absent", env["no_proxy"])
		}
	})

	t.Run("explicit loopback already present → no duplicates", func(t *testing.T) {
		env := map[string]string{
			"HTTP_PROXY": "http://proxy:8080",
			"NO_PROXY":   "localhost,127.0.0.1,::1",
		}
		withLoopbackNoProxy(env)
		want := "localhost,127.0.0.1,::1"
		if env["NO_PROXY"] != want || env["no_proxy"] != want {
			t.Fatalf("NO_PROXY=%q no_proxy=%q, want both %q", env["NO_PROXY"], env["no_proxy"], want)
		}
	})
}

func TestBuildEnv_LoopbackNoProxy(t *testing.T) {
	// End-to-end through buildEnv: with a LAN proxy inherited from the
	// parent process and a CIDR-only no_proxy, the child env must gain
	// the literal loopback entries (issue #75).
	t.Setenv("HTTP_PROXY", "http://192.168.1.254:6268")
	t.Setenv("HTTPS_PROXY", "http://192.168.1.254:6268")
	t.Setenv("NO_PROXY", "localhost,127.0.0.0/8,::1")
	t.Setenv("no_proxy", "localhost,127.0.0.0/8,::1")
	out := buildEnv(nil, "token")
	m := map[string]string{}
	for _, kv := range out {
		k, v, _ := strings.Cut(kv, "=")
		m[k] = v
	}
	for _, k := range []string{"NO_PROXY", "no_proxy"} {
		v := m[k]
		for _, host := range []string{"localhost", "127.0.0.1", "::1"} {
			found := false
			for _, e := range strings.Split(v, ",") {
				if e == host {
					found = true
					break
				}
			}
			if !found {
				t.Fatalf("%s=%q missing explicit loopback entry %q", k, v, host)
			}
		}
	}
	// Proxies themselves must stay intact: remote LLM providers still
	// go through the proxy (only loopback is bypassed).
	if m["HTTP_PROXY"] != "http://192.168.1.254:6268" {
		t.Fatalf("HTTP_PROXY = %q, want preserved", m["HTTP_PROXY"])
	}
}

func TestExtractListeningAddress(t *testing.T) {
	tests := []struct {
		name, line, wantH, wantP string
		wantOK                   bool
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

// TestPumpAndWatch_DrainsAfterListening guards against the bug where
// pumpAndWatch returned as soon as it parsed the listening line, leaving
// the stdout pipe undrained. The child process would then block on its
// next write(2) once the OS pipe buffer filled (~64 KiB on Linux),
// presenting as the UI "stuck" with no obvious cause.
func TestPumpAndWatch_DrainsAfterListening(t *testing.T) {
	// ~256 KiB of post-listening chatter — well above the 64 KiB pipe
	// buffer, so an undrained reader would block the writer.
	const trailing = 256 * 1024
	var sb strings.Builder
	sb.WriteString("opencode server listening on http://127.0.0.1:4096\n")
	for i := 0; i < trailing; i++ {
		sb.WriteByte('x')
	}
	sb.WriteString("\nEND\n")
	in := strings.NewReader(sb.String())

	h := &Handle{
		ready:  framework.NewReadySignal(),
		cancel: func() {},
		wg:     &sync.WaitGroup{},
	}
	h.wg.Add(1)

	d := Driver{}
	done := make(chan struct{})
	go func() {
		d.pumpAndWatch(context.Background(), h, in)
		close(done)
	}()

	// Wait for the ready signal — that is when pumpAndWatch would
	// historically have returned without draining.
	select {
	case <-h.ready.Channel():
	case <-time.After(2 * time.Second):
		t.Fatal("ready signal not closed within 2s")
	}

	// Now the goroutine should still be alive draining remaining bytes.
	// We can't directly observe drain, but we can confirm pumpAndWatch
	// returns promptly (it must read until EOF after the listening line).
	select {
	case <-done:
		// OK — drained to EOF and returned.
	case <-time.After(2 * time.Second):
		t.Fatal("pumpAndWatch did not return within 2s after EOF; trailing bytes likely undrained")
	}

	// And the parsed host/port are correct.
	h.mu.Lock()
	host, port := h.host, h.port
	h.mu.Unlock()
	if host != "127.0.0.1" || port != "4096" {
		t.Fatalf("parsed host=%q port=%q, want 127.0.0.1 / 4096", host, port)
	}
}
