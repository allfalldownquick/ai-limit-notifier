# AI Limit Notifier

Zero-context usage monitoring and scheduled Telegram reset notifications for Claude Code and OpenAI Codex.

> **Goal:** know when you can get back to coding without setting timers manually.

## What makes this project different

- **Zero AI calls for monitoring** — the notifier never sends prompts to Claude or Codex.
- **Zero local runtime writes** — local runtime state stays in RAM; no usage database, cache, history, or rotating logs are written by the agent while it runs.
- **Credentials stay local** — the hosted server receives only normalized usage percentages and reset timestamps, never Claude/OpenAI credentials, prompts, project files, or terminal contents.
- **No remote execution** — the hosted server has no protocol for executing commands on connected devices.
- **Telegram delivery** — notify a private bot chat or a configured Telegram channel.
- **Self-hosted or hosted** — the public source is designed for both modes.
- **Source-available licensing** — distributed under PolyForm Shield 1.0.0: ordinary use, modification, self-hosting and internal company use are allowed, while providing a competing product/service requires separate permission from the copyright holder.

## Easiest setup: let Claude Code or Codex inspect first

The intended easiest installation path is deliberately different from a blind `curl | bash` flow:

```text
Open a NEW Claude Code or Codex session
              |
              v
Paste the official install-with-AI prompt
              |
              v
AI inspects your actual Windows / WSL / Linux / macOS setup
              |
              v
AI reviews the repository + installer + security model
              |
              v
AI explains compatibility, missing prerequisites and every planned change
              |
              v
You approve (or decline)
              |
              v
Official installer is applied and adapted to that machine
              |
              v
Doctor + show-payload verification
              |
              v
Telegram pairing
              |
              v
Close the AI session — monitoring runs independently with zero AI calls
```

This allows the coding agent already running on the machine to handle legitimate environment-specific differences: selecting the correct WSL distribution, choosing a supported install location, creating the project's dedicated directory, installing a documented missing prerequisite, fixing project-owned permissions/PATH/service issues, and explaining any required command or UAC/sudo step.

It may **not** silently weaken security, upload provider credentials, invent screen/browser scraping, add remote execution, or modify unrelated projects/system settings. Unexpected privileged or security-sensitive fixes require a new user approval.

The copy-paste prompt and exact rules are being developed here:

- [`prompts/install-with-ai.md`](prompts/install-with-ai.md)
- [`docs/AI_ASSISTED_INSTALL.md`](docs/AI_ASSISTED_INSTALL.md)
- [`docs/INSTALLER_CONTRACT.md`](docs/INSTALLER_CONTRACT.md)

A normal Windows/Linux installer and build-from-source path will remain available for users who do not want AI-assisted setup.

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

The installer will auto-detect supported local environments rather than requiring users to understand the adapter details. The AI-assisted path can additionally explain and fix ordinary machine-specific setup problems while staying inside the documented installer/security contract.

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

## Distribution: releases, not a separate user branch

`main` is the product source of truth. Ordinary users will install versioned **GitHub Releases**, not a permanently separate "user" branch.

```text
main
  |
  v
CI + tests
  |
  v
version tag
  |
  v
GitHub Release
  +-- Windows installer/binary
  +-- Linux/WSL binary
  +-- checksums
  `-- release notes
```

The project-hosted VPS does **not** distribute executable code or push updates/commands to connected devices. Its job is only authenticated usage ingestion, durable scheduling, pairing/account state and Telegram delivery. This keeps a server compromise from automatically becoming remote code execution on user machines.

See:

- [`docs/DISTRIBUTION.md`](docs/DISTRIBUTION.md) — source/release/server trust boundaries;
- [`docs/PROTOCOL_V1.md`](docs/PROTOCOL_V1.md) — pairing, device authentication and usage API contract.

The first beta will not have unattended server-driven auto-updates. Those are deferred until release signing and rollback behavior are designed.

## Licensing

The project uses the **PolyForm Shield License 1.0.0**. See [`LICENSE`](LICENSE).

The intent is simple:

- ordinary personal use is allowed;
- self-hosting is allowed;
- modification is allowed;
- internal company use is allowed when it does not provide a competing product/service;
- a company that wants rights outside the public license can request separate terms from the copyright holder;
- custom development/support for a company can also be negotiated independently of software licensing.

See [`COMMERCIAL.md`](COMMERCIAL.md) for the practical company/commercial path. No public commercial tariff is fixed; any separate arrangement is individual.

Because PolyForm Shield restricts competing use, this repository should be described as **source-available**, not OSI-approved open source.

## Development status

Early development. The first milestone is a real WSL proof-of-concept that reads Claude Code and Codex 5-hour/weekly usage without model calls, persists nothing locally at runtime, schedules server-side reset events, and delivers them to Telegram.

The AI-assisted installation documents currently define the installation/review contract; they do not mean a production installer has already been released.

### Next step: validate the real machine

The architecture/design work that can be done without a real provider installation is largely prepared. The current blocker is P0 evidence from an actual Claude Code/Codex environment.

For that step, open a new Claude Code or Codex coding-agent session in the local repository and use:

- [`prompts/validate-real-machine.md`](prompts/validate-real-machine.md) — canonical P0 procedure for proving the installed Claude/Codex rate-limit readers, field semantics, no-monitoring-model-call behavior, and runtime side effects.

The prompt starts read-only, asks before persistent/privileged changes, rejects unsafe fallbacks, and requires sanitized evidence before production adapters are implemented.

### Continue development after P0

`main` is the active product branch. A fresh coding-agent session should not rely on old chat history.

1. Read [`docs/PROJECT_STATUS.md`](docs/PROJECT_STATUS.md) for the exact current implementation state and priority order.
2. Read [`docs/RELEASE_CRITERIA.md`](docs/RELEASE_CRITERIA.md) for the evidence required before calling v0.1 usable.
3. Read [`docs/DECISIONS.md`](docs/DECISIONS.md) for settled choices and remaining blockers.
4. After P0 validation, use [`prompts/continue-development.md`](prompts/continue-development.md) for normal implementation continuation.

The next agent is instructed to inspect first, run current checks, preserve the fixed security constraints, work on `main`, and continue from the first incomplete implementation priority rather than reopening settled product discussions.

CI on `main` checks Go formatting, `go test ./...`, and `go vet ./...` as the codebase grows.

## Security principles

See `SECURITY.md` as the implementation develops. The server API will accept a strict, minimal schema and will not accept arbitrary commands, URLs, Telegram messages, local paths, or executable payloads.
