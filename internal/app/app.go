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
	"strconv"
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
	mux.HandleFunc("/api/instances/archive", s.handleInstanceArchive)
	mux.HandleFunc("/api/instances/delete", s.handleInstanceDelete)
	mux.HandleFunc("/api/instances/log", s.handleInstanceLog)
	mux.HandleFunc("/api/instances/log/stream", s.handleInstanceLogStream)
	mux.HandleFunc("/api/tags", s.handleTags)
	mux.HandleFunc("/api/branches", s.handleBranches)
	mux.HandleFunc("/api/mcp/tools", s.handleMCPTools)
	mux.HandleFunc("/api/mcp/call", s.handleMCPCall)
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
			WorktreeID string            `json:"worktree_id"`
			TagID      string            `json:"tag_id"`
			Command    string            `json:"command"`
			Name       string            `json:"name"`
			Labels     map[string]string `json:"labels"`
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
			Labels:     normalizeLabels(req.Labels),
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

func (s *Server) handleInstanceArchive(w http.ResponseWriter, r *http.Request) {
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
	if err := s.instanceMgr.Archive(req.ID); err != nil {
		writeErr(w, http.StatusBadRequest, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
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
		item, err := s.worktreeMgr.Create(args.TaskDescription, args.BaseRef)
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
		def := gitx.DefaultBranch(s.root)
		items, err := gitx.ListLocalBranchesByCommitTime(s.root, 50)
		if err != nil {
			writeErr(w, http.StatusInternalServerError, err)
			return
		}
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
			WorktreeID string            `json:"worktree_id"`
			TagID      string            `json:"tag_id"`
			Command    string            `json:"command"`
			Name       string            `json:"name"`
			Labels     map[string]string `json:"labels"`
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
			Labels:     normalizeLabels(args.Labels),
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
	case "instance_archive":
		var args struct {
			ID string `json:"id"`
		}
		if err := decodeArgs(req.Args, &args); err != nil {
			writeErr(w, http.StatusBadRequest, err)
			return
		}
		if err := s.instanceMgr.Archive(args.ID); err != nil {
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

func normalizeLabels(in map[string]string) map[string]string {
	if len(in) == 0 {
		return nil
	}
	out := map[string]string{}
	for k, v := range in {
		key := strings.TrimSpace(k)
		val := strings.TrimSpace(v)
		if key == "" || val == "" {
			continue
		}
		out[key] = val
	}
	if len(out) == 0 {
		return nil
	}
	return out
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
