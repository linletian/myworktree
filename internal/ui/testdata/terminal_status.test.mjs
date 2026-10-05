// Behavioral tests for the instance-status handling in the terminal UI
// (issue #80): a newly created instance is observed as `starting` for a short
// window, and the frontend must neither paint it as stopped nor leave it
// without a transport.
//
// The logic under test lives in the web root (index.html's inline script and
// kinds/pty.js), so this file lives in testdata/ instead of static/ on
// purpose: static/* is embedded and served to every browser, and test code
// does not belong in the web root.
//
// The functions are sliced out of the real sources and evaluated against
// stubbed browser state, so the assertions run against the shipped code
// rather than a copy of it. Run: node --test testdata/terminal_status.test.mjs
// (also wired into terminal_status_test.go for `go test`).
import test from "node:test";
import assert from "node:assert/strict";
import { readFileSync } from "node:fs";
import { fileURLToPath } from "node:url";
import { dirname, join } from "node:path";

const here = dirname(fileURLToPath(import.meta.url));
const staticDir = join(here, "..", "static");
const indexSource = readFileSync(join(staticDir, "index.html"), "utf8");
const ptySource = readFileSync(join(staticDir, "kinds", "pty.js"), "utf8");

// --- source slicing helpers -------------------------------------------------

function inlineScript(html) {
  const start = html.lastIndexOf("<script>");
  const end = html.indexOf("</script>", start);
  assert.ok(start > 0 && end > start, "could not locate index.html's inline <script> block");
  return html.slice(start + "<script>".length, end);
}

// Slices a `function name(...) { ... }` declaration by brace matching.
function sliceBlock(src, header) {
  const start = src.indexOf(header);
  assert.notEqual(start, -1, `source no longer contains ${JSON.stringify(header)}`);
  let depth = 0;
  for (let i = src.indexOf("{", start); i < src.length; i++) {
    if (src[i] === "{") depth++;
    else if (src[i] === "}") {
      depth--;
      if (depth === 0) return src.slice(start, i + 1);
    }
  }
  throw new Error(`unbalanced braces while slicing ${JSON.stringify(header)}`);
}

// Quote/comment-aware variant of sliceBlock. parseTTYControlMessage holds
// the literal string "{" and a literal "}" comparison, which the naive brace
// counter of sliceBlock cannot balance; this variant skips over string
// literals, line comments and block comments while counting. It is used only
// by the issue #87 WS-cursor section so existing slices are untouched.
function sliceBlockQuoted(src, header) {
  const start = src.indexOf(header);
  assert.notEqual(start, -1, `source no longer contains ${JSON.stringify(header)}`);
  let depth = 0;
  let quote = null;
  for (let i = src.indexOf("{", start); i < src.length; i++) {
    const ch = src[i];
    if (quote) {
      if (ch === "\\") { i++; continue; }
      if (ch === quote) quote = null;
      continue;
    }
    if (ch === '"' || ch === "'" || ch === "`") { quote = ch; continue; }
    if (ch === "/" && src[i + 1] === "/") {
      i = src.indexOf("\n", i);
      if (i < 0) break;
      continue;
    }
    if (ch === "/" && src[i + 1] === "*") {
      i = src.indexOf("*/", i);
      if (i < 0) break;
      i++;
      continue;
    }
    if (ch === "{") depth++;
    else if (ch === "}") {
      depth--;
      if (depth === 0) return src.slice(start, i + 1);
    }
  }
  throw new Error(`unbalanced braces while slicing ${JSON.stringify(header)}`);
}

// Slices a single `const X = ...;` statement up to its terminating semicolon.
function sliceStatement(src, header) {
  const start = src.indexOf(header);
  assert.notEqual(start, -1, `source no longer contains ${JSON.stringify(header)}`);
  const end = src.indexOf(";", start);
  assert.notEqual(end, -1, `no statement terminator after ${JSON.stringify(header)}`);
  return src.slice(start, end + 1);
}

// --- index.html harness -----------------------------------------------------

const indexFactory = new Function("deps", `
  const {
    state, terminalSessions, window,
    disconnectTTY, loadLog, connectTTY, hasLiveTTYConnection, destroyTerminalSession,
  } = deps;
  ${[
    "const INSTANCE_LIVE_STATUSES",
    "const INSTANCE_PENDING_STATUSES",
  ].map(h => sliceStatement(inlineScript(indexSource), h)).join("\n")}
  ${[
    "function isInstanceLiveStatus",
    "function isInstancePendingStatus",
    "function isInstanceTerminalStatus",
    "function instanceStatusLabel",
    "function reconcileTerminalSessions",
    "function syncActiveTerminalStatus",
    "function hasTerminalTransportInFlight",
    "function ensureTerminalLiveTransport",
    "function maybeDestroyInactiveStoppedSession",
  ].map(h => sliceBlock(inlineScript(indexSource), h)).join("\n\n")}
  return {
    isInstanceLiveStatus,
    isInstancePendingStatus,
    isInstanceTerminalStatus,
    instanceStatusLabel,
    hasTerminalTransportInFlight,
    reconcileTerminalSessions,
    syncActiveTerminalStatus,
    ensureTerminalLiveTransport,
    maybeDestroyInactiveStoppedSession,
  };
`);

function makeSession(id, extra = {}) {
  return {
    id,
    term: {},
    container: { style: {} },
    ttyState: "IDLE",
    ttySocket: null,
    logStream: null,
    ttyReconnectTimer: null,
    lastKnownStatus: null,
    ...extra,
  };
}

// Fresh page state plus call recorders for every collaborator the sliced
// functions talk to.
function indexHarness({ instances = [], activeInst = null, sessions = [] } = {}) {
  const calls = { connect: [], disconnect: [], loadLog: [], destroy: [] };
  const state = { instances, activeInst };
  const window = { __ptyRenderer: { allSessions: () => sessions } };
  const api = indexFactory({
    state,
    terminalSessions: {},
    window,
    disconnectTTY: s => calls.disconnect.push(s.id),
    loadLog: (s) => { calls.loadLog.push(s.id); return Promise.resolve(); },
    // Mirrors the real connectTTY's two synchronous writes (index.html sets
    // ttyState before it constructs the socket). Without this the stub left
    // the session pristine and the guard under test was never exercised.
    connectTTY: s => {
      calls.connect.push(s.id);
      s.ttyState = "CONNECTING";
      s.ttySocket = { readyState: 0 };
    },
    hasLiveTTYConnection: id => !!sessions.find(s => s.id === id && s.live),
    destroyTerminalSession: (id) => {
      calls.destroy.push(id);
      const at = sessions.findIndex(s => s.id === id);
      if (at >= 0) sessions.splice(at, 1);
    },
  });
  return { api, calls, state, sessions };
}

// --- status classification --------------------------------------------------

test("the status-bar label follows the buckets for the web-UI kinds", () => {
  const { api } = indexHarness();
  // reasonix / dsh-web renderers keep their iframe navigating while the
  // instance is `starting`, so "stopped" next to a live page was a lie.
  assert.equal(api.instanceStatusLabel({ status: "running" }, "reasonix web"), "reasonix web");
  assert.equal(api.instanceStatusLabel({ status: "unhealthy" }, "reasonix web"), "reasonix web");
  assert.equal(api.instanceStatusLabel({ status: "starting" }, "reasonix web"), "starting...");
  assert.equal(api.instanceStatusLabel({ status: "stopping" }, "dsh web ui"), "stopping...");
  assert.equal(api.instanceStatusLabel({ status: "stopped" }, "dsh web ui"), "stopped");
  assert.equal(api.instanceStatusLabel({ status: "failed" }, "dsh web ui"), "stopped");
  assert.equal(api.instanceStatusLabel(null, "dsh web ui"), "stopped");
});

test("every framework status lands in exactly one bucket", () => {
  const { api } = indexHarness();
  const expected = {
    running: "live",
    unhealthy: "live",
    starting: "pending",
    stopping: "pending",
    stopped: "terminal",
    failed: "terminal",
    exited: "terminal",
  };
  for (const [status, bucket] of Object.entries(expected)) {
    const bucketOf = {
      live: api.isInstanceLiveStatus(status),
      pending: api.isInstancePendingStatus(status),
      terminal: api.isInstanceTerminalStatus(status),
    };
    const matched = Object.keys(bucketOf).filter(k => bucketOf[k]);
    assert.deepEqual(matched, [bucket], `status ${status} must be ${bucket}, got ${matched}`);
  }
  // An unknown or absent status is treated as terminal: the UI must not keep
  // a transport open on a record it cannot classify.
  for (const status of [undefined, null, "", "bogus"]) {
    assert.equal(api.isInstanceTerminalStatus(status), true, `status ${status} should be terminal`);
    assert.equal(api.isInstanceLiveStatus(status), false);
    assert.equal(api.isInstancePendingStatus(status), false);
  }
});

// --- the issue #80 regression: starting instance must not be stranded -------

test("issue #80: a starting instance is neither disconnected nor painted as stopped", () => {
  const session = makeSession("inst1", { lastKnownStatus: "starting" });
  const { api, calls } = indexHarness({
    instances: [{ id: "inst1", kind: "pty", status: "starting" }],
    activeInst: "inst1",
    sessions: [session],
  });

  api.reconcileTerminalSessions();

  assert.deepEqual(calls.disconnect, [], "a pending instance must keep its transport state");
  assert.deepEqual(calls.loadLog, [], "a pending instance must not be replayed as a stopped one");
  assert.deepEqual(calls.connect, [], "a pending instance has nothing to connect to yet");
});

