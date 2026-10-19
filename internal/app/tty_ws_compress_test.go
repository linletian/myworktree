package app

// Issue #114 decision 5a (TASK-A3): app-layer compression over the TTY
// WebSocket. These tests drive the real handler over a real hijacked socket
// and pin, per the §三 R12 four-combination matrix:
//
//	caps                    overflow      binary wire      cursor unit
//	(none, legacy)         1013          plaintext          wire=logical
//	overflow-notice        notice+drop   plaintext          wire=logical
//	compress               1013          deflate-raw        logical (inflated)
//	both                   notice+drop   deflate-raw        logical (inflated)
//
// plus the two structural pins the design rests on: the per-connection
// deflate stream is PERSISTENT (one context across frames — replay and live
// share it, never Closed mid-stream), and the legacy wire is byte-identical
// to the pre-compression bytes (zero flate framing on that path).

import (
	"bytes"
	"compress/flate"
	"context"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"myworktree/internal/ws"
)

// The two ready frames, pinned byte-for-byte (TASK-A3 C1.7): a compressed
// connection's ready echoes the decision, every other connection's ready is
// the legacy frame verbatim.
const (
	compressReadyFrame = `{"type":"ready","compress":true}`
	legacyReadyFrame   = `{"type":"ready"}`
)

// compressSeedLine is the replay content for the handshake tests: repetitive
// text (deflate compresses it, though no ratio is pinned — C4.5) that
// contains no 0x78 byte anywhere. That keeps the legacy plaintext pin
// unambiguous: 0x78 is the ZLIB container's CMF byte, and the wire format
// here is raw deflate (RFC 1951, what flate.Writer emits and what
// DecompressionStream('deflate-raw') reads) which carries NO such
// header — so the real discriminator between the two paths is inflate-vs-
// verbatim, asserted below, not any magic leading byte.
func compressSeedLine(i int) string {
	return fmt.Sprintf("line-%06d: the quick brown dog jumps over the lazy dog\n", i)
}

func compressSeed(n int) string {
	var b strings.Builder
	for i := 0; i < n; i++ {
		b.WriteString(compressSeedLine(i))
	}
	return b.String()
}

// inflateTTYStream inflates the concatenated BINARY payloads of one
// connection back to logical bytes — the test-side mirror of the client's
// persistent DecompressionStream('deflate-raw'), and the instrument for
// C4.4: a per-frame stream (or any desync) fails or truncates here, while
// the contract's single shared context round-trips exactly.
//
// The trailing io.ErrUnexpectedEOF is EXPECTED and ends the read cleanly:
// the server never Closes the shared stream mid-connection (5a pin 2 —
// Close would emit a final block and desync every later frame), so the
// bytes captured from a live socket end mid-block by contract, exactly the
// state a streaming client inflates without complaint.
func inflateTTYStream(t *testing.T, payloads [][]byte) []byte {
	t.Helper()
	zr := flate.NewReader(bytes.NewReader(bytes.Join(payloads, nil)))
	defer func() { _ = zr.Close() }()
	var out bytes.Buffer
	buf := make([]byte, 64*1024)
	for {
		n, err := zr.Read(buf)
		out.Write(buf[:n])
		if err == nil {
			continue
		}
		if err == io.EOF || err == io.ErrUnexpectedEOF {
			return out.Bytes()
		}
		t.Fatalf("inflate of the compressed wire failed: %v", err)
	}
}

// dialTTYCompress dials a compressed connection and pins the compress
// echo in the ready frame byte-for-byte. It deliberately does NOT reuse
// dialTTY (whose byte-pin is the legacy frame): the two ready shapes are
// separate contracts, each pinned by its own helper, so the paths cannot
// silently collapse into each other.
func dialTTYCompress(t *testing.T, addr, path string) *ws.Conn {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	conn, err := ws.Dial(ctx, addr, path, nil)
	if err != nil {
		t.Fatalf("dial %s: %v", path, err)
	}
	t.Cleanup(func() { _ = conn.Close() }) // idempotent
	op, p := readFrame(t, conn, 5*time.Second)
	if op != wsOpText || string(p) != compressReadyFrame {
		t.Fatalf("ready frame op=%d payload=%q, want text %q", op, p, compressReadyFrame)
	}
	return conn
}

