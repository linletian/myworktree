#!/usr/bin/env bash
# run-netem.sh — TASK-D netem acceptance runner (issue #114 §五).
# Covers AC1a (plaintext overflow -> drop-oldest + notice generation), AC2
# (windowed catch-up), AC3a (compress-vs-plaintext contrast), AC3b (e2e
# catch-up timing + per-frame cursor), AC3c (storm-band consumer-keeps-up),
# AC5 (N=5 independent consumers + RSS), AC7 (write-deadline bounded reclaim).
#
# ARBITER-MANDATED ISOLATION: netem lives on `lo` INSIDE a private network
# namespace (`mwacc`); the HOST qdisc is never touched. Daemon and every
# harness run live inside the namespace via `ip netns exec`. Teardown =
# `ip netns del mwacc`. Host `tc qdisc show dev lo` snapshotted before/after
# must be byte-identical (`qdisc noqueue 0: root refcnt 2` here).
#
# netem does NOT accept `buffer` (that is tbf/sfq); backlog is `limit` in
# packets — the arbiter's verbatim `buffer 3200` is dropped accordingly.
#
# G1 (round4): ONE daemon process serves the WHOLE run — quiet RSS baseline,
# AC5 load RSS, AC3a compress contrast, AC7 env=5000 — so the two RSS groups
# (quiet vs AC5) come from the SAME pid and are subtractable. AC7 no longer
# restarts the daemon; env=5000 is set once at (re)start. The daemon CWD MUST
# be inside a git repo: R4-B3 confirmed the round3 startup "not a git
# repository" was git-root resolution from CWD (NOT the WRITE_TIMEOUT env,
# which is read only per-connection; env present/absent 3/3 fine — review B).
# All artifacts land in a per-round timestamped dir $OUT-<ts>/ (Q-C).
#
# H2 (round5) script hardening: AC5's 5 instances each produce ~1.2MB/s, so
# they MUST be DELETEd after the AC5 step — otherwise later steps dial-timeout
# under the residual 6MB/s load (review B aborts 1/2 reruns without this). Also:
# `wait` is fault-tolerant (|| true), each JSON-producing step retries once on
# failure (工单纪律) and stamps STEP-FAILED if it still fails (evidence kept,
# run continues), DPID is asserted unique by BIN name, and `ip netns list` is
# teed into $OUT/netns-list.txt.
set -Eeo pipefail

HARNESS=${HARNESS:-/tmp/acc-harness}
BIN=${BIN:-/tmp/mw-acc}
HOME_DIR=${HOME_DIR:-/tmp/mw-acc-home}
NS=mwacc
TS=$(date +%Y%m%d-%H%M%S)
OUT=${OUT:-/tmp/acc-netem-$TS}
REPO=${REPO:-$HOME_DIR/repo}          # throwaway git repo = daemon git-root (CWD)
mkdir -p "$OUT" "$REPO"

fail() { echo "FATAL: $*" >&2; exit 1; }
need() { [ -n "${1:-}" ] || fail "$2 is empty"; }

cleanup() {
  local rc=$?
  echo "=== TEARDOWN (rc=$rc) ==="
  for p in $(pgrep -x "$(basename "$BIN")" 2>/dev/null); do kill "$p" 2>/dev/null || true; done
  sleep 1
  ip netns del "$NS" 2>/dev/null || true
  echo "host qdisc AFTER:"; tc qdisc show dev lo | tee "$OUT/host-after.txt"
  echo "netns list:"; ip netns list | tee "$OUT/netns-list.txt"
  exit $rc
}
trap cleanup EXIT INT TERM

echo "=== HOST qdisc BEFORE ==="; tc qdisc show dev lo | tee "$OUT/host-before.txt"

echo "=== namespace + netem (host untouched) ==="
ip netns add "$NS"
ip netns exec "$NS" ip link set lo up
ip netns exec "$NS" tc qdisc add dev lo root netem \
   delay 300ms 50ms distribution normal loss 2% rate 800kbit limit 1000
