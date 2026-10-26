package portal

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func TestGenerateInstanceID(t *testing.T) {
	id1 := generateInstanceID()
	id2 := generateInstanceID()

	if id1 == "" {
		t.Fatal("generateInstanceID returned empty string")
	}
	if id1 == id2 {
		t.Fatal("generateInstanceID returned same ID twice")
	}

	for i, c := range id1 {
		if c == '-' {
			continue
		}
		if !((c >= '0' && c <= '9') || (c >= 'a' && c <= 'f')) {
			t.Fatalf("instanceID contains invalid character %c at position %d", c, i)
		}
	}
}

func TestRegistrationFileWriteRead(t *testing.T) {
	tmpDir := t.TempDir()

	cfg := Config{
		PortalPort:   12345,
		InstancePort: 54321,
		Host:         "localhost",
		AuthToken:    "test-token",
		RegistryDir:  tmpDir,
		RepoName:     "test-repo",
		RepoHash:     "abc123",
		WorktreePath: "/path/to/test-worktree",
	}

	p := New(cfg)
	p.writeRegistration()

	regFile := filepath.Join(tmpDir, p.instanceID+".json")
	data, err := os.ReadFile(regFile)
	if err != nil {
		t.Fatalf("failed to read registration file: %v", err)
	}

	var reg registration
	if err := json.Unmarshal(data, &reg); err != nil {
		t.Fatalf("registration file is not valid JSON: %v", err)
	}

	if reg.InstanceID != p.instanceID {
		t.Fatalf("expected instanceID %q, got %q", p.instanceID, reg.InstanceID)
	}
	if reg.PID != os.Getpid() {
		t.Fatalf("expected PID %d, got %d", os.Getpid(), reg.PID)
	}
	if reg.Port != 54321 {
		t.Fatalf("expected port %d, got %d", 54321, reg.Port)
	}
	if reg.RepoName != "test-repo" {
		t.Fatalf("expected RepoName %q, got %q", "test-repo", reg.RepoName)
	}
	if reg.RepoHash != "abc123" {
		t.Fatalf("expected RepoHash %q, got %q", "abc123", reg.RepoHash)
	}
	if reg.StartedAt == "" {
		t.Fatal("expected StartedAt to be set")
	}
	if reg.Path != "/path/to/test-worktree" {
		t.Fatalf("expected Path %q, got %q", "/path/to/test-worktree", reg.Path)
	}
}

func TestIsPortalHolder(t *testing.T) {
	cfg := Config{PortalPort: 12345}
	p := New(cfg)

	if p.isPortalHolder() {
		t.Fatal("new Portal should not be a portal holder")
	}
}

func TestIsProcessAlive_PIDNotExist(t *testing.T) {
	alive := isProcessAlive(999999, 0)
	if alive {
		t.Fatal("process with non-existent PID should not be alive")
	}
}

func TestIsProcessAlive_PIDExistsNoPort(t *testing.T) {
	alive := isProcessAlive(os.Getpid(), 0)
	if !alive {
		t.Fatal("current process should be alive even without port")
	}
}

func TestIsProcessAlive_InvalidPID(t *testing.T) {
	alive := isProcessAlive(-1, 0)
	if alive {
		t.Fatal("negative PID should not be considered alive")
	}
}

func TestCSRFGenerate(t *testing.T) {
	state := newCSRFState()
	token := state.generate()

	if len(token) != 64 {
		t.Fatalf("expected 64 char hex token, got %d", len(token))
	}

	token2 := state.generate()
	if token == token2 {
		t.Fatal("two generated tokens should be different")
	}
}

func TestCSRFVerifyAndConsume_Success(t *testing.T) {
	state := newCSRFState()
	token := state.generate()

	if !state.verifyAndConsume(token, token) {
		t.Fatal("verifyAndConsume should succeed with matching token")
	}

	if state.verifyAndConsume(token, token) {
		t.Fatal("second verification should fail (token already used)")
	}
}

func TestCSRFVerifyAndConsume_Mismatch(t *testing.T) {
	state := newCSRFState()
	token := state.generate()

	if state.verifyAndConsume(token, "different") {
		t.Fatal("verifyAndConsume should fail with mismatched token")
	}
}

func TestCSRFVerifyAndConsume_Reuse(t *testing.T) {
	state := newCSRFState()
	token := state.generate()

	if !state.verifyAndConsume(token, token) {
		t.Fatal("first verification should succeed")
	}
	if state.verifyAndConsume(token, token) {
		t.Fatal("second verification should fail (token already used)")
	}
}

func TestCSRFVerifyAndConsume_Expired(t *testing.T) {
	state := newCSRFState()
	state.used["old-token"] = time.Now().Add(-6 * time.Minute)

	if !state.verifyAndConsume("old-token", "old-token") {
		t.Fatal("expired token should be accepted (spec: not in used OR in used but > 5min TTL)")
	}
}

func TestCSRFAllowCSRFRequest_FirstRequest(t *testing.T) {
	state := newCSRFState()

	if !state.allowCSRFRequest("192.168.1.1") {
		t.Fatal("first request from IP should be allowed")
	}
}

func TestCSRFAllowCSRFRequest_RateLimit(t *testing.T) {
	state := newCSRFState()
	ip := "192.168.1.2"

	if !state.allowCSRFRequest(ip) {
		t.Fatal("first request should be allowed")
	}
	if state.allowCSRFRequest(ip) {
		t.Fatal("second request within 1 second should be rate limited")
	}
}

func TestCSRFAllowCSRFRequest_DifferentIPs(t *testing.T) {
	state := newCSRFState()

	if !state.allowCSRFRequest("192.168.1.1") {
		t.Fatal("first IP should be allowed")
	}
	if !state.allowCSRFRequest("192.168.1.2") {
		t.Fatal("different IP should be allowed")
	}
}

func TestCSRFCleanup(t *testing.T) {
	state := newCSRFState()

	state.used["old-token"] = time.Now().Add(-6 * time.Minute)
	state.rateLimits["old-ip"] = time.Now().Add(-6 * time.Minute)

	state.cleanup()

	if _, exists := state.used["old-token"]; exists {
		t.Fatal("old token should have been cleaned up")
	}
	if _, exists := state.rateLimits["old-ip"]; exists {
		t.Fatal("old IP rate limit should have been cleaned up")
	}
}

func TestPortalStructFields(t *testing.T) {
	cfg := Config{
		PortalPort:  12345,
		Host:        "localhost",
		AuthToken:   "test-token",
		RegistryDir: "/tmp/portal",
		RepoName:    "test-repo",
		RepoHash:    "abc123",
	}

	p := New(cfg)

	if p.cfg.PortalPort != 12345 {
		t.Fatalf("expected PortalPort 12345, got %d", p.cfg.PortalPort)
	}
	if p.instanceID == "" {
		t.Fatal("instanceID should not be empty")
	}
	if p.done == nil {
		t.Fatal("done channel should be initialized")
	}
	if p.csrfState == nil {
		t.Fatal("csrfState should be initialized")
	}
}

func TestPortalCloseOnce(t *testing.T) {
	cfg := Config{PortalPort: 12345}
	p := New(cfg)

	p.Stop()
	p.Stop()
	p.Stop()
	p.Stop()
	p.Stop()
}

func TestGetIP(t *testing.T) {
	tests := []struct {
		remoteAddr string
		xff        string
		expected   string
	}{
		{"192.168.1.1:12345", "", "192.168.1.1"},
		{"192.168.1.1:12345", "10.0.0.1", "10.0.0.1"},
		{"192.168.1.1:12345", "10.0.0.1, 10.0.0.2", "10.0.0.1"},
	}

	for _, tt := range tests {
		req := &http.Request{
			RemoteAddr: tt.remoteAddr,
			Header:     http.Header{},
		}
		if tt.xff != "" {
			req.Header.Set("X-Forwarded-For", tt.xff)
		}

		ip := getIP(req)
		if ip != tt.expected {
			t.Errorf("getIP(remoteAddr=%q, XFF=%q) = %q, expected %q",
				tt.remoteAddr, tt.xff, ip, tt.expected)
		}
	}
}

