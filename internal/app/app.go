package app

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
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
	"runtime/debug"
	"sort"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"

	"myworktree/internal/gitx"
	"myworktree/internal/instance"
	"myworktree/internal/mcp"
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
}

type Server struct {
	cfg       Config
	logger    *log.Logger
	ln        net.Listener
	mux       *http.ServeMux
	root      string
	dataDir   string
	serverRev string

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

var errInvalidRepoListenPort = errors.New("invalid persisted listen_port")

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
		serverRev:   computeServerRevision(),
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
	if err := ui.Register(mux, filepath.Base(filepath.Clean(s.root))); err != nil {
		return nil, fmt.Errorf("ui.Register: %w", err)
	}

	return s, nil
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

	h := s.withServerRevision(s.withAuth(s.mux))

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

func (s *Server) listTopBranches() (string, []gitx.Branch, error) {
	def := gitx.DefaultBranch(s.root)
	items, err := gitx.ListLocalBranchesByCommitTime(s.root, 50)
	if err != nil {
		return "", nil, err
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
	return def, out, nil
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
	mux.HandleFunc("/api/worktrees/unmanaged", s.handleWorktreesUnmanaged)
	mux.HandleFunc("/api/worktrees/import", s.handleWorktreeImport)
	mux.HandleFunc("/api/worktrees/delete", s.handleWorktreeDelete)
	mux.HandleFunc("/api/worktree/status", s.handleWorktreeStatus)
	mux.HandleFunc("/api/instances", s.handleInstances)
	mux.HandleFunc("/api/instances/reorder", s.handleInstanceReorder)
	mux.HandleFunc("/api/instances/stop", s.handleInstanceStop)
	mux.HandleFunc("/api/instances/restart", s.handleInstanceRestart)
	mux.HandleFunc("/api/instances/archive", s.handleInstanceArchive)
	mux.HandleFunc("/api/instances/delete", s.handleInstanceDelete)
	mux.HandleFunc("/api/instances/purge", s.handleInstancePurge)
	mux.HandleFunc("/api/instances/input", s.handleInstanceInput)
	mux.HandleFunc("/api/instances/tty/ws", s.handleInstanceTTYWS)
	mux.HandleFunc("/api/instances/log", s.handleInstanceLog)
	mux.HandleFunc("/api/instances/log/stream", s.handleInstanceLogStream)
	mux.HandleFunc("/api/tags", s.handleTags)
	mux.HandleFunc("/api/branches", s.handleBranches)
	mux.HandleFunc("/api/mcp/tools", s.handleMCPTools)
	mux.HandleFunc("/api/mcp/call", s.handleMCPCall)
	mux.HandleFunc("/api/main", s.handleMain)
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
	branch, err := gitx.CurrentBranch(s.root)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"name":   name,
		"branch": branch,
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
	items := make([]tag.Tag, 0, len(m))
	for _, t := range m {
		items = append(items, t)
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
			AdoptIfExists   bool   `json:"adopt_if_exists"`
		}
		if err := readJSON(r.Body, &req); err != nil {
			writeErr(w, http.StatusBadRequest, err)
			return
		}
		item, err := s.worktreeMgr.CreateWithOptions(req.TaskDescription, worktree.CreateOptions{BaseRef: req.BaseRef, AdoptIfExists: req.AdoptIfExists})
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

	cmd := gitx.GitCommand(2*time.Second, gitRoot, "diff", "--stat", "HEAD")
	out, err := cmd.Output()
	if err != nil {
		// "No changes" produces empty output; git error messages are non-empty.
		if len(out) == 0 {
			writeJSON(w, http.StatusOK, map[string]any{
				"changes": []any{},
				"total":   map[string]int{"additions": 0, "deletions": 0},
			})
		} else {
			writeErr(w, http.StatusInternalServerError, fmt.Errorf("git diff failed: %s", strings.TrimSpace(string(out))))
		}
		return
	}

	changes, total := parseGitDiffStat(string(out))
	writeJSON(w, http.StatusOK, map[string]any{
		"changes": changes,
		"total":   total,
	})
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
			Root: func() string {
				if req.WorktreeID == instance.MainWorktreeID {
					return s.root
				}
				return ""
			}(),
			TagID:   req.TagID,
			Command: req.Command,
			Name:    req.Name,
			Labels:  normalizeLabels(req.Labels),
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

func (s *Server) handleInstancePurge(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	var req struct {
		WorktreeID string `json:"worktree_id"`
		Version    int64  `json:"version"`
	}
	if err := readJSON(r.Body, &req); err != nil {
		writeErr(w, http.StatusBadRequest, err)
		return
	}
	err := s.instanceMgr.PurgeArchivedInstances(req.WorktreeID, req.Version)
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
	defer conn.Close()

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
				_ = s.instanceMgr.Resize(id, resizeMsg.Cols, resizeMsg.Rows)
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
	case "instance_purge":
		var args struct {
			WorktreeID string `json:"worktree_id"`
			Version    int64  `json:"version"`
		}
		if err := decodeArgs(req.Args, &args); err != nil {
			writeErr(w, http.StatusBadRequest, err)
			return
		}
		if err := s.instanceMgr.PurgeArchivedInstances(args.WorktreeID, args.Version); err != nil {
			if errors.Is(err, store.ErrVersionConflict) {
				st, _ := s.store.Load()
				writeJSON(w, http.StatusConflict, map[string]any{
					"error":   "state changed, please refresh",
					"version": st.Version,
				})
				return
			}
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
	type config struct {
		ListenPort int `json:"listen_port"`
	}
	b, err := os.ReadFile(filepath.Join(dataDir, "server.json"))
	if errors.Is(err, os.ErrNotExist) {
		return 0, nil
	}
	if err != nil {
		return 0, err
	}
	var cfg config
	if err := json.Unmarshal(b, &cfg); err != nil {
		return 0, err
	}
	if cfg.ListenPort < 1 || cfg.ListenPort > 65535 {
		return 0, fmt.Errorf("%w: %d", errInvalidRepoListenPort, cfg.ListenPort)
	}
	return cfg.ListenPort, nil
}

func writeRepoListenPort(dataDir string, port int) error {
	type config struct {
		ListenPort int `json:"listen_port"`
	}
	if err := os.MkdirAll(dataDir, 0o755); err != nil {
		return err
	}
	b, err := json.MarshalIndent(config{ListenPort: port}, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(dataDir, "server.json"), b, 0o600)
}

// parseGitDiffStat parses the output of "git diff --stat HEAD" and returns
// per-file change info and totals.
//
// Git diff --stat format per file line:
//
//	<filename> | <stat>
//
// where <stat> is a mix of numbers and '+'/'-' bar characters, e.g.:
//
//	"2 +-"     (2 lines changed: 1 addition, 1 deletion, bar only)
//	"10 ++++--------"  (10 additions, 8 deletions, bar characters)
//	"1 +"      (1 addition, no deletions)
//	"1 -"      (1 deletion, no additions)
//
// The summary line (e.g. "3 files changed, 5 insertions(+), 3 deletions(-)")
// provides authoritative totals. Per-file bars are proportional but may not
// show exact counts when changes are large.
//
// The summary line's "insertion"/"deletion" labels only appear there, not in
// per-file lines. Binary files show "Bin" instead of a bar.
//
// Parsing strategy:
//   - Split on "| " (unique: filenames cannot contain this exact delimiter).
//   - Extract totals from the summary line's "insertion"/"deletion" labels.
//   - For per-file lines, count '+' and '-' bar characters as proportional
//     counts when inline numbers are absent; use inline numbers when present.
func parseGitDiffStat(output string) ([]map[string]any, map[string]int) {
	var changes []map[string]any
	totalAdds, totalDels := 0, 0

	// Parse the summary line to get authoritative totals.
	// Git wraps at terminal width, so "changed" may appear on a different
	// line than the insertion/deletion counts. We look for "insertion" and
	// "deletion" labels independently in each non-empty line.
	for _, line := range strings.Split(output, "\n") {
		if line == "" {
			continue
		}

		// Extract "N insertion(s)": skip non-digits to land on the last digit,
		// then walk backwards to the first digit, then forward to determine the end.
		if idx := strings.Index(line, "insertion"); idx > 0 {
			i := idx - 1
			for i >= 0 && (line[i] < '0' || line[i] > '9') {
				i--
			}
			for i >= 0 && line[i] >= '0' && line[i] <= '9' {
				i--
			}
			i++
			end := i
			for end < idx && line[end] >= '0' && line[end] <= '9' {
				end++
			}
			if n, err := strconv.Atoi(line[i:end]); err == nil {
				totalAdds = n
			}
		}

		// Extract "N deletion(s)": same approach.
		if idx := strings.Index(line, "deletion"); idx > 0 {
			i := idx - 1
			for i >= 0 && (line[i] < '0' || line[i] > '9') {
				i--
			}
			for i >= 0 && line[i] >= '0' && line[i] <= '9' {
				i--
			}
			i++
			end := i
			for end < idx && line[end] >= '0' && line[end] <= '9' {
				end++
			}
			if n, err := strconv.Atoi(line[i:end]); err == nil {
				totalDels = n
			}
		}
	}

	// Parse per-file lines. Git diff --stat format:
	//  <filename> | <stat>
	// where <stat> is either:
	//   - "Bin OLD -> NEW bytes" for binary files, or
	//   - N [+|−]* for regular files (visual proportional bars, may also
	//     include inline numbers for small files).
	for _, line := range strings.Split(output, "\n") {
		if line == "" {
			continue
		}

		// Split on "| " (filenames cannot contain this exact sequence).
		sep := "| "
		idx := strings.Index(line, sep)
		if idx < 0 {
			continue
		}
		path := strings.TrimSpace(line[:idx])
		statPart := strings.TrimSpace(line[idx+len(sep):])
		if statPart == "" {
			continue
		}

		adds, dels := 0, 0

		if strings.HasPrefix(statPart, "Bin") {
			// Binary file: no line-level stats.
		} else {
			// The stat part is: [N] [+|-]+ (number + proportional bar chars).
			// Find where the number ends by scanning to the first non-digit.
			numEnd := 0
			for numEnd < len(statPart) && statPart[numEnd] >= '0' && statPart[numEnd] <= '9' {
				numEnd++
			}
			// Extract the leading number if present.
			if numEnd > 0 {
				if n, err := strconv.Atoi(statPart[:numEnd]); err == nil {
					adds = n
				}
			}
			// Count bar characters in the remainder (after the number).
			bar := statPart[numEnd:]
			adds = strings.Count(bar, "+")
			dels = strings.Count(bar, "-")
		}

		changes = append(changes, map[string]any{
			"path":      path,
			"additions": adds,
			"deletions": dels,
		})
	}

	return changes, map[string]int{"additions": totalAdds, "deletions": totalDels}
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
