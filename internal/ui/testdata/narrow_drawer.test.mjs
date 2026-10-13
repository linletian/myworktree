// Behavioral tests for the issue #100 L2 breakpoint / drawer layer (PR3 of
// docs/plans/feature-v0.6.0-webui-sidebar-responsive/): the collapsed state
// keeps its single-source form — one boolean, one `.collapsed` class on
// #app, one localStorage key mw.ui.sidebarCollapsed (plan §3.1) — and PR3
// adds only (a) the R14 viewport-dispatched DEFAULT for a missing or
// unparseable persisted value (narrow screens default to the closed drawer
// so a first visit does not cover the terminal; an explicit persisted value
// always wins, which is exactly why a desktop-collapsed user lands on the
// closed drawer on a phone — §5.3-5 needs no special case), and (b) the R7
// mask's close path, which must run through the SAME apply+persist wiring
// as the toggle button and Ctrl/Cmd+B — never a second state variable.
//
// The CSS side (drawer positioning, mask display gating, the breakpoint
// boundaries themselves) is layout, not behavior: it is pinned by the
// served-asset text anchors in TestNarrowDrawerAnchors and by the manual
// checklist per the plan's UA-hint principle, not here.
//
// Same arrangement as sidebar_collapse.test.mjs: the functions are sliced
// out of the real served index.html and evaluated against stubbed browser
// state (localStorage / document / window.matchMedia / rAF), so a drift
// between the slice anchors and index.html fails loudly at slice time. Run:
// node --test testdata/narrow_drawer.test.mjs
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

// Slices a single `const X = ...;` statement up to its terminating semicolon.
function sliceStatement(src, header) {
  const start = src.indexOf(header);
  assert.notEqual(start, -1, `source no longer contains ${JSON.stringify(header)}`);
  const end = src.indexOf(";", start);
  assert.notEqual(end, -1, `no statement terminator after ${JSON.stringify(header)}`);
  return src.slice(start, end + 1);
}

// --- index.html harness -----------------------------------------------------

// The sliced functions close over the stubbed browser globals provided via
// deps; the real wiring between them (mask click -> apply + persist, keydown
// -> toggle) is what is under test, so ALL of it is sliced from the shipped
// script rather than re-declared here.
const drawerFactory = new Function("deps", `
  const {
    window, document, localStorage, requestAnimationFrame, console,
    getActiveTerminalSession, sendResize, syncTerminalToAppliedSize,
  } = deps;
  ${sliceStatement(inlineScript(indexSource), "const SIDEBAR_COLLAPSED_KEY")}
  // Mirrors the module-level declaration in index.html;
  // applySidebarCollapsed assigns it — the getter exposes it below.
  let sidebarCollapsed = false;
  ${[
    "function readSidebarCollapsed",
    "function defaultSidebarCollapsedByViewport",
    "function persistSidebarCollapsed",
    "function refitActiveTerminal",
    "function applySidebarCollapsed",
    "function toggleSidebarCollapse",
    // R7: the mask's shipped close path — the case below drives it against
    // the shipped apply/persist wiring instead of re-declaring a copy.
    "function closeSidebarDrawer",
    // Sliced so the sharing case can prove the mask, the button and the
    // shortcut all flip the ONE class / ONE key.
    "function handleSidebarToggleKeydown",
  ].map(h => sliceBlock(inlineScript(indexSource), h)).join("\n\n")}
  return {
    SIDEBAR_COLLAPSED_KEY,
    readSidebarCollapsed,
    persistSidebarCollapsed,
    applySidebarCollapsed,
    toggleSidebarCollapse,
    closeSidebarDrawer,
    handleSidebarToggleKeydown,
    isCollapsed: () => sidebarCollapsed,
  };
`);

function makeLocalStorage({ throwing = false } = {}) {
  const map = new Map();
  return {
    map,
    getItem(k) { if (throwing) throw new Error("SecurityError: denied"); return map.has(k) ? map.get(k) : null; },
    setItem(k, v) { if (throwing) throw new Error("SecurityError: denied"); map.set(k, String(v)); },
  };
}

// window.matchMedia stub: records the queries it receives (the R14 contract
// names the exact breakpoint expression) and answers `narrow` for them.
function makeWindow({ narrow = false, noMatchMedia = false, throwing = false } = {}) {
  const queries = [];
  if (noMatchMedia) return { queries };
  return {
    queries,
    matchMedia(mq) {
      queries.push(mq);
      if (throwing) throw new Error("matchMedia exploded");
      return { matches: narrow, media: mq };
    },
  };
}

