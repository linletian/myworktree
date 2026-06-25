package opencode_web

import (
	"bytes"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/http/httputil"
	"net/url"
	"os"
	"strings"

	"myworktree/internal/framework"
)

// ProxyHandler returns a single reverse-proxy handler for ALL
// opencode-web instances. Registered once at /__opencode/ in the
// main mux. The path format is /__opencode/<id>/<rest...>.
//
// Each request:
//   - extracts the instance id from the first path segment
//   - looks up the instance via the framework Manager
//   - reads the upstream host:port from the instance's KindBlob
//   - forwards <rest> to http://host:port/<rest> with Basic auth
//     (username "opencode", password = authToken)
//   - adds ?directory=<worktree> for GET/HEAD requests to API paths
func ProxyHandler(m *framework.Manager, authToken string) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Path after StripPrefix is "/<id>/<rest...>".
		path := strings.TrimPrefix(r.URL.Path, "/")
		if path == "" {
			http.Error(w, "missing instance id", http.StatusBadRequest)
			return
		}
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
			http.Error(w, `{"error":"instance not found"}`, http.StatusNotFound)
			return
		}
		if inst.Kind != "opencode-web" {
			http.Error(w, `{"error":"not an opencode-web instance"}`, http.StatusNotFound)
			return
		}

		host, port, worktree := readBlob(inst.KindBlob)

		if port == "" {
			writeJSON(w, http.StatusServiceUnavailable, map[string]string{
				"error": "opencode server not yet ready",
			})
			return
		}

		fmt.Fprintf(os.Stderr, "[opencode-proxy] %s %s → http://%s:%s%s\n",
			r.Method, r.URL.Path, host, port, rest)

		target := &url.URL{Scheme: "http", Host: host + ":" + port}
		proxy := httputil.NewSingleHostReverseProxy(target)
		proxy.FlushInterval = -1

		origDirector := proxy.Director
		proxy.Director = func(req *http.Request) {
			origDirector(req)
			req.URL.Scheme = target.Scheme
			req.URL.Host = target.Host
			req.URL.Path = rest
			req.URL.RawPath = ""
			req.URL.RawQuery = r.URL.RawQuery
			req.Header.Set("Authorization", "Basic "+basicAuth("opencode", authToken))
			if (r.Method == http.MethodGet || r.Method == http.MethodHead) &&
				isAPIPath(rest) && !req.URL.Query().Has("directory") && worktree != "" {
				q := req.URL.Query()
				q.Set("directory", worktree)
				req.URL.RawQuery = q.Encode()
			}
		}

		// Rewrite HTML so all assets route through the proxy.
		// opencode SPA emits absolute paths (/assets/*.js, /favicon*,
		// /site.webmanifest, etc.) which <base> does NOT affect.
		// We rewrite every src="/…" and href="/…" to include the
		// proxy prefix, then inject <base> for the remaining
		// root-relative paths (API calls, client-side routes).
		proxyPrefix := "/__opencode/" + id
		baseTag := "<base href=\"" + proxyPrefix + "/\">"
		proxy.ModifyResponse = func(resp *http.Response) error {
			ct := resp.Header.Get("Content-Type")
			if strings.Contains(ct, "text/html") {
				fixProxyHTML(resp, proxyPrefix, baseTag)
			}
			return nil
		}

		proxy.ErrorHandler = func(w http.ResponseWriter, req *http.Request, err error) {
			writeJSON(w, http.StatusBadGateway, map[string]string{
				"error": "opencode server unreachable",
			})
		}

		proxy.ServeHTTP(w, r)
	})
}

