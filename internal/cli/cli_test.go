package cli

import (
	"bytes"
	"encoding/json"
	"io"
	"log"
	"os"
	"path/filepath"
	"testing"

	"myworktree/internal/config"
	"myworktree/internal/version"
)

func TestRunVersionSubcommand(t *testing.T) {
	oldVersion, oldCommit, oldBuildDate := version.Version, version.Commit, version.BuildDate
	version.Version = "v0.1.0"
	version.Commit = "1234567890abcdef"
	version.BuildDate = "2026-03-11T10:00:00Z"
	t.Cleanup(func() {
		version.Version = oldVersion
		version.Commit = oldCommit
		version.BuildDate = oldBuildDate
	})

	stdout := captureStdout(t)
	if code := Run([]string{"myworktree", "version"}, log.New(io.Discard, "", 0)); code != 0 {
		t.Fatalf("expected exit code 0, got %d", code)
	}
	if got := stdout(); got != "myworktree v0.1.0 (1234567890ab) built 2026-03-11T10:00:00Z\n" {
		t.Fatalf("unexpected stdout: %q", got)
	}
}

func TestRunVersionFlag(t *testing.T) {
	oldVersion, oldCommit, oldBuildDate := version.Version, version.Commit, version.BuildDate
	version.Version = "v0.1.0"
	version.Commit = "1234567890abcdef"
	version.BuildDate = "2026-03-11T10:00:00Z"
	t.Cleanup(func() {
		version.Version = oldVersion
		version.Commit = oldCommit
		version.BuildDate = oldBuildDate
	})

	stdout := captureStdout(t)
	if code := Run([]string{"mw", "--version"}, log.New(io.Discard, "", 0)); code != 0 {
		t.Fatalf("expected exit code 0, got %d", code)
	}
	if got := stdout(); got != "mw v0.1.0 (1234567890ab) built 2026-03-11T10:00:00Z\n" {
		t.Fatalf("unexpected stdout: %q", got)
	}
}

func captureStdout(t *testing.T) func() string {
	t.Helper()

	oldStdout := os.Stdout
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatalf("os.Pipe failed: %v", err)
	}
	os.Stdout = w

	return func() string {
		_ = w.Close()
		os.Stdout = oldStdout

		var buf bytes.Buffer
		if _, err := io.Copy(&buf, r); err != nil {
			t.Fatalf("copy stdout failed: %v", err)
		}
		_ = r.Close()
		return buf.String()
	}
}

func testConfigPath(tmpDir string) func() (string, error) {
	return func() (string, error) {
		return filepath.Join(tmpDir, "myworktree", "auth.json"), nil
	}
}

func TestResolveGlobalAuthToken_NormalFileInheritsToken(t *testing.T) {
	tmpDir := t.TempDir()
	reset := config.SetPathForTest(testConfigPath(tmpDir))
	defer reset()

	token := "test-token-inherit"
	path := filepath.Join(tmpDir, "myworktree", "auth.json")
	os.MkdirAll(filepath.Dir(path), 0o755)
	cfg := config.GlobalConfig{AuthToken: token}
	data, _ := json.Marshal(cfg)
	os.WriteFile(path, data, 0o600)

	var buf bytes.Buffer
	logger := log.New(&buf, "", 0)

	result := resolveGlobalAuthToken("", logger)
	if result != token {
		t.Fatalf("expected token %q, got %q", token, result)
	}
	if buf.Len() > 0 {
		t.Fatalf("expected no warning, got: %s", buf.String())
	}
}

func TestResolveGlobalAuthToken_FileNotExistsAutoGeneratesToken(t *testing.T) {
	tmpDir := t.TempDir()
	reset := config.SetPathForTest(testConfigPath(tmpDir))
	defer reset()

	var buf bytes.Buffer
	logger := log.New(&buf, "", 0)

	result := resolveGlobalAuthToken("", logger)
	if result == "" {
		t.Fatal("expected auto-generated auth token, got empty string")
	}
	if len(result) != 32 {
		t.Fatalf("expected 32-char hex token, got %d chars: %q", len(result), result)
	}
	if buf.Len() > 0 {
		t.Fatalf("expected no warning, got: %s", buf.String())
	}

	cfg, err := config.Load()
	if err != nil {
		t.Fatalf("failed to load config after auto-generation: %v", err)
	}
	if cfg.AuthToken != result {
		t.Fatalf("saved token %q does not match returned token %q", cfg.AuthToken, result)
	}
}

func TestResolveGlobalAuthToken_CorruptedFileWarnsAndAutoGenerates(t *testing.T) {
	tmpDir := t.TempDir()
	reset := config.SetPathForTest(testConfigPath(tmpDir))
	defer reset()

	path := filepath.Join(tmpDir, "myworktree", "auth.json")
	os.MkdirAll(filepath.Dir(path), 0o755)
	os.WriteFile(path, []byte("{invalid-json"), 0o600)

	var buf bytes.Buffer
	logger := log.New(&buf, "", 0)

	result := resolveGlobalAuthToken("", logger)
	if result == "" {
		t.Fatal("expected auto-generated token for corrupted config, got empty")
	}
	if !bytes.Contains(buf.Bytes(), []byte("[config] failed to load auth config:")) {
		t.Fatalf("expected Warn log containing '[config] failed to load auth config:', got: %s", buf.String())
	}

	cfg, err := config.Load()
	if err != nil {
		t.Fatalf("failed to load fixed config: %v", err)
	}
	if cfg.AuthToken != result {
		t.Fatalf("saved token %q does not match returned token %q", cfg.AuthToken, result)
	}
}

