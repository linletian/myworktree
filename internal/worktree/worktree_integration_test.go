package worktree

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
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

	if err := m.Delete(wt.ID, false); err != nil {
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

	if err := m.Delete(wt.ID, false); err != nil {
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

// makeWorktreeDirty reproduces the issue #102 shape inside a managed
// worktree: a tracked file deleted, an untracked scratch file, and a
// gitignored file (which the blocking dirty check must NOT count, but the
// refusal must WARN about — force would destroy it unrecoverably).
func makeWorktreeDirty(t *testing.T, wtPath string) {
	t.Helper()
	if err := os.Remove(filepath.Join(wtPath, "README.md")); err != nil {
		t.Fatalf("remove tracked file failed: %v", err)
	}
	if err := os.WriteFile(filepath.Join(wtPath, "scratch.txt"), []byte("scratch\n"), 0o600); err != nil {
		t.Fatalf("write untracked file failed: %v", err)
	}
	if err := os.WriteFile(filepath.Join(wtPath, ".gitignore"), []byte("*.log\n"), 0o600); err != nil {
		t.Fatalf("write .gitignore failed: %v", err)
	}
	if err := os.WriteFile(filepath.Join(wtPath, "debug.log"), []byte("log\n"), 0o600); err != nil {
		t.Fatalf("write ignored file failed: %v", err)
	}
}

func TestDeleteDirtyRefusalCarriesDetails(t *testing.T) {
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

	wt, err := m.Create("dirty work", "HEAD")
	if err != nil {
		t.Fatalf("Create failed: %v", err)
	}
	makeWorktreeDirty(t, wt.Path)

	err = m.Delete(wt.ID, false)
	var dirty *DirtyWorktreeError
	if !errors.As(err, &dirty) {
		t.Fatalf("Delete should refuse with DirtyWorktreeError, got %v", err)
	}
	// " D README.md" is the one deletion; "?? .gitignore" + "?? scratch.txt"
	// are the two untracked entries; debug.log is ignored and must NOT
	// block — only surface as the force-risk count.
	if dirty.Entries != 3 || dirty.Deleted != 1 || dirty.Modified != 0 || dirty.Untracked != 2 {
		t.Fatalf("dirty breakdown = entries:%d deleted:%d modified:%d untracked:%d, want 3/1/0/2",
			dirty.Entries, dirty.Deleted, dirty.Modified, dirty.Untracked)
	}
	if dirty.Ignored != 1 {
		t.Fatalf("Ignored = %d, want 1 (debug.log) — the gitignored file must be NAMED as a force risk even though it does not block", dirty.Ignored)
	}
	if len(dirty.FirstPaths) == 0 || dirty.Porcelain == "" {
		t.Fatalf("refusal must carry first paths and the full porcelain output, got %#v", dirty)
	}
	msg := err.Error()
	for _, want := range []string{"3 entries", "1 deleted", "2 untracked", "1 gitignored entry", "retry with force"} {
		if !strings.Contains(msg, want) {
			t.Fatalf("refusal message missing %q:\n%s", want, msg)
		}
	}

	// The refusal changed nothing: state keeps the record, the path lives.
	st, err := fs.Load()
	if err != nil {
		t.Fatalf("load state failed: %v", err)
	}
	if len(st.Worktrees) != 1 {
		t.Fatalf("refused delete must keep the state record, got %#v", st.Worktrees)
	}
	if _, err := os.Stat(wt.Path); err != nil {
		t.Fatalf("refused delete must keep the path: %v", err)
	}
}

func TestDeleteForceRemovesDirtyWorktree(t *testing.T) {
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

	wt, err := m.Create("force me", "HEAD")
	if err != nil {
		t.Fatalf("Create failed: %v", err)
	}
	makeWorktreeDirty(t, wt.Path)

	if err := m.Delete(wt.ID, true); err != nil {
		t.Fatalf("force Delete failed: %v", err)
	}
	if _, err := os.Stat(wt.Path); !os.IsNotExist(err) {
		t.Fatalf("force delete should remove the path, got err=%v", err)
	}
	st, err := fs.Load()
	if err != nil {
		t.Fatalf("load state failed: %v", err)
	}
	if len(st.Worktrees) != 0 {
		t.Fatalf("force delete should drop the state record, got %#v", st.Worktrees)
	}
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
