// Behavioral tests for the desktop sidebar collapse / expand feature
// (issue #99, PR1 of docs/plans/feature-v0.6.0-webui-sidebar-responsive/):
// the collapsed state is a `.collapsed` class on #app (R1), persisted under
// the namespaced localStorage key mw.ui.sidebarCollapsed (R3), toggled from
// a button hosted in #header (R2) or the first document-level Ctrl/Cmd+B
// keydown (R4), and refits the active xterm after the class flip (R12).
//
// The logic under test lives in the web root (index.html's inline script),
// so this file lives in testdata/ instead of static/ on purpose: static/*
// is embedded and served to every browser, and test code does not belong in
// the web root.
//
// The functions are sliced out of the real sources and evaluated against
// stubbed browser state (localStorage / document / rAF), so the assertions
// run against the shipped code rather than a copy of it — a drift between
// the slice anchors and index.html fails loudly at slice time. Run:
// node --test testdata/sidebar_collapse.test.mjs
// (also wired into terminal_status_test.go for `go test`).
import test from "node:test";
import assert from "node:assert/strict";
import { readFileSync } from "node:fs";
import { fileURLToPath } from "node:url";
import { dirname, join } from "node:path";

const here = dirname(fileURLToPath(import.meta.url));
const staticDir = join(here, "..", "static");
const indexSource = readFileSync(join(staticDir, "index.html"), "utf8");

