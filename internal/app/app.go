package app

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"html"
	"io"
	"log"
	"net"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"runtime/debug"
	"sort"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"

	"myworktree/internal/config"
	"myworktree/internal/gitx"
	"myworktree/internal/instance"
	"myworktree/internal/llm"
	"myworktree/internal/mcp"
	"myworktree/internal/monitor"
	"myworktree/internal/portal"
	"myworktree/internal/store"
	"myworktree/internal/tag"
	"myworktree/internal/ui"
	"myworktree/internal/worktree"
	"myworktree/internal/ws"
)

type Config struct {
	ListenAddr   string
	AuthToken    string
	TLSCert      string
	TLSKey       string
	Open         bool
	WorktreesDir string
	PortalPort   int
}

type serverConfig struct {
	ListenPort int    `json:"listen_port"`
	InstanceID string `json:"instance_id,omitempty"`
}

type Server struct {
	cfg       Config
	logger    *log.Logger
	ln        net.Listener
	mux       *http.ServeMux
	root      string
	dataDir   string
	serverRev string
	isSecure  bool

	portal      *portal.Portal
	httpSrv     *http.Server
	store       store.FileStore
	worktreeMgr worktree.Manager
	instanceMgr *instance.Manager
	mcpAdapter  mcp.Adapter
	monitor     monitor.Collector
	authMu      sync.Mutex
	authFails   map[string]authFail
	ttyMu       sync.Mutex
	ttyClients  map[string]map[string]*ttyClientState
	ttyApplied  map[string]ttySize
	ttyNextID   uint64

	gitRunner func(timeout time.Duration, gitRoot string, args ...string) ([]byte, error)
}

type authFail struct {
	Count     int
	WindowEnd time.Time
}

type ttySize struct {
	Cols int
	Rows int
}

type ttyClientState struct {
	size    ttySize
	hasSize bool
	updates chan ttySize
}

const maxTTYClientsPerInstance = 8

type ttyClientHandle struct {
	server     *Server
	instanceID string
	clientID   string
	once       sync.Once
}

var errInvalidRepoListenPort = errors.New("invalid persisted listen_port")
var errWorktreeNotFound = errors.New("worktree not found")

func New(cfg Config, logger *log.Logger) (*Server, error) {
	if logger == nil {
		return nil, errors.New("logger is required")
	}
	root, err := gitx.GitRoot(".")
	if err != nil {
		return nil, err
	}
	dataDir, err := userProjectDataDir(root)
	if err != nil {
		return nil, err
	}
	st := store.FileStore{Path: filepath.Join(dataDir, "state.json")}
	worktreeMgr := worktree.Manager{
		GitRoot:      root,
		DataDir:      dataDir,
		WorktreesDir: cfg.WorktreesDir,
		Store:        st,
	}
	instanceMgr := &instance.Manager{
		DataDir: dataDir,
		Root:    root,
		Store:   st,
		Logger:  logger,
	}

	mux := http.NewServeMux()
	isSecure := cfg.TLSCert != "" && cfg.TLSKey != ""
	s := &Server{
		cfg:         cfg,
		logger:      logger,
		mux:         mux,
		root:        root,
		dataDir:     dataDir,
		serverRev:   computeServerRevision(),
		store:       st,
		worktreeMgr: worktreeMgr,
		instanceMgr: instanceMgr,
		mcpAdapter: mcp.Adapter{
			Worktrees: worktreeMgr,
			Instances: instanceMgr,
		},
		authFails: map[string]authFail{},
		isSecure:  isSecure,
	}
	s.registerAPIs(mux)
	if err := ui.Register(mux, filepath.Base(filepath.Clean(s.root)), func(r *http.Request) bool {
		return !isLoopbackRequest(r)
	}); err != nil {
		return nil, fmt.Errorf("ui.Register: %w", err)
	}

	s.gitRunner = func(timeout time.Duration, gitRoot string, args ...string) ([]byte, error) {
		cmd := gitx.GitCommand(timeout, gitRoot, args...)
		return cmd.Output()
	}

	return s, nil
}

// registerTTYClient tracks one frontend terminal viewer for an instance and returns
// a per-connection update stream with the server-applied shared size.
func (s *Server) registerTTYClient(instanceID string) (string, <-chan ttySize, error) {
	s.ttyMu.Lock()
	defer s.ttyMu.Unlock()
	if s.ttyClients == nil {
		s.ttyClients = map[string]map[string]*ttyClientState{}
	}
	if s.ttyApplied == nil {
		s.ttyApplied = map[string]ttySize{}
	}
	s.ttyNextID++
	clientID := strconv.FormatUint(s.ttyNextID, 10)
	updates := make(chan ttySize, 1)
	if s.ttyClients[instanceID] == nil {
		s.ttyClients[instanceID] = map[string]*ttyClientState{}
	}
	if len(s.ttyClients[instanceID]) >= maxTTYClientsPerInstance {
		return "", nil, fmt.Errorf("too many tty clients for instance: %s", instanceID)
	}
	s.ttyClients[instanceID][clientID] = &ttyClientState{updates: updates}
	if size, ok := s.ttyApplied[instanceID]; ok {
		updates <- size
	}
	return clientID, updates, nil
}

func (s *Server) newTTYClientHandle(instanceID string) (*ttyClientHandle, <-chan ttySize, error) {
	clientID, updates, err := s.registerTTYClient(instanceID)
	if err != nil {
		return nil, nil, err
	}
	return &ttyClientHandle{
		server:     s,
		instanceID: instanceID,
		clientID:   clientID,
	}, updates, nil
}

func (h *ttyClientHandle) Close() {
	if h == nil || h.server == nil {
		return
	}
	h.once.Do(func() {
		h.server.unregisterTTYClient(h.instanceID, h.clientID)
	})
}

// unregisterTTYClient removes a frontend terminal viewer and recomputes the shared
// instance size so the remaining viewers stay in sync.
func (s *Server) unregisterTTYClient(instanceID, clientID string) {
	s.ttyMu.Lock()
	var removed chan ttySize
	if clients := s.ttyClients[instanceID]; clients != nil {
		if state := clients[clientID]; state != nil {
			removed = state.updates
			delete(clients, clientID)
		}
		if len(clients) == 0 {
			delete(s.ttyClients, instanceID)
		}
	}
	applied, changed, notify := s.recomputeTTYSizeLocked(instanceID)
	s.ttyMu.Unlock()

	if removed != nil {
		close(removed)
	}
	s.applyTTYSize(instanceID, applied, changed, notify)
}

// updateTTYClientSize records one viewer's proposed terminal size. The shared size
// is the smallest width and smallest height across all connected viewers.
func (s *Server) updateTTYClientSize(instanceID, clientID string, cols, rows int) {
	s.ttyMu.Lock()
	if s.ttyClients == nil || s.ttyClients[instanceID] == nil || s.ttyClients[instanceID][clientID] == nil {
		s.ttyMu.Unlock()
		return
	}
	s.ttyClients[instanceID][clientID].size = ttySize{Cols: cols, Rows: rows}
	s.ttyClients[instanceID][clientID].hasSize = true
	applied, changed, notify := s.recomputeTTYSizeLocked(instanceID)
	s.ttyMu.Unlock()

	s.applyTTYSize(instanceID, applied, changed, notify)
}

func (s *Server) recomputeTTYSizeLocked(instanceID string) (ttySize, bool, []chan ttySize) {
	old, hadOld := s.ttyApplied[instanceID]
	clients := s.ttyClients[instanceID]
	var min ttySize
	hasMin := false
	notify := make([]chan ttySize, 0, len(clients))
	for _, client := range clients {
		notify = append(notify, client.updates)
		if !client.hasSize {
			continue
		}
		if !hasMin {
			min = client.size
			hasMin = true
			continue
		}
		if client.size.Cols < min.Cols {
			min.Cols = client.size.Cols
		}
		if client.size.Rows < min.Rows {
			min.Rows = client.size.Rows
		}
	}
	if !hasMin {
		delete(s.ttyApplied, instanceID)
		return ttySize{}, hadOld, nil
	}
	if hadOld && old == min {
		return min, false, nil
	}
	s.ttyApplied[instanceID] = min
	return min, true, notify
}

func (s *Server) applyTTYSize(instanceID string, size ttySize, changed bool, notify []chan ttySize) {
	if !changed {
		return
	}
	if s.instanceMgr != nil && size.Cols > 0 && size.Rows > 0 {
		if err := s.instanceMgr.Resize(instanceID, size.Cols, size.Rows); err != nil {
			if s.logger != nil {
				s.logger.Printf("tty resize apply failed for %s: %v", instanceID, err)
			}
		}
	}
	for _, ch := range notify {
		if ch == nil {
			continue
		}
		select {
		case ch <- size:
		default:
			select {
			case <-ch:
			default:
			}
			select {
			case ch <- size:
			default:
			}
		}
	}
}

