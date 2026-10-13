// Behavioral tests for the terminal quick-keys panel (issue #100 input
// gap; docs/plans/feature-v0.6.0-terminal-quick-keys/) — the FULL R18–R22
// surface: the byte table with its application-cursor-keys branch and the
// pointerdown send/toggle handlers (R20), the derived panel visibility +
// content-state reset and the instance-binding round trip (R19), the IME
// composition queue with its flush ownership (R20/§3.3), and — since PR2 —
// the paste pipeline with its cap-before-wrap order, the two clipboard
// paths with session attribution, the on-screen fallback state, and the
// selection-first / viewport-fallback copy (R21/R22). Everything is
// sliced from the shipped index.html (functions AND the module-level
// clipboard state) and driven against stubbed browser state — no copies
// (§6 discipline), so a drift between the slice anchors and the served
// source fails loudly at slice time. Run:
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

// Slices a single `const X = ...;` statement up to its terminator.
function sliceStatement(src, header) {
  const start = src.indexOf(header);
  assert.notEqual(start, -1, `source no longer contains ${JSON.stringify(header)}`);
  const end = src.indexOf(";", start);
  assert.notEqual(end, -1, `no statement terminator after ${JSON.stringify(header)}`);
  return src.slice(start, end + 1);
}

// --- index.html harness -----------------------------------------------------

const factory = new Function("deps", `
  "use strict";
  const { document, navigator, updateStatus, getActiveTerminalSession } = deps;
  ${sliceStatement(inlineScript(indexSource), "const QUICK_KEYS_PASTE_MAX_CHARS")}
  ${sliceStatement(inlineScript(indexSource), "let quickKeysPasteSession = null;")}
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
    "function normalizePasteNewlines",
    "function stripBracketedPasteMarkers",
    "function maybeWrapBracketedPaste",
    "function extractViewportText",
    "function deliverQuickKeysPaste",
    "function quickKeyPaste(event)",
    "function enterQuickKeysPasteFallback(session)",
    "function quickKeysPasteCaptured(event)",
    "function cancelQuickKeysPaste(event)",
    "function quickKeyCopy(event)",
    "function writeQuickKeysClipboard",
    "function fallbackExecCommandCopy",
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
    normalizePasteNewlines,
    stripBracketedPasteMarkers,
    maybeWrapBracketedPaste,
    extractViewportText,
    deliverQuickKeysPaste,
    quickKeyPaste,
    enterQuickKeysPasteFallback,
    quickKeysPasteCaptured,
    cancelQuickKeysPaste,
    quickKeyCopy,
    writeQuickKeysClipboard,
    fallbackExecCommandCopy,
    pasteOwner: () => quickKeysPasteSession,
  };
`);

