package ui

import (
	"encoding/base64"
	"encoding/json"
	"errors"
	"log"
	"net/http"
	"net/http/httputil"
	"net/url"
	"strings"

	"myworktree/internal/instance"
)

// OpencodeProxy returns a reverse proxy handler that forwards requests to
// the opencode server backing the given instance. The instance ID is
// extracted from the URL path: /<id>/rest/...
func OpencodeProxy(m *instance.Manager) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Path: /<id>/<rest...>
		// r.URL.Path after StripPrefix is "/<id>/<rest...>"
		path := strings.TrimPrefix(r.URL.Path, "/")
		if path == "" || path == "/" {
			http.Error(w, "missing instance id", http.StatusBadRequest)
			return
		}

		// Extract instance id (first path segment).
		idx := strings.Index(path, "/")
		var id, rest string
		if idx < 0 {
			id = path
			rest = "/"
		} else {
			id = path[:idx]
			rest = path[idx:]
		}

		inst, err := m.Get(id)
		if err != nil {
			if errors.Is(err, instance.ErrInstanceNotFound) {
				http.Error(w, `{"error":"instance not found"}`, http.StatusNotFound)
				return
			}
			http.Error(w, `{"error":"`+err.Error()+`"}`, http.StatusInternalServerError)
			return
		}
		if inst.Kind != "opencode-web" {
			http.Error(w, `{"error":"not an opencode-web instance"}`, http.StatusNotFound)
			return
		}

		host := inst.Extra["host"]
		port := inst.Extra["port"]
		password := inst.Extra["password"]
		worktreeAbs := inst.Extra["worktree_abs"]

		if port == "" || password == "" {
			writeJSON(w, http.StatusServiceUnavailable, map[string]string{
				"error": "opencode server not yet ready",
			})
			return
		}

		target := &url.URL{Scheme: "http", Host: host + ":" + port}
		proxy := httputil.NewSingleHostReverseProxy(target)
		proxy.FlushInterval = -1

		origDirector := proxy.Director
		proxy.Director = func(req *http.Request) {
			origDirector(req)
			req.Host = target.Host
			req.URL.Path = rest
			req.URL.RawPath = ""
			req.URL.RawQuery = r.URL.RawQuery
			req.Header.Set("Authorization", "Basic "+basicAuth(opencodeUser, password))

			if (r.Method == http.MethodGet || r.Method == http.MethodHead) &&
				isAPIPath(rest) &&
				!req.URL.Query().Has("directory") &&
				worktreeAbs != "" {
				q := req.URL.Query()
				q.Set("directory", worktreeAbs)
				req.URL.RawQuery = q.Encode()
			}
		}

		proxy.ErrorHandler = func(w http.ResponseWriter, req *http.Request, err error) {
			log.Printf("opencode proxy error for instance %s: %v", id, err)
			writeJSON(w, http.StatusBadGateway, map[string]string{
				"error": "opencode server unreachable",
			})
		}

		proxy.ServeHTTP(w, r)
	})
}

const opencodeUser = "opencode"

func basicAuth(user, pass string) string {
	auth := user + ":" + pass
	return base64.StdEncoding.EncodeToString([]byte(auth))
}

func isAPIPath(p string) bool {
	return instance.IsAPIPath(strings.SplitN(p, "?", 2)[0])
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}
