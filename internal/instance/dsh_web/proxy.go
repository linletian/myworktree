package dsh_web

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"net/http/httputil"
	"net/url"
	"strings"
	"time"

	"myworktree/internal/authq"
)

// The per-instance reverse proxy (PLAN.md §嵌入形态). Each dsh-web
// instance gets its OWN listener — a dedicated origin — because the
// dsh SPA hardcodes its API base to location.origin + '/api' with no
// override (packages/client/connection/src/api-path.ts), so a
// same-origin subpath mount would collide with myworktree's own /api.
//
//   - Local mode (default): binds 127.0.0.1:<free> and requires no
//     token — the same loopback trust model as the main UI.
//   - Remote mode (main listener non-loopback, or TLS): binds the main
//     listener's host and REQUIRES the myworktree token from every
//     non-loopback client (?token= or the mw_token cookie, WebSocket
//     upgrades included). Loopback clients bypass the gate — a local
//     browser must keep working even when the proxy binds 0.0.0.0
//     (the default main listener), exactly like the main UI's loopback
//     auth bypass. dsh has no authentication layer, so the proxy is
//     the only boundary between the LAN and the unauthenticated
//     upstream (settings/credentials become reachable once Host is
//     rewritten to loopback). The gate is hardcoded and cannot be
//     disabled.
//   - Token hygiene (remote mode): the FIRST navigation carries
//     ?token= — the main-origin mw_token cookie cannot travel to the
//     proxy origin. On a valid query token the proxy sets the
//     HttpOnly mw_token cookie on ITS origin and 302-redirects to the
//     token-free URL, so the embedded document never retains the
//     myworktree token in its own location.search (ARCHITECTURE §9
//     checklist). The Director additionally strips ?token= before
//     forwarding upstream.
//
// The /api trust fence (upstream api-request-trust.ts) requires the
// request Host to be loopback/trusted and any browser markers to be
// same-origin; the Director therefore rewrites Host to the upstream
// loopback authority and deletes the browser Origin (absent Origin is
// fine; sec-fetch-site passes through — the SPA's own requests are
// same-origin with the proxy, so it never carries "cross-site").
//
// Scope monitoring (PLAN.md §工作区限制): POST /api RPC bodies are
// parsed for workspace.create / session.create and recorded in the
// ScopeTracker — record-only, never blocking or rewriting.

// ProxyConfig is the myworktree-side configuration for the per-instance
// listener. Built by app.New from the main listener settings.
type ProxyConfig struct {
	// BindHost: "127.0.0.1" locally; the main listener's host when
	// non-loopback (remote mode).
	BindHost string
	// RequireToken: mandatory token gate (remote mode / TLS).
	RequireToken bool
	// AuthToken: the myworktree token the gate validates against.
	AuthToken string
	// TLSCert / TLSKey: mirror the main listener's TLS (avoids mixed
	// content for https pages). Empty = plain HTTP.
	TLSCert string
	TLSKey  string
}

// maxScopeBodyBytes caps the RPC body read for scope observation. dsh
// RPC payloads are tiny (paths, ids); anything larger is not a
// workspace/session call worth observing and is forwarded untouched.
const maxScopeBodyBytes = 16 << 20 // 16 MiB

// ProxyStarterFn returns the ProxyStarter implementation for this
// driver: it starts the per-instance listener (bound per Driver.Proxy)
// once the upstream listening address is known (called by
// pumpAndWatch). app.New assigns it to Driver.ProxyStarter.
func (d *Driver) ProxyStarterFn() func(h *Handle, upstreamHost, upstreamPort string) (string, string, func(), error) {
	cfg := d.Proxy
	logger := d.Logger
	return func(h *Handle, upstreamHost, upstreamPort string) (string, string, func(), error) {
		return startProxyListener(h, cfg, upstreamHost, upstreamPort, d.Tracker, logger)
	}
}

