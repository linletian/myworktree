// Behavioral tests for the issue #114 Phase B client contract in index.html:
// the exponential reconnect backoff (decision 2), the overflow-notice
// consumption path (reset + visible notice + tail reconnect, decision 1/R6),
// the generation+fencing of every async artifact, the app-layer `compress`
// consumption pipeline (decision 5a) and its client-side backlog bound
// (B-1.6; the v7 inflation-ratio guard was retired by the v8
// re-arbitration, PR129-ADJUDICATION P1), plus the R16
// graceful degradation when the server never echoes ready.compress.
//
// Like the sibling suites, every function under test is SLICED out of the
// shipped index.html and evaluated against stubbed browser state - nothing
// here re-implements the logic, so a source edit that breaks the contract
// breaks these tests. Run: node --test testdata/tty_overflow_reconnect.test.mjs
// (also wired into tty_overflow_reconnect_test.go for `go test`).
import test from "node:test";
import assert from "node:assert/strict";
import { readFileSync } from "node:fs";
import { fileURLToPath } from "node:url";
import { dirname, join } from "node:path";
import zlib from "node:zlib";

const here = dirname(fileURLToPath(import.meta.url));
const staticDir = join(here, "..", "static");
const indexSource = readFileSync(join(staticDir, "index.html"), "utf8");

// Server-like deflate-raw framing (mirrors app.go: ONE persistent deflate
// context, Z_SYNC_FLUSH after every WS frame, the writer NEVER closed).
// Given plaintext chunks (one per WS frame) it returns the per-frame wire
// bytes; feeding those sequentially into a real
// DecompressionStream('deflate-raw') reproduces the production wire exactly,
// which is what the R7 "real DecompressionStream" tests need. Returns the
// frames plus the measured wire/inflated totals so a test can assert the
// actual inflation ratio it constructed.
async function makeServerFrames(chunks, level = 1) {
  const d = zlib.createDeflateRaw({ level });
  const out = [];
  d.on("data", (c) => out.push(c));
  const frames = [];
  let off = 0;
  let inflated = 0;
  for (const ch of chunks) {
    const buf = Buffer.from(ch.buffer ? ch.buffer : ch, ch.byteOffset || 0, ch.byteLength);
    inflated += buf.length;
    d.write(buf);
    await new Promise((r) => d.flush(zlib.constants.Z_SYNC_FLUSH, r));
    const all = Buffer.concat(out);
    frames.push(new Uint8Array(all.subarray(off, all.length)));
    off = all.length;
  }
  const wire = frames.reduce((n, f) => n + f.length, 0);
  d.destroy(); // server never closes the context mid-connection; release it
  return { frames, wire, inflated, ratio: wire > 0 ? inflated / wire : Infinity };
}

// --- source slicing helpers (same shape as terminal_status.test.mjs) ----

function inlineScript(html) {
  const start = html.lastIndexOf("<script>");
  const end = html.indexOf("</script>", start);
  assert.ok(start > 0 && end > start, "could not locate index.html's inline <script> block");
  return html.slice(start + "<script>".length, end);
}

// Quote/comment-aware brace matcher: the sliced functions carry literal "{"
// inside strings and regexes the naive counter cannot balance.
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

function sliceStatement(src, header) {
  const start = src.indexOf(header);
  assert.notEqual(start, -1, `source no longer contains ${JSON.stringify(header)}`);
  const end = src.indexOf(";", start);
  assert.notEqual(end, -1, `no statement terminator after ${JSON.stringify(header)}`);
  return src.slice(start, end + 1);
}

// --- backoff factory: the delay math and the 60s anchor, in isolation ---
// ttyReconnectDelayMs and armTTYBackoffReconnect are sliced verbatim;
// Math.random and Date.now are injected so the whole sequence and the
// anchor arithmetic are deterministic (TASK-B B3.4 "injectable clock").
const backoffConstHeaders = [
  "const TTY_RECONNECT_BASE_MS",
  "const TTY_RECONNECT_CAP_MS",
  "const TTY_RECONNECT_CAP_MS_HIDDEN",
  "const TTY_BACKOFF_RESET_ALIVE_MS",
];

const backoffFactory = new Function("deps", `
  const { setTimeout, clearTimeout, connectTTY, Math, Date, document } = deps;
  ${backoffConstHeaders.map(h => sliceStatement(inlineScript(indexSource), h)).join("\n")}
  ${[
    "function ttyReconnectDelayMs",
    "function armTTYBackoffReconnect",
  ].map(h => sliceBlockQuoted(inlineScript(indexSource), h)).join("\n\n")}
  return {
    ttyReconnectDelayMs,
    armTTYBackoffReconnect,
    TTY_RECONNECT_BASE_MS,
    TTY_RECONNECT_CAP_MS,
    TTY_RECONNECT_CAP_MS_HIDDEN,
    TTY_BACKOFF_RESET_ALIVE_MS,
  };
`);

// A deterministic backoff world: random()=0.5 puts the ±25% jitter factor
// on exactly 1.0, so each step lands on min(base*2^n, cap) exactly; the
// fake Date hands a single mutable `now` to every clock reader.
function backoffHarness({ hidden = false, attempts = 0, closingSyncAt = 0, now = 1_000_000 } = {}) {
  const timers = [];
  const connects = [];
  let nextId = 1;
  let clockNow = now;
  const clock = { now: () => clockNow };
  const api = backoffFactory({
    setTimeout: (fn, ms) => { const t = { id: nextId++, fn, ms }; timers.push(t); return t.id; },
    clearTimeout: (id) => { const at = timers.findIndex(t => t.id === id); if (at >= 0) timers.splice(at, 1); },
    connectTTY: (s) => { connects.push(s); },
    Math: { min: (...a) => Math.min(...a), pow: Math.pow, random: () => 0.5 },
    Date: clock,
    document: { hidden },
  });
  const session = { ttyReconnectAttempts: attempts, ttyClosingSyncAt: closingSyncAt };
  const fireNext = () => {
    assert.ok(session.ttyReconnectTimer != null, "a reconnect must be armed to fire");
    const at = timers.findIndex(t => t.id === session.ttyReconnectTimer);
    const t = timers.splice(at, 1)[0];
    session.ttyReconnectTimer = null;
    t.fn();
  };
  return { api, session, timers, connects, clock, fireNext };
}

test("issue #114 B3: backoff doubles per attempt from base ~1s and honors the visible cap", () => {
  const h = backoffHarness();
  const armed = [];
  for (let i = 0; i < 6; i++) {
    const delay = h.api.armTTYBackoffReconnect(h.session);
    armed.push(delay);
    // fire so the loop models consecutive deaths (the stub connectTTY does
    // not re-arm; the counter is what matters here).
    h.fireNext();
  }
  // random()=0.5 -> jitter factor exactly 1.0.
  assert.deepEqual(armed, [1000, 2000, 4000, 8000, 8000, 8000],
    "1s x2 growth, capped at 8000ms while visible (decision 2)");
  assert.equal(h.session.ttyReconnectAttempts, 6, "attempts increments once per scheduled retry");
  assert.equal(h.connects.length, 6, "every armed timer's callback calls connectTTY");
});

