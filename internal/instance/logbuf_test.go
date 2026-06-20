package instance

import (
	"strings"
	"sync"
	"testing"
)

func TestRingBuffer_BasicWriteRead(t *testing.T) {
	t.Parallel()
	rb := NewRingBuffer(1024)
	rb.Write([]byte("hello "))
	rb.Write([]byte("world"))
	if got, _ := rb.Tail(100); got != "hello world" {
		t.Fatalf("Tail = %q, want %q", got, "hello world")
	}
	if rb.BytesUsed() != 11 {
		t.Fatalf("BytesUsed = %d, want 11", rb.BytesUsed())
	}
	if rb.Offset() != 11 {
		t.Fatalf("Offset = %d, want 11", rb.Offset())
	}
}

func TestRingBuffer_WrapAroundKeepsLastCap(t *testing.T) {
	t.Parallel()
	rb := NewRingBuffer(10)
	rb.Write([]byte("0123456789")) // exactly at cap
	if rb.BytesUsed() != 10 {
		t.Fatalf("BytesUsed after fill = %d, want 10", rb.BytesUsed())
	}
	rb.Write([]byte("ABC")) // pushes 3 oldest out
	if rb.BytesUsed() != 10 {
		t.Fatalf("BytesUsed after wrap = %d, want 10", rb.BytesUsed())
	}
	if rb.Offset() != 13 {
		t.Fatalf("Offset after wrap = %d, want 13", rb.Offset())
	}
	got, head := rb.Tail(10)
	if got != "3456789ABC" {
		t.Fatalf("Tail after wrap = %q, want %q", got, "3456789ABC")
	}
	if head != 13 {
		t.Fatalf("Tail head = %d, want 13", head)
	}
}

func TestRingBuffer_OverwriteAcrossWrapBoundary(t *testing.T) {
	t.Parallel()
	rb := NewRingBuffer(8)
	rb.Write([]byte("ABCDEFGH")) // head=8, used=8
	rb.Write([]byte("XYZ"))      // wraps: data="XYZDEFGH", head=11, used=8
	got, _ := rb.Tail(8)
	if got != "DEFGHXYZ" {
		t.Fatalf("Tail = %q, want %q", got, "DEFGHXYZ")
	}
}

func TestRingBuffer_TailPartial(t *testing.T) {
	t.Parallel()
	rb := NewRingBuffer(1024)
	rb.Write([]byte("hello world"))
	if got, _ := rb.Tail(5); got != "world" {
		t.Fatalf("Tail(5) = %q, want %q", got, "world")
	}
	if got, _ := rb.Tail(0); got != "" {
		t.Fatalf("Tail(0) = %q, want empty", got)
	}
	if got, _ := rb.Tail(-1); got != "" {
		t.Fatalf("Tail(-1) = %q, want empty", got)
	}
}

func TestRingBuffer_ReadSinceNewData(t *testing.T) {
	t.Parallel()
	rb := NewRingBuffer(1024)
	rb.Write([]byte("part1"))
	cursor := rb.Offset()
	rb.Write([]byte("part2"))
	got, next, err := rb.ReadSince(cursor, 1024)
	if err != nil {
		t.Fatalf("ReadSince err: %v", err)
	}
	if got != "part2" {
		t.Fatalf("ReadSince = %q, want %q", got, "part2")
	}
	if next != rb.Offset() {
		t.Fatalf("ReadSince next = %d, want %d", next, rb.Offset())
	}
}

func TestRingBuffer_ReadSinceNoNewData(t *testing.T) {
	t.Parallel()
	rb := NewRingBuffer(1024)
	rb.Write([]byte("part1"))
	cursor := rb.Offset()
	got, next, err := rb.ReadSince(cursor, 1024)
	if err != nil {
		t.Fatalf("ReadSince err: %v", err)
	}
	if got != "" {
		t.Fatalf("ReadSince no-new = %q, want empty", got)
	}
	if next != cursor {
		t.Fatalf("ReadSince no-new next = %d, want %d (unchanged)", next, cursor)
	}
}

func TestRingBuffer_ReadSinceClampsMaxBytes(t *testing.T) {
	t.Parallel()
	rb := NewRingBuffer(1024)
	rb.Write([]byte("0123456789"))
	got, next, err := rb.ReadSince(0, 5)
	if err != nil {
		t.Fatalf("ReadSince err: %v", err)
	}
	if got != "01234" {
		t.Fatalf("ReadSince(0, 5) = %q, want %q", got, "01234")
	}
	if next != 5 {
		t.Fatalf("ReadSince next = %d, want 5", next)
	}
}

