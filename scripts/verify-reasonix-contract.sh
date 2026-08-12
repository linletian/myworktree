#!/usr/bin/env bash
#
# verify-reasonix-contract.sh — verify the upstream reasonix behavior contract
# that myworktree's reasonix driver depends on (see docs/ARCHITECTURE.md §4.2
# "Upstream contract (verified on reasonix v1.22.0)").
#
# The contract, all verified against a REAL reasonix binary:
#   1. Version gate: reasonix >= 1.22.0 (the driver's defaultMinVersion).
#   2. Session layout: `serve` (no --resume) keeps sessions under
#      <stateRoot>/projects/<cwd-slug>/sessions, isolating projects by cwd.
#   3. Fresh-session uniqueness: concurrent `serve`s on the SAME cwd each get
#      their own session (nanosecond-unique path) and run independently.
#   4. Lease mutual exclusion: `--resume` of a session held by another process
#      is REFUSED with a clear message (never silently double-written).
#
# Any of these changing on a reasonix upgrade means the embedded web UI
# integration may break — run this before/after upgrading reasonix. It is also
# reachable via `go test` (opt-in: REASONIX_CONTRACT=1, see
# internal/instance/reasonix/contract_test.go) and via the CI workflow
# (.github/workflows/reasonix-contract.yml, which SKIPs when no reasonix
# binary is present on the runner).
#
# Usage:
#   scripts/verify-reasonix-contract.sh             # finds reasonix on PATH
#   REASONIX_BIN=/path/to/reasonix scripts/verify-reasonix-contract.sh
#
# Exit codes: 0 = all checks passed (or reasonix absent → SKIP, see below),
#             1 = at least one contract check FAILED (risk surfaced).
#
# When no reasonix binary is found the script SKIPs with an install hint and
# exits 0, so CI stays green on runners that do not carry a reasonix binary.
#
# All probes run against an isolated temporary REASONIX_HOME — real user
# sessions/config/credentials are never touched.

set -u

MIN_VERSION="1.22.0"
FAILED=0
PASSED=0
SKIPPED=0

pass() { printf '[PASS] %s\n' "$1"; PASSED=$((PASSED + 1)); }
fail() { printf '[FAIL] %s\n' "$1"; FAILED=$((FAILED + 1)); }
skip() { printf '[SKIP] %s\n' "$1"; SKIPPED=$((SKIPPED + 1)); }

# version_part v n — the n-th x.y.z segment, "" when the segment is absent.
version_part() { printf '%s' "$1" | cut -d. -f"$2" 2>/dev/null; }

# version_ge a b — numeric x.y.z compare treating missing segments as 0
# ("1.23" == "1.23.0"), 0 if a >= b.
version_ge() {
  local i va vb
  for i in 1 2 3; do
    va="$(version_part "$1" "$i")"; va="${va:-0}"
    vb="$(version_part "$2" "$i")"; vb="${vb:-0}"
    if [ "$va" -gt "$vb" ]; then return 0; fi
    if [ "$va" -lt "$vb" ]; then return 1; fi
  done
  return 0
}

BIN="${REASONIX_BIN:-}"
if [ -z "$BIN" ]; then
  BIN="$(command -v reasonix || true)"
fi
if [ -z "$BIN" ]; then
  echo "SKIP: reasonix not found on PATH (set REASONIX_BIN to point at a reasonix binary)."
  echo "      The upstream contract checks run only when a real reasonix binary is available."
  exit 0
fi

echo "== reasonix contract check (bin: $BIN) =="

# --- Check 1: version gate ---------------------------------------------------
VER_OUT="$("$BIN" --version 2>&1)"
VER="$(printf '%s\n' "$VER_OUT" | grep -oE '[0-9]+\.[0-9]+(\.[0-9]+)?' | head -1 || true)"
if [ -z "$VER" ]; then
  fail "cannot parse a version from \`$BIN --version\` output: $(printf '%s' "$VER_OUT" | tr '\n' ' ')"
elif version_ge "$VER" "$MIN_VERSION"; then
  pass "version $VER >= $MIN_VERSION (driver defaultMinVersion gate)"
