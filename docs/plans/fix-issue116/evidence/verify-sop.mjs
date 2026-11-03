#!/usr/bin/env node
// Checks a capture against docs/plans/fix-issue116/A0-SOP.md (contract v3.2,
// "FULL-HEIGHT shell + PTY-LOCAL avoidance SCOPED TO SOFT-KEYBOARD SESSIONS — a
// shortfall outside a live session simply overlaps — non-scrollable root") and
// prints a PASS/FAIL table per invariant. v3.2 replaces the v3.1 0.25 stateless
// ratio floor with the keyboard-session state machine the applier now runs; the
// fixed #status-bar and the wheel isolation carry over, and the shell/clip/overlay
// invariants below carry their original v3.0 numbers unchanged.
//
// The SOP is written for a human on the iPad; this is the machine half of the
// same contract. Two halves, two axes:
//
//   THE SHELL NEVER MOVES. v2.2 pinned #app to vv.height, and that pairing is
//   retired here on the strength of the user's own control run of real Gmail on
//   the same iPad — the reference keeps a FULL-HEIGHT layout and lets the
//   keyboard float over it, so what shows through the keyboard is the page that
//   was already there. The verdict number is therefore
//
//     layoutGap = documentElement.clientHeight - app.bottom
//       |layoutGap| > 1  ->  the shell was resized  (the retired pin is back)
//
//   and the retired reading is EXPLICITLY NOT APPLIED any more:
//
//     vv.height - app.bottom < 0  ->  EXPECTED, not a failure
//
//   A full-height shell deliberately extends under the keyboard. The old
//   "crossing" bucket is deleted, and a fixture proves the old failure shape now
//   passes for the right reason.
//
//   THE TERMINAL AVOIDS LOCALLY, INSIDE A KEYBOARD SESSION. The judge replays
//   the capture's frames IN ORDER through the same state machine the applier
//   runs: at scale = 1, with editable focus (ui.activeElementTag TEXTAREA /
//   INPUT — contenteditable focus is not visible to the probe, see NOTE below)
//   and raw = max(0, clientHeight - vv.height):
//
//     ENTER   at raw >= 0.50 * clientHeight — an explicit keyboard starting up
//             (measured 274 pays raw 428 on the 702 layout, host bottom 254);
//     HOLD    at raw every pass while that focus and raw > 1 survive — 340 /
//             206.1 / 147 retract frames of ONE keyboard move exactly that far;
//     LEAVE   at raw <= 1, zoom, or focus loss; after leaving, a shortfall
//             under the 0.50 line can never re-enter — the thin floating strip
//             (633.5 → 68.5) and any shallow state simply overlap a terminal
//             that stays flush.
//
//   The retired v3.1 0.25 floor is NOT replayed: 206.1px intermediates sat
//   ABOVE its 175.5px line and re-lifted the terminal and Transport mid-session
//   — that bounce was the device feedback, and geometry without session state
//   cannot tell those frames from a fresh shallow overlay. NOTE: the probe
//   reports only tagName, so a contenteditable focus reads "not editable" to
//   this judge; the product app has no contenteditable hosts (xterm's helper is
//   a TEXTAREA), and if that ever changes the judge must learn the field. The
//   hosts are the only boxes this build moves, so they are what the verdict
//   reads:
//
//     app.inlineHeight !== ""                 -> FAIL "pinned"   (the retired pin)
//     occlusion != session shortfall         -> FAIL "occlusion" (wrong arithmetic)
//     a host inline bottom != occlusion      -> FAIL "host"      (a host missed)
//     hosts disagree with each other         -> FAIL "residue"   (the next band)
//     a host bottom > vv.height + 1 (avoiding frames only) -> FAIL "underKeyboard"
//
//   the under-* checks bind only on AVOIDING frames (session live, owed > 1px).
//   Outside a session the terminal keeps its full height and is overlapped on
//   purpose, so a box dipping under the visible bottom there is the required
//   behaviour, not a fault.
//
//   root scroll: document.scrollTop / scrollingElementScrollTop are judged AND the
//   root scroll RANGE is judged (rootScrollHeight / scrollingElementScrollHeight
//   must not exceed clientHeight). A non-zero value is STILL A FAIL and nothing in
//   this build repairs it — v2.1 parked the origin and that was withdrawn (a page
//   that must be dragged back is a page that can scroll). The reference app grows
//   a hidden, swipe-revealable strip under its shell while the keyboard is up;
//   copying that is FORBIDDEN by this contract, so a root scroll range appearing
//   under the keyboard is a regression here, not a feature.
//
//   structure: htmlOverflow / bodyOverflow must compute to `clip`,
//   app.computedPosition to `fixed`, and rootScrollHeight must not exceed
//   clientHeight. Those are the reasons the page cannot scroll; a capture that
//   shows scrollY = 0 without them has only proven that nothing moved YET.
//
//   vv.offsetTop / document.pageTop stay INFORMATIONAL: they say how far iOS
//   panned, and never enter a verdict (the p4 rule that added pageTop back onto
//   the app rect is retired).
//
//   rows: on a STABLE frame (manual / settle / periodic / *hint, i.e. not a
//   resize or scroll transient) xtermRows.bottom must sit inside vv.height.
//
// A run can only PASS on evidence: a missing probe, a missing probe.visualBottom,
// a missing key field, a stamp from another build, a duplicated seq or a hole in
// the sequence makes the whole run INCONCLUSIVE instead of quietly green. A frame
// whose geometry is complete but that never hit-tested its bottom edge says
// NOTHING about who owns the band's pixels, so it never counts as an ink PASS
// either — the geometry is still judged, but the ink invariant stays unproven.
// Screenshots stay required for the final judgement (A0 SOP invariant 9): this
// script certifies geometry and hit ownership, never the look of a strip.
//
// TOLERANCES ARE APPLIED TO RAW CSS PX, never to printed values: layoutGap is
// compared as the unrounded clientHeight - app.bottom, so a 1.04px resize FAILS
// (rounding it to 1.0 first would wave it through). Only the printed columns are
// rounded.
//
// Usage:
//   MW_A0_TOKEN=… node verify-sop.mjs --session <id> --base http://host:port
//   MW_A0_TOKEN=… node verify-sop.mjs --file evidence/raw/<id>.jsonl
//   node verify-sop.mjs --self-test          # fixtures, no network
//
// Exit code: 0 PASS, 1 FAIL, 2 INCONCLUSIVE.

import { readFileSync } from "node:fs";

// The build under test. A capture stamped with anything else cannot certify it.
const EXPECTED_PROBE_VERSION = "p9-full-shell-session-avoidance-v1";
// Reasons whose geometry has settled. `resize` and `scroll` are transients: the
// host bottom was just written and the flex column / xterm refit has not landed
// yet, so they are reported but never asserted.
const STABLE_REASONS = new Set(["manual", "settle", "periodic", "keyboard-hint", "magic-hint"]);
const TOLERANCE_PX = 1;
const SCROLL_TOLERANCE_PX = 0.5;
// #terminal-controls' authored `bottom`, out of the shipped CSS. The column sits
// 20px above the transport strip and the avoidance adds the occlusion ON TOP of
// that, so the number a frame owes is 20 + occlusion — while the strip itself,
// authored at bottom 0, owes the occlusion alone. Spelled as a constant here
// because the two targets being DIFFERENT is the thing under test: an
// implementation that wrote the host value to all three boxes would satisfy every
// other check in this file and still bury the quick-keys panel.
const CONTROLS_AUTHORED_BOTTOM = 20;

// CONTRACT v3.2 (issue #116): the KEYBOARD-SESSION entry line, mirrored from the
// applier. 0.50 of clientHeight is compared ONLY while no session is open — a
// shortfall at or over it, with editable focus, is an explicit soft keyboard
// starting up and ENTERS avoidance. Inside a live session there is NO line: the
// effective occlusion tracks raw every pass (the 340 / 206.1 / 147 retract frames
// of one keyboard move exactly that far), and at raw <= 1, zoom, or focus loss the
// session ENDS — after which any shortfall under 0.50 can never re-open it. The
// retired v3.1 0.25 stateless floor is NOT replayed here: 206.1px intermediates sat
// ABOVE its 175.5px line and re-lifted the terminal and Transport mid-session, the
// exact bounce the device feedback reported; a ratio without session state cannot
// tell those frames from a fresh shallow overlay, which is why the shape changed.
// The ratio is a RATIO, not a pixel count, so it holds at every viewport height.
const KEYBOARD_ENTRY_RATIO = 0.50;

function num(value) {
  return typeof value === "number" && Number.isFinite(value) ? value : null;
}

function round1(value) {
  return Math.round(value * 10) / 10;
}

function isZoomed(snapshot) {
  const scale = num(snapshot.vv ? snapshot.vv.scale : null);
  return scale !== null && Math.abs(scale - 1) > 0.01;
}

