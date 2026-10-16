// Behavioral tests for the ios-ime bridge (issue #115 — iPadOS/iOS Chinese-IME
// direct-commit input loss; docs/plans/fix-issue115/plan.md §四 T2).
// The EXACT 20-scenario matrix of plan.md T2: the bridge block is SLICED from
// the shipped index.html between the §八 BEGIN/END banners (banner-anchored
// indexOf slice, inclusive of both banners — NOT the brace-matching sliceBlock(),
// which silently truncates on banner-anchored blocks) and evaluated inside
// new Function with the scheduler/console/Date injected — real node timers never
// fire, and the debug gate is driven through globalThis.location behind the
// block's own typeof guard. No mirrored copies (§六 slice discipline): moving or
// renaming the block, or deleting a banner, fails LOUDLY at slice time. Every
// expected log-row shape follows DATA-COLLECTION.md §一 (deliver precedes clear;
// armed = at-entry value; rearmed = keptEarliest; branch null on empty-delta
// rows). Pinned behaviors (fix-round review): the iosImeComposing clause of the
// 229 guard (flag-only tap), blur self-heal of the missed-compositionend latch,
// the deliver-before-clear log order, the armed-at-entry value on every deliver
// row, and the iosImeSending flag excluding the bridge's OWN mid-send echo.
// Run: node --test testdata/ios_ime_direct_input.test.mjs
// (the Go wrapper wiring is T3's job; the wrapper skips only when node absent).
import test from "node:test";
import assert from "node:assert/strict";
import { readFileSync } from "node:fs";
import { fileURLToPath } from "node:url";
import { dirname, join } from "node:path";

const here = dirname(fileURLToPath(import.meta.url));
const staticDir = join(here, "..", "static");
const indexSource = readFileSync(join(staticDir, "index.html"), "utf8");

// --- source slicing helpers (house style, quick_keys.test.mjs) --------------

// Input assumptions (§六 helper discipline): brace matching does NOT skip
// braces inside comments or strings — a sliced block whose comments/strings
// are not brace-balanced truncates and fails loudly at evaluation time.
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

// Slices a single `const X = ...;` statement up to its terminator.
function sliceStatement(src, header) {
  const start = src.indexOf(header);
  assert.notEqual(start, -1, `source no longer contains ${JSON.stringify(header)}`);
  const end = src.indexOf(";", start);
  assert.notEqual(end, -1, `no statement terminator after ${JSON.stringify(header)}`);
  return src.slice(start, end + 1);
}

// --- the removable block, sliced between the §八 banners ----------------------
// Banner-anchored indexOf slice INCLUSIVE of both banners — deliberately NOT
// sliceBlock(): a first-"{" brace matcher silently returns a partial slice on
// banner-anchored blocks (plan.md §八, review N4/G6). Every guard below is a
// loud slice-time failure if the block is moved, duplicated or un-bannered.

const IOS_IME_BANNER_BEGIN = "// ==== ios-ime bridge (issue #115) BEGIN - removable block ====";
const IOS_IME_BANNER_END = "// ==== ios-ime bridge (issue #115) END - removable block ====";

function countOccurrences(haystack, needle) {
  let n = 0;
  for (let i = haystack.indexOf(needle); i !== -1; i = haystack.indexOf(needle, i + needle.length)) n++;
  return n;
}

function sliceIosImeBlock(src) {
  assert.equal(countOccurrences(src, IOS_IME_BANNER_BEGIN), 1, "the BEGIN banner must occur exactly once in the whole index.html — a moved/duplicated block breaks the removable-block contract");
  assert.equal(countOccurrences(src, IOS_IME_BANNER_END), 1, "the END banner must occur exactly once in the whole index.html — a moved/duplicated block breaks the removable-block contract");
  const begin = src.indexOf(IOS_IME_BANNER_BEGIN);
  const end = src.indexOf(IOS_IME_BANNER_END);
  assert.notEqual(begin, -1, "the ios-ime BEGIN banner is gone — the block was moved or un-bannered");
  assert.notEqual(end, -1, "the ios-ime END banner is gone — the block was moved or un-bannered");
  assert.ok(end > begin, "the END banner precedes the BEGIN banner — the block got reordered");
  const block = src.slice(begin, end + IOS_IME_BANNER_END.length); // inclusive of BOTH banners
  for (const fn of ["function iosImeDeltaBytes", "function iosImeResidualPayload", "function flushIosIme", "function ensureIosImeDirectInputBridge"]) {
    assert.ok(block.includes(fn), `the sliced block no longer contains ${JSON.stringify(fn)} — the block got split or truncated`);
  }
  return block;
}

const iosImeBlock = sliceIosImeBlock(indexSource);

// The bridge is evaluated inside new Function with the scheduler, console and
// Date INJECTED (§四 T0/T2 slice discipline: the block introduces no new free
// module-level identifiers; location is read only behind the block's own typeof
// guard, so the debug gate is driven via globalThis.location per test).
function buildBridge(consoleStub, setTimeoutStub, clearTimeoutStub) {
  return new Function("session", "console", "Date", "setTimeout", "clearTimeout",
    iosImeBlock + "\n;return { ensureIosImeDirectInputBridge, iosImeDeltaBytes, iosImeResidualPayload, flushIosIme, iosImeResidualBranch, iosImeFailsafeMs, iosImeEmissionsMaxCount, iosImeEmissionsMaxChars };"
  )(null, consoleStub, Date, setTimeoutStub, clearTimeoutStub);
}

// Scenario 15 needs the REAL app-layer query-response filter, sliced from
// index.html with the house brace matcher (a normal function — brace matching
// works there) plus its MIN_RESPONSE_LENGTH const statement, never a copy.
const isTerminalQueryResponse = new Function(
  sliceStatement(indexSource, "const MIN_RESPONSE_LENGTH") + "\n" +
  sliceBlock(indexSource, "function isTerminalQueryResponse") +
  "\n;return isTerminalQueryResponse;"
)();

// --- fake environment -----------------------------------------------------------
// ONE harness for all 20 tests: fake textarea (listener capture map), fake
// term (onData -> disposable, per-call input recording with an optional throw
// script), fake scheduler (setTimeout -> {id, fn, ms, cleared, ran}; the REAL
// node timers never fire), recording console ([prefix, fields]), and a session
// whose iosIme* fields start undefined — the bridge initializes them itself.

