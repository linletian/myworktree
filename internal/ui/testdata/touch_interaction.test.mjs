// Behavioral tests for the issue #100 L3 touch-interaction layer (PR4 of
// docs/plans/feature-v0.6.0-webui-sidebar-responsive/): the sidebar
// split-panel drag migrated from mouse events to Pointer Events (R9) —
// pointerdown/pointermove/pointerup plus setPointerCapture, with pointercancel
// treated as a stop condition (iOS fires it when the UA takes over a
// gesture). Mouse, touch and pen all run this one code path. The clamp math
// (minTop 120 / minBottom 100) is pinned byte-for-byte, and the CSS side
// (touch-action:none on the handle, 44px min-* targets, hover-wrapping) is
// pinned by the served-asset text anchors in TestTouchInteractionAnchors.
//
// Same arrangement as narrow_drawer.test.mjs: the functions are sliced out
// of the real served index.html and evaluated against stubbed browser
// state, so a drift between the slice anchors and index.html fails loudly
// at slice time. Run:
// node --test testdata/touch_interaction.test.mjs
// (also wired into terminal_status_test.go for `go test`).
import test from "node:test";
import assert from "node:assert/strict";
import { readFileSync } from "node:fs";
import { fileURLToPath } from "node:url";
import { dirname, join } from "node:path";

const here = dirname(fileURLToPath(import.meta.url));
const staticDir = join(here, "..", "static");
const indexSource = readFileSync(join(staticDir, "index.html"), "utf8");

// --- source slicing helpers (same approach as narrow_drawer.test.mjs) ---

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
// deps; the real wiring (pointerdown -> capture + listeners + state) is what
// is under test, so ALL of it is sliced from the shipped script rather than
// re-declared here.
const touchFactory = new Function("deps", `
  const { document } = deps;
  // Mirrors the module-level declarations in index.html; startSidebarResize
  // assigns all three — the getter exposes them below.
  let isResizingSidebar = false;
  let sidebarResizeStartY = 0;
  let sidebarTopPanelStartHeight = 0;
  ${[
    "function startSidebarResize",
    "function handleSidebarResize",
    "function stopSidebarResize",
  ].map(h => sliceBlock(inlineScript(indexSource), h)).join("\n\n")}
  return {
    startSidebarResize,
    handleSidebarResize,
    stopSidebarResize,
    isResizing: () => isResizingSidebar,
    startY: () => sidebarResizeStartY,
    startHeight: () => sidebarTopPanelStartHeight,
  };
`);

function makeHandle({ hasCapture = true, throwOnCapture = false } = {}) {
  const classes = new Set();
  const calls = { capture: [], release: [] };
  return {
    classes,
    calls,
    classList: {
      add: (c) => classes.add(c),
      remove: (c) => classes.delete(c),
      contains: (c) => classes.has(c),
    },
    setPointerCapture(pointerId) {
      calls.capture.push(pointerId);
      if (throwOnCapture) throw new Error("setPointerCapture not supported");
    },
    releasePointerCapture(pointerId) { calls.release.push(pointerId); },
    hasPointerCapture(pointerId) { return hasCapture && calls.capture.includes(pointerId); },
  };
}

// Layout stub: sidebar 600px tall, title 40px -> content 560px; top panel
// starts at 300px. Clamp window: [minTop=120, contentHeight-minBottom=460].
function makeDocument({ handle } = {}) {
  const listeners = new Map(); // document-level listeners, like the shipped code uses
  const topPanel = { offsetHeight: 300, style: {} };
  const bottomPanel = { style: {} };
  const sidebar = {
    offsetHeight: 600,
    querySelector: () => ({ offsetHeight: 40 }), // .sidebar-title
  };
  const elements = {
    sidebar,
    "sidebar-top-panel": topPanel,
    "sidebar-bottom-panel": bottomPanel,
    "sidebar-resize-handle": handle,
  };
  return {
    elements,
    listeners,
    getElementById(id) { return elements[id] ?? null; },
    addEventListener(type, fn) {
      if (!listeners.has(type)) listeners.set(type, []);
      listeners.get(type).push(fn);
    },
    removeEventListener(type, fn) {
      const list = listeners.get(type) ?? [];
      const i = list.indexOf(fn);
      if (i >= 0) list.splice(i, 1);
    },
  };
}

