package store

import (
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"syscall"
)

type State struct {
	Worktrees []ManagedWorktree `json:"worktrees"`
	Instances []ManagedInstance `json:"instances"`
}

type ManagedWorktree struct {
	ID        string `json:"id"`
	Name      string `json:"name"` // human-facing slug
	Path      string `json:"path"`
	Branch    string `json:"branch"`
	BaseRef   string `json:"base_ref"`
	CreatedAt string `json:"created_at"` // RFC3339
}

type ManagedInstance struct {
	ID         string            `json:"id"`
	WorktreeID string            `json:"worktree_id"`
	TagID      string            `json:"tag_id"`
	Name       string            `json:"name"`
	Command    string            `json:"command"`
	Cwd        string            `json:"cwd"`
	Env        map[string]string `json:"env,omitempty"`
	PID        int               `json:"pid"`
	Status     string            `json:"status"` // running|exited|stopped|failed
	LogPath    string            `json:"log_path"`
	CreatedAt  string            `json:"created_at"`
	StoppedAt  string            `json:"stopped_at,omitempty"`
}

type FileStore struct {
	Path string
}

func (fs FileStore) Load() (State, error) {
	st := State{}
	if fs.Path == "" {
		return st, errors.New("store path is required")
	}
	if err := os.MkdirAll(filepath.Dir(fs.Path), 0o755); err != nil {
		return st, err
	}
	f, err := os.OpenFile(fs.Path, os.O_RDONLY|os.O_CREATE, 0o600)
	if err != nil {
		return st, err
	}
	defer f.Close()
	if err := syscall.Flock(int(f.Fd()), syscall.LOCK_SH); err != nil {
		return st, err
	}
	defer func() { _ = syscall.Flock(int(f.Fd()), syscall.LOCK_UN) }()

	b, err := io.ReadAll(f)
	if err != nil {
		return st, err
	}
	if len(b) == 0 {
		return st, nil
	}
	if err := json.Unmarshal(b, &st); err != nil {
		return st, err
	}
	return st, nil
}

func (fs FileStore) Save(st State) error {
	if fs.Path == "" {
		return errors.New("store path is required")
	}
	if err := os.MkdirAll(filepath.Dir(fs.Path), 0o755); err != nil {
		return err
	}
	lock, err := os.OpenFile(fs.Path+".lock", os.O_RDWR|os.O_CREATE, 0o600)
	if err != nil {
		return err
	}
	defer lock.Close()
	if err := syscall.Flock(int(lock.Fd()), syscall.LOCK_EX); err != nil {
		return err
	}
	defer func() { _ = syscall.Flock(int(lock.Fd()), syscall.LOCK_UN) }()

	b, err := json.MarshalIndent(st, "", "  ")
	if err != nil {
		return err
	}

	tmp := fs.Path + ".tmp"
	if err := os.WriteFile(tmp, b, 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, fs.Path)
}
