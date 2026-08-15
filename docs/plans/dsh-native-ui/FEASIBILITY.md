# DeepSeek Harness Web UI 原生 web UI 实例化可行性分析

> 调研对象：
> - `deepseek-harness`（`/home/linletian/GithubRepo/deepseek-harness/`，web UI = `apps/web` + `packages/client/*` + `packages/bundle/web-app` + `apps/cli`）
> - `myworktree`（本仓库，参照物 = 已落地的 `opencode-web` 实例 kind：`internal/instance/opencode_web/` + `docs/plans/opencode-native-ui/`）
>
> 调研时间：2026-08（分支 `feature/dsh-native-ui`，基线 `develop@5b3bf54`）。
>
> 结论一句话：**可行**。`dsh web` 本身就是一个「无头 HTTP server + 嵌入式 SPA」，形态与 `opencode serve` 高度同构，完全可以按 opencode-web 的既有模式做成 myworktree 的 `dsh-web` 实例 kind；且 harness 的**工作区限制（沙箱策略）是 OS 级硬强制（dsh 自带）**；myworktree 侧按用户决策采用 opencode 式做法——禁用/置灰跨 worktree 入口 + 观察式监测只提醒（不拦截）。

---

## 1. harness web UI 的运行形态（事实陈述）

### 1.1 `dsh web` 本质上是 serve + 内嵌 UI

- `dsh web` 是 `dsh --profile web` 的别名（`apps/cli/src/args.ts:156`），boot 的是 `packages/bundle/web-app` 这个 profile 层。
- 它是一个**前台长驻进程**：起 HTTP server（`packages/host/webserver`），由 `frontend-static` 兜底路由托管 `apps/web` 构建出的 `dist/`（`packages/host/frontend-static/src/index.ts`），`dist/index.html` 经 index tap 注入 `window.__DSH_BOOT__`（`packages/client/modules/src/index.ts:161-170`）。
- **不依赖 TTY**：无交互式输入，SIGTERM/SIGINT 有优雅关停（`apps/cli/src/process-shutdown.ts`，5s 宽限后强制退出）——与 myworktree 的 `Kind.Stop(SIGTERM→超时→SIGKILL)` 生命周期天然对齐。
- 前端是 Vite 构建的单页应用；`vite.config.ts` 里明确写着 "apps/web is not a standalone application: bare Vite cannot inject window.__DSH_BOOT__"——即**只能通过 `dsh web` 承载**，这与 opencode「server 内嵌 web UI」的定位一致。

### 1.2 启动参数（`packages/bundle/web-app/src/startup.ts`）

| 参数 | 默认 | 对 myworktree 的意义 |
| --- | --- | --- |
| `--host <host>` | `127.0.0.1` | 恰好与 opencode-web 一样只绑 loopback |
| `--port <port>` | `3080` | **支持 `--port 0` 让 OS 分配空闲端口**（注释明确写出） |
| `--trusted-host <auth>` | — | LAN 访问的信任栅栏扩展；代理场景可不依赖它 |
| `--host 0.0.0.0` | — | **被显式拒绝**（`startup.ts:42`，安全考虑） |

就绪信号：boot 完成后打印 `dsh web: http://127.0.0.1:<port>`（`packages/bundle/web-app/src/index.ts` `printUrl`）——与 opencode 的 `opencode server listening on http://...` 完全同构，`driver.go` 里抓 stdout 解析端口的模式（`listeningAddrRe`）可直接复用，**`--port 0` + 解析 URL 行 = 零端口冲突**。

### 1.3 认证与信任栅栏（`packages/client/connection/src/api-request-trust.ts`）

- **没有认证层**（文档明言 "this fence is not an auth layer"）。`/api` 的信任栅栏规则：
  1. Host 必须是 loopback 或 `trustedHosts` 名单；
  2. `sec-fetch-site: cross-site` 拒绝；
  3. 有 `Origin` 时，`Origin.host` 必须等于 Host 的 authority（**没有 Origin 则放行**）。
- 后果（对集成很关键）：myworktree 反代时**设 Host 为上游 `127.0.0.1:<port>`、删除 Origin 头**即可通过栅栏（"Absent Origin is fine" 是文档明确行为）。
- 反面提醒：`settings` / `credentials` / 原生对话框等 API 被"loopback 才放行"（`packages/client/connection/src/index.ts` 注释），**经代理后这些请求的 Host 是 loopback，等于门户把整个配置面（含凭据描述）暴露给了代理入口** → 远程访问必须由 myworktree portal 自己叠加 token（与 opencode 分支 §6 决策 5 相同的结论）。

