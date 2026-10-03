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
    connectTTY: s => calls.connect.push(s.id),
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

function activateWith(status, extra = {}) {
  const h = ptyHarness();
  const session = makeSession("inst1", extra);
  h.window.state.instances = [{ id: "inst1", kind: "pty", status }];
  h.window.state.activeInst = "inst1";
  h.window.hasTerminalTransportInFlight = s => h.api.hasTerminalTransportInFlight(s);
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
  // re-selecting a tab look inert for the whole reconnect window.
  const queued = activateWith("running", { ttyReconnectTimer: 42 });
  assert.equal(queued.calls.status.at(-1), "ws closed, retrying...");

  const connecting = activateWith("running", { ttyState: "CONNECTING" });
  assert.equal(connecting.calls.status.at(-1), "connecting...");

  // An SSE session is live, not connecting: do not overwrite its message.
  const streaming = activateWith("running", { logStream: {} });
  assert.deepEqual(streaming.calls.status, [], "the live SSE message must survive the guard");
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
    ["a queued reconnect", { ttyReconnectTimer: 42 }],
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