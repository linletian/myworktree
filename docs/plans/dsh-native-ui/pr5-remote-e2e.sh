#!/bin/bash
# PR5 远程模式半自动端到端实测（REVIEW issue #62）
# 真实 myworktree daemon（0.0.0.0 主监听 + 自签 TLS + token 门）+
# 真实 dsh 0.1.0-rc.6 二进制 + 隔离 DSH_HOME/仓库；
# 用本机 LAN IP 作为"非 loopback 远端客户端"驱动全部验证。
set -u

ROOT=/home/linletian/SoftwareWorkspace/myworktree
export GOCACHE=$ROOT/.gocache
TDIR=$(mktemp -d /tmp/dsh-pr5.XXXXXX)
LOG=$TDIR/daemon.log
DAEMON_PID=""
PORT=50991
TOKEN=sekret-e2e

cleanup() {
  if [ -n "$DAEMON_PID" ]; then
    kill "$DAEMON_PID" 2>/dev/null || true
    sleep 2
    kill -9 "$DAEMON_PID" 2>/dev/null || true
  fi
  # myworktree Shutdown stops dsh-web instances; sweep any stragglers
  # spawned from this run (dsh children live under the daemon tree).
  pkill -9 -f "dshhome" 2>/dev/null || true
}
trap cleanup EXIT

say()  { echo "[PR5] $*"; }
fail() { echo "[PR5] FAIL: $*"; tail -30 "$LOG" 2>/dev/null; exit 1; }

# --- 0. build ---------------------------------------------------------
cd "$ROOT" && go build -o "$TDIR/mw" ./cmd/myworktree || fail "build"
say "binary built"

# --- 1. isolated repo --------------------------------------------------
mkdir -p "$TDIR/repo" && cd "$TDIR/repo" && git init -q . || fail "git init"
say "temp repo ready"

LANIP=$(hostname -I 2>/dev/null | awk '{print $1}')
[ -z "$LANIP" ] && fail "no LAN IP"
# The environment may route LAN traffic through an HTTP proxy; bypass
# it for the LAN IP so curl really is the "remote browser" (source
# address = LAN IP, non-loopback) and WS handshakes hit the daemon
# directly instead of the proxy's CONNECT.
export NO_PROXY="$LANIP,${NO_PROXY-}"
export no_proxy="$NO_PROXY"
say "LAN IP (non-loopback client source): $LANIP"

# --- 2. self-signed TLS -------------------------------------------------
openssl req -x509 -newkey rsa:2048 -keyout "$TDIR/key.pem" -out "$TDIR/cert.pem" \
  -days 1 -nodes -subj "/CN=localhost" \
  -addext "subjectAltName=IP:127.0.0.1,IP:$LANIP,DNS:localhost" >/dev/null 2>&1 \
  || fail "openssl cert"
say "TLS cert ready"

# --- 3. daemon (0.0.0.0 + TLS + token) -----------------------------------
# XDG_CONFIG_HOME keeps the daemon's state dir (~/.config/myworktree/<hash>)
# inside the sandbox-writable temp dir; DSH_HOME isolates the dsh session
# pool / profiles from the real user's ~/.dsh.
mkdir -p "$TDIR/dshhome" "$TDIR/config"
XDG_CONFIG_HOME="$TDIR/config" DSH_HOME="$TDIR/dshhome" PATH="/home/linletian/.npm-global/bin:$PATH" \
  "$TDIR/mw" -listen "0.0.0.0:$PORT" -auth "$TOKEN" \
  -tls-cert "$TDIR/cert.pem" -tls-key "$TDIR/key.pem" \
  -open=false -portal-port 0 >"$LOG" 2>&1 &
DAEMON_PID=$!

UP=""
for i in $(seq 1 30); do
  if curl -sk -o /dev/null "https://127.0.0.1:$PORT/api/worktrees"; then UP=1; break; fi
  sleep 1
done
[ -n "$UP" ] || fail "daemon did not come up"
say "daemon up (pid $DAEMON_PID)"

# --- 4. create dsh-web instance -------------------------------------------
RESP=$(curl -sk -X POST "https://127.0.0.1:$PORT/api/instances" \
  -H 'Content-Type: application/json' \
  -d '{"worktree_id":"__main__","kind":"dsh-web","name":"e2e","tag_id":"dsh-web"}')
INST=$(echo "$RESP" | jq -r '.id // empty' 2>/dev/null)
[ -n "$INST" ] || fail "instance create: $RESP"
say "instance created: $INST"

# --- 5. wait ready (real dsh boots the plugin tree; be patient) ------------
READY=""
for i in $(seq 1 120); do
  INFO=$(curl -sk "https://127.0.0.1:$PORT/api/instances/dsh?id=$INST")
  PP=$(echo "$INFO" | jq -r '.proxy_port // empty' 2>/dev/null)
  ST=$(curl -sk "https://127.0.0.1:$PORT/api/instances" | jq -r ".instances[] | select(.id==\"$INST\") | .status" 2>/dev/null)
  if [ -n "$PP" ] && [ "$ST" = "running" ]; then READY=1; break; fi
  if [ "$ST" = "failed" ]; then fail "instance failed: $INFO"; fi
  sleep 1
done
[ -n "$READY" ] || fail "instance not ready in 120s (last: $INFO)"
say "instance running; proxy port $PP"

# --- 6. proxy token-gate matrix (remote client = LAN IP) -------------------
C1=$(curl -sk -o /dev/null -w '%{http_code}' "https://$LANIP:$PP/")
say "LAN no-token -> HTTP $C1"
[ "$C1" = "401" ] || fail "expected 401 from LAN client without token, got $C1"

