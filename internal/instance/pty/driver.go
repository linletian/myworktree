// Package pty implements the framework.Kind interface for
// PTY-backed instances (zsh / bash running interactively inside the
// worktree directory). It owns:
//
//   - the open / spawn / fork-exec call to creack/pty
//   - the ring buffer that captures PTY output
//   - the broadcast channel that fans output to UI subscribers
//   - the Resize / SendInput controls
//
// The package is intentionally standalone: it does not import the
// opencode-web kind, the proxy, or any kind-specific knowledge. The
// framework treats this package as an opaque Kind registered under
// "pty".
package pty

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/creack/pty"
	"myworktree/internal/framework"
	"myworktree/internal/redact"
)

// Driver is the PTY kind implementation. One Driver instance is
// registered with framework.Registry under "pty"; it is stateless
// across instances (per-instance state lives inside Handle).
type Driver struct{}

// Manifest returns the static KindInfo for the PTY kind.
func (Driver) Manifest() framework.KindInfo {
	return framework.KindInfo{
		Name:        "pty",
		Label:       "Terminal",
		Description: "Interactive shell session running inside the worktree.",
		Interactive: true,
	}
}

// Handle is the per-instance state owned by the PTY kind. The
// framework holds onto it via framework.Handle; the PTY kind is the
// only code that ever reads its fields.
type Handle struct {
	id   string
	cmd  *exec.Cmd
	ptmx *os.File
	buf  *framework.RingBuffer
	pub  framework.Publisher

	cancel context.CancelFunc
	wg     *sync.WaitGroup
}

// blob is the persisted blob schema. PTY has no kind-private state
// beyond what ManagedInstance already stores, so the blob is empty.
type blob struct{}

// SetPublishers wires the per-instance publisher and persists the
// spawned PID so resource stats can sample the right process.
func (d Driver) SetPublishers(handle framework.Handle, p framework.Publisher) {
	h := handle.Unwrap().(*Handle)
	h.pub = p
	if h.cmd != nil && h.cmd.Process != nil {
		_ = p.SetPID(h.cmd.Process.Pid)
	}
}

// Spawn launches a zsh shell inside the worktree path.
func (d Driver) Spawn(ctx context.Context, params framework.SpawnParams) (framework.Handle, *framework.ReadySignal, error) {
	// Tag parity: a tag-backed PTY must carry a command (the pre-refactor
	// Manager rejected tag command-less starts). Ad-hoc starts (empty
	// TagID) may run an idle shell.
	if params.TagID != "" && strings.TrimSpace(params.Command) == "" {
		return framework.Handle{}, nil, errors.New("tag command is required")
	}

	cmd := exec.Command("zsh", "-f", "-i")
	cmd.Dir = params.WorktreePath
	cmd.Env = os.Environ()
	for k, v := range params.ExtraEnv {
		cmd.Env = append(cmd.Env, k+"="+v)
	}

	// Tag preStart runs with the same environment the shell will get.
	if strings.TrimSpace(params.PreStart) != "" {
		pre := exec.Command("zsh", "-lc", params.PreStart)
		pre.Dir = cmd.Dir
		pre.Env = cmd.Env
		if out, err := pre.CombinedOutput(); err != nil {
			return framework.Handle{}, nil, fmt.Errorf("preStart failed: %w: %s", err, strings.TrimSpace(string(out)))
		}
	}

	ptmx, err := pty.Start(cmd)
	if err != nil {
		return framework.Handle{}, nil, fmt.Errorf("pty start: %w", err)
	}

	// The framework pre-allocates the per-instance ring buffer
	// (params.Buffer) in Manager.Start before calling Spawn. A nil here is
	// a framework bug, not a user input — Manager.Start unconditionally
	// wires params.Buffer from m.AllocateBuffer(id, capBytes).
	if params.Buffer == nil {
		panic("pty: SpawnParams.Buffer must be set by framework.Manager.Start (framework bug, not user error)")
	}
	buf := params.Buffer

	runCtx, cancel := context.WithCancel(context.Background())
	wg := &sync.WaitGroup{}
	wg.Add(2)

	h := &Handle{
		id:     params.InstanceID,
		cmd:    cmd,
		ptmx:   ptmx,
		buf:    buf,
		cancel: cancel,
		wg:     wg,
	}

	ready := framework.NewReadySignal()
	ready.Close()

	go h.pumpLogs()
	go h.wait(runCtx)

	// Send the tag / ad-hoc command as initial input (pre-refactor
	// parity: 150ms after start so the shell has finished init).
	if strings.TrimSpace(params.Command) != "" {
		go func() {
			time.Sleep(150 * time.Millisecond)
			_, _ = io.WriteString(ptmx, params.Command+"\n")
		}()
	}

	return framework.NewHandle("pty", h), ready, nil
}

