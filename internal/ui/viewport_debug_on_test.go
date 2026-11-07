//go:build viewportdebug

package ui

import (
	"regexp"
	"strings"
	"testing"
)

// TestViewportDebugAssetAnchors pins the opt-in diagnostics asset and its
// geometry-only upload path. It deliberately does not execute CSS or Safari;
// behavior lives in testdata/viewport_debug.test.mjs and real-device A0.
func TestViewportDebugAssetAnchors(t *testing.T) {
	bodyText := fetchIndexHTML(t)
	debugTag := `<script src="/static/viewport_debug.js"></script>`
	frameworkTag := `<script src="/static/framework.js"></script>`
	debugAt := strings.Index(bodyText, debugTag)
	frameworkAt := strings.Index(bodyText, frameworkTag)
	if debugAt < 0 || frameworkAt < 0 || debugAt > frameworkAt {
		t.Fatal("GET / must load /static/viewport_debug.js before framework.js and the inline application")
	}
	asset := fetchStaticAsset(t, "/static/viewport_debug.js")
	for _, want := range []string{
		"snapshotFromPage", "createClient", "installBrowser", "startContinuous", "stopContinuous",
		`params.get("viewport-debug")`, `params.get("debug-session")`,
		`"/api/viewport-debug/report"`, `"magic-hint"`, `"keyboard-hint"`, `"periodic"`,
		"previous.latestSeq", "initialSeq",
		`addEventListener("pointerdown"`, `event.preventDefault()`,
		// A0 实测缺陷回归：主按钮必须先于次要控件入 DOM，面板高度按可视区夹紧，
		// 否则 iPad 软键盘弹出时 keyboard-hint / magic-hint 无法按下。
		"panelLayoutFor", "applyPanelLayout", "rowPrimary", "rowSecondary",
		"PANEL_COLLAPSE_THRESHOLD", `visualViewport.addEventListener("resize", relayout)`,
		`"viewport-debug-pos"`,
		// issue #116 Phase 2 任务 1 / 任务 2 的锚点：归属、同帧聚焦 rect、避让
		// 会话标记、§5.5 解锁事件留痕与面板自身落位。缺任何一项，PHASE1-CONTRACT
		// 的「验收前置条件」不成立，相应条目只能记 INCONCLUSIVE。
		"schemaRev", "focusOwnershipOf", "keyboardObservationOf", "keyboardSessionOf",
		"createEventTrace", "advisoryOnly", "panelReportOf", "dataset.debugCorner",
		`"visible-window-too-short-for-panel"`, `"HO-1"`, `"HO-2"`, `"HO-3"`, `"HO-4"`,
		`"geometryGrace"`, `"recovering"`,
		// 本轮评审修复的锚点：任何一项被删掉，对应的缺陷就重新变成"只能记
		// INCONCLUSIVE"——不是代码风格问题，是证据链断点。
		// B-3：会话块自报为派生提示，权威仍在冻结 judge。
		"derived: true", "derivedState", `"unlocked-after-previous-frame-latch-state-unpublished"`,
		// B-2：面板词汇表与服务器白名单一致；dataset 未写时也必须有答案。
		`"unavailable"`, `"panel-layout-not-authored"`,
		// M-1：不支持必须渲染到面板可见状态行。
		`"viewport-debug-placement"`,
		// B-1：capture 标志随注册记录、随摘除回传。
		`{ passive: true, capture: true }`, "item.capture === true",
		// M-6 / N-1：几何只计数，窗口 resize 与 visualViewport resize 分开。
		"geometryCountedOnly: true", "windowResize", "checkVisibility()",
		// m-2：读取失败也出一帧，带显式标记，不在产品页抛未捕获异常。
		`"probe-read-failed"`, "probeReadFailed",
		// N-3：留痕条目自带 layout 侧与横向的同一瞬间几何。
		"vvWidth", "layoutHeight",
	} {
		if !strings.Contains(asset, want) {
			t.Fatalf("viewport_debug.js missing %q", want)
		}
	}
	for _, forbidden := range []string{"document.cookie", "session.term", "buffer.active", "translateToString", "terminalText"} {
		if strings.Contains(asset, forbidden) {
			t.Fatalf("diagnostic asset must not read terminal/auth content: found %q", forbidden)
		}
	}
	// Phase 2 的聚焦归属只读结构与 rect：宿主正文一律不读。注释先剥掉——探针
	// 正是用注释写下"不读什么"，剥注释才会把禁区本身当成违规（沿用 mjs 侧口径）。
	// 面板自己的 status 行写 textContent 是 UI 写入，不在禁读之列。
	code := regexp.MustCompile(`(?s)/\*.*?\*/`).ReplaceAllString(asset, "")
	code = regexp.MustCompile(`//[^\n]*`).ReplaceAllString(code, "")
	// 状态行只写 textContent；任何读回 DOM 文本的写法都不许出现。
	for _, forbidden := range []string{"innerHTML", "innerText", "selectedText"} {
		if strings.Contains(code, forbidden) {
			t.Fatalf("diagnostic asset must not read host content: found %q", forbidden)
		}
	}
	// 聚焦宿主的内容、value、placeholder 一律不读：归属只看 tag / 结构 / rect。
	for _, banned := range []*regexp.Regexp{
		regexp.MustCompile(`activeElement\s*\.\s*(value|textContent|innerText|innerHTML|placeholder)`),
		regexp.MustCompile(`\.contentDocument\s*\.\s*(body|documentElement)\s*\.\s*(textContent|innerText|innerHTML)`),
	} {
		if banned.MatchString(code) {
			t.Fatalf("diagnostic asset must not read host content: matched %s", banned)
		}
	}
	// 归属判定只允许读一个属性名：PTY 宿主标记。出现别的属性名，就是在收集内容。
	for _, match := range regexp.MustCompile(`getAttribute\(([^)]*)\)`).FindAllStringSubmatch(code, -1) {
		if strings.TrimSpace(match[1]) != `"data-instance-id"` {
			t.Fatalf("the ownership walk may only read the host marker attribute, found getAttribute(%s)", match[1])
		}
	}
}

