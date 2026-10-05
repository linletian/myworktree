package framework

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

// ringBackedKind mirrors internal/instance/pty/driver.go's ReadLogs onto a
// real RingBuffer and records the largest maxBytes it was ever asked for, so
// "EndOffset does not copy the buffer" is an observable fact rather than a
// comment (issue #86).
type ringBackedKind struct {
	buf     *RingBuffer
	maxSeen atomic.Int64
}

func (k *ringBackedKind) Manifest() KindInfo {
	return KindInfo{Name: "ring-backed", Label: "Ring Backed"}
}

func (k *ringBackedKind) Spawn(ctx context.Context, p SpawnParams) (Handle, *ReadySignal, error) {
	ready := NewReadySignal()
	ready.Close()
	return NewHandle("ring-backed", "inner"), ready, nil
}

func (k *ringBackedKind) Stop(h Handle, graceSeconds int) error { return nil }

func (k *ringBackedKind) Status(h Handle) (Status, string) { return StatusRunning, "" }

func (k *ringBackedKind) ReadLogs(h Handle, since, maxBytes int64) (string, int64, error) {
	if maxBytes > k.maxSeen.Load() {
		k.maxSeen.Store(maxBytes)
	}
	if since < 0 {
		body, off := k.buf.Tail(maxBytes)
		return body, off, nil
	}
	return k.buf.ReadSince(since, maxBytes)
}

func (k *ringBackedKind) KindBlob(h Handle) (json.RawMessage, error) {
	return json.RawMessage(`{}`), nil
}

func (k *ringBackedKind) HTTPHint(instanceID string) string { return "" }

func (k *ringBackedKind) RegisterHTTP(mux *http.ServeMux, instanceID string, h Handle) {}

// startRingBackedInstance starts a ring-backed instance and waits for the
// starting→running transition, so reads below never race runLifecycle's
// async persist (the same reason startRecordingInstance waits).
func startRingBackedInstance(t *testing.T, mgr *Manager) string {
	t.Helper()
	inst, err := mgr.Start(context.Background(), StartParams{WorktreeID: "wt1", Kind: "ring-backed", Name: "t1"})
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	waitFor := time.Now().Add(5 * time.Second)
	for {
		got, err := mgr.Get(inst.ID)
		if err != nil {
			t.Fatalf("Get: %v", err)
		}
		if got.Status == StatusRunning.String() {
			break
		}
		if time.Now().After(waitFor) {
			t.Fatal("status did not reach running within 5s")
		}
		time.Sleep(50 * time.Millisecond)
	}
	t.Cleanup(func() {
		if err := mgr.Stop(inst.ID); err != nil {
			t.Errorf("Stop: %v", err)
		}
	})
	return inst.ID
}

// TestManager_EndOffsetIsTheHeadWithoutTheBody pins the whole reason
// EndOffset exists (issue #86): the callers that already hold the tail need
// the cursor only, so they must get the exact head Tail reports without
// asking the kind for a single buffered byte.
func TestManager_EndOffsetIsTheHeadWithoutTheBody(t *testing.T) {
	t.Parallel()
	k := &ringBackedKind{buf: NewRingBuffer(64)}
	reg := NewRegistry()
	reg.Register(k)
	mgr, _ := newTestManager(t, reg, t.TempDir())
	instID := startRingBackedInstance(t, mgr)

	// An untouched ring sits at 0, and 0 is a legitimate cursor here.
	if off, err := mgr.EndOffset(instID); err != nil || off != 0 {
		t.Fatalf("EndOffset of an untouched ring = (%d, %v), want (0, nil)", off, err)
	}

	k.buf.WriteString("0123456789") // head 10
	off, err := mgr.EndOffset(instID)
	if err != nil {
		t.Fatalf("EndOffset: %v", err)
	}
	if off != 10 {
		t.Fatalf("EndOffset = %d, want the head 10", off)
	}
	if k.maxSeen.Load() != 0 {
		t.Fatalf("the kind was asked for %d bytes, want 0 — EndOffset must not copy the tail", k.maxSeen.Load())
	}

	// The head agrees with Tail's, so a cursor published from either path
	// stays contiguous with the same live stream.
	if _, tailOff, err := mgr.Tail(instID, 4096); err != nil || tailOff != off {
		t.Fatalf("Tail head = (%d, %v), want EndOffset's %d", tailOff, err, off)
	}

	// Past the cap the head keeps counting while the body stays bounded —
	// the follow-from-live-end cursor must be the counter, not the length.
	k.buf.WriteString(strings.Repeat("x", 100)) // head 110, oldest live byte at 46
	if off, err := mgr.EndOffset(instID); err != nil || off != 110 {
		t.Fatalf("EndOffset after an overrun = (%d, %v), want (110, nil)", off, err)
	}
	if body, tailOff, err := mgr.Tail(instID, 4096); err != nil || tailOff != 110 || int64(len(body)) != 64 {
		t.Fatalf("Tail after an overrun = (%d bytes, %d, %v), want 64 bytes at head 110", len(body), tailOff, err)
	}
}

