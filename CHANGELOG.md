# Changelog

All notable changes to this project are documented here.
The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.1.0/),
and this project adheres to [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

## [Unreleased]

### Added
- **Per-chat memory database**: `clichat-host` now maintains
  `~/.clichat/memory.sqlite3` with SQLite WAL + FTS5 indexing for each chat's
  messages, summary, current topic, and topic history. New HTTP endpoints expose
  `/v1/memory/{id}/summary` and `/v1/memory/{id}/search`.
- **Smart chat titles**: chat titles/topics are now updated locally from the
  latest user intent, with MCP `agent_chat_set_topic` still able to refine the
  active subject. The visible chat name no longer depends on Claude remembering
  to call a tool.
- **One-command CLI installer**: `clichat install`, `clichat repair`,
  `clichat status`, and `clichat logs` now provide the simple install/repair
  flow previously split across shell scripts and helper commands.
- **Go-native install path**: the module path is now
  `github.com/luisdemarchi/CLIchat`, so users can install the CLI with
  `go install github.com/luisdemarchi/CLIchat/cmd/clichat@main`.

### Fixed
- **Terminal handoff per chat**: when an app-owned terminal closes, the chat now
  stays intact and the UI shows an explicit "open terminal" action. Opening a
  replacement terminal sends a compact handoff prompt built from that chat's
  internal memory.
- **Provider transfer**: the same chat can now be moved to another terminal
  provider (Claude, Codex, Gemini). The old PTY is stopped, the provider is
  switched on the existing instance, and the new provider receives the chat
  handoff as startup context.
- **Internal session isolation**: empty/home CWD chats now get a per-instance
  sandbox directory, transcript linking chooses the closest matching internal
  chat instead of map iteration order, and CWD matching no longer treats two
  unrelated folders with the same basename as equivalent.
- **Codex reconnect safety**: reconnect uses stored `codex resume <session-id>`
  when available and starts a fresh isolated PTY otherwise. It no longer uses
  the global `codex resume --last` fallback, which could attach a chat to
  another terminal's rollout.
- **Transcript tailing**: JSONL cursors are persisted by byte offset and
  messages carry a source id, preventing restart-time transcript replay from
  duplicating or cross-feeding bubbles.
- **Gemini internal chats**: Gemini is restored as a provider, starts with an
  explicit per-chat `--session-id`, and uses `--skip-trust` to avoid getting
  stuck on the trust-folder prompt for app-owned sessions.

### Removed
- **`agentctl` binary**: hook, attach, list, install, and repair behavior moved
  into the single `clichat` CLI. The uninstaller removes stale `agentctl` and
  hook repair strips legacy `agentctl-managed` hook entries.

## [0.3.0] - 2026-05-01

### Added
- **Swipe-to-delete** chat in the sidebar: drag a chat row to the left past ~130px → confirm dialog → `App.DeleteSession` (DELETE `/v1/instances/{id}` → manager.Stop + store.Unregister). New `chat-row-bg` strip with red gradient + Trash2 icon underneath.
- **Reconnect on click**: clicking an offline chat row triggers `App.ReconnectSession(id)` automatically — respawns the CLI with `claude --resume <id>` / `codex resume --last` / gemini fresh. Frontend `lib/api.ts reconnectSession()` + bridge.
- **PTY resize sync**: new `POST /v1/instances/{id}/resize {cols, rows}` endpoint backed by `pty.Setsize`. `TerminalPane.tsx` calls `resizeTerminal()` on every `fit.fit()` so the embedded xterm and the underlying TUI agree on dimensions; no more clipped Claude/Codex layouts.
- **Stop button** next to the busy status pill (red Square icon). Sends `\x1b` (ESC) via `sendTerminalInput` to interrupt the running CLI.
- **Bubble font zoom**: 5 levels (0.85 / 0.95 / 1.0 / 1.15 / 1.3) via `--wa-bubble-scale`. ZoomIn/ZoomOut buttons in the sidebar header. Keyboard shortcuts `Cmd +`, `Cmd -`, `Cmd 0`. Persisted in `localStorage`.
- **Sandbox CWD default** for spawned CLIs: `~/.clichat/sandbox` is created on demand and used whenever the chat CWD is empty *or* `$HOME`. Prevents Claude/Codex/Gemini from scanning `~/Library/CloudStorage/*` and triggering macOS TCC prompts (Apple Music, Google Drive, iCloud Drive…). Older instances created with `cwd=$HOME` are auto-migrated on next reconnect.
- **launchd persistence** + control script:
  - `~/Library/LaunchAgents/com.clichat.host.plist` keeps the daemon running with `KeepAlive=true`, `RunAtLoad=true`, `ThrottleInterval=5`. Logs in `~/.clichat/logs/host.{out,err}.log`.
  - `~/.local/bin/clichat`: `start | stop | restart | status | logs | rebuild | daemon-only`.
  - `clichat rebuild` now also codesigns the daemon ad-hoc with stable identifier `com.clichat.host` so macOS TCC decisions persist between rebuilds.
- **Codesign + entitlements**: ad-hoc signing of `clichat-host` via `build/clichat-host.entitlements.plist` (explicit-deny for media-library, music/movies/pictures/photos, audio-input, camera, calendars, addressbook, location). Identifier stable, so `Não Permitir` decisions in TCC stick across rebuilds.
- **System-event filter**: transcript watcher now drops bubbles whose body starts with `<task-notification>`, `<system-reminder>`, `<command-name>`, `<command-message>`, `<command-args>`, `<local-command-stdout>`, `<local-command-stderr>`, `<user-prompt-submit-hook>`, `<bash-input>`, `<bash-stdout>`, `<bash-stderr>`, `<bash-stdin-disabled>`, `<function_calls>`, `<function_results>` (`internal/agent/transcript_filter_test.go` covers the cases). Real Claude messages stay; plumbing XML stays out of the chat UI.

### Changed
- **Backend renamed** `agent-host` → `clichat-host` everywhere: source dir (`cmd/agent-host` → `cmd/clichat-host`), binary (`~/.local/bin/clichat-host`), launchd plist payload, log strings, error messages in `internal/app/app.go`, README/CHANGELOG/ARCHITECTURE/HANDOFF.
- **Auto-reconnect now retries fresh** when the resume hint fails (`codex resume --last` → no rollout / locked, `claude --resume <id>` → session UUID gone). Reconnect timeout bumped 5s → 30s; success/failure logged (`reconnect-ok / reconnect-failed / reconnect-retry-fresh`).
- **`SendMessage` timeout** 5s → 30s (Codex/Claude take longer to settle).
- **Composer**: no longer blocks input while the agent is busy — CLIs accept concurrent input. `canSend` now only depends on `terminalAttached && status !== 'offline'`. Disabled state has clear visual: opacity 0.5 + italic placeholder + `cursor: not-allowed`. Placeholder reads "Chat offline — clique no chat para reconectar".
- **Sidebar row snippet** no longer duplicates the topic — when `lastMessage === topic`, falls back to the live status label.
- **`markBootOffline`** now forces `Status=Offline + TerminalAttached=false` for **all** internal instances at boot; previously stale `terminalAttached=true` rows were skipped by `tryAutoReconnect` and stayed frozen.
- **Embedded terminal hardened**: light + dark themes synced with `data-theme` via `MutationObserver`, dynamic cols/rows from `FitAddon.fit()` (was 120 cols fixed), sticky-bottom (only auto-scroll when user is already at the bottom), scrollback 10k → 20k, smooth scroll 80ms, `cursorStyle: 'block'`, `macOptionIsMeta`, `rightClickSelectsWord`. CSS overrides `.terminal-panel` + `.xterm-shell` for both themes.

### Fixed
- **Claude rate-limit menu** now surfaces as a prompt-card bubble. The numbered-option regex no longer requires the cursor glyph (`›`) on every line — Claude only marks the selected option. New `cursorOption` gate plus expanded trigger phrases (`what do you want to do`, `what would you like to do`, `enter to confirm`, `esc to cancel`).
- **macOS TCC prompts (Music, Desktop, iCloud, Documents…)** at chat creation: voicemode MCP removed (`claude mcp remove voicemode` + `~/.claude/settings.local.json` permission entries deleted) — was the original culprit. Combined with the sandbox CWD default + codesign-stable identifier, prompts no longer appear on each rebuild.
- **`stateEvents` daemon panic** (`send on closed channel`) when an SSE client disconnected mid-broadcast: listener callback now uses `defer recover()` and defers reordered (`unsubscribe` before `close(out)`).

### Removed
- **voicemode MCP** unregistered from Claude Code (originally responsible for the Apple Music TCC prompt). Settings cleaned: `mcp__voicemode__converse` and `mcp__voicemode__service` permissions dropped from `~/.claude/settings.local.json`.

## [0.2.0] - 2026-04-29

### Added
- Light/dark theme selector in the sidebar header (Sun/Moon toggle), persisted in `localStorage` with `prefers-color-scheme` fallback. WhatsApp Web Dark palette applied via `[data-theme='dark']` on `<html>`.
- New app icon — WhatsApp-style speech bubble with `CLI` text on a green gradient. SVG source at `build/appicon.svg`, regenerated `build/appicon.png` (1024×1024) and `build/iconfile.icns` (multi-size).
- File attachment in the composer: native multi-file picker (Wails `OpenMultipleFilesDialog`) lets the user pick several files at once; the daemon dispatches them to the underlying CLI **one at a time** as `@<path>` lines, with a small settle delay between each. A single user bubble groups all attached paths.
- `+` button now opens a dropdown menu (Claude / Gemini / Codex) anchored to the button, with provider logos, click-outside / Esc to dismiss, and `indisponivel` hint when the CLI is missing on the host.

### Changed
- `internal/agent/promptdetect.go`: relaxed the numbered-option regex so options without the cursor glyph are still captured (Claude's TUI marks only the selected option). Added a new `cursorOption` gate plus extra trigger phrases (`what do you want to do`, `what would you like to do`, `enter to confirm`, `esc to cancel`) so menus like the rate-limit picker render as bubbles instead of staying stuck in the terminal.
- `frontend/src/styles.css`: refactored hardcoded color literals into CSS variables (`--wa-body-bg`, `--wa-row-divider`, `--wa-avatar-bg`, `--wa-hatch`, `--wa-tint-fg`, `--wa-error-bg`, `--wa-warn-bg`, `--wa-pill-*`, `--wa-prompt-card-bg`, `--wa-opt-key-bg`, `--wa-composer-disabled-bg`, `--wa-empty-overlay`) so both themes share the same selectors.

### Fixed
- Claude rate-limit menu (`What do you want to do? / 1. Stop and wait... / 2. Upgrade your plan / 3. Upgrade to Team plan`) no longer fails to surface as a prompt-card bubble.
- Markdown numbered lists inside ordinary assistant answers are still ignored (covered by `internal/agent/promptdetect_test.go`).

## [0.1.0] - 2026-04-29

### Added
- Initial open-source release as **CLIchat**.
- Wails (Go + React 19) desktop app: WhatsApp-style sidebar, conversation panel with markdown bubbles, embedded `xterm.js` terminal per chat.
- `clichat-host` daemon: PTY manager, SSE state stream, MCP HTTP, prompt detector, JSONL transcript watcher, JSON state file.
- Discovery of running Claude sessions via `~/.claude/projects/*/*.jsonl`.
- Hooks installed by `agentctl install-hooks` (`SessionStart`, `Stop`, `PreToolUse`, `PostToolUse`, `UserPromptSubmit`).
- README in EN and PT-BR with prerequisites, install steps and troubleshooting.

[0.3.0]: https://github.com/luisdemarchi/CLIchat/releases/tag/v0.3.0
[0.2.0]: https://github.com/luisdemarchi/CLIchat/releases/tag/v0.2.0
[0.1.0]: https://github.com/luisdemarchi/CLIchat/releases/tag/v0.1.0
