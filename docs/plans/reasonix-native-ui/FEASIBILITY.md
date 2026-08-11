# reasonix-native-ui — 可行性分析报告

**调研日期**: 2026-08-10
**目标项目**: https://github.com/esengine/DeepSeek-Reasonix(reasonix)
**上游官方文档**: https://reasonix.io/docs/#cli
**调研人**: linletian
**状态**: 调研完成,待决策

> **修订记录(2026-08-10 整合)**:初版经二次源码核实后修订——修正 2 处事实错误(desktop 壳是 Wails 非 Tauri;token 鉴权走 Cookie/query 而非 Bearer header)、2 处表述偏差(端口以 `--port-file` 为准;vanilla HTML 根相对 fetch 仍需轻量 URL 改写),补充布局问题与注入方案(§9)、session lease、sandbox、provider-setup CSP、托管 flag、无 `?directory=` 概念等。修订点均以「整合」标记。

---

## 0. 需求背景

### 0.1 现状

myworktree 当前对 AI coding agent 的支持是 **PTY + xterm.js 跑 agent TUI**(Claude Code / opencode TUI / reasonix TUI 都用同一套机制):

- 每个 instance = 1 个 `script` 托管的伪终端
- WebSocket / SSE / polling 三档降级把 PTY 字节流搬到浏览器
- xterm.js 渲染 VT 输出

### 0.2 已知痛点(已在多份文档中记录)

| 痛点 | 出处 |
|---|---|
| OSC/DA 查询回声污染 TUI 输出 | `docs/TERMINAL_FILTER_REVIEW.md:45-54` |
| 中文 IME / 复杂脚本渲染异常 | `docs/CHINESE_IME_ANALYSIS.md` |
| TUI 重绘 CPU 高 | `docs/GHOSTTY_WEB_RESEARCH.md` §2.1 |
| 键鼠跨 PTY 往返脆弱 | 同上 |
| xterm.js 的 `script -qfc` 输出擦写问题 | `fix/top-cli-exited-abnormal-char` 系列 commit |

### 0.3 期望效果

把 reasonix 的官方 web dashboard 嵌进 myworktree 实例面板,作为新的 instance **Kind**(`reasonix-web`),与现有 PTY 实例并列:

```
┌─ myworktree 实例面板 ─────────────────────────────────┐
│ Tab: [terminal-pty]  [reasonix-web]  [+ new instance] │
├──────────────────────────────────────────────────────┤
│                                                       │
│   reasonix 官方 web UI (iframe)                      │
│                                                       │
└──────────────────────────────────────────────────────┘
```

类似 `feature/opencode-native-ui` 分支对 opencode 做的那样,但本次目标是 reasonix。

---

## 1. reasonix 项目概览

### 1.1 基本形态

- **GitHub**: https://github.com/esengine/DeepSeek-Reasonix
- **官网**: https://reasonix.io
- **定位**: "A coding agent you can leave running" — 前缀缓存稳定、长时自治运行、checkpoint/undo
- **License**: MIT
- **语言**: Go 1.25+(后端) + React 19 + Vite(仅 desktop 壳)+ TypeScript(部分 SDK)
- **发布形态**: 单 Go 二进制 + 桌面壳(dmg/zip/exe/deb)

### 1.2 顶层目录

```
cmd/
├── reasonix/                  # 主 CLI 入口(main.go)
├── reasonix-launcher/         # 启动器
├── reasonix-legacy-migrator/  # 老版本迁移
├── reasonix-plugin-example/   # 插件示例
├── e2ebench/                  # 端到端 benchmark
├── extension-protocol-gen/    # 扩展协议生成器
└── signpath-contract/         # 签名契约

internal/  (80+ 包,Go 主体)
├── serve/        # ★ web server 实现 (本章重点)
├── cli/          # CLI 子命令 + serve_frontend.go
├── control/      # transport-agnostic controller
├── event/        # 事件模型
├── session*/     # 会话状态机
├── agent/        # agent 循环
├── provider/     # 模型 provider 抽象
├── mcp*/         # MCP 集成
└── ...

desktop/         # Wails + React 19 + Vite 桌面壳(与 web server 无关;初版误记为 Tauri,已修正)
site/            # 官网静态站(reasonix.io)
sdk/go/          # Go SDK
npm/             # npm 包发布基础设施
```

### 1.3 关键认知修正

**误判一(整合修正)**:~~desktop/ 是 Tauri + React~~。实际是 **Wails + React**(`wails.json`、`wails build`、desktop/README.md 原文 "Reasonix Desktop (Wails shell)")。初版把正确的 Wails 误"修正"成了 Tauri,方向反了,本版改回。
**误判二(已确认)**:reasonix web UI = Tauri SPA。实际:`internal/serve/` 是 **纯 Go `net/http` server**,前端是 `//go:embed` 的 vanilla HTML+CSS+JS(index.html 单文件,无 SPA 框架、无动态 import、无外部 bundle)。`desktop/` 是独立产品线,不与 web server 共享前端代码。
**补充(整合)**:虽然不是 SPA,但 index.html 的 22 个 API 请求 + `EventSource('/events')` 全部是**根相对路径**(`fetch('/history')` 等),反代挂子路径时仍需**单层轻量 fetch/EventSource 前缀改写**(~15 行),不能假设"vanilla HTML 就零处理"。详见 §2.7、§9。

