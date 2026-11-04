#!/usr/bin/env node
// Real-browser check of the PTY resize/refit chain (issue #116, contract
// p9-portrait-refit-import-v1). The last link the static gates cannot reach.
//
// WHY THIS FILE EXISTS.
//
// `tty_resize_refit.test.mjs` slices the shipped functions out of index.html and
// kinds/pty.js and runs them against a FAKE clock and FAKE sockets. It proves the
// throttle has a trailing edge and that a fit is never repainted at the previous
// ack. It cannot prove that a REAL viewport change reaches a REAL host box, that a
// REAL FitAddon recomputes the rows, and that the request then goes out over a
// REAL WebSocket. This file does exactly that, in headless Chrome over CDP:
//
//   1194x702  →  834x1062  →  1194x702
//
// through `Emulation.setDeviceMetricsOverride`, reading the live page between the
// steps (≥ one animation frame + `--settle-ms`, so the 100ms throttle window in
// queueTTYResize has closed and its trailing edge has fired).
//
// WHAT IT ASSERTS (all on numbers read back from the page, none inferred):
//   A. rows follow the host height      the taller host gives MORE local rows,
//                                          the restored host gives the baseline back;
//   B. columns were refitted too        a ~30% narrower host that reports the SAME
//                                          cols never ran fit() at all;
//   C. .xterm-rows agrees with rows     px-per-row is constant across phases, and
//                                          the rows container itself grew. This is the
//                                          p8 symptom in one number: host h1002 with
//                                          xtermRows still h646;
//   D. the ask matches the screen       over an OPEN socket the final
//                                          lastSentTTYResize equals the local cols/rows;
//   E. no stale-ack revert              the local size must not sit at a size that
//                                          was already superseded on the wire.
//
// WHAT IT DELIBERATELY DOES NOT ASSERT:
//   * it does NOT judge by `appliedTTYSize`. That is the server's answer, and a
//     server that has not echoed a new ack yet is not a client failure — a smaller
//     or missing ack is recorded as context, never as a FAIL (the p8 fix is
//     precisely that a local fit stays on screen while it waits);
//   * it is HEADLESS CHROME, not iPadOS Safari, and `setDeviceMetricsOverride` is
//     NOT the iOS soft keyboard. It certifies the browser's fit/ack chain at two
//     viewport sizes; the keyboard-driven occlusion is A0/SOP field evidence.
//
// VERDICTS: PASS / FAIL / INCONCLUSIVE → exit 0 / 1 / 2. INCONCLUSIVE is a real
// answer: no active terminal session, no OPEN socket, or a host box that never
// actually changed size means nothing about refit was proven, and the run says so
// instead of passing.
//
// Usage (evidence tool, not CI; Node built-ins + CDP only, no dependencies):
//   node docs/plans/fix-issue116/evidence/pty-refit-check.mjs \
//       --url "http://127.0.0.1:38235/?token=<token>" \
//       --chrome /usr/bin/google-chrome --port 9351 --label p9
//   node docs/plans/fix-issue116/evidence/pty-refit-check.mjs --self-test
//
// The token comes from the caller's --url and is never written here: every log
// line and the JSON evidence carry it redacted.
import { mkdirSync, mkdtempSync, rmSync, writeFileSync } from "node:fs";
import { spawn } from "node:child_process";
import { createServer } from "node:net";
import { tmpdir } from "node:os";
import { dirname, join } from "node:path";
import { fileURLToPath } from "node:url";

const HERE = dirname(fileURLToPath(import.meta.url));
const WS_OPEN = 1;
// A host that grew by less than this did not really change for the purpose of
// this check: 1194x702 → 834x1062 moves the host bottom by ~360px, so anything
// under 40px is sub-character noise, not a refit opportunity.
const HOST_GROW_MIN_PX = 40;
// Local row/col counts are integers measured from a rect; ±1 covers a scrollbar
// or a rounding difference between two identical viewports.
const ROW_TOLERANCE = 1;
// A stale rows container sits a whole character-height away from the truth
// (646/58 = 11.1px vs 17.0px/row), so 1px of drift is the tolerance, not the
// signal.
const PX_PER_ROW_TOLERANCE = 1;

function arg(name, fallback) {
  const i = process.argv.indexOf("--" + name);
  return i !== -1 && process.argv[i + 1] ? process.argv[i + 1] : fallback;
}
const sleep = (ms) => new Promise((r) => setTimeout(r, ms));
const redact = (u) => String(u).replace(/([?&]token=)[^&]*/gi, "$1<redacted>");
const round = (n) => (typeof n === "number" ? Math.round(n * 100) / 100 : n);

