// Behavioral tests for the issue #100 R17 terminal touch-scroll mapping
// (PR #110 beta follow-up): vendored xterm v6 has no working touch scroll
// (the .xterm-viewport holds no scrollable content, touches land on
// .xterm-screen, and the Gesture singleton maps no scroll), so
// initTerminalTouchScroll() maps a one-finger drag to term.scrollLines()
// itself, while the unconditional non-passive touchmove preventDefault
// hard-locks the page rubber-band on every iOS version. Direction FOLLOWS
// THE FINGER (iOS/macOS content-follows-finger): an upward finger reveals
// newer content (positive scrollLines count), a downward finger walks
// toward older content (negative count). These cases slice the shipped
// initTerminalTouchScroll AND its three module-level state declarations
// straight from the served source — nothing is hand-mirrored (§6) — and
// drive them against stubbed touch events. Per PRD-CHANGE §5.6-1:
// direction sign, remainder carry, multi-finger abort, the no-session
// lock, and touchend/touchcancel reset.
//
// Same arrangement as touch_interaction.test.mjs. Run:
// node --test testdata/terminal_touch_scroll.test.mjs
// (also wired into terminal_status_test.go for `go test`).
import test from "node:test";
import assert from "node:assert/strict";
import { readFileSync } from "node:fs";
import { fileURLToPath } from "node:url";
import { dirname, join } from "node:path";

const here = dirname(fileURLToPath(import.meta.url));
const staticDir = join(here, "..", "static");
const indexSource = readFileSync(join(staticDir, "index.html"), "utf8");

// --- source slicing helpers (same approach as touch_interaction.test.mjs) ---

function inlineScript(html) {
  const start = html.lastIndexOf("<script>");
  const end = html.indexOf("</script>", start);
  assert.ok(start > 0 && end > start, "could not locate index.html's inline <script> block");
  return html.slice(start + "<script>".length, end);
}

// Input assumptions (§6 helper discipline): brace matching does NOT skip
// braces inside comments or strings — a sliced block whose comments/strings
// are not brace-balanced truncates and fails loudly at evaluation time
// (SyntaxError from new Function), which is the wanted failure mode.
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

// The three module-level state declarations, sliced from the served source
// as one contiguous span (never hand-mirrored — §6). The factory body is
// "use strict" so a rename/missing declaration turns the handlers' writes
// into a loud ReferenceError instead of a silent implicit global.
const SLICED_STATE = (() => {
  const src = inlineScript(indexSource);
  const head = "let activeTouchId = null;";
  const tail = "let touchScrollRemainderPx = 0;";
  const start = src.indexOf(head);
  assert.notEqual(start, -1, `source no longer contains ${JSON.stringify(head)}`);
  const end = src.indexOf(tail, start);
  assert.notEqual(end, -1, `source no longer contains ${JSON.stringify(tail)} after the state block head`);
  return src.slice(start, end + tail.length);
})();

// --- index.html harness -----------------------------------------------------

const factory = new Function("deps", `
  "use strict";
  const { document, getActiveTerminalSession } = deps;
  // Sliced from the shipped script (see SLICED_STATE above):
  ${SLICED_STATE}
  ${sliceBlock(inlineScript(indexSource), "function initTerminalTouchScroll")}
  return {
    initTerminalTouchScroll,
    state: () => ({ id: activeTouchId, lastY: activeTouchLastY, remainder: touchScrollRemainderPx }),
  };
`);

// Listeners per type are stored as ARRAYS: a future double-init must
// surface as length > 1 instead of silently overwriting (NIT-4).
function makeContainer() {
  const listeners = new Map(); // type -> [{fn, opts}]
  return {
    listeners,
    addEventListener(type, fn, opts) {
      if (!listeners.has(type)) listeners.set(type, []);
      listeners.get(type).push({ fn, opts });
    },
  };
}

function touchEvent({ touches = [], changedTouches = [] } = {}) {
  return {
    touches, changedTouches,
    prevented: false,
    preventDefault() { this.prevented = true; },
  };
}

