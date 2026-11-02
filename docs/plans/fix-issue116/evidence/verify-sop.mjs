#!/usr/bin/env node
// Checks a capture against docs/plans/fix-issue116/A0-SOP.md and prints a
// PASS/FAIL table per invariant, plus a one-line summary per manual Capture.
//
// The SOP is written for a human on the iPad; this is the machine half of the
// same contract. Three invariants are asserted (see the SOP's last section):
//
//   1. no band:      (vv.offsetTop + vv.height) - app.bottom <= 1
//   2. bottom ink:   probe.visualBottom hits the app (workspace / terminal host),
//                    not BODY or HTML (a BODY/HTML hit means the root canvas is
//                    genuinely exposed)
//   3. page still:   document.pageTop and document.scrollTop stay 0
//
// Usage:
//   MW_A0_TOKEN=… node verify-sop.mjs --session <id> --base http://host:port
//   MW_A0_TOKEN=… node verify-sop.mjs --file evidence/raw/<id>.jsonl
//
// Exit code is 0 only when every invariant holds on every in-scope frame.

import { readFileSync } from "node:fs";

function arg(name, fallback = null) {
  const i = process.argv.indexOf(`--${name}`);
  return i >= 0 && i + 1 < process.argv.length ? process.argv[i + 1] : fallback;
}

async function loadEvents() {
  const file = arg("file");
  if (file) {
    return readFileSync(file, "utf8")
      .split("\n")
      .filter((line) => line.trim())
      .map((line) => JSON.parse(line));
  }
  const session = arg("session");
  const base = arg("base");
  const token = process.env.MW_A0_TOKEN;
  if (!session || !base || !token) {
    console.error("need --file, or --session + --base + MW_A0_TOKEN");
    process.exit(2);
  }
  const url = `${base}/api/viewport-debug/report?session=${encodeURIComponent(session)}&after=0&token=${encodeURIComponent(token)}`;
  const res = await fetch(url, { signal: AbortSignal.timeout(10000) });
  if (!res.ok) {
    console.error(`GET report -> ${res.status} (session unknown or expired)`);
    process.exit(2);
  }
  const body = await res.json();
  return body.events || [];
}

const events = await loadEvents();
if (!events.length) {
  console.error("no events in this session yet — open the diagnostic URL and press Capture");
  process.exit(2);
}

const versions = [...new Set(events.map((e) => e.snapshot.probeVersion || "(unstamped)"))];
const pannedFrames = events.filter((e) => (e.snapshot.vv?.offsetTop || 0) > 0.5).length;

const failures = { band: [], ink: [], scroll: [] };
const rows = [];

for (const e of events) {
  const s = e.snapshot;
  const vv = s.vv;
  // Zoom is exempt end to end, exactly as the shipped guard is.
  if (vv && Math.abs((vv.scale || 1) - 1) > 0.01) continue;
  const visibleBottom = vv ? (vv.offsetTop || 0) + vv.height : s.clientHeight;
  const band = Math.round((visibleBottom - s.app.bottom) * 10) / 10;
  if (band > 1) failures.band.push({ seq: e.seq, reason: s.reason, band });

  const hit = s.probe?.visualBottom;
  if (hit && /^(BODY|HTML)$/i.test(hit.tag)) {
    failures.ink.push({ seq: e.seq, reason: s.reason, tag: hit.tag, id: hit.id, bg: hit.background });
  }

  const pageTop = s.document?.pageTop;
  const scrollTop = s.document?.scrollTop;
  if ((pageTop ?? 0) > 0.5 || (scrollTop ?? 0) > 0.5) {
    failures.scroll.push({ seq: e.seq, reason: s.reason, pageTop, scrollTop });
  }

  if (s.reason === "manual") {
    rows.push({
      seq: e.seq,
      vv: vv ? `${Math.round(vv.height * 10) / 10}` : "-",
      pan: vv ? vv.offsetTop : "-",
      inline: s.app.inlineHeight || "-",
      band,
      ink: hit ? `${hit.tag}${hit.id ? "#" + hit.id : ""}` : "-",
    });
  }
}

const mark = (n) => (n === 0 ? "PASS" : "FAIL");
console.log(`events: ${events.length}   probeVersion: ${versions.join(", ")}`);
console.log(`panned frames (informational, iOS panning the VISUAL viewport): ${pannedFrames}`);
if (versions.some((v) => v === "(unstamped)")) {
  console.log("WARNING: some frames carry no probeVersion — those came from a build older than p4.");
}
console.log("");
console.log("manual captures:");
console.log("  seq   vv.height  pan   inline     band   bottom ink");
for (const r of rows) {
  console.log(`  ${String(r.seq).padEnd(5)} ${String(r.vv).padEnd(10)} ${String(r.pan).padEnd(5)} ${String(r.inline).padEnd(10)} ${String(r.band).padEnd(6)} ${r.ink}`);
}
console.log("");
console.log(`1. no band (visibleBottom - app.bottom <= 1)      ${mark(failures.band.length)}  (${failures.band.length} frames)`);
console.log(`2. bottom ink is the app, not BODY/HTML           ${mark(failures.ink.length)}  (${failures.ink.length} frames)`);
console.log(`3. page did not scroll (pageTop/scrollTop == 0)   ${mark(failures.scroll.length)}  (${failures.scroll.length} frames)`);
for (const [name, list] of Object.entries(failures)) {
  if (!list.length) continue;
  console.log(`\n  ${name} failures (first 8):`);
  for (const f of list.slice(0, 8)) console.log("   ", JSON.stringify(f));
}

process.exit(failures.band.length + failures.ink.length + failures.scroll.length === 0 ? 0 : 1);