// What the build owes the PTY hosts: the applier's state machine REPLAYED in
// capture order, independently of the probe's own `occlusion` mirror, so the two
// can disagree and the disagreement is the finding. State is ONE boolean — no
// timers, no history — and the judge advances it on every frame, zoom included.
// Returns { owed, gap }: owed null with a gap name when the frame cannot place the
// session (no geometry, or no ui.activeElementTag to prove the focus half). The
// probe reports tagName only, so contenteditable focus reads non-editable to the
// judge — the product has no contenteditable hosts (xterm's helper is a TEXTAREA),
// and if that ever changes, the probe and this judge must learn the field together.
function makeKeyboardSessionJudge() {
  let active = false;
  return {
    owedOcclusion: function (snapshot) {
      if (isZoomed(snapshot)) { active = false; return { owed: 0, gap: null }; }
      const clientHeight = num(snapshot.clientHeight);
      const vvHeight = num(snapshot.vv ? snapshot.vv.height : null);
      if (clientHeight === null || vvHeight === null) return { owed: null, gap: "clientHeight or vv.height" };
      const raw = Math.max(0, clientHeight - vvHeight);
      if (raw <= 1) { active = false; return { owed: 0, gap: null }; }
      const tag = snapshot.ui && typeof snapshot.ui.activeElementTag === "string" ? snapshot.ui.activeElementTag : "";
      if (tag === "") return { owed: null, gap: "ui.activeElementTag" };
      const editable = tag === "TEXTAREA" || tag === "INPUT";
      if (!editable) { active = false; return { owed: 0, gap: null }; }
      if (!active) active = raw >= clientHeight * KEYBOARD_ENTRY_RATIO;
      return { owed: active ? raw : 0, gap: null };
    },
  };
}

function parsePx(value) {
  if (typeof value !== "string") return null;
  const trimmed = value.trim();
  if (!trimmed.endsWith("px")) return null;
  const n = parseFloat(trimmed.slice(0, -2));
  return Number.isFinite(n) ? n : null;
}

// One frame -> { fail[], info }. No I/O, so --self-test can drive it with
// hand-built snapshots and prove the READING, not just the plumbing. It is NOT
// stateless: v3.2's occlusion is a KEYBOARD SESSION, so the session judge advances
// on EVERY frame — zoom included, because zoom ENDS a session in flight — and a
// caller that skipped a frame could not tell a mid-session raw from a never-opened
// shallow strip. A fresh judge runs one capture; the frame loop must feed it every
// event in seq order, exactly like the live applier is fed every visualViewport
// pass, focus edge and 500ms tick.
function judgeFrame(event, session) {
  const s = event.snapshot || {};
  const out = { fail: [], info: "" };
  // The session advances BEFORE any early return: the zoom early-return below must
  // still leave the state machine closed, so a shallow post-pinch frame cannot read
  // as mid-session raw.
  const owed = session.owedOcclusion(s);
  out.sessionActive = owed.owed > TOLERANCE_PX;
  if (isZoomed(s)) {
    out.zoom = true;
    return out;
  }
  const vvHeight = num(s.vv ? s.vv.height : null);
  const appBottom = num(s.app ? s.app.bottom : null);
  const clientHeight = num(s.clientHeight);
  const scrollTop = num(s.document ? s.document.scrollTop : null);
  const scrollingTop = num(s.document ? s.document.scrollingElementScrollTop : null);
  const missing = [];
  if (vvHeight === null) missing.push("vv.height");
  if (appBottom === null) missing.push("app.bottom");
  if (clientHeight === null) missing.push("clientHeight");
  if (scrollTop === null) missing.push("document.scrollTop");
  if (scrollingTop === null) missing.push("document.scrollingElementScrollTop");
  if (missing.length) {
    out.missing = missing;
    return out;
  }
  // COMPARISON IS ON RAW CSS PX; round1() only ever formats a printed value.
  // round1(702 - 700.96) == 1.0 would read "quiet" while the shell really was
  // resized by 1.04px.
  const layoutGapRaw = clientHeight - appBottom;
  out.layoutGapRaw = layoutGapRaw;
  out.layoutGap = round1(layoutGapRaw);
  // THE SHELL IS FULL HEIGHT. A shell that stopped above the visible bottom is
  // the retired v2.2 pin returning, and a shell shorter than the layout height in
  // ANY state is the same bug wearing a different hat.
  if (Math.abs(layoutGapRaw) > TOLERANCE_PX) out.fail.push("shellShort");
  // The retired pairing, recorded so a reader cannot mistake its absence for an
  // oversight: vv.height - app.bottom going negative is what v3.0 WANTS.
  out.underBy = round1(vvHeight - appBottom);
  // INLINE HEIGHT IS NEVER WRITTEN. Any non-empty value is the whole-page pin.
  out.inline = typeof s.app.inlineHeight === "string" ? s.app.inlineHeight : null;
  if (out.inline === null) {
    missing.push("app.inlineHeight");
    out.missing = missing;
    return out;
  }
  if (out.inline !== "") out.fail.push("pinned");

  // PTY-LOCAL AVOIDANCE. The session owed-value was computed above from the
  // replayed state machine — geometry plus the focus evidence — not trusted from
  // the probe, and it is held against every host, visible and hidden alike.
  const reported = num(s.occlusion);
  if (owed.owed === null) {
    missing.push(owed.gap || "clientHeight or vv.height");
    out.missing = missing;
    return out;
  }
  out.occlusion = owed.owed;
  const owedPx = owed.owed;
  // Are we in an AVOIDING frame at all? Only then may we demand that no box dips
  // below the visible bottom. OUTSIDE a live session (owed 0 — no editable focus,
  // keyboard closed, or a shallow shortfall that never opened a session) the
  // terminal legitimately stays full height and the strip simply overlaps it —
  // "OVERLAPS a page that never moved" is the required behaviour there, so an
  // under-bottom is not a failure. Gating every *Under check on this keeps the
  // session machine honest instead of failing the very case it certifies.
  const avoiding = owed.owed > TOLERANCE_PX;
  out.avoiding = avoiding;
  if (reported === null) {
    missing.push("occlusion");
    out.missing = missing;
    return out;
  }
  if (Math.abs(reported - owedPx) > TOLERANCE_PX) out.fail.push("occlusion");
  if (!Array.isArray(s.ptyHosts)) {
    // ptyHosts is p7's own field. A frame without it comes from an older
    // instrument and cannot speak about the local avoidance at all.
    missing.push("ptyHosts");
    out.missing = missing;
    return out;
  }
  out.hosts = s.ptyHosts.length;
  if (s.ptyHosts.length) {
    out.hostsObserved = true;
    const bottoms = new Set();
    for (const host of s.ptyHosts) {
      const inline = parsePx(host && host.inlineBottom);
      const bottom = num(host ? host.bottom : null);
      if (inline === null || bottom === null) {
        missing.push("ptyHosts[].inlineBottom/bottom");
        out.missing = missing;
        return out;
      }
      bottoms.add(host.inlineBottom || "");
      if (Math.abs(inline - owedPx) > TOLERANCE_PX) out.fail.push("host");
      if (avoiding && bottom > vvHeight + TOLERANCE_PX) out.fail.push("underKeyboard");
      // The host DEEPEST into the page is the one that decides the verdict, so
      // track the largest bottom rather than the first one seen.
      if (out.worstHostBottomRaw === undefined || bottom > out.worstHostBottomRaw) out.worstHostBottomRaw = bottom;
    }
    out.hostBottom = round1(out.worstHostBottomRaw);
    // NO RESIDUE. Every host must carry the same value — the hidden ones most of
    // all, because a stale bottom there is exactly the band the next switch shows.
    if (bottoms.size > 1) out.fail.push("residue");
    out.distinctBottoms = [...bottoms];
  }

  // THE OVERLAY HALF (contract v3.0, the part this round settles). #workspace is
  // NEVER shortened by the keyboard, so the quick-keys column and the transport
  // strip sit UNDER it unless the same pass lifts them too — and "the input and
  // the quick-keys stay reachable" is the original requirement, not an
  // improvement left for someone to approve. Their authored offsets differ, which
  // is why the two numbers must differ:
  //     #terminal-controls bottom = 20 + occlusion   (authored bottom: 20px)
  //     #status-bar         bottom =     occlusion    (authored bottom: 0)
  // Copying the host target onto the column would bury the quick-keys 20px into
  // the transport strip's band, so equality with the host value is a FAILURE here.
  // Judged on STABLE frames only, for the reason rows are: both boxes sit under
  // a CSS transition, and a `resize` frame catches the lift mid-flight.
  if (STABLE_REASONS.has(s.reason)) {
    if (!s.controls || !s.statusBar) {
      // Absent is not zero. A frame from an instrument that never looked at the
      // overlay column cannot certify that quick-keys was reachable, and inventing
      // a 20px here would turn missing evidence into a false PASS.
      missing.push("controls/statusBar");
      out.missing = missing;
      return out;
    }
    const controlsInline = parsePx(s.controls.inlineBottom);
    const statusInline = parsePx(s.statusBar.inlineBottom);
    if (controlsInline === null || statusInline === null) {
      missing.push("controls.inlineBottom/statusBar.inlineBottom");
      out.missing = missing;
      return out;
    }
    out.controlsInline = round1(controlsInline);
    out.statusInline = round1(statusInline);
    if (s.controls.visible) {
      out.overlayObserved = true;
      if (Math.abs(controlsInline - (CONTROLS_AUTHORED_BOTTOM + owedPx)) > TOLERANCE_PX) out.fail.push("controls");
      const controlsBottom = num(s.controls.bottom);
      if (avoiding && controlsBottom !== null && controlsBottom > vvHeight + TOLERANCE_PX) out.fail.push("controlsUnder");
    }
    if (s.statusBar.visible) {
      out.overlayObserved = true;
      if (Math.abs(statusInline - owedPx) > TOLERANCE_PX) out.fail.push("statusBar");
      const statusBottom = num(s.statusBar.bottom);
      if (avoiding && statusBottom !== null && statusBottom > vvHeight + TOLERANCE_PX) out.fail.push("statusBarUnder");
    }
    // The usable height of the key set. #quick-keys-panel sits at `top: 0`
    // inside the column with `max-height: 100%`, so it can never be taller than
    // the column it is in: a panel that outgrows its shortened container means the
    // column did NOT shorten and the keys run under the keyboard. `maxHeight` is
    // only compared when the engine resolved it to an absolute length — a
    // percentage is a promise, not a measurement.
    if (s.quickKeys && s.quickKeys.open && s.controls.visible) {
      const panelHeight = num(s.quickKeys.clientHeight);
      const columnHeight = num(s.controls.height);
      const maxPx = parsePx(s.quickKeys.maxHeight);
      if (panelHeight !== null && columnHeight !== null) {
        out.quickKeysObserved = true;
        out.quickKeysHeight = round1(panelHeight);
        out.quickKeysScrolls = num(s.quickKeys.scrollHeight) !== null && num(s.quickKeys.scrollHeight) > panelHeight + SCROLL_TOLERANCE_PX;
        if (panelHeight > columnHeight + TOLERANCE_PX) out.fail.push("quickKeys");
        if (maxPx !== null && Math.abs(maxPx - columnHeight) > TOLERANCE_PX) out.fail.push("quickKeys");
      }
    }
  }

  // THE STRUCTURAL HALF. `overflow: clip` on both root boxes, a fixed shell and a
  // root scroll range of zero are WHY the surface does not move. A frame that
  // merely reports scrollY = 0 has not proven any of it, so a capture from an
  // instrument that predates these fields is never allowed to certify — and a
  // frame that reports `hidden` (still a scroll mechanism, just one iOS ignores)
  // FAILS instead of being excused as "close enough".
  const htmlOverflow = (s.document && typeof s.document.htmlOverflow === "string") ? s.document.htmlOverflow : null;
  const bodyOverflow = (s.document && typeof s.document.bodyOverflow === "string") ? s.document.bodyOverflow : null;
  const appPosition = (s.app && typeof s.app.computedPosition === "string") ? s.app.computedPosition : null;
  const rootScrollHeight = num(s.document ? s.document.rootScrollHeight : null);
  const scrollingHeight = num(s.document ? s.document.scrollingElementScrollHeight : null);
  if (htmlOverflow === null || bodyOverflow === null || appPosition === null || rootScrollHeight === null) {
    missing.push("htmlOverflow/bodyOverflow/computedPosition/rootScrollHeight");
    out.missing = missing;
    return out;
  }
  out.structure = `${htmlOverflow}/${bodyOverflow}/${appPosition} range=${round1(rootScrollHeight)}/${round1(clientHeight)}`;
  if (htmlOverflow !== "clip" || bodyOverflow !== "clip") out.fail.push("overflow");
  if (appPosition !== "fixed") out.fail.push("position");
  if (rootScrollHeight > clientHeight + TOLERANCE_PX) out.fail.push("range");
  const rootScroll = Math.max(Math.abs(scrollTop), Math.abs(scrollingTop || 0));
  out.rootScrollRaw = rootScroll;
  out.rootScroll = round1(rootScroll);
  // A non-zero root scroll is a FAIL and nothing here recovers it: the build
  // under test has no origin parking left, so a leak is a leak.
  if (rootScroll > SCROLL_TOLERANCE_PX) out.fail.push("scroll");
  if (scrollingHeight !== null && scrollingHeight > clientHeight + SCROLL_TOLERANCE_PX) out.fail.push("range");
  // Ink needs its own evidence. A frame that never hit-tested the bottom edge
  // cannot say who owns those pixels, so it is NEVER an ink PASS: the geometry
  // above is still judged, but the run stays INCONCLUSIVE until a probe lands.
  // When there IS a hit, the old rule stands unchanged — BODY / HTML means the
  // root canvas is exposed (FAIL); anything else is auxiliary and not a proof.
  const hasProbe = s.probe !== null && typeof s.probe === "object";
  const hit = hasProbe ? s.probe.visualBottom : null;
  if (!hasProbe) {
    out.noEvidence = "probe";
  } else if (hit === null || hit === undefined) {
    out.noEvidence = "probe.visualBottom";
  } else if (/^(BODY|HTML)$/i.test(hit.tag || "")) {
    out.fail.push("ink");
    out.ink = `${hit.tag}${hit.id ? "#" + hit.id : ""} ${hit.background || ""}`.trim();
  }
  const rowsBottom = num(s.xtermRows ? s.xtermRows.bottom : null);
  out.hasRows = rowsBottom !== null;
  // xterm's rendered rows must not sit under the keyboard EITHER — but only in an
  // AVOIDING frame (live session). OUTSIDE a session the rows keep full height and
  // the strip overlaps them by design, exactly like the host under-check above.
  if (avoiding && STABLE_REASONS.has(s.reason) && rowsBottom !== null && rowsBottom > vvHeight + TOLERANCE_PX) {
    out.fail.push("rows");
  }
  out.stable = STABLE_REASONS.has(s.reason);
  // Informational: how far iOS panned. Reported, never judged.
  const pan = num(s.vv ? s.vv.offsetTop : null) || num(s.document ? s.document.pageTop : null) || 0;
  if (Math.abs(pan) > 0.5) out.panned = round1(pan);
  return out;
}

