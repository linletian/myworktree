package ui

import (
	"embed"
	"io"
	"net/http"
)

//go:embed static/*
var staticFS embed.FS

func Register(mux *http.ServeMux) {
	// Serve / as the embedded index.html.
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/" {
			http.NotFound(w, r)
			return
		}
		f, err := staticFS.Open("static/index.html")
		if err != nil {
			http.Error(w, "missing index.html", http.StatusInternalServerError)
			return
		}
		defer f.Close()
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		_, _ = io.Copy(w, f)
	})

	// Static assets.
	fs := http.FS(staticFS)
	mux.Handle("/static/", http.StripPrefix("/static/", http.FileServer(fs)))
}
