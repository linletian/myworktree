package app

import (
	"bytes"
	"fmt"
	"io"
	"net/http"
	"net/http/httputil"
	"net/url"
	"regexp"
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
		// Rewrite root-relative asset attributes (src/href/action/poster) on
		// the server so the browser never issues a wrong first request for
		// them (a client-side DOMContentLoaded pass would be too late — the
		// browser fetches <img src="/assets/..."> before any script runs).
		rewritten := rewriteRootAttrs(body, "/rx/"+id)
		injected := injectReasonixPrefix(rewritten, "/rx/"+id)
		resp.Body = io.NopCloser(bytes.NewReader(injected))
		resp.Header.Set("Content-Length", strconv.Itoa(len(injected)))
		resp.Header.Del("Content-Encoding")
		return nil
	}
	proxy.ServeHTTP(w, r)
}

// tagRe/attrRe match a root-relative URL inside an HTML tag attribute.
//
// Matching notes (verified behaviour, not guarantees):
//   - Anchored on "<tag ... " plus a whitespace/quote before the attribute
//     name ([\s"']), so data-src / xlink:href are NOT matched (a \b anchor
//     would have hit them after "-"/":"), and property-style JS assignments
//     (`el.src="/x"`) are NOT matched because "." is not whitespace/quote.
//   - Case-insensitive (?i), and unquoted attribute values are supported
//     (`<img src=/x.png>`, valid HTML5).
//   - The "<" anchor is NOT a full HTML parser: an attribute value containing
//     ">" (e.g. title="a > b") truncates the match and the attribute is left
//     unrewritten; the MutationObserver only covers nodes inserted after
//     load, so there is no second pass for the initial page. Accepted
//     tradeoff for regex-based rewriting.
var (
	tagRe  = regexp.MustCompile(`(?i)<[a-zA-Z][^>]*>`)
	attrRe = regexp.MustCompile(`(?i)([\s"'](?:src|href|action|poster)\s*=\s*["']?)(/[^"'>\s]*)`)
)

// rewriteRootAttrs prefixes every root-relative src/href/action/poster value
// with mount (e.g. /rx/abc123). It first extracts each whole tag, then
// rewrites ALL matching attributes inside it, so a tag with several
// rewriteable attributes (<video src=... poster=...>) is fully covered.
// Protocol-relative URLs (//host/...) and values already carrying the mount
// prefix are left untouched, so the pass is idempotent. Not covered
// (documented limitation): CSS url(), srcset, and SVG <use> — reasonix's
// page uses none.
func rewriteRootAttrs(html []byte, mount string) []byte {
	if len(html) == 0 || mount == "" {
		return html
	}
	return tagRe.ReplaceAllFunc(html, func(tag []byte) []byte {
		return attrRe.ReplaceAllFunc(tag, func(m []byte) []byte {
			sub := attrRe.FindSubmatch(m)
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
// URL the Reasonix page issues with the mount path (e.g. /rx/abc123).
//
// Coverage split:
//   - Initial-page static attributes (img/a/link/script/source/form/video)
//     are rewritten server-side by rewriteRootAttrs before this script is
//     injected, so the browser never issues a wrong first request.
//   - This script covers the JS network layer (fetch, EventSource,
//     XMLHttpRequest) and a MutationObserver fallback for nodes inserted
//     dynamically after load (e.g. images inside chat messages). The observer
//     rewrites the same attribute set (src/href/action/poster) as the server
//     pass.
//   - There is deliberately NO overlap: the regex pass runs once on the
//     initial page; attributes it misses (e.g. a value containing ">") have
//     no second chance, because the observer only fires for inserted nodes.
//   - Not covered: CSS url(), srcset, SVG <use>, and scripts loaded before
//     rewriting (reasonix ships none of these).
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
  function prefixEl(el){
    var attrs=['src','href','action','poster'];
    for(var a=0;a<attrs.length;a++){
      var k=attrs[a];
      var v=el.getAttribute&&el.getAttribute(k);
      if(v&&v.charAt(0)==='/'&&v.indexOf(P)!==0)el.setAttribute(k,P+v);
    }
  }
  var mo=new MutationObserver(function(muts){
    for(var i=0;i<muts.length;i++){
      var ns=muts[i].addedNodes;
      for(var j=0;j<ns.length;j++){
        var n=ns[j];
        if(n.nodeType===1){
          prefixEl(n);
          var els=n.querySelectorAll?n.querySelectorAll('img[src^="/"],a[href^="/"],link[href^="/"],source[src^="/"],video[poster^="/"],form[action^="/"]'):[];
          for(var k=0;k<els.length;k++)prefixEl(els[k]);
        }
      }
    }
  });
  if(document.documentElement)mo.observe(document.documentElement,{childList:true,subtree:true});
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