test("issue #114 B3: the hidden-tab cap is 30s and the jitter stays inside ±25%", () => {
  const hidden = backoffHarness({ hidden: true, attempts: 4 });
  // attempts=4 -> 16000 raw; visible cap would clamp at 8000, hidden at 30000.
  assert.equal(hidden.api.ttyReconnectDelayMs(hidden.session), 16000, "16000 < 30000 so no clamp yet");
  const big = backoffHarness({ hidden: true, attempts: 10 });
  assert.equal(big.api.ttyReconnectDelayMs(big.session), 30000, "hidden cap 30000 (decision 2 R10)");

  // The jitter bounds, pinned at the shipped random() endpoints: the
  // product stays inside [0.75, 1.25] * the clamped base.
  const lowFactory = new Function("deps", `
    const { setTimeout, connectTTY, Math, Date, document } = deps;
    ${backoffConstHeaders.map(x => sliceStatement(inlineScript(indexSource), x)).join("\n")}
    ${["function ttyReconnectDelayMs"].map(x => sliceBlockQuoted(inlineScript(indexSource), x)).join("\n")}
    return { ttyReconnectDelayMs };
  `);
  for (const [rnd, want] of [[0, 750], [1, 1250]]) {
    const api = lowFactory({
      setTimeout: () => 0, connectTTY: () => {}, Date: { now: () => 0 }, document: { hidden: false },
      Math: { min: (...a) => Math.min(...a), pow: Math.pow, random: () => rnd },
    });
    assert.equal(api.ttyReconnectDelayMs({ ttyReconnectAttempts: 0 }), want,
      `random=${rnd} must land the first step on exactly ${want}ms`);
  }
});

test("issue #114 B3: the ONLY reset anchor is a closing sync survived >= 60s on the same connection", () => {
  // Under 60s: no reset. The counter keeps growing even though a closing
  // sync landed - that is the v4 MED-1 fix (a notice->tail reconnect that
  // reaches its closing sync in ~1.3s must NOT clear the sequence).
  const h = backoffHarness({ attempts: 3, closingSyncAt: 1_000_000, now: 1_000_000 + 59_999 });
  const delay = h.api.armTTYBackoffReconnect(h.session);
  assert.equal(delay, 8000, "attempts=3 stays put when the connection survived only 59.999s");
  assert.equal(h.session.ttyReconnectAttempts, 4, "and the sequence keeps climbing");

  // At exactly 60s: reset, then the fresh sequence starts from the base.
  const ok = backoffHarness({ attempts: 3, closingSyncAt: 1_000_000, now: 1_000_000 + 60_000 });
  const d2 = ok.api.armTTYBackoffReconnect(ok.session);
  assert.equal(d2, 1000, "survived >= 60s after closing sync resets the sequence to base");
  assert.equal(ok.session.ttyReconnectAttempts, 1, "reset then the increment for THIS retry");

  // No closing sync ever (anchor 0): never reset, no matter the uptime.
  const no = backoffHarness({ attempts: 2, closingSyncAt: 0, now: 99_999_999 });
  assert.equal(no.api.armTTYBackoffReconnect(no.session), 4000, "anchor 0 never resets");
});

// --- WS/consume harness: the shipped connectTTY wired to stub sockets ---
// The big factory slices connectTTY together with EVERYTHING its #114
// paths call - notice handling, the inflate pump and feed, the fence
// bumps, the stream reset and the backoff scheduler - so each assertion
// runs against shipped code, never a stub of the behavior under test.
const consumeFactory = new Function("deps", `
  const {
    state, updateStatus, token, window, location, WebSocket, terminalSessions,
    resetTTYOutputDecoder, applyTTYSize, sendResize,
    syncTerminalToAppliedSize, focusTerminalIfPossible,
    writeSanitizedTerminalOutput, decodeTTYOutputChunk, startSSE,
    setTimeout, clearTimeout, setInterval, clearInterval, console,
    Math, Date, DecompressionStream,
  } = deps;
  ${[
    "const INSTANCE_LIVE_STATUSES",
    "const INSTANCE_PENDING_STATUSES",
    "const TTY_LIVENESS_CHECK_MS",
    "const TTY_LIVENESS_STALE_MS",
    "const TTY_RECONNECT_BASE_MS",
    "const TTY_RECONNECT_CAP_MS",
    "const TTY_RECONNECT_CAP_MS_HIDDEN",
    "const TTY_BACKOFF_RESET_ALIVE_MS",
    "const TTY_CLIENT_BACKLOG_MAX_BYTES",
    "const CURSOR_UNKNOWN",
    "const CURSOR_FOLLOW_LIVE_END",
  ].map(h => sliceStatement(inlineScript(indexSource), h)).join("\n")}
  ${[
    "function isInstanceLiveStatus",
    "function isInstancePendingStatus",
    "function parseTTYControlMessage",
    "function getTerminalSession",
    "function isTTYHeartbeatStale",
    "function hasLiveTTYConnection",
    "function bumpTTYGeneration",
    "function teardownTTYDecompressor",
    "function resetTTYStreamAndReconnect",
    "function sanitizeTerminalOutput",
    "function startTTYCompressedPipe",
    "async function pumpTTYDecompressor",
    "async function feedTTYCompressedFrame",
    "function handleTTYOverflow",
    "function ttyReconnectDelayMs",
    "function armTTYBackoffReconnect",
    "function disconnectTTY",
    "function reconnectStaleTTY",
    "function hasTerminalTransportInFlight",
    "function ensureTerminalLiveTransport",
    "function connectTTY",
  ].map(h => sliceBlockQuoted(inlineScript(indexSource), h)).join("\n\n")}
  return {
    connectTTY,
    disconnectTTY,
    reconnectStaleTTY,
    handleTTYOverflow,
    bumpTTYGeneration,
    resetTTYStreamAndReconnect,
    teardownTTYDecompressor,
    parseTTYControlMessage,
    armTTYBackoffReconnect,
    TTY_CLIENT_BACKLOG_MAX_BYTES,
    TTY_RECONNECT_BASE_MS,
    TTY_RECONNECT_CAP_MS,
    TTY_BACKOFF_RESET_ALIVE_MS,
    CURSOR_UNKNOWN,
    CURSOR_FOLLOW_LIVE_END,
  };
`);

// A controllable stand-in for DecompressionStream('deflate-raw'):
// writer.write(u8) "inflates" by asking the harness how big the output of
// THIS frame is (inflateFn), enqueues it, and wakes a parked reader. The
// real stream flushes per frame server-side, so one frame in -> one chunk
// out is the faithful shape for the guards under test.
function makeFakeDS(registry) {
  const ds = {
    _out: [],
    _parked: [],
    _parkedWrites: [],
    _aborted: false,
    _writes: [],
    writable: {
      getWriter() {
        return {
          write(u8) {
            if (ds._aborted) return Promise.reject(new Error("aborted"));
            // v8 P2 fault injection: write rejects (errored transform).
            if (registry.writeReject) return Promise.reject(new Error(registry.writeRejectMsg || "inflate write rejected"));
            // v8 P2 write-direction teardown pin: holdWrites parks the write
            // UNRESOLVED (the transform is "in flight") so the test can call
            // teardown (bump generation, THEN abort) mid-await and drive the
            // feed's write-catch on a fenced generation. The frame is NOT
            // processed while parked.
            if (registry.holdWrites) {
              return new Promise((res, rej) => ds._parkedWrites.push({ res, rej }));
            }
            ds._writes.push(u8.length);
            const size = registry.inflate(u8.length);
            // registry.split models a large frame surfacing as MULTIPLE
            // read() chunks (proven: the server flushes per frame into one
            // shared deflate context, so a >internal-chunk frame splits).
            const split = Math.max(1, registry.split || 1);
            const per = Math.ceil(size / split);
            let remaining = size;
            while (remaining > 0) {
              const n = Math.min(per, remaining);
              const chunk = new Uint8Array(n);
              // identity content by default so painted text equals fed text
              if (size === u8.length) chunk.set(u8.subarray(size - remaining, size - remaining + n));
              ds._out.push(chunk);
              remaining -= n;
            }
            // wake parked readers, one queued chunk each
            while (ds._parked.length && ds._out.length) {
              const r = ds._parked.shift();
              r.res({ done: false, value: ds._out.shift() });
            }
            return Promise.resolve();
          },
          abort(e) {
            ds._aborted = true;
            // v8 P2: a real writer.abort errors the transform, so BOTH
            // pending writes and parked reads must settle - writes reject
            // (the feed's write-catch is what the teardown test drives),
            // parked reads reject with the flag or resolve done.
            const pw = ds._parkedWrites.splice(0);
            pw.forEach(p => p.rej(e || new Error("aborted")));
            const waiting = ds._parked.splice(0);
            // v8 P2: with abortRejectsReads the parked reads REJECT, the
            // way a real errored readable errors its pending read - this is
            // how the test drives the pump's read-catch.
            waiting.forEach(r => registry.abortRejectsReads ? r.rej(e || new Error("aborted")) : r.res({ done: true }));
            return Promise.resolve();
          },
        };
      },
    },
    readable: {
      cancel() {
        ds._aborted = true;
        const waiting = ds._parked.splice(0);
        waiting.forEach(r => r.res({ done: true }));
        return Promise.resolve();
      },
      getReader() {
        return {
          read() {
            if (ds._out.length) return Promise.resolve({ done: false, value: ds._out.shift() });
            if (ds._aborted) return Promise.resolve({ done: true });
            return new Promise((res, rej) => ds._parked.push({ res, rej }));
          },
          cancel() {
            ds._aborted = true;
            const waiting = ds._parked.splice(0);
            waiting.forEach(r => r.res({ done: true }));
            return Promise.resolve();
          },
        };
      },
    },
  };
  return ds;
}

