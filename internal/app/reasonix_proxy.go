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
	"myworktree/internal/instance/reasonix"
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
		// reject it anyway (it has its own ?token= scheme). Any OTHER query
		// params are preserved — the injected prefix() JS keeps the browser's
		// query string, and reasonix itself uses query params (e.g.
		// ?session=) that must reach it intact.
		q := req.URL.Query()
		q.Del("token")
		req.URL.RawQuery = q.Encode()
		// Inject the reasonix auth cookie (single source: reasonix.CookieName)
		// so every upstream request carries the instance token; myworktree's
		// own auth query is never forwarded upstream.
		req.Header.Set("Cookie", reasonix.CookieName+"="+info.Token)
		// Encoding strategy (review nit): the HTML injection rewrites the
		// body, so an HTML navigation response must reach ModifyResponse
		// uncompressed — force identity on the browser's HTML navigation
		// requests (Accept contains text/html). Every other request
		// (script/style/JSON/SSE) explicitly asks for gzip: without an
		// explicit Accept-Encoding, Go's Transport would negotiate gzip,
		// transparently decompress, and strip Content-Encoding, wasting the
		// negotiated compression (the browser would get a plain body).
		// Compressed non-HTML bodies are forwarded verbatim; the
		// ModifyResponse guard below refuses to splice into any HTML response
		// that is still compressed.
		if strings.Contains(req.Header.Get("Accept"), "text/html") {
			req.Header.Set("Accept-Encoding", "identity")
		} else {
			req.Header.Set("Accept-Encoding", "gzip")
		}
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
		// Safety net: if the upstream still served a compressed HTML response
		// (e.g. an XHR fetch of HTML whose Accept: *\/* negotiated gzip), do
		// not splice the injection into the compressed body — pass it through
		// untouched. HTML navigation requests are forced to identity above,
		// so the normal page load still gets the injection.
		if enc := resp.Header.Get("Content-Encoding"); enc != "" && enc != "identity" {
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
// URL the Reasonix page issues with the mount path (e.g. /rx/abc123), and
// appends the issue #48 layout injection (collapsible sidebar, collapsed by
// default) right after it. Both are inserted at a single point (right after
// <head>), so the proxy has exactly one HTML injection site.
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

	// Issue #48 layout injection: the Reasonix sidebar is a fixed 220px grid
	// column with no desktop collapse affordance (@media(min-width:769px)
	// hides #menu-btn; the JS handler only opens, never closes). We add our
	// own toggle button and drive collapse via an `mw-rx` class on <html>.
	//
	// Design (verified against reasonix v1.22.0 index.html):
	//   - Only the desktop breakpoint (min-width:769px) is overridden; on
	//     narrow screens the native mobile sidebar (fixed + overlay + #menu-btn)
	//     is left untouched.
	//   - Collapsed (default): .app becomes a 0-width first column and the
	//     sidebar is display:none; chat takes the full width.
	//   - Grid-placement fix: upstream relies on auto-placement for
	//     .transcript / .footer (the sidebar's explicit grid-row:1/3 claims
	//     column 1, pushing them to column 2). Once the sidebar is
	//     display:none, auto-placement reflows: .transcript would land in the
	//     0px column and .footer would stretch across row 1 — the chat area
	//     vanishes while the input bar stays. We pin both explicitly when
	//     collapsed so they keep upstream's column-2 row-1/row-2 slots.
	//   - Expanded: .app restores the native grid; the sidebar width is
	//     configurable via --mw-sidebar-w (default 220px, same as upstream).
	//   - The toggle is our own #mw-sidebar-toggle button (fixed, top-left,
	//     moved next to the sidebar edge when expanded) — we deliberately do
	//     NOT reuse upstream #menu-btn: it is hidden on desktop, its onclick
	//     runs after our <head> script and would clobber ours.
	//   - Button sizing (measured against v1.22.0): the desktop
	//     .transcript has padding:24px 28px, so the collapsed chat's left
	//     gutter is 28px wide (the narrow-screen padding:16px only applies
	//     under max-width:768px, where our toggle is hidden anyway). The
	//     button is a vertical 24x64px pill at (2,8): its right edge at
	//     2+24=26px stays inside the 28px gutter, so the height may extend
	//     freely without ever overlapping message text — in both the
	//     collapsed and expanded states (expanded, the chat gutter starts at
	//     the sidebar's right edge and the button sits right next to it at
	//     left:calc(var(--mw-sidebar-w) + 4px)). The taller target is easier
	//     to see and click; a CSS ::before arrow switches ▶/◀ with the
	//     .mw-rx state (points at the direction the sidebar will move: ▶ to
	//     expand, ◀ to collapse — no text, no i18n).
	//     Re-verify the gutter width if upstream changes
	//     .transcript padding.
	//   - Timing note: the CSS is static and the class flip is synchronous
	//     (add('mw-rx') runs as the <head> script parses), so there is no
	//     race; the html:not(.mw-rx) selector simply tracks the current
	//     state. The layer order guarantee (our <style> after upstream's)
	//     holds only while upstream keeps its styles in <head> — a future
	//     upstream change that injects a <style> after </head> would need
	//     re-verification.
	//   - Narrow screens (<769px) keep the native mobile sidebar; our toggle
	//     is hidden there (its styles are desktop-scoped, so without this the
	//     unstyled button would render at the top of the page flow).
	//   - Collapsed by default keeps the management scope on the current
	//     worktree: the sidebar (brand / nav / session list) is what the user
	//     asked to hide (issue #48).
	//   - Verified against v1.22.0: the sidebar has NO cross-project entry —
	//     sessions are scoped by the serve working directory (per-instance
	//     worktree), so "hiding the project switcher" reduces to collapsing
	//     the whole sidebar; nothing further to hide when expanded.
	layout := `<style>
@media(min-width:769px){
  :root{--mw-sidebar-w:220px}
  .mw-rx .app{grid-template-columns:0 1fr}
  .mw-rx .sidebar{display:none}
  .mw-rx .transcript{grid-column:2;grid-row:1}
  .mw-rx .footer{grid-column:2;grid-row:2}
  .app{grid-template-columns:var(--mw-sidebar-w,220px) 1fr}
  #mw-sidebar-toggle{position:fixed;top:8px;left:2px;z-index:97;width:24px;height:64px;box-sizing:border-box;display:flex;align-items:center;justify-content:center;border-radius:12px;background:var(--panel,#222);border:1px solid var(--border,#333);color:var(--fg-2,#aaa);cursor:pointer;font-size:15px;line-height:1;transition:background .15s,left .25s ease}
  #mw-sidebar-toggle::before{content:'▶';font-size:15px;line-height:1}
  html:not(.mw-rx) #mw-sidebar-toggle::before{content:'◀'}
  #mw-sidebar-toggle:hover{background:var(--card-hover,#2a2a2a);color:var(--fg,#eee)}
  html:not(.mw-rx) #mw-sidebar-toggle{left:calc(var(--mw-sidebar-w,220px) + 4px);color:var(--fg,#eee)}
  #menu-btn{display:none!important}
}
@media(max-width:768px){
  #mw-sidebar-toggle{display:none!important}
}
</style>
<script>
(function(){
  var root=document.documentElement;
  root.classList.add('mw-rx'); /* collapsed by default */
  function ensureBtn(){
    if(document.getElementById('mw-sidebar-toggle'))return;
    var b=document.createElement('button');
    b.id='mw-sidebar-toggle';b.type='button';
    b.setAttribute('aria-label','Toggle sidebar');
    b.title='Toggle sidebar';
    b.addEventListener('click',function(){root.classList.toggle('mw-rx');});
    document.body.appendChild(b);
  }
  if(document.readyState==='loading'){document.addEventListener('DOMContentLoaded',ensureBtn);}
  else{ensureBtn();}
})();
</script>`

	idx := bytes.Index(html, []byte("</head"))
	if idx >= 0 {
		// Insert right before </head>: the injected <style> then sits AFTER
		// the upstream <style>, so same-specificity rules (e.g. .app grid
		// columns) resolve in our favour without !important.
		out := make([]byte, 0, len(html)+len(script)+len(layout))
		out = append(out, html[:idx]...)
		out = append(out, script...)
		out = append(out, layout...)
		return append(out, html[idx:]...)
	}
	idx = bytes.Index(html, []byte("<head"))
	if idx < 0 {
		idx = bytes.Index(html, []byte("<html"))
		if idx < 0 {
			return append([]byte(script+layout), html...)
		}
		// Insert right after <html ...>.
		end := bytes.IndexByte(html[idx:], '>')
		if end < 0 {
			return append([]byte(script+layout), html...)
		}
		out := make([]byte, 0, len(html)+len(script)+len(layout))
		out = append(out, html[:idx+end+1]...)
		out = append(out, script...)
		out = append(out, layout...)
		return append(out, html[idx+end+1:]...)
	}
	// Insert right after <head> or <head ...>.
	end := bytes.IndexByte(html[idx:], '>')
	if end < 0 {
		return append([]byte(script+layout), html...)
	}
	out := make([]byte, 0, len(html)+len(script)+len(layout))
	out = append(out, html[:idx+end+1]...)
	out = append(out, script...)
	out = append(out, layout...)
	return append(out, html[idx+end+1:]...)
}