func (s *Server) Start() (string, error) {
	if err := s.validateSecurity(); err != nil {
		return "", err
	}
	if err := os.MkdirAll(s.dataDir, 0o755); err != nil {
		return "", err
	}
	if n, err := s.instanceMgr.ReconcileRunningOnStartup(); err != nil {
		s.logger.Printf("reconcile running instances failed: %v", err)
	} else if n > 0 {
		s.logger.Printf("reconciled %d stale running instances to stopped", n)
	}

	listenAddr, err := resolveRepoListenAddr(s.cfg.ListenAddr, s.dataDir, s.logger)
	if err != nil {
		return "", err
	}
	s.cfg.ListenAddr = listenAddr
	ln, err := net.Listen("tcp", s.cfg.ListenAddr)
	if err != nil {
		if errors.Is(err, syscall.EADDRINUSE) {
			if altAddr, altErr := allocateRepoListenAddr(s.cfg.ListenAddr, s.dataDir); altErr == nil {
				ln, err = net.Listen("tcp", altAddr)
				if err == nil {
					s.cfg.ListenAddr = altAddr
				}
			}
		}
	}
	if err != nil {
		return "", err
	}
	s.ln = ln

	if s.cfg.PortalPort > 0 {
		base, err := os.UserConfigDir()
		if err != nil {
			s.logger.Printf("[portal] warning: UserConfigDir failed: %v", err)
			base = ""
		}
		instancePort := 0
		if tcpAddr, ok := s.ln.Addr().(*net.TCPAddr); ok {
			instancePort = tcpAddr.Port
		}
		cfg := portal.Config{
			PortalPort:   s.cfg.PortalPort,
			InstancePort: instancePort,
			Host:         "0.0.0.0",
			AuthToken:    s.cfg.AuthToken,
			RegistryDir:  filepath.Join(base, "myworktree", "portal"),
			DataDir:      s.dataDir,
			RepoName:     filepath.Base(filepath.Clean(s.root)),
			RepoHash:     gitx.HashPath(s.root),
			WorktreePath: s.root,
		}
		s.portal = portal.New(cfg)
		if err := s.portal.Start(); err != nil {
			s.logger.Printf("[portal] warning: portal.Start failed: %v", err)
			s.portal = nil
		} else {
			s.logger.Printf("[portal] Portal dashboard at: http://0.0.0.0:%d/", s.cfg.PortalPort)
			if tsName := portal.TailscaleDNSName(); tsName != "" {
				s.logger.Printf("[portal] Tailscale URL: https://%s/", tsName)
			}
		}
	}

	h := s.withServerRevision(s.withAuth(s.mux))
	s.httpSrv = &http.Server{Handler: h}

	go func() {
		if s.cfg.TLSCert != "" || s.cfg.TLSKey != "" {
			_ = s.httpSrv.ServeTLS(ln, s.cfg.TLSCert, s.cfg.TLSKey)
			return
		}
		_ = s.httpSrv.Serve(ln)
	}()

	scheme := "http"
	if s.cfg.TLSCert != "" && s.cfg.TLSKey != "" {
		scheme = "https"
	}
	// IPv6 is explicitly disabled. Always use IPv4 127.0.0.1 regardless of what
	// the OS reports for the listening socket (macOS may report [::]:port).
	addr := "127.0.0.1"
	if tcpAddr, ok := ln.Addr().(*net.TCPAddr); ok {
		addr = net.JoinHostPort("127.0.0.1", strconv.Itoa(tcpAddr.Port))
	}
	url := fmt.Sprintf("%s://%s/", scheme, addr)
	if s.cfg.Open && s.cfg.PortalPort > 0 {
		if err := waitForServer(s.cfg.PortalPort, 5*time.Second); err != nil {
			s.logger.Printf("server not ready: %v", err)
			s.logger.Printf("please manually open http://127.0.0.1:%d/", s.cfg.PortalPort)
		} else {
			if openErr := OpenURL(fmt.Sprintf("http://127.0.0.1:%d/", s.cfg.PortalPort)); openErr != nil {
				s.logger.Printf("open browser failed: %v", openErr)
			}
		}
	}
	return url, nil
}

func waitForServer(port int, timeout time.Duration) error {
	addr := net.JoinHostPort("127.0.0.1", strconv.Itoa(port))
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		conn, err := net.DialTimeout("tcp", addr, 100*time.Millisecond)
		if err == nil {
			conn.Close()
			return nil
		}
		time.Sleep(50 * time.Millisecond)
	}
	return fmt.Errorf("server not ready after %v (port %d)", timeout, port)
}

func (s *Server) Shutdown() {
	if s.portal != nil {
		s.portal.Stop()
	}
	if s.httpSrv != nil {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = s.httpSrv.Shutdown(ctx)
	}
	if s.ln != nil {
		s.ln.Close()
	}
}

func (s *Server) listTopBranches() (string, []gitx.Branch, error) {
	def := gitx.DefaultBranch(s.root)
	items, err := gitx.ListLocalBranchesByCommitTime(s.root, 50)
	if err != nil {
		return "", nil, err
	}

	// Build a quick existence lookup for branch names
	exists := make(map[string]bool, len(items))
	for _, b := range items {
		exists[b.Name] = true
	}

	// Priority order: default > main > develop; skip if same as default
	priorities := make([]string, 0, 3)
	priorities = append(priorities, def)
	if def != "main" && exists["main"] {
		priorities = append(priorities, "main")
	}
	if def != "develop" && exists["develop"] {
		priorities = append(priorities, "develop")
	}

	// Build result: priority branches first, then the rest by commit time, total 10.
	seen := make(map[string]bool, 10)
	out := make([]gitx.Branch, 0, 10)
	for _, name := range priorities {
		if seen[name] {
			continue
		}
		for _, b := range items {
			if b.Name == name {
				out = append(out, b)
				seen[name] = true
				break
			}
		}
	}
	for _, b := range items {
		if len(out) >= 10 {
			break
		}
		if seen[b.Name] {
			continue
		}
		out = append(out, b)
		seen[b.Name] = true
	}
	return def, out, nil
}

func OpenURL(url string) error {
	cmd := exec.Command("open", url)
	return cmd.Start()
}

func (s *Server) validateSecurity() error {
	host, _, err := net.SplitHostPort(s.cfg.ListenAddr)
	if err != nil {
		// If user passes ":0" etc, net.Listen will still accept, but SplitHostPort fails.
		// We'll validate after Listen in later iteration.
		return nil
	}
	if !isLoopbackHost(host) && strings.TrimSpace(s.cfg.AuthToken) == "" {
		return errors.New("--auth is required when listening on a non-loopback address")
	}
	if (s.cfg.TLSCert == "") != (s.cfg.TLSKey == "") {
		return errors.New("--tls-cert and --tls-key must be provided together")
	}
	return nil
}

func (s *Server) registerAPIs(mux *http.ServeMux) {
	mux.HandleFunc("/api/worktrees", s.handleWorktrees)
	mux.HandleFunc("/api/worktrees/unmanaged", s.handleWorktreesUnmanaged)
	mux.HandleFunc("/api/worktrees/import", s.handleWorktreeImport)
	mux.HandleFunc("/api/worktrees/delete", s.handleWorktreeDelete)
	mux.HandleFunc("/api/worktree/status", s.handleWorktreeStatus)
	mux.HandleFunc("/api/instances", s.handleInstances)
	mux.HandleFunc("/api/instances/reorder", s.handleInstanceReorder)
	mux.HandleFunc("/api/instances/stop", s.handleInstanceStop)
	mux.HandleFunc("/api/instances/restart", s.handleInstanceRestart)
	mux.HandleFunc("/api/instances/delete", s.handleInstanceDelete)
	mux.HandleFunc("/api/instances/input", s.handleInstanceInput)
	mux.HandleFunc("/api/instances/tty/ws", s.handleInstanceTTYWS)
	mux.HandleFunc("/api/instances/log", s.handleInstanceLog)
	mux.HandleFunc("/api/instances/log/stream", s.handleInstanceLogStream)
	mux.HandleFunc("/api/instances/stats", s.handleInstanceStats)
	mux.HandleFunc("/api/tags", s.handleTags)
	mux.HandleFunc("/api/tags/open-dir", s.handleTagsOpenDir)
	mux.HandleFunc("/api/branches", s.handleBranches)
	mux.HandleFunc("/api/worktrees/open-terminal", s.handleWorktreeOpenTerminal)
	mux.HandleFunc("/api/worktrees/open-finder", s.handleWorktreeOpenFinder)
	mux.HandleFunc("/api/worktrees/diverged", s.handleWorktreesDiverged)
	mux.HandleFunc("/api/worktree/diverged", s.handleWorktreeDiverged)
	mux.HandleFunc("/api/mcp/tools", s.handleMCPTools)
	mux.HandleFunc("/api/mcp/call", s.handleMCPCall)
	mux.HandleFunc("/api/main", s.handleMain)
	mux.HandleFunc("/api/llm/config", s.handleLLMConfig)
	mux.HandleFunc("/api/llm/test", s.handleLLMTest)
	mux.HandleFunc("/api/llm/generate", s.handleLLMGenerate)
	mux.HandleFunc("/login", s.handleLogin)
}

