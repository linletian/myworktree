package instance

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
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

// TestPumpLogsBroadcastAndBuffer spawns a process via Start and asserts the
// per-instance ring buffer + subscriber channel both receive PTY output.
// This is the integration coverage for the in-memory log refactor — it
// guarantees the post-pumpLogs data path (write to buffer, broadcast to
// subscribers) is wired up end-to-end on a real PTY runtime.
func TestPumpLogsBroadcastAndBuffer(t *testing.T) {
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
		Command:    "printf 'pumplogs-marker\\n' && sleep 0.2",
		Name:       "pumplogs",
	})
	if err != nil {
		t.Fatalf("Start failed: %v", err)
	}

	subCh, unsub, err := m.SubscribeOutput(inst.ID)
	if err != nil {
		t.Fatalf("SubscribeOutput failed: %v", err)
	}
	defer unsub()

	// Drain broadcast channel until we see the marker (or timeout).
	sawMarker := false
	deadline := time.After(5 * time.Second)
	for !sawMarker {
		select {
		case chunk := <-subCh:
			if strings.Contains(chunk, "pumplogs-marker") {
				sawMarker = true
			}
		case <-deadline:
			t.Fatalf("subscriber did not receive marker in time")
		}
	}

	// Buffer must also contain the marker — Tail reads from the in-memory
	// ring buffer, which is the only place logs live now.
	tail, err := m.Tail(inst.ID, 64*1024)
	if err != nil {
		t.Fatalf("Tail err: %v", err)
	}
	if !strings.Contains(tail, "pumplogs-marker") {
		t.Fatalf("buffer missing marker; tail = %q", tail)
	}

	// And ReadSince with a stale cursor must still return the marker.
	body, next, err := m.ReadSince(inst.ID, 0, 64*1024)
	if err != nil {
		t.Fatalf("ReadSince err: %v", err)
	}
	if !strings.Contains(body, "pumplogs-marker") {
		t.Fatalf("ReadSince missing marker; body = %q", body)
	}
	if next == 0 {
		t.Fatalf("ReadSince next = 0, want non-zero (cursor must advance)")
	}

	if err := m.Stop(inst.ID); err != nil {
		t.Fatalf("Stop failed: %v", err)
	}
	waitInstanceNotRunning(t, fs, inst.ID)
	waitRuntimeReleased(t, m, inst.ID)
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
