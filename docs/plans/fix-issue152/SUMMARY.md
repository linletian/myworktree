# issue #152：VS16 emoji 双写取证与处置

**日期**：2026-11-11
**结论**：根因在 vendored xterm v6 的输入事件处理，不在本仓库的 ios-ime 桥接。本仓库不改产品代码；issue #152 关闭不处理；上游已提交 [xtermjs/xterm.js#6320](https://github.com/xtermjs/xterm.js/issues/6320)。
**证据**：`raw/` 下为脱敏副本；原始未脱敏件留在仓库外，不随提交分发。

## 1. 现象

同一图形被发两遍。vendored xterm v6 有两条发送通道：

- `_keyPress` 发 `String.fromCharCode(charCode)`，即 BMP 基字符（碎片）；
- `_inputEvent` 发 `ev.data`，即完整字形。

去重依赖 `_keyPressHandled` / `_keyDownSeen`，而 `_keyUp` 会把它们清零。事件顺序决定结果：

| 事件顺序 | 结果 |
| --- | --- |
| `keyup` 在 `insertText` 之前（iOS/iPadOS 实测） | 标志被清零 ⇒ `_inputEvent` 放行 ⇒ 多一个基字符（`❤❤️`、`⚽⚽️`） |
| `insertText` 在 `keyup` 之前（CDP 合成对照） | 标志仍在 ⇒ `_inputEvent` 被挡 ⇒ 少一个 VS16（`❤`、`⚽`） |

**不是 iOS 专属，也不是 #115 桥接引入**；桥接全程零 `deliver` / `reconcile`。

## 2. 证据

### 2.1 真机计数

| 来源 | VS16 敲击 | 双写 | `keypress` charCode | 未双写 |
| --- | --- | --- | --- | --- |
| iPhone（iOS 27.3 / Safari 27.3） | 9 | **7（100%）** | U+2764 ×4、U+26BD ×3 | `⭐️`、`⛄️`（无 keydown/keypress） |
| iPad（iPadOS 27.2 / Safari 27.2） | 2 | **2（100%）** | U+2764 ×2 | — |
| 真机合计 | 11 | **9** | **9** | **2，两次都无 keypress** |
| headless Chrome 157（CDP 合成序） | 2 | **2** | 4（A 段 2 + B 段 2） | B 段 2 次 = VS16 被吞 |
| 全部 | 13 | **11** | **13** | 另有 2 次 VS16 丢失 |

> 设备版本为操作者确认的实际测试版本；capture 的 UA 报 `CPU iPhone OS 18_7` / `CPU OS 18_7`，与设备设置版本不一致，故此处不采用 UA 的 OS 版本。

`attribute.py` 输出：`BMP-charCode keypress(13) == 双写(11) + VS16 丢失(2)`；每次带 BMP `charCode` 的 `keypress` 都产生一次碎片发送。真机侧 `9 : 9` 零例外。

### 2.2 单次完整链（iPhone，`❤️`，`m=performance.now()`）

```
m=40544 keydown   kc=0     key='❤️'          textarea 未变
m=40599 keypress  kc=10084 charCode=U+2764   ← 碎片发送的触发点
m=40600 TX        e2 9d a4                    '❤'    ← _keyPress
m=40608 keyup                                  ← 清零 _keyDownSeen / _keyPressHandled
m=40621 beforeinput insertText '❤️'
m=40624 input → TX e2 9d a4 ef b8 8f          '❤️'   ← _inputEvent
```

`textarea.value` 始终只有一份 `❤️`；重复只发生在线上，不在输入框。`keypress` bubble 相 `defaultPrevented=false`（`cancelEvents` 为 false，`cancel()` 不阻止默认行为）。

### 2.3 跨平台合成

用 CDP `Input.dispatchKeyEvent` / `Input.insertText`（浏览器输入层，不是 JS `dispatchEvent`）驱动同一份 stock 6.0.0：

| 实验 | 注入序 | wire | 结果 |
| --- | --- | --- | --- |
| A（= iPhone 序） | keyDown → char(`❤`) → keyUp → insertText(`❤️`) | `e2 9d a4` + `e2 9d a4 ef b8 8f` | ❌ `❤❤️` |
| A（⚽） | 同上 | `e2 9a bd` + `e2 9a bd ef b8 8f` | ❌ `⚽⚽️` |
| B（对照） | keyDown → char(`❤`) → insertText(`❤️`) → keyUp | 仅 `e2 9d a4` | ⚠ 不重复，但 VS16 丢 |
| C（astral） | keyDown → keyUp → insertText(`😀`) | 单帧 | ✅ |
| D（真键盘 ASCII） | keyDown(text) → keyUp | 每字符一帧 | ✅ |

> 口径说明：A/B 是合成序。A 与 iPhone 实测序一致，故“任何按此序派发的 UA 都复现”成立。B 的 capture 中 CDP 的 `char` 注入本身还会产生一次基字符 `insertText`，不是干净的单次敲击模型；它证明的是 xterm 会挡掉两次 `insertText`，不是真实 B 序 UA 的精确 wire。真实 B 序（一次完整字形插入在 keyup 前）仍会只发碎片、吞 VS16。
>
> CDP 的 `Input.dispatchKeyEvent` 必须显式设置 `key: "❤️"`，否则 `ev.key` 为空，事件形态与真机不一致。

astral `😀 🍎 👍`、ASCII `abc123`、拼音 `你好` 均单帧，无附带伤害。

## 3. 根因（vendored xterm v6）

