package app

import (
	"bytes"
	"errors"
	"log"
	"net/http"
	"net/http/httptest"
	"os"
	"reflect"
	"regexp"
	"runtime"
	"runtime/debug"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"myworktree/internal/store"
)

// ---------------------------------------------------------------------------
// D1 — MYWORKTREE_PANIC_ON_FAULT: resolution (pure) and its two arm points.
// ---------------------------------------------------------------------------

// The three branches the plan names — absent means ON, 0/false mean OFF,
// anything else keeps the default AND says so — plus the trim/case tolerance
// that decides whether "FALSE" in a systemd unit is honoured or warned about.
func TestResolvePanicOnFaultBranches(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name        string
		raw         string
		present     bool
		wantEnabled bool
		wantWarn    bool
	}{
		// Absent → the default, which is ON, and NOT explicit: the log has
		// to be able to tell "nobody configured this" from "the operator
		// asked for it", because after the fact those are different answers
		// to "who decided to keep this process alive".
		{name: "absent means enabled by default", raw: "", present: false, wantEnabled: true, wantWarn: false},
		{name: "explicit 1", raw: "1", present: true, wantEnabled: true, wantWarn: false},
		{name: "explicit true", raw: "true", present: true, wantEnabled: true, wantWarn: false},
		{name: "explicit TRUE is honoured after case fold", raw: "TRUE", present: true, wantEnabled: true, wantWarn: false},
		{name: "explicit 0 disables", raw: "0", present: true, wantEnabled: false, wantWarn: false},
		{name: "explicit false disables", raw: "false", present: true, wantEnabled: false, wantWarn: false},
		{name: "explicit False disables", raw: "False", present: true, wantEnabled: false, wantWarn: false},
		{name: "padded 0 disables", raw: "  0  ", present: true, wantEnabled: false, wantWarn: false},
		// The invalid branch: the default (ON) stands and the operator gets
		// told. A typo that silently did nothing is the failure mode this
		// whole feature exists to refuse.
		{name: "yes is invalid, default stands with a warning", raw: "yes", present: true, wantEnabled: true, wantWarn: true},
		{name: "off is invalid (false is the spelling)", raw: "off", present: true, wantEnabled: true, wantWarn: true},
		{name: "empty string set is invalid", raw: "", present: true, wantEnabled: true, wantWarn: true},
		{name: "2 is invalid", raw: "2", present: true, wantEnabled: true, wantWarn: true},
	}
	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got := resolvePanicOnFault(tc.raw, tc.present)
			if got.Enabled != tc.wantEnabled {
				t.Fatalf("Enabled=%v, want %v (raw=%q present=%v)", got.Enabled, tc.wantEnabled, tc.raw, tc.present)
			}
			if got.Explicit != tc.present {
				t.Fatalf("Explicit=%v, want %v — a default and a decision must stay distinguishable in the log", got.Explicit, tc.present)
			}
			if warned := got.Warning != ""; warned != tc.wantWarn {
				t.Fatalf("Warning=%q, want a warning=%v (raw=%q)", got.Warning, tc.wantWarn, tc.raw)
			}
			if tc.wantWarn && !strings.Contains(got.Warning, envPanicOnFault) {
				t.Fatalf("the warning must name the variable the operator mistyped, got %q", got.Warning)
			}
		})
	}
}

// The interval knob: default 60 s, missing/unparsable fall back to it, and
// ≤0 is the OFF switch the plan names (0 = the loop never launches at all).
func TestResolveRuntimeHealthInterval(t *testing.T) {
	t.Parallel()
	if got := resolveRuntimeHealthInterval("", false); got != defaultRuntimeHealthInterval {
		t.Fatalf("absent = %v, want the %v default", got, defaultRuntimeHealthInterval)
	}
	if got := resolveRuntimeHealthInterval("abc", true); got != defaultRuntimeHealthInterval {
		t.Fatalf("unparsable = %v, want the default — a typo must not silence the instrument", got)
	}
	for _, off := range []string{"0", "-1", "-60000"} {
		if got := resolveRuntimeHealthInterval(off, true); got != 0 {
			t.Fatalf("%q = %v, want 0 (= the loop never runs)", off, got)
		}
	}
	if got := resolveRuntimeHealthInterval("250", true); got != 250*time.Millisecond {
		t.Fatalf("250 = %v, want 250ms", got)
	}
	// The plan's cadence pinned as a literal, so "60s" cannot quietly become
	// "30s" in a refactor nobody read as a cadence change.
	if defaultRuntimeHealthInterval != 60*time.Second {
		t.Fatalf("defaultRuntimeHealthInterval = %v, want 60s (FIX-PLAN D2)", defaultRuntimeHealthInterval)
	}
	// The OFF switch must not be reachable through an unparsable value —
	// "off" in a unit file means the operator wanted the instrument OFF but
	// spelled it wrong, and the honest answer is the default, not silence.
	if got := resolveRuntimeHealthInterval("off", true); got != defaultRuntimeHealthInterval {
		t.Fatalf("\"off\" = %v, want the default; only a NUMBER ≤0 switches it off", got)
	}
}

func TestResolveRuntimeHealthGoroutineAlert(t *testing.T) {
	t.Parallel()
	if got := resolveRuntimeHealthGoroutineAlert("", false); got != defaultRuntimeHealthGoroutineAlert {
		t.Fatalf("absent = %d, want the 1000 default", got)
	}
	if got := resolveRuntimeHealthGoroutineAlert("many", true); got != defaultRuntimeHealthGoroutineAlert {
		t.Fatalf("unparsable = %d, want the default", got)
	}
	// ≤0 is NOT "alert always": an alert on every tick is noise, and the
	// interval env is the honest way to ask for more samples.
	for _, bad := range []string{"0", "-5"} {
		if got := resolveRuntimeHealthGoroutineAlert(bad, true); got != defaultRuntimeHealthGoroutineAlert {
			t.Fatalf("%q = %d, want the default (zero is not a request to alert every tick)", bad, got)
		}
	}
	if got := resolveRuntimeHealthGoroutineAlert("7", true); got != 7 {
		t.Fatalf("7 = %d, want 7", got)
	}
	// The plan's threshold, pinned: 1000 against the 48 live goroutines
	// #130's daemon had at death.
	if defaultRuntimeHealthGoroutineAlert != 1000 {
		t.Fatalf("default alert threshold = %d, want 1000", defaultRuntimeHealthGoroutineAlert)
	}
}

// The 30-minute dump cooldown is the plan's number and the log's protection
// against a stuck leak re-dumping every tick. The test seams shorten it to
// milliseconds, so THIS is where the production value is pinned.
func TestRuntimeHealthDumpCooldownConstant(t *testing.T) {
	t.Parallel()
	if runtimeHealthGoroutineDumpCooldown != 30*time.Minute {
		t.Fatalf("dump cooldown = %v, want ≥30min (FIX-PLAN D2)", runtimeHealthGoroutineDumpCooldown)
	}
}

// newTestRuntimeHealth builds a sampler with its own log buffer and every
// knob a literal — cadence, threshold, dump seam — so no test here depends on
// the host's environment, which the parallel suite shares. The cooldown is the
// production constant rather than a short seam: no case here needs a shorter
// window, and the one case that needs a cooldown to END moves lastDumpNano
// instead of shrinking the window (see TestRuntimeHealthAlertDumpsOncePerCooldown).
func newTestRuntimeHealth(t *testing.T, interval time.Duration, threshold int64) (*runtimeHealth, *lockedLogBuffer, *atomic.Int64) {
	t.Helper()
	out := &lockedLogBuffer{}
	dumps := &atomic.Int64{}
	h := &runtimeHealth{
		logger:         log.New(out, "", 0),
		interval:       interval,
		goroutineAlert: threshold,
		dumpCooldown:   runtimeHealthGoroutineDumpCooldown,
		dumpProfile: func(w *bytes.Buffer) error {
			dumps.Add(1)
			_, _ = w.WriteString("fake goroutine profile\n")
			return nil
		},
		startedAt: time.Now(),
		endpoints: map[string]*runtimeEndpointStat{},
		stop:      make(chan struct{}),
		done:      make(chan struct{}),
	}
	t.Cleanup(h.Stop)
	return h, out, dumps
}

