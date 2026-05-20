#!/usr/bin/env bash
# CLIchat source-checkout installer.
# Preferred public install:
#   go install github.com/luisdemarchi/CLIchat/cmd/clichat@main
#   clichat install

set -euo pipefail

REPO_ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"

if ! command -v go >/dev/null 2>&1; then
  echo "go not found. Install Go first." >&2
  exit 1
fi

cd "$REPO_ROOT"
exec go run ./cmd/clichat install "$@"
