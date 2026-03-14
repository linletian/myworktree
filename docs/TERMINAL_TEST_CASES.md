# Terminal Test Cases

This document lists test cases for terminal input/output handling in myworktree.

## Overview

Test coverage includes:
- Input character handling
- Query response filtering
- Secret redaction
- False positive prevention

For architecture and analysis, see `TERMINAL_IO_ANALYSIS.md`.

## Test Execution

```bash
# Run all redact tests
go test ./internal/redact -v

# Run all instance tests
go test ./internal/instance -v

# Run full test suite
go test ./... -v
```

## Test Categories

### 1. Secret Redaction (Active)

These verify that secrets are redacted from logs.

| ID | Test Name | Input | Expected |
|----|-----------|-------|----------|
| S1 | API key (sk-) | `sk-abcdefghijklmnopqrstuvwxyz` | `sk-REDACTED` |
| S2 | Bearer token | `Bearer abcdefghijklmnopqrstuvwxyz` | `Bearer REDACTED` |
| S3 | Mixed secrets | `key: sk-abc... token: Bearer xyz...` | `key: sk-REDACTED token: Bearer REDACTED...` |

**Code**: `internal/redact/redact.go`

### 2. Mouse Event Residues (DISABLED)

**Note**: Backend filtering is DISABLED to protect TUI programs. Tests remain for documentation.

| ID | Test Name | Input | Expected (if enabled) |
|----|-----------|-------|------------------------|
| M1 | SGR left click | `[<35;55;34M` | `` |
| M2 | SGR scroll | `[<64;80;24M` | `` |
| M3 | Without brackets | `35;107;1M` | `` |
| M4 | Cursor report | `35;107R` | `` |

**Status**: Backend filter DISABLED. Frontend filters query responses instead.

### 3. False Positives (Preserved)

These verify that legitimate text is NOT filtered.

| ID | Test Name | Input | Expected |
|----|-----------|-------|----------|
| F1 | Array notation | `[1;2;3]` | `[1;2;3]` |
| F2 | Array index | `arr[0]` | `arr[0]` |
| F3 | JSON array | `{"pos": [100, 200]}` | `{"pos": [100, 200]}` |
| F4 | Small coords | `[0;5;9M` | `[0;5;9M` |

**Rationale**: Array notation, JSON, and small coordinates should be preserved.

### 4. Legitimate Escape Sequences (Preserved)

All legitimate escape sequences are preserved in output.

| ID | Sequence | Description |
|----|----------|-------------|
| L1 | `\x1b[A` | Cursor up |
| L2 | `\x1b[B` | Cursor down |
| L3 | `\x1b[31m` | Red text |
| L4 | `\x1b[?1000h` | Enable mouse |
| L5 | `\x1b[?1049h` | Alternate screen |

**Rationale**: Sequences with ESC byte (0x1B) are preserved.

### 5. Control Character Input

| ID | Test Name | Input | Behavior |
|----|-----------|-------|----------|
| I1 | Ctrl+C | `\x03` | Send SIGINT + write to stdin |
| I2 | Ctrl+Z | `\x1A` | Send SIGTSTP + write to stdin |
| I3 | Ctrl+\ | `\x1C` | Send SIGQUIT + write to stdin |
| I4 | Normal char | `a` | Write to stdin |

**Code**: `internal/instance/manager.go:SendInput()`

### 6. Query Response Filtering (Frontend)

These test the frontend `isTerminalQueryResponse()` function.

| ID | Test Name | Input | Expected |
|----|-----------|-------|----------|
| Q1 | OSC 11 color response | `11;rgb:0b0b/1010/2020` | Filtered |
| Q2 | Device Attributes | `1;2c` | Filtered |
| Q3 | Cursor Position | `;1R` | Filtered |
| Q4 | DEC Private Mode | `2027;0$y` | Filtered |
| Q5 | OSC with ESC | `\x1b]11;rgb:...` | Filtered |
| Q6 | Normal text | `hello world` | Preserved |
| Q7 | User ESC sequence | `\x1b[c` | Preserved |

**Code**: `internal/ui/static/index.html:isTerminalQueryResponse()`

### 7. ESC Key Handling (Frontend)

These verify that ESC key and common ESC sequences pass through correctly.

| ID | Test Name | Input | Length | Expected |
|----|-----------|-------|--------|----------|
| E1 | Single ESC | `\x1b` | 1 | Preserved (length < MIN_RESPONSE_LENGTH) |
| E2 | ESC + bracket | `\x1b[` | 2 | Preserved (not a response pattern) |
| E3 | Arrow up | `\x1b[A` | 3 | Preserved (not `\[\d+;\d+R$` pattern) |
| E4 | Arrow in app mode | `\x1bOA` | 3 | Preserved (not OSC response) |
| E5 | Page Up | `\x1b[5~` | 4 | Preserved (not a response pattern) |
| E6 | F1 key | `\x1bOP` | 3 | Preserved (not `\x1b]` or `\x1bP` OSC) |