// The tick line's exact shape at a compressed cadence: every field the plan
// names, in its slot, self-consistent. This line IS the data source, so its
// format is the contract.
func TestRuntimeHealthTickLineFormat(t *testing.T) {
	t.Parallel()
	h, out, _ := newTestRuntimeHealth(t, 20*time.Millisecond, 1<<40)
	h.Start()

	line := waitForLogLine(t, out, "[runtime-health] tick ", 3*time.Second)
	m := regexp.MustCompile(`^\[runtime-health\] tick uptMs=(\d+) goroutines=(\d+) heapMB=(\d+) sysMB=(\d+) gcCount=(\d+) inflight=(\d+) reqTotal=(\d+) top=\((.*)\)$`).FindStringSubmatch(line)
	if m == nil {
		t.Fatalf("tick line = %q, want the frozen field order \"[runtime-health] tick uptMs=… goroutines=… heapMB=… sysMB=… gcCount=… inflight=… reqTotal=… top=(…)\"", line)
	}
	if upt := parseOrFail(t, "uptMs", m[1]); upt <= 0 {
		t.Fatalf("uptMs=%d, want a positive uptime — the whole point of the field is that nobody is watching this process", upt)
	}
	if g := parseOrFail(t, "goroutines", m[2]); g < 1 {
		t.Fatalf("goroutines=%d, want at least the test goroutine itself", g)
	}
	// Heap/sys are machine-dependent absolutes, so the assertion is the
	// invariant between them rather than a literal: heap-alloc can never
	// exceed the address space the runtime mapped.
	if heap, sys := parseOrFail(t, "heapMB", m[3]), parseOrFail(t, "sysMB", m[4]); heap > sys {
		t.Fatalf("heapMB=%d > sysMB=%d: heap-alloc can never exceed the mapped address space: %q", heap, sys, line)
	}
	if inflight, total := parseOrFail(t, "inflight", m[6]), parseOrFail(t, "reqTotal", m[7]); inflight > total {
		t.Fatalf("inflight=%d > reqTotal=%d: nothing can be in flight that was never requested: %q", inflight, total, line)
	}
	if top := m[8]; top != "" {
		t.Fatalf("top=%q on a sampler that has metered nothing, want the empty () — cold prints empty, not a guess", top)
	}
}

// A threshold crossing prints the alert line on EVERY tick over the line and
// dumps only once per cooldown window: the crossing is the fact a reader
// scans for, the dump is the expensive repeating part.
//
// The cooldown here is driven through lastDumpNano, NOT the wall clock. An
// earlier revision shortened the seam to 50ms and slept 80ms, which put the
// assertion in the same order of magnitude as one tick's scheduling delay:
// under -race the first run of `go test -count=10 -race` reported
// `dumps = 2, want exactly 1` because the 50 ms window lapsed between two
// ticks that were supposed to be inside it. Both halves are exact now: the
// window is the production 30 min, so host jitter cannot lapse it while three
// ticks pile up, and the half that needs the window to END moves the clock
// itself. What is under test is the comparison inside alert() — advancing
// lastDumpNano tests that mechanism instead of the machine's timer.
func TestRuntimeHealthAlertDumpsOncePerCooldown(t *testing.T) {
	t.Parallel()
	h, out, dumps := newTestRuntimeHealth(t, time.Hour, 1)
	// The production cooldown, and no Start() in this test, so this write has
	// no concurrent reader: every tick below is driven by this goroutine.
	h.dumpCooldown = runtimeHealthGoroutineDumpCooldown

	// Three goroutines over a threshold of 1, held for the whole test so the
	// condition is a STATE and not a moment.
	var wg sync.WaitGroup
	release := make(chan struct{})
	for i := 0; i < 3; i++ {
		wg.Add(1)
		go func() { defer wg.Done(); <-release }()
	}
	defer close(release)
	// The settle is not decoration: NumGoroutine counts from creation, so
	// this is the ordering that makes the crossing real before the first
	// tick reads it (the goroutines above are scheduled by then).
	time.Sleep(30 * time.Millisecond)

	// Ticks driven directly — the loop's cadence is not the subject here.
	h.tick()
	h.tick()
	h.tick()

	if alerts := countPrefixedLines(out.String(), "[runtime-health] alert goroutines="); alerts != 3 {
		t.Fatalf("alert lines = %d, want 3 — one per tick over the threshold; suppressing them would hide whether the leak is still running. log=%q", alerts, out.String())
	}
	if got := dumps.Load(); got != 1 {
		t.Fatalf("dumps = %d, want exactly 1 across three alerts — the cooldown is what stops a stuck leak dumping every tick", got)
	}
	if !strings.Contains(out.String(), "[runtime-health] goroutine-dump bytes=") {
		t.Fatalf("the dump body must follow the alert line, log=%q", out.String())
	}
	// The threshold is stated on the line: a reader must be able to tell a
	// 1000-floor alert from a 50-floor one without reading the env.
	if !strings.Contains(out.String(), "threshold=1 ") {
		t.Fatalf("the alert line must carry its threshold, log=%q", out.String())
	}

	// After the cooldown the next tick dumps again: the dump is
	// rate-limited, never one-shot — a leak's slope needs more than one
	// point, and this is the half of the cooldown contract that a
	// "dump once ever" implementation would silently fail. The clock is rolled
	// back by hand because sleeping past the production window is not an
	// option, and a millisecond-scale fake window is the flake this test used
	// to lose under -race.
	h.lastDumpNano.Store(time.Now().Add(-h.dumpCooldown - time.Second).UnixNano())
	h.tick()
	if got := dumps.Load(); got != 2 {
		t.Fatalf("dumps = %d after the cooldown elapsed, want 2 — the cooldown bounds the rate, it does not disable the dump", got)
	}
	if alerts := countPrefixedLines(out.String(), "[runtime-health] alert goroutines="); alerts != 4 {
		t.Fatalf("alert lines = %d, want 4", alerts)
	}

	// And the fresh dump re-arms the guard: the tick immediately after it sits
	// inside the window again and must not dump. Without this half, a dump
	// that stamped nothing would still pass everything above.
	h.tick()
	if got := dumps.Load(); got != 2 {
		t.Fatalf("dumps = %d on the tick right after a fresh dump, want 2 — a dump must stamp the cooldown it just consumed", got)
	}
	if alerts := countPrefixedLines(out.String(), "[runtime-health] alert goroutines="); alerts != 5 {
		t.Fatalf("alert lines = %d, want 5 — the alert line keeps firing on every tick while only the dump is cooled", alerts)
	}
}

