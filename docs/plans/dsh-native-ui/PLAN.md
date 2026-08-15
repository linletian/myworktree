# dsh-web 原生 UI 实例 — 实施计划

> **范围**：在 myworktree 中新增第 4 种 instance kind `dsh-web`（`Interactive: false`），把 `dsh web`（DeepSeek Harness 浏览器 UI）作为托管子进程嵌入实例标签页，对齐 opencode-web / reasonix 的既有模式（managed 子进程 + iframe 嵌入 + 反代）。
>
> **依据**：`FEASIBILITY.md` 为调研与决策文档（结论一句话：可行，零 dsh 源码改动）。本文档把 FEASIBILITY 落到代码：实施步骤、改动文件清单、测试 / 手测 checklist、文档更新项（文档优先原则）。
>
> **本目录文档关系**：`FEASIBILITY.md` = 上游事实 + 最终决策 + 踩坑记录；本文档 = 实施计划；`TASK.md` = PR 级勾选清单。

---

## Context

`dsh web` = 无头 HTTP server + 内嵌 SPA（`dsh --profile web` 别名），形态与 `opencode serve` 同构：前台长驻、`--port 0` OS 分配端口、SIGTERM 优雅关停、就绪行 `dsh web: http://127.0.0.1:<port>`。dsh 自带 OS 级工作区沙箱（比 opencode 的"只提醒"更强）与官方 patch / 插件机制，myworktree 侧按 opencode 式哲学做外围：数据面按 worktree 切分注册表 + 禁用跨 worktree 入口 + 观察式监测只提醒。

关键上游事实（已复核，见 FEASIBILITY §1 与附录 A）：

- `dsh web --patch <overlay> --host 127.0.0.1 --port 0`（**launcher 选项必须前置**，见 §踩坑 11）；**端口冲突无回退**（listen 失败 fail-loud）→ Spawn 恒带 `--port 0`。
- `--patch` overlay 应用顺序在 profile 自有层与用户层（`$DSH_HOME/cordis.patch.yml`）**之上**（`apps/cli/src/profile-boot.ts`）→ restrict overlay 是最后发言，用户无法覆盖。
- `/api` 信任栅栏：Host ∈ loopback/trusted + `sec-fetch-site: cross-site` 拒 + 有 Origin 时 `Origin.host === Host.host`（**无 Origin 放行**）→ 反代要点：Host 设上游 loopback、删 Origin、透传 WS Upgrade。
- 工作区注册表只在 DSH_HOME 首次初始化 bootstrap 或 `workspace.create` RPC 时注册；进程 cwd / `session.create {cwd}` **不会**自动注册 → 必须自举注入。
- 沙箱三模式 OS 级强制，workspaceRoot = 会话不可变 cwd；myworktree 零干预。

---

## 方案选型与关键决策

### 嵌入形态：每实例一个独立 loopback origin

**硬约束**：dsh SPA 的客户端 API base 硬编码 `location.origin + '/api'`（`packages/client/connection/src/api-path.ts`，无覆写开关），dist 资产为根绝对路径 → 同源子路径挂载（opencode 的 `/__opencode/<id>/` 模式）不可行（会与 myworktree 自己的 `/api` 冲突）。因此：

- **本地模式（默认）**：myworktree 在 `127.0.0.1:<free>` 监听并反代到上游（每实例一个独立 loopback origin），iframe src = `http://127.0.0.1:<proxyPort>/`。**零 SPA 注入**（无需 HTML/JS 重写）。
- **远程模式**（主监听非 loopback 或 TLS）：代理绑主监听同 host，**强制 token 门**（每请求校验 `?token=` / `mw_token` cookie，种 cookie 后剥离 token 再转发；WS Upgrade 同样校验）——dsh 无认证面，非 loopback 必须叠 myworktree token（FEASIBILITY 踩坑 9）。代理 TLS 镜像主监听证书。两种模式均保持独立 origin，SPA 零改动。

### 数据面：共享 DSH_HOME + 注册表按 worktree 隔离

