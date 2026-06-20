# 文件改动预览功能 — 可行性分析报告（方案 A 深化版）

> 分析日期：2026-06-20  
> 状态：**✅ 完全可行，技术风险低**  
> 选定方案：**方案 A — 新浏览器窗口 + 嵌入式 HTML 模板**

---

## 1. 需求概述

在 Staged / Unstaged 文件改动列表中，点击任意文件项：

- 在**新浏览器窗口**中打开预览页面
- 窗口包含两个 tab：**Diff 预览** 和 **格式化预览**
- 格式化预览需支持 Markdown 渲染和主流代码语法高亮

---

## 2. 方案 A 核心设计

### 2.1 方案定义

方案 A 的核心思路是：**主页面打开新窗口 → 新窗口加载嵌入式 HTML 模板 → 模板通过 URL 参数获取上下文 → 向 API 请求数据 → 客户端渲染**。

与项目中现有的 `index.html` 完全一致的架构模式：单文件 HTML，通过 Go 的 `embed.FS` 编译进二进制，在全功能浏览器环境中渲染。

### 2.2 完整交互流程

```
用户点击文件
    │
    ▼
主页面（index.html）
    ├── 从 state 中收集元数据（项目名、工作区名、分支名）
    ├── 构建 URL：/preview?id=wt1&path=src/main.go&repo=myworktree&...
    └── window.open(url, '_blank')
            │
            ▼
    新浏览器窗口加载 /preview
            │
            ▼
    后端返回 preview.html（从 embed.FS 读取）
            │
            ▼
    preview.html 解析 URL query 参数
            │
            ├── 渲染标题栏（项目名/工作区/分支/文件名 + 完整路径）
            ├── 渲染 Tab 栏
            └── 默认激活 Diff 预览 tab
                    │
                    ▼
            发起 fetch：GET /api/worktree/file/diff?id=...&path=...&staged=false
                    │
                    ▼
            后端执行 git diff -- <path>，返回纯文本 diff
                    │
                    ▼
            前端用代码高亮库渲染（diff 语言）
            │
            └── 用户切换到格式化预览 tab
                    │
                    ▼
            发起 fetch：GET /api/worktree/file/content?id=...&path=...
                    │
                    ▼
            后端读取文件内容，返回纯文本 + X-File-Type 头
                    │
                    ▼
            前端根据文件类型选择渲染器（Markdown / 代码高亮 / 纯文本）
```

**关键设计特点**：

- 预览窗口**不依赖父窗口**——关闭父窗口后预览窗口仍可正常工作
- URL 包含完整上下文，**可刷新、可复制分享**（在同域内）
- 每次切换 tab 才发起对应的 API 请求，**惰性加载**（避免同时请求 diff 和 content）
- 预览窗口**不轮询** git 状态——它展示的是打开时刻的快照

### 2.3 数据流与上下文传递

预览窗口需要的上下文信息分为三层：

**第一层：URL 传递（主页面 → 预览窗口）**

| 参数 | 含义 | 必要性 | 来源 |
|---|---|---|---|
| `id` | 工作树 ID | 必填（API 调用需要） | `state.activeWT` |
| `path` | 文件相对路径 | 必填（API 调用需要） | `c.path`（git changes 数据） |
| `repo` | 项目名 | 标题栏显示 | `state.mainRepo.name` |
| `wt_name` | 工作区名称 | 标题栏显示 | `state.worktrees` 中匹配 |
| `branch` | 分支名 | 标题栏显示 | `state.mainRepo.branch` 或 wt 数据 |
| `file` | 文件名（basename） | 标题栏显示 | `path.split('/').pop()` |
| `staged` | 是否 staged 区域 | Diff API 参数 | section === 'staged' |
| `full_path` | 完整绝对路径 | 标题栏第二行 | 可选（可后续由 API 响应头回填） |

**第二层：API 调用（预览窗口 → 后端）**

预览窗口的 JS 使用 `fetchAPI()` 封装（与主页面一致的认证处理），调用：
- `GET /api/worktree/file/diff?id=...&path=...&staged=...` → 获取 diff 文本
- `GET /api/worktree/file/content?id=...&path=...` → 获取文件内容

**第三层：后端响应头（后端 → 预览窗口）**

