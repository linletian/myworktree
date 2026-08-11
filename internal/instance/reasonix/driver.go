// Package reasonix drives a `reasonix serve` subprocess per myworktree
// instance so the Reasonix web chat UI can be embedded in the instance tab.
//
// Lifecycle isolation per instance:
//
//	<DataDir>/reasonix/<instanceID>/
//	  home/            REASONIX_HOME (sessions, history, state)
//	  home/config.toml symlink -> ~/.reasonix/config.toml (if present)
//	  home/.env        symlink -> ~/.reasonix/.env (provider credentials, if present)
//	  token            auth token for --auth token (chmod 600)
//	  port             actual bound address (written by --port-file)
//	  pid              serve main process PID (written by --pid-file)
//	  session.jsonl    fixed session file (--resume), so an instance keeps
//	                   its conversation across myworktree restarts. Note that
//	                   an explicit instance Restart allocates a fresh id and a
//	                   fresh state dir, i.e. it starts a brand-new conversation
//	                   (the old dir, including this session, is removed).
//	  serve.log        subprocess stdout/stderr
//
// REASONIX_HOME is isolated per instance so concurrent worktree instances
// never contend on session leases, while config.toml/.env are symlinked from
// the user's real ~/.reasonix so provider credentials and settings stay live.
package reasonix

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"log"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"
)

// DataDir is where per-instance reasonix state lives (see package comment).
type Driver struct {
	DataDir string
	Logger  *log.Logger

	// ReasonixBin overrides the reasonix executable. Empty means exec.LookPath("reasonix").
	ReasonixBin string

	mu    sync.Mutex
	cache map[string]cachedAddr // issue #46: avoid a file read per proxy request

	// startsMu guards starts: per-instance in-flight Start locks, so two
	// concurrent Start calls for the same instance cannot both pass the
	// Health check and spawn duplicate serve processes (a race would leave
	// the loser's process untracked, both writing the same port/pid files
	// and contending for the session lease). The second caller blocks until
	// the first finishes, then sees the healthy process and returns its
	// Info idempotently.
	startsMu sync.Mutex
	starts   map[string]*sync.Mutex

	// verMu guards verPass: per-binary memo of a PASSED version gate, so
	// repeated Starts of the same reasonix binary do not re-spawn
	// `reasonix --version` on every call (issue #45). Failures are NOT
	// cached, so an upgraded binary is picked up on the next attempt.
	// Boundary: the key is the binary path string — upgrading reasonix in
	// place (same PATH, new inode) keeps a pass cached until the PATH
	// changes or the process restarts; a downgrade below 1.22.0 at the same
	// PATH would not be re-gated, but that requires a deliberate downgrade
	// and is caught by the serve-phase errors if the flag contract breaks.
	verMu   sync.Mutex
	verPass map[string]bool

	// MinVersion is the minimum reasonix CLI version accepted by Start. Empty
	// means defaultMinVersion; set to "0" to disable the version gate.
	MinVersion string
}

// defaultMinVersion is the reasonix CLI version whose flag contract the
// driver depends on: serve --addr/--auth token/--token-file/--port-file/
// --pid-file/--no-open/--resume plus the reasonix_token cookie name (proxy
// side). Verified against v1.22.0 (issue #45).
const defaultMinVersion = "1.22.0"

// cachedAddr is a cached {port, token} for one instance, valid until
// Stop/Cleanup or the next successful Start overwrites it (no TTL — see the
// Driver comment on cache).
type cachedAddr struct {
	info Info
}

type StartInput struct {
	InstanceID   string // myworktree instance id (used for the state dir name)
	WorktreePath string // serve working directory (session is scoped to it)

	// Env appends extra "KEY=VALUE" entries to the serve process environment
	// (e.g. HTTP_PROXY). Appended after REASONIX_HOME so an entry can
	// deliberately override it.
	Env []string

	// PreStart runs right before spawning serve (after state dirs and
	// symlinks are ready); an error aborts Start before any process is
	// launched. Used to prepare the environment (DEFERRED §3).
	PreStart func() error
}

