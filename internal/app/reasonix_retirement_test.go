package app

import (
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"

	"myworktree/internal/store"
)

// ---------------------------------------------------------------------------
// Test doubles
// ---------------------------------------------------------------------------

// rrEvents is the ordered log that proves the SEQUENCE (backup → signal →
// save), not merely the end state.
type rrEvents struct {
	mu      sync.Mutex
	names   []string
	signals []string
}

func (e *rrEvents) add(name string) {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.names = append(e.names, name)
}

func (e *rrEvents) addSignal(s string) {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.signals = append(e.signals, s)
}

func (e *rrEvents) order() []string {
	e.mu.Lock()
	defer e.mu.Unlock()
	return append([]string(nil), e.names...)
}

func (e *rrEvents) sent() []string {
	e.mu.Lock()
	defer e.mu.Unlock()
	return append([]string(nil), e.signals...)
}

func (e *rrEvents) index(name string) int {
	e.mu.Lock()
	defer e.mu.Unlock()
	for i, n := range e.names {
		if n == name {
			return i
		}
	}
	return -1
}

// rrStore wraps the real store.FileStore, records the call order and injects
// the save failures the contract demands an abort for.
type rrStore struct {
	inner      store.FileStore
	ev         *rrEvents
	failSave   error
	beforeSave func()
}

func (s *rrStore) Load() (store.State, error) {
	s.ev.add("load")
	return s.inner.Load()
}

func (s *rrStore) SaveWithVersion(st store.State, expected int64) error {
	s.ev.add("save")
	if s.beforeSave != nil {
		s.beforeSave()
	}
	if s.failSave != nil {
		return s.failSave
	}
	return s.inner.SaveWithVersion(st, expected)
}

// rrProcs is the process-power seam: nothing in here can touch a real
// process. It records every signal so "not one signal too many" is provable,
// and `stubborn` models a pid that survives TERM and KILL so the
// confirm-failure abort is reachable without an unkillable process.
type rrProcs struct {
	ev         *rrEvents
	mu         sync.Mutex
	alivePids  map[int]bool
	dialPorts  map[int]bool
	stubborn   map[int]bool
	dying      map[int]int // pid → polls left before it reports exited
	groupFails bool        // the process-group signal fails (ESRCH / EPERM)
}

func newRRProcs(ev *rrEvents) *rrProcs {
	return &rrProcs{
		ev:        ev,
		alivePids: map[int]bool{},
		dialPorts: map[int]bool{},
		stubborn:  map[int]bool{},
		dying:     map[int]int{},
	}
}

func (p *rrProcs) ops() legacyProcessOps {
	return legacyProcessOps{
		alive:     p.alive,
		dial:      p.dial,
		signal:    p.signal,
		sleep:     p.sleep,
		termGrace: 500 * time.Millisecond,
		killGrace: 500 * time.Millisecond,
	}
}

func (p *rrProcs) alive(pid int) bool {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.alivePids[pid]
}

func (p *rrProcs) dial(_ string, port int) bool {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.dialPorts[port]
}

func (p *rrProcs) signal(pid int, sig syscall.Signal) error {
	p.ev.addSignal(fmt.Sprintf("%d/%d", pid, int(sig)))
	p.mu.Lock()
	defer p.mu.Unlock()
	// A negative target is a process GROUP: the group leader's pid is the
	// member that must report exited.
	target := pid
	if pid < 0 {
		if p.groupFails {
			return syscall.ESRCH
		}
		target = -pid
	}
	if p.stubborn[target] {
		return nil
	}
	if p.alivePids[target] {
		// Exit one poll later, so the bounded wait loop is exercised rather
		// than shortcut by an instant flip.
		p.dying[target] = 1
	}
	return nil
}

func (p *rrProcs) sleep(time.Duration) {
	p.mu.Lock()
	defer p.mu.Unlock()
	for pid, left := range p.dying {
		if left <= 1 {
			delete(p.dying, pid)
			delete(p.alivePids, pid)
			continue
		}
		p.dying[pid] = left - 1
	}
}

func (p *rrProcs) live(pid, port int) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.alivePids[pid] = true
	if port > 0 {
		p.dialPorts[port] = true
	}
}

// sent is the recorded signal list: what this code tried to kill, in order.
func (p *rrProcs) sent() []string { return p.ev.sent() }

func (p *rrProcs) setPortOpen(port int, open bool) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.dialPorts[port] = open
}

// syncBuffer is a goroutine-safe log target.
type syncBuffer struct {
	mu  sync.Mutex
	buf []byte
}

func (b *syncBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.buf = append(b.buf, p...)
	return len(p), nil
}

func (b *syncBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return string(b.buf)
}

// ---------------------------------------------------------------------------
// Fixtures
// ---------------------------------------------------------------------------

// rrLegacyID is the id carried by the fixture's legacy record: a safe single
// path segment.
const rrLegacyID = "rxlegacy0001"

const (
	rrSIGTERMv = int(syscall.SIGTERM)
	rrSIGKILLv = int(syscall.SIGKILL)
)

type rrFixture struct {
	dataDir   string
	statePath string
	bakPath   string
	mroot     string
	logBuf    *syncBuffer
	ev        *rrEvents
	procs     *rrProcs
}

func newRRFixture(t *testing.T, st store.State) *rrFixture {
	t.Helper()
	return newRRFixtureIn(t, t.TempDir(), st)
}

func newRRFixtureIn(t *testing.T, dataDir string, st store.State) *rrFixture {
	t.Helper()
	f := &rrFixture{
		dataDir:   dataDir,
		statePath: filepath.Join(dataDir, "state.json"),
		bakPath:   filepath.Join(dataDir, reasonixRetirementBackupName),
		mroot:     filepath.Join(dataDir, reasonixLegacyDirName),
		logBuf:    &syncBuffer{},
	}
	f.ev = &rrEvents{}
	f.procs = newRRProcs(f.ev)
	if err := (store.FileStore{Path: f.statePath}).Save(st); err != nil {
		t.Fatalf("seed state.json: %v", err)
	}
	return f
}

func (f *rrFixture) store() *rrStore {
	return &rrStore{inner: store.FileStore{Path: f.statePath}, ev: f.ev}
}

func (f *rrFixture) env() legacyReasonixEnv { return f.envWith(f.procs, f.store()) }

// realEnv wires the production syscalls: the fixture's serve is a real child
// process on a real port, so the gate has to be real too.
func (f *rrFixture) realEnv() legacyReasonixEnv {
	e := f.env()
	e.Ops = defaultLegacyProcessOps()
	return e
}

func (f *rrFixture) envWith(p *rrProcs, st legacyReasonixStore) legacyReasonixEnv {
	return legacyReasonixEnv{
		Store:       st,
		StatePath:   f.statePath,
		BackupPath:  f.bakPath,
		ManagedRoot: f.mroot,
		Ops:         p.ops(),
		Logger:      log.New(f.logBuf, "", 0),
	}
}

func (f *rrFixture) legacyDir(id string) string { return filepath.Join(f.mroot, id) }

func (f *rrFixture) loadState(t *testing.T) store.State {
	t.Helper()
	st, err := (store.FileStore{Path: f.statePath}).Load()
	if err != nil {
		t.Fatalf("load state.json: %v", err)
	}
	return st
}

func (f *rrFixture) stateBytes(t *testing.T) []byte {
	t.Helper()
	return readFileBytes(t, f.statePath)
}