else
  fail "version $VER < required $MIN_VERSION (driver defaultMinVersion gate)"
fi

# --- Probe setup (isolated temp home; real sessions untouched) ---------------
TMP_ROOT="$(mktemp -d)"
WT_DIR="$TMP_ROOT/wt"                 # shared cwd for all serves
HOME_DIR="$TMP_ROOT/home"             # isolated REASONIX_HOME
RUN_DIR="$TMP_ROOT/run"
mkdir -p "$WT_DIR" "$HOME_DIR" "$RUN_DIR"
SERVE_PIDS=""
LAUNCH_PID=""
PID_A=""; PID_B=""; PID_C=""
WAITED_EXIT=0

# cleanup terminates every probe serve (including any children it spawned)
# and then removes the temp root. TERM first, then KILL, then a short settle
# wait so no dying process can race the directory removal.
cleanup() {
  for p in $SERVE_PIDS; do
    pkill -TERM -P "$p" 2>/dev/null || true
    kill -TERM "$p" 2>/dev/null || true
  done
  sleep 1
  for p in $SERVE_PIDS; do
    pkill -KILL -P "$p" 2>/dev/null || true
    kill -KILL "$p" 2>/dev/null || true
  done
  sleep 1
  rm -rf "$TMP_ROOT"
}
trap cleanup EXIT

# launch_serve <label> [extra serve args...] — starts a serve on the shared cwd
# with the isolated home; the new process pid is left in $LAUNCH_PID (and added
# to $SERVE_PIDS for cleanup). NOT invoked via command substitution: the
# background job must stay a child of this shell so `wait`/`kill` work.
launch_serve() {
  local label="$1"; shift
  printf 'verify-token\n' > "$RUN_DIR/token-$label"
  chmod 600 "$RUN_DIR/token-$label"
  ( cd "$WT_DIR" || exit 1
    exec env REASONIX_HOME="$HOME_DIR" "$BIN" serve \
      --addr 127.0.0.1:0 --auth token \
      --token-file "$RUN_DIR/token-$label" \
      --port-file "$RUN_DIR/port-$label" \
      --pid-file "$RUN_DIR/pid-$label" \
      --no-open "$@"
  ) > "$RUN_DIR/serve-$label.log" 2>&1 &
  LAUNCH_PID=$!
  SERVE_PIDS="$SERVE_PIDS $LAUNCH_PID"
}

# wait_ready <label> <pid> — 0 when the port file appears (serve bound), 1 on
# timeout or early process exit.
wait_ready() {
  local label="$1" pid="$2" i
  for i in $(seq 1 60); do
    if [ -s "$RUN_DIR/port-$label" ]; then return 0; fi
    if ! kill -0 "$pid" 2>/dev/null; then return 1; fi
    sleep 0.25
  done
  return 1
}

# wait_exit <label> <pid> <seconds> — 0 when the process exits within the
# timeout (its exit code is left in $WAITED_EXIT for the caller), 1 on
# timeout. NOTE: the exit code comes from `wait`, not from this function's
# return value — reading $? at the call site would always yield this
# function's own 0.
wait_exit() {
  local label="$1" pid="$2" secs="$3" i
  for i in $(seq 1 $((secs * 4))); do
    if ! kill -0 "$pid" 2>/dev/null; then
      wait "$pid"; WAITED_EXIT=$?
      return 0
    fi
    sleep 0.25
  done
  return 1
}

sessions_dir() {
  find "$HOME_DIR/projects" -type d -name sessions 2>/dev/null | head -1
}

# --- Check 2: session layout + fresh lease on plain start --------------------
launch_serve A; PID_A="$LAUNCH_PID"
if ! wait_ready A "$PID_A"; then
  fail "serve A did not become ready within 15s (log: $(tr '\n' ' ' < "$RUN_DIR/serve-A.log"))"
