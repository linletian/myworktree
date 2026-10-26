package framework

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"sync"
	"testing"
	"time"
)

// subPlainKind implements ONLY the plain SubscribeOutput door — no policy
// surface — so SubscribeOutputWith's callback can prove the two halves of
// its contract against it: the kind it receives is the kind resolved
// through the running-instance gate, and a caller whose preferred interface
// is absent still gets a live subscription from the fallback inside its own
// callback (issue #114: the app layer must not fork liveness onto a store
// read, and a kind without policy support must stay subscribable exactly as
// Manager.SubscribeOutput subscribes it).
type subPlainKind struct {
	mu   sync.Mutex
	subs []chan string
}

func (k *subPlainKind) Manifest() KindInfo {
	return KindInfo{Name: "sub-plain", Label: "Sub Plain"}
}

func (k *subPlainKind) Spawn(ctx context.Context, p SpawnParams) (Handle, *ReadySignal, error) {
	ready := NewReadySignal()
	ready.Close()
	return NewHandle("sub-plain", "inner"), ready, nil
}

func (k *subPlainKind) Stop(h Handle, graceSeconds int) error { return nil }

func (k *subPlainKind) Status(h Handle) (Status, string) { return StatusRunning, "" }

func (k *subPlainKind) ReadLogs(h Handle, since, maxBytes int64) (string, int64, error) {
	return "", since, nil
}

func (k *subPlainKind) KindBlob(h Handle) (json.RawMessage, error) {
	return json.RawMessage(`{}`), nil
}

func (k *subPlainKind) HTTPHint(instanceID string) string { return "" }

func (k *subPlainKind) RegisterHTTP(mux *http.ServeMux, instanceID string, h Handle) {}

func (k *subPlainKind) SubscribeOutput(id string) (<-chan string, func(), error) {
	ch := make(chan string, 8)
	k.mu.Lock()
	k.subs = append(k.subs, ch)
	k.mu.Unlock()
	cancel := func() {
		k.mu.Lock()
		defer k.mu.Unlock()
		for i, c := range k.subs {
			if c == ch {
				k.subs = append(k.subs[:i], k.subs[i+1:]...)
				break
			}
		}
	}
	return ch, cancel, nil
}

func (k *subPlainKind) publish(chunk string) {
	k.mu.Lock()
	defer k.mu.Unlock()
	for _, c := range k.subs {
		select {
		case c <- chunk:
		default:
		}
	}
}

// ghostHandleKind spawns a handle whose KindName is NOT the registry key
// of any registered kind, so the running-instance gate passes and
// Registry.Get(ri.handle.KindName) fails — the exact fork-free shape that
// makes SubscribeOutput's kind-resolution error observable. Both entries
// into that door (SubscribeOutput and SubscribeOutputWith) must answer it
// byte-identically.
type ghostHandleKind struct{}

func (k ghostHandleKind) Manifest() KindInfo {
	return KindInfo{Name: "ghost-handle", Label: "Ghost Handle"}
}

func (k ghostHandleKind) Spawn(ctx context.Context, p SpawnParams) (Handle, *ReadySignal, error) {
	ready := NewReadySignal()
	ready.Close()
	return NewHandle("ghost-kind-not-registered", "inner"), ready, nil
}

func (k ghostHandleKind) Stop(h Handle, graceSeconds int) error { return nil }

func (k ghostHandleKind) Status(h Handle) (Status, string) { return StatusRunning, "" }

func (k ghostHandleKind) ReadLogs(h Handle, since, maxBytes int64) (string, int64, error) {
	return "", since, nil
}

func (k ghostHandleKind) KindBlob(h Handle) (json.RawMessage, error) {
	return json.RawMessage(`{}`), nil
}

func (k ghostHandleKind) HTTPHint(instanceID string) string { return "" }

func (k ghostHandleKind) RegisterHTTP(mux *http.ServeMux, instanceID string, h Handle) {}

