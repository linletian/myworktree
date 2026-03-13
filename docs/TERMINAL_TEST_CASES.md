# Terminal I/O Test Cases

This document lists all test cases for terminal input/output handling in myworktree.

## Overview

Test coverage for terminal I/O includes:
- Input character handling (normal, control, special keys)
- Output filtering (mouse event residues)
- False positive prevention
- Edge cases and boundary conditions

## Test Execution

```bash
# Run all redact tests
go test ./internal/redact -v

# Run all instance tests
go test ./internal/instance -v

# Run full test suite
go test ./... -v
```

## Test Case Categories

### 1. Mouse Event Residues (Should Filter)

These test cases verify that incomplete mouse event sequences are filtered from output.

#### 1.1 SGR Mouse Events (With Angle Bracket)

| ID | Test Name | Input | Expected | Status |
|----|-----------|-------|----------|--------|
| M1 | mouse event with angle bracket | `normal text[<35;55;34Mmore text` | `normal textmore text` | ✓ |
| M2 | SGR left click | `[<0;107;35M` | `` | ✓ |
| M3 | SGR right click | `[<2;50;20M` | `` | ✓ |
| M4 | SGR scroll | `[<64;80;24M` | `` | ✓ |
| M5 | SGR release (lowercase) | `[<0;100;50m` | `` | ✓ |
| M6 | SGR mouse with single digit button | `[<0;100;50M` | `` | ✓ |

**Rationale**: SGR (Select Graphic Rendition) mouse mode (1006) uses `<button;col;row` format.

#### 1.2 Mouse Events Without Angle Bracket

| ID | Test Name | Input | Expected | Status |
|----|-----------|-------|----------|--------|
| M7 | mouse event without angle bracket | `text[535;52;38Mend` | `textend` | ✓ |
| M8 | Without prefix button=0 | `0;107;35M` | `` | ✓ |
| M9 | Without prefix button=1 | `35;107;1M` | `` | ✓ |
| M10 | No brackets | `35;107;1M` | `` | ✓ |

**Rationale**: Sometimes the `[` prefix is also stripped, leaving bare `button;col;row` format.

#### 1.3 Multiple Mouse Events

| ID | Test Name | Input | Expected | Status |
|----|-----------|-------|----------|--------|
| M11 | multiple sequences | `[<35;55;34M[<35;55;33M[<35;54;32M` | `` | ✓ |
| M12 | multiple mouse events without brackets | `35;107;1M35;106;2M35;105;3M` | `` | ✓ |
| M13 | scroll wheel events | `64;80;24M65;80;24M` | `` | ✓ |
| M14 | User reported issue | `35;107;1M35;106;2M35;105;3M35;103;4M` | `` | ✓ |

**Rationale**: Mouse events often occur in rapid succession, especially during scrolling.

#### 1.4 Mouse Events in Mixed Content

| ID | Test Name | Input | Expected | Status |
|----|-----------|-------|----------|--------|
| M15 | mixed content | `Output: [<35;55;34MHello[535;52;38M World` | `Output: Hello World` | ✓ |
| M16 | mixed with text | `Error: 35;107;1M occurred` | `Error:  occurred` | ✓ |
| M17 | Log with mouse events | `[INFO] Processing...35;107;1MDone` | `[INFO] Processing...Done` | ✓ |

**Rationale**: Mouse events can appear anywhere in log output, not just in isolation.

#### 1.5 Edge Cases

| ID | Test Name | Input | Expected | Status |
|----|-----------|-------|----------|--------|
| M18 | Very large coords | `[<0;999;999M` | `` | ✓ |
| M19 | Zero button | `0;100;50M` | `` | ✓ |
| M20 | Big first and last | `[<100;5;100M` | `` | ✓ |
| M21 | Small terminal col=5 | `[<0;5;100M` | `` | ✓ |
| M22 | Small terminal row=5 | `[<0;100;5M` | `` | ✓ |

**Rationale**: Test boundary conditions for coordinate values.

### 2. Cursor Position Reports (Should Filter)

These test cases verify that cursor position report residues are filtered.

| ID | Test Name | Input | Expected | Status |
|----|-----------|-------|----------|--------|
| C1 | cursor position report | `before[<35;55;34Rafter` | `beforeafter` | ✓ |
| C2 | cursor position report #2 | `[35;107R` | `` | ✓ |
| C3 | cursor report without bracket | `35;107R` | `` | ✓ |
| C4 | cursor big;big | `100;200R` | `` | ✓ |

