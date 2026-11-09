// Behavioural tests for issue #144's front-end safety envelope:
//
//   * preflight measures bytes / lines / longest line before any DOM or
//     highlight work, and refuses all three independently;
//   * chunk sizing shrinks after a slow/slow-layout chunk and grows after a
//     fast one, clamped to the configured floor/ceiling;
//   * the bounded excerpt never exceeds line / byte / line-length limits;
//   * the shipped source still uses the interruptible async chunk loop and
//     cancellation checks rather than a full synchronous append.
//
// The pure helpers are sliced from the shipped preview.html rather than
// copied, so drift in the served page fails the slice anchors loudly.
//
// Run: node --test testdata/preview_adaptive_render.test.mjs
import test from "node:test";
import assert from "node:assert/strict";
import { readFileSync } from "node:fs";
import { fileURLToPath } from "node:url";
import { dirname, join } from "node:path";
import vm from "node:vm";

const here = dirname(fileURLToPath(import.meta.url));
const previewSource = readFileSync(join(here, "..", "static", "preview.html"), "utf8");
const workerSource = readFileSync(join(here, "..", "static", "preview_markdown_worker.js"), "utf8");
const sourceWorkerSource = readFileSync(join(here, "..", "static", "preview_source_highlight_worker.js"), "utf8");
const highlightSource = readFileSync(join(here, "..", "static", "vendor", "highlight", "highlight.min.js"), "utf8");
const markedSource = readFileSync(join(here, "..", "static", "vendor", "marked", "marked.min.js"), "utf8");