type Info struct {
	PID   int
	Port  int
	Token string
}

func (d *Driver) logf(format string, args ...any) {
	if d.Logger != nil {
		d.Logger.Printf("[reasonix] "+format, args...)
	}
}

func (d *Driver) dir(instanceID string) string {
	return filepath.Join(d.DataDir, "reasonix", instanceID)
}

func (d *Driver) homeDir(instanceID string) string {
	return filepath.Join(d.dir(instanceID), "home")
}

// lockStart serializes Start calls per instance id: it returns the unlock
// function for the instance's in-flight lock, creating the lock on first use.
// Locks are never deleted (at most one small mutex per instance id ever
// started), which keeps the map race-free.
func (d *Driver) lockStart(instanceID string) func() {
	d.startsMu.Lock()
	if d.starts == nil {
		d.starts = map[string]*sync.Mutex{}
	}
	l, ok := d.starts[instanceID]
	if !ok {
		l = &sync.Mutex{}
		d.starts[instanceID] = l
	}
	d.startsMu.Unlock()
	l.Lock()
	return l.Unlock
}

// Start launches (or re-attaches to) the reasonix serve process for an
// instance and waits until it is listening. It is idempotent: if a healthy
// process is already tracked in the pid file, it returns its Info.
func (d *Driver) Start(in StartInput) (Info, error) {
	if strings.TrimSpace(in.InstanceID) == "" {
		return Info{}, errors.New("reasonix: instance id is required")
	}
	if strings.TrimSpace(in.WorktreePath) == "" {
		return Info{}, errors.New("reasonix: worktree path is required")
	}
	if d.DataDir == "" {
		return Info{}, errors.New("reasonix: data dir is required")
	}

	// Serialize per instance: see lockStart. The Health check below must run
	// under the lock so concurrent Starts cannot both decide to spawn.
	unlock := d.lockStart(in.InstanceID)
	defer unlock()

	if info, ok, err := d.Health(in.InstanceID); err == nil && ok {
		d.logf("instance %s already running (pid %d, port %d)", in.InstanceID, info.PID, info.Port)
		return info, nil
	}

	dir := d.dir(in.InstanceID)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return Info{}, fmt.Errorf("reasonix: create state dir: %w", err)
	}
	home := d.homeDir(in.InstanceID)
	if err := os.MkdirAll(home, 0o755); err != nil {
		return Info{}, fmt.Errorf("reasonix: create home dir: %w", err)
	}

	// Inherit the user's live config and credentials without copying them.
	if err := symlinkIfExists(legacyUserPath("config.toml"), filepath.Join(home, "config.toml")); err != nil {
		return Info{}, err
	}
	if err := symlinkIfExists(legacyUserPath(".env"), filepath.Join(home, ".env")); err != nil {
		return Info{}, err
	}

	bin := d.ReasonixBin
	if bin == "" {
		var err error
		bin, err = exec.LookPath("reasonix")
		if err != nil {
			return Info{}, fmt.Errorf("reasonix: executable not found in PATH (install reasonix, or set ReasonixBin): %w", err)
		}
	}

	// Version gate (issue #45): fail fast with a readable error instead of a
	// 15s readiness timeout when the installed CLI predates the flag
	// contract the driver relies on.
	minV := d.MinVersion
	if strings.TrimSpace(minV) == "" {
		minV = defaultMinVersion
	}
	if minV != "0" {
		if err := d.checkVersion(bin, minV); err != nil {
			return Info{}, err
		}
	}

	token, err := generateToken()
	if err != nil {
		return Info{}, err
	}
	if err := os.WriteFile(filepath.Join(dir, "token"), []byte(token+"\n"), 0o600); err != nil {
		return Info{}, fmt.Errorf("reasonix: write token: %w", err)
	}

	// A fixed empty session file makes --resume deterministic and gives the
	// instance its own lease path (reasonix locks by session file path).
	sessionFile := filepath.Join(dir, "session.jsonl")
	if _, err := os.Stat(sessionFile); os.IsNotExist(err) {
		if err := os.WriteFile(sessionFile, nil, 0o600); err != nil {
			return Info{}, fmt.Errorf("reasonix: create session file: %w", err)
		}
	}

	portFile := filepath.Join(dir, "port")
	pidFile := filepath.Join(dir, "pid")
	logFile := filepath.Join(dir, "serve.log")
	// Stale port/pid from a previous run: remove both so waitReady can only
	// pick up the NEW process's port and pid (a leftover pid file would let
	// Health report the old dead pid against the new port).
	_ = os.Remove(portFile)
	_ = os.Remove(pidFile)

	args := []string{
		"serve",
		"--addr", "127.0.0.1:0", // let reasonix pick a free port; port-file is authoritative
		"--auth", "token",
		"--token-file", filepath.Join(dir, "token"),
		"--port-file", portFile,
		"--pid-file", pidFile,
		"--no-open",
		"--resume", sessionFile,
	}
	// PreStart hook: run before any process is spawned; an error aborts Start
	// (DEFERRED §3).
	if in.PreStart != nil {
		if err := in.PreStart(); err != nil {
			return Info{}, fmt.Errorf("reasonix: preStart failed: %w", err)
		}
	}
	cmd := exec.Command(bin, args...)
	cmd.Dir = in.WorktreePath
	cmd.Env = append(os.Environ(), "REASONIX_HOME="+home)
	cmd.Env = append(cmd.Env, in.Env...)
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	f, err := os.OpenFile(logFile, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600)
	if err != nil {
		return Info{}, fmt.Errorf("reasonix: open serve.log: %w", err)
	}
	defer f.Close()
	cmd.Stdout = f
	cmd.Stderr = f
	if err := cmd.Start(); err != nil {
		return Info{}, fmt.Errorf("reasonix: start serve: %w", err)
	}
	d.logf("started reasonix serve pid %d for instance %s (worktree %s)", cmd.Process.Pid, in.InstanceID, in.WorktreePath)

	// Reap the process from a goroutine: cmd.Wait() both detects an early
	// exit (via done) and prevents the dead process from lingering as a
	// zombie (a zombie makes kill(pid,0) report "alive", which would break
	// Health/Stop below).
	done := make(chan error, 1)
	go func() {
		done <- cmd.Wait()
	}()

	// Wait until port-file appears (serve is listening) or the process dies.
	info, err := d.waitReady(in.InstanceID, done, portFile, pidFile, token, 15*time.Second)
	if err != nil {
		_ = cmd.Process.Kill()
		// waitReady may already have consumed the done value (early-exit
		// path); a blocking <-done would then hang forever. Drain
		// non-blockingly: the Wait goroutine reaps the process either way,
		// and done is buffered so a late send cannot leak.
		select {
		case <-done:
		default:
		}
		return Info{}, err
	}
	d.storeCache(in.InstanceID, info)
	return info, nil
}