function pointerEvent({ pointerId = 7, clientY = 0, pointerType = "mouse" } = {}) {
  return {
    pointerId, clientY, pointerType,
    prevented: false,
    preventDefault() { this.prevented = true; },
  };
}

function touchHarness({ hasCapture = true, throwOnCapture = false } = {}) {
  const handle = makeHandle({ hasCapture, throwOnCapture });
  const document = makeDocument({ handle });
  const api = touchFactory({ document });
  return { api, document, handle };
}

// --- R9: pointerdown wiring --------------------------------------------------

test("issue #100 L3 R9: pointerdown captures the pointer, flips the drag state, registers move/up/cancel, preventDefaults", () => {
  const h = touchHarness();
  const ev = pointerEvent({ pointerId: 7, clientY: 250 });

  h.api.startSidebarResize(ev);
  assert.equal(h.api.isResizing(), true, "the drag state flips on");
  assert.equal(h.api.startY(), 250, "the start Y is recorded");
  assert.equal(h.api.startHeight(), 300, "the top panel's start height is recorded");
  assert.deepEqual(h.handle.calls.capture, [7], "setPointerCapture runs with the event's pointerId");
  assert.equal(h.handle.classes.has("dragging"), true, "the .dragging class lands on the handle");
  for (const type of ["pointermove", "pointerup", "pointercancel"]) {
    assert.equal(h.document.listeners.get(type)?.length, 1, `a document ${type} listener registers`);
  }
  assert.equal(ev.prevented, true, "pointerdown is preventDefaulted (selection guard)");
});

test("issue #100 L3 R9: touch and pen pointer types run the exact same path as mouse", () => {
  for (const pointerType of ["touch", "pen", "mouse"]) {
    const h = touchHarness();
    const ev = pointerEvent({ pointerId: 3, clientY: 100, pointerType });
    h.api.startSidebarResize(ev);
    assert.equal(h.api.isResizing(), true, `${pointerType}: drag starts`);
    assert.deepEqual(h.handle.calls.capture, [3], `${pointerType}: capture runs`);
    assert.equal(ev.prevented, true, `${pointerType}: preventDefault runs`);
  }
});

test("issue #100 L3 R9: an engine whose setPointerCapture throws still drags (guard degrades, listeners registered)", () => {
  const h = touchHarness({ throwOnCapture: true });
  const ev = pointerEvent({});
  assert.doesNotThrow(() => h.api.startSidebarResize(ev), "a throwing capture must not kill the drag start");
  assert.equal(h.api.isResizing(), true);
  assert.equal(h.document.listeners.get("pointermove")?.length, 1, "the document listeners still cover the drag");
});

test("issue #100 L3 R9: a second pointerdown mid-drag is ignored — no baseline hijack, no dual-pointer capture", () => {
  const h = touchHarness();
  h.api.startSidebarResize(pointerEvent({ pointerId: 1, clientY: 200 }));
  assert.equal(h.api.startY(), 200, "first pointerdown sets the baseline");

  // Mid-drag second pointerdown (desktop secondary button / second finger):
  // must NOT re-baseline the drag or capture a second pointer.
  h.api.startSidebarResize(pointerEvent({ pointerId: 2, clientY: 500 }));
  assert.equal(h.api.startY(), 200, "the second pointerdown must not hijack the drag baseline");
  assert.equal(h.api.startHeight(), 300, "the start height stays the original one");
  assert.deepEqual(h.handle.calls.capture, [1], "setPointerCapture ran exactly once — no dual-pointer contention");
  assert.equal(h.api.isResizing(), true);

  // The original drag still applies against the ORIGINAL baseline.
  const topPanel = h.document.elements["sidebar-top-panel"];
  h.api.handleSidebarResize(pointerEvent({ clientY: 250 }));
  assert.equal(topPanel.style.height, "350px", "the in-flight drag keeps using the first baseline");
});

