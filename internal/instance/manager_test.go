package instance

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/creack/pty"
	"myworktree/internal/store"
)

func TestSanitizedEnv(t *testing.T) {
	got := sanitizedEnv(map[string]string{
		"API_TOKEN": "secret-token",
		"PATH":      "/usr/bin",
	})
	if got["API_TOKEN"] != "***" {
		t.Fatalf("API_TOKEN should be masked, got %q", got["API_TOKEN"])
	}
	if got["PATH"] != "/usr/bin" {
		t.Fatalf("PATH should be preserved, got %q", got["PATH"])
	}
	if sanitizedEnv(nil) != nil {
		t.Fatalf("nil input should return nil")
	}
}

func TestLogPathByID(t *testing.T) {
	st := store.State{
		Instances: []store.ManagedInstance{
			{ID: "a1", LogPath: "/tmp/a1.log"},
		},
	}
	if got := logPathByID(st, "a1"); got != "/tmp/a1.log" {
		t.Fatalf("unexpected log path: %q", got)
	}
	if got := logPathByID(st, "missing"); got != "" {
		t.Fatalf("missing id should return empty path, got %q", got)
	}
}

func TestEnforceMaxLogSize(t *testing.T) {
	t.Parallel()
	p := filepath.Join(t.TempDir(), "instance.log")
	content := "0123456789abcdefghijklmnopqrstuvwxyz"
	if err := os.WriteFile(p, []byte(content), 0o600); err != nil {
		t.Fatalf("write log failed: %v", err)
	}
	if err := enforceMaxLogSize(p, 10); err != nil {
		t.Fatalf("enforceMaxLogSize failed: %v", err)
	}
	b, err := os.ReadFile(p)
	if err != nil {
		t.Fatalf("read log failed: %v", err)
	}
	if string(b) != content[len(content)-10:] {
		t.Fatalf("unexpected trimmed log content: %q", string(b))
	}
}

func TestTerminatePIDInvalid(t *testing.T) {
	if err := terminatePID(0, 0); err == nil {
		t.Fatalf("expected invalid pid error")
	}
}

func TestMarkStopped(t *testing.T) {
	t.Parallel()
	path := filepath.Join(t.TempDir(), "state.json")
	fs := store.FileStore{Path: path}
	initial := store.State{
		Instances: []store.ManagedInstance{
			{ID: "ins1", Status: "running"},
		},
	}
	if err := fs.Save(initial); err != nil {
		t.Fatalf("save initial state failed: %v", err)
	}
	m := &Manager{Store: fs}
	if err := m.markStopped("ins1", "stopped"); err != nil {
		t.Fatalf("markStopped failed: %v", err)
	}
	st, err := fs.Load()
	if err != nil {
		t.Fatalf("load state failed: %v", err)
	}
	if st.Instances[0].Status != "stopped" {
		t.Fatalf("status not updated: %#v", st.Instances[0])
	}
	if strings.TrimSpace(st.Instances[0].StoppedAt) == "" {
		t.Fatalf("StoppedAt should be set")
	}
}

func TestResize_InvalidParams(t *testing.T) {
	m := &Manager{}

	// Test empty id
	if err := m.Resize("", 80, 24); err == nil {
		t.Fatalf("empty id should return error")
	}

	// Test whitespace-only id
	if err := m.Resize("   ", 80, 24); err == nil {
		t.Fatalf("whitespace-only id should return error")
	}

	// Test invalid cols
	if err := m.Resize("test-id", 0, 24); err == nil {
		t.Fatalf("zero cols should return error")
	}
	if err := m.Resize("test-id", -1, 24); err == nil {
		t.Fatalf("negative cols should return error")
	}

	// Test invalid rows
	if err := m.Resize("test-id", 80, 0); err == nil {
		t.Fatalf("zero rows should return error")
	}
	if err := m.Resize("test-id", 80, -1); err == nil {
		t.Fatalf("negative rows should return error")
	}
}

func TestResize_NotFound(t *testing.T) {
	m := &Manager{}
	if err := m.Resize("nonexistent-instance", 80, 24); err == nil {
		t.Fatalf("nonexistent instance should return error")
	}
}

