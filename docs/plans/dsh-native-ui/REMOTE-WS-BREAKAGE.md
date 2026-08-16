# dsh-web 远程访问侧栏空白问题报告 —— WebSocket 升级被网络路径掐断

> 记录日期：2026-08-16（真机验证 + 无头浏览器复现 + 逐字节服务端复核）
> 涉及版本：dsh 0.1.0-rc.6（npm 安装，`~/.npm-global`）；myworktree `feature/dsh-native-ui`
> 状态：**已定位**——网络路径问题（浏览器发起的 WebSocket 升级在远程路径上死亡），非 myworktree、非 dsh 代码缺陷；修复方向待决策（见 §8）
> 关联文档：`CROSS-PROCESS-SESSION.md`（单写者边界，与本问题正交）；`PLAN.md` §数据面（共享 sessions 池设计）

---

## 1. 现象

- 通过远程路径（`http://100.86.87.23:41143`，tailscale IP）打开 portal 并启动 dsh-web 实例：实例侧栏只有「**暂无会话**」——没有工作区列表、没有会话列表、没有「未分组」桶。
- 同路径下终端启动的 dsh 会话（含大量对话内容）在实例侧栏**完全不可见**——初判疑似"跨进程会话不可见"的设计问题，实为**整个会话/工作区列表从未加载**（见 §2）。
- 同一实例在本机走 `http://127.0.0.1:41143` 访问：侧栏正常（myworktree 组 + 未分组桶，终端会话全部可见）。
- 用户终端自行启动的 dsh（3080）通过 `ssh -L 3080:127.0.0.1:3080` 映射到远程本机回环访问：完全正常。
- opencode-web / reasonix 实例在同样的远程路径下：正常（原因见 §6）。
- 远程浏览器 **Safari 与 Chrome 表现一致**（均失败）——不是浏览器个例行为。

## 2. 根因链路

1. dsh SPA 的会话/工作区列表**不在页面加载时拉取**：`dsh-client-runtime` 的列表与工作区 store 只在 `handleConnected()` 里刷新（`refreshList()` / `refresh()`），而 `handleConnected` 只在客户端连接（`/api/events.mux` + `/api/events.host` 两条 WebSocket）**建立成功后**由连接循环触发。
2. 两条 WS 在远程路径上**握手阶段死亡**：浏览器控制台报 `WebSocket is closed before the connection is established`，close code 1006，`open` 事件从未触发；连接循环反复 `[web-runtime] connection lost, retry #N`（堆栈 `handleAbort → abort`，是 SPA 自己的中止级联——一条流失败后关闭另一条，属后果而非原因）。
3. 连接永远建立不起来 → `handleConnected` 永不触发 → 会话/工作区两个 store 始终为空 → 侧栏渲染空树 →「暂无会话」。终端会话并非被隐藏，而是连列表容器都没渲染出来。
4. 传输层特征（决定性）：同一路径上**普通 HTTP 全部正常**——portal 页面、SPA 资产、JSON RPC、SSE 都通；**只有 WebSocket Upgrade 握手死亡**。且服务器本机上 curl / node 的 WS 握手对 `100.86.87.23`（tailscale 自连）全部成功（101 + 首帧），浏览器（Chrome/Safari）失败 → 指向网络路径对浏览器发起的 WS 升级的特定拦截/丢弃（远程侧中间设备或链路特性），**不是 mw、不是 dsh 的代码问题**。

## 3. 浏览器侧证据（用户实测日志摘录）

```
[Error] Failed to load resource: ... 401 (Unauthorized) (index-Dqw48FrP.js.map) ×38
[Error] WebSocket connection to 'ws://100.86.87.23:41701/api/events.mux' failed:
        WebSocket is closed before the connection is established.   ← ×11 次重试
        handleAbort (client.js:10056)  abort  （匿名函数）(client.js:110)
[Warning] [web-runtime] connection lost, retry #1 … #11
```

判读：

| 条目 | 含义 |
| --- | --- |
| `.map` 文件 401 ×38 | DevTools 触发的 source map 请求不带页面 cookie（DevTools 请求方式差异）。同一 origin 的 JS 资产全部 200、SPA 正常渲染 → 与认证链路无关，**噪音** |
| WS `closed before established`，无 HTTP 状态 | 若认证失败会显示 `Unexpected response code: 401`；此处无状态 → 连接在握手完成前被网络层掐断，非 token 问题 |
| `handleAbort` 堆栈 | SPA 连接循环的 abort 级联（`dsh-client-connection` 的 `readWebSocket`：`signal` abort 时 `socket.close()`），是失败后的清理动作 |

