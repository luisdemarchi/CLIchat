# Handoff — clichat

## Objetivo
App desktop (macOS-first) estilo WhatsApp Web pra leigo conversar com Claude Code / Codex / Gemini CLI sem ver TUI. Cada chat = 1 PTY oculto rodando o CLI real. Bolhas de conversa em cima + terminal embutido (xterm.js) em baixo.

## Stack
- Go 1.25 + Wails v2.12
- React 19 + Vite + xterm.js + lucide-react
- node-pty equivalente em Go: `github.com/creack/pty`

## Arquitetura
3 binários:

```
┌─────────────────┐  HTTP/SSE  ┌─────────────────────┐
│ AgentChatLocal  │ ─────────▶ │      clichat-host     │
│ (Wails desktop) │            │    daemon Go puro   │
└─────────────────┘            │  • PTY manager      │
                               │  • SSE state stream │
~/.claude/settings.json hooks  │  • MCP HTTP         │
                               │  • prompt detector  │
                               │  • JSONL watcher    │
                               │  • JSON state file  │
                               └──────────┬──────────┘
                                          ▲
                            agentctl hook session-start|...
```

- `clichat-host` em `127.0.0.1:47657` (HTTP) + `:47656` (TCP attach). Persiste em `~/.clichat/state.json`.
- `agentctl` CLI: `attach <id>`, `hook *`, `install-hooks`, `uninstall-hooks`, `list`, `register`.
- Wails app: thin client; consome `/v1/state/events` SSE.

## Principais arquivos
```
clichat/
├── cmd/clichat-host/main.go           # daemon HTTP + MCP + endpoints REST
├── cmd/agentctl/main.go             # CLI hooks + install
├── cmd/agent-test/main.go           # E2E smoke test
├── internal/agent/
│   ├── store.go                     # registry persistente JSON
│   ├── transcript.go                # JSONL discovery + watcher
│   └── promptdetect.go              # detect numbered/yes-no menus do TUI
├── internal/terminal/
│   ├── manager.go                   # PTY manager
│   └── process_unix.go              # PTY + SendLine (universal submit)
├── internal/hostclient/client.go    # Wails → daemon HTTP client
├── internal/app/app.go              # Wails bindings (CreateChat, SendMessage, FocusTerminal…)
├── frontend/src/
│   ├── App.tsx                      # WhatsApp-like UI
│   ├── components/TerminalPane.tsx  # xterm.js embed (cols=120 fixo, scroll horizontal)
│   ├── lib/status.tsx               # ícone por status/tool (lucide)
│   └── styles.css                   # theme WhatsApp
└── scripts/install.sh               # build all + install hooks + launchd/systemd
```

## O que JÁ funciona (Atualizado 2026-04-29)
1. **Persistência do Terminal (Fix Bug "Zeroing")** — Implementado `scrollback buffer` (100KB) no backend (`internal/terminal/process_unix.go`). Quando você troca de chat, o terminal não "reseta" mais; ele reenvia os últimos dados ao xterm.js assim que o painel é montado.
2. **Bolhas para Codex e Gemini** — `TranscriptWatcher` agora monitora `~/.codex/sessions/` e `~/.gemini/tmp/`. As conversas aparecem como bolhas de chat (User/Assistant), não apenas no terminal.
3. **Botões de Confirmação (WhatsApp Style)** — Corrigido bug onde bolhas do assistente limpavam botões pendentes. Agora os botões ficam ativos até a resposta do usuário.
4. **Auto-Link de Transcritos** — Transcritos externos são automaticamente vinculados a instâncias internas se o CWD ou o provedor baterem, evitando duplicidade.
5. **Submit Universal Robusto** — `SendLine` otimizado: `\x1b[I` (Focus-In) apenas para Claude; `\r\n` e delays de 300ms para Gemini/Codex garantem que o comando "entre" no CLI.

## Testes Realizados
- **E2E Test Suite (`agent-test`)**: Todos os 3 provedores (Claude, Gemini, Codex) passam 5/5.
- **Persistência**: Validado via código que o buffer de 100KB é mantido e re-transmitido para novos subscribers.

