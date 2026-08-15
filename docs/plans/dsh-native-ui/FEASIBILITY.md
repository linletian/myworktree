# DeepSeek Harness Web UI 原生实例化 — 方案与最终决策

> 目标：把 `dsh web`（DeepSeek Harness 浏览器 UI）做成 myworktree 的 `dsh-web` 实例 kind，对齐 opencode-web 的既有模式（managed 子进程 + iframe 嵌入 + 反向代理）。
> 调研对象：`/home/linletian/GithubRepo/deepseek-harness/`（web UI 相关：`apps/web`、`packages/client/*`、`packages/bundle/web-app`、`apps/cli`、`packages/workspace`、`packages/sandbox`）。
> 调研时间：2026-08-15；分支 `feature/dsh-native-ui`（基线 `main@8247a4f`）。
>
> **实施修订（2026-08-15 真机验证，见 PLAN.md §实施踩坑补充）**：① §2.3「restrict overlay 禁用 directory-picker 行」在本版 dsh（0.1.0-rc.6）不成立——该行是自动组合器，api-gateway 硬依赖其 host 后端的 `directoryPicker` 服务，直接禁用会整树加载失败；已改为「停组合器 + `insert` 裸挂 `-browse` host 后端行（服务保留、client 表面不挂）」。② §2.1 Spawn 命令的 flag 顺序须为 `dsh web --patch <overlay> --host 127.0.0.1 --port 0`（launcher 选项前置，否则 `--patch` 被透传给 app 层报 unknown option）。③ §2.2 的 `POST /api/workspace.create` wire 表述正确（endpoint 从 URL 解析，body `method` 须一致）。④ **跨进程会话边界（dsh 上游问题）**：§2.2 的"会话跨进程互通"需限定为"可见、可打开、可读快照"——dsh 会话实时事件只在写入者进程内广播（无跨进程同步），且第二个进程打开活跃会话会因 `session/end-seed` 无锁追加撞 seq 而损坏日志（实测：`corrupt session log: seq gap in committed region`）；完整分析见 `CROSS-PROCESS-SESSION.md`。
>
> **结论一句话**：可行。`dsh web` = 无头 HTTP server + 内嵌 SPA，形态与 `opencode serve` 同构；dsh 自带 OS 级工作区沙箱（比 opencode 的"只提醒"更强）；myworktree 侧按 opencode 式哲学做外围——数据面按 worktree 切分注册表 + 禁用跨 worktree 入口 + 观察式监测只提醒，全部经 dsh 官方 patch / 插件机制实现，**零 dsh 源码改动**。

---

## 1. 上游关键事实（决策依据）

### 1.1 进程与启动

- `dsh web` = `dsh --profile web`（`apps/cli/src/args.ts`）：前台长驻 HTTP server，由 `frontend-static` 托管 `apps/web` 构建的 `dist/`，`dist/index.html` 经 index tap 注入 `window.__DSH_BOOT__`。**不依赖 TTY**；SIGTERM/SIGINT 优雅关停（5s 宽限，`apps/cli/src/process-shutdown.ts`）→ 与 myworktree `Kind.Stop` 语义对齐。
- flags：`--host`（默认 `127.0.0.1`）、`--port`（默认 `3080`；**`0` = OS 分配空闲端口**）、`--trusted-host`；`--host 0.0.0.0` 被显式拒绝（`packages/bundle/web-app/src/startup.ts`）。
- 就绪信号：`dsh web: http://127.0.0.1:<port>`（打印 OS 实分配端口）。
- **端口冲突语义**：webserver 无"端口被占就换一个"的回退——listen 失败即 boot fail-loud 退出（`packages/host/webserver/src/index.ts`）。→ dsh-web 的 Spawn **必须恒带 `--port 0`**。

### 1.2 认证与信任栅栏（`packages/client/connection/src/api-request-trust.ts`）

- **无认证层**。`/api` 栅栏：①Host ∈ loopback / trustedHosts；②`sec-fetch-site: cross-site` 拒；③有 Origin 时 `Origin.host === Host.authority`（**无 Origin 放行**）。
- 反代要点：Host 设为上游 loopback、**删除 Origin**、透传 WebSocket Upgrade（事件通道是 WS：`/api/events.mux`、`/api/events.host`）。
- `settings`/`credentials` 等 loopback-gated 接口，经代理后 Host 即 loopback → 远程访问必须由 myworktree portal 自己叠 token。