func TestRegistration_NoAuthTokenInFile(t *testing.T) {
	tmpDir := t.TempDir()

	cfg := Config{
		PortalPort:  12345,
		AuthToken:   "secret-token-should-not-be-in-file",
		RegistryDir: tmpDir,
		RepoName:    "test-repo",
		RepoHash:    "abc123",
	}

	p := New(cfg)
	p.writeRegistration()

	regFile := filepath.Join(tmpDir, p.instanceID+".json")
	data, _ := os.ReadFile(regFile)

	var reg map[string]interface{}
	json.Unmarshal(data, &reg)

	if _, exists := reg["auth_token"]; exists {
		t.Fatal("registration file should not contain auth_token")
	}
}

func TestCSRFState_MaxCapacity(t *testing.T) {
	state := &csrfState{
		used:       make(map[string]time.Time),
		rateLimits: make(map[string]time.Time),
	}

	for i := 0; i < 9999; i++ {
		state.rateLimits[string(rune('0'+i%10))+string(rune('0'+(i/10)%10))] = time.Now()
	}

	if !state.allowCSRFRequest("new-ip") {
		t.Fatal("should allow request when map is at capacity (10000 limit is check before update)")
	}
}

func TestPortalMuConcurrentAccess(t *testing.T) {
	cfg := Config{PortalPort: 12345}
	p := New(cfg)

	var wg sync.WaitGroup
	for i := 0; i < 10; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < 100; j++ {
				_ = p.isPortalHolder()
			}
		}()
	}

	wg.Wait()
}

func fakeExitError() error {
	cmd := exec.Command("false")
	return cmd.Run()
}

func TestGetTailscaleServeStatus_AlreadyConfigured(t *testing.T) {
	origStatus := tsStatus
	defer func() { tsStatus = origStatus }()

	tsStatus = func(ctx context.Context) ([]byte, error) {
		return []byte(`{"TCP":{":443":"http://127.0.0.1:12345"}}`), nil
	}

	cfg := Config{PortalPort: 12345}
	p := New(cfg)

	status, err := p.getTailscaleServeStatus()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if status.currentPort != 12345 {
		t.Fatalf("expected currentPort 12345, got %d", status.currentPort)
	}
}

func TestGetTailscaleServeStatus_WrongPort(t *testing.T) {
	origStatus := tsStatus
	defer func() { tsStatus = origStatus }()

	tsStatus = func(ctx context.Context) ([]byte, error) {
		return []byte(`{"TCP":{":443":"http://127.0.0.1:9999"}}`), nil
	}

	cfg := Config{PortalPort: 12345}
	p := New(cfg)

	status, err := p.getTailscaleServeStatus()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if status.currentPort != 9999 {
		t.Fatalf("expected currentPort 9999, got %d", status.currentPort)
	}
}

func TestGetTailscaleServeStatus_NotConfigured(t *testing.T) {
	origStatus := tsStatus
	defer func() { tsStatus = origStatus }()

	tsStatus = func(ctx context.Context) ([]byte, error) {
		return []byte(`{"TCP":{}}`), nil
	}

	cfg := Config{PortalPort: 12345}
	p := New(cfg)

	status, err := p.getTailscaleServeStatus()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if status.currentPort != 0 {
		t.Fatalf("expected currentPort 0 (not configured), got %d", status.currentPort)
	}
}

func TestGetTailscaleServeStatus_NoTCPKey(t *testing.T) {
	origStatus := tsStatus
	defer func() { tsStatus = origStatus }()

	tsStatus = func(ctx context.Context) ([]byte, error) {
		return []byte(`{"Other":"value"}`), nil
	}

	cfg := Config{PortalPort: 12345}
	p := New(cfg)

	status, err := p.getTailscaleServeStatus()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if status.currentPort != 0 {
		t.Fatalf("expected currentPort 0, got %d", status.currentPort)
	}
}

func TestGetTailscaleServeStatus_JSONParseFailure(t *testing.T) {
	origStatus := tsStatus
	defer func() { tsStatus = origStatus }()

	tsStatus = func(ctx context.Context) ([]byte, error) {
		return []byte(`not json`), nil
	}

	cfg := Config{PortalPort: 12345}
	p := New(cfg)

	_, err := p.getTailscaleServeStatus()
	if err == nil {
		t.Fatal("expected error for unparseable JSON")
	}
	if !p.stoppedTailscaleServe {
		t.Fatal("expected stoppedTailscaleServe to be true after JSON parse failure")
	}

	_, err = p.getTailscaleServeStatus()
	if err == nil {
		t.Fatal("expected error when stoppedTailscaleServe is true")
	}
}

func TestGetTailscaleServeStatus_TailscaleNotFound(t *testing.T) {
	origStatus := tsStatus
	defer func() { tsStatus = origStatus }()

	tsStatus = func(ctx context.Context) ([]byte, error) {
		return nil, &exec.Error{Name: "tailscale", Err: exec.ErrNotFound}
	}

	cfg := Config{PortalPort: 12345}
	p := New(cfg)

	_, err := p.getTailscaleServeStatus()
	if err == nil {
		t.Fatal("expected error for tailscale not found")
	}
	if !p.stoppedTailscaleServe {
		t.Fatal("expected stoppedTailscaleServe to be true after tailscale not found")
	}

	_, err = p.getTailscaleServeStatus()
	if err == nil {
		t.Fatal("expected error when stoppedTailscaleServe is true")
	}
}

func TestGetTailscaleServeStatus_ExitError(t *testing.T) {
	origStatus := tsStatus
	defer func() { tsStatus = origStatus }()

	tsStatus = func(ctx context.Context) ([]byte, error) {
		return nil, fakeExitError()
	}

	cfg := Config{PortalPort: 12345}
	p := New(cfg)

	status, err := p.getTailscaleServeStatus()
	if err != nil {
		t.Fatalf("exit error should return status with currentPort 0, got error: %v", err)
	}
	if status.currentPort != 0 {
		t.Fatalf("expected currentPort 0 for non-zero exit, got %d", status.currentPort)
	}
	if p.stoppedTailscaleServe {
		t.Fatal("stoppedTailscaleServe should NOT be set for non-zero exit code")
	}
}

func TestRepairTailscaleServe_AlreadyCorrect(t *testing.T) {
	origAction := tsServeAction
	defer func() { tsServeAction = origAction }()

	var actionCalled bool
	tsServeAction = func(ctx context.Context, args ...string) error {
		actionCalled = true
		return nil
	}

	cfg := Config{PortalPort: 12345}
	p := New(cfg)
	p.repairTailscaleServe(12345)

	if actionCalled {
		t.Fatal("expected no action when currentPort matches portal port")
	}
}

func TestRepairTailscaleServe_PointsToWrongPort(t *testing.T) {
	origAction := tsServeAction
	defer func() { tsServeAction = origAction }()

	var actions []string
	tsServeAction = func(ctx context.Context, args ...string) error {
		actions = append(actions, strings.Join(args, " "))
		return nil
	}

	cfg := Config{PortalPort: 12345}
	p := New(cfg)
	p.repairTailscaleServe(9999)

	if len(actions) != 2 {
		t.Fatalf("expected 2 actions (stop + start), got %d: %v", len(actions), actions)
	}
	if actions[0] != "serve stop" {
		t.Fatalf("expected first action 'serve stop', got %q", actions[0])
	}
	if actions[1] != "serve --bg 12345" {
		t.Fatalf("expected second action 'serve --bg 12345', got %q", actions[1])
	}
}

func TestRepairTailscaleServe_NotConfigured(t *testing.T) {
	origAction := tsServeAction
	defer func() { tsServeAction = origAction }()

	var actions []string
	tsServeAction = func(ctx context.Context, args ...string) error {
		actions = append(actions, strings.Join(args, " "))
		return nil
	}

	cfg := Config{PortalPort: 12345}
	p := New(cfg)
	p.repairTailscaleServe(0)

	if len(actions) != 1 {
		t.Fatalf("expected 1 action (start only), got %d: %v", len(actions), actions)
	}
	if actions[0] != "serve --bg 12345" {
		t.Fatalf("expected 'serve --bg 12345', got %q", actions[0])
	}
}

func TestRepairTailscaleServe_StopFailsDoesNotStart(t *testing.T) {
	origAction := tsServeAction
	defer func() { tsServeAction = origAction }()

	var actions []string
	tsServeAction = func(ctx context.Context, args ...string) error {
		actions = append(actions, strings.Join(args, " "))
		if strings.Join(args, " ") == "serve stop" {
			return errors.New("stop failed")
		}
		return nil
	}

	cfg := Config{PortalPort: 12345}
	p := New(cfg)
	p.repairTailscaleServe(9999)

	if len(actions) != 1 {
		t.Fatalf("expected only 1 action (stop, which fails), got %d: %v", len(actions), actions)
	}
	if actions[0] != "serve stop" {
		t.Fatalf("expected 'serve stop', got %q", actions[0])
	}
}

