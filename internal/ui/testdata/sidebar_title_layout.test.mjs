// Direct source-level checks on the sidebar title row's CSS (issue #116).
//
// WHAT THIS PROVES AND WHAT IT DOES NOT.
// On a real iPad the middle tier caps #sidebar at 200px, a remote session shows
// the "Remote" badge and a coarse pointer makes each action button 44px, so the
// title row had to hold title + badge + 96px of buttons in a 175px content box;
// the "Import" button sat outside the rail. The fix is three flex constraints.
//
// This file asserts those constraints against the shipped stylesheet, i.e. that
// the row may wrap, the text cell may shrink and break, the action group cannot be
// squeezed, and nothing was hidden to make room. NO LAYOUT ENGINE RUNS HERE — a
// passing run is not a browser geometry proof; the real-device check stays with
// the user.
//
// Run: node --test testdata/sidebar_title_layout.test.mjs
import test from "node:test";
import assert from "node:assert/strict";
import { readFileSync } from "node:fs";
import { fileURLToPath } from "node:url";
import { dirname, join } from "node:path";

const here = dirname(fileURLToPath(import.meta.url));
const htmlPath = join(here, "..", "static", "index.html");
const html = readFileSync(htmlPath, "utf8");

function styleText(src) {
  const start = src.indexOf("<style>");
  const end = src.indexOf("</style>", start);
  assert.ok(start >= 0 && end > start, "no <style> block in index.html");
  return src.slice(start + "<style>".length, end);
}

const css = styleText(html);

// Returns the index just past the `}` closing the brace opened at `open`.
function braceEnd(src, open, label) {
  let depth = 0;
  for (let i = open; i < src.length; i++) {
    if (src[i] === "{") depth++;
    else if (src[i] === "}") {
      depth--;
      if (depth === 0) return i + 1;
    }
  }
  throw new Error(`unbalanced braces while slicing ${label}`);
}

// One rule block ("selector { ... }") for an EXACT selector string, or null.
// Anchored to the start of a line so ".remote-badge" cannot match inside
// ".sidebar-title > span > .remote-badge".
function rule(src, selector) {
  const esc = selector.replace(/[.*+?^${}()|[\]\\]/g, "\\$&");
  const re = new RegExp(`^([ \\t]*)${esc}[ \\t]*\\{`, "m");
  const m = src.match(re);
  if (!m) return null;
  const open = m.index + m[0].length - 1;
  return src.slice(open + 1, braceEnd(src, open, selector) - 1);
}

function decl(block, prop) {
  const re = new RegExp(`(?:^|[;\\n])\\s*${prop}\\s*:\\s*([^;]+);`);
  const m = block.match(re);
  return m ? m[1].trim() : null;
}

// The markup of the title row as shipped: everything from the row's opening div
// up to the panel that follows it.
const rowStart = html.indexOf('<div class="sidebar-title">');
assert.notEqual(rowStart, -1, "index.html no longer contains <div class=\"sidebar-title\">");
const rowEnd = html.indexOf('id="sidebar-top-panel"', rowStart);
assert.notEqual(rowEnd, -1, "the sidebar title row is no longer followed by #sidebar-top-panel");
const titleRowMarkup = html.slice(rowStart, rowEnd);

test("#116: the title row can wrap and shrink instead of overflowing", () => {
  const row = rule(css, ".sidebar-title");
  assert.ok(row !== null, ".sidebar-title rule is gone from the shipped stylesheet");
  assert.equal(decl(row, "display"), "flex", "the row is a flex container");
  assert.equal(decl(row, "flex-wrap"), "wrap",
    "wrap is the floor: where the cells cannot share a line the buttons drop below the title instead of leaving the rail");
  assert.equal(decl(row, "min-width"), "0", "the row itself may shrink below its content's min-content");
  assert.ok(decl(row, "column-gap"), "the wrapped lines keep a column gap so the cells do not touch");
  assert.doesNotMatch(row, /overflow\s*:\s*(?:hidden|clip)/,
    "the row must not clip — clipping would cut the wrapped line instead of showing it");
  assert.doesNotMatch(row, /max-height\s*:/, "no max-height on the row, or a wrapped line would be cut");
});

