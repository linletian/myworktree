// Behavioral tests for the issue #111 P2 Resource Monitor enhancement:
// the status-bar alert badge (worst-severity class + count text, Transport
// text untouched), the modal alerts strip (signal/threshold/current/advice
// verbatim from the server, HTML-escaped), the expandable subtree detail
// (top-N + "and M more" arithmetic + the walk-capped honesty note), the
// measured footnote that REPLACED the old "children not counted" disclaimer
// (its boundary sentence is the visibility contract), and the ~10s alert
// poll's start/stop lifecycle — plus its two refresh-truthfulness guards:
// a cold cache (sampled_at_ms=0) paints no badge, and the light poll stands
// down entirely while the monitor modal is open so the modal's 2s full poll
// owns the badge without flicker.
//
// Same arrangement as narrow_drawer.test.mjs: every function is SLICED from
// the shipped index.html and evaluated against stubbed browser state — no
// copies (§6 discipline), so a drift between the slice anchors and the
// served source fails loudly at slice time. Pure display helpers take all
// input as parameters; DOM-touching functions guard `document` behind
// `typeof`. Run:
//   node --test testdata/monitor_alerts.test.mjs
// (also wired into terminal_status_test.go for `go test`).
import test from "node:test";
import assert from "node:assert/strict";
import { readFileSync } from "node:fs";
import { fileURLToPath } from "node:url";
import { dirname, join } from "node:path";

const here = dirname(fileURLToPath(import.meta.url));
const staticDir = join(here, "..", "static");
const indexSource = readFileSync(join(staticDir, "index.html"), "utf8");

// --- source slicing helpers (same approach as narrow_drawer.test.mjs) ---

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

function sliceStatement(src, header) {
  const start = src.indexOf(header);
  assert.notEqual(start, -1, `source no longer contains ${JSON.stringify(header)}`);
  const end = src.indexOf(";", start);
  assert.notEqual(end, -1, `no statement terminator after ${JSON.stringify(header)}`);
  return src.slice(start, end + 1);
}

const script = inlineScript(indexSource);

// monitorFactory: the shipped functions, sliced whole. escapeHTML/formatBytes
// ride along because the render helpers call them (shared source of truth —
// the tests exercise THE escaping/formatting the page ships, not a copy).
// monitorAlertsTimer / monitorExpandedTrees mirror the module-level
// declarations in index.html; the getters expose them below.
const monitorFactory = new Function("deps", `
  const { api, console, document, setInterval, clearInterval } = deps;
  ${sliceStatement(script, "const MONITOR_ALERTS_POLL_MS")}
  let monitorAlertsTimer = null;
  const monitorExpandedTrees = new Set();
  ${[
    "function escapeHTML",
    // index.html declares formatBytes TWICE in this script (a legacy
    // 2-decimal variant defined earlier); the LATER declaration is what
    // the page runs (last function declaration wins), and its header is
    // the unique anchor — slicing the earlier one would test a function
    // the page never calls.
    "function formatBytes(bytes)",
    "function alertWorstSeverity",
    "function alertBadgeText",
    "function monitorAlertsHTML",
    "function subtreeNoteText",
    "function subtreeAgeText",
    "function monitorSubtreeRowsHTML",
    "function monitorFootnoteText",
    "function updateStatus",
    "function applyMonitorAlerts",
    "async function fetchMonitorAlerts",
    "function startMonitorAlertsPolling",
    "function stopMonitorAlertsPolling",
    "function toggleMonitorTree",
    "function handleMonitorTbodyClick",
  ].map(h => sliceBlock(script, h)).join("\n\n")}
  return {
    MONITOR_ALERTS_POLL_MS,
    alertWorstSeverity,
    alertBadgeText,
    monitorAlertsHTML,
    subtreeNoteText,
    subtreeAgeText,
    monitorSubtreeRowsHTML,
    monitorFootnoteText,
    updateStatus,
    applyMonitorAlerts,
    fetchMonitorAlerts,
    startMonitorAlertsPolling,
    stopMonitorAlertsPolling,
    toggleMonitorTree,
    handleMonitorTbodyClick,
    isPolling: () => monitorAlertsTimer !== null,
    expandedIds: () => Array.from(monitorExpandedTrees),
  };
`);

