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
	Protocol   string `json:"protocol"` // "openai", "anthropic", "openai_compatible"
	APIKey     string `json:"api_key"`
	APIAddress string `json:"api_address"` // custom endpoint URL
	Model      string `json:"model"`       // model name (e.g., "gpt-4o-mini", "abab6.5s-chat")

	// Deprecated: Mode is kept for migration from old config files.
	// Use Protocol instead. This field is not serialized.
	Mode string `json:"-"`
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
		return &Config{}
	}
	return &Config{Protocol: c.Protocol, APIKey: c.APIKey, APIAddress: c.APIAddress, Model: c.Model}
}

// Save persists the global LLM configuration to the config file.
// The file is stored at ~/.config/myworktree/config.json with 0o600 permissions.
func Save(cfg *Config) error {
	if cfg == nil {
		cfg = &Config{}
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
		return &Config{}
	}
	data, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return &Config{}
		}
		return &Config{}
	}
	// Try to unmarshal into a struct that has both old "mode" and new "protocol" fields.
	var raw struct {
		Mode string `json:"mode"`
	}
	if err := json.Unmarshal(data, &raw); err == nil && raw.Mode != "" {
		// Old format: migrate Mode to Protocol
		cfg := &Config{Protocol: raw.Mode}
		_ = json.Unmarshal(data, cfg) // load api_key and api_address
		return cfg
	}
	var cfg Config
	if err := json.Unmarshal(data, &cfg); err != nil {
		return &Config{}
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
		cfg.Protocol = "openai"
		cfg.APIAddress = DefaultAddress("openai")
		return
	}
	if anthropicKey != "" {
		cfg.APIKey = anthropicKey
		cfg.Protocol = "anthropic"
		cfg.APIAddress = DefaultAddress("anthropic")
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

// CurrentMode returns the current LLM protocol string.
func CurrentMode() string {
	cfg := Load()
	return cfg.Protocol
}

// IsAvailable returns true if a protocol is set and API key is configured.
func IsAvailable() bool {
	cfg := Load()
	return cfg.Protocol != "" && cfg.APIKey != ""
}

// DefaultModel returns the default model name for the given protocol.
func DefaultModel(protocol string) string {
	switch protocol {
	case "openai":
		return "gpt-4o-mini"
	case "anthropic":
		return "claude-3-5-haiku-20241022"
	case "openai_compatible":
		return ""
	default:
		return ""
	}
}

// DefaultAddress returns the default API endpoint URL for the given protocol.
func DefaultAddress(protocol string) string {
	switch protocol {
	case "openai":
		return "https://api.openai.com/v1/chat/completions"
	case "anthropic":
		return "https://api.anthropic.com/v1/messages"
	case "openai_compatible":
		return ""
	default:
		return ""
	}
}
