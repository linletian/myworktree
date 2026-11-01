// 诊断浮层可达性校验（issue #116 A0 工具修复的验收证据）。
//
// A0 采证中发现的缺陷：浮层原先固定 72vh 且按钮排在明细 JSON 之后，
// iPad 软键盘弹出（visualViewport.height≈274/633）时主按钮整排被埋掉。
// 本脚本用 headless Chrome + CDP 把视口高度压到这些实测值，断言：
//   1) 主按钮排（Magic bar / Soft keyboard / Capture）在可视区内；
//   2) 每个主按钮 elementFromPoint 命中自身或其子孙（真的能点，不是被遮住）；
//   3) 浮层不越界（rect 在可视区内）；
//   4) 次要控件默认折叠（面板高度 ≤ 可视区）。
//
// 用法（人工证据脚本，不进 CI；只用 Node 内建模块）：
//   node docs/plans/fix-issue116/evidence/panel-check.mjs \
//       --chrome /usr/bin/google-chrome --url "http://127.0.0.1:38231/?token=...&viewport-debug=1"
//
// 注意：headless Chrome 不模拟 iOS 软键盘，这里用实测的 visualViewport 高度
// 覆盖视口来复现"可视区被压扁"，证明的是浮层自身的布局契约，
// 不能替代 iPadOS 真机。

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
const URL = arg("url", process.env.MW_TEST_URL || "");
const WIDTH = Number(arg("width", "1194"));
// A0 实测的 visualViewport 高度：全高 702、地址栏/浮层 633.5、软键盘 274。
const HEIGHTS = (arg("heights", "702,633.5,545,495.9,274")).split(",").map(Number);
const DEBUG_PORT = Number(arg("port", "0")) || 9333;
const here = dirname(fileURLToPath(import.meta.url));
const outDir = join(here, "panel-visibility");
mkdirSync(outDir, { recursive: true });

if (!URL) { console.error("need --url"); process.exit(2); }

const sleep = (ms) => new Promise((r) => setTimeout(r, ms));

// 证据文件不得含访问令牌：落盘与日志统一脱敏 query 里的 token。
const redactUrl = (u) => String(u).replace(/([?&]token=)[^&]*/gi, "$1<redacted>");

const chrome = spawn(CHROME, [
    "--headless=new", "--disable-gpu", "--no-first-run", "--no-default-browser-check",
    "--remote-debugging-port=" + DEBUG_PORT, "--window-size=" + WIDTH + ",702",
    "about:blank",
], { stdio: "ignore" });

let ws = null;
let idSeq = 0;
const pending = new Map();

function send(method, params = {}, sessionId) {
    const id = ++idSeq;
    return new Promise((resolve, reject) => {
        pending.set(id, { resolve, reject });
        ws.send(JSON.stringify({ id, method, params, ...(sessionId ? { sessionId } : {}) }));
    });
}

async function attach() {
    for (let i = 0; i < 60; i++) {
        try {
            const res = await fetch(`http://127.0.0.1:${DEBUG_PORT}/json/version`);
            const info = await res.json();
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
                    if (msg.error) p.reject(new Error(msg.error.message));
                    else p.resolve(msg.result);
                }
            });
            return sock;
        } catch (_) {
            await sleep(500);
        }
    }
    throw new Error("chrome devtools endpoint not reachable");
}

const PROBE = `(() => {
    const panel = document.getElementById("viewport-debug-panel");
    if (!panel) return { error: "panel missing" };
    const primary = document.getElementById("viewport-debug-row-primary");
    const sec = document.getElementById("viewport-debug-row-secondary");
    const pr = panel.getBoundingClientRect();
    const visH = visualViewport ? visualViewport.height : innerHeight;
    const labels = ["Magic bar", "Soft keyboard", "Capture", "Move"];
    const buttons = [...(primary ? primary.querySelectorAll("button") : [])];
    const hits = [];
    for (const b of buttons) {
        const r = b.getBoundingClientRect();
        const cx = r.left + r.width / 2, cy = r.top + r.height / 2;
        const top = document.elementFromPoint(cx, cy);
        hits.push({
            label: b.textContent.trim(),
            rect: { top: +r.top.toFixed(1), bottom: +r.bottom.toFixed(1), h: +r.height.toFixed(1) },
            inVisible: r.bottom <= visH + 1 && r.top >= -1,
            clickable: !!top && (top === b || b.contains(top)),
        });
    }
    return {
        labels: labels.map((t) => hits.some((h) => h.label === t)),
        hits,
        panel: { top: +pr.top.toFixed(1), bottom: +pr.bottom.toFixed(1), height: +pr.height.toFixed(1), right: +pr.right.toFixed(1) },
        visH: +visH.toFixed(1),
        innerHeight,
        secondaryVisible: !!sec && getComputedStyle(sec).display !== "none",
        panelMaxHeight: getComputedStyle(panel).maxHeight,
    };
})()`;

