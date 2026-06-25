// Package isolation_check provides a single smoke test that exercises
// the framework + every registered kind end-to-end. It lives in its
// own package so it can import both framework and the kinds (the
// kinds themselves cannot import a test in their dependency).
package isolation_check

import (
	"sort"
	"testing"

	"myworktree/internal/framework"
	_ "myworktree/internal/instance/opencode_web"
	_ "myworktree/internal/instance/pty"
)

// TestKindsRegistered verifies that every kind package registers
// itself with the framework's global registry on init().
func TestKindsRegistered(t *testing.T) {
	got := framework.Names()
	sort.Strings(got)
	want := []string{"opencode-web", "pty"}
	if len(got) != len(want) {
		t.Fatalf("got %v, want %v", got, want)
	}
	for i, n := range want {
		if got[i] != n {
			t.Fatalf("kind[%d] = %q, want %q (full: %v)", i, got[i], n, got)
		}
	}
}

// TestKindsManifests verifies that each registered kind returns a
// Manifest() with the expected Name field. Catches accidental
// renames or duplicate registrations.
func TestKindsManifests(t *testing.T) {
	for _, name := range []string{"pty", "opencode-web"} {
		k, err := framework.Get(name)
		if err != nil {
			t.Fatalf("Get(%q): %v", name, err)
		}
		m := k.Manifest()
		if m.Name != name {
			t.Fatalf("manifest Name = %q, want %q", m.Name, name)
		}
		if m.Label == "" {
			t.Errorf("manifest Label empty for %q", name)
		}
	}
}
