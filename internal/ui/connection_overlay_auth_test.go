package ui

import (
	"os/exec"
	"testing"
)

// TestConnectionOverlayAuthHandling runs the Node unit tests for the issue
// #137 connection-overlay split (testdata/connection_overlay_auth.test.mjs):
// a poll failure with 401/403 must render the "Session expired" state with a
// token-free /login action and stop every poller, while a network failure or
// 5xx keeps the pre-existing "Backend server unreachable. Retrying..." state
// and keeps polling.
//
// Those are browser JS, so the logic — including the sticky 429 rule and the
// cross-file stop signal kinds/dsh_web.js reads — is covered there against
// stubbed page state, with the functions sliced out of the shipped index.html
// rather than copied. This wrapper makes `go test` enforce it. The file lives
// in testdata/ instead of static/ because static/* is embedded and served to
// every browser. Skips when node is absent.
func TestConnectionOverlayAuthHandling(t *testing.T) {
	node, err := exec.LookPath("node")
	if err != nil {
		t.Skip("node not on PATH; skipping connection overlay JS tests")
	}
	out, err := exec.Command(node, "--test", "testdata/connection_overlay_auth.test.mjs").CombinedOutput()
	if err != nil {
		t.Fatalf("node --test testdata/connection_overlay_auth.test.mjs: %v\n%s", err, out)
	}
	t.Logf("%s", out)
}
