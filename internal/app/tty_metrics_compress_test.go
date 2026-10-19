//go:build ttymetrics

package app

// Issue #114 decision 5a / R14 + review R1: the double-field
// instrumentation and its LOGICAL counterparts. On a compressed connection
// the [tty-metrics] lines carry TWO families:
//
//	WIRE  (frozen names, historically comparable): replayBytes, liveBytes,
//	                                                  wireBytes
//	LOGICAL (ring bytes the client's cursor advances by): replayLogicalBytes,
//	                                                       liveLogicalBytes,
//	                                                       logicalBytes
//
// On a plaintext connection the two families are EQUAL by construction,
// which is what keeps the unified column layout comparable across every
// connection since the fields existed. These tests pin the compressed
// shrink, the logical drain arithmetic FIX-PLAN §五.3.b computes off, and
// the plaintext equality.

import (
	"errors"
	"net"
	"regexp"
	"strconv"
	"strings"
	"testing"
	"time"
)

var (
	ttyWireLogicalRe   = regexp.MustCompile(`wireBytes=(\d+) logicalBytes=(\d+)`)
	ttyReplayLogicalRe = regexp.MustCompile(`replayBytes=(\d+) replayMs=\d+ budgetExhausted=\w+ windowed=\w+ droppedFromWindow=\d+ replayLogicalBytes=(\d+)`)
	ttyLiveLogicalRe   = regexp.MustCompile(`liveBytes=(\d+) liveFrames=\d+ headNow=\d+ maxWriteBlockMs=\d+ slowWrites=\d+ liveLogicalBytes=(\d+)`)
)

// ttyCompressTestChunk repeated 4096× is 32KB of trivially compressible
// content: the strict wire<logical assertions are only sound for input that
// deflate provably shrinks at BestSpeed.
const ttyCompressTestChunk = "abcdefgh"

// deliverAndHangup publishes the chunk twice, lets the handler drain it
// onto the socket (the live loop consumes within microseconds of the
// publish; the generous sleep only covers scheduler jitter), and hangs up
// so the deferred conn-closed line fires.
func deliverAndHangup(t *testing.T, k *ttyHandshakeKind, c interface{ Close() error }) {
	t.Helper()
	chunk := strings.Repeat(ttyCompressTestChunk, 4096)
	k.publish(chunk)
	k.publish(chunk)
	time.Sleep(300 * time.Millisecond)
	_ = c.Close()
}

func ttyParseInt(t *testing.T, name, s string) int64 {
	t.Helper()
	v, err := strconv.ParseInt(s, 10, 64)
	if err != nil {
		t.Fatalf("%s %q: %v", name, s, err)
	}
	return v
}

func parseWireLogical(t *testing.T, line string) (wire, logical int64) {
	t.Helper()
	m := ttyWireLogicalRe.FindStringSubmatch(line)
	if m == nil {
		t.Fatalf("conn-closed line carries no wireBytes=/logicalBytes= fields: %q", line)
	}
	return ttyParseInt(t, "wireBytes", m[1]), ttyParseInt(t, "logicalBytes", m[2])
}

// parseReplayPair returns (wire replayBytes, logical replayLogicalBytes)
// from a handshake line — the two families read positionally so the
// adjacency the fix-plan contract freezes is itself under test.
func parseReplayPair(t *testing.T, line string) (wire, logical int64) {
	t.Helper()
	m := ttyReplayLogicalRe.FindStringSubmatch(line)
	if m == nil {
		t.Fatalf("handshake line carries no replayBytes=…replayLogicalBytes= pair: %q", line)
	}
	return ttyParseInt(t, "replayBytes", m[1]), ttyParseInt(t, "replayLogicalBytes", m[2])
}

// parseLivePair returns (wire liveBytes, logical liveLogicalBytes) from an
// overflow line.
func parseLivePair(t *testing.T, line string) (wire, logical int64) {
	t.Helper()
	m := ttyLiveLogicalRe.FindStringSubmatch(line)
	if m == nil {
		t.Fatalf("overflow line carries no liveBytes=…liveLogicalBytes= pair: %q", line)
	}
	return ttyParseInt(t, "liveBytes", m[1]), ttyParseInt(t, "liveLogicalBytes", m[2])
}