// CookieName is the reasonix auth cookie the web UI authenticates with
// (internal/serve auth.go checkToken cookie fast path). The reverse proxy
// injects this cookie from the per-instance token file; keeping the name here
// is the single source of truth for the contract (issue #45).
const CookieName = "reasonix_token"

func (d *Driver) waitReady(instanceID string, done <-chan error, portFile, pidFile, token string, timeout time.Duration) (Info, error) {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		select {
		case err := <-done:
			return Info{}, fmt.Errorf("reasonix: serve exited early for instance %s: %v\nserve.log tail:\n%s", instanceID, err, d.serveLogTail(instanceID, 10))
		default:
		}
		if b, err := os.ReadFile(portFile); err == nil {
			if addr := strings.TrimSpace(string(b)); addr != "" {
				if _, port, err := net.SplitHostPort(addr); err == nil {
					if p, perr := strconv.Atoi(port); perr == nil && p > 0 {
						// The tracked pid comes from reasonix's own pid file:
						// serve may spawn a wrapper, so the exec.Cmd pid is
						// not necessarily the process Stop/Health target.
						// Require a valid pid so the persisted instance record
						// never carries PID 0 (Stop/Health would be inert).
						if pid := cmdProcessPID(pidFile); pid > 0 {
							return Info{PID: pid, Port: p, Token: token}, nil
						}
						// Port up but pid file not written yet: keep polling
						// (upstream writes port before pid).
					}
				}
			}
		}
		time.Sleep(150 * time.Millisecond)
	}
	return Info{}, fmt.Errorf("reasonix: serve did not become ready within %v for instance %s\nserve.log tail:\n%s", timeout, instanceID, d.serveLogTail(instanceID, 10))
}

