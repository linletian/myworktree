package store

import (
	"os"
	"path/filepath"
	"sync"
	"testing"
)

func TestFileStorePathRequired(t *testing.T) {
	fs := FileStore{}
	if _, err := fs.Load(); err == nil {
		t.Fatalf("Load should fail when path is empty")
	}
	if err := fs.Save(State{}); err == nil {
		t.Fatalf("Save should fail when path is empty")
	}
}

func TestFileStoreLoadSaveRoundTrip(t *testing.T) {
	t.Parallel()
	path := filepath.Join(t.TempDir(), "state.json")
	fs := FileStore{Path: path}

	src := State{
		Worktrees: []ManagedWorktree{
			{ID: "wt1", Name: "feature-auth", Path: "/tmp/wt", Branch: "feature/auth"},
		},
		Instances: []ManagedInstance{
			{ID: "ins1", WorktreeID: "wt1", Status: "running", LogPath: "/tmp/ins1.log"},
		},
	}
	if err := fs.Save(src); err != nil {
		t.Fatalf("Save failed: %v", err)
	}

	got, err := fs.Load()
	if err != nil {
		t.Fatalf("Load failed: %v", err)
	}
	if len(got.Worktrees) != 1 || got.Worktrees[0].ID != "wt1" {
		t.Fatalf("unexpected worktrees after round trip: %#v", got.Worktrees)
	}
	if len(got.Instances) != 1 || got.Instances[0].ID != "ins1" {
		t.Fatalf("unexpected instances after round trip: %#v", got.Instances)
	}
}

func TestFileStoreLoadFromEmptyFile(t *testing.T) {
	t.Parallel()
	path := filepath.Join(t.TempDir(), "state.json")
	fs := FileStore{Path: path}

	got, err := fs.Load()
	if err != nil {
		t.Fatalf("Load should succeed for empty/non-existing state file: %v", err)
	}
	if len(got.Worktrees) != 0 || len(got.Instances) != 0 {
		t.Fatalf("new state should be empty, got %#v", got)
	}
}

func TestSaveWithVersionFirstSaveOnEmptyFile(t *testing.T) {
	t.Parallel()
	path := filepath.Join(t.TempDir(), "state.json")
	fs := FileStore{Path: path}

	// Empty file: Load returns Version=0
	st, err := fs.Load()
	if err != nil {
		t.Fatalf("Load failed: %v", err)
	}
	if st.Version != 0 {
		t.Fatalf("empty state Version should be 0, got %d", st.Version)
	}

	// First save with version=0 should succeed (file doesn't exist yet)
	st.Instances = []ManagedInstance{{ID: "ins1"}}
	err = fs.SaveWithVersion(st, 0)
	if err != nil {
		t.Fatalf("SaveWithVersion(0) on empty file should succeed: %v", err)
	}

	// Verify version was incremented
	got, err := fs.Load()
	if err != nil {
		t.Fatalf("Load failed: %v", err)
	}
	if got.Version != 1 {
		t.Fatalf("expected Version=1 after first save, got %d", got.Version)
	}
	if len(got.Instances) != 1 || got.Instances[0].ID != "ins1" {
		t.Fatalf("instances lost: got %v", got.Instances)
	}
}

func TestSaveWithVersionConflictDetected(t *testing.T) {
	t.Parallel()
	path := filepath.Join(t.TempDir(), "state.json")
	fs := FileStore{Path: path}

	// Init: save first version
	st := State{Instances: []ManagedInstance{{ID: "ins1"}}}
	if err := fs.SaveWithVersion(st, 0); err != nil {
		t.Fatalf("first save failed: %v", err)
	}

	// Simulate stale caller: tries to save with version=0 but file is now version=1
	st2 := State{Instances: []ManagedInstance{{ID: "ins2"}}}
	err := fs.SaveWithVersion(st2, 0)
	if err == nil {
		t.Fatal("expected version conflict error, got nil")
	}
	if err != ErrVersionConflict {
		t.Fatalf("expected ErrVersionConflict, got: %v", err)
	}

	// Verify first instance was NOT overwritten
	got, err := fs.Load()
	if err != nil {
		t.Fatalf("Load failed: %v", err)
	}
	if len(got.Instances) != 1 || got.Instances[0].ID != "ins1" {
		t.Fatalf("stale save should not overwrite; got instances: %v", got.Instances)
	}
	if got.Version != 1 {
		t.Fatalf("version should remain 1, got %d", got.Version)
	}
}