else
  SESS_DIR="$(sessions_dir)"
  if [ -z "$SESS_DIR" ]; then
    fail "no <home>/projects/<slug>/sessions dir created by serve (layout contract broken)"
  else
    LEASE_A="$(find "$SESS_DIR" -name '*.jsonl.lease.json' | head -1)"
    if [ -z "$LEASE_A" ]; then
      fail "serve created no session lease under $SESS_DIR (fresh-session contract broken)"
    else
      SESSION_A="$(basename "$LEASE_A" .lease.json)"
      pass "serve (no --resume) opens a fresh per-cwd session lease: $SESS_DIR/$SESSION_A"
    fi
  fi
fi

# --- Check 3: two serves on the same cwd run independently -------------------
# Depends on Check 2 establishing SESSION_A; if it did not (contract already
# breaking), skip with a pointer instead of crashing on the unbound variable
# or running find against an empty path.
launch_serve C; PID_C="$LAUNCH_PID"
if ! wait_ready C "$PID_C"; then
  fail "serve C did not become ready within 15s (log: $(tr '\n' ' ' < "$RUN_DIR/serve-C.log"))"
elif [ -z "${SESSION_A:-}" ]; then
  skip "two-serves independence check: Check 2 did not establish a session lease (see the FAIL above)"
else
  SESS_DIR="$(sessions_dir)"
  if [ -z "$SESS_DIR" ]; then
    fail "sessions dir vanished between checks (layout contract broken)"
  else
    LEASE_C="$(find "$SESS_DIR" -name '*.jsonl.lease.json' | grep -vF "$SESSION_A" | head -1)"
    PORT_A="$(cat "$RUN_DIR/port-A")"
    PORT_C="$(cat "$RUN_DIR/port-C")"
    if [ -z "$LEASE_C" ]; then
      fail "second serve on the same cwd did not get its own session lease (uniqueness contract broken)"
    elif [ "$PORT_A" = "$PORT_C" ]; then
      fail "two serves on the same cwd bound the same port: $PORT_A"
    elif ! kill -0 "$PID_A" 2>/dev/null; then
      fail "serve A died while serve C started (they must not interfere)"
    else
      pass "two serves on the same cwd run independently (distinct leases $(basename "$SESSION_A") / $(basename "$LEASE_C" .lease.json), distinct ports)"
    fi
  fi
fi

# --- Check 4: --resume of a held session is refused --------------------------
if [ -z "${SESSION_A:-}" ]; then
  skip "lease-refusal check: no session lease to test against (see Check 2)"
else
  SESS_DIR="$(sessions_dir)"
  if [ -z "$SESS_DIR" ]; then
    fail "sessions dir unavailable for the lease-refusal check (layout contract broken)"
  else
    launch_serve B --resume "$SESS_DIR/$SESSION_A"; PID_B="$LAUNCH_PID"
    if wait_exit B "$PID_B" 10; then
      B_LOG="$(tr '\n' ' ' < "$RUN_DIR/serve-B.log")"
      # Match the refusal with several forgiving patterns: the exact wording
      # is upstream's, and a single literal pattern would false-fail when they
      # merely rephrase it. A non-zero exit alone is NOT enough — other errors
      # also exit non-zero — so an unmatched log is reported for review.
      case "$B_LOG" in
        *"in use by another"*|*"in use by "*|*"already in use"*|*"session is in use"*)
          pass "--resume of a held session is refused with a clear message (lease mutual exclusion)"
          ;;
        *)
          fail "--resume of a held session exited (code $WAITED_EXIT) without a known refusal message (upstream may have reworded it; inspect the log): $B_LOG"
          ;;
      esac
    else
      fail "--resume of a held session did NOT exit within 10s (lease mutual exclusion broken)"
    fi
  fi
fi

echo "== $PASSED passed, $FAILED failed, $SKIPPED skipped =="

if [ "$FAILED" -gt 0 ]; then
  echo "RISK: a reasonix upgrade (or change) broke the upstream contract myworktree's"
  echo "      reasonix driver relies on — see docs/ARCHITECTURE.md §4.2 and re-verify"
  echo "      the driver (go test ./internal/instance/...) before shipping."
  exit 1
fi
echo "Reasonix upstream contract verified (docs/ARCHITECTURE.md §4.2)."