function makeBar({ withAlerts = true, withTransport = true } = {}) {
  const els = {};
  const mk = (id, classes = []) => {
    const cls = new Set(classes);
    return {
      id, textContent: "", hidden: false,
      classList: {
        add: (c) => cls.add(c),
        // Real DOM classList.remove is variadic — the shipped
        // applyMonitorAlerts clears BOTH severity classes in one call,
        // and a single-argument stub would leak the other class and fake
        // a defect the browser does not have.
        remove: (...cs) => cs.forEach(c => cls.delete(c)),
        contains: (c) => cls.has(c),
        toggle: (c, force) => {
          const on = force === undefined ? !cls.has(c) : !!force;
          if (on) cls.add(c); else cls.delete(c);
          return on;
        },
      },
      _classes: cls,
    };
  };
  if (withAlerts) els["status-bar-alerts"] = mk("status-bar-alerts");
  if (withTransport) els["status-bar-transport"] = mk("status-bar-transport");
  els["status-bar"] = mk("status-bar");
  return {
    els,
    getElementById: (id) => els[id] ?? null,
  };
}

function harness(doc) {
  const intervals = [];
  let nextHandle = 1;
  const apiCalls = [];
  const apiImpl = async (path) => { apiCalls.push(path); return harness._apiReply; };
  const api = (path, opts) => apiImpl(path, opts);
  const document = doc ?? makeBar();
  const m = monitorFactory({
    api,
    console: { error: () => {}, log: () => {} },
    document,
    setInterval: (fn, ms) => { intervals.push({ fn, ms, handle: nextHandle }); return nextHandle++; },
    clearInterval: (h) => { const i = intervals.findIndex(x => x.handle === h); if (i >= 0) intervals.splice(i, 1); },
  });
  return { m, intervals, apiCalls, document };
}

const A = (sev, id, extra = {}) => ({
  id, severity: sev, signal: "sig-" + id, threshold: ">= 30%", current: "47.4%",
  advice: "do the thing: docs/PLAYBOOK-memory-alerts.md#" + id, ...extra,
});

// --- badge: pure parts ---

test("alertBadgeText counts and pluralizes; empty when all clear", () => {
  const { m } = harness();
  assert.equal(m.alertBadgeText([]), "");
  assert.equal(m.alertBadgeText(null), "");
  assert.equal(m.alertBadgeText(undefined), "");
  assert.equal(m.alertBadgeText([A("amber", "psi-memory")]), "\u26A0 1 alert");
  assert.equal(m.alertBadgeText([A("amber", "a"), A("red", "b")]), "\u26A0 2 alerts");
});

test("alertWorstSeverity: red beats amber; silence is empty", () => {
  const { m } = harness();
  assert.equal(m.alertWorstSeverity([]), "");
  assert.equal(m.alertWorstSeverity(null), "");
  assert.equal(m.alertWorstSeverity([A("amber", "a")]), "amber");
  assert.equal(m.alertWorstSeverity([A("amber", "a"), A("red", "b")]), "red");
  assert.equal(m.alertWorstSeverity([A("red", "a"), A("amber", "b")]), "red");
});

// --- badge: DOM application preserves Transport + swaps the class ---

