package dsh_web

import (
	"context"
	"embed"
	"encoding/base64"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"regexp"
	"strings"
	"sync"
	"time"

	"myworktree/internal/ws"
)

const maxBridgeBodyBytes = 4 << 20

var bridgeConnPattern = regexp.MustCompile(`^[A-Za-z0-9_-]{8,64}$`)

//go:embed static/dsh-bridge.js
var bridgeAssets embed.FS

type muxBridge struct {
	authority string
	auth      *upstreamAuth
	logger    *log.Logger
	mu        sync.Mutex
	conns     map[string]*bridgeConn
}

type bridgeConn struct {
	id         string
	bridge     *muxBridge
	upstream   *ws.Conn
	downstream net.Conn
	writeMu    sync.Mutex
	cleanup    sync.Once
}

func newMuxBridge(authority string, auth *upstreamAuth, logger *log.Logger) *muxBridge {
	return &muxBridge{authority: authority, auth: auth, logger: logger, conns: make(map[string]*bridgeConn)}
}

func (b *muxBridge) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	connID := r.URL.Query().Get("conn")
	if !bridgeConnPattern.MatchString(connID) {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid bridge conn"})
		return
	}
	switch r.Method {
	case http.MethodGet:
		b.serveDownlink(w, r, connID)
	case http.MethodPost:
		b.serveUplink(w, r, connID)
	default:
		w.Header().Set("Allow", "GET, POST")
		writeJSON(w, http.StatusMethodNotAllowed, map[string]string{"error": "method not allowed"})
	}
}

func (b *muxBridge) serveDownlink(w http.ResponseWriter, r *http.Request, connID string) {
	b.mu.Lock()
	old := b.conns[connID]
	delete(b.conns, connID)
	b.mu.Unlock()
	if old != nil {
		old.close()
	}

	hijacker, ok := w.(http.Hijacker)
	if !ok {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "streaming unsupported"})
		return
	}
	downstream, rw, err := hijacker.Hijack()
	if err != nil {
		return
	}
	if _, err = rw.WriteString("HTTP/1.1 200 OK\r\nContent-Type: text/event-stream\r\nCache-Control: no-cache\r\nConnection: keep-alive\r\n\r\nretry: 300000\n\n"); err != nil {
		_ = downstream.Close()
		return
	}
	if err = rw.Flush(); err != nil {
		_ = downstream.Close()
		return
	}

	upstream, err := b.dial(r.Context())
	if err != nil {
		_, _ = rw.WriteString("event: bridgeerror\ndata: {\"reason\":\"dial\"}\n\n")
		_ = rw.Flush()
		_ = downstream.Close()
		b.logf("dsh bridge dial %s: %v", b.authority, err)
		return
	}
	conn := &bridgeConn{id: connID, bridge: b, upstream: upstream, downstream: downstream}
	b.mu.Lock()
	b.conns[connID] = conn
	b.mu.Unlock()
	if err := conn.writeSSE("event: open\ndata: {}\n\n"); err != nil {
		conn.close()
		return
	}

	done := make(chan struct{})
	go func() {
		select {
		case <-r.Context().Done():
			conn.close()
		case <-done:
		}
	}()
	go conn.watchDownstream(done)
	go conn.keepAlive(done)
	conn.pump()
	close(done)
	conn.close()
}

func (c *bridgeConn) watchDownstream(done <-chan struct{}) {
	buf := make([]byte, 1)
	if _, err := c.downstream.Read(buf); err != nil {
		select {
		case <-done:
		default:
			c.close()
		}
	}
}

