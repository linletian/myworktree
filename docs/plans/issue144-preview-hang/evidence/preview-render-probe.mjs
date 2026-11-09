#!/usr/bin/env node
// Issue #144 sandbox render probe.
//
// Serves the REAL preview.html + vendor JS/CSS from this repo behind a mock
// content API, then drives headless Chrome over CDP (Node built-ins only).
// No production file or API is touched, and no daemon/worktree is created.
//
// Usage:
//   node preview-render-probe.mjs --suite=h1 --cpu=6
//   node preview-render-probe.mjs --suite=h2 --cpu=6
//   node preview-render-probe.mjs --suite=smoke --cpu=1
//
// Output: JSON Lines, one object per measured fixture.

import http from 'node:http';
import { spawn } from 'node:child_process';
import { mkdtempSync, readFileSync, rmSync, writeFileSync } from 'node:fs';
import { tmpdir } from 'node:os';
import { dirname, join, resolve } from 'node:path';
import { fileURLToPath } from 'node:url';
import { setTimeout as sleep } from 'node:timers/promises';

const HERE = dirname(fileURLToPath(import.meta.url));
const REPO = process.env.ISSUE144_REPO || resolve(HERE, '../../../..');
const PREVIEW_PATH = join(REPO, 'internal/ui/static/preview.html');
const VENDOR_PATHS = {
  '/static/vendor/highlight/highlight.min.js': 'internal/ui/static/vendor/highlight/highlight.min.js',
  '/static/vendor/highlight/github.min.css': 'internal/ui/static/vendor/highlight/github.min.css',
  '/static/vendor/highlight/github-dark.min.css': 'internal/ui/static/vendor/highlight/github-dark.min.css',
  '/static/vendor/marked/marked.min.js': 'internal/ui/static/vendor/marked/marked.min.js',
  '/static/preview_markdown_worker.js': 'internal/ui/static/preview_markdown_worker.js',
};

function arg(name, fallback) {
  const p = process.argv.find(a => a.startsWith(`--${name}=`));
  return p ? p.slice(name.length + 3) : fallback;
}
const suite = arg('suite', 'h1');
const cpuRate = Number(arg('cpu', '6'));
const timeoutMs = Number(arg('timeout', '120000'));
let base = process.env.ISSUE144_BASE_URL || '';

// ---------------------------------------------------------------------------
// Deterministic fixtures. All are generated in-memory; no temp repo files.
// ---------------------------------------------------------------------------
const fixtures = new Map();
function add(name, body, opts = {}) {
  fixtures.set(name, {
    body: Buffer.isBuffer(body) ? body : Buffer.from(body, 'utf8'),
    fileType: opts.fileType || 'text/x-code',
    status: opts.status || 200,
    error: opts.error || '',
  });
}
function longHtml(bytes) {
  const head = '<!doctype html><html><body><div>';
  const tail = '</div></body></html>';
  const fill = 'x'.repeat(Math.max(0, bytes - head.length - tail.length));
  return head + fill + tail;
}
function fixedRows(bytes, rows) {
  // Keep total input ~= `bytes` while varying only the row count.
  const per = Math.max(1, Math.floor((bytes - (rows - 1)) / rows));
  const lines = Array.from({ length: rows }, () => 'x'.repeat(per));
  return lines.join('\n');
}
function htmlRows(bytes, rows) {
  // Each row is <i>...</i>\n = 8 bytes of markup/newline.
  const per = Math.max(1, Math.floor((bytes - rows * 8) / rows));
  return Array.from({ length: rows }, () => `<i>${'x'.repeat(per)}</i>`).join('\n');
}

add('smoke.html', longHtml(24 * 1024));