func (s *Server) handleBranches(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	def, out, err := s.listTopBranches()
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"default":  def,
		"branches": out,
	})
}

func (s *Server) handleMain(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	name := filepath.Base(filepath.Clean(s.root))
	branch, _ := gitx.CurrentBranch(s.root) // returns empty string on detached HEAD
	writeJSON(w, http.StatusOK, map[string]any{
		"name":   name,
		"branch": branch,
	})
}

func (s *Server) handleLogin(w http.ResponseWriter, r *http.Request) {
	if r.Method == http.MethodGet {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		token := extractAuthToken(r)
		if token != "" {
			cfg, _ := config.Load()
			if cfg != nil && token == cfg.AuthToken {
				http.Redirect(w, r, "/", http.StatusFound)
				return
			}
		}
		loginHTML := `<!DOCTYPE html>
<html lang="en">
<head>
    <meta charset="UTF-8">
    <meta name="viewport" content="width=device-width, initial-scale=1.0">
    <title>Login - myworktree</title>
    <style>
        :root {
            --bg-color: #f6f8fa;
            --text-primary: #24292e;
            --text-secondary: #586069;
            --card-bg: #ffffff;
            --card-border: #e1e4e8;
            --card-shadow: 0 4px 24px rgba(0,0,0,0.08);
            --input-bg: #ffffff;
            --input-border: #d0d7da;
            --input-focus-border: #2ea44f;
            --input-focus-shadow: rgba(46, 164, 79, 0.2);
            --button-bg: #2ea44f;
            --button-hover-bg: #2c974b;
            --error-text: #cb2431;
            --card-radius: 16px;
            --input-radius: 8px;
            --btn-radius: 8px;
        }
        @media (prefers-color-scheme: dark) {
            :root {
                --bg-color: #1a1a1a;
                --text-primary: #e0e0e0;
                --text-secondary: #888;
                --card-bg: #222222;
                --card-border: #333;
                --card-shadow: 0 4px 24px rgba(0,0,0,0.3);
                --input-bg: #2a2a2a;
                --input-border: #444;
                --input-focus-border: #3fb950;
                --input-focus-shadow: rgba(63, 185, 80, 0.2);
                --button-bg: #238636;
                --button-hover-bg: #2ea043;
                --error-text: #ff6666;
            }
        }
        body { font-family: -apple-system, BlinkMacSystemFont, "Segoe UI", Helvetica, Arial, sans-serif, "Apple Color Emoji", "Segoe UI Emoji"; background: var(--bg-color); color: var(--text-primary); display: flex; justify-content: center; align-items: center; height: 100vh; margin: 0; }
        .login-box { background: var(--card-bg); border: 1px solid var(--card-border); border-radius: var(--card-radius); padding: 40px; width: 100%; max-width: 400px; box-shadow: var(--card-shadow); box-sizing: border-box; margin: 20px; }
        h2 { font-size: 20px; font-weight: 600; margin: 0 0 8px 0; text-align: center; }
        .subtitle { font-size: 13px; color: var(--text-secondary); margin: 0 0 24px 0; text-align: center; }
        label { display: block; margin-bottom: 6px; font-size: 13px; font-weight: 500; color: var(--text-primary); }
        input[type="password"] { width: 100%; padding: 10px 12px; font-size: 14px; background: var(--input-bg); border: 1px solid var(--input-border); color: var(--text-primary); border-radius: var(--input-radius); box-sizing: border-box; margin-bottom: 16px; outline: none; transition: border-color 0.2s, box-shadow 0.2s; }
        input[type="password"]:focus { border-color: var(--input-focus-border); box-shadow: 0 0 0 3px var(--input-focus-shadow); }
        button { width: 100%; padding: 10px 16px; font-size: 14px; font-weight: 500; background: var(--button-bg); color: #ffffff; border: none; border-radius: var(--btn-radius); cursor: pointer; transition: background 0.15s ease; }
        button:hover { background: var(--button-hover-bg); }
    </style>
</head>
<body>
    <div class="login-box">
        <h2>myworktree</h2>
        <p class="subtitle">Enter your auth token to continue</p>
        <form id="login-form" method="post" action="/login">
            <input type="hidden" name="next" value="{{NEXT}}">
            <label for="token">Auth Token</label>
            <input type="password" id="token" name="token" placeholder="Enter auth token" required>
            <button type="submit">Login</button>
        </form>
    </div>
</body>
</html>`
		nextURL := r.URL.Query().Get("next")
		if nextURL == "" || !isValidRedirectPath(nextURL) {
			nextURL = "/"
		}
		loginHTML = strings.ReplaceAll(loginHTML, "{{NEXT}}", html.EscapeString(nextURL))
		w.Write([]byte(loginHTML))
		return
	}
	if r.Method == http.MethodPost {
		if err := r.ParseForm(); err != nil {
			http.Error(w, "bad request", http.StatusBadRequest)
			return
		}
		token := strings.TrimSpace(r.FormValue("token"))
		cfg, err := config.Load()
		if err != nil || token != cfg.AuthToken {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		nextURL := r.FormValue("next")
		if nextURL == "" || !isValidRedirectPath(nextURL) {
			nextURL = "/"
		}
		cookie := &http.Cookie{
			Name:     "mw_token",
			Value:    token,
			Path:     "/",
			MaxAge:   86400,
			SameSite: http.SameSiteLaxMode,
			HttpOnly: true,
		}
		if r.TLS != nil || r.Header.Get("X-Forwarded-Proto") == "https" {
			cookie.Secure = true
		}
		http.SetCookie(w, cookie)
		http.Redirect(w, r, nextURL, http.StatusFound)
		return
	}
	http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
}

func (s *Server) handleTags(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	base, err := os.UserConfigDir()
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err)
		return
	}
	mgr := tag.Manager{
		GlobalPath:  filepath.Join(base, "myworktree", "tags.json"),
		ProjectPath: filepath.Join(s.dataDir, "tags.json"),
	}
	m, err := mgr.LoadMerged()
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err)
		return
	}
	items := make([]tag.Tag, 0, len(m))
	for _, t := range m {
		items = append(items, t)
	}
	sort.Slice(items, func(i, j int) bool { return items[i].ID < items[j].ID })
	writeJSON(w, http.StatusOK, map[string]any{"tags": items})
}

func (s *Server) handleTagsOpenDir(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if !isLoopbackRequest(r) {
		writeErr(w, http.StatusForbidden, errors.New("host GUI actions are only allowed from loopback clients"))
		return
	}
	base, err := os.UserConfigDir()
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err)
		return
	}
	dir := filepath.Join(base, "myworktree")
	ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, "open", dir)
	out, err := cmd.CombinedOutput()
	if err != nil {
		if errors.Is(ctx.Err(), context.DeadlineExceeded) {
			err = fmt.Errorf("command timed out: open %s", dir)
		} else {
			detail := strings.TrimSpace(string(out))
			if detail != "" {
				err = fmt.Errorf("%w: %s", err, detail)
			}
		}
		s.logger.Printf("open tags dir command failed: args=%q err=%v", dir, err)
		writeErr(w, http.StatusInternalServerError, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

func (s *Server) handleWorktrees(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		items, err := s.worktreeMgr.List()
		if err != nil {
			writeErr(w, http.StatusInternalServerError, err)
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"worktrees": items})
	case http.MethodPost:
		var req struct {
			TaskDescription string `json:"task_description"`
			BaseRef         string `json:"base_ref"`
			AdoptIfExists   bool   `json:"adopt_if_exists"`
			BranchName      string `json:"branch_name"`
		}
		if err := readJSON(r.Body, &req); err != nil {
			writeErr(w, http.StatusBadRequest, err)
			return
		}
		opts := worktree.CreateOptions{BaseRef: req.BaseRef, AdoptIfExists: req.AdoptIfExists}
		if req.BranchName != "" {
			opts.BranchName = req.BranchName
		}
		item, err := s.worktreeMgr.CreateWithOptionsCtx(r.Context(), req.TaskDescription, opts)
		if err != nil {
			writeErr(w, http.StatusBadRequest, err)
			return
		}
		writeJSON(w, http.StatusCreated, item)
	default:
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
	}
}

