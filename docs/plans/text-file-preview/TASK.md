# 文件改动预览功能 — 实施计划

> 基于：[FEASIBILITY.md](./FEASIBILITY.md)（方案 A 深化版）  
> 创建日期：2026-06-20  
> 总预估：约 8-11h

---

## Phase 1：后端 API（预估 1-2h）

### 任务清单

- [ ] **提取公共函数 `resolveWorktreePath(id string) (string, error)`**
  - 位置：`internal/app/app.go`
  - 内容：将 `handleWorktreeStatus` 中 worktree path 解析逻辑（行 866-887）提取为独立方法
  - 验证：`handleWorktreeStatus` 改用新方法后行为不变

- [ ] **实现 Diff API handler**
  - 路由：`GET /api/worktree/file/diff?id=<wt>&path=<f>&staged=true|false`
  - 位置：`internal/app/app.go`（新增 `handleWorktreeFileDiff` 方法）
  - 逻辑：`resolveWorktreePath` → `isPathWithin` → `s.gitRunner("diff", [--cached], "--", path)` → 返回纯文本
  - 响应头：`Content-Type: text/plain; charset=utf-8`
  - 错误处理：路径穿越返回 403；git 失败返回 500 + 错误消息

- [ ] **实现 Content API handler**
  - 路由：`GET /api/worktree/file/content?id=<wt>&path=<f>`
  - 位置：`internal/app/app.go`（新增 `handleWorktreeFileContent` 方法）
  - 逻辑：`resolveWorktreePath` → `isPathWithin` → 1MB 限制 → `isBinaryData` → 返回文件内容
  - 响应头：`Content-Type: text/plain; charset=utf-8`、`X-File-Type`（文件类型提示）、`X-File-FullPath`（完整路径）
  - 错误处理：路径穿越 403；文件过大 413；二进制 415

- [ ] **实现安全验证函数**
  - `isPathWithin(worktreeRoot, filePath string) bool`：防止路径穿越
  - 位置：`internal/app/app.go`

- [ ] **注册路由**
  - 在 `registerAPIs()` 中新增 2 行路由注册
  - 位置：`internal/app/app.go:527` 附近

### 产出文件

| 文件 | 改动类型 | 说明 |
|---|---|---|
| `internal/app/app.go` | 修改 | 新增 ~130 行 |

### 验证方式

```bash
# 本地启动 mw，测试 API
curl "http://localhost:<port>/api/worktree/file/diff?id=main&path=README.md&staged=false"
curl "http://localhost:<port>/api/worktree/file/content?id=main&path=README.md"
```

---

## Phase 2：前端预览页面（预估 3-4h）

### 任务清单

- [ ] **创建 `preview.html`**
  - 位置：`internal/ui/static/preview.html`
  - 内容：
    - 标题栏（项目名/工作区/分支/文件名 + 完整路径）
    - Tab 栏（Diff Preview / Formatted Preview）
    - Diff 渲染区（集成 highlight.js 或 Prism.js 的 `diff` 语言）
    - 格式化预览区（Markdown → marked.js；代码 → 高亮库；纯文本 → `<pre>`）
    - 错误/加载状态展示
  - 样式：明/暗双主题，与 `index.html` CSS 变量体系一致

- [ ] **下载 vendor 依赖**
  - `highlight.js`（或 `Prism.js`）：代码 + diff 语法高亮
  - `marked.js`：Markdown 渲染
  - 位置：`internal/ui/static/vendor/`
  - 注意：需确保下载的版本允许本地嵌入（查看各库的 LICENSE 文件）

- [ ] **注册 `/preview` 路由**
  - 位置：`internal/ui/ui.go`
  - 新增 `//go:embed static/preview.html` + `var previewHTML []byte`
  - 在 `Register` 函数中注册 `mux.HandleFunc("/preview", ...)`

- [ ] **更新 Open Source Licenses 列表**
  - 位置：`internal/ui/static/index.html` 的 `#monitor-licenses-list`（行 1374-1384）
  - 新增条目：`marked.js`（MIT）、`highlight.js`（BSD-3-Clause）或 `Prism.js`（MIT）

### 产出文件

| 文件 | 改动类型 | 说明 |
|---|---|---|
| `internal/ui/static/preview.html` | 新增 | ~300 行 |
| `internal/ui/static/vendor/highlight/` | 新增 | 代码高亮库 |
| `internal/ui/static/vendor/marked/` | 新增 | Markdown 渲染库 |
| `internal/ui/ui.go` | 修改 | 路由注册 ~10 行 |
| `internal/ui/static/index.html` | 修改 | Licenses 列表 ~4 行 |

### 验证方式

```bash
# 启动 mw，浏览器访问
open "http://localhost:<port>/preview?id=main&path=README.md&repo=myworktree&wt_name=main&branch=main&file=README.md"
```

---

