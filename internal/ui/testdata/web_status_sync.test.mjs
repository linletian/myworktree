// Behavioral tests for the cross-browser status self-heal of the embedded
// web-UI renderers (issue #120): the SAME instance open in two browsers —
// browser A clicks Stop, browser B only ever sees the polled status change.
// Its badge repaints stopped on the next ~2s refresh tick, but activate()
// never re-runs (selectInstance early-returns on the same id), so without
// the poll-driven syncStatus the live iframe in browser B would stay alive
// and interactive forever.
//
// The logic under test lives in the web root (kinds/dsh_web.js,
// kinds/opencode_web.js and index.html's inline script), so this file lives
// in testdata/ instead of static/ on purpose: static/* is embedded and
// served to every browser, and test code does not belong in the web root.
//
// The renderers are loaded whole into a new Function(...) sandbox against a
// stubbed document/window — the assertions run against the shipped code
// rather than a copy of it (same arrangement as terminal_status.test.mjs's
// ptyHarness). Run: node --test testdata/web_status_sync.test.mjs (also
// wired into terminal_status_test.go for `go test`).
import test from "node:test";
import assert from "node:assert/strict";
import { readFileSync } from "node:fs";
import { fileURLToPath } from "node:url";
import { dirname, join } from "node:path";

const here = dirname(fileURLToPath(import.meta.url));
const staticDir = join(here, "..", "static");
const indexSource = readFileSync(join(staticDir, "index.html"), "utf8");
const dshSource = readFileSync(join(staticDir, "kinds", "dsh_web.js"), "utf8");
const ocSource = readFileSync(join(staticDir, "kinds", "opencode_web.js"), "utf8");

// Drain the microtask queue (the renderers' polls are async functions that
// resume right after their awaited fetch). Node's own setTimeout here — the
// fake timers below never run on their own.
const flush = () => new Promise((resolve) => setTimeout(resolve, 0));

// --- source slicing helpers (same style as terminal_status.test.mjs) -----

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

// --- minimal DOM stub -------------------------------------------------------

// Flat fake DOM: appendChild/insertBefore/remove track connectivity the
// way the renderers consume it (isConnected decides _showOnly, _prune and
// destroyFrame), innerHTML parses the flat <div class="..."> runs the
// loading overlays are built from, querySelector matches #id and .class
// among direct children — exactly the queries the shipped code issues.
function makeEl(tag) {
  const el = {
    tagName: tag,
    id: "",
    className: "",
    hidden: false,
    isConnected: false,
    parentNode: null,
    children: [],
    dataset: {},
    style: {},
    src: "",
    textContent: "",
    _classes: new Set(),
    _html: "",
  };
  el.classList = {
    add: (c) => el._classes.add(c),
    remove: (c) => el._classes.delete(c),
    contains: (c) => el._classes.has(c),
  };
  el.setAttribute = () => {};
  el.appendChild = (child) => {
    el.children.push(child);
    child.parentNode = el;
    child.isConnected = el.isConnected;
    return child;
  };
  el.insertBefore = (child, ref) => {
    const at = el.children.indexOf(ref);
    if (at < 0) el.children.push(child);
    else el.children.splice(at, 0, child);
    child.isConnected = el.isConnected;
    child.parentNode = el;
    return child;
  };
  el.removeChild = (child) => {
    const at = el.children.indexOf(child);
    if (at >= 0) el.children.splice(at, 1);
    child.parentNode = null;
    child.isConnected = false;
    return child;
  };
  el.remove = () => {
    if (el.parentNode) el.parentNode.removeChild(el);
    el.isConnected = false;
  };
  el.querySelector = (sel) => {
    for (const c of el.children) {
      if (sel.startsWith("#")) {
        if (c.id === sel.slice(1)) return c;
      } else if ((" " + c.className + " ").includes(" " + sel.slice(1) + " ")) {
        return c;
      }
    }
    return null;
  };
  Object.defineProperty(el, "innerHTML", {
    get: () => el._html,
    set: (v) => {
      el._html = v;
      el.children.length = 0;
      const re = /<(\w+)([^>]*)>([^<]*)<\/\1>/g;
      let m;
      while ((m = re.exec(v)) !== null) {
        const child = makeEl(m[1]);
        const cls = /class="([^"]*)"/.exec(m[2]);
        if (cls) child.className = cls[1];
        child.textContent = m[3];
        child.parentNode = el;
        child.isConnected = el.isConnected;
        el.children.push(child);
      }
    },
  });
  return el;
}

// --- renderer harness ---------------------------------------------------------

