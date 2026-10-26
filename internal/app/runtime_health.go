package app

// Runtime health: the fault safety net and the attribution instrument that
// issue #130 asked for.
//
// THE INCIDENT (issue #130). An unattended daemon ran 82.7 h and killed
// itself with `fatal error: fault`: a wild-pointer SIGSEGV
// (addr=0x28066ec1be0, code=1=SEGV_MAPERR) inside net/http's per-connection
// teardown defer — `inFlightResponse.cancelCtx()` (the issue's Go 1.26 dump
// says server.go:1911; in the local go1.25.0 tree that same defer spans
// server.go:1939-1958 with recover() at 1940 and cancelCtx() at 1947). A
// fault at a non-nil address ≥0x1000 is not a nil dereference, so
// `runtime.sigpanic` prints "unexpected fault address" and calls
// `throw("fault")` (GOROOT runtime/signal_unix.go:923-939, throw at :939;
// the `gp.paniconfault` branch at :928 is the only thing between a fault
// and the throw). The throw itself DOES print a traceback naming the
// faulting frame — that is how #130's dump could name `cancelCtx` at all
// (throw: panic.go:1085) — what it cannot do is recover: the process exits,
// and with only that one dump and no core the root cause stays undecided,
// and the plan forbids guessing at it.
//
// WHAT THIS FILE DOES — the promise, stated at exactly its real width
// (v2 arbitration, D4's unified wording). D1 arms `SetPanicOnFault` by
// default on the HTTP connection goroutines that pass through the mounted
// middleware:
//
//  1. FAULT RESCUE, handler path (D1). On an ARMED connection goroutine,
//     a fault at a non-nil address INSIDE a handler body becomes a
//     recoverable `runtime.Error` (which may carry an `Addr()` method,
//     GOROOT runtime/error.go:111); net/http's per-conn defer catches it
//     at its recover() (server.go:1940), logs `http: panic serving <addr>:
//     <err>` followed by `runtime.Stack(buf, false)` — the faulting
//     goroutine's Go stack — closes that one connection, and the daemon
//     keeps serving. Pinned by the subprocess test that runs the real
//     `Start()`, the real middleware and a real munmap-then-read SIGSEGV.
//
//  2. THE BOUNDARY, teardown path (D1, and NOT covered by the rescue). A
//     fault that happens AFTER that same defer's recover() has RETURNED —
//     the #130 `cancelCtx()` shape at server.go:1947 — cannot be caught
//     again by that defer: recover only ends the panic that is already in
//     flight (GOROOT runtime/panic.go:1138 `recovery` consumes `gp._panic`
//     once), and there is no outer recover on a conn goroutine above
//     net/http's own. So on THAT shape the flag still converts the fault
//     to a panic, but nothing catches it: the process exits with an
//     UNRECOVERED `panic:` carrying the faulting goroutine's stack —
//     exit 2, daemon dead. This file makes NO survival promise for a
//     recover-after teardown fault, and that path never prints `http:
//     panic serving`; the pinned subprocess test asserts exit 2 + `panic:`,
//     never liveness.
//
//  3. ATTRIBUTION (D2). A periodic `[runtime-health]` tick (goroutines,
//     heap, sys, GC, in-flight, cumulative requests, top endpoints) plus a
//     goroutine-count alert that dumps the aggregated goroutine profile.
//     #130 died with 48 live goroutines out of ~1.88 M created (≈6.3/s of
//     churn nobody could attribute); these lines make the next occurrence
//     self-explaining instead. The aggregated profile is a standing
//     instrument, NOT a crash traceback — it never substitutes for one.
//
// DUMP WIDTH, PINNED (D1). An armed teardown fault now exits as a Go panic
// instead of a runtime throw, and the traceback width is decided by
// GOTRACEBACK: the DEFAULT `single` prints only the panicking goroutine —
// narrower than the all-goroutine view a `fatal error:` throw can carry —
// so an unattended forensics deployment should set `GOTRACEBACK=all` (all
// ≠ `system` ≠ `crash`; use `system` for runtime/internal frames, `crash`
// for a core file). The single-vs-all difference is pinned by a subprocess
// pair over a parked marker goroutine. This code never mutates the
// operator's GOTRACEBACK.
//
// WHY THE ARMING LIVES IN THE MIDDLEWARE, NOT IN Start().
// `debug.SetPanicOnFault` is PER-GOROUTINE, not process-wide: it writes
// `gp.paniconfault` on the CALLING goroutine only (GOROOT runtime/rdebug.go
// :16-22), `runtime/debug` says so in its own doc ("SetPanicOnFault applies
// only to the current goroutine", garbage.go:153), and a new goroutine does
// NOT inherit it — `newproc1` resets the field to false on every new and
// recycled g (proc.go:4452). `Server.Start()` therefore only arms the
// goroutine that calls Start() — and `Serve` runs on ANOTHER goroutine
// (`go c.serve(ctx)` per connection, and the daemon's own `go func(){ …
// Serve(ln) …}`), so arming in Start() covers neither the accept loop nor
// any connection. Every request re-arms its own connection goroutine in
// `withPanicOnFault` and never disarms. An earlier revision of this header
// claimed a modelling experiment proved the flag "covers the teardown": that
// experiment faulted on the WRONG SIDE of the boundary — it armed a
// goroutine, returned from the arming function, and faulted later in that
// goroutine with no recover() already consumed, so of course it recovered.
// The real net/http defer consumes its recover() at :1940 BEFORE touching
// `cancelCtx()` at :1947, which is where #130 faulted; the subprocess tests
// in runtime_health_fault_test.go pin both sides of that line, not a model
// of it.
//
// THE COST, STATED HONESTLY. A fault that this turns into a panic is still
// a memory-corruption fault: `runtime/debug`'s own doc warns that such
// faults "are typically caused by bugs such as runtime memory corruption",
// and continuing after one may expose further corruption (a corrupted heap
// does not become safe because we caught the first panic). What we buy is a
// full-stack log line and a daemon that stays up for the OTHER connections
// when the fault was inside a handler; what we risk is a longer tail on a
// process that was already broken. That trade is why the switch is
// env-controlled and defaults to on only for a configuration the operator can
// flip back in one variable (D1's rollback), and why the tick (D2) is the
// part that cannot hurt anything.
//
// RESIDUAL, NOT CLOSED HERE (deliberate). Goroutines that a handler spawns
// itself (the TTY read pump, SSE pumps, the proxy pumps) are NEW goroutines
// and therefore stay un-armed; closing that gap means touching every `go`
// statement in the package, which is a wider diff than this safety net is
// worth, and the fault we are answering happened on the conn goroutine.
// No claim is made that every fault path in the process is converted.

