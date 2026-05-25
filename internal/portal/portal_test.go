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
	p.handleList(listRec, listReq)
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
	p.handleList(afterLogoutRec, afterLogoutReq)
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