if (suite === 'h1' || suite === 'smoke') {
  for (const kb of [50, 100, 250, 500, 900]) {
    add(`h1-long-${kb}k.html`, longHtml(kb * 1024));
    add(`h1-many-${kb}k.html`, htmlRows(kb * 1024, Math.ceil(kb * 1024 / 70)));
  }
  // Fixed ~100 KiB payload, varying only the number of rendered rows.
  for (const rows of [1, 10, 100, 1000, 5000, 10000]) {
    add(`h1-rows-${rows}.html`, fixedRows(100 * 1024, rows));
  }
}

if (suite === 'h2' || suite === 'smoke') {
  add('h2-minified.html', longHtml(100 * 1024));
  add('h2-inline-base64.html',
    '<!doctype html><html><body><img src="data:image/png;base64,' +
    'A'.repeat(100 * 1024) + '"></body></html>');
  // GBK bytes for 中文 repeated: intentionally invalid UTF-8.
  const gbk = Buffer.from('d6d0cec4'.repeat(2000), 'hex');
  add('h2-nonutf8.html', Buffer.concat([Buffer.from('<html><body>'), gbk, Buffer.from('</body></html>')]));
  add('h2-sass.sass', Array.from({ length: 300 }, (_, i) => `.class-${i}\n  color: red;`).join('\n'));
  add('h2-Dockerfile', Array.from({ length: 300 }, (_, i) => `RUN echo line-${i}`).join('\n'), { fileType: 'text/plain' });
  add('h2-foo.dockerfile', Array.from({ length: 300 }, (_, i) => `RUN echo line-${i}`).join('\n'));
  add('h2-binary-tail.html',
    'A'.repeat(9000) + '\u0000' + Array.from({ length: 100 }, (_, i) => `tail-${i}`).join('\n'));
  add('h2-script.html', '<!doctype html><html><body><script>window.__issue144_xss = 1</script></body></html>');
  add('h2-too-large.html', 'file too large', { fileType: 'text/plain', status: 413, error: 'file too large' });
  add('h2-binary.bin', 'binary file not supported', { fileType: 'text/plain', status: 415, error: 'binary file not supported' });
}

if (suite === 'interaction') {
  // Both limits are exceeded here: the preflight path should render no rows.
  add('interaction-limit.html', htmlRows(200 * 1024, 3000));
  // Under the preflight limits but slow enough under CPU throttling that the
  // user can hit Stop while the adaptive loop is still adding chunks.
  add('interaction-slow.html', htmlRows(100 * 1024, 1463));
}

const preview = readFileSync(PREVIEW_PATH);
const vendorCache = new Map();
for (const [urlPath, rel] of Object.entries(VENDOR_PATHS)) vendorCache.set(urlPath, readFileSync(join(REPO, rel)));

const server = http.createServer((req, res) => {
  const u = new URL(req.url, 'http://127.0.0.1');
  if (u.pathname === '/preview') {
    res.writeHead(200, { 'Content-Type': 'text/html; charset=utf-8', 'Cache-Control': 'no-store' });
    res.end(preview);
    return;
  }
  if (vendorCache.has(u.pathname)) {
    const ct = u.pathname.endsWith('.css') ? 'text/css; charset=utf-8' : 'text/javascript; charset=utf-8';
    res.writeHead(200, { 'Content-Type': ct, 'Cache-Control': 'no-store' });
    res.end(vendorCache.get(u.pathname));
    return;
  }
  if (u.pathname === '/api/worktree/file/content') {
    const name = u.searchParams.get('path');
    const f = fixtures.get(name);
    if (!f) {
      res.writeHead(404, { 'Content-Type': 'text/plain; charset=utf-8' });
      res.end('unknown sandbox fixture: ' + name);
      return;
    }
    res.writeHead(f.status, {
      'Content-Type': 'text/plain; charset=utf-8',
      'X-File-Type': f.fileType,
      'X-File-Size': String(f.body.length),
      'X-File-FullPath': '/sandbox/' + name,
      'Cache-Control': 'no-store',
    });
    res.end(f.error || f.body);
    return;
  }
  if (u.pathname === '/api/worktree/file/diff') {
    res.writeHead(200, { 'Content-Type': 'text/plain; charset=utf-8', 'Cache-Control': 'no-store' });
    res.end('');
    return;
  }
  res.writeHead(404, { 'Content-Type': 'text/plain; charset=utf-8' });
  res.end('not found');
});