### 1.3 会话 / 工作区 / 沙箱模型

- 单进程多会话多工作区；会话由 client 请求创建，按 agent presets 挂载；`session.create` 接受任意 `cwd`，`workspace.create` 接受任意 `path`。
- 持久化：工作区注册表在 `$DSH_HOME/storages`，会话 jsonl 在 `$DSH_HOME/sessions`，凭据 `~/.dsh/.credentials.yaml`、设置 `settings-file`、profiles 都在 `$DSH_HOME` 下。
- **注册时机**（首次运行列表为空的原因）：注册表只在①该 DSH_HOME 首次初始化时从已有 session 头按 cwd bootstrap（仅一次），②`workspace.create` RPC——**进程 cwd 不会自动注册**，`session.create {cwd}` 也不注册工作区。
- `sessions.list` 全量返回（无工作区过滤）→ 未被本注册表认领的会话进侧栏「未分组」组（`ui-workspace/src/client/tree.ts`）。
- 沙箱（`packages/sandbox/sandbox-policy/src/index.ts`）：`read-only` / `workspace-write` / `danger-full-access` 三模式；**workspaceRoot = 会话不可变 cwd**；OS 级强制（bwrap/Landlock/Seatbelt）；审批默认 `ask`；每会话可运行时切换（`sandbox/mode` 事件）。

---

## 2. 最终决策

### 2.1 实例形态与生命周期

| 环节 | 决策 |
| --- | --- |
| Kind | `internal/instance/dsh_web/driver.go`，注册名 `dsh-web`，`Interactive: false`（骨架复制 opencode-web） |
| Spawn | `dsh web --host 127.0.0.1 --port 0 --patch <restrict overlay>`；`cmd.Dir = WorktreePath`；env 继承（DSH_HOME 共享，免重配） |
| 就绪 | 抓 stdout `dsh web: http://127.0.0.1:(\d+)` → 写 blob → `ReadySignal.Close()` |
| 健康 | 定时 `GET /`（SPA index 恒 200），连续失败置 failed |
| 停止 | SIGTERM → 5s 宽限 → SIGKILL |
| **嵌入形态** | **每实例一个独立 loopback origin**：myworktree 在 `127.0.0.1:<free>` 监听并反代到上游（Host=上游 loopback、删 Origin、透传 Upgrade），iframe src=`http://127.0.0.1:<proxyPort>/`。必须独立 origin：客户端 API base 硬编码 `location.origin + '/api'`，dist 资产为根绝对路径，同源子路径挂载会与 myworktree 自己的 `/api` 冲突 |
| 远程访问 | portal 现有反代叠 token，转发到上述 loopback 监听（`internal/portal/portal.go` 已有 AuthToken/CSRF 框架） |

### 2.2 数据面：共享 home + 注册表按 worktree 隔离

- **DSH_HOME 共享**（凭据 / 设置 / profiles 免重配、免 symlink）；restrict overlay 只重述一行：
  `storage-json.root` → `<myworktree-data>/dsh/<worktree-hash>/storages`（注册表按 worktree 隔离）。
- **sessions 不重定向**（保持默认 `~/.dsh/sessions` 共享）。
- 效果：
  - 工作区列表 = 本 worktree（注册表隔离）+ 启动自举注入 → **"启动就是对应工作区"** 严格成立；
  - 同 worktree 会话跨进程**可见、可打开（快照）**（共享 sessions 池 + 按 cwd 归组；实时刷新仅限写入者进程，跨进程打开活跃会话会损坏日志——dsh 上游问题，见 `CROSS-PROCESS-SESSION.md`）；
  - **终端裸跑 dsh（同位置）与 web 实例会话互通（快照级）**；
  - 跨 worktree 会话以「未分组」组可见（无对应工作区行）——可见不可点，与"防误操作、只提醒"哲学一致。
- **工作区自举**：就绪后直连上游 loopback 打一次 `POST /api/workspace.create`（RPC 信封 `{type:'client-request', rpcId, method, payload:{path:<worktree>}}`；幂等 adopt 语义、新建的 prepend 到列表最前）；可选再打 `session.create {cwd}` 预建空白会话直接落到会话页；失败仅告警不阻塞。
- 明确不采用（曾评估后否决）：
  - sessions 也按 worktree 分家 → 代价是裸终端 dsh 与 web 实例会话面分家（终端拿不到 web 会话），与"终端互通"需求冲突；
  - 每 worktree 一份完整 `DSH_HOME` → 丢历史 + 凭据/设置要 symlink（reasonix issue #56 教训）。