function makeTextarea() {
  return {
    value: "",
    registrations: [], // { type, fn } — every addEventListener call, in order
    addEventListener(type, fn) { this.registrations.push({ type, fn }); },
  };
}

function makeTerm(textarea) {
  const term = {
    textarea,
    inputCalls: [],   // per-call strings, in order
    throwScript: [],  // optional per-call throw: entry i throws instead of recording
    disposables: [],  // every disposable onData returned, in order
    __wm: null,       // the watermark handler (last onData registration)
    onData(fn) {
      const disposable = { disposed: false, disposeCount: 0, dispose() { this.disposed = true; this.disposeCount++; } };
      term.disposables.push(disposable);
      term.__wm = fn;
      return disposable;
    },
    input(bytes) {
      const n = term.inputCalls.length;
      if (term.throwScript[n]) throw term.throwScript[n];
      term.inputCalls.push(bytes);
    },
  };
  return term;
}

function makeHarness({ debug = false } = {}) {
  // the gate: the block reads location only behind its own typeof guard; set
  // or delete globalThis.location per test — it never leaks across tests
  // because every test builds its harness first.
  if (debug) globalThis.location = { search: "?iosime-debug=1" };
  else delete globalThis.location;

  const logs = [];
  const consoleStub = { log: (prefix, fields) => logs.push([prefix, fields]) };

  const timers = []; // { id, fn, ms, cleared, ran }
  let nextTimerId = 1;
  const setTimeoutStub = (fn, ms) => {
    const t = { id: nextTimerId++, fn, ms, cleared: false, ran: false };
    timers.push(t);
    return t.id;
  };
  const clearTimeoutStub = (id) => {
    const t = timers.find((x) => x.id === id);
    if (t) t.cleared = true;
  };

  const session = { id: "s1", term: makeTerm(makeTextarea()) };
  const api = buildBridge(consoleStub, setTimeoutStub, clearTimeoutStub);

  const fire = (type, ev = {}) => {
    for (const reg of session.term.textarea.registrations) if (reg.type === type) reg.fn(ev);
  };
  const fireWatermark = (data) => { if (session.term.__wm) session.term.__wm(data); };

  const lastLiveTimer = (ms) => {
    const live = timers.filter((t) => t.ms === ms && !t.cleared && !t.ran);
    assert.ok(live.length > 0, `no live setTimeout(${ms}) timer is pending`);
    return live[live.length - 1];
  };
  const runDeferred = () => { const t = lastLiveTimer(0); t.ran = true; t.fn(); };
  const runFailsafe = () => { const t = lastLiveTimer(api.iosImeFailsafeMs); t.ran = true; t.fn(); };

  const rows = (name) => logs.filter(([prefix]) => prefix === `[ios-ime] ${name}`).map(([, fields]) => fields);
  const bridgeTimers = () => timers.filter((t) => t.ms === api.iosImeFailsafeMs);

  // FIFO discipline (scenario 19 stage two): run EVERY live setTimeout(0)
  // callback in REGISTRATION order -- xterm's own keydown timer was queued
  // first, so it runs before our keyup-defer and its watermark lands before
  // the flush adjudicates. Running the queue in LIFO order would let the
  // flush see an EMPTY emissions array (branch A) and mask the suppression.
  const runAllZero = () => {
    for (const t of timers.filter((x) => x.ms === 0 && !x.cleared && !x.ran)) { t.ran = true; t.fn(); }
  };
  // Schedule a raw setTimeout(0) through the SAME stub the bridge uses, so a
  // caller can enqueue a callback that must run BEFORE the bridge's own
  // keyup-defer (scenario 19 stage two emulates xterm's keydown timer).
  const scheduleZero = (fn) => setTimeoutStub(fn, 0);
  // Raw plumbing for the external-rewrite / flag-discrimination stages: a
  // direct value write with NO input event, a direct flag latch, and firing a
  // SINGLE registration by index (so the bridge's own keyup handler can be
  // bypassed to model xterm's _keyDownSeen short-circuit).
  const setValueRaw = (v) => { session.term.textarea.value = v; };
  const setFlag = (name, value) => { session[name] = value; };
  const fireOne = (type, idx, ev = {}) => { const r = session.term.textarea.registrations.filter((x) => x.type === type)[idx]; assert.ok(r, `no ${JSON.stringify(type)} listener at index ${idx}`); r.fn(ev); };

  const bind = () => api.ensureIosImeDirectInputBridge(session);
  const keydown = (keyCode, extra = {}) => fire("keydown", { keyCode, key: String.fromCharCode(keyCode), ...extra });
  const keyup = (keyCode = 229, extra = {}) => fire("keyup", { keyCode, key: String(keyCode), ...extra });
  const input = (extra = {}) => fire("input", { inputType: "insertText", data: "", composed: true, ...extra });
  const compositionstart = () => fire("compositionstart", { data: "" });
  const compositionend = (data = "") => fire("compositionend", { data });
  const blur = () => fire("blur", {});
  // grow the live value and fire the input event — the fake textarea has no
  // real input model; events always target the term's CURRENT textarea.
  const tapAndGrow = (keyCode, append) => { keydown(keyCode); session.term.textarea.value += append; input(); };

  return {
    api, session, timers, logs,
    get term() { return session.term; },
    get textarea() { return session.term.textarea; },
    bind, fire, fireWatermark, runDeferred, runFailsafe, runAllZero, rows, bridgeTimers,
    setValueRaw, setFlag, fireOne, scheduleZero,
    keydown, keyup, input, compositionstart, compositionend, blur, tapAndGrow,
  };
}

// --- the 20 scenarios (plan.md §四 T2, verbatim mapping) -----------------------

