// Package opencode_web implements the framework.Kind interface for
// opencode-web instances (the opencode AI coding agent's HTTP /
// browser UI, packaged as a managed background service).
//
// The package is intentionally standalone. It owns:
//
//   - the hardcoded `opencode serve` invocation
//   - env construction (forced OPENCODE_SERVER_PASSWORD = authToken,
//     forced OPENCODE_CLIENT = "myworktree")
//   - stdout scanning for the "listening on http://..." line that
//     signals the upstream port is ready
//   - the reverse proxy at /__opencode/<id>/... that injects Basic
//     auth and the ?directory=<worktree> query parameter
//   - the periodic /global/health probe that surfaces transient
//     failures as StatusFailed
//
// This package does NOT import the pty kind, the framework manager,
// or any other kind. The framework treats this as an opaque Kind
// registered under "opencode-web".
package opencode_web

import (
	"bufio"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"regexp"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"time"

	"myworktree/internal/framework"
	"myworktree/internal/redact"
)

// Tunables. Same values the pre-refactor manager used.
const (
	healthInterval      = 5 * time.Second
	healthFailThreshold = 3
	healthTimeout       = 3 * time.Second
	scanMaxLineBytes    = 1 << 20 // 1 MiB
	stopGrace           = 5 * time.Second
	readyTimeout        = 60 * time.Second
	// preStartTimeout bounds tag preStart execution (see Spawn). Long
	// enough for the documented `npm install` example, short enough that
	// a hanging template cannot stall a Start request indefinitely.
	preStartTimeout = 2 * time.Minute
)

// Driver is the opencode-web kind implementation. Registered with
// framework.Registry under "opencode-web".
type Driver struct{}

// Manifest returns the static KindInfo for the opencode-web kind.
func (Driver) Manifest() framework.KindInfo {
	return framework.KindInfo{
		Name:        "opencode-web",
		Label:       "Opencode-Web",
		Description: "Opencode AI agent with embedded web UI. Managed by myworktree; command and port are fixed.",
		Interactive: false,
	}
}

// Blob is the kind-private state persisted alongside the instance
// record. Opencode-web needs the listening address, the worktree
// path, and the iframe URL path so the frontend can build the
// reverse-proxy URL.
type Blob struct {
	Host        string `json:"host,omitempty"`
	Port        string `json:"port,omitempty"`
	WorktreeAbs string `json:"worktree_abs,omitempty"`
	IframeURL   string `json:"url_path,omitempty"`
	// Version is the installed opencode CLI version probed at spawn time;
	// VersionSupported reports whether it is within the range the injected
	// hide script targets. Advisory only — the instance still starts either
	// way (WORKTREE-ISOLATION.md §4.7 L1).
	Version          string `json:"version,omitempty"`
	VersionSupported bool   `json:"version_supported,omitempty"`
}

// Handle is the per-instance runtime state owned by the opencode-web
// kind.
type Handle struct {
	cmd       *exec.Cmd
	stdoutR   io.Reader
	cwd       string
	authToken string

	ready  *framework.ReadySignal
	cancel context.CancelFunc
	wg     *sync.WaitGroup

	// exited closes after the serve process has been reaped AND the
	// kind's final store write (MarkExited) has completed. Stop waits on
	// it so that when Stop returns, no goroutine can still be persisting
	// state for this instance (otherwise teardown of the data dir races
	// with MarkExited's state.json write — observed on macOS CI).
	exited chan struct{}

	mu         sync.Mutex
	host       string
	port       string
	blob       Blob
	instanceID string
	failCount  atomic.Int32

	// publisher set by SetPublishers, called by kind to push state
	// changes back to the framework. atomic.Pointer so SetPublishers
	// (called once after Spawn) and the readers in pumpAndWatch /
	// healthLoop / wait don't race on the bare field.
	publisher atomic.Pointer[framework.Publisher]
}

