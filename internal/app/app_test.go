package app

import (
	"bytes"
	"encoding/json"
	"log"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

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

func TestClientIP(t *testing.T) {
	if got := clientIP("127.0.0.1:12345"); got != "127.0.0.1" {
		t.Fatalf("expected host only, got %q", got)
	}
	if got := clientIP("not-a-host-port"); got != "not-a-host-port" {
		t.Fatalf("invalid host:port should return original, got %q", got)
	}
}

func TestIsLoopbackHost(t *testing.T) {
	tests := []struct {
		name string
		host string
		want bool
	}{
		{name: "localhost", host: "localhost", want: true},
		{name: "ipv4 loopback", host: "127.0.0.1", want: true},
		{name: "ipv6 loopback", host: "::1", want: true},
		{name: "remote ipv4", host: "203.0.113.10", want: false},
		{name: "blank", host: "", want: false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := isLoopbackHost(tt.host); got != tt.want {
				t.Fatalf("isLoopbackHost(%q) = %v, want %v", tt.host, got, tt.want)
			}
		})
	}
}

func mustReadTTYSize(t *testing.T, ch <-chan ttySize) ttySize {
	t.Helper()
	select {
	case size, ok := <-ch:
		if !ok {
			t.Fatal("tty size channel closed unexpectedly")
		}
		return size
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for tty size update")
		return ttySize{}
	}
}

func TestTTYSizeAggregationUsesMinimumAcrossClients(t *testing.T) {
	srv := &Server{}
	client1, updates1, err := srv.registerTTYClient("inst-1")
	if err != nil {
		t.Fatalf("register client1 failed: %v", err)
	}
	client2, updates2, err := srv.registerTTYClient("inst-1")
	if err != nil {
		t.Fatalf("register client2 failed: %v", err)
	}

	srv.updateTTYClientSize("inst-1", client1, 120, 40)
	if got := mustReadTTYSize(t, updates1); got != (ttySize{Cols: 120, Rows: 40}) {
		t.Fatalf("client1 first applied size = %#v", got)
	}
	if got := mustReadTTYSize(t, updates2); got != (ttySize{Cols: 120, Rows: 40}) {
		t.Fatalf("client2 first applied size = %#v", got)
	}

	srv.updateTTYClientSize("inst-1", client2, 80, 50)
	want := ttySize{Cols: 80, Rows: 40}
	if got := mustReadTTYSize(t, updates1); got != want {
		t.Fatalf("client1 aggregated size = %#v, want %#v", got, want)
	}
	if got := mustReadTTYSize(t, updates2); got != want {
		t.Fatalf("client2 aggregated size = %#v, want %#v", got, want)
	}

	srv.updateTTYClientSize("inst-1", client1, 90, 30)
	want = ttySize{Cols: 80, Rows: 30}
	if got := mustReadTTYSize(t, updates1); got != want {
		t.Fatalf("client1 updated aggregated size = %#v, want %#v", got, want)
	}
	if got := mustReadTTYSize(t, updates2); got != want {
		t.Fatalf("client2 updated aggregated size = %#v, want %#v", got, want)
	}
	if got := srv.ttyApplied["inst-1"]; got != want {
		t.Fatalf("server applied size = %#v, want %#v", got, want)
	}
}

func TestTTYSizeAggregationRecomputesOnDisconnect(t *testing.T) {
	srv := &Server{}
	client1, updates1, err := srv.registerTTYClient("inst-2")
	if err != nil {
		t.Fatalf("register client1 failed: %v", err)
	}
	client2, updates2, err := srv.registerTTYClient("inst-2")
	if err != nil {
		t.Fatalf("register client2 failed: %v", err)
	}

	srv.updateTTYClientSize("inst-2", client1, 80, 24)
	_ = mustReadTTYSize(t, updates1)
	_ = mustReadTTYSize(t, updates2)

	srv.updateTTYClientSize("inst-2", client2, 120, 40)
	select {
	case <-updates1:
		t.Fatal("larger second client should not change aggregated size")
	case <-updates2:
		t.Fatal("larger second client should not change aggregated size")
	case <-time.After(200 * time.Millisecond):
	}

	srv.unregisterTTYClient("inst-2", client1)
	want := ttySize{Cols: 120, Rows: 40}
	if got := mustReadTTYSize(t, updates2); got != want {
		t.Fatalf("remaining client size after disconnect = %#v, want %#v", got, want)
	}

	select {
	case _, ok := <-updates1:
		if ok {
			t.Fatal("removed client channel should be closed")
		}
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for removed client channel to close")
	}
}

