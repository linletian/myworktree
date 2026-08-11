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

func TestSymlinkIfExists(t *testing.T) {
	dir := t.TempDir()
	src := filepath.Join(dir, "src-file")
	if err := os.WriteFile(src, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}

	target := filepath.Join(dir, "target")
	if err := symlinkIfExists(src, target); err != nil {
		t.Fatalf("symlinkIfExists: %v", err)
	}
	fi, err := os.Lstat(target)
	if err != nil {
		t.Fatalf("target not created: %v", err)
	}
	if fi.Mode()&os.ModeSymlink == 0 {
		t.Fatal("target is not a symlink")
	}

	// Idempotent: existing target is left alone.
	if err := symlinkIfExists(src, target); err != nil {
		t.Fatalf("second symlinkIfExists: %v", err)
	}

	// Missing source: no-op, no error.
	missing := filepath.Join(dir, "missing")
	if err := symlinkIfExists(missing, filepath.Join(dir, "t2")); err != nil {
		t.Fatalf("missing source should be a no-op: %v", err)
	}
	if _, err := os.Lstat(filepath.Join(dir, "t2")); !os.IsNotExist(err) {
		t.Fatal("target for missing source should not exist")
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

	// State dir contents: token, port, pid, session.jsonl, symlinks skipped.
	dir := d.dir(id)
	if b, err := os.ReadFile(filepath.Join(dir, "token")); err != nil || strings.TrimSpace(string(b)) != info.Token {
		t.Fatalf("token file = %q, err %v", b, err)
	}
	if _, err := os.Stat(filepath.Join(dir, "session.jsonl")); err != nil {
		t.Fatalf("session.jsonl missing: %v", err)
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
