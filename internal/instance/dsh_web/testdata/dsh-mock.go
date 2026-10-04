//go:build ignore

// dsh-mock is a tiny drop-in binary that emulates `dsh web` just
// enough for integration tests. It handles:
//
//	--version                    → "0.2.0-rc.2"
//	web --patch F --dump-config  → "# == dump" + contents of F
//	web --patch F --host 127.0.0.1 --port 0 --no-open → prints the
//	                                 "dsh web: http://…" ready line, serves
//	                                 GET / with 200, and exits on
//	                                 SIGTERM/SIGINT
//
// Both web invocations are the exact argv the driver emits — launch.go's
// dumpArgs and webArgs. What THIS mock validates is the flag SET
// (knownWebFlags membership); it does NOT validate ORDER — it would
// happily accept `--host 127.0.0.1 --port 0 --no-open --patch F`. The
// ORDER is the load-bearing part of the real contract (the real dsh
// launcher parses with allowUnknownOption + passThroughOptions, so the
// launcher-level --patch must come BEFORE the web-app options (--host /
// --port / --no-open), which must stay grouped after it — PLAN.md §踩坑)
// and is pinned by launch_test.go's TestWebArgs / TestDumpArgs, which
// assert the exact vectors independently of the expected slices.
//
// Anything after `web` that is not in the recognised flag set
// (knownWebFlags) fails like commander does: "unknown option '--x'" on
// stderr, non-zero exit — see validateWebFlags.
//
// The dump emulates the composed-tree output carrying the restrict
// overlay rows (the file F is the restrict overlay itself).
//
// The RPC surface is the dsh 0.2.x wire: `<namespace>/<method>`
// endpoints (/api/workspace/create, /api/session/create, plus the eight
// write verbs of proxy.go's ownSessionWriteMethods — see handleRPC for
// the upstream source of truth) with the verb's object argument nested at
// payload.args.request. --no-open is accepted and ignored (issue #84:
// real 0.2.x would open the default browser).
//
// Usage:
//
//	go build -o /path/to/mock-dsh internal/instance/dsh_web/testdata/dsh-mock.go
//	export PATH=$PATH:/path/to/
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
)

func main() {
	if len(os.Args) < 2 {
		fmt.Println("mock dsh: use --version or web subcommand")
		os.Exit(1)
	}
	if os.Args[1] == "--version" || os.Args[1] == "-V" {
		fmt.Println("0.2.0-rc.2")
		return
	}
	if os.Args[1] != "web" {
		fmt.Println("mock dsh: unknown subcommand " + os.Args[1])
		os.Exit(1)
	}
	rest := os.Args[2:]
	// Scan the WHOLE argv for --dump-config first: with the launcher
	// flags ordered first (`web --patch X --dump-config`), a linear
	// scan would hit --patch before --dump-config and wrongly enter
	// serve mode.
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
	fmt.Println("mock dsh: web without --patch/--dump-config")
	os.Exit(1)
}

// knownWebFlags / valueTakingWebFlags are the mock's FLAG CONTRACT with
// the real dsh: the web-app options of 0.2.0-rc.2 (`dsh web --help`:
// --host / --port / --no-open / --patch / --trusted-host) plus the
// launcher-level --dump-config. Anything else is rejected exactly like
// commander does — `unknown option '--x'` on stderr, exit 1 — because
// that is what makes a real boot fail. Without this the mock accepted any
// argv, so a flag the real dsh never learned (--no-open dropped by a
// rename, --patch moved behind an app-level option and therefore
// forwarded verbatim to the web app — see launch.go's ORDER MATTERS note)
// would keep every integration test green while every real Start died.
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
			fmt.Fprintf(os.Stderr, "mock dsh: unknown option '%s'\n", tok)
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