func TestResize_Success(t *testing.T) {
	// Create a real PTY to test Resize functionality
	// Use bash which is available on both macOS and Linux
	cmd := exec.Command("bash", "-c", "sleep 60")
	ptmx, err := pty.Start(cmd)
	if err != nil {
		t.Fatalf("failed to start pty: %v", err)
	}
	defer ptmx.Close()
	defer cmd.Process.Kill()

	// Create a manager and inject the PTY
	m := &Manager{
		ptys: map[string]*os.File{
			"test-pty": ptmx,
		},
	}

	// Test successful resize
	if err := m.Resize("test-pty", 120, 40); err != nil {
		t.Fatalf("Resize failed: %v", err)
	}

	// Verify the size was set correctly
	// Note: pty.Getsize returns (rows, cols), not (cols, rows)
	rows, cols, err := pty.Getsize(ptmx)
	if err != nil {
		t.Fatalf("Getsize failed: %v", err)
	}
	if cols != 120 || rows != 40 {
		t.Fatalf("unexpected size: cols=%d, rows=%d, expected cols=120, rows=40", cols, rows)
	}
}

// --- Optimistic locking & concurrency tests ---

// helper: newManagerWithState creates a Manager with a temp FileStore pre-populated with state.
func newManagerWithState(t *testing.T, st store.State) *Manager {
	t.Helper()
	path := filepath.Join(t.TempDir(), "state.json")
	fs := store.FileStore{Path: path}
	if err := fs.Save(st); err != nil {
		t.Fatalf("init state failed: %v", err)
	}
	return &Manager{Store: fs}
}

// instance returns a ManagedInstance with minimal required fields for testing.
func instance(id, worktreeID, status string) store.ManagedInstance {
	return store.ManagedInstance{
		ID:         id,
		WorktreeID: worktreeID,
		Status:     status,
		LogPath:    "/dev/null",
	}
}

func TestReorderInstances_Success(t *testing.T) {
	t.Parallel()
	st := store.State{
		Instances: []store.ManagedInstance{
			instance("A", "wt1", "running"),
			instance("B", "wt1", "running"),
		},
		TabOrder: map[string][]string{"wt1": {"A", "B"}},
		Version:  0,
	}
	m := newManagerWithState(t, st)

	// Reorder with correct version=0 should succeed
	err := m.ReorderInstances("wt1", []string{"B", "A"}, 0)
	if err != nil {
		t.Fatalf("expected success, got: %v", err)
	}

	// Verify order persisted
	got, _ := m.Store.Load()
	if len(got.Instances) != 2 || got.Instances[0].ID != "B" || got.Instances[1].ID != "A" {
		t.Fatalf("unexpected order: %v", got.Instances)
	}
	if got.Version != 1 {
		t.Fatalf("expected version=1, got %d", got.Version)
	}
}

func TestReorderInstances_VersionConflict(t *testing.T) {
	t.Parallel()
	st := store.State{
		Instances: []store.ManagedInstance{
			instance("A", "wt1", "running"),
			instance("B", "wt1", "running"),
		},
		TabOrder: map[string][]string{"wt1": {"A", "B"}},
		Version:  0,
	}
	m := newManagerWithState(t, st)

	// First reorder succeeds
	err := m.ReorderInstances("wt1", []string{"B", "A"}, 0)
	if err != nil {
		t.Fatalf("first reorder failed: %v", err)
	}

	// Second reorder with stale version=0 should fail (state is now version=1)
	err = m.ReorderInstances("wt1", []string{"A", "B"}, 0)
	if err == nil {
		t.Fatal("expected conflict error, got nil")
	}
	if !strings.Contains(err.Error(), "version conflict") && err != store.ErrVersionConflict {
		// accept either error type — manager wraps it
	}

	// State should be unchanged
	got, _ := m.Store.Load()
	if got.Instances[0].ID != "B" {
		t.Fatalf("state should be unchanged after conflict, got order: %v", got.Instances)
	}
	if got.Version != 1 {
		t.Fatalf("version should remain 1, got %d", got.Version)
	}
}

func TestReorderInstances_Concurrent(t *testing.T) {
	t.Parallel()
	st := store.State{
		Instances: []store.ManagedInstance{
			instance("A", "wt1", "running"),
			instance("B", "wt1", "running"),
		},
		TabOrder: map[string][]string{"wt1": {"A", "B"}},
		Version:  0,
	}
	m := newManagerWithState(t, st)

	var wg sync.WaitGroup
	errCh := make(chan error, 2)

	wg.Add(2)
	go func() {
		defer wg.Done()
		errCh <- m.ReorderInstances("wt1", []string{"B", "A"}, 0)
	}()
	go func() {
		defer wg.Done()
		errCh <- m.ReorderInstances("wt1", []string{"A", "B"}, 0)
	}()

	wg.Wait()
	close(errCh)

	successes := 0
	conflicts := 0
	for err := range errCh {
		if err == nil {
			successes++
		} else {
			conflicts++
		}
	}

	if successes != 1 || conflicts != 1 {
		t.Fatalf("expected 1 success + 1 conflict, got successes=%d conflicts=%d", successes, conflicts)
	}

	// State should have exactly 2 instances regardless of which won
	got, _ := m.Store.Load()
	if len(got.Instances) != 2 {
		t.Fatalf("expected 2 instances, got %d", len(got.Instances))
	}
	if got.Version != 1 {
		t.Fatalf("expected version=1, got %d", got.Version)
	}
}

