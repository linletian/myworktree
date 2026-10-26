package ws

import (
	"bufio"
	"bytes"
	"context"
	"encoding/binary"
	"errors"
	"io"
	"net"
	"net/http"
	"os"
	"sync"
	"testing"
	"time"
)

func TestWSAccept(t *testing.T) {
	key := "dGhlIHNhbXBsZSBub25jZQ=="
	want := "s3pPLMBiTxaQ9kYGzzhZRbK+xOo="
	if got := wsAccept(key); got != want {
		t.Fatalf("unexpected ws accept: got %q, want %q", got, want)
	}
}

func TestHeaderContainsToken(t *testing.T) {
	if !headerContainsToken("keep-alive, Upgrade", "upgrade") {
		t.Fatalf("should detect token case-insensitively")
	}
	if headerContainsToken("keep-alive, close", "upgrade") {
		t.Fatalf("unexpected token match")
	}
}

func TestCloseMessage(t *testing.T) {
	msg := CloseMessage(1000, "bye")
	if len(msg) != 5 {
		t.Fatalf("unexpected close message length: %d", len(msg))
	}
	if code := binary.BigEndian.Uint16(msg[:2]); code != 1000 {
		t.Fatalf("unexpected close code: %d", code)
	}
	if string(msg[2:]) != "bye" {
		t.Fatalf("unexpected close reason: %q", string(msg[2:]))
	}
}

func TestOpcodeHelpers(t *testing.T) {
	if !IsDataOpcode(opText) || !IsDataOpcode(opBinary) {
		t.Fatalf("data opcode helper mismatch")
	}
	if IsDataOpcode(opPing) {
		t.Fatalf("ping should not be data opcode")
	}
	if !IsPing(opPing) || IsPing(opClose) {
		t.Fatalf("ping helper mismatch")
	}
	if !IsClose(opClose) || IsClose(opText) {
		t.Fatalf("close helper mismatch")
	}
}

// --- per-write timeout (issue #114, decision 3) ----------------------------

// writeTimeoutStall runs one upgraded SERVER conn against a client that
// completes the handshake and then never reads, with both socket buffers
// pinned small so a big write blocks in the kernel — the exact stalled-peer
// state the tty write deadline exists to bound. The server-side loop is
// handed the upgraded conn and its result travels back on the channel.
func writeTimeoutStall(t *testing.T, loop func(*Conn) error) (error, time.Duration) {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = ln.Close() }()

	type outcome struct {
		err error
		at  time.Duration
	}
	out := make(chan outcome, 1)
	go serveUpgrade(t, ln, func(s *Conn, req *http.Request) {
		if tc, ok := s.Conn.(*net.TCPConn); ok {
			_ = tc.SetWriteBuffer(4096)
		}
		start := time.Now()
		err := loop(s)
		out <- outcome{err: err, at: time.Since(start)}
	})

	c, err := Dial(context.Background(), ln.Addr().String(), "/ws", nil)
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	defer func() { _ = c.Close() }()
	if tc, ok := c.Conn.(*net.TCPConn); ok {
		_ = tc.SetReadBuffer(4096)
	}
	// The client NEVER reads: every server write past the tiny buffers
	// stalls in the kernel. This is the stalled peer.
	res := <-out
	return res.err, res.at
}

// TestConnWriteTimeoutBoundsStalledWrite pins that SetWriteTimeout turns
// the write that used to block forever into a bounded, deadline-exceeded
// error (issue #114 AC3): the stalled-peer write must fail with a timeout,
// at the armed duration and not half of it.
func TestConnWriteTimeoutBoundsStalledWrite(t *testing.T) {
	const timeout = 150 * time.Millisecond
	chunk := make([]byte, 64*1024)

	err, at := writeTimeoutStall(t, func(s *Conn) error {
		s.SetWriteTimeout(timeout)
		for i := 0; i < 100; i++ {
			if err := s.WriteBinary(chunk); err != nil {
				return err
			}
		}
		return nil
	})

	if err == nil {
		t.Fatal("write to a stalled peer returned nil: the armed write deadline did not fire")
	}
	var ne net.Error
	if !errors.As(err, &ne) || !ne.Timeout() {
		t.Fatalf("error = %v, want a net timeout error (deadline-exceeded)", err)
	}
	if !errors.Is(err, os.ErrDeadlineExceeded) {
		t.Fatalf("error = %v, want it to unwrap to os.ErrDeadlineExceeded", err)
	}
	if at < timeout {
		t.Fatalf("write failed after %v, want >= the armed %v (a too-early failure means the deadline was mis-armed)", at, timeout)
	}
}

