package dsh_web

import (
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"myworktree/internal/framework"
	"myworktree/internal/store"
)

func TestExtractListeningAddress(t *testing.T) {
	cases := []struct {
		in    string
		host  string
		port  string
		token string
		ok    bool
	}{
		{"dsh web: http://127.0.0.1:4096", "127.0.0.1", "4096", "", true},
		{"dsh web: http://127.0.0.1:4096\r\n", "127.0.0.1", "4096", "", true},
		{"dsh web: http://127.0.0.1:4096   ", "127.0.0.1", "4096", "", true},
		{"dsh web: http://127.0.0.1:4096 (LAN: http://192.168.1.5:4096)", "127.0.0.1", "4096", "", true},
		{"\x1b[32mdsh web: http://127.0.0.1:4096\x1b[0m", "127.0.0.1", "4096", "", true}, // ANSI
		{"dsh web: http://localhost:4096", "localhost", "4096", "", true},
		// dsh >=0.1.2 ready lines carry the browser-auth token.
		{"dsh web: http://127.0.0.1:41143/?token=abc-DEF_123", "127.0.0.1", "41143", "abc-DEF_123", true},
		{"dsh web: http://127.0.0.1:41143/?token=abc-DEF_123 (LAN: http://100.86.87.23:41143/?token=zzz)", "127.0.0.1", "41143", "abc-DEF_123", true},
		{"\x1b[32mdsh web: http://127.0.0.1:41143/?token=abc-DEF_123\x1b[0m (LAN: http://100.86.87.23:41143/?token=zzz)", "127.0.0.1", "41143", "abc-DEF_123", true}, // ANSI + LAN
		{"some other log line", "", "", "", false},
		{"", "", "", "", false},
		{"dsh web: not a url", "", "", "", false},
	}
	for _, c := range cases {
		host, port, token, ok := extractListeningAddress(c.in)
		if host != c.host || port != c.port || token != c.token || ok != c.ok {
			t.Errorf("extractListeningAddress(%q) = (%q, %q, %q, %v), want (%q, %q, %q, %v)",
				c.in, host, port, token, ok, c.host, c.port, c.token, c.ok)
		}
	}
}

// Given a ready line whose launch token starts with the literal "sk-"
// prefix (base64url can produce it) followed by >=16 word chars, When
// pumpAndWatch parses it, Then the token is captured VERBATIM — the
// redact.Text pass (\bsk-[A-Za-z0-9_-]{16,}\b -> sk-REDACTED) must
// never touch the line before parsing, or the mint would be
// permanently broken for this instance.
func TestPumpAndWatchParsesSkPrefixedTokenVerbatim(t *testing.T) {
	const tok = "sk-abcdefghijklmnop123456" // matches redact's sk- pattern after token=
	pr, pw := io.Pipe()
	h := &Handle{
		instanceID: "inst-1",
		cwd:        "/wt",
		ready:      framework.NewReadySignal(),
		wg:         &sync.WaitGroup{},
	}
	h.wg.Add(1)
	d := &Driver{}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go d.pumpAndWatch(ctx, h, pr)

	if _, err := fmt.Fprintf(pw, "dsh web: http://127.0.0.1:41143/?token=%s\n", tok); err != nil {
		t.Fatal(err)
	}
	<-h.ready.Channel()

	h.mu.Lock()
	gotTok, gotHost, gotPort := h.token, h.host, h.port
	h.mu.Unlock()
	if gotTok != tok {
		t.Errorf("token = %q, want verbatim %q (redaction must not run before parsing)", gotTok, tok)
	}
	if gotHost != "127.0.0.1" || gotPort != "41143" {
		t.Errorf("host/port = %q/%q, want 127.0.0.1/41143", gotHost, gotPort)
	}
	if a := h.upstreamAuthRelay(); a == nil || !a.Enabled() {
		t.Error("auth relay not enabled for the sk- prefixed token")
	}

	_ = pw.Close()
	h.wg.Wait()
}

// Given the spawn-gate memo pre-populated with an OLD version (dsh was
// started before an in-place upgrade), When ProbeCapability runs, Then
// it reports the binary's CURRENT version — the UI-facing probe is
// fresh and never served from verMemo.
func TestProbeCapabilityBypassesStaleMemo(t *testing.T) {
	dir := t.TempDir()
	wt := t.TempDir()
	bin := filepath.Join(t.TempDir(), "dsh")
	writeFakeVersionBin(t, bin, "0.2.0-rc.2")

	d := &Driver{DataDir: dir, DshBin: bin}
	d.verMemo = map[string]string{bin: "0.1.9"} // pre-upgrade spawn-gate entry

	c := d.ProbeCapability(wt)
	if c.Missing {
		t.Fatalf("Missing = true, want false (binary probes fine)")
	}
	if c.Version != "0.2.0" {
		t.Errorf("Version = %q, want fresh 0.2.0 (memo held 0.1.9)", c.Version)
	}
	if !c.RemoteCapable {
		t.Error("RemoteCapable = false, want true for the fresh 0.2.0")
	}
}

