package app

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"myworktree/internal/store"
	"myworktree/internal/worktree"
)

// setupDeleteTestWorktree builds a real git repo with one managed worktree
// registered in a state file — the worktree delete handler shells out to
// git, so only a real repo exercises the refusal path honestly.
func setupDeleteTestWorktree(t *testing.T) (srv *Server, fs store.FileStore, wtPath string) {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git is required")
	}
	repo := t.TempDir()
	runGitDeleteTest(t, repo, "init")
	runGitDeleteTest(t, repo, "config", "user.name", "Test User")
	runGitDeleteTest(t, repo, "config", "user.email", "test@example.com")
	if err := os.WriteFile(filepath.Join(repo, "README.md"), []byte("init\n"), 0o600); err != nil {
		t.Fatalf("write seed file: %v", err)
	}
	runGitDeleteTest(t, repo, "add", "README.md")
	runGitDeleteTest(t, repo, "commit", "-m", "init")

	wtPath = filepath.Join(t.TempDir(), "wt")
	runGitDeleteTest(t, repo, "worktree", "add", wtPath, "-b", "wt-branch")

	dataDir := t.TempDir()
	fs = store.FileStore{Path: filepath.Join(dataDir, "state.json")}
	if err := fs.Save(store.State{
		Worktrees: []store.ManagedWorktree{{ID: "wt1", Name: "wt1", Path: wtPath, Branch: "wt-branch"}},
	}); err != nil {
		t.Fatalf("save state: %v", err)
	}
	srv = &Server{worktreeMgr: worktree.Manager{GitRoot: repo, DataDir: dataDir, Store: fs}}
	return srv, fs, wtPath
}

func runGitDeleteTest(t *testing.T, dir string, args ...string) {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git %v failed: %v (%s)", args, err, out)
	}
}

// makeDeleteTestWorktreeDirty reproduces the issue #102 shape: a tracked
// file deleted, an untracked scratch file, and a gitignored file that the
// blocking check must not count but the refusal must warn about.
func makeDeleteTestWorktreeDirty(t *testing.T, wtPath string) {
	t.Helper()
	if err := os.Remove(filepath.Join(wtPath, "README.md")); err != nil {
		t.Fatalf("remove tracked file: %v", err)
	}
	if err := os.WriteFile(filepath.Join(wtPath, "scratch.txt"), []byte("x\n"), 0o600); err != nil {
		t.Fatalf("write scratch: %v", err)
	}
	if err := os.WriteFile(filepath.Join(wtPath, ".gitignore"), []byte("*.log\n"), 0o600); err != nil {
		t.Fatalf("write .gitignore: %v", err)
	}
	if err := os.WriteFile(filepath.Join(wtPath, "debug.log"), []byte("log\n"), 0o600); err != nil {
		t.Fatalf("write ignored file: %v", err)
	}
}

// TestHandleWorktreeDeleteDirtyRefusalIsStructured pins the issue #102 API
// contract: a dirty-worktree refusal answers 409 with error
// "worktree_dirty" and the full breakdown the dialog renders — counts by
// category, first paths, the full porcelain output, and the gitignored
// force-risk count. The flat "delete is refused" message made a damaged
// workspace indistinguishable from a leftover scratch file.
func TestHandleWorktreeDeleteDirtyRefusalIsStructured(t *testing.T) {
	srv, fs, wtPath := setupDeleteTestWorktree(t)
	makeDeleteTestWorktreeDirty(t, wtPath)

	req := httptest.NewRequest(http.MethodPost, "/api/worktrees/delete", strings.NewReader(`{"id":"wt1"}`))
	w := httptest.NewRecorder()
	srv.handleWorktreeDelete(w, req)

	if w.Code != http.StatusConflict {
		t.Fatalf("status = %d, want 409: %s", w.Code, w.Body.String())
	}
	var body map[string]any
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
		t.Fatalf("invalid JSON: %v (%s)", err, w.Body.String())
	}
	if body["error"] != "worktree_dirty" {
		t.Fatalf("error = %v, want worktree_dirty (the dashboard matches on this code)", body["error"])
	}
	if msg, _ := body["message"].(string); !strings.Contains(msg, "3 entries") || !strings.Contains(msg, "retry with force") {
		t.Fatalf("message = %q, want the one-line summary with counts and the force exit", msg)
	}
	dirty, ok := body["dirty"].(map[string]any)
	if !ok {
		t.Fatalf("dirty details missing: %s", w.Body.String())
	}
	// " D README.md" is the deletion; "?? .gitignore" + "?? scratch.txt"
	// are untracked; debug.log is ignored — a force risk, NOT a blocker.
	num := func(key string) float64 {
		v, _ := dirty[key].(float64)
		return v
	}
	if num("entries") != 3 || num("deleted") != 1 || num("untracked") != 2 || num("ignored") != 1 {
		t.Fatalf("dirty = entries:%v deleted:%v untracked:%v ignored:%v, want 3/1/2/1",
			num("entries"), num("deleted"), num("untracked"), num("ignored"))
	}
	if paths, _ := dirty["first_paths"].([]any); len(paths) == 0 {
		t.Fatalf("first_paths must name example paths: %v", dirty)
	}
	if porc, _ := dirty["porcelain"].(string); !strings.Contains(porc, "README.md") {
		t.Fatalf("porcelain must carry the full git status output: %q", porc)
	}

	// The refusal changed nothing: the state record survives.
	st, err := fs.Load()
	if err != nil {
		t.Fatalf("load state: %v", err)
	}
	if len(st.Worktrees) != 1 {
		t.Fatalf("refused delete must keep the state record, got %#v", st.Worktrees)
	}
}

// TestHandleWorktreeDeleteForceDeletes pins the force exit (issue #102 P2):
// resending with force:true removes the dirty worktree and drops its
// record — previously the only way out was a manual `git worktree remove
// --force` the daemon never noticed.
func TestHandleWorktreeDeleteForceDeletes(t *testing.T) {
	srv, fs, wtPath := setupDeleteTestWorktree(t)
	makeDeleteTestWorktreeDirty(t, wtPath)

	req := httptest.NewRequest(http.MethodPost, "/api/worktrees/delete", strings.NewReader(`{"id":"wt1","force":true}`))
	w := httptest.NewRecorder()
	srv.handleWorktreeDelete(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200: %s", w.Code, w.Body.String())
	}
	if _, err := os.Stat(wtPath); !os.IsNotExist(err) {
		t.Fatalf("force delete should remove the path, err=%v", err)
	}
	st, err := fs.Load()
	if err != nil {
		t.Fatalf("load state: %v", err)
	}
	if len(st.Worktrees) != 0 {
		t.Fatalf("force delete should drop the state record, got %#v", st.Worktrees)
	}
}