## Phase 3：主页面点击事件（预估 0.5h）

### 任务清单

- [ ] **在 `renderGitChanges()` 中添加点击事件**
  - 位置：`internal/ui/static/index.html`（`changes.forEach` 循环内，约行 1768）
  - 为每个 `.git-change-item` 添加 `click` 事件监听
  - 调用 `openFilePreview(worktreeId, filePath, section)`

- [ ] **实现 `openFilePreview` 函数**
  - 位置：`internal/ui/static/index.html`
  - 从 `state` 收集元数据（项目名、工作区名、分支名）
  - 构建 URL query 参数
  - 调用 `window.open(url, '_blank')`

### 产出文件

| 文件 | 改动类型 | 说明 |
|---|---|---|
| `internal/ui/static/index.html` | 修改 | ~30 行 |

### 验证方式

1. 启动 mw，在 Staged/Unstaged 列表中点击任意文件
2. 应打开新窗口，显示 Diff 预览
3. 切换到 Formatted Preview tab，应显示文件内容

---

## Phase 4：安全加固（预估 1h）

### 任务清单

- [ ] **实现敏感文件黑名单 `isSensitiveFile(path string) bool`**
  - 位置：`internal/app/app.go`
  - 拒绝扩展名：`.env`、`.pem`、`.key`、`.pfx`、`.p12`
  - 拒绝路径关键字：`secret`、`token`、`credential`

- [ ] **完善大小限制**
  - 在 Content API handler 中添加 1MB 限制（复用 `countFileLines` 的模式）
  - 在 Diff API handler 中添加 500KB 输出限制

- [ ] **优化错误提示**
  - 路径穿越 → "无法访问此路径"
  - 敏感文件 → "此文件类型不支持预览"
  - 文件过大 → "文件过大，无法预览"
  - 二进制文件 → "此文件为二进制文件，无法预览"
  - 文件不存在 → "文件已不存在"

### 产出文件

| 文件 | 改动类型 | 说明 |
|---|---|---|
| `internal/app/app.go` | 修改 | ~50 行 |

### 验证方式

```bash
# 尝试预览敏感文件
curl "http://localhost:<port>/api/worktree/file/content?id=main&path=.env"
# 预期返回 403

# 尝试路径穿越
curl "http://localhost:<port>/api/worktree/file/content?id=main&path=../../etc/passwd"
# 预期返回 403
```

---

## Phase 5：测试（预估 2-3h）

### 任务清单

- [ ] **Diff API 单元测试**
  - 文件：`internal/app/app_test.go`
  - 场景：正常 diff、staged diff、空 diff、文件不存在、路径穿越、超时

- [ ] **Content API 单元测试**
  - 文件：`internal/app/app_test.go`
  - 场景：正常读取、二进制文件、超大文件、敏感文件、路径穿越、文件不存在

- [ ] **安全函数单元测试**
  - `isPathWithin`：正常路径、`../` 穿越、符号链接、绝对路径
  - `isSensitiveFile`：敏感扩展名、关键字匹配、正常文件

- [ ] **集成测试**
  - 完整流程：点击 → 打开预览 → Diff 渲染 → 切换 Tab → 格式化渲染
  - 异常流程：文件被删除、git 状态变化、网络断开

### 产出文件

| 文件 | 改动类型 | 说明 |
|---|---|---|
| `internal/app/app_test.go` | 修改 | ~150 行 |

### 验证方式

```bash
go test ./internal/app/ -v -run "TestHandleWorktreeFile"
```

---

## 实施顺序与依赖

```
Phase 1 (后端 API)
    │
    ├── Phase 2 依赖 Phase 1（preview.html 需要调用 API）
    │       │
    │       └── Phase 3 依赖 Phase 2（点击事件打开预览窗口）
    │
    └── Phase 4 依赖 Phase 1（在已有 handler 上加安全层）
            │
            └── Phase 5 依赖 Phase 1-4（全功能测试）
```

- Phase 1 和 Phase 4 可以部分并行（安全函数可以先写好，Phase 1 直接调用）
- Phase 2 和 Phase 3 可以部分并行（`openFilePreview` 函数和 `preview.html` 可以同时开发）
- Phase 5 必须在所有 Phase 完成后进行

## 总文件改动清单

| 文件 | Phase | 改动量 |
|---|---|---|
| `internal/app/app.go` | 1, 4 | +180 行 |
| `internal/ui/ui.go` | 2 | +10 行 |
| `internal/ui/static/preview.html` | 2 | +300 行（新文件） |
| `internal/ui/static/vendor/highlight/` | 2 | 新目录 |
| `internal/ui/static/vendor/marked/` | 2 | 新目录 |
| `internal/ui/static/index.html` | 2, 3 | +34 行 |
| `internal/app/app_test.go` | 5 | +150 行 |
| **总计** | | **~680 行** |
