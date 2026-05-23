package portal

import (
	"context"
	_ "embed"
	cryptorand "crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"math/rand"
	"net"
	"net/http"
	"net/http/httputil"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"
)

//go:generate go run gen.go

//go:embed dashboard.html
var DashboardHTML []byte

var tsStatus = func(ctx context.Context) ([]byte, error) {
	cmd := exec.CommandContext(ctx, "tailscale", "serve", "status", "--json")
	return cmd.Output()
}

var tsServeAction = func(ctx context.Context, args ...string) error {
	cmd := exec.CommandContext(ctx, "tailscale", args...)
	return cmd.Run()
}

type Config struct {
	PortalPort   int
	InstancePort int
	Host         string
	AuthToken    string
	RegistryDir  string
	DataDir      string
	RepoName     string
	RepoHash     string
}

type Portal struct {
	cfg       Config
	instanceID string
	mu        sync.Mutex
	srv       *http.Server
	ln        net.Listener
	done      chan struct{}
	wg        sync.WaitGroup
	closeOnce sync.Once
	// stoppedTailscaleServe is a one-way latch set when tailscale management
	// is permanently unavailable (binary not installed, JSON parse failures).
	// Once true, tailscale serve management is disabled for the lifetime of this instance.
	stoppedTailscaleServe bool

	csrfState *csrfState
}

type registration struct {
	InstanceID string `json:"instance_id"`
	PID        int    `json:"pid"`
	Port       int    `json:"port"`
	Host       string `json:"host"`
	RepoName   string `json:"repo_name"`
	RepoHash   string `json:"repo_hash"`
	StartedAt  string `json:"started_at"`
}

type portalStatus struct {
	InstanceID string `json:"instance_id"`
	Port       int    `json:"port"`
	UpdatedAt  string `json:"updated_at"`
}

func New(cfg Config) *Portal {
	return &Portal{
		cfg:       cfg,
		instanceID: generateInstanceID(),
		done:       make(chan struct{}),
		csrfState:  newCSRFState(),
	}
}

func generateInstanceID() string {
	pid := os.Getpid()
	timestamp := time.Now().UnixNano()
	randBytes := make([]byte, 4)
	cryptorand.Read(randBytes)
	return fmt.Sprintf("%d-%d-%s", pid, timestamp, hex.EncodeToString(randBytes))
}

func (p *Portal) Start() error {
	p.writeRegistration()
	p.wg.Add(4)
	go p.claimerLoop()
	go p.csrfState.cleanupLoop(p.done, &p.wg)
	go p.cleanupRegistrationLoop()
	go p.tailscaleServeLoop()
	return nil
}

func (p *Portal) Stop() {
	p.closeOnce.Do(func() {
		close(p.done)
		p.mu.Lock()
		wasHolder := p.ln != nil
		if p.srv != nil {
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			if err := p.srv.Shutdown(ctx); err != nil {
				log.Printf("[portal] warning: srv.Shutdown failed: %v", err)
			}
			p.srv = nil
			p.ln = nil
		}
		p.mu.Unlock()
		p.mu.Lock()
		skip := p.stoppedTailscaleServe
		p.mu.Unlock()
		if !skip {
			if err := p.stopTailscaleServe(); err != nil {
				log.Printf("[portal] warning: stopTailscaleServe failed: %v", err)
			}
		}
		p.deleteRegistration(wasHolder)
		p.wg.Wait()
	})
}

func (p *Portal) isPortalHolder() bool {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.ln != nil
}

