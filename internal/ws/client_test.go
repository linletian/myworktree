package ws

import (
	"bufio"
	"context"
	"io"
	"net"
	"net/http"
	"testing"
	"time"
)

// hijackRW is a minimal http.ResponseWriter + http.Hijacker over a raw TCP
// conn, so Upgrade can run in tests without a full net/http server.
type hijackRW struct {
	conn net.Conn
	br   *bufio.Reader
	hdr  http.Header
}

func (h *hijackRW) Header() http.Header         { return h.hdr }
func (h *hijackRW) Write(p []byte) (int, error) { return h.conn.Write(p) }
func (h *hijackRW) WriteHeader(int)             {}
func (h *hijackRW) Hijack() (net.Conn, *bufio.ReadWriter, error) {
	return h.conn, bufio.NewReadWriter(h.br, bufio.NewWriter(h.conn)), nil
}

// serveUpgrade accepts one raw conn, parses the handshake request, and runs
// the real Upgrade server path, then hands the upgraded conn to loop.
func serveUpgrade(t *testing.T, ln net.Listener, loop func(*Conn, *http.Request)) {
	t.Helper()
	conn, err := ln.Accept()
	if err != nil {
		t.Errorf("accept: %v", err)
		return
	}
	defer func() { _ = conn.Close() }()
	br := bufio.NewReader(conn)
	req, err := http.ReadRequest(br)
	if err != nil {
		t.Errorf("read request: %v", err)
		return
	}
	c, err := Upgrade(&hijackRW{conn: conn, br: br, hdr: make(http.Header)}, req)
	if err != nil {
		t.Errorf("upgrade: %v", err)
		return
	}
	loop(c, req)
}

func waitDone(t *testing.T, done chan struct{}) {
	t.Helper()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("server loop did not finish in time")
	}
}

func TestDialRoundtrip(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = ln.Close() }()

	done := make(chan struct{})
	go serveUpgrade(t, ln, func(s *Conn, req *http.Request) {
		defer close(done)
		if got := req.Header.Get("Cookie"); got != "session=abc123" {
			t.Errorf("cookie header = %q, want session=abc123", got)
		}
		op, p, err := s.ReadMessage()
		if err != nil {
			t.Errorf("server read: %v", err)
			return
		}
		if op != opText || string(p) != "hello" {
			t.Errorf("server got op=%d payload=%q, want text hello", op, p)
			return
		}
		if err := s.WriteText([]byte("world")); err != nil {
			t.Errorf("server write: %v", err)
			return
		}
		op, p, err = s.ReadMessage()
		if err != nil {
			t.Errorf("server read ping: %v", err)
			return
		}
		if op != opPing || string(p) != "pingdata" {
			t.Errorf("server got op=%d payload=%q, want ping pingdata", op, p)
			return
		}
		if err := s.WritePong(p); err != nil {
			t.Errorf("server pong: %v", err)
			return
		}
		op, _, err = s.ReadMessage()
		if err != nil {
			t.Errorf("server read close: %v", err)
			return
		}
		if op != opClose {
			t.Errorf("server got op=%d, want close", op)
		}
	})

	hdr := http.Header{}
	hdr.Set("Cookie", "session=abc123")
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	c, err := Dial(ctx, ln.Addr().String(), "/ws", hdr)
	if err != nil {
		t.Fatalf("Dial: %v", err)
	}

	if err := c.WriteText([]byte("hello")); err != nil {
		t.Fatalf("client write: %v", err)
	}
	op, p, err := c.ReadMessage()
	if err != nil {
		t.Fatalf("client read: %v", err)
	}
	if op != opText || string(p) != "world" {
		t.Fatalf("client got op=%d payload=%q, want text world", op, p)
	}
	if err := c.writeFrame(opPing, []byte("pingdata")); err != nil {
		t.Fatalf("client ping: %v", err)
	}
	op, p, err = c.ReadMessage()
	if err != nil {
		t.Fatalf("client read pong: %v", err)
	}
	if op != opPong || string(p) != "pingdata" {
		t.Fatalf("client got op=%d payload=%q, want pong pingdata", op, p)
	}
	if err := c.WriteClose(CloseMessage(1000, "bye")); err != nil {
		t.Fatalf("client close: %v", err)
	}
	_ = c.Close()
	waitDone(t, done)
}