// dump prints a composed-tree-shaped output carrying the overlay rows
// (the file passed after --patch). The row ids and values come from the
// file itself, so verifyOverlayDump finds them.
func dump(args []string) {
	var patch string
	for i, a := range args {
		if a == "--patch" && i+1 < len(args) {
			patch = args[i+1]
		}
	}
	b, err := os.ReadFile(patch)
	if err != nil {
		fmt.Fprintln(os.Stderr, "mock dsh: read patch:", err)
		os.Exit(1)
	}
	fmt.Println("# == dump (mock), patched by " + patch)
	fmt.Print(strings.TrimRight(string(b), "\n"))
	fmt.Println()
}

// serve prints the ready line and serves GET / with 200 and POST /api
// with RPC envelope responses (workspace/create adopts any path and
// returns a stable workspaceId; session/create succeeds), until a
// termination signal arrives, then closes the listener so the port is
// released.
func serve() {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		panic(err)
	}
	addr := listener.Addr().String()
	host, port, _ := net.SplitHostPort(addr)

	// This is the line dsh_web.pumpAndWatch scans for.
	fmt.Printf("dsh web: http://%s:%s\n", host, port)

	mux := http.NewServeMux()
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPost && strings.HasPrefix(r.URL.Path, "/api") {
			handleRPC(w, r)
			return
		}
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`<!doctype html><title>mock dsh</title>`))
	})

	sig := make(chan os.Signal, 1)
	signal.Notify(sig, syscall.SIGTERM, syscall.SIGINT)

	go func() {
		_ = http.Serve(listener, mux)
	}()

	<-sig
	listener.Close()
}

// handleRPC answers the client-request envelope with a server-response,
// faithfully mirroring the real dsh 0.2.x wire contract: the route table
// registers `/api/<namespace>/<method>` only — the 0.1.x dotted
// `/api/workspace.create` has NO route and 404s at the HTTP layer (the
// same layer that 404s bare `/api`, PLAN.md §踩坑 13); the envelope's
// method must equal the endpoint (otherwise "method does not match
// endpoint"); the verb's single object argument arrives nested at
// payload.args.request — anything else the real gateway rejects with
// "Remote payload must contain exactly one plain-object args field",
// which this mock mirrors. workspace/create adopts the given path
// (workspaceId = "mock-ws-<hash>"), session/create succeeds with a dummy
// value, and the eight write verbs proxy.go's ownSessionWriteMethods
// observes answer ok:true with a small value.
func handleRPC(w http.ResponseWriter, r *http.Request) {
	endpoint := strings.TrimPrefix(r.URL.Path, "/api/")
	// Routing check FIRST, before the envelope is even decoded: on 0.2 a
	// dotted endpoint never reaches the RPC layer at all, so a regression
	// to the 0.1 names must fail as a routing miss (404) and not as a
	// confusing payload complaint.
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
	// The 0.2.x payload nests the verb's arguments at payload.args, and
	// every verb this mock serves takes exactly one object argument,
	// named `request` (mirrors `@Remote('create') create(request: …)`).
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
	// Hand-maintained MIRROR of the upstream verb table. The source of
	// truth for these eight verb names is the `@Remote('<verb>')`
	// decorators in the dsh 0.2.x remote controllers' typert.host.js —
	// session: prompt / cancel / fork / rename / selectModel /
	// attachment / updateQueue; subagents (PLURAL): prompt. This case
	// list is a hand-written copy of that truth (the mock is a
	// standalone //go:build ignore binary and cannot import the
	// package), so it must be updated in the same change as proxy.go's
	// ownSessionWriteMethods. An upstream rename is NOT detected
	// automatically: this is a hand-written switch, so the map, both
	// mocks and the test loop would go stale together and no test turns
	// red. What IS guarded is internal consistency — if one of these
	// hand-maintained copies drifts (typo, partial rename), proxy,
	// mocks and test disagree and CI goes red: a verb missing from this
	// table answers method-not-found and the write-verb POSTs in
	// TestDshWebProxyEndToEnd fail. The one automatic pin is
	// TestProxySessionOwnMarking's bidirectional map↔table check
	// (proxy.go's map ↔ proxy_test.go's literal table).
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