func TestEnsureTailscaleServe_AllowsRepairWhenCalled(t *testing.T) {
	origStatus := tsStatus
	origAction := tsServeAction
	defer func() {
		tsStatus = origStatus
		tsServeAction = origAction
	}()

	tsStatus = func(ctx context.Context) ([]byte, error) {
		return []byte(`{"TCP":{":443":"http://127.0.0.1:9999"}}`), nil
	}

	var actions []string
	tsServeAction = func(ctx context.Context, args ...string) error {
		actions = append(actions, strings.Join(args, " "))
		return nil
	}

	cfg := Config{PortalPort: 12345}
	p := New(cfg)

	p.mu.Lock()
	p.ln = &net.TCPListener{}
	p.mu.Unlock()

	p.ensureTailscaleServe()

	if len(actions) != 2 {
		t.Fatalf("expected 2 actions (stop + start), got %d", len(actions))
	}
	if actions[0] != "serve stop" {
		t.Fatalf("expected first action 'serve stop', got %q", actions[0])
	}
}

func TestCleanupStaleTailscaleServe_AlreadyCorrect(t *testing.T) {
	origStatus := tsStatus
	origAction := tsServeAction
	defer func() {
		tsStatus = origStatus
		tsServeAction = origAction
	}()

	tsStatus = func(ctx context.Context) ([]byte, error) {
		return []byte(`{"TCP":{":443":"http://127.0.0.1:12345"}}`), nil
	}

	var actionCalled bool
	tsServeAction = func(ctx context.Context, args ...string) error {
		actionCalled = true
		return nil
	}

	cfg := Config{PortalPort: 12345}
	p := New(cfg)
	p.cleanupStaleTailscaleServe()

	if actionCalled {
		t.Fatal("expected no action when already correctly configured")
	}
}

func TestTailscaleServeLoop_IgnoresNonHolder(t *testing.T) {
	cfg := Config{PortalPort: 12345}
	p := New(cfg)

	p.wg.Add(1)
	go func() {
		p.tailscaleServeLoop()
	}()
	time.Sleep(10 * time.Millisecond)
	close(p.done)
	p.wg.Wait()
}

func TestCSPHashes_MatchesDashboardHTML(t *testing.T) {
	content := string(DashboardHTML)

	scriptRe := regexp.MustCompile(`(?s)<script>(.+?)</script>`)
	styleRe := regexp.MustCompile(`(?s)<style>(.+?)</style>`)

	scriptMatches := scriptRe.FindAllStringSubmatch(content, -1)
	styleMatches := styleRe.FindAllStringSubmatch(content, -1)

	if len(scriptMatches)+len(styleMatches) == 0 {
		t.Fatal("no <script> or <style> blocks found in dashboard.html")
	}

	if len(CSPHashes) == 0 {
		t.Fatal("CSPHashes is empty, run go generate")
	}

	if len(CSPHashes) != len(scriptMatches)+len(styleMatches) {
		t.Fatalf("CSPHashes has %d entries, but dashboard.html has %d script/style blocks — run go generate",
			len(CSPHashes), len(scriptMatches)+len(styleMatches))
	}

	seen := make(map[string]int)
	for i, entry := range CSPHashes {
		seen[entry[1]]++
		if seen[entry[1]] > 1 {
			t.Errorf("duplicate hash value %q in CSPHashes (index %d)", entry[1], i)
		}
	}

	scriptCount, styleCount := 0, 0
	for _, entry := range CSPHashes {
		switch entry[0] {
		case "script":
			scriptCount++
		case "style":
			styleCount++
		default:
			t.Errorf("unknown CSPHashes type %q", entry[0])
		}
	}
	if scriptCount != len(scriptMatches) {
		t.Errorf("CSPHashes has %d script entries, expected %d", scriptCount, len(scriptMatches))
	}
	if styleCount != len(styleMatches) {
		t.Errorf("CSPHashes has %d style entries, expected %d", styleCount, len(styleMatches))
	}

	var cspScripts, cspStyles []string
	for _, entry := range CSPHashes {
		switch entry[0] {
		case "script":
			cspScripts = append(cspScripts, entry[1])
		case "style":
			cspStyles = append(cspStyles, entry[1])
		}
	}

	for i, m := range scriptMatches {
		h := sha256.Sum256([]byte(m[1]))
		exp := "sha256-" + base64.StdEncoding.EncodeToString(h[:])
		found := false
		for _, csp := range cspScripts {
			if strings.Trim(csp, "'") == exp {
				found = true
				break
			}
		}
		if !found {
			t.Errorf("script block #%d hash %q not found in CSPHashes scripts", i+1, exp)
		}
	}

	for i, m := range styleMatches {
		h := sha256.Sum256([]byte(m[1]))
		exp := "sha256-" + base64.StdEncoding.EncodeToString(h[:])
		found := false
		for _, csp := range cspStyles {
			if strings.Trim(csp, "'") == exp {
				found = true
				break
			}
		}
		if !found {
			t.Errorf("style block #%d hash %q not found in CSPHashes styles", i+1, exp)
		}
	}
}

func TestCSPHashes_NotEmpty(t *testing.T) {
	if len(CSPHashes) == 0 {
		t.Fatal("CSPHashes is empty — run go generate ./internal/portal/")
	}
}

func TestCSPHashes_NoDuplicateTypes(t *testing.T) {
	seen := make(map[string]string)
	for _, entry := range CSPHashes {
		tag := entry[0]
		hashVal := entry[1]
		if prev, ok := seen[tag]; ok && prev == hashVal {
			t.Errorf("duplicate CSP hash entry: %s %s", tag, hashVal)
		}
		seen[tag] = hashVal
	}
}

func TestPortalPortContention(t *testing.T) {
	tmpDir := t.TempDir()
	portalPort := 0
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Skip("no available port for contention test")
	}
	portalPort = ln.Addr().(*net.TCPAddr).Port
	ln.Close()

	const goroutines = 5
	var holders int32
	var wg sync.WaitGroup
	ready := make(chan struct{}, goroutines)

	for i := 0; i < goroutines; i++ {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			cfg := Config{
				PortalPort:  portalPort,
				Host:        "127.0.0.1",
				AuthToken:   "test-token",
				RegistryDir: filepath.Join(tmpDir, fmt.Sprintf("client%d", idx)),
				RepoName:    "test-repo",
				RepoHash:    fmt.Sprintf("hash%d", idx),
			}
			p := New(cfg)
			p.Start()
			ready <- struct{}{}

			deadline := time.Now().Add(8 * time.Second)
			for time.Now().Before(deadline) {
				if p.isPortalHolder() {
					atomic.AddInt32(&holders, 1)
					break
				}
				time.Sleep(50 * time.Millisecond)
			}
			p.Stop()
		}(i)
	}

	for i := 0; i < goroutines; i++ {
		<-ready
	}
	wg.Wait()

	if holders == 0 {
		t.Errorf("expected at least 1 holder, got %d", holders)
	}
}

func TestConcurrentRegistryReadWrite(t *testing.T) {
	tmpDir := t.TempDir()
	cfg := Config{
		PortalPort:  12345,
		Host:        "localhost",
		AuthToken:   "test-token",
		RegistryDir: tmpDir,
		RepoName:    "test-repo",
		RepoHash:    "abc123",
	}
	p := New(cfg)
	p.writeRegistration()

	var wg sync.WaitGroup
	const readers = 10
	for i := 0; i < readers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < 50; j++ {
				os.ReadDir(tmpDir)
			}
		}()
	}

	for i := 0; i < 5; i++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			cfg := Config{
				PortalPort:  12345,
				Host:        "localhost",
				AuthToken:   "test-token",
				RegistryDir: tmpDir,
				RepoName:    "test-repo",
				RepoHash:    fmt.Sprintf("concurrent%d", id),
			}
			p := New(cfg)
			p.writeRegistration()
		}(i)
	}

	wg.Wait()
}

