#!/usr/bin/env node
// Real-geometry check for the sidebar title row (issue #116, contract
// p9-portrait-refit-import-v1).
//
// WHY A SEPARATE CHECK, AND WHY IT HAS TO OPEN A BROWSER.
//
// The defect was reported from a real Tailscale session on an iPad in portrait:
// the 200px sidebar's two action buttons spilled into the instance area. No
// source grep can prove or refute that, because the cause is not a missing
// declaration — it is the intrinsic min-content width of an inline run. The markup
// is
//
//     <span>Worktrees<span id="remote-badge" class="remote-badge hidden">Remote</span></span>
//
// with NO whitespace between the word and the badge, so under UAX #14 the two form
// ONE unbreakable line: the span's min-content is the fused "WorktreesRemote"
// (131.13px as Chrome measures it here), and a flex item may never shrink below
// its min-content. Add the two 44px coarse-pointer targets and their 8px gap and
// the row needs 131 + 96 = 227px inside a 175px content box.
//
// So this script measures the live layout in a real engine and asserts five things
// at each sidebar width:
//
//   1. the remote badge is VISIBLE (display != none) — the fix may not hide it;
//   2. both action buttons are >= 44px on both axes — the coarse hit target;
//   3. every button's right edge <= the sidebar's CONTENT right edge — the report;
//   4. the title text cell and the action group do not overlap;
//   5. .sidebar-title does not overflow its own box (scrollWidth <= clientWidth).
//
// Verdicts: PASS / FAIL / INCONCLUSIVE, exit 0 / 1 / 2. INCONCLUSIVE is a real
// answer and not a soft pass: if the sidebar, the title row or the buttons are not
// in the page, or the badge cannot be revealed, nothing was measured and the run
// says so.
//
// WHAT THIS DOES NOT PROVE (read this before quoting a PASS):
//   * it is HEADLESS CHROME, not iOS Safari — it proves the W3C flex answer, not
//     iPadOS behaviour (see the same caveat in structural-check.mjs);
//   * the remote state is SIMULATED: `.remote-access` is added to #app and
//     `hidden` is removed from #remote-badge, because the badge is revealed by a
//     successful remote-transport check that needs a real Tailscale peer. The badge
//    's own CSS is the shipped CSS; only its visibility is driven;
//   * the widths are driven through an INLINE max-width on #sidebar so 200px and
//     240px are both measured in one page load. At 834px CSS width the shipped
//     middle-tier rule already caps #sidebar at 200px, so the 200px case is the
//     page's own state, not an override.
//
// Usage:
//   node sidebar-title-check.mjs [--url file:///…/index.html] [--width 834]
//                                 [--height 1062] [--port 0] [--out DIR] [--label p9]
//   node sidebar-title-check.mjs --self-test        # judgement fixtures, no browser
import { mkdirSync, writeFileSync } from "node:fs";
import { spawn } from "node:child_process";
import { dirname, join } from "node:path";
import { fileURLToPath } from "node:url";

const HERE = dirname(fileURLToPath(import.meta.url));
const DEFAULT_PAGE = "file://" + join(HERE, "..", "..", "..", "..", "internal", "ui", "static", "index.html");
const WIDTHS = [200, 240, 160];
const TOLERANCE_PX = 1;
const MIN_TARGET_PX = 44;

function arg(name, fallback) {
  const i = process.argv.indexOf("--" + name);
  return i !== -1 && process.argv[i + 1] ? process.argv[i + 1] : fallback;
}
const sleep = (ms) => new Promise((r) => setTimeout(r, ms));

