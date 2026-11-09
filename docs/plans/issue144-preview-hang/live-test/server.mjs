#!/usr/bin/env node
// Issue #144 live Tailscale test pages.
//
// Serves the real preview.html + vendor assets with three in-memory fixtures so
// an iPad/Safari on the same Tailnet can exercise the adaptive renderer without
// a daemon or a real worktree.
//
//   node server.mjs
//   PORT=41777 HOST=0.0.0.0 node server.mjs
//
// Pages:
//   /case/small   ~50 KiB Markdown / 732 list lines
//   /case/medium  ~100 KiB Markdown / 1,463 list lines
//   /case/large   ~900 KiB Markdown / 13,166 list lines
//   /case/largest  largest Markdown in ~/SoftwareWorkspace or ~/GithubRepo

import http from 'node:http';
import { readFileSync } from 'node:fs';
import { dirname, join, resolve } from 'node:path';
import { fileURLToPath } from 'node:url';

const HERE = dirname(fileURLToPath(import.meta.url));
const REPO = resolve(HERE, '../../../..');
const PORT = Number(process.env.PORT || 41777);
const HOST = process.env.HOST || '0.0.0.0';

const preview = readFileSync(join(REPO, 'internal/ui/static/preview.html'));
const previewText = preview.toString('utf8');
// Test-only performance instrumentation. It must run after marked.js is loaded
// but before the main preview IIFE starts, so it can wrap fetch/text decode,
// the main-thread fallback parser, and the Markdown Worker. The 10 ms interval
// is a main-thread heartbeat: any long main-thread task delays the next tick,
// and the gap is reported in the fixed panel.
const perfScript = `
<script>
(function() {
  var startedAt = performance.now();
  var perf = window.__issue144Perf = {
    startedAt: startedAt,
    fetchMs: 0,
    textMs: 0,
    textBytes: 0,
    markedMs: 0,
    markedCalls: 0,
    workerMs: 0,
    workerStartAt: 0,
    workerInputBytes: 0,
    workerResultBytes: 0,
    workerError: '',
    hiddenMs: 0,
    visibilityChanges: 0,
    domReadyMs: 0,
    layoutMs: 0,
    firstFrameMs: 0,
    longestGapMs: 0,
    gapsOver50Ms: 0,
    state: '',
    markdownNodes: 0,
    done: false
  };

  var lastTick = startedAt;
  var hiddenAt = document.hidden ? startedAt : 0;
  var heartbeat = setInterval(function() {
    var now = performance.now();
    // Safari throttles timers for hidden tabs. Do not count that pause as a
    // main-thread stall; reset the visible heartbeat window instead.
    if (document.hidden) {
      lastTick = now;
      return;
    }
    var gap = now - lastTick;
    lastTick = now;
    if (gap > perf.longestGapMs) perf.longestGapMs = gap;
    if (gap > 50) perf.gapsOver50Ms++;
  }, 10);
  document.addEventListener('visibilitychange', function() {
    perf.visibilityChanges++;
    var now = performance.now();
    if (document.hidden) {
      if (!hiddenAt) hiddenAt = now;
      lastTick = now;
    } else if (hiddenAt) {
      perf.hiddenMs += now - hiddenAt;
      hiddenAt = 0;
      lastTick = now;
    }
  });

  var origFetch = window.fetch;
  window.fetch = function() {
    var t = performance.now();
    var promise = origFetch.apply(this, arguments);
    promise.then(function() {
      perf.fetchMs = performance.now() - t;
    }, function() {});
    return promise;
  };

  var origText = Response.prototype.text;
  Response.prototype.text = async function() {
    var t = performance.now();
    var text = await origText.call(this);
    perf.textMs += performance.now() - t;
    perf.textBytes += text.length;
    return text;
  };

  // The shipped page no longer evaluates marked on the main thread. Keep the
  // optional instrumentation for compatibility with older served builds.
  if (typeof marked !== 'undefined' && typeof marked.parse === 'function') {
    var origParse = marked.parse.bind(marked);
    marked.parse = function() {
      var t = performance.now();
      try {
        return origParse.apply(null, arguments);
      } finally {
        perf.markedMs = performance.now() - t;
        perf.markedCalls++;
      }
    };
  }

  // The shipped page now parses Markdown in a Worker. Wrap the Worker
  // constructor so the test panel can report the actual worker round-trip
  // without touching production code.
  var NativeWorker = window.Worker;
  if (typeof NativeWorker === 'function') {
    window.Worker = function(url) {
      var worker = new NativeWorker(url);
      var nativePost = worker.postMessage.bind(worker);
      worker.postMessage = function(message) {
        if (message && message.type === 'parse') {
          perf.workerStartAt = performance.now();
          perf.workerInputBytes = String(message.text || '').length;
          perf.workerMs = 0;
          perf.workerResultBytes = 0;
          perf.workerError = '';
        }
        return nativePost(message);
      };
      worker.addEventListener('message', function(event) {
        var data = event.data || {};
        if (data.type === 'result') {
          perf.workerMs = performance.now() - perf.workerStartAt;
          perf.workerResultBytes = String(data.html || '').length;
        } else if (data.type === 'error') {
          perf.workerError = data.message || 'worker error';
        }
      });
      worker.addEventListener('error', function(event) {
        perf.workerError = event.message || 'worker error';
      });
      return worker;
    };
  }

  var panel = document.createElement('div');
  panel.id = 'issue144-perf-panel';
  panel.style.cssText = 'position:fixed;right:12px;bottom:12px;z-index:2147483647;' +
    'max-width:min(360px,calc(100vw - 24px));box-sizing:border-box;padding:10px 12px;' +
    'border-radius:8px;background:rgba(0,0,0,.86);color:#fff;' +
    'font:11px/1.45 ui-monospace,SFMono-Regular,Menlo,monospace;' +
    'white-space:pre-wrap;box-shadow:0 4px 20px rgba(0,0,0,.35);' +
    'pointer-events:auto;cursor:text;user-select:text;-webkit-user-select:text;' +
    '-webkit-touch-callout:default;';
  panel.textContent = 'Issue #144 perf (test only)\\nwaiting for Markdown...';
  document.body.appendChild(panel);

  function fmt(n) { return (Math.round(n * 10) / 10).toFixed(1); }

  function renderPanel() {
    panel.textContent = [
      'Issue #144 perf (test only)',
      'state: ' + (perf.state || '-'),
      'fetch: ' + fmt(perf.fetchMs) + ' ms',
      'text decode: ' + fmt(perf.textMs) + ' ms',
      'marked.parse (main): ' + fmt(perf.markedMs) + ' ms (' + perf.markedCalls + ' call)',
      'worker round-trip (wall): ' + fmt(perf.workerMs) + ' ms',
      'hidden time: ' + fmt(perf.hiddenMs) + ' ms (visibility changes: ' + perf.visibilityChanges + ')',
      'worker input/result: ' + perf.workerInputBytes + ' / ' + perf.workerResultBytes + ' chars',
      'worker error: ' + (perf.workerError || '-'),
      'DOM ready: ' + fmt(perf.domReadyMs) + ' ms',
      'post-worker commit: ' + fmt(Math.max(0, perf.domReadyMs - perf.workerMs)) + ' ms',
      'forced layout: ' + fmt(perf.layoutMs) + ' ms',
      'DOM -> first frame: ' + fmt(Math.max(0, perf.firstFrameMs - perf.domReadyMs)) + ' ms',
      'to first frame: ' + fmt(perf.firstFrameMs) + ' ms',
      'visible elapsed approx: ' + fmt(Math.max(0, perf.firstFrameMs - perf.hiddenMs)) + ' ms',
      'longest visible gap (noisy across visibility changes): ' + fmt(perf.longestGapMs) + ' ms',
      'gaps > 50 ms: ' + perf.gapsOver50Ms,
      'markdown nodes: ' + perf.markdownNodes
    ].join('\\n');
  }

  function markComplete() {
    if (perf.done) return;
    var content = document.getElementById('content');
    if (!content) return;
    perf.state = content.getAttribute('data-render-state') || '';
    var body = content.querySelector('.markdown-body');
    if (!body) {
      if (perf.state === 'limit' || perf.state === 'error') {
        perf.done = true;
        perf.domReadyMs = performance.now() - startedAt;
        clearInterval(heartbeat);
        renderPanel();
      }
      return;
    }
    perf.done = true;
    perf.domReadyMs = performance.now() - startedAt;
    perf.markdownNodes = body.getElementsByTagName('*').length;
    var lt = performance.now();
    // Force the same layout the first visible frame needs.
    void body.scrollHeight;
    perf.layoutMs = performance.now() - lt;
    requestAnimationFrame(function() {
      setTimeout(function() {
        perf.firstFrameMs = performance.now() - startedAt;
        clearInterval(heartbeat);
        renderPanel();
        console.log('[issue144-perf] ' + JSON.stringify(perf));
      }, 0);
    });
  }

  var observer = new MutationObserver(function() {
    var content = document.getElementById('content');
    if (!content) return;
    var state = content.getAttribute('data-render-state') || '';
    if (state === 'complete' || state === 'excerpt' || state === 'limit' || state === 'error' || state === 'truncated') {
      markComplete();
    }
  });
  var content = document.getElementById('content');
  if (content) {
    observer.observe(content, { childList: true, subtree: true, attributes: true, attributeFilter: ['data-render-state'] });
  }
})();
</script>
`;
const perfMarker = '\n<script>\n(function() {';
function injectPerf(html) {
  const markerAt = html.indexOf(perfMarker);
  if (markerAt < 0) throw new Error('failed to locate preview.html main script marker for perf injection');
  return html.slice(0, markerAt) + '\n' + perfScript + html.slice(markerAt);
}
const perfPreview = Buffer.from(injectPerf(previewText), 'utf8');
const vendor = new Map([
  ['/static/vendor/highlight/highlight.min.js', 'internal/ui/static/vendor/highlight/highlight.min.js'],
  ['/static/vendor/highlight/github.min.css', 'internal/ui/static/vendor/highlight/github.min.css'],
  ['/static/vendor/highlight/github-dark.min.css', 'internal/ui/static/vendor/highlight/github-dark.min.css'],
  ['/static/vendor/marked/marked.min.js', 'internal/ui/static/vendor/marked/marked.min.js'],
  ['/static/preview_markdown_worker.js', 'internal/ui/static/preview_markdown_worker.js'],
  ['/static/preview_source_highlight_worker.js', 'internal/ui/static/preview_source_highlight_worker.js'],
]);
for (const [urlPath, rel] of vendor) vendor.set(urlPath, readFileSync(join(REPO, rel)));