// Spawn launches opencode serve and parses its stdout for the
// listening address.
func (d Driver) Spawn(ctx context.Context, params framework.SpawnParams) (framework.Handle, *framework.ReadySignal, error) {
	cmd := exec.Command("opencode", "serve", "--hostname", "127.0.0.1", "--port", "0")
	cmd.Dir = params.WorktreePath
	cmd.Env = buildEnv(params.ExtraEnv, params.AuthToken)

	// Tag preStart runs with the same environment the serve process
	// will get (buildEnv output incl. the forced auth token). Its
	// failure output is surfaced to the API caller, so it must be
	// redacted first: a debug preStart (`env`, `printenv`) would
	// otherwise echo OPENCODE_SERVER_PASSWORD — the myworktree main
	// auth token — into the error. Bounded by preStartTimeout: Spawn
	// has not returned yet, so the framework's ready-timeout has not
	// started either, and a hanging template would stall Start forever.
	if strings.TrimSpace(params.PreStart) != "" {
		preCtx, cancel := context.WithTimeout(context.Background(), preStartTimeout)
		pre := exec.CommandContext(preCtx, "zsh", "-lc", params.PreStart)
		pre.Dir = cmd.Dir
		pre.Env = cmd.Env
		out, err := pre.CombinedOutput()
		cancel()
		if preCtx.Err() == context.DeadlineExceeded {
			return framework.Handle{}, nil, fmt.Errorf("preStart timed out after %s", preStartTimeout)
		}
		if err != nil {
			sanitized := strings.TrimSpace(redact.Secret(redact.Text(string(out)), params.AuthToken))
			return framework.Handle{}, nil, fmt.Errorf("preStart failed: %w: %s", err, sanitized)
		}
	}

	stdoutR, stdoutW := io.Pipe()
	cmd.Stdout = stdoutW
	cmd.Stderr = stdoutW

	if err := cmd.Start(); err != nil {
		stdoutW.Close()
		stdoutR.Close()
		return framework.Handle{}, nil, fmt.Errorf("opencode start: %w", err)
	}

	ready := framework.NewReadySignal()
	runCtx, cancel := context.WithCancel(context.Background())
	wg := &sync.WaitGroup{}
	wg.Add(3) // pumpAndWatch + healthLoop + wait (probeVersion is fire-and-forget: it exits via runCtx cancel, not the WaitGroup)

	h := &Handle{
		cmd:       cmd,
		stdoutR:   stdoutR,
		cwd:       params.WorktreePath,
		authToken: params.AuthToken,
		ready:     ready,
		cancel:    cancel,
		wg:        wg,
		exited:    make(chan struct{}),
		blob:      Blob{WorktreeAbs: params.WorktreePath},
	}

	go d.pumpAndWatch(runCtx, h, stdoutR)
	go d.healthLoop(runCtx, h)
	go d.wait(runCtx, h)
	go d.probeVersion(runCtx, h)

	return framework.NewHandle("opencode-web", h), ready, nil
}

// AttachInstanceID is called by the framework Manager immediately
// after Spawn, before any HTTP routes are registered. The instance
// ID is needed to build the iframe URL.
func (h *Handle) AttachInstanceID(id string) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.instanceID = id
}

// SetPublishers wires the framework Publisher the kind uses to push
// state transitions. Required because opencode-web's internal
// goroutines (pumpAndWatch, healthLoop, wait) learn about state
// transitions before the Manager's periodic Status() poll would
// notice — pushing inline keeps the state.json consistent.
//
// The Publisher is stored in an atomic.Pointer so the readers
// (pumpAndWatch, healthLoop, wait) don't have to take h.mu on every
// state transition; the data race detector would otherwise flag the
// bare-field read in those hot paths.
// SetPublishers must be called exactly once between Spawn returning and
// any goroutine starting (see framework/kind.go SetPublishers doc). A
// second call is a framework bug — surface it loudly.
func (Driver) SetPublishers(handle framework.Handle, p framework.Publisher) {
	h := mustHandle(handle)
	// atomic.Pointer[T].CompareAndSwap compares the stored *T (here
	// *framework.Publisher), so pass nil for the zero value, not a
	// pointer to a local interface variable.
	if !h.publisher.CompareAndSwap(nil, &p) {
		panic("opencode-web: SetPublishers called twice on the same handle (framework bug)")
	}
	h.AttachInstanceID(p.InstanceID())
	if h.cmd != nil && h.cmd.Process != nil {
		_ = p.SetPID(h.cmd.Process.Pid)
	}
}

// loadPublisher returns the Publisher (if any) for this handle.
func (h *Handle) loadPublisher() framework.Publisher {
	if p := h.publisher.Load(); p != nil {
		return *p
	}
	return nil
}

// Stop terminates the opencode serve process. graceSeconds is the
// time allowed between SIGTERM and SIGKILL; values <= 0 fall back to
// the package default (stopGrace). The framework passes its
// configured stopGraceSeconds through (see framework.Manager.Stop),
// so a user-configured shorter grace period actually shortens the
// wait — previously this was hardcoded and the parameter was dead.
//
// Stop blocks until the process has been reaped and the kind's final
// MarkExited store write has landed. Returning early (the previous
// behaviour: SIGKILL escalation was fire-and-forget) let the wait
// goroutine's state.json write race with whatever tears the instance
// down next — tests that remove the data dir right after Stop hit
// "directory not empty" on macOS.
func (d Driver) Stop(handle framework.Handle, graceSeconds int) error {
	h := mustHandle(handle)
	if h.cmd == nil || h.cmd.Process == nil {
		return nil
	}
	pid := h.cmd.Process.Pid
	grace := time.Duration(graceSeconds) * time.Second
	if grace <= 0 {
		grace = stopGrace
	}
	_ = terminatePID(pid, syscall.SIGTERM)
	select {
	case <-h.exited:
	case <-time.After(grace):
		_ = terminatePID(pid, syscall.SIGKILL)
		// The process is SIGKILLed; it cannot outlive this wait, so
		// blocking here is bounded. ESRCH from either signal just means
		// the process exited and wait() reaps it, closing h.exited.
		<-h.exited
	}
	return nil
}

