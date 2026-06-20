# ghostty-web 调研报告：与 myworktree 的对比与借鉴

**调研日期**: 2026-06-19
**目标项目**: https://github.com/anomalyco/ghostty-web
**调研目的**: 评估 ghostty-web 是否可作为 myworktree 前端终端渲染器（xterm.js）的替代方案
**结论摘要**: ghostty-web 精准命中 myworktree 已记录的两类 xterm.js 痛点（OSC/DA 查询回声、复杂脚本渲染），推荐作为可选 renderer 引入，feature flag 灰度过渡。

---

## 1. 项目背景

**ghostty-web** 是 Coder 公司 fork 自 `coder/ghostty-web` 的一个 TypeScript 库，把原生 [Ghostty](https://github.com/ghostty-org/ghostty) 终端模拟器的解析层编译成 WASM，在浏览器里跑出 xterm.js 兼容的 `Terminal` API。

**核心卖点**（来自上游 README）：
- 与 xterm.js 完全 API 兼容：「Migrate from xterm by changing your import: `@xterm/xterm` → `ghostty-web`」
- WASM 编译的 VT 解析器，与原生 Ghostty 共用同一份经过实战检验的代码
- 零运行时依赖，~400KB WASM bundle
- 修掉 xterm.js 的两个长期 issue：
  - 复杂脚本（Devanagari、Arabic）渲染
  - XTPUSHSGR/XTPOPSGR 等控制序列缺失
- 依赖 Mitchell Hashimoto 正在做的 [libghostty](https://mitchellh.com/writing/libghostty-is-coming)，目前 patch 极小（见 `patches/ghostty-wasm-api.patch`）

**目录结构**：
```
ghostty-web/
├── lib/            # TS 库主体（Terminal、Buffer、Renderer、InputHandler、SelectionManager、LinkDetector…）
├── demo/           # Web demo，连接真实 shell
├── dist/           # 构建产物
├── ghostty/        # 子模块：upstream Ghostty 源码
├── patches/        # Ghostty 的 WASM 补丁
├── scripts/        # 构建脚本
├── bench/          # 基准测试
├── AGENTS.md       # AI 协作指南
├── biome.json      # Biome 格式化 + lint 配置
├── bun.lock        # Bun 依赖锁
├── vite.config.js  # Vite 构建
└── flake.nix       # Nix 开发环境
```

---

## 2. 与 myworktree 的相同点

两者都面向「**浏览器里的终端体验**」，且都把一个终端渲染器 vendored 到前端用：

| 维度 | ghostty-web | myworktree |
|---|---|---|
| 终端渲染器 | `ghostty-vt.wasm`（Zig→WASM 的 Ghostty VT 解析器） | `xterm.js v6.0.0` + `xterm-addon-fit`（vendor 在 `internal/ui/static/vendor/`） |
| 接入方式 | 前端 JS 库 + WASM | 前端 `<script>` 标签引用 vendored 文件 |
| 核心 API | `Terminal` / `write` / `onData`（xterm.js 兼容） | `Terminal` / `fitAddon.fit()` / `term.write` / `term.onData` |
| 部署形态 | npm 包（库） | Go 二进制 + 内嵌 `index.html`（应用） |
| License | MIT | MIT |
| Demo | 内置 web demo，连真实 shell 跑 `npx @ghostty-web/demo@next` | `mw` 自带 web UI + PTY 实例 |

### 2.1 关键交集：都吃 xterm.js 的亏

ghostty-web 的 README 直接点名 xterm.js 在 OSC 11/DA/CPR 等查询序列上的 bug；myworktree 的 `docs/TERMINAL_FILTER_REVIEW.md:45-54` 记录了**完全一样的问题**（实例时 `11;rgb:...`、`1;2c`、`;1R` 串到 PTY 里污染 TUI），并且专门写了一个 `isTerminalQueryResponse()` 前端过滤器去兜底。

这是少见的「第三方库的 README 第一条吐槽就精准命中你的痛点」的情况。

---

## 3. 与 myworktree 的不同点

| 维度 | ghostty-web | myworktree |
|---|---|---|
| 角色 | **库**（只做 VT 解析 + 渲染） | **应用**（worktree + 实例 + 标签 + 鉴权 + MCP + Portal 全套） |
| 语言 | TypeScript + Zig | Go |
| 进程管理 | ❌ 不管 | ✅ `internal/instance` 管理 PTY 生命周期、Tag 模板、restart 链式记录 |
| Git worktree | ❌ | ✅ `internal/gitx` + `internal/worktree` |
| 服务端传输层 | Demo 用 WS，但库本身无服务 | ✅ WS + SSE + polling 三档降级 + 状态显示 |
| MCP | ❌ | ✅ `/api/mcp/tools`, `/api/mcp/call` |
| 鉴权 / Portal | ❌ | ✅ 全局 token + HttpOnly Cookie + CSRF + Tailscale + Portal 反向代理 |
| TUI 输入净化 | ✅ 渲染器层面修掉 | ⚠️ 前端正则过滤 + 后端 redact 已禁用（见 `TERMINAL_FILTER_REVIEW.md:14-22`） |
| 复杂脚本/IME | ✅ "Proper grapheme handling" | ⚠️ 有专门的 `docs/CHINESE_IME_ANALYSIS.md`（中文 AI CLI 场景痛点） |
| 构建链 | Bun + Vite + WASM patch | `go build` + `scripts/download-xterm.sh` 拉 vendor |
| 包大小 | ~400KB WASM、零运行时依赖 | xterm.js v6 + addon-fit，零依赖 |
| 成熟度 | 预发布（无 GitHub release、依赖上游 libghostty） | 已发 v0.2.0，darwin amd64/arm64 release |

---

## 4. 可以借鉴的地方

### 4.1 直接替换 xterm.js（最大收益）

README 原话：「**Migrate from xterm by changing your import: `@xterm/xterm` → `ghostty-web`**」。myworktree 用了 ~3500 行 `internal/ui/static/index.html`，但 `Terminal` / `FitAddon` / `write` / `onData` / `onResize` 调用面是兼容的，理论上是个**单点替换**。

落地步骤：
- 删 `scripts/download-xterm.sh` → 新增 `scripts/download-ghostty-web.sh`（拉 wasm + js + css + 完整性校验）
- `internal/ui/static/index.html:1346-1348` 把 `<script src="/static/vendor/xterm/xterm.js">` 换成 `<script type="module">` 引入 ghostty-web
- `new Terminal(...)` 前加 `await init()`
- 调用点大概率零改动

直接收益：
- 干掉 `internal/ui/ui_test.go:41-51` 里的 `isTerminalQueryResponse()` 前端过滤器（ghostty 自身不会把这些 OSC/DA 回声送回 `onData`）
- 干掉 `internal/redact` 里那段坏过 TUI 的 `controlSeqRemnant`（已禁用但代码还在）
- 解决 `CHINESE_IME_ANALYSIS.md` 里的复杂脚本/IME 渲染问题——目标用户是 Claude/MiniMax/Qwen 中文 CLI
- 顺手干掉 vendor 目录里独立维护的 `xterm-addon-fit`（ghostty-web 内置 fit）

### 4.2 借鉴「API 兼容」策略做替换过渡

ghostty-web 没强行让用户改业务代码，只换 import。这是 myworktree 可以直接照抄的迁移方法论：**不破坏既有 3500 行 `index.html` 的存量调用**。

具体落地建议保留 `isTerminalQueryResponse()` 过滤器一两个版本作为 fallback（feature flag），确认 ghostty-web 在 PTY 回放 / TUI 应用（opencode、Claude Code、lazygit）这些场景下都稳了再删。

### 4.3 借鉴 demo / 验证流程

ghostty-web 有 `demo/index.html` 跑真实 shell + ephemeral VM 做端到端验证。myworktree 的 `internal/ui/ui_test.go` 目前是字符串级断言（检查 `let terminalSessions = {}` 出现），可以借鉴 ghostty-web 的 `lib/*.test.ts`（`buffer.test.ts`、`renderer.test.ts`、`selection-manager.test.ts` 等）做基于真实渲染产物的快照测试——对 `codecliteams.png` / `webui.png` 演示质量会有帮助。

### 4.4 借鉴「上游合并」策略

`patches/ghostty-wasm-api.patch` 维持极小补丁，跟上游 libghostty 演进。这是和 myworktree `internal/redact` 那段历史教训（regex 误伤 TUI）**互为镜像**的教训：别在渲染/解析层硬塞项目特定逻辑，改用配置/hook。myworktree 这边可以反过来审视——前端那坨 `isTerminalQueryResponse` 正犯了同样的错。

### 4.5 不建议直接照搬的点

- **WASM 加载 + 完整性校验**需要新增（参照 `scripts/download-xterm.sh` 现有 pattern）
- **400KB WASM** 比 xterm.js v6 大，但换来零运行时依赖 + 复杂脚本正确性，trade-off 划算
- **License 归因**：`NOTICE` 要追加 ghostty-web (MIT) + Ghostty (MIT) + 任何 patch
- **成熟度风险**：ghostty-web 没有 release、依赖未发布的 libghostty、`patches/ghostty-wasm-api.patch` 注释说「we expect them to get smaller」——myworktree 的卖点是「实例常驻不掉」，渲染层引入预发布 WASM 库需要评估故障面
- **测试断言**：现有 `ui_test.go:41` 显式断言 `let terminalSessions = {}` 字符串，迁移后要更新

---

## 5. 推荐落地顺序

1. **新增** `scripts/download-ghostty-web.sh`（与现有 `download-xterm.sh` 并存）
2. **加 feature flag** `mw -terminal-renderer=xterm|ghostty`（默认 xterm 不变）
3. **shadow 模式跑一版**——同时挂两个 renderer，对比 PTY 输入回显是否还有 `1;2c` 这类噪声
4. **CI 加 `lib/*.test.ts` 风格的快照测试**（buffer / 渲染输出）
5. **稳了再默认切 ghostty-web**，删前端 `isTerminalQueryResponse` 过滤器、`redact.go` 里的相关代码、`xterm-addon-fit`

---

## 6. 关键判断

- **收益明确**：替换后能消掉 `TERMINAL_FILTER_REVIEW.md` + `CHINESE_IME_ANALYSIS.md` 两份文档描述的痛点，对中文 AI CLI 工作流尤其友好
- **风险可控**：xterm.js 路径保留为默认 + feature flag，灰度切换，故障时可一键回滚
- **维护成本**：新增一个 vendor 下载脚本 + 一份完整性校验，长期减少对 `isTerminalQueryResponse` 正则黑名单的维护负担
- **战略对齐**：ghostty-web 依赖 Mitchell Hashimoto 正在做的 libghostty（Ghostty 官方 WASM 化路径），押注 Ghostty 生态是低风险长期赌注
