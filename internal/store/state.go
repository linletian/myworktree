package store

import (
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"syscall"
)

var ErrVersionConflict = errors.New("version conflict")

type State struct {
	Worktrees []ManagedWorktree   `json:"worktrees"`
	Instances []ManagedInstance   `json:"instances"`
	TabOrder  map[string][]string `json:"tab_order"` // key=worktree_id, value=ordered instance IDs
	Version   int64               `json:"version"`
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
	ID            string            `json:"id"`
	WorktreeID    string            `json:"worktree_id"`
	WorktreeName  string            `json:"worktree_name,omitempty"`
	TagID         string            `json:"tag_id"`
	Name          string            `json:"name"`
	Labels        map[string]string `json:"labels,omitempty"`
	Command       string            `json:"command"`
	Cwd           string            `json:"cwd"`
	Env           map[string]string `json:"env,omitempty"`
	PID           int               `json:"pid"`
	Status        string            `json:"status"` // running|exited|stopped|failed
	Archived      bool              `json:"archived,omitempty"`
	ArchivedAt    string            `json:"archived_at,omitempty"`
	RestartedFrom string            `json:"restarted_from,omitempty"`
	RestartedTo   string            `json:"restarted_to,omitempty"`
	LogPath       string            `json:"log_path"`
	CreatedAt     string            `json:"created_at"`
	StoppedAt     string            `json:"stopped_at,omitempty"`
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

// SaveWithVersion saves only if the current version matches expectedVersion.
// Use expectedVersion < 0 to skip version check.
func (fs FileStore) SaveWithVersion(st State, expectedVersion int64) error {
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

	// Re-load under exclusive lock to check version.
	current, err := fs.Load()
	if err != nil {
		return err
	}
	if expectedVersion >= 0 && current.Version != expectedVersion {
		return ErrVersionConflict
	}

	st.Version = current.Version + 1
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