test("ios-ime 1: pure-append rescue — debug OFF fires the whole path with zero console rows", () => {
  // baseline: the iOS failure mode is the keyCode-229 direct commit whose
  // xterm setTimeout(0) diff misses the late insertion (plan §一); the bridge
  // must deliver the appended code point on the deferred keyup flush — with
  // the debug gate OFF, proving the fix is not a diagnostics artifact.
  const h = makeHarness({ debug: false });
  h.bind();
  // debug-OFF listener census (PR round A-5): EXACTLY the six always-on
  // listeners, and the debug-only beforeinput must not be registered at all.
  assert.equal(h.textarea.registrations.length, 6, "debug OFF pins EXACTLY the six always-on listeners (keydown/input/keyup/compositionstart/compositionend/blur)");
  assert.ok(!h.textarea.registrations.some((r) => r.type === "beforeinput"), "the debug-only beforeinput listener must be ABSENT with the gate OFF");
  h.keydown(229); // baseline ""
  h.textarea.value = "a";
  h.input();
  h.keyup();
  h.runDeferred();
  assert.deepEqual(h.term.inputCalls, ["a"], "the appended code point reaches term.input");
  assert.equal(h.session.iosImePending, null, "the window closed after delivery");
  assert.equal(h.session.iosImeFailsafeTimer, null, "the failsafe timer cleared on close");
  assert.deepEqual(h.logs, [], "debug OFF means zero console rows");
});

test("ios-ime 2: watermark branch C suppresses the double send", () => {
  // desktop sogou / upstream-keyup paths: xterm already delivered the text via
  // onData (the equal-length branch sends the WHOLE textarea value), the
  // watermark records it, and the flush must go branch C payload "" — deliver
  // row first, then clear(close-window) (DATA-COLLECTION §一).
  const h = makeHarness({ debug: true });
  h.textarea.value = "abc";
  h.bind();
  h.tapAndGrow(229, "."); // baseline "abc" -> "abc."
  h.fireWatermark(h.textarea.value); // xterm self-delivered the current value
  h.keyup();
  h.runDeferred();
  assert.deepEqual(h.term.inputCalls, [], "branch C delivers NOTHING");
  const deliver = h.rows("deliver");
  assert.equal(deliver.length, 1, "one deliver row");
  assert.equal(deliver[0].branch, "C", "branch C — the watermark already contains the delta");
  assert.equal(deliver[0].payload, "", "the payload is empty");
  assert.equal(h.session.iosImePending, null, "the window still closes");
  assert.deepEqual(h.rows("clear").map((r) => r.reason), ["close-window"], "clear(close-window) follows the deliver row");
  // ORDER pinned in the RAW log stream (DATA-COLLECTION §一): moving the
  // non-empty deliver log after closeIosImeWindow must fail here.
  const deliverIdx = h.logs.findIndex(([prefix]) => prefix === "[ios-ime] deliver");
  const clearIdx = h.logs.findIndex(([prefix]) => prefix === "[ios-ime] clear");
  assert.notEqual(deliverIdx, -1, "the non-empty deliver row was logged");
  assert.notEqual(clearIdx, -1, "the clear(close-window) row was logged");
  assert.ok(deliverIdx < clearIdx, "the deliver row PRECEDES its clear(close-window) row in h.logs");
});

test("ios-ime 3: composition never intercepted — taps are inert and compositionstart clears any open window", () => {
  // the candidate-bar path self-delivers via xterm's composition pipeline; the
  // bridge must not open windows mid-composition (the watermark is empty there,
  // an open window would take branch A and leak interim pinyin). compositionstart
  // additionally terminates a pre-existing window: clear + armed reset, timer
  // cleared — while the composing taps themselves touch no bridge state.
  const h = makeHarness({ debug: true });
  h.bind();
  // ARM the window FIRST (empty-delta keyup flush -> armed true, stays open):
  // the armed-reset assertion below is NON-vacuous only if something armed it.
  h.keydown(229); // open a window, baseline ""
  h.keyup();
  h.runDeferred(); // empty delta -> arm, stays open
  assert.equal(h.session.iosImeFailsafeArmed, true, "the empty-delta flush armed the window");
  assert.notEqual(h.session.iosImePending, null, "the armed window exists pre-composition");
  h.compositionstart();
  assert.equal(h.session.iosImePending, null, "compositionstart cleared the open window");
  assert.equal(h.session.iosImeFailsafeArmed, false, "armed reset with the termination");
  assert.ok(h.bridgeTimers().every((t) => t.cleared), "the window's failsafe timer cleared");
  assert.deepEqual(h.rows("clear").map((r) => r.reason), ["compositionstart"], "the compositionstart clear row");
  // flag-only tap: isComposing is UNDEFINED on the event, so ONLY the
  // iosImeComposing clause of the 229 guard can keep this tap inert.
  assert.equal(h.session.iosImeComposing, true, "compositionstart latched the composing flag");
  const failsafesBeforeFlagTap = h.bridgeTimers().length;
  h.keydown(229); // no isComposing on this event — flag-only guard path
  assert.equal(h.session.iosImePending, null, "the iosImeComposing clause kept the flag-only tap out of the state machine");
  assert.equal(h.bridgeTimers().length, failsafesBeforeFlagTap, "the flag-only tap armed no failsafe");
  const failsafesBefore = h.bridgeTimers().length;
  for (const ch of ["n", "i"]) {
    h.keydown(229, { isComposing: true });
    h.textarea.value += ch;
    h.input({ inputType: "insertCompositionText", data: ch, composed: true });
    h.keyup(229, { isComposing: true });
  }
  assert.equal(h.session.iosImePending, null, "composing taps never open a window");
  assert.equal(h.bridgeTimers().length, failsafesBefore, "composing taps never arm a failsafe");
  h.compositionend("你好");
  assert.equal(h.session.iosImeComposing, false, "compositionend unlatches the composing flag");
  assert.deepEqual(h.term.inputCalls, [], "the bridge delivered nothing — the xterm path is untouched");
});

