# 中文输入法全角符号首次输入丢失问题分析报告

**日期**: 2026-03-24
**状态**: 未解决
**影响**: 搜狗拼音输入法用户

---

## 问题描述

在 Instance 窗口使用搜狗中文输入法时，长按 Shift + 符号键输入全角符号时，**第一个全角符号无法输入成功**，从第二个开始才能正常输入。松开 Shift 再次长按，第一个又不行。

**影响符号**: `！@#￥%……&*（）——+「」|："《》？~`（全角符号范围 U+FF01-U+FF5E）

**不影响**:
- 大写字母输入（通过输入法候选栏）
- 系统自带中文输入法（测试未复现）

---

## 问题根因分析

### 1. 搜狗输入法的特殊行为

通过详细的事件日志分析，发现搜狗输入法的行为与系统输入法有显著差异：

**搜狗输入法的事件序列：**
```
keydown (ShiftLeft, keyCode=229)  → textarea = ""
_handleAnyTextareaChanges()       → 捕获 ""
setTimeout callback               → textarea 仍是 ""！
keydown (Digit1, keyCode=229)     → textarea = "！"
_handleAnyTextareaChanges()       → 捕获 "！"
setTimeout callback               → textarea = "！"，diff = ""（无变化）
```

**关键发现：** 搜狗输入法在 keydown 事件触发后才插入字符到 textarea，这导致：

1. 第一次 keydown 触发 `_handleAnyTextareaChanges()` 时，textarea 是空的
2. `setTimeout(0)` 回调执行时，textarea **仍然是空的**（IME 还没插入字符）
3. 然后 IME 插入 `！` 到 textarea
4. 第二次 keydown 触发时，textarea 已经是 `"！"`
5. 此时 `_handleAnyTextareaChanges()` 捕获的是 `"！"`
6. setTimeout 回调执行时，captured = current = `"！"`，diff = `""`，不满足发送条件

### 2. xterm.js 的 `_handleAnyTextareaChanges` 逻辑

```javascript
_handleAnyTextareaChanges() {
    const e = this._textarea.value;  // 捕获当前值
    setTimeout(() => {
        if (!this._isComposing) {
            const t = this._textarea.value;  // 回调时的值
            const i = t.replace(e, "");       // 计算差异
            this._dataAlreadySent = i;
            t.length > e.length ? this._coreService.triggerDataEvent(i, true)
            : t.length < e.length ? this._coreService.triggerDataEvent(`${C0.DEL}`, true)
            : t.length === e.length && t !== e && this._coreService.triggerDataEvent(t, true);
        }
    }, 0);
}
```

**问题核心：**
- `setTimeout(0)` 的回调在当前事件循环的微任务队列之后执行
- 搜狗 IME 插入字符的时机在 setTimeout 回调之后
- 导致第一个字符的 setTimeout 回调执行时，textarea 还没有变化

### 3. 事件时序对比

**正常的 IME 行为（系统输入法）：**
```
keydown → IME 插入字符 → textarea 变化 → setTimeout 回调 → 检测到变化 → 发送
```

**搜狗输入法的行为：**
```
keydown → setTimeout 回调（textarea 未变）→ IME 插入字符 → textarea 变化
```

### 4. 为什么第二个字符能正常输入？

因为此时 textarea 中已经有第一个字符 `！`：
- keydown 触发，`_handleAnyTextareaChanges()` 捕获 `"！"`
- setTimeout 回调执行时，textarea 变成 `"！@"`
- diff = `"@"`，满足 `t.length > e.length`，触发发送

**但实际上发送的是第二个字符，第一个字符永远丢失。**

---

## 测试日志证据

以下是一次 Shift + 1 输入 `！` 的完整日志：

```
[IME-DEBUG] _handleAnyTextareaChanges called, captured: ""
[IME-DEBUG] keydown: Shift keyCode: 229 textarea: "" isComposing: false
[IME-DEBUG] setTimeout callback, isComposing: false captured: "" current: ""
[IME-DEBUG] diff: "" t.length: 0 capturedValue.length: 0  ← 没有变化！

[IME-DEBUG] _handleAnyTextareaChanges called, captured: "！"
[IME-DEBUG] keydown: ! keyCode: 229 textarea: "！" isComposing: false
[IME-DEBUG] setTimeout callback, isComposing: false captured: "！" current: "！"
[IME-DEBUG] diff: "" t.length: 1 capturedValue.length: 1  ← 长度相等，无差异！

[IME-DEBUG] textarea changed: "" -> "！"  ← IME 在 keydown 之后才插入
```

---

## 已尝试的解决方案

### 方案 1：升级 xterm.js 到 6.0.0

**结果**: 引入兼容性问题，终端无法正常工作