func TestCleanupDuringGoroutines(t *testing.T) {
	tmpDir := t.TempDir()
	cfg := Config{
		PortalPort:  12345,
		Host:        "localhost",
		AuthToken:   "test-token",
		RegistryDir: tmpDir,
		RepoName:    "test-repo",
		RepoHash:    "xyz789",
	}
	p := New(cfg)
	p.writeRegistration()

	deadInstanceIDs := make([]string, 10)
	for i := 0; i < 10; i++ {
		cfg := Config{
			PortalPort:  12345,
			Host:        "localhost",
			AuthToken:   "test-token",
			RegistryDir: tmpDir,
			RepoName:    "test-repo",
			RepoHash:    fmt.Sprintf("dead%d", i),
		}
		pp := New(cfg)
		deadInstanceIDs[i] = pp.instanceID
		reg := registration{
			InstanceID: pp.instanceID,
			PID:        999999,
			Port:       54321,
			RepoHash:   fmt.Sprintf("dead%d", i),
			Path:       fmt.Sprintf("/path/to/dead%d", i),
		}
		data, _ := json.Marshal(reg)
		os.WriteFile(filepath.Join(tmpDir, pp.instanceID+".json"), data, 0o600)
	}

	var wg sync.WaitGroup
	for i := 0; i < 5; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < 20; j++ {
				os.ReadDir(tmpDir)
			}
		}()
	}

	p.cleanupStaleRegistrations()
	wg.Wait()

	for _, instanceID := range deadInstanceIDs {
		if _, err := os.Stat(filepath.Join(tmpDir, instanceID+".json")); !os.IsNotExist(err) {
			t.Errorf("stale registration file %s.json should have been cleaned up", instanceID)
		}
	}
}

func TestStopAndClaimerLoopConcurrent(t *testing.T) {
	tmpDir := t.TempDir()
	cfg := Config{
		PortalPort:  12346,
		Host:        "127.0.0.1",
		AuthToken:   "test-token",
		RegistryDir: tmpDir,
		RepoName:    "test-repo",
		RepoHash:    "stop123",
	}
	p := New(cfg)
	p.Start()

	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		p.Stop()
	}()

	time.Sleep(100 * time.Millisecond)
	wg.Wait()
}

func TestRegistryDirDeletionRecovery(t *testing.T) {
	tmpDir := t.TempDir()
	cfg := Config{
		PortalPort:  12347,
		Host:        "localhost",
		AuthToken:   "test-token",
		RegistryDir: tmpDir,
		RepoName:    "test-repo",
		RepoHash:    "recovery123",
	}
	p := New(cfg)
	p.writeRegistration()

	os.RemoveAll(tmpDir)
	if _, err := os.Stat(tmpDir); !os.IsNotExist(err) {
		t.Fatal("expected registry dir to be removed before test")
	}

	p.cleanupStaleRegistrations()

	p.writeRegistration()
	if _, err := os.Stat(tmpDir); os.IsNotExist(err) {
		t.Fatal("registry dir was not recreated by writeRegistration()")
	}

	regFile := filepath.Join(tmpDir, p.instanceID+".json")
	if _, err := os.Stat(regFile); os.IsNotExist(err) {
		t.Fatal("registration file was not recreated after writeRegistration()")
	}
}

func TestHandleAuth_EmptyTokenReturns400(t *testing.T) {
	cfg := Config{
		PortalPort:  12348,
		AuthToken:   "",
		RegistryDir: t.TempDir(),
	}
	p := New(cfg)

	req := httptest.NewRequest("POST", "/api/auth", strings.NewReader(`{"token":"","csrf_token":"test"}`))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	p.handleAuth(w, req)

	if w.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d", w.Code)
	}

	var resp map[string]string
	json.NewDecoder(w.Body).Decode(&resp)
	if resp["error"] != "auth token not configured on server" {
		t.Fatalf("unexpected error message: %q", resp["error"])
	}
}

func TestHandleLogout_CSRFValidation(t *testing.T) {
	cfg := Config{
		PortalPort:  12349,
		AuthToken:   "test-token",
		RegistryDir: t.TempDir(),
	}
	p := New(cfg)

	csrfRec := httptest.NewRecorder()
	p.handleCSRFToken(csrfRec, httptest.NewRequest("GET", "/api/csrf-token", nil))
	if csrfRec.Code != http.StatusOK {
		t.Fatalf("CSRF token request failed: %d", csrfRec.Code)
	}
	csrfCookies := csrfRec.Result().Cookies()
	var csrfCookie *http.Cookie
	for _, c := range csrfCookies {
		if c.Name == "mw_csrf" {
			csrfCookie = c
			break
		}
	}
	if csrfCookie == nil {
		t.Fatal("no mw_csrf cookie")
	}
	validToken := csrfCookie.Value

	tests := []struct {
		name       string
		csrfCookie *http.Cookie
		csrfBody   string
		wantStatus int
	}{
		{
			name:       "valid CSRF token",
			csrfCookie: csrfCookie,
			csrfBody:   validToken,
			wantStatus: http.StatusOK,
		},
		{
			name:       "missing CSRF token",
			csrfCookie: nil,
			csrfBody:   "",
			wantStatus: http.StatusForbidden,
		},
		{
			name:       "CSRF token mismatch",
			csrfCookie: &http.Cookie{Name: "mw_csrf", Value: "cookie-token"},
			csrfBody:   "body-token",
			wantStatus: http.StatusForbidden,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			body := `{"csrf_token":"` + tt.csrfBody + `"}`
			if tt.csrfBody == "" {
				body = `{}`
			}
			req := httptest.NewRequest("POST", "/api/logout", strings.NewReader(body))
			req.Header.Set("Content-Type", "application/json")
			if tt.csrfCookie != nil {
				req.AddCookie(tt.csrfCookie)
			}
			w := httptest.NewRecorder()
			p.handleLogout(w, req)

			if w.Code != tt.wantStatus {
				t.Errorf("expected %d, got %d", tt.wantStatus, w.Code)
			}
		})
	}
}

func TestEndToEndAuthFlow(t *testing.T) {
	tmpDir := t.TempDir()
	cfg := Config{
		PortalPort:  12350,
		Host:        "localhost",
		AuthToken:   "e2e-test-token",
		RegistryDir: tmpDir,
		RepoName:    "test-repo",
		RepoHash:    "e2e123",
	}
	p := New(cfg)

	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Skip("port not available")
	}
	defer ln.Close()
	instancePort := ln.Addr().(*net.TCPAddr).Port

	reg := registration{
		InstanceID: p.instanceID,
		PID:        os.Getpid(),
		Port:       instancePort,
		RepoHash:   "e2e123",
		Path:       "/path/to/e2e123",
	}
	data, _ := json.Marshal(reg)
	os.WriteFile(filepath.Join(tmpDir, p.instanceID+".json"), data, 0o600)

	go http.Serve(ln, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))

	csrfReq := httptest.NewRequest("GET", "/api/csrf-token", nil)
	csrfRec := httptest.NewRecorder()
	p.handleCSRFToken(csrfRec, csrfReq)
	if csrfRec.Code != http.StatusOK {
		t.Fatalf("CSRF token request failed: %d", csrfRec.Code)
	}

	var csrfResp map[string]string
	json.NewDecoder(csrfRec.Body).Decode(&csrfResp)
	authCSRFToken := csrfResp["csrf_token"]
	if authCSRFToken == "" {
		t.Fatal("no CSRF token in response")
	}

	cookies := csrfRec.Result().Cookies()
	var csrfCookieValue string
	for _, c := range cookies {
		if c.Name == "mw_csrf" {
			csrfCookieValue = c.Value
			break
		}
	}
	if csrfCookieValue == "" {
		t.Fatal("no mw_csrf cookie set")
	}

	authReq := httptest.NewRequest("POST", "/api/auth", strings.NewReader(
		`{"token":"e2e-test-token","csrf_token":"`+authCSRFToken+`"}`))
	authReq.Header.Set("Content-Type", "application/json")
	authReq.AddCookie(&http.Cookie{Name: "mw_csrf", Value: csrfCookieValue})
	authRec := httptest.NewRecorder()
	p.handleAuth(authRec, authReq)
	if authRec.Code != http.StatusOK {
		t.Fatalf("auth request failed: %d - %s", authRec.Code, authRec.Body.String())
	}

	authCookies := authRec.Result().Cookies()
	var tokenCookieValue string
	for _, c := range authCookies {
		if c.Name == "mw_token" {
			tokenCookieValue = c.Value
			break
		}
	}
	if tokenCookieValue == "" {
		t.Fatal("no mw_token cookie set after auth")
	}

	listReq := httptest.NewRequest("GET", "/api/list", nil)
	listReq.AddCookie(&http.Cookie{Name: "mw_token", Value: tokenCookieValue})
	listRec := httptest.NewRecorder()
	p.withAuth(p.handleList)(listRec, listReq)
	if listRec.Code != http.StatusOK {
		t.Fatalf("list request failed: %d", listRec.Code)
	}

	listCookies := listRec.Result().Cookies()
	var slidingCookieFound bool
	for _, c := range listCookies {
		if c.Name == "mw_token" && c.MaxAge == 86400 {
			slidingCookieFound = true
			tokenCookieValue = c.Value
			break
		}
	}
	if !slidingCookieFound {
		t.Fatal("sliding auth cookie not refreshed on /api/list response")
	}

	var listResp map[string]interface{}
	json.NewDecoder(listRec.Body).Decode(&listResp)
	if listResp["is_portal"] == nil {
		t.Fatal("is_portal missing from list response")
	}

	time.Sleep(time.Second)
	logoutCSRFReq := httptest.NewRequest("GET", "/api/csrf-token", nil)
	logoutCSRFRec := httptest.NewRecorder()
	p.handleCSRFToken(logoutCSRFRec, logoutCSRFReq)
	if logoutCSRFRec.Code != http.StatusOK {
		t.Fatalf("logout CSRF token request failed: %d", logoutCSRFRec.Code)
	}
	var logoutCSRFCookie *http.Cookie
	for _, c := range logoutCSRFRec.Result().Cookies() {
		if c.Name == "mw_csrf" {
			logoutCSRFCookie = c
			break
		}
	}
	if logoutCSRFCookie == nil {
		t.Fatal("no mw_csrf cookie for logout")
	}

	logoutReq := httptest.NewRequest("POST", "/api/logout", strings.NewReader(
		`{"csrf_token":"`+logoutCSRFCookie.Value+`"}`))
	logoutReq.Header.Set("Content-Type", "application/json")
	logoutReq.AddCookie(logoutCSRFCookie)
	logoutReq.AddCookie(&http.Cookie{Name: "mw_token", Value: tokenCookieValue})
	logoutRec := httptest.NewRecorder()
	p.handleLogout(logoutRec, logoutReq)
	if logoutRec.Code != http.StatusOK {
		t.Fatalf("logout failed: %d", logoutRec.Code)
	}

	afterLogoutReq := httptest.NewRequest("GET", "/api/list", nil)
	afterLogoutRec := httptest.NewRecorder()
	p.withAuth(p.handleList)(afterLogoutRec, afterLogoutReq)
	if afterLogoutRec.Code != http.StatusUnauthorized {
		t.Fatalf("after logout without cookie: expected 401, got %d", afterLogoutRec.Code)
	}
}

