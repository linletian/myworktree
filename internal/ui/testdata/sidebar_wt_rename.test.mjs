// Behavioral tests for the issue #122 sidebar worktree-row rename:
// double-clicking a worktree row turns its name cell into an input that
// commits on Enter / non-empty blur and rolls back on Escape / empty blur —
// the instance-tab rename state machine mirrored one-for-one onto the
// sidebar (plan decision H) — plus the guards that keep the 2s poll, the 60s
// diverge timer and selectWorktree's per-row diverge fetch from rebuilding
// #wt-list (and tearing the input plus its IME composition down) while a row
// is being renamed (pitfall #1/#2/#4, plan decision G), the focus guard that
// stops the terminal from stealing
// focus from that input, and the mutual exclusion that keeps at most ONE
// rename input in the page at any moment (plan decision D).
//
// Cases 10 and 11 pin the two races the code review turned up (B-F1 / B-F2):
// B-F1 — fetchDivergeSingle's late /api/worktree/diverged response, which
// selectWorktree fires on the FIRST click of a double-click while the rename
// input does not exist yet, must merge state.diverged WITHOUT rebuilding
// #wt-list; B-F2 — commitWTRename's PATCH response must not tear down a NEW
// editing-state input that a second double-click opened during the round trip,
// while its state.worktrees splice and its refresh() resync stay unconditional.
//
// Cases 12 and 13 pin the blur re-entrancy race that only a REAL browser
// shows: removing the focused input inside an innerHTML write makes Chrome
// dispatch blur on that node while getElementById still resolves it, so
// onblur re-enters the commit handler with the editing state already cleared.
// Case 12 covers the self path (Enter / Escape / genuine blur on one row) and
// case 13 the mutual-exclusion cross path (startWTRename tearing a tab input
// down through renderTabs, and startRename tearing a row input down through
// renderSidebar) — neither teardown may commit what it removed.
//
// Cases 14 and 15 pin review A-F1, the sibling race those state guards cannot
// reach: when a THIRD party rebuilds the list, the row's editing state is
// still set, so a re-entered commit passes its guard and PATCHes text nobody
// confirmed — the teardown point has to carry its own guard. Case 14 drives
// commitRename's success render() (which rebuilds BOTH containers) with a
// sidebar row mid-rename, and case 15 parks openTerminal / openFinder in
// their up-to-10s await, opens a row inside that window, then releases the
// response and checks the finally against the live input. Both pair every
// expectation with a control that renames nobody, so muting all repaints could
// not pass either.
//
// Same arrangement as sidebar_collapse.test.mjs: every function is SLICED
// from the shipped index.html (and window.state from framework.js) and
// evaluated against stubbed browser state — no second copy of the logic — so
// a drift between the slice anchors and the served source fails loudly at
// slice time. The stubbed renderers do more than record: they snapshot
// state.renamingWT AT CALL TIME and create / destroy the input element the
// way the shipped template branch does, so the cases can tell "this render
// built the input" from "this render restored static text". The template
// itself (its escaping included) is pinned by the Go anchor tests.
// Run:
//   node --test testdata/sidebar_wt_rename.test.mjs
// (also wired into terminal_status_test.go for `go test`).
import test from "node:test";
import assert from "node:assert/strict";
import { readFileSync } from "node:fs";
import { fileURLToPath } from "node:url";
import { dirname, join } from "node:path";

const here = dirname(fileURLToPath(import.meta.url));
const staticDir = join(here, "..", "static");
const indexSource = readFileSync(join(staticDir, "index.html"), "utf8");
const frameworkSource = readFileSync(join(staticDir, "framework.js"), "utf8");

// --- source slicing helpers (same approach as sidebar_collapse.test.mjs) ---

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

const script = inlineScript(indexSource);

// window.state arrives through its SHIPPED initializer rather than a literal
// copy here, so the renamingWT / renamingWTValue fields the cases read are
// the ones framework.js actually declares.
const stateInit = sliceStatement(frameworkSource, "window.state = {");

function makeState() {
  const win = {};
  try {
    new Function("window", stateInit)(win);
  } catch (e) {
    // sliceStatement stops at the first `;`, so a semicolon written into a
    // comment inside this object literal would hand new Function a truncated
    // literal — name the cause instead of leaking a bare SyntaxError.
    throw new Error(`the window.state slice from framework.js does not parse (${e.message}) — check for a ";" inside its initializer`);
  }
  assert.ok(win.state && "renamingWT" in win.state && "renamingWTValue" in win.state,
    "framework.js window.state must declare renamingWT / renamingWTValue (issue #122)");
  return win.state;
}

// --- factories: the shipped functions, sliced whole -----------------------

// The rename state machines of both halves of the sidebar/tab pair ride in
// together: the mutual-exclusion calls (startWTRename -> renderTabs,
// startRename -> renderSidebar) are shipped code, not harness plumbing.
// commitRename rides in too — startWTRename's mutual-exclusion teardown of a
// live tab input re-enters it through onblur in a real browser, so that path
// has to be the shipped one and not a stub. renderSidebar is injected as a
// spy because the sidebar renderer is the thing being guarded here, not the
// thing under test (its template is pinned by the Go anchors), and refresh /
// render are injected because those loops live in pollFactory below.
const renameFactory = new Function("deps", `
  const { api, console, document, requestAnimationFrame, renderSidebar, renderTabs, refresh, render, state } = deps;
  ${[
    "function startRename(id, event)",
    "function commitRename(id, event)",
    "function startWTRename(id, event)",
    "function commitWTRename(id, event)",
    "function cancelWTRename()",
    "function handleWTRenameKeydown(id, event)",
  ].map((h) => sliceBlock(script, h)).join("\n\n")}
  return { startRename, commitRename, startWTRename, commitWTRename, cancelWTRename, handleWTRenameKeydown };
`);

