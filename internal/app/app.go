package app

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"myworktree/internal/gitx"
	"myworktree/internal/instance"
	"myworktree/internal/mcp"
	"myworktree/internal/store"
	"myworktree/internal/tag"
	"myworktree/internal/ui"
	"myworktree/internal/worktree"
)

type Config struct {
	ListenAddr   string
	AuthToken    string
	TLSCert      string
	TLSKey       string
	Open         bool
	WorktreesDir string
}

type Server struct {
	cfg     Config
	logger  *log.Logger
	ln      net.Listener
	mux     *http.ServeMux
	root    string
	dataDir string

	store       store.FileStore
	worktreeMgr worktree.Manager
	instanceMgr *instance.Manager
	mcpAdapter  mcp.Adapter
	authMu      sync.Mutex
	authFails   map[string]authFail
}

type authFail struct {
	Count     int
	WindowEnd time.Time
}

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
		Store:   st,
		Logger:  logger,
	}

	mux := http.NewServeMux()
	s := &Server{
		cfg:         cfg,
		logger:      logger,
		mux:         mux,
		root:        root,
		dataDir:     dataDir,
		store:       st,
		worktreeMgr: worktreeMgr,
		instanceMgr: instanceMgr,
		mcpAdapter: mcp.Adapter{
			Worktrees: worktreeMgr,
			Instances: instanceMgr,
		},
		authFails: map[string]authFail{},
	}
	s.registerAPIs(mux)
	ui.Register(mux)

	return s, nil
}

func (s *Server) Start() (string, error) {
	if err := s.validateSecurity(); err != nil {
		return "", err
	}
	if err := os.MkdirAll(s.dataDir, 0o755); err != nil {
		return "", err
	}

	ln, err := net.Listen("tcp", s.cfg.ListenAddr)
	if err != nil {
		return "", err
	}
	s.ln = ln

	h := s.withAuth(s.mux)

	go func() {
		if s.cfg.TLSCert != "" || s.cfg.TLSKey != "" {
			_ = http.ServeTLS(ln, h, s.cfg.TLSCert, s.cfg.TLSKey)
			return
		}
		_ = http.Serve(ln, h)
	}()

	scheme := "http"
	if s.cfg.TLSCert != "" && s.cfg.TLSKey != "" {
		scheme = "https"
	}
	addr := ln.Addr().String()
	url := fmt.Sprintf("%s://%s/", scheme, addr)
	if s.cfg.Open {
		if err := openURL(url); err != nil {
			s.logger.Printf("open browser failed: %v", err)
		}
	}
	return url, nil
}

func openURL(url string) error {
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
	isLoopback := host == "127.0.0.1" || host == "localhost" || host == "::1"
	if !isLoopback && strings.TrimSpace(s.cfg.AuthToken) == "" {
		return errors.New("--auth is required when listening on a non-loopback address")
	}
	if (s.cfg.TLSCert == "") != (s.cfg.TLSKey == "") {
		return errors.New("--tls-cert and --tls-key must be provided together")
	}
	return nil
}

func (s *Server) registerAPIs(mux *http.ServeMux) {
	mux.HandleFunc("/api/worktrees", s.handleWorktrees)
	mux.HandleFunc("/api/worktrees/delete", s.handleWorktreeDelete)
	mux.HandleFunc("/api/instances", s.handleInstances)
	mux.HandleFunc("/api/instances/stop", s.handleInstanceStop)
	mux.HandleFunc("/api/instances/log", s.handleInstanceLog)
	mux.HandleFunc("/api/tags", s.handleTags)
	mux.HandleFunc("/api/branches", s.handleBranches)
	mux.HandleFunc("/api/mcp/tools", s.handleMCPTools)
}

func (s *Server) handleBranches(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	def := gitx.DefaultBranch(s.root)
	items, err := gitx.ListLocalBranchesByCommitTime(s.root, 50)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err)
		return
	}

	// Default branch always first; then newest -> oldest, total 10.
	out := make([]gitx.Branch, 0, 10)
	if def != "" {
		for _, b := range items {
			if b.Name == def {
				out = append(out, b)
				break
			}
		}
	}
	for _, b := range items {
		if len(out) >= 10 {
			break
		}
		if def != "" && b.Name == def {
			continue
		}
		out = append(out, b)
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"default":  def,
		"branches": out,
	})
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
	type item struct {
		ID      string `json:"id"`
		Command string `json:"command"`
	}
	items := make([]item, 0, len(m))
	for id, t := range m {
		items = append(items, item{ID: id, Command: t.Command})
	}
	sort.Slice(items, func(i, j int) bool { return items[i].ID < items[j].ID })
	writeJSON(w, http.StatusOK, map[string]any{"tags": items})
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
		}
		if err := readJSON(r.Body, &req); err != nil {
			writeErr(w, http.StatusBadRequest, err)
			return
		}
		item, err := s.worktreeMgr.Create(req.TaskDescription, req.BaseRef)
		if err != nil {
			writeErr(w, http.StatusBadRequest, err)
			return
		}
		writeJSON(w, http.StatusCreated, item)
	default:
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
	}
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

func (s *Server) handleInstances(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		items, err := s.instanceMgr.List()
		if err != nil {
			writeErr(w, http.StatusInternalServerError, err)
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"instances": items})
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
			TagID:      req.TagID,
			Command:    req.Command,
			Name:       req.Name,
		})
		if err != nil {
			writeErr(w, http.StatusBadRequest, err)
			return
		}
		writeJSON(w, http.StatusCreated, item)
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

func (s *Server) handleInstanceLog(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	id := strings.TrimSpace(r.URL.Query().Get("id"))
	body, err := s.instanceMgr.Tail(id, 64*1024)
	if err != nil {
		writeErr(w, http.StatusBadRequest, err)
		return
	}
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	_, _ = io.WriteString(w, body)
}

func (s *Server) handleMCPTools(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"tools": s.mcpAdapter.ToolNames()})
}

func (s *Server) withAuth(next http.Handler) http.Handler {
	if strings.TrimSpace(s.cfg.AuthToken) == "" {
		return next
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !sameOriginHost(r) {
			http.Error(w, "forbidden origin", http.StatusForbidden)
			return
		}
		token := strings.TrimSpace(strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer "))
		if token == "" {
			token = strings.TrimSpace(r.URL.Query().Get("token"))
		}
		if token != s.cfg.AuthToken {
			if !s.allowAuthAttempt(clientIP(r.RemoteAddr)) {
				http.Error(w, "too many unauthorized attempts", http.StatusTooManyRequests)
				return
			}
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		s.resetAuthAttempts(clientIP(r.RemoteAddr))
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