test("issue #80: the next tick promotes the starting instance once it turns live", () => {
  const session = makeSession("inst1", { lastKnownStatus: "starting" });
  const instances = [{ id: "inst1", kind: "pty", status: "starting" }];
  const { api, calls } = indexHarness({ instances, activeInst: "inst1", sessions: [session] });

  // Tick 1: still starting — nothing happens.
  api.reconcileTerminalSessions();
  assert.deepEqual(calls.connect, []);

  // Tick 2: the ready signal landed.
  instances[0].status = "running";
  api.reconcileTerminalSessions();
  assert.deepEqual(calls.connect, ["inst1"], "the live status must promote the session");
  assert.equal(session.lastKnownStatus, "running");
});

test("the promotion does not flap an in-flight connection", () => {
  // hasLiveTTYConnection() is false while the socket is still CONNECTING, so
  // a weaker guard would tear the WebSocket down and re-open it every 2s.
  // Each case is the active session on its own, so every guard in
  // ensureTerminalLiveTransport() is individually load-bearing.
  const cases = [
    ["a CONNECTING socket", { ttyState: "CONNECTING", ttySocket: {} }],
    ["a CONNECTING state with no socket yet", { ttyState: "CONNECTING" }],
    ["a DISCONNECTING state", { ttyState: "DISCONNECTING" }],
    ["a socket assigned before the state moves", { ttySocket: {} }],
    ["a live SSE stream", { logStream: {} }],
    ["a pending reconnect timer", { ttyReconnectTimer: 42 }],
    ["an activation awaiting its log load", { loadLogController: {} }],
    ["an already READY socket", { live: true, ttySocket: {}, ttyState: "READY" }],
  ];
  for (const [label, extra] of cases) {
    const session = makeSession("inst1", extra);
    const { api, calls } = indexHarness({
      instances: [{ id: "inst1", status: "running" }],
      activeInst: "inst1",
      sessions: [session],
    });
    api.ensureTerminalLiveTransport(session);
    assert.deepEqual(calls.connect, [], `must not reconnect a session with ${label}`);
  }
});

test("the promotion connects once and then leaves the session to its own bring-up", () => {
  const idle = makeSession("inst1");
  const live = makeSession("inst2", { live: true, ttySocket: {}, ttyState: "READY" });
  const instances = [{ id: "inst1", status: "running" }, { id: "inst2", status: "running" }];
  const { api, calls } = indexHarness({ activeInst: "inst1", instances, sessions: [idle, live] });

  api.ensureTerminalLiveTransport(idle);
  api.ensureTerminalLiveTransport(live);
  api.ensureTerminalLiveTransport(idle); // caller asked twice

  assert.deepEqual(calls.connect, ["inst1"], "a second ask must not open a second socket");
  assert.equal(idle.ttyState, "CONNECTING", "the session is handed to the connect that was started");
});

test("the promotion refuses sessions that are not the active tab", () => {
  const session = makeSession("inst1");
  const { api, calls } = indexHarness({
    instances: [{ id: "inst1", status: "running" }],
    activeInst: "other",
    sessions: [session],
  });
  api.ensureTerminalLiveTransport(session);
  assert.deepEqual(calls.connect, []);
});

// --- terminal transitions ---------------------------------------------------

test("entering a terminal status disconnects and replays the log exactly once", () => {
  const session = makeSession("inst1", { lastKnownStatus: "running" });
  const instances = [{ id: "inst1", status: "running" }];
  const { api, calls } = indexHarness({ activeInst: "inst1", instances, sessions: [session] });

  instances[0].status = "exited"; // crashed on its own
  api.reconcileTerminalSessions();
  assert.deepEqual(calls.disconnect, ["inst1"]);
  assert.deepEqual(calls.loadLog, ["inst1"], "the crash must render the log plus the stopped banner");

  // A second tick while it stays stopped must not refetch / repaint.
  api.reconcileTerminalSessions();
  assert.deepEqual(calls.loadLog, ["inst1"], "the log replay must happen once, not on every poll");
  assert.deepEqual(calls.disconnect, ["inst1", "inst1"], "disconnect is idempotent and cheap");
});

test("a failed terminal replay backs off instead of retrying every tick", () => {
  // loadLog() reports its failures into the terminal, so an unbounded 2s
  // retry would append one error line per tick forever.
  const session = makeSession("inst1", { lastKnownStatus: "running" });
  const instances = [{ id: "inst1", status: "running" }];
  const { api, calls } = indexHarness({ activeInst: "inst1", instances, sessions: [session] });

  instances[0].status = "stopped";
  api.reconcileTerminalSessions();
  assert.equal(calls.loadLog.length, 1);

  session.lastLogLoadFailed = true;
  session.lastLogLoadFailures = 3; // next retry is 8s away
  session.lastLogLoadAttemptAt = Date.now();
  api.reconcileTerminalSessions();
  assert.equal(calls.loadLog.length, 1, "not yet due");

  api.reconcileTerminalSessions();
  api.reconcileTerminalSessions();
  assert.equal(calls.loadLog.length, 1, "still not due, however many ticks pass");

  session.lastLogLoadAttemptAt = Date.now() - 8001;
  api.reconcileTerminalSessions();
  assert.equal(calls.loadLog.length, 2, "due once the backoff elapsed");

  session.lastLogLoadFailed = false;
  api.reconcileTerminalSessions();
  assert.equal(calls.loadLog.length, 2, "a success stops the retry loop for good");
});

test("a start that fails while pending still gets the stopped banner", () => {
  const session = makeSession("inst1", { lastKnownStatus: "starting" });
  const instances = [{ id: "inst1", status: "starting" }];
  const { api, calls } = indexHarness({ activeInst: "inst1", instances, sessions: [session] });

  api.reconcileTerminalSessions(); // starting
  instances[0].status = "failed";
  api.reconcileTerminalSessions();

  assert.deepEqual(calls.disconnect, ["inst1"]);
  assert.deepEqual(calls.loadLog, ["inst1"]);
});

test("a failed terminal replay keeps being retried until one lands", () => {
  // loadLog swallows its own errors, so the latch needs its own flag: without
  // it the once-per-transition replay never runs again and the tab is left
  // with neither log nor banner until someone hits Refresh by hand.
  const session = makeSession("inst1", { lastKnownStatus: "running" });
  const instances = [{ id: "inst1", status: "running" }];
  const { api, calls } = indexHarness({ activeInst: "inst1", instances, sessions: [session] });

  instances[0].status = "stopped";
  api.reconcileTerminalSessions();
  assert.equal(calls.loadLog.length, 1, "first attempt on entry to the terminal bucket");

  api.reconcileTerminalSessions();
  assert.equal(calls.loadLog.length, 1, "no retry while the last attempt succeeded");

  session.lastLogLoadFailed = true; // the fetch rejected
  api.reconcileTerminalSessions();
  assert.equal(calls.loadLog.length, 2, "a failed attempt must be retried");

  session.lastLogLoadFailed = false;
  api.reconcileTerminalSessions();
  assert.equal(calls.loadLog.length, 2, "and the retry stops once one succeeds");
});

test("an unhealthy instance keeps its transport instead of being torn down", () => {
  const session = makeSession("inst1", { lastKnownStatus: "running" });
  const instances = [{ id: "inst1", status: "running" }];
  const { api, calls } = indexHarness({ activeInst: "inst1", instances, sessions: [session] });

  instances[0].status = "unhealthy";
  api.reconcileTerminalSessions();

  assert.deepEqual(calls.disconnect, [], "unhealthy means the process is still alive");
  assert.deepEqual(calls.loadLog, [], "unhealthy must not repaint the screen as stopped");
  assert.deepEqual(calls.connect, ["inst1"], "unhealthy instances get a transport like running ones");
});

// --- inactive sessions ------------------------------------------------------

test("inactive sessions are destroyed only once the status is terminal", () => {
  const starting = makeSession("a1");
  const stopped = makeSession("a2");
  const live = makeSession("a3");
  const { api, calls } = indexHarness({
    instances: [
      { id: "a1", status: "starting" },
      { id: "a2", status: "stopped" },
      { id: "a3", status: "running" },
    ],
    activeInst: "a0",
    sessions: [starting, stopped, live],
  });

  api.reconcileTerminalSessions();
  assert.deepEqual(calls.destroy, ["a2"], "only terminal-bucket inactive sessions are destroyed");
});

test("a vanished instance destroys its session", () => {
  const session = makeSession("a1");
  const { api, calls } = indexHarness({ instances: [], activeInst: "a0", sessions: [session] });
  api.reconcileTerminalSessions();
  assert.deepEqual(calls.destroy, ["a1"]);
});

test("maybeDestroyInactiveStoppedSession keeps a starting instance alive", () => {
  const starting = makeSession("a1");
  const stopped = makeSession("a2");
  const { api, calls } = indexHarness({
    instances: [{ id: "a1", status: "starting" }, { id: "a2", status: "stopped" }],
    activeInst: "a0",
    sessions: [starting, stopped],
  });
  api.maybeDestroyInactiveStoppedSession("a1");
  api.maybeDestroyInactiveStoppedSession("a2");
  api.maybeDestroyInactiveStoppedSession("a0"); // the active tab is never destroyed
  assert.deepEqual(calls.destroy, ["a2"]);
});

// --- loadLog: the [Process Stopped] banner itself ---------------------------

const STOPPED_BANNER = "\r\n\r\n\x1b[41;37m[Process Stopped]\x1b[0m\r\n";