func (s *Server) handleWorktreesUnmanaged(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	items, err := s.worktreeMgr.ListUnmanaged()
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"candidates": items})
}

func (s *Server) handleWorktreeImport(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	var req struct {
		Name string `json:"name"`
	}
	if err := readJSON(r.Body, &req); err != nil {
		writeErr(w, http.StatusBadRequest, err)
		return
	}
	item, err := s.worktreeMgr.Import(req.Name)
	if err != nil {
		writeErr(w, http.StatusBadRequest, err)
		return
	}
	writeJSON(w, http.StatusCreated, item)
}

func (s *Server) handleWorktreeDelete(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	var req struct {
		ID string `json:"id"`
	}
	if err := readJSON(r.Body, &req); err != nil {
		writeErr(w, http.StatusBadRequest, err)
		return
	}
	if err := s.worktreeMgr.Delete(req.ID); err != nil {
		writeErr(w, http.StatusBadRequest, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

func (s *Server) handleWorktreeStatus(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	id := strings.TrimSpace(r.URL.Query().Get("id"))
	if id == "" {
		writeErr(w, http.StatusBadRequest, errors.New("id is required"))
		return
	}

	var gitRoot string
	if id == instance.MainWorktreeID {
		gitRoot = s.root
	} else {
		worktrees, err := s.worktreeMgr.List()
		if err != nil {
			writeErr(w, http.StatusInternalServerError, err)
			return
		}
		found := false
		for _, wt := range worktrees {
			if wt.ID == id {
				gitRoot = wt.Path
				found = true
				break
			}
		}
		if !found {
			writeErr(w, http.StatusBadRequest, fmt.Errorf("unknown worktree id: %s", id))
			return
		}
	}

	type diffResult struct {
		changes []map[string]any
		total   map[string]int
		errMsg  string
	}

	runDiff := func(args ...string) diffResult {
		out, err := s.gitRunner(2*time.Second, gitRoot, args...)
		if err != nil {
			msg := strings.TrimSpace(string(out))
			if msg == "" {
				msg = err.Error()
			}
			return diffResult{
				changes: []map[string]any{},
				total:   map[string]int{"additions": 0, "deletions": 0},
				errMsg:  fmt.Sprintf("git diff failed: %s", msg),
			}
		}
		changes, total := parseGitDiffNumStat(string(out))
		return diffResult{changes: changes, total: total}
	}

	stagedCh := make(chan diffResult, 1)
	unstagedCh := make(chan diffResult, 1)
	untrackedCh := make(chan diffResult, 1)

	go func() { stagedCh <- runDiff("diff", "--cached", "--numstat") }()
	go func() { unstagedCh <- runDiff("diff", "--numstat") }()
	go func() {
		out, err := s.gitRunner(2*time.Second, gitRoot, "ls-files", "--others", "--exclude-standard")
		if err != nil {
			msg := strings.TrimSpace(string(out))
			if msg == "" {
				msg = err.Error()
			}
			untrackedCh <- diffResult{
				changes: []map[string]any{},
				total:   map[string]int{"additions": 0, "deletions": 0},
				errMsg:  fmt.Sprintf("git ls-files failed: %s", msg),
			}
			return
		}
		lines := strings.Split(strings.TrimSpace(string(out)), "\n")
		var changes []map[string]any
		totalAdds := 0
		for _, p := range lines {
			p = strings.TrimSpace(p)
			if p == "" {
				continue
			}
			fullPath := filepath.Join(gitRoot, p)
			adds := countFileLines(fullPath)
			totalAdds += adds
			changes = append(changes, map[string]any{
				"path":      p,
				"additions": adds,
				"deletions": 0,
				"status":    "untracked",
			})
		}
		untrackedCh <- diffResult{changes: changes, total: map[string]int{"additions": totalAdds, "deletions": 0}}
	}()

	var staged, unstaged, untracked diffResult
	select {
	case staged = <-stagedCh:
	case <-r.Context().Done():
		return
	}
	select {
	case unstaged = <-unstagedCh:
	case <-r.Context().Done():
		return
	}
	select {
	case untracked = <-untrackedCh:
	case <-r.Context().Done():
		return
	}

	unstaged.changes = append(unstaged.changes, untracked.changes...)
	unstaged.total["additions"] += untracked.total["additions"]

	if staged.errMsg != "" && unstaged.errMsg != "" {
		writeErr(w, http.StatusInternalServerError, fmt.Errorf("staged: %s; unstaged: %s", staged.errMsg, unstaged.errMsg))
		return
	}

	resp := map[string]any{
		"staged": map[string]any{
			"changes": staged.changes,
			"total":   staged.total,
		},
		"unstaged": map[string]any{
			"changes": unstaged.changes,
			"total":   unstaged.total,
		},
	}
	if staged.errMsg != "" {
		resp["staged"].(map[string]any)["error"] = staged.errMsg
	}
	if unstaged.errMsg != "" {
		resp["unstaged"].(map[string]any)["error"] = unstaged.errMsg
	}
	if untracked.errMsg != "" {
		resp["unstaged"].(map[string]any)["warning"] = untracked.errMsg
	}
	writeJSON(w, http.StatusOK, resp)
}

func (s *Server) handleWorktreesDiverged(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	result := make(map[string]gitx.DivergedResult)

	worktrees, err := s.worktreeMgr.List()
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err)
		return
	}

	mainBranch := gitx.DefaultBranch(s.root)

	var wg sync.WaitGroup
	var mu sync.Mutex

	for _, wt := range worktrees {
		wg.Add(1)
		go func(wt store.ManagedWorktree) {
			defer wg.Done()
			branch, err := gitx.CurrentBranch(wt.Path)
			var items gitx.DivergedResult
			if err != nil {
				items = gitx.DivergedResult{
					gitx.MainBranchKey: gitx.DivergedStatus{Error: fmt.Sprintf("cannot determine branch: %v", err)},
				}
			} else {
				items = gitx.CheckDiverged(wt.Path, branch, mainBranch)
			}
			mu.Lock()
			if len(items) > 0 {
				result[wt.ID] = items
			}
			mu.Unlock()
		}(wt)
	}

	wg.Add(1)
	go func() {
		defer wg.Done()
		rootBranch, err := gitx.CurrentBranch(s.root)
		var items gitx.DivergedResult
		if err != nil {
			items = gitx.DivergedResult{
				gitx.MainBranchKey: gitx.DivergedStatus{Error: fmt.Sprintf("cannot determine branch: %v", err)},
			}
		} else {
			items = gitx.CheckDiverged(s.root, rootBranch, mainBranch)
		}
		mu.Lock()
		if len(items) > 0 {
			result[instance.MainWorktreeID] = items
		}
		mu.Unlock()
	}()

	wg.Wait()

	writeJSON(w, http.StatusOK, map[string]any{"items": result})
}

func (s *Server) handleWorktreeDiverged(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	id := strings.TrimSpace(r.URL.Query().Get("id"))
	if id == "" {
		writeErr(w, http.StatusBadRequest, errors.New("id is required"))
		return
	}

	var worktreePath string
	if id == instance.MainWorktreeID {
		worktreePath = s.root
	} else {
		worktrees, err := s.worktreeMgr.List()
		if err != nil {
			writeErr(w, http.StatusInternalServerError, err)
			return
		}
		found := false
		for _, wt := range worktrees {
			if wt.ID == id {
				worktreePath = wt.Path
				found = true
				break
			}
		}
		if !found {
			writeErr(w, http.StatusBadRequest, fmt.Errorf("unknown worktree id: %s", id))
			return
		}
	}

	mainBranch := gitx.DefaultBranch(s.root)

	branch, err := gitx.CurrentBranch(worktreePath)
	if err != nil {
		result := map[string]gitx.DivergedResult{
			id: {
				gitx.MainBranchKey: gitx.DivergedStatus{Error: fmt.Sprintf("cannot determine branch: %v", err)},
			},
		}
		writeJSON(w, http.StatusOK, map[string]any{"items": result})
		return
	}

	items := gitx.CheckDiverged(worktreePath, branch, mainBranch)

	result := make(map[string]gitx.DivergedResult)
	if len(items) > 0 {
		result[id] = items
	}

	writeJSON(w, http.StatusOK, map[string]any{"items": result})
}

func (s *Server) handleInstances(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		items, err := s.instanceMgr.List()
		if err != nil {
			writeErr(w, http.StatusInternalServerError, err)
			return
		}
		st, err := s.store.Load()
		version := int64(0)
		if err == nil {
			version = st.Version
		}
		writeJSON(w, http.StatusOK, map[string]any{"instances": items, "version": version})
	case http.MethodPost:
		var req struct {
			WorktreeID string `json:"worktree_id"`
			TagID      string `json:"tag_id"`
			Command    string `json:"command"`
			Name       string `json:"name"`
		}
		if err := readJSON(r.Body, &req); err != nil {
			writeErr(w, http.StatusBadRequest, err)
			return
		}
		item, err := s.instanceMgr.Start(instance.StartInput{
			WorktreeID: req.WorktreeID,
			Root: func() string {
				if req.WorktreeID == instance.MainWorktreeID {
					return s.root
				}
				return ""
			}(),
			TagID:   req.TagID,
			Command: req.Command,
			Name:    req.Name,
		})
		if err != nil {
			writeErr(w, http.StatusBadRequest, err)
			return
		}
		writeJSON(w, http.StatusCreated, item)
	case http.MethodPatch:
		var req struct {
			ID   string `json:"id"`
			Name string `json:"name"`
		}
		if err := readJSON(r.Body, &req); err != nil {
			writeErr(w, http.StatusBadRequest, err)
			return
		}
		updated, err := s.instanceMgr.UpdateName(req.ID, req.Name)
		if err != nil {
			if errors.Is(err, instance.ErrInstanceNotFound) {
				writeErr(w, http.StatusNotFound, err)
				return
			}
			writeErr(w, http.StatusBadRequest, err)
			return
		}
		writeJSON(w, http.StatusOK, updated)
	default:
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
	}
}

