# Issue #116 冻结实现验证记录（2026-11-04）

## 范围与判据

- 验证对象：工作区中的冻结实现，产品基线 `5268869`，当前提交 `45b6d44`。
- 执行旧 PLAN 的自动化门禁与证据脚本；凡旧候选文案与最终行为不同，以 `docs/IPADOS-UI-COMPATIBILITY.md` 描述的冻结契约为准，不用旧阈值反判最终实现。
- 未修改 `internal/ui/static/index.html`、`internal/ui/static/kinds/pty.js`、诊断资产或诊断服务端语义；已核对上述文件与 `5268869` 无 diff。
- Headless Chrome 只证明桌面 Chromium 结构/几何，不冒充 iPadOS Safari 真机结果。

## 已通过的自动化门禁

| 验证 | 结果 |
| --- | --- |
| `go test ./internal/ui/ ./internal/app/` | PASS |
| `go test -tags viewportdebug ./internal/ui/ ./internal/app/` | PASS |
| `go generate ./internal/portal` 前后比较 `csp_gen.go` | PASS，生成物可复现，无 diff |
| `gofmt -l .` | PASS，无输出 |
| `go test ./internal/portal/` | PASS |
| `go test ./...` | PASS，全部包通过 |
| `node --test 'internal/ui/testdata/*.test.mjs' 'internal/portal/testdata/*.test.mjs'` | PASS，365/365 |
| `verify-sop.mjs --self-test` | PASS，54 个 fixture 均按预期判定 |
| `structural-check.mjs --self-test` | PASS，71 个 fixture/guard 均按预期判定 |
| `pty-refit-check.mjs --self-test` | PASS，18 个判定均按预期 |
| `sidebar-title-check.mjs --self-test` | PASS，12 个判定均按预期 |
| `verify-live-choiceA.mjs --html internal/ui/static/index.html` | PASS，冻结仓库源码离线回放通过 |
| `MW_LIVE_URL=http://127.0.0.1:38235/ verify-live-choiceA.mjs` | PASS，现有 issue116 服务实际提供的源码回放通过 |

## 本轮未完成：真实浏览器运行检查（INCONCLUSIVE，不是产品 FAIL）

目标是复用现有 `127.0.0.1:38235` issue116 服务，用 `structural-check.mjs` 检查真实页面结构，再顺序执行侧栏几何与 PTY refit 检查。

实际结果：

1. 第一次 `structural-check.mjs` 运行超过 120 秒，无任何 verdict 输出；对同一页面的最小只读 `Runtime.evaluate` 也在 5 秒后超时。
2. 带 `Page.javascriptDialogOpening` / `Runtime.exceptionThrown` 监听和逐步超时的启动诊断中，`Page.navigate` 在 5 秒后超时；未观察到 JS dialog 或 exception。
3. 移除 HTTP/HTTPS/SOCKS 代理环境变量后，独立重试仍超过 120 秒且无 verdict。
4. 两次后台任务均已终止，相关 Chrome 进程和 9337/9347/9351 验证端口已释放；没有停止或替换现有 `:38235` 服务。
5. 因实时页面启动/CDP 未完成，本轮未继续运行真实侧栏几何和 PTY refit 浏览器检查，也没有生成 `RESULTS-frozen-final-*.json`。

结论：现有证据不能判定这是产品行为失败，也不能判定实时浏览器检查通过。需要先把“页面启动/CDP 无响应”作为独立验证环境或工具问题归因，不能与冻结前端修复混成一轮修改。

## iPad 核心键盘与 quick-keys 复验

### A 会话的 scale=1 连续段

A 会话早段 153 帧全部为 `scale=0.75`，只作为 zoom 豁免记录，不能认证键盘行为。后续实际操作仍写回 A 会话，取得了连续、完整的 `seq 367–486` 共 120 帧 `scale=1` 数据，派生文件为 `raw/final116-20261104-a-scale1.jsonl`；原始文件未改。

关键结果：

- 软键盘 `vv.height=320` 时 `occlusion=428`，宿主/控制列/状态栏分别正确避让；收起后宿主和 xterm 高度恢复。
- 再次聚焦、再次打开软键盘可重新进入避让；恢复中的 147px 浅中间态不重新抬起终端。
- `magic-hint` 的 68.5px 浅遮挡保持 `occlusion=0`。
- 唯一未观测项是 quick-keys 面板从未打开，因此该段单独判读为 INCONCLUSIVE，而不是产品 FAIL。

### C 会话的 quick-keys 补齐段

`seq 88–99` 是连续、无 pan、quick-keys 打开的完整软键盘开/关过程，派生文件为 `raw/final116-20261104-c-quickkeys.jsonl`。`verify-sop.mjs` 对该段 verdict 为 **PASS**：包括面板随缩短的控制列收高度、末行/控制列/状态栏位置和恢复。它只证明几何与命中，不替代用户截图/目测。

