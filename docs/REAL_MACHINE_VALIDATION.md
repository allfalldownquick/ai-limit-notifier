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

## Claude Code — P0 still pending

Installed version: `2.1.258`.

The binary contains strings/symbols related to `rate_limits`, `five_hour`, `seven_day`, `utilization`, and `resets_at`, but this is **not sufficient evidence** of the runtime statusLine payload shape or semantics.

At the time of the initial validation:

- no `statusLine` was configured in the relevant global config;
- no active Claude Code process/session was available for capture;
- no model request was created solely for validation;
- no safe Claude reader was claimed.

Next required proof:

1. determine the exact statusLine schema for the installed Claude Code version using read-only inspection where possible;
2. if real capture requires a temporary statusLine configuration, obtain explicit user approval first;
3. capture only the minimum rate-limit fields during a normal Claude Code session, not by creating a prompt/session solely to consume tokens for monitoring;
4. restore the prior configuration exactly after the test;
5. prove 5-hour/weekly percentage and reset timestamp semantics from real observed data.

P0 is not complete until the Claude Code reader is either proven safe for the declared v0.1 environment or that environment is explicitly scoped out as unsupported.