// loadLog is sliced separately: it is the only place that paints the banner,
// so it is worth exercising against a stubbed fetch rather than stubbed out.
const loadLogFactory = new Function("deps", `
  const {
    state, fetch, headers, rememberServerRevision, AbortController,
    reportSessionError, writeSanitizedTerminalOutput,
  } = deps;
  ${[
    "const INSTANCE_LIVE_STATUSES",
    "const INSTANCE_PENDING_STATUSES",
    // The two cursor states loadLog chooses between (issue #86).
    "const CURSOR_UNKNOWN",
    "const CURSOR_FOLLOW_LIVE_END",
  ].map(h => sliceStatement(inlineScript(indexSource), h)).join("\n")}
  ${[
    "function isInstanceLiveStatus",
    "function isInstancePendingStatus",
    "function isInstanceTerminalStatus",
    "async function loadLog",
  ].map(h => sliceBlock(inlineScript(indexSource), h)).join("\n\n")}
  return { loadLog };
`);

function loadLogHarness(status, { offset = "42", fail = false } = {}) {
  const writes = [];
  const resets = { count: 0 };
  const session = makeSession("inst1", {
    term: {
      reset: () => { resets.count++; },
      write: text => writes.push(text),
    },
  });
  const requests = [];
  const errors = [];
  const api = loadLogFactory({
    state: { instances: [{ id: "inst1", status }], activeInst: "inst1" },
    fetch: (url) => {
      requests.push(url);
      if (fail) return Promise.reject(new Error("network down"));
      return Promise.resolve({
        ok: true,
        text: () => Promise.resolve("log bytes"),
        headers: { get: (name) => (name === "X-Log-Offset" ? offset : null) },
      });
    },
    headers: {},
    rememberServerRevision: () => {},
    AbortController,
    reportSessionError: (s, what, err) => errors.push(`${what}: ${err}`),
    // Mirrors the REAL writeSanitizedTerminalOutput contract: sanitizes,
    // writes nothing when the result is empty, and RETURNS the byte count the
    // caller uses to decide whether the screen holds anything (issue #86).
    writeSanitizedTerminalOutput: (s, text) => {
      if (!text) return 0;
      writes.push(text);
      return text.length;
    },
  });
  return { api, session, writes, resets, requests, errors };
}

test("loadLog latches its own outcome so a failed replay can be retried", async () => {
  // The reconciler replays the terminal bucket once per transition, and
  // loadLog swallows its own errors — so without this latch a transient fetch
  // failure would leave the tab with neither log nor banner, and nothing would
  // ever ask again.
  const failed = loadLogHarness("stopped", { fail: true });
  await failed.api.loadLog(failed.session);
  assert.equal(failed.session.lastLogLoadFailed, true);
  assert.equal(failed.errors.length, 1, "the failure is still reported to the user");
  assert.deepEqual(failed.writes, [], "and nothing was painted");

  // loadLog owns the backoff inputs; the reconciler only reads them, so the
  // counter and the timestamp have to be set here to mean anything.
  assert.equal(failed.session.lastLogLoadFailures, 1, "one failure counted");
  assert.ok(failed.session.lastLogLoadAttemptAt > 0, "the attempt is timestamped");

  const ok = loadLogHarness("stopped");
  await ok.api.loadLog(ok.session);
  assert.equal(ok.session.lastLogLoadFailed, false);
  assert.equal(ok.session.lastLogLoadFailures, 0, "a success resets the backoff");
  assert.ok(ok.writes.length > 0);
});

test("loadLog paints [Process Stopped] only for a terminal status", async () => {
  const expected = {
    running: { banner: false, reset: false },
    unhealthy: { banner: false, reset: false },
    starting: { banner: false, reset: false },
    stopping: { banner: false, reset: false },
    stopped: { banner: true, reset: true },
    failed: { banner: true, reset: true },
    exited: { banner: true, reset: true },
  };
  for (const [status, want] of Object.entries(expected)) {
    const { api, session, writes, resets } = loadLogHarness(status);
    await api.loadLog(session);
    const bannered = writes.some(w => w.includes("[Process Stopped]"));
    assert.equal(bannered, want.banner, `status ${status}: banner expectation`);
    assert.equal(resets.count, want.reset ? 1 : 0, `status ${status}: terminal reset expectation`);
    // The log itself is always replayed.
    assert.ok(writes.includes("log bytes"), `status ${status}: the log must still be written`);
  }
});

test("loadLog keeps the byte cursor in step with the replay (issue #81 contract)", async () => {
  const { api, session } = loadLogHarness("running", { offset: "4096" });
  await api.loadLog(session);
  assert.equal(session.logCursor, 4096);

  // A stripped X-Log-Offset must not reset the cursor to 0: startSSE would
  // then replay the oldest 64 KB on top of the tail just written. Issue
  // #86 gave that case its own state — the tail IS painted, only its
  // offset is unknown — so the answer is the follow-live-end sentinel, and
  // a stale positive cursor is not retained either: it describes a screen
  // this replay has already overwritten.
  const missing = loadLogHarness("running", { offset: null });
  missing.session.logCursor = 1234;
  await missing.api.loadLog(missing.session);
  assert.equal(missing.session.logCursor, -2);
});

// --- kinds/pty.js activate() ------------------------------------------------

function ptyHarness({ doc } = {}) {
  const calls = { connect: [], disconnect: [], loadLog: [], focus: [], reset: [], status: [] };
  const window = {
    state: { instances: [], activeInst: null },
    registerRenderer(kind, renderer) {
      this.KindRenderers = this.KindRenderers || {};
      this.KindRenderers[kind] = renderer;
    },
    isInstanceLiveStatus: s => s === "running" || s === "unhealthy",
    isInstancePendingStatus: s => s === "starting" || s === "stopping",
    isInstanceTerminalStatus: s => !(s === "running" || s === "unhealthy" || s === "starting" || s === "stopping"),
    hasLiveTTYConnection: () => false,
    resetTerminalForSwitch: s => calls.reset.push(s.id),
    disconnectTTY: s => calls.disconnect.push(s.id),
    loadLog: (s) => { calls.loadLog.push(s.id); return Promise.resolve(); },
    // Mirrors the real connectTTY, which begins by disconnecting and thereby
    // cancelling a queued retry timer.
    connectTTY: s => { calls.connect.push(s.id); s.ttyReconnectTimer = null; },
    focusTerminalIfPossible: () => calls.focus.push(true),
    updateStatus: text => calls.status.push(text),
  };
  const document = doc || { getElementById: () => null };
  const load = new Function("window", "document", "WebSocket", "ResizeObserver", ptySource);
  load(window, document, { OPEN: 1 }, class {
    observe() {}
    disconnect() {}
  });
  // The in-flight guard is shared with index.html (one definition, both paths);
  // these tests drive the sliced copy so the two cannot drift apart.
  const api = indexFactory({
    state: window.state,
    terminalSessions: {},
    window,
    disconnectTTY: () => {},
    loadLog: () => Promise.resolve(),
    connectTTY: () => {},
    hasLiveTTYConnection: () => false,
    destroyTerminalSession: () => {},
  });
  return { renderer: window.__ptyRenderer, window, calls, api };
}

function activateWith(status, extra = {}, { pendingLoad = false } = {}) {
  const h = ptyHarness();
  if (pendingLoad) {
    // Hold loadLog open: the window in which the queued retry used to still be
    // armed, because activate() only reaches disconnectTTY after it settles.
    let release;
    h.window.loadLog = (s) => {
      h.calls.loadLog.push(s.id);
      return new Promise((resolve) => { release = resolve; });
    };
    h.window.__releaseLoadLog = () => release();
  }
  const session = makeSession("inst1", extra);
  h.window.state.instances = [{ id: "inst1", kind: "pty", status }];
  h.window.state.activeInst = "inst1";
  // Both arguments must be forwarded: dropping `options` here would silently
  // disable the ignoreQueuedRetry handover the activation path asks for.
  h.window.hasTerminalTransportInFlight = (s, o) => h.api.hasTerminalTransportInFlight(s, o);
  h.renderer._sessions.set("inst1", session);
  h.renderer.activate(session, null);
  return { ...h, session };
}

test("pty activate(): a starting instance is left pending, not stopped", () => {
  const { calls } = activateWith("starting");
  assert.deepEqual(calls.disconnect, []);
  assert.deepEqual(calls.loadLog, [], "no stopped replay for a transient status");
  assert.deepEqual(calls.connect, [], "connectTTY is refused for pending; reconcile promotes later");
  assert.deepEqual(calls.status, ["starting..."]);
});

test("pty activate(): a stopping instance is left pending too", () => {
  const { calls } = activateWith("stopping");
  assert.deepEqual(calls.loadLog, []);
  assert.deepEqual(calls.status, ["stopping..."]);
});

test("pty activate(): a genuinely stopped instance still gets the replay and banner", () => {
  const { calls, session } = activateWith("stopped");
  assert.deepEqual(calls.disconnect, ["inst1"]);
  assert.deepEqual(calls.loadLog, ["inst1"]);
  assert.deepEqual(calls.status, ["stopped"]);
  assert.equal(session.lastKnownStatus, "stopped", "activate records the status so reconcile does not replay twice");
});

test("pty activate(): an unhealthy instance takes the live path", () => {
  const { calls, session } = activateWith("unhealthy");
  assert.deepEqual(calls.loadLog, ["inst1"]);
  assert.deepEqual(calls.disconnect, [], "an unhealthy process must not be disconnected");
  assert.deepEqual(calls.reset, ["inst1"]);
  assert.equal(session.logCursor, -1, "the cleared screen resets the cursor to unknown");
});

test("pty activate(): a running instance connects and records its status", () => {
  const { calls, session } = activateWith("running");
  assert.deepEqual(calls.reset, ["inst1"]);
  assert.equal(session.lastKnownStatus, "running");
  assert.deepEqual(calls.disconnect, []);
});

