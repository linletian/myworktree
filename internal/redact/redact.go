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
	// Terminal control sequence remnants (without ESC prefix)
	// Matches patterns like [<35;55;34M, [535;52;38M, [<row;col;modifierM, etc.
	// These are typically cursor/position reports or mouse event reports
	// The < is optional to handle both [<...M and [...M formats
	controlSeqRemnant = regexp.MustCompile(`\[<?\d+(?:;\d+)+[MmRr]`)
)

func Text(s string) string {
	s = skPattern.ReplaceAllString(s, "sk-REDACTED")
	s = bearerPattern.ReplaceAllString(s, "Bearer REDACTED")
	// Remove terminal control sequence remnants (e.g., from TUI programs like top)
	s = controlSeqRemnant.ReplaceAllString(s, "")
	return s
}

func EnvKey(key, value string) string {
	k := strings.ToUpper(strings.TrimSpace(key))
	if strings.Contains(k, "TOKEN") || strings.Contains(k, "SECRET") || strings.Contains(k, "KEY") || strings.Contains(k, "PASSWORD") {
		return "***"
	}
	return value
}
