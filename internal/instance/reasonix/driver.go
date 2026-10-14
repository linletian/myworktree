// Package reasonix drives a `reasonix serve` subprocess per myworktree
// instance so the Reasonix web chat UI can be embedded in the instance tab.
//
// Per-instance state under <DataDir>/reasonix/<instanceID>/:
//
//	token            auth token for --auth token (chmod 600)
//	port             actual bound address (written by --port-file)
//	pid              serve main process PID (written by --pid-file)
//	serve.log        subprocess stdout/stderr
//
// The serve runs WITHOUT a REASONIX_HOME override: it uses the user's real
// ~/.reasonix exactly like a terminal-run reasonix, so sessions, history,
// config and provider credentials are shared per project (reasonix organizes
// them under ~/.reasonix/projects/<cwd-slug>/sessions and isolates projects
// by cwd itself). Each instance Start opens a fresh session (no --resume),
// matching terminal behaviour; the same project's full history stays visible
// and switchable in the embedded sidebar. myworktree only manages the serve
// process, never the agent's session state. Concurrency is handled by
// reasonix's own per-session-file lease (refuse-style, no silent double
// write). An explicit instance Restart allocates a fresh id and a fresh
// state dir — a brand-new session, consistent with the terminal "new
// session" model.
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

	// pidsMu guards pids: instance id -> pid of the live serve process.
	// REGISTRATIONS are written only under the per-instance Start lock; the
	// entry is dropped by Stop, which deliberately runs outside that lock (see
	// the invariant stated on trackPID/untrackPID). The table covers TWO
	// kinds of pid, and it is worth naming both because they are not the same
	// guarantee:
	//   (a) a pid THIS Driver spawned — registered at cmd.Start, before
	//       readiness (see Start), then overwritten after waitReady with the
	//       pid file's authoritative value;
	//   (b) a SURVIVOR pid adopted by Start's idempotent re-attach branch —
	//       when the Health check at the top of Start finds a serve already
	//       running, Start records the pid it just saw healthy even though
	//       this Driver never spawned it, so memory covers that serve from
	//       the moment the branch adopts it.
	// What the table does NOT cover is an instance reached ONLY through
	// Reattach (kind.go): Reattach probes Health alone and deliberately does
	// NOT populate this table, so such a serve is stopped via its pid file
	// and never via memory — unless and until some later Start re-attaches
	// and adopts it, which is case (b).
	// Stop takes the pid from the pid file first and falls back to this
	// table when that file yields nothing — the state dir can be wiped before
	// Stop runs (Restart migrates onto a fresh id and Cleanup removes the
	// old dir; a test that RemoveAlls the dir hits the same window), and
	// without the fallback the serve process would be orphaned forever
	// (issue #113: each leaked fake serve costs ~16MB on the dev box).
	// Deliberate scope (issue #113 arbitration): production does NOT scan
	// the process table to kill historical orphans by cmdline pattern —
	// pattern matching risks killing foreign processes. A cross-restart
	// orphan is instead reached through the persisted pid file, which is what
	// Health reads. Reconcile/Reattach semantics are unchanged.
	// Known, NOT implemented follow-up: sweep <DataDir>/reasonix/*/pid at
	// startup and adopt still-live cross-restart orphans into the running
	// set. Rejected for #113 because that issue's scope is the fake-serve
	// TEST leak; a production startup sweep needs its own evaluation (killing
	// or taking over processes this process never spawned is a different
	// risk profile, and Reattach already covers the surviving-record case).
	pidsMu sync.Mutex
	pids   map[string]int

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
// --pid-file/--no-open plus the reasonix_token cookie name (proxy side).
// Verified against v1.22.0 (issue #45).
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
	// (e.g. HTTP_PROXY), appended to the inherited environment.
	Env []string

	// PreStart runs right before spawning serve (after the state dir is
	// ready); an error aborts Start before any process is launched. Used to
	// prepare the environment (DEFERRED §3).
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

