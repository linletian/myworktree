// Behavioral + structural tests for the viewport & height layer (issue #100 L1
// R6/R12, contract v3.0 "full-height shell + PTY-local avoidance", plan
// docs/plans/fix-issue116/).
//
// WHAT THIS FILE PROTESTS, AND WHY TWO PREVIOUS ANSWERS WERE WRONG.
//
// Contract v2.1 answered "iOS scrolls the root anyway" by PARKING it: one pass
// wrote #app's height AND dragged scrollingElement / documentElement / body /
// window back to 0, re-checked on the following layout frames. That is a SYMPTOM
// MASK — a page that has to be pulled back to the top every frame is still a page
// the user can scroll, and the pull was visible (the band appeared, then was
// yanked away). Deleted, not renamed.
//
// Contract v2.2 was structural: out-of-flow fixed shell, `overflow: clip` root,
// and ONE write — it pinned #app to vv.height whenever the visual viewport fell
// short. The structure survives; the pin does not, because the user's own control
// experiment (real Gmail, same iPad) showed what the reference actually does: with
// the soft keyboard OR the Magic Keyboard's floating panel up, the LAYOUT IS
// UNCHANGED — the keyboard floats over a page that never moved. Resizing the
// whole shell to vv.height was the shell moving, and every frame of this capture
// proves the layout never changed at all — the pin was cosmetic and wrong.
//
// Contract v3.0 is therefore "FULL-HEIGHT SHELL, PTY-LOCAL AVOIDANCE":
//
//   * #app is `position: fixed; top: 0; left: 0`, 100vh → 100dvh, and is FULL
//     HEIGHT IN EVERY STATE: app.bottom answers to documentElement.clientHeight
//     (layoutGap = clientHeight − app.bottom ≈ 0), NOT to vv.height. In this
//     harness #app's inline height setter THROWS, so "the shell is never resized"
//     is executed, not grepped: the retired pin coming back fails at the write.
//   * html/body are `overflow: hidden` (legacy floor) THEN `overflow: clip`: a
//     clipped box has no scroll mechanism, so there is no range to walk and no
//     origin to park. The root has NO mechanism in this harness — every scrollTop
//     setter and scrollTo / scrollBy THROWS. This is STRICTER than the reference:
//     Gmail grows a hidden strip under the keyboard that a full-page swipe
//     reveals; this build never gives the root a scroll area at all.
//   * The ONE write is each PTY host's OWN `bottom`, inside
//     #terminal-container (`position:absolute; inset:0` in both host factories):
//     raw = max(0, clientHeight − vv.height) at scale = 1, 0 under zoom, then
//     FLOORED — under SMALL_OCCLUSION_RATIO (0.15) of clientHeight it owes 0.
//     In the measured 702-tall layout whose hosts bottom out at 682 the floor is
//     702*0.15 = 105.3px, so it pays BOTH real keyboards — 274 → 428px (host bottom
//     254) and 555 → 147px (host bottom 535) — and exempts only the thin floating
//     strip (633.5 → raw 68.5 < 105.3 → host bottom stays 682), without moving a
//     single ancestor box. EVERY host gets it, hidden and
//     inactive included, because a stale bottom on the host the user switches TO is
//     the next band.
//   * INNER scrollers stay scrollable (A0 step S8): .xterm-viewport, #wt-list,
//     dialog bodies, the quick-keys panel. Mutable in this harness, asserted
//     UNWRITTEN.
//
// Same arrangement as sidebar_collapse.test.mjs: the logic is sliced out of the
// real served index.html and evaluated against stubbed browser state, so a drift
// between the slice anchors and index.html fails loudly at slice time. Run:
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
const ptySource = readFileSync(join(staticDir, "kinds", "pty.js"), "utf8");

// The measured iPad-landscape layout this contract was calibrated on
// (docs/plans/fix-issue116/evidence/raw/sop6-3357d50e…, read-only reference):
// documentElement.clientHeight = 702, env(safe-area-inset-bottom) = 20px, so
// #workspace / #terminal-container — and every host inside them — bottom out at
// 682 while the shell bottoms out at 702.
const LAYOUT_HEIGHT = 702;
const SAFE_BOTTOM = 20;
const HOST_BOTTOM = LAYOUT_HEIGHT - SAFE_BOTTOM; // 682
// #terminal-controls' authored offsets, straight out of the shipped CSS: the
// column sits 20px from the top of #workspace and 20px from its bottom (the
// clearance over the transport strip). The avoidance NEVER writes top, so the
// quick-keys entry button is fixed while the column shortens; the panel's
// `max-height: 100%` then resolves against the shortened height.
const CONTROLS_TOP = 20;
const CONTROLS_AUTHORED_BOTTOM = 20;
// The transport strip's real band height (~17px, per the CSS comment on the
// column's bottom). Only ever used to derive a rect for the stub.
const STATUS_BAR_HEIGHT = 17;

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

