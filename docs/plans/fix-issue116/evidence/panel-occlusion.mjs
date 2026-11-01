// 诊断浮层 vs quick-keys 控制列遮挡校验（issue #116 A0 工具修复证据）。
//
// A0 采证反馈：诊断浮层 fixed 右上，压住 #terminal-controls（内含
// #quick-keys-toggle），iPad 上点不到 quick-keys。本脚本量化四个角落
// 以及 Move 循环后的遮挡情况：
//   - #terminal-controls 与浮层的矩形是否相交；
//   - 控制列按钮中心的 elementFromPoint 命中的是不是该按钮本身。
//
// 无活动实例时控制列是 hidden 的；本脚本会在页面上临时 unhide 它来量几何，
// 这是**测试工具行为**，不修改产品代码、不写入样式到磁盘。输出中显式标注 harness-forced。
//
// 用法：
//   node docs/plans/fix-issue116/evidence/panel-occlusion.mjs \
//       --chrome /usr/bin/google-chrome --url "http://host:port/?token=...&viewport-debug=1"

import { spawn } from "node:child_process";
import { mkdirSync, writeFileSync } from "node:fs";
import { dirname, join } from "node:path";
import { fileURLToPath } from "node:url";

const args = process.argv.slice(2);
const arg = (name, fallback = "") => {
    const i = args.indexOf("--" + name);
    return i >= 0 && args[i + 1] ? args[i + 1] : fallback;
};
const CHROME = arg("chrome", process.env.CHROME_BIN || "/usr/bin/google-chrome");
const URL = arg("url", "");
const WIDTH = Number(arg("width", "1194"));
const HEIGHT = Number(arg("height", "702"));
const PORT = Number(arg("port", "9666"));
const here = dirname(fileURLToPath(import.meta.url));
const outDir = join(here, "panel-occlusion");
mkdirSync(outDir, { recursive: true });
if (!URL) { console.error("need --url"); process.exit(2); }
const sleep = (ms) => new Promise((r) => setTimeout(r, ms));

// 证据文件不得含访问令牌：落盘与日志统一脱敏 query 里的 token。
const redactUrl = (u) => String(u).replace(/([?&]token=)[^&]*/gi, "$1<redacted>");

const chrome = spawn(CHROME, ["--headless=new", "--disable-gpu", "--no-first-run", "--remote-debugging-port=" + PORT, "--window-size=" + WIDTH + "," + HEIGHT, "about:blank"], { stdio: "ignore" });
let ws = null, idSeq = 0;
const pending = new Map();
const send = (method, params = {}, sessionId) => new Promise((resolve, reject) => {
    const id = ++idSeq;
    pending.set(id, { resolve, reject });
    ws.send(JSON.stringify({ id, method, params, ...(sessionId ? { sessionId } : {}) }));
});

async function attach() {
    for (let i = 0; i < 60; i++) {
        try {
            const info = await (await fetch(`http://127.0.0.1:${PORT}/json/version`)).json();
            const sock = new WebSocket(info.webSocketDebuggerUrl);
            await new Promise((resolve, reject) => {
                sock.addEventListener("open", resolve, { once: true });
                sock.addEventListener("error", reject, { once: true });
            });
            sock.addEventListener("message", (ev) => {
                const msg = JSON.parse(ev.data);
                if (msg.id && pending.has(msg.id)) {
                    const p = pending.get(msg.id);
                    pending.delete(msg.id);
                    if (msg.error) p.reject(new Error(msg.error.message)); else p.resolve(msg.result);
                }
            });
            return sock;
        } catch (_) { await sleep(500); }
    }
    throw new Error("devtools endpoint unreachable");
}