// ---------------------------------------------------------------------------
// The page-side measurement. Numbers only, no verdicts — the judgement lives in
// judge() so --self-test can drive it with fixtures and no browser.
// ---------------------------------------------------------------------------
const PROBE = `(() => {
  const R = (el) => {
    if (!el) return null;
    const r = el.getBoundingClientRect();
    return { top: r.top, bottom: r.bottom, left: r.left, right: r.right, width: r.width, height: r.height };
  };
  const env = {
    innerWidth: innerWidth,
    innerHeight: innerHeight,
    clientHeight: document.documentElement ? document.documentElement.clientHeight : null,
    vvHeight: window.visualViewport ? Math.round(window.visualViewport.height * 100) / 100 : null,
    dpr: window.devicePixelRatio || 1,
  };
  // The accessor is a top-level declaration of index.html's inline script, so it
  // is a global on a booted shell and absent on the login gate or a broken page.
  // It is also called inside a try, because a shell whose own boot threw before its
  // state was initialised leaves that state in a TDZ and the accessor then throws a
  // ReferenceError (measured, not theorised: a file:// load of this page does
  // exactly that). A thrown accessor is "nothing was measured here", reported as
  // such — never a lost phase, never an inferred zero.
  if (typeof getActiveTerminalSession !== "function") {
    return Object.assign(env, { hasSession: false, reason: "getActiveTerminalSession is not a function — the shell script never booted" });
  }
  let session = null;
  try {
    session = getActiveTerminalSession();
  } catch (e) {
    return Object.assign(env, { hasSession: false, reason: "getActiveTerminalSession threw: " + (e && e.message ? e.message : String(e)) });
  }
  if (!session) return Object.assign(env, { hasSession: false, reason: "no active terminal session (no instance selected, or none is a PTY)" });
  const term = session.term || null;
  const host = session.container || document.getElementById("terminal-container");
  const socket = session.ttySocket || null;
  if (!host) {
    return Object.assign(env, {
      hasSession: true, reason: "the active session has no host element",
      cols: term ? term.cols : 0, rows: term ? term.rows : 0,
      socketReadyState: socket ? socket.readyState : null, ttyState: session.ttyState || null,
    });
  }
  const applied = session.appliedTTYSize || null;
  const sent = session.lastSentTTYResize || null;
  const pending = session.pendingTTYResize || null;
  return Object.assign(env, {
    hasSession: true,
    instanceId: session.id || null,
    hostSource: session.container ? "session.container" : "#terminal-container",
    hostRect: R(host),
    xtermScreen: R(host.querySelector(".xterm-screen")),
    // null when the canvas renderer is active — reported, never guessed at.
    xtermRows: R(host.querySelector(".xterm-rows")),
    cols: term ? term.cols : 0,
    rows: term ? term.rows : 0,
    applied: applied ? { cols: applied.cols, rows: applied.rows } : null,
    lastSent: sent ? { cols: sent.cols, rows: sent.rows } : null,
    pending: pending ? { cols: pending.cols, rows: pending.rows } : null,
    socketReadyState: socket ? socket.readyState : null,
    ttyState: session.ttyState || null,
    fitted: !!session.fitAddon,
  });
})()`;

// Two frames: the first lets the reflow of the new metrics land, the second
// guarantees the paint after it, so the reading is of the settled box and not of
// a mid-layout intermediate. The race with a timer is not decoration: a throttled
// or occluded tab may never fire requestAnimationFrame, and a settle that waits
// forever is a hung run rather than a measured one — 2s is the bound, and the
// `frames:` field tells the reader which happened.
const TWO_FRAMES = `new Promise((resolve) => {
  let done = false;
  const finish = (how) => { if (!done) { done = true; resolve(how); } };
  setTimeout(() => finish("timeout"), 2000);
  requestAnimationFrame(() => requestAnimationFrame(() => finish("frames")));
})`;

const phaseRect = (p, key) => (p && p[key] ? p[key] : null);

// A free port, asked of the OS rather than guessed. This host runs several
// headless browsers at once, and a hard-coded port that a neighbour already holds
// turns the run into a 30-second wait for an endpoint that never answers.
function freePort() {
  return new Promise((resolve) => {
    const s = createServer();
    s.once("error", () => resolve(0));
    s.listen(0, "127.0.0.1", () => {
      const port = s.address().port;
      s.close(() => resolve(port));
    });
  });
}

