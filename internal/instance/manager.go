package instance

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"time"

	"github.com/creack/pty"
	"myworktree/internal/redact"
	"myworktree/internal/store"
	"myworktree/internal/tag"
)

const (
	MainWorktreeID = "__main__"
)

// ErrInstanceNotFound is returned when an instance ID does not match any known instance.
var ErrInstanceNotFound = errors.New("unknown instance id")

type Manager struct {
	DataDir string
	Root    string // git repo root, used for MainWorktreeID resolution
	Store   store.FileStore
	Logger  *log.Logger

	// LogBufferBytes is the per-instance ring buffer cap from config.
	// 0 means "use adaptive sizing".
	LogBufferBytes int64

	// memSampler queries system memory. Defaults to gopsutilMem if nil.
	memSampler MemSampler

	stateMu     sync.Mutex
	mu          sync.Mutex
	running     map[string]*exec.Cmd
	inputs      map[string]io.WriteCloser
	ptys        map[string]*os.File
	subscribers map[string]map[chan string]struct{}
	conns       map[string]string // instance ID -> connection type ("websocket"/"sse"/"")

	// buffers holds the per-instance ring buffer pointers for captured PTY
	// output. Each value is an atomic.Pointer so the hot path in pumpLogs
	// can read the current buffer with a single atomic load (no stateMu
	// acquisition per 1024-byte PTY chunk). Map mutations (insert/delete)
	// still require stateMu.
	buffers map[string]*atomic.Pointer[RingBuffer]

	// totalBufBytes is the sum of all live buffer caps. Atomic so it doesn't
	// contend with stateMu.
	totalBufBytes atomic.Int64

	// memSampleMu guards the 60s memory-sample cache below. The cache is used
	// only by the adaptive-cap path in Start(); the budget check always reads
	// a fresh sample to avoid rejecting requests based on stale data.
	memSampleMu      sync.Mutex
	lastMemSampleAt  time.Time
	lastMemAvailable int64

	connsMu sync.Mutex
}

type StartInput struct {
	WorktreeID string
	Root       string // optional: absolute path, used instead of WorktreeID lookup
	TagID      string
	Command    string // optional if TagID is set; required if TagID is empty
	Name       string
	Kind       string // "pty" (default) | "opencode-web"
}

// ReconcileRunningOnStartup marks stale "running" records as "stopped".
// Process I/O channels are in-memory and cannot be resumed across server restarts.
func (m *Manager) ReconcileRunningOnStartup() (int, error) {
	m.stateMu.Lock()
	defer m.stateMu.Unlock()
	st, err := m.Store.Load()
	if err != nil {
		return 0, err
	}
	changed := 0
	now := time.Now().UTC().Format(time.RFC3339)
	for i := range st.Instances {
		if st.Instances[i].Status != "running" {
			continue
		}
		st.Instances[i].Status = "stopped"
		if strings.TrimSpace(st.Instances[i].StoppedAt) == "" {
			st.Instances[i].StoppedAt = now
		}
		changed++
	}
	if changed == 0 {
		return 0, nil
	}
	if err := m.Store.SaveWithVersion(st, st.Version); err != nil {
		return changed, err
	}
	return changed, nil
}

// PurgeOrphanLogFiles removes every *.log file in DataDir/logs/. After the
// in-memory ring buffer refactor, no live state references these files — they
// are dead artifacts left over from the old per-instance log file. Runs once
// at daemon startup to reclaim disk space.
//
// Returns the number of files removed. A missing logs/ directory is not an
// error (returns 0).
func (m *Manager) PurgeOrphanLogFiles() (int, error) {
	if strings.TrimSpace(m.DataDir) == "" {
		return 0, nil
	}
	logDir := filepath.Join(m.DataDir, "logs")
	entries, err := os.ReadDir(logDir)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return 0, nil
		}
		return 0, err
	}
	removed := 0
	var failed []string
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		if !strings.HasSuffix(e.Name(), ".log") {
			continue
		}
		full := filepath.Join(logDir, e.Name())
		if err := os.Remove(full); err != nil {
			failed = append(failed, fmt.Sprintf("%s: %v", full, err))
			continue
		}
		removed++
	}
	if m.Logger != nil {
		if removed > 0 {
			m.Logger.Printf("purge: removed %d orphan log files from %s", removed, logDir)
		}
		for _, f := range failed {
			m.Logger.Printf("purge: %s", f)
		}
	}
	return removed, nil
}

// memSampleCacheTTL is how long the adaptive-cap path reuses a single
// mem.VirtualMemory() result. The cap is a starting point that's re-evaluated
// on every instance start, so staleness here is harmless.
const memSampleCacheTTL = 60 * time.Second

// sampledAvailable returns a mem.Available result, refreshing at most once
// per memSampleCacheTTL. Used by the adaptive-cap path in Start(). On a cache
// miss it queries the configured sampler (or gopsutilMem as the default) and
// stores the result.
func (m *Manager) sampledAvailable(now time.Time) (int64, error) {
	m.memSampleMu.Lock()
	defer m.memSampleMu.Unlock()
	if !m.lastMemSampleAt.IsZero() && now.Sub(m.lastMemSampleAt) < memSampleCacheTTL {
		return m.lastMemAvailable, nil
	}
	sampler := m.memSampler
	if sampler == nil {
		sampler = gopsutilMem{}
	}
	vm, err := sampler.VirtualMemory()
	if err != nil {
		return 0, err
	}
	avail := vm.Available
	if avail <= 0 {
		avail = vm.Total - vm.Used
	}
	if avail < 0 {
		avail = 0
	}
	m.lastMemAvailable = avail
	m.lastMemSampleAt = now
	return avail, nil
}