// Status reports the opencode-web instance's current state.
func (d Driver) Status(handle framework.Handle) (framework.Status, string) {
	h := mustHandle(handle)
	if h.cmd == nil || h.cmd.Process == nil {
		return framework.StatusExited, ""
	}
	h.mu.Lock()
	host, port := h.host, h.port
	h.mu.Unlock()
	if host == "" || port == "" {
		return framework.StatusStarting, ""
	}
	if h.failCount.Load() >= int32(healthFailThreshold) {
		return framework.StatusFailed, "opencode health probe failed"
	}
	return framework.StatusRunning, ""
}

// ReadLogs: opencode-web does not buffer stdout for the log endpoint.
func (d Driver) ReadLogs(handle framework.Handle, since int64, maxBytes int64) (string, int64, error) {
	return "", since, nil
}

// KindBlob returns the persisted blob.
func (d Driver) KindBlob(handle framework.Handle) (json.RawMessage, error) {
	h := mustHandle(handle)
	h.mu.Lock()
	defer h.mu.Unlock()
	return json.Marshal(h.blob)
}

// HTTPHint returns the prefix where the proxy should be mounted. The
// opencode-web reverse proxy is registered globally in app.go (one handler
// for ALL instances at /__opencode/), not per-instance here, so this kind
// declares no per-instance HTTP surface and returns "".
func (d Driver) HTTPHint(instanceID string) string {
	return ""
}

// RegisterHTTP is a no-op: the opencode-web reverse proxy is registered
// globally in app.go, so no per-instance routes are wired here.
func (d Driver) RegisterHTTP(mux *http.ServeMux, instanceID string, handle framework.Handle) {}

// --- helpers ---

func buildEnv(tagEnv map[string]string, authToken string) []string {
	seen := make(map[string]string)
	for _, kv := range os.Environ() {
		k, v, _ := strings.Cut(kv, "=")
		seen[k] = v
	}
	for k, v := range tagEnv {
		seen[k] = v
	}
	seen["OPENCODE_SERVER_PASSWORD"] = authToken
	seen["OPENCODE_CLIENT"] = "myworktree"

	out := make([]string, 0, len(seen))
	for k, v := range seen {
		out = append(out, k+"="+v)
	}
	return out
}

var listeningAddrRe = regexp.MustCompile(`opencode server listening on http://([^:\s]+):(\d+)`)

func extractListeningAddress(line string) (host, port string, ok bool) {
	clean := stripANSI(line)
	m := listeningAddrRe.FindStringSubmatch(clean)
	if len(m) != 3 {
		return "", "", false
	}
	return m[1], m[2], true
}

var ansiRe = regexp.MustCompile(`\x1b\[[0-9;]*[a-zA-Z]`)

func stripANSI(s string) string {
	return strings.TrimSpace(ansiRe.ReplaceAllString(s, ""))
}

func isAPIPath(p string) bool {
	p = strings.TrimRight(p, "/")
	if p == "" {
		p = "/"
	}
	if strings.HasPrefix(p, "/api") || strings.HasPrefix(p, "/global") {
		return true
	}
	switch p {
	case "/", "/doc", "/config", "/session", "/agent", "/command", "/skill",
		"/lsp", "/formatter", "/mcp", "/provider", "/project",
		"/experimental", "/tui", "/vcs", "/path", "/instance",
		"/file", "/find", "/event", "/log", "/auth":
		return true
	}
	return false
}

func basicAuth(user, pass string) string {
	auth := user + ":" + pass
	return base64.StdEncoding.EncodeToString([]byte(auth))
}

func terminatePID(pid int, sig syscall.Signal) error {
	if pid <= 0 {
		return errors.New("invalid pid")
	}
	if err := syscall.Kill(pid, sig); err == nil || errors.Is(err, syscall.ESRCH) {
		return nil
	}
	if p, perr := os.FindProcess(pid); perr == nil {
		if err := p.Signal(sig); err != nil && !errors.Is(err, syscall.ESRCH) {
			return err
		}
	}
	return nil
}

// --- lifecycle goroutines ---