**原因**:
- PR #5024 (Fix duplicate input for some IMEs) 在 6.0.0 中发布
- 但 addon-fit 版本不兼容，导致终端无法初始化

### 方案 2：监听 input 事件补偿发送

**结果**: 只解决了部分问题，不稳定

**问题**:
- 只能检测到特定范围的全角符号
- 延迟时间不确定（50ms 在某些系统上不够）
- 可能导致重复发送（用户快速输入时）
- 影响中文正常输入（拼音也会被捕获）

### 方案 3：包装 `_handleAnyTextareaChanges` 增加延迟

**结果**: 无法从根本上解决问题

**原因**:
- 延迟时间难以确定
- 仍然依赖 setTimeout 的执行时机
- 可能影响其他正常输入

---

## 可能的解决方向

### 1. 向 xterm.js 提交 Issue/PR

报告搜狗输入法的特殊行为，建议增加更长的延迟或使用 `requestAnimationFrame` 轮询检测变化。

### 2. 使用 MutationObserver 监听 textarea

监听 textarea 的 value 变化，而不是依赖 setTimeout。

### 3. 在 keyup 事件中补充检测

搜狗输入法的字符插入可能在 keyup 之前完成，可以在 keyup 中补充检查。

### 4. 升级到 xterm.js 6.x 并解决兼容性问题

需要同时升级 addon-fit 到兼容版本，或自行打包。

---

## 相关文件

| 文件 | 说明 |
|------|------|
| `internal/ui/static/index.html` | 前端终端逻辑 |
| `internal/ui/static/vendor/xterm/xterm.js` | xterm.js 库（当前版本） |
| `internal/ui/static/vendor/xterm-addon-fit/xterm-addon-fit.js` | 终端自适应插件 |
| `docs/TERMINAL_IO_ANALYSIS.md` | 终端 I/O 架构文档 |
| `docs/TERMINAL_FILTER_REVIEW.md` | 终端过滤实现指南 |

---

## 参考资料

- [xterm.js CompositionHelper 源码](https://github.com/xtermjs/xterm.js/blob/master/src/browser/CompositionHelper.ts)
- [xterm.js Issue #4486: Various problems with Chinese IMEs](https://github.com/xtermjs/xterm.js/issues/4486)
- [xterm.js PR #5024: Fix duplicate input for some IMEs](https://github.com/xtermjs/xterm.js/pull/5024)
- [MDN: KeyboardEvent.keyCode 229](https://developer.mozilla.org/en-US/docs/Web/API/KeyboardEvent/keyCode#keycode_229)

---

## 附录：完整事件日志

输入 `！@#` 三次按键的完整日志：

```
[IME-DEBUG] _handleAnyTextareaChanges called, captured: ""
[IME-DEBUG] keydown: Shift keyCode: 229 textarea: "" isComposing: false
[IME-DEBUG] setTimeout callback, isComposing: false captured: "" current: ""
[IME-DEBUG] diff: "" t.length: 0 capturedValue.length: 0

[IME-DEBUG] _handleAnyTextareaChanges called, captured: "！"
[IME-DEBUG] keydown: ! keyCode: 229 textarea: "！" isComposing: false
[IME-DEBUG] setTimeout callback, isComposing: false captured: "！" current: "！"
[IME-DEBUG] diff: "" t.length: 1 capturedValue.length: 1

[IME-DEBUG] textarea changed: "" -> "！"

[IME-DEBUG] triggerDataEvent: "@" wasUserInput: true  ← 第一个字符丢失，第二个正常
xterm.js: sending data "@" [64] (1)

[IME-DEBUG] _handleAnyTextareaChanges called, captured: "！@"
[IME-DEBUG] keydown: @ keyCode: 229 textarea: "！@" isComposing: false
[IME-DEBUG] setTimeout callback, isComposing: false captured: "！@" current: "！@"
[IME-DEBUG] diff: "" t.length: 2 capturedValue.length: 2

[IME-DEBUG] textarea changed: "！" -> "！@"

[IME-DEBUG] triggerDataEvent: "#" wasUserInput: true
xterm.js: sending data "#" [35] (1)

[IME-DEBUG] textarea changed: "！@" -> "！@#"
[IME-DEBUG] _handleAnyTextareaChanges called, captured: "！@#"
[IME-DEBUG] keydown: # keyCode: 229 textarea: "！@#" isComposing: false
[IME-DEBUG] setTimeout callback, isComposing: false captured: "！@#" current: "！@#"
[IME-DEBUG] diff: "" t.length: 3 capturedValue.length: 3

[IME-DEBUG] textarea changed: "！@#" -> ""  ← xterm.js 清空 textarea
```

**结论：** 第一个全角符号 `！` 从未触发 `triggerDataEvent`，只有从第二个字符 `@` 开始才正常发送。
