#!/usr/bin/env bash
# CLIchat — uninstall
# Removes binaries, hooks, and the autostart service. Keeps state.json by default.

set -euo pipefail

BIN_DIR="${AGENT_CHAT_BIN_DIR:-$HOME/.local/bin}"
STATE_DIR="$HOME/.clichat"
KEEP_STATE="${KEEP_STATE:-1}"

OS="$(uname -s)"

if command -v "$BIN_DIR/agentctl" >/dev/null 2>&1; then
  "$BIN_DIR/agentctl" uninstall-hooks || true
fi

case "$OS" in
  Darwin)
    PLIST="$HOME/Library/LaunchAgents/com.clichat.host.plist"
    if [[ -f "$PLIST" ]]; then
      launchctl unload "$PLIST" 2>/dev/null || true
      rm -f "$PLIST"
    fi
    rm -rf /Applications/AgentChatLocal.app 2>/dev/null || true
    ;;
  Linux)
    systemctl --user disable --now clichat.service 2>/dev/null || true
    rm -f "$HOME/.config/systemd/user/clichat.service"
    systemctl --user daemon-reload
    rm -f "$HOME/.local/share/clichat/AgentChatLocal" 2>/dev/null || true
    ;;
esac

rm -f "$BIN_DIR/agent-host" "$BIN_DIR/agentctl"

if [[ "$KEEP_STATE" != "1" ]]; then
  rm -rf "$STATE_DIR"
  echo "Estado removido em $STATE_DIR."
else
  echo "Estado preservado em $STATE_DIR (defina KEEP_STATE=0 para apagar)."
fi

echo "CLIchat desinstalado."