function markdownRows(targetBytes, rows) {
  // A simple list item per line keeps Markdown parsing and DOM creation
  // representative without any HTML execution risk.
  const prefix = '- item ';
  const per = Math.max(1, Math.floor((targetBytes - rows * (prefix.length + 1)) / rows));
  return Array.from({ length: rows }, (_, i) =>
    `${prefix}${String(i + 1).padStart(6, '0')} ${'x'.repeat(per)}`).join('\n');
}

const LARGEST_MD_PATH = process.env.LARGEST_MD_PATH || '';
let largestMarkdown = null;
let largestMetrics = null;
if (LARGEST_MD_PATH) {
  try {
    largestMarkdown = readFileSync(LARGEST_MD_PATH, 'utf8');
    const lines = largestMarkdown.split('\n');
    if (lines.length > 0 && lines[lines.length - 1] === '') lines.pop();
    let maxLine = 0;
    for (const line of lines) if (line.length > maxLine) maxLine = line.length;
    largestMetrics = { bytes: Buffer.byteLength(largestMarkdown), lines: lines.length, maxLine };
  } catch (err) {
    console.warn('largest Markdown fixture not available: ' + LARGEST_MD_PATH + ' (' + err.message + ')');
  }
} else {
  console.log('LARGEST_MD_PATH not set; /case/largest is omitted');
}

