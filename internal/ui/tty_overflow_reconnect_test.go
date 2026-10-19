package ui

import (
	"os/exec"
	"testing"
)

// TestTTYOverflowReconnectHandling runs the Node unit tests for the issue
// #114 Phase B client contract (testdata/tty_overflow_reconnect.test.mjs):
// the exponential reconnect backoff with its visible/hidden caps and the
// closing-sync 60s reset anchor (decision 2); the overflow-notice handling
// (whitelist + fence + reset + visible status line + cursorless tail
// reconnect, decision 1/R6); the generation+fencing of late frames and
// in-flight inflate output; the app-layer `compress` consumption pipeline
// behind its ready-frame gate with the R16 plaintext degradation (decision
// 5a); and the two client-side guards — the sliding-window inflation-ratio
// guard (B-1.5, rework W7/W13) and the 2MB backlog (B-1.6).
//
// Same arrangement as TestTerminalStatusHandling: the cases slice the
// shipped index.html and run against stubbed browser state, this wrapper
// makes `go test` enforce them, and the file lives in testdata/ because
// static/* is embedded and served to every browser. Skips when node is
// absent.
func TestTTYOverflowReconnectHandling(t *testing.T) {
	node, err := exec.LookPath("node")
	if err != nil {
		t.Skip("node not on PATH; skipping tty overflow/reconnect JS tests")
	}
	out, err := exec.Command(node, "--test", "testdata/tty_overflow_reconnect.test.mjs").CombinedOutput()
	if err != nil {
		t.Fatalf("node --test testdata/tty_overflow_reconnect.test.mjs: %v\n%s", err, out)
	}
	t.Logf("%s", out)
}
