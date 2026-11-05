import test from "node:test";
import assert from "node:assert/strict";
import { readFileSync } from "node:fs";
import { fileURLToPath } from "node:url";
import { dirname, join } from "node:path";

const here = dirname(fileURLToPath(import.meta.url));
const source = readFileSync(join(here, "..", "debugassets", "viewport_debug.js"), "utf8");

function loadAPI() {
  const module = { exports: {} };
  new Function("window", "document", "module", source)(null, null, module);
  return module.exports;
}

function fakeRect(top, bottom, width, height) {
  return { top, bottom, width, height };
}

// DOM listener options are either a boolean (the legacy capture flag) or an
// options object; the identity of a listener is the (type, handler, capture) triple.
function captureFlag(options) {
  if (typeof options === "boolean") return options;
  return !!(options && options.capture);
}

function fakeElement(rect, style = {}, classes = []) {
  const classSet = new Set(classes);
  return {
    style: { ...style },
    classList: { contains: (name) => classSet.has(name) },
    getBoundingClientRect: () => rect,
  };
}

// The v3.2 stand-in, laid out like the measured iPad-landscape shell: a
// FULL-HEIGHT shell (app 0 → 1180 against clientHeight 1180), #workspace inside
// the 16px safe-area padding (bottom 1164), and an OPEN keyboard-session frame at
// vv.height 860 — the EDITABLE field was focused and the keyboard entered across
// the 50% line, so inside the session every PTY host's own bottom carries the raw
// 320px, landing its real bottom at 844, above the covered strip. The probe reads
// that 320 off window.__mwTerminalOcclusion — the applier's mirror — it does not
// re-derive it. Nothing in this page is scrollable, and #app carries NO inline
// height.
const LAYOUT_HEIGHT = 1180;
const VV_HEIGHT = 860;
const WORKSPACE_BOTTOM = 1164;
const OCCLUSION = LAYOUT_HEIGHT - VV_HEIGHT; // 320
const HOST_BOTTOM = WORKSPACE_BOTTOM - OCCLUSION; // 844
// #terminal-controls' authored bottom, straight from the shipped CSS. The
// avoidance adds the occlusion ON TOP of it, so the column lifts while keeping
// its clearance over the transport strip — the two numbers a verdict compares.
const CONTROLS_AUTHORED_BOTTOM = 20;

function makePage({ withVV = true, hosts = [true, false] } = {}) {
  const app = fakeElement(
    fakeRect(0, LAYOUT_HEIGHT, 820, LAYOUT_HEIGHT),
    { height: "", position: "fixed", paddingTop: "0px", paddingRight: "0px", paddingBottom: "16px", paddingLeft: "0px", backgroundColor: "rgba(0, 0, 0, 0)" },
    ["collapsed"],
  );
  const main = fakeElement(fakeRect(0, LAYOUT_HEIGHT, 620, LAYOUT_HEIGHT));
  const workspace = fakeElement(fakeRect(40, WORKSPACE_BOTTOM, 620, WORKSPACE_BOTTOM - 40));
  const xtermScreen = fakeElement(fakeRect(40, HOST_BOTTOM - 1, 620, HOST_BOTTOM - 41));
  const xtermRows = fakeElement(fakeRect(40, HOST_BOTTOM - 10, 620, HOST_BOTTOM - 50));
  const host = fakeElement(fakeRect(40, HOST_BOTTOM, 620, HOST_BOTTOM - 40), { bottom: OCCLUSION + "px", display: "block" });
  host.querySelector = (selector) => selector === ".xterm-screen" ? xtermScreen : selector === ".xterm-rows" ? xtermRows : null;
  // Every PTY host inside #terminal-container, the visible one and the hidden
  // one alike — that is what the p7 host list is for.
  const terminalContainer = fakeElement(fakeRect(40, WORKSPACE_BOTTOM, 620, WORKSPACE_BOTTOM - 40));
  terminalContainer.hosts = hosts.map((visible, index) => fakeElement(
    fakeRect(40, HOST_BOTTOM, 620, HOST_BOTTOM - 40),
    { bottom: OCCLUSION + "px", display: visible ? "block" : "none" },
  ));
  terminalContainer.querySelectorAll = (selector) => {
    if (selector !== ":scope > [data-instance-id]") throw new Error("unexpected host selector: " + selector);
    return terminalContainer.hosts;
  };
  // The two overlay children of #workspace and the quick-keys panel inside the
  // column. v3.0 lifts these too — #workspace stays full height, so they would
  // sit under the keyboard otherwise — and the probe reports each one's rect,
  // the bottom the applier wrote, and (for the panel) the COMPUTED max-height,
  // which is the number that proves the column really shortened.
  const controlsBottom = CONTROLS_AUTHORED_BOTTOM + OCCLUSION; // 480
  const controls = fakeElement(
    fakeRect(60, WORKSPACE_BOTTOM - controlsBottom, 44, WORKSPACE_BOTTOM - controlsBottom - 60),
    { bottom: controlsBottom + "px", display: "block" },
  );
  const statusBar = fakeElement(
    fakeRect(WORKSPACE_BOTTOM - OCCLUSION - 17, WORKSPACE_BOTTOM - OCCLUSION, 120, 17),
    { bottom: OCCLUSION + "px", display: "block" },
  );
  const quickKeys = fakeElement(
    fakeRect(60, 60 + (WORKSPACE_BOTTOM - controlsBottom - 60), 200, WORKSPACE_BOTTOM - controlsBottom - 60),
    { display: "block" },
  );
  quickKeys.clientHeight = WORKSPACE_BOTTOM - controlsBottom - 60;
  // Two rows of keys taller than the shortened box: the internal scroll is the
  // only thing that keeps them reachable, so the fixture has to be overflowable.
  quickKeys.scrollHeight = quickKeys.clientHeight + 96;
  // B-1: the listener stands-in are keyed by `type|capture`, and removal matches
  // on the SAME triple the DOM matches on (type, handler, capture). Keyed by the
  // bare type alone, a removeEventListener that forgot its capture flag still
  // "worked" here — which is exactly how a capture-phase `click` handler stayed on
  // the product's document forever while every test stayed green.
  const listeners = new Map();
  const docListeners = new Map();
  const bucketKey = (type, options) => type + "|" + (captureFlag(options) ? "capture" : "bubble");
  function addTo(map, type, handler, options) {
    const key = bucketKey(type, options);
    if (!map.has(key)) map.set(key, []);
    map.get(key).push(handler);
  }
  function removeFrom(map, type, handler, options) {
    const key = bucketKey(type, options);
    map.set(key, (map.get(key) || []).filter((item) => item !== handler));
  }
  const vv = withVV ? {
    width: 820, height: VV_HEIGHT, offsetTop: 0, offsetLeft: 0, scale: 1, pageTop: 0,
    addEventListener(type, handler, options) { addTo(listeners, type, handler, options); },
    removeEventListener(type, handler, options) { removeFrom(listeners, type, handler, options); },
  } : null;
  const state = {
    activeInst: "instance-private-id",
    instances: [{ id: "instance-private-id", kind: "pty", status: "running", name: "must-not-leak" }],
  };
  const page = {
    window: {
      innerWidth: 820,
      innerHeight: 1180,
      devicePixelRatio: 2,
      // v3.2: the probe's occlusion is READ from this mirror the applier writes
      // (window.__mwTerminalOcclusion in index.html), never re-derived here.
      // This fixture is a LIVE keyboard session mid-state — every host already
      // carries bottom 320px, so the applier's mirror is 320 too. A page where
      // no pass has run (or the session is closed) leaves it 0/absent, and the
      // cases below spell that out.
      __mwTerminalOcclusion: withVV ? OCCLUSION : 0,
      navigator: {
        userAgent: "Mozilla/5.0 (iPad; CPU OS 18_0 like Mac OS X) Safari/605.1.15",
        platform: "iPad",
        maxTouchPoints: 5,
      },
      screen: { width: 820, height: 1180, orientation: { type: "portrait-primary" } },
      matchMedia: () => ({ matches: true }),
      visualViewport: vv,
      getComputedStyle(element) {
        if (element === app) return app.style;
        // issue #116 contract v2.2 / v3.0: the two ROOT boxes answer the
        // overflow question, so the stand-in has to answer it too — a stub with
        // no `overflow` would make snapshot.document.htmlOverflow "" and the
        // structural field would never be exercised by any test.
        if (element === page.document.documentElement || element === page.document.body) {
          return { backgroundColor: "rgb(11, 16, 32)", overflow: "clip" };
        }
        // A PTY host also answers `bottom`, because v3.0's whole claim lives in
        // that one longhand and the probe reports the computed value next to the
        // inline one the applier wrote.
        if (element && element.style && element.style.bottom) {
          return { backgroundColor: "rgb(11, 16, 32)", bottom: element.style.bottom };
        }
        if (element === quickKeys) {
          return { backgroundColor: "rgb(13, 17, 23)", maxHeight: quickKeys.clientHeight + "px" };
        }
        return { backgroundColor: "rgb(11, 16, 32)" };
      },
    },
    document: {
      body: { scrollTop: 0 },
      scrollingElement: { scrollTop: 0, scrollHeight: 1164 },
      documentElement: { clientWidth: 820, clientHeight: LAYOUT_HEIGHT, scrollHeight: LAYOUT_HEIGHT },
      activeElement: { tagName: "TEXTAREA" },
      // Phase 2 task 1: the probe journals `click` / `touchstart` and registers
      // focus listeners on the document, so the stand-in has to take them.
      addEventListener(type, handler, options) { addTo(docListeners, type, handler, options); },
      removeEventListener(type, handler, options) { removeFrom(docListeners, type, handler, options); },
      querySelector: () => null,
      // issue #116: the bottom-edge probe hit-tests this. Returning the
      // workspace stands in for "the pixels at the bottom edge are the app's own
      // ink", the healthy state; a case that wants to simulate an exposed root
      // canvas overrides this to return the documentElement.
      elementFromPoint: () => ({ tagName: "DIV", id: "workspace", className: "" }),
      getElementById(id) {
        if (id === "app") return app;
        if (id === "main") return main;
        if (id === "workspace") return workspace;
        if (id === "terminal-container") return terminalContainer;
        if (id === "terminal-controls") return controls;
        if (id === "status-bar") return statusBar;
        if (id === "quick-keys-panel") return quickKeys;
        return null;
      },
    },
    state,
    getActiveTerminalSession: () => ({ container: host, quickKeysOpen: true }),
  };
  return { page, app, main, workspace, host, terminalContainer, controls, statusBar, quickKeys, vv, listeners, docListeners };
}

function makeHarness(options = {}) {
  const { page, listeners, docListeners } = makePage(options);
  const frames = [];
  const timers = new Map();
  let nextId = 0;
  const uploads = [];
  const client = loadAPI().createClient({
    sessionId: "0123456789abcdef",
    initialSeq: options.initialSeq || 0,
    page,
    upload: options.upload || (async (report) => { uploads.push(report); }),
    now: () => new Date("2026-10-27T12:00:00.000Z"),
    requestAnimationFrame(callback) {
      const id = ++nextId;
      frames.push({ id, callback });
      return id;
    },
    cancelAnimationFrame(id) {
      const at = frames.findIndex((item) => item.id === id);
      if (at >= 0) frames.splice(at, 1);
    },
    setTimeout(callback, ms) {
      const id = ++nextId;
      timers.set(id, { callback, ms });
      return id;
    },
    clearTimeout(id) { timers.delete(id); },
  });
  return {
    client, page, listeners, docListeners, uploads, frames, timers,
    flushFrames() {
      while (frames.length) frames.shift().callback();
    },
    runTimer() {
      const [id, timer] = timers.entries().next().value || [];
      assert.ok(id, "expected a pending timer");
      timers.delete(id);
      timer.callback();
    },
    dispatch(type, target) {
      const handlers = [];
      for (const [key, list] of docListeners) {
        if (key.split("|")[0] === type) handlers.push(...list.slice());
      }
      assert.ok(handlers.length > 0, `no document listener registered for ${type}`);
      for (const handler of handlers) handler({ type, target });
    },
  };
}

const flushPromises = () => new Promise((resolve) => setImmediate(resolve));

test("snapshot captures geometry and UI state without content, token, names or paths", () => {
  const { page } = makePage();
  const snapshot = loadAPI().snapshotFromPage(page, "manual", "2026-10-27T12:00:00.000Z");
  assert.equal(snapshot.reason, "manual");
  assert.equal(snapshot.vv.height, VV_HEIGHT);
  // issue #116 contract v3.0: the shell is FULL HEIGHT. app.bottom answers to
  // documentElement.clientHeight, never to vv.height, so layoutGap is 0 even
  // with the keyboard up at vv.height 860 — and inlineHeight, the channel v2.2
  // pinned, is empty.
  assert.equal(snapshot.app.bottom, LAYOUT_HEIGHT);
  assert.equal(snapshot.app.paddingBottom, "16px");
  assert.equal(snapshot.app.inlineHeight, "");
  assert.equal(snapshot.app.layoutGap, 0);
  assert.equal(snapshot.app.computedPosition, "fixed");
  // …while the occlusion is owed to, and paid by, every PTY host.
  assert.equal(snapshot.occlusion, OCCLUSION);
  assert.deepEqual(snapshot.ptyHosts, [
    { index: 0, top: 40, bottom: HOST_BOTTOM, height: HOST_BOTTOM - 40, inlineBottom: "320px", computedBottom: "320px", visible: true },
    { index: 1, top: 40, bottom: HOST_BOTTOM, height: HOST_BOTTOM - 40, inlineBottom: "320px", computedBottom: "320px", visible: false },
  ]);
  assert.deepEqual(snapshot.device, {
    userAgent: "Mozilla/5.0 (iPad; CPU OS 18_0 like Mac OS X) Safari/605.1.15",
    platform: "iPad",
    touchPoints: 5,
    orientation: "portrait-primary",
    screenWidth: 820,
    screenHeight: 1180,
    coarse: true,
  });
  assert.deepEqual(snapshot.document, {
    bodyBackground: "rgb(11, 16, 32)",
    htmlBackground: "rgb(11, 16, 32)",
    // issue #116 contract v2.2: the structural trio. deepEqual is the point —
    // an absent field is as loud as a wrong one, which is the same guard that
    // caught the pageTop drift between client and server.
    htmlOverflow: "clip",
    bodyOverflow: "clip",
    rootScrollHeight: 1180,
    pageTop: 0,
    scrollTop: 0,
    scrollingElementScrollTop: 0,
    scrollingElementScrollHeight: 1164,
    bodyScrollTop: 0,
  });
  // issue #116: the bottom-edge probe must report what is actually painted at
  // the last visible pixel, at BOTH the visual and layout bottoms. The stubbed
  // document reports app ink under both, which is the healthy answer; a case
  // that returns the root canvas there is what an exposed-band capture looks
  // like, and that is the distinction the field exists to make.
  assert.deepEqual(snapshot.probe, {
    visualBottom: { y: VV_HEIGHT - 2, tag: "DIV", id: "workspace", className: "", background: "rgb(11, 16, 32)" },
    layoutBottom: { y: 1178, tag: "DIV", id: "workspace", className: "", background: "rgb(11, 16, 32)" },
    // elementFromPoint is a CLIENT-space API; pageTop is recorded alongside so the
    // two coordinate systems in one snapshot are never confused again.
    pageTop: 0,
  });
  assert.equal(snapshot.workspace.bottom, WORKSPACE_BOTTOM);
  // The terminal is the box that moves: its bottom sits OCCLUSION above the
  // workspace floor, and the rows it paints stay above the covered strip.
  assert.equal(snapshot.terminal.bottom, HOST_BOTTOM);
  assert.equal(snapshot.terminal.bottom <= VV_HEIGHT + 1, true, "no terminal row may sit under the keyboard");
  assert.equal(snapshot.webPanel, null);
  assert.equal(snapshot.xtermScreen.bottom, HOST_BOTTOM - 1);
  assert.equal(snapshot.xtermRows.bottom, HOST_BOTTOM - 10);
  assert.deepEqual(snapshot.ui, {
    collapsed: true,
    quickKeysOpen: true,
    kind: "pty",
    status: "running",
    activeElementTag: "TEXTAREA",
  });
  const serialized = JSON.stringify(snapshot);
  for (const forbidden of ["instance-private-id", "must-not-leak", "token", "worktree", "terminalText", "/home/"]) {
    assert.equal(serialized.includes(forbidden), false, `snapshot leaked ${forbidden}`);
  }
});