// A dump that FAILS must not spend the cooldown it never used (issue #130
// review R5). The CAS stamps the slot BEFORE the dump runs, so a transient
// failure — pprof's own walk hitting a moment when the profile cannot be
// written — would otherwise sit out the full 30 minutes for a dump that never
// happened, and the operator loses the next ~30 samples of exactly the leak
// being investigated. The rollback restores the pre-claim stamp exactly, so
// the retry lands on the NEXT tick (≤60 s).
//
// Driven through the seam, on the production cooldown, and with every tick
// called by this goroutine (no Start()), so no timing is in the balance: the
// second tick is immediate, and the third proves the cooldown still works
// after the retry succeeded.
func TestRuntimeHealthDumpFailureRollsBackTheCooldown(t *testing.T) {
	t.Parallel()
	h, out, _ := newTestRuntimeHealth(t, time.Hour, 1)

	// The seam counts attempts and fails exactly one named attempt, so the
	// failure is a fact the test schedules rather than a race it hopes for.
	var attempts atomic.Int64
	var failOn atomic.Int64
	failOn.Store(1)
	h.dumpProfile = func(w *bytes.Buffer) error {
		if attempts.Add(1) == failOn.Load() {
			return errors.New("injected dump failure")
		}
		_, _ = w.WriteString("fake goroutine profile\n")
		return nil
	}

	// Over the threshold of 1 for the whole test, so the alert condition is a
	// STATE and every tick below really is an alert.
	var wg sync.WaitGroup
	release := make(chan struct{})
	for i := 0; i < 3; i++ {
		wg.Add(1)
		go func() { defer wg.Done(); <-release }()
	}
	defer close(release)
	time.Sleep(30 * time.Millisecond)

	// Phase A — prev was 0 (never dumped): a failed dump must leave it 0, not
	// a fresh stamp, and the retry must be the very next tick.
	h.tick()
	if got := attempts.Load(); got != 1 {
		t.Fatalf("dump attempts = %d after the first alert, want 1", got)
	}
	if !strings.Contains(out.String(), "[runtime-health] goroutine-dump failed: injected dump failure") {
		t.Fatalf("the failure must be logged as it happens, log=%q", out.String())
	}
	if got := h.lastDumpNano.Load(); got != 0 {
		t.Fatalf("lastDumpNano = %d after a FAILED dump, want the 0 it was before the claim — a failed dump must hand the slot back", got)
	}

	h.tick()
	if got := attempts.Load(); got != 2 {
		t.Fatalf("dump attempts = %d on the tick right after a failed dump, want 2 — the retry belongs to the NEXT tick (≤60 s), not to the far side of a 30 min cooldown", got)
	}
	if !strings.Contains(out.String(), "[runtime-health] goroutine-dump bytes=") {
		t.Fatalf("the retried dump must reach the log, log=%q", out.String())
	}
	if got := h.lastDumpNano.Load(); got == 0 {
		t.Fatal("a SUCCESSFUL dump must stamp the cooldown — the rollback must not disable the guard")
	}

	// Phase B — the cooldown is live again: the next tick must NOT dump.
	h.tick()
	if got := attempts.Load(); got != 2 {
		t.Fatalf("dump attempts = %d on a cooled tick, want 2 — the rollback is for failures only", got)
	}

	// Phase C — exact restore, not a zeroing: with a real earlier stamp, the
	// failed dump must put THAT number back.
	prev := time.Now().Add(-h.dumpCooldown - time.Second).UnixNano()
	h.lastDumpNano.Store(prev)
	failOn.Store(attempts.Load() + 1)
	h.tick()
	if got := h.lastDumpNano.Load(); got != prev {
		t.Fatalf("lastDumpNano = %d after a failed dump that started from %d, want the exact pre-claim stamp restored — rolling back to 0 would discard a cooldown the process had already earned", got, prev)
	}
}

// The alert must NOT fire at or below the threshold: an instrument that cries
// wolf at the floor trains the reader to ignore the prefix.
func TestRuntimeHealthAlertFiresOnlyAboveThreshold(t *testing.T) {
	t.Parallel()
	h, out, dumps := newTestRuntimeHealth(t, time.Hour, 1<<31-1)
	h.tick()
	s := out.String()
	if strings.Contains(s, "[runtime-health] alert") {
		t.Fatalf("alerted below the threshold, log=%q", s)
	}
	if !strings.Contains(s, "[runtime-health] tick ") {
		t.Fatalf("the tick line must still print, log=%q", s)
	}
	if dumps.Load() != 0 {
		t.Fatalf("dumped without an alert — the dump belongs to the alert and nothing else")
	}
}

// The dump must be TEXT. debug=1 is the text mode of Profile.WriteTo and it
// is load-bearing: debug=0 emits gzipped protobuf, and piping that into the
// daemon's log file produces a blob nobody can read at 4am — the exact
// "no readable evidence" failure #130 died of. This runs the REAL seam (not
// the test fake), so the mode itself is pinned.
func TestRuntimeHealthGoroutineDumpIsText(t *testing.T) {
	t.Parallel()
	var buf bytes.Buffer
	if err := writeGoroutineProfile(&buf); err != nil {
		t.Fatalf("real goroutine profile dump failed: %v", err)
	}
	s := buf.String()
	if !strings.HasPrefix(s, "goroutine profile: total ") {
		t.Fatalf("dump does not start with the pprof TEXT header, so it is not a readable dump (first bytes %q)", s[:min(32, len(s))])
	}
	// The pprof text body is `count @ addr addr …` cluster lines followed
	// by `#\taddr\tfunc+off` frame lines. The "goroutine N [state]:" form
	// belongs to runtime.Stack, NOT to Profile.WriteTo(debug=1) — pinning
	// the wrong text shape here would fail on a perfectly readable dump,
	// which is worse than not pinning at all.
	if !strings.Contains(s, " @ 0x") {
		t.Fatalf("text profile carries no `count @ 0x…` cluster lines, want the pprof text frames, got %q", s[:min(64, len(s))])
	}
}

// ---------------------------------------------------------------------------
// D2 — the per-endpoint middleware.
// ---------------------------------------------------------------------------

// In-flight rises when a request enters and falls when it leaves — and holds
// for the WHOLE life of a blocked handler, which is the property that makes
// this the right instrument for a long-lived TTY/SSE socket: the gauge
// measures the connection, not the request rate.
func TestRuntimeHealthMiddlewareInflightRisesAndFalls(t *testing.T) {
	t.Parallel()
	h, _, _ := newTestRuntimeHealth(t, time.Hour, 1<<31-1)
	mux := http.NewServeMux()

	entered := make(chan struct{})
	release := make(chan struct{})
	mux.HandleFunc("/api/instances/log", func(w http.ResponseWriter, r *http.Request) {
		close(entered)
		<-release // the stand-in for a socket that parks for hours
		w.WriteHeader(http.StatusNoContent)
	})

	srv := &Server{mux: mux, runtimeHealth: h}
	closed := make(chan struct{})
	go func() {
		defer close(closed)
		rec := httptest.NewRecorder()
		srv.withRuntimeHealth(mux).ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/instances/log?id=7", nil))
	}()

	<-entered
	inflight, total, top := h.snapshot()
	if inflight != 1 {
		t.Fatalf("inflight=%d while one handler is parked, want 1", inflight)
	}
	if total != 1 {
		t.Fatalf("reqTotal=%d, want 1", total)
	}
	// The key is the registered PATTERN, not the raw path: attribution has
	// to be bounded by the route table or the map becomes the next leak.
	if !strings.Contains(top, "/api/instances/log 1/1") {
		t.Fatalf("top=%q, want the parked endpoint at inflight/total = 1/1", top)
	}

	close(release)
	<-closed
	// The gauge returns to zero and the counter does not — that asymmetry is
	// the difference between a gauge and a counter, and the tick line prints
	// both for exactly that reason.
	inflight, total, _ = h.snapshot()
	if inflight != 0 {
		t.Fatalf("inflight=%d after the handler returned, want 0 — a gauge that sticks is worse than no gauge", inflight)
	}
	if total != 1 {
		t.Fatalf("reqTotal=%d after the handler returned, want the cumulative 1", total)
	}
}

