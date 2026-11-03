#!/usr/bin/env node
// Verify the DEPLOYED bundle's PTY-local avoidance against contract v3.2
// ("full-height shell + PTY-LOCAL avoidance SCOPED TO SOFT-KEYBOARD SESSIONS,
// non-scrollable root", issue #116). v3.2 = the v3.0 base (full-height shell,
// host-local lift, overlay half) with the avoidance gated by the keyboard-session
// state machine — plus the fixed #status-bar and the wheel isolation from v3.1.
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
//   - A shortfall is paid only INSIDE A KEYBOARD SESSION (contract v3.2). The raw
//     shortfall is clientHeight - vv.height. A session ENTERS at raw >= 0.50 *
//     clientHeight with editable focus (document.activeElement TEXTAREA/INPUT or
//     contenteditable) — the explicit soft keyboard (274 → raw 428) — and while it
//     HOLDS there is NO ratio line: every retract frame pays its RAW (555 → 147,
//     495.9 → 206.1, 362 → 340). The session LEAVES at raw <= 1, at zoom, or when
//     focus stops being editable, and after leaving, a shortfall UNDER the 0.50
//     line can never re-open it — the thin floating strip (633.5 → raw 68.5) and
//     any shallow state simply overlap a terminal that stays flush. The retired
//     v3.1 0.25 STATELESS floor is NOT replayed: its 175.5px line sat BELOW the
//     measured 206.1px and 147px retransition intermediates, and paying them was the
//     ~8.6-row bounce plus the climbing Transport the device reported back — no
//     fraction can tell those live-session frames from a fresh shallow overlay, so
//     the gate is STATE, and the one surviving fraction (0.50) only opens sessions.
//     A genuine magic-hint overlay (vv.height = clientHeight) still lands on
//     occlusion 0 by arithmetic.
//   - EVERY host is lifted, hidden and inactive included, and a host born under a
//     covered bottom is lifted at creation — inside a live session only; born
//     outside one it is born flush.
//   - pinch zoom (scale != 1) opts out entirely AND CLOSES the session (its raw is
//     a zoom artifact); restore bottom: 0px.
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
// Contract v3.2: this fraction of the layout height is the session ENTRY line and
// NOTHING ELSE. At or over it, with editable focus and no session open, the keyboard
// is explicit and opens avoidance; once open, the session pays RAW at every height
// until raw closes (<= 1), zoom, or focus loss. 702 * 0.50 = 351px is the entry
// line: the full soft keyboard (274 → raw 428) opens it; the thin floating strip
// (68.5) and a never-opened 555 frame (147) never do. The retired v3.1 floor
// (SMALL_OCCLUSION_RATIO 0.25 → 175.5) is NOT here: it applied its line to the
// RUNNING occlusion and re-lifted the terminal mid-retract — see the HOLD block
// below, where the same 147 is paid BECAUSE a session entered.
const KEYBOARD_ENTRY_RATIO = 0.25;
const KEYBOARD_REENTRY_RATIO = 0.50;

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
  // The focus half of the v3.2 session gate. The terminal's editable helper is a
  // TEXTAREA (xterm's input helper), so keyboard frames legitimately carry it;
  // setFocus below swaps it for BODY / contenteditable to drive the edge cases.
  activeElement: { tagName: "TEXTAREA", isContentEditable: false },
  getElementById: (id) => (id === "app" ? app
    : id === "terminal-container" ? container
      : id === "terminal-controls" ? controls
        : id === "status-bar" ? statusBar
          : null),
};
const setFocus = (el) => { liveDocument.activeElement = el; };
const liveWindow = {
  visualViewport: vv,
  scrollTo: () => ban("window.scrollTo(...)"),
  scrollBy: () => ban("window.scrollBy(...)"),
  get scrollY() { return 0; },
};
// The v3.2 applier keeps ONE module-level boolean (`terminalAvoidanceActive`) that
// lives ABOVE the function slice, exactly like `sidebarCollapsed` does in the node
// harness. The slice ships only the function, so the factory must declare that
// variable in the same closure the sliced function references — otherwise the live
// bytes would throw on the first `terminalAvoidanceActive =` and never prove the
// session machine. It starts CLOSED, matching the shipped page load.
const factory = new Function("document", "window", `let terminalAvoidanceActive = false;\nlet terminalAvoidanceRecovering = false;\nlet terminalAvoidanceGeometryGrace = false;\nlet previousTerminalRaw = 0;\nlet previousTerminalLayoutHeight = 0;\nlet previousTerminalVVWidth = 0;\n${apply}\n\nreturn applyVisualViewportOcclusion;`);
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