function inlineScript(html) {
  const start = html.lastIndexOf("<script>");
  const end = html.indexOf("</script>", start);
  assert.ok(start > 0 && end > start, "could not locate preview.html inline <script>");
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

const script = inlineScript(previewSource);
const helperFactory = new Function(`
  ${sliceBlock(script, "function formatPreviewBytes(")}
  ${sliceBlock(script, "function formatPreviewCount(")}
  ${sliceBlock(script, "function measurePreviewContent(")}
  ${sliceBlock(script, "function previewLimitReasons(")}
  ${sliceBlock(script, "function adaptPreviewChunkSize(")}
  ${sliceBlock(script, "function buildPreviewExcerpt(")}
  ${sliceBlock(script, "function extToLang(")}
  ${sliceBlock(script, "function basenameToLang(")}
  ${sliceBlock(script, "function langForFilePath(")}
  return {
    formatPreviewBytes, formatPreviewCount, measurePreviewContent,
    previewLimitReasons, adaptPreviewChunkSize, buildPreviewExcerpt,
    extToLang, basenameToLang, langForFilePath,
  };
`);
const h = helperFactory();

const LIMITS = {
  maxBytes: 128 * 1024,
  maxLines: 2000,
  maxLineChars: 12000,
  excerptLines: 200,
  excerptBytes: 64 * 1024,
  initialChunkSize: 80,
  minChunkSize: 20,
  maxChunkSize: 400,
  targetChunkMs: 16,
  maxTotalRenderMs: 2000,
};

test("issue #144: metrics drop the trailing newline and report the longest line", () => {
  const info = h.measurePreviewContent("a\n" + "b".repeat(12) + "\n");
  assert.equal(info.lineCount, 2);
  assert.equal(info.longestLine, 12);
  assert.equal(info.charCount, 15);
  assert.deepEqual(info.lines, ["a", "b".repeat(12)]);
});

test("issue #144: each hard limit is independent and exact-boundary input is allowed", () => {
  assert.deepEqual(h.previewLimitReasons({ lineCount: 10, longestLine: 100 }, 100, LIMITS), []);
  assert.deepEqual(
    h.previewLimitReasons({ lineCount: 10, longestLine: 100 }, LIMITS.maxBytes + 1, LIMITS),
    ["size"],
  );
  assert.deepEqual(
    h.previewLimitReasons({ lineCount: LIMITS.maxLines + 1, longestLine: 100 }, 100, LIMITS),
    ["lines"],
  );
  assert.deepEqual(
    h.previewLimitReasons({ lineCount: 10, longestLine: LIMITS.maxLineChars + 1 }, 100, LIMITS),
    ["line-length"],
  );
  assert.deepEqual(
    h.previewLimitReasons(
      { lineCount: LIMITS.maxLines + 1, longestLine: LIMITS.maxLineChars + 1 },
      LIMITS.maxBytes + 1,
      LIMITS,
    ),
    ["size", "lines", "line-length"],
  );
});

test("issue #144: chunk size halves on slow chunks, doubles on fast ones, and clamps", () => {
  assert.equal(h.adaptPreviewChunkSize(160, 40, 16, LIMITS), 80, "slow DOM work must shrink");
  assert.equal(h.adaptPreviewChunkSize(160, 1, 80, LIMITS), 80, "slow frame/layout must shrink");
  assert.equal(h.adaptPreviewChunkSize(100, 1, 16, LIMITS), 200, "fast chunks may grow");
  assert.equal(h.adaptPreviewChunkSize(300, 1, 16, LIMITS), LIMITS.maxChunkSize);
  assert.equal(h.adaptPreviewChunkSize(40, 40, 16, LIMITS), LIMITS.minChunkSize);
  assert.equal(h.adaptPreviewChunkSize(100, 10, 20, LIMITS), 100, "inside the target band stays");
});

test("issue #144: the safe excerpt respects all three excerpt limits", () => {
  const lines = Array.from({ length: 50 }, (_, i) => "x".repeat(30) + String(i));
  const ex = h.buildPreviewExcerpt(lines, {
    excerptLines: 7,
    excerptBytes: 100,
    maxLineChars: 10,
  });
  assert.ok(ex.lineCount > 0 && ex.lineCount <= 7);
  assert.ok(ex.charCount <= 100);
  assert.ok(ex.omittedLines > 0);
  assert.equal(ex.lineTruncated, true);
  for (const line of ex.lines) {
    assert.ok(line.length <= 11, "a shortened line is at most maxLineChars plus the ellipsis");
  }
  assert.equal(ex.text, ex.lines.join("\n"));
});

test("issue #144: byte formatting is stable for the preflight notice", () => {
  assert.equal(h.formatPreviewBytes(512), "512 B");
  assert.equal(h.formatPreviewBytes(128 * 1024), "128 KB");
  assert.equal(h.formatPreviewCount(12345), "12,345");
});

test("issue #144: backend extension/special-name support has a front-end language mapping", () => {
  assert.equal(h.extToLang(".htm"), "xml");
  assert.equal(h.extToLang(".mjs"), "javascript");
  assert.equal(h.extToLang(".kt"), "kotlin");
  assert.equal(h.extToLang(".sass"), "scss");
  assert.equal(h.extToLang(".graphql"), "graphql");
  assert.equal(h.extToLang(".diff"), "diff");
  assert.equal(h.extToLang(".ini"), "ini");
  assert.equal(h.langForFilePath("Dockerfile"), "dockerfile");
  assert.equal(h.langForFilePath("src/Makefile"), "makefile");
  assert.equal(h.langForFilePath("foo.dockerfile"), "dockerfile");
  assert.equal(h.langForFilePath("report.htm"), "xml");
});

test("issue #144: shipped page keeps the interruptible adaptive loop wired", () => {
  for (const anchor of [
    "var SOURCE_LIMITS = {",
    "var MARKDOWN_LIMITS = {",
    "maxBytes: 128 * 1024,",
    "maxBytes: 1024 * 1024,",
    "maxLines: 20000,",
    "maxLineChars: 32 * 1024,",
    "function startPreviewSourceRender(text, fileType, context) {",
    "function startPreviewMarkdownRender(text, metrics) {",
    "new Worker('/static/preview_markdown_worker.js')",
    "new Worker('/static/preview_source_highlight_worker.js')",
    "worker.terminate()",
    "function highlightSourceWithWorker(job, text, lang) {",
    "await highlightSourceWithWorker(job, text, lang);",
    "await yieldToPreviewFrame(job);",
    "if (job.cancelled) return;",
    "void container.scrollHeight;",
    "chunkSize = adaptPreviewChunkSize(chunkSize, workMs, frameMs, activeLimits);",
    "finishTruncatedPreviewRender(job, 'timeout');",
    "content.setAttribute('data-render-state', 'running');",
    "content.setAttribute('data-render-state', 'limit');",
    "resp.headers.get('X-File-Size')",
    "if (loadSeq !== activeLoadSeq || activeTab !== 'formatted') return;",
    "showPreviewLimit(text, fileType, sourceBytes, metrics, reasons, limits);",
    "function showContentLoadError(err) {",
    "function observeMarkdownCodeBlocks(container) {",
    "IntersectionObserver",
    "AbortController",
    "formattedRenderCache",
  ]) {
    assert.ok(script.includes(anchor), `preview.html is missing issue #144 anchor: ${anchor}`);
  }
  assert.ok(
    !script.includes("marked.parse("),
    "Markdown parsing must not run directly on the page main thread",
  );
  assert.ok(
    !script.includes("marked.min.js"),
    "the main preview page must not load the Markdown parser",
  );
  assert.ok(
    !script.includes("hlLines = splitHighlightedLines(text, lang);"),
    "source highlighting must not run synchronously on the main thread",
  );
  assert.ok(
    sourceWorkerSource.includes("importScripts('/static/vendor/highlight/highlight.min.js')"),
    "source highlight worker must load the vendored highlighter",
  );
  assert.ok(
    !script.includes("content.appendChild(renderWithLineNumbers("),
    "the old one-shot synchronous source renderer must not remain as a live path",
  );
  assert.match(
    script,
    /var safety = setTimeout\(finish, 250\);/,
    "hidden-tab rAF fallback is part of the interruption contract",
  );
});

test("issue #144: source worker returns per-line highlighted HTML", () => {
  const messages = [];
  const context = {
    console,
    self: { postMessage: message => messages.push(message) },
  };
  context.importScripts = () => vm.runInContext(highlightSource, context);
  vm.createContext(context);
  vm.runInContext(sourceWorkerSource, context);

  context.self.onmessage({
    data: {
      type: "highlight",
      id: 9,
      language: "javascript",
      text: "const answer = 42;\nfunction add(a, b) { return a + b; }",
    },
  });
  assert.equal(messages.length, 1);
  assert.equal(messages[0].type, "result");
  assert.equal(messages[0].id, 9);
  assert.equal(messages[0].lines.length, 2);
  assert.match(messages[0].lines[0], /hljs-/);
});

test("issue #144: Markdown worker escapes raw HTML and unsafe URLs", () => {
  assert.match(markedSource, /marked v12\.0\.2/);
  assert.match(workerSource, /typeof html !== 'string'/);
  assert.match(workerSource, /typeof href !== 'string'/);
  const messages = [];
  const context = {
    console,
    self: { postMessage: message => messages.push(message) },
  };
  context.importScripts = () => vm.runInContext(markedSource, context);
  vm.createContext(context);
  vm.runInContext(workerSource, context);

  const payload = [
    '<img src=x onerror="alert(1)">',
    '',
    '<script>alert(2)</script>',
    '',
    '[click](javascript:alert(3))',
    '',
    '[encoded](java&#x73;cript:alert(3))',
    '',
    '[encoded-colon](javascript&#58;alert(3))',
    '',
    '![x](javascript:alert(4))',
    '',
    '![encoded](java&#x73;cript:alert(4))',
    '',
    '[safe](https://example.com)',
  ].join('\n');
  context.self.onmessage({ data: { type: 'parse', id: 7, text: payload } });
  assert.equal(messages.length, 1);
  const message = messages[0];
  assert.equal(message.type, 'result');
  assert.equal(message.id, 7);
  assert.ok(message.html.includes('&lt;img src=x onerror=&quot;alert(1)&quot;&gt;'));
  assert.ok(!message.html.includes('<script>'));
  assert.ok(!message.html.includes('javascript:'));
  assert.ok(!message.html.includes('java&#x73;cript:'));
  assert.ok(!message.html.includes('javascript&#58;'));
  assert.ok(message.html.includes('<p>click</p>'));
  assert.ok(message.html.includes('<p>encoded</p>'));
  assert.ok(message.html.includes('<p>encoded-colon</p>'));
  assert.ok(message.html.includes('<p>x</p>'));
  assert.ok(message.html.includes('<p>encoded</p>'));
  assert.ok(message.html.includes('<a href="https://example.com">safe</a>'));
});