// --- source slicing helpers (same approach as terminal_status.test.mjs) ---

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
// deps; the real wiring between them (toggle -> apply + persist, keydown ->
// toggle) is what is under test, so ALL of it is sliced from the shipped
// script rather than re-declared here.
const sidebarFactory = new Function("deps", `
  const {
    document, localStorage, requestAnimationFrame, console,
    getActiveTerminalSession, sendResize, syncTerminalToAppliedSize,
    renderGitChanges,
  } = deps;
  ${sliceStatement(inlineScript(indexSource), "const SIDEBAR_COLLAPSED_KEY")}
  // Mirrors the module-level declarations in index.html;
  // applySidebarCollapsed assigns sidebarCollapsed and toggleGitSection
  // assigns gitExpandedSection — the getters below expose both so the
  // orthogonality case can drive the real wiring of each state.
  let sidebarCollapsed = false;
  let gitExpandedSection = 'unstaged';
  ${[
    "function readSidebarCollapsed",
    // PR3 (R14): readSidebarCollapsed delegates the missing/garbage default
    // to this viewport dispatcher — sliced alongside so the shipped wiring
    // is what runs. This harness passes no `window`, and the shipped
    // guard (`typeof window === "undefined"`) degrades to the wide-screen
    // default, which is what these #99 cases assert.
    "function defaultSidebarCollapsedByViewport",
    "function persistSidebarCollapsed",
    "function refitActiveTerminal",
    "function applySidebarCollapsed",
    "function toggleSidebarCollapse",
    "function handleSidebarToggleKeydown",
    // R13: sliced so the behaviour case drives the SHIPPED accordion
    // toggle against the SHIPPED collapse toggle. Its only collaborators
    // are the gitExpandedSection variable above, document.getElementById
    // and renderGitChanges — all provided here, so the slice is cheap.
    "function toggleGitSection",
  ].map(h => sliceBlock(inlineScript(indexSource), h)).join("\n\n")}
  return {
    SIDEBAR_COLLAPSED_KEY,
    readSidebarCollapsed,
    persistSidebarCollapsed,
    applySidebarCollapsed,
    toggleSidebarCollapse,
    handleSidebarToggleKeydown,
    toggleGitSection,
    isCollapsed: () => sidebarCollapsed,
    getGitExpandedSection: () => gitExpandedSection,
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

// Attribute-model element stub: plain-object expandos cannot stand in for
// DOM properties here — the shipped toggle uses toggleAttribute("hidden")
// because <svg> has no hidden IDL property, and a stub that accepted
// `.hidden = x` would have let that bug pass (it did, in PR1 review).
// Attribute and class state are therefore kept in Sets behind the same
// Element API the browser exposes.
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
  // the right ">>" one ships with the content attribute set, and the
  // unstaged git section starts expanded.
  const elements = {
    "sidebar-toggle-icon-left": makeElement(),
    "sidebar-toggle-icon-right": makeElement({ attrs: ["hidden"] }),
    "sidebar-toggle-btn": makeElement({ attrValues: [["aria-expanded", "true"]] }),
    "git-staged-section": makeElement(),
    "git-unstaged-section": makeElement({ classes: ["expanded"] }),
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

function sidebarHarness({ throwingStorage = false, storageMap = null } = {}) {
  const localStorage = makeLocalStorage({ throwing: throwingStorage });
  if (storageMap) {
    for (const [k, v] of storageMap) localStorage.map.set(k, v);
  }
  const document = makeDocument();
  const rafCallbacks = [];
  const resizeCalls = [];
  const syncCalls = [];
  const gitRenderCalls = [];
  let activeSession = null;
  const api = sidebarFactory({
    document,
    localStorage,
    requestAnimationFrame: (cb) => { rafCallbacks.push(cb); return rafCallbacks.length; },
    console,
    getActiveTerminalSession: () => activeSession,
    sendResize: (s, cols, rows) => resizeCalls.push({ id: s.id, cols, rows }),
    syncTerminalToAppliedSize: (s) => syncCalls.push(s.id),
    renderGitChanges: () => { gitRenderCalls.push(api?.getGitExpandedSection?.() ?? "?"); },
  });
  return {
    api, localStorage, document, resizeCalls, syncCalls, gitRenderCalls,
    setActiveSession: (s) => { activeSession = s; },
    // Runs the rAF-scheduled refit; the shipped order is class flip (sync)
    // THEN fit (frame), so flushing after the toggle must observe both.
    flushFrames: () => { for (const cb of rafCallbacks.splice(0)) cb(); },
  };
}

function makeSession(extra = {}) {
  return {
    id: "inst1",
    term: { cols: 80, rows: 24 },
    fitAddon: { fit() { this.fitted = (this.fitted || 0) + 1; } },
    ...extra,
  };
}

function keydownEvent({ key = "b", ctrlKey = false, metaKey = false, altKey = false, repeat = false, target = null } = {}) {
  return {
    key, ctrlKey, metaKey, altKey, repeat,
    target: target ?? { closest: () => null },
    prevented: false,
    preventDefault() { this.prevented = true; },
  };
}

// --- R3: persistence --------------------------------------------------------

test("issue #99 R3: collapse state round-trips through localStorage under the namespaced key", () => {
  const h = sidebarHarness();
  assert.equal(h.api.SIDEBAR_COLLAPSED_KEY, "mw.ui.sidebarCollapsed");

  // Collapse and "reopen the page": a fresh harness over the SAME storage
  // map must read the persisted preference back.
  h.api.toggleSidebarCollapse();
  assert.equal(h.localStorage.map.get("mw.ui.sidebarCollapsed"), "true");
  const reopened = sidebarHarness({ storageMap: h.localStorage.map });
  assert.equal(reopened.api.readSidebarCollapsed(), true, "a refresh must restore the collapsed state");

  // And back to expanded.
  h.api.toggleSidebarCollapse();
  const reopened2 = sidebarHarness({ storageMap: h.localStorage.map });
  assert.equal(reopened2.api.readSidebarCollapsed(), false, "a refresh must restore the expanded state too");
});

test("issue #99 R3: a missing key defaults to expanded on the wide tier (R14 viewport dispatch)", () => {
  const h = sidebarHarness();
  assert.equal(h.api.readSidebarCollapsed(), false);
  assert.equal(h.localStorage.map.size, 0, "reading must not write");
});

test("issue #99 R3: an unparseable stored value degrades to the viewport-dispatched default (expanded here)", () => {
  for (const garbage of ["{not json", "[broken"]) {
    const h = sidebarHarness({ storageMap: [["mw.ui.sidebarCollapsed", garbage]] });
    assert.equal(h.api.readSidebarCollapsed(), false, `stored ${JSON.stringify(garbage)} must read as expanded on a windowless host (the R14 guard's wide default)`);
  }
  // Values that parse but are not the boolean true stay the explicit
  // "expanded" answer without consulting the viewport (PR3 keeps the
  // JSON.parse === true shape).
  for (const parsed of ["1", "null", "\"yes\""]) {
    const h = sidebarHarness({ storageMap: [["mw.ui.sidebarCollapsed", parsed]] });
    assert.equal(h.api.readSidebarCollapsed(), false, `stored ${JSON.stringify(parsed)} parses but is not true — expanded`);
  }
});

test("issue #99 R3: throwing storage (private mode) degrades to expanded without crashing", () => {
  const h = sidebarHarness({ throwingStorage: true });
  assert.equal(h.api.readSidebarCollapsed(), false, "unreadable storage must answer expanded");
  assert.doesNotThrow(() => h.api.persistSidebarCollapsed(true), "persisting must not throw");
  assert.doesNotThrow(() => h.api.toggleSidebarCollapse(), "toggling must still work, unpersisted");
  assert.equal(h.api.isCollapsed(), true, "the toggle itself still works for this page load");
});

// --- R1/R2: class toggle + two-state button ---------------------------------