### 1.4 会话与工作区模型（多会话、多工作区、单进程）

- 一个 `dsh web` 进程 = 一个 host graph，**多个 session、多个 workspace 并存**；web profile 的 `agent-loop` 为空（`packages/bundle/base/cordis.patch.yml:435`："Web creates sessions on client request"），每个会话通过 **agent presets** 挂载自己的工具/提示词。
- 会话创建：`session.create` RPC 接受 `workspaceId` **或任意 `cwd`**（`packages/host/apiproxy/src/api/sessions.schema.ts:102-110`），实现无 cwd 白名单。
- 工作区：`workspaceRegistry` 持久化目录列表（`$DSH_HOME/storages`），`workspace.create` 接受**任意路径**（`packages/host/apiproxy/src/api/workspace.schema.ts:34-37`）；UI 侧 `ui-workspace` 的 WorkspaceBrowser 挂在 `sidebar.workspaces` 槽位，带 **directory flow**（原生 OS 目录选择器 `directory-picker-native` 或**应用内浏览主机文件系统的 browse 选择器**）。
- 持久化：会话 jsonl 在 `$DSH_HOME/sessions`，工作区/存储域在 `$DSH_HOME/storages`，凭据、profiles 也在 `$DSH_HOME` 下。
- 事件通道：`/api` fetch RPC + **WebSocket**（`/api/events.mux`、`/api/events.host`）——反代必须透传 `Upgrade`（Go `httputil.ReverseProxy` 原生支持；比 opencode 的纯 SSE 多一个要求）。

**对比 opencode**：这套"单进程多 directory 多会话 + UI 一级公民地展示/切换工作区"的结构，正是 `docs/plans/opencode-native-ui/FEASIBILITY.md §1.6` 里 opencode 被吐槽的形态的翻版——**跨 worktree 切换在 harness UI 里同样是"一次点击"**。

---

## 2. 工作区限制功能分析（用户重点关注项）

### 2.1 harness 原生的工作区限制 = 沙箱策略（硬强制，非 UI 隐藏）

`packages/sandbox/sandbox-policy/src/index.ts` 是权威出处：

- **三层模式**：`read-only` / `workspace-write` / `danger-full-access`（`SandboxMode`，文件效果维度）。
- **工作区根 = 会话的不可变 cwd**：`resolve()` 返回 `workspaceRoot: session.header.cwd`（无会话时回退 `process.cwd()`）。
- **OS 级强制**：Linux bwrap/Landlock、macOS Seatbelt、Windows ACL（`packages/sandbox/sandbox-local`），bash/fs/terminal 三类后端读同一份策略；denial 以 `[sandbox: file access denied under workspace-write mode]` 标记进入模型上下文，带严格加宽的升级阶梯（`workspace-write → danger-full-access` 需审批）。
- 部署默认：base 层 `sandbox-policy` 行 `mode: DSH_PERMISSION_MODE ?? 'workspace-write'`、`workspaceRoot: process.cwd()`；审批策略默认 `ask`（danger-full-access 下 `never`）。**每条 session 还有运行时开关**（`sandbox/mode` 事件 + UI 权限 presets），存活于会话日志，重启重放即恢复。
- 活证据：dsh 会话的系统提示即为此机制产物——"Current DSH file policy: workspace-write … under the session workspace: <cwd>"。
- **工作区注册时机**（用户实证 2026-08-15）：首次运行 dsh web 时工作区列表为空——注册表只在两类时机建记录：①该 DSH_HOME **首次初始化**时从已有 session 头按 cwd 分组 bootstrap（`packages/workspace/workspace/src/index.ts:426`，`state.initialized` 置位后不再跑）；②`workspace.create` RPC（`api-proxy.ts:1712-1714`）。**进程 cwd 不会自动注册**，`session.create {cwd}` 也不注册工作区（只决定会话目录，`api-proxy.ts:2170-2176`）；多进程"工作区互通"是共享 DSH_HOME durable registry 的设计行为。
- **启动注入没有 flag，但有幂等的驱动层自举**：`dsh web` 的 flag 面只有 `--host/--port/--trusted-host`（`startup.ts`），无 `--workspace`；但 `workspace.create {path}` 是幂等 adopt 语义（realpath 归一、目录须存在、已存在原样返回、新建的 prepend 到列表最前）。dsh-web driver 在就绪后直连上游 loopback 打一次 `POST /api/workspace.create`（RPC 信封 `{type:'client-request', rpcId, method, payload:{path:<worktree>}}`，Go http client 无 Origin、Host=loopback，天然过信任栅栏）即可把 worktree 注册进侧栏，同 cwd 历史会话自动归组；可选再打 `session.create {cwd}` 预建空白会话直接落到会话页。失败只告警不阻塞（见 §3 集成表）。

