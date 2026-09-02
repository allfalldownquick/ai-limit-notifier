# Architecture

## Product modes

AI Limit Notifier is designed around two deployment modes that share the same local agent and protocol.

### Hosted

A user links the local agent to the public Telegram bot. The hosted service stores only normalized usage/reset data, delivery settings and billing state.

### Self-hosted

A user runs the server and Telegram bot delivery on infrastructure they control. No hosted AI Limit Notifier service is required.

## Local agent

The agent is intentionally outbound-only. The hosted server cannot issue commands to it.

Runtime invariants:

- no LLM/model requests for monitoring;
- no usage/history/cache/log file writes by the agent;
- transient state is held in RAM;
- install-time files (binary, service definition and static credential/configuration) may exist but are not periodically rewritten;
- provider credentials are never uploaded to the AI Limit Notifier server;
- only normalized usage/reset information is sent to the server.

## Provider adapters

The core does not assume WSL. Provider adapters expose the same normalized model:

```text
provider
five_hour.used_percent
five_hour.reset_at
weekly.used_percent
weekly.reset_at
```

Initial adapters:

1. `claude/statusline` for Claude Code in WSL/Linux. Claude Code invokes the installed capture command with status JSON; only rate-limit fields are retained in RAM.
2. `codex/app-server` for Codex CLI in WSL/Linux. The local Codex app-server rate-limit interface is used without starting a model turn.

Planned adapters:

- native Windows Claude Code;
- native Windows Codex CLI;
- Codex desktop app where a stable local read-only interface is available;
- macOS.

The project must not scrape terminal UI or screen pixels as a primary integration.

## Environment detection

The installer should detect rather than ask users to understand implementation details:

```text
Windows
  |-- WSL with claude/codex -> install Linux agent in selected distro
  |-- native claude/codex   -> install Windows agent/adapters when supported
  `-- desktop-only setup    -> report supported/unsupported adapters explicitly

Linux/WSL
  |-- claude -> configure Claude statusLine capture
  `-- codex  -> enable Codex app-server reader
```

Unsupported environments must fail closed: no browser scraping, no credential upload and no hidden model request fallback.

## Threshold and scheduling model

Usage is normalized to `used_percent`.

- Codex `20% left` => `80% used`.
- Claude Code `80% usage` => `80% used`.

Default reset notification threshold: 80% used.

The local agent sends the current snapshot when required. The server determines whether a durable reset event must be created. This means a local restart can safely resend a snapshot; server-side idempotency prevents duplicate Telegram notifications.

Reset notifications are scheduled for `reset_at + 1 minute` by default.

If equivalent Claude Code and Codex windows reset within 10 minutes, the server may send one combined notification at the earlier reset and mark the later event as covered so it is not sent again.

## Weekly pacing

A weekly window is treated as seven continuous days ending at the exact provider `reset_at`, not as calendar days.

The expected even-use percentage at time `now` is:

```text
elapsed_since_window_start / 7 days * 100
```

The server can compare this to actual weekly usage:

- negative delta: usage is below even pace (reserve available);
- positive delta: usage is ahead of even pace;
- remaining percentage shows capacity left for the rest of the window.

This is planning information, not a guarantee about provider availability.

## Server API

The device API is narrow and data-only. It must never accept arbitrary commands, shell strings, local paths, URLs to fetch, or Telegram message bodies.

Conceptual request:

```json
{
  "provider": "codex",
  "five_hour": {
    "used_percent": 83,
    "reset_at": "2026-09-02T20:00:00Z"
  },
  "weekly": {
    "used_percent": 57,
    "reset_at": "2026-09-08T08:12:00Z"
  }
}
```

The server returns success only after the relevant state has been durably persisted.

## Telegram delivery

A user selects one active delivery destination:

- private chat with the bot; or
- Telegram channel where the bot has permission to post.

The local device never supplies the destination or arbitrary message text. Both are resolved server-side from the linked account/device.

## Payments

Hosted billing is separate from the open-source/self-hosted core.

Telegram Stars price must be server-configurable. Digital-service invoices use Telegram Stars (`XTR`). A 30-day recurring subscription may be added after the core monitoring path is proven stable.

Self-hosted use remains independent of hosted billing.