> 注：用户最初在 **portal 页面上下文**（41143）运行诊断一行得到 `bad response from the server`——那是 mw 主 mux 无 `/api/events.mux` 路由的预期响应，**不是**实例代理端口（41701）的测试结果；本报告结论不依赖该次误测。

## 4. 服务端全链路复核（逐项通过，排除 mw/dsh 服务端）

对运行中的实例（代理端口 37781 / 41701，上游 127.0.0.1:<port>）逐项验证：

| 检查项 | 方法 | 结果 |
| --- | --- | --- |
| `session.list` | LAN+token / loopback / 上游直连 | 全部 200；返回全池 21 个会话，含全部终端会话（fb8d2f5f、e1afa9f6、d95142a8…），blank 标记正确 |
| `workspace.list` | 同上 | 200；隔离注册表 1 个工作区 + sessionIds 快照 |
| `host.describe` | 同上 | 200；`ok: true` |
| WS 101 握手 | raw socket 重放浏览器精确请求头（含 Cookie、permessage-deflate offer） | 101；`Sec-WebSocket-Accept` 与 `Sec-WebSocket-Key` **逐一计算核对全部匹配** |
| 首帧 | raw 客户端 | `session/subscribed` 正常到达（mux），host 流静默（符合预期） |
| 长连接保持 | 单条 / mux+host 成对、持 4s+ | 全部稳定，无服务端主动关闭 |
| 页面上下文裸 WS（带 cookie、双连接、DOMContentLoaded 即触发、URL 对象构造——复刻 SPA 的每种构造方式） | 无头 Chromium 走 LAN | **全部 OPEN + 帧正常** |
| 纯 stdlib `httputil.ReverseProxy`（无任何 mw 代码，仅带信任围栏的 Host 重写）跑同一场景 | 无头 Chromium 走 LAN | **同样失败** → 排除 mw 代理实现 |
| 无头 Chromium 对 `100.86.87.23` 上**手写裸回显 WS 服务器**握手 | 服务器本机沙箱 | 失败：回显服务器收到 TCP 连接但**未收到任何握手字节**，连接即被关闭（与用户浏览器签名一致） |
| 本机真实 Chromium 走 `127.0.0.1:37781` 端到端 | 侧栏 DOM | ✅ 正常：myworktree 组 5 个会话 + 未分组 4 个（fb8d2f5f、e1afa9f6、跨 worktree 会话） |

> 沙箱说明：第 8 行在 AI 会话沙箱内执行，沙箱网络对 Chrome 到 LAN IP 的套接字有已知干扰（偶发 `ERR_ADDRESS_UNREACHABLE`），属**参考证据**；决定性证据是用户真实浏览器的日志（§3）与 §4 前七行的字节级复核。

## 5. 与既有设计文档的关系

- 共享 sessions 池设计（终端同 cwd 会话在实例侧栏**可见、可打开、可读快照**，`PLAN.md` §数据面 / `CROSS-PROCESS-SESSION.md` §5）**仍然成立**；本问题不是共享池失效，而是"列表加载依赖 WS 连接"这一 dsh 客户端行为在远程路径上的暴露。
- 本问题与 `CROSS-PROCESS-SESSION.md`（单写者/跨进程打开活跃会话损坏日志）**正交**：后者管"会话能不能安全打开"，本问题管"列表能不能加载出来"。

## 6. 为什么 opencode / reasonix 不受影响

| kind | iframe 形态 | 实时长连接 | 远程路径 |
| --- | --- | --- | --- |
| opencode-web | 与 portal **同源**子路径 `/__opencode/<id>/` | **SSE**（EventSource，纯 HTTP 流式 GET；代理层有专门 SSE 处理与 fetch/EventSource/XHR 重写 shim） | ✅ |
| reasonix | 与 portal **同源**子路径 `/rx/<id>/` | **SSE**（`GET /events`，FEASIBILITY 原文 "typed event stream as Server-Sent Events"） | ✅ |
| dsh-web | **独立 origin**（每实例一个代理端口；SPA 硬编码 `location.origin + '/api'`，同源子路径会撞 mw 自己的 `/api`） | **WebSocket**（`/api/events.mux` + `/api/events.host` 两条下行 WS） | ❌ |

结论：这条远程路径只杀 **WS Upgrade**（HTTP 全通）。opencode/reasonix 的实时流是普通 HTTP GET（SSE），且挂在与 portal 同源的 origin 下，两个条件都不踩；dsh 恰好把「独立 origin + WebSocket 主连接」凑齐，成为唯一中招的 kind。

## 7. 临时方案（workaround，已验证）

沿用用户终端 dsh 的既有模式，把实例的两个端口一起走 ssh 隧道，使 iframe 变回环传输：