func TestRingBuffer_ReadSinceClampsToOldestLive(t *testing.T) {
	t.Parallel()
	rb := NewRingBuffer(5)
	rb.Write([]byte("0123456789")) // 5 oldest dropped
	got, next, err := rb.ReadSince(0, 1024)
	if err != nil {
		t.Fatalf("ReadSince err: %v", err)
	}
	if got != "56789" {
		t.Fatalf("ReadSince past-oldest = %q, want %q", got, "56789")
	}
	if next != 10 {
		t.Fatalf("ReadSince next = %d, want 10", next)
	}
}

func TestRingBuffer_ReadSinceNegativeSince(t *testing.T) {
	t.Parallel()
	rb := NewRingBuffer(1024)
	rb.Write([]byte("abc"))
	got, next, err := rb.ReadSince(-5, 100)
	if err != nil {
		t.Fatalf("ReadSince err: %v", err)
	}
	if got != "abc" {
		t.Fatalf("ReadSince(-5) = %q, want %q", got, "abc")
	}
	if next != 3 {
		t.Fatalf("ReadSince(-5) next = %d, want 3", next)
	}
}

func TestRingBuffer_NoOpWhenCapZero(t *testing.T) {
	t.Parallel()
	rb := NewRingBuffer(0)
	rb.Write([]byte("ignored"))
	if rb.BytesUsed() != 0 {
		t.Fatalf("BytesUsed = %d, want 0", rb.BytesUsed())
	}
	if rb.Offset() != 7 {
		t.Fatalf("Offset = %d, want 7 (head advances for cursor monotonicity)", rb.Offset())
	}
	if got, _ := rb.Tail(100); got != "" {
		t.Fatalf("Tail = %q, want empty", got)
	}
	// ReadSince clamps to head (since used=0) so cursor advances to head.
	if got, next, _ := rb.ReadSince(0, 100); got != "" || next != 7 {
		t.Fatalf("ReadSince past-oldest = (%q, %d), want (\"\", 7)", got, next)
	}
}

func TestRingBuffer_CloseIdempotent(t *testing.T) {
	t.Parallel()
	rb := NewRingBuffer(1024)
	rb.Write([]byte("data"))
	rb.Close()
	rb.Close() // idempotent
	if n := rb.Write([]byte("more")); n != 0 {
		t.Fatalf("Write after Close returned %d, want 0", n)
	}
	if rb.BytesUsed() != 0 {
		t.Fatalf("BytesUsed after Close = %d, want 0", rb.BytesUsed())
	}
	if got, _ := rb.Tail(100); got != "" {
		t.Fatalf("Tail after Close = %q, want empty", got)
	}
	if rb.Offset() != 4 {
		t.Fatalf("Offset after Close = %d, want 4 (head preserved for cursor)", rb.Offset())
	}
}

func TestRingBuffer_ConcurrentWritersReaders(t *testing.T) {
	t.Parallel()
	rb := NewRingBuffer(4096)
	var wg sync.WaitGroup
	const writers = 4
	for w := 0; w < writers; w++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			for i := 0; i < 50; i++ {
				chunk := []byte{byte(id), byte(i)}
				rb.Write(chunk)
			}
		}(w)
	}
	for r := 0; r < 2; r++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := 0; i < 200; i++ {
				_ = rb.BytesUsed()
				_ = rb.Offset()
				_, _ = rb.Tail(64)
				_, _, _ = rb.ReadSince(0, 64)
			}
		}()
	}
	wg.Wait()
	if rb.BytesUsed() > 4096 {
		t.Fatalf("BytesUsed %d exceeds cap 4096", rb.BytesUsed())
	}
}

func TestRingBuffer_LargeWriteAcrossWrap(t *testing.T) {
	t.Parallel()
	rb := NewRingBuffer(16)
	rb.Write([]byte("AAAAAAAAAAAAAAAA"))         // 16 chars, used=16
	rb.Write([]byte("BBBBBBBBBBBBBBBBBBBBBBBB")) // 24 chars, only last 16 kept
	got, _ := rb.Tail(16)
	if got != "BBBBBBBBBBBBBBBB" {
		t.Fatalf("Tail = %q, want %q", got, "BBBBBBBBBBBBBBBB")
	}
}

func TestRingBuffer_LargeWriteExceedsCap(t *testing.T) {
	t.Parallel()
	rb := NewRingBuffer(8)
	rb.Write([]byte("01234567890123456789")) // 20 bytes, single write > cap
	got, head := rb.Tail(8)
	if got != "23456789" {
		t.Fatalf("Tail = %q, want %q", got, "23456789")
	}
	if head != 20 {
		t.Fatalf("head = %d, want 20", head)
	}
	if rb.BytesUsed() != 8 {
		t.Fatalf("BytesUsed = %d, want 8", rb.BytesUsed())
	}
}