import (
	"bytes"
	"log"
	"net/http"
	"os"
	"runtime"
	"runtime/debug"
	"runtime/pprof"
	"sort"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

// The operator knobs (issue #130). Same MYWORKTREE_* convention as the
// issue #114 knobs next door, and the same parse discipline: missing or
// unparsable lands on the default, and only an explicit value moves the
// number — a typo in a systemd unit must degrade to the shipped cadence,
// never to "no instrumentation" or to a tick storm.
const (
	// envPanicOnFault gates the fault→panic rescue (D1). DEFAULT IS ON: the
	// whole point is that an unattended daemon does not silently vanish, and
	// the population that needs it is the one that never reads a config.
	// "0" / "false" turn it off, which restores the runtime's own behaviour
	// (instant `throw("fault")` exit) — that is the documented rollback.
	envPanicOnFault = "MYWORKTREE_PANIC_ON_FAULT"

	// envRuntimeHealthIntervalMS sets the tick cadence (D2). 0 or negative
	// switches the tick loop OFF (the goroutine is never launched, so an
	// operator who wants zero background work gets zero, not a quieter
	// version of the same work). An unparsable value falls back to the
	// default rather than silently disabling the instrument.
	envRuntimeHealthIntervalMS = "MYWORKTREE_RUNTIME_HEALTH_INTERVAL_MS"

	// envRuntimeHealthGoroutineAlert sets the goroutine count that earns an
	// alert + profile dump. Default 1000: #130's daemon had 48 live
	// goroutines at death, so 1000 is not a threshold that would have fired
	// for that incident — it is the "something is leaking goroutines" light
	// for the churn the incident could not explain, set two orders of
	// magnitude above the idle floor and low enough to catch a real leak
	// while there is still a process to inspect.
	envRuntimeHealthGoroutineAlert = "MYWORKTREE_RUNTIME_HEALTH_GOROUTINE_ALERT"
)

const (
	// defaultRuntimeHealthInterval is the tick cadence (D2). 60 s is the
	// number the plan names, and the arithmetic supports it: the tick's one
	// expensive call is runtime.ReadMemStats, which DOES stop the world
	// (GOROOT go1.25.0 runtime/mstats.go:356-358, stopTheWorld at :358).
	// Measured on this tree at 201 live goroutines over 500 consecutive
	// calls: mean 137 µs, p50 134 µs, p99 275 µs, max 618 µs. At a 60 s
	// cadence that is a duty cycle of 0.00023 % — six orders of magnitude
	// below anything a terminal user can feel, and the reason the cadence
	// does not need to be event-driven. Raising the rate is the env knob's
	// job, not this constant's.
	defaultRuntimeHealthInterval = 60 * time.Second

	// defaultRuntimeHealthGoroutineAlert is the alert threshold behind
	// envRuntimeHealthGoroutineAlert — see that constant for the arithmetic.
	defaultRuntimeHealthGoroutineAlert int64 = 1000

	// runtimeHealthGoroutineDumpCooldown is the minimum spacing between two
	// goroutine-profile dumps in one process. The alert condition
	// (goroutines > threshold) is a STATE, not an event: once a leak has
	// crossed the line it stays over it, so without a cooldown every tick
	// would re-dump — at 60 s that is 1440 dumps a day of a profile that
	// barely changes, burying the one line anyone reads. 30 min keeps the
	// dump a trend sample (two per hour is plenty to see a leak's slope)
	// while capping the log growth a stuck daemon can produce.
	runtimeHealthGoroutineDumpCooldown = 30 * time.Minute

	// runtimeHealthTopEndpoints is how many per-endpoint readings ride the
	// tick and alert lines. 5 is "where is the traffic" at a glance without
	// turning the line into a report.
	runtimeHealthTopEndpoints = 5

	// runtimeHealthMaxEndpointKeys bounds the attribution map. Keys come
	// from registered ServeMux patterns, so in normal operation the set is
	// fixed at ~45 and this never fires; it exists for the fallback key,
	// which carries the request path. A scanner hammering random URLs
	// against an un-instrumented daemon must not be able to grow a map in
	// the very process this file is here to keep alive — folding starts at
	// cap−1 so the overflow bucket lands INSIDE the cap and every new key
	// past it folds into that one bucket, making len ≤ this constant an
	// invariant; the only thing lost is per-path resolution of the noise.
	// Known limitation, accepted deliberately (review B12): unauthenticated
	// random paths can occupy real keys BEFORE folding starts, and the
	// top-5 render is ordered by cumulative traffic, so a flood can crowd
	// legitimate endpoints out of the visible top-N while the cap holds.
	// The bound is the contract; a per-prefix split of the meter (auth vs
	// anon) would be a second key space and is not worth it here.
	runtimeHealthMaxEndpointKeys = 64

	// runtimeHealthOverflowKey is that single bounded bucket.
	runtimeHealthOverflowKey = "(!capped)"

	// runtimeHealthUnmatchedPrefix marks a fallback key — a request that no
	// registered pattern claims — as "(!pattern:METHOD:/path)". The
	// parenthesis form is load-bearing, not cosmetic: every field on these
	// lines is space-delimited, so a key with a space in it would break the
	// grep/awk readers the way the [tty-metrics] columns already are.
	runtimeHealthUnmatchedPrefix = "(!pattern:"
)

// panicOnFaultSetting is the resolved answer to "should a fault panic
// instead of killing the process", plus what the environment actually said.
//
// It is a struct rather than a bare bool because the operator's intent has
// to stay visible in the log: "on because you did not say otherwise" and
// "on because you said so" are different facts, and "you wrote
// MYWORKTREE_PANIC_ON_FAULT=yes and I ignored it" is a fact the log must
// carry or the knob becomes unanswerable when it matters.
type panicOnFaultSetting struct {
	// Enabled is what gets handed to debug.SetPanicOnFault.
	Enabled bool
	// Explicit reports that the variable WAS set (to anything).
	Explicit bool
	// Warning is non-empty when the value was set but unrecognised, in
	// which case Enabled fell back to the default (on).
	Warning string
}

// resolvePanicOnFault is the pure half of D1: string in, decision out, no
// environment, no runtime call, so the three branches are unit-testable
// without touching a process-global setting.
//
// Recognised spellings, after trimming: "1"/"true" (any case) enable,
// "0"/"false" (any case) disable. Anything else — "yes", "off", an empty
// value, a stray "2" — keeps the DEFAULT (on) and produces a warning,
// because the default is the safe direction for an unattended process and a
// silently-ignored typo is the failure mode this repo refuses elsewhere.
func resolvePanicOnFault(raw string, present bool) panicOnFaultSetting {
	if !present {
		return panicOnFaultSetting{Enabled: true}
	}
	switch strings.ToLower(strings.TrimSpace(raw)) {
	case "1", "true":
		return panicOnFaultSetting{Enabled: true, Explicit: true}
	case "0", "false":
		return panicOnFaultSetting{Enabled: false, Explicit: true}
	default:
		return panicOnFaultSetting{
			Enabled:  true,
			Explicit: true,
			Warning:  envPanicOnFault + "=" + strconv.Quote(raw) + " is not 0/1/true/false; keeping the default (enabled)",
		}
	}
}

// resolveRuntimeHealthInterval parses the tick cadence in milliseconds.
// Missing or unparsable → defaultRuntimeHealthInterval; ≤0 → 0, which the
// caller reads as "do not run the loop at all".
func resolveRuntimeHealthInterval(raw string, present bool) time.Duration {
	if !present {
		return defaultRuntimeHealthInterval
	}
	ms, err := strconv.ParseInt(strings.TrimSpace(raw), 10, 64)
	if err != nil {
		return defaultRuntimeHealthInterval
	}
	if ms <= 0 {
		return 0
	}
	return time.Duration(ms) * time.Millisecond
}

// resolveRuntimeHealthGoroutineAlert parses the alert threshold. Missing,
// unparsable, zero or negative → defaultRuntimeHealthGoroutineAlert. Zero
// is NOT honoured as "alert always": an alert that fires every tick is not
// an alert, and the cadence env is the honest way to ask for more samples.
func resolveRuntimeHealthGoroutineAlert(raw string, present bool) int64 {
	if !present {
		return defaultRuntimeHealthGoroutineAlert
	}
	n, err := strconv.ParseInt(strings.TrimSpace(raw), 10, 64)
	if err != nil || n <= 0 {
		return defaultRuntimeHealthGoroutineAlert
	}
	return n
}

// runtimeEndpointStat is one endpoint's attribution counters. Both are
// atomic so the request path never holds a lock across next.ServeHTTP (the
// TTY handler parks there for hours by design); the guard on `inflight`
// keeps the gauge from going negative if a leave ever ran twice.
type runtimeEndpointStat struct {
	pattern  string
	inflight atomic.Int64
	total    atomic.Int64
}

// runtimeHealth is the [runtime-health] sampler + meter. Safe for concurrent
// use: the HTTP path calls leave/enter while the loop reads the same maps.
//
// Lifecycle mirrors monitor.AlertSampler (issue #111 P2) because that shape
// is what the shutdown path proves: `done` is closed by the loop itself, so
// Stop() returning IS the no-leak evidence, and `stopped` is sticky so a
// Start after Shutdown can never resurrect a loop past the shutdown that
// stopped it.
type runtimeHealth struct {
	logger         *log.Logger
	interval       time.Duration
	goroutineAlert int64

	// dumpCooldown is the seam over runtimeHealthGoroutineDumpCooldown. It
	// is a field and not the constant so a test can watch two dumps happen
	// in milliseconds; the constant it defaults to is pinned separately
	// (TestRuntimeHealthDumpCooldownIsThirtyMinutes) so the seam cannot
	// quietly become the production value.
	dumpCooldown time.Duration
	// dumpProfile is the seam over pprof.Lookup("goroutine").WriteTo — the
	// test wants to count dumps, not parse a profile of its own process.
	dumpProfile func(w *bytes.Buffer) error

	startedAt time.Time

	mu        sync.Mutex
	endpoints map[string]*runtimeEndpointStat

	inflight atomic.Int64
	reqTotal atomic.Int64

	lastDumpNano atomic.Int64

	stop     chan struct{}
	stopOnce sync.Once
	done     chan struct{}
	started  bool
	stopped  bool
}

// newRuntimeHealth builds the sampler WITHOUT starting it. The env is read
// exactly once here, at construction, and never per request: the tick loop
// reads runtime counters, not the environment, so a mid-life env edit has no
// effect until restart — which is the right semantic for a knob whose whole
// purpose is to describe the process it configures.
func newRuntimeHealth(logger *log.Logger) *runtimeHealth {
	interval := resolveRuntimeHealthInterval(lookupRuntimeHealthEnv(envRuntimeHealthIntervalMS))
	return &runtimeHealth{
		logger:         logger,
		interval:       interval,
		goroutineAlert: resolveRuntimeHealthGoroutineAlert(lookupRuntimeHealthEnv(envRuntimeHealthGoroutineAlert)),
		dumpCooldown:   runtimeHealthGoroutineDumpCooldown,
		dumpProfile:    writeGoroutineProfile,
		startedAt:      time.Now(),
		endpoints:      map[string]*runtimeEndpointStat{},
		stop:           make(chan struct{}),
		done:           make(chan struct{}),
	}
}

// lookupRuntimeHealthEnv is the single env read point. Aliased rather than
// wrapped so the resolvers above keep a pure (raw, present) signature that
// unit tests drive directly, and the impure half is one named symbol.
var lookupRuntimeHealthEnv = os.LookupEnv

// writeGoroutineProfile writes the aggregated goroutine profile as TEXT.
//
// debug=1 is the text mode and it is load-bearing: `Profile.WriteTo` with
// debug>=1 emits the human/`grep`-able form ("goroutine profile: total N"
// then one block per distinct stack with its frames), while debug=0 emits
// gzipped protobuf — piping that into a log file would write binary bytes
// into the daemon's text log and produce a dump nobody can read at 4am
// (verified against GOROOT go1.25.0 runtime/pprof/pprof.go:366 WriteTo +
// writeGoroutine at :743, and by running WriteTo into a buffer: the output
// begins with the literal "goroutine profile: total").
func writeGoroutineProfile(w *bytes.Buffer) error {
	p := pprof.Lookup("goroutine")
	if p == nil {
		return nil
	}
	return p.WriteTo(w, 1)
}

// Start launches the tick loop. Idempotent, and inert after Stop — the same
// contract as the alert sampler next door.
//
// A zero interval (MYWORKTREE_RUNTIME_HEALTH_INTERVAL_MS<=0) launches
// nothing at all. Note what that does NOT turn off: the fault arming
// (withPanicOnFault) and the per-endpoint counting are separate switches on
// separate paths, so an operator who silences the tick does not also
// silence the fault rescue. That separation is the point of three env
// variables instead of one.
func (h *runtimeHealth) Start() {
	if h == nil || h.interval <= 0 {
		return
	}
	h.mu.Lock()
	if h.started || h.stopped {
		h.mu.Unlock()
		return
	}
	// Uptime starts HERE, at the moment the loop that reports it starts —
	// not at construction. newRuntimeHealth may run long before Start
	// (config building, TLS setup), and an uptMs that silently includes
	// that dead time would misdate every tick on the log.
	h.startedAt = time.Now()
	h.started = true
	h.mu.Unlock()
	go h.loop()
}

// loop ticks on the interval until Stop closes `stop`. The ticker is
// stopped on exit and `done` is closed by the goroutine itself, so Stop's
// wait is the proof that no tick survives Shutdown.
func (h *runtimeHealth) loop() {
	defer close(h.done)
	ticker := time.NewTicker(h.interval)
	defer ticker.Stop()
	for {
		select {
		case <-h.stop:
			return
		case <-ticker.C:
			h.tick()
		}
	}
}

// Stop terminates the tick loop and WAITS for the goroutine to exit — the
// same join contract as monitor.AlertSampler.Stop: `done` is closed by the
// loop itself, so returning IS the no-leak proof.
//
// Sticky, in both orders: Stop-before-Start marks the sampler dead without
// anything to join, and a Start that arrives afterwards stays inert — a
// daemon whose Shutdown already ran must not grow a loop that outlives the
// shutdown that stopped it (the review G2 rule the alert sampler follows,
// applied here unchanged).
//
// Called on a nil receiver (a hand-built Server that never ran New) it is
// a no-op, which is what lets Shutdown() call it unconditionally.
func (h *runtimeHealth) Stop() {
	if h == nil {
		return
	}
	h.mu.Lock()
	started := h.started
	h.stopped = true
	h.mu.Unlock()
	if !started {
		return // nothing to join — marking it dead is the whole job
	}
	h.stopOnce.Do(func() { close(h.stop) })
	<-h.done
}

// tick emits one [runtime-health] line and, if the goroutine count is over
// threshold, the alert + dump.
//
// There is deliberately no first-tick-on-start here (the alert sampler does
// that because its cache answers an endpoint; this loop answers nobody, it
// just prints). The first line therefore arrives one interval after Start,
// and uptime on it is already ≥ the cadence — the number a reader of an
// unattended daemon wants is "how long has this been up and what did it
// cost", and the tick is the honest place to state it.
func (h *runtimeHealth) tick() {
	if h == nil {
		return
	}
	goroutines := int64(runtime.NumGoroutine())
	var ms runtime.MemStats
	runtime.ReadMemStats(&ms)

	inflight, total, top := h.snapshot()
	upt := time.Since(h.startedAt)

	if h.logger != nil {
		h.logger.Printf("[runtime-health] tick uptMs=%d goroutines=%d heapMB=%d sysMB=%d gcCount=%d inflight=%d reqTotal=%d top=%s",
			upt.Milliseconds(), goroutines,
			ms.HeapAlloc>>20, ms.Sys>>20, ms.NumGC,
			inflight, total, top)
	}

	if goroutines > h.goroutineAlert {
		h.alert(goroutines, inflight, total, top)
	}
}

// alert logs the threshold crossing and, once per cooldown window, the
// aggregated goroutine profile.
//
// The LINE fires on every tick over the threshold and only the DUMP is
// cooled: the crossing is the fact a reader scans for, it costs one line,
// and suppressing it would hide whether the leak is still running. The dump
// is the expensive, mostly-repeating part — that is what the 30 min
// cooldown bounds (see runtimeHealthGoroutineDumpCooldown).
//
// The dump rides out in ONE Printf call, the way net/http's own panic log
// does it, so the logger's mutex serialises it against other lines and the
// profile cannot interleave with a concurrent tick mid-write.
func (h *runtimeHealth) alert(goroutines, inflight, total int64, top string) {
	if h.logger != nil {
		h.logger.Printf("[runtime-health] alert goroutines=%d threshold=%d inflight=%d reqTotal=%d top=%s",
			goroutines, h.goroutineAlert, inflight, total, top)
	}

	now := time.Now()
	last := h.lastDumpNano.Load()
	if last != 0 && now.Sub(time.Unix(0, last)) < h.dumpCooldown {
		return
	}
	// CAS claims the slot: two ticks cannot both decide to dump (they
	// cannot — one loop — but a second producer added later inherits the
	// guard for free rather than inheriting a race).
	if !h.lastDumpNano.CompareAndSwap(last, now.UnixNano()) {
		return
	}

	var buf bytes.Buffer
	if err := h.dumpProfile(&buf); err != nil {
		if h.logger != nil {
			h.logger.Printf("[runtime-health] goroutine-dump failed: %v", err)
		}
		return
	}
	if h.logger != nil {
		h.logger.Printf("[runtime-health] goroutine-dump bytes=%d cooldownMs=%d\n%s",
			buf.Len(), h.dumpCooldown.Milliseconds(), buf.String())
	}
}

// leave/enter bracket one request's attribution. enter returns the counter
// to decrement (nil when metering is off), so the middleware holds no key
// lookup across the handler call.
func (h *runtimeHealth) enter(key string) *runtimeEndpointStat {
	if h == nil {
		return nil
	}
	h.mu.Lock()
	st, ok := h.endpoints[key]
	if !ok {
		// Fold one slot EARLY (cap−1, not cap): the overflow bucket is
		// itself a map entry, so folding only once the map is already full
		// would insert it as entry cap+1 — the bound the constant promises
		// would be off by exactly the bucket that exists to enforce it.
		// With the early fold, every first fold creates the bucket inside
		// the cap and every later fold finds it, so len(endpoints) ≤
		// runtimeHealthMaxEndpointKeys is an invariant, not an aspiration.
		if len(h.endpoints) >= runtimeHealthMaxEndpointKeys-1 {
			// Bounded-fold path: see runtimeHealthMaxEndpointKeys. The
			// overflow bucket is created once and then lives on the fast
			// path like any other key.
			if key != runtimeHealthOverflowKey {
				key = runtimeHealthOverflowKey
			}
			st, ok = h.endpoints[key]
		}
		if !ok {
			st = &runtimeEndpointStat{pattern: key}
			h.endpoints[key] = st
		}
	}
	h.mu.Unlock()

	st.inflight.Add(1)
	st.total.Add(1)
	h.inflight.Add(1)
	h.reqTotal.Add(1)
	return st
}

func (h *runtimeHealth) leave(st *runtimeEndpointStat) {
	if st == nil {
		return
	}
	if st.inflight.Add(-1) < 0 {
		// Cannot happen through the middleware (every enter is matched by a
		// deferred leave). Repair rather than leave a gauge pinned negative,
		// where it would under-report for the life of the process.
		st.inflight.Store(0)
	}
	// The aggregate gets the SAME clamp as the per-endpoint gauge above —
	// symmetric repair, same double-leave scenario, so neither gauge can
	// under-report while the other stands corrected.
	if h.inflight.Add(-1) < 0 {
		h.inflight.Store(0)
	}
}

// snapshot sums the aggregates and renders the top-N endpoint readings as
// `pattern inflight/total`, ordered by cumulative traffic (the leak
// question), then by what is parked right now, then by name so two runs of
// the same state print identically.
func (h *runtimeHealth) snapshot() (inflight, total int64, top string) {
	if h == nil {
		return 0, 0, "()"
	}
	inflight = h.inflight.Load()
	total = h.reqTotal.Load()

	type row struct {
		pattern  string
		inflight int64
		total    int64
	}
	h.mu.Lock()
	rows := make([]row, 0, len(h.endpoints))
	for _, st := range h.endpoints {
		rows = append(rows, row{st.pattern, st.inflight.Load(), st.total.Load()})
	}
	h.mu.Unlock()

	sort.Slice(rows, func(i, j int) bool {
		if rows[i].total != rows[j].total {
			return rows[i].total > rows[j].total
		}
		if rows[i].inflight != rows[j].inflight {
			return rows[i].inflight > rows[j].inflight
		}
		return rows[i].pattern < rows[j].pattern
	})
	if len(rows) > runtimeHealthTopEndpoints {
		rows = rows[:runtimeHealthTopEndpoints]
	}

	var b strings.Builder
	b.WriteByte('(')
	for i, r := range rows {
		if i > 0 {
			b.WriteString(", ")
		}
		b.WriteString(r.pattern)
		b.WriteByte(' ')
		b.WriteString(strconv.FormatInt(r.inflight, 10))
		b.WriteByte('/')
		b.WriteString(strconv.FormatInt(r.total, 10))
	}
	b.WriteByte(')')
	return inflight, total, b.String()
}

// runtimeHealthKey resolves the attribution key for ONE request, ahead of
// dispatch.
//
// Why this is not just `r.Pattern`, which is what the plan says.
// `r.Pattern` is written by `ServeMux.ServeHTTP` itself (GOROOT
// net/http/server.go:2859 — `h, r.Pattern, r.pat, r.matches =
// mux.findHandler(r)`), so it is populated only INSIDE the mux. This
// middleware is mounted at `http.Server.Handler`, OUTSIDE `s.mux`, because
// the plan (rightly) refuses to touch the registrations — and a wrapper
// outside the mux sees an empty `r.Pattern` at the moment it has to take
// the in-flight gauge. Verified by running it: a wrapper around a mux prints
// `pattern=""` before dispatch and `pattern="/api/instances/log"` after.
// Waiting for the return value would attribute a 6-hour TTY socket to a
// placeholder while it is in flight, which is the exact opposite of the
// instrument's purpose.
//
// So the resolution order is:
//  1. `r.Pattern` if it is already set — free, and correct for any future
//     mount point that sits inside a mux.
//  2. `mux.Handler(r)` — the public, NON-mutating lookup that returns the
//     registered pattern ServeMux is about to dispatch to ("Handler does not
//     modify its argument", GOROOT go1.25.0 net/http/server.go:2680-2683).
//     One extra routing-tree walk per request, the same walk ServeHTTP is
//     about to do anyway.
//  3. `(!pattern:METHOD:/path)` for a request no pattern claims (a 404, or
//     a GODEBUG=httpmuxgo121=1 build where patterns are empty by
//     construction) — bounded by the map cap above.
//
// The path in the fallback key is `r.URL.EscapedPath()`, NOT `r.URL.Path`,
// and that is a security contract, not cosmetics: `URL.Path` is the
// DECODED path, so an unauthenticated request for `/a%0A[runtime-health]%20
// tick fake=1` would put a literal space, a literal newline and a forged
// `[runtime-health]` token into the log line — breaking the space-free key
// contract (b) and injecting fake lines, since this middleware runs OUTSIDE
// withAuth. EscapedPath re-emits the percent form (or the RawPath when the
// encoding matches), so every byte below 0x20 and above 0x7E, plus space,
// stays percent-escaped and the key remains exactly one whitespace-free,
// newline-free token. The callers in this repo register no pattern with a
// space, so patterns from step 1/2 need no cleaning; the fallback is the
// only unbounded input and it is escaped at the source.
//
// The registered patterns here are path-only (`mux.HandleFunc("/api/…",
// …)`), so the key is the endpoint path with no method on it; a pattern
// that DOES declare a method carries it verbatim, because that is what was
// registered.
func runtimeHealthKey(mux *http.ServeMux, r *http.Request) string {
	if r.Pattern != "" {
		return r.Pattern
	}
	if mux != nil {
		if _, pattern := mux.Handler(r); pattern != "" {
			return pattern
		}
	}
	return runtimeHealthUnmatchedPrefix + r.Method + ":" + r.URL.EscapedPath() + ")"
}

// withRuntimeHealth meters one endpoint's in-flight and cumulative traffic
// on the MAIN listener's chain (D2's answer to "who created the 1.88 M
// goroutines"). A long-lived SSE or WebSocket handler holds the gauge for
// the whole life of the connection by construction — the wrapper's
// next.ServeHTTP does not return until the handler unwinds — which is what
// makes the gauge the right instrument for connection churn rather than
// request rate.
func (s *Server) withRuntimeHealth(next http.Handler) http.Handler {
	return s.withRuntimeHealthOn(s.mux, next)
}

// withRuntimeHealthOn is the per-listener form (issue #130 review P3): the
// pattern pre-lookup must run against the mux THAT ACTUALLY DISPATCHES the
// request — the main listener passes s.mux, the independent rx listener
// passes its own rxMux. Querying s.mux for an rx request would mislabel
// every rx 404 as a main-listener miss, because rxMux registers only
// /rx/ and its routing table is different. Both listeners share ONE
// runtimeHealth — one 64-key cap, one meter, one sampling loop, no second
// ticker.
func (s *Server) withRuntimeHealthOn(mux *http.ServeMux, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		st := s.runtimeHealth.enter(runtimeHealthKey(mux, r))
		defer s.runtimeHealth.leave(st)
		next.ServeHTTP(w, r)
	})
}

