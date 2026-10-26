// Behavioral tests for the unknown-id ghost selection of index.html's
// selectInstance/renderTabs pair (issue #127, family of #120 / PR #126):
// selectInstance(id) with an id state.instances cannot resolve — the
// create/restart flows' `await refresh(); selectInstance(res.id)` when the
// list does not carry res.id yet — runs render() FIRST, and renderTabs'
// invalid-selection fix nests selectInstance(null) → auto-select →
// selectInstance(B) → activate(B), which shows B's panel and re-points
// state.activeInst to B. The outer frame then kept running with stale
// locals: prevRenderer.deactivate() re-hid the panel the nested activation
// had just shown (previousID resolves to the previously-shown kind, the
// ghost id defaults to 'pty', so prevRenderer !== renderer fires whenever
// prev is any web kind), hideWebPanels() erased every web panel anyway, and
// renderer.activate({id, instance: undefined}) opened an EMPTY terminal for
// an id the server does not know — kinds/pty.js creates and shows its
// session before its own `if (!inst) return` — while the tab highlight
// stayed on B. No self-heal: re-clicking B's tab dies on the same-id
// early-return and the 2s poll never re-runs activate(). The fix is the
// takeover guard right after render(): `if (state.activeInst !== id)
// return;` — a frame whose render() re-pointed the selection is stale and
// must not touch the screen.
//
// The logic under test lives in index.html's inline script, so this file
// lives in testdata/ instead of static/ on purpose: static/* is embedded and
// served to every browser, and test code does not belong in the web root.
//
// The functions are sliced whole out of the shipped page into a
// new Function(...) sandbox against a stubbed DOM/window/state — the
// assertions run against the shipped code rather than a copy of it (same
// arrangement as web_delete_autoselect.test.mjs). The kind renderers are
// stubs that honour the shipped activate()/deactivate() contract, and the
// pty stub mirrors kinds/pty.js's session-before-inst-resolution order so
// "no terminal was opened for the ghost id" is a real observation, not a
// tautology. Run: node --test testdata/web_ghost_select.test.mjs (also
// wired into terminal_status_test.go for `go test`).
import test from "node:test";
import assert from "node:assert/strict";
import { readFileSync } from "node:fs";
import { fileURLToPath } from "node:url";
import { dirname, join } from "node:path";
import vm from "node:vm";

const here = dirname(fileURLToPath(import.meta.url));
const staticDir = join(here, "..", "static");
const indexSource = readFileSync(join(staticDir, "index.html"), "utf8");
const frameworkSource = readFileSync(join(staticDir, "framework.js"), "utf8");

