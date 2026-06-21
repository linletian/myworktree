package instance

import (
	"encoding/hex"
	"os"
	"strings"
	"testing"
)

func TestCommand(t *testing.T) {
	exe, args := Command()
	if exe != "opencode" {
		t.Fatalf("exe = %q, want opencode", exe)
	}
	if len(args) != 5 {
		t.Fatalf("len(args) = %d, want 5", len(args))
	}
	if args[0] != "serve" || args[1] != "--hostname" || args[2] != "127.0.0.1" || args[3] != "--port" || args[4] != "0" {
		t.Fatalf("args = %v, want [serve --hostname 127.0.0.1 --port 0]", args)
	}
}

func TestGeneratePassword(t *testing.T) {
	p1, err := GeneratePassword()
	if err != nil {
		t.Fatalf("GeneratePassword: %v", err)
	}
	if len(p1) != 32 {
		t.Fatalf("len = %d, want 32", len(p1))
	}
	// Must be valid hex.
	if _, decErr := hex.DecodeString(p1); decErr != nil {
		t.Fatalf("not valid hex: %v", decErr)
	}
	// Two calls must produce different values.
	p2, _ := GeneratePassword()
	if p1 == p2 {
		t.Fatal("two passwords are equal; want different")
	}
}

func TestBuildEnv(t *testing.T) {
	// Test: tagEnv contains weak OPENCODE_SERVER_PASSWORD → overwritten.
	t.Setenv("INHERITED_KEY", "inherited_val")
	password := "abc123def456ghi78901234567890ab"
	tagEnv := map[string]string{
		"FOO":                      "bar",
		"OPENCODE_SERVER_PASSWORD": "weak-pass",
		"OPENCODE_EXPERIMENTAL":    "1",
	}
	out := BuildEnv(tagEnv, password)
	m := make(map[string]string, len(out))
	for _, kv := range out {
		k, v, _ := strings.Cut(kv, "=")
		m[k] = v
	}
	if m["FOO"] != "bar" {
		t.Fatalf("FOO = %q, want bar", m["FOO"])
	}
	if m["OPENCODE_SERVER_PASSWORD"] != password {
		t.Fatalf("OPENCODE_SERVER_PASSWORD = %q, want %q (myworktree override)", m["OPENCODE_SERVER_PASSWORD"], password)
	}
	if m["OPENCODE_CLIENT"] != "myworktree" {
		t.Fatalf("OPENCODE_CLIENT = %q, want myworktree", m["OPENCODE_CLIENT"])
	}
	if m["OPENCODE_EXPERIMENTAL"] != "1" {
		t.Fatalf("OPENCODE_EXPERIMENTAL = %q, want 1 (tag env should be passed through)", m["OPENCODE_EXPERIMENTAL"])
	}
	if m["INHERITED_KEY"] != "inherited_val" {
		t.Fatalf("INHERITED_KEY = %q, want inherited_val (os.Environ not preserved)", m["INHERITED_KEY"])
	}
}

func TestBuildEnv_NilTagEnv(t *testing.T) {
	password := "deadbeef0123456789abcdef01234567"
	out := BuildEnv(nil, password)
	m := make(map[string]string, len(out))
	for _, kv := range out {
		k, v, _ := strings.Cut(kv, "=")
		m[k] = v
	}
	if m["OPENCODE_SERVER_PASSWORD"] != password {
		t.Fatalf("password not injected when tagEnv is nil")
	}
	if m["OPENCODE_CLIENT"] != "myworktree" {
		t.Fatalf("client not injected when tagEnv is nil")
	}
}

func TestExtractListeningAddress(t *testing.T) {
	tests := []struct {
		name   string
		line   string
		wantH  string
		wantP  string
		wantOK bool
	}{
		{"normal", "opencode server listening on http://127.0.0.1:4096", "127.0.0.1", "4096", true},
		{"trailing", "opencode server listening on http://127.0.0.1:4096\r\n", "127.0.0.1", "4096", true},
		{"with spaces", "  opencode server listening on http://127.0.0.1:4096  ", "127.0.0.1", "4096", true},
		{"with ANSI", "\x1b[33mopencode server listening on http://127.0.0.1:4096\x1b[0m", "127.0.0.1", "4096", true},
		{"empty", "", "", "", false},
		{"random text", "some random output", "", "", false},
		{"partial (missing url)", "opencode server listening on", "", "", false},
		{"partial (wrong prefix)", "listening on http://127.0.0.1:4096", "", "", false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			h, p, ok := ExtractListeningAddress(tt.line)
			if ok != tt.wantOK {
				t.Fatalf("ok = %v, want %v", ok, tt.wantOK)
			}
			if h != tt.wantH {
				t.Fatalf("host = %q, want %q", h, tt.wantH)
			}
			if p != tt.wantP {
				t.Fatalf("port = %q, want %q", p, tt.wantP)
			}
		})
	}
}

func TestIsAPIPath(t *testing.T) {
	for _, p := range []string{
		"/api/foo", "/api/", "/doc", "/global/event",
		"/config", "/session", "/agent", "/command", "/skill",
		"/lsp", "/formatter", "/mcp", "/provider", "/project",
		"/experimental", "/tui", "/vcs", "/path", "/instance",
		"/file", "/find", "/event", "/log", "/auth",
	} {
		if !IsAPIPath(p) {
			t.Fatalf("IsAPIPath(%q) = false, want true", p)
		}
	}
	for _, p := range []string{
		"/", "/index.html", "/assets/x.js", "/static/favicon.ico",
	} {
		if IsAPIPath(p) {
			t.Fatalf("IsAPIPath(%q) = true, want false", p)
		}
	}
}

func TestStripANSI(t *testing.T) {
	clean := stripANSI("\x1b[33mhello\x1b[0m")
	if clean != "hello" {
		t.Fatalf("stripANSI = %q, want hello", clean)
	}
	clean = stripANSI("  normal text  ")
	if clean != "normal text" {
		t.Fatalf("stripANSI = %q, want normal text", clean)
	}
}

func init() {
	// Avoid picking up stray env from test runner for BuildEnv.
	os.Setenv("INHERITED_KEY", "")
}
