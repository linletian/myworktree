package instance

import (
	"bufio"
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

	"myworktree/internal/redact"
	"myworktree/internal/store"
	"myworktree/internal/tag"
)

const maxLogBytes int64 = 10 * 1024 * 1024

type Manager struct {
	DataDir string
	Store   store.FileStore
	Logger  *log.Logger

	mu      sync.Mutex
	running map[string]*exec.Cmd
}

type StartInput struct {
	WorktreeID string
	TagID      string
	Command    string // optional if TagID is set; required if TagID is empty
	Name       string
}

func (m *Manager) Start(in StartInput) (store.ManagedInstance, error) {
	m.mu.Lock()
	if m.running == nil {
		m.running = map[string]*exec.Cmd{}
	}
	m.mu.Unlock()

	if strings.TrimSpace(in.WorktreeID) == "" {
		return store.ManagedInstance{}, errors.New("worktree_id is required")
	}
	if strings.TrimSpace(in.TagID) == "" && strings.TrimSpace(in.Command) == "" {
		return store.ManagedInstance{}, errors.New("tag_id or command is required")
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

	cmd := exec.Command("zsh", "-lc", command)
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

	stdout, err := cmd.StdoutPipe()
	if err != nil {
		_ = logFile.Close()
		return store.ManagedInstance{}, err
	}
	stderr, err := cmd.StderrPipe()
	if err != nil {
		_ = logFile.Close()
		return store.ManagedInstance{}, err
	}

	if err := cmd.Start(); err != nil {
		_ = logFile.Close()
		return store.ManagedInstance{}, err
	}

	now := time.Now().UTC().Format(time.RFC3339)
	inst := store.ManagedInstance{
		ID:         id,
		WorktreeID: wt.ID,
		TagID:      effectiveTagID,
		Name:       instName,
		Command:    command,
		Cwd:        cwd,
		Env:        sanitizedEnv(env),
		PID:        cmd.Process.Pid,
		Status:     "running",
		LogPath:    logPath,
		CreatedAt:  now,
	}
	st.Instances = append(st.Instances, inst)
	if err := m.Store.Save(st); err != nil {
		_ = cmd.Process.Kill()
		_ = logFile.Close()
		return store.ManagedInstance{}, err
	}

	m.mu.Lock()
	m.running[id] = cmd
	m.mu.Unlock()

	go pumpLogs(stdout, stderr, logFile, logPath)
	go m.wait(id, cmd, logFile)
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
	m.mu.Lock()
	cmd := m.running[id]
	m.mu.Unlock()
	if cmd == nil || cmd.Process == nil {
		return fmt.Errorf("instance not running: %s", id)
	}
	if err := cmd.Process.Signal(syscall.SIGTERM); err != nil {
		return err
	}
	go func() {
		time.Sleep(5 * time.Second)
		m.mu.Lock()
		defer m.mu.Unlock()
		if m.running[id] != nil && m.running[id].Process != nil {
			_ = m.running[id].Process.Kill()
		}
	}()
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
	var path string
	for _, it := range st.Instances {
		if it.ID == id {
			path = it.LogPath
			break
		}
	}
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

func (m *Manager) wait(id string, cmd *exec.Cmd, logFile *os.File) {
	err := cmd.Wait()
	_ = logFile.Close()

	m.mu.Lock()
	delete(m.running, id)
	m.mu.Unlock()

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

func pumpLogs(stdout io.Reader, stderr io.Reader, out *os.File, logPath string) {
	var wg sync.WaitGroup
	wg.Add(2)
	write := func(r io.Reader) {
		defer wg.Done()
		s := bufio.NewScanner(r)
		for s.Scan() {
			line := redact.Text(s.Text())
			_, _ = out.WriteString(line + "\n")
			_ = enforceMaxLogSize(logPath, maxLogBytes)
		}
	}
	go write(stdout)
	go write(stderr)
	wg.Wait()
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