// ---------------------------------------------------------------------------
// Browser / CDP driver.
// ---------------------------------------------------------------------------
const USER_DATA = mkdtempSync(join(tmpdir(), 'issue144-chrome-'));
let chrome = null;
let ws = null;
let send = null;

async function startBrowser() {
  chrome = spawn('google-chrome', [
    '--headless=new', '--no-sandbox', '--disable-gpu', '--disable-dev-shm-usage',
    '--no-first-run', '--no-default-browser-check', '--disable-extensions',
    '--remote-debugging-port=0',
    '--user-data-dir=' + USER_DATA,
    '--window-size=1024,768',
    'about:blank',
  ], { stdio: 'ignore' });

  const activePortFile = join(USER_DATA, 'DevToolsActivePort');
  let port = 0;
  for (let i = 0; i < 100; i++) {
    try {
      const lines = readFileSync(activePortFile, 'utf8').trim().split(/\r?\n/);
      port = Number(lines[0]);
      if (port > 0) break;
    } catch {}
    await sleep(100);
  }
  if (!port) throw new Error('Chrome DevToolsActivePort never appeared');

  let pageWs = '';
  for (let i = 0; i < 50; i++) {
    try {
      const list = await (await fetch(`http://127.0.0.1:${port}/json/list`)).json();
      const page = list.find(t => t.type === 'page');
      if (page) { pageWs = page.webSocketDebuggerUrl; break; }
    } catch {}
    await sleep(100);
  }
  if (!pageWs) throw new Error('Chrome page target never appeared');

  ws = new WebSocket(pageWs);
  await new Promise((res, rej) => { ws.onopen = res; ws.onerror = rej; });
  let id = 0;
  const pending = new Map();
  ws.onmessage = ev => {
    const m = JSON.parse(ev.data);
    if (!m.id || !pending.has(m.id)) return;
    const { res, rej, timer } = pending.get(m.id);
    clearTimeout(timer);
    pending.delete(m.id);
    if (m.error) rej(new Error(`${m.error.message || 'CDP error'}: ${JSON.stringify(m.error.data || '')}`));
    else res(m);
  };
  send = (method, params = {}, timeout = timeoutMs) => new Promise((res, rej) => {
    const mid = ++id;
    const timer = setTimeout(() => {
      pending.delete(mid);
      rej(new Error(`CDP timeout ${method} after ${timeout}ms`));
    }, timeout);
    pending.set(mid, { res, rej, timer });
    ws.send(JSON.stringify({ id: mid, method, params }));
  });

  await send('Page.enable');
  await send('Runtime.enable');
  await send('Performance.enable');
  await send('Emulation.setCPUThrottlingRate', { rate: cpuRate });
}

async function evalJs(expression, awaitPromise = false, timeout = timeoutMs) {
  const r = await send('Runtime.evaluate', {
    expression, returnByValue: true, awaitPromise, timeout,
  }, timeout + 5000);
  if (r.result?.exceptionDetails) {
    throw new Error('page evaluation failed: ' + JSON.stringify(r.result.exceptionDetails).slice(0, 1000));
  }
  return r.result?.result?.value;
}

async function navigate(url) {
  await send('Page.navigate', { url });
  for (let i = 0; i < 200; i++) {
    try {
      if (await evalJs('document.readyState', false, 5000) === 'complete') return;
    } catch {}
    await sleep(50);
  }
  throw new Error('page navigation timed out');
}