// Resize forwards the terminal size to the kernel PTY.
func (h *Handle) Resize(cols, rows int) error {
	return pty.Setsize(h.ptmx, &pty.Winsize{
		Cols: uint16(cols),
		Rows: uint16(rows),
	})
}

// SendInput writes raw bytes to the PTY. Special control characters
// translate to signals.
func (h *Handle) SendInput(input string) error {
	for _, ch := range input {
		switch ch {
		case 0x03:
			if h.cmd != nil && h.cmd.Process != nil {
				_ = syscall.Kill(-h.cmd.Process.Pid, syscall.SIGINT)
			}
			if _, err := io.WriteString(h.ptmx, string(ch)); err != nil {
				return err
			}
			return nil
		case 0x1A:
			if h.cmd != nil && h.cmd.Process != nil {
				_ = syscall.Kill(-h.cmd.Process.Pid, syscall.SIGTSTP)
			}
			if _, err := io.WriteString(h.ptmx, string(ch)); err != nil {
				return err
			}
			return nil
		case 0x1C:
			if h.cmd != nil && h.cmd.Process != nil {
				_ = syscall.Kill(-h.cmd.Process.Pid, syscall.SIGQUIT)
			}
			if _, err := io.WriteString(h.ptmx, string(ch)); err != nil {
				return err
			}
			return nil
		}
	}
	_, err := io.WriteString(h.ptmx, input)
	return err
}

// Stop terminates the PTY's process.
func (d Driver) Stop(handle framework.Handle, graceSeconds int) error {
	h := mustHandle(handle)
	if h.cmd == nil || h.cmd.Process == nil {
		return nil
	}
	pid := h.cmd.Process.Pid
	if err := syscall.Kill(-pid, syscall.SIGTERM); err != nil && !errors.Is(err, syscall.ESRCH) {
		if p, perr := os.FindProcess(pid); perr == nil {
			_ = p.Signal(syscall.SIGTERM)
		}
	}
	return nil
}

// Status reports the PTY's current state.
func (d Driver) Status(handle framework.Handle) (framework.Status, string) {
	h := mustHandle(handle)
	if h.cmd == nil || h.cmd.Process == nil {
		return framework.StatusExited, ""
	}
	if err := syscall.Kill(h.cmd.Process.Pid, 0); err != nil {
		if errors.Is(err, syscall.ESRCH) {
			return framework.StatusExited, ""
		}
		return framework.StatusFailed, err.Error()
	}
	return framework.StatusRunning, ""
}

// ReadLogs returns captured PTY output. A negative since returns the
// newest maxBytes held in the ring buffer (tail semantics).
func (d Driver) ReadLogs(handle framework.Handle, since int64, maxBytes int64) (string, int64, error) {
	h := mustHandle(handle)
	if h.buf == nil {
		return "", since, nil
	}
	if since < 0 {
		body, off := h.buf.Tail(maxBytes)
		return body, off, nil
	}
	return h.buf.ReadSince(since, maxBytes)
}