function makePanel() {
  const classes = new Set();
  return {
    classes,
    classList: {
      add: (c) => classes.add(c),
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
      rows: 24,
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

function quickKeysHarness({ navigatorStub = {} } = {}) {
  const panel = makePanel();
  const pasteTextarea = { value: "stale", focused: 0, focus() { this.focused++; } };
  const statusCalls = [];
  const execCalls = [];
  const created = [];
  let execOk = true;
  let active = null;
  const documentStub = {
    getElementById: (id) => {
      if (id === "quick-keys-panel") return panel;
      if (id === "quick-keys-paste-textarea") return pasteTextarea;
      return null;
    },
    createElement: () => {
      const ta = {
        value: "", style: {}, attrs: {}, selected: false, focusedTa: false,
        setAttribute(k, v) { this.attrs[k] = v; },
        focus() { this.focusedTa = true; },
        select() { this.selected = true; },
      };
      created.push(ta);
      return ta;
    },
    body: {
      appendChild(ta) { return ta; },
      removeChild(ta) { return ta; },
    },
    execCommand: (cmd) => { execCalls.push(cmd); return execOk; },
  };
  const api = factory({
    document: documentStub,
    navigator: navigatorStub,
    updateStatus: (msg) => statusCalls.push(msg),
    getActiveTerminalSession: () => active,
  });
  return {
    api, panel, pasteTextarea, statusCalls, execCalls, created,
    setExecOk: (v) => { execOk = v; },
    setActive: (s) => { active = s; }, getActive: () => active,
  };
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

// --- R21: paste pipeline (pure stages) ---------------------------------------

test("quick keys R21: normalizePasteNewlines folds CRLF and LF to CR, leaving lone CR alone", () => {
  const h = quickKeysHarness();
  assert.equal(h.api.normalizePasteNewlines("a\r\nb"), "a\rb", "CRLF folds to CR");
  assert.equal(h.api.normalizePasteNewlines("a\nb"), "a\rb", "bare LF folds to CR");
  assert.equal(h.api.normalizePasteNewlines("a\r\nb\nc\rd"), "a\rb\rc\rd", "mixed input, lone CR untouched");
  assert.equal(h.api.normalizePasteNewlines(""), "", "empty stays empty");
});

test("quick keys R21: stripBracketedPasteMarkers removes embedded 200~/201~ markers (injection hardening)", () => {
  const h = quickKeysHarness();
  assert.equal(h.api.stripBracketedPasteMarkers("a\x1b[200~b\x1b[201~c"), "abc", "one embedded pair stripped");
  assert.equal(h.api.stripBracketedPasteMarkers("\x1b[200~\x1b[200~x\x1b[201~\x1b[201~"), "x", "repeated markers all stripped");
  assert.equal(h.api.stripBracketedPasteMarkers("plain"), "plain", "plain text untouched");
});

test("quick keys R21: maybeWrapBracketedPaste wraps iff the mode is on — no alt-screen exclusion", () => {
  const h = quickKeysHarness();
  assert.equal(h.api.maybeWrapBracketedPaste("x", true), "\x1b[200~x\x1b[201~", "bracketed mode wraps");
  assert.equal(h.api.maybeWrapBracketedPaste("x", false), "x", "mode off sends bare");
  assert.equal(h.api.maybeWrapBracketedPaste("x", undefined), "x", "absent mode reads as off");
});

test("quick keys R21: deliverQuickKeysPaste runs the pipeline through the input gate — empty is a fed-back no-op, overflow truncates with feedback", () => {
  const h = quickKeysHarness();
  const s = makeSession("a");
  h.setActive(s);

  s.term.modes.bracketedPasteMode = true;
  h.api.deliverQuickKeysPaste("one\r\ntwo\x1b[200~injected", s);
  assert.deepEqual(s.inputCalls, ["\x1b[200~one\rtwoinjected\x1b[201~"], "normalize -> strip -> wrap -> term.input in one payload");

  s.term.modes.bracketedPasteMode = false;
  const before = s.inputCalls.length;
  h.api.deliverQuickKeysPaste("", s);
  assert.equal(s.inputCalls.length, before, "empty clipboard sends nothing");
  assert.deepEqual(h.statusCalls, ["Paste skipped: clipboard empty"], "empty clipboard reports itself");

  const big = "y".repeat(256 * 1024 + 10);
  h.api.deliverQuickKeysPaste(big, s);
  assert.equal(s.inputCalls[before].length, 256 * 1024, "overflow truncates to exactly the character cap (pre-wrap)");
  assert.ok(h.statusCalls.some((m) => m.includes("truncated")), "overflow says so");
});

// --- R21: the two clipboard paths ---------------------------------------------

test("quick keys R21: path A — navigator.clipboard.readText present means the async read delivers through the pipeline", async () => {
  const reads = [];
  const navigatorStub = { clipboard: { readText: () => { reads.push(1); return Promise.resolve("hi\nthere"); } } };
  const h = quickKeysHarness({ navigatorStub });
  const s = makeSession("a");
  h.setActive(s);
  const ev = { prevented: false, preventDefault() { this.prevented = true; } };
  h.api.quickKeyPaste(ev);
  assert.equal(ev.prevented, true, "paste entry preventDefaults the pointerdown");
  assert.equal(reads.length, 1, "readText called inside the transient activation");
  assert.ok(!h.panel.classes.has("paste-mode"), "path A never touches the fallback state");
  await new Promise((r) => setTimeout(r, 0)); // let the thenable settle
  assert.deepEqual(s.inputCalls, ["hi\rthere"], "the resolved text goes through the pipeline");
});

test("quick keys R21: path A rejection feeds back and drops to the fallback; path B absence flips the fallback state", async () => {
  const rejecting = quickKeysHarness({ navigatorStub: { clipboard: { readText: () => Promise.reject(new Error("denied")) } } });
  const s = makeSession("a");
  s.quickKeysOpen = true; // the Paste key is only reachable with the panel open
  rejecting.setActive(s);
  rejecting.api.quickKeyPaste({ preventDefault() {} });
  await new Promise((r) => setTimeout(r, 0));
  assert.deepEqual(s.inputCalls, [], "a rejected read sends nothing");
  assert.ok(rejecting.statusCalls.some((m) => m.startsWith("Paste failed")), "a rejected read feeds back");
  assert.ok(rejecting.panel.classes.has("paste-mode"), "a rejected read DROPS TO the fallback state (§7 risk-table mitigation)");
  assert.equal(rejecting.api.pasteOwner(), s, "the fallback belongs to the session that asked");

  const insecure = quickKeysHarness({ navigatorStub: {} }); // no clipboard at all — LAN http main scenario
  const s2 = makeSession("b");
  s2.quickKeysOpen = true; // panel open, as the reachable Paste key implies
  insecure.setActive(s2);
  const ev = { prevented: false, preventDefault() { this.prevented = true; } };
  insecure.api.quickKeyPaste(ev);
  assert.equal(ev.prevented, true, "path B entry also preventDefaults");
  assert.ok(insecure.panel.classes.has("paste-mode"), "no readText -> the panel flips to the paste fallback state");
  assert.equal(insecure.pasteTextarea.value, "", "the textarea is cleared on entry");
  assert.equal(insecure.pasteTextarea.focused, 1, "the textarea is focused so the long-press callout can anchor");
});

test("quick keys R21: the fallback state — capture sends and flips back automatically; cancel flips back without closing the panel", () => {
  const h = quickKeysHarness({ navigatorStub: {} });
  const s = makeSession("a");
  h.setActive(s);
  s.quickKeysOpen = true;
  h.api.syncQuickKeysPanel();
  h.panel.classes.add("paste-mode");

  h.api.quickKeysPasteCaptured({ preventDefault() {}, clipboardData: { getData: () => "pasted\r\ntext" } });
  assert.deepEqual(s.inputCalls, ["pasted\rtext"], "the paste event text is delivered");
  assert.ok(!h.panel.classes.has("paste-mode"), "capture flips back to the key grid automatically");
  assert.ok(h.panel.classes.has("open"), "the panel stays open after capture (sending never collapses)");

  h.panel.classes.add("paste-mode");
  const ev = { prevented: false, preventDefault() { this.prevented = true; } };
  h.api.cancelQuickKeysPaste(ev);
  assert.equal(ev.prevented, true, "cancel preventDefaults (focus contract)");
  assert.ok(!h.panel.classes.has("paste-mode"), "cancel flips back");
  assert.ok(h.panel.classes.has("open"), "cancel does NOT close the panel");
  assert.deepEqual(s.inputCalls, ["pasted\rtext"], "cancel delivers nothing");
});

// --- R22: copy -----------------------------------------------------------------

test("quick keys R22: extractViewportText copies the visible rows, trims tails, drops trailing blanks — one code path for both buffers", () => {
  const h = quickKeysHarness();
  const makeTerm = (lines, viewportY, rows) => ({
    rows,
    buffer: {
      active: {
        viewportY,
        length: lines.length,
        getLine: (i) => ({ translateToString: (trim) => (trim ? lines[i].replace(/\s+$/, "") : lines[i]) }),
      },
    },
  });
  // buffer.active identity IS the pty/TUI split — the function reads it
  // as-is, so an alt-screen-shaped buffer (the "TUI frame") works the same.
  const shellView = makeTerm(["$ ls   ", "a.txt", "", ""], 0, 4);
  assert.equal(h.api.extractViewportText(shellView), "$ ls\na.txt", "pty normal buffer: visible rows, trailing blanks dropped");
  const tuiView = makeTerm(["┌─errors─┐", "│ boom  │", "", ""], 0, 4);
  assert.equal(h.api.extractViewportText(tuiView), "┌─errors─┐\n│ boom  │", "TUI alt-screen frame copies identically");
  const scrolled = makeTerm(["old", "mid", "$ current", ""], 1, 2);
  assert.equal(h.api.extractViewportText(scrolled), "mid\n$ current", "the [viewportY, viewportY+rows) window is respected");
});

test("quick keys R22: quickKeyCopy prefers a live selection, falls back to the viewport, and writes via writeText or the execCommand fallback", async () => {
  const h = quickKeysHarness({ navigatorStub: { clipboard: { writeText: (t) => { h.writeCalls.push(t); return Promise.resolve(); } } } });
  h.writeCalls = [];
  const s = makeSession("a");
  h.setActive(s);

  // Selection present and non-empty -> selection wins.
  s.term.hasSelection = () => true;
  s.term.getSelection = () => "sel\ntext";
  h.api.quickKeyCopy({ preventDefault() {} });
  await new Promise((r) => setTimeout(r, 0));
  assert.deepEqual(h.writeCalls, ["sel\ntext"], "writeText receives the selection");
  assert.deepEqual(h.statusCalls, ["Copied"], "success feeds back");

  // Empty selection string counts as NO selection -> viewport fallback.
  s.term.getSelection = () => "";
  s.term.buffer = { active: { viewportY: 0, length: 1, getLine: () => ({ translateToString: () => "viewport line" }) } };
  h.api.quickKeyCopy({ preventDefault() {} });
  await new Promise((r) => setTimeout(r, 0));
  assert.deepEqual(h.writeCalls.slice(-1), ["viewport line"], "an empty selection falls back to the viewport text");

  // No clipboard at all -> execCommand('copy') with a selected temp textarea.
  const insecure = quickKeysHarness({ navigatorStub: {} });
  const s2 = makeSession("b");
  insecure.setActive(s2);
  s2.term.hasSelection = () => true;
  s2.term.getSelection = () => "exec text";
  insecure.api.quickKeyCopy({ preventDefault() {} });
  await new Promise((r) => setTimeout(r, 0));
  assert.deepEqual(insecure.execCalls, ["copy"], "execCommand('copy') is the insecure-context path");
  assert.equal(insecure.created.length, 1, "a temporary textarea is created");
  assert.equal(insecure.created[0].value, "exec text", "the temp textarea carries the text");
  assert.equal(insecure.created[0].selected, true, "the temp textarea is selected");
  assert.ok(insecure.created[0].attrs.readonly !== undefined, "the temp textarea is readonly-guarded");
  assert.deepEqual(insecure.statusCalls, ["Copied"], "the fallback reports success");

  // execCommand refusing -> fed-back failure.
  insecure.setExecOk(false);
  insecure.api.quickKeyCopy({ preventDefault() {} });
  await new Promise((r) => setTimeout(r, 0));
  assert.deepEqual(insecure.statusCalls.slice(-1), ["Copy failed"], "a refused execCommand feeds back");
});

test("quick keys R21: the readText promise delivers to the session captured at pointerdown — an instance switch inside the window cannot misdeliver", async () => {
  let resolveRead = null;
  const navigatorStub = { clipboard: { readText: () => new Promise((r) => { resolveRead = r; }) } };
  const h = quickKeysHarness({ navigatorStub });
  const a = makeSession("a");
  const b = makeSession("b");
  h.setActive(a);
  h.api.quickKeyPaste({ preventDefault() {} });
  h.setActive(b); // instance switch inside the promise window
  resolveRead("hello\r\nworld");
  await new Promise((r) => setTimeout(r, 0));
  assert.deepEqual(a.inputCalls, ["hello\rworld"], "the payload lands on the session that was active at pointerdown");
  assert.deepEqual(b.inputCalls, [], "the newly active session receives nothing");
});

test("quick keys R21: the cap runs BEFORE the wrap — an overflowing bracketed payload still ends with the closing marker", () => {
  const h = quickKeysHarness();
  const s = makeSession("a");
  h.setActive(s);
  s.term.modes.bracketedPasteMode = true;
  h.api.deliverQuickKeysPaste("x".repeat(256 * 1024 + 5), s);
  const payload = s.inputCalls[0];
  assert.ok(payload.endsWith("\x1b[201~"), "the closing marker is never cut off");
  assert.equal(payload.length, 256 * 1024 + 12, "cap chars + the 6+6 marker bytes, nothing more");
  assert.ok(payload.startsWith("\x1b[200~"), "the opening marker is intact");
});

test("quick keys R19/R21: an instance switch inside the paste fallback resets the content state to the key grid (binding follows the active session)", () => {
  const h = quickKeysHarness({ navigatorStub: {} });
  const a = makeSession("a");
  const b = makeSession("b");
  h.setActive(a);
  a.quickKeysOpen = true;
  h.api.syncQuickKeysPanel();
  h.api.quickKeyPaste({ preventDefault() {} });
  assert.ok(h.panel.classes.has("paste-mode"), "A is in the fallback state");
  assert.equal(h.api.pasteOwner(), a, "the fallback belongs to A");

  h.setActive(b);
  b.quickKeysOpen = true;
  h.api.syncQuickKeysPanel(); // B is open — the panel stays visible…
  assert.ok(h.panel.classes.has("open"), "B's panel is visible");
  assert.ok(!h.panel.classes.has("paste-mode"), "…but the content state resets to the key grid (A's fallback does not linger)");
  assert.equal(h.api.pasteOwner(), null, "the ownership clears with the reset");
});

test("quick keys R21: an empty resolve is a fed-back no-op and does NOT enter the fallback", async () => {
  const navigatorStub = { clipboard: { readText: () => Promise.resolve("") } };
  const h = quickKeysHarness({ navigatorStub });
  const s = makeSession("a");
  h.setActive(s);
  h.api.quickKeyPaste({ preventDefault() {} });
  await new Promise((r) => setTimeout(r, 0));
  assert.deepEqual(s.inputCalls, [], "an empty clipboard sends nothing");
  assert.deepEqual(h.statusCalls, ["Paste skipped: clipboard empty"], "the empty resolve reports itself");
  assert.ok(!h.panel.classes.has("paste-mode"), "an empty resolve does not drop to the fallback");
});

test("quick keys R21: a paste into a disposed session is caught and fed back — nothing escapes", () => {
  const h = quickKeysHarness();
  const s = makeSession("a");
  h.setActive(s);
  // pty.js _destroySession disposes the term WITHOUT nulling it — input()
  // on the dead emitter throws before the onData gate can refuse.
  s.term.input = () => { throw new TypeError("Cannot fire on a disposed emitter"); };
  assert.doesNotThrow(() => h.api.deliverQuickKeysPaste("hello", s), "the throw is contained");
  assert.deepEqual(h.statusCalls, ["Paste failed: session closed"], "the closed session feeds back");
});

test("quick keys R21: a clipboard holding nothing but bracketed markers is a fed-back no-op — no empty frame is sent", () => {
  const h = quickKeysHarness();
  const s = makeSession("a");
  h.setActive(s);
  s.term.modes.bracketedPasteMode = true;
  h.api.deliverQuickKeysPaste("\x1b[200~\x1b[201~", s);
  assert.deepEqual(s.inputCalls, [], "no empty frame reaches term.input");
  assert.deepEqual(h.statusCalls, ["Paste skipped: clipboard empty"], "the marker-only clipboard reports itself");
});

test("quick keys R21: a cap cut never leaves a lone high surrogate at the payload tail", () => {
  const h = quickKeysHarness();
  const s = makeSession("a");
  h.setActive(s);
  // cap-1 fillers, then an emoji (two code units): the cap cut lands
  // EXACTLY between the pair's halves.
  const text = "x".repeat(256 * 1024 - 1) + "\u{1F600}" + "tail";
  h.api.deliverQuickKeysPaste(text, s);
  const payload = s.inputCalls[0];
  const last = payload.charCodeAt(payload.length - 1);
  assert.ok(!(last >= 0xD800 && last <= 0xDBFF), "the payload never ends on an isolated high surrogate");
  assert.equal(payload.length, 256 * 1024 - 1, "the dangling high surrogate is dropped");
  assert.ok(h.statusCalls.some((m) => m.includes("truncated")), "the truncation still reports itself");
});

test("quick keys R20/R21: a paste payload arriving mid-composition queues and flushes on compositionend — the shared gate for bytes and pastes", () => {
  const h = quickKeysHarness();
  const s = makeSession("a");
  h.setActive(s);
  h.api.ensureQuickKeysImeBinding(s);

  s.fireIme("compositionstart");
  h.api.deliverQuickKeysPaste("pasted\r\ntext", s);
  assert.deepEqual(s.inputCalls, [], "mid-composition the payload queues instead of sending");
  assert.deepEqual(s.quickKeysQueue, ["pasted\rtext"], "the paste rides the same per-session queue as byte keys");

  s.fireIme("compositionend");
  assert.deepEqual(s.inputCalls, ["pasted\rtext"], "compositionend flushes the queued paste");
  assert.equal(s.quickKeysQueue.length, 0, "the queue drains");
});
