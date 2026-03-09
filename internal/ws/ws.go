package ws

import (
	"bufio"
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
	rw *bufio.ReadWriter
	mu sync.Mutex
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

func (c *Conn) ReadMessage() (opcode byte, payload []byte, err error) {
	h := make([]byte, 2)
	if _, err = io.ReadFull(c.rw, h); err != nil {
		return 0, nil, err
	}
	fin := (h[0] & 0x80) != 0
	opcode = h[0] & 0x0F
	masked := (h[1] & 0x80) != 0
	length := int64(h[1] & 0x7F)
	if length == 126 {
		ext := make([]byte, 2)
		if _, err = io.ReadFull(c.rw, ext); err != nil {
			return 0, nil, err
		}
		length = int64(binary.BigEndian.Uint16(ext))
	} else if length == 127 {
		ext := make([]byte, 8)
		if _, err = io.ReadFull(c.rw, ext); err != nil {
			return 0, nil, err
		}
		u := binary.BigEndian.Uint64(ext)
		if u > 10*1024*1024 {
			return 0, nil, errors.New("websocket frame too large")
		}
		length = int64(u)
	}
	if !masked {
		return 0, nil, errors.New("client websocket frame must be masked")
	}
	mask := make([]byte, 4)
	if _, err = io.ReadFull(c.rw, mask); err != nil {
		return 0, nil, err
	}
	payload = make([]byte, length)
	if _, err = io.ReadFull(c.rw, payload); err != nil {
		return 0, nil, err
	}
	for i := range payload {
		payload[i] ^= mask[i%4]
	}
	if !fin {
		return 0, nil, errors.New("fragmented websocket frames are not supported")
	}
	return opcode, payload, nil
}

func (c *Conn) WriteText(p []byte) error   { return c.writeFrame(opText, p) }
func (c *Conn) WriteBinary(p []byte) error { return c.writeFrame(opBinary, p) }
func (c *Conn) WritePong(p []byte) error   { return c.writeFrame(opPong, p) }
func (c *Conn) WriteClose(p []byte) error  { return c.writeFrame(opClose, p) }

func (c *Conn) writeFrame(op byte, p []byte) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	h := []byte{0x80 | op}
	n := len(p)
	switch {
	case n <= 125:
		h = append(h, byte(n))
	case n <= 65535:
		h = append(h, 126, byte(n>>8), byte(n))
	default:
		h = append(h, 127, 0, 0, 0, 0, byte(n>>24), byte(n>>16), byte(n>>8), byte(n))
	}
	if _, err := c.rw.Write(h); err != nil {
		return err
	}
	if _, err := c.rw.Write(p); err != nil {
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
