//go:build ignore
// +build ignore

package main

import (
	"fmt"
	"net"
	"net/http"
	"os"
	"time"
)

func main() {
	port := "0"
	for i, a := range os.Args {
		if a == "--port" && i+1 < len(os.Args) {
			port = os.Args[i+1]
			break
		}
	}

	ln, err := net.Listen("tcp", "127.0.0.1:"+port)
	if err != nil {
		fmt.Fprintf(os.Stderr, "listen error: %v\n", err)
		os.Exit(1)
	}

	_, p, _ := net.SplitHostPort(ln.Addr().String())
	fmt.Printf("opencode server listening on http://127.0.0.1:%s\n", p)

	mux := http.NewServeMux()
	mux.HandleFunc("/global/health", func(w http.ResponseWriter, r *http.Request) {
		_, pass, _ := r.BasicAuth()
		if pass == "" {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		w.WriteHeader(http.StatusOK)
		fmt.Fprintln(w, `{"healthy":true}`)
	})

	srv := &http.Server{Handler: mux}
	go srv.Serve(ln)
	time.Sleep(1 * time.Hour)
}
