# Issue #116 v2.0：输入所有权驱动的 iPadOS 视口适配计划

**日期**：2026-11-04
**状态**：Phase 2 已于 2026-11-05 按 owner B 方案**收口（有显式登记缺口）**；这不等于各门禁全通过或 #116 完成。Phase 3 及以后仍须独立授权与门禁，未签署的实现任务不得提前开工。
**核心原则**：[iPadOS 输入与视口设计指导思想](../../IPADOS-INPUT-OWNERSHIP-DESIGN.md)（`47c5c3e`）。

> **终端输入归应用，Web 输入归 Safari；归属由当前焦点决定，切换时明确交接，不做全局 pan 锁死。**

## 0. 本计划如何取代旧计划

本文件取代本目录 v1.x 中关于 #116 的算法选择、实施顺序和验收口径。旧版本中的候选阈值、钉高/回顶试验及 PR1/PR2/PR3 顺序只保留在 Git 历史中，不再作为新实施依据。

- `5268869` 仍是当前产品回退基线，`2e857de` 是更早的稳定回退点。
- 本计划**允许后续原子变更修改冻结实现**，但不改变这些历史提交的可回退性，也不要求重写历史。
- 本计划本身不是产品改动，也不表示新原则已经落地。
- #100 的顶栏/动作改版与 #118 Portal 仍是独立工作，不纳入本计划，不阻塞本计划。

当前有效证据见 [VALIDATION-2026-11-04.md](evidence/VALIDATION-2026-11-04.md)。后续引用 PASS/FAIL 时必须注明具体会话、连续 seq 段和适用场景，不能用局部 PASS 覆盖整个采证过程。

## 1. 已确认的事实与待解决问题

### 1.1 已确认可工作的部分

- `scale=1`、无持续 pan 时，软键盘打开/收起/再次打开、终端局部避让和恢复符合当前冻结机制。
- A 会话连续 `seq 367–486` 中观察到 428px 软键盘避让、恢复、浅浮层不重新抬起及妙控浮层 `occlusion=0`，仅作该场景内旁证；quick-keys 从未打开，该段整体判为 **INCONCLUSIVE**，不得用于认证 quick-keys 或整个 Phase 1 契约。
- C 会话连续 `seq 88–99` 仅在 quick-keys 打开、无 pan 的连续场景内，控制列、面板高度和键盘开/关的几何与命中判为 **PASS**；该结果不替代用户截图/目测，不覆盖 C 其余帧，且 C 全文件另有 8 个序列缺口，不能作为无缺口整段验收包。
- 全仓 Go 测试、365 个 UI/Portal Node 用例及现有证据 fixture 已通过；这些自动结果不能替代 Safari 真机。

### 1.2 新原则必须解决的问题

1. **PTY 与 Safari 重复处理可见性**：C 会话有 120 个稳定 pan 帧，四次手动 Capture 均出现 `vv.offsetTop=403`、`app.top=-403`、PTY 宿主底边 `-103`。当前终端局部避让不能阻止整个终端被系统 pan 移出屏显区域。
2. **输入所有权过宽**：当前任何本页 `TEXTAREA` / `INPUT` / `contenteditable` 都可能触发终端避让，与“普通 Web 输入归 Safari”冲突。
3. **诊断工具自身不可达**：底部居位未考虑软键盘，顶部居位未考虑非零 `vv.offsetTop`，Capture 在部分键盘/角落组合下不可达；现有测试未覆盖这些组合。
4. **缩放边界**：A 会话早段 153 帧全部为 `scale=0.75`，当前实现按既有 guard 完全退出避让。普通网页与 Orca 也采用类似 zoom 豁免，但是否满足 myworktree 的终端目标需要独立产品决定。
5. **证据链不完整**：部分采集有序列缺口；实时 Chrome/CDP 检查未取得 verdict；原始、派生和最终验收证据必须分开保存。

### 1.3 控件形态台账与本机可验性（2026-11-05 下午）

- **浮动键盘（floating keyboard）**：owner 报告无论拖到屏幕何处，`rawShortfall` 恒为 0，即可视视口没有键盘侵入。按现有契约，这一形态不能凭 `raw>1` 进入活动避让会话；若此前会话仍 active，`raw≤1` 按冻结规则退出。因此不触发终端让位、`occlusion=0` 是契约解释上的正确结果，不是遮挡 FAIL；终端持焦时 HO-2 归属也不因此改变。本笔只登记本机控件形态，不新设键盘分类或补偿分支。
- **Stage Manager 无键盘缩放**：Safari 窗口拖至本机最矮时 `rawShortfall=1`，放大复原回 0，拖动中偶现 1。该 0/1 读数按 resize 间隙的噪声 / 竞态级数值登记，不构成真实遮挡事实，也不可用于制造门线样本。
- **25%–50% 判别带**：浮动键盘不产生正 raw；停靠键盘在本机随布局高同比例变化（c3ee 的 0.75 深键盘约 57.2%）；无键盘窗口缩放只有 0/1。故原 `249.25 < raw < 498.5` 操作目标在**本机当前可用形态下不可达**。r3-Z-MENU-01 改承担 settle 序列观测，25% / 50% 门线本身不变，其真机判别子项继续 INCONCLUSIVE；不得用深键盘同越两线的重入反推门线或恢复锁已获真机证明。

以上均来自 owner 文字口径，证据等级为 **INCONCLUSIVE（口头级真机观察）**，不是新行为 PASS；完整事实、设备形态限制与补证边界见 VALIDATION 同名补测节。

## 2. 产品契约

### R1：前台 PTY 终端

前台 PTY 的真实终端输入宿主获得焦点且处于软键盘会话时，由应用负责终端适配：

- 终端当前可用视口、输入区域、quick-keys 和 Transport 应位于屏显区域内；
- 不以 Safari pan 把终端主体移出屏显区域作为最终体验；
- 只调整终端相关几何，不重新钉短整个应用外壳；
- fit / resize / ack 最终一致，不恢复“刚 fit 就被旧 ack 刷回”或漏发最终尺寸的问题。

### R2：普通 Web 输入

新建/重命名 worktree 或实例、设置表单、quick-keys 粘贴 textarea 及其他本页输入，由 Safari 负责输入可见性：

- 不因这些输入触发 PTY 避让；
- 不阻止系统按实际输入位置进行最小 pan；
- 不叠加应用补偿与 Safari pan。

### R3：iframe 输入

opencode、reasonix、dsh 等 iframe 内输入由 iframe / Safari 处理。父页面不新增跨 iframe 光标协议，不要求 iframe 内部页面对其自身响应式做改动。

### R4：焦点交接

只有前台 PTY 的真实输入宿主可以取得终端输入所有权。PTY ↔ Web 输入、PTY ↔ iframe、PTY A ↔ PTY B、quick-keys 粘贴、旋转、窗口变化、缩放及前后台切换都必须重新确认归属，旧状态不得滞留。

### R5：zoom 独立决策与 scale<1 已签署兼容

双指缩放与“已处于缩放状态时打开软键盘”必须分开讨论。不得以删除全部 zoom guard、全局锁 pan 或把 `vv.offsetTop` 加入 raw 的方式顺带解决。

2026-11-04 产品 owner 的真机操作观察与明确指令已将**兼容 scale<1 的软键盘避让**签署为确定目标，采用 D2 的非对称策略：`scale>1` 继续保守豁免，`scale<1` 只在前台 PTY editable focus、真实遮挡和 settle 后稳定几何等条件同时成立时，按冻结 §5 的其余会话规则避让。该签署不删除全部 zoom guard，不把 owner 的文字口径写成测试 PASS；正式来源冲突或行为不成立时按 D2 的失败 / 回退路径处理。

### R6：可采证、可回退

诊断面板在所有要求居位和键盘状态下必须可读、可操作，或者明确标记某居位不支持；不能因工具不可达而伪造产品 PASS。每个行为 PR 必须有独立回退路径。

## 3. 明确不做

- 不全局锁死 pan；
- 不通过 `scrollTo(0,0)`、反复写根 `scrollTop` 或反向 `transform` 抢回页面；
- 不重新把整个 `#app` 钉到 `visualViewport.height`；
- 不把 `vv.offsetTop` 简单加入现有 raw，形成重复补偿；
- 不修改 vendored xterm，不改变服务端跨查看者取最小尺寸的协议；
- 不引入新的运行时依赖或跨 iframe 输入协议；
- 不把普通 Web 输入统一视为 PTY 输入；
- 不以 Headless Chrome 模拟结果替代 iPadOS 真机；
- 不顺带处理 #100、#118 或无关重构。

## 4. 实施前必须签署的开放决策

### D1：PTY 如何避免系统 pan

需要由测量证明 Safari 实际聚焦目标、其 rect 和事件时序，再选择机制。合理方向是在 Safari 产生自动 pan 之前让终端输入区域已经可见，而不是事后抢回原点。

若无法在不恢复禁止机制的前提下满足 R1，必须回到产品讨论，不能静默接受 pan 或强行试修。

### D2：缩放状态的终端行为与 scale<1 兼容

**签署状态：已签署目标（2026-11-04，产品 owner 指令，本轮仲裁签署）**。`scale<1` 场景的终端软键盘避让由候选 / 待签升格为确定目标，采用下述非对称策略。签署依据是产品 owner 的真机操作观察、文字口径和明确产品指令；该证据按 **INCONCLUSIVE** 级登记，但在 D2 产品决策域内是缩放来源判断的**直接产品证据**，不是行为验收 PASS。逐字原文与证据缺口见 VALIDATION“当前有效证据”。

**签署前冲突核查**：A 原始会话早段 153 帧全为 `scale=0.75`、`occlusion=0`，符合冻结 zoom guard 的历史事实，但没有唯一缩放来源标注，既不证明双指来源，也不与新目标冲突；C 原始会话 583 帧全为 `scale=1`，不存在 zoom 帧反例。历史 `DATA-COLLECTION.md` 的“双指/双击缩放”与“75% 与 100%”写在同一组合操作行，未建立“双指从 `scale=1` 持续缩小至 `vv.scale<1`”的唯一来源链，不能当作 owner 口径的直接反例，也不得反过来充当来源证明。基于当前有效证据未发现阻止签署的直接冲突。

**已签署的非对称行为目标**：

1. **`scale>1`：维持保守豁免**。双指放大 / 缩小过程不得被误认为软键盘，终端避让必须 opt-out。owner 实测双手释放后回到 `scale=1`；这是 Safari 来源行为及验收观察项，不授权应用写入 scale、强制回 1 或阻止系统手势。释放并 settle 后，才按当前焦点与几何重判，避免把释放瞬间误判为键盘。
2. **`scale<1`：允许符合契约的软键盘避让**。按 owner 实测口径，持续 `vv.scale<1` 只来自妙控键盘 / 外接键盘 `cmd+'-'` 或浏览器菜单缩放指令，不来自从 `scale=1` 开始的双指缩小。只有前台 PTY 真实终端输入宿主持有 editable focus、存在真实遮挡（`raw>1`）且 scale / layout / VV 已 settle 时，才允许进入软键盘避让；这三项是必要条件，具体进入 / 重入仍服从冻结 §5 的 25% / 50% 门槛及既有解锁规则，不得用 `raw>1` 绕过门线。无前台 PTY editable focus、无键盘 / 无真实遮挡或几何尚未 settle 时，不得仅因 `scale<1` 抬高终端。Phase 3 前的宿主识别过渡口径及 Web 输入帧的独立登记见下文 D-A，不得把冻结的通用 editable 谓词解释为普通 Web 输入已符合 R2。
3. **带外 scale 变档先视为新几何**。这里的“变档”限于：进入或在 `scale<1` 带外域内发生超过 settle 容差的变化，以及从带外域返回 `scale≈1`；冻结 `abs(scale-1) <= 0.01` 带内抖动仍完全归冻结状态机，`scale>1` 内部台阶由既有全帧 opt-out 覆盖，不另加 scale 几何项。变档当帧及未 settle 的过渡帧不得触发避让，不复用变档前的 layout、raw 或让位结果；恢复锁是否清除按 D-B / D-D 的窄例外与冻结退出原因分别判定，不再用“一律清锁”覆盖 `scale>1` 或同帧 `raw<=1` / 失焦。settle 后以当前几何重建判定基线，再按当前 scale、焦点和遮挡重判。`1 → 0.75 → 1` 的回程变档帧同样必须归零并重判，不得沿用 0.75 的 active 状态在新 raw 上让位一帧。该规则不新增第二套会话状态机。
4. **zoom 与键盘信号按 owner 实测认定可分，但仍须双确认**。双指 `scale>1` 豁免、释放回 1 后 settle 重判，与“前台 PTY editable focus + `raw>1`”共同防止把缩放或释放瞬间当成键盘。除本次签署的 scale 准入非对称、“带外 scale 变档 = 新几何”及下文 D-B 的恢复锁窄例外外，raw 算式、活动会话 `occlusion=raw`、恢复退出、click / focusin / 几何解锁、25% / 50% 门线及一次 settle 宽容均以冻结 §5 为唯一真源，不改写、不替换。
5. **失败与回退方向**。签署后的 MUST 在真机不成立即记该场景 FAIL，不得以 Node 结果或 `zoomExempt` 记 PASS，也不得追加阈值掩盖。若正式 Z-PINCH 证明双指能从 1 持续产生 `vv.scale<1`、释放行为与签署前提冲突，或 zoom / 键盘信号实际上不可分，则停止该行为交付、重开 D2 回产品讨论，并单独回退本 D2 实现；冻结基线 `5268869` 与历史提交不改写。

**Phase 2 实施双评审终审（对 32fab22 签署口径的可执行化；不扩大产品目标）**：

- **D-A：选择过渡方案 B，不提交 owner 追加拍板**。Phase 3 宿主分类尚未实施前，D2 只能沿用冻结 `document.activeElement` + `TEXTAREA / INPUT / contenteditable` 谓词（符号 `focusEditable`），不得新增 helper class、祖先链或显式宿主标记读取，也不得另建归属机器。由此，`scale<1` 下 quick-keys 粘贴 textarea 与普通 Web input 可能触发终端避让，登记为 **R2 已知偏离 / Phase 3 归属拆分依赖**，不是 D2 的 PTY 准入证明，也不改判 HO-3。Z-MENU / Z-MAGIC 必须分列“前台 PTY 宿主帧”（判 D2）与“Web 输入帧”（只记 R2 泄漏，不据此判 D2 PASS / FAIL）；R2 最终门禁仍在 Phase 3，不得借本过渡口径关闭。选择 B 的理由是 owner 已签署 scale<1 避让目标，而 A 会提前改动 Phase 3 的产品归属判定，C 则把确定目标再次无限期推迟；B 不改变冻结机制的所有权谓词，并保留可独立回退性。

  **Phase 3 已取代注记（`AUTH-20261105-P3-OWNERSHIP`，2026-11-06 仲裁签署）**：上述方案 B 保留为 Phase 2 / Phase 3 前的历史过渡语义；自本次 Phase 3 授权生效起，其中禁止祖先链 / 宿主归属读取的过渡红线，由 Phase 3 的 **pass 内纯函数前台 PTY 宿主判定**取代。`zoom_scale.test.mjs` D2-7b 等结构断言只准更新这一过渡部分，使其表达“单次当前焦点读取 + 前台实例宿主祖先 / 既有 `data-instance-id` 匹配；普通 Web、隐藏 / 非前台宿主与 iframe 均无 PTY 避让资格”的新不变量；D2-7 的 D-A Web 输入过渡预期应改为 HO-3 不避让，不得删例。`scale>1` opt-out、D-C settle、D-B / D-D 锁与回程、`raw>1` 及 25% / 50% 门线等 D2 行为断言必须逐条保留并通过；本注不授权修改 evidence `.mjs`，也不追溯改写历史评审。
- **D-B：批准 M8，但免置锁只覆盖“纯变档退出”**。在 `scale<1` 带外变档帧仍满足 `raw>1 && focusEditable`、即退出仅由变档引起时，先按新几何清旧锁、退出并付 `occlusion=0`，且该退出不得新置恢复锁；后续 settle 帧以未锁的 25% 进入线按当前几何重判，单步 / 多步结果必须一致。若同一变档帧另有 `raw<=1` 或失焦，冻结退出原因优先：活动会话仍按冻结规则置 / 保留恢复锁，settle 本身不清锁，未经 click / focusin / 几何解锁时按 50% 重入线判定。`scale>1`、同几何回落及其他普通退出也全部保持冻结置锁语义。源码顺序按符号核对：`geometryChanged` 清锁先于包含 `zoomOptOut` 的退出分支；`scaleStepNoLatchExit` 的 `raw>1 && focusEditable` 限定是防止路径依赖且不误免其他退出的必要组成。**冻结节目注记只写在本 PLAN 与 PHASE1-CONTRACT，不回写 `IPADOS-UI-COMPATIBILITY.md`**：“这是 D2 对冻结 §5.6 源码置锁顺序的窄例外，仅适用于上述纯 scale<1 带外变档退出；§5.6 历史基线描述不变，其余退出路径不变。”F14 回程按 D-D 分别处理 0.75→1 与 scale>1→1，不得扩张成普遍免置锁。
- **D-C：settle 用两次既有周期 tick，禁止单帧 `===` 直接准入**。`scale<1` 初次进入或变档后，以及 `zoomReturnFrame` 从带外返回 scale≈1 后，只有**连续两次现有 500ms `setInterval` tick pass**均满足下列条件才算 settled；初始 scale≈1 且 `previousTerminalScale=0` 不得伪造回程或开启候选。①除明确的回程候选外，相关 pass 均在 `scale<1` 带外域，且相邻读数及相对首个候选 tick / 当前已 settle 基线读数的 `scale` 差均 `<= 0.001`；②候选期间 `layoutHeight`、`vv.width` 与 raw 均无变化；③其间没有带外域跨越或新的超限 scale 变化。任一条件破坏即清零候选计数并保持零让位；settled 基线仅在重建时更新，逐帧小于 ε 但累计超 ε 的慢速漂移必须重新进入候选；已 settled 后累计未超 ε 的亚 ε 漂移不重建候选，超过 ε 才退出并重新计数。resize / scroll / focus 等普通 pass 可破坏候选，但不得增加 tick 计数。达到两次后计数饱和，后续同尺度 tick 不得反复退出 / 再 settle；已 settled 后单纯键盘 raw 开合不再重置 scale settle，而交由冻结 §5 当帧处理。`scale>1` 不得开启或累计候选；从 scale<1 进入 scale>1 时须清空既有 `scaleSettle*` 候选，回带内后从完整两 tick 重新开始，避免在保守 opt-out 域银行化计数。
  - 参数依据与证据限度：A 原始会话早段 153 帧的 scale 全部精确为 `0.75`、相邻差全为 0，但没有变档 / ULP 抖动样本，不能从中估计真实噪声上限。故 `N=2`、`ε=0.001` 是保守实现参数：ε 小于冻结 ±0.01 带宽容差一个数量级，又远大于 1 ULP；它们不是实测阈值，必须在 Z-MENU / Z-MAGIC 真机记录相邻 scale、raw 与实际 tick 节奏后复核，证据不足时相应 settle 项 INCONCLUSIVE，不得追加调参来制造 PASS。**IEEE-754 边界**：`SCALE_SETTLE_EPSILON` 与 `vv.scale` 均按 JavaScript binary64 Number 运算，验收以实际 `Math.abs(scale - scaleSettleBaseline)` 与 ε 的比较为准；十进制书写上“恰好 0.001”不保证计算差等于 ε，不得用十进制直觉断言边界方向。1 ULP 用例须按位构造相邻 Number，并证明其差小于 ε；ε 边界用例须同时记录实际差和预期比较结果。
  - 不新增 timer、`Date.now` / `performance.now` 或第二事件循环；只给现有 500ms tick 的 applier 调用加明确来源标记 `scaleSettleTick`。两个相邻合格 tick 的名义间隔约 500ms，受事件循环调度影响，不宣称精确墙钟时长；`initVisualViewportHandling` 的现有 tick 在 `document.hidden` 时不执行，不得累计 settle。新变档只开始一个新的候选窗口，稳定后状态幂等，因此不存在“settle 后又同值变档”的自激无限循环；真实的新超限变档则重新退出、重新计两 tick。
