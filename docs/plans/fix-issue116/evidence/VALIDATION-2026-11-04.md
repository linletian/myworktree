# Issue #116 冻结实现验证记录（2026-11-04）

## 范围与判据

- 验证对象：工作区中的冻结实现，产品基线 `5268869`，当前提交 `45b6d44`。
- 执行旧 PLAN 的自动化门禁与证据脚本；凡旧候选文案与最终行为不同，以 `docs/IPADOS-UI-COMPATIBILITY.md` 描述的冻结契约为准，不用旧阈值反判最终实现。
- 未修改 `internal/ui/static/index.html`、`internal/ui/static/kinds/pty.js`、诊断资产或诊断服务端语义；已核对上述文件与 `5268869` 无 diff。
- Headless Chrome 只证明桌面 Chromium 结构/几何，不冒充 iPadOS Safari 真机结果。

## 当前有效证据

| 日期 / 来源 | 用户真机口头观察与产品判定 | 证据等级 / 可支持结论 | 缺口与下一步 |
| --- | --- | --- | --- |
| 2026-11-04；产品 owner；iPad 真机口头反馈 | `scale=1` 软键盘避让“基本符合需求”；`scale>1` 不随软键盘自适应，用户判定符合预期；`scale<1` 同样不响应，用户判定“与需求预期不符” | **INCONCLUSIVE（口头真机旁证）**。可登记用户观察与产品期望，并作为启动 D2 正式评估的产品指令；观察与冻结 §5.6 的双向 zoom 退出条件一致，但不得写成冻结行为或 D2 验收 PASS。本行只保留“启动评估”当时状态；同日后续来源实测与确定目标指令见下行，D2 已由本轮仲裁另行签署 | 无截图、无 raw 会话、无缩放来源标注或连续 seq；来源与产品目标由下行直接产品证据补充，实现后仍须按 PLAN §4 D2 的 Z 系列会话验收 |
| 2026-11-04；产品 owner；iPad 真机操作观察（文字口径） | 一、双指手势缩小两种情况实测：<br>1. 当时 scale=1 时，双指不能缩小页面，直接缩小整个浏览器页面进入平铺标签页（Tab Overview 视图，不是 vv.scale<1）；<br>2. 当时 scale>1 时，双指可以缩小页面，但双手释放后必定回到 scale=1。<br>二、实测找到的实现 scale<1 的两种方法：<br>1. 妙控键盘/外接键盘 cmd+'-'（组合键）；<br>2. 浏览器菜单里的具体缩放指令。<br>三、产品指令："按照我这个需求（scale<1 场景终端软键盘避让）将它设为确定的目标"。 | **INCONCLUSIVE（产品 owner 真机操作观察、文字口径）**；作为 D2 来源判断的**直接产品证据**和确定目标的签署依据。可支持 D2 非对称目标签署，不可支持“实现已完成”或真机行为 PASS；Tab Overview 不得记为 `vv.scale<1`，释放回 1 也不得写成应用主动修改 scale | 无截图 / 无 raw 会话 / 无连续 seq；签署后仍须以独立连续 Z-PINCH / Z-MENU / Z-MAGIC 会话、手动 Capture 与同场截图验证已实现行为，A0 0.75 / 153 帧场景为必测锚点 |
| 2026-11-04；产品 owner；iPad 真机口头反馈 | 打开链接后 debug 面板存在但默认未展开，需手动展开 | **INCONCLUSIVE（口头真机旁证）**；登记为独立可改进 follow-up，不判定 R6 / 冻结行为失败，不产生 PASS | 无截图或 raw；需先确认“未展开”指 More 次要控件还是主面板状态，再另行明确默认态需求与验收；未获授权不改诊断工具 |
| 2026-11-05；c3ee 真机会话 + 双评审 + owner“此次数据为准，其余信息一致” | 483 帧探索性混合会话；`scale<1` 深键盘 31 帧让位几何与 owner 现场确认一致 | **D2 整行 INCONCLUSIVE；仅下列行为子项 PASS**。owner 指令允许把“scale<1 避让成立”登记为 D2 真机行的子项级记录，不升格为整行验收，不免除其余形态项 | 非验收包：混合来源、无 `zoomSource`、无截图、缺 `seq 355`、`seq 468` 为 reload 分界、无 settle 可见性；按 DATA-COLLECTION 的 SOP v3 补采 |
| 2026-11-05；42cd 真机混采单会话 + owner 截图目测口头判定 | 207 帧连续 `seq 1–207`、唯一 init；owner 口头登记本会话截图目测符合预期 | **D-C settle 与 D-B 纯变档可观察行为子项 PASS；D2 整行、F18、25% / 50%、D1、GI-3 均不上浮**。截图判定仅为 **INCONCLUSIVE（口头级视觉旁证）** | `zoomSource=menu/cmd` 不可分，B / C 段仅作形态记录；未向仲裁交付截图文件及逐 seq 绑定；深键盘重入不证明不新置锁或门线选择；详见下方 42cd 专节 |
| 2026-11-05 下午；产品 owner；iPadOS 27 真机操作观察（文字口径） | 浮动键盘拖到任意位置 `rawShortfall` 恒 0；Stage Manager 无键盘拖至最矮时为 1、复原为 0、拖动中偶现 1；本机没有可用的分裂 / 靠边键盘形态 | **INCONCLUSIVE（口头级真机观察）**。可支持控件形态台账、本机 25%–50% 判别带不可达及 r3 改道；不支持产品行为 PASS / FAIL，也不推广到其他设备 | 无原始会话、截图或连续 seq；详见下方“判别带不可达与浮动键盘登记” |

**D2 签署前交叉核对（只读检查，不是新增真机测试）**：A 原始会话共 273 帧，其中早段 153 帧全为 `scale=0.75` 且 `occlusion=0`、后段 120 帧全为 `scale=1`；C 原始会话 583 帧全为 `scale=1`。因此 A 与旧 zoom guard 一致，C 不含 zoom 帧反例；A 没有唯一缩放来源标注，不能证明其 0.75 来自何种操作。历史 `DATA-COLLECTION.md` 将“双指/双击缩放”和“75% 与 100%”列在同一组合操作行，缺少唯一 `zoomSource`，既不足以构成“双指从 1 持续缩小至 `vv.scale<1`”的直接反例，也不可用作来源证明。本次未发现与 owner 文字口径直接冲突的现有帧证据；该结论不替代实现后的 Z 系列真机验收。

## 2026-11-05 c3ee 探索性混合会话仲裁（D2 真机行，非验收包）

### 登记口径与原始事实

- 原始文件为 [c3ee73e2653ba89dc615d4554ceb7204.jsonl](raw/c3ee73e2653ba89dc615d4554ceb7204.jsonl)，本地重存档带 `seq` / `receivedAt`。共 **483 行 / 483 帧**，`seq 1–484` 中只缺 **`seq 355`**；`seq 1` 与 **`seq 468`** 均为 `init`，后者是同标签页 reload 的新运行时分界，不能把前后两段当作同一产品状态机连续链。评审报告中的行号 / seq 对应关系以评审报告为准；本节引用一律显式写 seq，不改写 raw。
- 本会话混合双指、缩放指令、PTY / Web 输入与多次键盘操作，没有唯一 `zoomSource`，没有同场截图，不能拆事后冒充 Z-PINCH / Z-MENU / Z-MAGIC 三条独立验收会话。**登记为“探索性混合会话（非验收包）”；D2 真机整行维持 INCONCLUSIVE，不得上浮为整行 PASS，也不得以“99.2%”之类覆盖率代替 verdict。**
- 仲裁只读复算与双评审一致的事实如下；这些事实支持指定的行为子项，不自动满足来源、时序、视觉或完整会话门禁。

| 事实 | 复核结果 | 证明边界 |
| --- | --- | --- |
| applier mirror 与 reported | `mirrorOcclusion ≡ occlusion` **483/483** | 证明本会话镜像传输一致，不证明派生标签正确 |
| 正让位与 raw | 正让位 **98/98** 帧 `occlusion=raw` | 不含 owed / latch / settle 的独立证明 |
| 三盒几何 | 同 98 帧均为宿主 `raw`、控制列 `20+raw`、Transport `raw`，**98/98** 自洽 | 只证明数值与 computed bottom，不替代截图和输入行 / 最后一行验收 |
| scale=1 深键盘 | A1 `seq 37–39`、A2 `seq 47–98`、A3 `seq 413–418`、A4 `seq 420–425`，共 **67 帧** `raw=occlusion=428`，四段符合冻结 §5 的已观测让位几何 | A3 是 HO-3 `INPUT / web-input`，不是 PTY 证明；见下方 D-A 偏离 |
| scale=0.75 深键盘 | `seq 132–135`、`140–150`、`167–174`、`440–443`、`448–451` 共 **31 帧** `raw=occlusion=570.34375`；五组对应五次键盘开 / 合，scale 恒 0.75 | 可证稳定观察帧的 scale<1 让位几何；不能反推两个 500ms tick、来源或门线选择 |
| scale>1 保守退出 | 带外 scale>1 共 **97 帧 occ=0**；`seq 319–324` 在 `scale=1.7473722696304321` 下 `raw=566.59375` 仍付 0，含手动 Capture `seq 323` | 是 opt-out 的硬证据；无唯一 zoomSource，仍只作行为子项 |
| 回程付 0 | 双评审登记的 4 个回程候选均付 0；只有 **`seq 386`** 有判别力：前态 `scale=1.0103167295455933`，超过 0.01 的余量约 `0.00031673`（仅阈值的约 3.17%），当帧 `raw=428 / occ=0` | 不据此认证 D-D 全项；边缘样本、旧锁、settle 后重入均须定向补证 |
| D-A / HO-3 现场实例 | A3 `seq 413–418` 为普通 Web `INPUT`，仍 `occ=428` | 登记为方案 B / 冻结通用 editable 谓词下的 **R2 已知偏离现场实例**；不判 D2 PTY PASS，不关闭 Phase 3，也不把该六帧改判为 HO-2 |
| 派生 zoom 标签 | 多个 scale<1 正让位帧同时报 `derivedState:none / basis=zoomed-session-exempt` 与正 mirror | 以 reported + computed bottom 为准；标签矛盾登记为工具限制，不反判产品 FAIL |
| 面板支持性 | `seq 253` 自报 `unsupported / visible-window-outside-layout-viewport`，但 panel client rect 在可视区内且 `insideVisibleViewport=true`；free 拖拽预算用 `vvWidth`，支撑判定另用 `layoutWidth`，而 `insideVisibleViewport` 只查纵向 | 属工具误判，不是产品不支持证据；修复授权见下 |

### 行为子项 PASS 表（只限已观测行为，不是整行验收）

| 子项 | verdict | 适用范围与保留项 |
| --- | --- | --- |
| B1：scale=1 深键盘三盒让位 / 恢复几何 | **PASS（子项级）** | 上述 A1–A4 共 67 帧；A3 只证明方案 B 下 Web 输入也会触发既有几何，不构成 HO-3 / R2 合规或 D2 PTY 准入证明 |
| B2：scale=0.75 的 PTY 软键盘让位与无键盘归零 | **PASS（子项级；owner 现场确认 + 31 帧）** | 只登记“scale<1 避让成立”的已观测几何行为；D-C settle、25% / 50%、D-B / D-D、截图和唯一来源仍是独立未闭合项 |
| B3：scale>1 全程保守 opt-out | **PASS（子项级）** | 97 帧 occ=0，含 raw=566.59375 硬证据；不认证双指来源前提或释放后的完整状态机 |
| B4：回程候选当帧付 0 | **PASS（子项级观察）** | 只限当帧 `occ=0`；不扩张为 settle、恢复锁或回程后重入 PASS |

**99.2% 把关结论：确认评审 1 的限制必须保留。** 本次没有截图，任何 PASS 只能是上述子项级、非整行登记；即使未来补了档外截图，也只能补足其明确对应的同场行为项，不能追溯把 c3ee 混合会话变成三条单源验收包或补齐 settle / latch 缺字段。

### INCONCLUSIVE 清单与补采责任