// TestWritePingRoundtrip pins the new server-initiated WritePing (issue
// #83): the frame must hit the wire as an RFC 6455 PING (opcode 0x9) —
// which ReadMessage surfaces as its own message rather than swallowing —
// and a peer's Pong must arrive back as opcode 0xA with the echoed
// payload. The empty ping is load-bearing: the tty heartbeat writes
// WritePing(nil), and a header-only frame is the liveness traffic on
// that path. Real browsers answer the ping/pong legs automatically from
// their network stack; here the client answers by hand, like the ping
// leg of TestDialRoundtrip does.
func TestWritePingRoundtrip(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = ln.Close() }()

	done := make(chan struct{})
	go serveUpgrade(t, ln, func(s *Conn, req *http.Request) {
		defer close(done)
		// Empty ping first — the exact frame the tty heartbeat writes.
		if err := s.WritePing(nil); err != nil {
			t.Errorf("server ping(nil): %v", err)
			return
		}
		op, p, err := s.ReadMessage()
		if err != nil {
			t.Errorf("server read pong: %v", err)
			return
		}
		if op != opPong || len(p) != 0 {
			t.Errorf("server got op=%d payload=%q, want pong with empty payload", op, p)
			return
		}
		// Payload-carrying ping: the Pong must echo it back verbatim.
		if err := s.WritePing([]byte("liveness")); err != nil {
			t.Errorf("server ping: %v", err)
			return
		}
		op, p, err = s.ReadMessage()
		if err != nil {
			t.Errorf("server read pong 2: %v", err)
			return
		}
		if op != opPong || string(p) != "liveness" {
			t.Errorf("server got op=%d payload=%q, want pong liveness", op, p)
		}
	})

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	c, err := Dial(ctx, ln.Addr().String(), "/ws", nil)
	if err != nil {
		t.Fatalf("Dial: %v", err)
	}

	op, p, err := c.ReadMessage()
	if err != nil {
		t.Fatalf("client read ping: %v", err)
	}
	if op != opPing || len(p) != 0 {
		t.Fatalf("client got op=%d payload=%q, want ping (0x9) with empty payload", op, p)
	}
	if err := c.WritePong(p); err != nil {
		t.Fatalf("client pong: %v", err)
	}
	op, p, err = c.ReadMessage()
	if err != nil {
		t.Fatalf("client read ping 2: %v", err)
	}
	if op != opPing || string(p) != "liveness" {
		t.Fatalf("client got op=%d payload=%q, want ping liveness", op, p)
	}
	if err := c.WritePong(p); err != nil {
		t.Fatalf("client pong 2: %v", err)
	}
	_ = c.Close()
	waitDone(t, done)
}

func TestDialSendsMaskedFrames(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = ln.Close() }()

	done := make(chan struct{})
	go serveUpgrade(t, ln, func(s *Conn, req *http.Request) {
		defer close(done)
		h := make([]byte, 2)
		if _, err := io.ReadFull(s.rw, h); err != nil {
			t.Errorf("read frame header: %v", err)
			return
		}
		if h[0] != 0x80|opText {
			t.Errorf("first byte = %#x, want FIN|text", h[0])
		}
		if h[1]&0x80 == 0 {
			t.Errorf("client frame not masked on the wire: second byte %#x", h[1])
		}
		n := int(h[1] & 0x7F)
		mask := make([]byte, 4)
		if _, err := io.ReadFull(s.rw, mask); err != nil {
			t.Errorf("read mask: %v", err)
			return
		}
		payload := make([]byte, n)
		if _, err := io.ReadFull(s.rw, payload); err != nil {
			t.Errorf("read payload: %v", err)
			return
		}
		for i := range payload {
			payload[i] ^= mask[i%4]
		}
		if string(payload) != "masked-hello" {
			t.Errorf("unmasked payload = %q, want masked-hello", payload)
		}
	})

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	c, err := Dial(ctx, ln.Addr().String(), "/ws", nil)
	if err != nil {
		t.Fatalf("Dial: %v", err)
	}
	if err := c.WriteText([]byte("masked-hello")); err != nil {
		t.Fatalf("client write: %v", err)
	}
	waitDone(t, done)
	_ = c.Close()
}