// refresh(), fetchDiverge() and fetchDivergeSingle() sliced whole, with the
// guards the rename depends on still inside them. refreshInFlight /
// stateVersion mirror the module-level declarations in index.html. Neither
// diverge fetcher is injected: refresh() calls the shipped fetchDiverge
// sliced in this same factory, and fetchDivergeSingle is exported so a case
// can drive it exactly the way selectWorktree's first click does (review
// B-F1 — that request is in flight before the rename input even exists).
const pollFactory = new Function("deps", `
  const { api, console, document, state, renderSidebar, render, refreshGitChanges, syncActiveWebStatus, reconcileTerminalSessions, getActiveInstance, isInstanceLiveStatus, hasLiveTTYConnection, updateStatus } = deps;
  ${sliceStatement(script, "const MAIN_WT_ID")}
  let refreshInFlight = null;
  let stateVersion = 0;
  ${[
    "async function refresh()",
    "async function fetchDiverge()",
    "async function fetchDivergeSingle(id)",
  ].map((h) => sliceBlock(script, h)).join("\n\n")}
  return { refresh, fetchDiverge, fetchDivergeSingle };
`);

// openTerminal() / openFinder() sliced whole, because their
// `finally { renderSidebar() }` is the third-party teardown review A-F1
// flagged: plan R1's exemption ("a user action blurs the input first") holds
// for the spinner-ON render before the await and NOT for this finally — the
// await can park for up to 10s (AbortController), which is plenty of time for
// a double-click to open an editing state that this repaint would destroy.
const openerFactory = new Function("deps", `
  const { api, console, alert, loadingActions, renderSidebar, AbortController, setTimeout, clearTimeout, state } = deps;
  ${[
    "async function openTerminal(id, event)",
    "async function openFinder(id, event)",
  ].map((h) => sliceBlock(script, h)).join("\n\n")}
  return { openTerminal, openFinder };
`);

// windowHasFocus / openDialogs mirror the module-level declarations in
// index.html; the setters below let a case reach the exact preconditions the
// earlier guards demand, so the rename guard is the one that decides.
const focusFactory = new Function("deps", `
  const { state, getActiveTerminalSession, isInstanceLiveStatus } = deps;
  let windowHasFocus = true;
  const openDialogs = new Set();
  ${sliceBlock(script, "function focusTerminalIfPossible()")}
  return { focusTerminalIfPossible, setWindowFocus: (on) => { windowHasFocus = on; } };
`);

// --- stubs ------------------------------------------------------------------

// An input stub that records what the shipped code does to it: focus() and
// select() counts are the whole of the "auto focus + select all" promise.
function makeInput(value) {
  return {
    value,
    focusCount: 0,
    selectCount: 0,
    focus() { this.focusCount += 1; },
    select() { this.selectCount += 1; },
  };
}

function makeEvent(extra = {}) {
  return {
    stopPropagationCount: 0,
    stopPropagation() { this.stopPropagationCount += 1; },
    ...extra,
  };
}

const flush = () => new Promise((resolve) => setTimeout(resolve, 0));

const DEFAULT_WORKTREES = [{ id: "wt-1", name: "fix-the-flaky-thing", branch: "feature/flaky", path: "/tmp/wt-1" }];
// A tab has to exist for the mutual-exclusion cross-path to have anything to
// tear down (startWTRename -> renderTabs -> tab input blur -> commitRename).
const DEFAULT_INSTANCES = [{ id: "inst-1", name: "terminal one", kind: "pty", status: "running" }];