| 条目 | 本轮状态 / 原因 | r3 补采或前置条件 |
| --- | --- | --- |
| D2 整行 | **INCONCLUSIVE**；混合会话、无唯一来源、无截图、seq 缺口和 reload 分界 | 按 DATA-COLLECTION SOP v3 拆为独立 Z-PINCH / Z-MENU / Z-MAGIC，不 reuse c3ee 名义 |
| D-C settle | **结构性 INCONCLUSIVE**；schema 无 `scaleSettle*`，1000ms periodic 看不到产品 500ms tick；`unlockTrace.captureTick` 不是 settle tick | 先执行另立授权 `AUTH-20261105-D2-SETTLE-READONLY`，再在 r3 采连续两 tick 及重置 / 回程链 |
| 25% / 50% 判别 | **INCONCLUSIVE（本机当前形态不可达）**；428 与 570.34375 同越两线，owner 下午补测又排除浮动键盘与窗口拖矮作为中间带来源 | 不再要求本机取得 `249.25 < raw < 498.5`；保留 Node 门线逻辑回归，真机不得据此 PASS。未来设备若自然产生中间带可另行补采；见下方补测裁决 |
| D-B 纯变档退出 | **INCONCLUSIVE / 未触发**；30 个 scale 变档帧无一落在五组抖动点，也无变档时活动会话 | 从 scale=1、PTY 持焦、深键盘活动让位中，用该行唯一来源做一次粗糙变档到 0.75；逐步保存变档帧与两个 settle tick。深键盘重入仍不单独证明未置锁 / 25% 线 |
| D-D 恢复锁 / 回程重入 | **INCONCLUSIVE**；只有 seq386 一个边缘判别观察，缺旧锁和 settle 后 25% / 50% 分辨 | r3 分别保留纯 0.75→1 与 scale>1→1 的 settle 链；深键盘同越两线时恢复锁 / 门线选择仍 INCONCLUSIVE，不用重入结果反推 latch |
| F18 干净开→关→重开 | **INCONCLUSIVE**；混合操作不能证明焦点全程不离开终端 | 独立连续段全程 `xterm-helper-textarea` 前台持焦，无中间 Web 聚焦 / 点击解锁，保留全部事件帧 |
| D1 对比 | **INCONCLUSIVE**；缺“xterm helper 持焦 + 键盘开 + Safari pan”与普通 Web input 的同场对比帧 | r3-D1-GI3 固定 scale=1，分别采普通 input 与 helper 的 rect、pan 和事件顺序 |
| GI-3 / GI-7 helper 与 rows/cols | **INCONCLUSIVE**；`seq 254` 起 helper rect 出现 `top=839 / bottom=856` 的停滞观察，现有材料不能区分聚焦 rect、refit 滞后与真实出屏 | r3 专项以 `pty-refit-check.mjs` 的 rows / cols / xtermRows / ack 读数作定向清单；Headless 结果只证 GI-7 工具链，不替代真机 GI-3 |
| 视觉与其余形态项 | **INCONCLUSIVE**；无截图，不能以 98 帧三盒数值关闭输入行、最后一行、quick-keys 视觉可达性或整行 | 每会话 1–2 张同场截图并绑定 seq；缺哪项就保留哪项，不用 owner 指令一并豁免 |

补采清单同时固定为：①三个单源会话拆分；②逐会话 `zoomSource` 人工标注；③settle 计数 / tick 可见性；④截图同场链；⑤r3-Z-MENU-01 改采 settle 变档 / 回程序列，25% / 50% 中间 raw 在本机登记为不可达而非必得用例；⑥D-B 活动会话中变档用例；⑦F18 干净用例；⑧D1 helper pan 对比帧；⑨GI-3 rows/cols 定向复采。执行细节见 DATA-COLLECTION“命题二：2026-11-05 首轮混合会话后二轮单源采证”。

### 2026-11-05 下午 owner 补测：判别带不可达与浮动键盘登记（口头级）

**证据边界**：以下来源只有 owner 当次真机操作的文字口径，没有新 raw 会话、连续 seq 或截图，全部按 **INCONCLUSIVE（口头级真机观察）**登记。它足以裁决“本机原采集目标不可执行、改采可观测 settle 序列”，不足以把任何新行为写成 PASS / FAIL。

#### owner 实测文字口径

1. **浮动键盘**：无论把 floating keyboard 拖到屏幕何处，`rawShortfall` 恒为 **0**；浮动键盘不写入可视视口、不产生任何 VV 侵入。
2. **Stage Manager 无键盘窗口缩放**：把 Safari 窗口拖到本机允许的最矮高度（约屏幕一半，再不可矮）时 `rawShortfall` 变为 **1**；放大复原后回 **0**；拖动过程中偶现 **1**。仲裁按 resize 间隙刷新竞态 / 噪声级数值登记，不把它当作真实遮挡事实，也不据此判产品 FAIL 或制造门线样本。
3. **本机可达形态**：owner 报告该 iPadOS 27 设备没有可用的分裂 / 靠边键盘。停靠键盘的 raw 与布局高同比例变化；既有 c3ee 的 `scale=0.75` 深键盘为 `570.34375 / 997 ≈ 57.2%`，窗口缩放没有改变该比例。owner 提供的 Apple 指南版本范围（拆分键盘指南止于 iPadOS 16、移动屏幕键盘指南仍至 26）仅作外部背景线索，本轮未独立检索核验；本裁决不依赖该线索，也不据此断言其他设备 / 系统版本永远没有这些形态。

#### 仲裁结论

- **本机 25%–50% 判别带不可达**：浮动键盘只有 raw=0，停靠深键盘约 57.2% 同越两线，无键盘 Stage Manager 缩放只有 0/1；原 `249.25 < raw < 498.5` 目标不再作为本机 r3 必做操作。25% / 50% 契约门线及其 Node 逻辑回归**不删除、不改值**，但本机真机门线子项维持 **INCONCLUSIVE（当前形态不可达）**。
- **二选一裁决：选择 `AUTH-20261105-D2-SETTLE-READONLY`，不选择“25% / 50% 降为 Node-only 永远自洽”**。前者能直接补足 D-C、D-B 变档时序与回程的可观测性；后者不能产生真机证据，“永远自洽”也会掩盖测试可能失败及未覆盖场景。本机门线只作不可达登记，未来若其他设备 / 键盘形态自然产生中间带，可另开会话补真机判别，不在本机反复强行造数。
- **浮动键盘不让位符合契约**：raw=0 不能凭 `raw>1` 进入活动避让会话；若此前 active，`raw≤1` 按冻结规则退出。因此浮动键盘不触发终端避让、`occlusion=0` 是契约解释上的正确行为；键盘几何义务休眠 / 退出不改变 PTY 焦点所决定的 HO-2 归属。此项是产品语义登记，不是本次口头材料升格出的实现 PASS。

#### r3-Z-MENU-01 新判别判据框架（settle 序列可观测会话）

1. **前置**：从 `scale=1`、前台 PTY editable focus、深键盘活动让位 `occ=raw>1` 开始，保存变档前 Capture；随后只使用 Safari 菜单缩小并尝试精确 0.75。`cmd+'-' / cmd+0` 只属于 Z-MAGIC 来源，不得在 Z-MENU 混用。
2. **变档帧**：记录旧 / 新 scale、raw、focus、occ 及 settle 快照；纯 D-B 用例须仍 `raw>1 && focusEditable`，变档帧 `occ=0`、候选以新 scale / layout / VV width / raw 重建、`ticked=0`、pending。若 raw≤1 或失焦，改走冻结置锁路径，不计入纯变档样本。
3. **0.75 的两个产品 tick**：首条来源必须为 `scaleSettleTick`，候选 / 基线及 layout / VV width / raw 不变，`ticked=1`、pending、`occ=0`；第二条同样稳定，`ticked=2`、`scaleBelowOneSettled=true`、pending 解除，满足其余冻结条件时可在该 pass 重入 `occ=raw`。普通 pass 不得增加计数；中间重置、来源不明或 journal 断链按相应子项 INCONCLUSIVE，不得补猜。
4. **回 1**：菜单回程普通 pass `occ=0`，并将 `scaleSettleCandidate` 建立 / 重建为 concrete 新候选、开启 return pending（此前无候选时为 `0→concrete`；从已 settle 的 scale<1 候选回程时可由旧 concrete 值替换为新 concrete 值）；第一条产品 tick 后仍为 concrete candidate / `ticked=1` / pending / `occ=0`。第二条产品 tick 完成时，applier 在发布诊断快照前先清候选，故该 tick 的 journal / 帧内可观测完成形态是 **`scaleSettleCandidate concrete→0`、`scaleSettleTicked 1→0`、`scaleSettlePending true→false`、`zoomReturnFramePending=true→false`**；`scaleBelowOneSettled=false` 在 scale=1 是正常结果。须以回程新候选、此前第一条 tick、未变 baseline / layout / VV width / raw 和严格单调 pass 序号共同确认完成；**回程链不得只找 `ticked 1→2`，也不得因没有 1→2 就把这条合法完成链误判为 INCONCLUSIVE**。
5. **证明边界**：深键盘 raw 同时大于 25% / 50%，所以 settle 后重入只证明“settle 后重入”，**不能单独证明实际选择未锁 25% 线、恢复锁已清或 D-B 不新置锁**。契约可以用“未锁 25% 重入”描述预期路径，但真机 verdict 不得越界。字段齐全且行为违反上述 MUST 时相应子项 FAIL；字段 / tick 链 / seq 缺失、reload 或场景未触发则 INCONCLUSIVE。整行仍须满足既有连续会话与截图门禁。

### settle 与两处诊断缺陷的仲裁

1. **D-C 选择候选一：另立只读 schema 增补授权，不选择人工记“重启后第一 tick”，也不永久降级。** 原“不授权修改诊断层”的意图是防止 D2 把产品修复与 diagnostics 混成一轮、借工具试修产品或改判据制造 PASS；本例外只读取已经存在的状态，禁止写产品几何、改状态机 / 阈值 / timer、改 owed 或 verdict，故不破坏该意图。人工记录第一 tick 无法证明两个连续合格 tick、候选重置和 pending 优先，不能替代机器字段。若授权未落地，D-C 继续结构性 INCONCLUSIVE，不得以 31 帧稳定结果反推 settle PASS。
2. **zoom 派生标签：本轮选择不改。** reported mirror 已有 483/483 一致证据，现行契约也明确派生标签不是权威；把 zoom 分支改为先读 mirror 只能消除文案矛盾，不能增加 settle / 来源 / 门线证据，反而会让旧基线标签语义和既有测试发生无关漂移。保留“以 reported 为准 + 记录矛盾”的读法；未来如确需消歧，必须另案改标签与直接测试并双评审，不得夹进 settle 或面板修复。
3. **面板 vvWidth / layoutWidth 错配：批准另立修复，采用方案②。** 不用 layout viewport 的矩形界作为 `unsupported` 理由，支持性统一由 client 坐标的实际落点对 `[0,vv.width] × [0,vv.height]` 判定，并给快照 `insideVisibleViewport` 补横向 client 界。方案①把 free 拖拽和支撑判定都改随 layoutWidth，虽能消除一处不一致，却继续混用验收坐标并可能把实际屏外误判为 supported，故不取。修复后须以 seq253 同形态真机复采，不追溯改写本次 raw。

### 另立授权清单（本轮只登记，未改代码、未执行、未 commit）

