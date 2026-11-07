// Behavioural tests for the PTY fit → resize → ack timing (issue #116).
//
// WHAT BROKE ON THE REAL DEVICE. After a portrait keyboard was dismissed the
// terminal stayed at the keyboard-era height: the host box had its full height
// back while the screen kept the old one. Two client-side ordering defects in
// the shipped code produced that, and both are EXECUTED here rather than grepped:
//
//   1. THE REVERT. Every fit path ended `sendResize(...)` followed by
//      `syncTerminalToAppliedSize(session)`, which repaints the size the server
//      LAST APPLIED — the previous ack. fit() had already measured the new box;
//      the next statement in the same tick threw that away. On any path where
//      sendResize refuses because the socket is not OPEN (SSE-only, reconnect in
//      flight) nothing was ever asked, so no ack could ever come back: the old
//      size stayed on screen permanently. A fit ASKS; only the `resize` control
//      frame ANSWERS, and the answer is adopted where it lands.
//
//   2. THE MISSING TRAILING EDGE. The throttle was
//      `if (now - lastResizeTime > 100) { lastResizeTime = now; send(...) }` —
//      leading edge only. A fit at t+50ms was dropped, and once the keyboard has
//      settled the host's box stops changing, so no later observer event came back
//      to deliver the size the user was looking at. Leading + ONE trailing edge
//      with a replaced payload is the fix: the newest size always goes out, and a
//      stream of fits cannot postpone it by re-arming.
//
// Everything below runs code sliced out of the shipped files (index.html's inline
// script and kinds/pty.js) against a fake clock and fake sockets, so a drift
// between an anchor and the shipped source fails loudly at slice time.
//
// Run: node --test testdata/tty_resize_refit.test.mjs
import test from "node:test";
import assert from "node:assert/strict";
import { readFileSync } from "node:fs";
import { fileURLToPath } from "node:url";
import { dirname, join } from "node:path";

const here = dirname(fileURLToPath(import.meta.url));
const staticDir = join(here, "..", "static");
const indexSource = readFileSync(join(staticDir, "index.html"), "utf8");
const ptySource = readFileSync(join(staticDir, "kinds", "pty.js"), "utf8");

// --- source slicing ---------------------------------------------------------

const indexBody = (() => {
  const start = indexSource.lastIndexOf("<script>");
  const end = indexSource.indexOf("</script>", start);
  assert.ok(start > 0 && end > start, "could not locate index.html's inline <script> block");
  return indexSource.slice(start + "<script>".length, end);
})();

function braceEnd(src, open, label) {
  let depth = 0;
  for (let i = open; i < src.length; i++) {
    if (src[i] === "{") depth++;
    else if (src[i] === "}") {
      depth--;
      if (depth === 0) return i + 1;
    }
  }
  throw new Error(`unbalanced braces while slicing ${JSON.stringify(label)}`);
}

function sliceFunction(src, name, label = "index.html") {
  const header = `function ${name}(`;
  const start = src.indexOf(header);
  assert.notEqual(start, -1, `${label} no longer contains ${JSON.stringify(header)}`);
  return src.slice(start, braceEnd(src, src.indexOf("{", start), header));
}

function sliceMethod(src, name) {
  const header = `\n    ${name}(`;
  const start = src.indexOf(header);
  assert.notEqual(start, -1, `kinds/pty.js no longer contains the method ${JSON.stringify(name)}`);
  return src.slice(start + 1, braceEnd(src, src.indexOf("{", start), header));
}

// The observer callback IS the contract under test — it is what the browser runs
// on a real resize — so it is taken from the shipped factory and executed.
function sliceResizeObserver(src, label) {
  const anchor = "session.resizeObserver = new ResizeObserver(entries => {";
  const start = src.indexOf(anchor);
  assert.notEqual(start, -1, `${label}: no ResizeObserver anchored at ${JSON.stringify(anchor)}`);
  assert.equal(src.indexOf(anchor, start + anchor.length), -1,
    `${label}: more than one ResizeObserver registration — the slice would be ambiguous`);
  return "entries => " + src.slice(src.indexOf("{", start), braceEnd(src, src.indexOf("{", start), anchor));
}

