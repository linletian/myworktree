# Issue #160 证据记录

> **目标调整（2026-11-13）**：本 issue 已从“适配 Reasonix 2.x”改为“移除 Reasonix Web 实例功能”。本文件只保留原问题与上游状态的证据，不再提供适配方案；实施范围和验收以 [PLAN.md](PLAN.md) 为准。

## 结论

**问题已在当前工作区基线 `f9e8615` 上通过真实 Reasonix v2.61.0 复现。** 当前 myworktree 的版本门禁、子进程启动、实例状态与反代都能工作，但 `GET /rx/<id>/` 返回的是上游 **404 API-only 占位页**，不是聊天 UI。

根因不是端口或 iframe 缓存，而是：

1. [driver.go](../../../internal/instance/reasonix/driver.go#L123-L127) 只设 `>=1.22.0` 下限，v2.61.0 会通过；
2. [driver.go](../../../internal/instance/reasonix/driver.go#L292-L300) 的启动参数没有 `--page`；
3. v2.61.0 不再内嵌 v1 聊天页，找不到 Studio `dist` 时由上游 `index` 明确返回 `page.not_built`；
4. 当前就绪判断只等待 port/pid 文件（[driver.go](../../../internal/instance/reasonix/driver.go#L389-L409)），不检查聊天页；前端仍把该地址放进 iframe（[reasonix.js](../../../internal/ui/static/kinds/reasonix.js#L177-L189)）。

## 1. Issue 与上游事实来源

- 本仓库 [issue #160](https://github.com/linletian/myworktree/issues/160)：报告 v1.31.4 升级到 v2.61.0 后页面无法加载，静态判断为上游移除内嵌页面。
- 上游 [v2.61.0 `page_missing.go`](https://github.com/esengine/DeepSeek-Reasonix/blob/v2.61.0/internal/frontend/serve/page_missing.go)：占位页标题为 `Reasonix server: no web page`，正文明确写着 standalone CLI 只提供 API，`writePageMissing` 固定返回 **404**。
- 上游 [v2.61.0 `serve.go`](https://github.com/esengine/DeepSeek-Reasonix/blob/v2.61.0/internal/frontend/serve/serve.go)：`index` 在 `s.page == nil` 时按 `Accept` 返回上述 HTML 或 `page.not_built` JSON；有页面时直接在 `GET /` 返回 `index.html`。
- 上游 [v2.61.0 `page.go`](https://github.com/esengine/DeepSeek-Reasonix/blob/v2.61.0/internal/frontend/serve/page.go)：显式 `--page` 目录必须有 `index.html`；空值时查找二进制旁及少量相对位置的 `frontend-next/dist`。
- 上游 [v2.61.0 `auth.go`](https://github.com/esengine/DeepSeek-Reasonix/blob/v2.61.0/internal/frontend/serve/auth.go)：`reasonix_token` cookie 快速认证路径仍存在；另有 URL fragment → `/auth/token` → HttpOnly cookie 的浏览器引导路径。
- 上游 [v2.61.0 `vite.config.ts`](https://github.com/esengine/DeepSeek-Reasonix/blob/v2.61.0/desktop/frontend-next/vite.config.ts)：Studio 是 Vite 构建，`base: "./"`，不是 v1 的单文件内联 SPA。
- 上游 [v2.61.0 发布说明](https://github.com/esengine/DeepSeek-Reasonix/releases/tag/studio-v2.61.0)：这是上游最后版本，之后不再维护。
- 上游 [停止维护公告](https://github.com/esengine/DeepSeek-Reasonix/discussions/15110) 与 [只读仓库](https://github.com/esengine/DeepSeek-Reasonix)：自 2026-11-12 起不再有新版本、缺陷/安全修复或 Issue/PR 处理；已有安装可继续用，允许 MIT fork，但不转交仓库、不指定接手人。公告和维护者后续答复均未公开具体停维护动机，本证据不采用相关猜测。

### 对 issue 推测的纠正

`serve --help` 的 `-page` 文案仍写着 `served under /_studio/`，但同一标签的实际 `page.go`、`serve.go` 与 [`web_entry_test.go`](https://github.com/esengine/DeepSeek-Reasonix/blob/v2.61.0/internal/frontend/serve/web_entry_test.go) 表明：**页面在 `/` 和 `/sessions/{id}` 直接返回，文件按真实路径提供，没有根到 `/_studio/` 的重定向。** 该纠正用于否定旧的 `/_studio/` 适配设想；当前目标已改为移除功能，不再设计任何 Studio 映射或 `Location` 重写。陈旧帮助文本只记录为上游文档问题，不在本仓库试修。

## 2. 当前代码的失效前提

| 当前行为 | 位置 | 证据/影响 |
|---|---|---|
| 只检查最低版本 | [driver.go](../../../internal/instance/reasonix/driver.go#L262-L273) | v2.61.0 通过，不能代表 UI 契约仍可用 |
| 版本通过缓存只按路径命中 | [driver.go](../../../internal/instance/reasonix/driver.go#L106-L116) | 同路径自升级后，长驻 daemon 还可能沿用旧结论；本探针是新进程，未把该风险写成运行时实测 |
| 启动时不传页面目录 | [driver.go](../../../internal/instance/reasonix/driver.go#L292-L300) | CLI 包旁没有 `frontend-next/dist` 时只启动 API kernel |
| TCP/port/pid 就绪即成功 | [driver.go](../../../internal/instance/reasonix/driver.go#L418-L446)、[kind.go](../../../internal/instance/reasonix/kind.go#L75-L120) | 实例可进入 `running`，但浏览器没有聊天页 |
| 反代剥前缀并注入 cookie | [reasonix_proxy.go](../../../internal/app/reasonix_proxy.go#L70-L110) | 传输链路仍工作；不是 502/超时 |
| 所有 HTML 都注入前缀 shim 与旧布局 | [reasonix_proxy.go](../../../internal/app/reasonix_proxy.go#L114-L141) | 占位页也会被注入；v2 Studio 的 `.app` 可能受到 v1 CSS 污染 |
| 旧布局依赖 v1 DOM | [reasonix_proxy.go](../../../internal/app/reasonix_proxy.go#L305-L403) | `.app/.sidebar/.transcript/.footer/#menu-btn` 不是 Studio 契约 |
| 上游契约脚本不请求聊天页 | [verify-reasonix-contract.sh](../../../scripts/verify-reasonix-contract.sh#L75-L84) | 只检查版本、session/lease，缺页不会失败 |
| CI 无二进制时绿色跳过 | [go-ci.yml](../../../.github/workflows/go-ci.yml#L75-L80) | 该步骤当前不构成真实上游 UI 门禁 |

## 3. 沙盒复现

### 隔离与边界

- 所有者：本次 issue #160 分析任务。
- 独立 worktree：`/tmp/mw-issue160-sandbox`，detached HEAD `f9e8615`。
- 独立测试二进制：沙盒内 `.issue160-runtime/app.test`，由 `go test -c ./internal/app/` 生成。
- 真实上游二进制：本机 npm 包中的 Linux x64 ELF；`reasonix --version` 输出 `reasonix v2.61.0`，SHA-256 为 `ad3da91966244aa097dc4fe40c4164ef1de8886963082bc745d4bc41747ddc6f`。
- npm wrapper 与 optional package 元数据仍标 `1.22.0`，但被执行的原生二进制已就地升级为 v2.61.0；这与 issue 背景一致。
- 一个临时 `reasonix serve`，绑定 `127.0.0.1:0`；`REASONIX_HOME`、`REASONIX_STATE_HOME`、`TMPDIR`、管理目录均指向沙盒。未连接生产 daemon，未启动浏览器。
- 沙盒测试文件 `internal/app/issue160_probe_test.go` 只是诊断探针，不进入主工作区或修复提交。

### 探针路径

探针复用当前真实实现，而非重写一套复现逻辑：

```text
framework.Manager.Start(kind=reasonix)
  → reasonix.Driver.Start（真实 v2.61.0，现有参数，无 --page）
  → 等 port/pid，并等实例状态从 starting 到 running
  → httptest 请求当前 reasonixProxy：GET /rx/<id>/
     Accept: text/html,application/xhtml+xml
  → 当前 Director 剥前缀、注入每实例 cookie
  → 当前 ModifyResponse 执行 HTML 重写和注入
```

探针只有在以下条件同时成立时才通过：实例已进入 `running`；响应含 `no web page`；v1 聊天 DOM 标记全部缺席；当前反代注入确实发生。它是在“证实 bug 存在”，不是未来的回归测试。

### 脱敏结果

最终确认运行返回码为 0，关键输出原样保留如下；不含 token、实例 ID、真实端口或本机用户路径：

```text
build_rc=0
probe_rc=0
issue160 reproduction: version gate passed; instance=running; proxy_status=404; content_type="text/html; charset=utf-8"; placeholder=true; v1_app=false; v1_menu=false; v1_sidebar=false; v1_transcript=false; prefix_injected=true; response_bytes=5347
PASS
```

这直接证明：**服务健康与页面可用在当前实现里被错误地当成同一件事；占位页经过的正是用户 iframe 使用的反代路径。**

### 探针迭代说明

同一探针只复跑过两次，均有明确归因，不是无条件重跑：

1. 第一次把 `Manager.Start` 返回后的瞬时 `starting` 错当成最终状态；改为有界等待既有 watcher 转为 `running`。
2. 第二次已观察到真实占位响应是 404，而探针预设成 issue 中尚未验证的 200；改为上游源码和实测共同确认的 404。
3. 第三次通过，此后停止运行时探针。

## 4. 已证实与未证实

### 已证实

- v2.61.0 通过当前最低版本门禁并能被当前 driver 拉起。
- 无 `--page`、二进制旁无页面时，当前 `/rx/<id>/` 返回 404 HTML 占位页。
- v1 的 `<div class="app">`、`id="menu-btn"`、`class="sidebar"`、`class="transcript"` DOM 标记均不在该响应中；注入 CSS 里出现的选择器名称不算聊天 DOM。
- 当前反代会向该占位页注入前缀脚本。
- 上游 v2.61.0 的 `reasonix_token` cookie 认证代码仍存在。

### 原适配路线的未验证项（现已取消）

以下仅保留原调查边界，**不再是本 issue 的实施或验收任务**，也不得据此宣称曾完成 Studio 适配：

- 真正 Studio `frontend-next/dist` 配合 `--page` 的端到端页面加载；本机没有该目录，本轮未下载或安装 Studio。
- Studio 的 JS/CSS、动态分块、SPA 深链、`/runtimes`、SSE 与提交消息在 `/rx/<id>/` 下的完整浏览器行为。
- v2 页面模式下 cookie 的运行时认证；无页面根路径本身可公开展示，所以本次 404 不能证明 cookie 已被接受。
- v1.31.4 实物回归；本轮只使用 v2.61.0。
- 其他平台 Studio 安装路径；方案只接受显式目录和上游自身自动发现，不猜测平台路径。

## 5. 认证与清理记录

- 沙盒前后均记录 `$HOME/.config/myworktree/auth.json` 的大小、mtime 和 SHA-256 基线；最终复核结果见本节末尾的清理确认。仅计算文件级基线，未复制、展示、写入、清除或轮换其中的 token，未执行 `mw config set-auth/clear-auth`。
- driver 按既有逻辑创建的每实例 Reasonix 临时 token 只存在于沙盒管理目录；没有 token 值进入本文、终端摘要或计划文档。
- 原始 serve 日志、临时 token、测试二进制和诊断探针均不归档；只归档上面的脱敏结论与输出。
- 清理动作：停止自有 serve（测试 `Stop`/cleanup）→ 确认无沙盒测试/serve 进程 → `git worktree remove --force /tmp/mw-issue160-sandbox` → 确认路径和 worktree 注册消失、全局认证文件未变。

> **清理确认（已完成）**：自有测试/serve 进程在删除前后均为 0；`/tmp/mw-issue160-sandbox` 已删除且不再注册为 git worktree；临时 token、原始日志、测试二进制与诊断探针随沙盒删除。`auth.json` 的大小、mtime、SHA-256 与沙盒前基线完全一致，认证 token 未被改动。
