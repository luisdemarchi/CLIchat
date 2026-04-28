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
- Comando planejado para abrir/acompanhar terminal externo: `agentctl attach <session-id>`.
- Modulo separado para espelhamento local/Tailscale futuro.

## Comandos

Neste Mac, Go foi instalado via Homebrew e Wails ficou em `/Users/luis/go/bin/wails`.

```bash
cd /Users/luis/projetos/pessoal/agent-chat-local
pnpm --dir frontend install
/Users/luis/go/bin/wails dev
```

Para gerar binario:

```bash
/Users/luis/go/bin/wails build
```

## Reuso do `claude-voice`

O projeto atual sera usado como referencia, nao como dependencia direta. As partes que valem reaproveitar conceitualmente sao:

- `claude_voice/registry.py`: modelo de sessoes, status e selecao.
- `claude_voice/sender.py`: estrategias antigas para injetar texto no terminal, substituidas aqui por uma arquitetura PTY-owned.
- `claude_voice/providers/claude`: hooks e eventos de ciclo de vida do Claude.

O app novo deve ser dono das sessoes de terminal. Quando o usuario quiser terminal externo, o terminal deve se conectar ao app com `agentctl attach`, em vez de tentar mover o processo para fora da janela.