// KindBlob: PTY has no private state.
func (d Driver) KindBlob(handle framework.Handle) (json.RawMessage, error) {
	return json.Marshal(blob{})
}

// HTTPHint: PTY has no HTTP surface.
func (d Driver) HTTPHint(instanceID string) string { return "" }

// RegisterHTTP: PTY has no HTTP handlers.
func (d Driver) RegisterHTTP(mux *http.ServeMux, instanceID string, handle framework.Handle) {}

// Resize (extension interface) unwraps the PTY handle and forwards
// the terminal size to the kernel PTY. framework.Manager calls this
// via a resizer type assertion when the frontend sends a resize
// message.
func (d Driver) Resize(handle framework.Handle, cols, rows int) error {
	return mustHandle(handle).Resize(cols, rows)
}

// SendInput (extension interface) unwraps the PTY handle and writes
// raw bytes to the PTY stdin. framework.Manager calls this via an
// inputter type assertion when the WebSocket or HTTP input endpoint
// delivers user keystrokes.
func (d Driver) SendInput(handle framework.Handle, input string) error {
	return mustHandle(handle).SendInput(input)
}

// SubscribeOutput (extension interface) returns a buffered channel
// that receives redacted PTY output chunks for ONE instance. framework.Manager
// calls this via an outputSub type assertion when a WebSocket client
// subscribes to the instance's live output stream.
//
// The id argument scopes the subscription to a single instance —
// without it, every PTY tab would receive every other PTY tab's
// output (a regression from the pre-kind-refactor Manager, which
// keyed subscribers per-instance).
//
// The channel closes when the subscription ends: on cancel, and also
// when broadcast overflows because this consumer cannot keep up (issue
// #82). A closed channel therefore means "resync", never "the instance
// produced nothing more".
func (d Driver) SubscribeOutput(id string) (<-chan string, func(), error) {
	return SubscribeOutput(id)
}

// --- handle internals ---

func (h *Handle) pumpLogs() {
	defer h.wg.Done()
	// Release the master fd once the read loop exits (the slave side
	// dies with the child): without this every PTY start/stop leaked a
	// fd until exhaustion (REVIEW-2026-08-16 HIGH-1).
	defer h.ptmx.Close()
	readBuf := make([]byte, 1024)
	for {
		n, err := h.ptmx.Read(readBuf)
		if n > 0 {
			chunk := redact.Text(string(readBuf[:n]))
			// This ordering — ring buffer FIRST, broadcast second — is
			// load-bearing, not stylistic: it is the entire reason an
			// overflowing subscriber may have its backlog discarded at the
			// source (broadcast, issue #82). Any chunk a subscriber ever
			// sees is already in the ring, so the reconnect's `since`
			// replay can hand it back. Broadcasting first would make the
			// drain lose bytes no replay can recover. Once the buffer has
			// been closed and dropped by the framework these writes are
			// no-ops, which is the one case the replay cannot cover
			// (ARCHITECTURE §4.1).
			if h.buf != nil {
				h.buf.WriteString(chunk)
			}
			broadcast(h.id, chunk)
		}
		if err != nil {
			return
		}
	}
}

func (h *Handle) wait(ctx context.Context) {
	defer h.wg.Done()
	_ = h.cmd.Wait()
	h.cancel()
}

func mustHandle(h framework.Handle) *Handle {
	inner := h.Unwrap()
	if hh, ok := inner.(*Handle); ok {
		return hh
	}
	panic("pty: framework.Handle inner is not *Handle")
}

// --- subscriber broadcast ---