func (p *Portal) claimerLoop() {
	defer p.wg.Done()

	initialDelay := time.Duration(rand.Intn(5000)) * time.Millisecond

	select {
	case <-time.After(initialDelay):
	case <-p.done:
		return
	}

	failCount := 0

	for {
		select {
		case <-p.done:
			return
		default:
		}

		addr := fmt.Sprintf("%s:%d", p.cfg.Host, p.cfg.PortalPort)
		ln, err := net.Listen("tcp", addr)
		if err != nil {
			if failCount == 9 {
				log.Printf("[portal] claimer: portal port %d unavailable after 10 attempts, consider --portal-port or --portal-port 0", p.cfg.PortalPort)
			}
			failCount++
			backoff := 10*time.Second + time.Duration(rand.Intn(5000))*time.Millisecond
			select {
			case <-time.After(backoff):
				continue
			case <-p.done:
				return
			}
		}

		failCount = 0

		p.mu.Lock()
		p.ln = ln
		srv := p.newServer()
		p.srv = srv
		p.mu.Unlock()

		p.writePortalStatus()
		p.cleanupStaleTailscaleServe()

		err = srv.Serve(ln)
		if err != nil && err != http.ErrServerClosed {
			log.Printf("[portal] HTTP serve exited unexpectedly: %v", err)
		}

		p.mu.Lock()
		p.ln = nil
		p.srv = nil
		p.mu.Unlock()

		select {
		case <-time.After(10*time.Second + time.Duration(rand.Intn(5000))*time.Millisecond):
			continue
		case <-p.done:
			return
		}
	}
}

func (p *Portal) newServer() *http.Server {
	mux := http.NewServeMux()
	mux.HandleFunc("/", p.handleDashboard)
	mux.HandleFunc("/api/csrf-token", p.handleCSRFToken)
	mux.HandleFunc("/api/auth", p.handleAuth)
	mux.HandleFunc("/api/list", p.handleList)
	mux.HandleFunc("/api/portal-status", p.handlePortalStatus)
	mux.HandleFunc("/api/logout", p.handleLogout)
	mux.HandleFunc("/s/", p.handleProxy)

	return &http.Server{
		Handler: mux,
	}
}

func (p *Portal) cleanupRegistrationLoop() {
	defer p.wg.Done()
	ticker := time.NewTicker(5 * time.Minute)
	defer ticker.Stop()
	for {
		select {
		case <-ticker.C:
			p.cleanupStaleRegistrations()
		case <-p.done:
			return
		}
	}
}

func (p *Portal) tailscaleServeLoop() {
	defer p.wg.Done()
	ticker := time.NewTicker(30 * time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-ticker.C:
			p.mu.Lock()
			isHolder := p.ln != nil
			p.mu.Unlock()
			if !isHolder {
				continue
			}
			p.ensureTailscaleServe()
		case <-p.done:
			return
		}
	}
}

func (p *Portal) ensureTailscaleServe() {
	status, err := p.getTailscaleServeStatus()
	if err != nil {
		return
	}

	p.repairTailscaleServe(status.currentPort)
}

func (p *Portal) repairTailscaleServe(currentPort int) {
	if currentPort == p.cfg.PortalPort {
		return
	}

	if currentPort > 0 {
		log.Printf("[portal] detaching stale tailscale serve at :443 → 127.0.0.1:%d", currentPort)
		if err := p.stopTailscaleServe(); err != nil {
			log.Printf("[portal] failed to stop stale tailscale serve: %v", err)
			return
		}
	}

	if err := p.startTailscaleServe(); err != nil {
		log.Printf("[portal] failed to start tailscale serve: %v", err)
	} else {
		log.Printf("[portal] tailscale serve started successfully on :443 → 127.0.0.1:%d", p.cfg.PortalPort)
	}
}

type tailscaleStatus struct {
	currentPort int
}

