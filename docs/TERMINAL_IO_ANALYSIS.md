# Terminal I/O Analysis

This document provides a comprehensive analysis of terminal input/output scenarios, character handling, and control sequence filtering in myworktree.

## Table of Contents

- [Overview](#overview)
- [Input Scenarios](#input-scenarios)
- [Output Scenarios](#output-scenarios)
- [Filtering Logic](#filtering-logic)
- [Edge Cases](#edge-cases)
- [Test Coverage](#test-coverage)
- [Architecture Flow](#architecture-flow)

## Overview

myworktree provides a web-based terminal interface that requires careful handling of:
- User input (keystrokes, mouse events, control characters)
- Program output (text, escape sequences, mouse event residues)

The key challenge is filtering **mouse event residues** (incomplete escape sequences) from logs while preserving:
- Normal text output
- Legitimate terminal escape sequences
- User input

## Input Scenarios

### 1.1 Normal Characters

| Type | Example | Handling | Status |
|------|---------|----------|--------|
| Letters | a-z, A-Z | Pass through to PTY | ✓ |
| Numbers | 0-9 | Pass through to PTY | ✓ |
| Symbols | !@#$%^&*() | Pass through to PTY | ✓ |
| Space/Tab | " ", "\t" | Pass through to PTY | ✓ |
| Enter | "\r", "\n" | Pass through to PTY | ✓ |

**Implementation**: Direct write to PTY stdin via `SendInput()`.

### 1.2 Control Characters

| Key | Code | Current Implementation | Status |
|-----|------|------------------------|--------|
| Ctrl+C | 0x03 | Send SIGINT + write to stdin | ✓ |
| Ctrl+Z | 0x1A | Send SIGTSTP + write to stdin | ✓ |
| Ctrl+\ | 0x1C | Send SIGQUIT + write to stdin | ✓ |
| Ctrl+D | 0x04 | Pass through (EOF) | ✓ |
| Ctrl+L | 0x0C | Pass through (redraw) | ✓ |
| Backspace | 0x08 or 0x7F | Pass through | ✓ |

**Rationale**: Ctrl+C/Z/\ send signals for immediate process control, but also write to stdin as fallback (matching real terminal behavior).

**Code**: `internal/instance/manager.go:327-358`

### 1.3 Special Keys (Multi-byte Sequences)

All special keys generate multi-byte escape sequences that are passed through unchanged:

| Key | Sequence | Format | Status |
|-----|----------|--------|--------|
| Up | ESC[A | CSI (Control Sequence Introducer) | ✓ |
| Down | ESC[B | CSI | ✓ |
| Right | ESC[C | CSI | ✓ |
| Left | ESC[D | CSI | ✓ |
| Home | ESC[H or ESC[1~ | CSI | ✓ |
| End | ESC[F or ESC[4~ | CSI | ✓ |
| F1-F4 | ESC OP/Q/R/S | SS3 (Single Shift Select) | ✓ |
| F5-F12 | ESC[15~ etc | CSI | ✓ |
| PageUp | ESC[5~ | CSI | ✓ |
| PageDown | ESC[6~ | CSI | ✓ |
| Insert | ESC[2~ | CSI | ✓ |
| Delete | ESC[3~ | CSI | ✓ |

### 1.4 Modified Keys

| Key | Sequence | Status |
|-----|----------|--------|
| Ctrl+Arrow | ESC[1;5A | ✓ Pass through |
| Alt+Arrow | ESC[1;3A | ✓ Pass through |
| Shift+Arrow | ESC[1;2A | ✓ Pass through |
| Ctrl+Shift+Arrow | ESC[1;6A | ✓ Pass through |

### 1.5 Mouse Input (When Mouse Mode Enabled)

If the terminal program enables mouse tracking, mouse events are sent as input:

| Mode | Format | Example | Status |
|------|--------|---------|--------|
| X10 (1000) | ESC[M bxy (encoded) | ESC[M ?? | ✓ Pass through |
| UTF-8 (1005) | ESC[M bxy (UTF-8) | ESC[M ?? | ✓ Pass through |
| SGR (1006) | ESC[<b;x;yM/m | ESC[<0;10;20M | ✓ Pass through |

**Note**: Mouse input is sent TO the program, not filtered. Only mouse event RESIDUES in output are filtered.

## Output Scenarios

### 2.1 Normal Text Output

| Type | Example | Handling | Status |
|------|---------|----------|--------|
| Plain text | "Hello World" | Pass through | ✓ |
| Unicode | "你好" | Pass through | ✓ |
| Newlines | "\r\n" | Pass through | ✓ |
| Tabs | "\t" | Pass through | ✓ |

### 2.2 Legitimate Terminal Escape Sequences (Should NOT Filter)

These sequences control terminal display and are preserved:

| Type | Sequence | Purpose | Should Filter? |
|------|----------|---------|----------------|
| Cursor movement | ESC[A, ESC[B, ESC[C, ESC[D | Move cursor | NO ✓ |
| Cursor position | ESC[row;colH | Set cursor position | NO ✓ |
| Clear screen | ESC[2J | Clear display | NO ✓ |
| Clear line | ESC[K | Clear to end of line | NO ✓ |
| Colors | ESC[31m, ESC[0m | Set/reset text colors | NO ✓ |
| Bold/Italic | ESC[1m, ESC[3m | Text attributes | NO ✓ |
| Mouse enable | ESC[?1000h | Enable mouse tracking | NO ✓ |
| Mouse disable | ESC[?1000l | Disable mouse tracking | NO ✓ |
| Alternate screen | ESC[?1049h/l | Switch buffer | NO ✓ |
| Set title | ESC]0;titleBEL | Set window title | NO ✓ |

**Why preserved**: These all have the ESC byte (0x1B) prefix, which distinguishes them from residues.

### 2.3 Mouse Event Residues (Should FILTER)

These are incomplete mouse event sequences that appear in logs when the ESC prefix is stripped:

| Scenario | Format | Why it appears | Should Filter? |
|----------|--------|----------------|----------------|
| Mouse click | 35;107;1M | ESC[< stripped | YES ✓ |
| Scroll wheel | 64;80;24M | ESC[< stripped | YES ✓ |
| Multiple events | 35;107;1M35;106;2M | Batch events | YES ✓ |
| Cursor report | 35;107R | ESC[ stripped | YES ✓ |
| With partial prefix | [<0;100;50M | ESC stripped | YES ✓ |

**Characteristics**:
- Missing ESC (0x1B) byte
- May be missing `[` prefix
- Format: `params;params;params` + `M/m/R/r`
- At least one parameter is >= 10 (row/col coordinate)
- Button code can be 0-127 (1-3 digits)

### 2.4 False Positives to AVOID

Patterns that look similar but should NOT be filtered:

| Pattern | Example | Why not filter |
|---------|---------|----------------|
| Array notation | [1;2;3] | No M/R terminator |
| Array index | arr[0] | No semicolon separator |
| JSON array | {"pos": [100, 200]} | No M/R terminator |
| CSS RGB | color: [255;0;0] | No M/R terminator |
| Small coords | [0;5;9M | Too ambiguous (all params < 10) |

## Filtering Logic

### 3.1 Current Implementation

```go
// internal/redact/redact.go
var controlSeqRemnant = regexp.MustCompile(`(?:\[)?(?:<)?(?:` +
    `\d{2,};\d+[MmRr]|` +        // big;any (2 params)
    `\d+;\d{2,}[MmRr]|` +        // any;big (2 params)
    `\d{2,};\d{2,};\d+[MmRr]|` + // big;big;any (3 params)
    `\d{2,};\d+;\d{2,}[MmRr]|` + // big;any;big (3 params)
    `\d+;\d{2,};\d{2,}[MmRr]` +  // any;big;big (3 params)
    `)`)
```

### 3.2 Matching Rules

**Matches** (filter these):
- Optional leading `[` and `<`
- 2 or 3 semicolon-separated numbers
- At least ONE number >= 10
- Ends with `M`, `m`, `R`, or `r`

**Does NOT match** (preserve these):
- All numbers < 10 (too ambiguous)
- No M/R/r terminator
- Normal text
- Legitimate ESC sequences (have 0x1B byte)

### 3.3 Pattern Breakdown

| Pattern | Matches | Example |
|---------|---------|---------|
| `\d{2,};\d+[MmRr]` | 2 params, first >= 10 | `35;107R` (cursor) |
| `\d+;\d{2,}[MmRr]` | 2 params, second >= 10 | `5;100R` (cursor) |
| `\d{2,};\d{2,};\d+[MmRr]` | 3 params, first two >= 10 | `35;107;1M` (mouse) |
| `\d{2,};\d+;\d{2,}[MmRr]` | 3 params, first and last >= 10 | `35;5;100M` (mouse) |
| `\d+;\d{2,};\d{2,}[MmRr]` | 3 params, last two >= 10 | `0;100;50M` (mouse) |

## Edge Cases

### 4.1 Why Not Filter ALL Sequences Like `[...M`?

**Problem**: Legitimate terminal output can contain similar patterns.

**Example**: Cursor positioning uses `ESC[row;colH`. If we see `[10;20H` without ESC, we can't be certain it's a residue.

**Solution**: Require `M/R/r` terminator which is specific to mouse events and cursor reports, not general cursor movement.

### 4.2 What About Small Terminal Sizes?

**Reality**: Terminals < 10 rows/cols are extremely rare.

**Statistics**:
- Most terminals: 24+ rows, 80+ columns
- Small terminals (5x9): Edge case

**Trade-off**: Preserve ambiguous small coordinates to avoid false positives.

**Example**: `[0;5;9M` is preserved because all params < 10.

### 4.3 What About Incomplete Sequences?

**Scenarios**:

| Input | Filtered? | Reason |
|-------|-----------|--------|
| `35;107` | NO | Missing M/R terminator, might be coordinates |
| `;107;1M` | NO | Missing first number, broken sequence |
| `35;107;1M` | YES | Complete, recognizable pattern |

**Principle**: Only filter complete, recognizable patterns.

### 4.4 Unicode and Binary Data

| Type | Handling | Status |
|------|----------|--------|
| UTF-8 text | Passed through correctly | ✓ |
| Binary data | Logged as-is (rare in terminals) | ✓ |
| ESC byte (0x1B) | Preserved in legitimate sequences | ✓ |

## Test Coverage

### 5.1 Unit Tests

Location: `internal/redact/redact_test.go`

**Test Categories**:

| Category | Test Cases | Status |
|----------|-----------|--------|
| Mouse events | 10+ variants | ✓ Pass |
| Cursor reports | 3 variants | ✓ Pass |
| False positives | 7 variants | ✓ Pass |
| Mixed content | 2 variants | ✓ Pass |

### 5.2 Test Cases

#### Mouse Events (Should Filter)

```go
// SGR mouse events
{"SGR left click", "[<0;107;35M", ""}
{"SGR right click", "[<2;50;20M", ""}
{"SGR scroll", "[<64;80;24M", ""}
{"SGR release", "[<0;100;50m", ""}

// Without prefix
{"No prefix button=0", "0;107;35M", ""}
{"No prefix button=1", "35;107;1M", ""}
{"No brackets", "35;107;1M", ""}

// Multiple events
{"Multiple events", "35;107;1M35;106;2M", ""}
{"Scroll wheel", "64;80;24M65;80;24M", ""}
```

#### Cursor Reports (Should Filter)

```go
{"Cursor report", "[35;107R", ""}
{"Cursor no [", "35;107R", ""}
```

#### False Positives (Should Preserve)

```go
{"Array notation", "[1;2;3]", "[1;2;3]"}
{"Array index", "arr[0]", "arr[0]"}
{"JSON", `{"data": [100, 200]}`, `{"data": [100, 200]}`}
{"CSS RGB", "color: [255;0;0]", "color: [255;0;0]"}
{"Small coords", "[0;5;9M", "[0;5;9M]"}
```

#### Mixed Content

```go
{"Text + event", "Error: 35;107;1M occurred", "Error:  occurred"}
{"Multiple in text", "Start35;107;1M35;106;2MEnd", "StartEnd"}
```

### 5.3 Integration Tests

Tested with real TUI programs:
- `top` - Generates mouse event residues
- `vim` - Uses legitimate escape sequences
- `htop` - Interactive TUI with mouse support
- `less` - Pager with scrolling

All programs display correctly without visible mouse event garbage.

## Architecture Flow

### Input Path

```
User Input (Browser)
    ↓
WebSocket/API
    ↓
SendInput() [internal/instance/manager.go]
    ↓
    ├─→ Control chars (Ctrl+C/Z/\)
    │       ↓
    │   Send signal + write to stdin
    │
    └─→ Normal input
            ↓
        Write to PTY stdin
```

### Output Path

```
Program stdout/stderr
    ↓
PTY (pseudo-terminal)
    ↓
pumpLogs() [internal/instance/manager.go]
    ↓
redact.Text() [internal/redact/redact.go]
    ↓
    ├─→ Filter mouse event residues
    │       ↓
    │   Remove from output
    │
    └─→ Preserve everything else
            ↓
    ├─→ Log file (.log)
    └─→ WebSocket → Browser xterm.js
```

### Key Insight

Legitimate escape sequences are preserved because they contain the ESC byte (0x1B), which our regex doesn't match. We only filter residues that are missing this prefix.

## Conclusion

### Coverage Summary

- ✓ Normal characters: All handled correctly
- ✓ Control characters: Ctrl+C/Z/\ specially handled with signals
- ✓ Special keys: All multi-byte sequences passed through
- ✓ Mouse input: Passed through to program (not filtered)
- ✓ Normal output: Preserved
- ✓ Legitimate ESC sequences: Preserved (have 0x1B byte)
- ✓ Mouse event residues: Filtered (missing ESC)
- ✓ False positives: Avoided (require M/R + large params)

### Known Limitations

1. **Tiny terminals (< 10 rows/cols)**: Mouse events may not be filtered
   - Trade-off: Avoid filtering `[0;5;9M` which might be normal text
   - Impact: Minimal (extremely rare terminal size)

2. **Incomplete sequences**: Not filtered
   - Trade-off: Only filter complete, recognizable patterns
   - Impact: Minimal (broken sequences are rare)

### Performance

- Regex compilation: Once at package initialization
- Filtering overhead: O(n) where n = log line length
- Memory: Minimal (streaming filter)

### Maintenance

When adding new filtering rules:

1. Add test cases to `internal/redact/redact_test.go`
2. Run `go test ./internal/redact -v`
3. Verify no regressions in integration tests
4. Update this document if adding new categories

## References

- [ECMA-48](https://www.ecma-international.org/publications-and-standards/standards/ecma-48/) - Terminal escape sequences
- [XTerm Control Sequences](https://invisible-island.net/xterm/ctlseqs/ctlseqs.html) - Mouse tracking modes
- [xterm.js](https://xtermjs.org/) - Browser terminal emulator used in myworktree