- env 继承（DSH_HOME 共享：凭据/设置/profiles 免重配）；restrict overlay 只重述一行：`storage-json.root` → `<dataDir>/dsh/<gitx.HashPath(worktreePath)>/storages`（注册表按 worktree 隔离）。
- sessions 不重定向（保持默认 `~/.dsh/sessions` 共享）→ 终端裸跑 dsh（同位置）与 web 实例会话**可见、可打开、可读快照**（实时刷新仅限写入者进程内，跨进程打开活跃会话会损坏日志——dsh 上游问题，见 `CROSS-PROCESS-SESSION.md`）；跨 worktree 会话以「未分组」组可见不可点。
- **工作区自举**：就绪后直连上游 loopback（Go http.Client，无 Origin → 过信任栅栏）POST **`/api/<method>`**（wire 契约：endpoint 从 URL 路径解析，body 的 `method` 必须与 endpoint 一致——打到裸 `/api` 会 404 "not found"，见 §踩坑 13）RPC 信封 `{type:'client-request', rpcId, method, payload}`：`workspace.create {path:<worktree>}`（幂等 adopt）；常量开关 `bootstrapCreateSession=true` 时追加 `session.create {cwd:<worktree>}` 预建空白会话。失败仅告警不阻塞。自举响应里的本 worktree `workspace.id` 记录进 blob（scope 判定用）。

### 工作区限制：禁用入口、不拦截、只提醒

- restrict overlay 对目录选择器的处理（**实施修正，见 §踩坑 12**）：`directory-picker` 行是「自动组合器」——同时挂载 host 后端（提供 `directoryPicker` 服务，api-gateway 硬依赖）与 client 表面（「Add workspace…」入口）。**直接禁用该行会导致整棵插件树加载失败**（实测 dsh 0.1.0-rc.6：「1 entry did not activate … waiting for service: directoryPicker」）。正确姿势：`directory-picker.disabled: true`（停组合器）+ `insert: @deepseek-ai/dsh-host-directory-picker-browse`（裸挂 host 后端行，服务保留、无 client 表面 → 入口不渲染、可无头/远程运行）+ `client-hmr.disabled: true`（hygiene）。
- **监测（只提醒、零干预）**：代理观察 POST `/api` body 的 RPC 信封：`workspace.create {path}`、`session.create {cwd|workspaceId}`，与实例 worktree 归一化比对（`normalizeDir`/`classify` 平移自 opencode `scope.go`）；`workspaceId` 与 blob 中自举记录的 worktree workspaceId 精确比对。只 Record，不拦截、不改写、不降级。
- iframe 外常驻警告条（myworktree 自己的 DOM）三态：正常 / 越界 / 版本或裁剪失效。
- **裁剪有效性兜底（L1 + L2，平移 opencode §4.7 模式）**：L1 = 版本门 advisory 区间（行 id 随 dsh 升级漂移时前端常驻警告）；L2 = Spawn 预检跑 `dsh web --dump-config --patch <restrict.yml>`（launcher 打印合成树后退出，含 --patch overlay），校验四行存在且值正确 → blob `overlay_verified`，false 时前端按「裁剪失效」警告。L3（DOM 锚点检测）随置灰占位 client plugin 推迟（见 out of scope）。

### 缺失依赖：npx / 安装交互选择

- Spawn 预检 `exec.LookPath("dsh")`；未找到 → `ErrDshNotFound{npmAvailable, suggestedPin}` → markFailed → 前端对话框三选：
  1. **npx 启动**：`npx --yes @deepseek-ai/dsh@<pin> web --port 0 ...`（`--yes` 免确认、`@<pin>` 固定版本）；**必须 Setpgid + 杀进程组**（npx 是 dsh 父进程，只杀 npx PID 会留孤儿占端口）。
  2. **立即安装**：代跑 `npm install -g @deepseek-ai/dsh`，完成后 `npm prefix -g` 求全局 bin 目录以**绝对路径** Spawn——全程免重启（各 kind 均每次 Start 现读 `os.Environ()`，无 PATH 缓存）。
  3. **取消**：保持 failed，展示可读提示。