func TestLogPrefixVerification(t *testing.T) {
	var logBuf bytes.Buffer
	origOutput := log.Writer()
	log.SetOutput(&logBuf)
	defer log.SetOutput(origOutput)

	tmpDir := t.TempDir()
	cfg := Config{
		PortalPort:  12351,
		Host:        "localhost",
		AuthToken:   "test-token",
		RegistryDir: tmpDir,
		RepoName:    "test-repo",
		RepoHash:    "log123",
	}
	p := New(cfg)
	p.writeRegistration()
	p.writePortalStatus()
	p.cleanupStaleTailscaleServe()
	p.cleanupStaleRegistrations()

	output := logBuf.String()
	if !strings.Contains(output, "[portal]") {
		t.Errorf("expected log output to contain [portal] prefix, got: %s", output)
	}
}

// TestPortalWithAuthSyncsCookie pins the portal↔daemon symmetry: a
// request authenticated via the address-bar token must land the mw_token
// cookie, exactly like the daemon's withAuth, so portal embeds and
// future routes authenticate via the cookie alone.
func TestPortalWithAuthSyncsCookie(t *testing.T) {
	p := New(Config{
		PortalPort: 12345,
		Host:       "localhost",
		AuthToken:  "test-token",
	})

	// Unauthenticated → 401.
	req := httptest.NewRequest(http.MethodGet, "/api/list", nil)
	rec := httptest.NewRecorder()
	p.withAuth(p.handleList)(rec, req)
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("unauthenticated /api/list: status=%d, want 401", rec.Code)
	}

	// Address-bar token authenticates and lands the cookie.
	req2 := httptest.NewRequest(http.MethodGet, "/api/list?token=test-token", nil)
	rec2 := httptest.NewRecorder()
	p.withAuth(p.handleList)(rec2, req2)
	if rec2.Code != http.StatusOK {
		t.Fatalf("authed /api/list: status=%d, want 200", rec2.Code)
	}
	var got *http.Cookie
	for _, c := range rec2.Result().Cookies() {
		if c.Name == "mw_token" {
			got = c
		}
	}
	if got == nil {
		t.Fatalf("authed portal response must sync the mw_token cookie; got %v", rec2.Result().Cookies())
	}
	if got.Value != "test-token" || !got.HttpOnly {
		t.Fatalf("mw_token cookie = %+v, want value test-token, HttpOnly", got)
	}
}

// === issue #131: registration cleanup matrix, heartbeat self-heal ===

// openPort returns a live TCP listener port; the listener stays open until
// the test ends (dial succeeds without accept — the kernel answers SYN from
// the listen backlog).
func openPort(t *testing.T) int {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("no available port: %v", err)
	}
	t.Cleanup(func() { ln.Close() })
	return ln.Addr().(*net.TCPAddr).Port
}

// closedPort returns a port with no listener: dialing it fails fast with
// connection refused.
func closedPort(t *testing.T) int {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("no available port: %v", err)
	}
	port := ln.Addr().(*net.TCPAddr).Port
	ln.Close()
	return port
}

// The two mutually exclusive D3 log branches, anchored on their PREFIX ONLY and
// spelled exactly once here. The tails are deliberately NOT part of the anchor:
// an assertion of the form "this branch logged nothing" must keep matching even
// after the wording after the prefix changes, otherwise a future rewording makes
// it match nothing and pass vacuously. The two anchors cannot match each other —
// after "[portal] cleanup: " the success branch says "removed", the failure
// branch says "failed". The exact wording is still pinned, by the fragment
// checks inside TestCleanupStaleRegistrations_MatrixAndRemovalLogs.
const (
	cleanupSuccessLogPrefix = "[portal] cleanup: removed "
	cleanupFailureLogPrefix = "[portal] cleanup: failed to remove "
)

// staleHeartbeat returns a heartbeat stamp that is EXPIRED by any sane lease.
// The age is a FIXED 2 h and deliberately does NOT reference registrationLease:
// a fixture built as -registrationLease-1m moves in lockstep with the constant
// under test, so shortening the lease (down to less than one heartbeat
// interval, which silently voids matrix cell 3's protection — the original
// issue #131 bug) would keep every behavioural test here green. The lease
// floors are nailed separately by TestRegistrationLivenessConstantsPinned.
func staleHeartbeat() string {
	return time.Now().Add(-2 * time.Hour).Format(time.RFC3339)
}

func freshHeartbeat() string {
	return time.Now().Format(time.RFC3339)
}

// TestRegistrationLivenessConstantsPinned nails the VALUES plan §四 wrote down,
// not just the behaviour they currently produce. The behavioural matrix tests
// cannot cover this on their own: they construct staleness relative to the
// lease (see staleHeartbeat above) or in absolute terms that stay stale for any
// lease, so shrinking registrationLease below defaultHeartbeatInterval — which
// makes a LIVE daemon's fresh heartbeat look expired and restores the exact
// issue #131 deletion — passes every one of them. The floors below are the two
// lower bounds the plan states for the lease, plus the 1 s probe timeout the
// plan and CHANGELOG promise and that keeps the cleanup side's observation
// window identical to the list side's. defaultHeartbeatInterval == 60 s is
// already pinned by TestHeartbeatLoopPeriodicallyRewritesRegistrationFile, so
// it is not restated here; it only appears in the first floor as a multiplier.
func TestRegistrationLivenessConstantsPinned(t *testing.T) {
	hbFloor := 10 * defaultHeartbeatInterval
	if registrationLease < hbFloor {
		t.Fatalf("registrationLease = %v, want >= 10*defaultHeartbeatInterval (%v): a lease shorter than ten heartbeats lets one GC pause, RFC3339 second-truncation or scheduler stall expire a LIVE daemon's lease, re-opening issue #131 (matrix cell 3 must keep it)", registrationLease, hbFloor)
	}
	twoSweeps := 2 * 5 * time.Minute
	if registrationLease < twoSweeps {
		t.Fatalf("registrationLease = %v, want >= 2 cleanup ticker periods (%v): the cleaner must not expire a foreign lease inside its own sweep gap", registrationLease, twoSweeps)
	}
	if tcpProbeTimeout != 1*time.Second {
		t.Fatalf("tcpProbeTimeout = %v, want the 1 s both plan §四 and the CHANGELOG promise, and the same dial timeout the list side uses in isProcessAlive so the two sides' observations stay cross-comparable", tcpProbeTimeout)
	}
}