// probeVersion runs `opencode --version` and records the result (advisory
// only) so the frontend can warn when the installed version is outside the
// range the injected hide script targets. A failed or unparseable probe is
// tolerated (Version stays empty → treated as supported).
func (d Driver) probeVersion(ctx context.Context, h *Handle) {
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	out, err := exec.CommandContext(ctx, "opencode", "--version").Output()
	if err != nil {
		return
	}
	v, _ := parseVersion(string(out))
	if v == "" {
		return
	}
	h.mu.Lock()
	h.blob.Version = v
	h.blob.VersionSupported = isSupportedVersion(v)
	blob, err := json.Marshal(h.blob)
	h.mu.Unlock()
	if err == nil {
		if p := h.loadPublisher(); p != nil {
			_ = p.UpdateKindBlob(blob)
		}
	}
}

func (d Driver) pumpAndWatch(ctx context.Context, h *Handle, r io.Reader) {
	defer h.wg.Done()
	sc := bufio.NewScanner(r)
	sc.Buffer(make([]byte, 0, 64*1024), scanMaxLineBytes)
	for sc.Scan() {
		if ctx.Err() != nil {
			return
		}
		line := redact.Text(sc.Text())

		host, port, ok := extractListeningAddress(line)
		if ok {
			h.mu.Lock()
			h.host = host
			h.port = port
			h.blob.Host = host
			h.blob.Port = port
			if h.instanceID != "" {
				// Full-page embed (WORKTREE-ISOLATION.md §0.4): load the
				// SPA root, not the /:dir/session/:id deep link. The SPA
				// Router matches HomeRoute when the shim strips the proxy
				// prefix; deep-linking a session page has repeatedly failed
				// (blank screen — see DEBUG.md).
				h.blob.IframeURL = "/__opencode/" + h.instanceID + "/"
			}
			// Marshal inside the lock so it never races with probeVersion,
			// which writes h.blob.Version under the same mutex.
			blob, err := json.Marshal(h.blob)
			h.mu.Unlock()
			if err == nil {
				if p := h.loadPublisher(); p != nil {
					_ = p.UpdateKindBlob(blob)
				}
			}
			h.ready.Close()
			if p := h.loadPublisher(); p != nil {
				_ = p.MarkRunning()
			}
			// Drain remaining stdout: the scanner stops reading once we
			// return, and stdoutR is an io.PipeReader (OS pipe under the
			// hood, ~64 KiB on Linux). If the child process keeps writing
			// after we've parsed the listening line, the pipe fills and
			// opencode blocks on its next write(2) — leaving the UI
			// "stuck" with no obvious cause. Discard until EOF / ctx cancel.
			_, _ = io.Copy(io.Discard, r)
			return
		}
	}
}

func (d Driver) healthLoop(ctx context.Context, h *Handle) {
	defer h.wg.Done()
	ticker := time.NewTicker(healthInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			h.mu.Lock()
			host, port := h.host, h.port
			h.mu.Unlock()
			if host == "" || port == "" {
				continue
			}
			ok := probeHealth(ctx, host, port, h.authToken)
			if ok {
				h.failCount.Store(0)
				continue
			}
			n := h.failCount.Add(1)
			if int(n) >= healthFailThreshold {
				if p := h.loadPublisher(); p != nil {
					_ = p.MarkFailed("opencode health probe failed")
				}
				return
			}
		}
	}
}

func (d Driver) wait(ctx context.Context, h *Handle) {
	defer h.wg.Done()
	_ = h.cmd.Wait()
	h.cancel()
	exitCode := -1
	if h.cmd.ProcessState != nil {
		exitCode = h.cmd.ProcessState.ExitCode()
	}
	if p := h.loadPublisher(); p != nil {
		_ = p.MarkExited(exitCode)
	}
	// Close AFTER MarkExited: Stop waits on this channel, so by the time
	// Stop returns the final store write is done and no further writes
	// for this instance can happen.
	close(h.exited)
}

func probeHealth(ctx context.Context, host, port, authToken string) bool {
	addr := "http://" + host + ":" + port + "/global/health"
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, addr, nil)
	if err != nil {
		return false
	}
	req.SetBasicAuth("opencode", authToken)
	client := &http.Client{Timeout: healthTimeout}
	resp, err := client.Do(req)
	if err != nil {
		return false
	}
	defer resp.Body.Close()
	return resp.StatusCode < 400
}

func mustHandle(h framework.Handle) *Handle {
	if hh, ok := h.Unwrap().(*Handle); ok {
		return hh
	}
	panic("opencode-web: framework.Handle inner is not *Handle")
}

// init registers the opencode-web kind with the framework registry.
func init() {
	framework.Register(Driver{})
}
