package app

import (
	"bytes"
	"context"
	"encoding/binary"
	"encoding/json"
	"errors"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"myworktree/internal/framework"
	"myworktree/internal/ws"
)

// WebSocket frame opcodes (the ws package keeps these unexported).
const (
	wsOpText   byte = 0x1
	wsOpBinary byte = 0x2
	wsOpClose  byte = 0x8
)

// ttyHandshakeKind is a minimal live kind for driving
// handleInstanceTTYWS over a real WebSocket (issue #87). It is backed by
// a real framework.RingBuffer and implements ReadLogs exactly the way the
// PTY driver does — tail for a negative since, clamped ReadSince for a
// non-negative one — so the handshake replay paths run against genuine
// ring-buffer semantics, including the silent clamp on buffer overrun.
// Both driver branches are pinned in internal/instance/pty/driver_test.go
// (TestDriver_ReadLogs_TailReturnsNewestBytes and
// TestDriver_ReadLogs_SinceReadsIncremental) so this mirror cannot drift
// silently from the real driver.
type ttyHandshakeKind struct {
	buf      *framework.RingBuffer
	subsMu   sync.Mutex
	subs     map[*ttySubscriber]struct{}
	failRead atomic.Bool

	// scriptSince, when non-nil, makes ReadLogs for since>=0 pop a
	// scripted (body, next) instead of consulting the ring. It exists to
	// reproduce the read→consult race window deterministically (FIX-F):
	// the first incremental read answers "caught up" ("", cursor) while a
	// later one returns real bytes, which no single RingBuffer snapshot
	// can express without racing a live writer. The Tail consult still
	// hits the real ring (since<0 is not scripted), so `head` reported to
	// completeHandshake is genuine. Consumed under scriptMu.
	scriptMu    sync.Mutex
	scriptSince []scriptedRead
}

// scriptedRead is one canned incremental ReadLogs answer (body, next).
type scriptedRead struct {
	body string
	next int64
}

var errTTYReadSimulated = errors.New("synthetic tty read failure")

func newTTYHandshakeKind(capBytes int64) *ttyHandshakeKind {
	return &ttyHandshakeKind{
		buf:  framework.NewRingBuffer(capBytes),
		subs: map[*ttySubscriber]struct{}{},
	}
}

// ttySubscriber mirrors internal/instance/pty's subscriber value (issue
// #82): the channel plus the exactly-once close flag, closed only under
// subsMu. The double has to carry the same shape because the handler is
// tested against the same two closers — an overflow in publish and the
// handler's deferred cancel — and a bare channel would panic on the
// second one.
type ttySubscriber struct {
	ch     chan string
	closed bool
}

func (s *ttySubscriber) closeLocked() {
	if s.closed {
		return
	}
	s.closed = true
	close(s.ch)
}

func (k *ttyHandshakeKind) Manifest() framework.KindInfo {
	return framework.KindInfo{Name: "tty-handshake", Label: "TTY Handshake"}
}

func (k *ttyHandshakeKind) Spawn(ctx context.Context, p framework.SpawnParams) (framework.Handle, *framework.ReadySignal, error) {
	ready := framework.NewReadySignal()
	ready.Close()
	return framework.NewHandle("tty-handshake", "inner"), ready, nil
}

func (k *ttyHandshakeKind) Stop(h framework.Handle, graceSeconds int) error { return nil }

func (k *ttyHandshakeKind) Status(h framework.Handle) (framework.Status, string) {
	return framework.StatusRunning, ""
}

// ReadLogs mirrors internal/instance/pty/driver.go: negative since is a
// tail read, non-negative is a clamped incremental read. failRead flips
// both branches into errors to drive the handshake error path. A scripted
// since>=0 queue (scriptSince) overrides the incremental read to drive the
// FIX-F read→consult window deterministically; the tail consult is never
// scripted, so `head` stays genuine.
func (k *ttyHandshakeKind) ReadLogs(h framework.Handle, since, maxBytes int64) (string, int64, error) {
	if k.failRead.Load() {
		return "", 0, errTTYReadSimulated
	}
	if since < 0 {
		body, off := k.buf.Tail(maxBytes)
		return body, off, nil
	}
	k.scriptMu.Lock()
	if len(k.scriptSince) > 0 {
		r := k.scriptSince[0]
		k.scriptSince = k.scriptSince[1:]
		k.scriptMu.Unlock()
		return r.body, r.next, nil
	}
	k.scriptMu.Unlock()
	return k.buf.ReadSince(since, maxBytes)
}

// scriptIncrementalReads arms the deterministic since>=0 queue (FIX-F).
func (k *ttyHandshakeKind) scriptIncrementalReads(reads ...scriptedRead) {
	k.scriptMu.Lock()
	k.scriptSince = reads
	k.scriptMu.Unlock()
}

func (k *ttyHandshakeKind) KindBlob(h framework.Handle) (json.RawMessage, error) {
	return json.RawMessage(`{}`), nil
}

func (k *ttyHandshakeKind) HTTPHint(instanceID string) string { return "" }

func (k *ttyHandshakeKind) RegisterHTTP(mux *http.ServeMux, instanceID string, h framework.Handle) {}

func (k *ttyHandshakeKind) Resize(h framework.Handle, cols, rows int) error { return nil }