// startRunningInstance starts an instance of a registered kind and waits
// for the starting→running transition (same bounded wait as
// startRecordingInstance in manager_tail_test.go), stopping it on cleanup.
func startRunningInstance(t *testing.T, mgr *Manager, kind, name string) string {
	t.Helper()
	inst, err := mgr.Start(context.Background(), StartParams{WorktreeID: "wt1", Kind: kind, Name: name})
	if err != nil {
		t.Fatalf("Start(%s): %v", kind, err)
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

// TestSubscribeOutputWith_NotRunningErrorByteIdenticalToSubscribeOutput
// pins the door: an id absent from m.running must be answered by
// SubscribeOutputWith with the byte-identical error SubscribeOutput
// returns — same text, so an app-layer teardown (the TTY handler's 1013
// reason) cannot tell the two entries apart. fn must never be called: the
// gate closes before any kind resolution, let alone any callback.
func TestSubscribeOutputWith_NotRunningErrorByteIdenticalToSubscribeOutput(t *testing.T) {
	t.Parallel()
	reg := NewRegistry()
	reg.Register(&subPlainKind{})
	mgr, _ := newTestManager(t, reg, t.TempDir())

	const id = "never-started"
	called := false
	ch, cancel, errWith := mgr.SubscribeOutputWith(id, func(k Kind) (<-chan string, func(), error) {
		called = true
		return nil, nil, errors.New("fn must not run behind a closed door")
	})
	chPlain, cancelPlain, errPlain := mgr.SubscribeOutput(id)

	if errWith == nil || errPlain == nil {
		t.Fatalf("errors = (%v, %v); want both non-nil for a non-running instance", errWith, errPlain)
	}
	if errPlain.Error() != errWith.Error() {
		t.Fatalf("error fork: SubscribeOutputWith = %q, SubscribeOutput = %q; want byte-identical", errWith.Error(), errPlain.Error())
	}
	if want := "instance not running: " + id; errWith.Error() != want {
		t.Fatalf("error = %q, want %q", errWith.Error(), want)
	}
	if called {
		t.Fatal("fn ran for an instance absent from the running map: the gate must precede the callback")
	}
	if ch != nil || cancel != nil || chPlain != nil || cancelPlain != nil {
		t.Fatal("non-running subscribe returned a channel or cancel; want nils on the error path")
	}
}

// TestSubscribeOutputWith_KindResolutionErrorByteIdenticalToSubscribeOutput
// pins the second door: the instance IS running (past the gate) but its
// handle names a kind the registry does not know. SubscribeOutputWith and
// SubscribeOutput resolve through the same Registry.Get(ri.handle.KindName)
// lookup and must therefore answer byte-identically, sentinel included.
func TestSubscribeOutputWith_KindResolutionErrorByteIdenticalToSubscribeOutput(t *testing.T) {
	t.Parallel()
	reg := NewRegistry()
	reg.Register(ghostHandleKind{})
	mgr, _ := newTestManager(t, reg, t.TempDir())
	instID := startRunningInstance(t, mgr, "ghost-handle", "ghost")

	called := false
	_, _, errWith := mgr.SubscribeOutputWith(instID, func(k Kind) (<-chan string, func(), error) {
		called = true
		return nil, nil, nil
	})
	_, _, errPlain := mgr.SubscribeOutput(instID)

	if errWith == nil || errPlain == nil {
		t.Fatalf("errors = (%v, %v); want both non-nil for an unresolvable kind", errWith, errPlain)
	}
	if errPlain.Error() != errWith.Error() {
		t.Fatalf("error fork: SubscribeOutputWith = %q, SubscribeOutput = %q; want byte-identical", errWith.Error(), errPlain.Error())
	}
	if !errors.Is(errWith, ErrUnknownKind) {
		t.Fatalf("error %v does not wrap ErrUnknownKind; the sentinel must survive verbatim", errWith)
	}
	if called {
		t.Fatal("fn ran although Registry.Get failed: fn receives only a RESOLVED kind")
	}
}

// TestSubscribeOutputWith_FallbackInsideFnSubscribesPlainKind drives the
// consumer shape the TTY handler uses (issue #114): fn asserts an optional
// policy interface the kind does NOT implement and falls back inside the
// callback to that kind's plain SubscribeOutput. The channel that comes
// back must be the live one the kind publishes into — proof that the
// fallback path is usable, not just reachable, and that fn received the
// very kind the gate resolved.
func TestSubscribeOutputWith_FallbackInsideFnSubscribesPlainKind(t *testing.T) {
	t.Parallel()
	k := &subPlainKind{}
	reg := NewRegistry()
	reg.Register(k)
	mgr, _ := newTestManager(t, reg, t.TempDir())
	instID := startRunningInstance(t, mgr, "sub-plain", "plain")

	type policySub interface {
		SubscribeOutputWithPolicy(id string) (<-chan string, func(), error)
	}

	ch, cancel, err := mgr.SubscribeOutputWith(instID, func(kind Kind) (<-chan string, func(), error) {
		if _, ok := kind.(policySub); ok {
			t.Errorf("unexpected policy surface on subPlainKind")
		}
		if got := kind.Manifest().Name; got != "sub-plain" {
			t.Errorf("fn received kind %q, want the resolved %q", got, "sub-plain")
		}
		if plain, ok := kind.(interface {
			SubscribeOutput(id string) (<-chan string, func(), error)
		}); ok {
			return plain.SubscribeOutput(instID)
		}
		return nil, nil, errors.New("kind without plain SubscribeOutput")
	})
	if err != nil {
		t.Fatalf("SubscribeOutputWith: %v", err)
	}
	defer cancel()
	if ch == nil {
		t.Fatal("SubscribeOutputWith returned a nil channel with nil error")
	}

	k.publish("hello")
	select {
	case got := <-ch:
		if got != "hello" {
			t.Fatalf("chunk = %q, want hello: fn's fallback must deliver from the kind behind the gate", got)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("fallback subscription never received the published chunk")
	}
}

// TestSubscribeOutputWith_FnResultPassesThroughVerbatim pins the last
// piece of the contract: whatever fn returns — including its own error —
// comes back uninterpreted, and a nil fn is rejected instead of panicking
// deep inside the gate.
func TestSubscribeOutputWith_FnResultPassesThroughVerbatim(t *testing.T) {
	t.Parallel()
	reg := NewRegistry()
	reg.Register(&subPlainKind{})
	mgr, _ := newTestManager(t, reg, t.TempDir())
	instID := startRunningInstance(t, mgr, "sub-plain", "pass")

	sentinel := errors.New("fn sentinel error")
	ch, cancel, err := mgr.SubscribeOutputWith(instID, func(kind Kind) (<-chan string, func(), error) {
		return nil, nil, sentinel
	})
	if err != sentinel {
		t.Fatalf("err = %v, want the sentinel fn returned, verbatim (same error value)", err)
	}
	if ch != nil || cancel != nil {
		t.Fatal("fn's nils were not passed through verbatim")
	}

	if _, _, err := mgr.SubscribeOutputWith(instID, nil); err == nil {
		t.Fatal("nil fn must error, not panic")
	}
}