// ---------------------------------------------------------------------------
// Pure judgement over the collected phases.
// ---------------------------------------------------------------------------
function judgePhase(name, p, base, baseUsable, fail, info, blocked) {
  const tag = "[" + name + "]";
  if (!p) { blocked.push(tag + " phase was never measured"); return; }
  if (!p.hasSession) {
    blocked.push(tag + " no active terminal session (" + (p.reason || p.error || "unknown") + ") — nothing was measured in this phase");
    return;
  }
  if (!(p.cols > 0) || !(p.rows > 0)) {
    blocked.push(tag + " term.cols/rows are not positive (" + p.cols + "x" + p.rows + ") — xterm never opened a sized screen");
    return;
  }
  const host = phaseRect(p, "hostRect");
  if (!host || !(host.height > 0)) { blocked.push(tag + " no host rect"); return; }

  // A. rows follow the host height — and C. the rows container grew with them.
  // Both are only meaningful once the host genuinely grew: if the driven metrics
  // never reached the box, nothing about refit was measured, and that is an
  // INCONCLUSIVE rather than a FAIL about a resize that never happened.
  const bHost = base && phaseRect(base, "hostRect");
  const grew = bHost ? host.height - bHost.height : 0;
  if (name === "tall" && baseUsable && base && base.rows > 0 && base.cols > 0 && grew >= HOST_GROW_MIN_PX) {
    if (p.rows <= base.rows) {
      fail.push(tag + " host grew by " + round(grew) + "px (" + round(bHost.height) + " → " + round(host.height) +
        ") but local rows stayed at " + p.rows + " (baseline " + base.rows + ") — fit() never picked up the taller box");
    } else {
      info.push(tag + " host " + round(bHost.height) + " → " + round(host.height) + "px, local rows " + base.rows + " → " + p.rows);
    }
    // B. columns refitted: a ~30% narrower host cannot keep the same cols.
    const charW = host.width / p.cols;
    const narrower = bHost.width - host.width;
    if (narrower > 2 * charW && p.cols === base.cols) {
      fail.push(tag + " host narrowed by " + round(narrower) + "px (~" + round(narrower / charW) +
        " characters) and cols stayed " + p.cols + " — the fit did not recompute the width either");
    } else if (narrower > 2 * charW) {
      info.push(tag + " cols " + base.cols + " → " + p.cols + " over " + round(bHost.width) + " → " + round(host.width) + "px of host");
    }
    // C. the rows container itself grew (the p8 symptom is a host that grew and an
    // .xterm-rows that did not).
    const bRows = phaseRect(base, "xtermRows");
    const rowsBox = phaseRect(p, "xtermRows");
    if (!rowsBox || !bRows) {
      blocked.push(tag + " .xterm-rows is not in the DOM (renderer=" + (rowsBox ? "dom" : "unknown/canvas") +
        ") — the rect half of this contract cannot be certified here");
    } else {
      if (rowsBox.height <= bRows.height + 20) {
        fail.push(tag + " host grew to " + round(host.height) + "px but .xterm-rows stayed " + round(bRows.height) +
          " → " + round(rowsBox.height) + "px — the rows container kept the old geometry while rows read " + p.rows +
          " (the p8 signature: h646 inside an h1002 host)");
      } else {
        info.push(tag + " .xterm-rows " + round(bRows.height) + " → " + round(rowsBox.height) + "px for " + p.rows + " rows");
      }
      const ratio = rowsBox.height / p.rows;
      const baseRatio = bRows.height / base.rows;
      if (Math.abs(ratio - baseRatio) > PX_PER_ROW_TOLERANCE) {
        fail.push(tag + " .xterm-rows height / rows = " + round(ratio) + "px but the baseline is " + round(baseRatio) +
          "px — the painted rows and term.rows disagree, so the screen is at a size xterm does not hold");
      }
      if (rowsBox.height > host.height + 2) {
        fail.push(tag + " .xterm-rows is " + round(rowsBox.height) + "px inside a " + round(host.height) + "px host — rows overflow the box");
      }
    }
  } else if (name === "tall" && baseUsable && bHost) {
    blocked.push(tag + " host height went " + round(bHost.height) + " → " + round(host.height) +
      ", under the " + HOST_GROW_MIN_PX + "px this drive is meant to produce — the viewport change never reached the terminal host, so no refit was proven");
  }

  // D. the ask matches the screen, over an OPEN socket.
  if (name !== "baseline") {
    if (p.socketReadyState !== WS_OPEN) {
      blocked.push(tag + " ttySocket readyState=" + p.socketReadyState + " (not OPEN) — no resize can leave the client, so the ack chain is unobservable");
    } else if (!p.lastSent) {
      fail.push(tag + " the socket is OPEN and the box was resized, yet lastSentTTYResize is null — nothing was ever asked of the server");
    } else if (p.lastSent.cols !== p.cols || p.lastSent.rows !== p.rows) {
      fail.push(tag + " local screen is " + p.cols + "x" + p.rows + " but the newest size on the wire is " +
        p.lastSent.cols + "x" + p.lastSent.rows + " — a revert repainted the screen away from what was asked");
    } else {
      info.push(tag + " lastSentTTYResize " + p.lastSent.cols + "x" + p.lastSent.rows + " == local");
    }
  }

  // E. context only, never a verdict: the ack is the server's answer.
  if (p.applied && (p.applied.rows !== p.rows || p.applied.cols !== p.cols)) {
    info.push(tag + " server ack still " + p.applied.cols + "x" + p.applied.rows + " while local is " +
      p.cols + "x" + p.rows + " — recorded as context; a fit waits, it does not revert (p9 rule)");
  } else if (!p.applied) {
    info.push(tag + " no ack yet (appliedTTYSize null) — context only");
  }
  info.push(tag + " socket=" + p.socketReadyState + " ttyState=" + p.ttyState +
    " host h=" + round(host.height) + " w=" + round(host.width) + " local=" + p.cols + "x" + p.rows +
    " xtermRows h=" + (phaseRect(p, "xtermRows") ? round(phaseRect(p, "xtermRows").height) : "-") +
    " applied=" + (p.applied ? p.applied.cols + "x" + p.applied.rows : "-") +
    " lastSent=" + (p.lastSent ? p.lastSent.cols + "x" + p.lastSent.rows : "-"));
}

