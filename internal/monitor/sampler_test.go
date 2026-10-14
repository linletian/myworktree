package monitor

import (
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/shirou/gopsutil/v4/mem"
)

// installFakeSystem pins ALL of the sampler's procfs seams: PSI content,
// virtual memory, and daemon RSS. Tests then drive the loop against fixed
// numbers instead of the machine's real load (which could flicker the
// thresholds and make the assertions weather-dependent). Either reader can
// be made to fail (psiErr / memErr) — the ARB-R2-F2 shape needs BOTH
// failing at once.
func installFakeSystem(t *testing.T, psiContent string, psiErr error, total, avail, daemonRSSBytes uint64, memErr error) {
	t.Helper()
	oldPSI, oldMem, oldDaemon := readPSIMemory, readVirtualMemory, daemonRSS
	t.Cleanup(func() { readPSIMemory, readVirtualMemory, daemonRSS = oldPSI, oldMem, oldDaemon })

	readPSIMemory = func() (string, error) {
		if psiErr != nil {
			return "", psiErr
		}
		return psiContent, nil
	}
	readVirtualMemory = func() (*mem.VirtualMemoryStat, error) {
		if memErr != nil {
			return nil, memErr
		}
		return &mem.VirtualMemoryStat{Total: total, Available: avail}, nil
	}
	daemonRSS = func() uint64 { return daemonRSSBytes }
}

// waitFor polls cond until timeout — the test's only concession to real
// time; the sampler loop itself is pinned to a 10ms interval here.
func waitFor(t *testing.T, timeout time.Duration, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for %s", what)
}

func TestAlertSamplerTicksAndCaches(t *testing.T) {
	installFakeSystem(t, "some avg10=47.36 avg60=38.50 avg300=20.06 total=1931789490\nfull avg10=0 avg60=0 avg300=0 total=0\n",
		nil, 100<<30, 10<<30, 2<<30, nil) // PSI 47.36 amber, avail 10% amber, daemon 2GiB amber

	s := NewAlertSampler(10 * time.Millisecond)
	s.Start()
	t.Cleanup(s.Stop) // second Stop is a no-op (sync.Once + done already closed)

	waitFor(t, 2*time.Second, "first tick to fill the cache", func() bool {
		snap, warm := s.Snapshot()
		return warm && len(snap.Alerts) > 0
	})

	snap, warm := s.Snapshot()
	if !warm {
		t.Fatal("snapshot cold after ticks")
	}
	if !snap.System.HasPSI || snap.System.PSIsSomeAvg10 != 47.36 {
		t.Errorf("system = %+v", snap.System)
	}
	if snap.System.TotalBytes != 100<<30 || snap.System.AvailableBytes != 10<<30 {
		t.Errorf("mem = %+v", snap.System)
	}
	// Background cache carries ONLY system-level signals — no subtree ids
	// (the loop never walks trees).
	for _, a := range snap.Alerts {
		if a.ID == "psi-memory" || a.ID == "available-ram" || a.ID == "daemon-rss" {
			continue
		}
		t.Errorf("background cache must not contain instance-level alert %q", a.ID)
	}
	if len(snap.Alerts) != 3 {
		t.Errorf("alerts = %v, want psi+available+daemon", alertIDs(snap.Alerts))
	}
}

// TestAlertSamplerStopsCleanly is the no-goroutine-leak proof required by
// the contract: Stop() WAITS on the loop's own done channel (closed by the
// goroutine itself), so when Stop returns the goroutine is provably gone —
// not "probably gone by now".
func TestAlertSamplerStopsCleanly(t *testing.T) {
	installFakeSystem(t, "some avg10=0 avg60=0 avg300=0 total=0\n", nil, 100<<30, 90<<30, 0, nil)

	s := NewAlertSampler(10 * time.Millisecond)
	s.Start()
	waitFor(t, 2*time.Second, "warm cache", func() bool { _, warm := s.Snapshot(); return warm })

	done := make(chan struct{})
	go func() { s.Stop(); close(done) }()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("Stop did not wait for the loop to exit — goroutine leak")
	}

	// Stopped loop must not resurrect: Start after Stop is inert, and a
	// second Stop returns immediately.
	s.Start()
	s.Stop()

	// The cache is readable after shutdown (display of a dead cache is the
	// cold-path contract, not a panic).
	if _, warm := s.Snapshot(); !warm {
		t.Error("cache lost after Stop")
	}
}

