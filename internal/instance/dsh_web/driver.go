// Package dsh_web implements the framework.Kind interface for dsh-web
// instances (the DeepSeek Harness browser UI, `dsh web`, packaged as a
// managed background service — PLAN.md).
//
// The package is intentionally standalone, modeled on opencode_web. It
// owns:
//
//   - the hardcoded `dsh web --host 127.0.0.1 --port 0 --patch
//     <restrict.yml>` invocation (plus the npx / install launch modes)
//   - the per-instance state dir (<DataDir>/dsh/<id>/: restrict.yml,
//     launch.json)
//   - the version gate (`dsh --version` hard-gated below 0.1.0) and
//     the advisory supported range
//   - the L2 overlay verification via `dsh web --dump-config`
//   - stdout scanning for the `dsh web: http://127.0.0.1:<port>` ready
//     line
//   - the health probe (GET / — the SPA index always answers 200)
//   - the optional per-instance reverse-proxy listener, plugged in via
//     Driver.ProxyStarter (proxy.go)
//
// Data-plane isolation (registry per worktree, shared sessions pool)
// is entirely expressed by the restrict overlay — see overlay.go.
//
// This package does NOT import the pty kind, the framework manager, or
// any other kind. The framework treats this as an opaque Kind
// registered under "dsh-web".
package dsh_web

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"time"

	"myworktree/internal/framework"
	"myworktree/internal/gitx"
	"myworktree/internal/redact"
)

// Tunables. Same values the opencode-web driver uses where the
// behaviour is shared.
const (
	kindName = "dsh-web"

	healthInterval       = 5 * time.Second
	healthFailThreshold  = 3
	healthTimeout        = 3 * time.Second
	scanMaxLineBytes     = 1 << 20 // 1 MiB
	stopGrace            = 5 * time.Second
	preStartTimeout      = 2 * time.Minute
	versionProbeTimeout  = 5 * time.Second
	overlayVerifyTimeout = 90 * time.Second // npx mode may download the pinned package on first run
)

// Blob is the kind-private state persisted alongside the instance
// record. The frontend / API read host/port/proxy_* to build the
// iframe; the advisory fields drive the warning bar.
type Blob struct {
	Host        string `json:"host,omitempty"`
	Port        string `json:"port,omitempty"`
	ProxyHost   string `json:"proxy_host,omitempty"` // set by the reverse-proxy listener (proxy.go)
	ProxyPort   string `json:"proxy_port,omitempty"`
	IframeURL   string `json:"iframe_url,omitempty"`
	WorktreeAbs string `json:"worktree_abs,omitempty"`

	// Version is the installed dsh core version probed at spawn time;
	// VersionSupported reports whether it is within the range the
	// restrict overlay rows / WS paths / ready line are verified
	// against. Advisory — the instance still starts either way.
	Version          string `json:"version,omitempty"`
	VersionSupported bool   `json:"version_supported,omitempty"`

	// RemoteCapable reports whether the dsh core version satisfies the
	// remote-access floor (isRemoteCapable). Drives the frontend's
	// remote-mode affordances.
	RemoteCapable bool `json:"remote_capable"`

	// OverlayVerified is the L2 check result: the spawn-time
	// --dump-config run confirmed the restrict overlay rows are
	// composed with the expected values. false → frontend shows the
	// "裁剪失效" (restriction not effective) warning.
	OverlayVerified bool `json:"overlay_verified,omitempty"`

	// WorkspaceID is the worktree's own workspace id learned from the
	// bootstrap workspace.create response (bootstrap.go); scope
	// classification by workspaceId compares against it.
	WorkspaceID string `json:"workspace_id,omitempty"`
}