- **D-D：回程付 0 与 D-C 优先；冻结恢复锁按来源分别处理**。带内 `0.99–1.01` 的精确边界仍由冻结 `abs(scale-1)>0.01` 决定，普通带内抖动不为 D2 新增 geometry 项；scale>1 内部台阶也无需 scale 几何项，因为每帧本来即 opt-out。当前修订版以 `zoomReturnFrame` 识别 0.75→1 / scale>1→1：回程当帧必须退出、付 0、作废旧几何判据并开启新候选；从该回程帧到两 tick 完成前，**D-C 优先于通常的带内冻结准入**，`scaleSettlePending` 必须持续阻止进入，完成后才交还冻结 §5。普通带内 jitter 若没有回程 / 候选，不得被这两 tick 压制。
  - **回程 latch 闭环（细则 2 优先于旧细则 4 的“一律清恢复状态”措辞）**：纯 0.75→1 变档按 D-D 清旧锁并沿用 D-B 的免置新锁，settle 后按未锁 25%；scale>1 的退出不是 D-B 例外，其在先 exit 已按冻结规则置锁，故 scale>1→1 回程只保证不新置锁，**不抹掉该旧锁**。若旧锁未被独立的真实窗口几何变化 / click / focusin 按冻结规则解除，settle 后仍按 50%；D2-6 正是以同布局 / 同 VV 宽度的浅键盘证明这一保守结果，再以 click 解锁回 25%。这保留 scale>1 的冻结语义，并不允许回程当帧让位。
- **D-E：容器早退不得制造伪回程**。`applyVisualViewportOcclusion` 的 `!container` 早退也必须同步 D2 的 scale 历史并清理 `scaleSettle*` 候选，使容器消失期间发生的 scale 变化不会在恢复后的首帧被旧 `previousTerminalScale` 误判为 `zoomReturnFrame`。该清理只限 D2 scale / settle 状态，不授权改动冻结 active / recovering、raw / layout / VV-width 历史或其他生命周期语义；须补“候选存在 → 容器消失并变档 → 容器恢复”的回归用例。
- **切片主动权与 evidence 窄例外（N1）**：`applyVisualViewportOcclusion` 的既有函数内常量先例是为了命名函数切片可独立求值；但跨 pass 状态已有工厂闭包镜像约定。为消除当前九绑定缺失且最小扰动，本次选择**只同步 `verify-live-choiceA.mjs` 的工厂镜像闭包**，补 `previousTerminalScale`、`SCALE_SETTLE_TICKS`、`SCALE_SETTLE_EPSILON` 与六个 `scaleSettle*` 绑定，共九项；不迁移产品状态，不改该工具任何断言、回放步骤、几何或判定口径。此例外只证明镜像闭包与源码依赖重新对齐，**不等于该门禁 PASS**。未来新增切片外部依赖时，默认优先让被切片函数自包含；确需跨 pass 模块状态者，必须在同一变更中枚举并同步所有切片镜像（直接 Node harness 与 evidence 工具），逐一运行受影响门禁，未同步或未执行不得登记 PASS。
- **复评后的直接编码 / 测试待修项**：①执行上述 N1 九绑定镜像例外；补绑定后若旧回程断言仍按冻结 v3.2 期待即时让位，必须如实保留 FAIL 并回报，不得在本授权下改断言。②执行 D-E 早退同步与用例；为 D-B 的同帧 raw<=1 / 失焦各补恢复锁与 50% 用例，为 D-C 的 scale>1 候选禁用补完整两 tick 用例。③补 settled 后亚 ε 保持 / 累计超 ε 重建的对偶用例、候选期亚 ε 台阶后再计完整两 tick、IEEE ε 边界与 1 ULP、layoutHeight 与 vv.width 分别变化且其他候选字段不变的重建用例。④澄清 D2-2b 的 settled raw 变化不走 zoom 退出、D2-8 只有初始候选需要两 tick、D2-7 的 INPUT / quick-keys 粘贴 textarea 属 HO-3 / R2 已知偏离而非 PTY 证明，并在 D2-6 保留 scale>1 旧 latch 的理由；另补“预置旧锁、layout / VV width 不变的 0.75→1”回程清锁后按 25% 的用例，若失败只修该 D-D 路径，不得牵连 scale>1→1。⑤诊断同帧若出现 `derivedState:none / zoomed-session-exempt` 而 reported mirror `occlusion>0`，D2 验收仍以 reported `occlusion` + 宿主 / 控制列 / Transport computed bottom 为准；本项不授权修改诊断层。**2026-11-05 后续仲裁只新增 `AUTH-20261105-D2-SETTLE-READONLY` 的只读、追加式 schema 例外；它由调度者独立派单，不追溯扩大当时 D2 编码范围，也不允许改行为、阈值或 verdict。**

- **v3.2 回程旧断言处置（本轮 owner 另行授权的仲裁）**：旧即时回程断言由 `4def4d6` 写入并由冻结基线 `5268869` 原样继承，均早于 D2 签署 `32fab22` 与 D-C / D-D 仲裁 `6e8a006`；当前回程帧付 0 因此不是 D2 实现违约。仅更新 `verify-live-choiceA.mjs` 的 0.75→1 回程断言块：回程普通 pass 与第一次 `avoid("scaleSettleTick")` 均断言 hosts `bottom=0px`，第二次既有 tick 后断言 `428px`，并保留控制列 `448px` / Transport `428px` 的 settle 后断言；除该块两次既有 tick 推进外，不改工具其他内容。raw 428 同时越过 25% / 50% 两条线，本门禁只证明回程时序与最终重入，不单独证明恢复锁线选择。本仲裁实测命令与 exit 0 见 VALIDATION“D2 回程旧断言处置仲裁”；这只是离线门禁适配与 PASS，不是真机验收。

**A0 历史素材激活（签署后必测，不是追溯 PASS）**：A 会话 0.75 / 153 帧正式转为新实现的**必测场景锚点**。Z-MENU 与 Z-MAGIC 均须尝试达到精确 `scale=0.75`；达到后必须记录该 scale 下的无键盘、软键盘开 / 关、恢复及回 1。两条独立连续会话合计至少一条必须**尝试完整覆盖** 0.75 场景，并如实记录每次实际达到的 scale 值；不能达到精确 0.75 时，该精确锚点及未执行子项记 INCONCLUSIVE，但其他实际达到的 scale<1 场景仍按实测值分别判读，不得伪称 0.75 或把整条会话作废。历史 153 帧只证明旧 guard 曾退出避让，原始文件不改写，不得冒充新行为已验证。

**D2 签署后真机验收矩阵（实现后执行，不改工具）**：每行使用独立、连续、无缺口的会话，从 `scale=1`、软键盘关闭开始，并且只用该行来源改变 scale。每阶段保留人工时间线、连续 raw、手动 Capture 和同场截图；验证目标是**已实现行为是否符合签署契约**，不再是“是否解锁 / 是否签署 D2”。来源无法产生目标 scale 或软键盘无法出现时，如实记录实际范围，对应子项未覆盖 / INCONCLUSIVE，不伪造场景。

| 会话 | 单一来源与最小操作顺序 | 签署后的验收目标 |
| --- | --- | --- |
| Z-PINCH | 双指：`1 / 无键盘` 基线 → 放大至 `scale>1` 并 settle → 保持该 scale 打开 / 关闭软键盘 → 双指缩小并释放，记录是否回 `1` → settle 后在当前几何继续键盘开 / 关 → 在 `1` 继续尝试双指缩小；若实际持续产生 `vv.scale<1`，保留完整会话并补做键盘开 / 关 | `scale>1` 全程保守 opt-out；释放回 1 的变档当帧不误避让，settle 后才按前台 PTY focus + raw + 冻结 §5 重判；从 1 双指缩小若进入平铺标签页 / Tab Overview，必须明确它不是 `vv.scale<1`，不得冒充 scale<1 验收。若双指实际持续产生 `vv.scale<1`，记录为签署来源前提冲突并走失败 / 回退路径 |
| Z-MENU | 仅浏览器菜单缩放指令：`1 / 无键盘` → 缩小至 `scale<1`（必须尝试达到 0.75）并 settle、Capture → 先验证无键盘 → 保持该 scale 打开 / 关闭 / 重开软键盘 → 菜单回到 `1` 并 settle | 非前台 PTY editable focus 或无真实遮挡时仅缩放、不误让位；`scale<1 + 前台 PTY focus + raw>1 + settle` 按冻结 §5 其余规则实现避让、恢复与重入；输入行 / 最后一行、quick-keys、Transport 和 rows/cols 按现行契约验收；回 1 后无旧 layout / raw / 让位结果，恢复锁则按 D-B / D-D 的来源规则核对，不得把 scale>1 在先退出形成的锁误记为应一律清除 |
| Z-MAGIC | 仅妙控键盘 / 外接键盘 `cmd+'-'` 改变 scale：按 Z-MENU 同样的阶段顺序执行并尝试达到 0.75，不混用双指或菜单缩放；软键盘开 / 关单独标注，不能以 `magic-hint` 代替真实软键盘 | 验证命令缩小本身不误避让，以及 `scale<1 + 软键盘` 开 / 关 / 重开、恢复和回 1 的签署行为；无法打开真实软键盘时对应子项 INCONCLUSIVE，不用 Node / Headless 结果补 PASS |

**每会话最小字段**：①会话元数据：设备 / iPadOS / Safari、方向与窗口形态、被测构建、`probeVersion` / `schemaRev`、session；②人工标注：唯一 `zoomSource`、每步起止 seq、实际操作、软键盘开 / 关状态、手动 Capture 与截图对应关系；③逐帧事实：连续 `seq` / `reason` / `capturedAt`，`vv.scale/height/width/offsetTop/offsetLeft`，布局 `clientHeight/clientWidth` 与派生 raw，输入归属 / 活动宿主类别及 client rect（或不可得原因），reported `occlusion`，前台 PTY 宿主 rect 与 inline / computed bottom，输入行 / 最后一行及 `xtermRows` rect，面板 rect / corner / support；④保留所有过渡帧、zoom 帧和 settle 后回到 `scale=1` 的稳定帧，不得只筛非 zoom 或稳定帧；⑤逐帧记录本次 scale 候选计数所需的相邻 scale / 首个候选 scale / raw 与 tick 来源，Z-MENU / Z-MAGIC 另将前台 PTY 宿主帧与 Web 输入帧分列，后者只登记 D-A 的 R2 已知偏离。

**与 C-3 及现行重放器的衔接**：冻结 p9 judge 的 `zoomExempt`、探针 `derivedState=none` 或混合会话其他帧的 PASS 均不得认证 D2。对旧基线，zoom 帧只证明 scale 与退出记账；对已签署的新目标，旧 judge 在 `scale<1` 给出的 owed=0 也只描述旧基线，既不能直接判新实现 FAIL，也不能因豁免而判 PASS。签署后的验收须由经授权的真机验收判读者按 D2 的 scale 准入规则 + 冻结 §5 其余状态机，结合唯一来源人工标注、连续会话、同场截图及 settle 后稳定帧逐项 verdict；现行工具不自动产生新行为 PASS。**D2 同帧读数优先级**：若 scale<1 帧报 `derivedState:none / basis=zoomed-session-exempt` 而 mirror `occlusion>0`，以 reported `occlusion` 和宿主 / 控制列 / Transport 的 computed bottom 判定实际 D2 行为，派生标签只描述旧基线；该矛盾须随 verdict 记录。除上述 N1 对 `verify-live-choiceA.mjs` 工厂闭包九绑定的窄例外外，不得借机修改诊断资产、探针或 evidence 工具。**2026-11-05 起仅有的后续例外是下方另立清单中的只读 settle / GI-3 字段和面板 client 界修复；均须独立派单，不属于 N1 或原 D2 编码包。**