const FAILURE_KINDS = ["shellShort", "pinned", "occlusion", "host", "residue", "underKeyboard", "controls", "controlsUnder", "statusBar", "statusBarUnder", "quickKeys", "scroll", "range", "overflow", "position", "ink", "rows"];

function evaluateFrames(events) {
  const r = {
    versions: [], checked: 0, zoomExempt: 0, panned: 0, stableFrames: 0, rowsFrames: 0, hostFrames: 0,
    overlayFrames: 0, quickKeysFrames: 0, openColumnHeight: undefined,
    failures: Object.fromEntries(FAILURE_KINDS.map((k) => [k, []])),
    inkUnproven: [], inconclusive: [], manualRows: [], maxLayoutGap: 0, minLayoutGap: 0,
  };
  r.versions = [...new Set(events.map((e) => (e.snapshot && e.snapshot.probeVersion) || "(unstamped)"))];
  if (r.versions.some((v) => v === "(unstamped)")) {
    r.inconclusive.push("frames carry no probeVersion — they predate the stamped instrument");
  }
  if (r.versions.some((v) => v !== EXPECTED_PROBE_VERSION)) {
    r.inconclusive.push(`probeVersion ${r.versions.join(", ")} != ${EXPECTED_PROBE_VERSION} — this capture is not from the build under test`);
  }
  const seqs = events.map((e) => e.seq);
  const seen = new Set();
  const dups = [];
  for (const q of seqs) {
    if (seen.has(q)) dups.push(q);
    seen.add(q);
  }
  if (dups.length) r.inconclusive.push(`duplicated seq: ${[...new Set(dups)].slice(0, 8).join(", ")}`);
  const sorted = [...seen].sort((a, b) => a - b);
  const holes = [];
  for (let i = 1; i < sorted.length; i++) {
    for (let q = sorted[i - 1] + 1; q < sorted[i]; q++) holes.push(q);
  }
  if (holes.length) {
    r.inconclusive.push(`seq holes: ${holes.length} frame(s) never delivered (first: ${holes[0]}) — the capture is not complete`);
  }

  // ONE session judge for the WHOLE run: the frames are replayed in seq order and
  // the state machine carries across them, exactly like the applier's single
  // terminalAvoidanceActive carries across the device's passes.
  const keyboardSession = makeKeyboardSessionJudge();
  for (const e of events) {
    const s = e.snapshot || {};
    const j = judgeFrame(e, keyboardSession);
    if (j.zoom) { r.zoomExempt++; continue; }
    if (j.missing) {
      r.inconclusive.push(`seq ${e.seq} (${s.reason}) missing ${j.missing.join(", ")}`);
      continue;
    }
    r.checked++;
    if (j.layoutGapRaw > r.maxLayoutGap) r.maxLayoutGap = j.layoutGapRaw;
    if (j.layoutGapRaw < r.minLayoutGap) r.minLayoutGap = j.layoutGapRaw;
    if (j.panned) r.panned++;
    if (j.stable) r.stableFrames++;
    if (j.stable && j.hasRows) r.rowsFrames++;
    if (j.hostsObserved) r.hostFrames++;
    if (j.overlayObserved) r.overlayFrames++;
    if (j.quickKeysObserved) r.quickKeysFrames++;
    // THE COLUMN SHORTENS WITH THE KEYBOARD. #quick-keys-panel resolves
    // `max-height: 100%` against this column's height, so the column's own height
    // IS the usable height of the key set, and what it lost is exactly the
    // occlusion. Two frames say it together: the keyboard-up column must be
    // shorter than the keyboard-down column by that much. Anything else means the
    // lift landed somewhere other than the column — on the shell (the retired pin)
    // or nowhere at all (keys under the keyboard).
    if (j.stable && j.overlayObserved) {
      const columnHeight = num(s.controls ? s.controls.height : null);
      if (columnHeight !== null) {
        if (j.occlusion === 0) r.openColumnHeight = columnHeight;
        else if (r.openColumnHeight !== undefined) {
          const lost = r.openColumnHeight - columnHeight;
          if (Math.abs(lost - j.occlusion) > TOLERANCE_PX) {
            r.failures.quickKeys.push({
              seq: e.seq, reason: s.reason, occlusion: j.occlusion,
              columnHeight: round1(columnHeight), openHeight: round1(r.openColumnHeight),
              lost: round1(lost), expected: round1(j.occlusion),
            });
          }
        }
      }
    }
    if (j.noEvidence) {
      r.inkUnproven.push({ seq: e.seq, reason: s.reason, field: j.noEvidence });
    }
    for (const kind of j.fail) {
      r.failures[kind].push({
        seq: e.seq, reason: s.reason,
        layoutGap: j.layoutGap, underBy: j.underBy, occlusion: j.occlusion,
        hostBottom: j.hostBottom === undefined ? "-" : j.hostBottom,
        rootScroll: j.rootScroll, ink: j.ink || "",
        vv: round1(num(s.vv.height)), bottom: round1(num(s.app.bottom)),
        client: round1(num(s.clientHeight)), inline: j.inline || "-",
        hosts: j.distinctBottoms ? j.distinctBottoms.join("|") : "-",
        controls: j.controlsInline === undefined ? "-" : j.controlsInline,
        status: j.statusInline === undefined ? "-" : j.statusInline,
        panel: j.quickKeysHeight === undefined ? "-" : j.quickKeysHeight,
      });
    }
    if (s.reason === "manual") {
      r.manualRows.push({
        seq: e.seq, vv: round1(num(s.vv.height)),
        client: round1(num(s.clientHeight)), inline: j.inline || "-",
        layoutGap: j.layoutGap, occlusion: j.occlusion,
        hostBottom: j.hostBottom === undefined ? "-" : j.hostBottom,
        hostCount: j.hosts === undefined ? "-" : j.hosts,
        scroll: j.rootScroll, pan: j.panned || 0,
        ink: s.probe && s.probe.visualBottom ? `${s.probe.visualBottom.tag}${s.probe.visualBottom.id ? "#" + s.probe.visualBottom.id : ""}` : "-",
      });
    }
  }
  if (!r.checked) r.inconclusive.push("no in-scope frames (every frame was zoomed or incomplete)");
  else if (!r.stableFrames) r.inconclusive.push("no stable frames (manual/settle/periodic/*hint) — nothing has settled");
  else if (!r.rowsFrames) r.inconclusive.push("no stable frame carried xtermRows — the rows invariant was never observed");
  else if (!r.hostFrames) r.inconclusive.push("no frame carried a PTY host — the local avoidance was never observed (web-kind only?)");
  else if (!r.overlayFrames) r.inconclusive.push("no stable frame carried a VISIBLE #terminal-controls / #status-bar — the quick-keys-reachability half of contract v3.0 was never observed");
  else if (!r.quickKeysFrames) r.inconclusive.push("no stable frame carried an OPEN #quick-keys-panel — the panel's usable height inside the shortened column was never observed (open the quick-keys panel while capturing)");
  else if (r.inkUnproven.length === r.checked) {
    r.inconclusive.push(`not one in-scope frame carried probe.visualBottom (first: seq ${r.inkUnproven[0].seq}) — the bottom pixels' OWNER was never observed, so invariant 5 cannot be judged`);
  } else if (r.inkUnproven.length) {
    r.inconclusive.push(`${r.inkUnproven.length}/${r.checked} in-scope frame(s) carried no probe.visualBottom (first: seq ${r.inkUnproven[0].seq}) — invariant 5 is judged on fewer frames than the run covers`);
  }

  const failed = Object.values(r.failures).reduce((n, list) => n + list.length, 0);
  r.verdict = failed ? "FAIL" : (r.inconclusive.length ? "INCONCLUSIVE" : "PASS");
  r.failedCount = failed;
  return r;
}

