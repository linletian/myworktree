# 终端实例存活与重启拉回 — 讨论稿

> 状态：讨论稿，未决策，不含实现承诺。记录一次关于 pty 终端实例生命周期的
> 讨论：myworktree 关闭后终端及其内部进程的存亡、能否让它们存活且重启后
> 原样拉回、以及长期未回收的风险。
>
> 所有代码事实均标注文件行号，基于写稿时的 main/develop 代码。

## 1. 问题

- Q1：myworktree 进程关闭后，终端实例（pty kind）及其中的进程（如 opencode
  cli）是否自动一起被杀掉？
- Q2：有没有可能让终端实例和内部进程继续存活，并且（a）可追溯是 myworktree
  启动的，（b）下次 myworktree 启动后原样拉回，（c）拉回过程中断为零？
- Q3：如果允许长期存活，长期未回收的风险有哪些？

## 2. 现状：终端实例的生命周期（代码事实）

### 2.1 进程拓扑

- 每个终端实例是一个 `zsh -f -i`，经 `creack/pty.Start()` 启动
  （`internal/instance/pty/driver.go:87,104`）。creack/pty v1.1.24 的
  `Start` 语义是"新会话 + 控制终端"（Setsid + Setctty）：zsh 是会话首领，
  PTY slave 是它的控制终端。
- **PTY master fd（`h.ptmx`）持有在 myworktree 进程内**，保存在 per-instance
  Handle 里（`internal/instance/pty/driver.go:56`）。Resize / SendInput /
  pumpLogs 全部通过这个 fd 进行（`driver.go:251,259,262`）。
- 用户在终端里跑的前台程序（如 opencode cli）因交互 shell 的 job control
  而拥有独立进程组。

结论：**终端的命脉（master fd）握在 myworktree 手里**。这是后续一切结论的
根源。

### 2.2 关闭链路：Q1 的答案是"必死"

| 场景 | 机制 | 结果 |
|---|---|---|
| 优雅退出 | `Server.Shutdown()`（`internal/app/app.go:697-709`）**不显式杀 pty**（代码注释原文：tty instances "die when their PTY hangs up"），只显式 `StopAllKind(reasonix/dsh)`。myworktree 进程退出 → 内核关闭 ptmx | PTY hangup：内核对前台进程组与会话首领发 SIGHUP(+SIGCONT)。opencode cli（TUI，默认不捕获 HUP）直接终止；zsh 收到 HUP 后退出并把自己的作业 hup 掉 |
| kill -9 / 崩溃 / OOM | 进程死亡 → 所有 fd 关闭 | 同上，同一 hangup 链路 |

即：**终端实例与其中的 opencode cli 会随 myworktree 自动一起死，且不依赖
myworktree 的主动清理**——这是内核级 PTY 语义（谁持有 master fd，谁就是终端
的命脉），不是代码里某个显式 kill 的结果。

**逃逸口（已知）**：终端内自行 `nohup` / `setsid` / `& disown` 的进程会活
下来，但成为**无人追踪的孤儿**——全仓库无任何 `Pdeathsig` 设置（grep 确认），
这些进程躲过 SIGHUP 后彻底失联。这正是 Q3 风险的现成入口。

### 2.3 Reattach 机制现状：Q2 的可行性底子

框架已具备一套"进程存活 + 重启拉回"机制，但 pty 被刻意排除：

- `framework.Kind` 有可选的 `RestartSurvivor` 接口：`Reattach(ctx, id)`
  （`internal/framework/kind.go:356-361`）。同处注释明确写着 "Kinds whose
  processes die with the server (PTY) do NOT implement this interface"。
- `Manager.ReconcileRunningOnStartup()`（`internal/framework/methods.go:241`）
  在启动时对每条 running/starting 记录询问对应 kind：实现了
  `RestartSurvivor` 且 `Reattach` 成功 → 记录保持 running；否则标记
  stopped。
- **唯一实现者是 reasonix**（`internal/instance/reasonix/kind.go:192`）：
  serve 子进程用 `Setpgid` 启动（`reasonix/driver.go:274`），pid/port 落盘，
  `Health` = `kill(pid,0)` + TCP 探测（`driver.go:531,555`）。myworktree 被
  kill -9 后 serve 活着，重启后拉回，框架照常管理它。
- pty 不实现的原因（`docs/ARCHITECTURE.md:90`）：in-memory stdin/stdout 绑定
  无法在进程重启后恢复；且 PTY 输出 ring buffer 是纯内存态
  （`ARCHITECTURE.md:100-102,129`），重启即丢，无法回放。

