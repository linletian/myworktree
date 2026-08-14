//go:build ignore

// opencode-mock is a tiny drop-in binary that emulates `opencode
// serve --hostname 127.0.0.1 --port 0` just enough for
// integration tests. It prints the "opencode server listening on …"
// line that opencode_web.pumpAndWatch scans for, and responds to
// GET /global/health with 200 OK (the health probe expects that).
//
// Usage:
//
//	go build -o /path/to/mock-opencode internal/instance/opencode_web/testdata/opencode-mock.go
//	export PATH=$PATH:/path/to/
//
// The test picks it up by overriding the binary in PATH.
package main

import (
	"fmt"
	"net"
	"net/http"
	"os"
	"os/signal"
	"syscall"
)

func main() {
	if len(os.Args) > 1 && os.Args[1] == "serve" {
		serve()
		return
	}
	fmt.Println("mock opencode: use 'serve' subcommand")
	os.Exit(1)
}

func serve() {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		panic(err)
	}
	addr := listener.Addr().String()
	host, port, _ := net.SplitHostPort(addr)

	// This is the line opencode_web.pumpAndWatch scans for.
	fmt.Printf("opencode server listening on http://%s:%s\n", host, port)

	mux := http.NewServeMux()
	mux.HandleFunc("/global/health", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"status":"ok"}`))
	})

	sig := make(chan os.Signal, 1)
	signal.Notify(sig, syscall.SIGTERM, syscall.SIGINT)

	go func() {
		_ = http.Serve(listener, mux)
	}()

	<-sig
	listener.Close()
}
