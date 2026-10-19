package pty

import (
	"fmt"
	"os"
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
// that stalled. That "very next receive" is a property of THIS test, not of
// the mechanism: here the test goroutine is the channel's sole consumer and
// does not read during broadcast, so nothing competes with the drain. A real
// WS handler parked in its select is readied by close() and races the drain
// for the buffered values, and may legitimately take some of them. Discarding
// the backlog loses nothing: pumpLogs wrote every chunk to the ring buffer
// BEFORE broadcasting it, so the reconnect's replay from the client's #87
// cursor delivers those bytes again.
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

	// The next receive IS the close here, and the assertion below is what
	// pins the drain — but read the precondition with the claim: THIS test is
	// the channel's sole consumer and has not read a single value during the
	// broadcasts above, so nothing competes with broadcast's post-unlock drain.
	// That makes "the close is next" a property of this test's construction,
	// not of the mechanism. A real WS handler parked in its select is readied
	// by close() and races the drain for the buffered values, and may
	// legitimately win some of them; see broadcast in driver.go, which states
	// the guarantee as bounded (no full backlog on a stalled socket) rather
	// than ordered.
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

	// The dropped subscriber's backlog is DISCARDED at the source — but NOT
	// inside subsMu, and that placement is the whole point of broadcast's shape.
	// Under the lock broadcast only deletes the subscriber and closeLocked()s
	// its channel, collecting it into `closed` when that call is the one that
	// performed the close; the drain itself runs in broadcast's deferred func,
	// AFTER subsMu.Unlock(). It must, because `for range` over a channel
	// terminates only by virtue of that channel already being closed, and subsMu
	// is process-global: run the drain inside the critical section and one
	// future edit that drops or reorders the closeLocked() call — while keeping
	// the `delete`, which looks load-bearing and would survive — parks this
	// goroutine holding subsMu forever, silently freezing every other instance's
	// PTY output with no panic and no log. So the backlog is gone by the time
	// broadcast returns, and the next receive here IS the close — with the
	// precondition stated plainly, because it is the test's and not the
	// mechanism's: THIS test is that channel's only consumer and has not read
	// anything during the broadcast, so nothing competes with the drain. A real
	// WS handler parked in its select is readied by close() and races the drain
	// for the buffered values, and may legitimately win some of them (see
	// broadcast in driver.go: the guarantee is bounded, not ordered). Discarding
	// is safe because pumpLogs wrote every dropped chunk to the ring buffer
	// before broadcasting it, so the client's cursor replay (issue #87) delivers
	// those bytes again — everything STILL in the ring buffer — and it is the
	// point: the WS handler must reach the 1013 teardown without first flushing
	// ~64 KB onto the socket that stalled.
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
	// Overflow happened: the overflow arm drained the backlog at the source —
	// in broadcast's deferred func, after subsMu was released, never under it —
	// so the channel reports closed on the very next receive. That ordering is
	// again this test's, not the mechanism's: this test is the channel's sole
	// consumer and does not read during the broadcast, so nothing competes with
	// the drain; a real WS handler readied by close() races it and may
	// legitimately take some buffered values first.
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

// --- issue #114: OverflowDropOldest policy -----------------------------------

// pinDefaultDropQueueCapacity removes MYWORKTREE_TTY_DROP_QUEUE_CHUNKS from
// this test's environment so every drop subscriber it creates resolves to the
// compiled-in 4096 default (决策 1b).
//
// 评审留痕（issue #114 决策 1b 双评审 · 评审A nit · 已采纳）：capacity is
// resolved per subscription from the environment, so a value lingering in the
// runner's environment — a CI matrix export, a developer's shell, a sibling
// test that restored late — would drift these tests. Two kinds of drift, both
// real:
//
//   - assertion drift: TestBroadcast_DropOldestDoesNotDisturbClosePolicyPeers
//     asserts the drop queue is strictly BIGGER than the legacy 64, which a
//     residual "32" fails outright;
//   - footprint drift: these tests loop cap(ch) times, and
//     make(chan string, N) allocates N×16 B eagerly, so a residual 1e8 is a
//     1.6 GB buffer here — and 200 subscriptions' worth of them in
//     TestBroadcast_DropOldestParallel.
//
// The pattern is t.Setenv THEN os.Unsetenv, never Unsetenv alone: t.Setenv
// is what registers the cleanup restoring the runner's ORIGINAL value (testing
// tracks only the key it was handed), so the hygiene stays scoped to this test
// and cannot leak into the next; the Unsetenv then makes the variable genuinely
// ABSENT for the test body, which is the branch that answers the default — a
// set-but-empty value exercises a DIFFERENT branch (the Atoi failure), so
// pinning this way is not tautological.
//
// Tests that never size a queue need no pin (see
// TestDropStats_PairPublishedAsSingleAtomicUnit), and
// TestSubscribeOutputWithPolicy_DropQueueCapacityEnvOverride must NOT call
// this: the env value is that test's input.
func pinDefaultDropQueueCapacity(t *testing.T) {
	t.Helper()
	t.Setenv(envSubscriberQueueChunksDrop, "unused")
	if err := os.Unsetenv(envSubscriberQueueChunksDrop); err != nil {
		t.Fatalf("unset %s: %v", envSubscriberQueueChunksDrop, err)
	}
}

// TestSubscribeOutputWithPolicy_DropOldestKeepsNewestAndCounts pins the
// core of the drop-oldest contract: the overflowing subscriber STAYS
// registered and streaming (the channel never closes, the registry entry
// survives), it keeps the NEWEST chunks (the queue's head is the first
// survivor, not the first broadcast), and every discarded chunk is COUNTED
// — the counts are what the app layer turns into the in-band overflow
// notice, so an uncounted drop here is the silent corruption issue #82
// condemned, re-introduced under a new name.
func TestSubscribeOutputWithPolicy_DropOldestKeepsNewestAndCounts(t *testing.T) {
	const id = "inst-drop-oldest"

	// Needs the pin: the assertions here are cap-relative (capacity+5
	// broadcasts must drop exactly 5), so they survive any capacity — but
	// that is why they then LOOP capacity+5 times, and the queue is allocated
	// eagerly, so a residual env value silently decides this test's footprint.
	pinDefaultDropQueueCapacity(t)

	baseline := registeredInstanceCount()

	ch, cancel, stats, err := SubscribeOutputWithPolicy(id, OverflowDropOldest)
	if err != nil {
		t.Fatalf("SubscribeOutputWithPolicy: %v", err)
	}
	defer cancel()

	if stats == nil {
		t.Fatal("DropStats handle is nil; the API promises a non-nil zero handle")
	}

	capacity := cap(ch)
	chunkLen := len("chunk-000")
	for i := 0; i < capacity+5; i++ {
		broadcast(id, fmt.Sprintf("chunk-%03d", i))
	}

	// Overflowed, and STILL the picture: registered, open, counted.
	if n := liveSubscribers(id); n != 1 {
		t.Fatalf("%d subscriber(s) registered after a drop-oldest overflow, want 1: the subscriber must be kept, not deleted", n)
	}
	if n := registeredInstanceCount(); n != baseline+1 {
		t.Fatalf("registered instance ids = %d, want %d: the id key must stay while the drop subscriber lives", n, baseline+1)
	}
	if got := stats.Chunks(); got != 5 {
		t.Fatalf("dropped chunks = %d, want 5 (the five oldest did not fit)", got)
	}
	if want := int64(5 * chunkLen); stats.Bytes() != want {
		t.Fatalf("dropped bytes = %d, want %d", stats.Bytes(), want)
	}

	// The survivor window is the NEWEST capacity chunks: chunk-000..004
	// are gone, chunk-005 leads. Reading them out is also the proof the
	// channel is alive — a close here would be the old #82 teardown.
	for i := 5; i < capacity+5; i++ {
		if got := readWithTimeout(t, ch, 200*time.Millisecond); got != fmt.Sprintf("chunk-%03d", i) {
			t.Fatalf("survivor chunk %d = %q, want chunk-%03d (drop must evict from the HEAD, keeping the newest)", i-5, got, i)
		}
	}

	// And it keeps streaming past its own overflow.
	broadcast(id, "live")
	if got := readWithTimeout(t, ch, 200*time.Millisecond); got != "live" {
		t.Fatalf("post-overflow chunk = %q, want live", got)
	}
	if got := stats.Chunks(); got != 5 {
		t.Fatalf("dropped chunks grew to %d after a non-overflowing broadcast, want 5", got)
	}
}

// TestSubscribeOutputWithPolicy_DropOldestCancelIsIdempotent pins that the
// new policy does not loosen the close contract: cancel is the ONLY closer
// for a drop subscriber (broadcast never closes it), and like
// SubscribeOutput's it is exactly-once and safe to call twice — here once
// after a live overflow, where a naive second close would panic.
func TestSubscribeOutputWithPolicy_DropOldestCancelIsIdempotent(t *testing.T) {
	const id = "inst-drop-cancel"

	// Needs the pin: "chunk-005 is the head" holds at ANY capacity (a
	// capacity+5 flood always evicts exactly 5), so what a residual env value
	// actually decides here is how many chunks get allocated and then dropped
	// by the drain after cancel — the footprint, not the verdict.
	pinDefaultDropQueueCapacity(t)

	baseline := registeredInstanceCount()

	ch, cancel, stats, err := SubscribeOutputWithPolicy(id, OverflowDropOldest)
	if err != nil {
		t.Fatalf("SubscribeOutputWithPolicy: %v", err)
	}

	capacity := cap(ch)
	for i := 0; i < capacity+5; i++ {
		broadcast(id, fmt.Sprintf("chunk-%03d", i))
	}
	if stats.Chunks() == 0 {
		t.Fatal("no drops counted after overflowing capacity+5 broadcasts")
	}

	// The channel survived its overflow (the pre-cancel receive below is
	// proof by progress: it returns a chunk, not a close).
	if got := readWithTimeout(t, ch, 200*time.Millisecond); got != "chunk-005" {
		t.Fatalf("first survivor = %q, want chunk-005", got)
	}

	func() {
		defer func() {
			if r := recover(); r != nil {
				t.Fatalf("cancel panicked after a drop-oldest overflow: %v", r)
			}
		}()
		cancel()
		cancel()
	}()

	select {
	case _, ok := <-ch:
		if ok {
			// Buffered leftovers are legal on a cancelled channel (cancel
			// is the polite unsubscribe, it does not drain); drain to the
			// close.
			for range ch {
			}
		}
	case <-time.After(200 * time.Millisecond):
		t.Fatal("channel not closed by cancel within timeout")
	}

	if n := liveSubscribers(id); n != 0 {
		t.Fatalf("%d subscriber(s) registered after cancel, want 0", n)
	}
	if n := registeredInstanceCount(); n != baseline {
		t.Fatalf("registered instance ids = %d after cancelling the last subscriber, want %d", n, baseline)
	}
}

// TestSubscribeOutputWithPolicy_ClosePolicyMatchesSubscribeOutput pins that
// the new API with its default policy is today's behaviour: full queue →
// delete + close + drain, zero counts (the close path must never touch
// stats — a counted close would double-report loss to a client that gets a
// 1013 instead), channel closed on the next receive, cancel idempotent
// afterwards.
func TestSubscribeOutputWithPolicy_ClosePolicyMatchesSubscribeOutput(t *testing.T) {
	const id = "inst-policy-close"

	baseline := registeredInstanceCount()

	ch, cancel, stats, err := SubscribeOutputWithPolicy(id, OverflowClose)
	if err != nil {
		t.Fatalf("SubscribeOutputWithPolicy: %v", err)
	}
	defer cancel()

	capacity := cap(ch)
	for i := 0; i < capacity+5; i++ {
		broadcast(id, fmt.Sprintf("chunk-%03d", i))
	}

	select {
	case got, ok := <-ch:
		if ok {
			t.Fatalf("chunk %q delivered after a close-policy overflow: the overflow arm must still delete+close+drain this subscriber (the drain is pinned here by this being the channel's only consumer, same precondition as TestBroadcast_OverflowDisconnectsSubscriber)", got)
		}
	case <-time.After(200 * time.Millisecond):
		t.Fatal("channel still open after a close-policy overflow")
	}

	if n := liveSubscribers(id); n != 0 {
		t.Fatalf("%d subscriber(s) registered after a close-policy overflow, want 0", n)
	}
	if n := registeredInstanceCount(); n != baseline {
		t.Fatalf("registered instance ids = %d, want %d: the close path keeps the top-level invariant", n, baseline)
	}
	if stats.Chunks() != 0 || stats.Bytes() != 0 {
		t.Fatalf("close-policy stats = (%d, %d), want (0, 0): loss here is signalled by the close, not by counts", stats.Chunks(), stats.Bytes())
	}
}

// TestBroadcast_DropOldestDoesNotDisturbClosePolicyPeers pins the mixed
// registry the negotiation produces: one client with the overflow-notice
// cap and one without, on the same instance. The cap-less peer must still
// be deleted, closed and drained exactly as issue #82 shipped it, while
// the cap peer keeps streaming with the loss counted on ITS stats only —
// per-subscriber policy, per-subscriber accounting, no cross-talk.
func TestBroadcast_DropOldestDoesNotDisturbClosePolicyPeers(t *testing.T) {
	const id = "inst-mixed-policy"

	// Needs the pin the most: this test's precondition is an ASSERTION about
	// the drop capacity (strictly greater than the legacy 64, asserted below),
	// so a residual MYWORKTREE_TTY_DROP_QUEUE_CHUNKS="32" in the runner's
	// environment fails the test for a reason that has nothing to do with the
	// code under test. The legacy side is env-proof by construction and needs
	// no pin of its own.
	pinDefaultDropQueueCapacity(t)

	drop, cancelDrop, stats, err := SubscribeOutputWithPolicy(id, OverflowDropOldest)
	if err != nil {
		t.Fatalf("SubscribeOutputWithPolicy(drop): %v", err)
	}
	defer cancelDrop()

	legacy, cancelLegacy, err := SubscribeOutput(id)
	if err != nil {
		t.Fatalf("SubscribeOutput(legacy): %v", err)
	}
	defer cancelLegacy()

	// 决策 1b split the two capacities ON PURPOSE, so this test's original
	// "one shared size" precondition is now the very thing that must NOT
	// hold: the cap peer rides the 4096-chunk queue, while the legacy peer
	// keeps issue #82's 64 verbatim — unmoved by the new policy living next
	// door, and unmovable by MYWORKTREE_TTY_DROP_QUEUE_CHUNKS too. Asserted
	// as literals, because both numbers ARE the contract (#82's 64 and
	// 决策 1b's floor above it), not an implementation detail to echo back.
	legacyCapacity := cap(legacy)
	if legacyCapacity != 64 {
		t.Fatalf("legacy peer capacity = %d, want 64: issue #82's close-policy queue size stays verbatim whatever the drop policy gets", legacyCapacity)
	}
	capacity := cap(drop)
	if capacity <= legacyCapacity {
		t.Fatalf("drop peer capacity = %d, want strictly more than the legacy peer's %d (决策 1b)", capacity, legacyCapacity)
	}

	// Fill to the LARGER capacity plus one chunk: the legacy peer overflows
	// first — on the 65th chunk, one past its own 64 — while the drop peer
	// only overflows on the very last broadcast, so one stream exercises the
	// two policies at two different thresholds and each peer's teardown is
	// still decided by ITS OWN queue, never by the neighbour's size.
	for i := 0; i <= capacity; i++ {
		broadcast(id, fmt.Sprintf("chunk-%03d", i))
	}

	// Legacy: closed, deregistered, backlog drained at the source — the
	// #82 shape, unchanged by the new policy living next door.
	select {
	case got, ok := <-legacy:
		if ok {
			t.Fatalf("legacy peer got %q after overflow, want the closed channel (delete+close+drain must stay verbatim for it)", got)
		}
	case <-time.After(200 * time.Millisecond):
		t.Fatal("legacy peer channel still open after overflow")
	}

	// Drop peer: registered, open, one counted eviction, newest window.
	if n := liveSubscribers(id); n != 1 {
		t.Fatalf("%d subscriber(s) registered, want exactly 1 (the drop peer)", n)
	}
	if got := stats.Chunks(); got != 1 {
		t.Fatalf("drop peer dropped chunks = %d, want 1", got)
	}
	if got := readWithTimeout(t, drop, 200*time.Millisecond); got != "chunk-001" {
		t.Fatalf("drop peer first survivor = %q, want chunk-001 (chunk-000 was the eviction)", got)
	}
	// Drain the rest of the survivor window (chunk-002..chunk-<capacity>)
	// so the next receive is unambiguously the post-overflow chunk.
	for i := 2; i <= capacity; i++ {
		if got := readWithTimeout(t, drop, 200*time.Millisecond); got != fmt.Sprintf("chunk-%03d", i) {
			t.Fatalf("drop peer survivor %d = %q, want chunk-%03d", i, got, i)
		}
	}
	broadcast(id, "still-live")
	if got := readWithTimeout(t, drop, 200*time.Millisecond); got != "still-live" {
		t.Fatalf("drop peer post-overflow chunk = %q, want still-live", got)
	}
}

// --- issue #114 决策 1b: queue capacity per overflow policy --------------

// TestSubscribeOutputWithPolicy_QueueCapacityByPolicy pins the capacity
// split itself (FIX-PLAN 决策 1b): a drop-policy subscriber gets 4096
// chunks — 4096 × 1024 B = 4,194,304 B ≈ 4.19 MB (10^6 口径, 习称 4MB),
// the precise per-subscriber upper bound, exact only because pumpLogs' read
// is 1024 bytes — while BOTH close-policy routes keep issue #82's 64:
// SubscribeOutputWithPolicy(id, OverflowClose) and the untouched
// SubscribeOutput. The numbers are asserted as literals, not as the consts
// they come from, because they ARE the contract: 决策 1b's whole argument is
// the gap arithmetic (burst 150–358 KB/s − median drain 47 KB/s ≈ 103–311
// KB/s) that turns 4 MB into a ~13–39 s reset cadence and 64 KB into
// ~0.2–0.6 s, so a silent change of either constant must fail here loudly.
func TestSubscribeOutputWithPolicy_QueueCapacityByPolicy(t *testing.T) {
	const id = "inst-capacity-by-policy"

	// This test pins the COMPILED-IN defaults, so the env has to be genuinely
	// ABSENT — see pinDefaultDropQueueCapacity for the 评审留痕 and for why
	// absent is a different branch from set-but-empty (the empty spelling is
	// pinned in the override table below).
	pinDefaultDropQueueCapacity(t)

	// The env NAME is operator-facing contract too, and the pin above reaches
	// it through the const — so the literal is spelled once here, which is
	// what makes that pin non-tautological: renaming the const to some other
	// variable now fails loudly instead of quietly pinning nothing.
	if envSubscriberQueueChunksDrop != "MYWORKTREE_TTY_DROP_QUEUE_CHUNKS" {
		t.Fatalf("env name = %q, want MYWORKTREE_TTY_DROP_QUEUE_CHUNKS", envSubscriberQueueChunksDrop)
	}
	if got := subscriberQueueChunksDrop(); got != 4096 {
		t.Fatalf("subscriberQueueChunksDrop() with the env absent = %d, want the 4096 default", got)
	}

	baseline := registeredInstanceCount()

	drop, cancelDrop, _, err := SubscribeOutputWithPolicy(id, OverflowDropOldest)
	if err != nil {
		t.Fatalf("SubscribeOutputWithPolicy(drop): %v", err)
	}
	defer cancelDrop()

	policyClose, cancelClose, _, err := SubscribeOutputWithPolicy(id, OverflowClose)
	if err != nil {
		t.Fatalf("SubscribeOutputWithPolicy(close): %v", err)
	}
	defer cancelClose()

	legacy, cancelLegacy, err := SubscribeOutput(id)
	if err != nil {
		t.Fatalf("SubscribeOutput: %v", err)
	}
	defer cancelLegacy()

	if got := cap(drop); got != 4096 {
		t.Fatalf("OverflowDropOldest capacity = %d, want 4096 (决策 1b: ~4 MB of 1024-byte chunks per subscriber)", got)
	}
	if got := cap(policyClose); got != 64 {
		t.Fatalf("OverflowClose capacity = %d, want 64: issue #82's queue size is verbatim", got)
	}
	if got := cap(legacy); got != 64 {
		t.Fatalf("SubscribeOutput capacity = %d, want 64: the legacy route is untouched by 决策 1b", got)
	}
	if n := liveSubscribers(id); n != 3 {
		t.Fatalf("%d subscriber(s) registered, want all 3 (capacity is not a reason to deregister)", n)
	}
	if n := registeredInstanceCount(); n != baseline+1 {
		t.Fatalf("registered instance ids = %d, want %d", n, baseline+1)
	}
}

// TestSubscribeOutputWithPolicy_DropQueueCapacityEnvOverride pins the env
// knob and, in the same table, the half that must NOT move: 64 for both
// close-policy routes, whatever the environment says. The resolution rule is
// "an integer > 0 wins, anything else falls back to 4096" — missing, empty,
// unparsable, zero and negative all take the default, and the parse is
// strict (no trimming), so " 128" is an unparsable value rather than a
// silently accepted 128: an operator typo falls back visibly-to-zero-effect
// instead of half-applying.
//
// It also pins WHEN the value is read: per subscription, as a snapshot.
// Changing the env afterwards never resizes a queue that already exists —
// a channel's capacity is fixed at make — so the knob takes effect on the
// next connection's subscription, which is exactly why an env read per
// subscription is affordable (connections are rare next to chunks) and why
// no restart is needed.
func TestSubscribeOutputWithPolicy_DropQueueCapacityEnvOverride(t *testing.T) {
	const id = "inst-capacity-env"

	cases := []struct {
		name string
		env  string
		want int
	}{
		{"valid override", "128", 128},
		{"valid small", "1", 1},
		{"unparsable", "abc", 4096},
		{"zero", "0", 4096},
		{"negative", "-5", 4096},
		{"empty", "", 4096},
		{"whitespace padded is unparsable", " 128", 4096},
		{"float is unparsable", "4.5", 4096},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv(envSubscriberQueueChunksDrop, tc.env)

			drop, cancelDrop, _, err := SubscribeOutputWithPolicy(id, OverflowDropOldest)
			if err != nil {
				t.Fatalf("SubscribeOutputWithPolicy(drop): %v", err)
			}
			defer cancelDrop()

			if got := cap(drop); got != tc.want {
				t.Fatalf("OverflowDropOldest capacity with %s=%q = %d, want %d", envSubscriberQueueChunksDrop, tc.env, got, tc.want)
			}

			// The close-policy routes are env-proof, and this is asserted on
			// EVERY row — including the valid overrides — because the risk
			// this guards is a future "unify the capacity" refactor that
			// quietly moves a #82 number.
			policyClose, cancelClose, _, err := SubscribeOutputWithPolicy(id, OverflowClose)
			if err != nil {
				t.Fatalf("SubscribeOutputWithPolicy(close): %v", err)
			}
			defer cancelClose()
			if got := cap(policyClose); got != 64 {
				t.Fatalf("OverflowClose capacity with %s=%q = %d, want 64: the env reaches the drop policy only", envSubscriberQueueChunksDrop, tc.env, got)
			}

			legacy, cancelLegacy, err := SubscribeOutput(id)
			if err != nil {
				t.Fatalf("SubscribeOutput: %v", err)
			}
			defer cancelLegacy()
			if got := cap(legacy); got != 64 {
				t.Fatalf("SubscribeOutput capacity with %s=%q = %d, want 64: the legacy route is untouched", envSubscriberQueueChunksDrop, tc.env, got)
			}
		})
	}

	t.Run("snapshot per subscription", func(t *testing.T) {
		t.Setenv(envSubscriberQueueChunksDrop, "128")
		first, cancelFirst, _, err := SubscribeOutputWithPolicy(id, OverflowDropOldest)
		if err != nil {
			t.Fatalf("SubscribeOutputWithPolicy(first): %v", err)
		}
		defer cancelFirst()

		t.Setenv(envSubscriberQueueChunksDrop, "256")
		second, cancelSecond, _, err := SubscribeOutputWithPolicy(id, OverflowDropOldest)
		if err != nil {
			t.Fatalf("SubscribeOutputWithPolicy(second): %v", err)
		}
		defer cancelSecond()

		if got := cap(first); got != 128 {
			t.Fatalf("pre-existing subscriber capacity = %d, want 128: capacity is a make-time snapshot and a later env change must not be able to resize it", got)
		}
		if got := cap(second); got != 256 {
			t.Fatalf("post-change subscriber capacity = %d, want 256: the next subscription picks up the new value with no restart", got)
		}
	})
}