// Flush the async pump's promise chains: a macrotask boundary runs after
// every pending microtask, and the pump never re-queues one of its own,
// so a few rounds settle any in-flight feed/pump/write chain. (This
// setTimeout is node's real one - the harness stub only sees sliced code.)
async function settle(n = 4) {
  for (let i = 0; i < n; i++) await new Promise((res) => setTimeout(res, 0));
}

// Painted bytes once the pump has fully drained: every inflated byte has
// reached the fake terminal AND the last paint callback has returned (the
// backlog leaves the ledger only at that callback). Bounded - a stuck pump
// fails the test that calls this, not the helper.
async function drainPainted(h, target, maxRounds = 4000) {
  const paintedLen = () => h.writes.reduce((n, t) => n + new TextEncoder().encode(t).length, 0);
  for (let i = 0; i < maxRounds && paintedLen() < target && h.session.ttySocket !== null; i++) {
    await settle(1);
  }
  await settle(4); // let the final term.write callbacks land and clear backlog
  return paintedLen();
}

function consumeHarness({
  ttyOffset = -1,
  status = "running",
  compressCapable = true,
  inflate = (n) => n,
  split = 1,
  realDS = false, // use the REAL global DecompressionStream (rework W7/W12)
  writeReject = false, // v8 P2: writer.write rejects (errored transform)
  holdWrites = false, // v8 P2: writer.write parks UNRESOLVED (in flight),
                      // rejected later by abort — drives the write-direction
                      // teardown race (teardown fenced the generation first)
  abortRejectsReads = false, // v8 P2: parked reader.read() rejects on abort
  failPipeConstruct = false, // v8 P2: the PIPE's construction throws (the
                             // caps probe - construction #1 - still succeeds)
} = {}) {
  const sockets = [];
  const writes = [];
  const timers = [];
  const intervals = [];
  const statusLines = [];
  const consoleErrors = [];
  const consoleLogs = [];
  let nextId = 1;
  let clockNow = 1_000_000;
  const registry = { inflate, split, writeReject, holdWrites, abortRejectsReads, failPipeConstruct };
  const enc = new TextEncoder();

  // Each new DecompressionStream construction (the caps probe and the
  // pipe) gets its own fake; the harness tracks the LATEST so tests can
  // assert against the live pipe.
  const dsList = [];
  function FakeDecompressionStream(format) {
    assert.equal(format, "deflate-raw", "only deflate-raw is negotiated");
    // v8 P2: the caps probe is construction #1 and the live pipe is #2;
    // failPipeConstruct throws ONLY on the pipe so the probe still passes
    // and the compress cap is still declared (the realistic window: the
    // platform probed OK at connect but the ready-time construction throws).
    if (registry.failPipeConstruct && dsList.length >= 1) {
      throw new Error("synthetic pipe construction failure");
    }
    const ds = makeFakeDS(registry);
    dsList.push(ds);
    return ds;
  }

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
      if (this.readyState === 3) return;
      this.readyState = 3;
      if (this.onclose) this.onclose();
    }
  }
  FakeWebSocket.OPEN = 1;

  // The fake terminal: writes are recorded IN ORDER. The parse callback is
  // fired ASYNCHRONOUSLY on a real macrotask (production xterm's contract) -
  // this is the harness fix that EXPOSES the F1 cursor race: with a
  // synchronous callback every replay paint lands inside its own dispatch and
  // the closing-sync overshoot is invisible. _hold additionally lets a test
  // freeze the pump mid-write to drive the post-callback fence check.
  const term = {
    cols: 80,
    rows: 24,
    resets: 0,
    _held: [],
    _writeDelay: 0,
    write(text, cb) {
      writes.push(text);
      if (term._hold) { term._held.push(cb); return; }
      if (cb) setTimeout(cb, term._writeDelay);
    },
    reset() { term.resets++; },
    clear() {},
    scrollToBottom() {},
    blur() {},
    focus() {},
  };

  const session = {
    id: "inst1",
    term,
    container: { style: {} },
    ttyState: "IDLE",
    ttySocket: null,
    ttyReconnectTimer: null,
    ttyOffset,
    logCursor: ttyOffset,
    lastDataAt: 0,
    ttyLivenessTimer: null,
    ttyOutputDecoder: null,
    ttyReconnectAttempts: 0,
    ttyGeneration: 0,
    ttyClosingSyncAt: 0,
    ttyCompressed: false,
    ttyBacklogBytes: 0,
    ttyDecompressor: null,
  };

  const api = consumeFactory({
    state: { instances: [{ id: "inst1", kind: "pty", status }], activeInst: "inst1" },
    updateStatus: (msg) => { statusLines.push(msg); },
    token: "",
    window: { WebSocket: FakeWebSocket },
    location: { protocol: "http:", host: "ws.test" },
    WebSocket: FakeWebSocket,
    terminalSessions: { inst1: session },
    resetTTYOutputDecoder: (s) => { s.ttyOutputDecoder = null; },
    applyTTYSize: () => {},
    sendResize: () => {},
    syncTerminalToAppliedSize: () => {},
    focusTerminalIfPossible: () => {},
    decodeTTYOutputChunk: (s, bytes) => {
      if (!bytes || !bytes.length) return "";
      if (!s.ttyOutputDecoder) s.ttyOutputDecoder = new TextDecoder();
      return s.ttyOutputDecoder.decode(bytes, { stream: true });
    },
    writeSanitizedTerminalOutput: (s, text) => { writes.push(text); return text.length; },
    startSSE: () => { throw new Error("must not fall back to SSE while a WebSocket is available"); },
    setTimeout: (fn, ms) => { const t = { id: nextId++, fn, ms }; timers.push(t); return t.id; },
    clearTimeout: (id) => { const at = timers.findIndex(t => t.id === id); if (at >= 0) timers.splice(at, 1); },
    setInterval: (fn, ms) => { const t = { id: nextId++, fn, ms }; intervals.push(t); return t.id; },
    clearInterval: (id) => { const at = intervals.findIndex(t => t.id === id); if (at >= 0) intervals.splice(at, 1); },
    console: {
      warn: () => {},
      error: (...a) => { consoleErrors.push(a.join(" ")); },
      log: (...a) => { consoleLogs.push(a.join(" ")); },
    },
    Math: { min: (...a) => Math.min(...a), pow: Math.pow, random: () => 0.5 },
    Date: { now: () => clockNow },
    DecompressionStream: realDS
      ? globalThis.DecompressionStream
      : (compressCapable ? FakeDecompressionStream : undefined),
  });

  const fireTimers = (ms) => {
    for (const t of timers.filter(t => t.ms === ms)) {
      const at = timers.indexOf(t);
      if (at >= 0) timers.splice(at, 1);
      t.fn();
    }
  };

  const connect = () => api.connectTTY(session);
  const toReady = ({ compress = false } = {}) => {
    const ws = sockets[sockets.length - 1];
    ws.readyState = 1;
    ws.onopen();
    ws.onmessage({ data: compress ? '{"type":"ready","compress":true}' : '{"type":"ready"}' });
    return ws;
  };
  const startSync = (offset = 100) => {
    const ws = sockets[sockets.length - 1];
    ws.onmessage({ data: `{"type":"sync","offset":${offset},"start":true}` });
  };
  const closingSync = (offset) => {
    const ws = sockets[sockets.length - 1];
    ws.onmessage({ data: `{"type":"sync","offset":${offset}}` });
  };
  const advance = (ms) => { clockNow += ms; };

  return {
    api, session, sockets, writes, timers, intervals, statusLines,
    consoleErrors, consoleLogs, term, dsList, enc, advance, clockRef: () => clockNow,
    fireTimers, connect, toReady, startSync, closingSync,
    firstSocket: () => sockets[0],
  };
}

