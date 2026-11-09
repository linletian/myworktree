package ui

import (
	"os/exec"
	"testing"
)

// TestPreviewAdaptiveRenderHandling runs the issue #144 front-end tests:
// preflight limits, adaptive chunk sizing, safe excerpt bounds, and the shipped
// interruptible render-loop anchors. The pure helpers are sliced from
// static/preview.html so the tests run against the served bytes. Skips when
// node is absent.
func TestPreviewAdaptiveRenderHandling(t *testing.T) {
	node, err := exec.LookPath("node")
	if err != nil {
		t.Skip("node not on PATH; skipping preview adaptive render JS tests")
	}
	out, err := exec.Command(node, "--test", "testdata/preview_adaptive_render.test.mjs").CombinedOutput()
	if err != nil {
		t.Fatalf("node --test testdata/preview_adaptive_render.test.mjs: %v\n%s", err, out)
	}
	t.Logf("%s", out)
}