func (b *muxBridge) dial(ctx context.Context) (*ws.Conn, error) {
	makeHeader := func() (http.Header, error) {
		header := make(http.Header)
		if b.auth == nil || !b.auth.Enabled() {
			return header, nil
		}
		cookie, err := b.auth.Cookie(ctx)
		if err != nil {
			return nil, err
		}
		header.Set("Cookie", cookie)
		return header, nil
	}
	header, err := makeHeader()
	if err != nil {
		return nil, err
	}
	conn, err := ws.Dial(ctx, b.authority, "/api/remote.mux", header)
	var dialErr *ws.DialError
	if err == nil || b.auth == nil || !errors.As(err, &dialErr) || dialErr.Status != http.StatusUnauthorized {
		return conn, err
	}
	b.auth.Invalidate()
	header, err = makeHeader()
	if err != nil {
		return nil, err
	}
	return ws.Dial(ctx, b.authority, "/api/remote.mux", header)
}

func (b *muxBridge) serveUplink(w http.ResponseWriter, r *http.Request, connID string) {
	b.mu.Lock()
	conn := b.conns[connID]
	b.mu.Unlock()
	if conn == nil {
		writeJSON(w, http.StatusConflict, map[string]string{"error": "unknown bridge conn"})
		return
	}
	if r.URL.Query().Get("close") == "1" {
		conn.close()
		writeJSON(w, http.StatusAccepted, map[string]string{})
		return
	}
	body, err := io.ReadAll(io.LimitReader(r.Body, maxBridgeBodyBytes))
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid bridge body"})
		return
	}
	if r.URL.Query().Get("bin") == "1" {
		body, err = base64.StdEncoding.DecodeString(string(body))
		if err != nil {
			// A garbage body is the request's fault, not the conn's:
			// answer 400 and leave the bridge conn open for retries.
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid base64"})
			return
		}
		err = conn.upstream.WriteBinary(body)
	} else {
		err = conn.upstream.WriteText(body)
	}
	if err != nil {
		conn.close()
		writeJSON(w, http.StatusConflict, map[string]string{"error": "bridge conn closed"})
		return
	}
	writeJSON(w, http.StatusAccepted, map[string]string{})
}

func (c *bridgeConn) pump() {
	for {
		opcode, payload, err := c.upstream.ReadMessage()
		if err != nil {
			return
		}
		switch {
		case ws.IsPing(opcode):
			if c.upstream.WritePong(payload) != nil {
				return
			}
		case ws.IsClose(opcode):
			code := uint16(1000)
			if len(payload) >= 2 {
				code = binary.BigEndian.Uint16(payload[:2])
			}
			_ = c.writeSSE(fmt.Sprintf("event: close\ndata: {\"code\":%d}\n\n", code))
			return
		case ws.IsDataOpcode(opcode):
			var event string
			if opcode == 0x2 {
				event = "event: bin\ndata: " + base64.StdEncoding.EncodeToString(payload) + "\n\n"
			} else {
				lines := strings.Split(string(payload), "\n")
				event = "data: " + strings.Join(lines, "\ndata: ") + "\n\n"
			}
			if c.writeSSE(event) != nil {
				return
			}
		}
	}
}

func (c *bridgeConn) keepAlive(done <-chan struct{}) {
	ticker := time.NewTicker(15 * time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-ticker.C:
			if c.writeSSE(": ka\n\n") != nil {
				c.close()
				return
			}
		case <-done:
			return
		}
	}
}

func (c *bridgeConn) writeSSE(event string) error {
	c.writeMu.Lock()
	defer c.writeMu.Unlock()
	_, err := io.WriteString(c.downstream, event)
	return err
}

func (c *bridgeConn) close() {
	c.cleanup.Do(func() {
		_ = c.upstream.WriteClose(ws.CloseMessage(1000, ""))
		_ = c.upstream.Close()
		_ = c.downstream.Close()
		c.bridge.mu.Lock()
		if c.bridge.conns[c.id] == c {
			delete(c.bridge.conns, c.id)
		}
		c.bridge.mu.Unlock()
	})
}

func (b *muxBridge) logf(format string, args ...any) {
	if b.logger != nil {
		b.logger.Printf(format, args...)
	}
}
