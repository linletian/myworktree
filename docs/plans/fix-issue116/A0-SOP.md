# A0 真机验证 SOP（issue #116）

> 每个步骤做完按一次浮层上的 **Capture**，我按本文对账。
> 关键前提：浮层标题必须是 **`Viewport diagnostics · p4-reconcile-wheel`**。
> 若标题里没有 `· p4-...`，说明跑的是旧代码 → 关掉标签页重开（服务端已禁缓存）。

## 步骤

| # | 操作 | 期望 | 我会核对的量 |
| --- | --- | --- | --- |
| S1 | 什么都不开（无软键盘、无浮窗、静止）→ `Capture` | 底部无任何色带；终端铺到物理底 | `inline` 为空；`visibleBottom − app.bottom ≤ 1` |
| S2 | 点终端唤起**软键盘** → `Soft keyboard` → `Capture` | 页面变矮，**一行文字都不被压住**；底部无白带 | `inline == vv.height`；`xtermRows.bottom ≤ 键盘顶` |
| S3 | 保持软键盘 → **接入妙控键盘** → 等 2 秒 → `Capture` | **原白带消失**（本步是重点） | `inline` 清空或等于当前 `vv.height`；`visibleBottom − app.bottom ≤ 1` |
| S4 | 已接妙控键盘 → 用**浮窗**展开软键盘 → 等 2 秒 → `Capture` | 不出现键盘高度的宽白带 | `visibleBottom − app.bottom ≤ 1` |
| S5 | 浮窗存在时，在**实例区外**用**触摸板双指**上下滚 → `Capture` | 页面**完全不动** | `pageTop` / `scrollTop` 与上一帧一致 |
| S6 | 同上但用**手指触摸**在实例区外拖 → `Capture` | 页面**完全不动** | `pageTop` / `scrollTop` 不变 |
| S7 | 收起软键盘 / 断开妙控键盘 → 等 2 秒 → `Capture` | 恢复满高，无残留 | `inline` 为空；`visibleBottom − app.bottom ≤ 1` |
| S8 | 终端内部用手指上下滚；侧栏/监控弹窗上下滚 | **必须仍能滚**（豁免逻辑不能误伤） | 不做数值断言，肉眼确认即可 |

## 我这边自动核对的三个不变量

1. **无白带**：`band = (vv.offsetTop + vv.height) − app.bottom`，任何一帧 `band > 1` 即失败。
2. **底部像素归属**：`probe.visualBottom` 命中的元素必须是 `workspace` / 终端宿主，命中 `BODY` / `HTML` 说明根画布真被露出。
3. **页面不动**：S5/S6 期间 `document.pageTop` 与 `scrollTop` 必须保持 0；`vv.offsetTop` 允许变化（那是 iOS 自己的可视区平移），但不得转化为文档滚动。

## 已知的"允许失败"边界

- **缩放态**（`scale ≠ 1`）整个补偿豁免，不参与判定。
- 键盘/浮窗**动画当帧**可能有一帧 `xtermRows` 尚未 refit（`ResizeObserver` 下一帧完成），只允许当帧、不允许持续。
