// Minimal CDP driver (Node built-ins only) to probe real browser dblclick
// semantics when the first click replaces the DOM (mirrors selectWorktree ->
// render() -> renderSidebar() rebuilding #wt-list innerHTML).
import { spawn } from "node:child_process";
import { setTimeout as sleep } from "node:timers/promises";

const chrome = spawn("google-chrome", [
  "--headless=new",
  "--no-sandbox",
  "--disable-gpu",
  "--remote-debugging-port=9333",
  "about:blank",
], { stdio: "ignore" });

async function getTargetWs() {
  for (let i = 0; i < 50; i++) {
    try {
      const res = await fetch("http://127.0.0.1:9333/json/list");
      const list = await res.json();
      const page = list.find((t) => t.type === "page");
      if (page) return page.webSocketDebuggerUrl;
    } catch {}
    await sleep(200);
  }
  throw new Error("chrome devtools never came up");
}

const ws = new WebSocket(await getTargetWs());
await new Promise((res, rej) => { ws.onopen = res; ws.onerror = rej; });

let msgId = 0;
const pending = new Map();
ws.onmessage = (ev) => {
  const m = JSON.parse(ev.data);
  if (m.id && pending.has(m.id)) {
    pending.get(m.id)(m);
    pending.delete(m.id);
  }
};
const send = (method, params = {}) =>
  new Promise((res) => {
    const id = ++msgId;
    pending.set(id, res);
    ws.send(JSON.stringify({ id, method, params }));
  });

async function evalJs(expr) {
  const r = await send("Runtime.evaluate", { expression: expr, returnByValue: true });
  return r.result?.result?.value;
}

await send("Page.enable");
await send("Page.navigate", { url: "file:///tmp/dblclick-probe/test.html" });
await sleep(600);

async function doubleClick(targetId, mode) {
  await evalJs(`window.log = []; window.mode = ${JSON.stringify(mode)}; 'ok'`);
  const box = await evalJs(`(() => { const r = document.getElementById(${JSON.stringify(targetId)}).getBoundingClientRect(); return {x: r.x + r.width/2, y: r.y + r.height/2}; })()`);
  for (const clickCount of [1, 2]) {
    await send("Input.dispatchMouseEvent", { type: "mousePressed", x: box.x, y: box.y, button: "left", clickCount, buttons: 1 });
    await send("Input.dispatchMouseEvent", { type: "mouseReleased", x: box.x, y: box.y, button: "left", clickCount, buttons: 0 });
    await sleep(30);
  }
  await sleep(150);
  return await evalJs("window.log.join(' | ')");
}

console.log("REPLACE-mode dblclick on non-active row #b:", await doubleClick("b", "replace"));
console.log("KEEP-mode    dblclick on same row     #b:", await doubleClick("b", "keep"));
console.log("KEEP-mode    dblclick on same row     #a:", await doubleClick("a", "keep"));

ws.close();
chrome.kill();
process.exit(0);