const KIND_CONFIG = {
  "dsh-web": {
    source: dshSource,
    panel: "dsh-panel",
    frames: "dsh-frames",
    warning: "dsh-scope-warning",
    loading: "dsh-loading",
    loadingTitleClass: ".dsh-loading-title",
    loadingInfoClass: ".dsh-loading-info",
    stoppedTitle: "dsh instance stopped",
    scopePath: "/api/instances/dsh/scope",
    infoPath: "/api/instances/dsh?id=",
    // The backend serves missing_dsh ONLY while the instance is failed
    // (handleInstanceDshInfo); the harness mirrors that gate so the
    // one-shot sync-path check sees exactly what production sees.
    missingDshPayload: {
      missing_dsh: { npm_available: true, suggested_pin: "dsh" },
    },
    infoPayload: {
      iframe_src: "http://dsh.test/inst1/",
      version: "0.2.0",
      version_supported: true,
      remote_capable: true,
      overlay_verified: true,
    },
  },
  "opencode-web": {
    source: ocSource,
    panel: "opencode-panel",
    frames: "opencode-frames",
    warning: "opencode-scope-warning",
    loading: "opencode-loading",
    loadingTitleClass: ".opencode-loading-title",
    loadingInfoClass: ".opencode-loading-info",
    stoppedTitle: "opencode instance stopped",
    scopePath: "/api/instances/opencode/scope",
    infoPath: "/api/instances/opencode?id=",
    infoPayload: {
      port: 8080,
      iframe_src: "/mw/inst1/",
      version: "1.18.0",
      version_supported: true,
    },
  },
};