// Driver implements framework.Kind for dsh-web instances.
type Driver struct {
	// DataDir is the myworktree project data dir; per-instance state
	// lives under <DataDir>/dsh/<instanceID>/ and the per-worktree
	// storages root under <DataDir>/dsh/<worktreeHash>/.
	DataDir string
	// Logger receives driver diagnostics (nil = discard).
	Logger *log.Logger
	// DshBin overrides the resolved dsh executable (tests). An empty
	// value means resolve via launch mode / LookPath.
	DshBin string

	// Proxy is the per-instance reverse-proxy configuration (bind
	// host, token gate, TLS mirror). Filled by app.New.
	Proxy ProxyConfig

	// Tracker receives the out-of-scope observations the reverse proxy
	// records (shared with the API's scope endpoint). Filled by
	// app.New; nil disables recording.
	Tracker *ScopeTracker

	// SessionWatch observes the shared dsh sessions pool for sessions
	// actively written by OTHER processes (sessionwatch.go). The proxy
	// and the workspace bootstrap feed it the session ids this daemon
	// itself drives (MarkOwn). Filled by app.New; nil disables the
	// foreign-activity advisory.
	SessionWatch *SessionWatch

	// ProxyStarter is the proxy.go seam: called once the upstream
	// listening address is known (after the ready line is parsed). It
	// starts the per-instance reverse-proxy listener and returns its
	// address plus an idempotent close function. nil keeps the
	// instance proxy-less (blob has no proxy_* fields). app.New wires
	// it via ProxyStarterFn().
	ProxyStarter func(h *Handle, upstreamHost, upstreamPort string) (host, port string, closeFn func(), err error)

	// verMemo memoizes successful version probes per binary path
	// (reasonix issue-#45 pattern): a gate that already passed does not
	// re-probe on every instance start.
	verMu   sync.Mutex
	verMemo map[string]string
}

// Handle is the per-instance runtime state owned by the dsh-web kind.
type Handle struct {
	cmd        *exec.Cmd
	stdoutR    io.Reader
	cwd        string
	instanceID string
	stateDir   string
	killGroup  bool // npx mode: Stop signals the whole process group

	ready  *framework.ReadySignal
	cancel context.CancelFunc
	wg     *sync.WaitGroup

	// exited closes after the serve process has been reaped AND the
	// kind's final store write (MarkExited) has completed. Stop waits on
	// it so that when Stop returns, no goroutine can still be persisting
	// state for this instance.
	exited chan struct{}

	mu    sync.Mutex
	host  string
	port  string
	token string // upstream browser-auth token; CREDENTIAL — never serialized into Blob, never logged
	// auth is the SINGLE upstream auth relay (auth.go) shared with the
	// reverse proxy (proxy.go). Write-once: pumpAndWatch assigns it in
	// the same critical section as host/port/token, BEFORE
	// h.ready.Close(), and never mutates it afterwards. Readers go
	// through upstreamAuthRelay() — callers gated on
	// <-h.ready.Channel() (bootstrap.go) are ordered after the write by
	// the ready close (write-once, read-after-ready); the lock makes
	// the discipline race-free for every other call timing.
	auth       *upstreamAuth
	proxyHost  string
	proxyPort  string
	proxyClose func() // idempotent (proxyOnce)
	proxyOnce  sync.Once
	// proxyDead marks the instance failed because the per-instance
	// reverse-proxy listener died on its own (proxy.go). deathReason
	// carries the failure for wait() to persist alongside the exit.
	proxyDead   bool
	deathReason string
	// watch is the driver-level SessionWatch (foreign-activity
	// advisory); the proxy and bootstrap report own-traffic to it.
	watch     *SessionWatch
	blob      Blob
	failCount atomic.Int32
	publisher atomic.Pointer[framework.Publisher]
}

// upstreamAuthToken returns the browser-auth token captured from the
// ready line ("" on legacy dsh). The token is a credential: callers
// must never log it or persist it into the Blob.
func (h *Handle) upstreamAuthToken() string {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.token
}

// upstreamAuthRelay returns the shared upstream auth relay built by
// pumpAndWatch from the ready-line token (auth.go). Never nil for a
// Spawn-ed instance once the ready line was parsed; nil only on Handles
// constructed outside Spawn (tests). The relay itself is safe for
// concurrent use; the lock here guards the write-once assignment.
func (h *Handle) upstreamAuthRelay() *upstreamAuth {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.auth
}

