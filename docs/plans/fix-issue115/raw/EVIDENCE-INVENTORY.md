# 证据清单（EVIDENCE-INVENTORY）

所有文件均在 `docs/plans/fix-issue115/raw/`。**受校验文件 38 个 + `MANIFEST.md5` 本身**。

校验（仓库根目录）：

```sh
md5sum -c docs/plans/fix-issue115/raw/MANIFEST.md5
```

**脱敏**：私网 IP、主机名/用户名、token 前缀已替换为**等长占位符**（PTY 字节偏移不变），`MANIFEST.md5` 按脱敏后内容重算。映射、分支重写与校验记录见 `../REDACTION.md`。**证据中不含真实 token**（全分支扫描命中 0；完整 token 在任何提交中都未出现）。

---

## 一、批次与目录

| 批次 | 文件 | 覆盖 |
| --- | --- | --- |
| 2026-11-09 iPhone **A 腿（桥接开）** | `iphone-on.capture.console.txt`（13 段）、`final-on.capture.console.txt`（拆机前 14 段，超集） | B-1 / B-2 / B-3 / B-5 / B-6 / B-8 / 分支 C / 桥接必要性 / S2 / S3 / E1 / E2 |
| 2026-11-09 iPhone **B 腿（桥接整块编掉）** | `iphone-off.capture.console.txt`（9 段）、`final-off.capture.console.txt`（逐字节相同） | 关桥基线（直提交全灭） |
| 2026-11-09 **iPad 26键** | `ipad-on/off.capture.console.txt` | 同路径、无符号候选栏、B-2 不可达 |
| 2026-11-09 **F1 独立实例** `306fe8fa9254` | `final-hook-instance.capture.console.txt`、`f1-hook.pty.bin`、`f1-attribution.md` | 发送时刻 `mode=OFF` + 粘贴落点时间轴 |
| PTY 环缓冲 | `iphone-*.pty.*.bin`、`ipad-*.pty.bin`、`iphone-e1e2.pty.bin` | 终端侧原始字节（主判据） |

> **PTY 是环形缓冲**：`iphone-on.pty.full.bin`（145,373B）与拆机前的 `iphone-on.pty.final-before-hookrestart.bin`（181,408B）都是当时的全量窗口；**缓冲满后旧数据会滚动丢失**，后续补测必须当天回传归档。

---

## 二、文件表

| 文件 | 字节 | 覆盖 / 支撑 |
| --- | ---: | --- |
| `e1-e2-readout.md` | 3,574 | E1 emoji 到达 + `⚽️` 双写；E2 autoindent 级联与责任排除 |
| `e2-vim-p2.txt` | 98 | E2 落盘靶文（行首 `4/8/tab+4/tab+8/2tab+4`） |
| `e3-paste-wrapped.raw` | 97 | **输入侧正证**：`033[200~` 在首字节、`033[201~` 在末尾，6+85+6 |
| `e3-paste-wrapped.readout.md` | 2,715 | E3 判读 + R5/R6 闭环 |
| `f1-attribution.md` | 3,045 | F1 发送时刻读数、PTY 模式时间轴、B-8 定责、R7 |
| `f1-hook.pty.bin` | 9,970 | F1 实例 PTY 全量（模式时间轴可复算） |
| `f1-vim-cascade.txt` | 98 | F1 vim 级联落盘（与 `e2-vim-p2.txt` 逐字节相同） |
| `f2-cat-raw.txt` | 85 | F1 对照组裸文本（首字节空格、无 `033[`） |
| `final-A-instance306fe8fa9254.pty.bin` | 9,970 | 拆机前快照；**与 `f1-hook.pty.bin` 相同** |
| `final-B-instance227ff8642158.pty.bin` | 53,616 | 拆机前快照；**与 `iphone-off.pty.full.bin` 相同** |
| `final-hook-instance.capture.console.txt` | 23,551 | F1 实例控制台（2 段；含 `tap=paste` + `bytes mode=OFF`） |
| `final-off.capture.console.txt` | 54,330 | 拆机前快照；**与 `iphone-off.capture.console.txt` 相同** |
| `final-on.capture.console.txt` | 285,376 | 拆机前快照；`iphone-on` 的超集（第 14 段为探针重装） |
| `ipad-off.capture.console.txt` | 7,533 | iPad B 腿（仅 composition 产物 + 粘贴 URL） |
| `ipad-off.pty.bin` | 2,166 | iPad B 腿 PTY 切片 |
| `ipad-off.visible.txt` | 164 | iPad B 腿净化可见行（全角标点 0） |
| `ipad-on.capture.console.txt` | 18,898 | iPad A 腿（直提交 10 键、`deleteContentBackward`=0） |
| `ipad-on.pty.bin` | 6,218 | iPad A 腿 PTY 切片 |
| `ipad-on.visible.txt` | 255 | iPad A 腿净化可见行（逐字同步、零野引号） |
| `iphone-datapack.txt` | 4,002 | 首轮量化指标包 |
| `iphone-e1e2.pty.bin` | 31,628 | E1/E2 PTY 增量：emoji 5 个全达、`⚽`×2 + `VS16`×1、`?2004h/l` |
| `iphone-off.capture.console.txt` | 54,330 | iPhone B 腿 9 段 |
| `iphone-off.pty.bin` | 4,356 | 21:00 早期快照（iPad 切片切点参照） |
| `iphone-off.pty.full.bin` | 53,616 | iPhone B 腿 PTY 全量 |
| `iphone-off.visible.txt` | 1,672 | B 腿净化可见行（`，`=0、野引号 1） |
| `iphone-on.capture.console.txt` | 204,728 | iPhone A 腿 13 段（B-1/B-2/B-6/必要性/分支 C/S2/S3） |
| `iphone-on.pty.bin` | 6,272 | 21:00 早期快照（同上） |
| `iphone-on.pty.final-before-hookrestart.bin` | 181,408 | 重启钩子前的 PTY 全量（焦点回报可复算） |
| `iphone-on.pty.full.bin` | 145,373 | A 腿 PTY 全量（vim alt-screen、`?2004`、`^[[I/O`） |
| `iphone-on.visible.txt` | 2,679 | A 腿净化可见行（9键野引号 4 / 26键 0） |
| `round-index-iphone-off.tsv` | 481 | B 腿逐段机读索引（见 §四 的字段边界） |
| `round-index-iphone-on.tsv` | 650 | A 腿逐段机读索引（同上） |
| `ROUND-INDEX.md` | 2,627 | 轮次索引与时间锚点 |
| `s1-vim-decomposition.md` | 1,806 | `/tmp/t5.txt` 43B 按腿解剖（A31 + B12） |
| `s1-vim-t5.txt.saved` | 43 | vim 落盘原文 |
| `s2-c-s3-readout.md` | 2,278 | 分支 C 结案、B-3 未复现、S2 无损 |
| `s2-vim-paste-target.txt` | 86 | S2 粘贴靶文（5 行 4 空格） |