// --- p7 / contract v3.0: the fields the new verdict reads ---------------------

test("p9 stamps every snapshot with the version that measures v3.2", () => {
  const { page } = makePage();
  const snapshot = loadAPI().snapshotFromPage(page, "manual", "2026-10-27T12:00:00.000Z");
  assert.equal(snapshot.probeVersion, "p9-full-shell-session-avoidance-v1", "the stamp is what stops a capture from certifying the wrong build");
});

test("p9 reports the applier's mirrored occlusion, never a re-derived ratio", () => {
  const api = loadAPI();
  // v3.2 moved the occlusion JUDGMENT into the applier's keyboard-session state
  // machine (index.html) and the probe now READS window.__mwTerminalOcclusion.
  // It must not re-run the retired v3.1 floor (raw < clientHeight*0.25 ? 0 :
  // raw) here — that arithmetic would call a MID-SESSION 147px retract frame
  // "exempt" and a post-session shallow strip "owed", which is exactly backwards
  // and is what re-lifted the terminal on the device. What the applier wrote IS
  // the truth the probe carries.
  const thin = makePage();
  thin.vv.height = LAYOUT_HEIGHT - Math.floor(LAYOUT_HEIGHT * 0.25) + 40; // a shortfall under the retired 0.25 line
  thin.page.window.__mwTerminalOcclusion = 0; // the applier closed/left the session → wrote 0
  const thinSnapshot = api.snapshotFromPage(thin.page, "resize", "2026-10-27T12:00:00.000Z");
  assert.ok(thinSnapshot.vv.height > 0, "the viewport really did shrink");
  assert.equal(thinSnapshot.occlusion, 0, "the probe mirrors the applier's 0, not a re-derived raw shortfall");

  // Mid-session under the retired floor: the session is LIVE, so the applier
  // paid raw and the probe must carry that raw — the retired floor would have
  // reported 0 here and certified the bounce as correct.
  const mid = makePage();
  mid.vv.height = LAYOUT_HEIGHT - 147; // raw 147, under the retired 295 line
  mid.page.window.__mwTerminalOcclusion = 147; // session live, hosts pay raw
  const midSnapshot = api.snapshotFromPage(mid.page, "resize", "2026-10-27T12:00:00.000Z");
  assert.equal(midSnapshot.occlusion, 147, "a live session's raw shortfall is reported RAW — geometry alone cannot re-classify it");

  // An unset mirror (no pass ran yet) reads 0 — exactly what the applier owes
  // before the first keyboard enters.
  const fresh = makePage();
  delete fresh.page.window.__mwTerminalOcclusion;
  assert.equal(api.snapshotFromPage(fresh.page, "resize", "2026-10-27T12:00:00.000Z").occlusion, 0, "an unset mirror reads 0");
});

test("p7 lists EVERY PTY host, hidden included, with its own bottom", () => {
  const { page, terminalContainer } = makePage({ hosts: [true, false, false] });
  const snapshot = loadAPI().snapshotFromPage(page, "manual", "2026-10-27T12:00:00.000Z");
  assert.equal(snapshot.ptyHosts.length, 3, "a host the user has not switched to yet still has to appear — a stale bottom there is the next band");
  assert.deepEqual(snapshot.ptyHosts.map((host) => host.visible), [true, false, false]);
  assert.deepEqual([...new Set(snapshot.ptyHosts.map((host) => host.inlineBottom))], ["320px"], "no host may carry a different bottom");
  assert.deepEqual(snapshot.ptyHosts.map((host) => host.index), [0, 1, 2], "index is sequential: the verifier reads hosts in order");
  // A stale host is exactly what the list exists to catch: lift one host and
  // its own value has to show up, not the average.
  terminalContainer.hosts[2].style.bottom = "0px";
  terminalContainer.hosts[2].getBoundingClientRect = () => ({ top: 40, bottom: WORKSPACE_BOTTOM, height: WORKSPACE_BOTTOM - 40, width: 620 });
  const second = loadAPI().snapshotFromPage(page, "manual", "2026-10-27T12:00:00.000Z");
  assert.deepEqual(second.ptyHosts.map((host) => host.inlineBottom), ["320px", "320px", "0px"], "a stale host must be visible in the data, not smoothed over");
  assert.deepEqual(second.ptyHosts.map((host) => host.bottom), [HOST_BOTTOM, HOST_BOTTOM, WORKSPACE_BOTTOM]);
});

test("p7 reports the controls column and the status strip with their own bottoms", () => {
  // The second half of the LOCAL avoidance: #terminal-controls and #status-bar
  // are lifted by the SAME occlusion, each from its OWN authored bottom (20 +
  // occlusion for the column, occlusion for the strip). The probe has to report
  // what each box was written, because a verdict that only reads the hosts cannot
  // see quick-keys sitting under the keyboard.
  const { page, controls, statusBar } = makePage();
  const snapshot = loadAPI().snapshotFromPage(page, "manual", "2026-10-27T12:00:00.000Z");
  assert.equal(snapshot.controls.inlineBottom, (CONTROLS_AUTHORED_BOTTOM + OCCLUSION) + "px", "the column's inline bottom is the authored 20px plus the occlusion");
  assert.equal(snapshot.controls.computedBottom, snapshot.controls.inlineBottom, "computed agrees with inline — the longhand won, in the engine's own words");
  assert.equal(snapshot.controls.bottom, WORKSPACE_BOTTOM - CONTROLS_AUTHORED_BOTTOM - OCCLUSION, "the column's rect bottom lifted by the occlusion");
  assert.ok(snapshot.controls.visible, "the column is on screen in this fixture");
  assert.equal(snapshot.statusBar.inlineBottom, OCCLUSION + "px", "the strip lifts by the occlusion itself, its authored bottom being 0");
  assert.equal(snapshot.statusBar.bottom, WORKSPACE_BOTTOM - OCCLUSION, "and its rect bottom says so");
  // The one number a reviewer is likely to get wrong: the two bottoms are NOT
  // equal, and they must not be — the column keeps its clearance.
  assert.notEqual(snapshot.controls.inlineBottom, snapshot.statusBar.inlineBottom, "the column keeps its 20px clearance, it does not copy the host target");

  // A missing overlay is null, never a zero — "absent" and "at zero" are
  // different claims and the verifier answers them differently.
  const original = page.document.getElementById;
  page.document.getElementById = (id) => (id === "terminal-controls" || id === "status-bar" ? null : original(id));
  const bare = loadAPI().snapshotFromPage(page, "manual", "2026-10-27T12:00:00.000Z");
  assert.equal(bare.controls, null, "no column, no numbers invented");
  assert.equal(bare.statusBar, null, "…and the same for the strip");
  assert.equal(bare.ptyHosts.length, 2, "…while the hosts are still reported — one missing overlay never blinds the capture");
  void controls; void statusBar;
});

test("p7 reports the quick-keys panel's computed max-height against its own scroll", () => {
  // `max-height: 100%` is a percentage, and a percentage is only a promise until
  // an engine resolves it. The probe reports the COMPUTED max-height next to
  // clientHeight / scrollHeight, so a capture states that the column actually got
  // shorter and the key set is still scrollable inside it.
  const { page, quickKeys } = makePage();
  const snapshot = loadAPI().snapshotFromPage(page, "manual", "2026-11-03T12:00:00.000Z");
  assert.equal(snapshot.quickKeys.maxHeight, quickKeys.clientHeight + "px", "the computed max-height is the resolved number, not the authored percentage");
  assert.equal(snapshot.quickKeys.maxHeight, snapshot.controls.height + "px", "…and it equals the shortened column's height, which is the containing block it resolves against");
  assert.ok(snapshot.quickKeys.scrollHeight > snapshot.quickKeys.clientHeight, "content taller than the box: the internal scroll is live, not a clipped row");
  assert.equal(snapshot.quickKeys.open, false, "the fixture panel is closed; `open` reflects the class, and a closed panel is still measured");

  // No panel on the page => null, and the rest of the capture survives.
  const original = page.document.getElementById;
  page.document.getElementById = (id) => (id === "quick-keys-panel" ? null : original(id));
  assert.equal(loadAPI().snapshotFromPage(page, "manual", "2026-11-03T12:00:00.000Z").quickKeys, null, "absent panel is reported absent");
});

test("p7 occlusion: no visualViewport or zoom both mean nothing is owed", () => {
  // v3.2 spelling: the applier's session machine writes 0 on all three edges —
  // no visualViewport, zoom, keyboard closed — and the probe mirrors the 0. The
  // probe itself judges nothing; it reads window.__mwTerminalOcclusion.
  const withoutVV = makePage({ withVV: false });
  const snapshot = loadAPI().snapshotFromPage(withoutVV.page, "init", "2026-10-27T12:00:00.000Z");
  assert.equal(snapshot.vv, null);
  assert.equal(snapshot.occlusion, 0, "no visualViewport, nothing covered: the shell and the hosts stay put");
  assert.equal(snapshot.app.layoutGap, 0);

  const zoomed = makePage();
  zoomed.vv.scale = 2;
  zoomed.vv.height = 450;
  zoomed.page.window.__mwTerminalOcclusion = 0; // zoom ENDED the session; the applier wrote 0
  const zoomedSnapshot = loadAPI().snapshotFromPage(zoomed.page, "resize", "2026-10-27T12:00:00.000Z");
  assert.equal(zoomedSnapshot.occlusion, 0, "zoom owns nothing — a pinch must not lift the terminal off a page the user is panning");

  const open = makePage();
  open.vv.height = LAYOUT_HEIGHT;
  open.page.window.__mwTerminalOcclusion = 0; // raw 0 closed the session
  assert.equal(loadAPI().snapshotFromPage(open.page, "resize", "2026-10-27T12:00:00.000Z").occlusion, 0, "the real floating overlay never enters visualViewport: occlusion 0 by arithmetic");
});

test("p7 survives a page with no #terminal-container", () => {
  const { page } = makePage();
  const original = page.document.getElementById;
  page.document.getElementById = (id) => (id === "terminal-container" ? null : original(id));
  const snapshot = loadAPI().snapshotFromPage(page, "manual", "2026-10-27T12:00:00.000Z");
  assert.deepEqual(snapshot.ptyHosts, [], "a web-kind page owns no PTY host; the snapshot reports that instead of throwing");
  assert.equal(snapshot.occlusion, OCCLUSION, "the occlusion is a fact about the viewport, independent of which kind is showing");
});

test("snapshot degrades to vv=null when visualViewport is unavailable", () => {
  const { page } = makePage({ withVV: false });
  const snapshot = loadAPI().snapshotFromPage(page, "init", "2026-10-27T12:00:00.000Z");
  assert.equal(snapshot.vv, null);
});

test("client captures init, two-frame resize and 250ms settle with increasing seq", async () => {
  const h = makeHarness();
  h.client.start();
  await flushPromises();
  assert.deepEqual(h.uploads.map((r) => [r.seq, r.snapshot.reason]), [[1, "init"]]);

  h.page.window.visualViewport.height = 300;
  h.client.schedule("resize");
  h.flushFrames();
  await flushPromises();
  assert.deepEqual(h.uploads.map((r) => [r.seq, r.snapshot.reason]), [[1, "init"], [2, "resize"]]);
  assert.equal(h.uploads[1].snapshot.vv.height, 300);

  h.runTimer();
  await flushPromises();
  assert.deepEqual(h.uploads.map((r) => [r.seq, r.snapshot.reason]), [[1, "init"], [2, "resize"], [3, "settle"]]);
  assert.equal(h.client.getStatus().lastUploadedSeq, 3);
});

test("stop cancels a scheduled retry and never uploads the failed report", async () => {
  let attempts = 0;
  const uploaded = [];
  const h = makeHarness({
    upload: async (report) => {
      attempts++;
      if (attempts === 1) throw new Error("offline");
      uploaded.push(report);
    },
  });
  h.client.start();
  await flushPromises();
  assert.equal(h.client.getStatus().lastError, "upload failed; retrying");
  assert.equal(h.timers.size, 1);

  h.client.stop();
  assert.equal(h.timers.size, 0);
  await flushPromises();
  assert.equal(attempts, 1);
  assert.deepEqual(uploaded, []);
});

test("a newer capture replaces the failed report waiting for retry", async () => {
  const calls = [];
  let rejectFirst;
  const h = makeHarness({
    upload: (report) => new Promise((resolve, reject) => {
      calls.push(report);
      if (calls.length === 1) rejectFirst = reject;
      else resolve();
    }),
  });
  h.client.start();
  assert.equal(calls.length, 1);
  h.client.capture("manual");
  assert.equal(calls.length, 1, "the active upload prevents a concurrent second request");
  rejectFirst(new Error("offline"));
  await flushPromises();
  assert.equal(h.timers.size, 1);
  h.runTimer();
  await flushPromises();
  assert.deepEqual(calls.map((r) => r.seq), [1, 2]);
  assert.equal(calls[1].snapshot.reason, "manual");
  assert.equal(h.client.getStatus().lastUploadedSeq, 2);
});

test("stop cancels scheduled capture and listeners without touching the uploaded history", async () => {
  const h = makeHarness();
  h.client.start();
  await flushPromises();
  assert.equal(h.page.window.visualViewport !== null, true);
  h.client.schedule("resize");
  assert.equal(h.frames.length, 1);
  h.client.stop();
  assert.equal(h.frames.length, 0);
  assert.equal(h.timers.size, 0);
  assert.equal(h.client.getStatus().running, false);
  for (const handlers of h.listeners.values()) assert.deepEqual(handlers, []);
  h.client.schedule("resize");
  h.client.capture("manual");
  await flushPromises();
  assert.deepEqual(h.uploads.map((r) => r.snapshot.reason), ["init"]);
});