// dialHandshakeCompress is dialHandshakeStart for a compressed connection:
// ready (compress echo) → resize → handshake frames up to the closing sync,
// collecting the BINARY payloads as deflate segments and requiring every
// TEXT frame to be plaintext JSON (C4.1: TEXT is never compressed, on a
// compressed connection the frame-type split is the only thing keeping the
// client's inflate stream and its control parser apart).
func dialHandshakeCompress(t *testing.T, addr, path string) (c *ws.Conn, binaries [][]byte, syncOffset int64) {
	t.Helper()
	conn := dialTTYCompress(t, addr, path)
	sendResize(t, conn)
	sawSync := false
	for !sawSync {
		op, p := readFrame(t, conn, 5*time.Second)
		switch op {
		case wsOpBinary:
			binaries = append(binaries, p)
		case wsOpPing:
		case wsOpText:
			var ctl struct {
				Type   string `json:"type"`
				Offset int64  `json:"offset"`
				Start  bool   `json:"start"`
			}
			if err := json.Unmarshal(p, &ctl); err != nil {
				t.Fatalf("TEXT frame %q on a compressed connection is not plaintext JSON: %v", p, err)
			}
			switch ctl.Type {
			case "sync":
				if ctl.Start {
					continue
				}
				syncOffset = ctl.Offset
				sawSync = true
			case "resize", "ping", "pong":
			default:
				t.Fatalf("unexpected control frame %q before sync", p)
			}
		default:
			t.Fatalf("unexpected opcode %d during handshake", op)
		}
	}
	return conn, binaries, syncOffset
}

// floodUntilOverflow publishes the chunk until the legacy subscriber is
// torn down (the 16-slot queue ran out): the same outrun-the-drain shape
// TestHandleInstanceTTYWS_SubscriberOverflowClosesConnectionWith1013
// proves deterministic, reused verbatim so the compress cells drive the
// identical overflow mechanics.
func floodUntilOverflow(t *testing.T, k *ttyHandshakeKind, chunk string) int {
	t.Helper()
	published := 0
	deadline := time.Now().Add(20 * time.Second)
	for k.subscriberCount() > 0 {
		if time.Now().After(deadline) {
			t.Fatalf("overflow never happened after %d publishes: the slow consumer was not disconnected", published)
		}
		k.publish(chunk)
		published++
	}
	if published < 17 {
		t.Fatalf("subscriber vanished after only %d publishes, want at least 17", published)
	}
	return published
}

// floodUntilDrop publishes until the drop-oldest subscriber has counted at
// least one eviction (the shape the overflow-notice test proved).
func floodUntilDrop(t *testing.T, k *ttyHandshakeKind, chunk string) int {
	t.Helper()
	published := 0
	deadline := time.Now().Add(20 * time.Second)
	for {
		if time.Now().After(deadline) {
			t.Fatalf("no drops counted after %d publishes into a 16-slot queue", published)
		}
		k.publish(chunk)
		published++
		if dc, _ := k.droppedTotals(); dc > 0 {
			break
		}
	}
	return published
}