| 编号 | 文件与改动范围 | 授权决定 / 评审路径 |
| --- | --- | --- |
| **AUTH-20261105-D2-SETTLE-READONLY** | `internal/ui/static/index.html`：仅允许在既有 applier mirror 边界发布 D2 settle 的只读诊断快照，不得改任何行为语句；`internal/ui/debugassets/viewport_debug.js`：追加读取与采集；`internal/app/viewport_debug.go`：追加 schema 传输 / 校验；直接测试限 `internal/ui/testdata/viewport_debug.test.mjs`、`internal/ui/viewport_debug_on_test.go`、`internal/app/viewport_debug_test.go`，既有锚点确需同步时才可窄改 `internal/ui/ui_test.go` | **批准，待调度者派编码。** 已选定新增只读 `window.__mwTerminalSettle`，保留数值型 `window.__mwTerminalOcclusion`；公开六个既有状态、pending / settled / return-pending、pass 来源 / 单调序号，并选固定 8 条只读 tick journal，只有 1s periodic 终态不合格。同步新 `schemaRev`；`probeVersion` 保持冻结行为 stamp `p9-full-shell-session-avoidance-v1`，旧 raw 不回填；不改 `evidence/*.mjs`、不新增 timer、不改门线 / latch / 验收判据。字段、测试及双评审路径详见下一小节；工具门禁通过不等于 D-C 真机 PASS |
| **AUTH-20261105-DIAG-PANEL-CLIENT** | `internal/ui/debugassets/viewport_debug.js` 的 `panelLayoutFor` / panel 自省，及 `internal/ui/testdata/viewport_debug.test.mjs` 直接测试；仅当既有资产锚点必须同步时窄改 `internal/ui/viewport_debug_on_test.go` | **批准方案②，待调度者派编码。** 删除 layout 界作为 unsupported 依据，保留 / 补齐 client 纵横界与 free 拖拽同场回归；不改产品 raw / occlusion，不改 evidence `.mjs`。路径：独立诊断原子变更 → Node 矩阵（含 seq253 同形态 fixture）→ 双评审 → 真机复采；原 `937d91f` 评审范围不能用来豁免本次回归复核 |
| **AUTH-20261105-R3-GI3-READONLY** | `internal/ui/debugassets/viewport_debug.js`：追加前台 PTY 的 `.xterm-rows` client rect、`term.rows/cols` 与 `appliedTTYSize/lastSentTTYResize/pendingTTYResize` 只读快照，不可得必须显式标记；`internal/app/viewport_debug.go`：追加传输 / 校验；直接测试限 `internal/ui/testdata/viewport_debug.test.mjs`、`internal/ui/viewport_debug_on_test.go`、`internal/app/viewport_debug_test.go` | **批准，待调度者独立派单。** 不采 instance id、终端正文或 token，不改产品状态、refit / ack、evidence `pty-refit-check.mjs` 或其 judge，不新增 zoom 判据。路径：独立诊断原子变更 → Node + viewportdebug Go → 双评审 → r3-D1-GI3 真机；Headless pty-refit 只证 GI-7 工具链，不关闭 GI-3 |
| **AUTH-20261105-DIAG-ZOOM-LABEL** | 无文件改动 | **本轮不授权、不派编码。** 保留派生标签及旧基线含义，验收继续以 reported + computed bottom 为准；若未来另提消歧，须独立授权和双评审，不得与上述两项捆绑 |

### AUTH-20261105-D2-SETTLE-READONLY 可执行边界细化（2026-11-05 下午）

本小节只是把已批准授权细化到可派单程度；**本轮未改代码、未运行实现测试、未 commit**。调度者仍须独立派单，编码后双评审路径不变。

1. **窗口暴露形态：选新增 `window.__mwTerminalSettle`，不改 `window.__mwTerminalOcclusion` 的类型或值。** 后者是现有数值型 occlusion mirror，不能把字段“加”到 Number 上而不破坏既有读者。新对象只由 applier 在既有 mirror 边界发布为新的不可变快照：字段不可写，无 setter、方法或指向内部可变状态的引用；探针只复制读数，产品任何决策路径都不得读取它。
2. **当前快照的 additive 字段白名单**如下；除了下列字段和 tick journal，不得借本授权公开 latch、宿主身份、正文或其他状态：

   | 字段 | 只读语义 / 形状 |
   | --- | --- |
   | `scaleSettleCandidate` | 当前候选 scale；0 = 无候选 |
   | `scaleSettleBaseline` | 当前候选的漂移基线；不得由探针重算 |
   | `scaleSettleTicked` | 整数 0…2；仅既有产品 tick 可递增，2 饱和 |
   | `scaleSettleLayoutHeight` / `scaleSettleVVWidth` / `scaleSettleRaw` | 候选建立时对应的三项几何读数；raw 只镜像既有算式，不新增 offsetTop 或第二 raw |
   | `scaleSettlePending` | 镜像现有同名 pending 结果，不把 `ticked<2` 的探针猜测当产品状态 |
   | `scaleBelowOneSettled` | 镜像当前 pass 的同名布尔结果；scale=1 回程完成时为 false 属正常 |
   | `zoomReturnFramePending` | 仅诊断投影：`!zoomed && scaleSettleCandidate!==0 && scaleSettleTicked<SCALE_SETTLE_TICKS`；不得新建产品状态或回写决策 |
   | `scaleSettlePassSource` | 固定枚举：只有收到既有 token 时为 `scaleSettleTick`，其余发布为 `other`；不得把 `other` 猜成某一种事件 |
   | `scaleSettlePassSeq` | 每页运行时的正安全整数，随 applier 发布严格单调递增；只表示发布顺序，不是墙钟时间，也不是探针 `captureTick` |

3. **选择有界只读 tick journal，不选择 applier 主动触发探针 Capture。** `window.__mwTerminalSettle.scaleSettleTickJournal` 固定最多 8 条，只记录来源为既有 `scaleSettleTick` 的发布后快照；每条携带上述标量字段，不嵌套 journal。这样普通 resize / scroll 不会挤掉 tick 链，也不新增 timer、事件源或产品→探针调用。探针在后续 Capture / periodic 中原样复制 journal，读者按 `scaleSettlePassSeq` 去重并按序号排序；普通 pass 造成的序号间隔不等于丢 tick。500ms 只是现有 interval 的名义节奏，本授权不加时钟、不宣称精确墙钟间隔，hidden 仍不得累计。
   - **普通 scale<1 候选完成链**：对初次进入或变档后的带外候选，须取得连续产品 tick 的 `ticked 0→1→2`（journal 至少可证 1→2）、pending 解除及相应稳定字段；只有 1s periodic 终态而没有 tick 链不够。
   - **回程完成链是另一种合法形态，不与上一条互相套用**：回程普通 pass 先建立 / 重建 concrete 新候选（无旧候选时 `scaleSettleCandidate 0→concrete`；已有 scale<1 候选时可由旧 concrete 值换成新 concrete 值）；第一条产品 tick 为 concrete candidate / `ticked=1` / pending；第二条产品 tick 在发布前清候选，因此 journal / 帧内必须按 **concrete candidate→0、`ticked 1→0`、pending true→false、`zoomReturnFramePending` 同帧归 false** 判读，并结合未变 baseline / layout / VV width / raw 与严格单调 pass 序号确认。**不得只找 1→2；合法回程没有 1→2 本身不是 INCONCLUSIVE 理由。** 不能把缺少前述回程上下文的其他清候选 / 重置帧冒充完成。
   - **统一 INCONCLUSIVE 清单**：序号回退、同号内容冲突、目标 tick 被覆盖、字段缺失 / 不可读、来源或 tick 链断裂，以及对所声明场景既未取得普通候选 1→2 链、也未取得上述完整回程完成链，均使相应 settle 子项 INCONCLUSIVE，不插值补齐。另将**跨字段矛盾帧**列入同一清单，例如 `scaleSettlePending !== (scaleSettleCandidate !== 0 && scaleSettleTicked < 2)`（包括 pending=true 但 candidate=0 或 ticked=2，以及 candidate≠0、ticked<2 却 pending=false），或 `zoomReturnFramePending` 与同帧 `zoomed / candidate / ticked` 的已授权投影式矛盾。矛盾帧保留上传与原始记录，只把相关 settle 证据判为 INCONCLUSIVE；不得让它通过 settle 验收，也不因此丢弃或自动改判整帧的其他独立证据。
4. **探针与服务端同批 additive**：在 `keyboardSession.scaleSettle` 增加**可空对象**，字段名与上述窗口快照一致，并含 `scaleSettleTickJournal`。mirror 未发布 / 不可读时为 `null`，不得伪造成全零对象；旧 schema 根本没有该字段时也不得回填。`internal/app/viewport_debug.go` 必须为同一对象、journal 及每个字段增加严格传输 / 类型白名单，接受新旧声明的 `schemaRev` 并按各自字段集校验；客户端和服务端字段必须同批落地，避免 `DisallowUnknownFields` 把整帧 400。同步新 `schemaRev`；`probeVersion` 保持冻结行为 stamp `p9-full-shell-session-avoidance-v1`，不改 `evidence/*.mjs`。
5. **隐私与 verdict 边界**：只允许有限几何 Number、布尔、固定来源枚举和安全整数序号；不得出现 DOM / 文本、token、worktree / 实例标识、路径、任意错误正文或时间戳。所有字段只读、只作证据，绝不回灌产品 raw / occlusion / settle / latch，也不由服务端或探针重算 owed / verdict。`scaleBelowOneSettled=true` 单独不是 PASS；深键盘重入也不是 25% / 50% 或锁状态判别。
6. **直接测试与评审**：至少覆盖①新对象字段 / 类型 / 不可变性及旧数值 occlusion mirror 不变；②变档 0→首 tick 1→次 tick 2、普通 pass 不计数、重置、饱和、scale>1 清候选、回 1 第二 tick 清候选的可读序列；③journal 固定容量、顺序、序号、只含产品 tick，以及 mirror 缺失时 probe 为 `null`；④新旧 schema 兼容、服务端往返、非法类型 / 未知字段拒绝和隐私字段审计。最小命令为 `node --test internal/ui/testdata/viewport_debug.test.mjs` 与 `go test -tags viewportdebug ./internal/ui ./internal/app`；若未改既有 Go 锚点不得顺手扩改 `ui_test.go`。路径固定为：独立诊断原子变更 → 上述门禁 → 双评审 → r3 真机双评审。工具 PASS 不等于 D-C 真机 PASS，未实施前 D-C 仍结构性 INCONCLUSIVE。

### AUTH-20261105-D2-SETTLE-READONLY 实施双评审复回收口仲裁（2026-11-05）

本节登记评审 1 **APPROVE / 3 项观察**与评审 2 **条件 APPROVE**后的技术裁决。F-2、F-3、F-4 是原条件的 r3 前收口项；本仲裁另把会扩大证据损失半径的 F-6 纳入同一授权、同一编码 diff 与同一轮双评审。本节 F 编号均指本次 AUTH 实施复评，不指 PLAN 台账中的 E 系列同名历史编号。**本节不是对未执行改动的 APPROVE，也不是 r3 / 真机 PASS。**

1. **F-2：采用“普通候选链 / 回程完成链”二态判读，禁止双重扣死。** 已按 applier 在第二 tick 同一 pass 先清候选、后发布的事实，改写上方第 3 条与 r3-Z-MENU-01：普通 scale<1 候选仍须 1→2；回程则以新候选建立、首 tick pending、次 tick `candidate concrete→0 / ticked 1→0 / pending true→false / zoomReturnFramePending=false` 及稳定字段、单调序号闭链。缺少 1→2 不再能单独否定合法回程；没有回程上下文的清零也不能冒充完成。只改判读文档，不改产品清候选顺序、发布位置或 evidence `.mjs`。
2. **F-3：全文件设计红线测试为 r3 前必须项。** 现有 applier 切片的位置 / 发布锚点保留，但 `window.__mwTerminalSettle` 的“产品任何决策路径不得读取”必须改为对 `fetchIndexHTML(t)` 的**全文件**调用现成 `stripJSComments` 后计数：可执行代码中该完全限定名恰好 2 次，即发布块内一次自续读、一次写入；不得再用 applier 切片或自写正则代替这一全文件红线。探针文件的只读复制检查仍分开，不计入这 2 次。
3. **F-4：选择读者规则防线，不新增服务端跨字段 400。** 上方统一 INCONCLUSIVE 清单已纳入 pending / candidate / ticked 及 return-pending 投影矛盾帧；服务端继续现有类型、范围、枚举、journal 形状校验，但不得为本项给 `internal/app/viewport_debug.go` 增加跨字段不变量 400。理由是诊断矛盾最需要保留原帧定位时，整帧 400 会连同 ownership、宿主几何、reported occlusion 等独立证据一起丢失，爆炸半径大于收益；雪湖／产品不变量由产品状态机及直接行为测试保证，不应依赖诊断上传端拒绝来维持。保留矛盾帧 + 读者禁止 settle PASS 已满足“至少一道”防线，同时不预先断言矛盾来自产品还是诊断通道，也不误伤其他子项。
4. **F-6：本轮必改，且只前置对齐服务端既有结构校验。** 当前探针会复制非单调 journal，而服务端随后整帧 400；这会在 r3 直接丢失同帧其他证据，不能留到真机后再处理。授权在同次 diff 内窄改 `internal/ui/debugassets/viewport_debug.js` 与 `internal/ui/testdata/viewport_debug.test.mjs`：复制后、上传前检查 journal 只含 `scaleSettleTick`、`scaleSettlePassSeq` 严格递增，并补齐有限非负几何等与服务端现有形状规则的前置一致性；任一不满足即将整个 `keyboardSession.scaleSettle` 降级为 `null + scaleSettleUnavailableReason="settle-mirror-not-published"`。不得排序、去重、修补或部分保留坏 journal，不得新增 reason 词表；同帧其余字段照常上传。服务端的相同校验保留给其他客户端作防线，本项不改服务端，也**不得**把 F-4 的跨字段语义矛盾一并前置过滤。
5. **其余 P3 观察项的授权粒度**：F-5、F-7、F-8、F-9、F-10 留 follow-up 台账，不在本次编码授权内；它们未被本次仲裁升格为 r3 阻断项。后续若拿出会整帧丢证据、破坏只读红线或使 r3 无法判读的具体复现，须另次仲裁明确文件 / 测试边界后签证，编码者不得借 F-6 顺带扩大。

