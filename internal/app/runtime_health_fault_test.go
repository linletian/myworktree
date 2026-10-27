//go:build unix

package app

// Real-fault integration and boundary tests for D1 (issue #130, review P4).
//
// These are the two sides of the line that v1's narrative blurred, pinned
// with REAL non-nil SIGSEGVs in subprocesses — no normal panic standing in
// for a fault, no hand-written recover standing in for net/http's:
//
//   - HANDLER path: a fault inside a handler body, on a connection goroutine
//     armed by the production withPanicOnFault middleware, is caught by
//     net/http's per-conn recover (server.go:1940): the connection dies
//     with `http: panic serving` + the faulting stack, the daemon answers
//     the NEXT request with 200, and Shutdown completes.
//   - TEARDOWN path (the #130 shape): a fault in a defer AFTER recover()
//     has returned cannot be caught by that defer (panic.go:1138 recovery
//     consumes gp._panic once) — the process exits 2 with an unrecovered
//     `panic:` naming the faulting frame. NO liveness is asserted here,
//     and `http: panic serving` must NOT appear on this path.
//   - DUMP WIDTH: the same teardown crash under GOTRACEBACK=single (the
//     default — only the panicking goroutine) vs =all (a parked marker
//     goroutine's stack included). Exit 2 in both; this code never
//     mutates the operator's GOTRACEBACK, the subprocess pair just pins
//     why unattended forensics should set `all`. The marker's entry is
//     synchronized before the fault lands, so both halves compare against a
//     frame that exists by construction.
//
// The fatal shapes live ONLY in the helper, reached through a helper env;
// the parent launches `exec.Command(os.Args[0], "-test.run=^…Helper$", …)`
// and judges the exit code, so an expected crash never becomes a CI
// failure. Run under plain `go test` and under `-race` alike.

import (
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"regexp"
	"runtime/debug"
	"strconv"
	"strings"
	"sync/atomic"
	"syscall"
	"testing"

	"myworktree/internal/store"
)

// faultHelperEnv selects the helper mode in the subprocess. Unset (every
// ordinary test run) means the helper test skips without touching anything.
const faultHelperEnv = "MYWORKTREE_FAULT_TEST_HELPER"

// faultSink keeps the faulting load from being dead-code-eliminated: the
// value of the load is consumed by a package-level atomic, so the compiler
// cannot delete the read that is the entire point of the helper.
var faultSink atomic.Int64

// faultViaMunmap raises a REAL SIGSEGV at a non-nil address ≥0x1000 — the
// same fault shape as #130 (addr=0x28066ec1be0): mmap one anonymous page,
// munmap it, then read the first byte through the stale slice header. No
// unsafe, no cgo: syscall.Mmap hands back a []byte whose backing is unmapped
// under it, and the load of b[0] faults exactly like a wild pointer would.
// The result flows into faultSink so the load survives optimisation.
//
// The si_code is deliberately NOT part of that shape, because it is platform
// vocabulary for ONE fault — "this page is not accessible": Linux reports the
// unmapped page as code=0x1 SEGV_MAPERR (the form #130's Linux/Go 1.26 dump
// carries) and macOS/arm64 reports the identical munmap-then-read as code=0x2
// SEGV_ACCERR (measured on CI run 40437789057). Nothing here claims the #130
// shape exists only under one si_code; what separates it from a nil
// dereference is the non-nil addr ≥0x1000, asserted on its own in the
// teardown parent test below.
//
// One text gotcha, pinned by that same test: the panic this produces on an
// armed goroutine comes from panicmemAddr (GOROOT go1.25.0
// runtime/signal_unix.go:928 → panic.go:265), whose message text is
// deliberately the SAME as a nil dereference ("invalid memory address or nil
// pointer dereference", panic.go:258 + error.go:106) — the discriminator is
// NOT the message but the `[signal SIGSEGV … addr=0x…]` header over a
// non-nil addr ≥0x1000 (si_code 0x1 on Linux, 0x2 on macOS), the
// recoverability itself (no `fatal error: fault`), and the frame at the
// post-munmap load line.
func faultViaMunmap() {
	// Argument order is (fd, offset, length, prot, flags) — Go's syscall
	// package puts fd FIRST (syscall_linux.go:1278), unlike the C mmap.
	b, err := syscall.Mmap(-1, 0, os.Getpagesize(),
		syscall.PROT_READ|syscall.PROT_WRITE, syscall.MAP_ANON|syscall.MAP_PRIVATE)
	if err != nil {
		panic("fault helper: mmap failed: " + err.Error())
	}
	if err := syscall.Munmap(b); err != nil {
		panic("fault helper: munmap failed: " + err.Error())
	}
	faultSink.Add(int64(b[0])) // SIGSEGV at b[0]: non-nil addr ≥0x1000, si_code platform-specific
}