// TestHandleInstanceTTYWS_CompressCapDeflatesReplayWithLogicalCursor pins
// the compress cell of the R12 matrix at handshake: ready echoes
// compress:true, the replay arrives as MULTIPLE deflate segments that one
// single-stream inflate restores to the seeded bytes exactly (persistence
// pin C4.4 — per-frame streams would truncate here; BestSpeed pin C4.5 —
// decodable, ratio deliberately unpinned), every TEXT frame stays plaintext
// JSON, the wire is genuinely smaller than the logical seed, and the
// closing sync's cursor is the LOGICAL byte count (C2.1: the ring offset,
// not the compressed length).
func TestHandleInstanceTTYWS_CompressCapDeflatesReplayWithLogicalCursor(t *testing.T) {
	t.Parallel()
	k := newTTYHandshakeKind(1 << 20)
	addr, _, instID, _ := ttyWSTestServer(t, k)
	seed := compressSeed(4000) // ~184KB: over 64KB, so the catch-up replay is several frames
	k.buf.WriteString(seed)

	conn, binaries, syncOffset := dialHandshakeCompress(t, addr, ttyWSPath(instID, "caps=sync,compress&since=0"))
	_ = conn

	if len(binaries) < 2 {
		t.Fatalf("handshake replay arrived in %d binary frame(s), want ≥2 for the persistence pin", len(binaries))
	}
	got := inflateTTYStream(t, binaries)
	if string(got) != seed {
		t.Fatalf("single-stream inflate restored %d bytes, want the seeded %d verbatim (first mismatch at %d)", len(got), len(seed), firstDiff(got, []byte(seed)))
	}
	var wire int
	for _, f := range binaries {
		wire += len(f)
	}
	if wire >= len(seed) {
		t.Fatalf("compressed wire %d >= logical seed %d: compression did not happen (not a ratio pin, a sanity floor)", wire, len(seed))
	}
	if syncOffset != int64(len(seed)) {
		t.Fatalf("closing sync offset = %d, want the LOGICAL byte count %d (cursor arithmetic never sees compressed lengths)", syncOffset, len(seed))
	}
}

// TestHandleInstanceTTYWS_NoCompressCapWireIsPlaintextVerbatim is the C4.3
// legacy pin: without the compress cap the binary frames are the ring bytes
// byte-for-byte — no deflate framing, no rechunking, ready frame verbatim.
// The 0x78 check is the ticket's literal smoke test: the seed provably
// contains no 0x78 byte, so one at a frame head could only mean framing
// leaked into the legacy path (the verbatim equality below is the full pin;
// raw deflate carries no header of its own, so no leading byte alone
// distinguishes the paths).
func TestHandleInstanceTTYWS_NoCompressCapWireIsPlaintextVerbatim(t *testing.T) {
	t.Parallel()
	k := newTTYHandshakeKind(1 << 20)
	addr, _, instID, _ := ttyWSTestServer(t, k)
	seed := compressSeed(4000)
	k.buf.WriteString(seed)

	c := dialTTY(t, addr, ttyWSPath(instID, "caps=sync&since=0")) // dialTTY pins the legacy ready bytes
	sendResize(t, c)
	var binaries [][]byte
	sawSync := false
	for !sawSync {
		op, p := readFrame(t, c, 5*time.Second)
		switch op {
		case wsOpBinary:
			binaries = append(binaries, p)
		case wsOpPing:
		case wsOpText:
			var ctl struct {
				Type  string `json:"type"`
				Start bool   `json:"start"`
			}
			if err := json.Unmarshal(p, &ctl); err != nil {
				t.Fatalf("TEXT frame %q is not plaintext JSON: %v", p, err)
			}
			if ctl.Type == "sync" && !ctl.Start {
				sawSync = true
			}
		default:
			t.Fatalf("unexpected opcode %d", op)
		}
	}
	if len(binaries) < 2 {
		t.Fatalf("replay arrived in %d frames, want ≥2", len(binaries))
	}
	for i, f := range binaries {
		if f[0] == 0x78 {
			t.Fatalf("frame %d starts with 0x78 on a plaintext connection: flate framing leaked into the legacy path", i)
		}
	}
	var plain []byte
	for _, f := range binaries {
		plain = append(plain, f...)
	}
	if string(plain) != seed {
		t.Fatalf("plaintext replay differs from the seeded bytes (%d vs %d, first mismatch at %d): the legacy wire must be byte-identical", len(plain), len(seed), firstDiff(plain, []byte(seed)))
	}
}