test("issue #99 R1/R2: toggling flips the .collapsed class, swaps the chevrons and persists", () => {
  const h = sidebarHarness();
  const left = h.document.elements["sidebar-toggle-icon-left"];
  const right = h.document.elements["sidebar-toggle-icon-right"];
  const btn = h.document.elements["sidebar-toggle-btn"];
  assert.deepEqual([...h.document.classes], [], "starts expanded: no class on #app");
  assert.equal(left.hasAttribute("hidden"), false, "<< shows while expanded");
  assert.equal(right.hasAttribute("hidden"), true, ">> hides while expanded");
  assert.equal(btn.getAttribute("aria-expanded"), "true", "aria-expanded starts in the expanded state");

  h.api.toggleSidebarCollapse();
  assert.deepEqual([...h.document.classes], ["collapsed"], "collapsed class lands on #app");
  assert.equal(left.hasAttribute("hidden"), true, "<< hides once collapsed");
  assert.equal(right.hasAttribute("hidden"), false, ">> shows once collapsed");
  assert.equal(btn.getAttribute("aria-expanded"), "false", "aria-expanded reports the folded-away sidebar");
  assert.equal(h.localStorage.map.get("mw.ui.sidebarCollapsed"), "true");

  h.api.toggleSidebarCollapse();
  assert.deepEqual([...h.document.classes], [], "second toggle expands again");
  assert.equal(left.hasAttribute("hidden"), false, "<< shows when expanded");
  assert.equal(right.hasAttribute("hidden"), true, ">> hides when expanded");
  assert.equal(btn.getAttribute("aria-expanded"), "true");
  assert.equal(h.localStorage.map.get("mw.ui.sidebarCollapsed"), "false");
});

// --- R4: the first global keydown -------------------------------------------

test("issue #99 R4: Ctrl+B on a non-input target toggles and preventDefaults", () => {
  const h = sidebarHarness();
  const ev = keydownEvent({ key: "b", ctrlKey: true });
  h.api.handleSidebarToggleKeydown(ev);
  assert.equal(h.api.isCollapsed(), true, "Ctrl+B toggles the sidebar");
  assert.equal(ev.prevented, true, "the browser's own Ctrl+B (bookmarks bar) must be suppressed");
});

test("issue #99 R4: Cmd+B on macOS toggles without Ctrl", () => {
  const h = sidebarHarness();
  h.api.handleSidebarToggleKeydown(keydownEvent({ key: "b", metaKey: true }));
  assert.equal(h.api.isCollapsed(), true, "Cmd+B toggles the sidebar");
});

test("issue #99 R4: Caps Lock / Ctrl+Shift+B report key as 'B' and still toggle", () => {
  const h = sidebarHarness();
  h.api.handleSidebarToggleKeydown(keydownEvent({ key: "B", ctrlKey: true }));
  assert.equal(h.api.isCollapsed(), true, "event.key 'B' must match via toLowerCase()");
});

test("issue #99 R4: key-repeat while holding Ctrl+B is ignored (no flapping)", () => {
  const h = sidebarHarness();
  h.api.handleSidebarToggleKeydown(keydownEvent({ key: "b", ctrlKey: true, repeat: true }));
  assert.equal(h.api.isCollapsed(), false, "a held key must not toggle back and forth");
  assert.equal(h.localStorage.map.size, 0, "and must not persist anything");
});

test("issue #99 R4: a bare 'b' without Ctrl/Cmd does not toggle", () => {
  const h = sidebarHarness();
  h.api.handleSidebarToggleKeydown(keydownEvent({ key: "b" }));
  assert.equal(h.api.isCollapsed(), false);
});

test("issue #99 R4: Ctrl+Alt (AltGr) combinations do not toggle", () => {
  const h = sidebarHarness();
  h.api.handleSidebarToggleKeydown(keydownEvent({ key: "b", ctrlKey: true, altKey: true }));
  assert.equal(h.api.isCollapsed(), false, "AltGr layouts must keep Ctrl+Alt+B");
  assert.equal(h.localStorage.map.size, 0);
});

test("issue #99 R4: the input guard yields to any textarea host's Ctrl+B", () => {
  // Guard semantics only — NOT a model of the terminal path: xterm cancels
  // its own keydowns before they ever bubble here (vendored xterm.js
  // _keyDown calls cancel(e, !0) on the xterm-helper-textarea, which
  // preventDefaults AND stopPropagations). This case pins what the
  // input/textarea/contenteditable guard promises for any host that DOES
  // bubble without cancelling — the modal form fields today, and the
  // terminal itself should xterm's cancellation ever change: the event is
  // yielded untouched, so Ctrl+B stays with the field.
  const h = sidebarHarness();
  const hostTextarea = { closest: (sel) => (sel.includes("textarea") ? {} : null) };
  const ev = keydownEvent({ key: "b", ctrlKey: true, target: hostTextarea });
  h.api.handleSidebarToggleKeydown(ev);
  assert.equal(h.api.isCollapsed(), false, "Ctrl+B whose target is a textarea must not fold the sidebar");
  assert.equal(ev.prevented, false, "the host keeps the keystroke");
  assert.equal(h.localStorage.map.size, 0, "and nothing is persisted");
});