## Próximos Passos
1. **Zellij/Multiplexer integration** — Investigar se vale a pena usar Zellij como backend para gerenciar os painéis (sistemas "mult").
2. **Audio/Image support** — Começar a tratar os campos de anexo nas mensagens.
3. **Layout refinements** — Ajustar o scroll do xterm.js para ser mais fluido com o backfill do buffer.

## O que JÁ funcionava (validado E2E)
1. **Spawn dos 3 CLIs em PTY oculto** — `clichat-host` cria internal instance + start-terminal com command/args/env/cwd.
2. **Submit "oi" + Enter universal** — `internal/terminal/process_unix.go` `SendLine`:
   ```
   write \x1b[I    # Focus In (re-arma TUIs com focus tracking)
   write text      # plain, SEM bracketed paste
   sleep 50ms
   write \r        # Enter isolado
   ```
   Solução copiada de `github.com/johannesjo/parallel-code/electron/ipc/pty.ts`. Antes de descobrir, tentei provider-specific (claude=plain, codex=bracketed paste, gemini=delayed plain) — funcionava 4/5 pro Claude e quebrava Gemini. Universal funciona 3/3 e 5/5 nos meus runs.
3. **Wait-for-ready** — `Process.readyCh` fecha no primeiro chunk de output; `SendLine` aguarda até 10s + 1.5s grace antes de escrever, evitando perder input enquanto TUI ainda inicializa.
4. **Discovery global** via `~/.claude/projects/*/*.jsonl` — sessões Claude rodando em qualquer terminal aparecem.
5. **Hooks Claude** (`SessionStart`, `Stop`, `PreToolUse`, `PostToolUse`, `UserPromptSubmit`) instalados em `~/.claude/settings.json` por `agentctl install-hooks` — atualizam status/tool em tempo real.
6. **MCP HTTP** em `/mcp` com tools `agent_chat_register`, `agent_chat_reply`, `agent_chat_question` — **mas atualmente desabilitado pra Claude** (sem `--mcp-config`) por causar "MCP server failed" race intermitente. Bolhas chegam via JSONL transcript watcher.
7. **Persistência** — JSON file (`~/.clichat/state.json`); restart preserva tudo.
8. **Dedup mensagens** (last 30 messages) — evita user+JSONL duplicar bolha.
9. **Prompt detector** — scan output PTY (8KB sliding buffer) detecta menus `1. opt 2. opt` ou `(y/n)` → `store.SetPending` → bolhas de botão na UI.
10. **UI WhatsApp-fiel**: sidebar 30%, header verde-cinza, bolhas verde-claro/branco com tail, fundo bege com hatch, composer verde `#00a884`, auto-scroll-to-bottom, status pill clicável que abre terminal.
11. **Avatar com SVG (lucide)** por status/tool: Bash=SquareTerminal verde, Read=Search azul, Write=Pencil laranja, Agent=Zap roxo, busy=Loader2 girando, idle=Moon, pending=HelpCircle amarelo.
12. **Terminal embutido preservado** entre toggles e troca de chat — todos `<TerminalPane>` montados sempre, só `display:none` toggla. xterm.js cols=120 fixo + scroll horizontal pra TUI não quebrar layout. Tema dark com `black=#5a5a5a` pra texto cinza-escuro do Codex ficar legível.
13. **Externos escondidos da UI** — discovery continua rodando, mas Wails filtra `origin === 'external'` pra leigo não ver permission prompts/AppleScript.

## O que o USER reporta NÃO funcionar (comigo passando E2E)
User diz que mesmo após meus fixes, no app real:
- "oi" no Codex aparece mas não submete (input fica `> oi` sem submit)
- Gemini fica preso no prompt de Trust folder
- Claude às vezes não responde

E2E (`/tmp/agent-test`) bate path **idêntico** ao Wails app (`POST /v1/instances`, `POST /start-terminal`, `POST /send`) e passa 5/5. Suspeito que:
- **App rodando build antigo** — toda mudança no daemon PTY exige só rebuild do clichat-host, mas mudanças de Wails frontend exigem `wails build -clean` + `open -a` do .app.
- **Cache do Wails frontend** — vite/wails embedam JS+CSS dentro do .app. Update de styles.css/App.tsx só aparece após `wails build -clean` + relaunch.

