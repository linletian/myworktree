# Issue #131 修复计划：Portal 注册表误删与心跳自愈

**状态**：v1.1（仲裁修订已实现并全绿）。
**Issue**：#131（bug，GitHub `linletian/myworktree#131`）。
**范围**：`internal/portal/portal.go`、`internal/portal/portal_test.go`、本文档、`CHANGELOG.md` 一条 Unreleased 条目。
**索引约定**：本文档的「修复后」索引一律使用**符号锚点**（函数名 / 字段名 / 常量名），不写行号——行号会随任何一次无关改动而失真，符号锚点不会。「修复前」行号是 issue 提出时已核实的源码位置，作为历史证据保留。

---

## 一、问题摘要

Portal Dashboard 的实例列表是**纯文件驱动**的：每个 daemon 启动时在
`~/.config/myworktree/portal/` 写一份 `<instance_id>.json` 注册文件，
`/api/list`（`handleList`）扫描该目录渲染成表。注册文件一旦消失，该项目在
Dashboard 上**永久消失**——除非属主 daemon 重启。

Bug 链条（修复前）：

1. `cleanupStaleRegistrations()`（修复前 portal.go:423-458，每 5 分钟一个
   ticker，**每个 daemon 都跑，不止 portal holder**）扫描注册目录，对非自己的
   条目调用 `isRegistrationAlive()` 判活，判死即 `os.Remove`。
2. `isRegistrationAlive()`（修复前 portal.go:460-480）的语义是
   「PID 存活 **且** TCP 探测成功」：PID 活着但 1 秒 loopback TCP 探测失败
   （如 OOM 前夜的瞬时抖动、端口尚未 bind、backlog 打满）→ **直接判死并删
   文件**，没有任何 PID 兜底。
3. 列表侧的 `isProcessAlive()`（修复前 portal.go:811-832）语义**相反**：
   TCP 失败时回落为「PID 存活即存活」。同一份文件，两侧判活结论可以相反，
   而清理侧是那个会动手删文件的。
4. 注册文件只在 `Portal.Start()`（修复前 portal.go:125）经 `writeRegistration()`
   写**一次**，没有任何补写/心跳路径——误删不可自愈。
5. 清理删除**完全无日志**——用户报"项目从 Dashboard 消失"时无法诊断。

本机 2026-10-25 已实际发生：一次瞬时探测失败导致注册文件被删，项目从
Dashboard 永久消失。

## 二、已核实证据（修复前：文件:行号）

| 证据 | 位置 |
| --- | --- |
| 清理循环，5 分钟 ticker，每个 daemon 都跑 | `cleanupRegistrationLoop` / `cleanupStaleRegistrations`，portal.go:268-280、423-458 |
| 「PID 且 TCP」判活、TCP 失败即删、无 PID 兜底 | `isRegistrationAlive`，portal.go:460-480（删文件在 :455） |
| 列表侧「PID 兜底」语义（两侧语义相反） | `isProcessAlive`，portal.go:811-832 |
| 注册文件一次性写入，无补写路径 | `Start()` 调 `writeRegistration()`，portal.go:124-131、482-504 |
| 删除无日志 | portal.go:454-456（裸 `os.Remove`） |

### 修复后索引（符号锚点）

| 改动 | 符号锚点 |
| --- | --- |
| `heartbeat_at` 字段（`omitempty`，空串 = legacy 文件，清理侧据此永不按 TCP/租约判删） | `registration.HeartbeatAt` |
| 生产默认 60 s 心跳常量 + 每实例可注入的周期字段 + 唯一的种子写入点 | `defaultHeartbeatInterval` / `Portal.heartbeatInterval` / `New` |
| 心跳 goroutine 的接线处（wg 计数 +1） | `Portal.Start` |
| 心跳循环：每 `p.heartbeatInterval` 重写注册文件直到 `p.done` | `Portal.heartbeatLoop` |
| 清理侧唯一权威的纯判定矩阵 | `decideRegistrationCleanup` |
| 两侧共享的判活原语（PID 存在性 / TCP 探测） | `pidAlive` / `tcpProbe` |
| 清理扫描（跳过自己）与单次删除 + 删除结果日志 | `cleanupStaleRegistrations` / `removeStaleRegistration` |
| 原子写（tmp+rename）：盖 `HeartbeatAt`、`StartedAt` 不漂移 | `Portal.writeRegistration` |
| 停机：`close(done)` → Shutdown → `wg.Wait()` → 删注册文件 | `Portal.Stop` |
| 列表侧展示语义（本次逐行等价，仅收敛到共享原语） | `handleList` / `isProcessAlive` |