// tailLine returns the LAST line of log that starts with prefix — a test
// that opens two connections against one logger needs its own connection's
// line, not the first one that ever fired.
func tailLine(t *testing.T, log, prefix string) string {
	t.Helper()
	found := ""
	for _, l := range strings.Split(log, "\n") {
		if strings.HasPrefix(l, prefix) {
			found = l
		}
	}
	if found == "" {
		t.Fatalf("no line with prefix %q in log", prefix)
	}
	return found
}

// TestTTYMetricsCompressHandshakeReplayLogicalBytes pins the compressed
// handshake: a catch-up replay of seeded bytes reports the WIRE replay
// (deflate output) SHRUNK against the LOGICAL replay (the seeded ring
// bytes the client's cursor advances by), the two sit in the frozen
// adjacency, the line ends with compress=true, and the logical replay
// equals the seeded byte count exactly (FIX-PLAN §五.3.b arithmetic
// inputs).
func TestTTYMetricsCompressHandshakeReplayLogicalBytes(t *testing.T) {
	t.Parallel()
	k := newTTYHandshakeKind(1 << 20)
	addr, _, instID, logOut := ttyWSTestServer(t, k)
	seed := compressSeed(4000) // ~184KB, well over 64KB so the catch-up replays multiple chunks
	k.buf.WriteString(seed)

	dialHandshakeCompress(t, addr, ttyWSPath(instID, "caps=sync,compress&since=0"))

	waitForTTYMetricsLine(t, logOut, "[tty-metrics] handshake id="+instID, 5*time.Second)
	line := tailLine(t, logOut.String(), "[tty-metrics] handshake id="+instID)
	wire, logical := parseReplayPair(t, line)
	if logical != int64(len(seed)) {
		t.Fatalf("replayLogicalBytes=%d, want the seeded %d logical bytes (cursor-unit ground truth): %q", logical, len(seed), line)
	}
	if wire >= logical {
		t.Fatalf("replayBytes=%d >= replayLogicalBytes=%d on a compressed replay of compressible content: the wire shrink must be visible: %q", wire, logical, line)
	}
	if !strings.HasSuffix(line, " compress=true") {
		t.Fatalf("compressed handshake line must end with the appended compress=true flag: %q", line)
	}
}

// TestTTYMetricsCompressConnReportsWireAndLogicalBytes pins the compressed
// conn-closed pair: wire < logical for compressible content, and both
// delivered chunks are accounted in the logical count.
func TestTTYMetricsCompressConnReportsWireAndLogicalBytes(t *testing.T) {
	t.Parallel()
	k := newTTYHandshakeKind(1 << 20)
	addr, _, instID, logOut := ttyWSTestServer(t, k)

	c, _, _ := dialHandshakeCompress(t, addr, ttyWSPath(instID, "caps=sync,compress"))
	k.waitSubscribers(t, 5*time.Second)

	deliverAndHangup(t, k, c)

	waitForTTYMetricsLine(t, logOut, "[tty-metrics] conn-closed id="+instID, 5*time.Second)
	line := tailLine(t, logOut.String(), "[tty-metrics] conn-closed id="+instID)
	wire, logical := parseWireLogical(t, line)
	if logical < 2*int64(len(ttyCompressTestChunk)*4096) {
		t.Fatalf("logicalBytes=%d, want ≥%d (both published chunks must have been delivered): %q", logical, 2*int64(len(ttyCompressTestChunk)*4096), line)
	}
	if wire >= logical {
		t.Fatalf("wireBytes=%d >= logicalBytes=%d on a compressed connection of compressible content: the shrink must be visible: %q", wire, logical, line)
	}
}

