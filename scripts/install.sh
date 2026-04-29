#!/usr/bin/env bash
# CLIchat — installer
# One-shot install for end users:
#   1. Build agent-host + agentctl Go binaries → ~/.local/bin
#   2. Build Wails desktop app → /Applications (macOS) or ~/.local/share/clichat (linux)
#   3. Install Claude Code hooks in ~/.claude/settings.json
#   4. Auto-start agent-host (launchd on macOS, systemd user on linux)
# Re-running is safe: idempotent.

set -euo pipefail

REPO_ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
BIN_DIR="${AGENT_CHAT_BIN_DIR:-$HOME/.local/bin}"
STATE_DIR="$HOME/.clichat"
LOG_DIR="$STATE_DIR/logs"

color_red=$'\033[31m'
color_green=$'\033[32m'
color_yellow=$'\033[33m'
color_reset=$'\033[0m'

info()  { printf "${color_green}==>${color_reset} %s\n" "$*"; }
warn()  { printf "${color_yellow}!! ${color_reset} %s\n" "$*"; }
fail()  { printf "${color_red}xx ${color_reset} %s\n" "$*" >&2; exit 1; }

need_cmd() {
  command -v "$1" >/dev/null 2>&1 || fail "$1 nao encontrado. Instale antes de rodar este script."
}

OS="$(uname -s)"
case "$OS" in
  Darwin) PLATFORM=mac ;;
  Linux) PLATFORM=linux ;;
  *) fail "OS nao suportado: $OS" ;;
esac

info "Plataforma detectada: $PLATFORM"

# ---------------------------------------------------------------------------
# Dependencies
# ---------------------------------------------------------------------------

need_cmd go
need_cmd uname

if ! command -v wails >/dev/null 2>&1; then
  warn "Wails CLI nao encontrado. Instalando em \$GOBIN/\$GOPATH/bin..."
  go install github.com/wailsapp/wails/v2/cmd/wails@v2.12.0
fi
need_cmd wails

if ! command -v pnpm >/dev/null 2>&1; then
  warn "pnpm nao encontrado — fallback para npm."
  need_cmd npm
fi

mkdir -p "$BIN_DIR" "$STATE_DIR" "$LOG_DIR"

# ---------------------------------------------------------------------------
# Build Go binaries
# ---------------------------------------------------------------------------

info "Compilando agent-host…"
( cd "$REPO_ROOT" && go build -o "$BIN_DIR/agent-host" ./cmd/agent-host )

info "Compilando agentctl…"
( cd "$REPO_ROOT" && go build -o "$BIN_DIR/agentctl" ./cmd/agentctl )

info "Binarios em $BIN_DIR"

# ---------------------------------------------------------------------------
# Install Claude Code hooks
# ---------------------------------------------------------------------------

info "Instalando hooks no ~/.claude/settings.json…"
AGENTCTL_BIN="$BIN_DIR/agentctl" "$BIN_DIR/agentctl" install-hooks || warn "install-hooks falhou (continuando)"

# ---------------------------------------------------------------------------
# Build Wails desktop app
# ---------------------------------------------------------------------------

if [[ "${SKIP_WAILS:-}" != "1" ]]; then
  info "Compilando app desktop (Wails)…"
  ( cd "$REPO_ROOT" && wails build -clean -tags webkit2_41 2>/dev/null || wails build -clean )
fi

# ---------------------------------------------------------------------------
# Place app
# ---------------------------------------------------------------------------

if [[ "$PLATFORM" == "mac" ]]; then
  APP_SRC="$REPO_ROOT/build/bin/AgentChatLocal.app"
  APP_DEST="/Applications/AgentChatLocal.app"
  if [[ -d "$APP_SRC" ]]; then
    info "Copiando AgentChatLocal.app para /Applications/…"
    rm -rf "$APP_DEST"
    cp -R "$APP_SRC" "$APP_DEST" || warn "Sem permissao para /Applications. App ficou em $APP_SRC."
  elif [[ "${SKIP_WAILS:-}" != "1" ]]; then
    warn "App Wails nao gerado em $APP_SRC."
  fi
fi

if [[ "$PLATFORM" == "linux" ]]; then
  APP_SRC="$REPO_ROOT/build/bin/AgentChatLocal"
  APP_DEST="$HOME/.local/share/clichat/AgentChatLocal"
  if [[ -f "$APP_SRC" ]]; then
    mkdir -p "$(dirname "$APP_DEST")"
    cp "$APP_SRC" "$APP_DEST"
    chmod +x "$APP_DEST"
    info "App copiado para $APP_DEST"
  fi
fi

# ---------------------------------------------------------------------------
# Auto-start agent-host
# ---------------------------------------------------------------------------

if [[ "$PLATFORM" == "mac" ]]; then
  PLIST_PATH="$HOME/Library/LaunchAgents/com.clichat.host.plist"
  info "Registrando launchd em $PLIST_PATH…"
  cat >"$PLIST_PATH" <<PLIST
<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0">
<dict>
  <key>Label</key>
  <string>com.clichat.host</string>
  <key>ProgramArguments</key>
  <array>
    <string>$BIN_DIR/agent-host</string>
    <string>serve</string>
  </array>
  <key>KeepAlive</key>
  <true/>
  <key>RunAtLoad</key>
  <true/>
  <key>StandardOutPath</key>
  <string>$LOG_DIR/host.out.log</string>
  <key>StandardErrorPath</key>
  <string>$LOG_DIR/host.err.log</string>
  <key>EnvironmentVariables</key>
  <dict>
    <key>PATH</key>
    <string>/usr/local/bin:/opt/homebrew/bin:/usr/bin:/bin</string>
  </dict>
</dict>
</plist>
PLIST
  launchctl unload "$PLIST_PATH" 2>/dev/null || true
  launchctl load "$PLIST_PATH"
  info "agent-host rodando via launchd."
fi

if [[ "$PLATFORM" == "linux" ]]; then
  SYSTEMD_DIR="$HOME/.config/systemd/user"
  mkdir -p "$SYSTEMD_DIR"
  SERVICE_PATH="$SYSTEMD_DIR/clichat.service"
  cat >"$SERVICE_PATH" <<UNIT
[Unit]
Description=CLIchat host
After=network.target

[Service]
ExecStart=$BIN_DIR/agent-host serve
Restart=always
RestartSec=2
StandardOutput=append:$LOG_DIR/host.out.log
StandardError=append:$LOG_DIR/host.err.log

[Install]
WantedBy=default.target
UNIT
  systemctl --user daemon-reload
  systemctl --user enable --now clichat.service || warn "systemd user nao iniciou. Rode manualmente: agent-host serve"
  info "agent-host rodando via systemd user."
fi

# ---------------------------------------------------------------------------
# Summary
# ---------------------------------------------------------------------------

cat <<EOF

${color_green}✓ CLIchat instalado.${color_reset}

  Binarios:        $BIN_DIR/agent-host, $BIN_DIR/agentctl
  Estado:          $STATE_DIR/state.json
  Logs:            $LOG_DIR/
  Hooks Claude:    ~/.claude/settings.json (managed)

Como usar:
  1. Abra o app desktop AgentChatLocal (em /Applications no macOS).
  2. Em qualquer terminal, rode \`claude\` — a sessao aparece automaticamente
     na lista de chats do app, com status, ferramenta atual e respostas.
  3. Para verificar o daemon:    agent-host serve   (se ja nao estiver rodando)
                                  agentctl list
  4. Para desinstalar hooks:     agentctl uninstall-hooks
  5. Garanta que $BIN_DIR esta no \$PATH (adicione em ~/.zshrc se necessario).

EOF