## 三、决策

### D1 判活：共享原语 + 一个受控例外

两侧**共享的是判活原语，不是同一个最终结论**：

- `pidAlive(pid)` 是唯一的 PID 存在性检查（PID ≤ 0 / `FindProcess` 失败 /
  `signal(0)` 失败 → 死），清理侧与列表侧都只经它取 PID 事实。
- `tcpProbe(port, timeout)` 是唯一的 TCP 探测（1 s 超时），两侧共用同一个
  探测实现与同一个超时值，观测窗口对齐，日志可交叉比对。
- **清理侧的唯一权威是 `decideRegistrationCleanup` 这张矩阵**：清理路径上
  任何其他地方都不得再对存活性作二次判断，矩阵没返回 `remove` 的文件绝不
  被删。
- **格⑤ 是与 `/api/list` 展示语义之间唯一的有意例外**：一个 PID 存活、
  租约过期、TCP 不通的注册，列表侧（`isProcessAlive`）仍然报
  `alive=true`，而清理侧可以在格⑤把它删掉并记录
  `reason=lease-expired-tcp-unreachable`。这个不对称是刻意的，也是收敛的：
  列表是一次**展示判断**，矩阵是一次**不可逆操作**；仍然存活的属主下一次
  成功心跳就会把文件全量重建回来（≤1 个心跳间隔）。除此之外，两侧对
  「PID 死 → 死」「PID 活 + 心跳新鲜 → 活」的结论完全一致。

### D2 心跳 / 租约（自愈关键）

- `registration` 新增 `HeartbeatAt string`（RFC3339；空串 = 旧格式 legacy
  文件）。
- `writeRegistration()` 保留原子写（tmp+rename），每次统一盖
  `HeartbeatAt: time.Now().Format(time.RFC3339)`；`StartedAt` 固定为
  `p.startedAt`——**在 `New()` 时捕获一次**的那个时间戳，因此心跳重写不会把
  `/api/list` 里的 `started_at` 漂成"最后一次心跳时间"。
- 心跳周期**默认 60 s，且每实例可注入**：`defaultHeartbeatInterval` 是生产
  默认值，`New()` 用它播种 `Portal.heartbeatInterval`；`heartbeatLoop` 只读
  `p.heartbeatInterval` 建 ticker。**测试只能在 `Start()`（或它自己 spawn
  心跳 goroutine）之前覆盖这个字段**——goroutine 起来之后该字段就是只读的，
  之后再写是数据竞争而不是旋钮。
- `Portal.Start` 新增 `heartbeatLoop()`（wg 计数 +1），直到 `p.done` 退出；
  `PortalPort == 0 / RegistryDir == ""` 的早退守卫保持不变。
- **周期自愈必须由真实 `Start()` + ticker 的测试钉住**（
  `TestHeartbeatLoopPeriodicallyRewritesRegistrationFile`）。手工再调一次
  `writeRegistration()` 只能证明重建原语（`TestHeartbeatSelfHealsRegistrationFile`
  的职责），既不能证明 ticker 会按时触发，也不能证明 `Start()` 真的把
  `heartbeatLoop` 接了上去——两者都必须走真实路径。
- **自愈承诺的前提**：属主进程**存活**且**写盘成功**。心跳不是保险丝——一个
  写不进盘的属主（磁盘满、目录被 revoke 权限）不会自愈；此时 Dashboard 的
  不可见是真故障，应当被看见，而不是被兜底掩盖。
- `cleanupStaleRegistrations()` 对**非自己**条目的裁决（纯函数
  `decideRegistrationCleanup`，TCP 探测走包级可注入 `tcpProbe`，仿 `tsStatus`
  模式）：