---

## 2. reasonix serve 模式分析(关键)

### 2.1 CLI 入口(官方文档摘录)

来自 https://reasonix.io/docs/#cli:

| 命令 | 用途 | 关键 flag |
|------|------|-----------|
| `reasonix web` | 本地浏览器 UI,`127.0.0.1:8787`,端口占用时自动递增,自动生成 token | `--model`, `--max-steps`, `--resume` |
| `reasonix serve` | 监督式 / 远程 HTTP+SSE 前端,支持 token/password 鉴权 | `--addr 0.0.0.0:8787`, `--auth token\|password`, `--password`, `--hash-password` |
| `reasonix` | 交互式 TUI(默认) | — |
| `reasonix run "<task>"` | 无头模式,writer 默认 fail-closed | `-y`, `--auto`, `--permission-mode auto` |
| `reasonix bot start` | IM 网关(飞书/企业微信) | `--channels feishu,lark,weixin --dir /path` |
| `reasonix acp` | 编辑器 ACP 协议后端 | `--model`, `--profile` |

### 2.2 配置文件

```toml
[serve]
auth_mode    = "password"   # none | token | password
password_hash = "$2a$12$..."
behind_proxy = true         # 仅在受信反向代理后启用
```

环境变量:`REASONIX_DISABLE_MOUSE=1`,`REASONIX_HOME=...`

### 2.3 源码确认

通过 GitHub API 列出 `internal/serve/` 实际文件:

```
auth.go                       # authGate (none/token/password)
auth_fragment_test.go
auth_test.go
broadcaster.go                # SSE broadcaster
broadcaster_test.go
csrf_test.go                  # CSRF guard
index.html                    # //go:embed
login.html                    # //go:embed
logo-wordmark.svg             # //go:embed
provider_setup.html           # //go:embed
serve.go                      # 路由 + Server struct
serve_*.go                    # 各种测试
titlecache.go
web_entry_test.go
```

### 2.4 宿主式集成 flag(整合补充)

`internal/cli/serve_frontend.go` 为"监督式宿主"(如 myworktree)预留了以下 flag,这是本次调研最关键的发现之一:

| flag | 作用 |
|---|---|
| `--addr` | 监听地址,默认 `127.0.0.1:8787`;`reasonix web` 端口被占时自动 +1 重试最多 100 次 |
| `--port-file` | **绑定后把实际 host:port 写入该文件**——端口冲突自动递增时仍是权威值,应作为端口获取的唯一途径(初版"stdout 扫 listening 行"不可靠:reasonix 打印的是 `reasonix serve — <label> on http://<addr>`,格式与 opencode 的 `listening on` 不同) |
| `--token-file` | 预共享 token 文件(须 `chmod 600`),避免 token 进 argv / 日志 |
| `--pid-file` / `--behind-proxy` | 进程 pid 落盘 / 信任 X-Forwarded-* 头 |

另注意:`reasonix web` 默认 `--auth token` 且自动生成 token;`reasonix serve` 默认走 config(none)。嵌入场景推荐 `serve --auth token --token-file <file> --port-file <file> --no-open`。

### 2.5 HTTP 路由(从 serve.go 提取)

```go
// Server.Handler() 返回完整路由表
mux.HandleFunc("GET /",                    s.index)
mux.HandleFunc("GET /sessions/{id}",       s.index)              // ★ 会话路由,显式
mux.HandleFunc("GET /assets/logo-wordmark.svg", s.logoWordmark)
mux.HandleFunc("GET /provider-setup",      s.providerSetupStatus)
mux.HandleFunc("POST /provider-setup",     s.providerSetupSave)
mux.HandleFunc("GET /events",              s.events)             // ★ SSE
mux.HandleFunc("GET /history",             s.history)
mux.HandleFunc("GET /context",             s.context)
mux.HandleFunc("POST /submit",             s.submit)
mux.HandleFunc("POST /cancel",             s.cancel)
mux.HandleFunc("POST /approve",            s.approve)
mux.HandleFunc("POST /plan",               s.plan)
mux.HandleFunc("POST /compact",            s.compact)
mux.HandleFunc("POST /new",                s.newSession)
mux.HandleFunc("POST /rewind",             s.rewind)
mux.HandleFunc("POST /fork",               s.fork)
mux.HandleFunc("POST /summarize",          s.summarize)
mux.HandleFunc("POST /tool-approval-mode", s.toolApprovalMode)
mux.HandleFunc("POST /auto-approve-tools", s.autoApproveTools)
mux.HandleFunc("POST /bypass",             s.bypass)
mux.HandleFunc("POST /goal",               s.goal)
mux.HandleFunc("POST /answer",             s.answer)
mux.HandleFunc("POST /resume",             s.resume)
mux.HandleFunc("POST /forget",             s.forget)
mux.HandleFunc("GET /checkpoints",         s.checkpoints)
mux.HandleFunc("GET /branches",            s.branches)
mux.HandleFunc("GET /models",              s.models)
mux.HandleFunc("POST /extensions/reload",  s.reloadExtensionsHTTP)
mux.HandleFunc("GET /status",              s.status)
mux.HandleFunc("GET /sessions",            s.sessions)
mux.HandleFunc("GET /skills",              s.skills)
mux.HandleFunc("GET /todos",               s.todos)
mux.HandleFunc("POST /delete-session",     s.deleteSession)
```