function touchHarness({ withSession = true } = {}) {
  const container = makeContainer();
  const scrollCalls = [];
  let session = withSession ? {
    term: {
      options: { fontSize: 16, lineHeight: 1.2 }, // linePx = 19.2
      scrollLines(n) { scrollCalls.push(n); },
    },
  } : null;
  const api = factory({
    document: { getElementById: (id) => (id === "terminal-container" ? container : null) },
    getActiveTerminalSession: () => session,
  });
  api.initTerminalTouchScroll();
  const fire = (type, ev) => container.listeners.get(type)[0].fn(ev);
  return {
    api, container, scrollCalls, fire,
    setSession: (s) => { session = s; },
    registration: (type) => container.listeners.get(type).map((l) => l.opts),
  };
}

const LINE_PX = 16 * 1.2; // 19.2

function assertSingleRegistration(h, t) {
  for (const type of ["touchstart", "touchmove", "touchend", "touchcancel"]) {
    assert.equal(h.container.listeners.get(type).length, 1, `${type}: exactly one delegated listener — a double init must be visible`);
  }
}

// --- §5.6-1 cases ------------------------------------------------------------

test("issue #100 R17: direction follows the finger — a DOWNWARD drag walks toward old content (negative count), an UPWARD drag reveals newer content (positive count)", () => {
  const h = touchHarness();
  assertSingleRegistration(h);
  assert.deepEqual(h.registration("touchstart"), [{ passive: true }], "touchstart stays passive (tap path untouched)");
  assert.deepEqual(h.registration("touchmove"), [{ passive: false }], "touchmove is the non-passive hard lock");

  h.fire("touchstart", touchEvent({ touches: [{ identifier: 7, clientY: 300 }] }));
  const tapProbe = touchEvent({ touches: [{ identifier: 7, clientY: 300 }] });
  h.fire("touchstart", tapProbe);
  assert.equal(tapProbe.prevented, false, "touchstart never preventDefaults — taps keep focus/keyboard");
  const down = touchEvent({ touches: [{ identifier: 7, clientY: 300 + LINE_PX + 5 }] }); // dy = -24.2
  h.fire("touchmove", down);
  assert.equal(down.prevented, true, "touchmove hard-locks the page");
  assert.deepEqual(h.scrollCalls, [-1], "one full line DOWN -> scrollLines(-1), toward OLD content");
  assert.ok(Math.abs(h.api.state().remainder + 5) < 1e-9, "the sub-line remainder carries, signed");
  const up = touchEvent({ touches: [{ identifier: 7, clientY: 300 + LINE_PX + 5 - 40 }] }); // dy = +40
  h.fire("touchmove", up);
  assert.deepEqual(h.scrollCalls, [-1, 1], "a full line UP -> scrollLines(1), toward NEWER content");
  // fontSize 0/undefined must not poison the accumulator with NaN (NIT-6).
  h.setSession({ term: { options: { fontSize: 0 }, scrollLines(n) { h.scrollCalls.push(n); } } });
  h.fire("touchstart", touchEvent({ touches: [{ identifier: 8, clientY: 100 }] }));
  h.fire("touchmove", touchEvent({ touches: [{ identifier: 8, clientY: 100 - 16 }] })); // linePx falls back to 13 * 1 = 13
  assert.deepEqual(h.scrollCalls, [-1, 1, 1], "fontSize 0 falls back to 13px lines — still finite, still scrolls");
  assert.ok(Number.isFinite(h.api.state().remainder), "the accumulator never goes NaN");
});