// Code, not prose: the contract documents BY NAME the calls it removed, so a
// ban that greps the file including comments would have to ban its own
// explanation. Strip comments first — a check that cannot fail is not a check.
function stripComments(src) {
  return src.replace(/\/\/[^\n]*/g, "").replace(/\/\*[\s\S]*?\*\//g, "");
}

// The whole <style> block: the structural contract lives in CSS, so a
// text-level anchor has to read CSS and not prose.
function styleBlock(html) {
  const start = html.indexOf("<style>");
  const end = html.indexOf("</style>", start);
  assert.ok(start >= 0 && end > start, "index.html has no <style> block");
  return html.slice(start + "<style>".length, end);
}

// Every CSS rule whose selector matches, in source order. `last` is the rule
// the cascade resolves for same-origin, equal-specificity declarations — the
// same mechanism this contract depends on for `hidden` → `clip` and for
// `100vh` → `@supports (height: 100dvh)`.
function cssRules(css, selectorRe) {
  const stripped = css.replace(/\/\*[\s\S]*?\*\//g, "");
  // The `g` flag is not optional here: exec without it re-matches the same
  // position forever and hangs the runner.
  const re = new RegExp(selectorRe.source + "\\s*\\{", selectorRe.flags.includes("g") ? selectorRe.flags : selectorRe.flags + "g");
  const hits = [];
  let m;
  while ((m = re.exec(stripped)) !== null) {
    let depth = 0;
    for (let i = m.index + m[0].length - 1; i < stripped.length; i++) {
      if (stripped[i] === "{") depth++;
      else if (stripped[i] === "}") {
        depth--;
        if (depth === 0) {
          hits.push(stripped.slice(m.index, i + 1));
          break;
        }
      }
    }
  }
  assert.ok(hits.length >= 1, `no CSS rule matched ${selectorRe} in index.html`);
  return hits;
}

// --- index.html harness -----------------------------------------------------

// A NON-SCROLLABLE root. The load-bearing stub of the whole file: the box
// reports NO scroll range (scrollHeight === clientHeight) and REFUSES every
// scroll write. v2.1's harness modelled a scrollable root with a counted position
// so it could prove the parking worked; that model can no longer fail the way the
// product can, which is precisely the mistake being corrected.
function makeNonScrollableRoot(layoutHeight) {
  const attempts = [];
  const ban = (what) => {
    attempts.push(what);
    throw new Error(`the ROOT must never be written — attempted ${what}`);
  };
  const rootBox = { clientHeight: layoutHeight, scrollHeight: layoutHeight };
  Object.defineProperty(rootBox, "scrollTop", {
    configurable: true,
    get: () => 0,
    set: (value) => ban(`scrollTop = ${value}`),
  });
  Object.defineProperty(rootBox, "scrollHeight", {
    configurable: true,
    get: () => layoutHeight,
    set: (value) => ban(`scrollHeight = ${value}`),
  });
  return { box: rootBox, attempts };
}

// #app: the FULL-HEIGHT fixed shell. Two things are executed rather than
// asserted — the inline height setter THROWS (v2.2's pin must not come back),
// and the rect is derived from the cascade's layout height alone, so app.bottom
// is clientHeight in every state and `layoutGap` cannot lie.
function makeAppElement(layoutHeight, attempts) {
  const classes = new Set();
  const app = {
    style: {},
    classList: {
      add: (c) => classes.add(c),
      remove: (c) => classes.delete(c),
      contains: (c) => classes.has(c),
      toggle: (c, on) => { if (on === undefined ? !classes.has(c) : on) classes.add(c); else classes.delete(c); },
    },
    classes,
  };
  Object.defineProperty(app, "getBoundingClientRect", {
    value: () => ({ top: 0, bottom: layoutHeight, height: layoutHeight, left: 0, right: 1194, width: 1194 }),
  });
  Object.defineProperty(app.style, "height", {
    configurable: true,
    get: () => {
      attempts.push("READ of #app's inline height — nothing in v3.0 should even need it");
      return "";
    },
    set: (value) => {
      const what = `#app.style.height = ${JSON.stringify(value)} (the shell is FULL HEIGHT by contract; the retired v2.2 pin must not return)`;
      attempts.push(what);
      throw new Error(what);
    },
  });
  return app;
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

// An INNER scroller: the terminal's viewport and the worktree list. Mutable on
// purpose — the user must still be able to scroll them — and counted, so every
// case can assert the viewport layer never wrote one.
function makeInnerScroller(name, state) {
  return {
    get scrollTop() { return state.scrollTop; },
    set scrollTop(value) {
      state.scrollTop = value;
      state.writes[name] = (state.writes[name] || 0) + 1;
    },
    writes: state.writes,
    scrollHeight: 4000,
    clientHeight: 600,
  };
}

// A PTY host: `position:absolute; inset:0` inside #terminal-container in both
// shipped factories (index.html's createTerminalHost and PtyRenderer._createSession
// in kinds/pty.js). The stub models the RESOLVED inset — bottom 0 until the
// applier writes its own bottom — and counts every write per host, which is what
// makes "write-only-on-change" and "no stale host" testable instead of arguable.
// A real engine certifies that the LONGHAND survives the `inset` SHORTHAND in
// structural-check.mjs; this harness certifies the arithmetic and the coverage.
function promoteHost(host, container, stats) {
  stats = stats || container.stats;
  host.writes = 0;
  Object.defineProperty(host.style, "bottom", {
    configurable: true,
    get: () => (host.style.__bottom === undefined ? "" : host.style.__bottom),
    set: (value) => {
      host.writes += 1;
      stats.hostWrites += 1;
      stats.perHost[host.dataset.instanceId] = (stats.perHost[host.dataset.instanceId] || 0) + 1;
      host.style.__bottom = value;
    },
  });
  Object.defineProperty(host, "getBoundingClientRect", {
    value: () => {
      const offset = parseFloat(host.style.bottom || "0") || 0;
      const top = container.top;
      const bottom = container.bottom - offset;
      return { top, bottom, height: Math.max(0, bottom - top), left: 0, right: container.width, width: container.width };
    },
  });
  return host;
}

function makePtyHost(id, container, stats) {
  const host = {
    tagName: "DIV",
    dataset: { instanceId: id },
    style: { position: "absolute", display: "none" },
  };
  return promoteHost(host, container, stats);
}

// #terminal-container: the containing block every host resolves its `bottom`
// against. Its own box is FIXED by the contract — v3.0 never shortens it — so
// the geometry here is a constant, and the only moving part is each host's
// bottom offset.
function makeTerminalContainer({ top = 0, bottom = HOST_BOTTOM, width = 1194 } = {}) {
  const container = {
    id: "terminal-container",
    top,
    bottom,
    width,
    hosts: [],
    stats: { hostWrites: 0, perHost: {} },
    selectorAttempts: [],
    querySelectorAll(selector) {
      container.selectorAttempts.push(selector);
      // The harness answers ONLY the shipped selector. If index.html changes the
      // selector, this throws and the harness has to be re-audited on purpose
      // rather than passing by accident.
      assert.equal(selector, ':scope > [data-instance-id]', "the host selector the applier queries changed; re-audit the harness");
      return container.hosts;
    },
    appendChild(node) {
      // A node created through document.createElement arrives bare; promote it
      // exactly the way the shipped factory's host behaves once it is in the
      // container, so the applier can see and lift it.
      if (!node.getBoundingClientRect) promoteHost(node, container);
      container.hosts.push(node);
      return node;
    },
    getBoundingClientRect: () => ({ top, bottom, height: bottom - top, left: 0, right: width, width }),
    addHost(id) {
      return container.appendChild(makePtyHost(id, container, container.stats));
    },
    host: (id) => container.hosts.find((item) => item.dataset.instanceId === id),
    bottoms: () => container.hosts.map((item) => item.style.bottom),
    rects: () => container.hosts.map((item) => item.getBoundingClientRect()),
  };
  return container;
}

function makeDocument(app, root, container, overlays = {}) {
  const elements = {
    "sidebar-toggle-icon-left": makeIconElement(),
    "sidebar-toggle-icon-right": makeIconElement({ attrs: ["hidden"] }),
    "sidebar-toggle-btn": makeIconElement({ attrValues: [["aria-expanded", "true"]] }),
    "terminal-container": container,
    // The two overlay children of #workspace. Absent by default: the applier has
    // to survive a page without them (login gate, pre-boot) exactly as it
    // survives a missing #terminal-container, and handing out a stub for every id
    // would make that branch untestable.
    "terminal-controls": overlays.controls ?? null,
    "status-bar": overlays.statusBar ?? null,
  };
  const docEvents = new Map();
  return {
    // The occlusion sentinel: documentElement.clientHeight, the
    // scrollbar-gutter-free layout height the FULL-HEIGHT shell answers to.
    documentElement: root.box,
    // The second root box, and it answers the same way: no range, no writes.
    body: root.box,
    scrollingElement: root.box,
    hidden: false,
    // Bare on purpose: createTerminalHost stamps dataset / cssText itself, and
    // the container promotes the node when it is appended, so the factory path
    // and the pre-built hosts in the harness go through the same code.
    createElement: (tagName) => ({ tagName: String(tagName).toUpperCase(), dataset: {}, style: {} }),
    getElementById(id) {
      if (id === "app") return app;
      return elements[id] ?? null;
    },
    // visibilitychange / focusin / focusout are registered HERE, so the stub
    // records them — a no-op addEventListener would make the lifecycle cases
    // above unable to fail, which is the same as not testing them.
    events: docEvents,
    addEventListener(type, handler) {
      if (!docEvents.has(type)) docEvents.set(type, []);
      docEvents.get(type).push(handler);
    },
    dispatch(type) {
      for (const handler of docEvents.get(type) ?? []) handler({ type });
    },
    listenerCount: (type) => (docEvents.get(type) ?? []).length,
  };
}

// The overlay children — #terminal-controls (the quick-keys entry / panel column,
// absolutely positioned in #workspace) and #status-bar (the transport strip, now
// `position:fixed` under v3.1). Their containing blocks are NOT shortened by the
// keyboard, so the only way they stay reachable is their OWN `bottom`, which is
// exactly what the judgment writes and exactly what this stub makes observable:
// `writes` counts every write, `bottom` reads back the last value, and the rect
// resolves the written offset against the box each element is actually anchored to
// — `resolveBottom`. For #terminal-controls that is the workspace bottom (682, the
// same floor the hosts bottom out at); for the fixed #status-bar it is the VIEWPORT
// bottom (layoutHeight, 702), because a fixed box resolves `bottom` against the
// viewport, not the safe-area-inset workspace. That 20px is the whole point of the
// fixed change: at occlusion 0 the Transport reaches the PHYSICAL bottom (702), not
// the inset bottom (682) — so it no longer sits ~safe-area short of the edge. The
// column's authored `top` stays fixed, so its HEIGHT shortens by the floored
// occlusion — the number #quick-keys-panel's `max-height: 100%` resolves against.
function makeOverlayChild({ id, top, workspaceBottom, authoredBottom, resolveBottom }) {
  const anchor = resolveBottom === undefined ? workspaceBottom : resolveBottom;
  const writes = [];
  const el = { id, style: {}, writes };
  Object.defineProperty(el.style, "bottom", {
    configurable: true,
    get() { return this._bottom === undefined ? "" : this._bottom; },
    set(value) {
      this._bottom = value;
      writes.push({ id, bottom: value });
    },
  });
  Object.defineProperty(el, "getBoundingClientRect", {
    value: () => {
      const written = el.style._bottom;
      const offset = written === undefined || written === "" ? authoredBottom : parseFloat(written);
      const bottom = anchor - offset;
      return { top, bottom, height: bottom - top, width: 44 };
    },
  });
  return el;
}

// A stand-in for window.visualViewport: geometry is mutable so a case can
// open/close the keyboard, pan the viewport, or pinch-zoom, and dispatch()
// fires the listeners the shipped init registered.
function makeVisualViewport({ height = LAYOUT_HEIGHT, offsetTop = 0, scale = 1 } = {}) {
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
      for (const cb of listeners.get(type) ?? []) cb({ type });
    },
    listenerCount: (type) => (listeners.get(type) ?? []).length,
  };
}

function buildViewportFactory(src) {
  const slices = [
    "function applyVisualViewportOcclusion",
    "function reapplyVisualViewportOcclusion",
    "function initVisualViewportHandling",
    "function createTerminalHost",
    "function refitActiveTerminal",
    "function applySidebarCollapsed",
  ].map((header) => sliceBlock(src, header)).join("\n\n");
  return new Function("deps", `
  const {
    window, document, requestAnimationFrame, console,
    getActiveTerminalSession, sendResize, syncTerminalToAppliedSize,
    setInterval: injectInterval,
  } = deps;
  // The avoidance backstop runs on a timer; route it through the injected
  // dependency so the harness owns the cadence and node never keeps a real
  // interval alive past the test.
  const setInterval = (fn, ms) => injectInterval(fn, ms);
  // Mirrors the module-level declaration in index.html;
  // applySidebarCollapsed assigns it — the getter exposes it below.
  let sidebarCollapsed = false;

  ${slices}
  return {
    applyVisualViewportOcclusion,
    reapplyVisualViewportOcclusion,
    initVisualViewportHandling,
    createTerminalHost,
    applySidebarCollapsed,
    isCollapsed: () => sidebarCollapsed,
  };
`);
}

const viewportFactory = buildViewportFactory(inlineScript(indexSource));

function viewportHarness({
  withVisualViewport = true,
  layoutHeight = LAYOUT_HEIGHT,
  hosts = ["a"],
  // Pass { controls: null } / { statusBar: null } to model a page WITHOUT that
  // overlay (login gate, pre-boot). `false` means "omit the element entirely";
  // anything else uses the built stub. A stub handed out unconditionally would
  // make the guarded-lookup branch impossible to reach, so the default is here
  // rather than inside makeDocument.
  overlays = {},
} = {}) {
  const appAttempts = [];
  const root = makeNonScrollableRoot(layoutHeight);
  const inner = {
    terminal: makeInnerScroller("xtermViewport", { scrollTop: 0, writes: {} }),
    list: makeInnerScroller("wtList", { scrollTop: 0, writes: {} }),
  };
  const container = makeTerminalContainer({ top: 0, bottom: layoutHeight - SAFE_BOTTOM });
  const app = makeAppElement(layoutHeight, appAttempts);
  for (const id of hosts) container.addHost(id);
  // #workspace is NOT stubbed as a writable box — it stays a NUMBER, the floor
  // the two overlay children resolve against (bottom = layoutHeight − safe area,
  // the same 682 the hosts bottom out at). A stub could be written by a
  // regression without any case noticing; a number cannot be.
  const workspaceBottom = layoutHeight - SAFE_BOTTOM;
  // `overlays: { controls: false }` omits the element; an absent key builds the
  // stub. Both spellings are accepted because "I did not think about it" and "I
  // removed it on purpose" must not look the same in the option bag.
  const omitControls = "controls" in overlays && !overlays.controls;
  const omitStatusBar = "statusBar" in overlays && !overlays.statusBar;
  const controls = omitControls ? null : makeOverlayChild({
    id: "terminal-controls",
    top: CONTROLS_TOP,
    workspaceBottom,
    authoredBottom: CONTROLS_AUTHORED_BOTTOM,
  });
  const statusBar = omitStatusBar ? null : makeOverlayChild({
    id: "status-bar",
    // v3.1 `position:fixed`: the strip anchors to the VIEWPORT, so its authored
    // bottom (0) sits at the physical bottom (layoutHeight), and its authored top
    // is layoutHeight − height — NOT the safe-area-inset workspace bottom. That is
    // precisely the "Transport reaches the physical bottom" this round delivers.
    top: layoutHeight - STATUS_BAR_HEIGHT,
    workspaceBottom,
    authoredBottom: 0,
    resolveBottom: layoutHeight,
  });
  const document = makeDocument(app, root, container, { controls, statusBar });
  const vv = makeVisualViewport({ height: layoutHeight, offsetTop: 0 });
  const windowEvents = new Map();
  const rafCalls = [];
  const ban = (what) => {
    root.attempts.push(what);
    throw new Error(`the ROOT must never be written — attempted ${what}`);
  };
  const window = {
    // Lifecycle edges are registered here (pageshow / orientationchange) and
    // every one of them must run the AVOIDANCE judgment only.
    addEventListener(type, handler, options) {
      if (!windowEvents.has(type)) windowEvents.set(type, []);
      windowEvents.get(type).push({ handler, options });
    },
    // There is no scroll to perform. Both entry points throw, so the ban on
    // scrollTo is executed by the browser stand-in rather than only grepped.
    scrollTo: () => ban("window.scrollTo(...)"),
    scrollBy: () => ban("window.scrollBy(...)"),
    get scrollY() { return 0; },
    innerHeight: layoutHeight,
    visualViewport: undefined,
  };
  if (withVisualViewport) window.visualViewport = vv;

  const intervalCallbacks = [];
  const api = viewportFactory({
    window,
    document,
    // A fixed shell with no root scroll needs no follow-up layout frame: a
    // request here from the occlusion path is the retired rAF reconciliation
    // coming back.
    requestAnimationFrame: (cb) => { rafCalls.push(cb); return rafCalls.length; },
    console,
    getActiveTerminalSession: () => null,
    sendResize: () => {},
    syncTerminalToAppliedSize: () => {},
    setInterval: (fn) => { intervalCallbacks.push(fn); return intervalCallbacks.length; },
  });
  return {
    api, app, vv, window, document, root, inner, container, layoutHeight,
    classes: app.classes,
    stats: {
      hostWrites: () => container.stats.hostWrites,
      hostWritesFor: (id) => container.stats.perHost[id] || 0,
      appAttempts: appAttempts,
      rootAttempts: root.attempts,
      rafCalls: () => rafCalls.length,
      innerWrites: () => Object.keys(inner.terminal.writes).length + Object.keys(inner.list.writes).length,
      // Overlay writes counted SEPARATELY from the host writes: a case that
      // asserts "the hosts were not rewritten" must stay readable, and a case
      // that asserts the column and the strip were not rewritten needs its own
      // number. Both are guarded writes, so each counts writes actually made.
      controlsWrites: () => (controls ? controls.writes.length : 0),
      statusBarWrites: () => (statusBar ? statusBar.writes.length : 0),
      overlayWrites: () => (controls ? controls.writes.length : 0) + (statusBar ? statusBar.writes.length : 0),
    },
    flushFrames: () => { for (const cb of rafCalls.splice(0)) cb(); },
    appBottom: () => app.getBoundingClientRect().bottom,
    layoutGap: () => document.documentElement.clientHeight - app.getBoundingClientRect().bottom,
    bottoms: () => container.bottoms(),
    hostBottoms: () => container.rects().map((r) => r.bottom),
    controlsBottom: () => (controls ? controls.style.bottom : null),
    statusBarBottom: () => (statusBar ? statusBar.style.bottom : null),
    controlsRect: () => (controls ? controls.getBoundingClientRect() : null),
    statusBarRect: () => (statusBar ? statusBar.getBoundingClientRect() : null),
    // The column's usable height: what #quick-keys-panel's `max-height: 100%`
    // resolves against, so THIS is the number that has to shrink by occlusion.
    controlsHeight: () => (controls ? controls.getBoundingClientRect().height : null),
    // The element the applier actually got for each id, so a case can assert the
    // guarded lookup returned nothing instead of a stub.
    overlayEl: (id) => document.getElementById(id),
    dispatchWindow(type) {
      for (const entry of windowEvents.get(type) ?? []) entry.handler({ type });
    },
    windowListenerCount: (type) => (windowEvents.get(type) ?? []).length,
    // Drives the avoidance backstop without waiting real time.
    tick: () => { for (const fn of intervalCallbacks) fn(); },
    openKeyboard: (visibleHeight) => { vv.height = visibleHeight; vv.dispatch("resize"); },
    closeKeyboard: () => { vv.height = layoutHeight; vv.dispatch("resize"); },
    // A pan: the VISUAL viewport moves (offsetTop), the ROOT has nowhere to go
    // and the occlusion does not depend on it.
    pan: (offsetTop) => { vv.offsetTop = offsetTop; vv.dispatch("scroll"); },
    zoom: (scale) => { vv.scale = scale; vv.dispatch("resize"); },
  };
}

// The occlusion this layout owes at a given visible height — the formula under
// test, spelled out once so every case states the EXPECTATION independently. v3.1
// floors it: a shortfall under SMALL_OCCLUSION_RATIO of the layout height (the thin
// floating strip) is owed NOTHING, so the host / controls / status keep their
// authored bottoms; at or over the ratio (every measured keyboard, 147 and 428 on a
// 702 layout against a 105.3px floor) the shortfall is paid in full.
const SMALL_OCCLUSION_RATIO = 0.15;
const owedPx = (visibleHeight) => {
  const raw = Math.max(0, LAYOUT_HEIGHT - visibleHeight);
  return raw < LAYOUT_HEIGHT * SMALL_OCCLUSION_RATIO ? 0 : raw;
};
const owed = (visibleHeight) => owedPx(visibleHeight) + "px";

// --- R6: graceful degradation -----------------------------------------------

test("issue #100 L1 R6: without visualViewport the init is a no-op — no listeners, no host writes, no throw", () => {
  const h = viewportHarness({ withVisualViewport: false });
  h.api.initVisualViewportHandling();
  assert.equal(h.vv.listenerCount("resize"), 0);
  assert.equal(h.windowListenerCount("pageshow"), 0);
  assert.equal(h.document.listenerCount("visibilitychange"), 0);
  // Nothing is covered, so nothing may move: the hosts keep the inset they were
  // built with and the shell keeps its full height.
  assert.deepEqual(h.bottoms(), [""]);
  assert.equal(h.stats.hostWrites(), 0);
  assert.equal(h.appBottom(), LAYOUT_HEIGHT);
});

// --- v3.1 §1: the occlusion rule, floored at the small-overlay boundary -------

test("v3.1: every keyboard lifts every PTY host's own bottom (274 / 555); only the thin strip is exempt (633.5)", () => {
  const h = viewportHarness({ hosts: ["a"] });
  h.api.initVisualViewportHandling();
  assert.equal(h.appBottom(), LAYOUT_HEIGHT, "full height before the keyboard");

  // The measured table. The floor is 702*0.15 = 105.3px, and BOTH measured keyboards
  // clear it and lift by their raw shortfall — the full soft keyboard (274 → 428) and
  // the 555 keyboard (147 → host bottom 535). Only the 633.5 thin floating strip
  // (68.5 = 9.8%) falls under the floor and is owed NOTHING — the terminal keeps its
  // full height and the strip just overlaps it ("浮窗不压缩任何终端区域"). 555 is
  // stated HERE as an avoiding frame on purpose: at the 0.25 this round first shipped
  // (floor 175.5px) the arithmetic wrongly exempted it, which is the conflict the
  // review caught and the reason the boundary is 0.15.
  const table = [
    [274, "428px", 254],
    [555, "147px", HOST_BOTTOM - 147],
    [633.5, "0px", HOST_BOTTOM],
  ];
  for (const [visible, bottom, hostBottom] of table) {
    h.openKeyboard(visible);
    assert.deepEqual(h.bottoms(), [bottom], `vv.height ${visible} owes bottom ${bottom}`);
    assert.deepEqual(h.hostBottoms(), [hostBottom], `vv.height ${visible} puts the host's real bottom at ${hostBottom}`);
    assert.equal(h.appBottom(), LAYOUT_HEIGHT, `the shell stays FULL HEIGHT at ${LAYOUT_HEIGHT} while vv.height is ${visible}`);
    assert.equal(h.layoutGap(), 0, "layoutGap = clientHeight − app.bottom, and it stays 0");
  }
});

test("v3.1: avoiding states lift controls (20 + occlusion) and the fixed strip (occlusion); the exempt strip stays authored", () => {
  // The other half of the local avoidance, and the half the product asked for: the
  // input and the quick-keys have to stay reachable when a REAL keyboard is up, so
  // #terminal-controls (absolute in a #workspace the keyboard does NOT shorten) and
  // #status-bar (`position:fixed`, anchored to the viewport) get their OWN bottom in
  // the same one pass. The two numbers are NOT the same: the column keeps its
  // authored 20px clearance over the transport strip and adds the occlusion ON TOP
  // of it, while the strip lifts by the occlusion alone. The floor is v3.1's: a
  // shortfall under the ratio (633.5 → raw 68.5 ≈ 9.8%, under the 105.3px floor)
  // owes NOTHING, so the strip stays authored at the PHYSICAL bottom (702) and simply
  // overlaps the terminal; 555 (raw 147 ≈ 20.9%) clears the floor and lifts.
  //   controls bottom = 20 + owed
  //   status   bottom = owed        (fixed, anchored to layoutHeight)
  const h = viewportHarness({ hosts: ["a"] });
  h.api.initVisualViewportHandling();
  assert.equal(h.controlsBottom(), "20px", "at rest the column keeps its authored 20px");
  assert.equal(h.statusBarBottom(), "0px", "and the strip its authored 0");

  const table = [
    // [vv.height, owedOcclusion] — owed = floored occlusion (0 when exempt).
    [274, owedPx(274)],   // 428
    [555, owedPx(555)],   // 147 — above the 105.3 floor, it avoids
    [633.5, owedPx(633.5)], // 0 — under the floor, exempt
  ];
  assert.equal(table[0][1], 428, "274 clears the 702*0.15 = 105.3 floor and owes its full shortfall");
  assert.equal(table[1][1], 147, "555 is 147 = 20.9% > 15% — above the floor, it owes the whole shortfall");
  assert.equal(table[2][1], 0, "633.5 is 68.5 = 9.8% < 15% — under the floor and owes nothing");

  for (const [visible, occlusion] of table) {
    h.openKeyboard(visible);
    assert.deepEqual(h.bottoms(), [occlusion + "px"], `vv.height ${visible}: the host owes ${occlusion}px`);
    assert.equal(h.controlsBottom(), (20 + occlusion) + "px", `vv.height ${visible}: controls bottom ${20 + occlusion} = 20 + ${occlusion}`);
    assert.equal(h.statusBarBottom(), occlusion + "px", `vv.height ${visible}: status bottom ${occlusion} = the occlusion itself`);
    // Geometry: the column is absolute in #workspace (bottom 682), the strip is
    // FIXED to the viewport (bottom 702), so even at occlusion 0 the strip already
    // sits 20px below the column's floor — the safe-area it no longer inherits.
    assert.equal(h.controlsRect().bottom, HOST_BOTTOM - 20 - occlusion, "the column's real bottom lifts by the occlusion");
    assert.equal(h.statusBarRect().bottom, LAYOUT_HEIGHT - occlusion, "the fixed strip's real bottom resolves against the VIEWPORT, not the inset workspace");
    if (occlusion > 0) {
      assert.ok(h.controlsRect().bottom <= visible + 1, "no part of the control column hides under the keyboard");
      assert.ok(h.statusBarRect().bottom <= visible + 1, "no part of the transport strip hides under the keyboard");
    } else {
      // Exempt: the thin floating strip overlaps a full-height surface — the strip
      // is at the physical bottom by design, NOT lifted.
      assert.equal(h.statusBarRect().bottom, LAYOUT_HEIGHT, "exempt strip rests at the physical bottom, overlapped by the floating bar");
    }
    // The strip is fixed (702 anchor) and the column absolute (682 anchor), so the
    // gap between them is authored 20 + the safe-area inset (SAFE_BOTTOM): a fixed
    // strip reaches 20px LOWER than the inset column floor. Recorded, not rewritten.
    assert.equal(h.statusBarRect().bottom - h.controlsRect().bottom, CONTROLS_AUTHORED_BOTTOM + SAFE_BOTTOM, "the fixed strip clears the absolute column by its authored 20 plus the safe-area the strip no longer inherits");
    assert.equal(h.appBottom(), LAYOUT_HEIGHT, `the shell stays FULL HEIGHT while vv.height is ${visible}`);
  }
});

test("v3.0: the quick-keys column's usable height shrinks by exactly the occlusion (panel max-height:100%)", () => {
  // #quick-keys-panel carries `max-height: 100%`, and a percentage resolves
  // against its containing block — #terminal-controls (PRD-CHANGE §3.1: a
  // percentage against an auto-height box is treated as none, which is why the
  // column carries BOTH top and bottom in CSS). So the number that decides
  // whether the key set is reachable is the COLUMN's height, and this case
  // measures it through the keyboard rather than reading the stylesheet: the
  // authored top stays put, the bottom rises, and the height shortens by
  // exactly the occlusion — never by 20, never by 20 + occlusion twice.
  const h = viewportHarness({ hosts: ["a"] });
  h.api.initVisualViewportHandling();
  const fullHeight = h.controlsHeight();
  assert.equal(fullHeight, HOST_BOTTOM - 20 - CONTROLS_AUTHORED_BOTTOM, "at rest: workspace bottom − top 20 − authored bottom 20");

  for (const visible of [274, 555, 633.5]) {
    const occlusion = owedPx(visible);
    h.openKeyboard(visible);
    assert.equal(h.controlsHeight(), fullHeight - occlusion, `vv.height ${visible}: the column is ${occlusion}px shorter (0 when exempt), so the panel's max-height tracks the occlusion it actually owes`);
    assert.equal(h.controlsRect().top, CONTROLS_TOP, "top:20px is never written — the quick-keys ENTRY BUTTON does not move, and never jumps");
    assert.ok(h.controlsHeight() > 0, "the column never collapses to nothing");
  }

  h.closeKeyboard();
  assert.equal(h.controlsHeight(), fullHeight, "closing hands the full usable height back");
});

test("v3.0: occlusion 0 restores controls 20px and status 0px — and repeated calls write nothing more", () => {
  const h = viewportHarness({ hosts: ["a"] });
  h.api.initVisualViewportHandling();
  h.openKeyboard(274);
  const afterOpen = { c: h.stats.controlsWrites(), s: h.stats.statusBarWrites() };
  assert.equal(h.controlsBottom(), "448px");
  assert.equal(h.statusBarBottom(), "428px");

  // Same target, over and over: guarded writes mean the 500ms backstop cannot
  // rewrite an identical value — every extra write would invalidate the layout of
  // a page the user is looking at, and re-fire the host ResizeObserver chain.
  h.tick();
  h.tick();
  h.pan(120);
  h.openKeyboard(274);
  assert.equal(h.stats.controlsWrites(), afterOpen.c, "a stable covered state re-writes nothing on the column");
  assert.equal(h.stats.statusBarWrites(), afterOpen.s, "…nor on the strip");

  // occlusion 0 = the REAL floating overlay / keyboard closed: exactly the
  // authored values come back, and they come back ONCE.
  h.closeKeyboard();
  assert.equal(h.controlsBottom(), "20px", "occlusion 0 restores the authored 20px, not 0px");
  assert.equal(h.statusBarBottom(), "0px", "and the strip's authored 0");
  const afterClose = { c: h.stats.controlsWrites(), s: h.stats.statusBarWrites() };
  h.tick();
  h.tick();
  assert.equal(h.stats.controlsWrites(), afterClose.c, "the restore is one write, then silence");
  assert.equal(h.stats.statusBarWrites(), afterClose.s, "…on both boxes");
  assert.deepEqual(h.bottoms(), ["0px"], "and the hosts are restored in the same breath");
});

test("v3.0: zoom restores controls 20px and status 0px — the opt-out covers all three boxes", () => {
  // Zoom opts out of the avoidance entirely, and "entirely" has to mean every
  // box the avoidance writes. A pinch that left the column at 448px would float
  // the quick-keys entry off the bottom of a page the user is panning around.
  const h = viewportHarness({ hosts: ["a"] });
  h.api.initVisualViewportHandling();
  h.openKeyboard(274);
  assert.equal(h.controlsBottom(), "448px");

  h.zoom(2);
  assert.deepEqual(h.bottoms(), ["0px"], "zoom restores the host bottom");
  assert.equal(h.controlsBottom(), "20px", "zoom restores the column's authored 20px too");
  assert.equal(h.statusBarBottom(), "0px", "…and the strip's 0");

  // Still zoomed, the keyboard's shortfall is NOT owed anything.
  h.vv.height = 274;
  h.vv.dispatch("resize");
  assert.deepEqual(h.bottoms(), ["0px"], "while zoomed, no shortfall is paid");
  assert.equal(h.controlsBottom(), "20px", "…and the column stays authored");
  assert.equal(h.statusBarBottom(), "0px", "…and so does the strip");

  h.zoom(1);
  assert.deepEqual(h.bottoms(), [owed(274)], "back at scale 1 the shortfall is owed again");
  assert.equal(h.controlsBottom(), "448px", "…and the column lifts again");
  assert.equal(h.statusBarBottom(), "428px", "…and the strip lifts again");
});

test("v3.0: a missing controls column or status strip is skipped, never fatal — hosts still avoid", () => {
  // The login gate and any pre-boot page have no #terminal-controls / #status-bar.
  // Their absence must not throw and must not stop the hosts' avoidance: the two
  // lookups are guarded, not assumed. Same for a page that HAS the overlays but
  // no #terminal-container (the container check still returns early — the hosts
  // are the reason the pass exists).
  const h = viewportHarness({ hosts: ["a"], overlays: { controls: null, statusBar: null } });
  h.api.initVisualViewportHandling();
  h.openKeyboard(274);
  assert.deepEqual(h.bottoms(), ["428px"], "the host still avoids with no overlays on the page");
  assert.deepEqual(h.bottoms(), ["428px"]);
  h.tick();
  assert.deepEqual(h.bottoms(), ["428px"], "and the backstop keeps running without them");
});

test("v3.0: #app's inline height is NEVER written — the retired v2.2 pin fails at the write", () => {
  // The setter THROWS, so this case cannot pass by grepping the right shape: any
  // revival of `app.style.height = …` inside the applier explodes here.
  const h = viewportHarness({ hosts: ["a"] });
  h.api.initVisualViewportHandling();
  h.openKeyboard(274);
  h.openKeyboard(555);
  h.openKeyboard(633.5);
  h.closeKeyboard();
  h.tick();
  assert.deepEqual(h.stats.appAttempts, [], "nothing in the v3.0 viewport layer reads or writes #app's height");
});

test("v3.0: full height and keyboard close both hand the bottom back — bottom 0px, host back at 682", () => {
  const h = viewportHarness({ hosts: ["a"] });
  h.api.initVisualViewportHandling();
  assert.deepEqual(h.bottoms(), ["0px"], "the init runs the judgment once: nothing covered, bottom 0px");

  h.openKeyboard(274);
  assert.deepEqual(h.bottoms(), ["428px"]);
  h.closeKeyboard();
  assert.deepEqual(h.bottoms(), ["0px"], "closing hands the bottom back, it does not strand the lift");
  assert.deepEqual(h.hostBottoms(), [HOST_BOTTOM]);

  // A visible viewport that equals the layout height — the REAL floating overlay
  // (session b90643be…, seq388: vv.height = innerHeight = clientHeight = 702) —
  // is occlusion 0 by arithmetic, not by a special case.
  h.openKeyboard(LAYOUT_HEIGHT);
  assert.deepEqual(h.bottoms(), ["0px"]);
});

// --- v3.0 §2: coverage — every host, write-only-on-change, no residue -------

test("v3.0: EVERY host gets the avoidance, hidden and inactive included — no stale bottom on switch", () => {
  const h = viewportHarness({ hosts: ["a", "b", "c"] });
  h.api.initVisualViewportHandling();
  h.openKeyboard(274);
  // All three — including the two the user cannot see — carry the same value.
  assert.deepEqual(h.bottoms(), ["428px", "428px", "428px"]);
  assert.deepEqual(h.hostBottoms(), [254, 254, 254]);

  // A switch: the host the user lands on must already be correct, because the
  // lift was written while it was hidden.
  h.container.host("b").style.display = "block";
  h.container.host("a").style.display = "none";
  assert.deepEqual(h.bottoms(), ["428px", "428px", "428px"], "no residue: a hidden host never keeps an old bottom");

  // And a host BORN under a covered bottom is lifted at creation, not at the
  // next event — the same idempotent applier, called by createTerminalHost.
  h.api.createTerminalHost("d");
  assert.equal(h.container.host("d").style.bottom, "428px", "a host created while the keyboard is up is born lifted");
});

test("v3.0: writes happen ONLY on change — a stable page writes nothing at all", () => {
  const h = viewportHarness({ hosts: ["a", "b"] });
  h.api.initVisualViewportHandling();
  const before = h.stats.hostWrites();
  h.api.applyVisualViewportOcclusion();
  h.api.applyVisualViewportOcclusion();
  h.tick();
  h.tick();
  h.pan(428);
  assert.equal(h.stats.hostWrites(), before, "re-running the judgment on a steady state writes zero styles (no layout churn, no ResizeObserver re-fire)");

  h.openKeyboard(274);
  assert.equal(h.stats.hostWrites(), before + 2, "one write per host, and only for the hosts whose bottom moved");
  const after = h.stats.hostWrites();
  h.tick();
  assert.equal(h.stats.hostWrites(), after, "the backstop is a compare, not a write");
});

// --- v3.0 §3: zoom is exempt ---------------------------------------------------

test("v3.0: zoom restores bottom 0px and touches nothing else", () => {
  const h = viewportHarness({ hosts: ["a"] });
  h.api.initVisualViewportHandling();
  h.openKeyboard(274);
  assert.deepEqual(h.bottoms(), ["428px"]);

  // Pinch: vv.height shrinks with the scale, and without the short-circuit the
  // terminal would be lifted off a page the user is panning around.
  h.zoom(2);
  assert.deepEqual(h.bottoms(), ["0px"], "zoom owns nothing: the terminal returns to its own bottom");
  assert.equal(h.stats.rootAttempts.length, 0, "zoom does not touch the root either");

  // Zoom released with the keyboard STILL up: the judgment is live again at
  // scale 1 and the same shortfall is owed again — zoom suppresses, it does not
  // cancel, the avoidance.
  h.zoom(1);
  assert.deepEqual(h.bottoms(), ["428px"], "back at scale 1 the shortfall is owed again");
  h.closeKeyboard();
  assert.deepEqual(h.bottoms(), ["0px"]);
});

test("v3.0: zoom never lifts even at keyboard-sized visible heights", () => {
  const h = viewportHarness({ hosts: ["a"] });
  h.api.initVisualViewportHandling();
  h.vv.height = 274;
  h.vv.scale = 1.8;
  h.vv.dispatch("resize");
  assert.deepEqual(h.bottoms(), ["0px"]);
});

// --- v3.0 §4: state changes that fire no event -------------------------------

test("v3.1: a stranded lift is cleared by the 500ms tick when the keyboard leaves silently", () => {
  const h = viewportHarness({ hosts: ["a"] });
  h.api.initVisualViewportHandling();
  // A REAL keyboard (274 clears the 105.3 floor), so there IS a lift that could strand.
  h.openKeyboard(274);
  assert.deepEqual(h.bottoms(), ["428px"]);

  // The silent recovery: the geometry moves back and NO event fires (b90643be…,
  // seq54 — inline cleared, overlay gone, vv back at 702, not one resize/scroll
  // event in between). Only the tick can know.
  h.vv.height = LAYOUT_HEIGHT;
  assert.deepEqual(h.bottoms(), ["428px"], "without the tick the terminal stays stranded above a bottom that is visible again");
  h.tick();
  assert.deepEqual(h.bottoms(), ["0px"], "the tick re-runs the judgment and hands the bottom back");
});

test("v3.0: a shortfall that appears silently is paid by the tick and by every lifecycle edge", () => {
  // The registration split the shipped init declares: the WINDOW edges are the
  // ones visualViewport does not already cover (a vv resize fires on a keyboard,
  // a pageshow / orientationchange may not), the DOCUMENT edges are the focus
  // and visibility transitions a floating panel collapse rides on.
  const edges = [
    { where: "window", type: "pageshow" },
    { where: "window", type: "orientationchange" },
    { where: "document", type: "visibilitychange" },
    { where: "document", type: "focusin" },
    { where: "document", type: "focusout" },
  ];
  for (const edge of edges) {
    const h = viewportHarness({ hosts: ["a"] });
    h.api.initVisualViewportHandling();
    h.vv.height = 274; // no resize event: the edge alone must run the judgment
    if (edge.where === "window") h.dispatchWindow(edge.type);
    else h.document.dispatch(edge.type);
    assert.deepEqual(h.bottoms(), ["428px"], `${edge.where} ${edge.type} must run the avoidance itself`);
  }
  const h = viewportHarness({ hosts: ["a"] });
  h.api.initVisualViewportHandling();
  assert.equal(h.windowListenerCount("pageshow"), 1);
  assert.equal(h.windowListenerCount("orientationchange"), 1);
  assert.equal(h.document.listenerCount("visibilitychange"), 1);
  assert.equal(h.document.listenerCount("focusin"), 1);
  assert.equal(h.document.listenerCount("focusout"), 1);
  assert.equal(h.vv.listenerCount("resize"), 1);
  assert.equal(h.vv.listenerCount("scroll"), 1);
  h.vv.height = 274;
  h.tick();
  assert.deepEqual(h.bottoms(), ["428px"], "the periodic tick runs the avoidance too");
});

test("v3.0: vv.offsetTop is not an input — a pan cannot change or re-write the avoidance", () => {
  const h = viewportHarness({ hosts: ["a"] });
  h.api.initVisualViewportHandling();
  h.openKeyboard(274);
  const before = h.stats.hostWrites();

  // iOS pans the visual viewport; the occlusion is client-space vv.height
  // against the stable layout height, so the target does not move and — because
  // writes are guarded — nothing is written at all.
  h.pan(428);
  assert.deepEqual(h.bottoms(), ["428px"]);
  assert.equal(h.stats.hostWrites(), before, "a pan must not rewrite a style");
  h.pan(0);
  assert.equal(h.stats.hostWrites(), before);
});

// --- v3.0 §5: the root is never written, and inner scrollers stay live ------

test("v3.0: every entry point runs the avoidance WITHOUT writing the root or the shell", () => {
  const h = viewportHarness({ hosts: ["a"] });
  h.api.initVisualViewportHandling();
  h.openKeyboard(274);
  h.pan(300);
  h.dispatchWindow("pageshow");
  h.dispatchWindow("orientationchange");
  h.vv.dispatch("resize");
  h.document.dispatch("visibilitychange");
  h.document.dispatch("focusin");
  h.document.dispatch("focusout");
  h.tick();
  assert.deepEqual(h.stats.rootAttempts, [], "no root scroll write on any path — the box throws, so this is executed");
  assert.equal(h.appBottom(), LAYOUT_HEIGHT);
});

test("v3.0: inner scrollers stay scrollable and are never written (A0 step S8)", () => {
  const h = viewportHarness({ hosts: ["a"] });
  h.api.initVisualViewportHandling();
  h.openKeyboard(274);
  h.inner.terminal.scrollTop = 400;
  h.inner.list.scrollTop = 120;
  h.tick();
  assert.equal(h.inner.terminal.scrollTop, 400, "the terminal's own scrollback stays where the user put it");
  assert.equal(h.inner.list.scrollTop, 120);
  assert.equal(h.stats.innerWrites(), 2, "only the user wrote them — the viewport layer wrote none");
});

test("v3.0: the avoidance path never requests an animation frame", () => {
  const h = viewportHarness({ hosts: ["a"] });
  h.api.initVisualViewportHandling();
  h.openKeyboard(274);
  h.tick();
  h.pan(200);
  assert.equal(h.stats.rafCalls(), 0, "no scroll to follow, so no follow-up frame — the retired rAF reconciliation must not return");
});

// --- R12 x PR1: collapse and keyboard at the same time ----------------------

test("issue #100 L1 R12: collapse + keyboard coexist — the class and the host bottom never fight", () => {
  const h = viewportHarness({ hosts: ["a"] });
  h.api.initVisualViewportHandling();

  h.api.applySidebarCollapsed(true);
  assert.deepEqual([...h.classes], ["collapsed"]);
  h.openKeyboard(300);
  assert.deepEqual(h.bottoms(), ["402px"]);
  assert.deepEqual([...h.classes], ["collapsed"], "the viewport handler owns the host bottom only — the class stays");

  h.flushFrames();
  h.api.applySidebarCollapsed(false);
  assert.deepEqual([...h.classes], []);
  assert.deepEqual(h.bottoms(), ["402px"], "expanding does not undo the keyboard avoidance");

  h.closeKeyboard();
  assert.deepEqual(h.bottoms(), ["0px"]);
  assert.deepEqual([...h.classes], []);
});

// --- STRUCTURE: the contract lives in CSS, so the anchors read CSS ---------

test("v3.0 structure: #app leaves the normal flow AND stays full height", () => {
  // Two `#app {` rules exist: the shell itself and the @supports dvh upgrade.
  // The positioning contract is the one that declares `position`.
  const rules = cssRules(styleBlock(indexSource), /#app(?![\w.#-])/);
  const shell = rules.find((r) => /position:/.test(r));
  assert.ok(shell, "the #app rule that carries the positioning contract is gone");
  assert.match(shell, /position:\s*fixed/, "#app must be out of flow — the structural half of 'the root cannot scroll'");
  assert.match(shell, /top:\s*0/, "an out-of-flow box needs its anchor; it is the viewport origin");
  assert.match(shell, /left:\s*0/, "…on both axes");
  assert.match(shell, /width:\s*100%/, "width stays 100% of the viewport it is fixed to");
  assert.equal(/width:\s*100vw/.test(shell), false, "100vw counts the scrollbar gutter and overflows horizontally (issue #100 L1 R5)");
  // FULL HEIGHT is now the contract: a static viewport basis, never a JS pin.
  assert.match(shell, /height:\s*100vh/, "the legacy full-height basis survives the move");
  assert.match(shell, /env\(safe-area-inset-/, "safe-area padding stays on the shell (issue #100 L1 R5)");
  const heights = rules.flatMap((r) => r.match(/height:\s*[^;]+/g) || []);
  assert.deepEqual(heights, ["height: 100vh", "height: 100dvh"], "100vh first, then the @supports dvh upgrade — swapped, modern browsers silently stay static (issue #100 L1 R5)");
  for (const height of heights) {
    assert.equal(/vv\.height|clientHeight|var\(--vv/.test(height), false, `the shell's height must be a static basis, not a viewport measurement: ${height}`);
  }
});

test("v3.0 structure: html/body declare hidden FIRST and clip SECOND", () => {
  const rule = cssRules(styleBlock(indexSource), /body,\s*html/).at(-1);
  const hidden = rule.search(/overflow:\s*hidden/);
  const clip = rule.search(/overflow:\s*clip/);
  assert.ok(hidden >= 0, "the legacy floor (overflow:hidden) must stay for engines that never learned clip");
  assert.ok(clip >= 0, "overflow:clip is the answer under test — without it the root is still scrollable, and the reference app's hidden bottom strip would become reachable");
  assert.ok(hidden < clip, "source order is the mechanism: last-declaration-wins, so clip must come AFTER hidden — swapped, every modern browser gets its scrollable root back");
  assert.match(rule, /overscroll-behavior:\s*none/, "the iOS rubber-band lock stays (issue #100 padOS②)");
  assert.match(rule, /height:\s*100%/, "the root boxes keep their height declaration — the contract does not resize them");
  assert.equal(/position:/.test(rule), false, "body/html must NOT be positioned — the body-fixed combination was measured and reverted (2ac6c83)");
});

test("v3.0 structure: the covered strip is PAINT, not a scroll area — workspace keeps its ink and its bleed", () => {
  // v3.0 never shortens #workspace / #terminal-container / #app, so the strip
  // iOS covers with the keyboard is still there under the keyboard and must be
  // the app's own ink rather than a white band. A 100vh column bleed is still
  // banned: the shell no longer moves, so the bleed is exactly the safe-area
  // strip it always was.
  const css = styleBlock(indexSource);
  for (const [name, selector] of [["#sidebar", /#sidebar\b/], ["#workspace", /#workspace\b/]]) {
    const rules = cssRules(css, selector);
    const bleeds = rules.flatMap((r) => r.match(/box-shadow:[^;]+/g) || []);
    assert.ok(bleeds.length >= 1, `${name} must carry its downward bleed`);
    for (const b of bleeds) {
      assert.equal(/100vh|100dvh/.test(b), false, `${name} bleed must be env(safe-area-inset-bottom), not a viewport height: ${b}`);
      assert.match(b, /env\(safe-area-inset-bottom/, `${name} bleed must stay safe-area sized: ${b}`);
    }
  }
  const workspace = cssRules(css, /#workspace\b/).join("\n");
  assert.match(workspace, /background:\s*var\(--term-bg\)/, "#workspace paints the terminal ink, so the area under a lifted host is a column of the app, not the UA canvas");
  assert.match(workspace, /overflow:\s*hidden/, "#workspace clips its own children and owns no scroll");
});

test("v3.0 structure: the PTY hosts are absolute inset:0 in BOTH factories — the containing block the bottom resolves against", () => {
  // The avoidance writes each host's OWN bottom, so the hosts must stay
  // position:absolute; inset:0 inside #terminal-container, in the legacy factory
  // here AND in the live renderer. If either changes, the written longhand stops
  // being the last word on that box and the avoidance silently stops working.
  const legacy = sliceBlock(inlineScript(indexSource), "function createTerminalHost");
  const live = sliceBlock(ptySource, "_createSession(id, termContainer) {");
  for (const [name, body] of [["index.html createTerminalHost", legacy], ["kinds/pty.js _createSession", live]]) {
    assert.match(body, /position:absolute;\s*inset:0/, `${name} must create the host as an absolutely positioned inset:0 box`);
    assert.match(body, /dataset\.instanceId\s*=/, `${name} must stamp data-instance-id — the selector the avoidance queries`);
  }
  const container = cssRules(styleBlock(indexSource), /#terminal-container\b/).join("\n");
  assert.match(container, /position:\s*relative/, "#terminal-container is the containing block every host's bottom resolves against");
  assert.equal(/height:\s*calc\(/.test(container), false, "#terminal-container must never be shortened by the keyboard");
});

test("v3.0 structure: the viewport layer writes the TERMINAL'S OWN bottoms and NOTHING else", () => {
  const src = inlineScript(indexSource);
  for (const header of ["function applyVisualViewportOcclusion", "function reapplyVisualViewportOcclusion", "function initVisualViewportHandling"]) {
    const body = stripComments(sliceBlock(src, header));
    for (const banned of [/app\.style\.height/, /documentElement\.style/, /document\.body\.style/, /documentElement\.height/, /scrollTop\s*=[^=]/, /scrollTo\s*\(/, /scrollBy\s*\(/, /translate[YX]?\(/, /#workspace/, /terminalContainer\.style/, /container\.style/, /controls\.style\.top/, /statusBar\.style\.top/, /workspace\.style/]) {
      assert.equal(banned.test(body), false, `${header} must not resize the shell, the workspace or the root, and must not scroll — matched ${banned}`);
    }
  }
  // The write the judgment owns, and its TARGET SET. Three boxes, all the
  // terminal's own, all `bottom` longhands on boxes whose containing block the
  // keyboard does NOT shorten: the hosts inside #terminal-container, the
  // controls column and the transport strip inside #workspace. Nothing outside
  // that set may be assigned — that is what keeps the shell full height while
  // quick-keys and the input stay reachable, so the set is asserted EXACTLY
  // (deepEqual, not "contains"), and a fourth target fails just as hard as a
  // missing one.
  const apply = stripComments(sliceBlock(src, "function applyVisualViewportOcclusion"));
  assert.match(apply, /style\.bottom\s*=\s*target/, "the hosts' own bottom is written from the occlusion");
  assert.match(apply, /Math\.max\(0,\s*layoutHeight\s*-\s*vv\.height\)/, "occlusion = max(0, clientHeight − vv.height)");
  assert.match(apply, /const CONTROLS_BOTTOM_OFFSET = 20;/, "the column's authored 20px clearance is stated as a named constant, agrees with the CSS rule, and is not a threshold");
  assert.match(apply, /const controlsTarget = \(CONTROLS_BOTTOM_OFFSET \+ occlusion\) \+ "px";/, "controls bottom = 20 + occlusion, so the authored clearance survives the lift");
  assert.match(apply, /controls\.style\.bottom = controlsTarget/, "the controls column is lifted by the same pass");
  assert.match(apply, /statusBar\.style\.bottom = target/, "the transport strip is lifted by the occlusion itself (authored bottom 0)");
  const targets = new Set((apply.match(/[\w.[\]]+\.style\.\w+\s*=/g) || []));
  assert.deepEqual(
    [...targets].sort(),
    ["controls.style.bottom =", "hosts[i].style.bottom =", "statusBar.style.bottom ="],
    `the terminal's own three bottoms are the only assignment targets in the judgment, got ${[...targets].join(", ")}`,
  );
});

test("v3.0 structure: the retired pin and the retired root-origin machinery are gone, not renamed", () => {
  const src = stripComments(inlineScript(indexSource));
  for (const gone of [
    "applyVisualViewportHeight",
    "reapplyPinnedVisualViewportHeight",
    "resetRootScrollOrigin",
    "reconcileViewportShell",
    "SHELL_RECONCILE_MAX_FRAMES",
    "pendingShellReconcileFrame",
    "shellReconcileFramesLeft",
    "HARD_OCCLUSION_RATIO",
  ]) {
    assert.equal(src.includes(gone), false, `${gone} must be deleted from the shipped code, not left disabled`);
  }
  assert.equal(/window\.addEventListener\(\s*["']scroll["']/.test(src), false, "no root/window scroll listener may be registered by this build — the origin is not chased any more");
  // The retired v2 gate is banned by its STRUCTURE, not by a numeral. It compared the
  // VISUAL HEIGHT against a fraction of the layout height ("a short keyboard does not
  // get to lift anything") — that is the cliff that left a real keyboard sitting on
  // the terminal, and it must never come back in any spelling. The numeral 0.15 is
  // legitimate on this build: it is SMALL_OCCLUSION_RATIO, a floor applied to the
  // SHORTFALL in the opposite direction (a shallow shortfall moves nothing). A test
  // that banned the literal would only prove it cannot tell the two apart. The floor
  // is 0.15 and not the 0.25 this round first shipped: 0.25 put the 702-layout floor
  // at 175.5px, ABOVE the measured 147px keyboard, and exempted a real keyboard.
  assert.equal(/layoutHeight\s*\*\s*\(\s*1\s*-/.test(src), false, "the retired (1 − ratio) cliff must not survive — it refused to lift SHORT keyboards");
  assert.equal(/vv\.height\s*<=?[^;{]*\*/.test(src), false, "vv.height may never be compared against a fraction of the layout height — that is the retired occlusion classification");
  assert.equal(/isHardOcclusion|covered\s*>=/.test(src), false, "the retired hard/soft occlusion classification must not return under its old names");
  assert.equal(/occlusion\s*<\s*layoutHeight\s*\*\s*SMALL_OCCLUSION_RATIO/.test(src), true, "the only surviving boundary is the SHORTFALL floor, spelled exactly like this");
  assert.equal(src.includes("visibleBottom"), false, "the retired pan-based occlusion arithmetic must not come back under its old name");
});

test("v3.0 structure: every listener runs the same avoidance judgment, by name", () => {
  const src = inlineScript(indexSource);
  const init = sliceBlock(src, "function initVisualViewportHandling");
  assert.ok(init.includes(`vv.addEventListener("resize", applyVisualViewportOcclusion)`), "resize runs the judgment");
  assert.ok(init.includes(`vv.addEventListener("scroll", reapplyVisualViewportOcclusion)`), "visualViewport scroll keeps its addressable anchor");
  assert.ok(init.includes(`window.addEventListener(type, applyVisualViewportOcclusion)`), "the window lifecycle edges run the judgment itself");
  assert.ok(init.includes(`document.addEventListener(type, applyVisualViewportOcclusion)`), "and so do the document lifecycle edges");
  assert.ok(init.includes(`applyVisualViewportOcclusion()`), "the init itself runs it once");
  assert.ok(/setInterval\(\(\)\s*=>\s*\{[^}]*applyVisualViewportOcclusion\(\)/.test(init), "the 500ms backstop re-runs the judgment, not a height pin");
  const reapply = stripComments(sliceBlock(src, "function reapplyVisualViewportOcclusion"));
  assert.equal(reapply.match(/(?<!re)applyVisualViewportOcclusion\(\)/g)?.length, 1, "the anchor does exactly one thing: re-run the judgment");
});
