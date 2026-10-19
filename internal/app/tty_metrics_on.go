//go:build ttymetrics

package app

// The REAL instrumentation, compiled only with -tags ttymetrics (the beta
// release workflow). See tty_metrics.go for the shared machinery and the
// gating contract.

import (
	"log"
	"time"
)

// ttyMetricsBuild tells the rest of the app the instrumentation is compiled
// in — withServerRevision uses it to decide whether to hand the page the
// mw_tty_metrics cookie that switches the client half on.
const ttyMetricsBuild = true

// ttySlowWriteThreshold is the write-blocking duration that earns its own
// log line. A healthy socket writes a ≤64KB frame in well under a
// millisecond; anything past a second means TCP backpressure — the peer's
// receive path (kernel buffers → browser → xterm.js) is the bottleneck,
// which is exactly the slow-consumer state issue #114 needs to measure.
const ttySlowWriteThreshold = time.Second

// timed runs one socket write and records how long it blocked. The slow-write
// log fires INSIDE the write path on purpose: a connection that later dies
// without ever reaching the overflow or close-summary line still leaves its
// backpressure evidence behind.
func (c *ttyMeteredConn) timed(op string, n int, write func() error) error {
	start := time.Now()
	err := write()
	d := time.Since(start)
	c.m.totalWriteBlock += d
	if d > c.m.maxWriteBlock {
		c.m.maxWriteBlock = d
	}
	if d >= ttySlowWriteThreshold {
		c.m.slowWrites++
		if c.logger != nil {
			c.logger.Printf("[tty-metrics] slow-write id=%s op=%s bytes=%d blockMs=%d",
				c.id, op, n, d.Milliseconds())
		}
	}
	return err
}

// logHandshake emits the per-handshake line once the live subscription is
// in place. replayBytes is read off the connection's binary counter at that
// instant, which is exact for all three handshake modes (tail, catch-up,
// follow-live-end) without per-branch accounting.
//
// The two issue #114 fields are APPENDED at the end of the line, and every
// field name and position before them is frozen: this line is a data source
// (DATA-COLLECTION.md), so the column layout the collector's grep/awk and
// the decision matrix already depend on is part of its contract. windowed
// is the OR of the two window truncations (start clamp / total cap),
// droppedFromWindow their sum — see ttyConnMetrics for the exact arithmetic
// and for why a windowed handshake can legitimately report 0.
func (m *ttyConnMetrics) logHandshake(logger *log.Logger, id string, since int64) {
	if logger == nil {
		return
	}
	logger.Printf("[tty-metrics] handshake id=%s mode=%s since=%d headAtStart=%d published=%d replayBytes=%d replayMs=%d budgetExhausted=%v windowed=%v droppedFromWindow=%d",
		id, m.mode, since, m.headAtStart, m.publishedOffset,
		m.replayBytes, m.replayDur.Milliseconds(), m.budgetExhausted,
		m.windowed, m.droppedFromWindow)
}

// logOverflow emits the subscriber-overflow line next to the existing
// human-readable one (which a test pins verbatim — do not merge them).
// liveBytes/liveFrames are what THIS connection delivered between
// subscribing and the overflow: liveBytes/subAgeMs is the consumer's
// effective drain rate, the number the 64KB queue capacity gets compared
// against. headNow (-1 on error) minus the client's cursor (client-side
// close line) sizes the backlog the next reconnect will replay.
func (m *ttyConnMetrics) logOverflow(logger *log.Logger, id string, headNow int64) {
	if logger == nil {
		return
	}
	var subAgeMs int64
	if !m.subscribedAt.IsZero() {
		subAgeMs = time.Since(m.subscribedAt).Milliseconds()
	}
	logger.Printf("[tty-metrics] overflow id=%s subAgeMs=%d liveBytes=%d liveFrames=%d headNow=%d maxWriteBlockMs=%d slowWrites=%d",
		id, subAgeMs, m.binBytes-m.replayBytes, m.binFrames,
		headNow, m.maxWriteBlock.Milliseconds(), m.slowWrites)
}

// logSummary emits the per-connection line on handler exit, whatever the
// exit path — normal close, overflow teardown, write error, read deadline.
func (m *ttyConnMetrics) logSummary(logger *log.Logger, id string) {
	if logger == nil {
		return
	}
	logger.Printf("[tty-metrics] conn-closed id=%s aliveMs=%d binBytes=%d binFrames=%d txtFrames=%d pingFrames=%d slowWrites=%d maxWriteBlockMs=%d totalWriteBlockMs=%d",
		id, time.Since(m.openedAt).Milliseconds(), m.binBytes, m.binFrames,
		m.txtFrames, m.pingFrames, m.slowWrites,
		m.maxWriteBlock.Milliseconds(), m.totalWriteBlock.Milliseconds())
}
