package dsh_web

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// newAuthUpstream fakes dsh's browser-auth endpoint and counts every
// request so tests can pin the caching / singleflight contract.
func newAuthUpstream(t *testing.T, handler func(w http.ResponseWriter, r *http.Request)) (*httptest.Server, *atomic.Int32) {
	t.Helper()
	var requests atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests.Add(1)
		handler(w, r)
	}))
	t.Cleanup(srv.Close)
	return srv, &requests
}

func authorityOf(srv *httptest.Server) string {
	return strings.TrimPrefix(srv.URL, "http://")
}

// mintHandler mirrors browser-auth.ts authorizeIndex: a correct
// ?token= on GET / answers 303 + the dsh-auth-* cookie; anything else
// answers the same minimal 401.
func mintHandler(token, setCookie string) func(http.ResponseWriter, *http.Request) {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet || r.URL.Path != "/" ||
			r.URL.Query().Get("token") != token {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		w.Header().Set("Location", "/")
		w.Header().Set("Set-Cookie", setCookie)
		w.WriteHeader(http.StatusSeeOther)
	}
}

// Given an upstream that mints a cookie for the right token, When
// Cookie is called three times, Then the exchange happens exactly once
// and every call returns the minted pair.
func TestUpstreamAuthTokenExchangeCached(t *testing.T) {
	const want = "dsh-auth-abc123=v1.body.sig"
	srv, requests := newAuthUpstream(t, mintHandler("secret", want+"; Max-Age=86400; Path=/; HttpOnly; SameSite=Strict"))
	auth := newUpstreamAuth("secret", authorityOf(srv))

	for i := 0; i < 3; i++ {
		got, err := auth.Cookie(context.Background())
		if err != nil {
			t.Fatalf("Cookie call %d: %v", i, err)
		}
		if got != want {
			t.Fatalf("Cookie call %d = %q, want %q", i, got, want)
		}
	}
	if n := requests.Load(); n != 1 {
		t.Fatalf("upstream requests = %d, want 1 (cache reuse)", n)
	}
}

// Given an upstream answering 401 (wrong token), When Cookie is
// called, Then it errors and caches nothing.
func TestUpstreamAuthWrongToken(t *testing.T) {
	srv, requests := newAuthUpstream(t, func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
	})
	auth := newUpstreamAuth("wrong", authorityOf(srv))

	if _, err := auth.Cookie(context.Background()); err == nil {
		t.Fatal("Cookie: expected error on 401, got nil")
	}
	if n := requests.Load(); n != 1 {
		t.Fatalf("upstream requests = %d, want 1", n)
	}
}

// Given a minted cookie, When Invalidate runs (upstream answered 401
// on the relayed cookie), Then the next Cookie re-mints upstream.
func TestUpstreamAuthInvalidateRemints(t *testing.T) {
	srv, requests := newAuthUpstream(t, mintHandler("secret", "dsh-auth-x=v1.a.b; Path=/"))
	auth := newUpstreamAuth("secret", authorityOf(srv))

	if _, err := auth.Cookie(context.Background()); err != nil {
		t.Fatalf("Cookie: %v", err)
	}
	auth.Invalidate()
	if _, err := auth.Cookie(context.Background()); err != nil {
		t.Fatalf("Cookie after Invalidate: %v", err)
	}
	if n := requests.Load(); n != 2 {
		t.Fatalf("upstream requests = %d, want 2 (remint after Invalidate)", n)
	}
}

// Given a legacy upstream (no launch token), When any method runs,
// Then everything no-ops and the upstream is never contacted.
func TestUpstreamAuthLegacyDisabled(t *testing.T) {
	srv, requests := newAuthUpstream(t, func(w http.ResponseWriter, _ *http.Request) {
		t.Error("legacy mode must never contact the upstream")
		w.WriteHeader(http.StatusInternalServerError)
	})
	auth := newUpstreamAuth("", authorityOf(srv))

	if auth.Enabled() {
		t.Fatal("Enabled() = true for empty token, want false")
	}
	got, err := auth.Cookie(context.Background())
	if err != nil || got != "" {
		t.Fatalf("Cookie() = %q, %v; want \"\", nil", got, err)
	}
	if err := auth.Prime(context.Background()); err != nil {
		t.Fatalf("Prime: %v", err)
	}
	auth.Invalidate()
	if n := requests.Load(); n != 0 {
		t.Fatalf("upstream requests = %d, want 0", n)
	}
}

// Given a minted cookie with the exact upstream shape, When Cookie
// returns it, Then the name=value pair is forwarded verbatim with all
// attributes stripped.
func TestUpstreamAuthCookieVerbatim(t *testing.T) {
	const want = "dsh-auth-Zm9vLzEyMw=v1.ABC_def-123.XYZ_456-sig"
	srv, _ := newAuthUpstream(t, mintHandler("secret",
		want+"; Max-Age=3600; Path=/; Expires=Wed, 01 Jan 2031 00:00:00 GMT; HttpOnly; SameSite=Strict"))
	auth := newUpstreamAuth("secret", authorityOf(srv))

	got, err := auth.Cookie(context.Background())
	if err != nil {
		t.Fatalf("Cookie: %v", err)
	}
	if got != want {
		t.Fatalf("Cookie = %q, want verbatim %q", got, want)
	}
}