Content API 的响应可携带额外元数据：
- `X-File-Type`：文件类型提示（`text/markdown` / `text/x-code` / `text/plain`）
- `X-File-FullPath`：文件的完整绝对路径（回填标题栏第二行）
- `X-File-Size`：文件大小（前端可据此决定是否截断显示）

### 2.4 预览窗口生命周期

```
创建：window.open('/preview?...', '_blank')
    │
    ├── 窗口加载 preview.html
    ├── 从 URL 解析参数 → 渲染标题栏和 Tab 栏
    ├── 发起首个 API 请求（默认 Diff 预览）
    └── 渲染内容
    │
切换 Tab：用户点击 Tab 按钮
    │
    ├── 不重新加载窗口
    ├── 如果目标 tab 的数据未缓存 → 发起 API 请求
    ├── 如果已缓存 → 直接切换显示
    └── 更新 URL hash（/preview?...tab=formatted）支持浏览器前进/后退
    │
关闭：用户关闭窗口
    │
    └── 无需清理——窗口内无持久连接，无 WebSocket
```

**关键行为**：
- 用户可同时打开多个预览窗口（比较不同文件的 diff）
- 刷新窗口会重新加载 preview.html，重新发起 API 请求（数据可能已变化）
- 窗口标题动态更新为文件名

---

## 3. 与现有架构的集成分析

### 3.1 后端：两个新 API 的设计考量

两个新 API 在路由、认证、请求模式上与现有架构完全同构：

| 考量维度 | 设计决策 | 原因 |
|---|---|---|
| **路由注册位置** | `registerAPIs()` 中新增 2 行 | 与其他 API 统一管理 |
| **认证** | 不经额外处理，自动继承 `withAuth` 中间件 | 所有 `/api/*` 路由已统一经过认证链 |
| **参数传递** | URL query 参数（GET 请求） | 纯读取操作，与 `/api/worktree/status` 一致 |
| **超时** | 2 秒（复用 `s.gitRunner` 的默认超时） | 已满足大部分场景 |
| **响应格式** | 纯文本（`text/plain`），非 JSON | diff 和文件内容本质是文本，前端直接使用 |
| **错误处理** | HTTP 状态码 + 纯文本错误消息 | 前端展示友好的错误提示 |

**与现有 API 的差异**：现有的 `handleWorktreeStatus` 只返回 `--numstat` 统计（不返回 diff 内容），新 Diff API 返回完整 `git diff` 输出。两个 API 互补——主页面用 status 展示文件列表，预览窗口用 diff 展示单文件变更。

### 3.2 前端：与 index.html 的集成点

**改动点 1：文件列表点击事件**

- 位置：`renderGitChanges()` 函数中 `changes.forEach` 循环内
- 改动量：为每个 `.git-change-item` 添加 `click` 事件监听
- 影响范围：只新增行为，不改变现有渲染逻辑

**改动点 2：openFilePreview 辅助函数**

- 位置：index.html 中新增一个函数
- 职责：从 `state` 收集元数据，拼接 URL，调用 `window.open`
- 数据来源：`state.mainRepo`（项目名和分支）、`state.worktrees`（工作区名称和分支）、`c.path`（文件路径）

### 3.3 静态资源嵌入机制

`preview.html` 的嵌入方式与 `index.html` 完全一致：

- `ui.go` 中新增 `//go:embed static/preview.html` 指令
- `ui.Register` 中注册 `/preview` 路由
- 编译时 `preview.html` 被嵌入二进制，无需运行时文件系统访问
- `preview.html` 中引用的 vendor JS 库同样通过 `embed.FS` 嵌入

**可选的 title 动态注入**：与 `index.html` 一样，`ui.Register` 可以在返回 `preview.html` 前注入 repo name 到 `<title>` 标签。

### 3.4 CSP（内容安全策略）考量

当前 `index.html` 使用了大量内联 `<script>` 和 `<style>`，`preview.html` 预计采用相同模式：

- **内联脚本和样式**：不需要 CSP 调整 —— 现有 index.html 已大量使用内联代码
- **CDN 引入风险**：如果通过 CDN 引入 marked.js / highlight.js，需检查浏览器是否拦截
- **建议**：将 JS 库下载到 `static/vendor/` 通过 embed 嵌入，与现有 xterm 库一致，完全避免 CDN 依赖

---

## 4. 两个 Tab 的渲染分析

### 4.1 Diff 预览 Tab

**数据源**：`git diff [--cached] -- <path>` 的原始输出（unified diff 格式）

