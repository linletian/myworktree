#!/usr/bin/env node
// Verify the DEPLOYED bundle's PTY-local avoidance against contract v3.1
// ("full-height shell + PTY-LOCAL avoidance above a ratio floor, non-scrollable
// root", issue #116). v3.1 = the v3.0 base (full-height shell, host-local lift,
// overlay half) PLUS the 0.25 small-overlay floor, the fixed #status-bar and the
// wheel isolation.
//
// Why this exists: the node suite in internal/ui/testdata/ slices the repo
// source, and the Go anchors assert served-asset *text*. Neither proves the
// running server's bytes behave. This script fetches the live HTML from the
// diagnostic server, slices the shipped applyVisualViewportOcclusion out of it
// and drives it against geometry replayed from the iPad capture (session
// ios272recal…, keyboard held open, vv.height constant 274; layout 702 with the
// 20px safe-area inset that puts the host floor at 682).
//
// Choices pinned (docs/plans/fix-issue116/PLAN.md §4.2, DATA-COLLECTION.md §5g,
// A0-SOP.md v3.0), as they stand now:
//   - occlusion is judged on vv.height against documentElement.clientHeight ONLY;
//     vv.offsetTop never enters (choice A: fewer rows, never hidden rows)
//   - the shell is NEVER resized: `#app.style.height` is stubbed to THROW, so the
//     retired v2.2 pin fails at the write instead of being politely ignored. v2.2
//     pinned the whole page to vv.height here and asserted app.style.height;
//     those assertions are DELIBERATELY GONE — the same four heights now have to
//     leave #app untouched and pay the shortfall into the hosts instead.
//   - A shortfall is paid ONLY ABOVE A RATIO FLOOR (contract v3.1). The raw
//     shortfall is clientHeight - vv.height; at or above 0.25 * clientHeight it is
//     a real keyboard and its RAW value is paid into every host (274 →
//     428px). UNDER that floor (702*0.25=175.5, so BOTH the
//     633.5 → raw 68.5 = 9.8% strip AND the 555 → raw 147 = 20.9% Magic-keyboard
//     retract frames sit under it) it is the shallow overlay the
//     user said must NOT compress the terminal, so
//     it is exempt and owes 0 — the hosts stay flush and the strip overlaps a page
//     that never moved. The 0.15 this script asserted before (floor 105.3) is NOT
//     kept: it sat BELOW the measured 147px frame, made that transient a paying
//     occupant, and the device reported the terminal bouncing ~8.6 rows (147 over
//     the ~17px row) with the Transport climbing as the soft keyboard left. The floor is a RATIO of the layout height, so it holds at
//     every viewport size; it REPLACES v3.0's "any shortfall, no classification" and
//     the retired v2.x HARD_OCCLUSION_RATIO gate alike. A genuine magic-hint overlay
//     (vv.height = clientHeight) still lands on occlusion 0 by arithmetic.
//   - EVERY host is lifted, hidden and inactive included, and a host born under a
//     covered bottom is lifted at creation.
//   - pinch zoom (scale != 1) opts out entirely (ratio included) and restores bottom: 0px.
//   - nothing in this layer may write the root: scrollTo / scrollTop THROW below.
//
// Gotchas hit while writing this — do not reintroduce:
//   - the sliced text ENDS IN A LINE COMMENT, so the returned-function epilogue
//     must start on its own newline ("; return …" gets swallowed by the //).
//   - `new Function(src)(stub)()` calls the INNER function too and yields
//     undefined; build the factory first, then call it once.
//   - the host stub must answer the EXACT selector the shipped code queries
//     (":scope > [data-instance-id]"); a stub that answers anything would keep
//     this green if the selector drifted and the applier stopped finding hosts.
//
// Run: MW_LIVE_URL="http://<host>:<port>/?token=***" node verify-live-choiceA.mjs
//      (the token is read from the environment / a file, never hard-coded here)
// Offline: node verify-live-choiceA.mjs --html ../../../../internal/ui/static/index.html
// (four levels up: evidence -> fix-issue116 -> plans -> docs -> repo root). A
// relative path that does not resolve from the current directory is retried
// against THIS file's own directory, so the command works from either place.
//      (same drive against repo bytes; proves the logic, never that a server served it)

