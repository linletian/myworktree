package config

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

func testConfigPath(tmpDir string) func() (string, error) {
	return func() (string, error) {
		return filepath.Join(tmpDir, "myworktree", "auth.json"), nil
	}
}

func TestLoad_FileNotExist(t *testing.T) {
	tmpDir := t.TempDir()
	oldConfigPath := configPath
	configPath = testConfigPath(tmpDir)
	defer func() { configPath = oldConfigPath }()

	cfg, err := Load()
	if err != nil {
		t.Fatalf("expected nil error, got %v", err)
	}
	if cfg == nil {
		t.Fatal("expected non-nil config")
	}
	if cfg.AuthToken != "" {
		t.Fatalf("expected empty AuthToken, got %q", cfg.AuthToken)
	}
}

func TestLoad_JSONCorrupted(t *testing.T) {
	tmpDir := t.TempDir()
	oldConfigPath := configPath
	configPath = testConfigPath(tmpDir)
	defer func() { configPath = oldConfigPath }()

	path := filepath.Join(tmpDir, "myworktree", "auth.json")
	os.MkdirAll(filepath.Dir(path), 0o755)
	if err := os.WriteFile(path, []byte("invalid json"), 0o600); err != nil {
		t.Fatal(err)
	}

	cfg, err := Load()
	if err == nil {
		t.Fatal("expected non-nil error")
	}
	if cfg == nil {
		t.Fatal("expected non-nil config")
	}
	if cfg.AuthToken != "" {
		t.Fatalf("expected empty AuthToken, got %q", cfg.AuthToken)
	}
}

func TestLoad_JSONCorrupted_DoesNotPanic(t *testing.T) {
	tmpDir := t.TempDir()
	oldConfigPath := configPath
	configPath = testConfigPath(tmpDir)
	defer func() { configPath = oldConfigPath }()

	path := filepath.Join(tmpDir, "myworktree", "auth.json")
	os.MkdirAll(filepath.Dir(path), 0o755)
	if err := os.WriteFile(path, []byte("{"), 0o600); err != nil {
		t.Fatal(err)
	}

	defer func() {
		if r := recover(); r != nil {
			t.Errorf("Load panicked: %v", r)
		}
	}()
	Load()
}

func TestSave_AtomicWrite(t *testing.T) {
	tmpDir := t.TempDir()
	oldConfigPath := configPath
	configPath = testConfigPath(tmpDir)
	defer func() { configPath = oldConfigPath }()

	cfg := &GlobalConfig{AuthToken: "test-token-123"}
	if err := Save(cfg); err != nil {
		t.Fatalf("Save failed: %v", err)
	}

	path := filepath.Join(tmpDir, "myworktree", "auth.json")
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("failed to read auth.json: %v", err)
	}
	var loaded GlobalConfig
	if err := json.Unmarshal(data, &loaded); err != nil {
		t.Fatalf("auth.json is not valid JSON: %v", err)
	}
	if loaded.AuthToken != "test-token-123" {
		t.Fatalf("expected AuthToken %q, got %q", "test-token-123", loaded.AuthToken)
	}
}

func TestSave_FilePermissions(t *testing.T) {
	tmpDir := t.TempDir()
	oldConfigPath := configPath
	configPath = testConfigPath(tmpDir)
	defer func() { configPath = oldConfigPath }()

	cfg := &GlobalConfig{AuthToken: "test-token-123"}
	if err := Save(cfg); err != nil {
		t.Fatalf("Save failed: %v", err)
	}

	path := filepath.Join(tmpDir, "myworktree", "auth.json")
	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("failed to stat auth.json: %v", err)
	}
	if info.Mode().Perm() != 0o600 {
		t.Fatalf("expected permissions 0o600, got %o", info.Mode().Perm())
	}
}

func TestSave_Consistency(t *testing.T) {
	tmpDir := t.TempDir()
	oldConfigPath := configPath
	configPath = testConfigPath(tmpDir)
	defer func() { configPath = oldConfigPath }()

	token := "consistent-token-abc"
	for i := 0; i < 5; i++ {
		cfg := &GlobalConfig{AuthToken: token}
		if err := Save(cfg); err != nil {
			t.Fatalf("Save iteration %d failed: %v", i, err)
		}
		cfg2, err := Load()
		if err != nil {
			t.Fatalf("Load iteration %d failed: %v", i, err)
		}
		if cfg2.AuthToken != token {
			t.Fatalf("iteration %d: expected %q, got %q", i, token, cfg2.AuthToken)
		}
	}
}

func TestCopy(t *testing.T) {
	cfg := &GlobalConfig{AuthToken: "original"}
	copy := cfg.Copy()
	if copy.AuthToken != cfg.AuthToken {
		t.Fatalf("expected %q, got %q", cfg.AuthToken, copy.AuthToken)
	}
	copy.AuthToken = "modified"
	if cfg.AuthToken == "modified" {
		t.Fatal("Copy did not create independent copy")
	}
}

func TestCopy_Nil(t *testing.T) {
	var cfg *GlobalConfig
	copy := cfg.Copy()
	if copy == nil {
		t.Fatal("Copy of nil should return empty config, not nil")
	}
	if copy.AuthToken != "" {
		t.Fatalf("expected empty AuthToken, got %q", copy.AuthToken)
	}
}