// parkedStackMarker parks on a channel nobody closes, so its frame is
// guaranteed to be visible in an all-goroutine traceback and guaranteed NOT
// to be the panicking goroutine. Its name is the probe for dump width.
//
// `entered` is the marker's arrival signal, and it is load-bearing, not
// tidiness: the width pair compares a traceback against this goroutine's
// frame, so the marker has to be PAST its own entry point before the
// faulting goroutine is allowed to crash the process. Without the signal the
// race between "go parkedStackMarker" and the fault decides the verdict —
// measured at 7/20 failures on `go test -count=20`, where the process died
// before the marker ever ran and the GOTRACEBACK=all assertion failed on an
// absent frame. A width comparison against a goroutine that never existed
// proves nothing in either direction: `single` passes vacuously (nothing to
// exclude) and `all` fails on luck.
func parkedStackMarker(release chan struct{}, entered chan struct{}) {
	close(entered) // "I am here"; the helper waits on this before faulting
	<-release
}

// runFaultHelper starts this same test binary as a subprocess in the named
// helper mode and returns its combined output plus exit code. The child
// inherits the parent's environment (so -race builds fault under -race too)
// plus the mode selector and any extra pins the case names.
func runFaultHelper(t *testing.T, mode string, extraEnv ...string) (string, int) {
	t.Helper()
	cmd := exec.Command(os.Args[0], "-test.run=^TestRuntimeHealthFaultHelper$", "-test.timeout=60s")
	cmd.Env = append(os.Environ(), faultHelperEnv+"="+mode)
	cmd.Env = append(cmd.Env, extraEnv...)
	out, err := cmd.CombinedOutput()
	if ee, ok := err.(*exec.ExitError); ok {
		return string(out), ee.ExitCode()
	}
	if err != nil {
		t.Fatalf("starting fault helper %q failed: %v\noutput=%q", mode, err, out)
	}
	return string(out), 0
}

// TestRuntimeHealthFaultSurvivalOverRealListener pins the HANDLER-path
// promise at its full width: real Server.Start(), real middleware chain,
// real munmap SIGSEGV inside the /fault handler body. The request itself
// dies (EOF is the closed connection), `http: panic serving` plus the
// faulting test function's stack appear, the very next request on the same
// listener answers 200, and Shutdown completes — exit 0, never exit 2.
func TestRuntimeHealthFaultSurvivalOverRealListener(t *testing.T) {
	out, code := runFaultHelper(t, "handler")
	if code != 0 {
		t.Fatalf("handler-fault helper exited %d, want 0 — the daemon must SURVIVE a fault inside a handler body\noutput=%q", code, out)
	}
	if !strings.Contains(out, "http: panic serving") {
		t.Fatalf("no `http: panic serving` in the survivor output — net/http's per-conn recover never reported the fault; output=%q", out)
	}
	if !strings.Contains(out, "faultViaMunmap") {
		t.Fatalf("the panic log must carry the faulting test function's frame (the second dump #130 never had); output=%q", out)
	}
	if strings.Contains(out, "fatal error: fault") {
		t.Fatalf("the process took the throw path instead of the panic path — the arm did not reach the connection goroutine; output=%q", out)
	}
	if !strings.Contains(out, "ok-body") {
		t.Fatalf("the /ok request after the fault must answer 200 with its body — the daemon did not keep serving; output=%q", out)
	}
	if !strings.Contains(out, "[fault-helper] DONE") {
		t.Fatalf("helper did not reach its final marker (Shutdown included); output=%q", out)
	}
}