// The key resolution order and the fallback. The middleware mounts OUTSIDE
// the mux (the plan refuses to touch registrations), so `r.Pattern` is empty
// at metering time and the pattern comes from the non-mutating mux lookup:
// the plan's "key by r.Pattern" cannot work as literally written, and this
// test pins what does.
func TestRuntimeHealthKeyPatternAndFallback(t *testing.T) {
	t.Parallel()
	mux := http.NewServeMux()
	mux.HandleFunc("/api/instances/log", func(http.ResponseWriter, *http.Request) {})
	mux.Handle("/static/", http.NotFoundHandler())

	if got := runtimeHealthKey(mux, httptest.NewRequest(http.MethodGet, "/api/instances/log?id=7", nil)); got != "/api/instances/log" {
		t.Fatalf("key=%q, want the registered pattern (a query param must never reach the key)", got)
	}
	if got := runtimeHealthKey(mux, httptest.NewRequest(http.MethodGet, "/static/x/y.png", nil)); got != "/static/" {
		t.Fatalf("key=%q, want the prefix pattern /static/ that ServeMux dispatches to", got)
	}
	// Unmatched: the parenthesised, SPACE-FREE fallback form. A space here
	// would break the space-delimited readers of every [runtime-health] line.
	fallback := runtimeHealthKey(mux, httptest.NewRequest(http.MethodGet, "/nope/nothing", nil))
	if fallback != "(!pattern:GET:/nope/nothing)" {
		t.Fatalf("fallback key=%q, want \"(!pattern:GET:/nope/nothing)\" — parenthesised so it stays one space-delimited token", fallback)
	}
	if strings.Contains(fallback, " ") {
		t.Fatalf("fallback key %q contains a space, which would break the line's field layout", fallback)
	}
	// A pattern already on the request wins, cost-free — the fast path for
	// any future mount point that sits inside a mux dispatch.
	r := httptest.NewRequest(http.MethodGet, "/api/instances/log", nil)
	r.Pattern = "/already/matched"
	if got := runtimeHealthKey(mux, r); got != "/already/matched" {
		t.Fatalf("key=%q, want the already-set r.Pattern to win", got)
	}
	// A nil mux (a hand-built Server) still yields a well-shaped key rather
	// than panicking on the request path.
	if got := runtimeHealthKey(nil, httptest.NewRequest(http.MethodPost, "/x", nil)); got != "(!pattern:POST:/x)" {
		t.Fatalf("nil-mux key=%q, want the fallback shape", got)
	}
	// The log-injection case (issue #130 review B2): `r.URL.Path` is the
	// DECODED path, so a request for this percent-encoded URI would put a
	// literal newline and two literal spaces into the key if the fallback
	// used URL.Path — and this middleware runs OUTSIDE withAuth, so the
	// attacker needs no credentials to forge a `[runtime-health]` token on
	// a line of the daemon's log. First prove the hazard is real (decoded
	// Path carries the space and the newline), then pin that the key does
	// not: EscapedPath keeps the percent form, the key stays ONE
	// whitespace-free, newline-free token.
	injected := "/a%0A[runtime-health]%20tick%20fake=1"
	rInj := httptest.NewRequest(http.MethodGet, injected, nil)
	if !strings.Contains(rInj.URL.Path, " ") || !strings.Contains(rInj.URL.Path, "\n") {
		t.Fatalf("precondition broken: decoded Path=%q should carry the decoded space and newline", rInj.URL.Path)
	}
	keyInj := runtimeHealthKey(mux, rInj)
	if strings.ContainsAny(keyInj, " \n\r\t") {
		t.Fatalf("fallback key %q carries whitespace — URL.Path leaked into the key, breaking the space-delimited contract and allowing forged [runtime-health] lines", keyInj)
	}
	if !strings.Contains(keyInj, "%0A") || !strings.Contains(keyInj, "%20") {
		t.Fatalf("fallback key %q must keep the percent-escaped form (%%0A, %%20) so the information survives without breaking the layout", keyInj)
	}
	if !strings.HasPrefix(keyInj, runtimeHealthUnmatchedPrefix) || !strings.HasSuffix(keyInj, ")") {
		t.Fatalf("fallback key %q must keep the parenthesised fallback shape", keyInj)
	}
	// Control characters beyond space/newline are escaped too (0x01):
	// the contract is "no control characters", not "no newline".
	keyCtl := runtimeHealthKey(mux, httptest.NewRequest(http.MethodGet, "/ctl%01name", nil))
	for _, c := range keyCtl {
		if c < 0x20 {
			t.Fatalf("fallback key %q contains a raw control character %#U", keyCtl, c)
		}
	}
}

// The fallback key is the only unauthenticated, unbounded input in the key
// space, so BOTH of its dimensions are bounded (issue #130 review R5): the
// COUNT by runtimeHealthMaxEndpointKeys, and here the DELIMITERS and the
// LENGTH.
//
// The delimiter half is a real hazard, not a hypothetical one, and the test
// proves the hazard before it pins the fix: `EscapedPath()` leaves `,` `(`
// `)` RAW (`,` is a path sub-delim RFC 3986 §3.3 allows unescaped, and the
// parens ride through RawPath) — verified by running it, see the precondition
// below. A raw `,` breaks a reader that splits `top=(a 1/2, b 0/1)` on the
// comma, and a raw `)` closes the `top=(…)` wrapper for a reader that matches
// the first one. Both get percent form here, so every key is one token no
// reader can split at the wrong place.
func TestRuntimeHealthFallbackKeyEscapesDelimiters(t *testing.T) {
	t.Parallel()
	mux := http.NewServeMux()
	mux.HandleFunc("/api/instances/log", func(http.ResponseWriter, *http.Request) {})

	target := "/scan/a,b(c)"
	r := httptest.NewRequest(http.MethodGet, target, nil)
	if !strings.Contains(r.URL.EscapedPath(), ",") || !strings.Contains(r.URL.EscapedPath(), "(") {
		t.Fatalf("precondition broken: EscapedPath(%q) = %q, want the raw comma and parens still present — without that this test proves nothing", target, r.URL.EscapedPath())
	}
	key := runtimeHealthKey(mux, r)
	if key != "(!pattern:GET:/scan/a%2Cb%28c%29)" {
		t.Fatalf("key=%q, want \"(!pattern:GET:/scan/a%%2Cb%%28c%%29)\" — the fallback path's , ( ) in percent form, the wrapper's own parens raw", key)
	}
	body := strings.TrimSuffix(strings.TrimPrefix(key, runtimeHealthUnmatchedPrefix), ")")
	if strings.ContainsAny(body, ",()") {
		t.Fatalf("fallback key body %q still carries a raw , ( or ) — the top=(…) field would split at a delimiter the key does not own", body)
	}
	// Registered patterns pass through verbatim: this repo registers no comma
	// and no parenthesis in any pattern (grep over internal/app/app.go's 41
	// mux.Handle* calls: zero matches), so nothing real is rewritten here.
	if got := runtimeHealthKey(mux, httptest.NewRequest(http.MethodGet, "/api/instances/log", nil)); got != "/api/instances/log" {
		t.Fatalf("registered-pattern key=%q, want the verbatim pattern — the escaping is for the fallback path only", got)
	}
	// The whitespace/control-char contract the %20/%0A cases pin stays green
	// on this input too (0x7f included: "no control characters" is the whole
	// ASCII control range, not just newline).
	if strings.ContainsAny(key, " \t\n\r") {
		t.Fatalf("fallback key %q carries whitespace", key)
	}
	for _, c := range key {
		if c < 0x20 || c == 0x7f {
			t.Fatalf("fallback key %q contains a raw control character %#U", key, c)
		}
	}
}

// The LENGTH half of the same contract. An 8000-byte request path is legal —
// the only ceiling on a request line is net/http's header budget, 1 MB by
// default — so without the clamp the map could hold 64 keys of ~1 MB each
// (~64 MB) in the process this file exists to keep alive, and every tick line
// would print a megabyte-long top token. With it: one bounded, marked token.
func TestRuntimeHealthFallbackKeyLengthIsClamped(t *testing.T) {
	t.Parallel()
	long := "/" + strings.Repeat("x", 8000)
	key := runtimeHealthKey(nil, httptest.NewRequest(http.MethodGet, long, nil))
	if len(key) > runtimeHealthMaxFallbackKeyLen {
		t.Fatalf("fallback key = %d bytes from an %d-byte path, want ≤ %d: %q", len(key), len(long), runtimeHealthMaxFallbackKeyLen, key)
	}
	if !strings.HasSuffix(key, runtimeHealthFallbackTruncationMarker+")") {
		t.Fatalf("key %q must end with the ASCII truncation marker before the closing paren, so a reader can tell a clamped key from a whole path", key)
	}
	if strings.ContainsAny(key, " \t\n\r") {
		t.Fatalf("clamped key %q carries whitespace — truncation must not break the single-token contract", key)
	}
	for _, c := range key {
		if c < 0x20 || c > 0x7e {
			t.Fatalf("clamped key %q carries a non-ASCII or control byte %#U — the truncation marker and the cut are ASCII-only by contract", key, c)
		}
	}
	// The retained half is the real prefix of the real path, not a stub: a
	// reader must still be able to recognise the endpoint it is looking at.
	body := strings.TrimSuffix(strings.TrimPrefix(key, runtimeHealthUnmatchedPrefix+http.MethodGet+":"), ")")
	retained := strings.TrimSuffix(body, runtimeHealthFallbackTruncationMarker)
	if len(retained) < 8 || !strings.HasPrefix(long, retained) {
		t.Fatalf("retained path %q is not a prefix of the request path %q", retained, long)
	}
	// The bound holds for every method, not just GET — the method is
	// attacker-chosen too, so it is clamped through the same helper.
	for _, method := range []string{http.MethodGet, http.MethodDelete, "PROPFIND", strings.Repeat("M", 200)} {
		k := runtimeHealthKey(nil, httptest.NewRequest(method, long, nil))
		if len(k) > runtimeHealthMaxFallbackKeyLen {
			t.Fatalf("method %q: key = %d bytes, want ≤ %d: %q", method, len(k), runtimeHealthMaxFallbackKeyLen, k)
		}
	}
	// A path made of percent escapes must not be cut INSIDE one: a dangling
	// `%` or `%2` is a broken escape a percent-decoding reader chokes on, and
	// the cut position moves with the prefix, so this input walks the whole
	// remainder class of the budget.
	for _, pad := range []string{"", "a", "aa", "aaa", "aaaa"} {
		target := "/" + pad + strings.Repeat("%2C", 4000)
		k := runtimeHealthKey(nil, httptest.NewRequest(http.MethodGet, target, nil))
		if len(k) > runtimeHealthMaxFallbackKeyLen {
			t.Fatalf("key = %d bytes, want ≤ %d (pad %q)", len(k), runtimeHealthMaxFallbackKeyLen, pad)
		}
		b := strings.TrimSuffix(strings.TrimPrefix(k, runtimeHealthUnmatchedPrefix+http.MethodGet+":"), ")")
		r := strings.TrimSuffix(b, runtimeHealthFallbackTruncationMarker)
		if strings.HasSuffix(r, "%") || (len(r) >= 2 && r[len(r)-2] == '%') {
			t.Fatalf("clamped path %q is cut inside a %%XX escape — the cut must back off to a whole escape boundary", r)
		}
	}
}

