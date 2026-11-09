# Issue #144 沙盒验证结果

日期：2026-11-09
状态：**归因分析完成；产品代码未修改，issue 未修复**

## 1. 环境

- Linux，Node `v24.19.0`
- Google Chrome `157.0.8103.38`，`--headless=new`，1024×768
- CDP over WebSocket，Node 内置模块，无 npm 依赖
- CPU 节流：`Emulation.setCPUThrottlingRate`，未节流 `1x` 与 `6x` 各跑一组
- 服务的是仓库当前 `internal/ui/static/preview.html`；API 为探针内置 mock

**这不是 iPadOS Safari 实测。** 6x CPU 节流只是低算力环境的近似，不能据此宣称真机已复现或阈值已定案。

## 2. 当前实现的两个明确事实

### 2.1 `.html` 没有“效果预览”

`detectFileType()` 把 `.html` 归为 `text/x-code`；`showFormatted()` 走 `renderWithLineNumbers()`，`extToLang()` 把 `.html` 映射到 `xml` 源码高亮。沙盒中所有 HTML 样本均为：

- `codeLinesContainer = true`
- `iframeCount = 0`
- HTML 中的 `<script>` 未执行（`scriptExecuted = false`）

因此“HTML 渲染后的页面效果”和“沙箱 iframe 预览”当前都不存在。

### 2.2 同步渲染路径没有长度防线

`preview.html` 的 Formatted 路径是同一调用栈内：

1. `resp.text()` 解码完整文本；
2. `hljs.highlight(text, {language})` 高亮完整文本；
3. 逐行 `document.createElement` 两个 `span`，对内容行执行 `innerHTML`；
4. 每行 `.code-line { display:flex; white-space:pre; min-width:max-content; }`，浏览器为全部行计算布局。

`cachedContent` 只缓存文本，不缓存高亮结果或 DOM；每次重开 Formatted tab 会重复执行高亮与渲染。后端只有 1 MiB 硬拒绝；前端没有自己的大小/行数阈值、取消、超时、分帧、虚拟化或“仅显示前 N 行”退路。

### 2.3 格式支持矩阵静态核对

`detectFileType()` / `extToLang()` 与 issue 描述基本一致，但有一处需要精确化：

- `.html`、`.svg` 都归 `text/x-code`，只做 XML 源码高亮，不做效果预览/图片预览；
- issue 列出的 `.kt` `.swift` `.rb` `.php` `.lua` `.dart` `.cs` `.r` `.pl` `.ex` `.exs` `.hs` `.clj` `.mjs` `.cjs` `.graphql` `.proto` `.tf` `.mk` `.textproto` `.tsv` `.csv` `.ini` `.cfg` `.log` `.txt` `.diff` `.patch` 等均未命中后端白名单，落到 `text/plain`（仍会走前端源码行渲染）；
- `.sass` 后端归 `text/x-code`，但 `extToLang()` 没有 `.sass` 映射，实际退化为无高亮；
- 无扩展名的 `Dockerfile` / `Makefile` / `Rakefile` / `Gemfile` 确实落到 `text/plain`；
- issue 中“`.dockerfile` 这条 case 实际永不命中”并不完全准确：Go `filepath.Ext("foo.dockerfile") == ".dockerfile"`，该 case 可命中；只有 `filepath.Ext("Dockerfile") == ""`，所以无扩展名的 `Dockerfile` 不命中。

以上是静态核对结果，不是本沙盒对真实后端 `detectFileType()` 的端到端测试。

## 3. H1：长度 / 行数假设

### 3.1 固定约 100 KiB，只改变行数（6x CPU）

| 渲染行数 | wallMs | 建 DOM | 布局 | 最大长任务 |
|---:|---:|---:|---:|---:|
| 1 | 189 ms | 48 ms | 40 ms | 109 ms |
| 10 | 189 ms | 51 ms | 53 ms | 128 ms |
| 100 | 241 ms | 63 ms | 77 ms | 165 ms |
| 1,000 | 786 ms | 119 ms | 521 ms | 664 ms |
| 5,000 | 3,132 ms | 353 ms | 2,484 ms | 2,864 ms |
| 10,000 | 5,613 ms | 697 ms | 4,433 ms | — |

同一份约 100 KiB 文本，只把行数从 1 提到 10,000，点击到首帧从约 0.19 s 变成约 5.6 s。**行数本身是独立放大因子，不只是字节数。**

### 3.2 相同数量级字节：单长行 vs 多行 HTML（6x CPU）

