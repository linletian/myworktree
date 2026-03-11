package instance

import (
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/creack/pty"
	"myworktree/internal/redact"
	"myworktree/internal/store"
	"myworktree/internal/tag"
)

const maxLogBytes int64 = 10 * 1024 * 1024

type Manager struct {
	DataDir string
	Store   store.FileStore
	Logger  *log.Logger

	stateMu     sync.Mutex
	mu          sync.Mutex
	running     map[string]*exec.Cmd
	inputs      map[string]io.WriteCloser
	ptys        map[string]*os.File
	subscribers map[string]map[chan string]struct{}
}

type StartInput struct {
	WorktreeID string
	TagID      string
	Command    string // optional if TagID is set; required if TagID is empty
	Name       string
	Labels     map[string]string
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
	if err := m.Store.Save(st); err != nil {
		return 0, err
	}
	return changed, nil
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
	m.mu.Unlock()

	if strings.TrimSpace(in.WorktreeID) == "" {
		return store.ManagedInstance{}, errors.New("worktree_id is required")
	}

	st, err := m.Store.Load()
	if err != nil {
		return store.ManagedInstance{}, err
	}

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
			return store.ManagedInstance{}, fmt.Errorf("unknown tag id: %s", effectiveTagID)
		}
		if strings.TrimSpace(t.Command) == "" {
			return store.ManagedInstance{}, errors.New("tag command is required")
		}
		command = t.Command
		cwdRel = t.Cwd
		preStart = t.PreStart
		env = t.Env
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
	logDir := filepath.Join(m.DataDir, "logs")
	if err := os.MkdirAll(logDir, 0o755); err != nil {
		return store.ManagedInstance{}, err
	}
	logPath := filepath.Join(logDir, id+".log")
	logFile, err := os.OpenFile(logPath, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600)
	if err != nil {
		return store.ManagedInstance{}, err
	}

	cwd := wt.Path
	if strings.TrimSpace(cwdRel) != "" && cwdRel != "." {
		cwd = filepath.Join(wt.Path, cwdRel)
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
			_ = logFile.Close()
			_ = os.WriteFile(logPath, []byte(redact.Text(string(out))), 0o600)
			return store.ManagedInstance{}, fmt.Errorf("preStart failed: %w: %s", err, strings.TrimSpace(string(out)))
		}
	}

	ptmx, err := pty.Start(cmd)
	if err != nil {
		_ = logFile.Close()
		return store.ManagedInstance{}, err
	}

	now := time.Now().UTC().Format(time.RFC3339)
	inst := store.ManagedInstance{
		ID:           id,
		WorktreeID:   wt.ID,
		WorktreeName: wt.Name,
		TagID:        effectiveTagID,
		Name:         instName,
		Labels:       in.Labels,
		Command:      command,
		Cwd:          cwd,
		Env:          sanitizedEnv(env),
		PID:          cmd.Process.Pid,
		Status:       "running",
		LogPath:      logPath,
		CreatedAt:    now,
	}
	m.stateMu.Lock()
	st2, err := m.Store.Load()
	if err == nil {
		st2.Instances = append(st2.Instances, inst)
		err = m.Store.Save(st2)
	}
	m.stateMu.Unlock()
	if err != nil {
		_ = cmd.Process.Kill()
		_ = ptmx.Close()
		_ = logFile.Close()
		return store.ManagedInstance{}, err
	}

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

	go m.pumpLogs(id, ptmx, ptmx, logFile, logPath)
	go m.wait(id, cmd)
	return inst, nil
}