echo "netns qdisc:"; ip netns exec "$NS" tc qdisc show dev lo | tee "$OUT/netns-qdisc.txt"

# Throwaway git repo so the daemon's git-root resolution succeeds (R4-B3).
git -C "$REPO" rev-parse --is-inside-work-tree >/dev/null 2>&1 || git init -q "$REPO"

echo "=== SINGLE daemon (env=5000 whole-run, CWD=$REPO, stdin held open) ==="
cd "$REPO"
# `tail -f /dev/null |` keeps the daemon's stdin open so its 'o'-reader never
# EOF-exits; the whole pipeline is a child of this script -> killed by cleanup.
tail -f /dev/null | ip netns exec "$NS" env HOME="$HOME_DIR" MYWORKTREE_TTY_WRITE_TIMEOUT_MS=5000 \
  "$BIN" -listen=127.0.0.1:0 -open=false -portal-port=0 >"$OUT/daemon.out" 2>"$OUT/daemon.log" &
sleep 4
PORT=$(grep -oE '127.0.0.1:[0-9]+' "$OUT/daemon.out" | head -1 | cut -d: -f2); need "$PORT" "PORT"
DPID_LIST=$(pgrep -x "$(basename "$BIN")" 2>/dev/null); DPID=$(printf '%s' "$DPID_LIST" | head -1)
DPID_N=$(printf '%s' "$DPID_LIST" | grep -c .); need "$DPID" "daemon pid"
[ "$DPID_N" -eq 1 ] || fail "expected exactly 1 $(basename "$BIN") daemon, found $DPID_N (RSS attribution would be ambiguous)"
echo "PORT=$PORT DPID=$DPID  (unique $(basename "$BIN") pid for ALL RSS samples)"
echo "daemon.out:"; sed -n '1,6p' "$OUT/daemon.out"

mkinst() { # $1=name $2=command ; echoes instance id
  local r id
  r=$(ip netns exec "$NS" curl -s -m10 -X POST "http://127.0.0.1:$PORT/api/instances" \
      -H 'Content-Type: application/json' \
      -d "{\"worktree_id\":\"__main__\",\"kind\":\"pty\",\"name\":\"$1\",\"command\":\"$2\"}") || fail "create $1 failed"
  id=$(printf '%s' "$r" | python3 -c "import sys,json;print(json.load(sys.stdin).get('id',''))") || fail "parse $1 resp"
  need "$id" "instance id for $1"
  printf '%s' "$id"
}
run() { ip netns exec "$NS" "$HARNESS" "$@"; }
# sample VmRSS (raw /proc line) for the single daemon pid — same DPID everywhere.
rss() { grep VmRSS /proc/$DPID/status 2>/dev/null; }
# best-effort instance delete: frees the producer so residual flood load does
# not make later steps dial-timeout (H2). Never aborts.
delinst() { # $1=id
  [ -n "${1:-}" ] || return 0
  ip netns exec "$NS" curl -s -m10 -X POST "http://127.0.0.1:$PORT/api/instances/delete" \
     -H 'Content-Type: application/json' -d "{\"id\":\"$1\"}" >/dev/null 2>&1 || true
}
# run a harness step to $1 (json), retry once on failure (工单纪律), stamp
# STEP-FAILED on final failure, ALWAYS return 0 so one flaky step never aborts
# the whole round (evidence for the other steps is still collected).
run_recover() { # $1=out.json, rest=harness args
  local out="$1"; shift
  if run "$@" >"$out" 2>&1; then return 0; fi
  echo "WARN: $(basename "$out") attempt=1 failed — retrying once (工单纪律)" >&2
  sleep 1
  if run "$@" >"$out" 2>&1; then echo "# STEP-OK after retry" >>"$out"; return 0; fi
  echo "# STEP-FAILED (after 1 retry) — partial evidence retained, round continues" >>"$out"
  return 0
}

