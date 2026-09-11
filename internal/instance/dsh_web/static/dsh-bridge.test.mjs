// Unit tests for dsh-bridge.js — the WebSocket→SSE bridge shim injected for
// remote dsh-web access. All browser APIs (window.WebSocket, EventSource,
// fetch, crypto, CloseEvent/MessageEvent, URL) are stubbed; no real network.
// Run: node --test static/dsh-bridge.test.mjs (also wired into
// bridge_shim_test.go for `go test`).
import test from "node:test";
import assert from "node:assert/strict";
import { readFileSync } from "node:fs";
import { fileURLToPath } from "node:url";
import { dirname, join } from "node:path";

const shimSource = readFileSync(join(dirname(fileURLToPath(import.meta.url)), "dsh-bridge.js"), "utf8");
const RealURL = URL;
const sleep = (ms) => new Promise((resolve) => setTimeout(resolve, ms));
const MUX = "wss://mw.example/api/remote.mux?foo=1";
const CONN = "0001020304050607"; // from the deterministic crypto stub below

class StubMessageEvent extends Event {
  constructor(type, init = {}) { super(type); this.data = init.data; }
}
class StubCloseEvent extends Event {
  constructor(type, init = {}) { super(type); this.code = init.code ?? 0; this.reason = init.reason ?? ""; }
}
// Thin wrapper over the real parser: records nothing, hits no network, just
// lets tests swap the global like the other stubs.
class StubURL {
  constructor(url, base) { this._u = new RealURL(url, base); }
  get protocol() { return this._u.protocol; }
  set protocol(value) { this._u.protocol = value; }
  get search() { return this._u.search; }
  set search(value) { this._u.search = value; }
  get href() { return this._u.href; }
  toString() { return this._u.toString(); }
}
class StubNativeWebSocket {
  static instances = [];
  constructor(url, protocols) {
    this.url = String(url); this.protocols = protocols;
    this.protocol = ""; this.extensions = "";
    this.listeners = new Map(); this.sent = []; this.closedWith = null;
    StubNativeWebSocket.instances.push(this);
  }
  addEventListener(type, fn) { this.listeners.set(type, [...(this.listeners.get(type) || []), fn]); }
  send(data) { this.sent.push(data); }
  close(code, reason) { this.closedWith = { code, reason }; }
  fire(type, event) { for (const fn of [...(this.listeners.get(type) || [])]) fn.call(this, event); }
}
class StubEventSource {
  static CLOSED = 2;
  static instances = [];
  constructor(url) {
    this.url = String(url);
    this.listeners = new Map(); this.onmessage = null; this.closed = false;
    // A dropped connection fires 'error' with readyState CONNECTING (0)
    // while EventSource waits to auto-reconnect — never CLOSED.
    this.readyState = 0;
    StubEventSource.instances.push(this);
  }
  addEventListener(type, fn) { this.listeners.set(type, [...(this.listeners.get(type) || []), fn]); }
  close() { this.closed = true; this.readyState = StubEventSource.CLOSED; }
  fire(type, event) {
    for (const fn of [...(this.listeners.get(type) || [])]) fn.call(this, event);
    if (type === "message" && this.onmessage) this.onmessage.call(this, event);
  }
}

// Evaluates the shim source against a fresh set of stubbed globals and
// returns the installed MWWebSocket class plus the stub registry.
function loadShim({ timeoutMs, fetchImpl }) {
  StubNativeWebSocket.instances = [];
  StubEventSource.instances = [];
  const win = { WebSocket: StubNativeWebSocket, location: { href: "https://mw.example/" } };
  if (timeoutMs !== undefined) win.__MW_BRIDGE_NATIVE_TIMEOUT_MS = timeoutMs;
  const fetchCalls = [];
  globalThis.window = win;
  globalThis.EventSource = StubEventSource;
  globalThis.fetch = (url, init) => { fetchCalls.push({ url, init }); return fetchImpl(url, init); };
  Object.defineProperty(globalThis, "crypto", {
    configurable: true,
    value: { getRandomValues(arr) { for (let i = 0; i < arr.length; i++) arr[i] = i; return arr; } },
  });
  globalThis.MessageEvent = StubMessageEvent;
  globalThis.CloseEvent = StubCloseEvent;
  globalThis.URL = StubURL;
  new Function(shimSource)();
  return { MW: win.WebSocket, fetchCalls };
}

const okFetch = () => Promise.resolve({ ok: true, status: 200 });

// Given a mux URL, When the native WebSocket opens before the timeout, Then
// the shim passes through to native and never constructs an EventSource.
test("mux URL: native opens in time -> pass-through, no EventSource", async () => {
  const { MW } = loadShim({ timeoutMs: 20, fetchImpl: okFetch });
  const ws = new MW(MUX);
  const events = [];
  ws.addEventListener("open", (e) => events.push(["open", e]));
  ws.addEventListener("message", (e) => events.push(["message", e.data]));
  const native = StubNativeWebSocket.instances[0];
  native.protocol = "dsh";
  native.fire("open", new Event("open"));
  assert.equal(ws.readyState, 1);
  assert.equal(ws.protocol, "dsh");
  assert.deepEqual(events.map(([t]) => t), ["open"]);
  ws.send("via-native");
  assert.deepEqual(native.sent, ["via-native"]);
  native.fire("message", new StubMessageEvent("message", { data: "downlink" }));
  assert.deepEqual(events.map(([t]) => t), ["open", "message"]);
  assert.equal(events[1][1], "downlink");
  await sleep(40);
  assert.equal(StubEventSource.instances.length, 0);
});