**2026-11-05 c3ee 首轮真机仲裁与 r3 路径**：`c3ee73e2653ba89dc615d4554ceb7204` 的 483 帧登记为**探索性混合会话（非验收包）**。双评审及仲裁复核支持 scale=1 深键盘三盒几何、scale=0.75 的 31 帧 PTY 让位、scale>1 的 97 帧 occ=0 及回程当帧付 0 等**子项级行为 PASS**；但整行因混合来源、无 zoomSource / 截图、缺 seq355、seq468 reload 分界、D-C 无字段、25% / 50% 不可判别、D-B 未触发以及 F18 / D1 / GI-3 缺证，维持 **INCONCLUSIVE**。owner“此次数据为准，其余信息一致”只允许把“scale<1 避让成立”登记为 D2 真机行的行为子项记录，**不上浮整行，不豁免其他形态项**。任何 99.2% 类覆盖率不得充当 D2 verdict。逐帧事实、子项表与授权清单见 VALIDATION 同名节；二轮必须执行 [DATA-COLLECTION SOP v3](DATA-COLLECTION.md#10-sop-v3命题二2026-11-05-首轮混合会话后二轮单源采证) 的 r3-Z-PINCH / r3-Z-MENU / r3-Z-MAGIC 与 r3-D1-GI3 会话。上述矩阵所写“不改工具”仍是 c3ee 首轮的边界；r3 前只能使用另立授权已完成的只读诊断增强，不能由采集者临时改探针。

**D-C 可验性后续裁决**：c3ee 的 1000ms periodic 与现有 schema 看不到两个产品 500ms tick，D-C 在本 schema 下是**结构性 INCONCLUSIVE**，不是 PASS / FAIL。选择只读 `scaleSettle*` schema 增补，而不采用 owner 手记第一 tick，也不永久取消该子项；原因是原禁令防止 D2 混入 diagnostics 试修或改判据，而只读镜像不写产品状态、不新增 timer、不改阈值 / 门线 / verdict。授权未实施前不得用稳定 31 帧反推 settle。zoom 派生标签本轮**不改**，继续以 reported 为准；面板 `seq253` 的 layout / client 错配则另批方案②：unsupported 只按实际 client 界判定并补横向 client 检查。GI-3 的 helper rect 停滞列入 r3-D1-GI3 专项，以 pty-refit-check 的 rows/cols / xtermRows / ack 读数定向复采；Headless 结果不得替代真机 GI-3。

**2026-11-05 下午补充仲裁：r3-Z-MENU-01 改目标，不改门线。** 二选一选择 `AUTH-20261105-D2-SETTLE-READONLY`，不把 25% / 50% 判别降成“Node-only 永远自洽”。本机浮动键盘 raw=0、停靠深键盘约 57.2%、无键盘窗口缩放仅 0/1，使中间判别带不可达；真机门线子项因此保留 INCONCLUSIVE，Node 只继续承担实现逻辑回归。r3-Z-MENU-01 重定义为 **settle 序列可观测会话**：scale=1 深键盘活动让位 → 仅菜单变档并尝试 0.75 → 变档帧付 0 → 两个连续产品 tick 的候选 / 重建 / settled / 重入 → 菜单回 1 的回程链。它证明 settle 时序与已观测行为，不能用深键盘同越两线的重入宣称 25% / 50% 或恢复锁选择已判别。窗口形态选定新增只读 `window.__mwTerminalSettle` 与有界 tick journal，保留数值型 `window.__mwTerminalOcclusion`；字段、隐私、测试和双评审细则见 VALIDATION 的同名授权节。

**后续编码 Sub-agent 的授权范围（仅 D2，调度者另行分派后生效）**：

- **允许**：仅修改 `internal/ui/static/index.html` 的 zoom guard 段，以及该行为的直接测试——`internal/ui/testdata/` 下相关用例；如现有 Go 门禁直接锚定该 guard，可在 `internal/ui` 内同步对应的直接门禁测试 / 断言。若需要改动其他产品逻辑、诊断资产 / 探针、服务端协议或 `internal/ui` 的非直接关联实现，即超出授权，停止并回报。evidence 工具仅有上述 N1 窄例外：可在 `docs/plans/fix-issue116/evidence/verify-live-choiceA.mjs` 的工厂镜像闭包补齐指定九绑定；不得改该文件其他语句，也不得改其他 evidence 工具。
- **禁止**：把 `vv.offsetTop` 加进 raw 算式；全局锁 pan；把 `#app` 钉高；恢复、重写或新增已经属于合同的 click / focusin / 几何解锁；改变 25% / 50% 门线；修改 trailing resize、最终尺寸去重或断开清理机制；删除全部 zoom guard；借本任务修改 §3 其他禁令或 R1–R6 其他语义。
- **状态机与提交边界**：新行为除上述已列明的 D2 窄例外外，仍以冻结 §5 为唯一会话状态机真源。产品实现与直接测试组成一个可独立解释、验证和回退的原子变更；不得混入诊断、历史证据或无关重构，必须能单独回退而不改写 `5268869`。N1 的证据镜像九绑定是按本计划单独登记的辅助门禁适配，不得夹带任何判定语义变化。
- **真机义务**：实现后必须完成 scale<1 避让、Z-MENU、Z-MAGIC 以及 zoom 状态下键盘开 / 关 / 重开的真机验证，并按矩阵补 Z-PINCH 和 A0 0.75 必测项。Node / Go / Headless 只证明其覆盖的代码路径，不得据此宣称真机 PASS；未执行或缺证据逐项记 INCONCLUSIVE（PLAN §7、PHASE1-CONTRACT F 节）。

### D3：非活动 PTY 宿主的几何策略

明确输入所有权只由前台 PTY 触发后，隐藏/非活动宿主是继续同步目标以便切换，还是延迟到激活时计算。不得让切回旧实例时恢复历史遮挡状态。

### D4：键盘期间的 PTY refit 策略

在“完整终端视口”目标下，明确哪些几何变化应立即 refit，哪些应在键盘稳定或关闭后合并。不得直接照搬 Orca 的 translateY/refit 架构。

## 5. 自上而下任务与门禁

### Phase 0：原则、证据与仓库基线

**产出**

- 核心设计原则、本计划、有效测试适配和验证工具；
- A/C 原始会话、连续派生段、p8 历史证据和验证报告；
- 清除过时 p9 实验及无测量内容的空跑结果。

**门禁**

- 工作区中的每项内容都能归入“当前基线 / 后续工具 / 历史证据 / 待处理”之一；
- 提交不夹带旧实验或产品代码；
- 证据文件不含 token、终端正文或无关敏感信息。

### Phase 1：产品状态与验收契约（仅设计）

**任务**

1. 定义逻辑输入所有权：无归属、前台 PTY、普通 Web、iframe；不要求用某个具体变量或状态机实现。
2. 写出焦点交接表：PTY ↔ 重命名、PTY ↔ quick-keys 粘贴、PTY ↔ iframe、PTY A ↔ B、失焦、隐藏实例、旋转、缩放、前后台和键盘打开时刷新。
3. 定义几何不变量：外壳、PTY 可用视口、输入区、quick-keys、Transport、pan、最终 PTY rows/cols。
4. 签署 D1–D4；不能签署的项不得进入对应实现。
5. 把 v1.x 的历史候选结论与本版目标分开，旧证据只证明其对应场景。

**门禁**：产品、终端交互和验收口径先评审，未通过不得改产品代码。

### Phase 2：测量、诊断与设计稿门禁

**阶段状态：收口（有显式登记缺口，2026-11-05，owner B）。** 本 LLM 周期不再发起真机补采；已提交的诊断、设计与判据产物按实际范围验收，未闭环项不按完成计算，统一见 §11 的逐门禁裁决与承接清单。

**任务**

1. 在不采集正文、token、worktree 名称/路径的前提下，补足：输入归属类别、活动输入宿主类型及可见 rect（或明确的不可得标记）、`vv.height/offsetTop/scale`、诊断面板自身 rect/corner。
2. 修正诊断面板底部居位和非零 `vv.offsetTop` 的可达性，并按已实现口径补面板矩阵测试：四角 `tl/tr/bl/br` 覆盖三档键盘 × 四档 pan（0、纵向 403、横向 44、横向 overscroll −120），`free` 覆盖三档键盘 × 三档拖拽 × pan 0/纵向 403，另有横向与负 overscroll 边界用例；诊断修正与产品几何修改分开提交。
3. 对比普通 input 与 xterm helper 的聚焦目标及 Safari pan 时序，决定 D1；探针 tick 顺序和同瞬间几何只是顺序证据，`vv.offsetTop` 采样变化只证明 pan 事实，不得冒充 Safari 侧归因。
4. 更新设计源，至少表现 PTY 模式、Web 输入模式、焦点交接、键盘/pan/zoom 边界和异常回退；设计稿不是实现证据。
5. 保留现有 PTY refit / sidebar 几何工具，按新契约更新其判据，旧 p9 结果只作历史对照。

**门禁**

- 诊断 Capture 在要求的真机场景中可达；
- D1 有实测依据，D2–D4 有明确结论；
- 设计源、PNG 和验收矩阵一致并签署；
- 实时 Chrome/CDP 问题单独归因，不与产品修复捆绑。

### Phase 3：输入所有权与 Web 原生交接

**任务**

1. 将“任何可编辑焦点”细分为前台 PTY 输入与普通 Web 输入；本页重命名、设置和 quick-keys 粘贴不再直接套用 PTY 避让。
2. 明确 PTY → Web、Web → PTY、PTY A → B、iframe 出入以及失焦/恢复时的状态清理。
3. 为交接补行为测试：连续事件、focusin/focusout 顺序、键盘已打开、旧焦点残留和实例切换。
4. 真机确认普通 Web 输入由 Safari 保证可见，且无应用与系统的双重补偿。

**阶段状态（2026-11-06 离线实现轮）**：任务 1–3 离线完成（提交 `0258f1e`，双评审 APPROVE）；任务 4 真机确认待安排、待 verdict。门禁：R2 / R3 / R4 的 Web 部分在真机确认未执行前不得写通过。

**阶段状态（2026-11-07 真机首轮 p3-A-WEB-01 判读后）**：**任务 4 首轮完成：F1 / F3 / F8 / F9 + F10 子项 PASS；F5 焦点常驻 FAIL 待修；R2 / R3 / R4 未整行闭环。** 会话 a22c（374 帧，seq 1–375，唯一缺口 seq169），判读见 [VALIDATION“2026-11-06 Phase 3 真机首轮 p3-A-WEB-01 判读”](evidence/VALIDATION-2026-11-04.md)。子项 PASS 不上浮整行：iframe 侧（HO-4 / GI-3 / GI-7）**INCONCLUSIVE**，seq84 单帧告警保留登记。**F5 FAIL**（quick-keys 粘贴 textarea 持焦时被程序化抢回终端）经代码身份核对归为**冻结基线 `5268869` 既有行为（分类 b）**，修复授权见下方 `AUTH-20261105-P3-FOCUS-GUARD`；**异常 B**（失焦回终端第一下不弹软键盘）按**同根假设**登记、**未归因**，并入同一复验会话，不孤立复采。

**独立提交边界**：输入分类/交接及其直接测试为一组，不携带 PTY 几何算法重写。

**门禁**：R2、R3、R4 的 Web/iframe 部分通过；PTY 几何仍保持可回退基线。

**Phase 3 编码授权：`AUTH-20261105-P3-OWNERSHIP`（2026-11-06 仲裁签署，本次授权签署轮生效；评审对比基线 = `ae9ad00`）**

- **允许修改**：`internal/ui/static/index.html` 中 `focusEditable` 谓词及其直接关联段，以最小实现把通用 editable 准入换成前台 PTY 终端输入宿主归属判定，包括 `scaleStepNoLatchExit`、既有退出与进入条件中的同一谓词；直接测试限 `internal/ui/testdata/viewport_height.test.mjs` 与 `internal/ui/testdata/zoom_scale.test.mjs`。若另有 testdata 文件直接切片引用该函数，只允许最小同步其切片夹具 / 归属断言。Go 侧只准同步直接锚定这些 JS 行为 / 源码形状的 `internal/ui/terminal_status_test.go`、`internal/ui/ui_test.go` 对应包装注释或断言，不得另改门禁语义。
- **交接语义**：在 F1–F5、F8–F10 口径下，swap 前后除归属收窄外的冻结会话语义一致。终端 → Web 聚焦按既有焦点退出条件处理，不新增“额外补偿”路径；quick-keys 粘贴 textarea、本页其他 Web 输入、隐藏 / 非前台终端宿主及 iframe 焦点均不得取得 PTY 避让资格。必须补连续事件、`focusin` / `focusout` 顺序、键盘已打开、旧焦点残留与实例切换的直接行为测试。
- **实现形态边界**：本包只放行 **pass 内纯函数判定**——从当次 `document.activeElement` 沿祖先链 / 既有宿主 `dataset`，与当前前台实例 `state.activeInst` 对应关系判定；不得新增跨 pass 归属缓存、第二状态机、事件源或显式 `data-input-host` 等 DOM 标记写入。应保持被切片函数在现有闭包依赖下可求值；不得以“纯函数”为名新增未镜像的外部 helper / 模块绑定。
- **停止并回报待再次仲裁**：纯 pass 判定无法完整满足上述契约；或实现需要跨 pass 缓存、新增宿主标记、新增切片外部依赖，或需要修改任一 evidence `.mjs`；或需要超出主授权及下方 A1 的文件 / 语句清单。除下方 A1 明确登记的夹具-only 窄例外外，任一条件成立即停止，不得自行扩大、改镜像来放行或提交半成品。
- **禁止**：改 raw 算式、把 `vv.offsetTop` 入算、改 25% / 50% 门线、click / focusin / 几何解锁、恢复锁语义、trailing resize / 最终尺寸去重 / 断开清理、D2 非对称 zoom guard 的 scale 准入 / settle / 回程语义、诊断 / 探针 / 服务端、evidence `.mjs`（仅下方 A1 的夹具-only 窄例外除外）、跨 iframe 协议或新增运行时依赖；不得携带 PTY 几何算法重写。上述事项如需变化，只能先回来再仲裁。
- **评审与验证**：以 `ae9ad00` 为 diff / 行为回归基线，输入分类 / 交接及直接测试为一个原子变更。至少分别执行 `viewport_height.test.mjs`、`zoom_scale.test.mjs` 及受影响的直接 Go 门禁；Node / Go 通过不替代 Phase 3 真机确认。授权生效不表示实现或验收已完成。

**A1 夹具同步修正案：`AUTH-20261105-P3-OWNERSHIP-A1`（2026-11-06 二轮仲裁签署，本次生效）**

- **结构性冲突确认**：`verify-live-choiceA.mjs` 是 evidence 中唯一切片 `applyVisualViewportOcclusion` 的工具；其初始及后续焦点均为无宿主祖先链的裸 stub，且两个既有宿主初始均 `display:none`。任一符合 HO-2 的前台宿主判定都会使既有基线回放与 FOCUS edge 失去准入，故必须同步夹具；代码级禁词表不含 `closest` / `parentElement` / `dataset`，不存在靠该禁词表自动兼容的第三路径。
- **只允许 fixture-only 同步**：在 `docs/plans/fix-issue116/evidence/verify-live-choiceA.mjs` 当前 `:225-236` 夹具块复用既有 `hosts[0]`，将其设为已展示前台宿主，并给默认 TEXTAREA 焦点提供指向该宿主的 `parentElement` 祖先链；在当前 `:407-428` 只给需要取得资格的 contenteditable / TEXTAREA stub 补同一祖先链，BODY 仍保持无宿主、无资格。可同步当前 `:225-236` 与 `:405-432` 的叙述注释及 assert message 文案，使其明确“前台宿主内 editable”；不得改其他语句。不得新增 `state` 或其他工厂绑定，`state.activeInst` 对应仍是语义要求；若产品必须直接读取未镜像的 `state` 或新增外部 helper，仍须停止上报。
- **逐字不变项**：`makeHost`、宿主数组、工厂闭包、geometry、帧序、`avoid` / `sessionFrame` 调用、bottom / rect / journal、断言语句的表达式与预期值均不变；只允许上述 stub 结构和 assert message 文案变化。不得新增 / 删除帧或用断言值适配失败。contenteditable-in-host 仅延续既有 `isContentEditable` 分支在“前台宿主内 editable”模型下的覆盖，不声称真实 xterm helper 是 contenteditable，也不授予宿主外 Web contenteditable 资格，无须为此另改产品语义。若实现进一步限定特定 helper 身份、排除宿主内 contenteditable 并改变该既有断言结果，则属于新的产品语义收窄，超出本授权，必须停止回报。
- **提交与验证**：A1 与主授权的产品实现、直接测试同一原子提交，以遵守 N1 的“同一变更枚举并同步所有切片镜像”，并避免产品先行提交留下必然红色的离线镜像；Node / Go 双评审必须同时覆盖产品 diff 与镜像 diff。必须从仓库根目录执行既有离线命令 `node docs/plans/fix-issue116/evidence/verify-live-choiceA.mjs --html internal/ui/static/index.html`，不新起服务；所有既有断言必须原值通过。失败即如实报告首个失败断言、actual / expected 与 exit code，保留 FAIL / INCONCLUSIVE，不得改 bottom 值、删帧、跳过工具或改用 live 服务制造 PASS。

**Phase 3 真机首轮修复授权：`AUTH-20261105-P3-FOCUS-GUARD`（2026-11-07 仲裁签署，本次生效；评审对比基线 = `e6bb0da`）**

- **授权起因（owner 当次需求驱动，非自主修复）**：owner 在 Phase 3 任务 4 真机首轮（会话 a22c）中亲自报告 **F4 焦点问题**——quick-keys 粘贴 textarea 持焦时焦点被抢回终端（VALIDATION 异常 A），以及失焦回终端第一下不弹软键盘（异常 B）。本授权只承接该 owner 需求。
- **与冻结基线的关系（必须登记明的口径）**：`focusTerminalIfPossible()` 属**冻结基线 `5268869` 机制**，且经逐字 diff 核对，`5268869`（:7842-7863）与当前 `index.html`（:8238-8253）**函数体完全相同**（含 #122 rename 守卫、末行 `session.term.focus()`），无粘贴 / 通用 editable 守卫。**本修复是有意改动冻结机制，依据仅限上方 owner 当次需求 + 本节授权，不得被后读为“AGENTS.md 冻结核对遗漏”或“自动回退 / 试修”**。改动点唯一：在该函数内新增一个提前 return 守卫；函数其余语句、九个调用点、`#122` 守卫、`session.term.focus()` 末行身份不变。按 AGENTS.md“新反馈先记录并等待指令，不自动回退或试修”，本授权即为该指令。
- **允许修改**：
  1. `internal/ui/static/index.html` 的 `focusTerminalIfPossible()` 函数体（当前 **:8238-8253**）：**仅新增一个守卫**，位置在该函数既有两个 early-return 组之后、`session.term.focus()` 之前。
  2. 新增 minimal 直接测试——对该函数做最小 stub 切片，至少覆盖两条：**(i) 粘贴 textarea 持焦时轮询不抢焦点**（焦点不动、`term.focus()` 未被调用）；**(ii) 终端宿主内 helper 持焦时照常 `focus()`**。测试文件须为**新增**独立文件或既有 Node 测试文件中的**新增**用例，不得改写既有断言。
- **裁决：采用通用守卫（优先候选），不采用 paste-mode 专用窄标记。** 形态：

  `activeElement` 为可编辑（`tagName === "TEXTAREA" || tagName === "INPUT" || isContentEditable`）**且其祖先链上不存在带真值 `data-instance-id` 的元素**时，直接 `return`（不抢焦）。

  **理由（证据锚点）**：(a) 与 `0258f1e` 已交付的产品所有权模型**判据完全同源**——`index.html:3916` 的 `focusEditableElement` 三分支与 `:3922-3934` 的 `data-instance-id` 祖先链走法，本守卫只是复用同一读法，不引入第二套输入分类，也不与 `focusEditable` 谓词本身冲突（谓词未被修改）；(b) 修复面覆盖 **HO-3 全类**（INPUT / TEXTAREA / contenteditable），不止粘贴框——raw 显示 `HO-3` 同时含 INPUT（seq 36–60 等）与 TEXTAREA，窄标记只堵粘贴框、同类抢焦仍会复现；(c) 窄候选需新增 paste-mode 状态读取（`quickKeysPasteSession` / `#quick-keys-panel.paste-mode`，`index.html:4883-4897`），把**面板 UI 状态**耦合进焦点函数，扩大跨模块耦合，与 `0258f1e` 的“pass 内纯 DOM 判定、不新增跨 pass 状态”口径相悖。
  **已指认的通用守卫风险（须在实现说明中逐条回应，不构成否决）**：(1) **循环 / 自激**：`document.activeElement` 是同步只读，守卫**不写 DOM、不派发事件、不调用 `focus()`**，不构成 `focusin` ↔ 守卫的重入回路；但**禁止**在守卫内调用 `ensureIosImeDirectInputBridge()` 或任何 `focus()`，否则会形成“守卫拒抢 → 桥接抢 → 再触发”的自激，实现须保证守卫位于这些副作用之后仍是纯读。(2) **自动后台行为**：守卫让轮询**不再**纠正“用户把焦点留在本页 Web 输入上”的漂移——这是**本授权的目标语义**（与 `0258f1e` 的 `focusEditable` 收窄一致：HO-3 不取得终端归属）；须在注释中写明“这是有意行为，不是回归”。(3) **不可编辑宿主内焦点**（如 xterm 隐藏 help textarea 之外的挂件）：本守卫**不改变**其在宿主内 / 宿主外的既有结果，只拦“可编辑且无宿主祖先”一类。
- **禁止**：改 `focusEditable` 谓词本身及其三处消费点（`:4032`、`:4072`、`:4094`）；改避让状态机 / 25% / 50% 门线 / click、focusin、几何解锁 / 恢复锁 / raw 算式 / `vv.offsetTop`；改 quick-keys 面板逻辑（`enterQuickKeysPasteFallback` 及 `paste-mode` 生命周期）；改事件监听结构或新增事件源；新增任何**跨 pass** 状态变量；新增运行时依赖；改 `internal/ui/debugassets/*`、服务端、探针 schema；改任一 `docs/plans/fix-issue116/evidence/*.mjs`；改本节与 VALIDATION 判读口径。
- **停止并回报待再次仲裁**：若最小实现需要触碰上述任何禁止项，或需要产品代码新增跨 pass 缓存 / 新 DOM 标记 / 新 helper 绑定，或四类 Node 文件 + Go 三锚点中有任一因本改动而需**修改既有断言**（而非新增），立即停止，不得自行扩大。
- **验证清单（全部必须执行并原文回报）**：
  1. `node --test internal/ui/testdata/viewport_height.test.mjs`；
  2. `node --test internal/ui/testdata/zoom_scale.test.mjs`；
  3. Go 三锚点匹配测试（沿用既有锚点，不新增门禁语义）：`go test -count=1 -run 'TestViewportHeightHandling|TestZoomScaleAvoidance|TestTerminalStatusHandling' ./internal/ui`（`internal/ui/terminal_status_test.go:21/211/244`）；
  4. `go test -tags viewportdebug -count=1 -run 'ViewportDebug' ./internal/ui`（viewportdebug ON 变体，`internal/ui/viewport_debug_on_test.go`）；
  5. `node docs/plans/fix-issue116/evidence/verify-live-choiceA.mjs --html internal/ui/static/index.html` **离线无关确认**（从仓库根目录、不新起服务、既有断言原值通过）；
  6. 新增的 `focusTerminalIfPossible` 最小 stub 用例（守卫 (i)(ii) 两条）。
  以上全绿**不替代**真机复验，也不关闭 F5 / R2 / R3 / R4。
- **复验条款（异常 B 并入，不做孤立复采）**：修复后由 owner 在**同一复验会话**内同时确认：F5 焦点常驻（粘贴 textarea 持焦不被抢）、异常 B（失焦回终端第一下即弹软键盘）、S2 焦点常驻子项。**不得为异常 B 单独发起一次采证会话**；若同一会话仍复现 B，则 B 的“同根假设”被削弱，转独立归因再议。
- **提交边界**：产品守卫 + 其直接 minimal 测试为**一个原子提交**；不携带 A/B 之外的任何改动；不与 raw 文件提交、不与其他产物混入；文档登记（本节 + VALIDATION）单独提交。

**修正案 `AUTH-20261105-P3-FOCUS-GUARD-A1`（2026-11-07 仲裁签署：编码停止回报的 fixture-only 裁决）**

- **停止回报事实（仲裁独立复核，非转述）**：守卫在 `index.html:8276` 新增 `document.activeElement` 读取。既有 testdata 文件 `internal/ui/testdata/sidebar_wt_rename.test.mjs`（`32d1766` 引入，**不在本节验证清单四类 Node 文件内**）的 `focusFactory` 切片（:205-211）以 `new Function("deps", …)` 组装、其 deps 解构（:206）**从未包含 `document`**，故运行期抛 `ReferenceError: document is not defined`（实测：切片内第 45 行、调用点 :691）。该函数原先不读 `document`，本改动使其成为新的 stub 依赖缺口。
- **裁决：签发 A1。** 理由：该失败**不属**本节停止条款所列任一情形——不需产品代码新增跨 pass 缓存 / 新 DOM 标记 / 新 helper 绑定，不需**修改既有断言**（:692、:693、:697-698、:703-704、:708-709 的断言表达式与预期值全部原样保留），也不属“自主试修”：它是守卫引入的**测试夹具依赖链**缺口，补齐属本次改动的直接对外后果。
- **A1 唯一允许改动**：`internal/ui/testdata/sidebar_wt_rename.test.mjs` 的 `focusFactory` 夹具，**恰好两处、均在不含任何断言的夹具区**：
  1. :206 的 deps 解构追加 `document` 标识符（该行本就不含断言）：由 `const { state, getActiveTerminalSession, isInstanceLiveStatus, ensureIosImeDirectInputBridge } = deps;` 改为 `…, ensureIosImeDirectInputBridge, document } = deps;`。
  2. :684-689 的 `focusFactory({ … })` 实参对象内追加一行 `document: { activeElement: null },`。
  可选：同步 :198-204 的夹具叙述注释，使“#116 FOCUS-GUARD 使 `document.activeElement` 成为新的 stub 依赖”有据可读。
- **对编码建议的一处订正（必读）**：编码建议“只在 deps 处加一行 `document: { activeElement: null }`”**不充分**。`new Function("deps", …)` 不会把 deps 对象的键变成词法变量，只加键不加解构仍抛同一 `ReferenceError`（仲裁已用临时探针实测：单点补链后仍为 18 项中 1 项失败）；**必须按上述两处一起补**，探针实测两处齐补后 18/18 通过。
- **语义不变证明**：`activeElement = null` 时守卫首语句 `if (active && …)` 短路、直接落到既有 `session.term.focus()`，与守卫前行为等价；该用例的焦点 / 桥接调用序列（1→1→1→2 与 1→2→3→4）全部保持原预期值。
- **禁止（A1 不扩大既有边界）**：不得触碰或改写任何既有断言表达式、预期值、`test()` 标题与用例结构；不得改动 :150 / :171 切片及其 :393 / :471 的 `document` 注入；不得改 `index.html` 守卫形态（A1 为夹具补链，不是产品改动）；不得顺带修该文件其余任何内容；不得新增测试用例（新增用例已由 `focus_terminal_guard.test.mjs` 承担）。
- **A1 验证清单**：
  1. `node --test internal/ui/testdata/sidebar_wt_rename.test.mjs` → 回绿 **18/18**；
  2. `node --test internal/ui/testdata/focus_terminal_guard.test.mjs` → 全绿（**当前实测为 5 个用例，非 6**：该文件 :141 / :155 / :169 / :182 / :196 共五处 `test()`，其中 :155 为“spread”变体、内部含 INPUT 与 contenteditable 两次断言；以该文件实际 `ℹ tests` 计数为准回报）；
  3. **不因 A1 复跑** `viewport_height` / `zoom_scale` / Go 三锚点 / viewportdebug / `verify-live-choiceA.mjs`：A1 只增 `docs` 之外的**测试夹具对象字面量**，不触碰 `index.html`、`framework.js` 或任何证据脚本，上述五项的输入字节不变，复跑不产生新信息；其已于 `d1ccbb6` + 本轮产品改动下由编码自跑全绿，该结果对这些项继续有效。
- **A1 与提交边界**：夹具补链的这 3–4 行**并入同一个原子提交**（产品守卫 + 其直接 minimal 测试 + 守卫引入的既有夹具补链），不单独成为一个提交，也不与 raw 文件或文档登记混入。

**评审处置与遗留（`AUTH-20261105-P3-FOCUS-GUARD` / `-A1` 编码双评审后登记，2026-11-07；本节仅登记，不改动上方授权语义）**

- **双评审结论**：编码产出经两名评审独立 APPROVE——评审 1 号（`opencode-go/muse-spark-1.3-contributor`）：0 BLOCKER / 0 MAJOR / CHALLENGE×3；评审 2 号（`minimax-cn/MiniMax-M3.1-Flash`）：0 BLOCKER / 0 MAJOR / MINOR×6 / CHALLENGE×6。无停线项，下方逐条给出处置或留待归属。**本节登记不重新打开、不修改、不放宽**上方 `AUTH-20261105-P3-FOCUS-GUARD`、`-A1` 及下方 raw 处置裁决的任何授权语义、文件清单或禁止项。
- **【登记项 1 · 两评审共同点】守卫的宿主测试是「宽松版」，不得后读为与所有权谓词等价。** 守卫（`internal/ui/static/index.html:8284-8294`，判定读 `:8284-8285`、宿主祖先遍历 `:8286-8292`、拒绝 `:8293`）的宿主判定 = **祖先链上任一真值 `data-instance-id` 祖先**即视为“终端内”，命中即放行 `session.term.focus()`；所有权谓词（同文件 `:3915-3936`，`focusEditableElement` 于 `:3915-3916`、`focusEditable` 赋值分支 `:3917-3936`）额外要求首个 stamp 祖先必须是 **`#terminal-container` 的直接子元素且非 `display:none`**。差异方向是**保守的**：本守卫把“stamp 祖先不合那两项额外条件”的可编辑焦点仍归入终端内，因此**宁可多抢**，其 refusal 面（拒绝抢焦的集合）比所有权谓词**更窄**；但反过来说，宿主外、仅挂在模板 stamp 下的可编辑元素在本守卫口径下会被当作终端内输入。**这是刻意的口径差异**，代码注释已如实改写为 “stamped ancestor” 口径并显式标注 `DELIBERATELY LOOSE`，不得被后读当作“守卫与谓词同一判据”或“两处可互推等价”。
- **【登记项 2 · MINOR-6】“九个调用点”的计数更正（追加注记，不改写原句）。** 上方 `AUTH-20261105-P3-FOCUS-GUARD` 的正文与 `-A1` 的引用句中使用“九个调用点”，该计数**先于本 diff 存在**于已提交的授权文本中（`65ded23`），不是本轮编码产出引入的；本轮实测：`focusTerminalIfPossible` 的**调用点 7 处**——`:5497`、`:6432`、`:6713`、`:6766`、`:8322`、`:8365`、`:9223`；另有 **1 处 `window` 导出**（`:10363` `window.focusTerminalIfPossible = …`）与 **1 处函数声明**（`:8238`）。**实质主张不受影响**：调用点身份（位置与形态）在本 diff 前后不变，本守卫不改写、不新增、不删除任何调用点，故“九”属计数笔误而非语义错误。**本条以追加更正注记的形式登记，不改写原句**，以免破坏已签署授权的可追溯性。
- **【登记项 3 · MINOR-5】下方 `-A1` 节内行号为补丁前偏移。** `-A1` 节（下方 :276-289 一带）引用的 `sidebar_wt_rename.test.mjs` 行号 `:206` / `:684-689` / `:198-204` 是**本轮补丁前**的偏移。补丁在 :203 后新增了 3 行 A1 注释，故补丁后实际为：deps 解构行 **:209**、`focusFactory({ … })` 实参对象 **:688-693**（新增的 `document: { activeElement: null },` 位于 **:692**）、夹具叙述注释块 **:198-207**（新增的三行 A1 注释在 **:204-206**，`:209` 为 deps 解构行）。同节内 `:692` / `:693` / `:697-698` / `:703-704` / `:708-709` 为 `-A1` 节所断言的**断言区 / 调用区**行号（如 :697-698 的两条 `assert`），均位于本轮新增行之前，故补丁前后一致、不需换算；`:150` / `:171` 切片及 `:393` / `:471` 的 `document` 注入位于新增行之前，同样不需换算。**作为补丁前指令自洽**：该节是“补丁前写的指令”，按补丁前偏移阅读即为其本意，本条只作补丁后偏移的对照注记，不改写原行号。
- **【已处置 · 注释级】本轮编码已收敛、落码待提交（不计入遗留）。** MINOR-1：`index.html` 守卫注释中“SAME read-only judgment”的表述收窄为 **“SAME two reads”**（只声称复用同样的两处读取，不声称同一判定）；MINOR-2：注释与口径统一为 **host ancestor → stamped ancestor**；评审 1 的 CHALLENGE-1 即上述【登记项 1】，其要求已在注释中以 `DELIBERATELY LOOSE` + 差异方向说明落码；`focus_terminal_guard.test.mjs:89-96` 的自述修正（MINOR-3，把“activeElement 身份断言”明确定性为**辅助观测**、主证据是 `term.focusCount`）——以上**均已落码**，但**尚未提交**，随产品守卫同一原子提交入库。
- **【保留未动】明确不改的两项。** (a) `focus_terminal_guard.test.mjs:194` 的 assert message 仍含旧措辞 “editable without a host ancestor”（严格说应为 “stamped ancestor”）：它属**断言实参**，而 A1 与授权“禁止改断言”条款覆盖的正是这一层，**禁止改断言，保留原样**；措辞不准不影响断言表达式与预期值。(b) MINOR-4（新测试的正向形状断言略收紧、可讨论）：**保留现状，不扩大**，不在本包内追加收敛。
- **【设计级遗留 · 不阻塞本包】转复验清单 / 后续独立任务。** (a) **INPUT 一刀切**：守卫把**所有** `tagName === "INPUT"` 视为可编辑，不区分输入类型；若未来出现“点了宿主外按钮后终端不回焦”的反馈，**先回看本守卫**再归因其他路径。(b) **iframe（HO-4）**：iframe 持焦不在本守卫覆盖面内（授权已显式锁定本包不动 iframe 语义，**保持不变**），列入 owner 复验清单。(c) **shadow-DOM / slottable**：`isContentEditable` + `parentElement` 链在 shadow root / slot 边界处会截断，属**已知未覆盖面**，复验会话宜含一个 contenteditable 探针。(d) **新测试 DOM stub 的建模边界**：`focus_terminal_guard.test.mjs` 的 stub 未建模 body 以外元素（`document` 元素、shadow-root host），故对 (b)(c) 不构成证据。以上四项均为设计级、**不阻塞本包提交**，转复验与后续独立任务处理。
- **【门禁与状态】** 双评审均独立复跑并通过 **8/8** 全部检查项：`viewport_height` 40/0、`zoom_scale` 37/0、`sidebar_wt_rename` 18/0、`focus_terminal_guard` 5/0、`terminal_status` 68/0、Go 三锚点 `ok`、viewportdebug `ok`、`verify-live-choiceA.mjs` 离线原值 PASS。**提交边界不变**：产品守卫 + 新测试 + A1 夹具将并为一个原子提交，本登记为 docs 单独提交（与上方 `-A1` 提交边界及 raw 处置裁决一致）。**真机复验未执行**：F5 / 异常 B 同根假设 / S2 焦点常驻子项须由 owner 在**同一复验会话**内完成，因此 **F5 / R2 / R3 / R4 不整行关闭**；离线 8/8 全绿不替代真机证据。

**raw 文件处置裁决（a22c，Q4：`AUTH-20261105-P3-FOCUS-GUARD` 同轮仲裁，2026-11-07）**

- **判读先行、raw 后置**：本轮先提交**判读与派生文档**（本节 + VALIDATION“2026-11-06 Phase 3 真机首轮 p3-A-WEB-01 判读”，含全部计数、seq 锚点与复核订正），**再**以**独立提交**归档 raw 本体 `docs/plans/fix-issue116/evidence/raw/a22c660f67a1347b1f4e38ca0126a7d0.jsonl`（3 559 089 字节，374 帧）。两者**不得同提交**，以维持“原始不改写、判读与派生分开标识”（§8、§9）。
- **raw 本体逐字节不改写**：不得裁剪、重排、补 seq169、重采样或格式化该文件；缺口 seq169 作为原始事实保留（已在 VALIDATION 登记为 61ms 突发、无 verdict 跨越）。
- **不需要 `.gitignore` 例外、也不需要特殊归属**：`evidence/raw/` 下既有 raw 已按同一路径入库（如 `42cd679e…jsonl`、`b7460ddb…jsonl`），`git status` 显示该文件为**普通 untracked**（非 ignored）；本裁决沿用既有证据归档惯例，**不新增 ignore 规则、不改 `.gitattributes`**。仓库无 LFS 使用先例，约 3.4MB 直入 Git 可接受。
- **提交信息与边界**：raw 提交只含该一个文件，提交信息须标明“原始设备会话、未改写、缺口 seq169、判读见 VALIDATION 对应节”；不得夹带 PLAN / VALIDATION 或其他任何改动。
- **本仲裁轮不执行任何 `git add` / `git commit`**：上述两次提交由调度者按本裁决派单执行。

**Phase 3 授权包层级声明（`P3-OWNERSHIP` / `-A1` ↔ `P3-FOCUS-GUARD`）**：`AUTH-20261105-P3-FOCUS-GUARD` 是**追加的窄修复包**，**不改变 P3 主包边界**——不重新打开 `AUTH-20261105-P3-OWNERSHIP` / `-A1` 的文件清单、谓词形态与已关闭的评审结论，不改 `G10` 主口径，不改 `focusEditable` 谓词；两包在 `index.html` 内**不相交**（主包 = `applyVisualViewportOcclusion` 内谓词段；本包 = `focusTerminalIfPossible()` 函数体）。`A1` 的 fixture-only 例外**不适用于**本包（本包不触碰 `verify-live-choiceA.mjs`，只作离线无关确认）。

**阶段状态（2026-11-07 真机复验 p3-A-WEB-02 判读后）**：**任务 4 复验完成下列子项：F5 焦点常驻 PASS、S2 焦点常驻 PASS（均为帧级一致）；iframe 侧观察级 PASS；回归-重命名 INCONCLUSIVE；异常 B 未归因、维持登记。R2 / R3 / R4 未整行闭环。** 会话 `2a33f246…`（534 帧，seq 1–534 无缺口，`scale` 534/534 ≡ 1），逐子项 verdict、逐击分析与复核订正见 [VALIDATION“2026-11-07 Phase 3 真机复验 p3-A-WEB-02 判读”](evidence/VALIDATION-2026-11-04.md)。**R2 / R3 / R4 不整行关闭的理由**：R2 属本轮 B 缺口所在门禁——B 的两个子问题（“helper 持焦下键盘自行收起”32 次转变、同域内 click 派发随机）**均缺真值字段**，R2 的“普通 Web 输入自身由 Safari 保证可见”完整链路也未覆盖，故 R2 **受缺口影响、不整行关闭**；R3 的 iframe 内部焦点不可检视（12 帧 `occ` 0 仅父页数值面无干预）；R4 的实例切换 / PTY A→B 段本轮未覆盖。**子项 PASS 不上浮整行。**

**阶段状态补充（2026-11-07 Phase 3 真机 p3-B-EVID-01 B 归因真值轮判读后）**：**B 处置 = 候选排序数值化「A12 ＞ A2（局部 1/3 失败击）＞ A1（局部 0/3 命中、不可排除）＞ A13 ＞ A11 ＞ A8 ＞ A7（判死）」，判别式 =「本击是否 native `mousedown` + `click`」⇔「键盘是否顶起（8/8 无反例）」；已闭环的只是「页面侧可归因面」（3 次失败击中 2 次与页面无关、1 次与 document 锁相关），剩余根因在 iOS 对「已聚焦 + 键盘自收」之后是否重合成鼠标事件（平台域 / Phase 4 D1 域）。B 证据等级维持 INCONCLUSIVE，不作「已归因」声明；F5 / S2 的 PASS 登记不变，R2 / R3 / R4 维持不整行关闭。** 判读细节见 [VALIDATION「2026-11-07 Phase 3 真机 p3-B-EVID-01 B 归因真值轮」](evidence/VALIDATION-2026-11-04.md)。

**阶段状态补充（2026-11-07 owner B 最终决策登记后）**：**B 处置 = 维持 `2b64e7e` + 候选矩阵 v2 权重排序收口，归因闭环归 Phase 4；帧真值归因走探针 additive 增补路线，授权见下方 `AUTH-20261107-P3-PROBE-TOUCHV2`。** 本次"收口"收的是**处置决定**，**不是** B 的证据结论——**B 的归因状态维持 INCONCLUSIVE**。上述 F5 / S2 的 PASS 登记与 R2 / R3 / R4 的"不整行关闭"结论**均不变**。

**异常 B 与 owner 回退建议的处置登记（2026-11-07，p3-A-WEB-02 同轮仲裁）**

- **登记口径（重要）**：本段只登记**证据与两选项的代价对照**，**不代表仲裁替 owner 作出“维持”或“回退”的产品决定**。产品方向属 **owner 决定权**。
- **owner 目测（本轮 `2b64e7e` 构建）**：F5 粘贴框焦点不被抢 **PASS**；iframe 不被抢 **PASS**；**异常 B 退化为“任意空白非输入区 → 终端输入区，随机 0–2 次不弹软键盘、无复现规律”**，与守卫前“staged/unstaged 标题 → 终端首击不弹”的固定印象不同。owner 建议：**如归因不出原因 → 回退到守卫前介绍（`0258f1e` 一带状态）**。
- **owner 条件式指令的登记状态**：上述“如果找不出原因建议回退”按 **owner 表达的倾向** 原样登记于此，**不写为已生效决定**；其生效前提（“归因不出原因”）是否成立，由下条证据判断，最终由 owner 定夺。
- **回退的事实前提不被帧支持（证据裁定，非产品裁定）**：owner 回退建议的隐含前提是“**守卫导致 B 退化**”。本轮与 a22c（守卫前）对照**不支持**该前提：
  - 两轮**同形**：以统一 helper 筛选口径，本轮 69 次 helper tap 中 44 派发 / 25 不派发，a22c 40 次中 28 派发 / 12 不派发；**按点击前一帧域** `preVvH=748`，本轮 38 派发 / 23 不派发，a22c **14 派发 / 11 不派发**——**守卫前同样存在大量 no-click 样本，且大量首击即派发样本推翻“a22c 第一下必不弹”**。
  - **无守卫直接冲突帧**：本类 tap 点击前 `activeElement` 恒为宿主内 helper，必命中守卫 `internal/ui/static/index.html:8296` 放行路径；守卫为纯读（`:8284-8294`），不调 `focus()`、不写 DOM、不派发事件。
  - **诚实边界**：全轮**无拒绝计数器**（探针 schema 无此字段），故只能登记“**帧内无守卫参与痕迹**”，**不能登记“已证守卫无关”**。
  - **裁定的准确表述**：回退**在现有帧上缺乏依据**，且**会一并带回守卫前的 F5 粘贴框抢焦（原始缺陷 A）**；但在无拒绝计数器的前提下，**“守卫间接改变了暴露窗口”这一可能无法被帧排除**。故本段**不发布“必须维持”或“应当回退”的结论**。
- **两选项并列（供 owner 决策，代价对照）**：

| 选项 | 依据 | 收益 | 代价 / 风险 |
| --- | --- | --- | --- |
| **A. 维持 `2b64e7e`** | F5 / S2 帧级 PASS（64 帧 `occ` 0、27 帧 `occ` 0）；B 两轮同形、无守卫冲突帧 | 保住 F5 原始缺陷修复与 S2 焦点常驻；不重开已签署授权；符合“先限定范围、不自动回退” | B 未归因、形态可能仍随机；需新一轮探针授权才能闭环 |
| **B. 回退至 `0258f1e` 一带（守卫前）** | owner 目测倾向（条件式） | 消除“守卫是否间接相关”的不确定性 | **F5 粘贴框抢焦原始缺陷 A 一并回归**（a22c 4 次实证）；S2 焦点常驻子项一并回退；需**撤销** `AUTH-20261105-P3-FOCUS-GUARD` / `-A1` 的相应登记与本节 verdict；**B 本身仍无证据表明会改善**（a22c 同样有 no-click 样本） |

- **仲裁的建议倾向（不等于决定）**：基于“回退未获帧级依据、且回退会确定性地带回 F5 原始缺陷”，**建议维持 `2b64e7e`**；该建议**不阻却** owner 选择回退，若 owner 选择回退，则按上表“代价”栏同步撤销相应登记并重开 F5。

**二选一处追加注记（2026-11-07，代码级候选补登轮；不改变上表并列结构，不作产品决定）**

- **事实更新（新证据类型：代码级，非帧级）**：本轮补做**只读代码级审计**并登记**唯一残余候选 A1**——`internal/ui/static/index.html:4499`，`#terminal-container` 的 `touchmove` 首句 `e.preventDefault(); // hard page lock — all iOS, session or not`（`{ passive: false }` 见 `:4523`），位于多指中止／无会话／`dy`=0 等所有早退**之前**（`:4500`/`:4508`/`:4509`），故 tap 序列中**任何** touchmove 都被吞。按 WebKit 合成语义，这会使 `touchend` 后的 tap `click` 被抑制 → 键盘不弹；随机性 = 指尖是否产生 touchmove。行号、语义词、测试背书（`internal/ui/testdata/terminal_touch_scroll.test.mjs:143/151/190/199`、`internal/ui/ui_test.go:2119-2142`）与排除面均由仲裁**独立复核**，详见 [VALIDATION 代码级候选补登节](evidence/VALIDATION-2026-11-04.md)。
- **(a) A1 天然归口 Phase 4，不归口本轮、不归口守卫**：A1 属**冻结基线 `5268869` 的 pan 处理域**（R17 无线触摸滚动映射 + 页面硬锁），正是本计划 **Phase 4“PTY 终端自有视口与 pan 处理”任务 1** 的既有范围；已签署的部分 D1 授权含 pan 相关条款，但 D1 仍为“**仅禁止手段部分签署**”（§7 ②），**正向机制未选**。**B 的根因闭环路径 = Phase 4 的设计／实现**：届时若要收敛 A1（例如改为“越过滚动阈值才 `preventDefault`”），将**改动已被测试签名的契约**（上述两处测试均断言“无条件锁”）且触及冻结机制，按 AGENTS.md 必须由 **owner 新指令 + 仲裁授权**另行签发。**本轮不签发、不改任何产品代码。**
- **(b) “回退”选项的新事实（事实权重进一步下降）**：A1 在**守卫前构建**中同样存在——a22c 归档 `d1ccbb6` 的 `index.html:4499` 为**逐字相同**行（`:4485` 同为 `initTerminalTouchScroll`）。故**回退不改变 B**（该候选机制在守卫前后同时存在），**且会确定性带回 F5 粘贴框抢焦（原始缺陷 A）与 S2 焦点常驻回退**。诚实边界：raw 帧无 git 构建身份字段，此处依据是归档源码 + 该会话登记时的构建口径，**不是探针直证**。
- **(c) A1 未达“已归因”**：缺少 `touchmove` **计数／`dy` 序列**、`clickSuppressed`、`touchcancel` 计数等真值字段，无法**逐击**把 no-click tap 对上 A1。上述探针真值字段**仍列为可选强化授权**（与下方“后续归因任务建议”①–④同级），**本周不签发**；零改码替代是 owner 同场目测（首击不弹是否与“指尖轻微滑动”强相关）。
- **结论口径（不变）**：本节仍**不替 owner 作决定**，上表两选项并列维持，“建议维持 `2b64e7e`”的建议倾向维持；上条“建议”的证据权重因 (b) 增强，但**主因是 A1 归口 Phase 4 而非“必须维持”**——即便 owner 选择回退，B 也不会消失。

**二选一处追加注记 v2（2026-11-07，owner 补充实验 + 候选矩阵 v2 裁决轮；不改写上方任一行、不作产品决定）**

- **登记性质**：本节为对上方两段（原二选一表、代码级候选补登追加注记）的**追加注记**，不改写其上任何已签署文本；证据细节见 [VALIDATION 候选矩阵 v2 节](evidence/VALIDATION-2026-11-04.md)。**证据口径**：owner 三条为**目测级**（debug panel 关闭 ⇒ 本轮 **0 帧**）；候选矩阵为只读源码审计 + 仲裁独立抽验。**权重是排序，不是结论。**

- **(a) owner 实验③ 的"仅本页面"结论与候选矩阵 v2 一并归入 B 处置**：
  - owner 观察（目测）：**其他网站、本项目 iframe 内 dsh web 页面均无此问题**；本页面其他原生 web 输入框（`#wtTask`）同样随机不弹，形态为"**`:focus` 高亮外圈已发生、光标未进、键盘不弹**"。
  - 归入 B 处置的含义：B 的嫌疑面**收窄到本 `index.html` 的页面级 touch／focus 链**，不再视作整机 iPadOS 状态问题。**该收窄本身不构成归因**，且因 iframe 内同样是 vendored xterm 且工作正常，**xterm 自身的合成 mousedown／focus 链不能被单独当作充分条件**。
  - 候选矩阵 v2（排序，**非结论**）：**候选 A13（非手势态 `focusTerminalIfPossible()` 抢焦）＞ 候选 A11（helper textarea 离屏 ⇒ 终端无原生可聚焦目标）＞ 候选 A2（document 级条件性 `touchmove` 锁）＞ 候选 A8（重命名框 rAF 延时 focus）＞ 候选 A12（`showModal()` UA 自动聚焦、`#wtTask` 无兜底 `focus()`）＞ 候选 A1 ＞ A7（判死）**。**仅 A13 能同时覆盖 owner ①②③**；`#wtTask` 一侧最贴合 A12 + A13。**候选 A7 判死**：vendored xterm 的 document 级 `touchend` `preventDefault` **代码存在、通路不可达**（`Gesture.INSTANCE` 仅由 `addTarget`／`ignoreTarget` 构造，二者调用点 0，故构造函数内的 document 监听器**从未安装**）。**候选 A1 权重下调但未判死**：owner ①（谨慎空击仍复现）削弱其"有／无 touchmove 决定开关"的强预测。

- **(b) "回退"选项：结论不变，但改善期望按 v2 收紧为 0**：
  - **结论不变的部分**：候选 A1 在守卫前构建中**逐字存在**（`a22c` 归档 `d1ccbb6` 的 `index.html:4499` 与 `:4485` 同形），故**回退不改变 A1**；回退仍会确定性带回 **F5 粘贴框抢焦（原始缺陷 A）与 S2 焦点常驻回退**。上表两选项**仍并列**，"建议维持 `2b64e7e`"的建议倾向**维持**。
  - **本轮新增的核验结论（逐一构建归属 grep 佐证）**：本仲裁**已逐项核验**，全部候选（A2／A8／A11／A12／A13／A1）**均存在于守卫前构建**——证据：A2 基线 `5268869:4060`(`document` `touchmove`)、`:4046`(`ownsTouchGesture`)；A8 基线 `:5560-5563`／`:5658-5661`（rAF `focus()+select()`）；A12 基线 `:2554`（`#wtTask`，产品侧 0 处 `focus()/select()`）；A13 基线 `:7842`(函数)、`:7856`(`session.term.focus()`)、`:5071`(`pollTimer` 2000ms)；A1 基线 `:4103`（hard page lock，`touchstart` passive 同形 `:4092-4101`）；A11 依赖的 vendored xterm 的 `xterm.js`／`xterm.css` **自 `5268869` 起 blob id 未变**（`e47e2dd…`／`819654e…`，`git diff --stat 5268869 HEAD -- internal/ui/static/vendor/xterm/` 为空）。
  - **因此**：既然**全部核心候选都存在于守卫前构建**，回退对任一候选的**改善期望近期为 0**（回退不消除任何候选），同时确定性地带回 F5／S2 回退。**该表述已经独立核验成立**，故在此按结论写入，而非"未逐一核验"。
  - **诚实边界（不变）**：raw 帧不含 git 构建身份字段，"a22c 运行的就是该源码"仍是**已知前提上的推断**；本核验是**源码归属**证据，不是探针直证。

- **(c) B 闭环路线（两条，仅登记、不签发）**：
  - **(i) Phase 4 pan／own-viewport 域（已有归口）**：候选 A1／A2 均属**冻结基线 `5268869` 的 pan 处理域**，归口 **Phase 4"PTY 终端自有视口与 pan 处理"**。收敛它们会**改动已被测试签名的"无条件锁"契约**（`terminal_touch_scroll.test.mjs:143/151/190/199`、`ui_test.go:2119-2142`），须 **owner 新指令 + 仲裁授权**另行签发。**本轮不签发。**
  - **(ii) 若 owner 需要在 Phase 4 前就 B 给出更强归因，唯一途径是探针 additive schema 增补授权**：**本轮不签发**，留待 **owner 用户同意后另行派单**。所需字段（additive、只读）见 [VALIDATION 真值字段清单](evidence/VALIDATION-2026-11-04.md)：`touchmove` 次数／`dy` 序列、`touchend` 时 `preventDefault` 真值、**`navigator.userActivation.isActive` 于 `focus()` 调用时刻**、`mousedown` 合成／派发真值、`showModal()` 后 `activeElement` 序列。**在这些字段落地前，矩阵 v2 只能是排序；任何人不得据矩阵把 B 写成"已归因"。**

- **不改项**：本节不改变 F5 / S2 的 PASS 登记、R2 / R3 / R4 禁令与状态行、raw 文件处置裁决，以及既有探针字段排布。

**后续归因任务建议（Q3：本轮只登记，不签发授权）**

- **需一次新授权**：B 的 cause 段归因依赖探针真值字段，而 `internal/ui/debugassets/*` 与**探针 schema 属 `AUTH-20261105-P3-FOCUS-GUARD` 明列禁改面**；故须由 owner 另发**独立授权**，**本轮不签发**。
- **建议所需授权范围（最小集）**：① `unlockTrace.entries` 或新字段记录元素标识（`id` / 稳定路径摘要），使逐击落点归属可判（补对照 B / 步骤 5 缺口）；② 事件级 `event.timeStamp`（`touchstart` / `touchend` / `click`）与 `preventDefault` 调用计数（补 cause 段诱因缺口）；③ `focusTerminalIfPossible()` **拒绝路径只计数、不改行为**，使“守卫参与与否”由不可证伪转为可测；④ 键盘真值旁证字段或 owner 同场目测记录表。
- **无需改探针的低成本替代（建议优先）**：在**守卫前 `0258f1e` 构建**上重采一段**同时包含 F5 与 B** 的会话——若同样出现“helper 持焦 + 键盘收起”转变帧，则“B 与守卫无关”由**对照实证**取代当前的“帧内无痕迹”推断。此路线只需 owner 一次真机操作。

**raw 文件处置裁决（2a33，p3-A-WEB-02 同轮，2026-11-07）**

- **沿用 a22c 先例**：先提交**判读与派生文档**（本段 + VALIDATION“2026-11-07 Phase 3 真机复验 p3-A-WEB-02 判读”），**再以独立提交**归档 raw 本体 `docs/plans/fix-issue116/evidence/raw/2a33f24644d4ffa0fa060bf891911180.jsonl`（**5 081 383 字节 / 534 帧 / md5 `75e32f913c238d053bdf832f4a33d3b9`**）。两者**不得同提交**。
- **逐字节不改写**：不得裁剪、重排、重采样或格式化；**本会话无缺口**（seq 1–534 连续），无须补帧记录。
- **归属执行**：raw 提交只含该一个文件，由**调度者按本裁决派单执行**；**本仲裁轮不执行任何 `git add` / `git commit`**。

### Phase 4：PTY 终端自有视口与 pan 处理

**任务**

1. 按已签署的 D1 实现前台 PTY 的完整视口和输入可见性，不以系统 pan 作为最终体验。
2. 按 D3/D4 处理宿主同步、fit/resize/ack 和键盘期间 refit 时序。
3. 保留现有 trailing resize、最终尺寸去重、断开清理和旧 ack 不回刷机制，或提供等价回归证明。
4. 覆盖光标/内容位于上方、中部和底部，quick-keys 打开、Transport、妙控浮层、连续键盘开/关/重开。

**独立提交边界**：每个可独立解释和回退的 PTY 行为单独提交；不与诊断面板、普通 Web 输入或无关样式混合。

**门禁**：同一 iPad 上证明 R1；若仍出现持续非预期 pan 或终端出屏，停止并回到 D1，不通过追加阈值掩盖。

### Phase 5：生命周期与全表面回归

**任务**

- 横竖屏、Split View、Stage Manager、窗口尺寸变化；
- 按已签署的 D2 实现非对称 zoom guard，并执行 Z-PINCH / Z-MENU / Z-MAGIC 与 A0 0.75 必测场景；若正式证据冲突，按 D2 重开决策并单独回退，不静默保留全量豁免；
- 前后台恢复、键盘打开时刷新、实例切换、隐藏宿主；
- quick-keys 粘贴、IME、触摸滚动、内部滚动器、safe-area、暗色/浅色；
- opencode、reasonix、dsh iframe 的父页面无干预验证。

**门禁**：每个独立场景均有明确 PASS / FAIL / INCONCLUSIVE；缺设备、缺字段或工具未完成不得写成 PASS。

### Phase 6：最终验收、文档与交付

**任务**

1. 在最终提交上执行针对性 Node/Go、带 `viewportdebug` 的包测试、全仓 Go 和直接 Node 门禁。诊断专项至少执行 `go test -tags viewportdebug -run ViewportDebug ./internal/ui ./internal/app`；无 tag 的同名 `-run` 只覆盖 `!viewportdebug` OFF 变体与不限标签的 JS 包装测试，不能替代 ON 变体及服务端诊断门禁。
2. 按真机矩阵保存截图、几何、会话和派生差值，特别复核 A 的 zoom 场景与 C 的持续 pan 场景。
3. 更新 PRD、CHANGELOG、当前兼容性说明和设计稿状态；历史候选文档标记为历史，不改写原始证据。
4. 评审最终 diff、保留回退点、合并后再关闭 #116。

**关闭条件**：R1–R6 的已签署范围全部通过，未覆盖项明确记录，旧 PLAN 的遗留项已解决或经产品同意正式降级。

## 6. 真机验收矩阵

| 场景 | 必须回答的问题 |
| --- | --- |
| PTY 静止、键盘开/关/重开 | 终端可用视口、输入区、quick-keys 和 Transport 是否完整可见；最终 rows/cols 是否一致 |
| 光标/内容位于上、中、下 | 是否只进行符合产品目标的必要调整，不把终端主体移出屏显区域 |
| 持续 pan / 键盘下触摸板与触摸 | 是否出现应用与 Safari 双重补偿；出现持续出屏即停止并回到 D1 |
| 本页新建/重命名/设置输入 | Safari 是否按输入位置处理；是否完全未触发 PTY 避让 |
| quick-keys 面板与粘贴 textarea | 面板可达与 Web 输入归属是否分别成立 |
| opencode / reasonix / dsh iframe | 父页面是否保持无干预，系统输入可见性是否成立 |
| 妙控键盘与浮动提示栏 | 是否避免误压缩终端，同时保持所需控件可达 |
| 横竖屏、Split View、Stage Manager | 输入归属和几何是否按新窗口重算，无旧恢复状态 |
| `scale=0.75`、scale 往返、zoom + 键盘 | 按已签署 D2 的非对称口径验收，不以 0.75 历史数据冒充新实现或 scale=1；另验菜单 / 妙控缩小但无键盘时不得误避让，以及 `scale>1` opt-out、变档 settle 后重判和 zoom 状态下键盘开 / 关 / 重开 |
| 前后台、键盘打开时刷新 | 是否重新识别输入归属和当前几何 |
| 暗色/浅色、safe-area | 截图是否无默认白画布缝，几何与视觉证据是否一致 |

iPhone Safari 按旧 PLAN 至少补一轮；不同 iPadOS 版本和 Chrome(iOS) 在设备可得时补充，未执行项必须列出。

## 7. 验证与资源纪律

1. 先纯函数/源码/针对性 Node，再受影响 Go 包，再真实流程，最后才是交付所需全仓门禁。
2. 一次只验证一个独立假设；旧测试通过不等于最终用户行为通过。
3. 浏览器并发为 1；不重复启动已有 daemon，不为取证临时替换服务。
4. 长任务必须有所有者、超时和清理路径，结束后确认进程树与端口释放。
5. 真机发现需要改变已签署原则才能解决的问题时，先讨论，不顺手修改。
6. 达到当前阶段门禁后停止，不为了增加“完整性”扩张工具、矩阵或重构。

## 8. 原子提交与回退边界

- 计划/原则、诊断工具、输入归属、PTY 几何、生命周期和交付文档分别评审。
- 产品实现只与该行为直接相关的测试/设计同提交；历史证据、流程文档和其他 issue 不混入产品提交。
- 每次提交前检查 staged 文件清单和 diff，不默认把工作区全部内容视为本任务产物。
- 保留 `2e857de`、`5268869` 及原则提交 `47c5c3e` 的可回退性；后续修复以新提交叠加，不改写历史。

## 9. 本轮未提交内容的处置基线

| 内容 | 处置 | 对应阶段 |
| --- | --- | --- |
| `sidebar_collapse.test.mjs` 对 `queueTTYResize` 的适配 | 独立测试提交，保留 | Phase 0 / Phase 4 回归 |
| `pty-refit-check.mjs`、`sidebar-title-check.mjs` | 独立验证工具提交，保留并在 Phase 2 复核判据 | Phase 2 |
| A/C 原始会话、连续 scale=1 / quick-keys 派生段、p8 原始记录 | 独立证据提交，原始与派生分开标识 | Phase 0 / Phase 6 |
| a22c 原始会话（`evidence/raw/a22c660f67a1347b1f4e38ca0126a7d0.jsonl`） | **判读先行（PLAN §5 + VALIDATION 2026-11-06 Phase 3 真机首轮节），raw 本体单独一并提交**；原始不改写、派生与原始分开，不新增 `.gitignore` 规则 | Phase 3 |
| `RESULTS-authenticated-shell-p9.json`、侧栏 baseline/working 结果 | 仅作历史结构/失败证明，不宣称最终真机验收 | Phase 2 |
| CHANGELOG 两处测试总数 27 → 34 | 独立计数修正提交 | Phase 0 门禁 |
| 无活动终端的 smoke、旧 p9 暂存实验 | 清理，不提交 | 已完成，若再出现同类内容继续排除 |

## 10. Phase 2 诊断基座仲裁后续台账与另立授权

本台账只登记评审后续，不改变 R1–R6、§3 或“诊断测量 ≠ 补偿”。除下方明确标记 `AUTH-20261105-*`、且写明“已仲裁批准”的独立诊断事项外，本台账不授权产品代码、诊断代码或 evidence 工具改动；这些例外也须待调度者独立派单后生效。

| 项目 | 仲裁处置 | 后续边界 |
| --- | --- | --- |
| E 系列余项：N-1、N-2、N-3、N-4、N-5 | 维持原状，仅记录 | 不改契约、不授权代码；须按原评审描述另立任务 |
| E 系列余项：NP-1、NP-2 | 维持原状，仅记录 | 同上 |
| E 系列余项：F-2、F-3、F-4、F-7、F-8 | 维持原状，仅记录 | 同上 |
| E 系列余项：m-8、m-9 | 维持原状，仅记录 | 同上 |
| C6 / N-6 / m-6 | **需用户确认**；建议形状错误仍 400，语义冗余改为留存 + verifier INCONCLUSIVE | 用户拍板前维持 937d91f 的 400 行为；若采纳变更，转编码 Sub-agent 另授权 |
| C-2 / F-1 的注释不符与可选指针化 | 本轮只把降级帧排他规则写入契约 | 修注释或改指针均须另授权；本轮不批准 schema / 产品代码改动 |
| C-1 / F-6 的 unlockTrace 读者 | 当前仅为可采证 groundwork，GI-2 集群维持 INCONCLUSIVE | 新增读者或判读流程须另授权 evidence 工具与对应测试 |
| NP-3 / F-5 的可选服务端收窄 | 当前传输层宽容声明为有意 | 若要求服务端强制“环成员 = 决策集”，转编码 Sub-agent 另授权 |
| PO-2026-11-04：debug 面板默认未展开 | **独立 follow-up，可改进项**；产品 owner 口头观察，无截图 / raw，INCONCLUSIVE。源码显示 More 次要控件默认隐藏、可手动展开（`viewport_debug.js` 1779–1782、1995–1999）；不据此判定 R6 / 冻结行为失败，也不产生 PASS | 先确认“未展开”指 More 次要控件还是主面板状态；另立默认态需求、影响范围与真机验收后再授权。当前不改产品代码、诊断代码或 evidence 工具，也不改冻结文档 |
| Phase 2 任务 5：PTY / sidebar 工具契约化终审（C1–C4、P4–P8） | 签署“现有判据已等于各自应承担的现行契约”的窄口径：PTY 属冻结 §7 / GI-7；sidebar 为工具本地规则 + shipped CSS、无 GI 落点，不立 GI-10；D2 由 `zoom_scale.test.mjs`、choiceA 离线回放与 Z 系列真机矩阵承担。fixture 计数采用 VALIDATION 单点登记：合法新增 fixture 须同变更更新 VALIDATION，工具注释去数字并指向该节；新增 judge 输入或改变 verdict 规则 / 既有预期仍须走判据变更路径。产物标记用 `artifactRev`，只表示契约对齐的产物修订，不代表判据版本 | 后续编码只落实终审所列注释 / 标记修订，不改判据；P6 默认 label 改名本轮不做，登记独立 follow-up。四命令 exit 0 仅为离线基线；Z 系列真机未做，不据此宣布任务整体正式完成。完整证据与读法见 VALIDATION“Phase 2 任务 5 工具契约化终审” |
| AUTH-20261105-D2-SETTLE-READONLY | **已仲裁批准、待调度者独立派单**；新增只读 `window.__mwTerminalSettle`，公开六个 scaleSettle 状态、pending / settled / return-pending、pass 来源 / 序号，并以固定 8 条只读 tick journal 逐条保留两个连续 500ms 产品 tick；数值型 `window.__mwTerminalOcclusion` 不变。只改既有诊断 mirror、探针 schema / 传输与直接测试，不改行为、timer、判据或 evidence `.mjs` | 与 D2 产品实现分离为独立诊断原子变更；Node + viewportdebug Go 门禁后双评审，再进入 r3 真机。未实施时 D-C 结构性 INCONCLUSIVE；字段白名单、可空 probe 对象、隐私与测试细则见 VALIDATION；r3-Z-MENU-01 已改承担 settle 序列，不冒充 25% / 50% 真机判别 |
| AUTH-20261105-D2-SETTLE-READONLY（实施复回收口，不另立授权） | **r3 前只准以下窄收口**：F-2 普通 1→2 / 回程 1→0 分链及 F-4 矛盾帧读者 INCONCLUSIVE 已在 VALIDATION 落地；F-3 必须在 `internal/ui/viewport_debug_on_test.go` 改用 `stripJSComments` 对 index 全文件计数 ==2；F-6 必须在 `internal/ui/debugassets/viewport_debug.js` 前置检查非单调 / 非 tick journal 等既有服务端形状规则，坏 settle 块降级 null + 固定 reason，直接测试限 `internal/ui/testdata/viewport_debug.test.mjs` | F-3 / F-6 可与当前实施同 diff、同次双评审；不得改产品清候选 / 发布顺序，不得新增 F-4 服务端跨字段 400，不得改 evidence `.mjs`。F-5、F-7、F-8、F-9、F-10 留 P3 follow-up 台账，无本轮代码授权。完整裁决、门禁与互核清单见 VALIDATION“实施双评审复回收口仲裁” |
| AUTH-20261105-DIAG-PANEL-CLIENT | **批准方案②、待独立派单**；unsupported 不再由 layout viewport 界触发，统一用 client 实际落点，并补快照横向 client 界；修 `viewport_debug.js` 及直接测试，不碰产品 raw / occlusion | 独立诊断提交，必须含 c3ee seq253 同形态 fixture、完整面板矩阵、双评审和真机复采；不得借既有 B-2 / E 系列 APPROVE 免审 |
| AUTH-20261105-DIAG-ZOOM-LABEL | **本轮不改、不派编码**；保留 zoomed-session-exempt 的旧基线标签含义，D2 继续以 reported + computed bottom 为准并记录矛盾 | 若未来确需先读 mirror 再定 derivedState，须另案授权、直接测试和双评审；不得夹入 settle 或面板修复 |
| AUTH-20261105-R3-GI3-READONLY | **批准仅诊断只读增补、待独立派单**；真机探针补前台 PTY 的 xtermRows client rect、rows/cols 与 applied/lastSent/pending 尺寸（不可得须显式标记），不采 instance id / 正文；沿用 pty-refit-check 的字段口径但不改其 judge、不新增 zoom 判据 | 与 settle 分包；探针 / 服务端 schema 及直接测试经双评审后执行 r3-D1-GI3。Headless pty-refit PASS 只证 GI-7 工具链，不关闭真机 GI-3 |
| 2026-11-05 42cd 混采单会话仲裁 | [42cd raw](evidence/raw/42cd679ea2cfdc147d6dc8b4ebdad910.jsonl) 连续 207 帧、唯一 init。**D-C settle 真机子项 PASS**：0.85 / 0.75 普通链均有 journal 1→2，回程链有 concrete→0 / ticked 1→0 / pending 与 returnPending 同帧解除；**D-B 纯变档可观察行为 PASS**：seq 49→50→53 与 53→54→57 覆盖活动中变档付 0、重建候选、settle 后重入。owner 截图目测符合预期仅作口头级旁证 | **D2 真机整行、F18、25% / 50%、D1、GI-3 均维持 INCONCLUSIVE**。menu/cmd 的 zoomSource 不可分，B / C 仅作形态记录；深键盘同越两线，重入不能证明“不新置锁”或门线选择；F18 候选链存在中间终端点击；GI-3 新增 seq 29–54 helper rect 958/975 停滞观察。完整 seq / journal 证据见 VALIDATION 42cd 专节；本行不升格单源 Z 验收、不关闭 R2 / Phase 3 |

## 11. Phase 2 正式收口仲裁（2026-11-05，owner B）

### 11.1 收口原则与逐门禁裁决

owner 选择 B：**就此打住，未闭环项按已登记状态如实收束，Phase 2 结束。** 本仲裁据此结束的是 Phase 2 的持续推进，不是把缺口改判为 PASS，也不是豁免后续阶段的实现、签署或验收义务。本 LLM 周期不再运行真机补采 / 补验；后续只有在对应阶段重新派单时才激活。

| 门禁 | 正式裁决 | 判据与保留项 |
| --- | --- | --- |
| ① 诊断 Capture 在要求的真机场景中可达 | **部分达成＋登记，不判无例外 closed** | 三份新 schema 会话共 810 帧，面板自报 `supported` 809 帧；c3ee 四角与 free 有 32 次手动 Capture，42cd 另有 8 次，均自报 supported 且在可视区。唯一 `unsupported` 是 c3ee `seq253` 的已确认工具误判，已由 `938fcd2` 修复；但完整键盘 × pan × 居位组合未全部真机执行，且 `seq253` 同形态 free 复采未做，故保留 G01。详见 VALIDATION 收口复核节 |
| ② D1 有实测依据 | **明判：有部分实测依据，但不足以决定 D1；部分达成＋登记** | 聚焦目标身份、归属与同帧 rect 已实测成立；raw 也存在非零 `offsetTop` 的 focus 帧，不能写成“完全没有 pan 样本”。但同条件普通 Web input ↔ xterm helper 对偶链为 0，Safari 内部 pan 归因及同帧子帧时序仍 INCONCLUSIVE。D1 维持“仅禁止手段部分签署”，正向机制未选、未验证，Phase 4 对应实现不得开工；保留 G02 |
| ③ D2–D4 有明确结论 | **closed（仅决策结论域）** | D2 已签署，且有指定行为子项 PASS 与真机整行 INCONCLUSIVE 的明确分层；“整行 INCONCLUSIVE”是明确证据结论，不是无结论，也不是行为 PASS。D3 已签署等价性口径，D4 已签署 refit 分类与最终一致口径，二者缺的是 Phase 4 实现和 Phase 4/5 实测，不是设计结论。实测缺口分别保留 G03–G07 |
| ④ 设计源、PNG 和验收矩阵一致并签署 | **未达成＋显式推迟** | `ef5037b` 已交付 v4 设计源与 6 张 PNG，但 EXPORT-MANIFEST 仍明确“设计评审与 Phase 2 门禁待签”；v4 又形成于 D2 签署及 c3ee / 42cd 子项判读之前，当前没有设计源 / PNG / 最新验收矩阵的一致签署记录。不得追认 all-closed；设计签署推迟为 **Phase 3 开工前置**，保留 G08。补签可如实签署含 INCONCLUSIVE 的矩阵，不要求把未验项伪造成 PASS |
| ⑤ 实时 Chrome/CDP 问题单独归因，不与产品修复捆绑 | **部分达成＋低优先级登记** | 既有记录已把页面启动 / CDP 无响应隔离为独立验证环境或工具问题，未与产品修复捆绑，也未据此判产品 FAIL；但具体根因与实时 verdict 均未取得，本仲裁未重启 Chrome / CDP。隔离义务已执行，归因与 live 检查未闭环，保留 G09 |

**总判定：Phase 2 为“收口（有显式登记缺口）”，不是“门禁全部通过”。** 其中门禁③的 closed 只覆盖 D2–D4 的决策明确性，不上浮任何真机整行 verdict。

### 11.2 VA-IDAP 主简报底稿：已提交产物汇总

| Phase 2 任务 / 关联线 | 已提交层级 | 收口状态（供最终简报引用） |
| --- | --- | --- |
| 任务 1：诊断字段与归属 / 宿主 rect 能力 | **`937d91f`**（任务描述中的 `937d914` 为不存在编号） | 诊断 schema、归属类别、活动输入宿主及同帧 rect、面板自省已交付；测试与双评审完成。工具能力不等于所有真机命题已验 |
| 任务 2：Capture / 面板可达性 | `937d91f`＋**`938fcd2`** client 坐标修复 | 已提交并有真机主体证据；四角与 free 有实际手动 Capture。完整组合及 `seq253` 同形态复采按 G01 登记，不写全矩阵 PASS |
| 任务 3：D1 对比测量 | `d42b62e`（c3ee raw）、`f394d48`（b746 raw）、`14c2b90`（42cd raw）；`1488ead`、`9df1914` 判读 | **部分达成＋G02 显式推迟**：身份 / rect 与若干 pan 替代事实存在；对偶归因不足，D1 正向机制仍未签署 |
| 任务 4：v4 设计源与 PNG | **`ef5037b`** | 设计产物已提交；与最新矩阵的一致签署未完成，按 G08 推迟至 Phase 3 开工前 |
| 任务 5：PTY refit / sidebar 工具判据 | **`4a17f9c`**；`14c2b90` 仅为 42cd raw 归档，不是工具判据提交 | 判据窄口径 closed：PTY = 冻结 §7 / GI-7 重述，sidebar = 工具本地规则＋shipped CSS；判据类双评审 APPROVE。实时 Chrome/CDP verdict 未取得，按 G09 独立登记 |
| 关联 D2 实证（不另立 Phase 2 任务） | `a260763` 实现；`4383788` settle 只读诊断；上述 raw / 判读提交 | 已签署＋指定子项级 PASS；D2 真机整行、25% / 50%、D-B 锁判别、F18 等仍 INCONCLUSIVE，按 G03–G05 由 Phase 5 承接 |

### 11.3 Phase 2 收口时显式登记的已知缺口

下表是本周期停止补采 / 补验时的**未闭环清单**，不是关闭清单。各项保留原有 PASS / FAIL / INCONCLUSIVE 状态；后续承接不表示已获实现或采证授权。

| 编号 | 已知缺口与当前状态 | 后续承接 |
| --- | --- | --- |
| **G01** | 面板完整真机组合未覆盖；`938fcd2` 的 c3ee `seq253` 同形态 free 复采未做。其余已观测帧不上浮为完整矩阵 PASS | **Phase 3 真机验收的诊断前置**；Phase 5 全表面回归复核 |
| **G02** | **2026-11-06 owner 裁决关闭（限制接受）**：D1 正向机制按 owner 产品裁决接受现行为（低概率 pan、可恢复；未见致命遮挡）并挂起；证据子项仍 **INCONCLUSIVE**（同条件 Web ↔ helper 对偶帧为 0、Safari 归因不可采），关闭不是证据 PASS。owner 原文、现象与归因边界见 [VALIDATION 2026-11-06 专节](evidence/VALIDATION-2026-11-04.md) | **解除 Phase 4 的 D1 相应验收前置阻断（owner 限制接受）**，不再等对偶帧，不启动 Phase 3/4 的 D1 归因或机制选择义务；本行是最新有效口径，覆盖本文此前 D1 阻断表述。**Phase 3 输入所有权拆分（G10）仍照常**，其他独立门禁不变 |
| **D1-FOLLOWUP（新 issue 候选，未开启）** | 当前不另开 issue、不为该非致命缺陷继续补采；保留 D1 证据子项 **INCONCLUSIVE** 与上述现行为接受口径 | 未来若复现率可观，或发生“pan 不抬升且终端不抬升、光标被遮挡”等致命 / 不可恢复遮挡，依据 VALIDATION 2026-11-06 专节开独立 issue 处理 D1 pan 归因与机制选择；不追溯转化为本次 Phase 3/4 的 D1 义务 |
| **G03** | D2 的 Z-PINCH / Z-MENU / Z-MAGIC 三条独立单源会话、完整截图 ↔ seq 链与整行形式要件未满足；整行 **INCONCLUSIVE** | **Phase 5** D2 真机矩阵与 A0 0.75 锚点验收 |
| **G04** | 本机 25% / 50% 中间判别带不可达；深键盘重入不能证明门线选择、D-B 不新置锁或 D-D latch 语义，相关真机子项 **INCONCLUSIVE** | **Phase 5**；仅在未来设备 / 形态自然产生可判别几何时补采，不强行造数，Node 逻辑回归继续独立承担实现覆盖 |
| **G05** | F18 虽有连续持焦与开→关→重开几何链，但重入前存在中间终端 click；干净段 **INCONCLUSIVE** | **Phase 5** 键盘生命周期回归 |
| **G06** | D3 已签署但隐藏 / 非活动宿主的实现形态与切回旧实例真机等价性未验证 | **Phase 4** 实现选择；**Phase 5** 实例切换 / 隐藏宿主实测 |
| **G07** | D4 已签署但 refit 时序实现与最终一致性未实测；GI-3 helper rect 停滞及 rows/cols / ack 真机链 **INCONCLUSIVE** | **Phase 4** refit / ack 实现与定向验证；**Phase 5** 生命周期回归 |
| **G08** | v4 设计源 / PNG 与最新验收矩阵的一致签署未完成 | **Phase 3 开工前置补签**；必要时先同步设计源与 PNG，不在本次收口擅自改设计产物 |
| **G09** | 实时页面启动 / CDP 无响应的具体根因和 live verdict 未取得；不是产品 PASS，也未证明产品 FAIL（低优先级独立工具链项） | **Phase 5** 浏览器全表面回归前排期；需要最终实时证据时最迟并入 **Phase 6** |
| **G10** | D-A 过渡期普通 Web input 触发终端避让的 **R2 已知偏离**仍存在；c3ee A3 与 42cd HO-3 是现场实例，不是 D2 PTY 证明 | **Phase 3** 输入所有权拆分与 Web 原生交接门禁 |

### 11.4 诚实收束声明

- owner B 的含义是**本周期不再采真机、不再为 Phase 2 缺口追加工具或测试轮次**，不是这些缺口以后永久免验。
- 所有 INCONCLUSIVE 项继续原样保留；本登记只改变 Phase 2 的推进状态，**不改变任何行为 verdict**。
- D2 子项 PASS 不覆盖整行；D3/D4 设计签署不覆盖实现与真机；D1 有部分实测依据不等于正向机制已签署。
- Phase 2 收口不自动放行 Phase 3/4/5，不关闭 R1–R6，不表示 #116 已完成；各后续阶段仍按本计划独立派单、验证和回退。

## 12. owner B 最终决策登记与探针增补授权（2026-11-07，issue #116）

**本节性质**：对 §5 二选一处三段追加注记（原二选一表、代码级候选补登、候选矩阵 v2）的**纯追加**登记。**不改写、不删除上文任何既有行**；其中"两选项并列待决""建议维持（不等于决定）""归因任务建议本轮不签发""(c)(ii) 本轮不签发"等表述**保留原状**（当时登记状态，可追溯），其**现行效力**由本节统一覆盖。本节不改变 R1–R6、§3、§10 / §11 台账，也不改变任何 verdict。

### 12.1 决策①：维持 `2b64e7e`，B 处置收口，归因闭环归 Phase 4

- **owner 决定（2026-11-07，本票生效）**：**维持 `2b64e7e`**。B 按「**候选 A13 + A12 / A11 领衔、候选 A1 降权、候选 A7 判死**」收口，**归因闭环归 Phase 4**。
- **对早前登记的取代范围（仅限效力，不改写正文）**：
  - §5 二选一表中「**B. 回退至 `0258f1e` 一带（守卫前）**」选项**不再并列待决**，**已被本决策取代**；表中"A. 维持 `2b64e7e`"成为**唯一生效处置**。F5 粘贴框抢焦（原始缺陷 A）与 S2 焦点常驻的 PASS 登记**原样维持**。注：§11.3 的 **`G02`（D1 限制接受）同样以"选项 B"为名**，那是 **Phase 2 收口裁决**、**与本票 B 无关**，不受本节影响——两处"B"不得混读。
  - §5「owner 条件式指令的登记状态」中"如归因不出原因 → 回退"的**前提未被触发为生效决定**，由本决策直接落定。"仲裁的建议倾向（不等于决定）"改由 owner 决定**落地**。
  - §5 候选矩阵 v2 追加注记 **(c)(ii)** 的"本轮不签发探针增补授权、留待 owner 同意后另行派单"，其**"owner 已同意"条件已满足**，见 §12.2；"(i) Phase 4 pan 域"路线**不变、仍须 owner 新指令 + 仲裁授权**。
  - §5「后续归因任务建议」的"本轮只登记，不签发授权"，其**时效**由 §12.2 取代；但该节 ①–④ 字段**不是**本授权的清单（见 §12.3）。
- **闭环状态（防上浮）**：**B 仍未归因，证据状态维持 INCONCLUSIVE。** 本决策收的是"维持哪个构建、归因走哪条路线"，**不是** B 的证据结论。任何人不得据本节把 B 写成"已归因""已修复"，也不得据此改判 R2 / R3 / R4 或任何既有帧级 verdict。

### 12.2 策略②：批准探针 additive schema 增补路线（"先 1 后 2"之路线 2）

- owner 批准在 **Phase 4 之前**签发探针增补授权，目的是把 B 从"候选"推进到"帧真值归因"。该批准**移除**矩阵 v2 的"字段未落地"阻塞，**不改变**"拿到真值字段前矩阵只能是排序"的限制。
- **授权已于本票签署**：`AUTH-20261107-P3-PROBE-TOUCHV2`（评审对比基线 = `cc9a8d8`），正文见 §12.4 与 [VALIDATION 同节](evidence/VALIDATION-2026-11-04.md)。
- **本票不派编码**：签署只表示**边界已定**；实现须由调度者**独立派单**。
- **Phase 4 归口不变**：B 的归因闭环归 Phase 4；候选 A1／候选 A2(document-touchmove) 的**收敛**（改动"无条件锁"契约，涉及 `terminal_touch_scroll.test.mjs:143/151/190/199`、`ui_test.go:2119-2142`）仍须 **owner 新指令 + 仲裁授权**。探针路线只**测量**，不替代 Phase 4 的设计／实现。

### 12.3 `AUTH-20261107-P3-PROBE-TOUCHV2`（摘要；正文以 §12.4 为准）

| 维度 | 内容 |
| --- | --- |
| 标识 / 基线 | `AUTH-20261107-P3-PROBE-TOUCHV2` / `cc9a8d8` |
| 允许文件 | `internal/ui/debugassets/viewport_debug.js`（新增 `touchTruth` 只读块）、`internal/app/viewport_debug.go`（放行 + 校验）、直接测试新增（`viewport_debug.test.mjs` / `viewport_debug_test.go` / `viewport_debug_on_test.go` 字面量） |
| rev 口径 | `PROBE_VERSION` **不动**（`p9-full-shell-session-avoidance-v1`）；`SCHEMA_REV` → `p9-additive-touch-truth-v1`；服务端正则**三 rev 并存** |
| 禁止 | `index.html` 语义、FOCUS-GUARD、避让状态机、quick-keys、TTL／容量／体积机制、`evidence/*.mjs`、`PROBE_VERSION` bump、新字段接进任何 verdict、`mousedown` listener 写 DOM／`passive:false` |
| **不覆盖** | `showModal()` 前后 `activeElement` 显式序列（观察者嵌入被观察面，须另立授权） |
| 真机 | **本授权不做**，由调度者 / owner 另行排期 |
| 停止条款 | 触碰禁止项、或需改"授权冲突清单"①–④ 之外的既有断言 ⇒ 停止回报 |

### 12.4 授权冲突与自洽注记（Q3）

- **FOCUS-GUARD 禁令 vs 本授权**：`AUTH-20261105-P3-FOCUS-GUARD` 明列"禁止改 `internal/ui/debugassets/*`、服务端、探针 schema、新增事件源"。该禁令**在其包范围内继续完全有效**；`AUTH-20261107-P3-PROBE-TOUCHV2` 是**另一独立诊断包**，只豁免**自身列明的**探针 additive 面，**不重开** FOCUS-GUARD 的产品面（`focusTerminalIfPossible()` 函数体、`focusEditable` 谓词、事件监听结构、跨 pass 状态）。两包在 `index.html` 内**依然不相交**（本包对 `index.html` **零改动**）。
- **rev bump 的既有断言冲突（已裁定，走同步而非停止）**：`viewport_debug.test.mjs:1257` / `:1336`、`viewport_debug_on_test.go:158` 三处 `p9-additive-settle-readonly-v1` 字面量是 **rev bump 的必然同步面**，允许**且必须**在同 diff 内**仅改该字面量**；`internal/app/viewport_debug_test.go` 的 `schemaRev` 行只准**新增**，`:266` 的 `p10-invented-revision` 拒收断言**必须保留**。除此之外任何"需改既有断言"⇒ 触发停止条款。
- **"维持"与"Phase 4 归口"的语句调和（防自相矛盾）**：本 PLAN 同时出现"维持 `2b64e7e`"与"归因闭环归 Phase 4"，二者**不冲突**——**维持**针对**产品构建与处置**，**归 Phase 4** 针对**归因与根因收敛**。为避免被读成"B 已闭环"或"回退仍在台面"：①`2b64e7e` 是**现行产品构建**，无回退计划；②B 的**根因**仍未知（INCONCLUSIVE），其**收敛**留 Phase 4 + 探针真值路线；③*本 PLAN 不含*任何"回退 `0258f1e`"的生效条款，§5 该选项仅作历史登记。
- **矩阵权重与结论的分界（复用既有口径）**：矩阵 v2 权重（`A13 > A11 > 候选 A2 > A8 > A12 > 候选 A1 > A7判死`）是**排序**；owner 确认的是**排序与收口处置**，**不是**任何候选为根因。`A7` 判死作为**静态审计结论**（代码存在、通路不可达）维持有效，其重开条件见 §5。
- **本节不改项**：R1–R6、§3、§7、§8、§9、§10、§11 台账、G01–G10 状态、raw 处置裁决（a22c / 2a33）、探针既有字段排布，以及 `unlockTrace` 排布**全部不变**。

**本节结论**：owner 两项决策已登记；**维持 `2b64e7e`**，B 处置收口、归因归 Phase 4；**`AUTH-20261107-P3-PROBE-TOUCHV2` 已签署待独立派单**；**B 证据状态维持 INCONCLUSIVE**，未做真机验证，不关闭 R2 / R3 / R4 或 #116。

### 12.5 PROBE-TOUCHV2 评审轮与返工追认（2026-11-07）

> **本节性质**：**纯追加登记**。**不重新打开、不修改、不放宽**上方 §12.1–§12.4 与 [VALIDATION 授权正文](evidence/VALIDATION-2026-11-04.md) 的任何授权语义、文件清单或禁止项；已签署正文**一字不改**，两处与之不符的字段／枚举以**追加订正注记**处理（见 §12.5.2）。
> **基线**：评审对比基线 `cc9a8d8` = 工 HEAD `d1bc6c8` 之上、本包 5 个文件的**未提交改动**（`git diff --stat`：`viewport_debug.js` 2481 行 / `viewport_debug.test.mjs` 2376 行 / `viewport_debug.go` 1349 行 / `viewport_debug_test.go` 1361 行 / `viewport_debug_on_test.go` 1 行字面量）。本仲裁轮**只读代码**、**不改任何产品／测试文件**、**不执行 `git add` / `git commit`**。

#### 12.5.1 双评审结论（首轮 → 返工 → 第二轮）

| 轮次 | 评审 1 号（`opencode-go/muse-spark-1.3-contributor`） | 评审 2 号（`minimax-cn/MiniMax-M3.1-Flash`） |
| --- | --- | --- |
| **第一轮** | **APPROVE**（0 BLOCKER / 0 MAJOR） | **NOT APPROVE**：**MAJOR×3**（M1 / M2 / M3） |
| **返工** | 改名（`synthesizedOnTerminalHost` → `onTerminalHost`）／`report()` 延迟回读 + 置 `null`／宿主命中改走产品窄口径 `mousedownInsideTerminalHost`／补负例 | 同左（同一 diff） |
| **第二轮** | **APPROVE**：M1 / M2 / M3 **全部关闭**，逐项给出锚点；遗留 MINOR×3 / CHALLENGE×1 | **APPROVE**：用**变异实测**（`/tmp` 内）证明 M3 负例**承重**（去掉窄口径即翻红）；`synthesizedOnTerminalHost` 生产代码**0 残留**（仅测试护栏 2 处）；6 项门禁全绿；遗留 MINOR×4 / CHALLENGE×3 |

**MAJOR 逐条关闭锚点（两评审第二轮一致，仲裁已独立复读源码核对）**：

| 编号 | 首轮问题 | 返工落点（行号为本工作区实际） | 关闭依据 |
| --- | --- | --- | --- |
| **M1** | `mousedown.defaultPrevented` 在 capture 相位读，结构上恒 `false`，与产品 TARGET/BUBBLE 相位的 `preventDefault` 永不重叠 ⇒ 字段**测不出真值** | `viewport_debug.js:1077` `pendingMousedown` 声明为 `null`；`:1182` 仅存**事件引用**；`:1187-1189` `report()` 内**延迟回读** `pendingMousedown.defaultPrevented` 后**置 `null`**；`:1175-1179` 注释声明 `defaultPrevented:false` 为**暂定值、从不原样发布**；`:968-976` 写明"capture 相位读取结构上为假"的理由 | 引用不跨帧存活（`:1189` 置 `null`），回读发生在事件已派发之后；与既有 `clickSuppressed` 的延迟回读**同构** |
| **M2** | 配套用例用**测不出真值的字面量**断言（自证自话） | `viewport_debug.test.mjs:2264` 构造时 `defaultPrevented:false`、`:2266` 先断言 capture 相位仍为 `false`、`:2267` **派发后**才置 `true`、`:2273` 断言帧内读到 `true`；`:2276-2283` 再证"引用已弃、不能回溯改写已结算帧" | 用例**变异敏感**：读点若退回 capture 相位，`:2273` 必红——测的是真值而非字面量 |
| **M3** | 宿主命中口径与 ownership 谓词**不同源**；`synthesized` 命名**过度承诺**（探针无法区分 isTrusted） | 口径：`viewport_debug.js:1045-1065` `mousedownInsideTerminalHost()` = 首个真值 `data-instance-id` 祖先决定、**不回溯**、且须为 `#terminal-container` 的**直接子元素**（容器只读）；负例：`viewport_debug.test.mjs:2301-2307`（模板 stamp 在容器**外** ⇒ 计数不涨）；命名：`:1173` `onTerminalHost`，`:1167-1170` 显式声明**不区分合成与真实** | 负例**承重**（2 号评审变异实测已证）；生产代码旧名**0 残留** |

#### 12.5.2 两处授权偏差的追认（**越权 = 否**）

**偏差 A · 字段名 `synthesizedOnTerminalHost` → `onTerminalHost`**

| 项 | 授权正文（`AUTH-20261107-P3-PROBE-TOUCHV2`，VALIDATION:741） | 本包实际实现 |
| --- | --- | --- |
| 字段名 | `synthesizedOnTerminalHost` | **`onTerminalHost`**（`viewport_debug.js:1173`、`viewport_debug.go` Go 孪生、`viewport_debug.test.mjs:2271/:2291/:2305/:2314`、`viewport_debug_test.go:1322/:1324/:1336`） |
| 语义主张 | 只记**到达**与真值 | **不变**：两端（客户端 `:1167-1170`、服务端注释）均已声明"**不区分合成与真实**，isTrusted 不可得、不做信任分类" |
| 事由 | — | 评审 **M3**：`synthesized` 一词声称了探针**无法铸造也无法读取**的信任分类，属**过度承诺**；改名使其与可测事实对齐 |
| 裁决 | — | **追认。非越权**：改名是在**授权同意的字段面内**收窄**名称**、**不扩大**采集面与只读性；字段语义**不弱化**（原字段本就不承载信任判定，两侧正文已如此声明）；旧名在门面上**锁死为 400**（见下条），无静默兼容 |

- **旧名护栏（防漂移）**：`viewport_debug_test.go:1336` 以 `{"the retired synthesizedOnTerminalHost name", …}` 断言**旧名必须 400**，理由：`DisallowUnknownFields()` 使未知字段即 400，旧客户端仍说旧名必须**在门口被抓住**，而非被静默补零。仲裁轮以 `-tags viewportdebug` 实跑，该子用例 **PASS**（见 §12.5.4）。
- **`synthesizedOnTerminalHost` 全仓残留**：生产代码（`internal/ui/debugassets/viewport_debug.js`）**0 处**；仅测试护栏 2 处（`viewport_debug_test.go:1332` 注释、`:1336` 断言）。

**偏差 B · 服务端交叉校验 `cancelled ⟺ endType == "touchcancel"`**

- **事实**：授权正文明列的服务端校验（VALIDATION:752）为 `taps`/`userActivation` 长度 ≤ 8、计数非负、`dyMax`/`dyCumulative` 有限、`targetTag` 匹配 `viewportDebugTagPattern`、`endType ∈ {touchend, touchcancel}`；实现另有 `viewport_debug.go:1277-1282`：`tap.Cancelled != (tap.EndType == "touchcancel")` ⇒ `errors.New("touchTruth cancelled must agree with endType")`（返回 400），超出该枚举清单。
- **事由**：调度者派单**小项 2** 追加。
- **裁决**：**追认，定性为「附加一致性校验」，非门禁语义扩权**。理由三条：①它校验的是**同一事实的两种写法**是否自洽（`viewport_debug.go:514-525` 注释即此口径），不引入**新的事实维度**、不改变任何字段语义；②它**只收紧自相矛盾帧**（`touchend` 标 `cancelled`、或 `touchcancel` 未标 `cancelled`），**不使任何自洽的合法帧失败**——授权框架内"只有漂移帧被拒"的增量，与"旧 rev 帧永远继续可上传、不 backfill"的口径**不冲突**；③它命中服务端**既有**的拒收通道（`DisallowUnknownFields` 同门的 `validateViewportDebugReport`），**未触碰**禁止项 3（TTL／容量／体积机制）与禁止项 5（新字段进 verdict）。负例已双向下锁：`viewport_debug_test.go:1330-1331`，实跑 PASS。
- **边界**：该 400 只说明**传输漂移**，**不构成**任何 verdict，不改变 `INCONCLUSIVE` 状态。

**"越权 = 否"的总理由（三项）**

1. **字面量护栏 400（测试锁死）**：两处偏差都配了**方向明确的测试锁**——旧名 ⇒ 400（`viewport_debug_test.go:1336`）、`cancelled` 矛盾 ⇒ 400（`:1330-1331`，双向）。偏差**没有**留下"两种写法都接受"的静默兼容面。
2. **口径收紧方向与授权禁止清单无冲突**：M3 的返工方向是**向产品窄口径收敛**（`index.html:3915-3936`），而非放宽；且 `index.html` **零改动**（`git status --porcelain internal/ui/static/index.html` 为空），`evidence/*.mjs` **零改动**，`PROBE_VERSION` 仍 `p9-full-shell-session-avoidance-v1`（`viewport_debug.js:80`）、`SCHEMA_REV` = `p9-additive-touch-truth-v1`（`:344`）、服务端正则**三 rev 并存**（`viewport_debug.go:95`）、无 `passive: false`、`mousedown` 监听零 DOM 写 —— 禁止项 **1/2/3/4/5/6/7/8** 逐条**未触碰**。
3. **`dyMax` 语义落差 = 措辞落差，登记不死结**：授权正文（VALIDATION:738）只**列举字段名**、未定义 `dyMax`；实现（`viewport_debug.js:1134-1135`）取**单步位移的 max |dy|**（幅度，符号统一为正），而 `dyCumulative` 是**带符号累加**。二者是**两个不同量**，授权文本对前者叫"最大"并无"累计最大绝对值"之外的相反约定，故属**措辞落差**，判读时**以本登记的"幅度 = 单步 max abs"为准**；不在本轮改写授权正文。**对 B 的判读影响**：单指单链下幅度与位移同向可用；多指抖动见下 §12.5.3 KNOWN 限制条，须随帧一并标注。

#### 12.5.3 遗留 MINOR / CHALLENGE 分级汇总（**只登记，不阻塞提交**）

| 来源 | 编号 | 内容要点 | 归属 |
| --- | --- | --- | --- |
| 1 号 | MINOR×3 | 命名／注释可读性级意见 | **后续判读须知**，低优先留待 |
| 1 号 | CHALLENGE×1 | 已由 `DELIBERATELY LOOSE` 口径与差异方向注释在案（同 §12.4 既有口径） | **后续判读须知** |
| 2 号 | MINOR×4 | 测试与注释的次要收敛点（含注释措辞、用例组织） | **低优先留待** |
| 2 号 | CHALLENGE×3 | 判读工具侧提醒（未知字段必须**忽略**；`dyMax`/多指语义随帧标注） | **后续判读须知** |
| 本仲裁 | 观察 | 1 号第二轮给出的"负例"锚点 `:2301-2307` **实际为 `viewport_debug.test.mjs`**（非 `viewport_debug.js`）；`viewport_debug.js:2301-2307` 是启动序列，与负例无关 | **已在本节**按测试文件登记，无需返工 |

- **多指限制（随帧标注，不改代码）**：`viewport_debug.js:996-1002` 已声明 KNOWN 多指限制（第二条 `touchstart` 重启链、只跟 `touches[0]`、换指时 `dyCumulative`/`dyMax` 可跳变）。**判读任何多指帧时，`taps[]` 数值不得作单指真值使用**；B 为单指点击，本限制不构成阻塞。
- **`focusEditable` 的额外条件不并入本计数**：`index.html:3931-3932` 的 ownership 谓词额外要求 `hostNode.style.display !== "none"`，而 `mousedownInsideTerminalHost` **只**用祖先布局规则、**不加** foreground/display 门（`viewport_debug.js:990-994` 已把该不对称声明为字段边界）。这是**刻意的窄化后仍不相等**，属后续判读须知，**不在本包修**。

#### 12.5.4 门禁：8 项数字与三方复跑记录

| # | 门禁项 | 结果 | 2 号评审 | 本仲裁轮**独立复跑**（2026-11-07） |
| --- | --- | --- | --- | --- |
| 1 | `viewport_debug.test.mjs`（node --test） | **54 / 0** | ✅ | ✅ `tests 54 / pass 54 / fail 0`；帧字节 `8735`、`touchTruth` `1565`、门 `65536`、预算 ≤`16384` |
| 2 | Go `viewportdebug` 双包 | **ok** | ✅ | ✅ `ok internal/ui`、`ok internal/app` |
| 3 | Go 三锚点 | **ok** | ✅ | ✅ `-count=1` 非缓存：`TestViewportHeightHandling` / `TestZoomScaleAvoidance` / `TestViewportDebugHandling` 全 PASS |
| 4 | `verify-live-choiceA.mjs` 离线原值 | **PASS** | ✅ | ✅ `--html internal/ui/static/index.html` **PASS**；该 `.mjs` **零改动** |
| 5 | `viewport_height` | **40 / 0** | ✅ | ✅ `tests 40 / pass 40 / fail 0` |
| 6 | `zoom_scale` | **37 / 0** | ✅ | ✅ `tests 37 / pass 37 / fail 0` |
| 7 | `sidebar_wt_rename` | **18 / 0** | ✅ | ✅ `tests 18 / pass 18 / fail 0` |
| 8 | `focus_terminal_guard` / `terminal_status` | **5 / 0** · **68 / 0** | ✅ | ✅ `5 / 0` · `68 / 0` |

- **三方独立复跑**：**2 号评审**按 a–f 原文逐项复跑；**1 号评审**报 8/8 门禁绿；**本仲裁轮**按上表逐项**重新执行**（node 五项 + Go 三项 + choiceA 离线），数字与派单记录**逐项一致**。
- **补充实证（仲裁轮）**：`go test -count=1 -tags viewportdebug -run TestViewportDebugRejectsMalformedTouchTruthBlocks ./internal/app` 中 `the_retired_synthesizedOnTerminalHost_name`、`a_touchend_flagged_cancelled`、`a_touchcancel_not_flagged_cancelled` 三个子用例 **PASS**——两处偏差的 400 护栏**有实测承重**，不是孤立写法。
- **缓存说明**：锚点首跑返回 `(cached)`，已用 `-count=1 -v` **重跑**取得真实执行证据（`0.34s` / `0.33s` / `0.46s` 全 PASS），不以后者替代前者。

#### 12.5.5 状态边界与真机口径（**防上浮**）

- **提交边界**：本包**产品实现 + 直接测试同 diff 成一个原子提交**（`internal/ui/debugassets/viewport_debug.js`、`internal/app/viewport_debug.go` 与三处测试文件同步面）；**文档登记（本节 + VALIDATION 同节）单独提交**。措辞与 §12.x 既有 FOCUS-GUARD 边界 (`PLAN:272` / `:289` / `:300`："产品守卫 + 其直接 minimal 测试为**一个原子提交**；文档登记（本节 + VALIDATION）**单独提交**")**完全一致**：不夹带 raw 文件、不夹带其他包改动、不与文档登记混入。
- **本授权不 substitute 真机**：离线 8/8 全绿 + 双 APPROVE **只证明**探针 additive 面正确、门禁无回归，**不产生**任何真机触摸证据。**B 的证据状态维持 INCONCLUSIVE**；`touchTruth` 真机帧的采集与判读是**一个独立后续**，**owner = 调度者**（约时另行安排）。本节**不**关闭 R2 / R3 / R4，**不**改判任何 verdict，**不**把 B 写成"已归因 / 已修复"。
- **文档欠账（本轮已清）**：VALIDATION:741 的旧字段名已在同文档**追加订正注记**（不改写原行），无需另派 docs-only 工单。

**本节结论**：`AUTH-20261107-P3-PROBE-TOUCHV2` 的编码产出经**第二轮双评审双双 APPROVE**，3 项 MAJOR **全部关闭**并有锚点；两处授权偏差（字段改名、`cancelled` 交叉校验）**追认，越权 = 否**；门禁 **8/8** 经**三方独立复跑**一致。**B 维持 INCONCLUSIVE**，真机帧判读为独立后续，不关闭 #116。

### 12.6 Phase 3 真机 p3-B-EVID-01 B 归因真值轮判读（2026-11-07，`touchTruth`）

> **本节性质**：**纯追加登记**。**不重新打开、不修改、不放宽** §12.1–§12.5 与 [VALIDATION 授权正文](evidence/VALIDATION-2026-11-04.md) 的任何授权语义、文件清单或禁止项；已签署与已登记正文**一字不改**。本节只**登记真机判读结果**，**不改任何产品／测试代码**，**不执行 `git add` / `git commit`**。判读全文（含逐击真值表、独立抽验表、缺口清单）见 [VALIDATION「2026-11-07 Phase 3 真机 p3-B-EVID-01 B 归因真值轮（touchTruth）」](evidence/VALIDATION-2026-11-04.md)。

#### 12.6.1 会话与抽验（本仲裁**独立实测**，非转述）

- **会话**：`382f4da6a88a077f8ac0fea79d8ff3d8`，**497 帧**（`seq 1–497` 连续无缺口），raw **已冻结归档未提交**，md5 `50e94c14540b5c74e5f840daf5dc87a4`（本仲裁 `md5sum` 复核一致）。工 HEAD `583681f`；工作区 `git status` 唯一未跟踪项即该 raw。
- **探针身份**：`probeVersion=p9-full-shell-session-avoidance-v1`（未 bump，符合 §12.3 口径）；`schemaRev=p9-additive-touch-truth-v1` **497/497**；`touchTruth` 块 **497/497**。
- **抽验 6 点全部独立复跑**（`a`–`f`）：`(a)` 覆盖 497/497 ✅；`(b)` F1/F2/F3 的环条目与 `mousedown.tick` 冻结（88 / 101 / 106 分别等于前序帧）✅；`(c)` S1–S5 的 native `mousedown` + `click` + 键盘顶起（`md.tick` 96/101/106/111/114、`click` 14/15/16/17/18、`vv 748→320` 且 `offsetTop 0→127`）✅；`(d)` `touchcancelCount` **497/497 = 0** ✅；`(e)` `userActivation` 去重并集 **52 条、52/52 `isActive=true`** ✅；`(f)` journal 四类**流入**条数 `64+68+27+25 = 184` 与 `counts` 相符 ✅，但**"零淘汰"表述不成立**——`TRACE_MAX_ENTRIES=16`（`viewport_debug.js:449`），终帧 `entries` 恰 16 条、**168 条已淘汰**，已按**订正注记**处理（不改写抽验原文）。

#### 12.6.2 判读结论与候选新排序（**排序数值化，仍 INCONCLUSIVE**）

- **判别式**：**"本击是否到达 native `mousedown` 且派发 `click`" ⇔ "软键盘是否顶起"**，本会话 **8/8 无反例**。该判据把**结果**与**原因**分开：它不解释 iOS 为何不合成，只把**随机性定位到 iOS 的鼠标事件合成决策**。
- **失败击**：F1 `seq174`（`touchmoveCount=1`、`clickSuppressed=true` ⇒ 目标 `BUTTON` 不走 `ownsTouchGesture` 豁免，落 **`index.html:4456` document 锁** = **A2 线**）；F2 `seq197` / F3 `seq211`（`touchmoveCount=0`、`defaultPrevented=false` ⇒ **页面侧无可归因 `preventDefault`**，击前 `ownership=HO-3`、`activeElementTag=INPUT` ⇒ **A12 线**）。
- **关键对照**：**F2（`seq197`）与 S3（`seq201`）同一 DIALOG、同一 `INPUT`、同一 `HO-3`、相隔 4 帧**，一个不弹一个弹——**页面侧输入面在两次击之间无可观测变化**。

| 候选 | 本轮新状态 | 一句话依据 |
| --- | --- | --- |
| **A12** | **正向支持**（页面侧唯一尚可承重者，仍不足以解释随机性） | F2/F3：击前已聚焦 `INPUT`、`role=web-input`、`insideVisibleViewport=true`、`touchmoveCount=0` |
| **A2** | **局部**（1/3 失败击相关） | 仅 F1 有 `touchmoveCount=1` 且目标在 `#terminal-container` 外 ⇒ `document` 锁 |
| **A1** | **局部**（0/3 命中，不可排除） | 3 次失败击的 `touchmove` 均不在 `#terminal-container` 内 ⇒ A1 无可归因面；维持归口 Phase 4、不判死 |
| **A13** | **削弱**（仍须保全） | 失败击前后 `HO-3` ⇒ 守卫下不抢焦；但轮询存在，52 条激活记录中 **2 次程序化 `focusin` 后键盘随即抬起** |
| **A11** | **削弱** | 失败击发生在 `web-input` 语境，非 xterm helper 可聚焦性 |
| **A8** | **无真值覆盖** | 步 4/5 无元素 id 锚点、`mousedown` 单槽，rAF `focus()+select()` 无法对齐 |
| **A7** | **维持判死** | `touchcancelCount` 497/497 = 0、`cancelled` 全帧 0 |

**候选新排序（现行排序；矩阵 v2 作历史排序保留，不改写）**：**`A12 ＞ A2（局部）＞ A1（局部）＞ A13 ＞ A11 ＞ A8 ＞ A7（判死）`**。

- **排序是排序，不是结论**（复用 §12.4 口径）：A12 排第一**只表示"页面侧最值得先排除/先验证的一项"**，**不表示**"改 A12 就能修好"——**已聚焦本身是 iOS 正常行为**（第 2 击在同一已聚焦状态下就能弹出），断点在**平台域**的鼠标事件合成。
- **随机性的重新定位**：矩阵 v2 隐含"多个页面缺陷并列、随机触发"的模型；本轮真值**不支持**该模型——**随机性在"iOS 对已聚焦 + 键盘自收后的这一击是否重合成鼠标事件"**，不在"页面多个缺陷轮流命中"。

#### 12.6.3 撤销措辞与状态边界（**防上浮**）

- **不作"B 已归因"声明。** 已闭环的**仅是"页面侧可归因面"**——**3 次失败击中 2 次与页面无关、1 次与 document 锁相关**；**剩余根因在 iOS 对"已聚焦 + 键盘自收"之后是否重合成鼠标事件（平台域）**。
- **B 的证据等级维持 INCONCLUSIVE**（归因排序**已数值化**，但根因未落到可修页面点）。
- **F5 / S2 的 PASS 登记不变**；**R2 / R3 / R4 维持不整行关闭**；不据此改判任何既有帧级 verdict；§12.1 的"维持 `2b64e7e`"、§12.4 的"矩阵权重与结论的分界"**继续有效**。
- **缺口清单（8 项，判读须知）**：tap 环上限 8 / `mousedown` 单槽 / 无 `activeElement`@`mousedown` 快照 / 无 `showModal` 位 / 无 `touches.length` / `vv.height` 679.5–565.5 语义未定义 / 步 4、5 无元素 id 锚点 / 多指自证缺。全文见 VALIDATION 同节。

#### 12.6.4 建议（**不签发，owner 决定**）

- **若 owner 要最终闭 root，唯一稳健路径仍是 Phase 4**（D1 own-viewport：键盘自收 / 再入路径按登记重设计）——因为断点位于平台合成域，页面侧候选已无剩余承重项。
- **替代路径 = 一轮扩展真值字段授权**（`touches.length` / `showModal` 位 / `vv.height` 语义标定 / `mousedown` 多槽）——**本仲裁不签发**，仅**建议**；是否派单由 **owner 决定**。

**本节结论**：`touchTruth` 真机帧给出了**可观测判据**（native `mousedown` + `click` ⇔ 键盘顶起，8/8 无反例）与**数值化排序**（`A12 ＞ A2 ＞ A1 ＞ A13 ＞ A11 ＞ A8 ＞ A7`）；**页面侧可归因面已闭环（2/3 失败击与页面无关、1/3 与 document 锁相关）**，**剩余根因在平台域**。**B 维持 INCONCLUSIVE**，不作"已归因"声明；本节**不签发**任何新授权、**不关闭** R2 / R3 / R4 或 #116。

## 13. Phase 4 首个窄切片：REFIT-CLASS 授权与 C1 交付（2026-11-07，issue #116）

**本节性质**：**纯追加**登记。**不重新打开、不修改、不放宽**上文任何既有行的授权语义、文件清单或口径；**已签署正文一字不改**。本节只登记 **Phase 4 第一条已授权窄切片**的开工与交付，**不代表 Phase 4 完成**，**不关闭**任何 G 缺口或 R1–R6。

### 13.1 授权流程（2026-11-07，本轮）

- **调度者起草 → 范围分析子代理只读增量分析**：结论为现状**已结构性满足** D4 纯 pan 不 refit（`index.html:4247-4250` 零写入）、**单一 ack 通道**（`index.html:9217-9226` / `:9355-9372` establishment 直发为**注释自证豁免**）、**D3 形态一成立**（`index.html:4191-4203` 全宿主共享 + **创建即应用** `pty.js:247-249`）；**唯一缺口 = D4② 激活路径 fit 后未投递**（`internal/ui/static/kinds/pty.js` activate）。
- **仲裁轮 1 = MODIFY**（11 处草案修正 P1–P11）。
  - **裁决 (a)**：**D4① 的含义是「不得新增由键盘 / 归属信号主动发起的 refit」**，**不是**豁免 GI-7 几何收敛；实现须「**不引入 refit 起源状态**」，**RO→fit→`queueTTYResize` 收敛链与 trailing、去重、拒绝不占槽、断开清理完整保留**。
  - **裁决 (b)**：**不新建** settle 状态机 / timer / 事件源 / 跨 pass 来源标记；「合并」由**既有** 100ms leading + 单 trailing + `dispatchTTYResize` 同尺寸去重 + 宿主 bottom write-only-on-change 承担，**`MIN_RESIZE_MS = 100` 原值不动**。
- **终稿 v2 → 同一仲裁复核 = SIGN**（非阻断附注：**行号引用以稳定标题为先**；**真正不可改锚点**为测试 `sliceResizeObserver` 精确字符串及其全文件唯一性；**无产品用途的焦点核对一律不写**）。
- **N1–N7 不签发面**：
  1. **Phase 4 任务 1**（D1 正向机制）与**任务 4**（真机）；
  2. **A1 / A2「无条件锁」收敛**与 **touchmove 硬锁**（`terminal_touch_scroll.test.mjs:143/151/190/198`、`ui_test.go:2119-2142`）；
  3. **D2 zoom guard** 任何语义；
  4. **`internal/app`** 与服务端最小尺寸 / ack 协议；
  5. **诊断 / 探针 / `evidence/*.mjs`**；
  6. **D3 形态二**；
  7. **`queueTTYResize` 窗口常量 / leading / 同尺寸去重 / 拒绝不占槽**。

### 13.2 C1 交付与双评审

- **提交 `a55bbec`**（原子：`internal/ui/static/kinds/pty.js` **+13** + 直接测试 `internal/ui/testdata/tty_resize_refit.test.mjs` **+34**）：activate fit **原位保留**，fit 后经**既有** `queueTTYResize` 投递**已测量尺寸**（**尺寸守卫**保隔离单测安全、**`typeof` 守卫**对齐 observer 投递形状；同尺寸被既有去重吸收；**无新 timer / 事件源 / 跨 pass 状态**）；新增 **1 条**结构断言用例钉住 activate 送达形状。评审 **MINOR**（注释把隔离保护归给 `typeof`）已微修并经调度者**独立确认**（16/16、68/68 复跑）后才提交。
- **双评审（并行、互不可见）**：
  - **评审 1 号** `opencode-go/muse-spark-1.3-contributor`（delta-diff 分工）= **APPROVE**（0 BLOCKER / 0 MAJOR / 1 MINOR：`s.term` nil 检查与 observer 首行 `if (!session.term) return` **不对称**，理论问题；核实 `terminal_status.test.mjs:610-628` fake-window activate 用例属实、`s.term` 恒存在）。
  - **评审 2 号** `minimax-cn/MiniMax-M3.1-Flash`（独立抽验 + 门禁复跑）= **APPROVE**（0B / 0M / 2 MINOR / 2 CHALLENGE）。
- **门禁复跑记录（三方独立，数字一致）**：`tty_resize_refit` **16/16**；七文件只读回归 **181/181**；`testdata` 全 **20 文件逐个全绿**；`go ./internal/ui` 与 `-tags viewportdebug` **仅存量红** `TestResponsiveLayerChangelogCounts`（CHANGELOG 34 vs viewport_height 40；评审 2 以只读 `git archive HEAD` 导出 pristine 树复跑证明**基线原样红**）；`go ./internal/app` **ok**；`pty-refit-check.mjs --self-test` **18/18**；`verify-live-choiceA.mjs --html` **PASS**。
- **变异承重（评审 2，全部 /tmp 副本）**：**9/9 变异使新用例唯一红**——删块 / 形参互换 / 接收者错 / 位置错 / 仅去 `typeof` / 全去守卫（`terminal_status` 14 红例）/ 尺寸守卫弱化 / 重复投递 / 早于 fit。

### 13.3 诚实边界

- 产物状态 = **离线绿 + 真机未验**；**不得**以任何离线结果宣称 GI-7 / D4 / GI-3 / R1 PASS（PHASE1-CONTRACT.md:204 / :242 / :246）。
- Phase 4 门禁「**同一 iPad 证明 R1**」（§5）**仍开放**。
- `seq 573→574` 三态规则**仅适用 applied 滞后类**；**持续 pan 为停止型 FAIL**。

### 13.4 登记的后续项（不阻塞本节交付）

1. **行为见证升级**（下一切片候选）：activate 投递断言从源码形状断言升级为**行为见证**（fake-window harness 已真实执行 activate，补 `term:{cols,rows}` / `fitAddon` / `queueTTYResize` 桩即可，顺带让 `typeof` 守卫在隔离环境**真正承重**；一并处置 `s.term` nil 不对称，**低优先、生产不可达**）。
2. **首次激活 socket 未 OPEN 的收敛缺口**（独立事项，归属待确认）：D4②「激活即收敛」**仅在激活时 WS 已 OPEN 成立**；connect 路径（`pty.js:208-215` loadLog→connectTTY）未触碰、端到端未证，是否符合既有 establishment 直发豁免语义**待仲裁 / owner 确认**。
3. **存量红独立处置**：`TestResponsiveLayerChangelogCounts`（CHANGELOG 计数 34 vs 40，基线原样红）需**独立小提交**，不与 Phase 4 行为提交混同。
