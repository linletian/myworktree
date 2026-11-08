// Regression tests for the iPadOS white-band paint (issue #116).
//
// A0 real-device calibration (docs/plans/fix-issue116/evidence/, session
// 997d18dc48ee49c84f6fbf5b797969f7, 263/263 snapshots) established the root
// cause as a MISSING PAINT, not a missing height:
//
//   html / body / #app  -> rgba(0,0,0,0)   (nothing paints)
//   #workspace          -> rgb(11,16,32)   (var(--term-bg); the only painted layer)
//   #workspace.bottom -> visible bottom = a CONSTANT 20.0px in EVERY state
//                        (landscape, portrait, soft keyboard up, floating bar pinned)
//
// Those 20px are #app's `padding-bottom: env(safe-area-inset-bottom)`. With no
// background beneath them the UA root canvas showed through, and `color-scheme`
// was never declared in the document, so the canvas was WHITE — the band in the
// issue report. #app.bottom already equalled the visible bottom, so no height
// change could ever have removed it.
//
// Parse discipline: one linear pass over the <style> block, cached at module
// load. No global regexes and no manual lastIndex mutation — an earlier version
// of this file combined `/([^{}]+)\{/g` with `open.lastIndex = i` inside a loop
// and hung the test runner indefinitely. Keep it boring.
//
// Run: node --test testdata/canvas_paint.test.mjs
import test from "node:test";
import assert from "node:assert/strict";
import { readFileSync } from "node:fs";
import { fileURLToPath } from "node:url";
import { dirname, join } from "node:path";

const here = dirname(fileURLToPath(import.meta.url));
const indexSource = readFileSync(join(here, "..", "static", "index.html"), "utf8");