// SubscribeOutput mirrors the pty driver's subscription registry,
// including the issue #82 contract: the channel closes when the
// subscription ends, whether that is this cancel or an overflow in
// publish, and cancel stays safe to call after either.
func (k *ttyHandshakeKind) SubscribeOutput(id string) (<-chan string, func(), error) {
	sub := &ttySubscriber{ch: make(chan string, 16)}
	k.subsMu.Lock()
	k.subs[sub] = struct{}{}
	k.subsMu.Unlock()
	cancel := func() {
		k.subsMu.Lock()
		defer k.subsMu.Unlock()
		delete(k.subs, sub)
		sub.closeLocked()
	}
	return sub.ch, cancel, nil
}

// publish stands in for pumpLogs' broadcast side of the PTY output pump,
// overflow behaviour included (issue #82): a subscriber whose buffer is
// full is removed and closed rather than having the chunk dropped in an
// empty `default:` arm.
func (k *ttyHandshakeKind) publish(chunk string) {
	k.subsMu.Lock()
	defer k.subsMu.Unlock()
	for sub := range k.subs {
		select {
		case sub.ch <- chunk:
		default:
			delete(k.subs, sub)
			sub.closeLocked()
		}
	}
}

// subscriberCount reports how many subscribers are still registered — the
// observable proof that an overflowing subscriber was dropped rather than
// kept and silently skipped.
func (k *ttyHandshakeKind) subscriberCount() int {
	k.subsMu.Lock()
	defer k.subsMu.Unlock()
	return len(k.subs)
}

// waitSubscribers blocks until the server's completeHandshake has reached
// SubscribeOutput. The sync frame is written BEFORE the subscription, so
// publishing right after reading sync races the server and the chunk can
// be dropped by the broadcast's default arm — a genuinely flaky test,
// worse under t.Parallel. Poll the subscriber set instead.
func (k *ttyHandshakeKind) waitSubscribers(t *testing.T, d time.Duration) {
	t.Helper()
	deadline := time.Now().Add(d)
	for {
		k.subsMu.Lock()
		n := len(k.subs)
		k.subsMu.Unlock()
		if n > 0 {
			return
		}
		if time.Now().After(deadline) {
			t.Fatal("server never reached SubscribeOutput")
		}
		time.Sleep(5 * time.Millisecond)
	}
}

// ttyWSTestServer wires handleInstanceTTYWS at its canonical route on a
// real httptest server (the handler hijacks the connection, so
// ResponseRecorder cannot serve it) and starts one live instance.
func ttyWSTestServer(t *testing.T, k *ttyHandshakeKind) (addr string, m *framework.Manager, instID string) {
	t.Helper()
	_, m = newLogTestServer(t, k)
	mux := http.NewServeMux()
	mux.HandleFunc("/api/instances/tty/ws", (&Server{instanceMgr: m}).handleInstanceTTYWS)
	hs := httptest.NewServer(mux)
	t.Cleanup(hs.Close)
	instID = startKindInstance(t, m, "tty-handshake")
	return strings.TrimPrefix(hs.URL, "http://"), m, instID
}

func ttyWSPath(id, query string) string {
	p := "/api/instances/tty/ws?id=" + url.QueryEscape(id)
	if query != "" {
		p += "&" + query
	}
	return p
}

// readFrame reads one message with a deadline, so a handshake that never
// completes fails the test fast instead of hanging the suite. The read
// deadline on the underlying conn guarantees the helper goroutine always
// unblocks — even when the select gives up first — so no ReadMessage
// goroutine leaks on any timeout path.
func readFrame(t *testing.T, c *ws.Conn, d time.Duration) (byte, []byte) {
	t.Helper()
	type frame struct {
		op  byte
		p   []byte
		err error
	}
	ch := make(chan frame, 1)
	if err := c.SetReadDeadline(time.Now().Add(d)); err != nil {
		t.Fatalf("set read deadline: %v", err)
	}
	go func() {
		op, p, err := c.ReadMessage()
		ch <- frame{op: op, p: p, err: err}
	}()
	select {
	case f := <-ch:
		_ = c.SetReadDeadline(time.Time{}) // clear for the next phase
		if f.err != nil {
			t.Fatalf("read frame: %v", f.err)
		}
		return f.op, f.p
	case <-time.After(d + 2*time.Second):
		t.Fatal("timed out waiting for a websocket frame")
		return 0, nil
	}
}

// dialTTY dials the TTY endpoint and consumes the ready frame.
func dialTTY(t *testing.T, addr, path string) *ws.Conn {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	conn, err := ws.Dial(ctx, addr, path, nil)
	if err != nil {
		t.Fatalf("dial %s: %v", path, err)
	}
	t.Cleanup(func() { _ = conn.Close() }) // idempotent
	op, p := readFrame(t, conn, 5*time.Second)
	if op != wsOpText || string(p) != `{"type":"ready"}` {
		t.Fatalf("first frame op=%d payload=%q, want text {\"type\":\"ready\"}", op, p)
	}
	return conn
}

// sendResize sends the first resize, which completes the handshake
// immediately instead of waiting out the 5s timer.
func sendResize(t *testing.T, c *ws.Conn) {
	t.Helper()
	if err := c.WriteText([]byte(`{"type":"resize","cols":80,"rows":24}`)); err != nil {
		t.Fatalf("write resize: %v", err)
	}
}

// expectCloseFrame asserts the next frame is a close carrying the given
// code and a reason containing wantReason — and that nothing else (most
// importantly no binary diagnostic frame, which the client would count as
// ring-buffer bytes) arrives first.
func expectCloseFrame(t *testing.T, c *ws.Conn, code uint16, wantReason string) {
	t.Helper()
	op, p := readFrame(t, c, 5*time.Second)
	if op != wsOpClose {
		t.Fatalf("op=%d payload=%q, want close frame", op, p)
	}
	if len(p) < 2 {
		t.Fatalf("close frame payload too short: %v", p)
	}
	if got := binary.BigEndian.Uint16(p); got != code {
		t.Fatalf("close code = %d, want %d (reason %q)", got, code, string(p[2:]))
	}
	if !strings.Contains(string(p[2:]), wantReason) {
		t.Fatalf("close reason = %q, want it to contain %q", string(p[2:]), wantReason)
	}
}

