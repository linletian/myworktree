// A0 原始快照归堆分析（issue #116）。
// 读 evidence/raw/<session>.jsonl，按几何签名聚类，输出可直接回填
// DATA-COLLECTION.md 的判定表。只读本地文件，不访问网络。
//
//   node docs/plans/fix-issue116/evidence/analyze-a0.mjs [session]

import { readFileSync, readdirSync, existsSync } from "node:fs";
import { dirname, join } from "node:path";
import { fileURLToPath } from "node:url";

const here = dirname(fileURLToPath(import.meta.url));
const rawDir = join(here, "raw");
const wanted = process.argv[2] || "";
if (!existsSync(rawDir)) { console.error("no raw dir"); process.exit(1); }
const files = readdirSync(rawDir).filter((f) => f.endsWith(".jsonl") && f.startsWith(wanted));
if (!files.length) { console.error("no raw jsonl matching", wanted || "*"); process.exit(1); }

const ev = [];
for (const f of files) {
    for (const line of readFileSync(join(rawDir, f), "utf8").split("\n")) {
        if (!line.trim()) continue;
        const e = JSON.parse(line);
        ev.push({ seq: e.seq, ...e.snapshot });
    }
}
ev.sort((a, b) => a.seq - b.seq);

const f1 = (n) => (typeof n === "number" ? n.toFixed(1) : "?");
const fmtPx = (n) => (typeof n === "number" ? n.toFixed(2) + "px" : "?");

// 聚类签名：缩放 / 是否钉高 / 遮挡分量 / 方向 / kind / 快捷键 / web 面板
function bucket(s) {
    const vv = s.vv || {};
    const occl = typeof s.innerHeight === "number" && typeof vv.height === "number"
        ? Math.round((s.innerHeight - vv.height) * 10) / 10 : 0;
    return [
        "scale=" + vv.scale,
        s.app.inlineHeight ? "pinned" : "css",
        "occl=" + occl,
        (s.device.orientation || "").replace("-primary", ""),
        "kind=" + s.ui.kind,
        s.ui.quickKeysOpen ? "QK" : "-",
        s.webPanel && s.webPanel.height ? "webPanel" : "-",
    ].join(" ");
}

const groups = new Map();
for (const s of ev) {
    const b = bucket(s);
    let g = groups.get(b);
    if (!g) {
        g = { n: 0, seqs: [], hints: new Set(), padBot: new Set(), rootBg: new Set(), xtermGap: [], err: [], vvH: new Set(), wsBot: new Set() };
        groups.set(b, g);
    }
    g.n++;
    g.seqs.push(s.seq);
    if (s.reason === "magic-hint") g.hints.add("magic");
    if (s.reason === "keyboard-hint") g.hints.add("softkb");
    if (s.reason === "manual") g.hints.add("manual");
    g.padBot.add(s.app.paddingBottom);
    g.rootBg.add((s.document.bodyBackground === "rgba(0, 0, 0, 0)" ? "transparent" : s.document.bodyBackground)
        + "/" + (s.document.htmlBackground === "rgba(0, 0, 0, 0)" ? "transparent" : s.document.htmlBackground)
        + "/" + (s.app.background === "rgba(0, 0, 0, 0)" ? "transparent" : s.app.background));
    g.vvH.add(f1(s.vv.height));
    g.wsBot.add(f1(s.workspace.bottom));
    if (typeof s.app.bottom === "number" && typeof s.vv.height === "number") {
        g.err.push((s.vv.offsetTop || 0) + s.vv.height - s.app.bottom);
    }
    const xr = s.xtermRows && s.xtermRows.bottom;
    if (typeof xr === "number" && typeof s.workspace.bottom === "number") g.xtermGap.push(s.workspace.bottom - xr);
}

function span(seqs) {
    const u = [...new Set(seqs)].sort((a, b) => a - b);
    if (!u.length) return "-";
    let runs = [], start = u[0], prev = u[0];
    for (const s of u.slice(1)) {
        if (s === prev + 1) { prev = s; continue; }
        runs.push(start === prev ? `${start}` : `${start}–${prev}`);
        start = prev = s;
    }
    runs.push(start === prev ? `${start}` : `${start}–${prev}`);
    return runs.slice(0, 6).join(",") + (runs.length > 6 ? `,…(+${runs.length - 6})` : "");
}