// Loads the REAL renderer source against a stubbed page: registry-backed
// document, ready-resolving fetch, recording fake setTimeout (only the
// scope poll's 1500ms rearm is ever scheduled once the info poll resolves),
// and the window bucket helpers index.html publishes. teardownStopped is
// wrapped to COUNT invocations while still running the shipped body, so
// the gate under test (in syncStatus) is judged on exactly-once effects.
function webHarness(kind) {
  const cfg = KIND_CONFIG[kind];
  const registry = {};

  const panel = makeEl("div");
  panel.id = cfg.panel;
  panel.isConnected = true;
  const framesHost = makeEl("div");
  framesHost.id = cfg.frames;
  framesHost.isConnected = true;
  framesHost.parentNode = panel;
  panel.children.push(framesHost);
  const warning = makeEl("div");
  warning.id = cfg.warning;
  warning.hidden = true;
  warning.isConnected = true;
  const termContainer = makeEl("div");
  termContainer.id = "terminal-container";
  termContainer.style = {};
  registry[cfg.panel] = panel;
  registry[cfg.frames] = framesHost;
  registry[cfg.warning] = warning;
  registry["terminal-container"] = termContainer;

  const document = {
    getElementById: (id) => registry[id] || null,
    createElement: (tag) => makeEl(tag),
  };

  const fetches = [];
  const fetch = (url) => {
    fetches.push(url);
    if (url.startsWith(cfg.scopePath)) {
      return Promise.resolve({
        ok: true,
        json: () => Promise.resolve({ scope: "in-scope", directory: "" }),
      });
    }
    // Info endpoint: a FAILED dsh instance reports missing_dsh, exactly
    // like the backend does (failed-only), so the one-shot sync-path
    // check in syncStatus surfaces the dialog just like production.
    if (cfg.missingDshPayload && url.startsWith(cfg.infoPath)) {
      const m = /id=([^&]+)/.exec(url);
      const target = m && window.state.instances.find((i) => i.id === decodeURIComponent(m[1]));
      if (target && target.status === "failed") {
        return Promise.resolve({ ok: true, json: () => Promise.resolve(cfg.missingDshPayload) });
      }
    }
    return Promise.resolve({ ok: true, json: () => Promise.resolve(cfg.infoPayload) });
  };

  const timers = [];
  let nextTimerId = 1;
  const setTimeout = (fn, ms) => {
    const t = { id: nextTimerId++, fn, ms };
    timers.push(t);
    return t.id;
  };
  const fireTimers = (ms) => {
    for (const t of timers.filter((x) => x.ms === ms)) {
      const at = timers.indexOf(t);
      if (at >= 0) timers.splice(at, 1); // one-shot semantics
      t.fn();
    }
  };

  const messageListeners = [];
  const window = {
    state: { instances: [], activeInst: null },
    registerRenderer(k, r) {
      this.KindRenderers = this.KindRenderers || {};
      this.KindRenderers[k] = r;
    },
    isRemoteAccess: () => false,
    isInstanceLiveStatus: (s) => s === "running" || s === "unhealthy",
    isInstancePendingStatus: (s) => s === "starting" || s === "stopping",
    isInstanceTerminalStatus: (s) =>
      !(s === "running" || s === "unhealthy" || s === "starting" || s === "stopping"),
    addEventListener: (name, fn) => messageListeners.push({ name, fn }),
    removeEventListener: (name, fn) => {
      const at = messageListeners.findIndex((l) => l.name === name && l.fn === fn);
      if (at >= 0) messageListeners.splice(at, 1);
    },
    location: { origin: "http://harness.test", search: "" },
  };

  const load = new Function("window", "document", "fetch", "setTimeout", "api", cfg.source);
  load(window, document, fetch, setTimeout, () => Promise.resolve());
  const renderer = window.KindRenderers[kind];
  assert.ok(renderer, `${kind}: renderer did not register itself`);

  const teardowns = [];
  // The full session objects the teardowns received, so a case can assert
  // what the sync path forwards (issue #120 review: the instance object must
  // travel with the session, not just the id).
  const teardownSessions = [];
  const realTeardown = renderer.teardownStopped.bind(renderer);
  assert.equal(typeof realTeardown, "function", `${kind}: teardownStopped must exist on the shipped renderer`);
  renderer.teardownStopped = (session) => {
    teardowns.push(session.id);
    teardownSessions.push(session);
    return realTeardown(session);
  };

  // Same counting wrap for the missing-dsh dialog (dsh-web only): the
  // sandbox registry has no modal-dsh-missing, so the observable is the
  // guard-flag _showMissingDialog sets before its modal lookup bails.
  const dialogOpens = [];
  if (cfg.missingDshPayload) {
    const realShow = renderer._showMissingDialog.bind(renderer);
    renderer._showMissingDialog = (id, missing) => {
      dialogOpens.push(id);
      return realShow(id, missing);
    };
  }

  const inst = () => window.state.instances.find((i) => i.id === "inst1");
  const activate = async (status, id = "inst1") => {
    const existing = window.state.instances.find((i) => i.id === id);
    if (existing) existing.status = status;
    else window.state.instances.push({ id, kind, status });
    window.state.activeInst = id;
    renderer.activate({ id, instance: existing || window.state.instances[window.state.instances.length - 1] }, null);
    await flush();
  };

  // The loading overlay's info block, as the user reads it.
  const loadingInfoText = () => {
    const el = panel.querySelector("#" + cfg.loading);
    const info = el && el.querySelector(cfg.loadingInfoClass);
    return info ? info.textContent : "";
  };

  return {
    renderer,
    window,
    fetches,
    timers,
    teardowns,
    teardownSessions,
    dialogOpens,
    messageListeners,
    panel,
    warning,
    framesHost,
    activate,
    inst,
    fireTimers,
    flush,
    frameFor: (id = "inst1") => renderer._frames.get(id) || null,
    scopeFetches: () => fetches.filter((u) => u.startsWith(cfg.scopePath)),
    infoFetches: () => fetches.filter((u) => u.startsWith(cfg.infoPath)),
    loading: () => panel.querySelector("#" + cfg.loading),
    loadingTitle: () => {
      const el = panel.querySelector("#" + cfg.loading);
      const title = el && el.querySelector(cfg.loadingTitleClass);
      return title ? title.textContent : "";
    },
    // The placeholder's info lines as they reach the DOM: _showLoading joins
    // them with '\n' into .dsh-loading-info / .opencode-loading-info, so
    // splitting there is what the user actually reads (issue #120 review: a
    // terminal placeholder must carry the failure reason).
    loadingInfo: loadingInfoText,
    loadingLines: () => (loadingInfoText() === "" ? [] : loadingInfoText().split("\n")),
  };
}

// --- the issue #120 scenarios, per web kind ---------------------------------