// The payoff of both bounds in one place: the rendered `top=(…)` field splits
// on `", "` with NO ambiguity, even with comma-and-paren paths in the meter.
// This is the reader contract every grep/awk collector of these lines rests
// on, and before the escaping it was false — the raw comma inside
// `/scan/a,b(c)` produced a fourth field.
func TestRuntimeHealthTopFieldsSplitWithoutAmbiguity(t *testing.T) {
	t.Parallel()
	h, _, _ := newTestRuntimeHealth(t, time.Hour, 1<<31-1)
	mux := http.NewServeMux()
	mux.HandleFunc("/api/instances/log", func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(http.StatusOK) })
	hm := (&Server{mux: mux, runtimeHealth: h}).withRuntimeHealth(mux)

	for _, target := range []string{"/api/instances/log", "/scan/a,b(c)", "/scan/d,e(f)"} {
		rec := httptest.NewRecorder()
		hm.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, target, nil))
	}

	_, _, top := h.snapshot()
	if !strings.HasPrefix(top, "(") || !strings.HasSuffix(top, ")") {
		t.Fatalf("top=%q, want the parenthesised wrapper", top)
	}
	body := strings.TrimSuffix(strings.TrimPrefix(top, "("), ")")
	fields := strings.Split(body, ", ")
	if len(fields) != 3 {
		t.Fatalf("top split on \", \" into %d fields %q, want exactly 3 — one per metered endpoint; a raw comma inside a key is what makes this wrong", len(fields), fields)
	}
	if strings.Count(body, ",") != len(fields)-1 {
		t.Fatalf("body %q carries %d commas for %d separators — every comma in top=(…) must be a separator", body, strings.Count(body, ","), len(fields)-1)
	}
	fieldRE := regexp.MustCompile(`^\S+ \d+/\d+$`)
	for _, f := range fields {
		if fieldRE.FindStringSubmatch(f) == nil {
			t.Fatalf("top field %q is not `pattern inflight/total` — the render or the key escaping is off: full top=%q", f, top)
		}
	}
}

// The attribution map is bounded. Keys come from registered patterns in
// normal operation (fixed at ~45 here), but the fallback key carries the
// RAW path — so a client hammering random URLs must fold into one bucket
// rather than grow a map inside the process this file exists to keep alive.
func TestRuntimeHealthEndpointKeyMapIsBounded(t *testing.T) {
	t.Parallel()
	h, _, _ := newTestRuntimeHealth(t, time.Hour, 1<<31-1)
	const hits = runtimeHealthMaxEndpointKeys * 3
	for i := 0; i < hits; i++ {
		st := h.enter("(!pattern:GET:/scan/" + strconv.Itoa(i) + ")")
		h.leave(st)
	}
	h.mu.Lock()
	keys := len(h.endpoints)
	h.mu.Unlock()
	if keys > runtimeHealthMaxEndpointKeys {
		t.Fatalf("endpoint map holds %d keys, want at most %d — the fallback key carries the raw path, and an unbounded map here is the leak this issue is about", keys, runtimeHealthMaxEndpointKeys)
	}
	inflight, total, top := h.snapshot()
	if total != hits {
		t.Fatalf("reqTotal=%d, want every request counted even when its key folded into the overflow bucket", total)
	}
	if inflight != 0 {
		t.Fatalf("inflight=%d, want 0 after every request left", inflight)
	}
	if !strings.Contains(top, runtimeHealthOverflowKey) {
		t.Fatalf("top=%q, want the %s bucket to carry the folded noise", top, runtimeHealthOverflowKey)
	}
}

// The meter must survive a nil sampler: hand-built Servers (and any future
// Server that skips New) take the middleware with no meter behind it, and
// the request path must not be where that gets discovered.
func TestRuntimeHealthMiddlewareSurvivesNilSampler(t *testing.T) {
	t.Parallel()
	mux := http.NewServeMux()
	mux.HandleFunc("/api/main", func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(http.StatusOK) })
	srv := &Server{mux: mux}
	rec := httptest.NewRecorder()
	srv.withRuntimeHealth(mux).ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/main", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("status=%d, want the request served untouched by a nil sampler", rec.Code)
	}
}

// The meter and the sampler must agree: what the middleware counted must
// appear on the tick line, with the top-N rendered in the documented form.
// A tick that prints 0 beside a busy server is the lie to avoid.
func TestRuntimeHealthTickCarriesMeteredTraffic(t *testing.T) {
	t.Parallel()
	h, out, _ := newTestRuntimeHealth(t, time.Hour, 1<<31-1)
	mux := http.NewServeMux()
	mux.HandleFunc("/api/worktrees", func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(http.StatusOK) })
	mux.HandleFunc("/api/instances", func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(http.StatusOK) })
	hm := (&Server{mux: mux, runtimeHealth: h}).withRuntimeHealth(mux)

	for i := 0; i < 4; i++ {
		rec := httptest.NewRecorder()
		hm.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/worktrees", nil))
	}
	rec := httptest.NewRecorder()
	hm.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/instances", nil))

	h.tick()
	line := lastLineContaining(t, out.String(), "[runtime-health] tick ")
	if !strings.Contains(line, "inflight=0 reqTotal=5 ") {
		t.Fatalf("tick line = %q, want inflight=0 reqTotal=5 — every metered request belongs in the cumulative count", line)
	}
	// Top-N ordered by cumulative traffic, so the busier endpoint leads, in
	// the exact `pattern inflight/total` shape the collector parses.
	if !strings.Contains(line, "top=(/api/worktrees 0/4, /api/instances 0/1)") {
		t.Fatalf("tick line = %q, want top=(pattern inflight/total, …) with the busier endpoint first", line)
	}
}

// ---------------------------------------------------------------------------
// D2 × rx listener — the independent listener is covered too (review P3).
// ---------------------------------------------------------------------------