### 2.6 Server struct 关键字段

```go
type Server struct {
    ctrl          control.SessionAPI     // transport-agnostic controller
    bc            *Broadcaster           // SSE sink
    auth          *authGate              // 启用鉴权时非 nil
    leases        *control.SessionLeaseKeeper  // 多 runtime 互斥
    // ...
}

//go:embed index.html
var indexHTML []byte
```

注释中关键事实(从源码直接抄录):
> "Package serve exposes a control.Controller over HTTP: the typed event stream as Server-Sent Events, and the commands as small JSON POST endpoints. It is a second frontend alongside the chat TUI — proof that the controller is transport-agnostic, and the basis for a browser/desktop client. **One server drives one session; multiple browser tabs share it.**"

### 2.6.1 运行前提(整合补充)

- **session lease**:同机已有 reasonix 会话(桌面窗口 / 另一 CLI / 另一 serve)时,新 serve 会报 `session is in use`(SessionLeaseKeeper 互斥,实测复现)。v1.22.0 的 serve 无 `--session-id`(main 分支源码有,存在版本差异),嵌入前需验证同机多实例并发策略。
- **bash sandbox**:reasonix ≥1.16 默认要求 `bwrap`,无则 agent 的 shell 工具拒绝执行(报 "refusing to run unconfined");serve 本身可启动,但需在 reasonix 配置 `[sandbox] bash = "off"` 或部署 bwrap(实测本机缺 bwrap)。
- **健康探针**:无 `/health`,但 `GET /status` 返回 running/goal/cwd/context 等,可作探针。
- **CSRF**:状态变更 POST 必须是 `application/json`,否则 415(Content-Type 校验,非 Origin/Referer 校验)。同源 iframe 反代场景下前端本就发 JSON,**不冲突**(csrf_test.go 注释明言 "same-origin frontend always sends JSON and is unaffected")。cookie `SameSite=Lax`,同源请求正常携带。
- **鼠标捕获**:**不适用**。`REASONIX_DISABLE_MOUSE` 只影响 TUI(chat_tui.go);web UI index.html 无任何 mouse 监听,不存在"web UI 捕获鼠标影响 myworktree"的问题(初版 §6 担忧不成立)。

### 2.7 前端 index.html 形态

- 引用 Google Fonts(DM Sans / Space Grotesk / JetBrains Mono)
- CSS 用 OKLCH 颜色空间,深色主题
- 自包含 HTML + 内联 JS(由头部 `<style>` 块推测,**不是** 外部 bundle)
- 无任何框架依赖(无 React/Vue/Solid)
- **fetch 全为根相对路径**(整合核实):`fetch('/history')`、`EventSource('/events')`、`post('/submit')` 等 22 个 API 端点 + `src="/assets/logo-wordmark.svg"` 1 处 asset;无 XHR、无动态 import
- **主页面无 CSP**:serve.go 的 index handler 纯 HTML 输出,无 `Content-Security-Policy` / `X-Frame-Options` → iframe 嵌入与 inline 脚本注入均无障碍
- **唯一 CSP 障碍**:`/provider-setup` 页(provider 首次配置)带 `frame-ancestors 'none'`(provider_setup.go:100)→ 该页不能被 iframe 嵌入,需提示用户新窗口打开完成一次全局配置(配置是全局的,仅首次)
- 引用 Google Fonts(离线降级系统字体,不影响功能)

---

## 3. myworktree 实例界面架构(嵌入目标)

### 3.1 当前形态

- 单 Go 二进制,`internal/ui/ui.go` 用 `//go:embed static/*` 把前端打进 binary
- `static/index.html` 3704 行 vanilla JS 单文件 SPA
- xterm.js v6.0.0 + xterm-addon-fit 走 `/static/vendor/*`

### 3.2 路由表

