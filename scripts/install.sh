#!/usr/bin/env bash
# myworktree one-line installer.
#
# Usage:
#   curl -fsSL https://raw.githubusercontent.com/linletian/myworktree/main/scripts/install.sh | bash
#   curl -fsSL .../install.sh | bash -s -- -v v0.4.2
#   curl -fsSL .../install.sh | bash -s -- --no-modify-path
#   INSTALL_ALIAS=mwt bash install.sh        # avoid `mw` name clash
#
# Env vars:
#   INSTALL_DIR=<dir>       target directory (default: ~/.local/bin)
#   INSTALL_ALIAS=<name>    short alias command (default: mw; empty to skip)
#   MYWORKTREE_VERSION=<v>  pin version (default: latest release)
#   NO_MODIFY_PATH=1        do not modify shell rc files

set -euo pipefail

REPO="linletian/myworktree"
APP="myworktree"

MUTED='\033[0;2m'
RED='\033[0;31m'
NC='\033[0m'

# Defaults; users can override via env or flags.
INSTALL_DIR="${INSTALL_DIR:-$HOME/.local/bin}"
: "${INSTALL_ALIAS:=mw}"
requested_version="${MYWORKTREE_VERSION:-}"
no_modify_path="${NO_MODIFY_PATH:-false}"
binary_path=""

usage() {
  cat <<EOF
myworktree installer

Usage: install.sh [options]

Options:
  -h, --help              show this help
  -v, --version <ver>     install a specific version (e.g., v0.4.2 or 0.4.2)
      --no-modify-path    do not modify shell rc files

Env vars:
  INSTALL_DIR=<dir>       target directory (default: ~/.local/bin)
  INSTALL_ALIAS=<name>    short alias command (default: mw; empty to skip)
  MYWORKTREE_VERSION=<v>  pin version (default: latest release)
  NO_MODIFY_PATH=1        same as --no-modify-path

Examples:
  curl -fsSL https://raw.githubusercontent.com/${REPO}/main/scripts/install.sh | bash
  curl -fsSL .../install.sh | bash -s -- -v v0.4.2
  INSTALL_ALIAS=mwt bash install.sh     # avoid conflict with the Debian/Ubuntu 'mw' package
  INSTALL_DIR=~/bin bash install.sh     # install into a custom dir
EOF
}

die() { echo -e "${RED}Error:${NC} $*" >&2; exit 1; }
info() { echo -e "${MUTED}$*${NC}"; }

while [[ $# -gt 0 ]]; do
  case "$1" in
    -h|--help) usage; exit 0 ;;
    -v|--version)
      [[ -n "${2:-}" ]] || die "--version requires a value"
      requested_version="$2"; shift 2 ;;
    --no-modify-path) no_modify_path=true; shift ;;
    *) info "Warning: unknown option '$1' (ignored)"; shift ;;
  esac
done

# --- platform detection ---------------------------------------------------
case "$(uname -s)" in
  Darwin*) platform="macOS" ;;
  Linux*)  platform="Linux" ;;
  *) die "Unsupported OS: $(uname -s) (only macOS and Linux are supported)" ;;
esac

case "$(uname -m)" in
  arm64|aarch64) goarch="arm64" ;;
  x86_64|amd64)  goarch="amd64" ;;
  *) die "Unsupported architecture: $(uname -m)" ;;
esac

# Rosetta 2: x86_64 binary on Apple Silicon → prefer arm64
if [ "$platform" = "macOS" ] && [ "$goarch" = "amd64" ]; then
  translated=$(sysctl -n sysctl.proc_translated 2>/dev/null || echo 0)
  if [ "$translated" = "1" ]; then
    info "Detected Rosetta x86_64 on Apple Silicon; using arm64 build"
    goarch="arm64"
  fi
fi

info "Platform: ${platform}/${goarch}"

# --- version resolution --------------------------------------------------
if [ -n "$requested_version" ]; then
  version="${requested_version#v}"
  tag="v${version}"
else
  info "Resolving latest version from GitHub..."
  # /releases/latest redirects to /releases/tag/<tag>; capture the redirect target
  # to avoid the GitHub API rate limit and JSON parsing.
  tag=$(curl -fsSLI -o /dev/null -w '%{url_effective}\n' \
          "https://github.com/${REPO}/releases/latest" \
        | sed -n 's#.*/tag/\(v[^/?]*\).*#\1#p' | head -1)
  [ -n "$tag" ] || die "Failed to resolve latest version from GitHub"
  version="${tag#v}"
fi
info "Version:  ${version} (${tag})"