// renameHarness: stubbed page for the rename state machines. The render
// stubs mirror the shipped template's editing-state branch: while
// state.renamingWT (resp. state.renamingInst) names a row (resp. tab), the
// corresponding input element exists; once the state is cleared the next
// render destroys it, i.e. the row goes back to static text — and it fires
// that row's shipped commit handler on the way out, the way Chrome's blur
// does mid-removal (blurOnRemoval: false opts a case out of that).
function renameHarness({ worktrees = DEFAULT_WORKTREES, instances = DEFAULT_INSTANCES, gatePatch = false, gateOpen = false, blurOnRemoval = true } = {}) {
  const state = makeState();
  state.worktrees = worktrees.map((w) => ({ ...w }));
  state.instances = instances.map((i) => ({ ...i }));
  state.activeWT = state.worktrees.length ? state.worktrees[0].id : null;

  const elements = new Map();
  const frames = [];
  const calls = { renderSidebar: [], renderTabs: [], refresh: [], render: [], api: [], alert: [] };
  const blurEvents = [];
  let releasePatch = null;
  let releaseOpen = null;
  let shipped = null; // the sliced handlers, wired once the factory is built

  // Chrome dispatches blur on the focused input WHILE the innerHTML setter is
  // removing it, and getElementById still resolves that node at that moment.
  // So the onblur handler runs HERE, re-entrantly, inside the render — before
  // the node is gone. A stub that simply deleted the element hid the
  // duplicate-PATCH race a real browser hits (issue #122 E2E), so the shipped
  // commit handler is invoked at exactly this point, while the node is still
  // in the map. blurEvents proves the re-entry actually happened, so a case
  // cannot be green just because the model never fired the blur.
  function blurRemoving(prefix, handler) {
    if (!blurOnRemoval || !shipped) return;
    for (const key of Array.from(elements.keys())) {
      if (!key.startsWith(prefix)) continue;
      const id = key.slice(prefix.length);
      blurEvents.push(key);
      handler(id, makeEvent({ type: "blur" }));
    }
  }

  // One container's editing-state branch, shared by renderSidebar (rows),
  // renderTabs (tabs) and render() (both at once): build the input while the
  // state names it; otherwise blur it out of existence first, then drop it.
  const rowValue = (prefix, activeId) => {
    if (prefix === "wt-rename-input-") {
      const w = state.worktrees.find((x) => x.id === activeId);
      return state.renamingWTValue || (w ? w.name : "");
    }
    return state.renamingInstValue || "tab-name";
  };

  // innerHTML = ... destroys every node in the container and builds fresh
  // ones, whatever the state says. So a rebuild costs the focused input its
  // focus, its selection and any live IME composition, and the replacement's
  // value comes from STATE, not from what the user had typed. The model
  // therefore ALWAYS blurs (the node is still findable at that moment) and
  // ALWAYS recreates, instead of leaving a live input untouched just because
  // the state still names it — leaving it alone was a fidelity gap that made
  // review A-F1's third-party teardown look harmless when it is not.
  function settleRow(prefix, activeId, handler) {
    blurRemoving(prefix, handler);
    for (const key of Array.from(elements.keys())) {
      if (key.startsWith(prefix)) elements.delete(key);
    }
    if (activeId) elements.set(prefix + activeId, makeInput(rowValue(prefix, activeId)));
  }

  const commitWT = (id, ev) => shipped.commitWTRename(id, ev);
  const commitTab = (id, ev) => shipped.commitRename(id, ev);

  function renderSidebar() {
    calls.renderSidebar.push({ renamingWT: state.renamingWT, renamingInst: state.renamingInst });
    settleRow("wt-rename-input-", state.renamingWT, commitWT);
  }

  function renderTabs() {
    calls.renderTabs.push({ renamingWT: state.renamingWT, renamingInst: state.renamingInst });
    settleRow("rename-input-", state.renamingInst, commitTab);
  }

  // render() rebuilds BOTH containers, so it settles both. That is precisely
  // how a tab PATCH's success continuation can destroy a SIDEBAR row input
  // (review A-F1) — and it is kept out of calls.renderSidebar / renderTabs so
  // the other cases' counts stay about their own renderers.
  function renderAll() {
    calls.render.push({ renamingWT: state.renamingWT, renamingInst: state.renamingInst });
    settleRow("wt-rename-input-", state.renamingWT, commitWT);
    settleRow("rename-input-", state.renamingInst, commitTab);
  }

  // The server half: a PATCH answers with the stored record carrying the NEW
  // name verbatim — no escaping, no trimming, no length limit (plan C). With
  // gatePatch the response is held until the case releases it, so a second
  // double-click can be slipped INTO the round trip (review B-F2). The
  // instance endpoint is recorded too: a blur re-entering commitRename would
  // fire a self-rename PATCH at /api/instances that nobody asked for.
  async function api(path, opts = {}) {
    calls.api.push({ path, opts });
    if (opts.method === "PATCH" && path === "/api/worktrees") {
      const req = JSON.parse(opts.body);
      const prev = state.worktrees.find((w) => w.id === req.id) || { id: req.id };
      const response = { ...prev, name: req.name };
      if (gatePatch) return new Promise((resolve) => { releasePatch = () => resolve(response); });
      return response;
    }
    if (opts.method === "PATCH" && path === "/api/instances") {
      const req = JSON.parse(opts.body);
      const prev = state.instances.find((i) => i.id === req.id) || { id: req.id };
      if (gatePatch) return new Promise((resolve) => { releasePatch = () => resolve({ ...prev, name: req.name }); });
      return { ...prev, name: req.name };
    }
    // The two spinner POSTs, held open on demand: this is how a case slips a
    // double-click into openTerminal's / openFinder's up-to-10s await window
    // and then watches what the finally does to the sidebar (review A-F1).
    if (opts.method === "POST" && (path === "/api/worktrees/open-terminal" || path === "/api/worktrees/open-finder")) {
      if (gateOpen) return new Promise((resolve) => { releaseOpen = () => resolve({}); });
      return {};
    }
    return {};
  }

  const api_ = renameFactory({
    api,
    console,
    document: { getElementById: (id) => (elements.has(id) ? elements.get(id) : null) },
    requestAnimationFrame: (cb) => { frames.push(cb); return frames.length; },
    renderSidebar,
    renderTabs,
    refresh: () => { calls.refresh.push(1); },
    render: renderAll,
    state,
  });
  shipped = api_;

  // The two spinners, sharing THIS page: same state, same element map, same
  // renderSidebar — so the finally's repaint really is the render that would
  // destroy the row input the case opened mid-await. Timers are faked so the
  // 10s abort never fires (and never dangles the test process).
  const openers = openerFactory({
    api,
    console,
    alert: (msg) => { calls.alert.push(msg); },
    loadingActions: new Set(),
    renderSidebar,
    AbortController: function AbortController() { this.signal = { aborted: false }; this.abort = () => { this.signal.aborted = true; }; },
    setTimeout: () => 1,
    clearTimeout: () => {},
    state,
  });

  return {
    state,
    calls,
    api,
    ...api_,
    ...openers,
    // The stub renderers stay reachable so a case can seed the OTHER half
    // of the page (a live tab input) before driving the shipped handler.
    renderSidebar,
    renderTabs,
    // Which inputs the mid-removal blur fired for — a case asserts on this so
    // its re-entrancy expectation cannot be vacuously true.
    blurEvents,
    input: (id) => (elements.has(`wt-rename-input-${id}`) ? elements.get(`wt-rename-input-${id}`) : null),
    tabInput: (id) => (elements.has(`rename-input-${id}`) ? elements.get(`rename-input-${id}`) : null),
    // Every live rename input in the page — the invariant of decision D is
    // "at most one", so the cases count them rather than eyeballing two ids.
    renameInputCount: () => Array.from(elements.keys()).filter((k) => k.includes("rename-input-")).length,
    flushFrames: () => { for (const cb of frames.splice(0)) cb(); },
    // Releases a gated PATCH response (see gatePatch above).
    resolvePatch: () => { const r = releasePatch; releasePatch = null; if (r) r(); },
    // Releases a gated spinner POST (see gateOpen above).
    resolveOpen: () => { const r = releaseOpen; releaseOpen = null; if (r) r(); },
    flush,
  };
}

