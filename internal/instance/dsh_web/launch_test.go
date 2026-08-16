package dsh_web

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestReadLaunchDefault(t *testing.T) {
	cfg, err := readLaunch(t.TempDir(), "/wt")
	if err != nil {
		t.Fatalf("readLaunch: %v", err)
	}
	if cfg.Mode != launchPath {
		t.Errorf("default mode = %q, want %q", cfg.Mode, launchPath)
	}
}

func TestWriteReadLaunchRoundTrip(t *testing.T) {
	dir := t.TempDir()
	want := launchConfig{Mode: launchInstall, ResolvedBin: "/opt/npm/lib/node_modules/@deepseek-ai/dsh/bin/dsh.js"}
	if err := writeLaunch(dir, "/wt", want); err != nil {
		t.Fatalf("writeLaunch: %v", err)
	}
	got, err := readLaunch(dir, "/wt")
	if err != nil {
		t.Fatalf("readLaunch: %v", err)
	}
	if got.Mode != want.Mode || got.ResolvedBin != want.ResolvedBin {
		t.Errorf("round-trip = %+v, want %+v", got, want)
	}
}

func TestResolveLaunchPathMode(t *testing.T) {
	// Override wins over LookPath; killGroup must be false.
	exe, prefix, killGroup, err := resolveLaunch("/fake/dsh", launchConfig{Mode: launchPath})
	if err != nil {
		t.Fatalf("resolveLaunch: %v", err)
	}
	if exe != "/fake/dsh" || len(prefix) != 0 || killGroup {
		t.Errorf("path mode = (%q, %v, %v), want (/fake/dsh, [], false)", exe, prefix, killGroup)
	}
}

func TestResolveLaunchPathModeMissing(t *testing.T) {
	// Empty PATH: LookPath fails for both dsh and npm.
	t.Setenv("PATH", filepath.Join(t.TempDir(), "empty"))
	_, _, _, err := resolveLaunch("", launchConfig{Mode: launchPath})
	var nf *ErrDshNotFound
	if !errors.As(err, &nf) {
		t.Fatalf("err = %v, want *ErrDshNotFound", err)
	}
	if nf.NpmAvailable {
		t.Error("NpmAvailable = true with empty PATH, want false")
	}
	if nf.SuggestedPin != NpxPin {
		t.Errorf("SuggestedPin = %q, want %q", nf.SuggestedPin, NpxPin)
	}
}

func TestResolveLaunchNpxMode(t *testing.T) {
	// npx itself must be resolvable; put a fake npx on PATH.
	dir := t.TempDir()
	fakeNpx := filepath.Join(dir, "npx")
	if err := os.WriteFile(fakeNpx, []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))

	exe, prefix, killGroup, err := resolveLaunch("", launchConfig{Mode: launchNpx})
	if err != nil {
		t.Fatalf("resolveLaunch: %v", err)
	}
	if exe != "npx" {
		t.Errorf("exe = %q, want npx", exe)
	}
	if !killGroup {
		t.Error("killGroup = false, want true (npx mode)")
	}
	wantPrefix := []string{"--yes", "@deepseek-ai/dsh@" + NpxPin}
	if len(prefix) != len(wantPrefix) || prefix[0] != wantPrefix[0] || prefix[1] != wantPrefix[1] {
		t.Errorf("prefix = %v, want %v", prefix, wantPrefix)
	}
}

func TestResolveLaunchNpxModeMissingNpx(t *testing.T) {
	t.Setenv("PATH", filepath.Join(t.TempDir(), "empty"))
	_, _, _, err := resolveLaunch("", launchConfig{Mode: launchNpx})
	if err == nil {
		t.Fatal("err = nil, want npx-not-found error")
	}
	var nf *ErrDshNotFound
	if errors.As(err, &nf) {
		t.Errorf("err = %v, want non-ErrDshNotFound (npx missing is a different failure)", err)
	}
}

func TestResolveLaunchInstallMode(t *testing.T) {
	// Recorded absolute bin wins.
	exe, prefix, killGroup, err := resolveLaunch("", launchConfig{
		Mode: launchInstall, ResolvedBin: "/opt/npm/bin/dsh",
	})
	if err != nil {
		t.Fatalf("resolveLaunch: %v", err)
	}
	if exe != "/opt/npm/bin/dsh" || killGroup {
		t.Errorf("install mode = (%q, %v), want (/opt/npm/bin/dsh, false)", exe, killGroup)
	}
	_ = prefix

	// Recorded bin gone + LookPath fails → ErrDshNotFound.
	t.Setenv("PATH", filepath.Join(t.TempDir(), "empty"))
	_, _, _, err = resolveLaunch("", launchConfig{Mode: launchInstall})
	var nf *ErrDshNotFound
	if !errors.As(err, &nf) {
		t.Fatalf("err = %v, want *ErrDshNotFound", err)
	}
}

func TestWebArgs(t *testing.T) {
	args := webArgs("/tmp/restrict.yml")
	// Launcher flags (--patch) MUST precede the app flags (--host/--port):
	// the web subcommand switches to pass-through mode at the first
	// unknown option, so a trailing --patch would be rejected by the web
	// app ("unknown option '--patch'") and the server never boots.
	want := []string{"web", "--patch", "/tmp/restrict.yml", "--host", "127.0.0.1", "--port", "0"}
	if strings.Join(args, " ") != strings.Join(want, " ") {
		t.Errorf("webArgs = %v, want %v", args, want)
	}
}

func TestErrDshNotFoundMessage(t *testing.T) {
	e := &ErrDshNotFound{NpmAvailable: true, SuggestedPin: NpxPin}
	msg := e.Error()
	if !strings.Contains(msg, "dsh not found") || !strings.Contains(msg, NpxPin) {
		t.Errorf("message = %q", msg)
	}
	e2 := &ErrDshNotFound{NpmAvailable: false, SuggestedPin: NpxPin}
	if !strings.Contains(e2.Error(), "npm is not available") {
		t.Errorf("no-npm message = %q", e2.Error())
	}
}
