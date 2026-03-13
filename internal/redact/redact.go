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
	// Uses heuristics to avoid false positives:
	// - At least one parameter must be >= 10 (large coord or button)
	// - Must end with M/m (mouse) or R/r (cursor report)
	// - Optional leading [ and < for SGR mode
	// Examples matched: [<0;107;35M (mouse), 35;107;1M (no prefix), [35;107R (cursor)
	// Examples NOT matched: [1;2;3] (array), [0] (index), normal text
	controlSeqRemnant = regexp.MustCompile(`(?:\[)?(?:<)?(?:` +
		`\d{2,};\d+[MmRr]|` + // big;any (2 params: cursor or mouse)
		`\d+;\d{2,}[MmRr]|` + // any;big (2 params)
		`\d{2,};\d{2,};\d+[MmRr]|` + // big;big;any (3 params: mouse)
		`\d{2,};\d+;\d{2,}[MmRr]|` + // big;any;big
		`\d+;\d{2,};\d{2,}[MmRr]` + // any;big;big
		`)`)
)

func Text(s string) string {
	s = skPattern.ReplaceAllString(s, "sk-REDACTED")
	s = bearerPattern.ReplaceAllString(s, "Bearer REDACTED")
	// DISABLED: Backend control sequence filtering breaks TUI programs
	// This filter removes sequences in real-time, corrupting TUI display.
	// Sequences are filtered in frontend (loadLog) only for stopped instances.
	// s = controlSeqRemnant.ReplaceAllString(s, "")
	return s
}

func EnvKey(key, value string) string {
	k := strings.ToUpper(strings.TrimSpace(key))
	if strings.Contains(k, "TOKEN") || strings.Contains(k, "SECRET") || strings.Contains(k, "KEY") || strings.Contains(k, "PASSWORD") {
		return "***"
	}
	return value
}