function pollHarness({ renamingWT = null } = {}) {
  const state = makeState();
  state.worktrees = DEFAULT_WORKTREES.map((w) => ({ ...w }));
  state.activeWT = "wt-1";
  state.renamingWT = renamingWT;

  const calls = { renderSidebar: [], render: [], refreshGitChanges: [], syncActiveWebStatus: [], api: [] };
  const overlay = { style: { display: "none" } };

  async function api(path) {
    calls.api.push(path);
    if (path === "/api/worktrees") return { worktrees: state.worktrees };
    if (path === "/api/instances") return { instances: [], version: 7 };
    if (path === "/api/tags") return { tags: [] };
    if (path === "/api/branches") return { branches: [] };
    if (path === "/api/main") return { name: "myworktree", branch: "develop" };
    if (path === "/api/worktrees/diverged") return { items: { "wt-1": { diverged: true, ahead: 2 } } };
    // selectWorktree's per-row fetch (the one review B-F1 is about).
    if (path.startsWith("/api/worktree/diverged?id=")) return { items: { "wt-1": { diverged: true, ahead: 5 } } };
    throw new Error(`unexpected api path ${path}`);
  }

  const api_ = pollFactory({
    api,
    console,
    document: { getElementById: (id) => (id === "connection-overlay" ? overlay : null) },
    state,
    renderSidebar: () => { calls.renderSidebar.push({ renamingWT: state.renamingWT }); },
    render: () => { calls.render.push(1); },
    refreshGitChanges: () => { calls.refreshGitChanges.push(1); },
    syncActiveWebStatus: () => { calls.syncActiveWebStatus.push(1); },
    reconcileTerminalSessions: () => {},
    getActiveInstance: () => null,
    isInstanceLiveStatus: () => false,
    hasLiveTTYConnection: () => false,
    updateStatus: () => {},
  });

  return { state, calls, refresh: api_.refresh, fetchDiverge: api_.fetchDiverge, fetchDivergeSingle: api_.fetchDivergeSingle, flush };
}

// --- 1: entering the editing state ------------------------------------------

test("issue #122: double-clicking a worktree row builds the input, then focuses and selects it", async () => {
  const h = renameHarness();
  const ev = makeEvent();

  h.startWTRename("wt-1", ev);

  assert.equal(ev.stopPropagationCount, 1, "the double-click must not reach the row's selectWorktree");
  assert.equal(h.state.renamingWT, "wt-1", "the row owns the rename state");
  assert.equal(h.state.renamingWTValue, "", "it starts empty — the input shows the stored name (plan decision F5)");
  assert.equal(h.calls.renderSidebar.length, 1, "renderSidebar runs synchronously to CREATE the input DOM");
  assert.equal(h.calls.renderSidebar[0].renamingWT, "wt-1", "that render happens while the state names the row, so it builds the input");

  const input = h.input("wt-1");
  assert.ok(input, "the editing-state render created #wt-rename-input-<id>");
  assert.equal(input.value, "fix-the-flaky-thing", "the input is seeded with the current name");
  assert.equal(input.focusCount, 0, "focus waits for the scheduled frame — the input must exist first");
  assert.equal(input.selectCount, 0);

  h.flushFrames();
  assert.equal(input.focusCount, 1, "the frame focuses the input");
  assert.equal(input.selectCount, 1, "and selects the text so the first keystroke replaces it");

  // Esc leaves no residue behind (also part of the rollback contract).
  h.cancelWTRename();
  assert.equal(h.state.renamingWT, null);
  assert.equal(h.input("wt-1"), null, "canceling destroys the input — the row is static text again");
});

// --- 2 / 3 / 5: the commit paths --------------------------------------------

test("issue #122: Enter commits a trimmed PATCH, the response replaces the row, and the branch badge resyncs", async () => {
  const h = renameHarness();
  h.startWTRename("wt-1", makeEvent());
  h.flushFrames();

  const input = h.input("wt-1");
  input.value = "   renamed worktree   ";
  h.handleWTRenameKeydown("wt-1", makeEvent({ key: "Enter" }));
  await h.flush();

  const patch = h.calls.api.filter((c) => c.opts.method === "PATCH");
  assert.equal(patch.length, 1, "exactly one PATCH");
  assert.equal(patch[0].path, "/api/worktrees", "the worktree endpoint, not /api/instances");
  assert.deepEqual(JSON.parse(patch[0].opts.body), { id: "wt-1", name: "renamed worktree" }, "leading/trailing whitespace is trimmed before it hits the wire (plan C)");

  assert.equal(h.state.renamingWT, null, "the editing state is left on commit, whatever the API does");
  assert.equal(h.state.worktrees[0].name, "renamed worktree", "the PATCH response replaces the row in state.worktrees");
  assert.equal(h.state.worktrees[0].branch, "feature/flaky", "only the name changes — branch/path/id ride along untouched");

  const lastRender = h.calls.renderSidebar[h.calls.renderSidebar.length - 1];
  assert.equal(lastRender.renamingWT, null, "the final render restores the row as static text");
  assert.equal(h.calls.refresh.length, 1, "commit resyncs through refresh() so the branch badge keeps List()'s live value (review A-F6)");

  // The blur that follows the Enter-driven rebuild must not double-commit:
  // the input is gone by then, and commitWTRename bails on a missing node.
  h.commitWTRename("wt-1", makeEvent({ type: "blur" }));
  await h.flush();
  assert.equal(h.calls.api.filter((c) => c.opts.method === "PATCH").length, 1, "no second PATCH from the follow-up blur");
});

test("issue #122: an all-whitespace Enter writes nothing back — no PATCH, state reset, text restored", async () => {
  const h = renameHarness();
  h.startWTRename("wt-1", makeEvent());
  h.flushFrames();

  h.input("wt-1").value = "   \t  ";
  h.handleWTRenameKeydown("wt-1", makeEvent({ key: "Enter" }));
  await h.flush();

  assert.deepEqual(h.calls.api, [], "an empty trimmed name never reaches the server (plan C: the old name stays)");
  assert.equal(h.state.renamingWT, null);
  assert.equal(h.state.renamingWTValue, "");
  assert.equal(h.input("wt-1"), null, "the row is static text again");
  assert.equal(h.state.worktrees[0].name, "fix-the-flaky-thing", "and still shows the stored name");
  assert.equal(h.calls.refresh.length, 0, "nothing to resync — no round trip happened");
});

