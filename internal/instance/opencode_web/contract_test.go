package opencode_web

import (
	"bufio"
	"io"
	"net/http"
	"os"
	"os/exec"
	"strings"
	"testing"
	"time"
)

// TestOpencodeUpstreamContract verifies the upstream opencode behaviors the
// reverse proxy depends on (WORKTREE-ISOLATION.md §2): the `--version`
// output is parseable by parseVersion, and the served SPA homepage is
// injectable — it has a <head> and a CSP whose script-src carries the
// 'wasm-unsafe-eval' token that fixProxyHTML appends its hash after.
//
// OPT-IN: it spawns a real `opencode serve` subprocess, so it is skipped
// unless OPENCODE_CONTRACT=1 is set (and an opencode binary is found). Run:
//
//	OPENCODE_CONTRACT=1 go test ./internal/instance/opencode_web/ -run TestOpencodeUpstreamContract -v
func TestOpencodeUpstreamContract(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping opencode upstream contract check in -short mode")
	}
	if os.Getenv("OPENCODE_CONTRACT") != "1" {
		t.Skip("opencode upstream contract check is opt-in: set OPENCODE_CONTRACT=1")
	}
	bin, err := exec.LookPath("opencode")
	if err != nil {
		t.Skip("opencode binary not found on PATH; skipping upstream contract check")
	}

	// 1. --version must be parseable.
	verOut, err := exec.Command(bin, "--version").Output()
	if err != nil {
		t.Fatalf("opencode --version: %v", err)
	}
	if v, ok := parseVersion(string(verOut)); !ok {
		t.Fatalf("parseVersion(%q) failed — version output format changed?", strings.TrimSpace(string(verOut)))
	} else {
		t.Logf("opencode version: %s (supported=%v)", v, isSupportedVersion(v))
	}

	// 2. Spawn `opencode serve` on an isolated data dir and probe the homepage.
	dataDir := t.TempDir()
	wt := t.TempDir()
	cmd := exec.Command(bin, "serve", "--hostname", "127.0.0.1", "--port", "0")
	cmd.Dir = wt
	cmd.Env = append(os.Environ(), "XDG_DATA_HOME="+dataDir, "OPENCODE_SERVER_PASSWORD=contract-test-token")
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	cmd.Stderr = cmd.Stdout
	if err := cmd.Start(); err != nil {
		t.Fatalf("opencode serve: %v", err)
	}
	defer func() {
		_ = cmd.Process.Kill()
		_ = cmd.Wait()
	}()

	var host, port string
	deadline := time.Now().Add(20 * time.Second)
	sc := bufio.NewScanner(stdout)
	listening := make(chan struct{})
	go func() {
		for sc.Scan() {
			if h, p, ok := extractListeningAddress(sc.Text()); ok {
				host, port = h, p
				close(listening)
				return
			}
		}
	}()

	select {
	case <-listening:
	case <-time.After(deadline.Sub(time.Now())):
		t.Fatalf("opencode serve did not print listening address within 20s")
	}

	url := "http://" + host + ":" + port + "/"
	req, _ := http.NewRequest(http.MethodGet, url, nil)
	req.SetBasicAuth("opencode", "contract-test-token")
	client := &http.Client{Timeout: 5 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		t.Fatalf("GET %s: %v", url, err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)

	csp := resp.Header.Get("Content-Security-Policy")
	if !strings.Contains(csp, "'wasm-unsafe-eval'") {
		t.Fatalf("CSP missing 'wasm-unsafe-eval' anchor: %q", csp)
	}
	if !strings.Contains(string(body), "<head>") {
		t.Fatal("homepage missing <head> — fixProxyHTML injection anchor gone")
	}
}