**最终方案：代码高亮库**。选用 highlight.js 或 Prism.js 的 `diff` 语法模块，对 `git diff` 输出进行 token 级语法高亮，效果对标 GitHub PR diff 视图。

**为什么不用纯 CSS 作为正式方案**：

纯 CSS（行首字符匹配）只能识别 3 种元素（`+`/`-`/`@@`），着色粒度停留在行级——`diff --git`、`index`、`---`、`+++` 等结构性行与普通上下文行混在一起，用户难以快速定位 diff 边界。代码高亮库能识别 7+ 种元素，token 级着色（如段落头 `@@ -1,5 +1,7 @@` 内部的行号范围也会单独着色），视觉层次与 GitHub 一致。

> 以一段典型的 `git diff` 输出为例，编码高亮库会给每类元素分配独立颜色：文件头灰色、index 行棕色、`@@` 段落头青色（数字范围单独着色）、`+` 行绿色、`-` 行红色、上下文行保持原色。用户一眼就能看出 diff 的结构层次。

**关键分析**：
- Diff 输出**不是 Markdown**——不能也不应该用 Markdown 渲染器处理
- Diff 输出**本质是代码高亮的子集**——`diff` 是代码高亮库的一等公民语言
- 高亮库体积可控（~10-30KB gzipped），对首屏加载影响可以忽略

**实际实现偏差（2026-06-20）**：

| 维度 | 计划 | 实现 |
|------|------|------|
| **布局** | 高亮库 + 双列 `<pre>` 布局 | 单行 flex 容器（`.code-line`）— 行号和代码在同一 DOM 行中，避免双列基线偏差累积错位 |
| **行号** | 无 | 真实文件行号：Diff 解析 `@@` hunk header 提取新文件行号；Formatted 使用自然序号 |
| **Diff 着色** | highlight.js `diff` 语言 | 同 plan — 保留 hljs `diff` token 级着色，CSS 仅设背景色（`diff-add`/`diff-del`） |
| **代码高亮** | highlight.js | 整段 `hljs.highlight` → `splitHighlightedLines` 按行安全分割，追踪跨行 token 的 `<span>` 标签并恢复 |
| **Tab 样式** | 无特别规划 | 对齐主页面 Instance tab 样式（顶部强调条、圆角上沿） |
| **合成 diff** | 未规划 | 新文件（untracked）场景后端合成完整 unified diff，含 `\ No newline at end of file` 标记 |
| **中文路径** | 未规划 | 双重防御：所有 git 命令加 `-c core.quotePath=false` + `unquoteGitPath` 反转义兜底 |
| **合成 diff 大小** | 未规划 | `os.Stat` 预检 + 合成后复检，防止大文件 OOM/DoS |

### 4.2 格式化预览 Tab

**数据源**：文件在工作区的当前内容（磁盘上的实际文件）

**渲染策略**（根据文件类型分派）：

| 文件类型 | 渲染器 | 说明 |
|---|---|---|
| `.md`, `.markdown` | marked.js | 客户端 Markdown → HTML 渲染 |
| `.go`, `.py`, `.js`, `.ts`, `.rs`, `.java`, `.c`, `.cpp`, `.html`, `.css`, `.json`, `.yaml`, `.yml`, `.toml`, `.sh`, `.bash`, `.zsh` | highlight.js / Prism.js | 代码语法高亮 |
| 其他纯文本 | `<pre>` + 等宽字体 | 无高亮的纯文本显示 |
| 二进制（检测到 null 字节） | 拒绝预览 | 显示提示："此文件为二进制文件，无法预览" |

**关键分析**：
- 文件类型判断**在前端完成**——后端通过 `X-File-Type` 响应头提供提示，前端最终决定使用哪个渲染器
- Markdown 和代码高亮**不会同时使用**——一个文件只走一条渲染路径
- 纯文本回退是**普适的兜底策略**——即使文件类型无法识别，用户仍能看到内容

### 4.3 依赖策略

**依赖清单**（全部在 Phase 2 引入）：

| 库 | 用途 | 大小（gzipped） | 被哪个 Tab 使用 |
|---|---|---|---|
| highlight.js（或 Prism.js） | 代码 + diff 语法高亮 | ~10-30KB | **两个 Tab 共享** |
| marked.js | Markdown → HTML 渲染 | ~20KB | 仅格式化预览 Tab |

