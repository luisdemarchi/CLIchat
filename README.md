# Agent Chat Local

App desktop pessoal para conversar com agentes locais de IA em um modelo parecido com mensageiro. A primeira meta e substituir o fluxo menubar/voz do `claude-voice` por um app com janela normal, icone no dock, conversas persistentes e provedores selecionaveis.

## Stack escolhida

- Go 1.24 para o core local, processos, PTY, WebSocket e futuro espelhamento via Tailscale.
- Wails 2 para empacotar app desktop em macOS, Windows e Linux usando WebView nativo.
- React + Vite para a interface de chat.

## Escopo inicial

- Lista de conversas com foto/status visual.
- Tags de provedor: Claude, Gemini e Codex.
- Estado de sessao inspirado no `claude-voice`: `idle`, `busy`, `waiting`, `offline`, ultima mensagem e ferramenta atual.
- Chat limpo: baloes de conversa `text`, com estrutura preparada para `audio` e `image`.
- Terminal bruto separado: `agentctl attach <session-id>` abre/conecta qualquer terminal externo ao PTY oculto da conversa.
- Modulo separado para espelhamento local/Tailscale futuro.

## Comandos

Neste Mac, Go foi instalado via Homebrew e Wails ficou em `/Users/luis/go/bin/wails`.

Antes de usar o app, rode o host de terminais em um terminal normal:

```bash
agent-host serve
```

```bash
cd /Users/luis/projetos/pessoal/agent-chat-local
pnpm --dir frontend install
/Users/luis/go/bin/wails dev
```

Para gerar binario:

```bash
/Users/luis/go/bin/wails build
```

Para conferir o terminal que esta por tras de uma conversa:

```bash
agentctl attach <session-id>
```

O `agent-host` escuta em `127.0.0.1:47657` para o app e em `127.0.0.1:47656` para anexos de terminal. O terminal externo nao cria outro LLM; ele anexa no mesmo PTY que o `agent-host` abriu.

## Reuso do `claude-voice`

O projeto atual sera usado como referencia, nao como dependencia direta. As partes que valem reaproveitar conceitualmente sao:

- `claude_voice/registry.py`: modelo de sessoes, status e selecao.
- `claude_voice/sender.py`: estrategias antigas para injetar texto no terminal, substituidas aqui por uma arquitetura PTY-owned.
- `claude_voice/providers/claude`: hooks e eventos de ciclo de vida do Claude.

O app novo deve ser dono das sessoes de terminal. Quando o usuario quiser terminal externo, o terminal deve se conectar ao app com `agentctl attach`, em vez de tentar mover o processo para fora da janela.