// TestConnWriteTimeoutZeroNeverTrips pins the zero-value contract from the
// other side: a Conn that never called SetWriteTimeout has NO deadline, so
// writes to a deaf peer stay BLOCKED — not failed — for as long as the peer
// stays deaf, and only the peer draining unblocks them. The "still blocked"
// observation is what distinguishes "no deadline" from "any deadline".
//
// It runs over net.Pipe, not TCP: the pipe is SYNCHRONOUS (every write
// blocks until the peer consumes byte-for-byte), so the stall is structural
// rather than a kernel-buffer accident — and a deaf-then-draining TCP peer
// with buffers shrunk on the live socket collapses its receive window
// permanently (measured: the blocked writes never resumed once the drain
// began), which would make the drain half of this test flaky for reasons
// that have nothing to do with ws.Conn.
func TestConnWriteTimeoutZeroNeverTrips(t *testing.T) {
	const frames = 50
	chunk := make([]byte, 64*1024)
	stalled := make(chan struct{})
	done := make(chan error, 1)

	srvPipe, cliPipe := net.Pipe()
	defer func() { _ = cliPipe.Close() }()
	s := &Conn{Conn: srvPipe, rw: bufio.NewReadWriter(bufio.NewReader(srvPipe), bufio.NewWriter(srvPipe))}

	go func() {
		// NOTE: no SetWriteTimeout call anywhere — this is the default
		// zero path. The first flush blocks on the synchronous pipe (the
		// peer is deaf) and the flood must stay blocked — never error —
		// until this test drains it.
		close(stalled)
		var werr error
		for i := 0; i < frames && werr == nil; i++ {
			werr = s.WriteBinary(chunk)
		}
		done <- werr
	}()

	<-stalled
	select {
	case err := <-done:
		t.Fatalf("the %d-frame flood completed with %v while the peer was deaf and no timeout was armed: the zero default must leave writes unbounded, exactly pre-#114", frames, err)
	case <-time.After(500 * time.Millisecond):
		// Still mid-flood after 500 ms — correct: zero means NO deadline.
	}

	// Drain every byte: the synchronous pipe advances the flood one read
	// at a time, so the flood completes only now, with nil — the one
	// unblock any armed deadline would have replaced with an error.
	go func() { _, _ = io.Copy(io.Discard, cliPipe) }()
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("write failed after the peer drained: %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("write flood never completed after the peer drained")
	}
	_ = srvPipe.Close()
}

// TestConnWriteTimeoutIsPerWrite pins the refresh semantics: the deadline
// re-arms at now+d on EVERY writeFrame, so a healthy-but-slow connection —
// frames flowing one every d/2, each fitting the buffers — never trips it,
// even though the traffic SPAN far exceeds d. This is why the production
// bound (15 s > the 10 s heartbeat) is invisible to healthy traffic rather
// than a slow-death timer.
func TestConnWriteTimeoutIsPerWrite(t *testing.T) {
	const timeout = 200 * time.Millisecond
	const frames = 5 // ~500 ms total span, > 2 × timeout

	err, at := writeTimeoutStall(t, func(s *Conn) error {
		s.SetWriteTimeout(timeout)
		for i := 0; i < frames; i++ {
			// One-byte frames: every write completes straight through the
			// buffers (the deaf peer never matters at this size), so only
			// the inter-write sleeps make the SPAN exceed the deadline.
			if err := s.WriteBinary([]byte{byte(i)}); err != nil {
				return err
			}
			time.Sleep(timeout / 2)
		}
		return nil
	})

	if err != nil {
		t.Fatalf("healthy slow-traffic writes failed with %v after %v: the deadline must re-arm per write, so the SPAN of successful writes must never trip it", err, at)
	}
	if at < 2*timeout {
		t.Fatalf("loop finished in %v, want >= %v: the test must actually span more than one armed deadline to prove the per-write refresh", at, 2*timeout)
	}
}

