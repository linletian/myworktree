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
	"sync"
	"syscall"

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
	cmd    *exec.Cmd
	ptmx   *os.File
	buf    *framework.RingBuffer

	cancel context.CancelFunc
	wg     *sync.WaitGroup
}

// blob is the persisted blob schema. PTY has no kind-private state
// beyond what ManagedInstance already stores, so the blob is empty.
type blob struct{}

// Spawn launches a zsh shell inside the worktree path.
func (d Driver) Spawn(ctx context.Context, params framework.SpawnParams) (framework.Handle, *framework.ReadySignal, error) {
	cmd := exec.Command("zsh", "-f", "-i")
	cmd.Dir = params.WorktreePath
	cmd.Env = os.Environ()
	for k, v := range params.ExtraEnv {
		cmd.Env = append(cmd.Env, k+"="+v)
	}

	ptmx, err := pty.Start(cmd)
	if err != nil {
		return framework.Handle{}, nil, fmt.Errorf("pty start: %w", err)
	}

	buf := framework.NewRingBuffer(framework.DefaultBufferCap)

	runCtx, cancel := context.WithCancel(context.Background())
	wg := &sync.WaitGroup{}
	wg.Add(2)

	h := &Handle{
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
// that receives redacted PTY output chunks. framework.Manager calls
// this via an outputSub type assertion when a WebSocket client
// subscribes to the instance's live output stream.
func (d Driver) SubscribeOutput() (<-chan string, func(), error) {
	return SubscribeOutput()
}

// --- handle internals ---

func (h *Handle) pumpLogs() {
	defer h.wg.Done()
	readBuf := make([]byte, 1024)
	for {
		n, err := h.ptmx.Read(readBuf)
		if n > 0 {
			chunk := redact.Text(string(readBuf[:n]))
			if h.buf != nil {
				h.buf.WriteString(chunk)
			}
			broadcast(chunk)
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

var (
	subsMu sync.Mutex
	subs   map[chan string]struct{}
)

func broadcast(chunk string) {
	if chunk == "" {
		return
	}
	subsMu.Lock()
	defer subsMu.Unlock()
	for ch := range subs {
		select {
		case ch <- chunk:
		default:
		}
	}
}

// SubscribeOutput is exposed to the websocket handler.
func SubscribeOutput() (<-chan string, func(), error) {
	subsMu.Lock()
	if subs == nil {
		subs = map[chan string]struct{}{}
	}
	ch := make(chan string, 64)
	subs[ch] = struct{}{}
	subsMu.Unlock()
	cancel := func() {
		subsMu.Lock()
		delete(subs, ch)
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