test("applyMonitorAlerts paints badge and bar class, transport span untouched", () => {
  const { m, document } = harness();
  const bar = document.els["status-bar"];
  const badge = document.els["status-bar-alerts"];
  const transport = document.els["status-bar-transport"];
  transport.textContent = "Transport: websocket";

  m.applyMonitorAlerts([A("amber", "psi-memory"), A("amber", "available-ram")]);
  assert.equal(badge.hidden, false);
  assert.equal(badge.textContent, "\u26A0 2 alerts");
  assert.equal(bar._classes.has("status-alert-amber"), true);
  assert.equal(bar._classes.has("status-alert-red"), false);
  assert.equal(transport.textContent, "Transport: websocket");

  m.applyMonitorAlerts([A("red", "psi-memory")]);
  assert.equal(bar._classes.has("status-alert-red"), true);
  assert.equal(bar._classes.has("status-alert-amber"), false, "severity swap, not stack");

  m.applyMonitorAlerts([]);
  assert.equal(badge.hidden, true);
  assert.equal(bar._classes.has("status-alert-red"), false);
  assert.equal(transport.textContent, "Transport: websocket", "clearing alerts never touches transport text");
});

test("updateStatus writes ONLY the transport span", () => {
  const { m, document } = harness();
  document.els["status-bar-alerts"].textContent = "\u26A0 3 alerts";
  m.updateStatus("connecting");
  assert.equal(document.els["status-bar-transport"].textContent, "Transport: connecting");
  assert.equal(document.els["status-bar-alerts"].textContent, "\u26A0 3 alerts", "badge survives a transport repaint");
});

test("DOM glue degrades without document / missing spans", () => {
  const { m } = harness();
  assert.doesNotThrow(() => m.applyMonitorAlerts([A("red", "x")]));
  // Bar exists but the alert spans do not (a stale cached page shape):
  // must not throw.
  const { m: m2 } = harness(makeBar({ withAlerts: false, withTransport: false }));
  assert.doesNotThrow(() => m2.applyMonitorAlerts([A("red", "x")]));
  assert.doesNotThrow(() => m2.updateStatus("x"));
});

// --- modal alerts strip ---