const CASES = [
  {
    slug: 'small',
    path: 'case-50k.md',
    file: 'case-50k.md',
    fileType: 'text/markdown',
    label: 'Small Markdown',
    target: '50 KiB, 732 list lines',
    expected: 'Markdown Formatted Preview should parse via marked.js and complete.',
    body: markdownRows(50 * 1024, 732),
  },
  {
    slug: 'medium',
    path: 'case-100k.md',
    file: 'case-100k.md',
    fileType: 'text/markdown',
    label: 'Medium Markdown',
    target: '100 KiB, 1,463 list lines',
    expected: 'Under the preflight limits, but marked.parse + DOM insertion still run synchronously; this page includes the perf panel.',
    body: markdownRows(100 * 1024, 1463),
    perf: true,
  },
  {
    slug: 'large',
    path: 'case-900k.md',
    file: 'case-900k.md',
    fileType: 'text/markdown',
    label: 'Large Markdown',
    target: '900 KiB, 13,166 list lines',
    expected: 'Under the 1 MiB hard cap and 20,000-line cap; Markdown parsing runs in the Worker, so the main thread should stay responsive while the status/progress panel is shown.',
    body: markdownRows(900 * 1024, 13166),
  },
];
if (largestMarkdown !== null) {
  CASES.push({
    slug: 'largest',
    path: 'magpie-gateway-routing.md',
    file: 'gateway-routing.md',
    fileType: 'text/markdown',
    label: 'Largest local Markdown',
    target: `${largestMetrics.bytes.toLocaleString()} bytes, ${largestMetrics.lines.toLocaleString()} lines, longest line ${largestMetrics.maxLine.toLocaleString()} chars`,
    expected: 'Under the production 1 MiB / 20,000-line / 32 KiB-line limits. Markdown parsing runs in the Worker; watch the main-thread gap and Worker round-trip in the perf panel.',
    body: largestMarkdown,
    perf: true,
  });
}
CASES.push({
  slug: 'unsupported',
  path: 'case-unsupported.png',
  file: 'case-unsupported.png',
  fileType: 'unsupported',
  label: 'Unsupported file',
  target: 'PNG-like bytes / no text renderer',
  expected: 'The content API returns 415 with no parseable body; the page must show Preview not supported and must not fall back to forced text parsing.',
  body: Buffer.from([0x89, 0x50, 0x4e, 0x47, 0x0d, 0x0a, 0x1a, 0x0a, 0x00, 0x01]),
  unsupported: true,
});

