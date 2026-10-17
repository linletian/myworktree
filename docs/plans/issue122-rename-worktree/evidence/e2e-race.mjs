// Issue #122 B-F1 race reproduction: double-click a NON-active row while the
// /api/worktree/diverged response it triggered (synchronously on click 1, via
// selectWorktree -> fetchDivergeSingle) is still in flight. Without the
// fetchDivergeSingle guard, the landing response rebuilds #wt-list through the
// editing-state branch — focus, selection, half-typed text and the IME
// composition are all destroyed. This script holds the diverged responses via
// the CDP Fetch domain, drives the real double-click, and checks survival.
import { spawn, execSync } from "node:child_process";
import { mkdtempSync, writeFileSync, mkdirSync } from "node:fs";
import { tmpdir } from "node:os";
import { join } from "node:path";
import { setTimeout as sleep } from "node:timers/promises";

const PORT = 39778;
const TOKEN = "e2e-token";
const BASE = `http://127.0.0.1:${PORT}`;
const ROOT = mkdtempSync(join(tmpdir(), "issue122-race-"));

const results = [];
const check = (name, ok, detail = "") => {
  results.push({ name, ok, detail });
  console.log(`${ok ? "PASS" : "FAIL"}  ${name}${detail ? ` — ${detail}` : ""}`);
};

mkdirSync(ROOT, { recursive: true });
writeFileSync(join(ROOT, "README.md"), "seed\n");
const git = (...args) => execSync(["git", ...args].join(" "), { cwd: ROOT, stdio: "pipe" }).toString();
git("init"); git("config user.name E2E"); git("config user.email e2e@example.com");
git("add README.md"); git("commit -m init");

const daemon = spawn("/tmp/issue122-e2e/myworktree", [
  "--listen", `127.0.0.1:${PORT}`, "--portal-port", "0", "--auth", TOKEN, "--worktrees-dir", "data",
], { cwd: ROOT, stdio: ["ignore", "pipe", "pipe"] });

async function api(method, path, body) {
  const res = await fetch(BASE + path, {
    method,
    headers: { "Content-Type": "application/json", "Authorization": `Bearer ${TOKEN}` },
    body: body ? JSON.stringify(body) : undefined,
  });
  return { status: res.status, body: await res.json().catch(() => null) };
}
async function waitUp() {
  for (let i = 0; i < 60; i++) {
    try { if ((await api("GET", "/api/main")).status === 200) return; } catch {}
    await sleep(250);
  }
  throw new Error("daemon never came up");
}