function report(events) {
  const r = evaluateFrames(events);
  console.log(`events: ${events.length}   checked: ${r.checked}   zoom-exempt: ${r.zoomExempt}   host frames: ${r.hostFrames}   probeVersion: ${r.versions.join(", ") || "-"}`);
  console.log(`layoutGap (clientHeight - app.bottom) range: ${round1(r.minLayoutGap)} .. ${round1(r.maxLayoutGap)}   panned frames (informational, iOS pans the visual viewport): ${r.panned}`);
  console.log("");
  console.log("manual captures:");
  console.log("  seq   vv.height  clientH  inline     gap    occl   hostBottom hosts rootScroll  ink");
  for (const row of r.manualRows) {
    console.log(`  ${String(row.seq).padEnd(5)} ${String(row.vv).padEnd(10)} ${String(row.client).padEnd(8)} ${String(row.inline).padEnd(10)} ${String(row.layoutGap).padEnd(6)} ${String(row.occlusion).padEnd(6)} ${String(row.hostBottom).padEnd(10)} ${String(row.hostCount).padEnd(5)} ${String(row.scroll).padEnd(11)} ${row.ink}`);
  }
  if (!r.manualRows.length) console.log("  (none — press Capture on the panel to get rows here)");
  console.log("");
  const mark = (list) => (list.length ? "FAIL" : "ok  ");
  console.log(`1. shell full height (|clientHeight - app.bottom| <= 1)   ${mark(r.failures.shellShort)}  (${r.failures.shellShort.length} frames)`);
  console.log(`2. #app inline height never written (retired pin)          ${mark(r.failures.pinned)}  (${r.failures.pinned.length} frames)`);
  console.log(`3. occlusion == live-session shortfall (held raw, else 0)    ${mark(r.failures.occlusion)}  (${r.failures.occlusion.length} frames)`);
  console.log(`4. every PTY host bottom == occlusion                      ${mark(r.failures.host)}  (${r.failures.host.length} frames)`);
  console.log(`5. no residue across hosts (one value everywhere)          ${mark(r.failures.residue)}  (${r.failures.residue.length} frames)`);
  console.log(`6. host never under keyboard (avoiding frames only)       ${mark(r.failures.underKeyboard)}  (${r.failures.underKeyboard.length} frames)`);
  console.log(`7. root origin (document scrollTop == 0)                     ${mark(r.failures.scroll)}  (${r.failures.scroll.length} frames)`);
  console.log(`8. root has NO scroll range (scrollHeight <= clientHeight)  ${mark(r.failures.range)}  (${r.failures.range.length} frames)`);
  console.log(`9. structure (root clip + fixed shell)                       ${mark([...r.failures.overflow, ...r.failures.position].length)}  (${[...r.failures.overflow, ...r.failures.position].length} frames)`);
  console.log(`10. bottom ink never BODY/HTML (other hits = auxiliary)     ${mark(r.failures.ink)}  (${r.failures.ink.length} frames, ${r.inkUnproven.length} unproven)`);
  console.log(`11. xterm rows inside vv (avoiding stable frames)          ${mark(r.failures.rows)}  (${r.rowsFrames} observed)`);
  console.log(`12. quick-keys column bottom == 20 + occlusion (stable)     ${mark([...r.failures.controls, ...r.failures.controlsUnder].length)}  (${[...r.failures.controls, ...r.failures.controlsUnder].length} frames, ${r.overlayFrames} observed)`);
  console.log(`13. status bar bottom == occlusion (stable; under@avoid)   ${mark([...r.failures.statusBar, ...r.failures.statusBarUnder].length)}  (${[...r.failures.statusBar, ...r.failures.statusBarUnder].length} frames)`);
  console.log(`14. panel fits the shortened column (max-height:100%)       ${mark(r.failures.quickKeys.length)}  (${r.failures.quickKeys.length} frames, ${r.quickKeysFrames} open-panel observed)`);
  for (const [name, list] of Object.entries(r.failures)) {
    if (!list.length) continue;
    console.log(`\n  ${name} failures (first 8):`);
    for (const f of list.slice(0, 8)) console.log("   ", JSON.stringify(f));
  }
  if (r.inconclusive.length) {
    console.log(`\nINCONCLUSIVE — evidence is incomplete, this run cannot certify the build (${r.inconclusive.length} reasons):`);
    for (const line of r.inconclusive.slice(0, 8)) console.log("  - " + line);
  }
  console.log(`\nNOTE: a PASS here is geometry + hit evidence only. The final judgement is the screenshot set the SOP asks for (panel dragged off the bottom, one shot per state) — this script has never seen a pixel.`);
  console.log(`VERDICT: ${r.verdict}`);
  return r;
}