**含义**：myworktree 把 `dsh web` 以 `cwd=<worktree>` 拉起后，**该 worktree 内的每个会话天然被 OS 级沙箱钉死在自己的 cwd 上写文件**。这是 opencode-web 方案完全没有的硬保证（opencode 只能"防误操作 + 警告"，见 `docs/plans/opencode-native-ui/WORKTREE-ISOLATION.md §0.3`）。

### 2.2 确认方案（用户决策 2026-08-15）：opencode 式——禁用入口、不拦截、只提醒

**决策**：遵循 opencode 分支的定位（防误操作、非禁止）：disable 掉跨 worktree 的入口（尽量"置灰可见"而非删除），**不干涉 dsh 自身的运行规律（不拦截任何请求、不改写任何响应、不拒绝会话/工作区创建）**，越界行为只监测 + 常驻提醒。沙箱自身的 OS 级写限制照旧生效（那是 dsh 自己的规则，不是 myworktree 加的）。

可行性核实（dsh 组合机制原生支持，比 opencode 当年靠 DOM 注入干净得多）：

| 入口 | dsh 原生控制面 | "置灰可见"的途径 |
| --- | --- | --- |
| "Add workspace…" + 项目选择菜单（UI 上唯一"收养任意目录"的入口） | ✅ `--patch` overlay 禁用 `directory-picker` 行 → directory-flow hole 空置 → 入口整体不渲染（`slots.ts` 契约原文："an unoccupied hole leaves the surface with no add affordance at all"；`WorkspaceBrowser.tsx:1053` `directoryFlowAvailable &&` 门控） | 微型 client plugin 占据 `sidebar.workspaces.directoryFlow` / `conversation.hero.workspace.directoryFlow` 两个 hole，渲染禁用态占位（tooltip「工作区切换由 myworktree 管理」）。client plugin 是 dsh 一等公民机制（`dsh.client` rows + `dsh plugin --profile web add <pkg>`），非 fork |
| 侧栏工作区行（点击即切换/新建其他 worktree 的会话） | ❌ 无行级 slot | 三选一：①opencode 式 DOM 注入（`buildInjectScript` 同款，先例已验证、升级脆弱）；②禁用 `ui-workspace` + 自写简化侧栏 client plugin（原生但改动大）；③**不置灰**——行保持可见可点，越界点击由监测警告条兜底（最贴合"只提醒不干预"，建议默认） |
| 会话列表（其他 worktree 的历史会话） | 保持原样 | 共享 DSH_HOME 语义下**刻意保留**（与 opencode"可读当前项目全部历史"一致；范围外条目也是提醒的素材来源） |

监测（只提醒，零干预）：

- proxy 层解析请求体中的工作区信号——`session.create {workspaceId|cwd}`、`workspace.create {path}`（schema 已核实）——与实例 worktree 归一化比较，**只 Record 越界状态，不拦截、不改写、不降级**；结构照抄 opencode `scope.go` 的 `normalizeDir` / `classify` / `ScopeTracker`。
- 与 opencode 的覆盖面差异：dsh 没有 `x-opencode-directory` 这种"每个请求都带目录"的机制，可观测信号集中在创建类 RPC 的 body；但"任何跨 worktree 切换必然触发一次 session.create / 连接重建"的论证同样成立（opencode `WORKTREE-ISOLATION.md §4.3` 同款），对"只提醒"足够。
- 前端 iframe 外常驻警告条（myworktree 自己的 DOM，不进入 dsh 页面），三态：正常 / 越界（`⚠ dsh 已离开 worktree 范围: <dir>`）/ 裁剪失效（版本门 L1 + DOM 锚点检测 L2/L3 平移，见 opencode `WORKTREE-ISOLATION.md §4.7`，防 dsh 升级后入口重新出现而静默失效）。

