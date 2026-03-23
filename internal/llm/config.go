package llm

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"sync"
)

// Config holds the LLM configuration.
type Config struct {
	Mode   string `json:"mode"`   // "regex", "openai", or "anthropic"
	APIKey string `json:"api_key"`
}

var (
	globalCfg     *Config
	globalCfgOnce sync.Once
)

// Load returns a copy of the current LLM configuration.
// Environment variables take precedence over the config file.
func Load() *Config {
	globalCfgOnce.Do(func() {
		globalCfg = loadFromFile()
		overrideFromEnv(globalCfg)
	})
	// Return a copy so callers hold an independent snapshot.
	// Save() replaces globalCfg to point to a new object, but existing
	// callers are unaffected because they work on their own copy.
	return globalCfg.Copy()
}

// Copy returns a deep copy of the config.
func (c *Config) Copy() *Config {
	if c == nil {
		return &Config{Mode: "regex"}
	}
	return &Config{Mode: c.Mode, APIKey: c.APIKey}
}

// Save persists the global LLM configuration to the config file.
// The file is stored at ~/.config/myworktree/config.json with 0o600 permissions.
func Save(cfg *Config) error {
	if cfg == nil {
		cfg = &Config{Mode: "regex"}
	}
	// Work on a copy so callers holding the original are unaffected.
	pc := cfg.Copy()
	if err := saveToFile(pc); err != nil {
		return err
	}
	globalCfg = pc
	return nil
}

// Path returns the config file path.
func Path() (string, error) {
	base, err := os.UserConfigDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(base, "myworktree", "config.json"), nil
}

func loadFromFile() *Config {
	path, err := Path()
	if err != nil {
		return &Config{Mode: "regex"}
	}
	data, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return &Config{Mode: "regex"}
		}
		return &Config{Mode: "regex"}
	}
	var cfg Config
	if err := json.Unmarshal(data, &cfg); err != nil {
		return &Config{Mode: "regex"}
	}
	if cfg.Mode == "" {
		cfg.Mode = "regex"
	}
	return &cfg
}

func saveToFile(cfg *Config) error {
	path, err := Path()
	if err != nil {
		return err
	}
	// Ensure directory exists
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	// Write to temp file then rename for atomic write
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
	// Enforce permissions on the final file
	return os.Chmod(path, 0o600)
}

// overrideFromEnv applies environment variable overrides.
// Only one provider key should be set at a time; if both are set, OpenAI wins.
// Both keys are not expected to coexist in normal usage.
func overrideFromEnv(cfg *Config) {
	openAIKey := os.Getenv("OPENAI_API_KEY")
	anthropicKey := os.Getenv("ANTHROPIC_API_KEY")

	if openAIKey != "" {
		cfg.APIKey = openAIKey
		cfg.Mode = "openai"
		return
	}
	if anthropicKey != "" {
		cfg.APIKey = anthropicKey
		cfg.Mode = "anthropic"
	}
}

// MaskKey returns a masked version of an API key: first 3 chars + "***" + last 3 chars.
// If the key is shorter than 6 chars, returns "***".
func MaskKey(key string) string {
	if len(key) < 6 {
		return "***"
	}
	return key[:3] + "***" + key[len(key)-3:]
}

// CurrentMode returns the current LLM mode string.
func CurrentMode() string {
	cfg := Load()
	return cfg.Mode
}

// IsAvailable returns true if LLM mode is set and API key is configured.
func IsAvailable() bool {
	cfg := Load()
	if cfg.Mode == "regex" {
		return false
	}
	return cfg.APIKey != ""
}
