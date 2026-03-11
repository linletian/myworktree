package cli

import (
	"bytes"
	"io"
	"log"
	"os"
	"testing"

	"myworktree/internal/version"
)

func TestRunVersionSubcommand(t *testing.T) {
	oldVersion, oldCommit, oldBuildDate := version.Version, version.Commit, version.BuildDate
	version.Version = "v0.1.0"
	version.Commit = "1234567890abcdef"
	version.BuildDate = "2026-03-11T10:00:00Z"
	t.Cleanup(func() {
		version.Version = oldVersion
		version.Commit = oldCommit
		version.BuildDate = oldBuildDate
	})

	stdout := captureStdout(t)
	if code := Run([]string{"myworktree", "version"}, log.New(io.Discard, "", 0)); code != 0 {
		t.Fatalf("expected exit code 0, got %d", code)
	}
	if got := stdout(); got != "myworktree v0.1.0 (1234567890ab) built 2026-03-11T10:00:00Z\n" {
		t.Fatalf("unexpected stdout: %q", got)
	}
}

func TestRunVersionFlag(t *testing.T) {
	oldVersion, oldCommit, oldBuildDate := version.Version, version.Commit, version.BuildDate
	version.Version = "v0.1.0"
	version.Commit = "1234567890abcdef"
	version.BuildDate = "2026-03-11T10:00:00Z"
	t.Cleanup(func() {
		version.Version = oldVersion
		version.Commit = oldCommit
		version.BuildDate = oldBuildDate
	})

	stdout := captureStdout(t)
	if code := Run([]string{"mw", "--version"}, log.New(io.Discard, "", 0)); code != 0 {
		t.Fatalf("expected exit code 0, got %d", code)
	}
	if got := stdout(); got != "mw v0.1.0 (1234567890ab) built 2026-03-11T10:00:00Z\n" {
		t.Fatalf("unexpected stdout: %q", got)
	}
}

func captureStdout(t *testing.T) func() string {
	t.Helper()

	oldStdout := os.Stdout
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatalf("os.Pipe failed: %v", err)
	}
	os.Stdout = w

	return func() string {
		_ = w.Close()
		os.Stdout = oldStdout

		var buf bytes.Buffer
		if _, err := io.Copy(&buf, r); err != nil {
			t.Fatalf("copy stdout failed: %v", err)
		}
		_ = r.Close()
		return buf.String()
	}
}
