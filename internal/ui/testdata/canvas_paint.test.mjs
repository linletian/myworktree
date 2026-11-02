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
    "var(--bg-color)",
    "issue #116: the root canvas is the app's chrome tone. Design truth is " +
      "ui/sidebar-states.pen, where the safe-area strip is $bg-root (#F4F6FA) and " +
      "sits beside $bg-sidebar (#F7F7F9) — never $term-bg (#1B1A23). Painting the " +
      "terminal ink here produced a dark slab across the bottom of every column.",
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

test("--term-bg resolves to one value in BOTH themes (no light/dark split)", () => {
  // The claim pinned in the fix comment and in DATA-COLLECTION.md: --term-bg is
  // defined once and is NOT redefined under prefers-color-scheme: dark, so
  // painting it matches #workspace byte-identically in either theme.
  const defined = rules
    .flatMap((r) => stripComments(r.body).match(/--term-bg\s*:[^;]+;?/g) || [])
    .map((d) => d.replace(/--term-bg\s*:\s*/, "").replace(/;$/, "").trim());
  assert.equal(defined.length, 1, `--term-bg must be defined exactly once, got ${JSON.stringify(defined)}`);
  assert.equal(defined[0], "#0b1020", `--term-bg must stay #0b1020 (measured rgb(11,16,32) on #workspace), got ${defined[0]}`);
});

test("#workspace keeps painting the terminal background the band must join", () => {
  const bodies = bodiesFor(/^#workspace$/);
  assert.equal(bodies.length, 1, "expected exactly one #workspace rule");
  assert.equal(
    decl(bodies[0], "background"),
    "var(--term-bg)",
    "#workspace is the layer the safe-area band must stay continuous with; if its " +
      "background token changes, the root-canvas background must change with it",
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

// --- 「背景全出血，内容内缩」 (design: ui/sidebar-states.pen) ---------------
// The design does not decorate the safe-area strip; it makes the strip vanish by
// having each column carry its OWN background down to the physical edge while the
// content stays inset. These cases pin that per-column contract.

for (const [selector, ink] of [
  ["#sidebar", "var(--bg-color)"],
  ["#workspace", "var(--term-bg)"],
]) {
  // #sidebar has several variant rules (base, .collapsed width:0, the narrow-tier
  // drawer); the bleed belongs to the BASE rule, so pick the one that carries it
  // rather than assuming a rule count.
  const bleedRule = () =>
    bodiesFor(new RegExp(`^${selector}$`)).find((b) => (decl(b, "box-shadow") || "").includes("env(safe-area-inset-bottom"));

  test(`${selector}: the column bleeds its own background past the safe area`, () => {
    assert.ok(
      bodiesFor(new RegExp(`^${selector}$`)).length >= 1,
      `expected at least one ${selector} rule`,
    );
    const body = bleedRule();
    assert.ok(body, `${selector} must carry a safe-area bleed box-shadow`);
    assert.equal(
      decl(body, "box-shadow"),
      `0 env(safe-area-inset-bottom, 0px) 0 0 ${ink}`,
      `${selector} must carry its own ink ${ink} across env(safe-area-inset-bottom). ` +
        "Design 「drawer/main/workspace 背景全出血」: the strip reads as the column, " +
        "not as a separate painted band. Zero blur and zero spread are load-bearing — " +
        "they make the shadow a solid colour extension rather than a glow.",
    );
  });

  test(`${selector}: the bleed carries no magic number`, () => {
    const shadow = decl(bleedRule(), "box-shadow") || "";
    // The offset must BE the env(): 20px is Apple's iPad value today and changes
    // per device/orientation, so a literal would be wrong elsewhere. env()'s own
    // 0px fallback is the declared default, not a magic number, so it is removed
    // before the scan.
    const withoutEnvFallbacks = shadow.replace(/env\([^)]*\)/g, "env()");
    assert.ok(
      !/\b\d+(\.\d+)?px\b/.test(withoutEnvFallbacks),
      `${selector} bleed must derive its offset from env(), not a px constant (got "${shadow}")`,
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

// --- Gmail-style app shell: the page must never scroll or pan ----------------
// Measured failure (issue #116, magic floating bar up): the shell was NOT
// resized (#app computed 702px throughout, no inline height) yet
// visualViewport.offsetTop walked 0 -> 69. With #app exactly as tall as the
// layout viewport, that pan shifts the whole shell up and exposes 69px of root
// canvas BELOW its bottom edge — the reported band, whose height matching the
// bar plus 1-2px is what identified the pan as the cause.
//
// A `body { position: fixed }` shell anchor was tried and REVERTED: it did not
// stop the pan on-device, and with the soft keyboard up it produced a
// keyboard-height slab of canvas (body stayed a full viewport tall while #app
// was pinned to the keyboard's top edge). The lock is a gesture guard instead.

test("body is NOT pinned — the fixed-shell anchor is a measured dead end", () => {
  const bodies = bodiesFor(/^body$/);
  const position = bodies.map((b) => decl(b, "position") || "").join("");
  assert.equal(
    position,
    "",
    "issue #116: `body { position: fixed }` was tried and reverted — it did not stop " +
      "the visual-viewport pan and it added a keyboard-height canvas slab in the " +
      "keyboard state. Do not re-add it without on-device evidence.",
  );
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

test("the shell lock exempts the terminal so xterm keeps its own gesture handling", () => {
  const start = indexSource.indexOf("function initShellScrollLock()");
  const body = indexSource.slice(start, indexSource.indexOf("\n        function initTerminalTouchScroll()", start));
  assert.match(
    body,
    /terminal-container/,
    "the terminal must be exempt: #terminal-container already preventDefaults and maps the " +
      "drag onto xterm.scrollLines(), and xterm handles wheel itself",
  );
});
