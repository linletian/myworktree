// Behavioral tests for the issue #100 L1 viewport & height layer (PR2 of
// docs/plans/feature-v0.6.0-webui-sidebar-responsive/): the iOS soft
// keyboard parks over the bottom of the page (the layout viewport does not
// resize, body/html never scroll, and interactive-widget=resizes-content is
// deliberately NOT used — Chromium-only, plan §3.5), so #app is pinned to
// window.visualViewport's visible bottom edge while anything is covered —
// with three guard rails: the occlusion sentinel is
// documentElement.clientHeight (scrollbar-gutter-free, unlike
// window.innerHeight), pinch/double-tap zoom (vv.scale != 1, whose
// vv.height shrinks WITH the scale) opts out of pinning entirely, and the
// scroll listener only re-pins an existing pin — a pure pan never
// introduces one. The inline height is cleared again when nothing is
// covered, and environments without visualViewport degrade to a no-op.
//
// R12 with PR1: this file also drives the shipped applySidebarCollapsed
// against the shipped viewport handler to prove the two coexist — the
// collapse owns the .collapsed class, the viewport handler owns an inline
// height, and neither touches the other's state.
//
// Same arrangement as sidebar_collapse.test.mjs: the logic is sliced out of
// the real served index.html and evaluated against stubbed browser state,
// so a drift between the slice anchors and index.html fails loudly at slice
// time. Run:
// node --test testdata/viewport_height.test.mjs
// (also wired into terminal_status_test.go for `go test`).
import test from "node:test";
import assert from "node:assert/strict";
import { readFileSync } from "node:fs";
import { fileURLToPath } from "node:url";
import { dirname, join } from "node:path";

const here = dirname(fileURLToPath(import.meta.url));
const staticDir = join(here, "..", "static");
const indexSource = readFileSync(join(staticDir, "index.html"), "utf8");

// --- source slicing helpers (same approach as sidebar_collapse.test.mjs) ---

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

// --- index.html harness -----------------------------------------------------

// The sliced functions close over the stubbed browser globals provided via
// deps; applySidebarCollapsed is sliced from the shipped PR1 code so the
// R12 case drives the REAL collapse wiring against the REAL viewport
// handler instead of a re-declared copy.
const viewportFactory = new Function("deps", `
  const {
    window, document, requestAnimationFrame, console,
    getActiveTerminalSession, sendResize, syncTerminalToAppliedSize,
    setInterval: injectInterval,
  } = deps;
  // issue #116 added a timer-driven reconcile; route it through the injected
  // dependency so the harness owns the cadence and node never keeps a real
  // interval alive past the test.
  const setInterval = (fn, ms) => injectInterval(fn, ms);
  // Mirrors the module-level declaration in index.html;
  // applySidebarCollapsed assigns it — the getter exposes it below.
  let sidebarCollapsed = false;
  ${[
    "function applyVisualViewportHeight",
    "function reapplyPinnedVisualViewportHeight",
    "function initVisualViewportHandling",
    "function refitActiveTerminal",
    "function applySidebarCollapsed",
  ].map(h => sliceBlock(inlineScript(indexSource), h)).join("\n\n")}
  return {
    applyVisualViewportHeight,
    reapplyPinnedVisualViewportHeight,
    initVisualViewportHandling,
    applySidebarCollapsed,
    // issue #116: the root-box sizing is part of the contract, so tests need the
    // document stub the handler writes to.
    __document: document,
    isCollapsed: () => sidebarCollapsed,
  };
`);

function makeAppElement() {
  const classes = new Set();
  return {
    style: {},
    classList: {
      add: (c) => classes.add(c),
      remove: (c) => classes.delete(c),
      contains: (c) => classes.has(c),
      toggle: (c, on) => { if (on === undefined ? !classes.has(c) : on) classes.add(c); else classes.delete(c); },
    },
    classes,
  };
}