// Fixtures for --self-test. Each row is a minimal snapshot plus the verdict it
// must produce, so the READING is tested, not just the plumbing: a resized shell
// must fail, a full-height shell with the keyboard over it must PASS (the old
// "crossing" FAIL is retired), a stale host must fail, and missing evidence must
// never come back green.
function selfTest() {
  // `over` merges ONE LEVEL DOWN on purpose. Several fixtures below override
  // only `app.bottom` or only `document.scrollTop`; a shallow replace would drop
  // the sibling structural fields, every such frame would come back "missing
  // fields / INCONCLUSIVE", and the fixtures that exist to prove the local
  // avoidance would silently stop testing anything. `probe` is the exception: two
  // fixtures are specifically about a probe that is absent or empty, so whatever a
  // case says about the probe is taken verbatim.
  const LAYOUT = 702;
  const HOST_FLOOR = 682; // LAYOUT minus the 20px safe-area inset, as measured
  const hostsAt = (occlusion, extra = {}) => [
    { index: 0, top: 0, bottom: HOST_FLOOR - occlusion, height: 0, inlineBottom: occlusion + "px", computedBottom: occlusion + "px", visible: true, ...extra },
  ];
  // The overlay column and the transport strip, derived from the SAME occlusion
  // the hosts get, each from its OWN authored bottom. Written here rather than
  // typed per fixture so no fixture can accidentally describe the shape this round
  // forbids (hosts lifted, quick-keys left under the keyboard) and still read as
  // a PASS. `column` is the height #quick-keys-panel's max-height resolves
  // against, so the panel is exactly as tall as the space the lift leaves it and
  // scrolls inside it (scrollHeight is deliberately taller).
  const COLUMN_TOP = 20;
  const stripAt = (occlusion, extra = {}) => ({ top: HOST_FLOOR - occlusion - 17, bottom: HOST_FLOOR - occlusion, width: 120, height: 17, inlineBottom: occlusion + "px", computedBottom: occlusion + "px", visible: true, ...extra });
  const columnHeightAt = (occlusion) => HOST_FLOOR - COLUMN_TOP - CONTROLS_AUTHORED_BOTTOM - occlusion;
  const columnAt = (occlusion, extra = {}) => ({ top: COLUMN_TOP, bottom: HOST_FLOOR - CONTROLS_AUTHORED_BOTTOM - occlusion, width: 44, height: columnHeightAt(occlusion), inlineBottom: (CONTROLS_AUTHORED_BOTTOM + occlusion) + "px", computedBottom: (CONTROLS_AUTHORED_BOTTOM + occlusion) + "px", visible: true, ...extra });
  const panelAt = (occlusion, extra = {}) => ({ top: COLUMN_TOP, bottom: HOST_FLOOR - CONTROLS_AUTHORED_BOTTOM - occlusion, width: 200, height: columnHeightAt(occlusion), maxHeight: columnHeightAt(occlusion) + "px", clientHeight: columnHeightAt(occlusion), scrollHeight: columnHeightAt(occlusion) + 96, open: true, ...extra });
  const base = {
    probeVersion: EXPECTED_PROBE_VERSION,
    reason: "manual",
    clientHeight: LAYOUT,
    vv: { height: LAYOUT, offsetTop: 0, scale: 1 },
    // The focus half of the v3.2 session gate: the terminal's editable helper is
    // a TEXTAREA, so keyboard fixtures really do carry the evidence the session
    // machine needs (a frame WITHOUT ui is inconclusive by design, see judge).
    ui: { activeElementTag: "TEXTAREA" },
    app: { top: 0, bottom: LAYOUT, height: LAYOUT, inlineHeight: "", computedPosition: "fixed" },
    occlusion: 0,
    ptyHosts: hostsAt(0),
    document: { scrollTop: 0, scrollingElementScrollTop: 0, pageTop: 0, htmlOverflow: "clip", bodyOverflow: "clip", rootScrollHeight: LAYOUT, scrollingElementScrollHeight: LAYOUT },
    probe: { visualBottom: { tag: "DIV", id: "workspace", background: "rgb(11, 16, 32)" } },
    xtermRows: { top: 40, bottom: 669 },
  };
  // A keyboard ENTERING is a keyboard fixture: one thing — the visible height —
  // and the shell, the session occlusion, the host bottom and the overlays derive
  // from it, so no fixture can accidentally describe the retired shape (a shorter
  // shell AND a lifted host). v3.2: keyboard() is the ENTRY frame — editable focus
  // (base.ui) with raw >= 50% of the layout height, the session opens, hosts pay
  // RAW. It THROWS on a shallow height on purpose: inside a live session (pay raw)
  // and outside one (pay 0) the SAME shallow height means different things, and a
  // helper may not invent session history — those frames are spelled out.
  const keyboard = (visible) => {
    const raw = Math.max(0, LAYOUT - visible);
    if (raw < LAYOUT * KEYBOARD_ENTRY_RATIO) {
      throw new Error(`keyboard(${visible}) never ENTERS a v3.2 session (raw ${raw} < ${LAYOUT * KEYBOARD_ENTRY_RATIO}) — spell this frame's occlusion explicitly`);
    }
    return {
      vv: { height: visible, offsetTop: 0, scale: 1 },
      occlusion: raw,
      ptyHosts: hostsAt(raw),
      xtermRows: { top: 40, bottom: HOST_FLOOR - raw - 13 },
    };
  };
  // The never-entered shallow frame: editable focus is there but raw stays under
  // the 50% entry line, so the session machine owes NOTHING to any box.
  const shallow = (visible) => {
    const raw = Math.max(0, LAYOUT - visible);
    if (raw >= LAYOUT * KEYBOARD_ENTRY_RATIO) {
      throw new Error(`shallow(${visible}) clears the entry line — that is a keyboard(), use keyboard(${visible})`);
    }
    return {
      vv: { height: visible, offsetTop: 0, scale: 1 },
      occlusion: 0,
      ptyHosts: hostsAt(0),
      xtermRows: { top: 40, bottom: HOST_FLOOR - 13 },
    };
  };
  // A HOLD frame of a live session: a PRIOR keyboard() opened the session at a
  // tall raw shortfall, and the keyboard is now retracting to a SHORTER visible
  // height. Because the session is already active, the machine pays the RAW here
  // with NO floor — 340 / 206.1 / 147 / 68.5 exactly — which is the whole point of
  // v3.2: the same shallow height that a never-entered frame pays 0, a live session
  // pays in full. Valid ONLY inside a session; the fixture's seq order is what makes
  // it true (the judge threads the state, not this helper).
  const holdSession = (visible) => {
    const raw = Math.max(0, LAYOUT - visible);
    if (raw <= 1) {
      throw new Error(`holdSession(${visible}) is a CLOSED session (raw ${raw} <= 1) — that frame owes 0, spell it with shallow()/rest`);
    }
    return {
      vv: { height: visible, offsetTop: 0, scale: 1 },
      occlusion: raw,
      ptyHosts: hostsAt(raw),
      xtermRows: { top: 40, bottom: HOST_FLOOR - raw - 13 },
    };
  };
  const snap = (over = {}) => {
    const merged = {
      ...base,
      ...over,
      vv: { ...base.vv, ...(over.vv || {}) },
      app: { ...base.app, ...(over.app || {}) },
      document: { ...base.document, ...(over.document || {}) },
      ptyHosts: over.ptyHosts || hostsAt(num(over.occlusion) ?? 0),
    };
    // Every fixture pays the overlay half unless it says otherwise, so the
    // fixtures that fail on the HOST keep failing on the host alone, and a
    // fixture that wants to test the overlay half overrides just that box.
    const owedByFrame = num(merged.occlusion) ?? 0;
    for (const [key, built] of [["controls", columnAt], ["statusBar", stripAt], ["quickKeys", panelAt]]) {
      if (!(key in over)) merged[key] = built(owedByFrame);
    }
    if (!("occlusion" in over)) merged.occlusion = 0;
    if (!("probe" in over)) merged.probe = base.probe;
    return merged;
  };
  const cases = [
    { name: "at rest, full height, hosts flush at 682", events: [
      { seq: 1, snapshot: snap() }, { seq: 2, snapshot: snap({ reason: "periodic" }) }], want: "PASS" },
    { name: "soft keyboard 274: shell STILL full height, hosts lifted 428px", events: [
      { seq: 1, snapshot: snap({ reason: "keyboard-hint", ...keyboard(274) }) }], want: "PASS" },
    // v3.2 HOLD — the half the retired 0.25 floor got BACKWARDS: once the session
    // has ENTERED at 274 (seq1), the SAME keyboard retracting through 555 (raw
    // 147) and 633.5 (raw 68.5) pays RAW at every pass with NO line on it — the
    // hosts follow raw, they do NOT snap back to flush mid-transition. The judge
    // proves this is a session, not a ratio: the same 555/633.5 frames with no
    // entered session (below) owe 0.
    { name: "live session HOLDS at raw: 274 (428) then 555 (147) then 633.5 (68.5) — no floor snaps it back", events: [
      { seq: 1, snapshot: snap({ reason: "keyboard-hint", ...keyboard(274) }) },
      { seq: 2, snapshot: snap({ reason: "settle", ...holdSession(555) }) },
      { seq: 3, snapshot: snap({ reason: "settle", ...holdSession(633.5) }) }], want: "PASS" },
    // v3.2 EXIT and the closed-session exemption: the keyboard closes (raw <= 1 at
    // seq2), the session LEAVES, and the shallow frames after it (555 → 147,
    // 633.5 → 68.5) with editable focus still owe NOTHING — a closed session
    // cannot be re-opened under the 50% entry line. This is the same geometry the
    // HOLD fixture above pays raw; here the state, not the ratio, says 0.
    { name: "closed session: full-height exit then shallow 555 / 633.5 stay flush with editable focus", events: [
      { seq: 1, snapshot: snap({ reason: "keyboard-hint", ...keyboard(274) }) },
      { seq: 2, snapshot: snap({ reason: "settle" }) },
      { seq: 3, snapshot: snap({ reason: "settle", ...shallow(555) }) },
      { seq: 4, snapshot: snap({ reason: "settle", ...shallow(633.5) }) }], want: "PASS" },
    // v3.2 RE-ENTRY: a shallow never-opened strip does not block the next REAL
    // keyboard — a new 274 frame crosses the entry line and the session opens.
    { name: "shallow strip first, then a NEW soft keyboard 274 ENTERS and lifts 428px", events: [
      { seq: 1, snapshot: snap({ reason: "settle", ...shallow(633.5) }) },
      { seq: 2, snapshot: snap({ reason: "keyboard-hint", ...keyboard(274) }) }], want: "PASS" },
    // v3.2 FOCUS edges: a non-editable focus ENDS the session (seq2 owes 0 even
    // at raw 428), and an INPUT focus enters it (seq4). contenteditable is
    // invisible to the probe (tagName only) — the NOTE in the header owns that gap.
    { name: "BODY focus exits the session (hosts flush again); INPUT focus enters it", events: [
      { seq: 1, snapshot: snap({ reason: "keyboard-hint", ...keyboard(274) }) },
      { seq: 2, snapshot: snap({ reason: "settle", ui: { activeElementTag: "BODY" }, ...shallow(555) }) },
      { seq: 3, snapshot: snap({ reason: "settle", ui: { activeElementTag: "BODY" } }) },
      { seq: 4, snapshot: snap({ reason: "keyboard-hint", ui: { activeElementTag: "INPUT" }, ...keyboard(274) }) }], want: "PASS" },
    // v3.2 ENFORCEMENT: outside a session, lifting is a fault. A thin strip that
    // STILL lifts its hosts is the retired no-floor arithmetic returning — owed 0
    // because no session ever entered at 68.5, so a host at inline 68.5px
    // disagrees and FAILS.
    { name: "thin strip 633.5 (no session) that WRONGLY lifts hosts to 68.5px -> FAIL host", events: [
      { seq: 1, snapshot: snap({ reason: "settle", vv: { height: 633.5, offsetTop: 0, scale: 1 }, occlusion: 0, ptyHosts: hostsAt(68.5), controls: columnAt(0), statusBar: stripAt(0), quickKeys: panelAt(0) }) }], want: "FAIL" },
    // THE OTHER DIRECTION: 555 outside a session owes NOTHING (raw 147 < 351 and
    // no session entered). A build that lifts its hosts to 147px is the stateless
    // arithmetic coming back — the transient that made the terminal bounce ~8.6
    // rows and climb the Transport off the bottom on the device — so it must FAIL.
    { name: "shallow frame 555 outside a session that WRONGLY lifts hosts to 147px -> FAIL host", events: [
      { seq: 1, snapshot: snap({ reason: "settle", vv: { height: 555, offsetTop: 0, scale: 1 }, occlusion: 147, ptyHosts: hostsAt(147), controls: columnAt(147), statusBar: stripAt(147), quickKeys: panelAt(147) }) }], want: "FAIL" },
    // THE THIRD DIRECTION: lifting DURING the session that should hold raw. The
    // judge threads 555 AFTER a 274 entry — owed 147 — so this fixture is spelled
    // with the entry first and a WRONG full-lift after: hosts at 428 mid-retract
    // disagree with the held raw 147 and FAIL host (residue of the entry value).
    { name: "mid-session 555 frozen at the entry lift 428 (no follow of raw) -> FAIL host", events: [
      { seq: 1, snapshot: snap({ reason: "keyboard-hint", ...keyboard(274) }) },
      { seq: 2, snapshot: snap({ reason: "settle", vv: { height: 555, offsetTop: 0, scale: 1 }, occlusion: 428, ptyHosts: hostsAt(428), controls: columnAt(428), statusBar: stripAt(428), quickKeys: panelAt(428) }) }], want: "FAIL" },
    { name: "REAL floating overlay: vv.height == clientHeight -> occlusion 0", events: [
      { seq: 1, snapshot: snap({ reason: "magic-hint" }) }], want: "PASS" },
    // THE RETIRED READING: app.bottom at the layout height while the keyboard
    // covers the rest is what v3.0 WANTS. Under v2.2 this exact frame FAILED the
    // "crossing" bucket, and that is the judgement this round retires.
    { name: "shell under the keyboard (vv.height 274 < app.bottom 702) is EXPECTED now", events: [
      { seq: 1, snapshot: snap({ reason: "keyboard-hint", vv: { height: 274, offsetTop: 0, scale: 1 }, app: { bottom: LAYOUT, inlineHeight: "" }, occlusion: 428, ptyHosts: hostsAt(428), xtermRows: { top: 40, bottom: 241 } }) }], want: "PASS" },
    { name: "the retired pin came back: #app inline height 633.5px -> FAIL", events: [
      { seq: 1, snapshot: snap({ reason: "keyboard-hint", ...shallow(633.5), app: { bottom: 633.5, inlineHeight: "633.5px" } }) }], want: "FAIL" },
    { name: "shell resized but inline cleared (shorter than the layout) -> FAIL shellShort", events: [
      { seq: 1, snapshot: snap({ reason: "settle", app: { bottom: 690, inlineHeight: "" } }) }], want: "FAIL" },
    { name: "raw layoutGap 1.04px FAILS even though it prints 1.0", events: [
      { seq: 1, snapshot: snap({ app: { bottom: 700.96, inlineHeight: "" } }) }], want: "FAIL" },
    { name: "raw layoutGap exactly 1px is inside the tolerance (boundary inclusive)", events: [
      { seq: 1, snapshot: snap({ app: { bottom: 701, inlineHeight: "" } }) }], want: "PASS" },
    { name: "host left at inset:0 while the keyboard covers it -> FAIL host", events: [
      { seq: 1, snapshot: snap({ reason: "keyboard-hint", ...keyboard(274), ptyHosts: hostsAt(0) }) }], want: "FAIL" },
    { name: "host bottom below the visible bottom -> FAIL underKeyboard", events: [
      { seq: 1, snapshot: snap({ reason: "keyboard-hint", ...keyboard(274), ptyHosts: [{ index: 0, top: 0, bottom: 300, height: 300, inlineBottom: "428px", computedBottom: "428px", visible: true }] }) }], want: "FAIL" },
    { name: "STALE HIDDEN HOST keeps the old bottom -> FAIL residue", events: [
      // Anchored on 274 (a LIVE session entered at raw 428) so this fixture fails
      // on the RESIDUE — two hosts disagreeing — and not, by accident, on whether
      // a shallow frame is exempt.
      { seq: 1, snapshot: snap({ reason: "settle", ...keyboard(274), ptyHosts: [...hostsAt(428), { index: 1, top: 0, bottom: HOST_FLOOR, height: HOST_FLOOR, inlineBottom: "0px", computedBottom: "0px", visible: false }] }) }], want: "FAIL" },
    { name: "probe's occlusion disagrees with clientHeight - vv.height -> FAIL occlusion", events: [
      // 274 owes its RAW shortfall (428): a probe reporting 0 disagrees. The 555
      // frame is NOT a second anchor any more — OUTSIDE a session it owes 0, so a
      // probe reporting 0 there is CORRECT — which is why this
      // stays anchored on 274, the biggest shortfall in the table.
      { seq: 1, snapshot: snap({ reason: "keyboard-hint", ...keyboard(274), occlusion: 0 }) }], want: "FAIL" },
    { name: "rows below the visible bottom on a stable frame -> FAIL rows", events: [
      { seq: 1, snapshot: snap({ reason: "keyboard-hint", ...keyboard(274), xtermRows: { top: 40, bottom: 300 } }) }], want: "FAIL" },
    { name: "rows below vv during resize are a transient, not asserted (settle decides)", events: [
      { seq: 1, snapshot: snap({ reason: "resize", ...keyboard(274), xtermRows: { top: 40, bottom: 669 } }) },
      { seq: 2, snapshot: snap({ reason: "settle", ...keyboard(274) }) }], want: "PASS" },
    { name: "root scroll appears -> FAIL scroll (and nothing recovers it)", events: [
      { seq: 1, snapshot: snap({ document: { scrollTop: 122, scrollingElementScrollTop: 122, pageTop: 122, htmlOverflow: "clip", bodyOverflow: "clip", rootScrollHeight: LAYOUT, scrollingElementScrollHeight: LAYOUT } }) }], want: "FAIL" },
    { name: "Gmail-shape regression: a root scroll RANGE appeared under the keyboard -> FAIL range", events: [
      { seq: 1, snapshot: snap({ reason: "keyboard-hint", ...keyboard(274), document: { scrollTop: 0, scrollingElementScrollTop: 0, pageTop: 0, htmlOverflow: "clip", bodyOverflow: "clip", rootScrollHeight: 900, scrollingElementScrollHeight: 900 } }) }], want: "FAIL" },
    { name: "root scroll range 1px over clientHeight is inside the tolerance", events: [
      { seq: 1, snapshot: snap({ document: { ...base.document, rootScrollHeight: 703 } }) }], want: "PASS" },
    { name: "root still overflow:hidden (v2.1 build) -> FAIL, scroll mechanism exists", events: [
      { seq: 1, snapshot: snap({ document: { ...base.document, htmlOverflow: "hidden", bodyOverflow: "hidden" } }) }], want: "FAIL" },
    { name: "shell not fixed -> FAIL, the document still carries the flow", events: [
      { seq: 1, snapshot: snap({ app: { top: 0, bottom: LAYOUT, height: LAYOUT, inlineHeight: "", computedPosition: "relative" } }) }], want: "FAIL" },
    { name: "root canvas hit at the visible bottom -> FAIL ink", events: [
      { seq: 1, snapshot: snap({ probe: { visualBottom: { tag: "BODY", id: "", background: "rgb(249, 249, 249)" } } }) }], want: "FAIL" },
    { name: "some OTHER element hit at the bottom is auxiliary, not a verdict", events: [
      { seq: 1, snapshot: snap({ probe: { visualBottom: { tag: "DIV", id: "", className: "xterm-screen", background: "rgba(0, 0, 0, 0)" } } }) }], want: "PASS" },
    { name: "zoom frame is exempt end to end", events: [
      { seq: 1, snapshot: snap({ vv: { height: 400, offsetTop: 120, scale: 1.5 }, occlusion: 0, ptyHosts: hostsAt(0), document: { ...base.document, scrollTop: 90, scrollingElementScrollTop: 90, pageTop: 90 } }) },
      { seq: 2, snapshot: snap({ reason: "settle" }) }], want: "PASS" },
    { name: "zoom frame without probe stays exempt (zoom asserts no ink)", events: [
      { seq: 1, snapshot: snap({ vv: { height: 400, offsetTop: 120, scale: 1.5 }, probe: undefined }) },
      { seq: 2, snapshot: snap({ reason: "settle" }) }], want: "PASS" },
    // v3.2 ZOOM CLOSES A LIVE SESSION: the zoomed frame itself is exempt from the
    // geometry verdict, but the machine still LEAVES the session — so the first
    // un-zoomed frame after the pinch sits at raw-under-entry and owes 0. A judge
    // that skipped zoom frames without advancing state would call seq3 mid-session
    // and pay 147 — this fixture is the difference between the two machines.
    { name: "zoom during a live session closes it: post-pinch 555 owes 0, hosts flush -> PASS", events: [
      { seq: 1, snapshot: snap({ reason: "keyboard-hint", ...keyboard(274) }) },
      { seq: 2, snapshot: snap({ reason: "resize", vv: { height: 400, offsetTop: 120, scale: 1.5 }, occlusion: 0, ptyHosts: hostsAt(0) }) },
      { seq: 3, snapshot: snap({ reason: "settle", ...shallow(555) }) }], want: "PASS" },
    // Geometry complete, ink evidence absent — which must never read as a PASS of
    // invariant 10.
    { name: "valid geometry, NO PROBE AT ALL -> INCONCLUSIVE", events: [
      { seq: 1, snapshot: snap({ probe: undefined }) }], want: "INCONCLUSIVE" },
    { name: "valid geometry, probe but NO probe.visualBottom -> INCONCLUSIVE", events: [
      { seq: 1, snapshot: snap({ probe: {} }) },
      { seq: 2, snapshot: snap({ reason: "settle", probe: { layoutBottom: { tag: "DIV", id: "workspace" } } }) }], want: "INCONCLUSIVE" },
    { name: "MISSING GEOMETRY FIELDS -> INCONCLUSIVE (frame incomplete)", events: [
      { seq: 1, snapshot: { reason: "manual", vv: { height: 702, scale: 1 }, clientHeight: 702 } }], want: "INCONCLUSIVE" },
    { name: "no ptyHosts field at all (older instrument) -> INCONCLUSIVE", events: [
      { seq: 1, snapshot: (() => { const s = snap(); delete s.ptyHosts; return s; })() }], want: "INCONCLUSIVE" },
    { name: "web-kind frame with NO PTY host anywhere -> INCONCLUSIVE (avoidance unobserved)", events: [
      { seq: 1, snapshot: snap({ ptyHosts: [] }) }], want: "INCONCLUSIVE" },
    { name: "seq HOLE -> INCONCLUSIVE (never silently green)", events: [
      { seq: 1, snapshot: snap() }, { seq: 3, snapshot: snap({ reason: "periodic" }) }], want: "INCONCLUSIVE" },
    { name: "duplicated seq -> INCONCLUSIVE", events: [
      { seq: 1, snapshot: snap() }, { seq: 1, snapshot: snap({ reason: "periodic" }) }], want: "INCONCLUSIVE" },
    { name: "stale probeVersion -> INCONCLUSIVE", events: [
      { seq: 1, snapshot: snap({ probeVersion: "p4-reconcile-wheel" }) }], want: "INCONCLUSIVE" },
    { name: "only transient frames -> INCONCLUSIVE", events: [
      { seq: 1, snapshot: snap({ reason: "scroll" }) }, { seq: 2, snapshot: snap({ reason: "resize" }) }], want: "INCONCLUSIVE" },
    // Written OUTSIDE snap() on purpose: snap merges the p7 fields back in, and
    // this fixture is precisely a p6 capture where they were never sent — it
    // proves the p6 instrument cannot certify a v3.0 build.
    { name: "p6 capture (no occlusion / ptyHosts / layoutGap) -> INCONCLUSIVE", events: [
      { seq: 1, snapshot: {
        probeVersion: "p6-structural-no-scroll-v1", reason: "manual", clientHeight: 702,
        vv: { height: 702, offsetTop: 0, scale: 1 },
        app: { top: 0, bottom: 702, height: 702, inlineHeight: "", computedPosition: "fixed" },
        document: { scrollTop: 0, scrollingElementScrollTop: 0, pageTop: 0, htmlOverflow: "clip", bodyOverflow: "clip", rootScrollHeight: 702, scrollingElementScrollHeight: 702 },
        probe: { visualBottom: { tag: "DIV", id: "workspace" } },
        xtermRows: { top: 40, bottom: 669 },
      } }], want: "INCONCLUSIVE" },
    // THE OVERLAY HALF — the part of contract v3.0 that keeps the QUICK-KEYS and
    // the transport strip reachable. These are stated with literal numbers rather
    // than through columnAt()/stripAt() on purpose: the helpers encode the
    // arithmetic, and a helper that computed 428 + 20 as 428 would make every
    // derived fixture agree with a broken implementation.
    { name: "keyboard 274: hosts 428px, column 448px, strip 428px -> PASS", events: [
      { seq: 1, snapshot: snap({ reason: "keyboard-hint", ...keyboard(274),
        controls: { top: 20, bottom: 234, width: 44, height: 214, inlineBottom: "448px", computedBottom: "448px", visible: true },
        statusBar: { top: 237, bottom: 254, width: 120, height: 17, inlineBottom: "428px", computedBottom: "428px", visible: true },
        quickKeys: { top: 20, bottom: 234, width: 200, height: 214, maxHeight: "214px", clientHeight: 214, scrollHeight: 310, open: true } }) }], want: "PASS" },
    // THE 555 TRIPLE IN LITERAL NUMBERS — a shallow frame OUTSIDE any session,
    // typed and not derived: raw 147 never crossed the 351px entry line, so
    // NOTHING is owed and each box sits at its OWN authored value (hosts flush at
    // 682, column 20 + 0 = 20, strip 0). Typed, for the same reason the 274 triple
    // above is: a helper that computed a lift where none is owed would agree with
    // the stateless 0.15/0.25 arithmetic coming back — inside a live session the
    // SAME 555 frame owes raw 147 (the HOLD fixture says so); what makes this frame
    // flush is the CLOSED session, and a fixture helper may not invent which one
    // this seq belongs to.
    { name: "shallow frame 555 outside a session: hosts 0px, column 20px, strip 0px -> PASS (nothing moves)", events: [
      { seq: 1, snapshot: snap({ reason: "keyboard-hint", ...shallow(555),
        controls: { top: 20, bottom: 662, width: 44, height: 642, inlineBottom: "20px", computedBottom: "20px", visible: true },
        statusBar: { top: 665, bottom: 682, width: 120, height: 17, inlineBottom: "0px", computedBottom: "0px", visible: true },
        quickKeys: { top: 20, bottom: 662, width: 200, height: 642, maxHeight: "642px", clientHeight: 642, scrollHeight: 738, open: true } }) }], want: "PASS" },
    { name: "quick-keys column left at its authored 20px while the keyboard covers it -> FAIL controls", events: [
      { seq: 1, snapshot: snap({ reason: "keyboard-hint", ...keyboard(274),
        controls: { top: 20, bottom: 662, width: 44, height: 642, inlineBottom: "20px", computedBottom: "20px", visible: true } }) }], want: "FAIL" },
    // THE HELPFUL MISTAKE: the same target written to all three boxes. Every
    // host check passes, the strip passes, and the column sits 20px LOW — inside
    // the transport strip's band. Only a check that knows the column's authored
    // clearance can see this, which is why 448 is asserted here and not 428.
    { name: "column given the HOST target (428px instead of 448px) -> FAIL controls", events: [
      { seq: 1, snapshot: snap({ reason: "keyboard-hint", ...keyboard(274),
        controls: { top: 20, bottom: 254, width: 44, height: 234, inlineBottom: "428px", computedBottom: "428px", visible: true } }) }], want: "FAIL" },
    { name: "status bar left at bottom 0 while the keyboard covers it -> FAIL statusBar", events: [
      { seq: 1, snapshot: snap({ reason: "keyboard-hint", ...keyboard(274),
        statusBar: { top: 665, bottom: 682, width: 120, height: 17, inlineBottom: "0px", computedBottom: "0px", visible: true } }) }], want: "FAIL" },
    { name: "panel taller than its shortened column -> FAIL quickKeys (keys run under the keyboard)", events: [
      { seq: 1, snapshot: snap({ reason: "keyboard-hint", ...keyboard(274),
        quickKeys: { top: 20, bottom: 662, width: 200, height: 642, maxHeight: "642px", clientHeight: 642, scrollHeight: 642, open: true } }) }], want: "FAIL" },
    // The column's height lost MORE than the occlusion: its top moved, so the
    // quick-keys ENTRY BUTTON jumped. `top: 20px` is never written by the
    // avoidance, and this is the shape that proves it was.
    { name: "column shrank by the wrong amount (top moved) -> FAIL quickKeys", events: [
      { seq: 1, snapshot: snap({ reason: "settle" }) },
      // Anchored on 274 (owed 428, controls 448): the column must shorten by the
      // occlusion with its top held at 20. A 555 frame OUTSIDE a session owes 0
      // (controls back at 20) and inside one pays raw — so 274, the session opener,
      // is the keyboard that carries this verdict cleanly.
      { seq: 2, snapshot: snap({ reason: "settle", ...keyboard(274),
        controls: { top: 20, bottom: 320, width: 44, height: 300, inlineBottom: "448px", computedBottom: "448px", visible: true } }) }], want: "FAIL" },
    // A web-kind workspace hides the column with the .oc-hidden CLASS, which
    // zeroes its rect without clearing the value the avoidance wrote. Judging that
    // zeroed rect would FAIL a correct page, so a hidden column is skipped — and
    // the visible frame before it is what carries the invariant.
    { name: "column hidden (.oc-hidden, web kind) is skipped, never judged on a zeroed rect -> PASS", events: [
      { seq: 1, snapshot: snap({ reason: "settle", ...keyboard(274) }) },
      { seq: 2, snapshot: snap({ reason: "settle", ...keyboard(274),
        controls: { top: 0, bottom: 0, width: 0, height: 0, inlineBottom: "448px", computedBottom: "448px", visible: false },
        quickKeys: { top: 0, bottom: 0, width: 0, height: 0, maxHeight: "0px", clientHeight: 0, scrollHeight: 0, open: false } }) }], want: "PASS" },
    { name: "web-kind run where the column was NEVER visible -> INCONCLUSIVE (the lift was never observed)", events: [
      { seq: 1, snapshot: snap({ reason: "settle", ...keyboard(274),
        controls: { top: 0, bottom: 0, width: 0, height: 0, inlineBottom: "448px", computedBottom: "448px", visible: false } }) }], want: "INCONCLUSIVE" },
    { name: "controls / statusBar reported NULL (element absent) -> INCONCLUSIVE, never zero", events: [
      { seq: 1, snapshot: snap({ reason: "keyboard-hint", ...keyboard(274), controls: null, statusBar: null }) }], want: "INCONCLUSIVE" },
    { name: "frame without controls/statusBar at all (older instrument) -> INCONCLUSIVE", events: [
      { seq: 1, snapshot: (() => { const frame = snap({ reason: "keyboard-hint", ...keyboard(274) }); delete frame.controls; delete frame.statusBar; delete frame.quickKeys; return frame; })() }], want: "INCONCLUSIVE" },
    { name: "panel never opened during the capture -> INCONCLUSIVE (its usable height was never observed)", events: [
      { seq: 1, snapshot: snap({ reason: "keyboard-hint", ...keyboard(274),
        quickKeys: { top: 20, bottom: 234, width: 200, height: 214, maxHeight: "214px", clientHeight: 0, scrollHeight: 0, open: false } }) }], want: "INCONCLUSIVE" },
    { name: "empty capture -> INCONCLUSIVE", events: [], want: "INCONCLUSIVE" },
  ];
  let bad = 0;
  for (const c of cases) {
    const got = evaluateFrames(c.events).verdict;
    const ok = got === c.want;
    if (!ok) bad++;
    console.log(`${ok ? "ok  " : "FAIL"}  want=${c.want.padEnd(12)} got=${String(got).padEnd(12)} ${c.name}`);
  }
  console.log(bad ? `\n${bad} fixture(s) misjudged` : `\nall ${cases.length} fixtures judged as specified`);
  process.exit(bad ? 1 : 0);
}

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
    console.error("need --file, or --session + --base + MW_A0_TOKEN, or --self-test");
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

if (process.argv.includes("--self-test")) {
  selfTest();
} else {
  const events = await loadEvents();
  if (!events.length) {
    console.error("no events in this session yet — open the diagnostic URL and press Capture");
    process.exit(2);
  }
  const result = report(events);
  process.exit(result.verdict === "PASS" ? 0 : (result.verdict === "FAIL" ? 1 : 2));
}
