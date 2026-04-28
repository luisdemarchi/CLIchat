# Handoff - Agent Chat Local

Data: 2026-04-28

## O que o usuario quer

O objetivo e transformar a ideia do `claude-voice` em um app desktop de chat
local, parecido com WhatsApp, para macOS, Windows e Linux.

O comportamento desejado:

- Cada novo chat abre uma sessao real de terminal para o LLM escolhido.
- Provedores esperados: Claude, Gemini e Codex.
- O chat deve mostrar apenas conversa limpa entre usuario e IA, em baloes:
  texto agora, audio/imagem no futuro.
- Tudo que roda por tras deve continuar disponivel para conferencia em um
  terminal real.
- O terminal pode ficar oculto no app, mas o usuario deve poder abrir o mesmo
  terminal externamente quando quiser analisar ou interagir diretamente.
- Quando o CLI pedir confirmacao/permissao, o app deve mostrar botoes no chat
  em vez de depender do usuario ler a TUI.
- A captura de resposta nao deve ser scraping da tela do terminal. O modelo
  correto e o do `claude-voice`: MCP/ferramentas estruturadas para a IA enviar
  ao app o texto exato que deve aparecer.

## O que foi feito

- Criado projeto novo em `/Users/luis/projetos/pessoal/agent-chat-local`.
- Implementado app Go/Wails com frontend React.
- Criado `agent-host`, processo separado do app grafico, responsavel por:
  - manter PTYs reais dos CLIs;
  - expor HTTP local em `127.0.0.1:47657`;
  - expor attach TCP em `127.0.0.1:47656`;
  - permitir anexar um terminal externo ao mesmo PTY.
- Criado `agentctl attach <session-id>` para abrir/conectar um terminal externo
  ao PTY da conversa.
- O app nao deve mais iniciar processos LLM diretamente dentro do `.app`; isso
  reduz prompts de permissao do macOS.
- Adicionado botao para abrir terminal externo real no macOS.
- Adicionado painel de terminal embutido com `xterm.js`, mas ele nao deve ser
  considerado fonte confiavel de conversa.
- Implementado MCP HTTP basico no `agent-host` em `/mcp`.
- Ao iniciar Claude, o app passa:
  - `--mcp-config` apontando para `http://127.0.0.1:47657/mcp`;
  - `--allowedTools` para `agent_chat_reply` e `agent_chat_question`;
  - `--append-system-prompt` instruindo Claude a chamar `agent_chat_reply` para
    toda resposta final ao usuario.
- O app recebe eventos MCP estruturados via SSE e cria bolhas de chat com o
  texto recebido.
- Adicionado fallback inspirado no `claude-voice`: watcher do JSONL do Claude
  em `~/.claude/projects/.../*.jsonl`, lendo apenas blocos assistant/text.
- Feitos commits incrementais ate `28233c9 Use MCP bridge for structured Claude chat replies`.

## O que nao ficou resolvido

- Ainda nao foi validado end-to-end com uma conversa nova confirmando que Claude
  chama `agent_chat_reply` sempre e que a bolha fica 100% limpa.
- O MCP implementado e minimo. Pode precisar de ajustes finos para o protocolo
  exato aceito por Claude Code em todos os cenarios.
- Gemini e Codex ainda nao receberam uma estrategia equivalente de MCP/tool
  estruturado. O foco ate agora foi Claude.
- O terminal embutido ainda nao tem qualidade de terminal de produto final.
  A direcao preferida passou a ser abrir terminal externo real para interacao
  seria.
- Confirmacoes/permissoes ainda precisam ser conectadas ao fluxo estruturado
  completo. Existe `agent_chat_question`, mas precisa validar e melhorar a UX.
- Nao foi implementado audio/imagem no chat.
- Nao foi implementado espelhamento local/Tailscale.
- Nao foi feita persistencia de historico de chats.
- Nao foi feita empacotamento cross-platform completo.

## Aprendizado importante

O erro das primeiras tentativas foi tentar extrair texto da TUI do Claude. Isso
gera lixo como `Processing`, dicas, tokens, box drawing, shortcuts e mensagens
de status. O `claude-voice` funcionava melhor porque nao dependia da TUI:

- Claude chamava ferramentas MCP (`claude_voice_speak`, `claude_voice_notify`,
  `claude_voice_await`) com texto estruturado.
- O app mostrava ou falava exatamente os argumentos dessas ferramentas.
- Como fallback, o projeto lia o transcript JSONL oficial do Claude.

Para este app, a fonte de verdade da conversa deve ser:

1. MCP/tool call estruturada (`agent_chat_reply`, `agent_chat_question`);
2. fallback por JSONL oficial do Claude;
3. nunca scraping da tela do terminal para criar bolha.

## Proximos passos recomendados

1. Testar uma conversa Claude nova do zero e verificar se o MCP aparece como
   aprovado/disponivel dentro do Claude Code.
2. Se houver prompt de permissao para MCP, configurar permissao local/projeto
   de forma controlada, sem mexer em segredos globais.
3. Validar que `agent_chat_reply` gera exatamente uma bolha por resposta.
4. Remover ou reduzir o parser antigo de TUI depois que MCP/JSONL estiverem
   confiaveis.
5. Criar equivalente estruturado para Gemini e Codex.
6. Melhorar o botao de terminal externo para escolher Terminal.app/iTerm/WezTerm
   quando existirem.
7. Persistir chats e sessoes.
8. Planejar Tailscale/espelhamento local como camada separada.