// subs is keyed by instance id so a WS subscriber to instance A does
// not also receive instance B's PTY output. (Pre-kind-refactor
// Manager.broadcastOutput was per-instance; this is the equivalent.)
//
// The value is a *subscriber, not a bare channel, because the channel
// gained a second closer: an overflowing broadcast (see broadcast, issue
// #82) closes it just as cancel does. A bare channel cannot express
// "already closed", so a cancel racing an overflow panics on the second
// close. `closed` sits beside the channel and is touched only under
// subsMu — the lock every access to this map already requires — which
// makes the close exactly-once by construction rather than by callers
// remembering to guard it.
//
// subsMu is PROCESS-GLOBAL: one mutex for every PTY instance in the daemon.
// That is why broadcast's overflow drain runs after it is released — a loop
// that never terminates while holding this lock does not stall one instance,
// it freezes the daemon's entire output fan-out. See broadcast.
var (
	subsMu sync.Mutex
	subs   map[string]map[*subscriber]struct{}
)

// subscriber is one output sink: its channel plus the exactly-once close
// flag that guards it. Both closers (the caller's cancel and an overflow
// in broadcast) go through closeLocked under subsMu.
type subscriber struct {
	ch     chan string
	closed bool
}

// drain discards everything still buffered on this subscriber's channel.
//
// It terminates ONLY because the channel is already closed: `for range` over
// an OPEN, empty channel blocks forever. That is why broadcast collects its
// overflowed subscribers under subsMu and drains them after releasing it —
// never inside the critical section, where one reordered line would wedge
// every instance in the process on the package-level subsMu. See broadcast
// for the full argument.
func (s *subscriber) drain() {
	for range s.ch {
	}
}

// closeLocked closes the channel at most once. Callers must hold subsMu.
//
// The division of labour between the two guards, stated exactly — they do
// different jobs:
//
//   - The map `delete` (under subsMu, performed by BOTH closers before
//     they close) is what serialises the two closers. Once broadcast has
//     removed a subscriber from subs[id], broadcast can never reach it
//     again, and cancel's delete under the same lock cannot interleave with
//     it; the two closeLocked calls therefore cannot run concurrently for
//     one subscriber regardless of what the flag does.
//   - The `closed` flag is what makes the remaining SEQUENTIAL cases safe:
//     cancel after an overflow, and cancel called twice. In those cases
//     there is no race to win — the second close simply arrives later at
//     an already-closed channel — and without the flag each would panic
//     with `close of closed channel`. Both are pinned by
//     TestSubscribeOutput_CancelAfterOverflowDoesNotPanic.
//
// Callers must still hold subsMu when they call this, because the flag is
// meaningless if two goroutines can read it at the same time — but the
// concurrency proof rests on the delete-plus-lock-ordering above, not on
// this function being lock-guarded in isolation.
//
// It reports whether THIS call performed the close. That return value is not
// decoration: broadcast collects a subscriber for its post-unlock drain only
// when closeLocked returns true, which turns "the channel we are about to
// drain is already closed" — the single fact that makes `for range` over it
// terminate — from a convention the reader has to trust into something this
// function proves (see broadcast). cancel ignores it: cancel is the last
// closer, there is nothing left to drain behind it.
func (s *subscriber) closeLocked() bool {
	if s.closed {
		return false
	}
	s.closed = true
	close(s.ch)
	return true
}