test("ios-ime 4: multi-tap keeps the EARLIEST baseline and re-arms every 229", () => {
  // upstream semantics: the baseline is never re-read mid-window; iOS long-press
  // streams 229s so each keydown re-arms the failsafe (review M17) — the previous
  // failsafe timer entry is REPLACED, and rearmed === keptEarliest on the pending row.
  const h = makeHarness({ debug: true });
  h.bind();
  h.keydown(229); // baseline ""
  h.textarea.value = "ab";
  h.input();
  h.keydown(229); // second tap mid-window
  assert.equal(h.session.iosImePending.baseline, "", "the baseline stays the FIRST");
  const pending = h.rows("pending");
  assert.equal(pending.length, 2, "one pending row per 229 keydown");
  assert.equal(pending[1].keptEarliest, true, "the second tap keeps the earliest baseline");
  assert.equal(pending[1].rearmed, true, "rearmed === keptEarliest");
  const failsafes = h.bridgeTimers();
  assert.equal(failsafes.length, 2, "the failsafe was re-armed, not skipped");
  assert.ok(failsafes[0].cleared, "the previous failsafe timer entry was replaced");
  assert.ok(!failsafes[1].cleared, "the newest failsafe timer entry is live");
});

test("ios-ime 5: two consecutive windows never swallow each other (HIGH-1)", () => {
  // HIGH-1/M5: emissions reset at baseline creation AND at the closing flush.
  // Without reset ②, window 1's watermark ["b"] would branch-B-suppress window
  // 2's own "b" — the stale watermark must be gone before the next window.
  const h = makeHarness({ debug: true });
  h.bind();
  h.tapAndGrow(229, "a");
  h.fireWatermark("b"); // a watermark entry that WOULD suppress window 2 if it leaked
  h.keyup();
  h.runDeferred();
  assert.equal(h.session.iosImeEmissions.length, 0, "reset ②: the closing flush emptied the emissions");
  h.tapAndGrow(229, "b"); // window 2, fresh baseline "a"
  h.keyup();
  h.runDeferred();
  assert.deepEqual(h.term.inputCalls, ["a", "b"], "each window delivers its own delta exactly once");
  const deliver = h.rows("deliver");
  assert.equal(deliver.length, 2, "one deliver row per window");
  assert.equal(deliver[1].branch, "A", "window 2 sees an empty watermark — not suppressed by window 1");
});

test("ios-ime 6: failsafe rescue + the dead-key bounded second chance (N2 stage two)", () => {
  // stage one: a MISSED keyup — only the failsafe can rescue the bytes.
  // stage two (N2): a dead-key tap (229, NO insertion) leaves an empty delta:
  // the first keyup flush ARMS and stays open (bounded second chance), the
  // failsafe then closes UNCONDITIONALLY — a pending leak would poison every
  // later tap with a stale baseline. The NEXT character still delivers.
  const h = makeHarness({ debug: true });
  h.bind();
  h.tapAndGrow(229, "x"); // keydown... no keyup
  h.runFailsafe();
  assert.deepEqual(h.term.inputCalls, ["x"], "the failsafe delivers the pending bytes");
  assert.equal(h.session.iosImePending, null, "the failsafe closed the window");
  h.keydown(229); // dead-key tap: the value never grows
  h.keyup(0);
  h.runDeferred(); // empty delta -> arm, stay open
  assert.equal(h.session.iosImeFailsafeArmed, true, "the first empty flush armed");
  assert.notEqual(h.session.iosImePending, null, "the window stayed open for its second chance");
  h.runFailsafe(); // unconditional close (N2)
  assert.equal(h.session.iosImePending, null, "the failsafe closed unconditionally");
  assert.equal(h.session.iosImeFailsafeArmed, false, "armed reset on close");
  h.tapAndGrow(229, "z"); // the NEXT character delivers normally
  h.keyup();
  h.runDeferred();
  assert.deepEqual(h.term.inputCalls, ["x", "z"], "the dead key delivered nothing, the next char did");
});

test("ios-ime 7: repeated 229 re-arms; two empty-delta keyup flushes close the window (G1 stage two)", () => {
  // G1's else branch: the bounded second chance is spent by the FIRST empty
  // keyup flush; the SECOND empty flush closes (armed was already true), and
  // close resets armed so the next window gets its own fresh second chance.
  const h = makeHarness({ debug: true });
  h.bind();
  h.keydown(229);
  h.keydown(229); // re-arm — the baseline stays ""
  assert.equal(h.bridgeTimers().length, 2, "the second 229 re-armed the failsafe");
  h.keyup();
  h.runDeferred(); // empty delta, unarmed -> arm
  assert.equal(h.session.iosImeFailsafeArmed, true, "the first empty flush armed");
  assert.notEqual(h.session.iosImePending, null, "the window stayed open once");
  // armed is the AT-ENTRY value (DATA-COLLECTION §一): the first flush enters
  // unarmed, the second enters armed — forcing armedAtEntry=false fails here.
  const deliver = h.rows("deliver");
  assert.equal(deliver.length, 1, "the first empty flush emitted its deliver row");
  assert.equal(deliver[0].armed, false, "first flush's deliver row: armed === false (at-entry)");
  assert.equal(deliver[0].branch, null, "first flush's deliver row: branch === null on the empty delta");
  h.keyup();
  h.runDeferred(); // empty delta again, armed -> unconditional close (G1)
  assert.equal(h.session.iosImePending, null, "the second empty flush closed the window");
  assert.equal(h.session.iosImeFailsafeArmed, false, "close resets the armed flag");
  const deliver2 = h.rows("deliver");
  assert.equal(deliver2.length, 2, "the second empty flush emitted its deliver row");
  assert.equal(deliver2[1].armed, true, "second flush's deliver row: armed === true (at-entry)");
  assert.equal(deliver2[1].branch, null, "second flush's deliver row: branch === null on the empty delta");
  h.tapAndGrow(229, "a"); // the next character delivers
  h.keyup();
  h.runDeferred();
  assert.deepEqual(h.term.inputCalls, ["a"], "the next character delivers normally");
});

