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
    writeSanitizedTerminalOutput: (s, text) => writes.push(text),
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
  // then replay the oldest 64 KB on top of the tail just written.
  const missing = loadLogHarness("running", { offset: null });
  missing.session.logCursor = 1234;
  await missing.api.loadLog(missing.session);
  assert.equal(missing.session.logCursor, 1234);
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
    writeSanitizedTerminalOutput: (s, text) => { writes.push(text); },
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
  // cursor when the screen is genuinely cleared.
  assert.ok(indexSource.includes("ttyOffset: -1,"), "index.html factory initialises the cursor");
  assert.ok(ptySource.includes("ttyOffset: -1,"), "pty.js factory initialises the cursor");
  assert.ok(indexSource.includes("session.ttyOffset = -1;"), "index.html clears the cursor with the screen");
  assert.ok(ptySource.includes("s.ttyOffset = -1;"), "pty.js clears the cursor with the screen");

  // refreshCurrentInstance wipes the screen before loadLog (FIX-A): both
  // cursors must go to the -1 sentinel AT the clear, before loadLog runs.
  // A loadLog that then fails (or loses its X-Log-Offset header) would
  // otherwise leave stale pre-refresh cursors behind, and connectTTY would
  // send since=<oldHead> painting only [oldHead, head) onto the blank
  // screen — worse than the pre-#87 full-tail replay this button got.
  const refreshBlock = sliceBlockQuoted(inlineScript(indexSource), "async function refreshCurrentInstance");
  assert.ok(refreshBlock.includes("session.ttyOffset = -1;"), "refresh resets the WS cursor with the screen");
  assert.ok(refreshBlock.includes("session.logCursor = -1;"), "refresh resets the SSE cursor with the screen");
  assert.ok(refreshBlock.indexOf("session.term.clear()") < refreshBlock.indexOf("session.ttyOffset = -1;"),
    "the cursor reset sits at the screen clear, not before it");
  assert.ok(refreshBlock.indexOf("session.ttyOffset = -1;") < refreshBlock.indexOf("await loadLog(session)"),
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

  // A stripped X-Log-Offset must leave both cursors at their previous value.
  const stripped = loadLogHarness("running", { offset: null });
  stripped.session.logCursor = 1234;
  stripped.session.ttyOffset = 1234;
  await stripped.api.loadLog(stripped.session);
  assert.equal(stripped.session.ttyOffset, 1234, "missing header keeps the previous cursor");
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
    writeSanitizedTerminalOutput: (s, text) => writes.push(text),
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
