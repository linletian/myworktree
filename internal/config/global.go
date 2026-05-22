package config

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
)

type GlobalConfig struct {
	AuthToken string `json:"auth_token"`
}

func (c *GlobalConfig) Copy() *GlobalConfig {
	if c == nil {
		return &GlobalConfig{}
	}
	return &GlobalConfig{AuthToken: c.AuthToken}
}

var configPath = defaultConfigPath

func defaultConfigPath() (string, error) {
	base, err := os.UserConfigDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(base, "myworktree", "auth.json"), nil
}

func Load() (*GlobalConfig, error) {
	path, err := configPath()
	if err != nil {
		return &GlobalConfig{}, err
	}
	data, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return &GlobalConfig{}, nil
		}
		return &GlobalConfig{}, fmt.Errorf("config: failed to read auth.json: %w", err)
	}
	var cfg GlobalConfig
	if err := json.Unmarshal(data, &cfg); err != nil {
		return &GlobalConfig{}, fmt.Errorf("config: failed to parse auth.json: %w", err)
	}
	return &cfg, nil
}

func Save(cfg *GlobalConfig) error {
	if cfg == nil {
		cfg = &GlobalConfig{}
	}
	path, err := configPath()
	if err != nil {
		return err
	}
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	data, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		return err
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, data, 0o600); err != nil {
		return err
	}
	if err := os.Rename(tmp, path); err != nil {
		return err
	}
	return os.Chmod(path, 0o600)
}