// --- source slicing helpers (same style as web_delete_autoselect.test.mjs) -

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

  // Ordered event log across ALL renderers and the shared hide, so
  // "nothing touched the screen after the nested activate" is a checkable
  // property of the sequence, not of one renderer's counters.
  const events = [];

  // Stubs honouring the shipped renderer contract: activate shows its own
  // panel and hides the terminal container, deactivate hides the panel.
  const renderers = {};
  for (const [kind, panelId] of Object.entries(PANEL_OF)) {
    renderers[kind] = {
      kind,
      panelId,
      activate(session) {
        events.push(`activate:${kind}:${session.id}`);
        els["terminal-container"].style.display = "none";
        els[this.panelId].hidden = false;
      },
      deactivate() {
        events.push(`deactivate:${kind}`);
        els[this.panelId].hidden = true;
      },
      destroyFrame() {},
      syncStatus() {},
    };
  }
  // Mirrors kinds/pty.js activate(): shows the terminal container and
  // CREATES the per-id session BEFORE resolving state.instances (its own
  // `if (!inst) return` sits after _createSession), so an unresolvable id
  // really does get an empty xterm host on the shipped page. `created` is
  // the session ledger the ghost cases assert against.
  renderers.pty = {
    kind: "pty",
    created: [],
    activate(session) {
      events.push(`activate:pty:${session.id}`);
      els["terminal-container"].style.display = "";
      els["opencode-panel"].hidden = true;
      if (!this.created.includes(session.id)) this.created.push(session.id);
      // kinds/pty.js resolves state.instances only AFTER the session exists
      // (`const inst = …; if (!inst) return;`) — the ghost id would stop
      // right here with an empty, SHOWN xterm host on the shipped page.
      const inst = state.instances.find((i) => i.id === session.id);
      if (!inst) return;
      // A resolvable id would now proceed to the transport branches; this
      // harness never needs them (no live terminal kind is exercised).
    },
    deactivate() {
      events.push(`deactivate:pty`);
      els["terminal-container"].style.display = "none";
    },
  };

  const statuses = [];
  const destroyed = [];
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
    // alone (same reduction as web_delete_autoselect.test.mjs, PR #126
    // review note).
    function render() { renderTabs(); renderWorkspace(); }
    ${sliceBlock(indexSource, "function renderTabs")}
    ${sliceBlock(indexSource, "function selectInstance")}
    return { selectInstance, renderTabs, renderWorkspace };
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
    // Stub mirroring ONLY the falsy-id half of the shipped first line
    // (`if (!id || id === state.activeInst) return;`), so `destroyed`
    // records real teardowns instead of the harmless null previousID of
    // the first activation — and so the `id === state.activeInst` half
    // stays OBSERVED: without the takeover guard a ghost frame would reach
    // the tail with previousID and the ledger would go red.
    (id) => { if (!id) return; destroyed.push(id); },
    () => null,
    () => {},
    () => {},
    () => {}
  );

  const lastIndexOf = (prefix) => {
    for (let i = events.length - 1; i >= 0; i--) {
      if (events[i].startsWith(prefix)) return i;
    }
    return -1;
  };

  return { api, els, state, renderers, statuses, events, destroyed, lastIndexOf };
}

// The visible panel of the moment: every web panel reported un-hidden.
function visiblePanels(h) {
  return Object.values(PANEL_OF).filter((id) => !h.els[id].hidden);
}

// --- the reported regression: selectInstance with an unknown id -------------

test("selectInstance(GHOST) on two web instances keeps the auto-selected panel and opens no ghost terminal", () => {
  const h = harness();
  const P = { id: "P", kind: "dsh-web", status: "running", worktree_id: "wt1" };
  const Q = { id: "Q", kind: "dsh-web", status: "running", worktree_id: "wt1" };
  h.state.instances = [P, Q];
  h.api.selectInstance("P");
  assert.deepEqual(visiblePanels(h), ["dsh-panel"], "P's panel shows while P is active");

  // The issue's measured probe: an id state.instances cannot resolve.
  h.api.selectInstance("GHOST");

  assert.equal(h.state.activeInst, "P",
    "the takeover's pick survives — the outer ghost frame must not overwrite the selection");
  assert.equal(h.els["dsh-panel"].hidden, false,
    "P's panel stays visible — prevRenderer.deactivate() and hideWebPanels() must not run from the stale outer frame (the reported re-cover)");
  assert.deepEqual(h.renderers.pty.created, [],
    "NO terminal session for GHOST — kinds/pty.js would have created and shown an empty xterm host for the unresolved id");
  assert.equal(h.lastIndexOf("activate:pty"), -1, "the pty renderer never activated");
  assert.notEqual(h.lastIndexOf("activate:dsh-web:P"), -1, "the nested auto-select did activate P");
  assert.ok(h.lastIndexOf("activate:dsh-web:P") > h.lastIndexOf("deactivate:dsh-web"),
    "no renderer.deactivate ran after the nested activate");
  assert.equal(h.els["terminal-container"].style.display, "none",
    "the terminal container stays hidden — no empty terminal covers the web panel");
  assert.deepEqual(h.destroyed, [], "the takeover return skips even the tail destroy call — reconcile owns it from the next tick on");
  assert.match(h.els["tabs-container"].innerHTML, /class="tab active"[^>]*data-instance-id="P"/,
    "the tab highlight shows P, agreeing with the screen");
  assert.ok(!h.els["tabs-container"].innerHTML.includes("GHOST"),
    "no tab exists for the ghost id — the highlight cannot point at it either");
});