func (m *Manager) Start(in StartInput) (store.ManagedInstance, error) {
	m.mu.Lock()
	if m.running == nil {
		m.running = map[string]*exec.Cmd{}
	}
	if m.inputs == nil {
		m.inputs = map[string]io.WriteCloser{}
	}
	if m.ptys == nil {
		m.ptys = map[string]*os.File{}
	}
	if m.subscribers == nil {
		m.subscribers = map[string]map[chan string]struct{}{}
	}
	if m.conns == nil {
		m.conns = map[string]string{}
	}
	m.mu.Unlock()

	if strings.TrimSpace(in.WorktreeID) == "" {
		return store.ManagedInstance{}, errors.New("worktree_id is required")
	}
	if in.Kind == "" {
		in.Kind = "pty"
	}

	st, err := m.Store.Load()
	if err != nil {
		return store.ManagedInstance{}, err
	}

	var wtPath string
	var wtName string

	if in.Root != "" {
		wtPath = in.Root
		wtName = filepath.Base(filepath.Clean(in.Root))
	} else {
		var wt *store.ManagedWorktree
		for i := range st.Worktrees {
			if st.Worktrees[i].ID == in.WorktreeID {
				wt = &st.Worktrees[i]
				break
			}
		}
		if wt == nil {
			return store.ManagedInstance{}, fmt.Errorf("unknown worktree id: %s", in.WorktreeID)
		}
		wtPath = wt.Path
		wtName = wt.Name
	}

	var (
		t              tag.Tag
		command        string
		cwdRel         string
		preStart       string
		env            map[string]string
		effectiveTagID = strings.TrimSpace(in.TagID)
	)

	if effectiveTagID != "" {
		tags, err := m.loadTags()
		if err != nil {
			return store.ManagedInstance{}, err
		}
		var ok bool
		t, ok = tags[effectiveTagID]
		if !ok {
			if in.Kind == "opencode-web" {
				// Use defaults; tag is a reference label only. Command/env
				// are hardcoded in startOpencodeWeb regardless of tag content.
				command = ""
				env = map[string]string{}
			} else {
				return store.ManagedInstance{}, fmt.Errorf("unknown tag id: %s", effectiveTagID)
			}
		} else {
			if in.Kind != "opencode-web" && strings.TrimSpace(t.Command) == "" {
				return store.ManagedInstance{}, errors.New("tag command is required")
			}
			command = t.Command
			cwdRel = t.Cwd
			preStart = t.PreStart
			if t.Env != nil {
				env = t.Env
			} else {
				env = map[string]string{}
			}
		}
	} else {
		effectiveTagID = "adhoc"
		command = strings.TrimSpace(in.Command)
		if command == "" {
			effectiveTagID = "idle"
			command = ""
		}
		cwdRel = ""
		preStart = ""
		env = map[string]string{}
	}

	id := shortID()
	instName := strings.TrimSpace(in.Name)
	if instName == "" {
		instName = effectiveTagID
	}

	cwd := wtPath
	if strings.TrimSpace(cwdRel) != "" && cwdRel != "." {
		cwd = filepath.Join(wtPath, cwdRel)
	}

	// Resolve buffer cap with budget enforcement. Done BEFORE exec.Command /
	// pty.Start so a budget-exceeded error short-circuits the heavy work
	// (process spawn, PTY allocation) and leaves no resources to clean up.
	//
	// The cap path uses a 60s-cached mem.Available sample (cheap to query
	// repeatedly under batch starts); the budget check inside resolveCap
	// always queries the sampler live so budget decisions never use stale
	// data.
	sampler := m.memSampler
	if sampler == nil {
		sampler = gopsutilMem{}
	}
	var capAvail int64 = -1
	if m.LogBufferBytes <= 0 {
		// Adaptive path: use the cached Available sample. Errors here fall
		// back to live sampling inside resolveCap → adaptiveCapFromAvailable.
		if avail, sErr := m.sampledAvailable(time.Now()); sErr == nil {
			capAvail = avail
		}
	}
	capBytes, err := resolveCapFromAvailable(m.LogBufferBytes, sampler, m.totalBufBytes.Load(), capAvail)
	if err != nil {
		return store.ManagedInstance{}, err
	}

	if in.Kind == "opencode-web" {
		return m.startOpencodeWeb(in.WorktreeID, wtName, effectiveTagID, cwd, env, id, instName, capBytes)
	}

	cmd := exec.Command("zsh", "-f", "-i")
	cmd.Dir = cwd
	cmd.Env = os.Environ()
	for k, v := range env {
		cmd.Env = append(cmd.Env, k+"="+v)
	}

	if strings.TrimSpace(preStart) != "" {
		pre := exec.Command("zsh", "-lc", preStart)
		pre.Dir = cwd
		pre.Env = cmd.Env
		if out, err := pre.CombinedOutput(); err != nil {
			return store.ManagedInstance{}, fmt.Errorf("preStart failed: %w: %s", err, strings.TrimSpace(string(out)))
		}
	}

	ptmx, err := pty.Start(cmd)
	if err != nil {
		return store.ManagedInstance{}, err
	}
	buf := NewRingBuffer(capBytes)
	m.totalBufBytes.Add(capBytes)

	now := time.Now().UTC().Format(time.RFC3339)
	inst := store.ManagedInstance{
		ID:           id,
		WorktreeID:   in.WorktreeID,
		WorktreeName: wtName,
		TagID:        effectiveTagID,
		Name:         instName,
		Command:      command,
		Cwd:          cwd,
		Env:          sanitizedEnv(env),
		PID:          cmd.Process.Pid,
		Status:       "running",
		CreatedAt:    now,
	}
	m.stateMu.Lock()
	st2, loadErr := m.Store.Load()
	if loadErr != nil {
		m.stateMu.Unlock()
		_ = cmd.Process.Kill()
		_ = ptmx.Close()
		buf.Close()
		m.totalBufBytes.Add(-capBytes)
		return store.ManagedInstance{}, loadErr
	}
	st2.Instances = append(st2.Instances, inst)
	if st2.TabOrder == nil {
		st2.TabOrder = make(map[string][]string)
	}
	st2.TabOrder[in.WorktreeID] = append(st2.TabOrder[in.WorktreeID], inst.ID)
	if err := m.Store.SaveWithVersion(st2, st2.Version); err != nil {
		m.stateMu.Unlock()
		_ = cmd.Process.Kill()
		_ = ptmx.Close()
		buf.Close()
		m.totalBufBytes.Add(-capBytes)
		return store.ManagedInstance{}, err
	}
	if m.buffers == nil {
		m.buffers = map[string]*atomic.Pointer[RingBuffer]{}
	}
	p := &atomic.Pointer[RingBuffer]{}
	p.Store(buf)
	m.buffers[id] = p
	m.stateMu.Unlock()

	m.mu.Lock()
	m.running[id] = cmd
	m.inputs[id] = ptmx
	m.ptys[id] = ptmx
	m.mu.Unlock()

	if strings.TrimSpace(command) != "" {
		go func() {
			time.Sleep(150 * time.Millisecond)
			_, _ = io.WriteString(ptmx, command+"\n")
		}()
	}

	go m.pumpLogs(id, ptmx)
	go m.wait(id, cmd)
	return inst, nil
}