**依赖引入方式**：与现有 `internal/ui/static/vendor/xterm/` 一致，下载后放入 `vendor/` 目录，通过 `embed.FS` 嵌入。

**开源许可证合规**：引入新开源项目后，必须在 `index.html` 的 Open Source Licenses 列表中追加新条目。该列表位于资源监控对话框（`#modal-monitor`）底部的 `.monitor-licenses-list` 中（`index.html:1374-1384`），当前已有 xterm.js（MIT）、gopsutil（BSD-3-Clause）等条目。需要新增：

| 需新增条目 | 许可证类型 | 说明 |
|---|---|---|
| `marked.js` vX.Y.Z | MIT | Markdown 渲染库 |
| `highlight.js` vX.Y.Z | BSD-3-Clause | 代码语法高亮（如选 highlight.js） |
| 或 `Prism.js` vX.Y.Z | MIT | 代码语法高亮（如选 Prism.js） |

---

## 5. 远程访问场景分析

### 5.1 认证链

当前认证体系：`mw_token` HttpOnly cookie + `SameSite=Lax`。

**预览窗口的认证流程**：

```
主页面（已认证）
    │
    ├── window.open('/preview?...', '_blank')
    │       │
    │       └── 同站导航，浏览器自动携带 mw_token cookie
    │
    ▼
预览窗口加载 /preview
    │
    └── withAuth 中间件检查 cookie → 通过 → 返回 preview.html
            │
            └── preview.html 中 fetch('/api/worktree/file/...')
                    │
                    └── 浏览器自动携带 mw_token cookie → API 调用通过认证
```

**关键分析**：
- **无需 URL 中传递 token**——cookie 自动携带，避免了 token 泄露到浏览器历史记录
- **无需预览窗口独立登录**——它共享主页面的认证状态
- **环回请求自动放行**——本地开发时完全无感

### 5.2 跨域场景限制

- Portal 端口与主应用端口不同 → 跨域 → **不能直接在其他端口打开预览**
- 如果通过 Portal Dashboard 访问主应用，预览窗口的新窗口将在主应用端口打开 → **不跨域**
- Tailscale Serve 场景：`https://xxx.ts.net` → 主应用端口 → 预览窗口同域 → **正常**

### 5.3 多个 `mw` 实例的场景

同一台机器可能运行多个 `mw` 实例（不同项目、不同端口）。预览窗口的 URL 是相对路径 `/preview`，因此始终在同一实例内打开——不会跨实例混淆。

---

## 6. 边界情况与异常处理

### 6.1 文件层面的边界情况

| 场景 | 预期行为 | 由哪层处理 |
|---|---|---|
| **文件在工作区中不存在** | Diff API 返回空或 git 错误；Content API 返回 404 | 后端 |
| **文件是二进制** | Content API 拒绝，返回错误；Diff API 返回 `Binary files differ` | 后端检测 + 前端展示 |
| **文件超过 1MB** | Content API 拒绝，前端显示"文件过大"提示 | 后端限制 + 前端友好提示 |
| **文件是敏感文件**（`.env` 等） | Content API 拒绝，返回"此文件类型不支持预览" | 后端黑名单 |
| **文件被删除（unstaged delete）** | Diff API 可正常返回删除 diff；Content API 返回 404 | 后端 + 前端区分展示 |
| **新增文件（untracked / staged new）** | Diff API 返回完整文件内容作为新增；Content API 正常读取 | 后端 |
| **路径包含特殊字符**（空格、中文） | URL query 参数需 encodeURIComponent；Git 命令正确引用 | 前端编码 + 后端 `--` 分隔符 |

### 6.2 窗口层面的边界情况

| 场景 | 预期行为 |
|---|---|
| **用户打开同一文件的多个预览窗口** | 各自独立，互不干扰 |
| **用户在主页面切换工作区** | 已打开的预览窗口**不受影响**（不依赖父窗口） |
| **用户刷新预览窗口** | 重新加载 preview.html，重新发起 API 请求 |
| **git 状态在预览期间变化** | 预览窗口展示的是打开时刻的快照，不实时更新 |
| **网络断开** | fetch 失败，前端显示错误提示 |
| **API 超时**（2 秒） | 后端返回 504，前端显示"请求超时" |

### 6.3 浏览器兼容性

- `window.open`：所有现代浏览器均支持，无兼容性问题
- `URLSearchParams`：所有现代浏览器均支持
- `fetch` API：所有现代浏览器均支持
- **不支持 IE11**（符合项目现有定位——macOS + 现代浏览器）

