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
  } = deps;
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
    documentElement: { clientHeight: layoutHeight },
    getElementById(id) {
      if (id === "app") return app;
      return elements[id] ?? null;
    },
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
  const window = {};
  if (withVisualViewport) window.visualViewport = vv;
  const rafCallbacks = [];
  const api = viewportFactory({
    window,
    document,
    requestAnimationFrame: (cb) => { rafCallbacks.push(cb); return rafCallbacks.length; },
    console,
    getActiveTerminalSession: () => null,
    sendResize: () => {},
    syncTerminalToAppliedSize: () => {},
  });
  return {
    api, app, vv, window,
    classes: app.classes,
    flushFrames: () => { for (const cb of rafCallbacks.splice(0)) cb(); },
    // Simulates the iOS keyboard opening: the layout viewport KEEPS its
    // height (that is the whole reason R6 exists), only the visual
    // viewport shrinks.
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

  // Already pinned (keyboard open while panned): panning back re-pins to
  // the new visible bottom edge. This case exercises the shrinking
  // direction only; growth is equally allowed — an existing pin follows
  // the visible bottom edge both ways, bounded above by
  // documentElement.clientHeight (the visual viewport cannot pan out of
  // the layout viewport, and scale != 1 short-circuits to no pin).
  const h2 = viewportHarness();
  h2.vv.offsetTop = 40;
  h2.vv.height = 260;
  h2.api.initVisualViewportHandling();
  assert.equal(h2.app.style.height, "300px", "panned + keyboard at init pins the visible bottom edge");
  h2.vv.offsetTop = 0;
  h2.vv.dispatch("scroll");
  assert.equal(h2.app.style.height, "260px", "scroll re-pins the already-pinned app to the new bottom edge");
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
