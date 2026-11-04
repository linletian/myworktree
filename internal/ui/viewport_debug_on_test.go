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
