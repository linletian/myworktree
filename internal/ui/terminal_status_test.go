package ui

import (
	"os"
	"os/exec"
	"regexp"
	"strconv"
	"strings"
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

// TestSidebarCollapseHandling runs the Node unit tests for the issue #99
// desktop sidebar collapse semantics (testdata/sidebar_collapse.test.mjs):
// localStorage round-trip and degradation defaults (R3), the class-only
// collapse toggle with its two-state chevron button (R1/R2), the first
// document-level Ctrl/Cmd+B keydown and its input/repeat/case guards (R4),
// and the xterm refit scheduled after the class flip (R12).
//
// Same arrangement as TestTerminalStatusHandling: the cases slice the
// shipped index.html and run against stubbed browser state, this wrapper
// makes `go test` enforce them, and the file lives in testdata/ because
// static/* is embedded and served to every browser. Skips when node is
// absent.
func TestSidebarCollapseHandling(t *testing.T) {
	node, err := exec.LookPath("node")
	if err != nil {
		t.Skip("node not on PATH; skipping sidebar collapse JS tests")
	}
	out, err := exec.Command(node, "--test", "testdata/sidebar_collapse.test.mjs").CombinedOutput()
	if err != nil {
		t.Fatalf("node --test testdata/sidebar_collapse.test.mjs: %v\n%s", err, out)
	}
	t.Logf("%s", out)
}

// TestViewportHeightHandling runs the Node unit tests for the issue #100 L1
// viewport & height layer (testdata/viewport_height.test.mjs): the iOS soft
// keyboard is compensated by pinning #app's inline height to
// window.visualViewport's visible bottom edge (sentinel:
// documentElement.clientHeight), pinch / double-tap zoom opts out of
// pinning, the scroll listener only re-pins an existing pin, the inline
// height is cleared again when nothing is covered, environments without
// visualViewport degrade to a no-op, and the collapse class (PR1) plus
// keyboard narrowing (PR2) coexist without touching each other's state
// (R12).
//
// Same arrangement as TestSidebarCollapseHandling: the cases slice the
// shipped index.html and run against stubbed browser state, this wrapper
// makes `go test` enforce them, and the file lives in testdata/ because
// static/* is embedded and served to every browser. Skips when node is
// absent.
func TestViewportHeightHandling(t *testing.T) {
	node, err := exec.LookPath("node")
	if err != nil {
		t.Skip("node not on PATH; skipping viewport height JS tests")
	}
	out, err := exec.Command(node, "--test", "testdata/viewport_height.test.mjs").CombinedOutput()
	if err != nil {
		t.Fatalf("node --test testdata/viewport_height.test.mjs: %v\n%s", err, out)
	}
	t.Logf("%s", out)
}

// TestNarrowDrawerHandling runs the Node unit tests for the issue #100 L2
// breakpoint / drawer layer (testdata/narrow_drawer.test.mjs): the R14
// viewport-dispatched default for a missing / unparseable persisted value
// (narrow first-load defaults to the closed drawer, an explicit persisted
// value always wins — the §5.3-5 desktop-collapsed-user case needs no
// special case), the availability guards around matchMedia, and the R7
// mask's close path running through the same single .collapsed class and
// shared mw.ui.sidebarCollapsed key as the toggle button and Ctrl/Cmd+B
// (no second state variable, plan §3.1).
//
// Same arrangement as TestViewportHeightHandling: the cases slice the
// shipped index.html and run against stubbed browser state, this wrapper
// makes `go test` enforce them, and the file lives in testdata/ because
// static/* is embedded and served to every browser. Skips when node is
// absent.
func TestNarrowDrawerHandling(t *testing.T) {
	node, err := exec.LookPath("node")
	if err != nil {
		t.Skip("node not on PATH; skipping narrow drawer JS tests")
	}
	out, err := exec.Command(node, "--test", "testdata/narrow_drawer.test.mjs").CombinedOutput()
	if err != nil {
		t.Fatalf("node --test testdata/narrow_drawer.test.mjs: %v\n%s", err, out)
	}
	t.Logf("%s", out)
}

// TestTouchInteractionHandling runs the Node unit tests for the issue #100
// L3 touch-interaction layer (testdata/touch_interaction.test.mjs): the
// sidebar split-panel drag on Pointer Events — pointerdown captures the
// pointer (setPointerCapture), move/up/cancel listeners register and clean
// up, pointercancel ends the drag like pointerup, touch/pen/mouse share one
// path, the clamp math (minTop 120 / minBottom 100) is unchanged, and a
// throwing setPointerCapture degrades to the listener-based drag.
//
// Same arrangement as TestNarrowDrawerHandling: the cases slice the shipped
// index.html and run against stubbed browser state, this wrapper makes
// `go test` enforce them, and the file lives in testdata/ because static/*
// is embedded and served to every browser. Skips when node is absent.
func TestTouchInteractionHandling(t *testing.T) {
	node, err := exec.LookPath("node")
	if err != nil {
		t.Skip("node not on PATH; skipping touch interaction JS tests")
	}
	out, err := exec.Command(node, "--test", "testdata/touch_interaction.test.mjs").CombinedOutput()
	if err != nil {
		t.Fatalf("node --test testdata/touch_interaction.test.mjs: %v\n%s", err, out)
	}
	t.Logf("%s", out)
}

// TestResponsiveLayerChangelogCounts extends the issue #95 guard
// (TestTerminalStatusChangelogCount) to the four behavioral files behind
// the v0.6.0 responsive plan: each CHANGELOG entry quotes its file's
// scenario count in English number words ("seventeen #99 scenarios",
// "seven new issue #100 L1 scenarios", ...), and a hand-maintained word in
// prose drifts silently. Derive each count from `grep -c '^test('` and
// require every occurrence of that entry's phrase to say the same number.
// Unlike the terminal-status phrase (whose fixed "Pinned by N" wording the
// #95 guard owns), these entries use distinct per-layer phrasing, so the
// guard is per-file: a wrong or missing word fails here.
func TestResponsiveLayerChangelogCounts(t *testing.T) {
	// English number words the scenario counts can currently spell (extend
	// as files grow).
	words := map[string]int{
		"one": 1, "two": 2, "three": 3, "four": 4, "five": 5,
		"six": 6, "seven": 7, "eight": 8, "nine": 9, "ten": 10,
		"eleven": 11, "twelve": 12, "thirteen": 13, "fourteen": 14,
		"fifteen": 15, "sixteen": 16, "seventeen": 17, "eighteen": 18,
		"nineteen": 19, "twenty": 20,
	}
	changelog, err := os.ReadFile("../../CHANGELOG.md")
	if err != nil {
		t.Fatalf("read CHANGELOG.md: %v", err)
	}
	for _, tc := range []struct {
		file string
		// one capture group: the English number word preceding the phrase
		phrase string
	}{
		{"testdata/sidebar_collapse.test.mjs", `#99 scenarios`},
		{"testdata/viewport_height.test.mjs", `new issue #100 L1 scenarios`},
		{"testdata/narrow_drawer.test.mjs", `new issue #100 L2 scenarios`},
		{"testdata/touch_interaction.test.mjs", `new issue #100 L3 scenarios`},
	} {
		src, err := os.ReadFile(tc.file)
		if err != nil {
			t.Fatalf("read %s: %v", tc.file, err)
		}
		cases := len(regexp.MustCompile(`(?m)^test\(`).FindAll(src, -1))
		if cases == 0 {
			t.Fatalf("no node --test cases found in %s — the slice anchors probably moved", tc.file)
		}
		re := regexp.MustCompile(`(\w+) ` + regexp.QuoteMeta(tc.phrase))
		matches := re.FindAllSubmatch(changelog, -1)
		if len(matches) == 0 {
			t.Fatalf("CHANGELOG.md no longer quotes a count for %q — add the phrase or update this guard", tc.phrase)
		}
		for _, m := range matches {
			word := strings.ToLower(string(m[1]))
			got, ok := words[word]
			if !ok {
				t.Fatalf("CHANGELOG.md spells the %q count as %q — extend the word map in this guard", tc.phrase, word)
			}
			if got != cases {
				t.Errorf("CHANGELOG.md says %s %q but %s has %d cases — keep the wording and the guard in sync (issue #95)", word, tc.phrase, tc.file, cases)
			}
		}
	}
}

// TestTerminalStatusChangelogCount keeps the CHANGELOG's coverage claim honest.
// The entry quotes the number of `node --test` cases, and that number drifted
// wrong twice while the cases were still being added (17, then 22, then 26 for
// a file holding 27) — a hand-maintained count in prose is a claim nobody
// checks. Derive it here instead: count the cases, then require the entry to
// say the same thing.
//
// Issue #95: EVERY occurrence of the claim phrase must equal the derived
// count, not just the first match. The previous first-match lookup silently
// re-anchored the guard to whichever entry happened to sit highest in the
// file (entries are prepended in reverse chronological order), so a new
// entry quoting the phrase with a stale number would both hijack the
// constraint and leave the real claim unchecked. Requiring every occurrence
// to match makes the phrase position-independent — it now means "the file's
// total case count" wherever it appears (subset counts use different
// phrasing, e.g. #83's "fourteen new #83 cases"). Of the issue's two
// proposals this was chosen over per-entry anchoring as the simpler
// invariant to keep unambiguous as entries accumulate.
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
	claims := regexp.MustCompile(`Pinned by (\d+) `+"`"+`node --test`+"`"+` cases`).FindAllSubmatchIndex(changelog, -1)
	if len(claims) == 0 {
		t.Fatal("CHANGELOG.md no longer says \"Pinned by N `node --test` cases\"")
	}
	for _, loc := range claims {
		num := changelog[loc[2]:loc[3]]
		// Report the line so a multi-occurrence violation needs no grep.
		line := 1 + strings.Count(string(changelog[:loc[0]]), "\n")
		if got, err := strconv.Atoi(string(num)); err != nil || got != cases {
			t.Errorf("CHANGELOG.md:%d claims %s `node --test` cases but testdata/terminal_status.test.mjs has %d — EVERY occurrence of the phrase must state the file's total (issue #95)",
				line, num, cases)
		}
	}
}

