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
//
// The wireBytes/logicalBytes pair in the slow-write line is the running
// total BEFORE the frame currently being timed is counted (timed runs
// inside the metered WriteBinary, whose counters only advance once it
// returns): the blocked frame itself rides the line's own bytes= field.
// The new logicalBytes field is sampled at the SAME point as the existing
// binBytes — a snapshot before the current frame is counted — so the two
// stay structurally identical (review R1: same sampling point, accepted
// deliberately).
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
			c.logger.Printf("[tty-metrics] slow-write id=%s op=%s bytes=%d blockMs=%d wireBytes=%d logicalBytes=%d",
				c.id, op, n, d.Milliseconds(), c.m.binBytes, c.m.logicalBytes)
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
//
// replayLogicalBytes (review R1) is the LOGICAL counterpart of
// replayBytes, appended to the frozen column area BEFORE the conditional
// compress flag so the frozen layout never shifts: on a compressed
// connection replayBytes is the WIRE (deflate) count — name frozen,
// historical readings comparable — while replayLogicalBytes is the ring/
// logical bytes the replay delivered, which is what FIX-PLAN §五.3.b's
// drain-rate arithmetic (logicalBytes/replayMs) computes off. On a
// plaintext connection the two are equal by construction.
func (m *ttyConnMetrics) logHandshake(logger *log.Logger, id string, since int64) {
	if logger == nil {
		return
	}
	// compress=true is APPENDED, and only for a connection that negotiated
	// the `compress` cap (issue #114, decision 5a / R14): a connection
	// without the cap emits this line exactly as it did before the cap
	// existed (undeclared → not sent, the same opt-in invisibility as the
	// window fields' semantics). Column layout before it stays frozen.
	compressField := ""
	if m.compress {
		compressField = " compress=true"
	}
	logger.Printf("[tty-metrics] handshake id=%s mode=%s since=%d headAtStart=%d published=%d replayBytes=%d replayMs=%d budgetExhausted=%v windowed=%v droppedFromWindow=%d replayLogicalBytes=%d%s",
		id, m.mode, since, m.headAtStart, m.publishedOffset,
		m.replayBytes, m.replayDur.Milliseconds(), m.budgetExhausted,
		m.windowed, m.droppedFromWindow, m.replayLogicalBytes, compressField)
}

// logOverflow emits the subscriber-overflow line next to the existing
// human-readable one (which a test pins verbatim — do not merge them).
// liveBytes/liveFrames are what THIS connection delivered between
// subscribing and the overflow: liveBytes/subAgeMs is the consumer's
// effective drain rate, the number the 64KB queue capacity gets compared
// against. headNow (-1 on error) minus the client's cursor (client-side
// close line) sizes the backlog the next reconnect will replay.
//
// liveLogicalBytes (review R1) is the LOGICAL counterpart of liveBytes,
// computed the same way (logicalBytes − replayLogicalBytes) so the two
// families are structurally identical: on a compressed connection liveBytes
// is the WIRE drain and liveLogicalBytes the logical drain (the number
// that pairs with the client's cursor), on a plaintext connection they are
// equal.
func (m *ttyConnMetrics) logOverflow(logger *log.Logger, id string, headNow int64) {
	if logger == nil {
		return
	}
	var subAgeMs int64
	if !m.subscribedAt.IsZero() {
		subAgeMs = time.Since(m.subscribedAt).Milliseconds()
	}
	logger.Printf("[tty-metrics] overflow id=%s subAgeMs=%d liveBytes=%d liveFrames=%d headNow=%d maxWriteBlockMs=%d slowWrites=%d liveLogicalBytes=%d",
		id, subAgeMs, m.binBytes-m.replayBytes, m.binFrames,
		headNow, m.maxWriteBlock.Milliseconds(), m.slowWrites,
		m.logicalBytes-m.replayLogicalBytes)
}

// logSummary emits the per-connection line on handler exit, whatever the
// exit path — normal close, overflow teardown, write error, read deadline.
func (m *ttyConnMetrics) logSummary(logger *log.Logger, id string) {
	if logger == nil {
		return
	}
	logger.Printf("[tty-metrics] conn-closed id=%s aliveMs=%d binBytes=%d binFrames=%d txtFrames=%d pingFrames=%d slowWrites=%d maxWriteBlockMs=%d totalWriteBlockMs=%d wireBytes=%d logicalBytes=%d",
		id, time.Since(m.openedAt).Milliseconds(), m.binBytes, m.binFrames,
		m.txtFrames, m.pingFrames, m.slowWrites,
		m.maxWriteBlock.Milliseconds(), m.totalWriteBlock.Milliseconds(),
		m.binBytes, m.logicalBytes)
}