func (m *Manager) startOpencodeWeb(worktreeID, wtName, tagID, cwd string, tagEnv map[string]string, id, instName string, capBytes int64) (store.ManagedInstance, error) {
	password, err := GeneratePassword()
	if err != nil {
		return store.ManagedInstance{}, fmt.Errorf("generate opencode password: %w", err)
	}

	exe, args := Command()
	cmd := exec.Command(exe, args...)
	cmd.Dir = cwd
	cmd.Env = BuildEnv(tagEnv, password)

	stdoutR, stdoutW := io.Pipe()
	cmd.Stdout = stdoutW
	cmd.Stderr = stdoutW

	if err := cmd.Start(); err != nil {
		stdoutW.Close()
		stdoutR.Close()
		return store.ManagedInstance{}, err
	}

	buf := NewRingBuffer(capBytes)
	m.totalBufBytes.Add(capBytes)

	now := time.Now().UTC().Format(time.RFC3339)
	inst := store.ManagedInstance{
		ID:           id,
		WorktreeID:   worktreeID,
		WorktreeName: wtName,
		TagID:        tagID,
		Name:         instName,
		Command:      exe + " " + strings.Join(args, " "),
		Cwd:          cwd,
		Env:          sanitizedEnv(tagEnv),
		Kind:         "opencode-web",
		Extra:        map[string]string{"password": password, "worktree_abs": cwd},
		PID:          cmd.Process.Pid,
		Status:       "starting",
		CreatedAt:    now,
	}
	m.stateMu.Lock()
	st2, loadErr := m.Store.Load()
	if loadErr != nil {
		m.stateMu.Unlock()
		stdoutW.Close()
		stdoutR.Close()
		_ = cmd.Process.Kill()
		buf.Close()
		m.totalBufBytes.Add(-capBytes)
		return store.ManagedInstance{}, loadErr
	}
	st2.Instances = append(st2.Instances, inst)
	if st2.TabOrder == nil {
		st2.TabOrder = make(map[string][]string)
	}
	st2.TabOrder[worktreeID] = append(st2.TabOrder[worktreeID], inst.ID)
	if saveErr := m.Store.SaveWithVersion(st2, st2.Version); saveErr != nil {
		m.stateMu.Unlock()
		stdoutW.Close()
		stdoutR.Close()
		_ = cmd.Process.Kill()
		buf.Close()
		m.totalBufBytes.Add(-capBytes)
		return store.ManagedInstance{}, saveErr
	}
	if m.buffers == nil {
		m.buffers = map[string]*atomic.Pointer[RingBuffer]{}
	}
	p := &atomic.Pointer[RingBuffer]{}
	p.Store(buf)
	m.buffers[id] = p
	m.stateMu.Unlock()

	m.mu.Lock()
	m.running[id] = cmd
	m.inputs[id] = stdoutW
	m.mu.Unlock()

	go m.pumpAndWatch(id, stdoutR, password, cwd)
	go m.opencodeHealth(id)
	go m.wait(id, cmd)

	return inst, nil
}

