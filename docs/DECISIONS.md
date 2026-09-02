# Product Decisions

This document separates decisions that are already fixed from items that still need validation or a product choice.

## Closed decisions

- `main` is the single active product branch unless explicitly changed later.
- The project is open source and supports self-hosting.
- The project-operated hosted mode uses a dedicated AI Limit Notifier Telegram bot and project-operated server infrastructure.
- The initial hosted beta is free while the monitoring path is being validated.
- Hosted billing is server-configurable and is not enforced by the local agent.
- After the beta is stable, a symbolic recurring Telegram Stars price may start at `1 XTR` per 30 days if Telegram accepts that amount in the production subscription flow.
- Self-hosted use remains independent of hosted billing.
- The local monitor makes zero AI/model calls for monitoring.
- The local monitor does not persist usage/history/cache/runtime logs during normal operation.
- Claude/OpenAI credentials, prompts, project files and terminal contents are never uploaded to the AI Limit Notifier server.
- The hosted server has no server-to-device remote execution channel.
- Usage is normalized to `used_percent`.
- The default reset-notification threshold is 80% used.
- Reset messages are scheduled for `reset_at + 1 minute`.
- Equivalent Claude/Codex resets within 10 minutes may be combined.
- Missing provider windows are treated as unknown, never as zero usage.
- A user has one active Telegram delivery destination: private bot chat or configured channel.
- AI-assisted installation is a first-class setup method, but it must inspect and present the installation plan before making changes.

## Open technical validations

These are not product debates; they need implementation and real-environment proof.

- Prove a reliable Claude Code rate-limit reader in WSL/Linux without model calls and without local runtime persistence.
- Prove the Codex `account/rateLimits/read` path in the supported WSL/Linux environments without starting a model turn.
- Determine the safe native-Windows Claude Code adapter.
- Determine the safe native-Windows Codex CLI / Codex desktop adapter and explicitly mark unsupported environments when no stable read-only interface exists.
- Choose the WSL agent lifecycle so monitoring works reliably without unnecessarily keeping a WSL distro alive.
- Implement and test the deterministic installer surface: `detect`, `install --plan`, `install`, `doctor`, `show-payload`, `status`, `uninstall --plan`, `uninstall`.
- Implement strict hosted API authentication, replay protection, rate limiting, schema validation and device revocation.
- Implement durable server scheduling, SQLite persistence, Telegram rate-limited delivery and restart recovery.
- Verify Telegram private-chat and channel binding, including safe channel ownership/permission verification.
- Verify Telegram Stars recurring billing in Telegram's test environment before enabling any production charge.
- Add release checksums/signing strategy before promoting unattended automatic updates.
- Add CI and complete security/code review before a stable release.

## Open product choices

These can be decided after the core path works.

- How much usage/history the hosted service retains and for how long.
- Whether weekly pacing is shown only on demand, sent on a schedule, or triggers warnings when usage gets materially ahead of pace.
- Whether hosted beta users can link unlimited devices or whether a simple device cap is useful later.
- Exact Free/Pro feature split after the free beta.
- Final post-beta Stars price after observing real usage and infrastructure/support costs.
- Whether sponsored messages are ever used; they are not required for the initial product.
