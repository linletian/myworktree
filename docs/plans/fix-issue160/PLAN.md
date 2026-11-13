# Issue #160 开发计划：移除 Reasonix Web 实例功能

> **决策更新（2026-11-13）**：本 issue 的目标已从“适配 Reasonix 2.x / Studio 页面”改为“**移除 myworktree 的 Reasonix Web 实例功能**”。此前关于 `--page`、Studio 注入和版本缓存的适配方向全部废止，不再作为实施依据。

## 0. 目标与依据

### 最终目标

完整移除专用的 `kind=reasonix` Web 实例能力：创建入口、生命周期 driver、iframe renderer、`/rx/<id>/` 反代、独立 listener、上游契约脚本和 CI 步骤均退出当前产品。

升级时安全退休已有 Reasonix Web 实例，但不删除用户的 Reasonix 会话、配置或凭据，不卸载/覆盖上游 CLI 或 Studio，也不影响 PTY、OpenCode Web、DSH Web 及通用 Tag 能力。

### 决策依据

- 原问题已通过真实 v2.61.0 复现：无 `--page` 时当前 `/rx/<id>/` 返回 404 API-only 占位页，而实例为 `running`；证据见 [EVIDENCE.md](EVIDENCE.md)。
- 上游 [停止维护公告](https://github.com/esengine/DeepSeek-Reasonix/discussions/15110) 确认自 **2026-11-12** 起不再发布版本、修复缺陷/安全问题或处理 Issue/PR，v2.61.0 为最终版本；[仓库已归档为只读](https://github.com/esengine/DeepSeek-Reasonix)。
- 上游没有公开停止维护的具体动机，本计划不引用相关猜测。移除决策依据是已经证实的集成破坏、后续兼容维护责任和上游停止维护的事实。
- 继续适配将要求 myworktree 自行承担 Studio 页面分发、打包资源、认证、DOM 与 API 契约维护；本 issue 明确不再投入这条路线。

### 本轮边界

本轮只更新计划并追加 issue 评论，**不实施产品代码删除**。下文是后续实现的验收合同。

## 1. 完成定义（DoD）

| 场景 | 移除后的必须行为 |
|---|---|
| 创建实例 UI | 不再出现 Reasonix-Web 页签、名称输入、RX badge、Reasonix panel 或 iframe |
| API/其他创建入口 | `kind="reasonix"` 走现有 unknown-kind 拒绝路径，HTTP 创建返回 400；不得生成记录或启动进程 |
| 静态资源 | `GET /static/kinds/reasonix.js` 返回 404；主页面不再引用该资源 |
| Reasonix 反代 | `GET /rx/...` 返回 404；不再注册 handler，不再注入 Reasonix cookie |
| 独立监听 | 不再创建 Reasonix 专用 loopback listener，不再产生对应 `web_url` |
| 启动/关闭 | 不注册 Reasonix kind，不调用其 Start/Reattach/StopAllKind，不探测 Reasonix 版本 |
| 存量实例 | 启动时一次性退休旧记录并清理 `tab_order`；经过存量 pid+port 检查的存活 serve 被有界停止 |
| 用户数据 | `~/.reasonix`、自定义 Reasonix home、旧 `home/`、配置、凭据和上游程序均保持原样 |
| 其他实例 | PTY、OpenCode Web、DSH Web 的创建、切换、停止、删除、状态同步与共享测试不退化 |
| 通用 CLI | 仍可通过普通 PTY/自定义 Tag 运行用户自行安装的 Reasonix CLI/TUI；不提供专用 Web 集成 |

“代码里不再出现任何 `reasonix` 字符串”不是目标。存量迁移常量、移除说明和历史证据可以保留；不能用字符串清零代替行为验收。

## 2. 范围与非目标

### 允许修改的功能面

1. **应用与后端**：[app.go](../../../internal/app/app.go)、[reasonix_proxy.go](../../../internal/app/reasonix_proxy.go) 及直接相关测试。
2. **专用 kind**：[internal/instance/reasonix/](../../../internal/instance/reasonix/) 下的 driver、kind、contract 与测试。
3. **框架调用方**：删除 [manager_reasonix_test.go](../../../internal/framework/manager_reasonix_test.go)，更新 [manager.go](../../../internal/framework/manager.go)、[methods.go](../../../internal/framework/methods.go)、[kind.go](../../../internal/framework/kind.go) 中已失效的 Reasonix 支持说明；不借删除做通用框架重构。
4. **存量数据**：[state.go](../../../internal/store/state.go) 中保留的 legacy kind 常量及注释，以及一个新的 app 侧一次性退休实现和测试。
5. **Web UI**：[index.html](../../../internal/ui/static/index.html)、[kinds/reasonix.js](../../../internal/ui/static/kinds/reasonix.js)、[ui_test.go](../../../internal/ui/ui_test.go) 和受影响的 `internal/ui/testdata/*.test.mjs`。
6. **CI/脚本**：[go-ci.yml](../../../.github/workflows/go-ci.yml)、[verify-reasonix-contract.sh](../../../scripts/verify-reasonix-contract.sh)、[install.sh](../../../scripts/install.sh)、[RELEASING.md](../../../RELEASING.md)。
7. **当前产品文档**：[README.md](../../../README.md)、[README.zh-CN.md](../../../README.zh-CN.md)、[PRD.md](../../../docs/PRD.md)、[ARCHITECTURE.md](../../../docs/ARCHITECTURE.md)、[API.md](../../../docs/API.md)；实现发布时新增对应 CHANGELOG 条目。

### 明确不做

- 不安装、下载、内嵌或适配 Studio；不新增 `--reasonix-page-dir`，不新增 npm/Node/Go 依赖。
- 不删除或迁移 `~/.reasonix`，不读取/复制其中的会话、配置和凭据，不卸载 Reasonix CLI/Studio。
- 不删除通用 `RestartSurvivor`、`ResourceCleaner`、renderer registry 或其他 kind 的扩展接口；仅移除 Reasonix 实现和失效说明。
- 不改变 OpenCode/DSH 的 origin、认证、代理、iframe 缓存或生命周期策略。
- 不按进程名扫杀 Reasonix，不扫描进程表寻找历史孤儿，不处理非本实例创建的 serve。
- **不修改冻结的 iPadOS UI 兼容机制、设计、样式、诊断或验收口径。** [viewport_debug.js](../../../internal/ui/debugassets/viewport_debug.js) 和对应 viewport 测试即使保留旧 panel 选择器/注释，也不为字符串清理而改动；它们不再提供任何 Reasonix 功能。
- 不改写 [CHANGELOG.md](../../../CHANGELOG.md) 的既有历史，不修改 `docs/plans/reasonix-native-ui/`、历史评审或其他计划中的当时事实；只新增本次移除记录。
- 不回溯 Git 历史，不清理其他 issue 或无关注释。

## 3. 实施步骤

### A. 先实现一次性存量退休，保证删除功能不遗留进程/隐形记录

新增 app 侧小文件（建议 `internal/app/reasonix_retirement.go`）和针对性测试，在 [Server.Start](../../../internal/app/app.go#L615-L640) 中于 `ReconcileRunningOnStartup` **之前**调用。此时不再注册 Reasonix kind，不能依赖 Reattach。

**执行顺序固定为：识别旧记录 → 创建或复用本地备份 → 停止并清理旧实例 → 一次性保存新状态。** 下面按记录、进程和数据保护分别说明，不因小节排列改变这个顺序。

#### A1. 只处理明确的 legacy 记录

1. 从当前项目的 `state.json` 找出 `Kind == store.KindReasonix` 的记录。`store.KindReasonix` 仅作为旧数据识别常量保留，不再代表已注册功能。
2. 没有旧记录时不写状态、不创建备份、不扫描目录，直接返回。
3. 对每个 ID 先验证其为安全的单路径段；拒绝空值、`.`/`..`、路径分隔符和越界路径。管理目录唯一允许的位置是 `<project-data-dir>/reasonix/<legacy-id>`。
4. 从 `state.Instances` 生成删除旧记录、并从每个 `State.TabOrder` 列表删除对应 ID 的**内存副本**；其他实例、worktree、顺序和版本语义保持不变。此时尚不写盘。
5. 全部旧进程停止和管理文件清理完成后，才用 `SaveWithVersion` 一次性提交该副本并做版本检查；保存失败必须返回错误，不能留下“UI 已隐藏但磁盘仍反复恢复”的半迁移状态。

#### A2. 有界停止旧 serve，但不扩大进程权力

1. 从现有 `Driver.Health`/`Stop` 提取最小的 legacy 实现，保留 pid 文件、记录端口、进程组 TERM → 有界等待 → KILL 的既有语义；**不保留 Start、版本门、token 生成或任何新实例能力**。
2. 只有 pid/port 均有效且通过旧 Health 的“pid 存活 + 对应 TCP 端口可连接”检查时，才发送停止信号。缺文件、非法值、死 pid 或端口不通均按陈旧记录处理，不凭 store 里的孤立 PID 杀进程。
3. 停止后确认自有 pid 已退出；失败则中止本次状态提交并返回可读错误。不得按 `reasonix` 进程名、命令行模式或其他用户进程补杀。
4. 日志只记录退休数量和失败类别；不输出 token、cookie、完整命令行、用户 home 或认证文件内容。

#### A3. 数据保留与可回退性

1. 首次发现旧记录时，在对状态或管理目录做任何修改前，于同一项目数据目录准备权限 `0600` 的 `state.json.reasonix-removal.backup`：不存在时排他创建；已是普通文件时保留并复用，绝不覆盖；其他类型或创建错误则中止，且不停止进程、不删除记录。备份只存本机，不进入提交、日志或证据。
2. 旧管理目录只允许删除已知管理文件：`token`、`port`、`pid`、`serve.log`、`serve.log.1`。不要调用现有的整目录 `RemoveAll`。
3. 如果目录含早期 `home/`、旧会话或任何未知文件，保留该目录及其余内容并给出脱敏警告。只有目录为空时才用 `os.Remove` 删除目录本身。
4. 明确不触碰共享 `~/.reasonix`、自定义 home、CLI/Studio 安装目录。旧备份不能恢复已停止进程或 iframe 草稿，文档必须说明这一点。

### B. 删除后端 Web 实例实现

1. 在 [app.go](../../../internal/app/app.go) 删除：
   - Reasonix import、`rxDriver`/`rxAddr`/`rxSrv` 字段与 kind 注册；
   - [独立 listener 整段](../../../internal/app/app.go#L663-L706)；
   - [Shutdown 的 Reasonix 分支和 rxSrv 清理](../../../internal/app/app.go#L825-L866)，保留 DSH 与主 HTTP server；
   - [`/rx/` 注册](../../../internal/app/app.go#L968)；
   - [`instanceView` 的 Reasonix `web_url` 和 `reasonixWebURL`](../../../internal/app/app.go#L1860-L1926)。
2. 删除 [reasonix_proxy.go](../../../internal/app/reasonix_proxy.go) 与 [reasonix_proxy_test.go](../../../internal/app/reasonix_proxy_test.go)，不再保留 HTML 注入、cookie 注入或路径桥。
3. 完成 A 所需的最小提取后，删除 `internal/instance/reasonix/` 的 Start/driver/kind/contract 及原测试；生产代码不得再 import 该包。
4. 删除 [manager_reasonix_test.go](../../../internal/framework/manager_reasonix_test.go)，并移除 app/framework 测试 fixture 中仅为 Reasonix 注册 driver 的部分。
5. [runtime_health_test.go](../../../internal/app/runtime_health_test.go) 删除依赖 rx listener 的专门用例，保留主 listener 的 panic/runtime-health 验证；[runtime_health.go](../../../internal/app/runtime_health.go) 只更新因第二 listener 消失而失效的说明，不改采样和 meter 行为。
6. 保留通用框架接口。确认 `POST /api/instances` 的 `kind=reasonix` 由现有 Registry 返回 400 `unknown instance kind`，不新增一条平行拒绝框架。

### C. 删除 UI 入口和 renderer，保留其他 Web kind 的共享覆盖

1. 删除 [kinds/reasonix.js](../../../internal/ui/static/kinds/reasonix.js)。
2. 在 [index.html](../../../internal/ui/static/index.html) 删除：
   - Reasonix panel/frames、创建页签、`instNameRx`、脚本引用和 RX badge/CSS；
   - `WEB_UI_KINDS`、`hideWebPanels`、`renderWorkspace`、状态同步、停止/删除、modal reset 和 `doStartInstance` 中的 Reasonix 分支；
   - 已失效的 Reasonix renderer 注释。OpenCode/DSH 的共享数组、panel 和调用顺序保持原样。
3. 删除 `TestReasonixRendererKeepsFrameAlive`、Reasonix 固定命令页签等专用 Go 测试；增加主页面无 Reasonix 入口/脚本引用和静态文件 404 的精简断言。
4. 对 `web_ghost_select`、`web_delete_autoselect`、`web_status_sync` 等共享 JS 测试，把 Reasonix 这个“另一种 Web kind”的样例改为 OpenCode 或 DSH，保留切换、deactivate、自动选择、状态同步和 keep-alive 覆盖；不能只删除断言。
5. 不修改冻结的 viewport 诊断与其测试。其他文档/注释是否更新以是否仍描述当前功能为准，不做全仓字符串清洗。

### D. 移除契约、CI 和当前产品说明

1. 删除 [verify-reasonix-contract.sh](../../../scripts/verify-reasonix-contract.sh)，删除 [go-ci.yml](../../../.github/workflows/go-ci.yml#L75-L80) 的对应步骤，并移除 [RELEASING.md](../../../RELEASING.md#L68-L70) 的 SKIP 说明。
2. 更新 [install.sh](../../../scripts/install.sh#L335-L379) 的升级警告，移除 Reasonix 专属草稿和 serve 描述；保留“旧 daemon 不会热更新、优雅停止与强杀风险”的既有警告，不重写安装器。
3. 两份 README 删除 Reasonix Web 特性、kind 列表和 `REASONIX_HOME` 专属章节；通用 Tag 段落说明仍可启动任意 CLI，不新增 Reasonix 专用使用教程。
4. PRD 增加“专用 Reasonix Web 实例由 issue #160 移除”的当前决策；ARCHITECTURE §4.2 改为移除说明与 A 的存量退休合同；API §5.10 改为 `kind=reasonix` 400、`/rx/` 404、无 `web_url` 的移除说明。避免无必要地重编号其他章节。
5. 实现发布时新增一条 CHANGELOG；历史条目和历史计划不改写。

## 4. 验证计划

### 4.1 静态与针对性测试

先运行受影响包和现有浏览器层 Node 测试，不启动浏览器：

```sh
gofmt -l internal/app internal/framework internal/store internal/ui
go test ./internal/app/ ./internal/framework/ ./internal/store/ ./internal/ui/ ./internal/cli/
node --test 'internal/ui/testdata/*.test.mjs'
bash -n scripts/install.sh
```

必须包含以下新/改测试：

| 验证项 | 证据 |
|---|---|
| 无 legacy 记录 | 不写状态、不创建备份、不扫描/停止任何进程 |
| 混合旧状态 | 只移除 Reasonix 记录及其 tab ID；PTY/OpenCode/DSH 和其他顺序逐项不变 |
| 存活旧 serve | 仅对 pid+port 检查通过的自有 fixture 发停止；有界退出后才提交状态 |
| 陈旧/非法 pid/port | 不发信号，不产生进程误杀；记录按合同处理 |
| 路径穿越 ID | 不删除任何目录，返回错误 |
| 旧 `home/`/未知文件 | 内容逐字节不变；只清理允许的管理文件，不整目录删除 |
| 已有备份/备份创建失败 | 已有普通备份逐字节不变并可继续幂等退休；异常创建失败时不得先停止进程或提交状态 |
| 状态保存失败 | 返回错误且不谎报完成；备份保留，下一次启动以旧记录安全重试清理 |
| API 删除结果 | `kind=reasonix` 创建返回 400；`/rx/` 和旧 JS 返回 404；无 `web_url` |
| UI 删除结果 | 无创建页签/panel/renderer；共享 OpenCode/DSH 测试仍覆盖原通用路径 |
| CI | 工作流不再含 Reasonix 契约步骤，其他 job/step 不变 |

由于本改动跨包删除公开给内部模块的 kind， targeted 检查通过后，交付门禁再执行一次 `go test ./...` 捕获遗漏引用；不对同一失败无条件重跑。

### 4.2 独立沙盒迁移验收

在独立 worktree 构建独立二进制，使用独立数据目录和端口、`-portal-port 0`，不连接生产 daemon。只启动一个自有 fake legacy serve，不需要安装真实 Reasonix/Studio，也不需要浏览器。

1. 构造一份含 Reasonix + PTY + OpenCode/DSH、交叉 `tab_order`、旧 `home/` 标记和独立“共享 home 不应触碰”哨兵的本地状态。
2. 启动新二进制，验证：
   - fake legacy serve 已停止，其他 fixture 未收到信号；
   - Reasonix 实例及其 tab ID 消失，其他状态不变；
   - 本地备份以 0600 存在且重跑不覆盖；旧 `home/` 与共享 home 哨兵内容不变；
   - `/rx/`、旧静态 JS 均 404，创建 `kind=reasonix` 返回 400；
   - 主页面只有 Terminal/OpenCode/DSH 入口，不发起 `reasonix.js` 请求；
   - 无新增 Reasonix listener，停止后自有进程树和端口全部释放。
3. 再启动一次验证退休幂等：无旧记录时不改写状态、不生成第二份备份、不发停止信号。

达到 targeted 测试、一次全仓编译测试和上述沙盒迁移验收后停止；没有真实 Studio 验收项，因为该路线已取消。

## 5. 原子交付与回退

- 功能删除、必要的存量退休和直接相关测试/当前文档属于同一个行为变化，应作为一个可独立验证和 revert 的提交；不要把历史证据清理或无关重构混入。
- 本目录的方向调整文档与原问题取证可以独立作为文档变更，不默认夹带未来产品提交。
- 回退通过 revert 该移除提交恢复代码；本地状态备份只用于恢复实例记录，不能恢复已停止进程、运行时 token 或未发送草稿。不得把备份加入 Git。
- 提交前检查 staged diff 和文件清单，逐项扫描 token/API key、账户路径、主机名、私网地址、邮箱和本地备份。公开仓库/issue 链接可保留，任何真实状态备份、token、日志和终端证据不得提交。

## 6. 当前状态

- 原问题已复现，见 [EVIDENCE.md](EVIDENCE.md)。
- 上游停维护已核实，具体动机未公开，不能写成已知原因。
- 目标调整评论已追加并通过 API 读回确认：[issue #160 评论](https://github.com/linletian/myworktree/issues/160#issuecomment-6845396285)。
- **实现已完成（2026-11-13）**：§3.A–D 全部落地于单一原子提交 `9a91be6`（分支 `fix/fix-issue160`，基线 `a57200a`；36 文件，+2268/−5435）。存量退休实现于 `internal/app/reasonix_retirement.go` 及其测试；`kind=reasonix` 创建返回 400、`/rx/` 与 `static/kinds/reasonix.js` 返回 404、无 Reasonix listener 与 `web_url`；冻结的 iPadOS 机制两文件与基线逐字节一致。
- **验证状态**：定向测试（app/framework/store/ui/cli）全绿；`node --test` 472/472；交付门禁 `go test ./...` 21 ok/0 fail；§4.2 独立沙盒迁移验收 7 项全过（fake legacy serve 有界停止、状态逐项保留、备份 0600 幂等、哨兵逐字节不变、幂等重跑）；独立双评审（评审1号 `opencode-go/muse-spark-1.3-contributor`、评审2号 `minimax-cn/MiniMax-M3.1-Flash`）对同一冻结对象均 APPROVE，剩余发现均为 nit/INFO（FREEZE 文档数字、raw 标签可读性、`reused-open-perms` 备份告警与进程组语义为既有语义的后续建议项）。
- **未执行/边界**：未做真实 Studio 验收（该路线已取消）；`go test -race` 与 CI race 变体未在本轮复跑（CI 既有步骤保持）；`instance_log_test.go`/`manager_endoffset_test.go`/`kinds/opencode_web.js` 中历史 reasonix 注释按"不做全仓字符串清洗"保留。