// Attribute-model element stub, same rationale as sidebar_collapse.test.mjs:
// the shipped toggle flips the chevrons with toggleAttribute("hidden")
// because <svg> owns no hidden IDL property.
function makeIconElement({ attrs = [], attrValues: initialValues = [] } = {}) {
  const attrSet = new Set(attrs);
  const attrValues = new Map(initialValues.map(([k, v]) => [k, String(v)]));
  return {
    toggleAttribute(name, force) {
      const on = force === undefined ? !attrSet.has(name) : !!force;
      if (on) attrSet.add(name); else attrSet.delete(name);
      return on;
    },
    hasAttribute: (name) => attrSet.has(name),
    setAttribute: (name, value) => attrValues.set(name, String(value)),
    getAttribute: (name) => (attrValues.has(name) ? attrValues.get(name) : null),
  };
}

function makeDocument(app, layoutHeight) {
  const elements = {
    "sidebar-toggle-icon-left": makeIconElement(),
    "sidebar-toggle-icon-right": makeIconElement({ attrs: ["hidden"] }),
    "sidebar-toggle-btn": makeIconElement({ attrValues: [["aria-expanded", "true"]] }),
  };
  return {
    // The occlusion sentinel: documentElement.clientHeight, the
    // scrollbar-gutter-free layout height (vv.height excludes scrollbars
    // pinned to the visual viewport; window.innerHeight would include the
    // classic gutter and pin spuriously on desktops).
    // issue #116: the handler now also sizes the root box (it must never be
    // taller than the visible viewport, or that surplus becomes a scrollable
    // dead strip showing the canvas). The stub needs writable styles.
    documentElement: { clientHeight: layoutHeight, style: {} },
    // issue #116: the root box that must never exceed the visible viewport.
    body: { style: {} },
    getElementById(id) {
      if (id === "app") return app;
      return elements[id] ?? null;
    },
    // issue #116: lifecycle reconcile hooks (visibilitychange / focusin /
    // focusout) are registered through this stub.
    addEventListener() {},
    style: {},
  };
}

// A stand-in for window.visualViewport: geometry is mutable so a case can
// open/close the keyboard, pan the viewport, or pinch-zoom, and dispatch()
// fires the listeners the shipped init registered.
function makeVisualViewport({ height = 600, offsetTop = 0, scale = 1 } = {}) {
  const listeners = new Map();
  return {
    height,
    offsetTop,
    scale,
    listeners,
    addEventListener(type, cb) {
      if (!listeners.has(type)) listeners.set(type, []);
      listeners.get(type).push(cb);
    },
    dispatch(type) {
      for (const cb of listeners.get(type) ?? []) cb();
    },
    listenerCount: (type) => (listeners.get(type) ?? []).length,
  };
}

function viewportHarness({ withVisualViewport = true, layoutHeight = 600 } = {}) {
  const app = makeAppElement();
  const document = makeDocument(app, layoutHeight);
  const vv = makeVisualViewport({ height: layoutHeight, offsetTop: 0 });
  const windowEvents = new Map();
  const window = {
    // issue #116: initVisualViewportHandling reconciles on lifecycle edges, so
    // the stub must record listeners rather than throw.
    addEventListener(type, handler) {
      if (!windowEvents.has(type)) windowEvents.set(type, []);
      windowEvents.get(type).push(handler);
    },
  };
  if (withVisualViewport) window.visualViewport = vv;
  const rafCallbacks = [];
  const intervalCallbacks = [];
  const api = viewportFactory({
    window,
    document,
    requestAnimationFrame: (cb) => { rafCallbacks.push(cb); return rafCallbacks.length; },
    console,
    getActiveTerminalSession: () => null,
    sendResize: () => {},
    syncTerminalToAppliedSize: () => {},
    setInterval: (fn) => { intervalCallbacks.push(fn); return intervalCallbacks.length; },
  });
  return {
    api, app, vv, window,
    classes: app.classes,
    flushFrames: () => { for (const cb of rafCallbacks.splice(0)) cb(); },
    // Simulates the iOS keyboard opening: the layout viewport KEEPS its
    // height (that is the whole reason R6 exists), only the visual
    // viewport shrinks.
    windowEvents,
    // Drives the reconcile tick added for issue #116 without waiting real time.
    tickReconcile: () => { for (const fn of intervalCallbacks) fn(); },
    openKeyboard: (visibleHeight) => { vv.height = visibleHeight; vv.offsetTop = 0; vv.dispatch("resize"); },
    closeKeyboard: () => { vv.height = layoutHeight; vv.offsetTop = 0; vv.dispatch("resize"); },
  };
}