test("ios-ime 8: blur is flush-then-clear; an interleaved keyup-defer delivers exactly once (F6 stage two)", () => {
  // F6: the pending guard is the flush's FIRST line, so a keyup-deferred flush
  // that arrives AFTER the blur flush already delivered-and-closed must be a
  // no-op — correctness may not depend on arrival order. Blur also terminates a
  // still-open window: the blur flush arms (empty delta, stays open), then the
  // blur clear branch sweeps pending + timer + armed and logs clear(blur).
  const h = makeHarness({ debug: true });
  h.bind();
  h.tapAndGrow(229, "x");
  h.keyup(); // queues the deferred flush
  h.blur(); // blur flushes FIRST
  assert.deepEqual(h.term.inputCalls, ["x"], "blur delivered exactly once");
  h.runDeferred(); // the still-queued deferred flush
  assert.equal(h.session.iosImePending, null, "the deferred flush found pending null — no-op");
  assert.deepEqual(h.term.inputCalls, ["x"], "exactly ONE delivery total");
  assert.deepEqual(h.rows("clear").map((r) => r.reason), ["close-window"], "one clear row — the blur flush closed, no double log");
  h.tapAndGrow(229, ""); // a STILL-OPEN window whose flush will be empty
  h.blur(); // blur flush arms (stays open) -> blur clear branch sweeps
  assert.equal(h.session.iosImePending, null, "blur cleared the still-open window");
  assert.equal(h.session.iosImeFailsafeArmed, false, "blur reset armed on termination");
  assert.ok(h.bridgeTimers().every((t) => t.cleared), "blur cleared the failsafe timer");
  assert.deepEqual(h.rows("clear").map((r) => r.reason), ["close-window", "blur"], "the blur clear row follows");
  // stage three — blur self-heal of the missed-compositionend latch: focus
  // loss can abort a composition with NO compositionend; the blur handler's
  // UNCONDITIONAL unlatch is the only self-heal. Removing it would leave the
  // composing flag latched and mute the state machine for the term's life.
  h.compositionstart(); // latch composing, never end it
  assert.equal(h.session.iosImeComposing, true, "composing latched with no compositionend to follow");
  h.blur();
  assert.equal(h.session.iosImeComposing, false, "blur unlatched the abandoned composition unconditionally");
  assert.deepEqual(h.rows("clear").map((r) => r.reason), ["close-window", "blur"], "the self-heal blur logged no extra clear row — there was no pending to sweep");
  h.tapAndGrow(229, "y"); // the bridge is NOT muted: a plain direct-commit tap
  h.keyup();
  h.runDeferred();
  assert.deepEqual(h.term.inputCalls, ["x", "y"], "the post-blur tap delivers — the bridge self-healed");
  assert.deepEqual(h.rows("clear").map((r) => r.reason), ["close-window", "blur", "close-window"], "the self-heal window closes normally");
});

test("ios-ime 9: an external clear emits no ghost DEL — the lastSeen guard holds", () => {
  // the vendored bundle assigns textarea.value= in three places (blur clear,
  // ^C/CR clear, Linux middle-click): an external rewrite is NOT a user delete.
  // The flush diffs against iosImeSeen, never the externally cleared live
  // value — otherwise every ^C would send a phantom \x7f into the shell.
  const h = makeHarness({ debug: true });
  h.bind();
  h.textarea.value = "abc";
  h.keydown(229); // baseline "abc"
  h.textarea.value = ""; // external clear WITHOUT the input handler — xterm's blur/^C
  h.keyup();
  h.runDeferred();
  assert.deepEqual(h.term.inputCalls, [], "current fell back to iosImeSeen — zero ghost bytes");
  const deliver = h.rows("deliver");
  assert.equal(deliver.length, 1, "one deliver row");
  assert.equal(deliver[0].current, "abc", "the deliver row's current is the seen value, not the cleared live value");
  // stage two — M1 replay (PR round M1): an EXTERNAL rewrite mid-window must
  // TERMINATE the stale window at the next 229 tap, never diff against it. A
  // programmatic textarea.value="" dispatches NO input event, so seen stays
  // stale while live goes empty; the next tap sees live!==seen, kills the old
  // window (pending/timer/armed) and re-baselines to the LIVE value -- so the
  // flush yields the fresh "x", not a ghost \x7f off the stale "abc" baseline.
  h.setValueRaw("abc");
  h.keydown(229);            // live===seen here: no rewrite, the abc baseline rides on
  assert.equal(h.session.iosImePending.baseline, "abc", "the window rides on the abc baseline");
  h.setValueRaw("");         // external rewrite, NO input event -- seen stays "abc", live goes ""
  h.keydown(229);           // next tap: live!==seen -> terminate the stale window, re-baseline to live ""
  assert.equal(h.session.iosImePending.baseline, "", "the stale abc baseline was TERMINATED and re-baselined to the live empty value");
  assert.equal(h.session.iosImeFailsafeArmed, false, "termination reset the armed flag -- no double window, no stale armed carry-over");
  h.textarea.value = "x";
  h.input();                // seen now "x"
  h.keyup();
  h.runDeferred();
  assert.deepEqual(h.term.inputCalls, ["x"], "the fresh tap delivers its own x -- NOT a ghost DEL off the stale baseline");
  assert.equal(h.session.iosImePending, null, "exactly one window, now closed");
});

test("ios-ime 10: the pinned upstream #5835 equal-length sequence yields exactly \\x7f。", () => {
  // plan §3.3 pinned sequence: baseline " ，！ " -> current " ，！。": LCP=3,
  // removed=1 -> DEL + insert, branch A (empty watermark) delivers per code point.
  const h = makeHarness({ debug: true });
  h.bind();
  h.textarea.value = " ，！ ";
  h.keydown(229); // baseline " ，！ "
  h.textarea.value = " ，！。";
  h.input();
  h.keyup();
  h.runDeferred();
  assert.deepEqual(h.term.inputCalls, ["\x7f", "。"], "DEL the stale space, then insert 。 — exactly, in order");
  assert.equal(h.rows("deliver")[0].branch, "A", "empty watermark -> branch A");
});

test("ios-ime 11: iosImeDeltaBytes — pure shrink is ONE DEL, replace and append shapes pinned", () => {
  // the pure delta function straight from the sliced block: "abc"->"a" is
  // \x7f ONCE (xterm single-DEL semantics), never two; an equal-length replace
  // emits DEL×removed + insert; a pure append emits only the suffix.
  const d = (b, c) => makeHarness().api.iosImeDeltaBytes(b, c);
  assert.equal(d("abc", "a"), "\x7f", "pure shrink is exactly ONE DEL");
  assert.equal(d("abc", "axc"), "\x7f\x7fxc", "replace: DEL×removed + appended");
  assert.equal(d("abc", "abcd"), "d", "pure append: only the suffix");
});