| 收口编号 | 优先级 / 文件边界 | 同次双评审核验点 |
| --- | --- | --- |
| F-2 | **r3 前必须；文档已完成**：本 VALIDATION | 普通 1→2 与回程 1→0 分链；回程无 1→2 不单独判 INCONCLUSIVE，其他清零也不冒充回程完成 |
| F-3 | **r3 前必须；编码待做**：仅 `internal/ui/viewport_debug_on_test.go`，复用 `internal/ui/ui_test.go` 的现有 helper，不改 helper / 产品 | 全文件剥注释后完全限定名计数 ==2；保留 applier 发布位置、单次发布及探针只读锚点 |
| F-4 | **r3 前必须；文档已完成**：本 VALIDATION；明确排除服务端跨字段 400 | pending / candidate / ticked 与 return-pending 矛盾帧可留存，但相关 settle 子项不得 PASS，其他独立证据不被整帧连坐 |
| F-6 | **r3 前必须；同次编码**：仅 `internal/ui/debugassets/viewport_debug.js`、`internal/ui/testdata/viewport_debug.test.mjs` | 重复 / 回退序号、journal 内 `other`、有限但为负的几何等 fixtures 均降级 null + 固定 reason；同帧其他字段保留；合法 journal 原样复制 |
| F-5 / F-7 / F-8 / F-9 / F-10 | **P3 follow-up；本轮零代码授权** | 只登记，不夹带；有新的 r3 阻断事实时另签 |

F-3 / F-6 编码完成后，双评审均须独立核对实际 diff，并运行 `node --test internal/ui/testdata/viewport_debug.test.mjs` 与 `go test -tags viewportdebug ./internal/ui ./internal/app`；另以源码搜索确认没有给服务端新增 F-4 跨字段 400、没有改 evidence `.mjs`。两项代码收口及双评审复回前不得启动 r3 的 settle 验收判读；测试 PASS 仍只证明离线诊断通道，不是 D-C 真机 PASS。

## D2 Phase 2 实施双评审终审登记（非真机验收）

- 本节只登记对 `32fab22` 签署口径的实施裁决，不把当前未提交实现或 Node / Go 结果记为真机 PASS。D-A 选过渡方案 B：Phase 3 前沿用冻结通用 editable 谓词，scale<1 下 Web 输入导致的终端避让列为 **R2 已知偏离 / Phase 3 依赖**；PTY 宿主帧与 Web 输入帧分列，后者不用于 D2 verdict。本项不新增产品归属判定，故无需 owner 追加拍板。
- D-B 的免置新锁在复评中收窄为“纯 scale<1 带外变档退出”（当帧仍 `raw>1 && focusEditable`），settle 后按未锁 25% 重判；同帧 raw<=1 / 失焦及 scale>1 仍按冻结规则置 / 保留恢复锁，settle 不自动清锁。这是 PLAN / PHASE1-CONTRACT 记录的对冻结 §5.6 源码置锁顺序的窄例外，不回写冻结文档。D-D 要求 0.75→1 / scale>1→1 回程帧付 0 并重新 settle；其中 scale>1→1 不抹除在先 scale>1 退出形成的旧锁，未被独立解锁时 settle 后按 50%，D2-6 的保守结果据此成立。
- D-C 的 settle 定义固定为连续两次既有 500ms tick 合格：相邻 scale 与相对首个候选 tick / 当前已 settle 基线的差均 `<=0.001`，候选期间 layoutHeight / vv.width / raw 不变；基线只在重建时更新，累计超 ε 的慢速漂移同样重置，已 settled 的累计亚 ε 漂移保持。普通事件 pass 不计数，计数达 2 后饱和，已 settled 后单纯键盘 raw 开合不重置 scale settle。scale>1 域不得开启 / 累计候选，进入该域须清空旧候选。不新增 timer 或墙钟 / 单调时钟，hidden 不累计。ε 边界按 JavaScript binary64 的实际差判定，十进制“恰好 0.001”不保证等值，1 ULP 用例须按位构造。完整重置、回程和测试口径见 PLAN §4 D2 与 PHASE1-CONTRACT 术语章 / F13-F14。
- **参数证据边界**：对 `raw/final116-20261104-a.jsonl` 只读复算，按文件顺序的早段 153 帧 scale 唯一值为精确 `0.75`，相邻 scale 差唯一值为 `0`；其中 raw 范围为 `0–571.34375`，100 帧 `raw>1`。它既没有变档过程，也没有 1 ULP 或亚 ε 抖动样本，不能据此估计噪声上限，也不能把这 153 帧概括成“全部 raw=0 / 无键盘”。因此 N=2 / ε=0.001 是保守暂定实现参数，不是实测结论；Z-MENU / Z-MAGIC 必须记录实际 scale、raw 和 tick 节奏复核，未达到精确 0.75 时该锚点 INCONCLUSIVE，其余实际 scale<1 场景按实记录。
- 诊断层继续只读：`viewport_debug.js:675–680` 先对 zoom 帧返回 `derivedState:none / zoomed-session-exempt`，再检查 mirror `occlusion>1`，所以 D2 下同帧标签与正 occlusion 可并存。验收以 reported `occlusion` + computed bottom 为准并记录该矛盾；本句“不授权修改诊断资产、探针或重放器”是该次终审的历史边界，2026-11-05 仅由上方明确编号的独立只读 / 面板授权例外后续接替，不追溯扩大原编码包。
- 当前实现仍须按上述 D-B / D-C / D-D 及 PLAN 所列测试锚点修订后再评审；本节不是 APPROVE，不替代 Z-PINCH / Z-MENU / Z-MAGIC，也不关闭 R2 的 Phase 3 门禁。

## D2 实施修订版复评登记（非真机验收）

- **复评结论：NOT-APPROVE，N1 为阻断项。** 核对对象是 `6e8a006` 之后未提交的 D2 工作区（`internal/ui/static/index.html`、两个直接 JS 测试、两个 Go 包装 / 锚点测试及新增 `zoom_scale.test.mjs`）；本轮仲裁只改 PLAN / PHASE1-CONTRACT / 本记录，不改产品或测试，不 commit。N2 / N4 / N6 与注释、边界用例按 PLAN 的编码待修项执行；所有 Z 系列真机项仍未验证。
- **N1 实测（离线，不冒充 live / 真机）**：`node docs/plans/fix-issue116/evidence/verify-live-choiceA.mjs --html internal/ui/static/index.html` 对当前工作区 **exit 1**，首个异常为 `ReferenceError: previousTerminalScale is not defined`；同一工具对 `6e8a006` 的 `index.html` **exit 0**。原因是工具只切 `applyVisualViewportOcclusion`，工厂镜像未声明 D2 的九项函数外绑定。终审判定采用最小扰动的镜像例外：编码只准在该工厂闭包补 `previousTerminalScale`、两个 `SCALE_SETTLE_*` 常量和六个 `scaleSettle*` 变量，不得改其他任何工具语句；未来切片外部依赖按 PLAN 的“自包含优先 / 全镜像同步”规则处理。
- **N1 授权边界与新增实测**：用 `/tmp` 临时副本仅补上述九绑定后，当前源码不再抛该 ReferenceError，但仍在旧断言“back at scale 1 … a NEW session opens”处 **exit 1**（实际 `0px`，旧期望 `428px`；对应原工具 388–394 行）。这是旧 v3.2 即时回程断言与已签署 D-D 两 tick 语义的后续冲突；本仲裁**没有授权改该断言、回放或判定口径**，所以九绑定修复不得被登记为该门禁 PASS，须由调度者另行取得证据工具回放修订授权后处理。
- **针对性 Node 实况**：`node --test internal/ui/testdata/zoom_scale.test.mjs internal/ui/testdata/viewport_height.test.mjs` 为 **60/60 PASS**。它只证明当前切片 harness 的已覆盖断言，不抵消 evidence 门禁失败，不证明 N2 / N4 / N6 / N7 等缺失用例，也不构成 iPadOS Safari 真机 PASS。
- **N2 / N3 / N4 / N6 / 带内优先级**：容器早退须同步 scale 并清 D2 候选，禁止恢复首帧伪回程；细则 2 的 scale>1 冻结置锁优先，F14 不再写“一律清恢复状态”；纯变档免置锁收窄到 `raw>1 && focusEditable`，其他退出原因保留锁；scale>1 域禁止候选开启 / 累计；回程及 pending 期间 D-C 两 tick 优先于普通带内准入，普通带内 jitter 无候选时仍全归冻结机。完整可执行文字以 PLAN §4 D2 与 PHASE1-CONTRACT 细则 2–6 / F13-F14 为准。
- **源码锚点规则**：PLAN 对未提交 D2 实现不再使用易漂移的 `index.html:NNNN`，一律按 `scaleStepNoLatchExit` / `zoomReturnFrame` / `scaleSettlePending` / `SCALE_SETTLE_TICKS` 等符号引用；历史提交行号只在明确绑定该提交的基线事实中保留。

## D2 回程旧断言处置仲裁（非真机验收）

- **定性与授权链**：旧 v3.2 即时回程断言由 `4def4d6` 写入、由冻结基线 `5268869` 原样继承；D2 目标后来由 `32fab22` 签署，D-C 两 tick / D-D 回程付 0 由 `6e8a006` 仲裁，本轮又取得 owner 对该断言处置的显式授权。故当前 D2 工作区在 0.75→1 回程普通 pass 付 0 是已签署语义，旧断言的 exit 1 属于历史基线期望过时，不是 D2 实现违约。上方复评节的 NOT-APPROVE、N1 失败及其会话描述均作为当时历史原样保留，本节是后续派生裁决，不覆写它们。
- **最小处置与正确期望**：只改 `verify-live-choiceA.mjs` 的 0.75→1 回程断言块：回程普通 pass 为 hosts `["0px", "0px"]`；第一次 `avoid("scaleSettleTick")` 后仍为 `["0px", "0px"]`；第二次既有 tick 后为 `["428px", "428px"]`，控制列 / Transport 分别为 `448px` / `428px`，即“重新打开新会话”仍成立，但从当帧立即改为第二个 settle tick。除该块两次 tick 推进外，未改工具其他内容。该场景 raw 428 同时大于 702 的 25%（175.5）与 50%（351），所以本门禁不单独区分恢复锁是否清除；纯 0.75→1 的未锁 25% 与 scale>1→1 的保留旧锁 50% 仍由 PLAN / PHASE1-CONTRACT 的 D-D 及各自直接用例判定。
- **run-the-gate 实况**：本仲裁亲自执行 `node docs/plans/fix-issue116/evidence/verify-live-choiceA.mjs --html internal/ui/static/index.html`，处置后在同一未提交 D2 工作区 **exit 0**，末尾输出 `PASS — verified against the repo bytes`；处置前同一命令在旧回程断言 **exit 1**，actual 为 `["0px", "0px"]`、expected 为 `["428px", "428px"]`。这是离线切片门禁结果，不是 live 服务或 iPadOS Safari 真机 PASS，也不把此前整体复评自动改判为 APPROVE。

## Phase 2 任务 5 工具契约化终审（非真机验收）

