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
	"strconv"
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
	replayFrames    int64 // binFrames snapshot at handshake end
	replayDur       time.Duration
	// ringCapBytes is the instance's ring-buffer capacity in bytes
	// (RingBuffer.CapBytes, internal/framework/ringbuffer.go:176 — already
	// public, so no new Manager surface), and ringLagBytes is
	// headAtStart−since: how far behind the live head the client's cursor
	// was when this handshake began.
	//
	// Both exist because issue #130 could read the replay STARVATION but not
	// the steady state it was starving inside: the incident's daemon was
	// pinned at the 256 MiB ring ceiling while each reconnect advanced the
	// cursor by only the 8 MiB budget. `headAtStart` and `since` are both on
	// the handshake line already, so the lag is derivable — but only by a
	// reader who knows to subtract them, and only for the handshake line;
	// spelling it out on BOTH lines (the conn-closed summary has neither
	// field today) makes the two numbers the incident needs greppable in one
	// pass. Fixing the ring capacity or the replay semantics is explicitly
	// out of scope here (issue #130 §7): these fields change nothing, they
	// make the shape of the next incident visible without a second dump.
	//
	// Both are FROZEN at handshake start, and ringLagBytes on the conn-closed
	// line therefore repeats the handshake's figure rather than reporting a
	// live lag. That is deliberate, not an oversight: the live head at close
	// would need a second manager read (stateMu against the live pumpLogs
	// writer) on the teardown path of every connection, to produce a number
	// whose only reader is a comparison against headNow — which the overflow
	// line already carries. One frozen pair, zero teardown cost.
	//
	// The scope of the -1 below, stated first: this paragraph is about
	// ringLagBytes, whose -1 means the LAG is unknown. ringCapBytes' -1 is a
	// different fact and is not what follows — it means the probe never ran at
	// all (the connection died before completeHandshake), not "capacity
	// unknown"; the sentinel table for BOTH fields is docs/ARCHITECTURE.md
	// §10.
	//
	// -1 means unknown, and it is reachable: headAtStart is -1 when the
	// EndOffset probe errored (e.g. an EndOffset error; a merely-stopped
	// instance returns head 0 with cap 0, which still reads honestly
	// alongside cap=0 — Manager.EndOffset answers (0, nil) for a non-running
	// instance at manager.go:464-470, and only a Registry.Get failure gives
	// an error), and `since` is -1 for a cursorless first connect or -2
	// (sinceFollowLiveEnd) for the painted-screen-no-cursor state. In every
	// one of those cases the lag is undefined — NOT zero, which would read as
	// "caught up" and dress an unknown as an all-clear, the dishonesty this
	// repo's cold-cache contracts refuse everywhere else.
	ringCapBytes int64
	ringLagBytes int64
	// ringLagKnown gates the -1 above: false means ringLagBytes must not be
	// read as a measurement.
	ringLagKnown bool

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

// ttyRingLagBytes is the pure arithmetic behind the ringLagBytes field
// (issue #130): lag = headAtStart − since, the span the client's cursor sat
// behind the live head when the handshake began.
//
// Three of the four inputs are sentinels, and each has to land somewhere
// honest:
//
//	headAtStart < 0  → the EndOffset probe errored (e.g. Registry.Get
//	                  failed; a merely-stopped instance answers (0, nil) at
//	                  manager.go:464-470 and reads as head 0 + cap 0) → unknown
//	since == -1      → cursorless first connect, nothing painted → unknown
//	since == -2      → sinceFollowLiveEnd: painted, offset unknown → unknown
//	since > head     → the beyond-head degrade → 0, the client is NOT behind
//
// Unknown returns ok=false and the caller prints -1. It must never print 0
// for unknown: 0 reads as "caught up", which is the opposite of "we could
// not tell you", and the incident this instrument is for is precisely a
// lag nobody could see.
func ttyRingLagBytes(headAtStart, since int64) (lag int64, ok bool) {
	if headAtStart < 0 || since < 0 {
		return -1, false
	}
	if headAtStart < since {
		return 0, true
	}
	return headAtStart - since, true
}

// ringFields renders the two issue-#130 fields as they appear on BOTH the
// handshake and the conn-closed lines, appended after every column that
// existed before them so the frozen layout the collector greps never shifts
// (same rule the window and logical fields follow).
func (m *ttyConnMetrics) ringFields() string {
	lag := m.ringLagBytes
	if !m.ringLagKnown {
		lag = -1
	}
	return " ringCapBytes=" + strconv.FormatInt(m.ringCapBytes, 10) +
		" ringLagBytes=" + strconv.FormatInt(lag, 10)
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
		// headAtStart, ringCapBytes and ringLagBytes all start at the
		// UNKNOWN sentinel (-1, ringLagKnown=false): the ring may be gone by
		// the time the probe runs, and a connection that never reaches
		// completeHandshake still reaches the conn-closed line (it is in a
		// defer). Zero would print as "caught up on a zero-byte ring".
		m: &ttyConnMetrics{openedAt: time.Now(), headAtStart: -1, ringCapBytes: -1, ringLagBytes: -1},
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
