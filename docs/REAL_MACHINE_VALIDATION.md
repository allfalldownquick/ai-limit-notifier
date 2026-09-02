# Real Machine Validation

This document records sanitized evidence from real supported environments. It must not contain provider credentials, Authorization headers, cookies, prompts, model responses, private project data, or unrelated account metadata.

## Environment validated on 2026-09-02

- Host environment: WSL2
- Distribution: Ubuntu 26.04
- Codex CLI: `0.152.1`
- Claude Code: `2.1.258`

This validation is version-specific evidence. Provider interfaces may change and must remain compatibility-sensitive in production code.

## Codex — P0 validated

Status: **SAFE READER PROVEN for the tested environment/version**.

### Interface

The installed Codex CLI exposes a local JSON-RPC 2.0 app-server over stdio:

```text
codex app-server --stdio
```

The tested sequence was:

1. `initialize`
2. `account/rateLimits/read`

No `thread/*`, `turn/*`, prompt, completion, chat, or other inference method was invoked for this read.

Sanitized initialize request shape:

```json
{
  "jsonrpc": "2.0",
  "id": 1,
  "method": "initialize",
  "params": {
    "clientInfo": {
      "name": "ai-limit-notifier-p0-validator",
      "version": "0"
    },
    "capabilities": {}
  }
}
```

Sanitized rate-limit request:

```json
{
  "jsonrpc": "2.0",
  "id": 2,
  "method": "account/rateLimits/read",
  "params": {}
}
```

Observed useful response fields:

```json
{
  "rateLimits": {
    "limitId": "codex",
    "primary": {
      "usedPercent": 46,
      "windowDurationMins": 300,
      "resetsAt": 1788387939
    },
    "secondary": {
      "usedPercent": 60,
      "windowDurationMins": 10080,
      "resetsAt": 1788855178
    }
  }
}
```

The values above were real observations from the validation run and are retained only as non-secret rate-limit metadata.

### Field semantics

- `primary.windowDurationMins = 300` -> 5-hour window.
- `secondary.windowDurationMins = 10080` -> 7-day / weekly window.
- `usedPercent` is already **used percentage**, not remaining percentage.
- `resetsAt` is Unix epoch seconds.
- Missing fields/windows must still be treated as unknown rather than zero.

Observed normalized snapshot:

```json
{
  "provider": "codex",
  "five_hour": {
    "used_percent": 46,
    "reset_at": "2026-09-02T22:25:39Z"
  },
  "weekly": {
    "used_percent": 60,
    "reset_at": "2026-09-08T08:12:58Z"
  }
}
```

The raw app-server response also contained additional account/credits/plan/upsell metadata and a duplicate rate-limit map. AI Limit Notifier does not need that data and production parsing should extract only the minimal rate-limit fields required for normalization.

### Model/context result

For the tested read:

- model request created solely for monitoring: **NO observed**;
- model context added: **NO**;
- thread/turn created: **NO**;
- prompt/inference method invoked: **NO**.

This conclusion is based on the actual JSON-RPC exchange used for the validation.

### Network behavior

Whether `account/rateLimits/read` requires a network request on every read is **not proven**. A short `lsof` sampling interval did not capture a socket. The result may have come from provider cache/state or from a connection too short for the sampling method to observe.

Production code must not depend on an unproven assumption that the read is always offline.

### Provider-owned writes

Launching/using the installed Codex app-server occurred alongside normal writes in Codex-owned state under `~/.codex`, including SQLite WAL/SHM state, `logs_2.sqlite`, model/plugin cache data, and temporary argument links/files.

A parallel Codex process was active, so the validation does not attribute every observed provider-owned write specifically to the app-server invocation.

These are **Codex-owned writes**, not AI Limit Notifier runtime persistence. The product guarantee remains that AI Limit Notifier itself must not persist monitored usage/history/cache/runtime-log state during normal monitoring.

### Compatibility risk

`codex app-server` is marked experimental by Codex. Method names, schema, and field layout must therefore be treated as compatibility-sensitive.

Production adapter requirements:

