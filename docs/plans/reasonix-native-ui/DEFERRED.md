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
- **为何不转 issue**：tty 实例同样如此，单独为 reasonix 引入停机逻辑会破坏一致性；重启后 `Reconcile` 已接管存活进程（R-02），进程不会失控。
- **边界**：若未来给 tty 实例也做「退出时停实例」，reasonix 应一并纳入（届时再开 issue）。

## 3. reasonix 实例忽略 tag 的 `command` / `env` / `preStart`（有意设计）

- **现状**：`kind=reasonix` 实例固定运行 `reasonix serve`，创建时选择的 tag / command / env / preStart 被忽略（`startReasonix` 不读取）。
- **为何不转 issue**：MVP 设计如此——reasonix 实例的语义就是「跑 reasonix agent」，不是 shell 命令；忽略参数是有意的。
- **后续需求**：若需要给 serve 进程注入 env（如代理 `HTTP_PROXY`）或 preStart（如先初始化环境），可基于 `StartInput.Env` 扩展 driver（`cmd.Env` 已拼接 `os.Environ()`，加一层注入即可）——届时再开 issue。

## 4. PLAN.md / TASK.md 事后补写（豁免留档）

- **结论**：本次**豁免**补写 PLAN.md / TASK.md，理由已记录于评审文档 §9 流程收尾（实施已完成并经三轮评审核验、信息可追溯、原则固化为「下一个特性不再如此」）。
- **为何不转 issue**：纯流程文档，无代码任务；豁免理由已留档，无需排期。

## 5. 多实例并发的「实际收发消息」未逐实例验证

- **现状**：验证轮已实测同目录多 serve 并行启动成功（session lease 按文件路径互斥、fresh 唯一化）；但受 provider key 限制（见 §1），未逐实例验证真实对话收发。
- **结论**：进程级并发已验证；消息级验证依赖 §1 的真机环境，一并处理。

## 6. 追溯索引（已转 issue 项，便于反查）

| 项 | 状态 | 去向 |
| --- | --- | --- |
| 测试数据目录隔离（评审 R-07） | 预存问题 | issue #43 |
| 同源 iframe 安全加固 + 非 loopback 限制（R-01 / T-02） | MVP 后 | issue #44 |
| 上游 flag 版本检测（R-08c） | 排期 | issue #45 |
| 代理每请求读文件缓存（R-08g） | 排期 | issue #46 |
| Delete 持锁调 Stop 阻塞（T-03） | 排期 | issue #47 |
| 侧栏折叠/隐藏（布局注入） | MVP 后第一迭代 | issue #48 |
| Restart 会话保留（N-06 延伸） | 排期 | issue #49 |
