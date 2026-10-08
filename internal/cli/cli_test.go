package cli

import (
	"bytes"
	"encoding/json"
	"io"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"myworktree/internal/config"
	"myworktree/internal/store"
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

// setupWorktreeDeleteCLI builds a real git repo with one registered managed
// worktree and points the CLI at it (cwd + XDG_CONFIG_HOME), returning the
// worktree path. The delete subcommand shells out to git, so only a real
// repo exercises it honestly.
func setupWorktreeDeleteCLI(t *testing.T) string {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git is required")
	}
	repo := t.TempDir()
	runGitCLI := func(args ...string) {
		t.Helper()
		cmd := exec.Command("git", args...)
		cmd.Dir = repo
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v failed: %v (%s)", args, err, out)
		}
	}
	runGitCLI("init")
	runGitCLI("config", "user.name", "Test User")
	runGitCLI("config", "user.email", "test@example.com")
	if err := os.WriteFile(filepath.Join(repo, "README.md"), []byte("init\n"), 0o600); err != nil {
		t.Fatalf("write seed: %v", err)
	}
	runGitCLI("add", "README.md")
	runGitCLI("commit", "-m", "init")
	wtPath := filepath.Join(t.TempDir(), "wt")
	runGitCLI("worktree", "add", wtPath, "-b", "wt-branch")

	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	dataDir, err := projectDataDir(repo)
	if err != nil {
		t.Fatalf("projectDataDir: %v", err)
	}
	if err := os.MkdirAll(dataDir, 0o755); err != nil {
		t.Fatalf("mkdir dataDir: %v", err)
	}
	fs := store.FileStore{Path: filepath.Join(dataDir, "state.json")}
	if err := fs.Save(store.State{
		Worktrees: []store.ManagedWorktree{{ID: "wt1", Name: "wt1", Path: wtPath, Branch: "wt-branch"}},
	}); err != nil {
		t.Fatalf("save state: %v", err)
	}

	oldWd, err := os.Getwd()
	if err != nil {
		t.Fatalf("getwd: %v", err)
	}
	if err := os.Chdir(repo); err != nil {
		t.Fatalf("chdir: %v", err)
	}
	t.Cleanup(func() { _ = os.Chdir(oldWd) })
	return wtPath
}

// TestRunWorktreeDeleteTrailingForceFlag pins the issue #102 review fix:
// `myworktree worktree delete <id> --force` must actually force — Go's flag
// package stops parsing at the first positional arg, so the previous
// FlagSet-based parsing silently dropped a trailing --force and the user hit
// the very refusal the message told them to retry past.
func TestRunWorktreeDeleteTrailingForceFlag(t *testing.T) {
	wtPath := setupWorktreeDeleteCLI(t)
	// Dirty the worktree so only a forced delete can remove it.
	if err := os.WriteFile(filepath.Join(wtPath, "scratch.txt"), []byte("x\n"), 0o600); err != nil {
		t.Fatalf("write scratch: %v", err)
	}

	if code := Run([]string{"myworktree", "worktree", "delete", "wt1", "--force"}, log.New(io.Discard, "", 0)); code != 0 {
		t.Fatalf("trailing --force delete should succeed, exit code %d", code)
	}
	if _, err := os.Stat(wtPath); !os.IsNotExist(err) {
		t.Fatalf("trailing --force should delete the worktree, stat err=%v", err)
	}
}

// TestRunWorktreeDeleteExtraPositionalFails pins the usage guard: anything
// beyond the single id is a usage error, not a silently ignored argument.
func TestRunWorktreeDeleteExtraPositionalFails(t *testing.T) {
	setupWorktreeDeleteCLI(t)
	if code := Run([]string{"myworktree", "worktree", "delete", "wt1", "wt2"}, log.New(io.Discard, "", 0)); code == 0 {
		t.Fatal("extra positional args must fail with a usage error")
	}
}
