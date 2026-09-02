# Product Decisions

This document separates decisions that are already fixed from items that still need validation or a product choice.

## Closed decisions

- `main` is the single active product branch unless explicitly changed later.
- Ordinary users do not install from a separate product/user branch; user distribution is through versioned GitHub Releases.
- Beta/stable channels use release tags/prereleases rather than permanently divergent product branches.
- The repository is source-available under the PolyForm Shield License 1.0.0.
- Ordinary personal use, self-hosting, modification, and non-competing internal company use are allowed under the public license.
- Providing a competing product/service using the software requires separate permission from the copyright holder.
- Company-specific licensing, support, or custom development may be negotiated individually in the future; no public commercial tariff is fixed now.
- Because the license restricts competing use, the project should not be described as OSI-approved open source.
- External contributions must not be merged in a way that prevents the copyright holder from offering separate permissions/company licenses later; a CLA or equivalent rights-grant process is required before substantial outside code is accepted.
- The project-operated hosted mode uses a dedicated AI Limit Notifier Telegram bot and project-operated server infrastructure.
- The hosted server is not a software-distribution authority: it must not push executable code, arbitrary download URLs to execute, shell commands, or remote configuration changes to local agents.
- Official client binaries/installers come from official GitHub Releases or are built by the user from public source.
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
- The first beta does not use hosted-server-driven automatic executable updates. Unattended auto-update is deferred until release signing and rollback behavior are designed.

## Open technical validations

These are not product debates; they need implementation and real-environment proof.

- Prove a reliable Claude Code rate-limit reader in WSL/Linux without model calls and without local runtime persistence.
- Prove the Codex `account/rateLimits/read` path in the supported WSL/Linux environments without starting a model turn.
- Determine the safe native-Windows Claude Code adapter.
- Determine the safe native-Windows Codex CLI / Codex desktop adapter and explicitly mark unsupported environments when no stable read-only interface exists.
- Choose the WSL agent lifecycle so monitoring works reliably without unnecessarily keeping a WSL distro alive.
- Implement and test the deterministic installer surface: `detect`, `install --plan`, `install`, `doctor`, `show-payload`, `status`, `uninstall --plan`, `uninstall`.
- Decide and implement static linked-device credential storage per platform (for example restrictive file permissions on WSL/Linux and an appropriate protected store on native Windows).
- Implement strict hosted API authentication, idempotency/replay handling, rate limiting, schema validation and device revocation.
- Implement durable server scheduling, SQLite persistence, Telegram rate-limited delivery and restart recovery.
- Define Telegram delivery crash semantics; v0.1 should prefer safe at-least-once delivery with rare duplicate tolerance over silently losing reset notifications.
- Verify Telegram private-chat binding first; channel binding and safe ownership/permission verification follow after the private path works.
- Define bounded hosted-history retention and user deletion behavior before accepting broad public usage.
- Verify Telegram Stars recurring billing in Telegram's test environment before enabling any production charge.
- Build release artifacts for supported OS/architectures and publish checksums for the first public beta.
- Add a release-signing strategy before promoting unattended automatic updates.
- Extend CI/release automation and complete security/code review before a stable release.

## Open product choices

These can be decided after the core path works.

- Exact hosted usage/history retention period after observing what weekly analytics actually needs.
- Whether weekly pacing is shown only on demand, sent on a schedule, or triggers warnings when usage gets materially ahead of pace.
- Whether hosted beta users can link unlimited devices or whether a simple device cap is useful later.
- Exact Free/Pro feature split after the free beta.
- Final post-beta Stars price after observing real usage and infrastructure/support costs.

Advertising/sponsorship is intentionally out of scope until the core product is working and used in practice.
