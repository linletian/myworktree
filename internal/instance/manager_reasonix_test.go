package instance

import (
	"os"
	"path/filepath"
	"syscall"
	"testing"
	"time"

	"myworktree/internal/instance/reasonix"
	"myworktree/internal/store"
)

// fakeServeBinT writes a python3 script that emulates `reasonix serve`: it
// binds 127.0.0.1:0, writes the real address to --port-file and its pid to
// --pid-file, then serves HTTP until terminated (Health probes the TCP port).
func fakeServeBinT(t *testing.T) string {
	t.Helper()
	bin := filepath.Join(t.TempDir(), "fake-serve.py")
	script := `#!/usr/bin/env python3
import os, sys
from http.server import BaseHTTPRequestHandler, HTTPServer
args = sys.argv[1:]
def val(flag):
    try:
        i = args.index(flag)
        return args[i + 1]
    except (ValueError, IndexError):
        return None
pidfile = val("--pid-file")
portfile = val("--port-file")
class H(BaseHTTPRequestHandler):
    def do_GET(self):
        body = b"ok"
        self.send_response(200)
        self.send_header("Content-Length", str(len(body)))
        self.end_headers()
        self.wfile.write(body)
    def log_message(self, *a):
        pass
srv = HTTPServer(("127.0.0.1", 0), H)
with open(portfile, "w") as f:
    f.write("127.0.0.1:%d" % srv.server_address[1])
with open(pidfile, "w") as f:
    f.write(str(os.getpid()))
srv.serve_forever()
`
	if err := os.WriteFile(bin, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	return bin
}

func newReasonixTestManager(t *testing.T) (*Manager, store.FileStore) {
	t.Helper()
	dataDir := mustTempDir(t, "mw-rx-data-")
	workDir := mustTempDir(t, "mw-rx-work-")
	fs := store.FileStore{Path: filepath.Join(dataDir, "state.json")}
	if err := fs.Save(store.State{
		Worktrees: []store.ManagedWorktree{
			{ID: "wt1", Name: "wt1", Path: workDir},
		},
	}); err != nil {
		t.Fatalf("seed state failed: %v", err)
	}
	m := &Manager{
		DataDir: dataDir,
		Store:   fs,
		Reasonix: &reasonix.Driver{
			DataDir:     dataDir,
			ReasonixBin: fakeServeBinT(t),
		},
	}
	return m, fs
}

func TestStartStopReasonixInstance(t *testing.T) {
	m, fs := newReasonixTestManager(t)

	inst, err := m.Start(StartInput{WorktreeID: "wt1", Kind: store.KindReasonix, Name: "chat"})
	if err != nil {
		t.Fatalf("Start reasonix failed: %v", err)
	}
	if inst.Kind != store.KindReasonix {
		t.Fatalf("Kind = %q, want %q", inst.Kind, store.KindReasonix)
	}
	if inst.PID <= 0 {
		t.Fatalf("PID = %d, want > 0", inst.PID)
	}
	if inst.Status != "running" {
		t.Fatalf("Status = %q, want running", inst.Status)
	}

	if err := m.Stop(inst.ID); err != nil {
		t.Fatalf("Stop failed: %v", err)
	}
	waitInstanceNotRunning(t, fs, inst.ID)

	// Stopping again is a no-op.
	if err := m.Stop(inst.ID); err != nil {
		t.Fatalf("second Stop failed: %v", err)
	}
}

func TestStartReasonixDisabled(t *testing.T) {
	dataDir := mustTempDir(t, "mw-rx-dis-")
	workDir := mustTempDir(t, "mw-rx-work-")
	fs := store.FileStore{Path: filepath.Join(dataDir, "state.json")}
	if err := fs.Save(store.State{
		Worktrees: []store.ManagedWorktree{
			{ID: "wt1", Name: "wt1", Path: workDir},
		},
	}); err != nil {
		t.Fatalf("seed state failed: %v", err)
	}
	// No Reasonix driver configured → reasonix instances must be rejected.
	m := &Manager{DataDir: dataDir, Store: fs}
	if _, err := m.Start(StartInput{WorktreeID: "wt1", Kind: store.KindReasonix}); err == nil {
		t.Fatal("Start with nil Reasonix driver should error")
	}
}

func TestRestartReasonixKeepsKind(t *testing.T) {
	m, fs := newReasonixTestManager(t)

	inst, err := m.Start(StartInput{WorktreeID: "wt1", Kind: store.KindReasonix, Name: "chat"})
	if err != nil {
		t.Fatalf("Start failed: %v", err)
	}
	if err := m.Stop(inst.ID); err != nil {
		t.Fatalf("Stop failed: %v", err)
	}
	waitInstanceNotRunning(t, fs, inst.ID)

	newInst, err := m.Restart(inst.ID)
	if err != nil {
		t.Fatalf("Restart failed: %v", err)
	}
	if newInst.Kind != store.KindReasonix {
		t.Fatalf("restarted Kind = %q, want %q", newInst.Kind, store.KindReasonix)
	}
	if newInst.ID == inst.ID {
		t.Fatalf("restart should allocate a new instance id, got same %q", newInst.ID)
	}
	if err := m.Stop(newInst.ID); err != nil {
		t.Fatalf("Stop restarted failed: %v", err)
	}
}

func TestDeleteReasonixInstance(t *testing.T) {
	m, fs := newReasonixTestManager(t)

	inst, err := m.Start(StartInput{WorktreeID: "wt1", Kind: store.KindReasonix, Name: "chat"})
	if err != nil {
		t.Fatalf("Start failed: %v", err)
	}
	// Delete requires a stopped instance (same as tty semantics).
	if err := m.Delete(inst.ID); err == nil {
		t.Fatal("Delete of a running instance should error")
	}
	if err := m.Stop(inst.ID); err != nil {
		t.Fatalf("Stop failed: %v", err)
	}
	waitInstanceNotRunning(t, fs, inst.ID)
	if err := m.Delete(inst.ID); err != nil {
		t.Fatalf("Delete failed: %v", err)
	}

	st, err := fs.Load()
	if err != nil {
		t.Fatalf("Load after Delete: %v", err)
	}
	for _, it := range st.Instances {
		if it.ID == inst.ID {
			t.Fatalf("instance %q still present after Delete", inst.ID)
		}
	}
}

// TestDeleteReasonixStaleStoppedStopsProcess covers the N-01 window: the
// serve process is still alive but the record was marked "stopped" (e.g.
// Health briefly failed during reconcile). Delete must still kill the
// process before wiping the state dir.
//
// The assertion polls the captured pid directly: Health() reads the pid
// file, which Cleanup removes, so Health after Delete is always "not ok"
// regardless of whether the process survived — polling it would make this
// test vacuously pass (T-01).
func TestDeleteReasonixStaleStoppedStopsProcess(t *testing.T) {
	m, fs := newReasonixTestManager(t)

	inst, err := m.Start(StartInput{WorktreeID: "wt1", Kind: store.KindReasonix, Name: "chat"})
	if err != nil {
		t.Fatalf("Start failed: %v", err)
	}
	// Capture the live pid before Delete wipes the state dir.
	info, ok, herr := m.Reasonix.Health(inst.ID)
	if herr != nil || !ok || info.PID <= 0 {
		t.Fatalf("no live serve pid before Delete: ok=%v err=%v", ok, herr)
	}
	pid := info.PID

	// Force the state to "stopped" while the process is still alive, without
	// touching the manager's runtime view.
	st, err := fs.Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	for i := range st.Instances {
		if st.Instances[i].ID == inst.ID {
			st.Instances[i].Status = "stopped"
		}
	}
	if err := fs.Save(st); err != nil {
		t.Fatalf("Save: %v", err)
	}

	if err := m.Delete(inst.ID); err != nil {
		t.Fatalf("Delete failed: %v", err)
	}
	// The lingering serve process must be gone. kill(pid, 0) is the probe:
	// zombies cannot linger here because the driver's cmd.Wait goroutine
	// reaps the process it started.
	deadline := time.Now().Add(5 * time.Second)
	for {
		if err := syscall.Kill(pid, 0); err != nil {
			break // process gone (ESRCH)
		}
		if time.Now().After(deadline) {
			t.Fatalf("serve process %d still alive after Delete of stale-stopped instance", pid)
		}
		time.Sleep(50 * time.Millisecond)
	}
}

func TestReconcileKeepsLiveReasonixRunning(t *testing.T) {
	m, fs := newReasonixTestManager(t)

	inst, err := m.Start(StartInput{WorktreeID: "wt1", Kind: store.KindReasonix, Name: "chat"})
	if err != nil {
		t.Fatalf("Start failed: %v", err)
	}

	// The serve subprocess survives a server restart; Reconcile must re-attach
	// it (keep "running") instead of marking it stopped and orphaning it.
	changed, err := m.ReconcileRunningOnStartup()
	if err != nil {
		t.Fatalf("Reconcile: %v", err)
	}
	if changed != 0 {
		t.Fatalf("Reconcile changed %d instances, want 0 (live reasonix kept running)", changed)
	}
	st, err := fs.Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	found := false
	for _, it := range st.Instances {
		if it.ID == inst.ID {
			found = true
			if it.Status != "running" {
				t.Fatalf("live reasonix instance status = %q, want running", it.Status)
			}
		}
	}
	if !found {
		t.Fatalf("instance %q missing after Reconcile", inst.ID)
	}

	if err := m.Stop(inst.ID); err != nil {
		t.Fatalf("cleanup Stop failed: %v", err)
	}
}

func TestReconcileStopsDeadReasonix(t *testing.T) {
	m, fs := newReasonixTestManager(t)

	inst, err := m.Start(StartInput{WorktreeID: "wt1", Kind: store.KindReasonix, Name: "chat"})
	if err != nil {
		t.Fatalf("Start failed: %v", err)
	}
	// Kill the serve process out-of-band; state still says "running".
	if err := syscall.Kill(inst.PID, syscall.SIGKILL); err != nil {
		t.Fatalf("kill serve: %v", err)
	}
	deadline := time.Now().Add(5 * time.Second)
	for {
		if _, ok, _ := m.Reasonix.Health(inst.ID); !ok {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("serve process still healthy after SIGKILL")
		}
		time.Sleep(50 * time.Millisecond)
	}

	changed, err := m.ReconcileRunningOnStartup()
	if err != nil {
		t.Fatalf("Reconcile: %v", err)
	}
	if changed != 1 {
		t.Fatalf("Reconcile changed %d instances, want 1 (dead reasonix marked stopped)", changed)
	}
	st, err := fs.Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	for _, it := range st.Instances {
		if it.ID == inst.ID {
			if it.Status != "stopped" {
				t.Fatalf("dead reasonix instance status = %q, want stopped", it.Status)
			}
		}
	}
}

func TestRestartReasonixCleansOldDir(t *testing.T) {
	m, _ := newReasonixTestManager(t)

	inst, err := m.Start(StartInput{WorktreeID: "wt1", Kind: store.KindReasonix, Name: "chat"})
	if err != nil {
		t.Fatalf("Start failed: %v", err)
	}
	if err := m.Stop(inst.ID); err != nil {
		t.Fatalf("Stop failed: %v", err)
	}
	oldDir := filepath.Join(m.DataDir, "reasonix", inst.ID)
	if _, err := os.Stat(oldDir); err != nil {
		t.Fatalf("old reasonix dir missing before restart: %v", err)
	}

	if _, err := m.Restart(inst.ID); err != nil {
		t.Fatalf("Restart failed: %v", err)
	}
	if _, err := os.Stat(oldDir); !os.IsNotExist(err) {
		t.Fatalf("old reasonix dir still exists after restart: %v", err)
	}
}
