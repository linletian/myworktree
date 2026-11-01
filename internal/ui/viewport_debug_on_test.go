//go:build viewportdebug

package ui

import (
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
}