// --- R6: graceful degradation -----------------------------------------------

test("issue #100 L1 R6: without visualViewport the init is a no-op — no listeners, no inline style, no throw", () => {
  const h = viewportHarness({ withVisualViewport: false });
  assert.doesNotThrow(() => h.api.initVisualViewportHandling());
  assert.deepEqual(h.app.style, {}, "#app must not carry an inline style when there is nothing to compensate");
  assert.equal(h.api.isCollapsed(), false);
});

// --- R6: pinning #app to the visible bottom edge ----------------------------

test("issue #100 L1 R6: the keyboard opening narrows #app to the visible height in px", () => {
  const h = viewportHarness();
  h.api.initVisualViewportHandling();
  assert.equal(h.app.style.height || "", "", "at rest the CSS cascade (100dvh / 100vh fallback) owns the height");

  h.openKeyboard(300);
  assert.equal(h.app.style.height, "300px", "the covered strip is reclaimed by shrinking #app");
});

test("issue #100 L1 R6: the keyboard closing clears the inline height back to the CSS cascade", () => {
  const h = viewportHarness();
  h.api.initVisualViewportHandling();
  h.openKeyboard(300);
  assert.equal(h.app.style.height, "300px");

  h.closeKeyboard();
  assert.equal(h.app.style.height, "", "no occlusion -> no inline style, dvh/fallback sizing is restored");
  h.closeKeyboard();
  assert.equal(h.app.style.height, "", "re-clearing at rest is idempotent, not an oscillation");
});

test("issue #100 L1 R6: scroll only re-pins an existing pin — a pure pan never introduces one", () => {
  const h = viewportHarness();
  h.api.initVisualViewportHandling();
  assert.equal(h.vv.listenerCount("scroll"), 1, "the shipped init registers exactly one scroll listener");
  assert.equal(h.vv.listenerCount("resize"), 1, "... and exactly one resize listener");

  // Pure pan, nothing occluded (offsetTop grew and height shrank with it,
  // the visible bottom still sits at the layout bottom): scroll must NOT
  // introduce an inline height — the CSS cascade keeps ownership.
  h.vv.offsetTop = 40;
  h.vv.height = 560;
  h.vv.dispatch("scroll");
  assert.equal(h.app.style.height || "", "", "a pure pan leaves #app on the pure-CSS sizing path");

  // REVISED under issue #116 (product choice A): the pin target is
  // vv.height ALONE, so a pan moves nothing. Pinned while panned stays at
  // vv.height, and panning back to offsetTop 0 keeps it there — the whole
  // point being that nothing may ever sit behind the keyboard. (The previous
  // expectation, 300px = offsetTop + height, is exactly what let the pin
  // chase the pan and then strand; see the seq297–396 trace in
  // DATA-COLLECTION.md §5g.)
  const h2 = viewportHarness();
  h2.vv.offsetTop = 40;
  h2.vv.height = 260;
  h2.api.initVisualViewportHandling();
  assert.equal(h2.app.style.height, "260px", "panned + keyboard at init pins to vv.height, ignoring the pan");
  h2.vv.offsetTop = 0;
  h2.vv.dispatch("scroll");
  assert.equal(h2.app.style.height, "260px", "panning back changes nothing — vv.height never moved");
});