// withPanicOnFault arms the fault→panic conversion on the goroutine that
// will actually fault (D1; the header comment carries the per-goroutine
// evidence for why this has to be here and not just in Start()).
//
// It does NOT restore the previous value on the way out, and what staying
// armed buys must be stated at its real width: arming across the handler's
// return keeps the CONVERSION alive into net/http's teardown defer, so a
// fault there still surfaces as a `panic:` naming the faulting frame instead
// of the runtime's own `fatal error: fault` throw — but it does NOT survive
// that fault, and the swap is NOT about getting more stack: the throw path
// prints `fatal error: fault` PLUS every goroutine's stack even under the
// default GOTRACEBACK=single, because a failure internal to the run-time
// dumps all of them (GOROOT go1.25.0 runtime/extern.go:252-260, key clause
// at :256-257; verified by running an unarmed munmap fault: exit 2, 7
// goroutines dumped). The converted panic under `single` prints ONE stack —
// the narrower dump of the two is the armed one, which is exactly why the
// deployment docs ask for GOTRACEBACK=all. What arming buys is
// recoverability where a recover exists, not width. net/http's defer runs
// its recover() at server.go:1940 BEFORE cancelCtx() at :1947 (the #130
// crash site), and one defer cannot recover
// twice: after recover() has returned there is no panic in flight to catch,
// so the teardown fault exits the process as an unrecovered panic (exit
// 2). The survivable window is a fault inside a handler body — caught by
// that same recover(), connection closed, daemon keeps serving.
func (s *Server) withPanicOnFault(next http.Handler) http.Handler {
	if !s.panicOnFault {
		return next
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		s.applyPanicOnFaultOnThisGoroutine()
		next.ServeHTTP(w, r)
	})
}