test("issue #122: Escape cancels — no PATCH, state reset, stored name back", async () => {
  const h = renameHarness();
  h.startWTRename("wt-1", makeEvent());
  h.flushFrames();

  const input = h.input("wt-1");
  input.value = "half-typed garbage";
  h.handleWTRenameKeydown("wt-1", makeEvent({ key: "Escape" }));
  await h.flush();

  assert.deepEqual(h.calls.api, [], "Escape never commits — the keystroke is discarded");
  assert.equal(h.state.renamingWT, null);
  assert.equal(h.input("wt-1"), null, "the input is destroyed, not left behind with the discarded text");
  assert.equal(h.state.worktrees[0].name, "fix-the-flaky-thing");

  // A third key on the dead handler is inert (Enter after Escape).
  h.handleWTRenameKeydown("wt-1", makeEvent({ key: "Enter" }));
  await h.flush();
  assert.deepEqual(h.calls.api, [], "no input, no PATCH");
});

test("issue #122 decision H: blur commits a non-empty name exactly like Enter, and a blank blur rolls back", async () => {
  // Non-empty blur = commit (the tab's real behaviour; the issue's bold
  // requirement is parity with the tab, so blur shares commitRename's path).
  const h = renameHarness();
  h.startWTRename("wt-1", makeEvent());
  h.flushFrames();
  h.input("wt-1").value = "  blurred name  ";
  h.commitWTRename("wt-1", makeEvent({ type: "blur" }));
  await h.flush();

  const patch = h.calls.api.filter((c) => c.opts.method === "PATCH");
  assert.equal(patch.length, 1, "a non-empty blur commits");
  assert.deepEqual(JSON.parse(patch[0].opts.body), { id: "wt-1", name: "blurred name" }, "same trimmed payload as the Enter path");
  assert.equal(h.state.worktrees[0].name, "blurred name");
  assert.equal(h.calls.refresh.length, 1);

  // Empty / whitespace-only blur = rollback, no write at all.
  for (const blank of ["", "   ", "\t\n "]) {
    const h2 = renameHarness();
    h2.startWTRename("wt-1", makeEvent());
    h2.flushFrames();
    h2.input("wt-1").value = blank;
    h2.commitWTRename("wt-1", makeEvent({ type: "blur" }));
    await h2.flush();
    assert.deepEqual(h2.calls.api, [], `a blur of ${JSON.stringify(blank)} must not write back`);
    assert.equal(h2.state.renamingWT, null, "and the editing state still closes");
    assert.equal(h2.state.worktrees[0].name, "fix-the-flaky-thing", "the stored name survives");
  }
});

// --- 6: XSS / verbatim transport --------------------------------------------

test("issue #122: a name full of markup reaches the server verbatim — escaping is render-side only", async () => {
  const nasty = `<script>alert(1)</script>"'&`;
  const h = renameHarness({ worktrees: [{ id: "wt-1", name: nasty, branch: "feature/x", path: "/tmp/wt-1" }] });

  h.startWTRename("wt-1", makeEvent());
  h.flushFrames();

  // The browser hands the DOM the UNescaped attribute value back — that is
  // what escapeHtml on the template's value attribute produces when parsed.
  const input = h.input("wt-1");
  assert.equal(input.value, nasty, "the seeded value round-trips through escaping unchanged");
  h.handleWTRenameKeydown("wt-1", makeEvent({ key: "Enter" }));
  await h.flush();

  const patch = h.calls.api.filter((c) => c.opts.method === "PATCH");
  assert.equal(patch.length, 1);
  assert.equal(JSON.parse(patch[0].opts.body).name, nasty, "the wire carries the original characters — no entity encoding, no truncation (plan C/I)");
  assert.equal(h.state.worktrees[0].name, nasty, "and the row keeps it verbatim in state; the escaping that makes it inert lives in renderSidebar / escapeHtml (pinned by the Go anchors)");
});

// --- 7: the background render guards ----------------------------------------

test("issue #122 pitfall #1/#2/#4: a rename survives a full poll tick — no render(), no sidebar rebuild", async () => {
  const renaming = pollHarness({ renamingWT: "wt-1" });
  await renaming.refresh();
  await renaming.flush();

  assert.ok(renaming.calls.api.includes("/api/worktrees"), "the poll did run — the guards are being tested, not a stalled harness");
  assert.equal(renaming.calls.render.length, 0, "refresh() skips the full render() while renamingWT is set (pitfall #1)");
  assert.deepEqual(renaming.calls.renderSidebar, [],
    "neither the /api/main continuation nor fetchDiverge() rebuilds #wt-list mid-rename (pitfall #2/#4)");

  // The same poll with nothing being renamed must still repaint, or the
  // guards above would be vacuous.
  const idle = pollHarness();
  await idle.refresh();
  await idle.flush();
  assert.equal(idle.calls.render.length, 1, "an idle poll renders once");
  assert.equal(idle.calls.renderSidebar.length, 2, "/api/main and fetchDiverge each re-render the sidebar when free");

  // fetchDiverge on its OWN 60s timer (divergeTimer) — the guard lives in
  // the callee so that path is covered too (decision G).
  await renaming.fetchDiverge();
  assert.deepEqual(renaming.calls.renderSidebar, [], "the 60s diverge timer stays off the sidebar mid-rename");
  renaming.state.renamingWT = null;
  await renaming.fetchDiverge();
  assert.equal(renaming.calls.renderSidebar.length, 1, "and paints again once the rename is done");
});

// --- 8: the focus guard ---------------------------------------------------