echo "=== RSS quiet resident FIRST (minimal-output instance, 5 concurrent compress conns; same pid) ==="
QUIET=$(mkinst acc-quiet "sleep 3600") || QUIET=""
if [ -n "$QUIET" ]; then
  sleep 1
  QB=$(rss); echo "quiet BASE $(printf '%s' "$QB")" | tee "$OUT/rss-quiet.txt"
  CP=""
  for k in 1 2 3 4 5; do run -daemon 127.0.0.1:$PORT -id "$QUIET" -caps compress -mode normal -read-deadline-ms 30000 -run-ms 8000 >/dev/null 2>&1 & CP="$CP $!"; done
  QPEAK=0
  for s in $(seq 1 8); do sleep 1; L=$(rss); R=$(printf '%s' "$L"|grep -oE '[0-9]+'); [ -n "$R" ]&&[ "$R" -gt "$QPEAK" ]&&QPEAK=$R; printf 't=%ss %s\n' "$s" "$L" >> "$OUT/rss-quiet.txt"; done
  wait $CP || true   # client jobs only — NOT the daemon pipeline; tolerate a flaky client (H2)
  echo "quiet PEAK=${QPEAK}kB  (BASE-$(printf '%s' "$QB"|grep -oE '[0-9]+') => conn/queue component)" >> "$OUT/rss-quiet.txt"
  echo "quiet recorded to $OUT/rss-quiet.txt"
else
  echo "WARN: quiet instance create flaked; skipping quiet RSS (others continue)" | tee "$OUT/rss-quiet.txt"
fi

echo "=== AC5: N=5 independent instances, each 1 slow consumer (same pid RSS, subtractable vs quiet) ==="
AC5_IDS=""
for k in 1 2 3 4 5; do
  ID=$(mkinst "acc-x$k" "while :; do printf 'X%.0s' {1..60000}; sleep 0.05; done"); need "$ID" "ac5 inst $k id"
  AC5_IDS="$AC5_IDS $ID"
done
echo "AC5 ids:$AC5_IDS"
A5B=$(rss); echo "ac5 BASE $(printf '%s' "$A5B")" > "$OUT/ac5-rss.txt"
n=1; AC5P=""
for ID in $AC5_IDS; do run -daemon 127.0.0.1:$PORT -id "$ID" -caps overflow-notice -mode slow -read-delay-ms 400 -read-deadline-ms 30000 -run-ms 20000 >"$OUT/ac5-$n.json" 2>&1 & AC5P="$AC5P $!"; n=$((n+1)); done
PEAK=0
for s in $(seq 1 12); do sleep 2; L=$(rss); R=$(printf '%s' "$L"|grep -oE '[0-9]+'); [ -n "$R" ] && [ "$R" -gt "$PEAK" ] && PEAK=$R; printf 't=%ss %s\n' "$((s*2))" "$L" >> "$OUT/ac5-rss.txt"; done
wait $AC5P || true   # client jobs only — NOT the daemon pipeline; tolerate flaky clients (H2)
echo "ac5 PEAK=${PEAK}kB" >> "$OUT/ac5-rss.txt"
for k in 1 2 3 4 5; do grep -oE '"binaryFrames":[0-9]+|"binaryWireBytes":[0-9]+|"closeCode":[0-9]+' "$OUT/ac5-$k.json" 2>/dev/null | tr '\n' ' '; echo "<- c$k"; done > "$OUT/ac5.log"
grep "subscriber overflow" "$OUT/daemon.log" 2>/dev/null | grep -oE "for [a-f0-9]+" | sort | uniq -c >> "$OUT/ac5.log" || true
ALT=$(printf '%s' "$AC5_IDS" | tr ' ' '\n' | grep . | paste -sd'|'); grep -E "handshake id=($ALT) " "$OUT/daemon.log" > "$OUT/ac5-handshake.log" || true   # G2e/A-T2 handshake x5 sourced from $OUT
for ID in $AC5_IDS; do grep "conn-closed id=$ID" "$OUT/daemon.log" | tail -1 >> "$OUT/ac5.log"; done
# H2: DELETE AC5 instances now — each still produces ~1.2MB/s; leaving them up
# makes every later step dial-timeout under the residual ~6MB/s load (review B).
for ID in $AC5_IDS; do delinst "$ID"; done; delinst "$QUIET"
echo "AC5 instances deleted (release 5x1.2MB/s producer load before later steps)"

