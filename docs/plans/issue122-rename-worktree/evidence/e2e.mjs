// Issue #122 end-to-end verification: real daemon + real git repo + headless
// Chrome over CDP. Drives the §5 manual checklist items that the node/Go unit
// layers cannot prove (dashboard behaviour, Network-level PATCH, reload
// persistence, poll survival with an IME composition open).
import { spawn, execSync } from "node:child_process";
import { mkdtempSync, writeFileSync, mkdirSync } from "node:fs";
import { tmpdir } from "node:os";
import { join } from "node:path";
import { setTimeout as sleep } from "node:timers/promises";

const PORT = 39777;
const TOKEN = "e2e-token";
const BASE = `http://127.0.0.1:${PORT}`;
const ROOT = mkdtempSync(join(tmpdir(), "issue122-e2e-"));

const results = [];
const check = (name, ok, detail = "") => {
  results.push({ name, ok, detail });
  console.log(`${ok ? "PASS" : "FAIL"}  ${name}${detail ? ` — ${detail}` : ""}`);
};

// --- scratch git repo (the daemon's GitRoot) --------------------------------
mkdirSync(ROOT, { recursive: true });
writeFileSync(join(ROOT, "README.md"), "seed\n");
const git = (...args) => execSync(["git", ...args].join(" "), { cwd: ROOT, stdio: "pipe" }).toString();
git("init");
git("config user.name E2E");
git("config user.email e2e@example.com");
git("add README.md");
git("commit -m init");

// --- daemon ------------------------------------------------------------------
const daemon = spawn("/tmp/issue122-e2e/myworktree", [
  "--listen", `127.0.0.1:${PORT}`, "--portal-port", "0", "--auth", TOKEN, "--worktrees-dir", "data",
], { cwd: ROOT, stdio: ["ignore", "pipe", "pipe"] });
let daemonLog = "";
daemon.stdout.on("data", (d) => (daemonLog += d));
daemon.stderr.on("data", (d) => (daemonLog += d));

async function api(method, path, body) {
  const res = await fetch(BASE + path, {
    method,
    headers: { "Content-Type": "application/json", "Authorization": `Bearer ${TOKEN}`, "X-CSRF-Token": "" },
    body: body ? JSON.stringify(body) : undefined,
  });
  return { status: res.status, body: await res.json().catch(() => null) };
}

async function waitUp() {
  for (let i = 0; i < 60; i++) {
    try {
      const r = await api("GET", "/api/main");
      if (r.status === 200) return;
    } catch {}
    await sleep(250);
  }
  throw new Error("daemon never came up:\n" + daemonLog);
}

// --- CDP driver ---------------------------------------------------------------
const chrome = spawn("google-chrome", [
  "--headless=new", "--no-sandbox", "--disable-gpu",
  "--remote-debugging-port=9335", "about:blank",
], { stdio: "ignore" });

async function targetWs() {
  for (let i = 0; i < 50; i++) {
    try {
      const list = await (await fetch("http://127.0.0.1:9335/json/list")).json();
      const page = list.find((t) => t.type === "page");
      if (page) return page.webSocketDebuggerUrl;
    } catch {}
    await sleep(200);
  }
  throw new Error("chrome devtools never came up");
}

const ws = new WebSocket(await targetWs());
await new Promise((res, rej) => { ws.onopen = res; ws.onerror = rej; });
let msgId = 0;
const pending = new Map();
ws.onmessage = (ev) => {
  const m = JSON.parse(ev.data);
  if (m.id && pending.has(m.id)) { pending.get(m.id)(m); pending.delete(m.id); }
};
const send = (method, params = {}) => new Promise((res, rej) => {
  const id = ++msgId;
  const timer = setTimeout(() => rej(new Error(`CDP ${method} timed out`)), 20000);
  pending.set(id, (m) => { clearTimeout(timer); res(m); });
  ws.send(JSON.stringify({ id, method, params }));
});
const evalJs = async (expression) => {
  const r = await send("Runtime.evaluate", { expression, returnByValue: true, awaitPromise: true });
  if (r.result?.exceptionDetails) throw new Error("page eval failed: " + JSON.stringify(r.result.exceptionDetails));
  return r.result?.result?.value;
};