for (const kind of ["dsh-web", "opencode-web"]) {
  const cfg = KIND_CONFIG[kind];

  test(`${kind}: a cross-browser stop tears the iframe down exactly once on the sync tick`, async () => {
    const h = webHarness(kind);
    // A second cached frame of the SAME kind stands in for the issue #96
    // keep-alive frames: teardownStopped must hide them all, not just the
    // stopped one (activate's stopped branch never ran for them either).
    await h.activate("running", "inst2");
    const otherFrame = h.frameFor("inst2");
    await h.activate("running");
    const frame = h.frameFor();
    assert.ok(frame.isConnected, "live instance keeps its connected iframe");
    assert.equal(frame.hidden, false, "the ready frame is visible");

    // Browser B never navigates: only the polled status changes.
    h.inst().status = "stopped";
    h.renderer.syncStatus(h.inst());

    assert.deepEqual(h.teardowns, ["inst1"], "entry into the terminal bucket tears down exactly once");
    assert.equal(frame.isConnected, false, "the stopped instance's iframe is removed from the DOM");
    assert.equal(h.renderer._frames.has("inst1"), false, "the cached frame is released");
    assert.equal(otherFrame.hidden, true, "_showOnly(null): every other cached frame is hidden");
    assert.equal(h.loadingTitle(), cfg.stoppedTitle, "the stopped placeholder is shown");
    assert.equal(h.loading().hidden, false, "the placeholder covers the panel");
    assert.equal(h.warning.hidden, true, "no warning-bar residue");
    assert.equal(h.warning.textContent, "");
    assert.equal(h.renderer._abort.aborted, true, "the ready-wait poll is aborted");
    assert.equal(h.renderer._scopeAbort, null, "the scope poll is stopped");
    assert.deepEqual(h.messageListeners, [], "no message listener leaks (opencode)");

    // No further requests for the dead instance, however many ticks fire.
    const fetchCount = h.fetches.length;
    h.fireTimers(1500);
    h.fireTimers(500);
    await h.flush();
    assert.equal(h.fetches.length, fetchCount, "no new requests after teardown");
  });

  test(`${kind}: staying stopped does not re-teardown or repaint on later ticks`, async () => {
    const h = webHarness(kind);
    await h.activate("running");
    h.inst().status = "stopped";
    h.renderer.syncStatus(h.inst());
    assert.deepEqual(h.teardowns, ["inst1"], "the first tick tears down");

    const fetchCount = h.fetches.length;
    const loadingShownAt = h.loading();
    h.renderer.syncStatus(h.inst());
    h.renderer.syncStatus(h.inst());
    assert.deepEqual(h.teardowns, ["inst1"], "the gate holds: no second teardown while already terminal");
    assert.equal(h.loading(), loadingShownAt, "no placeholder rebuild");
    assert.equal(h.loadingTitle(), cfg.stoppedTitle, "the placeholder is not repainted");
    h.fireTimers(1500);
    await h.flush();
    assert.equal(h.fetches.length, fetchCount, "already-stopped instances are never re-requested");
    assert.deepEqual(h.timers.filter((t) => t.ms === 1500), [], "the scope poll is not rearmed");
  });

  test(`${kind}: pending and live statuses never trigger the teardown`, async () => {
    const h = webHarness(kind);
    await h.activate("starting");
    // The full pending/live matrix, in transition order: none may tear the
    // panel down (issue #80 — starting is routinely observed here).
    for (const status of ["starting", "stopping", "running", "unhealthy", "running"]) {
      h.inst().status = status;
      h.renderer.syncStatus(h.inst());
    }
    assert.deepEqual(h.teardowns, [], "pending/live statuses must never tear down");
    const frame = h.frameFor();
    assert.ok(frame.isConnected, "the live iframe survives every pending tick");
    assert.equal(frame.hidden, false, "the loaded page stays interactive");
    assert.notEqual(h.loadingTitle(), cfg.stoppedTitle, "no stopped placeholder painted");
    assert.equal(h.loading().hidden, true, "the loading overlay stays hidden");
  });

  test(`${kind}: every terminal-bucket member triggers once, later terminal ticks stay silent`, async () => {
    // stopped, failed and exited all land in the terminal bucket; the
    // gate must fire on the FIRST entry regardless of which member the
    // instance lands in, and never again while it stays terminal.
    const h = webHarness(kind);
    await h.activate("running");
    h.inst().status = "exited";
    h.renderer.syncStatus(h.inst());
    assert.deepEqual(h.teardowns, ["inst1"], "running -> exited tears down");
    h.inst().status = "failed";
    h.renderer.syncStatus(h.inst());
    h.inst().status = "stopped";
    h.renderer.syncStatus(h.inst());
    assert.deepEqual(h.teardowns, ["inst1"], "terminal -> terminal never re-fires");
  });

  test(`${kind}: activate() re-baselines the gate so a stale prev cannot double-fire`, async () => {
    const h = webHarness(kind);
    await h.activate("running");
    h.renderer.syncStatus(h.inst()); // gate: running
    h.inst().status = "stopped";
    // The user switches away and back onto the stopped tab: activate()'s
    // own stopped branch tears down once and must re-baseline the gate to
    // the CURRENT status — without that, the prev "running" left here would
    // make the next syncStatus tick tear down (and repaint) a second time.
    await h.activate("stopped");
    assert.deepEqual(h.teardowns, ["inst1"], "activate's stopped branch tears down once");
    h.renderer.syncStatus(h.inst());
    h.renderer.syncStatus(h.inst());
    assert.deepEqual(h.teardowns, ["inst1"], "the re-baselined gate never re-fires");
  });

  test(`${kind}: the scope poll clears the warning bar and stops rescheduling once stopped`, async () => {
    const h = webHarness(kind);
    await h.activate("running");
    assert.ok(h.scopeFetches().length >= 1, "the live instance's scope poll runs");
    assert.ok(h.timers.some((t) => t.ms === 1500), "the scope poll is armed for the next tick");
    const before = h.scopeFetches().length;

    // Cross-browser stop; the bar still shows a stale warning from before.
    h.inst().status = "stopped";
    h.warning.hidden = false;
    h.warning.textContent = "stale warning";
    h.fireTimers(1500);
    await h.flush();

    assert.equal(h.scopeFetches().length, before, "no scope fetch for a stopped instance");
    assert.equal(h.warning.hidden, true, "the warning bar is cleared, no residue");
    assert.equal(h.warning.textContent, "");
    assert.deepEqual(h.timers.filter((t) => t.ms === 1500), [], "the poll does not reschedule");

    // Even a vanished instance keeps the poll dead.
    h.window.state.instances = [];
    h.fireTimers(1500);
    await h.flush();
    assert.equal(h.scopeFetches().length, before, "a vanished instance is never polled either");
  });

  test(`${kind}: activate() after a teardown rebuilds a fresh frame (restart path)`, async () => {
    const h = webHarness(kind);
    await h.activate("running");
    const firstFrame = h.frameFor();
    h.inst().status = "stopped";
    h.renderer.syncStatus(h.inst());
    assert.equal(firstFrame.isConnected, false, "torn down");

    // Restart: a running activation must build a fresh iframe and show it.
    await h.activate("running");
    const secondFrame = h.frameFor();
    assert.ok(secondFrame, "a fresh frame was built");
    assert.notEqual(secondFrame, firstFrame, "the dead frame is not resurrected");
    assert.equal(secondFrame.isConnected, true, "the new frame is in the DOM");
    assert.equal(secondFrame.hidden, false, "the poll navigated and revealed it");
    assert.ok(secondFrame.src, "the new frame carries its src");
    assert.equal(h.loading().hidden, true, "the placeholder is cleared on the live page");
    assert.deepEqual(h.teardowns, ["inst1"], "the restart itself never counts as a teardown");

    // And the freshly re-baselined gate can still catch the NEXT stop.
    h.inst().status = "stopped";
    h.renderer.syncStatus(h.inst());
    assert.deepEqual(h.teardowns, ["inst1", "inst1"], "the gate is re-armed and catches the next stop");
    assert.equal(secondFrame.isConnected, false, "the second stop tears the rebuilt frame down");
  });

  test(`${kind}: syncStatus without the published window helper still classifies via the fallback`, async () => {
    const h = webHarness(kind);
    await h.activate("running");
    delete h.window.isInstanceTerminalStatus;
    delete h.window.isInstanceLiveStatus;
    delete h.window.isInstancePendingStatus;
    assert.doesNotThrow(() => h.renderer.syncStatus(h.inst()), "pending/live fallback classification must not throw");
    assert.deepEqual(h.teardowns, [], "live status: no teardown without the helper");
    h.inst().status = "stopped";
    assert.doesNotThrow(() => h.renderer.syncStatus(h.inst()), "terminal fallback classification must not throw");
    assert.deepEqual(h.teardowns, ["inst1"], "the fallback still catches the stop");
  });

  test(`${kind}: the stopped placeholder carries the terminal reason, plain stops stay bare`, async () => {
    // The watched running→failed transition used to paint only
    // `instance: <id>`, hiding the status/last_error the badge just flipped
    // to (issue #120 review A §2b). The reason line follows the timeout
    // overlay's format: `status:    <status>` plus ` — <last_error>` only
    // when there is one, so an empty reason never leaves a dangling dash.
    const h = webHarness(kind);
    await h.activate("running");
    h.inst().last_error = "port in use";
    h.inst().status = "failed";
    h.renderer.syncStatus(h.inst());
    await h.flush();
    assert.deepEqual(h.teardowns, ["inst1"], "the failed entry tears down");
    assert.deepEqual(
      h.loadingLines(),
      ["instance:  inst1", "status:    failed — port in use"],
      "the placeholder shows the status and its reason");

    // No last_error: the line is the bare status, no trailing separator.
    const bare = webHarness(kind);
    await bare.activate("running");
    bare.inst().status = "exited";
    bare.renderer.syncStatus(bare.inst());
    await bare.flush();
    assert.deepEqual(
      bare.loadingLines(),
      ["instance:  inst1", "status:    exited"],
      "a reason-less terminal entry gets the bare status line");

    // A plain stopped activation (activate's own branch) is byte-identical
    // to what it painted before: one line, no reason.
    const stopped = webHarness(kind);
    await stopped.activate("stopped");
    assert.deepEqual(stopped.teardowns, ["inst1"], "activate's stopped branch tears down");
    assert.deepEqual(stopped.loadingLines(), ["instance:  inst1"], "the plain stopped placeholder is unchanged");
  });

  test(`${kind}: syncStatus forwards the instance object to the teardown`, async () => {
    // { id, instance } — the id alone cannot produce the reason line, and
    // the forward-compat shape keeps the sync path and activate()'s path
    // handing teardownStopped the same session contract (issue #120 review).
    const h = webHarness(kind);
    await h.activate("running");
    h.inst().last_error = "backend gone";
    h.inst().status = "failed";
    h.renderer.syncStatus(h.inst());
    assert.equal(h.teardownSessions.length, 1, "exactly one teardown");
    const session = h.teardownSessions[0];
    assert.equal(session.id, "inst1", "the teardown still carries the id");
    assert.ok(session.instance, "the teardown carries an instance object, not just an id");
    assert.equal(session.instance, h.inst(), "it is the very instance object from window.state");
    assert.equal(session.instance.status, "failed", "at its current (failed) status");
    assert.deepEqual(
      h.loadingLines(),
      ["instance:  inst1", "status:    failed — backend gone"],
      "and the renderer actually consumed it for the reason line");
  });
}

