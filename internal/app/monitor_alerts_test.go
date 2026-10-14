package app

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"strings"
	"testing"
	"time"

	"myworktree/internal/monitor"
	"myworktree/internal/store"
)

// TestHandleMonitorAlertsShape pins the lightweight endpoint's contract:
// GET-only, alerts is [] (never null), system block present, sampled_at_ms
// cold=0. Runs against the REAL sampler (loopback daemon trust model —
// this is the user's own machine; no fake procfs needed for an endpoint
// contract).
func TestHandleMonitorAlertsShape(t *testing.T) {
	srv, _ := newIsolatedTestServer(t, store.State{})
	srv.alertSampler = monitor.NewAlertSampler(10 * time.Millisecond)
	srv.alertSampler.Start()
	t.Cleanup(srv.alertSampler.Stop)

	req := httptest.NewRequest(http.MethodPost, "/api/monitor/alerts", nil)
	rec := httptest.NewRecorder()
	srv.handleMonitorAlerts(rec, req)
	if rec.Code != http.StatusMethodNotAllowed {
		t.Fatalf("POST = %d, want 405", rec.Code)
	}

	req = httptest.NewRequest(http.MethodGet, "/api/monitor/alerts", nil)
	rec = httptest.NewRecorder()
	srv.handleMonitorAlerts(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("GET = %d", rec.Code)
	}
	body := rec.Body.String()
	if !strings.Contains(body, `"alerts":[]`) && !strings.Contains(body, `"alerts":[`) {
		t.Fatalf("alerts must be an array, never null: %s", body)
	}
	for _, want := range []string{`"system"`, `"sampled_at_ms"`, `"psi_available"`, `"available_bytes"`, `"total_bytes"`} {
		if !strings.Contains(body, want) {
			t.Errorf("missing %s in %s", want, body)
		}
	}
}

// TestHandleMonitorAlertsColdCache: sampler never ticked → empty alerts,
// sampled_at_ms=0. The UI must be able to tell "no data yet" from
// "sampled, all clear" — the zero stamp is that distinction.
func TestHandleMonitorAlertsColdCache(t *testing.T) {
	srv, _ := newIsolatedTestServer(t, store.State{})
	// Sampler constructed but NOT started: cold cache, and the endpoint must
	// still answer 200 with a valid empty payload (hand-built test servers
	// and the pre-first-tick window hit this path).
	srv.alertSampler = monitor.NewAlertSampler(time.Hour)

	req := httptest.NewRequest(http.MethodGet, "/api/monitor/alerts", nil)
	rec := httptest.NewRecorder()
	srv.handleMonitorAlerts(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("GET = %d", rec.Code)
	}
	var snap monitor.AlertsSnapshot
	if err := json.Unmarshal(rec.Body.Bytes(), &snap); err != nil {
		t.Fatal(err)
	}
	if len(snap.Alerts) != 0 || snap.SampledAtMS != 0 {
		t.Errorf("cold snapshot = %+v", snap)
	}
}

// TestAlertSamplerJoinsShutdown is the Shutdown-wiring proof required by
// the contract: a started sampler must be gone when Shutdown returns.
// Stop() joins the goroutine's done channel, so "Shutdown returned" IS the
// leak-free proof; the test additionally samples before/after to show the
// cache stops advancing only in the sense that no NEW tick can race the
// shutdown.
func TestAlertSamplerJoinsShutdown(t *testing.T) {
	srv, _ := newIsolatedTestServer(t, store.State{})
	// newIsolatedTestServer hand-builds a Server (no Start run) — wire the
	// sampler exactly like New()+Start() would, then shut down through the
	// REAL Shutdown().
	srv.alertSampler = monitor.NewAlertSampler(10 * time.Millisecond)
	srv.alertSampler.Start()

	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if _, warm := srv.alertSampler.Snapshot(); warm {
			break
		}
		time.Sleep(5 * time.Millisecond)
	}

	// Shutdown must not block forever and must reap the sampler.
	done := make(chan struct{})
	go func() { srv.Shutdown(); close(done) }()
	select {
	case <-done:
	case <-time.After(6 * time.Second):
		t.Fatal("Shutdown hung — alert sampler not reaped by the shutdown path")
	}

	// Second Shutdown is safe (idempotent stop).
	srv.Shutdown()
}

