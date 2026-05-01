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
│  CLIchat desktop    │ ────────────▶│       clichat-host         │
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
- **`clichat-host`** (`cmd/clichat-host`) — daemon em `127.0.0.1:47657` (HTTP) e
  `:47656` (TCP attach). Dono de todos os PTYs e do estado persistente
  em `~/.clichat/state.json`.
- **`agentctl`** (`cmd/agentctl`) — CLI usado pelos hooks do Claude Code
  (`agentctl hook session-start|stop|pre-tool-use|post-tool-use|user-prompt-submit`).

## Pré-requisitos

Instale uma vez no computador:

| Ferramenta | Por quê | Instalação |
|------------|---------|------------|
| Go ≥ 1.25 | Compilar daemon + app Wails | `brew install go` (mac) / `sudo apt install golang` (Linux) |
| Node ≥ 20 + pnpm | Compilar frontend React | `brew install node && npm i -g pnpm` |
| Wails v2.12 CLI | Empacotar app desktop | `go install github.com/wailsapp/wails/v2/cmd/wails@v2.12.0` (o instalador roda automaticamente se faltar) |
| Xcode CLT (macOS) | Cgo / WebKit | `xcode-select --install` |
| `claude` CLI | Provedor Claude Code | https://docs.claude.com/claude-code |
| `codex` CLI (opcional) | Provedor OpenAI Codex | `npm i -g @openai/codex` |

Garanta que `~/.local/bin` está no seu `$PATH`:

```bash
echo 'export PATH="$HOME/.local/bin:$PATH"' >> ~/.zshrc && source ~/.zshrc
```

## Instalar

Um comando:

```bash
git clone https://github.com/luisdemarchi/CLIchat.git
cd CLIchat
./scripts/install.sh
```

O instalador faz, em ordem:

1. **Compila `clichat-host` e `agentctl`** em `~/.local/bin/`.
2. **Instala os hooks do Claude Code** em `~/.claude/settings.json`
   (`SessionStart`, `Stop`, `PreToolUse`, `PostToolUse`, `UserPromptSubmit`)
   pra que toda sessão Claude — mesmo as abertas fora do app — reporte
   status pra CLIchat.
3. **Compila o app desktop Wails** e copia pra `/Applications/CLIchat.app`
   (macOS) ou `~/.local/share/clichat/CLIchat` (Linux).
4. **Registra o daemon como serviço** pra subir no login:
   - macOS: `~/Library/LaunchAgents/com.clichat.host.plist` (launchd)
   - Linux: `~/.config/systemd/user/clichat.service` (systemd user)

Re-rodar `./scripts/install.sh` é idempotente.

## Como usar

### 1. Abrir o app

- macOS: abra `Aplicativos` e dê dois cliques em **CLIchat**.
- Linux: rode `~/.local/share/clichat/CLIchat`.

Na primeira abertura o daemon pode levar alguns segundos pra registrar.
O header fica *"clichat-host online."* assim que estiver pronto.

### 2. Iniciar uma nova conversa

Clique no ícone **`+`** no topo da sidebar e escolha o provedor (Claude
ou Codex). O CLIchat:

- cria uma sessão interna,
- spawna o CLI escolhido num PTY oculto,
- aguarda a TUI inicializar (~1,5s de grace),
- mostra o chat no topo da sidebar com o logo do provedor no avatar.

### 3. Conversar

Digite no rodapé e aperte Enter. O CLIchat envia o texto pro PTY com a
sequência Focus-In + texto plain + `\r` (funciona em Claude, Codex e
Gemini). O balão aparece à direita; a resposta entra "digitando" conforme
o LLM responde.

Enquanto o agente trabalha, a **pílula de status** acima do rodapé
mostra a ferramenta atual (Bash / Read / Write / Web / Agent /
pensando…). Clique na pílula pra abrir/fechar o terminal embutido e ver
a TUI bruta ao vivo.

### 4. Acompanhar sessões Claude abertas fora do app

Abra outro terminal e rode `claude` em qualquer projeto. O hook
`SessionStart` avisa o CLIchat: a sessão aparece como chat na sidebar.
O watcher de transcript espelha cada resposta no balão. Você pode
continuar trabalhando no terminal — nesse caso o CLIchat é só um espelho
de leitura.

### 5. Fechar e reabrir sem perder contexto

- O daemon (`clichat-host`) continua rodando em background, então os PTYs
  ficam vivos quando você fecha o app.
- Se você matar o daemon de fato, na próxima abertura o CLIchat
  re-spawna cada chat interno com `claude --resume <session-id>` (Claude)
  ou `codex resume --last` (Codex), então a conversa continua.

### 6. Atalhos no terminal

```bash
agentctl list            # lista as conversas que o daemon conhece
agentctl install-hooks   # reescreve os hooks do Claude Code (idempotente)
agentctl uninstall-hooks # remove só os hooks
clichat-host serve         # roda o daemon manualmente (debug)
```

Estado em `~/.clichat/state.json`. Logs em `~/.clichat/logs/`.

## Troubleshooting

- **Pílula de status não aparece** → `~/.claude/settings.json` está sem
  os hooks gerenciados. Rode `agentctl install-hooks`.
- **Sessão Codex com balões vazios** → o Codex grava em
  `~/.codex/sessions`; o CLIchat só importa rollouts com até ~5 min de
  modificação. Mande mais um turno e ele pega automaticamente.
- **"MCP server failed" no boot do Claude** → o Claude está em corrida com
  o daemon. Confirme em `~/.claude/settings.json` que o bloco MCP aponta
  pra `http://127.0.0.1:47657/mcp`. O daemon precisa estar rodando antes
  do Claude — confira `launchctl list | grep clichat` (macOS) ou
  `systemctl --user status clichat` (Linux).
- **Ícone do app é o "W" padrão do Wails** → rode `wails build -clean`
  após dar pull do `build/appicon.png` mais novo.

## Desenvolver

```bash
go build ./...                 # todos os binários Go
cd frontend && pnpm install && cd ..
~/go/bin/wails dev             # hot-reload do app desktop
```

Em outro terminal:

```bash
~/.local/bin/clichat-host serve  # daemon (porta 47657)
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
