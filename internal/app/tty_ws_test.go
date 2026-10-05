package app

import (
	"context"
	"encoding/binary"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"regexp"
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
	subs     map[chan string]struct{}
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
		subs: map[chan string]struct{}{},
	}
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

func (k *ttyHandshakeKind) SubscribeOutput(id string) (<-chan string, func(), error) {
	ch := make(chan string, 16)
	k.subsMu.Lock()
	k.subs[ch] = struct{}{}
	k.subsMu.Unlock()
	var once sync.Once
	cancel := func() {
		once.Do(func() {
			k.subsMu.Lock()
			delete(k.subs, ch)
			k.subsMu.Unlock()
		})
	}
	return ch, cancel, nil
}

// publish stands in for pumpLogs' broadcast side of the PTY output pump.
func (k *ttyHandshakeKind) publish(chunk string) {
	k.subsMu.Lock()
	defer k.subsMu.Unlock()
	for ch := range k.subs {
		select {
		case ch <- chunk:
		default:
		}
	}
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

// dialHandshake dials the TTY endpoint, consumes the ready frame, sends
// the first resize (which completes the handshake immediately instead of
// waiting out the 5s timer), and collects frames up to the `sync` echo.
// It returns the connection, the concatenated binary replay, and the
// offset carried by the sync frame.
func dialHandshake(t *testing.T, addr, path string) (c *ws.Conn, replay string, syncOffset int64) {
	t.Helper()
	conn := dialTTY(t, addr, path)

	sendResize(t, conn)

	var body strings.Builder
	sawSync := false
	for !sawSync {
		op, p := readFrame(t, conn, 5*time.Second)
		switch op {
		case wsOpBinary:
			body.Write(p)
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
	return conn, body.String(), syncOffset
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

// TestHandleInstanceTTYWS_LargeDeltaReplaysNewestContiguousChunk pins the
// over-64KB reconnect case (review BLOCKER): when the offline delta
// exceeds the 64KB read cap, a single ReadSince would publish a sync
// cursor 64KB behind head while the live subscription starts AT head —
// a permanent, silent hole, because the client cursor only moves forward.
// The catch-up loop must instead replay the newest chunk contiguous with
// head and publish sync == head.
func TestHandleInstanceTTYWS_LargeDeltaReplaysNewestContiguousChunk(t *testing.T) {
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

	c2, replay2, sync2 := dialHandshake(t, addr, ttyWSPath(instID, "since=100"))
	_ = c2.WriteClose(ws.CloseMessage(1000, "bye"))
	_ = c2.Close()

	if sync2 != 100100 {
		t.Fatalf("sync offset = %d, want 100100 — the published cursor must equal head so the live stream stays contiguous with the replay (no hole)", sync2)
	}
	if len(replay2) > 65536 {
		t.Fatalf("replay = %d bytes, want the 64KB cap respected", len(replay2))
	}
	// The retained replay is contiguous with head: the exact suffix of
	// everything ever written, and — with sync == head — the live stream
	// continues exactly where it ends.
	if !strings.HasSuffix(content, replay2) {
		t.Fatal("replay must be contiguous with head (a suffix of the ring content)")
	}
	// Exact pin of the loop's chunking: [100,65636) is read first and
	// discarded, [65636,100100) is retained, then the loop sees head.
	if replay2 != content[65636:] {
		t.Fatalf("replay = %d bytes, want the newest chunk [65636,100100) = %d bytes", len(replay2), 100100-65636)
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

// --- the follow-from-live-end sentinel (issue #86) ------------------------

// sseLogFrame is one `event: log` payload of the SSE stream.
type sseLogFrame struct {
	Chunk string `json:"chunk"`
	Next  int64  `json:"next"`
}

// parseSSELogFrames extracts the log events of a recorded SSE body in
// order, ignoring the `: ping` keep-alive comment blocks.
func parseSSELogFrames(t *testing.T, body string) []sseLogFrame {
	t.Helper()
	const prefix = "event: log\ndata: "
	var out []sseLogFrame
	for _, block := range strings.Split(body, "\n\n") {
		if !strings.HasPrefix(block, prefix) {
			continue
		}
		var f sseLogFrame
		if err := json.Unmarshal([]byte(strings.TrimPrefix(block, prefix)), &f); err != nil {
			t.Fatalf("undecodable log frame %q: %v", block, err)
		}
		out = append(out, f)
	}
	return out
}

// startStream runs handleInstanceLogStream on a cancellable request against
// a recording writer and returns it with a waiter. Unlike runStreamUntilCancel
// it stays open while the test produces more data, so a follow-up frame can
// be observed on the SAME stream — which is the whole point of the
// follow-from-live-end sentinel (issue #86).
func startStream(t *testing.T, srv *Server, url string) (*syncStreamWriter, func(t *testing.T, want string) string) {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	req := httptest.NewRequest(http.MethodGet, url, nil).WithContext(ctx)
	w := newSyncStreamWriter()
	done := make(chan struct{})
	go func() {
		srv.handleInstanceLogStream(w, req)
		close(done)
	}()
	t.Cleanup(func() {
		cancel()
		select {
		case <-done:
		case <-time.After(5 * time.Second):
			t.Error("handleInstanceLogStream did not return after its context was cancelled")
		}
	})
	wait := func(t *testing.T, want string) string {
		t.Helper()
		deadline := time.Now().Add(5 * time.Second)
		for !strings.Contains(w.String(), want) {
			if time.Now().After(deadline) {
				t.Fatalf("stream body never contained %q; got %q", want, w.String())
			}
			time.Sleep(10 * time.Millisecond)
		}
		return w.String()
	}
	return w, wait
}

// TestHandleInstanceLogStream_FollowLiveEndServesNoReplayThenOnlyNewBytes is
// the SSE half of issue #86: a client whose screen already holds the tail but
// whose offset was stripped (hence since=-2) must get an EMPTY first frame
// carrying the live end offset — not the tail again — and then only bytes
// produced after that instant.
func TestHandleInstanceLogStream_FollowLiveEndServesNoReplayThenOnlyNewBytes(t *testing.T) {
	t.Parallel()
	k := newTTYHandshakeKind(1024)
	srv, m := newLogTestServer(t, k)
	instID := startKindInstance(t, m, "tty-handshake")
	k.buf.WriteString("SEEDED-TAIL") // head = 11

	_, wait := startStream(t, srv, "/api/instances/log/stream?id="+instID+"&since=-2")
	body := wait(t, `"next":11`)
	frames := parseSSELogFrames(t, body)
	if len(frames) != 1 {
		t.Fatalf("frames before the new data = %d, want exactly 1 (the empty sentinel frame): %v", len(frames), frames)
	}
	if frames[0].Chunk != "" {
		t.Fatalf("first frame chunk = %q, want empty — the client already painted this tail (issue #86)", frames[0].Chunk)
	}
	if frames[0].Next != 11 {
		t.Fatalf("first frame next = %d, want 11 (the live end offset the client lacked)", frames[0].Next)
	}

	// Only bytes produced after the sentinel read may ever follow on this
	// stream. The 1s poll loop picks them up off the ring buffer.
	k.buf.WriteString("NEW")
	k.publish("NEW")
	body = wait(t, `"chunk":"NEW"`)
	frames = parseSSELogFrames(t, body)
	if len(frames) != 2 {
		t.Fatalf("frames = %d, want the empty sentinel frame then the new data: %v", len(frames), frames)
	}
	if frames[1].Chunk != "NEW" || frames[1].Next != 14 {
		t.Fatalf("second frame = (%q, %d), want (\"NEW\", 14)", frames[1].Chunk, frames[1].Next)
	}
	if strings.Contains(body, "SEEDED-TAIL") {
		t.Fatalf("the sentinel stream re-delivered the seeded tail: %q", body)
	}
}

// TestHandleInstanceLogStream_OmittedAndRealSinceKeepTodaySemantics is the
// regression guard beside it: the sentinel must not have moved the two
// existing modes. An omitted `since` (the plain-unknown cursor: nothing
// painted) still gets the tail; a real cursor still gets [since, head).
func TestHandleInstanceLogStream_OmittedAndRealSinceKeepTodaySemantics(t *testing.T) {
	t.Parallel()
	k := newTTYHandshakeKind(1024)
	srv, m := newLogTestServer(t, k)
	instID := startKindInstance(t, m, "tty-handshake")
	k.buf.WriteString("SEEDED-TAIL") // head = 11

	_, waitTail := startStream(t, srv, "/api/instances/log/stream?id="+instID)
	tailFrames := parseSSELogFrames(t, waitTail(t, `"chunk":"SEEDED-TAIL"`))
	if tailFrames[0].Chunk != "SEEDED-TAIL" || tailFrames[0].Next != 11 {
		t.Fatalf("omitted since: first frame = (%q, %d), want the tail (\"SEEDED-TAIL\", 11)", tailFrames[0].Chunk, tailFrames[0].Next)
	}

	_, waitFrom6 := startStream(t, srv, "/api/instances/log/stream?id="+instID+"&since=6")
	from6 := parseSSELogFrames(t, waitFrom6(t, `"chunk":"-TAIL"`))
	if from6[0].Chunk != "-TAIL" || from6[0].Next != 11 {
		t.Fatalf("since=6: first frame = (%q, %d), want the incremental (\"-TAIL\", 11)", from6[0].Chunk, from6[0].Next)
	}
}

// TestHandleInstanceTTYWS_FollowLiveEndReplaysNothing is the WS half of
// issue #86: the handshake emits NO binary frame at all (every binary frame
// is ring-buffer output the client counts into its cursor), publishes the
// live end offset in `sync`, and then delivers live output normally.
func TestHandleInstanceTTYWS_FollowLiveEndReplaysNothing(t *testing.T) {
	t.Parallel()
	k := newTTYHandshakeKind(1024)
	addr, _, instID := ttyWSTestServer(t, k)
	k.buf.WriteString("HELLO-TAIL") // head = 10

	c, replay, syncOffset := dialHandshake(t, addr, ttyWSPath(instID, "since=-2"))
	if replay != "" {
		t.Fatalf("sentinel handshake replayed %q, want no binary frame at all — the client's screen already holds the tail", replay)
	}
	if syncOffset != 10 {
		t.Fatalf("sync offset = %d, want 10 (the live end offset)", syncOffset)
	}

	// Live output still flows: the sentinel suppresses the replay, not the
	// stream. Wait for the subscription before publishing (see waitSubscribers).
	k.waitSubscribers(t, 5*time.Second)
	k.buf.WriteString("LIVE")
	k.publish("LIVE")
	var op byte
	var p []byte
	for deadline := time.Now().Add(5 * time.Second); time.Now().Before(deadline); {
		op, p = readFrame(t, c, 5*time.Second)
		if op == wsOpBinary {
			break
		}
	}
	if op != wsOpBinary || string(p) != "LIVE" {
		t.Fatalf("live frame op=%d payload=%q, want binary LIVE", op, p)
	}
	if k.buf.Offset()-syncOffset != int64(len("LIVE")) {
		t.Fatalf("ring advanced by %d from sync %d, want %d", k.buf.Offset()-syncOffset, syncOffset, len("LIVE"))
	}
	_ = c.WriteClose(ws.CloseMessage(1000, "bye"))
	_ = c.Close()
}

// TestHandleInstanceTTYWS_FollowLiveEndOnEmptyRingPublishesZero pins the
// fresh-instance edge: with nothing ever written the sentinel still completes
// the handshake — no replay, sync at 0 — and 0 is a legitimate cursor the
// client keeps, not a failure. (A stopped instance's equivalent is
// TestHandleInstanceTTYWS_FollowLiveEndOnStoppedInstanceCloses.)
func TestHandleInstanceTTYWS_FollowLiveEndOnEmptyRingPublishesZero(t *testing.T) {
	t.Parallel()
	k := newTTYHandshakeKind(1024)
	addr, _, instID := ttyWSTestServer(t, k)

	c, replay, syncOffset := dialHandshake(t, addr, ttyWSPath(instID, "since=-2"))
	if replay != "" {
		t.Fatalf("replay on an empty ring = %q, want nothing", replay)
	}
	if syncOffset != 0 {
		t.Fatalf("sync offset = %d, want 0 (head of an untouched ring)", syncOffset)
	}

	k.waitSubscribers(t, 5*time.Second)
	k.buf.WriteString("first")
	k.publish("first")
	var op byte
	var p []byte
	for deadline := time.Now().Add(5 * time.Second); time.Now().Before(deadline); {
		op, p = readFrame(t, c, 5*time.Second)
		if op == wsOpBinary {
			break
		}
	}
	if op != wsOpBinary || string(p) != "first" {
		t.Fatalf("live frame op=%d payload=%q, want binary first", op, p)
	}
	_ = c.WriteClose(ws.CloseMessage(1000, "bye"))
	_ = c.Close()
}

// TestHandleInstanceTTYWS_FollowLiveEndOnStoppedInstanceCloses pins the
// non-running edge the sentinel must share with Tail: Manager.EndOffset of a
// stopped instance is 0 exactly as Manager.Tail is ("", 0, nil), so the
// handshake publishes 0 and then SubscribeOutput closes the connection with
// 1013 — never a binary frame, never a hang.
func TestHandleInstanceTTYWS_FollowLiveEndOnStoppedInstanceCloses(t *testing.T) {
	t.Parallel()
	k := newTTYHandshakeKind(1024)
	addr, m, instID := ttyWSTestServer(t, k)
	k.buf.WriteString("buffered-before-stop")
	if err := m.Stop(instID); err != nil {
		t.Fatalf("Stop: %v", err)
	}

	c := dialTTY(t, addr, ttyWSPath(instID, "since=-2"))
	sendResize(t, c)
	for {
		op, p := readFrame(t, c, 5*time.Second)
		switch {
		case op == wsOpBinary:
			t.Fatalf("binary frame on a stopped instance: %q", p)
		case op == wsOpText && strings.Contains(string(p), `"sync"`):
			// sync is written before SubscribeOutput runs, so it may reach
			// the wire before the close — and it must carry 0, the stopped
			// instance's end offset, never anything the client claimed.
			if !strings.Contains(string(p), `"offset":0`) {
				t.Fatalf("sync on a stopped instance = %q, want offset 0", p)
			}
		case op == wsOpText:
			// The shared-size resize echo: not a cursor, not a failure.
		case op == wsOpClose:
			if len(p) < 2 {
				t.Fatalf("close frame payload too short: %v", p)
			}
			if got := binary.BigEndian.Uint16(p); got != 1013 {
				t.Fatalf("close code = %d, want 1013 (reason %q)", got, string(p[2:]))
			}
			if !strings.Contains(string(p[2:]), "instance not running") {
				t.Fatalf("close reason = %q, want it to name the missing instance", string(p[2:]))
			}
			return
		default:
			t.Fatalf("unexpected opcode %d before the close: %q", op, p)
		}
	}
}

// TestHandleInstanceTTYWS_FollowLiveEndReadFailureClosesConnection pins the
// sentinel branch's error path: it must fail like the other two — a 1013
// close, no binary frame, and no sync claiming an offset nobody read.
func TestHandleInstanceTTYWS_FollowLiveEndReadFailureClosesConnection(t *testing.T) {
	t.Parallel()
	k := newTTYHandshakeKind(1024)
	addr, _, instID := ttyWSTestServer(t, k)
	k.buf.WriteString("some-bytes")
	k.failRead.Store(true)

	c := dialTTY(t, addr, ttyWSPath(instID, "since=-2"))
	sendResize(t, c)
	for {
		op, p := readFrame(t, c, 5*time.Second)
		if op == wsOpClose {
			if len(p) < 2 {
				t.Fatalf("close frame payload too short: %v", p)
			}
			if got := binary.BigEndian.Uint16(p); got != 1013 {
				t.Fatalf("close code = %d, want 1013 (reason %q)", got, string(p[2:]))
			}
			break
		}
		if op == wsOpBinary {
			t.Fatalf("error text smuggled as binary frame: %q", p)
		}
		if op == wsOpText && strings.Contains(string(p), `"sync"`) {
			t.Fatalf("sync published after a failed end-offset read: %q", p)
		}
	}
}

// TestFollowLiveEndSentinelAgreesAcrossTheWire pins the one number that has
// no compiler between its two declarations: the browser's
// CURSOR_FOLLOW_LIVE_END and the server's sinceFollowLiveEnd must be the
// same value, or every stripped-header client silently falls back to the
// tail — the exact bug of issue #86.
//
// It also pins the OTHER half of the contract, which is a crash rather than a
// behaviour change: every <script> index.html loads is a classic script, so
// they share one global lexical environment and kinds/pty.js must NOT declare
// a top-level CURSOR_* name. If it ever does, the inline application script
// dies at parse time and no amount of passing Go tests would notice.
func TestFollowLiveEndSentinelAgreesAcrossTheWire(t *testing.T) {
	t.Parallel()

	indexSrc, err := os.ReadFile("../ui/static/index.html")
	if err != nil {
		t.Fatalf("read index.html: %v", err)
	}
	m := regexp.MustCompile(`(?m)^\s*const CURSOR_FOLLOW_LIVE_END = ([^;\n]+);`).FindSubmatch(indexSrc)
	if m == nil {
		t.Fatal("index.html no longer declares CURSOR_FOLLOW_LIVE_END as a named constant")
	}
	nums := regexp.MustCompile(`-?\d+`).FindAllString(string(m[1]), -1)
	if len(nums) != 1 {
		t.Fatalf("index.html declares CURSOR_FOLLOW_LIVE_END = %q, want exactly one numeric literal", m[1])
	}
	n, err := strconv.ParseInt(nums[0], 10, 64)
	if err != nil {
		t.Fatalf("index.html sentinel literal %q is not an int: %v", nums[0], err)
	}
	if n != sinceFollowLiveEnd {
		t.Fatalf("index.html CURSOR_FOLLOW_LIVE_END = %d, but the server branches on %d", n, sinceFollowLiveEnd)
	}

	ptySrc, err := os.ReadFile("../ui/static/kinds/pty.js")
	if err != nil {
		t.Fatalf("read kinds/pty.js: %v", err)
	}
	// Same -1, prefixed name: the prefix is what stops the two classic
	// scripts colliding in the shared global lexical environment.
	uk := regexp.MustCompile(`(?m)^\s*const PTY_CURSOR_UNKNOWN = ([^;\n]+);`).FindSubmatch(ptySrc)
	if uk == nil {
		t.Fatal("kinds/pty.js no longer declares PTY_CURSOR_UNKNOWN as a named constant")
	}
	if pn := regexp.MustCompile(`-?\d+`).FindAllString(string(uk[1]), -1); len(pn) != 1 || pn[0] != "-1" {
		t.Fatalf("kinds/pty.js PTY_CURSOR_UNKNOWN = %q, want exactly the literal -1 that index.html uses", uk[1])
	}
	if c := regexp.MustCompile(`(?m)^\s*const CURSOR_\w+ =`).FindString(string(ptySrc)); c != "" {
		t.Fatalf("kinds/pty.js declares %q at top level; index.html declares the same global lexical name, so its script block would throw SyntaxError and kill the whole page", c)
	}
}

// TestHandleInstanceLogStream_FollowLiveEndOnStoppedInstancePublishesZero
// covers the SSE half of what docs/API.md promises for a stopped instance in
// sentinel mode: 200, ONE empty frame whose cursor is 0, then keep-alives —
// the stream stays open and never replays what the ring still holds, because
// a client that sent the sentinel has already painted that tail.
func TestHandleInstanceLogStream_FollowLiveEndOnStoppedInstancePublishesZero(t *testing.T) {
	t.Parallel()
	k := newTTYHandshakeKind(1024)
	srv, m := newLogTestServer(t, k)
	instID := startKindInstance(t, m, "tty-handshake")
	k.buf.WriteString("SEEDED-TAIL") // still in the ring after the Stop below
	if err := m.Stop(instID); err != nil {
		t.Fatalf("stop: %v", err)
	}

	w, wait := startStream(t, srv, "/api/instances/log/stream?id="+instID+"&since=-2")
	body := wait(t, `"next":0`)
	frames := parseSSELogFrames(t, body)
	if len(frames) != 1 {
		t.Fatalf("frames = %d, want exactly the one empty sentinel frame: %v", len(frames), frames)
	}
	if frames[0].Chunk != "" {
		t.Fatalf("chunk = %q, want empty — the ring still holds the tail and it must NOT be replayed here", frames[0].Chunk)
	}
	if frames[0].Next != 0 {
		t.Fatalf("next = %d, want 0 (EndOffset of a stopped instance reports 0 like Tail does)", frames[0].Next)
	}
	// Stays open on keep-alives rather than closing or replaying.
	body = wait(t, ": ping")
	if strings.Contains(body, "SEEDED-TAIL") {
		t.Fatalf("a stopped instance's sentinel stream replayed the ring: %q", body)
	}
	if strings.Contains(body, "-2") {
		t.Fatalf("the sentinel leaked into a published cursor: %q", body)
	}
	_ = w
}

// TestHandleInstanceLogStream_FollowLiveEndOnNonCapturingKindNeverEchoesTheSentinel
// is the wire-level pin for Manager.EndOffset's `off < 0` clamp. A kind with
// no log capture echoes the `since` it was handed back — and EndOffset hands
// it a negative (tail semantics) — so without the clamp this stream would
// publish {"next":-1} or {"next":-2}. The client would then re-send the
// sentinel on every reconnect forever, never advancing, and the tail it
// already painted would never be resolved into a real cursor. The manager
// tests pin the clamp at the manager layer; this is the only place the number
// reaches the wire.
func TestHandleInstanceLogStream_FollowLiveEndOnNonCapturingKindNeverEchoesTheSentinel(t *testing.T) {
	t.Parallel()
	srv, m := newLogTestServer(t, &emptyLogsKind{})
	instID := startKindInstance(t, m, "empty-logs")

	w, wait := startStream(t, srv, "/api/instances/log/stream?id="+instID+"&since=-2")
	body := wait(t, `"next":0`)
	frames := parseSSELogFrames(t, body)
	if len(frames) == 0 {
		t.Fatalf("no frames at all in %q", body)
	}
	if frames[0].Chunk != "" {
		t.Fatalf("chunk = %q, want empty", frames[0].Chunk)
	}
	for i, f := range frames {
		if f.Next < 0 {
			t.Fatalf("frame %d published cursor %d — a negative cursor on the wire makes the client re-send the sentinel forever; the body was %q", i, f.Next, body)
		}
	}
	if got := frames[0].Next; got != 0 {
		t.Fatalf("first frame next = %d, want the clamped 0", got)
	}
	_ = w
}

// TestHandleInstanceLog_RejectsFollowLiveEndSentinel pins FIX-F: the
// non-streaming endpoint treats any negative `since` as "give me the tail",
// which is the exact OPPOSITE of what the sentinel means. Silently serving the
// tail there would resurrect the duplication this value exists to prevent, so
// the mistake has to surface as a 400 — while the ordinary negative cursor
// (an omitted `since` from loadLog, or any other negative) keeps its tail
// semantics untouched.
func TestHandleInstanceLog_RejectsFollowLiveEndSentinel(t *testing.T) {
	t.Parallel()
	k := &logReplayKind{}
	srv, m := newLogTestServer(t, k)
	instID := startKindInstance(t, m, "log-replay")

	req := httptest.NewRequest(http.MethodGet, "/api/instances/log?id="+instID+"&since=-2", nil)
	w := httptest.NewRecorder()
	srv.handleInstanceLog(w, req)

	if w.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400 — this endpoint has no follow-from-the-live-end mode", w.Code)
	}
	if body := w.Body.String(); !strings.Contains(body, "/api/instances/log/stream") {
		t.Fatalf("error body = %q, want it to name the endpoints where the sentinel IS valid", body)
	}
	if body := w.Body.String(); body == "" || strings.Contains(body, "newest-bytes") {
		t.Fatalf("the rejected request must not be served a tail; body = %q", body)
	}

	// The neighbouring cursors are untouched: omitted still means tail,
	// a positive one still means incremental.
	req = httptest.NewRequest(http.MethodGet, "/api/instances/log?id="+instID, nil)
	w = httptest.NewRecorder()
	srv.handleInstanceLog(w, req)
	if w.Code != http.StatusOK || w.Body.String() != "newest-bytes" {
		t.Fatalf("omitted since = (%d, %q), want (200, \"newest-bytes\")", w.Code, w.Body.String())
	}
	req = httptest.NewRequest(http.MethodGet, "/api/instances/log?id="+instID+"&since=7", nil)
	w = httptest.NewRecorder()
	srv.handleInstanceLog(w, req)
	if w.Code != http.StatusOK || w.Body.String() != "chunk-from-cursor" {
		t.Fatalf("since=7 = (%d, %q), want (200, \"chunk-from-cursor\")", w.Code, w.Body.String())
	}
	req = httptest.NewRequest(http.MethodGet, "/api/instances/log?id="+instID+"&since=-1", nil)
	w = httptest.NewRecorder()
	srv.handleInstanceLog(w, req)
	if w.Code != http.StatusOK || w.Body.String() != "newest-bytes" {
		t.Fatalf("since=-1 = (%d, %q), want the tail as always", w.Code, w.Body.String())
	}
}