// markOwnSession reports a session id driven by THIS instance to the
// shared session watch (nil-safe).
func (h *Handle) markOwnSession(sessionID string) {
	if h.watch != nil {
		h.watch.MarkOwn(sessionID)
	}
}

// Manifest returns the static KindInfo for the dsh-web kind.
func (d *Driver) Manifest() framework.KindInfo {
	return framework.KindInfo{
		Name:        kindName,
		Label:       "DSH-Web",
		Description: "DeepSeek Harness agent with embedded web UI. Managed by myworktree; command and port are fixed.",
		Interactive: false,
	}
}

// Spawn launches `dsh web` and parses its stdout for the ready line.
// NeedsOutputBuffer reports that this kind never captures PTY-style
// output into the framework ring buffer, so Start skips the 16–256 MB
// pre-allocation (framework.BufferConsumer).
func (d *Driver) NeedsOutputBuffer() bool { return false }

func (d *Driver) Spawn(ctx context.Context, params framework.SpawnParams) (framework.Handle, *framework.ReadySignal, error) {
	// Per-instance state dir (restrict overlay + launch.json).
	stateDir := filepath.Join(d.DataDir, "dsh", params.InstanceID)
	if err := os.MkdirAll(stateDir, 0o755); err != nil {
		return framework.Handle{}, nil, fmt.Errorf("dsh: create state dir: %w", err)
	}

	// Launch mode (persisted per-worktree choice from the
	// missing-dependency dialog, or the default path mode).
	launch, err := readLaunch(d.DataDir, params.WorktreePath)
	if err != nil {
		return framework.Handle{}, nil, err
	}
	exe, prefix, killGroup, err := resolveLaunch(d.DshBin, launch)
	if err != nil {
		return framework.Handle{}, nil, err
	}

	// Version gate (PLAN.md §版本门). npx mode is pinned to an exact
	// version — the gate is trivially satisfied and the pin is recorded
	// as the probed version. path/install modes probe the resolved bin
	// and fail fast below 0.1.0.
	var version string
	versionSupported := true
	remoteCapable := false
	if killGroup {
		version = NpxPin
		versionSupported = isSupportedVersion(NpxPin)
		remoteCapable = isRemoteCapable(NpxPin)
	} else {
		got, gerr := d.probeAndGate(exe)
		if gerr != nil {
			return framework.Handle{}, nil, gerr
		}
		version = got
		versionSupported = isSupportedVersion(got)
		remoteCapable = isRemoteCapable(got)
	}

	// Restrict overlay: per-worktree storages root (registry isolation)
	// + disabled directory picker / hmr rows. The hash keeps instances
	// of the SAME worktree sharing one registry while different
	// worktrees stay apart.
	storagesRoot := filepath.Join(d.DataDir, "dsh", gitx.HashPath(params.WorktreePath), "storages")
	overlayPath := filepath.Join(stateDir, "restrict.yml")
	// 0o600 like launch.json: the overlay carries the worktree path and
	// the per-worktree storages root — not world-readable on shared
	// machines.
	if err := os.WriteFile(overlayPath, buildRestrictOverlay(storagesRoot), 0o600); err != nil {
		return framework.Handle{}, nil, fmt.Errorf("dsh: write restrict overlay: %w", err)
	}

	// L2 overlay verification (PLAN.md §裁剪有效性兜底): the composed
	// tree must carry our rows. Advisory — a failure logs and sets
	// OverlayVerified=false; the frontend warns. In npx mode this also
	// fails fast when the pinned package cannot be resolved (instead of
	// a 60s ready-timeout).
	overlayVerified := d.verifyOverlay(exe, prefix, overlayPath, storagesRoot)
	if !overlayVerified {
		d.logf("instance %s: restrict overlay NOT verified via --dump-config (rows may have drifted with the dsh version; frontend shows 裁剪失效)", params.InstanceID)
	}

	// Tag preStart runs with the same environment the web process will
	// get; failure output is redacted before surfacing (a debug
	// preStart could echo tag env secrets). Bounded by preStartTimeout
	// (Spawn has not returned, so the framework ready-timeout has not
	// started either).
	if strings.TrimSpace(params.PreStart) != "" {
		preCtx, cancel := context.WithTimeout(context.Background(), preStartTimeout)
		pre := exec.CommandContext(preCtx, "zsh", "-lc", params.PreStart)
		pre.Dir = params.WorktreePath
		pre.Env = buildEnv(params.ExtraEnv)
		out, err := pre.CombinedOutput()
		cancel()
		if preCtx.Err() == context.DeadlineExceeded {
			return framework.Handle{}, nil, fmt.Errorf("preStart timed out after %s", preStartTimeout)
		}
		if err != nil {
			sanitized := strings.TrimSpace(redact.Secret(redact.Text(string(out)), ""))
			return framework.Handle{}, nil, fmt.Errorf("preStart failed: %w: %s", err, sanitized)
		}
	}

	stdoutR, stdoutW := io.Pipe()
	cmd := exec.Command(exe, append(append([]string{}, prefix...), webArgs(overlayPath)...)...)
	cmd.Dir = params.WorktreePath
	cmd.Env = buildEnv(params.ExtraEnv)
	cmd.Stdout = stdoutW
	cmd.Stderr = stdoutW
	if killGroup {
		// npx is dsh's parent: the whole group must be killed on Stop,
		// otherwise the orphaned server keeps its port.
		cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	}
	if err := cmd.Start(); err != nil {
		stdoutW.Close()
		stdoutR.Close()
		return framework.Handle{}, nil, fmt.Errorf("dsh start: %w", err)
	}

	ready := framework.NewReadySignal()
	runCtx, cancel := context.WithCancel(context.Background())
	wg := &sync.WaitGroup{}
	wg.Add(4) // pumpAndWatch + healthLoop + wait + bootstrap

	h := &Handle{
		cmd:        cmd,
		stdoutR:    stdoutR,
		cwd:        params.WorktreePath,
		instanceID: params.InstanceID,
		stateDir:   stateDir,
		killGroup:  killGroup,
		watch:      d.SessionWatch,
		ready:      ready,
		cancel:     cancel,
		wg:         wg,
		exited:     make(chan struct{}),
		blob: Blob{
			WorktreeAbs:      params.WorktreePath,
			Version:          version,
			VersionSupported: versionSupported,
			RemoteCapable:    remoteCapable,
			OverlayVerified:  overlayVerified,
		},
	}

	go d.pumpAndWatch(runCtx, h, stdoutR)
	go d.healthLoop(runCtx, h)
	go d.wait(runCtx, h)
	go d.bootstrap(runCtx, h)

	return framework.NewHandle(kindName, h), ready, nil
}