test("ios-ime 12: emissions scope resets — baseline creation and closing flush, armed keeps", () => {
  // HIGH-1/M5 reset points: ① baseline creation empties the watermark list,
  // ② every closing flush empties it; the arm path (empty delta, stays open)
  // KEEPS it so the watermark keeps accumulating across the bounded second chance.
  const h = makeHarness({ debug: true });
  h.bind();
  h.tapAndGrow(229, "a");
  h.fireWatermark("stale");
  assert.deepEqual(h.session.iosImeEmissions, ["stale"], "the watermark recorded");
  h.keyup();
  h.runDeferred(); // closes -> reset ②
  assert.deepEqual(h.session.iosImeEmissions, [], "reset ②: the closing flush emptied the emissions");
  h.tapAndGrow(229, "b");
  h.fireWatermark("stale");
  h.compositionstart(); // clears pending but NOT emissions, latches composing
  assert.deepEqual(h.session.iosImeEmissions, ["stale"], "compositionstart keeps emissions — the next baseline resets them");
  h.compositionend("你好"); // unlatch; emissions untouched
  assert.deepEqual(h.session.iosImeEmissions, ["stale"], "compositionend keeps emissions too");
  h.keydown(229); // new baseline -> reset ①
  assert.deepEqual(h.session.iosImeEmissions, [], "reset ①: baseline creation emptied the emissions");
  h.keyup();
  h.runDeferred(); // empty delta -> arm, stays open
  h.fireWatermark("kept");
  assert.deepEqual(h.session.iosImeEmissions, ["kept"], "the arm path keeps the emissions alive across the second chance");
});

test("ios-ime 13: the iosImeSending reentrancy flag excludes the bridge's own echo", () => {
  // HIGH-1/M5: term.input goes through triggerDataEvent -> onData, so without
  // the flag the bridge's own delivery would poison the watermark and suppress
  // the NEXT window's delta. Stage one drives a REAL delivery whose fake
  // term.input RE-FIRES the watermark handler mid-send (the actual
  // triggerDataEvent -> onData echo) and observes the bridge's OWN flag-set —
  // deleting the flag-set line must fail here, not just the manual pin.
  const h = makeHarness({ debug: true });
  h.bind();
  h.tapAndGrow(229, "ab"); // window open, delta "ab"
  const term = h.term;
  const sendingAtInput = [];
  term.input = (bytes) => {
    sendingAtInput.push(h.session.iosImeSending === true);
    term.inputCalls.push(bytes);
    if (term.__wm) term.__wm(bytes); // the real echo: triggerDataEvent -> onData
  };
  h.keyup();
  h.runDeferred();
  assert.deepEqual(term.inputCalls, ["a", "b"], "the delivery itself completed — every code point");
  assert.equal(sendingAtInput.length, 2, "both code points delivered through the instrumented path");
  assert.ok(sendingAtInput.every((s) => s === true), "iosImeSending was TRUE for the whole bridge delivery");
  assert.deepEqual(h.session.iosImeEmissions, [], "the bridge's own mid-send echo never entered the watermark");
  assert.equal(h.session.iosImeSending, false, "iosImeSending reset in finally");
  // stage two: manual pin of the watermark's own flag clause (the window must
  // exist — the watermark only records while pending).
  h.tapAndGrow(229, "c"); // fresh window open
  h.session.iosImeSending = true;
  h.fireWatermark("echo");
  assert.deepEqual(h.session.iosImeEmissions, [], "while sending, the watermark ignores its own echo");
  h.session.iosImeSending = false;
  h.fireWatermark("real");
  assert.deepEqual(h.session.iosImeEmissions, ["real"], "outside sending, the watermark records");
  // stage three — emissions COUNT cap pin (plan §3.3): 70 short distinct
  // emissions through the open watermark must stop at EXACTLY the sliced
  // iosImeEmissionsMaxCount entries and never throw — the short strings keep
  // total chars far below iosImeEmissionsMaxChars, so only the count can bind.
  const shortEmissions = [];
  for (let i = 0; i < 70; i++) shortEmissions.push("e" + i);
  assert.doesNotThrow(() => { for (const s of shortEmissions) h.fireWatermark(s); }, "70 short emissions past the count cap must not throw");
  assert.equal(h.session.iosImeEmissions.length, h.api.iosImeEmissionsMaxCount, "the count cap pinned: EXACTLY iosImeEmissionsMaxCount entries survive");
  // stage four — emissions CHAR cap pin on a FRESH window (flush the capped
  // one closed first, so baseline creation resets the list): 100 long strings
  // whose cumulative length crosses iosImeEmissionsMaxChars BEFORE the 64-entry
  // count cap could bind — 80-char entries stop at 51 (4080 chars), so the
  // char cap is what freezes the list; a removed char-cap line would grow past
  // the cap (or to the count cap) and fail the assertions below.
  h.keyup();
  h.runDeferred(); // closes the capped window, delivering its own "c"
  h.tapAndGrow(229, "f"); // FRESH window, emissions reset by baseline creation
  assert.deepEqual(h.session.iosImeEmissions, [], "the fresh window starts with empty emissions");
  assert.equal(h.session.iosImeSending, false, "sending flag clear — the watermark records again");
  const longEmissions = [];
  for (let i = 0; i < 100; i++) longEmissions.push("L" + String(i).padStart(2, "0") + "x".repeat(77));
  assert.doesNotThrow(() => { for (const s of longEmissions) h.fireWatermark(s); }, "100 long emissions past the char cap must not throw");
  const recordedChars = h.session.iosImeEmissions.reduce((n, s) => n + s.length, 0);
  assert.ok(recordedChars <= h.api.iosImeEmissionsMaxChars, "the char cap pinned: total recorded chars stop at iosImeEmissionsMaxChars");
  assert.ok(h.session.iosImeEmissions.length < h.api.iosImeEmissionsMaxCount, "the char cap — NOT the count cap — halted the growth: the pin is discriminative");
  const frozenSnapshot = h.session.iosImeEmissions.slice();
  assert.doesNotThrow(() => { h.fireWatermark("x".repeat(80)); h.fireWatermark("y".repeat(80)); }, "post-cap emissions must not throw");
  assert.deepEqual(h.session.iosImeEmissions, frozenSnapshot, "stop-appending: the list is frozen at the cap");
  // stage five — the capped window still flushes normally: branch D over a
  // near-full watermark delivers its own delta and closes sane.
  h.keyup();
  h.runDeferred();
  assert.equal(h.term.inputCalls[h.term.inputCalls.length - 1], "f", "the capped window still delivered its own delta");
  assert.equal(h.session.iosImePending, null, "the capped window closed normally");
  assert.deepEqual(h.session.iosImeEmissions, [], "close reset the emissions");
  assert.equal(h.session.iosImeSending, false, "sending flag sane after the flush");
});