func (f *rrFixture) assertNoLegacyRecords(t *testing.T) {
	t.Helper()
	for _, it := range f.loadState(t).Instances {
		if it.Kind == store.KindReasonix {
			t.Fatalf("a kind=reasonix record survived: %s", it.ID)
		}
	}
	for wt, ids := range f.loadState(t).TabOrder {
		for _, id := range ids {
			if id == rrLegacyID {
				t.Fatalf("legacy id still in tab_order[%s]: %v", wt, ids)
			}
		}
	}
}

// buildMixedState: one legacy record among the other kinds, with cross
// referencing tab_order.
func buildMixedState() store.State {
	return store.State{
		Worktrees: []store.ManagedWorktree{
			{ID: "__main__", Name: "repo", Path: "/srv/repo", Branch: "main"},
			{ID: "wt2", Name: "other", Path: "/srv/other", Branch: "feat"},
		},
		Instances: []store.ManagedInstance{
			{ID: "pty1", WorktreeID: "__main__", Kind: store.KindPTY, Name: "shell", PID: 4001, Status: "running"},
			{ID: rrLegacyID, WorktreeID: "__main__", Kind: store.KindReasonix, Name: "rx", PID: 4002, Status: "running"},
			{ID: "oc1", WorktreeID: "wt2", Kind: "opencode-web", Name: "oc", PID: 4003, Status: "stopped"},
			{ID: "dsh1", WorktreeID: "wt2", Kind: store.KindDsh, Name: "dsh", PID: 4004, Status: "running"},
			{ID: "oldpty", WorktreeID: "__main__", Kind: "", Name: "ancient", PID: 4005, Status: "stopped"},
		},
		TabOrder: map[string][]string{
			"__main__": {"pty1", rrLegacyID, "oldpty"},
			"wt2":      {rrLegacyID, "oc1", "dsh1"},
		},
		Version: 17,
	}
}

func writeFile(t *testing.T, path string, mode os.FileMode, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("mkdir %s: %v", filepath.Dir(path), err)
	}
	if err := os.WriteFile(path, []byte(content), mode); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
}

func readFileBytes(t *testing.T, path string) []byte {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	return b
}

func mustNotExist(t *testing.T, path string) {
	t.Helper()
	if _, err := os.Lstat(path); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("%s must not exist (err=%v)", path, err)
	}
}

// seedManagedDir writes the five allowlisted management files in the exact
// shape the retired driver left behind (pid as text, port as bound host:port),
// plus whatever extras the case needs.
func seedManagedDir(t *testing.T, dir string, pid int, port int, extras map[string]string) {
	t.Helper()
	writeFile(t, filepath.Join(dir, "pid"), 0o600, strconv.Itoa(pid)+"\n")
	if port > 0 {
		writeFile(t, filepath.Join(dir, "port"), 0o600, net.JoinHostPort("127.0.0.1", strconv.Itoa(port))+"\n")
	}
	writeFile(t, filepath.Join(dir, "token"), 0o600, "fixture-token-not-logged\n")
	writeFile(t, filepath.Join(dir, "serve.log"), 0o600, "fixture serve log\n")
	writeFile(t, filepath.Join(dir, "serve.log.1"), 0o600, "fixture rotated log\n")
	for rel, content := range extras {
		writeFile(t, filepath.Join(dir, filepath.FromSlash(rel)), 0o600, content)
	}
}

// rrAssertLogClean: the daemon log carries counts and categories only — no
// absolute path, no home, no token/cookie/command-line material.
func rrAssertLogClean(t *testing.T, logText, dataDir string) {
	t.Helper()
	l := strings.ToLower(logText)
	for _, forbidden := range []string{strings.ToLower(dataDir), "/home/", "/srv/", "~", "auth.json", "reasonix-removal.backup", "token", "cookie", "bearer", "sk-", "home/", "command", "session"} {
		if strings.Contains(l, forbidden) {
			t.Fatalf("log leaked %q: %q", forbidden, logText)
		}
	}
}

// ---------------------------------------------------------------------------
// Row: no legacy record — no state write, no backup, no scan, no signal
// ---------------------------------------------------------------------------

func TestRetireNoLegacyRecordsChangesNothing(t *testing.T) {
	st := store.State{
		Worktrees: []store.ManagedWorktree{{ID: "__main__", Name: "repo", Path: "/srv/repo"}},
		Instances: []store.ManagedInstance{
			{ID: "pty1", Kind: store.KindPTY, Status: "running"},
			{ID: "oc1", Kind: "opencode-web", Status: "running"},
			{ID: "dsh1", Kind: store.KindDsh, Status: "running"},
			{ID: "old1", Kind: "", Status: "stopped"}, // "" is pty, not legacy
		},
		TabOrder: map[string][]string{"__main__": {"pty1", "oc1", "dsh1", "old1"}},
		Version:  9,
	}
	f := newRRFixture(t, st)
	before := f.stateBytes(t)

	rep, err := retireLegacyReasonixInstances(f.env())
	if err != nil {
		t.Fatalf("retire: %v", err)
	}
	if rep.LegacyFound != 0 || rep.Retired != 0 || rep.ServesStopped != 0 || rep.StaleRecords != 0 || rep.StateSaved {
		t.Fatalf("report must be all-zero: %+v", rep)
	}
	if string(f.stateBytes(t)) != string(before) {
		t.Fatalf("state.json must not be rewritten")
	}
	mustNotExist(t, f.bakPath) // no backup
	mustNotExist(t, f.mroot)   // no directory scan or creation
	if got := f.procs.sent(); len(got) != 0 {
		t.Fatalf("no signal may be sent: %v", got)
	}
	if f.ev.index("save") >= 0 {
		t.Fatalf("SaveWithVersion must not be called: %v", f.ev.order())
	}
	if f.logBuf.String() != "" {
		t.Fatalf("a clean state must be silent: %q", f.logBuf.String())
	}
}

// ---------------------------------------------------------------------------
// Row: mixed legacy state — only the legacy record and its tab ids vanish
// ---------------------------------------------------------------------------

