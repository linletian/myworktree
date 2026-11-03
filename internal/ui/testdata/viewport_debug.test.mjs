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
  const listeners = new Map();
  const vv = withVV ? {
    width: 820, height: VV_HEIGHT, offsetTop: 0, offsetLeft: 0, scale: 1, pageTop: 0,
    addEventListener(type, handler) {
      if (!listeners.has(type)) listeners.set(type, []);
      listeners.get(type).push(handler);
    },
    removeEventListener(type, handler) {
      listeners.set(type, (listeners.get(type) || []).filter((item) => item !== handler));
    },
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
  return { page, app, main, workspace, host, terminalContainer, controls, statusBar, quickKeys, vv, listeners };
}

function makeHarness(options = {}) {
  const { page, listeners } = makePage(options);
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
    client, page, listeners, uploads, frames, timers,
    flushFrames() {
      while (frames.length) frames.shift().callback();
    },
    runTimer() {
      const [id, timer] = timers.entries().next().value || [];
      assert.ok(id, "expected a pending timer");
      timers.delete(id);
      timer.callback();
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