test("issue #99 R4: keydown landing in an input / textarea / contenteditable is ignored", () => {
  const inField = (kind) => ({
    target: { closest: (sel) => (sel.includes(kind) ? {} : null) },
  });
  for (const kind of ["input", "textarea", "contenteditable"]) {
    const h = sidebarHarness();
    const ev = keydownEvent({ key: "b", ctrlKey: true });
    ev.target = inField(kind).target;
    h.api.handleSidebarToggleKeydown(ev);
    assert.equal(h.api.isCollapsed(), false, `Ctrl+B with focus in ${kind} must not toggle`);
    assert.equal(ev.prevented, false, "the field keeps the event");
    assert.equal(h.localStorage.map.size, 0);
  }
});

// --- R12: refit after the class flip -----------------------------------------

test("issue #99 R12: the class flip lands first, the xterm fit runs on the next frame", () => {
  const h = sidebarHarness();
  const session = makeSession();
  h.setActiveSession(session);

  h.api.toggleSidebarCollapse();
  assert.deepEqual([...h.document.classes], ["collapsed"]);
  assert.equal(session.fitAddon.fitted || 0, 0, "fit must wait for the scheduled frame, not run mid-toggle");

  h.flushFrames();
  assert.equal(session.fitAddon.fitted, 1, "xterm-addon-fit re-fits once after the toggle");
  assert.deepEqual(h.resizeCalls, [{ id: "inst1", cols: 80, rows: 24 }], "the new geometry is pushed to the PTY");
  assert.deepEqual(h.syncCalls, ["inst1"]);

  // A second toggle re-fits for the expanded geometry too.
  h.api.toggleSidebarCollapse();
  h.flushFrames();
  assert.equal(session.fitAddon.fitted, 2);
});

test("issue #99 R12: refitting with no active terminal is a no-op, not a crash", () => {
  const h = sidebarHarness();
  h.api.toggleSidebarCollapse();
  assert.doesNotThrow(() => h.flushFrames());
  assert.deepEqual(h.resizeCalls, []);
  assert.deepEqual(h.syncCalls, []);
});


// --- R13: orthogonality with the staged/unstaged git accordion ---------------

test("issue #99 R13: collapsing the sidebar never touches the git accordion, and vice versa", () => {
  const h = sidebarHarness();
  const staged = h.document.elements["git-staged-section"];
  const unstaged = h.document.elements["git-unstaged-section"];

  // Baseline matches the shipped markup: unstaged expanded, staged folded.
  assert.equal(h.api.getGitExpandedSection(), "unstaged");
  assert.equal(unstaged.classList.contains("expanded"), true);
  assert.equal(staged.classList.contains("expanded"), false);

  // Fold and unfold the sidebar: the accordion must not move.
  h.api.toggleSidebarCollapse();
  h.api.toggleSidebarCollapse();
  assert.equal(h.api.getGitExpandedSection(), "unstaged", "sidebar toggles must leave the accordion alone");
  assert.equal(unstaged.classList.contains("expanded"), true);
  assert.equal(staged.classList.contains("expanded"), false);
  assert.deepEqual([...h.document.classes], []);

  // Fold the accordion via the SHIPPED toggleGitSection: the sidebar
  // collapse state must not move.
  h.api.toggleGitSection("staged");
  assert.equal(h.api.getGitExpandedSection(), "staged");
  assert.equal(staged.classList.contains("expanded"), true);
  assert.equal(unstaged.classList.contains("expanded"), false);
  assert.equal(h.api.isCollapsed(), false, "the accordion must leave the sidebar alone");
  assert.deepEqual([...h.document.classes], []);
  assert.deepEqual(h.gitRenderCalls, ["staged"], "the accordion re-renders through the real renderGitChanges hook");

  // And both directions interleaved: collapse the sidebar while the
  // accordion is folded — each state keeps its own value.
  h.api.toggleSidebarCollapse();
  assert.equal(h.api.getGitExpandedSection(), "staged", "a later sidebar collapse still must not touch the accordion");
  assert.deepEqual([...h.document.classes], ["collapsed"]);
  h.api.toggleGitSection("unstaged");
  assert.equal(h.api.isCollapsed(), true, "an accordion toggle must not unfold the sidebar");
  assert.deepEqual([...h.document.classes], ["collapsed"]);
});
