#!/usr/bin/env bash
set -euo pipefail

PREFIX_DEFAULT="${HOME}/.local/bin"
PREFIX="${PREFIX_DEFAULT}"
SKIP_INIT=0

while [[ $# -gt 0 ]]; do
  case "$1" in
    --prefix)
      PREFIX="$2"
      shift 2
      ;;
    --skip-init)
      SKIP_INIT=1
      shift
      ;;
    -h|--help)
      cat <<'EOF'
Install vsmm without sudo.

Usage:
  ./scripts/install.sh [--prefix DIR] [--skip-init]

Options:
  --prefix DIR  Install destination directory (default: ~/.local/bin)
  --skip-init   Skip creating ~/.vsmm/config.json
EOF
      exit 0
      ;;
    *)
      echo "unknown option: $1" >&2
      exit 1
      ;;
  esac
done

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
mkdir -p "${PREFIX}"

TMP_BIN="$(mktemp "${TMPDIR:-/tmp}/vsmm.XXXXXX")"
cleanup() {
  rm -f "${TMP_BIN}"
}
trap cleanup EXIT

cd "${ROOT_DIR}"
go build -o "${TMP_BIN}" .
install -m 0755 "${TMP_BIN}" "${PREFIX}/vsmm"

VSM_HOME="${HOME}/.vsmm"
mkdir -p "${VSM_HOME}/cache" "${VSM_HOME}/backups"

if [[ "${SKIP_INIT}" -eq 0 ]]; then
  if [[ ! -f "${VSM_HOME}/config.json" ]]; then
    "${PREFIX}/vsmm" init --config "${VSM_HOME}/config.json"
  fi
fi

cat <<EOF
Installed vsmm to: ${PREFIX}/vsmm
VSMM home: ${VSM_HOME}
Config: ${VSM_HOME}/config.json
EOF

case ":${PATH}:" in
  *":${PREFIX}:"*) ;;
  *)
    echo
    echo "Note: ${PREFIX} is not on your PATH."
    echo "Add this to your shell profile:"
    echo "  export PATH=\"${PREFIX}:\$PATH\""
    ;;
esac