- **签署范围与完成口径**：本轮可签署的是两工具的窄结论——“保留现有 PTY refit / sidebar 几何工具；各自判据已经等于其应承担的现行契约，没有需要写入这两工具的 D2 zoom / scale 条款；旧 p9 结果只作历史对照”。这只是任务 5 的判据复核与契约对齐口径，**不据此把 Phase 2 任务 5 整体、Phase 2、D2 或 #116 标为正式完成**；Z-PINCH / Z-MENU / Z-MAGIC 真机矩阵仍未执行。
- **判据属域（C1 / C4）**：`pty-refit-check.mjs` 的判据属于冻结 §7 的 refit / ack 链，即 PHASE1-CONTRACT GI-7 所重述的最终尺寸一致、旧 ack 不回刷等义务；本工具的 headless 检查不单独全证 GI-7 的去重、断开清理或真机最终一致性。`sidebar-title-check.mjs` 的标题行 flex 几何在 PLAN / PHASE1-CONTRACT / 冻结文档中没有 GI 落点，GI-4 只规范 quick-keys 入口 / 面板 / 控制列，不能写成该行的“GI-4 neighbourhood”。终审采用最小处置：该行判据明确登记为**工具本地规则 + shipped CSS**，其 PASS 只是该针对性回归门禁结果，不是契约 GI 验收；本轮不立 GI-10，不扩契约覆盖面。
- **D2 判据承担者**：两工具的 `judge()` 都不读取 scale / occlusion 判定输入，新增 zoom fixture 只会形成不参与判定的空字段。D2 zoom 判据由 `internal/ui/testdata/zoom_scale.test.mjs` 的直接行为用例、`verify-live-choiceA.mjs` 的离线回放，以及 PLAN §4 D2 的 Z-PINCH / Z-MENU / Z-MAGIC 独立连续真机会话共同承担；前两者不能替代真机 verdict。
- **判据零改动审计**：双评审一致的 AST 级审计结论为，判据相关的输入读取、分支 / 阈值、verdict 聚合、fixture 与既有预期均未改变；本仲裁又逐块核对了未提交 diff。PTY 的一处 stale-ack `info` 文案变化不参与 verdict。fixture 总数保持 PTY **18**、sidebar **12（10 cases + 2 tolerance edges）**。JSON 侧相对 HEAD 仅新增产物标记字段，未删除或改变已有字段；仓库内文本检索未发现这些离线 RESULTS 的标记字段消费者。
- **产物标记读法（C3，仅本项约定）**：最终字段名为 `artifactRev`（编码将本轮尚未提交的 `schemaRev` 改名）与 `contractRev`。`artifactRev` 只标记“产物由哪一版契约对齐的工具修订产生”，**不代表 judge 输入 schema 或判据 / verdict 版本**；不得再写“no schemaRev bump needed”这类把两类版本混为一谈的句子。这句话只登记本轮两份离线证据产物的读法，**不立仓库级通用字段规范**，也不改诊断帧既有 `schemaRev` 语义。
- **fixture 登记纪律（C2，采用 VALIDATION 单点登记方案）**：只新增 fixture、且不新增 judge 输入、不改 verdict 规则或既有预期时，不需重开判据仲裁，但必须在同一变更中更新本节 / 相应 VALIDATION 登记；工具注释不写死数量，只指向 VALIDATION。新增 judge 输入、改变 verdict 规则或既有预期即属判据变更，必须走判据变更路径，不能借“补 fixture”绕过。
- **D-B 注释事实核查（P4）**：当前 `index.html` 的 `scaleStepNoLatchExit` 明确带 `raw > 1 && focusEditable` 限定；PLAN §4 D-B 与 PHASE1-CONTRACT 术语章细则 2 也只对“纯 scale<1 带外变档退出”免置新锁。两工具注释中“out-of-band step frames … book no new latch”必须补上该条件；同帧 `raw<=1` 或失焦仍按冻结规则置 / 保留恢复锁，不能写成无条件免置锁。
- **p8 / p9 读法**：`p9-portrait-refit-import-v1` 只作历史 provenance，不是现行验收契约；fixture 中 p8 / p9 名称只标记历史缺陷或形状，历史 RESULTS 只作历史结构 / 失败证明与对照，不是当前判据输入。sidebar 默认 label 的 p9 改名（P6）本轮不做，已在 PLAN §10 登记 follow-up，不得借该遗留名称反推现行契约版本。
- **签署时点离线基线（本仲裁亲自只读实跑）**：在 HEAD `b511197` 加两工具未提交改动的工作区，①`node docs/plans/fix-issue116/evidence/pty-refit-check.mjs --self-test` **exit 0，18/18**；②`node docs/plans/fix-issue116/evidence/sidebar-title-check.mjs --self-test` **exit 0，12/12**；③`node --test internal/ui/testdata/zoom_scale.test.mjs` **exit 0，37/37**；④`node docs/plans/fix-issue116/evidence/verify-live-choiceA.mjs --html internal/ui/static/index.html` **exit 0，PASS — verified against the repo bytes**。这些命令均未启动浏览器或写入证据产物；结果只证明离线 fixture / 切片与回放门禁，不是实时 PTY / sidebar 浏览器检查，更不是 iPadOS Safari 或 Z 系列真机 PASS。

## 已通过的自动化门禁（历史冻结基线；D2 工作区复评见上节）

| 验证 | 结果 |
| --- | --- |
| `go test ./internal/ui/ ./internal/app/` | PASS |
| `go test -tags viewportdebug ./internal/ui/ ./internal/app/` | PASS |
| `go generate ./internal/portal` 前后比较 `csp_gen.go` | PASS，生成物可复现，无 diff |
| `gofmt -l .` | PASS，无输出 |
| `go test ./internal/portal/` | PASS |
| `go test ./...` | PASS，全部包通过 |
| `node --test 'internal/ui/testdata/*.test.mjs' 'internal/portal/testdata/*.test.mjs'` | PASS，365/365 |
| `verify-sop.mjs --self-test` | PASS，54 个 fixture 均按预期判定 |
| `structural-check.mjs --self-test` | PASS，71 个 fixture/guard 均按预期判定 |
| `pty-refit-check.mjs --self-test` | PASS，18 个判定均按预期 |
| `sidebar-title-check.mjs --self-test` | PASS，12 个判定均按预期 |
| `verify-live-choiceA.mjs --html internal/ui/static/index.html` | **历史 PASS / D2 基线过时→已按签署语义更新 / 本轮回放 exit 0**：`6e8a006` 基线离线回放为历史 exit 0；当前未提交 D2 工作区处置前在旧回程断言 exit 1，本轮按 `32fab22` / `6e8a006` 与 owner 另行授权仅更新该断言块后，上述 run-the-gate 命令实测 exit 0。历史失败描述仍在复评节保留；本行只登记离线门禁，不认证 live / 真机行为 |
| `MW_LIVE_URL=http://127.0.0.1:38235/ verify-live-choiceA.mjs` | **历史 PASS**：仅指当时现有 issue116 冻结服务实际提供的源码；本轮未复跑，不认证当前 D2 工作区或真机行为 |

## 本轮未完成：真实浏览器运行检查（INCONCLUSIVE，不是产品 FAIL）

目标是复用现有 `127.0.0.1:38235` issue116 服务，用 `structural-check.mjs` 检查真实页面结构，再顺序执行侧栏几何与 PTY refit 检查。

实际结果：

1. 第一次 `structural-check.mjs` 运行超过 120 秒，无任何 verdict 输出；对同一页面的最小只读 `Runtime.evaluate` 也在 5 秒后超时。
2. 带 `Page.javascriptDialogOpening` / `Runtime.exceptionThrown` 监听和逐步超时的启动诊断中，`Page.navigate` 在 5 秒后超时；未观察到 JS dialog 或 exception。
3. 移除 HTTP/HTTPS/SOCKS 代理环境变量后，独立重试仍超过 120 秒且无 verdict。
4. 两次后台任务均已终止，相关 Chrome 进程和 9337/9347/9351 验证端口已释放；没有停止或替换现有 `:38235` 服务。
5. 因实时页面启动/CDP 未完成，本轮未继续运行真实侧栏几何和 PTY refit 浏览器检查，也没有生成 `RESULTS-frozen-final-*.json`。

结论：现有证据不能判定这是产品行为失败，也不能判定实时浏览器检查通过。需要先把“页面启动/CDP 无响应”作为独立验证环境或工具问题归因，不能与冻结前端修复混成一轮修改。

## iPad 核心键盘与 quick-keys 复验

### A 会话的 scale=1 连续段

A 会话早段 153 帧全部为 `scale=0.75`，只作为 zoom 豁免记录，不能认证键盘行为。后续实际操作仍写回 A 会话，取得了连续、完整的 `seq 367–486` 共 120 帧 `scale=1` 数据，派生文件为 `raw/final116-20261104-a-scale1.jsonl`；原始文件未改。

关键结果：

- 软键盘 `vv.height=320` 时 `occlusion=428`，宿主/控制列/状态栏分别正确避让；收起后宿主和 xterm 高度恢复。
- 再次聚焦、再次打开软键盘可重新进入避让；恢复中的 147px 浅中间态不重新抬起终端。
- `magic-hint` 的 68.5px 浅遮挡保持 `occlusion=0`。
- 唯一未观测项是 quick-keys 面板从未打开，因此该段单独判读为 INCONCLUSIVE，而不是产品 FAIL。

### C 会话的 quick-keys 补齐段

`seq 88–99` 是连续、无 pan、quick-keys 打开的完整软键盘开/关过程，派生文件为 `raw/final116-20261104-c-quickkeys.jsonl`。`verify-sop.mjs` 对该段 verdict 为 **PASS**：包括面板随缩短的控制列收高度、末行/控制列/状态栏位置和恢复。它只证明几何与命中，不替代用户截图/目测。

## C 全过程暴露的新反馈：键盘下的 Safari pan

C 会话还包含多轮额外滚动/平移与窗口变化，不能用上述局部 PASS 覆盖整个文件。

日志事实：

- `vv.offsetTop` 多次从 0 增长到 403；`app.top` 同步从 0 变为 -403，`document.scrollTop` 也跟随到 403。
- 这是持续状态，不只是动画帧：共有 120 个稳定帧带 pan，`seq 136 / 266 / 310 / 549` 四次手动 Capture 均记录到 `vv.offsetTop=403`、`app.top=-403`。
- 在这些帧中 PTY 宿主底边为 -103，终端确实可以整体离开可见区，与用户“外壳被抬升”的目测一致。
- 全部 583 帧仍满足 `rootScrollHeight == clientHeight`，`#app` inline height 始终为空，结构仍是 clip + fixed。因此这不是旧版“可滚动死区”或自动回顶代码复活。
- 键盘收起后 offsetTop、外壳和终端能够回到 0/满高；横竖屏切换的几何变化也有完整记录。
- `seq 573 → 574` 还有一帧“VV 已变、applier 尚未更新”的周期/resize 顺序差，下一帧即正确，不能与持续 pan 混为同一问题。
- 拉取过程有 8 个序列缺口，不能将完整 C 文件作为无缺口验收包；但上述反复出现且含手动 Capture 的 pan 事实不依赖缺口补齐。

## 诊断面板自身问题（工具层，非终端避让算法）

快照没有上报 `#viewport-debug-panel` 自身位置或 rect，所以面板四角实际位置以用户目测为主。源码与现象一致：

- `panelLayoutFor()` 只使用 `vv.height`，没有 `vv.offsetTop` 输入。
- 左上/右上使用固定 `top`；外壳发生 -403px 平移时，顶部面板也会离开可见区。
- 左下/右下使用固定 `bottom`；未 pan 时它仍相对布局底部，可能被软键盘覆盖，发生足够 pan 后又会进入可视区。
- 现有 Node 面板测试只覆盖默认顶部居位的高度和 free drag，没有覆盖底部居位，也没有覆盖非零 `vv.offsetTop`；既有 headless 四角检查同样不模拟 Safari 键盘 pan。

这解释了两件同时成立的事：诊断按钮在某些角落不可达，以及产品外壳/终端也被系统 pan 带走。不能把前者简单当成后者，也不能通过给终端 `occlusion` 再加 `offsetTop` 来修面板。

**处理边界：**这是一个新的、已真机复现的冻结外场景/需求冲突。不得直接恢复自动回顶、重钉整个 `#app`、修改终端 raw 算式或顺手改诊断面板。下一步需要先决定是仅修诊断工具，还是同时改变产品对 Safari 键盘 pan 的验收口径；任何产品改动都须另行讨论并做真机验证。

## 旧 PLAN 仍需真机完成的项目

由用户在可复现问题的 iPad 上操作，保留截图与必要的几何记录：

- 同一设备修前/修后口径对照：静止、软键盘弹出/收起、妙控键盘浮动栏、横竖屏、URL 栏显隐、缩放/pan、safe-area、前后台恢复；键盘已打开时刷新。
- Split View / Stage Manager 的适用场景；冻结实现已有窗口几何变化处理，但不能以源码或桌面模拟代替真机结果。
- PTY 与 opencode/reasonix/dsh iframe、quick-keys、IME、终端触摸滚动、fit/resize 回归。
- 暗色/浅色底部像素截图；诊断面板必须移离底边，不能以 `elementFromPoint` 代替视觉验收。
- 旧 PLAN 另列的 iPhone Safari 至少一轮；条件允许再补不同 iPadOS 版本与 Chrome(iOS)。