test("issue #100 L1 R6: pinch / double-tap zoom opts out of pinning — vv.height shrinks with the scale", () => {
  // Zoomed from the start: no pin, even though the zoomed vv.height looks
  // exactly like keyboard occlusion.
  const h = viewportHarness();
  h.vv.scale = 1.5;
  h.vv.height = 400;
  h.api.initVisualViewportHandling();
  assert.equal(h.app.style.height || "", "", "a zoomed visual viewport must not collapse the UI toward half height");

  // Zooming back to 1 re-runs the full judgment on the resize event: the
  // keyboard is still up, so the pin is restored.
  h.vv.scale = 1;
  h.vv.height = 300;
  h.vv.dispatch("resize");
  assert.equal(h.app.style.height, "300px", "zoom-out re-pins when the keyboard is still covered");

  // An existing keyboard pin is DROPPED, not shrunk, when the zoom arrives.
  const h2 = viewportHarness();
  h2.api.initVisualViewportHandling();
  h2.openKeyboard(300);
  assert.equal(h2.app.style.height, "300px");
  h2.vv.scale = 1.5;
  h2.vv.height = 200;
  h2.vv.dispatch("resize");
  assert.equal(h2.app.style.height || "", "", "the zoom short-circuit clears the pin instead of shrinking to the zoomed rectangle");
});

test("issue #100 L1 R6: init applies the current geometry immediately, so a reload with the keyboard already up starts narrowed", () => {
  const h = viewportHarness();
  h.openKeyboard(280);
  h.api.initVisualViewportHandling();
  assert.equal(h.app.style.height, "280px", "the initial apply runs before the first paint that matters");
});

// --- R12 x PR1: collapse and keyboard at the same time ----------------------

test("issue #100 L1 R12: collapse + keyboard coexist — the class and the inline height never fight each other", () => {
  const h = viewportHarness();
  h.api.initVisualViewportHandling();

  // Collapse first, keyboard second: the collapse must not have left an
  // inline height, and the keyboard must not have touched the class.
  h.api.applySidebarCollapsed(true);
  h.flushFrames();
  assert.deepEqual([...h.classes], ["collapsed"]);
  assert.equal(h.app.style.height, "", "PR1's collapse owns the class only — no inline height from it");

  h.openKeyboard(300);
  assert.equal(h.app.style.height, "300px");
  assert.deepEqual([...h.classes], ["collapsed"], "the viewport handler owns the height only — the class stays");
  h.api.applyVisualViewportHeight();
  assert.equal(h.app.style.height, "300px", "re-applying while collapsed is stable, the layout does not jump");

  // Expand again with the keyboard still open: the class flips back and
  // the narrowed height rides along untouched.
  h.api.applySidebarCollapsed(false);
  h.flushFrames();
  assert.deepEqual([...h.classes], []);
  assert.equal(h.app.style.height, "300px", "expanding does not clear the keyboard compensation");

  // Keyboard closes: the CSS cascade returns while the app is expanded.
  h.closeKeyboard();
  assert.equal(h.app.style.height, "");
  assert.deepEqual([...h.classes], []);
});

// --- issue #116: occlusion classification (soft keyboard vs floating bar) ---
// Calibrated on iPad Pro 11 landscape, layout viewport 702px:
//   soft keyboard     covered 428px = 60.9%  -> HARD  -> pin
//   magic floating bar covered 68.5px =  9.8% -> soft -> never pin
// The floating bar shrinks AND recovers the visual viewport without firing
// resize/scroll (measured seq47-54), so any pin introduced for it goes stale
// and leaves #app short by that amount forever. Not pinning removes the class.

test("issue #116: a soft keyboard (60.9% covered) pins #app to the visible bottom", () => {
  const h = viewportHarness({ layoutHeight: 702 });
  h.api.initVisualViewportHandling();
  h.openKeyboard(274); // measured seq14: vv.height 274, covered 428px
  assert.equal(h.app.style.height, "274px", "the keyboard is a hard occlusion and must be compensated");
  h.closeKeyboard();
  assert.equal(h.app.style.height || "", "", "and the pin is handed back to the cascade when it closes");
});

test("issue #116: the magic floating bar (9.8% covered) never pins #app", () => {
  const h = viewportHarness({ layoutHeight: 702 });
  h.api.initVisualViewportHandling();
  h.vv.height = 633.5; // measured seq47: covered 68.5px
  h.vv.dispatch("resize");
  assert.equal(h.app.style.height || "", "", "the floating bar is system-owned, user-movable chrome — the shell must not chase it");

  // The decisive property: because nothing was pinned, the event-less recovery
  // observed on-device (measured seq54: vv.height 633.5 -> 702 with NO
  // resize/scroll event) cannot leave a stale inline height behind.
  h.vv.height = 702;
  assert.equal(h.app.style.height || "", "", "no pin was ever introduced, so a silent recovery leaves no residue");
});

