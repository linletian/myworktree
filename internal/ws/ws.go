package ws

import (
	"bufio"
	"context"
	"crypto/rand"
	"crypto/sha1"
	"encoding/base64"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"strings"
	"sync"
	"time"
)

const (
	opText   = 0x1
	opBinary = 0x2
	opClose  = 0x8
	opPing   = 0x9
	opPong   = 0xA
)

type Conn struct {
	net.Conn
	rw     *bufio.ReadWriter
	mu     sync.Mutex
	client bool
	// writeTimeout bounds every writeFrame (issue #114, the #83 write
	// deadline the tty path had never implemented). ZERO — the value every
	// Conn is constructed with, and the value every endpoint that never
	// calls SetWriteTimeout keeps — means NO deadline, today's exact
	// behaviour: a stalled peer stalls the write indefinitely. It is
	// strictly opt-in, per Conn, so enabling it on the tty path cannot
	// reach the other WS endpoints or the client-side Dial path.
	//
	// read under c.mu (writeFrame holds it); written by SetWriteTimeout,
	// which also takes c.mu so the two cannot interleave with a write.
	writeTimeout time.Duration
	// Client-side continuation reassembly state (RFC 6455 §5.4): a
	// fin=0 data frame opens a message whose payload accumulates in
	// fragBuf until the fin=1 continuation; fragOp preserves the FIRST
	// frame's opcode for the assembled message. Server conns (the tty
	// path) never fragment and keep the strict rejection instead.
	fragOp  byte
	fragBuf []byte
}

// SetWriteTimeout arms a per-write deadline on every subsequent writeFrame
// (issue #114). d <= 0 clears it back to the default no-deadline behaviour:
// the disarm takes the socket with the field — SetWriteDeadline(time.Time{})
// on the embedded Conn — so the absolute deadline an earlier armed writeFrame
// left on the socket cannot outlive the timeout that armed it. Without that
// clear, arm(15s) → write → SetWriteTimeout(0) would still fail any write
// issued after the original now+15s with os.ErrDeadlineExceeded, a deadline
// no caller asked for any more, contradicting the sentence above.
// It takes c.mu — the same lock writeFrame holds across a write — so the
// field is never read mid-write by a racing writeFrame while being written
// here, and a caller cannot arm a deadline onto a write already in flight.
// The same lock is what makes the clear safe: no write is in flight here, so
// a zero deadline cannot land mid-frame.
//
// Opt-in by design: Upgrade/Dial never call it, so a Conn that does not ask
// for a deadline behaves byte-identically to before this method existed.
func (c *Conn) SetWriteTimeout(d time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if d <= 0 {
		c.writeTimeout = 0
		// The error is ignored on purpose: a socket that refuses to drop a
		// deadline is not actionable here. The next arm re-sets it through
		// writeFrame's own checked path, and on the zero path the only
		// thing a caller can depend on is that ws.Conn stops arming new
		// per-write deadlines — which holds regardless of whether this
		// particular clear landed.
		_ = c.Conn.SetWriteDeadline(time.Time{})
		return
	}
	c.writeTimeout = d
}

func Upgrade(w http.ResponseWriter, r *http.Request) (*Conn, error) {
	if !headerContainsToken(r.Header.Get("Connection"), "upgrade") ||
		!strings.EqualFold(strings.TrimSpace(r.Header.Get("Upgrade")), "websocket") {
		return nil, errors.New("not a websocket upgrade request")
	}
	if strings.TrimSpace(r.Header.Get("Sec-WebSocket-Version")) != "13" {
		return nil, errors.New("unsupported websocket version")
	}
	key := strings.TrimSpace(r.Header.Get("Sec-WebSocket-Key"))
	if key == "" {
		return nil, errors.New("missing websocket key")
	}

	hj, ok := w.(http.Hijacker)
	if !ok {
		return nil, errors.New("hijack unsupported")
	}
	conn, rw, err := hj.Hijack()
	if err != nil {
		return nil, err
	}
	accept := wsAccept(key)
	resp := "HTTP/1.1 101 Switching Protocols\r\n" +
		"Upgrade: websocket\r\n" +
		"Connection: Upgrade\r\n" +
		"Sec-WebSocket-Accept: " + accept + "\r\n\r\n"
	if _, err := rw.WriteString(resp); err != nil {
		_ = conn.Close()
		return nil, err
	}
	if err := rw.Flush(); err != nil {
		_ = conn.Close()
		return nil, err
	}
	return &Conn{Conn: conn, rw: rw}, nil
}

// ErrUpgradeRejected is the Unwrap target of *DialError, so callers can match
// a rejected upgrade with errors.Is and extract the status with errors.As.
var ErrUpgradeRejected = errors.New("websocket upgrade rejected")

// DialError reports a failed Dial handshake. Status carries the HTTP
// status code so callers can distinguish e.g. a 401 auth-gate
// rejection. Detail is set when the failure is not the status itself
// (a 101 reply with a bogus Sec-WebSocket-Accept).
type DialError struct {
	Status int
	Detail string
}

