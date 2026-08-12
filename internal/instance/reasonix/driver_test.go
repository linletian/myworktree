package reasonix

import (
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestGenerateToken(t *testing.T) {
	tok, err := generateToken()
	if err != nil {
		t.Fatalf("generateToken: %v", err)
	}
	if len(tok) != 32 {
		t.Fatalf("token length = %d, want 32 hex chars", len(tok))
	}
	// Deterministic randomness: two tokens differ.
	tok2, err := generateToken()
	if err != nil {
		t.Fatalf("generateToken 2: %v", err)
	}
	if tok == tok2 {
		t.Fatal("two tokens are identical")
	}
}

func TestRemoveEnv(t *testing.T) {
	in := []string{
		"PATH=/bin",
		"REASONIX_HOME=/fake/home",
		"REASONIX_HOME=/dup", // duplicate key: every occurrence is stripped
		"HOME=/home/u",
		"REASONIX_STATE_HOME=/fake/state",
		"REASONIX_STATE_HOME", // "="less entry for a target key: stripped
		"OTHER=1",
		"BAREVAR", // "="less entry for a non-target key: kept as-is
	}
	got := RemoveEnv(in, "REASONIX_HOME", "REASONIX_STATE_HOME")
	want := []string{"PATH=/bin", "HOME=/home/u", "OTHER=1", "BAREVAR"}
	if len(got) != len(want) {
		t.Fatalf("RemoveEnv = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("RemoveEnv = %v, want %v", got, want)
		}
	}
}

// TestStartStripsReasonixHome verifies that a serve spawned by Start does not
// inherit REASONIX_HOME / REASONIX_STATE_HOME from the parent environment.
// The fake serve echoes both in its HTTP response, so this check runs on any
// platform (no /proc dependency). Without it, a host that carries
// REASONIX_HOME would leak it into the serve and the isolation we removed
// would silently come back.
func TestStartStripsReasonixHome(t *testing.T) {
	t.Setenv("REASONIX_HOME", "/fake/home")
	t.Setenv("REASONIX_STATE_HOME", "/fake/state")

	dataDir := t.TempDir()
	worktree := t.TempDir()
	d := &Driver{DataDir: dataDir, ReasonixBin: fakeServeBin(t)}
	id := "inst1"

	info, err := d.Start(StartInput{InstanceID: id, WorktreePath: worktree})
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	defer func() { _ = d.Stop(id) }()

	resp, err := http.Get(fmt.Sprintf("http://127.0.0.1:%d/", info.Port))
	if err != nil {
		t.Fatalf("GET serve: %v", err)
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("read body: %v", err)
	}
	// Response format: env:<MW_RX_TEST_ENV>;rxhome:<REASONIX_HOME>;rxstate:<REASONIX_STATE_HOME>.
	if !strings.HasPrefix(string(body), "env:") {
		t.Fatalf("fake serve response missing env: prefix (corrupt/truncated?): %q", body)
	}
	for _, field := range []struct {
		marker string
		name   string
	}{
		{";rxhome:", "REASONIX_HOME"},
		{";rxstate:", "REASONIX_STATE_HOME"},
	} {
		s := string(body)
		idx := strings.Index(s, field.marker)
		if idx < 0 {
			t.Fatalf("fake serve response missing %s marker: %q", field.name, s)
		}
		// The value runs to the next ';' or the end of the response.
		rest := s[idx+len(field.marker):]
		val := rest
		if semi := strings.IndexByte(rest, ';'); semi >= 0 {
			val = rest[:semi]
		}
		if val != "" {
			t.Fatalf("serve process inherited %s=%q; want empty (stripped)", field.name, val)
		}
	}
}

// TestConcurrentInstancesSameCwd verifies the driver-level guarantee behind
// the shared-pool model: several reasonix instances on the SAME worktree
// (same cwd) start independently — each gets its own serve process, port,
// token and pid files, and they do not interfere. (Real session-lease
// serialization lives inside reasonix itself; this guards the driver layer.)
func TestConcurrentInstancesSameCwd(t *testing.T) {
	dataDir := t.TempDir()
	worktree := t.TempDir()
	d := &Driver{DataDir: dataDir, ReasonixBin: fakeServeBin(t)}

	ids := []string{"inst-a", "inst-b"}
	infos := make(map[string]Info, len(ids))
	for _, id := range ids {
		info, err := d.Start(StartInput{InstanceID: id, WorktreePath: worktree})
		if err != nil {
			t.Fatalf("Start %s: %v", id, err)
		}
		infos[id] = info
		defer func(id string) { _ = d.Stop(id) }(id)
	}
	if infos["inst-a"].PID == infos["inst-b"].PID {
		t.Fatal("two instances share the same serve pid")
	}
	if infos["inst-a"].Port == infos["inst-b"].Port {
		t.Fatal("two instances share the same port")
	}
	if infos["inst-a"].Token == infos["inst-b"].Token {
		t.Fatal("two instances share the same token")
	}
	// Both stay healthy while running side by side, and both actually serve
	// HTTP concurrently (not merely "did not deadlock").
	for _, id := range ids {
		if _, ok, err := d.Health(id); err != nil || !ok {
			t.Fatalf("Health %s after concurrent start: ok=%v err=%v", id, ok, err)
		}
		resp, err := http.Get(fmt.Sprintf("http://127.0.0.1:%d/", infos[id].Port))
		if err != nil {
			t.Fatalf("GET %s serve: %v", id, err)
		}
		_ = resp.Body.Close()
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("GET %s serve status = %d, want 200", id, resp.StatusCode)
		}
	}
}

