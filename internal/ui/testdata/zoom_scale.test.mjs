// issue #116 D2 — the signed scale-asymmetric soft-keyboard avoidance (PLAN §4
// D2, signed by 32fab22; the Phase 2 implementation verdicts D-A..D-D of the
// arbitration commit 6e8a006), executed against the shipped index.html. Same
// slicing harness as viewport_height.test.mjs: the judgment is cut out of the
// REAL served document and run on stubbed browser state, so a drift between
// this file's expectations and the shipped guard fails loudly.
//
// THE SIGNED ASYMMETRY, as this file tests it (arbitration 6e8a006):
//   * scale > 1  — the frozen conservative opt-out, UNCHANGED: every pass exits
//     any session (recovery latch included) and nothing may enter. The app
//     never writes scale; the release back to 1 belongs to Safari.
//   * scale ≈ 1  — the frozen ±0.01 tolerance, numbers unchanged, the whole
//     session machine untouched (regression red line; in-band jitter is OWNED
//     BY THE FROZEN THRESHOLD, D-D; the fuller scale = 1 matrix lives in
//     viewport_height.test.mjs).
//   * scale < 1  — NO blanket opt-out. A SETTLED scale enters through the
//     UNTOUCHED §5 gates: editable focus through the existing
//     document.activeElement path (D-A plan B — the frozen generic predicate,
//     no new host machine), raw = max(0, clientHeight − vv.height) > 1, the
//     25% entry / 50% re-entry lines, click / focusin / geometry-change
//     unlocks, the same-geometry-decrease exit and its recovery latch,
//     occlusion = raw in full.
//   * THE COUNTED SETTLE (D-C) — a settle is NOT a single-frame equality
//     (the first draft's `scale === previousTerminalScale` was superseded):
//     SCALE_SETTLE_TICKS = 2 consecutive 500ms self-check tick passes, each
//     inside SCALE_SETTLE_EPSILON = 0.001 of the candidate baseline (slow
//     drift accumulated past ε breaks the candidate even when every adjacent
//     step is inside ε), with layoutHeight / vv.width / raw unchanged while
//     the candidate is CANDIDATING. Only the tick carrying the
//     "scaleSettleTick" token counts — resize / scroll / focus / lifecycle
//     passes may break a candidate but never advance one; document.hidden
//     accumulates nothing; the count SATURATES, so an identical settled tick
//     is idempotent; after the settle, keyboard raw opening and closing no
//     longer resets the scale settle (frozen §5 handles that machine).
//   * ANY OUT-OF-BAND SCALE STEP = NEW GEOMETRY (D2 rule 3 + D-B / D-D):
//     a scale<1 step, and the trip BACK into the band (0.75 → 1, scale>1 → 1),
//     exit through the existing frozen exit edge and pay occlusion 0; the old
//     latch is retired by the existing geometry-change unlock; the step frame
//     books NO NEW recovery latch (scaleStepNoLatchExit — the narrow D2
//     exception to the frozen §5.6 latching exits, spelled nowhere else); the
//     settle then re-judges on the UNLOCKED 25% line, identically for a
//     single-step or a multi-step arrival. That 25% is the PURE scale<1 /
//     0.75 → 1 path only: a latch an earlier scale>1 pass booked is NOT erased
//     by the trip home or by the settle, so scale>1 → 1 re-enters on the 50%
//     recovering line unless a click / focusin / geometry change unlocked it
//     first (D-D / D2-6). Every other exit — scale > 1, raw <= 1, focus loss,
//     the same-geometry decrease — keeps the frozen latch exactly as before.
//   * ownership stays where the frozen code put it: the guard's ONLY focus
//     input is document.activeElement + the frozen editable test (D-A plan B).
//     This file PROVES the D2 edit owns nothing NEW (no parentElement /
//     closest / classList / getAttribute read); under scale < 1 an ordinary
//     Web input passing that generic predicate is the registered R2 known
//     deviation — a Phase 3 dependency, never a D2 PTY-admission proof.
//
// Device-faithful geometry (from the read-only evidence A0 0.75 segment, 153
// frames, raw/final116-20261104-a.jsonl — referenced, never rewritten here):
// at scale = 0.75 the layout viewport EXPANDS (clientHeight 936 = 702 / 0.75,
// vv.width 1592 = 1194 / 0.75); with the keyboard open vv.height ≈ 274 / s
// (measured 365.328125), so raw = 428 / s > 1 (measured 570.67 ≈ 61% of the
// layout); keyboard CLOSED means vv.height == clientHeight — raw 0 by exact
// arithmetic, which is why a keyboard-free zoom-out cannot lift the terminal:
// raw <= 1 ends and blocks on its own, and the state machine never enters.
// The DEEP-enough keyboard for distinguishing the 25% line from the 50% line
// after a step (arbitration: the 61% keyboard clears BOTH lines — useless as
// a discriminator) is the baseVisible = 402 band: raw 300 ≈ 42.7% of 702 at
// scale = 1, raw 400 ≈ 42.7% of 936 at scale = 0.75 — past 25% (175.5 / 234)
// and short of 50% (351 / 468).
//
// Run: node --test testdata/zoom_scale.test.mjs
import test from "node:test";
import assert from "node:assert/strict";
import { readFileSync } from "node:fs";
import { fileURLToPath } from "node:url";
import { dirname, join } from "node:path";

const here = dirname(fileURLToPath(import.meta.url));
const staticDir = join(here, "..", "static");
const indexSource = readFileSync(join(staticDir, "index.html"), "utf8");

// The scale = 1 calibration layout the frozen contract ships against
// (viewport_height.test.mjs): landscape, safe-area bottom 20, a real soft
// keyboard leaving vv.height 274 -> raw 428 = 61.0% of 702.
const L0 = 702;
const W0 = 1194;
const SAFE_BOTTOM = 20;
const KB_VISIBLE = 274; // keyboard-open visible height at scale = 1

function inlineScript(html) {
  const start = html.lastIndexOf("<script>");
  const end = html.indexOf("</script>", start);
  assert.ok(start > 0 && end > start, "could not locate index.html's inline <script> block");
  return html.slice(start + "<script>".length, end);
}

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