# --- download + verify ---------------------------------------------------
tmp_dir=$(mktemp -d -t myworktree-install-XXXXXX)
trap 'rm -rf "$tmp_dir"' EXIT

archive="${APP}_v${version}_${platform}_${goarch}.tar.gz"
base_url="https://github.com/${REPO}/releases/download/${tag}"

info "Downloading ${archive}..."
curl -fsSL -o "${tmp_dir}/${archive}" "${base_url}/${archive}" \
  || die "Download failed: ${base_url}/${archive}
Is release ${tag} published? (https://github.com/${REPO}/releases/tag/${tag})"

info "Downloading checksums.txt..."
curl -fsSL -o "${tmp_dir}/checksums.txt" "${base_url}/checksums.txt" \
  || die "Download failed: ${base_url}/checksums.txt"

info "Verifying SHA256..."
(cd "${tmp_dir}" && shasum -a 256 -c checksums.txt --ignore-missing) \
  || die "Checksum verification failed"

info "Extracting..."
tar -xzf "${tmp_dir}/${archive}" -C "${tmp_dir}"
extracted_dir=$(find "${tmp_dir}" -maxdepth 1 -mindepth 1 -type d \
                -name "${APP}_v*" | head -1)
[ -n "$extracted_dir" ] || die "Could not find extracted directory in archive"

# --- existing-binary conflict detection ---------------------------------
# Refuse to overwrite if the destination exists and is NOT a myworktree binary.
# (mw is a 2-letter name; Debian/Ubuntu's `mw` package is a real clash.)
# Detection ladder, in order:
#   1) `<path> --version` output mentions the string "myworktree"  (cheap, fast)
#   2) `<path> --version` output looks like the myworktree version line
#      (`<prog> vX.Y.Z (<commit>) built <date>`) — the prog name may be
#      "mw" (symlink), "myworktree", or anything else the user renamed,
#      so the text alone doesn't include "myworktree"
#   3) `strings <path>` contains "myworktree" — catches cases where the
#      binary is a valid myworktree build but --version is broken (rare)
check_existing() {
  local path="$1" label="$2"
  [ ! -e "$path" ] && return 0
  local out
  out=$("$path" --version 2>/dev/null || echo "")
  if echo "$out" | grep -qi "$APP"; then
    info "Existing ${label} is an older myworktree (matched 'myworktree' in --version); will upgrade in place"
    return 0
  fi
  if [ -n "$out" ] && echo "$out" | grep -Eq '^[^ ]+ +v[0-9]+\.[0-9]+\.[0-9]+( \([^)]+\))?( built [^ ]+)?$'; then
    # matches "<prog> vX.Y.Z [(commit)] [built <date>]" — the myworktree format
    info "Existing ${label} looks like a myworktree build (--version: ${out}); will upgrade in place"
    return 0
  fi
  if command -v strings >/dev/null 2>&1 && strings "$path" 2>/dev/null | grep -q "$APP"; then
    info "Existing ${label} is a myworktree build (matched 'myworktree' via strings); will upgrade in place"
    return 0
  fi
  die "${path} exists and is NOT myworktree (got: ${out:-non-myworktree binary}).
Resolve by one of:
  1) Remove it:        rm '${path}'
  2) Different alias:  INSTALL_ALIAS=mwt  bash install.sh
  3) Dedicated dir:    INSTALL_DIR=\$HOME/.myworktree/bin  bash install.sh
                       (then 'export PATH=\$HOME/.myworktree/bin:\$PATH')"
}

# --- install -------------------------------------------------------------
mkdir -p "${INSTALL_DIR}"

check_existing "${INSTALL_DIR}/${APP}" "${APP}"
[ -n "$INSTALL_ALIAS" ] && check_existing "${INSTALL_DIR}/${INSTALL_ALIAS}" "$INSTALL_ALIAS"

info "Installing to ${INSTALL_DIR}/"
install -m 755 "${extracted_dir}/${APP}" "${INSTALL_DIR}/${APP}"
if [ -n "$INSTALL_ALIAS" ] && [ "$INSTALL_ALIAS" != "$APP" ]; then
  ln -sf "${INSTALL_DIR}/${APP}" "${INSTALL_DIR}/${INSTALL_ALIAS}"
fi

# --- PATH handling -------------------------------------------------------
add_export_line() {
  local config_file="$1" line="$2"
  [ -f "$config_file" ] || return 1
  if grep -Fxq "$line" "$config_file" 2>/dev/null; then
    return 0
  fi
  if [[ -w "$config_file" ]]; then
    printf '\n# myworktree\n%s\n' "$line" >> "$config_file"
    info "Added ${INSTALL_DIR} to PATH in ${config_file}"
    return 0
  fi
  return 1
}

