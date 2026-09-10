package dsh_web

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

// writeSessionLog creates a fake session dir under the watch root with
// the given log mtime (the real layout is
// <sessions>/<slug>/<sessionId>/session.jsonl.zstd).
func writeSessionLog(t *testing.T, root, slug, sessionID string, modTime time.Time) {
	t.Helper()
	dir := filepath.Join(root, slug, sessionID)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, "session.jsonl.zstd")
	if err := os.WriteFile(path, []byte("fake-zstd"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Chtimes(path, modTime, modTime); err != nil {
		t.Fatal(err)
	}
}

func TestSessionWatchScanForeignActive(t *testing.T) {
	root := t.TempDir()
	w := NewSessionWatch(root, nil)

	writeSessionLog(t, root, "--wt--", "session-fresh", time.Now())
	writeSessionLog(t, root, "--wt--", "session-idle", time.Now().Add(-2*time.Hour))
	// A non-session file in a slug dir must be ignored.
	if err := os.WriteFile(filepath.Join(root, "--wt--", "junk"), []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}

	if err := w.ScanNow(); err != nil {
		t.Fatal(err)
	}
	n, ids := w.ForeignActive()
	if n != 1 || len(ids) != 1 || ids[0] != "session-fresh" {
		t.Errorf("ForeignActive = %d %v, want 1 [session-fresh]", n, ids)
	}
}

func TestSessionWatchOwnAttribution(t *testing.T) {
	root := t.TempDir()
	w := NewSessionWatch(root, nil)
	writeSessionLog(t, root, "--wt--", "session-mine", time.Now())
	writeSessionLog(t, root, "--wt--", "session-theirs", time.Now())

	// Before attribution: both are foreign.
	if err := w.ScanNow(); err != nil {
		t.Fatal(err)
	}
	if n, _ := w.ForeignActive(); n != 2 {
		t.Fatalf("ForeignActive = %d, want 2 before attribution", n)
	}

	// Mark session-mine as driven by our instance → excluded.
	w.MarkOwn("session-mine")
	if err := w.ScanNow(); err != nil {
		t.Fatal(err)
	}
	n, ids := w.ForeignActive()
	if n != 1 || len(ids) != 1 || ids[0] != "session-theirs" {
		t.Errorf("ForeignActive = %d %v, want 1 [session-theirs]", n, ids)
	}

	// An own entry older than the own window expires → foreign again.
	w.mu.Lock()
	w.own["session-mine"] = time.Now().Add(-sessionOwnWindow - time.Minute)
	w.mu.Unlock()
	if err := w.ScanNow(); err != nil {
		t.Fatal(err)
	}
	if n, _ := w.ForeignActive(); n != 2 {
		t.Errorf("ForeignActive = %d, want 2 after own-window expiry", n)
	}
}

func TestSessionWatchMissingDir(t *testing.T) {
	w := NewSessionWatch(filepath.Join(t.TempDir(), "does-not-exist"), nil)
	if err := w.ScanNow(); err != nil {
		t.Fatalf("ScanNow on missing dir: %v", err)
	}
	if n, _ := w.ForeignActive(); n != 0 {
		t.Errorf("ForeignActive = %d, want 0", n)
	}
}

func TestDshSessionsDir(t *testing.T) {
	t.Setenv("DSH_HOME", "/custom/dsh")
	if got := DshSessionsDir(); got != filepath.Join("/custom/dsh", "sessions") {
		t.Errorf("DshSessionsDir with DSH_HOME = %q", got)
	}
	t.Setenv("DSH_HOME", "")
	home, err := os.UserHomeDir()
	if err != nil {
		t.Skip("no home dir")
	}
	if got := DshSessionsDir(); got != filepath.Join(home, ".dsh", "sessions") {
		t.Errorf("DshSessionsDir default = %q", got)
	}
}