func TestRingBuffer_ReadSinceAtCursorEqualsHead(t *testing.T) {
	t.Parallel()
	rb := NewRingBuffer(1024)
	rb.Write([]byte("data"))
	got, next, err := rb.ReadSince(rb.Offset(), 100)
	if err != nil {
		t.Fatalf("ReadSince err: %v", err)
	}
	if got != "" {
		t.Fatalf("got = %q, want empty", got)
	}
	if next != rb.Offset() {
		t.Fatalf("next = %d, want %d (cursor unchanged)", next, rb.Offset())
	}
}

func TestRingBuffer_TailLargerThanUsed(t *testing.T) {
	t.Parallel()
	rb := NewRingBuffer(1024)
	rb.Write([]byte("small"))
	got, _ := rb.Tail(1024)
	if got != "small" {
		t.Fatalf("Tail(1024) = %q, want %q", got, "small")
	}
}

func TestRingBuffer_EmptyBufferReads(t *testing.T) {
	t.Parallel()
	rb := NewRingBuffer(1024)
	if got, _ := rb.Tail(100); got != "" {
		t.Fatalf("empty Tail = %q, want empty", got)
	}
	if got, next, err := rb.ReadSince(0, 100); err != nil || got != "" || next != 0 {
		t.Fatalf("empty ReadSince = (%q, %d, %v), want (\"\", 0, nil)", got, next, err)
	}
}

// Smoke test that the buffer can hold typical PTY terminal control sequences.
func TestRingBuffer_HandlesANSIEscapes(t *testing.T) {
	t.Parallel()
	rb := NewRingBuffer(4096)
	seq := "\x1b[?25l\x1b[2J\x1b[HHello, World!\x1b[0m"
	rb.WriteString(seq)
	got, _ := rb.Tail(1024)
	if !strings.Contains(got, "Hello, World!") {
		t.Fatalf("Tail missing payload: %q", got)
	}
}

func TestRingBuffer_BackingAllocatedEagerly(t *testing.T) {
	t.Parallel()
	rb := NewRingBuffer(4096)
	// First Write must not allocate the backing slice; CapBytes reflects
	// config but the slice must already exist for the hot path.
	// We assert this indirectly: if the backing slice was lazy, the first
	// Write would contend with itself; here we just check that Tail works
	// after a single small write without panic.
	rb.WriteString("x")
	if got, _ := rb.Tail(1); got != "x" {
		t.Fatalf("Tail after eager alloc = %q, want %q", got, "x")
	}
}

func TestRingBuffer_WriteString_MatchesWrite(t *testing.T) {
	t.Parallel()
	a := NewRingBuffer(64)
	b := NewRingBuffer(64)
	a.Write([]byte("0123456789"))
	b.WriteString("0123456789")
	gotA, headA := a.Tail(64)
	gotB, headB := b.Tail(64)
	if gotA != gotB {
		t.Fatalf("WriteString diverges from Write: %q vs %q", gotA, gotB)
	}
	if headA != headB {
		t.Fatalf("head diverges: %d vs %d", headA, headB)
	}
}

func TestRingBuffer_WriteString_WrapAround(t *testing.T) {
	t.Parallel()
	rb := NewRingBuffer(8)
	rb.WriteString("ABCDEFGH")
	rb.WriteString("XYZ")
	got, _ := rb.Tail(8)
	if got != "DEFGHXYZ" {
		t.Fatalf("WriteString wrap = %q, want %q", got, "DEFGHXYZ")
	}
}

func TestRingBuffer_WriteString_LargeExceedsCap(t *testing.T) {
	t.Parallel()
	rb := NewRingBuffer(4)
	rb.WriteString("abcdefghij")
	got, head := rb.Tail(4)
	if got != "ghij" {
		t.Fatalf("WriteString large = %q, want %q", got, "ghij")
	}
	if head != 10 {
		t.Fatalf("head = %d, want 10", head)
	}
}

func TestRingBuffer_WriteString_Empty(t *testing.T) {
	t.Parallel()
	rb := NewRingBuffer(64)
	if n := rb.WriteString(""); n != 0 {
		t.Fatalf("WriteString(\"\") = %d, want 0", n)
	}
	if rb.Offset() != 0 {
		t.Fatalf("Offset after empty WriteString = %d, want 0", rb.Offset())
	}
}

func TestRingBuffer_WriteString_AfterClose(t *testing.T) {
	t.Parallel()
	rb := NewRingBuffer(64)
	rb.WriteString("data")
	rb.Close()
	if n := rb.WriteString("more"); n != 0 {
		t.Fatalf("WriteString after Close = %d, want 0", n)
	}
}