// serveLogTail returns the last n non-empty lines of the instance's serve.log
// (or "" when absent/empty), so readiness failures carry reasonix's own error
// text instead of a bare "did not become ready".
func (d *Driver) serveLogTail(instanceID string, n int) string {
	b, err := os.ReadFile(filepath.Join(d.dir(instanceID), "serve.log"))
	if err != nil {
		return "(no serve.log)"
	}
	var lines []string
	for _, l := range strings.Split(strings.TrimRight(string(b), "\n"), "\n") {
		if strings.TrimSpace(l) != "" {
			lines = append(lines, l)
		}
	}
	if len(lines) > n {
		lines = lines[len(lines)-n:]
	}
	if len(lines) == 0 {
		return "(serve.log empty)"
	}
	return strings.Join(lines, "\n")
}

// checkVersion runs `reasonix --version` and fails when the installed CLI is
// older than minV. An unparseable version (e.g. a "dev" build) is logged and
// tolerated — we cannot prove incompatibility, and blocking dev builds would
// harm local development.
func (d *Driver) checkVersion(bin, minV string) error {
	d.verMu.Lock()
	passed := d.verPass[bin]
	d.verMu.Unlock()
	if passed {
		return nil
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	out, err := exec.CommandContext(ctx, bin, "--version").Output()
	if err != nil {
		return fmt.Errorf("reasonix: version probe `%s --version` failed: %w", bin, err)
	}
	got, ok := parseReasonixVersion(string(out))
	if !ok {
		d.logf("could not parse reasonix version from %q; continuing without version gate", strings.TrimSpace(string(out)))
		return nil
	}
	if versionLess(got, minV) {
		return fmt.Errorf("reasonix: version %s installed, but reasonix >= %s required (the embedded web UI relies on serve --port-file/--token-file/--pid-file/--no-open/--resume); upgrade reasonix or set Driver.MinVersion", got, minV)
	}
	d.verMu.Lock()
	if d.verPass == nil {
		d.verPass = map[string]bool{}
	}
	d.verPass[bin] = true
	d.verMu.Unlock()
	return nil
}

var versionRe = regexp.MustCompile(`(\d+)\.(\d+)\.(\d+)`)

// parseReasonixVersion extracts the first x.y.z semver from `reasonix --version`
// output ("reasonix v1.22.0" / "reasonix 1.22.0" / "dev").
func parseReasonixVersion(out string) (string, bool) {
	m := versionRe.FindStringSubmatch(out)
	if len(m) != 4 {
		return "", false
	}
	return m[1] + "." + m[2] + "." + m[3], true
}

// versionLess reports whether a < b for x.y.z versions ("1.22.0" < "1.22.1").
func versionLess(a, b string) bool {
	pa, oka := splitVersion(a)
	pb, okb := splitVersion(b)
	if !oka || !okb {
		return false
	}
	for i := 0; i < 3; i++ {
		if pa[i] != pb[i] {
			return pa[i] < pb[i]
		}
	}
	return false
}

func splitVersion(v string) ([3]int, bool) {
	parts := strings.SplitN(v, ".", 3)
	if len(parts) != 3 {
		return [3]int{}, false
	}
	var out [3]int
	for i := 0; i < 3; i++ {
		n, err := strconv.Atoi(parts[i])
		if err != nil {
			return [3]int{}, false
		}
		out[i] = n
	}
	return out, true
}

// Addr reads the tracked port/token for an instance without probing the
// process. Used by the reverse proxy. The value is cached in memory (issue
// #46) so the proxy path does not read two files per request; the cache is
// written on Start and dropped on Stop/Cleanup (no TTL — see the Driver
// comment on cache).
func (d *Driver) Addr(instanceID string) (Info, error) {
	if c, ok := d.cached(instanceID); ok {
		return c.info, nil
	}
	info, err := d.addrFromFiles(instanceID)
	if err != nil {
		return Info{}, err
	}
	d.storeCache(instanceID, info)
	return info, nil
}

func (d *Driver) addrFromFiles(instanceID string) (Info, error) {
	dir := d.dir(instanceID)
	b, err := os.ReadFile(filepath.Join(dir, "port"))
	if err != nil {
		return Info{}, fmt.Errorf("reasonix: instance %s has no port file (not started?): %w", instanceID, err)
	}
	addr := strings.TrimSpace(string(b))
	if addr == "" {
		return Info{}, errors.New("reasonix: empty port file for instance " + instanceID)
	}
	_, portStr, err := net.SplitHostPort(addr)
	if err != nil {
		return Info{}, fmt.Errorf("reasonix: parse port file %q: %w", addr, err)
	}
	port, err := strconv.Atoi(portStr)
	if err != nil {
		return Info{}, fmt.Errorf("reasonix: parse port %q: %w", portStr, err)
	}
	token := ""
	if tb, err := os.ReadFile(filepath.Join(dir, "token")); err == nil {
		token = strings.TrimSpace(string(tb))
	}
	return Info{Port: port, Token: token}, nil
}

func (d *Driver) cached(instanceID string) (cachedAddr, bool) {
	d.mu.Lock()
	defer d.mu.Unlock()
	c, ok := d.cache[instanceID]
	return c, ok
}

func (d *Driver) storeCache(instanceID string, info Info) {
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.cache == nil {
		d.cache = map[string]cachedAddr{}
	}
	d.cache[instanceID] = cachedAddr{info: info}
}

func (d *Driver) dropCache(instanceID string) {
	d.mu.Lock()
	defer d.mu.Unlock()
	delete(d.cache, instanceID)
}

// cmdProcessPID reads the pid file if present and valid, falling back to 0.
// reasonix serve writes its own pid there; it may differ from the exec.Cmd pid.
func cmdProcessPID(pidFile string) int {
	if pb, err := os.ReadFile(pidFile); err == nil {
		if v, aerr := strconv.Atoi(strings.TrimSpace(string(pb))); aerr == nil && v > 0 {
			return v
		}
	}
	return 0
}

// processAlive reports whether pid is a live process, using kill(pid, 0)
// only — no /proc access, which does not exist on macOS (a primary support
// platform). Zombies cannot linger for processes we started: Start reaps via
// the cmd.Wait goroutine. Health additionally probes the TCP port so a
// zombie or dead process is never reported healthy.
func processAlive(pid int) bool {
	if pid <= 0 {
		return false
	}
	return syscall.Kill(pid, 0) == nil
}

// tcpAlive probes host:port with a short dial. Cross-platform liveness
// check used by Health instead of a /proc state read.
func tcpAlive(host string, port int) bool {
	if port <= 0 {
		return false
	}
	conn, err := net.DialTimeout("tcp", net.JoinHostPort(host, strconv.Itoa(port)), 300*time.Millisecond)
	if err != nil {
		return false
	}
	_ = conn.Close()
	return true
}

// Health reports whether the tracked serve process is alive and, if so, its
// Info. Missing files, a dead process, or a port that does not accept
// connections report ok=false.
func (d *Driver) Health(instanceID string) (Info, bool, error) {
	dir := d.dir(instanceID)
	b, err := os.ReadFile(filepath.Join(dir, "pid"))
	if err != nil {
		return Info{}, false, nil // never started
	}
	pid, err := strconv.Atoi(strings.TrimSpace(string(b)))
	if err != nil || pid <= 0 {
		return Info{}, false, nil
	}
	if !processAlive(pid) {
		return Info{}, false, nil // dead or zombie
	}
	info, err := d.Addr(instanceID)
	if err != nil {
		return Info{}, false, nil // port file missing → not ready
	}
	info.PID = pid
	if !tcpAlive("127.0.0.1", info.Port) {
		return Info{}, false, nil // process alive but not serving
	}
	return info, true, nil
}

// Stop terminates the tracked serve process (its whole process group) and
// waits for it to exit. It is idempotent.
func (d *Driver) Stop(instanceID string) error {
	d.dropCache(instanceID)
	dir := d.dir(instanceID)
	b, err := os.ReadFile(filepath.Join(dir, "pid"))
	if err != nil {
		return nil // never started
	}
	pid, err := strconv.Atoi(strings.TrimSpace(string(b)))
	if err != nil || pid <= 0 {
		return nil
	}
	if err := syscall.Kill(-pid, syscall.SIGTERM); err != nil {
		// Fall back to signaling the single process (no group).
		_ = syscall.Kill(pid, syscall.SIGTERM)
	}
	// Give it a moment to exit, then SIGKILL.
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if !processAlive(pid) {
			d.logf("stopped reasonix serve pid %d for instance %s", pid, instanceID)
			return nil
		}
		time.Sleep(100 * time.Millisecond)
	}
	_ = syscall.Kill(-pid, syscall.SIGKILL)
	_ = syscall.Kill(pid, syscall.SIGKILL)
	d.logf("force-killed reasonix serve pid %d for instance %s", pid, instanceID)
	return nil
}