// deadlineSpy is a net.Conn that RECORDS the write deadlines writeFrame asks
// for and accepts every write. It exists because the question
// TestConnWriteTimeoutDisarmClearsDeadline asks is not "how long did the
// stall last" (what the stalled-peer tests above measure) but "was a deadline
// set at all" — and a recorder answers that deterministically, with no
// timing bet, no kernel buffer and no peer to keep deaf.
//
// It separates ARMING (a non-zero instant) from CLEARING (the zero instant),
// because the two are different promises: a disarmed Conn must stop arming
// new bounds while still being allowed — required, see SetWriteTimeout — to
// drop the stale one an earlier armed write left on the socket.
type deadlineSpy struct {
	mu       sync.Mutex
	arms     int
	clears   int
	deadline time.Time
	writes   int
}

func (s *deadlineSpy) SetWriteDeadline(t time.Time) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if t.IsZero() {
		s.clears++
	} else {
		s.arms++
	}
	s.deadline = t
	return nil
}

// snapshot reads the counters under the same lock, so a test can pin "no new
// deadline was ARMED across these writes" without racing anything.
func (s *deadlineSpy) snapshot() (arms, clears, writes int, deadline time.Time) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.arms, s.clears, s.writes, s.deadline
}

func (s *deadlineSpy) Read(p []byte) (int, error) { return 0, io.EOF }

func (s *deadlineSpy) Write(p []byte) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.writes++
	return len(p), nil
}

func (s *deadlineSpy) Close() error                    { return nil }
func (s *deadlineSpy) LocalAddr() net.Addr             { return &net.TCPAddr{} }
func (s *deadlineSpy) RemoteAddr() net.Addr            { return &net.TCPAddr{} }
func (s *deadlineSpy) SetDeadline(time.Time) error     { return nil }
func (s *deadlineSpy) SetReadDeadline(time.Time) error { return nil }

