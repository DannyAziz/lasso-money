#!/bin/sh
# Lasso Money installer.
#
#   curl -fsSL https://raw.githubusercontent.com/DannyAziz/lasso-money/main/install.sh | sh
#
# Downloads the latest release binary for this OS/arch, verifies its
# checksum, and installs it to /usr/local/bin or ~/.local/bin.
# Override the destination with LASSO_INSTALL_DIR.
set -eu

REPO="DannyAziz/lasso-money"
BASE_URL="https://github.com/${REPO}/releases/latest/download"

main() {
  os=$(detect_os)
  arch=$(detect_arch)
  archive="lasso_${os}_${arch}.tar.gz"

  tmpdir=$(mktemp -d)
  trap 'rm -rf "$tmpdir"' EXIT

  echo "Downloading ${archive}…"
  download "${BASE_URL}/${archive}" "${tmpdir}/${archive}"
  download "${BASE_URL}/checksums.txt" "${tmpdir}/checksums.txt"

  verify_checksum "$tmpdir" "$archive"

  tar -xzf "${tmpdir}/${archive}" -C "$tmpdir"
  [ -f "${tmpdir}/lasso" ] || fail "archive did not contain a lasso binary"

  dest=$(install_dir)
  install -m 0755 "${tmpdir}/lasso" "${dest}/lasso"

  echo
  echo "Installed lasso to ${dest}/lasso"
  "${dest}/lasso" version 2>/dev/null || true
  case ":${PATH}:" in
    *":${dest}:"*) ;;
    *)
      echo
      echo "NOTE: ${dest} is not on your PATH. Add it with:"
      echo "  export PATH=\"${dest}:\$PATH\""
      ;;
  esac
  echo
  echo "Next steps:"
  echo "  humans: run \`lasso init\`, then \`lasso doctor\` and follow its output"
  echo "  agents: read SETUP.md (https://github.com/${REPO}/blob/main/SETUP.md)"
  echo "          and start with \`lasso --llms\` + \`lasso doctor --format json\`"
}

detect_os() {
  case "$(uname -s)" in
    Darwin) echo darwin ;;
    Linux) echo linux ;;
    *) fail "unsupported OS $(uname -s); download manually from https://github.com/${REPO}/releases" ;;
  esac
}

detect_arch() {
  case "$(uname -m)" in
    x86_64 | amd64) echo amd64 ;;
    arm64 | aarch64) echo arm64 ;;
    *) fail "unsupported architecture $(uname -m); download manually from https://github.com/${REPO}/releases" ;;
  esac
}

download() {
  url=$1
  out=$2
  if command -v curl >/dev/null 2>&1; then
    curl -fsSL -o "$out" "$url" || fail "download failed: $url"
  elif command -v wget >/dev/null 2>&1; then
    wget -qO "$out" "$url" || fail "download failed: $url"
  else
    fail "need curl or wget"
  fi
}

verify_checksum() {
  dir=$1
  file=$2
  expected=$(grep " ${file}\$" "${dir}/checksums.txt" | awk '{print $1}')
  [ -n "$expected" ] || fail "no checksum found for ${file}"
  if command -v sha256sum >/dev/null 2>&1; then
    actual=$(sha256sum "${dir}/${file}" | awk '{print $1}')
  elif command -v shasum >/dev/null 2>&1; then
    actual=$(shasum -a 256 "${dir}/${file}" | awk '{print $1}')
  else
    echo "WARNING: no sha256 tool found; skipping checksum verification" >&2
    return 0
  fi
  [ "$actual" = "$expected" ] || fail "checksum mismatch for ${file}"
  echo "Checksum verified."
}

install_dir() {
  if [ -n "${LASSO_INSTALL_DIR:-}" ]; then
    mkdir -p "$LASSO_INSTALL_DIR"
    echo "$LASSO_INSTALL_DIR"
    return
  fi
  if [ -d /usr/local/bin ] && [ -w /usr/local/bin ]; then
    echo /usr/local/bin
    return
  fi
  mkdir -p "${HOME}/.local/bin"
  echo "${HOME}/.local/bin"
}

fail() {
  echo "error: $1" >&2
  exit 1
}

main "$@"
