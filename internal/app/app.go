package app

import (
	"bytes"
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
	"myworktree/internal/framework"
	"myworktree/internal/gitx"
	"myworktree/internal/instance/dsh_web"
	"myworktree/internal/instance/opencode_web"
	"myworktree/internal/instance/pty"
	"myworktree/internal/instance/reasonix"
	"myworktree/internal/llm"
	"myworktree/internal/mcp"
	"myworktree/internal/monitor"
	"myworktree/internal/portal"
	"myworktree/internal/redact"
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

	portal  *portal.Portal
	httpSrv *http.Server

	// Independent loopback listener serving only the reasonix reverse proxy,
	// so embedded reasonix pages are cross-origin with the myworktree API
	// (issue #44). Empty when not enabled (TLS mode → mixed content, falls
	// back to the same-origin /rx/ route).
	rxAddr string
	rxSrv  *http.Server

	// rxDriver owns the reasonix serve subprocesses and is shared by the
	// reasonix kind (registered below) and the /rx/ reverse proxy.
	rxDriver *reasonix.Driver

	store       store.FileStore
	worktreeMgr worktree.Manager
	instanceMgr *framework.Manager
	ocScope     *opencode_web.ScopeTracker
	dshScope    *dsh_web.ScopeTracker
	dshWatch    *dsh_web.SessionWatch
	dshDrv      *dsh_web.Driver
	// dshInstallMu serializes the dsh-web "install now" flow
	// (handleInstanceDshInstall): two tabs clicking at once must not
	// run two concurrent `npm install -g`.
	dshInstallMu sync.Mutex
	mcpAdapter   mcp.Adapter
	monitor      monitor.Collector
	authMu       sync.Mutex
	authFails    map[string]authFail
	ttyMu        sync.Mutex
	ttyClients   map[string]map[string]*ttyClientState
	ttyApplied   map[string]ttySize
	ttyNextID    uint64

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

// ttyHandshakeReplayBudget bounds how many bytes ONE TTY WebSocket
// handshake replays before handing the connection to the live output
// subscription (issue #87).
//
// Why a budget at all: the catch-up loop reads in 64KB chunks, and a ring
// buffer is 16-256MB (internal/framework/memsampler.go), so a client that
// reconnects after a long offline window can drive up to 4096 iterations —
// each one taking the ring mutex against the live pumpLogs writer — while
// the user stares at "websocket live" with no output, because the `ready`
// frame already cleared the handshake timer. An unbounded loop turns one
// reconnect into an unbounded stall of the output pump.
//
// Why 8MB: it is 128 x 64KB chunks, so the worst case is bounded at
// ~8MB of wire and ~8MB of ring-mutex serialization per reconnect — a
// fraction of a second on loopback — while still replaying far more output
// than a terminal viewport can hold (a full 8MB scrollback of missed
// output). It is a byte count, not a wall-clock deadline, so the
// truncation point is deterministic and testable rather than machine-speed
// dependent, and it never depends on a live writer being idle.
//
// Truncation is self-healing, not lossy: when the budget runs out the loop
// stops and publishes the end offset of the LAST chunk actually written, so
// the undelivered remainder sits ahead of the published cursor and the next
// reconnect re-requests exactly those bytes — the same property that covers
// the final-read→SubscribeOutput window. Bytes are never skipped, only
// deferred to a later reconnect.
//
// What 8MB costs, because the self-healing framing above can hide it: the
// ring caps are 16-256MB (internal/framework/memsampler.go), so a client
// whose cursor is far behind can have up to 256MB outstanding, and 8MB per
// handshake means up to 32 reconnects to converge. Those deferred bytes
// travel on the NEXT reconnect only — a connection that stays healthy never
// reconnects — so a truncated client keeps a behind screen while its socket
// looks fine. It is strictly better than pre-#87, where no cursor existed
// and every reconnect re-appended the newest 64KB forever and never
// converged, but it is a trade-off, not a free bound. The client appends
// the replay into xterm.js with scrollback: 10000 (index.html), so an 8MB
// replay is far larger than the history it lands in; what scrolls out is
// the replayed content, because the replay is OLDER output and arrives
// BEFORE the live stream — the recent live output still lands at the end
// and stays visible.
//
// The budget is a byte count and NOT a write deadline, and the two are
// different instruments on this path: the budget bounds how much one
// handshake may write in total, while ttyWriteTimeout (armed directly after
// the ready frame, before this loop runs) bounds how long any ONE of those
// writes may block. Neither is redundant with the other, and neither
// livelocks a slow-but-progressing consumer: the budget defers the
// remainder to the next reconnect, and the per-write deadline re-arms on
// every frame, so a consumer that keeps taking bytes keeps resetting it
// while a peer that stopped taking bytes at all is released in 15 s instead
// of pinning this goroutine and ws.Conn's mutex indefinitely (issue #114,
// decision 3 — the write deadline #83 deliberately left out; this is where
// #114 lands it, replay included).
const ttyHandshakeReplayBudget int64 = 8 << 20 // 8 MB

type ttyClientHandle struct {
	server     *Server
	instanceID string
	clientID   string
	once       sync.Once
}

var errInvalidRepoListenPort = errors.New("invalid persisted listen_port")
var errWorktreeNotFound = errors.New("worktree not found")

// dshInstallTimeout bounds the `npm install -g` run in
// handleInstanceDshInstall (a slow network can take minutes; the
// frontend shows progress while awaiting the response).
const dshInstallTimeout = 5 * time.Minute

// truncateText caps a diagnostic string for API error responses (npm
// output can be megabytes; redact.Text only strips secrets).
func truncateText(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "…"
}

// validPublicHost reports whether host is safe to embed in a
// client-derived iframe URL: a plain DNS name, an IPv4 literal, or a
// bracketed IPv6 literal — nothing else (no scheme, userinfo, port,
// path, query or CR/LF). Used by handleInstanceDshInfo before it
// builds an iframe_src from the request Host header.
func validPublicHost(host string) bool {
	if host == "" || len(host) > 253 {
		return false
	}
	if ip := net.ParseIP(host); ip != nil {
		return true
	}
	if strings.HasPrefix(host, "[") && strings.HasSuffix(host, "]") {
		return net.ParseIP(host[1:len(host)-1]) != nil
	}
	for i := 0; i < len(host); i++ {
		c := host[i]
		switch {
		case c >= 'a' && c <= 'z', c >= 'A' && c <= 'Z', c >= '0' && c <= '9', c == '-', c == '.':
		default:
			return false
		}
	}
	return true
}

// numericPort reports whether p is a non-empty all-digit port string
// (used to decide whether a Host header really is host:port before the
// host part is trusted).
func numericPort(p string) bool {
	if p == "" {
		return false
	}
	for i := 0; i < len(p); i++ {
		if p[i] < '0' || p[i] > '9' {
			return false
		}
	}
	return true
}

// dshProxyConfig derives the dsh-web reverse-proxy settings from the
// main listener: loopback bind + no token locally; the main listener's
// host + a mandatory token gate when non-loopback or TLS (the proxy is
// the only boundary between the network and the upstream's own
// browser-session credential — it holds the `dsh-auth-*` cookie,
// auth.go — so the mandatory myworktree token gate layers on top of
// dsh's own auth rather than replacing it). TLS mirrors the main
// certificate so an https page never embeds a mixed-content iframe.
func dshProxyConfig(cfg Config, isSecure bool) dsh_web.ProxyConfig {
	pc := dsh_web.ProxyConfig{
		BindHost: "127.0.0.1",
		TLSCert:  cfg.TLSCert,
		TLSKey:   cfg.TLSKey,
	}
	host, _, err := net.SplitHostPort(cfg.ListenAddr)
	if err != nil || !isLoopbackHost(host) {
		// "" (":port") or an explicit LAN address: the proxy must be
		// reachable wherever the main UI is.
		pc.BindHost = host
		if pc.BindHost == "" {
			pc.BindHost = "0.0.0.0"
		}
		pc.RequireToken = true
	}
	if isSecure {
		pc.RequireToken = true
	}
	if pc.RequireToken {
		pc.AuthToken = cfg.AuthToken
	}
	return pc
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
	globalCfg, _ := config.Load()
	isSecure := cfg.TLSCert != "" && cfg.TLSKey != ""
	// Dedicated kind registry per server: registering into framework.Default
	// would panic on the second New() in the same process (tests). The kinds
	// are explicitly registered here instead of relying on init().
	reg := framework.NewRegistry()
	reg.Register(pty.Driver{})
	reg.Register(opencode_web.Driver{})

	// The reasonix serve driver and its framework kind.
	rxDriver := &reasonix.Driver{
		DataDir: dataDir,
		Logger:  logger,
	}
	reg.Register(reasonix.NewKind(rxDriver))

	// The dsh web driver and its framework kind. The per-instance
	// reverse proxy binds 127.0.0.1 locally (loopback trust model, no
	// token); when the main listener is non-loopback or TLS it binds
	// the main listener's host with a MANDATORY token gate — the proxy
	// is the upstream's sole credential holder (it holds the minted
	// `dsh-auth-*` cookie, auth.go), so this gate layers on top of
	// dsh's own browser-session auth as the only boundary between the
	// LAN and the upstream's credential.
	dshDrv := &dsh_web.Driver{
		DataDir: dataDir,
		Logger:  logger,
	}
	dshScope := dsh_web.NewScopeTracker()
	dshDrv.Tracker = dshScope
	dshDrv.Proxy = dshProxyConfig(cfg, isSecure)
	dshDrv.ProxyStarter = dshDrv.ProxyStarterFn()
	// Foreign-activity advisory over the shared sessions pool (dsh
	// single-writer boundary, see
	// docs/plans/dsh-native-ui/CROSS-PROCESS-SESSION.md): started for
	// the daemon's lifetime; the proxy / bootstrap feed it own-traffic.
	dshWatch := dsh_web.NewSessionWatch(dsh_web.DshSessionsDir(), logger)
	dshWatch.Start()
	dshDrv.SessionWatch = dshWatch
	reg.Register(dshDrv)

	instanceMgr := framework.NewManager(reg, st, logger)
	instanceMgr.DataDir = dataDir
	instanceMgr.Root = root
	instanceMgr.LogBufferBytes = globalCfg.LogBufferBytes
	instanceMgr.AuthToken = cfg.AuthToken
	// Tags drive tag.Env / preStart / Command / Cwd resolution at instance
	// start (global defaults + per-project tags.json) — restored tag
	// semantics shared by every kind.
	if base, err := os.UserConfigDir(); err == nil {
		instanceMgr.Tags = tag.Manager{
			GlobalPath:  filepath.Join(base, "myworktree", "tags.json"),
			ProjectPath: filepath.Join(dataDir, "tags.json"),
		}
	}
	// SetHTTPHandler is wired by Register() below (so the mux exists
	// before any kind tries to register routes).

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
		ocScope:     opencode_web.NewScopeTracker(),
		dshScope:    dshScope,
		dshWatch:    dshWatch,
		dshDrv:      dshDrv,
		rxDriver:    rxDriver,
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
	if n, err := s.instanceMgr.PurgeOrphanLogFiles(); err != nil {
		s.logger.Printf("purge orphan log files failed: %v", err)
	} else if n > 0 {
		s.logger.Printf("purged %d orphan log files", n)
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

	// Independent loopback listener serving only /rx/ (issue #44): embedded
	// reasonix pages become cross-origin with the myworktree API, so content
	// rendered inside the chat iframe cannot silently call /api/* with the
	// user's session.
	//
	// It is enabled only when the main listener is loopback-only: the
	// reported web_url is an absolute http://127.0.0.1:<port>, which is only
	// reachable from the same machine. When the main listener is open to the
	// network (default 0.0.0.0, or an explicit LAN IP) a remote browser would
	// resolve that 127.0.0.1 to ITSELF and the iframe would fail — so we fall
	// back to the same-origin /rx/ route (the frontend then uses the relative
	// path /rx/<id>/, which follows whatever host the browser is on, and LAN
	// access works). TLS mode also falls back (an http iframe inside an https
	// page is blocked as mixed content). In both fallback cases web_url stays
	// empty and the frontend keeps working via /rx/<id>/.
	listenHost, _, _ := net.SplitHostPort(s.cfg.ListenAddr) // "" on error → not loopback → fallback
	if s.cfg.TLSCert == "" && s.cfg.TLSKey == "" && isLoopbackHost(listenHost) {
		rxLn, err := net.Listen("tcp", "127.0.0.1:0")
		if err != nil {
			s.logger.Printf("[reasonix] warning: independent listener failed, falling back to same-origin /rx/: %v", err)
		} else {
			rxMux := http.NewServeMux()
			rxMux.Handle("/rx/", &reasonixProxy{manager: s.instanceMgr, driver: s.rxDriver})
			s.rxSrv = &http.Server{Handler: rxMux}
			s.rxAddr = rxLn.Addr().String()
			go func() {
				// A non-ErrServerClosed failure would leave web_url pointing
				// at a dead port with the iframe failing silently — log it.
				if err := s.rxSrv.Serve(rxLn); err != nil && !errors.Is(err, http.ErrServerClosed) {
					s.logger.Printf("[reasonix] independent listener error: %v", err)
				}
			}()
			s.logger.Printf("[reasonix] independent web-ui listener on %s", s.rxAddr)
		}
	}

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
	// Stop reasonix serve processes first (behavior parity with tty
	// instances, which die when their PTY hangs up): their SSE connections
	// then close, so the HTTP servers below shut down promptly. State dirs
	// (token/port/pid/serve.log) are kept; sessions live in the shared
	// ~/.reasonix pool and are unaffected by instance lifecycle.
	if s.instanceMgr != nil {
		s.instanceMgr.StopAllKind(store.KindReasonix)
		s.instanceMgr.StopAllKind(store.KindDsh)
	}
	if s.dshWatch != nil {
		s.dshWatch.Stop()
	}
	if s.rxSrv != nil {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = s.rxSrv.Shutdown(ctx)
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

	mux.HandleFunc("/api/worktree/file/diff", s.handleWorktreeFileDiff)
	mux.HandleFunc("/api/worktree/file/content", s.handleWorktreeFileContent)
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
	mux.HandleFunc("/api/instances/opencode", s.handleInstanceOpencodeInfo)
	mux.HandleFunc("/api/instances/opencode/scope", s.handleInstanceOpencodeScope)
	mux.HandleFunc("/api/instances/dsh", s.handleInstanceDshInfo)
	mux.HandleFunc("/api/instances/dsh/scope", s.handleInstanceDshScope)
	mux.HandleFunc("/api/instances/dsh/launch", s.handleInstanceDshLaunch)
	mux.HandleFunc("/api/instances/dsh/install", s.handleInstanceDshInstall)
	mux.HandleFunc("/api/dsh/capability", s.handleDshCapability)
	mux.Handle("/rx/", &reasonixProxy{manager: s.instanceMgr, driver: s.rxDriver})
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
	// /__opencode/<id>/ — reverse proxy to opencode web UI.
	// One handler for ALL instances; path format /__opencode/<id>/<rest...>.
	// The handler extracts <id>, looks up the instance, reads host:port
	// from the kind blob, and proxies <rest> to the opencode server with
	// Basic auth injection and ?directory=<worktree> for API paths.
	mux.Handle("/__opencode/", http.StripPrefix("/__opencode", opencode_web.ProxyHandler(s.instanceMgr, s.cfg.AuthToken, s.ocScope)))
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
		"name":       name,
		"branch":     branch,
		"github_url": gitx.GitHubURL(s.root),
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
		// Force deletes even with uncommitted/untracked changes (issue
		// #102). The refusal answer lists what force would destroy —
		// including gitignored files the dirty check never blocks on —
		// so a client that resends with force has seen the cost.
		Force bool `json:"force"`
	}
	if err := readJSON(r.Body, &req); err != nil {
		writeErr(w, http.StatusBadRequest, err)
		return
	}
	ignoredDestroyed, err := s.worktreeMgr.Delete(req.ID, req.Force)
	if err != nil {
		var dirty *worktree.DirtyWorktreeError
		if errors.As(err, &dirty) {
			writeWorktreeDirtyErr(w, dirty)
			return
		}
		writeErr(w, http.StatusBadRequest, err)
		return
	}
	// ignored_destroyed is present only when the count is meaningful: the
	// force path skips the status probe, so there a 0 would read as "none
	// destroyed" while really meaning "not counted" (second review round).
	resp := map[string]any{"status": "ok"}
	if !req.Force {
		resp["ignored_destroyed"] = ignoredDestroyed
	}
	writeJSON(w, http.StatusOK, resp)
}

// writeWorktreeDirtyErr writes the structured 409 answer for a refused
// worktree delete (issue #102). The "error" code "worktree_dirty" is part
// of the API contract — the dashboard matches on it to open the
// dirty-details dialog (summary, collapsible full git status output,
// gitignored-file warning, and the force-delete exit) instead of a
// generic alert. "message" carries the same one-line summary for clients
// that only surface error text.
func writeWorktreeDirtyErr(w http.ResponseWriter, dirty *worktree.DirtyWorktreeError) {
	writeJSON(w, http.StatusConflict, map[string]any{
		"error":   "worktree_dirty",
		"message": dirty.Error(),
		"dirty": map[string]any{
			"entries":     dirty.Entries,
			"deleted":     dirty.Deleted,
			"modified":    dirty.Modified,
			"added":       dirty.Added,
			"renamed":     dirty.Renamed,
			"untracked":   dirty.Untracked,
			"ignored":     dirty.Ignored,
			"first_paths": dirty.FirstPaths,
			"porcelain":   dirty.Porcelain,
		},
	})
}

// resolveWorktreePath returns the filesystem path for a worktree id.
func (s *Server) resolveWorktreePath(id string) (string, error) {
	if id == framework.MainWorktreeID {
		return s.root, nil
	}
	worktrees, err := s.worktreeMgr.List()
	if err != nil {
		return "", err
	}
	for _, wt := range worktrees {
		if wt.ID == id {
			return wt.Path, nil
		}
	}
	return "", fmt.Errorf("unknown worktree id: %s", id)
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
	gitRoot, err := s.resolveWorktreePath(id)
	if err != nil {
		writeErr(w, http.StatusBadRequest, err)
		return
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

	go func() { stagedCh <- runDiff("-c", "core.quotePath=false", "diff", "--cached", "--numstat") }()
	go func() { unstagedCh <- runDiff("-c", "core.quotePath=false", "diff", "--numstat") }()
	go func() {
		out, err := s.gitRunner(2*time.Second, gitRoot, "-c", "core.quotePath=false", "ls-files", "--others", "--exclude-standard")
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
			p = unquoteGitPath(strings.TrimSpace(p))
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

// handleWorktreeFileDiff returns the full git diff for a single file.
func (s *Server) handleWorktreeFileDiff(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	id := strings.TrimSpace(r.URL.Query().Get("id"))
	filePath := strings.TrimSpace(r.URL.Query().Get("path"))
	if id == "" || filePath == "" {
		writeErr(w, http.StatusBadRequest, errors.New("id and path are required"))
		return
	}
	staged := strings.TrimSpace(r.URL.Query().Get("staged")) == "true"

	gitRoot, err := s.resolveWorktreePath(id)
	if err != nil {
		writeErr(w, http.StatusBadRequest, err)
		return
	}
	if !isPathWithin(gitRoot, filePath) {
		http.Error(w, "forbidden", http.StatusForbidden)
		return
	}
	if isSensitiveFile(filePath) {
		http.Error(w, "this file type is not supported for preview", http.StatusForbidden)
		return
	}

	args := []string{"-c", "core.quotePath=false", "diff"}
	if staged {
		args = append(args, "--cached")
	}
	args = append(args, "--", filePath)
	out, err := s.gitRunner(2*time.Second, gitRoot, args...)
	if err != nil {
		msg := strings.TrimSpace(string(out))
		if msg == "" {
			msg = err.Error()
		}
		http.Error(w, msg, http.StatusInternalServerError)
		return
	}
	if len(out) > 500<<10 { // 500KB limit
		http.Error(w, "diff output too large", http.StatusRequestEntityTooLarge)
		return
	}

	// If diff is empty (e.g. new untracked file), read the file and present
	// it as a full-addition diff so the user sees the content.
	if len(out) == 0 {
		fullPath := filepath.Join(gitRoot, filePath)
		// Stat first to avoid reading huge files into memory (same 500KB cap).
		info, err := os.Stat(fullPath)
		if err == nil && info.Size() <= 500<<10 {
			data, err := os.ReadFile(fullPath)
			if err == nil && !isBinaryData(data) {
				lines := strings.Split(string(data), "\n")
				nl := len(lines)
				if nl > 0 && lines[nl-1] == "" {
					lines = lines[:nl-1]
				}
				var buf bytes.Buffer
				fmt.Fprintf(&buf, "diff --git a/%s b/%s\n", filePath, filePath)
				buf.WriteString("new file mode 100644\n")
				buf.WriteString("--- /dev/null\n")
				fmt.Fprintf(&buf, "+++ b/%s\n", filePath)
				fmt.Fprintf(&buf, "@@ -0,0 +1,%d @@\n", len(lines))
				for _, line := range lines {
					buf.WriteByte('+')
					buf.WriteString(line)
					buf.WriteByte('\n')
				}
				// git diff appends this marker when the file lacks a trailing newline
				if len(data) > 0 && data[len(data)-1] != '\n' {
					buf.WriteString("\\ No newline at end of file\n")
				}
				out = buf.Bytes()
				// Re-check after synthesis: header and '+' prefixes can push a
				// borderline file over the 500KB limit.
				if len(out) > 500<<10 {
					http.Error(w, "diff output too large", http.StatusRequestEntityTooLarge)
					return
				}
			}
		}
	}

	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	w.Write(out)
}

// handleWorktreeFileContent returns the raw content of a single file in a worktree.
func (s *Server) handleWorktreeFileContent(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	id := strings.TrimSpace(r.URL.Query().Get("id"))
	filePath := strings.TrimSpace(r.URL.Query().Get("path"))
	if id == "" || filePath == "" {
		writeErr(w, http.StatusBadRequest, errors.New("id and path are required"))
		return
	}

	gitRoot, err := s.resolveWorktreePath(id)
	if err != nil {
		writeErr(w, http.StatusBadRequest, err)
		return
	}
	if !isPathWithin(gitRoot, filePath) {
		http.Error(w, "forbidden", http.StatusForbidden)
		return
	}
	if isSensitiveFile(filePath) {
		http.Error(w, "this file type is not supported for preview", http.StatusForbidden)
		return
	}

	fullPath := filepath.Join(gitRoot, filePath)
	info, err := os.Stat(fullPath)
	if err != nil {
		http.Error(w, "file not found", http.StatusNotFound)
		return
	}
	if info.Size() > 1<<20 {
		http.Error(w, "file too large", http.StatusRequestEntityTooLarge)
		return
	}
	data, err := os.ReadFile(fullPath)
	if err != nil {
		http.Error(w, "failed to read file", http.StatusInternalServerError)
		return
	}
	if isBinaryData(data) {
		http.Error(w, "binary file not supported", http.StatusUnsupportedMediaType)
		return
	}

	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	w.Header().Set("X-File-Type", detectFileType(filePath))
	w.Header().Set("X-File-FullPath", fullPath)
	w.Write(data)
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
			result[framework.MainWorktreeID] = items
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
	if id == framework.MainWorktreeID {
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

// instanceView renders a ManagedInstance for the API, attaching a web_url for
// reasonix instances pointing at the independent listener (issue #44). The
// field is intentionally not part of the persisted store schema.
//
// The map is built explicitly (no JSON round-trip) so the API response shape
// stays stable and cheap; keep the keys and the omitempty conditions in sync
// with ManagedInstance's json tags when the schema grows.
func (s *Server) instanceView(it store.ManagedInstance) map[string]any {
	m := map[string]any{
		"id":          it.ID,
		"worktree_id": it.WorktreeID,
		"tag_id":      it.TagID,
		"name":        it.Name,
		"command":     it.Command,
		"cwd":         it.Cwd,
		"pid":         it.PID,
		"status":      it.Status,
		"created_at":  it.CreatedAt,
	}
	if it.WorktreeName != "" {
		m["worktree_name"] = it.WorktreeName
	}
	if len(it.Env) > 0 {
		m["env"] = it.Env
	}
	if it.Kind != "" {
		m["kind"] = it.Kind
	}
	if len(it.Extra) > 0 {
		m["extra"] = it.Extra
	}
	if len(it.KindBlob) > 0 {
		m["kind_blob"] = json.RawMessage(it.KindBlob)
	}
	if it.LastError != "" {
		m["last_error"] = it.LastError
	}
	if it.ExitCode != 0 {
		m["exit_code"] = it.ExitCode
	}
	if it.RestartedFrom != "" {
		m["restarted_from"] = it.RestartedFrom
	}
	if it.RestartedTo != "" {
		m["restarted_to"] = it.RestartedTo
	}
	if it.StoppedAt != "" {
		m["stopped_at"] = it.StoppedAt
	}
	if it.Kind == store.KindReasonix {
		if u := s.reasonixWebURL(it.ID); u != "" {
			m["web_url"] = u
		}
	}
	return m
}

// reasonixWebURL returns the cross-origin base for a reasonix instance's web
// UI, or "" when the independent listener is not enabled (TLS mode, or a
// non-loopback main listener — see Server.Start), in which case the frontend
// falls back to the same-origin /rx/<id>/ route.
func (s *Server) reasonixWebURL(id string) string {
	if s.rxAddr == "" {
		return ""
	}
	return "http://" + s.rxAddr + "/rx/" + id + "/"
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
		view := make([]map[string]any, 0, len(items))
		for _, it := range items {
			view = append(view, s.instanceView(it))
		}
		writeJSON(w, http.StatusOK, map[string]any{"instances": view, "version": version})
	case http.MethodPost:
		var req struct {
			WorktreeID string `json:"worktree_id"`
			TagID      string `json:"tag_id"`
			Command    string `json:"command"`
			Name       string `json:"name"`
			Kind       string `json:"kind"`
		}
		if err := readJSON(r.Body, &req); err != nil {
			writeErr(w, http.StatusBadRequest, err)
			return
		}
		item, err := s.instanceMgr.Start(r.Context(), framework.StartParams{
			WorktreeID: req.WorktreeID,
			Root: func() string {
				if req.WorktreeID == framework.MainWorktreeID {
					return s.root
				}
				return ""
			}(),
			TagID:   req.TagID,
			Command: req.Command,
			Kind:    req.Kind,
			Name:    req.Name,
		})
		if err != nil {
			var budgetErr *framework.LogBufferBudgetError
			if errors.As(err, &budgetErr) {
				writeLogBufferBudgetErr(w, budgetErr)
				return
			}
			writeErr(w, http.StatusBadRequest, err)
			return
		}
		writeJSON(w, http.StatusCreated, s.instanceView(item))
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
			if errors.Is(err, framework.ErrInstanceNotFound) {
				writeErr(w, http.StatusNotFound, err)
				return
			}
			writeErr(w, http.StatusBadRequest, err)
			return
		}
		writeJSON(w, http.StatusOK, s.instanceView(updated))
	default:
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
	}
}

// handleInstanceOpencodeInfo returns iframe src and metadata for an opencode-web instance.
func (s *Server) handleInstanceOpencodeInfo(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	id := strings.TrimSpace(r.URL.Query().Get("id"))
	if id == "" {
		writeErr(w, http.StatusBadRequest, errors.New("id is required"))
		return
	}
	inst, err := s.instanceMgr.Get(id)
	if err != nil {
		if errors.Is(err, framework.ErrInstanceNotFound) {
			writeErr(w, http.StatusNotFound, err)
			return
		}
		writeErr(w, http.StatusInternalServerError, err)
		return
	}
	if inst.Kind != "opencode-web" {
		writeErr(w, http.StatusNotFound, errors.New("not an opencode-web instance"))
		return
	}

	// Connection info is in the per-kind blob (new code path) with
	// the legacy Extra map as a fallback for pre-refactor state.json
	// files. The blob is JSON {host, port, worktree_abs, url_path}.
	var host, port, worktreeAbs string
	var version string
	var versionSupported bool
	if len(inst.KindBlob) > 0 {
		var b struct {
			Host             string `json:"host"`
			Port             string `json:"port"`
			WorktreeAbs      string `json:"worktree_abs"`
			Version          string `json:"version"`
			VersionSupported bool   `json:"version_supported"`
		}
		if json.Unmarshal(inst.KindBlob, &b) == nil {
			host = b.Host
			port = b.Port
			worktreeAbs = b.WorktreeAbs
			version = b.Version
			versionSupported = b.VersionSupported
		}
	}
	// Fall back to legacy Extra for pre-refactor state.json compat.
	if host == "" {
		host = inst.Extra["host"]
	}
	if port == "" {
		port = inst.Extra["port"]
	}
	if worktreeAbs == "" {
		worktreeAbs = inst.Extra["worktree_abs"]
	}
	if worktreeAbs == "" {
		worktreeAbs = inst.Cwd
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"iframe_src":        "/__opencode/" + id + "/",
		"api_base":          "/__opencode/" + id,
		"worktree_path":     worktreeAbs,
		"host":              host,
		"port":              port,
		"version":           version,
		"version_supported": versionSupported,
	})
}

// handleInstanceOpencodeScope returns the current out-of-scope state for an
// opencode-web instance. The reverse proxy records directory-bearing requests
// in the in-memory ScopeTracker; this endpoint lets the frontend poll it to
// render the persistent warning bar.
func (s *Server) handleInstanceOpencodeScope(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	id := strings.TrimSpace(r.URL.Query().Get("id"))
	if id == "" {
		writeErr(w, http.StatusBadRequest, errors.New("id is required"))
		return
	}
	inst, err := s.instanceMgr.Get(id)
	if err != nil {
		if errors.Is(err, framework.ErrInstanceNotFound) {
			writeErr(w, http.StatusNotFound, err)
			return
		}
		writeErr(w, http.StatusInternalServerError, err)
		return
	}
	if inst.Kind != "opencode-web" {
		writeErr(w, http.StatusNotFound, errors.New("not an opencode-web instance"))
		return
	}
	state, ok := s.ocScope.Get(id)
	if !ok {
		state = opencode_web.ScopeState{Scope: opencode_web.ScopeInScope}
	}
	writeJSON(w, http.StatusOK, state)
}

// handleInstanceDshInfo returns iframe src and metadata for a dsh-web
// instance. The iframe loads the per-instance reverse-proxy listener
// (a dedicated origin — the dsh SPA hardcodes its API base to
// location.origin + '/api'); in remote mode the listener binds a
// non-loopback host with a mandatory token gate, so the returned
// iframe_src uses the caller-visible host and the frontend appends the
// token (PLAN.md §嵌入形态).
func (s *Server) handleInstanceDshInfo(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	id := strings.TrimSpace(r.URL.Query().Get("id"))
	if id == "" {
		writeErr(w, http.StatusBadRequest, errors.New("id is required"))
		return
	}
	inst, err := s.instanceMgr.Get(id)
	if err != nil {
		if errors.Is(err, framework.ErrInstanceNotFound) {
			writeErr(w, http.StatusNotFound, err)
			return
		}
		writeErr(w, http.StatusInternalServerError, err)
		return
	}
	if inst.Kind != store.KindDsh {
		writeErr(w, http.StatusNotFound, errors.New("not a dsh-web instance"))
		return
	}

	var b dsh_web.Blob
	if len(inst.KindBlob) > 0 {
		_ = json.Unmarshal(inst.KindBlob, &b)
	}
	worktreeAbs := b.WorktreeAbs
	if worktreeAbs == "" {
		worktreeAbs = inst.Cwd
	}

	resp := map[string]any{
		"iframe_src":         b.IframeURL,
		"proxy_host":         b.ProxyHost,
		"proxy_port":         b.ProxyPort,
		"host":               b.Host,
		"port":               b.Port,
		"worktree_path":      worktreeAbs,
		"version":            b.Version,
		"version_supported":  b.VersionSupported,
		"remote_capable":     b.RemoteCapable,
		"min_remote_version": dsh_web.RemoteMinVersion(),
		"overlay_verified":   b.OverlayVerified,
	}

	// Remote-mode iframe src: the caller-visible host (the listener
	// binds 0.0.0.0 / the LAN host — never a URL a browser can use
	// verbatim), scheme mirrors the main listener's TLS. The frontend
	// appends ?token= when the token gate is on.
	//
	// The 0.0.0.0 / :: case must derive the host from the request
	// (r.Host): those bind addresses are not routable, and r.Host is
	// exactly what the caller used to reach this listener. Because
	// that value is client-controlled, it is validated before being
	// embedded in the iframe URL — anything that is not a plain DNS
	// name or IP (schemes, userinfo, paths, CR/LF) yields no
	// iframe_src (the frontend stays on the "starting" placeholder).
	if b.ProxyHost != "" && b.ProxyPort != "" {
		proxyHost := b.ProxyHost
		if proxyHost == "0.0.0.0" || proxyHost == "::" {
			// Only trust a host:port split whose port is numeric;
			// anything else (path, userinfo, scheme junk) falls through
			// to whole-string validation and is rejected.
			if h, p, herr := net.SplitHostPort(r.Host); herr == nil && numericPort(p) {
				proxyHost = h
			} else {
				proxyHost = r.Host
			}
		}
		if validPublicHost(proxyHost) {
			scheme := "http"
			if s.isSecure {
				scheme = "https"
			}
			src := scheme + "://" + net.JoinHostPort(proxyHost, b.ProxyPort) + "/"
			// Remote mode: the per-instance proxy's token gate requires
			// the FIRST iframe navigation to carry ?token= (the embed is
			// a dedicated origin — the main-origin HttpOnly mw_token
			// cookie cannot travel to it, and JS can never read that
			// cookie to append the token itself). The server owns both
			// facts — the gate is on (RequireToken) and the token value —
			// so it appends the token here. The token read is the PROXY's
			// own AuthToken (Proxy.AuthToken), not cfg.AuthToken: that is
			// exactly the credential checkToken validates against, so the
			// URL stays correct even if the main token and the proxy
			// token are ever split. This handler sits behind withAuth
			// (loopback bypass included), so only authenticated clients
			// receive it; the proxy then validates, syncs its own HttpOnly
			// cookie, and 302-redirects to the token-free URL, so the
			// embedded document never retains the token in its
			// location.search (ARCHITECTURE §9 checklist).
			if s.dshDrv != nil && s.dshDrv.Proxy.RequireToken && strings.TrimSpace(s.dshDrv.Proxy.AuthToken) != "" {
				src += "?token=" + url.QueryEscape(s.dshDrv.Proxy.AuthToken)
			}
			resp["iframe_src"] = src
			resp["proxy_host"] = proxyHost
		} else {
			// The blob's IframeURL still names the unroutable bind
			// address; withhold it so the frontend stays on the
			// "starting" placeholder instead of a dead URL.
			s.logger.Printf("dsh-web instance %s: derived proxy host %q failed validation; iframe_src withheld", id, proxyHost)
			resp["iframe_src"] = ""
		}
	}

	// Missing-dsh state (recomputed on the fly): the instance failed at
	// spawn because no dsh executable could be resolved; the frontend
	// dialog needs structured facts, and a recheck also clears the
	// dialog once the user installs dsh.
	if inst.Status == "failed" {
		if _, lerr := exec.LookPath("dsh"); lerr != nil {
			resp["missing_dsh"] = map[string]any{
				"npm_available": dsh_web.NpmAvailable(),
				"suggested_pin": dsh_web.NpxPin,
			}
		}
	}

	writeJSON(w, http.StatusOK, resp)
}

// handleInstanceDshScope returns the current out-of-scope state for a
// dsh-web instance, recorded in-memory by the per-instance reverse
// proxy from RPC request bodies (session/create / workspace/create,
// dsh 0.2.x slash-style endpoints).
func (s *Server) handleInstanceDshScope(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	id := strings.TrimSpace(r.URL.Query().Get("id"))
	if id == "" {
		writeErr(w, http.StatusBadRequest, errors.New("id is required"))
		return
	}
	inst, err := s.instanceMgr.Get(id)
	if err != nil {
		if errors.Is(err, framework.ErrInstanceNotFound) {
			writeErr(w, http.StatusNotFound, err)
			return
		}
		writeErr(w, http.StatusInternalServerError, err)
		return
	}
	if inst.Kind != store.KindDsh {
		writeErr(w, http.StatusNotFound, errors.New("not a dsh-web instance"))
		return
	}
	state, ok := s.dshScope.Get(id)
	if !ok {
		state = dsh_web.ScopeState{Scope: dsh_web.ScopeInScope}
	}
	// Foreign-activity advisory (dsh single-writer boundary): sessions
	// in the shared pool actively written by another dsh process —
	// opening them from the embed can corrupt their log. Record-only;
	// the frontend renders the warning bar.
	if s.dshWatch != nil {
		if n, ids := s.dshWatch.ForeignActive(); n > 0 {
			state.ForeignActiveSessions = ids
		}
	}
	writeJSON(w, http.StatusOK, state)
}

// handleInstanceDshLaunch persists the launch mode (path / npx /
// install) for a dsh-web instance's worktree — the missing-dependency
// dialog's choice, applied at the next Start (PLAN.md §缺失依赖).
func (s *Server) handleInstanceDshLaunch(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if !s.dshWriteOriginGuard(w, r) {
		return
	}
	var req struct {
		ID   string `json:"id"`
		Mode string `json:"mode"`
	}
	if err := readJSON(r.Body, &req); err != nil {
		writeErr(w, http.StatusBadRequest, err)
		return
	}
	inst, err := s.requireDshInstance(req.ID)
	if err != nil {
		writeErr(w, http.StatusNotFound, err)
		return
	}
	if s.dshDrv == nil {
		writeErr(w, http.StatusInternalServerError, errors.New("dsh driver not configured"))
		return
	}
	if err := s.dshDrv.SetLaunchMode(inst.Cwd, req.Mode); err != nil {
		writeErr(w, http.StatusBadRequest, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok", "mode": req.Mode})
}

// handleInstanceDshInstall runs `npm install -g @deepseek-ai/dsh` on
// behalf of the user, then records the resolved global bin path
// (npm prefix -g) so the next Start spawns it directly — no daemon
// restart needed. Bounded by a generous timeout; the frontend shows
// progress while awaiting the response.
func (s *Server) handleInstanceDshInstall(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if !s.dshWriteOriginGuard(w, r) {
		return
	}
	var req struct {
		ID string `json:"id"`
	}
	if err := readJSON(r.Body, &req); err != nil {
		writeErr(w, http.StatusBadRequest, err)
		return
	}
	inst, err := s.requireDshInstance(req.ID)
	if err != nil {
		writeErr(w, http.StatusNotFound, err)
		return
	}
	if s.dshDrv == nil {
		writeErr(w, http.StatusInternalServerError, errors.New("dsh driver not configured"))
		return
	}

	// Serialize installs: the endpoint rewrites the global npm prefix,
	// and two tabs clicking "install now" at once must not race each
	// other (or the recorded bin path).
	s.dshInstallMu.Lock()
	defer s.dshInstallMu.Unlock()

	installCtx, cancel := context.WithTimeout(r.Context(), dshInstallTimeout)
	defer cancel()
	out, err := exec.CommandContext(installCtx, "npm", "install", "-g", "@deepseek-ai/dsh").CombinedOutput()
	if err != nil {
		// npm failure output can be huge (private-registry URLs, lock
		// dumps); cap it before it reaches the frontend. redact.Text
		// strips secrets first.
		writeErr(w, http.StatusBadGateway, fmt.Errorf("npm install failed: %w: %s", err, truncateText(strings.TrimSpace(redact.Text(string(out))), 300)))
		return
	}
	prefixOut, err := exec.CommandContext(installCtx, "npm", "prefix", "-g").Output()
	if err != nil {
		writeErr(w, http.StatusBadGateway, fmt.Errorf("npm prefix -g failed: %w", err))
		return
	}
	bin := filepath.Join(strings.TrimSpace(string(prefixOut)), "bin", "dsh")
	if err := s.dshDrv.SetInstallBin(inst.Cwd, bin); err != nil {
		writeErr(w, http.StatusInternalServerError, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"status":       "ok",
		"resolved_bin": bin,
	})
}

// handleDshCapability reports the dsh toolchain capability for a
// worktree (?worktree=<id>) without spawning an instance — the
// frontend consults it to decide whether remote mode can be offered.
// Resolution is store-backed (resolveWorktreePath): only registered
// worktree ids (or the main-worktree sentinel) resolve, so an
// arbitrary / traversal id can never reach the filesystem. A missing
// dsh is a structured 200 (missing + npm_available + suggested_pin),
// not an error — the frontend drives the install dialog off it.
func (s *Server) handleDshCapability(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	id := strings.TrimSpace(r.URL.Query().Get("worktree"))
	if id == "" {
		writeErr(w, http.StatusBadRequest, errors.New("worktree is required"))
		return
	}
	path, err := s.resolveWorktreePath(id)
	if err != nil {
		writeErr(w, http.StatusNotFound, err)
		return
	}
	if s.dshDrv == nil {
		writeErr(w, http.StatusInternalServerError, errors.New("dsh driver not configured"))
		return
	}
	capab := s.dshDrv.ProbeCapability(path)
	writeJSON(w, http.StatusOK, map[string]any{
		"mode":               capab.Mode,
		"version":            capab.Version,
		"remote_capable":     capab.RemoteCapable,
		"min_remote_version": capab.MinRemoteVersion,
		"missing":            capab.Missing,
		"npm_available":      capab.NpmAvailable,
		"suggested_pin":      capab.SuggestedPin,
	})
}

// dshWriteOriginGuard blocks cross-origin writes to the dsh install /
// launch endpoints. withAuth skips Origin checks for loopback clients
// (local trust), but a hostile webpage can drive a loopback fetch
// (no-cors simple POST) — browser requests always carry Origin, while
// non-browser clients (curl, the CLI) omit it and stay allowed
// (REVIEW-2026-08-16 MED-6: CSRF → arbitrary `npm install -g`).
func (s *Server) dshWriteOriginGuard(w http.ResponseWriter, r *http.Request) bool {
	if isLoopbackRequest(r) {
		if origin := r.Header.Get("Origin"); origin != "" && !sameOriginHost(r) {
			http.Error(w, "forbidden origin", http.StatusForbidden)
			return false
		}
	}
	return true
}

// requireDshInstance loads an instance and verifies its kind.
func (s *Server) requireDshInstance(id string) (store.ManagedInstance, error) {
	id = strings.TrimSpace(id)
	if id == "" {
		return store.ManagedInstance{}, errors.New("id is required")
	}
	inst, err := s.instanceMgr.Get(id)
	if err != nil {
		return store.ManagedInstance{}, err
	}
	if inst.Kind != store.KindDsh {
		return store.ManagedInstance{}, errors.New("not a dsh-web instance")
	}
	return inst, nil
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
	writeJSON(w, http.StatusCreated, s.instanceView(item))
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

// sinceFollowLiveEnd is the `since` sentinel meaning "send nothing, publish
// the current end offset, then stream only new data" (issue #86).
//
// A client reaches it when its screen is ALREADY painted with the ring
// buffer's tail but it never learned that tail's offset — the usual cause
// being a reverse proxy that strips `X-Log-Offset` from the
// GET /api/instances/log response. The plain-unknown sentinel (-1, an
// omitted `since`) means the opposite: nothing is painted, so the client
// genuinely wants the tail. Collapsing the two into one -1 is this bug:
// loadLog paints the tail, the stream serves the tail again, and the tail
// renders twice.
//
// parseInt64Default passes any negative value through untouched, so each
// sentinel must be branched on EXPLICITLY, ahead of `since < 0` — a -2
// that fell into a tail branch would behave exactly like an omitted `since`
// and reproduce the duplication this value exists to prevent. Consumers:
// handleInstanceLogStream (SSE) and completeHandshake (TTY WS). The client
// side of the contract is CURSOR_FOLLOW_LIVE_END in index.html /
// kinds/pty.js, which is what puts the value on the wire.
const sinceFollowLiveEnd int64 = -2

// Liveness knobs for the TTY WebSocket path (issue #83).
//
// The problem: a half-open TCP socket — laptop sleep, NAT/proxy idle
// timeout, a silently swapped network path — never delivers FIN or RST,
// so ReadMessage blocks forever, the browser fires no onclose, and the
// handler goroutine, the reader goroutine, the fd and the ttyClients
// entry pin the daemon for its entire lifetime. Nothing on this path
// generated traffic while the terminal was idle, so nothing ever noticed.
//
// The fix is a heartbeat the peer answers WITHOUT any app-layer code:
// every ttyPingInterval the handler writes an RFC 6455 ping (browsers
// auto-reply Pong from their network stack) plus a TEXT {"type":"ping"}
// control frame (onmessage never fires for control frames, so the text
// frame is the only heartbeat browser JavaScript can observe).
//
// The numbers, and the margin between them:
//
//		ttyPingInterval (10s)  →  4.5 ticks fit inside ttyReadDeadline (45s)
//		client threshold (30s) =  3 × ttyPingInterval; < ttyReadDeadline
//
//	  - 4.5×: a healthy peer refreshes the read deadline with an automatic
//	    Pong on EVERY ping, so expiry needs ~4.5 consecutive missed ping
//	    rounds — peer-dead, not peer-busy. A shell at a prompt, vim, less
//	    or top emit zero OUTPUT for hours but still answer pings, which is
//	    exactly why liveness rides on heartbeat traffic and never on the
//	    absence of output.
//	  - The client-side staleness threshold the browser pairs with these
//	    numbers is 3 × ttyPingInterval (30s, set in index.html): a
//	    background tab's setInterval is commonly throttled to ~1/min while
//	    WebSocket message delivery is NOT — the server's frames keep the
//	    client's lastDataAt fresh there, so the client threshold needs
//	    margin over the ping interval (delivery jitter, TCP retransmits),
//	    not over the throttled watchdog tick. A short server interval is
//	    therefore safe: it cannot cause background-tab reconnect churn.
//	  - client threshold < ttyReadDeadline: the browser's watchdog reaps a
//	    half-open socket first and reconnects on its own terms (keeping
//	    its #87 byte cursor); the server deadline is the backstop that
//	    caps the goroutine/fd/ttyClients leak at ~45s of inbound silence
//	    for peers that run no watchdog at all (stale cached pages,
//	    scripts, clients that half-close and go silent).
const (
	ttyPingInterval = 10 * time.Second
	ttyReadDeadline = 45 * time.Second
	// defaultTTYWriteTimeout is the fallback that bounds every write on the
	// TTY socket from the ready frame onward — the handshake replay writes
	// included (issue #114, decision 3 — the write deadline #83 deliberately
	// left out and #114 finally lands). It is the fallback rather than the
	// value: MYWORKTREE_TTY_WRITE_TIMEOUT_MS (envTTYWriteTimeoutMS) moves it,
	// resolved once per connection by ttyWriteTimeout() and armed at the one
	// call site below. The knob exists because "past 15 s the link is dead"
	// is a false binary (issue #114 review R2): on a 50 KB/s link with 2%
	// loss a single 64 KB write legitimately blocks longer than 15 s while
	// the connection recovers, and killing that connection replays into the
	// same stall — a deadline-induced loop. An operator on such a link raises
	// the bound to 30 s without a code change; a missing, unparsable, zero or
	// negative value lands back here, on this documented default.
	//
	// It is deliberately LONGER than ttyPingInterval: a
	// healthy peer re-arms it with its own traffic every 10 s and never
	// trips it, so the bound is invisible until a peer truly stalls — and
	// then it turns the write that used to pin this goroutine, ws.Conn's
	// mutex, and every frame behind it (heartbeats included, the issue #114
	// amplification) into a bounded stall that errors and unwinds the
	// handler through its existing defers. (On a `slow-link` connection the
	// heartbeat sits at ttyHeartbeatIntervalSlowLink, level with this bound:
	// the deadline is re-armed at the START of every write, so what it
	// measures is one blocked write, never the gap between two frames, and
	// the heartbeat frames themselves are a handful of bytes.)
	//
	// Arming sits right after the ready frame rather than after the
	// subscription because the replay is the larger exposure: up to 8 MB
	// (ttyHandshakeReplayBudget) written to a socket whose peer may already
	// be gone, with no subscription yet to bound it. A dead consumer there
	// pinned the handler and ws.Conn's mutex for as long as the OS let the
	// write block — the review's amplifier — and "armed after SubscribeOutput"
	// left that whole window unbounded. Per-write semantics are unchanged
	// (writeFrame re-arms at now+d on EVERY frame), so a slow but progressing
	// replay is never mistaken for a stall: only a peer that stops taking
	// bytes for a full timeout period trips. The explicit 5 s teardown
	// deadline keeps its tighter documented bound — that path disarms this
	// one first.
	defaultTTYWriteTimeout = 15 * time.Second
	// ttyDropLogInterval is the floor between two overflow log lines on one
	// connection (issue #114 review, minor). A drop storm produces one
	// notice per live chunk, and logging every one of them floods the log
	// while the peer that cannot take bytes is exactly the peer whose
	// congestion that log makes worse. The FIRST increment of every
	// connection still logs immediately — an operator's "did this connection
	// drop anything?" question is answered at the first drop — and later
	// ones are sampled at this rate. A sampled line carries ITS OWN
	// increment, not the sum of the suppressed ones: the log is a sample,
	// the wire notices are the complete record. The log gate and the wire
	// gate are two independent knobs — ttyOverflowNoticeInterval below is
	// the wire's, and neither one's rate bounds the other's.
	ttyDropLogInterval = 500 * time.Millisecond
	// ttyOverflowNoticeInterval is the floor between two overflow NOTICE
	// frames on one connection (issue #114, decision 1 补 1 / review R5).
	// The wire notice used to ride every single drop increment, which in a
	// storm is one TEXT frame per live chunk — ~150 frames/s at the
	// measured 150 KB/s burst, each one taking ws.Conn's write mutex away
	// from the binary chunk it precedes (writeFrame serialises on c.mu):
	// the notice was itself an amplifier of the congestion it reported.
	// So: the FIRST drop increment of a connection still notices immediately,
	// and after that at most one frame per interval — four per second —
	// carrying the SUM of every increment since the last notice.
	//
	// Why a ≤250 ms deferral is safe, and what it costs nobody:
	//   - what gets dropped is always the OLDEST undelivered chunk (the
	//     queue head: broadcast evicts the head to free a slot), so the
	//     running total is exact at every instant; aggregation only groups
	//     increments that are already final, it never estimates them;
	//   - the client's response to a notice is reset + tail (or cursor +
	//     Σdropped), and both are idempotent w.r.t. a notice arriving
	//     ≤250 ms late: the reset repaints, and a transient hole in the
	//     scrollback self-heals through that same repaint;
	//   - if output stops before a pending aggregate goes out, the pending
	//     counts cover nothing the screen still needs: the drops are behind
	//     the newest delivered bytes, and the bytes the client did receive
	//     stay contiguous, so there is no hole left uncovered by a frame
	//     that never arrived.
	// The counts themselves never depend on this gate: the baseline advances
	// on EVERY sample whether or not a frame goes out (see the live loop),
	// so no increment is ever lost, only grouped.
	ttyOverflowNoticeInterval = 250 * time.Millisecond
	// ttyHeartbeatIntervalSlowLink is the widened heartbeat for a client
	// that declared `slow-link` (issue #114, decision 3(c)/5c): 15 s instead
	// of ttyPingInterval's 10 s, with the RFC 6455 ping and the TEXT
	// {"type":"ping"} frame riding the same ticker as always, so the two
	// cannot drift. Rationale (ORCA relay runs 15 s/75s): on a 300 ms RTT
	// with 1-5% loss the 10 s tick spends a measurable share of the
	// connection's budget on heartbeat frames and their write-lock time,
	// while a 5:1 heartbeat-to-staleness ratio still detects a dead peer
	// well inside the client's own window. The client side of the pair —
	// the 60 s stale threshold — is Phase B; the server changes only its
	// transmit rate here. A connection that does NOT declare `slow-link`
	// keeps 10 s/45 s VERBATIM, and so does every compressed test seam:
	// see ttyHeartbeatIntervalFor.
	ttyHeartbeatIntervalSlowLink = 15 * time.Second
)

// The operator-tunable TTY knobs (issue #114, decision 5c; names frozen in
// Phase A per review R13 and documented in docs/API.md). Each is resolved
// from the environment ONCE PER CONNECTION at handshake time — never per
// frame, and never into a package-level value: a handshake is one env read
// against a connection that then serves thousands of frames, so the read is
// free, the operator's most recent setting always wins without a restart,
// and no connection inherits a value captured before it was configured.
// Same shape as pty.subscriberQueueChunksDrop, the sibling knob of this
// issue (internal/instance/pty/driver.go), for the same reasons.
const (
	// envTTYWriteTimeoutMS overrides defaultTTYWriteTimeout in milliseconds
	// (see there for why the knob exists). Missing, unparsable, zero or
	// negative yields the default — a timeout is not a knob worth failing a
	// handshake over.
	envTTYWriteTimeoutMS = "MYWORKTREE_TTY_WRITE_TIMEOUT_MS"

	// envTTYReplayWindowBytes sets DEFAULT replay window W (decision 4/5b):
	// the window bounds both the replay's START (a `since` older than
	// head−W is clamped forward) and the TOTAL bytes one handshake replay
	// may write. Default 1 MiB — the round-number reading of the plan's 1 MB
	// line-loss budget, and comfortably inside the 8 MB ttyHandshakeReplayBudget
	// that backstops it. Cross-border links measured at 50-100 KB/s want
	// 256-512 KB (decision 5b). W is a LINE-LOSS budget, not a scrollback
	// line count: bytes↔lines is non-linear under ANSI, so 1 MiB is an
	// engineering estimate (10000 rows × ~100 B), never a provable
	// equivalence. Missing, unparsable, zero or negative yields
	// defaultTTYReplayWindowBytes; W == 0 is legal and means pure tail.
	envTTYReplayWindowBytes = "MYWORKTREE_TTY_REPLAY_WINDOW_BYTES"

	// defaultTTYReplayWindowBytes is 1 MiB, the W of decision 4.
	defaultTTYReplayWindowBytes int64 = 1 << 20 // 1048576
)

// ttyWriteTimeout resolves the per-write deadline for ONE connection from
// MYWORKTREE_TTY_WRITE_TIMEOUT_MS, falling back to defaultTTYWriteTimeout on
// anything missing, unparsable, zero or negative. A negative value is a
// fallback and not a passthrough (unlike `since`, where negatives are
// meaningful sentinels): a deadline of now-d in the past would fail every
// write instantly, and "0" read as "no deadline" would silently remove the
// bound #114 exists to install.
func ttyWriteTimeout() time.Duration {
	raw, ok := os.LookupEnv(envTTYWriteTimeoutMS)
	if !ok {
		return defaultTTYWriteTimeout
	}
	ms, err := strconv.ParseInt(strings.TrimSpace(raw), 10, 64)
	if err != nil || ms <= 0 {
		return defaultTTYWriteTimeout
	}
	return time.Duration(ms) * time.Millisecond
}

// ttyReplayWindowDefaultBytes resolves the OPERATOR's default W from
// MYWORKTREE_TTY_REPLAY_WINDOW_BYTES: missing, unparsable, zero or negative
// all land on defaultTTYReplayWindowBytes. Zero is a fallback here and NOT a
// request for pure tail (the asymmetry against the client's cap is deliberate:
// an operator who types 0 into a systemd unit almost certainly did not mean
// "stop replaying terminals", while a client that puts replay-window=0 in a
// query string is asking for exactly that, in the field that means it — see
// ttyReplayWindowBytes). A value above ttyHandshakeReplayBudget is left
// alone here and clamped there, so the ceiling lives in one place.
func ttyReplayWindowDefaultBytes() int64 {
	raw, ok := os.LookupEnv(envTTYReplayWindowBytes)
	if !ok {
		return defaultTTYReplayWindowBytes
	}
	n, err := strconv.ParseInt(strings.TrimSpace(raw), 10, 64)
	if err != nil || n <= 0 {
		return defaultTTYReplayWindowBytes
	}
	return n
}

// ttyReplayWindowBytes resolves W_eff — the effective replay window of ONE
// connection — from the two sources decision 4/5b names, and only those:
//
//	W_eff = min(env default, the client's `replay-window=<bytes>` claim)
//
// The client's claim may only SHRINK the window: a declared value at or
// above the default is ignored rather than honoured, because the env default
// is the operator's ceiling and a client that asks for 512 MB of replay on a
// cross-border link would otherwise buy exactly the multi-minute, screen-
// freezing replay the window exists to bound (decision 5b: 只许缩小, anti-DoS).
// An unparsable, negative or empty claim is ignored the same way — the
// connection keeps the default and is never refused for a malformed token.
// W == 0 IS honoured: pure tail is a legitimate request.
//
// The final clamp to ttyHandshakeReplayBudget keeps W ≤ budget TRUE BY
// CONSTRUCTION even when an operator points the env at something absurd (an
// 8 MB+ window would otherwise let the loop run past the budget it is meant
// to sit inside): the budget stays the single hard backstop above W, which
// is what makes the budget guard in completeHandshake unreachable in normal
// operation while keeping it the authority on the last byte.
func ttyReplayWindowBytes(caps []string) int64 {
	w := ttyReplayWindowDefaultBytes()
	if raw, ok := ttyClientCapValue(caps, "replay-window"); ok {
		if v, err := strconv.ParseInt(strings.TrimSpace(raw), 10, 64); err == nil && v >= 0 && v < w {
			w = v
		}
	}
	if w > ttyHandshakeReplayBudget {
		w = ttyHandshakeReplayBudget
	}
	return w
}

// ttyHeartbeatIntervalFor picks the heartbeat period for ONE connection
// (issue #114, decision 3(c)/5c): ttyHeartbeatIntervalSlowLink when the
// client declared `slow-link` as a WHOLE token, otherwise the interval it
// was handed — which for every real connection is ttyPingInterval, verbatim
// and unchanged.
//
// The `pingInterval == ttyPingInterval` guard is what keeps the compressed
// test seam honest: handleInstanceTTYWSLiveness takes the interval as a
// parameter precisely so a test can watch a tick in milliseconds, and a cap
// that silently replaced that parameter with 15 s would both break those
// tests and hide the fact that the widening is applied at all. So the
// widening applies to the PRODUCTION interval and to nothing else: a caller
// that chose a different interval has already made its own decision about
// the heartbeat rate, and this function does not second-guess it.
//
// The read deadline is deliberately NOT widened here. At 15 s pings the 45 s
// deadline still leaves three unanswered ping rounds before it bites —
// peer-dead, not peer-busy — and the client's own staleness threshold for a
// slow-link connection (60 s) is Phase B's half of the pair.
func ttyHeartbeatIntervalFor(caps []string, pingInterval time.Duration) time.Duration {
	if pingInterval == ttyPingInterval && ttyClientHasCap(caps, "slow-link") {
		return ttyHeartbeatIntervalSlowLink
	}
	return pingInterval
}

// ttyClientCapValue reads a k=v capability out of the tty WS handshake's
// `caps` parameter (issue #114, review R15). ttyClientHasCap above matches
// WHOLE tokens only — by design, because "pinger" must not opt a client into
// the TEXT ping — and that same exactness makes it blind to the parameterised
// caps this issue adds (`replay-window=262144` is not the token
// `replay-window`). So parameterised caps get their own parser, with the
// same case-sensitivity and the same tolerance for a REPEATED caps query
// parameter (?caps=a&caps=b) that ttyClientHasCap has.
//
// The four shapes a caller can meet, pinned by unit test:
//
//	"replay-window=262144"  → ("262144", true)   the match
//	"replay-window="        → ("", false)         empty value names nothing; ignored
//	"replay-window"         → ("", false)         no '=' sign, that is a plain cap
//	"replay-windowX=1"      → ("", false)         the name must be the whole key
//
// The key is the text BEFORE the first '=' with whitespace trimmed, so a
// value may itself contain '=' (a future token-shaped cap) and a hand-typed
// `replay-window = 4096` in a systemd unit or a curl still resolves: the
// parser is forgiving about the separator and strict about the name, which is
// the same posture ttyClientHasCap takes toward whitespace.
//
// Repeated declarations take the FIRST PARSEABLE value, and a value that is
// not an integer does not consume the scan: `replay-window=abc` followed by
// `replay-window=4096` yields 4096. That is the arbitrated reading (task
// book G4: 首个可解析值) — a client that sends a malformed token and then a
// good one has expressed an intent the server can honour, and honouring the
// first PARSEABLE token is the only interpretation that does not let one
// stray token silently disable a capability the same client asked for.
// Nothing here rejects the connection: an unrecognised or malformed cap is
// ignored, and the client falls back to the behaviour it would have had
// without it (the R16 rollback surface).
func ttyClientCapValue(caps []string, name string) (string, bool) {
	for _, v := range caps {
		for _, tok := range strings.Split(v, ",") {
			key, val, found := strings.Cut(strings.TrimSpace(tok), "=")
			if !found {
				continue // a bare token: ttyClientHasCap's territory, not a value
			}
			if strings.TrimSpace(key) != name {
				continue // case-sensitive whole-key match, same rule as the token matcher
			}
			val = strings.TrimSpace(val)
			if val == "" {
				continue // "name=" names no value: not a hit, keep scanning
			}
			if _, err := strconv.ParseInt(val, 10, 64); err != nil {
				continue // not parseable: not a hit, keep scanning (see above)
			}
			return val, true
		}
	}
	return "", false
}

// ttyPolicyOutputSubscriber is the optional kind surface the TTY handler
// uses to take a live subscription with a chosen overflow policy (issue
// #114). The handler reaches it through Manager.SubscribeOutputWith,
// which gates on Manager's own running-instance authority (the same door
// as Manager.SubscribeOutput) and resolves the kind through the same
// Registry.Get(ri.handle.KindName) lookup before handing it to the
// callback; the handler asserts this structural interface inside that
// callback. A kind that does not implement it is subscribed there through
// its plain SubscribeOutput instead — the #82 close-on-overflow contract,
// which is what an overflow-notice client on a kind without drop support
// must fall back to (1013 on overflow, never a fabricated notice).
type ttyPolicyOutputSubscriber interface {
	SubscribeOutputWithPolicy(id string, policy pty.OverflowPolicy) (<-chan string, func(), *pty.DropStats, error)
}

// ttyPlainOutputSubscriber is Manager.SubscribeOutput's kind-side door,
// restated here so the fallback inside the SubscribeOutputWith callback
// calls the KIND's plain SubscribeOutput on the resolved kind — the exact
// dispatch Manager.SubscribeOutput performs behind the same gate.
type ttyPlainOutputSubscriber interface {
	SubscribeOutput(id string) (<-chan string, func(), error)
}

// handleInstanceTTYWS is the mux entry point: production liveness
// intervals, no mutable interval state anywhere on this path.
func (s *Server) handleInstanceTTYWS(w http.ResponseWriter, r *http.Request) {
	s.handleInstanceTTYWSLiveness(w, r, ttyPingInterval, ttyReadDeadline)
}

// handleInstanceTTYWSLiveness is the production handler with the two
// liveness intervals as explicit parameters — the test seam for issue
// #83. It is a signature, not a mutable package var: a test that
// compresses the windows to milliseconds cannot make a parallel test's
// connection flap, so -race and t.Parallel stay clean.
//
// pingInterval is the caller's chosen BASE period, and one thing may widen
// it below: a client that declared `slow-link` moves the PRODUCTION value
// (ttyPingInterval) to ttyHeartbeatIntervalSlowLink. Any other value a
// caller passed — a compressed test seam included — passes through
// untouched, which is what keeps this seam honest: the interval a test
// hands in is the interval that test observes.
func (s *Server) handleInstanceTTYWSLiveness(w http.ResponseWriter, r *http.Request, pingInterval, readDeadline time.Duration) {
	// Non-positive intervals are nonsense for a liveness seam
	// (time.NewTicker panics on <= 0; a non-positive read deadline would
	// either reap instantly or silently disable the deadline). Fall back
	// to the production constants rather than trusting every caller. The
	// fallback lands ON ttyPingInterval, so a caller that passed nonsense
	// is then subject to the slow-link widening above — which is right:
	// nonsense means "whatever production would have used".
	if pingInterval <= 0 {
		pingInterval = ttyPingInterval
	}
	if readDeadline <= 0 {
		readDeadline = ttyReadDeadline
	}
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	id := strings.TrimSpace(r.URL.Query().Get("id"))
	if id == "" {
		http.Error(w, "id is required", http.StatusBadRequest)
		return
	}
	// Optional byte cursor for the handshake replay (issue #87). A
	// reconnecting client sends the offset of everything it has already
	// rendered, so the handshake replays only newer bytes instead of the
	// full 64KB tail (which it would append under its live screen).
	// Unknown/absent yields -1: the "nothing painted" sentinel that keeps
	// the first-connect tail behaviour. sinceFollowLiveEnd (-2) is the
	// OTHER unknown-cursor state — the screen IS painted, only its end
	// offset is unknown — and is handled inside completeHandshake.
	since := parseInt64Default(r.URL.Query().Get("since"), -1)
	// Client capability flags (issue #98), a comma-separated list. The
	// parameter may be REPEATED (?caps=pong&caps=ping) — url.Values.Get
	// would return only the first value, so the raw slice is scanned.
	// Defined flags:
	//   "ping": the client whitelists the TEXT {"type":"ping"} heartbeat
	//     in parseTTYControlMessage (stamps its liveness clock, never
	//     paints it), so the server may send it. A client that does NOT
	//     opt in — a page loaded before the whitelist shipped (v0.5.0),
	//     any non-browser client — is never sent the text frame, because
	//     such a client would paint it into the terminal as literal
	//     output every ttyPingInterval. The RFC 6455 ping below is
	//     unaffected: control frames never reach page JS, so it is
	//     always safe.
	//   "sync": the client whitelists the {"type":"sync"} offset echo
	//     and drives its reconnect cursor off it (issue #87). The echo
	//     has a second, INFERRED opt-in: an explicit `since` parameter.
	//     The cursor parameter and the sync whitelist shipped in the
	//     same fix (#87, v0.5.1) — v0.5.0 never puts `since` on this
	//     endpoint at all — so a client presenting one provably parses
	//     the other, and the inference costs exactly nothing against
	//     the one population that would paint the frame. It keeps
	//     pre-caps v0.5.1 pages on the cursor contract: without the
	//     echo their handshakeSynced latch never sets, the cursor
	//     freezes, and every reconnect re-replays from it — a
	//     duplicate-paint regression strictly worse than the leak
	//     (review round 2). Residual: a pre-caps page whose cursor is
	//     still the unknown/zero sentinel sends no `since` either, so
	//     it gets no echo and its reconnects re-replay the ≤64KB tail
	//     until a loadLog re-pin or a refresh lands a cursor — the
	//     pre-#87 behaviour that page shipped with.
	// This is deliberately opt-IN, not version-gated: the server can
	// never again leak a future visible control frame to a client that
	// did not declare it understands it.
	caps := r.URL.Query()["caps"]
	textPingCap := ttyClientHasCap(caps, "ping")
	_, sincePresent := r.URL.Query()["since"]
	syncCap := ttyClientHasCap(caps, "sync") || sincePresent
	// "overflow-notice" (issue #114): the client whitelists the TEXT
	// {"type":"overflow","droppedBytes":N,"droppedChunks":M} control
	// frame and RESETS + repaints from a cursorless (tail) reconnect when
	// it sees one. Declaring it opts the connection into the pty kind's
	// OverflowDropOldest subscription: a full live queue now drops the
	// OLDEST chunks (counted) instead of closing the subscription, and
	// the counts ride to the client as that notice ahead of the chunk
	// they precede — a slow viewer degrades to one redraw instead of the
	// issue #82 close-every-overflow 1013 reconnect storm. A client that
	// does NOT declare it is never sent the frame (the same #98 leak
	// class as the TEXT ping: an unwhitelisted page paints it into the
	// terminal) and keeps the #82 contract verbatim — overflow closes the
	// subscription, and the !ok teardown below answers 1013.
	overflowNoticeCap := ttyClientHasCap(caps, "overflow-notice")
	// "slow-link" (issue #114, decision 3(c)/5c): a whole-token cap only, no
	// value to read. A client that declares it is on a link where the 10 s
	// heartbeat costs a measurable share of the connection's write budget
	// (cross-border, 200-400 ms RTT, 1-5% loss), so the server's heartbeat
	// drops to ttyHeartbeatIntervalSlowLink — the RFC 6455 ping and the TEXT
	// control frame ride the same ticker, so both move together and the
	// read deadline stays where it is (see ttyHeartbeatIntervalFor for the
	// full margin arithmetic, and the same helper for the whole-token test).
	// A client that does NOT declare it — every client that exists today,
	// legacy included — keeps 10 s/45 s verbatim: this is an opt-in
	// widening, never a default change.
	pingInterval = ttyHeartbeatIntervalFor(caps, pingInterval)
	// The replay window W_eff (issue #114, decision 4/5b) is resolved here,
	// once per connection, from the env default and the client's optional
	// `replay-window=<bytes>` claim (ttyReplayWindowBytes: the claim may only
	// shrink it, and the result never exceeds ttyHandshakeReplayBudget). The
	// catch-up branch of completeHandshake is its only consumer — the tail
	// and follow-live-end modes replay a bounded 64KB or nothing at all, so
	// there is no window there to apply — and it uses it twice: to clamp the
	// START forward past head−W, and to stop the loop once W bytes have been
	// WRITTEN. Read here rather than inside the closure so the caps are
	// consulted where every other cap is consulted, and so one connection
	// negotiates one window instead of re-reading the environment mid-replay.
	replayWindow := ttyReplayWindowBytes(caps)
	rawConn, err := ws.Upgrade(w, r)
	if err != nil {
		return
	}
	// Issue #114 diagnostics: every write on this handler is timed and
	// counted through the metered wrapper (tty_metrics.go). Method set is
	// identical to *ws.Conn, so nothing below changes shape.
	conn := newTTYMeteredConn(rawConn, s.logger, id)
	clientHandle, resizeUpdates, err := s.newTTYClientHandle(id)
	if err != nil {
		_ = conn.WriteClose(ws.CloseMessage(1013, err.Error()))
		_ = conn.Close()
		return
	}
	defer func() {
		conn.m.logSummary(s.logger, id)
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

	// Issue #114 (decision 3, revised by the Phase A review): arm the
	// per-write deadline HERE — after the ready frame, before completeHandshake
	// is ever called — so the handshake replay writes are covered too. The
	// replay is up to 8 MB (ttyHandshakeReplayBudget) written to a socket
	// whose peer may already be gone; armed only after SubscribeOutput, that
	// whole window left a dead consumer free to pin this goroutine, ws.Conn's
	// mutex and every frame queued behind it indefinitely, which is the
	// amplification the #114 evidence chain names. Arming is per-write
	// (writeFrame re-arms at now+d on every frame), so this costs a slow but
	// progressing replay nothing — it only bounds the stall. The ready frame
	// itself, and the writes before it, stay deadline-free exactly as they
	// shipped: they are one small frame each, and a peer that cannot take the
	// ready frame has no session to lose. SetWriteTimeout lives on ws.Conn,
	// promoted through the metered wrapper untouched.
	conn.SetWriteTimeout(ttyWriteTimeout())

	// Step 2: Start single goroutine to read all messages
	type wsMessage struct {
		op   byte
		data []byte
	}
	msgChan := make(chan wsMessage, 64)
	go func() {
		defer close(msgChan)
		for {
			// Issue #83: arm a fresh read deadline before EVERY read. A
			// half-open peer that stops answering can no longer block this
			// ReadMessage forever: on expiry it errors, the goroutine
			// returns, msgChan closes, and the existing
			// `case msg, ok := <-msgChan: if !ok { return }` below unwinds
			// the handler through its existing defers (clientHandle.Close
			// drops the ttyClients entry, conn.Close frees the fd). No
			// second exit path is added. A healthy peer keeps the deadline
			// refreshed — every resize echo, keystroke, automatic RFC 6455
			// Pong to the heartbeat ping, and the app-level {"type":"ping"}
			// probe all land here. The read deadline is independent of
			// net.Conn's write deadline, so this cannot interfere with the
			// writes on this path.
			if err := conn.SetReadDeadline(time.Now().Add(readDeadline)); err != nil {
				return
			}
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
	// dropStats is non-nil exactly when the live subscription was taken
	// with OverflowDropOldest (an overflow-notice cap client, issue #114);
	// dropsSeenChunks and dropsSeenBytes are the two baselines — the
	// cumulative counts already SAMPLED, not the counts already sent, which
	// is the distinction the 250ms aggregation below depends on. All four
	// are handler-goroutine state (completeHandshake and the select loop are
	// the same goroutine), so plain fields suffice — the counters themselves
	// are atomic because the pty broadcast producer writes them under its
	// own lock.
	var dropStats *pty.DropStats
	var dropsSeenChunks, dropsSeenBytes int64
	// pendingNoticeChunks / pendingNoticeBytes are the aggregated counts of
	// every drop increment sampled since the last notice FRAME went out
	// (issue #114, decision 1 补 1 / review R5): the baseline advances on
	// every sample, so an increment is never lost, only grouped, and the
	// group goes out whole on the next frame the rate gate opens. Zero when
	// nothing is owed.
	var pendingNoticeChunks, pendingNoticeBytes int64
	// lastDropNoticeLogAt is the LOG throttle clock for this connection
	// (issue #114 review, minor): the zero value means "nothing logged yet",
	// so the FIRST drop increment logs immediately and later ones are sampled
	// at ttyDropLogInterval. It is a separate clock from lastOverflowNoticeAt
	// below on purpose — the log's 500ms and the wire's 250ms are independent
	// knobs and neither may silently become the other's.
	var lastDropNoticeLogAt time.Time
	// lastOverflowNoticeAt is the WIRE throttle clock (ttyOverflowNoticeInterval):
	// the zero value means "no notice sent yet", which is what makes the
	// FIRST increment of a connection go out immediately — the client's reset
	// decision must not wait 250ms to learn it has a problem — and every
	// later frame wait out the interval.
	var lastOverflowNoticeAt time.Time
	defer func() {
		if cancel != nil {
			cancel()
		}
	}()

	handshakeTimer := time.NewTimer(5 * time.Second)
	defer handshakeTimer.Stop()

	// Liveness heartbeat ticker (issue #83): the same tick drives both the
	// RFC 6455 ping (A3) and the observable TEXT control frame (A4), so
	// the two can never drift apart. Stopped via defer, same pattern as
	// handshakeTimer above. The period is the caller's, widened to
	// ttyHeartbeatIntervalSlowLink for a client that declared `slow-link`
	// (issue #114 decision 3(c)) — one ticker, so the widening reaches both
	// frames by construction and there is no second interval to keep in
	// step.
	heartbeat := time.NewTicker(pingInterval)
	defer heartbeat.Stop()

	// completeHandshake replays what the client has not seen yet, publishes
	// the replay's end offset in a `sync` frame, then hands the connection
	// to the live output subscription. It reads the outer `since` (the
	// cursor parsed from the query string) rather than taking a parameter;
	// both call sites below pass that same variable.
	//
	// On the streaming catch-up path the replay is bracketed by TWO sync
	// frames (issue #94): one carrying the replay's real start offset S
	// ahead of the FIRST binary chunk — S = first chunk's next - len, the
	// clamped value whenever ReadSince clamped a stale cursor, never the
	// raw request — and the closing one carrying the end offset of the last
	// chunk written. The start frame lifts the client's handshakeSynced
	// latch at chunk 1, so a mid-replay drop resumes from the bytes already
	// painted instead of re-pulling the whole replay; the closing frame
	// keeps the #87 invariant below and corrects any drift absolutely. The
	// beyond-head tail degrade (cursor > head) announces its tail's start
	// the same way — the client presented a cursor, so every replay chunk
	// addressed to one is preceded by a start frame; only the CURSOR-LESS
	// first-connect tail goes without (its single ≤64KB frame has the
	// pre-#87 death window, and such a client may not parse sync at all).
	//
	// Three cursors, three modes: a real `since >= 0` replays only [since,
	// head); the -1 sentinel (nothing painted) replays the tail; and the
	// sinceFollowLiveEnd sentinel (-2, painted but offset unknown) replays
	// NOTHING and publishes the live head instead (issue #86).
	//
	// Invariant (issue #87): the offset published in `sync` is the end
	// offset of the LAST chunk actually WRITTEN to this socket — never an
	// offset over bytes the client did not receive AND CAN STILL RECEIVE,
	// because the client cursor only moves forward and would never
	// re-request them. Three paths publish the ring's head instead of a
	// write's end offset, and none breaks that invariant, because on all
	// three the bytes behind head are ones this client can no longer get:
	// a path that writes nothing (caught up at head), where there is no
	// last chunk to name; the sinceFollowLiveEnd (-2) path that replays
	// NOTHING on purpose (issue #86), because the client's screen already
	// holds the tail; and a ring whose process has exited and whose
	// buffer has been dropped, where head is the truthful cursor precisely
	// because those bytes are permanently undeliverable. All three are
	// spelled out at their sites below.
	//
	// The whole delta is STREAMED, not buffered: every chunk the catch-up
	// loop reads is written to the socket and dropped, so peak memory stays
	// ~64KB (one chunk) regardless of the ring cap. Retaining only the
	// newest chunk would deliver a replay BEHIND the cursor it publishes,
	// permanently stranding everything older — so this loop writes as it
	// reads and never publishes ahead of the wire.
	//
	// Output produced between the last chunk write and the SubscribeOutput
	// below is covered by neither path — but the published cursor sits at
	// that last write's end offset, BEHIND the bytes the window produced,
	// so the next reconnect re-requests exactly that window (self-healing;
	// this is contiguity, not absolute coverage). ttyHandshakeReplayBudget
	// truncation is self-healing the same way: when the budget runs out the
	// loop stops and publishes the end offset of the last chunk WRITTEN,
	// so the remainder stays ahead of the cursor and the next reconnect
	// re-requests it — deferred, never skipped.
	completeHandshake := func() bool {
		// endOffset is the end offset of the newest byte on the wire from
		// this handshake. Every path that writes bytes sets it from that
		// write's own end offset; every path that writes nothing sets it
		// from the ring's real head (or leaves the 0 of a fresh instance).
		var endOffset int64
		// windowed / droppedFromWindow (issue #114, decision 4): declared
		// out here so the handshake instrumentation below can see them, but
		// written ONLY by the catch-up branch — the tail mode replays a
		// bounded 64KB and the follow-live-end mode replays nothing, so
		// neither has a window to apply and both report windowed=false
		// droppedFromWindow=0, which is the truth rather than a default.
		windowed := false
		var droppedFromWindow int64
		// Issue #114 diagnostics: time the replay and record the ring's
		// head at handshake start, so headAtStart-since sizes the replay
		// demand even when the 8MB budget truncates it. EndOffset is a
		// cheap read; its error (stopped instance) only degrades the
		// metric to -1, and the branches below keep their own error
		// handling untouched. The whole probe is const-gated: stable
		// builds (no ttymetrics tag) compile this block away entirely,
		// so the manager read happens only where the data is collected.
		if ttyMetricsBuild {
			conn.m.handshakeStart = time.Now()
			if head, herr := s.instanceMgr.EndOffset(id); herr == nil {
				conn.m.headAtStart = head
			}
		}
		if since == sinceFollowLiveEnd {
			conn.m.mode = "follow-live-end"
			// Follow from the live end (issue #86): the client's screen
			// already holds the tail and ONLY its end offset is unknown (a
			// proxy stripped X-Log-Offset), so replay nothing — any byte
			// sent here lands under a screen that already shows it. Publish
			// the current head and hand over to the live subscription, which
			// from there carries only newly produced data. EndOffset rather
			// than Tail because the body would be thrown away.
			//
			// Bytes produced between this read and the SubscribeOutput below
			// are the pre-existing read→subscribe window described above: the
			// client's cursor is the offset published here, so a later reconnect
			// sends it as `since` and gets those bytes back out of the ring.
			// Self-healing, and deliberately not closed here.
			//
			// The window this mode OPENS is the other one, and it does not
			// heal. The client reached this sentinel because it painted
			// GET /api/instances/log's tail, which ended at some head H1. This
			// read reports H2 >= H1, and the client adopts H2 — so its cursor
			// is AHEAD of its own screen by H2-H1, and [H1, H2) is in nothing:
			// not in the tail it painted, not in a replay (there is none), not
			// in the live stream (that starts at H2). Because the cursor sits
			// past them, no reconnect ever re-requests them: those bytes are
			// permanently gone from this client's screen. Normally the hole is
			// the loadLog()→connectTTY hop plus the WS upgrade, i.e.
			// milliseconds; if the handshake times out and the client falls back
			// to SSE it is 5s + 500ms of output, which on a chatty instance is
			// real lost lines. That is the price of a proxy stripping
			// X-Log-Offset: choose between duplicating the tail and losing a
			// window, and losing the window is the one that keeps the screen
			// readable. Fixing it properly needs the client to know H1, which
			// is exactly the number the stripped header took away.
			off, err := s.instanceMgr.EndOffset(id)
			if err != nil {
				_ = conn.WriteClose(ws.CloseMessage(1013, err.Error()))
				return false
			}
			endOffset = off
		} else if since < 0 {
			conn.m.mode = "tail"
			// No cursor (first connect): replay the newest bytes (AC2).
			body, off, err := s.instanceMgr.Tail(id, 64*1024)
			if err != nil {
				// Diagnostics never travel as binary frames: every
				// binary frame is ring-buffer output, because the client
				// advances its byte cursor by their length. Close instead
				// (same precedent as newTTYClientHandle above); the
				// client reports "ws closed, retrying..." and reconnects.
				_ = conn.WriteClose(ws.CloseMessage(1013, err.Error()))
				return false
			}
			if body != "" {
				if err := conn.WriteBinary([]byte(body)); err != nil {
					return false
				}
			}
			endOffset = off
		} else {
			conn.m.mode = "catchup"
			cursor := since
			// ── Replay window, half one: the START clamp (issue #114,
			// decision 4) ─────────────────────────────────────────────────
			// W_eff bounds BOTH ends of one handshake replay, and this is
			// the front end. A client that reconnects with a `since` from
			// before head−W_eff does not get a replay of everything it has
			// missed — it gets the newest W_eff bytes, which is the whole
			// point on the link this was designed for: the backlog it is
			// asking for is bigger than its scrollback can render and bigger
			// than its link can carry before the live stream needs the same
			// socket (the 3.96MB / 84.6s replay of the 2026-10-17 evidence).
			//
			// No new wire surface: the clamped start reaches the client
			// through the issue #94 start-sync path it already implements.
			// emitStartSync below publishes `next - len(body)` — the start
			// of the first chunk from the SAME ReadSince call — so once
			// `cursor` has moved forward here, the frame the client adopts is
			// the clamped offset, and the closing sync republishes the real
			// end. A client that understands only sync-at-the-end is exactly
			// where it was before #94; nothing has to know a clamp happened.
			//
			// The EndOffset read is one manager call per catch-up handshake,
			// unconditionally now that the answer is load-bearing rather than
			// observational: handshakes are rare against the per-chunk work
			// the window then bounds. On error (an instance that stopped
			// between the upgrade and here) ONLY the start clamp is given up
			// — the total cap below never reads head, it counts bytes
			// already written, so the replay's upper bound stays in force
			// regardless. Failing to apply a clamp is a degradation, never
			// a corruption. The ReadSince below keeps its own error
			// handling untouched.
			// pureTail (issue #114, review B round 1 / R1): W_eff == 0 is
			// CONSTRUCTIVELY pure tail — the replay read loop below is
			// never entered, independent of whether the producer happens to
			// be idle (the review's live-producer probe sent 262144 bytes
			// through the old "cursor parked at head, loop writes nothing"
			// assumption, because ReadSince reads whatever head is current
			// at each call). Semantics mirror the sinceFollowLiveEnd
			// sentinel above: zero replay frames, publish the head this
			// clamp read, hand over to the live subscription. No new
			// mechanism; the sentinel path itself is untouched.
			pureTail := false
			if head, herr := s.instanceMgr.EndOffset(id); herr == nil {
				if span := head - cursor; span > replayWindow {
					clampedStart := head - replayWindow
					// 口径: the clamp's contribution to droppedFromWindow is
					// the span the window refused, clampedStart − since. When
					// `since` is ALSO stale (behind the ring's oldest live
					// byte) this figure overlaps the bytes #94's start-sync
					// already re-based the client onto — both counts describe
					// the same skipped region, and this one is the window's
					// arithmetic, which is what the field is for.
					droppedFromWindow += clampedStart - since
					cursor = clampedStart
					windowed = true
				}
				if replayWindow == 0 {
					// R1: the head read SUCCEEDED and the window is zero,
					// so the replay is empty BY CONSTRUCTION — publish this
					// head (the same value the clamp above is defined
					// against) and skip the whole catch-up loop, landing on
					// the existing closing sync + SubscribeOutput below.
					// A head read that FAILED leaves pureTail false: the
					// degradation stands (no window applied, loop runs
					// unclamped), consistent with the "failing to apply an
					// upper bound is a degradation" note above, and the
					// loop's own zero-progress path still publishes head
					// once the ring is caught up.
					endOffset = head
					pureTail = true
				}
			} else if s.logger != nil {
				s.logger.Printf("tty replay window not applied for %s: head unknown (%v), catching up unclamped", id, herr)
			}
			first := true
			var streamed int64
			// startPublished (issue #94) marks that the replay's real
			// START offset has been announced to the client, so it can
			// advance its cursor frame-by-frame from there instead of
			// waiting for the closing sync frame. It is deliberately NOT
			// `first`: the FIX-F consult below clears `first` before any
			// chunk has been written, while the start offset must be
			// published exactly once, ahead of the first CHUNK on the
			// wire.
			startPublished := false
			// emitStartSync announces a replay's real START offset
			// (issue #94) ahead of its first binary chunk, lifting the
			// client's handshakeSynced latch at chunk 1 so a mid-replay
			// drop resumes from the bytes already painted instead of
			// re-pulling the whole (up to 8MB) replay. The frame REUSES
			// the "sync" type rather than inventing a new one: the
			// client's whitelist (parseTTYControlMessage) paints any
			// unknown text frame into the terminal (the issue #98 leak
			// class), and the existing handler — set cursor to the
			// ABSOLUTE offset, lift the latch — is exactly the semantics
			// wanted here. The "start":true marker lets a consumer that
			// must know WHEN the replay ends (a test harness, a
			// third-party client) tell this announcement apart from the
			// closing echo; the in-repo client deliberately ignores it —
			// every sync is an absolute offset to adopt.
			//
			// syncCap is PROVABLY true at both call sites — they sit in
			// the catch-up branch, which requires since >= 0, i.e. an
			// explicit `since` in the query, already the inferred opt-in
			// half of syncCap (issue #98; review Minor-1). The guard
			// stays so a future change to that inference can never leak
			// the frame to a client that did not opt in — defensive
			// depth, not an option any client has today.
			emitStartSync := func(start int64) bool {
				if !syncCap {
					return true
				}
				payload := []byte(`{"type":"sync","offset":` + strconv.FormatInt(start, 10) + `,"start":true}`)
				return conn.WriteText(payload) == nil
			}
			// for !pureTail (R1): when W_eff == 0 and the clamp's head
			// read above succeeded, the loop is skipped in full — no
			// replay read is ever issued, so no replay byte can reach the
			// wire even under a live producer. endOffset already holds
			// that head and flows straight into the existing closing sync
			// and the live subscription below. The loop body is verbatim
			// unchanged for every other route.
			for !pureTail {
				if streamed >= ttyHandshakeReplayBudget {
					// Budget exhausted — the hard backstop, checked before the
					// read so no further read is issued once the
					// budget is spent. Reaching this line already means
					// ttyHandshakeReplayBudget (>0) bytes went out on this
					// socket, so endOffset holds the end offset of the LAST
					// chunk written — never the zero value. The undelivered
					// remainder is AHEAD of that cursor, so the next
					// reconnect re-requests it: truncated, not skipped.
					//
					// The guard counts bytes already written, so in principle
					// a stream could end one chunk past the boundary; 8MB is
					// a whole multiple of the 64KB read cap, so it lands
					// exactly on it — which is what the budget test pins.
					// The window trim added below (R3) interacts with this
					// boundary in two shapes, and neither breaks the pin:
					// ReadSince returns AT MOST maxBytes (a partial read is
					// routine — the inside-window test pins the [65536,
					// 36864] shape), so on the full-read route (W_eff ==
					// this budget, every read a full 64KB) the trim never
					// changes a read and `streamed` stays a whole multiple
					// of 64KB, landing exactly on the budget as before; on
					// a partial-read route the remainder can fall below one
					// full read, the trim DOES fire, and it tightens the
					// budget route to stop exactly on W — which incidentally
					// closes the budget's own ≤64KB−1 overshoot edge the
					// review B probe measured (8388608 trimmed / 8424144
					// untrimmed). The budget guard remains the stop the
					// budget test exercises, byte-for-byte unchanged there.
					//
					// Since issue #114 the window guard below is normally the
					// one that fires, because ttyReplayWindowBytes keeps
					// W_eff ≤ this budget BY CONSTRUCTION — an operator who
					// points MYWORKTREE_TTY_REPLAY_WINDOW_BYTES above 8MB is
					// clamped to it there, not honoured. That is deliberate,
					// and it is why this guard stays exactly where it is: the
					// budget remains the single authority on the last byte,
					// the ceiling a window can never lift, and the only stop
					// available to a handshake whose window was configured
					// away above it.
					//
					// Within that W_eff ≤ budget world, the reading
					// budgetExhausted=true with windowed=false and
					// droppedFromWindow=0 is LEGAL, not a contradiction:
					// it is reachable when W_eff == budget (the env raised
					// to ≥8MB and clamped there) AND the backlog at
					// handshake is < W_eff (so the START clamp above never
					// fires) AND a producer then outruns the replay past
					// the budget — the clamp had nothing to cut, yet the
					// total cap still did. windowed's semantics are the
					// OR of the two WINDOW truncations only (start clamp OR
					// total cap), deliberately excluding the budget stop —
					// the frozen-screen window is what the field reports
					// (the review-B arbitration), and a budget truncation
					// is reported by its own field. The truncated-not-
					// skipped semantics of a stop on exactly the last
					// written byte are pinned by the WINDOW tests, which
					// exercise the same closing-sync path this break flows
					// into (review B round 1 / R5).
					conn.m.budgetExhausted = true
					break
				}
				if replayWindow > 0 && streamed >= replayWindow {
					// ── Replay window, half two: the TOTAL cap (issue #114,
					// decision 4 / review HIGH-2) ─────────────────────────
					// Clamping the START does not bound the DURATION.
					// ReadSince reads to whatever head is current at each
					// call (ringbuffer.go), so a catch-up under sustained
					// production follows a head that keeps moving and streams
					// until the byte budget stops it — 84.6 s of replay in the
					// 2026-10-17 evidence, screen frozen the whole time. The
					// start clamp sizes the replay against the backlog; only
					// a count of the bytes actually WRITTEN sizes it against
					// reality. The two caps together are what make replay
					// duration ≤ W_eff / effective bandwidth, independent of
					// how big the backlog is.
					//
					// Semantics are the budget's verbatim, on purpose: the
					// closing sync below publishes the end offset of the LAST
					// chunk written — never the head — so the undelivered
					// remainder sits AHEAD of the client's cursor and the next
					// reconnect re-requests it. Truncated, not skipped. (The
					// bytes the START clamp skipped are the opposite case:
					// behind the cursor, off this client's screen for good —
					// that is what W costs, and docs/API.md says so.)
					//
					// replayWindow > 0 because W == 0 is legal and means
					// pure tail, and since R1 that purity is CONSTRUCTIVE:
					// the W==0 clamp route sets pureTail and this loop is
					// never entered, so this guard's byte count is only
					// ever compared against a positive W. On the degraded
					// W==0 route (head read failed, pureTail false) the
					// guard stays disarmed exactly as before — and a guard
					// firing on 0 >= 0 there would still break with
					// endOffset possibly 0, publishing a cursor the ring
					// may never have held, which is what the > 0 term has
					// always prevented.
					windowed = true
					// 口径: this truncation's contribution is the gap between
					// the ring head AT THIS INSTANT — one read, here, the
					// only moment the figure is defined — and the last byte
					// actually written. Zero when the loop happened to stop
					// exactly on head (the usual case with no live producer),
					// and the bytes still in the ring when a producer outran
					// the replay, which is the case this cap exists for.
					// Const-gated like every other probe on this path: the
					// flag itself is what the wire behaviour needs (nothing —
					// it is observational too), the manager read behind the
					// number is only worth making where the number is
					// collected, so stable builds skip it.
					if ttyMetricsBuild {
						if h, herr := s.instanceMgr.EndOffset(id); herr == nil && h > endOffset {
							droppedFromWindow += h - endOffset
						}
					}
					break
				}
				// RingBuffer.ReadSince silently clamps a stale cursor to
				// the oldest live byte and returns ("", since, nil) once
				// since >= head (ringbuffer.go), so a normal loop exit
				// terminates with endOffset == head and a stale offset
				// degrades to a tail replay (AC3).
				//
				// Read-size trim (issue #114, review B round 1 / R3): the
				// cap above is checked BEFORE the read, so a full 64KB
				// read could push the wire total to W + 64KB − 1 (the
				// review measured W=40000 → 65536 out). AC2's "replay
				// ≤ W_eff" is authoritative, so when the window is armed
				// and less than one full read remains, the read itself is
				// trimmed to the remainder — the cap then lands exactly
				// on W_eff, truncated-not-skipped semantics unchanged
				// (the closing sync still publishes the last byte
				// actually written). When W_eff == 0 this trim is inert
				// (guard below never arms, and the pureTail route never
				// reaches this line); on the degraded W==0 route
				// (head unknown) the trim is likewise inert, matching
				// the unclamped degradation above.
				readSize := int64(64 * 1024)
				if replayWindow > 0 {
					if rem := replayWindow - streamed; rem < readSize {
						// streamed < replayWindow here — the cap guard
						// above breaks otherwise — so rem >= 1.
						readSize = rem
					}
				}
				body, next, err := s.instanceMgr.ReadSince(id, cursor, readSize)
				if err != nil {
					_ = conn.WriteClose(ws.CloseMessage(1013, err.Error()))
					return false
				}
				if body == "" {
					if !first {
						// Normal loop exit (and the exhausted-budget-and-
						// caught-up exit): at least one chunk is already on
						// the wire. Take this read's `next` — for a live
						// ring that is head, the contiguous cursor; for a
						// CLOSED ring ReadSince answers ("", head, nil) with
						// an empty body while since < head, and head is
						// still the truthful value because those bytes can
						// never be delivered. Publishing the zero value
						// here instead would hand back a cursor the ring
						// never held and send the client into a full tail
						// replay (the duplicate paint this issue is about).
						endOffset = next
						break
					}
					// Zero progress on the FIRST read: at this
					// read's instant the cursor was at or beyond
					// head, and continuing would publish the
					// client's own number back as authoritative.
					// Consult the real head via Tail — the ring
					// may have written since that instant, so
					// split three ways:
					tail, head, herr := s.instanceMgr.Tail(id, 64*1024)
					if herr != nil {
						_ = conn.WriteClose(ws.CloseMessage(1013, herr.Error()))
						return false
					}
					switch {
					case cursor > head:
						// Cursor ahead of head: bogus (not
						// reachable through normal flows today
						// — Restart mints a new id), but if it
						// ever appeared it must degrade to the
						// first-connect tail, never to a cursor
						// the ring never held. Write the tail
						// before claiming head over it. Unlike
						// the CURSOR-LESS tail replay above, this
						// tail rides the issue #94 contract: the
						// client presented a cursor, so announce
						// the tail's real start S = head - len
						// ahead of the chunk and let its latch
						// lift at a truthful absolute base — the
						// closing sync then republishes the same
						// head the frames summed to.
						if tail != "" {
							if !emitStartSync(head - int64(len(tail))) {
								return false
							}
							if err := conn.WriteBinary([]byte(tail)); err != nil {
								return false
							}
						}
						endOffset = head
					case head > cursor:
						// FIX-F: the PTY wrote in the window
						// between the caught-up ReadSince and
						// this Tail consult. [cursor, head) is
						// in neither the replay nor the live
						// subscription (SubscribeOutput has not
						// run), and a client cursor only moves
						// forward — publishing head here would
						// skip those bytes forever. Do NOT
						// break: continue and let the next
						// ReadSince(cursor) deliver them.
						// Termination is structural, no extra
						// guard needed: this consult runs only
						// while first is true, and clearing it
						// right here means the branch can be
						// taken at most once — the next
						// iteration either delivers a chunk and
						// advances, or hits the !first break
						// (which now carries that read's
						// `next`, so it cannot publish 0).
						// Do not "optimise" this back into a
						// break.
						first = false
						continue
					default:
						// head == cursor: genuinely caught up
						// (the common idle reconnect) — replay
						// nothing, publish head. Must stay a
						// no-op.
						endOffset = head
					}
					break
				}
				// Write as we read: the chunk is on the wire now, so the
				// cursor published below is never ahead of it, and nothing
				// older than the next read is retained in memory.
				if !startPublished {
					startPublished = true
					// S = next - len(body) is the true start of THIS
					// chunk — the requested `since` when no clamp
					// happened, the clamped oldest live byte when it
					// did — because S and the first chunk come from the
					// SAME ReadSince call, so no new Manager surface is
					// needed and a stale `since` can never be echoed
					// raw. The closing sync below still arrives with
					// the end offset, so a client that only understands
					// sync-at-the-end is merely back to the pre-#94
					// death window, and a misbehaving kind that returns
					// a body without advancing the cursor (the
					// defensive break below) is corrected by that same
					// final absolute value. One corner on THAT
					// defensive path (review Minor-2): next <= cursor
					// there makes S = next - len(body) dip BELOW the
					// client's own `since`, so a drop between this
					// frame and the first chunk reconnects with a
					// regressed cursor and re-renders [S, since) —
					// bounded, self-healing, and still strictly better
					// than the pre-#94 full re-pull it replaces.
					if !emitStartSync(next - int64(len(body))) {
						return false
					}
				}
				if err := conn.WriteBinary([]byte(body)); err != nil {
					return false
				}
				endOffset = next
				streamed += int64(len(body))
				if next <= cursor {
					// Defensive: a kind whose ReadLogs returns a
					// non-empty body without advancing the cursor would
					// otherwise spin here forever — the handshake timer
					// is already stopped at this point, so the client
					// would sit at "live" with no output. Exit with the
					// chunk already written instead.
					break
				}
				cursor = next
				first = false
			}
		}
		// Offset echo: hand the client the end offset of the last byte it
		// received, so its next reconnect can send it back as `since`.
		// Without this the client can never learn a valid cursor and every
		// reconnect replays the full tail again. Sent even when the replay
		// was empty — offset 0 is a legitimate cursor on a fresh instance,
		// and every other empty-replay path set a truthful head above.
		// Built with strconv (not json.Marshal) so there is no
		// unreachable error branch.
		//
		// OPT-IN (the issue #98 orientation): the echo is itself visible
		// control traffic that post-dates the v0.5.0 whitelist, so an
		// un-refreshed v0.5.0 page would paint it into the terminal on
		// every connect — the same leak as the TEXT ping, once per
		// connect instead of once per tick. It goes only to a client
		// that declared "sync" in caps or presented an explicit `since`
		// cursor (the two shipped together in #87 — see the handshake
		// comment above); v0.5.0 does neither and never receives it.
		if syncCap {
			syncPayload := []byte(`{"type":"sync","offset":` + strconv.FormatInt(endOffset, 10) + `}`)
			if err := conn.WriteText(syncPayload); err != nil {
				return false
			}
		}

		// Subscribe per negotiated overflow policy (issue #114): a client
		// that declared overflow-notice gets OverflowDropOldest — overflow
		// is then counted and announced, never a disconnect — and every
		// other client gets today's Manager.SubscribeOutput call, the #82
		// close contract, unchanged. The cap path does NOT gate liveness
		// on a store read: Manager's running-instance map is the single
		// authority, so it goes through Manager.SubscribeOutputWith, the
		// same door Manager.SubscribeOutput itself checks — an instance
		// absent from it answers the byte-identical "instance not
		// running". The resolved kind is handed to the callback below,
		// which asserts the optional policy interface and, on a kind
		// without it, falls back inside the callback to that kind's plain
		// SubscribeOutput — the #82 contract, so a cap client on such a
		// kind still gets 1013 on overflow and never a fabricated notice
		// (dropStats stays nil, the live loop stays quiet).
		//
		// Every one of those degradations is LOGGED (issue #114 review,
		// minor): the client asked for counted-and-kept overflow and did
		// not get it, and without a server-side line the difference between
		// "this kind cannot drop-oldest" and "this kind silently regressed"
		// is invisible from the daemon — the client's console is the only
		// other witness, and it is not in front of the operator. The
		// nil-guard mirrors every other log on this path: production cannot
		// reach it (app.New rejects a nil logger), the tests inject one, so
		// the lines below are exercised rather than decorative.
		var ch <-chan string
		var cancelFn func()
		var err error
		if overflowNoticeCap {
			ch, cancelFn, err = s.instanceMgr.SubscribeOutputWith(id, func(k framework.Kind) (<-chan string, func(), error) {
				if ps, ok := k.(ttyPolicyOutputSubscriber); ok {
					pCh, pCancel, stats, pErr := ps.SubscribeOutputWithPolicy(id, pty.OverflowDropOldest)
					if pErr != nil {
						// Degradation, and the most surprising kind: the kind
						// claims drop support and the subscription itself
						// refused. The cap client gets the error verbatim
						// (1013 below) rather than a quiet plain fallback,
						// because a kind that fails a policy subscription it
						// advertised has already told us something is wrong
						// with this instance — inventing a subscription behind
						// its back would hide that, and the client would keep
						// a connection whose drops nobody counts.
						if s.logger != nil {
							s.logger.Printf("tty overflow-notice cap degraded for %s: kind %q policy subscription failed, closing with 1013 instead: %v", id, k.Manifest().Name, pErr)
						}
						return nil, nil, pErr
					}
					dropStats = stats
					return pCh, pCancel, nil
				}
				if plain, ok := k.(ttyPlainOutputSubscriber); ok {
					if s.logger != nil {
						s.logger.Printf("tty overflow-notice cap degraded for %s: kind %q has no policy subscription, falling back to legacy overflow-close (1013 on overflow, no notice)", id, k.Manifest().Name)
					}
					return plain.SubscribeOutput(id)
				}
				// Same message Manager.SubscribeOutput returns for a kind
				// behind the same gate; the registry keys kinds by
				// Manifest().Name, so the name is identical there too.
				if s.logger != nil {
					s.logger.Printf("tty overflow-notice cap degraded for %s: kind %q supports no output subscription at all, closing with 1013", id, k.Manifest().Name)
				}
				return nil, nil, fmt.Errorf("kind %q does not support output subscription", k.Manifest().Name)
			})
		} else {
			ch, cancelFn, err = s.instanceMgr.SubscribeOutput(id)
		}
		if err != nil {
			_ = conn.WriteClose(ws.CloseMessage(1013, err.Error()))
			return false
		}
		cancel = cancelFn
		outputChan = ch
		// No SetWriteTimeout here: the per-write deadline is armed earlier,
		// directly after the ready frame (Step 1 above), and stays armed
		// through this subscription and every live write after it. Arming
		// here — "once the subscription is established", the plan's original
		// wording — was the gap the Phase A review caught: everything this
		// closure writes before reaching this line, up to the 8 MB replay,
		// ran with no deadline at all, so a consumer that had already died
		// could pin this goroutine and ws.Conn's mutex inside a replay write
		// forever. The default timeout (15 s, MYWORKTREE_TTY_WRITE_TIMEOUT_MS
		// to change it) still exceeds the heartbeat period a connection
		// actually runs — 10 s, or 15 s on ttyHeartbeatIntervalSlowLink for
		// a `slow-link` client, where the deadline is re-armed at the START
		// of every write and so measures one blocked write rather than the
		// gap between frames — so a healthy connection re-arms it with its
		// own traffic long before it trips. The write deadline #83
		// deliberately scoped out is what #114 finally lands, now without
		// the replay hole.
		// Issue #114 diagnostics: snapshot the replay counters off the
		// binary counter — every binary frame so far was handshake replay,
		// so this is exact for all three modes with no per-branch
		// accounting. Const-gated like the entry probe: stable builds
		// compile the block away, and logHandshake is a no-op there.
		if ttyMetricsBuild {
			conn.m.publishedOffset = endOffset
			conn.m.windowed = windowed
			conn.m.droppedFromWindow = droppedFromWindow
			conn.m.replayBytes = conn.m.binBytes
			conn.m.replayDur = time.Since(conn.m.handshakeStart)
			conn.m.subscribedAt = time.Now()
		}
		conn.m.logHandshake(s.logger, id, since)
		return true
	}

	for {
		select {
		case msg, ok := <-msgChan:
			if !ok {
				return
			}

			if ws.IsClose(msg.op) {
				// The answer to the client's close is a best-effort close
				// of our own. Since this phase it also runs under the
				// per-write deadline armed after the ready frame, so a peer
				// that has already stopped reading cannot pin the handler
				// here forever — the write errors at ttyWriteTimeout and the
				// defers unwind the connection, which is the outcome this
				// branch was already heading toward.
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
			parseOK := json.Unmarshal(msg.data, &resizeMsg) == nil
			isResize := parseOK && resizeMsg.Type == "resize"
			// App-level liveness probe (issue #83): parsed the same way
			// as isResize, from the SAME unmarshal.
			isPing := parseOK && resizeMsg.Type == "ping"

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

			// MANDATORY placement: BEFORE the input fallthrough below. A
			// {"type":"ping"} probe is transport traffic, not keystrokes —
			// forwarding it would type JSON into the user's shell. Answer
			// with {"type":"pong"} (the reply is the point: a client whose
			// server→client direction is dead but whose client→server
			// direction still "looks" writable learns the truth only from
			// a missing pong) and consume the frame. The collision with a
			// user literally typing that exact JSON is the same accepted
			// trade-off as {"type":"resize",...} above.
			if isPing {
				if err := conn.WriteText([]byte(`{"type":"pong"}`)); err != nil {
					return
				}
				continue
			}

			if handshakeComplete && !isResize {
				if err := s.instanceMgr.SendInput(id, string(msg.data)); err != nil {
					return
				}
			}

		case <-heartbeat.C:
			// Liveness heartbeat (issue #83), two frames per tick:
			//  1. RFC 6455 ping — the browser's network stack auto-replies
			//     Pong, which lands on the reader and refreshes its
			//     read deadline, so a healthy connection is NEVER reaped.
			//     Invisible to page JS (onmessage never fires for control
			//     frames), hence frame 2:
			//  2. TEXT {"type":"ping"} — the heartbeat the client can
			//     actually observe and stamp lastDataAt from. OPT-IN ONLY
			//     (issue #98): sent solely to clients whose `caps`
			//     handshake parameter listed "ping" — a client without
			//     the whitelist (a pre-refresh v0.5.0 page, any older or
			//     non-browser client) would paint this frame into the
			//     terminal every 10s, so it gets frame 1 only and
			//     degrades to the pre-#83 behaviour it always had.
			// A failed write means the peer is gone: return and let the
			// existing defers unwind (same treatment as the binary output
			// write below).
			if err := conn.WritePing(nil); err != nil {
				return
			}
			if textPingCap {
				if err := conn.WriteText([]byte(`{"type":"ping"}`)); err != nil {
					return
				}
			}
			// Pending-notice flush (issue #114, review B round 1 / R2):
			// the drop sampling and notice write above live in the
			// outputChan branch, so once output stops a pending pair
			// stranded by the 250ms gate could never reach the wire —
			// the review measured 22-24 counted drops never announced.
			// That breaks the visible half of "notices are the complete
			// record": the client's cursor advanced only over received
			// bytes, so it sits behind the ring head and a reconnect
			// repaints the dropped region (the #87 complaint). Flushing
			// here — same goroutine, same select, no new lock, no new
			// timer — bounds the stranding to within one heartbeat
			// cycle: the gate has normally long opened by this tick
			// (10s/15s ≫ 250ms), so any pair still pending when output
			// stopped is delivered on the next heartbeat; the one
			// exception is a chunk-path notice written <250ms before the
			// tick, where the gate defers this flush by at most one
			// more gate window onto the following tick. Same verbatim
			// discipline as the outputChan branch: clear BEFORE the
			// write, a failed write takes the existing return path.
			if dropStats != nil && (pendingNoticeChunks > 0 || pendingNoticeBytes > 0) {
				if lastOverflowNoticeAt.IsZero() || time.Since(lastOverflowNoticeAt) >= ttyOverflowNoticeInterval {
					noticeChunks, noticeBytes := pendingNoticeChunks, pendingNoticeBytes
					pendingNoticeChunks, pendingNoticeBytes = 0, 0
					lastOverflowNoticeAt = time.Now()
					notice := []byte(`{"type":"overflow","droppedBytes":` + strconv.FormatInt(noticeBytes, 10) + `,"droppedChunks":` + strconv.FormatInt(noticeChunks, 10) + `}`)
					if err := conn.WriteText(notice); err != nil {
						return
					}
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
				// The live subscription ended while this handler was
				// still serving. cancel() only runs in this function's
				// own defer, so the only way here is the pty kind's
				// overflow disconnect (issue #82): our consumer fell
				// behind, its buffer filled, and it was closed instead
				// of having chunks silently dropped. Say so on the
				// wire — the client's ws.onclose logs abnormal codes
				// and reasons, so the browser console names the cause
				// instead of showing an unexplained drop.
				//
				// This frame is BEST-EFFORT and resync does not depend on
				// it: the browser reconnects on any socket close, reasoned
				// or not, and resyncs from its own cursor (the #87 offset
				// contract). The 5 s deadline below is deliberately
				// BOUNDED, and it bounds exactly one thing: THIS close-frame
				// write, on this teardown path — tighter than the 15 s
				// per-write timeout armed after the ready frame (issue
				// #114 / decision 3, the write deadline #83 deliberately
				// scoped out of its own contract and #114 landed here), so
				// the timeout is DISARMED first: writeFrame re-arms its
				// deadline on every write, and leaving 15 s armed would
				// silently loosen this 5 s bound it was set to enforce.
				// The general timeout still does NOT change what this
				// branch means: a handler already blocked inside a live
				// WriteBinary above never reaches it either — it now just
				// stops being pinned forever there (the write errors at
				// 15 s and the handler unwinds through the same defers;
				// AC3 of the #114 plan). What keeps this failure path from writing
				// the backlog onto the stalled socket is upstream, in the pty
				// kind: broadcast drains the closed subscriber's queue at
				// the source (driver.go) — after it releases subsMu, never
				// inside that process-global lock, because the drain only
				// terminates by virtue of the channel already being closed.
				// That BOUNDS what this branch can write; it does not order
				// it. The queue is empty by the time broadcast returns, but
				// close() readies any receiver already parked on the channel
				// — that is this handler, sitting in the select above — and a
				// readied receiver races the drain loop for the values still
				// buffered, so a few chunks may legitimately reach the socket
				// before this branch runs. What the drain does make impossible
				// is the unbounded version: this handler cannot flush a full
				// queue — up to 64 queued chunks (~64 KB) — onto the socket
				// that stalled. Every
				// drained chunk that is STILL IN THE RING BUFFER comes back
				// on the cursor reconnect; the exception is a ring the
				// framework has already closed and dropped, whose writes were
				// no-ops by the time they were broadcast (ARCHITECTURE §4.1,
				// and docs/API.md states the same qualifier). Nothing writes
				// on this conn after this branch returns —
				// this branch returns immediately and the deferred
				// conn.Close() follows. (Healthy connections never see the
				// armed 15 s timeout bite: their own frame traffic re-arms
				// it on every write, so the bound is invisible until a peer
				// truly stalls — which is the state the bound exists for.)
				//
				// Server-side, this is the only trace of the disconnect
				// besides the close frame: the client retries on a
				// hard-coded 5 s timer with no backoff, so without this line
				// a repeatedly-overflowing consumer would be a silent
				// reconnect+replay loop. The nil guard is defensive against a
				// state production cannot reach — app.New rejects a nil
				// logger ("logger is required") — and it is NOT dead to the
				// test suite: ttyWSTestServer injects a logger into the
				// Server it builds, so
				// TestHandleInstanceTTYWS_SubscriberOverflowClosesConnectionWith1013
				// asserts this line actually fired.
				if s.logger != nil {
					s.logger.Printf("tty output subscriber overflow for %s: consumer fell behind, closing with 1013 for cursor resync", id)
					// Issue #114 diagnostics: subscription age, live bytes
					// delivered, and the ring head right now. headNow minus
					// the client's cursor (its close line) is the backlog
					// the next reconnect will replay. The EndOffset probe is
					// const-gated: stable builds skip the manager read and
					// logOverflow is a no-op there.
					headNow := int64(-1)
					if ttyMetricsBuild {
						if h, herr := s.instanceMgr.EndOffset(id); herr == nil {
							headNow = h
						}
					}
					conn.m.logOverflow(s.logger, id, headNow)
				}
				// Disarm the general 15 s per-write timeout (armed after the
				// ready frame, issue #114) BEFORE setting this explicit
				// deadline: writeFrame re-arms its own deadline on every
				// write, so leaving it set would silently loosen this
				// documented 5 s bound to 15 s. With d == 0 the deadline
				// below is fully in charge, exactly as it was pre-#114.
				conn.SetWriteTimeout(0)
				_ = conn.SetWriteDeadline(time.Now().Add(5 * time.Second))
				_ = conn.WriteClose(ws.CloseMessage(1013, "subscriber overflow: slow consumer"))
				return
			}
			// Overflow notice (issue #114, drop-oldest subscribers only —
			// dropStats is nil for every legacy subscriber, so this is one
			// pointer nil-check there, zero wire change). The pty
			// broadcast has discarded OLDEST chunks to keep this consumer
			// streaming; the counts are the ONLY proof of that loss, and
			// they ride AHEAD of the chunk they precede so the client's
			// reset decision is made before a torn byte hits its screen:
			// TEXT {"type":"overflow","droppedBytes":N,"droppedChunks":M}.
			//
			// The counts are INCREMENTAL — every drop since the last notice
			// FRAME, which since R5 is a 250ms WINDOW rather than a single
			// increment. The client does not need the absolute total to know
			// it must reset, and an interval sum is exact for the same reason
			// a single increment was: what broadcast drops is always the
			// OLDEST undelivered chunk (the queue head), so every number here
			// is already final by the time it can be sampled, and each
			// increment still comes off ONE Snapshot() load — DropStats
			// publishes chunks and bytes as a single atomic unit, so summing
			// consecutive published pairs yields a pair the producer's history
			// really produced rather than two counters sampled torn.
			//
			// Written before the binary frame on purpose, and that ordering
			// survives the aggregation: a frame that goes out here still
			// precedes the chunk of THIS iteration, and the bytes it reports
			// are all strictly older than that chunk (they were evicted to
			// make room for chunks up to and including this one). The
			// client's cursor advances by binary frame length, so a notice
			// that trailed the bytes it covers could be processed after those
			// bytes were painted — too late to reset cleanly. The notice is
			// itself counted into txtFrames by the metered conn.
			//
			// TWO INDEPENDENT RATE GATES live in this block, and the split
			// is the point (issue #114 review minor + R5):
			//   - the LOG line at ttyDropLogInterval (500ms): first increment
			//     logs immediately, later ones sampled. A sampled line carries
			//     ITS OWN increment, not the sum of the suppressed ones — the
			//     log is a sample, the wire notices are the complete record,
			//     which is the right way round since the notices are what the
			//     client's reset decision actually reads. The [ttymetrics]
			//     line rides the same gate because it is the same flood
			//     written by the same iteration (and stays const-gated:
			//     stable builds compile it away entirely);
			//   - the WIRE frame at ttyOverflowNoticeInterval (250ms): the
			//     first increment of the connection goes out immediately —
			//     a client must not wait to learn it has a hole — and after
			//     that at most four frames per second. That ceiling is what
			//     this gate exists for: ~150 notices/s in a storm each took
			//     ws.Conn's write mutex away from the binary chunk they
			//     precede, so the notice was amplifying the very congestion
			//     it reported.
			//
			// The gate never costs a count: the baseline advances and the
			// pending pair accumulates on EVERY sample, and only the write
			// below is gated. A pending pair whose window has not opened is
			// carried to the next frame, not dropped — Σ is complete by
			// construction. There is no case in which a pending pair is
			// never written: when output stops before this branch's next
			// chunk un-gates it, the HEARTBEAT case above flushes it — by
			// the next tick at the latest (see the R2 flush comment there),
			// so the client's screen/cursor honesty is guaranteed by the
			// flush, not by the hole being silent.
			if dropStats != nil {
				if chunks, bytes := dropStats.Snapshot(); chunks != dropsSeenChunks || bytes != dropsSeenBytes {
					dChunks, dBytes := chunks-dropsSeenChunks, bytes-dropsSeenBytes
					dropsSeenChunks, dropsSeenBytes = chunks, bytes
					pendingNoticeChunks += dChunks
					pendingNoticeBytes += dBytes
					if s.logger != nil && (lastDropNoticeLogAt.IsZero() || time.Since(lastDropNoticeLogAt) >= ttyDropLogInterval) {
						lastDropNoticeLogAt = time.Now()
						s.logger.Printf("tty output subscriber overflow for %s: consumer fell behind, overflow notice pending (wire rate %s): droppedChunks=%d droppedBytes=%d (drop-oldest, connection kept)", id, ttyOverflowNoticeInterval, dChunks, dBytes)
						if ttyMetricsBuild {
							if h, herr := s.instanceMgr.EndOffset(id); herr == nil {
								conn.m.logOverflow(s.logger, id, h)
							}
						}
					}
				}
				if pendingNoticeChunks > 0 || pendingNoticeBytes > 0 {
					if lastOverflowNoticeAt.IsZero() || time.Since(lastOverflowNoticeAt) >= ttyOverflowNoticeInterval {
						noticeChunks, noticeBytes := pendingNoticeChunks, pendingNoticeBytes
						// Cleared BEFORE the write, not after: a client that
						// dies on this write never gets the frame, and the
						// counts must not then be double-counted into the next
						// connection's first notice — the baseline above has
						// already moved past them, and the reconnecting client
						// re-establishes its own zero from a fresh subscription.
						pendingNoticeChunks, pendingNoticeBytes = 0, 0
						lastOverflowNoticeAt = time.Now()
						notice := []byte(`{"type":"overflow","droppedBytes":` + strconv.FormatInt(noticeBytes, 10) + `,"droppedChunks":` + strconv.FormatInt(noticeChunks, 10) + `}`)
						if err := conn.WriteText(notice); err != nil {
							return
						}
					}
				}
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
	if since == sinceFollowLiveEnd {
		// 400 rather than a silent tail (issue #86): this endpoint answers a
		// negative `since` with the tail, which is the EXACT OPPOSITE of what
		// the sentinel asks for — the sentinel means "I already painted the
		// tail, do not send it". Serving the tail here would reproduce the
		// duplication this value exists to prevent, invisibly. Follow-from-the-
		// live-end is a property of a LONG-LIVED stream (publish the head,
		// then keep streaming), not of a one-shot body, so it lives only on
		// /api/instances/log/stream and /api/instances/tty/ws. The shipped UI
		// never sends `since` here at all (loadLog deliberately omits it), so
		// rejecting is safe and turns a caller's mistake into an error.
		writeErr(w, http.StatusBadRequest, fmt.Errorf("since=%d (follow from the live end) is only valid on /api/instances/log/stream and /api/instances/tty/ws", sinceFollowLiveEnd))
		return
	}
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
	body, next, err := s.instanceMgr.Tail(id, 64*1024)
	if err != nil {
		writeErr(w, http.StatusBadRequest, err)
		return
	}
	w.Header().Set("X-Log-Offset", strconv.FormatInt(next, 10))
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	_, _ = io.WriteString(w, body)
}

func (s *Server) handleInstanceLogStream(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	id := strings.TrimSpace(r.URL.Query().Get("id"))
	since := parseInt64Default(r.URL.Query().Get("since"), -1)

	var initial string
	var next int64
	var err error
	if since == sinceFollowLiveEnd {
		// Follow from the live end (issue #86): the client's screen already
		// holds the tail and only its END OFFSET is unknown, so serve no
		// body at all — the tail it just painted must not be painted twice.
		// Publish the current head and let the poll loop below deliver only
		// bytes produced after this instant. EndOffset (not Tail) because a
		// 64KB copy thrown away here is pure waste; an empty `initial` needs
		// no new frame format, since writeSSELogEvent always writes
		// {"chunk":…,"next":…}.
		//
		// Nothing after this read is lost: the poll loop below reads from the
		// published cursor, not from a subscription, so bytes produced while
		// the client is still painting arrive as ordinary frames. What IS
		// lost is the window BEFORE it — [H1, H2), where H1 is the head of
		// the tail the client actually painted via GET /api/instances/log and
		// H2 is the head published here. The client adopts H2, so its cursor
		// runs ahead of its own screen and [H1, H2) is in no replay, no
		// painted body and no later frame; because the cursor is already past
		// them, no reconnect re-requests them either. Those bytes are
		// permanently gone from this client's screen — normally the
		// loadLog()→startSSE hop (milliseconds), but seconds if the WS
		// handshake times out first and SSE is the fallback. That is the
		// accepted cost of a stripped X-Log-Offset: the alternative is
		// repainting the whole tail over the screen that already shows it.
		next, err = s.instanceMgr.EndOffset(id)
	} else if since < 0 {
		initial, next, err = s.instanceMgr.Tail(id, 64*1024)
	} else {
		initial, next, err = s.instanceMgr.ReadSince(id, since, 64*1024)
	}
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
			ID:              inst.ID,
			Name:            inst.Name,
			WorktreeID:      inst.WorktreeID,
			WorktreeName:    inst.WorktreeName,
			PID:             inst.PID,
			Status:          inst.Status,
			BufferCapBytes:  s.instanceMgr.BufferCapBytesFor(inst.ID),
			BufferUsedBytes: s.instanceMgr.BufferUsedBytesFor(inst.ID),
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
			// Force: same semantics as /api/worktrees/delete (issue
			// #102) — delete even with uncommitted/untracked changes.
			Force bool `json:"force"`
		}
		if err := decodeArgs(req.Args, &args); err != nil {
			writeErr(w, http.StatusBadRequest, err)
			return
		}
		ignoredDestroyed, err := s.worktreeMgr.Delete(args.ID, args.Force)
		if err != nil {
			// Same structured refusal as /api/worktrees/delete (409 +
			// worktree_dirty + breakdown), so MCP clients can recognize
			// the dirty state programmatically instead of parsing a flat
			// 400 message (issue #102 review).
			var dirty *worktree.DirtyWorktreeError
			if errors.As(err, &dirty) {
				writeWorktreeDirtyErr(w, dirty)
				return
			}
			writeErr(w, http.StatusBadRequest, err)
			return
		}
		// As in the HTTP handler, ignored_destroyed is present only when
		// counted (the force path skips the probe).
		result := map[string]any{"status": "ok"}
		if !args.Force {
			result["ignored_destroyed"] = ignoredDestroyed
		}
		writeJSON(w, http.StatusOK, map[string]any{"result": result})
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
		item, err := s.instanceMgr.Start(r.Context(), framework.StartParams{
			WorktreeID: args.WorktreeID,
			TagID:      args.TagID,
			Name:       args.Name,
		})
		if err != nil {
			var budgetErr *framework.LogBufferBudgetError
			if errors.As(err, &budgetErr) {
				writeLogBufferBudgetErr(w, budgetErr)
				return
			}
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
		body, _, err := s.instanceMgr.Tail(args.ID, args.N)
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
		// Sync the address-bar token into the session cookie (remote
		// access / portal pattern). Same-origin iframes (reasonix /rx/,
		// opencode /__opencode/) navigate with relative URLs that carry
		// no query, so they can only authenticate via the cookie — the
		// server must hand it out. Refreshing it on every page
		// navigation that carries ?token= also makes auth-token
		// rotation self-heal without a re-login. (The token is never
		// appended to iframe URLs, so embedded documents never see it in
		// location.search; both reverse proxies additionally strip any
		// stray token before forwarding upstream.)
		if r.URL.Query().Get("token") != "" {
			http.SetCookie(w, &http.Cookie{
				Name:     "mw_token",
				Value:    token,
				Path:     "/",
				MaxAge:   86400,
				SameSite: http.SameSiteLaxMode,
				HttpOnly: true,
				Secure:   r.TLS != nil || r.Header.Get("X-Forwarded-Proto") == "https",
			})
		}
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
		// Issue #114 diagnostics: ttyMetricsBuild is true only in builds
		// compiled with -tags ttymetrics (the beta release workflow). This
		// cookie is the synchronous server→page signal that switches the
		// client half of the instrumentation on; stable builds never send
		// it and the page's [tty-metrics] logging stays silent. Set only
		// when absent so the cost is one cookie per browser, not per
		// response; deliberately NOT HttpOnly (page JS must read it) and
		// NOT Secure (the UI is routinely served over plain http on LANs).
		if ttyMetricsBuild {
			if _, err := r.Cookie("mw_tty_metrics"); err != nil {
				http.SetCookie(w, &http.Cookie{
					Name:     "mw_tty_metrics",
					Value:    "1",
					Path:     "/",
					SameSite: http.SameSiteLaxMode,
				})
			}
		} else if _, err := r.Cookie("mw_tty_metrics"); err == nil {
			// Stable build holding a stale cookie: it was planted by a beta
			// daemon on this origin earlier in the same browser session
			// (session-scoped, so it survives a daemon restart), and leaving
			// it would arm the page's [tty-metrics] half with no server-side
			// counterpart — console noise with zero data value. Clear it.
			http.SetCookie(w, &http.Cookie{
				Name:     "mw_tty_metrics",
				Value:    "",
				Path:     "/",
				MaxAge:   -1,
				SameSite: http.SameSiteLaxMode,
			})
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

// writeLogBufferBudgetErr writes a structured 503 response when a new instance
// was rejected for exceeding the global log buffer budget. The "error" code
// "log_buffer_budget_exceeded" is part of the API contract — the dashboard
// matches on it to display a popup instead of a generic error.
func writeLogBufferBudgetErr(w http.ResponseWriter, err *framework.LogBufferBudgetError) {
	body := map[string]any{
		"error":        "log_buffer_budget_exceeded",
		"message":      fmt.Sprintf("Insufficient memory to start new instance: used %s, limit %s.", formatBytes(err.UsedBytes), formatBytes(err.LimitBytes)),
		"used_bytes":   err.UsedBytes,
		"limit_bytes":  err.LimitBytes,
		"system_bytes": err.SystemBytes,
		"hint":         "Close other instances, raise LogBufferBytes in auth.json, or reduce concurrent tabs.",
	}
	w.Header().Set("Retry-After", "0")
	writeJSON(w, http.StatusServiceUnavailable, body)
}

func formatBytes(b int64) string {
	const (
		kb = 1 << 10
		mb = 1 << 20
		gb = 1 << 30
	)
	switch {
	case b >= gb:
		return fmt.Sprintf("%.2f GB", float64(b)/float64(gb))
	case b >= mb:
		return fmt.Sprintf("%.2f MB", float64(b)/float64(mb))
	case b >= kb:
		return fmt.Sprintf("%.2f KB", float64(b)/float64(kb))
	default:
		return fmt.Sprintf("%d B", b)
	}
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

// ttyClientHasCap reports whether the tty WS handshake's `caps` query
// parameter — a comma-separated capability list (issue #98) that may be
// REPEATED, ?caps=pong&caps=ping — contains name as a WHOLE,
// case-sensitive token. Substring matching is deliberately not enough:
// "pinger" must not opt a client into the TEXT {"type":"ping"} heartbeat,
// and "PING" names no capability.
func ttyClientHasCap(caps []string, name string) bool {
	for _, v := range caps {
		for _, tok := range strings.Split(v, ",") {
			if strings.TrimSpace(tok) == name {
				return true
			}
		}
	}
	return false
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

// isPathWithin returns true if filePath resolves inside worktreeRoot,
// preventing path-traversal attacks.
func isPathWithin(worktreeRoot, filePath string) bool {
	absRoot, err := filepath.Abs(worktreeRoot)
	if err != nil {
		return false
	}
	absPath, err := filepath.Abs(filepath.Join(worktreeRoot, filePath))
	if err != nil {
		return false
	}
	sep := string(filepath.Separator)
	return strings.HasPrefix(absPath, absRoot+sep) || absPath == absRoot
}

// isSensitiveFile returns true if the file should not be previewed
// because it likely contains secrets or credentials.
func isSensitiveFile(path string) bool {
	name := strings.ToLower(filepath.Base(path))
	ext := strings.ToLower(filepath.Ext(name))
	// Sensitive extensions
	switch ext {
	case ".env", ".pem", ".key", ".pfx", ".p12":
		return true
	}
	// SSH private key filenames
	switch name {
	case "id_rsa", "id_ed25519", "id_ecdsa", "id_dsa",
		"id_rsa.pub", "id_ed25519.pub", "id_ecdsa.pub", "id_dsa.pub":
		// Allow .pub (public keys are safe), reject private key variants
		return !strings.HasSuffix(name, ".pub")
	}
	// .env and .env.* variants
	if name == ".env" || strings.HasPrefix(name, ".env.") {
		return true
	}
	// Sensitive keywords in path
	lower := strings.ToLower(path)
	for _, kw := range []string{"secret", "token", "credential", "password", "private"} {
		if strings.Contains(lower, kw) {
			return true
		}
	}
	return false
}

// detectFileType returns a hint about the file type based on its extension.
func detectFileType(path string) string {
	ext := strings.ToLower(filepath.Ext(path))
	switch ext {
	case ".md", ".markdown":
		return "text/markdown"
	case ".go", ".js", ".ts", ".tsx", ".jsx", ".py", ".rs", ".java", ".c", ".cpp", ".h", ".hpp",
		".html", ".css", ".scss", ".less", ".sass", ".json", ".yaml", ".yml", ".toml", ".sh", ".bash", ".zsh",
		".vue", ".svelte", ".xml", ".svg", ".sql", ".dockerfile":
		return "text/x-code"
	default:
		return "text/plain"
	}
}

// unquoteGitPath reverses git's core.quotePath quoting.
//
// When core.quotePath=true (git's default), git wraps file paths containing
// non-ASCII or special characters in double quotes and escapes \t, \n, \\,
// \" and non-ASCII bytes as \ooo octal sequences.
//
// The primary fix is passing -c core.quotePath=false to every git command
// that produces file paths (see handleWorktreeStatus, handleWorktreeFileDiff).
// This function is a defense-in-depth safety net: if a future contributor
// forgets -c on a new call site, or if an older git binary ignores the flag,
// the path will still be corrected before it reaches API consumers.
//
// Callers that parse git output should always apply unquoteGitPath.
// Do NOT rely on -c core.quotePath=false alone — use both layers.
func unquoteGitPath(p string) string {
	if len(p) < 2 || p[0] != '"' || p[len(p)-1] != '"' {
		return p
	}
	s := p[1 : len(p)-1]
	var b strings.Builder
	b.Grow(len(s))
	for i := 0; i < len(s); i++ {
		if s[i] == '\\' && i+1 < len(s) {
			switch s[i+1] {
			case '\\':
				b.WriteByte('\\')
				i++
			case '"':
				b.WriteByte('"')
				i++
			case 't':
				b.WriteByte('\t')
				i++
			case 'n':
				b.WriteByte('\n')
				i++
			default:
				if s[i+1] >= '0' && s[i+1] <= '7' {
					val := int(s[i+1] - '0')
					i++
					if i+1 < len(s) && s[i+1] >= '0' && s[i+1] <= '7' {
						val = val*8 + int(s[i+1]-'0')
						i++
						if i+1 < len(s) && s[i+1] >= '0' && s[i+1] <= '7' {
							val = val*8 + int(s[i+1]-'0')
							i++
						}
					}
					b.WriteByte(byte(val))
				} else {
					b.WriteByte('\\')
				}
			}
		} else {
			b.WriteByte(s[i])
		}
	}
	return b.String()
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

		path := unquoteGitPath(strings.TrimSpace(parts[2]))
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
