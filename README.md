# CLIchat

> WhatsApp-style desktop chat for the Claude Code and Codex CLIs. Each thread owns a real terminal, hidden by default.

[🇧🇷 Leia em português](./README.ptbr.md)

CLIchat turns the Claude Code and OpenAI Codex command-line tools into a clean
messenger UI: one conversation per chat row, real PTYs running in the
background, status pills for every tool the agent fires (Bash / Read / Write /
Web / …), markdown rendering, type-out animation, unread badges, and a sidebar
that shows what each agent is doing **right now**.

It detects every Claude session running on your machine — even sessions you
opened in another terminal outside the app — and surfaces them as chats. It
preserves transcripts across restarts and reattaches with `claude --resume`
so context is never lost.

Built in Brazil 🇧🇷 by [@luisdemarchi](https://github.com/luisdemarchi).

---

## Features

- **WhatsApp-style desktop UI** — sidebar with chat list, header, message
  bubbles with markdown (tables, code, lists, bold), unread counters, and
  per-chat typing animation so long answers stop feeling like a wall of text.
- **Real CLI inside every chat** — each thread spawns Claude / Codex / Gemini
  in a hidden PTY. xterm.js is embedded under the messages so you can watch
  the raw TUI when you want.
- **Brand logos in the avatar** — Claude / Codex / Gemini badges are real SVG
  logos via [`simple-icons`](https://simple-icons.org).
- **Status by tool** — busy ⚙️, Bash 💻, Read 🔍, Write 📝, Agent ⚡, Web 🌐,
  pending ❓, idle 💤. Click the status pill to open the embedded terminal.
- **Topic in the chat name** — Claude calls a custom MCP tool
  `agent_chat_set_topic` whenever it starts a new task; the chat name reflects
  what the agent is doing right now (`"analisando card S3-15693"`).
- **Discovery** — every Claude rollout (`~/.claude/projects/*/*.jsonl`),
  Codex rollout (`~/.codex/sessions/...`) and Gemini transcript
  (`~/.gemini/tmp/.../session-*.jsonl`) is monitored. Sessions you start
  outside the app are linked back to existing internal chats by working
  directory + timestamp.
- **Auto-reconnect** — close the app and reopen it: every chat re-spawns its
  PTY with `claude --resume <session-id>` (Claude) or `codex resume --last`
  (Codex), so the conversation carries on.
- **Permission prompts as buttons** — when a TUI shows
  `Choose: ❯ 1) yes ❯ 2) no` (or `(y/n)`), CLIchat detects it and renders
  the options as inline buttons in the chat.

## Architecture

```
┌─────────────────────┐  HTTP / SSE  ┌──────────────────────────┐
│  CLIchat desktop    │ ────────────▶│       agent-host         │
│  (Wails Go + React) │ ◀────────────│   (daemon, single source │
└─────────────────────┘              │    of truth)             │
                                     │                          │
                                     │  • PTY manager           │
                                     │  • JSON state file       │
                                     │  • MCP HTTP /mcp         │
                                     │  • Discovery + watcher   │
                                     │  • Prompt detector       │
                                     └────────────┬─────────────┘
                                                  ▲
                       ~/.claude/settings.json    │ POST /v1/instances/...
                       hooks → agentctl hook *    │
```

Three binaries:

- **`clichat`** (`main.go` + `internal/app`) — Wails desktop app. Thin client.
- **`agent-host`** (`cmd/agent-host`) — daemon on `127.0.0.1:47657` (HTTP) and
  `:47656` (TCP attach). Owns every PTY and the persistent state in
  `~/.clichat/state.json`.
- **`agentctl`** (`cmd/agentctl`) — small CLI used by Claude Code hooks
  (`agentctl hook session-start|stop|pre-tool-use|post-tool-use|user-prompt-submit`).

## Install (one shot, terminal)

```bash
git clone https://github.com/luisdemarchi/CLIchat.git
cd CLIchat
./scripts/install.sh
```

The installer:

1. compiles `agent-host` and `agentctl` into `~/.local/bin/`,
2. installs Claude Code hooks in `~/.claude/settings.json`,
3. builds the Wails app (`CLIchat.app`) and copies it to `/Applications/`
   on macOS or `~/.local/share/clichat/` on Linux,
4. registers the daemon on launchd (macOS) or systemd-user (Linux) so it
   starts at login.

After install, open `CLIchat.app` and click `+ Novo chat` → Claude/Codex.

## Develop

```bash
go build ./...                 # all Go binaries
cd frontend && pnpm install && cd ..
~/go/bin/wails dev             # hot-reload desktop app
```

In another terminal:

```bash
~/.local/bin/agent-host serve  # daemon (port 47657)
```

Run the smoke test:

```bash
go build -o /tmp/agent-test ./cmd/agent-test
/tmp/agent-test
```

## Uninstall

```bash
./scripts/uninstall.sh                 # removes binaries, hooks, autostart
KEEP_STATE=0 ./scripts/uninstall.sh    # also wipes ~/.clichat
```

## Acknowledgements

- [`johannesjo/parallel-code`](https://github.com/johannesjo/parallel-code) —
  inspired the universal Focus-In + plain text + `\r` submit sequence.
- [`claude-voice`](https://github.com/luisdemarchi/claude-voice) — earlier
  voice multiplexer; the registry / hook / topic patterns were borrowed.
- [`simple-icons`](https://simple-icons.org), [`react-markdown`](https://github.com/remarkjs/react-markdown),
  [`xterm.js`](https://xtermjs.org), [`Wails`](https://wails.io).

## Author

**Luís De Marchi** — [@luisdemarchi](https://github.com/luisdemarchi) ·
São Paulo, Brasil 🇧🇷 · [luisdemarchi.com.br](https://luisdemarchi.com.br)

## License

MIT — see [LICENSE](./LICENSE).