真机若出现需要改变冻结 CSS、键盘状态机、PTY resize 或诊断语义才能解决的问题，先停止修订并讨论，不在验证过程中直接改代码。

## 当前工作区说明

- 保留了 `sidebar_collapse.test.mjs` 对冻结 `queueTTYResize` 行为的测试适配。
- CHANGELOG 两处 `viewport_height.test.mjs` 总数由 27 修正为实际的 34，以满足既有计数门禁。
- 旧 p9 暂存实验已恢复；无活动终端、全阶段未测量的 `RESULTS-smoke-no-backend.json` 已删除。
- 保留了仍有验证价值的 PTY/侧栏检查脚本、p8 真机原始日志和侧栏改前/改后证据。
- 本文件只是本轮验证记录，不宣称 issue #116 已完成旧 PLAN 的全部真机关闭条件。

## 2026-11-05 42cd 混采单会话仲裁（D-C / D-B 子项，非单源验收包）

### 原始事实、三段边界与 owner 口头证据

- 原始文件为 [42cd679ea2cfdc147d6dc8b4ebdad910.jsonl](raw/42cd679ea2cfdc147d6dc8b4ebdad910.jsonl)。只读复核为 **207 行 / 207 帧，`seq 1–207` 连续无缺口**；只有 `seq 1` 为 init，没有 reload / 探针重启分界。207 帧的 `probeVersion` 均为 `p9-full-shell-session-avoidance-v1`，`schemaRev` 均为 `p9-additive-settle-readonly-v1`，`scaleSettle` 对象与 tick journal 均可读；`mirrorOcclusion ≡ occlusion` 为 207/207。手动 Capture 共 8 帧：`seq 36 / 59 / 83 / 116 / 135 / 146 / 164 / 184`。
- A=scale=1 菜单缩放与全键盘、B=妙控键盘 attached、C=D1 对比与输入框，是采集操作说明给出的三段顺序标签，不是 raw 内的唯一来源字段。文件没有可拆分 `menu` 与 `cmd` 的 `zoomSource`，也没有硬件 attached 的独立判别字段；因此本会话只通过“连续 seq + 无 reload + 判别字段在场 + 可按事件 / 数值分时间线”的 **42cd 混采判读闸门**支持下文明确列出的行为子项，**不成为 Z-MENU / Z-MAGIC 单源验收包，B / C 段仅作形态记录**。
- owner 已口头判定本会话截图目测符合预期。本仲裁未收到截图文件及截图↔seq 映射，故只登记 **INCONCLUSIVE（口头级视觉旁证）**；它不改写帧内数字，不单独产生视觉 PASS，也不豁免 D2 整行的同场截图链。
- 粗读中的“`offsetTop` 全 0”需修正：206 帧为 0，**`seq 14` 为 `90.65625`**，位于文件开头的 `scale=0.75` 段。下文 A 的 scale=1 判读链及 C 的 Web input 链均为 0；尤其 `seq 173–186` 没有 Safari pan，不能把这组数据写成 D1 所需的 Web input pan ↔ helper pan 对偶。

### A 段与回程：D-C settle 逐链判读

1. **变档前确为活动深键盘，普通 scale<1 链是 `0→1→2`，不是“数满即清”。** `seq 49` 为 `scale=1 / raw=occ=428 / xterm-helper-textarea / HO-2`。`seq 50` 变到 `scale=0.8500000238418579`、`raw=503.53125`，仍 helper 持焦，但当帧 `occ=0`、`candidate=0.8500000238418579 / ticked=0 / pending=true`。`seq 52` 的 journal `pass 60` 为 `ticked=1 / pending=true / occ=0`；`seq 53` 的 journal `pass 61` 为 `ticked=2 / scaleBelowOneSettled=true / pending=false`，同帧重入 `occ=503.53125`。两条 journal 的 candidate / baseline 均为该 0.85 值，`layoutHeight=880 / VVwidth=1404.703125 / raw=503.53125` 不变，故普通 0.85 链 **PASS**。
2. **0.85→0.75 是第二个活动中的纯变档样本，不是沿用旧候选。** `seq 54` 为 `scale=0.75 / raw=570.34375 / occ=0`，新候选 `candidate=baseline=0.75 / ticked=0 / pending=true`。`seq 57` 的同一 journal 同时保留 `pass 65: ticked=1 / pending=true` 与 `pass 66: ticked=2 / settled=true / pending=false`，两者 `layoutHeight=997 / VVwidth=1592 / raw=570.34375` 均不变；`seq 57` 当帧重入 `occ=570.34375`。这直接回答调度者关于“显示 2 后被清 0”的疑问：**普通链在 journal 中清楚显示 1→2，candidate 保持 0.75，不按回程清 0。**
3. **深键盘回程按已登记的 `1→0` 完成形态闭链。** 回程前 `seq 74` 为 0.75 活动让位 `occ=570.34375`；`seq 75` 回到 0.85、`occ=0` 并建立新候选，`seq 77` journal `pass 92` 为 `ticked=1 / pending=true`。`seq 78` 是当前 scale/raw 已变为 `1 / 428`、applier mirror 仍停在旧 0.85 `pass 94 / ticked=2 / occ=503.53125` 的过渡帧，`derivedState=unknown`；不把它冒充回程完成帧，也不让其连坐其他独立证据。随后 `seq 79` / journal `pass 96` 已重建为 `candidate=baseline=1 / ticked=1 / pending=true / zoomReturnFramePending=true`，`occ=0`、`derivedState=recovering`；`seq 81` / journal `pass 97` 为 **`candidate 1→0 / ticked 1→0 / pending true→false / zoomReturnFramePending true→false`**，同帧重入 `occ=428`。pass 96→97 的 `layoutHeight=748 / VVwidth=1194 / raw=428` 不变，pass 序号单调；按既有回程读者规则，该 D-C 回程链 **PASS**。seq 78 的过渡镜像 / 几何错位只作原样保留，不据此反判 D-C 或扩张为 D-D 全项 verdict。
4. 同文件另有一条无深键盘重入歧义的回程形态佐证：`seq 143` 普通 pass 169 为 `scale=1 / candidate=1 / ticked=0 / pending=true / returnPending=true / occ=0`；`seq 145` pass 170 为 `ticked=1` 且 pending / returnPending 保持 true；`seq 146` journal pass 171 为 `candidate=0 / ticked=0 / pending=false / returnPending=false`。三条均 `layoutHeight=748 / VVwidth=1194 / raw=68.5`，occ 保持 0。它同样证明回程清候选的合法形态，不把浅浮层写成键盘重入。
5. 全部 207 帧未出现已登记的两类跨字段矛盾：`pending != (candidate!==0 && ticked<2)` 或 return-pending 投影与同帧 zoomed/candidate/ticked 不符；各帧 journal 均只含 `scaleSettleTick` 且 pass 序号严格递增。故没有按 F-4 必须将上述 settle 链降为 INCONCLUSIVE 的矛盾帧。

**D-C 真机子项结论：PASS（子项级）**，范围仅为上述实际 scale 的普通候选链与回程完成链；不扩为 Z-MENU / Z-MAGIC、D-D latch、25% / 50% 或 D2 整行 PASS。

### D-B、F18 与 90% 优先项的演进边界

- **D-B 纯变档可观察行为由“缺样本”升级为 PASS（子项级）**：`seq 49→50→52→53` 与 `seq 53→54→57` 均满足变档前活动让位、变档帧仍 `raw>1 && focusEditable`、当帧付 0、重建候选、两个产品 tick 后重入。与 c3ee 的“活动中变档未触发”存在结构性差异。可是恢复锁字段未公开，且这些 raw 同时高于 50% 线，故“**不新置锁**”仍只是实现契约中的预期路径，**没有独立的真机判别 PASS**；不得以重入反推锁已清或实际选择 25% 线。
- **F18 维持 INCONCLUSIVE，但事实有所推进**：`seq 29–49` 连续为前台、可见的 `xterm-helper-textarea / HO-2`，可读 `seq 32–37 active(428)` → `seq 38–43 recovering / occ=0` → `seq 44–49 active(428)`，没有 Web 聚焦、reload 或 seq 缺口。然而 `unlockTrace` 在收起后、重入前另记录 `tick 27` 的终端宿主 click；0.75 的 `seq 57→62→68` 开合链也在重入前记录 `tick 37` 终端宿主 click。因此它们不是 VALIDATION 已登记的“无中间点击解锁”干净段；本项只登记连续持焦与几何链，不升格 PASS，也不判产品 FAIL。
- **D2 真机整行维持 INCONCLUSIVE**：本会话虽补齐 settle 与活动中变档，仍混用 menu/cmd 来源、起点不是正式矩阵要求的 scale=1 新会话，且截图只有 owner 口头级登记而无逐 seq 绑定；其他来源、形态与视觉义务不能用子项 PASS 覆盖。
- **25% / 50% 真机判别维持 INCONCLUSIVE（本机当前形态不可达）**：本会话正 raw 只有约 9.1% 浅遮挡（68.5/80.59375/91）与约 57.2% 深键盘（428/503.53125/570.34375），没有落入两条门线之间的样本；settle 后重入不承担该项证明。

### B / C 段形态、HO-3 与 GI-3 helper rect

- **B 段只作形态记录**：人工说明的“妙控键盘 attached”不能从 raw 单独分出硬件或 zoom 来源。可复核的浅遮挡形态包括 `seq 93–97`、`102–104`、`110–118`、`123–127` 的 `scale=1 / raw=68.5 / occ=0`，以及 `seq 128–146` 的第二次 1→0.85→0.75→0.85→1 缩放往返；不得把后者单独命名为 Z-MAGIC 或 Z-MENU 验收。
- **全文件 scale=1 的 PTY 深键盘让位共四组**：`seq 32–37`、`44–49`、`81–86`、`156–165`，均 `raw=occ=428` 且为 helper / HO-2；前三组属于 A 及其回程，第四组属于 C 的 PTY 形态记录。
- **C 段 HO-3 是第二次 R2 已知偏离现场实例**：`seq 172` 为 focusout / HO-1，`seq 173` focusin 到普通 `INPUT / web-input / HO-3`；`seq 176–186` 连续 `raw=occ=428`，含手动 Capture `seq 184`。`seq 187` 是 settle / HO-1 / raw=occ=0，不是额外的 focusout 帧。该实例继续只登记 Phase 3 前的 D-A / R2 已知偏离，不用于 D2 PTY PASS，也不关闭 R2。因 Web input 与 helper 段均无 Safari pan，**D1 对比仍 INCONCLUSIVE**。
- **GI-3 不是“本会话未见停滞”，而是新增同类停滞观察，仍 INCONCLUSIVE**：helper rect 在 `seq 15–26` 与 `29–54` 恒为 `top=958 / bottom=975`，且 `insideVisibleViewport=false`；这一不变值跨越 `seq 32–44` 的深键盘开、关、重开、`seq 50` 的 1→0.85 与 `seq 54` 的 0.85→0.75，直到 `seq 55` 才跳为 `210/227`。之后 helper 前台时的 `210/227` 又跨多组 scale/raw 变化保持，至 `seq 166` 键盘收起时才变为 `703/720`。这与 c3ee `seq 254+` 的 `839/856` 停滞属同类证据形态，但现有字段仍不能区分聚焦 rect、refit 滞后与真实出屏，也不凭 owner 的口头视觉符合预期反推 helper rect 正确；GI-3 不关闭、不判产品 FAIL。

## 2026-11-05 Phase 2 收口复核与门禁裁决（owner B）

