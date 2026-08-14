package framework

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"myworktree/internal/store"
)

// fakeKind is a controllable Kind for integration testing.
type fakeKind struct {
	manifest   KindInfo
	failSpawn  error
	blockReady bool
	spawned    chan struct{}
	stopped    chan struct{}
	lastGrace  atomic.Int64
	mu         sync.Mutex
	status     Status
	statusErr  string
}

func fakeKindPTY() *fakeKind {
	return &fakeKind{
		manifest: KindInfo{Name: "fake-pty", Label: "Fake PTY", Interactive: true},
		spawned:  make(chan struct{}, 2),
		stopped:  make(chan struct{}, 2),
	}
}

func fakeKindBlocking() *fakeKind {
	return &fakeKind{
		manifest:   KindInfo{Name: "fake-block", Label: "Blocking"},
		blockReady: true,
		spawned:    make(chan struct{}, 2),
		stopped:    make(chan struct{}, 2),
	}
}

func (f *fakeKind) Manifest() KindInfo { return f.manifest }

func (f *fakeKind) Spawn(ctx context.Context, p SpawnParams) (Handle, *ReadySignal, error) {
	f.spawned <- struct{}{}
	if f.failSpawn != nil {
		return Handle{}, nil, f.failSpawn
	}
	ready := NewReadySignal()
	if !f.blockReady {
		ready.Close()
	}
	return NewHandle(f.manifest.Name, "inner"), ready, nil
}

func (f *fakeKind) Stop(h Handle, grace int) error {
	f.lastGrace.Store(int64(grace))
	f.stopped <- struct{}{}
	return nil
}

func (f *fakeKind) Status(h Handle) (Status, string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.status, f.statusErr
}

func (f *fakeKind) ReadLogs(h Handle, since, max int64) (string, int64, error) {
	return "fake-log\n", since + 9, nil
}

func (f *fakeKind) KindBlob(h Handle) (json.RawMessage, error)           { return json.RawMessage(`{}`), nil }
func (f *fakeKind) HTTPHint(id string) string                            { return "" }
func (f *fakeKind) RegisterHTTP(mux *http.ServeMux, id string, h Handle) {}

func newTestManager(t *testing.T, reg *Registry, worktreePath string) (*Manager, store.FileStore) {
	t.Helper()
	dir := t.TempDir()
	storePath := filepath.Join(dir, "state.json")
	fs := store.FileStore{Path: storePath}
	if err := fs.Save(store.State{
		Worktrees: []store.ManagedWorktree{{ID: "wt1", Name: "wt1", Path: worktreePath}},
	}); err != nil {
		t.Fatalf("seed state: %v", err)
	}
	mgr := NewManager(reg, fs, nil)
	mgr.DataDir = dir
	mgr.Root = worktreePath
	return mgr, fs
}

// --- tests ---

func TestStartAndStatusTransition(t *testing.T) {
	t.Parallel()
	k := fakeKindPTY()
	reg := NewRegistry()
	reg.Register(k)
	mgr, _ := newTestManager(t, reg, t.TempDir())

	inst, err := mgr.Start(context.Background(), StartParams{WorktreeID: "wt1", Kind: "fake-pty", Name: "t1"})
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	if inst.Kind != "fake-pty" || inst.Status != StatusStarting.String() {
		t.Fatalf("kind=%s status=%s want fake-pty starting", inst.Kind, inst.Status)
	}

	select {
	case <-k.spawned:
	case <-time.After(2 * time.Second):
		t.Fatal("Spawn not called")
	}

	// blockReady=false → ReadySignal closed during Spawn.
	// Wait for runLifecycle polling to transition starting→running.
	waitFor := time.Now().Add(5 * time.Second)
	for time.Now().Before(waitFor) {
		got, err := mgr.Get(inst.ID)
		if err != nil {
			t.Fatalf("Get: %v", err)
		}
		if got.Status == StatusRunning.String() {
			return
		}
		time.Sleep(200 * time.Millisecond)
	}
	t.Fatal("status did not reach running within 5s")
}

func TestSpawnErrorMarksFailed(t *testing.T) {
	t.Parallel()
	k := &fakeKind{
		manifest:  KindInfo{Name: "flaky"},
		failSpawn: errors.New("spawn explosion"),
		spawned:   make(chan struct{}, 1),
		stopped:   make(chan struct{}, 1),
	}
	reg := NewRegistry()
	reg.Register(k)
	mgr, _ := newTestManager(t, reg, t.TempDir())

	_, err := mgr.Start(context.Background(), StartParams{WorktreeID: "wt1", Kind: "flaky"})
	if err == nil || !strings.Contains(err.Error(), "spawn explosion") {
		t.Fatalf("expected spawn error, got: %v", err)
	}
}