// ---------------------------------------------------------------------------
// The page-side measurement. One pass, all numbers, no verdicts here: the
// judgment lives in judge() so --self-test can drive it with captured payloads.
// ---------------------------------------------------------------------------
const MEASURE = `(() => {
  const R = (el) => {
    if (!el) return null;
    const r = el.getBoundingClientRect();
    return { left: r.left, right: r.right, top: r.top, bottom: r.bottom, width: r.width, height: r.height };
  };
  const sidebar = document.getElementById("sidebar");
  const title = document.querySelector(".sidebar-title");
  const actions = document.querySelector(".sidebar-actions");
  const badge = document.getElementById("remote-badge");
  if (!sidebar || !title || !actions) return { error: "missing sidebar / .sidebar-title / .sidebar-actions" };

  // The remote state, simulated as far as it can be simulated without a peer:
  // the class the transport check adds, and the attribute it clears.
  const app = document.getElementById("app");
  if (app) app.classList.add("remote-access");
  if (badge) badge.classList.remove("hidden");

  const out = { widths: [], coarse: !!(window.matchMedia && matchMedia("(pointer: coarse)").matches) };
  const border = parseFloat(getComputedStyle(sidebar).borderRightWidth || "0");
  for (const w of WIDTHS_INLINE) {
    sidebar.style.maxWidth = w + "px";
    void sidebar.offsetWidth; // force the reflow before reading, or we measure the previous width
    const btns = Array.prototype.slice.call(actions.querySelectorAll(".icon-btn")).map((b) => R(b));
    out.widths.push({
      sidebarWidth: w,
      sidebar: R(sidebar),
      sidebarContentRight: sidebar.getBoundingClientRect().right - border,
      title: R(title),
      titleScrollWidth: title.scrollWidth,
      titleClientWidth: title.clientWidth,
      textCell: R(title.firstElementChild),
      actions: R(actions),
      buttons: btns,
      badge: R(badge),
      badgeDisplay: badge ? getComputedStyle(badge).display : "",
      badgeText: badge ? (badge.textContent || "").trim() : "",
    });
  }
  sidebar.style.maxWidth = "";
  return out;
})()`;

// ---------------------------------------------------------------------------
// Pure judgment over a measurement payload. Every rule states its own reason.
// ---------------------------------------------------------------------------
function judgePerWidth(m, index) {
  const fail = [];
  const info = [];
  const tag = "sidebar@" + m.sidebarWidth + "px";
  const where = (i) => (i === undefined ? tag : tag + " button#" + i);

  if (!m.buttons || !m.buttons.length) {
    return { verdict: "INCONCLUSIVE", fail: [], info: [tag + ": no action buttons in the page — nothing measured"] };
  }
  if (!m.textCell) {
    return { verdict: "INCONCLUSIVE", fail: [], info: [tag + ": no title text cell — nothing measured"] };
  }

  // 1. the badge must be visible
  if (!m.badge) {
    return { verdict: "INCONCLUSIVE", fail: [], info: [tag + ": no #remote-badge element"] };
  }
  if (m.badgeDisplay === "none" || m.badge.width <= 0 || m.badge.height <= 0) {
    fail.push(where() + ": the remote badge is not visible (display=" + m.badgeDisplay +
      " " + m.badge.width + "x" + m.badge.height + ") — the fix may not hide remote state to fit");
  } else {
    info.push(where() + ": badge visible " + m.badge.width.toFixed(2) + "x" + m.badge.height.toFixed(2) +
      ' "' + m.badgeText + '"');
  }

  // 2. coarse hit targets
  m.buttons.forEach((b, i) => {
    if (b.width + TOLERANCE_PX < MIN_TARGET_PX || b.height + TOLERANCE_PX < MIN_TARGET_PX) {
      fail.push(where(i) + ": hit target " + b.width.toFixed(2) + "x" + b.height.toFixed(2) +
        " is under the 44px coarse-pointer minimum");
    }
  });

  // 3. the report itself: no button may sit outside the sidebar
  const maxRight = Math.max(...m.buttons.map((b) => b.right));
  if (maxRight > m.sidebarContentRight + TOLERANCE_PX) {
    fail.push(where() + ": the action group reaches x=" + maxRight.toFixed(2) +
      " but the sidebar content ends at " + m.sidebarContentRight.toFixed(2) +
      " — " + (maxRight - m.sidebarContentRight).toFixed(2) + "px of buttons over the instance area");
  } else {
    info.push(where() + ": rightmost button at " + maxRight.toFixed(2) +
      ", sidebar content ends at " + m.sidebarContentRight.toFixed(2));
  }

  // 4. text and actions must not overlap (wrap or clear, never collide)
  if (m.textCell.right > m.actions.left + TOLERANCE_PX && m.textCell.bottom > m.actions.top + TOLERANCE_PX) {
    fail.push(where() + ": the title text cell (" + m.textCell.right.toFixed(2) + " x " +
      m.textCell.top.toFixed(2) + "-" + m.textCell.bottom.toFixed(2) +
      ") overlaps the action group (left " + m.actions.left.toFixed(2) + ", top " + m.actions.top.toFixed(2) + ")");
  }

  // 5. the row owns its own box
  if (m.titleScrollWidth > m.titleClientWidth + TOLERANCE_PX) {
    fail.push(where() + ": .sidebar-title scrolls its own content (scrollWidth " + m.titleScrollWidth +
      " > clientWidth " + m.titleClientWidth + ")");
  }
  info.push(where() + ": title row h=" + m.title.height.toFixed(2) + " scrollW=" + m.titleScrollWidth +
    " clientW=" + m.titleClientWidth);

  return { verdict: fail.length ? "FAIL" : "PASS", fail, info };
}

