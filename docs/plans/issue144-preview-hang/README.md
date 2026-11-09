# Issue #144 Preview 渲染卡死 — 沙盒验证

本目录包含 issue #144 的 **归因沙盒** 和 **实施后可中断渲染验证**。

- `RESULTS.md` 第 1–7 节是实施前分析基线；
- `RESULTS.md` 第 8 节记录前端自适应限流实施后的真实 Chrome 沙盒结果；
- 产品实现位于 `internal/ui/static/preview.html`，精确文件大小由 `internal/app/app.go` 的 `X-File-Size` 响应头提供；
- Markdown sanitize、HTML iframe 效果预览和格式扩展不在本轮范围内。

## 目标与边界

沙盒使用：

- **真实产品文件**：`internal/ui/static/preview.html`、`internal/app/app.go` 对应的响应契约与仓库内 vendor 的 highlight.js / marked.js；
- **真实浏览器**：本机 headless Google Chrome，通过 CDP 驱动；
- **mock 后端**：只提供 `/api/worktree/file/content` 与 `/api/worktree/file/diff`，内容由探针在内存生成；
- **不启动 `mw serve`**，不建真实 worktree，不写仓库文件，不访问外网。

未覆盖：真实 daemon、真实 iPadOS Safari/WebKit、真机低内存/低算力环境。因此沙盒结果不能代替真机验收。

## 已实现行为

`preview.html` 对 Formatted Preview 增加了：

- 渲染前预检：文件大小 / 行数 / 最长行；
- 硬限制：128 KiB、2,000 行、12,000 字符/行；
- 超限时不执行全量源码渲染，展示实际大小、阈值和 **Show safe excerpt** 按钮；
- bounded excerpt：最多 200 行 / 64 KiB，超长行会被截断并明确提示；
- 源码渲染按行分片，每片之间让出主线程；
- 依据实际 DOM 创建耗时和帧等待耗时自适应调整片大小；
- 2 秒总渲染预算，超时自动停止并保留已渲染内容；
- 渲染中显示进度与 **Stop** 按钮，点击后停止且不再追加；
- 切换 tab 会取消进行中的渲染，late fetch/render 不会覆盖新 tab；
- `data-render-state` 暴露测试状态，不改变用户界面文案。

## 运行方式

前置：`node`（使用 Node 内置模块，无需 npm install）与 `google-chrome`。

```bash
# 实施后：长度 / 行数 / 硬限制扫描，6x CPU 节流
node docs/plans/issue144-preview-hang/evidence/preview-render-probe.mjs \
  --suite=h1 --cpu=6

# 实施后：格式 / 编码 / 错误响应样本
node docs/plans/issue144-preview-hang/evidence/preview-render-probe.mjs \
  --suite=h2 --cpu=6

# 交互验证：limit -> safe excerpt，以及 Stop 中断进行中的渲染
node docs/plans/issue144-preview-hang/evidence/preview-render-probe.mjs \
  --suite=interaction --cpu=6
```

每个探针会启动一个独立 Chrome 实例，测量完自动关闭浏览器、HTTP server，并删除临时 Chrome profile。输出为 JSONL，每个 fixture 一行。

本目录中的证据：

- 实施前基线：`evidence/h1-cpu6.jsonl`、`evidence/h1-cpu1.jsonl`、`evidence/h2-cpu6.jsonl`
- 实施后：`evidence/post-fix-h1-cpu6.jsonl`、`evidence/post-fix-h2-cpu6.jsonl`
- 实施后交互：`evidence/interaction-cpu6.jsonl`

## 指标说明

- `state`：最终 `#content[data-render-state]`，如 `limit`、`running`、`complete`、`excerpt`、`truncated`、`stopped`、`error`。
- `wallMs`：Node 侧从点击 Formatted tab 到页面进入终态的墙钟时间。
- `highlightMs`：主线程 `hljs.highlight()` 同步耗时。
- `rowCount`：最终 `.code-line` 数量。
- `rowSamples`：渲染过程中观察到的行数变化序列；中间行数证明渲染是分片让出执行的，而不是一次同步 append。
- `statusText` / `limitStatus`：进度、停止或限流提示的可见文案。
- `layoutMs`：终端状态下读取 `#content.scrollWidth/scrollHeight` 的布局耗时。
- `longTasks`：页面内 `PerformanceObserver` 捕获的长任务；部分样本因浏览器上报时序缺失，不代表没有长任务。
- `iframeCount` / `codeLinesContainer` / `scriptExecuted`：确认 HTML 是否走 iframe 效果预览、源码行渲染路径，以及仓库 HTML 中的脚本是否被执行。

## 相关分析

完整结论见 [RESULTS.md](./RESULTS.md)。
