//go:build ignore

// dsh-mock-modern emulates a MODERN `dsh web` (>= 0.2.0, the remote
// floor) for integration tests — the browser-session token gate and
// the unified /api/remote.mux WebSocket endpoint:
//
//	--version                    → "0.2.0-rc.2" (remote-capable core 0.2.0)
//	web --patch F --dump-config  → "# == dump" + contents of F
//	web --patch F --host 127.0.0.1 --port 0 --no-open → prints the ready
//	                                 line WITH a launch token
//	                                 ("dsh web: http://…/?token=test-token"),
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
// Both web invocations are the exact argv the driver emits — launch.go's
// dumpArgs and webArgs. What THIS mock validates is the flag SET
// (knownWebFlags membership), NOT the ORDER — it would happily accept
// `--host 127.0.0.1 --port 0 --no-open --patch F`. The ORDER is the
// load-bearing part of the real contract (the real launcher parses with
// allowUnknownOption + passThroughOptions, so the launcher-level --patch
// must come FIRST and the web-app options (--host / --port / --no-open)
// stay grouped after it — PLAN.md §踩坑) and is pinned by launch_test.go's
// TestWebArgs / TestDumpArgs, independently of the expected slices.
//
// Anything after `web` outside the recognised flag set (knownWebFlags)
// fails like commander does: "unknown option '--x'" on stderr, non-zero
// exit — see checkWebFlags.
//
// The RPC surface is the dsh 0.2.x wire: `<namespace>/<method>`
// endpoints with the verb's object argument nested at
// payload.args.request (see handleRPC).
//
// The ungated variant (dsh-mock.go) differs ONLY in the auth shape: no
// token in the ready line, no gate. Both speak the 0.2.x wire and both
// report a 0.2.x version — 0.1.x is out of support.
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
	mockVersion   = "0.2.0-rc.2"
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
	// Flag contract — see knownWebFlags.
	checkWebFlags(rest)
	for i, a := range rest {
		if a == "--patch" && i+1 < len(rest) {
			serve()
			return
		}
	}
	fmt.Println("mock dsh (modern): web without --patch/--dump-config")
	os.Exit(1)
}

// knownWebFlags / valueTakingWebFlags are the mock's FLAG CONTRACT with
// the real dsh — the same set dsh-mock.go documents and enforces: the
// web-app options of 0.2.0-rc.2 (`dsh web --help`: --host / --port /
// --no-open / --patch / --trusted-host) plus the launcher-level
// --dump-config. Anything else is rejected exactly like commander does —
// `unknown option '--x'` on stderr, exit 1 — because that is what makes a
// real boot fail; a mock that swallowed unknown flags would keep the
// token-gated integration tests green while every real Start died.
var (
	knownWebFlags = map[string]bool{
		"--patch":        true,
		"--host":         true,
		"--port":         true,
		"--no-open":      true,
		"--trusted-host": true,
		"--dump-config":  true, // handled by the dump branch before this runs
	}
	// valueTakingWebFlags: the ONE token AFTER a single-value flag is
	// its VALUE and must not be validated as a flag (a path is not an
	// option). --no-open takes no value, so it is deliberately absent —
	// as is --dump-config, which takes none either and is consumed by
	// the dump branch before this ever runs. --trusted-host is NOT in
	// this map: real 0.2.x declares it variadic/repeatable
	// (`dsh web --trusted-host <authority…>`), so it lives in
	// variadicWebFlags and consumes every token up to the next option.
	valueTakingWebFlags = map[string]bool{
		"--patch": true,
		"--host":  true,
		"--port":  true,
	}
	// variadicWebFlags: repeatable value flags — commander's variadic
	// `--trusted-host <authority…>` takes any number of space-separated
	// authorities, so the validator skips every token after the flag
	// until the next `--`-prefixed option or the end of argv.
	variadicWebFlags = map[string]bool{
		"--trusted-host": true,
	}
)