// pumpAndWatch reads stdout from an opencode server process, writes to the
// ring buffer and broadcast, and scans for the "opencode server listening on"
// address line. On match it updates the instance Extra with host/port and
// transitions status from "starting" → "running".
func (m *Manager) pumpAndWatch(id string, r io.Reader, password, worktreeAbs string) {
	lineBuf := new(strings.Builder)
	readBuf := make([]byte, 1024)
	addrFound := false

	for {
		n, err := r.Read(readBuf)
		if n > 0 {
			chunk := redact.Text(string(readBuf[:n]))
			if !addrFound {
				lineBuf.WriteString(chunk)
				accum := lineBuf.String()
				for {
					idx := strings.Index(accum, "\n")
					if idx < 0 {
						break
					}
					line := accum[:idx]
					accum = accum[idx+1:]
					if host, port, ok := ExtractListeningAddress(line); ok {
						extra := map[string]string{
							"host":         host,
							"port":         port,
							"password":     password,
							"worktree_abs": worktreeAbs,
							"url_path":     "/__opencode/" + id + "/",
						}
						_ = m.UpdateExtra(id, extra)
						addrFound = true
					}
				}
				if addrFound {
					lineBuf.Reset()
				} else {
					lineBuf.Reset()
					lineBuf.WriteString(accum)
				}
			}

			if rb := m.loadBuffer(id); rb != nil {
				rb.WriteString(chunk)
			}
			m.broadcastOutput(id, chunk)
		}
		if err != nil {
			return
		}
	}
}

func (m *Manager) loadBuffer(id string) *RingBuffer {
	m.stateMu.Lock()
	defer m.stateMu.Unlock()
	p, ok := m.buffers[id]
	if !ok || p == nil {
		return nil
	}
	return p.Load()
}

func (m *Manager) opencodeHealth(id string) {
	for {
		time.Sleep(5 * time.Second)
		extra, err := m.ocExtra(id)
		if err != nil || extra == nil {
			continue
		}
		host := extra["host"]
		port := extra["port"]
		password := extra["password"]
		if port == "" || password == "" {
			continue
		}
		addr := "http://" + host + ":" + port + "/global/health"
		req, reqErr := http.NewRequestWithContext(context.Background(), http.MethodGet, addr, nil)
		if reqErr != nil {
			continue
		}
		req.SetBasicAuth("opencode", password)
		client := &http.Client{Timeout: 3 * time.Second}
		resp, doErr := client.Do(req)
		if doErr != nil || (resp != nil && resp.StatusCode >= 400) {
			m.ocFail(id)
			if resp != nil {
				resp.Body.Close()
			}
			continue
		}
		if resp != nil {
			resp.Body.Close()
		}
		m.ocReset(id)
	}
}

func (m *Manager) ocFail(id string) {
	m.stateMu.Lock()
	defer m.stateMu.Unlock()
	st, err := m.Store.Load()
	if err != nil {
		return
	}
	for i := range st.Instances {
		if st.Instances[i].ID == id && st.Instances[i].Kind == "opencode-web" {
			if st.Instances[i].Extra == nil {
				st.Instances[i].Extra = map[string]string{}
			}
			failCount := 1
			if v, ok := st.Instances[i].Extra["_health_fail_count"]; ok {
				if n, pErr := fmt.Sscanf(v, "%d", &failCount); pErr == nil && n == 1 {
					failCount++
				}
			}
			st.Instances[i].Extra["_health_fail_count"] = fmt.Sprintf("%d", failCount)
			if failCount >= 3 {
				st.Instances[i].Status = "failed"
			}
			break
		}
	}
	_ = m.Store.SaveWithVersion(st, st.Version)
}

func (m *Manager) ocReset(id string) {
	m.stateMu.Lock()
	defer m.stateMu.Unlock()
	st, err := m.Store.Load()
	if err != nil {
		return
	}
	for i := range st.Instances {
		if st.Instances[i].ID == id && st.Instances[i].Kind == "opencode-web" {
			delete(st.Instances[i].Extra, "_health_fail_count")
			break
		}
	}
	_ = m.Store.SaveWithVersion(st, st.Version)
}

func (m *Manager) ocExtra(id string) (map[string]string, error) {
	st, err := m.Store.Load()
	if err != nil {
		return nil, err
	}
	for _, inst := range st.Instances {
		if inst.ID == id {
			return inst.Extra, nil
		}
	}
	return nil, nil
}

// Get returns a copy of the instance by ID, or an error.
func (m *Manager) Get(id string) (store.ManagedInstance, error) {
	st, err := m.Store.Load()
	if err != nil {
		return store.ManagedInstance{}, err
	}
	for _, inst := range st.Instances {
		if inst.ID == id {
			return inst, nil
		}
	}
	return store.ManagedInstance{}, fmt.Errorf("%w: %s", ErrInstanceNotFound, id)
}

