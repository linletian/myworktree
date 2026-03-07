package redact

import (
	"regexp"
	"strings"
)

var (
	// Covers common AI key prefixes like sk-..., sk-ant-...
	skPattern = regexp.MustCompile(`\bsk-[A-Za-z0-9_-]{16,}\b`)
	// Generic bearer-like secrets in logs.
	bearerPattern = regexp.MustCompile(`\bBearer\s+[A-Za-z0-9._-]{16,}\b`)
)

func Text(s string) string {
	s = skPattern.ReplaceAllString(s, "sk-REDACTED")
	s = bearerPattern.ReplaceAllString(s, "Bearer REDACTED")
	return s
}

func EnvKey(key, value string) string {
	k := strings.ToUpper(strings.TrimSpace(key))
	if strings.Contains(k, "TOKEN") || strings.Contains(k, "SECRET") || strings.Contains(k, "KEY") || strings.Contains(k, "PASSWORD") {
		return "***"
	}
	return value
}
