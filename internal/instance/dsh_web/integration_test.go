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
	mgr, _ := newTestManager(t, mockBin)

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
	if blob.Version != "0.1.0" {
		t.Errorf("Version = %q, want 0.1.0 (parsed core of 0.1.0-rc.6)", blob.Version)
	}
	if !blob.VersionSupported {
		t.Error("VersionSupported = false, want true")
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
	// A mock that reports an ancient version must fail the hard gate at
	// Start with a readable error (not a 60s ready-timeout).
	dir := t.TempDir()
	oldMock := filepath.Join(dir, "dsh-old")
	script := "#!/bin/sh\nif [ \"$1\" = \"--version\" ]; then echo 0.0.9; exit 0; fi\nexit 1\n"
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
	if !strings.Contains(err.Error(), "0.1.0") {
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
		"type": "client-request", "rpcId": "x", "method": "session.create",
		"payload": map[string]string{"cwd": "/other"},
	})
	// Wire contract: POST /api/<method> (endpoint from the URL path).
	req, _ := http.NewRequest(http.MethodPost, blob.IframeURL+"api/session.create", bytes.NewReader(body))
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