- launch 模式持久化于 **`<dataDir>/dsh/<worktreeHash>/launch.json`（按 worktree，不是按实例）**（`mode: path|npx|install` + `resolvedBin`），跨 myworktree 重启存活；**不改 framework**。按 worktree 的原因：framework 每次 Start/Restart 分配全新实例 id 并清掉旧实例状态目录（`methods.go` Restart → `cleanupKind`），按实例存会在"failed → 选 npx → Restart"时恰好丢失；而"这个 worktree 怎么拿到 dsh 二进制"本来就是 worktree 级属性。实例级状态目录 `<dataDir>/dsh/<id>/` 只放 restrict.yml（每次 Spawn 重新生成），`Driver.Cleanup` 随 Delete/Restart 清理。

### 版本门

- **硬门**（reasonix 先例，issue #45）：预检 `dsh --version`，解析 x.y.z 核心（容忍 `-rc.x` 后缀），核心 < `0.1.0` → fail-fast 可读错误；解析失败（dev 构建）→ 记日志放行。
- **advisory** 支持区间 `[0.1.0, 0.2.0)`（核心）：覆盖 restrict overlay 行 id / WS 路径 / 就绪行格式漂移风险 → blob `version_supported` 供前端常驻警告。
- npx pin 常量 `0.1.0-rc.6`（与真机验证版本一致，见 §实施踩坑补充；`dsh --version` 输出原始版本串）。

---

## 关键修改文件

### 后端（Go）—— 新建 `internal/instance/dsh_web/`（骨架照抄 `opencode_web` + `reasonix.Driver` 模式）

| 文件 | 内容 |
| --- | --- |
| `driver.go` | `Driver`（framework.Kind，注册名 `dsh-web`，`Interactive:false`）；`Spawn`：launch 解析 → 预检 LookPath → 版本门 → 写 restrict.yml → **L2 校验（`dsh web --dump-config --patch`，行存在且值正确 → blob `overlay_verified`）** → exec（npx 模式 `SysProcAttr{Setpgid:true}`）→ `pumpAndWatch` 抓就绪行（剥 ANSI）→ blob host/port → 起代理监听 → `ready.Close()`；tag preStart（照抄 opencode_web，`redact.Secret`）；`Stop`（SIGTERM→grace→SIGKILL；npx 模式 `syscall.Kill(-pid, ...)` 杀进程组；关代理监听；等 `exited`）；`Status`；`healthLoop`（`GET /`，5s / 3 连败 → failed）；`probeVersion`；`KindBlob`；`DshBin` 测试覆写 |
| `overlay.go` | restrict.yml 生成 + 写入 `<dataDir>/dsh/<id>/restrict.yml`（四行：`storage-json.config.root`、`directory-picker.disabled: true`、`insert: directory-picker-browse`（host 后端，见 §踩坑 12）、`client-hmr.disabled: true`；yml 用 `fmt.Sprintf` 手写，零 yaml 依赖） |
| `version.go` | `parseVersion`（x.y.z 核心 + rc 容忍）、`versionLess`、硬门 `checkVersion` |
| `launch.go` | launch.json 读写、`npm prefix -g` 解析全局 bin、npx 命令组装、resolveLaunch（path/npx/install 三模式） |
| `proxy.go` | `LoopbackProxy`：Director（Host=上游、删 Origin、`authq.StripToken`、Accept-Encoding 透传）、WS Upgrade 透传、`FlushInterval=-1`、502 error handler；非 loopback 绑定 + token 门 + TLS 镜像；body 级 RPC 信封 scope 记录；`ScopeTracker`（平移 opencode `scope.go` 结构） |
| `bootstrap.go` | 自举注入 RPC 客户端（信封构造 + 重试 3×2s + 失败仅告警 + 记录 workspaceId） |
| `testdata/dsh-mock.go` | mock 上游：打印就绪行 + `GET /` 200 + 应答 workspace.create/session.create |

### 既有文件改动

