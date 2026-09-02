# Project Status

This file is the handoff point for continuing development in Claude Code or Codex.

## Current state

**P0 is now closed for both providers** (Codex + Claude Code) on Ubuntu 26.04 / WSL2. See below and `docs/REAL_MACHINE_VALIDATION.md`.

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
- **Claude Code 2.1.258 on Ubuntu 26.04 / WSL2: real read-only rate-limit path proven via the existing `statusLine` command mechanism; real 5-hour/weekly `used_percentage`/`resets_at` fields observed via a temporary, byte-for-byte-restored instrumentation of the user's pre-existing statusLine script, with zero model requests and zero AI Limit Notifier persistent runtime writes.**

See `docs/REAL_MACHINE_VALIDATION.md` for sanitized evidence and compatibility caveats.

## Development rule

Work directly on `main` unless the owner explicitly asks for another branch. Keep `main` buildable and tested after each meaningful change.

Do not add advertising, growth features, paid tiers, dashboards, or unrelated integrations before the core release path below works end to end.

## Priority order to a real v0.1

### P0 — prove provider reads on a real WSL/Linux machine

**Current P0 status: Codex and Claude Code both proven for the tested version/environment.**

Use [`prompts/validate-real-machine.md`](../prompts/validate-real-machine.md) for this phase. It is deliberately validation-first and requires evidence before production adapters are written.

Claude Code — **PROVEN on 2.1.258 / Ubuntu 26.04 / WSL2**:

- local interface: the existing `statusLine` command mechanism (`~/.claude/statusline-command.sh`), invoked by Claude Code's own refresh cadence, not by any monitoring-triggered action;
- fields: `rate_limits.five_hour.used_percentage`, `rate_limits.five_hour.resets_at`, `rate_limits.seven_day.used_percentage`, `rate_limits.seven_day.resets_at`;
- `used_percentage` is already used percentage; `resets_at` is Unix epoch seconds;
- no model request/prompt was created to obtain the capture;
- AI Limit Notifier made zero persistent runtime writes; the temporary capture artifact lived only in `/dev/shm` (RAM) and was deleted, and the instrumented script was restored byte-for-byte (SHA-256 verified);
- statusLine JSON shape is not a publicly versioned API, so the production adapter must be version-aware and fail closed (report `unknown`/`unsupported`) rather than guess.

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

Exit condition met: both providers are proven with real-machine evidence for the declared v0.1 environment (Ubuntu 26.04 / WSL2). Production provider adapters can now be implemented from the observed schemas above rather than assumptions.

### P1 — local CLI and diagnostics

Implement the stable local surface defined in `docs/INSTALLER_CONTRACT.md`:

- [x] `detect`;
- [ ] `install --plan`;
- [x] `doctor`;
- [x] `show-payload`;
- [x] `status`;
- [ ] `uninstall --plan`.

`detect`, `doctor`, `show-payload`, `status` are implemented in `cmd/ai-limit-notifier` on top of `internal/provider/codex` and `internal/provider/claude`, and have been run on this real machine against the real installed Codex/Claude Code (see the session that closed Claude P0). `install --plan`/`uninstall --plan` are not implemented yet; there is no persistent installation to plan for until P2/P5 exist.

Codex's `show-payload`/`doctor`/`status` pull a live snapshot synchronously via `codex app-server`. Claude Code has no on-demand query interface: it only hands rate-limit data to a configured `statusLine` command on its own refresh cadence. `show-payload --provider claude` therefore accepts a captured payload via `--claude-stdin` (matching the real payload shape) rather than pulling live; turning the passive capture into a continuously available snapshot is P2 work.

`show-payload` must make data minimization independently inspectable by the user. Verified: the JSON it prints for both providers contains only `provider`/`five_hour`/`weekly` (`used_percent`, `reset_at`); it structurally cannot carry the credits/plan/upsell/account metadata or workspace/context_window/model fields present in the raw provider payloads, since the parsing structs never define fields for them.

