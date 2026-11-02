#!/usr/bin/env node
// Verify the DEPLOYED bundle's viewport pin against product choice A (issue #116).
//
// Why this exists: the node suite in internal/ui/testdata/ slices the repo
// source, and the Go anchors assert served-asset *text*. Neither proves the
// running server's bytes behave. This script fetches the live HTML from the
// diagnostic server, slices the shipped applyVisualViewportHeight out of it and
// drives it against geometry replayed from the iPad capture
// (session ios272recal…, keyboard held open, vv.height constant 274).
//
// Choices pinned (see docs/plans/fix-issue116/PLAN.md §4.2 and DATA-COLLECTION
// .md §5g):
//   - occlusion is judged on vv.height ONLY; the target is vv.height ONLY;
//     vv.offsetTop never enters either (choice A: fewer rows, never hidden)
//   - soft keyboard 428px = 60.9% of 702  -> pins
//   - magic floating bar 68.5px = 9.8%    -> never pins (out of scope)
//   - threshold is a RATIO with an inclusive boundary (>= 25%)
//   - pinch zoom (scale != 1) opts out entirely
//
// Gotchas hit while writing this — do not reintroduce:
//   - the sliced text ENDS IN A LINE COMMENT, so the returned-function epilogue
//     must start on its own newline ("; return …" gets swallowed by the //).
//   - `new Function(src)(stub)()` calls the INNER function too and yields
//     undefined; build the factory first, then call it once.
//
// Run: MW_LIVE_URL="http://<host>:<port>/?token=***" node verify-live-choiceA.mjs
//      (the token is read from the environment / a file, never hard-coded here)

import { readFileSync } from "node:fs";
import assert from "node:assert/strict";

const url = process.env.MW_LIVE_URL;
if (!url) {
  console.error("need MW_LIVE_URL (include ?token=… ; the token is never committed)");
  process.exit(2);
}

const res = await fetch(url, { signal: AbortSignal.timeout(8000) });
if (!res.ok) {
  console.error(`GET ${url.replace(/token=[^&]+/, "token=…")} -> ${res.status}`);
  process.exit(1);
}
const html = await res.text();
console.log(`live bytes: ${html.length} (${url.replace(/token=[^&]+/, "token=…")})`);

// Same slicing contract as the Go anchor: named function, indented at line
// start, terminated by the next sibling `function` at the same indent.
function sliceFunction(source, name) {
  const at = source.indexOf(`function ${name}(`);
  assert.ok(at >= 0, `live HTML must contain function ${name}`);
  const indent = source.slice(source.lastIndexOf("\n", at) + 1, at);
  const rest = source.slice(at);
  let end = rest.length;
  for (const marker of [`\n${indent}function `, `\n${indent}async function `]) {
    const j = rest.indexOf(marker);
    if (j >= 0 && j < end) end = j;
  }
  return rest.slice(0, end);
}

const apply = sliceFunction(html, "applyVisualViewportHeight");

// Choice A is a ban on CODE, not on prose: the function documents the very
// geometry it stopped reading.
const code = apply.replace(/\/\/[^\n]*/g, "").replace(/\/\*[\s\S]*?\*\//g, "");
for (const banned of ["offsetTop", "visibleBottom", "covered"]) {
  assert.ok(!code.includes(banned), `live applyVisualViewportHeight still reads ${banned}`);
}
console.log("✓ live code reads neither offsetTop, visibleBottom nor covered");

const app = { style: {} };
const vv = { height: 702, offsetTop: 0, scale: 1, addEventListener() {}, removeEventListener() {} };
const documentElement = { clientHeight: 702 };
const factory = new Function("document", "window", `${apply}\nreturn applyVisualViewportHeight;`);
const applyHeight = factory(
  { documentElement, getElementById: (id) => (id === "app" ? app : null) },
  { visualViewport: vv },
);

// iPad Pro 11 landscape, layout viewport 702 — the calibrated device.
vv.height = 702;
applyHeight();
assert.equal(app.style.height || "", "", "at rest the CSS cascade owns the height");

vv.height = 274; // soft keyboard: covered 428px = 60.9%
applyHeight();
assert.equal(app.style.height, "274px", "soft keyboard must pin to vv.height");

// The regression this replaced: dragging used to grow the pin (seq297–310),
// then clear it mid-drag once covered fell under the threshold (seq311), and
// never re-arm on the way back (seq362–396). Every pan answer is now 274.
for (const offsetTop of [12.5, 103, 237, 265.5, 428, 0]) {
  vv.offsetTop = offsetTop;
  applyHeight();
  assert.equal(app.style.height, "274px", `pan ${offsetTop} must not change the pin (choice A)`);
}
console.log("✓ pinned 274px through every pan offset — the pin can no longer be dragged away");

vv.offsetTop = 0;
vv.height = 633.5; // magic floating bar: covered 68.5px = 9.8%
applyHeight();
assert.equal(app.style.height || "", "", "the floating bar is out of scope — never pin for it");
console.log("✓ magic floating bar (9.8%) never pins");

vv.height = 702;
applyHeight();
assert.equal(app.style.height || "", "", "keyboard gone -> the cascade owns sizing again");

vv.scale = 0.75;
vv.height = 365; // measured at 0.75 zoom: covered 570.7px = 61.0%
applyHeight();
assert.equal(app.style.height || "", "", "zoom must opt out (this is why 0.75 never compresses)");
console.log("✓ zoom exemption intact — the real reason scale=0.75 stays full height");

// Ratio, not pixels: the same fractions must hold at another viewport size.
vv.scale = 1;
documentElement.clientHeight = 400;
vv.height = 300; // covered exactly 25% — inclusive
applyHeight();
assert.equal(app.style.height, "300px", "covered == 25% is hard (inclusive boundary)");
vv.height = 301; // covered 24.75%
applyHeight();
assert.equal(app.style.height || "", "", "just under 25% is soft");
console.log("✓ threshold is a ratio with an inclusive boundary");

console.log("\nPASS — verified against the bytes the running server actually served.");
