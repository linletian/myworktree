//go:build ttymetrics

package app

// Tests for the issue #114 [tty-metrics] instrumentation — the REAL
// implementation, so this whole file is compiled only under
// -tags ttymetrics (the beta build variant; go-ci.yml runs
// ./internal/app/ with the tag). The untagged suite never sees these
// tests, and the stable variant's silence is covered by the binary-level
// check plus the cookie-clearing test in tty_metrics_cookie_off_test.go.

import (
	"bytes"
	"log"
	"net/http"
	"net/http/httptest"
	"regexp"
	"strings"
	"testing"
	"time"
)

// waitForTTYMetricsLine polls the injected log buffer until the wanted
// substring appears. The metric lines are written from the handler
// goroutine — logHandshake AFTER the closing sync frame that dialHandshake
// returns on, conn-closed from a defer after the client hangs up — so the
// test cannot assume the line exists the moment its own call returns.
func waitForTTYMetricsLine(t *testing.T, logOut *lockedLogBuffer, want string, d time.Duration) {
	t.Helper()
	deadline := time.Now().Add(d)
	for !strings.Contains(logOut.String(), want) {
		if time.Now().After(deadline) {
			t.Fatalf("log line containing %q never appeared within %s; log so far = %q", want, d, logOut.String())
		}
		time.Sleep(5 * time.Millisecond)
	}
}

// The handshake line carries the mode, the client cursor and the replay
// accounting; the conn-closed line fires on ANY handler exit (here: a
// clean client hang-up), which is what makes per-connection write-block
// data collectible in the field.
func TestTTYMetricsHandshakeAndConnClosedLines(t *testing.T) {
	t.Parallel()
	k := newTTYHandshakeKind(1024)
	addr, _, instID, logOut := ttyWSTestServer(t, k)

	c, _, _ := dialHandshake(t, addr, ttyWSPath(instID, "caps=sync"))
	waitForTTYMetricsLine(t, logOut, "[tty-metrics] handshake id="+instID+" mode=tail since=-1 ", 5*time.Second)
	// A fresh instance replays an empty tail: the budget must not be
	// reported exhausted, and the replay cost is zero bytes.
	waitForTTYMetricsLine(t, logOut, "replayBytes=0", 5*time.Second)
	if logged := logOut.String(); strings.Contains(logged, "budgetExhausted=true") {
		t.Fatalf("empty tail must not exhaust the replay budget; log = %q", logged)
	}

	_ = c.Close()
	waitForTTYMetricsLine(t, logOut, "[tty-metrics] conn-closed id="+instID+" aliveMs=", 5*time.Second)
}