### 2.3 工作区限制：opencode 式——禁用入口、不拦截、只提醒

- **沙箱硬强制是 dsh 自带规则，myworktree 零干预**（会话 cwd 外的文件写入被 OS 级拒绝，越界会话的沙箱根 = 越界目录）。
- 入口处置：
  - "Add workspace…" + 项目选择菜单：restrict overlay 禁用 `directory-picker` 行 → directory-flow hole 空置 → 入口不渲染（官方语义）。**置灰可见**（可选增强）：微型 client plugin 占据 `sidebar.workspaces.directoryFlow` / `conversation.hero.workspace.directoryFlow` 两个 hole，渲染禁用态占位。
  - 侧栏工作区行：**不置灰**（无行级 slot，DOM 注入脆弱）——保持可见可点，越界点击由监测警告兜底。
- **监测（只提醒、零干预）**：proxy 观察 `session.create {workspaceId|cwd}` / `workspace.create {path}` 的 body，与实例 worktree 归一化比对，**只 Record 越界状态，不拦截、不改写、不降级**（结构照抄 opencode `scope.go` 的 `normalizeDir`/`classify`/`ScopeTracker`）。
- iframe 外常驻警告条（myworktree 自己的 DOM），三态：正常 / 越界（`⚠ dsh 已离开 worktree 范围: <dir>`）/ 裁剪失效；**版本门 L1 + DOM 锚点检测 L2/L3** 兜底（防 dsh 升级后入口裁剪静默失效，模式平移 opencode `WORKTREE-ISOLATION.md §4.7`）。
- 语义：用户主动越界（点别的 workspace、devtools 直调 API）照常成功，仅警告条常驻直到切回 worktree。

### 2.4 缺失依赖：npx / 安装交互选择

- Spawn 预检 `LookPath("dsh")`；未找到 → 专用错误 `ErrDshNotFound{npmAvailable, suggestedPin}` → `markFailed` → 前端 `kinds/dsh_web.js` 弹对话框三选：
  1. **用 npx 启动**：`npx --yes @deepseek-ai/dsh@<pin> web --port 0`（`--yes` 免确认、`@<pin>` 固定版本免联网检查；**必须 `Setpgid` + 杀进程组**，npx 是 dsh 父进程，只杀 npx PID 会留孤儿占端口）；
  2. **立即安装**：代跑 `npm install -g @deepseek-ai/dsh`，完成后 `npm prefix -g` 求全局 bin 目录以**绝对路径** Spawn——全程免重启（各 kind 均每次 Start 现读 `os.Environ()`，无 PATH 缓存）；
  3. **取消**：保持 failed，展示可读提示。
- 版本门：沿用 reasonix 的硬门先例（`checkVersion`，issue #45），具体范围 PLAN 阶段定。

---

## 3. 踩坑记录（过程教训）

1. **端口冲突无回退**：多个 dsh 不带 `--port` 都抢默认 3080，第二个 boot 即失败退出 → Spawn 恒带 `--port 0`。
2. **Origin 头必须删**：栅栏要求 `Origin.host === Host.host`，代理若透传浏览器 Origin 必 403；删掉即可（"Absent Origin is fine"）。
3. **必须独立 origin**：`/api` 与静态资产都是 origin 根路径，同源子路径挂载（opencode 的 `/__opencode/<id>/` 模式）不可行。
4. **注册表不自动收 cwd**：首次运行工作区为空、新目录不会自动加入是 dsh 设计行为 → 必须靠自举注入 `workspace.create`。
5. **sessions 分家的代价**：曾考虑 sessions 也按 worktree 重定向实现"严格会话隔离"——代价是实例退出后终端同位置起的 dsh 拿不到会话（纯目录不同，与进程生命周期无关）→ 最终选择共享 sessions。
6. **整份 DSH_HOME 隔离不可取**：丢历史 + 凭据/设置需 symlink（reasonix issue #56 的教训）→ 只重定向 storages 一行。
7. **npx 进程树清理**：npx 是 dsh 的父进程，清理必须 `Setpgid` 杀 `-pid` 进程组（opencode-web 现只杀单 PID，不可照抄）。
8. **dist 必须构建**：源码 checkout 需 `pnpm build`；npm 安装的包自带 dist（`files: ["dist"]`），npx/安装路径天然可用。
9. **无认证面**：经代理后 `settings`/`credentials` 对代理入口全暴露 → portal 必须叠自己的 token。
10. **版本门 + DOM 锚点检测**：裁剪依赖行 id / DOM 结构，dsh 升级可能静默失效 → L1/L2/L3 检测平移自 opencode 分支。