func startProxyListener(h *Handle, cfg ProxyConfig, upstreamHost, upstreamPort string, tracker *ScopeTracker, logger *log.Logger) (string, string, func(), error) {
	ln, err := net.Listen("tcp", net.JoinHostPort(cfg.BindHost, "0"))
	if err != nil {
		return "", "", nil, fmt.Errorf("dsh: proxy listen on %s: %w", cfg.BindHost, err)
	}
	host, port, _ := net.SplitHostPort(ln.Addr().String())

	ph := &proxyHandler{
		upstreamHost: upstreamHost,
		upstreamPort: upstreamPort,
		worktree:     h.cwd,
		instanceID:   h.instanceID,
		cfg:          cfg,
		tracker:      tracker,
		h:            h,
	}
	srv := &http.Server{Handler: ph}
	go func() {
		var serveErr error
		if cfg.TLSCert != "" && cfg.TLSKey != "" {
			serveErr = srv.ServeTLS(ln, cfg.TLSCert, cfg.TLSKey)
		} else {
			serveErr = srv.Serve(ln)
		}
		if serveErr != nil && !errors.Is(serveErr, http.ErrServerClosed) {
			// The listener died on its own (e.g. a TLS certificate
			// that fails to load after a reload). The health loop
			// probes only the UPSTREAM, so a dead proxy would stay
			// invisible — fail loud instead: log, mark the instance
			// failed, and drop the dead iframe URL.
			if logger != nil {
				logger.Printf("dsh-web instance %s: reverse proxy listener died: %v", h.instanceID, serveErr)
			}
			h.markProxyDead("dsh reverse proxy listener died: " + serveErr.Error())
		}
	}()
	closeFn := func() {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		_ = srv.Shutdown(ctx)
		_ = ln.Close()
	}
	return host, port, closeFn, nil
}

type proxyHandler struct {
	upstreamHost string
	upstreamPort string
	worktree     string
	instanceID   string
	cfg          ProxyConfig
	tracker      *ScopeTracker
	h            *Handle
}

func (p *proxyHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	// Token gate (remote mode): validate before any byte is forwarded.
	// Loopback clients skip the gate — the same trust model as the main
	// UI's loopback bypass (a local browser must reach the embed even
	// when the proxy binds the main listener's non-loopback host).
	if p.cfg.RequireToken && !loopbackClient(r) && !p.checkToken(w, r) {
		writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "unauthorized"})
		return
	}
	// A query token is only ever valid for the FIRST navigation (the
	// frontend appends it to the iframe src); checkToken synced the
	// proxy-origin HttpOnly cookie. Redirect to the token-free URL so
	// the embedded document never retains the token in its own
	// location.search. Non-GET requests never carry the token (the SPA
	// does not know it), so the redirect is limited to navigations.
	if p.cfg.RequireToken && r.URL.Query().Get("token") != "" &&
		(r.Method == http.MethodGet || r.Method == http.MethodHead) {
		q := r.URL.Query()
		q.Del("token")
		u := *r.URL
		u.RawQuery = q.Encode()
		http.Redirect(w, r, u.RequestURI(), http.StatusFound)
		return
	}

	// Scope observation (record-only): the SPA's RPC calls are
	// application/json POSTs under /api; anything else (assets, WS
	// upgrades) is forwarded untouched.
	if r.Method == http.MethodPost && strings.HasPrefix(r.URL.Path, "/api") &&
		strings.Contains(r.Header.Get("Content-Type"), "application/json") {
		p.observeBody(r)
	}

	target := &url.URL{Scheme: "http", Host: net.JoinHostPort(p.upstreamHost, p.upstreamPort)}
	proxy := httputil.NewSingleHostReverseProxy(target)
	proxy.FlushInterval = -1 // stream SSE / WS frames as they arrive

	origDirector := proxy.Director
	proxy.Director = func(req *http.Request) {
		origDirector(req)
		req.URL.Scheme = target.Scheme
		req.URL.Host = target.Host
		// The /api trust fence keys on Host: it must name the upstream
		// loopback authority, or the fence 403s.
		req.Host = target.Host
		// Never forward myworktree's auth query (?token=...) upstream:
		// it is a credential leak to the dsh subprocess. authq is the
		// single shared stripping path for every proxy.
		req.URL.RawQuery = authq.StripToken(req.URL.RawQuery)
		// The fence rejects an Origin that differs from the (rewritten)
		// Host; the browser's Origin names the PROXY origin, so it must
		// go. Absent Origin is fine.
		req.Header.Del("Origin")
	}

	proxy.ErrorHandler = func(w http.ResponseWriter, req *http.Request, err error) {
		writeJSON(w, http.StatusBadGateway, map[string]string{
			"error": "dsh server unreachable",
		})
	}

	proxy.ServeHTTP(w, r)
}