// UpdateExtra sets the Extra map for an instance (used by opencodeWatch to
// populate port/host after the listening line is parsed).
func (m *Manager) UpdateExtra(id string, extra map[string]string) error {
	m.stateMu.Lock()
	defer m.stateMu.Unlock()
	st, err := m.Store.Load()
	if err != nil {
		return err
	}
	for i := range st.Instances {
		if st.Instances[i].ID == id {
			st.Instances[i].Extra = extra
			if st.Instances[i].Status == "starting" {
				st.Instances[i].Status = "running"
			}
			return m.Store.SaveWithVersion(st, st.Version)
		}
	}
	return fmt.Errorf("%w: %s", ErrInstanceNotFound, id)
}

func (m *Manager) List() ([]store.ManagedInstance, error) {
	st, err := m.Store.Load()
	if err != nil {
		return nil, err
	}
	return st.Instances, nil
}

// UpdateName updates the display name of an existing instance.
func (m *Manager) UpdateName(id string, name string) (store.ManagedInstance, error) {
	id = strings.TrimSpace(id)
	if id == "" {
		return store.ManagedInstance{}, errors.New("id is required")
	}
	name = strings.TrimSpace(name)
	if name == "" {
		return store.ManagedInstance{}, errors.New("name cannot be empty")
	}

	m.stateMu.Lock()
	defer m.stateMu.Unlock()
	st, err := m.Store.Load()
	if err != nil {
		return store.ManagedInstance{}, err
	}
	for i := range st.Instances {
		if st.Instances[i].ID == id {
			st.Instances[i].Name = name
			if err := m.Store.SaveWithVersion(st, st.Version); err != nil {
				return store.ManagedInstance{}, err
			}
			return st.Instances[i], nil
		}
	}
	return store.ManagedInstance{}, fmt.Errorf("%w: %s", ErrInstanceNotFound, id)
}

// ReorderInstances sets the tab order for a specific worktree.
func (m *Manager) ReorderInstances(worktreeID string, orderIDs []string, expectedVersion int64) error {
	worktreeID = strings.TrimSpace(worktreeID)
	if worktreeID == "" {
		return errors.New("worktree_id is required")
	}

	m.stateMu.Lock()
	defer m.stateMu.Unlock()
	st, err := m.Store.Load()
	if err != nil {
		return err
	}

	idSet := make(map[string]bool, len(orderIDs))
	for _, id := range orderIDs {
		idSet[id] = true
	}

	for _, inst := range st.Instances {
		if inst.WorktreeID == worktreeID {
			if !idSet[inst.ID] {
				return fmt.Errorf("order list is missing instance: %s", inst.ID)
			}
		}
	}

	for _, id := range orderIDs {
		found := false
		for _, inst := range st.Instances {
			if inst.ID == id && inst.WorktreeID == worktreeID {
				found = true
				break
			}
		}
		if !found {
			return fmt.Errorf("instance %s does not belong to worktree %s", id, worktreeID)
		}
	}

	if st.TabOrder == nil {
		st.TabOrder = make(map[string][]string)
	}
	st.TabOrder[worktreeID] = orderIDs

	idToInst := make(map[string]store.ManagedInstance, len(st.Instances))
	for _, inst := range st.Instances {
		idToInst[inst.ID] = inst
	}

	newOrder := make([]store.ManagedInstance, 0, len(st.Instances))
	for _, id := range orderIDs {
		newOrder = append(newOrder, idToInst[id])
	}
	for _, inst := range st.Instances {
		if inst.WorktreeID != worktreeID {
			newOrder = append(newOrder, inst)
		}
	}
	st.Instances = newOrder

	return m.Store.SaveWithVersion(st, expectedVersion)
}

func (m *Manager) Stop(id string) error {
	id = strings.TrimSpace(id)
	if id == "" {
		return errors.New("id is required")
	}

	st, err := m.Store.Load()
	if err != nil {
		return err
	}
	var inst *store.ManagedInstance
	for i := range st.Instances {
		if st.Instances[i].ID == id {
			inst = &st.Instances[i]
			break
		}
	}
	if inst == nil {
		return fmt.Errorf("unknown instance id: %s", id)
	}
	if inst.Status != "running" {
		return nil
	}

	m.mu.Lock()
	cmd := m.running[id]
	in := m.inputs[id]
	m.mu.Unlock()

	if (cmd == nil || cmd.Process == nil) && inst.PID > 0 {
		terminatePID(inst.PID, syscall.SIGTERM)
		go func(pid int) {
			time.Sleep(5 * time.Second)
			terminatePID(pid, syscall.SIGKILL)
		}(inst.PID)
		_ = m.markStopped(id, "stopped")
		return nil
	}
	if cmd == nil || cmd.Process == nil {
		_ = m.markStopped(id, "stopped")
		return nil
	}
	if in != nil {
		_ = in.Close()
	}

	if err := terminatePID(cmd.Process.Pid, syscall.SIGTERM); err != nil {
		return err
	}
	go func() {
		time.Sleep(5 * time.Second)
		m.mu.Lock()
		defer m.mu.Unlock()
		if m.running[id] != nil && m.running[id].Process != nil {
			_ = terminatePID(m.running[id].Process.Pid, syscall.SIGKILL)
		}
	}()
	_ = m.markStopped(id, "stopped")
	return nil
}

