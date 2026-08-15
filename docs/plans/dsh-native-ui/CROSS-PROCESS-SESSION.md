# dsh 跨进程会话边界 — 分析与记录（dsh 上游问题）

> 记录日期：2026-08-15（真机验证，含磁盘字节级取证）
> 涉及版本：dsh 0.1.0-rc.6（npm 安装，`~/.npm-global`）；myworktree `feature/dsh-native-ui`
> 状态：**dsh 上游问题**（一个实现缺陷 + 一个设计边界），myworktree 侧暂不处理；已损坏的会话日志按用户要求保留原状，未做任何修复或进程操作。

---

## 1. 背景（两个现象）

场景：终端手动启动的 dsh web（PID 1872705，127.0.0.1:3080，17:40 启动）里有一个正在工作的会话；myworktree 托管的 dsh-web 实例（PID 1976758，上游 127.0.0.1:34463，20:22 启动）与其共享 `~/.dsh/sessions` 会话池。

- **现象 A（不刷新）**：在 mw 嵌入的 dsh UI 里打开终端 3080 进程的会话，刚打开能看到内容（首次快照），之后会话继续进展，但嵌入 UI **永不刷新，始终停在第一次读取的进度**。
- **现象 B（日志损坏报错）**：在终端同路径再起一个 dsh web（3081 端口），读取 3080 正在工作的会话，报错：

```
历史加载失败：history unavailable for session "session-d95142a8-1d9c-45b7-9cfd-a6bb0033925d":
Error: corrupt session log: seq gap in committed region at line 12228 (expected 234435, got 234434)（internal）
```

## 2. 结论一句话

- **现象 B（日志损坏）= dsh 的实现缺陷**：`Session` 构造器回放已有日志时**无条件追加 `session/end-seed` 事件，seq = 日志当前长度**，且无文件锁、无写前校验。第二个进程打开一个**正被其他进程活跃写入**的会话时，与写入者必然撞 seq，把日志永久写坏。**即使没有 myworktree**，两个终端 dsh web 进程打开同一活跃会话也会同样损坏日志。
- **现象 A（不刷新）= dsh 的设计边界**：会话的实时事件只在**写入者进程内**广播（无 `fs.watch`、无轮询、无跨进程同步），其他进程只能读到"打开时刻"的静态历史。myworktree 的代理层只是忠实地呈现了这个快照，不是 mw 的缺陷。

## 3. 现象 B 根因：源码 + 磁盘字节证据

### 3.1 机制：构造器追加 end-seed

`dsh-session/lib/index.js`（安装包内）`Session` 构造器：

```js
this.firstLiveSeq = this.log.length;                       // 本进程下一个 seq = 种子长度
...
if (seed !== void 0 && this.log.at(-1)?.type !== "session/end-seed")
    this.append("session/end-seed", {});                   // ← 回放后无条件追加标记
```

- `append()` 分配的 `seq = this.log.length`（"The next event's sequence number — always the log length"）。
- seq 是**日志内稠密 0 起索引**；读取端 scanner 严格校验（`dsh-session-persistence-jsonl/lib/index.js:290-293`）：

```js
if (event.seq !== this.events.length) {
    const expected = this.events.length;
    ... throw new Error(`corrupt session log: seq gap in committed region at line ${line} (expected ${expected}, got ${event.seq})`);
}
```

- 错误经 `session.history` 处理器包装（`dsh-host-apiproxy/lib/index.js:2635`）：`code: "internal"`、`history unavailable for session …` —— 与现象 B 逐字吻合。

### 3.2 磁盘证据（`~/.dsh/sessions/…/session-d95142a8-…/session.jsonl.zstd`，逐帧 zstd 解压）

```
L12227: turn/end#234433            @2026-08-15 20:20:41   ← 3080 进程写入
L12228: session/end-seed#234434    @2026-08-15 20:22:39   ← 第二个进程写入 ★
L12229: agent/inbox/spliced#234434 @2026-08-15 20:24:07   ← 3080 进程写入（与 end-seed 撞 seq）
```