func (e *DialError) Error() string {
	if e.Detail != "" {
		return fmt.Sprintf("websocket dial: %s (status %d)", e.Detail, e.Status)
	}
	return fmt.Sprintf("websocket dial: unexpected status %d %s", e.Status, http.StatusText(e.Status))
}

func (e *DialError) Unwrap() error { return ErrUpgradeRejected }

// Dial opens a client WebSocket connection to addr (host:port) and performs
// the RFC 6455 opening handshake for path, sending any extra hdr lines (e.g.
// Cookie). A ctx deadline also bounds the handshake reply wait.
func Dial(ctx context.Context, addr, path string, hdr http.Header) (*Conn, error) {
	var d net.Dialer
	conn, err := d.DialContext(ctx, "tcp", addr)
	if err != nil {
		return nil, err
	}
	if dl, ok := ctx.Deadline(); ok {
		_ = conn.SetDeadline(dl)
	}
	fail := func(err error) (*Conn, error) {
		_ = conn.Close()
		return nil, err
	}

	key := make([]byte, 16)
	if _, err := rand.Read(key); err != nil {
		return fail(err)
	}
	keyB64 := base64.StdEncoding.EncodeToString(key)
	var sb strings.Builder
	sb.WriteString("GET " + path + " HTTP/1.1\r\n")
	sb.WriteString("Host: " + addr + "\r\n")
	sb.WriteString("Upgrade: websocket\r\n")
	sb.WriteString("Connection: Upgrade\r\n")
	sb.WriteString("Sec-WebSocket-Key: " + keyB64 + "\r\n")
	sb.WriteString("Sec-WebSocket-Version: 13\r\n")
	for k, vs := range hdr {
		for _, v := range vs {
			sb.WriteString(k + ": " + v + "\r\n")
		}
	}
	sb.WriteString("\r\n")

	rw := bufio.NewReadWriter(bufio.NewReader(conn), bufio.NewWriter(conn))
	if _, err := rw.WriteString(sb.String()); err != nil {
		return fail(err)
	}
	if err := rw.Flush(); err != nil {
		return fail(err)
	}
	resp, err := http.ReadResponse(rw.Reader, &http.Request{Method: "GET"})
	if err != nil {
		return fail(err)
	}
	if resp.StatusCode != http.StatusSwitchingProtocols {
		return fail(&DialError{Status: resp.StatusCode})
	}
	// RFC 6455 §4.2.2: the accept must be the keyed hash of OUR
	// challenge — a 101 without it is not a WebSocket endpoint (a
	// misbehaving intermediary can produce exactly that).
	if got, want := resp.Header.Get("Sec-WebSocket-Accept"), wsAccept(keyB64); got != want {
		return fail(&DialError{Status: resp.StatusCode, Detail: "bad Sec-WebSocket-Accept"})
	}
	_ = conn.SetDeadline(time.Time{})
	return &Conn{Conn: conn, rw: rw, client: true}, nil
}

const maxMessageBytes = 10 * 1024 * 1024

func (c *Conn) ReadMessage() (opcode byte, payload []byte, err error) {
	for {
		fin, op, pl, err := c.readFrame()
		if err != nil {
			return 0, nil, err
		}
		if !c.client {
			// Server conns (the tty path) never fragment: keep the
			// strict rejection.
			if !fin {
				return 0, nil, errors.New("fragmented websocket frames are not supported")
			}
			return op, pl, nil
		}
		// Control frames (ping/pong/close) may interleave inside a
		// fragmented message (RFC 6455 §5.4) and are never fragmented
		// themselves: return each as its own message immediately.
		if op == opClose || op == opPing || op == opPong {
			return op, pl, nil
		}
		if op == 0x0 { // continuation
			if c.fragOp == 0 {
				return 0, nil, errors.New("websocket continuation frame without an open message")
			}
			c.fragBuf = append(c.fragBuf, pl...)
			if len(c.fragBuf) > maxMessageBytes {
				return 0, nil, errors.New("websocket message too large")
			}
			if !fin {
				continue
			}
			op, out := c.fragOp, c.fragBuf
			c.fragOp, c.fragBuf = 0, nil
			return op, out, nil
		}
		if c.fragOp != 0 {
			return 0, nil, errors.New("websocket data frame before the final continuation")
		}
		if fin {
			return op, pl, nil
		}
		c.fragOp = op
		c.fragBuf = append(c.fragBuf, pl...)
	}
}

