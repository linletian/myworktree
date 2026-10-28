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
	"testing"
	"time"

	"myworktree/internal/config"
)

func validViewportDebugReport(seq int) viewportDebugReport {
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
			Document: viewportDebugDocument{BodyBackground: "rgba(0, 0, 0, 0)", HTMLBackground: "rgba(0, 0, 0, 0)"},
			VV:       &viewportDebugVV{Width: 820, Height: 1164, OffsetTop: 0, OffsetLeft: 0, Scale: 1},
			App: viewportDebugApp{
				viewportDebugRect: viewportDebugRect{Top: 0, Bottom: 1164, Width: 820, Height: 1164},
				ComputedHeight:    "1164px",
				InlineHeight:      "1164px",
				PaddingTop:        "0px",
				PaddingRight:      "0px",
				PaddingBottom:     "16px",
				PaddingLeft:       "0px",
				Background:        "rgba(0, 0, 0, 0)",
			},
			Main:        viewportDebugRect{Top: 0, Bottom: 1164, Width: 620, Height: 1164},
			Workspace:   viewportDebugStyledRect{viewportDebugRect: viewportDebugRect{Top: 40, Bottom: 1148, Width: 620, Height: 1108}, Background: "rgb(11, 16, 32)"},
			Terminal:    &viewportDebugStyledRect{viewportDebugRect: viewportDebugRect{Top: 40, Bottom: 1148, Width: 620, Height: 1108}, Background: "rgb(11, 16, 32)"},
			WebPanel:    &viewportDebugStyledRect{viewportDebugRect: viewportDebugRect{Top: 40, Bottom: 1148, Width: 620, Height: 1108}, Background: "rgb(11, 16, 32)"},
			XtermScreen: &viewportDebugRect{Top: 40, Bottom: 1147, Width: 620, Height: 1107},
			XtermRows:   &viewportDebugRect{Top: 40, Bottom: 1138, Width: 620, Height: 1098},
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