// TestManager_EndOffsetOfNonRunningInstanceIsZero pins the edge the log
// stream and the WS handshake both inherit: Manager.Tail answers a stopped
// instance with ("", 0, nil), so EndOffset must answer 0 rather than an
// error or a stale head — the handshake then publishes 0 and fails at
// SubscribeOutput, exactly as it did before the sentinel existed.
func TestManager_EndOffsetOfNonRunningInstanceIsZero(t *testing.T) {
	t.Parallel()
	k := &ringBackedKind{buf: NewRingBuffer(64)}
	reg := NewRegistry()
	reg.Register(k)
	mgr, _ := newTestManager(t, reg, t.TempDir())
	instID := startRingBackedInstance(t, mgr)
	k.buf.WriteString("buffered-before-stop") // head 20

	if err := mgr.Stop(instID); err != nil {
		t.Fatalf("Stop: %v", err)
	}
	off, err := mgr.EndOffset(instID)
	if err != nil {
		t.Fatalf("EndOffset of a stopped instance: %v", err)
	}
	if off != 0 {
		t.Fatalf("EndOffset of a stopped instance = %d, want 0 (the Tail contract)", off)
	}
	body, tailOff, err := mgr.Tail(instID, 4096)
	if err != nil || body != "" || tailOff != 0 {
		t.Fatalf("Tail of a stopped instance = (%q, %d, %v), want (\"\", 0, nil) — the same answer EndOffset gives", body, tailOff, err)
	}
}

// echoLogsKind models the kinds without log capture (reasonix /
// opencode-web / dsh-web): ReadLogs returns no data and echoes `since`
// back, so a tail read answers ("", -1, nil) and Manager must normalise it.
type echoLogsKind struct {
	lastMax atomic.Int64
}

func (k *echoLogsKind) Manifest() KindInfo { return KindInfo{Name: "echo-logs", Label: "Echo Logs"} }

func (k *echoLogsKind) Spawn(ctx context.Context, p SpawnParams) (Handle, *ReadySignal, error) {
	ready := NewReadySignal()
	ready.Close()
	return NewHandle("echo-logs", "inner"), ready, nil
}

func (k *echoLogsKind) Stop(h Handle, graceSeconds int) error { return nil }

func (k *echoLogsKind) Status(h Handle) (Status, string) { return StatusRunning, "" }

func (k *echoLogsKind) ReadLogs(h Handle, since, maxBytes int64) (string, int64, error) {
	k.lastMax.Store(maxBytes)
	return "", since, nil
}

func (k *echoLogsKind) KindBlob(h Handle) (json.RawMessage, error) {
	return json.RawMessage(`{}`), nil
}

func (k *echoLogsKind) HTTPHint(instanceID string) string { return "" }

func (k *echoLogsKind) RegisterHTTP(mux *http.ServeMux, instanceID string, h Handle) {}

// TestManager_EndOffsetNormalisesTheEchoedTailSentinel pins the
// kinds-without-log-capture branch (reasonix / opencode-web / dsh-web):
// their ReadLogs echoes `since` back, so a tail read returns -1, which
// EndOffset clamps to 0 exactly as Manager.Tail does — otherwise the log
// stream's empty first frame would publish -2 as a "cursor" and the client
// would ask for the sentinel forever.
func TestManager_EndOffsetNormalisesTheEchoedTailSentinel(t *testing.T) {
	t.Parallel()
	k := &echoLogsKind{}
	reg := NewRegistry()
	reg.Register(k)
	mgr, _ := newTestManager(t, reg, t.TempDir())

	inst, err := mgr.Start(context.Background(), StartParams{WorktreeID: "wt1", Kind: "echo-logs", Name: "t1"})
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	instID := inst.ID
	t.Cleanup(func() { _ = mgr.Stop(instID) })

	off, err := mgr.EndOffset(instID)
	if err != nil {
		t.Fatalf("EndOffset: %v", err)
	}
	if off != 0 {
		t.Fatalf("EndOffset = %d, want 0 (the echoed -1 clamped, as in Tail)", off)
	}
	if got := k.lastMax.Load(); got != 0 {
		t.Fatalf("ReadLogs maxBytes = %d, want 0", got)
	}
	if _, tailOff, err := mgr.Tail(instID, 4096); err != nil || tailOff != 0 {
		t.Fatalf("Tail of a non-capturing kind = (%d, %v), want (0, nil) — the same answer", tailOff, err)
	}
}
