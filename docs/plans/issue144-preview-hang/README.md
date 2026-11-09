# Issue #144 Preview 渲染卡死 — 归因与实现验证

本目录记录 issue #144 的归因、实现和评审修复证据。

- `RESULTS.md` §1–§7 是实施前分析基线；
- `RESULTS.md` §8–§10 是中间轮次（阈值已废弃）；
- `RESULTS.md` §11 是最终 shipped 实现、测试和非目标；
- `evidence/README.md` 说明各 JSONL 属于哪一轮，只有 `final-*` 是 shipped 证据。

## 最终实现摘要

Formatted Preview 现在按 renderer 分开保护：

- **Markdown**
  - 上限：1 MiB / 20,000 行 / 32 KiB 单行；
  - `marked.parse()` 在 `preview_markdown_worker.js` 中执行；
  - raw HTML 转义，危险 URL scheme 拒绝；
  - 支持 Stop / 切 tab terminate；
  - 代码块通过 IntersectionObserver 可见区域懒高亮。
- **源码 / 纯文本**
  - 上限：128 KiB / 2,000 行 / 12,000 字符 / 2,000 ms；
  - `hljs.highlight()` 在 `preview_source_highlight_worker.js` 中执行；
  - DOM 按片构建，每片后强制布局测量并让出主线程；
  - 支持 Stop / 切 tab terminate。
- **通用**
  - backend text transport cap 2 MiB；超过返回 413；
  - 未知扩展名、二进制 NUL、非 UTF-8 在读取/解析前阻断；
  - 超过 renderer 上限但仍在 transport cap 内的文件返回前端，显示 `Preview limited` 和 bounded excerpt；
  - 不支持类型显示 `Preview not supported`，不强制降级解析；
  - fetch 支持 AbortController + 15 s timeout；
  - Markdown HTML 和源码完成 DOM 有 tab 级缓存；
  - `/preview` 与 `/static/*` 使用 `no-store`，避免页面/Worker 版本错配。

## 运行沙盒

前置：`node`（Node 内置模块，无 npm 依赖）与 `google-chrome`。

```bash
# 源码长度 / 行数 / 硬限制扫描（6x CPU）
node docs/plans/issue144-preview-hang/evidence/preview-render-probe.mjs \
  --suite=h1 --cpu=6

# 交互验证：limit -> safe excerpt，以及 Stop 中断
node docs/plans/issue144-preview-hang/evidence/preview-render-probe.mjs \
  --suite=interaction --cpu=6
```

每个探针启动一个受控 Chrome，结束后关闭浏览器和 HTTP server，并删除临时 profile。

## 证据索引

- 实施前基线：`evidence/h1-cpu1.jsonl`、`h1-cpu6.jsonl`、`h2-cpu6.jsonl`
- 中间轮次（非 shipped）：`post-fix-*`、`interaction-cpu6.jsonl`
- 最终源码路径：`evidence/final-source-h1-cpu6.jsonl`
- 最终 Markdown Worker：`evidence/final-markdown-live.jsonl`
- 探针：`evidence/preview-render-probe.mjs`

## 指标说明

- `state`：`#content[data-render-state]` 终态。
- `wallMs`：从点击 Formatted tab 到终态的墙钟时间。
- `highlightMs`：主线程 highlighter 耗时；最终源码路径应为 0，因为高亮在 Worker。
- `rowCount` / `rowSamples`：最终与过程中的代码行数。
- `layoutMs`：终端状态下强制布局耗时。
- `longTasks`：PerformanceObserver 捕获的长任务；缺失不代表没有长任务。
- Markdown live evidence 的 `panel` / `perf` 同时记录 Worker round-trip、post-worker commit、DOM→first-frame 和可见主线程 heartbeat；跨前后台时以 commit/first-frame 差值为准。

## 相关分析

完整结论见 [RESULTS.md](./RESULTS.md)。