// TestRuntimeHealthTeardownFaultExitsWithStack pins the BOUNDARY: the
// net/http teardown order — recover() called and RETURNED first, then a
// real fault in the same defer — is NOT covered by the rescue. The process
// must exit 2 with an unrecovered `panic:` naming faultViaMunmap, and must
// NOT print `http: panic serving` (nothing caught it) nor `fatal error:
// fault` (armed goroutines panic instead of throwing). No liveness claim.
func TestRuntimeHealthTeardownFaultExitsWithStack(t *testing.T) {
	out, code := runFaultHelper(t, "teardown")
	if code != 2 {
		t.Fatalf("teardown-fault helper exited %d, want 2 — a fault after recover() has returned is NOT survivable\noutput=%q", code, out)
	}
	if !strings.Contains(out, "panic:") {
		t.Fatalf("output must carry the unrecovered `panic:` line; output=%q", out)
	}
	if !strings.Contains(out, "faultViaMunmap") {
		t.Fatalf("the traceback must name the faulting frame; output=%q", out)
	}
	if strings.Contains(out, "http: panic serving") {
		t.Fatalf("the teardown path must NOT claim `http: panic serving` — no recover caught this fault; output=%q", out)
	}
	if strings.Contains(out, "fatal error: fault") {
		t.Fatalf("on an armed goroutine the fault converts to a panic before the throw could run; output=%q", out)
	}
	// The fault signature: a SIGSEGV header over a non-nil addr ≥0x1000 — a
	// nil dereference would be addr<0x1000 and never reach the SetPanicOnFault
	// branch at all. The si_code is accepted as 0x1 OR 0x2 because both are
	// the "page not accessible" spelling of THIS fault and the two are just
	// platform vocabulary: 0x1 SEGV_MAPERR is what Linux prints for the
	// unmapped page (the code #130's Linux/Go 1.26 dump carries), 0x2
	// SEGV_ACCERR is what macOS/arm64 prints for the same munmap-then-read
	// (measured on CI run 40437789057). The discriminator against a nil
	// dereference is the addr ≥0x1000, asserted on its own below — NOT the
	// si_code, so nothing here claims the #130 shape exists only at code=0x1.
	m := sigSEGVHeaderRE.FindStringSubmatch(out)
	if m == nil {
		t.Fatalf("output must carry the SIGSEGV header over a fault addr like #130's dump — si_code 0x1 (SEGV_MAPERR, Linux) or 0x2 (SEGV_ACCERR, macOS/arm64), the two platform spellings of the same inaccessible-page fault; output=%q", out)
	}
	addr, err := strconv.ParseInt(m[1], 16, 64)
	if err != nil || addr < 0x1000 {
		t.Fatalf("fault addr=%s, want a non-nil address ≥0x1000 (the #130 shape; below 0x1000 sigpanic would call panicmem, not panicmemAddr)", m[1])
	}
}

// sigSEGVHeaderRE is the teardown assertion's pattern, hoisted so the
// cross-platform claim it carries is pinned on its own and not only by
// whichever OS happens to run the suite: Linux (where #130's dump was taken)
// emits si_code 0x1 SEGV_MAPERR for the unmapped page, macOS/arm64 emits
// 0x2 SEGV_ACCERR for the identical munmap-then-read (CI run 40437789057).
// Narrowing this back to a single si_code would put `test-build
// (macos-latest)` straight back in the red while proving nothing about the
// fault shape, which the addr ≥0x1000 assertion is the discriminator for.
var sigSEGVHeaderRE = regexp.MustCompile(`\[signal SIGSEGV: segmentation violation code=0x[12] addr=0x([0-9a-f]+)`)

