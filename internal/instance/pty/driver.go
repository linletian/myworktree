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

// ReadLogs returns captured PTY output.
func (d Driver) ReadLogs(handle framework.Handle, since int64, maxBytes int64) (string, int64, error) {
	h := mustHandle(handle)
	if h.buf == nil {
		return "", since, nil
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
var (
	subsMu sync.Mutex
	subs   map[string]map[chan string]struct{}
)

func broadcast(id string, chunk string) {
	if chunk == "" || id == "" {
		return
	}
	subsMu.Lock()
	defer subsMu.Unlock()
	for ch := range subs[id] {
		select {
		case ch <- chunk:
		default:
		}
	}
}

// SubscribeOutput registers a channel for one instance's PTY output
// and returns the channel and a cancel func that unsubscribes.
//
// id is required: an empty id is rejected so a buggy caller does not
// accidentally receive every PTY instance's output.
func SubscribeOutput(id string) (<-chan string, func(), error) {
	if id == "" {
		return nil, nil, errors.New("pty: SubscribeOutput requires instance id")
	}
	subsMu.Lock()
	if subs == nil {
		subs = map[string]map[chan string]struct{}{}
	}
	if subs[id] == nil {
		subs[id] = map[chan string]struct{}{}
	}
	ch := make(chan string, 64)
	subs[id][ch] = struct{}{}
	subsMu.Unlock()
	cancel := func() {
		subsMu.Lock()
		delete(subs[id], ch)
		if len(subs[id]) == 0 {
			delete(subs, id)
		}
		subsMu.Unlock()
		close(ch)
	}
	return ch, cancel, nil
}

// Register registers the PTY kind with the framework registry on
// package init.
func init() {
	framework.Register(Driver{})
}
