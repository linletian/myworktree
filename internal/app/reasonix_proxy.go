package app

import (
	"bytes"
	"fmt"
	"io"
	"net/http"
	"net/http/httputil"
	"net/url"
	"strconv"
	"strings"

	"myworktree/internal/instance"
	"myworktree/internal/store"
)

// reasonixProxy serves the Reasonix web UI of a reasonix-kind instance under
// /rx/<id>/, same-origin with the myworktree UI so the instance tab can embed
// it in an iframe without CORS or cross-site cookies. Two things make the
// embedded page work:
//
//  1. Auth: every upstream request gets `Cookie: reasonix_token=<token>`.
//  2. URL prefix: the Reasonix page issues root-relative API calls
//     (fetch('/history'), new EventSource('/events'), ...). Since the page is
//     mounted under /rx/<id>/, those would hit myworktree's own routes. A
//     script injected into HTML responses rewrites them to carry the prefix.
type reasonixProxy struct {
	manager *instance.Manager
}

func (p *reasonixProxy) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	id, rest := splitReasonixPath(r.URL.Path)
	if id == "" {
		http.NotFound(w, r)
		return
	}

	inst, err := p.findInstance(id)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	if inst.Kind != store.KindReasonix {
		http.NotFound(w, r)
		return
	}
	if inst.Status != "running" || p.manager.Reasonix == nil {
		http.Error(w, "instance is not running", http.StatusServiceUnavailable)
		return
	}
	info, err := p.manager.Reasonix.Addr(id)
	if err != nil {
		http.Error(w, "reasonix backend unavailable: "+err.Error(), http.StatusServiceUnavailable)
		return
	}

	target, err := url.Parse("http://127.0.0.1:" + strconv.Itoa(info.Port))
	if err != nil {
		http.Error(w, "invalid backend address", http.StatusInternalServerError)
		return
	}

	proxy := httputil.NewSingleHostReverseProxy(target)
	proxy.FlushInterval = -1 // stream SSE events as they arrive
	proxy.Director = func(req *http.Request) {
		req.URL.Scheme = target.Scheme
		req.URL.Host = target.Host
		req.Host = target.Host
		// Strip the /rx/<id> mount prefix so reasonix sees its native paths.
		req.URL.Path = "/" + rest
		req.URL.RawPath = ""
		// Never forward myworktree's auth query (?token=...) upstream: it is
		// a credential leak to the reasonix subprocess and reasonix would
		// reject it anyway (it has its own ?token= scheme).
		req.URL.RawQuery = ""
		req.Header.Set("Cookie", "reasonix_token="+info.Token)
		// The HTML injection rewrites the body, so never let the upstream
		// compress it (ModifyResponse would otherwise splice plaintext into
		// a gzip stream).
		req.Header.Set("Accept-Encoding", "identity")
		req.Header.Del("X-Forwarded-For")
		req.Header.Del("X-Forwarded-Host")
		req.Header.Del("X-Forwarded-Proto")
	}
	proxy.ErrorHandler = func(w http.ResponseWriter, r *http.Request, err error) {
		http.Error(w, "reasonix backend unreachable", http.StatusBadGateway)
	}
	proxy.ModifyResponse = func(resp *http.Response) error {
		if !strings.HasPrefix(resp.Header.Get("Content-Type"), "text/html") {
			return nil
		}
		body, err := io.ReadAll(resp.Body)
		if err != nil {
			return err
		}
		_ = resp.Body.Close()
		injected := injectReasonixPrefix(body, "/rx/"+id)
		resp.Body = io.NopCloser(bytes.NewReader(injected))
		resp.Header.Set("Content-Length", strconv.Itoa(len(injected)))
		resp.Header.Del("Content-Encoding")
		return nil
	}
	proxy.ServeHTTP(w, r)
}

// splitReasonixPath splits "/rx/<id>/rest" into ("<id>", "rest").
func splitReasonixPath(path string) (string, string) {
	if !strings.HasPrefix(path, "/rx/") {
		return "", ""
	}
	rest := strings.TrimPrefix(path, "/rx/")
	if i := strings.IndexByte(rest, '/'); i >= 0 {
		return rest[:i], rest[i+1:]
	}
	return rest, ""
}

func (p *reasonixProxy) findInstance(id string) (store.ManagedInstance, error) {
	items, err := p.manager.List()
	if err != nil {
		return store.ManagedInstance{}, err
	}
	for _, it := range items {
		if it.ID == id {
			return it, nil
		}
	}
	return store.ManagedInstance{}, fmt.Errorf("unknown instance: %s", id)
}

// injectReasonixPrefix prepends a script that prefixes every root-relative
// URL the Reasonix page issues with the mount path (e.g. /rx/abc123). It only
// touches fetch, EventSource and XMLHttpRequest — the reasonix frontend uses
// no location/navigation-based root paths.
func injectReasonixPrefix(html []byte, mount string) []byte {
	script := fmt.Sprintf(`<script>
(function(){
  var P=%q;
  function prefix(u){return (typeof u==='string'&&u.charAt(0)==='/'&&u.indexOf(P)!==0)?P+u:u;}
  var _f=window.fetch;window.fetch=function(u,o){return _f.call(this,prefix(u),o);};
  var ES=window.EventSource;
  function PES(u,c){return new ES(prefix(u),c);}
  for(var k in ES){PES[k]=ES[k];}
  window.EventSource=PES;
  var _o=XMLHttpRequest.prototype.open;
  XMLHttpRequest.prototype.open=function(m,u){return _o.call(this,m,prefix(u),arguments[2],arguments[3],arguments[4]);};
})();
</script>`, mount)

	idx := bytes.Index(html, []byte("<head"))
	if idx < 0 {
		idx = bytes.Index(html, []byte("<html"))
		if idx < 0 {
			return append([]byte(script), html...)
		}
		// Insert right after <html ...>.
		end := bytes.IndexByte(html[idx:], '>')
		if end < 0 {
			return append([]byte(script), html...)
		}
		out := make([]byte, 0, len(html)+len(script))
		out = append(out, html[:idx+end+1]...)
		out = append(out, script...)
		return append(out, html[idx+end+1:]...)
	}
	// Insert right after <head> or <head ...>.
	end := bytes.IndexByte(html[idx:], '>')
	if end < 0 {
		return append([]byte(script), html...)
	}
	out := make([]byte, 0, len(html)+len(script))
	out = append(out, html[:idx+end+1]...)
	out = append(out, script...)
	return append(out, html[idx+end+1:]...)
}