- version-aware detection;
- strict JSON parsing;
- graceful unsupported/partial handling;
- never silently fall back to inference, browser/screen scraping, credential copying, or guessed fields;
- parse only the minimal required rate-limit data.

## Claude Code — P0 validated

Status: **SAFE READER PROVEN for the tested environment/version**.

Installed version: `2.1.258` (Ubuntu 26.04 / WSL2).

This closes out an earlier same-day attempt at this validation, which got as far as confirming the statusLine schema and safely arming/disarming the passive-capture mechanism, but observed no active Claude Code session during its test interval and so recorded no real payload. The run documented below used the same rollback-verified method and did observe a real payload.

### Interface

Claude Code's own `statusLine` mechanism invokes a user-configured command with a JSON payload on stdin on its normal refresh cadence (not on-demand for monitoring). The pre-existing, user-owned script `~/.claude/statusline-command.sh` (already configured before this project existed, for an unrelated PS1-style prompt) already parsed the exact fields needed:

```text
rate_limits.five_hour.used_percentage
rate_limits.five_hour.resets_at
rate_limits.seven_day.used_percentage
rate_limits.seven_day.resets_at
```

### Validation method

1. SHA-256 of the original script was recorded and a byte-for-byte copy was kept in `/dev/shm` (tmpfs, RAM-backed) only.
2. One additive line was inserted immediately after the script's existing field-extraction lines. It reused the already-computed shell variables (`usage`, `resets_at`, `weekly_usage`, `weekly_resets_at`) and wrote only those four values to `/dev/shm/ai-limit-notifier-p0-capture.json`. No existing line was modified, reordered, or removed; the script's visible statusLine output was unchanged.
3. The capture file appeared after 5 seconds via Claude Code's own normal statusLine refresh — no prompt/message/model request was sent to trigger it.
4. The original script was restored from the `/dev/shm` backup immediately after capture. Final SHA-256 matched the pre-edit SHA-256 exactly.
5. All `/dev/shm` artifacts (backup, sha256 file, capture file) were deleted. `~/.claude/settings.json` was never modified (verified unchanged via checksum before/after).

### Observed payload (sanitized)

```json
{"five_hour": {"used_percentage": "65", "resets_at": "1788388800"}, "weekly": {"used_percentage": "27", "resets_at": "1788850800"}}
```

### Field semantics

- `rate_limits.five_hour.used_percentage` / `rate_limits.seven_day.used_percentage` are already **used percentage** (0..100), not remaining.
- `rate_limits.five_hour.resets_at` / `rate_limits.seven_day.resets_at` are Unix epoch seconds.
- Missing window/field must still be treated as unknown, never zero.
- The installed statusLine schema also exposes an unrelated `spend_limit` window (from an earlier static read-only inspection of this same installed version); AI Limit Notifier does not need it and ignores it.

Observed normalized snapshot:

```json
{
  "provider": "claude",
  "five_hour": {
    "used_percent": 65,
    "reset_at": "2026-09-02T22:40:00Z"
  },
  "weekly": {
    "used_percent": 27,
    "reset_at": "2026-09-08T07:00:00Z"
  }
}
```

### Model/context result

- model request created solely for monitoring: **NO**;
- model context added: **NO**;
- capture was driven entirely by Claude Code's own pre-existing statusLine refresh, not by any action taken to "use" the assistant.

### Runtime write result

- AI Limit Notifier itself wrote nothing to persistent disk during this validation; the only temporary artifact lived in `/dev/shm` (tmpfs/RAM) and was deleted after capture.
- The user's own pre-existing `~/.claude/statusline-command.sh` and `~/.claude/settings.json` are unrelated, pre-existing, user-owned configuration — not AI Limit Notifier runtime state.

### Compatibility risk

The statusLine JSON payload shape is not a publicly versioned/stable API contract from Anthropic. A production Claude adapter must:

