# OpenCode 原生 Web UI 实例可行性分析

调研对象：`anomalyco/opencode`（本地路径 `/Users/linletian/Documents/GithubRepo/opencode`，分支 `dev`，版本 `1.17.8`，对应 `packages/opencode` 这一套 v1 CLI，二进制名 `opencode`）。
调研时间：2026-06-20。

---

## 0. 背景与原始需求

### 0.1 myworktree 现状

myworktree（PRD §1、§7）是一个 git worktree + coding CLI instance 管理框架，单人使用：

- 一个 git 仓库 → 多个 git worktree（隔离目录）
- 每个 worktree → 多个 **instance**（独立 CLI 进程）
- Web UI → 列出 / 启动 / 停止 instance，回放输出

当前实现的核心路径（`docs/PRD.md:60`「已实现 PTY + Web TTY」）：

```
myworktree (Go)
  └─ instance: tag = "opencode" / "shell" / ...
       └─ script -q /dev/null zsh -c "<tag.command>"  ← PTY
            └─ opencode  CLI (TUI)
                ↑↓
             xterm.js (browser)
                ↑↓
             WebSocket → PTY (回灌 input / 读 output)
```

instance = 一个 zsh 进程，命令一般是 `opencode`，跑 opencode CLI 自己的 TUI；前端用 xterm.js 渲染 PTY 输出，再把键盘 / 鼠标输入写回 PTY。

### 0.2 这条路的真实痛点

围绕 opencode TUI 路径，已经沉淀了一组文档：

| 文档 | 反映的痛点 |
| --- | --- |
| `docs/TERMINAL_FILTER_REVIEW.md` | xterm.js 处理 OSC/DA/CPR 查询回声有 bug（`11;rgb:...`、`1;2c`、`;1R` 等查询响应串到 PTY 污染 TUI），需要 `isTerminalQueryResponse()` 过滤器兜底 |
| `docs/TERMINAL_IO_ANALYSIS.md` | TUI 程序（opencode、lazygit 等）的 mouse tracking / 焦点切换 / 大量 alt-screen 切换在 PTY 路径上渲染异常 |
| `docs/TERMINAL_TEST_CASES.md` | opencode 鼠标可用性、中文显示、tui 切换等专项 case |
| `docs/GHOSTTY_WEB_RESEARCH.md`（2026-06-19 调研） | xterm.js 的 OSC/DA/复杂脚本渲染问题在 ghostty-web 中得到修；建议作为可选 renderer 灰度替换 |
| `memory/project_mw_disk_write_issue.md` + `internal/instance/logbuf.go` | opencode TUI 高频重绘场景下 PTY chunk 落盘造成 ~10万倍写放大、数据目录 280MB+（现已改为进程内 ring buffer，磁盘 I/O 完全消除，但**CPU 仍然在烧**） |

简言之：**xterm.js + PTY 这条链路上至少有三类问题——渲染失真、CPU 高、交互脆弱——都跟「绕一圈 PTY 模拟终端给一个 TUI 程序用」这件事本身有关**。这是结构性问题，不是某个渲染器或过滤器能根治的。

### 0.3 原始需求

> 调研 opencode 项目中的 app 和 cli 之间的运作关系，并根据本项目情况，分析是否可以设计一个纯 opencode web ui 的实例。相当于现在的实现情况下，新建一个 zsh 实例并在项目目录下运行 opencode cli 并将此 cli 数据展示为 web 页的形式而不是 TUI。调研此可行性。

拆开来是三件事：

1. **理解 opencode 自身的 app ↔ cli 关系**：opencode 是不是已经把 server / web UI / cli 拆开了？如果是，我能不能跳过 PTY，直接用它的 server？
2. **设计一个 myworktree 内的「opencode 原生 web UI 实例」**：对用户来说还是一个 instance，生命周期还是挂在 worktree 下；但实际跑的不是 `opencode`（TUI），而是 `opencode serve`（HTTP server），UI 是 opencode 自己的 web app，不再走 PTY / xterm.js。
3. **可行性 + 风险评估**：这种切换能不能在不改 opencode 源码的前提下做？opencode 自带的 web UI 是否已经够用，是否需要裁剪？已知 / 潜在的兼容性问题（特别是与 oh-my-opencode 等第三方插件）如何处理？

本分支 `feature/opencode-native-ui` 即为这条线工作的承载分支。

### 0.4 用户补充的两个具体观察