import { readFileSync } from "node:fs";
import assert from "node:assert/strict";
import { existsSync } from "node:fs";

const offline = (() => {
  const i = process.argv.indexOf("--html");
  return i >= 0 && i + 1 < process.argv.length ? process.argv[i + 1] : null;
})();
const url = process.env.MW_LIVE_URL;
if (!offline && !url) {
  console.error("need MW_LIVE_URL (include ?token=… ; the token is never committed) or --html <path>");
  process.exit(2);
}

let html;
if (offline) {
  let path = offline;
  if (!existsSync(path)) {
    const beside = new URL(offline, import.meta.url).pathname;
    if (existsSync(beside)) path = beside;
  }
  html = readFileSync(path, "utf8");
  console.log(`offline bytes: ${html.length} (${offline})`);
} else {
  const res = await fetch(url, { signal: AbortSignal.timeout(8000) });
  if (!res.ok) {
    console.error(`GET ${url.replace(/token=[^&]+/, "token=…")} -> ${res.status}`);
    process.exit(1);
  }
  html = await res.text();
  console.log(`live bytes: ${html.length} (${url.replace(/token=[^&]+/, "token=…")})`);
}

// Same slicing contract as the Go anchor: named function, indented at line
// start, terminated by the next sibling `function` at the same indent.
function sliceFunction(source, name) {
  const at = source.indexOf(`function ${name}(`);
  assert.ok(at >= 0, `served HTML must contain function ${name}`);
  const indent = source.slice(source.lastIndexOf("\n", at) + 1, at);
  const rest = source.slice(at);
  let end = rest.length;
  for (const marker of [`\n${indent}function `, `\n${indent}async function `]) {
    const j = rest.indexOf(marker);
    if (j >= 0 && j < end) end = j;
  }
  return rest.slice(0, end);
}

const apply = sliceFunction(html, "applyVisualViewportOcclusion");