func TestRetireMixedStateKeepsEverythingElseUnchanged(t *testing.T) {
	st := buildMixedState()
	f := newRRFixture(t, st)
	f.procs.live(4002, 4321)
	seedManagedDir(t, f.legacyDir(rrLegacyID), 4002, 4321, nil)
	before := f.stateBytes(t)

	rep, err := retireLegacyReasonixInstances(f.env())
	if err != nil {
		t.Fatalf("retire: %v", err)
	}
	if rep.LegacyFound != 1 || rep.Retired != 1 || rep.ServesStopped != 1 || rep.Backup != "created" || !rep.StateSaved {
		t.Fatalf("unexpected report: %+v", rep)
	}

	after := f.loadState(t)
	var want []store.ManagedInstance
	for _, it := range st.Instances {
		if it.ID != rrLegacyID {
			want = append(want, it)
		}
	}
	if len(after.Instances) != len(want) {
		t.Fatalf("instance count: got %d want %d", len(after.Instances), len(want))
	}
	for i := range want {
		if rrJSON(after.Instances[i]) != rrJSON(want[i]) {
			t.Fatalf("instance %d changed: got %s want %s", i, rrJSON(after.Instances[i]), rrJSON(want[i]))
		}
	}
	if len(after.Worktrees) != len(st.Worktrees) {
		t.Fatalf("worktrees changed: %+v", after.Worktrees)
	}
	for i := range st.Worktrees {
		if after.Worktrees[i] != st.Worktrees[i] {
			t.Fatalf("worktree %d changed", i)
		}
	}
	wantTab := map[string][]string{
		"__main__": {"pty1", "oldpty"},
		"wt2":      {"oc1", "dsh1"},
	}
	if len(after.TabOrder) != len(wantTab) {
		t.Fatalf("tab_order key set changed: %v", after.TabOrder)
	}
	for wt, ids := range wantTab {
		if strings.Join(after.TabOrder[wt], ",") != strings.Join(ids, ",") {
			t.Fatalf("tab_order[%s]: got %v want %v", wt, after.TabOrder[wt], ids)
		}
	}
	if after.Version != st.Version+1 {
		t.Fatalf("version: got %d want %d", after.Version, st.Version+1)
	}
	// Sequence proof: the backup holds the PRE-change bytes, so it was
	// written before this save, and the only signal is the one SIGTERM to
	// this record's process group.
	if got := f.procs.sent(); len(got) != 1 || got[0] != fmt.Sprintf("-4002/%d", rrSIGTERMv) {
		t.Fatalf("exactly one SIGTERM to the process group: %v", got)
	}
	if string(f.stateBytes(t)) == string(before) {
		t.Fatalf("state must have been rewritten")
	}
	if got := readFileBytes(t, f.bakPath); string(got) != string(before) {
		t.Fatalf("backup must hold the pre-change bytes exactly")
	}
	if fi, err := os.Lstat(f.bakPath); err != nil || !fi.Mode().IsRegular() || fi.Mode().Perm() != 0o600 {
		t.Fatalf("backup type/mode: %v %v", fi.Mode(), err)
	}
	// The emptied management directory is removed with os.Remove only.
	mustNotExist(t, f.legacyDir(rrLegacyID))
	rrAssertLogClean(t, f.logBuf.String(), f.dataDir)
}

// ---------------------------------------------------------------------------
// Row: a live legacy serve is stopped — REAL child process, REAL loopback
// listener on port 0, own process group. A live sibling of another kind and a
// stale legacy record are both left unsignalled.
// ---------------------------------------------------------------------------

func TestRetireStopsOwnLiveServeAndNothingElse(t *testing.T) {
	dataDir := t.TempDir()
	f := newRRFixtureIn(t, dataDir, store.State{})
	mroot := f.mroot

	// A: our legacy serve — own child, own process group, own loopback
	// listener on port 0, nothing registered with any portal.
	dirA := filepath.Join(mroot, "rxalive")
	_, pidA, portA := spawnRRServe(t, dirA)
	seedManagedDir(t, dirA, pidA, portA, map[string]string{
		"home/sessions/a.json": "LEGACY-SESSION-BYTES",
		"notes.txt":            "UNKNOWN-FILE-BYTES",
	})

	// B: a live process of another kind, outside the reasonix root.
	dirB := filepath.Join(dataDir, "opencode-web", "ocB")
	_, pidB, portB := spawnRRServe(t, dirB)
	seedManagedDir(t, dirB, pidB, portB, nil)

	// C: a legacy record whose serve is already dead — stale, never a target.
	dirC := filepath.Join(mroot, "rxdead")
	childC, pidC, portC := spawnRRServe(t, dirC)
	if err := childC.Process.Kill(); err != nil {
		t.Fatalf("stop fixture C: %v", err)
	}
	if err := waitPIDGone(pidC, 8*time.Second); err != nil {
		t.Fatalf("fixture C must exit: %v", err)
	}
	seedManagedDir(t, dirC, pidC, portC, nil)
	if rrDial(portC) {
		t.Fatalf("fixture C's port must be closed")
	}

	if err := (store.FileStore{Path: f.statePath}).Save(store.State{
		Worktrees: []store.ManagedWorktree{{ID: "__main__", Name: "repo", Path: "/srv/repo"}},
		Instances: []store.ManagedInstance{
			{ID: "rxalive", Kind: store.KindReasonix, Status: "running", PID: pidA},
			{ID: "rxdead", Kind: store.KindReasonix, Status: "running", PID: pidC},
			{ID: "ocB", Kind: "opencode-web", Status: "running", PID: pidB},
			{ID: "pty1", Kind: store.KindPTY, Status: "running", PID: pidB},
			{ID: "oldpty", Kind: "", Status: "stopped", PID: pidB},
		},
		TabOrder: map[string][]string{"__main__": {"rxalive", "ocB", "rxdead", "pty1", "oldpty"}},
		Version:  3,
	}); err != nil {
		t.Fatalf("seed state: %v", err)
	}
	before := f.stateBytes(t)

	rep, err := retireLegacyReasonixInstances(f.realEnv())
	if err != nil {
		t.Fatalf("retire: %v", err)
	}
	if rep.LegacyFound != 2 || rep.Retired != 2 || rep.ServesStopped != 1 || rep.StaleRecords != 1 || rep.DirsRetained != 1 || rep.Backup != "created" {
		t.Fatalf("unexpected report: %+v", rep)
	}

	// A stopped: the pid is gone, and its port no longer accepts connections.
	if err := waitPIDGone(pidA, 8*time.Second); err != nil {
		t.Fatalf("legacy serve A was not stopped: %v", err)
	}
	if rrDial(portA) {
		t.Fatalf("A's port must be closed after the bounded stop")
	}
	// B untouched: still serving, its files intact.
	if !rrDial(portB) {
		t.Fatalf("fixture B must still be serving — other kinds are not ours to stop")
	}
	if got := string(readFileBytes(t, filepath.Join(dirB, "pid"))); got != strconv.Itoa(pidB)+"\n" {
		t.Fatalf("another kind's pid file must be untouched")
	}

	after := f.stateBytes(t)
	if string(after) == string(before) {
		t.Fatalf("state.json must have been rewritten")
	}
	bak := readFileBytes(t, f.bakPath)
	if string(bak) != string(before) {
		t.Fatalf("backup must hold the pre-change bytes exactly")
	}
	if fi, err := os.Lstat(f.bakPath); err != nil || !fi.Mode().IsRegular() || fi.Mode().Perm() != 0o600 {
		t.Fatalf("backup type/mode: %v %v", fi.Mode(), err)
	}

	// A: allowlisted files deleted, unknown content preserved verbatim, the
	// non-empty directory kept.
	for _, n := range reasonixLegacyManagedFiles {
		mustNotExist(t, filepath.Join(dirA, n))
	}
	if got := string(readFileBytes(t, filepath.Join(dirA, "home", "sessions", "a.json"))); got != "LEGACY-SESSION-BYTES" {
		t.Fatalf("home/sessions bytes changed")
	}
	if got := string(readFileBytes(t, filepath.Join(dirA, "notes.txt"))); got != "UNKNOWN-FILE-BYTES" {
		t.Fatalf("unknown file bytes changed")
	}
	// C: stale record — no signal, allowlisted files cleaned, emptied dir
	// removed.
	for _, n := range reasonixLegacyManagedFiles {
		mustNotExist(t, filepath.Join(dirC, n))
	}
	mustNotExist(t, dirC)
	if _, err := os.Lstat(filepath.Join(dataDir, "opencode-web")); err != nil {
		t.Fatalf("another kind's data directory must exist: %v", err)
	}

	if !strings.Contains(f.logBuf.String(), "instance rxalive management directory retained unknown_entries=2") {
		t.Fatalf("retained warning: %q", f.logBuf.String())
	}
	if strings.Contains(f.logBuf.String(), "rxdead") {
		t.Fatalf("a fully cleaned stale directory needs no warning: %q", f.logBuf.String())
	}
	rrAssertLogClean(t, f.logBuf.String(), dataDir)

	// Teardown of our own fixture B, which retirement never touched.
	if err := syscall.Kill(-pidB, syscall.SIGKILL); err != nil {
		t.Fatalf("fixture B teardown: %v", err)
	}
	if err := waitPIDGone(pidB, 8*time.Second); err != nil {
		t.Fatalf("fixture B must be reaped: %v", err)
	}
}