func (m *Manager) SendInput(id string, input string) error {
	id = strings.TrimSpace(id)
	if id == "" {
		return errors.New("id is required")
	}
	m.mu.Lock()
	in := m.inputs[id]
	cmd := m.running[id]
	m.mu.Unlock()
	if in == nil {
		return fmt.Errorf("instance input unavailable: %s", id)
	}

	for _, ch := range input {
		switch ch {
		case 0x03:
			if cmd != nil && cmd.Process != nil {
				_ = terminatePID(cmd.Process.Pid, syscall.SIGINT)
			}
			if _, err := io.WriteString(in, string(ch)); err != nil {
				return err
			}
			return nil
		case 0x1A:
			if cmd != nil && cmd.Process != nil {
				_ = terminatePID(cmd.Process.Pid, syscall.SIGTSTP)
			}
			if _, err := io.WriteString(in, string(ch)); err != nil {
				return err
			}
			return nil
		case 0x1C:
			if cmd != nil && cmd.Process != nil {
				_ = terminatePID(cmd.Process.Pid, syscall.SIGQUIT)
			}
			if _, err := io.WriteString(in, string(ch)); err != nil {
				return err
			}
			return nil
		}
	}

	if _, err := io.WriteString(in, input); err != nil {
		return err
	}
	return nil
}

func (m *Manager) Resize(id string, cols, rows int) error {
	id = strings.TrimSpace(id)
	if id == "" {
		return errors.New("id is required")
	}
	if cols <= 0 || rows <= 0 {
		return errors.New("invalid terminal size")
	}
	m.mu.Lock()
	ptmx := m.ptys[id]
	m.mu.Unlock()
	if ptmx == nil {
		return fmt.Errorf("instance pty unavailable: %s", id)
	}
	return pty.Setsize(ptmx, &pty.Winsize{
		Cols: uint16(cols),
		Rows: uint16(rows),
	})
}

func (m *Manager) Restart(id string) (store.ManagedInstance, error) {
	id = strings.TrimSpace(id)
	if id == "" {
		return store.ManagedInstance{}, errors.New("id is required")
	}
	st, err := m.Store.Load()
	if err != nil {
		return store.ManagedInstance{}, err
	}
	idx := -1
	var old store.ManagedInstance
	for i := range st.Instances {
		if st.Instances[i].ID == id {
			idx = i
			old = st.Instances[i]
			break
		}
	}
	if idx == -1 {
		return store.ManagedInstance{}, fmt.Errorf("unknown instance id: %s", id)
	}
	if old.Status == "running" {
		return store.ManagedInstance{}, fmt.Errorf("instance is running: %s", id)
	}

	startIn := StartInput{
		WorktreeID: old.WorktreeID,
		Name:       old.Name,
	}
	if old.WorktreeID == MainWorktreeID {
		startIn.Root = m.Root
	}
	if old.TagID != "" && old.TagID != "adhoc" && old.TagID != "idle" {
		startIn.TagID = old.TagID
	} else {
		startIn.Command = old.Command
	}

	newInst, err := m.Start(startIn)
	if err != nil {
		return store.ManagedInstance{}, err
	}

	m.stateMu.Lock()
	st2, err := m.Store.Load()
	if err != nil {
		m.stateMu.Unlock()
		return newInst, nil
	}
	oldIdx := -1
	for i := range st2.Instances {
		if st2.Instances[i].ID == id {
			oldIdx = i
			break
		}
	}
	if oldIdx >= 0 {
		st2.Instances = append(st2.Instances[:oldIdx], st2.Instances[oldIdx+1:]...)
	}
	for i := range st2.Instances {
		if st2.Instances[i].ID == newInst.ID {
			st2.Instances[i].RestartedFrom = id
			break
		}
	}
	_ = m.Store.SaveWithVersion(st2, st2.Version)
	m.stateMu.Unlock()

	m.mu.Lock()
	delete(m.running, id)
	delete(m.inputs, id)
	if ptmx, ok := m.ptys[id]; ok {
		_ = ptmx.Close()
		delete(m.ptys, id)
	}
	m.closeSubscribersLocked(id)
	m.mu.Unlock()

	m.stateMu.Lock()
	m.dropBufferLocked(id)
	m.stateMu.Unlock()

	return newInst, nil
}

func (m *Manager) SubscribeOutput(id string) (<-chan string, func(), error) {
	id = strings.TrimSpace(id)
	if id == "" {
		return nil, nil, errors.New("id is required")
	}
	st, err := m.Store.Load()
	if err != nil {
		return nil, nil, err
	}
	found := false
	for _, it := range st.Instances {
		if it.ID == id {
			found = true
			break
		}
	}
	if !found {
		return nil, nil, fmt.Errorf("unknown instance id: %s", id)
	}

	m.mu.Lock()
	if m.subscribers == nil {
		m.subscribers = map[string]map[chan string]struct{}{}
	}
	if m.subscribers[id] == nil {
		m.subscribers[id] = map[chan string]struct{}{}
	}
	ch := make(chan string, 64)
	m.subscribers[id][ch] = struct{}{}
	m.mu.Unlock()

	cancel := func() {
		m.mu.Lock()
		if subs := m.subscribers[id]; subs != nil {
			if _, ok := subs[ch]; ok {
				delete(subs, ch)
				close(ch)
			}
			if len(subs) == 0 {
				delete(m.subscribers, id)
			}
		}
		m.mu.Unlock()
	}
	return ch, cancel, nil
}

