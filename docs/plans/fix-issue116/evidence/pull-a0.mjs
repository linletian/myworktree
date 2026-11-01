// A0 真机诊断增量拉取器（issue #116）。
//
// 用途：诊断会话每会话只保留最近 120 个事件、30 分钟 TTL
// （internal/app/viewport_debug.go:24-27），所以必须在 iPad 操作过程中
// 分段拉取，不能等全部做完再读。本脚本只做只读 GET，不写任何状态到服务端。
//
// 用法（token 只走环境变量，不落仓库）：
//   MW_A0_TOKEN=<token> node docs/plans/fix-issue116/evidence/pull-a0.mjs \
//       --session <面板上那串 Session id> [--base http://100.86.87.23:40751] \
//       [--label blockB] [--follow 20]
//
// 产物：raw/<session>.jsonl（逐事件原文）、raw/<session>.cursor（增量游标）、
// stdout 汇总表（人工回填 DATA-COLLECTION 用）。
//
// 仅用 Node 内建模块，不引入依赖，不进 CI。

import { appendFileSync, mkdirSync, readFileSync, writeFileSync } from "node:fs";
import { dirname, join } from "node:path";
import { fileURLToPath } from "node:url";

const args = process.argv.slice(2);
function arg(name, fallback = "") {
    const i = args.indexOf("--" + name);
    return i >= 0 && args[i + 1] ? args[i + 1] : fallback;
}

const base = (arg("base", "http://100.86.87.23:40751")).replace(/\/+$/, "");
const session = arg("session");
const label = arg("label", "pull");
const follow = Number(arg("follow", "0")) || 0;
const token = process.env.MW_A0_TOKEN || "";

if (!/^[A-Za-z0-9_-]{16,64}$/.test(session)) {
    console.error("need --session <16-64 chars from the iPad panel>");
    process.exit(2);
}

const here = dirname(fileURLToPath(import.meta.url));
const rawDir = join(here, "raw");
mkdirSync(rawDir, { recursive: true });
const rawPath = join(rawDir, session + ".jsonl");
const cursorPath = join(rawDir, session + ".cursor");

function loadCursor() {
    try {
        const n = Number(readFileSync(cursorPath, "utf8").trim());
        return Number.isInteger(n) && n > 0 ? n : 0;
    } catch (_) {
        return 0;
    }
}

async function pull(after) {
    const url = `${base}/api/viewport-debug/report?session=${encodeURIComponent(session)}&after=${after}&token=${encodeURIComponent(token)}`;
    const res = await fetch(url, { headers: { "cache-control": "no-store" } });
    if (res.status === 404) return { missing: true, events: [], latestSeq: after };
    if (!res.ok) throw new Error(`HTTP ${res.status}: ${(await res.text()).slice(0, 200)}`);
    return await res.json();
}

function row(e) {
    // 服务端存的是 {seq, receivedAt, snapshot:{...}}，几何全在 snapshot 层下。
    const s = e.snapshot || e;
    const vv = s.vv || {};
    const app = s.app || {};
    const ws = s.workspace || {};
    const term = s.terminal || {};
    const rows = s.xtermRows || {};
    const gap = typeof vv.offsetTop === "number" && typeof vv.height === "number"
        && typeof app.bottom === "number"
        ? (vv.offsetTop + vv.height) - app.bottom : NaN;
    return [
        String(e.seq ?? "").padStart(4),
        String(s.reason ?? "").padEnd(14),
        String(vv.height ?? "?").padStart(7),
        String(vv.offsetTop ?? "?").padStart(7),
        String(vv.scale ?? "?").padStart(5),
        String(app.bottom ?? "?").padStart(8),
        String(app.inlineHeight || "-").padStart(9),
        String(app.paddingBottom || "?").padStart(6),
        String(ws.bottom ?? "?").padStart(8),
        String(term.bottom ?? (rows.bottom ?? "?")).padStart(8),
        String(s.webPanel && s.webPanel.height ? "web" : "-").padStart(4),
        String((s.ui && s.ui.kind) || "-").padStart(9),
        String((s.ui && s.ui.status) || "-").padStart(9),
        Number.isFinite(gap) ? gap.toFixed(2).padStart(8) : "     n/a",
        (s.ui && s.ui.quickKeysOpen ? "QK" : "-").padStart(3),
    ].join(" ");
}

const header = ["seq", "reason", "vvH", "vvTop", "scale", "appBot", "inlineH",
    "padBot", "wsBot", "termBot", "web", "kind", "status", "vvBot-appBot", "qk"].join(" ");

async function once(cursor) {
    const report = await pull(cursor);
    if (report.missing) {
        console.log(`[${label}] session 尚不存在/已过期（404）。等 iPad 打开诊断链接后再试。`);
        return cursor;
    }
    const events = Array.isArray(report.events) ? report.events : [];
    if (events.length) {
        appendFileSync(rawPath, events.map((e) => JSON.stringify(e)).join("\n") + "\n");
        writeFileSync(cursorPath, String(report.latestSeq ?? cursor));
        console.log(`[${label}] +${events.length} events → seq ${report.latestSeq}  (raw: ${rawPath})`);
        console.log(header);
        for (const e of events) console.log(row(e));
    } else {
        console.log(`[${label}] 无新增事件，latestSeq=${report.latestSeq ?? cursor}`);
    }
    return report.latestSeq ?? cursor;
}

let cursor = loadCursor();
cursor = await once(cursor);
if (!follow) process.exit(0);

for (let i = 0; i < follow; i++) {
    await new Promise((r) => setTimeout(r, 20000));
    cursor = await once(cursor);
}