func TestTTYClientHandleCloseIsIdempotent(t *testing.T) {
	srv := &Server{}
	handle, updates, err := srv.newTTYClientHandle("inst-3")
	if err != nil {
		t.Fatalf("newTTYClientHandle failed: %v", err)
	}
	handle.Close()
	handle.Close()

	select {
	case _, ok := <-updates:
		if ok {
			t.Fatal("updates channel should be closed after handle close")
		}
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for updates channel to close")
	}

	if clients := srv.ttyClients["inst-3"]; len(clients) != 0 {
		t.Fatalf("tty clients should be cleaned up, got %#v", clients)
	}
}

func TestRegisterTTYClientRejectsExcessClients(t *testing.T) {
	srv := &Server{}
	for i := 0; i < maxTTYClientsPerInstance; i++ {
		if _, _, err := srv.registerTTYClient("inst-4"); err != nil {
			t.Fatalf("registerTTYClient(%d) failed unexpectedly: %v", i, err)
		}
	}
	if _, _, err := srv.registerTTYClient("inst-4"); err == nil {
		t.Fatal("expected tty client limit error")
	}
}

func TestWorktreeOpenEndpointsRejectNonLoopback(t *testing.T) {
	nullLogger := log.New(os.Stderr, "", 0)
	srv, err := New(Config{}, nullLogger)
	if err != nil {
		t.Fatalf("New() failed: %v", err)
	}

	tests := []struct {
		name    string
		handler func(http.ResponseWriter, *http.Request)
		path    string
	}{
		{name: "terminal", handler: srv.handleWorktreeOpenTerminal, path: "/api/worktrees/open-terminal"},
		{name: "finder", handler: srv.handleWorktreeOpenFinder, path: "/api/worktrees/open-finder"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodPost, tt.path, bytes.NewReader([]byte(`{"id":"__main__"}`)))
			req.Header.Set("Content-Type", "application/json")
			req.RemoteAddr = "203.0.113.5:12345"
			w := httptest.NewRecorder()

			tt.handler(w, req)

			if w.Code != http.StatusForbidden {
				t.Fatalf("expected 403, got %d: %s", w.Code, w.Body.String())
			}
		})
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

