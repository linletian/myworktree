#!/bin/bash
set -e

XTERM_VERSION="6.0.0"
FIT_VERSION="0.11.0"
BASE_DIR="$(cd "$(dirname "$0")/.." && pwd)"

VENDOR_XTERM="$BASE_DIR/internal/ui/static/vendor/xterm"
VENDOR_FIT="$BASE_DIR/internal/ui/static/vendor/xterm-addon-fit"

mkdir -p "$VENDOR_XTERM" "$VENDOR_FIT"

curl -fsSL "https://cdn.jsdelivr.net/npm/@xterm/xterm@${XTERM_VERSION}/lib/xterm.js" \
    -o "$VENDOR_XTERM/xterm.js"

curl -fsSL "https://cdn.jsdelivr.net/npm/@xterm/xterm@${XTERM_VERSION}/css/xterm.css" \
    -o "$VENDOR_XTERM/xterm.css"

curl -fsSL "https://cdn.jsdelivr.net/npm/@xterm/addon-fit@${FIT_VERSION}/lib/addon-fit.js" \
    -o "$VENDOR_FIT/xterm-addon-fit.js"

echo "Downloaded xterm.js v${XTERM_VERSION} and addon-fit v${FIT_VERSION}"
