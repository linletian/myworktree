package framework

import (
	"context"
	"encoding/json"
	"net/http"
	"sync/atomic"
	"testing"
	"time"
)

// recordingLogsKind captures the since argument passed to ReadLogs.
// It guards the issue #81 regression where Manager.Tail requested
// offset 0 (oldest bytes) instead of tail semantics (newest bytes).
type recordingLogsKind struct {
	lastSince atomic.Int64
}

func (k *recordingLogsKind) Manifest() KindInfo {
	return KindInfo{Name: "recording", Label: "Recording"}
}

func (k *recordingLogsKind) Spawn(ctx context.Context, p SpawnParams) (Handle, *ReadySignal, error) {
	ready := NewReadySignal()
	ready.Close()
	return NewHandle("recording", "inner"), ready, nil
}

func (k *recordingLogsKind) Stop(h Handle, graceSeconds int) error { return nil }

func (k *recordingLogsKind) Status(h Handle) (Status, string) { return StatusRunning, "" }

func (k *recordingLogsKind) ReadLogs(h Handle, since, max int64) (string, int64, error) {
	k.lastSince.Store(since)
	return "tail-body", 42, nil
}

func (k *recordingLogsKind) KindBlob(h Handle) (json.RawMessage, error) {
	return json.RawMessage(`{}`), nil
}

func (k *recordingLogsKind) HTTPHint(instanceID string) string { return "" }

func (k *recordingLogsKind) RegisterHTTP(mux *http.ServeMux, instanceID string, h Handle) {}

// startRecordingInstance starts a recording-kind instance and waits for
// the starting→running transition so runLifecycle's async state persist
// finishes before the test returns (TempDir cleanup raced with it
// otherwise); Stop joins the lifecycle goroutine.
func startRecordingInstance(t *testing.T, mgr *Manager) string {
	t.Helper()
	inst, err := mgr.Start(context.Background(), StartParams{WorktreeID: "wt1", Kind: "recording", Name: "t1"})
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

func TestManager_TailRequestsNewestBytes(t *testing.T) {
	t.Parallel()
	k := &recordingLogsKind{}
	reg := NewRegistry()
	reg.Register(k)
	mgr, _ := newTestManager(t, reg, t.TempDir())
	instID := startRecordingInstance(t, mgr)

	body, off, err := mgr.Tail(instID, 4096)
	if err != nil {
		t.Fatalf("Tail: %v", err)
	}
	if got := k.lastSince.Load(); got >= 0 {
		t.Fatalf("ReadLogs since = %d, want negative (tail semantics)", got)
	}
	if body != "tail-body" || off != 42 {
		t.Fatalf("Tail = (%q, %d), want (%q, 42)", body, off, "tail-body")
	}
}

func TestManager_ReadSinceClampsNegativeSince(t *testing.T) {
	t.Parallel()
	k := &recordingLogsKind{}
	reg := NewRegistry()
	reg.Register(k)
	mgr, _ := newTestManager(t, reg, t.TempDir())
	instID := startRecordingInstance(t, mgr)

	if _, _, err := mgr.ReadSince(instID, -5, 4096); err != nil {
		t.Fatalf("ReadSince: %v", err)
	}
	if got := k.lastSince.Load(); got != 0 {
		t.Fatalf("ReadLogs since = %d, want 0 (negative clamped; tail lives in Tail)", got)
	}
}

// shortCursorLogsKind models a kind that under-reports its tail cursor:
// it hands back 10 bytes but claims the stream only advanced to offset 4.
type shortCursorLogsKind struct{ recordingLogsKind }

func (k *shortCursorLogsKind) ReadLogs(h Handle, since, max int64) (string, int64, error) {
	return "0123456789", 4, nil
}

// Guards the manager-side cursor lower bound: no kind may report a tail
// cursor that ends before the bytes it just returned, or the follow-up
// ReadSince(off) re-delivers them.
func TestManager_TailClampsShortKindCursor(t *testing.T) {
	t.Parallel()
	k := &shortCursorLogsKind{}
	reg := NewRegistry()
	reg.Register(k)
	mgr, _ := newTestManager(t, reg, t.TempDir())
	instID := startRecordingInstance(t, mgr)

	body, off, err := mgr.Tail(instID, 4096)
	if err != nil {
		t.Fatalf("Tail: %v", err)
	}
	if body != "0123456789" {
		t.Fatalf("Tail body = %q, want the kind's body verbatim", body)
	}
	if off < int64(len(body)) {
		t.Fatalf("Tail cursor = %d but returned %d bytes; the cursor must not end before the data", off, len(body))
	}
	if off != int64(len(body)) {
		t.Fatalf("Tail cursor = %d, want %d (clamped to the end of the returned body)", off, len(body))
	}
}
