package ws

import (
	"encoding/binary"
	"testing"
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