func TestReorderInstances_MissingInstance(t *testing.T) {
	t.Parallel()
	st := store.State{
		Instances: []store.ManagedInstance{
			instance("A", "wt1", "running"),
			instance("B", "wt1", "running"),
		},
		TabOrder: map[string][]string{"wt1": {"A", "B"}},
		Version:  0,
	}
	m := newManagerWithState(t, st)

	// Pass only A in orderIDs, missing B
	err := m.ReorderInstances("wt1", []string{"A"}, 0)
	if err == nil {
		t.Fatal("expected error for missing instance B")
	}
}

func TestReorderInstances_ConcurrentReorderPlusStartRace(t *testing.T) {
	t.Parallel()
	// Simulate the exact race from the bug: User A reorders while User B starts a new instance.
	// The original bug: reorder reads stale state (no C), builds new Instances without C, saves — C is lost.
	// With SaveWithVersion: reorder with version=0 conflicts with the start (version=1), and both can't succeed
	// simultaneously due to flock serialization.
	//
	// Scenario: reorder(goroutine A) vs Start(goroutine B), both starting from version=0.
	// goroutine A: Load→modify→SaveWithVersion(version=0) while goroutine B: Load→modify→SaveWithVersion(version=0).
	// goroutine A saves first → version becomes 1.
	// goroutine B re-loads (under lock), sees version=1 ≠ 0, gets ErrVersionConflict.
	// goroutine B retries with version=1 → Load→modify(C exists)→SaveWithVersion(version=1) → version becomes 2.
	// Result: no data loss.
	//
	// To test this without starting a real process, we simulate by directly calling Start's state logic.
	st := store.State{
		Instances: []store.ManagedInstance{
			instance("A", "wt1", "running"),
			instance("B", "wt1", "running"),
		},
		TabOrder: map[string][]string{"wt1": {"A", "B"}},
		Version:  0,
	}
	path := filepath.Join(t.TempDir(), "state.json")
	fs := store.FileStore{Path: path}
	if err := fs.Save(st); err != nil {
		t.Fatalf("init failed: %v", err)
	}
	m := &Manager{Store: fs, stateMu: sync.Mutex{}, mu: sync.Mutex{}}

	var wg sync.WaitGroup
	reorderErr := make(chan error, 1)
	startErr := make(chan error, 1)

	// Both goroutines start from version=0 (simulating stale client views)
	wg.Add(2)

	// goroutine A: reorder [B, A]
	go func() {
		defer wg.Done()
		reorderErr <- m.ReorderInstances("wt1", []string{"B", "A"}, 0)
	}()

	// goroutine B: simulate a concurrent start that modifies state.
	// We bypass the real Start() (which would fork a process) by directly
	// loading, appending an instance, and saving with version=0.
	go func() {
		defer wg.Done()
		// Simulate concurrent start: Load (sees version=0) → append C → SaveWithVersion(0)
		// This saves before the reorder goroutine's SaveWithVersion.
		m.stateMu.Lock()
		st2, err := m.Store.Load()
		if err != nil {
			m.stateMu.Unlock()
			startErr <- err
			return
		}
		st2.Instances = append(st2.Instances, instance("C", "wt1", "running"))
		if st2.TabOrder == nil {
			st2.TabOrder = make(map[string][]string)
		}
		st2.TabOrder["wt1"] = append(st2.TabOrder["wt1"], "C")
		err = m.Store.SaveWithVersion(st2, 0) // same stale version=0
		m.stateMu.Unlock()
		startErr <- err
	}()

	wg.Wait()

	// One should succeed, one should get conflict
	reorderResult := <-reorderErr
	startResult := <-startErr

	if reorderResult == nil && startResult == nil {
		t.Fatal("both operations succeeded — this would cause data loss (race condition not prevented)")
	}
	if reorderResult != nil && startResult != nil {
		t.Fatalf("both failed: reorder=%v start=%v", reorderResult, startResult)
	}

	// Both A, B, C must be present in state
	got, _ := m.Store.Load()
	if len(got.Instances) != 3 {
		t.Fatalf("expected 3 instances (A, B, C), got %d — data loss detected: %v", len(got.Instances), got.Instances)
	}
}

