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

	if _, err := m.Delete(wt.ID, false); err != nil {
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

	if _, err := m.Delete(wt.ID, false); err != nil {
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

	_, err = m.Delete(wt.ID, false)
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

	if _, err := m.Delete(wt.ID, true); err != nil {
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

// worktreeGitDir resolves a managed worktree's real git dir from the
// "gitdir:" pointer file (a linked worktree's .git is a file, not a dir).
func worktreeGitDir(t *testing.T, wtPath string) string {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join(wtPath, ".git"))
	if err != nil {
		t.Fatalf("read .git pointer failed: %v", err)
	}
	line := strings.TrimSpace(string(raw))
	if !strings.HasPrefix(line, "gitdir: ") {
		t.Fatalf("unexpected .git pointer content: %q", line)
	}
	return strings.TrimPrefix(line, "gitdir: ")
}

// TestDeleteForceSkipsStatusProbe pins the issue #102 review fix: force must
// NOT gate on `git status` — on a damaged worktree (corrupt index) the probe
// fails while `git worktree remove --force` still succeeds, and force is the
// escape hatch for exactly that workspace.
func TestDeleteForceSkipsStatusProbe(t *testing.T) {
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

	wt, err := m.Create("damaged", "HEAD")
	if err != nil {
		t.Fatalf("Create failed: %v", err)
	}
	if err := os.WriteFile(filepath.Join(worktreeGitDir(t, wt.Path), "index"), []byte("garbage"), 0o600); err != nil {
		t.Fatalf("corrupt index failed: %v", err)
	}
	// Sanity: the probe really is broken now.
	if out, statErr := exec.Command("git", "-C", wt.Path, "status", "--porcelain").CombinedOutput(); statErr == nil {
		t.Fatalf("precondition failed: git status should fail on the corrupt index, got %s", out)
	}

	if _, err := m.Delete(wt.ID, true); err != nil {
		t.Fatalf("force Delete must not gate on git status (corrupt index): %v", err)
	}
	if _, err := os.Stat(wt.Path); !os.IsNotExist(err) {
		t.Fatalf("force delete should remove the path, got err=%v", err)
	}
	// The non-force path must still surface the probe failure.
	wt2, err := m.Create("damaged again", "HEAD")
	if err != nil {
		t.Fatalf("Create failed: %v", err)
	}
	if err := os.WriteFile(filepath.Join(worktreeGitDir(t, wt2.Path), "index"), []byte("garbage"), 0o600); err != nil {
		t.Fatalf("corrupt index failed: %v", err)
	}
	if _, err := m.Delete(wt2.ID, false); err == nil {
		t.Fatal("non-force Delete should fail when the status probe fails")
	}
	_ = os.RemoveAll(wt2.Path)
}

// TestDeleteUntrackedDirCountsFilesIndividually pins the -uall probe (issue
// #102 review): the default porcelain output collapses an untracked
// DIRECTORY to one "?? dir/" line, so a dependency dir with a thousand
// files would read as "1 untracked" in the refusal.
func TestDeleteUntrackedDirCountsFilesIndividually(t *testing.T) {
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

	wt, err := m.Create("untracked dir", "HEAD")
	if err != nil {
		t.Fatalf("Create failed: %v", err)
	}
	dir := filepath.Join(wt.Path, "vendorlibs")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("mkdir failed: %v", err)
	}
	for _, name := range []string{"a.so", "b.so", "c.so"} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte("x\n"), 0o600); err != nil {
			t.Fatalf("write failed: %v", err)
		}
	}

	_, err = m.Delete(wt.ID, false)
	var dirty *DirtyWorktreeError
	if !errors.As(err, &dirty) {
		t.Fatalf("Delete should refuse with DirtyWorktreeError, got %v", err)
	}
	if dirty.Untracked != 3 {
		t.Fatalf("Untracked = %d, want 3 — the untracked dir must count file-by-file (-uall), not collapse to one entry", dirty.Untracked)
	}
}

// TestDeleteRefusalKeepsNonASCIIPathsLiteral pins core.quotePath=false on the
// probe (issue #102 review): git's default C-style quoting would show a
// Chinese filename as octal escapes in the refusal and the dialog.
func TestDeleteRefusalKeepsNonASCIIPathsLiteral(t *testing.T) {
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

	wt, err := m.Create("unicode", "HEAD")
	if err != nil {
		t.Fatalf("Create failed: %v", err)
	}
	if err := os.WriteFile(filepath.Join(wt.Path, "中文文件.txt"), []byte("x\n"), 0o600); err != nil {
		t.Fatalf("write failed: %v", err)
	}

	_, err = m.Delete(wt.ID, false)
	var dirty *DirtyWorktreeError
	if !errors.As(err, &dirty) {
		t.Fatalf("Delete should refuse with DirtyWorktreeError, got %v", err)
	}
	if !strings.Contains(dirty.Porcelain, "中文文件.txt") {
		t.Fatalf("porcelain should carry the literal non-ASCII path, got:\n%s", dirty.Porcelain)
	}
	if len(dirty.FirstPaths) != 1 || dirty.FirstPaths[0] != "中文文件.txt" {
		t.Fatalf("FirstPaths = %#v, want the literal non-ASCII path (no octal escapes)", dirty.FirstPaths)
	}
}

// TestDeleteCleanWorktreeReportsIgnoredDestroyed pins the issue #102 review
// follow-up: a worktree whose ONLY contents at risk are gitignored files is
// deleted without a refusal (nothing blocks), but the caller must learn how
// many gitignored entries vanished with the directory instead of it passing
// silently. Ignored directories count file-by-file (-uall).
func TestDeleteCleanWorktreeReportsIgnoredDestroyed(t *testing.T) {
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

	wt, err := m.Create("ignored only", "HEAD")
	if err != nil {
		t.Fatalf("Create failed: %v", err)
	}
	if err := os.WriteFile(filepath.Join(wt.Path, ".gitignore"), []byte("*.log\nbuildcache/\n"), 0o600); err != nil {
		t.Fatalf("write .gitignore failed: %v", err)
	}
	// Commit .gitignore on the worktree's branch so the only remaining
	// status entries are the ignored ones.
	runGit(t, wt.Path, "add", ".gitignore")
	runGit(t, wt.Path, "commit", "-m", "add gitignore")
	if err := os.WriteFile(filepath.Join(wt.Path, "debug.log"), []byte("log\n"), 0o600); err != nil {
		t.Fatalf("write failed: %v", err)
	}
	cacheDir := filepath.Join(wt.Path, "buildcache")
	if err := os.MkdirAll(cacheDir, 0o755); err != nil {
		t.Fatalf("mkdir failed: %v", err)
	}
	for _, name := range []string{"x.o", "y.o"} {
		if err := os.WriteFile(filepath.Join(cacheDir, name), []byte("o\n"), 0o600); err != nil {
			t.Fatalf("write failed: %v", err)
		}
	}

	ignored, err := m.Delete(wt.ID, false)
	if err != nil {
		t.Fatalf("clean Delete failed: %v", err)
	}
	if ignored != 3 {
		t.Fatalf("ignoredDestroyed = %d, want 3 (debug.log + buildcache/x.o + buildcache/y.o — the ignored dir must count file-by-file)", ignored)
	}
}