// broadcast fans one chunk out to every live subscriber of one instance.
//
// A subscriber whose buffer is full is DISCONNECTED, not skipped: it is
// removed from the registry, its channel is closed, and its queued backlog is
// discarded at the source, so that backlog never reaches the socket writer.
// What that GUARANTEES is bounded and structural: the queue is empty by the
// time broadcast returns, so the WS handler can never be made to write a full
// queue — up to 64 chunks, ~64 KB — onto a socket that has already stalled.
// What it does NOT guarantee is that the handler's very next receive is the
// close itself. close() readies any receiver already parked on the channel,
// and a readied receiver races the drain loop for the values still buffered,
// so the consumer may legitimately win some of them and write those before it
// ever sees `!ok`. That is typical, not deterministic, and nothing here may be
// read as promising otherwise. What IS deterministic is the teardown once
// `!ok` does arrive — the handler answers with a 1013 close frame and returns
// (internal/app/app.go). The browser's ws.onclose fires and reconnects, and
// because the #87 offset contract is already in place it resumes from its own
// cursor instead of re-appending the whole tail.
//
// WHY discarding the backlog is the right thing: Go delivers a closed
// channel's BUFFERED values with ok == true before it ever reports ok ==
// false, so an undrained queue would have the WS handler WriteBinary every
// queued chunk — up to 64 of them, ~64 KB — onto the socket that stalled in
// the first place, and only then reach the `!ok` teardown branch (app.go).
// Draining cuts that exposure down, but it does not order it: close() readies
// any receiver already parked on the channel, so a consumer that is already
// waiting races the drain loop for the buffered values and may take some of
// them — those few still reach the socket before the consumer observes
// `!ok`. The guarantee is the bounded one, not an ordering one: the queue is
// drained by the time broadcast returns, so at most the handful of chunks that
// race can still go out, and never a full queue. The
// chunks are replayable because pumpLogs writes every chunk to the ring
// buffer BEFORE broadcasting it (see pumpLogs), so they come back on the
// reconnect's replay from the client's #87 cursor: nothing is lost THAT IS
// STILL IN THE RING BUFFER. The one exception is the ring that has already
// been closed and dropped (Manager.dropBuffer → RingBuffer.Close, which sets
// closed/data=nil/used=0 and does not reset head): a chunk drained after that
// point went nowhere when pumpLogs wrote it, so no replay can bring it back,
// and a still-connected client would have received it on the wire pre-drain.
// ARCHITECTURE §4.1 already declares that post-swap loss intentional; docs
// API.md and the CHANGELOG carry the same qualifier.
//
// WHY the drain runs AFTER subsMu is released, and cannot live in the loop:
// the loop terminates only because the channel it ranges over is already
// closed, and that is a convention this function keeps, not something the
// compiler enforces. `for range` over an OPEN, empty channel never returns.
// Drain inside the critical section and one future edit that drops or
// reorders the closeLocked() call — while keeping the `delete`, which looks
// load-bearing and would survive — blocks this goroutine forever holding the
// package-level subsMu, so every OTHER instance's broadcast wedges at
// subsMu.Lock(): no panic, no log, no timeout, the whole daemon's PTY output
// silently frozen. Before the drain existed that same slip produced a loud,
// local `panic: send on closed channel`; the drain must not trade a local
// loud failure for a global silent hang. Hence: closeLocked reports whether it
// performed the close, only those subscribers are collected, and the drain
// runs on the collection once subsMu is gone — so "we drain only channels
// this critical section closed" is provable, not assumed.
//
// The threshold is concrete, not "a bit slow": pumpLogs reads the PTY in
// 1024-byte chunks and each subscriber queue holds 64 of them, so a
// consumer is disconnected after roughly 64 KB of output it has not taken.
// That is the price of this design, and it is the right price — recovery
// costs the client one reconnect (the browser retries after a hard-coded
// 5 s, index.html) and then resumes incrementally from its own cursor.
// The knob that would make the whole thing gentler is a write deadline on
// the TTY socket for HEALTHY connections, turning TCP backpressure into a
// bounded stall; that is deliberately NOT done here and belongs to the
// separate liveness issue #83 (app.go sets a deadline only on its own
// teardown write, so the close frame cannot re-block the handler). Raising
// the buffer capacity is likewise deliberately not done: a bigger queue only
// postpones the disconnect of a consumer that cannot keep up, which is the
// correct outcome anyway.
//
// Dropping the chunk and keeping the subscriber registered — the old
// empty `default:` — was issue #82: a lost clear-screen or cursor move
// corrupts everything the client renders afterwards, unboundedly and with
// nothing on either side able to notice.
func broadcast(id string, chunk string) {
	if chunk == "" || id == "" {
		return
	}
	// closed collects the subscribers THIS critical section closed. Declared
	// before the lock so the deferred unlock below can see it: the unlock is
	// the first statement of that deferred func, so the drain provably runs
	// with subsMu released, and the collection is the only thing it touches.
	var closed []*subscriber
	subsMu.Lock()
	defer func() {
		subsMu.Unlock()
		// Outside the lock, by necessity: sub.drain terminates only because
		// closeLocked returned true for this subscriber, i.e. this section
		// closed this channel. Nothing can send to it again — it is out of
		// the map and closed — so draining needs no registry access at all.
		for _, sub := range closed {
			sub.drain()
		}
	}()
	for sub := range subs[id] {
		select {
		case sub.ch <- chunk:
		default:
			// Deleting from a map while ranging over it is
			// documented-safe: the deleted entry is simply never
			// produced again.
			delete(subs[id], sub)
			// Collect ONLY when this call is the one that closed the
			// channel: that is what makes the post-unlock drain provably a
			// drain of an already-closed channel. A false here would mean
			// somebody else closed it first, and their drain is theirs to
			// run — unreachable today, because cancel deletes before it
			// closes, so a subscriber still in the map has never been
			// closed. Defensive, and it costs nothing.
			if sub.closeLocked() {
				closed = append(closed, sub)
			}
			if len(subs[id]) == 0 {
				// Keep the invariant cancel already holds: an id
				// key exists only while it has a live subscriber.
				delete(subs, id)
			}
		}
	}
}