// --- B1: caps declaration + the R16 plaintext degradation -----------------

test("issue #114 B1: caps declare overflow-notice always and compress only when deflate-raw inflates", () => {
  const yes = consumeHarness();
  yes.connect();
  assert.ok(yes.sockets[0].url.includes("caps=ping,sync,overflow-notice,compress"),
    `full caps string expected, got ${yes.sockets[0].url}`);

  const no = consumeHarness({ compressCapable: false });
  no.connect();
  assert.ok(no.sockets[0].url.includes("caps=ping,sync,overflow-notice"),
    `overflow-notice must survive the probe failure, got ${no.sockets[0].url}`);
  assert.ok(!no.sockets[0].url.includes("compress"),
    "no deflate-raw -> no compress cap (pin 2 graceful degradation)");

  // v8 review A N1 (the adversarial half of the degradation): a server that
  // echoes compress=true to a client that did NOT declare the cap (or whose
  // platform lost the constructor since the probe) must hit the SAME visible
  // reset shape as every other "cannot inflate" branch - never a silent one,
  // never plaintext consumption of deflate bytes.
  no.toReady({ compress: true });
  assert.ok(no.consoleErrors.some((e) => e.includes("DecompressionStream is unavailable")),
    "the unavailable echo is logged");
  assert.ok(no.statusLines.includes("stream reset: stream error, data dropped"),
    "the unavailable branch shows the VISIBLE status line, isomorphic to the construction catch (review A N1)");
  // v8 review A P2-1: the reset line must be the LAST word - the ready
  // handler returns on the failure, so the tail never overwrites it with
  // "websocket live" and never arms the liveness watchdog on the abandoned
  // socket. (Mutant: drop the `!ok -> return` at the ready gate and the
  // tail repaints "websocket live" + arms a tick, reddening both asserts.)
  assert.equal(no.statusLines[no.statusLines.length - 1], "stream reset: stream error, data dropped",
    "the reset line is the LAST status line - the tail did not overwrite it with 'websocket live' (P2-1)");
  assert.ok(!no.statusLines.includes("websocket live"),
    "'websocket live' never appears after a failed pipe arm (P2-1)");
  assert.equal(no.session.ttyLivenessTimer, null, "no liveness watchdog armed on the abandoned socket (P2-1)");
  assert.equal(no.intervals.length, 0, "no setInterval was armed by the ready tail on the dead socket (P2-1)");
  assert.equal(no.session.ttyCompressed, false, "no compressed state survives");
  assert.equal(no.session.ttyDecompressor, null, "no pipe is built without the platform");
  assert.equal(no.session.ttySocket, null, "the socket was abandoned");
  assert.ok(no.term.resets >= 1, "reset + tail reconnect");
  assert.notEqual(no.session.ttyReconnectTimer, null, "the reconnect rides the backoff");

  // replay-window/slow-link stay undeclared by arbitration (TASK-B B1.2).
  assert.ok(!yes.sockets[0].url.includes("replay-window"), "replay-window NOT declared (arbitration)");
  assert.ok(!yes.sockets[0].url.includes("slow-link"), "slow-link NOT declared (arbitration)");
});

test("issue #114 B1.4/R16: ready WITHOUT compress keeps the plaintext path byte-for-byte", async () => {
  const h = consumeHarness();
  h.connect();
  const ws = h.toReady({ compress: false }); // the legacy ready frame
  assert.equal(h.session.ttyCompressed, false, "no compress echo -> plaintext state");
  assert.equal(h.session.ttyDecompressor, null, "no inflate pipe is built");

  h.startSync(100);
  ws.onmessage({ data: h.enc.encode("hello").buffer });
  await settle();
  assert.deepEqual(h.writes, ["hello"], "plaintext frames paint verbatim");
  assert.equal(h.session.ttyOffset, 105, "plaintext cursor advances by WIRE bytes");
  assert.equal(h.dsList.length, 1, "only the caps probe ever constructed a stream");
});

test("issue #114 B1: ready.compress===true wires the persistent inflate pipe and the cursor speaks inflated bytes", async () => {
  const h = consumeHarness();
  h.connect();
  const ws = h.toReady({ compress: true });
  assert.equal(h.session.ttyCompressed, true, "the echo is the switch (B1.4)");
  assert.notEqual(h.session.ttyDecompressor, null, "the per-connection pipe is live");
  assert.equal(h.dsList.length, 2, "one caps probe + one live pipe");

  // Closing sync marks the replay end and stamps ttyClosingSyncAt; frames
  // fed AFTER it are LIVE (head.live) and may advance the cursor (W1/c:
  // cursor additions live causally after the closing-sync assignment).
  h.startSync(100);
  h.closingSync(100);
  // A 5-byte wire frame whose inflated output is the SAME 5 bytes here
  // (identity inflate); a second harness inflates 3x the wire bytes, and
  // the cursor must follow the INFLATED count, not the wire count.
  ws.onmessage({ data: h.enc.encode("hello").buffer });
  await settle();
  assert.deepEqual(h.writes, ["hello"]);
  assert.equal(h.session.ttyOffset, 105, "cursor advanced by inflated bytes after the closing sync");

  const h2 = consumeHarness({ inflate: () => 3 });
  h2.connect();
  const ws2 = h2.toReady({ compress: true });
  h2.startSync(200);
  h2.closingSync(200);
  ws2.onmessage({ data: h2.enc.encode("X").buffer }); // 1 wire byte -> 3 inflated
  await settle();
  assert.equal(h2.session.ttyOffset, 203, "cursor follows the inflated length, never the wire length (5a)");
});

// --- B2: overflow notice -> fence + reset + visible notice + tail --------