后果语义（与 opencode 完全一致）：

- 用户主动点别的 workspace 开会话 → 请求照常放行（不干涉 dsh 运行规律），新会话的沙箱根 = 那个目录（dsh 自己的规则），警告条常驻直到切回 worktree。
- 沙箱继续兜底：越界会话的文件写入被 OS 级限制在那个目录内——"限制工作区"的硬部分由 dsh 沙箱承担，myworktree 只做"防误操作 + 提醒"。
- devtools 直调 API 同样不拦：越界创建照常成功，仅被监测记录并触发警告（与 opencode D3"只警告不干预"对齐）。

### 2.3 DSH_HOME 策略（对应 opencode 的"数据目录隔离"教训）

- **共享 `$DSH_HOME`**（推荐）：凭据、会话历史、工作区注册表跨 worktree 可见——等价于 opencode 共享 `opencode.db` 的语义（"读/切当前项目全部历史 session 天然成立"，`WORKTREE-ISOLATION.md §2.1`），但引入侧栏串台，需 §2.2 的 UI 裁剪（禁用/置灰入口）+ 监测提醒收口。
- **每实例 `DSH_HOME`**：完全隔离，但**丢历史 + 凭据要重配/软链**——正是 reasonix issue #56 踩过又回退的坑（`WORKTREE-ISOLATION.md §1.2`），不推荐作默认。

---

## 3. myworktree 集成设计（dsh-web kind）

myworktree 的 `framework.Kind` 接口（`internal/framework/kind.go`）已被 opencode-web 和 reasonix 两个 HTTP-backed kind 验证过，新 kind 的落点完全清晰：

| 环节 | 设计 | 依据/先例 |
| --- | --- | --- |
| Kind | `internal/instance/dsh_web/driver.go`，注册名 `dsh-web`，`Interactive: false` | 复制 opencode-web 骨架（`driver.go` 全篇） |
| Spawn | `dsh web --host 127.0.0.1 --port 0`，`cmd.Dir = WorktreePath`，env 继承 + `DSH_HOME` 策略、可选 `DSH_PERMISSION_MODE` 钉死 | `startup.ts` 参数面；沙箱策略 env seam |
| 就绪 | 抓 stdout `dsh web: http://127.0.0.1:(\d+)` → 解析端口 → 写 blob、`ReadySignal.Close()` | 与 `listeningAddrRe` 同构 |
| 健康 | 定时 `GET /`（SPA index 200）即可（无独立 health 端点） | `frontend-static` 兜底路由保证 `/` 恒 200 |
| 工作区自举 | 就绪后直连上游 loopback 打 `POST /api/workspace.create`（RPC 信封 `{type:'client-request', rpcId, method, payload:{path:<worktree>}}`），幂等 adopt，侧栏立即出现该 worktree 及其同 cwd 历史会话；可选 `session.create {cwd}` 预建空白会话直接落到会话页；失败仅告警不阻塞 | workspace registry `create(path)`（realpath 幂等、新建 prepend）；信封格式 `fetch/client.ts callUnary`；§2.1 注册时机 |
| 停止 | SIGTERM → 5s → SIGKILL（dsh 自身 5s 宽限，myworktree 默认 `stopGrace` 兼容） | `process-shutdown.ts` |
| **嵌入形态** | **每个实例一个独立 loopback 端口**：myworktree 在 `127.0.0.1:<free>` 开监听，反代到上游（Host=上游 loopback、删 Origin、透传 Upgrade），iframe src=`http://127.0.0.1:<proxyPort>/` | reasonix 分支 issue #44 的 loopback 模式先例 |
| 远程访问 | portal 现有反代叠 token，再转发到上述 loopback 监听 | `internal/portal/portal.go` 已有 AuthToken/CSRF 框架 |
| 工作区限制 | opencode 式、只提醒不拦截：①profile overlay 禁用 `directory-picker` 行（"Add workspace…"入口随之消失；置灰可见用微型 client plugin 占位）；②代理层**观察式**监测 `session.create`/`workspace.create` body 的越界目录，只 Record 不拦截；③iframe 外常驻警告条（三态：正常/越界/裁剪失效）；④版本门 + 锚点检测兜底 | §2.2；opencode 分支 `scope.go`/`proxy.go`/`WORKTREE-ISOLATION.md` 模式平移 |

