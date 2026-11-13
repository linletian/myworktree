// One-time retirement of persisted Reasonix Web instances (issue #160).
//
// The dedicated `kind=reasonix` feature is being removed. Existing users
// still have `kind=reasonix` records in <project-data-dir>/state.json plus
// serve-management directories under <project-data-dir>/reasonix/<id>/, and
// a `reasonix serve` subprocess may still be alive across the upgrade
// (reasonix was a RestartSurvivor kind, so its serve outlived the previous
// daemon). Without this file, the removal would leave records the UI no
// longer shows and processes nobody owns.
//
// Fixed order, never rearranged (PLAN §3.A):
//
//	identify legacy records → prepare local backup → stop + clean → save once
//
// Everything runs before framework.Manager.ReconcileRunningOnStartup, so it
// must not depend on Reattach or on a registered kind. This file therefore
// deliberately does NOT import internal/instance/reasonix: the two liveness
// probes and the bounded stop are the minimal legacy shape extracted from
// that package's Health/Stop so the package itself can disappear. It adds
// one thing the old Driver.Stop never did — confirming the pid is gone
// before the state commit (PLAN §3.A2.3).
//
// Power boundary: a signal is sent ONLY when the record's own pid file and
// port file both parse AND that pid is alive AND that TCP port accepts a
// connection. Missing files, garbage values, a dead pid or a closed port are
// stale records: no signal at all. There is no process-name scan, no
// command-line pattern, no /proc walk, and no RemoveAll of any directory.
//
// Data boundary: only the five known management files (token, port, pid,
// serve.log, serve.log.1) are removed. A management directory that still
// holds anything else — an early home/, an old session, any unknown entry —
// is kept verbatim with a sanitized warning. ~/.reasonix, custom Reasonix
// homes and CLI/Studio installs are never touched.
package app

import (
	"errors"
	"fmt"
	"io"
	"log"
	"net"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"

	"myworktree/internal/store"
)

const (
	// reasonixRetirementBackupName is the pre-change copy of state.json kept
	// beside it. It restores instance RECORDS only — never a stopped process,
	// a runtime token or an unsent draft. It stays on the machine: it must
	// never be committed, logged or copied into evidence.
	reasonixRetirementBackupName = "state.json.reasonix-removal.backup"

	// reasonixLegacyDirName is the parent of every per-instance
	// serve-management directory (the retired driver's DataDir layout).
	reasonixLegacyDirName = "reasonix"

	// Bounded stop windows, matching the retired Driver.Stop: SIGTERM, wait,
	// SIGKILL, confirm.
	reasonixLegacyTermGrace = 5 * time.Second
	reasonixLegacyKillGrace = 2 * time.Second
	reasonixLegacyPollEvery = 100 * time.Millisecond

	// reasonixLegacyLoopback is the only host the legacy probe dials — the
	// retired serve always bound 127.0.0.1.
	reasonixLegacyLoopback = "127.0.0.1"
)

// reasonixLegacyManagedFiles is the deletion allowlist inside a legacy
// management directory, exactly the files the retired driver wrote (see the
// internal/instance/reasonix package comment). Nothing else may be deleted.
var reasonixLegacyManagedFiles = []string{"token", "port", "pid", "serve.log", "serve.log.1"}

// Failure categories. Logged as a category name only, so an underlying
// *os.PathError (which embeds an absolute path under the user's home) never
// reaches the daemon log.
const (
	reasonixFailStateLoad = "state-load"
	reasonixFailUnsafeID  = "unsafe-id"
	reasonixFailBackup    = "backup"
	reasonixFailStop      = "stop-confirm"
	reasonixFailCleanup   = "cleanup"
	reasonixFailStateSave = "state-save"
)

// legacyReasonixStore is the store surface retirement needs — the same two
// methods framework.InstanceStore declares, satisfied by store.FileStore.
type legacyReasonixStore interface {
	Load() (store.State, error)
	SaveWithVersion(st store.State, expectedVersion int64) error
}

// legacyProcessOps is the process-power seam of the bounded stop. Production
// wires the real syscalls; tests inject a recorder so "no signal at all" and
// "confirm failed → abort" are provable without owning extra processes.
type legacyProcessOps struct {
	alive     func(pid int) bool
	dial      func(host string, port int) bool
	signal    func(pid int, sig syscall.Signal) error
	sleep     func(d time.Duration)
	termGrace time.Duration
	killGrace time.Duration
}