// TestSubscribeOutputWithPolicy_DropOldestCapacityBoundary4096 is the
// capacity-boundary test 决策 1b replaced the v2 "1024 chunk" residue with:
// the queue absorbs EXACTLY 4096 chunks with nothing dropped — stats prove
// every send landed, which is also the proof the buffer really is ≥4096 —
// and the 4097th evicts exactly ONE chunk, the OLDEST, counted in both
// chunks and bytes, with the channel alive and the head now the SECOND
// chunk. That head position is the eviction's fingerprint: any off-by-one in
// the drop arm, or an eviction of the incoming chunk instead of the queued
// one, shows up here as a wrong survivor or a wrong count, and a wrong count
// is the silent loss issue #82 condemned.
func TestSubscribeOutputWithPolicy_DropOldestCapacityBoundary4096(t *testing.T) {
	const id = "inst-drop-boundary-4096"

	// The pin is load-bearing here, not hygiene for its own sake: every
	// number in this test is the 4096 boundary, so an override in the runner's
	// environment would move the boundary the test is standing on.
	pinDefaultDropQueueCapacity(t)

	baseline := registeredInstanceCount()

	ch, cancel, stats, err := SubscribeOutputWithPolicy(id, OverflowDropOldest)
	if err != nil {
		t.Fatalf("SubscribeOutputWithPolicy: %v", err)
	}
	defer cancel()

	capacity := cap(ch)
	if capacity != 4096 {
		t.Fatalf("capacity = %d, want 4096 before the boundary is exercised", capacity)
	}
	// Fixed-width names so every chunk has the same length and the evicted
	// one is identifiable by bytes as well as by position.
	const chunkLen = len("chunk-0000")

	// Fill to EXACTLY capacity: a full queue is not yet an overflow.
	for i := 0; i < capacity; i++ {
		broadcast(id, fmt.Sprintf("chunk-%04d", i))
	}
	if got := stats.Chunks(); got != 0 {
		t.Fatalf("dropped chunks = %d after exactly %d broadcasts, want 0: a full queue must not evict before the next chunk arrives", got, capacity)
	}
	if got := stats.Bytes(); got != 0 {
		t.Fatalf("dropped bytes = %d after exactly %d broadcasts, want 0", got, capacity)
	}
	if n := liveSubscribers(id); n != 1 {
		t.Fatalf("%d subscriber(s) registered at exactly capacity, want 1", n)
	}

	// One chunk more: the boundary. One eviction, the oldest, counted.
	broadcast(id, fmt.Sprintf("chunk-%04d", capacity))
	if got := stats.Chunks(); got != 1 {
		t.Fatalf("dropped chunks = %d on chunk %d, want exactly 1", got, capacity)
	}
	if got := stats.Bytes(); got != int64(chunkLen) {
		t.Fatalf("dropped bytes = %d, want %d (the single evicted chunk chunk-0000)", got, chunkLen)
	}
	if n := liveSubscribers(id); n != 1 {
		t.Fatalf("%d subscriber(s) registered after the boundary overflow, want 1: the subscriber survives, un-deleted", n)
	}

	// The channel is alive and the head is the SECOND chunk — chunk-0000 is
	// the one that was evicted, and the NEW chunk is at the tail.
	select {
	case got, ok := <-ch:
		if !ok {
			t.Fatal("drop-policy channel closed at the capacity boundary: only cancel may close it")
		}
		if got != "chunk-0001" {
			t.Fatalf("head after the boundary overflow = %q, want chunk-0001 (chunk-0000 was the eviction)", got)
		}
	case <-time.After(200 * time.Millisecond):
		t.Fatal("drop-policy channel empty at the capacity boundary, want the survivor window")
	}

	// Drain the rest of the survivor window (chunk-0002..chunk-<capacity>)
	// in order: the eviction took one slot from the head and nothing else.
	for i := 2; i <= capacity; i++ {
		if got := readWithTimeout(t, ch, 200*time.Millisecond); got != fmt.Sprintf("chunk-%04d", i) {
			t.Fatalf("survivor %d = %q, want chunk-%04d", i, got, i)
		}
	}

	// With a slot free again the next chunk lands without evicting, and the
	// counter stays at 1 — the accounting must not drift as the queue drains.
	broadcast(id, "after-drain")
	if got := readWithTimeout(t, ch, 200*time.Millisecond); got != "after-drain" {
		t.Fatalf("chunk after the drain = %q, want after-drain", got)
	}
	if got := stats.Chunks(); got != 1 {
		t.Fatalf("dropped chunks = %d after a non-overflowing broadcast, want 1", got)
	}
	if n := registeredInstanceCount(); n != baseline+1 {
		t.Fatalf("registered instance ids = %d, want %d: the id key lives while its drop subscriber lives", n, baseline+1)
	}
}

