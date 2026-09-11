package ws

import (
	"bufio"
	"context"
	"errors"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestClientAcceptsUnmaskedServerFramesRaw(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = ln.Close() }()

	done := make(chan struct{})
	go serveUpgrade(t, ln, func(s *Conn, req *http.Request) {
		defer close(done)
		// Two unmasked text frames written back-to-back as raw bytes.
		raw := []byte{0x81, 0x02, 'h', 'i', 0x81, 0x03, 'b', 'y', 'e'}
		if _, err := s.Conn.Write(raw); err != nil {
			t.Errorf("raw write: %v", err)
			return
		}
		// Block until the client closes so the conn stays open while the
		// client reads both frames.
		if _, _, err := s.ReadMessage(); err != nil {
			t.Errorf("server read close: %v", err)
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
		t.Fatalf("client read frame 1: %v", err)
	}
	if op != opText || string(p) != "hi" {
		t.Fatalf("frame 1: op=%d payload=%q, want text hi", op, p)
	}
	op, p, err = c.ReadMessage()
	if err != nil {
		t.Fatalf("client read frame 2: %v", err)
	}
	if op != opText || string(p) != "bye" {
		t.Fatalf("frame 2: op=%d payload=%q, want text bye (stream misaligned)", op, p)
	}
	if err := c.WriteClose(CloseMessage(1000, "bye")); err != nil {
		t.Fatalf("client close: %v", err)
	}
	_ = c.Close()
	waitDone(t, done)
}

func TestClientToleratesMaskedServerFrames(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = ln.Close() }()

	done := make(chan struct{})
	go serveUpgrade(t, ln, func(s *Conn, req *http.Request) {
		defer close(done)
		mask := []byte{0x12, 0x34, 0x56, 0x78}
		msg := []byte("ok")
		raw := []byte{0x81, 0x80 | byte(len(msg))}
		raw = append(raw, mask...)
		for i, b := range msg {
			raw = append(raw, b^mask[i%4])
		}
		if _, err := s.Conn.Write(raw); err != nil {
			t.Errorf("raw write: %v", err)
			return
		}
		if _, _, err := s.ReadMessage(); err != nil {
			t.Errorf("server read close: %v", err)
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
		t.Fatalf("client read: %v", err)
	}
	if op != opText || string(p) != "ok" {
		t.Fatalf("op=%d payload=%q, want text ok", op, p)
	}
	if err := c.WriteClose(CloseMessage(1000, "bye")); err != nil {
		t.Fatalf("client close: %v", err)
	}
	_ = c.Close()
	waitDone(t, done)
}

func TestDialNon101ReturnsDialError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	addr := strings.TrimPrefix(srv.URL, "http://")
	_, err := Dial(context.Background(), addr, "/ws", nil)
	if err == nil {
		t.Fatal("expected error for non-upgrade response")
	}
	var de *DialError
	if !errors.As(err, &de) {
		t.Fatalf("error type = %T (%v), want *DialError", err, err)
	}
	if de.Status != http.StatusOK {
		t.Fatalf("DialError.Status = %d, want 200", de.Status)
	}
	if !errors.Is(err, ErrUpgradeRejected) {
		t.Fatalf("errors.Is(err, ErrUpgradeRejected) = false for %v", err)
	}
	if !strings.Contains(err.Error(), "200") {
		t.Fatalf("DialError.Error() = %q, want it to mention status 200", err.Error())
	}
}

// Given a server answering 101 with a Sec-WebSocket-Accept that does
// not match the client's challenge key, When Dial reads the handshake
// reply, Then it fails with a *DialError (Status 101) that still
// unwraps to ErrUpgradeRejected — a 101 without the keyed accept is not
// a WebSocket endpoint.
func TestDialBadSecWebSocketAccept(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = ln.Close() }()

	go func() {
		conn, err := ln.Accept()
		if err != nil {
			return
		}
		defer func() { _ = conn.Close() }()
		br := bufio.NewReader(conn)
		for {
			line, err := br.ReadString('\n')
			if err != nil {
				return
			}
			if line == "\r\n" {
				break
			}
		}
		_, _ = io.WriteString(conn, "HTTP/1.1 101 Switching Protocols\r\n"+
			"Upgrade: websocket\r\nConnection: Upgrade\r\n"+
			"Sec-WebSocket-Accept: bogus-not-the-keyed-hash\r\n\r\n")
	}()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	_, err = Dial(ctx, ln.Addr().String(), "/ws", nil)
	if err == nil {
		t.Fatal("expected error for a 101 with a bogus Sec-WebSocket-Accept")
	}
	var de *DialError
	if !errors.As(err, &de) {
		t.Fatalf("error type = %T (%v), want *DialError", err, err)
	}
	if de.Status != http.StatusSwitchingProtocols {
		t.Fatalf("DialError.Status = %d, want 101", de.Status)
	}
	if !errors.Is(err, ErrUpgradeRejected) {
		t.Fatalf("errors.Is(err, ErrUpgradeRejected) = false for %v", err)
	}
	if !strings.Contains(err.Error(), "Sec-WebSocket-Accept") {
		t.Fatalf("DialError.Error() = %q, want it to name Sec-WebSocket-Accept", err.Error())
	}
}

// Given a client conn, When the server sends a text message split into
// three fragments with a ping interleaved, Then ReadMessage first
// returns the ping as its own message, then the reassembled payload
// with the FIRST fragment's text opcode.
func TestClientReassemblesFragmentedMessage(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = ln.Close() }()

	done := make(chan struct{})
	go serveUpgrade(t, ln, func(s *Conn, req *http.Request) {
		defer close(done)
		// text fin=0 "hel" + continuation fin=0 "lo " + ping + continuation fin=1 "world"
		raw := []byte{opText, 0x03, 'h', 'e', 'l'}
		raw = append(raw, 0x00, 0x03, 'l', 'o', ' ')
		raw = append(raw, 0x80|opPing, 0x04, 'p', 'i', 'n', 'g')
		raw = append(raw, 0x80, 0x05, 'w', 'o', 'r', 'l', 'd')
		if _, err := s.Conn.Write(raw); err != nil {
			t.Errorf("raw write: %v", err)
			return
		}
		if _, _, err := s.ReadMessage(); err != nil {
			t.Errorf("server read close: %v", err)
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
	if op != opPing || string(p) != "ping" {
		t.Fatalf("first message: op=%d payload=%q, want interleaved ping", op, p)
	}
	op, p, err = c.ReadMessage()
	if err != nil {
		t.Fatalf("client read assembled: %v", err)
	}
	if op != opText || string(p) != "hello world" {
		t.Fatalf("assembled message: op=%d payload=%q, want text %q", op, p, "hello world")
	}
	if err := c.WriteClose(CloseMessage(1000, "bye")); err != nil {
		t.Fatalf("client close: %v", err)
	}
	_ = c.Close()
	waitDone(t, done)
}

// Given a SERVER conn, When a fragmented frame arrives, Then the strict
// rejection stands (the tty path never fragments — client-only
// reassembly must not loosen it).
func TestServerStillRejectsFragmentedFrames(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = ln.Close() }()

	done := make(chan struct{})
	go serveUpgrade(t, ln, func(s *Conn, req *http.Request) {
		defer close(done)
		_, _, err := s.ReadMessage()
		if err == nil || !strings.Contains(err.Error(), "fragmented") {
			t.Errorf("server read = %v, want fragmented-frame rejection", err)
		}
	})

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	c, err := Dial(ctx, ln.Addr().String(), "/ws", nil)
	if err != nil {
		t.Fatalf("Dial: %v", err)
	}
	// fin=0 text frame, zero-length masked payload (mask key present).
	if _, err := c.Conn.Write([]byte{opText, 0x80, 0, 0, 0, 0}); err != nil {
		t.Fatalf("raw write: %v", err)
	}
	waitDone(t, done)
	_ = c.Close()
}

func TestDialGarbageResponse(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = ln.Close() }()

	go func() {
		conn, err := ln.Accept()
		if err != nil {
			return
		}
		defer func() { _ = conn.Close() }()
		br := bufio.NewReader(conn)
		for {
			line, err := br.ReadString('\n')
			if err != nil {
				return
			}
			if line == "\r\n" {
				break
			}
		}
		_, _ = io.WriteString(conn, "this is not an HTTP response at all\r\n\r\n")
	}()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if _, err := Dial(ctx, ln.Addr().String(), "/ws", nil); err == nil {
		t.Fatal("expected error for garbage handshake response")
	}
}

func TestDialHungServerContextTimeout(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = ln.Close() }()

	go func() {
		conn, err := ln.Accept()
		if err != nil {
			return
		}
		defer func() { _ = conn.Close() }()
		time.Sleep(10 * time.Second) // accept but never respond
	}()

	ctx, cancel := context.WithTimeout(context.Background(), 500*time.Millisecond)
	defer cancel()
	start := time.Now()
	_, err = Dial(ctx, ln.Addr().String(), "/ws", nil)
	if err == nil {
		t.Fatal("expected timeout error against hung server")
	}
	if elapsed := time.Since(start); elapsed > 3*time.Second {
		t.Fatalf("Dial ignored ctx deadline: took %v", elapsed)
	}
}