func TestSaveWithVersionSuccessiveSaves(t *testing.T) {
	t.Parallel()
	path := filepath.Join(t.TempDir(), "state.json")
	fs := FileStore{Path: path}

	version := int64(0)
	for i := 0; i < 3; i++ {
		st := State{Version: version, Instances: []ManagedInstance{{ID: "ins"}}}
		err := fs.SaveWithVersion(st, version)
		if err != nil {
			t.Fatalf("save %d with version %d failed: %v", i, version, err)
		}
		loaded, _ := fs.Load()
		version = loaded.Version
		if version != int64(i+1) {
			t.Fatalf("after save %d: expected version %d, got %d", i, i+1, version)
		}
	}
}

func TestSaveWithVersionSkipCheck(t *testing.T) {
	t.Parallel()
	path := filepath.Join(t.TempDir(), "state.json")
	fs := FileStore{Path: path}

	// Save with version=-1 should skip check (for backward compat / startup reconcile)
	st := State{Instances: []ManagedInstance{{ID: "ins1"}}}
	err := fs.SaveWithVersion(st, -1)
	if err != nil {
		t.Fatalf("SaveWithVersion(-1) should skip check: %v", err)
	}

	// Now try stale save with version=0 but file is version=1
	st2 := State{Instances: []ManagedInstance{{ID: "ins2"}}}
	err = fs.SaveWithVersion(st2, 0)
	if err != ErrVersionConflict {
		t.Fatalf("expected conflict on stale save after skip, got: %v", err)
	}
}

func TestSaveWithVersionConcurrent(t *testing.T) {
	t.Parallel()
	path := filepath.Join(t.TempDir(), "state.json")
	fs := FileStore{Path: path}

	// Init: first save to establish version=1
	if err := fs.SaveWithVersion(State{}, 0); err != nil {
		t.Fatalf("init save failed: %v", err)
	}

	const nGoroutines = 10
	var wg sync.WaitGroup
	errCh := make(chan error, nGoroutines)

	// All goroutines try to save concurrently with stale version=1
	for i := 0; i < nGoroutines; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			st := State{Instances: []ManagedInstance{{ID: "concurrent"}}}
			err := fs.SaveWithVersion(st, 1)
			errCh <- err
		}()
	}

	wg.Wait()
	close(errCh)

	conflicts := 0
	for err := range errCh {
		if err == ErrVersionConflict {
			conflicts++
		} else if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
	}

	// Exactly one should succeed (version goes 1→2), rest should conflict
	if conflicts != nGoroutines-1 {
		t.Fatalf("expected %d conflicts, got %d", nGoroutines-1, conflicts)
	}

	// Only one instance should be persisted
	got, _ := fs.Load()
	if len(got.Instances) != 1 {
		t.Fatalf("expected 1 instance, got %d", len(got.Instances))
	}
	if got.Version != 2 {
		t.Fatalf("expected version=2, got %d", got.Version)
	}
}

// TestSaveWithVersionLegacyFile tests that an existing state.json without a
// version field (written by a pre-upgrade binary) is handled correctly.
// json.Unmarshal leaves Version=0 for missing fields, so it's equivalent to a
// fresh file with version=0.
func TestSaveWithVersionLegacyFile(t *testing.T) {
	t.Parallel()
	path := filepath.Join(t.TempDir(), "state.json")
	fs := FileStore{Path: path}

	// Write a legacy state.json with no version field (pre-upgrade format)
	legacyJSON := `{"worktrees":[],"instances":[{"id":"legacy1","worktree_id":"wt1","status":"running"}],"tab_order":{}}`
	if err := os.WriteFile(path, []byte(legacyJSON), 0o600); err != nil {
		t.Fatalf("failed to write legacy file: %v", err)
	}

	// Load: Version should default to 0 (missing JSON field)
	st, err := fs.Load()
	if err != nil {
		t.Fatalf("Load failed: %v", err)
	}
	if st.Version != 0 {
		t.Fatalf("legacy file Version should be 0 (missing field), got %d", st.Version)
	}

	// SaveWithVersion(0) should succeed: current=0, expected=0
	st.Instances = append(st.Instances, ManagedInstance{ID: "legacy2"})
	err = fs.SaveWithVersion(st, 0)
	if err != nil {
		t.Fatalf("SaveWithVersion(0) on legacy file should succeed: %v", err)
	}

	// Verify version is now 1 and both instances present
	got, _ := fs.Load()
	if got.Version != 1 {
		t.Fatalf("expected Version=1, got %d", got.Version)
	}
	if len(got.Instances) != 2 {
		t.Fatalf("expected 2 instances, got %d: %v", len(got.Instances), got.Instances)
	}

	// Next save with stale version=0 should conflict
	err = fs.SaveWithVersion(State{Instances: []ManagedInstance{{ID: "stale"}}}, 0)
	if err != ErrVersionConflict {
		t.Fatalf("expected conflict after legacy migration, got: %v", err)
	}
}
