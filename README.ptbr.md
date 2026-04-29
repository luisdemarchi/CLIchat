# CLIchat

> App desktop estilo WhatsApp para os CLIs do Claude Code e Codex. Cada conversa tem seu próprio terminal real, oculto por padrão.

[🇺🇸 Read in English](./README.md)

CLIchat transforma os CLIs do Claude Code e Codex em uma interface de
mensageiro: uma conversa por linha, PTYs reais rodando em background, pílulas
de status pra cada ferramenta que o agente dispara (Bash / Read / Write /
Web / …), renderização de markdown, animação de digitação, contadores de
não lidos e uma sidebar que mostra exatamente o que cada agente está
fazendo **agora**.

Ele detecta toda sessão Claude rodando na sua máquina — inclusive as que você
abriu em outro terminal fora do app — e mostra cada uma como um chat.
Persiste o histórico entre reinícios e reconecta com `claude --resume`,
preservando o contexto.

Feito no Brasil 🇧🇷 por [@luisdemarchi](https://github.com/luisdemarchi).

---

## O que faz

- **UI WhatsApp Web** — sidebar com lista de conversas, header, balões de
  mensagem com markdown (tabelas, código, listas, negrito), contadores de
  não lidas e animação de "digitando" pra que respostas longas não pareçam
  paredão de texto.
- **CLI real dentro de cada chat** — cada conversa abre Claude/Codex/Gemini
  num PTY oculto. xterm.js fica embutido sob as bolhas pra você ver a TUI
  bruta quando quiser.
- **Logos reais no avatar** — badges de Claude/Codex/Gemini via SVGs do
  [`simple-icons`](https://simple-icons.org).
- **Status por ferramenta** — trabalhando ⚙️, Bash 💻, Read 🔍, Write 📝,
  Agent ⚡, Web 🌐, aguardando ❓, ocioso 💤. Clique na pílula pra abrir o
  terminal embutido.
- **Tópico como nome** — Claude chama uma MCP tool `agent_chat_set_topic`
  no começo de cada tarefa; o nome do chat passa a ser a tarefa atual
  (`"analisando card S3-15693"`).
- **Descoberta automática** — toda rollout do Claude
  (`~/.claude/projects/*/*.jsonl`), Codex (`~/.codex/sessions/...`) e Gemini
  (`~/.gemini/tmp/.../session-*.jsonl`) é monitorada. Sessões abertas fora
  do app são linkadas ao chat interno correspondente pelo CWD + timestamp.
- **Reconectar ao reabrir** — fechar e abrir o app re-spawna cada chat com
  `claude --resume <session-id>` (Claude) ou `codex resume --last` (Codex).
  Conversa continua.
- **Permissões viram botões** — quando uma TUI mostra
  `Choose: ❯ 1) yes ❯ 2) no` (ou `(y/n)`), o detector reconhece e renderiza
  as opções como botões dentro do chat.

## Arquitetura

```
┌─────────────────────┐  HTTP / SSE  ┌──────────────────────────┐
│  CLIchat desktop    │ ────────────▶│       agent-host         │
│  (Wails Go + React) │ ◀────────────│   (daemon, fonte única   │
└─────────────────────┘              │    de verdade)           │
                                     │                          │
                                     │  • PTY manager           │
                                     │  • State JSON            │
                                     │  • MCP HTTP /mcp         │
                                     │  • Discovery + watcher   │
                                     │  • Prompt detector       │
                                     └────────────┬─────────────┘
                                                  ▲
                       ~/.claude/settings.json    │ POST /v1/instances/...
                       hooks → agentctl hook *    │
```

Três binários:

- **`clichat`** (`main.go` + `internal/app`) — app desktop Wails. Cliente
  thin.
- **`agent-host`** (`cmd/agent-host`) — daemon em `127.0.0.1:47657` (HTTP) e
  `:47656` (TCP attach). Dono de todos os PTYs e do estado persistente
  em `~/.clichat/state.json`.
- **`agentctl`** (`cmd/agentctl`) — CLI usado pelos hooks do Claude Code
  (`agentctl hook session-start|stop|pre-tool-use|post-tool-use|user-prompt-submit`).

## Instalar (terminal, um comando)

```bash
git clone https://github.com/luisdemarchi/CLIchat.git
cd CLIchat
./scripts/install.sh
```

O script:

1. compila `agent-host` e `agentctl` em `~/.local/bin/`,
2. instala os hooks no `~/.claude/settings.json`,
3. compila o app Wails (`CLIchat.app`) e copia pra `/Applications/`
   no macOS ou `~/.local/share/clichat/` no Linux,
4. registra o daemon no launchd (macOS) ou systemd-user (Linux) pra
   subir junto com o login.

Depois, abra `CLIchat.app` e clique em `+ Novo chat` → Claude/Codex.

## Desenvolver

```bash
go build ./...                 # todos os binários Go
cd frontend && pnpm install && cd ..
~/go/bin/wails dev             # hot-reload do app desktop
```

Em outro terminal:

```bash
~/.local/bin/agent-host serve  # daemon (porta 47657)
```

Smoke test:

```bash
go build -o /tmp/agent-test ./cmd/agent-test
/tmp/agent-test
```

## Desinstalar

```bash
./scripts/uninstall.sh                 # remove binários, hooks, autostart
KEEP_STATE=0 ./scripts/uninstall.sh    # apaga também ~/.clichat
```

## Agradecimentos

- [`johannesjo/parallel-code`](https://github.com/johannesjo/parallel-code) —
  origem do submit universal (Focus-In + texto plain + `\r`).
- [`claude-voice`](https://github.com/luisdemarchi/claude-voice) —
  multiplexador de voz anterior; padrões de registry / hook / topic
  vieram dele.
- [`simple-icons`](https://simple-icons.org), [`react-markdown`](https://github.com/remarkjs/react-markdown),
  [`xterm.js`](https://xtermjs.org), [`Wails`](https://wails.io).

## Autor

**Luís De Marchi** — [@luisdemarchi](https://github.com/luisdemarchi) ·
São Paulo, Brasil 🇧🇷 · [luisdemarchi.com.br](https://luisdemarchi.com.br)

## Licença

MIT — veja [LICENSE](./LICENSE).