func (s *Server) handleInstanceStop(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	var req struct {
		ID string `json:"id"`
	}
	if err := readJSON(r.Body, &req); err != nil {
		writeErr(w, http.StatusBadRequest, err)
		return
	}
	if err := s.instanceMgr.Stop(req.ID); err != nil {
		writeErr(w, http.StatusBadRequest, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

func (s *Server) handleInstanceRestart(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	var req struct {
		ID string `json:"id"`
	}
	if err := readJSON(r.Body, &req); err != nil {
		writeErr(w, http.StatusBadRequest, err)
		return
	}
	item, err := s.instanceMgr.Restart(req.ID)
	if err != nil {
		writeErr(w, http.StatusBadRequest, err)
		return
	}
	writeJSON(w, http.StatusCreated, item)
}

func (s *Server) handleInstanceDelete(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	var req struct {
		ID string `json:"id"`
	}
	if err := readJSON(r.Body, &req); err != nil {
		writeErr(w, http.StatusBadRequest, err)
		return
	}
	if err := s.instanceMgr.Delete(req.ID); err != nil {
		writeErr(w, http.StatusBadRequest, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

func (s *Server) handleInstanceReorder(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPatch {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	var req struct {
		WorktreeID string   `json:"worktree_id"`
		Order      []string `json:"order"`
		Version    int64    `json:"version"`
	}
	if err := readJSON(r.Body, &req); err != nil {
		writeErr(w, http.StatusBadRequest, err)
		return
	}
	err := s.instanceMgr.ReorderInstances(req.WorktreeID, req.Order, req.Version)
	if errors.Is(err, store.ErrVersionConflict) {
		st, _ := s.store.Load()
		writeJSON(w, http.StatusConflict, map[string]any{
			"error":   "state changed, please refresh",
			"version": st.Version,
		})
		return
	}
	if err != nil {
		writeErr(w, http.StatusBadRequest, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

func (s *Server) handleInstanceInput(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	var req struct {
		ID    string `json:"id"`
		Input string `json:"input"`
	}
	if err := readJSON(r.Body, &req); err != nil {
		writeErr(w, http.StatusBadRequest, err)
		return
	}
	if err := s.instanceMgr.SendInput(req.ID, req.Input); err != nil {
		writeErr(w, http.StatusBadRequest, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

func (s *Server) handleInstanceTTYWS(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	id := strings.TrimSpace(r.URL.Query().Get("id"))
	if id == "" {
		http.Error(w, "id is required", http.StatusBadRequest)
		return
	}
	conn, err := ws.Upgrade(w, r)
	if err != nil {
		return
	}
	clientHandle, resizeUpdates, err := s.newTTYClientHandle(id)
	if err != nil {
		_ = conn.WriteClose(ws.CloseMessage(1013, err.Error()))
		_ = conn.Close()
		return
	}
	defer func() {
		clientHandle.Close()
		_ = conn.Close()
		if rec := recover(); rec != nil {
			panic(rec)
		}
	}()

	s.instanceMgr.SetConnectionType(id, "websocket")
	defer s.instanceMgr.SetConnectionType(id, "")

	// Step 1: Send handshake ready message
	readyMsg := []byte(`{"type":"ready"}`)
	if err := conn.WriteText(readyMsg); err != nil {
		return
	}

	// Step 2: Start single goroutine to read all messages
	type wsMessage struct {
		op   byte
		data []byte
	}
	msgChan := make(chan wsMessage, 64)
	go func() {
		defer close(msgChan)
		for {
			op, data, err := conn.ReadMessage()
			if err != nil {
				return
			}
			msgChan <- wsMessage{op: op, data: data}
		}
	}()

	// Step 3: Wait for first resize message (with timeout)
	handshakeComplete := false
	var outputChan <-chan string
	var cancel context.CancelFunc
	defer func() {
		if cancel != nil {
			cancel()
		}
	}()

	handshakeTimer := time.NewTimer(5 * time.Second)
	defer handshakeTimer.Stop()

	completeHandshake := func() bool {
		initial, err := s.instanceMgr.Tail(id, 64*1024)
		if err != nil {
			_ = conn.WriteBinary([]byte(err.Error()))
			return false
		}
		if initial != "" {
			_ = conn.WriteBinary([]byte(initial))
		}

		ch, cancelFn, err := s.instanceMgr.SubscribeOutput(id)
		if err != nil {
			_ = conn.WriteBinary([]byte(err.Error()))
			return false
		}
		cancel = cancelFn
		outputChan = ch
		return true
	}

	for {
		select {
		case msg, ok := <-msgChan:
			if !ok {
				return
			}

			if ws.IsClose(msg.op) {
				_ = conn.WriteClose(ws.CloseMessage(1000, "bye"))
				return
			}
			if ws.IsPing(msg.op) {
				_ = conn.WritePong(msg.data)
				continue
			}
			if !ws.IsDataOpcode(msg.op) {
				continue
			}

			var resizeMsg struct {
				Type string `json:"type"`
				Cols int    `json:"cols"`
				Rows int    `json:"rows"`
			}
			isResize := json.Unmarshal(msg.data, &resizeMsg) == nil && resizeMsg.Type == "resize"

			if isResize && resizeMsg.Cols > 0 && resizeMsg.Rows > 0 {
				s.updateTTYClientSize(id, clientHandle.clientID, resizeMsg.Cols, resizeMsg.Rows)
			}

			if !handshakeComplete && isResize {
				handshakeComplete = true
				handshakeTimer.Stop()
				if !completeHandshake() {
					return
				}
				continue
			}

			if handshakeComplete && !isResize {
				if err := s.instanceMgr.SendInput(id, string(msg.data)); err != nil {
					return
				}
			}

		case <-handshakeTimer.C:
			if !handshakeComplete {
				handshakeComplete = true
				if !completeHandshake() {
					return
				}
			}

		case chunk, ok := <-outputChan:
			if !ok {
				return
			}
			if err := conn.WriteBinary([]byte(chunk)); err != nil {
				return
			}
		case size, ok := <-resizeUpdates:
			if !ok {
				resizeUpdates = nil
				continue
			}
			payload, err := json.Marshal(struct {
				Type string `json:"type"`
				Cols int    `json:"cols"`
				Rows int    `json:"rows"`
			}{
				Type: "resize",
				Cols: size.Cols,
				Rows: size.Rows,
			})
			if err != nil {
				return
			}
			if err := conn.WriteText(payload); err != nil {
				return
			}
		}
	}
}

func (s *Server) handleInstanceLog(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	id := strings.TrimSpace(r.URL.Query().Get("id"))
	since := parseInt64Default(r.URL.Query().Get("since"), -1)
	if since >= 0 {
		body, next, err := s.instanceMgr.ReadSince(id, since, 64*1024)
		if err != nil {
			writeErr(w, http.StatusBadRequest, err)
			return
		}
		w.Header().Set("X-Log-Offset", strconv.FormatInt(next, 10))
		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
		_, _ = io.WriteString(w, body)
		return
	}
	body, err := s.instanceMgr.Tail(id, 64*1024)
	if err != nil {
		writeErr(w, http.StatusBadRequest, err)
		return
	}
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	_, _ = io.WriteString(w, body)
}

func (s *Server) handleInstanceLogStream(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	id := strings.TrimSpace(r.URL.Query().Get("id"))
	since := parseInt64Default(r.URL.Query().Get("since"), 0)

	initial, next, err := s.instanceMgr.ReadSince(id, since, 64*1024)
	if err != nil {
		writeErr(w, http.StatusBadRequest, err)
		return
	}

	flusher, ok := w.(http.Flusher)
	if !ok {
		http.Error(w, "streaming unsupported", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")

	cleanupSSE := s.instanceMgr.RegisterSSEConnection(id)
	defer cleanupSSE()

	if err := writeSSELogEvent(w, initial, next); err != nil {
		return
	}
	flusher.Flush()

	ticker := time.NewTicker(1 * time.Second)
	defer ticker.Stop()
	cursor := next
	for {
		select {
		case <-r.Context().Done():
			return
		case <-ticker.C:
			chunk, n, err := s.instanceMgr.ReadSince(id, cursor, 64*1024)
			if err != nil {
				return
			}
			if chunk == "" {
				_, _ = io.WriteString(w, ": ping\n\n")
				flusher.Flush()
				continue
			}
			cursor = n
			if err := writeSSELogEvent(w, chunk, cursor); err != nil {
				return
			}
			flusher.Flush()
		}
	}
}

func (s *Server) handleInstanceStats(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	instances, err := s.instanceMgr.List()
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err)
		return
	}

	// Build flat input for the collector. Only include running instances.
	flat := make([]monitor.InputInstance, 0, len(instances))
	for _, inst := range instances {
		if inst.Status != "running" {
			continue
		}
		flat = append(flat, monitor.InputInstance{
			ID:           inst.ID,
			Name:         inst.Name,
			WorktreeID:   inst.WorktreeID,
			WorktreeName: inst.WorktreeName,
			PID:          inst.PID,
			Status:       inst.Status,
		})
	}

	connTypes := s.instanceMgr.AllConnectionTypes()
	stats := s.monitor.Collect(flat, connTypes)

	writeJSON(w, http.StatusOK, stats)
}

func (s *Server) handleMCPTools(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"tools": s.mcpAdapter.ToolNames()})
}

func (s *Server) handleMCPCall(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	var req struct {
		Tool string          `json:"tool"`
		Args json.RawMessage `json:"args"`
	}
	if err := readJSON(r.Body, &req); err != nil {
		writeErr(w, http.StatusBadRequest, err)
		return
	}
	tool := strings.TrimSpace(req.Tool)
	switch tool {
	case "worktree_list":
		items, err := s.worktreeMgr.List()
		if err != nil {
			writeErr(w, http.StatusInternalServerError, err)
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"result": map[string]any{"worktrees": items}})
	case "worktree_create":
		var args struct {
			TaskDescription string `json:"task_description"`
			BaseRef         string `json:"base_ref"`
		}
		if err := decodeArgs(req.Args, &args); err != nil {
			writeErr(w, http.StatusBadRequest, err)
			return
		}
		item, err := s.worktreeMgr.CreateWithOptionsCtx(r.Context(), args.TaskDescription, worktree.CreateOptions{BaseRef: args.BaseRef})
		if err != nil {
			writeErr(w, http.StatusBadRequest, err)
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"result": item})
	case "worktree_delete":
		var args struct {
			ID string `json:"id"`
		}
		if err := decodeArgs(req.Args, &args); err != nil {
			writeErr(w, http.StatusBadRequest, err)
			return
		}
		if err := s.worktreeMgr.Delete(args.ID); err != nil {
			writeErr(w, http.StatusBadRequest, err)
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"result": map[string]string{"status": "ok"}})
	case "branch_list":
		def, out, err := s.listTopBranches()
		if err != nil {
			writeErr(w, http.StatusInternalServerError, err)
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"result": map[string]any{"default": def, "branches": out}})
	case "tag_list":
		base, err := os.UserConfigDir()
		if err != nil {
			writeErr(w, http.StatusInternalServerError, err)
			return
		}
		mgr := tag.Manager{
			GlobalPath:  filepath.Join(base, "myworktree", "tags.json"),
			ProjectPath: filepath.Join(s.dataDir, "tags.json"),
		}
		m, err := mgr.LoadMerged()
		if err != nil {
			writeErr(w, http.StatusInternalServerError, err)
			return
		}
		type item struct {
			ID      string `json:"id"`
			Command string `json:"command"`
		}
		items := make([]item, 0, len(m))
		for id, t := range m {
			items = append(items, item{ID: id, Command: t.Command})
		}
		sort.Slice(items, func(i, j int) bool { return items[i].ID < items[j].ID })
		writeJSON(w, http.StatusOK, map[string]any{"result": map[string]any{"tags": items}})
	case "instance_list":
		items, err := s.instanceMgr.List()
		if err != nil {
			writeErr(w, http.StatusInternalServerError, err)
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"result": map[string]any{"instances": items}})
	case "instance_start":
		var args struct {
			WorktreeID string `json:"worktree_id"`
			TagID      string `json:"tag_id"`
			Command    string `json:"command"`
			Name       string `json:"name"`
		}
		if err := decodeArgs(req.Args, &args); err != nil {
			writeErr(w, http.StatusBadRequest, err)
			return
		}
		item, err := s.instanceMgr.Start(instance.StartInput{
			WorktreeID: args.WorktreeID,
			TagID:      args.TagID,
			Command:    args.Command,
			Name:       args.Name,
		})
		if err != nil {
			writeErr(w, http.StatusBadRequest, err)
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"result": item})
	case "instance_stop":
		var args struct {
			ID string `json:"id"`
		}
		if err := decodeArgs(req.Args, &args); err != nil {
			writeErr(w, http.StatusBadRequest, err)
			return
		}
		if err := s.instanceMgr.Stop(args.ID); err != nil {
			writeErr(w, http.StatusBadRequest, err)
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"result": map[string]string{"status": "ok"}})
	case "instance_input":
		var args struct {
			ID    string `json:"id"`
			Input string `json:"input"`
		}
		if err := decodeArgs(req.Args, &args); err != nil {
			writeErr(w, http.StatusBadRequest, err)
			return
		}
		if err := s.instanceMgr.SendInput(args.ID, args.Input); err != nil {
			writeErr(w, http.StatusBadRequest, err)
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"result": map[string]string{"status": "ok"}})
	case "instance_delete":
		var args struct {
			ID string `json:"id"`
		}
		if err := decodeArgs(req.Args, &args); err != nil {
			writeErr(w, http.StatusBadRequest, err)
			return
		}
		if err := s.instanceMgr.Delete(args.ID); err != nil {
			writeErr(w, http.StatusBadRequest, err)
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"result": map[string]string{"status": "ok"}})
	case "instance_log_tail":
		var args struct {
			ID string `json:"id"`
			N  int64  `json:"n"`
		}
		if err := decodeArgs(req.Args, &args); err != nil {
			writeErr(w, http.StatusBadRequest, err)
			return
		}
		body, err := s.instanceMgr.Tail(args.ID, args.N)
		if err != nil {
			writeErr(w, http.StatusBadRequest, err)
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"result": map[string]any{"text": body}})
	default:
		writeErr(w, http.StatusBadRequest, fmt.Errorf("unknown tool: %s", tool))
	}
}

func (s *Server) withAuth(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if isLoopbackRequest(r) {
			next.ServeHTTP(w, r)
			return
		}
		if !sameOriginHost(r) {
			http.Error(w, "forbidden origin", http.StatusForbidden)
			return
		}
		if r.URL.Path == "/login" || r.URL.Path == "/api/csrf-token" {
			next.ServeHTTP(w, r)
			return
		}
		cfg, err := config.Load()
		if err != nil {
			http.Error(w, "auth config unavailable", http.StatusServiceUnavailable)
			return
		}
		if strings.TrimSpace(cfg.AuthToken) == "" {
			next.ServeHTTP(w, r)
			return
		}
		token := extractAuthToken(r)
		if token != cfg.AuthToken {
			if !s.allowAuthAttempt(clientIP(r.RemoteAddr)) {
				http.Error(w, "too many unauthorized attempts", http.StatusTooManyRequests)
				return
			}
			if isAPIClient(r) {
				http.Error(w, "unauthorized", http.StatusUnauthorized)
				return
			}
			redirectURL := "/login"
			if r.URL.Path != "/" {
				redirectURL = "/login?next=" + url.QueryEscape(r.URL.Path)
			}
			http.Redirect(w, r, redirectURL, http.StatusFound)
			return
		}
		s.resetAuthAttempts(clientIP(r.RemoteAddr))
		next.ServeHTTP(w, r)
	})
}