> opencode web 和 opencode serve 命令可以直接启动 web 服务，但实际运行发现它是一个完整的 web ui，包括工作区管理和全局的设置，不太适合我想要的仅工作区内的管理模式，不知道有没有可以自己定制源码裁剪。

> 它的 web 启动后我这里无法加载 agent 列表，不确定原因，是否是和 oh-my-openagent 插件有兼容性问题。

这两点都是分析的关键约束：

- opencode 自带 web UI 不是「单 worktree 单 UI」的形态，而是「多 workspace 管理 + 全局设置」的形态；用户希望剥离这部分（**§1.6、§3.2-C、§4 方案对比**）
- agent 列表加载失败的现象需要给出根因诊断与处理路径（**§2.2、§5**）

### 0.5 本文档结构

- §1：opencode app/cli 关系（事实陈述，基于本地源码）
- §2：opencode 自带 UI 与 agent 列表两个观察的根因分析
- §3：myworktree 集成方案 A/B/C 对比 + 推荐
- §4：方案 A 的详细设计
- §5：agent 列表加载失败的快速排查清单
- §6：待用户决策的问题
- 附录 A：关键代码位置索引

---

## 1. OpenCode 的 app ↔ cli 关系（实际仓库代码）

### 1.1 CLI 命令全景（`packages/opencode/src/cli/cmd/`）

| 命令 | 作用 |
| --- | --- |
| `opencode` | 默认行为：起 TUI，连到一个 opencode HTTP server |
| `opencode tui` | 显式起 TUI（多数情况下等价于裸命令） |
| `opencode serve` | 启 headless HTTP server（**不打开浏览器**） |
| `opencode web` | 启 HTTP server + 自动 `open()` 浏览器（`packages/opencode/src/cli/cmd/web.ts:75`） |
| `opencode attach <url>` | 已有 server 时复用，避免 MCP 冷启动 |
| `opencode debug agents` / `mcp` / ... | 调试 / 子命令 |
| `opencode run [msg...]` | 单次提示词运行 |
| `opencode acp` | IDE 用的 Agent Communication Protocol |

> 注：`packages/cli` 这套是 `lildax`，是 v2.0 preview CLI，不属于用户当前用的发行版。本文只讨论 v1（`packages/opencode`，`bin/opencode`）。

### 1.2 `web` 和 `serve` 命令的差异

两者**最终都调用 `Server.listen(opts)`**（`server.ts:72`），返回的 `Listener` 是同一个 HTTP server：

- `serve` 在终端打印 `opencode server listening on http://...`，然后 `Effect.never`（阻塞直到 kill）
- `web` 在 `serve` 基础上额外 `open(url)` 打开浏览器，并把 `0.0.0.0` 的情况打印成 LAN / mDNS 多个地址

也就是说 `web` 只是 `serve + 自动开浏览器`，**没有任何额外的 UI 注入**。

### 1.3 server.ts 里的 server 拓扑（关键发现）

```ts
// packages/opencode/src/server/server.ts
export async function listen(opts: ListenOptions): Promise<Listener> {
  const listener = await Effect.runPromise(listenEffect(opts))
  // 不绑定到任何 cwd / 项目目录；返回的 server 可服务多个 directory
}
```

`Listener` 是单一进程内一个 HTTP server。但路由层做了**多租户隔离**——每次请求独立加载 instance（见 §1.4）。

### 1.4 每次请求决定 directory 的中间件

```ts
// packages/opencode/src/server/routes/instance/httpapi/middleware/workspace-routing.ts:86-88
function defaultDirectory(request, url): string {
  return url.searchParams.get("directory") || request.headers["x-opencode-directory"] || process.cwd()
}
```

请求路由顺序：

1. `?directory=...`（URL 查询参数，**GET/HEAD 才会被 SDK 改写**）
2. `x-opencode-directory`（header，SDK 创建 client 时默认设置）
3. fallback 到 server 启动时的 `process.cwd()`

下一个中间件 `instance-context.ts:29` 把该 directory 喂给 `InstanceStore.load({ directory })`，拿到独立的 `InstanceRef`（每个 directory 各自一份 session / agent / config / db / LSP / MCP / EventBus 等）。

> **结论**：OpenCode 的 server 进程天生是「**多 directory、多项目**」共用的。不需要在 server 启动时绑定一个目录。这正好契合 myworktree 的「每个 worktree 一个目录」。

### 1.5 server 同时内嵌 web UI

