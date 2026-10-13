// Behavioral tests for the terminal quick-keys panel PR1 (issue #100
// input gap; docs/plans/feature-v0.6.0-terminal-quick-keys/): the byte
// table with its application-cursor-keys branch (R20), the derived
// panel visibility + content-state reset (R19), and the IME composition
// queue with its flush ownership (R20/§3.3). Everything is sliced from
// the shipped index.html and driven against stubbed browser state — no
// copies (§6 discipline), so a drift between the slice anchors and the
// served source fails loudly at slice time. Run:
// node --test testdata/quick_keys.test.mjs
// (also wired into terminal_status_test.go for `go test`).
import test from "node:test";
import assert from "node:assert/strict";
import { readFileSync } from "node:fs";
import { fileURLToPath } from "node:url";
import { dirname, join } from "node:path";

const here = dirname(fileURLToPath(import.meta.url));
const staticDir = join(here, "..", "static");
const indexSource = readFileSync(join(staticDir, "index.html"), "utf8");

// --- source slicing helpers (same approach as terminal_touch_scroll.test.mjs) ---

function inlineScript(html) {
  const start = html.lastIndexOf("<script>");
  const end = html.indexOf("</script>", start);
  assert.ok(start > 0 && end > start, "could not locate index.html's inline <script> block");
  return html.slice(start + "<script>".length, end);
}

// Input assumptions (§6 helper discipline): brace matching does NOT skip
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

// --- index.html harness -----------------------------------------------------

const factory = new Function("deps", `
  "use strict";
  const { document, getActiveTerminalSession } = deps;
  ${[
    "function quickKeyBytes",
    "function deriveQuickKeysVisibility",
    "function resetQuickKeysPanelContent",
    "function syncQuickKeysPanel",
    "function ensureQuickKeysImeBinding",
    "function flushQuickKeysQueue",
    "function sendQuickKeyBytes",
    "function sendQuickKey(key, event)",
    "function toggleQuickKeysPanel(event)",
  ].map(h => sliceBlock(inlineScript(indexSource), h)).join("\n\n")}
  return {
    quickKeyBytes,
    deriveQuickKeysVisibility,
    resetQuickKeysPanelContent,
    syncQuickKeysPanel,
    ensureQuickKeysImeBinding,
    flushQuickKeysQueue,
    sendQuickKeyBytes,
    sendQuickKey,
    toggleQuickKeysPanel,
  };
`);

function makePanel() {
  const classes = new Set();
  return {
    classes,
    classList: {
      toggle: (c, on) => { if (on) classes.add(c); else classes.delete(c); },
      remove: (c) => classes.delete(c),
      contains: (c) => classes.has(c),
    },
  };
}

function makeSession(id) {
  const inputCalls = [];
  const imeListeners = new Map();
  return {
    id,
    inputCalls,
    composing: false,
    quickKeysOpen: false,
    term: {
      input(bytes) { inputCalls.push(bytes); },
      modes: { applicationCursorKeysMode: false },
      textarea: {
        addEventListener(type, fn) { imeListeners.set(type, fn); },
      },
    },
    fireIme: (type) => { if (imeListeners.has(type)) imeListeners.get(type)(); },
    imeBound: () => imeListeners.size,
  };
}

function quickKeysHarness() {
  const panel = makePanel();
  let active = null;
  const api = factory({
    document: { getElementById: (id) => (id === "quick-keys-panel" ? panel : null) },
    getActiveTerminalSession: () => active,
  });
  return { api, panel, setActive: (s) => { active = s; }, getActive: () => active };
}

// --- R20: byte table ----------------------------------------------------------