func TestParseGitDiffNumStat(t *testing.T) {
	out1 := "1\t1\ta.txt\n10\t8\tb.txt"
	changes1, total1 := parseGitDiffNumStat(out1)
	if len(changes1) != 2 {
		t.Fatalf("case1: expected 2 changes, got %d", len(changes1))
	}
	if changes1[0]["path"] != "a.txt" {
		t.Fatalf("case1[0]: path=%q, want 'a.txt'", changes1[0]["path"])
	}
	if changes1[0]["additions"] != 1 || changes1[0]["deletions"] != 1 {
		t.Fatalf("case1[0]: got additions=%v deletions=%v, want 1 and 1", changes1[0]["additions"], changes1[0]["deletions"])
	}
	if changes1[1]["path"] != "b.txt" {
		t.Fatalf("case1[1]: path=%q, want 'b.txt'", changes1[1]["path"])
	}
	if changes1[1]["additions"] != 10 || changes1[1]["deletions"] != 8 {
		t.Fatalf("case1[1]: got additions=%v deletions=%v, want 10 and 8", changes1[1]["additions"], changes1[1]["deletions"])
	}
	if total1["additions"] != 11 || total1["deletions"] != 9 {
		t.Fatalf("case1 total: got additions=%d deletions=%d, want 11 and 9", total1["additions"], total1["deletions"])
	}

	// Case 2: additions only.
	out2 := "20\t0\tnew_file.go"
	changes2, total2 := parseGitDiffNumStat(out2)
	if len(changes2) != 1 {
		t.Fatalf("case2: expected 1 change, got %d", len(changes2))
	}
	if changes2[0]["additions"] != 20 || changes2[0]["deletions"] != 0 {
		t.Fatalf("case2: got additions=%v deletions=%v, want 20 and 0", changes2[0]["additions"], changes2[0]["deletions"])
	}
	if total2["additions"] != 20 || total2["deletions"] != 0 {
		t.Fatalf("case2 total: got additions=%d deletions=%d, want 20 and 0", total2["additions"], total2["deletions"])
	}

	// Case 3: deletions only.
	out3 := "0\t15\tdead_code.go"
	changes3, total3 := parseGitDiffNumStat(out3)
	if len(changes3) != 1 {
		t.Fatalf("case3: expected 1 change, got %d", len(changes3))
	}
	if changes3[0]["additions"] != 0 || changes3[0]["deletions"] != 15 {
		t.Fatalf("case3: got additions=%v deletions=%v, want 0 and 15", changes3[0]["additions"], changes3[0]["deletions"])
	}
	if total3["additions"] != 0 || total3["deletions"] != 15 {
		t.Fatalf("case3 total: got additions=%d deletions=%d, want 0 and 15", total3["additions"], total3["deletions"])
	}

	// Case 4: binary file (no line stats).
	out4 := "-\t-\timage.png"
	changes4, total4 := parseGitDiffNumStat(out4)
	if len(changes4) != 1 {
		t.Fatalf("case4: expected 1 change, got %d", len(changes4))
	}
	if changes4[0]["additions"] != 0 || changes4[0]["deletions"] != 0 {
		t.Fatalf("case4 binary: got additions=%v deletions=%v, want 0 and 0", changes4[0]["additions"], changes4[0]["deletions"])
	}
	if total4["additions"] != 0 || total4["deletions"] != 0 {
		t.Fatalf("case4 total: got additions=%d deletions=%d, want 0 and 0", total4["additions"], total4["deletions"])
	}

	// Case 5: path with spaces.
	out5 := "3\t3\tpath with spaces/foo.go\n5\t0\tinternal/pkg bar/main.go"
	changes5, total5 := parseGitDiffNumStat(out5)
	if len(changes5) != 2 {
		t.Fatalf("case5: expected 2 changes, got %d", len(changes5))
	}
	if changes5[0]["path"] != "path with spaces/foo.go" {
		t.Fatalf("case5[0]: got %q", changes5[0]["path"])
	}
	if changes5[1]["path"] != "internal/pkg bar/main.go" {
		t.Fatalf("case5[1]: got %q", changes5[1]["path"])
	}
	if changes5[0]["additions"] != 3 || changes5[0]["deletions"] != 3 {
		t.Fatalf("case5[0]: got additions=%v deletions=%v, want 3 and 3", changes5[0]["additions"], changes5[0]["deletions"])
	}
	if changes5[1]["additions"] != 5 || changes5[1]["deletions"] != 0 {
		t.Fatalf("case5[1]: got additions=%v deletions=%v, want 5 and 0", changes5[1]["additions"], changes5[1]["deletions"])
	}
	if total5["additions"] != 8 || total5["deletions"] != 3 {
		t.Fatalf("case5 total: got additions=%d deletions=%d, want 8 and 3", total5["additions"], total5["deletions"])
	}

	// Case 6: empty output.
	changes6, total6 := parseGitDiffNumStat("")
	if len(changes6) != 0 {
		t.Fatalf("case6: expected 0 changes for empty output, got %d", len(changes6))
	}
	if total6["additions"] != 0 || total6["deletions"] != 0 {
		t.Fatalf("case6 total: got additions=%d deletions=%d, want 0 and 0", total6["additions"], total6["deletions"])
	}

	// Case 7: blank lines are ignored.
	out7 := "\n\n2\t1\tREADME.md\n"
	changes7, total7 := parseGitDiffNumStat(out7)
	if len(changes7) != 1 {
		t.Fatalf("case7: expected 1 change, got %d", len(changes7))
	}
	if total7["additions"] != 2 || total7["deletions"] != 1 {
		t.Fatalf("case7 total: got additions=%d deletions=%d, want 2 and 1", total7["additions"], total7["deletions"])
	}

	// Case 8: malformed lines are skipped without affecting totals.
	out8 := "not-a-numstat-line\n4\t2\tok.go"
	changes8, total8 := parseGitDiffNumStat(out8)
	if len(changes8) != 1 {
		t.Fatalf("case8: expected 1 valid change, got %d", len(changes8))
	}
	if changes8[0]["path"] != "ok.go" {
		t.Fatalf("case8[0]: got %q", changes8[0]["path"])
	}
	if total8["additions"] != 4 || total8["deletions"] != 2 {
		t.Fatalf("case8 total: got additions=%d deletions=%d, want 4 and 2", total8["additions"], total8["deletions"])
	}

	// Case 9: large values remain exact; unlike --stat bars, numstat does not
	// truncate per-file counts to terminal width.
	out9 := "100\t25\tlarge-change.go"
	changes9, total9 := parseGitDiffNumStat(out9)
	if len(changes9) != 1 {
		t.Fatalf("case9: expected 1 change, got %d", len(changes9))
	}
	if changes9[0]["additions"] != 100 || changes9[0]["deletions"] != 25 {
		t.Fatalf("case9: got additions=%v deletions=%v, want 100 and 25", changes9[0]["additions"], changes9[0]["deletions"])
	}
	if total9["additions"] != 100 || total9["deletions"] != 25 {
		t.Fatalf("case9 total: got additions=%d deletions=%d, want 100 and 25", total9["additions"], total9["deletions"])
	}
}