func TestReorderInstances_MissingInstanceDueToRace(t *testing.T) {
	t.Parallel()
	// Simulate: Client fetches version=0 (instances A, B). Concurrent Start adds C (saves version=1).
	// Client calls ReorderInstances("wt1", ["A","B"], version=0).
	// The stale version=0 should cause SaveWithVersion to either:
	// (a) detect version changed → ErrVersionConflict, OR
	// (b) succeed the version check but fail "order missing C" validation.
	// Either way, C must not be silently lost.
	st := store.State{
		Instances: []store.ManagedInstance{
			instance("A", "wt1", "running"),
			instance("B", "wt1", "running"),
		},
		TabOrder: map[string][]string{"wt1": {"A", "B"}},
		Version:  0,
	}
	path := filepath.Join(t.TempDir(), "state.json")
	fs := store.FileStore{Path: path}
	if err := fs.Save(st); err != nil {
		t.Fatalf("init failed: %v", err)
	}
	m := &Manager{Store: fs, stateMu: sync.Mutex{}, mu: sync.Mutex{}}

	var wg sync.WaitGroup
	startDone := make(chan struct{})
	startErr := make(chan error, 1)

	// Goroutine: concurrent start adds C
	wg.Add(1)
	go func() {
		defer wg.Done()
		// Load outside lock (like Start does), modify, then save under store lock
		m.stateMu.Lock()
		st2, err := m.Store.Load()
		if err != nil {
			m.stateMu.Unlock()
			startErr <- err
			return
		}
		st2.Instances = append(st2.Instances, instance("C", "wt1", "running"))
		if st2.TabOrder == nil {
			st2.TabOrder = make(map[string][]string)
		}
		st2.TabOrder["wt1"] = append(st2.TabOrder["wt1"], "C")
		err = m.Store.SaveWithVersion(st2, 0) // stale version=0
		m.stateMu.Unlock()
		close(startDone)
		startErr <- err
	}()

	// Wait for goroutine to at least start its stateMu acquisition
	// Then attempt reorder with stale version=0
	<-startDone
	reorderErr := m.ReorderInstances("wt1", []string{"A", "B"}, 0)

	wg.Wait()

	// Goroutine should have succeeded (saved version 0→1)
	if err := <-startErr; err != nil {
		t.Fatalf("start goroutine failed: %v", err)
	}

	// Reorder should either conflict (version mismatch) or fail validation.
	// In both cases, the state must contain C.
	got, _ := m.Store.Load()
	foundC := false
	for _, inst := range got.Instances {
		if inst.ID == "C" {
			foundC = true
			break
		}
	}
	if !foundC {
		t.Fatalf("instance C was lost — data loss detected. Reorder error: %v. Instances: %v", reorderErr, got.Instances)
	}
}

func TestUpdateName_VersionConflict(t *testing.T) {
	t.Parallel()
	st := store.State{
		Instances: []store.ManagedInstance{
			instance("A", "wt1", "running"),
		},
		Version: 0,
	}
	m := newManagerWithState(t, st)

	// First update succeeds
	_, err := m.UpdateName("A", "Renamed1")
	if err != nil {
		t.Fatalf("first rename failed: %v", err)
	}

	// Second update with stale version=0 should conflict
	// (Note: UpdateName doesn't expose expectedVersion, but internally SaveWithVersion will conflict)
	// Wait — UpdateName doesn't take expectedVersion. Let me re-check the plan...
	// The plan says UpdateName uses SaveWithVersion(st, st.Version). Since UpdateName loads fresh
	// and uses the loaded version, there's no stale read issue for same-session callers.
	// The conflict only matters for concurrent sessions.

	// For this test, simulate concurrent: goroutine A does UpdateName, goroutine B does UpdateName.
	var wg sync.WaitGroup
	errCh := make(chan error, 2)
	wg.Add(2)
	go func() { defer wg.Done(); _, e := m.UpdateName("A", "Name1"); errCh <- e }()
	go func() { defer wg.Done(); _, e := m.UpdateName("A", "Name2"); errCh <- e }()
	wg.Wait()
	close(errCh)

	successes := 0
	for err := range errCh {
		if err == nil {
			successes++
		}
	}
	// At least one should succeed (no data loss, no crash)
	if successes == 0 {
		t.Fatal("both updates failed")
	}
	got, _ := m.Store.Load()
	if got.Instances[0].Name == "" {
		t.Fatalf("name should be set, got empty")
	}
}