```ts
// packages/opencode/src/server/routes/instance/httpapi/server.ts:185-194
const uiRoute = HttpRouter.use((router) =>
  Effect.gen(function* () {
    const fs = yield* FSUtil.Service
    const client = yield* HttpClient.HttpClient
    const flags = yield* RuntimeFlags.Service
    yield* router.add("*", "/*", (request) =>
      serveUIEffect(request, { fs, client, disableEmbeddedWebUi: flags.disableEmbeddedWebUi }),
    )
  }),
)
```

`serveUIEffect`（`server/shared/ui.ts:78`）的决策树：

1. **`OPENCODE_DISABLE_EMBEDDED_WEB_UI=true`** → `embeddedUI()` 返回 `null` → 走到 `services.client.execute(...)` 代理到 **`https://app.opencode.ai`** 上游
2. 默认（构建时生成的 `opencode-web-ui.gen.ts`） → 直接从本地静态资源（构建时打包）出 web UI
3. 上述都失败 → 兜底代理到 `https://app.opencode.ai`

**关键陷阱**（`proxy-util.ts:17`）：代理到上游时会 `out.delete("x-opencode-directory")`，所以**header 模式不跨代理工作**；但 `?directory=` 查询参数会被保留（不在 strip 列表中），所以**只要 UI URL 上带 `?directory=...`，上游也能看到**。

### 1.6 web UI 的 URL 结构（`packages/app/src/pages/directory-layout.tsx` + `app.tsx`）

```
/                       → HomeRoute（workspace 列表 / 多 server 切换）
/{base64(dir)}          → DirectoryLayout（dir 在 URL path 里，base64 编码）
/{base64(dir)}/session/:id?   → SessionRoute（具体会话页）
/new-session?draftId=…  → 草稿页
```

`SDKProvider` 接收 `directory` prop，调用 `createOpencodeClient({ directory })`，SDK 内部把 directory 写到 `x-opencode-directory` header。

**注意**：web UI 的主页是「多 workspace 管理」，用户感受到的「完整 workspace + 全局设置」主要来自：
- `pages/home.tsx`：列出本 server 上已打开过的 directory，可切换 / 关闭
- `context/server.tsx` 的 `ServerProvider`：维护多 server 列表、default server、project 列表（持久化到 localStorage）
- 全局设置（provider、theme、keymap 等）持久化在浏览器 localStorage

**这些跟「单 worktree 内单 UI」的需求是冲突的**——用户需要的是只看到一个 worktree 的、干净的单页 UI。

### 1.7 OpenAPI / SDK

- OpenAPI 3.1 规范：`GET /doc`（`server.ts:67`）返回 `OpenApi.fromApi(PublicApi)`
- 完整 API 列表见 `packages/web/src/content/docs/server.mdx`，关键端点：
  - `GET /agent`、`GET /command`、`GET /skill`、`GET /lsp`、`GET /formatter`、`GET /mcp`
  - `GET /session`、`POST /session`、`POST /session/:id/message`、`POST /session/:id/abort` ...
  - `GET /event`（SSE 流）
  - `GET /global/health`
  - `GET /tui/control/next`（驱动本地 TUI，无 web 意义）
- SDK：`@opencode-ai/sdk`（`packages/sdk/js`），`createOpencodeClient({ baseUrl, directory })`，自动设置 `x-opencode-directory` header，自动 GET/HEAD 改写 `?directory=`

---

## 2. 「opencode web」与用户期望的偏差

用户反馈：

> 「opencode web 启动后是一个完整的 web ui，包括工作区管理和全局的设置，不太适合我想要的仅工作区内的管理模式，不知道有没有可以自己定制源码裁剪」

> 「同时它的 web 启动后我这里无法加载 agent 列表，不确定原因，是否是和 oh-my-openagent 插件有兼容性问题」

### 2.1 关于 workspace / 全局设置

确认偏差：

- `HomeRoute`（`/`）会展示所有「server 列表」+「每个 server 的 project 列表」+「default server 设置」，由 `ServerProvider` 持久化在浏览器 localStorage
- 切换 server / project 是 web app 一等公民行为，**无法在运行时通过 query param 完全隐藏**

裁剪方向有两条：