// TestTTYMetricsCompressOverflowLiveLogicalBytes pins the compressed
// overflow line: liveLogicalBytes (the logical drain) exceeds liveBytes
// (the wire drain) for compressible content, so the drain-rate FIX-PLAN
// §五.3.b reads off the LOGICAL family, not the wire one.
func TestTTYMetricsCompressOverflowLiveLogicalBytes(t *testing.T) {
	t.Parallel()
	k := newTTYHandshakeKind(1 << 20)
	addr, _, instID, logOut := ttyWSTestServer(t, k)

	c, _, _ := dialHandshakeCompress(t, addr, ttyWSPath(instID, "caps=sync,compress,overflow-notice"))
	k.waitSubscribers(t, 5*time.Second)
	chunk := strings.Repeat(ttyCompressTestChunk, 4096)
	floodUntilDrop(t, k, chunk)

	// The FIRST overflow line fires the instant the queue first overflows —
	// before the handler has delivered anything, so its live pair is
	// (0,0). Keep the producer outrunning the consumer and drain the
	// socket until a LATER sample lands: every overflow line is
	// logicalBytes−replayLogicalBytes at its sampling instant, so a post-
	// delivery line carries a positive logical drain that must exceed the
	// wire drain.
	deadline := time.Now().Add(10 * time.Second)
	var line string
	for {
		k.publish(chunk)
		if err := c.SetReadDeadline(time.Now().Add(200 * time.Millisecond)); err != nil {
			t.Fatalf("set read deadline: %v", err)
		}
		_, _, rerr := c.ReadMessage()
		_ = c.SetReadDeadline(time.Time{})
		if rerr != nil {
			var ne net.Error
			if !errors.As(rerr, &ne) || !ne.Timeout() {
				t.Fatalf("read on the notice connection: %v", rerr)
			}
		}
		if strings.Contains(logOut.String(), "[tty-metrics] overflow id="+instID) {
			if l := tailLine(t, logOut.String(), "[tty-metrics] overflow id="+instID); l != "" {
				if _, logical := parseLivePair(t, l); logical > 0 {
					line = l
					break
				}
			}
		}
		if time.Now().After(deadline) {
			t.Fatalf("no overflow line with a positive liveLogicalBytes within 10s; log = %q", logOut.String())
		}
	}
	wire, logical := parseLivePair(t, line)
	if logical <= 0 {
		t.Fatalf("liveLogicalBytes=%d, want a positive logical drain: %q", logical, line)
	}
	if wire >= logical {
		t.Fatalf("liveBytes=%d >= liveLogicalBytes=%d on a compressed overflow of compressible content: the drain must be measured logical: %q", wire, logical, line)
	}
}

// TestTTYMetricsPlainConnFamiliesEqual: on a plaintext connection every
// wire/logical pair is EQUAL — handshake (replayBytes==replayLogicalBytes),
// conn-closed (wireBytes==logicalBytes) — and the handshake line carries
// no compress field. This is what keeps the unified layout historically
// comparable.
func TestTTYMetricsPlainConnFamiliesEqual(t *testing.T) {
	t.Parallel()
	k := newTTYHandshakeKind(1 << 20)
	addr, _, instID, logOut := ttyWSTestServer(t, k)
	seed := compressSeed(500) // a bounded tail replay so the handshake pair is non-zero
	k.buf.WriteString(seed)

	c, _, _ := dialHandshake(t, addr, ttyWSPath(instID, "caps=sync"))
	k.waitSubscribers(t, 5*time.Second)

	deliverAndHangup(t, k, c)

	waitForTTYMetricsLine(t, logOut, "[tty-metrics] handshake id="+instID, 5*time.Second)
	hs := tailLine(t, logOut.String(), "[tty-metrics] handshake id="+instID)
	rw, rl := parseReplayPair(t, hs)
	if rw != rl {
		t.Fatalf("plaintext handshake replayBytes=%d != replayLogicalBytes=%d — the two families must be equal: %q", rw, rl, hs)
	}
	if strings.Contains(hs, "compress=true") {
		t.Fatalf("plaintext handshake line must not carry the compress field: %q", hs)
	}

	waitForTTYMetricsLine(t, logOut, "[tty-metrics] conn-closed id="+instID, 5*time.Second)
	line := tailLine(t, logOut.String(), "[tty-metrics] conn-closed id="+instID)
	wire, logical := parseWireLogical(t, line)
	if wire != logical {
		t.Fatalf("plaintext wireBytes=%d != logicalBytes=%d — the two must be equal: %q", wire, logical, line)
	}
}