const BY_PATH = new Map(CASES.map(c => [c.path, c]));
const BY_SLUG = new Map(CASES.map(c => [c.slug, c]));

function previewURL(c) {
  const q = new URLSearchParams({
    id: 'issue144-live',
    path: c.path,
    file: c.file,
    repo: 'issue144-live',
    wt_name: 'live',
    branch: 'tailnet',
    tab: 'formatted',
  });
  const route = c.perf ? '/preview-perf' : '/preview';
  return route + '?' + q.toString();
}

function send(res, status, headers, body) {
  res.writeHead(status, { 'Cache-Control': 'no-store', ...headers });
  res.end(body);
}

const landing = `<!doctype html>
<html lang="en"><head><meta charset="utf-8"><meta name="viewport" content="width=device-width, initial-scale=1">
<title>Issue #144 Markdown Formatted Preview tests</title>
<style>
:root{color-scheme:light dark;font-family:-apple-system,BlinkMacSystemFont,"Segoe UI",Helvetica,Arial,sans-serif}
body{margin:0;padding:20px;background:#f6f8fa;color:#24292e;line-height:1.5}
@media (prefers-color-scheme:dark){body{background:#0d1117;color:#c9d1d9}.card{background:#161b22!important;border-color:#30363d!important}a{color:#58a6ff!important}}
main{max-width:760px;margin:0 auto}h1{font-size:22px;margin:0 0 6px}p{font-size:14px}.sub{color:#57606a;margin-top:0}
.card{display:block;margin:14px 0;padding:16px;background:#fff;border:1px solid #d0d7de;border-radius:8px;text-decoration:none;color:inherit}
.card strong{display:block;font-size:17px;margin-bottom:4px}.meta{font-size:13px;color:#57606a;margin-top:4px}
.note{padding:12px 14px;border-left:3px solid #0969da;background:#fff;border-radius:4px;font-size:13px}
code{font-family:ui-monospace,SFMono-Regular,Menlo,monospace;font-size:12px}
</style></head><body><main>
<h1>Issue #144 Markdown Formatted Preview tests</h1>
<p class="sub">Real <code>preview.html</code> + mock Markdown content API. Open these on an iPad/Safari on the same Tailnet.</p>
${CASES.map(c => `<a class="card" href="/case/${c.slug}"><strong>${c.label}: ${c.target}</strong><div>${c.expected}</div><div class="meta">${previewURL(c)}</div></a>`).join('\n')}
<div class="note">The preview starts on <strong>Formatted Preview</strong>. Medium and Large use the same production limits but include the perf panel. Markdown parsing runs in a Worker; the panel reports Worker round-trip, main-thread gaps, layout, and DOM size. The perf panel text is selectable/copyable. The Unsupported case verifies that unknown/binary types are blocked with no forced text parse.</div>
<p class="meta">Health: <a href="/healthz">/healthz</a></p>
</main></body></html>`;

