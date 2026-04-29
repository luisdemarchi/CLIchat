# Arquitetura

## Direcao

O app deve ser um mensageiro local para agentes de terminal. Cada chat representa uma sessao persistente de um provedor: Claude, Gemini ou Codex.

```text
React UI (Wails WebView)
  -> Go app bindings
  -> session registry
  -> agent-host HTTP/SSE client
  -> agent-host
  -> PTY-owned CLI process
  -> event stream back to UI
```

## Chat versus Terminal

O chat nao deve mostrar transcript bruto de terminal. A UI principal renderiza apenas baloes de conversa: texto agora, audio/imagem depois. Spinners, prompts, menus TUI, comandos e logs ficam fora do chat.

O `agent-host`, nao o app grafico, deve ser dono do PTY. Isso evita depender de AppleScript, Terminal.app, iTerm2 ou tmux para recuperar uma sessao, e evita atribuir ao app grafico os acessos de filesystem que o LLM fizer. Qualquer terminal externo se conecta ao PTY pelo `agentctl`, no mesmo modelo mental do `rtk`: um binario pequeno entra entre a ferramenta e o terminal para interceptar/rotear IO.

Fluxo desejado:

1. Usuario cria chat no app.
2. App pede ao `agent-host` para iniciar `claude`, `gemini` ou `codex` em um PTY.
3. UI envia texto via HTTP ao `agent-host` e renderiza stdout/stderr filtrado como mensagens/eventos.
4. Se o usuario pedir terminal externo, o terminal roda `agentctl attach <session-id>`.
5. `agentctl` conecta no servidor local do app e espelha/controla o mesmo PTY.

## Equivalencias vindas do claude-voice

| claude-voice | clichat |
| --- | --- |
| `InstanceRegistry` | `internal/session.Registry` |
| `InstanceStatus` | `session.Status` |
| `last_message` | `Session.LastMessage` |
| `current_tool` | `Session.CurrentTool` |
| menubar popover | React chat list |
| AppleScript/tmux sender | PTY runner + `agentctl attach` |
| MCP server Claude-only | provider runners Claude/Gemini/Codex |

## Espelhamento via Tailscale

Primeira fase: servidor HTTP/WebSocket local escutando em `127.0.0.1` para `agentctl`.

Segunda fase: opcao "Compartilhar na rede local" expondo a mesma API em interface privada. Com Tailscale ativo, outro dispositivo na Tailnet acessa a UI remota.

Terceira fase: avaliar `tailscale.com/tsnet` no core Go para login/serve embutido sem exigir configuracao manual do usuario.

## Proximos modulos

- `internal/terminal`: PTY, resize, attach/detach e multiplexacao.
- `internal/provider`: comandos e argumentos por LLM.
- `internal/store`: persistencia local em SQLite.
- `internal/mirror`: HTTP/WebSocket para UI remota e `agentctl`.
- `internal/events`: stream unico para UI desktop e clientes remotos.