// TestHandleInstanceTTYWS_MatrixNoCapsOverflowPlaintext1013 is matrix cell
// 1: no caps → plaintext binary frames (each frame is one published chunk
// verbatim) and the #82 teardown with 1013 — today's behaviour, pinned to
// stay today's behaviour with the compressor code in the tree.
func TestHandleInstanceTTYWS_MatrixNoCapsOverflowPlaintext1013(t *testing.T) {
	t.Parallel()
	k := newTTYHandshakeKind(1 << 20)
	addr, _, instID, _ := ttyWSTestServer(t, k)

	c, _, _ := dialHandshake(t, addr, ttyWSPath(instID, "caps=sync"))
	k.waitSubscribers(t, 5*time.Second)
	chunk := strings.Repeat("abcdefgh", 4096) // 32KB, no 'x' byte at chunk heads
	floodUntilOverflow(t, k, chunk)

	for {
		op, p := readFrame(t, c, 10*time.Second)
		if op == wsOpClose {
			if len(p) < 2 {
				t.Fatalf("close payload too short")
			}
			if code := binary.BigEndian.Uint16(p); code != 1013 {
				t.Fatalf("close code = %d, want 1013 (reason %q)", code, string(p[2:]))
			}
			if !strings.Contains(string(p[2:]), "overflow") {
				t.Fatalf("close reason %q must name the overflow", string(p[2:]))
			}
			break
		}
		if op == wsOpBinary && !bytes.Equal(p, []byte(chunk)) {
			t.Fatalf("binary frame on the plaintext cell differs from the published chunk (%d bytes): wire must be verbatim", len(p))
		}
		if op == wsOpText && strings.Contains(string(p), `"sync"`) {
			t.Fatalf("sync after teardown claims a cursor past undelivered bytes: %q", p)
		}
	}
	expectSocketClosed(t, c, 10*time.Second)
}

// TestHandleInstanceTTYWS_MatrixOverflowNoticePlaintext is matrix cell 2:
// overflow-notice without compress → plaintext wire, the contract notice
// frame TEXT and plaintext, connection kept.
func TestHandleInstanceTTYWS_MatrixOverflowNoticePlaintext(t *testing.T) {
	t.Parallel()
	k := newTTYHandshakeKind(1 << 20)
	addr, _, instID, _ := ttyWSTestServer(t, k)

	c, _, _ := dialHandshake(t, addr, ttyWSPath(instID, "caps=sync,overflow-notice"))
	k.waitSubscribers(t, 5*time.Second)
	chunk := strings.Repeat("abcdefgh", 4096)
	floodUntilDrop(t, k, chunk)
	if n := k.subscriberCount(); n != 1 {
		t.Fatalf("%d subscribers after drop-oldest, want 1 (connection kept)", n)
	}

	var notice []byte
	for {
		op, p := readFrame(t, c, 10*time.Second)
		if op == wsOpClose {
			t.Fatalf("close frame on the notice cell: %q", p)
		}
		if op == wsOpBinary && !bytes.Equal(p, []byte(chunk)) {
			t.Fatalf("binary frame differs from the published chunk on the plaintext cell (%d bytes)", len(p))
		}
		if op == wsOpText && isOverflowNotice(p) {
			notice = p
			break
		}
	}
	var ctl struct {
		Type          string `json:"type"`
		DroppedBytes  int64  `json:"droppedBytes"`
		DroppedChunks int64  `json:"droppedChunks"`
	}
	if err := json.Unmarshal(notice, &ctl); err != nil {
		t.Fatalf("notice %q is not plaintext JSON: %v", notice, err)
	}
	if ctl.Type != "overflow" || ctl.DroppedBytes <= 0 || ctl.DroppedChunks <= 0 {
		t.Fatalf("notice %+v, want positive overflow counts", ctl)
	}
	// the chunk the notice precedes is plaintext too
	op, p := readFrame(t, c, 10*time.Second)
	if op != wsOpBinary || !bytes.Equal(p, []byte(chunk)) {
		t.Fatalf("frame after notice op=%d len=%d, want the plaintext chunk it precedes", op, len(p))
	}
}