**Rationale**: User keyboard input including ESC sequences must pass through. Only xterm.js auto-generated responses (OSC color, Device Attributes, cursor position) are filtered. The MIN_RESPONSE_LENGTH=2 guard ensures single ESC passes.

**Code**: `internal/ui/static/index.html:isTerminalQueryResponse()`

### 8. Instance Switch Terminal Reset

These verify terminal state is properly reset when switching instances.

| ID | Test Name | Scenario | Expected Behavior |
|----|-----------|----------|-------------------|
| R1 | Mouse mode reset | Instance A has mouse tracking, switch to B | Mousemodesdisabled before switch |
| R2 | Alternate buffer reset | Instance A in alternate buffer, switch to B | Exit and re-enter alternate buffer |
| R3 | Stopped instance switch | Instance A stopped, switch to B | Full terminal reset |
| R4 | Running instance switch | Instance A running TUI, switch to running B | Buffer cleared, modes reset |
| R5 | Mode re-initialization | Switch to instance with TUI | TUI receives SIGWINCH, re-enables modes |

**Sequences sent on instance switch:**
```
\x1b[?1000l  # Disable mouse button press/release
\x1b[?1002l  # Disable mouse drag
\x1b[?1003l  # Disable mouse all motion
\x1b[?1006l  # Disable SGR extended mouse mode
```

**Code**: `internal/ui/static/index.html:selectInstance()`

### 9. UTF-8 Multi-byte Handling (Frontend)

These verify that UTF-8 multi-byte characters are correctly decoded across WebSocket frames.

| ID | Test Name | Input | Expected |
|----|-----------|-------|----------|
| U1 | Chinese split across frames | Frame1: `\xe4\xb8`, Frame2: `\xad` | `中` (U+4E2D) correctly decoded |
| U2 | Emoji split across frames | Frame1: `\xf0\x9f`, Frame2: `\x98\x80` | `😀` correctly decoded |
| U3 | Single frame Chinese | `\xe4\xb8\xad` | `中` correctly decoded |
| U4 | Mixed ASCII and Chinese | `hello中文world` | Correctly decoded and filtered |

**Rationale**: TextDecoder with`{stream: true}` handles incomplete multi-byte sequences across frames.

**Code**: `internal/ui/static/index.html:decodeTTYOutputChunk()`

## Test Statistics

| Category | Test Cases | Status |
|----------|-----------|--------|
| Secret redaction | 3 | ✓ Pass |
| Mouse residues | 4 | ⚠️ Disabled |
| False positives | 4 | ✓ Pass |
| Legitimate sequences | 5 | ✓ Pass |
| Input handling | 4 | ✓ Pass |
| Query responses | 7 | ✓ Pass |
| ESC key handling | 6 | ✓ Pass |
| Instance switch reset | 5 | ✓ Pass |
| UTF-8 multi-byte | 4 | ✓ Pass |
| **Total** | **42** | **90% Active** |

## Adding New Test Cases

When adding new test cases:

1. Add test case to appropriate section in this document
2. For backend tests, add to `internal/redact/redact_test.go`
3. For frontend tests, add to browser console test
4. Run tests: `go test ./internal/redact -v`
5. Update statistics table

## Manual Testing

For manual testing with real TUI programs:

```bash
# Start myworktree server
go run ./cmd/myworktree -listen 127.0.0.1:8080

# Open web UI
open http://localhost:8080
```

### Basic TUI Tests

1. Create opencode instance - verify TUI displays correctly
2. Exit opencode - return to shell, no anomalous strings
3. Run vim/htop - verify display works
4. Type Chinese characters - verify UTF-8 handling

### Instance Switch Tests

1. **Mouse tracking reset**:
   - Create Instance A: run `opencode`
   - Enable mouse tracking in opencode (mouse should work)
   - Create Instance B: run `bash`
   - Switch back to Instance A, then to Instance B
   - Move mouse over terminal - no mouse event strings should appear

2. **TUI program switch**:
   - Create Instance A: run `htop` (TUI with mouse)
   - Create Instance B: run `vim` (TUI with alternate buffer)
   - Switch between A and B multiple times
   - Each TUI should display correctly without artifacts

3. **Chinese output in TUI**:
   - Create Instance A: run `opencode`
   - Have opencode output Chinese text
   - Switch to Instance B, then back to Instance A
   - Chinese characters should display correctly (no replacement characters)

4. **ESC key handling**:
   - Create Instance A: run `vim`
   - Press ESC key - should work as expected (exit insert mode)
   - Press sequences like ESC+[ (should not be filtered)

### Expected Behavior on Switch

When switching from Instance A (with TUI) to Instance B:
1. Terminal sends mouse disable sequences
2. Terminal exits/re-enters alternate buffer
3. WebSocket disconnects from A
4. WebSocket connects to B
5. Resize sent to B → TUI in B receives SIGWINCH
6. TUI in B re-initializes (re-enables mouse if needed)

## Related Documents

- `TERMINAL_IO_ANALYSIS.md` - Architecture and analysis
- `TERMINAL_FILTER_REVIEW.md` - Implementation guide
- `ARCHITECTURE.md` §5 - Terminal protocol timing