func TestHandleWorktreeStatusGitFailureBothFail(t *testing.T) {
	srv := &Server{
		root: t.TempDir(),
		gitRunner: func(timeout time.Duration, gitRoot string, args ...string) ([]byte, error) {
			return nil, os.ErrNotExist
		},
	}

	req := httptest.NewRequest(http.MethodGet, "/api/worktree/status?id=__main__", nil)
	w := httptest.NewRecorder()
	srv.handleWorktreeStatus(w, req)

	if w.Code != http.StatusInternalServerError {
		t.Fatalf("expected 500 when both git commands fail, got %d", w.Code)
	}
	if !strings.Contains(w.Body.String(), "git diff failed") {
		t.Fatalf("expected 'git diff failed' in error message, got %q", w.Body.String())
	}
}

func TestHandleWorktreeStatusPartialFailure(t *testing.T) {
	srv := &Server{
		root: t.TempDir(),
		gitRunner: func(timeout time.Duration, gitRoot string, args ...string) ([]byte, error) {
			for _, a := range args {
				if a == "--cached" {
					return []byte("1\t0\ttest.go"), nil
				}
			}
			return nil, os.ErrNotExist
		},
	}
	req := httptest.NewRequest(http.MethodGet, "/api/worktree/status?id=__main__", nil)
	w := httptest.NewRecorder()
	srv.handleWorktreeStatus(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200 for partial failure, got %d: %s", w.Code, w.Body.String())
	}

	var resp map[string]any
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("invalid JSON: %v", err)
	}

	staged, _ := resp["staged"].(map[string]any)
	if staged["changes"] == nil {
		t.Fatal("staged: missing changes")
	}
	if _, hasErr := staged["error"]; hasErr {
		t.Fatal("staged: should not have error field")
	}

	unstaged, _ := resp["unstaged"].(map[string]any)
	errMsg, _ := unstaged["error"].(string)
	if errMsg == "" {
		t.Fatal("unstaged: expected error field")
	}
	if !strings.Contains(errMsg, "git diff failed") {
		t.Fatalf("unstaged: expected 'git diff failed' in error, got %q", errMsg)
	}
	warnMsg, _ := unstaged["warning"].(string)
	if warnMsg == "" {
		t.Fatal("unstaged: expected warning field for ls-files failure")
	}
	if !strings.Contains(warnMsg, "git ls-files failed") {
		t.Fatalf("unstaged: expected 'git ls-files failed' in warning, got %q", warnMsg)
	}
}

func TestHandleWorktreeStatusWithUntracked(t *testing.T) {
	tmpDir := t.TempDir()
	os.WriteFile(filepath.Join(tmpDir, "newfile.go"), []byte("line1\nline2\nline3\n"), 0644)
	os.MkdirAll(filepath.Join(tmpDir, "subdir"), 0755)
	os.WriteFile(filepath.Join(tmpDir, "subdir", "nested.go"), []byte("package p\n"), 0644)

	srv := &Server{
		root: tmpDir,
		gitRunner: func(timeout time.Duration, gitRoot string, args ...string) ([]byte, error) {
			isLsFiles := false
			isCached := false
			for _, a := range args {
				if a == "ls-files" {
					isLsFiles = true
				}
				if a == "--cached" {
					isCached = true
				}
			}
			if isLsFiles {
				return []byte("newfile.go\nsubdir/nested.go\n"), nil
			}
			if isCached {
				return []byte("3\t0\tstaged.go\n"), nil
			}
			return []byte("1\t2\tmodified.go\n"), nil
		},
	}

	req := httptest.NewRequest(http.MethodGet, "/api/worktree/status?id=__main__", nil)
	w := httptest.NewRecorder()
	srv.handleWorktreeStatus(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}

	var resp map[string]any
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("invalid JSON: %v (body=%s)", err, w.Body.String())
	}

	staged, _ := resp["staged"].(map[string]any)
	stagedChanges := staged["changes"].([]interface{})
	if len(stagedChanges) != 1 {
		t.Fatalf("staged: expected 1 change, got %d", len(stagedChanges))
	}

	unstaged, _ := resp["unstaged"].(map[string]any)
	unstagedChanges := unstaged["changes"].([]interface{})
	if len(unstagedChanges) != 3 {
		t.Fatalf("unstaged: expected 3 changes (modified + 2 untracked), got %d", len(unstagedChanges))
	}

	var untrackedAdds float64
	foundUntracked := 0
	for _, c := range unstagedChanges {
		change := c.(map[string]interface{})
		status, _ := change["status"].(string)
		if status == "untracked" {
			foundUntracked++
			untrackedAdds += change["additions"].(float64)
		}
	}
	if foundUntracked != 2 {
		t.Fatalf("expected 2 untracked files, got %d", foundUntracked)
	}

	unstagedTotal := unstaged["total"].(map[string]interface{})
	expAdds := 1.0 + untrackedAdds
	if unstagedTotal["additions"].(float64) != expAdds {
		t.Fatalf("unstaged total additions: expected %v, got %v", expAdds, unstagedTotal["additions"])
	}
	if unstagedTotal["deletions"].(float64) != 2.0 {
		t.Fatalf("unstaged total deletions: expected 2, got %v", unstagedTotal["deletions"])
	}
}