function judge(measured) {
  if (!measured || measured.error) {
    return { verdict: "INCONCLUSIVE", cases: [], fail: [], info: [(measured && measured.error) || "no measurement"] };
  }
  if (!measured.coarse) {
    return {
      verdict: "INCONCLUSIVE", cases: [], fail: [],
      info: ["the page did not report a coarse pointer — the 44px hit targets this check exists to protect are not in play, so a pass here would be about a different layout"],
    };
  }
  const cases = (measured.widths || []).map((m, i) => ({ sidebarWidth: m.sidebarWidth, ...judgePerWidth(m, i) }));
  if (!cases.length) return { verdict: "INCONCLUSIVE", cases: [], fail: [], info: ["no width was measured"] };
  const fail = cases.flatMap((c) => c.fail);
  const conclusive = cases.filter((c) => c.verdict !== "INCONCLUSIVE");
  const verdict = conclusive.length === 0 ? "INCONCLUSIVE" : (fail.length ? "FAIL" : "PASS");
  return { verdict, cases, fail, info: cases.flatMap((c) => c.info) };
}

// ---------------------------------------------------------------------------
// --self-test: the judgement itself, driven by fixtures, no browser.
// ---------------------------------------------------------------------------
function selfTest() {
  const btn = (left) => ({ left, right: left + 44, top: 12, bottom: 56, width: 44, height: 44 });
  const base = {
    sidebarWidth: 200,
    sidebar: { left: 0, right: 200, top: 0, bottom: 1062, width: 200, height: 1062 },
    sidebarContentRight: 199,
    title: { left: 0, right: 199, top: 0, bottom: 69, width: 199, height: 69 },
    titleScrollWidth: 199, titleClientWidth: 199,
    textCell: { left: 12, right: 83, top: 19, bottom: 49, width: 71, height: 30 },
    actions: { left: 91, right: 187, top: 12, bottom: 56, width: 96, height: 44 },
    buttons: [btn(91), btn(143)],
    badge: { left: 18, right: 79.78, top: 33.56, bottom: 48.56, width: 61.78, height: 15 },
    badgeDisplay: "inline", badgeText: "Remote",
  };
  const coarse = { coarse: true, widths: [base, { ...base, sidebarWidth: 240 }] };

  const cases = [
    ["the shipped p9 shape passes", coarse, "PASS"],
    // The p8 defect, spelled as numbers: the second button reaches 239.13 in a
    // sidebar whose content ends at 199. This is the measured HEAD layout.
    ["the p8 overflow fails", { coarse: true, widths: [{ ...base, buttons: [btn(143), btn(195.13)] }] }, "FAIL"],
    ["a hidden badge fails", { coarse: true, widths: [{ ...base, badgeDisplay: "none", badge: { ...base.badge, width: 0, height: 0 } }] }, "FAIL"],
    ["a squeezed button fails", { coarse: true, widths: [{ ...base, buttons: [btn(91), { ...btn(143), width: 31, height: 31, right: 174 }] }] }, "FAIL"],
    ["overlapping text and actions fail", { coarse: true, widths: [{ ...base, textCell: { left: 12, right: 150, top: 19, bottom: 49, width: 138, height: 30 } }] }, "FAIL"],
    ["a row that scrolls its own content fails", { coarse: true, widths: [{ ...base, titleScrollWidth: 239 }] }, "FAIL"],
    ["a fine pointer is not this layout", { ...coarse, coarse: false }, "INCONCLUSIVE"],
    ["no buttons is not a pass", { coarse: true, widths: [{ ...base, buttons: [] }] }, "INCONCLUSIVE"],
    ["no width measured is not a pass", { coarse: true, widths: [] }, "INCONCLUSIVE"],
    ["a broken page is not a pass", { error: "missing .sidebar-actions" }, "INCONCLUSIVE"],
  ];

  let bad = 0;
  for (const [name, fixture, want] of cases) {
    const got = judge(fixture).verdict;
    const ok = got === want;
    if (!ok) bad++;
    console.log((ok ? "ok   " : "BAD  ") + name.padEnd(42) + " want=" + want.padEnd(12) + " got=" + got);
  }
  // The tolerance edge: exactly at the content edge passes, 1.5px past it fails.
  const edge = judge({ coarse: true, widths: [{ ...base, buttons: [btn(91), { ...btn(143), right: 199 }] }] }).verdict;
  console.log((edge === "PASS" ? "ok   " : "BAD  ") + "exactly-at-the-edge passes".padEnd(42) + " want=PASS" + "       got=" + edge);
  const over = judge({ coarse: true, widths: [{ ...base, buttons: [btn(91), { ...btn(143), right: 200.5 }] }] }).verdict;
  console.log((over === "FAIL" ? "ok   " : "BAD  ") + "1.5px past the edge fails".padEnd(42) + " want=FAIL" + "       got=" + over);
  const n = cases.length + 2;
  console.log("\n" + (bad ? bad + " judgement(s) misjudged" : "all " + n + " judgements behaved as specified"));
  process.exit(bad ? 1 : 0);
}

