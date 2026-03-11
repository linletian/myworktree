package instance

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

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