test("pause suppresses new captures and resume emits one manual snapshot", async () => {
  const h = makeHarness();
  h.client.start();
  await flushPromises();
  h.client.setPaused(true);
  assert.equal(h.client.capture("manual"), null);
  h.client.schedule("resize");
  assert.equal(h.frames.length, 0);
  h.client.setPaused(false);
  await flushPromises();
  assert.deepEqual(h.uploads.map((r) => r.snapshot.reason), ["init", "manual"]);
});

test("initialSeq resumes an existing debug session instead of restarting at one", async () => {
  const h = makeHarness({ initialSeq: 74 });
  h.client.start();
  await flushPromises();
  assert.equal(h.uploads.length, 1);
  assert.equal(h.uploads[0].seq, 75);
});

test("continuous mode emits periodic snapshots, pauses/resumes and stops cleanly", async () => {
  const h = makeHarness();
  h.client.start();
  h.client.startContinuous(1000);
  assert.equal(h.client.getStatus().continuous, true);
  h.runTimer();
  await flushPromises();
  h.runTimer();
  await flushPromises();
  assert.deepEqual(h.uploads.map((r) => [r.seq, r.snapshot.reason]), [[1, "init"], [2, "periodic"], [3, "periodic"]]);

  h.client.setPaused(true);
  assert.equal(h.timers.size, 0);
  h.client.setPaused(false);
  await flushPromises();
  assert.equal(h.uploads.at(-1).snapshot.reason, "manual");
  h.runTimer();
  await flushPromises();
  assert.equal(h.uploads.at(-1).snapshot.reason, "periodic");

  h.client.stopContinuous();
  assert.equal(h.timers.size, 0);
  assert.equal(h.client.getStatus().continuous, false);
  h.client.stop();
  assert.equal(h.timers.size, 0);
});

test("createClient rejects an invalid session or missing upload", () => {
  const api = loadAPI();
  assert.throws(() => api.createClient({ sessionId: "short", upload: async () => {} }), /invalid diagnostic session/);
  assert.throws(() => api.createClient({ sessionId: "0123456789abcdef" }), /upload is required/);
});

// A0 采证实测缺陷：原先面板固定 72vh 且按钮排在明细 JSON 之后，iPad 软键盘
// 弹出（visualViewport.height≈274/633）时主按钮整排被键盘埋掉，无法上传
// keyboard-hint。以下用例钉住"主按钮可达"的几何契约。
test("viewport debug panel layout keeps primary controls above the keyboard", () => {
  const api = loadAPI();
  const px = (value) => parseFloat(value);

  // 软键盘弹出：可视高 274，必须折叠且高度不超过可视区。
  const kb = api.panelLayoutFor({ innerHeight: 702, vvHeight: 274, vvWidth: 1194, innerWidth: 1194, safeBottom: 20, safeTop: 24 });
  assert.equal(kb.collapsed, true, "short visual viewport must collapse the panel");
  assert.ok(px(kb.maxHeight) + px(kb.top) <= 274 + 1, `collapsed panel ${kb.maxHeight}+${kb.top} must fit vv 274`);

  // 键盘收起回全高：允许展开，但仍不得超出可视区。
  const full = api.panelLayoutFor({ innerHeight: 702, vvHeight: 702, vvWidth: 1194, innerWidth: 1194, safeBottom: 20, safeTop: 24, collapsed: false });
  assert.equal(full.collapsed, false);
  assert.ok(px(full.maxHeight) + px(full.top) <= 702 + 1, `expanded panel ${full.maxHeight}+${full.top} must fit vv 702`);

  // 地址栏/滚动中间态：可视高 633.5（实测 seq269–276）仍应保持折叠阈值内的紧凑形态可用。
  const mid = api.panelLayoutFor({ innerHeight: 702, vvHeight: 633.5, vvWidth: 1194, innerWidth: 1194, safeBottom: 20 });
  assert.ok(px(mid.maxHeight) + px(mid.top) <= 633.5 + 1);

  // 六分屏窄宽（实测 innerWidth=392/592）：面板宽度必须收缩且不越界。
  for (const width of [392, 592, 618]) {
    const narrow = api.panelLayoutFor({ innerWidth: width, innerHeight: 702, vvWidth: width, vvHeight: 702 });
    assert.ok(px(narrow.width) <= width - 16 + 0.5, `panel ${narrow.width} must fit ${width}px Split View`);
  }

  // 左上角（避开右上 quick-keys toggle）与拖动夹紧。
  const tl = api.panelLayoutFor({ innerWidth: 1194, innerHeight: 702, vvWidth: 1194, vvHeight: 702, corner: "tl", safeTop: 24 });
  assert.equal(tl.right, "auto");
  assert.equal(px(tl.top), 24 + 8);
  const dragged = api.panelLayoutFor({ innerWidth: 1194, innerHeight: 702, vvWidth: 1194, vvHeight: 274, corner: "free", dragX: 9999, dragY: 9999 });
  assert.ok(px(dragged.left) + px(dragged.width) <= 1194, "dragged panel must be clamped horizontally");
  assert.ok(px(dragged.top) <= 274, "dragged panel must stay above the keyboard");
});

test("viewport debug panel reserves the quick-keys control column when docked right", () => {
  const api = loadAPI();
  const px = (value) => parseFloat(value);
  const right = api.panelLayoutFor({ innerWidth: 1194, innerHeight: 702, vvWidth: 1194, vvHeight: 702, corner: "tr", reservedRight: 40 });
  assert.equal(px(right.right), 48, "right inset must clear the control column plus margin");
  const left = api.panelLayoutFor({ innerWidth: 1194, innerHeight: 702, vvWidth: 1194, vvHeight: 702, corner: "tl", reservedRight: 40 });
  assert.equal(px(left.left), 8, "left docking ignores the right reservation");
  const hidden = api.panelLayoutFor({ innerWidth: 1194, innerHeight: 702, vvWidth: 1194, vvHeight: 702, corner: "tr", reservedRight: 0 });
  assert.equal(px(hidden.right), 8, "a hidden control column reserves nothing");
  assert.ok(px(right.right) + px(right.width) <= 1194, "reserved panel must stay inside the viewport");
});

test("every snapshot states which probe build produced it", () => {
  // Two verification rounds were spent on reports that turned out to describe a
  // page still running the previous build (the document carried no cache
  // directive, so a plain reload could reuse it). The stamp makes a capture
  // self-identifying; this pins that it is present and non-empty.
  const { page } = makePage();
  const snapshot = loadAPI().snapshotFromPage(page, "manual", "2026-10-27T12:00:00.000Z");
  assert.equal(typeof snapshot.probeVersion, "string", "probeVersion must be a string");
  assert.ok(snapshot.probeVersion.length >= 4, `probeVersion must be non-trivial, got ${JSON.stringify(snapshot.probeVersion)}`);
});

