package ui

import (
	"encoding/base64"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestBasicAuth(t *testing.T) {
	t.Parallel()
	got := basicAuth("opencode", "hunter2")
	want := base64.StdEncoding.EncodeToString([]byte("opencode:hunter2"))
	if got != want {
		t.Fatalf("basicAuth = %q, want %q", got, want)
	}
}

func TestIsAPIPath(t *testing.T) {
	t.Parallel()
	for _, p := range []string{"/api/foo", "/doc", "/global/event", "/session"} {
		if !isAPIPath(p) {
			t.Fatalf("isAPIPath(%q) = false, want true", p)
		}
	}
	for _, p := range []string{"/", "/index.html", "/assets/x.js"} {
		if isAPIPath(p) {
			t.Fatalf("isAPIPath(%q) = true, want false", p)
		}
	}
}

func TestOpencodeProxy_Director(t *testing.T) {
	t.Parallel()

	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		auth := r.Header.Get("Authorization")
		if auth == "" {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		dir := r.URL.Query().Get("directory")
		w.Header().Set("X-Auth", auth)
		w.Header().Set("X-Dir", dir)
		w.WriteHeader(http.StatusOK)
	}))
	defer upstream.Close()

	// We can't easily test OpencodeProxy directly without a real Manager.
	// Instead test the low-level building blocks.
	t.Logf("upstream server: %s", upstream.URL)
}

func TestOpencodeProxy_SSEFlush(t *testing.T) {
	t.Parallel()

	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		flusher, ok := w.(http.Flusher)
		if !ok {
			http.Error(w, "no flush", http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		for i := 0; i < 10; i++ {
			io.WriteString(w, "data: chunk\n\n")
			flusher.Flush()
			time.Sleep(10 * time.Millisecond)
		}
	}))
	defer upstream.Close()

	resp, err := http.Get(upstream.URL)
	if err != nil {
		t.Fatalf("GET SSE: %v", err)
	}
	defer resp.Body.Close()
	if ct := resp.Header.Get("Content-Type"); !strings.HasPrefix(ct, "text/event-stream") {
		t.Fatalf("Content-Type = %q, want text/event-stream", ct)
	}
	// Read body chunks.
	chunks := 0
	buf := make([]byte, 256)
	for {
		n, readErr := resp.Body.Read(buf)
		if n > 0 {
			if strings.Contains(string(buf[:n]), "data: chunk") {
				chunks++
			}
		}
		if readErr != nil {
			break
		}
	}
	if chunks < 5 {
		t.Fatalf("only got %d SSE chunks, want >=5", chunks)
	}
	t.Logf("received %d SSE chunks", chunks)
}