**嵌入形态必须选独立 origin**（而不是 opencode 的 `/__opencode/<id>/` 同源挂载）：harness 客户端把 API base 硬编码为 `location.origin + '/api'`（`packages/client/connection/src/api-path.ts` + fetch client 的 `INTERNAL_BASE = location.origin`），dist 资产也是根绝对路径——同源子路径挂载会与 myworktree 自己的 `/api` 冲突且无法区分实例；独立 origin 则**零改写、零注入**，比 opencode 分支的 HTML 重写 + CSP hash + DOM 隐藏脚本那套（`WORKTREE-ISOLATION.md §4`）干净得多。

---

## 4. 缺失依赖处理：npx 与安装的交互选择（已确认）

**用户决策**：未检测到 `dsh` 时，由用户在「npx 启动 / 直接安装 / 取消」之间交互选择，不做静默决策。

### 4.1 事实基础：PATH 快照语义（是否需要重启 myworktree）

- 三个既有 kind 都是**每次 Start 现读环境**：opencode-web 的 `buildEnv()` 每次 Spawn 现读 `os.Environ()`（`driver.go:315`），reasonix 的 `exec.LookPath` 在每次 `Start()` 现查（`driver.go:201`），pty 每次 `cmd.Env = os.Environ()`（`driver.go:94`）。不存在启动时缓存的二进制列表。
- 因此"安装后要不要重启"完全取决于安装落点是否已在 myworktree 继承的 PATH 里：

  | 安装落点 | 是否需重启 myworktree |
  | --- | --- |
  | 已在 myworktree 继承 PATH 中的目录（`~/.local/bin`、`/usr/local/bin`、已在 PATH 的 npm global bin） | **否**，下一次 Start 即找到 |
  | 新目录，且安装器把 PATH 写进 shell rc，而 myworktree 从 GUI/旧 shell 启动（进程 env 不会更新） | **是**；或走 §4.3 绝对路径启动绕过 |

- npm 全局安装的常见坑正是第二种：`npm i -g` 的 prefix 未必在 GUI 进程的 PATH 里。

### 4.2 npx 方式（免安装、免重启）

命令形态：`npx --yes @deepseek-ai/dsh@<pin> web --port 0`

- 就绪行透传：npx 以 stdio inherit 运行 dsh，`dsh web: http://127.0.0.1:<port>` 正常出现在 stdout，现有监听行正则照抓。
- `--yes` 跳过 npx 自身的安装确认；`@<pin>` 固定版本使缓存命中后不查 registry（避免离线失败/启动变慢）。
- **进程树清理（关键坑）**：npx 是 dsh 的父进程，只杀 npx 的 PID 会留下孤儿 dsh 占用端口。dsh-web 必须 `Setpgid: true` + 杀 `-pid` 进程组（先例：`reasonix/driver.go:274`、`pty/driver.go:216`；opencode-web 目前只杀单 PID，不可照抄）。
- 前提：npm 在 myworktree 的 PATH 中（与 node 同目录）。

### 4.3 直接安装方式（安装后同样免重启）

- myworktree 代跑 `npm install -g @deepseek-ai/dsh`（后台 job；`npm install` 作为 tag preStart 已是文档示例，语义延伸即可）。
- 安装完成后**不依赖重启**：用 `npm prefix -g` 动态求出全局 bin 目录（prefix 不在 PATH 也能求），以**绝对路径**直接 Spawn——绕开 §4.1 的 PATH 快照问题。
- 兜底：候选目录探测列表（`npm prefix -g`/bin、`~/.local/bin`、`~/.npm-global/bin`）依次 LookPath；全失败才提示用户重启 myworktree 或手动配置。
- 注：npm 安装的包自带前端 dist（`apps/web/package.json files: ["dist"]`），安装即完整可用；源码 checkout 场景才需要 `pnpm build`（见 §6 风险 1）。

### 4.4 交互流程设计

```
dsh-web Spawn 预检 LookPath("dsh")
  ├─ 找到 → 正常启动（后续可选版本门）
  └─ 未找到 → 返回专用错误 ErrDshNotFound{npmAvailable, suggestedPin}
        → markFailed + last_error
        → 前端 kinds/dsh_web.js 识别该错误 → 弹对话框：
            〔用 npx 启动〕→ 以 npx 命令重试（命令形态 §4.2；也可预置为 tag 默认值，经现有 /api/tags 自行切换）
            〔立即安装〕  → 代跑 npm i -g + npm prefix -g 求绝对路径 → 重试（§4.3）
            〔取消〕      → 保持 failed，展示原始提示
```