func (p *Portal) getTailscaleServeStatus() (*tailscaleStatus, error) {
	p.mu.Lock()
	if p.stoppedTailscaleServe {
		p.mu.Unlock()
		return nil, fmt.Errorf("tailscale serve management disabled")
	}
	p.mu.Unlock()

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	output, err := tsStatus(ctx)
	if err != nil {
		if errors.Is(err, exec.ErrNotFound) {
			log.Printf("[portal] tailscale not installed, skipping tailscale serve management: %v", err)
			p.mu.Lock()
			p.stoppedTailscaleServe = true
			p.mu.Unlock()
		} else if _, ok := err.(*exec.ExitError); ok {
			log.Printf("[portal] tailscale serve status returned non-zero (likely not configured): %v", err)
			return &tailscaleStatus{currentPort: 0}, nil
		} else {
			log.Printf("[portal] tailscale serve status check failed: %v", err)
		}
		return nil, fmt.Errorf("tailscale serve status check failed: %w", err)
	}

	var status struct {
		TCP map[string]string `json:"TCP"`
	}
	if err := json.Unmarshal(output, &status); err != nil {
		log.Printf("[portal] failed to parse tailscale serve status: %v", err)
		p.mu.Lock()
		p.stoppedTailscaleServe = true
		p.mu.Unlock()
		return nil, fmt.Errorf("failed to parse tailscale serve status: %w", err)
	}

	if target, ok := status.TCP[":443"]; ok {
		parts := strings.Split(target, ":")
		if len(parts) >= 2 {
			port, err := strconv.Atoi(parts[len(parts)-1])
			if err != nil {
				log.Printf("[portal] failed to parse tailscale serve port: %v", err)
				p.mu.Lock()
				p.stoppedTailscaleServe = true
				p.mu.Unlock()
				return nil, fmt.Errorf("failed to parse tailscale serve port: %w", err)
			}
			return &tailscaleStatus{currentPort: port}, nil
		}
	}

	return &tailscaleStatus{currentPort: 0}, nil
}

func (p *Portal) startTailscaleServe() error {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	return tsServeAction(ctx, "serve", "--bg", fmt.Sprintf("http://127.0.0.1:%d", p.cfg.PortalPort))
}

func (p *Portal) stopTailscaleServe() error {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	return tsServeAction(ctx, "serve", "stop")
}

func (p *Portal) cleanupStaleTailscaleServe() {
	p.ensureTailscaleServe()
}

func (p *Portal) cleanupStaleRegistrations() {
	if p.cfg.RegistryDir == "" {
		return
	}

	entries, err := os.ReadDir(p.cfg.RegistryDir)
	if err != nil {
		return
	}

	for _, entry := range entries {
		if entry.IsDir() || entry.Name() == "portal.json" {
			continue
		}
		if filepath.Ext(entry.Name()) != ".json" {
			continue
		}

		data, err := os.ReadFile(filepath.Join(p.cfg.RegistryDir, entry.Name()))
		if err != nil {
			continue
		}
		var reg registration
		if json.Unmarshal(data, &reg) != nil {
			continue
		}

		if reg.InstanceID == p.instanceID {
			continue
		}

		if !isRegistrationAlive(reg) {
			os.Remove(filepath.Join(p.cfg.RegistryDir, entry.Name()))
		}
	}
}

func isRegistrationAlive(reg registration) bool {
	if reg.PID <= 0 {
		return false
	}
	proc, err := os.FindProcess(reg.PID)
	if err != nil {
		return false
	}
	if proc.Signal(syscall.Signal(0)) != nil {
		return false
	}
	if reg.Port <= 0 {
		return true
	}
	conn, err := net.DialTimeout("tcp", fmt.Sprintf("127.0.0.1:%d", reg.Port), 1*time.Second)
	if err != nil {
		return false
	}
	conn.Close()
	return true
}

func (p *Portal) writeRegistration() {
	if p.cfg.RegistryDir == "" || p.cfg.PortalPort == 0 {
		return
	}

	os.MkdirAll(p.cfg.RegistryDir, 0o755)

	reg := registration{
		InstanceID: p.instanceID,
		PID:        os.Getpid(),
		Port:       p.cfg.InstancePort,
		Host:       p.cfg.Host,
		RepoName:   p.cfg.RepoName,
		RepoHash:   p.cfg.RepoHash,
		StartedAt:  time.Now().Format(time.RFC3339),
	}

	data, _ := json.Marshal(reg)
	tmp := filepath.Join(p.cfg.RegistryDir, p.instanceID+".json.tmp")
	os.WriteFile(tmp, data, 0o600)
	os.Rename(tmp, filepath.Join(p.cfg.RegistryDir, p.instanceID+".json"))
}