// Choice A is a ban on CODE, not on prose: the function documents the very
// geometry it stopped reading.
const code = apply.replace(/\/\/[^\n]*/g, "").replace(/\/\*[\s\S]*?\*\//g, "");
for (const banned of ["offsetTop", "visibleBottom", "covered", "app.style.height", "app.style.position", "documentElement.style", "document.body.style", "workspace.style", "terminalContainer.style", "container.style", "scrollTop", "scrollTo", "scrollBy", "requestAnimationFrame"]) {
  assert.ok(!code.includes(banned), `live applyVisualViewportOcclusion still touches ${banned}`);
}
console.log("✓ live code reads neither offsetTop nor visibleBottom, and writes neither the shell nor the root");

// The measured iPad Pro 11 landscape geometry: layout viewport 702 tall, and the
// 20px env(safe-area-inset-bottom) padding on #app puts #terminal-container — and
// therefore every host at inset:0 inside it — with its floor at 682.
const LAYOUT = 702;
const HOST_FLOOR = 682;
// Contract v3.1: a shortfall under this fraction of the layout height is a shallow
// overlay and owes nothing; at or over it an explicit larger keyboard owes
// its raw shortfall. 702 * 0.25 = 175.5px is the boundary: it
// sits ABOVE the thin floating strip (68.5, exempt) and ABOVE the measured 147px
// Magic-keyboard retract frames (exempt too — lifting the terminal on that transient
// was the ~8.6-row bounce plus the raised Transport the device reported back), and
// BELOW the full soft keyboard's 428px shortfall, which still avoids.
const SMALL_OCCLUSION_RATIO = 0.25;

// Full-height, out-of-flow #app. Its inline height setter THROWS: v2.2 pinned
// this box to vv.height and v3.0 never may, so the retired pin fails HERE rather
// than being forgiven by an inert stub. The rect is the layout height, always.
const app = { style: {} };
Object.defineProperty(app.style, "height", {
  configurable: true,
  get: () => "",
  set: (value) => { throw new Error(`the live bundle resized #app: style.height = ${JSON.stringify(value)} — v3.0 keeps the shell FULL HEIGHT`); },
});
Object.defineProperty(app, "getBoundingClientRect", {
  value: () => ({ top: 0, bottom: LAYOUT, height: LAYOUT, left: 0, right: 1194, width: 1194 }),
});

function makeHost(id) {
  const host = { tagName: "DIV", dataset: { instanceId: id }, style: { position: "absolute", display: "none" }, writes: 0 };
  Object.defineProperty(host.style, "bottom", {
    configurable: true,
    get: () => (host.style.__bottom === undefined ? "" : host.style.__bottom),
    set: (value) => { host.writes += 1; host.style.__bottom = value; },
  });
  Object.defineProperty(host, "getBoundingClientRect", {
    value: () => {
      const offset = parseFloat(host.style.bottom || "0") || 0;
      return { top: 0, bottom: HOST_FLOOR - offset, height: HOST_FLOOR - offset, left: 0, right: 1194, width: 1194 };
    },
  });
  return host;
}

// The two overlay children of #workspace, live by the same rules: a containing
// block the keyboard does NOT shorten, so their OWN bottom is the only thing that
// keeps the quick-keys entry / panel and the transport strip reachable. Each is
// authored at a DIFFERENT bottom (the column 20px above the strip), so each owes
// a different number — which is exactly what the assertions below compare, against
// literal 448 / 428 rather than a re-run of the same formula.
const CONTROLS_TOP = 20;
const CONTROLS_AUTHORED_BOTTOM = 20;
const WORKSPACE_BOTTOM = HOST_FLOOR;

function makeOverlayChild(id, top, authoredBottom) {
  const el = { id, tagName: "DIV", style: {}, writes: 0 };
  Object.defineProperty(el.style, "bottom", {
    configurable: true,
    get: () => (el.style.__bottom === undefined ? "" : el.style.__bottom),
    set: (value) => { el.writes += 1; el.style.__bottom = value; },
  });
  Object.defineProperty(el, "getBoundingClientRect", {
    value: () => {
      const offset = parseFloat(el.style.bottom || String(authoredBottom)) || 0;
      const bottom = WORKSPACE_BOTTOM - offset;
      return { top, bottom, height: bottom - top, left: 0, right: 44, width: 44 };
    },
  });
  return el;
}

const controls = makeOverlayChild("terminal-controls", CONTROLS_TOP, CONTROLS_AUTHORED_BOTTOM);
const statusBar = makeOverlayChild("status-bar", WORKSPACE_BOTTOM - 17, 0);

const hosts = [makeHost("live-a"), makeHost("live-b")];
const container = {
  getBoundingClientRect: () => ({ top: 0, bottom: HOST_FLOOR, height: HOST_FLOOR, left: 0, right: 1194, width: 1194 }),
  querySelectorAll(selector) {
    assert.equal(selector, ":scope > [data-instance-id]", "the host selector the live applier queries changed; re-audit this script");
    return hosts;
  },
  appendChild(node) { hosts.push(node); return node; },
};

const vv = { height: LAYOUT, offsetTop: 0, scale: 1, addEventListener() {}, removeEventListener() {} };
// A NON-SCROLLABLE root, matching the served CSS (`overflow: clip` on both root
// boxes). v2.1's stub forgave a scroll write; this one refuses, so "the root is
// never written" is executed against the live bytes instead of asserted.
const ban = (what) => { throw new Error(`the live bundle wrote the root: ${what}`); };
const documentElement = {
  clientHeight: LAYOUT,
  scrollHeight: LAYOUT,
  get scrollTop() { return 0; },
  set scrollTop(value) { ban(`documentElement.scrollTop = ${value}`); },
};
const rootBox = documentElement;
const liveDocument = {
  documentElement,
  body: rootBox,
  scrollingElement: rootBox,
  createElement: (tagName) => ({ tagName: String(tagName).toUpperCase(), dataset: {}, style: {} }),
  getElementById: (id) => (id === "app" ? app
    : id === "terminal-container" ? container
      : id === "terminal-controls" ? controls
        : id === "status-bar" ? statusBar
          : null),
};
const liveWindow = {
  visualViewport: vv,
  scrollTo: () => ban("window.scrollTo(...)"),
  scrollBy: () => ban("window.scrollBy(...)"),
  get scrollY() { return 0; },
};
const factory = new Function("document", "window", `${apply}\n\nreturn applyVisualViewportOcclusion;`);
const avoid = factory(liveDocument, liveWindow);

const bottoms = () => hosts.map((host) => host.style.bottom);
const hostBottoms = () => hosts.map((host) => host.getBoundingClientRect().bottom);
const writeCount = () => hosts.reduce((n, host) => n + host.writes, 0);
const controlsBottom = () => controls.style.bottom;
const statusBarBottom = () => statusBar.style.bottom;
const overlayWrites = () => controls.writes + statusBar.writes;
const controlsHeight = () => controls.getBoundingClientRect().height;

// At rest: nothing is covered, so the hosts sit flush and the shell is full.
avoid();
assert.deepEqual(bottoms(), ["0px", "0px"], "at rest every host owns bottom 0px");
assert.deepEqual(hostBottoms(), [HOST_FLOOR, HOST_FLOOR]);

// The measured table: the shortfall is paid into the HOST above the v3.1 ratio
// floor, and the same terminal geometry the old full-page pin produced comes out of
// it. Only 274 (raw 428) clears the 175.5 floor and pays 428 → host bottom 254;
// 555 (raw 147 = 20.9%) and 633.5 (raw 68.5 = 9.8%) both sit under it and pay 0 —
// the Magic-keyboard retract frames stay flush instead of bouncing the terminal.
// `owed` below is the floored shortfall — the single number every box is judged on,
// so no fixture can quietly restate the retired no-floor arithmetic.
const table = [274, 555, 633.5];
for (const visible of table) {
  const raw = LAYOUT - visible;
  const owed = raw >= LAYOUT * SMALL_OCCLUSION_RATIO ? raw : 0;
  const bottom = owed + "px";
  const hostBottom = HOST_FLOOR - owed;
  vv.height = visible;
  avoid();
  assert.deepEqual(bottoms(), [bottom, bottom], `vv.height ${visible} must set EVERY host bottom to ${bottom} (raw ${raw}, owed ${owed})`);
  assert.deepEqual(hostBottoms(), [hostBottom, hostBottom], `vv.height ${visible} must land the host at ${hostBottom}`);
  assert.equal(app.getBoundingClientRect().bottom, LAYOUT, "the shell stays full height while the keyboard covers the rest");
  // The overlay half, LITERAL numbers — 20 + owed for the column, the owed value
  // alone for the strip. A copy of the host target onto the column would leave the
  // quick-keys panel buried in the transport strip's band, and nothing else in this
  // file would notice. At owed 0 each returns to its OWN authored value.
  assert.equal(controlsBottom(), (20 + owed) + "px", `vv.height ${visible}: #terminal-controls owes bottom ${20 + owed}px (authored 20px + owed)`);
  assert.equal(statusBarBottom(), owed + "px", `vv.height ${visible}: #status-bar owes bottom ${owed}px (authored 0)`);
  assert.equal(controls.getBoundingClientRect().bottom, HOST_FLOOR - 20 - owed, "the column's real bottom lifts by the owed shortfall, keeping its 20px clearance");
  assert.equal(statusBar.getBoundingClientRect().bottom, HOST_FLOOR - owed, "the strip's real bottom lifts by the owed shortfall itself");
  // The strip/column must clear the keyboard ONLY in an avoiding frame. In an
  // exempt thin strip they legitimately stay full height and are overlapped on
  // purpose (the user's own request), so demanding clearance there would fail the
  // very case v3.1 protects.
  if (owed > 1) {
    assert.ok(controls.getBoundingClientRect().bottom <= visible + 1, `the column must sit above the keyboard at ${visible} (avoiding)`);
  }
  assert.ok(controls.getBoundingClientRect().top === CONTROLS_TOP, "top:20px is never written — the quick-keys ENTRY button does not jump");
  assert.equal(controlsHeight(), HOST_FLOOR - CONTROLS_TOP - CONTROLS_AUTHORED_BOTTOM - owed, `the column's usable height shrinks by exactly ${owed}px (what the panel's max-height:100% resolves against)`);
}
console.log("✓ 274 paid 428px into every host, the column (+20) and the strip; 555 (147=20.9%) and 633.5 (68.5=9.8%) are exempt under the 0.25 floor and pay 0, with the shell untouched");

// Pan immunity: the occlusion is client-space vv.height against the stable layout
// height, so the target does not move — and because writes are guarded, nothing is
// written at all.
vv.height = 274;
avoid();
const afterSettle = writeCount();
for (const offsetTop of [12.5, 103, 237, 265.5, 428, 0]) {
  vv.offsetTop = offsetTop;
  avoid();
  assert.deepEqual(bottoms(), ["428px", "428px"], `pan ${offsetTop} must not change the avoidance`);
}
assert.equal(writeCount(), afterSettle, "a pan must not rewrite a style (write-only-on-change)");
// The overlay boxes are guarded by the SAME rule: every extra write would
// invalidate the layout of a page the user is looking at and re-fire the host
// ResizeObserver chain, so the column and the strip must stay silent too.
const overlaySettled = overlayWrites();
for (const offsetTop of [12.5, 237, 428, 0]) {
  vv.offsetTop = offsetTop;
  avoid();
}
assert.equal(overlayWrites(), overlaySettled, "a pan must not rewrite the column or the strip either");
console.log("✓ every pan offset left both the lift and the write count untouched (hosts, column and strip)");

// 633.5 — the thin floating strip. raw shortfall 68.5 < 702*0.25=175.5, so under
// contract v3.1 it is EXEMPT: owed 0. Coming off the avoiding 274 frame above, the
// applier must actively RESTORE the hosts to flush rather than keep the last lift —
// this is the exemption doing real work, not merely a frame that happens to be flat.
// (555 → raw 147 is exempt by the SAME arithmetic and is covered by the table above;
// before this round it paid 147, and that lift was the bounce the device reported.)
vv.height = 633.5;
avoid();
assert.deepEqual(bottoms(), ["0px", "0px"], "a thin strip under the ratio is exempt: hosts restored to bottom 0px, terminal never compressed");
assert.deepEqual(hostBottoms(), [HOST_FLOOR, HOST_FLOOR], "and every host lands back flush at the host floor");
console.log("✓ a 633.5 strip (raw 68.5 < 175.5) is exempt at owed 0 — hosts restored flush, not lifted");

// The REAL floating overlay (b90643be…, seq388): vv.height == clientHeight, so
// occlusion is 0 by arithmetic and the hosts go back to flush.
vv.height = LAYOUT;
avoid();
assert.deepEqual(bottoms(), ["0px", "0px"], "a pure overlay covers nothing in client space: bottom back to 0px");
// occlusion 0 restores each overlay to its OWN authored value: the column back to
// 20px (NOT 0px — that would drop it into the transport strip) and the strip back
// to 0px.
assert.equal(controlsBottom(), "20px", "occlusion 0 restores the column's authored 20px, not the host value 0px");
assert.equal(statusBarBottom(), "0px", "and the strip's authored 0px");
const afterRestore = overlayWrites();
avoid();
avoid();
assert.equal(overlayWrites(), afterRestore, "the restore is one write per box, then silence");

// Zoom exemption: vv.height shrinks with the scale, and the short-circuit must
// stop that from lifting a page the user is panning around.
vv.height = 274;
avoid();
vv.scale = 0.75;
vv.height = 365; // measured at 0.75 zoom: 570.7px short of the layout height
avoid();
assert.deepEqual(bottoms(), ["0px", "0px"], "zoom must opt out (this is why 0.75 never compresses)");
// "Opt out entirely" has to mean every box the avoidance writes: a pinch that
// left the column at 448px would float the quick-keys entry off the bottom of a
// page the user is panning around.
assert.equal(controlsBottom(), "20px", "zoom restores the column's authored 20px too");
assert.equal(statusBarBottom(), "0px", "…and the strip's 0px");
vv.height = 633.5;
avoid();
assert.equal(controlsBottom(), "20px", "while zoomed, a shortfall owes nothing to the column");
vv.scale = 1;
vv.height = 274; // the keyboard is still up at the same visible height
avoid();
assert.deepEqual(bottoms(), ["428px", "428px"], "back at scale 1 the shortfall is paid again");
assert.equal(controlsBottom(), "448px", "…and the column lifts again at scale 1");
assert.equal(statusBarBottom(), "428px", "…and so does the strip");
console.log("✓ zoom exemption intact, and the judgment re-arms at scale 1");

// A host born under a covered bottom must not wait for the next event: the
// shipped createTerminalHost runs the same call.
const createHost = sliceFunction(html, "createTerminalHost");
const hostFactory = new Function("document", "applyVisualViewportOcclusion", `${createHost}\n\nreturn createTerminalHost;`);
vv.height = 274;
vv.scale = 1;
const born = hostFactory(liveDocument, avoid)("live-newborn");
assert.equal(born.style.bottom, "428px", "a host created while the keyboard is up is born lifted, not flush");
assert.equal(liveDocument.getElementById("terminal-container").querySelectorAll(":scope > [data-instance-id]").length, 3);
console.log("✓ a host created under a covered bottom is lifted at creation");

// A second viewport size (400), where the floor itself moves: 400 * 0.25 = 100px.
// The point of the block is now the FLOOR, not its absence. ABOVE the floor the
// shortfall is paid EXACTLY — half a pixel is half a pixel, no 1px rounding (v2.2
// put a 1px tolerance on the retired PIN decision; the A0 SOP's 1px figure is a
// tolerance on the VERDICT's comparisons, never a floor on the write). BELOW the
// floor it is exempt and collapses to 0. Stepping across 100px flips the write.
vv.scale = 1;
documentElement.clientHeight = 400;
vv.height = 299.5; // raw 100.5 >= 100: fractional, paid exactly
avoid();
assert.deepEqual(bottoms(), ["100.5px", "100.5px", "100.5px"], "above the floor a half-pixel shortfall is paid as half a pixel");
vv.height = 300; // raw exactly 100 = the floor: boundary is inclusive
avoid();
assert.deepEqual(bottoms(), ["100px", "100px", "100px"], "the floor is inclusive — a shortfall of exactly 0.25*layout is paid");
vv.height = 301; // raw 99 < 100: just under the floor, exempt
avoid();
assert.deepEqual(bottoms(), ["0px", "0px", "0px"], "under the floor the shortfall is exempt and collapses to 0 — the shallow overlay must not compress");
vv.height = 298.5; // raw 101.5 >= 100: back above the floor, exact again
avoid();
assert.deepEqual(bottoms(), ["101.5px", "101.5px", "101.5px"], "stepping back above the floor pays the raw shortfall exactly");
vv.height = 400;
avoid();
assert.deepEqual(bottoms(), ["0px", "0px", "0px"], "exactly full: nothing owed");
console.log("✓ at a second viewport the ratio floor is exact: fractional paid above 0.25*layout, exempt to 0 below it");

// The structural half: driving every path must leave the root untouched, and the
// stub throws, so reaching the end of this block IS the assertion.
documentElement.clientHeight = LAYOUT;
for (const height of [274, 555, 633.5, 702]) {
  vv.height = height;
  vv.offsetTop = 428;
  avoid();
  assert.equal(app.getBoundingClientRect().bottom, LAYOUT, "full height in every state");
}
console.log("✓ every geometry moved ONLY the terminal's own three bottoms — the root never received a scroll write, #app and #workspace never received a size");

console.log(`\nPASS — verified against the ${offline ? "repo" : "served"} bytes: full-height shell, ${hosts.length} PTY hosts + the quick-keys column + the status bar avoiding locally.`);