test("issue #100 R17: sub-line drifts accumulate across events — two 0.6-line moves roll exactly one line, remainder preserved", () => {
  const h = touchHarness();
  const half = LINE_PX * 0.6; // 11.52 — alone, not a line
  h.fire("touchstart", touchEvent({ touches: [{ identifier: 1, clientY: 500 }] }));
  h.fire("touchmove", touchEvent({ touches: [{ identifier: 1, clientY: 500 - half }] }));
  assert.deepEqual(h.scrollCalls, [], "0.6 line alone scrolls nothing (no jump)");
  h.fire("touchmove", touchEvent({ touches: [{ identifier: 1, clientY: 500 - half - half }] }));
  assert.deepEqual(h.scrollCalls, [1], "two 0.6-line moves = one whole line (positive = newer content)");
  assert.ok(Math.abs(h.api.state().remainder - (LINE_PX * 1.2 - LINE_PX)) < 1e-9, "remainder = 23.04 - 19.2 = 3.84 carries on");
  // And the carried remainder joins the NEXT event: 3.84 + 16 = 19.84 —
  // one more line, with 0.64 carrying on (16 chosen clear of the float
  // knife-edge at exactly 19.2).
  h.fire("touchmove", touchEvent({ touches: [{ identifier: 1, clientY: 500 - half - half - 16 }] }));
  assert.equal(h.scrollCalls.length, 2, "the carried remainder makes the next line roll");
  assert.ok(Math.abs(h.api.state().remainder - (LINE_PX * 1.2 + 16 - LINE_PX * 2)) < 1e-9, "19.84 - 19.2 = 0.64 carries on");
});

test("issue #100 R17: a second finger aborts the mapping (no pinch semantics), and the page lock still fires", () => {
  const h = touchHarness();
  h.fire("touchstart", touchEvent({ touches: [{ identifier: 3, clientY: 400 }] }));
  assert.equal(h.api.state().id, 3, "one finger starts the tracking");
  h.fire("touchstart", touchEvent({ touches: [{ identifier: 3, clientY: 400 }, { identifier: 4, clientY: 420 }] }));
  assert.equal(h.api.state().id, null, "a second finger aborts the mapping");
  const move = touchEvent({ touches: [{ identifier: 3, clientY: 100 }] });
  h.fire("touchmove", move);
  assert.equal(move.prevented, true, "the page lock is unconditional");
  assert.deepEqual(h.scrollCalls, [], "no scrolling while aborted");
});

test("issue #100 R17: with no active session the page still locks and nothing throws", () => {
  const h = touchHarness({ withSession: false });
  h.fire("touchstart", touchEvent({ touches: [{ identifier: 9, clientY: 250 }] }));
  const move = touchEvent({ touches: [{ identifier: 9, clientY: 100 }] });
  assert.doesNotThrow(() => h.fire("touchmove", move), "no session must not throw");
  assert.equal(move.prevented, true, "the lock holds with no session");
  assert.deepEqual(h.scrollCalls, [], "no session -> no scrolling");
});

test("issue #100 R17: touchend (and touchcancel) reset the gesture — the next one starts from zero", () => {
  for (const endType of ["touchend", "touchcancel"]) {
    const h = touchHarness();
    h.fire("touchstart", touchEvent({ touches: [{ identifier: 5, clientY: 300 }] }));
    h.fire("touchmove", touchEvent({ touches: [{ identifier: 5, clientY: 300 - LINE_PX * 0.6 }] }));
    assert.ok(Math.abs(h.api.state().remainder - LINE_PX * 0.6) < 1e-9, `${endType}: remainder accrued mid-gesture`);
    // An unrelated finger lifting must not reset the tracked gesture.
    h.fire(endType, touchEvent({ changedTouches: [{ identifier: 99, clientY: 0 }] }));
    assert.equal(h.api.state().id, 5, `${endType}: another touch lifting does not reset the tracked one`);
    h.fire(endType, touchEvent({ changedTouches: [{ identifier: 5, clientY: 0 }] }));
    assert.deepEqual(h.api.state(), { id: null, lastY: 0, remainder: 0 }, `${endType}: the tracked touch resets everything`);
    // New gesture: 0.6 line alone must again scroll NOTHING — proving the
    // old remainder is gone.
    h.fire("touchstart", touchEvent({ touches: [{ identifier: 6, clientY: 200 }] }));
    h.fire("touchmove", touchEvent({ touches: [{ identifier: 6, clientY: 200 - LINE_PX * 0.6 }] }));
    assert.deepEqual(h.scrollCalls, [], `${endType}: the new gesture starts from a zero accumulator`);
  }
});