// dialHandshakeFrames dials, completes the handshake with the first
// resize, and collects the replay up to the `sync` echo — but keeps the
// binary frames SEPARATE, so a test can pin how the replay was chunked
// (each frame is one streamed ring-buffer read, never the whole delta
// re-buffered) without an extra concatenation copy.
func dialHandshakeFrames(t *testing.T, addr, path string) (c *ws.Conn, frames [][]byte, syncOffset int64) {
	t.Helper()
	conn := dialTTY(t, addr, path)

	sendResize(t, conn)

	sawSync := false
	for !sawSync {
		op, p := readFrame(t, conn, 5*time.Second)
		switch op {
		case wsOpBinary:
			frames = append(frames, p)
		case wsOpText:
			var ctl struct {
				Type   string `json:"type"`
				Offset int64  `json:"offset"`
			}
			if err := json.Unmarshal(p, &ctl); err != nil {
				t.Fatalf("control frame %q is not JSON: %v", p, err)
			}
			switch ctl.Type {
			case "sync":
				syncOffset = ctl.Offset
				sawSync = true
			case "resize":
				// Size echo from the shared-size recompute; arriving
				// before sync is fine, it just is not the cursor.
			default:
				t.Fatalf("unexpected control frame %q before sync", p)
			}
		default:
			t.Fatalf("unexpected opcode %d during handshake", op)
		}
	}
	return conn, frames, syncOffset
}

// dialHandshake dials the TTY endpoint, consumes the ready frame, sends
// the first resize (which completes the handshake immediately instead of
// waiting out the 5s timer), and collects frames up to the `sync` echo.
// It returns the connection, the concatenated binary replay, and the
// offset carried by the sync frame.
func dialHandshake(t *testing.T, addr, path string) (c *ws.Conn, replay string, syncOffset int64) {
	t.Helper()
	conn, frames, syncOffset := dialHandshakeFrames(t, addr, path)
	var body strings.Builder
	for _, f := range frames {
		body.Write(f)
	}
	return conn, body.String(), syncOffset
}

// expectSocketClosed asserts the server stopped serving this connection: the
// next read fails (EOF / reset) instead of delivering another frame. A
// timeout is reported as a failure too — a socket left open and silent is
// precisely the unreported stall this teardown exists to end.
func expectSocketClosed(t *testing.T, c *ws.Conn, d time.Duration) {
	t.Helper()
	if err := c.SetReadDeadline(time.Now().Add(d)); err != nil {
		t.Fatalf("set read deadline: %v", err)
	}
	op, p, err := c.ReadMessage()
	_ = c.SetReadDeadline(time.Time{})
	if err == nil {
		t.Fatalf("connection still serving after the teardown: op=%d payload=%q", op, p)
	}
	var ne net.Error
	if errors.As(err, &ne) && ne.Timeout() {
		t.Fatalf("connection still open and silent after the teardown (read timed out): %v", err)
	}
}

// TestHandleInstanceTTYWS_ReconnectWithSinceDoesNotRedeliverSeenBytes is
// AC1 of issue #87: a reconnect that carries the cursor it learned from
// the first handshake's `sync` echo must receive only the bytes produced
// in between, never the tail again.
func TestHandleInstanceTTYWS_ReconnectWithSinceDoesNotRedeliverSeenBytes(t *testing.T) {
	t.Parallel()
	k := newTTYHandshakeKind(1024)
	addr, _, instID := ttyWSTestServer(t, k)
	k.buf.WriteString("HELLO-TAIL") // 10 bytes

	c1, replay1, sync1 := dialHandshake(t, addr, ttyWSPath(instID, ""))
	if replay1 != "HELLO-TAIL" {
		t.Fatalf("first handshake replay = %q, want HELLO-TAIL", replay1)
	}
	if sync1 != 10 {
		t.Fatalf("first sync offset = %d, want 10 (end of replay)", sync1)
	}

	// Output produced after the first handshake, while the socket is down.
	k.buf.WriteString("DELTA") // head is now 15
	_ = c1.WriteClose(ws.CloseMessage(1000, "bye"))
	_ = c1.Close()

	c2, replay2, sync2 := dialHandshake(t, addr, ttyWSPath(instID, "since=10"))
	_ = c2.WriteClose(ws.CloseMessage(1000, "bye"))
	_ = c2.Close()
	if strings.Contains(replay2, "HELLO-TAIL") {
		t.Fatalf("reconnect replay %q re-delivered already-seen bytes", replay2)
	}
	if replay2 != "DELTA" {
		t.Fatalf("reconnect replay = %q, want only the new bytes DELTA", replay2)
	}
	if sync2 != 15 {
		t.Fatalf("reconnect sync offset = %d, want 15 (advanced end offset)", sync2)
	}
}