---

## 7. 安全纵深分析

### 7.1 攻击面分析

| 攻击向量 | 风险等级 | 方案 A 的缓解措施 |
|---|---|---|
| **路径穿越**（`path=../../etc/passwd`） | 🔴 高 | 后端 `filepath.Join` 自动清理 + 显式 `isPathWithin` 验证 |
| **任意文件读取**（`path=.env`） | 🔴 高 | 文件扩展名黑名单 + 路径关键字过滤 |
| **信息泄露**（URL 中携带敏感参数） | 🟡 中 | 不使用 URL 传 token；文件路径不敏感 |
| **XSS**（Markdown 中的恶意脚本） | 🟡 中 | marked.js 默认 sanitize + 配置 `sanitize: true` |
| **未授权 API 调用** | 🔴 高 | 自动继承 `withAuth` 中间件，无新增风险 |
| **CSRF** | 🟢 低 | GET 请求 + cookie 认证，无状态变更 |

### 7.2 纵深防护层

```
第一层：路径验证（isPathWithin）
    └── 确保请求的文件在工作区目录内

第二层：敏感文件过滤（isSensitiveFile）
    └── 拒绝 .env / .pem / 含 secret 关键字的文件

第三层：大小限制（1MB）
    └── 防止读取超大文件导致 OOM

第四层：二进制检测（isBinaryData）
    └── 检查 null 字节，拒绝二进制文件

第五层：认证继承（withAuth）
    └── 所有 /api/* 请求自动经过认证
```

每一层独立生效，任一层拦截即可阻止不安全操作。

---

## 8. 实施路径

| 阶段 | 工作内容 | 产出 | 预估时间 |
|---|---|---|---|
| **Phase 1** | 后端：新增 Diff API + Content API + 路径验证 + 公共函数提取 | ~130 行 Go | 1-2h |
| **Phase 2** | 前端：创建 `preview.html`（标题栏 + Tab UI + API 调用 + 集成高亮库）+ vendor 打包 + `ui.go` 路由注册 + 更新 `index.html` Open Source Licenses 列表 | ~300 行 HTML/JS + ~10 行 Go + vendor 文件 | 3-4h |
| **Phase 3** | 前端：在 `index.html` 的 `renderGitChanges` 中添加点击事件 + `openFilePreview` 函数 | ~30 行 JS | 0.5h |
| **Phase 4** | 安全：敏感文件黑名单 + 大小限制完善 + 错误提示优化 | ~50 行 Go | 1h |
| **Phase 5** | 测试：单元测试 + 集成测试 + 边界情况验证 | ~150 行测试 | 2-3h |

### 实施策略建议

- **独立可测试**：每个 Phase 完成即可验证，不依赖后续 Phase
- **安全与功能并行**：Phase 1 已包含核心安全（路径验证），Phase 4 是加强层

---

## 9. 关键决策总结

| 决策点 | 选择 | 理由 |
|---|---|---|
| **窗口方案** | 方案 A（新窗口 + embed HTML） | 与 `index.html` 架构一致；URL 可分享；cookie 自然共享；不依赖父窗口 |
| **数据传递** | URL query 参数 | 无状态、可刷新、可书签；预览窗口无需回调父窗口 |
| **Tab 渲染** | Diff 用代码高亮，Content 按文件类型分派 | Diff 不是 Markdown；两个 Tab 共享同一个代码高亮库 |
| **依赖引入** | 本地 vendor 打包 | 与现有 xterm 一致；避免 CDN 依赖和 CSP 冲突 |
| **认证方式** | 自动继承 cookie 认证 | 无需在 URL 中传 token；无需预览窗口独立登录 |
| **Diff 渲染** | 代码高亮库（highlight.js / Prism.js `diff` 语言） | token 级着色，7+ 种元素识别，效果对标 GitHub |
| **Markdown 渲染** | marked.js | 客户端渲染，与项目 vendor 模式一致 |

---

## 10. 结论

**方案 A 完全可行，技术风险低。** 该方案在架构模式、安全机制、认证体系上与现有系统完全兼容。核心改动集中在新 API（2 个 handler）和新 HTML 模板（1 个文件），对现有代码的侵入量极小。推荐按 Phase 1-5 依次实施，Phase 2 一次性完成格式化渲染能力集成。
