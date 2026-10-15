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
// same log prefix.
//
// Build gating: this file carries the shared types and the metered write
// wrappers for BOTH build variants; the behavioural half lives in
// tty_metrics_on.go (//go:build ttymetrics — real timing + logging,
// ttyMetricsBuild = true) and tty_metrics_off.go (//go:build !ttymetrics —
// passthrough no-ops that inline away, ttyMetricsBuild = false). The beta
// release workflow (release-beta.yml) compiles with -tags ttymetrics, so
// beta builds carry the instrumentation long-term while stable releases
// (release.yml, no tag) ship neither the timing nor the logging. The
// client half reads the same switch through the mw_tty_metrics cookie,
// which withServerRevision hands out only when ttyMetricsBuild is true.
//
// Remove or gate this machinery once the fix lands and the data has
// answered those questions.

import (
	"log"
	"time"

	"myworktree/internal/ws"
)

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

// WriteBinary routes through timed (real timing in ttymetrics builds, a
// passthrough that inlines away otherwise). The counter updates stay in
// both variants: two integer increments per frame, below noise.
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

// WritePing is counted separately from text frames because the heartbeat is
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
