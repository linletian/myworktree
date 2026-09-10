//go:build ignore

// dsh-mock-modern emulates a MODERN `dsh web` (>= 0.1.5, the remote
// floor) for integration tests — the browser-session token gate and
// the unified /api/remote.mux WebSocket endpoint:
//
//	--version                    → "0.1.5-rc.1" (remote-capable core 0.1.5)
//	web --dump-config --patch F  → "# == dump" + contents of F
//	web --host … --port … --patch F → prints the ready line WITH a
//	                                 launch token ("dsh web: http://…/?token=test-token"),
//	                                 then:
//	                                 GET /?token=test-token → 303 + Set-Cookie
//	                                   dsh-auth-test=ok (the token exchange)
//	                                 GET / → 200 (health probe / SPA)
//	                                 ALL /api/* → 401 unless
//	                                   Cookie: dsh-auth-test=ok
//	                                 /api/remote.mux (cookie ok, WS
//	                                   upgrade) → echoes text/binary
//	                                   frames back to the sender
//
// The legacy variant (dsh-mock.go) keeps the pre-0.1.2 behavior: no
// token in the ready line, no gate.
//
// Usage:
//
//	go build -o /path/to/mock-dsh-modern internal/instance/dsh_web/testdata/dsh-mock-modern.go
package main

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"syscall"

	"myworktree/internal/ws"
)

const (
	launchToken   = "test-token"
	cookieName    = "dsh-auth-test"
	cookieValue   = "ok"
	mockVersion   = "0.1.5-rc.1"
	muxPath       = "/api/remote.mux"
	rpcPathPrefix = "/api/"
)

func main() {
	if len(os.Args) < 2 {
		fmt.Println("mock dsh (modern): use --version or web subcommand")
		os.Exit(1)
	}
	if os.Args[1] == "--version" || os.Args[1] == "-V" {
		fmt.Println(mockVersion)
		return
	}
	if os.Args[1] != "web" {
		fmt.Println("mock dsh (modern): unknown subcommand " + os.Args[1])
		os.Exit(1)
	}
	rest := os.Args[2:]
	// Scan the WHOLE argv for --dump-config first (see dsh-mock.go).
	for _, a := range rest {
		if a == "--dump-config" {
			dump(rest)
			return
		}
	}
	for i, a := range rest {
		if a == "--patch" && i+1 < len(rest) {
			serve()
			return
		}
	}
	fmt.Println("mock dsh (modern): web without --patch/--dump-config")
	os.Exit(1)
}

// dump mirrors the legacy mock: the composed-tree output carries the
// overlay rows from the patch file itself.
func dump(args []string) {
	var patch string
	for i, a := range args {
		if a == "--patch" && i+1 < len(args) {
			patch = args[i+1]
		}
	}
	b, err := os.ReadFile(patch)
	if err != nil {
		fmt.Fprintln(os.Stderr, "mock dsh (modern): read patch:", err)
		os.Exit(1)
	}
	fmt.Println("# == dump (mock), patched by " + patch)
	fmt.Print(strings.TrimRight(string(b), "\n"))
	fmt.Println()
}

// serve prints the token-bearing ready line and serves the gated API
// until a termination signal arrives, then closes the listener so the
// port is released.
func serve() {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		panic(err)
	}
	addr := listener.Addr().String()
	host, port, _ := net.SplitHostPort(addr)

	// Ready line scanned by dsh_web.pumpAndWatch (extractListeningAddress).
	fmt.Printf("dsh web: http://%s:%s/?token=%s\n", host, port, launchToken)

	mux := http.NewServeMux()
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		// The launch-token exchange (browser-auth.ts authorizeIndex):
		// a valid token mints the browser-session cookie and 303s to
		// the token-free URL. Ungated by design — it IS the mint.
		if r.URL.Path == "/" && r.Method == http.MethodGet &&
			r.URL.Query().Get("token") == launchToken {
			http.SetCookie(w, &http.Cookie{
				Name:     cookieName,
				Value:    cookieValue,
				Path:     "/",
				HttpOnly: true,
			})
			http.Redirect(w, r, "/", http.StatusSeeOther)
			return
		}
		if strings.HasPrefix(r.URL.Path, rpcPathPrefix) {
			if !authed(r) {
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(http.StatusUnauthorized)
				_, _ = w.Write([]byte(`{"error":"unauthorized"}`))
				return
			}
			if r.URL.Path == muxPath {
				handleMux(w, r)
				return
			}
			if r.Method == http.MethodPost {
				handleRPC(w, r)
				return
			}
			w.WriteHeader(http.StatusNotFound)
			return
		}
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`<!doctype html><title>mock dsh (modern)</title>`))
	})

	sig := make(chan os.Signal, 1)
	signal.Notify(sig, syscall.SIGTERM, syscall.SIGINT)

	go func() {
		_ = http.Serve(listener, mux)
	}()

	<-sig
	listener.Close()
}