// TestAlertSamplerStopJoinsLoopExit pins the ARB-F3 join. The other Stop
// tests only catch a HANG (Stop taking too long); removing the join
// (`<-a.done`) makes Stop return FASTER and stays green, so nothing observed
// the loop's actual exit. Here the loop is parked DETERMINISTICALLY inside
// its first tick (daemonRSS blocks), so the goroutine is provably alive and
// cannot have closed `done` yet. Stop is then required to WAIT: with the join
// it stays blocked while the loop lives; remove the join and Stop returns
// immediately (right after close(a.stop)) — the assertion below fires → RED.
// It keeps the hang-guard too (the release-then-return tail).
func TestAlertSamplerStopJoinsLoopExit(t *testing.T) {
	oldDaemon := daemonRSS
	t.Cleanup(func() { daemonRSS = oldDaemon })

	var once sync.Once
	parked := make(chan struct{})
	releaseCh := make(chan struct{})
	var releaseOnce sync.Once
	release := func() { releaseOnce.Do(func() { close(releaseCh) }) }
	daemonRSS = func() uint64 {
		once.Do(func() {
			close(parked)
			<-releaseCh // park the (only) tick until the test releases it
		})
		return 0
	}

	s := NewAlertSampler(time.Hour) // first tick fires immediately; no repeats to race
	s.Start()

	select {
	case <-parked:
	case <-time.After(2 * time.Second):
		t.Fatal("loop never reached its first tick")
	}

	// Always free + join the loop, even on an early failure, so the parked
	// goroutine never leaks out of the suite.
	t.Cleanup(func() { release(); s.Stop() })

	stopped := make(chan struct{})
	go func() { s.Stop(); close(stopped) }()

	// The join proof: while the loop is provably alive (parked, done open),
	// Stop MUST NOT return. With `<-a.done` it blocks here; without it, it
	// already closed a.stop and returned, so this fires → RED.
	select {
	case <-stopped:
		t.Fatal("Stop returned while the loop was still alive — the `<-a.done` join was removed (ARB-F3)")
	case <-time.After(150 * time.Millisecond):
		// Stop is (correctly) still waiting on the live loop.
	}

	// Let the loop finish its tick, observe the closed stop channel, exit,
	// and close done — so Stop returns (the hang-guard direction).
	release()
	select {
	case <-stopped:
	case <-time.After(2 * time.Second):
		t.Fatal("Stop did not return after the loop was released — hang-guard")
	}
}

func TestAlertSamplerStopBeforeStartIsImmediate(t *testing.T) {
	installFakeSystem(t, "", errors.New("no psi"), 100<<30, 50<<30, 0, nil)
	s := NewAlertSampler(time.Hour)
	// Must not block: no goroutine was ever started, so there is no done
	// to wait on.
	s.Stop()
	s.Stop() // idempotent even before Start
	if _, warm := s.Snapshot(); warm {
		t.Error("cold sampler reported warm")
	}
}

// TestAlertSamplerStopBeforeStartPoisonStopsResurrection pins the G2
// contract: Stop marks the sampler dead EVEN IF nothing was running. Before
// the fix Stop-before-Start returned leaving no trace, and a later Start
// launched a loop nobody would ever join — in production that is a sampler
// ticking past the Shutdown that already ran. The observable proof: the
// cache must stay cold forever (a resurrected loop would warm it on its
// first immediate tick), and the later Stop must still return without
// waiting on a done channel that was never armed.
func TestAlertSamplerStopBeforeStartPoisonStopsResurrection(t *testing.T) {
	installFakeSystem(t, "some avg10=0 avg60=0 avg300=0 total=0\n", nil, 100<<30, 90<<30, 0, nil)

	s := NewAlertSampler(10 * time.Millisecond)
	s.Stop()  // stop before start
	s.Start() // must be inert
	// Several tick-intervals of real time: a resurrected loop would have
	// published a warm cache by now.
	time.Sleep(100 * time.Millisecond)
	if _, warm := s.Snapshot(); warm {
		t.Fatal("Start after Stop-before-Start resurrected the loop — the un-joinable goroutine is back")
	}

	// The daemon's shutdown Stop must still be safe and immediate (there
	// is no loop to join; a wait on the never-armed done channel hangs).
	done := make(chan struct{})
	go func() { s.Stop(); close(done) }()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("Stop hung on a sampler whose loop never ran")
	}
}

// TestAlertSamplerStartAfterStopIsInert is the other half of the G2
// contract: the normal Start→Stop order must also be terminal — a later
// Start may not advance the cache, because the join in Stop is the only
// proof that no loop survives it.
func TestAlertSamplerStartAfterStopIsInert(t *testing.T) {
	installFakeSystem(t, "some avg10=0 avg60=0 avg300=0 total=0\n", nil, 100<<30, 90<<30, 0, nil)

	s := NewAlertSampler(10 * time.Millisecond)
	s.Start()
	waitFor(t, 2*time.Second, "warm cache", func() bool { _, warm := s.Snapshot(); return warm })
	s.Stop()

	before, _ := s.Snapshot()
	s.Start() // must not resurrect
	time.Sleep(100 * time.Millisecond)
	after, _ := s.Snapshot()
	if after.SampledAtMS != before.SampledAtMS {
		t.Errorf("cache advanced after Stop+Start (%d -> %d) — Start resurrected a stopped sampler",
			before.SampledAtMS, after.SampledAtMS)
	}
	s.Stop() // second Stop returns immediately (done already closed)
}