func (m *Manager) Delete(id string) error {
	id = strings.TrimSpace(id)
	if id == "" {
		return errors.New("id is required")
	}
	m.stateMu.Lock()
	defer m.stateMu.Unlock()
	st, err := m.Store.Load()
	if err != nil {
		return err
	}
	idx := -1
	for i := range st.Instances {
		if st.Instances[i].ID == id {
			if st.Instances[i].Status == "running" {
				return fmt.Errorf("instance is running: %s", id)
			}
			idx = i
			break
		}
	}
	if idx == -1 {
		return fmt.Errorf("unknown instance id: %s", id)
	}
	st.Instances = append(st.Instances[:idx], st.Instances[idx+1:]...)
	if err := m.Store.SaveWithVersion(st, st.Version); err != nil {
		return err
	}
	m.dropBufferLocked(id)
	m.mu.Lock()
	delete(m.running, id)
	delete(m.inputs, id)
	if ptmx, ok := m.ptys[id]; ok {
		_ = ptmx.Close()
		delete(m.ptys, id)
	}
	m.closeSubscribersLocked(id)
	m.mu.Unlock()
	return nil
}

func (m *Manager) Tail(id string, n int64) (string, error) {
	if n <= 0 {
		n = 4096
	}
	m.stateMu.Lock()
	p, ok := m.buffers[id]
	m.stateMu.Unlock()
	if !ok || p == nil {
		return "", nil
	}
	rb := p.Load()
	if rb == nil {
		return "", nil
	}
	body, _ := rb.Tail(n)
	return body, nil
}

// ReadSince returns log content starting at byte offset "since" (inclusive),
// capped by maxBytes, and the next byte offset.
func (m *Manager) ReadSince(id string, since int64, maxBytes int64) (string, int64, error) {
	if since < 0 {
		since = 0
	}
	if maxBytes <= 0 {
		maxBytes = 64 * 1024
	}
	m.stateMu.Lock()
	p, ok := m.buffers[id]
	m.stateMu.Unlock()
	if !ok || p == nil {
		return "", since, nil
	}
	rb := p.Load()
	if rb == nil {
		return "", since, nil
	}
	return rb.ReadSince(since, maxBytes)
}

func (m *Manager) wait(id string, cmd *exec.Cmd) {
	err := cmd.Wait()

	m.mu.Lock()
	delete(m.running, id)
	delete(m.inputs, id)
	if ptmx, ok := m.ptys[id]; ok {
		_ = ptmx.Close()
		delete(m.ptys, id)
	}
	m.closeSubscribersLocked(id)
	m.mu.Unlock()

	m.stateMu.Lock()
	defer m.stateMu.Unlock()
	m.dropBufferLocked(id)
	st, loadErr := m.Store.Load()
	if loadErr != nil {
		return
	}
	for i := range st.Instances {
		if st.Instances[i].ID == id {
			if err == nil {
				st.Instances[i].Status = "exited"
			} else {
				st.Instances[i].Status = "failed"
			}
			st.Instances[i].StoppedAt = time.Now().UTC().Format(time.RFC3339)
			break
		}
	}
	_ = m.Store.SaveWithVersion(st, st.Version)
}

// pumpLogs reads PTY output, redacts it, and writes to the instance's ring
// buffer (replacing the old on-disk log file). The hot path is designed for
// heavy TUI workloads (e.g. OpenCode CLI redraws producing MB/s): the ring
// buffer pointer is fetched once under stateMu, then each chunk uses an
// atomic load so neither the per-chunk stateMu acquisition nor the
// `[]byte(chunk)` allocation occurs on the data path.
//
// Lifecycle: PTY mode yields stdout == stderr, so a single Read is enough
// (no wg/stderr branch like the old file-based implementation needed).
// If Stop/Restart swaps the buffer to nil, subsequent atomic loads return
// nil and the chunk is dropped — acceptable because the same lifecycle
// event closes ptmx, causing Read to return EOF and ending this goroutine.
func (m *Manager) pumpLogs(id string, ptmx *os.File) {
	defer func() { _ = ptmx.Close() }()

	m.stateMu.Lock()
	p, ok := m.buffers[id]
	m.stateMu.Unlock()
	if !ok || p == nil {
		// No buffer (instance stopped before goroutine started) — drain
		// ptmx so the underlying process is not blocked on a full pty buffer,
		// then exit.
		_, _ = io.Copy(io.Discard, ptmx)
		return
	}

	readBuf := make([]byte, 1024)
	for {
		n, err := ptmx.Read(readBuf)
		if n > 0 {
			chunk := redact.Text(string(readBuf[:n]))
			if rb := p.Load(); rb != nil {
				rb.WriteString(chunk)
			}
			m.broadcastOutput(id, chunk)
		}
		if err != nil {
			return
		}
	}
}