// TestHandleInstanceTTYWS_OmittedSinceReplaysTail is AC2: a first connect
// — no `since`, an empty `since`, or an unparsable one — keeps the
// existing tail semantics.
func TestHandleInstanceTTYWS_OmittedSinceReplaysTail(t *testing.T) {
	t.Parallel()
	k := newTTYHandshakeKind(1024)
	addr, _, instID := ttyWSTestServer(t, k)
	k.buf.WriteString("tail-body") // 9 bytes

	for _, query := range []string{"", "since=", "since=bogus"} {
		replay, syncOffset := func() (string, int64) {
			c, replay, off := dialHandshake(t, addr, ttyWSPath(instID, query))
			_ = c.WriteClose(ws.CloseMessage(1000, "bye"))
			_ = c.Close()
			return replay, off
		}()
		if replay != "tail-body" {
			t.Fatalf("query %q: replay = %q, want tail-body", query, replay)
		}
		if syncOffset != 9 {
			t.Fatalf("query %q: sync offset = %d, want 9", query, syncOffset)
		}
	}
}

// TestHandleInstanceTTYWS_StaleSinceClampsSilently is AC3: a `since`
// older than the oldest live byte must not error — the ring buffer clamps
// the read to the oldest live byte and the sync frame carries the
// advanced end offset.
func TestHandleInstanceTTYWS_StaleSinceClampsSilently(t *testing.T) {
	t.Parallel()
	k := newTTYHandshakeKind(64)
	addr, _, instID := ttyWSTestServer(t, k)

	var full strings.Builder
	for i := 0; i < 100; i++ {
		full.WriteByte(byte('a' + i%26))
	}
	k.buf.WriteString(full.String()) // head 100, oldest live byte at 36

	replay, syncOffset := func() (string, int64) {
		c, replay, off := dialHandshake(t, addr, ttyWSPath(instID, "since=10"))
		_ = c.WriteClose(ws.CloseMessage(1000, "bye"))
		_ = c.Close()
		return replay, off
	}()
	want := full.String()[36:] // clamped to the oldest live byte
	if replay != want {
		t.Fatalf("stale-since replay = %q, want the live bytes from offset 36 (%q)", replay, want)
	}
	if syncOffset != 100 {
		t.Fatalf("sync offset = %d, want 100 (advanced past the clamped start)", syncOffset)
	}
}

// TestHandleInstanceTTYWS_LargeDeltaStreamsEveryByteSinceCursor is the
// over-64KB reconnect case (review BLOCKER A1): when the offline delta
// exceeds the 64KB read cap, the catch-up loop must deliver EVERY byte
// from `since`, not just the newest chunk. Buffering only the newest chunk
// — the earlier shape of the loop — handed the client a replay of
// content[65636:] while publishing sync == 100100, so the [100, 65636)
// window was never sent and, because a client cursor only moves forward,
// never re-requested: 65536 bytes gone. Streaming each chunk as it is read
// sends the whole delta with the SAME iteration count and the same published
// offset, so the fix is free and peak memory stays one chunk (~64KB).
func TestHandleInstanceTTYWS_LargeDeltaStreamsEveryByteSinceCursor(t *testing.T) {
	t.Parallel()
	k := newTTYHandshakeKind(128 * 1024) // ring cap above the 64KB read cap
	addr, _, instID := ttyWSTestServer(t, k)

	var full strings.Builder
	for i := 0; i < 100100; i++ {
		full.WriteByte(byte('a' + i%26))
	}
	content := full.String()
	k.buf.WriteString(content[:100])

	c1, replay1, sync1 := dialHandshake(t, addr, ttyWSPath(instID, ""))
	if replay1 != content[:100] || sync1 != 100 {
		t.Fatalf("first handshake: replay %d bytes / sync %d, want 100/100", len(replay1), sync1)
	}

	// Offline window far larger than the 64KB replay cap.
	k.buf.WriteString(content[100:]) // head = 100100
	_ = c1.WriteClose(ws.CloseMessage(1000, "bye"))
	_ = c1.Close()

	c2, frames2, sync2 := dialHandshakeFrames(t, addr, ttyWSPath(instID, "since=100"))
	_ = c2.WriteClose(ws.CloseMessage(1000, "bye"))
	_ = c2.Close()

	// Every byte of the delta is on the wire, in order, with nothing lost
	// and nothing re-delivered: the replay IS content[100:100100).
	var replay2 strings.Builder
	for _, f := range frames2 {
		if int64(len(f)) > 64*1024 {
			t.Fatalf("replay frame of %d bytes exceeds the 64KB read cap", len(f))
		}
		replay2.Write(f)
	}
	if replay2.String() != content[100:] {
		t.Fatalf("replay = %d bytes, want all %d bytes of the delta [100,100100) — over-cap bytes must be streamed, not dropped", replay2.Len(), 100100-100)
	}
	// Exact pin of the loop's chunking: [100,65636) then [65636,100100),
	// two chunks, then the loop sees head.
	if len(frames2) != 2 || len(frames2[0]) != 65536 || len(frames2[1]) != 100100-65636 {
		t.Fatalf("replay arrived as %d frames of sizes %v, want 2 frames of [65536, %d]", len(frames2), frameSizes(frames2), 100100-65636)
	}
	// sync equals the end of the LAST chunk written, which here is head:
	// the live stream continues exactly where the replay stops.
	if sync2 != 100100 {
		t.Fatalf("sync offset = %d, want 100100 — the published cursor must equal head so the live stream stays contiguous with the replay (no hole)", sync2)
	}
}

// frameSizes renders frame lengths for a failure message.
func frameSizes(frames [][]byte) []int {
	out := make([]int, 0, len(frames))
	for _, f := range frames {
		out = append(out, len(f))
	}
	return out
}