| # | 条件 | 裁决 | reason |
| --- | --- | --- | --- |
| 1 | PID 不存活（≤0 / FindProcess 失败 / signal(0) 失败） | **删** | `pid-exited` |
| 2 | PID 存活 + `heartbeat_at` 缺失/不可解析（legacy） | **保留** | — |
| 3 | PID 存活 + 心跳新鲜（age ≤ lease） | **保留**（无论 TCP 结果——本 bug 回归点） | — |
| 4 | PID 存活 + 心跳过期 + TCP 探测成功 | **保留** | — |
| 5 | PID 存活 + 心跳过期 + TCP 探测失败 | **删** | `lease-expired-tcp-unreachable` |

补充格：PID 存活 + 心跳过期 + `Port ≤ 0`：无端口即无法 TCP 证伪，**保留**
（与列表侧对无端口注册的既有 precedence 一致）。

**自愈性质**：有了心跳重写，任何误删都会在 ≤60 s 内被属主 daemon 自愈重建
（`writeRegistration()` 全量重写），删错不再永久——前提是 §D2 上文所述的
「属主存活且写盘成功」。

**向后兼容（双向，但不完全对称）**：

- 新 daemon 读旧格式文件（无 `heartbeat_at`）：走格②，**永不**因 TCP/租约删
  它，只有 PID 死亡才删；属主下次重启自然升级为带心跳的新格式。无需迁移
  脚本。
- 旧 daemon 读**新**格式文件：旧 daemon 里跑的还是修复前那套「PID 且 TCP」
  判活，它**仍然会**在一次瞬时 TCP 探测失败后删掉新格式文件——这是本次修复
  在旧二进制上无法关闭的缺口。共存期的保护不是"旧 daemon 不删"，而是**心跳
  补回**：新格式属主 ≤60 s 重写一次，删掉的文件在一个心跳间隔内回来。
- 因此共存期内接受一个**短暂可见性缺口**（旧 daemon 误删 → ≤60 s 后被补回，
  期间该项目在 Dashboard 上不可见）。这是明确接受的代价，不是遗漏。

### D3 删除必须可诊断（先删后记）

删除小块抽成 `removeStaleRegistration(path, reg, reason)`：**先
`os.Remove(path)`，再按结果打日志**——顺序本身就是正确性的来源：日志取自
unlink 的返回值，所以失败的删除不可能被记成成功的删除。反过来（先记"已删
除"再 unlink）一旦 unlink 失败，日志里就留下一条永不成立的"已删除"，把不可
诊断换成误诊断——这正是本轮修订要消除的写法。

两种格式，互斥：

```
[portal] cleanup: removed stale registration <instance_id> (file=<entry_name> pid=<pid> port=<port> repo=<repo> reason=<reason>)
[portal] cleanup: failed to remove stale registration <instance_id> (file=<entry_name> pid=<pid> port=<port> repo=<repo> reason=<reason>): <err>
```

- `file=` 取 `filepath.Base(path)`，即**实际被操作的目录项名**；
  `instance_id` 等取自**文件内容**。两者并存是刻意的：手工改过或写坏的注册
  文件里，文件名和内容里的 instance id 可以不一致，诊断时两个都要看见。
- `failed to remove` 这一支必须把 OS 错误原样带在行尾，否则"删不掉"仍然是
  一个不可诊断的状态。
- 前缀沿用 `[portal]`；不记录 token 等敏感信息（注册文件本身亦不含
  `auth_token`，由既有测试 `TestRegistration_NoAuthTokenInFile` 钉住）。

## 四、常量取值理由

| 常量 | 取值 | 理由 |
| --- | --- | --- |
| `defaultHeartbeatInterval` | 60s | 误删后的可见性损失上界；写盘频率仍远低于清理周期（5min 内仅 5 次原子小文件重写，磁盘开销可忽略）。每实例可注入（`Portal.heartbeatInterval`），所以测试能把周期压到毫秒级而不改变生产默认。 |
| `registrationLease` | 10min | ≥ 2 个清理 ticker 周期（5min）：清理者不会在自己的清理间隙里误判别人过期；≥ 10 个心跳间隔：GC 停顿、时钟粒度（RFC3339 秒级截断）、调度抖动、满载时心跳延迟一个数量级仍租约有效。 |
| `tcpProbeTimeout` | 1s | 与列表侧 `isProcessAlive` 的拨号超时保持一致，两侧观测窗口对齐可交叉比对日志。 |