// TestTerminalStatusLivenessHeartbeatContract pins the client half of the
// issue #83 wire contract to its server counterpart, as a source-anchor test
// (the behavioural half lives in the node cases). The pieces are individually
// easy to "fix" in a way that silently unfixes the whole: whitelisting
// "ping" but not "pong" paints pong JSON into the terminal; stamping only
// the control branch (not the top of onmessage) leaves binary-only traffic
// looking stale; initializing the fields in one factory but not the other
// is the drift this repo has already been bitten by twice. And the three
// numbers only form a safe margin together (server ping 10s × 3 = client
// threshold 30s < server read deadline 45s), so all three are pinned here in
// one place against the server's const block.
func TestTerminalStatusLivenessHeartbeatContract(t *testing.T) {
	index := fetchIndexHTML(t)
	pty, err := os.ReadFile("static/kinds/pty.js")
	if err != nil {
		t.Fatalf("read kinds/pty.js: %v", err)
	}
	appSrc, err := os.ReadFile("../app/app.go")
	if err != nil {
		t.Fatalf("read internal/app/app.go: %v", err)
	}

	// The whitelist guard must enumerate ping AND pong: parseTTYControlMessage
	// returning null for either means that frame paints as literal text.
	if !strings.Contains(index, `msg.type !== "ping" && msg.type !== "pong"`) {
		t.Fatal(`parseTTYControlMessage must whitelist both "ping" and "pong" (issue #83)`)
	}
	// Issue #98: the TEXT ping heartbeat and the sync offset echo are
	// opt-in on both halves of the wire — the client must DECLARE the
	// capabilities and the server must GATE on them (a client without the
	// whitelist would paint the ping frame every 10s and the sync frame
	// once per connect).
	if !strings.Contains(index, "&caps=ping,sync") {
		t.Fatal(`connectTTY must declare caps=ping,sync so the server sends the TEXT ping heartbeat and the sync offset echo (issue #98)`)
	}
	if !strings.Contains(string(appSrc), `ttyClientHasCap(caps, "ping")`) {
		t.Fatal("the server must gate the TEXT ping heartbeat on the client's caps opt-in (issue #98)")
	}
	if !strings.Contains(string(appSrc), `ttyClientHasCap(caps, "sync")`) {
		t.Fatal("the server must gate the sync offset echo on the client's caps opt-in (issue #98)")
	}
	if !strings.Contains(string(appSrc), `r.URL.Query()["since"]`) {
		t.Fatal("the sync echo must also flow to a client presenting an explicit since cursor — the inferred #87-era opt-in (issue #98 review round 2)")
	}
	if !strings.Contains(string(appSrc), `r.URL.Query()["caps"]`) {
		t.Fatal(`caps must be scanned across ALL repeated parameter values (Query()["caps"]), not Get's first value only`)
	}
	// The stamp must be the FIRST thing ws.onmessage does - before any
	// branch - so EVERY frame (control or binary) refreshes it. Anchoring on
	// the following line proves nothing was reordered between them.
	if !strings.Contains(index, "session.lastDataAt = Date.now();\n                    const controlMsg = parseTTYControlMessage(ev.data);") {
		t.Fatal("ws.onmessage must stamp lastDataAt at its very top, before parseTTYControlMessage (issue #83)")
	}
	// Both session factories initialize the two new fields identically.
	for name, src := range map[string]string{"index.html": index, "kinds/pty.js": string(pty)} {
		for _, field := range []string{"lastDataAt: 0,", "ttyLivenessTimer: null,"} {
			if !strings.Contains(src, field) {
				t.Fatalf("%s must initialize the liveness field %q (factories must not drift, issue #83)", name, field)
			}
		}
	}
	// The interval is cleared on every teardown path: clear-before-arm in
	// the ready handler, onclose, disconnectTTY (which detaches onclose
	// before closing, so it is the ONLY clear on that path), and startSSE
	// (the transport it replaces dies with the socket). The watchdog's
	// supersede guard clears its OWN captured handle, so it is not one of
	// these four. Four clears minimum or a reconnect stacks a duplicate
	// watchdog.
	if n := strings.Count(index, "clearInterval(session.ttyLivenessTimer)"); n < 4 {
		t.Fatalf("index.html has %d clearInterval(session.ttyLivenessTimer) sites, want >= 4 (arm/onclose/disconnectTTY/startSSE, issue #83)", n)
	}
	// Two shapes, split on socket existence (FIX-T). The SOCKET branch must
	// keep the three-step teardown CONTIGUOUS and in order: disconnectTTY,
	// then force IDLE - disconnectTTY leaves 'DISCONNECTING' (staleness
	// requires a socket), and connectTTY on that would stall ~1.05s in its
	// 100ms state wait-loop - then arm the retry AFTER disconnectTTY
	// cleared any previous one. The NO-socket branch is the connection-
	// overlay recovery, where the user is waiting: it must connect
	// IMMEDIATELY with no backoff, because there is no socket to flap
	// against - a refactor that quietly re-adds the 5000ms delay there is
	// exactly what the null-branch anchor forbids.
	hStart := strings.Index(index, "function reconnectStaleTTY(session) {")
	if hStart < 0 {
		t.Fatal("reconnectStaleTTY helper missing - the stale reconnects must share one teardown shape (issue #83)")
	}
	helper := index[hStart:]
	if end := strings.Index(helper, "\n        }"); end >= 0 {
		helper = helper[:end]
	}
	if !strings.Contains(helper, "disconnectTTY(session);\n            session.ttyState = 'IDLE';\n            session.ttyReconnectTimer = setTimeout(() => connectTTY(session), TTY_RECONNECT_DELAY_MS);") {
		t.Fatal("the socket branch must stay disconnect -> force IDLE -> arm retry, contiguously in that order (issue #83)")
	}
	nullAt := strings.Index(helper, "if (!session.ttySocket) {")
	immediateAt := strings.Index(helper, "session.ttyState = 'IDLE';\n                connectTTY(session);\n                return;")
	if nullAt < 0 || immediateAt < 0 || nullAt > immediateAt {
		t.Fatal("the no-socket branch must connect immediately, no TTY_RECONNECT_DELAY_MS backoff on overlay recovery (issue #83 FIX-T)")
	}
	if n := strings.Count(index, "reconnectStaleTTY(session);"); n < 3 {
		t.Fatalf("index.html has %d reconnectStaleTTY call sites, want >= 3 (watchdog / poll / reconnectRunning, issue #83)", n)
	}
	// The numbers, paired with the server's const block, and the margin
	// COMPUTED rather than eyeballed: the client threshold is exactly 3x
	// the server ping and strictly under the server read deadline, and the
	// watchdog ticks well inside the threshold.
	num := func(re, src, what string) int {
		m := regexp.MustCompile(re).FindStringSubmatch(src)
		if m == nil {
			t.Fatalf("%s not found - constant moved its shape (issue #83)", what)
		}
		v, err := strconv.Atoi(m[1])
		if err != nil {
			t.Fatalf("%s: %v", what, err)
		}
		return v
	}
	pingSec := num(`ttyPingInterval = (\d+) \* time\.Second`, string(appSrc), "server ttyPingInterval")
	readSec := num(`ttyReadDeadline = (\d+) \* time\.Second`, string(appSrc), "server ttyReadDeadline")
	checkMs := num(`const TTY_LIVENESS_CHECK_MS = (\d+);`, index, "client TTY_LIVENESS_CHECK_MS")
	staleMs := num(`const TTY_LIVENESS_STALE_MS = (\d+);`, index, "client TTY_LIVENESS_STALE_MS")
	delayMs := num(`const TTY_RECONNECT_DELAY_MS = (\d+);`, index, "client TTY_RECONNECT_DELAY_MS")
	if delayMs != 5000 {
		t.Fatalf("client TTY_RECONNECT_DELAY_MS = %d, want 5000 - the node FIX-K/FIX-T cases arm exactly this delay", delayMs)
	}
	if staleMs != 3*pingSec*1000 {
		t.Fatalf("client threshold %dms is not 3x the %ds server ping - re-derive the margin (issue #83)", staleMs, pingSec)
	}
	if staleMs >= readSec*1000 {
		t.Fatalf("client threshold %dms is not under the %ds server read deadline - the browser must reap first and keep its cursor (issue #83)", staleMs, readSec)
	}
	if checkMs >= staleMs {
		t.Fatalf("watchdog tick %dms is not inside the threshold %dms", checkMs, staleMs)
	}
	// The watchdog sends the client probe (the round trip a server-only ping
	// cannot provide).
	if !strings.Contains(index, `ws.send(JSON.stringify({ type: "ping" }));`) {
		t.Fatal(`the watchdog must send the {"type":"ping"} probe (issue #83)`)
	}
	// The input gate lives in BOTH session factories, and the renderer's copy
	// can only reach the predicate through this export - the node FIX-A cases
	// drive the renderer against a harness that publishes the sliced function
	// itself, so without this anchor deleting the export line would quietly
	// revert kinds/pty.js to the readyState-only gate it shipped before #83
	// while every node case stayed green.
	if !strings.Contains(index, "window.isTTYHeartbeatStale = isTTYHeartbeatStale;") {
		t.Fatal("index.html must export isTTYHeartbeatStale for kinds/pty.js (issue #83 input gate)")
	}
	for name, src := range map[string]string{"index.html": index, "kinds/pty.js": string(pty)} {
		if !strings.Contains(src, "&& !isTTYHeartbeatStale(session);") &&
			!strings.Contains(src, "&& !(window.isTTYHeartbeatStale ? window.isTTYHeartbeatStale(session) : false);") {
			t.Fatalf("%s must gate its onData socket arm on the heartbeat stamp (issue #83)", name)
		}
	}
}