// TestHandleInstanceTTYWS_ReplayBudgetTruncatesAndPublishesLastWrittenOffset
// pins the replay budget (review finding A3). A delta larger than
// ttyHandshakeReplayBudget must NOT be replayed in full on one handshake:
// the loop stops at the budget and publishes the end offset of the last
// chunk it actually WROTE, which is strictly behind head. The bytes past the
// budget are then ahead of the client's cursor, so the next reconnect
// re-requests them — truncation defers bytes, it never skips them (the same
// self-healing property as the final-read→SubscribeOutput window).
//
// The budget is a byte count, not a deadline, so the truncation point is
// exact and machine-independent: the client receives exactly
// ttyHandshakeReplayBudget bytes (8MB is a whole multiple of the 64KB read
// cap, so the loop's stop-before-read guard lands on the boundary) and sync
// equals that count.
func TestHandleInstanceTTYWS_ReplayBudgetTruncatesAndPublishesLastWrittenOffset(t *testing.T) {
	t.Parallel()
	// A delta of budget + 4 chunks, and a ring cap above it, so the
	// truncation under test is the budget's — never the ring's eviction.
	// It stays a few bytes past the boundary on purpose: with the guard
	// removed the loop delivers the whole delta, so any overshoot (or any
	// missing stop) shows up as delivered != budget.
	delta := make([]byte, ttyHandshakeReplayBudget+4*64*1024)
	for i := range delta {
		delta[i] = byte('a' + i%26)
	}
	k := newTTYHandshakeKind(int64(len(delta)) + 1024)
	addr, _, instID := ttyWSTestServer(t, k)
	k.buf.Write(delta) // head = budget + 4 chunks
	head := k.buf.Offset()

	c, frames, syncOffset := dialHandshakeFrames(t, addr, ttyWSPath(instID, "since=0"))
	_ = c.WriteClose(ws.CloseMessage(1000, "bye"))
	_ = c.Close()

	var delivered int64
	for i, f := range frames {
		if int64(len(f)) > 64*1024 {
			t.Fatalf("replay frame of %d bytes exceeds the 64KB read cap", len(f))
		}
		// Byte-exact and in order: each frame is the delta at its own
		// offset, so the budget cut at the TAIL and skipped nothing from
		// the middle.
		if !bytes.Equal(f, delta[delivered:delivered+int64(len(f))]) {
			t.Fatalf("replay frame %d at offset %d is not the delta's bytes there", i, delivered)
		}
		delivered += int64(len(f))
	}
	// The cap held: exactly the budget, not the 4 chunks beyond it.
	if delivered != ttyHandshakeReplayBudget {
		t.Fatalf("replay = %d bytes, want exactly the budget (%d) out of a %d-byte delta — the loop must stop there", delivered, ttyHandshakeReplayBudget, len(delta))
	}
	if len(frames) != int(ttyHandshakeReplayBudget/(64*1024)) {
		t.Fatalf("replay arrived as %d frames, want %d full 64KB chunks", len(frames), ttyHandshakeReplayBudget/(64*1024))
	}
	// The published cursor is the end of the LAST chunk written, behind
	// head: the remainder stays re-requestable.
	if syncOffset != ttyHandshakeReplayBudget {
		t.Fatalf("sync offset = %d, want %d (end of the last delivered chunk)", syncOffset, ttyHandshakeReplayBudget)
	}
	if syncOffset >= head {
		t.Fatalf("sync offset = %d, want strictly behind head %d — publishing head here would strand the undelivered remainder forever", syncOffset, head)
	}

	// Self-healing: a reconnect from the published cursor re-requests the
	// bytes the budget deferred — the last 4 chunks — and catches up to
	// head, so nothing was lost, only deferred.
	c2, replay2, sync2 := dialHandshake(t, addr, ttyWSPath(instID, "since="+strconv.FormatInt(syncOffset, 10)))
	_ = c2.WriteClose(ws.CloseMessage(1000, "bye"))
	_ = c2.Close()
	want2 := delta[ttyHandshakeReplayBudget:]
	if replay2 != string(want2) {
		t.Fatalf("re-request after truncation = %d bytes, want the %d deferred bytes — the budget must defer, never skip", len(replay2), len(want2))
	}
	if sync2 != head {
		t.Fatalf("sync offset after the re-request = %d, want head %d — the deferred remainder must be recoverable in full", sync2, head)
	}
}

// TestHandleInstanceTTYWS_EmptyReadAfterConsultPublishesHeadNotZero pins
// review finding B3, the FIX-F counterpart. The scripted queue drives the
// same shape TestHandleInstanceTTYWS_ConsultSeesNewBytesDeliversThem uses
// (first incremental read lies "caught up" at T1, the Tail consult then sees
// the genuine head 16 > cursor 10 and continues the loop), except the SECOND
// read answers with an EMPTY body and next == head. That read is real: on a
// CLOSED ring RingBuffer.ReadSince(50, 64KB) returns ("", 100, nil) — empty
// body while since < head, cursor advanced to head. The old `!first` break
// left endOffset at its zero value, so the handshake published offset 0 — a
// cursor the ring never held — and the client fell back to a full tail
// replay (the duplicate paint this issue is about). The break now carries
// that read's `next`, so the published offset is the truthful head.
func TestHandleInstanceTTYWS_EmptyReadAfterConsultPublishesHeadNotZero(t *testing.T) {
	t.Parallel()
	k := newTTYHandshakeKind(1024)
	addr, _, instID := ttyWSTestServer(t, k)
	k.buf.WriteString("0123456789ABCDEF") // real head = 16

	// Read 1: "caught up" at the cursor (T1). Read 2 (after the consult saw
	// head 16 > cursor 10): empty body with next == head — the closed-ring
	// shape. Nothing more is scripted; the loop must not read again.
	k.scriptIncrementalReads(
		scriptedRead{body: "", next: 10},
		scriptedRead{body: "", next: 16},
	)

	c, replay, syncOffset := dialHandshake(t, addr, ttyWSPath(instID, "since=10"))
	_ = c.WriteClose(ws.CloseMessage(1000, "bye"))
	_ = c.Close()

	if replay != "" {
		t.Fatalf("replay = %q, want nothing — both scripted reads were empty", replay)
	}
	if syncOffset != 16 {
		t.Fatalf("sync offset = %d, want 16 (the ring head the empty read reported) — publishing 0 hands back a cursor the ring never held and sends the client into a full tail replay", syncOffset)
	}
	if syncOffset == 0 {
		t.Fatal("sync offset is 0 — the B3 regression")
	}
}