1. **不裁源码**：用 iframe / 路由跳转时绕过首页，每次直跳到 `/{base64(dir)}/session`（用具体 dir 拼好），让用户看到的就是「单 worktree 内 UI」。缺点：浏览器 localStorage 仍可能污染；F5 后路径不变但 `ServerProvider` 仍记得别的 server。
2. **裁源码**：fork `packages/app`，删除 / 精简：
   - `context/server.tsx` 中的「多 server」逻辑（`servers`、`add`、`remove`、`setActive`）
   - `pages/home.tsx` 整个 HomeRoute
   - `pages/directory-layout.tsx` 跳过 base64，把 directory 直接放 query param
   - 修改 `entry.tsx` 让 default server 永远是当前 worktree 的 server

第二种方式实现成本可控（约 200~400 行删除/改动），但要承担 opencode 升级跟不上的代价。

### 2.2 关于 agent 列表加载失败（根因分析）

`/agent` 端点 (`packages/opencode/src/server/routes/instance/httpapi/groups/instance.ts:149` → `handlers/instance.ts:80` → `Agent.list()`)：

```ts
// packages/opencode/src/agent/agent.ts:98-105
const state = yield* InstanceState.make<State>(
  Effect.fn("Agent.state")(function* (ctx) {
    const cfg = yield* config.get()
    const skillDirs = yield* skill.dirs()
    const referenceDirs = yield* Effect.gen(function* () {
      yield* (yield* PluginBoot.Service).wait()       // ← 阻塞点
      return (yield* (yield* Reference.Service).list()).map((reference) => reference.path)
    }).pipe(Effect.provide(locations.get(Location.Ref.make({ directory: AbsolutePath.make(ctx.directory) }))))
    ...
```

`Agent.state` 在 `InstanceState.make` 里**只触发一次**（每个 directory 一个 instance state），但首次初始化时同步等待 `PluginBoot.wait()`：

```ts
// packages/core/src/plugin/boot.ts:75-126
const done = yield* Deferred.make<void>()
const boot = Effect.gen(function* () {
  yield* add(AgentPlugin.Plugin)
  yield* add(CommandPlugin.Plugin)
  yield* add(SkillPlugin.Plugin)
  for (const item of ProviderPlugins) yield* add(item)
  yield* add(ModelsDevPlugin)
  yield* add(ConfigProviderPlugin.Plugin)
  yield* add(ConfigAgentPlugin.Plugin)
  yield* add(ConfigCommandPlugin.Plugin)
  yield* add(ConfigSkillPlugin.Plugin)
  yield* add(ConfigReferencePlugin.Plugin)
}).pipe(Effect.withSpan("PluginBoot.boot"))

yield* boot.pipe(
  Effect.exit,
  Effect.flatMap((exit) => Deferred.done(done, exit)),
  Effect.forkScoped,
)
return Service.of({ wait: () => Deferred.await(done) })
```

`add()` 内部最终调用 `plugin.add({...})`（`PluginV2.Service.add`，v2 插件注册通道），把 plugin 的 effect 注册到当前 directory 的 instance。

**oh-my-opencode 是 v1 时代的第三方 npm 插件**（生态文档里被列为 third-party plugin）。它走的是 `PluginLoader.loadExternal`（`packages/opencode/src/plugin/loader.ts`），不会进入 `PluginBoot` 的 `add` 流程。

那么为什么 oh-my-opencode 会卡 agent 列表？最可能的两条路径：

1. **npm install 阶段卡死**：`loader.ts:97` 的 `resolvePluginTarget` 会动态 npm install 插件包（`packages/opencode/src/plugin/install.ts`）。如果 worktree 内 `node_modules` 损坏、网不通、bun 缓存命中失败，就会反复 retry 不退出。Plugin 启动失败本身**不会**让 boot 失败（loader 是容错的），但 `oh-my-opencode` 内部如果注册了 `PluginV2` hook、且 hook 内有同步 await Bun subprocess，就会阻塞整个 boot。
2. **v1 hook 注册路径与 v2 PluginBoot 解耦**：oh-my-opencode 走的是 `plugins/plugin.ts` 这条 v1 注册路径，会修改 `cfg.agent`。但 v2 的 `ConfigAgentPlugin.Plugin` 也会读 `cfg.agent`。如果 v2 boot 卡在读取 user config 的某个分支（agent 列表大 / Provider 未连接 / 模型 ID 错误），`Deferred.done` 永远不被调用，`agent.list()` 的 `Effect.await` 就一直挂着。

**诊断建议**（先不动代码）：