func TestAlertSamplerPSIUnavailableSkipsSignal(t *testing.T) {
	// macOS / PSI-off kernel: the read fails, HasPSI stays false, the
	// available-RAM signal still stands (21:48 incident: kernel OOM
	// WITHOUT oomd — PSI must never be the only light).
	installFakeSystem(t, "", errors.New("no such file"), 100<<30, 10<<30, 0, nil)
	s := NewAlertSampler(10 * time.Millisecond)
	s.Start()
	t.Cleanup(s.Stop)

	waitFor(t, 2*time.Second, "tick without PSI", func() bool { _, warm := s.Snapshot(); return warm })
	snap, _ := s.Snapshot()
	if snap.System.HasPSI {
		t.Error("HasPSI true despite failing reader")
	}
	if len(snap.Alerts) != 1 || snap.Alerts[0].ID != "available-ram" {
		t.Errorf("alerts = %v, want only available-ram", alertIDs(snap.Alerts))
	}
}

// TestAlertSamplerReadsProcfsOutsideTheLock is the G9 proof: a.mu guards
// the cache, never the syscall. The seam itself calls back into Snapshot
// mid-read — if the caller held the lock across daemonRSS, that callback
// would block for the full timeout and the flag fires. tick runs
// synchronously here (same-package test), so the critical section is
// deterministic, not a race against a real ticker.
func TestAlertSamplerReadsProcfsOutsideTheLock(t *testing.T) {
	oldPSI, oldMem, oldDaemon := readPSIMemory, readVirtualMemory, daemonRSS
	t.Cleanup(func() { readPSIMemory, readVirtualMemory, daemonRSS = oldPSI, oldMem, oldDaemon })
	readPSIMemory = func() (string, error) { return "some avg10=0 avg60=0 avg300=0 total=0\n", nil }
	readVirtualMemory = func() (*mem.VirtualMemoryStat, error) {
		return &mem.VirtualMemoryStat{Total: 100 << 30, Available: 90 << 30}, nil
	}

	var s *AlertSampler
	var invoked, blockedUnderLock atomic.Bool
	daemonRSS = func() uint64 {
		invoked.Store(true)
		joined := make(chan bool, 1)
		go func() { _, warm := s.Snapshot(); joined <- warm }()
		select {
		case <-joined: // readable mid-syscall ⇒ the lock was NOT held
		case <-time.After(500 * time.Millisecond):
			blockedUnderLock.Store(true)
		}
		return 2 << 30
	}

	s = NewAlertSampler(time.Hour)
	s.tick()
	s.PublishSystem(SystemStat{TotalBytes: 100 << 30, AvailableBytes: 90 << 30})

	if !invoked.Load() {
		t.Fatal("daemonRSS seam never invoked — this test would pass vacuously")
	}
	if blockedUnderLock.Load() {
		t.Fatal("Snapshot blocked while daemonRSS ran — the /proc read happens under a.mu (review G9)")
	}
}

func TestAlertSamplerPublishSystemRefreshesCache(t *testing.T) {
	installFakeSystem(t, "some avg10=1 avg60=1 avg300=1 total=1\n", nil, 100<<30, 90<<30, 0, nil)
	s := NewAlertSampler(time.Hour) // no natural tick — PublishSystem must be the refresher
	s.Start()
	t.Cleanup(s.Stop)
	waitFor(t, 2*time.Second, "first tick", func() bool { _, warm := s.Snapshot(); return warm })

	s.PublishSystem(SystemStat{PSIsSomeAvg10: 55, HasPSI: true, AvailableBytes: 9 << 30, TotalBytes: 100 << 30})
	snap, _ := s.Snapshot()
	if snap.System.PSIsSomeAvg10 != 55 || !snap.System.HasPSI {
		t.Errorf("PSI not published: %+v", snap.System)
	}
	found := false
	for _, a := range snap.Alerts {
		if a.ID == "psi-memory" && a.Severity == SeverityRed {
			found = true
		}
	}
	if !found {
		t.Errorf("published PSI 55 must evaluate to red psi-memory: %v", alertIDs(snap.Alerts))
	}
}