// --- dsh-web: the missing-dsh dialog survives the issue #120 teardown ----
//
// The ready-wait poll's 500ms tick is the dialog's only other driver, and
// teardownStopped aborts that poll — so without the one-shot failed-only
// recovery check in syncStatus, a user watching a starting dsh-web instance
// that fails with dsh missing could lose the recovery dialog (the ~2s sync
// tick can win the race against the 500ms poll). In the harness the ready
// poll resolves on its first tick (the stubbed info payload always carries
// iframe_src, like every other case here), so every info fetch counted
// after the failed flip provably comes from the sync path.

test("dsh-web: starting→failed on the active tab still surfaces the missing-dsh dialog", async () => {
  const h = webHarness("dsh-web");
  // The user is watching the starting tab; its ready poll is the dialog's
  // designed driver.
  await h.activate("starting");
  const frame = h.frameFor();
  // The instance FAILS (dsh missing) while this tab stays active.
  h.inst().status = "failed";
  const infoBefore = h.infoFetches().length;
  h.renderer.syncStatus(h.inst());
  await h.flush(); // the one-shot recovery fetch is fire-and-forget

  assert.deepEqual(h.teardowns, ["inst1"], "the failed entry tears the panel down exactly once");
  assert.equal(frame.isConnected, false, "the dead iframe is gone");
  assert.equal(h.loadingTitle(), "dsh instance stopped", "the stopped placeholder stands on its own");
  assert.equal(h.renderer._abort.aborted, true, "the teardown aborts the ready-wait poll");
  assert.equal(h.infoFetches().length, infoBefore + 1, "the entering gate fires exactly ONE recovery fetch of the ready poll's own info endpoint");
  assert.deepEqual(h.dialogOpens, ["inst1"], "_showMissingDialog ran on the sync path");
  assert.equal(h.renderer._dialogShownFor, "inst1", "the missing-dsh dialog is open");

  // Cancelled earlier? The SAME failed entry must not nag: _showMissingDialog's
  // guards keep working unchanged (dismissed → the dialog stays closed while
  // the one-shot fetch still runs and the placeholder stands).
  const h2 = webHarness("dsh-web");
  await h2.activate("starting");
  h2.renderer._dismissedMissingFor = "inst1"; // user cancelled the dialog
  h2.inst().status = "failed";
  const infoBefore2 = h2.infoFetches().length;
  h2.renderer.syncStatus(h2.inst());
  await h2.flush();
  assert.deepEqual(h2.teardowns, ["inst1"], "the failed entry still tears down");
  assert.equal(h2.infoFetches().length, infoBefore2 + 1, "the recovery check still runs once");
  assert.deepEqual(h2.dialogOpens, ["inst1"], "the check reaches _showMissingDialog");
  assert.notEqual(h2.renderer._dialogShownFor, "inst1", "the dismissed guard is respected: the dialog does not reopen");
});