// The overflow line rides next to the pinned human-readable one (which
// TestHandleInstanceTTYWS_SubscriberOverflowClosesConnectionWith1013
// asserts) and carries the drain-rate fields the fix design hinges on.
// The publish-outrun loop mirrors that test: a client that never reads
// overflows the subscription queue.
//
// v8 P3: this arm also seeds the ring so the handshake replays frames
// BEFORE the live phase. liveFrames must then count ONLY the live frames —
// strictly fewer than the conn-closed binFrames, which is every binary
// frame ever written (replay included, the queue drains nothing after the
// overflow close). The old reading shipped binFrames here verbatim, so
// liveFrames==binFrames on any replay>0 connection was the bug; the
// subtraction of the handshake replayFrames snapshot is what makes this
// assertion fail on the mutant and pass on the fix, deterministically
// (replay bytes > 0 is pinned from the handshake line).
func TestTTYMetricsOverflowLine(t *testing.T) {
	t.Parallel()
	k := newTTYHandshakeKind(1 << 20)
	addr, _, instID, logOut := ttyWSTestServer(t, k)
	k.buf.WriteString(strings.Repeat("s", 128*1024)) // seed: the handshake replays it

	_, replay, _ := dialHandshake(t, addr, ttyWSPath(instID, "caps=sync"))
	if len(replay) == 0 {
		t.Fatal("seeded replay delivered nothing — the replay>0 precondition of the liveFrames discriminator is unmet")
	}
	k.waitSubscribers(t, 5*time.Second)

	chunk := strings.Repeat("x", 32*1024)
	deadline := time.Now().Add(20 * time.Second)
	for k.subscriberCount() > 0 {
		if time.Now().After(deadline) {
			t.Fatal("overflow never happened: the slow consumer was not disconnected")
		}
		k.publish(chunk)
	}

	waitForTTYMetricsLine(t, logOut, "[tty-metrics] overflow id="+instID+" subAgeMs=", 5*time.Second)
	waitForTTYMetricsLine(t, logOut, "liveBytes=", 5*time.Second)
	// The conn-closed line fires right after the overflow teardown (the
	// handler closes with 1013 and its deferred summary runs): no binary
	// frame is written after the overflow line, so binFrames there is the
	// same total the overflow line saw.
	waitForTTYMetricsLine(t, logOut, "[tty-metrics] conn-closed id="+instID, 10*time.Second)

	over := tailLine(t, logOut.String(), "[tty-metrics] overflow id="+instID)
	closed := tailLine(t, logOut.String(), "[tty-metrics] conn-closed id="+instID)
	m := regexp.MustCompile(`liveBytes=(\d+) liveFrames=(\d+)`).FindStringSubmatch(over)
	if m == nil {
		t.Fatalf("overflow line carries no liveBytes=/liveFrames= pair: %q", over)
	}
	liveBytes := ttyParseInt(t, "liveBytes", m[1])
	liveFrames := ttyParseInt(t, "liveFrames", m[2])
	cm := regexp.MustCompile(`binBytes=(\d+) binFrames=(\d+)`).FindStringSubmatch(closed)
	if cm == nil {
		t.Fatalf("conn-closed line carries no binBytes=/binFrames= pair: %q", closed)
	}
	binBytes := ttyParseInt(t, "binBytes", cm[1])
	binFrames := ttyParseInt(t, "binFrames", cm[2])

	hs := tailLine(t, logOut.String(), "[tty-metrics] handshake id="+instID)
	rm := regexp.MustCompile(`replayBytes=(\d+)`).FindStringSubmatch(hs)
	if rm == nil {
		t.Fatalf("handshake line carries no replayBytes=: %q", hs)
	}
	replayBytes := ttyParseInt(t, "replayBytes", rm[1])
	if replayBytes <= 0 {
		t.Fatalf("replayBytes=%d — the seeded replay must be visible: %q", replayBytes, hs)
	}
	// liveFrames==0 is a LEGAL reading here (review B NIT-1): the
	// close-policy publish closes AND drains the channel, so a parked
	// handler can wake on ok=false having written no live frame at all
	// (the app.go overflow branch documents this race). The discriminator
	// is the next assertion — liveFrames >= binFrames is exactly the old
	// raw-binFrames reading and must never hold while replay>0 — and the
	// exact N+M arithmetic is pinned by
	// TestTTYMetricsOverflowLiveFramesExactBothArms.
	if liveFrames >= binFrames {
		t.Fatalf("liveFrames=%d >= binFrames=%d — replay frames leaked into liveFrames; it must be binFrames−replayFrames (v8 P3): overflow=%q closed=%q", liveFrames, binFrames, over, closed)
	}
	if liveBytes >= binBytes {
		t.Fatalf("liveBytes=%d >= binBytes=%d — replay bytes leaked into liveBytes: %q", liveBytes, binBytes, over)
	}
}

