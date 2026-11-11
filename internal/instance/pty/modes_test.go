package pty

import (
	"strings"
	"testing"
)

func preambleAfter(chunks ...string) string {
	t := newModeTracker()
	for _, c := range chunks {
		t.Write(c)
	}
	return t.Preamble()
}

func TestModeTracker_DefaultPreamble(t *testing.T) {
	t.Parallel()
	p := preambleAfter()
	// Always release synchronized output and reset the pen.
	for _, want := range []string{"\x1b[?2026l", "\x1b[0m"} {
		if !strings.Contains(p, want) {
			t.Fatalf("preamble %q missing %q", p, want)
		}
	}
	// Defaults: no alt screen, no mouse, autowrap on, cursor visible.
	if strings.Contains(p, "\x1b[?1049h") {
		t.Fatalf("default preamble must not enter the alternate screen: %q", p)
	}
	for _, want := range []string{
		"\x1b[?9l", "\x1b[?1000l", "\x1b[?1002l", "\x1b[?1003l",
		"\x1b[?1006l", "\x1b[?1016l",
		"\x1b[?7h", "\x1b[?25h",
	} {
		if !strings.Contains(p, want) {
			t.Fatalf("default preamble %q missing %q", p, want)
		}
	}
	if strings.Contains(p, "\x1b[?1003h") {
		t.Fatalf("default preamble must not enable mouse tracking: %q", p)
	}
}

func TestModeTracker_MouseProtocolLastSetWins(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name  string
		input string
		want  string
	}{
		{"x10", "\x1b[?9h", "\x1b[?9h"},
		{"vt200", "\x1b[?1000h", "\x1b[?1000h"},
		{"drag", "\x1b[?1002h", "\x1b[?1002h"},
		{"any", "\x1b[?1003h", "\x1b[?1003h"},
		{"later protocol wins", "\x1b[?1000h\x1b[?1003h", "\x1b[?1003h"},
		{"reset any protocol clears", "\x1b[?1003h\x1b[?1000l", ""},
		{"reset unknown protocol clears", "\x1b[?1003h\x1b[?9l", ""},
	}
	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			p := preambleAfter(tc.input)
			if tc.want == "" {
				for _, mode := range []string{"\x1b[?9h", "\x1b[?1000h", "\x1b[?1002h", "\x1b[?1003h"} {
					if strings.Contains(p, mode) {
						t.Fatalf("preamble %q unexpectedly enables %q", p, mode)
					}
				}
				return
			}
			if !strings.Contains(p, tc.want) {
				t.Fatalf("preamble %q missing %q", p, tc.want)
			}
		})
	}
}

func TestModeTracker_MouseEncoding(t *testing.T) {
	t.Parallel()
	cases := []struct {
		input string
		want  string
	}{
		{"\x1b[?1006h", "\x1b[?1006h"},
		{"\x1b[?1016h", "\x1b[?1016h"},
		{"\x1b[?1006h\x1b[?1016h", "\x1b[?1016h"},
		{"\x1b[?1016h\x1b[?1006h", "\x1b[?1006h"},
		{"\x1b[?1006h\x1b[?1006l", ""},
	}
	for _, tc := range cases {
		tc := tc
		t.Run(tc.input, func(t *testing.T) {
			t.Parallel()
			p := preambleAfter(tc.input)
			if tc.want == "" {
				for _, mode := range []string{"\x1b[?1006h", "\x1b[?1016h"} {
					if strings.Contains(p, mode) {
						t.Fatalf("preamble %q unexpectedly enables %q", p, mode)
					}
				}
				return
			}
			if !strings.Contains(p, tc.want) {
				t.Fatalf("preamble %q missing %q", p, tc.want)
			}
		})
	}
}

func TestModeTracker_AlternateScreen(t *testing.T) {
	t.Parallel()
	for _, mode := range []string{"47", "1047", "1049"} {
		p := preambleAfter("\x1b[?" + mode + "h")
		if !strings.Contains(p, "\x1b[?"+mode+"h") {
			t.Fatalf("preamble %q missing alt-screen enter %s", p, mode)
		}
	}
	p := preambleAfter("\x1b[?1049h\x1b[?1049l")
	if strings.Contains(p, "\x1b[?1049h") {
		t.Fatalf("preamble %q must not enter the alternate screen after an exit", p)
	}
}

