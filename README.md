# AI Limit Notifier

Zero-context usage monitoring and scheduled Telegram reset notifications for Claude Code and OpenAI Codex.

> **Goal:** know when you can get back to coding without setting timers manually.

## What makes this project different

- **Zero AI calls for monitoring** — the notifier never sends prompts to Claude or Codex.
- **Zero local runtime writes** — local runtime state stays in RAM; no usage database, cache, history, or rotating logs are written by the agent while it runs.
- **Credentials stay local** — the hosted server receives only normalized usage percentages and reset timestamps, never Claude/OpenAI credentials, prompts, project files, or terminal contents.
- **No remote execution** — the hosted server has no protocol for executing commands on connected devices.
- **Telegram delivery** — notify a private bot chat or a configured Telegram channel.
- **Self-hosted or hosted** — the open-source stack can be self-hosted; an optional hosted service can provide the server infrastructure and Telegram delivery.

## Core behavior

The local agent normalizes provider usage to `used_percent`:

- Codex: `20% left` becomes `80% used`.
- Claude Code: `80% usage` remains `80% used`.

By default, a reset notification is scheduled only after a window reaches **80% used**. This prevents notifications for lightly used windows.

The server stores the reset task durably and sends the Telegram notification at `reset_at + 1 minute`. If Claude Code and Codex reset within 10 minutes of each other, the server may combine the notification and suppress the redundant second message.

Both 5-hour and weekly windows are tracked. Weekly usage will also support pacing/budget information so users can see whether they are ahead of or behind an even weekly usage pace.

## Planned platform support

| Environment | Planned support |
| --- | --- |
| Claude Code in WSL/Linux | First-class, v0.1 |
| Codex CLI in WSL/Linux | First-class, v0.1 |
| Claude Code on native Windows | Planned |
| Codex CLI / Codex app on Windows | Planned; adapter depends on the locally available Codex interface |
| macOS | Planned |
| Plain Claude desktop/web chat without Claude Code | Not a v0.1 target |

The installer will auto-detect supported local environments rather than requiring users to understand the adapter details.

## Architecture

```text
Claude Code / Codex
        |
        v
Local agent (RAM-only runtime state)
        |
        | HTTPS: provider + usage + reset timestamps only
        v
Hosted or self-hosted server
        |
        +-- durable scheduler
        +-- deduplication
        +-- weekly pacing
        +-- Telegram Bot API
        |
        v
Telegram private chat OR channel
```

## Development status

Early development. The first milestone is a real WSL proof-of-concept that reads Claude Code and Codex 5-hour/weekly usage without model calls, persists nothing locally at runtime, schedules server-side reset events, and delivers them to Telegram.

## Security principles

See `SECURITY.md` as the implementation develops. The server API will accept a strict, minimal schema and will not accept arbitrary commands, URLs, Telegram messages, local paths, or executable payloads.
