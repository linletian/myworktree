// Behavioural tests for the issue #137 connection-overlay split: the overlay
// that used to say "Backend server unreachable. Retrying..." for EVERY poll
// failure now separates the two states that need different words and different
// exits.
//
//   * 401 -> "Session expired": the daemon's withAuth answer for a token
//     mismatch. Server is up, credential is not. Gets the token-free
//     /login?next=… action.
//   * 429 -> "Too many failed attempts": allowAuthAttempt's per-IP limiter
//     only counts FAILED auth attempts, so it is the same family — and
//     "retrying" is exactly the wrong promise there, since the retry is what
//     burned the window.
//   * everything else (network error, 5xx, 403) -> the pre-existing
//     "Connection Lost / Backend server unreachable. Retrying...".
//     403 deliberately stays here: every 403 this server emits is a
//     route-level refusal or an origin mismatch, and a re-login would be
//     refused identically — the login action would be a dead end.
//
// Both credential states stop every poller (the 2s poll, diverge, git changes,
// monitor alerts) because 20 failed attempts/IP/minute turns into 429 and then
// the page cannot authenticate even with the right token. The latch
// state.authExpired is one-way: only a fresh page load clears it.
//
// The logic under test lives in the web root, so this file lives in
// testdata/ instead of static/ on purpose: static/* is embedded and served to
// every browser, and test code does not belong in the web root. As in the
// other frontend suites, the functions are SLICED out of the shipped
// index.html and evaluated against stubbed browser state — there is no second
// copy of the logic, so a drift between the slice anchors and the served
// source fails loudly at slice time. The last case pins the source anchors of
// the "stop retrying" contract (the poll guard, the two transport dial guards,
// the monitor/poller restart guards and the two renderer scope polls), because
// those statements are what a future refactor is most likely to drop silently.
//
// Run: node --test testdata/connection_overlay_auth.test.mjs
// (also wired into connection_overlay_auth_test.go for `go test`).
import test from "node:test";
import assert from "node:assert/strict";
import { readFileSync } from "node:fs";
import { fileURLToPath } from "node:url";
import { dirname, join } from "node:path";

const here = dirname(fileURLToPath(import.meta.url));
const staticDir = join(here, "..", "static");
const indexSource = readFileSync(join(staticDir, "index.html"), "utf8");
const dshSource = readFileSync(join(staticDir, "kinds", "dsh_web.js"), "utf8");
const opencodeSource = readFileSync(join(staticDir, "kinds", "opencode_web.js"), "utf8");

// --- source slicing helpers (same approach as terminal_status.test.mjs) -----

function inlineScript(html) {
  const start = html.lastIndexOf("<script>");
  const end = html.indexOf("</script>", start);
  assert.ok(start > 0 && end > start, "could not locate index.html's inline <script> block");
  return html.slice(start + "<script>".length, end);
}

function sliceBlock(src, header) {
  const start = src.indexOf(header);
  assert.notEqual(start, -1, `source no longer contains ${JSON.stringify(header)}`);
  let depth = 0;
  for (let i = src.indexOf("{", start); i < src.length; i++) {
    if (src[i] === "{") depth++;
    else if (src[i] === "}") {
      depth--;
      if (depth === 0) return src.slice(start, i + 1);
    }
  }
  throw new Error(`unbalanced braces while slicing ${JSON.stringify(header)}`);
}

const script = inlineScript(indexSource);

// --- the shipped overlay logic, sliced whole -------------------------------

// The poller handles (pollTimer / divergeTimer / gitChangesTimer /
// monitorAlertsTimer / monitorTimer) are page globals declared further down
// the same script; the factory owns them here so stopAuthPolling's clearing is
// observable. `clearInterval` records the ids it was handed, and `console` /
// `updateStatus` are spies because "did we log / did we paint the liveness
// line" is part of the contract.
const overlayFactory = new Function("deps", `
  const {
    document, location, state, console,
    getActiveInstance, isInstanceLiveStatus, hasLiveTTYConnection, updateStatus,
  } = deps;
  let pollTimer = deps.timers.poll;
  let divergeTimer = deps.timers.diverge;
  let gitChangesTimer = deps.timers.gitChanges;
  let monitorAlertsTimer = deps.timers.alerts;
  let monitorTimer = deps.timers.monitor;
  const cleared = [];
  function clearInterval(id) { cleared.push(id); }
  ${sliceBlock(script, "function loginNextPath(")}
  ${sliceBlock(script, "function stopAuthPolling(")}
  ${sliceBlock(script, "function showAuthExpired(")}
  ${sliceBlock(script, "function showConnectionLost(")}
  ${sliceBlock(script, "function hideConnectionOverlay(")}
  ${sliceBlock(script, "function showConnectionFailure(")}
  return {
    nextPath: loginNextPath,
    fail: showConnectionFailure,
    showAuthExpired,
    showConnectionLost,
    hide: hideConnectionOverlay,
    authExpired: () => state.authExpired,
    timers: () => ({ pollTimer, divergeTimer, gitChangesTimer, monitorAlertsTimer, monitorTimer }),
    cleared,
    state,
  };
`);