```bash
ssh -L 41143:127.0.0.1:41143 -L <PROXY_PORT>:127.0.0.1:<PROXY_PORT> 服务器
# 浏览器打开 http://127.0.0.1:41143
```

- portal 的 `iframe_src` 按请求 Host 派生（`handleInstanceDshInfo`），从 127.0.0.1 访问时 iframe 也指向 `127.0.0.1:<PROXY_PORT>`，经隧道回服务器回环 → WS 正常 → 侧栏正常。
- 不便之处：**实例代理端口每次启动会变**，重启实例后需从实例信息取新端口重新映射。
- 备选：直接在本机（服务器）浏览器访问 `http://127.0.0.1:41143`。
- **前端拦截（已实现，未提交）**：远程访问时，Start Instance 窗口中 dsh-web 标签的 Start 按钮禁用，并显示红色"远程不可用"提示（`internal/ui/static/index.html`；远程判定统一为 `framework.js` 的 `isRemoteAccess()`，单点维护）。这层拦截让用户在启动前就看到限制，但**不改动**根因，隧道方案仍是远程可用的唯一路径。

## 8. 修复方向（待决策，留痕见 §10）

- **方案 A（根治，中工作量）**：代理侧 **WS→SSE 桥 + 连接 shim 注入**。dsh 的 mux/host 两条 WS 是**纯下行**（客户端从不发业务帧，只有关闭动作），桥很薄：代理在 `/api/events.mux`、`/api/events.host` 上接受普通 HTTP GET（SSE 长流），自己去上游建 WS 并把帧转成 SSE 推给浏览器；同时给 SPA 注入小段 shim，把这两条路径的 `WebSocket` 构造替换为 SSE 桥。仓库内 opencode 已有注入 shim 先例（fetch/EventSource/XHR 重写）。**与 PLAN 中"dsh 不做 iframe 内注入"的既有决策冲突，需评审**。注意上游网关当前对这两条路径的普通 GET 返回 426 `upgrade required`，桥需在转发前拦截。
- **方案 B（低成本兜底）**：本报告 + 实例卡片显示当前代理端口与 ssh 隧道命令（前端已有 proxy 端口数据）。
- **方案 C（网络侧，超出 mw 范围，仅记录）**：用户侧排查远端中间设备对 `Upgrade: websocket` 的拦截（如防火墙 DPI）、或 tailscale 链路 MTU——用于确认触发拦截的具体网络环节。

## 9. 未决问题

1. 触发拦截的**具体网络环节**未定位：服务器本机 tailscale 自连（沙箱 Chromium）与远程 wireguard 路径均复现"浏览器 WS 死、curl/node WS 通"。若要继续定位：在 iframe 上下文跑 `new WebSocket('ws://'+location.host+'/api/events.mux')` 观察 OPEN/CLOSE code；或用 Chrome net-export 对比 curl 与浏览器的握手字节是否到达代理（本报告撰写时，用户的诊断一行跑在了 portal 上下文，iframe 上下文的结果仍待补）。
2. 方案 A 与"dsh 不做 iframe 内注入"决策的取舍未定（留痕见 §10）。
3. 方案 A 若实施，还需验证：SSE 桥在 401/断流时的语义对齐（`stream/error` 帧、close 传播）、多实例代理端口上的桥互不干扰。

## 10. 决策留痕

> 本节为评审要求的显式留痕：每次决策 / 状态变化追加一行，不覆盖历史。

| 日期 | 事项 | 状态 / 结论 |
| --- | --- | --- |
| 2026-08-16 | 调研结论落档（本文档） | ✅ 完成 |
| 2026-08-16 | 立案 GitHub issue #72（`bug` 标签） | ✅ 完成 |
| 2026-08-16 | 前端拦截：远程访问时禁用 dsh-web Start + 红色"远程不可用"提示（未提交，待评审） | ✅ 已实现 |
| 2026-08-16 | 前端运行时提示：iframe 顶部英文警示条 + 远程时整个 iframe 区域置灰遮罩（未提交，待评审） | ✅ 已实现 |
| 2026-08-16 | 方案 A：代理侧 WS→SSE 桥 + 连接 shim 注入 | ⏳ 待决策（与 PLAN "dsh 不做 iframe 内注入"冲突，需评审） |
| 2026-08-16 | 方案 B：实例卡片显示代理端口与 ssh 隧道命令 | ⏳ 待决策 |
| 2026-08-16 | 方案 C：网络侧排查（超出 mw 范围，仅记录） | ⏳ 待决策（不阻塞 mw 侧） |
| 2026-08-16 | 未决问题 1：定位触发拦截的具体网络环节 | ⏳ 待用户侧在 iframe 上下文复测 |
