# Architecture

## Product modes

AI Limit Notifier is designed around two deployment modes that share the same local agent and protocol.

### Hosted

A user links the local agent to the public AI Limit Notifier Telegram bot operated by the project. The hosted service stores only normalized usage/reset data, delivery settings and billing state.

Launch policy:

- the initial hosted beta is free while monitoring, installation and delivery are being validated with real users;
- billing support is still designed into the server from the start;
- hosted pricing is configuration, not hard-coded product logic;
- after the beta is stable, the hosted service may use a symbolic recurring Telegram Stars subscription, initially `1 XTR` per 30 days if that price is accepted by Telegram's production payment flow;
- payment behavior must be verified in Telegram's test environment before it is enabled for production users;
- changing the hosted price later must not require a client update.

### Self-hosted

A user runs the server and Telegram bot delivery on infrastructure they control. No hosted AI Limit Notifier service is required. Self-hosting remains independent of hosted billing.

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

Default local notification threshold: 80% used, configurable per device (`ai-limit-notifier config threshold N`, `0 < N <= 100`) and stored only in that device's local static config — never on the server, never Telegram-managed.

The threshold decision happens entirely on the device: below it, `monitor` makes no HTTP request to the server at all. The server applies no percentage gate of its own — any schema-valid, authenticated usage submission it does receive has, by construction, already crossed that device's local threshold, so the server always creates/updates the corresponding durable event. This means a local restart can safely resubmit the same (provider, window, reset_at) after the threshold is still crossed; server-side durable dedup (not a second threshold check) is what prevents a duplicate Telegram notification.

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

Digital-service invoices use Telegram Stars (`XTR`). Hosted price and billing enablement must be server-configurable. Recurring subscriptions use Telegram's supported subscription period and are enabled only after the free beta monitoring path is proven stable.

The local agent contains no pricing enforcement and never receives the Telegram bot token or payment credentials.
