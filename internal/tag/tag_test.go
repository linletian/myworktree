package tag

import (
	"os"
	"path/filepath"
	"testing"
)

// TestDefaultOpencodeWeb_HasNoCommand verifies that the default
// opencode-web tag does NOT carry a Command field, since Manager.startOpencodeWeb
// ignores tag.Command for this kind (the binary invocation is hardcoded).
// A non-empty Command here would mislead users into thinking they can
// override the opencode CLI args via tags.json — which they cannot.
func TestDefaultOpencodeWeb_HasNoCommand(t *testing.T) {
	dir := t.TempDir()
	m := Manager{
		GlobalPath:  filepath.Join(dir, "global.json"),
		ProjectPath: filepath.Join(dir, "project.json"),
	}
	merged, err := m.LoadMerged()
	if err != nil {
		t.Fatalf("LoadMerged: %v", err)
	}
	tw, ok := merged["opencode-web"]
	if !ok {
		t.Fatalf("opencode-web missing from default tags; got %v", keys(merged))
	}
	if tw.Command != "" {
		t.Fatalf("default opencode-web tag has Command = %q, want empty (Manager hardcodes invocation)", tw.Command)
	}
}

// TestLoadMergedDefaultsSurviveExistingFile pins the fix for
// "unknown tag id: opencode-web": the built-in defaults must resolve even
// when the user's tags.json already exists (created before the defaults
// existed) and does not list them; user entries still override defaults.
func TestLoadMergedDefaultsSurviveExistingFile(t *testing.T) {
	dir := t.TempDir()
	global := filepath.Join(dir, "global.json")
	if err := os.WriteFile(global, []byte(`{"tags":[{"id":"docs","command":"echo custom"}]}`), 0o600); err != nil {
		t.Fatal(err)
	}
	m := Manager{
		GlobalPath:  global,
		ProjectPath: filepath.Join(dir, "project.json"),
	}
	merged, err := m.LoadMerged()
	if err != nil {
		t.Fatalf("LoadMerged: %v", err)
	}
	if tw, ok := merged["opencode-web"]; !ok {
		t.Fatalf("opencode-web default missing from merged tags of a pre-existing file; got %v", keys(merged))
	} else if tw.Command != "" {
		t.Fatalf("default opencode-web tag has Command = %q, want empty", tw.Command)
	}
	if got := merged["docs"].Command; got != "echo custom" {
		t.Fatalf("user-defined docs tag lost: Command = %q, want %q (user entries must override defaults)", got, "echo custom")
	}
	if _, ok := merged["dev"]; !ok {
		t.Fatalf("built-in dev default missing; got %v", keys(merged))
	}
}

func keys(m map[string]Tag) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	return out
}
