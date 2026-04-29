# Changelog

All notable changes to this project are documented here.
The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.1.0/),
and this project adheres to [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

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
- `agent-host` daemon: PTY manager, SSE state stream, MCP HTTP, prompt detector, JSONL transcript watcher, JSON state file.
- Discovery of running Claude sessions via `~/.claude/projects/*/*.jsonl`.
- Hooks installed by `agentctl install-hooks` (`SessionStart`, `Stop`, `PreToolUse`, `PostToolUse`, `UserPromptSubmit`).
- README in EN and PT-BR with prerequisites, install steps and troubleshooting.

[0.2.0]: https://github.com/luisdemarchi/CLIchat/releases/tag/v0.2.0
[0.1.0]: https://github.com/luisdemarchi/CLIchat/releases/tag/v0.1.0
