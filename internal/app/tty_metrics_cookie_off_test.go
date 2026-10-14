//go:build !ttymetrics

package app

// Stable-variant counterpart of tty_metrics_test.go (compiled without
// -tags ttymetrics, i.e. what release.yml ships): no instrumentation log
// lines exist to test, but the middleware still owns one behaviour — a
// stale mw_tty_metrics cookie, planted by a beta daemon earlier in the
// same browser session, must be cleared so the page's client half goes
// silent instead of logging without a server counterpart (review F1).

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestTTYMetricsCookieClearedWhenStale(t *testing.T) {
	t.Parallel()
	next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(http.StatusNoContent) })

	withCookie := httptest.NewRequest(http.MethodGet, "/", nil)
	withCookie.AddCookie(&http.Cookie{Name: "mw_tty_metrics", Value: "1"})
	rec := httptest.NewRecorder()
	(&Server{}).withServerRevision(next).ServeHTTP(rec, withCookie)
	got := rec.Header().Get("Set-Cookie")
	if !strings.Contains(got, "mw_tty_metrics=") || !strings.Contains(got, "Max-Age=0") {
		t.Fatalf("stable build must expire a stale mw_tty_metrics cookie, got Set-Cookie %q", got)
	}

	rec2 := httptest.NewRecorder()
	(&Server{}).withServerRevision(next).ServeHTTP(rec2, httptest.NewRequest(http.MethodGet, "/", nil))
	if got := rec2.Header().Get("Set-Cookie"); strings.Contains(got, "mw_tty_metrics") {
		t.Fatalf("stable build must never plant the cookie, got %q", got)
	}
}