test("issue #116: the hard-occlusion threshold is a ratio, not a pixel constant", () => {
  // Threshold = 25% of the layout viewport, so the same judgment holds at any
  // size. Exercised at 400px: covered 100 pins, covered 99 does not.
  const hard = viewportHarness({ layoutHeight: 400 });
  hard.api.initVisualViewportHandling();
  hard.openKeyboard(300); // covered exactly 100 = 25%
  assert.equal(hard.app.style.height, "300px", "covered == 25% of the layout viewport is hard");

  const soft = viewportHarness({ layoutHeight: 400 });
  soft.api.initVisualViewportHandling();
  soft.openKeyboard(301); // covered 99 = 24.75%
  assert.equal(soft.app.style.height || "", "", "just under the ratio is a soft occlusion — no pin");
});

test("issue #116 A: the pan is invisible to the judgment — the pin survives the whole keyboard gesture", () => {
  // Replay of the on-device sequence that stranded the shell (iPadOS 27.2,
  // session ios272recal…, keyboard held open throughout at vv.height 274 /
  // clientHeight 702): the OLD rule grew the pin as the page was dragged
  // (seq297–310, offsetTop 12.5→237 → pin 286.5→511px), CLEARED it once the
  // drag pushed covered under 25% (seq311), and then never re-armed it on the
  // way back (seq362–396, covered climbing to 428px = 61%) because the R6
  // "scroll never introduces a pin" gate refused. Net: the keyboard covered
  // the bottom ~428px of the terminal indefinitely.
  // Under choice A, vv.offsetTop is not an input to anything, so every one of
  // those frames answers the same thing: 274px.
  const h = viewportHarness({ layoutHeight: 702 });
  h.api.initVisualViewportHandling();
  h.openKeyboard(274); // measured seq296
  assert.equal(h.app.style.height, "274px", "keyboard open pins to vv.height");
  for (const pan of [12.5, 103, 237, 265.5, 428, 0]) {
    h.vv.offsetTop = pan;
    h.vv.dispatch("scroll");
    assert.equal(h.app.style.height, "274px", `pan ${pan} must not change the pin — nothing may hide behind the keyboard`);
  }
  h.closeKeyboard();
  assert.equal(h.app.style.height || "", "", "only the keyboard leaving hands sizing back to the cascade");
});

// --- issue #116: keyboard-state TRANSITIONS that emit no event ---------------
// Reported on-device, all three of this shape: connecting a Magic Keyboard while
// the soft keyboard is up, expanding the soft keyboard from the floating bar,
// and disconnecting either one — the shell kept an inline height from the
// previous keyboard state, so #app stayed shorter than the visible area and the
// root canvas showed through as a white band. The Magic floating bar is measured
// to both shrink AND recover the visual viewport with no resize/scroll at all,
// so an event-driven handler can never be the whole answer.

test("issue #116: a stranded pin is cleared by the reconcile tick when the keyboard leaves silently", () => {
  const h = viewportHarness({ layoutHeight: 702 });
  h.api.initVisualViewportHandling();
  h.openKeyboard(274);
  assert.equal(h.app.style.height, "274px", "keyboard open pins first");

  // The keyboard leaves with NO event whatsoever (the reported transitions).
  h.vv.height = 702;
  assert.equal(h.app.style.height, "274px", "without a tick the pin is stranded — this is the white band");

  h.tickReconcile();
  assert.equal(h.app.style.height, "", "the reconcile tick must hand sizing back to the cascade");
});

test("issue #116: an occlusion that appears silently is pinned by the reconcile tick", () => {
  const h = viewportHarness({ layoutHeight: 702 });
  h.api.initVisualViewportHandling();
  assert.equal(h.app.style.height || "", "", "at rest the cascade owns the height");

  // Keyboard appears without firing anything we listen to.
  h.vv.height = 274;
  h.tickReconcile();
  assert.equal(h.app.style.height, "274px", "the tick must also apply a pin that arrived event-lessly");
});