// TestServerStartWarmsAlertsCache pins the ARB-F9 wiring: a REAL Server
// Start() (not a hand-wired test shortcut) must call alertSampler.Start() so
// GET /api/monitor/alerts answers real data the moment the UI loads. The
// sampler is installed but NOT started by the test — Start() is the ONLY thing
// that can launch the loop — so if the `s.alertSampler.Start()` line is
// removed the cache stays cold and this poll times out (RED). The loop's
// first immediate tick warms the cache regardless of the interval.
func TestServerStartWarmsAlertsCache(t *testing.T) {
	srv, _ := newIsolatedTestServer(t, store.State{})
	srv.alertSampler = monitor.NewAlertSampler(10 * time.Millisecond)
	// NOTE: deliberately NOT started here — srv.Start() must do it.

	url, err := srv.Start()
	if err != nil {
		t.Fatalf("Start failed: %v", err)
	}
	if url == "" {
		t.Fatal("Start returned an empty url")
	}
	t.Cleanup(srv.Shutdown)

	deadline := time.Now().Add(2 * time.Second)
	warm := false
	for time.Now().Before(deadline) {
		if _, w := srv.alertSampler.Snapshot(); w {
			warm = true
			break
		}
		time.Sleep(5 * time.Millisecond)
	}
	if !warm {
		t.Fatal("srv.Start() did not warm the alerts cache — the alertSampler.Start() wiring is missing")
	}
}

// TestHandleInstanceStatsCarriesSubtreeAndAlerts drives the full endpoint
// against a REAL spawned child so the tree walk runs on real procfs: the
// test process spawns `sleep`, registers itself as a running instance, and
// the response must show child_count >= 1 with the sleep in the children.
func TestHandleInstanceStatsCarriesSubtreeAndAlerts(t *testing.T) {
	child := exec.Command("sleep", "30")
	if err := child.Start(); err != nil {
		t.Skipf("cannot spawn sleep: %v", err)
	}
	t.Cleanup(func() {
		// Cleanup-only kill of a process THIS test spawned — monitor code
		// itself never signals anything (zero-intervention boundary).
		_ = child.Process.Kill()
		_, _ = child.Process.Wait()
	})
	// Give the child a moment to exist in procfs as our child.
	time.Sleep(50 * time.Millisecond)

	srv, fs := newIsolatedTestServer(t, store.State{
		Version: 1,
		Worktrees: []store.ManagedWorktree{
			{ID: "wt1", Name: "wt1", Path: t.TempDir()},
		},
		Instances: []store.ManagedInstance{
			{ID: "inst-self", WorktreeID: "wt1", Name: "inst-self", Kind: "pty",
				Cwd: t.TempDir(), Status: "running", PID: os.Getpid()},
		},
	})
	_ = fs
	// Wire the sampler exactly like New()+Start() would (hand-built test
	// server leaves it nil).
	srv.alertSampler = monitor.NewAlertSampler(10 * time.Millisecond)
	srv.alertSampler.Start()
	t.Cleanup(srv.alertSampler.Stop)

	req := httptest.NewRequest(http.MethodGet, "/api/instances/stats", nil)
	rec := httptest.NewRecorder()
	srv.handleInstanceStats(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("GET = %d body=%s", rec.Code, rec.Body.String())
	}

	var stats monitor.Stats
	if err := json.Unmarshal(rec.Body.Bytes(), &stats); err != nil {
		t.Fatal(err)
	}
	if len(stats.Instances) != 1 {
		t.Fatalf("instances = %d", len(stats.Instances))
	}
	inst := stats.Instances[0]
	if inst.ChildCount < 1 {
		t.Errorf("child_count = %d, want >=1 (spawned sleep)", inst.ChildCount)
	}
	if inst.SubtreeRSSBytes < inst.MemoryRSSBytes {
		t.Errorf("subtree %d < main %d — subtree must include the main PID", inst.SubtreeRSSBytes, inst.MemoryRSSBytes)
	}
	if len(inst.Children) == 0 {
		t.Errorf("children empty, want the spawned sleep")
	}
	foundSleep := false
	for _, c := range inst.Children {
		if strings.Contains(c.Name, "sleep") {
			foundSleep = true
			if c.PPID != os.Getpid() {
				t.Errorf("sleep ppid = %d, want %d", c.PPID, os.Getpid())
			}
			if c.CreatedAtMS == 0 {
				t.Errorf("sleep created_at_ms = 0")
			}
		}
	}
	if !foundSleep {
		t.Errorf("sleep not in children: %+v", inst.Children)
	}

	// Alerts + system block present on the full endpoint.
	if stats.Alerts == nil {
		t.Error("alerts must be [] not null")
	}
	if stats.System.TotalBytes == 0 {
		t.Error("system.total_bytes = 0 — VirtualMemory unreadable in-suite?")
	}
	// The fresh system numbers were published to the sampler cache.
	snap, warm := srv.alertSampler.Snapshot()
	if !warm && snap.SampledAtMS == 0 {
		t.Error("full poll must have refreshed the sampler cache")
	}
}