// ---------------------------------------------------------------------------
// Row: stale / illegal pid+port — not one signal
// ---------------------------------------------------------------------------

func TestRetireStaleAndIllegalPidPortSendNoSignals(t *testing.T) {
	cases := []struct {
		name  string
		pid   string // pid file content ("" = no file)
		port  string // port file content ("" = no file)
		alive bool   // the parsed pid is alive in the seam
		dial  bool   // the parsed port accepts a connection in the seam
	}{
		{"missing pid file", "", "127.0.0.1:4321", true, true},
		{"unparseable pid", "not-a-pid\n", "127.0.0.1:4321", true, true},
		{"zero pid", "0\n", "127.0.0.1:4321", true, true},
		{"negative pid", "-4242\n", "127.0.0.1:4321", true, true},
		{"pid with junk suffix", "4242 serve\n", "127.0.0.1:4321", true, true},
		{"dead pid", "4242\n", "127.0.0.1:4321", false, true},
		{"missing port file", "4242\n", "", true, true},
		{"whitespace port file", "4242\n", "   \n", true, true},
		{"bare number port file", "4242\n", "4321", true, true},
		{"zero port", "4242\n", "127.0.0.1:0", true, true},
		{"port above 65535", "4242\n", "127.0.0.1:70000", true, true},
		{"non numeric port", "4242\n", "127.0.0.1:httpx", true, true},
		{"port not dialable", "4242\n", "127.0.0.1:4321", true, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			f := newRRFixture(t, buildMixedState())
			if tc.alive {
				f.procs.alivePids[4242] = true
			}
			if tc.dial {
				f.procs.dialPorts[4321] = true
			}
			dir := f.legacyDir(rrLegacyID)
			if tc.pid != "" {
				writeFile(t, filepath.Join(dir, "pid"), 0o600, tc.pid)
			}
			if tc.port != "" {
				writeFile(t, filepath.Join(dir, "port"), 0o600, tc.port)
			}
			before := f.stateBytes(t)

			rep, err := retireLegacyReasonixInstances(f.env())
			if err != nil {
				t.Fatalf("retire: %v", err)
			}
			if rep.ServesStopped != 0 || rep.StaleRecords != 1 || rep.Retired != 1 || !rep.StateSaved {
				t.Fatalf("stale record report: %+v", rep)
			}
			if got := f.procs.sent(); len(got) != 0 {
				t.Fatalf("a stale record must not be signalled: %v", got)
			}
			mustNotExist(t, filepath.Join(dir, "pid"))
			mustNotExist(t, filepath.Join(dir, "port"))
			if string(f.stateBytes(t)) == string(before) {
				t.Fatalf("state must be rewritten even for a stale record")
			}
			f.assertNoLegacyRecords(t)
			if string(readFileBytes(t, f.bakPath)) != string(before) {
				t.Fatalf("the backup must hold the pre-change bytes, proving it preceded the save")
			}
		})
	}
}

// ---------------------------------------------------------------------------
// Row: traversal-shaped ids — error, and not one directory is deleted
// ---------------------------------------------------------------------------

func TestRetireRejectsUnsafeIDsAndTouchesNothing(t *testing.T) {
	badIDs := []string{
		"", ".", "..", "../../etc", "rx/../..", "a/b", "/abs/rx", `rx\..`,
		"rx\nFORGED", "rx\x00", " rx ", "rx ", " rx",
	}
	for _, bad := range badIDs {
		t.Run(fmt.Sprintf("id=%q", bad), func(t *testing.T) {
			f := newRRFixture(t, store.State{
				Worktrees: []store.ManagedWorktree{{ID: "__main__"}},
				Instances: []store.ManagedInstance{
					{ID: bad, Kind: store.KindReasonix, Status: "running", PID: 4242},
					{ID: "pty1", Kind: store.KindPTY, Status: "running"},
				},
				TabOrder: map[string][]string{"__main__": {bad, "pty1"}},
				Version:  5,
			})
			f.procs.live(4242, 4321)
			sentinelDir := filepath.Join(f.mroot, "keepdir")
			writeFile(t, filepath.Join(sentinelDir, "keep.txt"), 0o600, "KEEP")
			writeFile(t, filepath.Join(f.mroot, "keeproot.txt"), 0o600, "KEEP")
			before := f.stateBytes(t)

			_, err := retireLegacyReasonixInstances(f.env())
			if err == nil {
				t.Fatalf("unsafe id %q must return an error", bad)
			}
			if reasonixRetirementCategory(err) != reasonixFailUnsafeID {
				t.Fatalf("category: got %q want %q (err=%v)", reasonixRetirementCategory(err), reasonixFailUnsafeID, err)
			}
			// Nothing deleted, nothing written, no backup, no signal.
			if got := string(readFileBytes(t, filepath.Join(sentinelDir, "keep.txt"))); got != "KEEP" {
				t.Fatalf("sentinel file deleted")
			}
			if got := string(readFileBytes(t, filepath.Join(f.mroot, "keeproot.txt"))); got != "KEEP" {
				t.Fatalf("root sentinel deleted")
			}
			if _, err := os.Lstat(f.mroot); err != nil {
				t.Fatalf("managed root must survive: %v", err)
			}
			if string(f.stateBytes(t)) != string(before) {
				t.Fatalf("state must be untouched on an unsafe id")
			}
			mustNotExist(t, f.bakPath)
			if got := f.procs.sent(); len(got) != 0 {
				t.Fatalf("no signal before validation: %v", got)
			}
			if f.ev.index("save") >= 0 {
				t.Fatalf("no save on an unsafe id: %v", f.ev.order())
			}
		})
	}
}

func TestLegacyReasonixManagedDirSegmentValidation(t *testing.T) {
	root := filepath.Join(string(filepath.Separator), "srv", "data", reasonixLegacyDirName)
	good := []string{"a", "rx-1", "rx_legacy.2", "x"}
	for _, id := range good {
		dir, err := legacyReasonixManagedDir(root, id)
		if err != nil {
			t.Fatalf("id %q must be accepted: %v", id, err)
		}
		if dir != filepath.Join(root, id) {
			t.Fatalf("id %q resolved to %q", id, dir)
		}
	}
	bad := []string{"", ".", "..", "a/b", "a\\b", "/a", "./a", "../a", "a/", "a\nb", " "}
	for _, id := range bad {
		if dir, err := legacyReasonixManagedDir(root, id); err == nil {
			t.Fatalf("id %q must be rejected (resolved to %q)", id, dir)
		}
	}
}

// ---------------------------------------------------------------------------
// Rows: backup — reused verbatim, idempotent rerun; abnormal or failed
// creation aborts BEFORE any signal or record deletion
// ---------------------------------------------------------------------------

