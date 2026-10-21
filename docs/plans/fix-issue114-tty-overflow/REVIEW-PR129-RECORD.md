# REVIEW-PR129-RECORD — PR #129 评审修复双评审闭环记录(2026-10-21)

对象:工作树未提交 diff(基线 HEAD=a8d93c8,12 文件,初版 +486/−388 → 返工后 +575/−388),
对应 TASK-PR129-fixes.md(P1 守卫移除 / P2 失败路径 / P3 liveFrames / R2-A env 上界)。
前置:P0 合并提交 a8d93c8 已先获双 APPROVE(评审 A 纯 APPROVE;评审 B APPROVE-WITH-NITS,
树级独立复现 + archive 副本全量复跑绿,4 nit 皆流程项)。

## Round 1(编码者初版)

- **评审 A(opencode-go/muse-spark-1.3-contributor):APPROVE-WITH-NITS**
  - 四主题逐项核验通过(范围/契约/纪律面全绿,实跑五组命令)。
  - N1(低):无-DecompressionStream 分支缺 updateStatus 可见行,与构造 catch 注释的
    "isomorphic" 字面不符;建议补一行(首选)或改注释。
- **评审 B(minimax-cn/MiniMax-M3.1-Flash):APPROVE-WITH-NITS(1 条必修)**
  - NIT-1(MEDIUM-HIGH,必修):tty_metrics_test.go 新增 `liveFrames <= 0` 断言竞态假阳性
    ——300 跑实测 24/16 次失败,HEAD 对照 300/300 绿;根因=close-policy 溢出路径
    close+drain 立刻唤醒 parked handler,0 live 帧合法;CI(ttymetrics tag)5-8% 概率红。
    方案 A(删断言)经 B 实测:300/300 绿且 mutant 仍 20/20 红。
  - NIT-2(低):write catch 的 teardown 方向无覆盖(mutant 存活)。
  - NIT-3(低,pre-existing,记录项):feed/pump 两侧 backlog 跳闸文案逐字相同,feed 侧
    无独立判别(非本次引入,不阻塞;留档为后续项)。
  - 11 项 mutant 实测表;工作树逐字还原(md5 核对)。
- **仲裁者复核**:NIT-1 独立复现(300 跑失败,签名一致 `liveFrames=0 liveBytes=0`)→
  成立,派返工;NIT-2 属工单 2.5 的 write 方向缺口 → 成立,派返工;A-N1 成立,派返工。

## Round 2(返工后确认)

- **评审 A:APPROVE** —— 三项返工逐字核验落地;新用例阴阳双 pin 推理成立;
  范围外零越界;实跑 ui/app + count=100 专项全绿。
- **评审 B:APPROVE** —— 三项返工各配 mutant 实证(删断言后 300/300 绿、mutant 双红;
  write-teardown mutant A/B 双向钉住;无-DS 可见行 mutant 转红 B1);race 干净;
  工作树逐字还原核对通过。
- **仲裁者终验**:独立 count=300 专项 0 失败;完整电池(build/vet/gofmt/go test ./.../
  -tags ttymetrics/-race)全绿后提交。

## 结论

双 APPROVE(round 2 无 nit 阻塞项)。提交两个:修复本体(12 文件)+ 过程文档
(本记录 + TASK-PR129-fixes.md)。遗留记录项:NIT-3(feed 侧 backlog 跳闸独立判别,
pre-existing)与评审 B 合并轮 N1(本地 develop 领先 origin 5 提交,后续推送 develop 后
本 PR 需再合一次 CHANGELOG)——均不阻塞,记入 PR 回复。
