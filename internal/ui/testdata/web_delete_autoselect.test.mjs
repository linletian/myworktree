// Behavioral tests for the delete-foreground-instance → auto-select flow of
// index.html's selectInstance/renderTabs/renderWorkspace trio (issue #120
// follow-up): deleting the ACTIVE instance makes renderTabs' invalid-selection
// branch call selectInstance(null), whose render() then nests renderTabs'
// auto-select — selectInstance(B) → renderer.activate(B) SHOWS B's panel —
// and the outer selectInstance(null) used to keep running into an
// unconditional hideWebPanels() that erased the panel the nested activation
// had just shown: a black workspace with no placeholder, no empty state and
// NO SELF-HEAL — the 2s poll never refills panel visibility (activate() is
// the sole panel-show entry point) and re-clicking the same tab dies on the
// same-id early-return; only another instance's tab, or a reload, brings it
// back. The terminal kinds escaped because their container visibility is
// driven by renderWorkspace, not by the web panels.
//
// The logic under test lives in index.html's inline script, so this file
// lives in testdata/ instead of static/ on purpose: static/* is embedded and
// served to every browser, and test code does not belong in the web root.
//
// The functions are sliced whole out of the shipped page into a
// new Function(...) sandbox against a stubbed DOM/window/state — the
// assertions run against the shipped code rather than a copy of it (same
// arrangement as web_status_sync.test.mjs). The kind renderers are stubs
// that honour the shipped activate()/deactivate() contract (kinds/dsh_web.js
// :70 etc.: activate shows its own panel and hides the terminal container,
// deactivate hides the panel) — their internals are pinned by
// web_status_sync.test.mjs and TestWebRenderersStoppedSwitchHidesAllFrames.
// Run: node --test testdata/web_delete_autoselect.test.mjs (also wired into
// terminal_status_test.go for `go test`).
import test from "node:test";
import assert from "node:assert/strict";
import { readFileSync } from "node:fs";
import { fileURLToPath } from "node:url";
import { dirname, join } from "node:path";

const here = dirname(fileURLToPath(import.meta.url));
const staticDir = join(here, "..", "static");
const indexSource = readFileSync(join(staticDir, "index.html"), "utf8");

// --- source slicing helpers (same style as web_status_sync.test.mjs) -----

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

function sliceStatement(src, header) {
  const start = src.indexOf(header);
  assert.notEqual(start, -1, `source no longer contains ${JSON.stringify(header)}`);
  const end = src.indexOf(";", start);
  assert.notEqual(end, -1, `no statement terminator after ${JSON.stringify(header)}`);
  return src.slice(start, end + 1);
}

// --- harness ------------------------------------------------------------------

function makeEl(id) {
  const el = {
    id,
    hidden: false,
    style: {},
    textContent: "",
    innerHTML: "",
    _classes: new Set(),
  };
  el.classList = {
    add: (c) => el._classes.add(c),
    remove: (c) => el._classes.delete(c),
    contains: (c) => el._classes.has(c),
    toggle: (c, flag) => {
      if (flag) el._classes.add(c);
      else el._classes.delete(c);
    },
  };
  return el;
}

const PANEL_OF = {
  "dsh-web": "dsh-panel",
  "opencode-web": "opencode-panel",
  reasonix: "reasonix-panel",
};

