package instance

import (
	"os"
	"os/exec"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"myworktree/internal/store"
)

func TestStartStopIntegration(t *testing.T) {
	requireRuntimeDeps(t)

	dataDir := mustTempDir(t, "mw-data-")
	workDir := mustTempDir(t, "mw-work-")
	fs := store.FileStore{Path: filepath.Join(dataDir, "state.json")}
	if err := fs.Save(store.State{
		Worktrees: []store.ManagedWorktree{
			{ID: "wt1", Name: "wt1", Path: workDir},
		},
	}); err != nil {
		t.Fatalf("seed state failed: %v", err)
	}

	m := &Manager{DataDir: dataDir, Store: fs}
	inst, err := m.Start(StartInput{
		WorktreeID: "wt1",
		Command:    "echo hi",
		Name:       "integration",
	})
	if err != nil {
		t.Fatalf("Start failed: %v", err)
	}
	if inst.ID == "" || inst.PID <= 0 {
		t.Fatalf("unexpected instance after Start: %#v", inst)
	}

	if err := m.Stop(inst.ID); err != nil {
		t.Fatalf("Stop failed: %v", err)
	}
	waitInstanceNotRunning(t, fs, inst.ID)
	waitRuntimeReleased(t, m, inst.ID)
}

func TestStartMainRepoInstance(t *testing.T) {
	requireRuntimeDeps(t)

	dataDir := mustTempDir(t, "mw-data-")
	mainDir := mustTempDir(t, "mw-main-")
	fs := store.FileStore{Path: filepath.Join(dataDir, "state.json")}
	if err := fs.Save(store.State{}); err != nil {
		t.Fatalf("seed state failed: %v", err)
	}

	m := &Manager{DataDir: dataDir, Store: fs}
	inst, err := m.Start(StartInput{
		WorktreeID: MainWorktreeID,
		Root:       mainDir,
		Command:    "echo main",
		Name:       "main-shell",
	})
	if err != nil {
		t.Fatalf("Start with Root failed: %v", err)
	}
	if inst.ID == "" {
		t.Fatalf("expected non-empty instance ID")
	}
	if inst.WorktreeID != MainWorktreeID {
		t.Fatalf("WorktreeID = %q, want %q", inst.WorktreeID, MainWorktreeID)
	}
	if inst.WorktreeName != filepath.Base(mainDir) {
		t.Fatalf("WorktreeName = %q, want %q", inst.WorktreeName, filepath.Base(mainDir))
	}
	if inst.Cwd != mainDir {
		t.Fatalf("Cwd = %q, want %q", inst.Cwd, mainDir)
	}

	if err := m.Stop(inst.ID); err != nil {
		t.Fatalf("Stop failed: %v", err)
	}
	waitInstanceNotRunning(t, fs, inst.ID)
	waitRuntimeReleased(t, m, inst.ID)
}

func TestConcurrentMultiInstanceStartStop(t *testing.T) {
	requireRuntimeDeps(t)

	dataDir := mustTempDir(t, "mw-data-")
	workDir := mustTempDir(t, "mw-work-")
	fs := store.FileStore{Path: filepath.Join(dataDir, "state.json")}
	if err := fs.Save(store.State{
		Worktrees: []store.ManagedWorktree{
			{ID: "wt1", Name: "wt1", Path: workDir},
		},
	}); err != nil {
		t.Fatalf("seed state failed: %v", err)
	}

	m := &Manager{DataDir: dataDir, Store: fs}
	const n = 3
	ids := make([]string, n)
	errs := make(chan error, n)
	var wg sync.WaitGroup
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			inst, err := m.Start(StartInput{WorktreeID: "wt1", Command: "echo hello"})
			if err != nil {
				errs <- err
				return
			}
			ids[i] = inst.ID
		}(i)
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatalf("concurrent Start failed: %v", err)
		}
	}

	seen := map[string]struct{}{}
	for _, id := range ids {
		if id == "" {
			t.Fatalf("empty instance id in concurrent run: %#v", ids)
		}
		if _, ok := seen[id]; ok {
			t.Fatalf("duplicate instance id: %q", id)
		}
		seen[id] = struct{}{}
		if err := m.Stop(id); err != nil {
			t.Fatalf("Stop(%s) failed: %v", id, err)
		}
	}
	for _, id := range ids {
		waitInstanceNotRunning(t, fs, id)
		waitRuntimeReleased(t, m, id)
	}
}

func requireRuntimeDeps(t *testing.T) {
	t.Helper()
	if _, err := exec.LookPath("script"); err != nil {
		t.Skip("script command is required")
	}
	if _, err := exec.LookPath("zsh"); err != nil {
		t.Skip("zsh is required")
	}
}

func waitInstanceNotRunning(t *testing.T, fs store.FileStore, id string) {
	t.Helper()
	deadline := time.Now().Add(8 * time.Second)
	for time.Now().Before(deadline) {
		st, err := fs.Load()
		if err != nil {
			t.Fatalf("Load failed: %v", err)
		}
		for _, it := range st.Instances {
			if it.ID == id && it.Status != "running" {
				return
			}
		}
		time.Sleep(100 * time.Millisecond)
	}
	t.Fatalf("instance %s stayed in running state", id)
}

func waitRuntimeReleased(t *testing.T, m *Manager, id string) {
	t.Helper()
	deadline := time.Now().Add(8 * time.Second)
	for time.Now().Before(deadline) {
		m.mu.Lock()
		cmd := m.running[id]
		in := m.inputs[id]
		m.mu.Unlock()
		if cmd == nil && in == nil {
			return
		}
		time.Sleep(100 * time.Millisecond)
	}
	t.Fatalf("instance runtime resources not released: %s", id)
}

func mustTempDir(t *testing.T, pattern string) string {
	t.Helper()
	dir, err := os.MkdirTemp("", pattern)
	if err != nil {
		t.Fatalf("create temp dir failed: %v", err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(dir) })
	return dir
}