test("dsh-web: non-failed terminal entries fire no recovery fetch; opencode never fires one", async () => {
  // The one-shot check is failed-only (missing_dsh is failed-only per the
  // backend): stopped/exited entries just tear down.
  for (const status of ["stopped", "exited"]) {
    const h = webHarness("dsh-web");
    await h.activate("running");
    h.inst().status = status;
    const infoBefore = h.infoFetches().length;
    h.renderer.syncStatus(h.inst());
    await h.flush();
    assert.deepEqual(h.teardowns, ["inst1"], `running→${status} tears down`);
    assert.equal(h.infoFetches().length, infoBefore, `a ${status} entry performs no extra info fetch from the sync path (the check is failed-only)`);
    assert.deepEqual(h.dialogOpens, [], `no dialog for ${status}`);
  }
  // opencode-web has no missing-dsh dialog at all: its failed entry tears
  // down but never fetches the info endpoint.
  const oc = webHarness("opencode-web");
  await oc.activate("running");
  oc.inst().status = "failed";
  const infoBefore = oc.infoFetches().length;
  oc.renderer.syncStatus(oc.inst());
  await oc.flush();
  assert.deepEqual(oc.teardowns, ["inst1"], "failed is terminal: opencode tears down");
  assert.equal(oc.infoFetches().length, infoBefore, "the one-shot info fetch is dsh-only; opencode fetches nothing on a failed entry");
});