// TestHandleInstanceTTYWS_SyncFrameCarriesEndOffset pins the sync-frame
// contract: it is emitted even when the replay is empty, it survives the
// live-stream handover, and its offset matches the ring buffer head.
func TestHandleInstanceTTYWS_SyncFrameCarriesEndOffset(t *testing.T) {
	t.Parallel()
	k := newTTYHandshakeKind(1024)
	addr, _, instID := ttyWSTestServer(t, k)

	// Empty ring buffer: no binary frame, but still a sync with offset 0.
	c, replay, syncOffset := dialHandshake(t, addr, ttyWSPath(instID, ""))
	if replay != "" {
		t.Fatalf("replay on empty buffer = %q, want no bytes", replay)
	}
	if syncOffset != 0 {
		t.Fatalf("sync offset = %d, want 0 on an empty buffer", syncOffset)
	}

	// The stream keeps flowing after the handshake. The sync frame is
	// written BEFORE SubscribeOutput, so publishing right after reading
	// sync races the server and the chunk can be dropped by the
	// broadcast's default arm — wait until the subscription exists.
	k.waitSubscribers(t, 5*time.Second)
	k.buf.WriteString("live-chunk")
	k.publish("live-chunk")
	// The resize notification queued by the handshake's own resize echo
	// may arrive first; skip text control frames until the binary one.
	var op byte
	var p []byte
	for deadline := time.Now().Add(5 * time.Second); time.Now().Before(deadline); {
		op, p = readFrame(t, c, 5*time.Second)
		if op == wsOpBinary {
			break
		}
	}
	if op != wsOpBinary || string(p) != "live-chunk" {
		t.Fatalf("live frame op=%d payload=%q, want binary live-chunk", op, p)
	}
	if int64(len("live-chunk")) != k.buf.Offset()-syncOffset {
		t.Fatalf("ring advanced by %d, want %d", k.buf.Offset()-syncOffset, len("live-chunk"))
	}

	// A reconnect from the live offset sees nothing new — and still gets
	// a sync frame carrying the unchanged end offset. This is the FIX-C
	// counterpart case: cursor exactly AT head is the normal caught-up
	// exit (empty replay, sync == head == cursor) — it must NOT take the
	// cursor-ahead tail fallback, which would re-append the tail over a
	// screen that already holds it.
	_ = c.WriteClose(ws.CloseMessage(1000, "bye"))
	_ = c.Close()
	replay2, sync2 := func() (string, int64) {
		c2, replay, off := dialHandshake(t, addr, ttyWSPath(instID, "since=10"))
		_ = c2.WriteClose(ws.CloseMessage(1000, "bye"))
		_ = c2.Close()
		return replay, off
	}()
	if replay2 != "" {
		t.Fatalf("reconnect at head replayed %q, want nothing", replay2)
	}
	if sync2 != 10 {
		t.Fatalf("sync offset = %d, want 10 (head unchanged)", sync2)
	}
}

// TestHandleInstanceTTYWS_ReplayReadFailureClosesConnection pins the
// handshake error path: a failing replay read must CLOSE the connection
// with 1013, never smuggle the error text into a binary frame — every
// binary frame is ring-buffer output the client counts into its cursor —
// and never publish a sync claiming a cursor over bytes never delivered.
func TestHandleInstanceTTYWS_ReplayReadFailureClosesConnection(t *testing.T) {
	t.Parallel()
	k := newTTYHandshakeKind(1024)
	addr, _, instID := ttyWSTestServer(t, k)
	k.buf.WriteString("some-bytes")
	k.failRead.Store(true)

	// Both branches fail the same way: the tail branch (no since) and
	// the catch-up loop (since=2) hit the error on their first read.
	for _, query := range []string{"", "since=2"} {
		c := dialTTY(t, addr, ttyWSPath(instID, query))
		sendResize(t, c)
		// The resize that triggers the handshake also queues a shared-size
		// echo into this connection's own update channel BEFORE
		// completeHandshake runs (app.go: updateTTYClientSize precedes it);
		// whether that echo reaches the wire before the close depends on
		// whether the aggregate size changed for this dial — the first
		// connect of an instance changes it, a same-size reconnect does not.
		// That scheduling made a strict "close is the very next frame"
		// assertion flaky. Skip the legitimate text echoes; assert what
		// must NEVER appear — a binary frame (the client counts every
		// binary frame as ring-buffer bytes) or a sync (claiming a cursor
		// over bytes never delivered) — then require the close.
		for {
			op, p := readFrame(t, c, 5*time.Second)
			if op == wsOpClose {
				if len(p) < 2 {
					t.Fatalf("close frame payload too short: %v", p)
				}
				if got := binary.BigEndian.Uint16(p); got != 1013 {
					t.Fatalf("close code = %d, want 1013 (reason %q)", got, string(p[2:]))
				}
				if !strings.Contains(string(p[2:]), "synthetic tty read failure") {
					t.Fatalf("close reason = %q, want it to name the read failure", string(p[2:]))
				}
				break
			}
			if op == wsOpBinary {
				t.Fatalf("error text smuggled as binary frame: %q", p)
			}
			if op == wsOpText && strings.Contains(string(p), `"sync"`) {
				t.Fatalf("sync published after failed replay: %q", p)
			}
			if op != wsOpText {
				t.Fatalf("unexpected opcode %d before close: %q", op, p)
			}
		}
	}
}