test("issue #114 B2: an overflow notice fences, resets, shows the status line and tail-reconnects", () => {
  const h = consumeHarness({ ttyOffset: 4242 });
  h.connect();
  const ws = h.toReady();
  h.startSync(4242);
  ws.onmessage({ data: '{"type":"overflow","droppedBytes":9000,"droppedChunks":9}' });

  assert.ok(h.consoleLogs.some(l => l.includes("droppedBytes=9000") && l.includes("droppedChunks=9")),
    "the notice lands in the console with both counters");
  // ONE visible status line (MED-3). Wording is English because the shipped
  // TestUICopyIsEnglishOnly (#73) forbids CJK in served copy - it carries
  // the SAME meaning as the Chinese text quoted in TASK-B B2 (report query).
  assert.ok(h.statusLines.includes("stream reset: slow link, data dropped"),
    "ONE visible status line (MED-3), shipped wording");
  assert.ok(h.term.resets >= 1, "the screen is reset");
  assert.equal(h.session.ttyOffset, h.api.CURSOR_UNKNOWN, "cursor unknown -> the reconnect sends NO since");
  assert.equal(h.session.ttySocket, null, "the socket was abandoned");
  assert.equal(ws.readyState, 3, "the socket is closed");

  // tail reconnect on the FIRST backoff step, counted into the sequence.
  assert.notEqual(h.session.ttyReconnectTimer, null, "the tail reconnect is scheduled");
  assert.equal(h.timers.filter(t => t.ms === h.api.TTY_RECONNECT_BASE_MS).length, 1,
    "first notice-triggered retry rides the base step (attempts 0)");
  h.fireTimers(h.api.TTY_RECONNECT_BASE_MS);
  assert.equal(h.sockets.length, 2, "the tail reconnect opened exactly one socket");
  assert.ok(!h.sockets[1].url.includes("&since="),
    "a notice reconnect carries NO since -> the server replays its tail");
  assert.equal(h.session.ttyReconnectAttempts, 1, "notice reconnects COUNT into the backoff");
});

test("issue #114 B2: the notice handler parses only via the whitelist - a bogus type still paints", () => {
  const h = consumeHarness();
  assert.notEqual(h.api.parseTTYControlMessage('{"type":"overflow","droppedBytes":1,"droppedChunks":1}'), null,
    "overflow is whitelisted");
  assert.equal(h.api.parseTTYControlMessage('{"type":"overflowed"}'), null,
    "whitelisting overflow must not widen to anything else");
});

// --- B2: generation fencing --------------------------------------------------

test("issue #114 B2 fencing: output of a fenced generation never paints or moves the cursor", async () => {
  const h = consumeHarness();
  h.connect();
  const ws = h.toReady({ compress: true });
  h.startSync(100);
  h.closingSync(100); // frame below is LIVE: without the fence it would advance the cursor

  // Freeze the pump mid-write: the inflated chunk reached xterm, the parse
  // callback has NOT fired. Now an out-of-band fence bump (what a notice or
  // a backlog reset does synchronously) must stop the write's continuation
  // from claiming the bytes.
  h.term._hold = true;
  ws.onmessage({ data: h.enc.encode("late").buffer });
  await settle();
  assert.deepEqual(h.writes, ["late"], "the paint itself happened before the fence");
  assert.equal(h.session.ttyOffset, 100, "the cursor only moves AFTER the awaited write callback");

  h.api.bumpTTYGeneration(h.session);
  const held = h.term._held.splice(0);
  assert.equal(held.length, 1, "the write callback is parked");
  held.forEach(cb => cb());
  await settle();
  assert.equal(h.session.ttyOffset, 100,
    "a callback that lands after the fence must NOT advance the cursor");
});

test("issue #114 B2 fencing: a binary frame dispatched after the fence never enters the pipe", async () => {
  const h = consumeHarness();
  h.connect();
  const ws = h.toReady({ compress: true });
  h.startSync(100);
  h.closingSync(100);
  ws.onmessage({ data: h.enc.encode("first").buffer });
  await settle();
  assert.equal(h.writes.length, 1);

  h.api.bumpTTYGeneration(h.session); // fence without replacing the socket
  ws.onmessage({ data: h.enc.encode("fenced").buffer });
  await settle();
  assert.deepEqual(h.writes, ["first"], "the fenced frame is dropped, not painted");
  assert.equal(h.session.ttyOffset, 105, "and it never touched the cursor");
});

// --- B1.6: the client-side backlog bound ------------------------------------
// The v7 sliding-window inflation-ratio guard (B1.5/W7) was RETIRED by the
// v8 re-arbitration (PR129-ADJUDICATION P1): legit BestSpeed output measures
// 118-209x at 64KB frames, no fixed ratio threshold separates legit from
// degenerate, and the windowed denominator collapses to <=0 when one frame
// inflates past the window. The backlog bound below is the surviving memory
// guard; corrupt streams surface as inflate read/write errors (the P2
// failure paths, pinned in their own section).

test("issue #114 B1.6: a backlog past 2MB abandons the connection and tail-reconnects", async () => {
  // A single inflated chunk past the backlog bound (2MB) but with essentially
  // no wire, so the trip is unambiguously the pump-side backlog accounting.
  const h = consumeHarness({ inflate: () => h.api.TTY_CLIENT_BACKLOG_MAX_BYTES + 1 });
  h.connect();
  const ws = h.toReady({ compress: true });
  h.startSync(100);
  ws.onmessage({ data: h.enc.encode("x").buffer });
  await settle();

  assert.ok(h.consoleErrors.some(e => e.includes("client backlog exceeded") && e.includes("(pump)")),
    "the backlog guard logs WHY and WHERE it fired - the PUMP site for a pump-driven trip (review B N-3)");
  assert.ok(!h.consoleErrors.some(e => e.includes("(feed)")),
    "the feed-side reason never appears on a pump-driven trip - the two sites are distinguishable in the log (N-3)");
  assert.ok(h.statusLines.includes("stream reset: backlog exceeded, data dropped"),
    "the backlog guard shows the VISIBLE status line, not just console (rework W16/N5)");
  assert.ok(h.term.resets >= 1);
  assert.equal(h.session.ttySocket, null);
  assert.equal(h.session.ttyOffset, h.api.CURSOR_UNKNOWN);
  assert.notEqual(h.session.ttyReconnectTimer, null);
});

test("issue #114 B1.6/W16: a feed-side backlog past 2MB shows the VISIBLE status line", async () => {
  // The FEED-side trip (distinct from the pump-side one above): identity
  // inflate so the wire bytes ARE the backlog; a single >2MB wire frame
  // crosses TTY_CLIENT_BACKLOG_MAX_BYTES at the feed site before the pump
  // drains any of it. rework W16/N5 - both backlog trip points must show the
  // visible status line, isomorphic to the overflow-notice path.
  const h = consumeHarness({ inflate: (n) => n });
  h.connect();
  const ws = h.toReady({ compress: true });
  h.startSync(100);
  const big = new Uint8Array(h.api.TTY_CLIENT_BACKLOG_MAX_BYTES + 1);
  ws.onmessage({ data: big.buffer });
  await settle();
  assert.ok(h.consoleErrors.some(e => e.includes("client backlog exceeded") && e.includes("(feed)")),
    "the feed-side backlog guard logged WHY and WHERE it fired - the FEED site (review B N-3)");
  assert.ok(!h.consoleErrors.some(e => e.includes("(pump)")),
    "the pump-side reason never appears on a feed-driven trip - the two sites are distinguishable in the log (N-3)");
  assert.ok(h.statusLines.includes("stream reset: backlog exceeded, data dropped"),
    "the feed-side backlog guard shows the VISIBLE status line (rework W16/N5)");
  assert.equal(h.session.ttySocket, null);
});

test("issue #114 B1.6: a healthy compressed stream drains the backlog to zero (no false trip)", async () => {
  const h = consumeHarness();
  h.connect();
  const ws = h.toReady({ compress: true });
  h.startSync(0);
  h.closingSync(0); // replay done; the 50 frames below are LIVE and advance the cursor
  for (let i = 0; i < 50; i++) {
    ws.onmessage({ data: h.enc.encode("chunk-" + i).buffer });
    await settle();
  }
  assert.equal(h.session.ttyBacklogBytes, 0, "wire bytes leave the ledger at the transform, inflated bytes at delivery");
  assert.equal(h.writes.length, 50, "all 50 frames painted");
  assert.equal(h.session.ttySocket, ws, "the healthy connection was never abandoned");
  assert.equal(h.session.ttyOffset, h.writes.reduce((n, t) => n + new TextEncoder().encode(t).length, 0),
    "the cursor equals the sum of inflated bytes painted");
  assert.equal(h.session.ttyDecompressor.frameLedger.length, 1,
    "every frame but the LAST drained off the head as the next was fed; the " +
    "trailing token stays until a later frame arrives (or the socket dies) - " +
    "the head only advances when a LATER token exists (W8 cursor attribution)");
});

