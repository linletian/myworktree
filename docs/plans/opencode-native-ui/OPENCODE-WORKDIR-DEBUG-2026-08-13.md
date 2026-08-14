# opencode-web 工作目录与会话显示问题 — 最终结论与修复记录

> **状态：已定稿（2026-08-14）**。本文档整合多轮分析、实施、评审后的**最终正确结果**；
> 过程性假设（早期"HOME 视图化"推断等）与中间错误判断已剔除，不再保留推导过程。
> 关联文档：`WORKTREE-ISOLATION.md`（方案设计）、`FOLLOWUPS.md`（后续优化/待办清单）。
> 适用版本：opencode 1.18.16 / 1.18.18（两版本关键逻辑逐项比对一致）。

---

## 1. 现象与最终根因

### 1.1 现象

1. **越界警告**：新会话触发 `⚠ opencode 已离开 worktree 范围: /home/linletian/projects/myworktree`。
2. **新会话找仓库**：agent 首条命令在 `/home/linletian/projects/myworktree`（非 git 仓库，仅含 `.codegraph/`）执行，需自行寻找真实 worktree。
3. **会话历史不完整**：实例内新会话看不到近期会话；同目录原生 `opencode web` 可见全部。
4. **搜索栏显示 URL**：显示 `在 <IP:Port>/__opencode/<id> 中搜索会话` 而非项目名。

### 1.2 根因（定稿）

**核心：`opencode.db` 中 project 表存在陈旧 `worktree` 字段，经前端项目恢复逻辑传播为请求的 `x-opencode-directory` header，server 依 header 把 agent 切到该（已废弃的）目录。**

**幽灵目录来历**：`/home/linletian/projects/myworktree` 是 project `a6e112c1ae884ba2e286275480e04b64179d5839`（myworktree 仓库项目）的 `worktree` 字段**陈旧值**——2026-06-19 首次初始化时该目录是仓库真实 checkout（该 project 下 5 个当日会话目录实证），后目录被清空只剩 `.codegraph`，但 `fromDirectory` 对已存在 project **永不更新该字段**，DB 原样保留。

**传播链（源码实证）**：

1. 前端 `GET /project` 返回 DB project 表**全量**（`Project.list` 无过滤），含陈旧行 `a6e112.worktree = /home/linletian/projects/myworktree`；
2. 新建会话默认选中项目：`newSessionProject = projects().find(p => p.worktree === projects.last()) ?? projects()[0]`（`home-controller.ts`）；`lastProject` 为空时取列表第一个；
3. 会话创建显式传目录：`session.create({ ..., location: { directory: sessionDirectory } })`（`submit.ts`），`sessionDirectory` 即选中项目的 worktree；
4. SDK 把目录写进每个请求：`x-opencode-directory: encodeURIComponent(config.directory)`（`v2/client.ts`，POST 也带；GET/HEAD 另由 interceptor 改写为 `?directory=`）；
5. server 目录解析优先级：`?directory= query > x-opencode-directory header > process.cwd()`（`workspace-routing.ts`）——**header 优先于 proxy 注入的 query**；
6. `Session.create` 的 body 无 directory 字段，`directory: ctx.directory`（`session.ts`）→ agent cwd = 幽灵目录。日志实证：当天 34 次 `creating instance directory=/home/linletian/projects/myworktree`。

**TUI 与 web UI 的差异是设计使然**：TUI 启动时 `process.chdir(启动目录)`（`tui.ts`），请求不带 directory 或带 cwd 值，**不经过任何项目恢复状态 → 永远正确**；web UI 的目录来自前端项目状态（serve 按 header 逐请求加载实例）。

**关键推论**：非 myworktree 引入的问题——只要 DB 有陈旧 project 行且前端选中它，**原生 opencode web 同样复现**。

### 1.3 四现象归因表

| 现象 | 最终根因 |
|---|---|
| 1+2 警告条/找仓库 | 请求 header 携带陈旧幽灵目录（优先于 proxy 注入的 query）→ `classify` out-of-scope；agent cwd = 幽灵目录（非 git） |
| 3 会话不完整 | 会话按 `project_id + directory` 分桶（schema 实证）；新会话落 `global + 幽灵目录` 桶，主页列表按请求目录解析出的 project（a6e112）过滤 → 互不可见。非物理丢失 |
| 4 搜索栏显示 URL | `selectedProject=null`（layout 选择态为空）+ 注入使 server 列表为 2 个（entry 的 `location.origin` 与注入的带路径 `pu`）→ 命中"多 server 显示 `serverName`"分支；`displayName` 缺失为次要因素 |

---

## 2. 修复与实施（最终状态）

### 2.1 数据修复（用户执行，已完成）