if [ "$no_modify_path" != "true" ]; then
  case ":$PATH:" in
    *":${INSTALL_DIR}:"*) : ;;  # already on PATH
    *)
      XDG_CONFIG_HOME="${XDG_CONFIG_HOME:-$HOME/.config}"
      shell_name=$(basename "${SHELL:-sh}")
      case "$shell_name" in
        fish)
          config="${XDG_CONFIG_HOME}/fish/config.fish"
          mkdir -p "$(dirname "$config")"
          [ -f "$config" ] || touch "$config"
          if ! grep -Fxq "fish_add_path ${INSTALL_DIR}" "$config"; then
            echo "fish_add_path ${INSTALL_DIR}" >> "$config"
            info "Added ${INSTALL_DIR} to PATH in ${config}"
          fi
          ;;
        zsh|bash|sh|ash)
          config=""
          for f in "${ZDOTDIR:-$HOME}/.${shell_name}rc" \
                   "${ZDOTDIR:-$HOME}/.zshenv" \
                   "$HOME/.${shell_name}rc" \
                   "$HOME/.bash_profile" \
                   "$HOME/.profile"; do
            [ -f "$f" ] && config="$f" && break
          done
          [ -n "$config" ] || config="$HOME/.profile"
          add_export_line "$config" "export PATH=\"${INSTALL_DIR}:\$PATH\"" \
            || info "Could not write to ${config}; add to PATH manually:
  export PATH=\"${INSTALL_DIR}:\$PATH\""
          ;;
        *)
          info "Add ${INSTALL_DIR} to your PATH manually:
  export PATH=\"${INSTALL_DIR}:\$PATH\""
          ;;
      esac
      ;;
  esac
fi

# --- GitHub Actions integration -----------------------------------------
if [ -n "${GITHUB_ACTIONS-}" ] && [ "${GITHUB_ACTIONS}" = "true" ]; then
  echo "${INSTALL_DIR}" >> "$GITHUB_PATH"
  info "Added ${INSTALL_DIR} to \$GITHUB_PATH"
fi

# --- macOS Gatekeeper hint ----------------------------------------------
if [ "$platform" = "macOS" ]; then
  echo ""
  info "macOS may quarantine the downloaded binary (Gatekeeper).
If 'myworktree' does not respond or shows 'cannot be opened':
  xattr -d com.apple.quarantine ${INSTALL_DIR}/myworktree"
fi

# --- final message -------------------------------------------------------
echo ""
echo -e "${MUTED}myworktree ${version} installed:${NC}"
echo "  ${APP}     → ${INSTALL_DIR}/${APP}"
if [ -n "$INSTALL_ALIAS" ] && [ "$INSTALL_ALIAS" != "$APP" ]; then
  echo "  ${INSTALL_ALIAS} → ${INSTALL_DIR}/${INSTALL_ALIAS} (symlink → ${APP})"
fi
echo ""
echo -e "${MUTED}Try:${NC}"
echo "  ${APP} --version"
[ -n "$INSTALL_ALIAS" ] && [ "$INSTALL_ALIAS" != "$APP" ] \
  && echo "  ${INSTALL_ALIAS} --version"
echo ""
echo -e "${MUTED}If 'command not found', open a new shell (PATH was added to your rc).${NC}"

# --- running-daemon warning --------------------------------------------
# The new binary is on disk; an already-running daemon is still on the OLD binary
# (in-memory code page is unaffected by the file replacement — the kernel keeps
# the old inode until the process exits). Restart the daemon to pick up the new
# binary. We do NOT auto-stop: that could hard-kill long-running PTY / opencode
# / reasonix sessions and discard the in-memory ring buffer + WebSocket state.
if command -v pgrep >/dev/null 2>&1 && pgrep -x myworktree >/dev/null 2>&1; then
  echo ""
  echo -e "${RED}Heads up:${NC} a myworktree daemon is currently running."
  echo "The installed binary (${version}) will not take effect until you restart it."
  echo ""
  echo "Recommended (in a worktree, after stopping work in the UI):"
  echo "  ${APP} stop   # asks the daemon to stop its instances and exit"
  echo "  ${APP} start  # spawns a new daemon on the new binary"
  echo ""
  echo "Force-kill fallback (only if 'stop' hangs; loses in-memory state):"
  echo "  pkill -x myworktree"
fi