// checkWebFlags validates the `web` subcommand's argv against the
// contract above.
func checkWebFlags(rest []string) {
	for i := 0; i < len(rest); i++ {
		tok := rest[i]
		if !strings.HasPrefix(tok, "--") {
			continue // positional argument, or the value of a flag before it
		}
		name, inline := tok, false
		if j := strings.IndexByte(tok, '='); j >= 0 {
			name, inline = tok[:j], true // --port=0 spelling
		}
		if !knownWebFlags[name] {
			fmt.Fprintf(os.Stderr, "mock dsh (modern): unknown option '%s'\n", tok)
			os.Exit(1)
		}
		if variadicWebFlags[name] && !inline {
			// Variadic: consume every following token until the next
			// `--` option or the end of argv.
			for i+1 < len(rest) && !strings.HasPrefix(rest[i+1], "--") {
				i++
			}
		} else if valueTakingWebFlags[name] && !inline {
			i++ // skip the single value token
		}
	}
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
		_, _ = w.Write([]byte(`<!doctype html><html><head><title>mock dsh (modern)</title></head><body>mock</body></html>`))
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

// handleRPC is behaviourally identical to the ungated mock's (identical
// routing and payload.args.request contract; this file uses the
// rpcPathPrefix constant, the other a "/api/" literal): the route table
// registers `/api/<namespace>/<method>` only (the 0.1.x dotted
// `/api/workspace.create` has no route and 404s at the HTTP layer), the
// envelope's method must equal it; the verb's single object argument is
// nested at payload.args.request (dsh 0.2.x — anything else the real
// gateway rejects with "Remote payload must contain exactly one
// plain-object args field", mirrored here). workspace/create adopts the
// given path (workspaceId = "mock-ws-<hash>"), session/create succeeds
// with a dummy value, and the eight write verbs proxy.go's
// ownSessionWriteMethods observes answer ok:true with a small value.
func handleRPC(w http.ResponseWriter, r *http.Request) {
	endpoint := strings.TrimPrefix(r.URL.Path, rpcPathPrefix)
	// Routing check FIRST, before the envelope is even decoded: on 0.2 a
	// dotted endpoint never reaches the RPC layer at all, so a regression
	// to the 0.1 names must fail as a routing miss (404) and not as a
	// confusing payload complaint. /api/remote.mux is served by an exact
	// pattern and never reaches here.
	if !strings.Contains(endpoint, "/") {
		w.WriteHeader(http.StatusNotFound)
		_, _ = w.Write([]byte(`{"error":"not found"}`))
		return
	}
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
	var payload struct {
		Args struct {
			Request json.RawMessage `json:"request"`
		} `json:"args"`
	}
	if err := json.Unmarshal(env.Payload, &payload); err != nil || len(payload.Args.Request) == 0 {
		writeRPCError(w, env.RPCID, "gateway/internal", "Remote payload must contain exactly one plain-object args field")
		return
	}

	var value any
	switch env.Method {
	case "workspace/create":
		var p struct {
			Path string `json:"path"`
		}
		_ = json.Unmarshal(payload.Args.Request, &p)
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
	case "session/create":
		value = map[string]any{"sessionId": "mock-session"}
	// Hand-maintained MIRROR of the upstream verb table — same table,
	// same caveats as dsh-mock.go. The source of truth for these eight
	// verb names is the `@Remote('<verb>')` decorators in the dsh 0.2.x
	// remote controllers' typert.host.js — session: prompt / cancel /
	// fork / rename / selectModel / attachment / updateQueue; subagents
	// (PLURAL): prompt. This hand-written copy must be updated in the
	// same change as proxy.go's ownSessionWriteMethods; an upstream
	// rename is NOT detected automatically (the map, both mocks and the
	// test loop would go stale together and no test turns red). What IS
	// guarded is internal consistency — a drifted copy makes proxy,
	// mocks and test disagree: the write-verb POSTs of the loop in
	// TestDshWebRemoteEndToEnd (integration_remote_test.go — NOT
	// TestDshWebProxyEndToEnd, which builds dsh-mock.go, not this
	// file) then answer method-not-found instead of ok:true and fail
	// CI. The one automatic pin is TestProxySessionOwnMarking's
	// bidirectional map↔table check (proxy.go's map ↔ proxy_test.go's
	// literal table).
	case "session/prompt", "session/cancel", "session/fork", "session/rename",
		"session/selectModel", "session/attachment", "session/updateQueue",
		"subagents/prompt":
		value = map[string]any{"accepted": true}
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