// The independent loopback rx listener (app.go rxSrv) must run the SAME two
// middlewares as the main listener, keyed against ITS OWN mux: a request
// that never touches the main listener must still raise the shared reqTotal
// and land in the /rx/ bucket. This goes to the real listener over TCP and
// picks a path that completes without any upstream (the reasonix proxy 404s
// an unknown instance id), so the test proves the mount, not an upstream.
func TestRuntimeHealthCoversRxListener(t *testing.T) {
	t.Setenv(envPanicOnFault, "1")
	srv, _ := newIsolatedTestServer(t, store.State{})
	// The shared sampler, tick loop OFF (interval 0): the meter and the
	// arming ride the request path and must stay live with the ticker off,
	// and no second ticker is what P3 demands.
	srv.runtimeHealth = newRuntimeHealth(srv.logger)
	srv.runtimeHealth.interval = 0

	// The arm seam, installed BEFORE Start so the write happens-before every
	// reader (Start spawns the accept loops, the accept loops spawn the
	// connection goroutines that read it). It DELEGATES to the real
	// debug.SetPanicOnFault — the rx goroutines really are armed — and only
	// counts what passes through, so the rx chain's arming can be asserted
	// directly instead of inferred from the resolved flag.
	var arms atomic.Int64
	var armsSeeingTrue atomic.Int64
	srv.setPanicOnFault = func(b bool) bool {
		arms.Add(1)
		prev := debug.SetPanicOnFault(b)
		if prev {
			armsSeeingTrue.Add(1)
		}
		return prev
	}

	url, err := srv.Start()
	if err != nil {
		t.Fatalf("Start failed: %v", err)
	}
	defer srv.Shutdown()
	if srv.rxAddr == "" {
		t.Fatal("no independent rx listener came up (loopback, no TLS) — nothing to test against")
	}
	if !strings.Contains(url, "127.0.0.1") {
		t.Fatalf("main listener %q should be loopback so rx is enabled", url)
	}
	// Baselines: Start's own arm of the calling goroutine is already counted;
	// everything below is attributed to the rx request alone.
	armedBeforeRx := arms.Load()
	sawTrueBeforeRx := armsSeeingTrue.Load()

	_, totalBefore, _ := srv.runtimeHealth.snapshot()

	// DisableKeepAlives so this request is served by a FRESH connection
	// goroutine: the arming assertions below are about who armed THE
	// GOROUTINE THAT RUNS THIS REQUEST, and a pooled connection reused from an
	// earlier test would make prev=false a matter of luck.
	rxClient := &http.Client{Transport: &http.Transport{DisableKeepAlives: true}}
	resp, err := rxClient.Get("http://" + srv.rxAddr + "/rx/nope/")
	if err != nil {
		t.Fatalf("GET through the real rx listener failed: %v", err)
	}
	// 404 from the reasonix proxy (unknown instance id) — completed without
	// any upstream, exactly the shape the acceptance names.
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("rx status=%d, want 404 from reasonixProxy with no such instance", resp.StatusCode)
	}
	_ = resp.Body.Close()

	_, totalAfter, top := srv.runtimeHealth.snapshot()
	if totalAfter != totalBefore+1 {
		t.Fatalf("reqTotal went %d → %d, want +1 — the rx listener shares the ONE meter with the main listener", totalBefore, totalAfter)
	}
	if !strings.Contains(top, "/rx/ ") {
		t.Fatalf("top=%q, want the /rx/ bucket visible — rx traffic must be keyed by rxMux's registered pattern /rx/, not mislabelled through s.mux", top)
	}

	// The fault arming is on the rx chain too, and it is asserted on the
	// chain ITSELF rather than inferred from the resolved flag:
	//
	//   - STRUCTURE: the handler net/http calls FIRST for an rx request is
	//     withPanicOnFault's closure (same check on the main listener). A
	//     bare mux there would answer "" and pass nothing.
	//   - BEHAVIOUR: exactly one arm call happened for this one rx request,
	//     and it saw prev=false — the flag was OFF on that connection
	//     goroutine until this middleware set it, which is the direct
	//     evidence that the arm ran ON the rx goroutine instead of being
	//     inherited (debug.SetPanicOnFault is per-goroutine: rdebug.go sets
	//     it on getg(), newproc1 resets it for a new goroutine).
	if got, want := arms.Load(), armedBeforeRx+1; got != want {
		t.Fatalf("arm calls = %d after one rx request (baseline %d at Start), want %d — withPanicOnFault must arm once per rx request", got, armedBeforeRx, want)
	}
	if got := armsSeeingTrue.Load() - sawTrueBeforeRx; got != 0 {
		t.Fatalf("rx-request arm calls that found the flag ALREADY armed = %d, want 0 — prev=false is what proves the arm happened on this fresh rx connection goroutine", got)
	}
	if name := handlerChainHeadName(srv.rxSrv.Handler); !strings.Contains(name, "(*Server).withPanicOnFault.func") {
		t.Fatalf("the rx listener's outermost handler is %q, want (*Server).withPanicOnFault.func… — an rx connection must be armed before any other part of its chain runs", name)
	}
	if name := handlerChainHeadName(srv.httpSrv.Handler); !strings.Contains(name, "(*Server).withPanicOnFault.func") {
		t.Fatalf("the main listener's outermost handler is %q, want (*Server).withPanicOnFault.func… — same mount, both listeners", name)
	}
	if !srv.panicOnFault {
		t.Fatal("panicOnFault is off, so neither listener's goroutines are armed")
	}
}

// ---------------------------------------------------------------------------
// D1 — the two arm points, and the wiring through the real Start().
// ---------------------------------------------------------------------------

// withPanicOnFault arms on every request when enabled, and when disabled
// returns `next` UNWRAPPED so the runtime's own behaviour is restored with
// zero residue — the rollback the plan promises, which "calls set(false)"
// would only approximate.
func TestWithPanicOnFaultArmsPerRequest(t *testing.T) {
	t.Parallel()
	var arms atomic.Int64
	srv := &Server{
		panicOnFault: true,
		setPanicOnFault: func(b bool) bool {
			arms.Add(1)
			if !b {
				t.Errorf("the per-request arm must pass true, got false")
			}
			return true
		},
	}
	mux := http.NewServeMux()
	mux.HandleFunc("/api/main", func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(http.StatusOK) })

	h := srv.withPanicOnFault(mux)
	for i := 0; i < 3; i++ {
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/main", nil))
	}
	if got := arms.Load(); got != 3 {
		t.Fatalf("arms=%d, want 3 — one per request, because the flag is PER-GOROUTINE and a keep-alive connection's next request can run on a goroutine that was never armed", got)
	}

	// Disabled: no wrapper, no call, the runtime default stands untouched.
	off := &Server{panicOnFault: false, setPanicOnFault: func(bool) bool {
		t.Fatal("a disabled server must not touch the runtime setting at all")
		return false
	}}
	if got := off.withPanicOnFault(mux); got != http.Handler(mux) {
		t.Fatalf("the disabled switch must return next unwrapped — the rollback is the ABSENCE of the arm")
	}
}