// SubscribeOutput registers a channel for one instance's PTY output
// and returns the channel and a cancel func that unsubscribes.
//
// id is required: an empty id is rejected so a buggy caller does not
// accidentally receive every PTY instance's output.
//
// The returned channel closes on unsubscribe — when cancel runs, and
// also when broadcast overflows (issue #82) — so a consumer must treat a
// closed channel as "you were disconnected, resync", never as end of
// output. On an overflow the queued backlog is discarded at the source
// (see broadcast), which empties the queue before broadcast returns and so
// keeps a full backlog off a socket that has already stalled. It does NOT
// make the close the consumer's very next receive: close() readies a
// receiver already parked on the channel, and that receiver races the drain
// loop for the values still buffered, so some of them may legitimately be
// delivered with ok == true first. Expect the close there; do not depend on
// its position. What it
// missed comes back on the cursor replay — everything still in the ring
// buffer; a ring that has already been closed and dropped cannot replay the
// chunks it stopped accepting (ARCHITECTURE §4.1). cancel is idempotent
// and safe to call after such an overflow.
func SubscribeOutput(id string) (<-chan string, func(), error) {
	if id == "" {
		return nil, nil, errors.New("pty: SubscribeOutput requires instance id")
	}
	subsMu.Lock()
	if subs == nil {
		subs = map[string]map[*subscriber]struct{}{}
	}
	if subs[id] == nil {
		subs[id] = map[*subscriber]struct{}{}
	}
	sub := &subscriber{ch: make(chan string, 64)}
	subs[id][sub] = struct{}{}
	subsMu.Unlock()
	cancel := func() {
		subsMu.Lock()
		defer subsMu.Unlock()
		delete(subs[id], sub)
		if len(subs[id]) == 0 {
			delete(subs, id)
		}
		// Inside the lock, and exactly once: an overflow may already
		// have closed this channel (issue #82), and a close of a
		// closed channel panics.
		//
		// closeLocked's return value is deliberately ignored here. It only
		// matters to a caller that drains afterwards, and cancel must NOT
		// drain: it is the polite unsubscribe, and a consumer that is still
		// reading is entitled to the buffered chunks it never took. The
		// overflow path is the one that discards them, and it discards them
		// outside this lock (see broadcast).
		sub.closeLocked()
	}
	return sub.ch, cancel, nil
}

// Register registers the PTY kind with the framework registry on
// package init.
func init() {
	framework.Register(Driver{})
}
