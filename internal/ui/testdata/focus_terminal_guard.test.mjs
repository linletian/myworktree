// AUTH-20261105-P3-FOCUS-GUARD — minimal direct test for the editable-focus
// guard added to focusTerminalIfPossible() (issue #116 Phase 3, PLAN section
// "AUTH-20261105-P3-FOCUS-GUARD", sanctioned 2026-11-07).
//
// WHAT THIS PROVES AND WHAT IT DOES NOT.
// The owner reported on the real iPad (session a22c, anomaly A / F5) that the
// quick-keys PASTE textarea had its focus stolen back to the terminal by the
// focus poll. focusTerminalIfPossible() is a frozen-baseline 5268869
// mechanism; AUTH-20261105-P3-FOCUS-GUARD authorizes exactly ONE deviation:
// an early return when document.activeElement is editable (TEXTAREA / INPUT /
// isContentEditable) and NO ancestor on its parentElement chain carries a
// truthy data-instance-id — the same HO-3 read already shipped in
// applyVisualViewportOcclusion's ownership predicate. An editable OUTSIDE the
// host keeps its focus; the poll no longer corrects that drift, which is the
// authorization's target semantics, not a regression.
//
// This file slices the SHIPPED focusTerminalIfPossible() out of index.html
// and runs it against stubbed browser state, so a drift between the slice
// anchors and index.html fails loudly at slice time (same arrangement as
// sidebar_wt_rename.test.mjs / viewport_height.test.mjs). NO BROWSER RUNS
// HERE: the real-device F5 / anomaly-B recheck stays with the owner in the
// single joint revalidation session the authorization demands.
//
// Run: node --test testdata/focus_terminal_guard.test.mjs
import test from "node:test";
import assert from "node:assert/strict";
import { readFileSync } from "node:fs";
import { fileURLToPath } from "node:url";
import { dirname, join } from "node:path";

const here = dirname(fileURLToPath(import.meta.url));
const indexSource = readFileSync(join(here, "..", "static", "index.html"), "utf8");