// TestHandleInstanceTTYWS_MatrixCompressOverflowDeflate1013 is matrix cell
// 3: compress without notice → deflate-raw wire and the 1013 teardown
// (1013 still closes a compressed connection — the matrix column for
// overflow behaviour does not change with the wire codec). Every binary
// frame inflates, and the whole stream restores to exactly
// frames×chunk: a dropped chunk that had leaked into the deflate context
// would overshoot that byte count (C2.2).
func TestHandleInstanceTTYWS_MatrixCompressOverflowDeflate1013(t *testing.T) {
	t.Parallel()
	k := newTTYHandshakeKind(1 << 20)
	addr, _, instID, _ := ttyWSTestServer(t, k)

	c, _, _ := dialHandshakeCompress(t, addr, ttyWSPath(instID, "caps=sync,compress"))
	k.waitSubscribers(t, 5*time.Second)
	chunk := strings.Repeat("abcdefgh", 4096)
	floodUntilOverflow(t, k, chunk)

	var binaries [][]byte
	for {
		op, p := readFrame(t, c, 10*time.Second)
		if op == wsOpClose {
			if len(p) < 2 {
				t.Fatalf("close payload too short")
			}
			if code := binary.BigEndian.Uint16(p); code != 1013 {
				t.Fatalf("close code = %d, want 1013 (reason %q)", code, string(p[2:]))
			}
			if !strings.Contains(string(p[2:]), "overflow") {
				t.Fatalf("close reason %q must name the overflow", string(p[2:]))
			}
			break
		}
		if op == wsOpBinary {
			binaries = append(binaries, p)
		}
		if op == wsOpText && strings.Contains(string(p), `"sync"`) {
			t.Fatalf("sync after teardown claims a cursor past undelivered bytes: %q", p)
		}
	}
	if len(binaries) == 0 {
		t.Fatal("no binary frames before the 1013 teardown")
	}
	got := inflateTTYStream(t, binaries)
	if want := bytes.Repeat([]byte(chunk), len(binaries)); !bytes.Equal(got, want) {
		t.Fatalf("inflated stream = %d bytes, want exactly %d frames × %d-byte chunks with no extras (a leaked drop would overshoot)", len(got), len(binaries), len(chunk))
	}
	expectSocketClosed(t, c, 10*time.Second)
}

// TestHandleInstanceTTYWS_MatrixCompressNoticeDeflateKeepsConnection is
// matrix cell 4 AND the C4.2 mid-compress-drop pin: both caps → deflate-
// raw wire, drops counted, connection kept, notice TEXT plaintext ahead of
// the chunk it covers — and the whole captured binary stream inflates,
// across the drop boundary, to exactly frames×chunk: dropped chunks never
// entered the deflate context, so the shared stream never desyncs.
func TestHandleInstanceTTYWS_MatrixCompressNoticeDeflateKeepsConnection(t *testing.T) {
	t.Parallel()
	k := newTTYHandshakeKind(1 << 20)
	addr, _, instID, _ := ttyWSTestServer(t, k)

	c, _, _ := dialHandshakeCompress(t, addr, ttyWSPath(instID, "caps=sync,compress,overflow-notice"))
	k.waitSubscribers(t, 5*time.Second)
	chunk := strings.Repeat("abcdefgh", 4096)
	floodUntilDrop(t, k, chunk)
	if n := k.subscriberCount(); n != 1 {
		t.Fatalf("%d subscribers after drop-oldest, want 1 (connection kept)", n)
	}

	var binaries [][]byte
	var notice []byte
	for len(notice) == 0 {
		op, p := readFrame(t, c, 10*time.Second)
		switch {
		case op == wsOpClose:
			t.Fatalf("close frame on the notice cell: %q", p)
		case op == wsOpBinary:
			binaries = append(binaries, p)
		case op == wsOpText && isOverflowNotice(p):
			notice = p
		}
	}
	var ctl struct {
		Type          string `json:"type"`
		DroppedBytes  int64  `json:"droppedBytes"`
		DroppedChunks int64  `json:"droppedChunks"`
	}
	if err := json.Unmarshal(notice, &ctl); err != nil {
		t.Fatalf("notice %q is not plaintext JSON on a compressed connection: %v", notice, err)
	}
	if ctl.Type != "overflow" || ctl.DroppedChunks <= 0 || ctl.DroppedBytes <= 0 {
		t.Fatalf("notice %+v, want positive overflow counts", ctl)
	}
	// Drain a bounded window so frames that ride AFTER the drop boundary
	// are in the captured stream too — the desync proof must span it.
	windowEnd := time.Now().Add(2 * time.Second)
	for {
		if err := c.SetReadDeadline(windowEnd); err != nil {
			t.Fatalf("set read deadline: %v", err)
		}
		op, p, rerr := c.ReadMessage()
		if rerr != nil {
			break
		}
		switch {
		case op == wsOpClose:
			t.Fatalf("close frame mid-stream on the notice cell: %q", p)
		case op == wsOpBinary:
			binaries = append(binaries, p)
		}
		if time.Now().After(windowEnd) {
			break
		}
	}
	if len(binaries) == 0 {
		t.Fatal("no binary frames streamed on the notice cell")
	}
	got := inflateTTYStream(t, binaries)
	if want := bytes.Repeat([]byte(chunk), len(binaries)); !bytes.Equal(got, want) {
		t.Fatalf("inflated stream across the drop boundary = %d bytes, want exactly %d frames × %d-byte chunks (mid-stream desync or leaked drop: first mismatch at %d)", len(got), len(binaries), len(chunk), firstDiff(got, want))
	}
	if n := k.subscriberCount(); n != 1 {
		t.Fatalf("%d subscribers at the end, want the connection still subscribed", n)
	}
}