func isAPIClient(r *http.Request) bool {
	accept := r.Header.Get("Accept")
	return strings.Contains(accept, "application/json") || strings.Contains(accept, "text/event-stream")
}

func isValidRedirectPath(path string) bool {
	if path == "" {
		return false
	}
	if path[0] != '/' {
		return false
	}
	if len(path) > 1 && path[1] == '/' {
		return false
	}
	if strings.Contains(path, "://") {
		return false
	}
	return true
}

func extractAuthToken(r *http.Request) string {
	if token := strings.TrimSpace(strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")); token != "" {
		return token
	}
	if token := strings.TrimSpace(r.URL.Query().Get("token")); token != "" {
		return token
	}
	if cookie, err := r.Cookie("mw_token"); err == nil {
		return strings.TrimSpace(cookie.Value)
	}
	return ""
}

func (s *Server) withServerRevision(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if s.serverRev != "" {
			w.Header().Set("X-Myworktree-Server-Rev", s.serverRev)
		}
		next.ServeHTTP(w, r)
	})
}

func readJSON(r io.Reader, out any) error {
	dec := json.NewDecoder(r)
	dec.DisallowUnknownFields()
	return dec.Decode(out)
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

func writeErr(w http.ResponseWriter, status int, err error) {
	writeJSON(w, status, map[string]string{"error": err.Error()})
}

func parseInt64Default(s string, def int64) int64 {
	s = strings.TrimSpace(s)
	if s == "" {
		return def
	}
	v, err := strconv.ParseInt(s, 10, 64)
	if err != nil {
		return def
	}
	return v
}

func decodeArgs(raw json.RawMessage, out any) error {
	if len(raw) == 0 {
		raw = []byte("{}")
	}
	return json.Unmarshal(raw, out)
}

func writeSSELogEvent(w io.Writer, chunk string, next int64) error {
	payload := map[string]any{
		"chunk": chunk,
		"next":  next,
	}
	b, err := json.Marshal(payload)
	if err != nil {
		return err
	}
	if _, err := io.WriteString(w, "event: log\n"); err != nil {
		return err
	}
	if _, err := io.WriteString(w, "data: "); err != nil {
		return err
	}
	if _, err := w.Write(b); err != nil {
		return err
	}
	_, err = io.WriteString(w, "\n\n")
	return err
}

func sameOriginHost(r *http.Request) bool {
	origin := strings.TrimSpace(r.Header.Get("Origin"))
	if origin == "" {
		return true
	}
	u, err := url.Parse(origin)
	if err != nil {
		return false
	}
	return strings.EqualFold(u.Host, r.Host)
}

func clientIP(remoteAddr string) string {
	host, _, err := net.SplitHostPort(remoteAddr)
	if err != nil {
		return remoteAddr
	}
	return host
}

// isLoopbackHost checks if host is a loopback address.
// IPv6 is explicitly disabled and NOT supported.
// Only IPv4 loopback (127.x.x.x) and "localhost" are considered loopback.
// IPv6 addresses (including ::1, ::ffff:127.0.0.1) are rejected as non-loopback.
func isLoopbackHost(host string) bool {
	host = strings.TrimSpace(host)
	if host == "" {
		return false
	}
	if strings.EqualFold(host, "localhost") {
		return true
	}
	// Reject all IPv6 addresses (not supported). IPv6 addresses contain ":".
	if strings.Contains(host, ":") {
		return false
	}
	ip := net.ParseIP(host)
	if ip == nil {
		return false
	}
	return ip.IsLoopback()
}

func isLoopbackRequest(r *http.Request) bool {
	return isLoopbackHost(clientIP(r.RemoteAddr))
}

func (s *Server) allowAuthAttempt(ip string) bool {
	s.authMu.Lock()
	defer s.authMu.Unlock()
	now := time.Now()
	v := s.authFails[ip]
	if now.After(v.WindowEnd) {
		v = authFail{Count: 0, WindowEnd: now.Add(time.Minute)}
	}
	v.Count++
	s.authFails[ip] = v
	return v.Count <= 20
}

func (s *Server) resetAuthAttempts(ip string) {
	s.authMu.Lock()
	defer s.authMu.Unlock()
	delete(s.authFails, ip)
}

func userProjectDataDir(gitRoot string) (string, error) {
	base, err := os.UserConfigDir()
	if err != nil {
		return "", err
	}
	repoHash := gitx.HashPath(gitRoot)
	return filepath.Join(base, "myworktree", repoHash), nil
}

func resolveRepoListenAddr(listenAddr, dataDir string, logger *log.Logger) (string, error) {
	host, port, err := net.SplitHostPort(listenAddr)
	if err != nil || strings.TrimSpace(port) != "0" {
		return listenAddr, nil
	}
	persisted, err := readRepoListenPort(dataDir)
	if err != nil {
		if errors.Is(err, errInvalidRepoListenPort) {
			if logger != nil {
				logger.Printf("ignore invalid repo listen port config: %v", err)
			}
			return allocateRepoListenAddr(listenAddr, dataDir)
		}
		return "", err
	}
	if persisted > 0 {
		addr := net.JoinHostPort(host, strconv.Itoa(persisted))
		if canListenTCP(addr) {
			return addr, nil
		}
	}
	return allocateRepoListenAddr(listenAddr, dataDir)
}

func allocateRepoListenAddr(listenAddr, dataDir string) (string, error) {
	host, _, err := net.SplitHostPort(listenAddr)
	if err != nil {
		return "", err
	}
	probe, err := net.Listen("tcp", net.JoinHostPort(host, "0"))
	if err != nil {
		return "", err
	}
	defer probe.Close()
	p := probe.Addr().(*net.TCPAddr).Port
	if err := writeRepoListenPort(dataDir, p); err != nil {
		return "", err
	}
	return net.JoinHostPort(host, strconv.Itoa(p)), nil
}

func canListenTCP(addr string) bool {
	ln, err := net.Listen("tcp", addr)
	if err != nil {
		return false
	}
	_ = ln.Close()
	return true
}

func readRepoListenPort(dataDir string) (int, error) {
	b, err := os.ReadFile(filepath.Join(dataDir, "server.json"))
	if errors.Is(err, os.ErrNotExist) {
		return 0, nil
	}
	if err != nil {
		return 0, err
	}
	var cfg serverConfig
	if err := json.Unmarshal(b, &cfg); err != nil {
		return 0, err
	}
	if cfg.ListenPort < 1 || cfg.ListenPort > 65535 {
		return 0, fmt.Errorf("%w: %d", errInvalidRepoListenPort, cfg.ListenPort)
	}
	return cfg.ListenPort, nil
}

func generateInstanceID() string {
	bid := make([]byte, 8)
	if _, err := rand.Read(bid); err != nil {
		// Fallback for crypto/rand failure (extremely rare)
		// Use timestamp + PID + stack hash for basic uniqueness
		h := sha256.New()
		h.Write([]byte(fmt.Sprintf("%d-%d-%p", time.Now().UnixNano(), os.Getpid(), generateInstanceID)))
		copy(bid, h.Sum(nil)[:8])
	}
	return fmt.Sprintf("%d-%d-%x", os.Getpid(), time.Now().UnixNano(), bid)
}

func writeRepoListenPort(dataDir string, port int) error {
	filePath := filepath.Join(dataDir, "server.json")
	var cfg serverConfig
	if data, err := os.ReadFile(filePath); err == nil {
		_ = json.Unmarshal(data, &cfg)
	}
	cfg.ListenPort = port
	if cfg.InstanceID == "" {
		cfg.InstanceID = generateInstanceID()
	}

	if err := os.MkdirAll(dataDir, 0o755); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(dataDir, "server.json.*")
	if err != nil {
		return err
	}
	tmpPath := tmp.Name()
	var renamed bool
	defer func() {
		tmp.Close()
		if !renamed {
			os.Remove(tmpPath)
		}
	}()
	if err := tmp.Chmod(0o600); err != nil {
		return err
	}
	if err := json.NewEncoder(tmp).Encode(cfg); err != nil {
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	if err := os.Rename(tmpPath, filePath); err != nil {
		return err
	}
	renamed = true
	return nil
}

// countFileLines reads the file at path and returns its line count.
// Returns 0 if the file cannot be read, exceeds 1 MB, or appears binary.
func countFileLines(path string) int {
	info, err := os.Stat(path)
	if err != nil {
		return 0
	}
	if info.Size() > 1<<20 {
		return 0
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return 0
	}
	if isBinaryData(data) {
		return 0
	}
	return strings.Count(string(data), "\n")
}

// isBinaryData returns true if data contains null bytes in the first 8 KB,
// indicating binary content.
func isBinaryData(data []byte) bool {
	n := len(data)
	if n > 8192 {
		n = 8192
	}
	for _, b := range data[:n] {
		if b == 0 {
			return true
		}
	}
	return false
}

// parseGitDiffNumStat parses the output of "git diff --numstat HEAD" and
// returns per-file change info and totals.
//
// Git diff --numstat format per file line:
//
//	<additions>\t<deletions>\t<path>
//
// Binary files use "-" for additions and deletions, which we surface as 0/0.
func parseGitDiffNumStat(output string) ([]map[string]any, map[string]int) {
	var changes []map[string]any
	totalAdds, totalDels := 0, 0

	for _, line := range strings.Split(output, "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}

		parts := strings.SplitN(line, "\t", 3)
		if len(parts) != 3 {
			continue
		}

		adds, dels := 0, 0
		var err error
		if parts[0] != "-" {
			adds, err = strconv.Atoi(parts[0])
			if err != nil {
				continue
			}
		}
		if parts[1] != "-" {
			dels, err = strconv.Atoi(parts[1])
			if err != nil {
				continue
			}
		}

		path := strings.TrimSpace(parts[2])
		if path == "" {
			continue
		}

		totalAdds += adds
		totalDels += dels
		changes = append(changes, map[string]any{
			"path":      path,
			"additions": adds,
			"deletions": dels,
		})
	}

	return changes, map[string]int{"additions": totalAdds, "deletions": totalDels}
}

func (s *Server) handleLLMConfig(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		cfg := llm.Load()
		apiKeyMasked := ""
		if cfg.APIKey != "" {
			apiKeyMasked = llm.MaskKey(cfg.APIKey)
		}
		writeJSON(w, http.StatusOK, map[string]any{
			"protocol":        cfg.Protocol,
			"api_key_masked":  apiKeyMasked,
			"api_address":     cfg.APIAddress,
			"model":           cfg.Model,
			"reasoning_split": cfg.ReasoningSplit,
			"is_secure":       s.isSecure,
			"available":       llm.IsAvailable(),
		})
	case http.MethodPatch:
		var req struct {
			Protocol       string `json:"protocol"`
			APIKey         string `json:"api_key"`
			APIAddress     string `json:"api_address"`
			Model          string `json:"model"`
			ReasoningSplit bool   `json:"reasoning_split"`
		}
		if err := readJSON(r.Body, &req); err != nil {
			writeErr(w, http.StatusBadRequest, err)
			return
		}
		cfg := llm.Load()
		if req.Protocol != "" {
			if req.Protocol != "openai" && req.Protocol != "anthropic" {
				writeErr(w, http.StatusBadRequest, fmt.Errorf("invalid protocol: must be 'openai' or 'anthropic'"))
				return
			}
			// Check if switching to a protocol without an API key
			if req.APIKey == "" && cfg.APIKey == "" {
				writeErr(w, http.StatusBadRequest, fmt.Errorf("API key is required for %s protocol", req.Protocol))
				return
			}
			cfg.Protocol = req.Protocol
		}
		if req.APIKey != "" {
			cfg.APIKey = req.APIKey
		}
		if req.APIAddress != "" {
			cfg.APIAddress = req.APIAddress
		}
		if req.Model != "" {
			cfg.Model = req.Model
		}
		cfg.ReasoningSplit = req.ReasoningSplit
		if err := llm.Save(cfg); err != nil {
			writeErr(w, http.StatusInternalServerError, err)
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"status": "ok", "protocol": cfg.Protocol})
	default:
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
	}
}