func TestModeTracker_AutowrapCursorAndInputModes(t *testing.T) {
	t.Parallel()
	p := preambleAfter(
		"\x1b[?7l\x1b[?25l\x1b[?1004h\x1b[?2004h\x1b[?1h\x1b[?66h",
	)
	for _, want := range []string{
		"\x1b[?7l", "\x1b[?25l", "\x1b[?1004h", "\x1b[?2004h", "\x1b[?1h", "\x1b[?66h",
	} {
		if !strings.Contains(p, want) {
			t.Fatalf("preamble %q missing %q", p, want)
		}
	}
}

func TestModeTracker_SequencesSplitAcrossWrites(t *testing.T) {
	t.Parallel()
	p := preambleAfter("\x1b", "[?10", "03", "h", "\x1b[?100", "6h")
	if !strings.Contains(p, "\x1b[?1003h") {
		t.Fatalf("split 1003h was not tracked: %q", p)
	}
	if !strings.Contains(p, "\x1b[?1006h") {
		t.Fatalf("split 1006h was not tracked: %q", p)
	}

	// A split ESC at the very end of a chunk must not be lost.
	p = preambleAfter("abc\x1b", "[?1049h")
	if !strings.Contains(p, "\x1b[?1049h") {
		t.Fatalf("ESC split across chunks was not tracked: %q", p)
	}
}

func TestModeTracker_CombinedParameters(t *testing.T) {
	t.Parallel()
	p := preambleAfter("\x1b[?1000;1006;1004;2004h")
	for _, want := range []string{
		"\x1b[?1000h", "\x1b[?1006h", "\x1b[?1004h", "\x1b[?2004h",
	} {
		if !strings.Contains(p, want) {
			t.Fatalf("combined DECSET %q missing %q", p, want)
		}
	}
}

func TestModeTracker_ControlStringTerminationMatchesXterm(t *testing.T) {
	t.Parallel()

	// Plain OSC content (no ESC/CAN/SUB/ST/BEL) is ignored. The `?1003h`
	// inside the title has no ESC, so it is not a DECSET.
	p := preambleAfter("\x1b]0;title ?1003h\x07", "\x1b[?1002h")
	if !strings.Contains(p, "\x1b[?1002h") {
		t.Fatalf("real DECSET after OSC was not tracked: %q", p)
	}
	if strings.Contains(p, "\x1b[?1003h") {
		t.Fatalf("DECSET text inside OSC was incorrectly tracked: %q", p)
	}

	// ESC ends OSC and starts a NEW sequence, as in xterm's OSC_END ->
	// ESCAPE transition. The following CSI is real and must be tracked.
	p = preambleAfter("\x1b]0;title \x1b[?1003h\x07")
	if !strings.Contains(p, "\x1b[?1003h") {
		t.Fatalf("CSI after ESC-terminated OSC was not tracked: %q", p)
	}

	// CAN/SUB terminate OSC too (xterm's OSC_END abort path).
	for _, term := range []string{"\x18", "\x1a", "\x9c"} {
		p = preambleAfter("\x1b]0;title" + term + "\x1b[?1003h")
		if !strings.Contains(p, "\x1b[?1003h") {
			t.Fatalf("CSI after OSC terminator %q was not tracked: %q", term, p)
		}
	}
}

func TestModeTracker_DCSAndAPCStringTerminationMatchesXterm(t *testing.T) {
	t.Parallel()

	// Plain DCS content is ignored until a terminator.
	p := preambleAfter("\x1bPjunk ?1003h\x9c", "\x1b[?1002h")
	if strings.Contains(p, "\x1b[?1003h") {
		t.Fatalf("DECSET text inside DCS was incorrectly tracked: %q", p)
	}
	if !strings.Contains(p, "\x1b[?1002h") {
		t.Fatalf("real DECSET after DCS was not tracked: %q", p)
	}

	// ESC/CAN/SUB end DCS (xterm's DCS_UNHOOK path); the following CSI is
	// a new sequence and must be tracked.
	// ESC is both the DCS terminator and the ESCAPE-state introducer, so
	// the CSI that follows starts at `[`, not at a second ESC.
	p = preambleAfter("\x1bPjunk\x1b[?1003h")
	if !strings.Contains(p, "\x1b[?1003h") {
		t.Fatalf("CSI after ESC-terminated DCS was not tracked: %q", p)
	}
	for _, term := range []string{"\x18", "\x1a", "\x9c"} {
		p = preambleAfter("\x1bPjunk" + term + "\x1b[?1003h")
		if !strings.Contains(p, "\x1b[?1003h") {
			t.Fatalf("CSI after DCS terminator %q was not tracked: %q", term, p)
		}
	}

	// APC (ESC _) shares the same string-state handling. Plain content is
	// ignored; ESC/CAN/SUB return to ground and the next CSI is tracked.
	p = preambleAfter("\x1b_junk ?1003h\x9c", "\x1b[?1002h")
	if strings.Contains(p, "\x1b[?1003h") {
		t.Fatalf("DECSET text inside APC was incorrectly tracked: %q", p)
	}
	if !strings.Contains(p, "\x1b[?1002h") {
		t.Fatalf("real DECSET after APC was not tracked: %q", p)
	}
	p = preambleAfter("\x1b_junk\x1b[?1003h")
	if !strings.Contains(p, "\x1b[?1003h") {
		t.Fatalf("CSI after ESC-terminated APC was not tracked: %q", p)
	}
	for _, term := range []string{"\x18", "\x1a", "\x9c"} {
		p = preambleAfter("\x1b_junk" + term + "\x1b[?1003h")
		if !strings.Contains(p, "\x1b[?1003h") {
			t.Fatalf("CSI after APC terminator %q was not tracked: %q", term, p)
		}
	}
}