// Given a dsh BELOW the hard floor that still executes, When
// ProbeCapability runs, Then the version is reported with
// RemoteCapable=false and Missing stays false — capability is
// advisory; the hard gate belongs to Spawn, not to the UI report.
func TestProbeCapabilityBelowHardFloorIsAdvisory(t *testing.T) {
	dir := t.TempDir()
	wt := t.TempDir()
	bin := filepath.Join(t.TempDir(), "dsh")
	writeFakeVersionBin(t, bin, "0.1.9")

	d := &Driver{DataDir: dir, DshBin: bin}
	c := d.ProbeCapability(wt)
	if c.Missing {
		t.Fatal("Missing = true, want false (the binary resolves and probes; floors are advisory)")
	}
	if c.Version != "0.1.9" || c.RemoteCapable {
		t.Errorf("Version/RemoteCapable = %q/%v, want 0.1.9/false", c.Version, c.RemoteCapable)
	}
}

// Given a dsh binary that EXISTS but whose `--version` exec fails (exits
// 1), When ProbeCapability runs, Then the report is Missing=true — the
// resolution/probe failure path, NOT a version: an unexecutable dsh
// drives the same three-way dialog as an absent one.
func TestProbeCapabilityVersionExecFailureIsMissing(t *testing.T) {
	dir := t.TempDir()
	wt := t.TempDir()
	bin := filepath.Join(t.TempDir(), "dsh")
	if err := os.WriteFile(bin, []byte("#!/bin/sh\nexit 1\n"), 0o755); err != nil {
		t.Fatal(err)
	}

	d := &Driver{DataDir: dir, DshBin: bin}
	c := d.ProbeCapability(wt)
	if !c.Missing {
		t.Fatalf("Missing = false, want true (the --version probe failed); capability = %+v", c)
	}
	if c.Version != "" {
		t.Errorf("Version = %q, want empty on a probe failure", c.Version)
	}
	if c.RemoteCapable {
		t.Error("RemoteCapable = true, want false when missing")
	}
	if c.SuggestedPin != NpxPin {
		t.Errorf("SuggestedPin = %q, want %s", c.SuggestedPin, NpxPin)
	}
}

// Given a worktree whose persisted launch mode is npx and NO dsh binary
// present, When ProbeCapability runs, Then the report is the exact pin:
// Version == NpxPin and RemoteCapable == true — npx mode needs no probe
// because the pin is exact.
func TestProbeCapabilityNpxModeReportsPin(t *testing.T) {
	if _, err := exec.LookPath("npx"); err != nil {
		t.Skip("npx not on PATH")
	}
	dir := t.TempDir()
	wt := t.TempDir()
	if err := writeLaunch(dir, wt, launchConfig{Mode: launchNpx}); err != nil {
		t.Fatal(err)
	}

	d := &Driver{DataDir: dir, DshBin: filepath.Join(t.TempDir(), "no-such-dsh")}
	c := d.ProbeCapability(wt)
	if c.Missing {
		t.Fatalf("Missing = true, want false for npx mode (no probe needed); capability = %+v", c)
	}
	if c.Mode != "npx" {
		t.Errorf("Mode = %q, want npx", c.Mode)
	}
	if c.Version != NpxPin {
		t.Errorf("Version = %q, want the exact pin %s", c.Version, NpxPin)
	}
	if !c.RemoteCapable {
		t.Error("RemoteCapable = false, want true (the pin satisfies the remote floor)")
	}
}

// writeFakeVersionBin writes an executable dsh stub whose `--version`
// output is versionOut.
func writeFakeVersionBin(t *testing.T, path, versionOut string) {
	t.Helper()
	if err := os.WriteFile(path, []byte("#!/bin/sh\necho '"+versionOut+"'\n"), 0o755); err != nil {
		t.Fatal(err)
	}
}