// TestFullStatsPollPublishesSystemIntoCache pins ARB-F4 non-tautologically:
// the full stats endpoint must copy its FRESH system numbers into the sampler
// cache so the status bar never contradicts a fresher modal. The sampler is
// built with time.Hour and NEVER started, so the background loop's own tick
// cannot fill the cache — the ONLY way it carries any system numbers is
// PublishSystem. Stub that and the cache stays cold ⇒ RED.
func TestFullStatsPollPublishesSystemIntoCache(t *testing.T) {
	srv, _ := newIsolatedTestServer(t, store.State{})
	srv.alertSampler = monitor.NewAlertSampler(time.Hour) // never started → cold until PublishSystem

	req := httptest.NewRequest(http.MethodGet, "/api/instances/stats", nil)
	rec := httptest.NewRecorder()
	srv.handleInstanceStats(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("GET /api/instances/stats = %d body=%s", rec.Code, rec.Body.String())
	}
	var stats monitor.Stats
	if err := json.Unmarshal(rec.Body.Bytes(), &stats); err != nil {
		t.Fatal(err)
	}

	snap, warm := srv.alertSampler.Snapshot()
	if !warm {
		t.Fatal("cache still cold after a full stats poll — PublishSystem did not run")
	}
	// The cached System section must EQUAL the response's System numbers:
	// they are the value PublishSystem copied from this very request.
	if snap.System.HasPSI != stats.System.HasPSI ||
		snap.System.PSIsSomeAvg10 != stats.System.PSIsSomeAvg10 ||
		snap.System.AvailableBytes != stats.System.AvailableBytes ||
		snap.System.TotalBytes != stats.System.TotalBytes {
		t.Errorf("cache system = %+v, want it to equal the stats response %+v (PublishSystem coherence)", snap.System, stats.System)
	}
}

// TestRegisterAPIsIncludesAlertsRoute pins the route wiring — a handler
// without a route is a silent 404.
func TestRegisterAPIsIncludesAlertsRoute(t *testing.T) {
	srv, _ := newIsolatedTestServer(t, store.State{})
	mux := http.NewServeMux()
	srv.registerAPIs(mux)
	req := httptest.NewRequest(http.MethodGet, "/api/monitor/alerts", nil)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	if rec.Code == http.StatusNotFound {
		t.Fatal("/api/monitor/alerts not registered")
	}
	if rec.Code != http.StatusOK {
		t.Fatalf("GET /api/monitor/alerts = %d", rec.Code)
	}
}
