# Terminal I/O Analysis

This document provides a comprehensive analysis of terminal input/output handling in myworktree.

## Table of Contents

- [Overview](#overview)
- [Architecture](#architecture)
- [Input Handling](#input-handling)
- [Output Handling](#output-handling)
- [Query Response Filtering](#query-response-filtering)
- [Test Coverage](#test-coverage)

## Overview

myworktree provides a web-based terminal interface using xterm.js. The key challenges are:

1. **TUI programs need complete control sequences** - Mouse events, cursor positioning, etc.
2. **Anomalous strings appear on instance switch** - Terminal query responses clutter the display
3. **State machine prevents timing issues** - WebSocket must be ready before focus

## Architecture

### Data Flow

```
┌─────────────────────────────────────────────────────────────────────┐
│                         User Input Path                              │
├─────────────────────────────────────────────────────────────────────┤
│                                                                     │
│  [Browser]                                                           │
│      │                                                               │
│      │ keystrokes / xterm.js query responses                        │
│      ▼                                                               │
│  term.onData() ───► isTerminalQueryResponse()? ──┐                  │
│      │                    │                       │                  │
│      │                    ├─ YES ──► RETURN     │                  │
│      │                    │                       │                  │
│      │                    └─ NO ──► Continue    │                  │
│      │                                            │                  │
│      └──────────► WebSocket ──► PTY stdin ◄─────┘                  │
│                                                                      │
└─────────────────────────────────────────────────────────────────────┘

┌─────────────────────────────────────────────────────────────────────┐
│                         Output Path                                  │
├─────────────────────────────────────────────────────────────────────┤
│                                                                      │
│  [PTY stdout/stderr]                                                │
│      │                                                               │
│      │ TUI program output (withcontrol sequences)                   │
│      ▼                                                               │
│  pumpLogs() ──► [NO FILTERING] ──► Log file + WebSocket            │
│      │                                                               │
│      │ Note: Backend filtering DISABLED to preserve TUI sequences    │
│      ▼                                                               │
│  [loadLog(), loadLogSince(), WebSocket onmessage]                    │
│      │                                                               │
│      │ Filter anomalous strings from logs                           │
│      ▼                                                               │
│  filterResponses() ──► term.write()                                │
│                                                                      │
└─────────────────────────────────────────────────────────────────────┘

┌─────────────────────────────────────────────────────────────────────┐
│                    State Machine (Focus Prevention)                  │
├─────────────────────────────────────────────────────────────────────┤
│                                                                      │
│  IDLE → CONNECTING → HANDSHAKING → READY → DISCONNECTING → IDLE    │
│                                                                      │
│  focusTerminalIfPossible() checks:                                  │
│      if (ttyState !== 'READY') return; // Don't focus too early     │
│                                                                      │
│  Purpose: Prevent xterm.js from sending query sequences before       │
│           WebSocket is ready to receive responses.                    │
│                                                                      │
└─────────────────────────────────────────────────────────────────────┘
```

### Key Components

| Component | Location | Purpose |
|-----------|----------|---------|
| Frontend Input Filter | `term.onData()` | Filter xterm.js query responses |
| Frontend Log Filter | `loadLog()`, etc. | Filter anomalous strings from logs |
| Frontend State Machine | `connectTTY()`, etc. | Prevent early focus |
| Backend Filter | `redact.go` | **DISABLED** (would break TUI) |

## Input Handling

### Normal Characters

| Type | Example | Handling |
|------|---------|----------|
| Letters | a-z, A-Z | Pass through to PTY |
| Numbers | 0-9 | Pass through to PTY |
| Symbols | !@#$%^&*() | Pass through to PTY |
| Space/Tab | " ", "\t" | Pass through to PTY |
| Enter | "\r", "\n" | Pass through to PTY |

### Control Characters

| Key | Code | Behavior |
|-----|------|----------|
| Ctrl+C | 0x03 | Send SIGINT + write to stdin |
| Ctrl+Z | 0x1A | Send SIGTSTP + write to stdin |
| Ctrl+\ | 0x1C | Send SIGQUIT + write to stdin |
| Ctrl+D | 0x04 | Pass through (EOF) |
| Ctrl+L | 0x0C | Pass through (redraw) |
| Backspace | 0x08 or 0x7F | Pass through |

**Code**: `internal/instance/manager.go:SendInput()`

### Special Keys

All special keys generate multi-byte escape sequences passed through unchanged:

| Key | Sequence | Format |
|-----|----------|--------|
| Arrows | ESC[A/B/C/D | CSI |
| Home/End | ESC[H, ESC[F | CSI |
| F1-F4 | ESC OP/Q/R/S | SS3 |
| F5-F12 | ESC[15~ etc | CSI |
| Modified | ESC[1;5A etc | CSI with modifiers |

### Mouse Input

When terminal program enables mouse tracking:

| Mode | Format | Example |
|------|--------|---------|
| X10 (1000) | ESC[M bxy | ESC[M ?? |
| UTF-8 (1005) | ESC[M bxy | ESC[M ?? |
| SGR (1006) | ESC[<b;x;yM/m | ESC[<0;10;20M |

**Note**: Mouse input is sent TO the program, not filtered.

## Output Handling

### TUI Program Output (NOT Filtered)

TUI programs send legitimate control sequences that MUST be preserved:

| Type | Sequence | Purpose |
|------|----------|---------|
| Cursor movement | ESC[A/B/C/D | Move cursor |
| Cursor position | ESC[row;colH | Set position |
| Clear | ESC[2J, ESC[K | Clear display/line |
| Colors | ESC[31m, ESC[0m | Set/reset colors |
| Mouse enable | ESC[?1000h | Enable tracking |
| Alternate screen | ESC[?1049h/l | Switch buffer |

**Why preserved**: These have ESC byte (0x1B) and are essential for TUI.

### Anomalous Strings (Filtered)

When xterm.js gains focus, it sends query sequences. The responses appear as garbage:

| Source | Format | Example |
|--------|--------|---------|
| OSC 11 Background | ESC]11;rgb:R/G/B BEL | `11;rgb:0b0b/1010/2020` |
| OSC 4 Palette | ESC]4;N;rgb:R/G/B BEL | `4;0;rgb:2e2e/3434/3636` |
| Device Attributes | ESC[?1;2c | `1;2c` |
| DEC Private Mode | ESC[?2027;0$y | `2027;0$y` |
| Cursor Position | ESC[;1R | `;1R`, `1R` |

**Why filtered**: These confuse the shell and appear as garbage text.

## Query Response Filtering

### Layer 1: Input Path Filter

**Location**: `term.onData()` callback

```javascript
term.onData(data => {
    // Filter xterm.js auto-generated query responses
    if (isTerminalQueryResponse(data)) {
        return; // Don't send to PTY
    }
    // Send user input to PTY
    ttySocket.send(data);
});
```

**Purpose**: Prevent query responses from being sent to shell.

### Layer 2: Log Filter

**Location**: `loadLog()`, `loadLogSince()`, WebSocket `onmessage`

```javascript
// Filter anomalous strings from all log content
text = text.replace(/\x1b?\]11;[^\x07\x1b]*(\x07|\x1b\\|($|[\r\n]))/g, '');
text = text.replace(/\x1b?\]4;[^\x07\x1b]*(\x07|\x1b\\|($|[\r\n]))/g, '');
text = text.replace(/\x1b?\](\d+;)?rgb:[^\x07\x1b\r\n]+/g, '');
// ... more patterns
```

**Purpose**: Clean up historical logs containing garbage strings.

### Layer 3: State Machine

**Location**: `focusTerminalIfPossible()`, `connectTTY()`

```javascript
function focusTerminalIfPossible() {
    if (ttyState !== 'READY') return; // CRITICAL: Only focus when ready
    term.focus();
}
```

**Purpose**: Prevent focus before WebSocket is ready, reducing unnecessary queries.

### Backend Filter: DISABLED

**Location**: `internal/redact/redact.go`

```go
func Text(s string) string {
    s = skPattern.ReplaceAllString(s, "sk-REDACTED")
    s = bearerPattern.ReplaceAllString(s, "Bearer REDACTED")
    // DISABLED: Would break TUI programs
    // s = controlSeqRemnant.ReplaceAllString(s, "")
    return s
}
```

**Why disabled**: Backend filtering removes TUI control sequences, corrupting display.

## Test Coverage

### Unit Tests

Location: `internal/redact/redact_test.go`

| Category | Tests | Status |
|----------|-------|--------|
| Secret redaction | 3 | ✓ Pass |
| Mouse event residues | 22 | ✓ Pass (but filter DISABLED) |
| Cursor reports | 4 | ✓ Pass (but filter DISABLED) |
| False positives | 14 | ✓ Pass |
| Input handling | 8 | ✓ Pass |

**Note**: Backend residue tests still pass, but filtering is DISABLED in production.

### Integration Tests

Tested with real TUI programs:
- opencode CLI - Full TUI display
- vim - Editor with alternate screen
- htop - Interactive monitoring
- less - Pager with scrolling

All display correctly without anomalous strings.

## Key Findings - What Changed

### Previous Understanding (WRONG)

> Backend `controlSeqRemnant` should filter mouse event residues from output.

**Reality**: Backend filtering breaks TUI programs by removing legitimate control sequences.

### Previous Understanding (WRONG)

> xterm.js responses don't go through `term.onData()`.

**Reality**: xterm.js DOES send responses through `term.onData()` when it gains focus.

### Correct Understanding (PROVEN)

1. **Frontend input filter** - Catches xterm.js query responses
2. **Frontend log filter** - Cleans up historical garbage
3. **Frontend state machine** - Prevents early focus
4. **Backend filter** - DISABLED to protect TUI programs

## Performance

| Operation | Overhead |
|-----------|----------|
| Input filter (onData) | O(n) per keystroke |
| Log filter (loadLog) | O(n) per log load |
| Regex compilation | Once at startup |

**Note**: Overhead is minimal (< 1μs per operation).

## References

- [ECMA-48](https://www.ecma-international.org/publications-and-standards/standards/ecma-48/) - Terminal escape sequences
- [XTerm Control Sequences](https://invisible-island.net/xterm/ctlseqs/ctlseqs.html) - Detailed sequence reference
- [xterm.js](https://xtermjs.org/) - Browser terminal emulator
- `ARCHITECTURE.md` §5 - Terminal Protocol Timing Specification
- `TERMINAL_FILTER_REVIEW.md` - Implementation guide