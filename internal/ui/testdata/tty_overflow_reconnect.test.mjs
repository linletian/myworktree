// Behavioral tests for the issue #114 Phase B client contract in index.html:
// the exponential reconnect backoff (decision 2), the overflow-notice
// consumption path (reset + visible notice + tail reconnect, decision 1/R6),
// the generation+fencing of every async artifact, the app-layer `compress`
// consumption pipeline (decision 5a) and its two client-side bounds
// (B-1.5/W7 sliding-window inflation ratio, B-1.6 backlog), plus the R16
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

// Threshold-DISCRIMINATING legit content (rework W18/N4-delta). A
// high-repetition template loop (a fixed log line whose only variation is
// k%7 in the timestamp digit and k%11 in the worker id) PLUS a per-frame
// random tail of `tailLen` printable bytes. The template collapses under
// deflate while the random tail is incompressible, so the REAL deflate ratio
// is pinned near the A0 production upper bound (~13.2:1 at tailLen=640) -
// comfortably inside (8, 32): legit (under 32, never trips) yet
// discriminating (a threshold mutated 32->8 WOULD trip it). The two
// no-false-trip tests use this so the ratio guard's threshold is actually
// exercised, not trivially passed by a ~4:1 prose stream. Each frame is
// ~60.7KB (60657 bytes: the loop counts lines by /52 but the template line
// is ~48-49 bytes, so a frame lands short of 64KB - the titles say ~60.7KB
// frames, and 72 frames give 4.367MB inflated, a 4.1% margin over 4MiB).
// CAVEAT (rework I2, Phase C tracking): the ~13.2:1 ratio is pinned to the
// server's BestSpeed basis (level=1, app.go:2696). Under Go DefaultCompression
// this SAME content measures ~58.3:1 and WOULD trip the 32 limit - any server
// compression-level change must re-pin both the threshold and this recipe.
function makeRatioLegitChunks(seed, frames, tailLen = 640) {
  let s = seed >>> 0;
  const rand = (n) => {
    let out = "";
    for (let j = 0; j < n; j++) { s = (s * 1103515245 + 12345) & 0x7fffffff; out += String.fromCharCode(32 + (s % 95)); }
    return out;
  };
  const chunks = [];
  for (let i = 0; i < frames; i++) {
    let str = "";
    const lines = Math.floor((65536 - tailLen) / 52);
    for (let k = 0; k < lines; k++) str += "[2024-01-01 00:00:0" + (k % 7) + "] svc worker-" + (k % 11) + " task done ok\n";
    str += rand(tailLen);
    chunks.push(new TextEncoder().encode(str));
  }
  return chunks;
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
    "const TTY_INFLATION_WINDOW_BYTES",
    "const TTY_INFLATION_RATIO_LIMIT",
    "const TTY_INFLATION_SAMPLE_BYTES",
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
    TTY_INFLATION_WINDOW_BYTES,
    TTY_INFLATION_RATIO_LIMIT,
    TTY_INFLATION_SAMPLE_BYTES,
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
    _aborted: false,
    _writes: [],
    writable: {
      getWriter() {
        return {
          write(u8) {
            if (ds._aborted) return Promise.reject(new Error("aborted"));
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
              r({ done: false, value: ds._out.shift() });
            }
            return Promise.resolve();
          },
          abort(e) {
            ds._aborted = true;
            const waiting = ds._parked.splice(0);
            waiting.forEach(r => r({ done: true }));
            return Promise.resolve();
          },
        };
      },
    },
    readable: {
      cancel() {
        ds._aborted = true;
        const waiting = ds._parked.splice(0);
        waiting.forEach(r => r({ done: true }));
        return Promise.resolve();
      },
      getReader() {
        return {
          read() {
            if (ds._out.length) return Promise.resolve({ done: false, value: ds._out.shift() });
            if (ds._aborted) return Promise.resolve({ done: true });
            return new Promise(res => ds._parked.push(res));
          },
          cancel() {
            ds._aborted = true;
            const waiting = ds._parked.splice(0);
            waiting.forEach(r => r({ done: true }));
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

function consumeHarness({
  ttyOffset = -1,
  status = "running",
  compressCapable = true,
  inflate = (n) => n,
  split = 1,
  realDS = false, // use the REAL global DecompressionStream (rework W7/W12)
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
  const registry = { inflate, split };
  const enc = new TextEncoder();

  // Each new DecompressionStream construction (the caps probe and the
  // pipe) gets its own fake; the harness tracks the LATEST so tests can
  // assert against the live pipe.
  const dsList = [];
  function FakeDecompressionStream(format) {
    assert.equal(format, "deflate-raw", "only deflate-raw is negotiated");
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

// --- B1.5 / B1.6: the two client-side bounds ------------------------------

test("issue #114 B1.5/W7: a sustained inflation ratio past the limit trips the sliding-window guard + visible notice", async () => {
  // The per-frame 4MB ceiling is GONE (unimplementable under the shared
  // deflate context, rework W7). The guard is now a sliding-window inflation
  // RATIO: over the trailing 2MB of painted output, wire/rate past 32:1 means
  // a degenerate stream. Here the fake inflates 40x the wire, so with cadence
  // the ratio over a full 2MB window is ~40:1 and trips once the window fills.
  const h = consumeHarness({ inflate: (n) => n * 40 }); // 40:1 > the 32:1 limit
  h.connect();
  const ws = h.toReady({ compress: true });
  h.startSync(100);
  h.closingSync(100);
  const wire = new Uint8Array(8192); // 8KB wire -> 320KB inflated per frame
  // Feed with per-message cadence (settle drains the pump between frames so
  // the BACKLOG stays low and the ratio guard, not backlog, is what fires).
  for (let i = 0; i < 12 && h.session.ttySocket !== null; i++) {
    ws.onmessage({ data: wire.buffer });
    await settle();
  }
  assert.ok(h.consoleErrors.some(e => e.includes("inflation ratio past")),
    "the sliding-window ratio guard logs why it fired");
  assert.ok(!h.consoleErrors.some(e => e.includes("client backlog exceeded")),
    "cadence keeps the backlog low - the RATIO guard, not backlog, caught it");
  assert.ok(h.statusLines.includes("stream reset: degenerate stream, data dropped"),
    "the ratio guard shows the VISIBLE status line, not just console (rework W7)");
  assert.ok(h.term.resets >= 1, "reset + abandon + tail reconnect, notice-isomorphic");
  assert.equal(h.session.ttyOffset, h.api.CURSOR_UNKNOWN, "tail reconnect: no since");
  assert.notEqual(h.session.ttyReconnectTimer, null, "and it rides the backoff sequence");
});

test("issue #114 B1.6: a backlog past 2MB abandons the connection and tail-reconnects", async () => {
  // A single inflated chunk past the backlog bound (2MB) but with essentially
  // no wire, so ONLY the backlog guard can fire - the ratio guard needs a
  // full window of wire-fed output and a 1-byte wire feeds none of it.
  const h = consumeHarness({ inflate: () => h.api.TTY_CLIENT_BACKLOG_MAX_BYTES + 1 });
  h.connect();
  const ws = h.toReady({ compress: true });
  h.startSync(100);
  ws.onmessage({ data: h.enc.encode("x").buffer });
  await settle();

  assert.ok(h.consoleErrors.some(e => e.includes("client backlog exceeded")),
    "the backlog guard logs why it fired");
  assert.ok(h.statusLines.includes("stream reset: backlog exceeded, data dropped"),
    "the backlog guard shows the VISIBLE status line, not just console (rework W16/N5)");
  assert.ok(!h.consoleErrors.some(e => e.includes("inflation ratio past")),
    "a 1-byte wire cannot fill a wire-fed ratio window - the BACKLOG guard caught it");
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
  // visible status line, isomorphic to the ratio/notice path.
  const h = consumeHarness({ inflate: (n) => n });
  h.connect();
  const ws = h.toReady({ compress: true });
  h.startSync(100);
  const big = new Uint8Array(h.api.TTY_CLIENT_BACKLOG_MAX_BYTES + 1);
  ws.onmessage({ data: big.buffer });
  await settle();
  assert.ok(h.consoleErrors.some(e => e.includes("client backlog exceeded")),
    "the feed-side backlog guard logged why it fired");
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

// --- rework W7/W8/W10: real DecompressionStream timing ------------------
// These exercise the REAL global DecompressionStream('deflate-raw') driven
// by REAL deflate-raw wire produced server-style (one persistent context,
// Z_SYNC_FLUSH per frame) - closing R7's "no real-stream coverage" gap and
// pinning the sliding-window ratio (W7), the paint-time cursor resume (W8)
// and the teardown-under-load rejection hygiene (W10).

test("issue #114 W7: a healthy multi-frame burst over the full window never false-trips (real DS)", async () => {
  const h = consumeHarness({ realDS: true });
  h.connect();
  const ws = h.toReady({ compress: true });
  h.startSync(0);
  // ~72 x ~60.7KB of threshold-DISCRIMINATING legit content (rework W18/N4-
  // delta): a high-repetition log-template loop + a 640-byte random tail per
  // frame, ONE persistent deflate context, delivered one message at a time.
  // The REAL deflate ratio lands ~13.2:1 - near the A0 production upper
  // bound (13.056:1) and INSIDE (8, 32): legit (under 32, never trips) yet
  // discriminating, so a threshold mutated 32->8 WOULD trip this very stream.
  const chunks = makeRatioLegitChunks(13, 72, 640);
  const { frames, ratio, inflated } = await makeServerFrames(chunks);
  assert.ok(inflated >= 4 * 1024 * 1024, "content produces >=4MB of output (got " + inflated + ")");
  assert.ok(ratio > 8 && ratio < 32,
    "measured real ratio " + ratio.toFixed(2) + ":1 sits inside (8,32) so the 32->8 mutant trips it");
  for (const f of frames) {
    if (h.session.ttySocket === null) break;
    ws.onmessage({ data: f.buffer });
    await settle();
  }
  const pipe = h.session.ttyDecompressor;
  assert.ok(pipe && pipe.inflEvals > 0,
    "the ratio guard ACTUALLY evaluated full windows (inflEvals=" + (pipe ? pipe.inflEvals : "no-pipe") + ")");
  assert.equal(h.session.ttySocket, ws, "the healthy burst connection survives");
  assert.ok(!h.consoleErrors.some(e => e.includes("inflation ratio past")),
    "a healthy multi-frame burst never trips the ratio guard");
});

test("issue #114 W7: a high-compression bomb trips the ratio guard with a VISIBLE notice (real DS)", async () => {
  const h = consumeHarness({ realDS: true });
  h.connect();
  const ws = h.toReady({ compress: true });
  h.startSync(0);
  h.closingSync(0);
  // Zeros: deflate collapses them ~200:1, well past the 32:1 limit. Enough
  // frames to fill the 2MB output window (the guard only evaluates then).
  const chunks = [];
  for (let i = 0; i < 48; i++) chunks.push(new Uint8Array(64 * 1024)); // ~3MB output
  const { frames, ratio } = await makeServerFrames(chunks);
  assert.ok(ratio > 32, "the bomb compresses past the limit (measured " + ratio.toFixed(0) + ":1)");
  for (const f of frames) {
    if (h.session.ttySocket === null) break;
    ws.onmessage({ data: f.buffer });
    await settle();
  }
  assert.ok(h.consoleErrors.some(e => e.includes("inflation ratio past")),
    "the ratio guard logs why it fired");
  assert.ok(h.statusLines.includes("stream reset: degenerate stream, data dropped"),
    "the ratio guard shows the VISIBLE status line, not just console (rework W7)");
  assert.equal(h.session.ttySocket, null, "the degenerate connection was abandoned");
  assert.ok(h.term.resets >= 1, "reset + tail reconnect");
});

test("issue #114 W7/W15: legit repetitive content under the threshold never trips over >=4MB of real ~60.7KB frames (real DS)", async () => {
  const h = consumeHarness({ realDS: true });
  h.connect();
  const ws = h.toReady({ compress: true });
  h.startSync(0);
  // ~72 x ~60.7KB of threshold-DISCRIMINATING legit content (different seed)
  // -> 4.367MB inflated (a 4.1% margin over 4MiB, many windows evaluated) at
  // a REAL deflate ratio ~13.2:1, inside (8, 32). Drain-lag false-trip
  // regression pin (rework W13/N1+N2): the fed-vs-fed denominator collapsed the
  // window wire during the pump's drain and inflated the computed ratio into a
  // false trip. The consumed-based denominator is a NET of two opposing leads
  // whose direction flips with shape/cadence (W19/F2): the feed leading the
  // pump raises the denominator, write() resolving before the transform emits
  // lowers it. The safety-relevant side is the over-estimate side (measured
  // <= ~1.61x at 64KB frames; can under-estimate to ~0.75x here, ~0.996x at
  // 1KB live frames). A legit stream at A0's true ceiling 13.056:1 therefore
  // computes <= ~21x, still under 32; the margin is held by measurement and
  // these tests, not by a structural proof (rework W19/F2).
  const chunks = makeRatioLegitChunks(99, 72, 640);
  const { frames, inflated, ratio } = await makeServerFrames(chunks);
  assert.ok(inflated >= 4 * 1024 * 1024, "content produces >=4MB of output (got " + inflated + ")");
  assert.ok(ratio > 8 && ratio < 32,
    "measured real ratio " + ratio.toFixed(2) + ":1 sits inside (8,32) so the 32->8 mutant trips it");
  for (const f of frames) {
    if (h.session.ttySocket === null) break;
    ws.onmessage({ data: f.buffer });
    await settle();
  }
  const pipe = h.session.ttyDecompressor;
  assert.ok(pipe && pipe.inflEvals > 0,
    "the ratio guard ACTUALLY evaluated full windows (inflEvals=" + (pipe ? pipe.inflEvals : "no-pipe") + ")");
  assert.equal(h.session.ttySocket, ws, "legit below-threshold content survives");
  assert.ok(!h.consoleErrors.some(e => e.includes("inflation ratio past")),
    "below-threshold repetitive content never trips");
});

test("issue #114 W15: a SINGLE-frame bomb (one wire -> >2MB output) trips the ratio guard + visible notice (real DS)", async () => {
  const h = consumeHarness({ realDS: true });
  h.connect();
  const ws = h.toReady({ compress: true });
  h.startSync(0);
  // ONE frame: 4MB of zeros collapses to a few KB of wire (~1000:1). Once
  // the pump paints >2MB the window fills and the guard evaluates. The
  // denominator (wireFedTotal(now) - ref.wireConsumed) is 0 or the whole tiny
  // frame wire; both trip - the fail-open clause closes the wireSpan<=0 hole
  // the old `wireSpan > 0 &&` guard left open (rework W13/N1, W15).
  const { frames, ratio } = await makeServerFrames([new Uint8Array(4 * 1024 * 1024)]);
  assert.ok(ratio > 32, "the single-frame bomb compresses far past the limit (" + ratio.toFixed(0) + ":1)");
  ws.onmessage({ data: frames[0].buffer });
  // One 4MB frame surfaces through the real stream as many ~64KB read
  // chunks; drive the pump until it either trips (socket abandoned) or the
  // whole frame is painted, so the window genuinely fills past 2MB.
  for (let i = 0; i < 400 && h.session.ttySocket !== null; i++) await settle(1);
  assert.ok(h.consoleErrors.some(e => e.includes("inflation ratio past")),
    "the single-frame bomb tripped the ratio guard");
  assert.ok(h.statusLines.includes("stream reset: degenerate stream, data dropped"),
    "the single-frame bomb shows the VISIBLE status line (rework W15)");
  assert.equal(h.session.ttySocket, null, "the degenerate connection was abandoned");
  assert.ok(h.term.resets >= 1, "reset + tail reconnect");
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