func TestRetireReusesExistingBackupAndRerunsIdempotently(t *testing.T) {
	f := newRRFixture(t, buildMixedState())
	f.procs.live(4002, 4321)
	seedManagedDir(t, f.legacyDir(rrLegacyID), 4002, 4321, map[string]string{"home/session.json": "KEEP-ME"})
	preexisting := readFileBytes(t, f.statePath)
	writeFile(t, f.bakPath, 0o600, string(preexisting))

	rep, err := retireLegacyReasonixInstances(f.env())
	if err != nil {
		t.Fatalf("retire: %v", err)
	}
	if rep.Backup != "reused" || !rep.StateSaved || rep.ServesStopped != 1 || rep.Retired != 1 {
		t.Fatalf("unexpected report: %+v", rep)
	}
	if got := string(readFileBytes(t, f.bakPath)); string(got) != string(preexisting) {
		t.Fatalf("existing backup must be byte-identical")
	}
	afterRun1 := f.stateBytes(t)

	// Rerun: no legacy record left → no write, no second backup, no signal.
	rep2, err := retireLegacyReasonixInstances(f.env())
	if err != nil {
		t.Fatalf("retire rerun: %v", err)
	}
	if rep2.LegacyFound != 0 || rep2.Retired != 0 || rep2.StateSaved {
		t.Fatalf("rerun must be a no-op: %+v", rep2)
	}
	if got := f.stateBytes(t); string(got) != string(afterRun1) {
		t.Fatalf("rerun must not rewrite state")
	}
	if got := string(readFileBytes(t, f.bakPath)); string(got) != string(preexisting) {
		t.Fatalf("rerun must not touch the backup")
	}
	if got := f.procs.sent(); len(got) != 1 {
		t.Fatalf("exactly one signal across both runs: %v", got)
	}
	if got := string(readFileBytes(t, filepath.Join(f.legacyDir(rrLegacyID), "home", "session.json"))); got != "KEEP-ME" {
		t.Fatalf("retained dir content changed")
	}
	// The pre-change records live in that backup, untouched.
	bakState := store.State{}
	if err := json.Unmarshal(readFileBytes(t, f.bakPath), &bakState); err != nil {
		t.Fatalf("decode backup: %v", err)
	}
	var legacyInBackup int
	for _, it := range bakState.Instances {
		if it.Kind == store.KindReasonix {
			legacyInBackup++
		}
	}
	if legacyInBackup != 1 || bakState.Version != 17 {
		t.Fatalf("backup must hold the pre-change records: legacy=%d version=%d", legacyInBackup, bakState.Version)
	}
}

func TestRetireOverOpenExistingBackupIsReusedNotRewritten(t *testing.T) {
	f := newRRFixture(t, buildMixedState())
	f.procs.live(4002, 4321)
	seedManagedDir(t, f.legacyDir(rrLegacyID), 4002, 4321, nil)
	content := "WORLD-READABLE-BACKUP-BYTES"
	writeFile(t, f.bakPath, 0o644, content)

	rep, err := retireLegacyReasonixInstances(f.env())
	if err != nil {
		t.Fatalf("retire: %v", err)
	}
	if rep.Backup != "reused-open-perms" || !rep.StateSaved {
		t.Fatalf("backup label/report: %+v", rep)
	}
	if got := string(readFileBytes(t, f.bakPath)); got != content {
		t.Fatalf("an over-open backup is still kept byte-for-byte")
	}
}

func TestRetireBackupAbnormalTypeAbortsBeforeAnything(t *testing.T) {
	shapes := []struct {
		name string
		make func(t *testing.T, path string)
	}{
		{"directory where the backup belongs", func(t *testing.T, path string) {
			if err := os.MkdirAll(path, 0o700); err != nil {
				t.Fatal(err)
			}
		}},
		{"symlink where the backup belongs", func(t *testing.T, path string) {
			writeFile(t, filepath.Join(filepath.Dir(path), "elsewhere.txt"), 0o600, "ELSEWHERE")
			if err := os.Symlink(filepath.Join(filepath.Dir(path), "elsewhere.txt"), path); err != nil {
				t.Fatal(err)
			}
		}},
	}
	for _, shape := range shapes {
		t.Run(shape.name, func(t *testing.T) {
			f := newRRFixture(t, buildMixedState())
			f.procs.live(4002, 4321)
			seedManagedDir(t, f.legacyDir(rrLegacyID), 4002, 4321, nil)
			shape.make(t, f.bakPath)
			before := f.stateBytes(t)

			rep, err := retireLegacyReasonixInstances(f.env())
			if err == nil {
				t.Fatalf("must abort when the backup path is not a regular file")
			}
			if reasonixRetirementCategory(err) != reasonixFailBackup {
				t.Fatalf("category: got %q want %q", reasonixRetirementCategory(err), reasonixFailBackup)
			}
			if rep.StateSaved || rep.ServesStopped != 0 || rep.Retired != 0 {
				t.Fatalf("nothing stopped, nothing retired: %+v", rep)
			}
			if got := f.procs.sent(); len(got) != 0 {
				t.Fatalf("no signal before the backup is ready: %v", got)
			}
			if f.ev.index("save") >= 0 {
				t.Fatalf("no state save after a backup abort: %v", f.ev.order())
			}
			if string(f.stateBytes(t)) != string(before) {
				t.Fatalf("state must be untouched")
			}
			for _, n := range reasonixLegacyManagedFiles {
				if _, err := os.Lstat(filepath.Join(f.legacyDir(rrLegacyID), n)); err != nil {
					t.Fatalf("%s must still exist: %v", n, err)
				}
			}
		})
	}
}

func TestRetireBackupCreateFailureAbortsBeforeAnything(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("root ignores directory modes; a read-only dir cannot fail to create")
	}
	f := newRRFixture(t, buildMixedState())
	f.procs.live(4002, 4321)
	seedManagedDir(t, f.legacyDir(rrLegacyID), 4002, 4321, nil)
	before := f.stateBytes(t)
	if err := os.Chmod(f.dataDir, 0o500); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(f.dataDir, 0o700) })

	rep, err := retireLegacyReasonixInstances(f.env())
	if err == nil {
		t.Fatalf("must abort when the backup cannot be created")
	}
	if reasonixRetirementCategory(err) != reasonixFailBackup {
		t.Fatalf("category: got %q want %q (err=%v)", reasonixRetirementCategory(err), reasonixFailBackup, err)
	}
	if rep.StateSaved || rep.ServesStopped != 0 || rep.Retired != 0 {
		t.Fatalf("nothing stopped, nothing retired: %+v", rep)
	}
	if got := f.procs.sent(); len(got) != 0 {
		t.Fatalf("no signal: %v", got)
	}
	if string(f.stateBytes(t)) != string(before) {
		t.Fatalf("state untouched")
	}
	for _, n := range reasonixLegacyManagedFiles {
		if _, err := os.Lstat(filepath.Join(f.legacyDir(rrLegacyID), n)); err != nil {
			t.Fatalf("%s must survive the abort: %v", n, err)
		}
	}
	// With the directory writable again the same run completes: the abort
	// left the retry path intact.
	if err := os.Chmod(f.dataDir, 0o700); err != nil {
		t.Fatal(err)
	}
	rep2, err := retireLegacyReasonixInstances(f.env())
	if err != nil {
		t.Fatalf("retry retire: %v", err)
	}
	if rep2.Backup != "created" || !rep2.StateSaved || rep2.Retired != 1 || rep2.ServesStopped != 1 {
		t.Fatalf("retry report: %+v", rep2)
	}
}

