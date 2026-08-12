package reasonix

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

// TestReasonixUpstreamContract verifies the upstream reasonix behavior
// contract the driver depends on (docs/ARCHITECTURE.md §4.2): version gate,
// per-cwd session layout, fresh-session uniqueness, and per-session-file
// lease mutual exclusion.
//
// The probes live in scripts/verify-reasonix-contract.sh (single source of
// truth) and run against a REAL reasonix binary in an isolated temporary
// home — real user sessions are never touched.
//
// This test is OPT-IN: it spawns real `reasonix serve` subprocesses (port
// binding, up to ~40s worst-case waits), which is heavier than ordinary unit
// tests and depends on bash, so it is skipped unless REASONIX_CONTRACT=1 is
// set (and a reasonix binary is found). Run it with:
//
//	REASONIX_CONTRACT=1 go test ./internal/instance/reasonix/ -run TestReasonixUpstreamContract -v
//
// `go test -short` always skips it. CI invokes it explicitly in go-ci.yml and
// in .github/workflows/reasonix-contract.yml; runners without a reasonix
// binary skip quietly (green), ones with one run the check for real.
func TestReasonixUpstreamContract(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping reasonix upstream contract check in -short mode")
	}
	if os.Getenv("REASONIX_CONTRACT") != "1" {
		t.Skip("reasonix upstream contract check is opt-in: set REASONIX_CONTRACT=1 to run it")
	}

	bin := os.Getenv("REASONIX_BIN")
	if bin == "" {
		bin, _ = exec.LookPath("reasonix")
	}
	if bin == "" {
		t.Skip("reasonix binary not found on PATH (set REASONIX_BIN); skipping upstream contract check")
	}

	// The script lives at <repo root>/scripts/verify-reasonix-contract.sh;
	// `go test` runs with the package dir as cwd (internal/instance/reasonix).
	script := filepath.Join("..", "..", "..", "scripts", "verify-reasonix-contract.sh")
	if _, err := os.Stat(script); err != nil {
		t.Fatalf("contract script missing at %s: %v", script, err)
	}

	cmd := exec.Command("bash", script)
	// Pin the binary we found so the script does not re-resolve a different
	// one from its own PATH.
	cmd.Env = append(os.Environ(), "REASONIX_BIN="+bin)
	out, err := cmd.CombinedOutput()
	t.Logf("verify-reasonix-contract.sh output:\n%s", out)
	if err != nil {
		t.Fatalf("reasonix upstream contract check FAILED (a reasonix upgrade broke the contract?): %v\n%s", err, out)
	}
}