func (m *Manager) List() ([]store.ManagedInstance, error) {
	st, err := m.Store.Load()
	if err != nil {
		return nil, err
	}
	return st.Instances, nil
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
		// Idempotent: stopping an already-exited instance is a no-op.
		return nil
	}

	m.mu.Lock()
	cmd := m.running[id]
	in := m.inputs[id]
	m.mu.Unlock()

	// If server restarted, cmd may be missing; best-effort signal by PID/process-group.
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

	// Process control characters (Ctrl+C/Z/\)
	// These send signals to the process group and return immediately.
	// Any characters after the control character in the same input are discarded.
	// This matches real terminal behavior where Ctrl+C interrupts immediately.
	// Rationale: once a signal is sent, the process state changes (may exit/suspend),
	// and sending additional input is usually not meaningful.
	for _, ch := range input {
		switch ch {
		case 0x03:
			if cmd != nil && cmd.Process != nil {
				_ = terminatePID(cmd.Process.Pid, syscall.SIGINT)
			}
			return nil
		case 0x1A:
			if cmd != nil && cmd.Process != nil {
				_ = terminatePID(cmd.Process.Pid, syscall.SIGTSTP)
			}
			return nil
		case 0x1C:
			if cmd != nil && cmd.Process != nil {
				_ = terminatePID(cmd.Process.Pid, syscall.SIGQUIT)
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
		Labels:     old.Labels,
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
	for i := range st2.Instances {
		if st2.Instances[i].ID == id {
			if !st2.Instances[i].Archived {
				st2.Instances[i].Archived = true
				st2.Instances[i].ArchivedAt = time.Now().UTC().Format(time.RFC3339)
			}
			st2.Instances[i].RestartedTo = newInst.ID
			break
		}
	}
	for i := range st2.Instances {
		if st2.Instances[i].ID == newInst.ID {
			st2.Instances[i].RestartedFrom = id
			break
		}
	}
	_ = m.Store.Save(st2)
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
	if logPathByID(st, id) == "" {
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

func (m *Manager) Archive(id string) error {
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
	for i := range st.Instances {
		if st.Instances[i].ID == id {
			if st.Instances[i].Status == "running" {
				return fmt.Errorf("instance is running: %s", id)
			}
			st.Instances[i].Archived = true
			st.Instances[i].ArchivedAt = time.Now().UTC().Format(time.RFC3339)
			return m.Store.Save(st)
		}
	}
	return fmt.Errorf("unknown instance id: %s", id)
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
	var logPath string
	for i := range st.Instances {
		if st.Instances[i].ID == id {
			if st.Instances[i].Status == "running" {
				return fmt.Errorf("instance is running: %s", id)
			}
			if !st.Instances[i].Archived {
				return fmt.Errorf("instance is not archived: %s", id)
			}
			idx = i
			logPath = st.Instances[i].LogPath
			break
		}
	}
	if idx == -1 {
		return fmt.Errorf("unknown instance id: %s", id)
	}
	st.Instances = append(st.Instances[:idx], st.Instances[idx+1:]...)
	if err := m.Store.Save(st); err != nil {
		return err
	}
	if strings.TrimSpace(logPath) != "" {
		_ = os.Remove(logPath)
	}
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
	st, err := m.Store.Load()
	if err != nil {
		return "", err
	}
	path := logPathByID(st, id)
	if path == "" {
		return "", fmt.Errorf("unknown instance id: %s", id)
	}
	f, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer f.Close()

	fi, err := f.Stat()
	if err != nil {
		return "", err
	}
	size := fi.Size()
	start := size - n
	if start < 0 {
		start = 0
	}
	if _, err := f.Seek(start, io.SeekStart); err != nil {
		return "", err
	}
	b, err := io.ReadAll(f)
	if err != nil {
		return "", err
	}
	return string(b), nil
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
	st, err := m.Store.Load()
	if err != nil {
		return "", since, err
	}
	path := logPathByID(st, id)
	if path == "" {
		return "", since, fmt.Errorf("unknown instance id: %s", id)
	}
	f, err := os.Open(path)
	if err != nil {
		return "", since, err
	}
	defer f.Close()

	fi, err := f.Stat()
	if err != nil {
		return "", since, err
	}
	size := fi.Size()
	start := since
	if start > size {
		start = size
	}
	if size-start > maxBytes {
		start = size - maxBytes
	}
	if _, err := f.Seek(start, io.SeekStart); err != nil {
		return "", since, err
	}
	b, err := io.ReadAll(f)
	if err != nil {
		return "", since, err
	}
	return string(b), start + int64(len(b)), nil
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
	_ = m.Store.Save(st)
}

func (m *Manager) pumpLogs(id string, stdout io.Reader, stderr io.Reader, out *os.File, logPath string) {
	defer func() { _ = out.Close() }()
	var wg sync.WaitGroup
	wg.Add(2)
	write := func(r io.Reader) {
		defer wg.Done()
		buf := make([]byte, 1024)
		for {
			n, err := r.Read(buf)
			if n > 0 {
				chunk := redact.Text(string(buf[:n]))
				_, _ = out.WriteString(chunk)
				_ = enforceMaxLogSize(logPath, maxLogBytes)
				m.broadcastOutput(id, chunk)
			}
			if err != nil {
				return
			}
		}
	}
	// For PTY mode, stdout and stderr are the same, so we only read once
	if stdout == stderr {
		wg.Done()
		write(stdout)
	} else {
		go write(stdout)
		go write(stderr)
	}
	wg.Wait()
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

func (m *Manager) closeSubscribersLocked(id string) {
	subs := m.subscribers[id]
	for ch := range subs {
		close(ch)
	}
	delete(m.subscribers, id)
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

func enforceMaxLogSize(path string, max int64) error {
	f, err := os.Open(path)
	if err != nil {
		return err
	}
	defer f.Close()
	fi, err := f.Stat()
	if err != nil {
		return err
	}
	if fi.Size() <= max {
		return nil
	}
	start := fi.Size() - max
	if start < 0 {
		start = 0
	}
	if _, err := f.Seek(start, io.SeekStart); err != nil {
		return err
	}
	b, err := io.ReadAll(f)
	if err != nil {
		return err
	}
	return os.WriteFile(path, b, 0o600)
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
	// Prefer signaling the process group (-pid) so `script` and its child shell exit together.
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
			return m.Store.Save(st)
		}
		return nil
	}
	return fmt.Errorf("unknown instance id: %s", id)
}

func logPathByID(st store.State, id string) string {
	for _, it := range st.Instances {
		if it.ID == id {
			return it.LogPath
		}
	}
	return ""
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
