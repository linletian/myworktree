package ui

import (
	"bytes"
	"embed"
	"fmt"
	"html"
	"net/http"
)

//go:embed static/*
var staticFS embed.FS

//go:embed static/preview.html
var previewHTML []byte

// indexHTMLReader is the read function used by Register. It can be overridden in tests.
var indexHTMLReader = func() ([]byte, error) {
	return staticFS.ReadFile("static/index.html")
}

func Register(mux *http.ServeMux, repoName string, isRemote func(r *http.Request) bool) error {
	content, err := indexHTMLReader()
	if err != nil {
		return fmt.Errorf("failed to read embedded index.html: %w", err)
	}
	title := fmt.Sprintf("<title>%s - myworktree</title>", html.EscapeString(repoName))
	content = bytes.ReplaceAll(content, []byte("<title>myworktree</title>"), []byte(title))

	// Serve / as the embedded index.html.
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/" {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		body := content
		if isRemote != nil && isRemote(r) {
			body = bytes.ReplaceAll(body, []byte("<body>"), []byte(`<body class="remote-access">`))
		}
		_, _ = w.Write(body)
	})

	// Serve /preview as the embedded preview.html.
	mux.HandleFunc("/preview", func(w http.ResponseWriter, r *http.Request) {
		body := previewHTML
		title := fmt.Sprintf("<title>File Preview - %s</title>", html.EscapeString(repoName))
		body = bytes.ReplaceAll(body, []byte("<title>File Preview - myworktree</title>"), []byte(title))
		if isRemote != nil && isRemote(r) {
			body = bytes.ReplaceAll(body, []byte("<body>"), []byte(`<body class="remote-access">`))
		}
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		_, _ = w.Write(body)
	})

	// Static assets.
	fs := http.FS(staticFS)
	mux.Handle("/static/", http.FileServer(fs))
	return nil
}