```bash
# 在 worktree 内
OPENCODE_EXPERIMENTAL=1 OPENCODE_DISABLE_DEFAULT_PLUGINS=1 opencode serve --port 4096
# 如果 agent 列表立刻返回，说明问题在某个内置 default plugin

# 然后单独试 oh-my-opencode
# 在 .opencode/config.json 或 opencode.json 中：
{
  "plugin": ["oh-my-opencode"]
}
# 单独 enable，看启动日志

# 看是否有过 PluginBoot log
DEBUG=1 opencode serve 2>&1 | grep -i "plugin\|boot"
```

最关键的诊断命令：

```bash
curl -sS http://localhost:4096/agent?directory=/abs/path/to/worktree -u opencode:$OPENCODE_SERVER_PASSWORD --max-time 30
# 超时即确认 boot 阻塞
```

如果确认是 oh-my-opencode 卡 boot，三种处理：

- `OPENCODE_DISABLE_DEFAULT_PLUGINS=1` 跳过内置 plugin，可能缓解
- 在 `.opencode/config.json` 把 `"plugin": []` 临时去掉 oh-my-opencode
- 等 oh-my-opencode 修（这是上游 plugin 自己的 bug，提交 issue 即可）

### 2.3 `?directory=` vs `x-opencode-directory` 的实际差异

| 维度 | `?directory=` | `x-opencode-directory` |
| --- | --- | --- |
| 走 proxy 时是否被剥离 | 否 | **是**（`proxy-util.ts:17`） |
| SDK 默认行为 | GET/HEAD 加上；POST 保留 header | 始终设置 |
| URL 分享 | 携带；可读；可能泄漏 | 浏览器不显示 |
| 多 directory 共存 | 一个 server 内天然支持 | 同上 |

**实际推荐**：浏览器内 UI 用 `?directory=...`（因为同时会被代理到上游且不会被剥），server 内部调用走 SDK（header 即可）。我们自己的 wrapper UI 同时支持即可。

---

## 3. 设计 myworktree 的「opencode 原生 web UI 实例」

### 3.1 目标

在工作区 (worktree) 内启动一个 opencode HTTP server，让用户在浏览器（或 myworktree 自己的 web UI 内嵌区）看到 opencode 原生 web UI，并且：

- 不同 worktree 之间**互不污染**（各自的 directory / agent / config / session）
- 不出现 opencode 自带的「多 workspace / 多 server 切换」首页
- myworktree 的生命周期（启动 / 关闭 / 重建）能映射到 opencode server 的生命周期
- opencode 异常（启动失败 / boot 卡住）能被 myworktree 监听到

### 3.2 三种集成路径

#### 方案 A：每个 worktree 一个独立 opencode server（最贴近现状，**推荐**）

```
┌──────────────────────────────────────────────────────────┐
│ myworktree (Go server)                                    │
│                                                            │
│  ┌── Worktree A (/repo/.worktrees/feat-a) ──┐             │
│  │  Instance #1: tag = "opencode-web"        │             │
│  │  command: opencode serve --port 14096 ... │             │
│  │  PTY (script) ── captures stdout ──► UI   │             │
│  │  暴露 http://localhost:14096/             │             │
│  └────────────────────────────────────────────┘             │
│  ┌── Worktree B (/repo/.worktrees/feat-b) ──┐             │
│  │  Instance #2: tag = "opencode-web"        │             │
│  │  command: opencode serve --port 14097 ... │             │
│  └────────────────────────────────────────────┘             │
└──────────────────────────────────────────────────────────┘
```

改造点：

1. **新增内置 Tag 模板** `opencode-web`：
   ```json
   {
     "id": "opencode-web",
     "command": "opencode serve --hostname 127.0.0.1 --port 0",
     "preStart": "true",
     "cwd": "<worktree>",
     "env": {
       "OPENCODE_SERVER_PASSWORD": "${generated-32-char-hex}",
       "OPENCODE_CLIENT": "myworktree"
     }
   }
   ```

   `--port 0` 让 opencode 自己选端口（`server.ts:120`），然后我们读 stdout 抓 `opencode server listening on http://127.0.0.1:<port>` 解析出真实端口。

2. **新增 instance 类型**：`opencode-web` 类型有别于 `shell`：
   - 不需要 PTY（不需要交互式输入）
   - 不需要 `script`（不需要终端模拟）
   - 需要 **HTTP health check**（`GET /global/health`）和 **端口嗅探**
   - stdout 抓「listening on」那一行后存入 `instance.extra["url"]` / `instance.extra["password"]`