// ---------------------------------------------------------------------------
// Row: state save failure — error surfaced, backup kept, no false "done",
// and the next startup retries the cleanup safely
// ---------------------------------------------------------------------------

func TestRetireSaveFailureKeepsBackupAndRetriesNextStart(t *testing.T) {
	f := newRRFixture(t, buildMixedState())
	f.procs.live(4002, 4321)
	seedManagedDir(t, f.legacyDir(rrLegacyID), 4002, 4321, nil)
	before := f.stateBytes(t)

	saveErr := errors.New("injected save failure")
	st := f.store()
	st.failSave = saveErr
	rep, err := retireLegacyReasonixInstances(f.envWith(f.procs, st))
	if err == nil {
		t.Fatalf("save failure must surface")
	}
	if reasonixRetirementCategory(err) != reasonixFailStateSave {
		t.Fatalf("category: got %q want %q", reasonixRetirementCategory(err), reasonixFailStateSave)
	}
	if !errors.Is(err, saveErr) {
		t.Fatalf("the inner error must unwrap: %v", err)
	}
	if rep.StateSaved {
		t.Fatalf("StateSaved must be false")
	}
	if string(f.stateBytes(t)) != string(before) {
		t.Fatalf("state must stay unwritten so the next startup retries")
	}
	firstBackup := readFileBytes(t, f.bakPath)
	if string(firstBackup) != string(before) {
		t.Fatalf("backup must be retained with the pre-change bytes")
	}

	// The Server-facing wrapper reports an abort, never a completion count.
	buf := &syncBuffer{}
	s := &Server{logger: log.New(buf, "", 0), dataDir: f.dataDir, store: store.FileStore{Path: f.statePath}}
	// The report half of the Start hook, over the same report the failing
	// save produced: it names the stage and never claims completion.
	s.reportReasonixRetirement(rep, err)
	if !strings.Contains(buf.String(), "aborted category=state-save") {
		t.Fatalf("wrapper must report the abort: %q", buf.String())
	}
	if !strings.HasPrefix(strings.TrimSpace(buf.String()), "[reasonix-retire] aborted") {
		t.Fatalf("the abort must be the leading statement: %q", buf.String())
	}
	if strings.Contains(buf.String(), "backup=") {
		t.Fatalf("an aborted run must not report a completed backup/save pair: %q", buf.String())
	}
	rrAssertLogClean(t, buf.String(), f.dataDir)

	// The real hook over a read-only data directory aborts at the save too,
	// then completes once the directory is writable again — the retry the
	// contract promises for the next startup.
	if os.Geteuid() != 0 {
		if err := os.Chmod(f.dataDir, 0o500); err != nil {
			t.Fatal(err)
		}
		buf2 := &syncBuffer{}
		s.logger = log.New(buf2, "", 0)
		s.retireLegacyReasonixOnStartup()
		if !strings.Contains(buf2.String(), "aborted category=state-save") {
			t.Fatalf("the real hook must report the save abort: %q", buf2.String())
		}
		if string(f.stateBytes(t)) != string(before) {
			t.Fatalf("state must survive the aborted hook")
		}
		if err := os.Chmod(f.dataDir, 0o700); err != nil {
			t.Fatal(err)
		}
		buf3 := &syncBuffer{}
		s.logger = log.New(buf3, "", 0)
		s.retireLegacyReasonixOnStartup()
		if !strings.Contains(buf3.String(), "retired=1") || strings.Contains(buf3.String(), "aborted") {
			t.Fatalf("the retry must complete: %q", buf3.String())
		}
		f.assertNoLegacyRecords(t)
		if string(readFileBytes(t, f.bakPath)) != string(before) {
			t.Fatalf("the backup must still hold the original pre-change bytes")
		}
	}
}

func TestRetireNextStartupRetriesAfterSaveFailure(t *testing.T) {
	f := newRRFixture(t, buildMixedState())
	f.procs.live(4002, 4321)
	seedManagedDir(t, f.legacyDir(rrLegacyID), 4002, 4321, nil)
	before := f.stateBytes(t)

	st := f.store()
	st.failSave = errors.New("injected save failure")
	if _, err := retireLegacyReasonixInstances(f.envWith(f.procs, st)); err == nil {
		t.Fatalf("first run must fail at the save")
	}
	// The management files are already gone, so the retry sends no signal —
	// the record is still in state, and it retires as stale.
	rep2, err := retireLegacyReasonixInstances(f.env())
	if err != nil {
		t.Fatalf("retry retire: %v", err)
	}
	if rep2.LegacyFound != 1 || rep2.Retired != 1 || rep2.ServesStopped != 0 || rep2.StaleRecords != 1 || rep2.Backup != "reused" {
		t.Fatalf("retry report: %+v", rep2)
	}
	if got := f.procs.sent(); len(got) != 1 {
		t.Fatalf("exactly one signal in total (the first run's): %v", got)
	}
	f.assertNoLegacyRecords(t)
	if string(readFileBytes(t, f.bakPath)) != string(before) {
		t.Fatalf("backup still holds the original pre-change bytes")
	}
}

func TestRetireVersionConflictAbortsCommit(t *testing.T) {
	f := newRRFixture(t, buildMixedState())
	f.procs.live(4002, 4321)
	seedManagedDir(t, f.legacyDir(rrLegacyID), 4002, 4321, nil)

	st := f.store()
	st.beforeSave = func() {
		// Persist a version bump behind our back: the strict check must bite.
		fs := store.FileStore{Path: f.statePath}
		cur, err := fs.Load()
		if err != nil {
			t.Fatalf("load for bump: %v", err)
		}
		if err := fs.SaveWithVersion(cur, cur.Version); err != nil {
			t.Fatalf("bump version: %v", err)
		}
	}
	_, err := retireLegacyReasonixInstances(f.envWith(f.procs, st))
	if err == nil {
		t.Fatalf("a version conflict must abort the commit")
	}
	if reasonixRetirementCategory(err) != reasonixFailStateSave {
		t.Fatalf("category: got %q want %q (err=%v)", reasonixRetirementCategory(err), reasonixFailStateSave, err)
	}
	if !errors.Is(err, store.ErrVersionConflict) {
		t.Fatalf("must unwrap to store.ErrVersionConflict: %v", err)
	}
	if got := f.loadState(t).Version; got != 18 {
		t.Fatalf("state version must be the conflicter's, not ours: %d", got)
	}
	var legacy int
	for _, it := range f.loadState(t).Instances {
		if it.Kind == store.KindReasonix {
			legacy++
		}
	}
	if legacy != 1 {
		t.Fatalf("the legacy record must still be in state after a conflict: %d", legacy)
	}
	if _, err := os.Lstat(f.bakPath); err != nil {
		t.Fatalf("backup must be retained on a conflict: %v", err)
	}
}

// ---------------------------------------------------------------------------
// Row: the pid is CONFIRMED gone before the commit — the step the retired
// Driver.Stop never performed
// ---------------------------------------------------------------------------