const OVERLAY_TIMERS = { poll: 11, diverge: 22, gitChanges: 33, alerts: 44, monitor: 55 };

function makeHarness({ pathname = "/", search = "", activeInstance = null, liveTTY = false } = {}) {
  const title = { textContent: "" };
  const sub = { textContent: "" };
  const overlay = {
    style: { display: "none" },
    querySelector: (sel) => (sel === ".conn-title" ? title : sel === ".conn-sub" ? sub : null),
  };
  const action = { style: { display: "none" }, href: "/login" };
  const document = {
    getElementById: (id) => (id === "connection-overlay" ? overlay : id === "conn-login" ? action : null),
  };
  const state = {};
  const logs = [];
  const statuses = [];
  const h = overlayFactory({
    document,
    location: { pathname, search },
    state,
    console: { error: (e) => logs.push(e) },
    getActiveInstance: () => activeInstance,
    isInstanceLiveStatus: () => liveTTY,
    hasLiveTTYConnection: () => liveTTY,
    updateStatus: (s) => statuses.push(s),
    timers: OVERLAY_TIMERS,
  });
  return { h, overlay, title, sub, action, logs, statuses };
}

const httpError = (status) => Object.assign(new Error(`HTTP ${status}`), { status });

// --- classification ---------------------------------------------------------

test("issue #137: 401 selects 'Session expired', 429 gets its own throttled words", () => {
  const expired = makeHarness();
  expired.h.fail(httpError(401));
  assert.equal(expired.title.textContent, "Session expired");
  assert.match(expired.sub.textContent, /log in again/);
  assert.ok(
    !/unreachable|retrying/i.test(expired.title.textContent + " " + expired.sub.textContent),
    "the credential state must never claim the backend is unreachable, nor promise a retry that cannot work",
  );
  assert.equal(expired.h.authExpired(), true);

  const throttled = makeHarness();
  throttled.h.fail(httpError(429));
  assert.equal(throttled.title.textContent, "Too many failed attempts");
  assert.match(throttled.sub.textContent, /rate-limited after failed logins/);
  assert.match(throttled.sub.textContent, /log in again/, "the login exit is the fix, and POST /login is not throttled");
  assert.ok(!/retrying/i.test(throttled.sub.textContent + " " + throttled.title.textContent));
  assert.equal(throttled.h.authExpired(), true, "a throttled window is a credential state: stop knocking");
  assert.deepEqual(throttled.h.cleared, [11, 22, 33, 44, 55], "and every poller is stopped");
});

test("issue #137: 403 / 5xx / network failures keep the reachability copy", () => {
  for (const [label, err] of [
    ["403", httpError(403)],
    ["500", httpError(500)],
    ["network", new TypeError("Failed to fetch")],
  ]) {
    const h = makeHarness();
    h.h.fail(err);
    assert.equal(h.title.textContent, "Connection Lost", `${label} is not a credential failure`);
    assert.equal(h.sub.textContent, "Backend server unreachable. Retrying...");
    assert.equal(h.action.style.display, "none", `${label} gets no action — re-login would hit the same refusal`);
    assert.ok(!h.h.authExpired(), `${label} must not latch the stop flag`);
    assert.deepEqual(h.h.cleared, [], `${label} keeps polling: it may genuinely recover`);
  }
});

test("issue #137: a credential failure outranks the 'websocket live (status refresh failed)' liveness split", () => {
  // A live TTY used to swallow EVERY failure into one status line; for a dead
  // credential that line is the misdirection the issue is about.
  const auth = makeHarness({ activeInstance: { id: "inst-1", status: "running" }, liveTTY: true });
  auth.h.fail(httpError(401));
  assert.equal(auth.h.authExpired(), true);
  assert.equal(auth.overlay.style.display, "flex");
  assert.deepEqual(auth.statuses, [], "the liveness line is not painted for a credential failure");

  // The liveness behaviour itself is unchanged for the failures it describes.
  const throttled = makeHarness({ activeInstance: { id: "inst-1", status: "running" }, liveTTY: true });
  throttled.h.fail(httpError(500));
  assert.deepEqual(throttled.statuses, ["websocket live (status refresh failed)"]);
  assert.equal(throttled.overlay.style.display, "none", "a live transport keeps the overlay away, as before");
  assert.ok(!throttled.h.authExpired(), "the liveness path never claims the credential is dead");
});