// --- the one-shot response-time guard (issue #120 review) ------------------
//
// The recovery fetch is fire-and-forget, so its response can land long AFTER
// the user switched to another tab, the instance was deleted, or a restart
// moved its status on. _dialogShownFor / _dismissedMissingFor keep DUPLICATE
// dialogs quiet but say nothing about an id the user is no longer looking at,
// so the response handler re-validates the live state before opening. Each
// case below changes the page state AFTER syncStatus issued the fetch and
// BEFORE the microtask drain, which is exactly the window a slow backend
// creates in a browser.

test("dsh-web: a stale recovery response is dropped when the user left the tab", async () => {
  const h = webHarness("dsh-web");
  await h.activate("starting");
  h.inst().status = "failed";
  const infoBefore = h.infoFetches().length;
  h.renderer.syncStatus(h.inst());
  h.window.state.activeInst = "other"; // switched away, response still pending
  await h.flush();
  assert.equal(h.infoFetches().length, infoBefore + 1, "the recovery fetch still fired exactly once");
  assert.deepEqual(h.dialogOpens, [], "a dialog for a tab the user left must not open");
  assert.notEqual(h.renderer._dialogShownFor, "inst1", "and no dialog state is left behind");
});

test("dsh-web: a stale recovery response is dropped when the instance was deleted", async () => {
  const h = webHarness("dsh-web");
  await h.activate("starting");
  h.inst().status = "failed";
  const infoBefore = h.infoFetches().length;
  h.renderer.syncStatus(h.inst());
  h.window.state.instances = []; // deleted while the response was pending
  await h.flush();
  assert.equal(h.infoFetches().length, infoBefore + 1, "the fetch still went out exactly once");
  assert.deepEqual(h.dialogOpens, [], "a deleted instance never gets its dialog retroactively");
});

test("dsh-web: a stale recovery response is dropped when the status moved on", async () => {
  // missing_dsh is failed-only per the backend: a response that arrives once
  // the instance reads stopped/exited describes a state the page no longer
  // shows, so it must not open (the dialog would contradict the badge).
  for (const [label, mutate] of [
    ["stopped", (i) => { i.status = "stopped"; }],
    ["exited", (i) => { i.status = "exited"; }],
    ["restarted into starting", (i) => { i.status = "starting"; }],
  ]) {
    const h = webHarness("dsh-web");
    await h.activate("starting");
    h.inst().status = "failed";
    const infoBefore = h.infoFetches().length;
    h.renderer.syncStatus(h.inst());
    mutate(h.inst()); // the polled status moved on before the response landed
    await h.flush();
    assert.equal(h.infoFetches().length, infoBefore + 1, `${label}: the fetch still fired exactly once`);
    assert.deepEqual(h.dialogOpens, [], `${label}: a non-failed live status drops the stale response`);
  }
});

test("dsh-web: the still-active failed case still reaches the dialog (guard control)", async () => {
  // The guard must only reject STALE responses: the plain case — the user is
  // still on the failed instance — still opens, once.
  const h = webHarness("dsh-web");
  await h.activate("starting");
  h.inst().status = "failed";
  const infoBefore = h.infoFetches().length;
  h.renderer.syncStatus(h.inst());
  await h.flush();
  assert.equal(h.infoFetches().length, infoBefore + 1, "exactly one recovery fetch");
  assert.deepEqual(h.dialogOpens, ["inst1"], "still listed, still failed, still active: the dialog opens");
});

// --- dsh scope-tick self-stop hygiene (issue #120 review) ------------------