func (m *Manager) broadcastOutput(id string, chunk string) {
	if chunk == "" {
		return
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	subs := m.subscribers[id]
	for ch := range subs {
		select {
		case ch <- chunk:
		default:
		}
	}
}

// closeSubscribersLocked closes subscriber channels for the given instance.
// Buffer cleanup is done separately via dropBufferLocked under stateMu to
// avoid lock-ordering issues.
//
// Caller must hold m.mu.
func (m *Manager) closeSubscribersLocked(id string) {
	subs := m.subscribers[id]
	for ch := range subs {
		close(ch)
	}
	delete(m.subscribers, id)
}

// dropBufferLocked removes the ring buffer for an instance and decrements the
// global cap counter. Caller must hold m.stateMu.
//
// Lifecycle note: in Restart the buffer is dropped AFTER the store is
// updated with the new instance id and AFTER subscribers are closed. There
// is a microsecond-scale window where the old id's buffer is still in the
// map; this is intentional so the old pumpLogs goroutine can finish its
// last few chunks (it will exit on its own when Restart closes the old
// ptmx). Any Tail/ReadSince for the old id in that window returns the
// remaining buffered bytes, which is the documented contract.
func (m *Manager) dropBufferLocked(id string) {
	p, ok := m.buffers[id]
	if !ok {
		return
	}
	if rb := p.Swap(nil); rb != nil {
		capBytes := rb.CapBytes()
		rb.Close()
		if capBytes > 0 {
			m.totalBufBytes.Add(-capBytes)
		}
	}
	delete(m.buffers, id)
}

// BufferCapBytesFor returns the ring buffer capacity in bytes for a given
// instance, or 0 if the instance has no active buffer.
func (m *Manager) BufferCapBytesFor(id string) int64 {
	m.stateMu.Lock()
	defer m.stateMu.Unlock()
	p, ok := m.buffers[id]
	if !ok || p == nil {
		return 0
	}
	rb := p.Load()
	if rb == nil {
		return 0
	}
	return rb.CapBytes()
}

// BufferUsedBytesFor returns the ring buffer actual usage in bytes for a
// given instance, or 0 if the instance has no active buffer.
func (m *Manager) BufferUsedBytesFor(id string) int64 {
	m.stateMu.Lock()
	defer m.stateMu.Unlock()
	p, ok := m.buffers[id]
	if !ok || p == nil {
		return 0
	}
	rb := p.Load()
	if rb == nil {
		return 0
	}
	return rb.BytesUsed()
}

func sanitizedEnv(in map[string]string) map[string]string {
	if len(in) == 0 {
		return nil
	}
	out := map[string]string{}
	for k, v := range in {
		out[k] = redact.EnvKey(k, v)
	}
	return out
}

func shortID() string {
	b := make([]byte, 6)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}

func terminatePID(pid int, sig syscall.Signal) error {
	if pid <= 0 {
		return errors.New("invalid pid")
	}
	if err := syscall.Kill(-pid, sig); err == nil || errors.Is(err, syscall.ESRCH) {
		return nil
	}
	p, err := os.FindProcess(pid)
	if err != nil || p == nil {
		return err
	}
	if err := p.Signal(sig); err != nil && !errors.Is(err, syscall.ESRCH) {
		return err
	}
	return nil
}

func (m *Manager) markStopped(id string, status string) error {
	m.stateMu.Lock()
	defer m.stateMu.Unlock()
	st, err := m.Store.Load()
	if err != nil {
		return err
	}
	for i := range st.Instances {
		if st.Instances[i].ID != id {
			continue
		}
		if st.Instances[i].Status == "running" {
			st.Instances[i].Status = status
			st.Instances[i].StoppedAt = time.Now().UTC().Format(time.RFC3339)
			return m.Store.SaveWithVersion(st, st.Version)
		}
		return nil
	}
	return fmt.Errorf("unknown instance id: %s", id)
}

func (m *Manager) loadTags() (map[string]tag.Tag, error) {
	base, err := os.UserConfigDir()
	if err != nil {
		return nil, err
	}
	tm := tag.Manager{
		GlobalPath:  filepath.Join(base, "myworktree", "tags.json"),
		ProjectPath: filepath.Join(m.DataDir, "tags.json"),
	}
	return tm.LoadMerged()
}

// SetConnectionType records the current transport type for an instance.
func (m *Manager) SetConnectionType(id, connType string) {
	m.connsMu.Lock()
	defer m.connsMu.Unlock()
	if m.conns == nil {
		m.conns = map[string]string{}
	}
	m.conns[id] = connType
}

// ConnectionType returns the current transport type for an instance.
func (m *Manager) ConnectionType(id string) string {
	m.connsMu.Lock()
	defer m.connsMu.Unlock()
	if m.conns == nil {
		return ""
	}
	return m.conns[id]
}

// AllConnectionTypes returns a map of all instance connection types.
func (m *Manager) AllConnectionTypes() map[string]string {
	m.connsMu.Lock()
	defer m.connsMu.Unlock()
	if m.conns == nil {
		return map[string]string{}
	}
	out := make(map[string]string, len(m.conns))
	for k, v := range m.conns {
		out[k] = v
	}
	return out
}

// RegisterSSEConnection marks an instance as having an active SSE connection
// and returns a cleanup function that clears the mark on call.
func (m *Manager) RegisterSSEConnection(id string) func() {
	m.SetConnectionType(id, "sse")
	return func() {
		m.connsMu.Lock()
		defer m.connsMu.Unlock()
		if m.conns != nil && m.conns[id] == "sse" {
			delete(m.conns, id)
		}
	}
}