// TestRuntimeHealthSIGSEGVHeaderRegexIsCrossPlatform pins the pattern above
// against the two REAL captured forms — #130's Linux/Go 1.26 header and the
// macOS/arm64 header CI run 40437789057 produced for this very test — so the
// platform tolerance is a tested property rather than a comment. The host's
// own subprocess run above still does the real faulting; this case only
// settles that the reader of its output is not platform-bound, and that a
// si_code outside the inaccessible-page pair is NOT waved through.
func TestRuntimeHealthSIGSEGVHeaderRegexIsCrossPlatform(t *testing.T) {
	t.Parallel()
	captured := []struct {
		os   string
		line string
		addr int64
	}{
		{
			// issue #130's own dump, Linux on Go 1.26.
			os:   "linux/amd64 (Go 1.26, #130)",
			line: "[signal SIGSEGV: segmentation violation code=0x1 addr=0x28066ec1be0 pc=0x4f2c14]",
			addr: 0x28066ec1be0,
		},
		{
			// The same helper's fault on the macos-latest runner, run
			// 40437789057 — the output that made the single-code pattern red.
			os:   "darwin/arm64 (macos-latest, run 40437789057)",
			line: "[signal SIGSEGV: segmentation violation code=0x2 addr=0x1013e8000 pc=0x100d5c1a0]",
			addr: 0x1013e8000,
		},
	}
	for _, c := range captured {
		m := sigSEGVHeaderRE.FindStringSubmatch(c.line)
		if m == nil {
			t.Fatalf("the pattern must match the %s fault header %q — one fault shape, two platform spellings", c.os, c.line)
		}
		got, err := strconv.ParseInt(m[1], 16, 64)
		if err != nil {
			t.Fatalf("%s: addr %q does not parse: %v", c.os, m[1], err)
		}
		if got != c.addr {
			t.Fatalf("%s: captured addr = %#x, want %#x — the capture, not the assertion, moved", c.os, got, c.addr)
		}
		if got < 0x1000 {
			t.Fatalf("%s: addr %#x is below 0x1000 — that is the nil-dereference side of the line, not this fault", c.os, got)
		}
	}
	// And the tolerance is bounded, not "any code": a different si_code (e.g.
	// 0x4 SEGV_BNDERR on Linux) is not this fault and must not match, so the
	// pair above is a decision and not a wildcard. The leading bracket is in
	// the sample on purpose — without it this case would pass for the wrong
	// reason (the pattern's `\[`), proving nothing about the si_code.
	if sigSEGVHeaderRE.MatchString("[signal SIGSEGV: segmentation violation code=0x4 addr=0x7ffd0000 pc=0x4f2c14]") {
		t.Fatal("the pattern matched si_code 0x4, which is not the inaccessible-page pair — the tolerance is 0x1/0x2 and nothing wider")
	}
}

// TestRuntimeHealthDumpWidthSingleVsAll pins the GOTRACEBACK contract: the
// same armed teardown crash prints only the panicking goroutine under the
// default `single`, and includes the parked marker goroutine under `all`
// (all ≠ system ≠ crash). Exit 2 in BOTH — dump width never changes the
// liveness verdict, which is the point the deployment docs make.
func TestRuntimeHealthDumpWidthSingleVsAll(t *testing.T) {
	singleOut, singleCode := runFaultHelper(t, "teardown", "GOTRACEBACK=single")
	if singleCode != 2 {
		t.Fatalf("single: exit %d, want 2\noutput=%q", singleCode, singleOut)
	}
	if strings.Contains(singleOut, "parkedStackMarker") {
		t.Fatalf("GOTRACEBACK=single must print ONLY the panicking goroutine's stack, yet the parked marker's frame appears — the width claim the deployment docs rest on would be false; output=%q", singleOut)
	}
	if !strings.Contains(singleOut, "faultViaMunmap") {
		t.Fatalf("single: the panicking goroutine's own stack must still be printed; output=%q", singleOut)
	}

	allOut, allCode := runFaultHelper(t, "teardown", "GOTRACEBACK=all")
	if allCode != 2 {
		t.Fatalf("all: exit %d, want 2\noutput=%q", allCode, allOut)
	}
	if !strings.Contains(allOut, "parkedStackMarker") {
		t.Fatalf("GOTRACEBACK=all must include the parked user goroutine's stack — this is the width unattended forensics needs and `single` silently loses; output=%q", allOut)
	}
}

// TestRuntimeHealthFaultHelper is the subprocess entry point. It runs ONLY
// when the helper env names a mode; every ordinary `go test` invocation
// skips it loudly, so the fatal shapes can never execute inside the parent
// suite and an expected exit 2 can never arrive as an unexplained failure.
func TestRuntimeHealthFaultHelper(t *testing.T) {
	mode := os.Getenv(faultHelperEnv)
	switch mode {
	case "handler":
		faultHelperSurvival(t)
	case "teardown":
		faultHelperTeardown(t)
	default:
		t.Skip("subprocess-only helper (set " + faultHelperEnv + "=handler|teardown via the parent test)")
	}
}