const chrome = spawn("google-chrome", [
  "--headless=new", "--no-sandbox", "--disable-gpu", "--remote-debugging-port=9334", "about:blank",
], { stdio: "ignore" });
async function targetWs() {
  for (let i = 0; i < 50; i++) {
    try {
      const list = await (await fetch("http://127.0.0.1:9334/json/list")).json();
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
const heldDiverged = [];
ws.addEventListener("message", (ev) => {
  const m = JSON.parse(ev.data);
  if (m.id && pending.has(m.id)) { pending.get(m.id)(m); pending.delete(m.id); }
  // Fetch domain: catch diverged responses and HOLD them (do not fulfill yet).
  if (m.method === "Fetch.requestPaused") {
    const p = m.params;
    if (p.request.url.includes("/diverged")) {
      heldDiverged.push(p);
      return; // leave it paused
    }
    ws.send(JSON.stringify({ id: ++msgId, method: "Fetch.continueRequest", params: { requestId: p.requestId } }));
  }
});
const send = (method, params = {}) => new Promise((res) => {
  const id = ++msgId;
  pending.set(id, res);
  ws.send(JSON.stringify({ id, method, params }));
});
const evalJs = async (expression) => {
  const r = await send("Runtime.evaluate", { expression, returnByValue: true, awaitPromise: true });
  if (r.result?.exceptionDetails) throw new Error("page eval failed: " + JSON.stringify(r.result.exceptionDetails));
  return r.result?.result?.value;
};
const clickAt = async (x, y, clickCount = 1) => {
  await send("Input.dispatchMouseEvent", { type: "mousePressed", x, y, button: "left", clickCount, buttons: 1 });
  await send("Input.dispatchMouseEvent", { type: "mouseReleased", x, y, button: "left", clickCount, buttons: 0 });
};
const boxOf = async (sel) => evalJs(`(() => { const el = document.querySelector(${JSON.stringify(sel)}); if (!el) return null; const r = el.getBoundingClientRect(); return {x: r.x + r.width/2, y: r.y + r.height/2}; })()`);

try {
  await waitUp();
  const created = await api("POST", "/api/worktrees", { task_description: "race rename" });
  check("worktree created", created.status === 201, created.body?.id);
  const wtId = created.body.id;

  await send("Page.enable");
  // Hold every /api/*diverged response at the Response stage.
  await send("Fetch.enable", { patterns: [{ urlPattern: "*diverged*", requestStage: "Response" }] });
  await send("Network.enable");
  await send("Page.navigate", { url: `${BASE}/?token=${TOKEN}` });
  await sleep(2000);

  const rowSel = `#wt-list .wt-item:not(.wt-item-main)`;
  const box = await boxOf(rowSel);
  check("row rendered", !!box);

  // Double-click the row: click 1 activates it (selectWorktree fires the
  // diverged request synchronously — now HELD), the dblclick builds the input.
  await clickAt(box.x, box.y, 1);
  await sleep(20);
  await clickAt(box.x, box.y, 2);
  await sleep(300);

  const inputOpen = await evalJs(`!!document.querySelector('${rowSel} input')`);
  check("dblclick opened the rename input", inputOpen);
  const focused = await evalJs(`document.activeElement && document.activeElement.id === 'wt-rename-input-${wtId}'`);
  check("input focused", focused);

  // Type a partial name and open a real IME composition. The composition text
  // is INSERTED at the caret, so the expected value is whatever the field
  // holds right after imeSetComposition — read it once and demand it back
  // verbatim after the delayed responses land.
  await evalJs(`(() => { const el = document.querySelector('${rowSel} input'); el.value = '半成'; return 1; })()`);
  await send("Input.imeSetComposition", { text: "半成拼音", selectionStart: 4, selectionEnd: 4 });
  const snap = async () => evalJs(`(() => { const el = document.querySelector('${rowSel} input'); return JSON.stringify({has: !!el, focused: document.activeElement === el, value: el ? el.value : null}); })()`);
  const composing = await snap();
  const parsed = JSON.parse(composing);
  check("half-typed text present, focus in the field", parsed.has && parsed.focused && parsed.value.length > 0, composing);
  check("diverged responses are held in flight", heldDiverged.length >= 1, `held=${heldDiverged.length}`);

  // NOW release the held responses — the B-F1 window.
  for (const p of heldDiverged.splice(0)) {
    const body = JSON.stringify({ items: {} });
    const b64 = Buffer.from(body, "utf8").toString("base64");
    await send("Fetch.fulfillRequest", {
      requestId: p.requestId,
      responseCode: 200,
      responseHeaders: [{ name: "Content-Type", value: "application/json" }],
      body: b64,
    });
  }
  await sleep(1200); // let both the diverge tail and a poll tick land

  const survived = await snap();
  check("B-F1: input, focus and composition text survive the delayed diverged response", survived === composing, `before=${composing} after=${survived}`);
  const stillOne = await evalJs(`document.querySelectorAll('#wt-list input, #tabs-container input').length`);
  check("exactly one input (no duplicate from the rebuild)", stillOne === 1, `inputs=${stillOne}`);

  // The row still commits cleanly afterwards.
  await evalJs(`(() => { const el = document.querySelector('${rowSel} input'); el.value = 'after-race'; return 1; })()`);
  await send("Input.dispatchKeyEvent", { type: "keyDown", key: "Enter", code: "Enter", windowsVirtualKeyCode: 13 });
  await send("Input.dispatchKeyEvent", { type: "keyUp", key: "Enter", code: "Enter" });
  await sleep(900);
  const final = await api("GET", "/api/worktrees");
  const wt = final.body.worktrees.find((w) => w.id === wtId);
  check("rename still commits after the race", wt?.name === "after-race", JSON.stringify(wt));
} catch (err) {
  check("harness error: " + err.message, false);
} finally {
  const failed = results.filter((r) => !r.ok);
  console.log(`\n===== RACE E2E: ${results.length - failed.length}/${results.length} PASS =====`);
  try { ws.close(); } catch {}
  chrome.kill();
  daemon.kill("SIGTERM");
  await sleep(400);
  try { daemon.kill("SIGKILL"); } catch {}
  process.exit(failed.length ? 1 : 0);
}
