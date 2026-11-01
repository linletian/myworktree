# myworktree 主 UI 设计文档

本目录是主 UI 响应式与终端辅助 UI 的**持续改进设计真源**。后续设计修改只在这里进行；`docs/plans/feature-v0.6.0-*` 下的同名内容保留为历史计划与历史评审记录，不再修改。

## 设计源与产物

| 范围 | 设计真源 | 导出 / 预览 | 当前状态 |
| --- | --- | --- | --- |
| Issue #116 / #100 主 UI 响应式、状态化动作与高度契约 | [sidebar-states.pen](sidebar-states.pen) | 本目录 16 张 PNG；[preview-responsive.html](preview-responsive.html) | D0-1 稿件已生成，设计评审待签 |
| 终端 quick-keys 及其与主壳层的契约 | [quick-keys/quick-keys-panel.pen](quick-keys/quick-keys-panel.pen) | `quick-keys/` 内 7 张 PNG | 已注明 issue #116 契约依赖，形态不变 |
| Issue #118 Portal 响应式 | [portal-responsive.pen](../docs/plans/fix-issue116/design/portal-responsive.pen) | 同目录 10 张 PNG | D0-2 稿件已生成，设计评审待签 |

## 维护纪律

1. **只改设计真源，再重出 PNG。** 不直接修图，不用后期缩放冒充重新导出。
2. 使用 `pen version` 记录实际 CLI；本批为 **0.3.11**。通过 `pen interactive` 的 `get_app_state()` 校验、`Get` 查询真实 frame id、`Export(ids, "png", dir)` 导出并 `save()` 保存。
3. `preview-responsive.html` 只提供交互演示，不是第二套设计真源，也不是 visualViewport、iPadOS 或真机高度证据。
4. 实施若偏离稿件，先回改本目录设计源并记录原因，再改代码。PR1 / PR2 / PR3 仍需按计划通过设计评审门禁。
5. 旧 plans 中的设计稿只用于追溯；若需继续演进，把变更落到本目录，不回写历史文件。

完整帧清单、实际导出尺寸、验证结果与评审检查项见 [EXPORT-MANIFEST.md](../docs/plans/fix-issue116/design/EXPORT-MANIFEST.md)。总体实施计划见 [PLAN.md](../docs/plans/fix-issue116/PLAN.md)。
