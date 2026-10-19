# REVIEW-A2 闭环记录(仲裁者整理,2026-10-19/20)

## 轮次与结论

| 轮次 | 评审 A(muse-spark-1.3-contributor) | 评审 B(MiniMax-M3.1-Flash) | 仲裁处置 |
| --- | --- | --- | --- |
| R1(22:07–22:52) | APPROVE(2 NIT:门控对称性、帧率断言+1 容差) | REQUEST_CHANGES(F1/F2 HIGH、F3/F4 MED、F5 LOW、F6~F8 LOW/NIT) | 逐条实证:F1/F2/F3/F6 成立返工;F4/F8 降级 LOW(后部分被 B round2 推翻→N1/N2);F5 裁定接受;F9 任务书禁区修订 |
| R2(23:53–00:14) | APPROVE(2 NIT) | APPROVE(2 LOW=N1/N2 注释失真、3 NIT、O1 观察) | 仅剩 nit 级 → 修后独立确认提交 |

## 评审 B R1 关键发现(全部实测核验)
- F1 HIGH:W=0 在活生产者下非纯 tail(探针实测 262144B 出网)→ R1 构造性纯 tail(pureTail)。
- F2 HIGH:聚合 notice 输出停止后永久搁浅(实测 22-24 个 drop 未告知)→ R2 心跳分支冲刷。
- F3 MED:总量封顶可超 W 一个 64KB 读块(实测 40000→65536)→ R3 读长裁剪。
- F4/F8:预算测试改写/守卫顺序 → R5 注释精确化(round2 被 N1/N2 部分推翻)。
- F5:env 每连接解析 vs 任务书「启动一次」→ 仲裁接受偏离,任务书措辞正名。

## 评审 B R2 新发现(实证推翻仲裁前提)
- **N1**:仲裁 R3「满读对齐」前提错误——ReadSince 至多 maxBytes、部分读为常态
  (仓库自带 [65536,36864] pin);裁剪在预算路线可触发并收紧到精确 8MB
  (探针:8388608 vs 8424144)。注释已修正。
- **N2**:budgetExhausted=true & windowed=false & droppedFromWindow=0 可达
  (W_eff==budget+握手时 backlog<W+生产者跑赢回放;ttymetrics 构建实证)。
  windowed 语义冻结为「两窗口截断的 OR」,组合合法性写入注释+API.md。
- N3:EndOffset 失败降级注释失真(总量封顶不依赖 head 读)→ 已修正。
- N4/N5、评审 A R2 NIT-1(冲刷上界 +250ms 例外):均已修正。

## 采纳证据原则
凡评审数字/行为断言,仲裁者必须亲读代码或亲跑命令核验后方采信;
评审探针(变异体 mut1/mut2/mut3)结论经逻辑链复核后采纳。

## 门禁终态
build/vet/gofmt/app test/app -race/全仓 test 全绿(仲裁者 00:24 复跑);
1013 legacy 测试与 HEAD 逐字节一致;无 flate 夹带(5a 属 TASK-A3)。