> `EVIDENCE-INVENTORY.md` 与 `MANIFEST.md5` 自身不计入上表的证据文件。

---

## 三、等价副本（不是独立证据）

| 副本 | 与谁相同 |
| --- | --- |
| `final-A-instance306fe8fa9254.pty.bin` | `f1-hook.pty.bin` |
| `final-B-instance227ff8642158.pty.bin` | `iphone-off.pty.full.bin` |
| `final-off.capture.console.txt` | `iphone-off.capture.console.txt` |
| `f1-vim-cascade.txt` | `e2-vim-p2.txt` |

`final-on.capture.console.txt` 是 `iphone-on.capture.console.txt` 的**超集**（多第 14 段：一次探针重装），不是独立轮次。拆机前另存 `final-*` 只为“归档先于删除”。

---

## 四、已知边界（读结论必须连着读）

1. **PTY 是环形缓冲**——全量见 `iphone-on.pty.full.bin` / `final-before-hookrestart`，但缓冲满后旧数据会滚动丢失；补测须当天回传。
2. **控制台采集只收 `[ios-ime]` / `[probe]` / `[quick-keys]` 前缀**；页面其它 `console.log` 不在内。
3. **B 腿无桥接埋点**（桥已被编掉）⇒ B 腿段没有桥接时间戳，先后靠 PTY 顺序与文件 mtime 锚定。
4. **`iphone-{on,off}.pty.bin`（21:00 快照）已被 `.full.bin` 涵盖**，保留仅为 iPad 切片的字节切点参照。
5. **TSV 的“按键序列”字段是近似摘要**：生成时丢了若干字符（如 `ntercatpace`），**不能当逐键证据**；以 `capture.console.txt` 为准。
6. **焦点回报是 `cat -v` 渲染后的字面 `^[[I` / `^[[O`**，不是 `ESC[I`；用 `ESC` 去搜会得 0。
7. **F1 的状态条文案不在采集范围**，`tap=paste` 2 次中只有 1 次产生 `bytes` 行（首次疑似 iOS 剪贴板读取在手势外被拒）。
8. **一次误操作**：文档正文曾被写进 shell 反引号导致命令被误执行、误开一个 vim；已核查 `/tmp/t5.txt`、`/tmp/p.txt` 与 git 均无损，残留 swp 已随拆机清理。
9. **脱敏后 md5 已重算**：与脱敏前的副本不通用；本清单与 `MANIFEST.md5` 只描述当前分支内容。
