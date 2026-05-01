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
│  CLIchat desktop    │ ────────────▶│       clichat-host         │
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
- **`clichat-host`** (`cmd/clichat-host`) — daemon on `127.0.0.1:47657` (HTTP) and
  `:47656` (TCP attach). Owns every PTY and the persistent state in
  `~/.clichat/state.json`.
- **`agentctl`** (`cmd/agentctl`) — small CLI used by Claude Code hooks
  (`agentctl hook session-start|stop|pre-tool-use|post-tool-use|user-prompt-submit`).

## Prerequisites

Install these once on the host machine:

| Tool | Why | Install |
|------|-----|---------|
| Go ≥ 1.25 | Build the daemon and Wails app | `brew install go` (mac) / `sudo apt install golang` (Linux) |
| Node ≥ 20 + pnpm | Build the React frontend | `brew install node && npm i -g pnpm` |
| Wails v2.12 CLI | Bundle the desktop app | `go install github.com/wailsapp/wails/v2/cmd/wails@v2.12.0` (the installer auto-runs this if missing) |
| Xcode CLT (macOS) | Cgo / WebKit | `xcode-select --install` |
| `claude` CLI | Claude Code provider | https://docs.claude.com/claude-code |
| `codex` CLI (optional) | OpenAI Codex provider | `npm i -g @openai/codex` |

Make sure `~/.local/bin` is on your `$PATH`:

```bash
echo 'export PATH="$HOME/.local/bin:$PATH"' >> ~/.zshrc && source ~/.zshrc
```

## Install

One command:

```bash
git clone https://github.com/luisdemarchi/CLIchat.git
cd CLIchat
./scripts/install.sh
```

The installer does, in order:

1. **Compiles `clichat-host` and `agentctl`** into `~/.local/bin/`.
2. **Installs Claude Code hooks** in `~/.claude/settings.json`
   (`SessionStart`, `Stop`, `PreToolUse`, `PostToolUse`, `UserPromptSubmit`)
   so every Claude session — even the ones you start outside the app —
   reports its status to CLIchat.
3. **Builds the Wails desktop app** and copies it to `/Applications/CLIchat.app`
   (macOS) or `~/.local/share/clichat/CLIchat` (Linux).
4. **Registers the daemon as a service** so it starts at login:
   - macOS: `~/Library/LaunchAgents/com.clichat.host.plist` (launchd)
   - Linux: `~/.config/systemd/user/clichat.service` (systemd user)

Re-running `./scripts/install.sh` is idempotent.

## How to use it

### 1. Open the app

- macOS: open `Applications` and double-click **CLIchat**.
- Linux: run `~/.local/share/clichat/CLIchat`.

The first time, the daemon may need a few seconds to register. The header
will show *"clichat-host online."* once it is ready.

### 2. Start a new chat

Click the **`+`** icon at the top of the sidebar and pick a provider
(Claude or Codex). CLIchat:

- creates a new internal session,
- spawns the chosen CLI in a hidden PTY,
- waits for the TUI to settle (~1.5 s grace),
- shows the chat at the top of the sidebar with the brand logo (Claude or
  Codex) on the avatar.

### 3. Talk to it

Type in the bottom composer and hit Enter. CLIchat sends the text into the
PTY using a Focus-In + plain text + `\r` sequence that works on Claude,
Codex and Gemini. The bubble appears immediately on the right; the answer
streams back as the LLM replies.

While the agent is working, the **status pill** above the composer shows
the current tool (Bash / Read / Write / Web / Agent / thinking…). Click
the pill to toggle the embedded terminal and see the raw TUI live.

### 4. Watch every Claude session, even outside the app

Open another terminal and run `claude` in any project. The
`SessionStart` hook tells CLIchat about it: the session shows up in the
sidebar as a chat. The transcript watcher mirrors every assistant message
into the bubble feed. You can keep working in the terminal — CLIchat is a
read-only mirror in this case.

### 5. Close and reopen with no context loss

- The daemon (`clichat-host`) keeps running in the background, so PTYs stay
  alive when you quit the app.
- If you actually kill the daemon, on next launch CLIchat re-spawns each
  internal chat with `claude --resume <session-id>` (Claude) or
  `codex resume --last` (Codex), so the conversation continues.

### 6. Useful shell helpers

```bash
agentctl list            # show every chat the daemon knows about
agentctl install-hooks   # rewrite the Claude Code hooks (idempotent)
agentctl uninstall-hooks # remove only the hooks
clichat-host serve         # run the daemon manually (debug)
```

The state lives in `~/.clichat/state.json`. Logs in `~/.clichat/logs/`.

## Troubleshooting

- **Status pill never shows up** → `~/.claude/settings.json` is missing the
  managed hooks. Run `agentctl install-hooks`.
- **Codex sessions show empty bubbles** → Codex writes to `~/.codex/sessions`;
  CLIchat needs the rollout to be at most ~5 min old to import it. Wait a
  turn or two; it will pick up automatically.
- **MCP server failed at boot** → Claude is racing the daemon. Open
  `~/.claude/settings.json` and confirm the MCP block points to
  `http://127.0.0.1:47657/mcp`. The daemon must be running before Claude
  starts; check `launchctl list | grep clichat` (macOS) or
  `systemctl --user status clichat` (Linux).
- **App icon is the default Wails "W"** → run `wails build -clean` after
  pulling the latest `build/appicon.png`.

## Develop

```bash
go build ./...                 # all Go binaries
cd frontend && pnpm install && cd ..
~/go/bin/wails dev             # hot-reload desktop app
```

In another terminal:

```bash
~/.local/bin/clichat-host serve  # daemon (port 47657)
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