// Network capture: every /api/worktrees PATCH the page issues.
const patches = [];
ws.addEventListener("message", (ev) => {
  const m = JSON.parse(ev.data);
  if (m.method === "Network.requestWillBeSent" && m.params.request.url.endsWith("/api/worktrees") && m.params.request.method === "PATCH") {
    patches.push({ url: m.params.request.url, postData: m.params.request.postData });
  }
});

async function clickAt(x, y, clickCount = 1) {
  await send("Input.dispatchMouseEvent", { type: "mousePressed", x, y, button: "left", clickCount, buttons: 1 });
  await send("Input.dispatchMouseEvent", { type: "mouseReleased", x, y, button: "left", clickCount, buttons: 0 });
}
async function dblClickAt(x, y) {
  await clickAt(x, y, 1);
  await sleep(20);
  await clickAt(x, y, 2);
  await sleep(150);
}
async function keyDown(key, code) {
  await send("Input.dispatchKeyEvent", { type: "keyDown", key, code: code || key, windowsVirtualKeyCode: key === "Enter" ? 13 : key === "Escape" ? 27 : 0 });
  await send("Input.dispatchKeyEvent", { type: "keyUp", key, code: code || key });
}
const boxOf = async (selector) => evalJs(`(() => { const el = document.querySelector(${JSON.stringify(selector)}); if (!el) return null; const r = el.getBoundingClientRect(); return {x: r.x + r.width/2, y: r.y + r.height/2, w: r.width, h: r.height}; })()`);