| 内容大小 | 单行 wall | 多行 wall | 单行布局 | 多行布局 | 多行行数 |
|---:|---:|---:|---:|---:|---:|
| ≈50 KiB | 227 ms | 1,169 ms | 64 ms | 576 ms | 732 |
| ≈100 KiB | 297 ms | 2,153 ms | 100 ms | 1,286 ms | 1,463 |
| ≈250 KiB | 599 ms | 4,839 ms | 295 ms | 2,986 ms | 3,658 |
| ≈500 KiB | 1,037 ms | 8,744 ms | 610 ms | 5,478 ms | 7,315 |
| ≈900 KiB | 1,624 ms | 15,126 ms | 840 ms | 9,616 ms | 13,166 |

约 908 KiB 的多行 HTML 在 6x 节流下：

- 高亮约 2.48 s；
- 逐行建 DOM + 高亮约 3.97 s；
- 布局约 9.62 s；
- 点击到首帧约 **15.1 s**。

同一 908 KiB 样本未节流时仍要约 **1.97 s**（布局约 1.21 s）。6x 节流放大约 7.7 倍。

### 3.3 H1 归因判断

证据支持 **长度/行数（尤其是 DOM 行数）主导**：

- 高亮自身不是唯一主因：900 KiB 单行的高亮约 0.08 s，而 13k 行布局约 9.6 s；
- 固定字节、增加行数时耗时单调大幅上升；
- 同一行数的 HTML 样本比纯文本样本更重，说明 highlight span 与布局结合会继续放大成本；
- 长单行也明显慢于短文本，但近 1 MiB 单行仍是秒级，而多行可到十几秒。

**结论：不能把问题只归为“格式支持”或“hljs 高亮慢”。当前同步逐行 DOM + 全量布局是主要可测瓶颈。** 未复现真机“整页无响应需强杀”的极端状态，因此仍不能宣布真机根因已闭环。

## 4. H2：格式 / 编码假设

### 4.1 短样本（6x CPU）

| 样本 | 结果 |
|---|---|
| 100 KiB minified HTML | 333 ms；1 行；源码高亮 |
| 100 KiB 内联 base64 HTML | 272 ms；1 行；源码高亮 |
| 非 UTF-8（GBK）HTML | 130 ms；无卡死；8,000 个 U+FFFD 替换字符，内容乱码 |
| `.sass` | 376 ms；600 行；后端归 `text/x-code`，前端无语言映射，退化为无高亮源码 |
| `Dockerfile` / `foo.dockerfile` | 224 / 198 ms；可显示为源码 |
| 实体尾部含 NUL 的 HTML | 165 ms；mock 模拟当前后端的 8 KiB 检测缺口，未卡死 |
| `413 file too large` | 29 ms；显示 `Failed to load content: file too large` |
| `415 binary file not supported` | 32 ms；显示对应通用错误 |
| 含 `<script>` 的短 HTML | 195 ms；脚本未执行 |

### 4.2 H2 归因判断

在本沙盒样本集内，**没有复现由格式解析本身触发的额外卡死**。但格式能力缺口是真实存在的：

- HTML 没有效果预览，且没有 iframe 沙箱路径；
- 不支持格式时没有“该格式不支持预览”的提示，而是静默落入源码/纯文本；
- 非 UTF-8 文本被按 UTF-8 解码，出现乱码；
- 413/415 只有通用错误，不显示实际大小、阈值或“仅看前 N 行 / 编辑器打开”等退路；
- 后端 8 KiB NUL 检测不能覆盖实体尾部二进制内容。

这些应作为格式/提示相关验收项，但与本次能测量到的卡死主因是两条独立假设。

## 5. 相邻安全问题（未修，仅确认）

vendor 中 `marked v12.0.2` 已移除 `sanitize` 选项，调用方仍传入：

```js
marked.parse(text, { sanitize: true })
```

在 Node 中直接加载该 vendor 实测：

```text
<img src=x onerror="alert(1)">
<script>alert(2)</script>
```

即 Markdown 预览仍会把仓库内容中的 HTML/脚本原样注入同源页面。该问题与卡死归因独立，不应混入本轮修复；建议单独开 issue 处理。

## 6. 建议的下一步（需用户确认范围）

