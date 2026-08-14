package opencode_web

import (
	"context"
	"encoding/json"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"myworktree/internal/framework"
	"myworktree/internal/store"
)

func TestStartOpencodeWebEndToEnd(t *testing.T) {
	mockSrc := filepath.Join("testdata", "opencode-mock.go")
	mockBin := filepath.Join(t.TempDir(), "opencode")
	if _, err := exec.LookPath("go"); err != nil {
		t.Skip("go not available")
	}

	build := exec.Command("go", "build", "-o", mockBin, mockSrc)
	out, err := build.CombinedOutput()
	if err != nil {
		t.Fatalf("build mock opencode: %v\n%s", err, out)
	}

	wtPath := filepath.Join(t.TempDir(), "wt")
	if err := os.MkdirAll(wtPath, 0o755); err != nil {
		t.Fatal(err)
	}

	dir := t.TempDir()
	storePath := filepath.Join(dir, "state.json")
	fs := store.FileStore{Path: storePath}
	if err := fs.Save(store.State{
		Worktrees: []store.ManagedWorktree{{ID: "wt1", Name: "wt1", Path: wtPath}},
	}); err != nil {
		t.Fatal(err)
	}

	reg := framework.NewRegistry()
	reg.Register(Driver{})
	mgr := framework.NewManager(reg, fs, nil)
	mgr.DataDir = dir
	mgr.Root = wtPath
	mgr.AuthToken = "test-token"

	t.Setenv("PATH", filepath.Dir(mockBin)+string(os.PathListSeparator)+os.Getenv("PATH"))

	inst, err := mgr.Start(context.Background(), framework.StartParams{
		WorktreeID: "wt1",
		Kind:       "opencode-web",
		Name:       "e2e-test",
	})
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	defer func() { _ = mgr.Stop(inst.ID) }()

	var port string
	deadline := time.Now().Add(15 * time.Second)
	for time.Now().Before(deadline) {
		got, err := mgr.Get(inst.ID)
		if err != nil {
			continue
		}
		if got.Status == "running" {
			var b Blob
			if json.Unmarshal(got.KindBlob, &b) == nil && b.Port != "" {
				port = b.Port
				break
			}
		}
		if got.Status == "failed" {
			t.Fatalf("instance failed: last_error=%s", got.LastError)
		}
		time.Sleep(300 * time.Millisecond)
	}

	if port == "" {
		t.Fatalf("port not populated within 15s")
	}

	resp, err := http.Get("http://127.0.0.1:" + port + "/global/health")
	if err != nil {
		t.Fatalf("health GET: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("health status = %d, want 200", resp.StatusCode)
	}
}
