package worktree

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"myworktree/internal/store"
)

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