1. **先定产品阈值，不直接上完整渲染重构**：至少同时限制字节数和渲染行数；阈值应基于真机 Safari 数据，而不是本沙盒的 6x 节流数据。本轮可先只做“超大文件不渲染 + 显示实际大小/阈值 + 明确退路”。
2. **拆分行为改动**：
   - 超阈值保护与提示；
   - 不支持格式提示；
   - HTML 效果预览的 sandbox iframe（不加 `allow-same-origin`）；
   - 高亮/DOM 缓存与分帧/虚拟化；
   - Markdown sanitization 单独安全修复。
3. **补真机证据**：用报告人的触发文件在 iPadOS Safari 记录 click→first paint、长时间帧和内存；同时确认桌面 Chrome/WebKit 表现。未取得该证据前，不建议把本沙盒阈值直接写进产品。

## 7. 未验证项

- 未复现 report 的真机浏览器卡死；
- 未取得报告人的具体触发文件；
- 未测 iPadOS Safari / WebKit；
- 未测真实后端 1 MiB 边界及真实 `mw serve` 数据流；
- 未验证不同 iPad 型号、内存压力、低电量模式或 Safari 版本；
- 未定义产品最终阈值，也未修改任何产品代码。

## 原始证据

- [`evidence/h1-cpu6.jsonl`](./evidence/h1-cpu6.jsonl)
- [`evidence/h1-cpu1.jsonl`](./evidence/h1-cpu1.jsonl)
- [`evidence/h2-cpu6.jsonl`](./evidence/h2-cpu6.jsonl)
- [`evidence/preview-render-probe.mjs`](./evidence/preview-render-probe.mjs)

## 8. 实施后的可中断渲染验证（2026-11-09）

第 1–7 节是实施前基线。用户确认“可中断渲染 + 自适应限流”后，本轮只修改了：

- `internal/ui/static/preview.html`
- `internal/app/app.go`（仅新增 `X-File-Size`，用于展示真实文件大小）
- `internal/ui/testdata/preview_adaptive_render.test.mjs`
- `internal/ui/preview_adaptive_render_test.go`
- `internal/app/app_test.go`（X-File-Size 精确值回归）
- 本目录的沙盒探针与证据

### 8.1 已实现策略

- 预检硬限制：128 KiB / 2,000 行 / 12,000 字符每行；
- 超限时默认不做完整源码渲染，显示实际值、阈值和 bounded excerpt 按钮；
- bounded excerpt：最多 200 行 / 64 KiB，超长行截断；
- 渲染分片：初始 80 行，最小 20，最大 400；
- 自适应目标：单片约 16 ms；按 DOM 创建耗时与帧等待耗时收缩/放大下一片；
- 总时间预算：2,000 ms，超过后停止并保留部分内容；
- Stop 按钮、tab 切换和替换 render 都会设置取消标记，循环在每次 yield 后检查；
- 每次分片后 `await` 一个 frame/timer yield，因此输入事件和进度 UI 可以运行。

### 8.2 真实 Chrome 沙盒结果（6x CPU 节流）

| 场景 | 结果 |
|---|---|
| 887.2 KiB / 13,166 行 HTML | `state=limit`，0 行，约 209 ms，未进入高亮/DOM 渲染 |
| 199.2 KiB / 3,000 行 HTML | `state=limit`，0 行；点击 safe excerpt 后 `state=excerpt`，200 行，约 530 ms |
| 100 KiB / 1,463 行 HTML | 分片渲染，`rowSamples` 33 个中间状态；约 2.09 s 时 `state=truncated`，保留 720 行并显示 2,000 ms 预算提示 |
| 50 KiB / 732 行 HTML | 分片渲染完成，`state=complete`，732 行；中间行数覆盖 80/120/…/732，不是一次同步 append |
| `.sass` 600 行 | `state=complete`，17 个中间行数样本，约 691 ms |
| `.dockerfile` 300 行 | `state=complete`，7 个中间行数样本，约 265 ms |
| 进行中的 100 KiB 渲染 | 观察到首片 80 行后点击 Stop：`state=stopped`，120 行，150 ms 后仍为 120 行，Stop 按钮消失 |
| 100 KiB 单行 HTML | `state=limit`，0 行，约 61 ms；实际最长行 102,400 字符被预检拒绝 |
| 10,000 行 / 约 100 KiB | `state=limit`，0 行，约 64 ms；行数上限先于 DOM 构造生效 |

可见证据：

- [`evidence/post-fix-h1-cpu6.jsonl`](./evidence/post-fix-h1-cpu6.jsonl)
- [`evidence/post-fix-h2-cpu6.jsonl`](./evidence/post-fix-h2-cpu6.jsonl)
- [`evidence/interaction-cpu6.jsonl`](./evidence/interaction-cpu6.jsonl)