test("dsh-web: the scope tick's self-stop resets the poll's warning state too", async () => {
  // The self-stop branch already cleared the bar and stopped rescheduling; it
  // must also run the symmetric cleanup, so the dead poll leaves no stale
  // warning-state flags behind — the same final state opencode's
  // _stopScopeMonitoring() lands in (and idempotent with the teardown).
  const h = webHarness("dsh-web");
  await h.activate("running");
  assert.ok(h.renderer._scopeAbort, "the live scope poll owns an abort handle");
  h.renderer._scope = "out-of-scope";
  h.renderer._scopeDir = "/tmp/elsewhere";
  h.renderer._versionUnsupported = true;
  h.renderer._overlayIneffective = true;
  h.renderer._foreignSessions = [{ session_id: "foreign" }];

  h.inst().status = "stopped";
  h.fireTimers(1500);
  await h.flush();
  assert.equal(h.renderer._scopeAbort, null, "the dead poll's abort handle is released");
  assert.equal(h.renderer._scope, "in-scope", "the scope flag is reset");
  assert.equal(h.renderer._scopeDir, "", "the out-of-scope directory is dropped");
  assert.equal(h.renderer._versionUnsupported, false, "the advisory flags are reset");
  assert.equal(h.renderer._overlayIneffective, false, "the advisory flags are reset");
  assert.deepEqual(h.renderer._foreignSessions, [], "the foreign-session list is cleared");
  assert.equal(h.warning.hidden, true, "the warning bar stays cleared");
  assert.deepEqual(h.timers.filter((t) => t.ms === 1500), [], "and it never rearms");

  // Same for a vanished instance (deleted without a terminal status seen).
  const gone = webHarness("dsh-web");
  await gone.activate("running");
  gone.window.state.instances = [];
  gone.fireTimers(1500);
  await gone.flush();
  assert.equal(gone.renderer._scopeAbort, null, "a vanished instance's poll is released too");
});

// --- index.html dispatch: syncActiveWebStatus ------------------------------

const webStatusFactory = new Function("deps", `
  const { state, window, console } = deps;
  ${sliceStatement(inlineScript(indexSource), "const WEB_UI_KINDS")}
  ${sliceBlock(inlineScript(indexSource), "function getActiveInstance")}
  ${sliceBlock(inlineScript(indexSource), "function syncActiveWebStatus")}
  return { syncActiveWebStatus };
`);

function indexSyncHarness({ kind = "dsh-web", renderer = { syncStatus: () => {} } } = {}) {
  const state = {
    activeInst: "inst1",
    instances: [{ id: "inst1", kind, status: "running" }],
  };
  const calls = [];
  const errors = [];
  const window = { KindRenderers: {} };
  if (renderer) {
    window.KindRenderers[kind] = {
      syncStatus: (inst) => {
        calls.push(inst.id);
        if (renderer.boom) throw new Error("renderer blew up");
      },
    };
  }
  const api = webStatusFactory({ state, window, console: { error: (...a) => errors.push(a.join(" ")) } });
  return { api, calls, errors, state, window };
}

test("index.html: syncActiveWebStatus dispatches to the active web renderer", () => {
  const h = indexSyncHarness({ kind: "dsh-web" });
  h.api.syncActiveWebStatus();
  assert.deepEqual(h.calls, ["inst1"], "the dsh-web renderer receives the active instance");

  const oc = indexSyncHarness({ kind: "opencode-web" });
  oc.api.syncActiveWebStatus();
  assert.deepEqual(oc.calls, ["inst1"], "the opencode-web renderer receives it too");
});

test("index.html: syncActiveWebStatus skips non-web kinds and renderers without syncStatus", () => {
  const pty = indexSyncHarness({ kind: "pty" });
  pty.api.syncActiveWebStatus();
  assert.deepEqual(pty.calls, [], "a pty instance is not a web kind");

  // reasonix is deliberately NOT wired (its own 2s tick self-heals): a
  // renderer without syncStatus must be skipped, not crash the dispatch.
  const rx = indexSyncHarness({ kind: "reasonix", renderer: null });
  rx.window.KindRenderers.reasonix = {}; // registered, but exposes no syncStatus
  assert.doesNotThrow(() => rx.api.syncActiveWebStatus(), "no syncStatus must be a silent no-op");
  assert.deepEqual(rx.errors, [], "and no error is even logged");

  const none = indexSyncHarness({ kind: "dsh-web", renderer: null });
  assert.doesNotThrow(() => none.api.syncActiveWebStatus(), "a missing renderer is a no-op");

  const noTab = indexSyncHarness({ kind: "dsh-web" });
  noTab.state.activeInst = null;
  noTab.api.syncActiveWebStatus();
  assert.deepEqual(noTab.calls, [], "no active tab, nothing to sync");
});

test("index.html: a throwing renderer never breaks the refresh dispatch", () => {
  const h = indexSyncHarness({ kind: "dsh-web", renderer: { boom: true } });
  assert.doesNotThrow(() => h.api.syncActiveWebStatus(), "a renderer error must not escape");
  assert.equal(h.errors.length, 1, "but it is still logged");
});