// Comments describe the retired behaviour BY NAME, so a ban that read them would
// have to ban its own explanation. Strip first: a check that cannot fail is not a
// check.
function stripComments(src) {
  return src.replace(/\/\/[^\n]*/g, "").replace(/\/\*[\s\S]*?\*\//g, "");
}

// --- fake clock -------------------------------------------------------------

function clockHarness(start = 10000) {
  let now = start;
  const timers = new Map();
  let nextId = 1;
  const runDue = () => {
    // A callback may arm another timer that is already due: loop until quiet.
    for (let guard = 0; guard < 100; guard++) {
      const due = [...timers.entries()].filter(([, t]) => t.at <= now).sort((a, b) => a[1].at - b[1].at);
      if (!due.length) return;
      for (const [id, t] of due) {
        timers.delete(id);
        t.cb();
      }
    }
  };
  return {
    now: () => now,
    setTimeout: (cb, ms) => { const id = nextId++; timers.set(id, { cb, at: now + ms }); return id; },
    clearTimeout: (id) => { timers.delete(id); },
    advance(ms) { now += ms; runDue(); },
    pending: () => timers.size,
  };
}

// --- the shipped resize layer, sliced and wired ---------------------------

// Builds the REAL sendResize / applyTTYSize / syncTerminalToAppliedSize /
// dispatchTTYResize / queueTTYResize out of index.html against a fake clock.
// Nothing here reimplements the throttle.
function resizeLayer(clock = clockHarness()) {
  const src = [
    sliceFunction(indexBody, "sendResize"),
    sliceFunction(indexBody, "applyTTYSize"),
    sliceFunction(indexBody, "syncTerminalToAppliedSize"),
    sliceFunction(indexBody, "dispatchTTYResize"),
    sliceFunction(indexBody, "queueTTYResize"),
    "return { sendResize, applyTTYSize, syncTerminalToAppliedSize, dispatchTTYResize, queueTTYResize };",
  ].join("\n");
  const factory = new Function("WebSocket", "Date", "setTimeout", "clearTimeout", "console", src);
  return {
    api: factory({ OPEN: 1, CLOSED: 3 }, { now: () => clock.now() }, clock.setTimeout, clock.clearTimeout, console),
    clock,
  };
}

function makeSession({ cols = 80, rows = 24, socket = null } = {}) {
  return {
    id: "inst1",
    term: { cols, rows, resize(c, r) { this.cols = c; this.rows = r; } },
    fitAddon: { fitted: 0, fit() { this.fitted++; } },
    ttySocket: socket,
    appliedTTYSize: null,
    lastResizeTime: 0,
    pendingTTYResize: null,
    ttyResizeTrailingTimer: null,
    lastSentTTYResize: null,
  };
}

function makeSocket() {
  return {
    readyState: 1, frames: [],
    send(msg) { this.frames.push(msg); },
    close() { this.readyState = 3; },
    onopen: null, onmessage: null, onclose: null, onerror: null,
  };
}

// Drives a shipped observer callback. Both sources get the same injection
// surface: index.html resolves queueTTYResize lexically, kinds/pty.js off
// `window`, so the harness supplies both and neither factory can quietly grow a
// third dependency. `syncTerminalToAppliedSize` THROWS, so a fit path that
// reaches the revert fails here instead of passing silently.
function runObserver(source, session, queue, { width = 931, height = 1002 } = {}) {
  const host = { clientWidth: width, clientHeight: height };
  const win = {
    queueTTYResize: queue,
    sendResize: () => false,
    syncTerminalToAppliedSize: () => { throw new Error("a fit path must not revert the screen"); },
  };
  const arrow = new Function("session", "host", "console", "queueTTYResize", "window", `return (${source})`)
    (session, host, console, queue, win);
  arrow([{ contentRect: { width, height }, target: host }]);
}

// fit() reports what a real FitAddon would report for a taller box; the observer,
// not the stub, decides when to call it.
function refitTo(session, cols, rows) {
  session.fitAddon.fit = function () {
    this.fitted++;
    session.term.cols = cols;
    session.term.rows = rows;
  };
}

const legacyObserver = sliceResizeObserver(indexBody, "index.html legacy factory");
const rendererObserver = sliceResizeObserver(ptySource, "kinds/pty.js renderer factory");

// --- 1. a fresh fit is not overwritten by the previous ack ----------------

test("#116: a fresh fit stays on screen (legacy ResizeObserver)", () => {
  const { api, clock } = resizeLayer();
  const socket = makeSocket();
  const session = makeSession({ socket });
  // 38 rows is the keyboard-era size the server last applied.
  api.applyTTYSize(session, 103, 38);
  refitTo(session, 103, 58);

  runObserver(legacyObserver, session, api.queueTTYResize);

  assert.equal(session.fitAddon.fitted, 1, "the observer fitted the host");
  assert.deepEqual({ cols: session.term.cols, rows: session.term.rows }, { cols: 103, rows: 58 },
    "the fitted size stays on screen — the previous ack must not pull it back");
  assert.deepEqual(session.appliedTTYSize, { cols: 103, rows: 38 },
    "asking is not applying: the ack is untouched until the server sends a new one");
  assert.deepEqual(socket.frames.map((f) => JSON.parse(f)), [{ type: "resize", cols: 103, rows: 58 }],
    "the fitted size is what goes on the wire");
  assert.equal(clock.pending(), 0, "outside the window the leading edge sent at once");
});

test("#116: a fit on a socket-less session keeps the new height and asks nothing", () => {
  const { api } = resizeLayer();
  // SSE-only, or a reconnect in flight. Under the retired shape this was the
  // permanent case: the revert painted the stale ack and no ack could follow.
  const session = makeSession({ socket: null });
  api.applyTTYSize(session, 103, 38);
  refitTo(session, 103, 58);

  runObserver(legacyObserver, session, api.queueTTYResize);

  assert.deepEqual({ cols: session.term.cols, rows: session.term.rows }, { cols: 103, rows: 58 },
    "no answer must NOT mean the old size comes back");
  assert.equal(session.lastSentTTYResize, null, "a refused send is not remembered as sent");
});

test("#116: the renderer factory (kinds/pty.js) behaves identically", () => {
  const { api } = resizeLayer();
  const session = makeSession();
  api.applyTTYSize(session, 103, 38);
  refitTo(session, 103, 58);

  runObserver(rendererObserver, session, api.queueTTYResize);

  assert.deepEqual({ cols: session.term.cols, rows: session.term.rows }, { cols: 103, rows: 58 },
    "the same fit leaves the same size on screen in both factories");
  assert.deepEqual(session.appliedTTYSize, { cols: 103, rows: 38 }, "no ack, no adoption");
  assert.equal(stripComments(ptySource).includes("syncTerminalToAppliedSize"), false,
    "kinds/pty.js must not reach the revert helper from any path");
});

test("#116: a zero-size entry is still refused by both observers, as shipped", () => {
  for (const [label, source] of [["legacy", legacyObserver], ["renderer", rendererObserver]]) {
    const { api } = resizeLayer();
    const socket = makeSocket();
    const session = makeSession({ socket });
    refitTo(session, 103, 58);
    const arrow = new Function("session", "host", "console", "queueTTYResize", "window", `return (${source})`)(
      session, { clientWidth: 0, clientHeight: 0 }, console, api.queueTTYResize,
      { queueTTYResize: api.queueTTYResize, syncTerminalToAppliedSize: () => { throw new Error("no revert"); } });
    arrow([{ contentRect: { width: 0, height: 0 } }]);
    assert.equal(session.fitAddon.fitted, 0, `${label}: a collapsed host is not fitted to`);
    assert.equal(socket.frames.length, 0, `${label}: nothing is asked for a box with no size`);
  }
});

// --- 2. the trailing edge delivers the newest size -----------------------

test("#116: t0 sends A, t50 fits B — the last frame on the wire is B", () => {
  const { api, clock } = resizeLayer();
  const socket = makeSocket();
  const session = makeSession({ socket });

  api.queueTTYResize(session, 103, 38);
  assert.deepEqual(socket.frames.map((f) => JSON.parse(f)), [{ type: "resize", cols: 103, rows: 38 }],
    "outside the window the leading edge sends at once — no 100ms lag on a real resize");

  clock.advance(50);
  api.queueTTYResize(session, 103, 58);
  assert.equal(socket.frames.length, 1, "inside the window nothing goes out yet");
  assert.equal(clock.pending(), 1, "exactly one trailing timer is armed");
  assert.deepEqual(session.pendingTTYResize, { cols: 103, rows: 58 }, "the newest size is the one held");

  clock.advance(60);
  assert.deepEqual(socket.frames.map((f) => JSON.parse(f)), [
    { type: "resize", cols: 103, rows: 38 },
    { type: "resize", cols: 103, rows: 58 },
  ], "the trailing edge delivers B — the size the user is looking at");
  assert.equal(session.pendingTTYResize, null, "the collected size is consumed");
  assert.equal(clock.pending(), 0, "no timer is left armed");
});

test("#116: coalescing keeps ONE trailing timer, never a resize storm", () => {
  const { api, clock } = resizeLayer();
  const socket = makeSocket();
  const session = makeSession({ socket });

  api.queueTTYResize(session, 103, 38);
  for (const rows of [39, 41, 44, 47, 50, 52, 55, 56, 57, 58]) {
    clock.advance(8);
    api.queueTTYResize(session, 103, rows);
  }
  assert.equal(socket.frames.length, 1, "no fit inside the window sends on its own");
  assert.equal(clock.pending(), 1, "re-arming is forbidden: one deadline, replaced payload");

  clock.advance(200);
  const frames = socket.frames.map((f) => JSON.parse(f));
  assert.equal(frames.length, 2, "leading + one trailing, and nothing else");
  assert.deepEqual(frames[frames.length - 1], { type: "resize", cols: 103, rows: 58 },
    "the newest size wins the trailing edge, never an older one");
});

test("#116: a trailing edge carrying an already-sent size is dropped, not repeated", () => {
  const { api, clock } = resizeLayer();
  const socket = makeSocket();
  const session = makeSession({ socket });

  api.queueTTYResize(session, 103, 38);
  clock.advance(50);
  api.queueTTYResize(session, 103, 38); // refit of an unchanged box
  clock.advance(200);

  assert.equal(socket.frames.length, 1, "leading and trailing are the same size, so one frame goes out");
  assert.deepEqual(session.lastSentTTYResize, { cols: 103, rows: 38 });
});

test("#116: nothing is remembered as sent when the socket refused", () => {
  const { api, clock } = resizeLayer();
  const session = makeSession({ socket: null });
  api.queueTTYResize(session, 103, 58);
  assert.equal(session.lastSentTTYResize, null,
    "a refusal must not occupy the dedupe slot, or a reopened socket would owe nothing");

  const socket = makeSocket();
  session.ttySocket = socket;
  clock.advance(200);
  api.queueTTYResize(session, 103, 58);
  assert.deepEqual(socket.frames.map((f) => JSON.parse(f)), [{ type: "resize", cols: 103, rows: 58 }],
    "the size the closed socket swallowed is asked of the new one");
});

// --- 3. the ack is adopted when it arrives -------------------------------

test("#116: the ack is adopted on the frame that carries it", () => {
  const { api } = resizeLayer();
  const socket = makeSocket();
  const session = makeSession({ socket });
  api.applyTTYSize(session, 103, 38);

  api.queueTTYResize(session, 103, 58);
  assert.deepEqual(session.appliedTTYSize, { cols: 103, rows: 38 },
    "nothing is adopted before the answer");

  api.applyTTYSize(session, 103, 58); // the ack for what was just asked
  assert.deepEqual(session.appliedTTYSize, { cols: 103, rows: 58 }, "the new ack is adopted");
  assert.equal(session.term.rows, 58, "and the screen follows it");

  // The applied size is the MINIMUM across the viewers of one instance: when the
  // server answers with something other than what it was asked for, that answer
  // wins, which is the authority it has always had.
  api.applyTTYSize(session, 103, 40);
  assert.deepEqual(session.appliedTTYSize, { cols: 103, rows: 40 }, "a smaller ack is adopted too");
  assert.equal(session.term.rows, 40);
});

test("#116: the reconnect sync still aligns the screen with the applied size", () => {
  const { api } = resizeLayer();
  const session = makeSession({ cols: 103, rows: 58, socket: makeSocket() });
  api.applyTTYSize(session, 103, 38);
  // The `ready` frame and the first-data tick call this directly and must still
  // put the screen at the size the PTY genuinely has, for the one round trip
  // until the ack lands.
  api.syncTerminalToAppliedSize(session);
  assert.equal(session.term.rows, 38);
  const fresh = makeSession();
  assert.doesNotThrow(() => api.syncTerminalToAppliedSize(fresh), "never acked, so nothing to adopt");
  assert.equal(fresh.term.rows, 24);
});

// --- 4. teardown: no timer may outlive its socket ------------------------

// The shipped disconnectTTY, wired to the fake clock. Its collaborators are
// stubbed because they own other subsystems; the throttle state it clears here is
// the point of the case.
function teardownLayer(clock) {
  const src = [
    sliceFunction(indexBody, "sendResize"),
    sliceFunction(indexBody, "dispatchTTYResize"),
    sliceFunction(indexBody, "queueTTYResize"),
    sliceFunction(indexBody, "disconnectTTY"),
    `function resetTTYOutputDecoder(s) {}
     function bumpTTYGeneration(s) {}
     function teardownTTYDecompressor(s) {}
     function clearInterval(id) { clearTimeout(id); }`,
    "return { disconnectTTY, queueTTYResize };",
  ].join("\n");
  const factory = new Function("WebSocket", "Date", "setTimeout", "clearTimeout", "clearInterval", "console", "state", src);
  return factory({ OPEN: 1 }, { now: () => clock.now() }, clock.setTimeout, clock.clearTimeout, clock.clearTimeout, console, { activeInst: "inst1" });
}

test("#116: disconnect disarms the trailing send and resets the dedupe key", () => {
  const clock = clockHarness();
  const api = teardownLayer(clock);
  const socket = makeSocket();
  const session = makeSession({ socket });

  api.queueTTYResize(session, 103, 38);
  clock.advance(50);
  api.queueTTYResize(session, 103, 58);
  assert.equal(clock.pending(), 1, "a trailing send is armed");

  api.disconnectTTY(session);
  assert.equal(clock.pending(), 0, "the trailing timer died with the connection");
  assert.equal(session.pendingTTYResize, null, "the pending size went with it");
  assert.equal(session.lastSentTTYResize, null,
    "a reopened socket starts with an empty size table server-side, so the old size is a NEW request");

  clock.advance(1000);
  assert.equal(socket.frames.length, 1, "only the leading frame ever went out");
});

test("#116: a session without a term cannot be made to send", () => {
  const { api } = resizeLayer();
  const socket = makeSocket();
  const session = makeSession({ socket });
  assert.equal(api.dispatchTTYResize(session, 103, 58), true, "control: a live session dispatches");
  session.term = null;
  assert.equal(api.dispatchTTYResize(session, 103, 60), false, "a destroyed terminal never writes");
  assert.deepEqual(session.lastSentTTYResize, { cols: 103, rows: 58 },
    "the refused dispatch did not move the dedupe key");
});

test("#116: all three teardown paths clear the trailing timer, in the shipped text", () => {
  const cases = [
    ["disconnectTTY", sliceFunction(indexBody, "disconnectTTY"), /clearTimeout\(session\.ttyResizeTrailingTimer\)/],
    ["destroyTerminalSession", sliceFunction(indexBody, "destroyTerminalSession"), /clearTimeout\(session\.ttyResizeTrailingTimer\)/],
    ["_destroySession", sliceMethod(ptySource, "_destroySession"), /clearTimeout\(s\.ttyResizeTrailingTimer\)/],
  ];
  for (const [label, code, re] of cases) {
    assert.match(stripComments(code), re, `${label} must clear the trailing resize timer`);
    assert.match(stripComments(code), /ttyResizeTrailingTimer = null/, `${label} must null it out`);
    assert.match(stripComments(code), /pendingTTYResize = null/, `${label} must drop the pending size`);
  }
});

// --- 5. who is allowed to call what ----------------------------------------

test("#116: every fit path asks; only the ack adopts", () => {
  const fitPaths = [
    ["legacy ResizeObserver", legacyObserver],
    ["renderer ResizeObserver", rendererObserver],
    ["refitActiveTerminal", sliceFunction(indexBody, "refitActiveTerminal")],
    ["renderTerminalSessions", sliceFunction(indexBody, "renderTerminalSessions")],
  ];
  for (const [label, code] of fitPaths) {
    const stripped = stripComments(code);
    assert.equal(stripped.includes("syncTerminalToAppliedSize"), false,
      `${label} must not revert the screen to the previous ack`);
    assert.equal(stripped.includes("queueTTYResize"), true,
      `${label} must route its fit through the shared throttle`);
  }
  // One definition plus the two socket-establishment sites. A fourth call means a
  // fit path has come back.
  const calls = stripComments(indexBody).split("syncTerminalToAppliedSize(").length - 1;
  assert.equal(calls, 3, "exactly three mentions of the revert helper remain in index.html");
});

// D4②: "a foreground instance was activated → refit and converge at once" only
// holds if activate, having fitted the host it just re-showed, IMMEDIATELY
// hands the measured size to the shared throttle — the same delivery the
// shipped ResizeObserver callback makes. A behaviour-level witness would need
// the full page host (document, state, connectTTY), so this pins the shipped
// delivery shape of the sliced activate method instead of a fake half-execution.
test("#116 D4②: activate fits, then delivers the measured size through queueTTYResize", () => {
  const activate = stripComments(sliceMethod(ptySource, "activate"));
  const fitIdx = activate.indexOf("s.fitAddon.fit()");
  assert.notEqual(fitIdx, -1, "activate no longer fits the foreground host it just re-showed");
  const deliverIdx = activate.indexOf("window.queueTTYResize(s,");
  assert.notEqual(deliverIdx, -1,
    "activate must ask for the size it just fitted — a fresh activation converges at once");
  assert.ok(deliverIdx > fitIdx, "the delivery follows the fit; nothing is asked before it is measured");
  assert.equal(activate.indexOf("window.queueTTYResize(s,", deliverIdx + 1), -1,
    "exactly one delivery in activate — a second one reopens double-asks per activation");

  // The guard + ask block matches the shipped observer's modulo the receiver
  // name: the same positive-size guard, the same same-session property args
  // (cols/rows read off the fitted `term`). The isolation `typeof` guard is
  // part of that shape — neither factory may assume the shell is loaded.
  const deliveryShape = (code, varName, label) => {
    const at = code.indexOf(`${varName}.term.cols > 0`);
    assert.notEqual(at, -1, `${label}: the delivery must be guarded by a positive-size check`);
    const ifAt = code.lastIndexOf("if (", at);
    assert.notEqual(ifAt, -1, `${label}: no guard if-block found around the size check`);
    return code.slice(ifAt, braceEnd(code, code.indexOf("{", ifAt), label))
      .replace(new RegExp(`\\b${varName}\\b`, "g"), "@").replace(/\s+/g, "");
  };
  assert.equal(deliveryShape(activate, "s", "activate"),
    deliveryShape(stripComments(rendererObserver), "session", "the pty.js shipped observer"),
    "the activation-time delivery is the observer's shipped shape — one channel, both fit paths");
});

test("#116: the retired leading-only gate is gone from both factories", () => {
  for (const [label, src] of [["index.html", indexBody], ["kinds/pty.js", ptySource]]) {
    assert.equal(stripComments(src).includes("now - session.lastResizeTime > 100"), false,
      `${label} must not keep a leading-only 100ms gate — it drops the newest fit`);
  }
  assert.match(indexSource, /window\.queueTTYResize = queueTTYResize;/,
    "the throttle is exported so kinds/pty.js shares ONE implementation");
});