test("issue #116: the reconcile tick is idempotent and does not churn the style", () => {
  const h = viewportHarness({ layoutHeight: 702 });
  h.api.initVisualViewportHandling();
  h.openKeyboard(274);
  // Sentinel: any write goes through this setter.
  let writes = 0;
  const real = h.app.style.height;
  Object.defineProperty(h.app.style, "height", {
    configurable: true,
    get: () => real,
    set: () => { writes += 1; },
  });
  h.tickReconcile();
  h.tickReconcile();
  assert.equal(writes, 0, "unchanged target must not write — the tick runs every 500ms and a write invalidates layout");
});

test("issue #116: transitions still respect the floating bar (no pin) and zoom (no pin)", () => {
  const h = viewportHarness({ layoutHeight: 702 });
  h.api.initVisualViewportHandling();
  h.vv.height = 633.5; // magic floating bar, 9.8%
  h.tickReconcile();
  assert.equal(h.app.style.height || "", "", "the bar must never pin, tick or not");
  h.vv.scale = 0.85;
  h.vv.height = 274; // even a keyboard-sized shrink while zoomed
  h.tickReconcile();
  assert.equal(h.app.style.height || "", "", "zoom keeps its blanket exemption on every path");
});

// --- issue #116: the document box must never exceed the visible viewport -----
// Measured failure (session sop-7746830…, S5): with the keyboard up, #app was
// correctly pinned to 274px, but html/body stayed 100% of the LAYOUT viewport
// (702px). The 702-274 = 428px surplus was genuinely SCROLLABLE: pageTop and
// scrollTop climbed in lockstep (16.5 -> 138 -> 276) and probe.visualBottom hit
// BODY rgb(249,249,249). The user sees a band exactly one keyboard tall, and no
// gesture guard can stop it because the document really does scroll — the scroll
// RANGE has to go.

test("issue #116: the root box shrinks with the visible viewport, removing the scroll range", () => {
  const h = viewportHarness({ layoutHeight: 702 });
  const doc = h.api.__document;
  h.api.initVisualViewportHandling();
  assert.equal(doc.documentElement.style.height || "", "", "at rest the root box is left to CSS");

  h.openKeyboard(274);
  assert.equal(h.app.style.height, "274px", "the terminal column is pinned");
  assert.equal(
    doc.documentElement.style.height,
    "274px",
    "issue #116: html must be sized to the visible viewport too — otherwise the " +
      "428px surplus becomes a scrollable dead strip that shows the canvas",
  );
  assert.equal(doc.body.style.height, "274px", "body must match, or it re-introduces the tall box");

  h.closeKeyboard();
  assert.equal(doc.documentElement.style.height || "", "", "restoring the keyboard restores the root box");
  assert.equal(doc.body.style.height || "", "", "and body with it");
});

test("issue #116: the floating bar also shrinks the root box (it is still surplus)", () => {
  // The bar is only 68.5px, but it is surplus all the same, and 68.5px of the
  // root canvas is exactly the band reported before the pin existed.
  const h = viewportHarness({ layoutHeight: 702 });
  const doc = h.api.__document;
  h.api.initVisualViewportHandling();
  assert.equal(h.app.style.height || "", "", "the bar must not pin #app");
  h.tickReconcile.call(null);
  h.vv.height = 633.5;
  h.tickReconcile();
  assert.equal(h.app.style.height || "", "", "the bar still never pins #app");
  assert.equal(doc.documentElement.style.height, "633.5px", "but the root box must not exceed the visible area");
});

test("issue #116: zoom leaves the root box alone, like every other compensation", () => {
  const h = viewportHarness({ layoutHeight: 702 });
  const doc = h.api.__document;
  h.api.initVisualViewportHandling();
  h.vv.scale = 0.85;
  h.vv.height = 500;
  h.tickReconcile();
  assert.equal(doc.documentElement.style.height || "", "", "zoom keeps its blanket exemption");
});