func TestUnknownKind(t *testing.T) {
	t.Parallel()
	reg := NewRegistry()
	mgr, _ := newTestManager(t, reg, t.TempDir())
	_, err := mgr.Start(context.Background(), StartParams{WorktreeID: "wt1", Kind: "ghost"})
	if err == nil || !(strings.Contains(err.Error(), "unknown instance kind") || strings.Contains(err.Error(), "not registered")) {
		t.Fatalf("expected unknown kind error, got: %v", err)
	}
}

func TestStopCallsKindStop(t *testing.T) {
	t.Parallel()
	k := fakeKindPTY()
	reg := NewRegistry()
	reg.Register(k)
	mgr, _ := newTestManager(t, reg, t.TempDir())

	inst, _ := mgr.Start(context.Background(), StartParams{WorktreeID: "wt1", Kind: "fake-pty"})
	// Wait for running
	for {
		got, _ := mgr.Get(inst.ID)
		if got.Status == StatusRunning.String() {
			break
		}
		time.Sleep(200 * time.Millisecond)
	}

	if err := mgr.Stop(inst.ID); err != nil {
		t.Fatalf("Stop: %v", err)
	}
	select {
	case <-k.stopped:
	case <-time.After(time.Second):
		t.Fatal("kind.Stop not called")
	}
}

func TestRestartCreatesNewID(t *testing.T) {
	t.Parallel()
	k := fakeKindPTY()
	reg := NewRegistry()
	reg.Register(k)
	mgr, _ := newTestManager(t, reg, t.TempDir())

	inst1, _ := mgr.Start(context.Background(), StartParams{WorktreeID: "wt1", Kind: "fake-pty"})

	// Wait running then stop
	for {
		got, _ := mgr.Get(inst1.ID)
		if got.Status == StatusRunning.String() {
			break
		}
		time.Sleep(200 * time.Millisecond)
	}
	_ = mgr.Stop(inst1.ID)
	// Wait for Stop to flush to the store before Restart reads it.
	for i := 0; i < 50; i++ {
		got, _ := mgr.Get(inst1.ID)
		if got.Status == StatusStopped.String() || got.Status == StatusExited.String() {
			break
		}
		time.Sleep(100 * time.Millisecond)
	}

	inst2, err := mgr.Restart(inst1.ID)
	if err != nil {
		t.Fatalf("Restart: %v", err)
	}
	if inst2.ID == inst1.ID {
		t.Fatalf("Restart returned same id %q", inst2.ID)
	}
	// RestartedFrom is set in the store during Restart's
	// post-Start cleanup; the returned value came from Start()
	// which does not include it. Read back from the store.
	loaded, err := mgr.Get(inst2.ID)
	if err != nil {
		t.Fatalf("Get(new): %v", err)
	}
	if loaded.RestartedFrom != inst1.ID {
		t.Fatalf("Get(new).RestartedFrom = %q, want %q", loaded.RestartedFrom, inst1.ID)
	}
}

func TestReconcileRunning(t *testing.T) {
	t.Parallel()
	k := fakeKindPTY()
	reg := NewRegistry()
	reg.Register(k)
	mgr, fs := newTestManager(t, reg, t.TempDir())

	// Build one state with all instances so the fileStore's Load
	// sees them all in a single batch. SaveWithVersion overwrites
	// the store on each call; we want one write containing all
	// entries.
	statuses := []string{"running", "starting", "exited", "stopped", "running"}
	var instances []store.ManagedInstance
	for i, st := range statuses {
		id := "inst-" + string(rune('a'+i))
		instances = append(instances, store.ManagedInstance{
			ID: id, WorktreeID: "wt1", Name: id, Kind: "fake-pty", Status: st,
		})
	}
	if err := fs.Save(store.State{
		Worktrees: []store.ManagedWorktree{{ID: "wt1", Name: "wt1", Path: "/tmp/wt"}},
		Instances: instances,
		Version:   0,
	}); err != nil {
		t.Fatalf("seed: %v", err)
	}

	n, err := mgr.ReconcileRunningOnStartup()
	if err != nil {
		t.Fatalf("Reconcile: %v", err)
	}
	if n != 3 {
		t.Fatalf("changed %d, want 3 (running + starting + running)", n)
	}
}