func (p *Portal) deleteRegistration(wasHolder bool) {
	if p.cfg.RegistryDir == "" {
		return
	}

	os.Remove(filepath.Join(p.cfg.RegistryDir, p.instanceID+".json"))

	if wasHolder {
		os.Remove(filepath.Join(p.cfg.RegistryDir, "portal.json"))
	}
}

func (p *Portal) writePortalStatus() {
	if p.cfg.RegistryDir == "" {
		return
	}

	status := portalStatus{
		InstanceID: p.instanceID,
		Port:       p.cfg.PortalPort,
		UpdatedAt:  time.Now().Format(time.RFC3339),
	}

	data, _ := json.Marshal(status)
	portalPath := filepath.Join(p.cfg.RegistryDir, "portal.json")
	tmp := portalPath + ".tmp"
	os.WriteFile(tmp, data, 0o600)
	os.Rename(tmp, portalPath)
}

func (p *Portal) handleDashboard(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path != "/" {
		http.NotFound(w, r)
		return
	}

	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("X-Content-Type-Options", "nosniff")

	var scriptHashes []string
	var styleHashes []string
	for _, h := range CSPHashes {
		if h[0] == "script" {
			scriptHashes = append(scriptHashes, h[1])
		} else if h[0] == "style" {
			styleHashes = append(styleHashes, h[1])
		}
	}

	csp := "default-src 'self'; script-src"
	for _, h := range scriptHashes {
		csp += " " + h
	}
	csp += "; style-src 'self'"
	for _, h := range styleHashes {
		csp += " " + h
	}
	w.Header().Set("Content-Security-Policy", csp)

	w.Write(DashboardHTML)
}