// Given an upstream answering 303 WITHOUT any Set-Cookie, When Cookie
// is called, Then it errors instead of relaying an empty cookie.
func TestUpstreamAuthSeeOtherWithoutCookie(t *testing.T) {
	srv, _ := newAuthUpstream(t, func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Location", "/")
		w.WriteHeader(http.StatusSeeOther)
	})
	auth := newUpstreamAuth("secret", authorityOf(srv))

	if _, err := auth.Cookie(context.Background()); err == nil {
		t.Fatal("Cookie: expected error on 303 without Set-Cookie, got nil")
	}
}

// Given garbage and foreign Set-Cookie headers around the valid one,
// When Cookie is called, Then only the dsh-auth- pair is picked.
func TestUpstreamAuthSkipsGarbageSetCookie(t *testing.T) {
	srv, _ := newAuthUpstream(t, func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Add("Set-Cookie", ";;;")
		w.Header().Add("Set-Cookie", "garbage-no-equals")
		w.Header().Add("Set-Cookie", "other=1; Path=/")
		w.Header().Add("Set-Cookie", "dsh-auth-multi=v1.ok.sig; Path=/; HttpOnly")
		w.Header().Set("Location", "/")
		w.WriteHeader(http.StatusSeeOther)
	})
	auth := newUpstreamAuth("secret", authorityOf(srv))

	got, err := auth.Cookie(context.Background())
	if err != nil {
		t.Fatalf("Cookie: %v", err)
	}
	if want := "dsh-auth-multi=v1.ok.sig"; got != want {
		t.Fatalf("Cookie = %q, want %q", got, want)
	}
}

// Given ONLY malformed Set-Cookie headers on a 303, When Cookie is
// called, Then it errors.
func TestUpstreamAuthOnlyGarbageSetCookie(t *testing.T) {
	srv, _ := newAuthUpstream(t, func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Add("Set-Cookie", ";;;")
		w.Header().Add("Set-Cookie", "dsh-authless-no-equals")
		w.Header().Set("Location", "/")
		w.WriteHeader(http.StatusSeeOther)
	})
	auth := newUpstreamAuth("secret", authorityOf(srv))

	if _, err := auth.Cookie(context.Background()); err == nil {
		t.Fatal("Cookie: expected error on 303 with only malformed cookies, got nil")
	}
}

// Given a slow upstream, When ten goroutines call Cookie concurrently,
// Then all share one exchange (singleflight) and get the same cookie.
func TestUpstreamAuthSingleflightMint(t *testing.T) {
	const want = "dsh-auth-shared=v1.one.mint"
	srv, requests := newAuthUpstream(t, func(w http.ResponseWriter, r *http.Request) {
		time.Sleep(150 * time.Millisecond)
		mintHandler("secret", want+"; Path=/")(w, r)
	})
	auth := newUpstreamAuth("secret", authorityOf(srv))

	const callers = 10
	var wg sync.WaitGroup
	errs := make(chan error, callers)
	cookies := make(chan string, callers)
	for i := 0; i < callers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			c, err := auth.Cookie(context.Background())
			if err != nil {
				errs <- err
				return
			}
			cookies <- c
		}()
	}
	wg.Wait()
	close(errs)
	close(cookies)

	for err := range errs {
		t.Errorf("concurrent Cookie: %v", err)
	}
	for c := range cookies {
		if c != want {
			t.Errorf("concurrent Cookie = %q, want %q", c, want)
		}
	}
	if n := requests.Load(); n != 1 {
		t.Fatalf("upstream requests = %d, want 1 (singleflight)", n)
	}
}

// Given an upstream that never responds, When Cookie is called, Then
// the 5 s client timeout wins instead of hanging forever.
func TestUpstreamAuthHungUpstreamTimesOut(t *testing.T) {
	srv, _ := newAuthUpstream(t, func(_ http.ResponseWriter, r *http.Request) {
		<-r.Context().Done()
	})
	auth := newUpstreamAuth("secret", authorityOf(srv))

	start := time.Now()
	if _, err := auth.Cookie(context.Background()); err == nil {
		t.Fatal("Cookie: expected timeout error, got nil")
	}
	if elapsed := time.Since(start); elapsed > 10*time.Second {
		t.Fatalf("Cookie hung for %s; the 5 s client timeout did not win", elapsed)
	}
}

// Given a fresh cookie, When Prime runs, Then it populates the cache
// best-effort and reports exchange failures to the caller for logging.
func TestUpstreamAuthPrime(t *testing.T) {
	srv, requests := newAuthUpstream(t, mintHandler("secret", "dsh-auth-p=v1.a.b; Path=/"))
	auth := newUpstreamAuth("secret", authorityOf(srv))

	if err := auth.Prime(context.Background()); err != nil {
		t.Fatalf("Prime: %v", err)
	}
	if n := requests.Load(); n != 1 {
		t.Fatalf("upstream requests = %d, want 1", n)
	}

	bad, _ := newAuthUpstream(t, func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
	})
	if err := newUpstreamAuth("wrong", authorityOf(bad)).Prime(context.Background()); err == nil {
		t.Fatal("Prime: expected exchange error for logging, got nil")
	}
}