test("pty activate(): the in-flight early return says something", () => {
  // deactivate() leaves the status bar on "idle", so a silent return makes
  // re-selecting a tab look inert until the in-flight connect finishes.
  const connecting = activateWith("running", { ttyState: "CONNECTING" });
  assert.equal(connecting.calls.status.at(-1), "connecting...");

  const replaying = activateWith("running", { loadLogController: {} });
  assert.equal(replaying.calls.status.at(-1), "connecting...");

  // An SSE session is live, not connecting: do not overwrite its message.
  const streaming = activateWith("running", { logStream: {} });
  assert.deepEqual(streaming.calls.status, [], "the live SSE message must survive the guard");
});

test("pty activate(): a queued reconnect does not freeze an explicit re-selection", async () => {
  // The user asked for this tab. Waiting out the 5s timer left it on the
  // pre-drop screen, and the handshake replay then painted the last screenful
  // a second time — so activation takes the handover, and connectTTY's own
  // disconnect cancels the pending timer.
  const { calls, session } = activateWith("running", { ttyReconnectTimer: 42 });
  assert.deepEqual(calls.reset, ["inst1"], "the screen is cleared for the reconnect");
  assert.deepEqual(calls.loadLog, ["inst1"]);

  await new Promise(r => setTimeout(r, 0));
  assert.deepEqual(calls.connect, ["inst1"], "and the handover connects");
  assert.equal(session.ttyReconnectTimer, null, "the queued retry is cancelled, not left to fire");
});

test("pty activate(): the handover cancels the retry before loadLog settles", async () => {
  // The window that made the handover racy: connectTTY — and therefore
  // disconnectTTY, the only thing that clears ttyReconnectTimer — does not run
  // until loadLog resolves. A retry firing inside that window opened one
  // socket and replayed the tail, and the connect that followed opened a
  // second and replayed it again.
  const { window, session, calls } = activateWith("running", { ttyReconnectTimer: 42 }, { pendingLoad: true });
  assert.equal(session.ttyReconnectTimer, null, "the retry is cancelled at the takeover, not at connect time");

  window.__releaseLoadLog();
  await new Promise(r => setTimeout(r, 0));
  assert.deepEqual(calls.connect, ["inst1"], "so exactly one connect ever happens");
});

test("pty activate(): the handover still yields to its own loadLog and its socket", () => {
  // ignoreQueuedRetry is scoped to the timer arm alone: an activation already
  // bootstrapping must not be raced by a second one.
  for (const [label, extra] of [
    ["an activation awaiting its log load", { loadLogController: {}, ttyReconnectTimer: 42 }],
    ["a CONNECTING socket", { ttyState: "CONNECTING", ttySocket: {}, ttyReconnectTimer: 42 }],
    ["a live SSE stream", { logStream: {}, ttyReconnectTimer: 42 }],
  ]) {
    const { calls } = activateWith("running", extra);
    assert.deepEqual(calls.reset, [], `must not clear the screen with ${label}`);
    assert.deepEqual(calls.connect, [], `must not connect on top of ${label}`);
  }
});

test("the poll-driven promotion still stands aside for a queued retry", () => {
  // The asymmetry is deliberate: the promotion is a bystander that would flap
  // the connection on every tick, so it passes no options and waits.
  const session = makeSession("inst1", { ttyReconnectTimer: 42 });
  const instances = [{ id: "inst1", status: "running" }];
  const { api, calls } = indexHarness({ activeInst: "inst1", instances, sessions: [session] });

  assert.equal(api.ensureTerminalLiveTransport(session), false, "the timer owns this session");
  assert.deepEqual(calls.connect, []);
  assert.equal(api.hasTerminalTransportInFlight(session, { ignoreQueuedRetry: true }), false,
    "activation is the caller that may take over");
  assert.equal(api.hasTerminalTransportInFlight(session), true);
});

test("pty activate(): classifies without the window helpers published", () => {
  // The inline fallbacks exist for a page that has not exported the bucket
  // helpers. All three must be guarded the same way — an unguarded call here
  // throws and takes the whole activation down.
  for (const status of ["starting", "stopping", "running", "unhealthy", "stopped", "failed", "exited"]) {
    const h = ptyHarness();
    delete h.window.isInstanceLiveStatus;
    delete h.window.isInstancePendingStatus;
    delete h.window.isInstanceTerminalStatus;
    delete h.window.hasTerminalTransportInFlight;
    const session = makeSession("inst1");
    h.window.state.instances = [{ id: "inst1", kind: "pty", status }];
    h.window.state.activeInst = "inst1";
    h.renderer._sessions.set("inst1", session);
    assert.doesNotThrow(() => h.renderer.activate(session, null), `status ${status}`);
  }
});

test("pty activate(): a tab switch during bring-up does not start a second one", () => {
  // A promotion may be mid-connect when the user switches back to the tab.
  // hasLiveTTYConnection() is false while the socket is CONNECTING, so
  // without the in-flight guard activate() would clear the screen and open a
  // competing socket — replaying the tail twice (issue #87).
  for (const [label, extra] of [
    ["a CONNECTING socket", { ttyState: "CONNECTING", ttySocket: {} }],
    ["an activation awaiting its log load", { loadLogController: {} }],
    ["a live SSE stream", { logStream: {} }],
  ]) {
    const { calls, session } = activateWith("running", extra);
    assert.deepEqual(calls.reset, [], `must not clear the screen with ${label}`);
    assert.deepEqual(calls.loadLog, [], `must not replay the log with ${label}`);
    assert.deepEqual(calls.disconnect, [], `must not drop the transport with ${label}`);
    // The status is recorded either way, so the reconciler keeps its say.
    assert.equal(session.lastKnownStatus, "running");
  }
});

// --- kinds/pty.js input path ------------------------------------------------

// The keystroke guard is the other half of issue #80: while the status read
// "starting", onData dropped every keystroke instead of forwarding it.
function ptySessionHarness(status) {
  const posts = [];
  let onDataHandler = null;
  const termContainer = { appendChild() {} };
  const h = ptyHarness({
    doc: {
      getElementById: () => termContainer,
      createElement: () => ({ dataset: {}, style: {}, appendChild() {} }),
    },
  });
  h.window.state.instances = [{ id: "inst1", kind: "pty", status }];
  h.window.state.activeInst = "inst1";
  h.window.api = (url, opts) => { posts.push({ url, opts }); return Promise.resolve(); };
  h.window.Terminal = class {
    constructor() { this.cols = 80; this.rows = 24; }
    loadAddon() {}
    open() {}
    resize() {}
    onData(cb) { onDataHandler = cb; return { dispose() {} }; }
  };
  const session = h.renderer._createSession("inst1", termContainer);
  // The factory batches keystrokes and flushes them on a short timer.
  const type = async (data) => {
    onDataHandler(data);
    await new Promise(resolve => setTimeout(resolve, 60));
  };
  return { posts, session, type };
}

test("pty input: a starting instance still forwards keystrokes", async () => {
  const h = ptySessionHarness("starting");
  await h.type("ls\r");
  assert.equal(h.posts.length, 1, "keystrokes must reach the process while it is starting");
  assert.equal(h.posts[0].url, "/api/instances/input");
  assert.match(h.posts[0].opts.body, /"input":"ls\\r"/);
});

test("pty input: a live or unhealthy instance forwards keystrokes", async () => {
  for (const status of ["running", "unhealthy"]) {
    const h = ptySessionHarness(status);
    await h.type("x");
    assert.equal(h.posts.length, 1, `status ${status} must accept input`);
  }
});

test("pty input: a stopped instance swallows keystrokes", async () => {
  for (const status of ["stopped", "failed", "exited"]) {
    const h = ptySessionHarness(status);
    await h.type("x");
    assert.equal(h.posts.length, 0, `status ${status} must not accept input`);
  }
});
// --- WS byte cursor (issue #87) ---------------------------------------------

// connectTTY is sliced out of the shipped index.html and driven against a
// fake WebSocket so the URL construction, the `sync` offset echo handling
// and the cursor accounting run against real code. writeSanitizedTerminalOutput
// and decodeTTYOutputChunk are stubbed only to observe the calls; the
// cursor arithmetic itself lives in the sliced onmessage/onclose handlers.
const ttyWsFactory = new Function("deps", `
  const {
    state, updateStatus, token, window, location, WebSocket,
    disconnectTTY, resetTTYOutputDecoder, applyTTYSize, sendResize,
    syncTerminalToAppliedSize, focusTerminalIfPossible,
    writeSanitizedTerminalOutput, decodeTTYOutputChunk, startSSE,
    setTimeout, clearTimeout, setInterval, clearInterval, console,
  } = deps;
  ${[
    "const INSTANCE_LIVE_STATUSES",
    "const INSTANCE_PENDING_STATUSES",
    "const CURSOR_UNKNOWN",
    "const CURSOR_FOLLOW_LIVE_END",
  ].map(h => sliceStatement(inlineScript(indexSource), h)).join("\n")}
  ${[
    "function isInstanceLiveStatus",
    "function isInstancePendingStatus",
    "function parseTTYControlMessage",
    "function connectTTY",
  ].map(h => sliceBlockQuoted(inlineScript(indexSource), h)).join("\n\n")}
  return { connectTTY, parseTTYControlMessage };
`);

