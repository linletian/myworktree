package dsh_web

import (
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"myworktree/internal/framework"
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
	writeFakeVersionBin(t, bin, "0.1.5-rc.1")

	d := &Driver{DataDir: dir, DshBin: bin}
	d.verMemo = map[string]string{bin: "0.1.2"} // pre-upgrade spawn-gate entry

	c := d.ProbeCapability(wt)
	if c.Missing {
		t.Fatalf("Missing = true, want false (binary probes fine)")
	}
	if c.Version != "0.1.5" {
		t.Errorf("Version = %q, want fresh 0.1.5 (memo held 0.1.2)", c.Version)
	}
	if !c.RemoteCapable {
		t.Error("RemoteCapable = false, want true for the fresh 0.1.5")
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
	writeFakeVersionBin(t, bin, "0.0.9")

	d := &Driver{DataDir: dir, DshBin: bin}
	c := d.ProbeCapability(wt)
	if c.Missing {
		t.Fatal("Missing = true, want false (the binary resolves and probes; floors are advisory)")
	}
	if c.Version != "0.0.9" || c.RemoteCapable {
		t.Errorf("Version/RemoteCapable = %q/%v, want 0.0.9/false", c.Version, c.RemoteCapable)
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