对照：reasonix 能活，是因为它**不需要 myworktree 持有的 PTY**（自己的日志
文件 + 端口 + pid 文件）；pty 的生死绑在 master fd 上，问题无解于"重启后
拉回"层，只能解于"master fd 所有权"层。

### 2.4 既存的孤儿面（顺带发现）

- dsh-web kind：`Setpgid` 管道子进程（`dsh_web/driver.go:325`），Stop 杀进程
  组（`driver.go:404-422`），但**没有实现 Reattach**（全仓库仅 reasonix 有）。
  myworktree 被 kill -9 后，dsh 进程会作为孤儿存活，重启时
  ReconcileRunningOnStartup 只能把记录标 stopped——进程仍占着端口和凭证。
- 终端内 nohup/setsid 逃逸的进程：完全失联（见 2.2）。

这两类就是当前架构下"长期未回收"的真实存量。

## 3. 需求收敛

讨论收敛为一条：**myworktree 重启后，终端会话原样恢复、运行不中断**——
包含 zsh 本体（环境变量、cwd、历史）、前台 TUI 程序（opencode cli）、屏幕
内容与 scrollback。

## 4. 充要条件

只有一个根条件，其余都是它的推论：

> **把 PTY master fd 的所有权从 myworktree 移到一个比 myworktree 命长的持
> 有者。**

推论为六条验收条件：

| # | 条件 | 说明 |
|---|---|---|
| 1 | master 所有权转移 | 持有者必须 setsid、忽略 SIGHUP、独立于用户登录会话，命长于 myworktree |
| 2 | 会话身份可持久化 | 会话名 = 实例 id（如 `mw-<instanceId>`），可探活；写入 KindBlob（现 pty blob 为 `{}`，`pty/driver.go:237`） |
| 3 | 屏幕原样回放 | 回放 pane 内容 + scrollback（带属性，如 tmux `capture-pane -p -e -S -N`）喂进现有 RingBuffer；前端 `since` 游标重放契约（`ARCHITECTURE.md:231`）不变 |
| 4 | 字节流续接 | 重连后 output 继续喂 `pumpLogs`→RingBuffer→广播；input → 按键注入；resize → 设窗口尺寸 |
| 5 | Shutdown 语义反转 | 现在 reasonix/dsh 是显式杀（`app.go:707-709`）；pty 要改为 **退出 = 全体客户端断开，会话侧保留** |
| 6 | 前端零改动 | pty Kind 本就是 opaque Kind + Resize/SendInput/SubscribeOutput 扩展接口（`pty/driver.go:251,259,262`），换后端不动 WS/SSE/游标/心跳协议 |

另需说明 `PDEATHSIG` 的方向：它保证"父死子必须死"（且内核关闭 fd 先于
PDEATHSIG 投递），与内核 hangup 现在扮演的角色相同——只能用来消灭孤儿，
不能用来保活。保活必须移走 master，没有捷径。

## 5. 候选方案

### A. tmux（隔离 socket）— 推荐讨论主线

- 形态：`tmux -L mw-<dataHash>` 独立 socket（绝不碰用户自己的 tmux server）；
  spawn = `new-session -d -s mw-<id> zsh -f -i`，150ms 后 `send-keys` 注入
  tag 命令（对齐 `pty/driver.go:139-144` 现有行为）；runtime = control mode
  （`tmux -C attach`）attach，`%output` 喂现有管道；restart = Reattach =
  `has-session` 探活 + `capture-pane` 回放 + 重进 control mode。
- 天然满足三条需求：**存活**（tmux server setsid、忽略 SIGHUP、不占任何终
  端，myworktree 生死只是客户端来去）、**可追溯**（session 命名 + `tmux ls`）、
  **可拉回**（控制模式重连 + scrollback 持久化，顺带解决"ring buffer 重启
  即丢"）。
- Reattach 形状与 `reasonix.Kind.Reattach`（`reasonix/kind.go:192`）一致，
  只是探活从 pid+TCP 换成 tmux 查询。框架这条路已跑通过一次。
- 代价：新增二进制依赖；control-mode 协议解析（`%begin/%end/%error`、
  `%output`、会话通知）是最大工程面；按键/括号粘贴映射；tmux 对输出字节做
  了自身解释，个别 passthrough/sixel/OSC-8 特性与直连 pty 有差异。
- 依赖管理：写稿时本开发机未安装 tmux（`which tmux` 为空）。需像 dsh 的
  `exec.LookPath` 预检一样处理缺失（结构化错误 + 前端引导），不建议保留
  in-process pty 做双后端 fallback（两套后端的维护成本高于一个预检）。

### B. 自研 terminal supervisor

