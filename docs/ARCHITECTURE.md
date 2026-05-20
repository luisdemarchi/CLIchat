# Arquitetura

## Direcao

O app deve ser um mensageiro local para agentes de terminal. Cada chat representa uma sessao persistente de um provedor: Claude, Gemini ou Codex.

```text
React UI (Wails WebView)
  -> Go app bindings
  -> session registry
  -> clichat-host HTTP/SSE client
  -> clichat-host
  -> per-chat memory (SQLite/FTS5)
  -> PTY-owned CLI process
  -> event stream back to UI
```

## Chat versus Terminal

O chat nao deve mostrar transcript bruto de terminal. A UI principal renderiza apenas baloes de conversa: texto agora, audio/imagem depois. Spinners, prompts, menus TUI, comandos e logs ficam fora do chat.

O `clichat-host`, nao o app grafico, deve ser dono do PTY. Isso evita depender de AppleScript, Terminal.app, iTerm2 ou tmux para recuperar uma sessao, e evita atribuir ao app grafico os acessos de filesystem que o LLM fizer. Qualquer terminal externo se conecta ao PTY pelo `clichat`, no mesmo modelo mental do `rtk`: um binario pequeno entra entre a ferramenta e o terminal para interceptar/rotear IO.

Fluxo desejado:

1. Usuario cria chat no app.
2. App pede ao `clichat-host` para iniciar `claude`, `gemini` ou `codex` em um PTY.
3. UI envia texto via HTTP ao `clichat-host` e renderiza stdout/stderr filtrado como mensagens/eventos.
4. O `clichat-host` atualiza `state.json` e `memory.sqlite3`: mensagens,
   resumo compacto, topico/titulo atual e indice FTS por chat.
5. Se o terminal fechar ou o usuario trocar Claude/Codex/Gemini, o app abre um
   PTY novo para o mesmo chat e injeta a memoria desse chat como handoff.
6. Se o usuario pedir terminal externo, o terminal roda `clichat attach <session-id>`.
7. `clichat` conecta no servidor local do app e espelha/controla o mesmo PTY.

## Memoria por chat

A memoria e local, por conversa, e fica em `~/.clichat/memory.sqlite3`.
Ela segue o papel pratico que interessa do `claude-mem`: WAL para escrita
segura, FTS5 para recuperacao textual e resumo compacto para reidratar um novo
processo de LLM. Nao ha memoria global injetada em outros chats; cada handoff
usa apenas o historico daquele `instance_id`.

O titulo visivel e atualizado em duas fontes:

1. localmente, quando uma mensagem do usuario entra, usando heuristica
   deterministica de assunto atual;
2. via MCP `agent_chat_set_topic`, quando o provider sabe declarar melhor o
   foco em andamento.

## Equivalencias vindas do claude-voice

| claude-voice | clichat |
| --- | --- |
| `InstanceRegistry` | `internal/session.Registry` |
| `InstanceStatus` | `session.Status` |
| `last_message` | `Session.LastMessage` |
| `current_tool` | `Session.CurrentTool` |
| menubar popover | React chat list |
| AppleScript/tmux sender | PTY runner + `clichat attach` |
| MCP server Claude-only | provider runners Claude/Gemini/Codex |

## Espelhamento via Tailscale

Primeira fase: servidor HTTP/WebSocket local escutando em `127.0.0.1` para `clichat`.

Segunda fase: opcao "Compartilhar na rede local" expondo a mesma API em interface privada. Com Tailscale ativo, outro dispositivo na Tailnet acessa a UI remota.

Terceira fase: avaliar `tailscale.com/tsnet` no core Go para login/serve embutido sem exigir configuracao manual do usuario.

## Proximos modulos

- `internal/terminal`: PTY, resize, attach/detach e multiplexacao.
- `internal/provider`: comandos e argumentos por LLM.
- `internal/agent`: estado JSON, topico/titulo inteligente e watchers de transcripts.
- `internal/memory`: persistencia local em SQLite/FTS5 por chat.
- `internal/mirror`: HTTP/WebSocket para UI remota e `clichat`.
- `internal/events`: stream unico para UI desktop e clientes remotos.