test("a normal valid switch still deactivates the previous renderer before activating the new one", () => {
  const h = harness();
  const A = { id: "A", kind: "dsh-web", status: "running", worktree_id: "wt1" };
  const B = { id: "B", kind: "reasonix", status: "running", worktree_id: "wt1" };
  h.state.instances = [A, B];
  h.api.selectInstance("A");
  assert.deepEqual(visiblePanels(h), ["dsh-panel"]);

  h.api.selectInstance("B");

  assert.equal(h.state.activeInst, "B", "the takeover guard is inert for a valid id — render() does not re-point the selection");
  assert.deepEqual(visiblePanels(h), ["reasonix-panel"],
    "the previous web kind's panel is hidden before the new renderer activates (the #96/#101 mutual exclusion)");
  assert.ok(h.lastIndexOf("deactivate:dsh-web") > h.lastIndexOf("activate:dsh-web:A"),
    "prevRenderer.deactivate() still runs on the honest switch path");
  assert.ok(h.lastIndexOf("activate:reasonix:B") > h.lastIndexOf("deactivate:dsh-web"),
    "deactivate precedes activate in the event order");
});

// --- non-regression: the #120 / #126 delete-path survivor --------------------

test("selectInstance(null) with remaining instances keeps the auto-selected panel visible", () => {
  const h = harness();
  const A = { id: "A", kind: "dsh-web", status: "running", worktree_id: "wt1" };
  const B = { id: "B", kind: "reasonix", status: "running", worktree_id: "wt1" };
  h.state.instances = [A, B];
  h.api.selectInstance("A");

  // deleteInstance()'s client half: A is gone; renderTabs' invalid-fix
  // calls selectInstance(null), whose render() nests the auto-select.
  h.state.instances = [B];
  h.api.selectInstance(null);

  assert.equal(h.state.activeInst, "B", "the null frame's takeover guard fired and left the auto-select's pick alone");
  assert.deepEqual(visiblePanels(h), ["reasonix-panel"],
    "the panel the nested auto-select showed survives the outer selectInstance(null) — #120/#126's contract, now enforced by the guard too");
  assert.ok(h.lastIndexOf("activate:reasonix:B") > h.lastIndexOf("deactivate:reasonix"),
    "nothing deactivated B's panel after it was activated");
});

test("deleting the last instance onto the empty state still hides every panel", () => {
  const h = harness();
  const A = { id: "A", kind: "dsh-web", status: "running", worktree_id: "wt1" };
  h.state.instances = [A];
  h.api.selectInstance("A");

  h.state.instances = [];
  h.api.selectInstance(null);

  assert.equal(h.state.activeInst, null, "nothing left to select");
  assert.deepEqual(visiblePanels(h), [],
    "renderWorkspace's empty branch still hides every web panel (issue #101)");
  assert.equal(h.els["empty-state"].style.display, "block", "the empty state shows");
});

// --- create/restart semantics (issue #127 Q4) ---------------------------------

test("selectInstance(res.id) with res.id missing from the list keeps the auto-selected instance on screen until the next tick", () => {
  const h = harness();
  const P = { id: "P", kind: "dsh-web", status: "running", worktree_id: "wt1" };
  const Q = { id: "Q", kind: "dsh-web", status: "stopped", worktree_id: "wt1" };
  h.state.instances = [P, Q];
  h.api.selectInstance("P");

  // startInstance()'s tail: `await refresh(); selectInstance(res.id)` when
  // the refreshed list does not carry the created id yet.
  h.api.selectInstance("NEW");

  assert.equal(h.state.activeInst, "P",
    "the auto-select's pick (running P) stays active — no ghost selection, no thrown-away focus");
  assert.equal(h.els["dsh-panel"].hidden, false, "the screen keeps showing P");
  assert.deepEqual(h.renderers.pty.created, [], "no empty terminal opened for the not-yet-listed id");
  assert.deepEqual(h.destroyed, [], "the takeover return skips even the tail destroy call — reconcile owns it from the next tick on");
  assert.ok(!h.els["tabs-container"].innerHTML.includes("NEW"),
    "the tab list simply does not show NEW this tick — the honest state");

  // The next refresh tick publishes NEW: the tab appears unselected, and
  // selecting it now works normally (guard inert for a valid id).
  const NEW = { id: "NEW", kind: "dsh-web", status: "starting", worktree_id: "wt1" };
  h.state.instances = [P, Q, NEW];
  h.api.renderTabs();
  assert.match(h.els["tabs-container"].innerHTML, /data-instance-id="NEW"/,
    "NEW's tab rides the next poll tick (the create-flow self-heal)");
  assert.ok(!h.els["tabs-container"].innerHTML.match(/class="tab active"[^>]*data-instance-id="NEW"/),
    "NEW's tab is unselected while P stays on screen");

  h.api.selectInstance("NEW");
  assert.equal(h.state.activeInst, "NEW", "once listed, NEW selects normally — no retry code was needed");
  assert.equal(h.els["dsh-panel"].hidden, false, "NEW's panel shows");
});