func (p *Portal) handleCSRFToken(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")

	if r.Method != "GET" {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	ip := getIP(r)
	if !p.csrfState.allowCSRFRequest(ip) {
		http.Error(w, "Rate limit exceeded", http.StatusTooManyRequests)
		return
	}

	token := p.csrfState.generate()
	if token == "" {
		http.Error(w, "Service unavailable", http.StatusServiceUnavailable)
		return
	}
	cookie := &http.Cookie{
		Name:     "mw_csrf",
		Value:    token,
		Path:     "/",
		SameSite: http.SameSiteStrictMode,
		HttpOnly: false,
	}
	if r.TLS != nil || r.Header.Get("X-Forwarded-Proto") == "https" {
		cookie.Secure = true
	}
	http.SetCookie(w, cookie)

	json.NewEncoder(w).Encode(map[string]string{"csrf_token": token})
}

func (p *Portal) handleAuth(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")

	if r.Method != "POST" {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	if p.cfg.AuthToken == "" {
		w.WriteHeader(http.StatusBadRequest)
		json.NewEncoder(w).Encode(map[string]string{"error": "auth token not configured on server"})
		return
	}

	ip := getIP(r)
	if !p.csrfState.allowAuthAttempt(ip) {
		http.Error(w, "Rate limit exceeded", http.StatusTooManyRequests)
		return
	}

	var req struct {
		Token     string `json:"token"`
		CSRFToken string `json:"csrf_token"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "Invalid request", http.StatusBadRequest)
		return
	}

	csrfCookie, err := r.Cookie("mw_csrf")
	if err != nil {
		http.Error(w, "CSRF cookie missing", http.StatusForbidden)
		return
	}

	if !p.csrfState.verifyAndConsume(csrfCookie.Value, req.CSRFToken) {
		http.Error(w, "CSRF verification failed", http.StatusForbidden)
		return
	}

	if req.Token != p.cfg.AuthToken {
		http.Error(w, "Invalid token", http.StatusUnauthorized)
		return
	}

	cookie := &http.Cookie{
		Name:     "mw_token",
		Value:    p.cfg.AuthToken,
		Path:     "/",
		MaxAge:   86400,
		SameSite: http.SameSiteLaxMode,
		HttpOnly: true,
	}
	if r.TLS != nil || r.Header.Get("X-Forwarded-Proto") == "https" {
		cookie.Secure = true
	}
	http.SetCookie(w, cookie)

	json.NewEncoder(w).Encode(map[string]string{"status": "ok"})
}

func (p *Portal) handleList(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")

	if !p.checkAuth(r) {
		http.Error(w, "Unauthorized", http.StatusUnauthorized)
		return
	}

	p.refreshAuthCookie(w, r)

	processes := []map[string]interface{}{}
	if p.cfg.RegistryDir != "" {
		entries, _ := os.ReadDir(p.cfg.RegistryDir)
		for _, entry := range entries {
			if entry.IsDir() || entry.Name() == "portal.json" {
				continue
			}
			if filepath.Ext(entry.Name()) != ".json" {
				continue
			}
			data, err := os.ReadFile(filepath.Join(p.cfg.RegistryDir, entry.Name()))
			if err != nil {
				continue
			}
			var reg registration
			if json.Unmarshal(data, &reg) != nil {
				continue
			}
			alive := isProcessAlive(reg.PID, reg.Port)
			processes = append(processes, map[string]interface{}{
				"instance_id": reg.InstanceID,
				"pid":         reg.PID,
				"port":        reg.Port,
				"repo_name":   reg.RepoName,
				"repo_hash":   reg.RepoHash,
				"started_at":  reg.StartedAt,
				"alive":       alive,
			})
		}
	}

	sort.Slice(processes, func(i, j int) bool {
		return processes[i]["repo_name"].(string) < processes[j]["repo_name"].(string)
	})

	json.NewEncoder(w).Encode(map[string]interface{}{
		"is_portal":  p.isPortalHolder(),
		"portal_port": p.cfg.PortalPort,
		"processes":  processes,
	})
}

func (p *Portal) handlePortalStatus(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	p.mu.Lock()
	isHolder := p.ln != nil
	p.mu.Unlock()

	json.NewEncoder(w).Encode(map[string]bool{"is_portal": isHolder})
}

func (p *Portal) handleLogout(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")

	if r.Method != "POST" {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	var req struct {
		CSRFToken string `json:"csrf_token"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "Invalid request", http.StatusBadRequest)
		return
	}

	csrfCookie, err := r.Cookie("mw_csrf")
	if err != nil || !p.csrfState.verifyAndConsume(csrfCookie.Value, req.CSRFToken) {
		http.Error(w, "CSRF verification failed", http.StatusForbidden)
		return
	}

	cookie := &http.Cookie{
		Name:     "mw_token",
		Value:    "",
		Path:     "/",
		MaxAge:   0,
		SameSite: http.SameSiteLaxMode,
		HttpOnly: true,
	}
	if r.TLS != nil || r.Header.Get("X-Forwarded-Proto") == "https" {
		cookie.Secure = true
	}
	http.SetCookie(w, cookie)

	json.NewEncoder(w).Encode(map[string]string{"status": "ok"})
}

func (p *Portal) handleProxy(w http.ResponseWriter, r *http.Request) {
	if !p.checkAuth(r) {
		http.Error(w, "Unauthorized", http.StatusUnauthorized)
		return
	}

	p.refreshAuthCookie(w, r)

	repoHash := parseRepoHash(r.URL.Path)
	if repoHash == "" {
		http.Error(w, "Invalid repo hash", http.StatusBadRequest)
		return
	}

	port := p.findInstancePort(repoHash)
	if port == 0 {
		http.Error(w, "Instance not found", http.StatusBadGateway)
		return
	}

	proxy := httputil.NewSingleHostReverseProxy(&url.URL{Scheme: "http", Host: fmt.Sprintf("127.0.0.1:%d", port)})
	originalPath := r.URL.Path
	proxy.Director = func(req *http.Request) {
		req.URL.Scheme = "http"
		req.URL.Host = fmt.Sprintf("127.0.0.1:%d", port)
		strippedPath := strings.TrimPrefix(originalPath, "/s/"+repoHash)
		if !strings.HasPrefix(strippedPath, "/") {
			strippedPath = "/" + strippedPath
		}
		req.URL.Path = strippedPath
		req.URL.RawPath = ""
	}
	proxy.ErrorLog = log.New(&logWriter{repoHash: repoHash, port: port}, "", 0)

	proxy.ServeHTTP(w, r)
}

type logWriter struct {
	repoHash string
	port     int
}

func (l *logWriter) Write(p []byte) (int, error) {
	log.Printf("[portal] reverse proxy: dial 127.0.0.1:%d (repo_hash=%q) failed: %s", l.port, l.repoHash, strings.TrimSpace(string(p)))
	return len(p), nil
}

func (p *Portal) checkAuth(r *http.Request) bool {
	token := extractToken(r)
	if token == "" {
		return false
	}
	return token == p.cfg.AuthToken
}

func (p *Portal) refreshAuthCookie(w http.ResponseWriter, r *http.Request) {
	cookie := &http.Cookie{
		Name:     "mw_token",
		Value:    p.cfg.AuthToken,
		Path:     "/",
		MaxAge:   86400,
		SameSite: http.SameSiteLaxMode,
		HttpOnly: true,
	}
	if r.TLS != nil || r.Header.Get("X-Forwarded-Proto") == "https" {
		cookie.Secure = true
	}
	http.SetCookie(w, cookie)
}

func extractToken(r *http.Request) string {
	if auth := r.Header.Get("Authorization"); strings.HasPrefix(auth, "Bearer ") {
		return auth[7:]
	}
	if token := r.URL.Query().Get("token"); token != "" {
		return token
	}
	if cookie, err := r.Cookie("mw_token"); err == nil {
		return cookie.Value
	}
	return ""
}

func parseRepoHash(path string) string {
	parts := strings.Split(strings.TrimPrefix(path, "/s/"), "/")
	if len(parts) == 0 {
		return ""
	}
	hash := parts[0]
	for _, c := range hash {
		if !((c >= 'a' && c <= 'f') || (c >= '0' && c <= '9')) {
			return ""
		}
	}
	return hash
}

func (p *Portal) findInstancePort(repoHash string) int {
	if p.cfg.RegistryDir == "" {
		return 0
	}
	entries, _ := os.ReadDir(p.cfg.RegistryDir)
	for _, entry := range entries {
		if entry.IsDir() || entry.Name() == "portal.json" {
			continue
		}
		if filepath.Ext(entry.Name()) != ".json" {
			continue
		}
		data, err := os.ReadFile(filepath.Join(p.cfg.RegistryDir, entry.Name()))
		if err != nil {
			continue
		}
		var reg registration
		if json.Unmarshal(data, &reg) != nil {
			continue
		}
		if reg.RepoHash == repoHash {
			return reg.Port
		}
	}
	return 0
}

func getIP(r *http.Request) string {
	if xff := r.Header.Get("X-Forwarded-For"); xff != "" {
		if idx := strings.Index(xff, ","); idx != -1 {
			return strings.TrimSpace(xff[:idx])
		}
		if idx := strings.Index(xff, " "); idx != -1 {
			return xff[:idx]
		}
		return xff
	}
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return r.RemoteAddr
	}
	return host
}