- `internal/store/state.go` — `KindDsh = "dsh-web"` 常量（`CanonicalKind` 不需要改，非空字符串直通）
- `internal/app/app.go` — `reg.Register(dsh_web.Driver{DataDir: dataDir, Logger: logger})`；路由：`GET /api/instances/dsh`、`GET /api/instances/dsh/scope`、`POST /api/instances/dsh/launch`、`POST /api/instances/dsh/install`；`Shutdown` 增 `StopAllKind(store.KindDsh)`
- `internal/tag/tag.go` — defaultTags 追加 `{ID: "dsh-web"}`（无 Command，注释同 opencode-web：硬编码启动，tag 仅作 label/env/preStart 来源）

### 前端（静态 HTML/JS）

- `internal/ui/static/kinds/dsh_web.js`（新）— `DshWebRenderer`：iframe 面板（sandbox 同 opencode）、就绪轮询（复用 `#opencode-loading` 模式）、per-instance iframe keep-alive、scope 轮询三态警告条、版本 advisory 警告、远程 `?token=` 附加（token 读取复用 `framework.js` `window.api` 模式：`mw_token` cookie → `?token=` query 回退）
- `internal/ui/static/index.html` — `#dsh-panel`/`#dsh-iframe`/警告条 DOM + CSS；tab badge；script 标签；**缺失依赖对话框**（三选：npx / 安装（进度+失败提示）/ 取消；触发：kind=dsh-web 且 status=failed 且 info 端点返回 `missing_dsh`）；`selectInstance` 面板互斥扩展

### 安全姿态

- 命令、`--host`、`--port 0` 由 Go 硬编码（用户无法经 tags.json 改写）；`--host 0.0.0.0` 本就被 dsh 拒绝（`startup.ts`）。
- 远程模式 token 门**代码硬编码不可关**（同 `--auth` 校验哲学）；非 loopback 无 token → 401。
- 代理是唯一认证点：本地模式等价主 UI 的 loopback 信任模型；远程模式 token 门把关 `settings`/`credentials`（经代理 Host=loopback 可达）。
- 不引入新 Go 第三方依赖（`httputil.ReverseProxy` + 手写 yml/版本解析）。

---

## 实施步骤（5 个 PR，依赖序，文档优先）

### PR 1：文档先行（规格落定）⚡ 最先执行，无代码

- [x] `docs/plans/dsh-native-ui/PLAN.md` — 本文档
- [x] `docs/plans/dsh-native-ui/TASK.md` — PR 级勾选清单（本文档 §实施步骤 的镜像）
- [x] `docs/PRD.md` §7 — 新增 dsh-web 实例段落（与 reasonix/opencode-web 并列）
- [x] `docs/API.md` §5 — 新增 5.14–5.17（dsh info / scope / launch / install）
- [x] `docs/ARCHITECTURE.md` — 新增 §9「dsh-web integration」（ASCII 图 + 威胁模型 + 评审检查表，仿 §8）
- [x] `docs/CHANGELOG.md` — Unreleased 条目：`feat(instance): add dsh-web kind (spec)`
- [x] `README.md` / `README.zh-CN.md` — Features 小节提及 dsh-web

**Verify**：纯 review，无 build；规格与 FEASIBILITY.md 一致性检查。

### PR 2：dsh_web 包核心（driver + overlay + 版本门 + launch 模式）

files：

- [x] `internal/instance/dsh_web/driver.go`（Spawn/就绪行/Stop/Status/healthLoop/probeVersion/preStart/Setpgid）
- [x] `internal/instance/dsh_web/overlay.go`
- [x] `internal/instance/dsh_web/version.go`
- [x] `internal/instance/dsh_web/launch.go`
- [x] `internal/instance/dsh_web/driver_test.go`（就绪行/版本门/launch 解析/Setpgid 断言）
- [x] `internal/instance/dsh_web/overlay_test.go`
- [x] `internal/instance/dsh_web/version_test.go`
- [x] `internal/instance/dsh_web/integration_test.go`（`testdata/dsh-mock.go`；Manager 全生命周期 + 双端口释放）

**Verify**：`gofmt -l .` clean；`go test ./internal/instance/dsh_web/... ./internal/instance/...` 过；pty/opencode-web/reasonix 零回归。