test("issue #122: the terminal does not steal focus from a renaming sidebar row", () => {
  const state = makeState();
  state.activeInst = "inst-1";
  state.instances = [{ id: "inst-1", status: "running" }];
  let focusCount = 0;
  const session = { id: "inst-1", ttyState: "READY", term: { focus: () => { focusCount += 1; } } };

  const page = focusFactory({
    state,
    getActiveTerminalSession: () => session,
    isInstanceLiveStatus: (s) => s === "running",
  });

  page.focusTerminalIfPossible();
  assert.equal(focusCount, 1, "control: with nothing being renamed the terminal takes focus");

  state.renamingWT = "wt-1";
  page.focusTerminalIfPossible();
  assert.equal(focusCount, 1, "renamingWT keeps the focus off the terminal (acceptance #4)");

  state.renamingWT = null;
  state.renamingInst = "inst-1";
  page.focusTerminalIfPossible();
  assert.equal(focusCount, 1, "the pre-existing tab-rename guard still holds as well");

  state.renamingInst = null;
  page.focusTerminalIfPossible();
  assert.equal(focusCount, 2, "and focus returns once both renames are over");
});

// --- 9: mutual exclusion, both directions -----------------------------------

test("issue #122 decision D: the two rename state machines are mutually exclusive in both directions", () => {
  const h = renameHarness();

  // A live tab rename, then a double-click on a sidebar row.
  h.state.renamingInst = "inst-1";
  h.renderTabs();
  assert.ok(h.tabInput("inst-1"), "the tab input exists before the sidebar takes over");
  assert.equal(h.renameInputCount(), 1);

  const tabCallsBefore = h.calls.renderTabs.length;
  h.startWTRename("wt-1", makeEvent());
  h.flushFrames();
  assert.equal(h.state.renamingInst, null, "startWTRename clears the tab rename state");
  assert.equal(h.tabInput("inst-1"), null, "and renderTabs tears its input down");
  assert.equal(h.state.renamingWT, "wt-1");
  assert.equal(h.renameInputCount(), 1, "exactly one rename input in the page");
  assert.ok(h.calls.renderTabs.length > tabCallsBefore, "startWTRename re-rendered the tabs to drop the old input");
  assert.equal(h.calls.renderTabs[tabCallsBefore].renamingInst, null,
    "that render ran AFTER the state was cleared, so no tab input survives it");

  // ...and the other way round: a double-click on a tab while a row edits.
  const sidebarCallsBefore = h.calls.renderSidebar.length;
  h.startRename("inst-2", makeEvent());
  h.flushFrames();
  assert.equal(h.state.renamingWT, null, "startRename clears the sidebar rename state");
  assert.equal(h.input("wt-1"), null, "and renderSidebar tore the row's input down");
  assert.equal(h.state.renamingInst, "inst-2");
  assert.equal(h.renameInputCount(), 1, "still exactly one rename input in the page");
  assert.ok(h.calls.renderSidebar.length > sidebarCallsBefore, "startRename re-rendered the sidebar to drop the old input");
  assert.equal(h.calls.renderSidebar[sidebarCallsBefore].renamingWT, null,
    "that render ran AFTER the state was cleared, so no input survives it");
  assert.ok(h.tabInput("inst-2"), "the tab input is the one that remains");
});

// --- 10: the per-row diverge fetch (review B-F1) ---------------------------

test("issue #122 review B-F1: fetchDivergeSingle's late response stays off the sidebar mid-rename, but still merges its state", async () => {
  // selectWorktree fires this request on the FIRST click of the double-click,
  // so it is already in flight before the dblclick builds the input — the
  // blur-commits-first argument cannot cover it. The response must update
  // state.diverged and simply skip the repaint.
  const renaming = pollHarness({ renamingWT: "wt-1" });
  await renaming.fetchDivergeSingle("wt-1");

  assert.ok(renaming.calls.api.includes("/api/worktree/diverged?id=wt-1"), "the request did run — the guard is being tested, not a stalled harness");
  assert.deepEqual(renaming.calls.renderSidebar, [], "no #wt-list rebuild while renamingWT is set (that would take focus, selection, half-typed text and the IME composition)");
  assert.deepEqual(renaming.state.diverged, { "wt-1": { diverged: true, ahead: 5 } }, "the diverged badge is state, not the input — it still merges and rides the next repaint");

  // Control: the same fetch with nothing being renamed repaints, or the guard
  // above would be vacuous.
  const idle = pollHarness();
  await idle.fetchDivergeSingle("wt-1");
  assert.equal(idle.calls.renderSidebar.length, 1, "an idle per-row diverge fetch repaints as it always did");
  assert.deepEqual(idle.state.diverged, { "wt-1": { diverged: true, ahead: 5 } });
});

// --- 11: a second double-click inside the PATCH round trip (review B-F2) --

test("issue #122 review B-F2: a PATCH response landing while another row edits does not tear that row down", async () => {
  const h = renameHarness({
    worktrees: [
      { id: "wt-1", name: "first row", branch: "feature/a", path: "/tmp/wt-1" },
      { id: "wt-2", name: "second row", branch: "feature/b", path: "/tmp/wt-2" },
    ],
    gatePatch: true,
  });

  h.startWTRename("wt-1", makeEvent());
  h.flushFrames();
  h.input("wt-1").value = "renamed first";
  h.handleWTRenameKeydown("wt-1", makeEvent({ key: "Enter" }));

  // The round trip is still open; the user double-clicks the OTHER row.
  assert.equal(h.calls.api.filter((c) => c.opts.method === "PATCH").length, 1, "the PATCH is in flight");
  h.startWTRename("wt-2", makeEvent());
  h.flushFrames();
  const secondInput = h.input("wt-2");
  assert.ok(secondInput, "the second row is being edited");
  secondInput.value = "半输入";

  const rendersBeforeResponse = h.calls.renderSidebar.length;
  h.resolvePatch();
  await h.flush();

  assert.equal(h.calls.renderSidebar.length, rendersBeforeResponse, "the success continuation must NOT repaint while another row owns the editing state");
  assert.equal(h.input("wt-2"), secondInput, "the fresh input survives the landing response, with its text intact");
  assert.equal(secondInput.value, "半输入", "including the half-typed text");
  assert.equal(h.state.worktrees[0].name, "renamed first", "the rename itself still lands in state — only the repaint waits");
  assert.equal(h.calls.refresh.length, 1, "and the resync still runs; refresh() carries its own guard");

  // Once the second edit is over, the next repaint shows the new name.
  h.cancelWTRename();
  assert.equal(h.state.worktrees[0].name, "renamed first", "the committed name is what the sidebar will paint");
});