```
/                  主 SPA(index.html)
/preview           文件预览 SPA(preview.html)
/static/*          静态资源

/api/worktrees                              GET
/api/worktrees/unmanaged                    GET
/api/worktrees/import                       POST
/api/worktrees/delete                       POST
/api/worktree/status                        GET
/api/worktree/file/diff                     GET
/api/worktree/file/content                  GET
/api/worktree/diverged                      GET
/api/worktrees/diverged                     GET
/api/instances                              GET
/api/instances/reorder                      POST
/api/instances/stop                         POST
/api/instances/restart                      POST
/api/instances/delete                       POST
/api/instances/input                        POST
/api/instances/tty/ws                       WebSocket  ★ PTY 流
/api/instances/log                          GET
/api/instances/log/stream                   GET(SSE) ★ 日志 SSE
/api/instances/stats                        GET
/api/tags                                   GET
/api/tags/open-dir                          POST
/api/branches                               GET
/api/worktrees/open-terminal                POST
/api/worktrees/open-finder                  POST
/api/mcp/tools                              GET
/api/mcp/call                               POST
/api/main                                   GET
/api/llm/config                             GET/POST
/api/llm/test                               POST
/api/llm/generate                           POST
/login                                      GET/POST
```

### 3.3 实例界面挂载点

DOM 结构(`internal/ui/static/index.html`):

```
#workspace
├── #instance-actions(工具栏,hidden by default)
├── #empty-state("Select a worktree and instance to start")
├── #terminal-container(xterm.js 挂载点)
└── #terminal-controls
```

PTY 实例通过 `appendChild(xtermHost)` 把 xterm 实例挂到 `#terminal-container`。要嵌入 reasonix-web 需要新增挂载点或条件渲染。

### 3.4 PTY/WS 协议

- WS URL: `/api/instances/tty/ws?id=<id>`
- 消息: JSON `{type:"resize", cols, rows}` / `{type:"input", data}` 等
- 降级: SSE → polling
- 状态显示: 顶栏 `websocket/sse/polling` + 重连按钮

---

## 4. 复盘:`feature/opencode-native-ui` 分支

### 4.1 分支状态

远程 `origin/feature/opencode-native-ui` 共 7 个相关 commit + 1 个 base:

```
b41cd84 docs: add opencode-native-ui feasibility and plan
496be94 docs: add opencode-web spec to PRD/API/ARCH/CHANGELOG
0a9e88e feat(instance): add Kind/Extra fields, opencode-web helpers, default tag
7e8ea49 feat(instance): add opencode-web Kind dispatch in Manager.Start
420b940 feat(api): add opencode-web endpoint + /__opencode/ reverse proxy
134d3e2 feat(ui): add opencode-web iframe panel + OC badge in tabs
aa405e0 feat(opencode-web): embed opencode SPA via reverse proxy + iframe
```

整体 ~10k 行代码增量,不是"空研究分支"。

### 4.2 已实现的架构(可复用部分)

| 模块 | 文件 | 抽象价值 |
|------|------|---------|
| `framework.Kind` 接口 + Manager 路由 | `internal/framework/kind.go`, `manager.go` | **100% 通用**,加新 Kind 零改动 manager 主体 |
| `PTY` driver 抽取 | `internal/instance/pty/driver.go` | 与本次无关,但证明包独立性原则 |
| reverse proxy 基本模式 | `internal/instance/opencode_web/proxy.go` | Director / ModifyResponse / ErrorHandler 模式通用 |
| iframe 在实例面板的 UI 集成 | `internal/ui/static/kinds/opencode_web.js` | Kind 分支 + tab 切换模式通用 |
| `framework.js` 共享前端 framework | `internal/ui/static/framework.js` | tab/badge/状态同步通用 |

### 4.3 未调试成功的原因(`docs/plans/opencode-native-ui/DEBUG.md`)

**10 个问题,前 8 个已修,iframe 现在能渲染 opencode SPA。剩 2 个尾巴**:

| # | 问题 | 状态 | 备注 |
|---|------|------|------|
| 1 | iframe 加载 opencode **首页**而非 session 页 | ✅ 修 | `iframe_src` 补 `/{base64(worktree)}/session` |
| 2 | API 请求 404(SPA 以 `location.origin` 为 baseUrl) | ✅ 修 | 注入三层 shim |
| 3 | CSP 拦截内联脚本 | ✅ 修 | SHA-256 hash 注入 CSP header |
| 4 | SSE 长连接超时(`new Request()` 丢 `timeout` 属性) | ✅ 修 | 手动复制 `q.timeout = i.timeout` |
| 5 | localStorage 残留旧 server URL | ✅ 修(后撤销) | 见 #8 |
| 6 | 502 Bad Gateway(proxy 连错端口) | ✅ 修 | opencode 双端口问题,改连 4096 |
| 7 | **iframe 空白**(SPA Router 不匹配 4 段路径) | ✅ 修 | `history.replaceState()` 剥前缀 |
| 8 | `server.v3` 清洗导致空白页(撤销了 #5) | ✅ 修 | 撤销清洗 |
| **9** | **`ReadableStream uploading is not supported`** | 🔴 未修 | Safari 兼容,`new Request()` 复制 ReadableStream body 失败 |
| **10** | **左侧 sidebar 项目路径不对** | 🔴 未修 | `?directory=` 注入可能没覆盖所有路径 |

### 4.4 对本次 reasonix 调研的可借鉴性

| opencode-native-ui 部分 | 可复用度 | 原因 |
|---|---|---|
| `framework.Kind` 接口 | **100%** | 通用设计,加 `reasonix-web` 不动 manager |
| Manager 注册流程 | **100%** | 同上 |
| reverse proxy Director / ModifyResponse 模式 | **90%** | 模式通用,但具体 URL/auth 注入要重写 |
| `<base>` 标签 + 绝对路径重写 | **80%** | 通用,但 reasonix 不一定需要(单文件 HTML) |
| 三层 SPA shim(history + localStorage + fetch 重写) | **0%** | 解决的是 opencode Solid.js SPA 特定问题,reasonix 是 vanilla HTML |
| CSP hash 修补 | **0%** | reasonix 没设 CSP |
| 双端口解析 | **0%** | reasonix 用 `--addr` 单端口 |
| `OPENCODE_SERVER_PASSWORD` 注入 | **0%** | reasonix 用 `--auth token --token <...>` |
| `?directory=<worktree>` 注入 | **0%** | reasonix **没有** directory 概念:会话按 serve 启动目录解析的项目级 SessionDir 隔离(resolveCLISessionDir,git-root 检测),UI 无跨项目/切目录入口(整合核实,见 §9.3) |

### 4.4.1 初版误判修正(整合)

初版写"reasonix 是 vanilla HTML,三层 SPA shim 0% 可复用"——方向对但**漏了一层**:vanilla HTML 的根相对 fetch 在反代子路径下同样会打回 origin 根,仍需**单层轻量 fetch/EventSource 前缀改写**。结论修正为:**三层 shim(路由剥离 / localStorage / 复杂 URL 重写)0% 可复用;单层 URL 前缀改写 100% 需要自研,但实现量极小(~15 行,无 CSP 障碍)**。

### 4.5 总体评价

`feature/opencode-native-ui` 分支:
- **架构层(40-50% 工作量):可直接复用** — Kind 抽象 + proxy 模式 + UI 集成
- **协议层(50-60% 工作量):不能复用** — opencode vs reasonix 路由、鉴权、流式协议完全不同
- **遗留问题**:Safari ReadableStream + 左侧 sidebar 是 opencode-specific,reasonix 不会遇到

**结论**:该分支对我们有借鉴意义,但**不应作为 reasonix 的技术主线**。架构抽象可学,具体实现要从零写。

---

## 5. 嵌入方案对比

### 5.1 方案 A:`reasonix-web` Kind(reverse proxy + iframe)— **推荐**

**做法**:复用 `framework.Kind` 抽象,新建 `internal/instance/reasonix_web/` driver。

- **driver.go**:Spawn 执行 `reasonix serve --auth token --token <myworktree token> --addr 127.0.0.1:0`(端口 0 让 reasonix 自选),从 stdout 扫 listening 地址
- **proxy.go**:`/__reasonix/<id>/*` → `http://127.0.0.1:<port>/*`,服务端注入 **`Cookie: reasonix_token=<token>`**(token 校验走 Cookie/query,**无 Bearer header 逻辑**,auth.go:304-330;不用 `?token=` 因 query 分支会 302 重定向),`FlushInterval=-1` 透传 SSE;HTML 响应做 §9 的四处注入(折叠 CSS/toggle JS/URL shim/logo 路径)
- **kinds/reasonix_web.js**:iframe `src="/__reasonix/<id>/sessions/<session-id>"`

**优点**:
- 与现有 PTY 实例并列,UI 体验一致
- 进程隔离,reasonix 升级不影响 myworktree
- myworktree 全局 token 复用,无需额外账号体系
- 复用 `framework.Kind` 接口,代码结构与 `opencode-web` 对称

**风险**:
- 同一时刻一个活动会话、多 tab 共享(`One server drives one session`);但 `/new`、`/resume`、`/sessions`、`/delete-session` 说明一个 serve 可管理/切换多个会话历史;多 worktree 隔离仍需要每 instance = 1 个 serve 子进程
- 多 worktree / 多 session 需要每个 instance = 1 个 reasonix serve 子进程(资源占用)
- reasonix web UI 风格可能与 myworktree 不统一(深色 OKLCH 主题)

**工作量**:3-5 天(基于 opencode-web ~480 行 driver.go + ~200 行 proxy.go 估算)

### 5.2 方案 B:MCP-only 集成

**做法**:不嵌入 UI,只把 reasonix 注册为 myworktree MCP provider,让 myworktree 实例里的 LLM agent 能调用 reasonix 工具。

**优点**:
- 改动最小,复用现有 `/api/mcp/call` 框架
- 零前端改动
- 隔离彻底

**风险/缺点**:
- 用户看不到 reasonix UI,体验差
- reasonix `web/serve` 模式被浪费
- 与"嵌入 UI"的原始目标不符

**结论**:**不推荐**。`reasonix serve` 模式已经可用,没必要退而求其次。

### 5.3 方案 C:深度 embed(把 reasonix 前端编译进 myworktree 二进制)

**做法**:编译 `internal/serve/` 的 HTML 资源,作为 `//go:embed` 进 myworktree,前端直接复用 myworktree 后端代理 reasonix。

**优点**:理论上单二进制无 sidecar 依赖

**缺点**:
- reasonix 是单二进制发布,无法单独 embed HTML(它的 HTML 在 `internal/serve/` 里,跟 Go 代码一起编译,不能拆出来)
- 即便能拆,需要给 reasonix 提 PR 或维护 fork
- 工程量大,收益小

**结论**:**不推荐**。

### 5.4 决策矩阵

| 维度 | 方案 A(iframe+sidecar) | 方案 B(MCP-only) | 方案 C(深度 embed) |
|------|-------------------------|-------------------|----------------------|
| 用户体验 | ⭐⭐⭐⭐ | ⭐⭐ | ⭐⭐⭐⭐ |
| 工程量 | 中(3-5 天) | 小(1-2 天) | 大(2-3 周) |
| 风险 | 低 | 极低 | 高 |
| 与 myworktree 架构契合 | ⭐⭐⭐⭐⭐ | ⭐⭐⭐ | ⭐⭐ |
| 未来扩展性 | ⭐⭐⭐⭐⭐ | ⭐⭐⭐ | ⭐⭐⭐ |

**推荐:方案 A**(理由见 5.4 决策矩阵)。

---

## 6. 安全姿态要点(参考 opencode-web)

参考 `feature/opencode-native-ui` 在 `docs/ARCHITECTURE.md §8` 的威胁模型,reasonix 嵌入的安全敏感点:

| 维度 | 现状 / 风险点 |
|---|---|
| 监听地址 | `reasonix serve --addr 127.0.0.1:0` 只绑 loopback,无 LAN 暴露;需 myworktree 强制,不允许 `0.0.0.0` |
| 鉴权传递 | `--auth token --token <myworktree token>` 与 myworktree 全局 token 统一,无需额外账号 |
| 深度防御 | myworktree reverse proxy 在 `/__reasonix/<id>/*` 路径再做一次 myworktree token 校验 |
| tag 覆盖 | 应阻止用户在 `tag.json` 里覆盖 `reasonix serve` 的命令/参数(同 opencode-web 的安全姿态) |
| provider-setup 页 | 首次配置 provider 的页面带 `frame-ancestors 'none'`,iframe 内拒绝渲染;UI 需提示"新窗口打开完成一次全局配置" |
| iframe sandbox | **同源反代下无需 sandbox 属性**(sandbox 会启用沙箱,即使同源);同源 iframe 直接加载即可 |
| CSRF 冲突 | **已核实不冲突**:CSRF 防御 = Content-Type 校验(非 JSON POST 一律 415),同源前端发 JSON 天然通过 |
| 鼠标捕获 | **不适用**:`REASONIX_DISABLE_MOUSE` 只影响 TUI;web UI index.html 无鼠标监听 |

---

## 7. 资源占用与并发模型

| 维度 | 数据 / 限制 |
|---|---|
| 每 instance 进程数 | 1 个 `reasonix serve` 进程 + 1 个模型 provider 连接 |
| 启动延迟 | reasonix serve 启动 ~1-2s(经验值,需实测) |
| session 并发 | "**One server drives one session**; multiple browser tabs share it" — 同刻一个活动会话、多 tab 共享;一个 server 可 new/resume 多个会话历史;多 worktree 隔离需每 instance 一个 serve 子进程 |
| 内存 | 默认 model 不在内存,按需加载;具体 RSS 需实测 |

---

## 8. 初版待确认问题的解答(2026-08-10 源码核实)

| # | 初版问题 | 答案 |
|---|---|---|
| 1 | token 通过什么 header 传递? | **无 Bearer 逻辑**;`authGate.checkToken`(auth.go:304-330)只校验 **Cookie `reasonix_token`**(fast path)或 `?token=` query(命中后 302 重定向到干净 URL 并种 cookie)。反代应注入 Cookie,避免 query 的 302 |
| 2 | 端口怎么拿? | **`--port-file`**(绑定后写实际 host:port,端口自动 +1 仍权威);stdout 打印 `reasonix serve — <label> on http://<addr>`(reportServeFrontend),格式不同于 opencode 的 `listening on` |
| 3 | CSRF 同源 iframe 兼容? | **兼容**:CSRF = Content-Type 校验(非 JSON POST 一律 415),同源前端发 JSON 天然通过(csrf_test.go) |
| 4 | SSE keepalive / 断连检测? | 未深究;反代 `FlushInterval=-1` 透传即可,前端 EventSource 自带重连 |
| 5 | web UI 捕获鼠标? | **不会**:index.html 无 mouse 监听;`REASONIX_DISABLE_MOUSE` 只影响 TUI |
| 6 | authGate 细节 | cookie fast path → query → 302+种 cookie;token 模式另有 `/auth/token` 端点(前端把 URL hash 里的 token POST 过去换 cookie) |
| 7 | `/health` 端点? | 无 `/health`;**`GET /status`** 返回 running/goal/cwd/context 等,可作健康探针 |

**三项实测验证(2026-08-10,`feature/reasonix-native-ui-verify` 分支,隔离 REASONIX_HOME=/tmp/rx-verify-*,不触碰用户活跃会话)**:

1. **同机多 serve 并发**:lease 按 session 文件路径互斥(`internal/control/session_lease_keeper.go` + `internal/agent/session_lease.go` 双层:进程内 registry + OS 锁文件);fresh session 路径**纳秒唯一化**(`agent/save.go NewSessionPath` 时间戳到纳秒)→ **同一 REASONIX_HOME 下多个 serve 也能并行**(实测三个 serve 同目录同时启动成功,根页面均 200);真实冲突仅发生在 `--resume` 同一既有 session 文件时(实测复现错误:`this session is in use by another Reasonix process (pid 2621204 ...); close the other Reasonix window or process first`)。v1.22.0 无 `--session-id`,但**不需要**:多 worktree 各起一个 serve 子进程,天然不同 session 路径,无 lease 障碍。
2. **`--addr` 端口行为**:`serve --addr 127.0.0.1:0 --port-file <f>` 实测 port-file 写入实际端口(如 `127.0.0.1:40741`),stdout 打印 `reasonix serve — <label> on http://127.0.0.1:40741`;端口冲突时 **serve 直接失败**(`listen tcp 127.0.0.1:8787: bind: address already in use`),**web 自动 +1**(`port 8787 is in use; using 8788 instead`,最多 100 次,port-file 写实际值)。→ 方案 1 采用 **serve + myworktree 协调端口(递增分配或预探测)+ `--port-file` 权威读取**。
3. **SSE keepalive**:`/events` 每 **15s** 发 `: ping` 保活注释行(`sseKeepaliveInterval = 15 * time.Second`,serve.go:636-672),连接打开即发 `: connected`;实测 15s 内收到 `: ping`。→ 反代 `FlushInterval=-1` 透传即可,无需额外保活。
4. **(附带)token/cookie 注入路径**:`--auth token --token-file` 实测:带 `Cookie: reasonix_token=<token>` 请求 `/history` → **200 且无重定向**;缺失/错误 cookie → **401**。→ 方案 1 的服务端 Cookie 注入方案实测支持(无 302 问题)。

---

## 9. 布局问题与注入方案(整合新增)

### 9.1 问题

reasonix 侧栏固定 **220px**(`.app{display:grid;grid-template-columns:220px 1fr}`,index.html:42),桌面端无折叠入口;折叠机制其实已存在但被锁在移动断点(`@media(max-width:768px)` 下侧栏变滑出式 + 汉堡按钮 `#menu-btn` + `openSidebar/closeSidebar` JS;`@media(min-width:769px)` 把按钮/overlay `display:none`,index.html:303-321,361,1917-1920)。

叠加 myworktree 左侧工作区栏(240px,可拖拽调宽):两个侧栏合计约 462px,窄屏下对话区局促。

### 9.2 约束(为什么必须反代)

跨源 iframe **无法定制** reasonix UI(浏览器禁止访问跨源 iframe DOM)→ 要解决布局问题,反代(同源)是**唯一路径**。被否决的替代:iframe 整体缩放(可读性差)、myworktree 侧栏收起(工作区栏比会话管理更重要)、独立窗口(多 worktree 并行时无法分辨所属项目)。因此"反代 + 注入"从可选升级为必经。

### 9.3 方案:反代 + 轻量注入(推荐)

- **路由**:`/rx/<instance-id>/*` → `http://127.0.0.1:<port>/*`,挂在 myworktree 现有 auth 中间件下(双层防护)
- **认证**:服务端注入 `Cookie: reasonix_token=<token>`(token 来自启动实例时的 `--token-file`;不用 `?token=` 因 query 分支会 302)
- **HTML 改写**(仅 `text/html` 且非 provider-setup 页,四处注入):
  1. 折叠 CSS:覆盖 `@media(min-width:769px)` 锁,让 `#menu-btn` 桌面可见、`.sidebar` 支持收起(自定义 `.mw-collapsed` 类控制),侧栏宽度可配(160/180/220px)
  2. toggle JS:覆盖 `menuBtn.onclick`(默认展开、点击收起;reasonix 原 handler 是"只开不关")
  3. URL shim(~15 行):拦截 `fetch`/`EventSource`,根相对路径前置 `/rx/<id>`
  4. logo 字符串替换:`src="/assets/logo-wordmark.svg"` 加前缀(`<img>` 不走 fetch,shim 管不到)
- **SSE**:`FlushInterval=-1` 透传
- **估算**:后端 proxy ~200 行 + 前端 kind 分支 ~60 行 + 注入 ~45 行,远小于 opencode 分支(480 行 driver + 199 行 proxy + 三层 shim)

### 9.4 与 opencode 分支的本质区别

| | opencode 分支(失败) | reasonix 注入 |
|---|---|---|
| 注入目的 | URL 重写 shim(路由/baseUrl) | 纯样式 + 折叠开关 + 单层 URL 前缀 |
| CSP | strict script-src,需 sha256 注入 | 主页面无 CSP,inline 直接可用 |
| 路由 | `/:dir/session`,需剥离前缀 | 无 path router |
| 会话归属 | 多 workspace,需裁剪/绕过首页 | 按启动目录隔离,天然锁死当前 worktree(侧栏即当前项目会话列表,无跨项目入口) |

### 9.5 安全权衡:同源 iframe 的权限提升面(评审 R-01 论证)

**事实**:`/rx/<id>/` 与 myworktree 主界面同源。同源 iframe 内的 reasonix 页面(其聊天区会渲染 AI 生成内容)可直接携带用户身份调用 myworktree 自身 API(`/api/worktrees`、`/api/instances` 等,可创建/删除 worktree、启动任意命令实例)。`internal/app` 无 CSRF 校验(CSRF 机制仅在 `internal/portal`)。

**鉴权中间件事实**(已核实 `internal/app/app.go` `withAuth`):
- **loopback 请求直接放行**(`isLoopbackRequest`),不做任何 Origin/Cookie 检查——同源 iframe 在本机场景完全静默、可读写;
- **非 loopback 请求**先做 `sameOriginHost` 检查(`Origin` 与请求 `Host` 不一致 → 403 forbidden origin),再走 Cookie 鉴权——同源 iframe 的请求 `Origin == Host`,天然通过,携带登录 Cookie 即拥有完整权限。

**威胁等级随访问模式变化**:
- **远程 / Portal + Tailscale 模式(产品主推场景)**:该权衡风险**最高**——用户为远程已认证会话,"loopback 免登录"前提不成立,同源 iframe 静默调用管理 API 的路径完全存在。**此场景下同源与跨源差异极大**:独立源方案下 iframe 页面(源 `port2`)请求 myworktree API(源 `port1`)时 `Origin != Host` → 被 `sameOriginHost` 403 拒绝,且跨域响应受 CORS 限制无法读取。
- **本机 loopback 模式**:差异较小——`isLoopbackRequest` 对本机连接直接放行,跨源 iframe 的副作用型请求仍会被服务端执行(但浏览器因 CORS 读不到响应);该场景下本机用户本就拥有完整系统权限,威胁模型本身较弱。

**MVP 决策**:当前实现接受该权衡(记录在案)。理由:
- MVP 目标为**本机工作流跑通**;远程模式使用 reasonix 实例属于后续场景,届时应同步落地独立源方案(下文方案 1)。
- 真实攻击链要求 reasonix 聊天区出现 XSS / 提示词注入且能突破 reasonix 自身渲染防线——reasonix 是否具备该防线属上游责任,本仓库无法控制;缓解责任分层。

**后续可选项(MVP 之后,按优先级)**:
1. **独立源**(推荐,且为**远程场景必需**):把 `/rx/` 反代挂到独立端口(如 `127.0.0.1:<rand>`),iframe `src` 指向该端口。reasonix 页面与 reasonix API 仍同源(无 CORS),但与 myworktree API 跨源:远程场景被 `sameOriginHost` 403 拦截,彻底切断 iframe 静默调用管理 API 的路径。改动量:一个额外 listener + 前端 iframe 地址来源(`/api/instances` 返回 `web_url`)。
2. **iframe `sandbox`**:受限 sandbox 会同时破坏 reasonix UI(需要 `allow-scripts allow-same-origin`),仅可作纵深防御。
3. **CSP**:对 `/rx/` 响应加 `Content-Security-Policy: default-src 'self'` 之类,降低注入面;不解决"同源即有权"的根本问题。

## 10. 参考资料

- reasonix 官方文档: https://reasonix.io/docs/#cli
- reasonix GitHub: https://github.com/esengine/DeepSeek-Reasonix
  - `internal/serve/serve.go`(HTTP 路由定义)
  - `internal/serve/auth.go`(鉴权)
  - `internal/serve/broadcaster.go`(SSE)
  - `internal/serve/index.html`(前端模板)
  - `internal/cli/serve_frontend.go`(CLI 入口)
  - `internal/cli/serve_files.go`(静态文件)
- myworktree `feature/opencode-native-ui` 分支
  - `docs/plans/opencode-native-ui/FEASIBILITY.md`
  - `docs/plans/opencode-native-ui/PLAN.md`
  - `docs/plans/opencode-native-ui/DEBUG.md`
  - `internal/framework/kind.go`(Kind 接口)
  - `internal/instance/opencode_web/driver.go`(driver 参考实现)
  - `internal/instance/opencode_web/proxy.go`(proxy 参考实现)

---

**结论(整合修订)**:reasonix 嵌入 myworktree 在技术上完全可行,且 **比 opencode-native-ui 简单**(vanilla HTML + 显式路由 + SSE + 标准 auth + 托管 flag)。三种方案中方案 A(iframe+sidecar,反代同源)最优;方案 B/C 不建议。**唯一需要自研的定制点是反代 + 轻量注入**(布局折叠 + URL 前缀改写,§9),实现量远小于 opencode 分支;实施前需先验证 session lease 并发与 sandbox 环境前提(§2.6.1)。