// ---------------------------------------------------------------------------

async function main() {
  if (process.argv.includes("--self-test")) return selfTest();
  const URL = arg("url", process.env.MW_TEST_URL || DEFAULT_PAGE);
  const CHROME = arg("chrome", process.env.CHROME_BIN || "/usr/bin/google-chrome");
  const WIDTH = Number(arg("width", "834"));
  const HEIGHT = Number(arg("height", "1062"));
  const PORT = Number(arg("port", "0")) || 9347;
  const LABEL = arg("label", "sidebar-title-p9");

  const chrome = spawn(CHROME, [
    "--headless=new", "--disable-gpu", "--no-first-run", "--no-default-browser-check",
    "--remote-debugging-port=" + PORT, "--window-size=" + WIDTH + "," + HEIGHT, "about:blank",
  ], { stdio: "ignore" });

  let ws = null;
  let idSeq = 0;
  const pending = new Map();
  const send = (method, params = {}, sessionId) => new Promise((resolve, reject) => {
    const id = ++idSeq;
    pending.set(id, { resolve, reject });
    ws.send(JSON.stringify({ id, method, params, ...(sessionId ? { sessionId } : {}) }));
  });

  try {
    let info = null;
    for (let i = 0; i < 60 && !info; i++) {
      try { info = await (await fetch(`http://127.0.0.1:${PORT}/json/version`)).json(); } catch (_) { await sleep(500); }
    }
    if (!info) throw new Error("chrome devtools endpoint not reachable on port " + PORT);
    ws = new WebSocket(info.webSocketDebuggerUrl);
    await new Promise((resolve, reject) => {
      ws.addEventListener("open", resolve, { once: true });
      ws.addEventListener("error", reject, { once: true });
    });
    ws.addEventListener("message", (ev) => {
      const msg = JSON.parse(ev.data);
      if (msg.id && pending.has(msg.id)) {
        const p = pending.get(msg.id);
        pending.delete(msg.id);
        if (msg.error) p.reject(new Error(msg.error.message)); else p.resolve(msg.result);
      }
    });

    const { targetId } = await send("Target.createTarget", { url: "about:blank" });
    const { sessionId } = await send("Target.attachToTarget", { targetId, flatten: true });
    await send("Page.enable", {}, sessionId);
    await send("Emulation.setTouchEmulationEnabled", { enabled: true, maxTouchPoints: 5 }, sessionId).catch(() => {});
    await send("Emulation.setDeviceMetricsOverride", { width: WIDTH, height: HEIGHT, deviceScaleFactor: 2, mobile: true }, sessionId);
    await send("Page.navigate", { url: URL }, sessionId);
    await sleep(1500);
    // After navigation: matchMedia("(pointer: coarse)") only answers coarse once the
    // emulation is in place, and the 44px rules are gated on exactly that.
    await send("Emulation.setEmulatedMedia", { features: [{ name: "pointer", value: "coarse" }, { name: "hover", value: "none" }] }, sessionId);
    await sleep(250);

    const evaluated = await send("Runtime.evaluate", {
      expression: "const WIDTHS_INLINE = " + JSON.stringify(WIDTHS) + ";" + MEASURE,
      returnByValue: true,
    }, sessionId);
    if (evaluated.exceptionDetails) {
      throw new Error("page-side measurement threw: " + JSON.stringify(evaluated.exceptionDetails.text || evaluated.exceptionDetails));
    }
    const measured = evaluated.result.value;
    const result = judge(measured);

    console.log("sidebar title-row geometry check — " + LABEL);
    console.log("page=" + URL.replace(/([?&])token=[^&]*/gi, "$1token=REDACTED"));
    console.log("engine=" + (info.Browser || "unknown") + " viewport=" + WIDTH + "x" + HEIGHT +
      " pointer=" + (measured && measured.coarse ? "coarse" : "fine/not reported"));
    for (const c of result.cases || []) {
      console.log("\n[" + c.verdict + "] sidebar " + c.sidebarWidth + "px");
      for (const line of c.info) console.log("  . " + line);
      for (const line of c.fail) console.log("  X " + line);
    }
    for (const line of (result.info || []).filter((l) => !(result.cases || []).length)) console.log("  . " + l);
    console.log("\nVERDICT: " + result.verdict);

    const outDir = arg("out", join(HERE, "structural-check"));
    mkdirSync(outDir, { recursive: true });
    const outFile = join(outDir, "RESULTS-" + LABEL + ".json");
    writeFileSync(outFile, JSON.stringify({
      label: LABEL, checkedAt: new Date().toISOString(),
      page: URL.replace(/([?&])token=[^&]*/gi, "$1token=REDACTED"),
      engine: info.Browser || "", viewport: { width: WIDTH, height: HEIGHT },
      widths: WIDTHS, tolerancePx: TOLERANCE_PX, minTargetPx: MIN_TARGET_PX,
      simulated: "remote badge revealed by removing [hidden] and adding .remote-access to #app; widths driven by inline max-width on #sidebar",
      measured, result,
    }, null, 2));
    console.log("evidence: " + outFile);
    process.exit(result.verdict === "PASS" ? 0 : result.verdict === "FAIL" ? 1 : 2);
  } finally {
    chrome.kill();
  }
}

main().catch((err) => { console.error("check failed: " + err.message); process.exit(2); });