// --- 12 / 13: the blur re-entrancy race (issue #122 browser verification) --
//
// Removing the focused input inside an innerHTML write makes Chrome dispatch
// blur on that node while getElementById still resolves it, so onblur re-
// enters the commit handler with the editing state already cleared. Without
// the state guard that re-entry sends a SECOND identical PATCH, nests another
// innerHTML write inside the first (unbounded recursion, which is what made
// the real renderer hang), and commits text nobody asked for. The harness
// models the mid-removal window by firing the shipped handler from inside the
// render stub, and blurEvents records that it really happened.

test("issue #122 blur re-entrancy: the render that removes the input must not double-commit", async () => {
  // Enter commit: the commit's own renderSidebar() fires the blur re-entry.
  const h = renameHarness();
  h.startWTRename("wt-1", makeEvent());
  h.flushFrames();
  h.input("wt-1").value = "renamed once";
  h.handleWTRenameKeydown("wt-1", makeEvent({ key: "Enter" }));
  await h.flush();

  assert.deepEqual(h.blurEvents, ["wt-rename-input-wt-1"], "the model really fired the mid-removal blur — this case is not vacuous");
  const wtPatches = h.calls.api.filter((c) => c.opts.method === "PATCH" && c.path === "/api/worktrees");
  assert.equal(wtPatches.length, 1, "exactly ONE PATCH: the re-entered commit must bail on the cleared state");
  assert.deepEqual(JSON.parse(wtPatches[0].opts.body), { id: "wt-1", name: "renamed once" });

  // The same call driven by hand AFTER the render finished: by now the
  // render's delete loop has dropped the node from the map, so this sub-assert
  // exercises the SECOND line of defence, `if (!input) return;`. The state
  // guard is the one pinned above, by the blur the render fired from inside
  // itself while the node was still findable (blurEvents is the receipt).
  h.commitWTRename("wt-1", makeEvent({ type: "blur" }));
  await h.flush();
  assert.equal(h.calls.api.filter((c) => c.opts.method === "PATCH" && c.path === "/api/worktrees").length, 1,
    "a re-entered commit with state.renamingWT already cleared never reaches the wire");

  // Esc: the discarded half-typed text must NOT be committed by that blur.
  const esc = renameHarness();
  esc.startWTRename("wt-1", makeEvent());
  esc.flushFrames();
  esc.input("wt-1").value = "discarded garbage";
  esc.handleWTRenameKeydown("wt-1", makeEvent({ key: "Escape" }));
  await esc.flush();
  assert.deepEqual(esc.calls.api, [], "Escape discards the text, and the blur it induces must not resurrect it");
  esc.commitWTRename("wt-1", makeEvent({ type: "blur" }));
  await esc.flush();
  assert.deepEqual(esc.calls.api, [], "nor may the same call after Escape, once the node is gone (second defence)");
  assert.equal(esc.state.worktrees[0].name, "fix-the-flaky-thing", "the stored name stands");

  // Control: a LEGITIMATE blur (the row still owns the editing state) commits
  // exactly once, so the guard cannot be quietly disarming the real path.
  const ok = renameHarness();
  ok.startWTRename("wt-1", makeEvent());
  ok.flushFrames();
  ok.input("wt-1").value = "user clicked away";
  ok.commitWTRename("wt-1", makeEvent({ type: "blur" }));
  await ok.flush();
  assert.equal(ok.calls.api.filter((c) => c.opts.method === "PATCH").length, 1, "the genuine blur still commits");
  assert.equal(ok.state.worktrees[0].name, "user clicked away");
});

test("issue #122 blur re-entrancy: neither mutual-exclusion teardown commits the input it removes", async () => {
  // Sidebar takes over from a live tab rename: startWTRename -> renderTabs()
  // tears the tab input down -> Chrome re-enters commitRename.
  const h = renameHarness();
  h.state.renamingInst = "inst-1";
  h.renderTabs();
  assert.ok(h.tabInput("inst-1"), "the tab input exists before the sidebar takes over");
  h.tabInput("inst-1").value = "tab name nobody re-typed";

  h.startWTRename("wt-1", makeEvent());
  h.flushFrames();
  await h.flush();
  assert.deepEqual(h.blurEvents, ["rename-input-inst-1"], "the teardown really blurred the live tab input");
  assert.deepEqual(h.calls.api.filter((c) => c.path === "/api/instances"), [],
    "no spurious self-rename PATCH /api/instances from the mutual-exclusion teardown");
  assert.equal(h.state.instances[0].name, "terminal one", "the instance keeps its name");

  // The same call driven by hand, after the teardown finished: the node is
  // out of the map by now, so this pins `if (!input) return;`. The state guard
  // is pinned by the mid-removal blur above, via blurEvents.
  h.commitRename("inst-1", makeEvent({ type: "blur" }));
  await h.flush();
  assert.deepEqual(h.calls.api.filter((c) => c.path === "/api/instances"), [], "and the post-teardown call stays silent too");

  // ...and the other direction: startRename -> renderSidebar() tears the row
  // input down, which must not commit that row.
  const h2 = renameHarness();
  h2.startWTRename("wt-1", makeEvent());
  h2.flushFrames();
  h2.input("wt-1").value = "row name about to be abandoned";
  const patchesBefore = h2.calls.api.length;
  assert.equal(patchesBefore, 0, "nothing sent yet — the row is still being edited");

  h2.startRename("inst-1", makeEvent());
  h2.flushFrames();
  await h2.flush();
  assert.deepEqual(h2.blurEvents, ["wt-rename-input-wt-1"], "the teardown really blurred the live row input");
  assert.deepEqual(h2.calls.api.filter((c) => c.path === "/api/worktrees"), [],
    "no PATCH /api/worktrees from the tab's mutual-exclusion teardown");
  assert.equal(h2.state.worktrees[0].name, "fix-the-flaky-thing", "the worktree keeps its name");
  h2.commitWTRename("wt-1", makeEvent({ type: "blur" }));
  await h2.flush();
  assert.deepEqual(h2.calls.api.filter((c) => c.path === "/api/worktrees"), [], "and its post-teardown call stays silent too");

  // Control: the tab's own genuine blur still commits — the guard is
  // state-based, not a blanket mute.
  const ok = renameHarness();
  ok.state.renamingInst = "inst-1";
  ok.renderTabs();
  ok.tabInput("inst-1").value = "genuinely retyped";
  ok.commitRename("inst-1", makeEvent({ type: "blur" }));
  await ok.flush();
  const instPatches = ok.calls.api.filter((c) => c.path === "/api/instances" && c.opts.method === "PATCH");
  assert.equal(instPatches.length, 1, "a real tab rename still reaches the server");
  assert.deepEqual(JSON.parse(instPatches[0].opts.body), { id: "inst-1", name: "genuinely retyped" });
});