// faultHelperSurvival is the HANDLER-side helper: production Start(),
// production middleware, munmap SIGSEGV inside the /fault handler body.
func faultHelperSurvival(t *testing.T) {
	srv, _ := newIsolatedTestServer(t, store.State{})
	// The sampler joins the chain as in production, tick OFF — the point of
	// this helper is the fault path, not the ticker.
	srv.runtimeHealth = newRuntimeHealth(srv.logger)
	srv.runtimeHealth.interval = 0

	srv.mux.HandleFunc("/fault", func(w http.ResponseWriter, r *http.Request) {
		faultViaMunmap() // real SIGSEGV, mid-handler, on the armed conn goroutine
		w.WriteHeader(http.StatusOK)
	})
	srv.mux.HandleFunc("/ok", func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte("ok-body"))
	})

	url, err := srv.Start()
	if err != nil {
		t.Fatalf("fault helper: Start failed: %v", err)
	}
	if !srv.panicOnFault {
		t.Fatalf("fault helper: panicOnFault is off — the production middleware would not have armed this connection at all")
	}

	// The faulting request: the connection is closed by net/http after it
	// recovers, so EOF (or a reset) is the correct client-side outcome.
	resp, err := http.Get(url + "fault")
	if err == nil {
		body, _ := io.ReadAll(resp.Body)
		_ = resp.Body.Close()
		t.Fatalf("fault helper: /fault returned %d %q, want the connection to die with EOF after the recovered panic", resp.StatusCode, body)
	}
	if !strings.Contains(err.Error(), "EOF") && !strings.Contains(err.Error(), "connection reset") {
		t.Fatalf("fault helper: /fault failed with %v, want EOF/connection reset from net/http closing the faulted connection", err)
	}

	// The survival: a NEW request on the SAME listener must answer 200.
	resp, err = http.Get(url + "ok")
	if err != nil {
		t.Fatalf("fault helper: /ok after the fault failed: %v — the daemon did not survive", err)
	}
	body, _ := io.ReadAll(resp.Body)
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusOK || string(body) != "ok-body" {
		t.Fatalf("fault helper: /ok = %d %q, want 200 \"ok-body\"", resp.StatusCode, body)
	}
	// The proof line the parent greps for — fmt.Println, not t.Log, so it
	// is in the child's stdout without -v.
	fmt.Printf("[fault-helper] ok-after-fault status=%d body=%q\n", resp.StatusCode, body)

	srv.Shutdown()
	// fmt, not t.Log: t.Log is silent without -v, and the parent reads the
	// child's CombinedOutput for this DONE marker.
	fmt.Println("[fault-helper] DONE")
}

// faultHelperTeardown is the BOUNDARY helper: it replays net/http's own
// defer order on an armed goroutine — recover() runs and RETURNS, then the
// real fault lands — and parks a marker goroutine for the width pair. The
// unrecovered panic must take the process down with exit 2.
//
// The marker's `entered` signal is synchronized BEFORE the faulting goroutine
// starts, on purpose: the parent compares the two subprocess tracebacks for
// this one frame, so the frame has to exist by construction in both runs.
// Unsynced, the outcome was a coin flip settled by the scheduler (7/20
// failures at -count=20) and the `single` half would have been passing
// vacuously on a missing marker rather than on real width suppression.
func faultHelperTeardown(t *testing.T) {
	release := make(chan struct{}) // never closed: the marker stays parked
	entered := make(chan struct{})
	go parkedStackMarker(release, entered)
	<-entered // the marker is parked (or at least past close) before any fault

	go func() {
		// Arm THIS goroutine — exactly what withPanicOnFault does for a
		// connection goroutine, with the connection scaffolding removed so
		// nothing but the defer order can be blamed for the outcome.
		debug.SetPanicOnFault(true)
		defer func() {
			_ = recover()    // ends first, mirroring server.go:1940…
			faultViaMunmap() // …then the fault, mirroring cancelCtx() at :1947
		}()
	}()

	// Park forever; the faulting goroutine's unrecovered panic exits the
	// process long before the -test.timeout could ever be the witness.
	select {}
}
