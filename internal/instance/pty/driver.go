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
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
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

// SubscribeOutputWithPolicy (extension interface) is SubscribeOutput with
// the overflow behaviour chosen per subscription (issue #114): the TTY WS
// handler hands OverflowDropOldest to clients that declared the
// `overflow-notice` capability and never touches this method otherwise, so
// framework.Manager.SubscribeOutput — which knows only the plain
// SubscribeOutput surface — keeps serving every other caller the #82 close
// contract unchanged.
func (d Driver) SubscribeOutputWithPolicy(id string, policy OverflowPolicy) (<-chan string, func(), *DropStats, error) {
	return SubscribeOutputWithPolicy(id, policy)
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

// OverflowPolicy selects what a full subscriber queue MEANS in broadcast.
// It is chosen per subscription (see SubscribeOutputWithPolicy) and lets
// issue #114 negotiate the trade per client: the #82 close-on-overflow
// contract stays the default and the untouched SubscribeOutput keeps it,
// while clients that declare the `overflow-notice` cap (internal/app
// handleInstanceTTYWSLiveness) opt into drop-oldest plus an in-band
// notice, so a slow-but-alive viewer degrades to a redraw instead of a
// 1013 reconnect storm.
type OverflowPolicy uint8

const (
	// OverflowClose is the default and the legacy behaviour (issue #82):
	// an overflowing subscriber is deleted from the registry, its channel
	// closed, and its backlog drained at the source, so the consumer sees
	// "resync" rather than a silently corrupt stream.
	OverflowClose OverflowPolicy = iota
	// OverflowDropOldest keeps the subscriber registered through an
	// overflow: broadcast discards the OLDEST queued chunks to make room
	// for the newest, so the consumer's screen lags instead of tearing
	// down. Every discarded chunk is counted in the subscriber's
	// DropStats, which is what makes the loss visible to the client (the
	// overflow notice) — the silence that issue #82 condemned is exactly
	// what this policy must never fall back to.
	OverflowDropOldest
)

// --- subscriber queue capacity (issue #114, FIX-PLAN 决策 1b) -------------
//
// The two overflow policies get two DIFFERENT capacities, and that split IS
// the decision: the close-policy queue keeps the number issue #82 was
// designed and tested around, while the drop-policy queue gets a queue big
// enough to ride out a burst instead of evicting inside the same second.
const (
	// subscriberQueueChunksClose is issue #82's capacity, verbatim: 64 of
	// the 1024-byte chunks pumpLogs reads, so a close-policy subscriber is
	// disconnected after ~64 KB it has not taken.
	//
	// The number is FIXED on purpose and there is NO env override for it —
	// not even the drop queue's env below. #82's contract is "fall ~64 KB
	// behind and you are disconnected, with a resync signal"; every #82/#87
	// regression test, doc and CHANGELOG entry reasons about that number,
	// and making it tunable would let an operator silently postpone the
	// disconnect the contract promises. The two capacities never move
	// together: MYWORKTREE_TTY_DROP_QUEUE_CHUNKS cannot reach this one.
	subscriberQueueChunksClose = 64

	// defaultSubscriberQueueChunksDrop is the capacity of an
	// OverflowDropOldest subscriber when the env override is absent or
	// unusable: 4096 chunks × 1024 B (pumpLogs' read size) = 4,194,304 B
	// ≈ 4.19 MB (10^6 口径; 习称 4MB) — the precise per-subscriber upper
	// bound 决策 1b quotes, and the reason the unit here is CHUNKS and not
	// bytes: the bound is exact only because a chunk can never exceed the
	// 1024-byte read that produced it.
	//
	// The arithmetic that picked 4096 (FIX-PLAN 决策 1b, measured on the
	// storm day; KB = 10^3, drain = liveBytes/subAgeMs, n=52):
	//
	//	burst production 150–358 KB/s − median drain 47 KB/s
	//		= a live gap of ~103–311 KB/s
	//	4×10^6 B ÷ (103–311 KB/s) ≈ ~13–39 s before the first drop
	//	64×10^3 B ÷ (103–311 KB/s) ≈ ~0.2–0.6 s before the first drop
	//
	// (The cadence divides the 习称 4 MB/64 KB figures, i.e. 10^6/10^3,
	// the plan's own 口径 — with the exact 4,194,304 B the 4 MB row reads
	// ~13–41 s, the same order, and nothing here depends on the difference.)
	//
	// The 64 KB row is the same order as the 19 sub-second re-overflows the
	// production log records, which is the cross-check that the queue — not
	// the client's backoff — was setting the storm's tempo. So 4096 buys a
	// typical consumer a reset cadence of tens of seconds instead of
	// sub-seconds. It does NOT rescue a pathological consumer (p25 drain
	// 6.4 KB/s, floor 0.6 KB/s): no fixed queue can, and that case is
	// answered by the client's reset+tail degradation, not by capacity.
	//
	// Overridable per subscription by MYWORKTREE_TTY_DROP_QUEUE_CHUNKS
	// (see subscriberQueueChunksDrop). Tuning it DOWN is the operator's
	// lever against RSS; tuning it UP only postpones the same notice.
	defaultSubscriberQueueChunksDrop = 4096

	// envSubscriberQueueChunksDrop is the operator override for the
	// DROP-POLICY capacity and for nothing else: an integer > 0 wins, and
	// anything missing, unparsable, zero or negative falls back to
	// defaultSubscriberQueueChunksDrop. It has no effect on
	// subscriberQueueChunksClose and no effect on SubscribeOutput, which is
	// close-policy by construction (issue #82, verbatim).
	envSubscriberQueueChunksDrop = "MYWORKTREE_TTY_DROP_QUEUE_CHUNKS"

	// maxSubscriberQueueChunksDrop is the UPPER bound of the env override:
	// 1<<20 = 1,048,576 chunks × 1024 B ≈ 1 GB of queue per subscriber at
	// the pump's read size — already far past any legitimate RSS budget.
	// A value past it falls back to defaultSubscriberQueueChunksDrop, the
	// same "invalid → default" family as ttyWriteTimeout() and
	// ttyReplayWindowDefaultBytes(). The bound is not about RSS politeness:
	// a maxInt-scale value PANICS the subscription at makechan ("size out
	// of range", reproduced in arbitration for PR #129 R2-A), killing the
	// connection with a runtime stack instead of serving it the default.
	maxSubscriberQueueChunksDrop = 1 << 20
)

// subscriberQueueChunksDrop resolves the drop-policy queue capacity, read
// from the environment at the moment of each subscription — a snapshot per
// subscription, not a package init value. That is deliberate: subscriptions
// are rare (one per TTY WS connection) compared to the per-chunk work the
// queue then serves, so an env read here costs nothing measurable, and in
// exchange the value is always the one the operator set most recently, with
// no restart and no init-order dependency. It also keeps the resolution
// local to the only policy that consults it: this is called for
// OverflowDropOldest subscriptions and never for a close-policy one.
//
// A missing, unparsable, zero or negative value yields
// defaultSubscriberQueueChunksDrop rather than an error: the queue size is
// not a knob worth failing a subscription over, and a fallback that keeps
// the service up beats a rejected connection. A huge-but-allocatable value
// is still NOT clamped down to the default — an operator who sets
// 1000000 owns the RSS that buys, and the bound that matters (4 MB per
// subscriber at the default) is documented at
// defaultSubscriberQueueChunksDrop — but a value PAST
// maxSubscriberQueueChunksDrop falls back to the default: past that ceiling
// the channel is no longer a big allocation, it is a makechan panic
// ("size out of range" at maxInt scale, PR #129 R2-A), and a subscription
// that panics serves nobody.
func subscriberQueueChunksDrop() int {
	raw, ok := os.LookupEnv(envSubscriberQueueChunksDrop)
	if !ok {
		return defaultSubscriberQueueChunksDrop
	}
	n, err := strconv.Atoi(raw)
	if err != nil || n <= 0 {
		return defaultSubscriberQueueChunksDrop
	}
	if n > maxSubscriberQueueChunksDrop {
		return defaultSubscriberQueueChunksDrop
	}
	return n
}

// dropSnapshot is the immutable counter pair DropStats publishes: one
// allocation per Add, never mutated after the Store that makes it visible.
// Pairing the two counts inside one value is what lets a reader see them
// as they stood at a single instant — two independent counters could not,
// because a producer's Add lands on them one at a time.
type dropSnapshot struct {
	chunks int64
	bytes  int64
}

// DropStats counts chunks an OverflowDropOldest subscriber never received.
// The producer (broadcast) Adds under subsMu; any goroutine may read at any
// time. The two counters are published as a SINGLE atomic unit — an
// atomic.Pointer to an immutable dropSnapshot — so every read here goes
// through the same atomic load of that pointer and can never return a
// chunks/bytes combination no producer ever produced: Add builds the new
// pair from the old one and publishes it in one CAS, and Chunks(), Bytes()
// and Snapshot() all derive from one Load of it. A reader that needs the
// PAIR together (the WS handler's incremental overflow notice) calls
// Snapshot(); the single-counter conveniences answer from that same unit,
// so even two of their own loads straddle only whole published pairs.
// (That is the contract the WS handler needs: it reports INCREMENTAL
// deltas off one Snapshot between live chunks, and the client resets and
// repaints on any notice.)
//
// SubscribeOutputWithPolicy always hands back a non-nil *DropStats — zero
// for OverflowClose subscriptions, where nothing is ever dropped — so
// callers never nil-guard the handle. The zero value is ready to use too:
// the first Add publishes the first snapshot, reads before that report zero.
type DropStats struct {
	snap atomic.Pointer[dropSnapshot]
}

// Add records n discarded chunks totalling nbytes. Producers only.
//
// Why it is exported, stated plainly: the cross-package test double needs
// it. internal/app's ttySubscriber mirrors this package's subscriber so the
// overflow-notice e2e test drives the same broadcast shape the pty kind
// produces in production — drop arm, retry arm and all — and a mirror that
// could not count its own drops would have to approximate that shape
// instead, which is precisely what makes such a double useless. There is no
// production caller outside this package today: broadcast's overflow arm is
// the only producer here. The CAS loop is what makes the export safe to
// share anyway — the pair is published in one CAS on a fresh immutable
// snapshot, so a concurrent reader never observes chunks advanced while bytes
// lagged behind (or the reverse), and the totals stay exact even when
// producers other than broadcast's (which serialises under subsMu) share a
// handle.
func (s *DropStats) Add(n int64, nbytes int64) {
	for {
		cur := s.snap.Load()
		next := dropSnapshot{chunks: n, bytes: nbytes}
		if cur != nil {
			next.chunks = cur.chunks + n
			next.bytes = cur.bytes + nbytes
		}
		if s.snap.CompareAndSwap(cur, &next) {
			return
		}
	}
}

// Snapshot returns the discarded-chunk and discarded-byte totals read from
// ONE atomic load — a snapshot-consistent pair by construction (see type
// doc). This is the accessor for callers that consult both counters
// together; the WS handler's incremental notice uses it.
func (s *DropStats) Snapshot() (chunks, bytes int64) {
	cur := s.snap.Load()
	if cur == nil {
		return 0, 0
	}
	return cur.chunks, cur.bytes
}

// Chunks returns the total number of chunks discarded so far, read from the
// same atomic unit as Bytes and Snapshot (see type doc).
func (s *DropStats) Chunks() int64 { chunks, _ := s.Snapshot(); return chunks }

// Bytes returns the total payload bytes discarded so far, read from the
// same atomic unit as Chunks and Snapshot (see type doc).
func (s *DropStats) Bytes() int64 { _, bytes := s.Snapshot(); return bytes }

// subscriber is one output sink: its channel plus the exactly-once close
// flag that guards it. Both closers (the caller's cancel and an overflow
// in broadcast) go through closeLocked under subsMu.
//
// policy/stats carry the issue #114 overflow choice: broadcast's overflow
// arm consults policy, and only OverflowDropOldest subscribers touch stats.
// A close-policy subscriber therefore behaves byte-identically to the
// pre-#114 struct — the new fields are inert there.
type subscriber struct {
	ch     chan string
	closed bool
	policy OverflowPolicy
	// stats is non-nil for EVERY subscriber — both constructors allocate
	// it — and stays exactly zero under OverflowClose, whose overflow path
	// signals by close and never counts. broadcast's drop arm is reachable
	// only for OverflowDropOldest subscribers, so the close-policy handle
	// is never written; callers of SubscribeOutputWithPolicy may read it
	// without a nil guard.
	stats *DropStats
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
// queue — up to 64 chunks, ~64 KB on a close-policy subscriber — onto a
// socket that has already stalled.
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
// queued chunk — up to 64 of them, ~64 KB on a close-policy subscriber —
// onto the socket that stalled in
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
// 1024-byte chunks and each close-policy subscriber queue holds 64 of them
// (subscriberQueueChunksClose), so such a consumer is disconnected after
// roughly 64 KB of output it has not taken. A drop-policy subscriber holds
// 4096 of them instead (决策 1b) and is never disconnected at all — for it
// this paragraph's threshold is not a teardown but the point at which the
// counted evictions begin.
// That is the price of this design, and it is the right price — recovery
// costs the client one reconnect (the browser retries after a hard-coded
// 5 s, index.html) and then resumes incrementally from its own cursor.
// The knob that would make the whole thing gentler is a write deadline on
// the TTY socket for HEALTHY connections, turning TCP backpressure into a
// bounded stall; that is deliberately NOT done here and belongs to the
// separate liveness issue #83 (app.go sets a deadline only on its own
// teardown write, so the close frame cannot re-block the handler). Raising
// the buffer capacity was likewise deliberately not done HERE, for the close
// policy: a bigger queue only postpones the disconnect of a consumer that
// cannot keep up, which is the correct outcome anyway. Issue #114 决策 1b
// later raised it for the DROP policy only — 4096 chunks, see
// defaultSubscriberQueueChunksDrop for the gap arithmetic — because a
// drop subscriber has no disconnect to postpone: capacity is the only thing
// that sets its eviction tempo, and at 64 chunks that tempo was the
// sub-second notice storm this issue is about. subscriberQueueChunksClose
// stays 64, and the argument in this paragraph still governs it verbatim.
//
// Dropping the chunk and keeping the subscriber registered — the old
// empty `default:` — was issue #82: a lost clear-screen or cursor move
// corrupts everything the client renders afterwards, unboundedly and with
// nothing on either side able to notice.
//
// Issue #114 adds a SECOND, negotiated answer to the same full queue: an
// OverflowDropOldest subscriber (see OverflowPolicy, SubscribeOutputWithPolicy)
// is kept registered and streaming, its oldest buffered chunks discarded to
// make room for the newest, every discard counted in its DropStats. That is
// NOT the #82 silence — the counts travel to the client as an in-band
// overflow notice (internal/app handleInstanceTTYWSLiveness), and the
// client resets and repaints from the tail instead of trusting a torn
// screen. It ships ONLY behind the `overflow-notice` capability, so every
// client that did not declare it still gets the close path above, verbatim,
// and every default-constructed subscriber (every plain SubscribeOutput)
// still carries OverflowClose.
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
			if sub.policy == OverflowDropOldest {
				// Issue #114 drop-oldest (issue #82's close path is kept
				// verbatim below for every other subscriber): make room for
				// the NEWEST chunk by discarding the OLDEST buffered one,
				// counted, channel kept open and subscriber kept in the
				// map. A channel cannot be popped from its head without a
				// receive, so the drop IS a non-blocking receive — and it
				// is bounded to one attempt per overflow because it cannot
				// fail to make room: the failed send just proved the queue
				// was full, every producer of this channel serialises
				// behind this same subsMu (all sends happen inside this
				// loop, under the lock), and the only concurrent actor left
				// is the consumer, which can only FREE slots. So either
				// this receive takes a chunk (one slot provably free) or
				// the consumer drained everything in between (even more
				// free) — and the retry send below takes it. The second
				// default arm counting the INCOMING chunk as dropped is
				// therefore defensive-unreachable, never silent: if it
				// ever fired, the loss lands in stats, not in the silence
				// issue #82 condemned.
				select {
				case dropped := <-sub.ch:
					sub.stats.Add(1, int64(len(dropped)))
				default:
				}
				select {
				case sub.ch <- chunk:
				default:
					// UNREACHABLE today, and kept to make the invariant
					// explicit rather than to handle a case: the failed send
					// above proved the queue full, every producer serialises
					// behind subsMu, and the consumer can only free slots —
					// so the receive just taken (or a consumer that drained
					// everything) provably leaves room for this retry. If it
					// ever did fire the loss still lands in stats, never in
					// the #82 silence; deleting the arm would assert the
					// invariant by making its violation invisible.
					sub.stats.Add(1, int64(len(chunk)))
				}
				continue
			}
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
	// The zero-value stats keeps the subscriber.stats invariant — non-nil
	// for every subscriber, zero under OverflowClose — true here too; this
	// path's overflow signals by close (issue #82) and never counts, so
	// the handle is inert exactly as the pre-#114 struct was.
	sub := &subscriber{ch: make(chan string, 64), stats: &DropStats{}}
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

// SubscribeOutputWithPolicy is SubscribeOutput with the overflow behaviour
// chosen per subscription (issue #114). It is strictly ADDITIVE: same
// registry, same exactly-once close contract, same idempotent cancel — the
// only differences are what a full queue does, and how big "full" is.
// OverflowClose selects today's behaviour, byte-for-byte, capacity included
// (subscriberQueueChunksClose = 64, the #82 number, untouched and
// env-proof), so the default policy of this API and of SubscribeOutput are
// the same thing. OverflowDropOldest gets the larger queue of 决策 1b
// (defaultSubscriberQueueChunksDrop = 4096, env-tunable), because a
// queue that only survives 0.2–0.6 s of burst would set the same sub-second
// reconnect tempo the fix exists to remove.
//
// Under OverflowDropOldest a full queue never closes the channel and never
// deletes the subscriber: broadcast discards the oldest buffered chunks to
// make room for the newest and counts them in the returned DropStats, so
// the consumer keeps streaming a lagging view whose loss is COUNTED, not
// silent — the app layer turns those counts into the in-band overflow
// notice the client resets on. The channel still closes on cancel, so
// "closed means resync" holds here too (cancel is the only closer for a
// drop subscriber); the consumer must still treat a close as resync.
//
// The returned *DropStats is always non-nil, zero for OverflowClose, and
// is the SAME handle broadcast counts into — readers see every discard the
// instant it happens, and always as the consistent pair the producer
// published (one atomic unit per Add; see DropStats).
//
// id is required, same as SubscribeOutput: an empty id is rejected so a
// buggy caller does not accidentally receive every PTY instance's output.
func SubscribeOutputWithPolicy(id string, policy OverflowPolicy) (<-chan string, func(), *DropStats, error) {
	if id == "" {
		return nil, nil, nil, errors.New("pty: SubscribeOutputWithPolicy requires instance id")
	}
	stats := &DropStats{}
	subsMu.Lock()
	if subs == nil {
		subs = map[string]map[*subscriber]struct{}{}
	}
	if subs[id] == nil {
		subs[id] = map[*subscriber]struct{}{}
	}
	// Capacity is policy-dependent (FIX-PLAN 决策 1b), and the two branches
	// are asymmetric on purpose: the close-policy capacity stays the 64 the
	// #82 contract is stated in — named at subscriberQueueChunksClose so the
	// number lives beside the code honouring it, and read from NOTHING else,
	// never from the environment, so no operator setting can move a #82
	// number — while only a drop subscriber consults
	// envSubscriberQueueChunksDrop, at this instant, per subscription (see
	// subscriberQueueChunksDrop for why resolving here is cheap and right).
	// A default-constructed subscriber — every plain SubscribeOutput, whose
	// own make(chan string, 64) is untouched — lands on OverflowClose and
	// therefore on 64 as well.
	capacity := subscriberQueueChunksClose
	if policy == OverflowDropOldest {
		capacity = subscriberQueueChunksDrop()
	}
	sub := &subscriber{ch: make(chan string, capacity), policy: policy, stats: stats}
	subs[id][sub] = struct{}{}
	subsMu.Unlock()
	cancel := func() {
		subsMu.Lock()
		defer subsMu.Unlock()
		delete(subs[id], sub)
		if len(subs[id]) == 0 {
			delete(subs, id)
		}
		// Same exactly-once contract as SubscribeOutput's cancel (see the
		// comment there): under OverflowClose an overflow may have closed
		// this channel first; under OverflowDropOldest broadcast never
		// closes it, so this is the only closer, but the flag makes cancel
		// idempotent here exactly where it makes it idempotent there, and
		// cancel must NOT drain for the same reason — the consumer is
		// entitled to what it queued.
		sub.closeLocked()
	}
	return sub.ch, cancel, stats, nil
}

// Register registers the PTY kind with the framework registry on
// package init.
func init() {
	framework.Register(Driver{})
}