test("ios-ime 14: per-code-point chunking — an astral char is ONE code point", () => {
  // M6: the payload is sent per CODE POINT, never per UTF-16 unit: a surrogate
  // pair must arrive as a single term.input call, not two halves.
  const h = makeHarness({ debug: false });
  h.bind();
  h.keydown(229); // baseline ""
  h.textarea.value = "\u{1F600}x";
  h.input();
  h.keyup();
  h.runDeferred();
  assert.deepEqual(h.term.inputCalls, ["\u{1F600}", "x"], "exactly two calls, the astral char whole");
  // stage two — code-point LCP (PR round M5): replacing an astral char
  // (a😀 -> a😃) must delete exactly ONE code point and insert the new one,
  // never a lone surrogate. At UTF-16-unit granularity the LCP would land
  // mid-surrogate-pair and emit a dangling low surrogate; at code-point
  // granularity (Array.from) the whole 😀 is one removed unit -> one \x7f.
  h.setValueRaw("a\u{1F600}");
  h.keydown(229);            // fresh baseline "a\u{1F600}"
  assert.equal(h.session.iosImePending.baseline, "a\u{1F600}", "the window baselines on the astral string");
  h.textarea.value = "a\u{1F603}"; // replace 😀 with 😃 (both surrogate pairs)
  h.input();
  h.keyup();
  h.runDeferred();
  assert.deepEqual(h.term.inputCalls.slice(2), ["\x7f", "\u{1F603}"], "one DEL for the whole removed astral char, then the new one whole");
  assert.ok(!h.term.inputCalls.some((c) => c.length === 1 && c.charCodeAt(0) >= 0xDC00 && c.charCodeAt(0) <= 0xDFFF), "no lone low surrogate is EVER emitted");
  assert.ok(!h.term.inputCalls.some((c) => c.length === 1 && c.charCodeAt(0) >= 0xD800 && c.charCodeAt(0) <= 0xDBFF), "no lone high surrogate is EVER emitted");
});

test("ios-ime 15: a whole rgb: delta is never swallowed by the query-response filter", () => {
  // M6: isTerminalQueryResponse (SLICED from index.html) matches WHOLE strings
  // (^rgb:/i) — a bulk "rgb:ff0000" delivery would be filtered away upstream,
  // while per-code-point single chars never match. The sliced filter pins both
  // halves of that claim, then the bridge delivers the same string per cp.
  const h = makeHarness({ debug: true });
  assert.equal(isTerminalQueryResponse("rgb:ff0000"), true, "the whole string IS a query-response shape");
  for (const cp of Array.from("rgb:ff0000")) {
    assert.equal(isTerminalQueryResponse(cp), false, `a lone ${JSON.stringify(cp)} is never a query response`);
  }
  h.bind();
  h.keydown(229); // baseline ""
  h.textarea.value = "rgb:ff0000";
  h.input();
  h.keyup();
  h.runDeferred();
  assert.deepEqual(h.term.inputCalls, Array.from("rgb:ff0000"), "delivered per code point — no bulk whole-string to swallow");
});

test("ios-ime 16: ensure-binding is idempotent per term", () => {
  // M10: the idempotency flag is TERM-scoped, so re-ensure on the SAME term is
  // a complete no-op — no extra listeners, no extra watermark registration, and
  // exactly one [ios-ime] bound row (DATA-COLLECTION §二's collection precondition).
  const h = makeHarness({ debug: true });
  h.bind();
  const firstCount = h.textarea.registrations.length;
  const firstDisposables = h.term.disposables.length;
  const firstBoundRows = h.rows("bound").length;
  const firstDisposable = h.session.iosImeDataDisposable;
  h.bind();
  h.bind();
  assert.equal(firstCount, 7, "keydown + input + keyup + compositionstart + compositionend + blur, plus debug-only beforeinput");
  assert.equal(h.textarea.registrations.length, firstCount, "no listener growth on re-ensure");
  assert.equal(h.term.disposables.length, firstDisposables, "no watermark growth on re-ensure");
  assert.equal(h.rows("bound").length, firstBoundRows, "exactly one bound row");
  assert.equal(h.session.iosImeDataDisposable, firstDisposable, "the SAME disposable survives re-ensure");
  assert.equal(firstDisposable.disposeCount, 0, "re-ensure on the same term disposes NOTHING");
});

test("ios-ime 17: a rebuilt term re-binds with fully reset state and zero ghost bytes", () => {
  // M10: the flag is term-scoped BECAUSE a Terminal can be rebuilt onto the
  // same session object — the stale window (old baseline vs the re-initialized
  // seen value) would compute a ghost \x7f into the NEW terminal; a latched
  // composing flag or a stale armed flag would mute or mis-close its first window.
  const h = makeHarness({ debug: true });
  h.bind();
  h.keydown(229); // baseline ""
  h.keyup();
  h.runDeferred(); // empty delta -> armed, stays open
  h.session.iosImeComposing = true; // latched
  h.fireWatermark("live"); // live emissions on the old term
  const oldDisposable = h.session.iosImeDataDisposable;
  const freshTextarea = makeTextarea();
  freshTextarea.value = "fresh";
  h.session.term = makeTerm(freshTextarea); // a rebuilt term on the SAME session
  h.bind(); // rebind — the term-scoped flag allows it
  assert.equal(oldDisposable.disposed, true, "the old watermark disposable disposed");
  assert.equal(oldDisposable.disposeCount, 1, "the old watermark disposable disposed EXACTLY once — not twice");
  assert.equal(h.session.iosImePending, null, "the stale pending window was reset");
  assert.equal(h.session.iosImeFailsafeArmed, false, "the armed flag was reset");
  assert.equal(h.session.iosImeComposing, false, "the composing latch was reset");
  assert.deepEqual(h.session.iosImeEmissions, [], "the emissions were reset");
  assert.equal(h.session.iosImeSeen, "fresh", "iosImeSeen re-initialized from the NEW textarea");
  assert.equal(freshTextarea.registrations.length, 7, "the new term carries exactly one fresh listener set");
  h.tapAndGrow(229, "X");
  h.keyup();
  h.runDeferred();
  assert.deepEqual(h.session.term.inputCalls, ["X"], "the post-rebind tap delivers on the NEW term with zero ghost bytes");
});