function inlineScript(src) {
  const start = src.lastIndexOf("<script>");
  const end = src.indexOf("</script>", start);
  assert.ok(start > 0 && end > start, "could not locate index.html's inline <script> block");
  return src.slice(start + "<script>".length, end);
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

function sliceStatement(src, header) {
  const start = src.indexOf(header);
  assert.notEqual(start, -1, `source no longer contains ${JSON.stringify(header)}`);
  const end = src.indexOf(";", start);
  assert.notEqual(end, -1, `no statement terminator after ${JSON.stringify(header)}`);
  return src.slice(start, end + 1);
}

const script = inlineScript(indexSource);

const focusGuardBlock = sliceBlock(script, "function focusTerminalIfPossible()");

// The shipped live-status pair sliced whole, so the live-instance guard
// inside the sliced function judges with the SHIPPED statuses, not a copy.
const liveStatuses = sliceStatement(script, "const INSTANCE_LIVE_STATUSES");
const liveStatusFn = sliceBlock(script, "function isInstanceLiveStatus(status)");

// focusTerminalIfPossible() sliced whole against stubbed module scope, the
// same focusFactory arrangement as sidebar_wt_rename.test.mjs: windowHasFocus
// / openDialogs mirror the module-level declarations, and
// ensureIosImeDirectInputBridge arrives as a RECORDING stub so a case can
// pin that the #115 bridge call at the top of the function still runs while
// the guard refuses the focus — and that the guard itself never re-invokes
// it (self-excitation ban of AUTH-20261105-P3-FOCUS-GUARD).
const focusFactory = new Function("deps", `
  const { document, state, getActiveTerminalSession, ensureIosImeDirectInputBridge } = deps;
  let windowHasFocus = true;
  const openDialogs = new Set();
  ${liveStatuses}
  ${liveStatusFn}
  ${focusGuardBlock}
  return { focusTerminalIfPossible, setWindowFocus: (on) => { windowHasFocus = on; } };
`);

// A minimal DOM element stub: tagName / dataset / parentElement /
// isContentEditable exactly the fields the guard reads, plus focusCount —
// focus() only counts and the stub NEVER MOVES activeElement. The PRIMARY
// evidence that the poll does not steal focus is therefore term.focusCount
// staying 0 (case i) / reaching 1 (cases ii and the non-editable case); the
// activeElement identity assertions are AUXILIARY observation — under a stub
// that cannot move focus they pin that the fixture is intact and that the
// guard itself touches nothing, not — on their own — that a real browser
// leaves the focus where the user put it.
function fakeElement(tagName, parent = null, dataset = {}, isContentEditable = false) {
  return {
    tagName,
    parentElement: parent,
    dataset,
    isContentEditable,
    focusCount: 0,
    focus() { this.focusCount += 1; },
  };
}

// The stubbed frame, mirroring the shipped shapes:
//   host      — the shown terminal host, stamped data-instance-id like both
//               host factories stamp it (index.html tab template / pty.js);
//   helper    — the xterm helper textarea INSIDE the host (host > xterm
//               screen > helper), the focus the poll must still take (ii);
//   pasteBox  — the quick-keys PASTE textarea, a bare HO-3 editable with NO
//               stamped ancestor, the focus the poll must NOT take (i).
function makePage() {
  const host = fakeElement("DIV", null, { instanceId: "sess-1" });
  const screen = fakeElement("DIV", host);
  const helper = fakeElement("TEXTAREA", screen);
  const pasteBox = fakeElement("TEXTAREA", fakeElement("DIV", null));
  const contentEditableBox = fakeElement("DIV", fakeElement("DIV", null), {}, true);
  const plainDiv = fakeElement("DIV", null); // focusable non-editable, HO-1 style
  const term = {
    textarea: helper,
    focusCount: 0,
    focus() { this.focusCount += 1; },
  };
  const session = { term, ttyState: "READY" };
  const bridgeCalls = { count: 0 };
  const document = { activeElement: null };
  const page = focusFactory({
    document,
    state: {
      activeInst: "sess-1",
      instances: [{ id: "sess-1", status: "running" }],
      renamingInst: false,
      renamingWT: false,
    },
    getActiveTerminalSession: () => session,
    // isInstanceLiveStatus is NOT injected: it arrives sliced from index.html above.
    ensureIosImeDirectInputBridge: () => { bridgeCalls.count += 1; },
  });
  return { ...page, document, host, helper, pasteBox, contentEditableBox, plainDiv, term, bridgeCalls };
}

test("#116 AUTH-P3-FOCUS-GUARD (i): a paste textarea holding focus is NOT stolen by the poll", () => {
  const page = makePage();
  page.document.activeElement = page.pasteBox;

  page.focusTerminalIfPossible();

  assert.equal(page.term.focusCount, 0,
    "the HO-3 quick-keys PASTE textarea holds focus — the poll must not call session.term.focus()");
  assert.equal(page.document.activeElement, page.pasteBox,
    "the focus stays on the paste textarea — the stub never moves it, and the guard must not either");
  assert.equal(page.bridgeCalls.count, 1,
    "the #115 bridge call at the TOP of focusTerminalIfPossible still ran exactly once — and the guard added no second call (no self-excitation)");
});

test("#116 AUTH-P3-FOCUS-GUARD (i) spread: INPUT and contenteditable HO-3 boxes keep their focus too", () => {
  const page = makePage();
  const searchInput = fakeElement("INPUT", fakeElement("DIV", null));
  page.document.activeElement = searchInput;
  page.focusTerminalIfPossible();
  assert.equal(page.term.focusCount, 0, "an INPUT outside any host is HO-3 — no focus steal");
  assert.equal(page.document.activeElement, searchInput, "the INPUT keeps focus");

  page.document.activeElement = page.contentEditableBox;
  page.focusTerminalIfPossible();
  assert.equal(page.term.focusCount, 0, "a contenteditable outside any host is HO-3 — no focus steal");
  assert.equal(page.document.activeElement, page.contentEditableBox, "the contenteditable keeps focus");
});

test("#116 AUTH-P3-FOCUS-GUARD (ii): a helper inside the stamped terminal host still gets focus()", () => {
  const page = makePage();
  // The xterm helper textarea sits INSIDE the data-instance-id host.
  assert.equal(page.helper.parentElement.parentElement, page.host,
    "fixture: helper -> screen -> host, host stamped data-instance-id");
  page.document.activeElement = page.helper;

  page.focusTerminalIfPossible();

  assert.equal(page.term.focusCount, 1,
    "an editable WITH a truthy data-instance-id ancestor is the terminal's own input — focus() runs as before");
});

test("#116 AUTH-P3-FOCUS-GUARD: a non-editable focus reaches the SAME outcome as before", () => {
  const page = makePage();
  page.document.activeElement = page.plainDiv; // non-editable, no host ancestor

  page.focusTerminalIfPossible();

  assert.equal(page.term.focusCount, 1,
    "risk (3) of the arbitration: the guard only refuses 'editable without a host ancestor' — a non-editable activeElement focuses the terminal exactly as the frozen baseline did");

  page.document.activeElement = null;
  page.focusTerminalIfPossible();
  assert.equal(page.term.focusCount, 2, "a null activeElement (nothing focused) focuses the terminal as before");
});

test("#116 AUTH-P3-FOCUS-GUARD: the guard segment is a PURE READ", () => {
  // Slice the guard out of the shipped function body: from its authorization
  // marker to just before session.term.focus().
  const marker = "// AUTH-20261105-P3-FOCUS-GUARD";
  const start = focusGuardBlock.indexOf(marker);
  assert.notEqual(start, -1, "the AUTH-20261105-P3-FOCUS-GUARD marker comment is gone from focusTerminalIfPossible");
  const tail = focusGuardBlock.indexOf("session.term.focus();", start);
  assert.notEqual(tail, -1, "session.term.focus() is no longer the last statement of focusTerminalIfPossible");
  const guard = focusGuardBlock.slice(start, tail);
  // The prose comment NAMES the banned symbols to forbid them; the bans are
  // pinned on the CODE only — comment lines stripped.
  const guardCode = guard.split("\n").filter((l) => !l.trim().startsWith("//")).join("\n");

  assert.match(guardCode, /document\.activeElement/, "the guard reads document.activeElement");
  assert.match(guardCode, /tagName === "TEXTAREA" \|\| .*tagName === "INPUT" \|\| .*\.isContentEditable/,
    "the editable three-way read is the same trio as the ownership predicate");
  assert.match(guardCode, /node\.dataset && node\.dataset\.instanceId/,
    "the ancestor read requires a TRUTHY data-instance-id stamp");
  // The self-excitation ban of the authorization, pinned on the guard code:
  assert.doesNotMatch(guardCode, /\.focus\s*\(/, "the guard must never call focus()");
  assert.doesNotMatch(guardCode, /ensureIosImeDirectInputBridge\s*\(/, "the guard must never call ensureIosImeDirectInputBridge()");
  assert.doesNotMatch(guardCode, /dispatchEvent|addEventListener/, "the guard must never touch events");
  assert.doesNotMatch(guardCode, /\.dataset\.[A-Za-z]+\s*=[^=]/, "the guard must never write the DOM");
  assert.match(guardCode, /if \(!hostAncestor\) return;/,
    "the guard is an early return before session.term.focus()");
});