## C 全过程暴露的新反馈：键盘下的 Safari pan

C 会话还包含多轮额外滚动/平移与窗口变化，不能用上述局部 PASS 覆盖整个文件。

日志事实：

- `vv.offsetTop` 多次从 0 增长到 403；`app.top` 同步从 0 变为 -403，`document.scrollTop` 也跟随到 403。
- 这是持续状态，不只是动画帧：共有 120 个稳定帧带 pan，`seq 136 / 266 / 310 / 549` 四次手动 Capture 均记录到 `vv.offsetTop=403`、`app.top=-403`。
- 在这些帧中 PTY 宿主底边为 -103，终端确实可以整体离开可见区，与用户“外壳被抬升”的目测一致。
- 全部 583 帧仍满足 `rootScrollHeight == clientHeight`，`#app` inline height 始终为空，结构仍是 clip + fixed。因此这不是旧版“可滚动死区”或自动回顶代码复活。
- 键盘收起后 offsetTop、外壳和终端能够回到 0/满高；横竖屏切换的几何变化也有完整记录。
- `seq 573 → 574` 还有一帧“VV 已变、applier 尚未更新”的周期/resize 顺序差，下一帧即正确，不能与持续 pan 混为同一问题。
- 拉取过程有 8 个序列缺口，不能将完整 C 文件作为无缺口验收包；但上述反复出现且含手动 Capture 的 pan 事实不依赖缺口补齐。

## 诊断面板自身问题（工具层，非终端避让算法）

快照没有上报 `#viewport-debug-panel` 自身位置或 rect，所以面板四角实际位置以用户目测为主。源码与现象一致：

- `panelLayoutFor()` 只使用 `vv.height`，没有 `vv.offsetTop` 输入。
- 左上/右上使用固定 `top`；外壳发生 -403px 平移时，顶部面板也会离开可见区。
- 左下/右下使用固定 `bottom`；未 pan 时它仍相对布局底部，可能被软键盘覆盖，发生足够 pan 后又会进入可视区。
- 现有 Node 面板测试只覆盖默认顶部居位的高度和 free drag，没有覆盖底部居位，也没有覆盖非零 `vv.offsetTop`；既有 headless 四角检查同样不模拟 Safari 键盘 pan。

这解释了两件同时成立的事：诊断按钮在某些角落不可达，以及产品外壳/终端也被系统 pan 带走。不能把前者简单当成后者，也不能通过给终端 `occlusion` 再加 `offsetTop` 来修面板。

**处理边界：**这是一个新的、已真机复现的冻结外场景/需求冲突。不得直接恢复自动回顶、重钉整个 `#app`、修改终端 raw 算式或顺手改诊断面板。下一步需要先决定是仅修诊断工具，还是同时改变产品对 Safari 键盘 pan 的验收口径；任何产品改动都须另行讨论并做真机验证。

## 旧 PLAN 仍需真机完成的项目

由用户在可复现问题的 iPad 上操作，保留截图与必要的几何记录：

- 同一设备修前/修后口径对照：静止、软键盘弹出/收起、妙控键盘浮动栏、横竖屏、URL 栏显隐、缩放/pan、safe-area、前后台恢复；键盘已打开时刷新。
- Split View / Stage Manager 的适用场景；冻结实现已有窗口几何变化处理，但不能以源码或桌面模拟代替真机结果。
- PTY 与 opencode/reasonix/dsh iframe、quick-keys、IME、终端触摸滚动、fit/resize 回归。
- 暗色/浅色底部像素截图；诊断面板必须移离底边，不能以 `elementFromPoint` 代替视觉验收。
- 旧 PLAN 另列的 iPhone Safari 至少一轮；条件允许再补不同 iPadOS 版本与 Chrome(iOS)。

真机若出现需要改变冻结 CSS、键盘状态机、PTY resize 或诊断语义才能解决的问题，先停止修订并讨论，不在验证过程中直接改代码。

## 当前工作区说明

- 保留了 `sidebar_collapse.test.mjs` 对冻结 `queueTTYResize` 行为的测试适配。
- CHANGELOG 两处 `viewport_height.test.mjs` 总数由 27 修正为实际的 34，以满足既有计数门禁。
- 旧 p9 暂存实验已恢复；无活动终端、全阶段未测量的 `RESULTS-smoke-no-backend.json` 已删除。
- 保留了仍有验证价值的 PTY/侧栏检查脚本、p8 真机原始日志和侧栏改前/改后证据。
- 本文件只是本轮验证记录，不宣称 issue #116 已完成旧 PLAN 的全部真机关闭条件。