// TestTTYMetricsOverflowLiveFramesExactBothArms pins the v8 P3 column
// arithmetic EXACTLY at the emitter, both arms: a handshake that replayed
// N frames + M live frames reports liveFrames==M — never N+M — and the
// byte columns stay in their own families (plaintext: wire==logical;
// compressed: wire<logical with the SAME frame count, so the two arms'
// liveFrames readings are identical by construction).
func TestTTYMetricsOverflowLiveFramesExactBothArms(t *testing.T) {
	t.Parallel()
	const (
		n       = int64(7) // handshake replay frames
		m       = int64(5) // live frames after the handshake
		liveB   = int64(512)
		replayB = int64(4096)
	)

	// Plaintext arm: wire and logical are equal by construction.
	var plain bytes.Buffer
	pm := &ttyConnMetrics{
		subscribedAt:       time.Now(),
		binFrames:          n + m,
		replayFrames:       n,
		binBytes:           n*replayB + m*liveB,
		replayBytes:        n * replayB,
		logicalBytes:       n*replayB + m*liveB,
		replayLogicalBytes: n * replayB,
	}
	pm.logOverflow(log.New(&plain, "", 0), "inst1", n*replayB+m*liveB)
	pf := regexp.MustCompile(`liveBytes=(\d+) liveFrames=(\d+) .*liveLogicalBytes=(\d+)`).FindStringSubmatch(plain.String())
	if pf == nil {
		t.Fatalf("plaintext overflow line malformed: %q", plain.String())
	}
	if got := ttyParseInt(t, "liveFrames", pf[2]); got != m {
		t.Fatalf("plaintext liveFrames=%d, want exactly M=%d (N=%d replay frames must be excluded): %q", got, m, n, plain.String())
	}
	if got := ttyParseInt(t, "liveBytes", pf[1]); got != m*liveB {
		t.Fatalf("plaintext liveBytes=%d, want %d: %q", got, m*liveB, plain.String())
	}
	if got := ttyParseInt(t, "liveLogicalBytes", pf[3]); got != m*liveB {
		t.Fatalf("plaintext liveLogicalBytes=%d, want liveBytes=%d equal on plaintext: %q", got, m*liveB, plain.String())
	}

	// Compressed arm: SAME frame counts, shrunk wire bytes — liveFrames
	// must read identically to the plaintext arm (frames are frame counts,
	// unaffected by the wire/logical split), while liveBytes follows the
	// wire family and liveLogicalBytes the logical family.
	var comp bytes.Buffer
	cm := &ttyConnMetrics{
		subscribedAt:       time.Now(),
		compress:           true,
		binFrames:          n + m,
		replayFrames:       n,
		binBytes:           n*400 + m*64,
		replayBytes:        n * 400,
		logicalBytes:       n*replayB + m*liveB,
		replayLogicalBytes: n * replayB,
	}
	cm.logOverflow(log.New(&comp, "", 0), "inst2", n*replayB+m*liveB)
	cf := regexp.MustCompile(`liveBytes=(\d+) liveFrames=(\d+) .*liveLogicalBytes=(\d+)`).FindStringSubmatch(comp.String())
	if cf == nil {
		t.Fatalf("compressed overflow line malformed: %q", comp.String())
	}
	if got := ttyParseInt(t, "liveFrames", cf[2]); got != m {
		t.Fatalf("compressed liveFrames=%d, want exactly M=%d — the two arms' frame readings must agree: %q", got, m, comp.String())
	}
	if got := ttyParseInt(t, "liveBytes", cf[1]); got != m*64 {
		t.Fatalf("compressed liveBytes=%d, want the WIRE live total %d: %q", got, m*64, comp.String())
	}
	if got := ttyParseInt(t, "liveLogicalBytes", cf[3]); got != m*liveB {
		t.Fatalf("compressed liveLogicalBytes=%d, want the LOGICAL live total %d: %q", got, m*liveB, comp.String())
	}
}

// Beta builds plant the mw_tty_metrics cookie (the client-side arming
// signal) exactly once per browser: set when absent, untouched when the
// browser already carries it.
func TestTTYMetricsCookiePlantedOnlyWhenAbsent(t *testing.T) {
	t.Parallel()
	next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(http.StatusNoContent) })

	rec := httptest.NewRecorder()
	(&Server{}).withServerRevision(next).ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/", nil))
	if got := rec.Header().Get("Set-Cookie"); !strings.Contains(got, "mw_tty_metrics=1") {
		t.Fatalf("beta build must plant mw_tty_metrics=1, got Set-Cookie %q", got)
	}

	withCookie := httptest.NewRequest(http.MethodGet, "/", nil)
	withCookie.AddCookie(&http.Cookie{Name: "mw_tty_metrics", Value: "1"})
	rec2 := httptest.NewRecorder()
	(&Server{}).withServerRevision(next).ServeHTTP(rec2, withCookie)
	if got := rec2.Header().Get("Set-Cookie"); strings.Contains(got, "mw_tty_metrics") {
		t.Fatalf("cookie already present — must not be re-set per response, got %q", got)
	}
}