func (c *Conn) readFrame() (fin bool, opcode byte, payload []byte, err error) {
	h := make([]byte, 2)
	if _, err = io.ReadFull(c.rw, h); err != nil {
		return false, 0, nil, err
	}
	fin = (h[0] & 0x80) != 0
	opcode = h[0] & 0x0F
	masked := (h[1] & 0x80) != 0
	length := int64(h[1] & 0x7F)
	if length == 126 {
		ext := make([]byte, 2)
		if _, err = io.ReadFull(c.rw, ext); err != nil {
			return false, 0, nil, err
		}
		length = int64(binary.BigEndian.Uint16(ext))
	} else if length == 127 {
		ext := make([]byte, 8)
		if _, err = io.ReadFull(c.rw, ext); err != nil {
			return false, 0, nil, err
		}
		u := binary.BigEndian.Uint64(ext)
		if u > maxMessageBytes {
			return false, 0, nil, errors.New("websocket frame too large")
		}
		length = int64(u)
	}
	var mask []byte
	if masked {
		mask = make([]byte, 4)
		if _, err = io.ReadFull(c.rw, mask); err != nil {
			return false, 0, nil, err
		}
	} else if !c.client {
		return false, 0, nil, errors.New("client websocket frame must be masked")
	}
	payload = make([]byte, length)
	if _, err = io.ReadFull(c.rw, payload); err != nil {
		return false, 0, nil, err
	}
	if masked {
		for i := range payload {
			payload[i] ^= mask[i%4]
		}
	}
	return fin, opcode, payload, nil
}

func (c *Conn) WriteText(p []byte) error   { return c.writeFrame(opText, p) }
func (c *Conn) WriteBinary(p []byte) error { return c.writeFrame(opBinary, p) }

// WritePing emits an RFC 6455 ping control frame (empty payload allowed).
// Browsers auto-reply with a Pong carrying the same payload — from their
// network stack, NOT from page JavaScript: onmessage never fires for
// control frames. That is what makes a server-initiated ping usable as a
// liveness probe (issue #83): the peer refreshes the server's read
// deadline with zero cooperation from the app layer.
func (c *Conn) WritePing(p []byte) error  { return c.writeFrame(opPing, p) }
func (c *Conn) WritePong(p []byte) error  { return c.writeFrame(opPong, p) }
func (c *Conn) WriteClose(p []byte) error { return c.writeFrame(opClose, p) }

func (c *Conn) writeFrame(op byte, p []byte) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	// Issue #114 (decision 3): a per-Conn write deadline, armed via
	// SetWriteTimeout, refreshed at the start of EVERY write so it bounds
	// each frame individually — a healthy connection keeps re-arming it
	// (every heartbeat ping, every chunk) and never trips it; a stalled
	// peer turns the indefinitely-blocked write that used to pin this
	// goroutine, c.mu, and every later frame behind it into a bounded
	// stall that errors and unwinds the caller. The zero path is a
	// compare-and-branch: writeTimeout == 0 (every Conn that never called
	// SetWriteTimeout — every other WS endpoint, every dialed client)
	// touches no deadline and behaves exactly as before, including leaving
	// any deadline a caller set on the raw Conn (e.g. app.go's explicit
	// 5 s teardown deadline) fully in charge.
	if c.writeTimeout > 0 {
		if err := c.SetWriteDeadline(time.Now().Add(c.writeTimeout)); err != nil {
			return err
		}
	}
	h := []byte{0x80 | op}
	n := len(p)
	var maskBit byte
	if c.client {
		maskBit = 0x80
	}
	switch {
	case n <= 125:
		h = append(h, maskBit|byte(n))
	case n <= 65535:
		h = append(h, maskBit|126, byte(n>>8), byte(n))
	default:
		h = append(h, maskBit|127, 0, 0, 0, 0, byte(n>>24), byte(n>>16), byte(n>>8), byte(n))
	}
	if _, err := c.rw.Write(h); err != nil {
		return err
	}
	if !c.client {
		if _, err := c.rw.Write(p); err != nil {
			return err
		}
		return c.rw.Flush()
	}
	mask := make([]byte, 4)
	if _, err := rand.Read(mask); err != nil {
		return err
	}
	if _, err := c.rw.Write(mask); err != nil {
		return err
	}
	mp := make([]byte, n)
	for i, b := range p {
		mp[i] = b ^ mask[i%4]
	}
	if _, err := c.rw.Write(mp); err != nil {
		return err
	}
	return c.rw.Flush()
}

func wsAccept(key string) string {
	const magic = "258EAFA5-E914-47DA-95CA-C5AB0DC85B11"
	sum := sha1.Sum([]byte(key + magic))
	return base64.StdEncoding.EncodeToString(sum[:])
}

func headerContainsToken(v, token string) bool {
	for _, p := range strings.Split(v, ",") {
		if strings.EqualFold(strings.TrimSpace(p), token) {
			return true
		}
	}
	return false
}

func IsDataOpcode(op byte) bool { return op == opText || op == opBinary }
func IsPing(op byte) bool       { return op == opPing }
func IsClose(op byte) bool      { return op == opClose }

func CloseMessage(code uint16, reason string) []byte {
	b := make([]byte, 2+len(reason))
	binary.BigEndian.PutUint16(b[:2], code)
	copy(b[2:], []byte(reason))
	return b
}

func (c *Conn) String() string { return fmt.Sprintf("ws(%s)", c.RemoteAddr()) }