3. **Web UI 改造**：
   - instance list 显示 opencode-web 类型时多一个「Open in browser」按钮（打开 `http://myworktree-host/proxy/<instanceId>/` 之类的代理路径，避免浏览器跨域）
   - 可选：把 opencode UI 直接 embed 在右侧主面板（iframe + `?directory=<worktree abs path>`），尺寸自适应
   - **不**需要用户在自己浏览器里访问 `localhost:14096`（穿透 LAN/Tailscale 由 myworktree portal 已有的 reverse proxy 处理）

4. **why 每个 worktree 一个进程**：
   - opencode server 是有状态的（in-memory LLM context、stream subscriber、EventBus），跨 worktree 共享容易互相干扰（虽然 directory 隔离，但 plugin boot、provider 模型缓存、LSP 工作进程仍共享 OS 资源）
   - 用户期望「一个 worktree 一个独立 opencode」，跟现状 instance 模型对齐
   - 简化生命周期：opencode 退出 ⟺ myworktree instance 退出

#### 方案 B：全 repo 一个共享 opencode server + myworktree 自己 fork 的薄 UI

```
┌─────────────────────────────────────────────────────────────┐
│ myworktree (Go server)                                       │
│   ┌── 共享 Service: opencode serve --port 14096 ──┐          │
│   │  路由 /{base64(dir)}/* → 该 dir 的 instance   │          │
│   └─────────────────────────────────────────────────┘         │
│   ┌── Worktree A 的 instance: tag = "opencode-shared" ──┐    │
│   │  command: 不启动进程，只更新 shared server 的 project list │   │
│   │  extra.url = "http://127.0.0.1:14096/?directory=<A>"     │    │
│   └─────────────────────────────────────────────────────────┘    │
│   ┌── Worktree B 同上 ──┐                                       │
│   └─────────────────────┘                                       │
└─────────────────────────────────────────────────────────────┘
```

改造点：

1. myworktree 启动时只起**一个** opencode serve 进程（不绑 worktree），作为全局服务
2. 每个 worktree 对应的 myworktree「instance」**不**是 opencode 进程，只是一个 metadata entry：
   - `instance.command` 实际是 myworktree 内置的 `noop register`（向 shared server 注册该 worktree 的 dir）
   - `extra.url` 是 `http://127.0.0.1:14096/?directory=<encoded>`
3. Web UI 把这个 URL 用 iframe 嵌入

权衡：

- ✅ 节省内存 / 启动时间 / plugin boot 重复
- ✅ 跨 worktree 共享 provider 凭据（**opencode 不支持跨 dir 共享凭据，server 进程内 directory 隔离，provider 是按 instance 加载的，所以这一点优势实际上不成立**）
- ❌ 失去了「opencode 实例随 worktree 生命周期」的对齐：一个 opencode 进程崩了，所有 worktree 都崩
- ❌ myworktree 的「删除 worktree 时一并清理 instance」语义弱化（opencode 进程内的 instance 是 lazy 加载的，理论上会自动 GC，但仍需要 dispose）

#### 方案 C：用 fork 后的 opencode web UI（裁剪掉 workspace 管理）

```
┌────────────────────────────────────────────────────┐
│ myworktree (Go server)                              │
│   ┌── 共享 service: opencode serve --port 14096 ──┐  │
│   └─────────────────────────────────────────────────┘  │
│   静态托管裁剪过的 opencode-web 静态资源（位于       │
│   internal/ui/static/opencode/），首页直接跳转到      │
│   /<encoded-dir>/session                             │
└────────────────────────────────────────────────────┘
```

改造点：

1. 拉一份 `packages/app` 源码做最小裁剪（详见 §4.2），build 出静态资源
2. myworktree 内置 serve 该静态资源，base path 改成 `/__opencode/`
3. 入口脚本强制 default server 为 `location.origin + '/__opencode/api/'`，去掉 server 列表 UI
4. 入口脚本把 `directory` 直接写到 `?directory=` query param，跳过 base64 URL 段

权衡：

- ✅ 用户体验最干净（没有 HomeRoute）
- ✅ 单 server 跨 worktree，资源省
- ❌ 需要自己维护 opencode app 的 fork，opencode 升级需要 rebase
- ❌ 静态资源可能很大（需评估大小）

### 3.3 三方案对比