const rows = [...groups.entries()].sort((a, b) => b[1].n - a[1].n);
console.log(`## 会话 ${files.join(",")}  事件 ${ev.length}  seq ${ev[0].seq}–${ev.at(-1).seq}\n`);
console.log("### 几何聚类（按事件数排序）\n");
console.log("| n | 签名 | vv.height | padBot | 底边误差 vvBottom−appBot | workspace−xtermRows | 根背景链 | seq 区间 | hint |");
console.log("| --- | --- | --- | --- | --- | --- | --- | --- | --- |");
for (const [b, g] of rows) {
    const fmt = (arr) => {
        if (!arr.length) return "-";
        const mn = Math.min(...arr), mx = Math.max(...arr);
        return mn === mx ? f1(mn) : `${f1(mn)}…${f1(mx)}`;
    };
    console.log(`| ${g.n} | \`${b}\` | ${[...g.vvH].slice(0, 3).join(",")}${g.vvH.size > 3 ? "…" : ""} | ${[...g.padBot].join(",")} | ${fmt(g.err)} | ${fmt(g.xtermGap)} | ${[...g.rootBg].slice(0, 2).join(" ")} | ${span(g.seqs)} | ${[...g.hints].join(",") || "-"} |`);
}

const hinted = ev.filter((s) => s.reason === "magic-hint" || s.reason === "keyboard-hint");
console.log("\n### 人工 hint 事件（分类的唯一直接证据）\n");
console.log("| seq | hint | vvH | vvTop | inner | appBot | inline | padBot | wsBot | termBot | kind | status |");
console.log("| --- | --- | --- | --- | --- | --- | --- | --- | --- | --- | --- | --- |");
for (const s of hinted) {
    console.log(`| ${s.seq} | ${s.reason} | ${f1(s.vv.height)} | ${f1(s.vv.offsetTop)} | ${s.innerHeight} | ${f1(s.app.bottom)} | ${s.app.inlineHeight || "-"} | ${s.app.paddingBottom} | ${f1(s.workspace.bottom)} | ${f1(s.terminal && s.terminal.bottom)} | ${s.ui.kind} | ${s.ui.status} |`);
}

console.log("\n### 覆盖矩阵自动判定\n");
const cov = (pred) => ev.filter(pred).length;
const table = [
    ["静止 scale=1 未钉高（外壳基线）", cov((s) => s.vv.scale === 1 && !s.app.inlineHeight && !s.vv.offsetTop)],
    ["钉高态（有遮挡补偿）", cov((s) => !!s.app.inlineHeight)],
    ["pan / offsetTop>0.5（坐标线证据）", cov((s) => (s.vv.offsetTop || 0) > 0.5)],
    ["负 app.top（拖动平移）", cov((s) => (s.app.top || 0) < -0.5)],
    ["缩放豁免 scale≠1", cov((s) => s.vv.scale !== 1)],
    ["横屏", cov((s) => (s.device.orientation || "").startsWith("landscape"))],
    ["竖屏", cov((s) => (s.device.orientation || "").startsWith("portrait"))],
    ["iframe kinds（webPanel 有高度）", cov((s) => s.webPanel && s.webPanel.height)],
    ["pty kind", cov((s) => s.ui.kind === "pty")],
    ["quick-keys 打开", cov((s) => s.ui.quickKeysOpen)],
    ["妙控 hint", cov((s) => s.reason === "magic-hint")],
    ["软键盘 hint", cov((s) => s.reason === "keyboard-hint")],
    ["focusin（焦点进入）", cov((s) => s.reason === "focusin")],
    ["pageshow / visibilitychange（生命周期）", cov((s) => s.reason === "pageshow" || s.reason === "visibilitychange")],
    ["Split View（innerWidth < 屏幕短边）", cov((s) => s.innerWidth < Math.min(s.device.screenWidth, s.device.screenHeight) - 40)],
];
console.log("| 覆盖项 | 事件数 | 判定 |");
console.log("| --- | --- | --- |");
for (const [name, n] of table) console.log(`| ${name} | ${n} | ${n ? "✅ 有数据" : "❌ 缺"} |`);

console.log("\n### 三不变量原始分量（scale=1 且未钉高的静止段）\n");
const stat = ev.filter((s) => s.vv.scale === 1 && !s.app.inlineHeight && !(s.vv.offsetTop > 0.5) && s.reason !== "scroll");
const uniq = new Map();
for (const s of stat) {
    const key = [s.innerHeight, f1(s.vv.height), f1(s.app.bottom), s.app.paddingBottom, f1(s.workspace.bottom), f1(s.terminal && s.terminal.bottom), f1(s.xtermRows && s.xtermRows.bottom), s.device.orientation].join("|");
    uniq.set(key, (uniq.get(key) || 0) + 1);
}
console.log("| inner | vv.height | app.bottom | padBot | ws.bottom | term.bottom | xtermRows.bottom | 方向 | 次数 |");
console.log("| --- | --- | --- | --- | --- | --- | --- | --- | --- |");
for (const [k, n] of [...uniq.entries()].sort((a, b) => b[1] - a[1])) {
    const c = k.split("|");
    console.log(`| ${c[0]} | ${c[1]} | ${c[2]} | ${c[3]} | ${c[4]} | ${c[5]} | ${c[6]} | ${c[7]} | ${n} |`);
}