echo "=== AC3a: plaintext overflow (expect drop-oldest) vs compress (expect no overflow) ==="
FLOOD=$(mkinst acc-flood "while :; do printf 'FL%.0s' {1..100000}; sleep 0.05; done"); sleep 1
run_recover "$OUT/ac3a-plaintext.json" -daemon 127.0.0.1:$PORT -id "$FLOOD" -caps overflow-notice -mode normal -read-deadline-ms 30000 -run-ms 20000
run_recover "$OUT/ac3a-compress.json" -daemon 127.0.0.1:$PORT -id "$FLOOD" -caps compress,overflow-notice -mode normal -read-deadline-ms 30000 -run-ms 12000
grep -E "subscriber overflow|drop-oldest|1013|overflow id=" "$OUT/daemon.log" | tail -4 | tee "$OUT/ac3a.log" || true

echo "=== AC1a: plaintext overflow -> drop-oldest, connection kept, no 1013 (explicit -read-deadline-ms) ==="
run_recover "$OUT/ac1a.json" -daemon 127.0.0.1:$PORT -id "$FLOOD" -caps overflow-notice -mode normal -read-deadline-ms 30000 -run-ms 12000
grep -E "subscriber overflow|drop-oldest|1013" "$OUT/daemon.log" | tail -4 | tee "$OUT/ac1a.log" || true

echo "=== AC2/AC3b: windowed catch-up (since=0, sync) -> e2eCatchupMs + continuityOK ==="
run_recover "$OUT/ac2_ac3b.json" -daemon 127.0.0.1:$PORT -id "$FLOOD" -caps compress,sync -since 0 -mode normal -read-deadline-ms 30000 -run-ms 25000
grep "handshake" "$OUT/daemon.log" | tail -1 | tee "$OUT/ac2.log" || true

echo "=== AC3c: storm-rate band, consumer keeps up -> expect maxWriteBlockMs=0 (no false-kill) ==="
STORM=$(mkinst acc-storm "while :; do printf 'ST%.0s' {1..4000}; sleep 0.1; done"); sleep 1
run_recover "$OUT/ac3c.json" -daemon 127.0.0.1:$PORT -id "$STORM" -caps compress,overflow-notice -mode normal -read-deadline-ms 30000 -run-ms 12000
sleep 1
grep -E "conn-closed id=$STORM" "$OUT/daemon.log" | tail -1 | tee "$OUT/ac3c.log" || true

echo "=== AC7: write-deadline bounded reclaim (same daemon, env=5000 set at start) ==="
AC7ID=$(mkinst acc-ac7 "while :; do printf 'A7%.0s' {1..100000}; sleep 0.05; done"); sleep 1
LOG_MARK=$(wc -l < "$OUT/daemon.log")
run_recover "$OUT/ac7.json" -daemon 127.0.0.1:$PORT -id "$AC7ID" -caps overflow-notice -mode stop -warmup-ms 500 -stop-block-ms 9000 -run-ms 14000
tail -n +$((LOG_MARK+1)) "$OUT/daemon.log" | grep -E "slow-write|conn-closed" | tee "$OUT/ac7.log" || true

echo "=== artifacts in $OUT ==="; ls -la "$OUT"
echo "=== DONE (teardown via trap; OUT=$OUT) ==="