// --- v8 P2: compressed-pipe failure paths - never silent, never plaintext --

test("issue #114 v8 P2: pipe construction failure resets visibly and NEVER falls back to plaintext", async () => {
  // The caps probe (construction #1) passed so the compress cap was declared
  // and the server echoes compress=true - then the PIPE's construction
  // throws. The old branch only console.error'd and returned false, and the
  // ready call site dropped the value: the client stayed on the plaintext
  // path and painted deflate garbage. v8 P2: isomorphic reset.
  const h = consumeHarness({ failPipeConstruct: true });
  h.connect();
  const ws = h.toReady({ compress: true });
  await settle();
  assert.ok(h.consoleErrors.some(e => e.includes("construction failed")),
    "the construction failure is logged");
  assert.ok(h.statusLines.includes("stream reset: stream error, data dropped"),
    "the failure shows the VISIBLE status line, isomorphic to the no-DecompressionStream branch");
  // v8 review A P2-1: same fail-fast contract as the unavailable branch -
  // the reset line is the LAST status line (no "websocket live" repaint on
  // the abandoned socket) and no liveness watchdog is armed there.
  assert.equal(h.statusLines[h.statusLines.length - 1], "stream reset: stream error, data dropped",
    "the reset line is the LAST status line - the ready tail did not run on the dead socket (P2-1)");
  assert.ok(!h.statusLines.includes("websocket live"),
    "'websocket live' never appears after a failed pipe arm (P2-1)");
  assert.equal(h.session.ttyLivenessTimer, null, "no liveness watchdog armed on the abandoned socket (P2-1)");
  assert.equal(h.intervals.length, 0, "no setInterval was armed by the ready tail on the dead socket (P2-1)");
  assert.equal(h.session.ttyCompressed, false, "no compressed state survives");
  assert.equal(h.session.ttyDecompressor, null, "no half-built pipe is left behind");
  assert.equal(h.session.ttySocket, null, "the socket was abandoned");
  assert.ok(h.term.resets >= 1, "the screen was reset");
  assert.notEqual(h.session.ttyReconnectTimer, null, "and a tail reconnect is armed");
  // The abandoned socket carries NO message handler at all (stronger than a
  // guarding one): nothing can even attempt plaintext consumption on it.
  assert.notEqual(typeof ws.onmessage, "function",
    "the abandoned socket has no message handler - plaintext consumption is unreachable");
  assert.equal(h.writes.length, 0,
    "nothing painted - no plaintext consumption of compressed bytes");
});

test("issue #114 v8 P2: an inflate read rejection on the CURRENT generation resets visibly", async () => {
  const h = consumeHarness({ abortRejectsReads: true });
  h.connect();
  const ws = h.toReady({ compress: true });
  h.startSync(0);
  h.closingSync(0);
  // Paint one healthy frame, then ERROR the live stream under the still
  // current socket+generation (a corrupt deflate surfacing through read()).
  ws.onmessage({ data: h.enc.encode("ok").buffer });
  await settle();
  assert.deepEqual(h.writes, ["ok"], "the healthy frame painted first");
  h.session.ttyDecompressor.writer.abort(new Error("inflate corrupt"));
  await settle();
  assert.ok(h.consoleErrors.some(e => e.includes("inflate stream error (read)")),
    "the read failure logs its reason");
  assert.ok(h.statusLines.includes("stream reset: stream error, data dropped"),
    "the read failure shows the VISIBLE status line - never a silent freeze with a live socket");
  assert.equal(h.session.ttySocket, null, "the connection was abandoned");
  assert.ok(h.term.resets >= 1, "reset + tail reconnect");
  assert.notEqual(h.session.ttyReconnectTimer, null, "the reconnect rides the backoff");
});

test("issue #114 v8 P2: a writer.write rejection on the CURRENT generation resets visibly", async () => {
  const h = consumeHarness({ writeReject: true });
  h.connect();
  const ws = h.toReady({ compress: true });
  h.startSync(0);
  h.closingSync(0);
  ws.onmessage({ data: h.enc.encode("boom").buffer });
  await settle();
  assert.deepEqual(h.writes, [], "the rejected frame never painted");
  assert.ok(h.consoleErrors.some(e => e.includes("inflate stream error (write)")),
    "the write failure logs its reason");
  assert.ok(h.statusLines.includes("stream reset: stream error, data dropped"),
    "the write failure shows the VISIBLE status line");
  assert.equal(h.session.ttySocket, null, "the connection was abandoned");
  assert.equal(h.session.ttyBacklogBytes, 0,
    "the reset path zeroes the backlog ledger - the bare-return site deliberately does not subtract");
  assert.notEqual(h.session.ttyReconnectTimer, null, "the reconnect rides the backoff");
});

test("issue #114 v8 P2: a read rejection of a FENCED generation stays silent - no reset, no unhandled rejection", async () => {
  const h = consumeHarness({ abortRejectsReads: true });
  const uncaught = [];
  const unhandled = [];
  const onExc = (e) => uncaught.push(e);
  const onRej = (e) => unhandled.push(e);
  process.on("uncaughtException", onExc);
  process.on("unhandledRejection", onRej);
  try {
    h.connect();
    h.toReady({ compress: true });
    h.startSync(0);
    // The shipped teardown order: fence FIRST, abort SECOND. The parked
    // read rejects, but the pump sees the generation has moved - an
    // expected teardown abort - and must return WITHOUT resetting again.
    h.api.teardownTTYDecompressor(h.session);
    await settle(8);
  } finally {
    process.off("uncaughtException", onExc);
    process.off("unhandledRejection", onRej);
  }
  assert.ok(!h.statusLines.some(s => s.includes("stream reset")),
    "the fenced rejection shows NO status line (teardown abort is expected)");
  assert.ok(!h.consoleErrors.some(e => e.includes("inflate stream error")),
    "the fenced rejection logs no stream error");
  assert.deepEqual(uncaught, [], "no uncaught exception");
  assert.deepEqual(unhandled, [], "the rejected read is handled by the pump, never unhandled");
});

test("issue #114 v8 P2: a write rejection DURING teardown (fenced generation) stays silent - no reset, no unhandled rejection", async () => {
  // Write-direction twin of the fenced-read pin above. holdWrites parks the
  // feed's `await pipe.writer.write(u8)` UNRESOLVED (transform in flight).
  // While it is parked, teardown runs the shipped order - bump generation
  // FIRST, abort the writer SECOND - so the abort rejects the parked write
  // AFTER the fence has moved. The feed's write-catch sees the fenced
  // generation and must return WITHOUT a status line or a second reset.
  const h = consumeHarness({ holdWrites: true });
  const uncaught = [];
  const unhandled = [];
  const onExc = (e) => uncaught.push(e);
  const onRej = (e) => unhandled.push(e);
  process.on("uncaughtException", onExc);
  process.on("unhandledRejection", onRej);
  try {
    h.connect();
    const ws = h.toReady({ compress: true });
    h.startSync(0);
    h.closingSync(0);
    const resetsBefore = h.term.resets;
    // Dispatch a binary frame: feed reaches `await pipe.writer.write(u8)`
    // and parks there (the write is held unresolved).
    ws.onmessage({ data: h.enc.encode("in-flight").buffer });
    await settle(2);
    // No live frame painted yet - the write never resolved.
    assert.deepEqual(h.writes, [], "the held write painted nothing while parked");
    // Tear down WHILE the write is parked: bump generation, then abort.
    h.api.teardownTTYDecompressor(h.session);
    await settle(8);
    assert.ok(!h.statusLines.some((s) => s.includes("stream reset")),
      "the fenced write rejection showed NO status line (teardown abort is expected)");
    assert.ok(!h.consoleErrors.some((e) => e.includes("inflate stream error")),
      "the fenced write rejection logged no stream error");
    assert.equal(h.term.resets, resetsBefore,
      "teardown's own reset is the only one - the fenced write-catch did not reset again");
  } finally {
    process.off("uncaughtException", onExc);
    process.off("unhandledRejection", onRej);
  }
  assert.deepEqual(uncaught, [], "no uncaught exception");
  assert.deepEqual(unhandled, [], "the rejected write is handled by the feed, never unhandled");
});