const server = http.createServer((req, res) => {
  const u = new URL(req.url, 'http://localhost');
  if (u.pathname === '/healthz') {
    return send(res, 200, { 'Content-Type': 'application/json; charset=utf-8' },
      JSON.stringify({ ok: true, host: HOST, port: PORT, cases: CASES.map(c => ({ slug: c.slug, path: c.path, bytes: Buffer.byteLength(c.body), perf: !!c.perf, unsupported: !!c.unsupported })) }));
  }
  if (u.pathname === '/') {
    return send(res, 200, { 'Content-Type': 'text/html; charset=utf-8' }, landing);
  }
  if (u.pathname.startsWith('/case/')) {
    const c = BY_SLUG.get(u.pathname.slice('/case/'.length));
    if (!c) return send(res, 404, { 'Content-Type': 'text/plain; charset=utf-8' }, 'unknown test case');
    res.writeHead(302, { Location: previewURL(c), 'Cache-Control': 'no-store' });
    return res.end();
  }
  if (u.pathname === '/preview') {
    return send(res, 200, { 'Content-Type': 'text/html; charset=utf-8' }, preview);
  }
  if (u.pathname === '/preview-perf') {
    return send(res, 200, { 'Content-Type': 'text/html; charset=utf-8' }, perfPreview);
  }
  if (vendor.has(u.pathname)) {
    const ct = u.pathname.endsWith('.css') ? 'text/css; charset=utf-8' : 'text/javascript; charset=utf-8';
    return send(res, 200, { 'Content-Type': ct }, vendor.get(u.pathname));
  }
  if (u.pathname === '/api/worktree/file/content') {
    const c = BY_PATH.get(u.searchParams.get('path'));
    if (!c) return send(res, 404, { 'Content-Type': 'text/plain; charset=utf-8' }, 'unknown test fixture');
    const body = Buffer.from(c.body);
    if (c.unsupported) {
      return send(res, 415, {
        'Content-Type': 'text/plain; charset=utf-8',
        'X-File-Type': 'unsupported',
        'X-File-Size': String(body.length),
        'X-File-FullPath': '/tailnet-test/' + c.path,
      }, 'preview not supported for this file type');
    }
    return send(res, 200, {
      'Content-Type': 'text/plain; charset=utf-8',
      'X-File-Type': c.fileType,
      'X-File-Size': String(body.length),
      'X-File-FullPath': '/tailnet-test/' + c.path,
    }, body);
  }
  if (u.pathname === '/api/worktree/file/diff') {
    return send(res, 200, { 'Content-Type': 'text/plain; charset=utf-8' }, '');
  }
  return send(res, 404, { 'Content-Type': 'text/plain; charset=utf-8' }, 'not found');
});

server.listen(PORT, HOST, () => {
  const actual = server.address();
  console.log(`issue144 live test server: http://${HOST}:${actual.port}`);
  for (const c of CASES) console.log(`  /case/${c.slug} -> ${previewURL(c)} (${Buffer.byteLength(c.body)} bytes)`);
});