// authed reports whether the request carries the minted browser-session
// cookie.
func authed(r *http.Request) bool {
	c, err := r.Cookie(cookieName)
	return err == nil && c.Value == cookieValue
}

// handleMux upgrades /api/remote.mux to a WebSocket and echoes data
// frames back (text → text, binary → binary) until the peer closes or
// errors. Ping is answered with pong; close is echoed and ends the
// loop.
func handleMux(w http.ResponseWriter, r *http.Request) {
	conn, err := ws.Upgrade(w, r)
	if err != nil {
		http.Error(w, "upgrade required", http.StatusBadRequest)
		return
	}
	defer conn.Close()
	for {
		opcode, payload, err := conn.ReadMessage()
		if err != nil {
			return
		}
		switch {
		case ws.IsPing(opcode):
			if err := conn.WritePong(payload); err != nil {
				return
			}
		case ws.IsClose(opcode):
			_ = conn.WriteClose(payload)
			return
		case ws.IsDataOpcode(opcode):
			if opcode == 0x1 {
				err = conn.WriteText(payload)
			} else {
				err = conn.WriteBinary(payload)
			}
			if err != nil {
				return
			}
		}
	}
}

// handleRPC is byte-identical to the legacy mock's: the endpoint comes
// from the URL path and the envelope's method must equal it.
// workspace.create adopts the given path (workspaceId =
// "mock-ws-<hash>"), session.create succeeds with a dummy value.
func handleRPC(w http.ResponseWriter, r *http.Request) {
	endpoint := strings.TrimPrefix(r.URL.Path, rpcPathPrefix)
	var env struct {
		Type    string          `json:"type"`
		RPCID   string          `json:"rpcId"`
		Method  string          `json:"method"`
		Payload json.RawMessage `json:"payload"`
	}
	if err := json.NewDecoder(r.Body).Decode(&env); err != nil {
		w.WriteHeader(http.StatusBadRequest)
		_, _ = w.Write([]byte(`{"error":"bad envelope"}`))
		return
	}
	if env.Method != endpoint {
		writeRPCError(w, env.RPCID, "bad-request", "method "+env.Method+" does not match endpoint "+endpoint)
		return
	}
	var value any
	switch env.Method {
	case "workspace.create":
		var p struct {
			Path string `json:"path"`
		}
		_ = json.Unmarshal(env.Payload, &p)
		sum := sha256.Sum256([]byte(p.Path))
		value = map[string]any{
			"workspace": map[string]any{
				"workspaceId": "mock-ws-" + hex.EncodeToString(sum[:4]),
				"path":        p.Path,
				"title":       p.Path,
				"sessionIds":  []string{},
				"createdAt":   "2026-08-15T00:00:00Z",
				"updatedAt":   "2026-08-15T00:00:00Z",
			},
			"created": true,
		}
	case "session.create":
		value = map[string]any{"sessionId": "mock-session"}
	default:
		writeRPCError(w, env.RPCID, "method-not-found", "unknown method "+env.Method)
		return
	}
	resp := map[string]any{
		"type":   "server-response",
		"rpcId":  env.RPCID,
		"result": map[string]any{"ok": true, "value": value},
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(resp)
}

func writeRPCError(w http.ResponseWriter, rpcID, code, message string) {
	resp := map[string]any{
		"type":   "server-response",
		"rpcId":  rpcID,
		"result": map[string]any{"ok": false, "error": map[string]any{"code": code, "message": message}},
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(resp)
}
