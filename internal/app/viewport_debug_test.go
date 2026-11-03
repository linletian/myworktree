//go:build viewportdebug

package app

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"myworktree/internal/config"
)

func validViewportDebugReport(seq int) viewportDebugReport {
	rootScrollHeight := float64(1180)
	return viewportDebugReport{
		Session: "0123456789abcdef",
		Seq:     seq,
		Snapshot: viewportDebugSnapshot{
			CapturedAt:   "2026-10-27T12:00:00.123456789Z",
			Reason:       "resize",
			InnerWidth:   820,
			InnerHeight:  1180,
			ClientWidth:  820,
			ClientHeight: 1180,
			DPR:          2,
			Device: viewportDebugDevice{
				UserAgent: "Mozilla/5.0 (iPad; CPU OS 18_0 like Mac OS X) Safari/605.1.15",
				Platform:  "iPad", TouchPoints: 5, Orientation: "portrait-primary",
				ScreenWidth: 820, ScreenHeight: 1180, Coarse: true,
			},
			// issue #116 contract v2.2 / probe p6: htmlOverflow / bodyOverflow /
			// rootScrollHeight are the structural fields the client started
			// sending then, and v3.0 keeps them for the same reason — the
			// structure is the claim. They are spelled out HERE — not left to the
			// zero value — because the whole point of this payload is to be the
			// live client's byte-for-byte shape: a field the client adds and the
			// server does not declare is a 400 on EVERY upload (the pageTop
			// incident lost a whole capture round), and a payload that only
			// carries the fields it happens to remember cannot catch that.
			Document: viewportDebugDocument{BodyBackground: "rgba(0, 0, 0, 0)", HTMLBackground: "rgba(0, 0, 0, 0)", HTMLOverflow: "clip", BodyOverflow: "clip", RootScrollHeight: &rootScrollHeight},
			// v3.0 shape, read the v3.2 way: a keyboard-session frame UP (vv.height
			// 900 of a 1180 layout) and the shell did not move — app.bottom answers
			// to clientHeight, so layoutGap is 0 and inlineHeight empty — while the
			// applier's occlusion mirror (this is a MID-session frame: a tall keyboard
			// entered earlier, the session still live) reads 280, paid by every host.
			VV: &viewportDebugVV{Width: 820, Height: 900, OffsetTop: 0, OffsetLeft: 0, Scale: 1},
			App: viewportDebugApp{
				viewportDebugRect: viewportDebugRect{Top: 0, Bottom: 1180, Width: 820, Height: 1180},
				ComputedHeight:    "1180px",
				ComputedPosition:  "fixed",
				InlineHeight:      "",
				LayoutGap:         0,
				PaddingTop:        "0px",
				PaddingRight:      "0px",
				PaddingBottom:     "16px",
				PaddingLeft:       "0px",
				Background:        "rgba(0, 0, 0, 0)",
			},
			Main:      viewportDebugRect{Top: 0, Bottom: 1180, Width: 620, Height: 1180},
			Workspace: viewportDebugStyledRect{viewportDebugRect: viewportDebugRect{Top: 40, Bottom: 1164, Width: 620, Height: 1124}, Background: "rgb(11, 16, 32)"},
			Terminal:  &viewportDebugStyledRect{viewportDebugRect: viewportDebugRect{Top: 40, Bottom: 884, Width: 620, Height: 844}, Background: "rgb(11, 16, 32)"},
			// Two hosts: the visible one and a hidden one carrying the SAME bottom,
			// which is the no-residue guarantee the p7 probe exists to show.
			Occlusion: 280,
			PtyHosts: []viewportDebugPtyHost{
				{Index: 0, Top: 40, Bottom: 884, Height: 844, InlineBottom: "280px", ComputedBottom: "280px", Visible: true},
				{Index: 1, Top: 40, Bottom: 884, Height: 844, InlineBottom: "280px", ComputedBottom: "280px", Visible: false},
			},
			// The two overlay children of #workspace, declared at the numbers the
			// applier owes them: the column at its authored 20px PLUS the occlusion
			// (480px here), the strip at the occlusion alone. They are declared
			// rather than left nil so the schema twin of every field the client
			// sends is exercised by every round trip — the pageTop incident is the
			// reason a nil default is not an excuse to skip a field.
			Controls:  &viewportDebugOverlay{viewportDebugRect: viewportDebugRect{Top: 60, Bottom: 684, Width: 44, Height: 624}, InlineBottom: "480px", ComputedBottom: "480px", Visible: true},
			StatusBar: &viewportDebugOverlay{viewportDebugRect: viewportDebugRect{Top: 867, Bottom: 884, Width: 120, Height: 17}, InlineBottom: "280px", ComputedBottom: "280px", Visible: true},
			// max-height is the RESOLVED number (a percentage against the column's
			// height), and it equals that column's height: proof the column
			// shortened and the panel still scrolls inside it.
			QuickKeys:   &viewportDebugQuickKeys{viewportDebugRect: viewportDebugRect{Top: 60, Bottom: 684, Width: 200, Height: 624}, MaxHeight: "624px", ClientHeight: 624, ScrollHeight: 720, Open: true},
			WebPanel:    &viewportDebugStyledRect{viewportDebugRect: viewportDebugRect{Top: 40, Bottom: 1164, Width: 620, Height: 1124}, Background: "rgb(11, 16, 32)"},
			XtermScreen: &viewportDebugRect{Top: 40, Bottom: 883, Width: 620, Height: 843},
			XtermRows:   &viewportDebugRect{Top: 40, Bottom: 874, Width: 620, Height: 834},
			UI: viewportDebugUI{
				Collapsed: false, QuickKeysOpen: true, Kind: "pty", Status: "running", ActiveElementTag: "TEXTAREA",
			},
		},
	}
}

