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
	// traffic the cursor never sees. Since decision 5a the meter sits
	// INSIDE the compressor, so on a compressed connection binBytes is
	// the WIRE count (deflate output) and the cursor-comparable number
	// moves to logicalBytes below; on a plaintext connection the two
	// stay equal, which is what keeps old and new readings comparable.
	binBytes        int64
	binFrames       int64
	txtFrames       int64
	pingFrames      int64
	slowWrites      int64
	maxWriteBlock   time.Duration
	totalWriteBlock time.Duration

	// logicalBytes counts the UNCOMPRESSED binary payload bytes handed to
	// the write path (issue #114, decision 5a R14). On a compressed
	// connection binBytes is the wire count (deflate output) and this is
	// its logical pair; on an uncompressed connection the two are equal by
	// construction — every frame arrives at the meter verbatim — so the
	// R14 lines can carry both fields on every connection and stay
	// historically comparable.
	logicalBytes int64
	// compress reports the app-layer deflate-raw negotiation (the
	// `compress` cap, decision 5a): true only when this connection declared
	// it and got the compressed path. The handshake line appends
	// compress=true for those connections only.
	compress bool

	// Handshake (filled in by completeHandshake).
	mode            string // "follow-live-end" | "tail" | "catchup"
	handshakeStart  time.Time
	headAtStart     int64 // ring head when the handshake began; -1 unknown
	budgetExhausted bool
	publishedOffset int64
	replayBytes     int64 // binBytes snapshot at handshake end
	replayDur       time.Duration
	// replayLogicalBytes is the LOGICAL counterpart of replayBytes
	// (issue #114 decision 5a, review R1): the snapshot of logicalBytes
	// at handshake end. On a COMPRESSED connection replayBytes is the
	// WIRE count — the deflate output the replay put on the socket — and
	// its name stays frozen for historical comparability, while
	// replayLogicalBytes is the ring/logical byte count the client's
	// cursor actually advanced by; FIX-PLAN §五.3.b's acceptance
	// arithmetic ("logicalBytes/replayMs on compressed connections")
	// computes off THIS field paired with replayMs. On a plaintext
	// connection the two families are equal by construction.
	replayLogicalBytes int64
	subscribedAt       time.Time // zero until SubscribeOutput succeeds
	// windowed reports that this handshake's replay was bounded by the
	// issue #114 replay window — TRUE when EITHER half fired: the START
	// clamp (the client's `since` sat behind head−W_eff, so the replay
	// began later than it asked) or the TOTAL cap (the loop stopped at
	// W_eff bytes written). Both halves truncate, and the two causes need
	// different follow-up reads — a clamped start means a backlog older
	// than the window, a total cap means a head that outran the replay —
	// so the flag is the OR and droppedFromWindow is the sum.
	//
	// W_eff ≤ ttyHandshakeReplayBudget holds BY CONSTRUCTION (the window
	// config clamps to the budget, never above). One reading this flag
	// records as legal: budgetExhausted=true with windowed=false and
	// droppedFromWindow=0 is a VALID combination, reachable when
	// W_eff == budget (the env raised to ≥8MB and clamped there) AND
	// the backlog at handshake is < W_eff (so the start clamp never
	// fired) AND a producer then outruns the replay past the budget —
	// the clamp had nothing to cut, yet the total cap still did, and
	// that truncation is the budget's own, reported by its own field.
	// windowed is the OR of the two WINDOW truncations only, by design
	// excluding the budget stop (the review-B arbitration): the field
	// answers "was the frozen screen sized by the window", not "did
	// any ceiling cut bytes".
	windowed bool
	// droppedFromWindow is the byte count the window kept off this socket:
	//
	//	droppedFromWindow = (clampedStart − since)          start clamp
	//	                  + (head − endOffset at truncation)  total cap
	//
	// The first term is the span the window refused at the front end,
	// measured against the client's own request — so when `since` is ALSO
	// stale (behind the ring's oldest live byte) it overlaps the region the
	// issue #94 start-sync had already re-based the client onto; both
	// figures describe the same skipped bytes, NOT two independent
	// losses, and this one is the window's arithmetic. The second term
	// is read once, at the instant the total cap
	// fires, and is 0 when the loop stopped exactly on head. Zero on a
	// windowed handshake is therefore legitimate: clamped at the front,
	// caught up at the back.
	droppedFromWindow int64
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
// both variants: two integer increments here (binBytes, binFrames), three
// once the compressing wrapper is counted in — it adds logicalBytes per
// compressed frame (issue #114 R1); all of them sit below measurement noise.
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