Exit condition: **met for read-only diagnostics** — a user/coding agent can determine compatibility and see exactly what would be sent before enabling monitoring. Not yet met for the "installed" half (`install --plan`/`uninstall --plan`), which is P5 territory.

### P2 — RAM-only monitoring agent

Implement provider polling/event capture, normalization, threshold decisions, in-memory retry state and HTTPS delivery.

**Status: implemented and run on this real machine, except HTTPS delivery (no server exists yet — P3). The sink is an interface with only a print/test implementation today, exactly as scoped.**

- `internal/agent`: `Core.Observe` applies the 80%-default threshold and RAM-only per-`(provider, window, reset_at)` dedup — 80→90→100 against the same `reset_at` fires once; a new `reset_at` fires again; delivery uses bounded retry/backoff and only marks a window "sent" on confirmed success, so a sink outage doesn't permanently lose a window's event within the process's lifetime (a restart resending is expected — the future server provides durable idempotency).
- `internal/provider/codex` is polled on a bounded, configurable interval (`PollCodex`, clamped to a 30s minimum) via `codex app-server`, same P0-proven interface.
- Claude Code has no on-demand pull, so it's fed passively: `internal/wrapper` implements the statusLine **chaining** wrapper (Claude Code → wrapper → original user command unchanged, plus a best-effort, non-blocking extraction of only the four rate-limit fields) and `internal/claudesock` + `internal/agent.ServeClaudeSocket` carry that snapshot to the agent over a local Unix domain socket under a RAM-backed directory (`XDG_RUNTIME_DIR`, falling back to `/dev/shm`) — never a network socket, never a file the payload is written to.
- **The user's `~/.claude/statusline-command.sh` is never modified.** `ai-limit-notifier install --plan` (read-only) shows the one persistent settings.json change that would be needed to actually wire the wrapper in; nothing writes to `~/.claude/settings.json` yet — that requires a separate, explicit, user-approved step that does not exist in this build. `doctor`/`status` recognize whether the wrapper is already the configured statusLine command and flag the one obvious drift case (wrapper installed with no `--original-command`, which would silently lose the user's real statusLine output).
- `ai-limit-notifier monitor [--dry-run] [--codex-interval] [--threshold]` runs both feeds against a print sink and shuts down gracefully (SIGINT/SIGTERM), removing its socket file. Run on this real machine: real Codex data (5h=100% used, correctly crossing the 80% threshold and firing once) and, via the wrapper invoked against the real (untouched) `statusline-command.sh`, real Claude rate-limit numbers observed earlier this session — see `docs/REAL_MACHINE_VALIDATION.md`.
- Verified on this machine: the wrapper's stdout is byte-for-byte identical to invoking the real script directly for the same input (including ANSI codes and the live-computed reset countdown); no process is left running after SIGTERM; no file anywhere outside the repo/scratch changed (filesystem diff before/after); `~/.claude/settings.json` and `~/.claude/statusline-command.sh` were never touched during this validation (checksums confirmed unchanged before/after).
- "one provider missing doesn't break the other" and Codex timeout/unavailable/malformed-response handling are proven by unit tests using a fake/missing Codex binary (`internal/agent` and `internal/provider/codex` tests) rather than by uninstalling the real Codex/Claude Code from this machine, which would be a destructive, unnecessary step.

Not yet done: an actual `install` that writes the wrapper into `~/.claude/settings.json` (needs explicit owner approval — this session only showed the plan), `uninstall`/full config-drift detection, and the real HTTPS sink (P3).

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

P0 is closed for both providers; both readers are evidenced in `docs/REAL_MACHINE_VALIDATION.md`. Do not repeat P0 validation unless a version/environment change requires revalidation.

Use [`prompts/continue-development.md`](../prompts/continue-development.md) for normal implementation continuation (P1 local CLI onward).
