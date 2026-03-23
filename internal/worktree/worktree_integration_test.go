package worktree

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"myworktree/internal/llm"
	"myworktree/internal/store"
)

func init() {
	// Wipe the global LLM config file before any test runs so that
	// llm.Load() (via sync.Once) always starts from a clean default.
	// This prevents state from a previous process run or an earlier test
	// in the same binary from leaking into integration tests.
	path, _ := llm.Path()
	_ = os.Remove(path)
}

func TestCreateDeleteWorktreeIntegration(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git is required")
	}
	repo := initGitRepo(t)
	dataDir := t.TempDir()
	fs := store.FileStore{Path: filepath.Join(dataDir, "state.json")}
	m := Manager{
		GitRoot: repo,
		DataDir: dataDir,
		Store:   fs,
	}

	wt, err := m.Create("fix login issue", "HEAD")
	if err != nil {
		t.Fatalf("Create failed: %v", err)
	}
	if wt.ID == "" || wt.Path == "" || wt.Branch == "" {
		t.Fatalf("created worktree has empty key fields: %#v", wt)
	}
	if _, err := os.Stat(wt.Path); err != nil {
		t.Fatalf("created worktree path should exist: %v", err)
	}

	if err := m.Delete(wt.ID); err != nil {
		t.Fatalf("Delete failed: %v", err)
	}
	st, err := fs.Load()
	if err != nil {
		t.Fatalf("load state failed: %v", err)
	}
	if len(st.Worktrees) != 0 {
		t.Fatalf("worktree should be removed from state, got %#v", st.Worktrees)
	}
}

func TestDeleteMissingWorktreeRemovesState(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git is required")
	}
	repo := initGitRepo(t)
	dataDir := t.TempDir()
	fs := store.FileStore{Path: filepath.Join(dataDir, "state.json")}
	m := Manager{
		GitRoot: repo,
		DataDir: dataDir,
		Store:   fs,
	}

	wt, err := m.Create("feature/ui improvements", "HEAD")
	if err != nil {
		t.Fatalf("Create failed: %v", err)
	}

	if err := os.RemoveAll(wt.Path); err != nil {
		t.Fatalf("remove worktree path failed: %v", err)
	}
	if _, err := os.Stat(wt.Path); !os.IsNotExist(err) {
		t.Fatalf("worktree path should be gone, got err=%v", err)
	}

	if err := m.Delete(wt.ID); err != nil {
		t.Fatalf("Delete failed for missing worktree: %v", err)
	}

	st, err := fs.Load()
	if err != nil {
		t.Fatalf("load state failed: %v", err)
	}
	if len(st.Worktrees) != 0 {
		t.Fatalf("worktree should be removed from state, got %#v", st.Worktrees)
	}

	items, err := listGitWorktrees(repo)
	if err != nil {
		t.Fatalf("list git worktrees failed: %v", err)
	}
	for _, item := range items {
		if filepath.Clean(item.Path) == filepath.Clean(wt.Path) {
			t.Fatalf("missing worktree should be pruned from git metadata, still found %#v", item)
		}
	}
}

func initGitRepo(t *testing.T) string {
	t.Helper()
	repo := t.TempDir()
	runGit(t, repo, "init")
	runGit(t, repo, "config", "user.name", "Test User")
	runGit(t, repo, "config", "user.email", "test@example.com")
	if err := os.WriteFile(filepath.Join(repo, "README.md"), []byte("init\n"), 0o600); err != nil {
		t.Fatalf("write seed file failed: %v", err)
	}
	runGit(t, repo, "add", "README.md")
	runGit(t, repo, "commit", "-m", "init")
	return repo
}

func runGit(t *testing.T, dir string, args ...string) {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %v failed: %v (%s)", args, err, string(out))
	}
}