func (s *Server) handleLLMTest(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if !llm.IsAvailable() {
		writeErr(w, http.StatusBadRequest, fmt.Errorf("no LLM configured"))
		return
	}
	branchName, err := llm.TestConnection(r.Context())
	if err != nil {
		writeErr(w, http.StatusBadRequest, fmt.Errorf("invalid API key or network error"))
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"status": "ok", "branch_name": branchName})
}

func (s *Server) handleLLMGenerate(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if !llm.IsAvailable() {
		writeErr(w, http.StatusBadRequest, fmt.Errorf("no LLM configured"))
		return
	}
	var req struct {
		TaskDescription string `json:"task_description"`
	}
	if err := readJSON(r.Body, &req); err != nil {
		writeErr(w, http.StatusBadRequest, err)
		return
	}
	taskDesc := strings.TrimSpace(req.TaskDescription)
	if taskDesc == "" {
		writeErr(w, http.StatusBadRequest, fmt.Errorf("task description is required"))
		return
	}
	branchName, err := llm.GenerateBranchName(r.Context(), taskDesc)
	if err != nil {
		writeErr(w, http.StatusBadRequest, fmt.Errorf("generation failed: %w", err))
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"branch_name": branchName})
}

func computeServerRevision() string {
	parts := []string{}
	if bi, ok := debug.ReadBuildInfo(); ok {
		parts = append(parts, bi.Main.Path, bi.Main.Version, bi.GoVersion)
		for _, key := range []string{"vcs.revision", "vcs.modified", "vcs.time"} {
			for _, s := range bi.Settings {
				if s.Key == key {
					parts = append(parts, key+"="+s.Value)
					break
				}
			}
		}
	}
	if exe, err := os.Executable(); err == nil {
		if fi, err := os.Stat(exe); err == nil {
			parts = append(parts, exe, fi.ModTime().UTC().Format(time.RFC3339Nano), strconv.FormatInt(fi.Size(), 10))
		}
	}
	sum := sha256.Sum256([]byte(strings.Join(parts, "|")))
	return hex.EncodeToString(sum[:8])
}