// TestConnWriteTimeoutDisarmClearsDeadline pins the other half of the
// SetWriteTimeout contract (issue #114 review, test supplement): d <= 0
// DISARMS. After SetWriteTimeout(0) — and after a negative d, which
// writeFrame's caller-visible surface normalises to the same zero — no
// further write ARMS a deadline, so the connection is back to the pre-#114
// no-deadline behaviour and any explicit deadline a caller sets on the raw
// Conn is fully in charge. That is exactly the ordering app.go's overflow
// teardown depends on: disarm first, then set its own documented 5 s bound,
// which a lingering 15 s per-write re-arm would otherwise silently loosen.
//
// What disarming ALSO does, and what this test therefore counts: it drops
// the ABSOLUTE deadline the last armed writeFrame stamped on the underlying
// socket, via SetWriteDeadline(time.Time{}) (issue #114 LOW-1). Zeroing the
// field alone left that instant live, so any write issued after the original
// now+15 s failed with os.ErrDeadlineExceeded under a timeout that had
// already been disarmed. The recorder therefore expects exactly ONE clear
// per disarm call and never a new arm, and the deadline it last saw is the
// zero instant; TestConnSetWriteTimeoutDisarmClearsSocketDeadline is the
// wire-level proof that the clear is not cosmetic.
func TestConnWriteTimeoutDisarmClearsDeadline(t *testing.T) {
	spy := &deadlineSpy{}
	c := &Conn{Conn: spy, rw: bufio.NewReadWriter(bufio.NewReader(spy), bufio.NewWriter(spy))}
	const armed = 2 * time.Second

	// Phase 1, armed: one write, one deadline, armed at ~now+armed. This is
	// the phase that makes the later "no new arm" assertions non-vacuous —
	// the recorder demonstrably does record when a deadline IS armed.
	c.SetWriteTimeout(armed)
	if err := c.WriteBinary([]byte("armed")); err != nil {
		t.Fatalf("armed write: %v", err)
	}
	arms, clears, writes, dl := spy.snapshot()
	if arms != 1 || clears != 0 || writes != 1 {
		t.Fatalf("armed phase: deadline arms=%d clears=%d writes=%d, want 1, 0 and 1 — the spy must observe the armed write (and no clear) or the disarm assertions below prove nothing", arms, clears, writes)
	}
	if left := time.Until(dl); left > armed || left < armed/2 {
		t.Fatalf("armed deadline = now+%v, want ~%v (the armed duration, refreshed per write)", left, armed)
	}

	// Phase 2, disarmed with the documented zero: the disarm clears the
	// socket once, and NONE of the writes after it arms anything — while
	// every write keeps landing on the socket.
	c.SetWriteTimeout(0)
	for i := 0; i < 3; i++ {
		if err := c.WriteBinary([]byte("zero")); err != nil {
			t.Fatalf("write %d after SetWriteTimeout(0): %v", i, err)
		}
	}
	if gotArms, gotClears, gotWrites, gotDL := spy.snapshot(); gotArms != arms || gotClears != clears+1 || gotWrites != 4 || !gotDL.IsZero() {
		t.Fatalf("after SetWriteTimeout(0): deadline arms=%d clears=%d writes=%d lastDeadline=%v, want %d, %d, 4 and the zero instant — a disarmed Conn must clear the stale deadline exactly once, stop arming new ones, and still write", gotArms, gotClears, gotWrites, gotDL, arms, clears+1)
	}

	// Phase 3, the d < 0 normalisation: a negative duration is the same
	// disarm, not a negative deadline (which SetWriteDeadline would take as
	// a deadline already in the past and turn every write into an instant
	// timeout — the failure this branch exists to prevent).
	c.SetWriteTimeout(-time.Second)
	if err := c.WriteBinary([]byte("negative")); err != nil {
		t.Fatalf("write after SetWriteTimeout(-1s): %v", err)
	}
	if gotArms, gotClears, gotWrites, gotDL := spy.snapshot(); gotArms != arms || gotClears != clears+2 || gotWrites != 5 || !gotDL.IsZero() {
		t.Fatalf("after SetWriteTimeout(-1s): deadline arms=%d clears=%d writes=%d lastDeadline=%v, want %d, %d, 5 and the zero instant — a negative d must normalise to the zero disarm, clear exactly once more, and never arm anything", gotArms, gotClears, gotWrites, gotDL, arms, clears+2)
	}
	// The wire-level observation above cannot separate 0 from -1s on its own
	// (writeFrame's guard is `> 0`, so both disarm identically: one clear,
	// no arm), so the normalisation itself is read off the field — in-package,
	// under the same mutex writeFrame holds across a write, so the read is
	// race-clean. It says what the branch did: a negative d never becomes a
	// deadline value anywhere, so no consumer of that field has to know one
	// was ever offered.
	c.mu.Lock()
	stored := c.writeTimeout
	c.mu.Unlock()
	if stored != 0 {
		t.Fatalf("stored writeTimeout = %v after SetWriteTimeout(-1s), want 0 — the d < 0 normalisation is what keeps a negative duration from surviving as a deadline", stored)
	}
}