- 预检错误必须带可读安装提示（对齐 reasonix 的 LookPath 提示风格，`reasonix/driver.go:198-205`），而不是 opencode-web 的原始 exec 错误（`opencode start: exec: "opencode": executable file not found in $PATH`）。
- 版本门：dsh 迭代快，建议沿用 reasonix 的硬版本门（`checkVersion`，issue #45 先例），具体在 PLAN 阶段定。

### 4.5 与既有 kind 的缺失依赖处理对比

| 环节 | opencode-web | reasonix | dsh-web（本设计） |
| --- | --- | --- | --- |
| 二进制缺失预检 | ❌ 原始 exec 错误 | ✅ LookPath + 安装提示 + ReasonixBin | ✅ LookPath + 专用错误 + 交互选择 |
| 安装/替代引导 | ❌ 无 | 仅文字提示 | ✅ UI 对话框（npx / 安装 / 取消） |
| 版本门 | advisory 警告条 | 硬门 fail-fast | 待定（PLAN 阶段，倾向硬门） |
| 进程树清理 | 单 PID | 进程组 | 进程组（npx 形态必须） |

---

## 5. 与 opencode-web 方案对比

| 维度 | opencode-web（现状） | dsh-web（本方案） |
| --- | --- | --- |
| 上游形态 | `opencode serve` 无头 server + 内嵌 UI | `dsh web` 无头 server + 内嵌 UI ✅ 同构 |
| 端口 | `--port 0` + 抓 listening 行 | 同 ✅ |
| 认证 | Basic auth（密码注入 env） | 无认证，信任栅栏（loopback） |
| 工作区限制 | **只警告不拦截**：DOM 注入隐藏入口 + 代理层监测 + 常驻警告条 + 版本门 | **沙箱 OS 级硬强制（dsh 自带，非 myworktree 添加）** + patch/插件裁剪 UI 入口 + 观察式监测只提醒（同 opencode 语义） |
| 跨 worktree 会话可见性 | 共享 db，天然可读全项目历史 | 共享 `$DSH_HOME` 同语义；隔离 DSH_HOME 则丢历史 |
| 定制途径 | 无组合机制 → 只能 fork 源码或 DOM 注入 hack（升级脆弱，需 L1/L2/L3 三层检测兜底） | **cordis patch overlay 是官方机制**，裁剪走配置不走 hack，升级跟随性更好 |
| 嵌入成本 | 重：URL 重写、CSP hash、localStorage 自愈、锚点检测 | 轻：独立 origin 反代，零改写 |
| 反代注意点 | SSE | WebSocket（Upgrade）+ fetch RPC |
| 远程访问 | 依赖 portal（0.0.0.0 语义） | 同，且 `--host 0.0.0.0` 被上游拒绝，**必须**走 portal |
| 前置依赖 | 系统里装 `opencode` | 系统里装 `dsh`（npm 包**自带 dist**，`package.json files: ["dist"]`）；源码 checkout 需先 `pnpm build`（本地 checkout 目前无 dist） |

---

## 6. 风险与注意事项

1. **前端 dist 必须构建**：`resolveDistIndex` 找不到 `@deepseek-ai/dsh-web-frontend/dist/index.html` 会启动即失败并提示 `pnpm run build`（`packages/bundle/web-app/src/index.ts`）。用 npm 安装的 `dsh` 没问题；若要从源码 checkout 跑，需纳入实例 preStart 或预构建。dist 体积未实测（vendor chunk 含 katex/shiki/markdown，预计数 MB）。
2. **无认证面**：代理把 `settings`/`credentials`（含凭据描述能力）暴露给代理入口——portal 必须叠自己的 token；且代理需严格删 Origin、只回填 loopback Host。
3. **工作区钉子的边界语义**：沙箱限制的是"会话 cwd 内的文件效果"，不是进程级；越界会话一旦建成，其沙箱根就是越界目录（不会自动缩回）。按"只提醒不拦截"的决策，这不需要硬保证——越界创建照常成功，靠监测警告条即时暴露（JSON body 解析 + 严格相等判定，复用 `scope.go` 的归一化）；硬写限制由 dsh 沙箱按新会话的 cwd 自行生效。
4. **版本跟随**：dsh 迭代快（profile/行 id 变化），裁剪 overlay 引用的行 id（`directory-picker`、`ui-workspace` 等）可能随上游调整——建议保留 opencode 分支的"版本门 + 锚点检测"思路做兜底警告（`WORKTREE-ISOLATION.md §4.7` 模式可平移）。
5. **WebSocket 反代**：事件通道是 WS，反代必须支持 Upgrade；Go 标准 `ReverseProxy` 可用，但 portal 远程链路（若有中间层）也要保证 WS 透传。
6. **HMR 行**：`client-hmr` 在生产嵌入下可留在 profile 中无害（仅在有 dev:web watcher 时活动），也可用 overlay 禁用。

