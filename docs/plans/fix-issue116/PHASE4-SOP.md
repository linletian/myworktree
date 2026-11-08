# Phase 4 真机验证 SOP（issue #116 · D3/D4 实现与 R1 真机门禁 · 采证与判负规则 v1）

**日期**：2026-11-07
**性质**：**执行层采证规范**。本 SOP 继承 [A0-SOP v3.1](A0-SOP.md) 的**满高壳 + 局部避让 + 比值地板 0.15** 契约与 `evidence/verify-sop.mjs` 不变量 1–10，把它们扩展到 [PLAN §5 Phase 4](PLAN.md) 的 **D3 / D4 / GI-7 / R1** 场景。**本 SOP 不修改 A0-SOP 任何判据**；凡本文与 A0-SOP 表述冲突，**以 A0-SOP 为准并停止回报**。本文只定义采集顺序、字段核对与 verdict 登记方式，**不授权任何产品实现、不修改任何 evidence 工具**。

**前置条件（逐条，缺一条该轮不得开跑）**：

1. **同一台 iPad**：Phase 4 门禁要求"同一 iPad 上证明 R1"（[PLAN §5 Phase 4 门禁](PLAN.md#phase-4pty-终端自有视口与-pan-处理)），故整轮矩阵必须在同一台设备上完成；记录型号与 iPadOS 版本。Safari **无缓存**（服务端已禁缓存），跑的是**工 HEAD 构建**（记录提交号）。
2. **诊断面板落位 `tr`（右上）**；**每步操作后按一次 `Capture`**（`manual` 帧）；需要连续 seq 的场景先按 **`Start continuous`**（1000ms periodic）再操作，结束后按 `Stop`。
3. **妙控键盘只在指定场景接入**（下表"妙控"列）：**禁接 = 不接且不摆上桌面**（避免意外浮层 / 键盘事件污染）；**必须接 = 按场景步骤接入并等 2 秒**；**可接 = 仅 P4-I 持续 pan 频度观察档可接**，其余场景非必需。**无障碍浮窗 / 分屏开启状态每场景记录**（形态字样 + 是否 B 会话 / Split View 在场）。
4. **浏览器并发 = 1**：整个 SOP 期间**不得开第二个标签页访问同一实例**；**不做任何 Chrome / CDP 并行采证**（AGENTS.md §4、[PLAN §7](PLAN.md#7-验证与资源纪律) 第 3 条）。
5. **令牌在 URL 内以认证 shell 打开**（`http://<host>:<port>/?token=***&viewport-debug=1`），令牌**外部传入、不落盘**。归档路径沿用既有 raw 命名惯例：`docs/plans/fix-issue116/evidence/raw/final116-<YYYYMMDD>-p4-<scenario>.jsonl`（`<scenario>` 取 `a` / `b` / `c` / `d` / `e` / `f` / `g` / `h` / `i`；同一场景多轮用 `-2` / `-3` 后缀，不覆盖上一轮）。

**适用构建（不匹配即停，不得开跑）**：诊断面板标题必须**是** `Viewport diagnostics · p9-full-shell-session-avoidance-v1`，且帧内 `schemaRev` 含 `p9-additive-touch-truth-v1`；任一不满足 → **停止，关掉标签页重开**（服务端已禁缓存）。这两项与现役 [evidence/verify-sop.mjs](evidence/verify-sop.mjs) 的 `EXPECTED_PROBE_VERSION` 及现役探针 `SCHEMA_REV` 一致。

> **戳记代际提醒**：A0-SOP 文首写的 `p8-full-shell-overlay-wheel-v1` 属 **p8 时代历史 SOP**（[PHASE1-CONTRACT](PHASE1-CONTRACT.md) 头部"历史文档定位"）。本文档与现役工具同代，戳记以本文"适用构建"为准；**不得混代比对**，也不得以戳记不同反判历史会话造假。

---

## 0. 核心纪律：scale 与妙控（每场景开始前必读）

**Phase 4 全部场景以 `vv.scale = 1` 为前提。** 这是硬门槛，不是建议：

- **每场景开始前与结束后各一次 `Capture` 自查 `vv.scale`**（两次 `manual` 帧），并把两次读数写进该场景时间线。结束后的那次 Capture 与开跑前的那次必须同为 1，否则整场景按 ×3 重做。
- **若步骤中意外出现 `vv.scale ≠ 1`**（双指误触、Safari 菜单缩放、妙控键盘 `cmd+'+'` / `cmd+'-'`）——**该帧不得出 verdict**。处置固定为：**缩回 1 → 重新 settle（两次 500ms tick + ε=0.001 口径，见 [PHASE1-CONTRACT](PHASE1-CONTRACT.md) 术语章 D2 细则 3）→ 重跑本场景**；帧序里**如实标注污染段**（起止 seq + 实际 scale 读数），污染段既不判 PASS 也不判 FAIL，但**必须保留在 raw 里、不得裁剪**。
- **低倍率场景不属本文**：`scale ≠ 1` 的 **0.75 深键盘等场景属 Phase 5 D2（Z-PINCH / Z-MENU / Z-MAGIC）**，由 [DATA-COLLECTION §10](DATA-COLLECTION.md) 的独立单源会话承担。**本 SOP 明确不测、不出任何 verdict**；Phase 4 遇到需要缩小才有意义的动作（例如为造更深的键盘而缩放），**一律不做**。

**妙控键盘纪律**：接入 / 断开本身会改变 `visualViewport` 与焦点事实，因此"禁接"场景里**键盘不摆上桌面**（放包里 / 远离）；"必须接"场景只按步骤接一次，接完**等 2 秒**再 Capture；环境变化（浮窗 / 分屏）要逐场景记录，否则该场景的证据不可归因。

---

## 1. 场景矩阵

每个场景一块，字段固定：编号 / 目的 / 前置会话状态 / 步骤逐条 / 每步 Capture 点 / scale 要求 / 妙控要求 / 期望（可核对字段量）/ 判负规则 / 证据要求。

**字段名来源声明**：本文所有字段名取自现役 p9 探针快照（`internal/ui/debugassets/viewport_debug.js`）与现役 `evidence/verify-sop.mjs` 的读取路径：`app.bottom` / `app.inlineHeight` / `app.layoutGap` / `document.htmlOverflow` / `document.bodyOverflow` / `app.computedPosition` / `document.rootScrollHeight` / `document.scrollTop` / `document.scrollingElementScrollTop` / `occlusion` / `ptyHosts[].bottom` / `ptyHosts[].inlineBottom` / `ptyHosts[].visible` / `controls.inlineBottom` / `controls.bottom` / `controls.height` / `statusBar.inlineBottom` / `statusBar.bottom` / `quickKeys.clientHeight` / `quickKeys.maxHeight` / `quickKeys.scrollHeight` / `xtermRows.bottom` / `vv.height` / `vv.offsetTop` / `vv.scale` / `ui.activeElementTag` / `ui.quickKeysOpen` / `reason` / `ownership` / `activeInput` / `keyboardSession` / `unlockTrace`。**未核实 / 不存在的字段一律按缺字段处理 → INCONCLUSIVE，不得编造字段名。**

### P4-A 回归基线（每轮必跑）

| 项 | 内容 |
| --- | --- |
| 目的 | 用同一构建重跑 A0-SOP S1–S8，确认 p9 现役构建没有把 A0 v3.1 已验证的几何整段弄丢。 |
| 前置会话状态 | 新开标签（认证 shell）→ 面板落位 `tr` → 不做任何其他操作。 |
| 步骤 | **原样执行 [A0-SOP](A0-SOP.md) 的步骤表 S1–S8**，顺序、操作、期望与"我会核对的量"列**一字不改**。 |
| Capture 点 | 按 A0-SOP 逐步：S1 / S2 / S3 / S4 / S5 / S6 / S7 / S8 各一次 `manual`。 |
| scale 要求 | 全程 `scale = 1`；开跑前 / 结束后各一次自查 Capture。 |
| 妙控 | S1 / S2 禁接；**S3 / S4 必须接 = A0-SOP S3 / S4 原样**（S3 保持软键盘后接入妙控等 2 秒；S4 已接妙控后用浮窗展开软键盘等 2 秒）；S5–S8 按 A0-SOP 原文（妙控仍在场，因为 S4 之后未断开）。 |
| 期望 | **整段继承 A0-SOP v3.1 判据，不做任何阈值改动**：`app.inlineHeight` 恒为空；`layoutGap = clientHeight − app.bottom`，`|layoutGap| ≤ 1`；`occlusion` 与地板后 shortfall 一致；所有宿主 `inlineBottom == occlusion + "px"` 且彼此相同；让位帧宿主真实底 `≤ vv.height + 1`；根 `scrollTop = 0`；`htmlOverflow` / `bodyOverflow == clip`；`computedPosition == fixed`；`rootScrollHeight ≤ clientHeight`；稳定帧 `xtermRows.bottom ≤ vv.height + 1`；`controls.inlineBottom == 20 + occlusion`；`statusBar.inlineBottom == occlusion`（两者**必须不同**）。 |
| 比例地板与布局地板 | **引用 A0-SOP 原文，本文不重写**：比值地板 `SMALL_OCCLUSION_RATIO = 0.15`；702 布局地板 = `702 × 0.15 = 105.3px`；实测锚点 274 → 428（61.0%，让位）、555 → 147（20.9%，让位）、633.5 → 68.5（9.8%，豁免为 0）、702 → 0（纯覆盖）。见 [A0-SOP "本构建的契约"](A0-SOP.md) 第 1–2 条与"局部避让的范围"条。 |
| 判负规则 | 按 A0-SOP 不变量 1–10 原判。本文**不新增**、**不放宽**、**不收紧**任何一条。 |
| 证据要求 | raw + `node evidence/verify-sop.mjs --file evidence/raw/<id>.jsonl` 退出码 + 各档截图（面板先 Move 离底部）。 |

> **本场景与 Phase 4 的关系**：P4-A 只证明"现有基线没退化"，**不证明** D3 / D4 / GI-7 任何一项。D3 / D4 / GI-7 的 verdict 只在 P4-F / P4-G / P4-H 里出。

### P4-B 键盘三态光标（R1 末行可达）

| 项 | 内容 |
| --- | --- |
| 目的 | 在**键盘确实让位**的状态下，验证输入行与最后一行分别在 顶部 / 中部 / 底部 三种光标位置都落在屏显区内（R1 / GI-3 目标；[PLAN §6](PLAN.md#6-真机验收矩阵)"光标/内容位于上、中、下"行）。 |
| 前置会话状态 | scale=1 静止基线（等同 A0-SOP S1）；终端里有足够内容把屏幕填满（内容不足时先跑命令把输出撑到满屏，**不记录终端文本**）。 |
| 步骤 | ① 点终端唤起软键盘 → 按 `Soft keyboard` 标一次 → Capture（确认让位成立再继续）；② **顶部态**：把输入行带到第一行（清屏后光标停在顶部 / 新开一行到顶）；③ **中部态**：让输入行居中于屏幕；④ **底部态**：连续回车 / 输出累加，让**最后输入行贴键盘顶边**（第五§情境）；⑤ 保持底部态不动作，静置 10 秒后再 Capture 一次（查 pan）。 |
| Capture 点 | ① 让位确认 1 次；②③④ 三态各 1 次 `manual`；⑤ 静置后 1 次 `manual`。 |
| scale 要求 | `scale = 1`；开跑前后各一次自查。 |
| 妙控 | **禁接**（不摆上桌面）。 |
| 期望 | **三态各独立判定**（不得一轮代三轮）：每态 `xtermRows.bottom ≤ vv.height + 1`；让位帧 `occlusion > 0`，所有宿主 `inlineBottom == occlusion + "px"`、真实底 `≤ vv.height + 1`；`app.inlineHeight` 为空、`|layoutGap| ≤ 1`；根 `scrollTop = 0`；全程 `vv.offsetTop = 0`。**输入行与最后一行是分别判定的两个量**（[PHASE1-CONTRACT](PHASE1-CONTRACT.md) 术语章"输入行 / 最后一行"），底部态要**两者都**在 `[0, vv.height]` 内。 |
| 判负规则 | 任一态 `xtermRows.bottom > vv.height + 1`（且该帧为让位稳定帧）= **FAIL**；输入行可见但最后一行不可见（或反之）= 该态 **FAIL**（不得互相代替）；`vv.offsetTop` 出现非 0 → 按 **P4-I 红线处置**（停止并回到 D1）；三态缺任一态 = **INCONCLUSIVE**。 |
| 证据要求 | 三态各一张真机截图（面板先 Move 离**键盘顶边**，只拍面板外区域）；raw 连 seq；`verify-sop.mjs` 独立复算。 |

### P4-C quick-keys 打开下的键盘场景（R1 / 控制列）

| 项 | 内容 |
| --- | --- |
| 目的 | 验证活动避让会话里 quick-keys 入口 / 面板整体可达、列高按遮挡缩短、面板盒内可滚，且**粘贴动作不触发额外 pan / 额外让位**（GI-4 + HO-3 的 F4 面）。 |
| 前置会话状态 | scale=1；键盘已开（`occlusion > 0` 的让位态）；`ui.quickKeysOpen = false` → 将被打开。 |
| 步骤 | ① 键盘开着时打开 quick-keys 入口 → 打开面板 → Capture；② 点入 quick-keys 粘贴 textarea → 粘贴一段**不含敏感内容**的测试文本 → Capture；③ 关面板、关键盘 → Capture。 |
| Capture 点 | ① 面板打开 1 次；② 粘贴后 1 次；③ 关闭后 1 次。 |
| scale 要求 | `scale = 1`；开跑前后各一次自查。 |
| 妙控 | **禁接**。 |
| 期望 | 列高按 `occlusion` 缩短（`controls.height` 变小）且 `controls.inlineBottom == 20 + occlusion`（**必须与宿主值不同**，抄成宿主值即 FAIL）；`quickKeys.clientHeight ≤ controls.height` 且 `quickKeys.maxHeight` 解析为与列高一致的长度；面板**盒内可滚**（肉眼 + `quickKeys.scrollHeight > clientHeight` 为辅助）；`controls.inlineBottom` 的 `top:20px` 不跳（入口按钮不移动，肉眼）；粘贴动作后**无额外 pan**（`vv.offsetTop` 仍 0）且 `occlusion` 不因粘贴而增大。 |
| 判负规则 | `controls.inlineBottom ≠ 20 + occlusion`、或等于宿主 `inlineBottom` = **FAIL**；面板高度 > 列高 = **FAIL**；粘贴后 `vv.offsetTop` 非 0 = 按 **P4-I** 处置；面板打开但整轮缺 `controls` / `quickKeys` 字段 = **INCONCLUSIVE**。 |
| 证据要求 | **继承 [PHASE1-CONTRACT](PHASE1-CONTRACT.md) GI-4 基线的严格限定**：C 会话 `seq 88–99` 只判**该连续场景**判 PASS，**不外推整行 PASS**——C 全文件另有 8 个序列缺口。本场景同理：**只认证本次粘贴 / 面板场景**，不关闭 GI-4 整行。 |

### P4-D Transport 状态条

| 项 | 内容 |
| --- | --- |
| 目的 | 确认状态条在让位帧落在键盘之上，且与宿主 / 控制列底边**数值不同**，并单独拍一张物理可见性截图。 |
| 前置会话状态 | 分两段：**D1 段**在 P4-B / P4-C 的让位帧上顺带核对（不新增操作）；**D2 段**单独开一次让位。 |
| 步骤 | D1 段：无需额外操作，直接在 P4-B / P4-C 的让位 Capture 帧上核对。D2 段（scale=1）：点终端开键盘 → 面板先 `Move` 离底部 → Capture → 拍截图。 |
| Capture 点 | D2 段 1 次 `manual`；D1 段复用 P4-B / P4-C 的帧（**显式标注帧号**，不得模糊引用）。 |
| scale 要求 | `scale = 1`；D1 段随 P4-B / P4-C 的 scale 自查；D2 段开跑前后各一次。 |
| 妙控 | **禁接**。 |
| 期望 | 让位帧 `statusBar.inlineBottom == occlusion`；`statusBar.bottom ≤ vv.height + 1`；`statusBar.inlineBottom` 与 `ptyHosts[].inlineBottom`、`controls.inlineBottom` **三者必须不同**（`occlusion` vs `20 + occlusion` vs `occlusion`，状态条与宿主同值本就允许"同为 occlusion"，但**与宿主 real bottom 必须不同**——状态条是 `fixed` 钉视口底，宿主是 `absolute` 在 `#workspace` 里，真机 safe-area≈20 下二者真实底必然不同）；截图里状态条**物理可见**（不被键盘压住）。 |
| 判负规则 | `statusBar.inlineBottom ≠ occlusion` = **FAIL**；`statusBar.bottom > vv.height + 1`（让位帧）= **FAIL**；缺 `statusBar` = **INCONCLUSIVE**（缺证据不等于通过）。 |
| 证据要求 | 让位帧面板离底部截图 1 张；D1 段的复算依赖 P4-B / P4-C 各自 raw。 |

### P4-E 妙控接入与浮层（A0 S3 / S4 等价重跑）

| 项 | 内容 |
| --- | --- |
| 目的 | 在 p9 构建上等价重跑妙控两档：接入时不残留上一键盘状态的带、页面不自己滑；浮窗展开软键盘时按比值地板分类（薄浮窗豁免 / 达标让位）。 |
| 前置会话状态 | scale=1；软键盘已开（即 A0-SOP S2 结束态）。 |
| 步骤 | ① **先软键盘开**（scale=1）→ 接入妙控键盘 → **等 2 秒** → Capture（= A0-SOP S3）；② 用**浮窗**展开软键盘 → 等 2 秒 → Capture（= A0-SOP S4）；③ 断开妙控、收起软键盘 → 等 2 秒 → Capture（= A0-SOP S7 等价）。 |
| Capture 点 | ①②③ 各 1 次 `manual`。 |
| scale 要求 | `scale = 1`；开跑前后各一次自查（接妙控后若出现 `scale ≠ 1`，按 §0 处置，**不得**顺手拿它当 D2 证据）。 |
| 妙控 | **必须接**（步骤① 接入并等 2 秒；步骤③ 断开）。 |
| 期望 | **步骤与期望全部引用 A0-SOP S3 / S4 / S7 原文**：S3——`layoutGap ≈ 0`；`occlusion` 与当时 `vv.height` 一致、宿主 bottom 随之改写；根 `scrollTop` **全程 = 0**（非 0 即 FAIL，这里没有"先动了再拉回"）。S4——**`633.5 → 68.5 = 9.8% < 105.3` 档 occlusion = 0、宿主满高被浮窗盖住**；**`555 → 147 = 20.9% ≥ 105.3` 档必须让位到 147px**；`vv.height == clientHeight`（真浮窗纯覆盖）时 `occlusion == 0`。 |
| 判负规则 | 按 A0-SOP S3 / S4 / S7 各行"期望"原判。**任一档 shortfall 落在 `63.5 / 147` 这两个实测锚点以外的数值时，只记录实测值并按 A0-SOP 比值地板公式（`raw ≥ clientHeight × 0.15`）重算，不新增档位、不改地板。** |
| 证据要求 | 每档面板离底部截图 1 张（浮窗展开状态要在截图里可见，或被如实标注"浮窗未入镜"）；raw 连 seq。 |

### P4-F 连续键盘开 / 关 / 重开（F18 真机补证）

| 项 | 内容 |
| --- | --- |
| 目的 | 补 F18 缺的"焦点全程在终端输入宿主"的**无缺口连续会话**证据（[PHASE1-CONTRACT](PHASE1-CONTRACT.md) F 表 F18 行"证据状态：未闭合"）。 |
| 前置会话状态 | scale=1 静止基线；**先按 `Start continuous`**（1000ms periodic）；无 quick-keys 面板、无本页表单。 |
| 步骤 | ① 点终端宿主开键盘 → 标一次 `keyboard-hint` → Capture；② 收起键盘（**焦点不离开终端宿主**：用键盘的"收起"手势 / 系统收起，**不得**点本页其他输入框、**不得**点顶栏按钮）→ Capture；③ **不再点击**（或只在终端宿主上做一次明确的 `click` 解锁，且逐帧记录）→ 再次弹出 → 标 `keyboard-hint` → Capture；④ 重复 ②③ 共**两轮**；⑤ `Stop continuous` → Capture。 |
| Capture 点 | ①② 各 1 次；③④ 每轮开 / 关各 1 次；⑤ 停止后 1 次。 |
| scale 要求 | `scale = 1`；开跑前后各一次自查。 |
| 妙控 | **禁接**。 |
| 期望 | 按 [PHASE1-CONTRACT](PHASE1-CONTRACT.md) F18 行与[冻结 §5](../IPADOS-UI-COMPATIBILITY.md)口径：**同几何 raw 回落 > 1px 即退出避让并进恢复锁**，终端 / 控制列 / 状态栏回到底边（宿主 `inlineBottom == "0px"`、`occlusion == 0`）；**不得因键盘收起释放归属**（`ownership` 仍为 HO-2，焦点仍在终端输入宿主）；重入需 **`raw ≥ 布局高 × 50%` 或新 `click` / `focusin` / 窗口几何变化解锁**；会话内 `occlusion = raw` 全额。 |
| 判负规则 | 键盘收起后宿主仍留着旧 `inlineBottom`（未回 0）= **FAIL**；收起后 `ownership` 变成非 HO-2 且无对应焦点变化帧 = **FAIL**；重入时未满足 50% / 解锁条件却出现 `occlusion > 0` = 记 **INCONCLUSIVE**（不得判 FAIL，须先查 §5.5 解锁事件证据）。 |
| 证据要求（硬性） | **无缺口连续 seq 段** + **归属（宿主身份）字段在位**（`ownership` 与 `activeInput` 同帧可读）。**缺任一 → 该场景 INCONCLUSIVE。** |
| 先例限定 | **不得引用 C 会话 `seq 93 / 97 / 120` 与 A 会话 `seq 367–486` 作纯案例**（已登记：C 的 `seq 101` 缺失、`seq 113–115` `activeElementTag = BODY` 焦点曾离开、`seq 93 / 97 / 120` 为 `resize` / `scroll` 过渡帧；A 段因 quick-keys 从未打开整体 INCONCLUSIVE）。见 PHASE1-CONTRACT F18 行与 F10 行。 |

### P4-G D3 切回旧实例两轮（GI-8 等价判据）

| 项 | 内容 |
| --- | --- |
| 目的 | 验证 D3 已签署的**等价性判据**：切回旧实例首帧既不残留旧键盘高度的让位，也不出现键盘压终端（[PHASE1-CONTRACT](PHASE1-CONTRACT.md) E 表 D3 行 / GI-8 / F8 / F11）。**不测形态二，也不宣判形态一为唯一形态。** |
| 前置会话状态 | 两个可交互的 PTY 实例（A、B）；scale=1；**`Start continuous`**。 |
| 步骤 | **轮 1（有键盘）**：① A 的终端开键盘 → 让位成立（Capture）；② 切到实例 B（B 无键盘）→ Capture；③ **切回 A → 立即 Capture**（continuous 已在跑，靠连续帧抓首帧）；④ `Stop continuous`。**轮 2（无键盘）**：⑤ 重新 `Start continuous`；⑥ A 曾让位 → 收起键盘（raw ≤ 1 退出、宿主回 0，Capture）；⑦ 切走 → 切回 A → 立即 Capture；⑧ `Stop continuous`。 |
| Capture 点 | ①②③⑥⑦ 各 1 次 `manual`。 |
| scale 要求 | `scale = 1`；每轮前 / 后各一次自查。 |
| 妙控 | **禁接**。 |
| 期望 | **轮 1**：切回首帧 A **无旧让位残留**——形态一下 `ptyHosts[].inlineBottom` **全体一致**即结构上不残留（任一宿主留着旧值 = 残留）；同时**无键盘压终端**（若切换瞬间键盘状态已变，**按当帧几何重判**，不沿用切换前结论）。**轮 2**：切回后**不恢复历史遮挡状态**，`occlusion` 按当时的焦点 / `scale` / 可用视口重算（此时应为 0，宿主 `inlineBottom == "0px"`）。 |
| 判负规则 | 任何"**切回瞬间出现旧 geometry 残带**"或"**键盘压终端首帧**" = **FAIL**（GI-8 等价判据）；字段缺失（`ptyHosts` 整块缺、`occlusion` 缺）= **INCONCLUSIVE**，不得默认通过。 |
| 证据要求 | 两轮各一轮独立证据（不合并）；每轮至少一张"切回后"的判读截图（面板先 Move 离底部）；raw 连 seq，且**首帧必须落在 continuous 段内**，不得用事后补 Capture 代替。 |

### P4-H 键盘开着旋转（F12，D4③ settle 后 refit 的定性观测）

| 项 | 内容 |
| --- | --- |
| 目的 | 观测 D4③（旋转属"合并到 settle 后 refit"类）在真机上是否收敛，并如实声明其**定量对账缺字段**。 |
| 前置会话状态 | scale=1；`Start continuous`；键盘开着让位态。 |
| 步骤 | ① 键盘开着让位（Capture）；② 旋转竖 ↔ 横；③ 等 **settle**（别名：两次 500ms tick 口径；本文**不要求**人手踩中每个 tick）；④ 旋转中 Capture 一次（过渡帧，只记录）；⑤ settle 后 Capture ×1；⑥ 反向旋转回原方向，重复 ④⑤；⑦ `Stop continuous`。 |
| Capture 点 | ①④⑤ 每方向各一次 + ⑦1 次。 |
| scale 要求 | `scale = 1`；旋转前后各一次自查（旋转后若出现 `scale ≠ 1`，按 §0 处置并记录，该帧不出 verdict）。 |
| 妙控 | **禁接**。 |
| 期望 | **旋转中帧按 settle 宽容处理**（`resize` 过渡帧不触发避让判定、不判几何；见 A0-SOP 不变量 6 / 8 的 stable 帧豁免）；settle 后 `occlusion`、宿主 `bottom`、`xtermRows.bottom` **收敛到新几何**；肉眼确认屏幕无残带、末行可见。 |
| **判据等级（如实声明）** | rows / cols / applied 的**定量**对账需要 **`AUTH-20261105-R3-GI3-READONLY`** 的探针增补（`rows` / `cols` / `applied` / `lastSent` / `pending` 字段，见 [PLAN §10](PLAN.md#10-phase-2-诊断基座仲裁后续台账与另立授权)）**先行落地**。**字段不在位时，本场景的 GI-3 定量子项一律记 INCONCLUSIVE**，只保留几何与可见性的定性记录，**不得写 PASS**。现役 p9 快照**不含** `rows` / `cols` / `applied` / `lastSent` / `pending`（这些是 `evidence/pty-refit-check.mjs` 在浏览器内读取的字段，不属探针快照），故本文**默认按"字段不在位"执行**，除非调度者另行确认增补已落地。 |
| 判负规则 | 持续 pan（`vv.offsetTop ≠ 0` 持续）或终端出屏 → 按 **P4-I** 停止型 FAIL 处置；settle 后几何不收敛且下一稳定帧仍不对 = 按 GI-2/3/4/5 共用三态规则判 **FAIL**（滞后链 FAIL）；缺字段 = **INCONCLUSIVE**，**不得以"下一帧大概会对"判 PASS**。 |
| 证据要求 | 两个方向的 settle 后截图（面板先 Move 离**当前方向下的键盘顶边**）；raw 连 seq；**定量子项留空并标 INCONCLUSIVE**。 |

### P4-I 持续 pan 红线（F17 / GI-7 适用范围）

| 项 | 内容 |
| --- | --- |
| 目的 | 观察键盘开着、焦点在终端宿主时，Safari 是否产生持续非预期 pan（F17 停止型 FAIL 的唯一合法判据——**不是**修复手段）。 |
| 前置会话状态 | scale=1；键盘开着；焦点在终端宿主；`Start continuous`。 |
| 步骤 | ① 让键盘让位成立并 Capture；② **静置数十秒**（≥ 30 秒，期间不碰屏幕）；③ 保持静置再 Capture 一次；④ `Stop continuous`。 |
| Capture 点 | ①②③ 各 1 次；④ 停止后 1 次。 |
| scale 要求 | `scale = 1`；开跑前后各一次自查。**本场景是整个 SOP 里唯一可能用双指的地方，但双指只许用于"确认不缩放"的反证（轻触即收），一旦真缩放，按 §0 处置并重跑。** |
| 妙控 | **可接**（观察档）。接 / 不接都要记录；不接时不得出现"妙控浮层"帧（出现即污染，记 INCONCLUSIVE）。 |
| 期望 | `vv.offsetTop` **恒为 0**；终端宿主 client rect 在 `[0, vv.height]` 内（`ptyHosts[].bottom ≤ vv.height + 1`）。 |
| 判负规则（停止型） | 出现**持续非预期 pan** 或**终端出屏**（client rect 越出 `[0, vv.height]`）= **FAIL**，并按 [PLAN §5 Phase 4 门禁](PLAN.md#phase-4pty-终端自有视口与-pan-处理) 处置：**停止并回到 D1**——**不得做任何"抢回"手段**（`scrollTo(0,0)` / 反复写根 `scrollTop` / 反向 transform 一律禁止），**不得继续跑后续场景**。**单次短暂 `offsetTop` 抖动按帧如实记录**；**连续多帧非 0 按 pan 事实处理**（归因栏 INCONCLUSIVE，**不得把相继当因果**）。 |
| 证据要求 | 静置段的连续 seq 全保留（不裁剪）；pan 事实帧逐帧列出（seq + `offsetTop` + 宿主 rect）；若判 FAIL，另附"D1 回退记录"。 |

### P4-Z 污染帧处置（横切）

适用于全部场景，优先级最高：

- **`vv.scale ≠ 1`** → 该帧起本场景记 **INCONCLUSIVE**，按 §0 处置后重跑。
- **意外双指缩放 / 意外 magic 浮层事件（`magic-hint`）** → 该帧起本场景记 **INCONCLUSIVE**；magic 浮层只标记"浮动提示条出现"，**不代替真实软键盘状态**，不得据此判会话 active。
- **面板不可达 / 不可操作（GI-9）** → 记 **INCONCLUSIVE**，**不得伪造产品 PASS**（面板不可达本身就是证据缺口）。
- **probe 缺字段** → 按 A0-SOP 不变量 9 的 INCONCLUSIVE 总门；`probeReadFailed: true` 帧的页面测量字段一律不可读（[PHASE1-CONTRACT](PHASE1-CONTRACT.md) "降级帧的可执行排他规则"）。
- **不得静默 PASS**：污染帧在时间线上必须可见（起止 seq + 污染类型），并**保留在 raw 里**。

---

## 2. 判负总表与归档纪律

### 2.1 判负总表

| 判定 | 触发条件 | 出处 |
| --- | --- | --- |
| **PASS** | A0-SOP 不变量 1–10 全部成立，本场景"期望"列全部满足，且证据齐全（连续 seq、面板离底截图、字段在位）。 | A0-SOP 不变量 9；PHASE1-CONTRACT F 节通用规则 |
| **FAIL** | 任一 MUST 不变量在真机被观测不成立（`shellShort` / `pinned` / `occlusion` / `host` / `residue` / `underKeyboard` / `controls` / `controlsUnder` / `statusBar` / `statusBarUnder` / `quickKeys` / `scroll` / `range` / `overflow` / `position` / `ink` / `rows`，见现役 `verify-sop.mjs` 的 `FAILURE_KINDS`）；或本场景"判负规则"明确列出的 FAIL。 | `verify-sop.mjs` |
| **INCONCLUSIVE** | 缺字段 / seq 缺口 / 场景未覆盖 / 面板不可达 / 只有离线证据 / 污染帧未重置。**缺证据不等于通过。** | A0-SOP 不变量 9；PHASE1-CONTRACT F 节 |
| **停止型 FAIL** | 持续 pan 或终端出屏（P4-I）。**停止实现并回到 D1，不追加阈值，不"抢回"。** | PLAN §5 Phase 4 门禁；PHASE1-CONTRACT F17 |

**verify-sop 不变量 1–10 全量沿用**，引用 [A0-SOP "我这边自动核对的不变量"](A0-SOP.md) 对应节，**本文不重写、不改阈值**。若发现 A0-SOP 与本构建实测字段不符，按本文 §1 声明处理：**未核实 / 缺字段 → INCONCLUSIVE**，不得编造。

### 2.2 复算与归档

- **对每轮原始会话独立复算**：`node evidence/verify-sop.mjs --file evidence/raw/<id>.jsonl`；**exit 0 / 1 / 2 = PASS / FAIL / INCONCLUSIVE**。一轮 raw 只复算它自己的场景，**不得跨轮拼接**。
- **原始不改写**：raw 逐字节保留，不裁剪、不重排、不重采样、不格式化；**md5 归档**（写在验收记录里），文件名与 md5 一起登记。
- **派生段与原始分开标识**：派生连续段（如 `…-p4-f-continuous.jsonl`）另名保存，**不得覆盖原始**；引用"段"时必须写明取自哪个原始文件、起止 seq。
- **验收记录三层分开**：① 原始会话 `raw/*.jsonl`；② 派生连续段；③ 最终验收记录。**三层不得互相冒充**（PHASE1-CONTRACT 证据分级 / [DATA-COLLECTION](DATA-COLLECTION.md) §2）。
- **每场景一张"面板先 Move 离底部"的真机截图**：面板自己就压在它要证明的那条边上，截图前必须把面板**拖离该边**，**只拍面板外区域**；截图与 seq 同场绑定。
- **隐私**：只记录几何、有限设备元数据与 UI 状态；不记录 token、终端文本、worktree 名称 / 路径或实例 ID（DATA-COLLECTION 文首约定）。

### 2.3 时间线模板（每场景一份，跑完立即填）

| session / 文件 | 场景 | seq 起–止 | 开跑前 scale | 结束 scale | 妙控状态 | 浮窗 / 分屏 | Capture seq / shotId | 污染段 seq（类型） | verdict |
| --- | --- | --- | --- | --- | --- | --- | --- | --- | --- |
| 待填 | P4-X | 待填 | 待填 | 待填 | 禁接 / 必须接 / 可接 | 待填 | 待填 | 无 / 待填 | 待填 |

---

## 3. SOP 与 A0-SOP 的关系声明

本文是 **Phase 4 扩展采证书**：它在 A0-SOP v3.1 之上新增**场景**（P4-A 至 P4-I）与**采集纪律**（scale / 妙控 / 并发 / 污染帧），**不新增契约判据**。

- A0-SOP 的契约判据与阈值（含比值地板 0.15、702 布局地板 105.3px、不变量 1–10）**不因本文改动**。
- 两者**冲突以 A0-SOP 为准**，并**停止回报**（不在真机上现场裁决，不在文档里就地改判据）。
- 本文**不升格**为现行基线：现行基线仍是冻结文档 `5268869` + 现役 p9 `verify-sop.mjs`（PHASE1-CONTRACT 文首"现状基线权威"）。A0-SOP 自身也已被 PHASE1-CONTRACT 定位为**历史执行 SOP**，本文沿用其判据**仅因它是当前唯一成文的 p8/p9 版执行判据来源**，并已按本文"适用构建"校正戳记代际。

---

## 4. 产物状态边界

**本 SOP 执行产物只能登记每场景 PASS / FAIL / INCONCLUSIVE。**

- **D3 / D4 / GI-7 / GI-3 的真机结论逐场景单独登记**，**不整行关闭**；`P4-G` 的 PASS 不等于 D3 整行通过，`P4-H` 的定性记录不等于 D4 整行通过，`P4-F` 的 PASS 不等于 F18 整行闭合，`P4-H` 的定量子项甚至默认 INCONCLUSIVE。GI-3 / GI-7 整行在 `AUTH-20261105-R3-GI3-READONLY` 增补落地并出真机读数前**保持未验证**。
- **不宣称 R1 门禁通过**，直到矩阵（P4-A 至 P4-I）**全部**有 verdict 且无未闭合的 INCONCLUSIVE 项；在此之前，任何单场景结果都只是**场景级**结论。
- **不得以离线结果冒充真机**：`verify-sop.mjs --self-test`、`structural-check.mjs`、`pty-refit-check.mjs --self-test` 都只证明工具链，不产生真机 PASS（A0-SOP "已知道的允许失败边界"末条；PLAN §13.3）。
- **回退性不变**：本文不改变 `5268869` / `2e857de` / `47c5c3e` 的回退点，也不表示 D3 / D4 已落地或已验证。