// applyPanicOnFaultOnThisGoroutine is the per-request arm. One call to
// debug.SetPanicOnFault is a getg + a bool store plus the previous-value
// return (GOROOT go1.25.0 runtime/rdebug.go:16-22) — free next to the
// syscall work every other part of this path already does.
func (s *Server) applyPanicOnFaultOnThisGoroutine() {
	set := s.setPanicOnFault
	if set == nil {
		set = debug.SetPanicOnFault
	}
	set(true)
}

// applyPanicOnFault resolves the D1 switch, arms the CALLER's goroutine,
// and makes the configuration observable.
//
// What this arms is the goroutine that calls Start() — and ONLY that one.
// It is not the accept loop: `Serve` runs on a goroutine Start() spawns
// (`go func(){ …Serve(ln)… }`), and per-connection goroutines are spawned
// by Serve itself, and the per-goroutine flag does not inherit into either
// (runtime/rdebug.go sets it on getg(); newproc1 resets it, proc.go:4452).
// The protection that would have converted #130's fault lives in
// withPanicOnFault, which re-arms every request on its own connection
// goroutine (header comment). This call stays because the caller's
// goroutine can fault too, and because this is where the resolved setting
// gets logged once instead of once per request.
//
// The DISABLED line's wording is pinned by fact, and here so it stays that
// way: with the switch off a non-nil-address fault takes the throw path,
// which prints `fatal error: fault` plus EVERY goroutine's stack (extern.go
// :256-257, verified by running one) — so the line must never claim "no
// stack". Disarming narrows the crash dump to the runtime's throw shape; it
// does not blind the operator.
func (s *Server) applyPanicOnFault() {
	setting := resolvePanicOnFault(lookupRuntimeHealthEnv(envPanicOnFault))
	s.panicOnFault = setting.Enabled

	set := s.setPanicOnFault
	if set == nil {
		set = debug.SetPanicOnFault
	}
	set(setting.Enabled)

	if s.logger == nil {
		return
	}
	switch {
	case setting.Warning != "":
		s.logger.Printf("[runtime-health] warning: %s", setting.Warning)
	}
	if setting.Enabled {
		if setting.Explicit {
			s.logger.Printf("[runtime-health] panic-on-fault enabled (issue #130) (explicit %s)", envPanicOnFault)
		} else {
			s.logger.Printf("[runtime-health] panic-on-fault enabled (issue #130)")
		}
		return
	}
	s.logger.Printf("[runtime-health] panic-on-fault DISABLED by %s — a non-nil fault will exit with `fatal error: fault` and the runtime's own all-goroutine dump (exit 2) — wider than the converted panic's traceback, not narrower (issue #130)", envPanicOnFault)
}
