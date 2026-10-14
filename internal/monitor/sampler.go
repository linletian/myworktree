package monitor

import (
	"sync"
	"time"
)

// System-level background sampling for the status-bar alert channel (issue
// #111 P2, layered polling design: the modal polls the full endpoint every
// 2s with the tree walk; the background loop samples ONLY the cheap
// system-level signals — PSI + available RAM + daemon RSS — never the
// instance trees, so a closed modal costs ~two /proc reads per tick, not a
// forest walk).
//
// Zero-intervention: this loop reads and caches. It never acts on what it
// sees; the cached alerts are display data.

// AlertSampler is the system-level alert cache. Safe for concurrent use:
// the HTTP handler reads Snapshot while the loop writes it.
type AlertSampler struct {
	interval time.Duration

	stop     chan struct{}
	stopOnce sync.Once
	done     chan struct{}
	started  bool
	// stopped is set by EVERY Stop, started or not (review G2): the one
	// documented promise Start cannot keep with `started` alone is "every
	// call after Stop returns without resurrecting it" — Stop-before-Start
	// used to return leaving no trace, and a later Start then launched a
	// loop nobody would ever join. stopped makes the dead state sticky.
	stopped bool

	mu      sync.Mutex
	alerts  []Alert
	system  SystemStat
	stamped time.Time
}

// SystemStat is the system-level section of both stats endpoints.
//
// HasPSI=false means /proc/pressure/memory was unreadable (non-Linux, or
// PSI compiled out); PSIsSomeAvg10 is then 0 and must be ignored — the
// available-RAM signal stands on its own (the 21:48 global-OOM in issue
// #111 happened WITHOUT oomd acting, so PSI must never be the only light).
type SystemStat struct {
	PSIsSomeAvg10  float64 `json:"psi_memory_some_avg10"`
	HasPSI         bool    `json:"psi_available"`
	AvailableBytes uint64  `json:"available_bytes"`
	TotalBytes     uint64  `json:"total_bytes"`
}

// NewAlertSampler returns a sampler that has NOT started yet.
func NewAlertSampler(interval time.Duration) *AlertSampler {
	return &AlertSampler{
		interval: interval,
		stop:     make(chan struct{}),
		done:     make(chan struct{}),
	}
}

// Start launches the background loop. Idempotent like SessionWatch.Start:
// only the first call launches the goroutine; every call after Stop returns
// without resurrecting it (the daemon's shutdown path calls Stop exactly once
// and a resurrected loop would outlive the shutdown that stopped it). The
// inert-after-Stop rule holds for BOTH orders — Start→Stop→Start and
// Stop-before-Start→Start — because Stop marks the sampler dead whether or
// not a loop was ever running (review G2).
func (a *AlertSampler) Start() {
	a.mu.Lock()
	if a.started || a.stopped {
		a.mu.Unlock()
		return
	}
	a.started = true
	a.mu.Unlock()
	go a.loop()
}

func (a *AlertSampler) loop() {
	defer close(a.done)
	ticker := time.NewTicker(a.interval)
	defer ticker.Stop()
	// First sample immediately: a daemon that just started should answer
	// /api/monitor/alerts with real numbers, not an empty cache, even if
	// the first tick is still seconds away.
	a.tick()
	for {
		select {
		case <-a.stop:
			return
		case <-ticker.C:
			a.tick()
		}
	}
}

// tick samples the cheap system signals and re-evaluates system-level
// alerts. Sampling errors leave the last good values in place (a transient
// /proc hiccup must not blank the status bar); a first tick whose reads ALL
// fail publishes nothing and the cache stays cold — Snapshot still answers
// empty-but-valid, so the endpoint contract holds (ARB-R2-F2).
//
// Lock discipline (review G9): every /proc read happens BEFORE the lock is
// taken — a.mu guards the cache, never the syscall. The old shape called
// daemonRSS (and re-acquired the lock per signal) while holding it; the
// 10s cadence meant no real contention, but a lock whose critical section
// includes filesystem reads is a trap for the next caller who takes it
// seriously, so the sample-then-publish split is structural here.
func (a *AlertSampler) tick() {
	// Phase 1: procfs reads, lock-free.
	var psi float64
	var psiOK bool
	if content, err := readPSIMemory(); err == nil {
		if v, ok := parsePSIsomeAvg10(content); ok {
			psi, psiOK = v, true
		}
	}
	var avail, total uint64
	var memOK bool
	if vm, err := readVirtualMemory(); err == nil {
		avail, total, memOK = vm.Available, vm.Total, true
	}

	// ARB-R2-F2: a refresh that read NEITHER system signal must leave the
	// cache COLD. The stamp means "this system section is a fresh
	// reading"; writing it over an all-zero sample would dress unreadable
	// silence as a fresh all-clear — EvaluateAlerts over zeros yields
	// zero alerts, so the ARB-F6 UI gate would paint "no alerts" as new
	// information, which is exactly what the sampled_at_ms=0 cold contract
	// (AlertsSnapshot, docs/API.md) exists to prevent. A blind tick on an
	// already-warm cache keeps its last good values AND its last stamp (a
	// transient /proc hiccup must not blank the status bar); a blind FIRST
	// tick simply never warms, so Snapshot reports no-data. One readable
	// signal (psiOK || memOK) is a real sample and publishes normally
	// below. daemonRSS is deliberately NOT a stamp witness: its 0 is
	// indistinguishable from a failed read, so it can never prove the
	// sample happened.
	if !psiOK && !memOK {
		return
	}

	rss := a.daemonRSS()

	// Phase 2: publish under the lock — pure memory, no syscalls.
	a.mu.Lock()
	if psiOK {
		a.system.PSIsSomeAvg10 = psi
		a.system.HasPSI = true
	}
	if memOK {
		a.system.AvailableBytes = avail
		a.system.TotalBytes = total
	}
	a.alerts = EvaluateAlerts(AlertInput{
		PSIsSomeAvg10:  a.system.PSIsSomeAvg10,
		HasPSI:         a.system.HasPSI,
		AvailableBytes: a.system.AvailableBytes,
		TotalBytes:     a.system.TotalBytes,
		DaemonRSSBytes: rss,
		// No instances: the background loop never walks trees — subtree
		// signals are evaluated fresh by the full endpoint instead.
	})
	a.stamped = time.Now()
	a.mu.Unlock()
}