func doViewportDebugRequest(t *testing.T, srv *Server, method, path string, body any) *httptest.ResponseRecorder {
	t.Helper()
	var reader io.Reader
	if body != nil {
		raw, err := json.Marshal(body)
		if err != nil {
			t.Fatalf("marshal request: %v", err)
		}
		reader = bytes.NewReader(raw)
	}
	req := httptest.NewRequest(method, path, reader)
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	rec := httptest.NewRecorder()
	srv.handleViewportDebugReport(rec, req)
	return rec
}

func TestViewportDebugRoundTripAndIncrementalDownload(t *testing.T) {
	srv := &Server{viewportDebug: newViewportDebugStore()}
	first := validViewportDebugReport(1)
	if rec := doViewportDebugRequest(t, srv, http.MethodPost, "/api/viewport-debug/report", first); rec.Code != http.StatusOK {
		t.Fatalf("first upload: status=%d body=%s", rec.Code, rec.Body.String())
	}
	// A retry of the same sequence is an idempotent no-op, never a duplicate.
	if rec := doViewportDebugRequest(t, srv, http.MethodPost, "/api/viewport-debug/report", first); rec.Code != http.StatusOK {
		t.Fatalf("retry upload: status=%d", rec.Code)
	} else {
		var got map[string]any
		_ = json.Unmarshal(rec.Body.Bytes(), &got)
		if got["accepted"] != false || got["latestSeq"] != float64(1) {
			t.Fatalf("retry was not idempotent: %v", got)
		}
	}
	second := validViewportDebugReport(2)
	second.Snapshot.Reason = "magic-hint"
	if rec := doViewportDebugRequest(t, srv, http.MethodPost, "/api/viewport-debug/report", second); rec.Code != http.StatusOK {
		t.Fatalf("second upload: status=%d", rec.Code)
	}

	rec := doViewportDebugRequest(t, srv, http.MethodGet, "/api/viewport-debug/report?session=0123456789abcdef&after=1", nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("download: status=%d body=%s", rec.Code, rec.Body.String())
	}
	if got := rec.Header().Get("Cache-Control"); got != "no-store" {
		t.Fatalf("Cache-Control=%q, want no-store", got)
	}
	var got struct {
		Session   string `json:"session"`
		LatestSeq int    `json:"latestSeq"`
		Events    []struct {
			Seq      int `json:"seq"`
			Snapshot struct {
				Reason string `json:"reason"`
			} `json:"snapshot"`
		} `json:"events"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode download: %v", err)
	}
	if got.Session != first.Session || got.LatestSeq != 2 || len(got.Events) != 1 || got.Events[0].Seq != 2 || got.Events[0].Snapshot.Reason != "magic-hint" {
		t.Fatalf("unexpected incremental report: %+v", got)
	}
}

func TestViewportDebugRejectsInvalidReports(t *testing.T) {
	srv := &Server{viewportDebug: newViewportDebugStore()}
	tests := []struct {
		name   string
		mutate func(*viewportDebugReport)
	}{
		{"bad session", func(r *viewportDebugReport) { r.Session = "short" }},
		{"bad sequence", func(r *viewportDebugReport) { r.Seq = 0 }},
		{"bad reason", func(r *viewportDebugReport) { r.Snapshot.Reason = "terminalText" }},
		{"bad capturedAt", func(r *viewportDebugReport) { r.Snapshot.CapturedAt = "now" }},
		{"bad kind", func(r *viewportDebugReport) { r.Snapshot.UI.Kind = "PTY" }},
		{"bad active tag", func(r *viewportDebugReport) { r.Snapshot.UI.ActiveElementTag = "<script>" }},
		{"bad rect", func(r *viewportDebugReport) { r.Snapshot.App.Width = -1 }},
		{"bad scale", func(r *viewportDebugReport) { r.Snapshot.VV.Scale = 0 }},
		// issue #116 contract v3.0: occlusion is the height the shell owes its
		// PTY hosts, so a negative one is not a measurement, it is a bug — and
		// the host list is what the verdict reads, so a gap in its index or an
		// over-long list (a client looping over hosts it should not have) is
		// rejected at the door rather than averaged into a report.
		{"negative occlusion", func(r *viewportDebugReport) { r.Snapshot.Occlusion = -1 }},
		{"unreasonable occlusion", func(r *viewportDebugReport) { r.Snapshot.Occlusion = 1e12 }},
		{"pty host index gap", func(r *viewportDebugReport) { r.Snapshot.PtyHosts[1].Index = 7 }},
		{"negative pty host height", func(r *viewportDebugReport) { r.Snapshot.PtyHosts[0].Height = -5 }},
		{"too many pty hosts", func(r *viewportDebugReport) {
			hosts := make([]viewportDebugPtyHost, 0, viewportDebugMaxPtyHosts+1)
			for i := 0; i <= viewportDebugMaxPtyHosts; i++ {
				hosts = append(hosts, viewportDebugPtyHost{Index: i})
			}
			r.Snapshot.PtyHosts = hosts
		}},
		// The two overlay children and the quick-keys panel moved into the schema
		// with the same bounds as everything else the verdict reads: a NaN or a
		// 1e12 in the controls column is a broken capture, not a wide column, and
		// a negative height would let the geometry checks in verify-sop.mjs
		// "pass" on nonsense.
		{"negative controls width", func(r *viewportDebugReport) { r.Snapshot.Controls.Width = -1 }},
		{"unreasonable status bar bottom", func(r *viewportDebugReport) { r.Snapshot.StatusBar.Bottom = 1e12 }},
		{"negative quick-keys client height", func(r *viewportDebugReport) { r.Snapshot.QuickKeys.ClientHeight = -1 }},
		{"negative quick-keys scroll height", func(r *viewportDebugReport) { r.Snapshot.QuickKeys.ScrollHeight = -1 }},
		{"over-long computed bottom", func(r *viewportDebugReport) {
			r.Snapshot.Controls.ComputedBottom = string(bytes.Repeat([]byte("x"), 161))
		}},
		{"over-long computed max-height", func(r *viewportDebugReport) { r.Snapshot.QuickKeys.MaxHeight = string(bytes.Repeat([]byte("x"), 161)) }},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			report := validViewportDebugReport(1)
			tt.mutate(&report)
			if rec := doViewportDebugRequest(t, srv, http.MethodPost, "/api/viewport-debug/report", report); rec.Code != http.StatusBadRequest {
				t.Fatalf("status=%d body=%s, want 400", rec.Code, rec.Body.String())
			}
		})
	}

	t.Run("unknown top-level field", func(t *testing.T) {
		var payload map[string]any
		raw, _ := json.Marshal(validViewportDebugReport(1))
		_ = json.Unmarshal(raw, &payload)
		payload["token"] = "must-not-be-accepted"
		if rec := doViewportDebugRequest(t, srv, http.MethodPost, "/api/viewport-debug/report", payload); rec.Code != http.StatusBadRequest {
			t.Fatalf("status=%d, want 400", rec.Code)
		}
	})
	t.Run("unknown nested content field", func(t *testing.T) {
		var payload map[string]any
		raw, _ := json.Marshal(validViewportDebugReport(1))
		_ = json.Unmarshal(raw, &payload)
		payload["snapshot"].(map[string]any)["terminalText"] = "secret"
		if rec := doViewportDebugRequest(t, srv, http.MethodPost, "/api/viewport-debug/report", payload); rec.Code != http.StatusBadRequest {
			t.Fatalf("status=%d, want 400", rec.Code)
		}
	})
	t.Run("unknown field inside a PTY host", func(t *testing.T) {
		var payload map[string]any
		raw, _ := json.Marshal(validViewportDebugReport(1))
		_ = json.Unmarshal(raw, &payload)
		hosts := payload["snapshot"].(map[string]any)["ptyHosts"].([]any)
		hosts[0].(map[string]any)["terminalText"] = "secret"
		if rec := doViewportDebugRequest(t, srv, http.MethodPost, "/api/viewport-debug/report", payload); rec.Code != http.StatusBadRequest {
			t.Fatalf("status=%d, want 400", rec.Code)
		}
	})
	// Same door for the three boxes v3.0 added: an unknown key inside
	// controls / statusBar / quickKeys is rejected, so a client that renames a
	// field cannot quietly start shipping content the server never declared.
	for _, key := range []string{"controls", "statusBar", "quickKeys"} {
		t.Run("unknown field inside "+key, func(t *testing.T) {
			var payload map[string]any
			raw, _ := json.Marshal(validViewportDebugReport(1))
			_ = json.Unmarshal(raw, &payload)
			payload["snapshot"].(map[string]any)[key].(map[string]any)["terminalText"] = "secret"
			if rec := doViewportDebugRequest(t, srv, http.MethodPost, "/api/viewport-debug/report", payload); rec.Code != http.StatusBadRequest {
				t.Fatalf("status=%d, want 400", rec.Code)
			}
		})
	}
	t.Run("wrong content type", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodPost, "/api/viewport-debug/report", bytes.NewBufferString(`{}`))
		req.Header.Set("Content-Type", "text/plain")
		rec := httptest.NewRecorder()
		srv.handleViewportDebugReport(rec, req)
		if rec.Code != http.StatusUnsupportedMediaType {
			t.Fatalf("status=%d, want 415", rec.Code)
		}
	})
	t.Run("trailing object", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodPost, "/api/viewport-debug/report", bytes.NewBufferString(`{} {}`))
		req.Header.Set("Content-Type", "application/json")
		rec := httptest.NewRecorder()
		srv.handleViewportDebugReport(rec, req)
		if rec.Code != http.StatusBadRequest {
			t.Fatalf("status=%d, want 400", rec.Code)
		}
	})
	t.Run("body limit", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodPost, "/api/viewport-debug/report", bytes.NewReader(bytes.Repeat([]byte(" "), viewportDebugBodyLimit+1)))
		req.Header.Set("Content-Type", "application/json")
		rec := httptest.NewRecorder()
		srv.handleViewportDebugReport(rec, req)
		if rec.Code != http.StatusRequestEntityTooLarge {
			t.Fatalf("status=%d, want 413", rec.Code)
		}
	})
	t.Run("method", func(t *testing.T) {
		rec := doViewportDebugRequest(t, srv, http.MethodDelete, "/api/viewport-debug/report", nil)
		if rec.Code != http.StatusMethodNotAllowed || rec.Header().Get("Allow") != "GET, POST" {
			t.Fatalf("status=%d Allow=%q", rec.Code, rec.Header().Get("Allow"))
		}
	})
}

func TestViewportDebugSessionTTLCapacityAndEventCap(t *testing.T) {
	store := newViewportDebugStore()
	now := time.Date(2026, 10, 27, 12, 0, 0, 0, time.UTC)
	store.now = func() time.Time { return now }
	for i := 0; i < viewportDebugMaxSessions; i++ {
		report := validViewportDebugReport(1)
		report.Session = fmt.Sprintf("%016d", i)
		store.put(report)
		now = now.Add(time.Minute)
	}
	// Ninth session evicts the oldest, not the newest active one.
	report := validViewportDebugReport(1)
	report.Session = "fedcba9876543210"
	store.put(report)
	if _, ok := store.get("0000000000000000"); ok {
		t.Fatal("oldest session was not evicted at capacity")
	}
	if _, ok := store.get(report.Session); !ok {
		t.Fatal("newest session was incorrectly evicted")
	}

	// Event history is capped and keeps the newest sequence numbers.
	for seq := 2; seq <= 125; seq++ {
		report := validViewportDebugReport(seq)
		report.Session = "fedcba9876543210"
		store.put(report)
	}
	got, ok := store.get(report.Session)
	if !ok || len(got.Events) != viewportDebugMaxEventsPerSes {
		t.Fatalf("event cap: ok=%v len=%d", ok, len(got.Events))
	}
	if got.Events[0].Seq != 6 || got.Events[len(got.Events)-1].Seq != 125 {
		t.Fatalf("wrong retained range: first=%d last=%d", got.Events[0].Seq, got.Events[len(got.Events)-1].Seq)
	}

	now = now.Add(viewportDebugSessionTTL + time.Second)
	if _, ok := store.get(report.Session); ok {
		t.Fatal("expired session is still downloadable")
	}
}

func TestViewportDebugEndpointUsesExistingAuth(t *testing.T) {
	tmpDir := t.TempDir()
	testConfigPath := filepath.Join(tmpDir, "myworktree", "auth.json")
	reset := config.SetPathForTest(func() (string, error) { return testConfigPath, nil })
	defer reset()
	if err := os.MkdirAll(filepath.Dir(testConfigPath), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(testConfigPath, []byte(`{"auth_token":"test-token"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	srv := &Server{
		cfg:       Config{AuthToken: "test-token"},
		logger:    log.New(io.Discard, "", 0),
		mux:       http.NewServeMux(),
		authFails: map[string]authFail{},
	}
	srv.registerAPIs(srv.mux)
	handler := srv.withAuth(srv.mux)
	body, _ := json.Marshal(validViewportDebugReport(1))

	call := func(token string) *httptest.ResponseRecorder {
		req := httptest.NewRequest(http.MethodPost, "/api/viewport-debug/report", bytes.NewReader(body))
		req.RemoteAddr = "192.168.1.5:12345"
		req.Host = "192.168.1.5:8080"
		req.Header.Set("Origin", "http://192.168.1.5:8080")
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Accept", "application/json")
		if token != "" {
			req.Header.Set("Authorization", "Bearer "+token)
		}
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, req)
		return rec
	}
	if rec := call(""); rec.Code != http.StatusUnauthorized {
		t.Fatalf("unauthenticated remote upload: status=%d, want 401", rec.Code)
	}
	if rec := call("test-token"); rec.Code != http.StatusOK {
		t.Fatalf("authenticated remote upload: status=%d body=%s", rec.Code, rec.Body.String())
	} else if rec.Header().Get("Cache-Control") != "no-store" {
		t.Fatalf("authenticated response lacks no-store")
	}
}

// TestViewportDebugAcceptsEveryClientField pins the CLIENT/SERVER schema together.
//
// The decoder uses DisallowUnknownFields, so any field the probe starts sending
// and the struct does not declare turns EVERY upload into a 400 — which is
// exactly what happened during issue #116: probeVersion, probe and the
// document scroll/pan discriminators were added to the client only, so the panel
// sat on "upload failed; retrying" while the server rejected the lot. A
// permissive unmarshal swallow would have hidden it; this test fails loudly
// instead. Keep this payload in step with snapshotFromPage in
// internal/ui/debugassets/viewport_debug.js.
//
// Mirroring means EVERY field the live probe emits, including the ones the
// server declares `omitempty`: webPanel, xtermScreen and the p7 overlay boxes
// (`controls`, `statusBar`, `quickKeys`) are omitempty, so if they were left out of this payload a client
// that starts sending them (or stops) would sail through this guard unnoticed,
// which is precisely the drift that turned every upload into a 400 during issue
// #116. `ptyHosts` is NOT omitempty — the probe sends it in every frame and an
// empty list is a measurement, see TestViewportDebugEmptyPtyHostListIsNeverOmitted.
// The fixture is the p9 / contract v3.2 shape: a FULL-HEIGHT shell (app.bottom at
// clientHeight, layoutGap 0, inlineHeight empty) with a THIN floating strip up
// (vv.height 1100, a shortfall of 80) and NO keyboard session open — the shallow
// shortfall never reached the 50% entry line, so the applier's occlusion mirror is
// 0, every host keeps its authored bottom ("0px") and the strip simply overlaps a
// terminal that never moved. #status-bar is `position:fixed`, so its rest rect sits
// at the physical viewport bottom (1180), not the inset workspace bottom (1160).
// This is the shape that must round-trip through the server without a 400; the
// geometry is illustrative of the closed-session overlap, the acceptance is the point.
func TestViewportDebugAcceptsEveryClientField(t *testing.T) {
	srv := &Server{viewportDebug: newViewportDebugStore()}
	payload := `{
	  "session": "0123456789abcdef",
	  "seq": 1,
	  "snapshot": {
	    "probeVersion": "p9-full-shell-session-avoidance-v1",
	    "capturedAt": "2026-10-27T12:00:00.000Z",
	    "reason": "manual",
	    "innerWidth": 820, "innerHeight": 1180,
	    "clientWidth": 820, "clientHeight": 1180, "dpr": 2,
	    "device": {"userAgent":"iPad","platform":"iPad","touchPoints":5,"orientation":"portrait-primary","screenWidth":820,"screenHeight":1180,"coarse":true},
	    "document": {
	      "bodyBackground": "rgb(249, 249, 249)",
	      "htmlBackground": "rgb(249, 249, 249)",
	      "htmlOverflow": "clip",
	      "bodyOverflow": "clip",
	      "rootScrollHeight": 1180,
	      "pageTop": 69,
	      "scrollTop": 0,
	      "scrollingElementScrollTop": 0,
	      "scrollingElementScrollHeight": 1180,
	      "bodyScrollTop": 0
	    },
	    "probe": {
	      "visualBottom": {"y":1109,"tag":"DIV","id":"workspace","className":"","background":"rgb(11, 16, 32)"},
	      "layoutBottom": {"y":1178,"tag":"DIV","id":"workspace","className":"","background":"rgb(11, 16, 32)"},
	      "pageTop": 69
	    },
	    "vv": {"width":820,"height":1100,"offsetTop":0,"offsetLeft":0,"scale":1},
	    "app": {"top":0,"bottom":1180,"width":820,"height":1180,"computedHeight":"1180px","computedPosition":"fixed","inlineHeight":"","layoutGap":0,"paddingTop":"0px","paddingRight":"0px","paddingBottom":"20px","paddingLeft":"0px","background":"rgba(0, 0, 0, 0)"},
	    "main": {"top":40,"bottom":1180,"width":820,"height":1140},
	    "workspace": {"top":40,"bottom":1160,"width":820,"height":1120,"background":"rgb(11, 16, 32)"},
	    "terminal": {"top":40,"bottom":1160,"width":820,"height":1120,"background":"rgba(0, 0, 0, 0)"},
	    "occlusion": 0,
	    "ptyHosts": [
	      {"index":0,"top":40,"bottom":1160,"height":1120,"inlineBottom":"0px","computedBottom":"0px","visible":true},
	      {"index":1,"top":40,"bottom":1160,"height":1120,"inlineBottom":"0px","computedBottom":"0px","visible":false}
	    ],
	    "controls": {"top":60,"bottom":1140,"width":44,"height":1080,"inlineBottom":"20px","computedBottom":"20px","visible":true},
	    "statusBar": {"top":1163,"bottom":1180,"width":120,"height":17,"inlineBottom":"0px","computedBottom":"0px","visible":true},
	    "quickKeys": {"top":60,"bottom":1140,"width":200,"height":1080,"maxHeight":"1080px","clientHeight":1080,"scrollHeight":1176,"open":true},
	    "webPanel": {"top":40,"bottom":1160,"width":820,"height":1120,"background":"rgb(11, 16, 32)"},
	    "xtermScreen": {"top":50,"bottom":1151,"width":820,"height":1101},
	    "xtermRows": {"top":50,"bottom":1151,"width":820,"height":1101},
	    "ui": {"collapsed":false,"quickKeysOpen":false,"kind":"pty","status":"running","activeElementTag":"TEXTAREA"}
	  }
	}`
	rec := doViewportDebugRequest(t, srv, http.MethodPost, "/api/viewport-debug/report", mustDecodeJSON(t, payload))
	if rec.Code != http.StatusOK {
		t.Fatalf("the probe's own snapshot shape must be accepted, got status=%d body=%s — a client field missing from viewportDebugSnapshot turns every upload into a 400 and the panel just says \"upload failed; retrying\"", rec.Code, rec.Body.String())
	}
}

// TestViewportDebugNullScrollFieldsAreAccepted covers the other half: the client
// sends null for the scroll discriminators when the platform API is missing, so
// "absent" must not be an error (pointers keep it distinguishable from zero).
func TestViewportDebugNullScrollFieldsAreAccepted(t *testing.T) {
	srv := &Server{viewportDebug: newViewportDebugStore()}
	payload := `{
	  "session": "0123456789abcdef",
	  "seq": 1,
	  "snapshot": {
	    "capturedAt": "2026-10-27T12:00:00.000Z",
	    "reason": "manual",
	    "innerWidth": 820, "innerHeight": 1180,
	    "clientWidth": 820, "clientHeight": 1180, "dpr": 2,
	    "device": {"userAgent":"iPad","platform":"iPad","touchPoints":5,"orientation":"portrait-primary","screenWidth":820,"screenHeight":1180,"coarse":true},
	    "document": {"bodyBackground":"","htmlBackground":"","pageTop":null,"scrollTop":null,"scrollingElementScrollTop":null,"scrollingElementScrollHeight":null,"bodyScrollTop":null},
	    "probe": null,
	    "vv": null,
	    "app": {"top":0,"bottom":1180,"width":820,"height":1180,"computedHeight":"","inlineHeight":"","layoutGap":0,"paddingTop":"0px","paddingRight":"0px","paddingBottom":"0px","paddingLeft":"0px","background":""},
	    "occlusion": 0,
	    "main": {"top":40,"bottom":1164,"width":820,"height":1124},
	    "controls": null,
	    "statusBar": null,
	    "quickKeys": null,
	    "workspace": {"top":40,"bottom":1148,"width":820,"height":1108,"background":""},
	    "ui": {"collapsed":false,"quickKeysOpen":false,"kind":"pty","status":"running","activeElementTag":"BODY"}
	  }
	}`
	if rec := doViewportDebugRequest(t, srv, http.MethodPost, "/api/viewport-debug/report", mustDecodeJSON(t, payload)); rec.Code != http.StatusOK {
		t.Fatalf("null scroll/probe fields must decode, got status=%d body=%s", rec.Code, rec.Body.String())
	}
}

// TestViewportDebugEmptyPtyHostListIsNeverOmitted is the regression guard for the
// INIT FRAME — a session's very first capture, taken before #terminal-container
// holds a PTY host — and for every web-kind page, which never holds one at all.
//
// The client has always SENT the field on those frames: ptyHostsOf answers `[]`
// when there is no host (internal/ui/debugassets/viewport_debug.js), and that `[]`
// IS the claim — "looked, zero hosts". `ptyHosts` was declared `omitempty`, so Go
// dropped the field from the DOWNLOADED response of exactly those frames, and the
// SOP reader (evidence/verify-sop.mjs: `!Array.isArray(s.ptyHosts)` → missing →
// INCONCLUSIVE) filed a legitimate hostless frame as "an older instrument that
// cannot speak about the local avoidance". Evidence of the actual page was thrown
// away as evidence of an outdated build.
//
// The assertion is therefore on the RAW BYTES of a real POST → GET round trip, not
// on the struct: unmarshal → store → encode must produce `"ptyHosts":[]`. That is
// what makes this test the guard rather than a restatement of the tag — a nil
// slice would encode `null` here, and putting `omitempty` back would encode
// nothing, and both failures are invisible to a test that only marshals Go values.
func TestViewportDebugEmptyPtyHostListIsNeverOmitted(t *testing.T) {
	srv := &Server{viewportDebug: newViewportDebugStore()}
	// An init frame spelled the way the p9 probe spells it: keyboard down (no
	// session: occlusion mirror 0, vv == layout), no host yet (`terminal`/`xterm*`
	// null), and the overlays still reported. #terminal-controls is a child of
	// #workspace (authored bottom 20px, rest bottom 1140), but #status-bar is
	// `position:fixed` since v3.1, so
	// at rest its bottom is the PHYSICAL viewport bottom (1180), not the inset
	// workspace bottom (1160) — the safe-area it no longer inherits.
	initFrame := `{
	  "session": "0123456789abcdef",
	  "seq": 1,
	  "snapshot": {
	    "probeVersion": "p9-full-shell-session-avoidance-v1",
	    "capturedAt": "2026-10-27T12:00:00.000Z",
	    "reason": "init",
	    "innerWidth": 820, "innerHeight": 1180,
	    "clientWidth": 820, "clientHeight": 1180, "dpr": 2,
	    "device": {"userAgent":"iPad","platform":"iPad","touchPoints":5,"orientation":"portrait-primary","screenWidth":820,"screenHeight":1180,"coarse":true},
	    "document": {"bodyBackground":"rgb(249, 249, 249)","htmlBackground":"rgb(249, 249, 249)","htmlOverflow":"clip","bodyOverflow":"clip","rootScrollHeight":1180,"pageTop":0,"scrollTop":0,"scrollingElementScrollTop":0,"scrollingElementScrollHeight":1180,"bodyScrollTop":0},
	    "probe": {"visualBottom":{"y":1179,"tag":"DIV","id":"workspace","className":"","background":"rgb(11, 16, 32)"},"layoutBottom":{"y":1179,"tag":"DIV","id":"workspace","className":"","background":"rgb(11, 16, 32)"},"pageTop":0},
	    "vv": {"width":820,"height":1180,"offsetTop":0,"offsetLeft":0,"scale":1},
	    "app": {"top":0,"bottom":1180,"width":820,"height":1180,"computedHeight":"1180px","computedPosition":"fixed","inlineHeight":"","layoutGap":0,"paddingTop":"0px","paddingRight":"0px","paddingBottom":"20px","paddingLeft":"0px","background":"rgba(0, 0, 0, 0)"},
	    "main": {"top":40,"bottom":1180,"width":820,"height":1140},
	    "workspace": {"top":40,"bottom":1160,"width":820,"height":1120,"background":"rgb(11, 16, 32)"},
	    "terminal": null,
	    "occlusion": 0,
	    "ptyHosts": [],
	    "controls": {"top":60,"bottom":1140,"width":44,"height":1080,"inlineBottom":"","computedBottom":"20px","visible":true},
	    "statusBar": {"top":1163,"bottom":1180,"width":120,"height":17,"inlineBottom":"","computedBottom":"0px","visible":true},
	    "quickKeys": {"top":60,"bottom":1140,"width":200,"height":1080,"maxHeight":"1080px","clientHeight":1080,"scrollHeight":1117,"open":false},
	    "xtermScreen": null,
	    "xtermRows": null,
	    "ui": {"collapsed":false,"quickKeysOpen":false,"kind":"pty","status":"","activeElementTag":"BODY"}
	  }
	}`
	if !strings.Contains(initFrame, `"ptyHosts"`) {
		t.Fatal(`the fixture must send ptyHosts at all — this test is about a PRESENT, EMPTY list`)
	}
	frame := mustDecodeJSON(t, initFrame)
	// The bytes that actually go over the wire must carry the field PRESENT and
	// EMPTY; a fixture that simply forgot it would otherwise pass by accident.
	rawRequest, err := json.Marshal(frame)
	if err != nil {
		t.Fatalf("marshal the init frame: %v", err)
	}
	if !bytes.Contains(rawRequest, []byte(`"ptyHosts":[]`)) {
		t.Fatalf("the POST body must carry an empty host list, got %s", rawRequest)
	}
	if rec := doViewportDebugRequest(t, srv, http.MethodPost, "/api/viewport-debug/report", frame); rec.Code != http.StatusOK {
		t.Fatalf("an init frame with an empty host list must be accepted, got status=%d body=%s", rec.Code, rec.Body.String())
	}

	rec := doViewportDebugRequest(t, srv, http.MethodGet, "/api/viewport-debug/report?session=0123456789abcdef&after=0", nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("download: status=%d body=%s", rec.Code, rec.Body.String())
	}
	body := rec.Body.String()
	if !strings.Contains(body, `"ptyHosts":[]`) {
		t.Fatalf("an observed EMPTY host list must come back as \"ptyHosts\":[], not be omitted or nulled; got %s", body)
	}
	if strings.Contains(body, `"ptyHosts":null`) {
		t.Fatalf("a non-nil empty list must not be encoded as null (Go prints nil slices as null): %s", body)
	}

	// Same decode path the verifier walks, one level deeper: the key EXISTS and is
	// an empty ARRAY, so `Array.isArray` is true and the frame is judged instead of
	// being reported as missing evidence.
	var got struct {
		Events []struct {
			Seq      int            `json:"seq"`
			Snapshot map[string]any `json:"snapshot"`
		} `json:"events"`
	}
	if err := json.Unmarshal([]byte(body), &got); err != nil {
		t.Fatalf("decode download: %v", err)
	}
	if len(got.Events) != 1 || got.Events[0].Seq != 1 {
		t.Fatalf("want exactly the init frame back, got %+v", got.Events)
	}
	rawHosts, present := got.Events[0].Snapshot["ptyHosts"]
	if !present {
		t.Fatalf("ptyHosts was omitted from the response — the SOP reads an absent field as an older instrument")
	}
	hosts, isArray := rawHosts.([]any)
	if !isArray {
		t.Fatalf("ptyHosts must be a JSON array, got %T", rawHosts)
	}
	if len(hosts) != 0 {
		t.Fatalf("the init frame must come back with zero hosts, got %d", len(hosts))
	}

	// The other half of the boundary, kept INTENTIONALLY un-normalized: a frame
	// that never carried the field at all (an older instrument, or a hand-made
	// payload) must still read as ABSENT — `null` — and not be laundered into
	// "observed, zero hosts". Absent and empty are different claims, and the
	// verifier is allowed to tell them apart; only the encoding of an empty LIST
	// was wrong.
	older := mustDecodeJSON(t, initFrame).(map[string]any)
	older["seq"] = float64(2)
	delete(older["snapshot"].(map[string]any), "ptyHosts")
	if rec := doViewportDebugRequest(t, srv, http.MethodPost, "/api/viewport-debug/report", older); rec.Code != http.StatusOK {
		t.Fatalf("a frame without ptyHosts must still be accepted, got status=%d body=%s", rec.Code, rec.Body.String())
	}
	rec = doViewportDebugRequest(t, srv, http.MethodGet, "/api/viewport-debug/report?session=0123456789abcdef&after=1", nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("download of the frame without the field: status=%d", rec.Code)
	}
	body = rec.Body.String()
	if !strings.Contains(body, `"ptyHosts":null`) {
		t.Fatalf("a frame that never sent ptyHosts must stay absent (null), not be normalized to []: %s", body)
	}
	if strings.Contains(body, `"ptyHosts":[]`) {
		t.Fatalf("absence was normalized into an observed empty list, which would let an older instrument certify the local avoidance: %s", body)
	}

	// And the populated path still round-trips: dropping `omitempty` must not
	// flatten a real host list into anything else.
	withHosts := validViewportDebugReport(3)
	if rec := doViewportDebugRequest(t, srv, http.MethodPost, "/api/viewport-debug/report", withHosts); rec.Code != http.StatusOK {
		t.Fatalf("upload with hosts: status=%d body=%s", rec.Code, rec.Body.String())
	}
	rec = doViewportDebugRequest(t, srv, http.MethodGet, "/api/viewport-debug/report?session=0123456789abcdef&after=2", nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("download with hosts: status=%d", rec.Code)
	}
	if body = rec.Body.String(); !strings.Contains(body, `"ptyHosts":[{`) {
		t.Fatalf("a populated host list must round-trip as a list of objects: %s", body)
	}
	if err := json.Unmarshal([]byte(body), &got); err != nil {
		t.Fatalf("decode download with hosts: %v", err)
	}
	if len(got.Events) != 1 || len(got.Events[0].Snapshot["ptyHosts"].([]any)) != 2 {
		t.Fatalf("want 2 hosts back, got %+v", got.Events)
	}
}

// mustDecodeJSON turns a raw JSON literal into a value the request helper can
// re-marshal (it takes `any` and marshals it), keeping the fixture readable as
// literal JSON rather than a deeply nested Go composite.
func mustDecodeJSON(t *testing.T, raw string) any {
	t.Helper()
	var v any
	if err := json.Unmarshal([]byte(raw), &v); err != nil {
		t.Fatalf("fixture is not valid JSON: %v", err)
	}
	return v
}
