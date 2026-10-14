package app

// Diagnostic instrumentation for issue #114 (tty websocket subscriber
// overflow → 1013 reconnect loop). Everything here is OBSERVATIONAL: it
// times writes and counts bytes, logs under the "[tty-metrics]" prefix,
// and changes no wire behaviour. The collected fields answer the data
// questions the fix design hinges on:
//
//   - How big is each reconnect's replay, and does it hit the 8MB budget?
//     (handshake line: replayBytes / budgetExhausted / headAtStart - since)
//   - How long does the server spend blocked writing to the socket — is
//     the consumer slow because the network is slow or because the browser
//     stops reading? (slow-write lines, maxWriteBlockMs, totalWriteBlockMs)
//   - At overflow time, how long had the subscription lived and how much
//     live output had it delivered — i.e. the consumer's effective drain
//     rate? (overflow line: subAgeMs, liveBytes)
//
// Client-side counterparts (visibility state, per-connection rx bytes,
// xterm.js parse backlog) live in internal/ui/static/index.html under the
// same log prefix. Remove or gate this file once the fix lands and the
// data has answered those questions.

import (
	"log"
	"time"

	"myworktree/internal/ws"
)

// ttySlowWriteThreshold is the write-blocking duration that earns its own
// log line. A healthy socket writes a ≤64KB frame in well under a
// millisecond; anything past a second means TCP backpressure — the peer's
// receive path (kernel buffers → browser → xterm.js) is the bottleneck,
// which is exactly the slow-consumer state issue #114 needs to measure.
const ttySlowWriteThreshold = time.Second

// ttyConnMetrics accumulates one TTY WebSocket connection's counters. All
// fields are touched only from the handler goroutine (completeHandshake and
// the select loop are the same goroutine; the read goroutine never writes
// through the metered conn), so plain fields suffice — no atomics.
type ttyConnMetrics struct {
	openedAt time.Time

	// Write path. binBytes counts BINARY frames only, deliberately: the
	// client advances its #87 cursor by binary frame length, so binBytes
	// is the number comparable with the client's cursor, while text
	// (sync/ping/pong/resize) and RFC 6455 ping frames are control
	// traffic the cursor never sees.
	binBytes        int64
	binFrames       int64
	txtFrames       int64
	pingFrames      int64
	slowWrites      int64
	maxWriteBlock   time.Duration
	totalWriteBlock time.Duration

	// Handshake (filled in by completeHandshake).
	mode            string // "follow-live-end" | "tail" | "catchup"
	handshakeStart  time.Time
	headAtStart     int64 // ring head when the handshake began; -1 unknown
	budgetExhausted bool
	publishedOffset int64
	replayBytes     int64 // binBytes snapshot at handshake end
	replayDur       time.Duration
	subscribedAt    time.Time // zero until SubscribeOutput succeeds
}

// ttyMeteredConn wraps *ws.Conn and times every write on the TTY handler
// path. Embedding keeps the rest of the handler source untouched: deadline
// setters, ReadMessage, WriteClose, WritePong and Close promote unchanged.
type ttyMeteredConn struct {
	*ws.Conn
	id     string
	logger *log.Logger
	m      *ttyConnMetrics
}

func newTTYMeteredConn(conn *ws.Conn, logger *log.Logger, id string) *ttyMeteredConn {
	return &ttyMeteredConn{
		Conn:   conn,
		id:     id,
		logger: logger,
		m:      &ttyConnMetrics{openedAt: time.Now(), headAtStart: -1},
	}
}

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

func (c *ttyMeteredConn) WriteBinary(p []byte) error {
	err := c.timed("binary", len(p), func() error { return c.Conn.WriteBinary(p) })
	if err == nil {
		c.m.binBytes += int64(len(p))
		c.m.binFrames++
	}
	return err
}

func (c *ttyMeteredConn) WriteText(p []byte) error {
	err := c.timed("text", len(p), func() error { return c.Conn.WriteText(p) })
	if err == nil {
		c.m.txtFrames++
	}
	return err
}

// WritePing is timed separately from text frames because the heartbeat is
// the canary for write-lock starvation: ping frames share writeFrame's
// mutex with output writes (ws.go), so a rising ping block time is the
// direct measure of the "heartbeat stuck behind output" coupling #114
// suspects between the overflow loop and the 30s liveness reap.
func (c *ttyMeteredConn) WritePing(p []byte) error {
	err := c.timed("ping", len(p), func() error { return c.Conn.WritePing(p) })
	if err == nil {
		c.m.pingFrames++
	}
	return err
}

// logHandshake emits the per-handshake line once the live subscription is
// in place. replayBytes is read off the connection's binary counter at that
// instant, which is exact for all three handshake modes (tail, catch-up,
// follow-live-end) without per-branch accounting.
func (m *ttyConnMetrics) logHandshake(logger *log.Logger, id string, since int64) {
	if logger == nil {
		return
	}
	logger.Printf("[tty-metrics] handshake id=%s mode=%s since=%d headAtStart=%d published=%d replayBytes=%d replayMs=%d budgetExhausted=%v",
		id, m.mode, since, m.headAtStart, m.publishedOffset,
		m.replayBytes, m.replayDur.Milliseconds(), m.budgetExhausted)
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