// Matrix cell 1: PID not alive → remove, reason pid-exited — regardless of
// heartbeat freshness or TCP reachability.
func TestDecideRegistrationCleanup_PidExitedRemoved(t *testing.T) {
	port := openPort(t)
	for _, pid := range []int{999999, 0, -1} {
		remove, reason := decideRegistrationCleanup(registration{
			InstanceID:  "dead",
			PID:         pid,
			Port:        port,
			HeartbeatAt: freshHeartbeat(),
		}, time.Now())
		if !remove || reason != "pid-exited" {
			t.Errorf("pid=%d: remove=%v reason=%q, want true/pid-exited", pid, remove, reason)
		}
	}
}

// Matrix cell 2: PID alive + legacy file (missing or unparseable heartbeat)
// → keep. During the upgrade-coexistence window cleanup never deletes on
// TCP/lease grounds; the owner upgrades the file on its next restart.
func TestDecideRegistrationCleanup_LegacyHeartbeatKept(t *testing.T) {
	for _, hb := range []string{"", "not-a-timestamp", "2026-13-45T99:99:99Z"} {
		remove, reason := decideRegistrationCleanup(registration{
			InstanceID:  "legacy",
			PID:         os.Getpid(),
			Port:        closedPort(t),
			HeartbeatAt: hb,
		}, time.Now())
		if remove {
			t.Errorf("heartbeat=%q: legacy registration was removed (reason=%q), want kept", hb, reason)
		}
	}
}

// Matrix cell 3 — THE issue #131 regression test: PID alive + fresh
// heartbeat + TCP probe target unreachable → MUST stay alive and be kept.
// The old cleanup deleted exactly here (PID alive, 1s loopback dial failed),
// making the project vanish from the Dashboard permanently.
func TestDecideRegistrationCleanup_FreshHeartbeatSurvivesTcpFailure(t *testing.T) {
	remove, reason := decideRegistrationCleanup(registration{
		InstanceID:  "alive-fresh-hb",
		PID:         os.Getpid(),
		Port:        closedPort(t),
		HeartbeatAt: freshHeartbeat(),
	}, time.Now())
	if remove {
		t.Fatalf("issue #131 regression: live daemon with fresh heartbeat was marked for removal (reason=%q)", reason)
	}
}

// Matrix cell 4: PID alive + lease expired + TCP reachable → keep.
func TestDecideRegistrationCleanup_LeaseExpiredTcpReachableKept(t *testing.T) {
	remove, reason := decideRegistrationCleanup(registration{
		InstanceID:  "expired-reachable",
		PID:         os.Getpid(),
		Port:        openPort(t),
		HeartbeatAt: staleHeartbeat(),
	}, time.Now())
	if remove {
		t.Fatalf("registration with reachable port was marked for removal (reason=%q), want kept", reason)
	}
}

// Matrix cell 5: PID alive + lease expired + TCP unreachable → remove.
// Safe because the owner's heartbeat rewrites the file whole within one
// heartbeatInterval if the verdict was wrong.
func TestDecideRegistrationCleanup_LeaseExpiredTcpUnreachableRemoved(t *testing.T) {
	remove, reason := decideRegistrationCleanup(registration{
		InstanceID:  "expired-unreachable",
		PID:         os.Getpid(),
		Port:        closedPort(t),
		HeartbeatAt: staleHeartbeat(),
	}, time.Now())
	if !remove || reason != "lease-expired-tcp-unreachable" {
		t.Fatalf("remove=%v reason=%q, want true/lease-expired-tcp-unreachable", remove, reason)
	}
}

// A port-less registration cannot be TCP-probed, so it can never be
// disproved — kept even with an expired lease.
func TestDecideRegistrationCleanup_NoPortNeverRemoved(t *testing.T) {
	remove, reason := decideRegistrationCleanup(registration{
		InstanceID:  "no-port",
		PID:         os.Getpid(),
		Port:        0,
		HeartbeatAt: staleHeartbeat(),
	}, time.Now())
	if remove {
		t.Fatalf("port-less registration was marked for removal (reason=%q), want kept", reason)
	}
}