// Fake timers: every scheduled callback is recorded and only runs when the
// test fires it, so nothing in the 5s/10s handshake windows can delay or
// nondeterministically drive the assertions.
function ttyWsHarness({ ttyOffset = -1, status = "running", token = "" } = {}) {
  const sockets = [];
  const writes = [];
  const timers = [];
  let nextTimerId = 1;
  class FakeWebSocket {
    constructor(url) {
      this.url = url;
      this.readyState = 0; // CONNECTING
      this.binaryType = "";
      this.sent = [];
      sockets.push(this);
    }
    send(data) { this.sent.push(data); }
    close() {
      if (this.readyState === 3) return; // already CLOSED
      this.readyState = 3;
      if (this.onclose) this.onclose();
    }
  }
  FakeWebSocket.OPEN = 1;
  const session = makeSession("inst1", { ttyOffset });
  const fireTimers = (ms) => {
    for (const t of timers.filter(t => t.ms === ms)) {
      const at = timers.indexOf(t);
      if (at >= 0) timers.splice(at, 1); // one-shot semantics
      t.fn();
    }
  };
  const api = ttyWsFactory({
    state: { instances: [{ id: "inst1", kind: "pty", status }], activeInst: "inst1" },
    updateStatus: () => {},
    token,
    window: { WebSocket: FakeWebSocket },
    location: { protocol: "http:", host: "ws.test" },
    WebSocket: FakeWebSocket,
    // Mirrors the real disconnectTTY's state handling for the paths the
    // connect/reconnect flow actually takes (no socket in flight here).
    disconnectTTY: (s) => {
      s.ttyState = s.ttySocket ? "DISCONNECTING" : "IDLE";
      s.ttySocket = null;
      s.ttyReconnectTimer = null;
    },
    resetTTYOutputDecoder: () => {},
    applyTTYSize: () => {},
    sendResize: () => {},
    syncTerminalToAppliedSize: () => {},
    focusTerminalIfPossible: () => {},
    decodeTTYOutputChunk: (s, bytes) => new TextDecoder().decode(bytes),
    // Mirrors the REAL writeSanitizedTerminalOutput contract: sanitizes,
    // writes nothing when the result is empty, and RETURNS the byte count the
    // caller uses to decide whether the screen holds anything (issue #86).
    writeSanitizedTerminalOutput: (s, text) => {
      if (!text) return 0;
      writes.push(text);
      return text.length;
    },
    startSSE: () => { throw new Error("must not fall back to SSE while a WebSocket is available"); },
    setTimeout: (fn, ms) => { const t = { id: nextTimerId++, fn, ms }; timers.push(t); return t.id; },
    clearTimeout: (id) => { const at = timers.findIndex(t => t.id === id); if (at >= 0) timers.splice(at, 1); },
    setInterval: () => 0,
    clearInterval: () => {},
    console: { warn: () => {}, error: () => {}, log: () => {} },
  });
  const enc = new TextEncoder();
  return { api, session, sockets, writes, timers, fireTimers, enc, connect: () => api.connectTTY(session) };
}

test("issue #87: connectTTY sends `since` only for a known positive cursor", () => {
  for (const [label, offset, want] of [
    ["unknown cursor (-1)", -1, false],
    ["zero cursor (would mean oldest live byte)", 0, false],
    ["real cursor", 4096, true],
  ]) {
    const h = ttyWsHarness({ ttyOffset: offset });
    h.connect();
    const url = h.sockets[0].url;
    assert.ok(url.includes(`/api/instances/tty/ws?id=inst1`), `${label}: base URL`);
    assert.equal(url.includes("since="), want, `${label}: since presence`);
    if (want) assert.ok(url.includes("&since=4096"), `${label}: since value`);
  }
});

test("issue #87: binary frames move the cursor only after the sync latch (FIX-B)", () => {
  const h = ttyWsHarness();
  h.connect();
  const ws = h.sockets[0];
  ws.readyState = 1;
  ws.onopen();
  ws.onmessage({ data: '{"type":"ready"}' });
  assert.equal(h.session.ttyState, "READY");

  // A binary frame BEFORE the sync echo belongs to the handshake replay:
  // it renders, but must NOT touch the cursor. The sync frame that
  // follows on the same TCP stream publishes the authoritative end offset
  // of the entire replay in one step; counting replay bytes here would
  // leave a behind-by-one-chunk value if the socket died in the
  // replay→sync gap, duplicating that window on the next reconnect.
  ws.onmessage({ data: h.enc.encode("tail-bytes").buffer });
  assert.equal(h.session.ttyOffset, -1, "pre-sync replay frames leave the sentinel untouched");
  assert.deepEqual(h.writes, ["tail-bytes"], "the bytes still render");

  ws.onmessage({ data: '{"type":"sync","offset":4242}' });
  assert.equal(h.session.ttyOffset, 4242, "the sync echo is the authoritative cursor");
  assert.deepEqual(h.writes, ["tail-bytes"], "the sync frame must not be painted as text");

  // AFTER the latch every binary frame is live ring-buffer output (the
  // server closes the connection instead of writing diagnostics as
  // binary), so the wire length is exactly the ring-buffer advance.
  ws.onmessage({ data: h.enc.encode(" more").buffer });
  assert.equal(h.session.ttyOffset, 4247, "post-sync binary frames advance by byte count");

  // Junk offsets must not move a latched cursor.
  for (const junk of ['{"type":"sync"}', '{"type":"sync","offset":-5}', '{"type":"sync","offset":"12"}']) {
    ws.onmessage({ data: junk });
    assert.equal(h.session.ttyOffset, 4247, `${junk} must not move the cursor`);
  }

  // And the whitelist still rejects unknown control types.
  assert.equal(h.api.parseTTYControlMessage('{"type":"bogus"}'), null);
});

test("issue #87: the cursor survives the drop and the reconnect resumes from it", () => {
  const h = ttyWsHarness();
  h.connect();
  const ws = h.sockets[0];
  assert.ok(!ws.url.includes("since="), "first connect has no cursor yet");
  ws.readyState = 1;
  ws.onopen();
  ws.onmessage({ data: '{"type":"ready"}' });
  ws.onmessage({ data: '{"type":"sync","offset":9}' });
  ws.onmessage({ data: h.enc.encode("live").buffer });
  assert.equal(h.session.ttyOffset, 13);

  // Drop the socket — the screen keeps its contents, so the cursor must
  // survive for the queued reconnect to use (the crux of issue #87).
  ws.close();
  assert.equal(h.session.ttyOffset, 13, "onclose must preserve the cursor");
  assert.equal(h.session.ttySocket, null, "the dead socket is dropped");

  // Fire the queued 5s reconnect: it carries the cursor as `since`.
  h.fireTimers(5000);
  assert.equal(h.sockets.length, 2, "the reconnect opened a new socket");
  assert.ok(h.sockets[1].url.includes("&since=13"),
    `reconnect must resume from the cursor, got ${h.sockets[1].url}`);

  // The new connection starts with its own fresh latch: replay bytes
  // arriving before ITS sync frame must not touch the cursor (FIX-B) —
  // counting them would leave the cursor short by one replay chunk if
  // this socket died in the replay→sync gap, duplicating that window on
  // the next reconnect.
  const ws2 = h.sockets[1];
  ws2.readyState = 1;
  ws2.onopen();
  ws2.onmessage({ data: '{"type":"ready"}' });
  ws2.onmessage({ data: h.enc.encode("bytes-since").buffer });
  assert.equal(h.session.ttyOffset, 13, "pre-sync replay bytes on the NEW socket must not move the cursor");
  ws2.onmessage({ data: '{"type":"sync","offset":24}' });
  assert.equal(h.session.ttyOffset, 24, "the new connection's sync republishes the cursor");
});

test("issue #87: both session factories and both screen-clear resets carry ttyOffset", () => {
  // The two session factories have drifted before (see kinds/pty.js's
  // comment) — pin both, plus both reset sites that must invalidate the
  // cursor when the screen is genuinely cleared. Both name the state
  // (issue #86) instead of scattering a bare -1 through the file.
  assert.ok(indexSource.includes("ttyOffset: CURSOR_UNKNOWN,"), "index.html factory initialises the cursor");
  assert.ok(ptySource.includes("ttyOffset: PTY_CURSOR_UNKNOWN,"), "pty.js factory initialises the cursor");
  assert.ok(indexSource.includes("session.ttyOffset = CURSOR_UNKNOWN;"), "index.html clears the cursor with the screen");
  assert.ok(ptySource.includes("s.ttyOffset = PTY_CURSOR_UNKNOWN;"), "pty.js clears the cursor with the screen");

  // refreshCurrentInstance wipes the screen before loadLog (FIX-A): both
  // cursors must go to the unknown sentinel AT the clear, before loadLog
  // runs. A loadLog that then fails would otherwise leave stale pre-refresh
  // cursors behind, and connectTTY would send since=<oldHead> painting only
  // [oldHead, head) onto the blank screen — worse than the pre-#87
  // full-tail replay this button got.
  const refreshBlock = sliceBlockQuoted(inlineScript(indexSource), "async function refreshCurrentInstance");
  assert.ok(refreshBlock.includes("session.ttyOffset = CURSOR_UNKNOWN;"), "refresh resets the WS cursor with the screen");
  assert.ok(refreshBlock.includes("session.logCursor = CURSOR_UNKNOWN;"), "refresh resets the SSE cursor with the screen");
  assert.ok(refreshBlock.indexOf("session.term.clear()") < refreshBlock.indexOf("session.ttyOffset = CURSOR_UNKNOWN;"),
    "the cursor reset sits at the screen clear, not before it");
  assert.ok(refreshBlock.indexOf("session.ttyOffset = CURSOR_UNKNOWN;") < refreshBlock.indexOf("await loadLog(session)"),
    "the cursors are invalidated before loadLog can half-succeed");

  // And behaviourally: the pty factory and the clearing activation both
  // hand the session a fresh unknown cursor.
  assert.equal(ptySessionHarness("running").session.ttyOffset, -1, "pty factory");
  assert.equal(activateWith("running").session.ttyOffset, -1, "clearing activate()");
});

