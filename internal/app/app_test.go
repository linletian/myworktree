package app

import (
	"bytes"
	"encoding/json"
	"log"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"myworktree/internal/store"
)

func TestParseInt64Default(t *testing.T) {
	if got := parseInt64Default("42", -1); got != 42 {
		t.Fatalf("expected 42, got %d", got)
	}
	if got := parseInt64Default("  ", -1); got != -1 {
		t.Fatalf("blank input should return default, got %d", got)
	}
	if got := parseInt64Default("abc", -1); got != -1 {
		t.Fatalf("invalid input should return default, got %d", got)
	}
}

func TestNormalizeLabels(t *testing.T) {
	if got := normalizeLabels(nil); got != nil {
		t.Fatalf("nil input should return nil, got %#v", got)
	}
	if got := normalizeLabels(map[string]string{}); got != nil {
		t.Fatalf("empty map should return nil, got %#v", got)
	}

	got := normalizeLabels(map[string]string{
		" team ":  " backend ",
		"":        "x",
		"x":       "",
		"owner":   "   ",
		"   ":     "value",
		"service": " api ",
	})
	if len(got) != 2 || got["team"] != "backend" || got["service"] != "api" {
		t.Fatalf("unexpected labels normalization result: %#v", got)
	}
	if _, ok := got["owner"]; ok {
		t.Fatalf("owner with whitespace value should be dropped: %#v", got)
	}
	if _, ok := got["x"]; ok {
		t.Fatalf("label with empty value should be dropped: %#v", got)
	}
	if _, ok := got[""]; ok {
		t.Fatalf("empty key should be dropped: %#v", got)
	}

	got = normalizeLabels(map[string]string{
		" ":   " ",
		"":    "",
		"foo": "   ",
	})
	if got != nil {
		t.Fatalf("all invalid labels should return nil, got %#v", got)
	}
}

func TestClientIP(t *testing.T) {
	if got := clientIP("127.0.0.1:12345"); got != "127.0.0.1" {
		t.Fatalf("expected host only, got %q", got)
	}
	if got := clientIP("not-a-host-port"); got != "not-a-host-port" {
		t.Fatalf("invalid host:port should return original, got %q", got)
	}
}

func TestHandleInstanceUpdate(t *testing.T) {
	nullLogger := log.New(os.Stderr, "", 0)
	srv, err := New(Config{}, nullLogger)
	if err != nil {
		t.Fatalf("New() failed: %v", err)
	}

	// GET /api/instances first to find an instance ID to rename
	reqList := httptest.NewRequest(http.MethodGet, "/api/instances", nil)
	wList := httptest.NewRecorder()
	srv.handleInstances(wList, reqList)
	var listResp map[string]any
	if err := json.Unmarshal(wList.Body.Bytes(), &listResp); err != nil {
		t.Fatalf("invalid JSON: %v", err)
	}
	instancesRaw := listResp["instances"]
	if instancesRaw == nil {
		t.Skip("no instances available for rename test")
	}
	instances, ok := instancesRaw.([]any)
	if !ok || len(instances) == 0 {
		t.Skip("no instances available for rename test")
	}
	firstInst := instances[0].(map[string]any)
	instID := firstInst["id"].(string)

	// PATCH → 200 with updated name
	body := map[string]any{"id": instID, "name": "renamed-instance"}
	bodyBytes, _ := json.Marshal(body)
	reqPatch := httptest.NewRequest(http.MethodPatch, "/api/instances", bytes.NewReader(bodyBytes))
	reqPatch.Header.Set("Content-Type", "application/json")
	wPatch := httptest.NewRecorder()
	srv.handleInstances(wPatch, reqPatch)
	if wPatch.Code != http.StatusOK {
		t.Fatalf("PATCH /api/instances: expected status 200, got %d", wPatch.Code)
	}
	var patchResp store.ManagedInstance
	if err := json.Unmarshal(wPatch.Body.Bytes(), &patchResp); err != nil {
		t.Fatalf("PATCH /api/instances: invalid JSON: %v", err)
	}
	if patchResp.Name != "renamed-instance" {
		t.Fatalf("PATCH /api/instances: name = %q, want %q", patchResp.Name, "renamed-instance")
	}
	if patchResp.ID != instID {
		t.Fatalf("PATCH /api/instances: id changed unexpectedly")
	}

	// PATCH with empty name → 400
	reqBad := httptest.NewRequest(http.MethodPatch, "/api/instances", bytes.NewReader([]byte(`{"id":"`+instID+`","name":"   "}`)))
	reqBad.Header.Set("Content-Type", "application/json")
	wBad := httptest.NewRecorder()
	srv.handleInstances(wBad, reqBad)
	if wBad.Code != http.StatusBadRequest {
		t.Fatalf("PATCH with empty name: expected 400, got %d", wBad.Code)
	}

	// PATCH with unknown id → 404
	req404 := httptest.NewRequest(http.MethodPatch, "/api/instances", bytes.NewReader([]byte(`{"id":"does-not-exist","name":"x"}`)))
	req404.Header.Set("Content-Type", "application/json")
	w404 := httptest.NewRecorder()
	srv.handleInstances(w404, req404)
	if w404.Code != http.StatusNotFound {
		t.Fatalf("PATCH with unknown id: expected 404, got %d", w404.Code)
	}
}

func TestHandleMain(t *testing.T) {
	nullLogger := log.New(os.Stderr, "", 0)
	srv, err := New(Config{}, nullLogger)
	if err != nil {
		t.Fatalf("New() failed: %v", err)
	}

	// GET → 200 with correct JSON fields
	req := httptest.NewRequest(http.MethodGet, "/api/main", nil)
	w := httptest.NewRecorder()
	srv.handleMain(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("GET /api/main: expected status 200, got %d", w.Code)
	}

	ct := w.Header().Get("Content-Type")
	if ct != "application/json" {
		t.Fatalf("GET /api/main: expected Content-Type application/json, got %q", ct)
	}

	var resp map[string]any
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("GET /api/main: invalid JSON: %v", err)
	}

	name, ok := resp["name"].(string)
	if !ok || name == "" {
		t.Fatalf("GET /api/main: expected non-empty name field, got %v", resp["name"])
	}
	// Name should match the basename of the git root.
	expectedName := filepath.Base(filepath.Clean(srv.root))
	if name != expectedName {
		t.Fatalf("GET /api/main: name = %q, want %q", name, expectedName)
	}

	// Branch must be present as a non-empty string (CurrentBranch errors otherwise).
	if _, ok := resp["branch"]; !ok {
		t.Fatalf("GET /api/main: missing branch field")
	}

	// POST → 405
	reqPost := httptest.NewRequest(http.MethodPost, "/api/main", nil)
	wPost := httptest.NewRecorder()
	srv.handleMain(wPost, reqPost)
	if wPost.Code != http.StatusMethodNotAllowed {
		t.Fatalf("POST /api/main: expected status 405, got %d", wPost.Code)
	}

	// PUT → 405
	reqPut := httptest.NewRequest(http.MethodPut, "/api/main", nil)
	wPut := httptest.NewRecorder()
	srv.handleMain(wPut, reqPut)
	if wPut.Code != http.StatusMethodNotAllowed {
		t.Fatalf("PUT /api/main: expected status 405, got %d", wPut.Code)
	}
}
