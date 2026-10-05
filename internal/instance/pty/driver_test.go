package pty

import (
	"fmt"
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

// TestDriver_ReadLogs_TailReturnsNewestBytes guards the issue #81
// regression: a negative since must return the newest bytes of the
// ring buffer, not the oldest.
func TestDriver_ReadLogs_TailReturnsNewestBytes(t *testing.T) {
	var sb strings.Builder
	for i := 0; i < 100; i++ {
		fmt.Fprintf(&sb, "line-%03d\n", i)
	}
	all := sb.String()

	buf := framework.NewRingBuffer(4096)
	buf.WriteString(all)

	d := Driver{}
	h := framework.NewHandle("pty", &Handle{buf: buf})

	body, off, err := d.ReadLogs(h, -1, 64)
	if err != nil {
		t.Fatalf("ReadLogs: %v", err)
	}
	if want := all[len(all)-64:]; body != want {
		t.Fatalf("tail body = %q, want newest 64 bytes %q", body, want)
	}
	if off != int64(len(all)) {
		t.Fatalf("cursor = %d, want end offset %d", off, len(all))
	}
}

// TestDriver_ReadLogs_SinceReadsIncremental pins the OTHER ReadLogs
// branch (since >= 0 → RingBuffer.ReadSince) next to the tail pin above,
// so the hand-written mirror in internal/app/tty_ws_test.go cannot
// silently drift from the real driver (issue #87 review): an incremental
// read returns [since, head) with the cursor advanced to head, a cursor
// already at head returns nothing with it unchanged, and a stale cursor
// silently clamps to the oldest live byte without erroring.
func TestDriver_ReadLogs_SinceReadsIncremental(t *testing.T) {
	var sb strings.Builder
	for i := 0; i < 100; i++ {
		fmt.Fprintf(&sb, "line-%03d\n", i)
	}
	all := sb.String() // 900 bytes

	d := Driver{}

	buf := framework.NewRingBuffer(4096)
	buf.WriteString(all)
	h := framework.NewHandle("pty", &Handle{buf: buf})

	body, next, err := d.ReadLogs(h, 300, 4096)
	if err != nil {
		t.Fatalf("ReadLogs: %v", err)
	}
	if body != all[300:] {
		t.Fatalf("incremental body = %d bytes, want all[300:] (%d bytes)", len(body), len(all)-300)
	}
	if next != int64(len(all)) {
		t.Fatalf("cursor = %d, want head %d", next, len(all))
	}

	// At head: nothing new, cursor unchanged (the poll-loop contract).
	body, next, err = d.ReadLogs(h, int64(len(all)), 4096)
	if err != nil {
		t.Fatalf("ReadLogs at head: %v", err)
	}
	if body != "" || next != int64(len(all)) {
		t.Fatalf("read at head = (%q, %d), want (\"\", %d)", body, next, len(all))
	}

	// Stale: a 64-byte ring holding the last 64 of the 900 bytes has
	// its oldest live byte at 836; since=10 clamps there, no error.
	small := framework.NewRingBuffer(64)
	small.WriteString(all)
	h2 := framework.NewHandle("pty", &Handle{buf: small})
	body, next, err = d.ReadLogs(h2, 10, 64*1024)
	if err != nil {
		t.Fatalf("ReadLogs stale since: %v", err)
	}
	if body != all[836:] {
		t.Fatalf("stale read = %d bytes, want the live bytes from 836 (%d bytes)", len(body), len(all)-836)
	}
	if next != int64(len(all)) {
		t.Fatalf("cursor after clamp = %d, want head %d", next, len(all))
	}
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

// liveSubscribers reports how many subscribers an id still holds. The
// overflow tests assert on this as well as on the channel, because "no
// silent loss" has two halves: the chunk must not be swallowed AND the
// subscriber that could not take it must not stay registered to be
// skipped by every later broadcast (issue #82).
func liveSubscribers(id string) int {
	subsMu.Lock()
	defer subsMu.Unlock()
	return len(subs[id])
}

// registeredInstanceCount reports how many instance ids currently hold a
// key in the top-level subs map — the registry level ABOVE liveSubscribers,
// which counts subscribers *within* one id and answers 0 just as happily for
// a stale empty set as for a removed key. Without this second lens the
// CHANGELOG claim that the overflow arm preserves the "key exists iff it has
// a live subscriber" invariant is unpinned: deleting
// `if len(subs[id]) == 0 { delete(subs, id) }` from broadcast's overflow
// arm leaves every per-id assertion green.
func registeredInstanceCount() int {
	subsMu.Lock()
	defer subsMu.Unlock()
	return len(subs)
}

// TestBroadcast_OverflowDisconnectsSubscriber pins the core of issue #82.
//
// Before the fix the `default:` arm was empty: once the 64-slot buffer
// filled — a backgrounded tab stops draining, its socket write blocks in
// rw.Flush on the hijacked connection, the select loop stops reading — every
// later chunk was discarded forever, with no counter, no signal and no
// disconnect, so the client's screen diverged irreparably from the real
// terminal (one missed clear-screen or cursor move corrupts everything
// rendered after it).
//
// Now the overflowing subscriber is removed, its channel closed AND its
// backlog drained at the source, which is what makes the WS handler tear the
// socket down. Asserted on observable behaviour: the consumer's very next
// receive is the close — not the up-to-64 queued chunks it never took — so
// the resync signal arrives without first flushing ~64 KB onto the socket
// that stalled. Discarding the backlog loses nothing: pumpLogs wrote every
// chunk to the ring buffer BEFORE broadcasting it, so the reconnect's replay
// from the client's #87 cursor delivers those bytes again.
func TestBroadcast_OverflowDisconnectsSubscriber(t *testing.T) {
	const id = "inst-overflow"

	// Registry-level baseline taken BEFORE subscribing; the assertions below
	// are deltas, so they hold regardless of what other ids exist.
	before := registeredInstanceCount()

	ch, cancel, err := SubscribeOutput(id)
	if err != nil {
		t.Fatalf("SubscribeOutput: %v", err)
	}
	// Also pins FIX-2 from the other side: this deferred cancel runs
	// against a channel the overflow already closed, and must not panic.
	defer cancel()

	if n := registeredInstanceCount(); n != before+1 {
		t.Fatalf("registered instance ids = %d right after subscribing %q, want %d", n, id, before+1)
	}

	capacity := cap(ch)
	for i := 0; i < capacity+5; i++ {
		broadcast(id, fmt.Sprintf("chunk-%03d", i))
	}

	select {
	case got, ok := <-ch:
		if ok {
			t.Fatalf("chunk %q was delivered after the overflow: the overflow arm must drain the closed subscriber's backlog at the source, so the consumer's next receive is the resync signal, not stale queue contents the WS handler would write onto the stalled socket", got)
		}
	case <-time.After(200 * time.Millisecond):
		t.Fatal("subscriber channel is still open after overflowing: it was neither disconnected nor closed")
	}

	if n := liveSubscribers(id); n != 0 {
		t.Fatalf("%d subscriber(s) still registered for %q after overflow: the failing subscriber must be removed, not kept and silently skipped", n, id)
	}

	// The len(subs) half of the invariant: the id key itself must leave the
	// top-level registry with its last subscriber. liveSubscribers(id) == 0
	// above cannot distinguish the removed key from a stale empty set, so
	// without this assertion the `delete(subs, id)` in the overflow arm —
	// and the CHANGELOG's claim about preserving that invariant — is
	// unpinned: removing the delete leaves every per-id check green.
	if n := registeredInstanceCount(); n != before {
		t.Fatalf("registered instance ids = %d after the overflow removed %q's last subscriber, want %d: the id key must leave the top-level registry, not linger over an empty subscriber set", n, id, before)
	}
}

// TestBroadcast_WithinCapacitySubscriberIsNeverDisconnected is the other
// half of the fix: disconnecting on overflow must not become disconnecting
// on sight. Two subscribers on one id, both filled to exactly capacity;
// then only the one that is still full when the next chunk arrives is
// dropped, while the drained one keeps streaming.
func TestBroadcast_WithinCapacitySubscriberIsNeverDisconnected(t *testing.T) {
	const id = "inst-within-capacity"

	baseline := registeredInstanceCount()

	fast, cancelFast, err := SubscribeOutput(id)
	if err != nil {
		t.Fatalf("SubscribeOutput(fast): %v", err)
	}
	defer cancelFast()

	slow, cancelSlow, err := SubscribeOutput(id)
	if err != nil {
		t.Fatalf("SubscribeOutput(slow): %v", err)
	}
	defer cancelSlow()

	capacity := cap(fast)
	if capacity != cap(slow) {
		t.Fatalf("subscriber capacities differ (%d, %d); the test assumes one shared size", capacity, cap(slow))
	}

	// Exactly capacity chunks: both buffers are now full, and no subscriber
	// may have been touched — a full buffer is not yet an overflow.
	for i := 0; i < capacity; i++ {
		broadcast(id, fmt.Sprintf("chunk-%03d", i))
	}
	if n := liveSubscribers(id); n != 2 {
		t.Fatalf("%d subscriber(s) registered after filling to exactly capacity, want both still live", n)
	}

	// Drain one; it now has room. The next chunk overflows the other.
	for i := 0; i < capacity; i++ {
		if got := readWithTimeout(t, fast, 200*time.Millisecond); got != fmt.Sprintf("chunk-%03d", i) {
			t.Fatalf("fast chunk %d = %q, want chunk-%03d", i, got, i)
		}
	}
	broadcast(id, "after-overflow")
	if got := readWithTimeout(t, fast, 200*time.Millisecond); got != "after-overflow" {
		t.Fatalf("fast subscriber got %q, want after-overflow: the peer's overflow must not disconnect a healthy subscriber", got)
	}

	// The dropped subscriber's backlog is DISCARDED at the source: the
	// overflow arm drains the closed channel inside subsMu, so the very
	// next receive is the close. That is safe — pumpLogs wrote every
	// dropped chunk to the ring buffer before broadcasting it, so the
	// client's cursor replay (issue #87) delivers those bytes again — and
	// it is the point: the WS handler must reach the 1013 teardown without
	// first flushing ~64 KB onto the socket that stalled.
	select {
	case got, ok := <-slow:
		if ok {
			t.Fatalf("slow subscriber got %q after the overflow, want the channel closed with its backlog discarded at the source", got)
		}
	case <-time.After(200 * time.Millisecond):
		t.Fatal("slow subscriber channel is still open, want it closed by the overflow")
	}
	if n := liveSubscribers(id); n != 1 {
		t.Fatalf("%d subscriber(s) registered, want exactly 1 (the healthy one)", n)
	}
	// The registry-level complement of the overflow test's assertion: the
	// id key STAYS in the top-level map while a healthy subscriber still
	// lives — dropping it early would deafen the survivor to every later
	// broadcast.
	if n := registeredInstanceCount(); n != baseline+1 {
		t.Fatalf("registered instance ids = %d while %q still has a healthy subscriber, want %d", n, id, baseline+1)
	}

	// The survivor keeps working after its peer was dropped.
	broadcast(id, "still-live")
	if got := readWithTimeout(t, fast, 200*time.Millisecond); got != "still-live" {
		t.Fatalf("surviving subscriber got %q, want still-live", got)
	}
}

// TestSubscribeOutput_CancelAfterOverflowDoesNotPanic is THE regression
// test for FIX-2. The channel now has two closers: broadcast closes an
// overflowing subscriber from inside subsMu, and the caller's cancel closes
// it too. cancel used to call close(ch) outside the lock, so an overflow
// followed by the WS handler's deferred cancel hit
//
//	panic: close of closed channel
//
// — in pumpLogs' case inside the PTY pump goroutine, which takes the whole
// daemon down. The channel now lives inside a subscriber value whose
// closeLocked does the check-and-close under subsMu, so the second closer
// is a no-op. Run under -race.
func TestSubscribeOutput_CancelAfterOverflowDoesNotPanic(t *testing.T) {
	const id = "inst-overflow-cancel"

	ch, cancel, err := SubscribeOutput(id)
	if err != nil {
		t.Fatalf("SubscribeOutput: %v", err)
	}

	capacity := cap(ch)
	for i := 0; i <= capacity; i++ {
		broadcast(id, fmt.Sprintf("chunk-%03d", i))
	}
	// Overflow happened: the overflow arm drained the backlog at the
	// source, so the channel reports closed on the very next receive.
	select {
	case _, ok := <-ch:
		if ok {
			t.Fatal("channel still delivering after overflowing its capacity")
		}
	case <-time.After(200 * time.Millisecond):
		t.Fatal("channel not closed by the overflow within timeout")
	}

	// Now the deferred cancel runs against an already-closed channel — and
	// twice, because callers are entitled to.
	func() {
		defer func() {
			if r := recover(); r != nil {
				t.Fatalf("cancel() after an overflow close panicked: %v", r)
			}
		}()
		cancel()
		cancel()
	}()
}

// TestSubscribeOutput_CancelRacesOverflow runs the two closers against each
// other from different goroutines instead of in a scripted order: one floods
// past capacity so broadcast closes, the other cancels. It pins the
// concurrent contract: no panic, and the subscriber is deregistered however
// the two goroutines interleave.
//
// What it does NOT prove, stated honestly: it is not a proof that exactly-
// once close rests on subsMu covering the flag check and the close. The
// two closeLocked calls can never run concurrently here regardless of the
// flag: broadcast holds subsMu across BOTH its map delete and its close,
// so once broadcast has deleted the entry, cancel cannot even reach its own
// delete until broadcast releases the lock. The serialisation that matters
// is the map delete under subsMu — a subscriber removed from the map can
// never be reached by broadcast again. (Moving cancel's closeLocked outside
// subsMu still passes this test under -race, which is exactly why the
// earlier wording here — "exactly-once rests entirely on subsMu covering
// both the check and the close", a flag read outside the lock "would show
// up here as a race report" — was wrong: it asserted something this test
// structurally cannot falsify.)
//
// The `closed` flag is load-bearing for the SEQUENTIAL cases instead —
// cancel after an overflow, and cancel called twice — where a naive
// close(sub.ch) in either closer panics with `close of closed channel`;
// those are pinned by TestSubscribeOutput_CancelAfterOverflowDoesNotPanic.
// A future refactor that kept the deletes but relaxed the locking would
// still pass this test, so read the locking contract from broadcast and
// cancel themselves, not from this test's greenness.
func TestSubscribeOutput_CancelRacesOverflow(t *testing.T) {
	const id = "inst-cancel-race"

	for i := 0; i < 100; i++ {
		ch, cancel, err := SubscribeOutput(id)
		if err != nil {
			t.Fatalf("SubscribeOutput: %v", err)
		}
		capacity := cap(ch)

		var wg sync.WaitGroup
		wg.Add(2)
		go func() {
			defer wg.Done()
			defer func() {
				if r := recover(); r != nil {
					t.Errorf("broadcast panicked: %v", r)
				}
			}()
			for j := 0; j < 2*capacity+10; j++ {
				broadcast(id, "y")
			}
		}()
		go func() {
			defer wg.Done()
			defer func() {
				if r := recover(); r != nil {
					t.Errorf("cancel panicked: %v", r)
				}
			}()
			cancel()
		}()
		wg.Wait()

		if n := liveSubscribers(id); n != 0 {
			t.Fatalf("%d subscriber(s) left registered for %q after cancel, want 0", n, id)
		}
	}
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