func TestRetireAbortsWhenPidIsNotConfirmedExited(t *testing.T) {
	f := newRRFixture(t, buildMixedState())
	f.procs.live(4002, 4321)
	f.procs.stubborn[4002] = true // survives TERM and KILL → confirm fails
	seedManagedDir(t, f.legacyDir(rrLegacyID), 4002, 4321, nil)
	before := f.stateBytes(t)

	rep, err := retireLegacyReasonixInstances(f.env())
	if err == nil {
		t.Fatalf("an unconfirmed exit must abort")
	}
	if reasonixRetirementCategory(err) != reasonixFailStop {
		t.Fatalf("category: got %q want %q", reasonixRetirementCategory(err), reasonixFailStop)
	}
	if rep.StateSaved || rep.Retired != 0 || rep.ServesStopped != 0 {
		t.Fatalf("nothing committed or retired: %+v", rep)
	}
	if string(f.stateBytes(t)) != string(before) {
		t.Fatalf("the state file must be unwritten — no hidden record")
	}
	want := fmt.Sprintf("-4002/%d,-4002/%d", rrSIGTERMv, rrSIGKILLv)
	if got := strings.Join(f.procs.sent(), ","); got != want {
		t.Fatalf("signals: got %q want %q", got, want)
	}
	for _, n := range []string{"pid", "port"} {
		if _, err := os.Lstat(filepath.Join(f.legacyDir(rrLegacyID), n)); err != nil {
			t.Fatalf("%s must survive the abort so the next start can find it: %v", n, err)
		}
	}
	if _, err := os.Lstat(f.bakPath); err != nil {
		t.Fatalf("backup retained: %v", err)
	}
}

func TestRetireFallsBackToSinglePidWhenGroupSignalFails(t *testing.T) {
	f := newRRFixture(t, buildMixedState())
	f.procs.live(4002, 4321)
	seedManagedDir(t, f.legacyDir(rrLegacyID), 4002, 4321, nil)
	f.procs.groupFails = true // ESRCH/EPERM on the group: the single pid is used

	rep, err := retireLegacyReasonixInstances(f.env())
	if err != nil {
		t.Fatalf("retire: %v", err)
	}
	if rep.ServesStopped != 1 || !rep.StateSaved {
		t.Fatalf("report: %+v", rep)
	}
	want := fmt.Sprintf("-4002/%d,4002/%d", rrSIGTERMv, rrSIGTERMv)
	if got := strings.Join(f.procs.sent(), ","); got != want {
		t.Fatalf("signals: got %q want %q", got, want)
	}
}

// ---------------------------------------------------------------------------
// Row: unknown entries and the emptied-directory rule
// ---------------------------------------------------------------------------

func TestRetireKeepsUnknownEntriesAndNeverRemoveAll(t *testing.T) {
	f := newRRFixture(t, buildMixedState())
	f.procs.live(4002, 4321)
	dir := f.legacyDir(rrLegacyID)
	// A known name occupied by a DIRECTORY is unknown content: kept, never
	// recursed into.
	writeFile(t, filepath.Join(dir, "pid", "inner.txt"), 0o600, "INNER")
	writeFile(t, filepath.Join(dir, "home", "sessions", "a.json"), 0o600, "SESSION-A")
	writeFile(t, filepath.Join(dir, "custom-home", "cfg.toml"), 0o600, "CFG")
	writeFile(t, filepath.Join(dir, "stray.bin"), 0o600, "STRAY")
	writeFile(t, filepath.Join(dir, "token"), 0o600, "tok\n")
	writeFile(t, filepath.Join(dir, "port"), 0o600, "127.0.0.1:4321\n")
	writeFile(t, filepath.Join(dir, "serve.log"), 0o600, "log\n")
	writeFile(t, filepath.Join(dir, "serve.log.1"), 0o600, "log1\n")
	if err := os.Symlink("/etc/hostname", filepath.Join(dir, "link")); err != nil {
		t.Fatal(err)
	}

	rep, err := retireLegacyReasonixInstances(f.env())
	if err != nil {
		t.Fatalf("retire: %v", err)
	}
	// "pid" is a directory here, so the record is unaddressable: no signal,
	// retired as stale, and its contents preserved.
	if rep.DirsRetained != 1 || rep.Retired != 1 || rep.ServesStopped != 0 || rep.StaleRecords != 1 {
		t.Fatalf("report: %+v", rep)
	}
	for _, rel := range []string{"home/sessions/a.json", "custom-home/cfg.toml", "stray.bin", "pid/inner.txt", "link"} {
		if _, err := os.Lstat(filepath.Join(dir, filepath.FromSlash(rel))); err != nil {
			t.Fatalf("%s must be preserved: %v", rel, err)
		}
	}
	if got := string(readFileBytes(t, filepath.Join(dir, "home", "sessions", "a.json"))); got != "SESSION-A" {
		t.Fatalf("session bytes changed")
	}
	if got := string(readFileBytes(t, filepath.Join(dir, "pid", "inner.txt"))); got != "INNER" {
		t.Fatalf("bytes under a known-name directory changed")
	}
	for _, n := range []string{"token", "port", "serve.log", "serve.log.1"} {
		mustNotExist(t, filepath.Join(dir, n))
	}
	// pid + home + custom-home + stray.bin + link = 5 unknown entries.
	if !strings.Contains(f.logBuf.String(), "retained unknown_entries=5") {
		t.Fatalf("retained warning missing: %q", f.logBuf.String())
	}
	rrAssertLogClean(t, f.logBuf.String(), f.dataDir)
}

func TestRetireRemovesOnlyEmptiedDirectoriesNeverTheRoot(t *testing.T) {
	f := newRRFixture(t, buildMixedState())
	f.procs.live(4002, 4321)
	seedManagedDir(t, f.legacyDir(rrLegacyID), 4002, 4321, nil)
	// A second legacy record whose directory never existed.
	st := buildMixedState()
	st.Instances = append(st.Instances, store.ManagedInstance{ID: "rxabsent", Kind: store.KindReasonix, Status: "stopped"})
	st.TabOrder["__main__"] = append(st.TabOrder["__main__"], "rxabsent")
	if err := (store.FileStore{Path: f.statePath}).SaveWithVersion(st, st.Version); err != nil {
		t.Fatal(err)
	}
	// A directory of another kind inside the same data dir must survive.
	writeFile(t, filepath.Join(f.dataDir, "pty", "pty1", "log.txt"), 0o600, "PTY")

	rep, err := retireLegacyReasonixInstances(f.env())
	if err != nil {
		t.Fatalf("retire: %v", err)
	}
	if rep.LegacyFound != 2 || rep.Retired != 2 || rep.ServesStopped != 1 || rep.StaleRecords != 1 || rep.DirsRetained != 0 {
		t.Fatalf("report: %+v", rep)
	}
	mustNotExist(t, f.legacyDir(rrLegacyID))
	mustNotExist(t, f.legacyDir("rxabsent"))
	if _, err := os.Lstat(f.mroot); err != nil {
		t.Fatalf("the managed root is not ours to remove: %v", err)
	}
	if got := string(readFileBytes(t, filepath.Join(f.dataDir, "pty", "pty1", "log.txt"))); got != "PTY" {
		t.Fatalf("another kind's files changed")
	}
}

// ---------------------------------------------------------------------------
// Row: log hygiene — counts and categories only
// ---------------------------------------------------------------------------

