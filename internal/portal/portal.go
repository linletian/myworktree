package portal

import (
	"context"
	_ "embed"
	cryptorand "crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"log"
	"math/rand"
	"net"
	"net/http"
	"net/http/httputil"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"syscall"
	"time"
)

//go:embed dashboard.html
var DashboardHTML []byte

var cspHashes = [][2]string{
	{"script", "sha256-placeholder"},
	{"style", "sha256-placeholder"},
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
	p.wg.Add(4)
	go p.claimerLoop()
	go p.csrfState.cleanupLoop(p.done, &p.wg)
	go p.cleanupRegistrationLoop()
	go p.tailscaleServeLoop()
	p.writeRegistration()
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
			p.srv.Shutdown(ctx)
			p.srv = nil
			p.ln = nil
		}
		p.mu.Unlock()
		p.stopTailscaleServe()
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

	rand.Seed(time.Now().UnixNano())
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
		p.srv = p.newServer()
		p.mu.Unlock()

		p.writePortalStatus()

		err = p.srv.Serve(ln)
		if err != nil && err != http.ErrServerClosed {
			log.Printf("[portal] HTTP serve exited unexpectedly: %v", err)
		}

		p.mu.Lock()
		p.ln = nil
		p.srv = nil
		p.mu.Unlock()

		if p.ln == nil {
			select {
			case <-time.After(10*time.Second + time.Duration(rand.Intn(5000))*time.Millisecond):
				continue
			case <-p.done:
				return
			}
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
		case <-p.done:
			return
		}
	}
}

func (p *Portal) stopTailscaleServe() {
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
	for _, h := range cspHashes {
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
	csp += "; style-src"
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
	r.URL.Host = fmt.Sprintf("127.0.0.1:%d", port)
	proxy.ServeHTTP(w, r)
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