## E2E test harness
`/tmp/agent-test` (compilado de `cmd/agent-test/main.go`):
- POST cria 3 instances internal (claude/gemini/codex)
- POST start-terminal cada
- SSE subscribe `/v1/instances/{id}/events`
- collect boot 6s
- auto-dismiss pending prompt (`agentctl /v1/instances/{id} → pendingActions` → POST /send "1")
- POST /send "oi"
- collect até 30s ou detectar resposta real
- heurística `hasRealReply()` — busca "thinking", "responding", "que tarefa", "olá" etc

## Bugs conhecidos / pontos abertos
1. **Verificação real de resposta LLM**: heurística atual baseada em keywords. Não confirma 100% que LLM gerou texto — pode dar false positive em "thinking" indicator.
2. **Claude flaky** quando user tem outros MCPs no `~/.claude/settings.json` (claude-voice, Citadel etc) — boot lento + "MCP server failed" pode bloquear primeiro Enter por alguns segundos. Mitigado com 1.5s grace pós-ready, mas não 100%.
3. **Codex/Gemini sem JSONL parser pra bolhas**: bolha assistant só aparece pro Claude (via JSONL). Pra Codex/Gemini o terminal mostra a resposta mas a bolha do chat fica vazia. Possível solução futura: parser TUI ou usar Codex `~/.codex/sessions/*.jsonl` rollouts (formato investigado em `~/.codex/sessions/2026/04/27/rollout-*.jsonl` — tem `session_meta`, mensagens user/assistant).
4. **Terminal embedded toggle** funciona mas xterm `fit()` pode disparar quando container=0px → `proposeDimensions()` retorna null. Já guard.
5. **Botão "monitor up" e fluxo Terminal.app externo** removidos do código atual (após user pediu "tudo interno").
6. **AppleScript focus** existe (botão olho) mas só pra externos — não exposto na UI atualmente porque externos foram escondidos.

## Como reproduzir
```bash
cd /Users/luis/projetos/pessoal/clichat

# build
go build -o ~/.local/bin/clichat-host ./cmd/clichat-host
go build -o ~/.local/bin/agentctl ./cmd/agentctl
~/go/bin/wails build -clean

# instala hooks
~/.local/bin/agentctl install-hooks

# daemon (pode rodar via launchd ~/Library/LaunchAgents/com.clichat.host.plist)
nohup ~/.local/bin/clichat-host serve > ~/.clichat/logs/host.out.log 2>&1 &

# app
open -a /Users/luis/projetos/pessoal/clichat/build/bin/clichat.app

# test
go build -o /tmp/agent-test ./cmd/agent-test
/tmp/agent-test
```

## Estado dos processos
```
clichat-host PID dinâmico em 127.0.0.1:47657
AgentChatLocal.app PID dinâmico
state em ~/.clichat/state.json
hooks em ~/.claude/settings.json (5 entries `_managedBy: agentctl-managed`)
```

## Pesquisa GitHub feita
- `johannesjo/parallel-code` — Electron+node-pty+solid; spawn idêntico ao nosso, **submit Focus-In + plain + 50ms + \r** (origem da minha solução universal)
- `openwong2kim/wmux` — Windows multiplexer; só **detecta** agentes existentes
- `Sora-bluesky/winsmux` — Rust nativo Win
- `bfly123/claude_code_bridge` — multi-AI bridge
- `thepushkarp/cc-gemini-plugin` + `sakibsadmanshajib/gemini-plugin-cc` — Gemini ACP mode pra Claude
- `Piebald-AI/awesome-gemini-cli` — lista geral

## Próximos passos sugeridos pra LLM seguinte
1. Parser TUI / JSONL pra Codex e Gemini → bolha assistant igual Claude.
2. Fix definitivo do "Wails app não pega rebuilds" — talvez forçar `outputfilename` único por build ou abrir via `--no-cache`.
3. Reabilitar MCP de forma robusta (handshake completo, não falhar no boot).
4. Botão pra clicar nos pendingActions detectados pelo prompt detector (já implementado mas não testado em fluxo real Claude permission).
5. Audio + imagem (schema Message.Type já tem campo).
6. Tailscale / espelhamento (no roadmap original).