| 维度 | A. 每 worktree 一进程 | B. 单 server + 薄壳 | C. 裁剪 UI + 单 server |
| --- | --- | --- | --- |
| 改动量（myworktree Go 代码） | 中（新增 instance 类型 + 端口嗅探 + 代理） | 小（无新进程） | 中（内置静态 + 裁剪脚本） |
| 改动量（前端） | 小（按钮 + iframe） | 小 | 大（fork opencode app） |
| 隔离性 | 最强 | 中（provider / plugin 共享 OS） | 中 |
| 生命周期对齐 | 自然对齐 | 错位 | 错位 |
| 资源占用 | 高（每 wt 一个 bun 进程） | 低 | 低 |
| 跟随 opencode 升级 | 完全跟随 | 完全跟随 | 需要 rebase |
| 可观测性 / 失败模式 | 简单（一个 instance 死了不影响其他） | 共享故障域 | 共享故障域 |
| 与现有 instance/manager 复用 | 高 | 低 | 低 |

**推荐**：方案 A 作为第一阶段（改动小、复用现有 instance 管理、隔离强），未来如果资源成为瓶颈再考虑切到 B 或 C。

---

## 4. 方案 A 详细设计

### 4.1 新增 Tag 模板

在 `internal/instance/manager.go` 的 default tag 列表里追加：

```go
{
  ID:      "opencode-web",
  Command: `opencode serve --hostname 127.0.0.1 --port 0`,
  Env: map[string]string{
    "OPENCODE_SERVER_PASSWORD": "<auto-generated>",
    "OPENCODE_CLIENT":          "myworktree",
  },
}
```

关键点：

- `--port 0` 让 opencode 自选端口（`server.ts:120`：`startListener(opts, 4096).pipe(Effect.catch(() => startListener(opts, 0)))`，4096 优先，失败回退到 0）
- `OPENCODE_SERVER_PASSWORD` 由 myworktree 生成后注入 env，存到 `instance.extra["password"]`
- `OPENCODE_CLIENT` 影响 server 行为（runtime-flags.ts:55）但只是标识作用

### 4.2 新增 instance 生命周期阶段

在 `internal/instance/manager.go` 的 `Start()` 里，对 `opencode-web` 类型执行以下步骤：

1. 生成 32 字节随机 hex token 作 `OPENCODE_SERVER_PASSWORD`
2. 启动子进程（与现有 `script` 流程并行；不需要 PTY，`os/exec` 直接 spawn 即可，或者继续用 `script -q /dev/null` 抓日志）
3. 抓 stdout 匹配正则 `opencode server listening on http://([^:]+):(\d+)` → 提取 `port`
4. 写入 `instance.extra["url"] = "http://127.0.0.1:<port>"`、`instance.extra["password"] = "<token>"`
5. 状态置为 `running`
6. 启动后台 goroutine：每 5s `GET http://127.0.0.1:<port>/global/health`，失败连续 3 次置 `stopped`

### 4.3 myworktree web UI 改动

`internal/ui/static/` 新增 opencode 嵌入页：

```html
<iframe src="/__opencode/proxy/<instanceId>/?directory=<encoded-worktree>"
        style="width:100%;height:100%;border:0"></iframe>
```

`internal/ui/handlers.go` 新增 reverse proxy handler：

```go
// 转发 /__opencode/proxy/<id>/* → http://127.0.0.1:<port>/*
// 注入 Authorization: Basic base64("opencode:<password>")
// 强制带上 ?directory=<worktree>（防止 iframe 内前端拼错）
```

要点：

- **CSP**：opencode web UI 内部还有 dynamic import、CSP header，proxy 时不要多加 `Content-Security-Policy`，透传 server 的
- **WebSocket**：`/event` 是 SSE，但浏览器端 ws 可能用到 `pty` 等，需确认 opencode UI 用的 transport 全是 SSE（`server.ts:78` 用了 SSE：`/global/event`、`/event`）
- **cookie / session**：opencode 没有自己的 session cookie，HTTP basic 就够了

### 4.4 与现有 portal / Tailscale 的兼容

portal 已经在做 `/s/<repo-hash>/` reverse proxy（README 里说「planned but not yet implemented」，但 portal 框架已就绪）。新加的 `/__opencode/proxy/<id>/` 可以走同一套代理层。

### 4.5 失败模式