func isProcessAlive(pid int, port int) bool {
	if pid <= 0 {
		return false
	}
	proc, err := os.FindProcess(pid)
	if err != nil {
		return false
	}
	if proc.Signal(syscall.Signal(0)) != nil {
		return false
	}
	if port <= 0 {
		return true
	}
	conn, err := net.DialTimeout("tcp", fmt.Sprintf("127.0.0.1:%d", port), 1*time.Second)
	if err != nil {
		return false
	}
	conn.Close()
	return true
}

type csrfState struct {
	mu         sync.Mutex
	pending    map[string]time.Time
	used       map[string]time.Time
	rateLimits map[string]time.Time
	authLimits map[string][]time.Time
}

func newCSRFState() *csrfState {
	return &csrfState{
		pending:    make(map[string]time.Time),
		used:       make(map[string]time.Time),
		rateLimits: make(map[string]time.Time),
		authLimits: make(map[string][]time.Time),
	}
}

func (s *csrfState) generate() string {
	s.mu.Lock()
	if len(s.used) >= 10000 {
		s.mu.Unlock()
		return ""
	}
	s.mu.Unlock()

	b := make([]byte, 32)
	cryptorand.Read(b)
	token := hex.EncodeToString(b)

	s.mu.Lock()
	s.pending[token] = time.Now()
	s.mu.Unlock()
	return token
}

