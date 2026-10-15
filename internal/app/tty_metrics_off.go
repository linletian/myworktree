//go:build !ttymetrics

package app

// The STABLE-BUILD half of the instrumentation split (no -tags ttymetrics).
// timed is a passthrough and the log methods are empty, so the compiler
// inlines every call site down to the bare socket write plus the cheap
// counter increments in tty_metrics.go, and the EndOffset probes are
// const-gated away at their app.go call sites — stable releases ship no
// timing, no logging, no manager reads, and no [tty-metrics] output, and
// withServerRevision clears any stale mw_tty_metrics cookie instead of
// planting one, so the client half stays silent too. What remains is a
// handful of dead field stores (mode/budgetExhausted markers), all single
// integer/pointer writes below noise.

import "log"

// ttyMetricsBuild is the build-channel switch for the rest of the app.
const ttyMetricsBuild = false

func (c *ttyMeteredConn) timed(op string, n int, write func() error) error {
	return write()
}

func (m *ttyConnMetrics) logHandshake(logger *log.Logger, id string, since int64) {}

func (m *ttyConnMetrics) logOverflow(logger *log.Logger, id string, headNow int64) {}

func (m *ttyConnMetrics) logSummary(logger *log.Logger, id string) {}