func TestBuildEnv(t *testing.T) {
	// Tag env overrides inherited env; everything else passes through.
	t.Setenv("DSH_MW_TEST_KEEP", "inherited")
	env := buildEnv(map[string]string{
		"DSH_MW_TEST_KEEP": "overridden",
		"DSH_MW_TEST_NEW":  "tagged",
	})
	var keep, neu string
	for _, kv := range env {
		k, v, _ := strings.Cut(kv, "=")
		switch k {
		case "DSH_MW_TEST_KEEP":
			keep = v
		case "DSH_MW_TEST_NEW":
			neu = v
		}
	}
	if keep != "overridden" {
		t.Errorf("tag override = %q, want overridden", keep)
	}
	if neu != "tagged" {
		t.Errorf("tag env = %q, want tagged", neu)
	}

	// nil tag env is safe and keeps inherited vars.
	env2 := buildEnv(nil)
	found := false
	for _, kv := range env2 {
		if strings.HasPrefix(kv, "DSH_MW_TEST_KEEP=") {
			found = true
		}
	}
	if !found {
		t.Error("buildEnv(nil) dropped inherited variable")
	}
}

// Given a worktree persisted in npx launch mode, When Spawn boots the
// instance, Then the blob reports VersionSupported=true with the exact
// pin as its version. npx mode never probes — Spawn assigns
// version = NpxPin and versionSupported = isSupportedVersion(NpxPin)
// (driver.go), and NpxPin is the PRERELEASE "0.2.0-rc.2". Before
// isSupportedVersion core-parsed its argument, splitVersion choked on
// the "0-rc.2" segment, versionLess answered false against BOTH bounds
// and EVERY npx-mode instance shipped VersionSupported=false — the
// permanent red "dsh version is too new or the restrict overlay is not
// effective" bar (kinds/dsh_web.js) on the missing-dependency dialog's
// primary fallback path. The literal in the last assertion is the pin's
// pin: a future NpxPin bump must not silently re-break npx mode (bump
// it to a version this test rejects, and it fails here first).
func TestSpawnNpxModeReportsVersionSupported(t *testing.T) {
	mockBin := buildMockDsh(t) // skips when the go toolchain is unavailable

	// Fake npx on PATH: drop the "--yes <pkg>" prefix and exec the mock
	// dsh (exec keeps the process group, exactly like TestNpxModeKills-
	// ProcessGroup). npx itself only has to resolve — resolveLaunch
	// LookPaths it and hands the web invocation to it verbatim.
	binDir := t.TempDir()
	fakeNpx := filepath.Join(binDir, "npx")
	if err := os.WriteFile(fakeNpx, []byte("#!/bin/sh\nshift 2\nexec \"$MOCK_DSH\" \"$@\"\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("MOCK_DSH", mockBin)
	t.Setenv("PATH", binDir+string(os.PathListSeparator)+os.Getenv("PATH"))

	dir := t.TempDir()
	wtPath := filepath.Join(t.TempDir(), "wt")
	if err := os.MkdirAll(wtPath, 0o755); err != nil {
		t.Fatal(err)
	}
	fs := store.FileStore{Path: filepath.Join(dir, "state.json")}
	if err := fs.Save(store.State{
		Worktrees: []store.ManagedWorktree{{ID: "wt1", Name: "wt1", Path: wtPath}},
	}); err != nil {
		t.Fatal(err)
	}
	drv := &Driver{DataDir: dir}
	reg := framework.NewRegistry()
	reg.Register(drv)
	mgr := framework.NewManager(reg, fs, nil)
	mgr.DataDir = dir
	mgr.Root = wtPath

	// The missing-dependency dialog's npx choice, persisted per worktree.
	if err := writeLaunch(dir, wtPath, launchConfig{Mode: launchNpx}); err != nil {
		t.Fatal(err)
	}

	inst, err := mgr.Start(context.Background(), framework.StartParams{
		WorktreeID: "wt1",
		Kind:       "dsh-web",
		Name:       "npx-version-pin",
	})
	if err != nil {
		t.Fatalf("Start (npx mode): %v", err)
	}
	defer func() { _ = mgr.Stop(inst.ID) }()

	blob := waitRunning(t, mgr, inst.ID)
	if blob.Version != NpxPin {
		t.Errorf("Version = %q, want the exact pin %s (npx mode records the pin, never a probe)", blob.Version, NpxPin)
	}
	if !blob.VersionSupported {
		t.Errorf("VersionSupported = false, want true for the pinned npx launch %s", NpxPin)
	}
	if !blob.RemoteCapable {
		t.Errorf("RemoteCapable = false, want true for the pinned npx launch %s", NpxPin)
	}
	// Same inputs Spawn used, pinned directly against the literal.
	if NpxPin != "0.2.0-rc.2" {
		t.Errorf("NpxPin = %q, want the literal prerelease pin this mode was verified on", NpxPin)
	}
	if !isSupportedVersion("0.2.0-rc.2") {
		t.Error("isSupportedVersion(\"0.2.0-rc.2\") = false, want true (raw-prerelease regression)")
	}
}
