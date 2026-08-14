package framework_test

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"

	"myworktree/internal/framework"
	"myworktree/internal/instance/reasonix"
	"myworktree/internal/store"
	"myworktree/internal/tag"
)

// fakeServeBinT writes a python3 script that emulates `reasonix serve`: it
// binds 127.0.0.1:0, writes the real address to --port-file and its pid to
// --pid-file, then serves HTTP until terminated (Health probes the TCP port).
func fakeServeBinT(t *testing.T) string {
	t.Helper()
	bin := filepath.Join(t.TempDir(), "fake-serve.py")
	script := `#!/usr/bin/env python3
import os, sys
# Avoid DNS reverse-lookup (socket.getfqdn) hanging on CI hosts with broken
# DNS (e.g. GitHub macOS runners): HTTPServer.server_bind calls getfqdn.
import socket
socket.getfqdn = lambda host="": host if host else "localhost"
from http.server import BaseHTTPRequestHandler, HTTPServer
args = sys.argv[1:]
if args[:1] == ["--version"]:
    print("reasonix v1.22.0")
    sys.exit(0)
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
        body = ("env:" + os.environ.get("MW_RX_TEST_ENV", "unset")).encode()
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

// rxTempDir creates a tracked temp dir (helper unique to the reasonix
// framework tests; other framework tests bring their own).
func rxTempDir(t *testing.T, prefix string) string {
	t.Helper()
	dir, err := os.MkdirTemp("", prefix)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(dir) })
	return dir
}

// newReasonixTestManagerOn builds a framework Manager with a fresh registry
// containing only the reasonix kind (driven by drv), over the given store
// and data dir. Reusing it with a shared fs/drv simulates a server restart
// (fresh in-memory state, persisted state intact).
func newReasonixTestManagerOn(t *testing.T, fs store.FileStore, dataDir string, drv *reasonix.Driver) *framework.Manager {
	t.Helper()
	reg := framework.NewRegistry()
	reg.Register(reasonix.NewKind(drv))
	m := framework.NewManager(reg, fs, nil)
	m.DataDir = dataDir
	m.Tags = tag.Manager{ProjectPath: filepath.Join(dataDir, "tags.json")}
	return m
}

func newReasonixTestManager(t *testing.T) (*framework.Manager, store.FileStore, *reasonix.Driver) {
	t.Helper()
	dataDir := rxTempDir(t, "mw-rx-data-")
	workDir := rxTempDir(t, "mw-rx-work-")
	fs := store.FileStore{Path: filepath.Join(dataDir, "state.json")}
	if err := fs.Save(store.State{
		Worktrees: []store.ManagedWorktree{
			{ID: "wt1", Name: "wt1", Path: workDir},
		},
	}); err != nil {
		t.Fatalf("seed state failed: %v", err)
	}
	drv := &reasonix.Driver{
		DataDir:     dataDir,
		ReasonixBin: fakeServeBinT(t),
	}
	return newReasonixTestManagerOn(t, fs, dataDir, drv), fs, drv
}

// waitInstanceStatus polls the store until the instance reaches want.
func waitInstanceStatus(t *testing.T, fs store.FileStore, id, want string) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		st, err := fs.Load()
		if err == nil {
			for _, it := range st.Instances {
				if it.ID == id && it.Status == want {
					return
				}
			}
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("instance %s never reached status %q", id, want)
}

// instanceByID loads the record for id from the store.
func instanceByID(t *testing.T, fs store.FileStore, id string) store.ManagedInstance {
	t.Helper()
	st, err := fs.Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	for _, it := range st.Instances {
		if it.ID == id {
			return it
		}
	}
	t.Fatalf("instance %q not found in store", id)
	return store.ManagedInstance{}
}

func TestStartStopReasonixInstance(t *testing.T) {
	m, fs, _ := newReasonixTestManager(t)

	inst, err := m.Start(context.Background(), framework.StartParams{WorktreeID: "wt1", Kind: reasonix.KindName, Name: "chat"})
	if err != nil {
		t.Fatalf("Start reasonix failed: %v", err)
	}
	if inst.Kind != reasonix.KindName {
		t.Fatalf("Kind = %q, want %q", inst.Kind, reasonix.KindName)
	}
	waitInstanceStatus(t, fs, inst.ID, "running")
	got := instanceByID(t, fs, inst.ID)
	if got.PID <= 0 {
		t.Fatalf("persisted PID = %d, want > 0", got.PID)
	}
	if got.Status != "running" {
		t.Fatalf("Status = %q, want running", got.Status)
	}

	if err := m.Stop(inst.ID); err != nil {
		t.Fatalf("Stop failed: %v", err)
	}
	waitInstanceStatus(t, fs, inst.ID, "stopped")

	// Stop must tear down the per-instance serve-management dir and the
	// Start lock: the id is never reused, so leaving them would let the
	// driver's starts map (and token/port/pid files) accumulate across
	// start→stop cycles.
	rxDir := filepath.Join(m.DataDir, "reasonix", inst.ID)
	if _, err := os.Stat(rxDir); !os.IsNotExist(err) {
		t.Fatalf("serve-management dir %s still exists after Stop (want removed)", rxDir)
	}

	// Stopping again is a no-op.
	if err := m.Stop(inst.ID); err != nil {
		t.Fatalf("second Stop failed: %v", err)
	}
}

func TestStartReasonixDisabled(t *testing.T) {
	dataDir := rxTempDir(t, "mw-rx-dis-")
	workDir := rxTempDir(t, "mw-rx-work-")
	fs := store.FileStore{Path: filepath.Join(dataDir, "state.json")}
	if err := fs.Save(store.State{
		Worktrees: []store.ManagedWorktree{
			{ID: "wt1", Name: "wt1", Path: workDir},
		},
	}); err != nil {
		t.Fatalf("seed state failed: %v", err)
	}
	// No reasonix kind registered → reasonix instances must be rejected.
	m := framework.NewManager(framework.NewRegistry(), fs, nil)
	m.DataDir = dataDir
	if _, err := m.Start(context.Background(), framework.StartParams{WorktreeID: "wt1", Kind: reasonix.KindName}); err == nil {
		t.Fatal("Start with unregistered reasonix kind should error")
	}
}

func TestRestartReasonixKeepsKind(t *testing.T) {
	m, fs, _ := newReasonixTestManager(t)

	inst, err := m.Start(context.Background(), framework.StartParams{WorktreeID: "wt1", Kind: reasonix.KindName, Name: "chat"})
	if err != nil {
		t.Fatalf("Start failed: %v", err)
	}
	waitInstanceStatus(t, fs, inst.ID, "running")
	if err := m.Stop(inst.ID); err != nil {
		t.Fatalf("Stop failed: %v", err)
	}
	waitInstanceStatus(t, fs, inst.ID, "stopped")

	newInst, err := m.Restart(inst.ID)
	if err != nil {
		t.Fatalf("Restart failed: %v", err)
	}
	if newInst.Kind != reasonix.KindName {
		t.Fatalf("restarted Kind = %q, want %q", newInst.Kind, reasonix.KindName)
	}
	if newInst.ID == inst.ID {
		t.Fatalf("restart should allocate a new instance id, got same %q", newInst.ID)
	}
	waitInstanceStatus(t, fs, newInst.ID, "running")
	if err := m.Stop(newInst.ID); err != nil {
		t.Fatalf("Stop restarted failed: %v", err)
	}
}

func TestDeleteReasonixInstance(t *testing.T) {
	m, fs, _ := newReasonixTestManager(t)

	inst, err := m.Start(context.Background(), framework.StartParams{WorktreeID: "wt1", Kind: reasonix.KindName, Name: "chat"})
	if err != nil {
		t.Fatalf("Start failed: %v", err)
	}
	waitInstanceStatus(t, fs, inst.ID, "running")
	// Delete requires a stopped instance (same as pty semantics).
	if err := m.Delete(inst.ID); err == nil {
		t.Fatal("Delete of a running instance should error")
	}
	if err := m.Stop(inst.ID); err != nil {
		t.Fatalf("Stop failed: %v", err)
	}
	waitInstanceStatus(t, fs, inst.ID, "stopped")
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
	m, fs, drv := newReasonixTestManager(t)

	inst, err := m.Start(context.Background(), framework.StartParams{WorktreeID: "wt1", Kind: reasonix.KindName, Name: "chat"})
	if err != nil {
		t.Fatalf("Start failed: %v", err)
	}
	waitInstanceStatus(t, fs, inst.ID, "running")
	// Capture the live pid before Delete wipes the state dir.
	info, ok, herr := drv.Health(inst.ID)
	if herr != nil || !ok || info.PID <= 0 {
		t.Fatalf("no live serve pid before Delete: ok=%v err=%v", ok, herr)
	}
	pid := info.PID

	// Force the state to "stopped" while the process is still alive, and
	// simulate a server restart (fresh manager, no in-memory running
	// entry) — the production shape of this window.
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
	m2 := newReasonixTestManagerOn(t, fs, m.DataDir, drv)

	if err := m2.Delete(inst.ID); err != nil {
		t.Fatalf("Delete failed: %v", err)
	}
	// The lingering serve process must be gone. kill(pid, 0) is the probe.
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
	m, fs, drv := newReasonixTestManager(t)

	inst, err := m.Start(context.Background(), framework.StartParams{WorktreeID: "wt1", Kind: reasonix.KindName, Name: "chat"})
	if err != nil {
		t.Fatalf("Start failed: %v", err)
	}
	waitInstanceStatus(t, fs, inst.ID, "running")

	// The serve subprocess survives a server restart; Reconcile on a fresh
	// manager must re-attach it (keep "running") instead of marking it
	// stopped and orphaning it.
	m2 := newReasonixTestManagerOn(t, fs, m.DataDir, drv)
	changed, err := m2.ReconcileRunningOnStartup()
	if err != nil {
		t.Fatalf("Reconcile: %v", err)
	}
	if changed != 0 {
		t.Fatalf("Reconcile changed %d instances, want 0 (live reasonix kept running)", changed)
	}
	if got := instanceByID(t, fs, inst.ID); got.Status != "running" {
		t.Fatalf("live reasonix instance status = %q, want running", got.Status)
	}

	// The re-attached handle must be stoppable through the fresh manager.
	if err := m2.Stop(inst.ID); err != nil {
		t.Fatalf("cleanup Stop failed: %v", err)
	}
	waitInstanceStatus(t, fs, inst.ID, "stopped")
}

func TestReconcileStopsDeadReasonix(t *testing.T) {
	m, fs, drv := newReasonixTestManager(t)

	inst, err := m.Start(context.Background(), framework.StartParams{WorktreeID: "wt1", Kind: reasonix.KindName, Name: "chat"})
	if err != nil {
		t.Fatalf("Start failed: %v", err)
	}
	waitInstanceStatus(t, fs, inst.ID, "running")
	pid := instanceByID(t, fs, inst.ID).PID
	// Kill the serve process out-of-band; state still says "running".
	if err := syscall.Kill(pid, syscall.SIGKILL); err != nil {
		t.Fatalf("kill serve: %v", err)
	}
	deadline := time.Now().Add(5 * time.Second)
	for {
		if _, ok, _ := drv.Health(inst.ID); !ok {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("serve process still healthy after SIGKILL")
		}
		time.Sleep(50 * time.Millisecond)
	}

	m2 := newReasonixTestManagerOn(t, fs, m.DataDir, drv)
	changed, err := m2.ReconcileRunningOnStartup()
	if err != nil {
		t.Fatalf("Reconcile: %v", err)
	}
	if changed != 1 {
		t.Fatalf("Reconcile changed %d instances, want 1 (dead reasonix marked stopped)", changed)
	}
	if got := instanceByID(t, fs, inst.ID); got.Status != "stopped" {
		t.Fatalf("dead reasonix instance status = %q, want stopped", got.Status)
	}
}

func TestRestartReasonixCleansOldDir(t *testing.T) {
	m, fs, _ := newReasonixTestManager(t)

	inst, err := m.Start(context.Background(), framework.StartParams{WorktreeID: "wt1", Kind: reasonix.KindName, Name: "chat"})
	if err != nil {
		t.Fatalf("Start failed: %v", err)
	}
	waitInstanceStatus(t, fs, inst.ID, "running")
	if err := m.Stop(inst.ID); err != nil {
		t.Fatalf("Stop failed: %v", err)
	}
	waitInstanceStatus(t, fs, inst.ID, "stopped")
	oldDir := filepath.Join(m.DataDir, "reasonix", inst.ID)
	// Stop now tears the serve-management dir down itself, so no leftover
	// exists to exercise Restart's cleanup; seed an artificial one to prove
	// Restart still removes any stale dir for the old id (RemoveAll on a
	// missing dir is a no-op, so this assertion stays meaningful).
	if err := os.MkdirAll(filepath.Join(oldDir, "stale"), 0o755); err != nil {
		t.Fatalf("seed stale dir: %v", err)
	}
	if _, err := os.Stat(oldDir); err != nil {
		t.Fatalf("stale reasonix dir missing before restart: %v", err)
	}

	if _, err := m.Restart(inst.ID); err != nil {
		t.Fatalf("Restart failed: %v", err)
	}
	if _, err := os.Stat(oldDir); !os.IsNotExist(err) {
		t.Fatalf("old reasonix dir still exists after restart: %v", err)
	}
}

// TestStopAllKind verifies Server.Shutdown parity: StopAllKind stops
// every running reasonix serve but leaves stopped instances untouched.
// No per-instance session.jsonl exists — sessions live in the shared
// ~/.reasonix project pool, untouched by instance lifecycle.
func TestStopAllKind(t *testing.T) {
	m, fs, drv := newReasonixTestManager(t)

	run, err := m.Start(context.Background(), framework.StartParams{WorktreeID: "wt1", Kind: reasonix.KindName, Name: "running"})
	if err != nil {
		t.Fatalf("Start running: %v", err)
	}
	dead, err := m.Start(context.Background(), framework.StartParams{WorktreeID: "wt1", Kind: reasonix.KindName, Name: "stopped"})
	if err != nil {
		t.Fatalf("Start stopped: %v", err)
	}
	waitInstanceStatus(t, fs, run.ID, "running")
	waitInstanceStatus(t, fs, dead.ID, "running")
	if err := m.Stop(dead.ID); err != nil {
		t.Fatalf("Stop dead: %v", err)
	}
	waitInstanceStatus(t, fs, dead.ID, "stopped")

	m.StopAllKind(reasonix.KindName)

	// The running instance's serve is now stopped (Health fails).
	if _, ok, herr := drv.Health(run.ID); herr != nil || ok {
		t.Fatalf("running instance should be stopped after StopAllKind (ok=%v err=%v)", ok, herr)
	}
	// No per-instance session.jsonl is ever created — sessions live in the
	// shared ~/.reasonix project pool.
	if _, err := os.Stat(filepath.Join(m.DataDir, "reasonix", run.ID, "session.jsonl")); !os.IsNotExist(err) {
		t.Fatalf("session.jsonl for %s should not exist (shared pool, not per-instance): %v", run.ID, err)
	}
}

// TestReasonixTagEnvAndPreStart verifies (DEFERRED §3) that a tag's env flows
// into the serve process and its preStart runs before serve; the tag command
// is NOT executed (reasonix instances run the agent, not a shell command).
func TestReasonixTagEnvAndPreStart(t *testing.T) {
	// preStart is executed via `zsh -lc` (same shell contract as the tag
	// command); skip on hosts without zsh (e.g. bare ubuntu runners) so the
	// test does not fail on the shell itself. CI installs zsh, so coverage
	// is retained there.
	if _, err := exec.LookPath("zsh"); err != nil {
		t.Skip("zsh not installed; preStart (zsh -lc) not testable")
	}
	m, _, drv := newReasonixTestManager(t)
	marker := filepath.Join(m.DataDir, "pre-marker")
	tagsJSON := fmt.Sprintf(`{"tags":[{"id":"rxenv","command":"echo I-MUST-NOT-RUN","env":{"MW_RX_TEST_ENV":"from-tag"},"preStart":"echo pre-ran > %s"}]}`, marker)
	if err := os.WriteFile(filepath.Join(m.DataDir, "tags.json"), []byte(tagsJSON), 0o600); err != nil {
		t.Fatal(err)
	}

	inst, err := m.Start(context.Background(), framework.StartParams{WorktreeID: "wt1", TagID: "rxenv", Kind: reasonix.KindName, Name: "chat"})
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	defer func() { _ = m.Stop(inst.ID) }()

	// preStart ran before serve.
	if _, err := os.Stat(marker); err != nil {
		t.Fatalf("preStart marker missing (preStart did not run): %v", err)
	}
	// The tag command must NOT have been executed.
	if b, err := os.ReadFile(marker); err == nil && strings.Contains(string(b), "I-MUST-NOT-RUN") {
		t.Fatalf("tag command was executed for a reasonix instance: %s", b)
	}

	// Tag env reached the serve process (fake serve echoes it).
	info, err := drv.Addr(inst.ID)
	if err != nil {
		t.Fatalf("Addr: %v", err)
	}
	resp, err := http.Get("http://127.0.0.1:" + strconv.Itoa(info.Port) + "/")
	if err != nil {
		t.Fatalf("GET: %v", err)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if !strings.Contains(string(body), "env:from-tag") {
		t.Fatalf("serve env missing tag env MW_RX_TEST_ENV: %q", body)
	}
}

func TestReasonixPreStartStripsInheritedHome(t *testing.T) {
	// preStart must run under the same stripped environment as serve
	// (reasonix.RemoveEnv), so a host-exported REASONIX_HOME /
	// REASONIX_STATE_HOME cannot make the two phases resolve different
	// ~/.reasonix homes. Skip without zsh (same contract as the sibling test).
	if _, err := exec.LookPath("zsh"); err != nil {
		t.Skip("zsh not installed; preStart (zsh -lc) not testable")
	}
	t.Setenv("REASONIX_HOME", "/fake/host/home")
	t.Setenv("REASONIX_STATE_HOME", "/fake/host/state")
	m, _, _ := newReasonixTestManager(t)
	marker := filepath.Join(m.DataDir, "pre-env-marker")
	tagsJSON := fmt.Sprintf(`{"tags":[{"id":"rxenv2","command":"echo I-MUST-NOT-RUN","preStart":"env | grep -E '^(REASONIX_HOME|REASONIX_STATE_HOME)=' > %s; true"}]}`, marker)
	if err := os.WriteFile(filepath.Join(m.DataDir, "tags.json"), []byte(tagsJSON), 0o600); err != nil {
		t.Fatal(err)
	}

	inst, err := m.Start(context.Background(), framework.StartParams{WorktreeID: "wt1", TagID: "rxenv2", Kind: reasonix.KindName, Name: "chat"})
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	defer func() { _ = m.Stop(inst.ID) }()

	b, err := os.ReadFile(marker)
	if err != nil {
		t.Fatalf("read preStart env marker: %v", err)
	}
	if len(b) != 0 {
		t.Fatalf("preStart inherited host REASONIX_HOME/REASONIX_STATE_HOME (serve strips them; preStart must too): %q", b)
	}
}

// TestKindNameMatchesStoreConstant guards the duplicated "reasonix"
// string in store.KindReasonix and reasonix.KindName (the two packages
// cannot reference each other without an import cycle).
func TestKindNameMatchesStoreConstant(t *testing.T) {
	if reasonix.KindName != store.KindReasonix {
		t.Fatalf("reasonix.KindName = %q, store.KindReasonix = %q — keep them in sync", reasonix.KindName, store.KindReasonix)
	}
}