// The v3.2 gate must be visible IN THE LIVE BYTES themselves, not just inferred
// from the bottoms: one boolean, the focus read, the entry-only line. (The Go
// anchors check the served text; this ties the FETCHED bytes to the same shape.)
assert.ok(code.includes("terminalAvoidanceActive"), "live code carries the keyboard-session boolean");
assert.ok(code.includes("document.activeElement"), "live code reads the focus before paying anything");
assert.ok(code.includes("KEYBOARD_ENTRY_RATIO"), "live code declares the entry-only fraction");
console.log("✓ live bytes carry the v3.2 session gate (terminalAvoidanceActive + document.activeElement + KEYBOARD_ENTRY_RATIO)");

// The measured session, replayed in capture order. ONE helper runs the full box
// check per frame; `owed` is passed in from the SESSION rule, never re-derived
// inside the helper, so a helper that floored or lifted on its own would disagree
// with the caller and fail. The sequence is the contract:
//   ENTER at 274 (raw 428 >= 351)  → every box pays 428, hosts land at 254;
//   HOLD 362 → raw 340 pays 340     (mid-session, NO line — the old 0.25 floor
//   HOLD 495.9 → raw 206.1 pays it   would have snapped these to 0 mid retract);
//   HOLD 555 → raw 147 pays 147;
//   the column pays owed + 20, the strip owed alone, literal numbers each frame.
const sessionFrame = (visible, owed, label) => {
  const bottom = owed + "px";
  const hostBottom = HOST_FLOOR - owed;
  vv.height = visible;
  avoid();
  assert.deepEqual(bottoms(), [bottom, bottom], `${label}: vv.height ${visible} must set EVERY host bottom to ${bottom}`);
  assert.deepEqual(hostBottoms(), [hostBottom, hostBottom], `${label}: vv.height ${visible} must land the host at ${hostBottom}`);
  assert.equal(app.getBoundingClientRect().bottom, LAYOUT, "the shell stays full height while the keyboard covers the rest");
  // The overlay half, LITERAL numbers — 20 + owed for the column, the owed value
  // alone for the strip. A copy of the host target onto the column would leave the
  // quick-keys panel buried in the transport strip's band, and nothing else in this
  // file would notice. At owed 0 each returns to its OWN authored value.
  assert.equal(controlsBottom(), (20 + owed) + "px", `${label}: #terminal-controls owes bottom ${20 + owed}px (authored 20px + owed)`);
  assert.equal(statusBarBottom(), owed + "px", `${label}: #status-bar owes bottom ${owed}px (authored 0)`);
  assert.equal(controls.getBoundingClientRect().bottom, HOST_FLOOR - 20 - owed, "the column's real bottom lifts by the owed shortfall, keeping its 20px clearance");
  assert.equal(statusBar.getBoundingClientRect().bottom, HOST_FLOOR - owed, "the strip's real bottom lifts by the owed shortfall itself");
  // The strip/column must clear the keyboard ONLY inside a live avoiding session.
  // Outside one they legitimately stay full height and are overlapped on purpose
  // (the user's own request), so demanding clearance there would fail the very
  // case v3.2 protects.
  if (owed > 1) {
    assert.ok(controls.getBoundingClientRect().bottom <= visible + 1, `the column must sit above the keyboard at ${visible} (avoiding)`);
  }
  assert.ok(controls.getBoundingClientRect().top === CONTROLS_TOP, "top:20px is never written — the quick-keys ENTRY button does not jump");
  assert.equal(controlsHeight(), HOST_FLOOR - CONTROLS_TOP - CONTROLS_AUTHORED_BOTTOM - owed, `the column's usable height shrinks by exactly ${owed}px (what the panel's max-height:100% resolves against)`);
};
sessionFrame(274, 428, "ENTER 274: the explicit soft keyboard lifts the terminal");
sessionFrame(495.9, 0, "RECOVER 495.9: a same-geometry decrease exits instead of holding 206.1px");
sessionFrame(555, 0, "Magic overlay 555 stays closed after recovery");
console.log("✓ the keyboard entered at 428, recovery restored all three boxes, and the floating intermediate did not re-open it");

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