// checkToken validates the myworktree token: ?token= query or the
// mw_token cookie. On a query-token success it syncs the cookie so
// subsequent same-origin requests (and WebSocket upgrades, which carry
// cookies) authenticate without the token in the URL — ServeHTTP then
// redirects the first navigation to a token-free URL. The query token
// itself is stripped by the Director before forwarding.
func (p *proxyHandler) checkToken(w http.ResponseWriter, r *http.Request) bool {
	token := r.URL.Query().Get("token")
	if token == "" {
		if c, err := r.Cookie("mw_token"); err == nil {
			token = c.Value
		}
	}
	if token == "" || token != p.cfg.AuthToken {
		return false
	}
	if r.URL.Query().Get("token") != "" {
		http.SetCookie(w, &http.Cookie{
			Name:     "mw_token",
			Value:    token,
			Path:     "/",
			MaxAge:   86400,
			SameSite: http.SameSiteLaxMode,
			HttpOnly: true,
			Secure:   p.cfg.TLSCert != "",
		})
	}
	return true
}

// loopbackClient reports whether the request's remote address is a
// loopback address (the client is on the same machine and therefore
// inside the main UI's loopback trust zone).
func loopbackClient(r *http.Request) bool {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return false
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}

// observeBody reads the RPC body, classifies it against the worktree,
// and records the scope state. The body is always restored for
// forwarding (the stream was consumed). A body that cannot be read or
// exceeds the cap is NOT observed and the request is passed through
// unchanged as far as possible (a read failure means the request
// stream is broken; the upstream will see a short body and error).
func (p *proxyHandler) observeBody(r *http.Request) {
	body, err := io.ReadAll(io.LimitReader(r.Body, maxScopeBodyBytes+1))
	if err != nil {
		return
	}
	if len(body) > maxScopeBodyBytes {
		// Oversized: not a plausible RPC envelope. Stitch the buffered
		// prefix back onto the untouched tail so the upstream receives
		// the request exactly as sent (no truncation).
		r.Body = io.NopCloser(io.MultiReader(bytes.NewReader(body), r.Body))
		return
	}
	r.Body = io.NopCloser(bytes.NewReader(body))
	st, ok := classifyRPCBody(body, p.worktree, p.worktreeWorkspaceID())
	if !ok {
		return
	}
	if p.tracker != nil {
		p.tracker.Record(p.instanceID, st)
	}
}

// worktreeWorkspaceID reads the worktree's own workspace id learned by
// the bootstrap (may still be empty while bootstrap is in flight).
func (p *proxyHandler) worktreeWorkspaceID() string {
	p.h.mu.Lock()
	defer p.h.mu.Unlock()
	return p.h.blob.WorkspaceID
}

// rpcEnvelope is the wire form of a client request (upstream
// rpc.schema.ts): {type:'client-request', rpcId, method, payload}.
type rpcEnvelope struct {
	Type    string          `json:"type"`
	Method  string          `json:"method"`
	Payload json.RawMessage `json:"payload"`
}

// classifyRPCBody parses a client-request envelope and classifies the
// workspace/session targets against the instance worktree. Non-RPC
// bodies and irrelevant methods are ignored (ok=false).
func classifyRPCBody(body []byte, worktree, worktreeWorkspaceID string) (ScopeState, bool) {
	var env rpcEnvelope
	if err := json.Unmarshal(body, &env); err != nil {
		return ScopeState{}, false
	}
	if env.Type != "client-request" {
		return ScopeState{}, false
	}
	switch env.Method {
	case "workspace.create":
		var payload struct {
			Path string `json:"path"`
		}
		if json.Unmarshal(env.Payload, &payload) != nil || payload.Path == "" {
			return ScopeState{}, false
		}
		return classifyRPC(worktree, worktreeWorkspaceID, payload.Path, "")
	case "session.create":
		var payload struct {
			Cwd         string `json:"cwd"`
			WorkspaceID string `json:"workspaceId"`
		}
		if json.Unmarshal(env.Payload, &payload) != nil {
			return ScopeState{}, false
		}
		return classifyRPC(worktree, worktreeWorkspaceID, payload.Cwd, payload.WorkspaceID)
	default:
		return ScopeState{}, false
	}
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}