### 8.3 测试结果

```text
node --test internal/ui/testdata/preview_adaptive_render.test.mjs  # 6 passed
GOCACHE=/tmp/issue144-gocache go test ./internal/ui ./internal/app -count=1
# ok myworktree/internal/ui
# ok myworktree/internal/app
```

### 8.4 仍然存在的边界

- 预检阈值内的 `hljs.highlight()` 仍在主线程同步执行；通过 128 KiB / 12,000 字符行上限把它限制在可接受范围，但没有移入 Web Worker。若后续需要支持更大的文件，Web Worker 高亮是下一阶段候选。
- 分片渲染不等于虚拟列表；成功完成时仍会为所有未超限的行建立 DOM。这里通过行数硬限制和 2,000 ms 自动停止防止无限增长。
- 未在 iPadOS Safari / WebKit 真机验证；所有阈值和交互仍需要真机数据确认。
- HTML 效果预览、不支持格式提示、Markdown sanitization 仍是独立后续项。
- Stop/自动停止后没有“继续渲染”按钮；重新点击 Formatted Preview 会从头开始。

## 9. 更新后的原始证据索引

- 实施前基线：`evidence/h1-cpu6.jsonl`、`evidence/h1-cpu1.jsonl`、`evidence/h2-cpu6.jsonl`
- 实施后自适应：`evidence/post-fix-h1-cpu6.jsonl`、`evidence/post-fix-h2-cpu6.jsonl`
- 实施后交互：`evidence/interaction-cpu6.jsonl`
- 探针：`evidence/preview-render-probe.mjs`

## 10. 最终安全 + 大文件方案（2026-11-09）

在 iPad 实测确认普通 111 KB Markdown 的同步 `marked.parse()` 仍需约 0.95–0.96 s、最大主线程空档约 1.01–1.02 s 后，本轮做了最终收敛：

### 10.1 支持的边界

- 文本文件上限：**1 MiB**（与后端硬上限一致）。
- 最大行数：**20,000 行**。
- 单行上限：**32 KiB**（后端也执行相同硬拒绝）。
- 超过边界时不强制解析，显示 `Preview limited` / `Preview not supported` 卡片。
- UTF-8 校验失败、二进制 NUL、未知扩展名：后端直接 415，前端不接收内容、不猜类型、不退化为强制 text 解析。

### 10.2 Markdown 安全异步路径

- 新增 `internal/ui/static/preview_markdown_worker.js`；
- `marked.parse()` 在 Worker 中执行，主线程只负责最终 DOM 注入；
- Worker 内转义 raw HTML，并拒绝 `javascript:` / `data:` / `vbscript:` 等显式危险 URL；
- 渲染中显示 `Parsing Markdown in background…` + Stop；切换 tab 会 terminate worker；
- Markdown 代码块高亮改为 `IntersectionObserver` 可见区域懒高亮，避免一次性 `hljs.highlightElement` 所有代码块。

### 10.3 大文件支持效果

桌面 headless Chrome 验证：

| 场景 | 旧主线程路径 | 新 Worker 路径 |
|---|---:|---:|
| 111 KB / 1,463 行 Markdown | marked.parse 45 ms（桌面）；iPad 约 949–964 ms | Worker 86 ms；主线程最大空档 98 ms；首帧 239 ms |
| 644 KB / 2,720 行 Markdown | iPad 约 9.5 s | Worker 197 ms；主线程最大空档 376 ms；首帧 616 ms |

源码分片路径（1x CPU）：

- 908,453 B / 13,166 行 HTML 源码：`complete`，13,166 行，约 7.1 s（分片可中断）。
- 99,999 B / 10,000 行源码：`complete`，约 1.35 s。
- 单行 > 32 KiB：`limit`，不建 DOM、不高亮。

### 10.4 未闭环项

- 需在 iPad Safari 上重新测 `/case/medium` 与 `/case/largest` 的 Worker 主线程空档；桌面数据不能替代真机。
- 1 MiB 以上文件仍由后端 413 拦截；未实现 DSH 风格的按页 Range API / Load more。若 iPad 上 1 MiB 的 DOM 注入仍明显卡顿，下一阶段再做分页。
- Markdown Worker 的 raw-HTML 转义是安全收敛，不是完整的 HTML sanitizer 合同；后续仍建议单独做安全审查。