// A further decrease in the same geometry exits immediately; it is not held as
// a partial 68.5px avoidance.
vv.height = 633.5;
avoid();
assert.deepEqual(bottoms(), ["0px", "0px"], "recovery at 633.5 restores the hosts instead of holding 68.5px");
assert.equal(controlsBottom(), "20px", "the column returns to its authored 20px");
assert.equal(statusBarBottom(), "0px", "and Transport returns to 0px");
// The REAL floating overlay / keyboard close (b90643be…, seq388): vv.height ==
// clientHeight → raw 0 ≤ 1 → the session LEAVES, hosts RESTORE to flush rather
// than keep the last lift — this restore is the exit doing real work, and occlusion
// 0 restores each overlay to its OWN authored value: the column back to 20px (NOT
// 0px — that would drop it into the transport strip) and the strip back to 0px.
vv.height = LAYOUT;
avoid();
assert.deepEqual(bottoms(), ["0px", "0px"], "raw <= 1 closes the session: bottom back to 0px");
assert.equal(controlsBottom(), "20px", "occlusion 0 restores the column's authored 20px, not the host value 0px");
assert.equal(statusBarBottom(), "0px", "and the strip's authored 0px");
const afterRestore = overlayWrites();
avoid();
avoid();
assert.equal(overlayWrites(), afterRestore, "the restore is one write per box, then silence");
// A CLOSED session does not reopen on shallow heights, editable focus included —
// 555 (raw 147) and 633.5 (raw 68.5) sit under the 351px entry line and pay 0.
// THE SAME two heights the HOLD block above paid raw: the state, not the size,
// answers.
vv.height = 555;
avoid();
assert.deepEqual(bottoms(), ["0px", "0px"], "closed session + editable focus at 555: the closed session holds, hosts stay flush");
vv.height = 633.5;
avoid();
assert.deepEqual(bottoms(), ["0px", "0px"], "…and at 633.5: the thin strip overlaps a terminal that never moved");
// A NEW soft keyboard crosses the entry line and opens a NEW session.
sessionFrame(274, 428, "a NEW keyboard at 274 RE-ENTERS the session");
console.log("✓ live session followed the retract through 633.5 (raw 68.5), CLOSED at full height with the authored restore, stayed flush at shallow 555/633.5 after leaving, and a new 274 re-opened it");

// Zoom: vv.height shrinks with the scale, and the short-circuit must stop that from
// lifting a page the user is panning around — AND it CLOSES the live session, so a
// post-pinch shallow frame cannot masquerade as mid-session raw.
vv.height = 274;
avoid();
vv.scale = 0.75;
vv.height = 365; // measured at 0.75 zoom: 570.7px short of the layout height
avoid();
assert.deepEqual(bottoms(), ["0px", "0px"], "zoom must opt out AND close the session (this is why 0.75 never compresses)");
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
assert.deepEqual(bottoms(), ["428px", "428px"], "back at scale 1 the raw 428 crosses the entry line again and a NEW session opens");
assert.equal(controlsBottom(), "448px", "…and the column lifts again at scale 1");
assert.equal(statusBarBottom(), "428px", "…and so does the strip");
console.log("✓ zoom opted out and CLOSED the session; at scale 1 the entry line re-armed the judgment");