---

## 7. 结论

- **可行，且集成成本低于 opencode-web 当年**：上游形态同构（serve+内嵌 UI、`--port 0`、stdout 就绪行、优雅关停），myworktree 的 `Kind` 框架已有两个 HTTP-backed 先例，`dsh-web` kind 基本是 opencode-web driver 的裁剪复制 + 独立 origin 反代。
- **工作区限制在 harness 里是"过强"而非"缺失"**：OS 级沙箱按会话 cwd 硬限制文件效果（dsh 自带，myworktree 零介入）；myworktree 按用户决策只做 opencode 式外围工作——**禁用/置灰多工作区 UI 入口**（patch/插件裁剪）+ **观察式监测只提醒**（不拦截）+ 共享 DSH_HOME 下处理侧栏串台。
- 唯一需要投入少量新代码的点：①代理层对 `session.create`/`workspace.create` body 的**观察式**监测（只 Record 越界，不拦截）；②iframe 外常驻警告条（三态）与版本门/锚点检测的 UX 平移；③restrict overlay + 微型 client plugin 的维护（随 dsh 升级核对行 id / slot 名）；④缺依赖的交互流程（§4：LookPath 预检 + npx/安装选择对话框 + `npm prefix -g` 绝对路径启动，均免重启）。

如果后续要推进，建议的第一步是：在 worktree 里手动 `DSH_HOME=<临时目录> dsh web --port 0` 验证就绪行抓取与 `GET /` 健康探针，再跑一次带 Origin 剥离的反代冒烟，即可锁定全部风险点。

---

## 附录 A：关键代码位置索引（harness 仓库）

- CLI launcher / `web` 别名：`apps/cli/src/args.ts`
- web app 参数与 `--help`：`packages/bundle/web-app/src/startup.ts`
- web runtime glue / URL 行 / dist 解析：`packages/bundle/web-app/src/index.ts`
- web profile 组合（rows、禁用的 host 平面行、agent presets）：`packages/bundle/web-app/cordis.patch.yml`
- base 层（沙箱默认、审批策略、fs cwd、agent-loop 空表）：`packages/bundle/base/cordis.patch.yml`
- SPA dist 托管 + index tap：`packages/host/frontend-static/src/index.ts`
- `/api` 信任栅栏：`packages/client/connection/src/api-request-trust.ts`
- API 路径常量（`/api`、WS 事件路径）：`packages/client/connection/src/api-path.ts`
- `session.create` 契约与 schema（workspaceId/cwd）：`packages/host/apiproxy/src/api/sessions.ts`、`sessions.schema.ts`
- `workspace.create` 契约与 schema（任意 path）：`packages/host/apiproxy/src/api/workspace.ts`、`workspace.schema.ts`
- 工作区实体注册表：`packages/workspace/workspace/src/index.ts`
- `workspace.create`/`session.create` 处理器实现（adopt/attach 语义）：`packages/host/apiproxy/src/api-proxy.ts`
- RPC 信封与 unary 协议（驱动层自举注入用）：`packages/host/apiproxy/src/fetch/client.ts`、`packages/host/apiproxy/src/api/rpc.schema.ts`
- 沙箱模式/工作区根/审批：`packages/sandbox/sandbox/src/index.ts`、`packages/sandbox/sandbox-policy/src/index.ts`
- 优雅关停：`apps/cli/src/process-shutdown.ts`
- boot manifest 注入：`packages/client/modules/src/index.ts`（`window.__DSH_BOOT__`）
- 前端 shell 入口：`packages/client/web/src/boot.tsx`、`apps/web/vite.config.ts`
