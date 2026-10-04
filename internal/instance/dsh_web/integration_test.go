package dsh_web

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"

	"myworktree/internal/framework"
	"myworktree/internal/store"
)

// buildMockDsh compiles testdata/dsh-mock.go and returns its path.
func buildMockDsh(t *testing.T) string {
	t.Helper()
	if _, err := exec.LookPath("go"); err != nil {
		t.Skip("go not available")
	}
	mockBin := filepath.Join(t.TempDir(), "dsh")
	build := exec.Command("go", "build", "-o", mockBin, filepath.Join("testdata", "dsh-mock.go"))
	if out, err := build.CombinedOutput(); err != nil {
		t.Fatalf("build mock dsh: %v\n%s", err, out)
	}
	return mockBin
}

// newTestManager wires a dsh_web driver (with the mock bin as DshBin)
// into a fresh framework manager.
func newTestManager(t *testing.T, mockBin string) (*framework.Manager, string) {
	t.Helper()
	wtPath := filepath.Join(t.TempDir(), "wt")
	if err := os.MkdirAll(wtPath, 0o755); err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	fs := store.FileStore{Path: filepath.Join(dir, "state.json")}
	if err := fs.Save(store.State{
		Worktrees: []store.ManagedWorktree{{ID: "wt1", Name: "wt1", Path: wtPath}},
	}); err != nil {
		t.Fatal(err)
	}
	reg := framework.NewRegistry()
	reg.Register(&Driver{DataDir: dir, DshBin: mockBin})
	mgr := framework.NewManager(reg, fs, nil)
	mgr.DataDir = dir
	mgr.Root = wtPath
	return mgr, wtPath
}

// waitRunning polls the manager until the instance reaches "running"
// with the blob's upstream port populated.
func waitRunning(t *testing.T, mgr *framework.Manager, id string) Blob {
	t.Helper()
	deadline := time.Now().Add(20 * time.Second)
	for time.Now().Before(deadline) {
		got, err := mgr.Get(id)
		if err != nil {
			time.Sleep(200 * time.Millisecond)
			continue
		}
		if got.Status == "failed" {
			t.Fatalf("instance failed: last_error=%s", got.LastError)
		}
		if got.Status == "running" {
			var b Blob
			if json.Unmarshal(got.KindBlob, &b) == nil && b.Port != "" {
				return b
			}
		}
		time.Sleep(200 * time.Millisecond)
	}
	t.Fatal("instance did not become ready within 20s")
	return Blob{}
}

