package reasonix

import (
	"fmt"
	"net"
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

func TestStartStopFakeServe(t *testing.T) {
	dataDir := t.TempDir()
	worktree := t.TempDir()
	d := &Driver{DataDir: dataDir, ReasonixBin: fakeServeBin(t)}
	id := "inst1"

	info, err := d.Start(StartInput{InstanceID: id, WorktreePath: worktree})
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
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