- **备份**：`.omo/opencode-backup/opencode.db.2026-08-14-01-37-17`（2.23GB，`VACUUM INTO` 一致快照；校验 projects=6/sessions=235/messages=7298 与源一致）。
- **删除+恢复**：删除 project 行 `a6e112` 后（外键级联删 14 会话/341 message/1339 part/21 todo/2 project_directory），按用户决定**恢复全部 14 个会话**，project 行重建时 **`worktree` 修正为真实仓库根 `/home/linletian/SoftwareWorkspace/myworktree`**，幽灵值彻底清除。
- **终验**：project 6/session 235/message 7298/part 32535/todo 637/project_directory 11 与备份一致；**无任何 project 行再指向幽灵目录**；a6e112 挂 14 会话。
- project id 基于 git remote hash 稳定：删除后在任何真实 worktree 解析目录时 `fromDirectory` 自动重建该行。

### 2.2 注入脚本最终形态（`proxy.go` `buildInjectScript`）

- **单 server preseed**：server 条目 URL = `location.origin`（bare origin），与 `entry.tsx` 的 canonical server 同 key → `resolveServerList` 合并为 1 个 → home 页单 server 模式（与原生一致）；`defaultServerUrl` 同步写 origin；项目 preseed 写入 canonical scope `projects['local']`。
- **实例键隔离**：所有实例 iframe 共享同一 origin → 共享 localStorage；把 `opencode.global.dat:server` 一个键按实例重定向到 `opencode.global.dat:server/__opencode/<id>`（劫持该键的 getItem/setItem/removeItem，其余键不受影响）——多实例并存互不覆盖 preseed。
- **displayName**：server 条目带 `displayName = worktree basename`（现象 4 修复；搜索栏 placeholder 显示项目名）。
- **disable 模式**（试验期）：跨 worktree 项目入口（主页项目列表、`Open project` 按钮、session 页 `project-switch` 非当前项）**可见但不可交互**——`pointer-events:none` + `opacity:0.45` + `aria-disabled`；当前 worktree 项保持可交互。L3 报告 `disable-failed`，前端文案"切换入口禁用未生效"。
- **URL 重写全家桶**（SDK baseUrl 为 bare origin 后所有请求经此补前缀）：
  - **`Request` 构造时重写（主路径）**：劫持 `window.Request` 构造——SDK 每个请求都经 `new Request(url, init)`，构造时即把 bare-origin URL 加前缀 → 后续 `fetch(request)` 原样放行，**无重建、无 body/duplex 往返**，与单 server 前路径完全一致（各浏览器一致，含 Safari 对 ReadableStream 的严格处理）；
  - 兜底劫持 `fetch`（字符串/`Request`/`URL` 对象三类输入）、`XMLHttpRequest.open`、`EventSource`、`Worker`、`navigator.sendBeacon`、`WebSocket`（防御未来）；
  - `r()` 覆盖：已含前缀直通、同源绝对路径、根相对路径、`http://127.0.0.1` 特例、`ws://`/`wss://` 同源；
  - `ri()` 的 `Request` 重建仅作兜底（显式复制 `method/headers/body/...` + `duplex:'half'`，失败回退原 Request 并 `console.warn`）——曾在 Safari 触发 `ReadableStream uploading is not supported`，故重建不再是主路径。
- **jsQuote 转义**：所有插值经 `strconv.Quote` + `<`→`\u003c`，防 script-tag breakout。

### 2.3 proxy 最终形态

- **仅监测，不做目录锁定**（曾实施"全方法强制覆盖 directory"后回退：锁定过度干涉 opencode 自身跨目录业务，opencode 应保留 agent `cd`、跨 worktree 会话能力，与终端实例一致——提醒而非禁止）。
- `ScopeTracker` 记录每次请求携带的 directory，`classify` 判定 in/out-of-scope；越界时前端常驻警告条提醒。
- `Director` 保留原始契约：GET/HEAD 且无 directory 的 API 请求注入 `?directory=<worktree>` 作为默认提示。

### 2.4 门户 UI 最终形态（`index.html` / `opencode_web.js`）

- **串台修复**：`deactivate()` 隐藏当前 iframe（keep-alive 语义不变）；`activate()` 仅对 `stopped` return，starting 继续 poll、ready 自动导航。
- **loading 层**（`#opencode-loading`）：starting 显示"正在启动 opencode server…"（instance id、elapsed），超时显示错误，stopped 显示"已停止"。
- **移除底部多行状态栏**（`.opencode-debug-bar`）：删除 `_ensureDebugBar` 及全部触点；状态信息由 loading 层承担，ready 后 iframe 全高。
- **快捷按钮按 kind 隐藏**：`#terminal-controls`（刷新 + 到底）对 `opencode-web` 实例隐藏（`oc-hidden` class），终端实例保留；无活动实例时也隐藏。

### 2.5 测试形态（`proxy_test.go` / `proxy_handler_test.go`）

