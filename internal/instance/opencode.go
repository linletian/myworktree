package instance

import (
	"crypto/rand"
	"encoding/hex"
	"os"
	"regexp"
	"strings"
)

// Command returns the hardcoded opencode CLI invocation for opencode-web instances.
// Users cannot override this via tags.json — Manager ignores tag.Command for this kind.
func Command() (exe string, args []string) {
	return "opencode", []string{"serve", "--hostname", "127.0.0.1", "--port", "0"}
}

// GeneratePassword returns a 32-character hex string (16 random bytes) for
// OPENCODE_SERVER_PASSWORD.
func GeneratePassword() (string, error) {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return hex.EncodeToString(b), nil
}

// BuildEnv merges os.Environ(), tag-provided env, and forced overrides.
// OPENCODE_SERVER_PASSWORD and OPENCODE_CLIENT are always set to myworktree's
// values, regardless of what tagEnv contains.
func BuildEnv(tagEnv map[string]string, password string) []string {
	seen := make(map[string]string)
	// Start with current process environment.
	for _, kv := range os.Environ() {
		k, v, _ := strings.Cut(kv, "=")
		seen[k] = v
	}
	// Layer tag-provided env on top.
	for k, v := range tagEnv {
		seen[k] = v
	}
	// Force myworktree overrides (last write wins).
	seen["OPENCODE_SERVER_PASSWORD"] = password
	seen["OPENCODE_CLIENT"] = "myworktree"

	out := make([]string, 0, len(seen))
	for k, v := range seen {
		out = append(out, k+"="+v)
	}
	return out
}

var listeningAddrRe = regexp.MustCompile(`opencode server listening on http://([^:\s]+):(\d+)`)

// ExtractListeningAddress parses an opencode stdout line for the listening
// address. Returns host, port, and whether the line matched. ANSI escape
// sequences in the input are stripped before matching.
func ExtractListeningAddress(line string) (host string, port string, ok bool) {
	clean := stripANSI(line)
	m := listeningAddrRe.FindStringSubmatch(clean)
	if len(m) != 3 {
		return "", "", false
	}
	return m[1], m[2], true
}

var ansiRe = regexp.MustCompile(`\x1b\[[0-9;]*[a-zA-Z]`)

func stripANSI(s string) string {
	return strings.TrimSpace(ansiRe.ReplaceAllString(s, ""))
}

// IsAPIPath reports whether the request path targets an opencode API endpoint
// that needs a ?directory=<worktree> query parameter injection.
func IsAPIPath(p string) bool {
	if strings.HasPrefix(p, "/api/") || strings.HasPrefix(p, "/global/") {
		return true
	}
	switch p {
	case "/doc", "/config", "/session", "/agent", "/command", "/skill",
		"/lsp", "/formatter", "/mcp", "/provider", "/project",
		"/experimental", "/tui", "/vcs", "/path", "/instance",
		"/file", "/find", "/event", "/log", "/auth":
		return true
	}
	return false
}