func TestHandleWorktreeStatusLsFilesFailsDiffSucceeds(t *testing.T) {
	srv := &Server{
		root: t.TempDir(),
		gitRunner: func(timeout time.Duration, gitRoot string, args ...string) ([]byte, error) {
			for _, a := range args {
				if a == "ls-files" {
					return nil, os.ErrNotExist
				}
				if a == "--cached" {
					return []byte("1\t0\tstaged.go\n"), nil
				}
			}
			return []byte("2\t3\tmodified.go\n"), nil
		},
	}

	req := httptest.NewRequest(http.MethodGet, "/api/worktree/status?id=__main__", nil)
	w := httptest.NewRecorder()
	srv.handleWorktreeStatus(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}

	var resp map[string]any
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("invalid JSON: %v", err)
	}

	unstaged, _ := resp["unstaged"].(map[string]any)

	if _, hasErr := unstaged["error"]; hasErr {
		t.Fatal("unstaged: should not have error field when diff succeeds")
	}

	warnMsg, _ := unstaged["warning"].(string)
	if warnMsg == "" {
		t.Fatal("unstaged: expected warning field for ls-files failure")
	}
	if !strings.Contains(warnMsg, "git ls-files failed") {
		t.Fatalf("unstaged: expected 'git ls-files failed' in warning, got %q", warnMsg)
	}

	unstagedChanges := unstaged["changes"].([]interface{})
	if len(unstagedChanges) != 1 {
		t.Fatalf("unstaged: expected 1 diff change, got %d", len(unstagedChanges))
	}

	staged, _ := resp["staged"].(map[string]any)
	if _, hasWarn := staged["warning"]; hasWarn {
		t.Fatal("staged: should not have warning field")
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

	// Branch field is present (may be empty string on detached HEAD).
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

func TestWithAuth_LoopbackBypass(t *testing.T) {
	nullLogger := log.New(os.Stderr, "", 0)
	srv, err := New(Config{AuthToken: "test-token"}, nullLogger)
	if err != nil {
		t.Fatalf("New() failed: %v", err)
	}

	handler := srv.withAuth(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))

	tests := []struct {
		name      string
		remoteAddr string
		origin    string
		token     string
		wantStatus int
	}{
		{
			name:       "loopback no token passes",
			remoteAddr: "127.0.0.1:12345",
			origin:     "",
			wantStatus: http.StatusOK,
		},
		{
			name:       "loopback with Authorization header passes",
			remoteAddr: "127.0.0.1:12345",
			origin:     "",
			token:      "wrong-token",
			wantStatus: http.StatusOK,
		},
		{
			name:       "loopback ipv6 passes",
			remoteAddr: "[::1]:12345",
			origin:     "",
			token:      "wrong-token",
			wantStatus: http.StatusOK,
		},
		{
			name:       "localhost passes",
			remoteAddr: "localhost:12345",
			origin:     "",
			token:      "wrong-token",
			wantStatus: http.StatusOK,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodGet, "/test", nil)
			req.RemoteAddr = tt.remoteAddr
			if tt.origin != "" {
				req.Header.Set("Origin", tt.origin)
			}
			if tt.token != "" {
				req.Header.Set("Authorization", "Bearer "+tt.token)
			}
			w := httptest.NewRecorder()
			handler.ServeHTTP(w, req)
			if w.Code != tt.wantStatus {
				t.Fatalf("expected %d, got %d", tt.wantStatus, w.Code)
			}
		})
	}
}