func TestResolveGlobalAuthToken_AuthAlreadySet(t *testing.T) {
	tmpDir := t.TempDir()
	reset := config.SetPathForTest(testConfigPath(tmpDir))
	defer reset()

	var buf bytes.Buffer
	logger := log.New(&buf, "", 0)

	result := resolveGlobalAuthToken("explicit-token", logger)
	if result != "explicit-token" {
		t.Fatalf("expected auth to remain %q, got %q", "explicit-token", result)
	}
	if buf.Len() > 0 {
		t.Fatalf("expected no warning/log when --auth is explicitly set, got: %s", buf.String())
	}
}

func TestConfigRegenAuth_EmptyConfigGeneratesNewToken(t *testing.T) {
	tmpDir := t.TempDir()
	reset := config.SetPathForTest(testConfigPath(tmpDir))
	defer reset()

	path := filepath.Join(tmpDir, "myworktree", "auth.json")
	os.MkdirAll(filepath.Dir(path), 0o755)
	os.WriteFile(path, []byte(`{}`), 0o600)

	err := configRegenAuth(nil)
	if err != nil {
		t.Fatalf("configRegenAuth failed: %v", err)
	}

	cfg, err := config.Load()
	if err != nil {
		t.Fatalf("Load after regen failed: %v", err)
	}
	if cfg.AuthToken == "" {
		t.Fatal("expected non-empty token after regen")
	}
	if len(cfg.AuthToken) != 32 {
		t.Fatalf("expected 32-char hex token, got %d chars: %q", len(cfg.AuthToken), cfg.AuthToken)
	}
}

func TestConfigRegenAuth_NoExistingTokenSkipsConfirmation(t *testing.T) {
	tmpDir := t.TempDir()
	reset := config.SetPathForTest(testConfigPath(tmpDir))
	defer reset()

	path := filepath.Join(tmpDir, "myworktree", "auth.json")
	os.MkdirAll(filepath.Dir(path), 0o755)
	os.WriteFile(path, []byte(`{}`), 0o600)

	restore := captureStdin(t, "y\n")
	err := configRegenAuth(nil)
	restore()
	if err != nil {
		t.Fatalf("configRegenAuth failed: %v", err)
	}

	cfg, err := config.Load()
	if err != nil {
		t.Fatalf("Load after regen failed: %v", err)
	}
	if cfg.AuthToken == "" {
		t.Fatal("expected non-empty token after regen")
	}
}

func TestConfigRegenAuth_ExistingTokenRequiresConfirmation(t *testing.T) {
	tmpDir := t.TempDir()
	reset := config.SetPathForTest(testConfigPath(tmpDir))
	defer reset()

	path := filepath.Join(tmpDir, "myworktree", "auth.json")
	os.MkdirAll(filepath.Dir(path), 0o755)
	os.WriteFile(path, []byte(`{"auth_token":"old-token-value"}`), 0o600)

	restore := captureStdin(t, "n\n")
	err := configRegenAuth(nil)
	restore()
	if err != nil {
		t.Fatalf("configRegenAuth failed: %v", err)
	}

	cfg, err := config.Load()
	if err != nil {
		t.Fatalf("Load after aborted regen failed: %v", err)
	}
	if cfg.AuthToken != "old-token-value" {
		t.Fatalf("expected old token to remain unchanged after abort, got %q", cfg.AuthToken)
	}
}

func TestConfigRegenAuth_YesConfirmationProceeds(t *testing.T) {
	tmpDir := t.TempDir()
	reset := config.SetPathForTest(testConfigPath(tmpDir))
	defer reset()

	path := filepath.Join(tmpDir, "myworktree", "auth.json")
	os.MkdirAll(filepath.Dir(path), 0o755)
	os.WriteFile(path, []byte(`{"auth_token":"old-token-value"}`), 0o600)

	restore := captureStdin(t, "y\n")
	err := configRegenAuth(nil)
	restore()
	if err != nil {
		t.Fatalf("configRegenAuth failed: %v", err)
	}

	cfg, err := config.Load()
	if err != nil {
		t.Fatalf("Load after regen failed: %v", err)
	}
	if cfg.AuthToken == "old-token-value" {
		t.Fatal("expected token to be regenerated, but old token remains")
	}
	if len(cfg.AuthToken) != 32 {
		t.Fatalf("expected 32-char hex token, got %d chars: %q", len(cfg.AuthToken), cfg.AuthToken)
	}
}

func captureStdin(t *testing.T, input string) (restore func()) {
	t.Helper()
	oldStdin := os.Stdin
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatalf("os.Pipe failed: %v", err)
	}
	go func() {
		w.WriteString(input)
		w.Close()
	}()
	os.Stdin = r
	return func() {
		os.Stdin = oldStdin
		r.Close()
	}
}