const INSTRUMENT = `(() => {
  const p = window.__issue144 = {
    errors: [], longTasks: [], rowSamples: [], done: false, started: false,
    replacementChars: 0, state: '', statusText: '',
  };
  try {
    new PerformanceObserver(list => {
      for (const e of list.getEntries()) p.longTasks.push({ start: e.startTime, duration: e.duration });
    }).observe({ entryTypes: ['longtask'] });
  } catch (e) { p.errors.push('longtask:' + e.message); }

  const origText = Response.prototype.text;
  Response.prototype.text = async function() {
    const t = performance.now();
    const text = await origText.call(this);
    p.textMs = performance.now() - t;
    p.textReadyAt = performance.now();
    p.textLength = text.length;
    p.replacementChars = [...text].filter(ch => ch.charCodeAt(0) === 0xFFFD).length;
    return text;
  };

  const origFetch = window.fetch.bind(window);
  window.fetch = async function(...args) {
    const t = performance.now();
    p.fetchUrl = String(args[0]);
    try {
      const res = await origFetch(...args);
      p.fetchMs = performance.now() - t;
      p.status = res.status;
      return res;
    } catch (e) {
      p.errors.push('fetch:' + e.message);
      throw e;
    }
  };

  const origHighlight = hljs.highlight.bind(hljs);
  hljs.highlight = function(...args) {
    const t = performance.now();
    p.highlightStartAt = t;
    try { return origHighlight(...args); }
    finally {
      p.highlightEndAt = performance.now();
      p.highlightMs = p.highlightEndAt - t;
    }
  };

  p.donePromise = new Promise(resolve => { p.resolveDone = resolve; });
  document.addEventListener('click', e => {
    if (e.target && e.target.id === 'tab-formatted') {
      p.clickAt = performance.now();
      p.started = true;
    }
  }, true);

  function snapshot() {
    if (!p.started || p.done) return;
    const content = document.getElementById('content');
    if (!content) return;
    const state = content.getAttribute('data-render-state') || '';
    const rows = content.querySelectorAll('.code-line').length;
    const status = document.querySelector('.preview-render-status .grow');
    const statusText = status ? status.textContent : '';
    if (rows !== p.lastRows) {
      p.lastRows = rows;
      if (rows > 0 && p.firstRowAt == null) p.firstRowAt = performance.now();
      p.rowSamples.push({ t: performance.now(), rows, state, status: statusText });
    }
    if (state !== p.state) {
      p.state = state;
      p.stateAt = performance.now();
    }
    const terminal = state === 'complete' || state === 'excerpt' ||
      state === 'truncated' || state === 'stopped' || state === 'limit' ||
      state === 'error' || state === 'empty';
    if (!terminal) return;

    p.done = true;
    p.rowCount = rows;
    p.statusText = statusText || (document.querySelector('.preview-limit') || {}).textContent || '';
    const layoutStart = performance.now();
    p.scrollWidth = content.scrollWidth;
    p.scrollHeight = content.scrollHeight;
    p.layoutMs = performance.now() - layoutStart;
    p.resolveDone(p);
  }

  p.observer = new MutationObserver(snapshot);
  const content = document.getElementById('content');
  if (content) {
    p.observer.observe(content, {
      childList: true, subtree: true, characterData: true,
      attributes: true, attributeFilter: ['data-render-state'],
    });
  }
  return 'instrumented';
})()`;

async function waitForPageDone() {
  return evalJs(`(async () => {
    const p = window.__issue144;
    await p.donePromise;
    return {
      done: p.done,
      state: p.state,
      status: p.status,
      fetchMs: p.fetchMs,
      textMs: p.textMs,
      textReadyAt: p.textReadyAt,
      textLength: p.textLength,
      replacementChars: p.replacementChars,
      highlightMs: p.highlightMs,
      rowCount: p.rowCount,
      rowSamples: p.rowSamples,
      firstRowAt: p.firstRowAt,
      stateAt: p.stateAt,
      statusText: p.statusText,
      scrollWidth: p.scrollWidth,
      scrollHeight: p.scrollHeight,
      layoutMs: p.layoutMs,
      longTasks: p.longTasks,
      errors: p.errors,
      clickAt: p.clickAt,
      now: performance.now(),
    };
  })()`, true, timeoutMs);
}

