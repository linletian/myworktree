package store

import (
	"path/filepath"
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