function judge(data) {
  const fail = [];
  const info = [];
  const blocked = [];
  if (!data || !Array.isArray(data.phases) || !data.phases.length) {
    return { verdict: "INCONCLUSIVE", fail, info, blocked: ["no phase was measured"] };
  }
  const get = (name) => data.phases.find((p) => p.phase === name);
  const base = get("baseline");
  if (!base) return { verdict: "INCONCLUSIVE", fail, info, blocked: ["no baseline phase"] };
  if (data.error || base.error) blocked.push(data.error || base.error);
  // Every cross-phase rule compares against the baseline. A baseline that never
  // held a sized screen makes those comparisons meaningless — it is then the whole
  // run that is inconclusive, not the resize: an Infinity px-per-row or "rows are
  // 38 instead of the baseline 0" is arithmetic noise, not a verdict about the app.
  const bHost = phaseRect(base, "hostRect");
  const baseUsable = !!(base.hasSession && base.cols > 0 && base.rows > 0 && bHost && bHost.height > 0);
  // Only when a session IS present but unsized: a phase with no session has
  // already said so, and repeating it as a second reason muddies the report.
  if (!baseUsable && base.hasSession && !base.error) {
    blocked.push("[baseline] a session is active but holds no sized screen at " + base.innerWidth + "x" + base.innerHeight +
      " (local=" + base.cols + "x" + base.rows + " host h=" + (bHost ? round(bHost.height) : "-") +
      ") — nothing to compare the driven phases against");
  }

  const order = ["baseline", "tall", "restored", "settled"];
  for (const name of order) judgePhase(name, get(name), base, baseUsable, fail, info, blocked);

  const restored = get("restored");
  const settled = get("settled") || restored;
  const tall = get("tall");

  // A (restored half): the same viewport must give the same rows back.
  if (baseUsable && restored && restored.hasSession) {
    const rHost = phaseRect(restored, "hostRect");
    if (rHost && Math.abs(rHost.height - bHost.height) <= 2) {
      if (Math.abs(restored.rows - base.rows) > ROW_TOLERANCE) {
        fail.push("[restored] viewport is back at " + restored.innerWidth + "x" + restored.innerHeight +
          " and the host is back at " + round(rHost.height) + "px, but local rows are " + restored.rows +
          " instead of the baseline " + base.rows + (tall ? " (tall was " + tall.rows + ")" : "") +
          " — the terminal stayed at a geometry belonging to a viewport that no longer exists");
      } else if (Math.abs(restored.cols - base.cols) > ROW_TOLERANCE) {
        fail.push("[restored] local cols are " + restored.cols + " against the baseline " + base.cols + " at the same width");
      } else {
        info.push("[restored] rows/cols back to the baseline " + base.cols + "x" + base.rows);
      }
    } else {
      blocked.push("[restored] host height " + round(rHost ? rHost.height : 0) + "px did not return to the baseline " +
        round(bHost.height) + "px — the restore never landed, so " +
        "\"it comes back\" was not tested");
    }
  } else if (!restored) {
    blocked.push("[restored] phase was never measured");
  }

  // The socket gate, decided on the LAST reading: a session that was never live
  // cannot have exercised the send path at all.
  if (settled && settled.hasSession && settled.socketReadyState !== WS_OPEN) {
    blocked.push("the tty socket was not OPEN at the end (readyState=" + settled.socketReadyState + "); a fit on a dead socket proves the client's intent and nothing about the ack chain");
  }

  const verdict = fail.length ? "FAIL" : (blocked.length ? "INCONCLUSIVE" : "PASS");
  return { verdict, fail, info, blocked };
}