`end-seed` 的 seq（234434 = 它回放时看到的日志长度）与 3080 内存计数器下一个 seq（也是 234434）重复 → 之后任何第三方读取都在 line 12228 报 `expected 234435, got 234434`。

### 3.3 时间线（与进程启动时间完全吻合）

| 时刻 | 事件 |
| --- | --- |
| 17:40:19 | 终端 dsh web（3080）启动，会话 d95142a8 于 19:09 创建，活跃写入中 |
| 20:22:15 | `./mw` 守护进程启动（myworktree 仓库） |
| 20:22:28 | mw 创建 dsh-web 实例（上游 34463） |
| **20:22:39** | **用户在 mw 嵌入 UI 打开会话 d95142a8 → 34463 进程构造 live Session → 追加 `end-seed#234434` 落盘** |
| 20:24:07 | 3080 继续追加 `agent/inbox/spliced#234434` → 重复 seq，日志损坏 |
| 之后 | 3080 盲追加不受影响（内存事件流，不重读文件；日志尾部持续增长，21:04 仍在写）；3080 之外的任何进程读取该会话都报 corrupt |

### 3.4 触发路径：读是安全的，打开会写

- `session.history` 对未挂载会话走 **detached 只读路径**（`historySourceFor` → `inspectServable`，注释明确 "starts no agent, session, or turn"）——**纯读取不会写日志**。
- 但打开会话页（订阅会话事件等路径）会在 host 端**构造带 seed 的 live Session** → 构造器把 end-seed **持久化**（store attachment 异步写穿）。
- 即：dsh 的 `end-seed` 机制把"打开会话"变成了**写操作**，而对"另一进程正在活跃写入的会话"没有任何并发防护（无文件锁、无 seq 比较交换、无写前重读校验）。

## 4. 现象 A 根因：源码证据

- `Session.append()` 只通知**本进程**观察者（`invokeContainedSessionObservers(entry.emitCtx, …)`），持久化异步写穿（"The hot path never blocks on I/O"）。
- 浏览器端实时更新 = 订阅**本进程** mux WS（`/api/events.mux`）上的 `session/event` 帧（`dsh-client-connection`）。
- 全包扫描确认：session/workspace 持久化路径**没有任何 `fs.watch` / `watchFile` / 轮询**（`fs.watch` 只出现在 hmr、skill-filesystem 等无关插件）——不存在"别的进程写文件、本进程感知并刷新"的机制。
- 结论：**dsh 会话 = 单进程写者模型**（谁在跑 agent 谁写、谁广播）；其他进程只能看到"打开时刻"的静态历史。两个 dsh 进程 = 两个独立事件域。

## 5. 对 myworktree 集成的影响与后续建议（均未实施）

- myworktree 的数据面设计（共享 `~/.dsh/sessions` 池、"终端互通"）本身成立——会话**可见、可打开、可读快照**；踩雷点是"在嵌入 UI 里打开共享池中**另一个进程正在活跃写入**的会话"，这触发了 dsh 未防护的并发写入路径。
- 后续可选方向：
  1. **mw 侧**：对共享池中"疑似其他进程活跃"的会话标记只读/禁止打开（需要可靠的活跃性判定手段，如会话 mtime + 进程归属探测——本身也是猜测，仅缓解）；
  2. **dsh 上游**：`end-seed` 只在真正驱动会话（prompt/request）时写，且写前重读校验/加锁；这是根治方向；
  3. **已损坏日志**：dsh 持久化层自带 `repair()`（截断到安全偏移），可恢复"损坏点之前"的历史；属写操作，本次未执行。
- 当前现状（按用户要求保留）：`session-d95142a8-…` 日志含重复 seq，3080 之外所有进程读取都报 corrupt；3080 自身不受影响。

## 6. 复现方式（如需向 dsh 上游报告）

1. 同一 `DSH_HOME` 下起两个 `dsh web`（如 3080 / 3081 端口）；
2. 在 A 进程的 UI 里新建会话并持续对话（保持活跃写入）；
3. 在 B 进程的 UI 里打开该会话（触发 live Session 构造 → end-seed 追加）；
4. B 或任何第三方进程再次读取 → `corrupt session log: seq gap in committed region`。
