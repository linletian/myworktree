// Behavioural tests for the issue #137 connection-overlay split: the overlay
// that used to say "Backend server unreachable. Retrying..." for EVERY poll
// failure now has two states — the credential is gone (401 / 403, plus the
// sticky 429 the limiter answers while the poll keeps knocking) and the
// backend is genuinely unreachable (network error, 5xx) — and only the
// credential state gets an action: a token-free `/login?next=…` link plus a
// full stop of the pollers, because retrying a dead credential can only burn
// the server's per-IP auth limiter (authFailLimit = 20/min). It also outranks
// the pre-existing "websocket live (status refresh failed)" liveness split,
// which would otherwise tell the user the session is fine while it is dead.
//
// The logic under test lives in the web root, so this file lives in
// testdata/ instead of static/ on purpose: static/* is embedded and served to
// every browser, and test code does not belong in the web root. As in the
// other frontend suites, the functions are SLICED out of the shipped
// index.html and evaluated against stubbed browser state — there is no second
// copy of the logic, so a drift between the slice anchors and the served
// source fails loudly at slice time. The last case pins the source anchors of
// the "stop retrying" contract (the poll's guard, the two transport dial
// guards, the single classification site and the dsh scope poll's cross-file
// read), because those statements are what a future refactor is most likely
// to drop silently.
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

test("issue #137: 401/403 pick the credential state; network and 5xx stay 'unreachable'", () => {
  const auth = makeHarness();
  auth.h.fail(httpError(401));
  assert.equal(auth.h.authExpired(), true);
  assert.equal(auth.title.textContent, "Session expired");

  const forbidden = makeHarness();
  forbidden.h.fail(httpError(403));
  assert.equal(forbidden.h.authExpired(), true, "403 is a rejected credential, not a dead backend");

  const serverError = makeHarness();
  serverError.h.fail(httpError(500));
  assert.equal(serverError.h.authExpired(), false);
  assert.equal(serverError.title.textContent, "Connection Lost");

  const offline = makeHarness();
  offline.h.fail(new TypeError("Failed to fetch"));
  assert.equal(offline.h.authExpired(), false, "a failed fetch carries no status at all");
  assert.equal(offline.title.textContent, "Connection Lost");

  const firstThrottle = makeHarness();
  firstThrottle.h.fail(httpError(429));
  assert.equal(firstThrottle.h.authExpired(), false, "a first-seen 429 is not evidence of a dead credential");
});

test("issue #137: once the credential is known dead, the limiter's 429 must not rewrite the overlay", () => {
  const h = makeHarness();
  h.h.fail(httpError(401));
  assert.equal(h.title.textContent, "Session expired");

  h.h.fail(httpError(429));
  assert.equal(h.h.authExpired(), true, "sticky: the session is still the problem, not the reachability");
  assert.equal(h.title.textContent, "Session expired");
  assert.ok(
    !/unreachable|retrying/i.test(h.title.textContent + " " + h.sub.textContent),
    "the credential state must never claim the backend is unreachable, nor promise a retry that cannot work",
  );
});

test("issue #137: an auth failure outranks the 'websocket live (status refresh failed)' liveness split", () => {
  // A live TTY used to swallow EVERY failure into one status line; for a dead
  // credential that line is the misdirection the issue is about.
  const auth = makeHarness({ activeInstance: { id: "inst-1", status: "running" }, liveTTY: true });
  auth.h.fail(httpError(401));
  assert.equal(auth.h.authExpired(), true);
  assert.equal(auth.overlay.style.display, "flex");
  assert.deepEqual(auth.statuses, [], "the liveness line is not painted for an auth failure");

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

  assert.match(o.sub.textContent, /log in again/);
  assert.equal(o.action.style.display, "", "the action is visible");
  assert.equal(
    o.action.href,
    "/login?next=" + encodeURIComponent("/worktrees/wt-1?tab=term"),
    "next keeps the current page and drops the stale token",
  );
  assert.equal(o.overlay.style.display, "flex");
  assert.equal(o.h.state.authExpired, true, "the single page-global stop flag");

  assert.deepEqual(o.h.cleared, [11, 22, 33, 44, 55], "every poller is cleared — the 429 storm cannot start");
  assert.deepEqual(
    o.h.timers(),
    { pollTimer: null, divergeTimer: null, gitChangesTimer: null, monitorAlertsTimer: null, monitorTimer: null },
    "and the handles are nulled, so a later tick cannot resurrect one",
  );
  assert.equal(o.logs.length, 1, "the failure is still logged once, as before");
});

test("issue #137: the unreachable state keeps the old words, offers no action, and keeps polling", () => {
  const o = makeHarness();

  o.h.fail(httpError(500));

  assert.equal(o.sub.textContent, "Backend server unreachable. Retrying...");
  assert.equal(o.action.style.display, "none", "no exit to offer: this one really does self-heal");
  assert.equal(o.overlay.style.display, "flex");
  assert.equal(o.h.state.authExpired, false);
  assert.deepEqual(o.h.cleared, [], "the poll keeps running — recovery is a successful refresh");

  // The recovery path (refresh()'s `recovered` branch) hides the overlay and
  // retracts the stop flag, or the dsh scope poll would stay off for the rest
  // of the page's life.
  o.h.hide();
  assert.equal(o.overlay.style.display, "none");
  assert.equal(o.h.state.authExpired, false);
  assert.equal(o.action.style.display, "none");
});

// --- source anchors ---------------------------------------------------------

test("issue #137: source anchors — the stop-retrying contract is still in the shipped page", () => {
  const count = (src, needle) => src.split(needle).length - 1;

  assert.equal(
    count(script, "if (state.authExpired === true) return;"),
    3,
    "refresh(), connectTTY() and startSSE() each bail out on the dead credential (the #137 / #143 boundary)",
  );
  assert.equal(
    count(script, "showConnectionFailure(e);"),
    1,
    "the poll's catch delegates to the single classification site",
  );
  assert.equal(
    count(script, 'updateStatus("websocket live (status refresh failed)")'),
    1,
    "the liveness split lives in exactly one place — inside the classifier, behind the auth check",
  );
  assert.equal(
    count(dshSource, "if (window.state && window.state.authExpired === true)"),
    1,
    "the dsh scope poll (a bare fetch, not window.api) honours the same single stop flag",
  );
  assert.ok(indexSource.includes('id="conn-login"'), "the overlay markup carries the re-login action");
});