func TestSubscribeOutputWithPolicy_RequiresID(t *testing.T) {
	if _, _, _, err := SubscribeOutputWithPolicy("", OverflowDropOldest); err == nil {
		t.Fatal("empty id should error, got nil")
	}
}

// TestDropStats_PairPublishedAsSingleAtomicUnit pins the issue #114
// review fix: the two counters are ONE atomic unit, not two counters that
// a producer's Add can tear between. The discriminator is arithmetic —
// every Add carries exactly L bytes per chunk, so bytes == chunks × L
// holds at EVERY instant of the correct implementation, and a reader of a
// two-counter design (chunks.Add then bytes.Add) catches the window where
// chunks has advanced while bytes still lags: bytes != chunks × L. Run
// under -race with concurrent producers hammering the exported Add directly
// (the contract app-side doubles rely on: producers outside broadcast's
// subsMu must keep the totals exact too, hence the CAS loop).
func TestDropStats_PairPublishedAsSingleAtomicUnit(t *testing.T) {
	// Deliberately NOT pinned (checked per the 评审A nit): this test never
	// subscribes and never sizes a queue — it drives a standalone
	// &DropStats{} directly, so the drop capacity, and therefore
	// MYWORKTREE_TTY_DROP_QUEUE_CHUNKS, plays no role in any of its
	// assertions. Adding the pin here would only suggest a dependency that
	// does not exist.
	const L = 64

	// Zero value is ready to use: (0, 0) through every accessor, 0×L==0.
	stats := &DropStats{}
	if c, b := stats.Snapshot(); c != 0 || b != 0 {
		t.Fatalf("zero-value Snapshot = (%d, %d), want (0, 0)", c, b)
	}
	if stats.Chunks() != 0 || stats.Bytes() != 0 {
		t.Fatalf("zero-value accessors = (%d, %d), want (0, 0)", stats.Chunks(), stats.Bytes())
	}

	var wg sync.WaitGroup
	stop := make(chan struct{})
	const producers = 4
	const addsPerProducer = 2000

	wg.Add(producers)
	for p := 0; p < producers; p++ {
		go func() {
			defer wg.Done()
			for i := 0; i < addsPerProducer; i++ {
				stats.Add(1, L)
			}
		}()
	}
	// Readers hammer the pair while producers Add: every observed
	// Snapshot must satisfy the producer's arithmetic invariant. This is
	// the regression that the pre-fix two-atomic-counter layout fails —
	// its torn window (chunks ahead of bytes) violates bytes == chunks×L
	// every time a producer lands an Add between the two loads.
	wg.Add(1)
	go func() {
		defer wg.Done()
		for {
			select {
			case <-stop:
				return
			default:
			}
			c, b := stats.Snapshot()
			if b != c*L {
				t.Errorf("torn pair: chunks=%d bytes=%d violates bytes == chunks×%d", c, b, L)
				return
			}
		}
	}()

	// A second reader walks the tearing DIRECTION the fix eliminated
	// through the single-counter conveniences: Chunks() then Bytes() are
	// two loads, so bytes may legitimately be AHEAD of chunks×L (later
	// whole pairs land in between), but bytes must never LAG — lagging is
	// the old two-counter tear (chunks.Add lands, reader loads chunks,
	// bytes.Add has not landed yet → bytes < chunks×L), and with the pair
	// published as one atomic unit every load can only straddle WHOLE
	// pairs, so it is unreachable.
	wg.Add(1)
	go func() {
		defer wg.Done()
		for {
			select {
			case <-stop:
				return
			default:
			}
			c, b := stats.Chunks(), stats.Bytes()
			if b < c*L {
				t.Errorf("torn pair: chunks=%d bytes=%d — bytes lag chunks×%d, the two-counter tear the atomic pair eliminates", c, b, L)
				return
			}
		}
	}()

	go func() {
		time.Sleep(50 * time.Millisecond)
		close(stop)
	}()
	wg.Wait()

	c, b := stats.Snapshot()
	if want := int64(producers * addsPerProducer); c != want {
		t.Fatalf("final chunks = %d, want %d: the CAS loop must not lose an Add", c, want)
	}
	if b != c*L {
		t.Fatalf("final pair = (%d, %d), want bytes == chunks×%d", c, b, L)
	}
}

