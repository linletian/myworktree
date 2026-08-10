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
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"log"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"
)

// DataDir is where per-instance reasonix state lives (see package comment).
type Driver struct {
	DataDir string
	Logger  *log.Logger

	// ReasonixBin overrides the reasonix executable. Empty means exec.LookPath("reasonix").
	ReasonixBin string
}

type StartInput struct {
	InstanceID   string // myworktree instance id (used for the state dir name)
	WorktreePath string // serve working directory (session is scoped to it)
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
	_ = os.Remove(portFile) // stale port from a previous run

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
	cmd := exec.Command(bin, args...)
	cmd.Dir = in.WorktreePath
	cmd.Env = append(os.Environ(), "REASONIX_HOME="+home)
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
		<-done // reap after kill
		return Info{}, err
	}
	return info, nil
}

func (d *Driver) waitReady(instanceID string, done <-chan error, portFile, pidFile, token string, timeout time.Duration) (Info, error) {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		select {
		case err := <-done:
			return Info{}, fmt.Errorf("reasonix: serve exited early for instance %s (see %s): %v", instanceID, filepath.Join(d.dir(instanceID), "serve.log"), err)
		default:
		}
		if b, err := os.ReadFile(portFile); err == nil {
			if addr := strings.TrimSpace(string(b)); addr != "" {
				if _, port, err := net.SplitHostPort(addr); err == nil {
					if p, perr := strconv.Atoi(port); perr == nil && p > 0 {
						// The tracked pid comes from reasonix's own pid file:
						// serve may spawn a wrapper, so the exec.Cmd pid is
						// not necessarily the process Stop/Health target.
						pid := cmdProcessPID(pidFile)
						return Info{PID: pid, Port: p, Token: token}, nil
					}
				}
			}
		}
		time.Sleep(150 * time.Millisecond)
	}
	return Info{}, fmt.Errorf("reasonix: serve did not become ready within %v for instance %s (see %s)", timeout, instanceID, filepath.Join(d.dir(instanceID), "serve.log"))
}

// Addr reads the tracked port/token for an instance without probing the
// process. Used by the reverse proxy.
func (d *Driver) Addr(instanceID string) (Info, error) {
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
