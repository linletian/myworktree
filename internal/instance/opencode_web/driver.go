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

	mu         sync.Mutex
	host       string
	port       string
	blob       Blob
	instanceID string
	failCount  atomic.Int32

	// publisher set by SetPublishers, called by kind to push state
	// changes back to the framework.
	publisher framework.Publisher
}

// Spawn launches opencode serve and parses its stdout for the
// listening address.
func (d Driver) Spawn(ctx context.Context, params framework.SpawnParams) (framework.Handle, *framework.ReadySignal, error) {
	cmd := exec.Command("opencode", "serve", "--hostname", "127.0.0.1", "--port", "0")
	cmd.Dir = params.WorktreePath
	cmd.Env = buildEnv(params.ExtraEnv, params.AuthToken)

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
func (Driver) SetPublishers(handle framework.Handle, p framework.Publisher) {
	h := mustHandle(handle)
	h.publisher = p
	h.AttachInstanceID(p.InstanceID())
}

// Stop terminates the opencode serve process.
func (d Driver) Stop(handle framework.Handle, graceSeconds int) error {
	h := mustHandle(handle)
	if h.cmd == nil || h.cmd.Process == nil {
		return nil
	}
	pid := h.cmd.Process.Pid
	_ = terminatePID(pid, syscall.SIGTERM)
	go func() {
		time.Sleep(stopGrace)
		_ = terminatePID(pid, syscall.SIGKILL)
	}()
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
	if err == nil && h.publisher != nil {
		_ = h.publisher.UpdateKindBlob(blob)
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
			if err == nil && h.publisher != nil {
				_ = h.publisher.UpdateKindBlob(blob)
			}
			h.ready.Close()
			if h.publisher != nil {
				_ = h.publisher.MarkRunning()
			}
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
				if h.publisher != nil {
					_ = h.publisher.MarkFailed("opencode health probe failed")
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
	if h.publisher != nil {
		_ = h.publisher.MarkExited(exitCode)
	}
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
	return &Handle{}
}

// init registers the opencode-web kind with the framework registry.
func init() {
	framework.Register(Driver{})
}