// lockStart serializes Start calls per instance id: it returns the unlock
// function for the instance's in-flight lock, creating the lock on first use.
// The map entry is removed by dropStartLock when the instance is torn down
// for good (Cleanup), so it cannot grow without bound across restarts.
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

// dropStartLock removes the per-instance Start lock once the instance is
// being torn down for good (deleted, or restarted onto a fresh id — the id
// is never reused, so the entry would otherwise accumulate forever). Safe
// against in-flight Start calls: a goroutine that already fetched the mutex
// keeps its unlock closure, and any later Start for this id (none expected
// after teardown) gets a fresh lock.
func (d *Driver) dropStartLock(instanceID string) {
	d.startsMu.Lock()
	delete(d.starts, instanceID)
	d.startsMu.Unlock()
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
		d.trackPID(in.InstanceID, info.PID) // re-attach: keep the memory table in sync
		return info, nil
	}

	// State dir only carries myworktree's own serve-management files (token,
	// port, pid, serve.log). Sessions/config/credentials all live in the
	// agent's own state root (~/.reasonix) — the same pool a terminal-run
	// reasonix uses — so we deliberately create no per-instance home.
	dir := d.dir(in.InstanceID)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return Info{}, fmt.Errorf("reasonix: create state dir: %w", err)
	}

	// Legacy migration hint: instances created before the 2026-08-12
	// revision carry an isolated home/ dir (plus a fixed session.jsonl) that
	// the driver no longer reads — sessions now live in the shared
	// ~/.reasonix pool. Leftovers are inert, but surface a hint so the user
	// knows they can delete the instance (or migrate its sessions manually).
	if _, err := os.Stat(filepath.Join(dir, "home")); err == nil {
		d.logf("instance %s has a legacy isolated home/ (pre-2026-08-12); it is no longer read — sessions live in the shared ~/.reasonix pool; delete the instance or migrate its sessions manually", in.InstanceID)
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
	// Strip any inherited REASONIX_HOME / REASONIX_STATE_HOME so the serve
	// always uses the user's real ~/.reasonix (same state root as a
	// terminal-run reasonix) — sessions/config/credentials are then shared
	// per project by reasonix itself (per-cwd isolation). Opt-out: a host
	// that deliberately exports either variable can override via the instance
	// tag env (StartInput.Env), appended afterwards. A stripped host value is
	// surfaced (key names only, never the value) so a silent ignore never
	// hides the user's own setting.
	var strippedHome []string
	for _, k := range []string{"REASONIX_HOME", "REASONIX_STATE_HOME"} {
		if os.Getenv(k) != "" {
			strippedHome = append(strippedHome, k)
		}
	}
	if len(strippedHome) > 0 {
		d.logf("stripping inherited %s from serve environment (serve uses the real ~/.reasonix); override via the instance tag env if intended", strings.Join(strippedHome, ", "))
	}
	cmd.Env = RemoveEnv(os.Environ(), "REASONIX_HOME", "REASONIX_STATE_HOME")
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
	// Register the pid the MOMENT the process exists (issue #113). Tracking
	// only after waitReady left a window as long as the readiness poll (up to
	// 15 s) in which a Stop for this id found no pid file (Start removed the
	// stale one above, serve has not written the new one yet) and nothing in
	// memory, answered "never started", and returned — while the process it
	// was asked to stop was already running and became an orphan.
	// pgid semantics, qualified to THIS registration: SysProcAttr.Setpgid
	// above put this child into a NEW process group whose pgid equals
	// cmd.Process.Pid, so cmd.Process.Pid is the group LEADER and Stop's
	// kill(-pid) reaches every descendant serve spawns inside that group —
	// exactly the target Setpgid exists for.
	// waitReady overwrites the entry with the pid file's pid once that file
	// appears (reasonix's own pid is the authoritative Health/Stop target and
	// may differ from the exec pid). The group-kill guarantee above holds for
	// the EXEC pid registration only: if the pid file's pid is a different
	// process, it is not this group's leader, kill(-pid) finds no such group
	// (ESRCH) and Stop degrades to signaling that single process. That is not
	// a regression — Stop has always preferred the pid file's pid, so the
	// degraded shape is the same one the pid-file path has always had; the
	// exec-pid registration is what makes the group kill reachable during the
	// readiness window.
	// The readiness-failure path untracks. Absent the narrow Start/Stop race
	// named below, a registration does not outlive the process it names: if
	// waitReady has already read the pid file and returned while a
	// concurrent Stop for this id has untracked and signalled it, this next
	// line re-registers a pid that is on its way out. The residue
	// self-heals — the next Stop finds processAlive false and untracks — so
	// the race costs one transient stale entry, never a permanent one.
	d.trackPID(in.InstanceID, cmd.Process.Pid)
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
		// The pid tracked at cmd.Start above names a process this Start is
		// abandoning; drop it so the table never carries a dead (or about to
		// be dead) registration that a later Stop would act on.
		d.untrackPID(in.InstanceID)
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
	d.trackPID(in.InstanceID, info.PID) // authoritative pid-file pid; Stop's fallback if that file is gone before then (issue #113)
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
		return fmt.Errorf("reasonix: version %s installed, but reasonix >= %s required (the embedded web UI relies on serve --port-file/--token-file/--pid-file/--no-open); upgrade reasonix or set Driver.MinVersion", got, minV)
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

// trackPID registers the live serve pid for an instance. Start writes twice:
// at cmd.Start (the exec pid, which is the process-group leader because of
// Setpgid) so Stop can reach the process from the instant it exists, and
// again after waitReady with the pid file's authoritative value; the
// idempotent re-attach branch writes the pid it just saw healthy. Stop
// deletes the entry once the process is gone. See the Driver comment on pids
// for why Stop needs the table at all (issue #113).
//
// LOCK INVARIANT — stated here because it is what makes untrackPID safe, and
// it holds today only by call-site placement: EVERY registration of a given
// instance id is written while that id's lockStart mutex is held. All three
// call sites are inside Start, between its lockStart/unlock pair, and Start
// is the only caller. Consequence: sibling Starts of the same id cannot
// interleave registrations, so the table holds at most one registration per id
// at a time in a known order, and untrackPID's unconditional delete-by-id
// (below) can never silently drop a pid a concurrent sibling Start just
// registered for a live process. Cost of honouring it: zero — no extra lock is
// taken, this only pins the placement. If a future change ever moves a
// trackPID call outside lockStart, the invariant breaks with no compiler error
// and no failing test; re-examine untrackPID at that point.
func (d *Driver) trackPID(instanceID string, pid int) {
	if pid <= 0 {
		return
	}
	d.pidsMu.Lock()
	if d.pids == nil {
		d.pids = map[string]int{}
	}
	d.pids[instanceID] = pid
	d.pidsMu.Unlock()
}

// trackedPID returns the last pid Start registered for instanceID, or 0
// when none is tracked.
func (d *Driver) trackedPID(instanceID string) int {
	d.pidsMu.Lock()
	defer d.pidsMu.Unlock()
	return d.pids[instanceID]
}

// untrackPID drops the registration for instanceID. It deletes by id alone
// and never compares the pid it removes against a pid the caller named, which
// is sound only under the lock invariant stated on trackPID: one registration
// per id, all of them written under that id's lockStart, so there is no
// concurrent sibling registration to lose.
// Locking of the two call sites differs, deliberately:
//   - Start's readiness-failure path runs UNDER lockStart, so its delete is
//     ordered against that same Start's registrations.
//   - Stop's two deletes run OUTSIDE lockStart — Stop does not take the Start
//     lock at all, and must not: the whole point of #113 is that Stop can
//     reach a process whose Start is still parked in the readiness poll, and
//     taking the lock here would make Stop wait behind it (up to 15 s).
//     The accepted residual of that asymmetry is a narrow window in which a
//     Stop acting on a stale pid deletes a fresher registration a concurrent
//     Start of the same id just made; it self-heals once serve writes its pid
//     file, which is Stop's primary source from then on.
func (d *Driver) untrackPID(instanceID string) {
	d.pidsMu.Lock()
	delete(d.pids, instanceID)
	d.pidsMu.Unlock()
}

// trackedInstanceIDs lists the instance ids with a tracked (not yet
// stopped) pid, so a caller tearing the driver down (e.g. a test
// t.Cleanup, issue #113) can Stop everything without keeping its own
// bookkeeping. Unexported: the production shutdown path is
// framework.Manager.StopAllKind, this only exposes the driver's own view.
func (d *Driver) trackedInstanceIDs() []string {
	d.pidsMu.Lock()
	defer d.pidsMu.Unlock()
	ids := make([]string, 0, len(d.pids))
	for id := range d.pids {
		ids = append(ids, id)
	}
	return ids
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
// waits for it to exit. It is idempotent. The pid comes from the pid file;
// the driver falls back to the pid Start registered in memory whenever that
// file yields nothing usable. "Nothing usable" covers THREE distinct
// triggers, and all three land in the same `pid <= 0` branch below:
//
//   - MISSING: there is no pid file at all — never started, or Cleanup /
//     Restart wiped the state dir before Stop ran. That is the shape of
//     TestDriverAddrCacheRefresh, which removes the old id's dir while its
//     serve still lives and whose deferred Stop must still kill it
//     (issue #113).
//   - UNREADABLE: the file exists but os.ReadFile fails on it — permission
//     denied, an I/O error, or the path is a directory.
//   - UNPARSEABLE or NON-POSITIVE CONTENT: the bytes are empty, a truncated
//     or garbage write, "0", or a negative number. strconv.Atoi fails, or
//     the value is <= 0, so no process could be named by it.
//
// Only when neither source yields a positive pid is the instance "never
// started", and that is the single case Stop reports as a no-op.
func (d *Driver) Stop(instanceID string) error {
	d.dropCache(instanceID)
	dir := d.dir(instanceID)
	pid := 0
	if b, err := os.ReadFile(filepath.Join(dir, "pid")); err == nil {
		if v, aerr := strconv.Atoi(strings.TrimSpace(string(b))); aerr == nil {
			pid = v
		}
	}
	if pid <= 0 {
		// MISSING / UNREADABLE / UNPARSEABLE-or-NON-POSITIVE (see the doc
		// comment): fall back to the pid Start registered in memory.
		pid = d.trackedPID(instanceID)
	}
	if pid <= 0 {
		return nil // never started (no usable pid file, nothing tracked in memory)
	}
	if err := syscall.Kill(-pid, syscall.SIGTERM); err != nil {
		// Fall back to signaling the single process (no group).
		_ = syscall.Kill(pid, syscall.SIGTERM)
	}
	// Give it a moment to exit, then SIGKILL.
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if !processAlive(pid) {
			d.untrackPID(instanceID)
			d.logf("stopped reasonix serve pid %d for instance %s", pid, instanceID)
			return nil
		}
		time.Sleep(100 * time.Millisecond)
	}
	_ = syscall.Kill(-pid, syscall.SIGKILL)
	_ = syscall.Kill(pid, syscall.SIGKILL)
	d.untrackPID(instanceID)
	d.logf("force-killed reasonix serve pid %d for instance %s", pid, instanceID)
	return nil
}

// Cleanup removes the per-instance serve-management state directory (token,
// port, pid, serve.log). It does NOT touch the agent's shared state root
// (~/.reasonix): sessions live in the project's shared session pool and are
// never deleted by instance lifecycle. The instance id is never reused
// (restart allocates a fresh id), so removing its management dir avoids
// unbounded accumulation under <DataDir>/reasonix/. Call after Stop when the
// instance is deleted. Cleanup deliberately does NOT kill and does NOT drop
// the in-memory pid entry: wiping the dir must stay side-effect-free, and
// keeping the pid is what lets a Stop that arrives after this Cleanup (the
// old-id teardown order in Restart-style flows) still kill the process
// (issue #113).
//
// The documented order is Stop first, Cleanup second, and this method carries
// a TRIPWIRE for that invariant: when it runs while the in-memory table
// still holds a live pid for the instance, it logs a warning naming the pid
// and the call-order problem, because the caller just removed the pid file out
// from under a serve process that is still running (a mis-ordered teardown,
// and the window in which a later Stop has nothing left to find if the driver
// dies before it).
// Stated at its exact reach, because the honest scope is narrower than the
// warning's existence suggests: on today's production paths this warning does
// NOT light up. The only two callers of Cleanup are Kind.Stop and
// Kind.Cleanup (kind.go), and BOTH call Stop first; Stop untracks on both of
// its return paths, so by the time Cleanup runs there trackedPID is already
// 0 and the tripwire cannot fire. It fires on an OUT-OF-ORDER teardown —
// currently exercised only by TestCleanupWarnsWhenStopWasSkipped — which makes
// it a guard for the invariant against a FUTURE caller that breaks the order,
// not a line the daemon log is expected to show routinely. The behaviour is
// unchanged either way — no kill, still RemoveAll.
func (d *Driver) Cleanup(instanceID string) error {
	d.dropCache(instanceID)
	d.dropStartLock(instanceID)
	if strings.TrimSpace(instanceID) == "" {
		return errors.New("reasonix: instance id is required")
	}
	dir := d.dir(instanceID)
	// Call-order alarm (issue #113): a tracked pid that is still alive means
	// Stop has not run (or did not reach the process) for this instance, yet
	// its management dir is being removed now. Deliberately BEFORE the
	// RemoveAll below, and deliberately warning-only — Cleanup neither kills
	// the process nor drops the pid entry (see the doc comment), so the
	// in-memory fallback stays available to the Stop that should have come
	// first.
	// Semantic boundary of the liveness test, so the false positive is named
	// rather than hidden: processAlive is kill(pid, 0), which answers ALIVE
	// both for a ZOMBIE (the slot is still occupied until its parent reaps
	// it) and for a pid the kernel has REUSED by an unrelated process. So
	// this warning can fire once about a pid that is already gone or no longer
	// ours — accepted, because it is a warning and Cleanup takes no signal on
	// the strength of it. The same kill(0) semantics under Stop's
	// SIGKILL-after-timeout (a reused pid would take a stray SIGKILL): that
	// is pre-existing behaviour on the pid-file path, unchanged by and not
	// introduced in #113, which only added memory as a second source of the
	// same number.
	if pid := d.trackedPID(instanceID); pid > 0 && processAlive(pid) {
		d.logf("WARNING cleanup before stop: instance %s still has a live serve pid %d tracked while %s is being removed — the caller must Stop, then Cleanup; the process is NOT killed here (by design) and now survives with no pid file on disk, reachable only through this driver's in-memory pid table", instanceID, pid, dir)
	}
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

// RemoveEnv returns env with any entries whose KEY (the part before '=')
// matches one of names removed, preserving the order of the rest. An entry
// without '=' is treated as a bare KEY and is dropped when its KEY is one of
// names (kept otherwise). Used to strip inherited REASONIX_HOME /
// REASONIX_STATE_HOME from the serve environment so the serve always uses the
// user's real ~/.reasonix, regardless of what the host process environment
// happens to carry. The manager applies the same strip to the preStart
// environment so both phases run under the same home resolution.
func RemoveEnv(env []string, names ...string) []string {
	drop := make(map[string]bool, len(names))
	for _, n := range names {
		drop[n] = true
	}
	out := make([]string, 0, len(env))
	for _, kv := range env {
		key := kv
		if i := strings.IndexByte(kv, '='); i >= 0 {
			key = kv[:i]
		}
		if !drop[key] {
			out = append(out, kv)
		}
	}
	return out
}
