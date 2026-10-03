package ui

import (
	"os"
	"os/exec"
	"regexp"
	"strconv"
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

// TestTerminalStatusChangelogCount keeps the CHANGELOG's coverage claim honest.
// The entry quotes the number of `node --test` cases, and that number drifted
// wrong twice while the cases were still being added (17, then 22, then 26 for
// a file holding 27) — a hand-maintained count in prose is a claim nobody
// checks. Derive it here instead: count the cases, then require the entry to
// say the same thing.
func TestTerminalStatusChangelogCount(t *testing.T) {
	src, err := os.ReadFile("testdata/terminal_status.test.mjs")
	if err != nil {
		t.Fatalf("read testdata/terminal_status.test.mjs: %v", err)
	}
	cases := len(regexp.MustCompile(`(?m)^test\(`).FindAll(src, -1))
	if cases == 0 {
		t.Fatal("no node --test cases found; the slice anchors probably moved")
	}

	changelog, err := os.ReadFile("../../CHANGELOG.md")
	if err != nil {
		t.Fatalf("read CHANGELOG.md: %v", err)
	}
	claimed := regexp.MustCompile(`Pinned by (\d+) ` + "`" + `node --test` + "`" + ` cases`).FindSubmatch(changelog)
	if claimed == nil {
		t.Fatal("CHANGELOG.md no longer says \"Pinned by N `node --test` cases\"")
	}
	if got, err := strconv.Atoi(string(claimed[1])); err != nil || got != cases {
		t.Fatalf("CHANGELOG.md claims %s `node --test` cases but testdata/terminal_status.test.mjs has %d",
			claimed[1], cases)
	}
}
