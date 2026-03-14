package instance

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/creack/pty"
	"myworktree/internal/store"
)

func TestSanitizedEnv(t *testing.T) {
	got := sanitizedEnv(map[string]string{
		"API_TOKEN": "secret-token",
		"PATH":      "/usr/bin",
	})
	if got["API_TOKEN"] != "***" {
		t.Fatalf("API_TOKEN should be masked, got %q", got["API_TOKEN"])
	}
	if got["PATH"] != "/usr/bin" {
		t.Fatalf("PATH should be preserved, got %q", got["PATH"])
	}
	if sanitizedEnv(nil) != nil {
		t.Fatalf("nil input should return nil")
	}
}

func TestLogPathByID(t *testing.T) {
	st := store.State{
		Instances: []store.ManagedInstance{
			{ID: "a1", LogPath: "/tmp/a1.log"},
		},
	}
	if got := logPathByID(st, "a1"); got != "/tmp/a1.log" {
		t.Fatalf("unexpected log path: %q", got)
	}
	if got := logPathByID(st, "missing"); got != "" {
		t.Fatalf("missing id should return empty path, got %q", got)
	}
}

func TestEnforceMaxLogSize(t *testing.T) {
	t.Parallel()
	p := filepath.Join(t.TempDir(), "instance.log")
	content := "0123456789abcdefghijklmnopqrstuvwxyz"
	if err := os.WriteFile(p, []byte(content), 0o600); err != nil {
		t.Fatalf("write log failed: %v", err)
	}
	if err := enforceMaxLogSize(p, 10); err != nil {
		t.Fatalf("enforceMaxLogSize failed: %v", err)
	}
	b, err := os.ReadFile(p)
	if err != nil {
		t.Fatalf("read log failed: %v", err)
	}
	if string(b) != content[len(content)-10:] {
		t.Fatalf("unexpected trimmed log content: %q", string(b))
	}
}

func TestTerminatePIDInvalid(t *testing.T) {
	if err := terminatePID(0, 0); err == nil {
		t.Fatalf("expected invalid pid error")
	}
}

func TestMarkStopped(t *testing.T) {
	t.Parallel()
	path := filepath.Join(t.TempDir(), "state.json")
	fs := store.FileStore{Path: path}
	initial := store.State{
		Instances: []store.ManagedInstance{
			{ID: "ins1", Status: "running"},
		},
	}
	if err := fs.Save(initial); err != nil {
		t.Fatalf("save initial state failed: %v", err)
	}
	m := &Manager{Store: fs}
	if err := m.markStopped("ins1", "stopped"); err != nil {
		t.Fatalf("markStopped failed: %v", err)
	}
	st, err := fs.Load()
	if err != nil {
		t.Fatalf("load state failed: %v", err)
	}
	if st.Instances[0].Status != "stopped" {
		t.Fatalf("status not updated: %#v", st.Instances[0])
	}
	if strings.TrimSpace(st.Instances[0].StoppedAt) == "" {
		t.Fatalf("StoppedAt should be set")
	}
}

func TestResize_InvalidParams(t *testing.T) {
	m := &Manager{}

	// Test empty id
	if err := m.Resize("", 80, 24); err == nil {
		t.Fatalf("empty id should return error")
	}

	// Test whitespace-only id
	if err := m.Resize("   ", 80, 24); err == nil {
		t.Fatalf("whitespace-only id should return error")
	}

	// Test invalid cols
	if err := m.Resize("test-id", 0, 24); err == nil {
		t.Fatalf("zero cols should return error")
	}
	if err := m.Resize("test-id", -1, 24); err == nil {
		t.Fatalf("negative cols should return error")
	}

	// Test invalid rows
	if err := m.Resize("test-id", 80, 0); err == nil {
		t.Fatalf("zero rows should return error")
	}
	if err := m.Resize("test-id", 80, -1); err == nil {
		t.Fatalf("negative rows should return error")
	}
}

func TestResize_NotFound(t *testing.T) {
	m := &Manager{}
	if err := m.Resize("nonexistent-instance", 80, 24); err == nil {
		t.Fatalf("nonexistent instance should return error")
	}
}

func TestResize_Success(t *testing.T) {
	// Create a real PTY to test Resize functionality
	// Use bash which is available on both macOS and Linux
	cmd := exec.Command("bash", "-c", "sleep 60")
	ptmx, err := pty.Start(cmd)
	if err != nil {
		t.Fatalf("failed to start pty: %v", err)
	}
	defer ptmx.Close()
	defer cmd.Process.Kill()

	// Create a manager and inject the PTY
	m := &Manager{
		ptys: map[string]*os.File{
			"test-pty": ptmx,
		},
	}

	// Test successful resize
	if err := m.Resize("test-pty", 120, 40); err != nil {
		t.Fatalf("Resize failed: %v", err)
	}

	// Verify the size was set correctly
	// Note: pty.Getsize returns (rows, cols), not (cols, rows)
	rows, cols, err := pty.Getsize(ptmx)
	if err != nil {
		t.Fatalf("Getsize failed: %v", err)
	}
	if cols != 120 || rows != 40 {
		t.Fatalf("unexpected size: cols=%d, rows=%d, expected cols=120, rows=40", cols, rows)
	}
}