func TestWithAuth_NonLoopbackRequiresToken(t *testing.T) {
	nullLogger := log.New(os.Stderr, "", 0)
	srv, err := New(Config{AuthToken: "test-token"}, nullLogger)
	if err != nil {
		t.Fatalf("New() failed: %v", err)
	}

	handler := srv.withAuth(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))

	req := httptest.NewRequest(http.MethodGet, "/test", nil)
	req.RemoteAddr = "192.168.1.5:12345"
	req.Host = "192.168.1.5:8080"
	req.Header.Set("Origin", "http://evil.com")
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)

	if w.Code != http.StatusForbidden {
		t.Fatalf("expected 403 for origin mismatch, got %d", w.Code)
	}
}

func TestWithAuth_CookieToken(t *testing.T) {
	nullLogger := log.New(os.Stderr, "", 0)
	srv, err := New(Config{AuthToken: "test-token"}, nullLogger)
	if err != nil {
		t.Fatalf("New() failed: %v", err)
	}

	handler := srv.withAuth(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))

	tests := []struct {
		name         string
		cookie       string
		wantStatus   int
	}{
		{
			name:       "valid cookie token",
			cookie:     "mw_token=test-token",
			wantStatus: http.StatusOK,
		},
		{
			name:       "cookie with other values",
			cookie:     "foo=bar; mw_token=test-token; baz=qux",
			wantStatus: http.StatusOK,
		},
		{
			name:       "wrong cookie token",
			cookie:     "mw_token=wrong-token",
			wantStatus: http.StatusUnauthorized,
		},
		{
			name:       "empty cookie token",
			cookie:     "mw_token=",
			wantStatus: http.StatusUnauthorized,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodGet, "/test", nil)
			req.RemoteAddr = "192.168.1.5:12345"
			req.Header.Set("Cookie", tt.cookie)
			w := httptest.NewRecorder()
			handler.ServeHTTP(w, req)
			if w.Code != tt.wantStatus {
				t.Fatalf("expected %d, got %d", tt.wantStatus, w.Code)
			}
		})
	}
}

func TestWithAuth_RateLimiting(t *testing.T) {
	nullLogger := log.New(os.Stderr, "", 0)
	srv, err := New(Config{AuthToken: "test-token"}, nullLogger)
	if err != nil {
		t.Fatalf("New() failed: %v", err)
	}

	handler := srv.withAuth(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))

	req := httptest.NewRequest(http.MethodGet, "/test", nil)
	req.RemoteAddr = "192.168.1.5:12345"
	req.Header.Set("Authorization", "Bearer wrong-token")

	for i := 0; i < 20; i++ {
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, req)
		if w.Code != http.StatusUnauthorized {
			t.Fatalf("request %d: expected 401, got %d", i+1, w.Code)
		}
	}

	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)
	if w.Code != http.StatusTooManyRequests {
		t.Fatalf("after 20 failures: expected 429, got %d", w.Code)
	}
}

func TestExtractAuthToken(t *testing.T) {
	tests := []struct {
		name    string
		setup   func(*http.Request)
		want    string
	}{
		{
			name: "Authorization Bearer",
			setup: func(r *http.Request) {
				r.Header.Set("Authorization", "Bearer my-secret-token")
			},
			want: "my-secret-token",
		},
		{
			name: "query token",
			setup: func(r *http.Request) {
				r.URL.RawQuery = "token=url-token"
			},
			want: "url-token",
		},
		{
			name: "cookie mw_token",
			setup: func(r *http.Request) {
				r.Header.Set("Cookie", "mw_token=cookie-token")
			},
			want: "cookie-token",
		},
		{
			name: "priority Authorization over cookie",
			setup: func(r *http.Request) {
				r.Header.Set("Authorization", "Bearer bearer-token")
				r.Header.Set("Cookie", "mw_token=cookie-token")
			},
			want: "bearer-token",
		},
		{
			name: "priority query over cookie",
			setup: func(r *http.Request) {
				r.URL.RawQuery = "token=url-token"
				r.Header.Set("Cookie", "mw_token=cookie-token")
			},
			want: "url-token",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodGet, "/test", nil)
			tt.setup(req)
			if got := extractAuthToken(req); got != tt.want {
				t.Fatalf("extractAuthToken() = %q, want %q", got, tt.want)
			}
		})
	}
}
