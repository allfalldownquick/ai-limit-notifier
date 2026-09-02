# Project Status

This file is the handoff point for continuing development in Claude Code or Codex.

## Current state

The repository contains the product architecture, security constraints, AI-assisted installation contract, installer command contract, distribution/protocol boundaries, licensing decisions, the first domain primitives for normalized usage windows and weekly pacing, and real-machine validation evidence for Codex.

The project is **not yet a usable release**. The next work is implementation and real-machine validation, not more product brainstorming.

Implemented/proven now:

- normalized `codex` / `claude` provider model;
- optional 5-hour and weekly windows (`missing` means unknown, never zero);
- Codex `left` -> internal `used_percent` normalization primitive;
- default 80% reset scheduling threshold primitive;
- reset timestamp validation primitives;
- weekly even-pace calculation;
- unit tests for the current domain behavior;
- documented zero-context / zero-local-runtime-write / no-remote-execution constraints;
- documented assisted-install flow and installer CLI contract;
- documented GitHub Release / hosted-server trust boundary;
- documented pairing/device API direction;
- source-available PolyForm Shield 1.0.0 licensing with future individual company arrangements preserved;
- hosted beta policy: project bot/server, initially free;
- CI on `main` for formatting, tests, and vetting;
- **Codex 0.152.1 on Ubuntu 26.04 / WSL2: real read-only rate-limit path proven via `codex app-server --stdio` -> JSON-RPC `initialize` -> `account/rateLimits/read`; real 5-hour/weekly fields and semantics observed without creating a thread/turn/prompt/inference request.**

See `docs/REAL_MACHINE_VALIDATION.md` for sanitized evidence and compatibility caveats.

## Development rule

Work directly on `main` unless the owner explicitly asks for another branch. Keep `main` buildable and tested after each meaningful change.

Do not add advertising, growth features, paid tiers, dashboards, or unrelated integrations before the core release path below works end to end.

## Priority order to a real v0.1

### P0 — prove provider reads on a real WSL/Linux machine

**Current P0 status: Codex proven for the tested version/environment; Claude Code still pending.**

Use [`prompts/validate-real-machine.md`](../prompts/validate-real-machine.md) for this phase. It is deliberately validation-first and requires evidence before production adapters are written.

Claude Code — **PENDING**:

- obtain 5-hour and weekly usage/reset metadata without sending a model prompt;
- do not persist monitored usage to AI Limit Notifier runtime files;
- validate whether `statusLine` rate-limit data is sufficiently reliable for the supported Claude Code versions;
- treat absent/partial data as unknown;
- if temporary statusLine configuration is needed for proof, require explicit user approval and restore the prior value exactly after the test.

Codex — **PROVEN on 0.152.1 / Ubuntu 26.04 / WSL2**:

- local interface: `codex app-server --stdio`;
- protocol: JSON-RPC 2.0;
- sequence: `initialize` then `account/rateLimits/read`;
- `primary.windowDurationMins = 300` was observed as the 5-hour window;
- `secondary.windowDurationMins = 10080` was observed as the weekly window;
- `usedPercent` is used percentage;
- `resetsAt` is Unix epoch seconds;
- no `thread/*`, `turn/*`, prompt, completion/chat, or inference request was invoked for the validated read;
- app-server is experimental, so production support must be version-aware and fail closed when schema/interface compatibility is not proven.

Whether Codex performs a network request on each rate-limit read is not yet proven and is not required for the zero-model-call guarantee. Provider-owned writes under `~/.codex` are distinct from the product guarantee that AI Limit Notifier itself must not persist monitored usage/runtime state.

Exit condition: Claude Code is either proven with the same evidence standard for the declared v0.1 environment, or the v0.1 support matrix is narrowed explicitly. Then production provider adapters can be implemented from observed schemas rather than assumptions.

### P1 — local CLI and diagnostics

Implement the stable local surface defined in `docs/INSTALLER_CONTRACT.md`:

- `detect`;
- `install --plan`;
- `doctor`;
- `show-payload`;
- `status`;
- `uninstall --plan`.

`show-payload` must make data minimization independently inspectable by the user.

Exit condition: a user/coding agent can determine compatibility and see exactly what would be installed/sent before enabling monitoring.

### P2 — RAM-only monitoring agent

Implement provider polling/event capture, normalization, threshold decisions, in-memory retry state and HTTPS delivery.

Requirements:

- no local usage/history/cache/runtime-log persistence by the application;
- no provider/model calls created solely for monitoring;
- bounded CPU/network use;
- retries with backoff while the process remains alive;
- safe resubmission after restart with server-side deduplication.

### P3 — hosted server core

Implement:

- strict device API;
- per-device revocable authentication;
- body-size/schema/range/timestamp validation;
- replay/idempotency protection;
- SQLite persistence;
- durable notification events;
- scheduler restart recovery;
- Telegram rate-limited worker with retry/backoff.

The device API must never accept arbitrary shell commands, URLs, local paths, Telegram text, or destinations.

### P4 — Telegram private-chat onboarding

Implement the project's dedicated bot:

`/start -> one-time pairing code -> local link -> device appears connected`.

Pairing requirements:

- short-lived code;
- one use only;
- brute-force/rate-limit protection;
- server issues a scoped device credential after successful exchange;
- bot token remains server-side only.

Private chat is the first required delivery mode. Channel binding can follow after this path is stable.

### P5 — deterministic installation

Implement the actual installer used by both humans and the AI-assisted flow.

Required targets for the first release:

- WSL/Linux first-class;
- clean install plan before mutations;
- documented prerequisite handling;
- service/autostart behavior that does not unnecessarily keep WSL alive;
- clean uninstall restoring project-owned Claude configuration changes where possible;
- `doctor` verification after installation.

### P6 — end-to-end reliability

Verify at minimum:

1. crossing 80% creates exactly one reset event for the same reset window;
2. 80 -> 90 -> 100 does not create duplicate notifications;
3. server restart does not lose an acknowledged event;
4. local PC/WSL can be offline at reset time and Telegram still arrives;
5. overdue unsent events recover after server restart;
6. Claude/Codex same-kind resets within 10 minutes can be combined without a later duplicate;
7. partial provider responses never become fake 0% values;
8. provider credentials/prompts/project data are absent from server requests/logs;
9. uninstall removes the agent/service/project-owned integration cleanly.

### P7 — native Windows adapters

Only after the WSL path is real and stable, investigate native Windows Claude Code and Codex/Codex App. Add support only when a safe read-only source is verified. Otherwise report `unsupported` explicitly.

### P8 — hosted beta

Launch with the project-operated Telegram bot and server **free during validation**. Collect reliability/installation feedback, not advertising revenue.

Telegram Stars billing is intentionally later. The server architecture may contain a configurable billing boundary, but payment must not block the first working beta.

## Before calling anything a release

Read and satisfy `docs/RELEASE_CRITERIA.md`.

## Starting the next real-machine session

For the remaining P0 work, continue with [`prompts/validate-real-machine.md`](../prompts/validate-real-machine.md), now focusing on Claude Code. The Codex reader is already evidenced in `docs/REAL_MACHINE_VALIDATION.md`; do not repeat it unless a version/environment change requires revalidation.

After P0 is closed, use `prompts/continue-development.md` for normal implementation continuation.
