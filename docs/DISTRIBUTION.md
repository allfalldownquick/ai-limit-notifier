# Distribution and Trust Model

This document defines how source code, user binaries, installation, the hosted service, and self-hosting relate to each other.

## One source branch, releases for users

`main` is the product source of truth. Ordinary users do **not** install from a separate "user branch".

The intended flow is:

```text
main source code
      |
      v
CI + tests
      |
      v
version tag
      |
      v
GitHub Release
      |
      +-- Windows binary / installer
      +-- Linux/WSL binary
      +-- checksums
      `-- release notes
```

A beta is represented by a prerelease/version tag, not by a permanently separate code branch.

This prevents `main`, a hidden hosted copy, and a user-install branch from silently becoming different products.

## The hosted server does not distribute executable code

The project-operated server is a data/control boundary for the notification service, not a software-distribution authority.

The hosted server may:

- receive authenticated normalized usage/reset snapshots;
- create/update durable notification events;
- associate linked devices with Telegram users;
- store delivery settings;
- send Telegram notifications;
- expose narrow status/pairing/account APIs.

The hosted server must **not**:

- push shell commands to an agent;
- tell an agent to execute arbitrary commands;
- return executable payloads to run;
- return arbitrary download URLs that the agent automatically executes;
- replace the installed agent binary;
- modify Claude Code/Codex configuration remotely.

If the hosted server is compromised, that compromise must not automatically become remote code execution on connected user machines.

## Where installation files come from

Normal installation sources:

1. an official GitHub Release from `allfalldownquick/ai-limit-notifier`;
2. a build performed by the user from the public source code.

AI-assisted installation uses the same source. Claude Code/Codex may inspect the repository, determine the correct local setup, and invoke the documented installer, but it must not substitute an unrelated binary or an unverified mirror.

Before unattended updates are enabled, releases must have an integrity/authenticity mechanism stronger than merely trusting a URL returned by the hosted API. Checksums are required for the first public beta; release signing is required before enabling unattended automatic updates.

## Hosted mode

```text
GitHub Release
      |
      v
local AI Limit Notifier agent
      |
      | HTTPS, outbound only
      | provider + usage + reset timestamps
      v
project hosted API
      |
      +-- SQLite durable state
      +-- scheduler
      +-- device/user pairing
      `-- Telegram Bot API
                 |
                 v
             Telegram
```

The local agent keeps provider credentials local. It stores only static install/link configuration needed to reconnect after reboot; monitored usage state remains in RAM during normal operation.

The hosted endpoint should use a stable project-owned HTTPS hostname. The endpoint is configuration, not an authority to change executable code.

## Self-hosted mode

Self-hosted users use the same public code/release but run the server themselves:

```text
local agent
    |
    v
user-owned server
    |
    +-- user-owned persistence/scheduler
    `-- user-owned Telegram bot token
```

The local protocol should remain compatible between hosted and self-hosted modes. A user should be able to change the configured server endpoint without replacing provider adapters or giving the server additional local privileges.

## Suggested binary layout

As implementation grows, keep local and server responsibilities explicit:

```text
cmd/
  ai-limit-notifier/       # local CLI + monitor agent
  ai-limit-server/         # hosted/self-hosted API + scheduler + Telegram
```

The exact package layout may evolve, but local-device code and server-only code must keep separate trust boundaries.

## Release channels

Use versioned releases rather than long-lived product branches:

- prerelease/beta tags for early testers;
- normal releases for declared supported environments;
- `main` remains the current product source and must stay buildable/tested.

A release must describe exactly which environments are verified. Unsupported environments must be reported as unsupported rather than silently using a weaker integration.

## Update policy

For the first beta:

- no hosted-server-driven automatic executable updates;
- `doctor`/`status` may report the installed version;
- the user or coding agent can deliberately install a newer official GitHub Release;
- installer should preserve/revalidate only project-owned static configuration.

Later, an automatic updater may be added only after release signing and rollback/failure behavior are designed and reviewed.

## Why this model exists

The desired trust story is simple:

```text
GitHub = source and releases
local agent = reads limits and sends minimal data
hosted VPS = stores/schedules/delivers notifications
Telegram = user-facing notification destination
```

The VPS is never the authority that can execute new code on user computers.
