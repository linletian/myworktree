package dsh_web

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"myworktree/internal/framework"
	"myworktree/internal/store"
)

// This file exercises the MODERN fake dsh variant
// (testdata/dsh-mock-modern.go): launch-token ready line, browser-auth
// cookie gate on /api/*, and the /api/remote.mux WebSocket echo — the
// full remote-access stack (auth relay + WS<->SSE bridge) end-to-end
// through spawn -> proxy. The legacy variant (dsh-mock.go +
// integration_test.go) is untouched and keeps passing its pre-existing
// assertions.

// buildMockDshModern compiles testdata/dsh-mock-modern.go and returns
// its path.
func buildMockDshModern(t *testing.T) string {
	t.Helper()
	if _, err := exec.LookPath("go"); err != nil {
		t.Skip("go not available")
	}
	mockBin := filepath.Join(t.TempDir(), "dsh-modern")
	build := exec.Command("go", "build", "-o", mockBin, filepath.Join("testdata", "dsh-mock-modern.go"))
	if out, err := build.CombinedOutput(); err != nil {
		t.Fatalf("build modern mock dsh: %v\n%s", err, out)
	}
	return mockBin
}

// readSSELine reads one line from an SSE stream, failing the test with
// the accumulated lines when the (ctx-bounded) read errors.
func readSSELine(t *testing.T, r *bufio.Reader, seen *[]string) string {
	t.Helper()
	line, err := r.ReadString('\n')
	if err != nil {
		t.Fatalf("SSE read: %v (lines so far: %q)", err, *seen)
	}
	line = strings.TrimRight(line, "\r\n")
	*seen = append(*seen, line)
	return line
}

// awaitSSELine reads until want appears (skipping blanks and other
// events), bounded by the request's context deadline.
func awaitSSELine(t *testing.T, r *bufio.Reader, seen *[]string, want string) {
	t.Helper()
	for {
		if readSSELine(t, r, seen) == want {
			return
		}
	}
}