test("issue #87: a successful loadLog pins ttyOffset to logCursor", async () => {
  // One ring-buffer end offset, two consumers: refreshCurrentInstance
  // clears the screen, replays via loadLog and reconnects the WS — if
  // ttyOffset did not follow logCursor there, the reconnect would replay
  // painted bytes back onto the freshly painted screen (review MAJOR-4).
  const ok = loadLogHarness("running", { offset: "4096" });
  await ok.api.loadLog(ok.session);
  assert.equal(ok.session.logCursor, 4096);
  assert.equal(ok.session.ttyOffset, 4096, "the WS cursor equals the new screen end");

  // A failed loadLog must not fabricate a cursor for bytes nobody saw.
  const failed = loadLogHarness("running", { fail: true });
  failed.session.ttyOffset = -1;
  await failed.api.loadLog(failed.session);
  assert.equal(failed.session.ttyOffset, -1, "failed replay leaves the cursor unknown");

  // A stripped X-Log-Offset moves BOTH cursors to the follow-live-end
  // state (issue #86): the tail is on the screen now, so the next transport
  // must not replay anything — but a stale positive cursor would be worse
  // still, claiming an offset this repaint never established.
  const stripped = loadLogHarness("running", { offset: null });
  stripped.session.logCursor = 1234;
  stripped.session.ttyOffset = 1234;
  await stripped.api.loadLog(stripped.session);
  assert.equal(stripped.session.ttyOffset, -2, "missing header means follow the live end");
  assert.equal(stripped.session.logCursor, -2, "and both cursors say the same thing");
});

// startSSE is sliced separately: while the transport is SSE, logCursor
// advances and ttyOffset would stay frozen — a later WS reconnect would
// then replay bytes the SSE path already painted. Both cursors track the
// same ring-buffer end offset, so the SSE handler must move them together
// (review MAJOR-5).
const sseCursorFactory = new Function("deps", `
  const {
    state, updateStatus, token, EventSource, writeSanitizedTerminalOutput, setTimeout,
  } = deps;
  ${[
    "const CURSOR_UNKNOWN",
    "const CURSOR_FOLLOW_LIVE_END",
  ].map(h => sliceStatement(inlineScript(indexSource), h)).join("\n")}
  ${sliceBlockQuoted(inlineScript(indexSource), "function startSSE")}
  return { startSSE };
`);

test("issue #87: the SSE fallback advances ttyOffset together with logCursor", () => {
  const opened = [];
  class FakeEventSource {
    constructor(url) { this.url = url; this.listeners = {}; opened.push(this); }
    addEventListener(name, cb) { this.listeners[name] = cb; }
    close() { this.closed = true; }
    emit(data) { (this.listeners.log || this.onmessage)({ data }); }
  }
  const writes = [];
  const api = sseCursorFactory({
    state: { instances: [], activeInst: "inst1" },
    updateStatus: () => {},
    token: "",
    EventSource: FakeEventSource,
    // Mirrors the REAL writeSanitizedTerminalOutput contract: sanitizes,
    // writes nothing when the result is empty, and RETURNS the byte count the
    // caller uses to decide whether the screen holds anything (issue #86).
    writeSanitizedTerminalOutput: (s, text) => {
      if (!text) return 0;
      writes.push(text);
      return text.length;
    },
    setTimeout: () => 0,
  });
  const session = makeSession("inst1", { logCursor: 100, ttyOffset: -1 });
  api.startSSE(session);
  assert.ok(opened[0].url.includes("&since=100"), "SSE resumes from logCursor");

  opened[0].emit(JSON.stringify({ chunk: "a-chunk", next: 123 }));
  assert.equal(session.logCursor, 123);
  assert.equal(session.ttyOffset, 123, "the WS cursor must not lag the SSE cursor");

  // Junk next values move neither cursor.
  opened[0].emit(JSON.stringify({ chunk: "junk", next: -5 }));
  assert.equal(session.logCursor, 123);
  assert.equal(session.ttyOffset, 123);
});

// --- issue #86: follow from the live end -------------------------------------

// Two different "no valid byte offset" states, and the bug was conflating
// them: loadLog painted the tail, a proxy stripped X-Log-Offset, both cursors
// stayed at the plain-unknown sentinel, so the transport that followed asked
// for the TAIL AGAIN and painted it a second time under the screen that
// already showed it. CURSOR_UNKNOWN now means "nothing painted, send me the
// tail"; CURSOR_FOLLOW_LIVE_END means "painted, offset unknown, send me
// nothing and follow the live end".
//
// loadLog, connectTTY and startSSE are sliced into ONE factory here so a
// single session travels the whole hand-off — the fake fetch can strip the
// header and the fake transports then record the URL they were opened with.
const followLiveEndFactory = new Function("deps", `
  const {
    state, fetch, headers, rememberServerRevision, AbortController,
    reportSessionError, writeSanitizedTerminalOutput, updateStatus, token,
    window, location, WebSocket, EventSource, disconnectTTY,
    resetTTYOutputDecoder, applyTTYSize, sendResize, syncTerminalToAppliedSize,
    focusTerminalIfPossible, decodeTTYOutputChunk,
    setTimeout, clearTimeout, setInterval, clearInterval, console,
  } = deps;
  ${[
    "const INSTANCE_LIVE_STATUSES",
    "const INSTANCE_PENDING_STATUSES",
    "const CURSOR_UNKNOWN",
    "const CURSOR_FOLLOW_LIVE_END",
  ].map(h => sliceStatement(inlineScript(indexSource), h)).join("\n")}
  ${[
    "function isInstanceLiveStatus",
    "function isInstancePendingStatus",
    "function isInstanceTerminalStatus",
    "function parseTTYControlMessage",
    "async function loadLog",
    "function connectTTY",
    "function startSSE",
  ].map(h => sliceBlockQuoted(inlineScript(indexSource), h)).join("\n\n")}
  return { loadLog, connectTTY, startSSE };
`);

// offset: null is the proxy that strips X-Log-Offset; fail: true is a fetch
// that never lands (the two unknown states, respectively). noTerm: true is a
// session with no terminal to paint into, so even a successful fetch paints
// nothing.
function followLiveEndHarness({ offset = "4096", fail = false, tail = "TAIL", noTerm = false } = {}) {
  const writes = []; // everything painted on the fake screen, in order
  const wsUrls = [];
  const sseUrls = [];
  let socket = null;
  let eventSource = null;
  class FakeWebSocket {
    constructor(url) {
      this.url = url; this.readyState = 0; this.binaryType = ""; this.sent = [];
      wsUrls.push(url); socket = this;
    }
    send(data) { this.sent.push(data); }
    close() {
      if (this.readyState === 3) return;
      this.readyState = 3;
      if (this.onclose) this.onclose();
    }
  }
  FakeWebSocket.OPEN = 1;
  class FakeEventSource {
    constructor(url) { this.url = url; this.listeners = {}; sseUrls.push(url); eventSource = this; }
    addEventListener(name, cb) { this.listeners[name] = cb; }
    close() { this.closed = true; }
    emit(obj) { (this.listeners.log || this.onmessage)({ data: JSON.stringify(obj) }); }
  }
  const session = makeSession("inst1", {
    ttyState: "IDLE", logCursor: -1, ttyOffset: -1, term: noTerm ? null : {},
  });
  const api = followLiveEndFactory({
    state: { instances: [{ id: "inst1", kind: "pty", status: "running" }], activeInst: "inst1" },
    fetch: () => {
      if (fail) return Promise.reject(new Error("network down"));
      return Promise.resolve({
        ok: true,
        text: () => Promise.resolve(tail),
        headers: { get: (name) => (name === "X-Log-Offset" ? offset : null) },
      });
    },
    headers: {},
    rememberServerRevision: () => {},
    AbortController,
    reportSessionError: () => {},
    // Mirrors the REAL writeSanitizedTerminalOutput contract: sanitizes,
    // writes nothing when the result is empty, and RETURNS the byte count the
    // caller uses to decide whether the screen holds anything (issue #86).
    writeSanitizedTerminalOutput: (s, text) => {
      if (!text) return 0;
      writes.push(text);
      return text.length;
    },
    updateStatus: () => {},
    token: "",
    window: { WebSocket: FakeWebSocket },
    location: { protocol: "http:", host: "ws.test" },
    WebSocket: FakeWebSocket,
    EventSource: FakeEventSource,
    disconnectTTY: (s) => {
      s.ttyState = s.ttySocket ? "DISCONNECTING" : "IDLE";
      s.ttySocket = null;
      s.ttyReconnectTimer = null;
    },
    resetTTYOutputDecoder: () => {},
    applyTTYSize: () => {},
    sendResize: () => {},
    syncTerminalToAppliedSize: () => {},
    focusTerminalIfPossible: () => {},
    decodeTTYOutputChunk: (s, bytes) => new TextDecoder().decode(bytes),
    // Recorded, never fired: nothing in the handshake windows may drive an
    // assertion on its own schedule.
    setTimeout: () => 0,
    clearTimeout: () => {},
    setInterval: () => 0,
    clearInterval: () => {},
    console: { warn: () => {}, error: () => {}, log: () => {} },
  });
  return {
    api, session, writes, wsUrls, sseUrls,
    enc: new TextEncoder(),
    socket: () => socket,
    eventSource: () => eventSource,
    loadLog: () => api.loadLog(session),
    connectTTY: () => api.connectTTY(session),
    startSSE: () => api.startSSE(session),
  };
}