---

## 4. 实现清单（myworktree 侧改动点）

- 新 kind `internal/instance/dsh_web/`：driver（Spawn/就绪/健康/停止/预检）+ 观察式 scope 监测 + 反代（独立 loopback origin、删 Origin、WS 透传）——骨架复制 `opencode_web`。
- restrict overlay 生成：Spawn 前在实例状态目录写 yml（重述 `storage-json.root`；禁用 `directory-picker` 行）。
- 自举注入：`workspace.create`（可选 `session.create`）RPC 客户端，失败仅告警。
- 前端 `kinds/dsh_web.js`：缺失依赖对话框（npx/安装/取消）、三态常驻警告条、版本警告；scope 查询端点 `/api/instances/dsh/scope`。
- portal：叠 token + 保证 WS Upgrade 透传。
- 推进第一步（冒烟）：worktree 内手动 `dsh web --host 127.0.0.1 --port 0` 验证就绪行抓取与 `GET /` 健康探针，再跑一次带 Origin 剥离的反代冒烟。

---

## 5. 风险与注意事项（仍有效）

1. dist 体积未实测（vendor chunk 含 katex/shiki/markdown，预计数 MB）。
2. 无认证面（见踩坑 9）。
3. 只提醒语义：越界会话可建成（其沙箱根=越界目录，硬写限制照旧），靠警告条即时暴露，不保证"不可发生"。
4. 版本跟随：restrict overlay 引用的行 id（`directory-picker` 等）随 dsh 升级需核对；版本门兜底。
5. WS 反代：portal 远程链路要保证 Upgrade 透传。
6. `client-hmr` 行生产嵌入无害（仅在有 dev:web watcher 时活动），可 overlay 禁用。

---

## 附录 A：关键代码位置索引（harness 仓库）

- CLI launcher / `web` 别名：`apps/cli/src/args.ts`
- web app 参数与 `--help`：`packages/bundle/web-app/src/startup.ts`
- web runtime glue / URL 行 / dist 解析：`packages/bundle/web-app/src/index.ts`
- web profile 组合（rows、禁用的 host 平面行、agent presets）：`packages/bundle/web-app/cordis.patch.yml`
- base 层（沙箱默认、审批策略、fs cwd、agent-loop 空表）：`packages/bundle/base/cordis.patch.yml`
- SPA dist 托管 + index tap：`packages/host/frontend-static/src/index.ts`
- webserver 监听/端口语义（EADDRINUSE fail-loud、`--port 0`）：`packages/host/webserver/src/index.ts`
- `/api` 信任栅栏：`packages/client/connection/src/api-request-trust.ts`
- API 路径常量（`/api`、WS 事件路径）：`packages/client/connection/src/api-path.ts`
- `session.create` 契约与 schema（workspaceId/cwd）：`packages/host/apiproxy/src/api/sessions.ts`、`sessions.schema.ts`
- `workspace.create` 契约与 schema（任意 path）：`packages/host/apiproxy/src/api/workspace.ts`、`workspace.schema.ts`
- 工作区实体注册表（bootstrap/归组）：`packages/workspace/workspace/src/index.ts`
- `workspace.create`/`session.create` 处理器实现（adopt/attach 语义）：`packages/host/apiproxy/src/api-proxy.ts`
- RPC 信封与 unary 协议（驱动层自举注入用）：`packages/host/apiproxy/src/fetch/client.ts`、`packages/host/apiproxy/src/api/rpc.schema.ts`
- 沙箱模式/工作区根/审批：`packages/sandbox/sandbox/src/index.ts`、`packages/sandbox/sandbox-policy/src/index.ts`
- 优雅关停：`apps/cli/src/process-shutdown.ts`
- boot manifest 注入：`packages/client/modules/src/index.ts`（`window.__DSH_BOOT__`）
- 前端 shell 入口：`packages/client/web/src/boot.tsx`、`apps/web/vite.config.ts`
- 侧栏工作区槽位与分组：`packages/client/ui-workspace/src/client/contract/slots.ts`、`packages/client/ui-workspace/src/client/tree.ts`