本节是 Phase 2 停止推进时的证据盘点，不是新增真机验收。复核方式为只读逐行解析既有 JSONL、核对提交与已登记文档；**未改 raw、未启动浏览器、未重跑实时 Chrome/CDP、未运行新真机采集**。owner 选择 B 后，本周期不再补采；未闭环项统一由 [PLAN §11.3](../PLAN.md#113-phase-2-收口时显式登记的已知缺口) 承接。

### 快速盘点复核

1. **提交编号以仓库实际对象为准**：任务 1+2 诊断基座为 **`937d91f`**，不是不存在的 `937d914`；任务 5 工具判据落地为 **`4a17f9c`**，`14c2b90` 只归档 42cd raw。设计源 / PNG 为 `ef5037b`，面板 client 界修复为 `938fcd2`。
2. **面板真机读数**：三份新 schema 会话共 810 帧，`support=supported` 809 帧；唯一例外为 c3ee `seq253`，其同帧 `insideVisibleViewport=true`，已确认为工具坐标误判。手动 Capture 是实际可达性的直接操作证据，周期帧的 `supported` 只是面板自报，二者不混写。

   | 会话 | 帧数 | supported | 手动 Capture | 居位覆盖与边界 |
   | --- | ---: | ---: | ---: | --- |
   | b746 | 120 | 120 | 0 | tr 77、free 43；短窗口 / pan 形态，无手动 Capture |
   | c3ee | 483 | 482 | 32 | tl / tr / bl / br / free 均有手动 Capture；唯一 `seq253` unsupported 为工具误判 |
   | 42cd | 207 | 207 | 8 | 全部 tr；在 `938fcd2` 后采集，但不是 `seq253` 同形态 free 复采 |
   | **合计** | **810** | **809** | **40** | 已有主体证据，不等于完整键盘 × pan × 居位矩阵全部执行 |

3. **D1 不是零 pan 样本，但仍零合格对偶归因链**：身份、归属和同帧 rect 已由真机取得；同时 raw 确有 focus + 非零 `offsetTop`，不得概括成“Safari 从未 pan”。
   - b746 有 28 帧非零 `offsetTop`，其中 25 帧为 helper / HO-2；它们属于短窗口 / 缩放与 pan 混合形态，没有普通 Web input 对偶。
   - c3ee 有 69 帧非零 `offsetTop`，其中 55 帧 editable；普通 Web input 在 `seq385 focusin` 后的 `seq386–388` 为 `offsetTop=190.5`，helper 另有多段缩放 / pan。`seq413–418` A3 则是有 Web input 身份 + rect、无 pan 的 HO-3 实例。上述材料可证 pan 替代事实与同帧几何，但不能区分 Safari 自动聚焦、手势 / 窗口操作及采样先后。
   - 42cd 只有 `seq14` 为 helper + `offsetTop=90.65625`；HO-3 `seq173–186` 全为 `offsetTop=0`。它没有普通 Web input pan ↔ helper pan 对偶。
   - 因此，**同条件、可归因的普通 Web input ↔ xterm helper 对偶帧组仍为 0**；D1 有部分实测依据，但不能据此选择正向机制。
4. **D2–D4 的决策与实测分层**：D2 已签署，c3ee / 42cd 只上浮指定行为子项，D2 真机整行仍 INCONCLUSIVE；这是明确的结论分层，不是行为全通过。D3 / D4 按 PHASE1-CONTRACT §E 已分别签署等价性、refit 分类与最终一致口径，未实测的是实现和场景验收，不得改写成“未签署”。
5. **设计与 CDP 现状**：`ef5037b` 的 v4 设计源与 PNG 已交付，但 EXPORT-MANIFEST 的评审签字仍空白，也没有与最新验收矩阵的一致签署记录。实时 Chrome/CDP 仍只有本节上文“页面启动 / CDP 无响应”的独立工具 / 环境问题记录，没有新 verdict 或具体根因。

### 逐门禁正式判定

| 门禁 | verdict | 证据理由与后续边界 |
| --- | --- | --- |
| ① Capture 真机可达 | **部分达成＋登记** | 四角 / free 有实际 Capture、主体 supported 证据充分；完整组合未覆盖，`seq253` 同形态复采未做。对应 PLAN G01 |
| ② D1 有实测依据 | **明判：有部分实测依据，但不足以决定 D1；部分达成＋登记** | 身份 / rect / pan 替代事实成立，合格对偶与 Safari 归因仍缺；D1 正向机制不签署，Phase 4 对应实现继续阻断。对应 G02 |
| ③ D2–D4 有明确结论 | **closed（决策结论域）** | D2 的签署 + 子项 PASS + 整行 INCONCLUSIVE 是明确结论；D3/D4 已签署。G03–G07 继续承担行为实测，不被本 verdict 关闭 |
| ④ 设计源、PNG、矩阵一致并签署 | **未达成＋显式推迟** | 设计产物存在不等于完成一致签署；推迟为 Phase 3 开工前置，补签不得抹平 INCONCLUSIVE。对应 G08 |
| ⑤ 实时 Chrome/CDP 单独归因 | **部分达成＋低优先级登记** | 已与产品修复隔离，未捆绑、未反判产品；具体根因和 live verdict 未取得。对应 G09 |

**收口结论**：Phase 2 按 owner B 标记为“收口（有显式登记缺口）”。本登记不关闭任何 INCONCLUSIVE，不把子项 PASS 上浮为整行 PASS，不把 D3/D4 签署当成真机验收，也不表示 #116 完成。

## 2026-11-06 G02 / D1 owner 限制接受与停采裁决

### 核心依据（owner 产品裁决）

> “根据最近的测试，偶尔 pan 抬升，只在焦点切换到终端后有小概率触发，复现条件不明确，移出再移回焦点恢复正常，也可以滑动页面恢复正常，用户影响小。pan 不抬升且终端不抬升、光标被遮挡——印象中没有发生，就算有也概率更低，且可恢复。不要再在非致命缺陷上花更多时间，投入产出比太低。先过，以后有更多信息再开新 issue 处理。”

本节是 2026-11-06 调度会话的后续产品裁决，只改变 G02 的产品处置与 Phase 3/4 门禁承接，不改写此前 raw、seq 或任何行为 verdict。

### 现象登记（owner 口头级，INCONCLUSIVE）

1. **偶发 pan**：偶尔 pan 抬升仅被观察为焦点切换到终端后的小概率事件，复现条件未明；不得写成稳定复现或已证明焦点切换是唯一原因。
2. **两条恢复路径**：移出再移回焦点可恢复正常；手动滑动页面也可恢复正常。owner 判定用户影响小。
3. **致命遮挡未观测登记**：owner 对“pan 不抬升且终端不抬升、光标被遮挡”的口径是印象中没有发生，即使有也概率更低且可恢复；因此只登记未观测到该致命 / 不可恢复形态，不把口头回忆写成穷尽性不存在证明。
4. **证据与归因边界不变**：本节没有新增 raw、连续 seq、截图或合格对偶帧，全部为 **INCONCLUSIVE（owner 口头级）**。D1 的同条件 Web ↔ helper 对偶帧组仍为 **0**；探针 tick 顺序与同瞬间几何只作顺序证据，`vv.offsetTop` 采样变化只证明 pan 事实，Safari 侧归因仍不可采，均不得冒充 Safari 内部因果证明。

### G02 处置与后续门禁

- **G02 改为 owner 裁决关闭（限制接受）**：D1 正向机制按 owner 产品裁决接受现行为（低概率 pan、可恢复；未见致命遮挡）并挂起。**D1 证据子项仍 INCONCLUSIVE**；此处关闭是产品层面的“接受现行为＋挂起”，不是证据 PASS，也不是正向机制已测量签署。
- **Phase 3/4 影响**：Phase 4 的 D1 相应验收前置按 owner 裁决解除，不再等待对偶帧，不启动 Phase 3/4 的 D1 归因或机制选择义务；Phase 3 输入所有权拆分（G10）仍照常，其他独立门禁不变。本裁决覆盖此前文档中的 D1 阻断口径，但不追溯修改历史证据记录。
- **停采与新 issue 候选**：不再为该非致命缺陷投入本轮补采 / 归因。未来若复现率可观，或发生上述致命 / 不可恢复遮挡，再依据本节证据开独立 issue 处理 D1 pan 归因与机制选择；当前只登记候选，不开启新 issue。

## 2026-11-06 Phase 2 收口补签记录（G08）

| 门禁 | 补签 verdict | 签署记录 |
| --- | --- | --- |
| ④ / **G08** 设计源、PNG、验收矩阵一致并签署 | **CLOSED（签署达成；仅一致性域）** | **上一轮 Phase 2 收口时门禁④未达成，本节即为补签。** [EXPORT-MANIFEST v5](../design/EXPORT-MANIFEST.md) 已登记矩阵状态入稿、改动节点与重出清单；`d1_notes` / `d1_viewport_geometry` / `qk0001` 对应 PNG 实际像素分别为 **2362×3762 / 2882×2482 / 1002×1864**。**截至本轮提交，设计源文本=VALIDATION 登记状态=PNG 内容三项一致。** G02 证据子项、D2 整行 / Z 系列、25% / 50%（本机不可达）、F18、GI-3、live CDP 等 INCONCLUSIVE 全部保留，子项 PASS 不上浮；真机像素排版仍未验证。本轮未改产品 / 诊断 / evidence 会话，未 commit。 |

## 2026-11-06 Phase 3 输入所有权拆分离线实现轮

- **授权与提交**：`AUTH-20261105-P3-OWNERSHIP` 及 `AUTH-20261105-P3-OWNERSHIP-A1`（[PLAN §5 授权包](../PLAN.md#L230-L244)）；实现提交 `0258f1e`，授权登记提交 `b49d34e`。本节只登记离线实现与门禁事实，**不是真机 verdict**。
- **产品谓词**：`document.activeElement` 单读，editable 逐字三分支保持为 `TEXTAREA` / `INPUT` / `isContentEditable`；随后当帧沿 `parentElement` 祖先链取首个 `data-instance-id` 真值宿主，仅在其为 `#terminal-container` 直接子且 inline `display !== "none"` 时取得资格。`scaleStepNoLatchExit`、既有退出边与进入门三处消费同一 `focusEditable` 布尔。
- **A1 夹具-only 同步**：`verify-live-choiceA.mjs` 仅同步 stub 的前台宿主祖先链并展示既有 `hosts[0]`；既有断言表达式与预期值保持原值，不新增 / 删除帧，不改几何或回放语义。
- **门禁计数（调度者独立复跑原文，本登记轮未复跑）**：`node --test internal/ui/testdata/viewport_height.test.mjs` **40 pass / 0 fail**；`zoom_scale.test.mjs` **37/37**；`terminal_status.test.mjs` **68/68**；`viewport_debug.test.mjs` **48/48**；`go test -count=1` 三锚点均 **ok**；`node docs/plans/fix-issue116/evidence/verify-live-choiceA.mjs --html internal/ui/static/index.html` 离线 **PASS / exit 0**，既有断言原值通过。以上均不替代真机确认。
- **双评审**：评审 1 `opencode-go/muse-spark-1.3-contributor` **APPROVE**（0 BLOCKER / 0 MAJOR，MINOR×4）；评审 2 `minimax-cn/MiniMax-M3.1-Flash` **APPROVE**（0 BLOCKER / 0 MAJOR，MINOR×6 + CHALLENGE×4）。修复轮已完成 MINOR-1 / MINOR-2 / MINOR-4 / MINOR-5。**未认证声明**：MINOR-3 / MINOR-6（A1 措辞张力）按登记解释处理，未改码。
- **G10 状态更新（不整行关闭）**：R2 已知偏离在**已交付代码**上关闭；c3ee A3 与 42cd HO-3 仍是 Phase 3 前的历史现场实例，历史证据及其性质不改写。真机 HO-3 / R2 验证仍未执行，Phase 3 任务 4 待安排，故 [PLAN §11.3 G10](../PLAN.md#L393) 余项保持，不写成整行关闭或真机 PASS。
- **已登记后续项（不新开工号，均不阻塞本轮）**：D2-7b 结构断言之外，tab 误判的当帧行为回归仍缺 tab 结构桩；单 shown host 不变量仍缺结构断言；`focusEditable` 复合布尔命名易混淆，可后续复查是否采用 `ptyFocusEligible` 之类命名。

## 2026-11-06 Phase 3 真机首轮 p3-A-WEB-01 判读

- **会话**：[a22c raw](raw/a22c660f67a1347b1f4e38ca0126a7d0.jsonl)，session `a22c660f67a1347b1f4e38ca0126a7d0`，`latestSeq` 375，文件内实际 **374 帧 / seq 1–375**，唯一缺口 `seq168 → seq170`（两条 `receivedAt` 相差 **61ms**，两侧均为 `periodic`、相邻帧无 verdict 跨越，按调度突发处理，不判 FAIL）。`probeVersion` 全 374 帧一致为 `p9-full-shell-session-avoidance-v1`；`keyboardSession.scale` 全 374 帧为 **1**；设备为 iPad / iPadOS 18_7 / Safari 27.2（`dpr` 2，`touchPoints` 5）。**raw 截至本节登记时仍为 untracked，尚未进入任何提交**（处置见 PLAN §5 `AUTH-20261105-P3-FOCUS-GUARD` 同轮的“raw 文件处置裁决”与 §9 表格新增行）。
- **判读口径**：本节全部 verdict 为 **单会话、单轮、子项级**。判据 = 帧字段 + owner 目测（**owner 2026-11-06 口头裁决取消本轮全部截图门槛**，故本轮任何子项都不得写成“截图确认”）。子项 PASS 一律不上浮为 R2 / R3 / R4 整行 verdict，Phase 3 任务 4 不因本节关闭。
- **独立性声明**：本节由仲裁 Sub-agent 从 raw 文件逐帧重算；**不采信测试 Sub-agent / 调度者的转述计数**。以下凡与转述不一致处均按 raw 重算值登记（见“复核订正”）。

### 原始帧分类计数（本节实测）

| `ownership.category` | 帧数 | 说明 |
| --- | --- | --- |
| `HO-2`（前台 PTY 宿主内 editable） | **246** | 其中 `raw > 0` 的 101 帧，`raw <= 0` 的 145 帧 |
| `HO-1`（非 editable / BODY / BUTTON / DIALOG） | **54** | `occlusion` 全 54 帧为 0 |
| `HO-3`（本页普通 Web 输入，editable 但不在终端宿主内） | **51** | 9 个连续块：36–43、53–60、86–94、109–112、119–120、125–126、130–134、347–350、355–363 |
| `unknown`（`iframe-inner-focus-not-inspectable`，**IFRAME 焦点，非 `HO-4` 计数值**） | **23** | seq 175–197，`occlusion` 全 23 帧为 0 |

`reason` 分布：`periodic` 174、`settle` 96、`resize` 47、`focusin` 28、`visibilitychange` 8、`scroll` 7、`manual` 7、`focusout` 6、`init` 1。

### 逐子项 verdict

| 编号 | 判读问题 | verdict | raw 证据锚点 |
| --- | --- | --- | --- |
| **S0** | 会话可采、schema / 版本一致 | **PASS** | 374 帧全 `probeVersion` 一致；`scale` 全 1；唯一缺口 seq168→170 61ms 突发、无 verdict 跨越 |
| **S1 / F1 + F3** | 本页普通 Web 输入（INPUT / 重命名等）不搭载 PTY 避让 | **PASS（子项）** | `HO-3` **51/51 帧 `occlusion` = 0**；`insideTerminalHost` 全 false、`insideTerminalContainer` 全 false；seq36–60 / 86–94 / 347–350 / 355–363 为 INPUT |
| **S4 / F8** | PTY 前台宿主持焦时避让回归 | **PASS（子项）** | `HO-2` 且 `raw > 0` 的 **101 帧中 81 帧 `occlusion` == `raw`**；区别的 20 帧全是 `raw` = **68.5** 的浅浮条帧（seq 4–8、322–326、332–333、338–341、369–370、373–374），`occlusion` = 0，属源码 `index.html:3622-3625` 明写的“低于 50% 进入线的浮条离开会话后不可自行重开”契约，**不是避让失败**。命中例：seq 61–83、113–118 等 `occ` 428 = `raw` 428 |
| **S5 / F9 + F10** | 失焦后无残留让位；焦点交接顺序 | **PASS（子项）** | 6 个 `focusout`（seq 35、52、154、279、327、334）**全部同帧 `occlusion` = 0** 且 `HO-1`；seq 327 / 334 的 `focusout` 帧本身仍带 `raw` 68.5（浅浮条），`occlusion` 仍为 0，无残留。之后 seq 331 / 345 / 364 重新 focusin 回到 `HO-2` 且 `occ` 0，几何无滞留 |
| **S2（几何）** | quick-keys 面板在键盘活动中可达 | **PASS（仅几何子项）** | `keyboardSession.derivedState == "active"` 的 82 帧中，`quickKeys.open` 为 true 的块（seq 113–137、199–203）面板 rect `bottom` = **272** ≤ `vv.height` = **320**；`panel.rect` 自身 `bottom` 219.2 / 244.2 全在 vv 内 |
| **S2（焦点常驻）** | 粘贴 textarea 持焦期间不被抢回终端 | **FAIL** | 见“异常 A” |
| **S3 / iframe（HO-4 侧）** | opencode / reasonix / dsh iframe 父页无干预 | **INCONCLUSIVE** | 23 帧全部 `activeInput.tag` = IFRAME、`unavailableReason` = `iframe-inner-focus-not-inspectable`、`occlusion` = 0、`hostIsForeground` = null。**父页数值面无干预成立**（23/23 `occ` 0），但 iframe 内部焦点不可检视，HO-4 / GI-3 / GI-7 **不出 verdict，不上浮** |
| **告警帧 seq84** | `HO-2` 但 `raw` 0 / `occ` 428 | **登记，不判 FAIL** | seq 80–85 均为 `HO-2`、`occ` 428、`derivedState` active；仅 seq84 单帧 `vvHeight` 748（`raw` 0），seq85 立即回到 320 / `raw` 428。判为单帧采样抖动（探针 `vv.height` 与镜面 `__mwTerminalOcclusion` 非同源读数），保留登记 |

### 复核订正（对上游转述的实证修正，以 raw 为准）

1. **“宿主 bottom 恒 728”不成立**：`HO-3` 51 帧的 `app.bottom` 实为 **748**（49 帧）与 **557**（seq 356–363，8 帧），**不存在 728**。748 是 shell 恒高（`screenshot-free` 口径下 shell 不被缩），557 出现在 `vv.offsetTop` = **190.5** 的 Safari pan 段（seq 356–363，`vv.height` 320）。该 pan 段正是 F1 要观察的“系统输入可见性交给 Safari”形态，已如实登记，但**未作为截图 / 视频门槛**（owner 已取消）。
2. **“HO-2 且 raw>0 的 81/81”不准确**：分母实为 **101**，`occ==raw` 为 **81**；差额 20 帧全为 68.5 浅浮条帧（见上表），按源码契约预期，**不改判 S4 结论**，但计数须按 81/101 登记。
3. **iframe 帧不是 `HO-4`**：`ownership.category` 实为 `unknown` + `iframe-inner-focus-not-inspectable`，不是契约分类里的 `HO-4`。登记时不得写成“HO-4 已取得分类”。
4. **探针 `keyboardSession.focusEditable` 不是产品谓词真值**：该字段由探针自身 `isEditableHost(doc.activeElement)`（`viewport_debug.js:467-471`、`:726`）独立计算，只看 TAG 是否可编辑、**不叠加** `0258f1e` 的前台宿主归属判定，故 `HO-3` 51/51 帧该字段为 `true` 属**探针口径**，不构成“产品谓词未收窄”。`HO-3` 全部 `occ` 0 = 反证。**限制**：当前无任何帧字段直接暴露产品侧 `focusEditable` 布尔，AUTH-20261105-P3-OWNERSHIP 真机验证的这一面属**结构性盲区**，与本轮修复无关，登记为后续诊断项。

### 异常 A：quick-keys 粘贴 textarea 焦点被程序化抢回（FAIL）

- **现象**：粘贴 fallback 的 `#quick-keys-paste-textarea` 持焦期间被程序化抢回终端。raw 上表现为 `HO-3` TEXTAREA 块被打断后落回 `HO-2`：seq 109–112 → 113、119–120 → 121–122、125–126 → 127–128、130–134 → 135，共 **4 次**；`click` / `clickOnTerminalHost` 计数在各次打断前后**未变化**（`clicks_between` = 0），即**不是指针事件驱动**。
- **间隔实测（纠正转述）**：各块“最后 HO-3 帧 → 首个 HO-2 帧”间隔为 **73.9ms（112→113）、73.7ms（120→122）、80.1ms（126→128）、74.2ms（134→135）**；两次打断的“HO-2 落点 ↔ 上一次打断落点”为 **739ms（113→122）、757ms（122→135）**。**与 2000ms 轮询周期、以及与 `periodic` 帧间实测中位数 ~1005ms 均不相符**，故本节**不采用上游“约 2013 / 2079ms、2000ms 轮询驱动”的时序表述**，只保留与之相容的定性结论（见下）。
- **代码身份（仲裁独立核对，逐字 diff）**：`git show 5268869:internal/ui/static/index.html` 的 `focusTerminalIfPossible()` 位于 **:7842-7863**，当前 `internal/ui/static/index.html` 同函数位于 **:8238-8253**；对两段取函数体做 `diff -u` **无差异（IDENTICAL）**。函数体含 `if (state.renamingInst || state.renamingWT) return;`（#122 rename 守卫）并以 **`session.term.focus()` 结尾**，**无任何粘贴 / 通用 editable 守卫**。基线 `pollTimer = setInterval(refresh, 2000)` 在 **:5071**，当前 **:5467**；`refresh()`（`:3302`）→ `render()`（`:5799`）→ `renderWorkspace()`（`:6386`）→ `focusTerminalIfPossible()`（`:6432`）。基线提交 `5268869d60687ad1256f5ffd9bde1408612972c9`（2026-11-03）。
- **归因裁决**：分类 **(b) 冻结基线既有行为**。依据 = **代码身份**（`5268869` 与当前逐字同一，且无 paste 守卫）+ **设备帧同现**（4 次程序化抢焦、`clicks_between` = 0、无 `onTerminalHost` 指针事件）。**不归因于 `0258f1e`**：该提交只收窄避让谓词，不改本函数；本轮 `HO-3` 51/51 `occ` 0 亦表明避让未参与。
- **未定项（诚实标注）**：具体**触发调用点**未能由 raw 唯一确定——`focusTerminalIfPossible()` 有 9 处调用（:5497、:6432、:6713、:6766、:8278、:8321、:9179，另有导出 :10319），`clicks_between` = 0 只能排除 `:6432` 由点击解锁路径触发的可能定式，**不能**排除其余调用点；739 / 757ms 的节律与 2s 轮询、~1005ms 帧间均不吻合，故“轮询驱动”仅作**相容假设**保留，不写为已证。

### 异常 B：失焦回终端第一下不弹软键盘、第二下才弹（同根假设，未归因）

- **现象（owner 口头级 + 帧侧间接）**：失焦后点回终端，第一下只出现实心光标、不弹软键盘，第二下才弹。帧侧：多个 `focusin` 帧相对上一帧 **`click` 计数 delta = 0**（seq 97、113、122、128、135、153、258、270、283、331），即焦点已在帧间被程序化取得，随后用户点击才产生键盘。
- **裁决**：按 **同根假设** 登记——与异常 A 同属 `focusTerminalIfPossible()` 的程序化 `session.term.focus()` 与用户触摸意图竞态。**不孤立复采、不单独修复、不冒充已归因**；修复后并入同一复验会话验证（见 PLAN `AUTH-20261105-P3-FOCUS-GUARD` 的复验条款）。
- **证据限制**：探针**无键盘显隐真值字段**，`keyboardSession.derivedState` 是 `vv` 高度差的派生推断而非键盘真值，故 B 不能仅凭帧定性；owner 目测为本条的唯一直接观察来源（截图门槛已取消，目测为口头级）。

### 本轮明确未完成 / 不上浮

- **R2 / R3 / R4 未整行闭环**：`HO-3` / `HO-2` / `focusout` 子项 PASS 不覆盖 R2 的“键盘已打开”全链路与 R4 的实例切换、PTY A→B 段；本节无任何整行 verdict。
- **iframe HO-4、GI-3 / GI-7 维持 INCONCLUSIVE**，不因本节 23 帧 `occ` 0 升格。
- **seq84 告警**保留为未定单帧。
- **F5 焦点常驻 FAIL** 已由 PLAN `AUTH-20261105-P3-FOCUS-GUARD` 承接，修复后须以**同一复验会话**同时复验 A、B 与 S2 焦点常驻子项。

### G10 状态更新（真机首轮后，仍不整行关闭）

- 上一节“离线实现轮”登记的“真机 HO-3 / R2 验证仍未执行”**已作废**：本轮 a22c 会话已执行真机首轮。
- 现状态：**HO-3 / R2 的“Web 输入不搭载 PTY 避让”子项 PASS**（`HO-3` 51/51 `occ` 0）；**R2 整行仍不闭合**——本轮未覆盖“普通 Web 输入自身被 Safari 保证可见”的完整链路（`clicks_between` 与 `HO-3` 段内出现 seq 356–363 的 `vv.offsetTop` 190.5 pan 段，未作截图门槛判定，owner 已取消截图门槛）。
- 另新增 **F5 焦点常驻 FAIL**，与 G10 无关但同属 Phase 3 任务 4，承接于 `AUTH-20261105-P3-FOCUS-GUARD`。
