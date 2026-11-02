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

function makePage({ withVV = true } = {}) {
  const app = fakeElement(
    fakeRect(0, 1164, 820, 1164),
    { height: "1164px", paddingTop: "0px", paddingRight: "0px", paddingBottom: "16px", paddingLeft: "0px", backgroundColor: "rgba(0, 0, 0, 0)" },
    ["collapsed"],
  );
  const main = fakeElement(fakeRect(0, 1164, 620, 1164));
  const workspace = fakeElement(fakeRect(40, 1148, 620, 1108));
  const xtermScreen = fakeElement(fakeRect(40, 1147, 620, 1107));
  const xtermRows = fakeElement(fakeRect(40, 1138, 620, 1098));
  const host = fakeElement(fakeRect(40, 1148, 620, 1108));
  host.querySelector = (selector) => selector === ".xterm-screen" ? xtermScreen : selector === ".xterm-rows" ? xtermRows : null;
  const listeners = new Map();
  const vv = withVV ? {
    width: 820, height: 1164, offsetTop: 0, offsetLeft: 0, scale: 1, pageTop: 0,
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
        return { backgroundColor: "rgb(11, 16, 32)" };
      },
    },
    document: {
      body: { scrollTop: 0 },
      scrollingElement: { scrollTop: 0, scrollHeight: 1164 },
      documentElement: { clientWidth: 820, clientHeight: 1180 },
      activeElement: { tagName: "TEXTAREA" },
      querySelector: () => null,
      // issue #116: the bottom-edge probe hit-tests this. Returning the
      // workspace stands in for "the pixels at the bottom edge are the app's own
      // ink", the healthy state; a case that wants to simulate an exposed root
      // canvas overrides this to return the documentElement.
      elementFromPoint: () => ({ tagName: "DIV", id: "workspace", className: "" }),
      getElementById(id) {
        return id === "app" ? app : id === "main" ? main : id === "workspace" ? workspace : null;
      },
    },
    state,
    getActiveTerminalSession: () => ({ container: host, quickKeysOpen: true }),
  };
  return { page, app, main, workspace, host, vv, listeners };
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
  assert.equal(snapshot.vv.height, 1164);
  assert.equal(snapshot.app.bottom, 1164);
  assert.equal(snapshot.app.paddingBottom, "16px");
  assert.equal(snapshot.app.inlineHeight, "1164px");
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
    visualBottom: { y: 1162, tag: "DIV", id: "workspace", className: "", background: "rgb(11, 16, 32)" },
    layoutBottom: { y: 1178, tag: "DIV", id: "workspace", className: "", background: "rgb(11, 16, 32)" },
  });
  assert.equal(snapshot.workspace.bottom, 1148);
  assert.equal(snapshot.terminal.bottom, 1148);
  assert.equal(snapshot.webPanel, null);
  assert.equal(snapshot.xtermScreen.bottom, 1147);
  assert.equal(snapshot.xtermRows.bottom, 1138);
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