async function collectMetrics() {
  // Performance.getMetrics values are cumulative for the renderer process and
  // therefore misleading across navigations. Heap after an explicit collection
  // is still only a coarse, snapshot-level observation; it is not a device
  // memory measurement.
  try {
    await send('HeapProfiler.enable');
    await send('HeapProfiler.collectGarbage');
  } catch {}
  const heap = await send('Runtime.getHeapUsage').catch(() => ({}));
  return { jsHeapUsedSize: heap.result?.usedSize };
}

async function measure(name) {
  const q = new URLSearchParams({
    id: 'sandbox', path: name, repo: 'issue144-sandbox', wt_name: 'sandbox',
    branch: 'sandbox', file: name, full_path: '/sandbox/' + name,
  });
  await navigate(`${base}/preview?${q}`);
  await evalJs(INSTRUMENT);

  const wallStart = performance.now();
  await evalJs(`document.getElementById('tab-formatted').click(); 'clicked'`, false, 10000);
  let result;
  try {
    result = await waitForPageDone();
  } catch (e) {
    return { fixture: name, fatal: e.message, wallMs: Math.round(performance.now() - wallStart) };
  }
  const wallMs = Math.round(performance.now() - wallStart);
  const metrics = await collectMetrics().catch(() => ({}));
  const post = await evalJs(`({
    errorText: (document.querySelector('.error-state') || {}).textContent || '',
    renderedRows: document.querySelectorAll('.code-line').length,
    scriptExecuted: window.__issue144_xss === 1,
    contentTextLength: (document.getElementById('content') || {}).textContent?.length || 0,
    iframeCount: document.querySelectorAll('#content iframe').length,
    codeLinesContainer: !!document.querySelector('#content .code-lines'),
    markdownBody: !!document.querySelector('#content .markdown-body'),
  })`).catch(() => ({}));
  return {
    fixture: name, wallMs, ...result, ...metrics, ...post,
    fatal: '',
  };
}

async function navigatePreview(name) {
  const q = new URLSearchParams({
    id: 'sandbox', path: name, repo: 'issue144-sandbox', wt_name: 'sandbox',
    branch: 'sandbox', file: name, full_path: '/sandbox/' + name,
  });
  await navigate(`${base}/preview?${q}`);
  await evalJs(INSTRUMENT);
}

