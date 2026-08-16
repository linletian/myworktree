package tag

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
)

// Tag config is JSON in MVP to keep myworktree dependency-free.
type Tag struct {
	ID       string            `json:"id"`
	Command  string            `json:"command"`
	Env      map[string]string `json:"env"`
	PreStart string            `json:"preStart"`
	Cwd      string            `json:"cwd"`
}

type File struct {
	Tags []Tag `json:"tags"`
}

type Manager struct {
	GlobalPath  string
	ProjectPath string
}

var defaultTags = []Tag{
	{ID: "docs", Command: "pwd"},
	{ID: "dev", Command: "pwd"},
	{ID: "review", Command: "pwd"},
	// opencode-web intentionally has no Command: Manager.startOpencodeWeb
	// hardcodes the invocation. Tag is used only as a label / reference key
	// for the instance, and Env remains empty so users can layer non-secret
	// env via tags.json without overwriting the forced auth token.
	{ID: "opencode-web"},
	// dsh-web: same contract — the invocation (`dsh web --host 127.0.0.1
	// --port 0 --patch <restrict.yml>`) is hardcoded by the dsh_web
	// driver; the tag is a label / env / preStart source only.
	{ID: "dsh-web"},
}

func (m Manager) ensureDefaults() error {
	if m.GlobalPath == "" {
		return nil
	}
	if _, err := os.Stat(m.GlobalPath); err == nil {
		return nil
	}
	dir := filepath.Dir(m.GlobalPath)
	if err := os.MkdirAll(dir, 0755); err != nil {
		return err
	}
	b, err := json.MarshalIndent(File{Tags: defaultTags}, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(m.GlobalPath, b, 0644)
}

// LoadMerged returns the effective tag set: the built-in default tags as
// the base, overlaid by the global file, then the project file (later
// layers win per id). The defaults are merged at load time — not only at
// file creation — so built-ins the product depends on (e.g. the
// command-less "opencode-web" reference tag) resolve even for users
// whose tags.json predates them. Unknown non-default tag ids still
// surface as "unknown tag id" at Start.
func (m Manager) LoadMerged() (map[string]Tag, error) {
	if err := m.ensureDefaults(); err != nil {
		return nil, err
	}
	merged := map[string]Tag{}
	for _, t := range defaultTags {
		if t.ID == "" {
			continue
		}
		merged[t.ID] = t
	}
	if err := loadInto(merged, m.GlobalPath); err != nil {
		return nil, err
	}
	if err := loadInto(merged, m.ProjectPath); err != nil {
		return nil, err
	}
	for id, t := range merged {
		if id == "" {
			return nil, errors.New("tag id cannot be empty")
		}
		if t.Env == nil {
			t.Env = map[string]string{}
			merged[id] = t
		}
	}
	return merged, nil
}

func loadInto(dst map[string]Tag, path string) error {
	if path == "" {
		return nil
	}
	b, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}
	var f File
	if err := json.Unmarshal(b, &f); err != nil {
		return err
	}
	for _, t := range f.Tags {
		if t.ID == "" {
			continue
		}
		if t.Cwd != "" {
			t.Cwd = filepath.Clean(t.Cwd)
		}
		dst[t.ID] = t
	}
	return nil
}