// fixProxyHTML rewrites absolute asset paths in the HTML so they route
// through the reverse proxy, then injects the <base> tag. The <base> tag
// alone only helps root-relative URLs — opencode emits absolute paths
// (src="/assets/…", href="/favicon…", etc.) that bypass <base>.
func fixProxyHTML(resp *http.Response, proxyPrefix, baseTag string) {
	body, err := io.ReadAll(resp.Body)
	resp.Body.Close()
	if err != nil {
		return
	}

	// Rewrite absolute src="/…" and href="/…" to route through the proxy.
	// We skip paths that already start with proxyPrefix to avoid double-
	// rewriting on repeated requests.
	prefix := proxyPrefix + "/"
	body = bytes.ReplaceAll(body,
		[]byte(`src="/`),
		[]byte(`src="`+prefix))
	body = bytes.ReplaceAll(body,
		[]byte(`href="/`),
		[]byte(`href="`+prefix))
	// Undo double-write: restore src="/__opencode/<id>/…" that was
	// already correctly prefixed.
	doubleSrc := []byte(`src="` + prefix + prefix)
	body = bytes.ReplaceAll(body, doubleSrc, []byte(`src="`+prefix))
	doubleHref := []byte(`href="` + prefix + prefix)
	body = bytes.ReplaceAll(body, doubleHref, []byte(`href="`+prefix))

	// Inject <base> tag after <head> and a shim that makes the SPA work
	// through the proxy. Three mechanisms:
	// 1. Rewrite browser URL to strip proxy prefix so SPA Router can
	//    match routes (/:dir/session/:id?). Without this the Router sees
	//    /__opencode/<id>/<base64>/session/ and finds no match → blank.
	// 2. Set defaultServerUrl so API calls go through proxy (primary)
	// 3. Intercept fetch/EventSource/XHR to rewrite URLs (defense-in-depth)
	shimContentFmt := "(function(){var p=%q,pu=location.origin+p;var a=location.pathname.slice(p.length);if(a)history.replaceState(null,'',a);try{localStorage.setItem('opencode.settings.dat:defaultServerUrl',pu)}catch(_){}var o=location.origin,pl=o.length;function r(u){if(typeof u!=='string')return u;if(u.indexOf(p)!==-1)return u;if(u.startsWith(o+'/'))return o+p+u.slice(pl);var lo='http://127.0.0.1';if(u.startsWith(lo)){var c=u.indexOf(':',lo.length);if(c===-1)return u;var s=u.indexOf('/',c);if(s===-1)s=u.length;return o+p+u.slice(s)}return u}function ri(i){if(typeof i==='string')return r(i);if(i&&i.url){var n=r(i.url);if(n!==i.url){var q=new Request(n,i);if(i.timeout!==undefined)q.timeout=i.timeout;return q}}return i}var of=fetch;window.fetch=function(i,ni){return of.call(this,ri(i),ni)};var OE=EventSource;window.EventSource=function(u,opts){return new OE(r(u),opts)};window.EventSource.prototype=OE.prototype;var hp=Object.prototype.hasOwnProperty;for(var k in OE){if(hp.call(OE,k))try{window.EventSource[k]=OE[k]}catch(_){}}var xo=XMLHttpRequest.prototype.open;XMLHttpRequest.prototype.open=function(m,u){return xo.call(this,m,r(u))}})();"
	shimContent := fmt.Sprintf(shimContentFmt, proxyPrefix)
	shimScript := "<script>" + shimContent + "</script>"
	body = bytes.Replace(body, []byte("<head>"), []byte("<head>"+baseTag+shimScript), 1)

	// Patch CSP to allow the injected inline shim script.
	// opencode serves Content-Security-Policy with strict script-src
	// that blocks inline scripts without a matching hash.
	// Per CSP spec, hash covers the script content (between tags), not
	// the <script> wrapper itself.
	if csp := resp.Header.Get("Content-Security-Policy"); csp != "" {
		h := sha256.Sum256([]byte(shimContent))
		hashB64 := base64.StdEncoding.EncodeToString(h[:])
		csp = strings.Replace(csp, "'wasm-unsafe-eval'", "'wasm-unsafe-eval' 'sha256-"+hashB64+"'", 1)
		resp.Header.Set("Content-Security-Policy", csp)
	}

	resp.Body = io.NopCloser(bytes.NewReader(body))
	resp.ContentLength = int64(len(body))
	resp.Header.Set("Content-Length", fmt.Sprintf("%d", len(body)))
}

func readBlob(raw json.RawMessage) (host, port, worktree string) {
	if len(raw) == 0 {
		return
	}
	var b struct {
		Host        string `json:"host"`
		Port        string `json:"port"`
		WorktreeAbs string `json:"worktree_abs"`
	}
	if json.Unmarshal(raw, &b) == nil {
		host = b.Host
		port = b.Port
		worktree = b.WorktreeAbs
	}
	return
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

var _ = log.Printf
var _ = io.Discard