// TestAlertSamplerBlindRefreshStaysCold pins ARB-R2-F2: when BOTH readers
// fail, NOTHING was sampled, so the refresh must leave the cache COLD —
// an unconditional `a.stamped = time.Now()` here turns EvaluateAlerts over
// the all-zero system into zero alerts and Snapshot into a timestamped
// "no alerts" all-clear built purely out of unreadable silence (the ARB-F6
// UI gate would paint it as fresh information). Reverting the guard makes
// this RED. tick runs synchronously (same-package test), so the outcome
// is deterministic rather than a race against the ticker.
func TestAlertSamplerBlindRefreshStaysCold(t *testing.T) {
	installFakeSystem(t, "", errors.New("no /proc/pressure/memory"), 0, 0, 0, errors.New("no /proc/meminfo"))

	s := NewAlertSampler(time.Hour) // no natural tick — drive it by hand
	s.tick()

	snap, warm := s.Snapshot()
	if warm {
		t.Error("blind refresh stamped the cache warm — unreadable silence dressed as all-clear (ARB-R2-F2)")
	}
	if snap.SampledAtMS != 0 {
		t.Errorf("sampled_at_ms = %d, want 0 (nothing was read)", snap.SampledAtMS)
	}
	if len(snap.Alerts) != 0 {
		t.Errorf("alerts = %v, want none on a blind refresh", alertIDs(snap.Alerts))
	}

	// The guard is per-refresh, not a permanent latch: the SAME sampler
	// warms as soon as one reader succeeds again.
	readVirtualMemory = func() (*mem.VirtualMemoryStat, error) {
		return &mem.VirtualMemoryStat{Total: 100 << 30, Available: 90 << 30}, nil
	}
	s.tick()
	snap, warm = s.Snapshot()
	if !warm || snap.SampledAtMS == 0 {
		t.Error("a readable refresh after a blind one still left the cache cold")
	}
	if snap.System.TotalBytes != 100<<30 || snap.System.AvailableBytes != 90<<30 {
		t.Errorf("system = %+v, want the post-recovery sample", snap.System)
	}
}

// TestAlertSamplerSingleSignalStampsWarm is the other half of ARB-R2-F2:
// ONE readable signal is a real sample, so a PSI-only or a mem-only
// refresh must still stamp warm — the guard is "neither read", never
// "both read". The mem-only direction is the macOS / PSI-off-kernel shape
// (issue #111: the 21:48 global-OOM happened WITHOUT oomd acting, so
// available-RAM must stand alone); the psi-only direction keeps PSI
// first-class when meminfo is the unreadable one.
func TestAlertSamplerSingleSignalStampsWarm(t *testing.T) {
	t.Run("psi only", func(t *testing.T) {
		installFakeSystem(t, "some avg10=0 avg60=0 avg300=0 total=0\n", nil, 0, 0, 0, errors.New("no /proc/meminfo"))
		s := NewAlertSampler(time.Hour)
		s.tick()
		snap, warm := s.Snapshot()
		if !warm || snap.SampledAtMS == 0 {
			t.Fatal("psi-only refresh left the cache cold")
		}
		if !snap.System.HasPSI {
			t.Error("PSI not recorded by a psi-only refresh")
		}
	})
	t.Run("mem only", func(t *testing.T) {
		installFakeSystem(t, "", errors.New("no /proc/pressure/memory"), 100<<30, 90<<30, 0, nil)
		s := NewAlertSampler(time.Hour)
		s.tick()
		snap, warm := s.Snapshot()
		if !warm || snap.SampledAtMS == 0 {
			t.Fatal("mem-only refresh left the cache cold")
		}
		if snap.System.HasPSI {
			t.Error("HasPSI true despite failing reader")
		}
		if snap.System.TotalBytes != 100<<30 || snap.System.AvailableBytes != 90<<30 {
			t.Errorf("system = %+v", snap.System)
		}
	})
}

// TestWalkSubtreeNilIndexIsUnavailable pins the ARB-R2-F3 defensive guard:
// walkSubtree(pid, nil) must degrade to Unavailable=true — the same marker
// the root-miss path emits — instead of panicking on ix.known. Today the
// collector only ever passes a non-nil index (it builds one exactly when a
// walk will run — ARB-F10, mirrored by anyRunningInstance), so this pins
// the shape for the day the walk's `running && PID>0` guard is ever widened
// without that predicate: unreadable must stay unreadable, never a crash
// and never a measured zero.
func TestWalkSubtreeNilIndexIsUnavailable(t *testing.T) {
	st := walkSubtree(12345, nil)
	if !st.Unavailable {
		t.Error("walkSubtree(pid, nil) did not report Unavailable")
	}
	if st.ChildCount != 0 || st.SubtreeRSSBytes != 0 || len(st.Children) != 0 {
		t.Errorf("nil-index walk emitted numbers: %+v", st)
	}
}