test("quick keys R20: the byte table sends every built-in key, with the arrow SS3/CSI branch on applicationCursorKeysMode", () => {
  const h = quickKeysHarness();
  const fixed = {
    esc: "\x1b", tab: "\t", "shift-tab": "\x1b[Z",
    "ctrl-c": "\x03", "ctrl-d": "\x04",
  };
  for (const [key, bytes] of Object.entries(fixed)) {
    assert.equal(h.api.quickKeyBytes(key, false), bytes, `${key} (mode off)`);
    assert.equal(h.api.quickKeyBytes(key, true), bytes, `${key} (mode on)`);
  }
  const csi = { up: "\x1b[A", down: "\x1b[B", right: "\x1b[C", left: "\x1b[D" };
  const ss3 = { up: "\x1bOA", down: "\x1bOB", right: "\x1bOC", left: "\x1bOD" };
  for (const key of Object.keys(csi)) {
    assert.equal(h.api.quickKeyBytes(key, false), csi[key], `${key} sends CSI when applicationCursorKeysMode is off`);
    assert.equal(h.api.quickKeyBytes(key, true), ss3[key], `${key} sends SS3 when applicationCursorKeysMode is on`);
  }
  assert.equal(h.api.quickKeyBytes("nonsense", false), null, "unknown keys send nothing");
});

test("quick keys R20: sendQuickKeyBytes goes through term.input — the shared onData gate, direct when not composing", () => {
  const h = quickKeysHarness();
  const s = makeSession("a");
  h.setActive(s);
  h.api.sendQuickKeyBytes(s, "\x1b");
  assert.deepEqual(s.inputCalls, ["\x1b"], "direct term.input when no composition is active");

  // sendQuickKey: reads term.modes.applicationCursorKeysMode LIVE at send
  // time (never cached) and preventDefaults the pointer event (§3.3).
  s.term.modes.applicationCursorKeysMode = true;
  let ev = { prevented: false, preventDefault() { this.prevented = true; } };
  h.api.sendQuickKey("up", ev);
  assert.equal(ev.prevented, true, "sendQuickKey preventDefaults the pointerdown");
  assert.deepEqual(s.inputCalls.slice(-1), ["\x1bOA"], "mode on at send time -> SS3");
  s.term.modes.applicationCursorKeysMode = false;
  ev = { prevented: false, preventDefault() { this.prevented = true; } };
  h.api.sendQuickKey("up", ev);
  assert.equal(ev.prevented, true, "preventDefault on the second tap too");
  assert.deepEqual(s.inputCalls.slice(-1), ["\x1b[A"], "mode off at send time -> CSI");
});

// --- R19: derived visibility + content reset ----------------------------------

test("quick keys R19: visibility derives from the active session's flag — no session hides, flag off hides, flag on shows", () => {
  const h = quickKeysHarness();
  h.setActive(null);
  assert.equal(h.api.deriveQuickKeysVisibility(null), false, "no session -> hidden");
  const s = makeSession("a");
  h.setActive(s);
  assert.equal(h.api.deriveQuickKeysVisibility(s), false, "flag unset -> hidden");
  s.quickKeysOpen = true;
  assert.equal(h.api.deriveQuickKeysVisibility(s), true, "flag set -> visible");

  // toggleQuickKeysPanel (the entry button's pointerdown handler): flips
  // the flag and runs the SAME sync — open -> close -> open — and is a
  // no-op without a session. The event is preventDefaulted (§3.3).
  s.quickKeysOpen = false;
  let ev = { prevented: false, preventDefault() { this.prevented = true; } };
  h.api.toggleQuickKeysPanel(ev);
  assert.equal(ev.prevented, true, "toggle preventDefaults the pointerdown");
  assert.equal(s.quickKeysOpen, true, "first tap opens");
  assert.ok(h.panel.classes.has("open"), "the sync after the toggle opens the panel");
  ev = { prevented: false, preventDefault() { this.prevented = true; } };
  h.api.toggleQuickKeysPanel(ev);
  assert.equal(s.quickKeysOpen, false, "second tap is the ONLY manual collapse (§3.2)");
  assert.ok(!h.panel.classes.has("open"), "the sync after the toggle closes the panel");
  ev = { prevented: false, preventDefault() { this.prevented = true; } };
  h.api.toggleQuickKeysPanel(ev);
  assert.equal(s.quickKeysOpen, true, "third tap reopens");

  // No active session: nothing to toggle, no exception.
  h.setActive(null);
  s.quickKeysOpen = false;
  ev = { prevented: false, preventDefault() { this.prevented = true; } };
  assert.doesNotThrow(() => h.api.toggleQuickKeysPanel(ev), "no session must not throw");
  assert.equal(s.quickKeysOpen, false, "a toggle with no active session touches no flag");
});