func TestModeTracker_CSIAbortMatchesXterm(t *testing.T) {
	t.Parallel()
	// A truncated CSI must not swallow the next sequence. ESC restarts the
	// parser; CAN/SUB/ST abort it.
	// ESC is both the abort and the ESCAPE-state introducer, so the next
	// CSI starts at `[`.
	p := preambleAfter("\x1b[?100\x1b[?1049h")
	if !strings.Contains(p, "\x1b[?1049h") {
		t.Fatalf("ESC abort swallowed the following DECSET: %q", p)
	}
	for _, abort := range []string{"\x18", "\x1a", "\x9c"} {
		p := preambleAfter("\x1b[?100" + abort + "\x1b[?1049h")
		if !strings.Contains(p, "\x1b[?1049h") {
			t.Fatalf("CSI abort %q swallowed the following DECSET: %q", abort, p)
		}
	}
}

func TestModeTracker_RISResetsModes(t *testing.T) {
	t.Parallel()
	p := preambleAfter("\x1b[?1003h\x1b[?1006h\x1b[?1049h\x1b[?7l\x1bc")
	if strings.Contains(p, "\x1b[?1003h") || strings.Contains(p, "\x1b[?1006h") || strings.Contains(p, "\x1b[?1049h") {
		t.Fatalf("RIS did not reset modes: %q", p)
	}
	for _, want := range []string{"\x1b[?7h", "\x1b[?25h"} {
		if !strings.Contains(p, want) {
			t.Fatalf("RIS preamble %q missing default %q", p, want)
		}
	}
}

func TestModeTracker_Ignores1007And2026(t *testing.T) {
	t.Parallel()
	p := preambleAfter("\x1b[?1007h\x1b[?2026h")
	if strings.Contains(p, "\x1b[?1007h") {
		t.Fatalf("preamble must never re-enable DECSET 1007: %q", p)
	}
	if !strings.Contains(p, "\x1b[?2026l") {
		t.Fatalf("preamble must release synchronized output: %q", p)
	}
	if strings.Contains(p, "\x1b[?2026h") {
		t.Fatalf("preamble must not re-enable synchronized output: %q", p)
	}
}

func TestModeTracker_IgnoresNonDECModes(t *testing.T) {
	t.Parallel()
	// Plain ANSI mode 4 (insert) and an SGR sequence must not be parsed as
	// DEC private modes.
	p := preambleAfter("\x1b[4h\x1b[1;31m\x1b[?1003h")
	if !strings.Contains(p, "\x1b[?1003h") {
		t.Fatalf("real DECSET after ANSI/SGR was not tracked: %q", p)
	}
}

func TestModeTracker_PreambleIsASCII(t *testing.T) {
	t.Parallel()
	// The GET path transports the preamble as base64 and the browser decodes
	// it with atob(); that round-trip is only sound while every emitted byte
	// is ASCII. Keep the invariant pinned at the source.
	p := preambleAfter(
		"\x1b[?1049h\x1b[?1003h\x1b[?1006h\x1b[?1004h\x1b[?2004h" +
			"\x1b[?1h\x1b[?66h\x1b[?7l\x1b[?25l",
	)
	for i := 0; i < len(p); i++ {
		if p[i] >= 0x80 {
			t.Fatalf("preamble byte %d = 0x%02x is non-ASCII: %q", i, p[i], p)
		}
	}
}