## 五、已知局限（明示，接受）

1. **SIGSTOP / 僵尸挂起**：外部 daemon 被 SIGSTOP 超过 10min 且监听 backlog
   打满时，会命中矩阵第 5 格被删——这是设计上的删除目标（真·僵尸该清）。
   若它随后恢复，属主下一次心跳（≤60s）会把文件重写回来，误删自愈；恢复前
   Dashboard 短暂不可见，接受。
2. **PID 复用窗口**：`signal(0)` 只能证明"这个 PID 上有进程"，不能证明是
   原属主。原属主死亡后、PID 被无关进程复用期间，注册文件会被判活保留
   （列表里显示为 alive 但点不进去）。租约过期 + TCP 不通的第 5 格是主要
   兜底通道；彻底根治需 pidfd（Linux-only），超出本次范围。
3. **僵尸进程（zombie）**：zombie 的 `signal(0)` 成功，判活保留；父进程
   reap 后自然进入第 1 格被清。窗口短暂，接受。
4. **时钟跳变，两个方向都要看**（`decideRegistrationCleanup` 用
   `now.Sub(heartbeat)`）：属主机与清理机同机（同一个 `~/.config` 目录），
   不存在跨机时钟偏差，剩下的只有本机 NTP 步进。
   - **回拨（时钟往回走）**：`now.Sub(heartbeat)` 变小甚至为负 → 心跳显得更
     新鲜 → 租约更不容易过期 → 只会让清理**更保守**，不会造成误删。
   - **前跳（时钟往前跳）**：心跳 age 一次性跳过租约 → 直接落到第 4/5 格。
     端口可达的活属主停在第 4 格被保留；只有"活着但此刻 TCP 不通"的属主
     会在前跳后被第 5 格删掉——而这需要跳变量 > `registrationLease`（10min）
     才会发生，且属主下一次成功心跳（≤60s）就把文件全量重建回来。残余窗口
     是"前跳之后、下一次成功心跳之前"的短暂不可见，接受。
5. **D4 不做**（见 §六）：属主 daemon 活着但心跳 writer 卡死 >10min 且 TCP
   不通时会被删；下一次成功心跳自愈。

## 六、D4 范围裁决（记录，不实现）

issue 建议 ④「Dashboard 兜底发现路径（扫 `~/.config/myworktree/<repoHash>/
server.json` 补全列表）」**本次不做**：

- D2 心跳自愈已把注册丢失的可见性损失压到 ≤60s（误删 → 下一次心跳重建）；
- ④ 引入跨目录扫描（另一套路径约定、另一套解析、又一组失败面），收益与
  复杂度不成比例；
- 此裁决评审有反证（如实测心跳自愈不覆盖某场景）可由仲裁重开。

## 七、不改的部分（评审基线）

`handleList` 的 `alive` 展示语义、`deleteRegistration()` 的内部实现与
`portal.json` 的语义、`claimerLoop` 均未改动（`isProcessAlive` 仅收敛到共享
的 `pidAlive`/`tcpProbe` 实现，判活语义逐行等价）。

`Portal.Stop()` 是本轮破例处，而它的**唯一改动是把 `p.wg.Wait()` 上移到
`p.deleteRegistration()` 之前**：`close(done)` 仍在最前（后移会死锁——所有
循环都靠它退出），HTTP `srv.Shutdown` 逻辑原样保留，`deleteRegistration()`
自身及其对 `portal.json` 的处理一字未动。改动只是让 unlink 成为**最后一个
写**：心跳 goroutine 还没退出时先删文件，会让一个卡在删除与 goroutine 退出
之间的 tick 把已停机的注册复活。

## 八、测试清单

新增于 `internal/portal/portal_test.go`（均不依赖真实 60s 周期；TCP 成功格
用 `net.Listen("tcp","127.0.0.1:0")` 真实 listener——kernel 从 backlog 应答
SYN，无需 accept；失败格用已 Close 的 listener 端口，loopback 立即
connection refused）：