// --- R9: the clamp math (pinned byte-for-byte from the shipped source) -------

test("issue #100 L3 R9: the drag clamps the top panel between minTop 120 and contentHeight-minBottom 460", () => {
  const h = touchHarness();
  const topPanel = h.document.elements["sidebar-top-panel"];

  h.api.startSidebarResize(pointerEvent({ clientY: 300 }));
  h.api.handleSidebarResize(pointerEvent({ clientY: 305 }));
  assert.equal(topPanel.style.height, "305px", "an in-range drag applies verbatim");
  assert.equal(topPanel.style.flex, "none", "the top panel leaves flex mode while resizing");
  assert.equal(h.document.elements["sidebar-bottom-panel"].style.flex, "1", "the bottom panel absorbs the rest");

  // Drag far up: clamps at minTop = 120 (300 + (50 - 300) = 50 -> 120).
  h.api.startSidebarResize(pointerEvent({ clientY: 300 }));
  h.api.handleSidebarResize(pointerEvent({ clientY: 50 }));
  assert.equal(topPanel.style.height, "120px", "dragging past the top clamps to minTop 120");

  // Drag far down: clamps at contentHeight(560) - minBottom(100) = 460.
  h.api.startSidebarResize(pointerEvent({ clientY: 300 }));
  h.api.handleSidebarResize(pointerEvent({ clientY: 900 }));
  assert.equal(topPanel.style.height, "460px", "dragging past the bottom clamps to contentHeight - minBottom");
});

test("issue #100 L3 R9: handleSidebarResize is a no-op when not dragging", () => {
  const h = touchHarness();
  const topPanel = h.document.elements["sidebar-top-panel"];
  h.api.handleSidebarResize(pointerEvent({ clientY: 42 }));
  assert.equal(topPanel.style.height, undefined, "no drag state -> no height write");
});

// --- R9: stop / cancel -------------------------------------------------------

test("issue #100 L3 R9: pointerup stops the drag — listeners removed, class lifted, capture released", () => {
  const h = touchHarness();
  h.api.startSidebarResize(pointerEvent({ pointerId: 9 }));

  h.api.stopSidebarResize(pointerEvent({ pointerId: 9 }));
  assert.equal(h.api.isResizing(), false, "the drag state flips off");
  assert.equal(h.handle.classes.has("dragging"), false, "the .dragging class is lifted");
  for (const type of ["pointermove", "pointerup", "pointercancel"]) {
    assert.equal(h.document.listeners.get(type)?.length, 0, `the document ${type} listener is removed`);
  }
  assert.deepEqual(h.handle.calls.release, [9], "the held pointer capture is released");
});

test("issue #100 L3 R9: pointercancel is registered as a stop condition and cleans up exactly like pointerup", () => {
  const h = touchHarness();
  h.api.startSidebarResize(pointerEvent({ pointerId: 5 }));
  // iOS fires pointercancel when the UA takes over the gesture; the shipped
  // wiring points it at the same stop handler.
  const cancelFns = h.document.listeners.get("pointercancel") ?? [];
  assert.equal(cancelFns.length, 1, "pointercancel is registered alongside pointerup");
  cancelFns[0](pointerEvent({ pointerId: 5 }));
  assert.equal(h.api.isResizing(), false, "cancel ends the drag state");
  assert.equal(h.handle.classes.has("dragging"), false, "cancel lifts the dragging class");
  assert.equal(h.document.listeners.get("pointermove")?.length, 0, "cancel removes the move listener");
});

test("issue #100 L3 R9: stop without held capture does not attempt a release (guard)", () => {
  const h = touchHarness({ hasCapture: false });
  h.api.startSidebarResize(pointerEvent({ pointerId: 2 }));
  h.api.stopSidebarResize(pointerEvent({ pointerId: 2 }));
  assert.deepEqual(h.handle.calls.release, [], "no hasPointerCapture -> no release call");
  assert.equal(h.api.isResizing(), false, "the drag still stops");
});