test("selectInstance(GHOST) with zero instances leaves the empty state on screen", () => {
  const h = harness();
  h.state.instances = [];

  h.api.selectInstance("GHOST");

  assert.equal(h.state.activeInst, null,
    "nothing to auto-select — the invalid-fix's selectInstance(null) stands, the ghost frame returns at the guard");
  assert.deepEqual(visiblePanels(h), [], "no web panel over the empty state");
  assert.equal(h.els["empty-state"].style.display, "block", "the empty state shows");
  assert.deepEqual(h.renderers.pty.created, [], "no empty terminal for GHOST");
});

test("the same-id early-return still short-circuits a redundant re-selection", () => {
  const h = harness();
  const P = { id: "P", kind: "dsh-web", status: "running", worktree_id: "wt1" };
  h.state.instances = [P];
  h.api.selectInstance("P");
  h.api.selectInstance("P");
  assert.deepEqual(h.events.filter((e) => e === "activate:dsh-web:P"), ["activate:dsh-web:P"],
    "activate ran exactly once — the guard sits BELOW the same-id early-return, not before it");
});

// --- Q3: framework.js's window.selectInstance is shadowed, not live --------

test("index.html's inline function selectInstance is the live global, shadowing framework.js's copy", () => {
  // Static half: classic scripts run in document order; framework.js loads
  // at index.html:2580 and assigns window.selectInstance, while the inline
  // block (opened AFTER it) declares `function selectInstance` whose
  // hoisted global binding overwrites that property at evaluation time.
  const fwTag = '<script src="/static/framework.js"></script>';
  const fwAt = indexSource.indexOf(fwTag);
  const inlineAt = indexSource.indexOf("<script>\n");
  assert.notEqual(fwAt, -1, "index.html no longer loads framework.js");
  assert.notEqual(inlineAt, -1, "index.html no longer has an inline <script> block");
  assert.ok(fwAt < inlineAt, "framework.js must load BEFORE the inline block for the shadowing argument");
  assert.ok(inlineScript(indexSource).includes("function selectInstance(id) {"),
    "the inline block no longer declares function selectInstance — the shadowing premise moved");
  assert.ok(frameworkSource.includes("window.selectInstance = function (id) {"),
    "framework.js no longer installs its window.selectInstance — the shadowing premise moved");
  assert.equal(indexSource.split("window.selectInstance").length - 1, 0,
    "index.html must never reassign window.selectInstance by hand — a manual re-assignment after the inline block would resurrect framework.js's stale copy");

  // Runtime half: evaluate framework.js whole, then the shipped inline
  // declaration, in ONE vm global — the last writer wins, and it is the
  // guarded index.html function.
  const ctx = { setInterval: () => 0, clearInterval: () => {}, console };
  ctx.window = ctx;
  vm.runInNewContext(frameworkSource, ctx);
  assert.equal(typeof ctx.selectInstance, "function", "framework.js installed its copy");
  assert.ok(!String(ctx.selectInstance).includes("state.activeInst !== id"),
    "framework.js's copy is the un-guarded one (it must stay the losing writer)");
  vm.runInNewContext(sliceBlock(indexSource, "function selectInstance"), ctx);
  assert.ok(String(ctx.selectInstance).includes("if (state.activeInst !== id) return;"),
    "after the inline script evaluates, the LIVE global selectInstance is index.html's guarded function — framework.js:163 is unreachable on the shipped page");
});