const MEASURE = `(() => {
    const panel = document.getElementById("viewport-debug-panel");
    const col = document.getElementById("terminal-controls");
    const toggle = document.getElementById("quick-keys-toggle");
    if (!panel || !col) return { error: "panel or terminal-controls missing" };
    // harness-forced：无活动实例时控制列 hidden，仅为量几何而临时显形。
    let forced = false;
    if (col.hidden) { col.hidden = false; forced = true; }
    if (getComputedStyle(col).display === "none") { col.style.display = "flex"; forced = true; }
    const cr = col.getBoundingClientRect();
    const pr = panel.getBoundingClientRect();
    const overlap = !(cr.right <= pr.left || cr.left >= pr.right || cr.bottom <= pr.top || cr.top >= pr.bottom);
    const hitOf = (el) => {
        if (!el) return null;
        const r = el.getBoundingClientRect();
        const top = document.elementFromPoint(r.left + r.width / 2, r.top + r.height / 2);
        return { label: (el.id || el.textContent || "").trim().slice(0, 24), hit: !!top && (top === el || el.contains(top)), blocker: top ? (top.id || top.tagName + "." + (top.className || "").toString().slice(0, 24)) : "none" };
    };
    return {
        forced,
        corner: panel.style.top === "auto" ? "bottom-*" : (panel.style.left === "auto" ? "top-right" : "top-left"),
        panelStyle: { top: panel.style.top, left: panel.style.left, right: panel.style.right, bottom: panel.style.bottom },
        col: { left: +cr.left.toFixed(1), right: +cr.right.toFixed(1), top: +cr.top.toFixed(1), bottom: +cr.bottom.toFixed(1) },
        panel: { left: +pr.left.toFixed(1), right: +pr.right.toFixed(1), top: +pr.top.toFixed(1), bottom: +pr.bottom.toFixed(1) },
        overlap,
        toggleHit: hitOf(toggle),
        firstColBtnHit: hitOf(col.querySelector("button")),
        visH: visualViewport ? +visualViewport.height.toFixed(1) : innerHeight,
    };
})()`;

const CORNERS = ["tr", "tl", "br", "bl"];
const results = [];
let bad = 0;
try {
    ws = await attach();
    const { targetId } = await send("Target.createTarget", { url: "about:blank" });
    const { sessionId } = await send("Target.attachToTarget", { targetId, flatten: true });
    await send("Runtime.enable", {}, sessionId);
    await send("Page.enable", {}, sessionId);
    await send("Emulation.setDeviceMetricsOverride", { width: WIDTH, height: HEIGHT, deviceScaleFactor: 2, mobile: true }, sessionId);
    await send("Page.navigate", { url: URL + (URL.includes("?") ? "&" : "?") + "viewport-debug=1&viewport-debug-pos=tr" }, sessionId);
    await sleep(3200);

    const corners = CORNERS;
    for (const corner of corners) {
        // 通过浮层自身的 Move 按钮循环，走真实用户路径；第一次直接刷新到目标角落。
        if (corner !== "tr") {
            const clicked = await send("Runtime.evaluate", {
                expression: `(() => { const b=[...document.querySelectorAll("#viewport-debug-row-primary button")].find(x=>x.textContent.trim().startsWith("Move")); if(!b) return "no-move-btn"; b.dispatchEvent(new PointerEvent("pointerdown",{bubbles:true,cancelable:true})); return b.textContent.trim(); })()`,
                returnByValue: true,
            }, sessionId);
            await sleep(500);
            console.log(`\n--- corner=${corner} (Move → ${clicked.result.value}) ---`);
        } else {
            console.log(`\n--- corner=${corner} (URL param) ---`);
        }
        const out = await send("Runtime.evaluate", { expression: MEASURE, returnByValue: true }, sessionId);
        const v = out.result && out.result.value ? out.result.value : { error: "no value" };
        v.cornerWanted = corner;
        results.push(v);
        if (v.error) { console.log("  ERROR " + v.error); bad++; continue; }
        const clickable = v.toggleHit && v.toggleHit.hit;
        console.log(`  panel ${v.panel.left}…${v.panel.right} × ${v.panel.top}…${v.panel.bottom} (visH=${v.visH}, ${JSON.stringify(v.panelStyle)})`);
        console.log(`  controls ${v.col.left}…${v.col.right} × ${v.col.top}…${v.col.bottom}${v.forced ? " [harness-forced visible]" : ""}`);
        console.log(`  quick-keys-toggle hit=${clickable ? "YES" : "NO (blocker=" + (v.toggleHit ? v.toggleHit.blocker : "?") + ")"}  机身几何相交=${v.overlap}（仅供参考：控制列很高，底部角落与机身相交是正常的）`);
        if (!clickable) bad++;
        const shot = await send("Page.captureScreenshot", { format: "png" }, sessionId);
        if (shot.data) writeFileSync(join(outDir, corner + ".png"), Buffer.from(shot.data, "base64"));
    }
} finally {
    try { ws && ws.close(); } catch (_) { /* ignore */ }
    chrome.kill("SIGTERM");
}
writeFileSync(join(outDir, "RESULTS.json"), JSON.stringify({ url: redactUrl(URL), width: WIDTH, height: HEIGHT, results }, null, 2));
console.log(`\n口径：验收 = 四个角落下 quick-keys 入口都能命中（elementFromPoint）；与控制列机身的几何相交仅作参考。`);
console.log(bad ? `FAIL: ${bad} 个角落下 quick-keys 入口不可点` : `PASS: ${CORNERS.join("/")} 四角下 quick-keys 入口均可命中（默认 tr 靠预留宽度避让）`);
process.exit(bad ? 1 : 0);
