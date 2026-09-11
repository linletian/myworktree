package dsh_web

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"
)

// upstreamCookieFreshFor is how long a minted dsh-auth-* cookie is
// reused before a fresh token exchange. dsh signs browser cookies with
// a configurable max age (days by default); 12 h stays comfortably
// below any sane upstream Max-Age while keeping the exchange off the
// proxied-request hot path.
const upstreamCookieFreshFor = 12 * time.Hour

// upstreamAuthTimeout bounds the token-exchange GET. The upstream is a
// loopback or LAN dsh process; 5 s is generous and keeps a hung
// upstream from wedging proxied requests.
const upstreamAuthTimeout = 5 * time.Second

// invalidateThrottle bounds how often Invalidate actually drops the
// cached cookie: a client repeatedly eliciting upstream 401s would
// otherwise force mutex-serialized re-mints on the proxied-request hot
// path. Tradeoff: a genuine rotation WITHIN the window is not honored
// immediately — recovery happens on the next 401 after the window.
const invalidateThrottle = 5 * time.Second

// upstreamAuth relays dsh's browser-session token exchange
// (packages/client/connection/src/browser-auth.ts authorizeIndex) for
// the reverse proxy: GET http://<authority>/?token=<token> answers 303
// plus a `dsh-auth-<hash>=<value>` Set-Cookie, which the proxy then
// replays as an ordinary Cookie header on API/WebSocket requests so
// the browser never sees the launch token.
//
// An empty token means the upstream predates browser auth (legacy dsh
// < 0.1.2): Enabled is false and every method no-ops.
//
// Nothing is persisted to disk, and neither the token nor the minted
// cookie value is ever logged — exchange failures surface only as
// wrapped errors for the caller to report.
type upstreamAuth struct {
	token     string
	authority string

	mu       sync.Mutex
	cookie   string
	mintedAt time.Time
	// lastInvalidate backs the Invalidate throttle (see
	// invalidateThrottle); zero means "never invalidated".
	lastInvalidate time.Time

	client *http.Client
}

func newUpstreamAuth(token, authority string) *upstreamAuth {
	return &upstreamAuth{
		token:     token,
		authority: authority,
		client: &http.Client{
			Timeout: upstreamAuthTimeout,
			// The exchange answer IS the 303; following it would
			// re-request / with the cookie and mask mint failures.
			CheckRedirect: func(_ *http.Request, _ []*http.Request) error {
				return http.ErrUseLastResponse
			},
		},
	}
}

// Enabled reports whether the upstream gates its SPA/API behind
// browser auth. Legacy upstreams (empty token) get a no-op relay.
func (a *upstreamAuth) Enabled() bool {
	return a.token != ""
}

// Cookie returns a fresh `dsh-auth-*=value` pair, minting one via the
// token exchange when the cache is empty or stale. Legacy upstreams
// return "" with no error.
func (a *upstreamAuth) Cookie(ctx context.Context) (string, error) {
	if !a.Enabled() {
		return "", nil
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	// Re-check after acquiring the lock: concurrent callers share the
	// first caller's mint (singleflight).
	if cookie := a.cachedLocked(); cookie != "" {
		return cookie, nil
	}
	return a.mintLocked(ctx)
}

// Invalidate drops the cached cookie; the next Cookie re-mints. Called
// when the upstream answers 401 to a relayed cookie (secret rotation,
// restart, expiry). Throttled: a drop less than invalidateThrottle
// after the previous one is a no-op, so a client that keeps eliciting
// 401s cannot force a re-mint per request.
func (a *upstreamAuth) Invalidate() {
	if !a.Enabled() {
		return
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	if time.Since(a.lastInvalidate) < invalidateThrottle {
		return
	}
	a.lastInvalidate = time.Now()
	a.cookie = ""
	a.mintedAt = time.Time{}
}

// Prime mints the cookie best-effort at startup so the first proxied
// request does not pay the exchange latency. The error is returned for
// the caller to log; a failed Prime is not fatal — Cookie retries.
func (a *upstreamAuth) Prime(ctx context.Context) error {
	if !a.Enabled() {
		return nil
	}
	_, err := a.Cookie(ctx)
	return err
}

// cachedLocked returns the cached cookie when fresh. Caller holds mu.
func (a *upstreamAuth) cachedLocked() string {
	if a.cookie == "" || time.Since(a.mintedAt) >= upstreamCookieFreshFor {
		return ""
	}
	return a.cookie
}

// mintLocked performs the token exchange. Caller holds mu.
func (a *upstreamAuth) mintLocked(ctx context.Context) (string, error) {
	exchange := url.URL{Scheme: "http", Host: a.authority, Path: "/"}
	query := exchange.Query()
	query.Set("token", a.token)
	exchange.RawQuery = query.Encode()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, exchange.String(), nil)
	if err != nil {
		return "", fmt.Errorf("dsh upstream auth: build exchange request: %w", sanitizeExchangeError(err))
	}
	res, err := a.client.Do(req)
	if err != nil {
		return "", fmt.Errorf("dsh upstream auth: token exchange: %w", sanitizeExchangeError(err))
	}
	defer func() {
		_, _ = io.Copy(io.Discard, res.Body) // keep-alive reuse
		_ = res.Body.Close()
	}()

	if res.StatusCode != http.StatusSeeOther {
		return "", fmt.Errorf("dsh upstream auth: token exchange: unexpected status %s", res.Status)
	}
	for _, header := range res.Header.Values("Set-Cookie") {
		pair, _, _ := strings.Cut(header, ";")
		name, value, ok := strings.Cut(pair, "=")
		if !ok || !strings.HasPrefix(name, "dsh-auth-") || value == "" {
			continue
		}
		a.cookie = pair
		a.mintedAt = time.Now()
		return pair, nil
	}
	return "", fmt.Errorf("dsh upstream auth: token exchange: 303 without a dsh-auth- cookie")
}

// sanitizeExchangeError strips the *url.Error wrapper a failed exchange
// carries: url.Error.Error() embeds the FULL request URL including the
// ?token=<launch-token> query, and every caller logs the returned
// error — the token (a live credential) must never reach a log string.
// Unwrap to the transport cause so errors.As/Is still classify it
// (e.g. *net.OpError, context.DeadlineExceeded); non-url errors pass
// through unchanged.
func sanitizeExchangeError(err error) error {
	var urlErr *url.Error
	if errors.As(err, &urlErr) && urlErr.Err != nil {
		return urlErr.Err
	}
	return err
}
