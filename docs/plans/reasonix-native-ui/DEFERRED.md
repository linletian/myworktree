# reasonix-native-ui — 延后处理备忘（DEFERRED）

> 记录**延后处理且不在 issue 范围内**的事项：已评估、已有明确结论，但因环境限制 / 有意设计 / 与现有语义一致等原因不进入 issue 排期。若某条后续需要推进，先在此处更新结论再转 issue。
>
> 生成时间：2026-08-11｜分支：`feature/reasonix-native-ui`（MVP 已合入范围）

## 1. 真实环境 AI 对话端到端验证（需用户真机）

- **现状**：沙箱环境把 `~/.reasonix/.env` 遮蔽为 `/dev/null`（无 provider key），端到端验证到「submit 端点 202、消息进入 reasonix 管道」为止，**真实 AI 回复未验证**（serve.log 显示 `balance: 401 Authentication Fails`）。
- **已覆盖**：创建实例 → `/rx/<id>/` HTML+注入 → status/history 代理 → SSE 透传 → submit 202 → stop/restart/delete 全链路。
- **待用户真机**：浏览器打开 reasonix 实例，发送真实消息确认收到 AI 回复（依赖 `~/.reasonix/.env` 有效 + `[sandbox] bash` 策略——本机无 bwrap 时需 `bash="off"`）。
- **结论**：非代码缺陷，代码链路已验证；验证动作在用户真实环境执行即可，不转 issue。

## 2. Shutdown 不停 reasonix 实例（与 tty 语义一致）

- **现状**：`Server.Shutdown()` 仅关 HTTP server，不停止实例进程；myworktree 退出后 reasonix serve 继续运行（与现有 tty 实例行为一致）。
- **连接影响（评审补记）**：退出时主 server 与独立 listener（`rxSrv`）都以 **5s 优雅超时** 关停——reasonix iframe 的活跃 SSE / 长连接会被断开，**进行中的流式 AI 回复会中断**。但 serve 子进程与会话（`REASONIX_HOME`）均保留：重开 myworktree 后 `Reconcile` 接管存活 serve，重新打开实例即可继续查看/对话，无内容丢失。这是「退出进程必然断开浏览器连接」的固有折衷，与 tty 实例一致。
- **为何不转 issue**：tty 实例同样如此，单独为 reasonix 引入停机逻辑会破坏一致性；重启后 `Reconcile` 已接管存活进程（R-02），进程不会失控。
- **边界**：若未来给 tty 实例也做「退出时停实例」，reasonix 应一并纳入（届时再开 issue）。

## 3. reasonix 实例忽略 tag 的 `command` / `env` / `preStart`（有意设计）

- **现状**：`kind=reasonix` 实例固定运行 `reasonix serve`，创建时选择的 tag / command / env / preStart 被忽略（`startReasonix` 不读取）。
- **为何不转 issue**：MVP 设计如此——reasonix 实例的语义就是「跑 reasonix agent」，不是 shell 命令；忽略参数是有意的。
- **后续需求（已实现 2026-08-11）**：`StartInput.Env`（追加到 serve 进程环境，如 `HTTP_PROXY`）与 `PreStart` 钩子（serve 启动前执行、失败即中止）已实现；Manager 侧将 tag 的 `env`/`preStart` 接入 reasonix 实例（`command` 仍忽略，保持「跑 agent 而非 shell 命令」语义）。**注意**：注入的 env 追加在 `REASONIX_HOME` 之后，同 key 后值生效——**tag 的 `env` 可显式覆盖 `REASONIX_HOME`**（有意设计，便于指向自建 home 时不受驱动默认值干扰）。

## 4. PLAN.md / TASK.md 事后补写（豁免留档）

- **结论**：本次**豁免**补写 PLAN.md / TASK.md，理由已记录于评审文档 §9 流程收尾（实施已完成并经三轮评审核验、信息可追溯、原则固化为「下一个特性不再如此」）。
- **为何不转 issue**：纯流程文档，无代码任务；豁免理由已留档，无需排期。

## 5. 多实例并发的「实际收发消息」未逐实例验证

- **现状**：验证轮已实测同目录多 serve 并行启动成功（session lease 按文件路径互斥、fresh 唯一化）；但受 provider key 限制（见 §1），未逐实例验证真实对话收发。
- **结论**：进程级并发已验证；消息级验证依赖 §1 的真机环境，一并处理。

## 6. 独立源（#44 落地）在远程 https / Tailscale 场景的限制

- **现状（2026-08-11 实施 #44）**：`/rx/` 反代挂到独立 loopback listener（`127.0.0.1:<rand>`），iframe 经 `/api/instances` 返回的 `web_url` 指向该源，与 myworktree API 跨源 → 本机场景 iframe 内静默调用管理 API 的路径被切断（相对 fetch 落在独立源上得 404）。
- **限制**：**TLS / 远程（portal + Tailscale）模式**下独立源**自动回退同源**——`http://127.0.0.1:<rand>` iframe 在 https 页面内属 mixed content 且远端设备不可达 loopback，`Server.Start` 检测到 TLS 配置时跳过独立 listener，`web_url` 为空、前端 fallback `/rx/<id>/`（即 #44 之前的行为，远程行为不回归）。
- **后续项**：远程场景的独立源需要可经 portal 反代 / Tailscale 可达的地址（如绑定对外地址或 portal 转发独立端口），届时再评估。当前已记录于 `FEASIBILITY.md §9.5` 与 `app.go` Server.Start 注释。

## 7. 追溯索引（已转 issue 项，便于反查）

> 状态更新（2026-08-11）：**全部 7 个 issue 已关闭**。各条去向如下：

| 项 | 状态 | 去向 |
| --- | --- | --- |
| 测试数据目录隔离（评审 R-07） | 预存问题 | issue #43（**已关闭**：app 测试改为临时数据目录 + 预置 state，Reorder 补传 version，新增 409 断言） |
| 同源 iframe 安全加固 + 非 loopback 限制（R-01 / T-02） | MVP 后 | issue #44（**已关闭**：独立 loopback listener 仅挂 /rx/，`web_url` 跨源；TLS/远程回退同源，见 §6） |
| 上游 flag 版本检测（R-08c） | 排期 | issue #45（**已关闭**：`reasonix --version` 版本门 ≥1.22.0、waitReady 错误带 serve.log 尾部、`reasonix.CookieName` 单点） |
| 代理每请求读文件缓存（R-08g） | 排期 | issue #46（**已关闭**：driver 内存缓存 {port,token}，生命周期 = 实例进程生命周期——Start 成功写入/覆盖，Stop/Cleanup 失效，无 TTL） |
| Delete 持锁调 Stop 阻塞（T-03） | 排期 | issue #47（**已关闭**：Stop/Cleanup 移出 stateMu 临界区） |
| 侧栏折叠/隐藏（布局注入） | MVP 后第一迭代 | issue #48（**已关闭**：桌面默认折叠 + 独立 toggle 按钮 + `--mw-sidebar-w` 可配；窄屏保持原生） |
| Restart 会话保留（N-06 延伸） | 排期 | issue #49（**已关闭，保持现状**：用户确认 Restart = 全新会话是预期语义，与 tty 一致；会话延续由 Reconcile 接管保证） |