// Attribute-model element stub, same rationale as sidebar_collapse.test.mjs:
// the shipped toggle flips the chevrons with toggleAttribute("hidden")
// because <svg> owns no hidden IDL property.
function makeElement({ classes = [], attrs = [], attrValues: initialValues = [] } = {}) {
  const cls = new Set(classes);
  const attrSet = new Set(attrs);
  const attrValues = new Map(initialValues.map(([k, v]) => [k, String(v)]));
  return {
    classList: {
      add: (c) => cls.add(c),
      remove: (c) => cls.delete(c),
      contains: (c) => cls.has(c),
    },
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

function makeDocument() {
  const classes = new Set();
  // Initial markup state: the left "<<" chevron has no hidden attribute,
  // the right ">>" one ships with the content attribute set.
  const elements = {
    "sidebar-toggle-icon-left": makeElement(),
    "sidebar-toggle-icon-right": makeElement({ attrs: ["hidden"] }),
    "sidebar-toggle-btn": makeElement({ attrValues: [["aria-expanded", "true"]] }),
  };
  return {
    classes,
    elements,
    getElementById(id) {
      if (id === "app") {
        return { classList: { toggle: (cls, on) => { if (on) classes.add(cls); else classes.delete(cls); } } };
      }
      return elements[id] ?? null;
    },
  };
}

function drawerHarness({ throwingStorage = false, storageMap = null, window: windowStub } = {}) {
  const localStorage = makeLocalStorage({ throwing: throwingStorage });
  if (storageMap) {
    for (const [k, v] of storageMap) localStorage.map.set(k, v);
  }
  const document = makeDocument();
  const rafCallbacks = [];
  const api = drawerFactory({
    window: windowStub ?? makeWindow(),
    document,
    localStorage,
    requestAnimationFrame: (cb) => { rafCallbacks.push(cb); return rafCallbacks.length; },
    console,
    getActiveTerminalSession: () => null,
    sendResize: () => {},
    syncTerminalToAppliedSize: () => {},
  });
  return { api, localStorage, document, windowStub: windowStub ?? makeWindow() };
}

function keydownEvent({ key = "b", ctrlKey = false, metaKey = false, altKey = false, repeat = false, target = null } = {}) {
  return {
    key, ctrlKey, metaKey, altKey, repeat,
    target: target ?? { closest: () => null },
    prevented: false,
    preventDefault() { this.prevented = true; },
  };
}

// --- R14: the viewport-dispatched default -----------------------------------

test("issue #100 L2 R14: no persisted value + narrow viewport defaults to the closed drawer (collapsed=true)", () => {
  const windowStub = makeWindow({ narrow: true });
  const h = drawerHarness({ window: windowStub });
  assert.equal(h.api.readSidebarCollapsed(), true, "a first visit on a narrow screen must not cover the terminal");
  assert.equal(h.localStorage.map.size, 0, "reading the default must not write");
  assert.deepEqual(windowStub.queries, ["(max-width: 768px)"], "the dispatch consults the narrow-tier breakpoint expression");
});

test("issue #100 L2 R14: no persisted value + wide viewport defaults to expanded (collapsed=false)", () => {
  const windowStub = makeWindow({ narrow: false });
  const h = drawerHarness({ window: windowStub });
  assert.equal(h.api.readSidebarCollapsed(), false, "a first visit on a wide screen keeps the sidebar expanded");
  assert.deepEqual(windowStub.queries, ["(max-width: 768px)"]);
});

test("issue #100 L2 R14 / §5.3-5: an explicit persisted value beats the viewport in both directions (shared key, no special case)", () => {
  // The §5.3-5 scenario in its literal form: the user collapsed the sidebar
  // on desktop (key "true"), the same browser profile now loads on a phone
  // — the drawer must come up closed. It falls out of the shared key for
  // free: readSidebarCollapsed returns the parsed value without ever
  // consulting matchMedia (asserted via the empty query log).
  for (const narrow of [true, false]) {
    const windowStub = makeWindow({ narrow });
    const h = drawerHarness({ window: windowStub, storageMap: [["mw.ui.sidebarCollapsed", "true"]] });
    assert.equal(h.api.readSidebarCollapsed(), true, `persisted true wins on a ${narrow ? "narrow" : "wide"} viewport`);
    assert.deepEqual(windowStub.queries, [], "an explicit value must never consult the viewport");
  }
  for (const narrow of [true, false]) {
    const windowStub = makeWindow({ narrow });
    const h = drawerHarness({ window: windowStub, storageMap: [["mw.ui.sidebarCollapsed", "false"]] });
    assert.equal(h.api.readSidebarCollapsed(), false, `persisted false wins on a ${narrow ? "narrow" : "wide"} viewport`);
    assert.deepEqual(windowStub.queries, [], "an explicit value must never consult the viewport");
  }
});

test("issue #100 L2 R14: an unparseable stored value degrades to the viewport-dispatched default", () => {
  // Genuinely UNPARSEABLE values (JSON.parse throws) — a parseable
  // non-boolean like "1" or "\"yes\"" is an explicit value that answers
  // expanded without consulting the viewport (pinned in
  // sidebar_collapse.test.mjs).
  for (const garbage of ["{not json", "[broken"]) {
    const narrow = makeWindow({ narrow: true });
    const hNarrow = drawerHarness({ window: narrow, storageMap: [["mw.ui.sidebarCollapsed", garbage]] });
    assert.equal(hNarrow.api.readSidebarCollapsed(), true, `stored ${JSON.stringify(garbage)} on a narrow viewport reads as the closed drawer`);

    const wide = makeWindow({ narrow: false });
    const hWide = drawerHarness({ window: wide, storageMap: [["mw.ui.sidebarCollapsed", garbage]] });
    assert.equal(hWide.api.readSidebarCollapsed(), false, `stored ${JSON.stringify(garbage)} on a wide viewport reads as expanded`);
  }
});

test("issue #100 L2 R14: a host without matchMedia degrades to the wide default (expanded)", () => {
  const windowStub = makeWindow({ noMatchMedia: true });
  const h = drawerHarness({ window: windowStub });
  assert.equal(h.api.readSidebarCollapsed(), false, "older browsers / non-browser hosts must not crash and must not close the drawer by default");
});

test("issue #100 L2 R14: a throwing matchMedia degrades to the wide default (expanded)", () => {
  const windowStub = makeWindow({ narrow: true, throwing: true });
  const h = drawerHarness({ window: windowStub });
  assert.equal(h.api.readSidebarCollapsed(), false, "a throwing matchMedia must not close the drawer by default");
});

test("issue #100 L2 R14: throwing storage (private mode) dispatches by viewport too, without crashing", () => {
  const windowStub = makeWindow({ narrow: true });
  const h = drawerHarness({ window: windowStub, throwingStorage: true });
  assert.equal(h.api.readSidebarCollapsed(), true, "unreadable storage on a narrow viewport answers the closed drawer");
  assert.doesNotThrow(() => h.api.persistSidebarCollapsed(true), "persisting must not throw");
});

// --- R7: the mask closes through the single shared state path ---------------

test("issue #100 L2 R7: a mask click closes the drawer via applySidebarCollapsed(true) + persist, on the one shared class and key", () => {
  const h = drawerHarness();
  const btn = h.document.elements["sidebar-toggle-btn"];
  const left = h.document.elements["sidebar-toggle-icon-left"];
  const right = h.document.elements["sidebar-toggle-icon-right"];

  // Drawer starts open (expanded): no class, "<<" showing.
  h.api.applySidebarCollapsed(false);
  assert.deepEqual([...h.document.classes], []);

  h.api.closeSidebarDrawer();
  assert.deepEqual([...h.document.classes], ["collapsed"], "the mask lands the same .collapsed class as every other close path");
  assert.equal(h.api.isCollapsed(), true, "the single boolean state moved — no second drawer state exists");
  assert.equal(h.localStorage.map.get("mw.ui.sidebarCollapsed"), "true", "the close persists under the shared key");
  assert.equal(btn.getAttribute("aria-expanded"), "false", "the toggle button reports the folded-away drawer");
  assert.equal(left.hasAttribute("hidden"), true, "<< hides once the drawer is closed");
  assert.equal(right.hasAttribute("hidden"), false, ">> shows once the drawer is closed");
});

test("issue #100 L2 R7 / §3.1: mask close, button toggle and Ctrl/Cmd+B all drive the same one class and key", () => {
  const h = drawerHarness();

  // Open -> mask closes.
  h.api.closeSidebarDrawer();
  assert.deepEqual([...h.document.classes], ["collapsed"]);
  assert.equal(h.localStorage.map.get("mw.ui.sidebarCollapsed"), "true");

  // The button path reopens the SAME state.
  h.api.toggleSidebarCollapse();
  assert.deepEqual([...h.document.classes], [], "the button reopen cleared the same class");
  assert.equal(h.api.isCollapsed(), false);
  assert.equal(h.localStorage.map.get("mw.ui.sidebarCollapsed"), "false");

  // And so does the shortcut — still one class, one key.
  const ev = keydownEvent({ key: "b", ctrlKey: true });
  h.api.handleSidebarToggleKeydown(ev);
  assert.deepEqual([...h.document.classes], ["collapsed"]);
  assert.equal(h.localStorage.map.size, 1, "exactly one entry — mw.ui.sidebarCollapsed — ever exists");
});