// TestTerminalStatusPollReconnectsStaleSocket pins the two guards that make
// the hasLiveTTYConnection staleness fix actually DO something. Stamping
// lastDataAt and then letting hasLiveTTYConnection return false is worthless
// if hasTerminalTransportInFlight still counts the dead-but-OPEN socket as
// "in flight" - every caller (poll promote, both activate() paths) would
// silently no-op and the frozen screen would survive the fix, which is the
// exact re-affirmation loop issue #83 is about.
func TestTerminalStatusPollReconnectsStaleSocket(t *testing.T) {
	index := fetchIndexHTML(t)
	for _, anchor := range []string{
		// The single-source predicate the fix consults, READY+OPEN-only so
		// CONNECTING sockets keep blocking exactly as before.
		"if (session.ttySocket && !stale) return true;",
		"if (session.ttyState && session.ttyState !== 'IDLE' && !stale) return true;",
		"return !isTTYHeartbeatStale(session);",
	} {
		if !strings.Contains(index, anchor) {
			t.Fatalf("GET / should include the staleness-aware guard %q (issue #83)", anchor)
		}
	}
	// ensureTerminalLiveTransport must have a reconnect arm for the stale
	// case, and it must sit between the (unchanged) in-flight guard and the
	// status check, so the poll is the PRIMARY detector that heals a
	// half-open socket within its 2s tick instead of silently returning
	// false forever. The arm must delegate to reconnectStaleTTY (the
	// teardown plus the documented backoff cadence live in one place) and
	// must return FALSE - the reconnect is queued, not live (FIX-K).
	start := strings.Index(index, "function ensureTerminalLiveTransport(session)")
	if start < 0 {
		t.Fatal("ensureTerminalLiveTransport no longer found")
	}
	body := index[start:]
	if end := strings.Index(body, "\n        }"); end >= 0 {
		body = body[:end]
	}
	staleAt := strings.Index(body, "if (isTTYHeartbeatStale(session)) {")
	guardAt := strings.Index(body, "if (hasTerminalTransportInFlight(session)) return false;")
	if guardAt < 0 {
		t.Fatal("the in-flight guard anchor must stay in ensureTerminalLiveTransport")
	}
	if staleAt < 0 {
		t.Fatal("ensureTerminalLiveTransport must gain the stale-socket reconnect arm (issue #83)")
	}
	if staleAt < guardAt {
		t.Fatal("the stale reconnect arm must run AFTER the in-flight guard, never before it")
	}
	if !strings.Contains(body[staleAt:], "reconnectStaleTTY(session);\n                return false;") {
		t.Fatal(`the stale arm must call reconnectStaleTTY(session) and return false - a queued reconnect is not a live transport (issue #83)`)
	}
	// The pre-existing non-stale connectTTY below the stale arm must stay
	// a bare connectTTY (that path has no socket to tear down; routing it
	// through the helper would needlessly delay a fresh first connect).
	after := body[staleAt:]
	if first, rest := strings.Index(after, "reconnectStaleTTY(session);"), strings.Index(after, "connectTTY(session);"); first < 0 || rest < 0 || first > rest {
		t.Fatal("the non-stale promotion path must keep its immediate bare connectTTY after the stale arm")
	}

	// FIX-V (the ROOT guard): connectTTY's leading disconnectTTY closes
	// and nulls the socket unconditionally, so any 'DISCONNECTING' left
	// behind describes a socket that no longer exists - normalising it to
	// IDLE before the wait-loop check is what keeps the #83-released
	// activation paths (loadLog().finally(connectTTY) with a stale socket
	// attached) out of the ~1.05s spin. Anchor order: guard, then check.
	cStart := strings.Index(index, "function connectTTY(session) {")
	if cStart < 0 {
		t.Fatal("connectTTY no longer found")
	}
	cBody := index[cStart:]
	if end := strings.Index(cBody, "\n        }"); end >= 0 {
		cBody = cBody[:end]
	}
	vAt := strings.Index(cBody, "if (!session.ttySocket) session.ttyState = 'IDLE';")
	wAt := strings.Index(cBody, "if (session.ttyState !== 'IDLE') {")
	if vAt < 0 {
		t.Fatal("connectTTY must carry the FIX-V stale-state guard (issue #83)")
	}
	if wAt < 0 || vAt > wAt {
		t.Fatal("the FIX-V guard must sit BEFORE the 'ttyState !== IDLE' wait-loop check, or the spin survives")
	}

	// FIX-U/FIX-W: reconnectRunningTerminalSessions must (a) leave an
	// armed retry alone and (b) route ONLY heartbeat-stale sessions
	// through the helper - non-stale (mid-CONNECTING, READY+CLOSED)
	// sessions keep the pre-#83 direct connectTTY at their old latency.
	rStart := strings.Index(index, "function reconnectRunningTerminalSessions() {")
	if rStart < 0 {
		t.Fatal("reconnectRunningTerminalSessions no longer found")
	}
	rBody := index[rStart:]
	if end := strings.Index(rBody, "\n        }"); end >= 0 {
		rBody = rBody[:end]
	}
	if !strings.Contains(rBody, "if (session.ttyReconnectTimer) continue;") {
		t.Fatal("reconnectRunningTerminalSessions must not cancel an armed retry (FIX-W, issue #83)")
	}
	if !strings.Contains(rBody, "if (isTTYHeartbeatStale(session)) {\n                        reconnectStaleTTY(session);\n                    } else {\n                        connectTTY(session);\n                    }") {
		t.Fatal("reconnectRunningTerminalSessions must route only stale sessions through the helper; the else-branch keeps the direct connectTTY (FIX-U, issue #83)")
	}
}