function stripComments(text) {
  return text.replace(/\/\*[\s\S]*?\*\//g, "");
}

// Collect `{selector: body}` for every rule in the document's <style> blocks via
// a single O(n) scan. Comment-free text, so a declaration mentioned only in
// prose cannot satisfy an assertion. `@media` wrappers are descended into so a
// rule defined inside a media query is still found by its own selector.
function collectRules(source) {
  const css = stripComments(source);
  const rules = [];
  const stack = [];
  let buf = "";
  for (let i = 0; i < css.length; i++) {
    const c = css[i];
    if (c === "{") {
      stack.push(buf.trim());
      buf = "";
    } else if (c === "}") {
      const selector = stack.pop();
      if (selector === undefined) {
        buf = "";
        continue;
      }
      // At-rules with blocks are containers, not style rules: keep walking
      // their contents (already handled by the stack) and index them by
      // their prelude so theme overrides stay distinguishable.
      rules.push({ selector, body: buf });
      buf = "";
    } else {
      buf += c;
    }
  }
  return rules;
}

const rules = collectRules(indexSource);

function bodiesFor(selectorRe) {
  return rules.filter((r) => selectorRe.test(r.selector)).map((r) => r.body);
}

function decl(body, prop) {
  const m = stripComments(body).match(new RegExp("(?:^|[;}])\\s*" + prop + "\\s*:([^;]+)", "i"));
  return m ? m[1].trim() : null;
}

test("the root canvas rule exists", () => {
  assert.equal(
    bodiesFor(/^(?:body,\s*html|html,\s*body)$/).length,
    1,
    "expected exactly one `body, html` rule in index.html (the paint contract below pins it)",
  );
});

test("the root canvas is painted with the CHROME token, not the terminal ink", () => {
  const body = bodiesFor(/^(?:body,\s*html|html,\s*body)$/)[0] || "";
  assert.equal(
    decl(body, "background"),
    "var(--safe-area-bg)",
    "the approved safe-area visual benchmark uses a dedicated chrome canvas token, never the terminal ink",
  );
});

test("the paint is a bare token, never a hard-coded colour", () => {
  const body = bodiesFor(/^(?:body,\s*html|html,\s*body)$/)[0] || "";
  const value = decl(body, "background") || "";
  assert.match(value, /^var\(--[a-z-]+\)$/, `root background must be a bare var(--token), got "${value}"`);
  assert.ok(
    !/#[0-9a-f]{3,8}|rgba?\(|\bwhite\b|\bblack\b/i.test(value),
    `root background must not hard-code a literal, got "${value}" — a literal drifts from the theme`,
  );
});

test("color-scheme is declared so the UA canvas itself is theme-aware", () => {
  const body = bodiesFor(/^(?:body,\s*html|html,\s*body)$/)[0] || "";
  assert.equal(
    decl(body, "color-scheme"),
    "light dark",
    "issue #116: the root rule must declare `color-scheme: light dark` — the same " +
      "signal the @media (prefers-color-scheme) blocks already key off, so the UA " +
      "canvas, form controls and scrollbars stop defaulting to light",
  );
});

test("--term-bg is pure black inside the terminal's rounded card", () => {
  const defined = rules
    .flatMap((r) => stripComments(r.body).match(/--term-bg\s*:[^;]+;?/g) || [])
    .map((d) => d.replace(/--term-bg\s*:\s*/, "").replace(/;$/, "").trim());
  assert.deepEqual(defined, ["#000000"], "the terminal card and xterm share one pure-black token in both system themes");
});

test("existing PTY canvases follow live system color-scheme changes", () => {
  assert.match(indexSource, /function syncTerminalTheme\(\)/, "the live theme synchronizer exists");
  assert.match(indexSource, /matchMedia\("\(prefers-color-scheme: dark\)"\)/, "it listens to the same system theme signal as the CSS");
  assert.match(indexSource, /session\.term\.options\.theme\s*=\s*Object\.assign\(\{\},\s*session\.term\.options\.theme,\s*\{ background \}\)/, "existing terminals adopt the current --term-bg without recreation");
});

test("#workspace outside the terminal card follows the system safe-area canvas", () => {
  const bodies = bodiesFor(/^#workspace$/);
  assert.equal(bodies.length, 1, "expected exactly one #workspace rule");
  assert.equal(
    decl(bodies[0], "background"),
    "var(--safe-area-bg)",
    "the rounded terminal's exterior must follow the system theme, never the black terminal interior",
  );
});

test("#app still consumes the safe-area insets that create the band", () => {
  // Guards the premise of the whole diagnosis: the band is #app's safe-area
  // padding. If that consumption ever goes away, the per-column bleeds become
  // dead weight and these tests should be revisited — not deleted.
  const bodies = bodiesFor(/^#app$/).join("\n");
  const pad = decl(bodies, "padding") || "";
  assert.match(
    pad,
    /env\(safe-area-inset-top/,
    "#app must keep consuming env(safe-area-inset-*) — the bottom inset IS the " +
      "band; the fix paints it rather than removing it (content stays inset, canvas fills)",
  );
});

// --- Approved safe-area visual benchmark -----------------------------------
// A shared content shell clips the rounded UI. The bottom Transport is fixed to
// the physical edge and uses the safe-area canvas; no per-column color slab is
// painted into the old bottom inset.

test("#content-shell owns the rounded, clipped visual frame", () => {
  const bodies = bodiesFor(/^#content-shell$/);
  assert.equal(bodies.length, 1, "expected exactly one shared content shell");
  assert.equal(decl(bodies[0], "overflow"), "hidden");
  assert.equal(decl(bodies[0], "border-radius"), "var(--content-shell-radius)");
  assert.equal(decl(bodies[0], "border"), "1px solid var(--safe-area-line)");
});

for (const selector of ["#sidebar", "#workspace"]) {
  test(`${selector}: no legacy bottom safe-area color bleed`, () => {
    const declarations = bodiesFor(new RegExp(`^${selector}$`))
      .map((body) => decl(body, "box-shadow") || "")
      .filter(Boolean);
    assert.equal(
      declarations.some((value) => value.includes("env(safe-area-inset-bottom")),
      false,
      `${selector} must not paint a separate bottom color slab under the approved Transport design`,
    );
  });
}

test("#workspace keeps overflow:hidden — the bleed relies on box-shadow, not a child", () => {
  // A ::after bleed at top:100% inside #workspace would be clipped (xterm needs
  // overflow:hidden). box-shadow is painted outside the element's own overflow
  // context, which is precisely why it is the mechanism used.
  const bodies = bodiesFor(/^#workspace$/);
  assert.equal(decl(bodies[0], "overflow"), "hidden", "#workspace must keep overflow:hidden for xterm");
});

// --- The page cannot scroll, and that is what keeps this paint covered -------
// Measured failure (issue #116, magic floating bar up): the shell was NOT
// resized (#app computed 702px throughout, no inline height) yet
// visualViewport.offsetTop walked 0 -> 69. With #app exactly as tall as the
// layout viewport, that pan shifted the whole shell up and exposed 69px of root
// canvas BELOW its bottom edge — the reported band, whose height matching the
// bar plus 1-2px is what identified the pan as the cause.
//
// Two answers have been tried and both are recorded as dead ends:
//   * `body { position: fixed }` — did not stop the pan on-device, and with the
//     soft keyboard up it produced a keyboard-height slab of canvas (body stayed
//     a full viewport tall while #app was pinned to the keyboard's top edge).
//   * `2ac6c83`: body-fixed + root clip + a 100vh column bleed. Reverted by
//     5fdbd84; the bleed widening to 100vh is what painted over the panned
//     strip instead of leaving anything visible, so it must not come back.
//
// Contract v2.2 answered structurally, and v3.0 keeps that answer: #app leaves
// the normal flow (position: fixed) so the root boxes carry no content to
// overflow, and html/body declare `overflow: clip` (after `hidden` as the legacy
// floor) so they have no scroll mechanism to move at all. v3.0 adds the half that
// matters for the paint: the shell — and every column inside it — stays FULL
// HEIGHT in every keyboard state, so the strip iOS covers with the keyboard is
// still painted with the app's own ink underneath it, and the only boxes that
// move are the TERMINAL'S OWN: each PTY host's OWN bottom inside
// #terminal-container, plus #terminal-controls and #status-bar — the two
// absolutely positioned overlay children of #workspace that the keyboard would
// otherwise cover. Their containing block stays full height by contract, so their
// OWN `bottom` is the only way the quick-keys entry / panel and the transport
// strip stay reachable. The paint tests below
// therefore pin BOTH halves: the ink that covers the safe-area strip, AND the
// structure that stops this paint from ever being panned out from under the user.

test("body is NOT pinned — the fixed-shell anchor on BODY is a measured dead end", () => {
  const bodies = bodiesFor(/^body$/);
  const position = bodies.map((b) => decl(b, "position") || "").join("");
  assert.equal(
    position,
    "",
    "issue #116: `body { position: fixed }` was tried and reverted — it did not stop " +
      "the visual-viewport pan and it added a keyboard-height canvas slab in the " +
      "keyboard state. The out-of-flow anchor lives on #app, not on body.",
  );
});

test("the out-of-flow anchor is on #app, where the content actually lives", () => {
  // The difference from 2ac6c83 that this whole round rests on: pinning the
  // SHELL takes the content out of the root's flow; pinning the BODY leaves the
  // body box a full viewport tall and painted behind a shorter #app.
  const appRules = bodiesFor(/^#app$/);
  const pinned = appRules.filter((b) => (decl(b, "position") || "") === "fixed");
  assert.equal(pinned.length, 1, "exactly one #app rule must carry position:fixed");
  assert.equal(decl(pinned[0], "top"), "0", "a fixed shell needs its anchor: top 0");
  assert.equal(decl(pinned[0], "left"), "0", "…and left 0");
});

test("the root rule clips — hidden first, clip second, in that order", () => {
  // Same cascade mechanism the paint contract above relies on, and the reason a
  // pan can no longer expose this paint: a clipped box has no scroll mechanism.
  const body = bodiesFor(/^(?:body,\s*html|html,\s*body)$/)[0] || "";
  const declared = (stripComments(body).match(/overflow:\s*[^;]+/g) || []).map((d) => d.replace(/overflow:\s*/, "").trim());
  assert.deepEqual(declared, ["hidden", "clip"], "overflow must be declared hidden then clip — reordered, every engine that knows clip gets a scrollable root back and the band returns");
});

test("no 100vh column bleed anywhere — the strip is covered by paint, not by reach", () => {
  // 2ac6c83 widened the bleeds to 100vh so the canvas could never show through a
  // pan. v3.0 does not need the reach because the root cannot pan and the shell
  // never shrinks; keeping the
  // widening would hide a regression of exactly that reasoning.
  const bleeds = rules.flatMap((r) => stripComments(r.body).match(/box-shadow:[^;]+/g) || []);
  for (const b of bleeds) {
    assert.equal(/100vh|100dvh/.test(b), false, `no box-shadow may reach a viewport height (2ac6c83's reverted bleed): ${b}`);
  }
});

test("no script ever sizes or positions the root boxes or the shell", () => {
  // The paint is CSS's job in this build. A JS write to html/body height is how
  // a "fix" turns into the flicker this issue has been chasing — and under v3.0
  // the same ban covers #app, because the shell is FULL HEIGHT by contract: the
  // only boxes this build moves are the terminal's own three bottoms — each PTY
  // host, #terminal-controls and #status-bar — and the day a script sizes #app
  // again the retired v2.2 pin is back.
  const script = indexSource.slice(indexSource.lastIndexOf("<script>"));
  const code = script.replace(/\/\/[^\n]*/g, "").replace(/\/\*[\s\S]*?\*\//g, "");
  for (const banned of [/documentElement\.style\.(?:height|width|position|overflow)/, /document\.body\.style\.(?:height|width|position|overflow)/, /documentElement\.style\.background/, /scrollTop\s*=[^=]/, /scrollTo\s*\(/, /app\.style\.height/, /app\.style\.position/, /workspace\.style\.(?:height|top|bottom)/, /terminalContainer\.style\.(?:height|top|bottom)/]) {
    assert.equal(banned.test(code), false, `no shipped code may size or position the root boxes, the shell, #workspace or #terminal-container, matched ${banned}`);
  }
  // The geometry writes that ARE allowed, stated so the ban above cannot be
  // satisfied merely by deleting the viewport layer. v3.0 moves the terminal's
  // own furniture and nothing else: each PTY host's bottom, plus the two overlay
  // children of #workspace that would otherwise sit under the keyboard — the
  // quick-keys control column and the transport status strip. Their containing
  // block stays full height by contract, so their OWN bottom is the only way the
  // quick-keys entry and panel stay reachable.
  assert.match(code, /hosts\[i\]\.style\.bottom\s*=/, "each PTY host's own bottom is one of the geometry writes this build makes");
  assert.match(code, /controls\.style\.bottom\s*=/, "#terminal-controls' own bottom is written — quick-keys stays reachable above the keyboard");
  assert.doesNotMatch(code, /statusBar\.style\.bottom\s*=/, "Transport stays fixed at the physical bottom during keyboard avoidance");
  assert.match(code, /CONTROLS_BOTTOM_OFFSET \+ occlusion/, "the column lifts by 20 + occlusion, keeping its authored clearance over the transport strip");
});

test("initShellScrollLock exists and is installed from the init path", () => {
  assert.match(indexSource, /function initShellScrollLock\(\)/, "the gesture guard must exist");
  const initAt = indexSource.indexOf("initTerminalTouchScroll();");
  assert.ok(initAt > 0, "the existing terminal touch lock must still be installed");
  const after = indexSource.slice(initAt, initAt + 400);
  assert.match(
    after,
    /initShellScrollLock\(\);/,
    "initShellScrollLock() must be installed right after the terminal touch lock — the " +
      "terminal-only listener never saw drags that started outside #terminal-container, " +
      "which is exactly how the shell was panned.",
  );
});

test("the shell lock prevents the default unless a native scroller owns the gesture", () => {
  const start = indexSource.indexOf("function initShellScrollLock()");
  assert.ok(start > 0, "initShellScrollLock must exist");
  const body = indexSource.slice(start, indexSource.indexOf("\n        function initTerminalTouchScroll()", start));
  assert.match(body, /touchmove/, "the lock must listen for touchmove");
  assert.match(
    body,
    /\{ passive: false \}/,
    "the lock MUST be { passive: false } — a passive listener cannot preventDefault, " +
      "which is the entire mechanism",
  );
  assert.match(body, /preventDefault\(\)/, "the lock must call preventDefault");
  assert.match(
    body,
    /IFRAME/,
    "web-kind iframes must be exempt — blocking their drags would freeze the " +
      "opencode / reasonix / dsh surfaces",
  );
  assert.match(
    body,
    /auto\|scroll/,
    "native scrollers must be exempt by computed overflow-y, or the sidebar panels, " +
      "monitor table, modal bodies and quick-keys panel stop scrolling",
  );
  assert.match(body, /scrollHeight > el\.clientHeight/, "the exemption requires ACTUAL overflow, not just overflow-y");
});

test("the shell lock also covers wheel — the trackpad is a second door", () => {
  const start = indexSource.indexOf("function initShellScrollLock()");
  const body = indexSource.slice(start, indexSource.indexOf("\n        function initTerminalTouchScroll()", start));
  assert.match(
    body,
    /addEventListener\("wheel"/,
    "issue #116: a Magic Keyboard trackpad two-finger scroll emits `wheel`, not " +
      "`touchmove`. Measured on-device: touch drags were locked but the trackpad still " +
      "panned the whole shell up and revealed the canvas band to the bar's top edge.",
  );
  assert.match(body, /e\.ctrlKey/, "wheel pinch-zoom (ctrlKey) must stay exempt so zooming still works");
});

test("the shell lock exempts the terminal for TOUCH only — xterm keeps its own touch handling", () => {
  const start = indexSource.indexOf("function initShellScrollLock()");
  const body = indexSource.slice(start, indexSource.indexOf("\n        function initTerminalTouchScroll()", start));
  // The touch path must still exempt the terminal: #terminal-container already
  // preventDefaults and maps the drag onto xterm.scrollLines() in
  // initTerminalTouchScroll, so a second scroll there would double-handle it.
  assert.match(
    body,
    /ownsTouchGesture/,
    "the touchmove path uses its own gesture owner, distinct from the wheel path",
  );
  assert.match(
    body,
    /ownsTouchGesture\(e\.target\)\) return/,
    "touchmove bails on the terminal owner so the terminal's own touch scroll stays authoritative",
  );
  assert.match(
    body,
    /if \(el\.id === "terminal-container"\) return true;[\s\S]*if \(el\.classList && el\.classList\.contains\("xterm"\)\) return true;[\s\S]*return false;\s*}\s*document\.addEventListener\("touchmove"/,
    "ownsTouchGesture exempts #terminal-container / .xterm (terminal owns the drag) — but only up to the touchmove listener, not the wheel",
  );
});

test("the shell lock blocks WHEEL over the terminal — it blocks the root default, never double-scrolls", () => {
  const start = indexSource.indexOf("function initShellScrollLock()");
  const body = indexSource.slice(start, indexSource.indexOf("\n        function initTerminalTouchScroll()", start));
  // The wheel path must NOT exempt the terminal — that exemption was the entry door
  // the p7 trackpad walked. It uses nativeScrollOwner (iframe + real scrollers),
  // which deliberately does not list #terminal-container / .xterm.
  assert.match(
    body,
    /addEventListener\("wheel", \(e\) => \{[\s\S]*?if \(e\.ctrlKey\) return;[\s\S]*?if \(nativeScrollOwner\(e\.target\)\) return;[\s\S]*?e\.preventDefault\(\);[\s\S]*?\}, \{ passive: false \}\);/,
    "the wheel listener: ctrlKey exempt, native scrollers exempt, everything else (incl. the terminal) preventDefault",
  );
  // The wheel owner must NOT name the terminal — that is the exemption that failed.
  // Strip BOTH comment forms first: the slice runs up to ownsTouchGesture, whose
  // `//` header prose narrates ".xterm.scrollLines", and prose is not the exemption.
  const nativeOwner = body.slice(body.indexOf("function nativeScrollOwner"), body.indexOf("function ownsTouchGesture"));
  const ownerCode = stripComments(nativeOwner).replace(/\/\/.*/g, "");
  assert.ok(!/terminal-container|\.xterm|xterm/.test(ownerCode), "nativeScrollOwner must NOT exempt the terminal — a wheel over the terminal must block the root default");
  // The lock never scrolls the terminal itself. (Strip comments — the prose
  // narrates "never calls term.scrollLines"; the CODE must not contain it.)
  assert.ok(!/scrollLines/.test(stripComments(body).replace(/\/\/.*/g, "")), "the shell lock must never call term.scrollLines — it owes only a preventDefault of the root default");
});

// A behavioural test, not a grep: run the shipped initShellScrollLock against a
// fake document, dispatch a synthetic wheel whose target is inside the terminal, and
// assert the lock preventDefaults it while scrolling nothing itself. xterm's own
// wheel handler lives closer to the target than this document listener, so the real
// terminal scroll already happened by the time the event bubbles here — this listener
// only owes the page a default-block. (Chrome in a Node stub is not iOS; the real
// device proof is the p8 capture. What this pins is the LISTENER CONTRACT.)
test("behaviour: a wheel over the terminal is defaultPrevented and never re-scrolled by this lock", () => {
  const start = indexSource.indexOf("function initShellScrollLock()");
  const src = indexSource.slice(start, indexSource.indexOf("\n        function initTerminalTouchScroll()", start));

  const listeners = new Map();
  // A DOMTokenList-shaped classList: the shipped code calls .contains(name).
  const cl = (...names) => ({ contains: (n) => names.includes(n) });
  const mkEl = (props) => ({ parentElement: null, ...props });
  const scrollerOverflow = (overflowY, overflowing) => ({ overflowY, scrollHeight: overflowing ? 500 : 100, clientHeight: 100 });

  // The target chain: a leaf .xterm-screen inside a host inside #terminal-container,
  // none of which is a native overflow scroller (xterm v6 virtualises scrolling).
  const terminalLeaf = mkEl({ id: "", classList: cl("xterm-screen") });
  const terminalHost = mkEl({ id: "", classList: cl("xterm") });
  const terminalContainer = mkEl({ id: "terminal-container", classList: cl() });
  terminalLeaf.parentElement = terminalHost;
  terminalHost.parentElement = terminalContainer;
  // A native scroller: overflow-y auto with real overflow.
  const nativeScroller = mkEl({ id: "wt-list", classList: cl(), ...scrollerOverflow("auto", true) });

  const computed = new Map([
    [terminalLeaf, { overflowY: "visible" }],
    [terminalHost, { overflowY: "visible" }],
    [terminalContainer, { overflowY: "visible" }],
    [nativeScroller, { overflowY: "auto" }],
  ]);

  const document = {
    body: { tagName: "BODY" },
    documentElement: { tagName: "HTML" },
    addEventListener(type, handler, options) {
      if (!listeners.has(type)) listeners.set(type, []);
      listeners.get(type).push({ handler, options });
    },
  };
  const window = { getComputedStyle: (el) => computed.get(el) || { overflowY: "visible" } };

  // scrollLines is the double-scroll this lock must never do. If any code path
  // reaches for it, this throws.
  const forbiddenScroll = () => { throw new Error("the shell lock must NOT scroll the terminal itself"); };

  // Evaluate the shipped function against the stand-ins.
  // eslint-disable-next-line no-new-func
  const run = new Function("document", "window", "scrollLines", `${src}; initShellScrollLock();`);
  run(document, window, forbiddenScroll);

  const fire = (type, ev) => { for (const l of listeners.get(type) ?? []) l.handler(ev); };
  const mkEvent = (target, extra = {}) => ({
    target,
    touches: [],
    ctrlKey: false,
    defaultPrevented: false,
    preventDefault() { this.defaultPrevented = true; },
    ...extra,
  });

  // Wheel over the terminal → prevented (root default blocked), no double-scroll.
  const termWheel = mkEvent(terminalLeaf);
  fire("wheel", termWheel);
  assert.equal(termWheel.defaultPrevented, true, "a wheel whose target is inside the terminal must block the ROOT default (the p7 trackpad door)");

  // ctrlKey pinch zoom → NOT prevented.
  const pinch = mkEvent(terminalLeaf, { ctrlKey: true });
  fire("wheel", pinch);
  assert.equal(pinch.defaultPrevented, false, "ctrlKey pinch-zoom stays exempt");

  // Wheel over a native scroller → NOT prevented (it scrolls itself).
  const scrollerWheel = mkEvent(nativeScroller);
  fire("wheel", scrollerWheel);
  assert.equal(scrollerWheel.defaultPrevented, false, "a wheel over a real overflow-y scroller must keep its native scroll");

  // TOUCH over the terminal → NOT prevented by this lock (initTerminalTouchScroll owns it).
  const termTouch = mkEvent(terminalLeaf, { touches: [{ identifier: 1 }] });
  fire("touchmove", termTouch);
  assert.equal(termTouch.defaultPrevented, false, "touchmove over the terminal stays exempt — the terminal's own touch scroll is authoritative here");

  // TOUCH over the shell (non-terminal, non-scroller) → prevented.
  const headerEl = mkEl({ id: "header", classList: cl(), parentElement: document.body });
  const shellTouch = mkEvent(headerEl, { touches: [{ identifier: 1 }] });
  fire("touchmove", shellTouch);
  assert.equal(shellTouch.defaultPrevented, true, "a touch drag that starts on the shell is panning the page — blocked");
});