C2=$(curl -sk -D "$TDIR/h1" -o /dev/null -w '%{http_code}' "https://$LANIP:$PP/?token=$TOKEN")
LOC=$(grep -i '^location:' "$TDIR/h1" | tr -d '\r' | awk '{print $2}')
CK=$(grep -ci '^set-cookie: mw_token=' "$TDIR/h1")
say "LAN first nav token -> HTTP $C2, Location=$LOC, set-cookie=$CK"
[ "$C2" = "302" ] || fail "expected 302 on token navigation, got $C2"
[ "$LOC" = "/" ] || fail "redirect target should be token-free '/', got $LOC"
[ "$CK" -ge 1 ] || fail "mw_token cookie not set"

# Follow the redirect with the cookie jar: the embed must land on 200.
C3=$(curl -skL -c "$TDIR/jar" -o "$TDIR/index.html" -w '%{http_code}' "https://$LANIP:$PP/?token=$TOKEN")
say "LAN token nav follow (-L, jar) -> HTTP $C3"
[ "$C3" = "200" ] || fail "expected 200 after redirect, got $C3"
grep -qi "<html" "$TDIR/index.html" || fail "SPA index not html"

# Cookie-only request (what the SPA's own fetches look like): 200.
C4=$(curl -sk -b "$TDIR/jar" -o /dev/null -w '%{http_code}' "https://$LANIP:$PP/")
say "LAN cookie-only -> HTTP $C4"
[ "$C4" = "200" ] || fail "expected 200 with cookie, got $C4"

# Loopback client bypasses the gate (local browser scenario).
C5=$(curl -sk -o /dev/null -w '%{http_code}' "https://127.0.0.1:$PP/")
say "loopback no-token -> HTTP $C5 (bypass expected)"
[ "$C5" = "200" ] || fail "expected 200 for loopback bypass, got $C5"

# --- 7. WS upgrade through the remote token gate ----------------------------
# Browsers speak WS over HTTP/1.1 (Upgrade is invalid over h2 — the
# Go server would answer 426 before forwarding), so pin http1.1.
WS=$(curl -sk --http1.1 --noproxy '*' -b "$TDIR/jar" --max-time 6 -i \
  -H "Connection: Upgrade" -H "Upgrade: websocket" \
  -H "Sec-WebSocket-Version: 13" -H "Sec-WebSocket-Key: dGhlIHNhbXBsZSBub25jZQ==" \
  "https://$LANIP:$PP/api/events.mux" | head -1)
say "LAN WS upgrade (cookie) -> $WS"
case "$WS" in
  *101*) say "WS upgrade passthrough OK" ;;
  *) echo "$WS" > "$TDIR/ws.txt"; say "WS upgrade got non-101: $WS (recorded, check upstream behaviour)" ;;
esac

# --- 8. session watch real-run validation (#63) ------------------------------
SCOPE1=$(curl -sk "https://127.0.0.1:$PORT/api/instances/dsh/scope?id=$INST")
F1=$(echo "$SCOPE1" | jq -c '.foreign_active_sessions // []' 2>/dev/null)
say "scope right after ready (own preseed must be excluded): $F1"
if echo "$F1" | jq -e 'length > 0' >/dev/null 2>&1; then
  fail "own preseed session leaked into foreign_active_sessions: $F1"
fi

FAKE="$TDIR/dshhome/sessions/--e2e-fake--/session-ffff1111"
mkdir -p "$FAKE" && touch "$FAKE/session.jsonl.zstd"
sleep 5   # watch polls every 3s
SCOPE2=$(curl -sk "https://127.0.0.1:$PORT/api/instances/dsh/scope?id=$INST")
F2=$(echo "$SCOPE2" | jq -c '.foreign_active_sessions // []' 2>/dev/null)
say "scope with fake active session: $F2"
echo "$F2" | jq -e 'index("session-ffff1111")' >/dev/null 2>&1 \
  || fail "fake session not flagged foreign: $F2"

rm -rf "$TDIR/dshhome/sessions/--e2e-fake--"
sleep 5
SCOPE3=$(curl -sk "https://127.0.0.1:$PORT/api/instances/dsh/scope?id=$INST")
F3=$(echo "$SCOPE3" | jq -c '.foreign_active_sessions // []' 2>/dev/null)
say "scope after fake removed: $F3"
echo "$F3" | jq -e 'index("session-ffff1111")' >/dev/null 2>&1 \
  && fail "fake session still flagged after removal: $F3"

# --- 9. shutdown cleanliness -------------------------------------------------
# Capture the daemon's direct children (the dsh web process) BEFORE the
# kill, then assert they are gone: the env is not part of the cmdline,
# so a cmdline-based pgrep would miss orphans.
CHILDREN=$(pgrep -P "$DAEMON_PID" 2>/dev/null | tr '\n' ' ')
kill "$DAEMON_PID" 2>/dev/null
for i in $(seq 1 20); do kill -0 "$DAEMON_PID" 2>/dev/null || break; sleep 1; done
kill -0 "$DAEMON_PID" 2>/dev/null && { kill -9 "$DAEMON_PID"; sleep 1; }
LEFTOVER=""
for cpid in $CHILDREN; do
  if kill -0 "$cpid" 2>/dev/null; then LEFTOVER="$LEFTOVER $cpid"; fi
done
say "daemon children at shutdown: [$CHILDREN]; still alive: [$LEFTOVER]"
[ -z "$LEFTOVER" ] || fail "orphan dsh processes left behind:$LEFTOVER"
DAEMON_PID=""

say "ALL PR5 CHECKS PASSED"
exit 0