`internal/ui/static/vendor/xterm/xterm.js`（6.0.0）：

```js
_keyPress(ev) {
  this._keyPressHandled = false;
  ...
  key = String.fromCharCode(ev.charCode);   // '❤' only
  triggerDataEvent(key, true);
  this._keyPressHandled = true;
}

_keyUp(ev) {
  this._keyDownSeen = false;
  ...
  this._keyPressHandled = false;
}

_inputEvent(ev) {
  if (ev.data && ev.inputType === 'insertText' &&
      (!ev.composed || !this._keyDownSeen) &&
      !screenReaderMode) {
    if (this._keyPressHandled) return false;
    triggerDataEvent(ev.data, true);
  }
}
```

- iOS 序：`keyup` 先清 `_keyPressHandled`，随后 `input` 放行完整字形 ⇒ 碎片 + 完整字形。
- 反向序：`input` 到达时 `_keyPressHandled` / `_keyDownSeen` 仍为 true ⇒ 完整字形被挡 ⇒ 只剩基字符。
- 真正的开关是“该次敲击是否派发带 BMP `charCode` 的 keypress”，不是“BMP 基字符 + VS16 必双写”：`⭐️`、`⛄️` 走无 key 事件的插入路径，正常。

## 4. 为什么本仓库不改产品代码

1. **guard-only 有已知反作用**：在 keypress 阶段拦截碎片可以修好 iOS 序，但在“insertText 早于 keyup”的合成序下，`_inputEvent` 仍会被 `_keyDownSeen` 挡住，结果是**整字丢失**，比现在的“只丢 VS16”更差。该序目前没有真实 UA 证据，但不能当作不存在。
2. **兜底未设计/未验证**：现有 `iosImeEmissions` 只在 229 窗口内记录，非 229 emoji tap 没有 watermark；补发还要处理模型同步、重入标志、多次 `insertText` 合并，属于第二个独立改动。
3. **不改 vendored bundle**：会破坏下载脚本的完整性校验与原子安装纪律。
4. **不升级 xterm**：当前 stable 仍是 6.0.0；master 上同一逻辑仍在，相关上游修复（#6200/#6009/#6313）针对的是同一 gate 的其他面，不是本 VS16 双写。

因此本轮只归档证据、关闭 issue，并把问题交给上游；若上游修复或未来升级 xterm，再按新的 bundle 行为重新评估。

## 5. 复现与判读

```sh
# 在仓库根目录运行；只读，不需要网络
python3 docs/plans/fix-issue152/attribute.py
python3 docs/plans/fix-issue152/analyze-round.py
python3 docs/plans/fix-issue152/analyze-round.py raw/capture-chrome.ndjson
```

- `attribute.py`：按 `installed` 的 UA + seq 链归属批次，输出每台设备的敲击/双写/BMP keypress。
- `analyze-round.py`：重建 `WebSocket.send` 的逐帧 wire，打印字节计数与首个双写链。
- `raw/pty-ios.bin`、`raw/pty-chrome.bin`、`raw/pty-chrome-partial.bin`：daemon PTY 回显的独立复核；与 tx 帧逐字节一致。

`attribute.py` 里的 `dropped: 0 expected` 是提示文本；真正的 `dropped=0` 来自 capture 首行的 `probe-152-v1 dropped=0`。

## 6. 已知局限

1. 第二批输入（`❤️`×3 / `⚽️`×3）只有客户端 tx 帧，PTY 回显未落盘；输入侧 tx 证据强于回显，不影响结论。
2. 探针未记录 `keypress.cancelable`，因此“preventDefault 被忽略”与“本就不可取消”未区分；不影响根因判断。
3. B 序是合成场景，未在真实桌面/辅助功能 UA 上观察到；其精确 wire 不能直接当作真实单次敲击基线。
4. iPad 会话未记录实例 id（旧探针只记 `sock=tty/ws`），归属依赖 `installed.ua` + seq 链。

## 7. 上游关联

| 编号 | 状态 | 关系 |
| --- | --- | --- |
| [xterm#6320](https://github.com/xtermjs/xterm.js/issues/6320) | open | 本问题的上游报告（英文，按 bug_report 模板） |
| xterm#6144 / #6200 | closed / merged | 同一 `_keyDownSeen` gate 的相反方向：input 早于 keydown 导致首字丢失；#6200 修了 modifier-only keydown 的 arm |
| xterm#6313 | open PR | 失焦时清 `_keyDownSeen` |
| xterm#6009 | open PR | composition 路径 reset `_keyDownSeen` + 取消 timeout 防 double-send（229 路径） |
| xterm#6045 / #5887 / #6078 / #6259 | open | 229 / `CompositionHelper._handleAnyTextareaChanges` 的重复或丢字 |
| xterm#6185 | open | 辅助功能插入被丢 + 多出一个字符，同一 gate 家族 |
| xterm#5374 | closed | Safari Shift 字符丢字，同一 gate；#6200 修 |

## 8. 归档与脱敏

- 本目录全部为脱敏副本；替换口径与扫描结果见 `REDACTION.md`。
- 未提交任何 token、API key、密码/私钥、主机名、本机账户名、私网/VPN IP、邮箱、绝对 home 路径或 32-hex 串。
- `MANIFEST.sha256` 覆盖本目录除 manifest 自身外的全部文件；在目录内执行 `sha256sum -c MANIFEST.sha256` 可复算。
- 原始未脱敏件留在仓库外；未改写任何历史提交，未使用 `filter-branch` / `rebase` / `amend` 清理旧历史。