func TestAddr(t *testing.T) {
	d := &Driver{DataDir: t.TempDir()}
	id := "abc123"
	dir := d.dir(id)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "port"), []byte("127.0.0.1:4321\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "token"), []byte("tok123\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	info, err := d.Addr(id)
	if err != nil {
		t.Fatalf("Addr: %v", err)
	}
	if info.Port != 4321 || info.Token != "tok123" {
		t.Fatalf("Addr = %+v, want port 4321 token tok123", info)
	}

	// Unknown instance → error.
	if _, err := d.Addr("nope"); err == nil {
		t.Fatal("Addr for unknown instance should error")
	}
}

func TestHealthDeadPid(t *testing.T) {
	d := &Driver{DataDir: t.TempDir()}
	id := "dead1"
	dir := d.dir(id)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	// An impossibly large pid is very unlikely to be alive.
	if err := os.WriteFile(filepath.Join(dir, "pid"), []byte("99999999\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	info, ok, err := d.Health(id)
	if err != nil {
		t.Fatalf("Health: %v", err)
	}
	if ok {
		t.Fatalf("Health reported dead pid as alive: %+v", info)
	}
}

func TestHealthAlive(t *testing.T) {
	d := &Driver{DataDir: t.TempDir()}
	id := "alive1"
	dir := d.dir(id)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "pid"), []byte(strconv.Itoa(os.Getpid())+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	// Health probes the TCP port, so point the port file at a real listener.
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	port := ln.Addr().(*net.TCPAddr).Port
	if err := os.WriteFile(filepath.Join(dir, "port"), []byte(fmt.Sprintf("127.0.0.1:%d\n", port)), 0o600); err != nil {
		t.Fatal(err)
	}
	info, ok, err := d.Health(id)
	if err != nil {
		t.Fatalf("Health: %v", err)
	}
	if !ok {
		t.Fatalf("Health reported own process as dead")
	}
	if info.PID != os.Getpid() || info.Port != port {
		t.Fatalf("Health = %+v, want pid %d port %d", info, os.Getpid(), port)
	}
}

func TestHealthPortClosed(t *testing.T) {
	d := &Driver{DataDir: t.TempDir()}
	id := "closed1"
	dir := d.dir(id)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "pid"), []byte(strconv.Itoa(os.Getpid())+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	// A listener that is closed again: process (us) is alive but the port is
	// not serving → Health must report not-ok.
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	port := ln.Addr().(*net.TCPAddr).Port
	_ = ln.Close()
	if err := os.WriteFile(filepath.Join(dir, "port"), []byte(fmt.Sprintf("127.0.0.1:%d\n", port)), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, ok, err := d.Health(id); err != nil || ok {
		t.Fatalf("Health on closed port: ok=%v err=%v, want ok=false", ok, err)
	}
}

// fakeServeBin writes a script that emulates `reasonix serve`: it binds
// 127.0.0.1:0, writes the real address to --port-file and its pid to
// --pid-file, then serves HTTP until terminated. python3 responds to SIGTERM
// by default.
func fakeServeBin(t *testing.T) string {
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
        body = ("env:" + os.environ.get("MW_RX_TEST_ENV", "unset")
                + ";rxhome:" + os.environ.get("REASONIX_HOME", "")
                + ";rxstate:" + os.environ.get("REASONIX_STATE_HOME", "")).encode()
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

func TestStartStopFakeServe(t *testing.T) {
	dataDir := t.TempDir()
	worktree := t.TempDir()
	d := &Driver{DataDir: dataDir, ReasonixBin: fakeServeBin(t)}
	id := "inst1"

	info, err := d.Start(StartInput{InstanceID: id, WorktreePath: worktree})
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	defer func() { _ = d.Stop(id) }()
	if info.Port <= 0 {
		t.Fatalf("port = %d, want > 0", info.Port)
	}
	if info.PID <= 0 {
		t.Fatalf("pid = %d, want > 0", info.PID)
	}
	if len(info.Token) != 32 {
		t.Fatalf("token length = %d, want 32", len(info.Token))
	}

	// State dir contents: token, port, pid; NO session.jsonl — sessions live
	// in the shared ~/.reasonix project pool (per-cwd), not per instance.
	dir := d.dir(id)
	if b, err := os.ReadFile(filepath.Join(dir, "token")); err != nil || strings.TrimSpace(string(b)) != info.Token {
		t.Fatalf("token file = %q, err %v", b, err)
	}
	if _, err := os.Stat(filepath.Join(dir, "session.jsonl")); !os.IsNotExist(err) {
		t.Fatalf("session.jsonl should not exist after isolation removal: %v", err)
	}

	// Health reports alive.
	hinfo, ok, err := d.Health(id)
	if err != nil || !ok {
		t.Fatalf("Health after Start: ok=%v err=%v", ok, err)
	}
	if hinfo.PID != info.PID || hinfo.Port != info.Port {
		t.Fatalf("Health info mismatch: %+v vs %+v", hinfo, info)
	}

	// Start again is idempotent (same pid/port).
	info2, err := d.Start(StartInput{InstanceID: id, WorktreePath: worktree})
	if err != nil {
		t.Fatalf("second Start: %v", err)
	}
	if info2.PID != info.PID || info2.Port != info.Port {
		t.Fatalf("second Start changed identity: %+v vs %+v", info2, info)
	}

	// Stop kills it; Health then reports dead.
	if err := d.Stop(id); err != nil {
		t.Fatalf("Stop: %v", err)
	}
	deadline := time.Now().Add(3 * time.Second)
	for {
		_, ok, _ := d.Health(id)
		if !ok {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("process still alive after Stop")
		}
		time.Sleep(50 * time.Millisecond)
	}

	// Stop again is a no-op.
	if err := d.Stop(id); err != nil {
		t.Fatalf("second Stop: %v", err)
	}
}

func TestStartInvalidInputs(t *testing.T) {
	wt := t.TempDir()
	if _, err := (&Driver{DataDir: t.TempDir()}).Start(StartInput{WorktreePath: wt}); err == nil {
		t.Fatal("empty InstanceID should error")
	}
	if _, err := (&Driver{DataDir: t.TempDir()}).Start(StartInput{InstanceID: "x"}); err == nil {
		t.Fatal("empty WorktreePath should error")
	}
	if _, err := (&Driver{}).Start(StartInput{InstanceID: "x", WorktreePath: wt}); err == nil {
		t.Fatal("empty DataDir should error")
	}
	// Nonexistent binary → start failure.
	bad := &Driver{DataDir: t.TempDir(), ReasonixBin: filepath.Join(t.TempDir(), "does-not-exist")}
	if _, err := bad.Start(StartInput{InstanceID: "x", WorktreePath: wt}); err == nil {
		t.Fatal("nonexistent ReasonixBin should error")
	}
}

// fakeVersionBin writes a script that prints a fixed `reasonix --version`
// line and exits — used to exercise the version gate without a real CLI.
func fakeVersionBin(t *testing.T, versionLine string) string {
	t.Helper()
	bin := filepath.Join(t.TempDir(), "fake-version.py")
	script := "#!/usr/bin/env python3\nimport sys\nprint(" + strconv.Quote(versionLine) + ")\n"
	if err := os.WriteFile(bin, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	return bin
}

func TestDriverVersionGate(t *testing.T) {
	wt := t.TempDir()
	tooOld := fakeVersionBin(t, "reasonix v1.20.0")
	if _, err := (&Driver{DataDir: t.TempDir(), ReasonixBin: tooOld}).Start(StartInput{InstanceID: "x", WorktreePath: wt}); err == nil {
		t.Fatal("Start with v1.20.0 should fail (min 1.22.0)")
	} else if !strings.Contains(err.Error(), "1.22.0 required") {
		t.Fatalf("unexpected version error: %v", err)
	}

	newEnough := fakeVersionBin(t, "reasonix v1.22.0")
	d := &Driver{DataDir: t.TempDir(), ReasonixBin: newEnough}
	// v1.22.0 passes the gate, then Start must fail on the serve phase
	// (fake-version exits after --version, so no port file appears), and the
	// error must carry the serve.log tail (issue #45).
	if _, err := d.Start(StartInput{InstanceID: "x", WorktreePath: wt}); err == nil {
		t.Fatal("Start should fail after gate: fake version bin never serves")
	} else if !strings.Contains(err.Error(), "serve.log tail") {
		t.Fatalf("readiness error should include serve.log tail, got: %v", err)
	}
}

func TestDriverVersionGateDevBuild(t *testing.T) {
	dev := fakeVersionBin(t, "reasonix dev")
	_, err := (&Driver{DataDir: t.TempDir(), ReasonixBin: dev}).Start(StartInput{InstanceID: "x", WorktreePath: t.TempDir()})
	if err == nil {
		t.Fatal("expected serve-phase failure")
	}
	if strings.Contains(err.Error(), "required") {
		t.Fatalf("dev build must not be blocked by the version gate: %v", err)
	}
}

// cache after Start (no file reads), and Stop/Cleanup invalidate it.
func TestDriverAddrCache(t *testing.T) {
	dataDir := t.TempDir()
	worktree := t.TempDir()
	d := &Driver{DataDir: dataDir, ReasonixBin: fakeServeBin(t)}
	id := "inst1"
	info, err := d.Start(StartInput{InstanceID: id, WorktreePath: worktree})
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	defer func() { _ = d.Stop(id) }()

	// Prime the cache.
	if _, err := d.Addr(id); err != nil {
		t.Fatalf("Addr: %v", err)
	}
	// Remove the backing files: a cached Addr must still succeed without
	// reading them (this is what the proxy path relies on).
	dir := d.dir(id)
	if err := os.Remove(filepath.Join(dir, "port")); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(filepath.Join(dir, "token")); err != nil {
		t.Fatal(err)
	}
	got, err := d.Addr(id)
	if err != nil {
		t.Fatalf("cached Addr failed after files removed: %v", err)
	}
	if got.Port != info.Port || got.Token != info.Token {
		t.Fatalf("cached Addr = %+v, want port %d token %s", got, info.Port, info.Token)
	}

	// Stop invalidates the cache: Addr now fails (files are gone, no cache).
	if err := d.Stop(id); err != nil {
		t.Fatalf("Stop: %v", err)
	}
	if _, err := d.Addr(id); err == nil {
		t.Fatal("Addr after Stop must fail (cache invalidated)")
	}
}

// TestDriverAddrCacheRefresh verifies Restart semantics: a fresh id gets a
// fresh cache entry with the new port/token, and the stale entry is dropped
// with Cleanup.
func TestDriverAddrCacheRefresh(t *testing.T) {
	dataDir := t.TempDir()
	worktree := t.TempDir()
	d := &Driver{DataDir: dataDir, ReasonixBin: fakeServeBin(t)}

	oldID := "old"
	oldInfo, err := d.Start(StartInput{InstanceID: oldID, WorktreePath: worktree})
	if err != nil {
		t.Fatalf("Start old: %v", err)
	}
	defer func() { _ = d.Stop(oldID) }()
	if _, err := d.Addr(oldID); err != nil {
		t.Fatalf("Addr old: %v", err)
	}

	// Simulate Restart: new id, old state dir removed.
	newID := "new"
	newInfo, err := d.Start(StartInput{InstanceID: newID, WorktreePath: worktree})
	if err != nil {
		t.Fatalf("Start new: %v", err)
	}
	defer func() { _ = d.Stop(newID) }()
	if newInfo.Port == oldInfo.Port {
		t.Fatalf("ports should differ across starts: %d", oldInfo.Port)
	}

	if err := d.Cleanup(oldID); err != nil {
		t.Fatalf("Cleanup old: %v", err)
	}
	if _, err := d.Addr(oldID); err == nil {
		t.Fatal("Addr old after Cleanup must fail (cache dropped)")
	}
	got, err := d.Addr(newID)
	if err != nil || got.Port != newInfo.Port {
		t.Fatalf("Addr new = %+v err %v, want port %d", got, err, newInfo.Port)
	}
}

// TestDriverConcurrentStart verifies the per-instance Start lock: concurrent
// Start calls for the same id must not both spawn a serve process (review
// should-fix #2). Without the lock, two spawns would race on the same
// port/pid files, the loser's process would be untracked and would survive
// a single Stop.
// TestCleanupDropsStartLock verifies that tearing an instance down (Cleanup)
// removes its per-instance Start lock, so the starts map cannot grow without
// bound across restarts (each restart allocates a fresh id; the old entry
// would otherwise live forever).
func TestCleanupDropsStartLock(t *testing.T) {
	d := &Driver{DataDir: t.TempDir(), ReasonixBin: fakeServeBin(t)}
	unlock := d.lockStart("rx-drop")
	unlock()
	if _, ok := d.starts["rx-drop"]; !ok {
		t.Fatal("lock should exist after first lockStart")
	}
	if err := d.Cleanup("rx-drop"); err != nil {
		t.Fatalf("Cleanup: %v", err)
	}
	if _, ok := d.starts["rx-drop"]; ok {
		t.Fatal("starts entry must be dropped by Cleanup (id is never reused)")
	}
	// A fresh id still starts normally after the drop.
	if _, err := d.Start(StartInput{InstanceID: "rx-fresh", WorktreePath: t.TempDir()}); err != nil {
		t.Fatalf("Start after drop: %v", err)
	}
	defer func() { _ = d.Stop("rx-fresh") }()
}

func TestDriverConcurrentStart(t *testing.T) {
	dataDir := t.TempDir()
	d := &Driver{DataDir: dataDir, ReasonixBin: fakeServeBin(t)}
	id := "rx-conc"
	worktree := t.TempDir()

	const n = 4
	infos := make([]Info, n)
	errs := make([]error, n)
	var wg sync.WaitGroup
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			infos[i], errs[i] = d.Start(StartInput{InstanceID: id, WorktreePath: worktree})
		}(i)
	}
	wg.Wait()

	for i := 0; i < n; i++ {
		if errs[i] != nil {
			t.Fatalf("concurrent Start %d: %v", i, errs[i])
		}
	}
	// All callers must observe the SAME process: a duplicate spawn would
	// allocate a new random port and diverge the Infos.
	for i := 1; i < n; i++ {
		if infos[i] != infos[0] {
			t.Fatalf("concurrent Starts returned different Info: %+v vs %+v", infos[i], infos[0])
		}
	}

	// One Stop must leave nothing behind: with a stray second process alive,
	// Health would still report healthy after Stop.
	if err := d.Stop(id); err != nil {
		t.Fatalf("Stop: %v", err)
	}
	if _, ok, _ := d.Health(id); ok {
		t.Fatal("stray process survived Stop (duplicate spawn under concurrent Start)")
	}
}

// TestDriverEnvInjection verifies StartInput.Env reaches the serve process
// (DEFERRED §3: e.g. HTTP_PROXY injection).
func TestDriverEnvInjection(t *testing.T) {
	d := &Driver{DataDir: t.TempDir(), ReasonixBin: fakeServeBin(t)}
	info, err := d.Start(StartInput{
		InstanceID:   "inst1",
		WorktreePath: t.TempDir(),
		Env:          []string{"MW_RX_TEST_ENV=hello"},
	})
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	defer func() { _ = d.Stop("inst1") }()

	resp, err := http.Get("http://127.0.0.1:" + strconv.Itoa(info.Port) + "/")
	if err != nil {
		t.Fatalf("GET: %v", err)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if !strings.Contains(string(body), "env:hello") {
		t.Fatalf("serve env missing MW_RX_TEST_ENV: %q", body)
	}
}

// TestDriverPreStart verifies the PreStart hook runs before serve and that an
// error aborts Start without launching the process.
func TestDriverPreStart(t *testing.T) {
	ran := false
	d := &Driver{DataDir: t.TempDir(), ReasonixBin: fakeServeBin(t)}
	info, err := d.Start(StartInput{
		InstanceID:   "inst1",
		WorktreePath: t.TempDir(),
		PreStart: func() error {
			ran = true
			return nil
		},
	})
	if err != nil {
		t.Fatalf("Start with ok preStart: %v", err)
	}
	defer func() { _ = d.Stop("inst1") }()
	if !ran || info.Port <= 0 {
		t.Fatalf("preStart ran=%v port=%d", ran, info.Port)
	}

	// A failing preStart aborts Start; no serve process is launched, so no
	// pid file appears.
	bad := &Driver{DataDir: t.TempDir(), ReasonixBin: fakeServeBin(t)}
	if _, err := bad.Start(StartInput{
		InstanceID:   "inst2",
		WorktreePath: t.TempDir(),
		PreStart: func() error {
			return errors.New("boom")
		},
	}); err == nil {
		t.Fatal("Start with failing preStart should error")
	} else if !strings.Contains(err.Error(), "preStart failed") {
		t.Fatalf("unexpected error: %v", err)
	}
	if _, err := os.Stat(filepath.Join(bad.dir("inst2"), "pid")); !os.IsNotExist(err) {
		t.Fatalf("pid file should not exist after failed preStart (serve must not be spawned)")
	}
}
