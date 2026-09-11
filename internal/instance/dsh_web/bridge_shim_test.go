package dsh_web

import (
	"os/exec"
	"testing"
)

// TestBridgeShim runs the Node unit tests for the injected WebSocket→SSE
// bridge shim (static/dsh-bridge.js). The shim is browser JS, so its logic
// (open gating, send queueing, close-code mapping, error teardown) is
// covered by static/dsh-bridge.test.mjs with stubbed browser APIs; this
// wrapper makes `go test` enforce it. Skips when node is not on PATH.
func TestBridgeShim(t *testing.T) {
	node, err := exec.LookPath("node")
	if err != nil {
		t.Skip("node not on PATH; skipping dsh-bridge shim JS tests")
	}
	out, err := exec.Command(node, "--test", "static/dsh-bridge.test.mjs").CombinedOutput()
	if err != nil {
		t.Fatalf("node --test static/dsh-bridge.test.mjs: %v\n%s", err, out)
	}
	t.Logf("%s", out)
}