test("the probe reports the STRUCTURE the v3.0 contract rests on, and never writes it", () => {
  // p6 moved the guarantee from behaviour to structure: the capture has to say
  // WHY the page cannot scroll (a fixed shell inside two clipped root boxes and a
  // root scroll range of zero), not merely that scroll happened to be 0. Without
  // these three fields a capture taken on a build that still repairs the page
  // looks identical to a capture taken on one that cannot scroll at all.
  const { page } = makePage();
  const snapshot = loadAPI().snapshotFromPage(page, "manual", "2026-10-27T12:00:00.000Z");
  assert.equal(snapshot.app.computedPosition, "fixed", "#app's computed position is the out-of-flow half of the claim");
  assert.equal(snapshot.document.htmlOverflow, "clip", "html must report the computed overflow it actually resolves to");
  assert.equal(snapshot.document.bodyOverflow, "clip", "and body the same — the floor is on both root boxes");
  assert.ok(
    snapshot.document.rootScrollHeight <= snapshot.clientHeight,
    "no scroll range: rootScrollHeight must not exceed clientHeight",
  );
  // The instrument measures and never repairs. The ban is on WRITES — reading
  // scrollTop IS the probe's job, so a regex over the bare word would fail on
  // the capture itself and teach nothing. Comments are stripped first because
  // the probe documents the very calls it must not make.
  const code = source.replace(/\/\/[^\n]*/g, "").replace(/\/\*[\s\S]*?\*\//g, "");
  for (const banned of [/\.scrollTop\s*=[^=]/, /\.scrollLeft\s*=[^=]/, /\bscrollTo\s*\(/, /\bscrollBy\s*\(/]) {
    assert.equal(banned.test(code), false, `the probe must never write scroll, matched ${banned}`);
  }
});

// ─── issue #116 Phase 2 task 1: ownership / focused host / session / unlock ──────
// PHASE1-CONTRACT「验收前置条件（工具依赖，不得绕过）」and D1's three missing
// field groups: ① who owns the focus, ② that element's client rect in the SAME
// frame as the terminal host's, ③ the sub-frame order of events against geometry.
// The stand-ins below therefore carry a real ancestor chain (a chain that cannot be
// walked must answer `unknown`, never a guessed HO-3) and a host identity that is
// only ever compared, never echoed.

const HOST_RECT = { top: 40, bottom: 844, left: 40, right: 820, width: 780, height: 804 };
// 聚焦目标必须落在键盘顶边（vv.height = 702）之上，才能断言"同帧可见"。
const FOCUS_RECT = { top: 640, bottom: 668, left: 120, right: 420, width: 300, height: 28 };

function chainOf(specs) {
  let parent = null;
  let leaf = null;
  const nodes = [];
  for (const spec of specs) {
    const node = {
      tagName: spec.tag || "DIV",
      id: spec.id || "",
      className: (spec.classes || []).join(" "),
      style: { display: spec.display || "block" },
      attrs: spec.attrs || {},
      parentNode: parent,
      getAttribute(name) {
        return Object.prototype.hasOwnProperty.call(this.attrs, name) ? this.attrs[name] : null;
      },
      getBoundingClientRect: () => spec.rect || { top: 0, bottom: 0, left: 0, right: 0, width: 0, height: 0 },
    };
    if (spec.isContentEditable) node.isContentEditable = true;
    if (spec.contentDocumentThrows) {
      Object.defineProperty(node, "contentDocument", { get() { throw new Error("cross-origin"); } });
    } else if (spec.contentDocument) {
      node.contentDocument = spec.contentDocument;
    }
    leaf = node;
    parent = node;
    nodes.push(node);
  }
  return { leaf, root: nodes[0] || null, nodes };
}

function ownershipReportOf(env) {
  return loadAPI().focusOwnershipOf(env.win, env.doc, env.state, env.vv);
}

function ownershipEnv(chain, activeInst = "instance-private-id") {
  return {
    win: {
      getComputedStyle: (el) => ({ display: (el && el.style && el.style.display) || "block" }),
      visualViewport: { width: 834, height: 702, offsetTop: 0, offsetLeft: 0, scale: 1 },
    },
    doc: {
      activeElement: chain.leaf,
      documentElement: chain.root,
      body: chain.nodes[1] || chain.root,
      getElementById: () => null,
    },
    state: { activeInst },
    vv: { width: 834, height: 702, offsetTop: 0, offsetLeft: 0, scale: 1 },
  };
}

const TERMINAL_CHAIN = [
  { tag: "HTML" },
  { tag: "BODY" },
  { tag: "DIV", id: "app" },
  { tag: "DIV", id: "workspace" },
  { tag: "DIV", id: "terminal-container" },
  { tag: "DIV", attrs: { "data-instance-id": "instance-private-id" }, rect: HOST_RECT },
  { tag: "DIV", classes: ["xterm", "xterm-dom-renderer-owner-1"] },
  { tag: "DIV", classes: ["xterm-screen"] },
  { tag: "TEXTAREA", classes: ["xterm-helper-textarea"], rect: FOCUS_RECT },
];

test("ownership answers HO-2 and carries the focused host's client rect in the SAME frame", () => {
  const api = loadAPI();
  const chain = chainOf(TERMINAL_CHAIN);
  const env = ownershipEnv(chain);
  const { ownership, activeInput } = api.focusOwnershipOf(env.win, env.doc, env.state, env.vv);
  assert.equal(ownership.category, "HO-2", "focus is on the foreground PTY host's xterm helper textarea");
  assert.equal(ownership.basis, "editable-focus-inside-foreground-terminal-host");
  assert.equal(ownership.unavailableReason, "", "nothing was unavailable, so nothing is excused");
  assert.equal(activeInput.tag, "TEXTAREA");
  assert.equal(activeInput.role, "xterm-helper-textarea", "the terminal host is told apart from a web input by ROLE MARKERS, not by text");
  assert.equal(activeInput.xtermMarker, true);
  assert.equal(activeInput.helperMarker, true);
  assert.equal(activeInput.insideTerminalHost, true);
  assert.equal(activeInput.hostIsForeground, true);
  assert.deepEqual(activeInput.rect, FOCUS_RECT, "D1 ②: the focused element's client rect, in client space");
  assert.equal(activeInput.insideVisibleViewport, true, `rect ${FOCUS_RECT.bottom} must be compared against vv.height 702, never against 702 + offsetTop`);
  assert.equal(activeInput.rectUnavailableReason, "");
  // Identity never leaves as a value: the host's id and the focused element's
  // content are compared and dropped.
  chain.nodes[5].attrs["data-instance-id"] = "instance-private-id";
  chain.leaf.value = "must-not-leak";
  const serialized = JSON.stringify({ ownership, activeInput });
  assert.equal(serialized.includes("instance-private-id"), false, "an instance id must never be reported");
  assert.equal(serialized.includes("must-not-leak"), false, "input content must never be read, let alone reported");
});

test("ownership splits HO-1 / HO-3 by focus landing, exactly as contract B章", () => {
  const api = loadAPI();
  // A hidden host has no right to ownership (核心原则 §5 末段), even when it holds
  // the caret — HO-1, never HO-2 and never HO-3.
  const hiddenSpecs = TERMINAL_CHAIN.map((spec, index) => (index === 5 ? { ...spec, display: "none" } : spec));
  const hidden = ownershipReportOf(ownershipEnv(chainOf(hiddenSpecs)));
  assert.equal(hidden.ownership.category, "HO-1");
  assert.equal(hidden.ownership.basis, "editable-focus-inside-hidden-or-inactive-terminal-host");

  // Same host, a DIFFERENT foreground instance: inactive ⇒ HO-1 as well.
  const inactive = ownershipReportOf(ownershipEnv(chainOf(TERMINAL_CHAIN), "some-other-instance"));
  assert.equal(inactive.ownership.category, "HO-1");
  assert.equal(inactive.ownership.basis, "editable-focus-inside-hidden-or-inactive-terminal-host");
  assert.equal(inactive.activeInput.hostIsForeground, false);

  // 本页普通输入：终端在背景也不改判（核心原则 §3 末段）。
  const web = chainOf([
    { tag: "HTML" },
    { tag: "BODY" },
    { tag: "DIV", id: "modal" },
    { tag: "TEXTAREA", rect: { top: 100, bottom: 160, left: 20, right: 320, width: 300, height: 60 } },
  ]);
  const webReport = ownershipReportOf(ownershipEnv(web));
  assert.equal(webReport.ownership.category, "HO-3");
  assert.equal(webReport.ownership.basis, "editable-focus-on-page-web-input");
  assert.equal(webReport.activeInput.role, "web-textarea");
  assert.equal(webReport.activeInput.insideTerminalHost, false, "a walked chain that never met a host says so positively");

  const editable = chainOf([
    { tag: "HTML" }, { tag: "BODY" },
    { tag: "DIV", classes: ["editor"], isContentEditable: true, rect: { top: 10, bottom: 40, left: 10, right: 40, width: 30, height: 30 } },
  ]);
  assert.equal(ownershipReportOf(ownershipEnv(editable)).activeInput.role, "web-contenteditable");

  // A button is not an editable host (B.5: quick-keys 入口按钮 → HO-1).
  const button = chainOf([{ tag: "HTML" }, { tag: "BODY" }, { tag: "BUTTON" }]);
  const buttonReport = ownershipReportOf(ownershipEnv(button));
  assert.equal(buttonReport.ownership.category, "HO-1");
  assert.equal(buttonReport.ownership.basis, "focus-on-non-editable-element");
  assert.equal(buttonReport.activeInput.editable, false);
  assert.equal(buttonReport.activeInput.role, "non-editable");

  // Focus left the document entirely.
  const noFocus = ownershipEnv(chainOf([{ tag: "HTML" }, { tag: "BODY" }]));
  noFocus.doc.activeElement = null;
  const noFocusReport = api.focusOwnershipOf(noFocus.win, noFocus.doc, noFocus.state, noFocus.vv);
  assert.equal(noFocusReport.ownership.category, "HO-1");
  assert.equal(noFocusReport.ownership.basis, "no-focused-element");
});

test("ownership answers `unknown` with a reason instead of guessing", () => {
  const api = loadAPI();
  // 1. The chain cannot be walked to the root: outside-the-terminal is NOT proven.
  const detached = chainOf([
    { tag: "HTML" }, { tag: "BODY" }, { tag: "DIV" }, { tag: "TEXTAREA" },
  ]);
  detached.leaf.parentNode = undefined;   // a detached node: the walk stops mid-way
  const truncated = ownershipReportOf(ownershipEnv(detached));
  assert.equal(truncated.ownership.category, "unknown");
  assert.equal(truncated.ownership.unavailableReason, "focus-ancestor-chain-truncated");
  assert.equal(truncated.activeInput.ancestryReachedRoot, false);
  assert.equal(truncated.activeInput.insideTerminalHost, null, "unknown, not false");

  // 2. A cross-origin iframe (the normal iPad case for opencode/reasonix/dsh):
  //    the parent may not inspect inside, and says so (R3 / HO-4).
  const crossOrigin = chainOf([
    { tag: "HTML" }, { tag: "BODY" },
    { tag: "IFRAME", id: "opencode-frame", contentDocumentThrows: true, rect: HOST_RECT },
  ]);
  const cross = ownershipReportOf(ownershipEnv(crossOrigin));
  assert.equal(cross.ownership.category, "unknown");
  assert.equal(cross.ownership.unavailableReason, "iframe-inner-focus-not-inspectable");
  assert.equal(cross.activeInput.role, "iframe", "the FACT is still reported: focus sits on an iframe");

  // 3. Same-origin, editable inside ⇒ HO-4; not editable ⇒ HO-1 (B章消歧补充).
  const sameOriginEditable = chainOf([
    { tag: "HTML" }, { tag: "BODY" },
    { tag: "IFRAME", contentDocument: { activeElement: { tagName: "INPUT" } } },
  ]);
  assert.equal(ownershipReportOf(ownershipEnv(sameOriginEditable)).ownership.category, "HO-4");
  const sameOriginStatic = chainOf([
    { tag: "HTML" }, { tag: "BODY" },
    { tag: "IFRAME", contentDocument: { activeElement: { tagName: "BODY" } } },
  ]);
  assert.equal(ownershipReportOf(ownershipEnv(sameOriginStatic)).ownership.category, "HO-1");

  // 4. No activeElement to read at all, and no foreground answer for a host.
  const blind = ownershipEnv(chainOf(TERMINAL_CHAIN));
  delete blind.doc.activeElement;
  assert.equal(api.focusOwnershipOf(blind.win, blind.doc, blind.state, blind.vv).ownership.unavailableReason, "document-active-element-unavailable");
  const noForeground = ownershipReportOf(ownershipEnv(chainOf(TERMINAL_CHAIN), ""));
  assert.equal(noForeground.ownership.category, "unknown");
  assert.equal(noForeground.ownership.unavailableReason, "foreground-instance-not-determinable");

  // 5. A focused element without a rect reports the miss, not a fabricated 0×0.
  const noRect = ownershipEnv(chainOf([
    { tag: "HTML" }, { tag: "BODY" },
    { tag: "TEXTAREA" },
  ]));
  noRect.doc.activeElement.getBoundingClientRect = undefined;
  const rectless = api.focusOwnershipOf(noRect.win, noRect.doc, noRect.state, noRect.vv);
  assert.equal(rectless.activeInput.rect, null);
  assert.equal(rectless.activeInput.rectUnavailableReason, "focus-client-rect-unavailable");
  assert.equal(rectless.activeInput.insideVisibleViewport, null);
});

test("keyboardSession is a DERIVED hint: named as derived, and silent where it cannot know (B-3)", () => {
  const api = loadAPI();
  const base = {
    mirrorExposed: true, mirrorOcclusion: 320, rawShortfall: 320, layoutHeight: 1108,
    vvHeight: 788, vvWidth: 834, vvOffsetTop: 0, scale: 1, zoomed: false,
    vvObserved: true, focusEditable: true,
  };
  // B-3: every answer this function gives is DERIVED and SAYS SO. The authority
  // for a session stays the frozen judge (evidence/verify-sop.mjs
  // makeKeyboardSessionJudge) plus the measurement fields; renaming `state` to
  // `derivedState` + `derived:true` is what keeps a hint from being read as the
  // acceptance state it can never be.
  assert.equal(api.keyboardSessionOf(base, null).derived, true);
  assert.equal(api.keyboardSessionOf(base, null).state, undefined,
    "the old authoritative name is gone, not kept alongside as a second truth");
  assert.equal(api.keyboardSessionOf(base, null).derivedState, "active");
  assert.equal(api.keyboardSessionOf(base, null).basis, "applier-mirror-positive");
  // geometryGrace: §5.7 grace is `active && geometryChanged` — so the PREVIOUS
  // frame must have owed a raw too. Geometry changing into a frame that never was
  // active is not grace, and the label says what it cannot know instead.
  const shifted = { ...base, layoutHeight: 1180 };
  assert.equal(api.keyboardSessionOf(shifted, base).derivedState, "geometryGrace");
  assert.equal(api.keyboardSessionOf(shifted, base).previousMirrorOcclusion, 320,
    "the frame the grace was inferred across travels with the label");
  // The over-claim this review named: geometry moving next to a frame that never
  // owed a raw is NOT grace. `active` stays the mirror's direct read; grace needs
  // an ACTIVE frame before the move, and without one the label is unknown + reason.
  const zeroBefore = { ...base, mirrorOcclusion: 0 };
  assert.equal(api.keyboardSessionOf(shifted, zeroBefore, {}).derivedState, "unknown",
    "grace needs an active frame before the geometry moved (§5.7)");
  assert.equal(api.keyboardSessionOf(shifted, zeroBefore, {}).unavailableReason, "never-active-before-geometry-change");
  assert.equal(api.keyboardSessionOf(shifted, null, {}).derivedState, "active",
    "no previous frame at all: the mirror's own positive read is still reported, and no grace is claimed");
  assert.equal(api.keyboardSessionOf(shifted, null, {}).geometryChangedSincePrevious, null,
    "nothing to compare against, and the field says so instead of answering false");
  const keyboardOnlyChange = { ...base, vvHeight: 702, rawShortfall: 406 };
  assert.equal(api.keyboardSessionOf(keyboardOnlyChange, base).derivedState, "active",
    "a vv.height-only change is the keyboard, not new geometry (§5.5)");
  // recovering: owed a raw last frame, owes nothing now, and NO unlock landed
  // between the two frames.
  assert.equal(api.keyboardSessionOf({ ...base, mirrorOcclusion: 0 }, base, {}).derivedState, "recovering");
  assert.equal(api.keyboardSessionOf({ ...base, mirrorOcclusion: 0 }, base, {}).previousMirrorOcclusion, 320);
  assert.equal(api.keyboardSessionOf({ ...base, mirrorOcclusion: 0 }, base, {}).unavailableReason, "recovery-latch-not-published-by-applier",
    "the latch itself stays unobservable, and the label says so while the §5.5 unlock trace records the clicks");
  // The review's third over-claim: a click ALREADY unlocked (§5.5) inside the span
  // — `recovering` is then not derivable at all, and the two ticks are the proof.
  const unlocked = api.keyboardSessionOf({ ...base, mirrorOcclusion: 0 }, base, { tick: 7, previousTick: 3 });
  assert.equal(api.keyboardSessionOf({ ...base, mirrorOcclusion: 0 }, base, { tick: 1, previousTick: null }).unavailableReason,
    "unlocked-after-previous-frame-latch-state-unpublished",
    "a previous frame that journalled no unlock at all still counts as before the unlock");
  assert.equal(unlocked.derivedState, "unknown");
  assert.equal(unlocked.unavailableReason, "unlocked-after-previous-frame-latch-state-unpublished");
  assert.equal(unlocked.unlockTick, 7);
  assert.equal(unlocked.previousUnlockTick, 3);
  // never-entered vs latched recovery is genuinely indistinguishable ⇒ unknown.
  const zero = { ...base, mirrorOcclusion: 0 };
  assert.equal(api.keyboardSessionOf(zero, zero, {}).derivedState, "unknown");
  assert.equal(api.keyboardSessionOf(zero, zero, {}).unavailableReason, "never-entered-or-latched-recovery-indistinguishable");
  // none: the exit conditions, each named.
  assert.equal(api.keyboardSessionOf({ ...zero, rawShortfall: 0 }, zero, {}).derivedState, "none");
  assert.equal(api.keyboardSessionOf({ ...zero, rawShortfall: 0 }, zero, {}).basis, "no-keyboard-shortfall");
  assert.equal(api.keyboardSessionOf({ ...zero, focusEditable: false }, zero, {}).basis, "no-editable-focus");
  // Zoom is a session exit by §D2, and it is checked BEFORE recovering: the frame
  // may not call itself recovering while its own rule says zoom is exempt.
  assert.equal(api.keyboardSessionOf({ ...zero, zoomed: true }, base, {}).derivedState, "none");
  assert.equal(api.keyboardSessionOf({ ...zero, zoomed: true }, base, {}).basis, "zoomed-session-exempt");
  assert.equal(api.keyboardSessionOf({ ...base, zoomed: true, mirrorOcclusion: 0 }, base, {}).basis, "zoomed-session-exempt",
    "zoom leaving a live session is exempt (§D2), not a recovery claim");
  // no mirror ⇒ the applier never spoke: unknown, never "none".
  const mute = api.keyboardSessionOf({ ...base, mirrorExposed: false, mirrorOcclusion: 0 }, null, {});
  assert.equal(mute.derivedState, "unknown");
  assert.equal(mute.unavailableReason, "applier-session-mirror-unset");
  // no visualViewport ⇒ the keyboard edge is unmeasurable.
  assert.equal(api.keyboardSessionOf({ ...zero, vvObserved: false }, zero, {}).unavailableReason, "no-visualViewport");
  // An `unknown` that does not explain itself is the one shape this function must
  // never produce; the server enforces the same rule at the door.
  for (const [obs, prev, span] of [[base, null, {}], [zero, zero, {}], [shifted, null, {}]]) {
    const marker = api.keyboardSessionOf(obs, prev, span);
    if (marker.derivedState === "unknown") {
      assert.ok(marker.unavailableReason, "unknown must state why it is undecidable");
    }
  }

  // The observation itself: raw is a MEASUREMENT and the mirror stays the verdict.
  const observation = api.keyboardObservationOf(
    { visualViewport: { height: 702, width: 834, offsetTop: 403, scale: 1 }, __mwTerminalOcclusion: 406 },
    { documentElement: { clientHeight: 1108 }, activeElement: { tagName: "TEXTAREA" } },
  );
  assert.equal(observation.rawShortfall, 406, "clientHeight − vv.height, client space only");
  assert.equal(observation.mirrorOcclusion, 406);
  assert.equal(observation.vvOffsetTop, 403, "the pan FACT is recorded, and it is never added to raw");
  assert.equal(observation.focusEditable, true);
  assert.equal(api.keyboardObservationOf({}, { documentElement: { clientHeight: 1108 } }).rawShortfall, 0,
    "no visualViewport means nothing is covered, not a full-height shortfall");
});

test("the unlock trace journals click and touchstart apart, records and judges nothing", () => {
  const api = loadAPI();
  const { leaf } = chainOf(TERMINAL_CHAIN);
  const { win } = ownershipEnv(chainOf(TERMINAL_CHAIN));
  // The stand-in document carries a documentElement because N-3 asks the journal to
  // record the LAYOUT side of the instant too, not only the visual viewport's.
  const traceDoc = { getElementById: () => null, documentElement: { clientHeight: 1108 } };
  const trace = api.createEventTrace(win, traceDoc);
  trace.note("click", { target: leaf });
  trace.note("touchstart", { target: leaf });
  trace.note("focusin", { target: leaf });
  const report = trace.report();
  assert.equal(report.advisoryOnly, true, "record only: nothing here may feed a judgment");
  assert.equal(report.geometryCountedOnly, true, "M-6: the ring holds decision events, geometry lives in counts");
  assert.equal(report.counts.click, 1);
  assert.equal(report.counts.clickOnTerminalHost, 1, "§5.5 unlocks on click, so the host attribution is the whole point");
  assert.equal(report.counts.touchstart, 1, "touchstart is kept distinct: it never unlocks the latch");
  assert.equal(report.counts.focusin, 1);
  assert.equal(report.captureTick, 3);
  assert.deepEqual(report.entries.map((entry) => entry.type), ["click", "touchstart", "focusin"]);
  assert.equal(report.entries[0].onTerminalHost, true);
  assert.equal(report.entries[0].vvHeight, 702, "geometry at the instant the handler ran — that is D1 ③");
  assert.equal(report.entries[0].tag, "TEXTAREA");
  assert.equal(JSON.stringify(report).includes("instance-private-id"), false, "the journal reports booleans, never ids");
  // N-3: the horizontal / layout sides of the SAME instant, so §5.7's
  // layoutHeight-vs-vvWidth comparison closes inside the journal.
  assert.equal(report.entries[0].vvWidth, 834);
  assert.equal(report.entries[0].scale, 1);
  assert.equal(report.entries[0].layoutHeight, 1108, "the layout side of the same instant (N-3)");

  // M-6: a CONTINUOUS PAN floods the geometry events. They keep their exact counts
  // and never enter the ring, so the click §5.5 asks for is still in the entries
  // after the flood — the old shared ring reported its own blindness as
  // "no click happened".
  for (let i = 0; i < 40; i++) trace.note("scroll", { target: leaf });
  for (let i = 0; i < 40; i++) trace.note("resize", { target: leaf });
  const after = trace.report();
  assert.equal(after.counts.vvScroll, 40, "counts keep counting while the ring stays put");
  assert.equal(after.counts.vvResize, 40);
  assert.deepEqual(after.entries.map((entry) => entry.type), ["click", "touchstart", "focusin"],
    "the pan flood evicted nothing the verdict reads");

  // B-3 plumbing: the journal remembers WHEN the last unlock landed, so the
  // derived session label can state that a recovering frame is not derivable.
  assert.equal(trace.unlockTick(), 3, "the unlock tick is the LAST click / focusin");
  assert.ok(trace.unlockTick() < trace.report().captureTick,
    "the pan flood after it never moved the unlock tick — geometry events are not unlocks");
  const fresh = api.createEventTrace(win, traceDoc);
  assert.equal(fresh.unlockTick(), null, "an unlock that never happened is null, not 0");
  fresh.note("resize", { target: leaf });
  assert.equal(fresh.unlockTick(), null);
  fresh.note("click", { target: leaf });
  assert.equal(fresh.unlockTick(), 2);

  // N-1: the WINDOW's resize is its own count. Both fire `resize` at the DOM level,
  // and reading one as the other invented keyboard-shaped signals.
  const split = api.createEventTrace(win, traceDoc);
  split.note("resize", { target: leaf });
  split.note("window-resize", { target: leaf });
  split.note("window-resize", { target: leaf });
  assert.equal(split.report().counts.vvResize, 1, "only the visualViewport's resize counts as vvResize");
  assert.equal(split.report().counts.windowResize, 2);
  assert.equal(split.report().counts.other, 0, "and it is not smuggled into `other` either");
  assert.equal(split.report().entries.length, 0, "window resize is not a decision event either");

  // The ring stays bounded even when the decision events themselves flood.
  for (let i = 0; i < 40; i++) split.note("click", { target: leaf });
  assert.equal(split.report().entries.length, 16, "the journal is bounded: 64KiB uploads stay small");
  assert.equal(split.report().counts.click, 40, "the count survives the roll");
});

test("a client frame carries ownership, the DERIVED session hint and the panel itself", async () => {
  const h = makeHarness();
  h.client.start();
  await flushPromises();
  const first = h.uploads[0].snapshot;
  assert.equal(first.probeVersion, "p9-full-shell-session-avoidance-v1", "the frozen stamp is untouched by the additive schema");
  assert.equal(first.schemaRev, "p9-additive-ownership-panel-v1");
  assert.equal(first.keyboardSession.derived, true, "B-3: the frame labels its session hint as derived");
  assert.equal(first.keyboardSession.derivedState, "active", "the fixture's applier mirror owes 320");
  assert.equal(first.keyboardSession.state, undefined, "no authoritative `state` field ships any more");
  assert.equal(first.ownership.category, "unknown", "the stand-in focus has no walkable chain, and that is reported, not guessed");
  assert.equal(first.ownership.unavailableReason, "focus-ancestor-chain-truncated");
  assert.equal(first.panel.exists, false);
  assert.equal(first.panel.corner, "unavailable");
  assert.equal(first.panel.support, "unavailable");
  assert.equal(first.panel.unsupportedReason, "panel-absent");

  // B-3 case 1 — the span that DOES derive `recovering`: two frames, mirror
  // 320 → 0, and NO unlock in between. The label names the latch it cannot see.
  h.page.window.__mwTerminalOcclusion = 0;   // the applier exited / latched
  h.client.capture("manual");
  await flushPromises();
  const second = h.uploads[1].snapshot;
  assert.equal(second.keyboardSession.derivedState, "recovering");
  assert.equal(second.keyboardSession.previousMirrorOcclusion, 320);
  assert.equal(second.keyboardSession.unavailableReason, "recovery-latch-not-published-by-applier");
  assert.ok(h.uploads[1].seq > h.uploads[0].seq, "frames are ordered by seq");
  assert.ok(second.unlockTrace.captureTick >= first.unlockTrace.captureTick, "and by the journal tick");

  // B-3 case 2 — the over-claim this review named: a §5.5 unlock click was
  // journalled INSIDE the span, so the latch may already be open. The journal
  // still records it; the label stops claiming `recovering` and says unknown +
  // the two ticks that prove why.
  const h2 = makeHarness();
  h2.client.start();
  await flushPromises();
  const live = h2.uploads[0].snapshot;
  assert.equal(live.keyboardSession.derivedState, "active");
  h2.dispatch("click", chainOf(TERMINAL_CHAIN).leaf);   // the §5.5 unlock
  h2.page.window.__mwTerminalOcclusion = 0;
  h2.client.capture("manual");
  await flushPromises();
  const unlocked = h2.uploads[h2.uploads.length - 1].snapshot;
  assert.ok(unlocked.unlockTrace.counts.click >= 1, "the §5.5 unlock click is admissible evidence now");
  assert.ok(unlocked.unlockTrace.captureTick > live.unlockTrace.captureTick, "sub-frame ordering is derivable from ticks");
  assert.equal(unlocked.keyboardSession.derivedState, "unknown",
    "an unlock landed between the frames: recovering is NOT derivable (B-3)");
  assert.equal(unlocked.keyboardSession.unavailableReason, "unlocked-after-previous-frame-latch-state-unpublished");
  assert.equal(unlocked.keyboardSession.previousMirrorOcclusion, 320);

  // M-7: an advisory journal that THROWS must never suppress the measurement. The
  // click target below refuses the attribute read the journal walks, so note() dies
  // inside the handler — and the click still has to schedule the capture of the
  // very geometry the incident happened next to.
  const h3 = makeHarness();
  h3.client.start();
  await flushPromises();
  const beforeHostile = h3.uploads.length;
  const hostile = chainOf(TERMINAL_CHAIN).leaf;
  hostile.getAttribute = () => { throw new Error("hostile attribute read"); };
  // `focusin` is the event that BOTH journals and schedules (a §5.5 unlock click
  // journals without scheduling, by design — see observeOnly), so it is the one
  // that proves the order: note() may die, schedule() must not.
  h3.dispatch("focusin", hostile);
  h3.flushFrames();
  await flushPromises();
  assert.ok(h3.uploads.length > beforeHostile,
    "focusin still scheduled a capture even though the journal threw (M-7)");
  const hostileFrame = h3.uploads[h3.uploads.length - 1].snapshot;
  assert.equal(hostileFrame.probeReadFailed, undefined,
    "the failed journal write is not a failed frame: the geometry is still measured");
  assert.equal(hostileFrame.vv.height, VV_HEIGHT);
  // m-2: when the MEASUREMENT itself dies, the frame still goes out — marked, with
  // every number absent instead of zero-filled — and nothing escapes uncaught.
  const h4 = makeHarness();
  h4.client.start();
  await flushPromises();
  h4.page.window.getComputedStyle = () => { throw new Error("computed style exploded"); };
  const frame = h4.client.capture("manual");
  await flushPromises();
  assert.equal(frame.snapshot.probeReadFailed, true);
  assert.equal(frame.snapshot.probeReadFailedReason, "probe-read-failed");
  assert.equal(frame.snapshot.vv, null, "no zero-filled stand-in for an unread viewport");
  assert.equal(frame.snapshot.ptyHosts, null, "and none for the host list either");
  assert.equal(frame.snapshot.reason, "manual", "the reason stays the whitelisted reason of the attempt");
  assert.equal(frame.snapshot.schemaRev, "p9-additive-ownership-panel-v1", "the frame still identifies its schema");
  assert.ok(frame.seq > 0, "a failed read still consumes a seq, so the gap a reader sees is honest");

  // A session that was never live never claims to be recovering.
  const cold = makeHarness();
  cold.page.window.__mwTerminalOcclusion = 0;
  cold.client.start();
  await flushPromises();
  assert.equal(cold.uploads[0].snapshot.keyboardSession.derivedState, "unknown");
  assert.equal(cold.uploads[0].snapshot.keyboardSession.unavailableReason, "never-entered-or-latched-recovery-indistinguishable");

  // No applier mirror at all ⇒ unknown, never "none".
  const mute = makeHarness();
  delete mute.page.window.__mwTerminalOcclusion;
  mute.client.start();
  await flushPromises();
  assert.equal(mute.uploads[0].snapshot.keyboardSession.derivedState, "unknown");
  assert.equal(mute.uploads[0].snapshot.keyboardSession.unavailableReason, "applier-session-mirror-unset");
  assert.equal(mute.uploads[0].snapshot.occlusion, 0, "the frozen occlusion field keeps its 'unset reads 0' meaning");

  // The panel reports itself once it is on the page.
  // 夹具修正（AUTH-20261105-DIAG-PANEL-CLIENT 方案②第 3 条）：原 rect right=826 对这个
  // harness 的 vv.width=820 本来就越出可见窗口 6px——旧纵向-only 口径没查横向，才把它
  // 报成 insideVisibleViewport=true。本用例的意图是"完整可见的 br 停靠位"，所以把夹具
  // 修成窗内自洽值（br 右 inset 8 → client 右缘 812），原越界值挪给下面的横向反证格。
  const { page } = makePage();
  const panel = {
    dataset: {
      debugCorner: "br", debugSupport: "supported", debugUnsupportedReason: "",
      debugLifted: "1", debugPanCorrected: "0", debugNominalHeight: "520",
    },
    getBoundingClientRect: () => ({ top: 154, bottom: 674, left: 452, right: 812, width: 360, height: 520 }),
  };
  const original = page.document.getElementById;
  page.document.getElementById = (id) => (id === "viewport-debug-panel" ? panel : original(id));
  const withPanel = loadAPI().snapshotFromPage(page, "manual", "2026-11-04T12:00:00.000Z");
  assert.equal(withPanel.panel.exists, true);
  assert.equal(withPanel.panel.corner, "br", "the panel states its own corner (GI-9 self-report)");
  assert.equal(withPanel.panel.support, "supported");
  assert.equal(withPanel.panel.lifted, true);
  assert.equal(withPanel.panel.nominalHeight, 520);
  assert.deepEqual(withPanel.panel.rect, { top: 154, bottom: 674, left: 452, right: 812, width: 360, height: 520 });
  assert.equal(withPanel.panel.keyboardOverlap, 0, `bottom 674 against vv.height ${VV_HEIGHT}`);
  assert.equal(withPanel.panel.insideVisibleViewport, true);
  // A panel under the keyboard says so instead of being quietly unreachable.
  // 纵向越界格：横向保持窗内（452..812），让 insideVisibleViewport=false 归因纯纵向。
  panel.getBoundingClientRect = () => ({ top: 900, bottom: 1012, left: 452, right: 812, width: 360, height: 112 });
  const covered = loadAPI().snapshotFromPage(page, "manual", "2026-11-04T12:00:00.000Z");
  assert.equal(covered.panel.keyboardOverlap, 152, "1012 − 860: the covered part of the panel, as a number");
  assert.equal(covered.panel.insideVisibleViewport, false);
  // 横向-only 越界反证（方案②补的横向界真正生效的证明）：纵横都在 [0, vv.height] 内、
  // 只有右缘越出 [0, vv.width=820] 一格——旧纵向-only 口径会放行报 true，必须如实 false。
  // 这就是 seq253 形状的反方向镜像：横向盲区不再存在。
  panel.getBoundingClientRect = () => ({ top: 154, bottom: 674, left: 466, right: 826, width: 360, height: 520 });
  const sideways = loadAPI().snapshotFromPage(page, "manual", "2026-11-04T12:00:00.000Z");
  assert.equal(sideways.panel.keyboardOverlap, 0, "这一格纵向完全在窗内，失败只能归因横向");
  assert.equal(sideways.panel.insideVisibleViewport, false, "横向越出 [0, vv.width=820] 不得再算可见（方案②快照横向界）");

  // B-2: a panel that EXISTS but whose dataset was never written answers
  // `unavailable` + a reason — from the ONE vocabulary the server whitelists. A
  // fourth word there was a 400 on every upload, which is how a capture round was
  // lost; the shape is pinned on both sides (see viewport_debug_test.go).
  panel.dataset = {};
  const unwritten = loadAPI().snapshotFromPage(page, "manual", "2026-11-04T12:00:00.000Z");
  assert.equal(unwritten.panel.exists, true);
  assert.equal(unwritten.panel.corner, "unknown");
  assert.equal(unwritten.panel.support, "unavailable");
  assert.equal(unwritten.panel.unsupportedReason, "panel-layout-not-authored");
  assert.ok(["supported", "unsupported", "unavailable"].includes(unwritten.panel.support),
    "the client's vocabulary is the server's whitelist, no fourth value");
  // An UNSUPPORTED corner keeps its reason too (R6: never silently unreachable).
  panel.dataset = { debugCorner: "br", debugSupport: "unsupported", debugUnsupportedReason: "visible-window-too-short-for-panel" };
  const unsupported = loadAPI().snapshotFromPage(page, "manual", "2026-11-04T12:00:00.000Z");
  assert.equal(unsupported.panel.support, "unsupported");
  assert.equal(unsupported.panel.unsupportedReason, "visible-window-too-short-for-panel");
});

test("stop() 按 type|capture 三元组摘除监听器，start/stop 循环不重复注册（B-1）", async () => {
  const h = makeHarness();
  const liveKeys = () => [...h.docListeners.keys()];
  h.client.start();
  await flushPromises();
  // 注册侧的口径先钉住：§5.5 的 click / touchstart 是 capture，其余是 bubble。
  assert.deepEqual((h.docListeners.get("click|capture") || []).length, 1, "click 以 capture 注册一次");
  assert.deepEqual((h.docListeners.get("touchstart|capture") || []).length, 1, "touchstart 以 capture 注册一次");
  assert.deepEqual((h.docListeners.get("focusin|bubble") || []).length, 1);
  assert.equal(h.docListeners.get("click|bubble"), undefined,
    "click 不曾以 bubble 注册——摘除时若忘了 capture 标志，就对不上这个 key");
  assert.ok(liveKeys().length > 0);

  h.client.stop();
  for (const [key, list] of h.docListeners) {
    assert.deepEqual(list, [], `stop() 后 ${key} 上还有 ${list.length} 个监听器（capture 标志没原样回传）`);
  }
  for (const [key, list] of h.listeners) {
    assert.deepEqual(list, [], `stop() 后 visualViewport 的 ${key} 上还有监听器`);
  }

  // 再来一轮：残留的监听器会在这一轮被重复注册，计数必须仍然是每 key 一个。
  h.client.start();
  await flushPromises();
  for (const key of ["click|capture", "touchstart|capture", "focusin|bubble"]) {
    assert.equal((h.docListeners.get(key) || []).length, 1, `第二轮 ${key} 必须恰好一个，多出来的就是上一轮的残留`);
  }
  h.client.stop();
  for (const [key, list] of h.docListeners) {
    assert.deepEqual(list, [], `两轮 start/stop 之后 ${key} 必须全空`);
  }
  // 摘干净之后，事件不再触发任何采集。
  const before = h.uploads.length;
  assert.throws(() => h.dispatch("focusin", { tagName: "TEXTAREA" }), /no document listener/,
    "stop 之后文档上不再有监听器可派发");
  assert.equal(h.uploads.length, before, "stop 后不再产生帧");
});

test("落位不支持必须渲染到面板可见状态行，不能只进上传数据（M-1）", () => {
  const api = loadAPI();
  const placement = { textContent: "", style: {} };
  const panel = {
    style: {}, dataset: {},
    querySelector: (sel) => (sel === "#viewport-debug-placement" ? placement : null),
  };
  const win = {
    innerWidth: 834, innerHeight: 1108,
    visualViewport: { width: 834, height: 70, offsetTop: 0, offsetLeft: 0, scale: 1 },
    document: { documentElement: { clientWidth: 834, clientHeight: 1108 }, getElementById: () => null },
    getComputedStyle: () => ({ display: "none" }),
  };
  api.applyPanelLayout(win, panel, { corner: "br", safeTop: 24, safeRight: 0, safeBottom: 20, collapsed: true });
  assert.equal(panel.dataset.debugSupport, "unsupported", "先确认这一格真的判成不支持");
  assert.equal(placement.style.display, "block", "不支持时状态行必须可见，不能藏在 dataset 里");
  assert.ok(placement.textContent.includes("corner br unsupported"), `状态行必须写明居位与结论，实得 ${placement.textContent}`);
  assert.ok(placement.textContent.includes("visible-window-too-short-for-panel"), `状态行必须带原因，实得 ${placement.textContent}`);
  assert.equal(placement.style.fontWeight, "700", "不支持必须加粗醒目，灰字回显会被当成正常状态");
  assert.equal(placement.style.color, "#f0b429", "不支持必须是琥珀色，不是回显用的灰字");

  // 回到放得下的一格：行仍然留在屏幕上，但必须退回灰字常规——琥珀 / 加粗不得残留。
  win.visualViewport.height = 900;
  api.applyPanelLayout(win, panel, { corner: "br", safeTop: 24, safeRight: 0, safeBottom: 20, collapsed: false });
  assert.equal(panel.dataset.debugSupport, "supported");
  assert.equal(placement.style.display, "block", "supported 时状态行同样亮着，灰字回显当前居位");
  assert.equal(placement.style.color, "#9aa7b4", "supported 必须退回灰字，上一格的琥珀警告不得残留");
  assert.equal(placement.style.fontWeight, "400", "supported 不得加粗，否则正常回显看起来像警告");
  assert.ok(placement.textContent.includes("corner br supported"), "回显当前居位，测试者知道面板自认为在哪");

  // 面板没有那一行（旧的 / 别的宿主页面）时不得抛异常——诊断在产品页上抛异常
  // 就等于把被测量的页面弄成不可测。
  assert.doesNotThrow(() => api.applyPanelLayout(win, { style: {}, dataset: {} }, { corner: "br" }));
});

test("applyPanelLayout 对缺省居位归一到 tr，不抛 TypeError（N-5）", () => {
  const api = loadAPI();
  const panel = { style: {}, dataset: {} };
  const win = {
    innerWidth: 834, innerHeight: 1108,
    visualViewport: { width: 834, height: 702, offsetTop: 0, offsetLeft: 0, scale: 1 },
    document: { documentElement: { clientWidth: 834, clientHeight: 1108 }, getElementById: () => null },
    getComputedStyle: () => ({ display: "block" }),
  };
  assert.doesNotThrow(() => api.applyPanelLayout(win, panel, {}));
  assert.equal(panel.dataset.debugCorner, "tr", "缺省居位归一到 tr 并自报出来");
  assert.equal(panel.style.top, "8px", "safe-area 缺省按 0 计，落点 = 0 + PANEL_MARGIN");
  assert.doesNotThrow(() => api.applyPanelLayout(win, panel, { corner: "" }));
  assert.equal(panel.dataset.debugCorner, "tr");
  assert.doesNotThrow(() => api.applyPanelLayout(win, panel, undefined || {}));
});

test("双轴同不足时的原因有确定优先级（W-5 / m-1）", () => {
  const api = loadAPI();
  const both = api.panelLayoutFor({
    innerWidth: 150, innerHeight: 702, layoutWidth: 150, layoutHeight: 702,
    vvWidth: 150, vvHeight: 70, corner: "br", safeBottom: 20,
  });
  assert.equal(both.support, "unsupported");
  // 取舍（写在实现注释里，这里钉住它）：纵向不足 = 主按钮排被埋，键盘采证直接
  // 不可行，是本轮真正的不可达；横向窄往往还能靠换角 / 折叠救回来。两轴同不足
  // 时保留先判定的纵向原因，横向不得把方向指错。
  assert.equal(both.unsupportedReason, "visible-window-too-short-for-panel");
  // 只窄不矮时才是横向原因。
  const narrowOnly = api.panelLayoutFor({
    innerWidth: 150, innerHeight: 702, layoutWidth: 150, layoutHeight: 702,
    vvWidth: 150, vvHeight: 702, corner: "tr",
  });
  assert.equal(narrowOnly.unsupportedReason, "visible-window-too-narrow-for-panel");
  // 落位越界的成因排最后：它是"写不出去"，不是"这一格本该存在却放不下"。
  const outFirst = api.panelLayoutFor({
    innerWidth: MATRIX_WIDTH, innerHeight: MATRIX_LAYOUT_HEIGHT,
    layoutWidth: MATRIX_WIDTH, layoutHeight: MATRIX_LAYOUT_HEIGHT,
    vvWidth: MATRIX_WIDTH, vvHeight: 680, vvOffsetTop: -400, corner: "tr", safeBottom: 20,
  });
  assert.equal(outFirst.support, "unsupported");
  assert.equal(outFirst.unsupportedReason, "visible-window-outside-layout-viewport");
});

test("m-3：嵌套宿主里最内层的宿主说了算", () => {
  const api = loadAPI();
  const marker = "data-instance-id";
  function node(props) {
    return {
      tagName: props.tag || "DIV",
      id: props.id || "",
      className: props.className || "",
      style: props.style || {},
      getAttribute: (name) => (name === marker && props.hostId ? props.hostId : null),
      parentNode: null,
    };
  }
  const html = node({ tag: "HTML", id: "html" });
  const body = node({ tag: "BODY" });
  body.parentNode = html;
  const outer = node({ hostId: "outer-host", style: { display: "none" } });   // 外层：隐藏的历史栈
  outer.parentNode = body;
  const inner = node({ hostId: "inner-host", style: { display: "block" } });    // 内层：前台宿主
  inner.parentNode = outer;
  const leaf = node({ tag: "TEXTAREA" });
  leaf.parentNode = inner;
  const env = {
    win: { getComputedStyle: (el) => ({ display: (el && el.style && el.style.display) || "block" }) },
    doc: { activeElement: leaf, documentElement: html, body },
    state: { activeInst: "inner-host" },
    vv: { height: 702, width: 834, offsetTop: 0, scale: 1 },
  };
  const report = api.focusOwnershipOf(env.win, env.doc, env.state, env.vv);
  assert.equal(report.activeInput.insideTerminalHost, true);
  assert.equal(report.activeInput.hostVisible, true,
    "内层宿主是可见的：外层隐藏不得覆盖它（旧实现在这里读到外层）");
  assert.equal(report.activeInput.hostIsForeground, true,
    "前台判定也必须对着最内层宿主比，否则前台输入被判成非前台 HO-1");
  assert.equal(report.ownership.category, "HO-2",
    "前台宿主内的可编辑聚焦 = HO-2，不被外层隐藏栈降成 HO-1");
  // 反证：把内层改成隐藏，才轮到"隐藏宿主无避让义务"。
  inner.style.display = "none";
  const hiddenInner = api.focusOwnershipOf(env.win, env.doc, env.state, env.vv);
  assert.equal(hiddenInner.activeInput.hostVisible, false, "m-4：自身 display:none 时判不可见");
  assert.equal(hiddenInner.ownership.category, "HO-1");
  // 没有宿主时仍然如实回答，不受本次改动影响。
  const plain = api.focusOwnershipOf(env.win, { ...env.doc, activeElement: body }, env.state, env.vv);
  assert.equal(plain.activeInput.insideTerminalHost, false, "走到根且无人带宿主标记 = 正向在宿主之外");
});

// ─── issue #116 Phase 2 task 2: 四角 × 键盘 × pan 可达性矩阵 ────────────────────────
// R6 / GI-9 / PLAN Phase 2 任务 2. 面板是 fixed 的：它的 client rect = 书写的
// layout 位置 − vv.offsetTop，而键盘顶边在 client y = vv.height。所以
// 底部居位在键盘打开时会被埋、顶部居位在 pan 时会出屏——这两个缺陷以前无测试。
// 断言口径（与 PHASE1-CONTRACT 术语章一致）：可读 = 面板 client rect 落在
// [0, vv.height] 内（横向 [0, vv.width]），且不落在键盘覆盖区；或该居位被显式
// 标记 support="unsupported" + 原因，而"不支持"只允许出现在物理上放不下的场合。
const MATRIX_LAYOUT_HEIGHT = 1108;   // C 会话同款 layout 高
const MATRIX_WIDTH = 834;            // C 会话同款 VV 宽度
const MATRIX_PAN = 403;             // C 会话实测持续 pan（app.top = −403）
const MATRIX_CORNERS = ["tl", "tr", "bl", "br"];
const MATRIX_KEYBOARD = [
  { name: "无键盘(raw=0)", vvHeight: MATRIX_LAYOUT_HEIGHT },
  { name: "键盘打开(raw>1)", vvHeight: MATRIX_LAYOUT_HEIGHT - 428 },
  { name: "恢复锁(raw<=1)", vvHeight: MATRIX_LAYOUT_HEIGHT - 1 },
];
// M-2 ②：pan 是 Safari 自己的平移，不由键盘缺口界定。真机证据里就有
// clientHeight=vv.height=702 而 vv.offsetTop=95 的一帧（b90643be），说明
// "无键盘 + pan≠0" 是真实几何，不是自造输入。旧夹具用
// `min(pan, layoutHeight − vvHeight)` 把这一格折回 pan=0，等于矩阵从未测过它。
const MATRIX_PANS = [
  { name: "pan=0", top: 0, left: 0 },
  { name: "纵pan=403", top: MATRIX_PAN, left: 0 },
  // M-2 ③：横向 pan。fixed 盒子的 client 横向位置同样要减 offsetLeft，负 / 正
  // 两个方向都会走到 inset 的 `Math.max(0, …)` 夹紧分支，必须进矩阵。
  { name: "横pan=44", top: 0, left: 44 },
  { name: "横向overscroll=-120", top: 0, left: -120 },
];
// M-2 ④：free 居位（拖拽）与四角共享同一套夹紧，不进矩阵就等于没覆盖。
const MATRIX_DRAGS = [
  { name: "拖拽原点", x: 0, y: 0 },
  { name: "拖拽中部", x: 200, y: 300 },
  { name: "拖出右下界", x: 9999, y: 9999 },
];

function matrixCase(corner, keyboard, pan, drag) {
  const vvHeight = keyboard.vvHeight;
  const src = {
    innerWidth: MATRIX_WIDTH,
    innerHeight: MATRIX_LAYOUT_HEIGHT,
    layoutWidth: MATRIX_WIDTH,
    layoutHeight: MATRIX_LAYOUT_HEIGHT,
    vvWidth: MATRIX_WIDTH,
    vvHeight: vvHeight,
    // pan 事实原样传入，不再按键盘缺口折叠：pan 与 raw 是两个独立的轴。
    vvOffsetTop: pan ? pan.top : 0,
    vvOffsetLeft: pan ? pan.left : 0,
    corner: corner,
    safeTop: 24,
    safeRight: 0,
    safeBottom: 20,
  };
  if (drag) {
    src.dragX = drag.x;
    src.dragY = drag.y;
  }
  const layout = loadAPI().panelLayoutFor(src);
  return { layout, vvHeight, pan: pan || { name: "pan=0", top: 0, left: 0 } };
}

// 一格几何的身份 = 它**算出来的落位结果**，不是它那组输入。M-2 ① 的要害就在这里：
// 签名若由循环输入（corner / keyboard.name / vvHeight / pan / drag）拼成，48 格天然
// 互异，`seen.size` 恒真——夹具把两格折成同一份落位（老缺陷：pan 被折回 0）它照样
// 绿，断言就退化成"循环跑了 N 次"。原子项取这条矩阵逐格断言的那些写出量：写进
// style 的四边 inset、由 inset + pan 换算出的 client 落点、键盘缺口与由它驱动的
// lifted / panCorrected。顶边居位不看键盘、底边居位不看横向 pan，没有一格是靠
// "输入不同"被救活的：两格写出同一份结果，这里就报重复。
function placementFingerprint(layout) {
  return JSON.stringify([layout.top, layout.bottom, layout.left, layout.right,
    layout.clientRect, layout.keyboardShortfall, layout.lifted, layout.panCorrected]);
}

test("四角 × 键盘 × pan 矩阵：面板每一格都落在屏显区域内，或明示不支持", () => {
  const px = (value) => (typeof value === "string" ? parseFloat(value) : finiteNumber(value));
  function finiteNumber(value) { return Number.isFinite(value) ? value : 0; }
  const seen = new Set();
  for (const corner of MATRIX_CORNERS) {
    for (const keyboard of MATRIX_KEYBOARD) {
      for (const pan of MATRIX_PANS) {
        const { layout, vvHeight } = matrixCase(corner, keyboard, pan, null);
        const signature = placementFingerprint(layout);
        assert.equal(seen.has(signature), false, `矩阵里出现重复落位：${signature}`);
        seen.add(signature);
        const label = `${corner} · ${keyboard.name} · ${pan.name}`;
        if (layout.support !== "supported") {
          // "不支持"只允许出现在物理上放不下的场合——这里必须自证它确实放不下。
          assert.equal(layout.unsupportedReason, "visible-window-too-short-for-panel", `${label}: unsupported 只能有物理原因`);
          assert.ok(vvHeight < 88 + 20 + 8, `${label}: 声明不支持却仍有 ${vvHeight}px 可用，不许放弃`);
          continue;
        }
        const box = layout.clientRect;
        assert.ok(box.top >= 0, `${label}: 顶边出屏 top=${box.top}`);
        assert.ok(box.bottom <= vvHeight + 1, `${label}: 落进键盘覆盖区 bottom=${box.bottom} > vv.height=${vvHeight}`);
        assert.ok(box.left >= 0 && box.right <= MATRIX_WIDTH + 1, `${label}: 横向越界 ${box.left}..${box.right}`);
        // 书写值必须能换算回同一个可见窗口（client = layout − offsetTop / offsetLeft）。
        if (corner.startsWith("b")) {
          const clientBottom = MATRIX_LAYOUT_HEIGHT - px(layout.bottom) - pan.top;
          assert.ok(Math.abs(clientBottom - box.bottom) <= 1, `${label}: bottom ${layout.bottom} 换算回 client 应为 ${box.bottom}，实得 ${clientBottom}`);
        } else {
          const clientTop = px(layout.top) - pan.top;
          assert.ok(Math.abs(clientTop - box.top) <= 1, `${label}: top ${layout.top} 换算回 client 应为 ${box.top}，实得 ${clientTop}`);
        }
        assert.equal(layout.keyboardShortfall, Math.max(0, MATRIX_LAYOUT_HEIGHT - vvHeight), `${label}: raw 只是测量，必须与几何一致`);
        if (corner.startsWith("b") && layout.keyboardShortfall > 1) {
          assert.equal(layout.lifted, true, `${label}: 键盘打开时底部居位必须抬到键盘顶边之上`);
        }
        if (corner.startsWith("b")) {
          assert.equal(layout.lifted, layout.keyboardShortfall > 1,
            `${label}: raw ≤ 1 按冻结 §5 不算键盘，底部居位不得做无谓抬起`);
        } else {
          assert.equal(layout.lifted, false, `${label}: 顶部居位不涉及抬起`);
        }
        // panCorrected 必须诚实：只有在 pan 真的改变了要写的 inset 时才为 true。
        // 纵向：底边居位没有键盘压进来时保持 rest 位（冻结 §5：raw ≤ 1 不算键盘）；
        // 顶边 / free 跟着平移。横向：每一格都写 left / right 之一，横向 pan 永远
        // 改变写出的值，所以它单独就能让 panCorrected 为真。
        const bottomCorner = corner === "bl" || corner === "br";
        const verticalMatters = pan.top !== 0 && (!bottomCorner || layout.keyboardShortfall > 1);
        const panChangesTheWriting = verticalMatters || pan.left !== 0;
        assert.equal(layout.panCorrected, panChangesTheWriting,
          `${label}: panCorrected=${layout.panCorrected}，pan=${pan.top}/${pan.left} shortfall=${layout.keyboardShortfall}`);
        if (!corner.startsWith("b") && pan.top !== 0) {
          assert.ok(px(layout.top) >= pan.top, `${label}: 顶边 inset ${layout.top} 必须含外壳平移 pan=${pan.top}，否则 client 落点出屏`);
        }
      }
    }
  }
  // M-2 ①：断言的是互异**落位结果**数，不是循环次数。
  assert.equal(seen.size, MATRIX_CORNERS.length * MATRIX_KEYBOARD.length * MATRIX_PANS.length,
    "四角 × 三档键盘 × 四档 pan 的落位结果必须互不相同，一格不能被夹具折成另一格");
});

test("无键盘 + pan≠0 是真实几何：不再被折叠，且面板仍在屏内（M-2 ②）", () => {
  const px = (value) => parseFloat(value);
  // 真机 b90643be 里有 clientHeight == vv.height 而 vv.offsetTop ≠ 0 的帧；把这种
  // 几何折回 pan=0，矩阵就从未覆盖过"外壳被平移、键盘没打开"的场合。
  for (const corner of [...MATRIX_CORNERS, "free"]) {
    const { layout } = matrixCase(corner, MATRIX_KEYBOARD[0], MATRIX_PANS[1], corner === "free" ? MATRIX_DRAGS[0] : null);
    const label = `${corner} · 无键盘 · ${MATRIX_PANS[1].name}`;
    assert.equal(layout.keyboardShortfall, 0, `${label}: raw=0 是这一格的定义`);
    assert.equal(layout.keyboardPresent, false, `${label}: raw ≤ 1 按冻结 §5 不算键盘`);
    assert.equal(layout.vvOffsetTop, MATRIX_PAN, `${label}: pan 事实必须原样保留，不许折回 0`);
    assert.equal(layout.support, "supported", `${label}: ${corner} 这一格放得下就不许声明不支持`);
    assert.ok(layout.clientRect.top >= 0, `${label}: client top ${layout.clientRect.top} 出屏`);
    assert.ok(layout.clientRect.bottom <= MATRIX_LAYOUT_HEIGHT + 1, `${label}: client bottom ${layout.clientRect.bottom} 出屏`);
    // 纵向 pan、无键盘：顶边 / free 必须跟着平移写；底边保持 rest 位（raw ≤ 1 按
    // 冻结 §5 不算键盘，既不抬起也不参与平移）——所以底边的 panCorrected 在这一格
    // 就该是 false，这是诚实的"没有改变写出的值"，不是漏修。
    const bottomCorner = corner === "bl" || corner === "br";
    assert.equal(layout.panCorrected, !bottomCorner,
      `${label}: 顶边 / free 跟着平移写，底边在无键盘时保持 rest 位`);
    assert.equal(layout.lifted, false, `${label}: 无键盘（raw ≤ 1）没有一格该被抬起`);
    if (corner === "tr" || corner === "tl") {
      assert.ok(px(layout.top) >= MATRIX_PAN, `${label}: 顶边 inset 必须含 pan，否则整块出屏`);
    }
  }
});

test("横向 pan 走 inset 夹紧分支（M-2 ③）", () => {
  const px = (value) => parseFloat(value);
  for (const pan of [MATRIX_PANS[2], MATRIX_PANS[3]]) {
    for (const corner of MATRIX_CORNERS) {
      const { layout } = matrixCase(corner, MATRIX_KEYBOARD[1], pan, null);
      const label = `${corner} · 键盘打开 · ${pan.name}`;
      // inset 永不为负（负 inset 在 overscroll 下画不出来），且落点必须自洽。
      const insets = [layout.top, layout.bottom, layout.left, layout.right].filter((v) => v !== "auto");
      for (const inset of insets) {
        assert.ok(px(inset) >= 0, `${label}: inset ${inset} 为负 = 写不出去的值`);
      }
      assert.equal(layout.vvOffsetLeft, pan.left, `${label}: 横向 pan 事实必须原样上报`);
      assert.ok(layout.clientRect.left >= 0 && layout.clientRect.right <= MATRIX_WIDTH + 1,
        `${label}: 横向落点 ${layout.clientRect.left}..${layout.clientRect.right} 越出 [0, ${MATRIX_WIDTH}]`);
      const rest = matrixCase(corner, MATRIX_KEYBOARD[1], MATRIX_PANS[0], null).layout;
      const moved = Math.abs(px(layout.left) - px(rest.left)) > 0.5 || Math.abs(px(layout.right) - px(rest.right)) > 0.5;
      assert.equal(layout.panCorrected, moved || layout.panCorrected,
        `${label}: panCorrected 必须与"写出的值是否真的变了"一致`);
    }
  }
});

test("free 居位（拖拽）落在可见窗口内并保留边距（M-2 ④ + M-4）", () => {
  const px = (value) => parseFloat(value);
  const PANEL_MARGIN = 8;
  const seen = new Set();
  for (const keyboard of MATRIX_KEYBOARD) {
    for (const drag of MATRIX_DRAGS) {
      for (const pan of [MATRIX_PANS[0], MATRIX_PANS[1]]) {
        const { layout } = matrixCase("free", keyboard, pan, drag);
        const signature = placementFingerprint(layout);
        assert.equal(seen.has(signature), false, `free 矩阵里出现重复落位：${signature}`);
        seen.add(signature);
        const label = `free · ${keyboard.name} · ${pan.name} · ${drag.name}`;
        assert.equal(layout.corner, "free", `${label}: 居位必须自报 free`);
        assert.equal(layout.lifted, false, `${label}: free 居位不涉及底部抬起`);
        if (layout.support !== "supported") continue;   // 极窄 / 极矮的诚实声明由专例覆盖
        const box = layout.clientRect;
        assert.ok(box.top >= 0, `${label}: client top ${box.top} 出屏`);
        assert.ok(box.bottom <= keyboard.vvHeight + 1, `${label}: client bottom ${box.bottom} 落进键盘区`);
        assert.ok(box.left >= 0 && box.right <= MATRIX_WIDTH + 1, `${label}: 横向越界`);
        // M-4：拖到极限也只夹到 `− PANEL_MARGIN`，不贴屏幕边缘、不压键盘顶边。
        if (drag.name === "拖出右下界") {
          assert.ok(Math.abs(MATRIX_WIDTH - box.right - PANEL_MARGIN) <= 1,
            `${label}: 右界应留 ${PANEL_MARGIN}px 边距，实留 ${MATRIX_WIDTH - box.right}`);
          assert.ok(Math.abs(keyboard.vvHeight - box.bottom - PANEL_MARGIN) <= 1,
            `${label}: 下界应留 ${PANEL_MARGIN}px 边距，实留 ${keyboard.vvHeight - box.bottom}`);
        }
        // 顶边 inset 必须含 pan（free 也一样：fixed 包含块被平移）。
        if (pan.top !== 0) assert.ok(px(layout.top) >= pan.top, `${label}: 顶边 inset ${layout.top} 必须含 pan`);
      }
    }
  }
  assert.equal(seen.size, MATRIX_KEYBOARD.length * MATRIX_DRAGS.length * 2,
    "free 格数 = 三档键盘 × 三档拖拽 × 两档 pan，一格不能少，且落位结果互异");
});

test("c3ee seq253 同形态：scale<1 回弹瞬间 free 落点在可见窗口内必须 supported（AUTH-20261105-DIAG-PANEL-CLIENT 方案②）", () => {
  const api = loadAPI();
  // 真机输入原样（raw c3ee73e2653ba89dc615d4554ceb7204 seq 253）：vv 1592×906 /
  // layout 1194×748 / vv.offsetTop 90.65625 / corner=free + dragX 1096。
  // 旧口径同帧自报 unsupported / visible-window-outside-layout-viewport，而真机
  // getBoundingClientRect（left 1104, right 1464）与 insideVisibleViewport=true 都
  // 说明面板完整可见——VALIDATION 2026-11-05 仲裁定案：工具误判，修 layout↔client
  // 口径错配，layout 远界不再作为 unsupported 理由。
  const SRC = {
    innerWidth: 1592, innerHeight: 906,
    layoutWidth: 1194, layoutHeight: 748,
    vvWidth: 1592, vvHeight: 906,
    vvOffsetTop: 90.65625, vvOffsetLeft: 0,
    corner: "free", dragX: 1096, dragY: 0,
  };
  const layout = api.panelLayoutFor(SRC);
  assert.equal(layout.support, "supported", `seq253 同形态必须 supported，实得 ${layout.support} / ${layout.unsupportedReason}`);
  assert.equal(layout.unsupportedReason, "");
  const box = layout.clientRect;
  assert.ok(box.left >= 0 && box.right <= 1592, `横向落点 ${box.left}..${box.right} 必须在 [0, vv.width=1592] 内`);
  assert.ok(box.top >= 0 && box.bottom <= 906, `纵向落点 ${box.top}..${box.bottom} 必须在 [0, vv.height=906] 内`);
  // 反证旧界仍在现场：这一格的落点越过**工具读取的 layout 视口输入**
  // （layoutWidth 读自 documentElement.clientWidth = 1194，与同帧实测 ICB ≥1592 不同源，
  // 不描述绘制事实——seq253 无 gutter），正是被删除的 `layoutLeft + width > layoutWidth`
  // 曾经误判的那条界；本断言作为旧远界回归钉子保留。
  assert.ok(box.right > layout.layoutWidth, `这一格必须真的越出 layout 远缘 ${layout.layoutWidth}，否则没有证到旧 layout 界`);
  assert.equal(layout.left, "1104px", "free+drag 1096 写出的 left inset = dragX + PANEL_MARGIN，与真机 client left 1104 同值（唯一合成点不折返）");
  // 拖拽预算 = 支撑判定（方案②第 2 条）：拖到 free 预算右 / 下极限（贴 vv − PANEL_MARGIN）
  // 这一格必须同样 supported——预算允许拖到的任何位置，判定都不许把它抹成不支持。
  const limit = api.panelLayoutFor({ ...SRC, dragX: 1592 - 360 - 8 - 8, dragY: 9999 });
  assert.equal(limit.support, "supported", `free 拖拽预算极限必须 supported，实得 ${limit.support} / ${limit.unsupportedReason}`);
  assert.ok(Math.abs(1592 - limit.clientRect.right - 8) <= 1 && Math.abs(906 - limit.clientRect.bottom - 8) <= 1,
    `极限格保留 8px 贴边余量：实得右 ${1592 - limit.clientRect.right} 下 ${906 - limit.clientRect.bottom}`);

  // 快照级同场自洽（panelReportOf 读法）：seq253 实测 client rect（真机测量值）补上
  // 横向界后仍判 true，与 support=supported 同帧一致，消除"纵向 true / 横向 unsupported"。
  const panel = {
    style: {},
    dataset: {
      debugCorner: "free", debugSupport: "supported", debugUnsupportedReason: "",
      debugLifted: "0", debugPanCorrected: "1", debugNominalHeight: "520",
    },
    getBoundingClientRect: () => ({ top: 7.65625, bottom: 233.734375, left: 1104, right: 1464, width: 360, height: 226.078125 }),
  };
  const win = {
    innerWidth: 1592, innerHeight: 906, devicePixelRatio: 2,
    visualViewport: { width: 1592, height: 906, offsetTop: 90.65625, offsetLeft: 0, scale: 0.75 },
    getComputedStyle: () => ({ display: "none" }),
    matchMedia: () => ({ matches: true }),
    __mwTerminalOcclusion: 0,
  };
  win.document = {
    documentElement: { clientWidth: 1194, clientHeight: 748 },
    getElementById: (id) => (id === "viewport-debug-panel" ? panel : null),
  };
  api.applyPanelLayout(win, panel, { corner: "free", safeTop: 0, safeRight: 0, safeBottom: 0, dragX: 1096, dragY: 0, collapsed: false });
  assert.equal(panel.style.left, "1104px", "写出的 fixed inset 与 seq253 真机 client 落点同值");
  assert.equal(panel.dataset.debugSupport, "supported", "applyPanelLayout 在 seq253 几何必须写 supported");
  const snapshot = api.snapshotFromPage({ window: win, document: win.document, state: { collapsed: false, quickKeysOpen: false, kind: "pty", status: "running" } }, "manual", "2026-11-05T02:21:40.005Z");
  assert.equal(snapshot.panel.support, "supported");
  assert.equal(snapshot.panel.unsupportedReason, "");
  assert.equal(snapshot.panel.insideVisibleViewport, true, `实测 rect ${snapshot.panel.rect.left}..${snapshot.panel.rect.right} 必须在 [0, vv.width=${snapshot.vv.width}] 内（横向界补完后可见窗完整成立）`);
  assert.equal(snapshot.panel.keyboardOverlap, 0);
});

test("不支持只留给真放不下的场合", () => {
  const api = loadAPI();
  const px = (value) => parseFloat(value);
  // 1. 可见窗口比折叠态主按钮一排还矮：真放不下。
  const tiny = api.panelLayoutFor({
    innerWidth: MATRIX_WIDTH, innerHeight: MATRIX_LAYOUT_HEIGHT,
    layoutWidth: MATRIX_WIDTH, layoutHeight: MATRIX_LAYOUT_HEIGHT,
    vvWidth: MATRIX_WIDTH, vvHeight: 70, corner: "br", safeBottom: 20,
  });
  assert.equal(tiny.support, "unsupported");
  assert.equal(tiny.unsupportedReason, "visible-window-too-short-for-panel");
  // 2. overscroll：pan 为负 = 可见窗口顶边落在 fixed 包含块之上。顶边居位要么被
  //    夹到 inset 0（落点仍出屏），要么 required inset 为负（写不出去）——无处可放，
  //    必须明说"不支持"，不能报一个看起来摆好了的面板。
  const outOfBounds = api.panelLayoutFor({
    innerWidth: MATRIX_WIDTH, innerHeight: MATRIX_LAYOUT_HEIGHT,
    layoutWidth: MATRIX_WIDTH, layoutHeight: MATRIX_LAYOUT_HEIGHT,
    vvWidth: MATRIX_WIDTH, vvHeight: 680, vvOffsetTop: -400, corner: "tr", safeBottom: 20,
  });
  assert.equal(outOfBounds.support, "unsupported");
  assert.equal(outOfBounds.unsupportedReason, "visible-window-outside-layout-viewport");
  assert.equal(px(outOfBounds.top), 0, "inset 被夹到 0：需要的负 inset 写不出去");
  assert.ok(outOfBounds.clientRect.bottom > 680, `真实落点 ${outOfBounds.clientRect.bottom} 必须暴露越界事实`);
  const outOfBoundsBottom = api.panelLayoutFor({
    innerWidth: MATRIX_WIDTH, innerHeight: MATRIX_LAYOUT_HEIGHT,
    layoutWidth: MATRIX_WIDTH, layoutHeight: MATRIX_LAYOUT_HEIGHT,
    vvWidth: MATRIX_WIDTH, vvHeight: 680, vvOffsetTop: -400, corner: "br", safeBottom: 20,
  });
  assert.equal(outOfBoundsBottom.support, "unsupported", "底部居位同样无处可放：落点顶边落在 fixed 包含块之上（origin 负值 = overscroll 负值物理原因；layout 远界已按方案②删除，这里不得是远界拒绝）");
  // 3. 六分屏级窄宽：面板宽度无处安放。
  const narrow = api.panelLayoutFor({
    innerWidth: 150, innerHeight: 702, layoutWidth: 150, layoutHeight: 702,
    vvWidth: 150, vvHeight: 702, corner: "tr",
  });
  assert.equal(narrow.support, "unsupported");
  assert.equal(narrow.unsupportedReason, "visible-window-too-narrow-for-panel");
  // 4. 反证：凡是放得下的可见高度，一律不得出现"不支持"。pan 是独立的一轴，
  //    不再按键盘缺口折叠（M-2 ②：真机上有 clientHeight=vv.height 而 offsetTop≠0
  //    的帧），所以每一档可见高度都要在 pan=0 / 纵向 pan / 横向 pan 下各自成立。
  for (let vvHeight = 130; vvHeight <= MATRIX_LAYOUT_HEIGHT; vvHeight += 37) {
    for (const corner of [...MATRIX_CORNERS, "free"]) {
      for (const pan of [MATRIX_PANS[0], MATRIX_PANS[1], MATRIX_PANS[2]]) {
        const label = `${corner} @ vv.height=${vvHeight} ${pan.name}`;
        const layout = api.panelLayoutFor({
          innerWidth: MATRIX_WIDTH, innerHeight: MATRIX_LAYOUT_HEIGHT,
          layoutWidth: MATRIX_WIDTH, layoutHeight: MATRIX_LAYOUT_HEIGHT,
          vvWidth: MATRIX_WIDTH, vvHeight: vvHeight, vvOffsetTop: pan.top, vvOffsetLeft: pan.left,
          corner, safeTop: 24, safeBottom: 20, dragX: 120, dragY: 120,
        });
        assert.equal(layout.support, "supported", `${label}: 放得下却声明不支持`);
        assert.ok(layout.clientRect.top >= 0, `${label}: top ${layout.clientRect.top} 出屏`);
        assert.ok(layout.clientRect.bottom <= vvHeight + 1, `${label}: bottom ${layout.clientRect.bottom} 落进键盘区`);
        assert.ok(layout.clientRect.left >= 0 && layout.clientRect.right <= MATRIX_WIDTH + 1, `${label}: 横向越界`);
        assert.ok(px(layout.maxHeight) <= vvHeight, `${label}: maxHeight ${layout.maxHeight} 超过可见窗口`);
      }
    }
  }
});

test("无遮挡时面板不被无谓抬起（保持原书写的 bottom）", () => {
  const api = loadAPI();
  const rest = api.panelLayoutFor({
    innerWidth: MATRIX_WIDTH, innerHeight: MATRIX_LAYOUT_HEIGHT,
    layoutWidth: MATRIX_WIDTH, layoutHeight: MATRIX_LAYOUT_HEIGHT,
    vvWidth: MATRIX_WIDTH, vvHeight: MATRIX_LAYOUT_HEIGHT, vvOffsetTop: 0,
    corner: "bl", safeBottom: 20,
  });
  assert.equal(rest.lifted, false);
  assert.equal(px2(rest.bottom), 20 + 8, "无键盘无 pan 时仍是 safe-area + 边距，不做无谓移动");
  assert.equal(rest.panCorrected, false);
  const liftedKeyboard = api.panelLayoutFor({
    innerWidth: MATRIX_WIDTH, innerHeight: MATRIX_LAYOUT_HEIGHT,
    layoutWidth: MATRIX_WIDTH, layoutHeight: MATRIX_LAYOUT_HEIGHT,
    vvWidth: MATRIX_WIDTH, vvHeight: MATRIX_LAYOUT_HEIGHT - 428, vvOffsetTop: 0,
    corner: "bl", safeBottom: 20,
  });
  assert.equal(liftedKeyboard.lifted, true);
  assert.ok(px2(liftedKeyboard.bottom) > px2(rest.bottom) + 400, "键盘打开时 bottom 必须抬到键盘顶边之上");
  function px2(value) { return parseFloat(value); }
});

test("applyPanelLayout 写下的落位被快照原样读回（键盘 + pan 的集成面）", () => {
  const api = loadAPI();
  const PANEL_MARGIN = 8;
  const LAYOUT_HEIGHT_PAN = 1108;
  const VV_PAN = 680;
  const PAN = 403;
  const controls = { getBoundingClientRect: () => ({ width: 44, height: 1000 }), hidden: false };
  const panel = { style: {}, dataset: {} };
  // 面板的 client rect 由它自己的 fixed inset + pan 推出来，跟浏览器一致：
  // client = layout − vv.offsetTop。
  panel.getBoundingClientRect = () => {
    const layoutTop = panel.style.top === "auto"
      ? LAYOUT_HEIGHT_PAN - parseFloat(panel.style.bottom) - 520
      : parseFloat(panel.style.top);
    const layoutLeft = panel.style.left === "auto"
      ? MATRIX_WIDTH - parseFloat(panel.style.right) - 360
      : parseFloat(panel.style.left);
    return {
      top: layoutTop - PAN,
      bottom: layoutTop - PAN + 520,
      left: layoutLeft,
      right: layoutLeft + 360,
      width: 360,
      height: 520,
    };
  };
  const win = {
    innerWidth: MATRIX_WIDTH,
    innerHeight: LAYOUT_HEIGHT_PAN,
    devicePixelRatio: 2,
    visualViewport: { width: MATRIX_WIDTH, height: VV_PAN, offsetTop: PAN, offsetLeft: 0, scale: 1 },
    getComputedStyle: () => ({ display: "flex" }),
    matchMedia: () => ({ matches: true }),
    __mwTerminalOcclusion: 428,
  };
  win.document = {
    documentElement: { clientWidth: MATRIX_WIDTH, clientHeight: LAYOUT_HEIGHT_PAN },
    getElementById: (id) => (id === "terminal-controls" ? controls : (id === "viewport-debug-panel" ? panel : null)),
  };
  const state = { corner: "br", safeTop: 24, safeRight: 0, safeBottom: 20, dragX: 0, dragY: 0, collapsed: false };
  api.applyPanelLayout(win, panel, state);

  assert.equal(panel.style.bottom, "53px", "br + 键盘 + pan=403：底边 inset = layout 高 − 可见底边 + 安全区");
  assert.equal(panel.style.right, `${44 + PANEL_MARGIN * 2 + PANEL_MARGIN}px`, "靠右居位继续为 quick-keys 控制列预留");
  assert.equal(panel.style.top, "auto");
  assert.equal(panel.dataset.debugCorner, "br");
  assert.equal(panel.dataset.debugSupport, "supported");
  assert.equal(panel.dataset.debugLifted, "1");
  assert.equal(panel.dataset.debugPanCorrected, "1");
  assert.equal(panel.dataset.debugNominalHeight, "520");

  const snapshot = api.snapshotFromPage({ window: win, document: win.document, state: { collapsed: false, quickKeysOpen: false, kind: "pty", status: "running" } }, "manual", "2026-11-04T12:00:00.000Z");
  assert.equal(snapshot.panel.exists, true);
  assert.equal(snapshot.panel.corner, "br");
  assert.equal(snapshot.panel.support, "supported");
  assert.equal(snapshot.panel.keyboardOverlap, 0, `面板底边 ${snapshot.panel.rect.bottom} 必须落在键盘顶边 ${VV_PAN} 之上`);
  assert.equal(snapshot.panel.insideVisibleViewport, true);
  assert.equal(snapshot.keyboardSession.derivedState, "active");

  // 控制列隐藏（无活动实例）时不再预留，右缘回到安全区 + 边距。
  controls.getBoundingClientRect = () => ({ width: 0, height: 0 });
  controls.hidden = true;
  win.getComputedStyle = () => ({ display: "none" });
  api.applyPanelLayout(win, panel, state);
  assert.equal(panel.style.right, `${PANEL_MARGIN}px`, "控制列隐藏就不预留");
  // 居位切换到 free 后自报跟着变（Move 按钮 / 拖拽的真实路径）。
  api.applyPanelLayout(win, panel, { ...state, corner: "free", dragX: 9999, dragY: 9999 });
  assert.equal(panel.dataset.debugCorner, "free");
  // M-4：夹回的口径含边距——面板贴到 `vv.width − width − PANEL_MARGIN`，不压屏幕边缘。
  assert.equal(panel.style.left, `${MATRIX_WIDTH - 360 - PANEL_MARGIN}px`, "拖出右界必须夹回可见窗口内，并保留贴边余量");
  assert.equal(panel.dataset.debugLifted, "0", "free 居位不涉及抬起");
});

test("the additive blocks stay out of the product geometry and out of the leak list", () => {
  const { page } = makePage();
  const snapshot = loadAPI().snapshotFromPage(page, "manual", "2026-11-04T12:00:00.000Z");
  const serialized = JSON.stringify(snapshot);
  for (const forbidden of ["instance-private-id", "must-not-leak", "token", "worktree", "terminalText", "/home/"]) {
    assert.equal(serialized.includes(forbidden), false, `the additive fields leaked ${forbidden}`);
  }
  // 诊断读 offsetTop 是事实记录（术语章：可见窗口在 layout 坐标的另一种描述），
  // 产品避让的 occlusion / raw 绝不由它产生（PLAN §3 禁止把 offsetTop 入 raw）。
  const code = source.replace(/\/\/[^\n]*/g, "").replace(/\/\*[\s\S]*?\*\//g, "");
  for (const banned of [
    /\braw[A-Za-z]*\s*=\s*[^;\n]*offsetTop/i,
    /\bocclusion[A-Za-z]*\s*=\s*[^;\n]*offsetTop/i,
    /vv\.height\s*\+\s*\w*offsetTop/i,
  ]) {
    assert.equal(banned.test(code), false, `offsetTop must never enter a compensation formula, matched ${banned}`);
  }
  assert.ok(/visibleWindowLayout:\s*\{\s*top:\s*vvOffsetTop,\s*bottom:\s*vvOffsetTop \+ vvHeight\s*\}/.test(code),
    "the layout-space description of the SAME window is the only place offsetTop composes, and it is a report, not a write");
});

test("the probe measures the product and writes none of it", () => {
  // Phase 2 adds a focused-host walk, an event journal and the panel's own box.
  // None of that may become a write: every product element gets a style / class /
  // attribute tripwire, so a "diagnostic" that quietly compensates fails here
  // instead of quietly invalidating the measurement it is supposed to prove.
  const { page, app, workspace, host, controls, statusBar, terminalContainer } = makePage();
  const guards = [app, workspace, host, controls, statusBar, terminalContainer,
    page.document.body, page.document.scrollingElement];
  const tripwire = (label) => new Proxy({}, {
    set() { throw new Error(`the probe wrote style on ${label}`); },
  });
  for (const element of guards) {
    element.style = tripwire("style");
    element.setAttribute = () => { throw new Error("the probe called setAttribute on a product element"); };
    element.getAttribute = (name) => (name === "data-instance-id" ? "instance-private-id" : null);
    if (element.classList) {
      element.classList.add = () => { throw new Error("the probe added a class"); };
      element.classList.remove = () => { throw new Error("the probe removed a class"); };
    }
  }
  const trace = loadAPI().createEventTrace(page.window, page.document);
  host.tagName = "DIV";   // the stand-in host needs a tag for the structure walk
  const snapshot = loadAPI().snapshotFromPage(page, "manual", "2026-11-04T12:00:00.000Z", { eventTrace: trace });
  assert.equal(snapshot.reason, "manual");
  assert.equal(snapshot.occlusion, OCCLUSION, "reading the mirror stays exact even under tripwired writes");
  assert.equal(snapshot.panel.exists, false);
  trace.note("click", { target: host });
  assert.equal(trace.report().counts.clickOnTerminalHost, 1, "journalling the host reads structure only");
});