const results = [];
let failed = 0;
try {
    ws = await attach();
    const { targetId } = await send("Target.createTarget", { url: "about:blank" });
    const { sessionId } = await send("Target.attachToTarget", { targetId, flatten: true });
    await send("Runtime.enable", {}, sessionId);
    await send("Page.enable", {}, sessionId);

    for (const height of HEIGHTS) {
        await send("Emulation.setDeviceMetricsOverride", {
            width: WIDTH, height: Math.round(height * 1000) / 1000 ? Math.round(height) : Math.round(height),
            deviceScaleFactor: 2, mobile: true,
        }, sessionId);
        await send("Emulation.setVisualViewportSize", {
            width: WIDTH, height: height,
        }, sessionId).catch(() => {});
        if (results.length === 0) {
            await send("Page.navigate", { url: URL }, sessionId);
            await sleep(3000);
        }
        await sleep(900);
        const out = await send("Runtime.evaluate", { expression: PROBE, returnByValue: true }, sessionId);
        const value = out.result && out.result.value ? out.result.value : { error: "no value" };
        value.requestedHeight = height;
        results.push(value);

        const shot = await send("Page.captureScreenshot", { format: "png" }, sessionId);
        if (shot.data) writeFileSync(join(outDir, `h${Math.round(height)}.png`), Buffer.from(shot.data, "base64"));

        const problems = [];
        if (value.error) problems.push(value.error);
        for (const need of ["Magic bar", "Soft keyboard", "Capture"]) {
            const idx = ["Magic bar", "Soft keyboard", "Capture"].indexOf(need);
            if (!value.labels || !value.labels[idx]) problems.push("primary button missing: " + need);
        }
        for (const h of value.hits || []) {
            if (!h.inVisible) problems.push(`${h.label} outside visible viewport (bottom=${h.rect.bottom} visH=${value.visH})`);
            if (!h.clickable) problems.push(`${h.label} not hit-testable (occluded)`);
        }
        if (value.panel && value.panel.bottom > (value.visH || 0) + 1) problems.push(`panel bottom ${value.panel.bottom} > visible ${value.visH}`);
        if (problems.length) failed++;
        console.log(`--- vv.height≈${height} ---`);
        console.log(`  panel ${value.panel ? value.panel.height + "px @top " + value.panel.top + " bottom " + value.panel.bottom : "?"} (maxHeight ${value.panelMaxHeight}) secondary=${value.secondaryVisible}`);
        for (const h of value.hits || []) console.log(`  ${h.clickable && h.inVisible ? "OK  " : "FAIL"} ${h.label.padEnd(14)} top=${h.rect.top} bottom=${h.rect.bottom} h=${h.rect.h}`);
        console.log(problems.length ? "  PROBLEMS: " + problems.join("; ") : "  all primary controls reachable");
    }
} finally {
    try { ws && ws.close(); } catch (_) { /* ignore */ }
    chrome.kill("SIGTERM");
}

const file = join(outDir, "RESULTS.json");
writeFileSync(file, JSON.stringify({ url: redactUrl(URL), width: WIDTH, results }, null, 2));
console.log(`\n${failed ? "FAIL: " + failed + " viewport(s) with problems" : "PASS: primary controls reachable at all " + HEIGHTS.length + " measured viewport heights"}`);
console.log("evidence: " + outDir);
process.exit(failed ? 1 : 0);
