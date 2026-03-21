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

func TestHandleInstanceReorder(t *testing.T) {
	nullLogger := log.New(os.Stderr, "", 0)
	srv, err := New(Config{}, nullLogger)
	if err != nil {
		t.Fatalf("New() failed: %v", err)
	}

	// GET instances to find a worktree with 2+ instances.
	reqList := httptest.NewRequest(http.MethodGet, "/api/instances", nil)
	wList := httptest.NewRecorder()
	srv.handleInstances(wList, reqList)
	var listResp map[string]any
	if err := json.Unmarshal(wList.Body.Bytes(), &listResp); err != nil {
		t.Fatalf("invalid JSON: %v", err)
	}
	instancesRaw := listResp["instances"]
	if instancesRaw == nil {
		t.Skip("no instances available for reorder test")
	}
	instances, ok := instancesRaw.([]any)
	if !ok || len(instances) < 2 {
		t.Skip("need at least 2 instances for reorder test")
	}

	// Group by worktree_id.
	type pair struct{ id, wt string }
	var pairs []pair
	for _, raw := range instances {
		m := raw.(map[string]any)
		pairs = append(pairs, pair{id: m["id"].(string), wt: m["worktree_id"].(string)})
	}
	wtCounts := map[string]int{}
	wtIDs := map[string][]string{}
	for _, p := range pairs {
		wtCounts[p.wt]++
		wtIDs[p.wt] = append(wtIDs[p.wt], p.id)
	}
	var wtID string
	var ids []string
	for w, c := range wtCounts {
		if c >= 2 {
			wtID = w
			ids = wtIDs[w]
			break
		}
	}
	if wtID == "" {
		t.Skip("no worktree with 2+ instances")
	}

	// Test 1: valid reorder — reverse the order.
	reversed := make([]string, len(ids))
	copy(reversed, ids)
	for i, j := 0, len(reversed)-1; i < j; i, j = i+1, j-1 {
		reversed[i], reversed[j] = reversed[j], reversed[i]
	}
	body := map[string]any{"worktree_id": wtID, "order": reversed}
	bodyBytes, _ := json.Marshal(body)
	req := httptest.NewRequest(http.MethodPatch, "/api/instances/reorder", bytes.NewReader(bodyBytes))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	srv.handleInstanceReorder(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("PATCH /api/instances/reorder: expected 200, got %d: %s", w.Code, w.Body.String())
	}

	// Verify order persisted: GET instances and check slice order.
	reqGet := httptest.NewRequest(http.MethodGet, "/api/instances", nil)
	wGet := httptest.NewRecorder()
	srv.handleInstances(wGet, reqGet)
	var getResp map[string]any
	json.Unmarshal(wGet.Body.Bytes(), &getResp)
	allInsts := getResp["instances"].([]any)
	var gotOrder []string
	for _, raw := range allInsts {
		m := raw.(map[string]any)
		if m["worktree_id"].(string) == wtID {
			gotOrder = append(gotOrder, m["id"].(string))
		}
	}
	if len(gotOrder) != len(reversed) {
		t.Fatalf("reorder: got %d instances, want %d", len(gotOrder), len(reversed))
	}
	for i := range reversed {
		if gotOrder[i] != reversed[i] {
			t.Fatalf("reorder: position %d: got %s, want %s", i, gotOrder[i], reversed[i])
		}
	}

	// Test 2: missing instance → 400.
	badBody := map[string]any{"worktree_id": wtID, "order": []string{ids[0]}}
	badBytes, _ := json.Marshal(badBody)
	reqBad := httptest.NewRequest(http.MethodPatch, "/api/instances/reorder", bytes.NewReader(badBytes))
	reqBad.Header.Set("Content-Type", "application/json")
	wBad := httptest.NewRecorder()
	srv.handleInstanceReorder(wBad, reqBad)
	if wBad.Code != http.StatusBadRequest {
		t.Fatalf("PATCH with missing instance: expected 400, got %d", wBad.Code)
	}

	// Test 3: instance from wrong worktree → 400.
	wrongBody := map[string]any{"worktree_id": wtID, "order": []string{ids[0], ids[1], "nonexistent"}}
	wrongBytes, _ := json.Marshal(wrongBody)
	reqWrong := httptest.NewRequest(http.MethodPatch, "/api/instances/reorder", bytes.NewReader(wrongBytes))
	reqWrong.Header.Set("Content-Type", "application/json")
	wWrong := httptest.NewRecorder()
	srv.handleInstanceReorder(wWrong, reqWrong)
	if wWrong.Code != http.StatusBadRequest {
		t.Fatalf("PATCH with invalid ID: expected 400, got %d", wWrong.Code)
	}

	// Test 4: wrong method → 405.
	reqGet2 := httptest.NewRequest(http.MethodGet, "/api/instances/reorder", nil)
	w405 := httptest.NewRecorder()
	srv.handleInstanceReorder(w405, reqGet2)
	if w405.Code != http.StatusMethodNotAllowed {
		t.Fatalf("GET /api/instances/reorder: expected 405, got %d", w405.Code)
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