func TestRetireLogsCarryNoPathsTokensOrCommandLines(t *testing.T) {
	f := newRRFixture(t, buildMixedState())
	f.procs.live(4002, 4321)
	seedManagedDir(t, f.legacyDir(rrLegacyID), 4002, 4321, map[string]string{
		"home/session.json": "SESSION-SECRET",
		"cookie.txt":        "cookie-bytes",
		"auth.json":         "auth-bytes",
	})
	writeFile(t, filepath.Join(f.legacyDir(rrLegacyID), "serve.log"), 0o600,
		"reasonix serve --auth token sk-REALTOKEN --addr 127.0.0.1:4321 /rx/rxlegacy0001/\n")

	if _, err := retireLegacyReasonixInstances(f.env()); err != nil {
		t.Fatalf("retire: %v", err)
	}
	logText := f.logBuf.String()
	if logText == "" {
		t.Fatalf("expected the retained warning")
	}
	for _, forbidden := range []string{"sk-", "bearer", "token", "cookie", "auth", "home", "secret", "/rx/", "serve.log", "127.0.0.1", "--auth", "command"} {
		if strings.Contains(strings.ToLower(logText), forbidden) {
			t.Fatalf("log leaked %q: %q", forbidden, logText)
		}
	}
	rrAssertLogClean(t, logText, f.dataDir)
	if !strings.Contains(logText, "retained unknown_entries=3") {
		t.Fatalf("expected the retained count: %q", logText)
	}
	// The summary line comes from the Start wrapper; cover it over the same
	// kind of sensitive content.
	f2 := newRRFixture(t, buildMixedState())
	f2.procs.live(4002, 4321)
	seedManagedDir(t, f2.legacyDir(rrLegacyID), 4002, 4321, map[string]string{"home/session.json": "S"})
	buf := &syncBuffer{}
	srv := &Server{logger: log.New(buf, "", 0), dataDir: f2.dataDir, store: store.FileStore{Path: f2.statePath}}
	srv.reportReasonixRetirement(retireLegacyReasonixInstances(f2.env()))
	if !strings.Contains(buf.String(), "retired=1 serves_stopped=1 stale=0 dirs_retained=1 backup=created") {
		t.Fatalf("expected counts only: %q", buf.String())
	}
	rrAssertLogClean(t, buf.String(), f2.dataDir)
}

// ---------------------------------------------------------------------------
// The Start hook itself: counts through the Server wrapper, silence after
// ---------------------------------------------------------------------------

func TestServerStartHookRetiresOnceThenStaysSilent(t *testing.T) {
	f := newRRFixture(t, buildMixedState())
	f.procs.live(4002, 4321)
	seedManagedDir(t, f.legacyDir(rrLegacyID), 4002, 4321, map[string]string{"home/x": "X"})
	buf := &syncBuffer{}
	s := &Server{logger: log.New(buf, "", 0), dataDir: f.dataDir, store: store.FileStore{Path: f.statePath}}

	s.reportReasonixRetirement(retireLegacyReasonixInstances(f.env()))
	if !strings.Contains(buf.String(), "retired=1 serves_stopped=1 stale=0 dirs_retained=1 backup=created") {
		t.Fatalf("wrapper log: %q", buf.String())
	}
	if got := len(f.procs.sent()); got != 1 {
		t.Fatalf("one signal through the wrapper: %v", f.procs.sent())
	}
	f.assertNoLegacyRecords(t)

	buf2 := &syncBuffer{}
	s.logger = log.New(buf2, "", 0)
	s.reportReasonixRetirement(retireLegacyReasonixInstances(f.env()))
	if buf2.String() != "" {
		t.Fatalf("a clean state must be silent: %q", buf2.String())
	}
	if got := len(f.procs.sent()); got != 1 {
		t.Fatalf("the silent second pass must send nothing: %v", f.procs.sent())
	}
}

// ---------------------------------------------------------------------------
// Real-process fixture: the fake legacy serve
// ---------------------------------------------------------------------------

// spawnRRServe starts OUR OWN child process — a second run of this test
// binary in its own process group — which binds 127.0.0.1:0, writes the pid
// and port files exactly as the retired `reasonix serve` did, and answers TCP
// until a signal ends it. Nothing is registered with any portal.
func spawnRRServe(t *testing.T, dir string) (*exec.Cmd, int, int) {
	t.Helper()
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatalf("mkdir %s: %v", dir, err)
	}
	cmd := exec.Command(os.Args[0], "-test.run=^TestRRFixtureChild$", "-test.timeout=120s")
	cmd.Env = append(os.Environ(), "RR_FIXTURE_MODE=serve", "RR_FIXTURE_DIR="+dir)
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	cmd.Stdout = nil
	cmd.Stderr = nil
	if err := cmd.Start(); err != nil {
		t.Fatalf("start fixture child: %v", err)
	}
	pid := cmd.Process.Pid
	// Reap promptly: a zombie would read as "alive" to kill(pid, 0) and make
	// the confirm step of the bounded stop fail.
	go func() { _ = cmd.Wait() }()
	t.Cleanup(func() { _ = syscall.Kill(-pid, syscall.SIGKILL) })

	port := 0
	deadline := time.Now().Add(15 * time.Second)
	for time.Now().Before(deadline) {
		if b, err := os.ReadFile(filepath.Join(dir, "port")); err == nil {
			if _, ps, perr := net.SplitHostPort(strings.TrimSpace(string(b))); perr == nil {
				if v, aerr := strconv.Atoi(ps); aerr == nil && v > 0 {
					port = v
					break
				}
			}
		}
		if !legacyProcessAlive(pid) {
			t.Fatalf("fixture child %d died before writing its port file", pid)
		}
		time.Sleep(20 * time.Millisecond)
	}
	if port == 0 {
		t.Fatalf("fixture child %d never wrote a usable port file", pid)
	}
	if !rrDial(port) {
		t.Fatalf("fixture child %d is not serving on its recorded port", pid)
	}
	return cmd, pid, port
}

// TestRRFixtureChild is the body of the fixture child process. In a normal
// `go test` run (no marker env) it skips instantly and costs nothing.
func TestRRFixtureChild(t *testing.T) {
	mode := os.Getenv("RR_FIXTURE_MODE")
	if mode == "" {
		t.Skip("fixture child only")
	}
	if mode == "noop" {
		return
	}
	if mode != "serve" {
		t.Fatalf("unknown fixture mode %q", mode)
	}
	dir := os.Getenv("RR_FIXTURE_DIR")
	if dir == "" {
		t.Fatalf("RR_FIXTURE_DIR is required")
	}
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("bind: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "pid"), []byte(strconv.Itoa(os.Getpid())+"\n"), 0o600); err != nil {
		t.Fatalf("write pid file: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "port"), []byte(ln.Addr().String()+"\n"), 0o600); err != nil {
		t.Fatalf("write port file: %v", err)
	}
	// A plain loopback listener: the contract only requires that the recorded
	// port accepts a connection. Default TERM/KILL disposition ends it.
	srv := &http.Server{Handler: http.NotFoundHandler(), ReadHeaderTimeout: 5 * time.Second}
	if err := srv.Serve(ln); err != nil && !errors.Is(err, http.ErrServerClosed) {
		t.Fatalf("serve: %v", err)
	}
}

// ---------------------------------------------------------------------------
// helpers
// ---------------------------------------------------------------------------

func rrDial(port int) bool {
	c, err := net.DialTimeout("tcp", net.JoinHostPort("127.0.0.1", strconv.Itoa(port)), 300*time.Millisecond)
	if err != nil {
		return false
	}
	_ = c.Close()
	return true
}

func waitPIDGone(pid int, timeout time.Duration) error {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if !legacyProcessAlive(pid) {
			return nil
		}
		time.Sleep(50 * time.Millisecond)
	}
	return fmt.Errorf("pid %d still alive", pid)
}

func rrJSON(v any) string {
	b, err := json.Marshal(v)
	if err != nil {
		panic(err)
	}
	return string(b)
}