func TestStartDshWebEndToEnd(t *testing.T) {
	mockBin := buildMockDsh(t)
	mgr, wtPath := newTestManager(t, mockBin)

	inst, err := mgr.Start(context.Background(), framework.StartParams{
		WorktreeID: "wt1",
		Kind:       "dsh-web",
		Name:       "e2e-test",
	})
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	defer func() { _ = mgr.Stop(inst.ID) }()

	blob := waitRunning(t, mgr, inst.ID)

	// The mock serves GET / with 200 (the health probe target).
	resp, err := http.Get("http://" + blob.Host + ":" + blob.Port + "/")
	if err != nil {
		t.Fatalf("GET /: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("GET / status = %d, want 200", resp.StatusCode)
	}

	// PR 2 has no ProxyStarter: blob must carry no proxy fields.
	if blob.ProxyPort != "" || blob.IframeURL != "" {
		t.Errorf("unexpected proxy fields in blob: %+v", blob)
	}
	if !blob.OverlayVerified {
		t.Error("OverlayVerified = false, want true (mock dump carries the overlay rows)")
	}
	if blob.Version != "0.2.0" {
		t.Errorf("Version = %q, want 0.2.0 (parsed core of 0.2.0-rc.2)", blob.Version)
	}
	if !blob.VersionSupported {
		t.Error("VersionSupported = false, want true")
	}

	// The whole point of the workspace/create bootstrap: the worktree's
	// own workspace id (mock returns a stable workspaceId per path) must
	// land in the blob — a flat-payload bootstrap regression would leave
	// this empty.
	if blob.WorkspaceID == "" {
		t.Error("bootstrap did not populate WorkspaceID")
	}
	if wantWS := "mock-ws-" + shortHash(wtPath); blob.WorkspaceID != wantWS {
		t.Errorf("WorkspaceID = %q, want %q", blob.WorkspaceID, wantWS)
	}

	// Stop: the instance exits and the upstream port is released.
	if err := mgr.Stop(inst.ID); err != nil {
		t.Fatalf("Stop: %v", err)
	}
	got, err := mgr.Get(inst.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.Status != "stopped" {
		t.Errorf("status after Stop = %q, want stopped", got.Status)
	}
	conn, err := net.DialTimeout("tcp", net.JoinHostPort(blob.Host, blob.Port), 500*time.Millisecond)
	if err == nil {
		conn.Close()
		t.Error("upstream port still accepting connections after Stop")
	}
}

func TestStartDshWebVersionGateFails(t *testing.T) {
	// A mock that reports a 0.1.x version must fail the hard gate at
	// Start with a readable error (not a 60s ready-timeout). 0.1.x is
	// out of support: its dotted-RPC wire is not addressable at all.
	dir := t.TempDir()
	oldMock := filepath.Join(dir, "dsh-old")
	script := "#!/bin/sh\nif [ \"$1\" = \"--version\" ]; then echo 0.1.9; exit 0; fi\nexit 1\n"
	if err := os.WriteFile(oldMock, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	mgr, _ := newTestManager(t, oldMock)

	_, err := mgr.Start(context.Background(), framework.StartParams{
		WorktreeID: "wt1",
		Kind:       "dsh-web",
		Name:       "gate-test",
	})
	if err == nil {
		t.Fatal("Start succeeded, want hard-gate error")
	}
	if !strings.Contains(err.Error(), "0.2.0") {
		t.Errorf("error = %q, want mention of the minimum version", err.Error())
	}
}

func TestStartDshWebMissingBinary(t *testing.T) {
	// PATH without dsh → ErrDshNotFound surfaced from Start.
	emptyDir := filepath.Join(t.TempDir(), "empty")
	if err := os.MkdirAll(emptyDir, 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", emptyDir)
	mgr, _ := newTestManager(t, "") // no DshBin override → LookPath

	_, err := mgr.Start(context.Background(), framework.StartParams{
		WorktreeID: "wt1",
		Kind:       "dsh-web",
		Name:       "missing-test",
	})
	if err == nil {
		t.Fatal("Start succeeded, want ErrDshNotFound")
	}
	var nf *ErrDshNotFound
	if !errors.As(err, &nf) {
		t.Fatalf("err = %v, want *ErrDshNotFound", err)
	}
}

// TestNpxModeKillsProcessGroup pins the npx-launch cleanup contract:
// a fake npx wrapper (exec'ing the mock dsh) plus Setpgid must leave
// NO process alive after Stop — killing only the npx PID would orphan
// the child holding the port.
func TestNpxModeKillsProcessGroup(t *testing.T) {
	mockBin := buildMockDsh(t)

	// Fake npx: drop "--yes <pkg>" and exec the mock dsh in the same
	// process group (exec replaces the image, so the group's leader
	// becomes the mock — exactly like real npx running dsh as a child
	// in the same group).
	binDir := t.TempDir()
	fakeNpx := filepath.Join(binDir, "npx")
	wrapper := "#!/bin/sh\nshift 2\nexec \"$MOCK_DSH\" \"$@\"\n"
	if err := os.WriteFile(fakeNpx, []byte(wrapper), 0o755); err != nil {
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
	reg := framework.NewRegistry()
	drv := &Driver{DataDir: dir}
	reg.Register(drv)
	mgr := framework.NewManager(reg, fs, nil)
	mgr.DataDir = dir
	mgr.Root = wtPath

	// Persist the npx launch mode for this worktree BEFORE Start (the
	// dialog flow writes the per-worktree launch.json).
	if err := writeLaunch(dir, wtPath, launchConfig{Mode: launchNpx}); err != nil {
		t.Fatal(err)
	}

	inst, err := mgr.Start(context.Background(), framework.StartParams{
		WorktreeID: "wt1",
		Kind:       "dsh-web",
		Name:       "npx-test",
	})
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	blob := waitRunning(t, mgr, inst.ID)

	// Find the npx process (the cmd leader) and its group.
	got, err := mgr.Get(inst.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.PID <= 0 {
		t.Fatalf("PID not recorded: %d", got.PID)
	}
	pid := got.PID

	if err := mgr.Stop(inst.ID); err != nil {
		t.Fatalf("Stop: %v", err)
	}

	// Neither the leader nor its process group may exist any more.
	if err := syscall.Kill(pid, 0); err == nil {
		t.Errorf("npx leader pid %d still alive after Stop", pid)
	}
	if err := syscall.Kill(-pid, 0); err == nil {
		t.Errorf("process group %d still alive after Stop", pid)
	}
	conn, err := net.DialTimeout("tcp", net.JoinHostPort(blob.Host, blob.Port), 500*time.Millisecond)
	if err == nil {
		conn.Close()
		t.Error("upstream port still accepting connections after Stop")
	}
}

// TestDshWebProxyEndToEnd wires the full PR-3 stack — per-instance
// loopback proxy, bootstrap injection, scope recording — around the
// mock upstream: the instance becomes ready, the proxy serves the SPA,
// the bootstrap adopts the worktree (blob WorkspaceID), an
// out-of-scope RPC through the proxy is recorded, and Stop releases
// both ports.
func TestDshWebProxyEndToEnd(t *testing.T) {
	mockBin := buildMockDsh(t)

	wtPath := filepath.Join(t.TempDir(), "wt")
	if err := os.MkdirAll(wtPath, 0o755); err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	fs := store.FileStore{Path: filepath.Join(dir, "state.json")}
	if err := fs.Save(store.State{
		Worktrees: []store.ManagedWorktree{{ID: "wt1", Name: "wt1", Path: wtPath}},
	}); err != nil {
		t.Fatal(err)
	}
	tracker := NewScopeTracker()
	drv := &Driver{DataDir: dir, DshBin: mockBin, Tracker: tracker}
	drv.Proxy = ProxyConfig{BindHost: "127.0.0.1"}
	drv.ProxyStarter = drv.ProxyStarterFn()
	reg := framework.NewRegistry()
	reg.Register(drv)
	mgr := framework.NewManager(reg, fs, nil)
	mgr.DataDir = dir
	mgr.Root = wtPath

	inst, err := mgr.Start(context.Background(), framework.StartParams{
		WorktreeID: "wt1",
		Kind:       "dsh-web",
		Name:       "proxy-e2e",
	})
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	defer func() { _ = mgr.Stop(inst.ID) }()

	blob := waitRunning(t, mgr, inst.ID)
	if blob.ProxyPort == "" || blob.IframeURL == "" {
		t.Fatalf("proxy fields missing from blob: %+v", blob)
	}

	// The proxy serves the SPA index.
	resp, err := http.Get(blob.IframeURL)
	if err != nil {
		t.Fatalf("GET via proxy: %v", err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("proxy status = %d, want 200", resp.StatusCode)
	}

	// The bootstrap adopted the worktree (the mock returns a stable
	// workspaceId per path).
	if blob.WorkspaceID == "" {
		t.Error("bootstrap did not populate WorkspaceID")
	}
	wantWS := "mock-ws-" + shortHash(wtPath)
	if blob.WorkspaceID != wantWS {
		t.Errorf("WorkspaceID = %q, want %q", blob.WorkspaceID, wantWS)
	}

	// Out-of-scope RPC through the proxy is recorded (record-only).
	body, _ := json.Marshal(map[string]any{
		"type": "client-request", "rpcId": "x", "method": "session/create",
		"payload": map[string]any{"args": map[string]any{"request": map[string]string{"cwd": "/other"}}},
	})
	// Wire contract (dsh 0.2.x): POST /api/<namespace>/<method> (the
	// endpoint is derived from the URL path).
	req, _ := http.NewRequest(http.MethodPost, blob.IframeURL+"api/session/create", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	resp, err = http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("POST via proxy: %v", err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("proxy POST status = %d, want 200 (record-only)", resp.StatusCode)
	}
	st, ok := tracker.Get(inst.ID)
	if !ok || st.Scope != ScopeOutOfScope || st.Directory != "/other" {
		t.Errorf("scope record = %+v ok=%v, want out-of-scope /other", st, ok)
	}

	// Write-verb routing coverage. The source of truth for these verb
	// names is the `@Remote('<verb>')` decorators in the dsh 0.2.x
	// remote controllers' typert.host.js; proxy.go's
	// ownSessionWriteMethods and both mocks' hand-written case lists
	// are hand-maintained mirrors of that truth and must be updated in
	// the same change. An upstream rename is NOT detected automatically
	// — every copy would go stale together and no test would turn red.
	// What these POSTs guard is internal consistency: if the proxy, the
	// mocks and this test drift apart they disagree and CI goes red — a
	// verb the proxy marks own but a mock does not route answers
	// method-not-found instead of ok:true. The one automatic pin is
	// TestProxySessionOwnMarking's bidirectional map↔table check, and
	// its table is where the exact expected verb list stays pinned by
	// literals. The loop below derives its verbs from
	// ownSessionWriteMethods precisely so this test cannot become a
	// second hand-maintained copy.
	for verb := range ownSessionWriteMethods {
		vbody, _ := json.Marshal(map[string]any{
			"type": "client-request", "rpcId": "v-" + verb, "method": verb,
			"payload": map[string]any{"args": map[string]any{"request": map[string]string{"sessionId": "s-" + verb}}},
		})
		req, _ := http.NewRequest(http.MethodPost, blob.IframeURL+"api/"+verb, bytes.NewReader(vbody))
		req.Header.Set("Content-Type", "application/json")
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatalf("POST /api/%s via proxy: %v", verb, err)
		}
		var rpc rpcResponse
		decErr := json.NewDecoder(resp.Body).Decode(&rpc)
		resp.Body.Close()
		if resp.StatusCode != http.StatusOK {
			t.Errorf("POST /api/%s status = %d, want 200 (mock must route every write verb)", verb, resp.StatusCode)
			continue
		}
		if decErr != nil || !rpc.Result.OK {
			t.Errorf("POST /api/%s not ok: decErr=%v result=%+v", verb, decErr, rpc.Result)
		}
	}

	// MOCK-ROUTING PIN (not a driver pin): this asserts that
	// testdata/dsh-mock.go registers `/api/<namespace>/<method>` ONLY — a
	// dotted `/api/session.create` has no route there and 404s at the HTTP
	// layer, exactly like the real 0.2.x gateway. It therefore pins the
	// MOCK's route table (and that this e2e harness really reaches it),
	// NOT the driver's classification behaviour: the proxy just forwards
	// whatever status upstream answers. The driver-side pin for the same
	// 0.1.x regression is the `classifyRPCBody` "legacy dotted method"
	// case in proxy_test.go, which asserts the dotted envelope classifies
	// nothing even when a route would answer 200.
	dotted, _ := json.Marshal(map[string]any{
		"type": "client-request", "rpcId": "y", "method": "session.create",
		"payload": map[string]string{"cwd": "/dotted"},
	})
	req, _ = http.NewRequest(http.MethodPost, blob.IframeURL+"api/session.create", bytes.NewReader(dotted))
	req.Header.Set("Content-Type", "application/json")
	resp, err = http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("POST dotted via proxy: %v", err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusNotFound {
		t.Errorf("dotted endpoint status = %d, want 404 (no route on dsh 0.2.x)", resp.StatusCode)
	}

	// Stop releases BOTH ports.
	if err := mgr.Stop(inst.ID); err != nil {
		t.Fatalf("Stop: %v", err)
	}
	for _, addr := range []string{
		net.JoinHostPort(blob.Host, blob.Port),
		net.JoinHostPort(blob.ProxyHost, blob.ProxyPort),
	} {
		conn, err := net.DialTimeout("tcp", addr, 500*time.Millisecond)
		if err == nil {
			conn.Close()
			t.Errorf("port %s still accepting connections after Stop", addr)
		}
	}
}

// shortHash mirrors the mock's workspaceId derivation (sha256[:4] hex).
func shortHash(s string) string {
	sum := sha256.Sum256([]byte(s))
	return hex.EncodeToString(sum[:4])
}

// runMockAccept runs the mock binary with the given web argv, waits for
// its ready line, then SIGTERMs it. Both streams go to ONE file, so the
// assertions can run after the process is reaped without racing an
// in-process buffer. It fails the test when the mock refused the argv —
// a rejection prints "unknown option …" and dies before any ready line.
func runMockAccept(t *testing.T, bin string, args []string) {
	t.Helper()
	logPath := filepath.Join(t.TempDir(), "mock.log")
	f, err := os.Create(logPath)
	if err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command(bin, args...)
	cmd.Stdout = f
	cmd.Stderr = f
	if err := cmd.Start(); err != nil {
		_ = f.Close()
		t.Fatalf("start %s %v: %v", bin, args, err)
	}
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		if b, err := os.ReadFile(logPath); err == nil && strings.Contains(string(b), "dsh web: http://") {
			break
		}
		time.Sleep(50 * time.Millisecond)
	}
	_ = cmd.Process.Signal(syscall.SIGTERM)
	waitErr := cmd.Wait()
	_ = f.Close()
	b, _ := os.ReadFile(logPath)
	logged := string(b)
	if !strings.Contains(logged, "dsh web: http://") {
		t.Fatalf("mock did NOT boot on %v (exit=%v); output:\n%s", args, waitErr, logged)
	}
	if strings.Contains(logged, "unknown option") {
		t.Errorf("mock complained about a recognised argv %v: %s", args, logged)
	}
}

// runMockReject runs the mock with a bogus web argv and asserts it fails
// the way commander does: non-zero exit, `unknown option '<flag>'` on
// stderr, and NO ready line (the real server never boots).
//
// The bounded wait is load-bearing, not hygiene: a mock that WRONGLY
// accepts the argv falls through to serve(), whose HTTP server never
// exits, so an unbounded cmd.Run() would HANG the whole suite (rescued
// only by go test's 10m default panic) instead of reporting the broken
// flag contract. A deadline therefore converts the false-accept into a
// clear failure at the point it happens.
func runMockReject(t *testing.T, bin string, args []string, bogusFlag string) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	var stdout, stderr bytes.Buffer
	cmd := exec.CommandContext(ctx, bin, args...)
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	err := cmd.Run()
	if ctx.Err() == context.DeadlineExceeded {
		t.Fatalf("mock never exited on the bogus flag %s in %v — it ACCEPTED the argv and booted (stdout:\n%s)",
			bogusFlag, args, stdout.String())
	}
	if err == nil {
		t.Fatalf("mock ACCEPTED the bogus flag %s in %v; stdout:\n%s", bogusFlag, args, stdout.String())
	}
	if want := "unknown option '" + bogusFlag + "'"; !strings.Contains(stderr.String(), want) {
		t.Errorf("stderr = %q, want it to contain %q", stderr.String(), want)
	}
	if strings.Contains(stdout.String(), "dsh web: http://") {
		t.Errorf("mock printed a ready line while rejecting %v: %s", bogusFlag, stdout.String())
	}
}

// TestMockDshWebFlagContract pins the mock's WEB FLAG SET — the contract
// that keeps testdata/dsh-mock.go and testdata/dsh-mock-modern.go from
// drifting into a permissive yes-man. Before it, both mocks ignored
// unknown argv, so a flag the real dsh never learned (--no-open renamed
// or dropped, or --patch pushed behind an app-level option and therefore
// forwarded verbatim to the web app — see launch.go's ORDER MATTERS note)
// would keep every integration test green while every real Start died on
// commander's "unknown option".
//
// Positive half: the EXACT argv the launcher emits — webArgs (a boot, so
// the ready line must appear) and dumpArgs (boot-free) — plus
// --trusted-host, whose VALUE must be skipped, not validated as a flag.
// Negative half: a renamed flag, a bogus flag sitting AFTER the
// values of --patch/--host/--port (which must not be mistaken for
// options themselves), and an option-like token immediately after the
// variadic --trusted-host (the old single-value skip swallowed it as a
// value; the variadic loop rejects it, like commander).
func TestMockDshWebFlagContract(t *testing.T) {
	overlay := filepath.Join(t.TempDir(), "restrict.yml")
	if err := os.WriteFile(overlay, []byte("web:\n  storages:\n    root: /tmp/storages\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	// Both mocks carry the same contract; the modern one is the
	// token-gated variant used by the remote e2e (integration_remote_test.go).
	for _, m := range []struct {
		name string
		bin  string
	}{
		{"dsh-mock", buildMockDsh(t)},
		{"dsh-mock-modern", buildMockDshModern(t)},
	} {
		bin := m.bin
		t.Run(m.name+"/webArgs argv boots", func(t *testing.T) {
			runMockAccept(t, bin, webArgs(overlay))
		})
		t.Run(m.name+"/dumpArgs argv accepted", func(t *testing.T) {
			var stdout, stderr bytes.Buffer
			cmd := exec.Command(bin, dumpArgs(overlay)...)
			cmd.Stdout = &stdout
			cmd.Stderr = &stderr
			if err := cmd.Run(); err != nil {
				t.Fatalf("dumpArgs %v rejected: %v\n%s\n%s", dumpArgs(overlay), err, stdout.String(), stderr.String())
			}
			if !strings.Contains(stdout.String(), "# == dump") {
				t.Errorf("dump output = %q, want the composed-tree dump", stdout.String())
			}
			if strings.Contains(stderr.String(), "unknown option") {
				t.Errorf("dumpArgs flagged unknown option: %s", stderr.String())
			}
		})
		t.Run(m.name+"/trusted-host value is skipped", func(t *testing.T) {
			runMockAccept(t, bin, append(webArgs(overlay), "--trusted-host", "10.0.0.5"))
		})
		t.Run(m.name+"/trusted-host accepts multiple authorities", func(t *testing.T) {
			// This pins the accepted argv FORM: `dsh web --trusted-host
			// <authority…>` is repeatable on 0.2.x, so several
			// authorities must all be accepted. The accepted-argv SET
			// is identical for every argv the driver emits and every
			// shape pinned by this subtest — a bare (non-`--`) token
			// falls through the generic positional `continue` branch
			// either way. The ONE corner that changed: an option-like
			// token immediately after `--trusted-host`. The old
			// single-value skip swallowed it as the flag's value and
			// never validated it, so a misspelled flag could hide
			// behind `--trusted-host`; the new variadic loop stops
			// consuming at the next `--` option and rejects it like
			// commander does — a genuine (small) hole closed, pinned
			// by the negative case below.
			runMockAccept(t, bin, append(webArgs(overlay), "--trusted-host", "10.0.0.5", "10.0.0.6", "hub.internal"))
		})
		t.Run(m.name+"/trusted-host followed by bogus option rejected", func(t *testing.T) {
			// The discriminating negative for the variadic model: an
			// option-like token right after --trusted-host is not one
			// of its values — the variadic consumption stops at the
			// next `--` option and validation rejects the token. The
			// old single-value `i++` skip accepted this by swallowing
			// it as the value.
			runMockReject(t, bin,
				append(webArgs(overlay), "--trusted-host", "--bogus"),
				"--bogus")
		})
		t.Run(m.name+"/renamed no-open rejected", func(t *testing.T) {
			runMockReject(t, bin,
				[]string{"web", "--patch", overlay, "--host", "127.0.0.1", "--port", "0", "--no-browser"},
				"--no-browser")
		})
		t.Run(m.name+"/bogus flag after values rejected", func(t *testing.T) {
			runMockReject(t, bin,
				append(webArgs(overlay), "--allow-origin", "*"),
				"--allow-origin")
		})
	}
}
