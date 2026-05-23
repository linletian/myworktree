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

func TestResolveGlobalAuthToken_FileNotExistsKeepsEmpty(t *testing.T) {
	tmpDir := t.TempDir()
	reset := config.SetPathForTest(testConfigPath(tmpDir))
	defer reset()

	var buf bytes.Buffer
	logger := log.New(&buf, "", 0)

	result := resolveGlobalAuthToken("", logger)
	if result != "" {
		t.Fatalf("expected empty auth, got %q", result)
	}
	if buf.Len() > 0 {
		t.Fatalf("expected no warning, got: %s", buf.String())
	}
}

func TestResolveGlobalAuthToken_CorruptedFileWarnsAndKeepsEmpty(t *testing.T) {
	tmpDir := t.TempDir()
	reset := config.SetPathForTest(testConfigPath(tmpDir))
	defer reset()

	path := filepath.Join(tmpDir, "myworktree", "auth.json")
	os.MkdirAll(filepath.Dir(path), 0o755)
	os.WriteFile(path, []byte("{invalid-json"), 0o600)

	var buf bytes.Buffer
	logger := log.New(&buf, "", 0)

	result := resolveGlobalAuthToken("", logger)
	if result != "" {
		t.Fatalf("expected empty auth for corrupted file, got %q", result)
	}
	if !bytes.Contains(buf.Bytes(), []byte("[config] auth.json is corrupted:")) {
		t.Fatalf("expected Warn log containing '[config] auth.json is corrupted:', got: %s", buf.String())
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
