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
	"regexp"
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
func ProxyHandler(m *framework.Manager, authToken string, tracker *ScopeTracker) http.Handler {
	logger := proxyLogger(m)
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

		// Scope monitoring (WORKTREE-ISOLATION.md §4.2): classify the
		// directory this request targets BEFORE forwarding. Only
		// directory-bearing requests (or scope=project) are recorded, so
		// static assets and directory-less global endpoints do not clobber
		// a previous out-of-scope observation.
		dir, hasDir := parseDirectory(r)
		crossProject := r.URL.Query().Get("scope") == "project"
		scope := ScopeInScope
		if hasDir || crossProject {
			scope = classify(worktree, dir, crossProject)
			tracker.Record(id, ScopeState{
				Scope:        scope,
				Directory:    dir,
				CrossProject: crossProject,
			})
		}

		// Per-request access log. SSE streams through this handler at
		// high frequency, so write via the framework's *log.Logger
		// (default: io.Discard) rather than os.Stderr — that previous
		// behaviour spammed stderr in production and racy with other
		// concurrent writers. Nil logger → skip silently.
		if logger != nil {
			logger.Printf("[opencode-proxy] %s %s → http://%s:%s%s (directory=%q scope=%s)",
				r.Method, r.URL.Path, host, port, rest, dir, scope)
		}

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
			// Accept-Encoding negotiation: the HTML injection rewrites the
			// body, so a navigation request (Accept contains text/html) must
			// reach ModifyResponse uncompressed — force identity. SSE streams
			// (text/event-stream) are also forced to identity: gzip buys
			// nothing for an already-compressed event stream and can add
			// buffering latency on a live channel. Everything else
			// (JS/CSS/JSON) asks for gzip; without an explicit
			// Accept-Encoding Go's Transport negotiates gzip, transparently
			// decompresses, and strips Content-Encoding, wasting the
			// negotiated compression.
			accept := req.Header.Get("Accept")
			if strings.Contains(accept, "text/html") || strings.Contains(accept, "text/event-stream") {
				req.Header.Set("Accept-Encoding", "identity")
			} else {
				req.Header.Set("Accept-Encoding", "gzip")
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
				missing := fixProxyHTML(resp, proxyPrefix, baseTag, worktree)
				tracker.SetCSPAnchorMissing(id, missing)
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
func fixProxyHTML(resp *http.Response, proxyPrefix, baseTag, worktree string) (cspAnchorMissing bool) {
	// Safety net: if upstream still served a compressed HTML response (e.g.
	// an XHR fetch of HTML whose Accept: */* negotiated gzip), do not splice
	// the injection into the compressed body — pass it through untouched.
	// Navigation requests are forced to identity in the Director, so the
	// normal page load still gets the injection.
	if enc := resp.Header.Get("Content-Encoding"); enc != "" && enc != "identity" {
		return
	}
	body, err := io.ReadAll(resp.Body)
	resp.Body.Close()
	if err != nil {
		return
	}

	// Rewrite root-relative asset attributes (src/href/action/poster) so
	// the browser never issues a wrong first request for them. regex-based
	// (idempotent, attribute whitelist — see rewriteRootAttrs), which covers
	// more attributes than a naive src="/…" href="/…" string replace and
	// does not touch data-src / xlink:href.
	body = rewriteRootAttrs(body, proxyPrefix)

	// Inject a single inline script (right after <head>) that adapts the SPA
	// to the proxy and enforces the single-worktree view (WORKTREE-ISOLATION.md
	// §4.6): strip the proxy prefix from the URL, set defaultServerUrl, collapse
	// the localStorage server list to a single server, hide cross-worktree switch
	// entries, and intercept fetch/EventSource/XHR as defense-in-depth.
	worktreeB64 := base64.RawURLEncoding.EncodeToString([]byte(worktree))
	inject := buildInjectScript(proxyPrefix, worktreeB64, worktree)
	injectScript := "<script>" + inject + "</script>"
	body = bytes.Replace(body, []byte("<head>"), []byte("<head>"+baseTag+injectScript), 1)

	// Patch CSP to allow the injected inline script. Per CSP spec the hash
	// covers the script content (between tags), not the <script> wrapper.
	if csp := resp.Header.Get("Content-Security-Policy"); csp != "" {
		const anchor = "'wasm-unsafe-eval'"
		if !strings.Contains(csp, anchor) {
			// CSP structure drifted: the hash cannot be appended safely, so
			// the injected script will likely be blocked. Flag it for the
			// frontend instead of silently losing the single-worktree hiding.
			cspAnchorMissing = true
		} else {
			h := sha256.Sum256([]byte(inject))
			hashB64 := base64.StdEncoding.EncodeToString(h[:])
			csp = strings.Replace(csp, anchor, anchor+" 'sha256-"+hashB64+"'", 1)
		}
		resp.Header.Set("Content-Security-Policy", csp)
	}

	resp.Body = io.NopCloser(bytes.NewReader(body))
	resp.ContentLength = int64(len(body))
	resp.Header.Set("Content-Length", fmt.Sprintf("%d", len(body)))
	return cspAnchorMissing
}

// buildInjectScript builds the inline script injected into every HTML
// navigation response. It is a single IIFE so the CSP needs only one hash.
// proxyPrefix is the mount path (e.g. /__opencode/<id>); worktreeB64 is the
// base64url-encoded worktree path, used to match opencode's data-project
// attribute (opencode's base64Encode == Go base64.RawURLEncoding).
func buildInjectScript(proxyPrefix, worktreeB64, worktree string) string {
	return fmt.Sprintf(`(function(){
var p=%q,wt=%q,wtp=%q;
var a=location.pathname.slice(p.length);
if(a)history.replaceState(null,'',a);
var pu=location.origin+p;
try{localStorage.setItem('opencode.settings.dat:defaultServerUrl',pu)}catch(_){}
try{
  var sk='opencode.global.dat:server';
  var sr=localStorage.getItem(sk);
  var sd=sr?JSON.parse(sr):{};
  var ch=false;
  if(!Array.isArray(sd.list)||sd.list.length===0||sd.list[0]!==pu){sd.list=[pu];ch=true;}
  if(!sd.projects||typeof sd.projects!=='object'){sd.projects={};ch=true;}
  if(!Array.isArray(sd.projects[pu])||sd.projects[pu].length===0){sd.projects[pu]=[{worktree:wtp,expanded:true}];ch=true;}
  if(ch)localStorage.setItem(sk,JSON.stringify(sd));
}catch(_){}
var o=location.origin,pl=o.length;
function r(u){if(typeof u!=='string')return u;if(u.indexOf(p)!==-1)return u;if(u.startsWith(o+'/'))return o+p+u.slice(pl);if(u.charAt(0)==='/'&&u.charAt(1)!=='/')return o+p+u;var lo='http://127.0.0.1';if(u.startsWith(lo)){var c=u.indexOf(':',lo.length);if(c===-1)return u;var s=u.indexOf('/',c);if(s===-1)s=u.length;return o+p+u.slice(s)}return u}
function ri(i){if(typeof i==='string')return r(i);if(i&&i.url){var n=r(i.url);if(n!==i.url){var q=new Request(n,i);if(i.timeout!==undefined)q.timeout=i.timeout;return q}}if(i&&i.href&&typeof i.href==='string'){var h=r(i.href);if(h!==i.href)return new URL(h)}return i}
var of=fetch;window.fetch=function(i,ni){return of.call(this,ri(i),ni)};
var OE=EventSource;window.EventSource=function(u,opts){return new OE(r(u),opts)};window.EventSource.prototype=OE.prototype;
var hp=Object.prototype.hasOwnProperty;for(var k in OE){if(hp.call(OE,k))try{window.EventSource[k]=OE[k]}catch(_){}}
var xo=XMLHttpRequest.prototype.open;XMLHttpRequest.prototype.open=function(m,u){return xo.call(this,m,r(u))};
var OW=Worker;window.Worker=function(u,opts){return new OW(r(u),opts)};window.Worker.prototype=OW.prototype;
function hide(el){if(!el)return;try{el.style.setProperty('display','none','important');el.setAttribute('aria-hidden','true');}catch(_){}}
function foreign(el){var d=el.getAttribute?el.getAttribute('data-project'):null;return !!d&&d!==wt;}
function filter(root){
  if(!wt||!root||!root.querySelectorAll)return;
  var i,els;
  els=root.querySelectorAll('[data-project]');for(i=0;i<els.length;i++){if(foreign(els[i]))hide(els[i]);}
  els=root.querySelectorAll('button[aria-label="Open project"]');for(i=0;i<els.length;i++)hide(els[i]);
}
var st=document.createElement('style');st.textContent='aside:has([data-slot="home-projects-scroll"]){display:none!important}[data-action="project-switch"]:not([data-project="'+wt+'"]){display:none!important}';(document.head||document.documentElement).appendChild(st);
function run(){filter(document);}
if(document.readyState==='loading'){document.addEventListener('DOMContentLoaded',run);}else{run();}
var mo=new MutationObserver(function(muts){
  for(var i=0;i<muts.length;i++){
    var ns=muts[i].addedNodes;
    for(var j=0;j<ns.length;j++){
      var n=ns[j];
      if(n&&n.nodeType===1){
        if(n.getAttribute&&n.getAttribute('data-project')&&foreign(n))hide(n);
        if(n.querySelectorAll)filter(n);
      }
    }
  }
});
function obs(){if(document.documentElement)mo.observe(document.documentElement,{childList:true,subtree:true});}
if(document.readyState==='loading'){document.addEventListener('DOMContentLoaded',obs);}else{obs();}
function report(status,detail){try{parent.postMessage({type:'mw-oc/hidden-report',status:status,detail:detail},'*');}catch(_){}}
function anchorsPresent(){return !!(document.querySelector('[data-component="sidebar-rail"]')||document.querySelector('[data-action="project-switch"]')||document.querySelector('[data-component="home-session-search"]')||document.querySelector('[data-action="home-add-project"]'));}
var checks=0,maxChecks=20;
var l2=setInterval(function(){
  checks++;
  if(anchorsPresent()){clearInterval(l2);runL3();return;}
  if(checks>=maxChecks){clearInterval(l2);report('structure-changed','no sidebar anchor after 10s');}
},500);
function runL3(){
  var foreignVisible=0,currentHidden=false,els=document.querySelectorAll('[data-project]');
  for(var i=0;i<els.length;i++){
    var el=els[i],d=el.getAttribute('data-project');
    if(!d)continue;
    var vis=el.style.getPropertyValue('display')!=='none';
    if(d!==wt&&vis)foreignVisible++;
    if(d===wt&&!vis)currentHidden=true;
  }
  if(foreignVisible>0||currentHidden){report('hide-failed',JSON.stringify({foreignVisible:foreignVisible,currentHidden:currentHidden}));}
  else{report('ok','');}
}
})();`, proxyPrefix, worktreeB64, worktree)
}

// rewriteTagRe/rewriteAttrRe match a root-relative URL inside an HTML tag
// attribute. Anchored on "<tag … " plus a whitespace/quote before the
// attribute name ([\s"']), so data-src / xlink:href are NOT matched, and
// property-style JS assignments (`el.src="/x"`) are NOT matched (no
// whitespace/quote before "src"). Idempotent: protocol-relative URLs and
// values already carrying the mount prefix are left untouched.
var (
	rewriteTagRe  = regexp.MustCompile(`(?i)<[a-zA-Z][^>]*>`)
	rewriteAttrRe = regexp.MustCompile(`(?i)([\s"'](?:src|href|action|poster)\s*=\s*["']?)(/[^"'>\s]*)`)
)

// rewriteRootAttrs prefixes every root-relative src/href/action/poster value
// with mount (e.g. /__opencode/abc123). Each whole tag is extracted first,
// then all matching attributes inside it are rewritten, so a tag with several
// rewriteable attributes (<video src=… poster=…>) is fully covered.
func rewriteRootAttrs(html []byte, mount string) []byte {
	if len(html) == 0 || mount == "" {
		return html
	}
	return rewriteTagRe.ReplaceAllFunc(html, func(tag []byte) []byte {
		return rewriteAttrRe.ReplaceAllFunc(tag, func(m []byte) []byte {
			sub := rewriteAttrRe.FindSubmatch(m)
			if len(sub) != 3 {
				return m
			}
			path := sub[2]
			if bytes.HasPrefix(path, []byte("//")) || bytes.HasPrefix(path, []byte(mount)) {
				return m // protocol-relative or already prefixed
			}
			out := make([]byte, 0, len(sub[1])+len(mount)+len(path))
			out = append(out, sub[1]...)
			out = append(out, mount...)
			out = append(out, path...)
			return out
		})
	})
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

// proxyLogger returns the framework Manager's logger so the proxy
// can write per-request access lines. NewManager defaults to
// io.Discard, so production runs stay quiet unless the operator wires
// a real logger into the Manager.
func proxyLogger(m *framework.Manager) *log.Logger {
	if m == nil {
		return nil
	}
	return m.Logger
}