function harness() {
  const els = {};
  for (const id of [
    "tabs-container", "workspace", "empty-state", "terminal-container",
    "instance-actions", "terminal-controls", "opencode-panel",
    "reasonix-panel", "dsh-panel",
  ]) {
    els[id] = makeEl(id);
  }
  const document = { getElementById: (id) => els[id] || null };

  const state = {
    activeWT: "wt1",
    activeInst: null,
    instances: [],
    renamingInst: null,
    renamingWT: null,
  };

  // Stubs honouring the shipped renderer contract: activate shows its own
  // panel and hides the terminal container, deactivate hides the panel.
  const renderers = {};
  for (const [kind, panelId] of Object.entries(PANEL_OF)) {
    renderers[kind] = {
      kind,
      panelId,
      calls: [],
      activate(session) {
        this.calls.push(session.id);
        els["terminal-container"].style.display = "none";
        els[this.panelId].hidden = false;
      },
      deactivate() {
        els[this.panelId].hidden = true;
      },
      destroyFrame() {},
      syncStatus() {},
    };
  }
  renderers.pty = {
    kind: "pty",
    calls: [],
    activate(session) {
      this.calls.push(session.id);
      els["terminal-container"].style.display = "";
    },
    deactivate() {
      els["terminal-container"].style.display = "none";
    },
  };

  const statuses = [];
  const api = new Function(
    "state", "window", "document", "updateStatus", "escapeHtml",
    "maybeDestroyInactiveStoppedSession", "getActiveTerminalSession",
    "focusTerminalIfPossible", "renderTerminalSessions", "syncQuickKeysPanel",
    `
    ${sliceStatement(indexSource, "const INSTANCE_LIVE_STATUSES")}
    ${sliceStatement(indexSource, "const INSTANCE_PENDING_STATUSES")}
    ${sliceBlock(indexSource, "function isInstanceLiveStatus")}
    ${sliceBlock(indexSource, "function isInstancePendingStatus")}
    ${sliceBlock(indexSource, "function isInstanceTerminalStatus")}
    ${sliceBlock(indexSource, "function instanceStatusLabel")}
    ${sliceBlock(indexSource, "function hideWebPanels")}
    ${sliceBlock(indexSource, "function renderWorkspace")}
    // Reduced on purpose: the shipped render() also runs renderSidebar /
    // renderModals, neither of which touches the three web panels'
    // visibility — the panel contract lives in renderTabs + renderWorkspace
    // alone, and slicing the two big ones in would only drag their
    // unrelated DOM dependencies into the harness (PR #126 review note).
    function render() { renderTabs(); renderWorkspace(); }
    ${sliceBlock(indexSource, "function renderTabs")}
    ${sliceBlock(indexSource, "function selectInstance")}
    return { selectInstance, renderTabs, renderWorkspace, hideWebPanels };
  `
  )(
    state,
    { KindRenderers: renderers },
    document,
    (s) => statuses.push(s),
    // Stub, not the shipped slice: the real escapeHtml's HTML entities
    // ("&amp;") contain ';', which the statement slicer would cut at.
    // renderTabs only needs SOME escape for display names here.
    (s) => String(s ?? ""),
    () => {},
    () => null,
    () => {},
    () => {},
    () => {}
  );

  return { api, els, state, renderers, statuses };
}

// The visible panel of the moment: every web panel reported un-hidden.
function visiblePanels(h) {
  return Object.values(PANEL_OF).filter((id) => !h.els[id].hidden);
}

// --- the reported regression: delete the foreground instance --------------

test("deleting the active web instance leaves the auto-selected web instance's panel visible", () => {
  const h = harness();
  const A = { id: "A", kind: "dsh-web", status: "running", worktree_id: "wt1" };
  const B = { id: "B", kind: "dsh-web", status: "running", worktree_id: "wt1" };
  h.state.instances = [A, B];
  h.api.selectInstance("A");
  assert.equal(h.els["dsh-panel"].hidden, false, "A's panel shows while A is active");

  // deleteInstance()'s client half: the instance vanishes from the polled
  // list, then refresh() → render() → renderTabs() fixes up the selection.
  h.state.instances = [B];
  h.api.renderTabs();

  assert.equal(h.state.activeInst, "B", "the deleted tab auto-advances to B");
  assert.deepEqual(h.renderers["dsh-web"].calls, ["A", "B"], "B was activated exactly once");
  assert.equal(h.els["dsh-panel"].hidden, false,
    "B's panel stays visible — the outer selectInstance(null) must not hide the panel the nested auto-select just showed (the reported black screen)");
  assert.equal(h.els["terminal-container"].style.display, "none",
    "the terminal container stays hidden for a web foreground");
});

test("deleting the active TERMINAL instance shows the auto-selected web instance's panel", () => {
  const h = harness();
  const A = { id: "A", kind: "pty", status: "running", worktree_id: "wt1" };
  const B = { id: "B", kind: "dsh-web", status: "running", worktree_id: "wt1" };
  h.state.instances = [A, B];
  h.api.selectInstance("A");
  assert.equal(h.els["dsh-panel"].hidden, true, "no web panel while a terminal is active");
  // '' is the CSS default (visible): renderWorkspace sets "block", then the
  // pty stub's activate mirrors kinds/pty.js and restores the default ''.
  assert.notEqual(h.els["terminal-container"].style.display, "none", "terminal visible");

  h.state.instances = [B];
  h.api.renderTabs();

  assert.equal(h.state.activeInst, "B");
  assert.equal(h.els["dsh-panel"].hidden, false,
    "the web panel the auto-select showed survives the outer selectInstance(null)");
  assert.equal(h.els["terminal-container"].style.display, "none",
    "the renderer hid the terminal container as it took over");
});