// daemonRSS is the daemon's own resident memory. Seam over the shared
// sampleProcess so the sampler and the full collect read the same source.
var daemonRSS = func() uint64 {
	return sampleProcess(int32(daemonPID())).rss
}

func (a *AlertSampler) daemonRSS() uint64 { return daemonRSS() }

// PublishSystem lets the FULL stats endpoint refresh the cache with its
// fresher system numbers (the modal's 2s poll carries a newer PSI reading
// than the ≤10s background tick). Instance-level alerts are deliberately NOT
// published: they belong to the full response and would go stale in the
// cache the moment the modal closes. This is the cache-coherence rule both
// endpoints document:
//
//	/api/instances/stats → everything fresh, computed per request.
//	/api/monitor/alerts  → cached system-level alerts, ≤interval old, and
//	                       no older than the last full poll (PublishSystem).
func (a *AlertSampler) PublishSystem(sys SystemStat) {
	// ARB-R2-F2: a publish carrying NO system signal at all (no PSI, no
	// RAM total) is a blind refresh — the caller read nothing, so the
	// cache must not be stamped warm, the same honesty rule tick()
	// enforces. The last good values and stamp stay untouched.
	if !sys.HasPSI && sys.TotalBytes == 0 {
		return
	}
	// daemonRSS is a /proc read — sampled BEFORE the lock, same discipline
	// as tick (review G9): the critical section stays pure memory.
	rss := a.daemonRSS()
	a.mu.Lock()
	defer a.mu.Unlock()
	if sys.HasPSI {
		a.system.PSIsSomeAvg10 = sys.PSIsSomeAvg10
		a.system.HasPSI = true
	}
	if sys.TotalBytes > 0 {
		a.system.AvailableBytes = sys.AvailableBytes
		a.system.TotalBytes = sys.TotalBytes
	}
	a.alerts = EvaluateAlerts(AlertInput{
		PSIsSomeAvg10:  a.system.PSIsSomeAvg10,
		HasPSI:         a.system.HasPSI,
		AvailableBytes: a.system.AvailableBytes,
		TotalBytes:     a.system.TotalBytes,
		DaemonRSSBytes: rss,
	})
	a.stamped = time.Now()
}

// Snapshot returns the cached alerts + system section + whether the cache
// has ever been filled (warm). Alerts is never nil (empty slice, not null)
// so the UI can iterate blindly. On a cold cache SampledAtMS is 0 and the
// UI shows no alerts rather than presenting an empty cache as a fresh
// all-clear — "never sampled" and "sampled, nothing wrong" are different
// truths and the payload must not blur them.
func (a *AlertSampler) Snapshot() (AlertsSnapshot, bool) {
	a.mu.Lock()
	defer a.mu.Unlock()
	alerts := a.alerts
	if alerts == nil {
		alerts = []Alert{}
	}
	var ms int64
	if !a.stamped.IsZero() {
		ms = a.stamped.UnixMilli()
	}
	return AlertsSnapshot{
		Alerts:      alerts,
		System:      a.system,
		SampledAtMS: ms,
	}, ms != 0
}

// AlertsSnapshot is the response body of GET /api/monitor/alerts.
type AlertsSnapshot struct {
	Alerts []Alert    `json:"alerts"`
	System SystemStat `json:"system"`
	// SampledAtMS is the Unix-ms instant of the last successful refresh;
	// 0 means "never sampled yet" — the UI treats the whole payload as
	// cold and shows no alerts rather than showing stale ones as fresh.
	SampledAtMS int64 `json:"sampled_at_ms"`
}

// Stop terminates the background loop and WAITS for the goroutine to exit
// (done is closed by the loop itself — the wait is the proof, and
// TestAlertSampler… pins it). Calling Stop before Start returns immediately
// — but it still marks the sampler stopped (review G2): a Start that arrives
// afterwards must stay inert, because a daemon whose shutdown already ran
// must not grow a loop that outlives the shutdown that stopped it.
func (a *AlertSampler) Stop() {
	if a == nil {
		return
	}
	a.mu.Lock()
	started := a.started
	a.stopped = true
	a.mu.Unlock()
	if !started {
		return // nothing to join — the trace above is the whole job
	}
	a.stopOnce.Do(func() { close(a.stop) })
	<-a.done
}