每实例一个 setsid 守护进程持有 PTY，myworktree 经 unix socket 转发 I/O。形态
上是 tmux 的最小子集，但 scrollback 持久化、多客户端、会话回收全部自踩一遍
tmux 踩过的坑。仅当需要 tmux 给不了的深度集成（如 ring buffer 与实例状态强
绑定）才考虑，否则不推荐。

### C. 保持现状 + 回收闭环（不改存活语义）

承认"终端随 daemon 生死"是设计语义，只补回收：注入 `MYWORKTREE_INSTANCE=<id>`
env（`/proc/*/environ` 可反查归属）；启动时对持久化 pid 做 starttime
校验（防 pid 复用）后杀进程组；Shutdown 显式 `StopAllKind(pty)` 把逃逸口焊
死；dsh-web 补 Reattach 或启动时强制清理。长期跑 agent 的需求走 dsh-web
kind（detached + reverse proxy 模式）。

### D. systemd --user scope / cgroup（作为 A/C 的补充）

解决不了 master fd 问题，但给每实例独立 scope/cgroup：按名字批量杀、资源
计量、崩溃后可寻回。是"可追溯 + 可回收"的 Linux 原生答案，与 A/C 正交。

## 6. "不中断"的边界

**能保证**（方案 A）：zsh 本体（环境变量、cwd、历史）、前台 opencode TUI、
其网络连接全部存活——终端里的 shell 不依赖 myworktree 的任何东西，这正是终
端比 reasonix serve（需注入 env/token）更容易做存活的原因。myworktree 优雅
退出与被 kill -9 在方案 A 下等价（都是摘除客户端）。

**不能保证**：
- 无客户端期间窗口尺寸停在旧值（重连时统一 resize，ncurses 通常自愈）；
- control-mode 的 `%output` 是 tmux 解释渲染后的字节流，个别
  passthrough/sixel/OSC-8 特性与直连 pty 有细微差异；
- 粘贴中途、半截输入等瞬时状态；
- 依赖 myworktree 自身环境/连接的进程例外（终端场景不存在此问题）；
- 登出即杀全部用户进程的环境（systemd `KillUserProcesses=yes`）下 tmux
  server 同样活不了——那种场景退回现状行为，可接受。

## 7. 长期未回收风险（若允许存活）

1. **失控代理**：opencode cli 无人值守继续烧 token、改文件、动 git worktree，
   而用户以为"关了"。会话活着 ≠ 用户知道它活着。
2. **日志账**：跨重启可回看就得把 ring buffer 落盘——项目刚因 10MB 文件截断
   造成 ~100,000× 写放大而删掉磁盘日志（`ARCHITECTURE.md:100-102`），不能
   重蹈覆辙。
3. **单点与升级 skew**：supervisor/tmux server 成为单点；老会话被新版本
   attach 的协议兼容、resize/redaction 语义差异。
4. **僵尸会话**：shell 已死但会话还在，Status 报 running 实则无人。
5. **Stop 路径必须穿过持有者**：否则 `kill -9 myworktree` 后再没人管这棵
   树——detached 架构最经典的泄漏。
6. **与 worktree 生命周期打架**：删 worktree 时里面有活进程 → git 删不掉、
   进程 hold 着已删除的 cwd 与软链凭证（reasonix 文档中
   "restart-then-delete 把持凭证的 serve 变孤儿"是缩小版）。
7. **假 reattach**：pid 复用导致探活误判——pidfd 或 `/proc/<pid>/stat`
   starttime 校验是必配。
8. **socket 安全**：隔离 socket 的目录权限（多用户机器必须私有化）。

## 8. 待决策分叉点（本文档不决策）

1. "原样"的边界：只要求活着的 shell + TUI + 当前屏幕（`capture-pane` 够
   用），还是包含完整 scrollback 历史（直接从持有者侧取，别让 myworktree 的
   RingBuffer/RAM 预算扛）？——决定回放源设计。
2. tmux 是硬依赖 + 预检，还是保留 in-process pty 做 fallback（双后端）？
3. Shutdown 改 detach 后，Stop/Delete/Restart 对会话侧的新语义（谁负责
   `kill-session`）。
4. 孤儿回收策略：shell 已死的会话、用户手动 `kill-server` 后的状态漂移。
5. C 方案（现状 + 闭环）是否作为 A 落地前的过渡step先做——尤其 dsh-web 的
   Reattach 缺口本身就是一个应当独立修复的缺陷。

## 9. 附注

- 写稿时开发环境未安装 tmux（`which tmux` 为空），依赖可用性是方案 A 的第
  一道 gate。
- 本文档为纯讨论记录，不修改任何代码。