// TestBroadcast_DropOldestParallel is the drop-policy counterpart of
// TestBroadcast_Parallel: subscribe / broadcast / cancel from racing
// goroutines, now with the stats counters live in the critical section.
// Run under -race; the atomic counters must not tear and the exactly-once
// close (cancel is the only closer here) must not panic.
func TestBroadcast_DropOldestParallel(t *testing.T) {
	const id = "inst-drop-par"

	// Needs the pin: no assertion here reads the capacity, but this test makes
	// 400 drop subscriptions and each allocates its channel buffer eagerly —
	// ~64 KB each at the default, ~1.6 GB each on a residual
	// MYWORKTREE_TTY_DROP_QUEUE_CHUNKS="100000000", which would surface as an
	// OOM blamed on the race detector.
	pinDefaultDropQueueCapacity(t)

	var wg sync.WaitGroup
	wg.Add(3)
	go func() {
		defer wg.Done()
		for i := 0; i < 200; i++ {
			_, cancel, stats, err := SubscribeOutputWithPolicy(id, OverflowDropOldest)
			if err != nil {
				t.Errorf("SubscribeOutputWithPolicy: %v", err)
				return
			}
			broadcast(id, "x")
			cancel()
			_ = stats.Chunks()
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
			_, cancel, stats, err := SubscribeOutputWithPolicy(id, OverflowDropOldest)
			if err != nil {
				t.Errorf("SubscribeOutputWithPolicy: %v", err)
				return
			}
			broadcast(id, "z")
			cancel()
			_ = stats.Bytes()
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