// TestHandleInstanceTTYWS_NonRunningInstanceDegradesToFirstConnect pins
// the FIX-C guard on its ONLY reachable today path: Manager.ReadSince
// echoes a stopped instance's cursor back verbatim ("", since, nil), so
// the first read makes zero progress and the loop would publish the
// client-supplied number as authoritative. The handshake consults Tail for
// the real head instead — Manager.Tail of a stopped instance is
// ("", 0, nil), cursor 5 > head 0 — so the handshake degrades to exactly
// the first-connect behaviour: empty replay, sync offset 0. Then
// SubscribeOutput fails and CLOSES the connection (not poisoning it with
// a binary error frame).
func TestHandleInstanceTTYWS_NonRunningInstanceDegradesToFirstConnect(t *testing.T) {
	t.Parallel()
	k := newTTYHandshakeKind(1024)
	addr, m, instID := ttyWSTestServer(t, k)
	k.buf.WriteString("buffered-before-stop")
	if err := m.Stop(instID); err != nil {
		t.Fatalf("Stop: %v", err)
	}

	c, replay, syncOffset := dialHandshake(t, addr, ttyWSPath(instID, "since=5"))
	if replay != "" {
		t.Fatalf("replay on stopped instance = %q, want nothing", replay)
	}
	if syncOffset != 0 {
		t.Fatalf("sync offset = %d, want 0 — the client-supplied cursor must not be echoed back as authoritative (first-connect semantics)", syncOffset)
	}
	expectCloseFrame(t, c, 1013, "instance not running")
}

// TestHandleInstanceTTYWS_CursorAheadOfHeadFallsBackToTail pins the
// FIX-C defensive clamp for a running instance: a `since` strictly greater
// than the ring-buffer head is unreachable through normal flows today
// (Restart mints a NEW instance id, the browser builds a fresh session
// with ttyOffset -1), but if it ever appeared, echoing it back would mean
// permanent silence — the worst outcome this subsystem can produce. The
// zero-progress first read consults Tail and degrades to the
// first-connect behaviour: replay the newest bytes, publish the tail's end
// offset. The counterpart — cursor exactly AT head — stays the normal
// caught-up exit (empty replay, sync == head), pinned by
// TestHandleInstanceTTYWS_SyncFrameCarriesEndOffset.
func TestHandleInstanceTTYWS_CursorAheadOfHeadFallsBackToTail(t *testing.T) {
	t.Parallel()
	k := newTTYHandshakeKind(1024)
	addr, _, instID := ttyWSTestServer(t, k)
	k.buf.WriteString("HELLO-RING") // head = 10

	c, replay, syncOffset := dialHandshake(t, addr, ttyWSPath(instID, "since=999"))
	_ = c.WriteClose(ws.CloseMessage(1000, "bye"))
	_ = c.Close()
	if replay != "HELLO-RING" {
		t.Fatalf("replay = %q, want the tail HELLO-RING (cursor ahead of head must fall back to first-connect tail)", replay)
	}
	if syncOffset != 10 {
		t.Fatalf("sync offset = %d, want 10 (the tail's end offset, not the client's bogus 999)", syncOffset)
	}
}

// TestHandleInstanceTTYWS_ConsultSeesNewBytesDeliversThem pins FIX-F.
//
// The FIX-C consult races: the first ReadSince returns "" because
// cursor >= head at THAT instant (T1); the Tail consult runs slightly
// later (T2), and if the PTY wrote in (T1, T2] the head is now GREATER
// than the cursor. Round-3's else-branch published endOffset = head and
// broke WITHOUT delivering [cursor, head): those bytes are in neither the
// replay nor the live subscription (SubscribeOutput has not run), and a
// client cursor only moves forward, so they were skipped FOREVER — a
// regression against round 2, which echoed `since` and thus stayed
// self-healing. FIX-F makes head > cursor continue the loop so the next
// ReadSince delivers the bytes.
//
// This is driven deterministically, not by racing: the ring is seeded so
// its real head (16) is genuinely greater than the cursor (10), but the
// scripted incremental queue makes the FIRST since>=0 read lie "caught up"
// ("", 10) — the T1 snapshot — and the SECOND read deliver the real bytes
// ("ABCDEF", 16) — the post-consult state. The Tail consult is NOT
// scripted, so the head it reports is genuine. We assert the client
// RECEIVES those bytes (not merely an offset covering them) and that sync
// equals head.
func TestHandleInstanceTTYWS_ConsultSeesNewBytesDeliversThem(t *testing.T) {
	t.Parallel()
	k := newTTYHandshakeKind(1024)
	addr, _, instID := ttyWSTestServer(t, k)
	k.buf.WriteString("0123456789ABCDEF") // real head = 16

	// since=10. Script: first incremental read says "caught up" (T1),
	// second (after the consult sees head 16 > cursor 10) delivers the
	// bytes that arrived in the window. The third read (cursor now 16) is
	// left unscripted so it falls through to the real ring and returns
	// ("", 16), which is the normal caught-up break.
	k.scriptIncrementalReads(
		scriptedRead{body: "", next: 10},
		scriptedRead{body: "ABCDEF", next: 16},
	)

	c, replay, syncOffset := dialHandshake(t, addr, ttyWSPath(instID, "since=10"))
	_ = c.WriteClose(ws.CloseMessage(1000, "bye"))
	_ = c.Close()
	if replay != "ABCDEF" {
		t.Fatalf("replay = %q, want ABCDEF — the bytes that arrived between the caught-up read and the Tail consult MUST be delivered, not just covered by the offset (FIX-F)", replay)
	}
	if syncOffset != 16 {
		t.Fatalf("sync offset = %d, want 16 (head of the delivered replay)", syncOffset)
	}
}