func TestDshWebRemoteEndToEnd(t *testing.T) {
	mockBin := buildMockDshModern(t)

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
	// Remote mode: the token gate is REQUIRED for the WS<->SSE bridge to
	// exist (proxy.go: bridge only when cfg.RequireToken && remoteCapable).
	// The test client is loopback, so the mw gate itself is bypassed
	// (loopbackClient) — the gate's purpose here is enabling remote mode.
	drv.Proxy = ProxyConfig{BindHost: "127.0.0.1", RequireToken: true, AuthToken: "mw-test-token"}
	drv.ProxyStarter = drv.ProxyStarterFn()
	reg := framework.NewRegistry()
	reg.Register(drv)
	mgr := framework.NewManager(reg, fs, nil)
	mgr.DataDir = dir
	mgr.Root = wtPath

	inst, err := mgr.Start(context.Background(), framework.StartParams{
		WorktreeID: "wt1",
		Kind:       "dsh-web",
		Name:       "remote-e2e",
	})
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	defer func() { _ = mgr.Stop(inst.ID) }()

	blob := waitRunning(t, mgr, inst.ID)
	if blob.ProxyPort == "" || blob.IframeURL == "" {
		t.Fatalf("proxy fields missing from blob: %+v", blob)
	}
	if !blob.RemoteCapable {
		t.Fatalf("RemoteCapable = false, want true (mock --version %s parses to core 0.1.5 >= floor 0.1.5)", "0.1.5-rc.1")
	}
	if blob.Version != "0.1.5" {
		t.Errorf("Version = %q, want 0.1.5 (parsed core of 0.1.5-rc.1)", blob.Version)
	}
	if !blob.VersionSupported {
		t.Error("VersionSupported = false, want true")
	}
	// The bootstrap ran the workspace.create RPC DIRECTLY against the
	// gated upstream through the shared auth relay (bootstrap.go).
	wantWS := "mock-ws-" + shortHash(wtPath)
	if blob.WorkspaceID != wantWS {
		t.Errorf("WorkspaceID = %q, want %q (bootstrap through the relay)", blob.WorkspaceID, wantWS)
	}

	upstream := "http://" + net.JoinHostPort(blob.Host, blob.Port)

	// PIN (misleading-success guard): the modern stub REALLY gates —
	// direct upstream /api requests WITHOUT the cookie get 401.
	gateBody, _ := json.Marshal(map[string]any{
		"type": "client-request", "rpcId": "gate", "method": "session.create",
		"payload": map[string]string{"cwd": wtPath},
	})
	resp, err := http.Post(upstream+"/api/session.create", "application/json", bytes.NewReader(gateBody))
	if err != nil {
		t.Fatalf("direct upstream POST: %v", err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("direct upstream /api without cookie = %d, want 401 (stub gate)", resp.StatusCode)
	}
	resp, err = http.Get(upstream + "/api/remote.mux")
	if err != nil {
		t.Fatalf("direct upstream remote.mux GET: %v", err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("direct upstream /api/remote.mux without cookie = %d, want 401 (gate precedes upgrade)", resp.StatusCode)
	}
	// And the token exchange itself honors ONLY the launch token.
	noRedirect := &http.Client{CheckRedirect: func(_ *http.Request, _ []*http.Request) error {
		return http.ErrUseLastResponse
	}}
	resp, err = noRedirect.Get(upstream + "/?token=test-token")
	if err != nil {
		t.Fatalf("token exchange: %v", err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusSeeOther {
		t.Fatalf("exchange status = %d, want 303", resp.StatusCode)
	}
	if c := resp.Header.Get("Set-Cookie"); !strings.Contains(c, "dsh-auth-test=ok") {
		t.Fatalf("exchange Set-Cookie = %q, want dsh-auth-test=ok", c)
	}

	// The iframe document via the proxy: the Director mints + injects
	// the relay cookie, so the gated upstream answers 200.
	resp, err = http.Get(blob.IframeURL)
	if err != nil {
		t.Fatalf("GET / via proxy: %v", err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("proxy GET / status = %d, want 200 (relay cookie)", resp.StatusCode)
	}

	// An RPC through the proxy rides the relay cookie -> 200, and the
	// scope observation records it (record-only), exactly like legacy.
	body, _ := json.Marshal(map[string]any{
		"type": "client-request", "rpcId": "x", "method": "session.create",
		"payload": map[string]string{"cwd": "/other"},
	})
	req, _ := http.NewRequest(http.MethodPost, blob.IframeURL+"api/session.create", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	resp, err = http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("POST via proxy: %v", err)
	}
	var rpcEnv rpcResponse
	if err := json.NewDecoder(resp.Body).Decode(&rpcEnv); err != nil {
		resp.Body.Close()
		t.Fatalf("decode proxy RPC response: %v", err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("proxy POST status = %d, want 200 (relay cookie)", resp.StatusCode)
	}
	if !rpcEnv.Result.OK {
		t.Errorf("proxy RPC result.ok = false, want true (relayed through the gate)")
	}
	st, ok := tracker.Get(inst.ID)
	if !ok || st.Scope != ScopeOutOfScope || st.Directory != "/other" {
		t.Errorf("scope record = %+v ok=%v, want out-of-scope /other", st, ok)
	}

	// The WS<->SSE bridge through the proxy: GET opens the SSE downlink
	// (retry line first, then the open event once the upstream WS is
	// dialed), POST uplinks a frame, the echo comes back as SSE data.
	// Everything is bounded by ONE context deadline — never a sleep.
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	bridgeURL := blob.IframeURL + "api/remote.mux?mwbridge=1&conn=abcd1234"
	sseReq, _ := http.NewRequestWithContext(ctx, http.MethodGet, bridgeURL, nil)
	sseResp, err := http.DefaultClient.Do(sseReq)
	if err != nil {
		t.Fatalf("SSE downlink via proxy: %v", err)
	}
	defer sseResp.Body.Close()
	if sseResp.StatusCode != http.StatusOK {
		t.Fatalf("SSE downlink status = %d, want 200", sseResp.StatusCode)
	}
	if ct := sseResp.Header.Get("Content-Type"); !strings.Contains(ct, "text/event-stream") {
		t.Fatalf("SSE Content-Type = %q, want text/event-stream", ct)
	}
	reader := bufio.NewReader(sseResp.Body)
	var seen []string
	awaitSSELine(t, reader, &seen, "retry: 300000")
	awaitSSELine(t, reader, &seen, "event: open")

	upReq, _ := http.NewRequestWithContext(ctx, http.MethodPost, bridgeURL, strings.NewReader("hello"))
	upResp, err := http.DefaultClient.Do(upReq)
	if err != nil {
		t.Fatalf("bridge uplink POST: %v", err)
	}
	upResp.Body.Close()
	if upResp.StatusCode != http.StatusAccepted {
		t.Fatalf("bridge uplink status = %d, want 202", upResp.StatusCode)
	}
	awaitSSELine(t, reader, &seen, "data: hello")

	// Stop releases BOTH ports (upstream + proxy), bridge conn included.
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
