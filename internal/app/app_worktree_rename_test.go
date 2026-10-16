package app

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"myworktree/internal/store"
	"myworktree/internal/worktree"
)

// newRenameTestServer builds the minimal Server the PATCH /api/worktrees
// handler needs (issue #122). newIsolatedTestServer does not wire a
// worktreeMgr, so — like app_worktree_delete_test.go — the rename tests
// build their own; UpdateName is a pure store operation, so a temp state
// file is the whole dependency (git-level invariants are pinned by the
// worktree package's integration test, not here).
func newRenameTestServer(t *testing.T) (*Server, store.FileStore) {
	t.Helper()
	dataDir := t.TempDir()
	fs := store.FileStore{Path: filepath.Join(dataDir, "state.json")}
	if err := fs.Save(store.State{
		Worktrees: []store.ManagedWorktree{{ID: "wt1", Name: "old-name", Path: t.TempDir(), Branch: "wt-branch"}},
	}); err != nil {
		t.Fatalf("save state: %v", err)
	}
	return &Server{worktreeMgr: worktree.Manager{Store: fs}}, fs
}

// TestHandleWorktreeRenamePersistsAndReadsBack pins the issue #122 happy
// path: PATCH answers 200 with the updated worktree (name trimmed, git-facing
// fields untouched), the rename reaches state.json, and the GET list reads
// the new name back — the mechanism behind "the new name survives a page
// refresh / daemon restart".
func TestHandleWorktreeRenamePersistsAndReadsBack(t *testing.T) {
	srv, fs := newRenameTestServer(t)
	wtPath := func() string {
		st, err := fs.Load()
		if err != nil {
			t.Fatalf("load state: %v", err)
		}
		return st.Worktrees[0].Path
	}()

	req := httptest.NewRequest(http.MethodPatch, "/api/worktrees", bytes.NewReader([]byte(`{"id":"wt1","name":"  renamed-wt  "}`)))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	srv.handleWorktrees(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200: %s", w.Code, w.Body.String())
	}
	var resp store.ManagedWorktree
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("invalid JSON: %v (%s)", err, w.Body.String())
	}
	if resp.Name != "renamed-wt" {
		t.Fatalf("name = %q, want the trimmed %q", resp.Name, "renamed-wt")
	}
	if resp.ID != "wt1" || resp.Branch != "wt-branch" || resp.Path != wtPath {
		t.Fatalf("rename must return the same record with git-facing fields intact: %#v", resp)
	}

	// Persisted, not just echoed.
	st, err := fs.Load()
	if err != nil {
		t.Fatalf("load state: %v", err)
	}
	if len(st.Worktrees) != 1 || st.Worktrees[0].Name != "renamed-wt" || st.Worktrees[0].Branch != "wt-branch" {
		t.Fatalf("state.json did not persist the rename: %#v", st.Worktrees)
	}

	// GET /api/worktrees reads the new name back (List() mechanism).
	wGet := httptest.NewRecorder()
	srv.handleWorktrees(wGet, httptest.NewRequest(http.MethodGet, "/api/worktrees", nil))
	if wGet.Code != http.StatusOK {
		t.Fatalf("GET status = %d, want 200: %s", wGet.Code, wGet.Body.String())
	}
	var listResp struct {
		Worktrees []store.ManagedWorktree `json:"worktrees"`
	}
	if err := json.Unmarshal(wGet.Body.Bytes(), &listResp); err != nil {
		t.Fatalf("GET invalid JSON: %v (%s)", err, wGet.Body.String())
	}
	if len(listResp.Worktrees) != 1 || listResp.Worktrees[0].Name != "renamed-wt" {
		t.Fatalf("GET must list the renamed worktree, got %#v", listResp.Worktrees)
	}
}

// TestHandleWorktreeRenameRejections pins the issue #122 error contract,
// mirroring the handleInstances PATCH shape: blank name → 400 with the
// store untouched; blank id → 400 (validation trims it to "id is required"
// BEFORE the lookup — not 404); unknown id → 404; malformed JSON → 400;
// and a request with unknown fields → 400 (readJSON's DisallowUnknownFields
// — pinning it here keeps the strict-decoder contract from silently
// loosening later).
func TestHandleWorktreeRenameRejections(t *testing.T) {
	srv, fs := newRenameTestServer(t)

	patch := func(body string) *httptest.ResponseRecorder {
		t.Helper()
		w := httptest.NewRecorder()
		srv.handleWorktrees(w, httptest.NewRequest(http.MethodPatch, "/api/worktrees", strings.NewReader(body)))
		return w
	}

	// Empty (whitespace-only) name → 400, and nothing was written.
	if w := patch(`{"id":"wt1","name":"   "}`); w.Code != http.StatusBadRequest {
		t.Fatalf("empty name: status = %d, want 400: %s", w.Code, w.Body.String())
	}
	st, err := fs.Load()
	if err != nil {
		t.Fatalf("load state: %v", err)
	}
	if len(st.Worktrees) != 1 || st.Worktrees[0].Name != "old-name" {
		t.Fatalf("rejected rename must leave the store unchanged, got %#v", st.Worktrees)
	}

	// Blank id → 400 ("id is required"), NOT 404: validation trims and
	// rejects before the lookup, so the blank-id contract cannot silently
	// regress to the not-found path if the check is ever removed.
	if w := patch(`{"id":"   ","name":"x"}`); w.Code != http.StatusBadRequest {
		t.Fatalf("blank id: status = %d, want 400 (not 404 — validation precedes lookup): %s", w.Code, w.Body.String())
	}

	// Unknown id → 404 (ErrWorktreeNotFound mapping).
	if w := patch(`{"id":"does-not-exist","name":"x"}`); w.Code != http.StatusNotFound {
		t.Fatalf("unknown id: status = %d, want 404: %s", w.Code, w.Body.String())
	}

	// Malformed JSON → 400.
	if w := patch(`{`); w.Code != http.StatusBadRequest {
		t.Fatalf("malformed JSON: status = %d, want 400: %s", w.Code, w.Body.String())
	}

	// Unknown field → 400 (strict decoder).
	if w := patch(`{"id":"wt1","name":"x","extra":1}`); w.Code != http.StatusBadRequest {
		t.Fatalf("unknown field: status = %d, want 400: %s", w.Code, w.Body.String())
	}

	// Still nothing written by any rejection.
	st, err = fs.Load()
	if err != nil {
		t.Fatalf("load state: %v", err)
	}
	if len(st.Worktrees) != 1 || st.Worktrees[0].Name != "old-name" {
		t.Fatalf("rejections must not mutate the store, got %#v", st.Worktrees)
	}
}
