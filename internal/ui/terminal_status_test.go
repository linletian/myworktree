package ui

import (
	"os/exec"
	"testing"
)

// TestTerminalStatusHandling runs the Node unit tests for the instance-status
// bucketing and the reconcile/promotion loop (testdata/terminal_status.test.mjs).
//
// Those are browser JS, so their logic — a `starting` instance must neither be
// painted as stopped nor be stranded without a transport (issue #80) — is
// covered there against stubbed page state, with the functions sliced out of
// the shipped index.html / kinds/pty.js rather than copied. This wrapper makes
// `go test` enforce it. The file lives in testdata/ instead of static/ because
// static/* is embedded and served to every browser. Skips when node is absent.
func TestTerminalStatusHandling(t *testing.T) {
	node, err := exec.LookPath("node")
	if err != nil {
		t.Skip("node not on PATH; skipping terminal status JS tests")
	}
	out, err := exec.Command(node, "--test", "testdata/terminal_status.test.mjs").CombinedOutput()
	if err != nil {
		t.Fatalf("node --test testdata/terminal_status.test.mjs: %v\n%s", err, out)
	}
	t.Logf("%s", out)
}