| 故障 | 检测 | 处理 |
| --- | --- | --- |
| opencode 启动失败（命令找不到、依赖缺失） | exit code != 0 / 10s 内没匹配 listening 行 | instance 状态 `failed`，UI 展示最后 50 行日志 |
| 端口嗅探超时（30s 内无 listening 行） | 定时器 | instance 状态 `failed`，logs 里有原因 |
| opencode 进程运行中但 `/global/health` 连续 3 次 5xx | 后台 goroutine | instance 状态 `unhealthy`，UI 显示警告 banner |
| opencode 进程被 SIGKILL | wait() 返回非零 | instance 状态 `stopped`，UI 提示 |
| oh-my-opencode 启动卡死（§2.2） | `/agent?directory=...` 请求 30s 超时 | **不在 instance 层级处理**；UI 层加个 toast 让用户去 worktree 内排查 plugin |

---

## 5. agent 列表加载失败的快速排查清单

不实施任何代码改动，按下面顺序做诊断：

```bash
# 1) 复现：在 worktree 内手动起 server
cd /path/to/worktree
OPENCODE_SERVER_PASSWORD=test opencode serve --hostname 127.0.0.1 --port 14096 &
SERVER_PID=$!
sleep 3

# 2) 看 agent 列表是否超时
time curl -sS --max-time 30 \
  "http://127.0.0.1:14096/agent?directory=/path/to/worktree" \
  -u opencode:test

# 3) 如果超时，禁用所有 plugin 再试
kill $SERVER_PID; wait $SERVER_PID 2>/dev/null
OPENCODE_SERVER_PASSWORD=test OPENCODE_DISABLE_DEFAULT_PLUGINS=1 opencode serve --port 14096 &
SERVER_PID=$!
sleep 3
time curl -sS --max-time 30 "http://127.0.0.1:14096/agent?directory=/path/to/worktree" -u opencode:test
# 如果这次秒回，说明问题是某个内置 plugin

# 4) 关掉 oh-my-opencode：在 .opencode/config.json / opencode.json 把
#    "plugin": ["oh-my-opencode"] 改成 "plugin": []，重启 server，重试 /agent

# 5) 看 server 端日志里有没有 Boot / Plugin 相关 error
# opencode 默认日志输出到 stderr，可加 DEBUG=1
```

把第 2、3、5 步的结果贴给 oh-my-opencode 仓库的 issue 跟踪；如果是 §2.2 的第二种路径（v1 plugin hook 阻塞 v2 PluginBoot），那是 upstream bug，得等修。

---

## 6. 待用户决策的问题

1. **方案选择**：A（每 wt 一进程，推荐）/ B（共享 server）/ C（裁剪 UI）？
2. **oh-my-opencode**：是否作为 P0 必须支持？还是允许 myworktree 在 oh-my-opencode 卡死时弹 toast 提示？
3. **tag 命名**：内置 tag id 用 `opencode-web` 还是 `opencode-serve` 还是别的？
4. **持久化**：`OPENCODE_SERVER_PASSWORD` 是否每次重启 instance 都重新生成（更安全）还是持久化到 `tags.json`（更省心）？
5. **远程访问**：portal 代理时如何处理 basic auth（建议 portal 自己再叠一层 token，opencode 的 password 仅本地用）？

---

## 附录 A：关键代码位置索引

- CLI 命令注册：`packages/opencode/src/cli/cmd/{web,serve,tui,...}.ts`
- HTTP server 入口：`packages/opencode/src/server/server.ts:72 listen()`
- 路由层：`packages/opencode/src/server/routes/instance/httpapi/server.ts:261 createRoutes()`
- 路由分支：`packages/opencode/src/server/routes/instance/httpapi/api.ts`、`groups/*.ts`
- 多租户中间件：`.../middleware/workspace-routing.ts:86-88 defaultDirectory()`
- 实例上下文中间件：`.../middleware/instance-context.ts:29 store.load({ directory })`
- Web UI 路由：`packages/app/src/app.tsx:451-460`
- DirectoryLayout：`packages/app/src/pages/directory-layout.tsx`
- ServerProvider（多 server）：`packages/app/src/context/server.tsx`
- 嵌入 UI 服务：`packages/opencode/src/server/shared/ui.ts:78 serveUIEffect()`
- 代理 header 剥离：`packages/opencode/src/server/proxy-util.ts:17 sanitize()`
- PluginBoot：`packages/core/src/plugin/boot.ts:75-126`
- Agent 阻塞点：`packages/opencode/src/agent/agent.ts:103 PluginBoot.wait()`
- SDK client：`packages/sdk/js/src/client.ts:33 createOpencodeClient({ directory })`
- OpenAPI：`packages/opencode/src/server/server.ts:67 openapi()`、`httpapi/server.ts:181 /doc`