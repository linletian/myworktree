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
	// Matches mouse events and cursor position reports from TUI programs.
	// Requires parameters to be at least 2 digits to avoid false positives
	// like [1;2;3] array notation in normal text.
	// Examples matched: [<35;55;34M (SGR mouse), [535;52;38M (mouse), [<35;55;34R (cursor report)
	// Examples NOT matched: [1;2;3M, [10;20M (likely array notation)
	controlSeqRemnant = regexp.MustCompile(`\[(?:<\d{2,}(?:;\d{2,}){1,}[MmRr]|\d{2,}(?:;\d{2,}){2,}[MmRr])`)
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