**Rationale**: Cursor position reports (DSR - Device Status Report) use similar format but with `R` terminator.

### 3. False Positives (Should Preserve)

These test cases verify that legitimate text is NOT filtered.

#### 3.1 Array Notation

| ID | Test Name | Input | Expected | Status |
|----|-----------|-------|----------|--------|
| F1 | normal brackets preserved | `array[0] and array[1]` | `array[0] and array[1]` | ✓ |
| F2 | Array [1;2;3] | `[1;2;3]` | `[1;2;3]` | ✓ |
| F3 | Array [0] | `[0]` | `[0]` | ✓ |
| F4 | Code arr[0] | `arr[0]` | `arr[0]` | ✓ |
| F5 | Code arr[0] = arr[1] | `arr[0] = arr[1]` | `arr[0] = arr[1]` | ✓ |

**Rationale**: Array notation is common in code and logs, must be preserved.

#### 3.2 JSON and Data Structures

| ID | Test Name | Input | Expected | Status |
|----|-----------|-------|----------|--------|
| F6 | JSON array | `{"pos": [100, 200]}` | `{"pos": [100, 200]}` | ✓ |
| F7 | JSON output | `{"data": [100, 200]}` | `{"data": [100, 200]}` | ✓ |

**Rationale**: JSON arrays must not be corrupted by filtering.

#### 3.3 CSS and Styling

| ID | Test Name | Input | Expected | Status |
|----|-----------|-------|----------|--------|
| F8 | CSS RGB notation | `background: rgb(255, 0, 0)` | `background: rgb(255, 0, 0)` | ✓ |
| F9 | CSS color (no terminator) | `color: [255;0;0]` | `color: [255;0;0]` | ✓ |

**Rationale**: CSS values in logs should be preserved.

#### 3.4 Dates and Numbers

| ID | Test Name | Input | Expected | Status |
|----|-----------|-------|----------|--------|
| F10 | Date [2024;1;15] | `[2024;1;15]` | `[2024;1;15]` | ✓ |
| F11 | Error message with coords | `Error at position [10, 20]` | `Error at position [10, 20]` | ✓ |

**Rationale**: Date formats and error messages should not be filtered.

#### 3.5 Ambiguous Small Coordinates

| ID | Test Name | Input | Expected | Status |
|----|-----------|-------|----------|--------|
| F12 | All small [0;1;2M] | `[0;1;2M` | `[0;1;2M` | ✓ |
| F13 | Small 2-param [5;9R] | `[5;9R` | `[5;9R` | ✓ |
| F14 | small ambiguous coords (preserve) | `pos: [0;5;9M` | `pos: [0;5;9M` | ✓ |

**Rationale**: When all parameters are < 10, the pattern is too ambiguous to filter safely.

### 4. Legitimate Escape Sequences (Should Preserve)

These test cases verify that real terminal escape sequences are NOT filtered.

**Note**: These are tested manually because they contain the ESC byte (0x1B).

| ID | Test Name | Sequence | Description | Status |
|----|-----------|----------|-------------|--------|
| L1 | Cursor up | `\x1b[A` | Move cursor up | ✓ |
| L2 | Cursor down | `\x1b[B` | Move cursor down | ✓ |
| L3 | Cursor right | `\x1b[C` | Move cursor right | ✓ |
| L4 | Cursor left | `\x1b[D` | Move cursor left | ✓ |
| L5 | Cursor position | `\x1b[10;20H` | Set cursor to row 10, col 20 | ✓ |
| L6 | Erase display | `\x1b[2J` | Clear entire screen | ✓ |
| L7 | Erase line | `\x1b[K` | Clear to end of line | ✓ |
| L8 | Set color | `\x1b[31m` | Set foreground to red | ✓ |
| L9 | Reset attributes | `\x1b[0m` | Reset all attributes | ✓ |
| L10 | Bold | `\x1b[1m` | Set bold | ✓ |
| L11 | F1 key | `\x1bOP` | F1 function key | ✓ |
| L12 | Home key | `\x1b[H` | Home key | ✓ |
| L13 | Enable mouse | `\x1b[?1000h` | Enable mouse tracking | ✓ |
| L14 | Disable mouse | `\x1b[?1000l` | Disable mouse tracking | ✓ |
| L15 | Set title | `\x1b]0;Title\x07` | Set window title | ✓ |

**Rationale**: All legitimate sequences have the ESC byte (0x1B), which our regex doesn't match.

### 5. Normal Text (Should Preserve)