func TestCleanupStaleRegistrations_MatrixAndRemovalLogs(t *testing.T) {
	tmpDir := t.TempDir()
	p := New(Config{
		PortalPort:  12352,
		Host:        "localhost",
		AuthToken:   "test-token",
		RegistryDir: tmpDir,
		RepoName:    "test-repo",
		RepoHash:    "matrix131",
	})

	writeRegFile := func(name string, reg registration) {
		t.Helper()
		data, err := json.Marshal(reg)
		if err != nil {
			t.Fatalf("marshal %s: %v", name, err)
		}
		if err := os.WriteFile(filepath.Join(tmpDir, name), data, 0o600); err != nil {
			t.Fatalf("write %s: %v", name, err)
		}
	}

	// Own entry: written by the portal itself, then sabotaged with a dead
	// PID and an expired lease — cleanup must skip it by instance ID alone.
	p.writeRegistration()
	writeRegFile(p.instanceID+".json", registration{
		InstanceID:  p.instanceID,
		PID:         999999,
		Port:        closedPort(t),
		RepoName:    "self",
		HeartbeatAt: staleHeartbeat(),
	})

	// Non-registration files in the registry dir must never be touched.
	if err := os.WriteFile(filepath.Join(tmpDir, "portal.json"), []byte("{}"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(tmpDir, "notes.txt"), []byte("keep me"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(tmpDir, "broken.json"), []byte("{not json"), 0o600); err != nil {
		t.Fatal(err)
	}

	// Matrix cells as external files: cells 2-5 one file each, cell 1 twice —
	// the plain victim below and the mislabeled fixture further down.
	writeRegFile("dead-pid.json", registration{
		InstanceID: "dead-pid", PID: 999999, Port: closedPort(t),
		RepoName: "dead-repo", HeartbeatAt: freshHeartbeat(),
	})
	writeRegFile("fresh-hb.json", registration{
		InstanceID: "fresh-hb", PID: os.Getpid(), Port: closedPort(t),
		RepoName: "alive-repo", HeartbeatAt: freshHeartbeat(),
	})
	// True legacy file: raw JSON with NO heartbeat_at key at all.
	if err := os.WriteFile(filepath.Join(tmpDir, "legacy.json"),
		[]byte(`{"instance_id":"legacy","pid":`+strconv.Itoa(os.Getpid())+`,"port":`+strconv.Itoa(closedPort(t))+`,"repo_name":"legacy-repo","started_at":"2026-10-25T00:00:00Z","path":"/tmp/legacy"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	writeRegFile("expired-reachable.json", registration{
		InstanceID: "expired-reachable", PID: os.Getpid(), Port: openPort(t),
		RepoName: "reachable-repo", HeartbeatAt: staleHeartbeat(),
	})
	writeRegFile("expired-unreachable.json", registration{
		InstanceID: "expired-unreachable", PID: os.Getpid(), Port: closedPort(t),
		RepoName: "unreachable-repo", HeartbeatAt: staleHeartbeat(),
	})
	// MISLABELED fixture (matrix cell 1, removable): the DIRECTORY ENTRY NAME
	// deliberately disagrees with the instance_id inside the file. Every other
	// REMOVABLE fixture here is named <its own instance_id>.json, so a log line
	// that derived file= from the parsed content instead of from the directory
	// entry would still match this test's expectations verbatim for all of them.
	// This fixture is the only one that separates the two identity fields, and it
	// makes both halves of the D3 contract observable on the SAME line: file= must
	// be mislabeled.json (the entry actually unlinked) while the positional id
	// must stay content-id-mislabeled (the content that was parsed).
	writeRegFile("mislabeled.json", registration{
		InstanceID: "content-id-mislabeled", PID: 999999, Port: closedPort(t),
		RepoName: "mislabeled-repo", HeartbeatAt: freshHeartbeat(),
	})

	var logBuf bytes.Buffer
	origOutput := log.Writer()
	log.SetOutput(&logBuf)
	defer log.SetOutput(origOutput)

	p.cleanupStaleRegistrations()

	for _, kept := range []string{
		p.instanceID + ".json", "portal.json", "notes.txt", "broken.json",
		"fresh-hb.json", "legacy.json", "expired-reachable.json",
	} {
		if _, err := os.Stat(filepath.Join(tmpDir, kept)); err != nil {
			t.Errorf("%s should have been kept: %v", kept, err)
		}
	}
	for _, removed := range []string{"dead-pid.json", "expired-unreachable.json", "mislabeled.json"} {
		if _, err := os.Stat(filepath.Join(tmpDir, removed)); !os.IsNotExist(err) {
			t.Errorf("%s should have been removed", removed)
		}
	}

	// D3: every removal is logged with instance id, the actual directory entry
	// name (file=) and reason; kept files never appear in a removal line. The
	// two branches are tallied by cleanupSuccessLogPrefix / cleanupFailureLogPrefix
	// — prefix-only anchors that survive a rewording of the tail — while the
	// exact wording below is pinned by the "removed stale registration <id> ("
	// fragments.
	output := logBuf.String()
	var removalLines []string
	for _, line := range strings.Split(output, "\n") {
		if strings.Contains(line, cleanupSuccessLogPrefix) {
			removalLines = append(removalLines, line)
		}
	}
	if len(removalLines) != 3 {
		t.Fatalf("expected exactly 3 removal log lines, got %d:\n%s", len(removalLines), output)
	}
	joined := strings.Join(removalLines, "\n")
	if n := strings.Count(output, cleanupFailureLogPrefix); n != 0 {
		t.Errorf("all three deletions succeeded, so no failure line may be logged (found %d):\n%s", n, output)
	}
	// ARB-4: file= carries the REAL directory entry name, side by side with
	// the instance_id read from the file's content. Each fixture is located by
	// its file= name — the identifier unique per removal now that two cells
	// share reason=pid-exited.
	for _, want := range []struct{ file, id, reason string }{
		{"file=dead-pid.json", "dead-pid", "reason=pid-exited"},
		{"file=expired-unreachable.json", "expired-unreachable", "reason=lease-expired-tcp-unreachable"},
		{"file=mislabeled.json", "content-id-mislabeled", "reason=pid-exited"},
	} {
		var line string
		for _, l := range removalLines {
			if strings.Contains(l, want.file) {
				line = l
			}
		}
		if line == "" {
			t.Errorf("no removal line carries %s:\n%s", want.file, joined)
			continue
		}
		for _, frag := range []string{"removed stale registration " + want.id + " (", want.file, want.reason} {
			if !strings.Contains(line, frag) {
				t.Errorf("removal line for %s must contain %q:\n%s", want.file, frag, line)
			}
		}
	}
	// The mislabeled fixture is what gives file= its discriminating power: the
	// content-derived name must NEVER show up as a file= value.
	if strings.Contains(output, "file=content-id-mislabeled.json") {
		t.Errorf("file= must be the directory entry name, never instance_id+\".json\" of the parsed content:\n%s", output)
	}
	for _, line := range removalLines {
		for _, keptID := range []string{"fresh-hb", "legacy", "expired-reachable", p.instanceID} {
			if strings.Contains(line, "registration "+keptID+" ") {
				t.Errorf("kept registration %s appeared in a removal log line: %s", keptID, line)
			}
		}
	}
}

// D2 self-heal, UNIT level: a single writeRegistration() call is the whole
// rebuild primitive — after any deletion (cleanup false positive, manual rm,
// tmp cleaner) it recreates the file whole with a refreshed heartbeat and a
// non-drifted started_at. It does NOT on its own prove the PERIODIC wiring
// (ticker + Start); that is TestHeartbeatLoopPeriodicallyRewritesRegistrationFile.
func TestHeartbeatSelfHealsRegistrationFile(t *testing.T) {
	tmpDir := t.TempDir()
	p := New(Config{
		PortalPort:   12353,
		InstancePort: 54321,
		Host:         "localhost",
		AuthToken:    "test-token",
		RegistryDir:  tmpDir,
		RepoName:     "selfheal-repo",
		RepoHash:     "heal131",
		WorktreePath: "/path/to/heal131",
	})

	p.writeRegistration()
	regFile := filepath.Join(tmpDir, p.instanceID+".json")

	if _, err := os.Stat(regFile); err != nil {
		t.Fatalf("registration file missing after first write: %v", err)
	}
	first, err := os.ReadFile(regFile)
	if err != nil {
		t.Fatal(err)
	}
	var reg1 registration
	if err := json.Unmarshal(first, &reg1); err != nil {
		t.Fatal(err)
	}
	if reg1.HeartbeatAt == "" {
		t.Fatal("writeRegistration must stamp heartbeat_at")
	}
	hb1, err := time.Parse(time.RFC3339, reg1.HeartbeatAt)
	if err != nil {
		t.Fatalf("heartbeat_at %q is not RFC3339: %v", reg1.HeartbeatAt, err)
	}

	// Simulate the issue #131 failure: the registration file disappears.
	if err := os.Remove(regFile); err != nil {
		t.Fatal(err)
	}

	// RFC3339 has second granularity — wait past it to see the refresh.
	time.Sleep(1100 * time.Millisecond)

	// The heartbeat tick calls exactly this: the file comes back whole.
	p.writeRegistration()

	second, err := os.ReadFile(regFile)
	if err != nil {
		t.Fatalf("registration file not recreated by heartbeat write: %v", err)
	}
	var reg2 registration
	if err := json.Unmarshal(second, &reg2); err != nil {
		t.Fatal(err)
	}
	hb2, err := time.Parse(time.RFC3339, reg2.HeartbeatAt)
	if err != nil {
		t.Fatalf("refreshed heartbeat_at %q is not RFC3339: %v", reg2.HeartbeatAt, err)
	}
	if !hb2.After(hb1) {
		t.Fatalf("heartbeat_at not refreshed: %v -> %v", hb1, hb2)
	}
	if reg2.InstanceID != p.instanceID || reg2.PID != os.Getpid() || reg2.Port != 54321 ||
		reg2.RepoName != "selfheal-repo" || reg2.RepoHash != "heal131" || reg2.Path != "/path/to/heal131" {
		t.Fatalf("recreated registration lost fields: %+v", reg2)
	}
	// started_at reports the process start, never the last heartbeat.
	if reg2.StartedAt != p.startedAt.Format(time.RFC3339) {
		t.Fatalf("started_at drifted to the heartbeat time: %q, want %q", reg2.StartedAt, p.startedAt.Format(time.RFC3339))
	}
}

// The heartbeat goroutine must exit promptly when the portal stops, so
// Stop() never blocks on a dangling writer.
func TestHeartbeatLoopExitsOnDone(t *testing.T) {
	p := New(Config{PortalPort: 12354, RegistryDir: t.TempDir()})

	p.wg.Add(1)
	go p.heartbeatLoop()
	close(p.done)

	done := make(chan struct{})
	go func() { p.wg.Wait(); close(done) }()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("heartbeatLoop did not exit after p.done was closed")
	}
}

// ARB-1 / D2: the PERIODIC heartbeat wiring end to end. This drives the real
// p.Start(), so it pins both ends of the chain at once — Start actually
// spawns heartbeatLoop, and heartbeatLoop's ticker actually rewrites the file
// on Portal.heartbeatInterval (injected before Start; defaultHeartbeatInterval
// in production). A hand-called second writeRegistration() would pin neither;
// TestHeartbeatSelfHealsRegistrationFile is the unit-level counterpart that
// covers the rebuild primitive and the field integrity only.
func TestHeartbeatLoopPeriodicallyRewritesRegistrationFile(t *testing.T) {
	tmpDir := t.TempDir()

	// Take PortalPort first so claimerLoop USUALLY cannot bind it and no real
	// HTTP server starts; the heartbeat does not depend on holding the portal
	// port. Not a guarantee: on a dual-stack host "localhost" can resolve to
	// ::1 while this listener sits on 127.0.0.1, so claimerLoop may still bind
	// successfully. That is harmless for what this test asserts — the heartbeat
	// rebuild, a strictly newer heartbeat_at and an unchanged started_at are all
	// independent of who holds the portal port — and the deferred Stop()
	// cleans up both branches (holder: srv/ln shutdown plus the portal.json
	// unlink; non-holder: registration unlink only).
	portalPort := openPort(t)

	p := New(Config{
		PortalPort:   portalPort,
		InstancePort: 54321,
		Host:         "localhost",
		AuthToken:    "test-token",
		RegistryDir:  tmpDir,
		RepoName:     "period131",
		RepoHash:     "period131",
		WorktreePath: "/path/to/period131",
	})
	if defaultHeartbeatInterval != 60*time.Second {
		t.Fatalf("defaultHeartbeatInterval = %v, want the 60 s production default", defaultHeartbeatInterval)
	}
	if p.heartbeatInterval != defaultHeartbeatInterval {
		t.Fatalf("New() must seed heartbeatInterval from defaultHeartbeatInterval (%v), got %v", defaultHeartbeatInterval, p.heartbeatInterval)
	}
	// The only legal override point: BEFORE the heartbeat goroutine exists.
	p.heartbeatInterval = 20 * time.Millisecond

	if err := p.Start(); err != nil {
		t.Fatalf("Start: %v", err)
	}
	defer p.Stop()

	regFile := filepath.Join(tmpDir, p.instanceID+".json")
	data, err := os.ReadFile(regFile)
	if err != nil {
		t.Fatalf("Start() must write the registration file: %v", err)
	}
	var initial registration
	if err := json.Unmarshal(data, &initial); err != nil {
		t.Fatalf("initial registration is not valid JSON: %v", err)
	}
	hb1, err := time.Parse(time.RFC3339, initial.HeartbeatAt)
	if err != nil {
		t.Fatalf("initial heartbeat_at %q is not RFC3339: %v", initial.HeartbeatAt, err)
	}
	if initial.StartedAt == "" {
		t.Fatal("initial started_at must be stamped")
	}

	// The issue #131 failure, verbatim: the file disappears under a live daemon.
	if err := os.Remove(regFile); err != nil {
		t.Fatalf("remove registration file: %v", err)
	}

	// RFC3339 has second granularity, so "the file came back" and "the file
	// came back AGAIN with a newer stamp" are two different observations:
	// waiting for a STRICTLY newer heartbeat_at is what proves the rewrite was
	// periodic (a second tick after the rebuild), not a single rebuild.
	var healed registration
	deadline := time.Now().Add(3 * time.Second)
	for {
		if raw, rerr := os.ReadFile(regFile); rerr == nil {
			var reg registration
			if jerr := json.Unmarshal(raw, &reg); jerr == nil {
				if hb2, perr := time.Parse(time.RFC3339, reg.HeartbeatAt); perr == nil && hb2.After(hb1) {
					healed = reg
					break
				}
			}
		}
		if !time.Now().Before(deadline) {
			t.Fatalf("heartbeat never rewrote %s with a newer heartbeat_at within %v (initial heartbeat_at %q): the Start/ticker wiring is broken",
				regFile, 3*time.Second, initial.HeartbeatAt)
		}
		time.Sleep(5 * time.Millisecond)
	}

	hb2, err := time.Parse(time.RFC3339, healed.HeartbeatAt)
	if err != nil {
		t.Fatalf("refreshed heartbeat_at %q is not RFC3339: %v", healed.HeartbeatAt, err)
	}
	if !hb2.After(hb1) {
		t.Fatalf("heartbeat_at not refreshed by the ticker: %v -> %v", hb1, hb2)
	}
	if healed.StartedAt != initial.StartedAt {
		t.Fatalf("started_at drifted across heartbeats: %q -> %q", initial.StartedAt, healed.StartedAt)
	}
	if healed.InstanceID != p.instanceID || healed.PID != os.Getpid() || healed.Port != 54321 ||
		healed.RepoName != "period131" || healed.RepoHash != "period131" || healed.Path != "/path/to/period131" {
		t.Fatalf("heartbeat rebuild lost fields: %+v", healed)
	}
}

// ARB-3: Stop() unlinks the registration only AFTER the heartbeat goroutine
// has exited (wg.Wait moved up), so a tick landing between the delete and the
// writer's exit can no longer resurrect a stopped daemon's registration. This
// is a lost race, not a deterministic bug, so it runs 200 independent rounds:
// fresh Portal, fresh RegistryDir, heartbeatInterval pinned to 1 ns so a
// writer is provably hot throughout the Stop window.
func TestStopRemovesRegistrationAfterHeartbeatExit(t *testing.T) {
	for round := 0; round < 200; round++ {
		tmpDir := t.TempDir()
		p := New(Config{
			PortalPort:   12360,
			InstancePort: 54321,
			Host:         "localhost",
			AuthToken:    "test-token",
			RegistryDir:  tmpDir,
			RepoName:     "stoporder131",
			RepoHash:     "stoporder131",
			WorktreePath: "/path/to/stoporder131",
		})
		// Before the goroutine starts — the only legal override point.
		p.heartbeatInterval = time.Nanosecond

		p.writeRegistration()
		regFile := filepath.Join(tmpDir, p.instanceID+".json")
		if _, err := os.Stat(regFile); err != nil {
			t.Fatalf("round %d: writeRegistration did not create %s: %v", round, regFile, err)
		}

		p.wg.Add(1)
		go p.heartbeatLoop()

		time.Sleep(100 * time.Microsecond)
		p.Stop()

		if _, err := os.Stat(regFile); !os.IsNotExist(err) {
			t.Fatalf("round %d: registration survived Stop (stat err=%v) — the unlink must follow wg.Wait()", round, err)
		}
	}
}

// ARB-4 / D3: the log line is emitted FROM the unlink's result, so a failed
// deletion is reported as a failure and never as a success. The victim is a
// NON-EMPTY DIRECTORY sitting at the .json path, which makes os.Remove fail
// deterministically (ENOTEMPTY) instead of simulating the failure.
func TestRemoveStaleRegistration_RemoveFailureLogsFailure(t *testing.T) {
	tmpDir := t.TempDir()
	victim := filepath.Join(tmpDir, "victim.json")
	if err := os.Mkdir(victim, 0o755); err != nil {
		t.Fatalf("mkdir victim: %v", err)
	}
	if err := os.WriteFile(filepath.Join(victim, "keep.txt"), []byte("not empty"), 0o600); err != nil {
		t.Fatal(err)
	}

	var logBuf bytes.Buffer
	origOutput := log.Writer()
	log.SetOutput(&logBuf)
	defer log.SetOutput(origOutput)

	// instance_id comes from the file's CONTENT ("content-id") while file=
	// must carry the actual directory entry name ("victim.json"): both stay.
	removeStaleRegistration(victim, registration{
		InstanceID: "content-id",
		PID:        4242,
		Port:       4545,
		RepoName:   "victim-repo",
	}, "pid-exited")

	if _, err := os.Stat(victim); err != nil {
		t.Fatalf("a failed removal must leave the target in place: %v", err)
	}

	out := logBuf.String()
	if !strings.Contains(out, cleanupFailureLogPrefix) {
		t.Fatalf("expected a failure log line, got:\n%s", out)
	}
	// The "no success line" check counts the success branch by its PREFIX
	// (cleanupSuccessLogPrefix) instead of the loose substring
	// "removed stale registration": a loose substring stops matching the moment
	// the success wording changes, and the assertion would then pass without ever
	// having looked at a real success line. A prefix-only anchor stays live, and
	// the failure line cannot be mistaken for it ("[portal] cleanup: failed …").
	if n := strings.Count(out, cleanupSuccessLogPrefix); n != 0 {
		t.Fatalf("os.Remove failed, so the success line must never be logged (found %d):\n%s", n, out)
	}
	for _, frag := range []string{
		"failed to remove stale registration",
		"content-id", "file=victim.json", "pid=4242", "port=4545",
		"repo=victim-repo", "reason=pid-exited",
	} {
		if !strings.Contains(out, frag) {
			t.Errorf("failure log line must contain %q:\n%s", frag, out)
		}
	}
	// The unlink error itself must be visible, not just the reason.
	if !strings.Contains(out, "reason=pid-exited): ") || !strings.Contains(out, "not empty") {
		t.Errorf("failure log line must append the unlink error after the reason:\n%s", out)
	}
	if n := strings.Count(out, "[portal] cleanup:"); n != 1 {
		t.Errorf("exactly one cleanup log line expected, got %d:\n%s", n, out)
	}
}