func (s *Server) resolveOpenWorktreePath(id string) (string, error) {
	if id == "__main__" {
		return s.root, nil
	}
	items, err := s.worktreeMgr.List()
	if err != nil {
		return "", err
	}
	for _, it := range items {
		if it.ID == id {
			return it.Path, nil
		}
	}
	return "", errWorktreeNotFound
}

func (s *Server) handleWorktreeOpen(w http.ResponseWriter, r *http.Request, args func(path string) []string) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if !isLoopbackRequest(r) {
		writeErr(w, http.StatusForbidden, errors.New("host GUI actions are only allowed from loopback clients"))
		return
	}
	var req struct {
		ID string `json:"id"`
	}
	if err := readJSON(r.Body, &req); err != nil {
		writeErr(w, http.StatusBadRequest, err)
		return
	}

	path, err := s.resolveOpenWorktreePath(req.ID)
	if err != nil {
		if errors.Is(err, errWorktreeNotFound) {
			writeErr(w, http.StatusNotFound, err)
			return
		}
		writeErr(w, http.StatusInternalServerError, err)
		return
	}

	cmdArgs := args(path)
	ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
	defer cancel()

	cmd := exec.CommandContext(ctx, cmdArgs[0], cmdArgs[1:]...)
	out, err := cmd.CombinedOutput()
	if err != nil {
		if errors.Is(ctx.Err(), context.DeadlineExceeded) {
			err = fmt.Errorf("command timed out: %s", strings.Join(cmdArgs, " "))
		} else {
			detail := strings.TrimSpace(string(out))
			if detail != "" {
				err = fmt.Errorf("%w: %s", err, detail)
			}
		}
		s.logger.Printf("open worktree command failed: args=%q err=%v", cmdArgs, err)
		writeErr(w, http.StatusInternalServerError, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

func (s *Server) handleWorktreeOpenTerminal(w http.ResponseWriter, r *http.Request) {
	s.handleWorktreeOpen(w, r, func(path string) []string {
		return []string{"open", "-a", "Terminal", path}
	})
}

func (s *Server) handleWorktreeOpenFinder(w http.ResponseWriter, r *http.Request) {
	s.handleWorktreeOpen(w, r, func(path string) []string {
		return []string{
			"osascript",
			"-e", fmt.Sprintf(`tell application "Finder" to open POSIX file %q`, path),
			"-e", `tell application "Finder" to activate`,
		}
	})
}