// --- 14 / 15: third-party teardown during an editing state (review A-F1) ---
//
// The commit handlers' own state guard cannot cover these: at the moment of
// teardown the row's state is STILL set, so a re-entered commit passes the
// guard and PATCHes the half-typed text as if the user had pressed Enter.
// The teardown point has to carry its own guard. Each sub-check is paired with
// a control that renames nobody, so a guard that muted every repaint could not
// pass either.

test("issue #122 review A-F1: a tab rename's success repaint must not destroy a row being renamed", async () => {
  const h = renameHarness({ gatePatch: true });
  h.state.renamingInst = "inst-1";
  h.renderTabs();
  h.tabInput("inst-1").value = "tab retyped";
  h.commitRename("inst-1", makeEvent({ key: "Enter" }));
  await h.flush();

  // The PATCH is in flight; this issue's mutual exclusion makes the crossing
  // reachable — the user now double-clicks a SIDEBAR row.
  assert.equal(h.state.renamingInst, null, "the tab commit cleared its own state, as designed");
  h.startWTRename("wt-1", makeEvent());
  h.flushFrames();
  h.input("wt-1").value = "half typed";

  const rendersBefore = h.calls.render.length;
  const blursBefore = h.blurEvents.length;
  h.resolvePatch();
  await h.flush();

  // Harm first, mechanism after: what the user would actually see is a row
  // renamed behind their back and their half-typed text gone.
  assert.ok(h.input("wt-1"), "the row input survives the tab's round trip");
  assert.equal(h.input("wt-1").value, "half typed", "with exactly the text the user typed");
  assert.deepEqual(h.calls.api.filter((c) => c.path === "/api/worktrees"), [],
    "the tab's round trip never renames a worktree behind the user");
  assert.equal(h.calls.render.length, rendersBefore,
    "render() must be skipped while ANY rename is open — it rebuilds both containers, so it would tear the row input down");
  assert.equal(h.blurEvents.length, blursBefore, "and that repaint must not blur the row input");
  assert.equal(h.state.instances[0].name, "tab retyped", "the instance splice itself stays unconditional");

  // Control: with nobody editing, the tab rename still repaints.
  const ok = renameHarness({ gatePatch: true });
  ok.state.renamingInst = "inst-1";
  ok.renderTabs();
  ok.tabInput("inst-1").value = "quiet retype";
  ok.commitRename("inst-1", makeEvent());
  await ok.flush();
  const before = ok.calls.render.length;
  ok.resolvePatch();
  await ok.flush();
  assert.equal(ok.calls.render.length, before + 1, "the guard is conditional, not a mute");
  assert.equal(ok.state.instances[0].name, "quiet retype");
});

test("issue #122 review A-F1: openTerminal / openFinder repaint after their await, guarded by the rename state", async () => {
  for (const [name, drive] of [
    ["openTerminal", (h) => h.openTerminal("wt-1", makeEvent())],
    ["openFinder", (h) => h.openFinder("wt-1", makeEvent())],
  ]) {
    // The await parks (up to 10s in the browser); inside it the user
    // double-clicks the row into its editing state.
    const h = renameHarness({ gateOpen: true });
    const inflight = drive(h);
    await h.flush();
    assert.equal(h.calls.api.filter((c) => c.opts.method === "POST").length, 1, `${name} really reached the server`);

    h.startWTRename("wt-1", makeEvent());
    h.flushFrames();
    h.input("wt-1").value = "composing 中文";

    const rendersBefore = h.calls.renderSidebar.length;
    const blursBefore = h.blurEvents.length;
    h.resolveOpen();
    await inflight;
    await h.flush();

    assert.ok(h.input("wt-1"), `${name} leaves the row input in the page`);
    assert.equal(h.input("wt-1").value, "composing 中文", "mid-composition text is untouched");
    assert.equal(h.calls.renderSidebar.length, rendersBefore,
      `${name}'s finally must not rebuild #wt-list while a row is being renamed`);
    assert.equal(h.blurEvents.length, blursBefore, `${name}'s finally must not blur the row input`);
    assert.deepEqual(h.calls.alert, [], `${name} succeeded, so nothing alerted`);

    // Control: nobody editing -> the spinner's repaint does happen.
    const ok = renameHarness({ gateOpen: true });
    const okInflight = drive(ok);
    await ok.flush();
    const before = ok.calls.renderSidebar.length;
    ok.resolveOpen();
    await okInflight;
    await ok.flush();
    assert.equal(ok.calls.renderSidebar.length, before + 1,
      `${name} still repaints its spinner away when nobody is renaming`);
  }
});
