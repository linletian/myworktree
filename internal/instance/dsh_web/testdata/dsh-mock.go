//go:build ignore

// dsh-mock is a tiny drop-in binary that emulates `dsh web` just
// enough for integration tests. It handles:
//
//	--version                    → "0.1.0-rc.6"
//	web --dump-config --patch F  → "# == dump" + contents of F
//	web --host … --port … --patch F → prints the "dsh web: http://…"
//	                                 ready line, serves GET / with 200,
//	                                 and exits on SIGTERM/SIGINT
//
// The dump emulates the composed-tree output carrying the restrict
// overlay rows (the file F is the restrict overlay itself).
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
		fmt.Println("0.1.0-rc.6")
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
	for i, a := range rest {
		if a == "--patch" && i+1 < len(rest) {
			serve()
			return
		}
	}
	fmt.Println("mock dsh: web without --patch/--dump-config")
	os.Exit(1)
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
// with RPC envelope responses (workspace.create adopts any path and
// returns a stable workspaceId; session.create succeeds), until a
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
// faithfully mirroring the real wire contract: the endpoint comes from
// the URL path (/api/<method>) and the envelope's method must equal it
// (otherwise "method does not match endpoint"). workspace.create adopts
// the given path (workspaceId = "mock-ws-<hash>"), session.create
// succeeds with a dummy value.
func handleRPC(w http.ResponseWriter, r *http.Request) {
	endpoint := strings.TrimPrefix(r.URL.Path, "/api/")
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
		value = map[string]any{"session": map[string]any{"id": "mock-session"}}
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