func firstDiff(a, b []byte) int {
	n := len(a)
	if len(b) < n {
		n = len(b)
	}
	for i := 0; i < n; i++ {
		if a[i] != b[i] {
			return i
		}
	}
	if len(a) != len(b) {
		return n
	}
	return -1
}

// ttyAlwaysFailWriter is the injectable failing sink for the compressor
// fault path: it accepts no byte and always errors, so flate.Writer's
// Write/Flush into it fail on the very first frame — the only way to reach
// the compressor-failure teardown on a real socket (a bytes.Buffer never
// errs). attempts counts the sink calls so the test can prove the sink was
// actually reached, not short-circuited.
type ttyAlwaysFailWriter struct{ attempts int }

func (w *ttyAlwaysFailWriter) Write(p []byte) (int, error) {
	w.attempts++
	return 0, errors.New("injected deflate sink failure")
}

// TestTTYCompressingConnFlateFailureSends1011 drives the compressor's
// failure teardown over a REAL socket: a compressor whose sink always fails
// makes the first binary write error, which must send a 1011 internal-error
// close (deliberately NOT 1013 — 1013 drives the overflow retry contract,
// and a dead compressor is not fixable by reconnecting to the same daemon),
// return the error to the caller, and let the handler's deferred Close
// reclaim the socket. The client sees exactly a close frame with 1011 and
// then EOF — never plaintext, never a 1013 retry.
func TestTTYCompressingConnFlateFailureSends1011(t *testing.T) {
	t.Parallel()

	var writeErr error
	sink := &ttyAlwaysFailWriter{}
	done := make(chan struct{})
	hs := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer close(done)
		raw, err := ws.Upgrade(w, r)
		if err != nil {
			t.Errorf("server upgrade: %v", err)
			return
		}
		mc := newTTYMeteredConn(raw, nil, "fault")
		defer mc.Close() // the deferred reclaim the test asserts completion of
		comp, cerr := newTTYCompressorSink(sink)
		if cerr != nil {
			t.Errorf("compressor with failing sink: %v", cerr)
			return
		}
		cc := newTTYCompressingConn(mc, comp)
		// The first (only) binary write must fail in the deflate stream and
		// tear the connection down with 1011.
		writeErr = cc.WriteBinary([]byte("terminal output that must never reach the wire as plaintext"))
	}))
	defer hs.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	c, err := ws.Dial(ctx, strings.TrimPrefix(hs.URL, "http://"), "/", nil)
	if err != nil {
		t.Fatalf("dial the fault server: %v", err)
	}
	defer c.Close()

	op, p := readFrame(t, c, 5*time.Second)
	if op != wsOpClose {
		t.Fatalf("after a compressor failure the only frame is the close, got op=%d payload=%q", op, p)
	}
	if len(p) < 2 {
		t.Fatalf("close frame carries no status code: %q", p)
	}
	if code := binary.BigEndian.Uint16(p[:2]); code != 1011 {
		t.Fatalf("close code = %d, want 1011 internal error — explicitly NOT 1013 (overflow retry) and not any other", code)
	}

	<-done // the handler ran its deferred Close
	if writeErr == nil {
		t.Fatalf("WriteBinary against a failing deflate sink must return the error, got nil")
	}
	if sink.attempts == 0 {
		t.Fatalf("the failing sink was never reached — the write short-circuited before the deflate flush")
	}
	expectSocketClosed(t, c, 5*time.Second)
}

