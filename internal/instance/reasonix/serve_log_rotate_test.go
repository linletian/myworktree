package reasonix

import (
	"bytes"
	"errors"
	"io"
	"log"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// seedServeLog creates the instance's management dir and a serve.log whose
// content starts with marker and whose SIZE is exactly sizeBytes. The size is
// reached by truncating, so an over-cap fixture is sparse and costs nothing on
// disk while still reporting the real length to os.Stat — the only thing the
// rotation threshold reads.
func seedServeLog(t *testing.T, d *Driver, instanceID, marker string, sizeBytes int64) string {
	t.Helper()
	dir := d.dir(instanceID)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	logFile := filepath.Join(dir, "serve.log")
	if err := os.WriteFile(logFile, []byte(marker+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Truncate(logFile, sizeBytes); err != nil {
		t.Fatal(err)
	}
	return logFile
}

// readPrefix reads up to n bytes of a file and returns what it got. An empty
// file returns "" rather than an error: a freshly rotated serve.log IS empty
// (the fake serve writes nothing), and "empty" is the answer the caller wants
// to see, not a failure of the read. The marker lives at the head of a sparse
// fixture, so this never needs to pull a whole 10 MiB file into memory.
func readPrefix(t *testing.T, path string, n int) string {
	t.Helper()
	f, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	buf := make([]byte, n)
	nr, err := f.Read(buf)
	if err != nil && nr == 0 && !errors.Is(err, io.EOF) {
		t.Fatalf("read %s: %v", path, err)
	}
	return string(buf[:nr])
}

// readSuffix reads the LAST n bytes of a file (all of it when shorter). The
// symlink test needs the tail: a serve appends after the sparse hole of a
// multi-megabyte fixture, so readPrefix would hand back the head marker and a
// run of zeros and prove nothing about where the new bytes went.
func readSuffix(t *testing.T, path string, n int) string {
	t.Helper()
	f, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	fi, err := f.Stat()
	if err != nil {
		t.Fatal(err)
	}
	if int64(n) > fi.Size() {
		n = int(fi.Size())
	}
	if n == 0 {
		return ""
	}
	if _, err := f.Seek(-int64(n), io.SeekEnd); err != nil {
		t.Fatal(err)
	}
	buf := make([]byte, n)
	nr, err := f.Read(buf)
	if err != nil && nr == 0 && !errors.Is(err, io.EOF) {
		t.Fatalf("read %s: %v", path, err)
	}
	return string(buf[:nr])
}

// fakeServeEchoBin wraps an existing fake serve bin in a /bin/sh shim that
// echoes marker to stdout once, then execs the real fixture. The driver hands
// serve a stdout fd opened on serve.log (cmd.Stdout = f), so this is how a
// test gets bytes into the log without a real reasonix: a marker found at the
// END of the file is proof of an append through whatever name serve.log
// carries — including through a symlink.
//
// The echo is gated on --port-file so the shim stays silent for the version
// probe, whose output checkVersion parses for a bare x.y.z. Flushing is not a
// concern: dash and bash both flush stdout before exec, so the marker is on
// disk before the fixture writes its port file, hence before Start returns.
// marker must not contain a single quote (it is embedded in shell quotes).
func fakeServeEchoBin(t *testing.T, realBin, marker string) string {
	t.Helper()
	shim := filepath.Join(t.TempDir(), "echo-serve.sh")
	script := "#!/bin/sh\n" +
		"case \" $* \" in\n" +
		"  *\" --port-file \"*) printf '" + marker + "\\n' ;;\n" +
		"esac\n" +
		"exec '" + realBin + "' \"$@\"\n"
	if err := os.WriteFile(shim, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	return shim
}

// TestStartRotatesOversizedServeLog is the issue's own acceptance shape: a
// pre-seeded over-cap serve.log must be renamed to serve.log.1 holding the OLD
// log, and the serve.log the new process writes into must start fresh. The
// "new log does not contain the old marker" assertion is what makes this a
// rotation rather than an append that happened to rename something.
func TestStartRotatesOversizedServeLog(t *testing.T) {
	d := newFakeServeDriver(t)
	id := "inst-rotate"
	logFile := seedServeLog(t, d, id, "OLD-RUN-MARKER", serveLogMaxBytes+1)

	if _, err := d.Start(StartInput{InstanceID: id, WorktreePath: t.TempDir()}); err != nil {
		t.Fatalf("Start: %v", err)
	}
	t.Cleanup(func() { _ = d.Stop(id) })

	backup := logFile + ".1"
	bfi, err := os.Stat(backup)
	if err != nil {
		t.Fatalf("backup %s missing after Start: %v", backup, err)
	}
	if bfi.Size() != serveLogMaxBytes+1 {
		t.Errorf("backup size = %d, want %d (the rotated log must be kept whole)", bfi.Size(), serveLogMaxBytes+1)
	}
	if got := readPrefix(t, backup, 16); !strings.HasPrefix(got, "OLD-RUN-MARKER") {
		t.Errorf("backup head = %q, want the old log's marker", got)
	}

	lfi, err := os.Stat(logFile)
	if err != nil {
		t.Fatalf("fresh serve.log missing after Start: %v", err)
	}
	if got := readPrefix(t, logFile, 16); strings.Contains(got, "OLD-RUN-MARKER") {
		t.Errorf("new serve.log still holds the old log — it did not start from zero: %q", got)
	}
	if lfi.Size() > serveLogMaxBytes {
		t.Errorf("fresh serve.log is already over the cap: %d", lfi.Size())
	}
}

// TestStartLeavesUnderCapServeLogAlone pins the negative half: a log under the
// threshold is neither renamed nor truncated. The append-through assertion
// matters — a "rotation" implemented as O_TRUNC would silently destroy the
// previous run's log on every restart, which is data loss dressed up as
// hygiene.
func TestStartLeavesUnderCapServeLogAlone(t *testing.T) {
	d := newFakeServeDriver(t)
	id := "inst-small"
	logFile := seedServeLog(t, d, id, "SMALL-KEEP-MARKER", 4096)

	if _, err := d.Start(StartInput{InstanceID: id, WorktreePath: t.TempDir()}); err != nil {
		t.Fatalf("Start: %v", err)
	}
	t.Cleanup(func() { _ = d.Stop(id) })

	if _, err := os.Stat(logFile + ".1"); !os.IsNotExist(err) {
		t.Errorf("a backup appeared for an under-cap log: stat err = %v", err)
	}
	if got := readPrefix(t, logFile, 18); !strings.HasPrefix(got, "SMALL-KEEP-MARKER") {
		t.Errorf("under-cap serve.log was replaced, not appended to: head = %q", got)
	}
	fi, err := os.Stat(logFile)
	if err != nil {
		t.Fatal(err)
	}
	if fi.Size() < 4096 {
		t.Errorf("serve.log shrank to %d bytes; the previous run's log must survive", fi.Size())
	}
}

// TestStartRotationOverwritesExistingBackup pins the single-backup rule: the
// pre-existing .1 is replaced by the log that just rotated out, and nothing
// older than that survives. This is the case a plain "does .1 exist" test
// passes vacuously — here the OLD .1's marker must be gone.
func TestStartRotationOverwritesExistingBackup(t *testing.T) {
	d := newFakeServeDriver(t)
	id := "inst-rotate2"
	logFile := seedServeLog(t, d, id, "SECOND-ROTATION-MARKER", serveLogMaxBytes+1)
	backup := logFile + ".1"
	if err := os.WriteFile(backup, []byte("ANCIENT-BACKUP-MARKER\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	if _, err := d.Start(StartInput{InstanceID: id, WorktreePath: t.TempDir()}); err != nil {
		t.Fatalf("Start: %v", err)
	}
	t.Cleanup(func() { _ = d.Stop(id) })

	if got := readPrefix(t, backup, 22); !strings.HasPrefix(got, "SECOND-ROTATION-MARKER") {
		t.Errorf("backup head = %q, want the log that just rotated out", got)
	}
	if strings.Contains(readPrefix(t, backup, 64), "ANCIENT-BACKUP-MARKER") {
		t.Error("the old .1 survived; rotation must keep exactly one backup")
	}
	// Exactly one backup, no multi-level chain.
	if _, err := os.Stat(backup + ".1"); !os.IsNotExist(err) {
		t.Errorf("a serve.log.1.1 appeared; the scheme is single-backup by design (err = %v)", err)
	}
}

// TestRotatedServeLogKeepsSixHundred pins the credential exposure boundary.
// serve.log can carry tokens and session text, so BOTH the live log and the
// backup must be 0600: os.Rename preserves the inode's mode, and the fresh
// file is created 0600 by Start. A world-readable backup is a real leak, not
// a cosmetic miss.
func TestRotatedServeLogKeepsSixHundred(t *testing.T) {
	d := newFakeServeDriver(t)
	id := "inst-mode"
	logFile := seedServeLog(t, d, id, "MODE-MARKER", serveLogMaxBytes+1)

	if _, err := d.Start(StartInput{InstanceID: id, WorktreePath: t.TempDir()}); err != nil {
		t.Fatalf("Start: %v", err)
	}
	t.Cleanup(func() { _ = d.Stop(id) })

	for _, p := range []string{logFile, logFile + ".1"} {
		fi, err := os.Stat(p)
		if err != nil {
			t.Fatalf("stat %s: %v", p, err)
		}
		if perm := fi.Mode().Perm(); perm != 0o600 {
			t.Errorf("%s mode = %o, want 600", p, perm)
		}
	}
}

// TestRotateServeLogFailureDoesNotBlockStart pins the best-effort trade-off
// AND its visibility. An existing serve.log.1 that is a DIRECTORY makes
// os.Rename fail (EISDIR/ENOTEMPTY) regardless of who runs the test, so this
// drives the real failure path instead of simulating one. Start must still
// bring serve up — and must say out loud that it did not rotate, because a
// silent miss here is the bug this issue is about.
func TestRotateServeLogFailureDoesNotBlockStart(t *testing.T) {
	var buf bytes.Buffer
	d := newFakeServeDriver(t)
	d.Logger = log.New(&buf, "", 0)
	id := "inst-rotate-fail"
	logFile := seedServeLog(t, d, id, "STAYS-PUT-MARKER", serveLogMaxBytes+1)
	if err := os.MkdirAll(logFile+".1", 0o755); err != nil {
		t.Fatal(err)
	}

	if _, err := d.Start(StartInput{InstanceID: id, WorktreePath: t.TempDir()}); err != nil {
		t.Fatalf("Start must survive an un-rotatable log: %v", err)
	}
	t.Cleanup(func() { _ = d.Stop(id) })

	fi, err := os.Stat(logFile)
	if err != nil {
		t.Fatal(err)
	}
	if fi.Size() < serveLogMaxBytes+1 {
		t.Error("the oversized log was truncated when its rotation failed; it must be left alone")
	}
	out := buf.String()
	if !strings.Contains(out, "rotate serve.log") {
		t.Errorf("a failed rotation must be logged, got: %q", out)
	}
	if !strings.Contains(out, id) {
		t.Errorf("the rotation warning must name the instance, got: %q", out)
	}
}

// TestServeLogExactlyAtCapIsNotRotated pins the threshold's boundary: the rule
// is "over the cap", so a log of exactly serveLogMaxBytes stays put. The
// fixture is built at the boundary rather than derived from a `+1` like the
// other tests here, because every other fixture would still pass if the
// comparison flipped from <= to <.
func TestServeLogExactlyAtCapIsNotRotated(t *testing.T) {
	d := newFakeServeDriver(t)
	id := "inst-at-cap"
	logFile := seedServeLog(t, d, id, "EXACTLY-AT-CAP", serveLogMaxBytes)

	if _, err := d.Start(StartInput{InstanceID: id, WorktreePath: t.TempDir()}); err != nil {
		t.Fatalf("Start: %v", err)
	}
	t.Cleanup(func() { _ = d.Stop(id) })

	if _, err := os.Stat(logFile + ".1"); !os.IsNotExist(err) {
		t.Errorf("a log of exactly the cap was rotated (stat err = %v); the rule is strictly OVER the cap", err)
	}
	if got := readPrefix(t, logFile, 15); !strings.HasPrefix(got, "EXACTLY-AT-CAP") {
		t.Errorf("at-cap log was replaced: head = %q", got)
	}
}

// TestServeLogOneByteOverCapIsRotated is the other side of that boundary, so
// the two tests together pin the comparison itself and not just one side of it.
func TestServeLogOneByteOverCapIsRotated(t *testing.T) {
	d := newFakeServeDriver(t)
	id := "inst-over-cap"
	logFile := seedServeLog(t, d, id, "ONE-BYTE-OVER", serveLogMaxBytes+1)

	if _, err := d.Start(StartInput{InstanceID: id, WorktreePath: t.TempDir()}); err != nil {
		t.Fatalf("Start: %v", err)
	}
	t.Cleanup(func() { _ = d.Stop(id) })

	if _, err := os.Stat(logFile + ".1"); err != nil {
		t.Errorf("a log one byte over the cap was not rotated: %v", err)
	}
}

// TestStartLeavesSymlinkedServeLogAlone pins the os.Lstat in rotateServeLog.
// It is the only test here that puts a NON-regular entry at serve.log, which is
// the whole point of Lstat: Stat follows a symlink, so a rotateServeLog written
// against Stat compares the TARGET's size and then renames the LINK — check and
// move acting on different objects, which quietly destroys the user's pointer
// while the oversized log stays exactly where it was.
//
// The fixture is built to bite: the log lives OUTSIDE the management dir
// ("somewhere else"), serve.log is a symlink to it, and the target is one byte
// OVER the cap. So the Stat version sees a regular 10 MiB+1 file, decides to
// rotate, and renames serve.log — every assertion below then fails, none of
// them by luck:
//   - serve.log.1 appears (as the moved link),
//   - serve.log stops being a symlink (OpenFile recreates a plain file),
//   - the target never receives the new run's bytes (serve writes into the
//     freshly created file, not through the link),
//   - the target never grows past the seeded size.
//
// Under Lstat the symlink is not a regular file, rotateServeLog returns before
// the size comparison, and Start still brings serve up with its stdout landing
// in the target through the untouched link.
func TestStartLeavesSymlinkedServeLogAlone(t *testing.T) {
	const (
		headMarker  = "SYMLINK-TARGET-MARKER" // already in the target before Start
		runMarker   = "SYMLINK-RUN-OUTPUT"    // echoed by serve, appended through the link
		seededSize  = serveLogMaxBytes + 1    // over the cap, so a Stat-following rotate would fire
		seededPerms = 0o600                   // same posture as the real serve.log
	)
	d := newFakeServeDriver(t)
	d.ReasonixBin = fakeServeEchoBin(t, d.ReasonixBin, runMarker)
	id := "inst-symlink"

	dir := d.dir(id)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	// The log itself, elsewhere: sparse over-cap like seedServeLog, so the
	// fixture costs nothing on disk while reporting the real length to Stat.
	target := filepath.Join(t.TempDir(), "elsewhere.log")
	if err := os.WriteFile(target, []byte(headMarker+"\n"), seededPerms); err != nil {
		t.Fatal(err)
	}
	if err := os.Truncate(target, seededSize); err != nil {
		t.Fatal(err)
	}
	logFile := filepath.Join(dir, "serve.log")
	if err := os.Symlink(target, logFile); err != nil {
		t.Skipf("this platform/filesystem cannot create symlinks: %v", err)
	}

	if _, err := d.Start(StartInput{InstanceID: id, WorktreePath: t.TempDir()}); err != nil {
		t.Fatalf("a symlinked serve.log must not block Start: %v", err)
	}
	t.Cleanup(func() { _ = d.Stop(id) })

	// (a) no rotation happened. Lstat, not Stat: under the Stat version
	// serve.log.1 IS a symlink, and a Stat would follow it to the very target
	// the assertions below already cover — it would not be testing the name.
	if _, err := os.Lstat(logFile + ".1"); !os.IsNotExist(err) {
		t.Errorf("a symlinked serve.log was rotated, serve.log.1 exists (err = %v); a link must never be renamed as if it were the log", err)
	}
	// The link itself is untouched and still points at the same target.
	lfi, err := os.Lstat(logFile)
	if err != nil {
		t.Fatalf("serve.log must still be in place, un-rotated: %v", err)
	}
	if lfi.Mode()&os.ModeSymlink == 0 {
		t.Errorf("serve.log mode = %v, want a symlink; rename moved the link and OpenFile replaced it with a plain file", lfi.Mode())
	}
	if got, err := os.Readlink(logFile); err != nil {
		t.Errorf("serve.log is no longer a symlink: %v", err)
	} else if got != target {
		t.Errorf("serve.log points at %q, want its original target %q", got, target)
	}

	// (b) Start completed normally and the run's output reached the TARGET
	// through the link — read via the link's name to state it that way. This
	// is the half that separates "correctly skipped" from "the log silently went
	// somewhere else": the head marker proves the previous run's bytes survived
	// un-truncated, the tail proves the new run appended to them.
	if got := readPrefix(t, logFile, len(headMarker)); !strings.HasPrefix(got, headMarker) {
		t.Errorf("the target's previous log was replaced, not appended to: head = %q", got)
	}
	if tail := readSuffix(t, logFile, 64); !strings.Contains(tail, runMarker) {
		t.Errorf("the new run wrote nothing to the target through the link; tail = %q", tail)
	}
	tfi, err := os.Stat(target)
	if err != nil {
		t.Fatalf("the target must still exist: %v", err)
	}
	if tfi.Size() <= seededSize {
		t.Errorf("target size = %d, want > the seeded %d; the append through the link never happened", tfi.Size(), seededSize)
	}
}

// TestServeLogMaxBytesIsTheDocumentedCap pins the constant the whole rule is
// written against. It is a cheap assertion, and it is here because every other
// test in this file derives its fixture from the constant — change the number
// and they all silently follow, so the number itself needs a pin.
func TestServeLogMaxBytesIsTheDocumentedCap(t *testing.T) {
	if serveLogMaxBytes != 10<<20 {
		t.Errorf("serveLogMaxBytes = %d, want 10 MiB (10<<20)", int64(serveLogMaxBytes))
	}
}
