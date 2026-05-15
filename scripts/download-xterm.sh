#!/bin/bash
set -e

# Check required dependencies
for cmd in curl python3; do
    if ! command -v "$cmd" > /dev/null 2>&1; then
        echo "ERROR: '$cmd' is required but not installed." >&2
        exit 1
    fi
done

XTERM_VERSION="6.0.0"
FIT_VERSION="0.11.0"
BASE_DIR="$(cd "$(dirname "$0")/.." && pwd)"

VENDOR_XTERM="$BASE_DIR/internal/ui/static/vendor/xterm"
VENDOR_FIT="$BASE_DIR/internal/ui/static/vendor/xterm-addon-fit"

# All intermediate files live here until the final step succeeds.
STAGING="$BASE_DIR/.xterm-staging"

# Clean up staging directory on exit (success or failure).
# Old vendor files are NOT touched until all steps succeed.
cleanup() {
    rm -rf "$STAGING"
}
trap cleanup EXIT

# ----- Step 1: Fetch metadata from npm registry -----
echo "Fetching package metadata from npm registry..."
NPM_REGISTRY="https://registry.npmjs.org"

XTERM_TARBALL=$(curl -fsSL "$NPM_REGISTRY/@xterm/xterm/${XTERM_VERSION}" | \
    python3 -c "import json,sys; print(json.load(sys.stdin)['dist']['tarball'])")
XTERM_INTEGRITY=$(curl -fsSL "$NPM_REGISTRY/@xterm/xterm/${XTERM_VERSION}" | \
    python3 -c "import json,sys; print(json.load(sys.stdin)['dist']['integrity'])")

FIT_TARBALL=$(curl -fsSL "$NPM_REGISTRY/@xterm/addon-fit/${FIT_VERSION}" | \
    python3 -c "import json,sys; print(json.load(sys.stdin)['dist']['tarball'])")
FIT_INTEGRITY=$(curl -fsSL "$NPM_REGISTRY/@xterm/addon-fit/${FIT_VERSION}" | \
    python3 -c "import json,sys; print(json.load(sys.stdin)['dist']['integrity'])")

# ----- Step 2: Download tarballs into staging -----
echo "Downloading tarballs into staging directory..."
mkdir -p "$STAGING"

echo "  @xterm/xterm@${XTERM_VERSION}"
echo "    Tarball: $XTERM_TARBALL"
echo "    Integrity: $XTERM_INTEGRITY"
curl -fsSL "$XTERM_TARBALL" -o "$STAGING/xterm.tgz"

echo "  @xterm/addon-fit@${FIT_VERSION}"
echo "    Tarball: $FIT_TARBALL"
echo "    Integrity: $FIT_INTEGRITY"
curl -fsSL "$FIT_TARBALL" -o "$STAGING/addon-fit.tgz"

# ----- Step 3: Verify tarball integrity against npm hashes -----
echo "Verifying tarball integrity (SHA-512 from npm)..."
python3 -c "
import sys, hashlib, base64

def verify(path, expected_integrity):
    alg, raw = expected_integrity.split('-', 1)
    expected = base64.b64decode(raw)
    digest = hashlib.sha512()
    with open(path, 'rb') as f:
        for chunk in iter(lambda: f.read(8192), b''):
            digest.update(chunk)
    if digest.digest() != expected:
        print('ERROR: integrity mismatch: ' + path, file=sys.stderr)
        print('  Expected: ' + expected_integrity, file=sys.stderr)
        sys.exit(1)
    print('  Verified: ' + alg + '-<integrity> OK')

verify('$STAGING/xterm.tgz', '$XTERM_INTEGRITY')
verify('$STAGING/addon-fit.tgz', '$FIT_INTEGRITY')
print('All tarballs verified.')
"

# ----- Step 4: Extract into staging subdirectories -----
echo "Extracting files into staging..."
python3 << PYEOF
import tarfile, os, shutil

staging = '$STAGING'
vendor_xterm = '$VENDOR_XTERM'
vendor_fit = '$VENDOR_FIT'

# Extract @xterm/xterm
xterm_pkg = os.path.join(staging, 'xterm-pkg')
os.makedirs(xterm_pkg)
with tarfile.open(os.path.join(staging, 'xterm.tgz'), 'r:gz') as t:
    t.extractall(xterm_pkg, filter='data')

# Extract @xterm/addon-fit
fit_pkg = os.path.join(staging, 'fit-pkg')
os.makedirs(fit_pkg)
with tarfile.open(os.path.join(staging, 'addon-fit.tgz'), 'r:gz') as t:
    t.extractall(fit_pkg, filter='data')

# Copy files to staging with correct names (not move, so extraction dirs can be cleaned)
files = [
    (os.path.join(xterm_pkg, 'package', 'lib', 'xterm.js'),
     os.path.join(staging, 'xterm.js')),
    (os.path.join(xterm_pkg, 'package', 'lib', 'xterm.js.map'),
     os.path.join(staging, 'xterm.js.map')),
    (os.path.join(xterm_pkg, 'package', 'css', 'xterm.css'),
     os.path.join(staging, 'xterm.css')),
    (os.path.join(fit_pkg, 'package', 'lib', 'addon-fit.js'),
     os.path.join(staging, 'xterm-addon-fit.js')),
    (os.path.join(fit_pkg, 'package', 'lib', 'addon-fit.js.map'),
     os.path.join(staging, 'xterm-addon-fit.js.map')),
]

for src, dst in files:
    if os.path.exists(src):
        shutil.copy2(src, dst)
        print(f'  Staged: {os.path.basename(dst)}')

# Clean up extracted package dirs
shutil.rmtree(xterm_pkg)
shutil.rmtree(fit_pkg)

print('Extraction complete.')
PYEOF

# ----- Step 5: Atomic move from staging to vendor directories -----
# Old vendor files are NOT touched until this point. If any step above fails,
# the trap cleans up staging and vendor remains untouched.
echo "Installing files to vendor directories..."
mkdir -p "$VENDOR_XTERM" "$VENDOR_FIT"

mv "$STAGING/xterm.js"      "$VENDOR_XTERM/xterm.js"
mv "$STAGING/xterm.js.map"  "$VENDOR_XTERM/xterm.js.map"
mv "$STAGING/xterm.css"     "$VENDOR_XTERM/xterm.css"
mv "$STAGING/xterm-addon-fit.js"      "$VENDOR_FIT/xterm-addon-fit.js"
mv "$STAGING/xterm-addon-fit.js.map"  "$VENDOR_FIT/xterm-addon-fit.js.map"

echo "Done. xterm.js v${XTERM_VERSION} and addon-fit v${FIT_VERSION} installed."
echo "Staging directory cleaned up by trap."