async function interactionProbe() {
  const out = [];

  // 1) Preflight limit -> safe excerpt button.
  await navigatePreview('interaction-limit.html');
  await evalJs(`document.getElementById('tab-formatted').click(); 'clicked'`);
  const limited = await waitForPageDone();
  const limitWallStart = performance.now();
  const limitButton = await evalJs(`(() => {
    const btn = document.querySelector('.preview-limit button');
    if (!btn) return false;
    btn.click();
    return true;
  })()`);
  const excerpt = await evalJs(`(async () => {
    const content = document.getElementById('content');
    for (let i = 0; i < 1000; i++) {
      const state = content.getAttribute('data-render-state') || '';
      if (state === 'excerpt' || state === 'error') {
        return {
          state,
          rows: document.querySelectorAll('.code-line').length,
          note: (document.querySelector('.preview-note') || {}).textContent || '',
          status: (document.querySelector('.preview-render-status .grow') || {}).textContent || '',
        };
      }
      await new Promise(r => setTimeout(r, 20));
    }
    return { state: 'timeout', rows: document.querySelectorAll('.code-line').length };
  })()`, true, 30000);
  out.push({
    fixture: 'interaction-limit.html',
    kind: 'limit-safe-excerpt',
    state: limited.state,
    limitRows: limited.rowCount,
    limitStatus: limited.statusText,
    excerptButtonClicked: limitButton,
    excerptState: excerpt.state,
    excerptRows: excerpt.rows,
    excerptNote: excerpt.note,
    excerptStatus: excerpt.status,
    wallMs: Math.round(performance.now() - limitWallStart),
  });

  // 2) Stop an in-flight adaptive render and verify no late chunks append.
  await navigatePreview('interaction-slow.html');
  await evalJs(`document.getElementById('tab-formatted').click(); 'clicked'`);
  const observed = await evalJs(`(async () => {
    const content = document.getElementById('content');
    for (let i = 0; i < 2000; i++) {
      const state = content.getAttribute('data-render-state') || '';
      const rows = document.querySelectorAll('.code-line').length;
      if (rows > 0 && state === 'running') {
        return { state, rows, status: (document.querySelector('.preview-render-status .grow') || {}).textContent || '' };
      }
      if (state === 'truncated' || state === 'error' || state === 'complete') {
        return { state, rows, status: 'render ended before Stop could be clicked' };
      }
      await new Promise(r => setTimeout(r, 5));
    }
    return { state: 'timeout', rows: 0 };
  })()`, true, 30000);
  const clickedStop = await evalJs(`(() => {
    const btn = document.querySelector('.preview-render-status button');
    if (!btn) return false;
    btn.click();
    return true;
  })()`);
  const stopped = await evalJs(`(async () => {
    const content = document.getElementById('content');
    for (let i = 0; i < 500; i++) {
      const state = content.getAttribute('data-render-state') || '';
      if (state === 'stopped') {
        const before = document.querySelectorAll('.code-line').length;
        await new Promise(r => setTimeout(r, 150));
        return {
          state,
          rowsBefore: before,
          rowsAfter: document.querySelectorAll('.code-line').length,
          status: (document.querySelector('.preview-render-status .grow') || {}).textContent || '',
          stopButtonPresent: !!document.querySelector('.preview-render-status button'),
        };
      }
      await new Promise(r => setTimeout(r, 10));
    }
    return { state: 'timeout', rowsBefore: -1, rowsAfter: -1 };
  })()`, true, 30000);
  out.push({
    fixture: 'interaction-slow.html',
    kind: 'stop-in-flight',
    observedState: observed.state,
    observedRows: observed.rows,
    stopButtonClicked: clickedStop,
    finalState: stopped.state,
    stoppedRowsBefore: stopped.rowsBefore,
    stoppedRowsAfter: stopped.rowsAfter,
    stoppedStatus: stopped.status,
    stopButtonPresentAfterStop: stopped.stopButtonPresent,
  });
  return out;
}

async function main() {
  await new Promise(resolve => server.listen(0, '127.0.0.1', resolve));
  const port = server.address().port;
  if (!base) base = `http://127.0.0.1:${port}`;
  try {
    await startBrowser();
    // Warm up the cold path with a small file; discard the result.
    await measure('smoke.html');
    if (suite === 'interaction') {
      const rows = await interactionProbe();
      for (const row of rows) {
        row.cpuRate = cpuRate;
        row.suite = suite;
        console.log(JSON.stringify(row));
      }
      return;
    }
    const names = [...fixtures.keys()].filter(n => n !== 'smoke.html');
    for (const name of names) {
      let out;
      try {
        out = await measure(name);
      } catch (e) {
        out = { fixture: name, fatal: e.message };
      }
      out.cpuRate = cpuRate;
      out.suite = suite;
      console.log(JSON.stringify(out));
    }
  } finally {
    try { ws?.close(); } catch {}
    try { chrome?.kill('SIGTERM'); } catch {}
    await sleep(150);
    try { chrome?.kill('SIGKILL'); } catch {}
    await new Promise(resolve => server.close(resolve));
    try { rmSync(USER_DATA, { recursive: true, force: true }); } catch {}
  }
}

main().catch(e => {
  console.error('FATAL', e.stack || e.message);
  process.exitCode = 1;
});