- `TestBuildInjectScript`：注入脚本关键片段断言；
- `TestBuildInjectScriptSyntax`：`node --check` 语法校验（CSP hash 自动重算由 `TestFixProxyHTMLCSPAnchor` 覆盖）；
- `TestBuildInjectScriptSingleServer`：node VM mock 浏览器环境执行注入脚本，**双实例模拟**（A 先 B 后）断言共享键从未写入、各实例 preseed 自己的 worktree、互不覆盖、`resolveServerList` 合并后均单 server；
- `TestInjectURLRewriteBehavior`：行为级重写验证——fetch（字符串/URL 对象/带流式 body 的 POST）、XHR、EventSource、sendBeacon、WebSocket 全部加前缀、远程 URL 原样；**读回重建后 Request 的 body** 断言流可读且内容完整（`fetch-body:hello`，body 丢失时记录 `fetch-body:MISSING`）；
- `TestBuildInjectScriptEscapes`：`</script>`/`<!--` 断出防护；
- `TestProxyHandlerScopeAndInject`：proxy 监测与注入行为。

---

## 3. 验证结果（已确认）

- **DB 恢复后**：无任何 project 行指向幽灵目录；a6e112 `worktree` = 仓库根。
- **新会话定位正确**（DB 实证）：新会话 `directory = /home/linletian/orca/workspaces/myworktree/opencode-native-ui`（实例 worktree），无漂移。
- **越界提示来源已确认（正常无害）**：新建会话页的 worktree 选择器用 `sync().project?.worktree`（仓库根，opencode 项目级主目录概念）查询分支 → 产生一次 `directory=仓库根` 的请求 → proxy 如实判 out-of-scope → 警告短暂亮起；发送第一条消息后覆盖为 in-scope，警告熄灭。
- **多实例会话按目录隔离**：同仓库不同 worktree 实例本就按 `project_id + directory` 分桶显示（`session.list` 双条件过滤），前端项目名差异来自各实例注入脚本 preseed 的 worktree（enrich 覆盖）。

**观察中（试验期）**：disable 模式效果（L3 报告数据）；越界提示可选优化（接受现状/恢复时设为当前 worktree/proxy 宽容同仓库目录——见 FOLLOWUPS.md）。

---

## 4. 已知限制

| 限制 | 说明 |
|---|---|
| markdown 根相对图片（`![](/path)`） | 解析到裸 origin → 404；属用户内容，与原生子路径部署行为一致；前端不动态创建根相对资源 |
| 多实例极端竞态 | 实例键隔离后，两实例同时刷新（并行加载）时 localStorage 最终值 = 最后完成者；各实例内存 store 不受影响，下次刷新自愈 |
| 旧 localStorage 孤儿数据 | `projects[origin+p]` 旧 key 与旧裸键残留，不自动清理（裸键可能含用户原生 opencode 数据） |
| WS/beacon 劫持为防御性 | SDK 现用 SSE；未来切换协议需验证 |

---

## 5. 后续优化

统一维护在 **[FOLLOWUPS.md](FOLLOWUPS.md)**：注入脚本提取为独立 `.js` 资源（含命名清理与历史薄弱路径保护）、Playwright 真浏览器冒烟测试、旧数据清理策略、观察期决策项、端到端验证清单。

---

## 附录：opencode 源码关键机制速查（供未来排障）

| 机制 | 位置 |
|---|---|
| 目录解析优先级 `query > header > cwd` | `packages/opencode/src/server/routes/instance/httpapi/middleware/workspace-routing.ts` |
| `Session.create` 用 `ctx.directory`（body 无 directory 字段） | `packages/opencode/src/session/session.ts` |
| serve 按 header 逐请求加载实例 | `packages/opencode/src/cli/cmd/serve.ts`（注释明确） |
| TUI `process.chdir(启动目录)` | `packages/opencode/src/cli/cmd/tui.ts` |
| `Project.list` 全表返回 | `packages/opencode/src/project/project.ts` |
| SDK 全局 `x-opencode-directory` header（encodeURIComponent） | `packages/sdk/js/src/v2/client.ts` |
| 会话创建显式传 `location.directory` | `packages/app/src/components/prompt-input/submit.ts` |
| 新会话默认项目 `last() ?? projects()[0]` | `packages/app/src/pages/home/home-controller.ts` |
| server 列表 `resolveServerList`（按 URL 去重合并 props+stored） | `packages/app/src/context/server.tsx` |
| canonical server = `location.origin` | `packages/app/src/entry.tsx` |
| home 页单/多 server 渲染分支 `servers().length > 1` | `packages/app/src/pages/home/home-projects-view.tsx` |
| myworktree 注入脚本 / ScopeTracker / 监测 | `internal/instance/opencode_web/proxy.go`、`scope.go` |
| myworktree 门户 UI（loading 层、controls、iframe 管理） | `internal/ui/static/index.html`、`internal/ui/static/kinds/opencode_web.js` |