func defaultLegacyProcessOps() legacyProcessOps {
	return legacyProcessOps{
		alive:     legacyProcessAlive,
		dial:      legacyTCPAlive,
		signal:    syscall.Kill,
		sleep:     time.Sleep,
		termGrace: reasonixLegacyTermGrace,
		killGrace: reasonixLegacyKillGrace,
	}
}

// legacyProcessAlive is kill(pid, 0) only — no /proc, which does not exist
// on macOS (a supported platform). Same probe the retired Health used.
func legacyProcessAlive(pid int) bool {
	if pid <= 0 {
		return false
	}
	return syscall.Kill(pid, 0) == nil
}

// legacyTCPAlive probes host:port with a short dial. A pid that is alive but
// not serving is a stale record, never a stop target.
func legacyTCPAlive(host string, port int) bool {
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

// reasonixRetirementReport counts. It carries numbers, not paths, ids or
// tokens, because it is what the daemon log prints.
type reasonixRetirementReport struct {
	LegacyFound   int
	Retired       int
	ServesStopped int
	StaleRecords  int
	DirsRetained  int
	// Backup names what happened to the pre-removal backup: created, reused,
	// or reused after this run tightened an over-open mode on it.
	Backup string
	// StateSaved is the only honest "committed" flag: it is set after
	// SaveWithVersion returned nil and never on an aborted run.
	StateSaved bool
}

// reasonixRetirementError names the stage that aborted. detail is authored
// here and holds no path; inner keeps the original error for errors.Is and
// for the caller's own handling, and is never logged by this package.
type reasonixRetirementError struct {
	category string
	detail   string
	inner    error
}

func (e *reasonixRetirementError) Error() string {
	return "reasonix retirement aborted [" + e.category + "]: " + e.detail
}

func (e *reasonixRetirementError) Unwrap() error { return e.inner }

// reasonixRetirementCategory is the loggable label of an error.
func reasonixRetirementCategory(err error) string {
	var re *reasonixRetirementError
	if errors.As(err, &re) {
		return re.category
	}
	return "unknown"
}

// legacyReasonixEnv is the injected world of one retirement run: where the
// state lives, where management directories live, and how to touch processes.
type legacyReasonixEnv struct {
	Store       legacyReasonixStore
	StatePath   string
	BackupPath  string
	ManagedRoot string
	Ops         legacyProcessOps
	Logger      *log.Logger
}

// legacyReasonixEnvFor wires the production env off a Server.
func (s *Server) legacyReasonixEnv() legacyReasonixEnv {
	return legacyReasonixEnv{
		Store:       s.store,
		StatePath:   s.store.Path,
		BackupPath:  filepath.Join(s.dataDir, reasonixRetirementBackupName),
		ManagedRoot: filepath.Join(s.dataDir, reasonixLegacyDirName),
		Ops:         defaultLegacyProcessOps(),
		Logger:      s.logger,
	}
}

func (env legacyReasonixEnv) logf(format string, args ...any) {
	if env.Logger != nil {
		env.Logger.Printf("[reasonix-retire] "+format, args...)
	}
}

// retireLegacyReasonixInstances is the Server-facing wrapper called once from
// Start, before ReconcileRunningOnStartup. A failure never fails startup and
// never claims success: it prints the stage category and the counts collected
// so far, leaves the records on disk, and the next startup retries the same
// idempotent path (by then the management files of a stale record are gone,
// so no signal can be sent again).
func (s *Server) retireLegacyReasonixOnStartup() {
	// Split so the report half is testable without a subprocess.
	s.reportReasonixRetirement(retireLegacyReasonixInstances(s.legacyReasonixEnv()))
}

// reportReasonixRetirement prints counts and the aborting category only.
func (s *Server) reportReasonixRetirement(rep reasonixRetirementReport, err error) {
	if err != nil {
		s.logger.Printf("[reasonix-retire] aborted category=%s legacy_found=%d retired=%d serves_stopped=%d",
			reasonixRetirementCategory(err), rep.LegacyFound, rep.Retired, rep.ServesStopped)
		return
	}
	if rep.LegacyFound == 0 {
		// Nothing to retire: no state write, no backup, no directory scan,
		// no signal — and no log noise on every single start.
		return
	}
	s.logger.Printf("[reasonix-retire] retired=%d serves_stopped=%d stale=%d dirs_retained=%d backup=%s",
		rep.Retired, rep.ServesStopped, rep.StaleRecords, rep.DirsRetained, rep.Backup)
}

// retireLegacyReasonixInstances runs the fixed sequence and returns the
// counts plus the first aborting stage. See the package comment for the
// contract each step implements.
func retireLegacyReasonixInstances(env legacyReasonixEnv) (reasonixRetirementReport, error) {
	rep := reasonixRetirementReport{}

	st, err := env.Store.Load()
	if err != nil {
		return rep, &reasonixRetirementError{category: reasonixFailStateLoad, detail: "state load failed", inner: err}
	}

	// Step 1 — identify. Only an explicit kind=reasonix record is legacy: an
	// empty Kind means pty, and every other kind is not ours to touch.
	legacy := make([]store.ManagedInstance, 0, len(st.Instances))
	legacyIDs := make(map[string]bool, len(st.Instances))
	for _, it := range st.Instances {
		if it.Kind == store.KindReasonix {
			legacy = append(legacy, it)
			legacyIDs[it.ID] = true
		}
	}
	rep.LegacyFound = len(legacy)
	if rep.LegacyFound == 0 {
		// Step 1.2: no legacy record → no backup, no scan, no save, no signal.
		return rep, nil
	}

	// Step 1.3 — validate ids BEFORE anything is created, modified or deleted,
	// so a traversal-shaped id aborts a run that has touched nothing.
	dirs := make(map[string]string, len(legacy))
	for _, it := range legacy {
		dir, verr := legacyReasonixManagedDir(env.ManagedRoot, it.ID)
		if verr != nil {
			return rep, &reasonixRetirementError{category: reasonixFailUnsafeID,
				detail: fmt.Sprintf("legacy instance id %q is not a safe single path segment", it.ID), inner: verr}
		}
		dirs[it.ID] = dir
	}

	// Step 2 — backup (A3.1): before the first state write and before the
	// first file removal.
	backup, berr := prepareLegacyReasonixBackup(env.StatePath, env.BackupPath)
	if berr != nil {
		return rep, &reasonixRetirementError{category: reasonixFailBackup,
			detail: "pre-removal backup unavailable; nothing stopped, no record deleted", inner: berr}
	}
	rep.Backup = backup

	// Step 3 — stop then clean, per record.
	for _, it := range legacy {
		dir := dirs[it.ID]
		stopped, serr := stopLegacyReasonixServe(it.ID, dir, env.Ops)
		if serr != nil {
			// A2.3: an unconfirmed pid aborts the commit. This record keeps
			// its pid/port files so the next start can find the process again;
			// the state file is never written, so the record is not hidden.
			return rep, &reasonixRetirementError{category: reasonixFailStop,
				detail: fmt.Sprintf("legacy serve pid for instance %s was not confirmed exited; state not saved", it.ID), inner: serr}
		}
		if stopped {
			rep.ServesStopped++
		} else {
			rep.StaleRecords++
		}
		retained, unknown, cerr := cleanLegacyReasonixDir(dir)
		if cerr != nil {
			return rep, &reasonixRetirementError{category: reasonixFailCleanup,
				detail: fmt.Sprintf("management file cleanup failed for instance %s", it.ID), inner: cerr}
		}
		if retained {
			rep.DirsRetained++
			// Sanitized by construction: an id and a count, never a path,
			// never a file name beyond the allowlist this package owns.
			env.logf("instance %s management directory retained unknown_entries=%d", it.ID, unknown)
		}
		rep.Retired++
	}

	// Step 4 — one commit of the in-memory copy, with the version check.
	newState := stripLegacyReasonixInstances(st, legacyIDs)
	if err := env.Store.SaveWithVersion(newState, st.Version); err != nil {
		// The backup stays, the old state stays on disk, and no "migrated"
		// message is printed. The next start retries.
		return rep, &reasonixRetirementError{category: reasonixFailStateSave,
			detail: "state save failed; backup kept, records left for the next startup", inner: err}
	}
	rep.StateSaved = true
	return rep, nil
}

// legacyReasonixManagedDir returns the only directory retirement may ever
// touch for one legacy id. The id must be a safe single path segment; the
// joined result is re-checked against ManagedRoot with filepath.Rel so a
// surprise (an empty root, an id that normalises upward) cannot escape.
func legacyReasonixManagedDir(managedRoot, id string) (string, error) {
	if id == "" {
		return "", errors.New("instance id is required")
	}
	// Control characters would forge log lines: this id is logged.
	for _, r := range id {
		if r < 0x20 || r == 0x7f {
			return "", errors.New("instance id contains control characters")
		}
	}
	if id == "." || id == ".." {
		return "", errors.New("instance id must not be a dot segment")
	}
	if strings.ContainsRune(id, '/') || strings.ContainsRune(id, '\\') || strings.ContainsRune(id, filepath.Separator) {
		return "", errors.New("instance id must not contain a path separator")
	}
	if filepath.IsAbs(id) || filepath.Base(id) != id {
		return "", errors.New("instance id must be a single path segment")
	}
	if strings.TrimSpace(id) != id {
		// Blank padding is invisible in an audit and turns a directory name
		// into "there is no id". Real ids come from newID() (hex).
		return "", errors.New("instance id must not have blank padding")
	}
	if strings.TrimSpace(managedRoot) == "" {
		return "", errors.New("managed root is required")
	}
	dir := filepath.Join(managedRoot, id)
	rel, err := filepath.Rel(managedRoot, dir)
	if err != nil {
		return "", err
	}
	if rel != id {
		return "", fmt.Errorf("instance id escapes the managed root (resolves to %q)", rel)
	}
	return dir, nil
}

// prepareLegacyReasonixBackup creates the 0600 pre-removal backup, or reuses
// an existing regular one. It never overwrites: the first backup holds the
// pre-change records, and a second run must not dilute it. An existing entry
// of any other type (directory, symlink, socket) aborts rather than being
// replaced, and a half-written copy is removed so the retry starts clean.
//
// The returned label is "created", "reused" or "reused-open-perms". The last
// one means the file already existed with group/other bits set — written by
// something other than this code, since our own creation is 0600. It is kept
// byte-for-byte and the mode is NOT changed: tightening a file this run did
// not create would be an unrequested edit of user data. The caller logs the
// case as a sanitized warning.
func prepareLegacyReasonixBackup(statePath, backupPath string) (string, error) {
	if backupPath == "" {
		return "", errors.New("backup path is required")
	}
	if fi, err := os.Lstat(backupPath); err == nil {
		if !fi.Mode().IsRegular() {
			return "", errors.New("backup path exists but is not a regular file")
		}
		if fi.Mode().Perm()&0o077 != 0 {
			return "reused-open-perms", nil
		}
		return "reused", nil
	} else if !errors.Is(err, os.ErrNotExist) {
		return "", err
	}
	if statePath == "" {
		return "", errors.New("state path is required")
	}
	if filepath.Clean(statePath) == filepath.Clean(backupPath) {
		return "", errors.New("state path and backup path are the same")
	}
	src, err := os.Open(statePath)
	if err != nil {
		return "", err
	}
	defer src.Close()
	dst, err := os.OpenFile(backupPath, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		// O_EXCL is the race guard: a concurrent start either writes the
		// backup or loses here and leaves the winner's bytes untouched.
		return "", err
	}
	if _, err := io.Copy(dst, src); err != nil {
		_ = dst.Close()
		_ = os.Remove(backupPath)
		return "", err
	}
	if err := dst.Sync(); err != nil {
		_ = dst.Close()
		_ = os.Remove(backupPath)
		return "", err
	}
	if err := dst.Close(); err != nil {
		return "", err
	}
	return "created", nil
}

// stopLegacyReasonixServe performs the bounded stop of ONE legacy serve, and
// only of that one. The gate is conjunctive — a valid pid file, a valid port
// file, that pid alive, that port accepting a connection — because a stored
// PID alone is not proof of ownership (the kernel reuses pids).
//
// stopped=false means "stale record, no signal sent" and is NOT an error.
// A non-nil error means the signal was sent and the pid was NOT confirmed
// gone within term+kill grace; the caller must then abort the state commit.
func stopLegacyReasonixServe(instanceID, dir string, ops legacyProcessOps) (bool, error) {
	pid, ok := readLegacyReasonixPID(dir)
	if !ok {
		return false, nil // missing / unreadable / unparseable / non-positive
	}
	port, ok := readLegacyReasonixPort(dir)
	if !ok {
		return false, nil // missing or malformed → not a serving process we can address
	}
	if !ops.alive(pid) {
		return false, nil // dead or zombie
	}
	if !ops.dial(reasonixLegacyLoopback, port) {
		return false, nil // alive but not serving → stale, never a kill target
	}
	// Process group first, single pid as fallback — the retired driver's
	// signalling shape, and the reason the fixture runs in its own group.
	if err := ops.signal(-pid, syscall.SIGTERM); err != nil {
		_ = ops.signal(pid, syscall.SIGTERM)
	}
	if waitForLegacyReasonixExit(pid, ops, ops.termGrace) {
		return true, nil
	}
	if err := ops.signal(-pid, syscall.SIGKILL); err != nil {
		_ = ops.signal(pid, syscall.SIGKILL)
	}
	if waitForLegacyReasonixExit(pid, ops, ops.killGrace) {
		return true, nil
	}
	return false, fmt.Errorf("legacy serve pid %d (instance %s) still alive after SIGTERM and SIGKILL", pid, instanceID)
}

func waitForLegacyReasonixExit(pid int, ops legacyProcessOps, grace time.Duration) bool {
	deadline := time.Now().Add(grace)
	for {
		if !ops.alive(pid) {
			return true
		}
		if !time.Now().Before(deadline) {
			return false
		}
		ops.sleep(reasonixLegacyPollEvery)
	}
}

// readLegacyReasonixPID reads the pid file the retired --pid-file wrote.
// Anything other than a positive integer is "no usable pid".
func readLegacyReasonixPID(dir string) (int, bool) {
	b, err := os.ReadFile(filepath.Join(dir, "pid"))
	if err != nil {
		return 0, false
	}
	pid, err := strconv.Atoi(strings.TrimSpace(string(b)))
	if err != nil || pid <= 0 {
		return 0, false
	}
	return pid, true
}

// readLegacyReasonixPort reads the --port-file, which holds a bound address
// ("127.0.0.1:4321"), exactly as the retired addrFromFiles parsed it.
func readLegacyReasonixPort(dir string) (int, bool) {
	b, err := os.ReadFile(filepath.Join(dir, "port"))
	if err != nil {
		return 0, false
	}
	addr := strings.TrimSpace(string(b))
	if addr == "" {
		return 0, false
	}
	_, portStr, err := net.SplitHostPort(addr)
	if err != nil {
		return 0, false
	}
	port, err := strconv.Atoi(portStr)
	if err != nil || port <= 0 || port > 65535 {
		return 0, false
	}
	return port, true
}

// cleanLegacyReasonixDir deletes ONLY the allowlisted regular files, then
// removes the directory itself when — and only when — it is empty. Unknown
// entries (an early home/, an old session, a symlink, a directory under a
// known name) are kept verbatim and reported as a count.
//
// retained reports that the directory still holds content.
func cleanLegacyReasonixDir(dir string) (retained bool, unknown int, err error) {
	if _, err := os.Lstat(dir); err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return false, 0, nil // never started, nothing to clean
		}
		return false, 0, err
	}
	for _, name := range reasonixLegacyManagedFiles {
		p := filepath.Join(dir, name)
		fi, lerr := os.Lstat(p)
		if errors.Is(lerr, os.ErrNotExist) {
			continue
		}
		if lerr != nil {
			return false, 0, lerr
		}
		if !fi.Mode().IsRegular() {
			// A known name occupied by something that is not a plain file is
			// unknown content: keep it, and never recurse into it.
			continue
		}
		if rmErr := os.Remove(p); rmErr != nil && !errors.Is(rmErr, os.ErrNotExist) {
			return false, 0, rmErr
		}
	}
	entries, rerr := os.ReadDir(dir)
	if rerr != nil {
		return false, 0, rerr
	}
	if len(entries) == 0 {
		if rmErr := os.Remove(dir); rmErr != nil && !errors.Is(rmErr, os.ErrNotExist) {
			return false, 0, rmErr
		}
		return false, 0, nil
	}
	return true, len(entries), nil
}

// stripLegacyReasonixInstances builds the in-memory copy that step 4 commits:
// legacy records gone, their ids gone from every tab_order list, everything
// else — records, worktrees, order, version — untouched and in place.
func stripLegacyReasonixInstances(st store.State, legacyIDs map[string]bool) store.State {
	out := store.State{
		Worktrees: append([]store.ManagedWorktree(nil), st.Worktrees...),
		Instances: make([]store.ManagedInstance, 0, len(st.Instances)),
		TabOrder:  make(map[string][]string, len(st.TabOrder)),
		Version:   st.Version,
	}
	for _, it := range st.Instances {
		if it.Kind == store.KindReasonix {
			continue
		}
		out.Instances = append(out.Instances, it)
	}
	for wt, ids := range st.TabOrder {
		kept := make([]string, 0, len(ids))
		for _, id := range ids {
			if legacyIDs[id] {
				continue
			}
			kept = append(kept, id)
		}
		out.TabOrder[wt] = kept
	}
	return out
}