// --- B3 through the real connectTTY: the anchor across connections -------

test("issue #114 B3: the 60s anchor is per-connection - mid-replay deaths never reset, a 60s survivor does", () => {
  const h = consumeHarness();
  h.connect();
  let ws = h.toReady();
  assert.equal(h.session.ttyClosingSyncAt, 0, "no closing sync yet -> no anchor");

  // Death #1: MID-REPLAY, before any closing sync. With a flapping history
  // (attempts=1) the sequence must keep growing - nothing can reset it.
  h.session.ttyReconnectAttempts = 1;
  ws.close(); // onclose arms the retry through the backoff
  assert.equal(h.session.ttyReconnectAttempts, 2, "mid-replay death never resets");
  assert.equal(h.timers[h.timers.length - 1].ms, 2000, "attempts was 1 -> the 2s step");

  h.fireTimers(2000);
  ws = h.toReady();
  assert.equal(h.session.ttyClosingSyncAt, 0, "the fresh connect cleared any anchor");

  // Death #2: closing sync lands, the connection dies at 30s - UNDER the
  // anchor, so the sequence keeps growing (the notice-loop shape the v4
  // MED-1 fix exists to stop from self-erasing).
  h.closingSync(500);
  assert.ok(h.session.ttyClosingSyncAt > 0, "closing sync stamps the anchor");
  h.advance(30_000);
  ws.close();
  assert.equal(h.session.ttyReconnectAttempts, 3, "30s survivor -> no reset");
  assert.equal(h.timers[h.timers.length - 1].ms, 4000, "the sequence kept climbing");

  // Death #3: closing sync lands, the connection survives >= 60s, THEN
  // dies - the anchor fires and the sequence restarts from the base.
  h.fireTimers(4000);
  ws = h.toReady();
  h.closingSync(700);
  h.advance(60_000);
  ws.close();
  assert.equal(h.session.ttyReconnectAttempts, 1, "reset to 0 by the anchor, then the increment for this retry");
  assert.equal(h.timers[h.timers.length - 1].ms, 1000, "back to the base step after a 60s survivor");
});

// --- legacy path sanity (the zero-change rule) ----------------------------

test("issue #114 legacy: plaintext consumption is untouched and only the reconnect cadence changed", async () => {
  const h = consumeHarness({ ttyOffset: 10 });
  h.connect();
  const ws = h.toReady();
  assert.ok(h.sockets[0].url.includes("&since=10"), "plaintext resume still sends the cursor");
  h.startSync(10);
  ws.onmessage({ data: h.enc.encode("plain").buffer });
  await settle();
  assert.deepEqual(h.writes, ["plain"], "plaintext paints through the legacy writer");
  assert.equal(h.session.ttyOffset, 15, "plaintext cursor advances by wire bytes");
  assert.equal(h.session.ttyCompressed, false);
  ws.close();
  assert.equal(h.timers[h.timers.length - 1].ms, 1000, "the only behavioral change: backoff base 1000");
});

// --- rework W1/W2/W6: the frame-ledger attribution + single-arm ----------

test("issue #114 W1: a replay burst + closing sync in one dispatch leaves the cursor exactly on sync.offset", async () => {
  const h = consumeHarness();
  h.connect();
  const ws = h.toReady({ compress: true });
  // Replay begins; NO closing sync yet, so every frame fed now is captured
  // replay (head.live=false) at feed time.
  h.startSync(0);
  const SYNC_OFFSET = 9000;
  // Feed a burst of replay binary frames AND the closing sync in the SAME
  // synchronous dispatch batch. term.write callbacks are DELAYED (real xterm
  // contract, the harness fix that EXPOSES F1), so every replay paint lands
  // AFTER the closing sync already pinned the cursor. The old pump read the
  // session latch there and added the replay bytes on top - overshooting the
  // absolute head by the whole replay volume, so the next `since` landed
  // beyond head and lost data. The frame ledger must NOT move the cursor for
  // those replay bytes.
  ws.onmessage({ data: h.enc.encode("replay-aaaa").buffer });
  ws.onmessage({ data: h.enc.encode("replay-bbbb").buffer });
  ws.onmessage({ data: h.enc.encode("replay-cccc").buffer });
  ws.onmessage({ data: `{"type":"sync","offset":${SYNC_OFFSET}}` }); // closing sync, same batch
  await settle(16);
  assert.deepEqual(h.writes, ["replay-aaaa", "replay-bbbb", "replay-cccc"],
    "all replay bytes still painted");
  assert.equal(h.session.ttyOffset, SYNC_OFFSET,
    "cursor is EXACTLY sync.offset - replay bytes painted after the sync never overshoot (F1)");
  // RESIDUAL (why this exact shape is not asserted here): the bounded
  // misattribution (a lagged head popping while the head frame's tail is
  // still queued, attributing <= one frame tail to the next LIVE head) needs
  // a REAL browser that splits one frame across several read() chunks AND
  // lags the paint behind the next frame's feed. The fake DS wakes a parked
  // reader deterministically IN STREAM ORDER (one queue entry per flush), so
  // in-harness a frame's bytes never straddle a head pop - the residual is
  // structurally unobservable here. Its bound is the server beyond-head tail
  // fallback (app.go:3577 `cursor > head` -> tail replay), pinned by the
  // server-side suite, so the residual cannot lose data regardless.
});

// --- rework W8/W10 + v8 P1: real DecompressionStream timing --------------
// These exercise the REAL global DecompressionStream('deflate-raw') driven
// by REAL deflate-raw wire produced server-style (one persistent context,
// Z_SYNC_FLUSH per frame) - closing R7's "no real-stream coverage" gap and
// pinning the paint-time cursor resume (W8) and the teardown-under-load
// rejection hygiene (W10). The v8 re-arbitration (PR129-ADJUDICATION P1)
// RETIRED the sliding-window ratio guard the old W7/W15 tests pinned: no
// fixed ratio separates legit from degenerate (legit BestSpeed output runs
// 118-209x at 64KB frames) and the windowed denominator collapses to <=0
// when one frame inflates past the window. What these tests pin NOW is the
// flip side of that ruling: a LEGIT high-inflation stream - the exact shape
// the retired guard false-killed - must paint every byte and survive.

