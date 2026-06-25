package tag

import (
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

func keys(m map[string]Tag) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	return out
}