test("monitorAlertsHTML renders the display triple + advice, escaped", () => {
  const { m } = harness();
  assert.equal(m.monitorAlertsHTML([]), "");
  assert.equal(m.monitorAlertsHTML(null), "");
  const html = m.monitorAlertsHTML([A("red", "psi-memory"), A("amber", "available-ram")]);
  assert.match(html, /class="monitor-alert alert-red"/);
  assert.match(html, /class="monitor-alert alert-amber"/);
  // threshold/current are escaped verbatim — ">=" arrives as "&gt;=",
  // which is the point: nothing from the server is trusted unescaped.
  assert.match(html, /threshold &gt;= 30% \u00B7 now 47\.4%/);
  assert.match(html, /PLAYBOOK-memory-alerts\.md#psi-memory/);
  const evil = m.monitorAlertsHTML([{ ...A("amber", "x"), signal: "<img src=x onerror=alert(1)>" }]);
  assert.ok(!evil.includes("<img"), "signal must be escaped: " + evil);
  assert.match(evil, /&lt;img/);
});

// --- subtree note / detail / footnote ---

test("subtreeNoteText shows count + subtree RSS; zero children says nothing", () => {
  const { m } = harness();
  assert.equal(m.subtreeNoteText({ child_count: 0, subtree_rss_bytes: 0 }), "");
  assert.equal(m.subtreeNoteText({}), "");
  assert.equal(m.subtreeNoteText(null), "");
  assert.equal(m.subtreeNoteText({ child_count: 37, subtree_rss_bytes: 1234567 }), "tree 37 \u00B7 1.2 MB");
});

test("tree_unavailable renders unavailable, never measured zeros (review G3)", () => {
  const { m } = harness();
  // The note: an unreadable tree says "unavailable" — silence or "tree 0"
  // would render a procfs failure as a measured all-clear.
  assert.equal(m.subtreeNoteText({ tree_unavailable: true, child_count: 0, subtree_rss_bytes: 0 }), "tree unavailable");
  assert.equal(m.subtreeNoteText({ tree_unavailable: true }), "tree unavailable");
  // The expanded detail: NOT the "No live child processes" claim — that
  // sentence is a measurement, and nothing was measured.
  const html = m.monitorSubtreeRowsHTML({ tree_unavailable: true, child_count: 0 }, 1700000000000);
  assert.match(html, /unavailable/i);
  assert.ok(!html.includes("No live child processes"), "unreadable must not render as measured-empty: " + html);
  // Healthy paths keep their old truths — the marker only moves when the
  // server actually set it.
  assert.equal(m.subtreeNoteText({ child_count: 0, tree_unavailable: false }), "");
  assert.match(m.monitorSubtreeRowsHTML({ child_count: 0 }, 1700000000000), /No live child processes/);
});

test("subtreeAgeText: coarse buckets, garbage renders empty", () => {
  const { m } = harness();
  const now = 1700000000000;
  assert.equal(m.subtreeAgeText(now - 5000, now), "5s");
  assert.equal(m.subtreeAgeText(now - 120000, now), "2m");
  assert.equal(m.subtreeAgeText(now - 7200000, now), "2h");
  assert.equal(m.subtreeAgeText(now - 3 * 86400000, now), "3d");
  assert.equal(m.subtreeAgeText(0, now), "", "missing stamp renders nothing");
  assert.equal(m.subtreeAgeText(now + 1, now), "", "future stamp renders nothing, never a negative age");
});

test("monitorSubtreeRowsHTML: children rows + and-M-more + truncation note", () => {
  const { m } = harness();
  const now = 1700000000000;
  const inst = {
    child_count: 13,
    children: [
      { pid: 11, ppid: 1, name: "fake-serve.py", rss_bytes: 5000000, cpu_percent: 12.5, created_at_ms: now - 60000 },
      { pid: 12, ppid: 11, name: "python3", rss_bytes: 1000000, cpu_percent: 0, created_at_ms: now - 5000 },
    ],
    children_truncated: true,
  };
  const html = m.monitorSubtreeRowsHTML(inst, now);
  assert.match(html, /fake-serve\.py/);
  assert.match(html, /and 11 more/, "13 counted - 2 shown = 11 more");
  assert.match(html, /walk capped/, "truncated flag must be visible");
  assert.match(html, /1m/, "child age bucket rendered");

  const noKids = m.monitorSubtreeRowsHTML({ child_count: 0 }, now);
  assert.match(noKids, /No live child processes/);
  assert.ok(!noKids.includes("and "), "zero children: no 'and M more'");

  // more arithmetic: exactly top-N shown, none hidden.
  const exact = m.monitorSubtreeRowsHTML({ child_count: 2, children: inst.children }, now);
  assert.ok(!exact.includes("and "), "count == shown: no 'and M more'");
});

test("monitorFootnoteText: measured totals + boundary + playbook, never the old lie", () => {
  const { m } = harness();
  const text = m.monitorFootnoteText({
    instances: [
      { status: "running", child_count: 3, subtree_rss_bytes: 1024 * 1024 },
      { status: "running", child_count: 10, subtree_rss_bytes: 3 * 1024 * 1024 },
      { status: "stopped", child_count: 99, subtree_rss_bytes: 99 },
    ],
  });
  assert.match(text, /2 running instances/);
  assert.match(text, /13 child processes/);
  assert.match(text, /4\.0 MB subtree RSS/, "stopped instances contribute nothing");
  assert.match(text, /main PID \+ descendants/);
  assert.match(text, /sibling processes in the same terminal scope are not visible/);
  assert.match(text, /local truth/);
  assert.match(text, /Detection only.*PLAYBOOK-memory-alerts\.md/s);
  assert.ok(!text.includes("not counted"), "the pre-#111 disclaimer must be gone");
  assert.equal(m.monitorFootnoteText(null).includes("0 running instances"), true);
});

// --- poll lifecycle ---

test("alert poll: ~10s cadence, single timer, stop clears it", async () => {
  const { m, intervals, apiCalls } = harness();
  harness._apiReply = { alerts: [A("amber", "psi-memory")], system: {} };

  m.startMonitorAlertsPolling();
  assert.equal(m.MONITOR_ALERTS_POLL_MS, 10000, "cadence is the design's ~10s");
  assert.equal(intervals.length, 1, "exactly one interval");
  assert.equal(intervals[0].ms, 10000);
  assert.equal(m.isPolling(), true);

  // Restarting never stacks timers — but it DOES kick once more (the
  // restart's own immediate fetch), so the count grows by exactly one.
  const kicksBefore = apiCalls.length;
  m.startMonitorAlertsPolling();
  assert.equal(intervals.length, 1);

  // The immediate kick hit the lightweight endpoint.
  await Promise.resolve();
  assert.ok(apiCalls.length > kicksBefore, "kick fetches /api/monitor/alerts");
  assert.ok(apiCalls.every(p => p === "/api/monitor/alerts"));

  m.stopMonitorAlertsPolling();
  assert.equal(m.isPolling(), false);
  assert.equal(intervals.length, 0);
  m.stopMonitorAlertsPolling(); // idempotent
  assert.equal(m.isPolling(), false);
});

test("fetchMonitorAlerts paints the endpoint's alerts on the badge", async () => {
  const doc = makeBar();
  const h = harness(doc);
  harness._apiReply = { alerts: [A("red", "psi-memory"), A("amber", "available-ram")], sampled_at_ms: 1700000000000 };
  await h.m.fetchMonitorAlerts();
  assert.equal(doc.els["status-bar-alerts"].textContent, "\u26A0 2 alerts");
  assert.equal(doc.els["status-bar"]._classes.has("status-alert-red"), true);
});

test("cold cache (sampled_at_ms=0) paints no badge, never a stale all-clear (ARB-F6)", async () => {
  const doc = makeBar();
  const h = harness(doc);
  // The endpoint still returns an alerts array, but sampled_at_ms=0 means
  // "never sampled yet" — the UI must NOT dress up silence as a fresh
  // no-alerts all-clear (the documented sampled_at_ms contract).
  harness._apiReply = { alerts: [A("red", "psi-memory")], sampled_at_ms: 0 };
  await h.m.fetchMonitorAlerts();
  assert.equal(doc.els["status-bar-alerts"].textContent, "", "cold cache must not paint the badge");

  // A missing sampled_at_ms is likewise cold.
  harness._apiReply = { alerts: [A("red", "psi-memory")] };
  await h.m.fetchMonitorAlerts();
  assert.equal(doc.els["status-bar-alerts"].textContent, "", "absent sampled_at_ms is cold ⇒ no paint");
});

test("light poll does NOT repaint the badge while the monitor modal is open (ARB-F8)", async () => {
  const doc = makeBar();
  doc.els["modal-monitor"] = { open: true };
  const h = harness(doc);
  // Pre-paint the badge as the modal's 2s full poll would (a fuller set).
  doc.els["status-bar-alerts"].textContent = "\u26A0 4 alerts";
  harness._apiReply = { alerts: [A("amber", "available-ram")], sampled_at_ms: 1700000000000 };
  await h.m.fetchMonitorAlerts();
  // The modal owns the badge while open: the ~10s light poll must stand down
  // ENTIRELY — no request, no overwrite with the system-only cached set.
  assert.equal(h.apiCalls.length, 0, "light poll stands down (no fetch) while the modal owns the badge");
  assert.equal(doc.els["status-bar-alerts"].textContent, "\u26A0 4 alerts", "badge untouched by the light poll while the modal is open");
});

test("toggleMonitorTree flips the Set and the DOM row optimistically", () => {
  const doc = makeBar();
  doc.els["subtree-row-i1"] = { hidden: true };
  doc.els["subtree-toggle-i1"] = { classList: { toggle: function(c, f) { this._s ??= new Set(); if (f) this._s.add(c); else this._s.delete(c); }, _s: new Set() } };
  const { m } = harness(doc);

  m.toggleMonitorTree("i1");
  assert.deepEqual(m.expandedIds(), ["i1"]);
  assert.equal(doc.els["subtree-row-i1"].hidden, false);

  m.toggleMonitorTree("i1");
  assert.deepEqual(m.expandedIds(), []);
  assert.equal(doc.els["subtree-row-i1"].hidden, true);
});

test("subtree toggle arrives via data-tree-id delegation, never inline JS (review G6)", () => {
  const doc = makeBar();
  doc.els["subtree-row-i1"] = { hidden: true };
  doc.els["subtree-toggle-i1"] = { classList: { toggle: function(c, f) { this._s ??= new Set(); if (f) this._s.add(c); else this._s.delete(c); }, _s: new Set() } };
  const { m } = harness(doc);

  // An id containing a single quote — the exact shape escapeHTML cannot put
  // inside an inline onclick JS string. Through the data attribute it is
  // just data and the toggle works.
  const evil = "i'td";
  doc.els["subtree-row-" + evil] = { hidden: true };
  doc.els["subtree-toggle-" + evil] = { classList: { toggle: function() {} } };
  const btn = { dataset: { treeId: evil } };
  const evt = { target: { closest: (sel) => (sel === ".subtree-toggle" ? btn : null) } };
  m.handleMonitorTbodyClick(evt);
  assert.deepEqual(m.expandedIds(), [evil]);
  assert.equal(doc.els["subtree-row-" + evil].hidden, false);

  // A click outside any toggle is inert; a toggle button without the
  // data attribute is inert too (no undefined ids into the Set).
  m.handleMonitorTbodyClick({ target: { closest: () => null } });
  m.handleMonitorTbodyClick({ target: { closest: () => ({ dataset: {} }) } });
  assert.deepEqual(m.expandedIds(), [evil]);
  m.handleMonitorTbodyClick(null); // defensive: no event at all

  // Shipped-source anchors: the quoting bug's source must stay gone.
  assert.ok(!indexSource.includes('onclick="toggleMonitorTree'),
    "inline onclick with a JS-string id is the G6 quoting bug — keep the delegation");
  assert.match(indexSource, /data-tree-id="/);
  assert.match(indexSource, /addEventListener\("click", handleMonitorTbodyClick\)/);
});

// --- shipped-source anchors: the wiring the slices cannot see ---

test("shipped index.html keeps the #111 P2 wiring whole", () => {
  // The old disclaimer is GONE (replaced by the measured footnote).
  assert.ok(!indexSource.includes("Child processes spawned by instances are not counted"),
    "the pre-#111 'children not counted' disclaimer must not resurface");
  // Status bar: transport + badge spans (no dead status-alert-badge class
  // attr — styling lives on #status-bar-alerts, ARB-F11).
  assert.match(indexSource, /<div id="status-bar"><span id="status-bar-alerts" hidden><\/span><span id="status-bar-transport">Transport: idle<\/span><\/div>/);
  // Click wiring UNCHANGED (still opens the Resource Monitor).
  assert.match(indexSource, /document\.getElementById\("status-bar"\)\.addEventListener\("click", openModalMonitor\)/);
  // Modal has the alerts strip.
  assert.match(indexSource, /id="monitor-alerts"/);
  // The lightweight endpoint URL is the server's route.
  assert.match(indexSource, /api\/monitor\/alerts/);
  // Lifecycle: poll stops on hidden and unload, restarts on visible.
  assert.match(indexSource, /stopMonitorAlertsPolling\(\);[\s\S]{0,80}if \(_truncateSpan\)/);
  const vis = sliceBlock(script, "document.addEventListener('visibilitychange', () => {");
  assert.match(vis, /stopMonitorAlertsPolling\(\)/);
  assert.match(vis, /startMonitorAlertsPolling\(\)/);
  // Severity CSS classes exist for both tiers on both surfaces.
  assert.match(indexSource, /#status-bar\.status-alert-amber/);
  assert.match(indexSource, /#status-bar\.status-alert-red/);
  assert.match(indexSource, /\.monitor-alert\.alert-amber/);
  assert.match(indexSource, /\.monitor-alert\.alert-red/);
});