// TestViewportDebugSettleMirrorAnchors pins AUTH-20261105-D2-SETTLE-READONLY：
// applier 在既有 occlusion mirror 边界旁新增只读 `window.__mwTerminalSettle`
// 发布，数值型 `window.__mwTerminalOcclusion` 一行原样、类型与值不动；探针只
// 复制、绝不写回。任何一项缺失，r3-Z-MENU 的 settle 序列就没有可读的证据链，
// 相应条目只能记 INCONCLUSIVE——这是可观测性锚点，不是产品行为锚点。
// 双评审复回收口仲裁 F-3（r3 前必须）：applier 切片的位置/发布锚点保留，但
// "产品任何决策路径不得读取"改为对整份 index.html 用 stripJSComments 剥注释后
// 的全文件计数红线（一次自续读、一次写入，恰好 2 次）。
func TestViewportDebugSettleMirrorAnchors(t *testing.T) {
	bodyText := fetchIndexHTML(t)
	// 数值 mirror 原样，且全文件只此一处发布（settle 是新增旁路，不是替换）。
	if n := strings.Count(bodyText, "window.__mwTerminalOcclusion = occlusion;"); n != 1 {
		t.Fatalf("the numeric occlusion mirror must stay verbatim and single, found %d", n)
	}
	// settle mirror 在同一 pass 尾部发布，恰好一次，且是不可变快照。
	apply := sliceJSFunction(t, bodyText, "applyVisualViewportOcclusion")
	publish := "window.__mwTerminalSettle = Object.freeze(Object.assign({}, settleSnapshotScalars, {"
	if n := strings.Count(apply, publish); n != 1 {
		t.Fatalf("the settle mirror must publish exactly once inside applyVisualViewportOcclusion, found %d", n)
	}
	occlusionAt := strings.Index(apply, "window.__mwTerminalOcclusion = occlusion;")
	settleAt := strings.Index(apply, publish)
	if occlusionAt < 0 || settleAt < 0 || settleAt < occlusionAt {
		t.Fatal("the settle mirror must be published at the existing mirror boundary, beside the numeric occlusion mirror")
	}
	for _, want := range []string{
		// 白名单字段的原样拼写（VALIDATION 2026-11-05 细化表）。
		"scaleSettleCandidate: scaleSettleCandidate,",
		"scaleSettlePending: scaleSettlePending,",
		"scaleBelowOneSettled: scaleBelowOneSettled,",
		"zoomReturnFramePending: !zoomed && scaleSettleCandidate !== 0 && scaleSettleTicked < SCALE_SETTLE_TICKS,",
		`scaleSettlePassSource: selfCheckPass ? "scaleSettleTick" : "other",`,
		"scaleSettlePassSeq: settleSeqPrevious + 1,",
		// 有界 tick journal：固定 8 条、只由既有自检 tick 入环、冻结拷贝。
		"const SCALE_SETTLE_JOURNAL_MAX = 8;",
		".slice(-SCALE_SETTLE_JOURNAL_MAX)",
		// 普通 pass 只续序号、不动 journal——jitter 永远挤不掉 tick 链。
		": Object.freeze(settleJournalPrevious.slice());",
		// 发布失败不得中断产品 pass。
		"} catch (_) { /* the diagnostic is dropped; the product pass is untouched */ }",
	} {
		if !strings.Contains(apply, want) {
			t.Fatalf("index.html settle publication missing %q", want)
		}
	}
	// 仲裁 F-3：这条红线是对整份 index.html 的【全文件】计数，用 ui_test.go 现成
	// 的引号感知 stripJSComments 剥注释——自写的 `[^\n]*` 方式作废，因为它会把
	// 字符串里的 `//`（URL、token）之后的真实代码一并吞掉，计数不可信。剥注释后
	// 可执行代码里完全限定名 `window.__mwTerminalSettle` 必须恰好出现 2 次：发布
	// 块内的一次自发布续读（序号/journal 续接，非判据）与一次写入。任何产品决策
	// 路径新增读取——applier 内或 applier 外——都会让计数越过 2，测试必须红。
	// 探针文件（viewport_debug.js）的只读复制检查在下方单独进行，不计入这 2 次。
	full := stripJSComments(bodyText)
	if n := strings.Count(full, "window.__mwTerminalSettle"); n != 2 {
		t.Fatalf("index.html executable code may only read-and-publish the settle mirror once each, found %d mentions", n)
	}
	// 探针侧：只读、只复制、失败退化为 null + 固定标记；绝不写回窗口 mirror。
	asset := fetchStaticAsset(t, "/static/viewport_debug.js")
	for _, want := range []string{
		"win.__mwTerminalSettle", "scaleSettleFieldsOf", "scaleSettleUnavailableReason",
		`"settle-mirror-not-published"`, "scaleSettleTickJournal", "SETTLE_JOURNAL_MAX",
		// additive schema 标记随字段集移动；冻结 stamp 不动（verify-sop 的 stamp 门依赖它）。
		`const SCHEMA_REV = "p9-additive-touch-truth-v1";`,
		`const PROBE_VERSION = "p9-full-shell-session-avoidance-v1";`,
	} {
		if !strings.Contains(asset, want) {
			t.Fatalf("viewport_debug.js settle block missing %q", want)
		}
	}
	if strings.Contains(asset, "__mwTerminalSettle =") {
		t.Fatal("the probe must never write the settle mirror — it only copies it")
	}
}