test("#116: the text cell may shrink and break, with a numeric flex basis", () => {
  const cell = rule(css, ".sidebar-title > span");
  assert.ok(cell !== null, "the title text cell (.sidebar-title > span) has no rule");
  assert.equal(decl(cell, "min-width"), "0", "the cell may shrink below its min-content — the fused \"WorktreesRemote\" word");
  assert.equal(decl(cell, "overflow-wrap"), "anywhere", "the break participates in the min-content calculation");
  const flex = decl(cell, "flex");
  assert.ok(flex !== null, "the cell needs an explicit flex shorthand");
  assert.equal(flex.split(/\s+/)[2], "0",
    `flex-basis must be 0, not auto — auto seeds the line with max-content and flex wraps before it shrinks (got ${JSON.stringify(flex)})`);
});

test("#116: the action group is never squeezed, so the 44px targets stay 44px", () => {
  const actions = rule(css, ".sidebar-actions");
  assert.ok(actions !== null, ".sidebar-actions rule is gone");
  assert.equal(decl(actions, "flex"), "0 0 auto", "the action group neither grows nor shrinks — buttons are not the give");
  assert.equal(decl(actions, "flex-wrap"), "nowrap", "the two buttons stay on one line with the title");
  assert.ok(decl(actions, "gap"), "the buttons keep their gap");

  // The coarse-pointer hit targets are untouched by this fix.
  const coarseStart = css.indexOf("@media (pointer: coarse) {");
  assert.notEqual(coarseStart, -1, "no coarse-pointer media block");
  const coarse = css.slice(coarseStart, braceEnd(css, css.indexOf("{", coarseStart), "@media (pointer: coarse)"));
  const coarseBtn = rule(coarse, ".icon-btn");
  assert.ok(coarseBtn !== null, "the coarse-pointer .icon-btn rule is gone");
  assert.equal(decl(coarseBtn, "min-width"), "44px", "coarse hit target stays 44px wide");
  assert.equal(decl(coarseBtn, "min-height"), "44px", "coarse hit target stays 44px tall");
  assert.doesNotMatch(actions, /(?:^|[;\n])\s*(?:width|max-width|flex-shrink)\s*:/,
    "the action rules must not cap or shrink the buttons");
});

test("#116: nothing was hidden to make room — the badge still renders", () => {
  // Markup: the badge is still there, and the two action buttons are still there.
  assert.match(html, /<span id="remote-badge" class="remote-badge(?: hidden)?">Remote<\/span>/,
    "the Remote badge must still be in the title row markup");
  assert.match(titleRowMarkup, /openModal\('modal-import-wt'\)/, "the Import button is still in the row");
  assert.match(titleRowMarkup, /openModal\('modal-create-wt'\)/, "the New button is still in the row");

  // Every rule that names the badge: none of them may hide it. The only display
  // change allowed is the JS-toggled `.hidden` state, which is the pre-existing
  // "not a remote session" mechanism.
  for (const selector of [".remote-badge", ".sidebar-title > span > .remote-badge", "#remote-badge"]) {
    const block = rule(css, selector);
    if (block === null) continue;
    assert.doesNotMatch(block, /display\s*:\s*none/, `${selector} must not hide the badge`);
    assert.doesNotMatch(block, /visibility\s*:\s*hidden/, `${selector} must not hide the badge`);
    assert.doesNotMatch(block, /(?:^|[;\n])\s*(?:width|max-width)\s*:/, `${selector} must not clip the badge`);
    assert.doesNotMatch(block, /overflow\s*:\s*(?:hidden|clip)/, `${selector} must not clip the badge`);
    assert.doesNotMatch(block, /font-size\s*:\s*0/, `${selector} must not collapse the badge text`);
  }
  assert.equal(rule(css, ".remote-badge.hidden") !== null, true,
    "the pre-existing JS-toggled hidden state stays as it was");
});
