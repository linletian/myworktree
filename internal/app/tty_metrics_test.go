//go:build ttymetrics

package app

// Tests for the issue #114 [tty-metrics] instrumentation — the REAL
// implementation, so this whole file is compiled only under
// -tags ttymetrics (the beta build variant; go-ci.yml runs
// ./internal/app/ with the tag). The untagged suite never sees these
// tests, and the stable variant's silence is covered by the binary-level
// check plus the cookie-clearing test in tty_metrics_cookie_off_test.go.

import (
	"net/http"
	"net/http/httptest"
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
func TestTTYMetricsOverflowLine(t *testing.T) {
	t.Parallel()
	k := newTTYHandshakeKind(1024)
	addr, _, instID, logOut := ttyWSTestServer(t, k)

	_, _, _ = dialHandshake(t, addr, ttyWSPath(instID, "caps=sync"))
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
