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
	if ViewportDebugEnabled {
		content = bytes.ReplaceAll(content, []byte("<!-- viewport-debug -->"), []byte(`<script src="/static/viewport_debug.js"></script>`))
	} else {
		content = bytes.ReplaceAll(content, []byte("<!-- viewport-debug -->"), nil)
	}

	// Serve / as the embedded index.html.
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/" {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		// issue #116: the shell is a single inlined document whose behaviour IS the
		// release. Without a cache directive Safari applied heuristic freshness to
		// this response, so a plain reload could keep running the previous build —
		// which made on-device verification reports ambiguous ("nothing changed"
		// could mean the fix failed, or that the old bytes were still live). Served
		// documents must always reflect this process's embedded assets.
		w.Header().Set("Cache-Control", "no-store, must-revalidate")
		w.Header().Set("Pragma", "no-cache")
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

	// Static assets. The diagnostic asset is compiled in only with the
	// viewportdebug tag; stable builds neither embed nor serve it.
	mux.HandleFunc("/static/viewport_debug.js", func(w http.ResponseWriter, r *http.Request) {
		if !ViewportDebugEnabled {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "application/javascript; charset=utf-8")
		// Same reasoning as the document above: this asset is the measurement
		// instrument, so a stale copy silently invalidates every capture taken with
		// it. Embedding makes the bytes cheap to re-send.
		w.Header().Set("Cache-Control", "no-store, must-revalidate")
		w.Header().Set("Pragma", "no-cache")
		_, _ = w.Write(viewportDebugAsset)
	})
	fs := http.FS(staticFS)
	mux.Handle("/static/", http.FileServer(fs))
	return nil
}