### PR 3：loopback 代理 + scope 监测 + 自举 + app 接线

files：

- [x] `internal/instance/dsh_web/proxy.go`（Director/WS/token 门/TLS/scope body 分类/ScopeTracker）
- [x] `internal/instance/dsh_web/bootstrap.go`
- [x] `internal/instance/dsh_web/proxy_test.go`（Director 矩阵/WS 升级/token 门/scope 分类矩阵）
- [x] `internal/instance/dsh_web/bootstrap_test.go`（信封格式 + 幂等 + 失败容忍）
- [x] `internal/store/state.go` — `KindDsh` 常量
- [x] `internal/app/app.go` — 注册 + 4 个路由 + `StopAllKind`
- [x] `internal/tag/tag.go` — `dsh-web` 默认 tag
- [x] `internal/app/app_test.go` — 路由存在性与 kind 校验

**Verify**：`go test ./...`；冒烟：起 mock dsh，直连上游验证 `/api` 信任栅栏（无 Origin 放行），经代理验证 Host 改写后放行。

### PR 4：前端 renderer + 缺失依赖对话框 + 警告条

files：

- [x] `internal/ui/static/kinds/dsh_web.js`
- [x] `internal/ui/static/index.html`（panel/警告条/badge/script 标签/对话框/面板互斥）

**Verify**：`go build ./...`；手测 checklist 本地全过。

### PR 5：远程访问验证 + 收尾

- [ ] 远程场景实测（LAN 直连 + TLS）：代理非 loopback 绑定 + token 门 + WS 透传 + iframe 跨源认证（**手测项**——代码路径已被单测覆盖，需真实远程浏览器端到端确认）；portal dashboard 对 dsh-web 的链接走现有 `?token=` 机制（评估后零改动）
- [x] 文档最终同步（CHANGELOG 完整条目、PLAN/TASK 状态标记）

**Verify**：远程浏览器实测通过；`go test ./...`、`gofmt -l .` 全 clean。

---

## Verification

### 单元测试

- `extractListeningAddress`：正常行 `dsh web: http://127.0.0.1:4096`；`\r\n`/尾部空白；ANSI 颜色码；错行 → ok=false
- 版本门：`0.1.0-rc.6` 过、`0.0.9` 拒、`dev`/解析失败放行（记日志）、`0.2.0` 过硬门但 `version_supported=false`
- overlay：四行 id（storage-json / directory-picker disabled / directory-picker-browse 挂载未禁用 / client-hmr disabled）+ 绝对路径 + yml 缩进格式
- launch：三模式解析、launch.json round-trip、`npm prefix -g` 错误容忍
- 代理 Director：Host=上游、Origin 删除、`?token=` 剥离、Accept-Encoding
- scope 分类矩阵：`session.create {cwd: 命中}` / `{cwd: 越界}` / `{workspaceId: 命中}` / `{workspaceId: 越界}` / 非 RPC body 忽略 / 非 POST 忽略
- 自举：信封 `{type:'client-request', rpcId, method:'workspace.create', payload:{path}}`；幂等；失败 3×2s 重试后放弃仅告警

### 集成测试

- `testdata/dsh-mock.go`（照抄 opencode-mock 模式）：打印 `dsh web: http://127.0.0.1:<port>`、`GET /` 200、应答 workspace.create/session.create 信封
- Manager Start 全生命周期：Spawn → ready（blob host/port/proxy_port 填充）→ health → Stop（上游 + 代理双端口释放）
- npx 模式：mock npx 脚本（sleep + 起 mock dsh），Stop 后进程组无残留（`syscall.Kill(-pid)` 断言）
- WS 透传：mock 升级应答

### 手测 checklist

```bash
# 0. 构建
go build -o myworktree ./cmd/myworktree

# 1. 启动（本地）
mkdir -p /tmp/dsh-test && cd /tmp/dsh-test && git init
./myworktree -listen 127.0.0.1:50099 -open=false
# 浏览器打开 http://localhost:50099/
```