// TestConnSetWriteTimeoutDisarmClearsSocketDeadline pins the half of the
// SetWriteTimeout contract the recorder test above structurally cannot see:
// an armed deadline is an ABSOLUTE instant (now+d) stamped on the underlying
// socket by writeFrame, so zeroing the field alone leaves that instant live.
// arm(50ms) → write → SetWriteTimeout(0) must leave the socket with NO write
// deadline at all; if the expired now+50ms survived the disarm, every later
// write on this socket — ws-level or raw — fails instantly with
// os.ErrDeadlineExceeded, enforcing a bound no caller asked for any more.
//
// Why net.Pipe: it is synchronous, so one frame to a peer that never reads
// blocks inside pipe.write and trips the armed deadline with no kernel-buffer
// luck involved, and pipeDeadline.set with a zero t re-opens the deadline
// channel (GOROOT net/pipe.go) — i.e. SetWriteDeadline(time.Time{}) is a
// real, observable clear on this transport, not a no-op. Every interval here
// is far under a second.
//
// Why the post-disarm probes are written the way they are: bufio.Writer
// caches its first write error and returns it from every later Write/Flush
// (GOROOT: b.err), so the Conn that tripped in phase 1 can never produce a
// fresh socket observation — the stale buffered error, not the deadline,
// would answer the question. Production never reuses a Conn past a tripped
// write (app.go tears it down), so nothing here is pretending otherwise:
// the raw-socket probe carries no wrapper state whatsoever, and the ws-level
// probe runs on the same Conn only after its poisoned buffer is Reset, which
// leaves the socket deadline as the single variable under test.
func TestConnSetWriteTimeoutDisarmClearsSocketDeadline(t *testing.T) {
	const timeout = 50 * time.Millisecond
	chunk := make([]byte, 32*1024)

	srvPipe, cliPipe := net.Pipe()
	defer func() { _ = cliPipe.Close() }()
	defer func() { _ = srvPipe.Close() }()
	s := &Conn{Conn: srvPipe, rw: bufio.NewReadWriter(bufio.NewReader(srvPipe), bufio.NewWriter(srvPipe))}

	// Phase 1, armed against a deaf peer: the write trips the armed deadline,
	// which proves the socket really carries now+timeout.
	s.SetWriteTimeout(timeout)
	if err := s.WriteBinary(chunk); err == nil {
		t.Fatal("write to a peer that never read returned nil: the armed socket deadline never fired, so nothing below would prove anything")
	} else if !errors.Is(err, os.ErrDeadlineExceeded) {
		t.Fatalf("armed write error = %v, want os.ErrDeadlineExceeded", err)
	}

	// Phase 2, disarm. The deadline armed in phase 1 is now comfortably in
	// the past, so it is the exact thing that must be gone.
	s.SetWriteTimeout(0)

	// Drain the peer, but only until the last probe byte lands: the drain
	// stops there so the re-arm phase below has a deaf peer again without
	// any goroutine-ordering guesswork. On a synchronous pipe a write only
	// completes when someone reads, so "drained" is a real handshake here,
	// not a hope — and the draining itself is not what is under test.
	drained := make(chan struct{})
	go func() {
		buf := make([]byte, 4096)
		var seen []byte
		for {
			n, err := cliPipe.Read(buf)
			seen = append(seen, buf[:n]...)
			if err != nil || bytes.Contains(seen, []byte("still disarmed")) {
				close(drained)
				return
			}
		}
	}()

	// Probe A, raw socket, zero wrapper state: the socket that phase 1
	// stamped with now+50ms must accept a write now that the timeout is
	// disarmed. Falsifiable against the previous implementation, which
	// zeroed only the field — pipe.write tests the deadline channel on
	// entry and would fail this instantly with a deadline nobody armed.
	raw := make(chan error, 1)
	go func() { _, err := srvPipe.Write([]byte("post-disarm")); raw <- err }()
	select {
	case err := <-raw:
		if err != nil {
			t.Fatalf("raw socket write after SetWriteTimeout(0) failed with %v: the disarm left the expired absolute deadline armed on the socket, so every later write inherits a bound nobody asked for", err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("raw socket write after SetWriteTimeout(0) never completed while the peer drains: a disarm must leave no deadline, not an unblockable stall")
	}

	// Probe B, ws level: the same Conn writes a fresh frame once its poisoned
	// buffer is reset (see the doc comment). Still disarmed, so writeFrame
	// touches no deadline — the socket either carries phase 1's expired
	// instant or it does not.
	s.rw.Writer.Reset(srvPipe)
	framed := make(chan error, 1)
	go func() { framed <- s.WriteBinary([]byte("still disarmed")) }()
	select {
	case err := <-framed:
		if err != nil {
			t.Fatalf("ws write after SetWriteTimeout(0) failed with %v, want nil", err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("ws write after SetWriteTimeout(0) never completed while the peer drains")
	}

	// Both probes are on the wire and the drain has stopped, so the peer is
	// deaf again. Re-arming after a disarm must still bound the write: the
	// clear must not have left arming inert.
	select {
	case <-drained:
	case <-time.After(3 * time.Second):
		t.Fatal("the peer never drained the two post-disarm probes: the probes above proved nothing about the socket")
	}
	s.SetWriteTimeout(timeout)
	if err := s.WriteBinary(chunk); err == nil {
		t.Fatal("re-armed write to a peer that stopped draining returned nil: re-arming after a disarm must still bound the write")
	} else if !errors.Is(err, os.ErrDeadlineExceeded) {
		t.Fatalf("re-armed write error = %v, want os.ErrDeadlineExceeded", err)
	}
}