// Cleanup removes the whole per-instance state directory. The instance id is
// never reused (restart allocates a fresh id), so the isolated REASONIX_HOME
// and session file can never be resumed; deleting everything avoids unbounded
// accumulation under <DataDir>/reasonix/ and drops the credential symlinks.
// Call after Stop when the instance is deleted.
func (d *Driver) Cleanup(instanceID string) error {
	d.dropCache(instanceID)
	if strings.TrimSpace(instanceID) == "" {
		return errors.New("reasonix: instance id is required")
	}
	dir := d.dir(instanceID)
	if _, err := os.Stat(dir); os.IsNotExist(err) {
		return nil
	}
	return os.RemoveAll(dir)
}

func generateToken() (string, error) {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		return "", fmt.Errorf("reasonix: generate token: %w", err)
	}
	return hex.EncodeToString(b), nil
}

// legacyUserPath returns ~/.reasonix/<name> (the Unix default Reasonix home),
// or "" if the home directory can't be resolved.
func legacyUserPath(name string) string {
	home, err := os.UserHomeDir()
	if err != nil || home == "" {
		return ""
	}
	return filepath.Join(home, ".reasonix", name)
}

// symlinkIfExists creates target -> src when src exists, skipping when target
// already exists (idempotent across restarts).
func symlinkIfExists(src, target string) error {
	if src == "" {
		return nil
	}
	if _, err := os.Stat(src); err != nil {
		return nil // user file absent → skip
	}
	if _, err := os.Lstat(target); err == nil {
		return nil // already linked
	}
	if err := os.Symlink(src, target); err != nil {
		return fmt.Errorf("reasonix: symlink %s -> %s: %w", target, src, err)
	}
	return nil
}