// ---------------------------------------------------------------------------
// --self-test: the judgement itself, on fixtures, no browser.
// ---------------------------------------------------------------------------
function selfTest() {
  // A coherent landscape reading: host 646px, 38 rows × 17px = 646, 103 cols.
  const phase = (name, over) => Object.assign({
    phase: name, innerWidth: 1194, innerHeight: 702, vvHeight: 702,
    hasSession: true, instanceId: "inst1", hostSource: "session.container",
    hostRect: { top: 40, bottom: 686, width: 934, height: 646 },
    xtermScreen: { top: 40, bottom: 686, width: 934, height: 646 },
    xtermRows: { top: 40, bottom: 686, width: 934, height: 646 },
    cols: 103, rows: 38,
    applied: { cols: 103, rows: 38 }, lastSent: { cols: 103, rows: 38 }, pending: null,
    socketReadyState: WS_OPEN, ttyState: "LIVE", fitted: true,
  }, over || {});
  // The portrait reading: host 1006px, 58 rows × 17px = 986, 72 cols.
  const tall = (over) => phase("tall", Object.assign({
    innerWidth: 834, innerHeight: 1062, vvHeight: 1062,
    hostRect: { top: 40, bottom: 1046, width: 634, height: 1006 },
    xtermScreen: { top: 40, bottom: 1046, width: 634, height: 1006 },
    xtermRows: { top: 40, bottom: 1026, width: 634, height: 986 },
    cols: 72, rows: 58, lastSent: { cols: 72, rows: 58 },
  }, over || {}));
  const healthy = {
    phases: [
      phase("baseline"),
      tall(),
      phase("restored"),
      phase("settled"),
    ],
  };
  const withPhases = (list) => ({ phases: list });

  const cases = [
    ["the healthy p9 chain passes", healthy, "PASS"],
    // The exact shape the p9 fix guarantees: the server still owes the old ack
    // while the local screen already holds the fitted size. Judging by `applied`
    // here would be the p8 bug wearing a different hat.
    ["a stale ack (38) under a local 58 passes", withPhases([
      phase("baseline"),
      tall({ applied: { cols: 103, rows: 38 } }),
      phase("restored"),
      phase("settled", { applied: { cols: 103, rows: 38 } }),
    ]), "PASS"],
    ["no ack at all still passes", withPhases([
      phase("baseline", { applied: null }),
      tall({ applied: null }),
      phase("restored", { applied: null }),
      phase("settled", { applied: null }),
    ]), "PASS"],
    // The reported defect: the host came back tall and the screen stayed short.
    ["local rows stuck at 38 while the host grew fails", withPhases([
      phase("baseline"),
      tall({ rows: 38, lastSent: { cols: 72, rows: 38 }, xtermRows: { top: 40, bottom: 686, width: 634, height: 646 } }),
      phase("restored"),
      phase("settled"),
    ]), "FAIL"],    ["a stale .xterm-rows height (h646 under h1002) fails", withPhases([
      phase("baseline"),
      tall({ xtermRows: { top: 40, bottom: 686, width: 634, height: 646 } }),
      phase("restored"),
      phase("settled"),
    ]), "FAIL"],
    ["rows that never come back after the restore fail", withPhases([
      phase("baseline"),
      tall(),
      phase("restored", { rows: 58, lastSent: { cols: 103, rows: 58 }, xtermRows: { top: 40, bottom: 1026, width: 934, height: 986 } }),
      phase("settled", { rows: 58, lastSent: { cols: 103, rows: 58 } }),
    ]), "FAIL"],
    ["a screen that drifted off the wire size fails", withPhases([
      phase("baseline"),
      tall({ lastSent: { cols: 103, rows: 38 } }),
      phase("restored"),
      phase("settled"),
    ]), "FAIL"],
    ["an OPEN socket that never asked fails", withPhases([
      phase("baseline"),
      tall({ lastSent: null }),
      phase("restored"),
      phase("settled", { lastSent: null }),
    ]), "FAIL"],
    ["a width that did not refit its cols fails", withPhases([
      phase("baseline"),
      tall({ cols: 103, lastSent: { cols: 103, rows: 58 } }),
      phase("restored"),
      phase("settled"),
    ]), "FAIL"],
    // INCONCLUSIVE: nothing was measured, so nothing may be claimed.
    ["no OPEN socket is inconclusive", withPhases([
      phase("baseline", { socketReadyState: 0 }),
      tall({ socketReadyState: 0, lastSent: null }),
      phase("restored", { socketReadyState: 3, lastSent: null }),
      phase("settled", { socketReadyState: 3, lastSent: null }),
    ]), "INCONCLUSIVE"],
    ["a socket that died mid-run is inconclusive", withPhases([
      phase("baseline"),
      tall({ socketReadyState: 3, lastSent: null }),
      phase("restored", { socketReadyState: 3, lastSent: null }),
      phase("settled", { socketReadyState: 3, lastSent: null }),
    ]), "INCONCLUSIVE"],
    ["no active session is inconclusive", withPhases([
      phase("baseline"),
      tall({ hasSession: false, reason: "no active terminal session", hostRect: null, xtermRows: null, cols: 0, rows: 0, lastSent: null, socketReadyState: null }),
      phase("restored"),
      phase("settled"),
    ]), "INCONCLUSIVE"],
    ["an unsized terminal is inconclusive", withPhases([
      phase("baseline", { cols: 0, rows: 0 }),
      tall(),
      phase("restored"),
      phase("settled"),
    ]), "INCONCLUSIVE"],
    ["a host that never grew is inconclusive", withPhases([
      phase("baseline"),
      tall({ hostRect: { top: 40, bottom: 690, width: 934, height: 650 }, xtermRows: { top: 40, bottom: 686, width: 934, height: 646 }, cols: 103, rows: 38, lastSent: { cols: 103, rows: 38 } }),
      phase("restored"),
      phase("settled"),
    ]), "INCONCLUSIVE"],
    ["a restore that did not land is inconclusive", withPhases([
      phase("baseline"),
      tall(),
      phase("restored", { hostRect: { top: 40, bottom: 1046, width: 634, height: 1006 }, xtermRows: { top: 40, bottom: 1026, width: 934, height: 986 }, rows: 58, cols: 72, lastSent: { cols: 72, rows: 58 } }),
      phase("settled", { rows: 58, cols: 72, lastSent: { cols: 72, rows: 58 } }),
    ]), "INCONCLUSIVE"],
    ["a canvas renderer is inconclusive", withPhases([
      phase("baseline", { xtermRows: null }),
      tall({ xtermRows: null }),
      phase("restored", { xtermRows: null }),
      phase("settled", { xtermRows: null }),
    ]), "INCONCLUSIVE"],
    ["a login gate is inconclusive", { phases: [phase("baseline", { hasSession: false, error: "getActiveTerminalSession is not a function — the shell script never booted" })] }, "INCONCLUSIVE"],
    ["no phases at all is inconclusive", { phases: [] }, "INCONCLUSIVE"],
  ];

  let bad = 0;
  for (const [name, fixture, want] of cases) {
    const r = judge(fixture);
    const got = r.verdict;
    const ok = got === want;
    if (!ok) bad++;
    console.log((ok ? "ok   " : "BAD  ") + name.padEnd(50) + " want=" + want.padEnd(12) + " got=" + got);
    if (!ok) {
      for (const f of r.fail) console.log("        X " + f);
      for (const b of r.blocked) console.log("        ? " + b);
    }
  }
  const n = cases.length;
  console.log("\n" + (bad ? bad + "/" + n + " judgement(s) misjudged" : "all " + n + " judgements behaved as specified"));
  process.exit(bad ? 1 : 0);
}