| 测试 | 覆盖 |
| --- | --- |
| `TestDecideRegistrationCleanup_PidExitedRemoved` | 矩阵格 1（PID=死/0/负），且心跳新鲜+端口可达也照删 |
| `TestDecideRegistrationCleanup_LegacyHeartbeatKept` | 矩阵格 2（空/不可解析心跳 + 端口不可达 → 保留） |
| `TestDecideRegistrationCleanup_FreshHeartbeatSurvivesTcpFailure` | **矩阵格 3 = 本 bug 回归测试**：PID 活 + 心跳新鲜 + 端口不可达 → 必须保留 |
| `TestDecideRegistrationCleanup_LeaseExpiredTcpReachableKept` | 矩阵格 4 |
| `TestDecideRegistrationCleanup_LeaseExpiredTcpUnreachableRemoved` | 矩阵格 5（reason 断言；亦即 D1 那个受控例外的钉桩） |
| `TestDecideRegistrationCleanup_NoPortNeverRemoved` | 补充格：无端口不可证伪 → 保留 |
| `TestCleanupStaleRegistrations_MatrixAndRemovalLogs` | 集成：t.TempDir 构造各格 + 自己条目（sabotaged 死 PID 也按 instance ID 跳过）+ `portal.json`/非 JSON/坏 JSON 不动；删/留断言 + D3 日志断言（恰好 3 条删除日志、reason 正确、`file=` 携带真实目录项名 `dead-pid.json`/`expired-unreachable.json`、被留者不出现在删除日志、无失败日志）；其中 `mislabeled.json` 是**文件名 ≠ 内容 instance_id** 的可删夹具（文件名 `mislabeled.json`、内容 `instance_id=content-id-mislabeled`，格①），使同一行日志里的 `file=` 与位置参数 instance_id 两个字段互相无法替代——把 `file=` 改成 `reg.InstanceID+".json"` 即红 |
| `TestRegistrationLivenessConstantsPinned` | **常量取值钉桩**：`registrationLease >= 10*defaultHeartbeatInterval` 且 `registrationLease >= 2*5*time.Minute`（§四 写下的两个下界），`tcpProbeTimeout == 1*time.Second`（§四 与 CHANGELOG 的承诺值，与列表侧 `isProcessAlive` 同窗口）。行为测试对此无判别力——它们要么按租约自身构造过期（见下方夹具纪律），要么用绝对过期时间（任何租约下都过期），把租约改短到小于心跳间隔会让格③保护静默失效 = 还原 #131 原 bug，而全套行为测试仍绿 |
| `TestHeartbeatSelfHealsRegistrationFile` | 单次 `writeRegistration` 的重建/字段完整性单元测试，**不单独证明周期接线**：write → rm → write → 文件重建、`heartbeat_at` 刷新（RFC3339 秒粒度，睡 1.1s 跨过）、全字段完整、`started_at` 不漂移 |
| `TestHeartbeatLoopPeriodicallyRewritesRegistrationFile` | **周期接线**：走真实 `p.Start()`，`heartbeatInterval` 在启动前注入 20ms；`openPort` 先占住 `PortalPort`（**通常**挡住 claimerLoop 真起 HTTP：双栈机上 `localhost` 可解析 `::1` 而该 listener 在 `127.0.0.1`，故不保证；即便抢到也不影响本测试断言，心跳重建/`heartbeat_at` 前进/`started_at` 不变都与谁持有 portal 端口无关，`defer Stop()` 两支都能清理）；删注册文件后 3s deadline 内轮询等心跳重建，断言新 `heartbeat_at` 严格 After 初始值（秒粒度下"严格更新"即证明是重建之后的又一次 tick，即周期重写）、`started_at` 不变、文件可完整解析 |
| `TestHeartbeatLoopExitsOnDone` | 心跳 goroutine 随 `p.done` 退出，`Stop()` 不悬挂 |
| `TestStopRemovesRegistrationAfterHeartbeatExit` | **Stop 顺序**：200 轮独立 Portal + 独立临时 RegistryDir，启动前把 `heartbeatInterval` 钉到 1ns 让写者全程滚烫，手工 `writeRegistration()` + `wg.Add(1)` + `go p.heartbeatLoop()`，跑约 100µs 后 `Stop()`，断言 Stop 返回后 `<instance_id>.json` 确定不存在 |
| `TestRemoveStaleRegistration_RemoveFailureLogsFailure` | **删除失败可见**：非空目录 `victim.json/` 使 `os.Remove` 确定性失败（ENOTEMPTY），内容 instance_id 用 `content-id`；断言目标仍存在、日志含 `failed to remove`、`victim.json`、`content-id`、reason 与 OS 错误原文，且**成功行数为零**（按前缀锚点 `[portal] cleanup: removed ` 计数 `== 0`，不用裸子串 `removed stale registration`——文案一改裸子串就会静默匹配不到而让断言变空；失败行不会被误计，`[portal] cleanup: ` 之后是 `failed`） |