| # | 操作 | 期望 |
| --- | --- | --- |
| 1 | worktree 下 Start dsh-web 实例 | tab 出现，iframe 内 dsh UI 加载，侧栏工作区 = 当前 worktree |
| 2 | 终端同 cwd 裸跑 `dsh web` 建会话 | web 实例侧栏可见该会话（共享 sessions 池） |
| 3 | 同 worktree 再启一个 dsh-web | 两实例独立上游/代理端口、独立标签，互不干扰 |
| 4 | devtools 直调 `session.create {cwd:/tmp/other}` | 警告条出现；切回 worktree 后消失（不拦截，会话照常建成） |
| 5 | 未装 dsh 时 Start | failed + 对话框；三选分别验证（npx 能起、install 后能起、取消保持 failed） |
| 6 | npx 模式 Stop | `ps` 无孤儿（dsh 与 npx 均退出），端口释放 |
| 7 | `dsh --version` 为 0.0.9（mock） | Start fail-fast，错误信息可读 |
| 8 | Stop/删除实例、杀 myworktree 重启 | 无孤儿进程；实例记录状态正确 |
| 9 | 远程浏览器（LAN + TLS）打开实例 | iframe 正常、WS 事件通道正常、无 token 泄漏上游 |
| 10 | 普通 PTY / opencode-web / reasonix 实例 | 零回归 |

### 验收标准

- [ ] 同一 worktree 可同时启 ≥ 2 个 `dsh-web` 实例，互不干扰
- [ ] 切换 tab 时 iframe 正确切换对应实例的会话视图
- [ ] 「启动就是对应工作区」严格成立（自举注入 workspace.create；跨 worktree 会话可见不可点）
- [ ] 终端裸跑 dsh（同 cwd）与 web 实例会话可见、可打开（快照）；跨进程实时刷新与并发打开受 dsh 上游限制（见 `CROSS-PROCESS-SESSION.md`）
- [ ] 越界只提醒不拦截；警告条三态正确
- [ ] 未装 dsh 时三选对话框全部可用；npx 模式无孤儿进程
- [ ] 版本硬门 fail-fast；advisory 区间外前端常驻警告；`overlay_verified=false`（L2 校验失败）时「裁剪失效」警告
- [ ] 远程模式无 token → 401（token 门不可关）
- [ ] PTY / opencode-web / reasonix 零回归
- [ ] 旧 `state.json` 加载成功（新增 kind 纯增量）
- [ ] `go.mod` 零新第三方依赖；`gofmt -l .` 无输出；`go test ./...` 全过
- [ ] `docs/PRD.md` §7、`docs/API.md` §5、`docs/ARCHITECTURE.md` §9、`docs/CHANGELOG.md` 同步更新

---

## 实施踩坑补充（真机验证 2026-08-15，dsh 0.1.0-rc.6）

FEASIBILITY §3 的 10 条之外，真机联调又踩到 4 个（均已修复并钉死在测试里）：