// ---------------------------------------------------------------------------

async function main() {
  if (process.argv.includes("--self-test")) return selfTest();

  const URL = arg("url", process.env.MW_TEST_URL || "");
  if (!URL) { console.error("need --url (the served UI, token included if the server requires one)"); process.exit(2); }
  const CHROME = arg("chrome", process.env.CHROME_BIN || "/usr/bin/google-chrome");
  const LABEL = arg("label", "pty-refit");
  const REQUESTED_PORT = Number(arg("port", "0")) || 0;
  const SETTLE_MS = Number(arg("settle-ms", "300")) || 300;
  const SESSION_TIMEOUT_MS = Number(arg("session-timeout-ms", "15000")) || 15000;
  // The two viewports the p8 portrait capture moved between, as CDP metrics. The
  // first is the baseline, the second the taller box, then back again.
  const PHASES = [
    { phase: "baseline", width: Number(arg("base-width", "1194")), height: Number(arg("base-height", "702")) },
    { phase: "tall", width: Number(arg("tall-width", "834")), height: Number(arg("tall-height", "1062")) },
    { phase: "restored", width: Number(arg("base-width", "1194")), height: Number(arg("base-height", "702")) },
    { phase: "settled", width: null, height: null }, // read again, no new override
  ];

  // A private profile and an OS-issued port are load-bearing, not tidiness. With
  // the default profile, a headless Chrome already running on this host adopts the
  // new invocation: the process hands over its URL, exits, and the DevTools
  // endpoint this tool polls never comes into existence — the same silent stall a
  // borrowed port produces. Both were observed here before this line was written.
  // `detached` plus a process-group kill is the other half: a killed parent must
  // not leave a browser holding that port for the next run.
  const profile = mkdtempSync(join(tmpdir(), "pty-refit-check-"));
  const DEBUG_PORT = REQUESTED_PORT || await freePort();
  const chrome = spawn(CHROME, [
    "--headless=new", "--disable-gpu", "--no-first-run", "--no-default-browser-check",
    "--user-data-dir=" + profile,
    "--remote-debugging-port=" + DEBUG_PORT, "--window-size=" + PHASES[0].width + "," + PHASES[0].height,
    "about:blank",
  ], { stdio: "ignore", detached: true });

  let ws = null;
  let idSeq = 0;
  const pending = new Map();
  // Every command is bounded. An unanswered CDP call must end the run with a
  // verdict, not leave it waiting on a browser that has stopped answering.
  const send = (method, params = {}, sessionId, timeoutMs = 8000) => new Promise((resolve, reject) => {
    const id = ++idSeq;
    const timer = setTimeout(() => {
      if (pending.delete(id)) reject(new Error(method + " timed out after " + timeoutMs + "ms"));
    }, timeoutMs);
    pending.set(id, {
      resolve: (v) => { clearTimeout(timer); resolve(v); },
      reject: (e) => { clearTimeout(timer); reject(e); },
    });
    try {
      ws.send(JSON.stringify({ id, method, params, ...(sessionId ? { sessionId } : {}) }));
    } catch (e) {
      clearTimeout(timer); pending.delete(id); reject(e);
    }
  });

  try {
    let info = null;
    for (let i = 0; i < 40 && !info; i++) {
      try { info = await (await fetch(`http://127.0.0.1:${DEBUG_PORT}/json/version`)).json(); } catch (_) { await sleep(250); }
    }
    if (!info) throw new Error("chrome devtools endpoint never answered on port " + DEBUG_PORT + " (chrome=" + CHROME + ")");
    ws = new WebSocket(info.webSocketDebuggerUrl);
    await new Promise((resolve, reject) => {
      const t = setTimeout(() => reject(new Error("the devtools websocket did not open within 8s")), 8000);
      ws.addEventListener("open", () => { clearTimeout(t); resolve(); }, { once: true });
      ws.addEventListener("error", () => { clearTimeout(t); reject(new Error("the devtools websocket errored")); }, { once: true });
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
    await send("Runtime.enable", {}, sessionId);
    await send("Page.enable", {}, sessionId);
    // Coarse pointer + touch, so the page resolves the same iPad rules the field
    // session did. Guarded: an unsupported call changes nothing about the chain.
    await send("Emulation.setTouchEmulationEnabled", { enabled: true, maxTouchPoints: 5 }, sessionId).catch(() => {});
    await send("Emulation.setEmulatedMedia", { features: [{ name: "pointer", value: "coarse" }, { name: "hover", value: "none" }] }, sessionId).catch(() => {});

    const metrics = (w, h) => send("Emulation.setDeviceMetricsOverride", { width: w, height: h, deviceScaleFactor: 2, mobile: true }, sessionId);
    const settle = async () => {
      const how = await send("Runtime.evaluate", { expression: TWO_FRAMES, awaitPromise: true, returnByValue: true }, sessionId)
        .then((r) => (r.result && r.result.value) || "unknown")
        .catch(() => "evaluate-failed");
      await sleep(SETTLE_MS);
      return how;
    };
    const read = async (phaseName, viewport) => {
      const r = await send("Runtime.evaluate", { expression: PROBE, returnByValue: true }, sessionId);
      if (r.exceptionDetails) {
        return { phase: phaseName, hasSession: false, error: "page-side probe threw: " + JSON.stringify(r.exceptionDetails.text || r.exceptionDetails), viewport };
      }
      return Object.assign({ phase: phaseName, viewport }, r.result.value || { hasSession: false, error: "no value" });
    };

    const out = { phases: [] };
    await metrics(PHASES[0].width, PHASES[0].height);
    await send("Page.navigate", { url: URL }, sessionId);
    await sleep(2000);

    // Wait for a live sized terminal, then run the drive. The poll never decides
    // anything — it only avoids measuring a page that is still booting; if the
    // deadline passes the phases are read anyway and judge() answers INCONCLUSIVE.
    // Each attempt REPLACES the baseline entry, so the baseline the judgement reads
    // is the state the drive started from, not an earlier half-booted reading.
    let waited = 0;
    for (;;) {
      const p = await read("baseline", { width: PHASES[0].width, height: PHASES[0].height });
      p.waitForSessionMs = waited;
      const last = out.phases[out.phases.length - 1];
      if (last && last.phase === "baseline") out.phases.pop();
      out.phases.push(p);
      if (p.hasSession && p.cols > 0 && p.rows > 0 && p.socketReadyState === WS_OPEN) break;
      if (waited >= SESSION_TIMEOUT_MS) break;
      waited += 700;
      await sleep(700);
    }

    for (const ph of PHASES.slice(1)) {
      if (ph.width && ph.height) await metrics(ph.width, ph.height);
      const how = await settle();
      const p = await read(ph.phase, ph.width ? { width: ph.width, height: ph.height } : { unchanged: true });
      p.settledBy = how;
      out.phases.push(p);
    }
    await send("Emulation.clearDeviceMetricsOverride", {}, sessionId).catch(() => {});

    const result = judge(out);

    console.log("PTY resize/refit check — " + LABEL);
    console.log("page=" + redact(URL) + "   engine=" + (info.Browser || "unknown") +
      "   devtools port=" + DEBUG_PORT + (REQUESTED_PORT ? " (as given)" : " (free port, asked of the OS)") +
      "   settle=2rAF+" + SETTLE_MS + "ms");
    for (const p of out.phases) {
      const v = p.viewport || {};
      // A phase whose probe never reached a session has no size to print: "-" and
      // the reason, never "undefinedxundefined".
      const size = (typeof p.cols === "number" && typeof p.rows === "number") ? p.cols + "x" + p.rows : "-";
      console.log("  " + String(p.phase).padEnd(9) + (v.width ? v.width + "x" + v.height : "(no change) ") +
        " host h=" + (p.hostRect ? round(p.hostRect.height) : "-") +
        " local=" + size +
        " xtermRows h=" + (p.xtermRows ? round(p.xtermRows.height) : "-") +
        " applied=" + (p.applied ? p.applied.cols + "x" + p.applied.rows : "-") +
        " lastSent=" + (p.lastSent ? p.lastSent.cols + "x" + p.lastSent.rows : "-") +
        " socket=" + (typeof p.socketReadyState === "number" ? p.socketReadyState : "-") +
        (p.reason ? "  (" + p.reason + ")" : "") + (p.error ? "  (" + p.error + ")" : ""));
    }
    for (const line of result.info) console.log("  . " + line);
    for (const line of result.fail) console.log("  X " + line);
    for (const line of result.blocked) console.log("  ? " + line);
    console.log("\nVERDICT: " + result.verdict);
    console.log("caveat: headless Chrome via Emulation.setDeviceMetricsOverride — the fit/ack chain at two viewport sizes, not the iOS soft keyboard.");

    const outDir = arg("out", join(HERE, "pty-refit-check"));
    mkdirSync(outDir, { recursive: true });
    const outFile = join(outDir, "RESULTS-" + LABEL + ".json");
    writeFileSync(outFile, JSON.stringify({
      label: LABEL, checkedAt: new Date().toISOString(),
      page: redact(URL), engine: info.Browser || "",
      method: "CDP Emulation.setDeviceMetricsOverride " + PHASES.map((p) => (p.width ? p.width + "x" + p.height : "unchanged")).join(" -> ") +
        "; settle = two requestAnimationFrame + " + SETTLE_MS + "ms (> the 100ms queueTTYResize window)",
      notProven: "headless Chrome, not iPadOS Safari; a driven viewport size, not the iOS soft keyboard",
      settleMs: SETTLE_MS, hostGrowMinPx: HOST_GROW_MIN_PX, pxPerRowTolerance: PX_PER_ROW_TOLERANCE,
      phases: out.phases, result,
    }, null, 2));
    console.log("evidence: " + outFile);
    process.exit(result.verdict === "PASS" ? 0 : result.verdict === "FAIL" ? 1 : 2);
  } finally {
    try { if (ws) ws.close(); } catch (_) { /* ignore */ }
    // The group, not just the parent: headless Chrome leaves its renderer and GPU
    // children behind, and a stray child keeps this debug port out of reach of the
    // next run.
    try { process.kill(-chrome.pid, "SIGTERM"); } catch (_) { try { chrome.kill("SIGTERM"); } catch (_) { /* gone */ } }
    try { rmSync(profile, { recursive: true, force: true }); } catch (_) { /* ignore */ }
  }
}

main().catch((err) => { console.error("check aborted (INCONCLUSIVE): " + (err && err.message ? err.message : err)); process.exit(2); });