// applyPanicOnFault is the Start()-time half of D1: resolve, arm this
// goroutine, and make the resolved setting one line of log. Every env state,
// because the log is the only place the difference is visible afterwards.
func TestApplyPanicOnFaultLogsEveryState(t *testing.T) {
	cases := []struct {
		name     string
		setenv   string // "" with set=false means do not set at all
		set      bool
		wantArm  bool
		wantLine string
		// wantAlso / forbid pin the DISABLED line's WORDING, not just its
		// prefix: with the conversion OFF the operator still gets the
		// runtime's own all-goroutine dump, so that phrase has to be in the
		// line, and a phrase claiming the converted panic carries "no stack"
		// must never come back — the throw path prints stacks, every
		// goroutine's (withPanicOnFault's header carries the evidence).
		wantAlso string
		forbid   string
	}{
		{name: "default", wantArm: true, wantLine: "[runtime-health] panic-on-fault enabled (issue #130)"},
		{name: "explicit on", setenv: "1", set: true, wantArm: true, wantLine: "[runtime-health] panic-on-fault enabled (issue #130)"},
		{name: "explicit off", setenv: "0", set: true, wantArm: false, wantLine: "[runtime-health] panic-on-fault DISABLED", wantAlso: "all-goroutine dump", forbid: "no stack"},
		{name: "invalid keeps the default and warns", setenv: "yes", set: true, wantArm: true, wantLine: "[runtime-health] warning: MYWORKTREE_PANIC_ON_FAULT=\"yes\""},
	}
	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			if tc.set {
				t.Setenv(envPanicOnFault, tc.setenv)
			} else {
				// "Absent" has to be MADE absent: t.Setenv only sets, and
				// this suite shares one process, so a neighbour — or this
				// file's own earlier case — may have left it set. os.Unsetenv
				// with a restore is the only honest way to test the default
				// branch without lying to the cases after it.
				saved, had := os.LookupEnv(envPanicOnFault)
				if err := os.Unsetenv(envPanicOnFault); err != nil {
					t.Fatalf("unset %s: %v", envPanicOnFault, err)
				}
				t.Cleanup(func() {
					if had {
						_ = os.Setenv(envPanicOnFault, saved)
					} else {
						_ = os.Unsetenv(envPanicOnFault)
					}
				})
			}
			out := &lockedLogBuffer{}
			var armed []bool
			srv := &Server{
				logger: log.New(out, "", 0),
				setPanicOnFault: func(b bool) bool {
					armed = append(armed, b)
					return false
				},
			}
			srv.applyPanicOnFault()
			if len(armed) != 1 {
				t.Fatalf("setter calls=%d, want exactly 1 (%v)", len(armed), armed)
			}
			if armed[0] != tc.wantArm {
				t.Fatalf("armed=%v, want %v", armed[0], tc.wantArm)
			}
			if srv.panicOnFault != tc.wantArm {
				t.Fatalf("s.panicOnFault=%v, want %v — withPanicOnFault reads THIS field, so a wrong value here silently unarms every connection", srv.panicOnFault, tc.wantArm)
			}
			logged := out.String()
			if !strings.Contains(logged, tc.wantLine) {
				t.Fatalf("log=%q, want it to contain %q", logged, tc.wantLine)
			}
			if tc.wantAlso != "" && !strings.Contains(logged, tc.wantAlso) {
				t.Fatalf("log=%q, want it to contain %q — with the conversion OFF the operator keeps the runtime's all-goroutine dump, and this line is the only place that trade is stated afterwards", logged, tc.wantAlso)
			}
			if tc.forbid != "" && strings.Contains(logged, tc.forbid) {
				t.Fatalf("log=%q, must NOT contain %q — the throw path DOES print stacks (every goroutine, even at the default GOTRACEBACK=single), so this claim is false whatever else the line gets right", logged, tc.forbid)
			}
		})
	}
}

// The REAL Start() arms the switch, starts the loop, and meters a real
// request through the real chain; the REAL Shutdown() joins. This is the
// wiring proof the plan asks for — a hand-wired test proves nothing about
// the path the daemon actually runs.
func TestServerStartArmsPanicOnFaultAndJoinsRuntimeHealth(t *testing.T) {
	t.Setenv(envPanicOnFault, "1")
	srv, _ := newIsolatedTestServer(t, store.State{})
	// newIsolatedTestServer logs to stderr; swap in a buffer BEFORE building
	// the sampler, which captures the logger it prints through.
	out := &lockedLogBuffer{}
	srv.logger = log.New(out, "", 0)
	// Install the sampler exactly as New() would — but do NOT start it:
	// Start() is the only thing allowed to launch the loop.
	srv.runtimeHealth = newRuntimeHealth(srv.logger)
	srv.runtimeHealth.interval = 20 * time.Millisecond
	var armed []bool
	srv.setPanicOnFault = func(b bool) bool { armed = append(armed, b); return false }

	url, err := srv.Start()
	if err != nil {
		t.Fatalf("Start failed: %v", err)
	}
	if len(armed) != 1 || !armed[0] {
		t.Fatalf("Start() did not arm the caller's goroutine (calls=%v) — Start arms ONLY this goroutine (the per-connection arm lives in the middleware), so a missed call here means even that one arm is gone", armed)
	}
	if !srv.panicOnFault {
		t.Fatal("Start() left panicOnFault false, so withPanicOnFault would wrap nothing and no connection goroutine would ever be armed")
	}
	if !strings.Contains(out.String(), "[runtime-health] panic-on-fault enabled (issue #130)") {
		t.Fatalf("the switch must be observable in the log — an arm nobody can see is not a rollback path; log=%q", out.String())
	}

	// One real request over the real listener, so the meter sits on the
	// production chain (this mux is empty, so the request lands on the
	// fallback key — which is exactly the path that must not be missed).
	resp, err := http.Get(url + "api/nothing/here")
	if err != nil {
		t.Fatalf("GET through the started server failed: %v", err)
	}
	_ = resp.Body.Close()

	deadline := time.Now().Add(3 * time.Second)
	for {
		if strings.Contains(out.String(), "[runtime-health] tick ") && strings.Contains(out.String(), "reqTotal=1") {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("Start() did not launch the loop, or the loop cannot see the traffic it is supposed to attribute; log=%q", out.String())
		}
		time.Sleep(10 * time.Millisecond)
	}

	// Shutdown joins: it must return, and the loop must be gone.
	done := make(chan struct{})
	go func() { srv.Shutdown(); close(done) }()
	select {
	case <-done:
	case <-time.After(8 * time.Second):
		t.Fatal("Shutdown hung — the [runtime-health] loop was not reaped")
	}
	srv.Shutdown() // idempotent
	select {
	case <-srv.runtimeHealth.done:
	default:
		t.Fatal("Shutdown returned but the sampler's done channel is still open — that is the leaked goroutine")
	}
}