try {
  await waitUp();
  check("daemon starts and serves /api/main", true);

  // --- seed a managed worktree through the real API -------------------------
  const created = await api("POST", "/api/worktrees", { task_description: "e2e double-click rename" });
  check("POST /api/worktrees creates the worktree", created.status === 201 && !!created.body?.id, JSON.stringify(created.body));
  const wtId = created.body.id;

  await send("Page.enable");
  await send("Network.enable");
  await send("Page.navigate", { url: `${BASE}/?token=${TOKEN}` });
  await sleep(1500);

  const rowCount = await evalJs(`document.querySelectorAll('#wt-list .wt-item:not(.wt-item-main)').length`);
  check("sidebar renders the worktree row", rowCount === 1, `rows=${rowCount}`);

  const rowSel = `#wt-list .wt-item:not(.wt-item-main)`;
  const rowBox = await boxOf(rowSel);
  check("row has a layout box", !!rowBox, JSON.stringify(rowBox));

  // --- AC1: double-click → editing input, focused + selected ----------------
  await dblClickAt(rowBox.x, rowBox.y);
  await sleep(300);
  const inputId = await evalJs(`(() => { const el = document.querySelector('${rowSel} input'); return el ? el.id : null; })()`);
  check("double-click opens the inline rename input", inputId === `wt-rename-input-${wtId}`, `id=${inputId}`);
  const focusSel = await evalJs(`(() => { const el = document.activeElement; return el ? (el.id + ':' + (el.selectionStart === 0 && el.selectionEnd === el.value.length)) : 'none'; })()`);
  check("input is focused with the text selected", focusSel === `${inputId}:true`, focusSel);

  // --- AC5-XSS: type a nasty name, commit with Enter -------------------------
  const nasty = `<script>alert(1)</script>"'& 长名字`;
  await evalJs(`(() => { const el = document.querySelector('${rowSel} input'); el.value = ${JSON.stringify(nasty)}; return el.value; })()`);
  await keyDown("Enter");
  await sleep(800);
  const patch = patches[patches.length - 1];
  check("Enter issues PATCH /api/worktrees", !!patch && patch.url.endsWith("/api/worktrees"), patch ? patch.postData : "none");
  check("PATCH body carries the trimmed raw name", !!patch && JSON.parse(patch.postData).name === nasty.trim(), patch ? patch.postData : "");
  const rowText = await evalJs(`document.querySelector('${rowSel} .wt-name').textContent`);
  check("sidebar row shows the new name as TEXT (no injection)", rowText === nasty.trim(), rowText);

  const gitList = execSync("git worktree list", { cwd: ROOT }).toString();
  check("git worktree list unaffected by the rename", gitList.split("\n").length >= 2 && gitList.includes(created.body.path), gitList.split("\n")[1] || "");
  const after = await api("GET", "/api/worktrees");
  const wt = after.body.worktrees.find((w) => w.id === wtId);
  check("Branch/Path unchanged server-side", !!wt && wt.branch === created.body.branch && wt.path === created.body.path, JSON.stringify(wt));

  // --- AC: Esc rolls back (no PATCH) -----------------------------------------
  const beforeEsc = patches.length;
  const box2 = await boxOf(rowSel);
  await dblClickAt(box2.x, box2.y);
  await sleep(250);
  await evalJs(`(() => { const el = document.querySelector('${rowSel} input'); el.value = 'should not persist'; return 1; })()`);
  await keyDown("Escape");
  await sleep(600);
  check("Escape issues no PATCH", patches.length === beforeEsc, `patches=${patches.length - beforeEsc}`);
  const rowText3 = await evalJs(`document.querySelector('${rowSel} .wt-name').textContent`);
  check("Escape restores the stored name", rowText3 === nasty.trim(), rowText3);

  // --- AC: empty commit writes nothing ---------------------------------------
  const box3 = await boxOf(rowSel);
  await dblClickAt(box3.x, box3.y);
  await sleep(250);
  await evalJs(`(() => { const el = document.querySelector('${rowSel} input'); el.value = '   '; return 1; })()`);
  await keyDown("Enter");
  await sleep(600);
  check("all-whitespace Enter issues no PATCH", patches.length === beforeEsc, `patches=${patches.length - beforeEsc}`);
  const rowText4 = await evalJs(`document.querySelector('${rowSel} .wt-name').textContent`);
  check("empty commit keeps the stored name", rowText4 === nasty.trim(), rowText4);

  // --- AC: non-empty blur commits (decision H parity) ------------------------
  const blurredName = "blurred-in";
  const box4 = await boxOf(rowSel);
  await dblClickAt(box4.x, box4.y);
  await sleep(250);
  await evalJs(`(() => { const el = document.querySelector('${rowSel} input'); el.value = '${blurredName}'; return 1; })()`);
  await evalJs(`document.querySelector('${rowSel} input').blur()`);
  await sleep(800);
  const blurPatch = patches[patches.length - 1];
  check("non-empty blur commits via PATCH", !!blurPatch && JSON.parse(blurPatch.postData).name === blurredName, blurPatch ? blurPatch.postData : "none");
  const rowText5 = await evalJs(`document.querySelector('${rowSel} .wt-name').textContent`);
  check("sidebar shows the blurred-in name", rowText5 === "blurred-in", rowText5);

  // --- AC3: input survives a background poll (incl. an open IME composition) --
  await send("Input.imeSetComposition", { text: "拼", selectionStart: 1, selectionEnd: 1 });
  const box5 = await boxOf(rowSel);
  await dblClickAt(box5.x, box5.y);
  await sleep(250);
  await evalJs(`(() => { const el = document.querySelector('${rowSel} input'); el.focus(); return 1; })()`);
  await send("Input.imeSetComposition", { text: "拼音输入中", selectionStart: 5, selectionEnd: 5 });
  const composing = await evalJs(`(() => { const el = document.querySelector('${rowSel} input'); return el ? (document.activeElement === el) : false; })()`);
  check("editing input is focused for the composition", composing);
  const pollTick = await evalJs(`new Promise(res => setTimeout(() => res('ticked'), 2600))`);
  check("waited across the 2s poll tick", pollTick === "ticked");
  const survived = await evalJs(`(() => { const el = document.querySelector('${rowSel} input'); return !!el && document.activeElement === el; })()`);
  check("input survives a poll tick (no teardown, focus kept)", survived);
  await send("Input.imeSetComposition", { text: "", selectionStart: 0, selectionEnd: 0 }); // commit composition
  await keyDown("Escape"); // leave editing cleanly
  await sleep(500);

  // --- AC7: the pinned main row never opens the editor ------------------------
  const mainBox = await boxOf(`#wt-list .wt-item-main`);
  if (mainBox) {
    const patchesBefore = patches.length;
    await dblClickAt(mainBox.x, mainBox.y);
    await sleep(300);
    const mainInput = await evalJs(`!!document.querySelector('#wt-list .wt-item-main input')`);
    check("double-click on the main row opens no editor", mainInput === false && patches.length === patchesBefore);
  } else {
    check("main row present for the negative check", false, "no .wt-item-main box");
  }

  // --- AC8: at most one rename input ------------------------------------------
  const box6 = await boxOf(rowSel);
  await dblClickAt(box6.x, box6.y);
  await sleep(250);
  const inputCount = await evalJs(`document.querySelectorAll('#wt-list input, #tabs-container input').length`);
  check("at most one rename input exists while editing", inputCount === 1, `inputs=${inputCount}`);
  await keyDown("Escape");
  await sleep(300);

  // --- AC2: reload persistence (fresh renderer = "user reopens the page") -----
  // NOTE: CDP Page.navigate hangs in this headless setup once a CDP-focused
  // input has been opened on the page (bisected: dblclick alone triggers it,
  // no commit involved — a harness quirk, not a product defect), and a
  // Page.reload on the same target wedges that target's session in this minimal
  // client. A brand-new target (fresh renderer) is both unaffected and the
  // faithful model of "the user reloaded / reopened the page".
  {
    const created2 = await send("Target.createTarget", { url: `${BASE}/?token=${TOKEN}` }, 15000);
    const targetId = created2.result.targetId;
    await sleep(2500);
    const list = await (await fetch("http://127.0.0.1:9335/json/list")).json();
    const page = list.find((t) => t.type === "page" && t.id === targetId) || list.find((t) => t.type === "page");
    const ws2 = new WebSocket(page.webSocketDebuggerUrl);
    await new Promise((res, rej) => { ws2.onopen = res; ws2.onerror = rej; });
    let id2 = 0;
    const pend2 = new Map();
    ws2.onmessage = (ev) => {
      const m = JSON.parse(ev.data);
      if (m.id && pend2.has(m.id)) { pend2.get(m.id)(m); pend2.delete(m.id); }
    };
    const eval2 = (expression) => new Promise((res, rej) => {
      const id = ++id2;
      const t = setTimeout(() => rej(new Error("fresh-target eval timed out")), 15000);
      pend2.set(id, (m) => { clearTimeout(t); res(m.result?.result?.value); });
      ws2.send(JSON.stringify({ id, method: "Runtime.evaluate", params: { expression, returnByValue: true } }));
    });
    const rowText2 = await eval2(`document.querySelector('#wt-list .wt-item:not(.wt-item-main) .wt-name').textContent`);
    check("name survives a page reload (fresh renderer reads it back)", rowText2 === blurredName, rowText2);
    ws2.close();
    await send("Target.closeTarget", { targetId }, 5000);
  }
  const apiReadback = await api("GET", "/api/worktrees");
  const wtApi = apiReadback.body.worktrees.find((w) => w.id === wtId);
  check("GET /api/worktrees reads the last committed name back", wtApi?.name === blurredName, JSON.stringify(wtApi));

  // --- git-facing invariants after the whole session ---------------------------
  const final = await api("GET", "/api/worktrees");
  const wtFinal = final.body.worktrees.find((w) => w.id === wtId);
  check("final record: branch unchanged, name last-committed value",
    !!wtFinal && wtFinal.branch === created.body.branch && wtFinal.name === blurredName, JSON.stringify(wtFinal));
} catch (err) {
  check("E2E harness error: " + err.message, false);
} finally {
  const failed = results.filter((r) => !r.ok);
  console.log(`\n===== E2E SUMMARY: ${results.length - failed.length}/${results.length} PASS =====`);
  for (const f of failed) console.log(`FAILED: ${f.name} — ${f.detail}`);
  try { ws.close(); } catch {}
  try { chrome.kill("SIGKILL"); } catch {}
  try { daemon.kill("SIGKILL"); } catch {}
  try { execSync("pkill -f 'remote-debugging-port=9335' || true"); } catch {}
  process.exit(failed.length ? 1 : 0);
}