test("issue #114 v8: a legit yes-style high-inflation stream (>32:1) paints every byte and survives (real DS)", async () => {
  const h = consumeHarness({ realDS: true });
  h.connect();
  const ws = h.toReady({ compress: true });
  h.startSync(0);
  h.closingSync(0);
  // A `yes`-style stream at the shipped BestSpeed basis (level=1): 64KB of
  // "y\n" per frame collapses ~200:1 - past the retired 32 limit, exactly
  // the shape PR129-ADJUDICATION measured at 209.4x and ruled CORRECT to
  // paint. The surviving guards (2MB backlog, inflate error paths) must let
  // it through untouched.
  const enc = new TextEncoder();
  const chunks = [];
  for (let i = 0; i < 64; i++) chunks.push(enc.encode("y\n".repeat(32 * 1024))); // 4MB output
  const { frames, ratio, inflated } = await makeServerFrames(chunks);
  assert.ok(inflated >= 4 * 1024 * 1024, "content produces >=4MB of output (got " + inflated + ")");
  assert.ok(ratio > 32,
    "legit yes-style content DOES inflate past the retired 32 limit (measured " + ratio.toFixed(0) + ":1)");
  for (const f of frames) {
    if (h.session.ttySocket === null) break;
    ws.onmessage({ data: f.buffer });
    await settle();
  }
  assert.equal(h.session.ttySocket, ws, "the healthy high-inflation connection was never abandoned");
  assert.ok(!h.statusLines.some(s => s.includes("stream reset")),
    "no reset status line on a legit stream");
  assert.ok(!h.consoleErrors.some(e => e.includes("resetting stream")),
    "no stream reset was logged for legit repetitive output");
  // Drain the pump fully (the real stream surfaces each 64KB frame as
  // several read chunks; settle rounds only advance one chunk each).
  const painted = await drainPainted(h, inflated);
  assert.equal(painted, inflated, "every inflated byte reached the terminal");
  assert.equal(h.session.ttyBacklogBytes, 0, "the backlog drained to zero");
});

test("issue #114 v8: a multi-frame burst of mixed compressibility over >=4MB paints every byte and survives (real DS)", async () => {
  const h = consumeHarness({ realDS: true });
  h.connect();
  const ws = h.toReady({ compress: true });
  h.startSync(0);
  h.closingSync(0);
  // ~72 x ~60.7KB of repetitive-log-template frames with a 640-byte
  // pseudo-random tail (one persistent deflate context, delivered one
  // message at a time) -> >=4MB inflated. Drain-lag regression shape kept
  // from the old W13/N1+N2 pin: the pump lags the feed during a burst of
  // this size, and the connection must still survive with the painted total
  // complete and the backlog drained. The random alphabet is LETTERS ONLY:
  // digits/semicolons would let the pre-existing sanitizeTerminalOutput
  // cursor-report patterns strip bytes, and this test must account every
  // byte exactly.
  let s = 99 >>> 0;
  const ALPHA = "abdefghijlmnopqstuvwxyzABDEFGHIJKLMNOPQTVWXYZ ";
  const rand = (n) => {
    let out = "";
    for (let j = 0; j < n; j++) { s = (s * 1103515245 + 12345) & 0x7fffffff; out += ALPHA[s % ALPHA.length]; }
    return out;
  };
  const enc = new TextEncoder();
  const chunks = [];
  for (let i = 0; i < 72; i++) {
    let str = "";
    const lines = Math.floor((65536 - 640) / 52);
    for (let k = 0; k < lines; k++) str += "[2024-01-01 00:00:0" + (k % 7) + "] svc worker-" + (k % 11) + " task done ok\n";
    str += rand(640);
    chunks.push(enc.encode(str));
  }
  const { frames, inflated } = await makeServerFrames(chunks);
  assert.ok(inflated >= 4 * 1024 * 1024, "content produces >=4MB of output (got " + inflated + ")");
  for (const f of frames) {
    if (h.session.ttySocket === null) break;
    ws.onmessage({ data: f.buffer });
    await settle();
  }
  assert.equal(h.session.ttySocket, ws, "the healthy burst connection survives");
  assert.ok(!h.statusLines.some(s => s.includes("stream reset")),
    "no reset status line on a healthy burst");
  const painted = await drainPainted(h, inflated);
  assert.equal(painted, inflated, "every inflated byte reached the terminal");
  assert.equal(h.session.ttyBacklogBytes, 0, "the backlog drained to zero");
});

test("issue #114 W8: a mid-replay disconnect resumes at the painted position, not the replay start", async () => {
  const h = consumeHarness({ realDS: true });
  h.connect();
  const ws = h.toReady({ compress: true });
  const BASE = 5000;
  h.startSync(BASE); // sync base known, replay begins; NO closing sync yet
  const chunks = [new TextEncoder().encode("A".repeat(200)), new TextEncoder().encode("B".repeat(200))];
  const { frames } = await makeServerFrames(chunks);
  for (const f of frames) { ws.onmessage({ data: f.buffer }); await settle(); }
  const painted = h.writes.reduce((n, t) => n + new TextEncoder().encode(t).length, 0);
  assert.ok(painted > 0, "replay bytes painted during replay");
  assert.equal(h.session.ttyOffset, BASE + painted,
    "cursor advanced from the start-sync base by painted replay bytes (mid-replay resume)");
  ws.close(); // die mid-replay, before any closing sync
  h.fireTimers(1000); // the backoff reconnect fires
  const next = h.sockets[h.sockets.length - 1];
  assert.ok(next.url.includes("&since=" + (BASE + painted)),
    "reconnect `since` is the painted position, not the replay start (#94-isomorphic)");
});

test("issue #114 W10/W14: teardown while the transform is still producing leaves NO uncaught exception and NO unhandled rejection (real DS)", async () => {
  const h = consumeHarness({ realDS: true });
  const uncaught = [];
  const unhandled = [];
  const onExc = (e) => uncaught.push(e);
  const onRej = (e) => unhandled.push(e);
  process.on("uncaughtException", onExc);
  process.on("unhandledRejection", onRej);
  try {
    h.connect();
    const ws = h.toReady({ compress: true });
    h.startSync(0);
    // Put the pump mid-flight (parked on reader.read()) with a LARGE bomb
    // still being transformed, then tear down: fence first, abort the writer
    // second. NO reader.cancel() (rework W14/N3) - cancelling the readable
    // races the transform's in-flight output and throws ERR_INVALID_STATE as
    // an UNCAUGHT exception (variant-proven), which a synchronous try/catch
    // and an unhandledRejection listener would both MISS. Aborting the writer
    // alone makes reader.read() reject, which the pump's try/catch turns into
    // a normal return.
    const chunks = [];
    for (let i = 0; i < 32; i++) chunks.push(new Uint8Array(64 * 1024)); // ~2MB zeros still inflating
    const { frames } = await makeServerFrames(chunks);
    for (const f of frames) ws.onmessage({ data: f.buffer }); // no settle - transform busy
    // Tear down WHILE the transform is still producing output.
    h.api.teardownTTYDecompressor(h.session);
    await settle(12);
    // A second teardown of an already-torn pipe must also stay silent.
    h.api.teardownTTYDecompressor(h.session);
    await settle(12);
  } finally {
    process.off("uncaughtException", onExc);
    process.off("unhandledRejection", onRej);
  }
  assert.deepEqual(uncaught, [], "teardown produced NO uncaught exception (rework W14/N3)");
  assert.deepEqual(unhandled, [], "teardown produced NO unhandled rejection (rework W10)");
});

test("issue #114 W6: arming the backoff twice clears the prior timer - exactly one reconnect is ever pending", () => {
  const h = consumeHarness();
  h.connect();
  const ws = h.toReady({ compress: true });
  h.closingSync(500);
  // Drive two arm calls back to back WITHOUT the socket dying in between -
  // the shape a stray reconnectStaleTTY plus a late onclose could produce.
  // The clear-then-arm invariant in armTTYBackoffReconnect (the ONLY
  // scheduling entry) means the second arm clears the first, so a reconnect
  // can never stack into a double connect.
  h.api.armTTYBackoffReconnect(h.session);
  const id1 = h.session.ttyReconnectTimer;
  assert.ok(id1, "the first arm set a reconnect timer handle");
  assert.ok(h.timers.some((t) => t.id === id1), "the first timer is pending");
  h.api.armTTYBackoffReconnect(h.session);
  const id2 = h.session.ttyReconnectTimer;
  assert.notEqual(id2, id1, "the second arm re-armed a fresh timer");
  assert.ok(!h.timers.some((t) => t.id === id1), "the prior timer was cleared, not stacked");
  assert.ok(h.timers.some((t) => t.id === id2), "exactly the newly armed timer is pending");
});
