# issue #152 证据脱敏记录

**日期**：2026-11-11
**范围**：本目录下将随提交入库的全部文件（`SUMMARY.md`、两个分析脚本、`raw/`）。原始未脱敏件留在仓库外，不随提交分发。
**口径**：AGENTS §9。`.bin` / `.ndjson` 中按字节偏移引用的证据采用**等长替换**；纯文本文档可改写。未改写任何历史提交。

## 1. 替换类别

| 类别 | 处理 |
| --- | --- |
| auth token / token 片段 | 不写入本目录；扫描 0 命中 |
| 主机名 / 本机账户名 | PTY 回显中的 shell 提示符等长替换为中性占位；文档不落真值 |
| Tailscale / 局域网 IPv4 | 等长替换为 RFC 5737 `203.0.113.0/24`、`198.51.100.0/24`；文档不落真值 |
| 含用户名的绝对路径 | 脚本改为相对本目录的 `raw/`；文档不落 home 路径 |
| 邮箱 / 电话 / 私钥 / API key | 扫描 0 命中 |

替换前断言原值与占位等长；替换后逐文件断言字节数与源一致。`raw/` 中 PTY 回显的 shell 提示符是主要命中点，等长替换不改变字节偏移。

## 2. 最终文件与字节数

| 文件 | 字节数 | 原始脱敏替换命中 |
| --- | ---: | ---: |
| `SUMMARY.md` | 9035 | 0（本次重写，未含敏感值） |
| `analyze-round.py` | 2018 | 1（路径字面量改为相对 `raw/`） |
| `attribute.py` | 3713 | 1（同上） |
| `raw/capture-ios.ndjson` | 160385 | 0 |
| `raw/capture-chrome.ndjson` | 36797 | 0 |
| `raw/pty-ios.bin` | 2159 | 42 |
| `raw/pty-chrome.bin` | 866 | 12 |
| `raw/pty-chrome-partial.bin` | 694 | 8 |

`MANIFEST.sha256` 覆盖上表全部文件（不含 manifest 自身）；在目录内执行 `sha256sum -c MANIFEST.sha256` 可复算。

## 3. 本次入库前的扫描结果

对最终文件逐字节扫描（token 按完整值与前 8/后 8 片段比对；主机名、账户名、home 路径、本机 IPv4 按实际值比对）：

| 类别 | 命中 |
| --- | ---: |
| 完整 auth token / token 前缀 / token 后缀 | 0 / 0 / 0 |
| API key `sk-…` / `Bearer …` / 私钥 | 0 / 0 / 0 |
| 主机名 / 本机账户名 | 0 / 0 |
| 含用户名的绝对路径 | 0 |
| Tailscale / 局域网 IPv4 | 0 |
| 邮箱 / 32-hex 串 | 0 / 0 |

## 4. 保留项

- RFC 5737 占位 IP、中性主机名/账户名占位（用于说明替换口径，不是真实身份）。
- iOS/iPadOS 版本、Safari/Chrome 版本、UA 字符串（用于复现，不含设备标识）。
- emoji、`cat -v`、`abc123`、拼音等测试字串；去掉就无法复算。
- `raw/` 中中性的 `/tmp` 沙盒路径（不含用户名），用于解释 PTY 回显上下文。

## 5. 有意不放入本目录

| 项 | 原因 |
| --- | --- |
| 原始 `daemon.log` | 守护启动可能打印 token；原始件留在仓库外 |
| 探针 `iosime_probe.js`、`cdp-repro.mjs`、`probe-smoke.js` | 诊断仪器不随证据入库；复现步骤已写入 `SUMMARY.md` |
| 合并原始 `probe.ndjson` | 与两份拆分的 capture 重复；原始件留在仓库外 |
| `RUNBOOK.md` / `READOUT.md` | 已压缩合并进 `SUMMARY.md`；沙盒 pid/端口/实例 id 等运行细节不入库 |
| `scrub-report.json` / `chrome-marks.json` | 旧的中间产物，与最终文件不一致；不随入库 |

## 6. 未做的事

- 未改写历史提交，未使用 `filter-branch` / `rebase` / `amend`。
- 未轮换、未改写、未删除认证 token。
- 上游 issue 仅包含英文事件链、字节与版本信息，不含本机路径、主机名、账户名、IP 或 token。