test("issue #86: a stripped X-Log-Offset means painted-but-unoffset, not empty", async () => {
  // The stripped-header case is the bug's trigger: the tail is on the
  // screen, only its end offset is missing, so the cursor must say "do not
  // replay" rather than the plain unknown that asks for the tail again.
  const stripped = followLiveEndHarness({ offset: null });
  await stripped.loadLog();
  assert.deepEqual(stripped.writes, ["TAIL"], "the tail was painted");
  assert.equal(stripped.session.logCursor, -2, "SSE cursor follows the live end");
  assert.equal(stripped.session.ttyOffset, -2, "WS cursor follows the live end");

  // A real header still pins the real offset (unchanged, issue #87).
  const ok = followLiveEndHarness({ offset: "4096" });
  await ok.loadLog();
  assert.equal(ok.session.logCursor, 4096);
  assert.equal(ok.session.ttyOffset, 4096);

  // A FAILED fetch paints nothing, so the screen genuinely wants the tail:
  // the plain-unknown sentinel stays, and the catch path must not have been
  // swept into the follow-live-end state.
  const failed = followLiveEndHarness({ fail: true });
  await failed.loadLog();
  assert.deepEqual(failed.writes, [], "nothing was painted");
  assert.equal(failed.session.logCursor, -1);
  assert.equal(failed.session.ttyOffset, -1);
});

test("issue #86: both transports ask for the sentinel, and never since=0", () => {
  // since=0 is the #81 symptom — the oldest live byte — so the sentinel
  // must be an explicit -2 and plain unknown must stay an OMITTED since.
  const since = (url) => {
    const m = url.match(/[?&]since=(-?\d+)/);
    return m ? m[1] : null;
  };
  for (const [label, cursor, want] of [
    ["follow-live-end", -2, "-2"],
    ["plain unknown (nothing painted)", -1, null],
    ["a real cursor", 4096, "4096"],
  ]) {
    const sse = followLiveEndHarness();
    sse.session.logCursor = cursor;
    sse.startSSE();
    assert.equal(since(sse.sseUrls.at(-1)), want, `SSE with ${label}`);

    const ws = followLiveEndHarness();
    ws.session.ttyOffset = cursor;
    ws.connectTTY();
    assert.equal(since(ws.wsUrls.at(-1)), want, `WS with ${label}`);
  }
});

test("issue #86: off a stripped header the SSE stream paints the tail once", async () => {
  const h = followLiveEndHarness({ offset: null });
  await h.loadLog();
  h.startSSE();
  assert.ok(h.sseUrls.at(-1).includes("&since=-2"), `stream must request the sentinel: ${h.sseUrls.at(-1)}`);

  // The server's answer to the sentinel is an EMPTY first frame carrying the
  // live end offset: nothing to paint, and the cursor the client lacked.
  h.eventSource().emit({ chunk: "", next: 4096 });
  assert.deepEqual(h.writes, ["TAIL"], "the empty first frame paints nothing");
  assert.equal(h.session.logCursor, 4096, "the client now holds a real cursor");
  assert.equal(h.session.ttyOffset, 4096, "both cursors track the same offset");

  h.eventSource().emit({ chunk: "NEW", next: 4099 });
  assert.deepEqual(h.writes, ["TAIL", "NEW"], "only new bytes are added — the tail rendered once");
  assert.equal(h.session.logCursor, 4099);
});

test("issue #86: off a stripped header the WS handshake replays nothing", async () => {
  const h = followLiveEndHarness({ offset: null });
  await h.loadLog();
  h.connectTTY();
  assert.ok(h.wsUrls.at(-1).includes("&since=-2"), `handshake must request the sentinel: ${h.wsUrls.at(-1)}`);

  const ws = h.socket();
  ws.readyState = 1;
  ws.onopen();
  ws.onmessage({ data: '{"type":"ready"}' });
  ws.onmessage({ data: '{"type":"sync","offset":4096}' });
  assert.deepEqual(h.writes, ["TAIL"], "no replay frame — the screen already holds the tail");
  assert.equal(h.session.ttyOffset, 4096, "sync publishes the live end offset");

  ws.onmessage({ data: h.enc.encode("NEW").buffer });
  assert.deepEqual(h.writes, ["TAIL", "NEW"], "live output still flows");
  assert.equal(h.session.ttyOffset, 4099);
});

test("issue #86: index.html and kinds/pty.js agree on the cursor numbers without sharing a global name", () => {
  // The two session factories have drifted before, and -2 is a wire value the
  // server branches on, so the NUMBERS must stay equal. The NAMES must stay
  // DIFFERENT: every <script> in this page is a classic script sharing one
  // global lexical environment, so a second top-level `const CURSOR_UNKNOWN`
  // would throw SyntaxError and kill the whole inline app (that is what the
  // "no two classic scripts declare the same top-level name" test below
  // enforces; pty.js therefore owns a prefixed PTY_CURSOR_UNKNOWN).
  const rhs = (src, name) => {
    const m = src.match(new RegExp(`const ${name} = ([^;]+);`));
    assert.ok(m, `${name} is no longer declared as a named constant`);
    return m[1];
  };
  assert.deepEqual((rhs(indexSource, "CURSOR_UNKNOWN").match(/-?\d+/g) || []).map(Number), [-1], "index.html CURSOR_UNKNOWN");
  assert.deepEqual((rhs(ptySource, "PTY_CURSOR_UNKNOWN").match(/-?\d+/g) || []).map(Number), [-1], "pty.js PTY_CURSOR_UNKNOWN");
  assert.deepEqual((rhs(indexSource, "CURSOR_FOLLOW_LIVE_END").match(/-?\d+/g) || []).map(Number), [-2], "index.html CURSOR_FOLLOW_LIVE_END");

  // pty.js declares the follow-live-end state neither under its own name nor
  // under index.html's: nothing in the pty renderer ever assigns it, and a
  // top-level copy here would be dead weight at best and a collision at worst.
  assert.ok(!/^\s*const (PTY_)?CURSOR_FOLLOW_LIVE_END\b/m.test(ptySource), "pty.js must not declare a follow-live-end constant");
  assert.ok(!/^\s*const CURSOR_UNKNOWN\b/m.test(ptySource), "pty.js must not declare an unprefixed CURSOR_UNKNOWN (global lexical collision with index.html)");

  // Nothing publishes the sentinels on window: pty.js parses before index.html
  // runs, so such a publication could only ever be read as `undefined` there.
  assert.ok(!indexSource.includes("window.CURSOR_UNKNOWN"), "index.html must not publish CURSOR_UNKNOWN on window (dead by load order)");
  assert.ok(!indexSource.includes("window.CURSOR_FOLLOW_LIVE_END"), "index.html must not publish CURSOR_FOLLOW_LIVE_END on window (dead by load order)");

  for (const [src, label] of [[indexSource, "index.html"], [ptySource, "kinds/pty.js"]]) {
    for (const bare of [/logCursor: -1,/, /ttyOffset: -1,/, /logCursor = -1;/, /ttyOffset = -1;/]) {
      assert.ok(!bare.test(src), `${label} still assigns a bare -1 at ${bare}`);
    }
  }
});

// --- classic-script global scope --------------------------------------------

// Every <script> index.html loads is a CLASSIC script — there is no
// type="module" anywhere in the page — so all of them share ONE global
// lexical environment. A top-level `const`/`let`/`class` declared by two of
// them is a SyntaxError thrown when the second one parses, and that kills the
// ENTIRE inline application script: no `state`, no session map, no loadLog /
// connectTTY / startSSE, no terminal — just the static shell. The slicing
// harness above evaluates each function through new Function(...), which puts
// every declaration in its own scope, so the suite is structurally blind to
// this class of bug. This test is the guard.
//
// `var`/`function` pairs are LEGAL across scripts (redeclaration; last one
// wins), so they are collected and reported as notes rather than failures —
// they still shadow each other, and the note keeps that visible.