function stripComments(src) {
  return src.replace(/\/\/[^\n]*/g, "").replace(/\/\*[\s\S]*?\*\//g, "");
}

function buildZoomFactory(src) {
  const slices = [
    "function applyVisualViewportOcclusion",
    "function reapplyVisualViewportOcclusion",
    "function initVisualViewportHandling",
  ].map((header) => sliceBlock(src, header)).join("\n\n");
  return new Function("deps", `
  const { window, document, setInterval: injectInterval } = deps;
  const setInterval = (fn, ms) => injectInterval(fn, ms);
  // Mirrors index.html's module-level session history, previousTerminalScale
  // included — the D2 settle memory lives there, nowhere else.
  let terminalAvoidanceActive = false;
  let terminalAvoidanceRecovering = false;
  let terminalAvoidanceGeometryGrace = false;
  let previousTerminalRaw = 0;
  let previousTerminalLayoutHeight = 0;
  let previousTerminalVVWidth = 0;
  let previousTerminalScale = 0;
  // D-C (arbitration commit 6e8a006): mirrors index.html's counted settle memory
  // (index.html:3759–3766) character for character — the sliced harness must own
  // the SAME module state the shipped guard reads, or the slice dies at the first
  // scale question with a ReferenceError instead of judging.
  const SCALE_SETTLE_TICKS = 2;
  const SCALE_SETTLE_EPSILON = 0.001;
  let scaleSettleCandidate = 0;
  let scaleSettleBaseline = 0;
  let scaleSettleTicked = 0;
  let scaleSettleLayoutHeight = 0;
  let scaleSettleVVWidth = 0;
  let scaleSettleRaw = 0;

  ${slices}
  return {
    applyVisualViewportOcclusion,
    reapplyVisualViewportOcclusion,
    initVisualViewportHandling,
    isAvoidanceActive: () => terminalAvoidanceActive,
    isRecovering: () => terminalAvoidanceRecovering,
    // READ-ONLY mirrors of the D-C settle memory and the D2 scale history. The
    // verdict (active / occlusion) cannot tell "no candidate" from "candidate
    // banked but pending", and the arbitration's rules are ABOUT that memory
    // (never bank in the scale>1 domain, never reset on a sub-ε step, break on a
    // layout / width change): these getters let the cases name the difference
    // without changing any judgment. Nothing in index.html reads them.
    settleState: () => ({
      candidate: scaleSettleCandidate,
      baseline: scaleSettleBaseline,
      ticked: scaleSettleTicked,
      layoutHeight: scaleSettleLayoutHeight,
      vvWidth: scaleSettleVVWidth,
      raw: scaleSettleRaw,
    }),
    previousScale: () => previousTerminalScale,
  };
`);
}

const zoomFactory = buildZoomFactory(inlineScript(indexSource));

function makeBottomBox(id) {
  const el = { id, style: {}, writes: [] };
  Object.defineProperty(el.style, "bottom", {
    configurable: true,
    get() { return this._bottom === undefined ? "" : this._bottom; },
    set(value) { this._bottom = value; el.writes.push(value); },
  });
  return el;
}

/**
 * `baseLayout` / `baseWidth` / `baseVisible` are the SCALE = 1 geometry.
 * zoomOutPass re-derives the zoomed layout the way iOS does (everything grows
 * by 1/scale; with the keyboard open only the visible band shrinks first and
 * then grows back scaled, staying a constant FRACTION of the layout — so the
 * §5 lines filter identically at every settled scale, which is exactly what
 * the evidence shows: 428/702 = 61.0% = 570.67/936 at scale 0.75).
 */
function zoomHarness({
  baseLayout = L0,
  baseWidth = W0,
  baseVisible = KB_VISIBLE,
  focus = { tagName: "TEXTAREA" }, // the xterm helper — the existing editable path
  hostId = "a",
} = {}) {
  const root = { clientHeight: baseLayout };
  const listeners = new Map();
  const vv = {
    height: baseLayout,
    width: baseWidth,
    offsetTop: 0,
    scale: 1,
    addEventListener(type, cb) {
      if (!listeners.has(type)) listeners.set(type, []);
      listeners.get(type).push(cb);
    },
    dispatch(type) {
      for (const cb of listeners.get(type) ?? []) cb({ type });
    },
  };
  const containerListeners = new Map();
  const host = makeBottomBox(hostId);
  Object.defineProperty(host, "getBoundingClientRect", {
    value: () => {
      const bottom = (root.clientHeight - SAFE_BOTTOM) - (parseFloat(host.style.bottom || "0") || 0);
      return { top: 0, bottom, height: bottom };
    },
  });
  const container = {
    addEventListener(type, cb) {
      if (!containerListeners.has(type)) containerListeners.set(type, []);
      containerListeners.get(type).push(cb);
    },
    dispatch(type) {
      for (const cb of containerListeners.get(type) ?? []) cb({ type });
    },
    querySelectorAll(selector) {
      assert.equal(selector, ":scope > [data-instance-id]", "the host selector the applier queries changed; re-audit the harness");
      return [host];
    },
  };
  const controls = makeBottomBox("terminal-controls");
  const statusBar = makeBottomBox("status-bar");
  const docEvents = new Map();
  // D-E (arbitration 6e8a006 re-review): #terminal-container can be ABSENT (an
  // instance switch, a teardown mid-zoom). The applier's `!container` early return
  // still has to keep the D2 scale history honest, so the harness can hide and
  // bring back exactly that one element and drive the judgment in between.
  let containerVisible = true;
  const document = {
    documentElement: root,
    hidden: false,
    activeElement: focus,
    getElementById(id) {
      if (id === "terminal-container") return containerVisible ? container : null;
      if (id === "terminal-controls") return controls;
      if (id === "status-bar") return statusBar;
      return null;
    },
    addEventListener(type, handler) {
      if (!docEvents.has(type)) docEvents.set(type, []);
      docEvents.get(type).push(handler);
    },
    dispatch(type) {
      for (const handler of docEvents.get(type) ?? []) handler({ type });
    },
  };
  const window = {
    addEventListener() {},
    visualViewport: vv,
  };
  const intervals = [];
  const api = zoomFactory({
    window,
    document,
    setInterval: (fn) => { intervals.push(fn); return intervals.length; },
  });
  return {
    api, vv, root, host, controls, statusBar, document, window,
    bottoms: () => [host.style.bottom],
    hostBottomRect: () => host.getBoundingClientRect().bottom,
    occlusion: () => (window.__mwTerminalOcclusion === undefined ? null : window.__mwTerminalOcclusion),
    active: () => api.isAvoidanceActive(),
    recovering: () => api.isRecovering(),
    hostWrites: () => host.writes.length,
    tick: () => { for (const fn of intervals) fn(); },
    // The container-absent window (D-E): the judgment runs, the early return
    // answers, and NOTHING is written — so a case can step the scale inside the
    // window and still observe what the first frame back decides.
    containerGone: () => { containerVisible = false; },
    containerBack: () => { containerVisible = true; },
    applyDirect: (token) => api.applyVisualViewportOcclusion(token),
    click: () => container.dispatch("click"),
    focusin: () => document.dispatch("focusin"),
    // A discrete ZOOM-OUT step at the current keyboard state. iOS grows the
    // layout viewport (and vv.width) by 1/scale; with the keyboard up the
    // visible band is baseVisible / scale, keyboard-less it equals the layout.
    zoomOutPass: (scale, { keyboardOpen = false } = {}) => {
      root.clientHeight = baseLayout / scale;
      vv.width = baseWidth / scale;
      vv.height = keyboardOpen ? baseVisible / scale : root.clientHeight;
      vv.scale = scale;
      vv.dispatch("resize");
    },
    // A discrete ZOOM-IN step: past scale = 1 the iOS layout viewport sits
    // back at its scale-1 base; only vv.height shrinks with the scale. A
    // step BACK to exactly 1 is also taken here (layout stays at the base).
    zoomInPass: (scale, { visibleHeight = baseVisible } = {}) => {
      root.clientHeight = baseLayout;
      vv.width = baseWidth;
      vv.height = visibleHeight;
      vv.scale = scale;
      vv.dispatch("resize");
    },
    // One pass at the CURRENT geometry — a stable resize dispatch. Under the
    // D-C counted settle (arbitration 6e8a006) this is NOT a settle: an event
    // pass carries no "scaleSettleTick" token, so it can keep a candidate
    // alive or break one, but only tick() advances the count.
    settlePass: () => vv.dispatch("resize"),
    // Move the keyboard band at the CURRENT scale/layout (no zoom change).
    keyboardTo: (visibleHeight) => { vv.height = visibleHeight; vv.dispatch("resize"); },
  };
}

// --- 1. scale = 1 baseline regression (the red line; frozen behavior) --------

test("D2-1a scale=1: entering keyboard at raw 428 still avoids exactly as before", () => {
  const h = zoomHarness();
  h.api.initVisualViewportHandling();
  h.keyboardTo(KB_VISIBLE);
  assert.equal(h.active(), true, "scale = 1: 428/702 clears the 25% entry line");
  assert.deepEqual(h.bottoms(), ["428px"]);
  assert.equal(h.occlusion(), 428, "an active session pays occlusion = raw, unchanged");
  assert.equal(h.controls.style.bottom, "448px");
  assert.equal(h.statusBar.style.bottom, "428px");
});

test("D2-1b scale=1: a same-geometry decrease exits into the recovery latch; a fresh 50% keyboard re-enters", () => {
  const h = zoomHarness();
  h.api.initVisualViewportHandling();
  h.keyboardTo(KB_VISIBLE);
  h.keyboardTo(495.9); // raw 206.1 < 428 − 1, same geometry: exit + latch
  assert.equal(h.active(), false);
  assert.deepEqual(h.bottoms(), ["0px"]);
  h.tick();
  assert.deepEqual(h.bottoms(), ["0px"], "the 500ms backstop cannot reopen past the latch");
  h.keyboardTo(KB_VISIBLE); // raw 428 ≥ 351: the fresh 50% keyboard re-enters
  assert.equal(h.active(), true);
  assert.deepEqual(h.bottoms(), ["428px"]);
});

test("D2-1c scale=1: the shallow band stays shut behind the latch; a new click unlocks the 25% line", () => {
  const h = zoomHarness();
  h.api.initVisualViewportHandling();
  h.keyboardTo(KB_VISIBLE);
  h.keyboardTo(L0); // raw 0 closes and latches
  assert.equal(h.active(), false);
  h.keyboardTo(555); // raw 147 < 175.5 = 25%: shallow stays shut
  assert.deepEqual(h.bottoms(), ["0px"]);
  h.click(); // the frozen click unlock, untouched by D2
  h.keyboardTo(500); // raw 202 ≥ 175.5: unlocked, the 25% line re-enters
  assert.equal(h.active(), true, "after the click the frozen 25% entry line governs again");
  assert.deepEqual(h.bottoms(), ["202px"]);
});

// --- 2. scale = 0.75 + editable focus + raw > 1 + settle → avoid raw ---------

test("D2-2a scale=0.75 settled, editable focus, raw > 1: enters at the 25% line and pays occlusion = raw", () => {
  const h = zoomHarness();
  h.api.initVisualViewportHandling();
  const initWrites = h.hostWrites();
  // Zoom out with the keyboard CLOSED (device rhythm): the transition pass may
  // move nothing, and a keyboard-free pass owes 0 by arithmetic anyway.
  h.zoomOutPass(0.75);
  assert.equal(h.root.clientHeight, 936, "device evidence: clientHeight 936 at scale 0.75");
  assert.deepEqual(h.bottoms(), ["0px"], "a zoom-out transition pass moves nothing");
  // The keyboard opens: raw becomes 428/0.75 = 570.67 — the SAME 61.0%
  // fraction as at scale = 1, clearing the 25% line of the zoomed layout
  // (936 × 0.25 = 234). But the keyboard-open pass RE-OPENS the D-C
  // candidate (the raw moved while it was pending), so entry lands on the
  // second consecutive counting tick, not on a resize. D2 admits the STATE;
  // the counting settle opens the gate; the line still decides the SIZE.
  h.keyboardTo(KB_VISIBLE / 0.75);
  assert.equal(h.active(), false, "the keyboard-open pass re-opens the candidate — same-scale-as-previous is no longer the settle (D-C)");
  h.tick();
  assert.equal(h.active(), false, "ONE counting tick at unchanged geometry is not a settle (N=2)");
  h.tick();
  assert.equal(h.active(), true, "editable focus + raw 570.67 > 1 + TWO consecutive counting ticks: the settled scale < 1 enters");
  assert.equal(h.occlusion(), Math.max(0, 936 - KB_VISIBLE / 0.75), "occlusion = raw, in full");
  assert.deepEqual(h.bottoms(), [h.occlusion() + "px"]);
  assert.equal(h.hostBottomRect(), 936 - SAFE_BOTTOM - h.occlusion(), "the host sits exactly raw px above its authored bottom");
  assert.equal(h.controls.style.bottom, (20 + h.occlusion()) + "px", "the column pays 20 + raw at zoom too");
  assert.equal(h.statusBar.style.bottom, h.occlusion() + "px");
  // Stable passes re-write nothing — the guarded-write mechanism is untouched:
  // exactly ONE host write happened after the init baseline (the lift itself).
  h.tick();
  h.tick();
  h.settlePass();
  assert.equal(h.hostWrites(), initWrites + 1, "only the lift wrote; settle/backstop passes are compares, not writes");
});

test("D2-2b scale=0.75: same-geometry decrease exits and latches; the 50% line re-enters; click unlocks the 25% line", () => {
  const h = zoomHarness();
  h.api.initVisualViewportHandling();
  h.zoomOutPass(0.75);
  h.keyboardTo(KB_VISIBLE / 0.75); // raw 570.67 under the SAME scale — re-opens the candidate
  h.tick();
  h.tick(); // D-C: two consecutive counting ticks settle the scale…
  assert.equal(h.active(), true, "…and the settled zoom keyboard at 25% enters");
  // The keyboard retracts by 150 layout px — same layout, same width, same
  // scale: the SAME geometry under the frozen tuple (D2 added the scale term
  // INSIDE zoom-out only, and the scale did not change here). The session
  // exits and latches exactly like scale = 1, and it exits through the FROZEN
  // SAME-GEOMETRY-DECREASE branch, not the zoom one: the scale never moved, the
  // candidate stayed SATURATED at two ticks, so scaleBelowOneSettled is still
  // true and zoomOptOut is still false on this frame. A raw move AFTER the
  // settle never re-opens the candidate (D-C re-review ④ / C10) — the settle
  // memory and the exit are two different machines.
  const shallowVisible = KB_VISIBLE / 0.75 + 150; // raw 420.67 < 468 = 50%
  h.keyboardTo(shallowVisible);
  assert.equal(h.active(), false, "same-geometry decrease exits at scale < 1 exactly like scale = 1");
  assert.deepEqual(h.bottoms(), ["0px"]);
  assert.equal(h.api.settleState().ticked, 2, "the candidate stayed SATURATED — a post-settle raw move does not re-open it (C10)");
  h.tick();
  assert.deepEqual(h.bottoms(), ["0px"], "the latch holds across the backstop");
  h.keyboardTo(KB_VISIBLE / 0.75); // raw 570.67 ≥ 468 = 50%, and the scale is STILL settled
  assert.equal(h.active(), true, "the 50% line was the ONLY thing missing: the settled scale never re-counts, so the frame that moves the raw re-enters (C10)");
  h.tick();
  h.tick(); // idempotent — the candidate is saturated and the settle was announced once
  assert.deepEqual(h.bottoms(), [(936 - KB_VISIBLE / 0.75) + "px"]);
  // Exit again and prove the §5.5 click unlock lives at zoom too: the shallow
  // 420.67 sits below the 50% recovering line and above the 25% entry line.
  h.keyboardTo(shallowVisible);
  assert.equal(h.active(), false);
  h.click(); // the frozen click unlock, unchanged — it clears the latch only…
  h.tick(); // …and the NEXT judgment pass runs the machine: 420.67 ≥ 234 = 25% admits (the scale is already settled)
  h.tick(); // idempotent, again
  assert.equal(h.active(), true, "unlocked + settled, 420.67 ≥ 234 = 25%: re-enters");
  assert.deepEqual(h.bottoms(), [(936 - shallowVisible) + "px"]);
});

test("D2-2c scale<1: a scale step exits an active session at once and pays 0 with NO new recovery latch (D-B); the counted settle re-judges on the UNLOCKED 25% line", () => {
  // DEEP-enough keyboard (arbitration: a 61% keyboard clears BOTH lines and
  // proves nothing): baseVisible 402 gives raw 300 ≈ 42.7% of the 702 layout
  // at scale = 1, and raw 400 ≈ 42.7% of the 936 layout at scale = 0.75 —
  // past the 25% entry line (175.5 / 234) and SHORT of the 50% recovering
  // line (351 / 468). The first draft let the step exit book a fresh latch,
  // so this keyboard had to pass 468 after ONE step and only 234 after
  // several — the arbitration's D-B kills exactly that path dependence, and
  // these numbers are what distinguish the two verdicts.
  const h = zoomHarness({ baseVisible: 402 });
  h.api.initVisualViewportHandling();
  h.keyboardTo(402); // scale = 1 session, raw 300 ≥ 175.5 = 25%
  assert.equal(h.active(), true);
  assert.deepEqual(h.bottoms(), ["300px"]);
  // cmd+'-' / menu step to 0.75 while the keyboard stays open: THIS pass is
  // the scale step = NEW GEOMETRY — the session exits through the frozen exit
  // edge, pays 0, and carries nothing over.
  h.zoomOutPass(0.75, { keyboardOpen: true });
  assert.equal(h.active(), false, "the scale-change frame exits the session, it does not pause it");
  assert.equal(h.occlusion(), 0, "transition frames trigger no avoidance");
  assert.deepEqual(h.bottoms(), ["0px"], "bottom restored on the step itself");
  assert.equal(h.recovering(), false, "D-B: the step retires the OLD latch via the geometry unlock and books NO new one");
  // D-C: two consecutive counting ticks at the unchanged 0.75 geometry settle
  // the scale; the settle re-judges on the UNLOCKED 25% line — raw 400 ≥ 234
  // enters, paid in the NEW raw. A latch here would have demanded 468.
  h.tick();
  assert.equal(h.active(), false, "one counting tick is not a settle");
  h.tick();
  assert.equal(h.active(), true, "settled scale < 1 re-judges: the unlocked 25% line enters, not the 50%");
  assert.deepEqual(h.bottoms(), [(936 - 402 / 0.75) + "px"], "paid in the NEW raw — 400 ≥ 234 = 25%");
});

// --- 3. scale = 0.75 WITHOUT a keyboard (menu / cmd+'-' zoom) ----------------

test("D2-3 scale=0.75 keyboard-free: raw is 0 by arithmetic, the terminal never lifts and never re-writes", () => {
  const h = zoomHarness();
  h.api.initVisualViewportHandling();
  const before = h.hostWrites(); // the init's one baseline "0px"
  for (const scale of [0.95, 0.9, 0.8, 0.75]) {
    h.zoomOutPass(scale); // keyboard CLOSED: vv.height == clientHeight, raw 0
    assert.equal(h.occlusion(), 0, `scale ${scale}: raw 0 owes nothing`);
    assert.deepEqual(h.bottoms(), ["0px"]);
    assert.equal(h.active(), false);
    h.settlePass();
    h.tick();
    assert.equal(h.active(), false, `scale ${scale} settled, no keyboard: still nothing owed`);
    assert.deepEqual(h.bottoms(), ["0px"], "the terminal stays at its authored full height");
  }
  assert.equal(h.hostBottomRect(), 936 - SAFE_BOTTOM, "full height at rest, exactly like the 153-frame A0 segment");
  assert.equal(h.hostWrites(), before, "keyboard-free zoom steps write nothing new");
});

test("D2-3b scale=0.75 shallow overlay (measured raw 91.34 ≈ 9.8%): stays shut — the lines survive zoom", () => {
  const h = zoomHarness();
  h.api.initVisualViewportHandling();
  h.zoomOutPass(0.75);
  // Device evidence 0.75 segment: raw 91.34 (vv.height 844.65625 on
  // clientHeight 936). Under D2 the STATE is admitted to the §5 machine, and
  // §5 itself refuses it below the 25% line (234) — a fresh session needs
  // raw ≥ 234, this is 91.34.
  h.keyboardTo(844.65625);
  assert.equal(h.occlusion(), 0, "raw 91.34 < 234 = 25%: the line still refuses");
  assert.deepEqual(h.bottoms(), ["0px"]);
  assert.equal(h.active(), false);
  h.tick();
  assert.deepEqual(h.bottoms(), ["0px"]);
});

// --- 4. continuous scale transition: nothing fires until settle ---------------

test("D2-4 continuous zoom-out (748-wide base toward 0.5): every transition frame owes 0; the counted settle re-judges fresh", () => {
  // Base 748 wide × 1024 tall; a 428 keyboard is 41.8% of the layout — above
  // the 25% line. Every zoom step rescales the layout the way iOS does, so
  // every step is new geometry (exit + block + no new latch, D-B), and the
  // COUNTED settle re-judges from a rebuilt baseline — the stale pre-zoom
  // latch is retired by the frozen geometry-change unlock ("不得复用旧恢复锁"),
  // so the plain 25% line governs the settle frames.
  const h = zoomHarness({ baseLayout: 1024, baseWidth: 748, baseVisible: 1024 - 428 });
  h.api.initVisualViewportHandling();
  h.keyboardTo(1024 - 428); // scale = 1 session, raw 428 ≥ 256 = 25%
  assert.equal(h.active(), true);
  for (const scale of [0.9, 0.8, 0.75, 0.7, 0.6, 0.55, 0.5]) {
    h.zoomOutPass(scale, { keyboardOpen: true });
    assert.equal(h.active(), false, `scale ${scale}: transition frames never avoid`);
    assert.equal(h.occlusion(), 0, `scale ${scale}: the mid-zoom raw ${428 / scale} is NOT paid`);
    assert.deepEqual(h.bottoms(), ["0px"]);
    assert.equal(h.recovering(), false, `scale ${scale}: no step books a latch (D-B)`);
  }
  // The first pass at the UNCHANGED scale 0.5 was the last step itself — it
  // opened the candidate. D-C: the settle is TWO consecutive counting ticks
  // at that candidate; a plain resize never counts, and one tick is not a
  // settle. Then the fresh baseline judges on the plain 25% line —
  // raw = 428/0.5 = 856 ≥ 512 = 25% of 2048.
  h.tick();
  assert.equal(h.active(), false, "one counting tick at 0.5 is not a settle (N=2)");
  h.tick();
  assert.equal(h.active(), true, "settled at 0.5 the rebuilt baseline re-judges: 856 ≥ 512 enters");
  assert.equal(h.occlusion(), 856, "pays the settled raw exactly");
  assert.deepEqual(h.bottoms(), ["856px"]);
});

test("D2-4b a fresh session starting under a settled scale<1 enters at the plain 25% line", () => {
  const h = zoomHarness();
  h.api.initVisualViewportHandling();
  h.zoomOutPass(0.75); // zoom out with the keyboard closed
  h.keyboardTo(KB_VISIBLE / 0.75); // the keyboard opens under the SAME scale —
  h.tick();
  assert.equal(h.active(), false, "the keyboard-open pass re-opened the candidate; one tick stays pending");
  h.tick(); // …but D-C counts two stable ticks from there before the line speaks
  assert.equal(h.active(), true, "two unchanged ticks at 0.75: settled — raw 570.67 ≥ 234 enters");
  assert.deepEqual(h.bottoms(), [(936 - KB_VISIBLE / 0.75) + "px"]);
  // And the transition-frame block at a NEW scale is owed 0 again mid-zoom.
  h.zoomOutPass(0.6, { keyboardOpen: true });
  assert.equal(h.active(), false, "stepping to 0.6 mid-session is again new geometry: block + exit");
  assert.deepEqual(h.bottoms(), ["0px"]);
  assert.equal(h.recovering(), false, "D-B: the step exit books no new latch");
});

// --- 5. scale > 1 keeps the frozen opt-out -------------------------------------

test("D2-5 scale>1 + keyboard: opt-out held — bottom 0px, never enters, settled or not", () => {
  const h = zoomHarness();
  h.api.initVisualViewportHandling();
  h.zoomInPass(1.8, { visibleHeight: KB_VISIBLE / 1.8 });
  assert.equal(h.active(), false, "scale > 1 never enters, settled or not");
  assert.deepEqual(h.bottoms(), ["0px"]);
  h.tick();
  h.tick();
  assert.equal(h.active(), false, "settled scale > 1 still opts out — the conservative half is UNCHANGED");
  assert.deepEqual(h.bottoms(), ["0px"]);
  h.keyboardTo(180); // a deeper keyboard shortfall under zoom: still owed nothing
  assert.equal(h.occlusion(), 0, "while zoomed in no shortfall is paid");
  assert.deepEqual(h.bottoms(), ["0px"]);
});

test("D2-5b scale>1 exits an active scale=1 session through the frozen edge, authored bottoms restored", () => {
  const h = zoomHarness();
  h.api.initVisualViewportHandling();
  h.keyboardTo(KB_VISIBLE); // scale=1 session
  assert.equal(h.active(), true);
  h.zoomInPass(2, { visibleHeight: 137 });
  assert.equal(h.active(), false, "zoom-in ends the session in flight, exactly as the frozen baseline");
  assert.deepEqual(h.bottoms(), ["0px"]);
  assert.equal(h.controls.style.bottom, "20px", "opt-out restores the authored bottoms");
  assert.equal(h.statusBar.style.bottom, "0px");
});

// --- 6. a scale<1 session suddenly returns to scale>1, then back to 1 --------

test("D2-6 a settled scale=0.75 session jumps to scale>1: exits at once and latches (frozen); back at 1 the return frame pays 0 (D-D) and the counted settle re-judges — the frozen latch keeps the shallow keyboard shut until a click", () => {
  const h = zoomHarness();
  h.api.initVisualViewportHandling();
  h.zoomOutPass(0.75);
  h.keyboardTo(KB_VISIBLE / 0.75); // raw 570.67…
  h.tick();
  h.tick(); // …counted out by D-C, THEN a session exists under the zoom
  assert.equal(h.active(), true);
  // The menu jumps to 125% in one step. At scale>1 the iOS layout viewport
  // sits back at its scale-1 base (702) — the step is new geometry AND
  // zoomed-in. The scale>1 exit is a FROZEN exit: it keeps its recovery
  // latch exactly as before — D-B's no-latch exception covers ONLY the
  // scale<1 step and the trip back into the band, never a scale>1 pass.
  h.zoomInPass(1.5, { visibleHeight: KB_VISIBLE / 1.5 });
  assert.equal(h.active(), false, "scale>1 exits the zoom session immediately");
  assert.deepEqual(h.bottoms(), ["0px"], "and restores the terminal bottom on the same pass");
  assert.equal(h.recovering(), true, "the exit latched recovery — the frozen opt-out bookkeeping");
  h.tick();
  assert.equal(h.active(), false, "settled scale>1 keeps the opt-out (re-entry would owe the 50% line and zoom blocks it anyway)");
  // D-D: back at exactly scale = 1 the frame that RETURNS is the EXIT frame —
  // it pays 0, opens the counted candidate, and books no new latch; it does
  // not carry the pre-zoom verdict over onto the new raw.
  h.zoomInPass(1, { visibleHeight: 450 });
  assert.equal(h.active(), false, "the return frame pays 0 and does not re-enter on the pass home");
  assert.deepEqual(h.bottoms(), ["0px"]);
  // Two counting ticks settle the return; the frozen scale>1-exit latch still
  // survives at the unchanged geometry (D-B keeps every other exit's latch
  // verbatim) and refuses the shallow raw 252 < 351 = 50% — only the
  // existing click / focusin unlocks re-open the 25% line.
  h.tick();
  h.tick();
  assert.equal(h.active(), false, "back at 1 with raw 252 < 351 = 50%: the recovery latch still refuses");
  assert.deepEqual(h.bottoms(), ["0px"]);
  h.click(); // the frozen click unlock — unchanged
  h.keyboardTo(450);
  assert.equal(h.active(), true, "unlocked, 252 ≥ 175.5 = 25%: exactly the pre-D2 scale=1 rule");
  assert.deepEqual(h.bottoms(), ["252px"]);
});

// --- 7. the ownership boundary does not widen -----------------------------------

test("D2-7 the frozen GENERIC editable predicate is the whole host gate at scale<1 — non-editable focus never avoids; an INPUT / the quick-keys paste TEXTAREA passing it is an HO-3 / R2 known deviation, NOT a PTY-host proof (D-A plan B)", () => {
  // D-A (arbitration commit 6e8a006, plan B, wording corrected by the
  // re-review / C12): until Phase 3 ships the host split, the ONLY focus input
  // stays the frozen document.activeElement + TEXTAREA / INPUT /
  // contenteditable predicate. No helper class, no ancestor chain, no explicit
  // host marker — so under scale < 1 an ordinary Web INPUT, and the quick-keys
  // PASTE textarea (#quick-keys-paste-textarea, a TEXTAREA whose focus belongs to
  // the Web layer under HO-3), can open the terminal avoidance through that
  // generic predicate. Every one of those hosts is the R2 KNOWN DEVIATION /
  // Phase 3 dependency registered by the arbitration: passing the generic
  // predicate is NOT a proof of PTY-host admission and must never be cited as
  // one, and it does not turn HO-3 into HO-2. What this case DOES prove: the
  // predicate still refuses non-editables, and the D2 edit gained no host
  // eligibility of its own.
  const h = zoomHarness({ focus: { tagName: "BODY" } });
  h.api.initVisualViewportHandling();
  h.zoomOutPass(0.75);
  h.keyboardTo(KB_VISIBLE / 0.75);
  h.tick();
  h.tick(); // the SCALE settles even with BODY focus — the settle machine is geometry-only…
  assert.equal(h.active(), false, "…but a settled scale cannot substitute for the editable focus: BODY never opens the session");
  assert.equal(h.occlusion(), 0);
  assert.deepEqual(h.bottoms(), ["0px"]);
  assert.equal(h.recovering(), false, "a focus-loss-shaped block at the baseline pages owes no latch either");
  // An INPUT entering through the SAME frozen predicate opens the session on
  // the focus frame itself — the scale is already settled, so nothing else is
  // missing. That is the generic predicate doing exactly what the frozen code
  // always did: an HO-3 Web host owning the keyboard geometry, i.e. the
  // registered R2 leak. Narrowing it to the PTY host is Phase 3's job, not
  // this guard's, and this pass is not a D2 admission proof.
  h.document.activeElement = { tagName: "INPUT" };
  h.focusin();
  assert.equal(h.active(), true, "the frozen editable-focus path admits ANY editable host — an INPUT is the HO-3 / R2 known deviation by design, not a PTY admission (C12)");
  assert.deepEqual(h.bottoms(), [(936 - KB_VISIBLE / 0.75) + "px"]);
  // The quick-keys PASTE textarea is the same story in the product's own words:
  // it is a TEXTAREA in the Web layer, so the frozen predicate admits it too and
  // the terminal pays for a Web input's keyboard. Registered R2 leak, HO-3
  // ownership; never a PTY-host proof.
  h.keyboardTo(936); // close the keyboard, then let the Web-layer host focus
  h.document.activeElement = { tagName: "TEXTAREA", id: "quick-keys-paste-textarea" };
  h.focusin();
  h.keyboardTo(KB_VISIBLE / 0.75);
  assert.equal(h.active(), true, "the quick-keys paste TEXTAREA passes the generic predicate too — HO-3 / R2 known deviation (Phase 3 dependency), not a PTY-host proof");
  assert.deepEqual(h.bottoms(), [(936 - KB_VISIBLE / 0.75) + "px"]);
});

test("D2-7b structure: the guard's focus input is the frozen activeElement read — no host-ownership machine may appear (red line, D-A plan B)", () => {
  const apply = stripComments(sliceBlock(inlineScript(indexSource), "function applyVisualViewportOcclusion"));
  assert.equal((apply.match(/document\.activeElement/g) || []).length, 1,
    "the focused element stays the SINGLE focus input of the judgment — D2 added no second ownership path");
  assert.match(apply, /focus\.tagName === "TEXTAREA" \|\| focus\.tagName === "INPUT" \|\| focus\.isContentEditable/,
    "the editable test survives character for character");
  for (const banned of [/parentElement/, /parentNode/, /\.closest\(/, /\.classList/, /getAttribute/, /className/]) {
    assert.equal(banned.test(apply), false, `the D2 guard must not consult ${banned} — no new host-ownership machine (PLAN §4 D2 rule 2)`);
  }
});

// --- 8. the raw floor and the pinch-shape boundaries --------------------------

test("D2-8 raw inside (0, 1] never enters — the frozen raw > 1 floor survives the settled scale<1 half (arbitration 直接编码待修项②: raw∈(0,1] 负例)", () => {
  // A settled scale < 1 hands the FRAME to §5; §5 still owes NOTHING until
  // raw EXCEEDS 1 — a sub-pixel shortfall is not real occlusion, and the
  // frozen floor must not creep to raw >= 1 or >= 0 because scale<1 entered
  // the machine. Boundary values, in order.
  // Clarification (arbitration re-review ④ / C11): ONLY the first boundary
  // needs the counted settle. The keyboard-open pass at raw 0.5 MOVES the raw
  // while the candidate is PENDING, so it re-opens the candidate and the two
  // ticks below are a real count. Once those two ticks are in, the scale is
  // SETTLED and SATURATED, and a raw move no longer re-opens it (D-C: "已
  // settled 后单纯键盘 raw 开合不再重置 scale settle") — the ticks after raw 1
  // and raw 2 are IDEMPOTENT re-checks of an already settled scale, not three
  // separate settles. The mirrors below say so instead of a comment hoping.
  const h = zoomHarness();
  h.api.initVisualViewportHandling();
  h.zoomOutPass(0.75);
  h.keyboardTo(936 - 0.5); // raw 0.5 — re-opens the candidate (the raw moved, pending)
  assert.equal(h.api.settleState().ticked, 0, "raw 0.5 broke the pending candidate — the count restarts here, and only here");
  h.tick();
  assert.equal(h.api.settleState().ticked, 1, "the first counting tick at raw 0.5");
  h.tick(); // settled — and STILL nothing owed
  assert.equal(h.api.settleState().ticked, 2, "the initial raw 0.5 candidate needed the FULL two ticks (C11)");
  assert.equal(h.active(), false, "raw 0.5 is not real occlusion: a settled scale never bypasses raw > 1");
  assert.equal(h.occlusion(), 0);
  assert.deepEqual(h.bottoms(), ["0px"]);
  h.keyboardTo(936 - 1); // raw exactly 1 — the boundary itself stays shut
  assert.equal(h.api.settleState().ticked, 2, "raw 1 did NOT re-open the settled candidate — the count stays saturated (C11)");
  h.tick();
  h.tick();
  assert.equal(h.active(), false, "raw == 1 fails raw > 1: the boundary never opens");
  assert.deepEqual(h.bottoms(), ["0px"]);
  h.keyboardTo(936 - 2); // raw 2 > 1 passes the FLOOR, but a fresh session still owes the 25% line
  assert.equal(h.api.settleState().ticked, 2, "and raw 2 neither re-opens nor re-counts the settled scale (C11)");
  h.tick();
  h.tick();
  assert.equal(h.active(), false, "raw > 1 does not bypass the 25% line — 2 < 234 stays shut");
  assert.deepEqual(h.bottoms(), ["0px"]);
});

test("D2-9 pinch model — layout / vv.width UNCHANGED, only vv.scale moves: the step is still NEW GEOMETRY (the sameGeometry scale term works)", () => {
  // The scale term added inside scale<1 is the ONLY thing that can make a
  // scale step a geometry change when the layout viewport does not visibly
  // move: layoutHeight 702 and vv.width 1194 stay identical through the whole
  // case, so if the term were missing, sameGeometry would be TRUE across the
  // step and the frozen stale latch would survive into the settle — where the
  // 50% line (raw 300 < 351) would keep the terminal shut under the keyboard.
  // A REAL stale latch is planted first (a same-geometry decrease exit, the
  // frozen latching edge), then the scale-only step must retire it.
  const h = zoomHarness();
  h.api.initVisualViewportHandling();
  h.keyboardTo(402); // raw 300 ≥ 175.5 = 25%: fresh session enters
  assert.equal(h.active(), true);
  assert.deepEqual(h.bottoms(), ["300px"]);
  h.keyboardTo(602); // raw 100 < 299 — same-geometry decrease: frozen EXIT + LATCH
  assert.equal(h.active(), false);
  assert.equal(h.recovering(), true, "the frozen same-geometry decrease exit keeps its latch (D-B: this exit is NOT exempt)");
  assert.deepEqual(h.bottoms(), ["0px"]);
  // The scale-only step to 0.75 — layout 702 and vv.width 1194 UNCHANGED:
  h.vv.scale = 0.75;
  h.vv.height = 402; // raw 300 again, at the unchanged layout
  h.vv.dispatch("resize");
  assert.equal(h.active(), false, "the unsettled step exits and pays 0");
  assert.deepEqual(h.bottoms(), ["0px"]);
  assert.equal(h.recovering(), false, "SAME layout / same width, scale only: ONLY the scale term can have made this new geometry and retired the stale latch");
  h.tick();
  assert.equal(h.active(), false, "one tick is not a settle");
  h.tick();
  assert.equal(h.active(), true, "settled: the UNLOCKED 25% line admits raw 300 ≥ 175.5 — a surviving latch would have demanded 351 and refused");
  assert.deepEqual(h.bottoms(), ["300px"]);
});

// --- 9. the counted D-C settle, executed ----------------------------------------

test("D2-10 only the 500ms tick counts: resize and scroll passes at stable geometry never advance the settle", () => {
  const h = zoomHarness();
  h.api.initVisualViewportHandling();
  h.zoomOutPass(0.75);
  h.keyboardTo(KB_VISIBLE / 0.75); // the candidate (re-)opens on this pass
  h.settlePass();
  h.settlePass();
  h.settlePass(); // three stable resize passes…
  assert.equal(h.active(), false, "event passes may keep a candidate alive, but three resizes never count it (D-C)");
  h.vv.dispatch("scroll"); // the scroll anchor runs the judgment too…
  assert.deepEqual(h.bottoms(), ["0px"], "…and pays nothing while the candidate is pending");
  h.tick();
  assert.equal(h.active(), false, "tick #1: ONE counting tick, still pending");
  h.tick();
  assert.equal(h.active(), true, "tick #2 settles: the two consecutive COUNTING ticks are the settle, nothing else");
  assert.deepEqual(h.bottoms(), [(936 - KB_VISIBLE / 0.75) + "px"]);
});

test("D2-11 after the settle the count SATURATES — identical ticks are idempotent, no re-exit, no re-announcement", () => {
  const h = zoomHarness();
  h.api.initVisualViewportHandling();
  h.zoomOutPass(0.75);
  h.keyboardTo(KB_VISIBLE / 0.75);
  h.tick();
  h.tick();
  assert.equal(h.active(), true);
  const writes = h.hostWrites();
  const bottom = h.bottoms();
  for (let i = 0; i < 5; i++) h.tick(); // five more identical settled ticks…
  h.settlePass();
  assert.equal(h.active(), true, "the settled verdict holds — saturation neither re-exits nor restarts a candidate (D-C)");
  assert.deepEqual(h.bottoms(), bottom, "the same paid raw, unchanged");
  assert.equal(h.occlusion(), Math.max(0, 936 - KB_VISIBLE / 0.75));
  assert.equal(h.hostWrites(), writes, "the guarded writes stay silent — the settle is announced once, not every 500ms");
});

test("D2-12 a raw change mid-candidate breaks the candidate — the full two ticks start over", () => {
  const h = zoomHarness();
  h.api.initVisualViewportHandling();
  h.zoomOutPass(0.75);
  h.keyboardTo(KB_VISIBLE / 0.75); // candidate at raw 570.67
  h.tick(); // tick #1 of the would-be settle
  h.keyboardTo(500); // the keyboard MOVES at the same scale — raw 436 breaks the candidate
  h.tick();
  assert.equal(h.active(), false, "post-break tick #1: the count restarted, still pending");
  h.tick();
  assert.equal(h.active(), true, "a full two ticks at the NEW raw settle the candidate");
  assert.deepEqual(h.bottoms(), [(936 - 500) + "px"], "paid in the new raw 436 ≥ 234");
});

test("D2-13 slow drift past ε breaks the candidate against the BASELINE even when every ADJACENT step sits inside ε", () => {
  const h = zoomHarness();
  h.api.initVisualViewportHandling();
  h.zoomOutPass(0.75);
  h.keyboardTo(KB_VISIBLE / 0.75); // candidate opened at 0.75 / raw 570.67
  h.tick(); // counting tick #1 at exactly 0.75
  assert.equal(h.active(), false);
  // A slow crawl: each ADJACENT reading sits inside ε, the ACCUMULATED one
  // does not — the baseline is what catches it (D-C).
  h.vv.scale = 0.7506; // +0.0006 — inside ε of both neighbours: pending survives
  h.vv.dispatch("resize");
  h.vv.scale = 0.7512; // adjacent +0.0006 (inside ε), baseline +0.0012 (past ε)
  h.vv.dispatch("resize");
  h.tick();
  assert.equal(h.active(), false, "accumulated drift past ε broke the candidate — the count restarted at 0.7512, one tick away from settled");
  h.tick();
  assert.equal(h.active(), true, "two counting ticks at the RE-BUILT baseline settle it");
  assert.deepEqual(h.bottoms(), [(936 - KB_VISIBLE / 0.75) + "px"], "judged fresh at 0.7512 — raw unchanged, the 25% line unchanged");
});

test("D2-14 hidden: the interval refuses to run the judgment there — a background tab accumulates NOTHING", () => {
  const h = zoomHarness();
  h.api.initVisualViewportHandling();
  h.zoomOutPass(0.75);
  h.keyboardTo(KB_VISIBLE / 0.75); // candidate re-opened here
  h.document.hidden = true;
  h.tick();
  h.tick();
  h.tick(); // three simulated 500ms passes…
  assert.equal(h.active(), false, "document.hidden: the interval skips the judgment — nothing counts, nothing enters");
  assert.deepEqual(h.bottoms(), ["0px"]);
  h.document.hidden = false;
  h.tick();
  assert.equal(h.active(), false, "counting restarts from ZERO in the foreground — the hidden passes banked nothing");
  h.tick();
  assert.equal(h.active(), true, "two FOREGROUND counting ticks settle it");
  assert.deepEqual(h.bottoms(), [(936 - KB_VISIBLE / 0.75) + "px"]);
});

test("D2-15 once SETTLED, keyboard raw opening and closing does NOT reset the scale settle — §5 does its own job in between", () => {
  const h = zoomHarness();
  h.api.initVisualViewportHandling();
  h.zoomOutPass(0.75);
  h.keyboardTo(KB_VISIBLE / 0.75);
  h.tick();
  h.tick();
  assert.equal(h.active(), true, "settled and entered");
  h.keyboardTo(936); // keyboard CLOSES at the unchanged scale: raw 0 — frozen §5 exits + latches; that is the KEYBOARD machine, not the scale settle
  assert.equal(h.active(), false);
  assert.equal(h.recovering(), true, "the frozen raw <= 1 exit keeps its latch (D-B keeps this exit verbatim)");
  h.keyboardTo(KB_VISIBLE / 0.75); // reopen past the 50% recovering line…
  assert.equal(h.active(), true, "…on the SAME pass, with no re-count: the scale stayed settled through the raw open/close (D-C)");
  assert.deepEqual(h.bottoms(), [(936 - KB_VISIBLE / 0.75) + "px"]);
  h.tick();
  assert.equal(h.active(), true, "and stays in");
});

// --- 10. F14 round trips and step-shape independence (D-B / D-D) ------------

test("D2-16 F14 deep-keyboard round trip 1 → 0.75 → 1: BOTH step frames pay 0, the return settles to the UNLOCKED 25% line", () => {
  // The arbitration's F14 MUST-PROVE: not one frame of new raw may be paid
  // on the frame home. The deep-enough band (raw ≈ 42.7% of the layout)
  // distinguishes the verdicts: entering needs 234 (unlocked) vs 468 (a
  // latch a buggy step would have booked).
  const h = zoomHarness({ baseVisible: 402 });
  h.api.initVisualViewportHandling();
  h.keyboardTo(402); // raw 300 ≥ 175.5 at scale = 1
  assert.equal(h.active(), true);
  h.zoomOutPass(0.75, { keyboardOpen: true });
  assert.equal(h.active(), false, "out-bound step frame: exit + pay 0");
  assert.deepEqual(h.bottoms(), ["0px"]);
  assert.equal(h.recovering(), false, "no new latch on the step out (D-B)");
  h.tick();
  h.tick();
  assert.equal(h.active(), true, "settled at 0.75: raw 400 ≥ 234 = 25% of 936");
  assert.deepEqual(h.bottoms(), ["400px"]);
  // The trip BACK — D-D: this frame pays 0 and re-settles; the session may
  // NOT carry the 0.75 verdict over onto the return frame's new raw.
  h.zoomInPass(1); // scale = 1, layout back at the 702 base, keyboard band 402
  assert.equal(h.active(), false, "the return frame is an exit frame");
  assert.deepEqual(h.bottoms(), ["0px"], "the return frame pays 0 — no raw 300 rides the step home");
  assert.equal(h.recovering(), false, "D-B / D-D: the return exit books NO new latch");
  h.tick();
  assert.equal(h.active(), false, "one in-band tick is not a settle (D-C)");
  h.tick();
  assert.equal(h.active(), true, "settled back in the band, the UNLOCKED 25% line re-judges: 300 ≥ 175.5 enters (a latch would have demanded 351)");
  assert.deepEqual(h.bottoms(), ["300px"]);
});

test("D2-17 single-step and multi-step zoom-out reach the SAME settle verdict — the verdict does not drift with Safari's step count (D-B)", () => {
  const mk = () => {
    const h = zoomHarness({ baseVisible: 402 });
    h.api.initVisualViewportHandling();
    h.keyboardTo(402); // live session at scale = 1, raw 300
    return h;
  };
  const one = mk();
  one.zoomOutPass(0.75, { keyboardOpen: true });
  assert.deepEqual(one.bottoms(), ["0px"], "the single step pays 0");
  assert.equal(one.recovering(), false, "and books no latch");
  one.tick();
  one.tick();
  const many = mk();
  for (const s of [0.9, 0.85, 0.8, 0.75]) {
    many.zoomOutPass(s, { keyboardOpen: true });
    assert.deepEqual(many.bottoms(), ["0px"], `every intermediate step at ${s} pays 0`);
    assert.equal(many.recovering(), false, `no step at ${s} books a latch — D-B kills the step-count path dependence`);
  }
  many.tick();
  many.tick();
  assert.equal(one.active(), true, "single step: settled, enters");
  assert.equal(many.active(), true, "multi step: settled, enters");
  assert.deepEqual(one.bottoms(), many.bottoms(), "identical settle-frame verdicts at the same final scale — the unlocked 25% line, raw 400 ≥ 234");
  assert.equal(one.occlusion(), many.occlusion());
});

// --- 11. the frozen band — a regression pull (D-D: 带内归冻结容差) -----------

test("D2-18 jitter INSIDE the frozen ±0.01 band is the frozen machine, untouched — the settle never speaks there", () => {
  const h = zoomHarness();
  h.api.initVisualViewportHandling();
  h.vv.scale = 1.005; // INSIDE the frozen ±0.01 tolerance — NOT zoomed
  h.keyboardTo(KB_VISIBLE); // raw 428 ≥ 25%
  assert.equal(h.active(), true, "in-band scale is not zoom: the frozen entry happens as always");
  assert.deepEqual(h.bottoms(), ["428px"]);
  h.keyboardTo(555); // raw 147 < 427 — same-geometry decrease: frozen exit + latch
  assert.equal(h.active(), false);
  assert.deepEqual(h.bottoms(), ["0px"]);
  h.keyboardTo(KB_VISIBLE); // 428 ≥ 351 = 50%: frozen recovering re-entry, in-band
  assert.equal(h.active(), true);
  h.vv.scale = 0.995; // drift to the other edge of the band at unchanged geometry
  h.vv.dispatch("resize");
  assert.equal(h.active(), true, "in-band jitter never exits and never consults the settle machine — zoomed is false, scaleBelowOne is false");
  assert.equal(h.occlusion(), 428, "the held session keeps paying raw in full through the jitter");
  h.vv.scale = 1.02; // out of the band upward: the frozen scale>1 opt-out takes over, verbatim
  h.vv.dispatch("resize");
  assert.equal(h.active(), false, "crossing out of the band upward is the frozen zoom exit");
  assert.deepEqual(h.bottoms(), ["0px"]);
  assert.equal(h.recovering(), true, "the frozen scale>1 exit keeps its latch — no D2 exemption reaches scale>1");
});

// --- 12. the container-absent window (D-E / contract rule 6, re-review C02) ----

test("D2-19 the container-absent early return syncs the scale history and drops the candidate — the first frame back is NOT a fake zoomReturnFrame (D-E)", () => {
  // D-E: `!container` is still a PASS that happened. If it returns without
  // touching the D2 scale memory, a scale step taken WHILE the container was
  // gone meets the stale previousTerminalScale = 0.75 on the first frame back
  // and is misread as a return trip: the frame exits, pays 0 and banks two more
  // suppressed ticks (the fake "wait for settle"). The fix is exactly two kinds
  // of write in that branch — the current scale into the history, and zero into
  // the six scaleSettle* fields — and nothing else: the frozen active /
  // recovering flags and the raw / layout / VV-width history stay untouched.
  const h = zoomHarness();
  h.api.initVisualViewportHandling();
  h.zoomOutPass(0.75);
  h.tick();
  assert.equal(h.api.settleState().candidate, 0.75, "a candidate is in flight when the container disappears");
  assert.equal(h.api.settleState().ticked, 1, "one counting tick is banked on it");
  assert.equal(h.api.previousScale(), 0.75, "the scale history reads 0.75 on the way in");
  const writesBefore = h.hostWrites();
  h.containerGone();
  assert.equal(h.applyDirect(), false, "the early return still answers false");
  // Inside the window Safari brings the scale home (and the layout with it).
  h.vv.scale = 1;
  h.root.clientHeight = L0;
  h.vv.width = W0;
  h.vv.height = 402; // raw 300 at the scale-1 layout
  assert.equal(h.applyDirect(), false, "with no container there is nothing to write");
  assert.deepEqual(h.api.settleState(), { candidate: 0, baseline: 0, ticked: 0, layoutHeight: 0, vvWidth: 0, raw: 0 },
    "D-E: the early return clears the whole candidate chain instead of banking the 0.75 count");
  assert.equal(h.api.previousScale(), 1, "D-E: the history carries the CURRENT scale, not the stale 0.75");
  assert.equal(h.hostWrites(), writesBefore, "the absent-container window writes nothing");
  h.containerBack();
  h.vv.dispatch("resize");
  assert.equal(h.active(), true, "the first frame back is an ordinary in-band pass — no fake return trip, no suppressed ticks");
  assert.deepEqual(h.bottoms(), ["300px"], "raw 300 ≥ 175.5 = 25% pays on the FIRST frame back");
  assert.equal(h.recovering(), false, "and the window booked no latch — the frozen flags were not the fix");
});

// --- 13. a frozen exit reason outranks the step exemption (D-B narrow, C04) -----

test("D2-20 a scale step whose frame ALSO has raw <= 1 keeps the frozen latch; the settle never clears it; only the 50% line re-enters (D-B narrow / C04)", () => {
  // scaleStepNoLatchExit is guarded by `raw > 1 && focusEditable` for exactly
  // this frame: the step did not END the session, the closing keyboard did, and
  // that is a FROZEN exit reason which keeps its latch. The counted settle then
  // answers the scale question only — it never unlocks the keyboard machine.
  const h = zoomHarness({ baseVisible: 402 });
  h.api.initVisualViewportHandling();
  h.keyboardTo(402); // scale = 1 session, raw 300 ≥ 175.5 = 25%
  assert.equal(h.active(), true);
  h.zoomOutPass(0.75); // the step AND raw 0 in the same frame (keyboard closed)
  assert.equal(h.active(), false, "the step frame exits");
  assert.deepEqual(h.bottoms(), ["0px"], "and pays 0");
  assert.equal(h.recovering(), true, "raw <= 1 is a FROZEN exit reason: the latch stays, the D-B exemption does not reach it");
  h.tick();
  h.tick();
  assert.equal(h.api.settleState().ticked, 2, "the scale settled");
  assert.equal(h.recovering(), true, "the settle does not clear a latch it did not create (D-B / contract rule 2)");
  h.keyboardTo(936 - 300); // raw 300 ≥ 234 = 25% but < 468 = 50%
  assert.equal(h.active(), false, "raw 300 sits under the locked 50% line: the latched re-entry line governs");
  assert.deepEqual(h.bottoms(), ["0px"]);
  h.tick();
  h.tick();
  assert.equal(h.active(), false, "settled or not, unreleased the 50% line still refuses raw 300");
  h.keyboardTo(936 - 500); // raw 500 ≥ 468 = 50%
  assert.equal(h.active(), true, "a fresh keyboard past 50% re-enters — the frozen line, unchanged");
  assert.deepEqual(h.bottoms(), ["500px"]);
});

test("D2-21 a scale step whose frame ALSO loses focus keeps the frozen latch; after the settle the latched 50% line refuses raw 400 until a click (D-B narrow / C04)", () => {
  // The same priority test with the OTHER frozen reason: the keyboard band is
  // unchanged (raw 400 > 1) but focus left the editable on the step frame.
  // `!focusEditable` is the exit, so the exit keeps its latch even though the
  // scale stepped in the same frame.
  const h = zoomHarness({ baseVisible: 402 });
  h.api.initVisualViewportHandling();
  h.keyboardTo(402);
  assert.equal(h.active(), true);
  h.document.activeElement = { tagName: "BODY" };
  h.zoomOutPass(0.75, { keyboardOpen: true }); // step + focus loss, raw 400
  assert.equal(h.active(), false);
  assert.deepEqual(h.bottoms(), ["0px"]);
  assert.equal(h.recovering(), true, "focus loss is a frozen exit reason — it keeps its latch over the step exemption");
  h.document.activeElement = { tagName: "TEXTAREA" }; // the xterm helper back
  h.tick();
  h.tick();
  assert.equal(h.api.settleState().ticked, 2, "the scale question settled");
  assert.equal(h.recovering(), true, "the settle never unlocks the keyboard latch (C04)");
  assert.equal(h.active(), false, "raw 400 ≥ 234 = 25% but < 468 = 50%: still shut behind the latch");
  assert.deepEqual(h.bottoms(), ["0px"]);
  h.click(); // the frozen unlock: clears the latch, runs no judgment of its own
  assert.equal(h.recovering(), false, "the click unlocked");
  assert.equal(h.active(), false, "the click handler only unlocks — the next judgment pass decides");
  h.tick();
  assert.equal(h.active(), true, "unlocked, the 25% line admits raw 400 ≥ 234");
  assert.deepEqual(h.bottoms(), ["400px"]);
});

// --- 14. the scale>1 domain runs no scale machine (D-C re-review, C05) ----------

test("D2-22 entering scale>1 DROPS an in-flight candidate and banks nothing there; the trip home counts a FULL two ticks (D-C / C05)", () => {
  // scale > 1 is the conservative opt-out domain: every frame there already
  // owes nothing through zoomOptOut, so the settle machine has no business
  // opening, banking or counting a candidate in it. A candidate inherited from
  // scale < 1 is cleared on entry to the domain, which is what makes the trip
  // back to 1 count two fresh ticks instead of resuming a count earned while the
  // zoom owed nothing.
  const h = zoomHarness({ baseVisible: 402 });
  h.api.initVisualViewportHandling();
  h.keyboardTo(402);
  assert.equal(h.active(), true);
  h.zoomOutPass(0.75, { keyboardOpen: true });
  h.tick();
  assert.equal(h.api.settleState().candidate, 0.75, "a scale<1 candidate with one tick banked");
  assert.equal(h.api.settleState().ticked, 1);
  h.zoomInPass(1.5, { visibleHeight: 402 / 1.5 }); // straight into scale > 1
  assert.deepEqual(h.api.settleState(), { candidate: 0, baseline: 0, ticked: 0, layoutHeight: 0, vvWidth: 0, raw: 0 },
    "the scale>1 frame CLEARS the candidate instead of re-basing it (C05 — no banking in the opt-out domain)");
  assert.equal(h.active(), false, "scale > 1 never enters");
  h.tick();
  assert.deepEqual(h.api.settleState(), { candidate: 0, baseline: 0, ticked: 0, layoutHeight: 0, vvWidth: 0, raw: 0 },
    "a tick inside scale > 1 neither opens nor advances anything");
  h.zoomInPass(1, { visibleHeight: 402 }); // the trip home: exit + pay 0 + a NEW candidate
  assert.equal(h.active(), false, "the return frame pays 0");
  assert.deepEqual(h.bottoms(), ["0px"]);
  assert.equal(h.api.settleState().candidate, 1, "a FRESH candidate at scale 1");
  assert.equal(h.api.settleState().ticked, 0, "starting from zero — the 0.75 count was dropped, not resumed");
  h.tick();
  assert.equal(h.active(), false, "one in-band tick is not a settle");
  h.tick();
  assert.equal(h.active(), true, "the FULL two ticks home settle it and the 25% line admits raw 300");
  assert.deepEqual(h.bottoms(), ["300px"]);
});

// --- 15. the settle memory after the settle, in binary64 (D-C, C06 / C07 / C08) -

// ε is the guard's own module constant, spelled once in index.html and mirrored
// here so these cases cannot drift from the shipped parameter.
const SETTLE_EPS = 0.001;
assert.ok(indexSource.includes("const SCALE_SETTLE_EPSILON = 0.001;"),
  "the guard still ships ε = 0.001 — the local constant below mirrors it, do not tune either");

/** The next representable binary64 value above x, built BITWISE (contract rule 3:
 *  a 1 ULP case must be constructed by bits, never written as a decimal guess. */
function nextUpBinary64(x) {
  const view = new DataView(new ArrayBuffer(8));
  view.setFloat64(0, x);
  let hi = view.getUint32(0);
  let lo = view.getUint32(4);
  if (lo === 0xFFFFFFFF) { hi = (hi + 1) >>> 0; lo = 0; } else { lo = (lo + 1) >>> 0; }
  view.setUint32(0, hi);
  view.setUint32(4, lo);
  return view.getFloat64(0);
}

test("D2-23 after the settle, ACCUMULATED sub-ε drift stays settled and a step past ε exits and rebuilds — the dual case (D-C / C06)", () => {
  const h = zoomHarness();
  h.api.initVisualViewportHandling();
  h.zoomOutPass(0.75);
  h.keyboardTo(KB_VISIBLE / 0.75);
  h.tick();
  h.tick();
  assert.equal(h.active(), true, "settled at 0.75 and paying raw");
  const saturated = h.api.settleState();
  // HALF ONE — accumulate INSIDE ε: still settled, no rebuild, no re-exit.
  const inside = Math.abs(0.7505 - 0.75);
  assert.ok(inside <= SETTLE_EPS, `binary64 actual difference 0.75 → 0.7505 is ${inside.toFixed(21)}, inside ε = ${SETTLE_EPS.toFixed(21)}`);
  h.vv.scale = 0.7505;
  h.vv.dispatch("resize");
  assert.equal(h.active(), true, "accumulated sub-ε drift keeps the settle — the candidate is NOT rebuilt");
  assert.deepEqual(h.api.settleState(), saturated, "same candidate, same baseline, same saturated count (C06 half one)");
  h.tick();
  assert.equal(h.active(), true, "and the tick there is idempotent");
  assert.deepEqual(h.api.settleState(), saturated, "saturated stays saturated — no re-announcement");
  // HALF TWO — the ACCUMULATED difference past ε: exit, rebuild, re-count.
  const beyond = Math.abs(0.751 - 0.75);
  assert.ok(beyond > SETTLE_EPS, `binary64 actual difference 0.75 → 0.751 is ${beyond.toFixed(21)}, PAST ε = ${SETTLE_EPS.toFixed(21)}`);
  h.vv.scale = 0.751;
  h.vv.dispatch("resize");
  assert.equal(h.active(), false, "accumulated drift past ε breaks the settle — the settled verdict is gone with it");
  assert.deepEqual(h.bottoms(), ["0px"], "and the terminal pays nothing until the rebuilt candidate counts out");
  assert.equal(h.api.settleState().candidate, 0.751, "the candidate rebuilt on the drifted scale");
  assert.equal(h.api.settleState().ticked, 0, "and the count restarts from zero");
  h.tick();
  assert.equal(h.active(), false, "one tick at the rebuilt baseline is not a settle");
  h.tick();
  assert.equal(h.active(), true, "two fresh ticks settle the new scale and the §5 line speaks again");
  assert.deepEqual(h.bottoms(), [(936 - KB_VISIBLE / 0.75) + "px"]);
});

test("D2-24 a sub-ε step DURING a candidate neither resets it nor counts it — the following two real ticks are what settle it (D-C / C07)", () => {
  const h = zoomHarness();
  h.api.initVisualViewportHandling();
  h.zoomOutPass(0.75);
  h.keyboardTo(KB_VISIBLE / 0.75); // the candidate opens here, ticked = 0
  assert.equal(h.api.settleState().ticked, 0);
  // A sub-ε step on an EVENT pass: inside ε of the adjacent reading AND of the
  // baseline, so it breaks nothing — but an event pass never counts.
  const step1 = Math.abs(0.7505 - 0.75);
  assert.ok(step1 <= SETTLE_EPS, `binary64 actual difference ${step1.toFixed(21)} is inside ε`);
  h.vv.scale = 0.7505;
  h.vv.dispatch("resize");
  assert.equal(h.api.settleState().baseline, 0.75, "the baseline stays the FIRST candidate reading — a sub-ε step does not re-base it (C07)");
  assert.equal(h.api.settleState().ticked, 0, "and the event pass neither counts nor resets");
  h.tick();
  assert.equal(h.api.settleState().ticked, 1, "tick #1 of the two");
  assert.equal(h.active(), false, "one tick is not a settle");
  const step2 = Math.abs(0.7509 - 0.75);
  assert.ok(step2 <= SETTLE_EPS, `binary64 actual difference ${step2.toFixed(21)} is still inside ε of the ORIGINAL baseline`);
  h.vv.scale = 0.7509;
  h.vv.dispatch("resize");
  assert.equal(h.api.settleState().ticked, 1, "the second sub-ε step neither reset nor advanced the count");
  assert.equal(h.api.settleState().baseline, 0.75, "baseline untouched");
  assert.equal(h.active(), false, "nothing enters before the two real ticks");
  h.tick();
  assert.equal(h.api.settleState().ticked, 2, "the second REAL tick settles");
  assert.equal(h.active(), true, "settled inside ε of the original baseline, the §5 line admits the raw");
});

test("D2-25 the ε boundary is binary64, not decimal — 0.75 → 0.751 is PAST ε while 0.75 → 0.7509999999 is inside; each case records its ACTUAL difference (D-C / C08)", () => {
  // Contract rule 3: "十进制看似恰好 0.001 不保证差值等于 ε". Decimal says both
  // steps are "about 0.001"; the machine compares the two binary64 numbers it
  // actually holds, and they land on OPPOSITE sides of ε. The actual difference
  // is printed into every assertion so a future retune cannot hide behind prose.
  const pastActual = Math.abs(0.751 - 0.75);
  const insideActual = Math.abs(0.7509999999 - 0.75);
  assert.ok(pastActual > SETTLE_EPS, `0.75 → 0.751: actual ${pastActual.toFixed(21)} > ε ${SETTLE_EPS.toFixed(21)} — the decimal "exactly 0.001" reading would be WRONG`);
  assert.ok(insideActual <= SETTLE_EPS, `0.75 → 0.7509999999: actual ${insideActual.toFixed(21)} ≤ ε ${SETTLE_EPS.toFixed(21)} — inside, whatever the decimal digits suggest`);
  // PAST ε: the settled candidate breaks and the count restarts.
  const past = zoomHarness();
  past.api.initVisualViewportHandling();
  past.zoomOutPass(0.75);
  past.keyboardTo(KB_VISIBLE / 0.75);
  past.tick();
  past.tick();
  assert.equal(past.active(), true, "settled at 0.75 first");
  past.vv.scale = 0.751;
  past.vv.dispatch("resize");
  assert.equal(past.active(), false, `actual difference ${pastActual.toFixed(21)} is past ε, so the settle is gone — verdict exit, pay 0`);
  assert.deepEqual(past.bottoms(), ["0px"]);
  assert.equal(past.api.settleState().ticked, 0, `rebuilt on 0.751 with a zero count (actual difference ${pastActual.toFixed(21)})`);
  past.tick();
  past.tick();
  assert.equal(past.active(), true, "the rebuilt candidate counts out and the 25% line admits again");
  // INSIDE ε: the settled candidate survives untouched.
  const kept = zoomHarness();
  kept.api.initVisualViewportHandling();
  kept.zoomOutPass(0.75);
  kept.keyboardTo(KB_VISIBLE / 0.75);
  kept.tick();
  kept.tick();
  assert.equal(kept.active(), true, "settled at 0.75 first");
  kept.vv.scale = 0.7509999999;
  kept.vv.dispatch("resize");
  assert.equal(kept.active(), true, `actual difference ${insideActual.toFixed(21)} is inside ε — the settled scale holds and keeps paying raw`);
  assert.equal(kept.api.settleState().baseline, 0.75, "no rebuild: the baseline is still the first candidate reading");
  assert.equal(kept.api.settleState().ticked, 2, "and the count stays saturated");
});

test("D2-26 ONE ULP off the settled baseline is inside ε by construction — bitwise neighbours, not decimal words (D-C / C08)", () => {
  const baseline = 0.75;
  const ulp = nextUpBinary64(baseline);
  const diff = Math.abs(ulp - baseline);
  assert.notEqual(ulp, baseline, "the bitwise neighbour really is a different binary64 value");
  assert.ok(diff < SETTLE_EPS, `1 ULP above 0.75 is ${ulp.toFixed(17)}; its ACTUAL binary64 difference is ${diff.toExponential(6)} — nine orders inside ε = ${SETTLE_EPS.toFixed(21)}, so it can never break a settle`);
  const h = zoomHarness();
  h.api.initVisualViewportHandling();
  h.zoomOutPass(0.75);
  h.keyboardTo(KB_VISIBLE / 0.75);
  h.tick();
  h.tick();
  assert.equal(h.active(), true, "settled at 0.75");
  h.vv.scale = ulp;
  h.vv.dispatch("resize");
  assert.equal(h.active(), true, `a 1 ULP drift (actual ${diff.toExponential(6)}) never exits a settled scale`);
  assert.equal(h.api.settleState().baseline, baseline, "the baseline is not re-based by a ULP");
  assert.equal(h.api.settleState().ticked, 2, "and the saturated count stays put — no re-settle, no re-announcement");
  assert.deepEqual(h.bottoms(), [(936 - KB_VISIBLE / 0.75) + "px"], "still paying the same raw, one ulp of scale later");
});

// --- 16. the candidate's own geometry terms (D-C ②, C09) -------------------------

test("D2-27 a layoutHeight change ALONE breaks the candidate — raw and every other candidate field held identical (D-C ② / C09)", () => {
  // raw = clientHeight − vv.height, so moving clientHeight alone would move raw
  // too and prove nothing about which term fired. The isolation: raise
  // clientHeight by EXACTLY 1 and vv.height by EXACTLY 1 as well, so raw stays
  // the bit-identical binary64 number it already was (936 − 365.3(3) =
  // 937 − 366.3(3) = 570.6(6)), vv.width and scale stay put, and the ONLY
  // candidate input that moved is layoutHeight.
  const h = zoomHarness();
  h.api.initVisualViewportHandling();
  h.zoomOutPass(0.75);
  h.keyboardTo(KB_VISIBLE / 0.75); // raw 570.6(6) on the 936 layout
  h.tick();
  assert.equal(h.api.settleState().ticked, 1, "one tick banked");
  const before = h.api.settleState();
  h.root.clientHeight = 937;
  h.vv.height = KB_VISIBLE / 0.75 + 1; // the band +1px too — raw unchanged, bit for bit
  h.vv.dispatch("resize");
  const after = h.api.settleState();
  assert.equal(after.raw, before.raw, `raw held identical (${before.raw}) so only the layout term can have fired`);
  assert.equal(after.vvWidth, before.vvWidth, "vv.width unchanged");
  assert.equal(after.candidate, 0.75, "same scale, same candidate value");
  assert.equal(after.layoutHeight, 937, "layoutHeight is the single field that moved");
  assert.equal(after.ticked, 0, "and it broke the candidate — the count restarts from zero (C09)");
  assert.equal(h.active(), false, "nothing enters on the broken frame");
  h.tick();
  assert.equal(h.active(), false, "one tick at the rebuilt baseline is not a settle");
  h.tick();
  assert.equal(h.active(), true, "the full two ticks settle it again");
});

test("D2-28 a vv.width change ALONE breaks the candidate — layoutHeight, raw, scale and the rest held identical (D-C ② / C09)", () => {
  // vv.width is the frozen sameGeometry partner and an independent candidate
  // term: it needs no layout height to move, so this frame changes NOTHING but
  // vv.width (raw = clientHeight − vv.height is untouched arithmetic).
  const h = zoomHarness();
  h.api.initVisualViewportHandling();
  h.zoomOutPass(0.75);
  h.keyboardTo(KB_VISIBLE / 0.75);
  h.tick();
  assert.equal(h.api.settleState().ticked, 1, "one tick banked");
  const before = h.api.settleState();
  h.vv.width = 1592.5;
  h.vv.dispatch("resize");
  const after = h.api.settleState();
  assert.equal(after.layoutHeight, before.layoutHeight, `layoutHeight held identical (${before.layoutHeight})`);
  assert.equal(after.raw, before.raw, `raw held identical (${before.raw}) — vv.width does not enter it`);
  assert.equal(after.candidate, 0.75, "same scale");
  assert.equal(after.vvWidth, 1592.5, "vv.width is the single field that moved");
  assert.equal(after.ticked, 0, "and it broke the candidate — the count restarts from zero (C09)");
  assert.equal(h.active(), false, "nothing enters on the broken frame");
  h.tick();
  assert.equal(h.active(), false, "one tick at the rebuilt baseline is not a settle");
  h.tick();
  assert.equal(h.active(), true, "the full two ticks settle it again");
});

// --- 17. a pre-existing latch and the 0.75 → 1 return (D-D / C03) ---------------

test("D2-29 a PRE-EXISTING latch and a 0.75 → 1 return with layout / vv.width UNCHANGED: the step clears the latch, the return pays 0, the settle re-enters on the 25% line (D-D / C03)", () => {
  // The scale-only shape (the D2-9 pinch model: the layout viewport and vv.width
  // never move), with a REAL frozen latch planted first — a same-geometry decrease
  // exit at scale = 1, the latching edge D-B does NOT exempt. Two things then
  // have to hold: (1) the 0.75 step out is NEW GEOMETRY through the scale term
  // alone, so it retires that stale latch and books no new one; (2) the frame
  // home pays 0 and the counted settle re-judges on the UNLOCKED 25% line —
  // raw 300 clears 175.5 while a surviving latch would have demanded 351 = 50%.
  // That 25% is THIS path's verdict only: scale > 1 → 1 keeps its earlier latch
  // and lands on 50% (D2-6), and neither number is re-derived here.
  const h = zoomHarness();
  h.api.initVisualViewportHandling();
  h.keyboardTo(402); // raw 300 ≥ 175.5 = 25% at the scale-1 layout
  assert.equal(h.active(), true);
  assert.deepEqual(h.bottoms(), ["300px"]);
  h.keyboardTo(602); // raw 100 < 299, same geometry: the FROZEN latching exit
  assert.equal(h.active(), false);
  assert.equal(h.recovering(), true, "a real frozen latch is planted before the zoom (C03)");
  h.vv.scale = 0.75;
  h.vv.height = 402; // layout 702 and vv.width 1194 UNCHANGED — raw 300 again
  h.vv.dispatch("resize");
  assert.equal(h.active(), false, "the step out exits and pays 0");
  assert.deepEqual(h.bottoms(), ["0px"]);
  assert.equal(h.recovering(), false, "the scale term alone made it new geometry: the planted latch is retired, no new one booked");
  h.tick();
  assert.equal(h.active(), false, "one counting tick is not a settle");
  h.tick();
  assert.equal(h.active(), true, "settled at 0.75: the unlocked 25% line admits raw 300 ≥ 175.5");
  assert.deepEqual(h.bottoms(), ["300px"]);
  h.vv.scale = 1; // the trip home, layout / vv.width still unchanged
  h.vv.dispatch("resize");
  assert.equal(h.active(), false, "the return frame is the exit frame: it pays 0, no pre-zoom verdict rides it");
  assert.deepEqual(h.bottoms(), ["0px"]);
  assert.equal(h.recovering(), false, "and it books NO new latch (D-D)");
  assert.equal(h.api.settleState().candidate, 1, "a fresh candidate opens at scale 1");
  assert.equal(h.api.settleState().ticked, 0, "counting from zero");
  h.tick();
  assert.equal(h.active(), false, "one in-band tick is not a settle — D-C outranks the frozen in-band entry");
  h.tick();
  assert.equal(h.active(), true, "settled, the UNLOCKED 25% line re-judges: 300 ≥ 175.5 (a surviving latch would have demanded 351 and refused)");
  assert.deepEqual(h.bottoms(), ["300px"]);
});