// TestHandleInstanceTTYWS_CompressNearTokensGetLegacyReadyAndPlaintext pins
// that the `compress` capability is matched as a WHOLE token, exactly like
// the pinger gate's near-token precedent (tty_ws_test.go:2341): every token
// that merely LOOKS like compress — a superstring (compressx, compressor,
// xcompress), the uppercase form (COMPRESS, match is case-sensitive), a
// version suffix (compress-v2), or an empty k=v form (compress=) — must NOT
// flip the compressor on. Each such connection therefore gets the byte-
// exact legacy ready frame (dialed via dialTTY, which fatals on the
// compress echo) AND a binary replay that is plaintext VERBATIM, byte for
// byte equal to the seeded ring bytes — no deflate framing, no shrink.
func TestHandleInstanceTTYWS_CompressNearTokensGetLegacyReadyAndPlaintext(t *testing.T) {
	t.Parallel()
	nearTokens := []string{"compressx", "COMPRESS", "compressor", "compress-v2", "compress=", "xcompress"}
	for _, token := range nearTokens {
		token := token
		t.Run(token, func(t *testing.T) {
			t.Parallel()
			k := newTTYHandshakeKind(1 << 20)
			addr, _, instID, _ := ttyWSTestServer(t, k)
			seed := compressSeed(200)
			k.buf.WriteString(seed)

			// dialTTY pins the first frame to EXACTLY {"type":"ready"} —
			// a near token that wrongly enabled compression would echo
			// {"type":"ready","compress":true} and fail inside dialTTY.
			c := dialTTY(t, addr, ttyWSPath(instID, "caps=sync,"+token+"&since=0"))
			sendResize(t, c)

			var binaries [][]byte
			sawSync := false
			for !sawSync {
				op, p := readFrame(t, c, 5*time.Second)
				switch op {
				case wsOpBinary:
					binaries = append(binaries, p)
				case wsOpPing:
				case wsOpText:
					var ctl struct {
						Type  string `json:"type"`
						Start bool   `json:"start"`
					}
					if err := json.Unmarshal(p, &ctl); err != nil {
						t.Fatalf("TEXT frame %q is not plaintext JSON: %v", p, err)
					}
					if ctl.Type == "sync" && !ctl.Start {
						sawSync = true
					}
				default:
					t.Fatalf("unexpected opcode %d", op)
				}
			}
			if len(binaries) == 0 {
				t.Fatalf("token %q: no binary replay arrived", token)
			}
			var got []byte
			for _, f := range binaries {
				got = append(got, f...)
			}
			if string(got) != seed {
				t.Fatalf("token %q: binary wire differs from the plaintext seed (%d vs %d bytes, first mismatch at %d) — a near-token must NOT flip the compressor, the legacy wire is byte-identical", token, len(got), len(seed), firstDiff(got, []byte(seed)))
			}
		})
	}
}