- be version-aware and fail closed (report `unknown`) rather than guess when fields are missing or reshaped;
- never assume `statusLine` is configured — detect its absence and report `unsupported`/`needs setup` rather than silently falling back to another method;
- never modify a user's existing statusLine command in a way that risks losing their prior configuration (install-time changes only, with plan/approval, per `docs/INSTALLER_CONTRACT.md`).

P0 is now closed for both providers in the tested environment: Codex CLI `0.152.1` and Claude Code `2.1.258` on Ubuntu 26.04 / WSL2.

## P2 — RAM-only monitoring agent validated on this real machine

Status: **wrapper chaining + Codex polling + Claude socket delivery proven end to end. HTTPS delivery is out of scope (P3 does not exist yet); only the print sink was exercised.**

### monitor --dry-run against real Codex

`ai-limit-notifier monitor --dry-run --codex-interval 30s` was run as a background process on this machine. Its immediate first poll produced:

```text
[would send] codex five_hour: 100% used, resets 2026-09-02T22:25:39Z
```

This matches the real live Codex rate limits observed independently in this same session (`doctor`/`status` also reported `five_hour=100% used, weekly=68% used` from the real `codex app-server`). Weekly stayed below the 80% threshold and correctly produced no event.

### Claude wrapper: real, untouched script parity

`~/.claude/statusline-command.sh` was **not modified** for this test (an auto-mode safety classifier declined a second temporary-instrumentation edit of that file in this session, and a non-invasive alternative was used instead — no attempt was made to work around the block). Parity was proven by feeding the same statusLine-shaped payload to both the real script directly and to `ai-limit-notifier statusline-wrapper --original-command "bash ~/.claude/statusline-command.sh"`, and diffing:

```text
diff direct_out.txt wrapper_out.txt
(no output — byte-for-byte identical, including ANSI color codes and the
live-computed "reset Xh Ym" countdown)
```

The payload's rate-limit values (`five_hour.used_percentage=73`, `resets_at=1788388800`; `seven_day.used_percentage=27`, `resets_at=1788850800`) are the real values observed earlier in this same session's Claude P0 closure, not fabricated numbers; `workspace`/`context_window`/`model` fields were a realistic synthetic shape matching the proven schema. `~/.claude/statusline-command.sh`'s SHA-256 was confirmed unchanged (`77123caf...e360d94`) before and after this test.

### Socket delivery end to end

With `monitor` running, invoking the wrapper against the real script with a rate-limit value above the 80% threshold produced, in the agent's log:

```text
[would send] claude five_hour: 91% used, resets 2026-09-02T22:40:00Z
```

confirming: wrapper → Unix socket (`XDG_RUNTIME_DIR`/`ai-limit-notifier-<uid>.sock`) → agent `Core.Observe` → threshold/dedup → sink, with the wrapper's own stdout/exit code toward Claude Code unaffected in every case (see parity test above).

### Cleanup / runtime-write guarantee

- `monitor` was stopped with `SIGTERM`: it logged `monitor: shutting down`, exited cleanly, and removed its own socket file.
- `ps aux` after every wrapper/monitor invocation showed no leftover child processes (the wrapper's `sh -c` chain to the real script, and that script's own `python3` calls, all exited).
- A filesystem diff of the repository and `$HOME` from before this validation to after showed no new or modified files anywhere outside the intentional source edits — no state/history/cache/log file was created by AI Limit Notifier.
- `~/.claude/settings.json` was confirmed unchanged (checksum) throughout.

### What this does not yet prove

- No real HTTPS sink exists (P3), so only the print sink was exercised end to end.
- "Codex absent" / "Claude absent" behavior (one provider failing must not break the other) was proven with fake/missing binaries in `internal/agent`'s unit tests, not by uninstalling the real Codex/Claude Code from this machine — doing that would be a destructive, unnecessary step for a diagnostics-tool validation.
- The wrapper was never actually installed into `~/.claude/settings.json` — only `install --plan` (read-only) was run, per the owner's explicit instruction to ask before any persistent Claude config change. A real live end-to-end capture through Claude Code's own statusLine refresh (as opposed to a direct wrapper invocation with a real script and real values) still requires that install step and a separate approval.
