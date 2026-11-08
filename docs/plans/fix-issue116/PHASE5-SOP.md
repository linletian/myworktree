# Phase 5 真机验证 SOP（issue #116 · D2 zoom 场景与生命周期全表面 · 采证与判负规则 v1）

**日期**：2026-11-07
**性质**：**执行层采证规范**。本 SOP 覆盖 [PLAN §5 Phase 5](PLAN.md#phase-5生命周期与全表面回归) 的**场景矩阵**：**D2 zoom 三档（P5-Z1 / P5-Z2 / P5-Z3）+ A0 0.75 必测（P5-Z5）+ scale>1 反向（P5-Z4）**，以及**生命周期 / 全表面回归**（P5-L1 至 P5-L6、P5-I1）。**本 SOP 不修改任何已定判据**，只定义采集顺序、字段核对与 verdict 登记方式；**不授权任何产品实现、不改任何 evidence 工具**。

**判据 = 现役 [evidence/verify-sop.mjs](evidence/verify-sop.mjs) 的 25% / 50% 会话机 + D2 `zoom_guard` 语义**（`internal/ui/static/index.html` 缩放段；PHASE1-CONTRACT 术语章"软键盘避让会话"与 D2 细则 1–6；PLAN §4 D2 已签署条款）。

> **判据代际声明（必须前置，承 PLAN §14.1 / §16）**：**A0-SOP 的 0.15 比值地板与其「不变量 1–10」编号属历史代际**，**不得作为本文任何场景的门线**。全文只在**解释「为何 147 档 / 25%–50% 中间带在本机不可判别」**时提及 **105.3** 等地板数字（PHASE4-SOP 同口径），**不构成判据、不进入 PASS / FAIL 公式**。凡本 SOP 与 [PHASE1-CONTRACT](PHASE1-CONTRACT.md) 表述冲突，**以契约与 owner 签署条款为准并停止回报**。

**与 [PHASE4-SOP](PHASE4-SOP.md) 的关系**：**镜头 / 流程骨架相同**——前置条件、判负总表、归档纪律、诚实边界、产物状态边界四段**照抄 PHASE4-SOP 原文**（凡新增之处**只以「弹同节例」形式追加**，不改写原句）。本 SOP **不重写 P4 已定判据**，只把 Phase 4 的 `scale = 1` 硬门槛替换为本文 §0 的 **scale 分档纪律**。

---

## 前置条件（逐条，缺一条该轮不得开跑；1–5 条**照抄 PHASE4-SOP 原文**）

1. **同一台 iPad**：Phase 4 门禁要求"同一 iPad 上证明 R1"（[PLAN §5 Phase 4 门禁](PLAN.md#phase-4pty-终端自有视口与-pan-处理)），故整轮矩阵必须在同一台设备上完成；记录型号与 iPadOS 版本。Safari **无缓存**（服务端已禁缓存），跑的是**工 HEAD 构建**（记录提交号）。
2. **诊断面板落位 `tr`（右上）**；**每步操作后按一次 `Capture`**（`manual` 帧）；需要连续 seq 的场景先按 **`Start continuous`**（1000ms periodic）再操作，结束后按 **`Stop`**。
3. **妙控键盘只在指定场景接入**（下表"妙控"列）：**禁接 = 不接且不摆上桌面**（避免意外浮层 / 键盘事件污染）；**必须接 = 按场景步骤接入并等 2 秒**；**可接 = 仅 P4-I 持续 pan 频度观察档可接**，其余场景非必需。**无障碍浮窗 / 分屏开启状态每场景记录**（形态字样 + 是否 B 会话 / Split View 在场）。
4. **浏览器并发 = 1**：整个 SOP 期间**不得开第二个标签页访问同一实例**；**不做任何 Chrome / CDP 并行采证**（AGENTS.md §4、[PLAN §7](PLAN.md#7-验证与资源纪律) 第 3 条）。
5. **令牌在 URL 内以认证 shell 打开**（`http://<host>:<port>/?token=***&viewport-debug=1`），令牌**外部传入、不落盘**。归档路径沿用既有 raw 命名惯例：`docs/plans/fix-issue116/evidence/raw/final116-<YYYYMMDD>-p5-<scenario>.jsonl`（`<scenario>` 取 `z0` / `z1` / `z2` / `z3` / `z4` / `z5` / `l1` / `l2` / `l3` / `l4` / `l5` / `l6` / `i1`；同一场景多轮用 `-2` / `-3` 后缀，**不覆盖上一轮**）。**本 SOP 的 `<scenario>` 段与 PHASE4-SOP 的 `p4-*` 段相互独立，不得同名冒充。**

> **弹同节例 · 前置条件新增第 6 条（本 SOP 专属，不改动上 5 条）**：
>
> 6. **本 SOP 需要外接 / 妙控键盘（≥ P5-Z3 需要 `⌘` 键）**：外接 / 妙控键盘是本轮**硬件前置**，不是可选配件。**无该硬件时 P5-Z3 记「未执行」**（verdict = INCONCLUSIVE，理由栏写"缺硬件"），**不得用其他手段替代生成 `zoomSource`**——不得用 JS 改 `visualViewport.scale`、不得用 DevTools 模拟、不得截屏伪造缩放帧、不得把 P5-Z2 的菜单缩放帧改标为 Z-MAGIC 帧。P5-Z1 / P5-Z2 / P5-Z4 / P5-Z5 不依赖该硬件，按各场景"妙控"列执行。

**适用构建（不匹配即停，不得开跑；**照抄 PHASE4-SOP 原文**）**：诊断面板标题必须**是** `Viewport diagnostics · p9-full-shell-session-avoidance-v1`，且帧内 `schemaRev` 含 `p9-additive-touch-truth-v1`；任一不满足 → **停止，关掉标签页重开**（服务端已禁缓存）。这两项与现役 [evidence/verify-sop.mjs](evidence/verify-sop.mjs) 的 `EXPECTED_PROBE_VERSION` 及现役探针 `SCHEMA_REV` 一致。

> **戳记代际提醒（照抄 PHASE4-SOP 原文）**：A0-SOP 文首写的 `p8-full-shell-overlay-wheel-v1` 属 **p8 时代历史 SOP**（[PHASE1-CONTRACT](PHASE1-CONTRACT.md) 头部"历史文档定位"）。本文档与现役工具同代，戳记以本文"适用构建"为准；**不得混代比对**，也不得以戳记不同反判历史会话造假。

---

## 0. 核心纪律：scale 分档与妙控（每场景开始前必读）

本节**改写 PHASE4-SOP §0 的第一条**——Phase 4 的 `scale = 1` 硬门槛在 Phase 5 **不再适用**，改为**分档纪律**：

- **每场景开始前与结束后各一次 `Capture` 自查 `vv.scale`**（两次 `manual` 帧），并把两次读数写进该场景时间线。**本 SOP 的全部 zoom 场景按"预期档位"记，而不是按"必须为 1"记**：P5-Z1 / P5-Z2 / P5-Z3 预期经历 `1 → <1 → 1`（或 `1 → <1 → 1 → <1`），P5-Z4 预期经历 `1 → >1 → 1`，P5-Z5 预期在 `0.75` 停留后回 `1`；**P5-Z0 / P5-L1 至 P5-L6 / P5-I1 仍以 `scale = 1` 为基线档**，出现非 1 即按污染帧处置。
- **`scale ≠ 1` 的帧在本 SOP 不再是"污染"**，但**只有在场景声明的档位内的非 1 帧**才可作判负载体：**当帧 `vv.scale` 必须与该步骤声明的目标档一致**（如"缩到 ~0.75"允许 0.75 附近读数，"回 1"必须读 1），**偏离声明档位的帧按污染帧处置**（起止 seq + 实际读数如实标注，**保留在 raw 里、不得裁剪**）。
- **settle 口径**：沿用 [PHASE1-CONTRACT](PHASE1-CONTRACT.md) 术语章 D2 细则 3（两次 500ms tick + ε=0.001 + 候选期几何 / raw 稳定）；**不新增、不放宽、不收紧**。
- **低倍率场景的归属变更**：PHASE4-SOP §0 写的"低倍率场景不属本文，由 [DATA-COLLECTION §10](DATA-COLLECTION.md) 的独立单源会话承担"是 **Phase 4 当时的边界**；**自本文起，`scale ≠ 1` 的 Z-PINCH / Z-MENU / Z-MAGIC / 0.75 必测由本 SOP 的 P5-Z1 / P5-Z2 / P5-Z3 / P5-Z5 承担**，此前 DATA-COLLECTION 路线**不改写、不升格、不重判**（历史证据原样保留）。

**判负锚点字段（本 SOP 每帧必读）**：`vv.scale` / `scaleSettle*` 族（`scaleSettleCandidate` / `scaleSettleBaseline` / `scaleSettleLayoutHeight` / `scaleSettleVVWidth` / `scaleSettleRaw` / `scaleSettleTicked` / `scaleSettlePassSeq` / `scaleSettlePassSource` / `scaleSettlePending` / `scaleBelowOneSettled` / `zoomReturnFramePending` / `scaleSettleTickJournal`）/ `ownership` / `activeInput`（含同帧 client rect 与 `insideTerminalHost` / `hostIsForeground` / `hostVisible`）/ `occlusion` / `ptyHosts[]` / `vv.height` / `vv.offsetTop`。这些字段**探针已职**（`internal/ui/debugassets/viewport_debug.js` 的归属块与 `scaleSettleFieldsOf` 镜像块，`viewport_debug.js:368-409` 及邻近）；**未核实 / 不存在的字段一律按缺字段处理 → INCONCLUSIVE，不得编造字段名**。

> **弹同节例 · 字段名族（本 SOP 专属）**：`owner` / `activeInput` / `scaleSettle*` 是**判负锚点**，不是装饰字段。**`scaleSettle` 整块为 `null` 且 `scaleSettleUnavailableReason = "settle-mirror-not-published"` 的帧**，其 settle 子项按缺字段处置（INCONCLUSIVE），**不得从稳定几何反推 settle**（PHASE1-CONTRACT F 节"结构性不可验不是产品失败"）。**判定锚点是字段值本身**：例如"释放后不回 1"必须同时有 `vv.scale` 读数与释放后帧的 `scaleSettle*` 链条，**不得只凭面板肉眼读数**。

**妙控键盘纪律（照抄 PHASE4-SOP 原文）**：接入 / 断开本身会改变 `visualViewport` 与焦点事实，因此"禁接"场景里**键盘不摆上桌面**（放包里 / 远离）；"必须接"场景只按步骤接一次，接完**等 2 秒**再 Capture；环境变化（浮窗 / 分屏）要逐场景记录，否则该场景的证据不可归因。

---

## 1. 场景矩阵

每个场景一块，字段固定：编号 / 目的 / 前置会话状态 / 步骤逐条 / 每步 Capture 点 / scale 要求 / 妙控要求 / 期望（可核对字段量）/ 判负规则 / 证据要求。

**字段名来源声明（照抄 PHASE4-SOP 原文，弹同节例）**：本文所有字段名取自现役 p9 探针快照（`internal/ui/debugassets/viewport_debug.js`）与现役 `evidence/verify-sop.mjs` 的读取路径：`app.bottom` / `app.inlineHeight` / `app.layoutGap` / `document.htmlOverflow` / `document.bodyOverflow` / `app.computedPosition` / `document.rootScrollHeight` / `document.scrollTop` / `document.scrollingElementScrollTop` / `occlusion` / `ptyHosts[].bottom` / `ptyHosts[].inlineBottom` / `ptyHosts[].visible` / `controls.inlineBottom` / `controls.bottom` / `controls.height` / `statusBar.inlineBottom` / `statusBar.bottom` / `quickKeys.clientHeight` / `quickKeys.maxHeight` / `quickKeys.scrollHeight` / `xtermRows.bottom` / `vv.height` / `vv.offsetTop` / `vv.scale` / `ui.activeElementTag` / `ui.quickKeysOpen` / `reason` / `ownership` / `activeInput` / `scaleSettle*` / `keyboardSession` / `unlockTrace`。**未核实 / 不存在的字段一律按缺字段处理 → INCONCLUSIVE，不得编造字段名。**
>
> **弹同节例 · `owner` 与 `zoomGuard` 的字段身份（本 SOP 专属，如实声明）**：任务口径中的 **`owner`** 对应探针帧里的 **`ownership`（归属类别 HO-1…HO-4）+ `activeInput`（活动输入宿主类型与同帧 client rect）** 两个字段，**探针不存在名为 `owner` 的单字段**——本 SOP 一律以上述两字段为准，**不得凭"看起来是谁"推断归属**（PHASE1-CONTRACT F 表前置工具条：`ptyHosts[].visible`、角色启发式或派生标签均不足以推断归属）。**`zoomGuard` 同理不是探针字段**：它是 `index.html` 缩放段的**行为语义名**（`zoomOptOut` / `scaleSettlePending` / `zoomReturnFrame` / `scaleStepNoLatchExit`），判读时以 `vv.scale` + `scaleSettle*` + `occlusion` 的**行为结果**为载体，**不得要求 raw 里出现 `zoomGuard` 字面字段**；若以源码名做归因，须在图注写明"归因标签，非帧字段"。

**场景编号与判据映射**（便于逐条落表，不改变各场景正文）：

| 编号 | 判据来源 | 主要锚点 |
| --- | --- | --- |
| P5-Z0 | PHASE4-SOP P4-A（S1–S8 引用）+ 静置 baseline | `occlusion` / `ptyHosts[]` / `vv.offsetTop` |
| P5-Z1 | PLAN §4 D2 来源口径 ①②；F13 | `vv.scale` 可达性 + 释放后回 1 |
| P5-Z2 | D2 细则 2 / 3 / 4；F13 / F14 | `scaleSettle*` 链 + `occlusion == raw` |
| P5-Z3 | 同 P5-Z2，来源换键盘 `⌘-`；PLAN §1.3 浮窗 | 同上 + zoomSource 可识别点 |
| P5-Z4 | D2 细则 3（scale>1 全帧 opt-out）；F13 | `occlusion = 0` + 不进入 |
| P5-Z5 | PLAN §1.3；契约 F 表「本机 25% / 50% 判别带不可达」 | 登记 only（不反推门线） |
| P5-L1 | F12 / F20 | 形态记录；不判 verdict（按 F12/F20 宽容） |
| P5-L2 | F15 / F16 | 归属重判 + raw / 条款字段 |
| P5-L3 | F8 / F11 / GI-8 / D3 | 补充帧采集（不判 FAIL） |
| P5-L4 | F19 | 观察记录；"组合文本不丢"不判 FAIL |
| P5-L5 | GI-6 / S8 | 根 `scrollTop` 恒 0 |
| P5-L6 | PLAN §6「暗色/浅色、safe-area」 | 帧记录 only |
| P5-I1 | PLAN §6 iframe 行；HO-4 | 父页面无干预；帧记录 only |

### P5-Z0 回归基线 + 连续会话真空段

| 项 | 内容 |
| --- | --- |
| 目的 | ① 用同一构建重跑 Phase 4 的 **P4-A 回归基线**一次（证明 p9 现役几何没退化，**不是** D2 证据）；② 在 **scale = 1 且无键盘状态**下采一段 ≥30 s **无事件 baseline**，用于判负"**静置不该自发 pan / 不自发让位**"。 |
| 前置会话状态 | 新开标签（认证 shell）→ 面板落位 `tr` → 不做任何其他操作；`scale = 1`；软键盘关闭；妙控不摆上桌面。 |
| 步骤 | **①** 逐条执行 [PHASE4-SOP P4-A](PHASE4-SOP.md) 的 S1–S8 步进（**引用其步骤表，本文不复制其判据数字、不改期望**），每步按 P4-A 的 Capture 点按一次 `manual`。**②** 收起全部键盘（若 S 序列结束时软键盘仍在，先用系统收起，确认 `raw ≤ 1` 且 `occlusion = 0`）→ `Capture`（baseline 起点）。**③** 按 `Start continuous`（1000ms periodic）→ **静置 ≥30 s**，期间**不碰屏幕、不接妙控、不旋转**。**④** `Stop continuous` → `Capture`（baseline 收尾）。 |
| Capture 点 | ① S1–S8 各 1 次（同 P4-A）；②③④ 各 1 次（共 3 次）。**每段起止各一次 `Capture` 自查 `vv.scale`**。 |
| scale 要求 | 全程 `scale = 1`；开跑前 / 结束后各一次自查，**两次须同为 1**，否则该段按污染帧处置并重跑该段。 |
| 妙控 | **禁接**（不摆上桌面）。 |
| 期望 | ① **P4-A 段整段继承 PHASE4-SOP P4-A 的"期望"与"判负规则"原判**（本文**不重写其判据数字**，含地板数字也不在此复述为门线）。② **真空段**：`vv.offsetTop = 0` 的帧数 = 真空段总帧数（**periodic 帧 pan 数 = 0**）；`occlusion = 0`；全部 `ptyHosts[].inlineBottom == "0px"`、`ptyHosts[].visible` 仍为前台宿主；`document.scrollTop = 0` 且 `scrollingElementScrollTop = 0`；`app.inlineHeight` 为空；`|layoutGap| ≤ 1`；`xtermRows.bottom ≤ vv.height + 1`。 |
| 判负规则 | ① 同 PHASE4-SOP P4-A **原判**（本文不新增、不放宽、不收紧）。② 真空段出现**任一** periodic 帧 `vv.offsetTop ≠ 0` 连续 ≥2 帧 → **登记为"静置自发 pan"帧事实**，归 [PLAN §16.2 `KNOWN-P4-PAN`](PLAN.md#162-已知问题-2known-p4-pansafari-pan-伴生位移-维持接受不修) 的**已知问题登记类**（**不新增产品 FAIL**），但**不得**写成 PASS；真空段出现 `occlusion ≠ 0` 或宿主 `inlineBottom ≠ "0px"` 且无键盘事实 → **FAIL**。真空段总时长 < 30 s 或 periodic 帧数 < 28 → **INCONCLUSIVE**。 |
| 证据要求 | raw 连 seq（真空段**不得裁剪**，单独另存派生段时写明取自哪个原始文件、起止 seq）；P4-A 段与真空段各一张"面板先 Move 离底部"的真机截图（**P4-A 段的截图是本轮整改重点，见 §2.2**）；`node evidence/verify-sop.mjs --file evidence/raw/<id>.jsonl` 退出码只对 P4-A 段作参考（真空段按上表逐帧人读）。 |

### P5-Z1 Z-PINCH（双指缩出判据前提校验）

| 项 | 内容 |
| --- | --- |
| 目的 | 校验 D2 的**来源前提**：owner 口径②称"`scale > 1` 双指可以缩小，释放后必定回 1"、③称"持续 `scale < 1` 只由妙控 / 外接键盘或浏览器菜单指令产生"。本场景用**双指从 `scale = 1` 持续缩出**，检验"**双指能否留下持续 `scale < 1`**"这一前提，并记录**释放后是否回 1**。**本场景不判 enter / re-enter**（D2 门线不在本场景判）。 |
| 前置会话状态 | `scale = 1`；软键盘**关闭**（无键盘，`raw ≤ 1`、`occlusion = 0`）；**无快捷键接入**；`Start continuous`；面板落位 `tr`。 |
| 步骤 | **①** Capture 起点（登记 `vv.scale = 1`、`raw`、`occlusion = 0`）。**②** 双指在终端区**持续缩出**（pinch-in），目标让 `vv.scale` **稳入 <1**（如 0.9 / 0.75），**保持手指不放 ≥2 s**；期间记录 `vv.scale` 首次 <1 的帧 seq 与稳定读数。**③** **释放双指**（同一帧序列内），**不点任何其他元素**，静置 ≥2 s（覆盖 ≥3 个 periodic tick）。**④** 若释放后 `vv.scale` 读到 1，**结束**；若落到并**保持** <1，静置 ≥2 个 500ms tick 后 Capture 一次（settle 后重判），记录 `scaleBelowOneSettled` 首次为 `true` 的帧 seq 与时刻。**⑤** `Stop continuous` → Capture 收尾。 |
| Capture 点 | ① 起点 1 次；② 缩出稳定后 1 次（`manual`）；③ 释放后 1 次（`manual`）；④ 每多静置一轮 1 次；⑤ 收尾 1 次。**起止各一次 `vv.scale` 自查**。 |
| scale 要求 | 目标档 `1 → <1`；**本节"是否可达 <1"本身就是判据**，不可达按"观测否证"登记；收尾**必须回 1** 才可继续后续场景。 |
| 妙控 | **禁接**（本场景的意义就是"纯双指"，接入会污染来源）。 |
| 期望 | ① 记录**双指能否让 `vv.scale` 稳入 <1**（可达 / 不可达，附读数与 seq）；② **释放后 `scale` 必须回 1**（owner 既有口径，PLAN §4 D2 来源条款 ②）；③ 若双指可达 <1 且**释放后保持 <1**，须记录**首个 `scaleBelowOneSettled = true` 的帧**（`scaleSettle*` 链完整：`scaleSettleCandidate` 递增至 2 tick、`scaleSettleBaseline` 在该段固定）；④ 无键盘帧全程 `occlusion = 0`、全部宿主 `inlineBottom == "0px"`（**缩小但无键盘不得误让位**，D2 已签署"无键盘 / 无遮挡不得仅因 scale<1 抬高终端"）。 |
| 判负规则 | **`vv.scale` 双指不可达 <1** → **该前提被观测否证，登记**（**不判 FAIL、不判 PASS**，来源前提的正式记录）；**可达 <1 但释放后落到并保持 <1** → 按 **D2-PINCH "释放后不回 1" 记该子项 FAIL**（D2 签署反例）→ **立即停跑后续 zoom 场景，走 §2.3 的 D2 重开路径**；**可达 <1 且释放后回 1** → 本子项 **PASS**（仅此子项）；无键盘帧出现 `occlusion ≠ 0` → **FAIL**；**enter / re-enter 本场景不判**，若出现避让帧只作登记；`vv.scale` 全段无读数或 `scaleSettle` 整块缺失而结论依赖 settle → **INCONCLUSIVE**。 |
| 证据要求 | 缩出稳定帧与释放后帧各一张截图（面板 Move 离底部 / 离手势区）；raw 连 seq（**缩出与释放不得裁剪**）；`scaleBelowOneSettled` 首次为 true 的帧号 + 当时 `scaleSettleTicked` / `scaleSettleBaseline` / `vv.scale` 一并记录。 |

### P5-Z2 Z-MENU（浏览器菜单 / URL 缩小到 75%）

| 项 | 内容 |
| --- | --- |
| 目的 | 按 D2 细则 2 / 3 / 4 与 F13 / F14，验证"**菜单缩小（键盘未开）不误让位 → scale<1 下开键盘按 25% / 50% 线判 enter → 回 1 回程付 0 且无新恢复锁 → 再回 0.75 开键盘（锁 D-C settle 全程）**"这条链，重点是 **`scaleSettle*` 链条完整**与 **`occlusion == raw`**。 |
| 前置会话状态 | 新开会话段，`scale = 1`；**键盘未开**；P5-Z0 的基线可信（真空段无自发 pan）；`Start continuous`。 |
| 步骤 | **①** Capture（`scale = 1`、无键盘、`occlusion = 0`）。**②** 用 **Safari 菜单 `aA` / URL 栏缩小**（**两档**，目标 `~0.75`），**键盘不开**；等 settle 完成（≥2 tick id）；Capture。**③** 对照 P5-Z0 baseline 观察：`occlusion = 0`、宿主 `inlineBottom == "0px"`、`vv.offsetTop = 0`（**D2-3 / 3b 对齐：缩小但无键盘不得误让位**）；Capture。**④** 点终端 editable 唤起软键盘（**仍 `scale<1`**）→ 等 settle → Capture；按 **25% / 50% 进入 / 重入线**判是否 enter，`occlusion` 必须 `== raw`（会话内全额）；Capture。**⑤** 用菜单 **缩回 `scale = 1`**（键盘保持开着）→ Capture **当帧**；等 settle（≥2 tick）→ Capture **settle 后**；判"回程帧付 0（`scaleStepNoLatchExit` 语义）且**无新恢复锁**"。**⑥** **再回 `~0.75`** 开着键盘 → 等 settle 全程（记录 `scaleSettleCandidate` 2 tick 链）→ Capture；判键盘开着且已 settle 的帧 `occlusion == raw`。**⑦** `Stop continuous` → Capture 收尾。 |
| Capture 点 | ①②③④⑤⑥ 各 1 次（⑤ 两次：当帧 + settle 后）；⑦ 收尾 1 次。**起止各一次 `vv.scale` 自查**。 |
| scale 要求 | 声明档位 `1 → ~0.75 → 1 → ~0.75`；**每帧的 `vv.scale` 必须落在其步骤声明档位**（偏差 → 该帧按污染帧处置，不出 verdict）；**精确 0.75 达不到时如实记录实际值**（PLAN §4 D2"不能达到精确 0.75 时该精确锚点 INCONCLUSIVE，其他实际 scale<1 子项仍按实测值判读"）。 |
| 妙控 | **禁接**（不摆上桌面）。 |
| 期望 | ① 无键盘缩小段：`occlusion = 0`、`ptyHosts[].inlineBottom` 全 `"0px"`、`vv.offsetTop = 0`。② 键盘在 scale<1 下打开：**期望帧的 `scaleSettle*` 链条完整**——`scaleSettleCandidate` 递增至 2 tick、`scaleSettleBaseline` 在该段固定、`scaleSettlePending` 在 **进入之前的过渡帧** 为 `true`；`scaleBelowOneSettled = true` 之后才可出现进入；进入后 `occlusion == raw`。③ 回 `scale = 1` **当帧付 0**（`occlusion = 0`，不得按新 raw 让位）、**不新置恢复锁**（回 1 后 settle 完成时按未锁 25% 线重判）；settle 后按当前几何**重新判**。④ 再回 `~0.75` 后：`scaleSettleCandidate` 从 0 重建、走完 2 tick 才 settled，settled 帧 `occlusion == raw`。 |
| 判负规则 | **任何"未 settle 就进入"**（`scaleSettleTicked < 2` 的帧出现 `occlusion > 0`）= **FAIL 候选**（按 D2 细则 3 判 FAIL）；**停止期（无键盘 / 无 `raw>1`）`occlusion ≠ raw`** = **FAIL 候选**；**键盘开着且已 settle 的帧 `occ ≠ raw`** → **显式登记失败**（含 **147 / 105.3 类两份**：`raw = 147` 档与基于 105.3 的历史地板档，**两份都按"occ 必须 == raw"当帧判**，**不得**以地板/中间带解释为通过——**105.3 只作历史代际说明，不作门线**）；回 1 当帧出现 `occlusion > 0` = **FAIL**（回程未付 0）；**settle 链缺失 / `scaleSettle` 整块 `null`** 而结论依赖 settle = **INCONCLUSIVE**；缺 `vv.scale` 自查 → **INCONCLUSIVE**。 |
| 证据要求 | 每档一张截图（面板先 Move 离**键盘顶边**；缩小状态与键盘状态均须在截图里可见或被如实标注"未入镜"）；raw 连 seq；**settle 链**逐帧列出（`scaleSettlePassSeq` / `scaleSettleTicked` / `vv.scale` / `raw` / `occlusion` 五列，**相邻 scale 一并记录**以供 D2 细则 3 的 ε 复核）；本场景为 **D2 正式验收会话**，须满足"唯一 `zoomSource` 标注 + 连续 seq + 手动 Capture + 同场截图 + settle 后稳定帧"五项。 |

### P5-Z3 Z-MAGIC（妙控 / 外接键盘 `⌘+⇧+` 或 `⌘-`）

| 项 | 内容 |
| --- | --- |
| 目的 | **与 P5-Z2 完全相同的判定链**，唯一差别是 **zoom 来源换成妙控 / 外接键盘的 `⌘-` / `⌘+⇧++`**（D2 来源条款 ③ 指定的唯一合法第三方来源）；并记录 **zoomSource 可识别点**（`scaleSettlePassSeq` 起点与 zoom 事件帧）。 |
| 前置会话状态 | `scale = 1`；键盘未开（先按步骤开妙控，**再**开缩放）；`Start continuous`；**外部键盘已连接并等 2 秒**。 |
| 步骤 | **①** 接入妙控 / 外接键盘 → **等 2 秒** → Capture（登记接入本身是否改变 `vv` / 焦点事实；**仍 `scale = 1`**）。**②** 记录 **zoomSource 可识别点**：`scaleSettlePassSeq` 在此前为「无候选」或旧值，随后出现的**第一个 zoom 事件帧**（`vv.scale` 首次 ≠ 1 的帧）即来源锚点；截图 + seq 一并登记。**③** 键盘 `⌘-`（或 `⌘+⇧++`）缩小两档到 `~0.75`，**不点终端**（键盘未开）→ 等 settle → Capture。**④** 点终端唤起软键盘（仍 `scale<1`）→ settle → Capture；`occlusion == raw`。**⑤** `⌘+0` / 菜单缩回 `scale = 1`（键盘开着）→ **当帧 Capture** → settle → Capture；判回程付 0、无新恢复锁。**⑥** 再 `⌘-` 回 `~0.75` 开键盘 → settle 全程 → Capture。**⑦** `Stop continuous` → Capture 收尾（妙控可留场，须记录）。 |
| Capture 点 | ①②③④⑤（两次）⑥⑦ 各 1 次。**起止各一次 `vv.scale` 自查**。 |
| scale 要求 | 同 P5-Z2（`1 → ~0.75 → 1 → ~0.75`），每帧须落在声明档位。 |
| 妙控 | **必须接**（步骤 ① 接入并等 2 秒；不要求中途断开，但**末帧须记录妙控在场状态**）。**无外部键盘 → 本场景记「未执行」(INCONCLUSIVE)，不得替代生成**（§前置条件第 6 条）。 |
| 期望 | 判定链**逐条同 P5-Z2 的"期望"**（本文不重写其数值口径），另加：**zoomSource 锚点可识别**（zoom 事件帧 seq + 前后 `scaleSettlePassSeq`）、**无键盘缩小段 `occlusion = 0`**、**settled 帧 `occlusion == raw`**。**owner 已知形态（PLAN §1.3）**：妙控**浮窗**形态下 `rawShortfall` 恒 0 ⇒ **浮窗期键盘 `raw = 0` 时"不进入避让"是正确结果，不是 FAIL**。 |
| 判负规则 | 同 P5-Z2 **原判**（未 settle 就进入 / 停止期 `occ≠raw` / 键盘开着已 settle 帧 `occ≠raw`（147 / 105.3 两档均判）/ 回 1 当帧付 0 失败）。**专属**：浮窗期 `raw = 0` 而 `occlusion = 0` **不判 FAIL**（契约解释上的正确结果），但**必须逐帧登记 `raw` 读数**以证明是浮窗形态而非遮挡缺失；**zoomSource 锚点不可识别**（无 seq / 无唯一来源标注）→ **INCONCLUSIVE**；妙控未接 → 未执行（INCONCLUSIVE）。 |
| 证据要求 | 同 P5-Z2；**另附 zoomSource 锚点截图 1 张**（zoom 事件帧当刻）；浮窗形态若在场须在截图可见或被标注"浮窗未入镜"；raw 连 seq。 |

### P5-Z4 Z-PINCH 反向（`scale > 1` 双指放大）

| 项 | 内容 |
| --- | --- |
| 目的 | 验证 D2 的**保守豁免**方向：`scale > 1` 全帧 opt-out（F13"`scale > 1` 保守 opt-out"、D2 细则 3"scale>1 域不累计候选"）。目标是在**键盘开着**的状态下双指放大，观测是否**不进入避让**。 |
| 前置会话状态 | `scale = 1`；**键盘开着**且已让位（`occlusion` 与 `raw` 一致）；`Start continuous`。 |
| 步骤 | **①** 开键盘让位成立 → Capture（登记 `occlusion` / `raw` 基线）。**②** 双指在终端区**放大**至 `scale > 1`（如 1.2）→ **保持**期间 Capture（放大中）；记录 `vv.scale` >1 的首帧 seq。**③** **释放**双指 → Capture（释放当帧）。**④** 等 `scale` 回 1 → settle（≥2 tick）→ Capture；判键盘是否**恢复交付 raw 避让**（重新让位、`occlusion == raw`）。**⑤** `Stop continuous` → Capture 收尾。 |
| Capture 点 | ①②③④⑤ 各 1 次。**起止各一次 `vv.scale` 自查**。 |
| scale 要求 | 声明档位 `1 → >1 → 1`；**`scale > 1` 的帧按声明档位可判**，但 settle 口径在 >1 域**不开启候选**（`scaleSettleCandidate` 应为 0 或不累计）。 |
| 妙控 | **禁接**（与 P5-Z1 对称，保持"纯双指"）。 |
| 期望 | ① **键盘开着的 `scale > 1` 帧应立即 opt-out**：`occlusion = 0`、全部宿主 `inlineBottom == "0px"`（host bottom 0px）、**不得进入避让**（D2 保守豁免）；`scaleSettleCandidate` 在 `scale > 1` 域**不累计**（若累计 → 登记违反 D2 细则 3）。② 释放后 `scale` 回 1 → settle 完成 → **重新判**：键盘仍在、`raw > 1` 且满足门线时**重新让位**，`occlusion == raw`。③ 全程 `vv.offsetTop = 0`（持续 pan 按 §2.1 停止型处置）。 |
| 判负规则 | **任何"`scale > 1` 内部正常起步 / 释放时触发了进入"的帧**（`scale > 1` 帧出现 `occlusion > 0`）= **FAIL 候选**（按 F13 / D2 细则 3 判）；回 1 settle 后**未重新让位**且几何满足门线 → **FAIL**（滞后链）；`scale > 1` 域 `scaleSettleCandidate` 累计 → 登记违反并判 **FAIL**；缺 `vv.scale` 或 `scaleSettle` → **INCONCLUSIVE**。 |
| 证据要求 | 放大中与释放后各一张截图（面板 Move 离键盘顶边 / 离手势区）；raw 连 seq；须列出 `scale > 1` 域每一帧的 `vv.scale` / `occlusion` / `ptyHosts[].inlineBottom` / `scaleSettleCandidate`。 |

### P5-Z5 A0 0.75 必测（两线同越的重入）

| 项 | 内容 |
| --- | --- |
| 目的 | 在 **`scale = 0.75`** 下制造**深键盘**，产生"**进入 → 收缩 → 快速重开 keyboard**"的**两线同越重入**记录（**登记 only，不得据它反推门线**，PLAN §1.3 / 契约 F 表"本机 25% / 50% 判别带不可达"）。A0 的 153 帧 0.75 历史素材在此**转为必测锚点**（PLAN §4 D2"签署后必测，不是追溯 PASS"）。 |
| 前置会话状态 | 已按 P5-Z2 / P5-Z3 稳定在 `scale ≈ 0.75`；`Start continuous`；键盘未开。 |
| 步骤 | **①** 确认 `vv.scale ≈ 0.75` 并 Capture（记录实际读数，非 0.75 时如实登记）。**②** 点终端唤起**深键盘**（让 `raw` 同时越 25% 与 50% 两线：**第②轮 observed raw 788 > 1108 × 50% ⟂ 274 > 748 × 25%**——**这两组数字是"本机深键盘确实同越两线"的观测记录，不是门线推导**）→ settle → Capture。**③** 收起键盘（或让其回落到 `raw ≤ 1`）→ **快速重开** keyboard → Capture（**重入帧**）；记录 `unlockTrace` / 恢复锁状态 / `occlusion` / `raw`。**④** 用菜单回 `scale = 1`（键盘开着）→ Capture **回程当帧**，记录 **D-D 一步**（回程付 0、`zoomReturnFramePending`、settle 重判）。**⑤** `Stop continuous` → Capture 收尾。 |
| Capture 点 | ①②③④⑤ 各 1 次。**起止各一次 `vv.scale` 自查**。 |
| scale 要求 | 声明档位 `0.75 → 1`；`0.75` 若达不到，**记录每一次实际达到的 scale**，该精确锚点 INCONCLUSIVE，**其他实际 scale<1 子项仍按实测值判读，不得伪称 0.75 或把整条会话作废**。 |
| 妙控 | **禁接**（本场景只做深键盘 + scale 档位，接入会引入浮窗 `raw = 0` 干扰）。 |
| 期望 | ① 深键盘在 settled `scale<1` 下按冻结 §5 其余规则**正常进入**（`occlusion == raw`），**重入帧**的锁 / 门线状态**如实记录**；② 回 `scale = 1` **当帧付 0**（D-D 一步：`occlusion = 0`、`zoomReturnFrame` 退出、不新置锁），settle 后按当前几何重判并与重入前记录**分开登记**。 |
| 判负规则 | **不得据两线同越的重入反推 25% / 50% 门线或恢复锁已获真机证明**——违反即该子项 **INCONCLUSIVE**（PLAN §1.3 明文）；深键盘 settled 帧 `occ ≠ raw` → **FAIL**（同 P5-Z2）；回 1 当帧 `occlusion > 0` → **FAIL**（回程未付 0）；`0.75` 未达 → 该锚点 INCONCLUSIVE（**不判 FAIL**）。 |
| 证据要求 | 0.75 档与键盘深档各一张截图；raw 连 seq；门线段落只写"登记 only"并引 PLAN §1.3；**禁止**在验收记录里写"25% / 50% 门线真机已验证"。 |

### P5-L1 窗口 / Split View / Stage Manager（F12 / F20）

| 项 | 内容 |
| --- | --- |
| 目的 | 按 [PHASE1-CONTRACT](PHASE1-CONTRACT.md) **F12**（旋转）与 **F20**（窗口尺寸变化 / Split View / Stage Manager），记录方向与窗口形态变化下的归属重判。 |
| 前置会话状态 | `scale = 1`；`Start continuous`；键盘状态按轮次分：**轮 1 无键盘 / 轮 2 键盘开着**。 |
| 步骤 | **①** 无键盘旋转竖↔横 **×4**（竖→横→竖→横→竖），每方向 Capture。**②** 键盘开着旋转竖↔横 **×2**，每方向 Capture，**两方向各等 settle 后再 Capture 一次**。**③** 若设备支持：开 **Split View** / **Stage Manager**，记录**形态名**与是否 B 会话在场，各 Capture 一次。**④** `Stop continuous` → Capture 收尾。 |
| Capture 点 | ①② 每方向 1 次（轮 2 每方向 2 次）；③ 每形态 1 次；④ 收尾 1 次。**每轮起止各一次 `vv.scale` 自查**。 |
| scale 要求 | `scale = 1` 基线档；**形态切换后若出现非 1，按污染帧处置并如实标注**（本场景**不测** zoom）。 |
| 妙控 | **禁接**。 |
| 期望 | **引 PHASE1-CONTRACT 原文，不重写**：F12——"归属类别不变则保持，但**必须重新确认**"前台实例、焦点宿主、键盘是否仍在、quick-keys 开合与新的可用视口；"不得沿用旧方向的判据或让位值；几何按新窗口重算；面板在新方向下仍须在盒内可达（GI-4）"。F20——"形态变化本身不自动改判归属，以**新焦点落点**为准；必须重新确认归属"，并重新确认前台实例、焦点宿主、软键盘避让会话是否仍 active、`vv.scale`、quick-keys 开合；"不得沿用旧窗口形态的判据或让位值"。**settle 宽容照 §14.2 簇 D 口径**（转向首帧少让位属 [§16.1 `KNOWN-P4-SEQ-GRACE`](PLAN.md#161-已知问题-1known-p4-seq-grace转向首帧少让位-接受不修) 已接受的已知问题，**不新增 FAIL**）。 |
| 判负规则 | **轮 1（无键盘）与轮 2（键盘开着）的旋转按 F12 / F20 的"宽容记录"处置**：仅当出现 **§2.1 的停止型 FAIL**（持续 pan / 终端出屏不可恢复）才判 FAIL；**Split View / Stage Manager 形态只记录形态名，不判 verdict**（帧记录 only）；**无旧恢复状态**若被观测违反（新方向沿用旧 `occlusion` 超出 settle 宽容窗口）：轮 1 / 轮 2 的旋转各记 **INCONCLUSIVE**（按 §16.1 归已知问题类，**不判 FAIL、不写 PASS**）；缺形态记录 → INCONCLUSIVE。 |
| 证据要求 | 每个方向 / 形态各一张截图（面板先 Move 离**当前方向下的键盘顶边**）；raw 连 seq；**形态名字样**（Split View / Stage Manager / 全屏）逐次登记。 |

### P5-L2 前后台 / 刷新（F15 / F16）

| 项 | 内容 |
| --- | --- |
| 目的 | 键盘开着时 **切到主屏幕再回 Safari**、以及 **刷新页面**，观测归属重判、`raw` / 条款字段与是否 pan；顺带 `probe.visualBottom` 缺帧处置（同 PHASE4-SOP P4-Z）。 |
| 前置会话状态 | `scale = 1`；键盘开着让位态；`Start continuous`。 |
| 步骤 | **①** 键盘开着让位（Capture）→ **②** 按 Home / 上滑**切到主屏幕**（等 ≥2 s）→ **③** 回到 Safari → **立即 Capture**（归属重判帧）；再等 settle → Capture。**④** 键盘开着 → **刷新页面**（`⌘R` / 菜单重载）→ **立即 Capture**（refresh 后首帧的 `raw` / 条款字段）→ 等 settle → Capture。**⑤** 刷新后若键盘未自动恢复，**不点任何元素**先 Capture 一次（记录失败形态），再按需重点一次终端解锁并记录（该帧单独标注）。**⑥** `Stop continuous` → Capture 收尾。 |
| Capture 点 | ①②③④⑤⑥ 各 1 次（③ 两次）。**起止各一次 `vv.scale` 自查**。 |
| scale 要求 | `scale = 1`；前后台 / 刷新后若出现非 1，按污染帧处置。 |
| 妙控 | **禁接**。 |
| 期望 | 切回 Safari 后归属**按当前焦点重判**（`ownership` / `activeInput` 同帧可读、类别正确）；全程 `vv.offsetTop = 0`（若出现 pan，按 §2.1 停止型处置）；刷新后帧的 `raw` / `occlusion` / 宿主 `inlineBottom` **按刷新后的实际焦点与几何重算**，不得沿用刷新前值。 |
| 判负规则 | 切回首帧出现**持续 pan** 或**终端出屏不可恢复** → 停止型 FAIL；**"键盘打开时刷新，键盘层第一次恢复失败"若属 Safari 层 → 登记 only**（不作产品 FAIL，须在证据里写明"首次恢复失败 = Safari 层行为"）；刷新后 `occlusion` / 宿主底沿用刷新前值且无对应重算事实 → **FAIL**；**`vv = 768` 等"键盘打开布局 > clientHeight"的过桥期间缺 `probe.visualBottom` 的帧** → **该帧 INCONCLUSIVE**（照抄 PHASE4-SOP P4-Z 处置，见 §4）；缺归属字段 → INCONCLUSIVE。 |
| 证据要求 | 切回与刷新后各一张截图（面板先 Move 离底部）；raw 连 seq；`probe.visualBottom` 缺帧**逐帧列 seq**。 |

### P5-L3 实例切换 / 隐藏宿主（F8 / F11 / GI-8 / D3）

| 项 | 内容 |
| --- | --- |
| 目的 | 两个实例（1 现役 + 1 隐藏宿主）间键盘聚焦切换来回若干次，采集"切回首帧"的**补充帧**。**本轮已有 §16 的 `KNOWN-P4-PAN` / `KNOWN-P4-SEQ-GRACE` 登记，本场景只作补充帧采集、不判 FAIL、不重开问题。** |
| 前置会话状态 | 实例 A（现役）与实例 B（隐藏 / 非前台）；`scale = 1`；键盘开着（聚焦在 A 终端）；`Start continuous`。 |
| 步骤 | **①** A 的终端开键盘让位 → Capture。**②** 切到实例 B（B 成为前台）→ Capture（记录 `ptyHosts[].visible` 变化事件帧）。**③** **切回 A → 立即 Capture**（靠 continuous 连续帧抓首帧）。**④** 重复 ②③ **共 3 个来回**（A→B→A ×3），每来回各 2 次 Capture。**⑤** 把 B 的宿主隐藏（或切换为隐藏）→ Capture。**⑥** `Stop continuous` → Capture 收尾。 |
| Capture 点 | ①②③ 每个来回 1 次；⑤⑥ 各 1 次。**起止各一次 `vv.scale` 自查**。 |
| scale 要求 | `scale = 1`；出现非 1 按污染帧处置。 |
| 妙控 | **禁接**。 |
| 期望 | 每次切换记录"**宿主 A ↔ B**"与 `ptyHosts[].visible` **事件帧**（seq + 变化）；切回首帧按 [PHASE1-CONTRACT](PHASE1-CONTRACT.md) **GI-8** 与 [E 表 **D3**](PHASE1-CONTRACT.md#e-d1d4-签署状态) 的**等价判据**观察（"切回旧实例的首帧既不得出现旧键盘高度对应的让位残留，也不得出现未让位导致的键盘压终端"），**残留即记补充帧事实**。 |
| 判负规则 | **本场景不判 FAIL、不重开问题**：所有偏离一律登记为**补充帧**（引 §16 `KNOWN-P4-PAN` / `KNOWN-P4-SEQ-GRACE`）；**持续 pan 或终端出屏不可恢复**才按 §2.1 **停止型 FAIL** 处置（该红线不受 §16 登记豁免）；`ptyHosts[].visible` 缺失 → **INCONCLUSIVE**（不得以角色启发式推断可见性）。 |
| 证据要求 | 每个来回一张"切回后"判读截图（面板先 Move 离底部）；raw 连 seq，**首帧必须落在 continuous 段内**；`ptyHosts[].visible` 变化事件列表（seq + A/B）。 |

### P5-L4 IME（F19）

| 项 | 内容 |
| --- | --- |
| 目的 | 按 [PHASE1-CONTRACT](PHASE1-CONTRACT.md) **F19** 观察：终端内**组合态**（未上屏）期间的可见性、**组合中焦点被本页抢走**的帧、组合中末行可见性；**"组合文本不丢"属事实陈述（F19），不判 FAIL**。 |
| 前置会话状态 | `scale = 1`；键盘开着；终端持焦；**切中文输入法**；`Start continuous`。 |
| 步骤 | **①** 切中文输入法 → 在终端内**组合态连续打字**（拼音未确认上屏）→ Capture。**②** 组合中**点入本页表单 / quick-keys 粘贴框**（抢走焦点）→ Capture（记录抢焦帧与归属重判）。**③** 回到终端，组合态下**提交（上屏）**或**不拆直接收起**（二选一，须写明选了哪个）→ Capture。**④** `Stop continuous` → Capture 收尾。 |
| Capture 点 | ①②③④ 各 1 次。**起止各一次 `vv.scale` 自查**。 |
| scale 要求 | `scale = 1`；出现非 1 按污染帧处置。 |
| 妙控 | **禁接**。 |
| 期望 | 组合进行中**逐帧**记录（`reason` / `occlusion` / `xtermRows.bottom` / `ownership` / `activeInput`）；**组合中末行可见性**作为观察量记录；**组合中抢焦后归属按新焦点落点重判**（F19：HO-2 → HO-3 / HO-4 / HO-1）；**不得把未上屏的组合文本当作已提交输入驱动几何或行数判定**。 |
| 判负规则 | **"组合文本不丢 / 保留"不判 FAIL**（F19 明文：只约束归属判定不被组合态冻结，不承诺组合内容保留策略）；**不得为组合窗口占位追加终端避让**——若出现因组合窗口新增的 `occlusion` 增量 → **FAIL**；**持续 pan / 终端出屏不可恢复** → 停止型 FAIL；缺归属字段或抢焦帧未覆盖 → INCONCLUSIVE。 |
| 证据要求 | 组合态中与抢焦帧各一张截图（面板先 Move 离键盘顶边）；raw 连 seq；**输入法名**与是否上屏须写明；**不记录终端文本内容**（隐私）。 |

### P5-L5 触摸滚动 / 内部滚动器（S8 延续）

| 项 | 内容 |
| --- | --- |
| 目的 | 延续 A0-SOP **S8** 的"触摸滚动"面，验证**根 `scrollTop` 必须恒 0**、内部滚动器可用。 |
| 前置会话状态 | `scale = 1`；键盘状态按分项：**终端内拖拽**（键盘开着）；**侧栏 / 本页列表 / 弹窗内**（键盘关闭）。`Start continuous`。 |
| 步骤 | **①** 终端内手指**拖拽滚动**（xterm 内部滚动器）→ Capture。**②** 侧栏手指滚动 → Capture。**③** 本页任何列表滚动（工作区 / 实例列表）→ Capture。**④** 弹窗内滚动 → Capture。**⑤** 每项后回到原状再进下一项（避免状态叠加）；`Stop continuous` → Capture 收尾。 |
| Capture 点 | ①②③④ 各 1 次；⑤ 收尾 1 次。**起止各一次 `vv.scale` 自查**。 |
| scale 要求 | `scale = 1`；出现非 1 按污染帧处置。 |
| 妙控 | **禁接**。 |
| 期望 | ①②③④ 全段 `document.scrollTop = 0` 且 `document.scrollingElementScrollTop = 0`；内部滚动器可用（终端自身滚动呈现、列表自身 scrolled，肉眼）；`vv.offsetTop = 0`；`rootScrollHeight` 不因滚动增长。 |
| 判负规则 | **任何"根跟着走"（`document.scrollTop > 0` 或 `scrollingElementScrollTop > 0`）= FAIL**（**仅纯内部滚动除外**——内部滚动器自身滚动不算根滚动）；持续 pan / 终端出屏不可恢复 → 停止型 FAIL。 |
| 证据要求 | 四项各一张截图；raw 连 seq；根滚动读数逐帧列出（seq + `scrollTop` + `scrollingElementScrollTop`）。 |

### P5-L6 暗色 / 浅色 & safe-area

| 项 | 内容 |
| --- | --- |
| 目的 | 切换暗色↔浅色，并观察横屏时 `env(safe-area-inset-*)` 的外观，**登记仅作回归参照**（PLAN §6"暗色/浅色、safe-area：截图是否无默认白画布缝，几何与视觉证据是否一致"）。 |
| 前置会话状态 | `scale = 1`；一个让位态帧（键盘开着）与一个无键盘帧各一次切换。 |
| 步骤 | **①** 系统切**暗色** → Capture → 截图。**②** 系统切**浅色** → Capture → 截图。**③** **横屏**下切暗色 / 浅色各一次，重点观察 `env(safe-area-inset-*)` 区的外观（屏幕左右 / 底部边缘是否出现白画布缝）→ 各 Capture + 截图。 |
| Capture 点 | ①②③ 各 1 次。**起止各一次 `vv.scale` 自查**。 |
| scale 要求 | `scale = 1`；出现非 1 按污染帧处置。 |
| 妙控 | **禁接**。 |
| 期望 | **不判 verdict**：只记录 `theme` / `vv.height` / `vv.offsetTop` / `occlusion` / 宿主底与截图，作为**回归参照**（下一轮与本节对比）。 |
| 判负规则 | **只有 §2.1 停止型 FAIL 可触发判负**（持续 pan / 终端出屏不可恢复）；**不判 PASS、不判 FAIL 于视觉项**；缺截图 → INCONCLUSIVE。 |
| 证据要求 | 暗 / 浅 × 竖 / 横 共 4 张截图（最低竖 2 张）；raw 连 seq。 |

### P5-I1 iframe 父页面无干预

| 项 | 内容 |
| --- | --- |
| 目的 | 除 dsh 外，在 **opencode / reasonix / dsh** iframe 内打字 / 滚动（**焦点进 iframe**），验证**父页面（本 shell）不干预**（HO-4；PLAN §6"opencode / reasonix / dsh iframe | 父页面是否保持无干预"）。 |
| 前置会话状态 | `scale = 1`；至少一个 iframe 实例（opencode / reasonix / dsh 中可得者，逐个）；键盘状态按场景（iframe 内输入）。 |
| 步骤 | **①** 焦点进 iframe → 在 iframe 内输入几个字符（**不记录文本**）→ Capture。**②** 在 iframe 内滚动 → Capture。**③** 点 iframe 外非输入区回到本 shell → Capture（记录 `focusout` 形态）。**④** 对可得的下一个 iframe 重复 ①②③。 |
| Capture 点 | 每个 iframe ①②③ 各 1 次。**起止各一次 `vv.scale` 自查**。 |
| scale 要求 | `scale = 1`；出现非 1 按污染帧处置。 |
| 妙控 | **禁接**。 |
| 期望 | **"父页面不干预"的判定 = 无 `focusin` 处理 / 无动作即可**：帧内 `activeInput.role = "iframe"`、`ownership` 为 HO-4（或按 F7 的落点），**本 shell 无额外避让 / 无几何写入**（`occlusion` 不因 iframe 焦点变化而改变）。 |
| 判负规则 | **只作帧记录**；若父页面**仅因 iframe 事件 panic / 记录异常帧** → **登记 only**（不判 FAIL）；**不得以 iframe 侧结果认证 HO-4 / GI-3 / GI-7 整行**（PHASE1-CONTRACT：iframe 侧子项维持 INCONCLUSIVE）；持续 pan / 终端出屏不可恢复 → 停止型 FAIL。 |
| 证据要求 | 每个 iframe 一张截图；raw 连 seq；`activeInput.role` / `ownership` / `occlusion` 逐帧列出。 |

### P5-D 收尾

| 项 | 内容 |
| --- | --- |
| 目的 | 全部场景结束后统一收尾，**交付截图链与 raw 归档**。 |
| 步骤 | **①** 全部场景跑完后 `Stop continuous`。**②** 最后一帧 `Capture`（收尾帧，记录结束 `vv.scale` / 妙控在场状态）。**③** 按 §2.2 的归档纪律整理 raw 与截图。 |
| Capture 点 | 收尾 1 次。 |
| scale 要求 | 记录收尾读数（不要求为 1，**如实登记**）。 |
| 妙控 | 记录收尾时是否在场。 |
| 期望 | raw 全量保留；文件名 `final116-<YYYYMMDD>-p5-<scenario>.jsonl`；每场景"面板移离"截图 1 张；**全程截图链是本 SOP 的必要交付**。 |
| 判负规则 | **截图链缺失 → 该场景 INCONCLUSIVE**。**上两轮 P4-A 综合结论 = INCONCLUSIVE 正是缺图片链（[PLAN §14.3](PLAN.md#143-整轮三值definitive) / §16.3）**；本 SOP **从第 1 场景（P5-Z0）起就带截图**，**不得采用"先采完再补图"的流程**——事后再补的图不认证当轮 verdict。 |
| 证据要求 | 场景 × 截图链清单表（`scenario` / `shotId` / 对应 seq 区间 / 移离方向）+ raw md5 清单。 |

---

## 2. 判负总表与归档纪律

### 2.1 判负总表

| 判定 | 触发条件 | 出处 |
| --- | --- | --- |
| **PASS** | 现役 `verify-sop` 25% / 50% 会话机的不变量（`shellShort` / `pinned` / `occlusion` / `host` / `residue` / `underKeyboard` / `controls` / `controlsUnder` / `statusBar` / `statusBarUnder` / `quickKeys` / `scroll` / `range` / `overflow` / `position` / `ink` / `rows`，见 `FAILURE_KINDS`）成立、**D2 `zoom_guard` 语义条款成立**、本场景"期望"列全部满足，且证据齐全（连续 seq、面板离底截图、字段在位、**D2 场景另有唯一 `zoomSource`**）。 | `verify-sop.mjs`；PHASE1-CONTRACT F 节通用规则 |
| **FAIL** | 任一 MUST 不变量在真机被观测不成立；或本场景"判负规则"明确列出的 FAIL（**含本文 §0 的 settle 口径**：未 settle 就进入、停止期 `occ ≠ raw`、键盘开着且已 settle 的帧 `occ ≠ raw`（147 / 105.3 两档均判）、回 1 当帧付 0 失败）。 | `verify-sop.mjs`；D2 细则 2 / 3 / 4 |
| **INCONCLUSIVE** | 缺字段 / seq 缺口 / 场景未覆盖 / 面板不可达 / 只有离线证据 / 污染帧未重置 / **缺外部键盘导致 P5-Z3 未执行** / **`scaleSettle` 整块缺失** / **缺截图链**。**缺证据不等于通过。** | PHASE1-CONTRACT F 节 |
| **停止型 FAIL** | ①**`scale > 1` 时出现进入（让位 / `occlusion > 0`）**；②**持续 pan + 终端出屏不可恢复**。触发即**立即停止**全部后续场景 + **截图收工** + **报调度者**，不做"抢回"（不做 `scrollTo(0,0)` / 反复写根 `scrollTop` / 反向 transform）。 | PHASE1-CONTRACT F17；PLAN §5 Phase 5 门禁；D2 细则 3 |

> **弹同节例 · 三值口径不变、锚点新增（本 SOP 专属）**：判定载体是**逐场景的三值**，**禁止跨场景推导**——P5-Z1 的 PASS 不认证 P5-Z2，P5-Z2 的 PASS 不认证 P5-Z3，P4 已登记的场景结论不回填本文。**`occlusion` / `ptyHosts[]` / `vv.offsetTop` / `scaleSettle*` / `ownership` / `activeInput` 六族字段缺任一而结论依赖它 → INCONCLUSIVE。**

### 2.2 复算与归档（**照抄 PHASE4-SOP 原文**）

- **对每轮原始会话独立复算**：`node evidence/verify-sop.mjs --file evidence/raw/<id>.jsonl`；**exit 0 / 1 / 2 = PASS / FAIL / INCONCLUSIVE**。一轮 raw 只复算它自己的场景，**不得跨轮拼接**。
- **原始不改写**：raw 逐字节保留，不裁剪、不重排、不重采样、不格式化；**md5 归档**（写在验收记录里），文件名与 md5 一起登记。
- **派生段与原始分开标识**：派生连续段（如 `…-p5-z2-continuous.jsonl`）另名保存，**不得覆盖原始**；引用"段"时必须写明取自哪个原始文件、起止 seq。
- **验收记录三层分开**：① 原始会话 `raw/*.jsonl`；② 派生连续段；③ 最终验收记录。**三层不得互相冒充**（PHASE1-CONTRACT 证据分级 / [DATA-COLLECTION](DATA-COLLECTION.md) §2）。
- **每场景一张"面板先 Move 离底部"的真机截图**：面板自己就压在它要证明的那条边上，截图前必须把面板**拖离该边**，**只拍面板外区域**；截图与 seq 同场绑定。
- **隐私**：只记录几何、有限设备元数据与 UI 状态；不记录 token、终端文本、worktree 名称 / 路径或实例 ID（DATA-COLLECTION 文首约定）。

### 2.3 D2 冲突处置（**本 SOP 新增，不改写 PHASE4-SOP 归档纪律**）

**触发条件（任一即触发）**：

- **P5-Z1 观测到"双指从 `scale = 1` 缩出 `scale < 1` 并保持"**——即 D2 来源前提 ③ 被正式证据反驳；**或**
- **P5-Z1 观测到"释放后不回 1"**——即 D2 来源前提 ② 被正式证据反驳。

**处置（固定顺序，不得跳步）**：

1. **立即停止全部后续 zoom 场景**（P5-Z2 / P5-Z3 / P5-Z4 / P5-Z5 一律不跑；已跑完的场景帧**保留但不据此更新 verdict**）。
2. **触发 D2 重开路径**（[PHASE1-CONTRACT E 表 **D2**](PHASE1-CONTRACT.md#e-d1d4-签署状态) 失败条款 / PHASE1-CONTRACT 术语章 D2 冲突处置：正式来源冲突或信号不可分 → 停止交付、重开 D2 回产品讨论并**单独回退**该实现），**交调度者 / owner 决议**。
3. **不得静默补阈值**（不改 25% / 50% / ε / tick 数）、**不得继续跑**、**不得把该帧解释成"操作误差"**、**不得改写 raw**。
4. 按 P5-D 收尾（`Stop` + 最后一帧 Capture + 截图链 + raw md5），报告里**单列"D2 冲突证据段"**（seq 区间 + 读数）。

> **弹同节例 · 与 PHASE4-SOP §2.1 的关系**：PHASE4-SOP 的停止型 FAIL 是**产品面**红线（持续 pan / 出屏）；本节是**判据面**红线（D2 前提被否证）。两者**都要"停 + 截图收工 + 报调度者"**，但**归口不同**：前者归 D1 / F17，后者归 D2 重开与 owner 决议。**同一轮若两者同时触发，先按本节停 zoom 场景，再按 §2.1 收工。**

### 2.4 时间线模板（每场景一份，跑完立即填；**照抄 PHASE4-SOP 原文，弹同节例**）

| session / 文件 | 场景 | seq 起–止 | 开跑前 scale | 结束 scale | 妙控状态 | 浮窗 / 分屏 | Capture seq / shotId | 污染段 seq（类型） | verdict |
| --- | --- | --- | --- | --- | --- | --- | --- | --- | --- |
| 待填 | P5-X | 待填 | 待填 | 待填 | 禁接 / 必须接 / 可接 | 待填 | 待填 | 无 / 待填 | 待填 |

> **弹同节例 · zoom 场景补充列（本 SOP 专属）**：P5-Z1 / Z2 / Z3 / Z4 / Z5 的时间线**额外**增加四列——**`zoomSource`**（双指 / 菜单 / 键盘 ⌘−，唯一标注）/ **`scaleBelowOneSettled` 首次 true 的 seq** / **`scaleSettlePassSeq` 起点** / **回程帧 seq（付 0 判定）**。缺这四列 → 该 zoom 场景 **INCONCLUSIVE**（契约 F 表：「每行须有唯一 `zoomSource`、连续 seq、手动 Capture、同场截图和 settle 后稳定帧」）。

---

## 3. 诚实边界（**结构照抄 PHASE4-SOP §4，内容按 Phase 5 换项**）

**本 SOP 执行产物只能登记每场景 PASS / FAIL / INCONCLUSIVE。**

- **D2 zoom 子项的"离线绿"不判真机 PASS**：`go test` / Node 用例 / Headless 只能证明工具链与离线逻辑，**真机 PASS 只由本 SOP 的具体场景帧为准**（PLAN §4 D2："Node / Go / Headless 不得宣称真机 PASS"）。实现侧离线绿**不**构成任何 zoom 场景的 PASS。
- **不整行关闭 R 项**：P5-Z2 / P5-Z3 的 PASS **不等于** D2 整行通过；P5-L1 的形态记录**不等于** F12 / F20 整行通过；P5-L4 的观察记录**不闭合** F19；P5-L3 补充帧**不闭合** GI-8 / D3。**R1–R6 一律维持未关闭**，直到各自整行证据闭环。
- **"`scale<1` 得以避让"是 D2 已签署目标，不因本 SOP 完成而关闭 #116**：本 SOP 只登记场景级 verdict；#116 的关闭权在最终验收（Phase 6）与 owner 决议。
- **不得以离线结果冒充真机**：`verify-sop.mjs --self-test`、`structural-check.mjs`、`pty-refit-check.mjs --self-test` 只证明工具链，不产生真机 PASS。
- **帧记录 only 的场景不产生 verdict 上浮**：P5-L1 的 Split View / Stage Manager、P5-L6 的暗 / 浅与 safe-area、P5-I1 的 iframe 父页面、P5-L3 的补充帧、P5-L4 的"组合文本不丢"，**只在验收记录里登记帧，不得写成 PASS 或 FAIL**（除非触发 §2.1 停止型 FAIL）。
- **`raw` = 0 的浮窗期不判遮挡 FAIL**：妙控浮窗期 `rawShortfall` 恒 0 是本机已知形态（[PLAN §1.3](PLAN.md#13-控件形态台账与本机可验性2026-11-05-下午)），"不进入避让"是契约解释上的正确结果，**不是**证据缺口，更不是 FAIL。

---

## 4. 与既有判据的关系

- **判据代际照 [PLAN §16](PLAN.md#16-phase-4-真机收口-owner-决定2026-11-07r4-p4-evid-0102-之后)**：现行判据 = 现役 `verify-sop` 25% / 50% 会话机 + D2 `zoom_guard` 语义；**A0-SOP 的 0.15 地板与 1–10 编号属历史代际**，只在解释"为何两档不可判"时提及（承 §14.1 既定口径），**不作门线**。
- **quickKeys / `lost` / `openHeight` / `expected` 是 `verify-sop` 派生旁证量**（[PLAN §14.2](PLAN.md#142-五簇判读)），**不得**当产品的"列高损失"读；本 SOP 的判负只用**帧内直接字段**，派生量只作旁证。
- **`vv = 768` 等"键盘打开布局 > clientHeight"的过桥期间探针缺 `probe.visualBottom` → INCONCLUSIVE**：照抄 [PHASE4-SOP P4-Z](PHASE4-SOP.md) 的处置（该帧不可读的页面测量字段一律不判，缺字段不是 PASS；`probeReadFailed: true` 帧同理）。
- **与 PHASE4-SOP 的边界**：PHASE4-SOP 的 P4-A..P4-I 场景与判据**原样有效**，本 SOP 的 P5-Z0 只**引用**其 S1–S8 步骤，**不复制、不重写**其判据数字；PHASE4-SOP §0 的"低倍率场景不属本文"由本文接管（见 §0），**但 PHASE4-SOP 原文不改写**。
- **`DATA-COLLECTION.md` §10 路线**：历史上承担 zoom 采集的独立单源会话路线**不改写、不重判**；本文是新代际的执行层 SOP，历史 raw 依其自身戳记归档。

---

**文档状态**：v1，执行层采证规范，**不授权任何实现、不改任何证据工具、不关闭任何 R 项或 #116**。执行前须由调度者确认硬件（§前置条件第 6 条）、构建戳记与归档路径；执行产物按 §2.4 时间线逐场景回填。

---

### Amend-1（2026-11-07，owner 决定，本 SOP 后续轮执行）
性质：纯追加修订注记，不回改既有行。

1. **zoom 单会话混合源 = 允许**：owner 决定 zoom 场景不按"每来源单独起会话"补采，一律按本 SOP 顺序成单会话；判读时对来源不可归因的帧按"owner 指认 + 行为子项帧级判读"处置，`zoomSource` 不再作为单源硬要求。
2. **Z3 来源已指认**：妙控/外接键盘 `cmd +/-`（无硬件帧化能力为探针边界，owner 指认即采证登记）。
3. **L1**：键盘开/关两态旋转均要求覆盖（此前轮次 owner 已按此执行）。
4. **L2 白话定义（入 SOP）**："键盘开着切主屏" = 终端正输入时按主屏手势离开 Safari 回 iOS 主屏/其他 App、再回 Safari——观察回到前台那一刻键盘与几何的重建、有无 pan（不是切换浏览器标签）。
5. **L4**：IME 组合态无 probe 可观测字段属工具边界；owner 操作记录（如输入"中文"无异常）为口头级证据，登记到判读，不判 PASS/FAIL。
6. **L6**：主题切换可能被 probe 的 `bodyBackground`（终端色）掩盖；判读按"owner 口头证据 + probe 盲区"登记。
7. **quick-keys**：无需为 Phase 5 补采（历史 phase 已多轮实测）；**owner 观察记录**（非缺陷候选）：粘贴后文本**直接进入 xterm、无编辑机会**——属 issue #100 quick-keys 的现有设计（粘贴立即发送）；留待 owner 后续指令。
8. **截图链要求取消**（owner 决定）：面板拖离底边的截图**不再是本轮交付或验收必要项**；视觉确认改为"owner 现场观察 + 帧级几何字段"；历史上因缺截图而 stuck 在 INCON 的 verdict 在下一轮判读中**该要求不再作为缺口**（以几何/帧字段 + owner 口径为准）。
9. 上述各项的判读处置一并适用于 PLAN §17（登记见后续 docs 提交，不在此节）。