11. **launcher 参数顺序**：`dsh web` 的 `--patch`/`--dump-config` 是 launcher 层选项，`--host`/`--port` 是 app 层选项；web 子命令用 `allowUnknownOption + passThroughOptions` 解析——遇到第一个不认识的选项（app 层的 `--host`）就进入透传模式，**其后的 launcher 选项会被原样转发给 app**，app 报 `error: unknown option '--patch'` 后退出，服务器永不 boot。症状：实例卡在 starting、进程秒退。修复：**launcher 选项必须前置**：`dsh web --patch <overlay> --host 127.0.0.1 --port 0`（`TestWebArgs` 钉死）。
12. **directory-picker 不能直接禁用**：该行是自动组合器，同时挂 host 后端（`ctx.directoryPicker` 服务）与 client 表面（「Add workspace…」入口）；`api-gateway`（`dsh-host-apiproxy`）**硬依赖 `directoryPicker` 服务**，禁用后整棵插件树加载失败（`1 entry did not activate … waiting for service: directoryPicker`），就绪行打印后进程立即崩溃。修复：`directory-picker.disabled: true` + **`insert:` 裸挂 `@deepseek-ai/dsh-host-directory-picker-browse`**（browse 后端无显示依赖、可无头/远程；服务保留、client 表面不挂 → 入口不渲染）。L2 校验相应改为四行检查（`overlay_test.go` 钉死）。
13. **RPC wire 是 `POST /api/<method>`**：endpoint 从 URL 路径解析（`rpcFetchHandler`），body 的 `method` 必须与 endpoint 一致；打到裸 `/api` 返回 404 `not found`。FEASIBILITY §2.2 原文写的就是 `POST /api/workspace.create`——实现时一度简化成 `/api` 导致 bootstrap 静默失败（warn-only 吞掉了）。修复：`callRPC` 拼 `base + "/api/" + method`（`bootstrap_test.go` 的路径断言钉死）。
14. **`--dump-config`/boot 会写 `$DSH_HOME/profiles/web/cordis.yml`**：首次运行需要 DSH_HOME 可写（只读 HOME/沙箱环境下 dump 与 boot 都会 EROFS 失败）——对正常用户环境无影响，但测试与容器化部署要保证 DSH_HOME 可写。
15. **跨进程打开活跃会话会损坏日志（dsh 上游问题）**：`Session` 构造器回放日志后无条件追加 `session/end-seed`（seq = 日志长度，无锁无校验），第二个进程打开**正被其他进程活跃写入**的会话时与写入者撞 seq → 日志永久损坏（`corrupt session log: seq gap in committed region`），且会话实时更新只在写入者进程内广播、其他进程只能看到打开时刻的快照（无 fs.watch/轮询）。**即使没有 myworktree，两个终端 dsh web 打开同一活跃会话也会损坏日志**——这是 dsh 的缺陷+设计边界，mw 侧暂不处理。完整源码/磁盘证据、时间线、复现方式见 `CROSS-PROCESS-SESSION.md`。

---

## 风险与失败模式（仍有效）

1. **行 id 漂移**：dsh 升级后 restrict overlay 行 id 失效——L1 版本门 advisory 区间 + L2 `--dump-config` 校验（`overlay_verified=false` → 前端「裁剪失效」警告）；实现时验证 dsh patch loader 对未知行的行为（fail-loud 则 Spawn 报错可发现）。另注意 `directory-picker` 的 disabled/insert 组合依赖 api-gateway ↔ directoryPicker 服务契约（§踩坑 12），该契约变化同样由 L1/L2 兜底。
2. **npx 进程树**：只杀 npx PID 会留孤儿占端口——Setpgid + 杀进程组，集成测试钉死。
3. **body 解析脆性**：RPC 信封/方法名随 dsh 升级变化 → scope 记录退化为"无记录"（只提醒语义下可接受，不拦截）；解析失败不 panic、不阻塞转发。
4. **远程暴露**：非 loopback 绑定必须 token 门（代码硬编码不可关）；漏配即裸奔——测试钉死"非 loopback 无 token → 401"。
5. **首启延迟**：npx 首次下载慢 → ready 超时 60s 需覆盖；超时走既有 failed + 重启路径。
6. **自举时序**：上游 ready 行出现但 API 未完全就绪 → 自举重试 3×2s 后放弃仅告警。
7. **TLS 混合内容**：主监听 TLS 时本地 iframe 必须 https（代理 TLS 镜像），手测 #9 覆盖。
8. **dist 体积**：vendor chunk 含 katex/shiki/markdown，预计数 MB——仅影响首次加载，不阻塞。

---

## 明确不做（out of scope）

- 不 fork / 不改 dsh 源码；不做 sessions 按 worktree 重定向（丢终端互通）；不做整份 DSH_HOME 隔离（丢历史 + symlink 教训）
- 不做 iframe 内 DOM 注入/隐藏（dsh 的跨 worktree 入口由 overlay 官方机制处理——停组合器 + 不挂 client 表面，无需 DOM 手术；置灰占位 client plugin 为可选增强，推迟）
- 不引入新 Go 第三方依赖
- 旧布局/旧版本 dsh（< 0.1.0）不支持（硬门）