test("quick keys R19: sync flips the panel class and resets the paste-fallback content state on EVERY hide path", () => {
  const h = quickKeysHarness();
  const a = makeSession("a");
  h.setActive(a);
  a.quickKeysOpen = true;
  h.api.syncQuickKeysPanel();
  assert.ok(h.panel.classes.has("open"), "flag on -> panel open");
  assert.equal(a.imeBound(), 2, "an open panel binds the IME composition listeners on its session");

  // The paste fallback state is transient: a hide must reset it, and the
  // hide paths are many (instance switch / delete / worktree / web kind) —
  // so the reset lives on the sync path, not the toggle.
  h.panel.classes.add("paste-mode");
  a.quickKeysOpen = false;
  h.api.syncQuickKeysPanel();
  assert.ok(!h.panel.classes.has("open"), "flag off -> panel closed");
  assert.ok(!h.panel.classes.has("paste-mode"), "every hide resets the content state to the key grid");

  // No active session at all: hidden AND reset (covers delete/switch/web-kind).
  h.panel.classes.add("paste-mode");
  h.setActive(null);
  h.api.syncQuickKeysPanel();
  assert.ok(!h.panel.classes.has("open"), "no session -> panel closed");
  assert.ok(!h.panel.classes.has("paste-mode"), "no session -> content state still reset");
});

test("quick keys R19: switching instances hides A's panel but keeps A's flag — the binding survives the round trip", () => {
  const h = quickKeysHarness();
  const a = makeSession("a");
  const b = makeSession("b");
  h.setActive(a);
  a.quickKeysOpen = true;
  h.api.syncQuickKeysPanel();
  assert.ok(h.panel.classes.has("open"), "A open");

  h.setActive(b); // instance switch: A hides, flag untouched
  h.api.syncQuickKeysPanel();
  assert.ok(!h.panel.classes.has("open"), "B has no flag -> hidden (A's panel does not linger)");

  h.setActive(a); // switching back restores
  h.api.syncQuickKeysPanel();
  assert.ok(h.panel.classes.has("open"), "back on A -> the expanded state is restored");
});

// --- R20: IME queue + flush ownership -----------------------------------------

test("quick keys R20: bytes queued during composition flush to the session that composed — never re-fetching the active one", () => {
  const h = quickKeysHarness();
  const a = makeSession("a");
  h.setActive(a);
  h.api.ensureQuickKeysImeBinding(a);

  // Mid-composition: bytes queue instead of sending.
  a.composing = true;
  h.api.sendQuickKeyBytes(a, "\x1b");
  h.api.sendQuickKeyBytes(a, "\x03");
  assert.deepEqual(a.inputCalls, [], "nothing sends while composing");
  assert.deepEqual(a.quickKeysQueue, ["\x1b", "\x03"], "bytes wait in the session's queue");

  // An instance switch inside the queue window must NOT redirect the bytes.
  const b = makeSession("b");
  h.setActive(b);

  // Composition ends on A: flush targets A (the composer), even though B is active.
  a.fireIme("compositionend");
  assert.deepEqual(a.inputCalls, ["\x1b", "\x03"], "flush lands on the composing session");
  assert.deepEqual(b.inputCalls, [], "the active session receives nothing");
  assert.equal(a.quickKeysQueue.length, 0, "the queue drains");
  assert.equal(a.composing, false, "the composition flag clears before the flush");
});

test("quick keys R20: IME binding is per-session lazy and guards a missing textarea", () => {
  const h = quickKeysHarness();
  const a = makeSession("a");
  const b = makeSession("b");
  h.api.ensureQuickKeysImeBinding(a);
  assert.equal(a.imeBound(), 2, "first bind installs compositionstart + compositionend");
  h.api.ensureQuickKeysImeBinding(a);
  assert.equal(a.imeBound(), 2, "re-binding is a no-op");
  h.api.ensureQuickKeysImeBinding(b);
  assert.equal(b.imeBound(), 2, "each session gets its own listeners");
  assert.doesNotThrow(() => h.api.ensureQuickKeysImeBinding({ term: {} }), "a session without a textarea binds nothing, not an exception");
});