func (s *csrfState) verifyAndConsume(cookieValue, bodyValue string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()

	if cookieValue != bodyValue {
		return false
	}

	if createdAt, exists := s.pending[cookieValue]; exists {
		if time.Since(createdAt) > 5*time.Minute {
			delete(s.pending, cookieValue)
			return false
		}
		delete(s.pending, cookieValue)
	} else if usedTime, exists := s.used[cookieValue]; exists {
		if time.Since(usedTime) <= 5*time.Minute {
			return false
		}
		delete(s.used, cookieValue)
	} else {
		return false
	}

	s.used[cookieValue] = time.Now()
	return true
}

func (s *csrfState) allowCSRFRequest(ip string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()

	if len(s.rateLimits) >= 10000 {
		return false
	}

	last, exists := s.rateLimits[ip]
	if exists && time.Since(last) < time.Second {
		return false
	}
	s.rateLimits[ip] = time.Now()
	return true
}

func (s *csrfState) cleanupLoop(done chan struct{}, wg *sync.WaitGroup) {
	defer wg.Done()
	ticker := time.NewTicker(30 * time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-ticker.C:
			s.cleanup()
		case <-done:
			return
		}
	}
}

func (s *csrfState) cleanup() {
	s.mu.Lock()
	defer s.mu.Unlock()

	cutoff := time.Now().Add(-5 * time.Minute)
	for k, v := range s.pending {
		if v.Before(cutoff) {
			delete(s.pending, k)
		}
	}
	for k, v := range s.used {
		if v.Before(cutoff) {
			delete(s.used, k)
		}
	}
	for k, v := range s.rateLimits {
		if v.Before(cutoff) {
			delete(s.rateLimits, k)
		}
	}
	for k, times := range s.authLimits {
		var remaining []time.Time
		for _, t := range times {
			if t.After(cutoff) {
				remaining = append(remaining, t)
			}
		}
		if len(remaining) == 0 {
			delete(s.authLimits, k)
		} else {
			s.authLimits[k] = remaining
		}
	}
}

func (s *csrfState) allowAuthAttempt(ip string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()

	if len(s.authLimits) >= 10000 {
		return false
	}

	cutoff := time.Now().Add(-1 * time.Minute)
	var newTimes []time.Time
	for _, t := range s.authLimits[ip] {
		if t.After(cutoff) {
			newTimes = append(newTimes, t)
		}
	}
	if len(newTimes) >= 20 {
		return false
	}
	newTimes = append(newTimes, time.Now())
	s.authLimits[ip] = newTimes
	return true
}

var tsDNSNameCmd = func(ctx context.Context) (string, error) {
	cmd := exec.CommandContext(ctx, "tailscale", "status", "--json")
	out, err := cmd.Output()
	if err != nil {
		return "", err
	}
	var status struct {
		Self struct {
			DNSName string `json:"DNSName"`
		} `json:"Self"`
	}
	if err := json.Unmarshal(out, &status); err != nil {
		return "", err
	}
	if status.Self.DNSName == "" {
		return "", fmt.Errorf("no DNS name")
	}
	return strings.TrimSuffix(status.Self.DNSName, "."), nil
}

func TailscaleDNSName() string {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	name, err := tsDNSNameCmd(ctx)
	if err != nil {
		return ""
	}
	return name
}