**夹具与日志断言纪律**（本轮补）：

- **过期心跳用固定时长**：`staleHeartbeat()` 返回 `time.Now().Add(-2*time.Hour)`，
  **不引用 `registrationLease` 自身**。按 `-registrationLease-1m` 构造的夹具会跟着
  被测常量一起动——把租约改短到 1 分钟，夹具仍然"过期"，格③的保护静默失效却无人
  变红。固定 2 h 明显超过任何合理租约，把"过期构造"与"租约取值"解耦；租约取值由
  `TestRegistrationLivenessConstantsPinned` 单独钉。
- **两条日志分支只用前缀锚点，且只写一次**：`cleanupSuccessLogPrefix =
  "[portal] cleanup: removed "` / `cleanupFailureLogPrefix = "[portal] cleanup:
  failed to remove "` 为包级测试常量，集成测试与失败测试共用同一拼写。锚点**不含
  尾部文案**，"某一支一条都没打"这类断言在文案改写后仍然有效（裸子串则会静默匹配
  不到）；两锚点互不误匹配（前缀之后分别是 `removed` / `failed`）。精确文案仍由
  集成测试里的 `removed stale registration <id> (` 片段钉住。
- **可删夹具至少有一个文件名与内容 instance_id 不一致**（`mislabeled.json`），否则
  `file=` 与位置参数 id 两个字段在夹具层面等价、其中之一被删掉或替换都不会被发现。

**测试纪律**（本轮明确维持的约束，评审时按此核对）：

- **不改写运行中的 `tcpProbe`**：测试不在心跳/cleanup goroutine 存活期间替换
  包级 `tcpProbe` 变量（那是无锁读的数据竞争）。需要 TCP 结果时用真实端口
  （`openPort` / `closedPort`）或纯函数入参 `now`，不注入运行中的 seam。
- **无 `t.Parallel()`**：本包测试不改写包级变量、不重定向全局 `log` 输出，
  并行会互相污染；全部串行。
- **无后台日志并发写无锁 buffer**：`log.SetOutput(&bytes.Buffer{})` 只出现在
  没有任何并发 writer 的窗口里（被测函数同步调用；未 `Start()`）。走真实
  `Start()` 的测试（如 `TestHeartbeatLoopPeriodicallyRewritesRegistrationFile`）
  完全不重定向 `log`，让 goroutine 写线程安全的默认 logger。
- **`closedPort` 端口复用**：`closedPort` 先 bind 再 Close 取端口号，理论上
  存在极小概率被第三方进程抢占复用、导致本该"不可达"的格变成可达。本机
  loopback 上实测未发生，**记录为已知限制**（不是缺陷清单里的待修项）。

存量回归：`TestCleanupDuringGoroutines`（死 PID 清理路径）、
`TestRegistryDirDeletionRecovery`（目录删除恢复）、`TestPortalPortContention` /
`TestStopAndClaimerLoopConcurrent`（Start/Stop 与 +1 后的 wg 计数、Stop 顺序
上移后的 wg 等待）全部保持绿；`go test ./internal/portal/ -count=1` 与
`-race` 全绿。

## 九、验证

- `test -z "$(gofmt -l .)"` ✅
- `go vet ./internal/portal/` ✅
- `go build ./...` ✅
- `go test ./internal/portal/ -count=1` ✅（`-race` ✅）
- `go test ./... -count=1` ✅（见最终报告）