function stripCode(src) {
  let out = "", i = 0, n = src.length, prevSig = "";
  while (i < n) {
    const c = src[i], d = src[i + 1];
    if (c === "/" && d === "/") { while (i < n && src[i] !== "\n") i++; continue; }
    if (c === "/" && d === "*") { i += 2; while (i < n && !(src[i] === "*" && src[i + 1] === "/")) i++; i += 2; out += " "; continue; }
    if (c === '"' || c === "'") { const q = c; i++; while (i < n && src[i] !== q) { if (src[i] === "\\") i++; i++; } i++; out += "''"; prevSig = "'"; continue; }
    if (c === "`") {
      i++; let o = "``";
      while (i < n && src[i] !== "`") {
        if (src[i] === "\\") { i += 2; continue; }
        if (src[i] === "$" && src[i + 1] === "{") {
          let dep = 1; i += 2; let inner = "";
          while (i < n && dep > 0) { if (src[i] === "{") dep++; else if (src[i] === "}") { dep--; if (dep === 0) break; } inner += src[i]; i++; }
          i++; o += "${" + stripCode(inner) + "}"; continue;
        }
        i++;
      }
      i++; out += o; prevSig = "`"; continue;
    }
    // Division vs regex literal: only treat `/` as a regex when the previous
    // significant character cannot end an expression.
    if (c === "/" && /[=(,:![&|?{};+\-*%<>~^]/.test(prevSig)) {
      let j = i + 1, cls = false, ok = false;
      while (j < n) {
        const e = src[j];
        if (e === "\\") { j += 2; continue; }
        if (e === "\n") break;
        if (e === "[") cls = true; else if (e === "]") cls = false;
        else if (e === "/" && !cls) { ok = true; break; }
        j++;
      }
      if (ok) { i = j + 1; while (i < n && /[a-z]/.test(src[i])) i++; out += "/RX/"; prevSig = "/"; continue; }
    }
    out += c; if (/\S/.test(c)) prevSig = c; i++;
  }
  return out;
}

function depthAt(s, target) {
  let depth = 0;
  for (let i = 0; i < target; i++) {
    const c = s[i];
    if (c === "{" || c === "(" || c === "[") depth++;
    else if (c === "}" || c === ")" || c === "]") depth--;
  }
  return depth;
}

// Names bound by a declarator list: `a`, `a = 1, b = 2`, `{a, b: c}`, `[x, y]`.
function boundNames(pat) {
  const parts = [];
  let depth = 0, cur = "";
  for (const ch of pat) {
    if (ch === "{" || ch === "[" || ch === "(") depth++;
    if (ch === "}" || ch === "]" || ch === ")") depth--;
    if (ch === "," && depth === 0) { parts.push(cur); cur = ""; continue; }
    cur += ch;
  }
  parts.push(cur);
  const names = [];
  for (const raw of parts) {
    const t = raw.trim();
    if (!t) continue;
    if (/^[[{]/.test(t)) {
      const open = t[0], close = open === "{" ? "}" : "]";
      let d = 0, body = "";
      for (let k = 0; k < t.length; k++) {
        if (t[k] === open) { d++; if (d === 1) continue; }
        if (t[k] === close) { d--; if (d === 0) break; }
        if (d >= 1) body += t[k];
      }
      names.push(...boundNames(body));
      continue;
    }
    const head = t.split("=")[0].trim();
    const renamed = head.lastIndexOf(":");
    const id = (renamed > -1 ? head.slice(renamed + 1) : head).trim();
    if (/^[A-Za-z_$][\w$]*$/.test(id)) names.push(id);
  }
  return names;
}

// Top-level declarations of one classic script: brace/paren depth 0 AND the
// keyword actually starting a statement, so `for (const x of ys)` and a named
// function expression (`window.f = function g() {}`, which binds no global)
// are not mistaken for global bindings. Indentation is irrelevant — the inline
// block in index.html is indented by eight spaces and is still top level.
function topLevelDecls(src) {
  const s = stripCode(src);
  const decls = [];
  const kw = /(?:const|let|var|class|function)[ \t\r\n]+/g;
  let m;
  while ((m = kw.exec(s))) {
    const kwAt = m.index;
    let k = kwAt - 1;
    while (k >= 0 && /[ \t]/.test(s[k])) k--;
    if (k >= 0 && !/[;{}():,=\n]/.test(s[k])) continue;
    if (depthAt(s, kwAt) !== 0) continue;
    const kind = s.slice(kwAt, kwAt + m[0].trimEnd().length);
    let p = kwAt + m[0].length;
    while (p < s.length && /\s/.test(s[p])) p++;
    let names;
    if (kind === "function" || kind === "class") {
      const id = s.slice(p).match(/^[A-Za-z_$][\w$]*/);
      names = id && id[0] ? [id[0]] : [];
    } else if (s[p] === "{" || s[p] === "[") {
      const open = s[p], close = open === "{" ? "}" : "]";
      let d = 0, q = p;
      for (; q < s.length; q++) {
        if (s[q] === open) d++;
        else if (s[q] === close) { d--; if (d === 0) { q++; break; } }
      }
      names = boundNames(s.slice(p, q));
    } else {
      let q = p;
      while (q < s.length && s[q] !== ";" && s[q] !== "\n") q++;
      names = boundNames(s.slice(p, q));
    }
    for (const nm of names) if (nm) decls.push({ kind, name: nm });
  }
  return decls;
}

// The classic, non-vendor scripts in the order index.html loads them.
function classicScripts(html, htmlLabel) {
  const out = [];
  const tag = /<script\b([^>]*)>([\s\S]*?)<\/script>/gi;
  let m;
  while ((m = tag.exec(html))) {
    const srcMatch = (m[1] || "").match(/\bsrc\s*=\s*["']([^"']+)["']/i);
    if (srcMatch) {
      const src = srcMatch[1];
      if (src.includes("/vendor/")) continue;
      const rel = src.replace(/^\/static\//, "").split("?")[0];
      const body = readFileSync(join(staticDir, rel), "utf8");
      out.push({ label: `static/${rel}`, body });
    } else if ((m[2] || "").trim()) {
      out.push({ label: `${htmlLabel} (inline)`, body: m[2] });
    }
  }
  return out;
}

test("no two classic scripts declare the same top-level name", () => {
  const scripts = classicScripts(indexSource, "index.html");
  // Sanity: the page really is classic scripts, and the guard really sees
  // the files. Without this a selector typo would silently scan nothing.
  assert.ok(!/type\s*=\s*["']module["']/i.test(indexSource), "index.html now loads a module; the shared-global-scope assumption needs revisiting");
  const labels = scripts.map(s => s.label);
  for (const want of ["static/framework.js", "static/kinds/renderer.js", "static/kinds/pty.js", "static/kinds/opencode_web.js", "static/kinds/reasonix.js", "static/kinds/dsh_web.js", "index.html (inline)"]) {
    assert.ok(labels.includes(want), `guard did not scan ${want}; loaded scripts: ${labels.join(", ")}`);
  }

  const lexical = new Map(); // name -> label of the script that declared it
  const varLike = new Map();
  const fatal = [], notes = [];
  let total = 0;
  for (const sc of scripts) {
    for (const d of topLevelDecls(sc.body)) {
      total++;
      const isLex = d.kind !== "var" && d.kind !== "function";
      const byLex = lexical.get(d.name), byVar = varLike.get(d.name);
      if (isLex) {
        if (byLex) fatal.push(`'${d.name}' is declared at top level by BOTH ${byLex} and ${sc.label} — the second <script> throws SyntaxError and the whole inline app dies`);
        else if (byVar) fatal.push(`'${d.name}' (${sc.label}) is a lexical declaration of a name already declared var/function by ${byVar} — throws SyntaxError`);
        lexical.set(d.name, sc.label);
      } else {
        if (byLex) fatal.push(`'${d.name}' (${sc.label}) is var/function but already declared lexically by ${byLex} — throws SyntaxError`);
        else if (byVar) notes.push(`'${d.name}': var/function in both ${byVar} and ${sc.label} (legal, last one wins — but one shadows the other)`);
        varLike.set(d.name, byVar || sc.label);
      }
    }
  }
  assert.ok(total > 100, `guard only found ${total} top-level declarations across ${scripts.length} scripts; it is not really scanning`);
  for (const n of notes) console.log("note:", n);
  assert.deepEqual(fatal, [], "duplicate top-level names across classic scripts");
});

test("issue #86: an unpainted session never claims the live end it lacks", () => {
  // The sentinel is a claim about SCREEN CONTENT. With no terminal to paint
  // into, loadLog succeeded but nothing reached the user, so the cursor must
  // stay CURSOR_UNKNOWN and both transports must still ask for the tail —
  // sending -2 here would mean the tail is never shown at all. That is the
  // gate a cursor assigned before the paint block could not honour.
  const since = (url) => {
    const m = url.match(/[?&]since=(-?\d+)/);
    return m ? m[1] : null;
  };
  const h = followLiveEndHarness({ offset: null, noTerm: true });
  return h.loadLog().then(() => {
    assert.deepEqual(h.writes, [], "with no terminal, nothing was painted");
    assert.equal(h.session.logCursor, -1, "an unpainted session keeps the unknown cursor, not -2");
    assert.equal(h.session.ttyOffset, -1, "and the same for the WS cursor");

    h.startSSE();
    assert.equal(since(h.sseUrls.at(-1)), null, `an unpainted session must request the tail, got ${h.sseUrls.at(-1)}`);
    h.connectTTY();
    assert.equal(since(h.wsUrls.at(-1)), null, `an unpainted WS connect must request the tail, got ${h.wsUrls.at(-1)}`);
  });
});

test("issue #86: an EMPTY painted tail does not claim the live end", () => {
  // The sentinel's premise is "the screen IS painted with the tail". A tail
  // of zero bytes paints nothing, so the premise is false, there is no
  // duplicate to prevent, and claiming the live end would SUPPRESS the first
  // line the process prints right after this load — a regression against HEAD
  // and issue #80's "new instance shows nothing" symptom. So an empty tail
  // keeps CURSOR_UNKNOWN and both transports keep asking for the tail, which
  // is byte-for-byte the pre-#86 behaviour for that state and costs nothing,
  // because an empty tail has nothing to duplicate.
  //
  // Note the gate this pins is the byte count writeSanitizedTerminalOutput
  // returns, NOT text.length: sanitizeTerminalOutput can empty a non-empty
  // input (a tail of only stripped escape sequences), and the harness fake
  // above mirrors that contract.
  const since = (url) => {
    const m = url.match(/[?&]since=(-?\d+)/);
    return m ? m[1] : null;
  };
  const h = followLiveEndHarness({ offset: null, tail: "" });
  return h.loadLog().then(() => {
    assert.deepEqual(h.writes, [], "an empty tail reached no screen");
    assert.equal(h.session.logCursor, -1, "empty tail keeps the unknown cursor, not -2");
    assert.equal(h.session.ttyOffset, -1, "same for the WS cursor");

    h.startSSE();
    assert.equal(since(h.sseUrls.at(-1)), null, `SSE must still ask for the tail, got ${h.sseUrls.at(-1)}`);
    h.connectTTY();
    assert.equal(since(h.wsUrls.at(-1)), null, `WS must still ask for the tail, got ${h.wsUrls.at(-1)}`);

    // And the output that arrived in the meantime is not lost: the stream
    // that follows paints it and puts a REAL cursor on the session.
    h.eventSource().emit({ chunk: "FIRST-LINE", next: 10 });
    assert.deepEqual(h.writes, ["FIRST-LINE"], "the first real line still arrives");
    assert.equal(h.session.logCursor, 10, "and the cursor becomes a real offset, not the sentinel");
    assert.equal(h.session.ttyOffset, 10, "startSSE advances both cursors together, per the #87 contract");
  });
});
