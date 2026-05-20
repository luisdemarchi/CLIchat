#!/usr/bin/env bash
# CLIchat uninstaller. Keeps ~/.clichat by default.

set -euo pipefail

if command -v clichat >/dev/null 2>&1; then
  exec clichat uninstall "$@"
fi

BIN_DIR="${CLICHAT_BIN_DIR:-$HOME/.local/bin}"
if [[ -x "$BIN_DIR/clichat" ]]; then
  exec "$BIN_DIR/clichat" uninstall "$@"
fi

echo "clichat command not found. Remove files manually or run from an installed CLIchat checkout." >&2
exit 1