// SetPublishers wires the framework Publisher the kind uses to push
// state transitions (see opencode_web.SetPublishers for the reasoning
// behind the atomic.Pointer).
func (d *Driver) SetPublishers(handle framework.Handle, p framework.Publisher) {
	h := mustHandle(handle)
	if !h.publisher.CompareAndSwap(nil, &p) {
		panic("dsh-web: SetPublishers called twice on the same handle (framework bug)")
	}
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

// Stop terminates the dsh web process. graceSeconds is the time
// allowed between SIGTERM and SIGKILL; values <= 0 fall back to the
// package default. npx mode signals the whole process group (negative
// pid). Stop also closes the per-instance reverse proxy listener and
// blocks until the process has been reaped and the final store write
// landed (see opencode_web.Stop for the race this guards).
func (d *Driver) Stop(handle framework.Handle, graceSeconds int) error {
	h := mustHandle(handle)
	if h.cmd == nil || h.cmd.Process == nil {
		return nil
	}
	pid := h.cmd.Process.Pid
	grace := time.Duration(graceSeconds) * time.Second
	if grace <= 0 {
		grace = stopGrace
	}
	_ = killPID(sigTarget(pid, h.killGroup), syscall.SIGTERM)
	select {
	case <-h.exited:
	case <-time.After(grace):
		_ = killPID(sigTarget(pid, h.killGroup), syscall.SIGKILL)
		<-h.exited
	}
	h.closeProxy()
	return nil
}

// sigTarget returns the pid to signal: the process group (negative)
// in npx mode, the plain pid otherwise.
func sigTarget(pid int, killGroup bool) int {
	if killGroup {
		return -pid
	}
	return pid
}

// Status reports the dsh-web instance's current state.
func (d *Driver) Status(handle framework.Handle) (framework.Status, string) {
	h := mustHandle(handle)
	if h.cmd == nil || h.cmd.Process == nil {
		return framework.StatusExited, ""
	}
	h.mu.Lock()
	host, port, proxyDead := h.host, h.port, h.proxyDead
	h.mu.Unlock()
	if proxyDead {
		return framework.StatusFailed, "dsh reverse proxy listener died"
	}
	if host == "" || port == "" {
		return framework.StatusStarting, ""
	}
	if h.failCount.Load() >= int32(healthFailThreshold) {
		return framework.StatusFailed, "dsh health probe failed"
	}
	return framework.StatusRunning, ""
}

// ReadLogs: dsh-web does not buffer stdout for the log endpoint.
func (d *Driver) ReadLogs(handle framework.Handle, since int64, maxBytes int64) (string, int64, error) {
	return "", since, nil
}

// KindBlob returns the persisted blob.
func (d *Driver) KindBlob(handle framework.Handle) (json.RawMessage, error) {
	h := mustHandle(handle)
	h.mu.Lock()
	defer h.mu.Unlock()
	return json.Marshal(h.blob)
}

// HTTPHint returns "": the per-instance reverse proxy is a
// kind-internal loopback listener (proxy.go), not a framework HTTP
// route.
func (d *Driver) HTTPHint(instanceID string) string { return "" }

// RegisterHTTP is a no-op (see HTTPHint).
func (d *Driver) RegisterHTTP(mux *http.ServeMux, instanceID string, handle framework.Handle) {}

// Cleanup wipes the per-instance state dir (restrict.yml). The
// framework calls it after Delete and after Restart migrates onto a
// fresh id (the old id is never reused). The per-worktree launch.json
// is deliberately NOT touched — it is the durable launch-mode choice.
func (d *Driver) Cleanup(instanceID string) error {
	if d.DataDir == "" || instanceID == "" {
		return nil
	}
	return os.RemoveAll(filepath.Join(d.DataDir, "dsh", instanceID))
}

// --- helpers ---

func (d *Driver) logf(format string, args ...any) {
	if d.Logger != nil {
		d.Logger.Printf(format, args...)
	}
}

// buildEnv merges tag env over the inherited environment. dsh needs no
// forced secrets (no upstream auth); the token gate lives at the
// reverse proxy (proxy.go).
func buildEnv(tagEnv map[string]string) []string {
	seen := make(map[string]string)
	for _, kv := range os.Environ() {
		k, v, _ := strings.Cut(kv, "=")
		seen[k] = v
	}
	for k, v := range tagEnv {
		seen[k] = v
	}
	out := make([]string, 0, len(seen))
	for k, v := range seen {
		out = append(out, k+"="+v)
	}
	return out
}

// probeAndGate runs `bin --version`, applies the hard gate, and
// returns the parsed core version ("" for unparseable output — a dev
// build is logged and tolerated, matching the reasonix gate). Passes
// are memoized per binary path so repeated instance starts do not
// re-probe; failures re-probe every time.
func (d *Driver) probeAndGate(bin string) (string, error) {
	d.verMu.Lock()
	if v, ok := d.verMemo[bin]; ok {
		d.verMu.Unlock()
		return v, nil
	}
	d.verMu.Unlock()

	ctx, cancel := context.WithTimeout(context.Background(), versionProbeTimeout)
	defer cancel()
	out, err := exec.CommandContext(ctx, bin, "--version").Output()
	if err != nil {
		return "", fmt.Errorf("dsh: version probe `%s --version` failed: %w", bin, err)
	}
	got, ok := parseVersion(string(out))
	if !ok {
		d.logf("could not parse dsh version from %q; continuing without version gate", strings.TrimSpace(string(out)))
		return "", nil
	}
	if !hardVersionOK(got) {
		return "", fmt.Errorf("dsh: version %s installed, but dsh >= %s required (the embedded web UI relies on --port 0, --patch and the web profile rows); upgrade @deepseek-ai/dsh or use the pinned npx launch (%s)", got, minVersion, NpxPin)
	}
	d.verMu.Lock()
	if d.verMemo == nil {
		d.verMemo = map[string]string{}
	}
	d.verMemo[bin] = got
	d.verMu.Unlock()
	return got, nil
}

// Capability is the dsh toolchain report for a worktree: what the
// /api/dsh/capability endpoint serves so the frontend knows whether
// remote mode can be offered BEFORE any instance is spawned.
type Capability struct {
	Mode             string // persisted launch mode: path | npx | install
	Version          string // probed core version ("" = unparseable dev build)
	RemoteCapable    bool   // Version satisfies the remote-access floor
	MinRemoteVersion string // the floor itself (RemoteMinVersion)
	Missing          bool   // no launchable dsh (resolution or probe failed)
	NpmAvailable     bool   // npm CLI on PATH (the install-now affordance)
	SuggestedPin     string // npx pin to suggest when dsh is missing
}

// ProbeCapability resolves the worktree's persisted launch mode and
// reports the dsh capability without spawning anything. Path/install
// modes reuse the memoized spawn-time probe (a binary the gate already
// passed costs nothing); npx mode needs no probe — the pin is exact.
func (d *Driver) ProbeCapability(worktreePath string) Capability {
	cfg, err := readLaunch(d.DataDir, worktreePath)
	if err != nil {
		d.logf("dsh: capability probe: %v; assuming default launch mode", err)
		cfg = launchConfig{Mode: launchPath}
	}
	capab := Capability{
		Mode:             string(cfg.Mode),
		MinRemoteVersion: RemoteMinVersion(),
	}
	// Every Missing report carries the dialog facts (npm availability +
	// suggested pin) uniformly: an unlaunchable dsh — unresolved OR
	// failing its version probe — drives the same three-way dialog.
	missing := func() Capability {
		capab.Missing = true
		capab.NpmAvailable = NpmAvailable()
		capab.SuggestedPin = NpxPin
		return capab
	}
	exe, _, _, err := resolveLaunch(d.DshBin, cfg)
	if err != nil {
		var nf *ErrDshNotFound
		if !errors.As(err, &nf) {
			d.logf("dsh: capability probe: resolve launch: %v", err)
		}
		return missing()
	}
	if cfg.Mode == launchNpx {
		capab.Version = NpxPin
		capab.RemoteCapable = isRemoteCapable(NpxPin)
		return capab
	}
	got, err := d.probeAndGate(exe)
	if err != nil {
		d.logf("dsh: capability probe: %v", err)
		return missing()
	}
	capab.Version = got
	capab.RemoteCapable = isRemoteCapable(got)
	return capab
}

// verifyOverlay runs the boot-free config dump with our overlay and
// checks the composed tree for the expected rows (L2, PLAN.md
// §裁剪有效性兜底). prefix carries the npx preamble in npx mode.
func (d *Driver) verifyOverlay(exe string, prefix []string, overlayPath, storagesRoot string) bool {
	ctx, cancel := context.WithTimeout(context.Background(), overlayVerifyTimeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, exe, append(append([]string{}, prefix...), dumpArgs(overlayPath)...)...)
	out, err := cmd.CombinedOutput()
	if err != nil {
		d.logf("dsh: overlay verification failed: %v\n%s", err, strings.TrimSpace(redact.Text(string(out))))
		return false
	}
	return verifyOverlayDump(string(out), storagesRoot)
}

// closeProxy closes the per-instance reverse proxy listener exactly
// once (idempotent across wait() and Stop()). The function pointer is
// written by pumpAndWatch under h.mu, so the read goes through the
// same lock (a Stop racing an early ready-line parse must not hit the
// race detector).
func (h *Handle) closeProxy() {
	h.proxyOnce.Do(func() {
		h.mu.Lock()
		fn := h.proxyClose
		h.mu.Unlock()
		if fn != nil {
			fn()
		}
	})
}

var listeningAddrRe = regexp.MustCompile(`dsh web: http://([^:\s]+):(\d+)(?:/\?token=([^\s]+))?`)

// extractListeningAddress parses the ready line. dsh >=0.1.2 appends
// a per-session browser-auth token to the loopback URL
// (`dsh web: http://127.0.0.1:PORT/?token=<base64url>`, optionally
// followed by a ` (LAN: <url>)` suffix the unanchored match ignores);
// legacy dsh prints the bare URL and token is "".
func extractListeningAddress(line string) (host, port, token string, ok bool) {
	clean := stripANSI(line)
	m := listeningAddrRe.FindStringSubmatch(clean)
	if len(m) != 4 {
		return "", "", "", false
	}
	return m[1], m[2], m[3], true
}

var ansiRe = regexp.MustCompile(`\x1b\[[0-9;]*[a-zA-Z]`)

func stripANSI(s string) string {
	return strings.TrimSpace(ansiRe.ReplaceAllString(s, ""))
}

func killPID(pid int, sig syscall.Signal) error {
	if pid == 0 {
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

// pumpAndWatch scans the child stdout for the ready line, then
// persists the upstream address, starts the reverse proxy (when a
// ProxyStarter is configured), and drains the remaining output so the
// child never blocks on a full pipe.
func (d *Driver) pumpAndWatch(ctx context.Context, h *Handle, r io.Reader) {
	defer h.wg.Done()
	sc := bufio.NewScanner(r)
	sc.Buffer(make([]byte, 0, 64*1024), scanMaxLineBytes)
	for sc.Scan() {
		if ctx.Err() != nil {
			return
		}
		line := redact.Text(sc.Text())

		host, port, token, ok := extractListeningAddress(line)
		if !ok {
			continue
		}

		h.mu.Lock()
		h.host, h.port, h.token = host, port, token
		// The shared upstream auth relay (auth.go) — write-once here,
		// in the same critical section as host/port/token, BEFORE
		// h.ready.Close(); never mutated afterwards. See the auth field
		// doc for the read discipline.
		h.auth = newUpstreamAuth(token, net.JoinHostPort(host, port))
		h.blob.Host, h.blob.Port = host, port
		starter := d.ProxyStarter
		h.mu.Unlock()

		// The starter runs OUTSIDE h.mu: startProxyListener reads the
		// relay back via h.upstreamAuthRelay(), which takes the lock —
		// invoking it under the held mutex would self-deadlock. The
		// results are written back under h.mu, keeping the same
		// lock discipline for proxyHost/proxyPort/proxyClose/blob.
		if starter != nil {
			ph, pp, closeFn, perr := starter(h, host, port)
			h.mu.Lock()
			if perr != nil {
				d.logf("instance %s: start reverse proxy: %v (instance runs proxy-less)", h.instanceID, perr)
			} else {
				h.proxyHost, h.proxyPort = ph, pp
				h.proxyClose = func() { closeFn() }
				h.blob.ProxyHost, h.blob.ProxyPort = ph, pp
				// Mirror the main listener's TLS in the persisted
				// iframe URL: handleInstanceDshInfo rebuilds the src
				// with the caller-visible host, but blob consumers
				// (tests, markProxyDead) must not see http:// for a
				// TLS deployment (REVIEW-2026-08-16 #1).
				scheme := "http"
				if d.Proxy.TLSCert != "" {
					scheme = "https"
				}
				h.blob.IframeURL = scheme + "://" + ph + ":" + pp + "/"
			}
			h.mu.Unlock()
		}
		h.mu.Lock()
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
		// Drain remaining stdout (see opencode_web.pumpAndWatch for the
		// full-pipe deadlock this prevents).
		_, _ = io.Copy(io.Discard, r)
		return
	}
}

func (d *Driver) healthLoop(ctx context.Context, h *Handle) {
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
			if probeHealth(ctx, host, port) {
				h.failCount.Store(0)
				continue
			}
			n := h.failCount.Add(1)
			if int(n) >= healthFailThreshold {
				if p := h.loadPublisher(); p != nil {
					_ = p.MarkFailed("dsh health probe failed")
				}
				return
			}
		}
	}
}

func (d *Driver) wait(ctx context.Context, h *Handle) {
	defer h.wg.Done()
	_ = h.cmd.Wait()
	h.cancel()
	exitCode := -1
	if h.cmd.ProcessState != nil {
		exitCode = h.cmd.ProcessState.ExitCode()
	}
	h.mu.Lock()
	deathReason := h.deathReason
	h.mu.Unlock()
	if p := h.loadPublisher(); p != nil {
		_ = p.MarkExited(exitCode)
		if deathReason != "" {
			// The child was terminated because the proxy listener died:
			// keep the failure reason visible (MarkExited only records
			// the exit code; MarkFailed restores the failed status).
			_ = p.MarkFailed(deathReason)
		}
	}
	// The upstream is gone; the reverse proxy has nothing to serve.
	h.closeProxy()
	// Close AFTER MarkExited: Stop waits on this channel, so by the
	// time Stop returns the final store write is done and no further
	// writes for this instance can happen.
	close(h.exited)
}

// markProxyDead handles an unexpected reverse-proxy listener death
// (proxy.go): the health loop probes only the upstream, so without
// this the embed would fail silently while the instance stays
// "running". Fail loud — log is the caller's job — mark the instance
// failed, drop the now-dead iframe URL from the blob, and tear the
// instance down (cancel the loops, close the proxy, terminate the
// upstream) so nothing leaks. wait() reaps the child and persists the
// failure reason.
func (h *Handle) markProxyDead(reason string) {
	h.mu.Lock()
	if h.proxyDead {
		h.mu.Unlock()
		return
	}
	h.proxyDead = true
	h.deathReason = reason
	h.blob.IframeURL = ""
	blob, _ := json.Marshal(h.blob)
	h.mu.Unlock()
	if p := h.loadPublisher(); p != nil {
		_ = p.UpdateKindBlob(blob)
		_ = p.MarkFailed(reason)
	}
	if h.cancel != nil {
		h.cancel()
	}
	h.closeProxy()
	if h.cmd != nil && h.cmd.Process != nil {
		_ = killPID(sigTarget(h.cmd.Process.Pid, h.killGroup), syscall.SIGTERM)
	}
}

// probeHealth checks the SPA index: GET / answers 200 on legacy dsh
// and 401 on dsh >=0.1.2 (browser auth gates even the index) — both
// mean the server is up.
func probeHealth(ctx context.Context, host, port string) bool {
	addr := "http://" + host + ":" + port + "/"
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, addr, nil)
	if err != nil {
		return false
	}
	client := &http.Client{Timeout: healthTimeout}
	resp, err := client.Do(req)
	if err != nil {
		return false
	}
	defer resp.Body.Close()
	return resp.StatusCode < 400 || resp.StatusCode == http.StatusUnauthorized
}

func mustHandle(h framework.Handle) *Handle {
	if hh, ok := h.Unwrap().(*Handle); ok {
		return hh
	}
	panic("dsh-web: framework.Handle inner is not *Handle")
}
