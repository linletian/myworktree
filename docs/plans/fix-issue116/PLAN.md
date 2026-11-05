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

**独立提交边界**：输入分类/交接及其直接测试为一组，不携带 PTY 几何算法重写。

**门禁**：R2、R3、R4 的 Web/iframe 部分通过；PTY 几何仍保持可回退基线。

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
| **G02** | D1 缺同条件 Web ↔ helper 对偶 pan 时序 / 归因；正向机制未签署，现状身份 / rect 与 pan 替代事实不足以选型 | **Phase 4 的 D1 机制实现前置阻断项**；未补签前不得开工 |
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
