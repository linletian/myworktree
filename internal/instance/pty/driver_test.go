package pty

import (
	"strings"
	"sync"
	"testing"
	"time"

	"myworktree/internal/framework"
)

func TestDriver_Manifest(t *testing.T) {
	d := Driver{}
	m := d.Manifest()
	if m.Name != "pty" {
		t.Fatalf("Name = %q, want pty", m.Name)
	}
	if !m.Interactive {
		t.Fatalf("Interactive = false, want true for PTY kind")
	}
	if m.Label == "" || m.Description == "" {
		t.Fatalf("Label/Description must be non-empty: %+v", m)
	}
}

func TestDriver_HTTPHint(t *testing.T) {
	d := Driver{}
	if hint := d.HTTPHint("any-id"); hint != "" {
		t.Fatalf("PTY kind must not register HTTP routes; got hint %q", hint)
	}
}

func TestDriver_KindBlob_Empty(t *testing.T) {
	d := Driver{}
	h := framework.NewHandle("pty", &Handle{})
	blob, err := d.KindBlob(h)
	if err != nil {
		t.Fatalf("KindBlob: %v", err)
	}
	// PTY has no kind-private state; blob is the empty marker {}.
	if !strings.HasPrefix(string(blob), "{") {
		t.Fatalf("PTY blob = %s, want object", blob)
	}
}

func TestNewID_KindName(t *testing.T) {
	h := framework.NewHandle("pty", &Handle{})
	if h.KindName != "pty" {
		t.Fatalf("Handle.KindName = %q, want pty", h.KindName)
	}
}

func TestMustHandle_PanicsOnWrongType(t *testing.T) {
	defer func() {
		if recover() == nil {
			t.Fatal("mustHandle should panic on wrong inner type")
		}
	}()
	mustHandle(framework.NewHandle("pty", "not a *Handle"))
}

// TestSubscribeOutput_ScopedPerInstance guards against the
// pre-kind-refactor regression where all PTY instances shared a single
// subscriber set (a WS subscriber to instance A would receive
// instance B's output).
func TestSubscribeOutput_ScopedPerInstance(t *testing.T) {
	const idA, idB = "inst-A", "inst-B"

	chA, cancelA, err := SubscribeOutput(idA)
	if err != nil {
		t.Fatalf("SubscribeOutput(A): %v", err)
	}
	defer cancelA()

	chB, cancelB, err := SubscribeOutput(idB)
	if err != nil {
		t.Fatalf("SubscribeOutput(B): %v", err)
	}
	defer cancelB()

	broadcast(idA, "hello-A")
	broadcast(idB, "hello-B")

	if got := readWithTimeout(t, chA, 200*time.Millisecond); got != "hello-A" {
		t.Fatalf("chA received %q, want hello-A", got)
	}
	if got := readWithTimeout(t, chB, 200*time.Millisecond); got != "hello-B" {
		t.Fatalf("chB received %q, want hello-B", got)
	}

	// Drain in case of stray empty values; we only care that A did NOT
	// get hello-B and vice versa.
	select {
	case stray := <-chA:
		if stray == "hello-B" {
			t.Fatalf("chA leaked instance B's output: %q", stray)
		}
	case <-time.After(50 * time.Millisecond):
	}
	select {
	case stray := <-chB:
		if stray == "hello-A" {
			t.Fatalf("chB leaked instance A's output: %q", stray)
		}
	case <-time.After(50 * time.Millisecond):
	}
}

func TestSubscribeOutput_RequiresID(t *testing.T) {
	if _, _, err := SubscribeOutput(""); err == nil {
		t.Fatal("empty id should error, got nil")
	}
}

func TestSubscribeOutput_CancelCleansUp(t *testing.T) {
	const id = "inst-cancel"
	ch, cancel, err := SubscribeOutput(id)
	if err != nil {
		t.Fatalf("SubscribeOutput: %v", err)
	}
	cancel()

	subsMu.Lock()
	_, stillThere := subs[id]
	subsMu.Unlock()
	if stillThere {
		t.Fatalf("subs[%q] still present after cancel", id)
	}

	// ch must be closed by cancel.
	select {
	case _, ok := <-ch:
		if ok {
			t.Fatal("ch not closed after cancel")
		}
	case <-time.After(200 * time.Millisecond):
		t.Fatal("ch not closed within timeout")
	}
}

// TestBroadcast_NoSubscribersNoOp guards that broadcasting to an id
// with no subscriber never panics (it would have, in the global-map
// world where a nil deref was possible).
func TestBroadcast_NoSubscribersNoOp(t *testing.T) {
	defer func() {
		if r := recover(); r != nil {
			t.Fatalf("broadcast to id with no subs panicked: %v", r)
		}
	}()
	broadcast("nobody", "ignored")
	broadcast("", "ignored") // empty id also no-op
}

// TestBroadcast_Parallel guards no data race under concurrent
// subscribe / cancel / broadcast. Run with `go test -race`.
func TestBroadcast_Parallel(t *testing.T) {
	const id = "inst-par"
	var wg sync.WaitGroup
	wg.Add(3)
	go func() {
		defer wg.Done()
		for i := 0; i < 200; i++ {
			ch, cancel, err := SubscribeOutput(id)
			if err != nil {
				t.Errorf("SubscribeOutput: %v", err)
				return
			}
			broadcast(id, "x")
			cancel()
			_ = ch
		}
	}()
	go func() {
		defer wg.Done()
		for i := 0; i < 200; i++ {
			broadcast(id, "y")
		}
	}()
	go func() {
		defer wg.Done()
		for i := 0; i < 200; i++ {
			broadcast("other-id", "z")
		}
	}()
	wg.Wait()
}

func readWithTimeout(t *testing.T, ch <-chan string, d time.Duration) string {
	t.Helper()
	select {
	case s, ok := <-ch:
		if !ok {
			t.Fatal("channel closed unexpectedly")
		}
		return s
	case <-time.After(d):
		t.Fatalf("timeout after %s waiting for chunk", d)
		return ""
	}
}