// FOCUS edges, on the live bytes: the session is LIVE at 274 (raw 428, TEXTAREA
// helper focused). A NON-editable focus (BODY) must END it — hosts restore flush
// even with the same covering geometry — and a contenteditable focus (the gate the
// product reads `isContentEditable` for) must open a session at the same raw. The
// soft keyboard here is whatever iOS shows; the session follows the FOCUS, which is
// why the probe reports activeElementTag and this stub can swap it.
sessionFrame(274, 428, "TEXTAREA focus keeps the session live");
vv.height = 555; // same-geometry decrease is already recovery
avoid();
assert.deepEqual(bottoms(), ["0px", "0px"], "recovery exits at 555 instead of holding 147px");
setFocus({ tagName: "BODY", isContentEditable: false });
avoid();
assert.deepEqual(bottoms(), ["0px", "0px"], "non-editable focus ENDS the session: hosts flush at the same raw 147");
assert.equal(controlsBottom(), "20px", "…and the column drops back to its authored 20px");
vv.height = 274; // the keyboard is gone with the focus; even a tall raw stays 0
avoid();
assert.deepEqual(bottoms(), ["0px", "0px"], "even raw 428 pays NOTHING without editable focus");
setFocus({ tagName: "DIV", isContentEditable: true });
avoid();
assert.deepEqual(bottoms(), ["428px", "428px"], "contenteditable focus ENTERS at raw 428 — the isContentEditable half of the gate");
setFocus({ tagName: "TEXTAREA", isContentEditable: false });
console.log("✓ focus edges executed: BODY ended the session (raw 428 paid nothing), contenteditable re-entered it");

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

// A second viewport size (400), where the ENTRY LINE itself moves: 400 * 0.50 =
// 200px. The point is the session line, fractional and stateful. FIRST the session
// must be CLOSED (the previous block left it live at scale 1 / 274), so the first
// frame here rests full height (raw 0 → LEAVES). Then a shallow NEVER-entered frame
// (raw 198.5 < 200) collapses to 0; the frame that touches the line (raw exactly
// 200) ENTERS and pays its RAW; while live it HOLDS shallower raw EXACTLY (half a
// pixel is half a pixel — no 1px rounding, v2.2's 1px tolerance was on the retired
// PIN decision, never a floor on the write); raw <= 1 closes it; and a NEW tall
// frame re-enters. Stepping across 200px flips the write because STATE flips.
vv.scale = 1;
vv.height = LAYOUT; // close the still-live session first: raw 0
avoid();
assert.deepEqual(bottoms(), ["0px", "0px", "0px"], "the second viewport opens only after the session is closed flush");
documentElement.clientHeight = 400; // entry line moves to 200px
vv.height = 300.5; // raw 99.5 < the 100px normal entry line
avoid();
assert.deepEqual(bottoms(), ["0px", "0px", "0px"], "a shallow shortfall under 25% stays exempt (99.5 < 100)");
vv.height = 300; // raw exactly 100 = 25%: ENTER, inclusive
avoid();
assert.deepEqual(bottoms(), ["100px", "100px", "100px"], "raw exactly 25% enters and pays its raw");
vv.height = 299.5; // raw 100.5: stable/increasing live session follows exactly
avoid();
assert.deepEqual(bottoms(), ["100.5px", "100.5px", "100.5px"], "a live session follows the exact fractional shortfall");
vv.height = 400; // raw 0: LEAVE
avoid();
assert.deepEqual(bottoms(), ["0px", "0px", "0px"], "raw <= 1 leaves the session at the second viewport too");
vv.height = 300.5; // raw 99.5: closed, shallow, must NOT re-open
avoid();
assert.deepEqual(bottoms(), ["0px", "0px", "0px"], "a closed session does not re-open on a shallow raw");
vv.height = 200; // raw 200 = the 50% post-recovery re-entry line
avoid();
assert.deepEqual(bottoms(), ["200px", "200px", "200px"], "a fresh 50% keyboard re-enters after recovery");
vv.height = 400;
avoid();
assert.deepEqual(bottoms(), ["0px", "0px", "0px"], "exactly full: nothing owed");
console.log("✓ at a second viewport the SESSION line is exact: ENTER at raw>=0.50*layout, HOLD shallower raw live, LEAVE at raw<=1, never re-open shallow");

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

console.log(`\nPASS — verified against the ${offline ? "repo" : "served"} bytes: full-height shell, a soft-keyboard SESSION gate (enter/hold/leave) lifting ${hosts.length} PTY hosts + the quick-keys column + the status bar locally, flush outside every session.`);
