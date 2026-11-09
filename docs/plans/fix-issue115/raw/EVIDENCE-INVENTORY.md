# 证据清单（EVIDENCE-INVENTORY）

所有文件均在 `docs/plans/fix-issue115/raw/`，校验用 `md5sum -c MANIFEST.md5`（在仓库根目录执行）。
**证据里不含 token**（已核：token 出现次数＝0）。

| 文件 | 字节 | 覆盖范围 | 支撑的结论 |
| --- | --- | --- | --- |
| `iphone-on.capture.console.txt` | 204,728 | iPhone A 腿**全部 13 段**（自装探针起） | B-1 / B-2 / B-6 / 桥接必要性 / 分支 C / S2 / S3 |
| `iphone-off.capture.console.txt` | 54,330 | iPhone B 腿**全部 9 段** | 关桥基线（直提交全灭） |
| `iphone-on.pty.full.bin` | 145,373 | 环缓冲**全量**输出流（含 vim alt-screen 进出） | 终端侧可见行、`?2004` 开关次数、焦点回报 |
| `iphone-off.pty.full.bin` | 53,616 | 同上（B 腿） | 同上 |
| `iphone-on.pty.bin` / `iphone-off.pty.bin` | 6,272 / 4,356 | 早期快照（21:00 前） | **iPad 切片的字节切点参照**（勿删） |
| `iphone-on.visible.txt` / `iphone-off.visible.txt` | — | 净化后可见行 | 一行级硬判据（0 vs 非 0） |
| `ipad-on/off.capture.console.txt` | 18,898 / 7,533 | iPad 段（自装探针起） | iPad 与 iPhone 同路径、无候选栏 |
| `ipad-on/off.pty.bin`、`.visible.txt` | — | iPad 终端切片与可见行 | iPad 桥接必要性 |
| `iphone-datapack.txt` | 4,002 | 首轮量化指标包 | 交付/到达计数 |
| `round-index-iphone-on/off.tsv` | — | 逐轮时间戳＋按键签名 | 本索引的机读版 |
| `s1-vim-t5.txt.saved` | 43 | vim 落盘原文 | **按腿切开 A31＋B12**（§4.3） |
| `s1-vim-decomposition.md` | — | 上述逐字节解剖 | 同上 |
| `s2-vim-paste-target.txt` | 86 | 粘贴靶文（5 行 4 空格） | **零缩进级联** |
| `s2-c-s3-readout.md` | — | 三组读数＋诚实边界 | 分支 C 结案、B-3 未复现、粘贴无损（**已被下一行修正**） |
| `e1-e2-readout.md` | — | emoji 与 autoindent 收口轮 | **B-8 复活**（级联 4/8/tab+4/tab+8/2tab+4）、**B-5 emoji 通过＋⚽️ 重复** |

## 已知边界（不算瑕疵，但读结论必须连着读）

1. **PTY 是环形缓冲**——全量已在本清单内（145,373 / 53,616），但**缓冲区满之后旧数据会滚动丢失**，故后续任何补测须**当天回传归档**，不能"下次再取"。
2. **控制台采集只收 `[ios-ime]` / `[probe]` / `[quick-keys]` 前缀**；页面自身其它 `console.log` 不在内。
3. **B 腿无桥接埋点**（桥已被编掉）⇒ B 腿段没有桥接时间戳，先后靠 PTY 顺序与文件 mtime 锚定。
4. **`iphone-{on,off}.pty.bin`（21:00 快照）已被 `.full.bin` 涵盖**，保留仅为 iPad 切片的字节切点参照；`/tmp` 里被替换掉的旧副本已在回收站。
5. **一次误操作**：文档正文曾被写进 shell 反引号导致其中命令被误执行、误开一个 vim。已核查 `/tmp/t5.txt`、`/tmp/p.txt` 与 git 均无损；**残留孤儿 swap `/tmp/.p.txt.swp` 未删**（担心有活跃 vim 持有该文件）。