// Given the native WebSocket never opens, When the probe timeout elapses,
// Then the shim starts the bridge, opens ONLY on the server-sent
// `event: open` MessageEvent (not the connection-level open), and send()
// POSTs to the mux URL.
test("native never opens -> bridge opens on server frame only, send POSTs", async () => {
  const { MW, fetchCalls } = loadShim({ timeoutMs: 20, fetchImpl: okFetch });
  const ws = new MW(MUX);
  let opens = 0;
  ws.addEventListener("open", () => opens++);
  await sleep(60);
  const native = StubNativeWebSocket.instances[0];
  assert.notEqual(native.closedWith, null); // fallback closed the native probe
  assert.equal(StubEventSource.instances.length, 1);
  const es = StubEventSource.instances[0];
  assert.equal(es.url, `https://mw.example/api/remote.mux?mwbridge=1&conn=${CONN}`);
  es.fire("open", new Event("open")); // connection-level: plain Event, no data
  assert.equal(ws.readyState, 0);
  assert.equal(opens, 0);
  es.fire("open", new StubMessageEvent("open", { data: "{}" })); // server frame
  assert.equal(ws.readyState, 1);
  assert.equal(opens, 1);
  ws.send("hello");
  await ws.queue;
  assert.equal(fetchCalls.length, 1);
  assert.equal(fetchCalls[0].url, `/api/remote.mux?mwbridge=1&conn=${CONN}`);
  assert.equal(fetchCalls[0].init.method, "POST");
  assert.equal(fetchCalls[0].init.body, "hello");
});

// Given the native WebSocket errors before opening, When the error fires,
// Then the shim falls back to the bridge and ignores late native events.
test("native error before open -> fallback to bridge", async () => {
  const { MW } = loadShim({ timeoutMs: 5000, fetchImpl: okFetch });
  const ws = new MW(MUX);
  let opens = 0;
  ws.addEventListener("open", () => opens++);
  const native = StubNativeWebSocket.instances[0];
  native.fire("error", new Event("error"));
  assert.equal(StubEventSource.instances.length, 1);
  native.fire("open", new Event("open")); // late native open must be ignored
  assert.equal(opens, 0);
  assert.equal(ws.readyState, 0);
});

// Given an open bridge, When one uplink POST rejects, Then a later send
// still issues its POST (the queue is not wedged by the rejection).
test("rejected POST does not wedge the send queue", async () => {
  let failNext = true;
  const { MW, fetchCalls } = loadShim({
    timeoutMs: 20,
    fetchImpl: () => (failNext ? ((failNext = false), Promise.reject(new Error("boom"))) : okFetch()),
  });
  const ws = new MW(MUX);
  await sleep(60);
  StubEventSource.instances[0].fire("open", new StubMessageEvent("open", { data: "{}" }));
  ws.send("one");
  ws.send("two");
  await ws.queue;
  assert.deepEqual(fetchCalls.map((c) => c.init.body), ["one", "two"]);
});

// Given an open bridge, When the EventSource fires an error, Then the shim
// closes with code 1006 exactly once and closes the EventSource (no
// in-place SSE reconnect; dsh's ConnectionController redials instead).
test("EventSource error -> terminal close 1006, exactly once", async () => {
  const { MW } = loadShim({ timeoutMs: 20, fetchImpl: okFetch });
  const ws = new MW(MUX);
  const closes = [];
  ws.addEventListener("close", (e) => closes.push(e));
  await sleep(60);
  const es = StubEventSource.instances[0];
  es.fire("open", new StubMessageEvent("open", { data: "{}" }));
  es.fire("error", new Event("error"));
  assert.equal(closes.length, 1);
  assert.equal(closes[0].code, 1006);
  assert.equal(ws.readyState, 3);
  assert.equal(es.closed, true);
  es.fire("error", new Event("error"));
  assert.equal(closes.length, 1);
});

// Given an open bridge, When the server sends `event: close` with
// {"code":4001}, Then the dispatched CloseEvent carries code 4001.
test("server close frame -> CloseEvent code mapped from frame data", async () => {
  const { MW } = loadShim({ timeoutMs: 20, fetchImpl: okFetch });
  const ws = new MW(MUX);
  const closes = [];
  ws.addEventListener("close", (e) => closes.push(e));
  await sleep(60);
  const es = StubEventSource.instances[0];
  es.fire("open", new StubMessageEvent("open", { data: "{}" }));
  es.fire("close", new StubMessageEvent("close", { data: '{"code":4001}' }));
  assert.equal(closes.length, 1);
  assert.equal(closes[0].code, 4001);
});

// Given a non-mux URL, When the shim constructs it, Then the real native
// WebSocket constructor result is returned untouched.
test("non-mux URL -> native constructor pass-through", () => {
  const { MW } = loadShim({ timeoutMs: 20, fetchImpl: okFetch });
  const ws = new MW("wss://mw.example/api/other.socket");
  assert.ok(ws instanceof StubNativeWebSocket);
  assert.equal(ws.url, "wss://mw.example/api/other.socket");
  assert.equal(StubEventSource.instances.length, 0);
});

// Given an open bridge, When send() is called with a Uint8Array, Then the
// uplink POST carries &bin=1 and a base64 body.
test("binary send -> POST with &bin=1 and base64 body", async () => {
  const { MW, fetchCalls } = loadShim({ timeoutMs: 20, fetchImpl: okFetch });
  const ws = new MW(MUX);
  await sleep(60);
  StubEventSource.instances[0].fire("open", new StubMessageEvent("open", { data: "{}" }));
  ws.send(new Uint8Array([0x68, 0x69])); // "hi"
  await ws.queue;
  assert.equal(fetchCalls.length, 1);
  assert.equal(fetchCalls[0].url, `/api/remote.mux?mwbridge=1&conn=${CONN}&bin=1`);
  assert.equal(fetchCalls[0].init.body, "aGk=");
});
