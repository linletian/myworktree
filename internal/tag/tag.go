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

func (m Manager) LoadMerged() (map[string]Tag, error) {
	merged := map[string]Tag{}
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