test("deleting the active web instance onto a terminal auto-select keeps panels hidden", () => {
  const h = harness();
  const A = { id: "A", kind: "dsh-web", status: "running", worktree_id: "wt1" };
  const B = { id: "B", kind: "pty", status: "running", worktree_id: "wt1" };
  h.state.instances = [A, B];
  h.api.selectInstance("A");
  assert.equal(h.els["dsh-panel"].hidden, false);

  h.state.instances = [B];
  h.api.renderTabs();

  assert.equal(h.state.activeInst, "B");
  assert.deepEqual(visiblePanels(h), [],
    "a terminal foreground shows no web panel — renderWorkspace's non-web branch still hides them all");
  assert.notEqual(h.els["terminal-container"].style.display, "none", "terminal visible");
});

test("deleting the active web instance onto a DIFFERENT web kind shows only the new kind's panel", () => {
  const h = harness();
  const A = { id: "A", kind: "dsh-web", status: "running", worktree_id: "wt1" };
  const B = { id: "B", kind: "reasonix", status: "running", worktree_id: "wt1" };
  h.state.instances = [A, B];
  h.api.selectInstance("A");
  assert.deepEqual(visiblePanels(h), ["dsh-panel"]);

  h.state.instances = [B];
  h.api.renderTabs();

  assert.equal(h.state.activeInst, "B");
  assert.deepEqual(h.renderers.reasonix.calls, ["B"], "the reasonix renderer activated once");
  assert.deepEqual(visiblePanels(h), ["reasonix-panel"],
    "cross-kind delete pins BOTH directions at once — the new kind's panel shown AND the old kind's hidden; the same-kind delete cases cannot tell those apart because dsh-panel.hidden=false is equally satisfied by A's panel never having been hidden (PR #126 review)");
});

// --- the mutual-exclusion contract the hide still has to serve -------------

test("switching between web kinds shows only the newly activated panel", () => {
  const h = harness();
  const A = { id: "A", kind: "dsh-web", status: "running", worktree_id: "wt1" };
  const B = { id: "B", kind: "opencode-web", status: "running", worktree_id: "wt1" };
  h.state.instances = [A, B];
  h.api.selectInstance("A");
  assert.deepEqual(visiblePanels(h), ["dsh-panel"]);

  h.api.selectInstance("B");
  assert.deepEqual(visiblePanels(h), ["opencode-panel"],
    "the panel of the previous web kind is hidden before the new renderer activates (the #96 half/half stack)");
});

test("switching a web foreground to a terminal hides every web panel", () => {
  const h = harness();
  const A = { id: "A", kind: "reasonix", status: "running", worktree_id: "wt1" };
  const B = { id: "B", kind: "pty", status: "running", worktree_id: "wt1" };
  h.state.instances = [A, B];
  h.api.selectInstance("A");
  assert.deepEqual(visiblePanels(h), ["reasonix-panel"]);

  h.api.selectInstance("B");
  assert.deepEqual(visiblePanels(h), [],
    "renderWorkspace's non-web branch is the backstop for terminal kinds");
});

// --- the empty-state backstop stays intact -------------------------------------

test("deleting the LAST instance hides every web panel and shows the empty state", () => {
  const h = harness();
  const A = { id: "A", kind: "dsh-web", status: "running", worktree_id: "wt1" };
  h.state.instances = [A];
  h.api.selectInstance("A");

  h.state.instances = [];
  h.api.renderTabs();

  assert.equal(h.state.activeInst, null, "nothing left to select");
  assert.deepEqual(visiblePanels(h), [],
    "renderWorkspace's empty branch hides every web panel over the empty state (issue #101)");
  assert.equal(h.els["empty-state"].style.display, "block");
});

test("a stopped auto-selected web instance still reaches its stopped placeholder", () => {
  const h = harness();
  const A = { id: "A", kind: "dsh-web", status: "running", worktree_id: "wt1" };
  const B = { id: "B", kind: "dsh-web", status: "stopped", worktree_id: "wt1" };
  h.state.instances = [A, B];
  h.api.selectInstance("A");

  h.state.instances = [B];
  h.api.renderTabs();

  assert.equal(h.state.activeInst, "B");
  assert.deepEqual(h.renderers["dsh-web"].calls, ["A", "B"]);
  assert.equal(h.els["dsh-panel"].hidden, false,
    "even a stopped auto-select stays visible — the placeholder is its content (the panel, not the workspace, owns it)");
});