| ID | Test Name | Input | Expected | Status |
|----|-----------|-------|----------|--------|
| N1 | Normal text | `Hello World` | `Hello World` | ✓ |
| N2 | Unicode | `你好世界` | `你好世界` | ✓ |
| N3 | Newlines | `line1\nline2\nline3` | `line1\nline2\nline3` | ✓ |
| N4 | Tabs | `col1\tcol2\tcol3` | `col1\tcol2\tcol3` | ✓ |

### 6. Secret Redaction (Should Filter)

These test cases verify that secrets are redacted from logs.

| ID | Test Name | Input | Expected | Status |
|----|-----------|-------|----------|--------|
| S1 | API key (sk-) | `sk-abcdefghijklmnopqrstuvwxyz` | `sk-REDACTED` | ✓ |
| S2 | Bearer token | `Bearer abcdefghijklmnopqrstuvwxyz` | `Bearer REDACTED` | ✓ |
| S3 | Mixed secrets | `key: sk-abc... token: Bearer xyz...` | `key: sk-REDACTED token: Bearer REDACTED...` | ✓ |

**Code**: `internal/redact/redact.go`

### 7. Environment Variable Sanitization

| ID | Test Name | Key | Value | Expected | Status |
|----|-----------|-----|-------|----------|--------|
| E1 | API token | `api_token` | `secret123` | `***` | ✓ |
| E2 | Password | `db_password` | `pass456` | `***` | ✓ |
| E3 | Secret key | `secret_key` | `key789` | `***` | ✓ |
| E4 | Normal var | `path` | `/usr/bin` | `/usr/bin` | ✓ |

**Code**: `internal/redact/redact.go:EnvKey()`

## Input Handling Test Cases

### 8. Control Character Input

These test cases verify control character handling in `SendInput()`.

| ID | Test Name | Input | Expected Behavior | Status |
|----|-----------|-------|-------------------|--------|
| I1 | Ctrl+C | `\x03` | Send SIGINT + write to stdin | ✓ |
| I2 | Ctrl+Z | `\x1A` | Send SIGTSTP + write to stdin | ✓ |
| I3 | Ctrl+\ | `\x1C` | Send SIGQUIT + write to stdin | ✓ |
| I4 | Normal char | `a` | Write to stdin | ✓ |
| I5 | String | `hello` | Write to stdin | ✓ |

**Code**: `internal/instance/manager.go:327-364`

### 9. Special Key Input

These are passed through as-is (not modified by myworktree).

| ID | Test Name | Sequence | Status |
|----|-----------|----------|--------|
| I6 | Arrow keys | `\x1b[A`, `\x1b[B`, etc. | ✓ |
| I7 | Function keys | `\x1bOP`, `\x1b[15~`, etc. | ✓ |
| I8 | Modified keys | `\x1b[1;5A` (Ctrl+Arrow) | ✓ |

## Test Statistics

| Category | Test Cases | Pass Rate |
|----------|-----------|-----------|
| Mouse events | 22 | 100% |
| Cursor reports | 4 | 100% |
| False positives | 14 | 100% |
| Legitimate sequences | 15 | 100% |
| Normal text | 4 | 100% |
| Secret redaction | 3 | 100% |
| Environment vars | 4 | 100% |
| Input handling | 8 | 100% |
| **Total** | **74** | **100%** |

## Adding New Test Cases

When adding new test cases:

1. Identify the category (mouse, cursor, false positive, etc.)
2. Add test case to appropriate section in this document
3. Add test case to `internal/redact/redact_test.go`
4. Run tests: `go test ./internal/redact -v`
5. Verify no regressions: `go test ./... -v`
6. Update statistics table

## Test Case Template

```go
{
    name: "descriptive test name",
    in:   "input string with pattern",
    want: "expected output string",
}
```

## Continuous Integration

All test cases are run in CI via `.github/workflows/go-ci.yml`:

```yaml
- name: Run tests
  run: go test ./... -v
```

## Manual Testing

For manual testing with real TUI programs:

```bash
# Start myworktree server
go run ./cmd/myworktree -listen 127.0.0.1:8080

# Open web UI
open http://localhost:8080

# Create worktree and instance
# Run TUI programs like: top, htop, vim, less
# Verify no mouse event garbage appears in terminal
```

## Performance Testing

Filtering performance is O(n) where n = log line length:

```bash
# Benchmark filtering
go test ./internal/redact -bench=. -benchmem
```

Expected: < 1μs per log line for typical terminal output.