test("ios-ime 18: dead-term tolerance — guard order and a mid-payload throw (F6 ordering asserted)", () => {
  // the failsafe timer outlives the destroy paths (plan §3.2), so flush must
  // tolerate a null term — and the pending guard is the FIRST line, before the
  // term guard (F6). A term.input throwing mid-payload drops the remainder and
  // resets iosImeSending in finally (the flushQuickKeysQueue precedent).
  const h = makeHarness({ debug: true });
  h.bind();
  h.tapAndGrow(229, "abc"); // open a pending window through the bound listener
  const term = h.session.term;
  h.session.term = null;
  assert.doesNotThrow(() => h.runFailsafe(), "a failsafe flush against a dead term must not throw");
  assert.notEqual(h.session.iosImePending, null, "the term guard returned silently — the window is intact");
  assert.doesNotThrow(() => h.api.flushIosIme({ iosImePending: null, term: null }, "blur"), "dead-term: pending guard and term guard both return silently — no throw");
  // F6 ordering made DISCRIMINATIVE by a throwing term getter: with pending
  // falsy the flush must return on line 1 and NEVER read session.term — a
  // term-first guard would read it on its first line and throw here.
  const throwingTermSession = { get term() { throw new Error("session.term touched before the pending guard"); } };
  assert.doesNotThrow(() => h.api.flushIosIme(throwingTermSession, "blur"), "F6 ordering: pending falsy — the flush never reads session.term; a term-first guard would throw");
  // non-vacuity: with pending SET the flush must reach the line-2 term guard
  // and read session.term — so the doesNotThrow above cannot pass by accident.
  assert.throws(() => h.api.flushIosIme({ iosImePending: { baseline: "" }, get term() { throw new Error("term guard reached with pending set"); } }, "blur"), /term guard reached/, "with pending set the flush reaches the term guard and reads session.term");
  h.session.term = term; // revive
  h.keyup(); // the window is still open; queue the deferred flush
  h.term.throwScript = [null, new Error("disposed"), null];
  h.runDeferred();
  assert.deepEqual(h.term.inputCalls, ["a"], "the remainder after the throw is dropped");
  assert.equal(h.session.iosImeSending, false, "iosImeSending reset in finally");
  assert.equal(h.session.iosImePending, null, "the window closed before the send (close order)");
});

test("ios-ime 19: keyup-before-input — the deferred flush arms, the failsafe delivers exactly once", () => {
  // the assumed order (§六): the deferred flush runs BEFORE the IME insertion
  // lands -> empty delta -> the bounded second chance stays open; the failsafe
  // flush then sees the grown value and delivers exactly once — not zero, not two.
  const h = makeHarness({ debug: true });
  h.bind();
  h.keydown(229); // baseline ""
  h.keyup(); // the insertion has NOT landed yet
  h.runDeferred(); // empty delta -> arm, stay open
  assert.equal(h.session.iosImeFailsafeArmed, true, "the empty-delta flush armed");
  assert.notEqual(h.session.iosImePending, null, "the window stayed open for its second chance");
  h.textarea.value = "a"; // the late insertion
  h.input();
  h.runFailsafe();
  assert.deepEqual(h.term.inputCalls, ["a"], "exactly ONE delivery of the byte");
  assert.equal(h.session.iosImePending, null, "the window closed");
  // stage two — FIFO ordering (PR round M4): emulate xterm's OWN keydown timer.
  // xterm queues its setTimeout(0) textarea diff at KEYDOWN, i.e. before the
  // bridge's keyup-defer, so under FIFO that timer runs first, lands the
  // insertion, and fills the watermark -- so the flush then adjudicates against
  // a NON-empty emissions array and branch C silences the fully-delivered
  // delta (no double send). Running the 0ms queue in LIFO order would flip the
  // branch to A and make the bridge double-send.
  h.keydown(229);                     // fresh window, baseline "a" (live===seen, not a rewrite)
  assert.equal(h.session.iosImePending.baseline, "a", "the new window baselines on the current value");
  h.scheduleZero(() => {             // xterm's own keydown timer: enqueued BEFORE our keyup-defer
    h.textarea.value = "ab";         // the insertion lands on xterm's tick
    h.input();                       // its input event refreshes seen to "ab"
    h.fireWatermark(h.textarea.value); // xterm's own onData diff delivers the whole value
  });
  h.keyup();                         // queues the bridge's keyup-defer AFTER xterm's timer
  h.runAllZero();                    // FIFO: xterm's timer first (fills the watermark), then the flush
  assert.deepEqual(h.term.inputCalls, ["a"], "exactly ONE delivery total -- the bridge added NOTHING");
  const del = h.rows("deliver");
  assert.equal(del[del.length - 1].branch, "C", "the flush adjudicated against the NON-empty watermark -> branch C");
  assert.equal(del[del.length - 1].payload, "", "branch C is silent for the fully-delivered delta");
  assert.equal(h.session.iosImePending, null, "the window closed");
});

test("ios-ime 20: a non-229 keydown is inert — no window, no timer, no pending row", () => {
  // keyCode 229 is the IME-tap signature; plain keys ride xterm's normal path
  // and the bridge must stay completely out of the way (plan §3.2).
  const h = makeHarness({ debug: true });
  h.bind();
  h.keydown(65);
  assert.equal(h.session.iosImePending, null, "no pending window");
  assert.equal(h.bridgeTimers().length, 0, "no failsafe timer");
  assert.deepEqual(h.rows("pending"), [], "no pending row");
  assert.equal(h.session.iosImeFailsafeArmed, false, "armed untouched");
});