// TestHandleInstanceTTYWS_SubscriberOverflowClosesConnectionWith1013 pins
// issue #82 end to end: when the live subscription is closed because the
// consumer stopped keeping up, the handler must NAME the reason on the wire
// and then stop serving.
//
// The condition exercised is the one that matters: the producer outruns the
// consumer's queue. The client goes quiet after the handshake, so nothing
// drains, while publish keeps pushing 32 KB chunks until the double's
// 16-slot channel fills and its overflow arm removes and closes the
// subscriber — the same state a production socket stall produces (a
// backgrounded tab stops reading, `conn.WriteBinary` blocks inside
// `rw.Flush()` because Upgrade sets no write deadline, the select loop stops
// draining, the queue backs up).
//
// To be exact about the fidelity: this test does NOT reproduce the TCP
// stall itself. A publish is a mutex plus a non-blocking send (~100 ns)
// while one handler loop iteration costs a 32 KB `write()` syscall, so the
// flood outruns the drain by orders of magnitude and the socket buffers never
// become the binding constraint — the overflow is reached in milliseconds.
// What is genuine, and what #82 actually changed, is everything downstream of
// the close: the real handler sees `<-outputChan` closed, writes 1013 with
// the reason on a real hijacked WebSocket, returns, closes the socket, and
// the deferred cancel runs against an already-closed channel.
//
// That frame is best-effort in production as well — the stalled socket may
// never drain it, which is why the teardown write carries a bounded deadline
// (app.go) — and the client resyncs on any socket close, reasoned or not.
func TestHandleInstanceTTYWS_SubscriberOverflowClosesConnectionWith1013(t *testing.T) {
	t.Parallel()
	k := newTTYHandshakeKind(1024)
	addr, _, instID := ttyWSTestServer(t, k)

	c, _, _ := dialHandshake(t, addr, ttyWSPath(instID, ""))
	k.waitSubscribers(t, 5*time.Second)

	// Outrun the queue: the client is not reading, so every chunk the
	// handler does take has to be matched by several more publishes before
	// the 16 slots run out.
	chunk := strings.Repeat("x", 32*1024)
	published := 0
	deadline := time.Now().Add(20 * time.Second)
	for k.subscriberCount() > 0 {
		if time.Now().After(deadline) {
			t.Fatalf("overflow never happened after %d chunks (%d MB published): the slow consumer was not disconnected", published, published*len(chunk)/(1<<20))
		}
		k.publish(chunk)
		published++
	}
	if published < 17 {
		// 16 slots + 1 is the earliest an overflow can happen; fewer means
		// the subscriber vanished without overflowing.
		t.Fatalf("subscriber vanished after only %d chunks, want at least 17 (the 16-slot capacity plus the one that did not fit)", published)
	}

	// Chunks the handler had already drained, and the resize echo its own
	// handshake queued, may still precede the close. What must never
	// precede it is a second `sync`: that would hand the client a cursor
	// past bytes it never received, so its next reconnect would skip them.
	for {
		op, p := readFrame(t, c, 10*time.Second)
		if op == wsOpClose {
			if len(p) < 2 {
				t.Fatalf("close frame payload too short: %v", p)
			}
			if got := binary.BigEndian.Uint16(p); got != 1013 {
				t.Fatalf("close code = %d, want 1013 (reason %q)", got, string(p[2:]))
			}
			if !strings.Contains(string(p[2:]), "overflow") {
				t.Fatalf("close reason = %q, want it to name the overflow so the browser console explains the drop", string(p[2:]))
			}
			break
		}
		if op == wsOpText && strings.Contains(string(p), `"sync"`) {
			t.Fatalf("a second sync after the teardown claims a cursor past undelivered bytes: %q", p)
		}
	}

	// Nothing after the close: the handler returned instead of looping on a
	// subscription it had already lost.
	expectSocketClosed(t, c, 10*time.Second)

	// The teardown deregistered the subscriber, and the handler's deferred
	// cancel ran against the channel the overflow had already closed.
	// Read this assertion for what it covers: an empty registry. It is NOT
	// the double-close pin — net/http recovers handler panics and logs them
	// to stderr, so a `close of closed channel` in that defer would be
	// reported by the harness, not by this test. The real exactly-once pin is
	// TestSubscribeOutput_CancelAfterOverflowDoesNotPanic (and its concurrent
	// counterpart TestSubscribeOutput_CancelRacesOverflow) in
	// internal/instance/pty/driver_test.go, where a panic fails the test.
	if n := k.subscriberCount(); n != 0 {
		t.Fatalf("%d subscriber(s) still registered after the teardown", n)
	}
}
