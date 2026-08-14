// Package authq centralizes the removal of myworktree's ?token= auth
// parameter from query strings before reverse proxies forward requests
// upstream. Every proxy path MUST route outgoing queries through
// StripToken — a forgotten path leaks the credential to the upstream
// subprocess. Grep for RawQuery/RawQuery assignments in proxy code when
// adding a new proxied route.
package authq

import (
	"net/url"
	"regexp"
	"strings"
)

// tokenSegRe matches one token=<value> segment at the start of the query
// or after & / ; separators. The value is everything up to the next
// separator, so it also matches values with malformed %-escapes.
var tokenSegRe = regexp.MustCompile(`(^|[&;])token=[^&;]*`)

// StripToken removes the token parameter from rawQuery. When the query
// parses cleanly it round-trips through url.Values (only touched when a
// token is present, so unrelated parameters keep their original encoding
// and order otherwise). On a parse failure (e.g. a malformed %-escape)
// re-encoding would mangle unrelated parameters, so the token segments
// are stripped verbatim instead; if no token segment is recognizable,
// the query is dropped entirely rather than forwarding a credential.
func StripToken(rawQuery string) string {
	if rawQuery == "" {
		return ""
	}
	q, err := url.ParseQuery(rawQuery)
	if err == nil {
		if !q.Has("token") {
			return rawQuery
		}
		q.Del("token")
		return q.Encode()
	}
	if !tokenSegRe.MatchString(rawQuery) {
		return ""
	}
	cleaned := tokenSegRe.ReplaceAllString(rawQuery, "")
	cleaned = strings.TrimLeft(cleaned, "&;")
	return cleaned
}