// Stop joins, is idempotent, and is sticky in both orders — the contract the
// alert sampler next door documents, applied to the loop that dumps profiles.
func TestRuntimeHealthStopJoinsAndIsSticky(t *testing.T) {
	t.Parallel()
	h, _, _ := newTestRuntimeHealth(t, 10*time.Millisecond, 1<<31-1)

	stopped := make(chan struct{})
	go func() { h.Stop(); close(stopped) }()
	select {
	case <-stopped:
	case <-time.After(2 * time.Second):
		t.Fatal("Stop-before-Start hung — there was nothing to join")
	}

	// A Start after Stop stays inert: a loop must not outlive the shutdown
	// that stopped it, and a dump written after Shutdown would be written
	// into a log the process is closing.
	h.Start()
	h.mu.Lock()
	started := h.started
	h.mu.Unlock()
	if started {
		t.Fatal("Start() resurrected a stopped sampler")
	}

	// The join proof on a loop that DID run.
	h2, _, _ := newTestRuntimeHealth(t, 10*time.Millisecond, 1<<31-1)
	baseline := runtime.NumGoroutine()
	h2.Start()
	time.Sleep(30 * time.Millisecond)
	h2.Stop()
	select {
	case <-h2.done:
	default:
		t.Fatal("Stop returned without the loop closing done")
	}
	// NumGoroutine is a whole-process count and this package runs parallel
	// tests, so the slack absorbs a neighbour's teardown; the exact proof is
	// the done channel above, this is the corroborating one.
	deadline := time.Now().Add(2 * time.Second)
	for runtime.NumGoroutine() > baseline+2 {
		if time.Now().After(deadline) {
			t.Fatalf("goroutine count stayed at %d against a baseline of %d after Stop joined", runtime.NumGoroutine(), baseline)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

// A zero interval launches NOTHING — the env's ≤0 is the off switch, and
// "off" has to mean no goroutine, not a quieter goroutine. The fault arming
// and the meter are separate switches and must still work (asserted below in
// the same test, because the independence of the three knobs is the point of
// having three).
func TestRuntimeHealthZeroIntervalLaunchesNoLoop(t *testing.T) {
	t.Parallel()
	h, out, _ := newTestRuntimeHealth(t, 0, 1<<31-1)
	h.Start()
	h.mu.Lock()
	started := h.started
	h.mu.Unlock()
	if started {
		t.Fatal("a zero interval still launched the loop — the env's ≤0 would be a lie")
	}
	h.Stop() // must not hang: nothing to join
	if strings.Contains(out.String(), "[runtime-health] tick ") {
		t.Fatalf("ticked with the loop off, log=%q", out.String())
	}

	// …and the meter still meters with the tick loop off.
	mux := http.NewServeMux()
	mux.HandleFunc("/api/main", func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(http.StatusOK) })
	rec := httptest.NewRecorder()
	(&Server{mux: mux, runtimeHealth: h}).withRuntimeHealth(mux).ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/main", nil))
	if _, total, _ := h.snapshot(); total != 1 {
		t.Fatalf("reqTotal=%d, want 1 — silencing the tick must not silence the attribution", total)
	}
}

// ---------------------------------------------------------------------------
// D3 — the ring pair on the tty lines (compiled in both variants; the
// lines themselves only exist under -tags ttymetrics, so the assertions that
// need them skip loudly rather than passing silently).
// ---------------------------------------------------------------------------

func TestTTYMetricsRingFieldsOnSummaryLine(t *testing.T) {
	if !ttyMetricsBuild {
		t.Skip("[tty-metrics] logging is compiled out of this build variant; run with -tags ttymetrics to pin the appended pair")
	}
	t.Parallel()

	// The incident's own numbers, as they must read on the summary line:
	// a 256 MiB ring with the cursor 194 MiB behind the head.
	var plain bytes.Buffer
	m := &ttyConnMetrics{
		openedAt:     time.Now(),
		headAtStart:  194 << 20,
		ringCapBytes: 256 << 20,
		ringLagBytes: 194 << 20,
		ringLagKnown: true,
	}
	m.logSummary(log.New(&plain, "", 0), "inst-ring")
	got := plain.String()
	if !strings.HasSuffix(got, " ringCapBytes=268435456 ringLagBytes=203423744\n") {
		t.Fatalf("conn-closed line = %q, want it to END with the ring pair (256 MiB cap, 194 MiB lag), with nothing before it moved", got)
	}
	if !strings.Contains(got, " logicalBytes=0 ringCapBytes=") {
		t.Fatalf("the pair must ride AFTER the pre-existing logicalBytes column, got %q", got)
	}

	// The unknown case: a connection that never reached completeHandshake
	// still reaches the deferred summary, and the pair must print -1/-1 —
	// never 0, which reads as "caught up on a zero-byte ring".
	var unknown bytes.Buffer
	u := newTTYMeteredConn(nil, nil, "inst-unknown")
	u.m.logSummary(log.New(&unknown, "", 0), "inst-unknown")
	if got := unknown.String(); !strings.HasSuffix(got, " ringCapBytes=-1 ringLagBytes=-1\n") {
		t.Fatalf("unknown-ring conn-closed line = %q, want ringCapBytes=-1 ringLagBytes=-1 (unknown is -1, not 0)", got)
	}

	// And the handshake line carries the same pair, appended after
	// replayLogicalBytes and BEFORE the conditional compress flag.
	var hs bytes.Buffer
	c := newTTYMeteredConn(nil, nil, "inst-hs")
	c.m.mode = "catchup"
	c.m.headAtStart = 5000
	c.m.ringCapBytes = 8192
	c.m.ringLagBytes, c.m.ringLagKnown = ttyRingLagBytes(5000, 2000)
	c.m.logHandshake(log.New(&hs, "", 0), "inst-hs", 2000)
	if got := hs.String(); !strings.HasSuffix(got, " replayLogicalBytes=0 ringCapBytes=8192 ringLagBytes=3000\n") {
		t.Fatalf("handshake line = %q, want it to END with replayLogicalBytes then the ring pair", got)
	}

	// compress=true must STILL be the last token on a compressed connection —
	// the pair slots in before it, it does not displace it.
	var cs bytes.Buffer
	cc := newTTYMeteredConn(nil, nil, "inst-cs")
	cc.m.compress = true
	cc.m.headAtStart = 900
	cc.m.ringCapBytes = 1024
	cc.m.ringLagBytes, cc.m.ringLagKnown = ttyRingLagBytes(900, 100)
	cc.m.logHandshake(log.New(&cs, "", 0), "inst-cs", 100)
	if got := cs.String(); !strings.HasSuffix(got, " ringCapBytes=1024 ringLagBytes=800 compress=true\n") {
		t.Fatalf("compressed handshake line = %q, want compress=true to stay last with the pair before it", got)
	}
}

// The lag arithmetic itself, across every sentinel `since` can carry. Pure,
// so it runs in BOTH build variants (no skip): this is the arithmetic the
// two lines print, and a wrong sign here is a wrong number in production.
func TestTTYRingLagBytesArithmetic(t *testing.T) {
	t.Parallel()
	cases := []struct {
		head, since int64
		want        int64
		wantOK      bool
		why         string
	}{
		{head: 1000, since: 400, want: 600, wantOK: true, why: "the ordinary case: head minus since"},
		{head: 1000, since: 1000, want: 0, wantOK: true, why: "caught up exactly"},
		{head: 400, since: 1000, want: 0, wantOK: true, why: "a cursor ahead of head is not behind"},
		{head: -1, since: 0, want: -1, wantOK: false, why: "EndOffset probe error -> unknown (a stopped instance answers head 0, not -1)"},
		{head: 1000, since: -1, want: -1, wantOK: false, why: "cursorless first connect -> unknown, NOT the head itself"},
		{head: 1000, since: sinceFollowLiveEnd, want: -1, wantOK: false, why: "painted-screen-no-cursor -> unknown"},
		{head: -1, since: -1, want: -1, wantOK: false, why: "both unknown"},
	}
	for _, tc := range cases {
		tc := tc
		t.Run(tc.why, func(t *testing.T) {
			t.Parallel()
			got, ok := ttyRingLagBytes(tc.head, tc.since)
			if got != tc.want || ok != tc.wantOK {
				t.Fatalf("ttyRingLagBytes(%d, %d) = %d/%v, want %d/%v — %s", tc.head, tc.since, got, ok, tc.want, tc.wantOK, tc.why)
			}
		})
	}
}

// ---------------------------------------------------------------------------
// helpers
// ---------------------------------------------------------------------------

// waitForLogLine polls an injected buffer for the LAST line containing want.
// The sampler writes from its own goroutine, so a test cannot assume the line
// exists the moment its own call returns.
func waitForLogLine(t *testing.T, buf *lockedLogBuffer, want string, d time.Duration) string {
	t.Helper()
	deadline := time.Now().Add(d)
	for {
		if s := buf.String(); strings.Contains(s, want) {
			return lastLineContaining(t, s, want)
		}
		if time.Now().After(deadline) {
			t.Fatalf("log line containing %q never appeared within %s; log so far = %q", want, d, buf.String())
		}
		time.Sleep(5 * time.Millisecond)
	}
}

func lastLineContaining(t *testing.T, s, want string) string {
	t.Helper()
	if i := strings.LastIndex(s, want); i >= 0 {
		if end := strings.IndexByte(s[i:], '\n'); end >= 0 {
			return s[i : i+end]
		}
		return s[i:]
	}
	t.Fatalf("no line containing %q in %q", want, s)
	return ""
}

func countPrefixedLines(s, prefix string) int {
	n := 0
	for _, line := range strings.Split(s, "\n") {
		if strings.HasPrefix(line, prefix) {
			n++
		}
	}
	return n
}

// handlerChainHeadName names the function net/http calls FIRST for a mounted
// handler: the code pointer behind an http.Handler. It is how a test inspects
// a middleware chain instead of trusting a flag — a middleware closure answers
// something like "myworktree/internal/app.(*Server).withPanicOnFault.func1",
// while a bare *http.ServeMux answers nothing useful (its address is not a
// function's), so a chain that skipped the arming step cannot pass a check
// for that step's name by accident.
func handlerChainHeadName(h http.Handler) string {
	if h == nil {
		return "<nil>"
	}
	v := reflect.ValueOf(h)
	switch v.Kind() {
	case reflect.Func, reflect.Ptr:
	default:
		// reflect.Value.Pointer panics on other kinds; a value-type handler
		// gets a name that cannot match any middleware's, so the assertion
		// still says what is wrong instead of crashing the run.
		return "<unsupported-handler-kind:" + v.Kind().String() + ">"
	}
	f := runtime.FuncForPC(v.Pointer())
	if f == nil {
		return "<not-a-func:" + v.Type().String() + ">"
	}
	return f.Name()
}

func parseOrFail(t *testing.T, name, s string) int64 {
	t.Helper()
	n, err := strconv.ParseInt(s, 10, 64)
	if err != nil {
		t.Fatalf("%s=%q is not an integer: %v", name, s, err)
	}
	return n
}