// --- the re-login target ----------------------------------------------------

test("issue #137: the re-login target keeps the page but never the address-bar token", () => {
  const { h } = makeHarness();
  assert.equal(h.nextPath("/", "?token=stale"), "/", "the token is the one thing that must not come back");
  assert.equal(h.nextPath("/", "?token=stale&active=inst-1"), "/?active=inst-1", "the other search params survive");
  assert.equal(h.nextPath("/worktrees/wt-1", ""), "/worktrees/wt-1");
  assert.equal(h.nextPath("/worktrees/wt-1", "?a=1&b=2"), "/worktrees/wt-1?a=1&b=2");
  assert.equal(h.nextPath("/", ""), "/", "no query means no '?' suffix");
});

// --- the two overlay states -------------------------------------------------

test("issue #137: the credential state offers a token-free re-login link and stops every poller", () => {
  const o = makeHarness({ pathname: "/worktrees/wt-1", search: "?token=stale&tab=term" });

  o.h.fail(httpError(401));

  assert.equal(o.action.style.display, "", "the action is visible");
  assert.equal(
    o.action.href,
    "/login?next=" + encodeURIComponent("/worktrees/wt-1?tab=term"),
    "next keeps the current page and drops the stale token",
  );
  assert.equal(o.overlay.style.display, "flex");
  assert.deepEqual(o.h.cleared, [11, 22, 33, 44, 55], "every poller is cleared — the 429 storm cannot start");
  assert.deepEqual(
    o.h.timers(),
    { pollTimer: null, divergeTimer: null, gitChangesTimer: null, monitorAlertsTimer: null, monitorTimer: null },
    "and the handles are nulled, so a later tick cannot resurrect one",
  );
  assert.equal(o.logs.length, 1, "the failure is still logged once, as before");
});

test("issue #137: the latch is one-way — no overlay state may clear it, only a page load can", () => {
  const o = makeHarness();
  o.h.fail(httpError(401));
  assert.equal(o.h.authExpired(), true);

  // Neither the reachability overlay nor the recovery hide may retract the
  // stop flag: that would leave an overlay promising "Retrying..." while no
  // timer is actually retrying.
  o.h.showConnectionLost();
  assert.equal(o.h.authExpired(), true, "showConnectionLost must not downgrade the latch");
  o.h.hide();
  assert.equal(o.h.authExpired(), true, "hiding the overlay is a display change, not a credential one");

  // And a fresh page (fresh state object) starts unlatched.
  const fresh = makeHarness();
  assert.ok(!fresh.h.authExpired());
  fresh.h.fail(httpError(500));
  fresh.h.hide();
  assert.equal(fresh.overlay.style.display, "none");
  assert.equal(fresh.action.style.display, "none");
});

// --- source anchors ---------------------------------------------------------

test("issue #137: source anchors — the stop-retrying contract is still in the shipped page", () => {
  const count = (src, needle) => src.split(needle).length - 1;

  assert.equal(
    count(script, "if (state.authExpired === true) return;"),
    5,
    "refresh(), startMonitorAlertsPolling(), the visibilitychange restart, connectTTY() and startSSE() all bail out",
  );
  assert.equal(
    count(script, "showConnectionFailure(e);"),
    1,
    "the poll's catch delegates to the single classification site",
  );
  assert.equal(
    count(script, 'updateStatus("websocket live (status refresh failed)")'),
    1,
    "the liveness split lives in exactly one place — inside the classifier, behind the credential check",
  );
  assert.equal(
    count(dshSource, "if (window.state && window.state.authExpired === true)"),
    1,
    "dsh's scope poll (a bare fetch, not window.api) honours the same flag",
  );
  assert.equal(
    count(opencodeSource, "if (window.state && window.state.authExpired === true)"),
    1,
    "opencode's twin scope poll does too — 40 req/min against a 20/min limiter",
  );
  assert.match(
    indexSource,
    /@media \(pointer: coarse\) \{[\s\S]*?#connection-overlay \.conn-action \{[\s\S]*?min-height: 44px;/,
    "the overlay's only action keeps the R10 coarse-pointer floor",
  );
  assert.match(
    script,
    /if \(state\.authExpired !== true\) \{\s*\n\s*event\.preventDefault\(\);/,
    "the leave-site prompt is skipped on the dead-credential path — otherwise it stands between the user and the login form (verified in a real browser)",
  );
  assert.ok(indexSource.includes('id="conn-login"'), "the overlay markup carries the re-login action");
});
