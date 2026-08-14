package framework

import (
	"errors"
	"sync"
)

// RingBuffer is a bounded, in-memory ring buffer for capturing
// process output. Kinds write their stdout/stderr here and the
// framework's log endpoints (Tail, ReadSince) read from it.
//
// Semantics:
//   - Writes append and never block. When `cap` is exceeded, oldest
//     data is overwritten FIFO so the buffer holds exactly the last
//     `cap` bytes.
//   - `Offset()` returns a monotonic total-bytes-written counter.
//     Callers pass this as the `since` cursor to `ReadSince` to fetch
//     only new data.
//   - `ReadSince(since, maxBytes)` returns ("", since, nil) when
//     since >= head, preserving the SSE 1-second poll-loop contract
//     (no new data → cursor unchanged).
//   - When since points to data older than the buffer's oldest live
//     byte, the read silently clamps to the oldest live byte.
//
// Concurrency: safe for concurrent Write, WriteString, Tail,
// ReadSince, Offset, BytesUsed, and Close. Close is idempotent;
// after Close, writes are dropped, reads return zero values, and
// Offset returns the last head observed.
//
// Hot-path design (heavy TUI workloads): the backing slice is
// allocated eagerly in NewRingBuffer so the first Write does not
// stall the producer goroutine for a 32MB malloc, and WriteString
// avoids the []byte(s) copy that would otherwise occur on every
// 1024-byte PTY chunk.
type RingBuffer struct {
	mu     sync.Mutex
	cap    int64
	data   []byte
	head   int64
	used   int64
	closed bool
}

// NewRingBuffer creates a RingBuffer that holds at most capBytes.
// If capBytes <= 0, the buffer is a no-op (writes succeed without
// storing). The backing slice is allocated eagerly so the first
// Write is allocation-free on the hot path.
func NewRingBuffer(capBytes int64) *RingBuffer {
	rb := &RingBuffer{cap: capBytes}
	if capBytes > 0 {
		rb.data = make([]byte, capBytes)
	}
	return rb
}

// Write appends p. Returns len(p) on success. After Close, returns 0.
func (r *RingBuffer) Write(p []byte) int {
	return r.write(p, "")
}

// WriteString is the string variant of Write.
func (r *RingBuffer) WriteString(s string) int {
	return r.write(nil, s)
}

func (r *RingBuffer) write(p []byte, s string) int {
	srcLen := int64(len(p))
	if s != "" {
		srcLen = int64(len(s))
	}
	if srcLen == 0 {
		return 0
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.closed {
		return 0
	}
	if r.cap > 0 {
		srcStart := int64(0)
		if srcLen > r.cap {
			srcStart = srcLen - r.cap
		}
		pos := (r.head + srcStart) % r.cap
		if p != nil {
			writeData := p[srcStart:]
			n := copy(r.data[pos:], writeData)
			if n < len(writeData) {
				copy(r.data, writeData[n:])
			}
		} else {
			writeData := s[srcStart:]
			n := copy(r.data[pos:], writeData)
			if n < len(writeData) {
				copy(r.data, writeData[n:])
			}
		}
	}
	r.head += srcLen
	if r.cap > 0 {
		newUsed := r.used + srcLen
		if newUsed > r.cap {
			r.used = r.cap
		} else {
			r.used = newUsed
		}
	}
	return int(srcLen)
}

// Offset returns the total bytes written so far. Monotonic.
func (r *RingBuffer) Offset() int64 {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.head
}

// BytesUsed returns bytes currently held in the buffer.
func (r *RingBuffer) BytesUsed() int64 {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.used
}

// Tail returns the last n bytes held in the buffer. Returns the
// absolute end offset of the returned bytes (= current head).
func (r *RingBuffer) Tail(n int64) (string, int64) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if n <= 0 || r.used == 0 {
		return "", r.head
	}
	if n > r.used {
		n = r.used
	}
	return r.copyRangeLocked(r.head-n, n), r.head
}

// ReadSince returns up to maxBytes of data starting at absolute offset since.
func (r *RingBuffer) ReadSince(since int64, maxBytes int64) (string, int64, error) {
	if maxBytes < 0 {
		return "", since, errors.New("maxBytes must be >= 0")
	}
	if since < 0 {
		since = 0
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if since >= r.head {
		return "", since, nil
	}
	if maxBytes == 0 {
		return "", since, nil
	}
	start := since
	if start < r.head-r.used {
		start = r.head - r.used
	}
	avail := r.head - start
	if avail > maxBytes {
		avail = maxBytes
	}
	return r.copyRangeLocked(start, avail), start + avail, nil
}

// Close drops the backing buffer and prevents further writes. Idempotent.
func (r *RingBuffer) Close() {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.closed = true
	r.data = nil
	r.used = 0
}

// CapBytes returns the configured cap (immutable after construction).
func (r *RingBuffer) CapBytes() int64 {
	if r == nil {
		return 0
	}
	return r.cap
}

func (r *RingBuffer) copyRangeLocked(start int64, n int64) string {
	if r.data == nil || n <= 0 {
		return ""
	}
	buf := make([]byte, n)
	pos := start % r.cap
	copied := copy(buf, r.data[pos:])
	if int64(copied) < n {
		copy(buf[copied:], r.data[:n-int64(copied)])
	}
	return string(buf)
}
