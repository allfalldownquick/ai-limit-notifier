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

## Claude Code — P0 partially validated

Status: **STATIC INTERFACE/SCHEMA PROVEN; REAL RUNTIME CAPTURE STILL PENDING**.

Installed version: `2.1.258`.

### Candidate interface

The installed Claude Code binary contains the statusLine schema for rate-limit metadata and documents command-based statusLine input as JSON on stdin.

The relevant installed-version schema was confirmed as:

```json
{
  "rate_limits": {
    "five_hour": {
      "used_percentage": "number 0..100",
      "resets_at": "Unix epoch seconds"
    },
    "seven_day": {
      "used_percentage": "number 0..100",
      "resets_at": "Unix epoch seconds"
    }
  }
}
```

The installed schema also includes an unrelated `spend_limit` window. AI Limit Notifier does not need that field and should ignore it.

Observed semantics from installed-version documentation/code:

- `used_percentage` means **used percentage**, 0–100;
- `resets_at` is Unix epoch seconds;
- `rate_limits` is optional;
- the data is expected only after a normal Claude API response and while a current window exists;
- missing windows must therefore remain unknown rather than becoming zero.

### Existing statusLine configuration

During the later read-only inspection, `~/.claude/settings.json` already contained a command-based `statusLine` pointing to an existing `~/.claude/statusline-command.sh`.

Read-only inspection of that existing script found that it already consumes the four fields required by AI Limit Notifier. No signs of network access or disk writes were found in that script during static inspection. The script was not modified.

This is useful evidence for compatibility, but static script/binary inspection alone is not enough to mark the Claude reader safe.

### Passive capture attempt

A user-approved temporary passive capture mechanism was armed with these constraints:

- only `statusLine.command` was temporarily replaced;
- the exact original `~/.claude/settings.json` bytes were backed up in `/dev/shm`;
- temporary wrapper/collector/socket state existed only in `/dev/shm`;
- no new Claude session, prompt, message, or model request was created for monitoring;
- no monitoring instructions were added to model context;
- no persistent AI Limit Notifier capture/history/cache/log state was written.

No normal active Claude Code session produced a statusLine invocation during the test interval, so **no real statusLine stdin payload was observed**.

The capture was therefore stopped without claiming success.

### Rollback result

The temporary configuration was safely rolled back:

- `settings.json` restored byte-for-byte: **YES**;
- original/final SHA-256 matched: **YES**;
- original statusLine command restored: **YES**;
- temporary `/dev/shm` backup/wrapper/socket removed: **YES**;
- repository remained clean: **YES**.

Observed restored file metadata during validation: mode `0644`, uid `1000`, gid `1000`.

### Model/context result so far

For the passive capture mechanism itself:

- model request created by monitoring: **NO**;
- model context added by monitoring: **NO**;
- temporary capture network activity: **NONE by design/static inspection**;
- notifier-owned persistent runtime writes: **NONE**.

A normal active Claude Code session is still required to prove the runtime payload path end to end.

### Compatibility risks

For Claude Code `2.1.258` on this tested WSL2 environment:

- rate-limit windows are optional;
- rate-limit data appears only after a normal provider response and while the window is current;
- statusLine is tied to an active/trusted Claude Code workspace/session;
- exact invocation cadence has not yet been proven;
- installed-version schema evidence must not be generalized to other Claude Code versions without version-aware compatibility handling.

### Remaining proof required

To complete Claude P0:

1. use a normal active Claude Code session that the user would have run anyway;
2. arm the same passive in-memory capture mechanism;
3. observe a real statusLine JSON stdin payload after a natural Claude response;
4. prove whether both `five_hour` and `seven_day` are present on the user's account at that moment;
5. capture only the four required fields;
6. normalize the observed values into `UsageSnapshot` without inventing missing windows;
7. restore the original statusLine configuration byte-for-byte and verify its hash again.

No prompt/session should be created solely to consume tokens for this validation.

P0 is not complete until the Claude Code reader is proven with a real runtime capture for the declared v0.1 environment, or Claude Code is explicitly scoped out of v0.1 support.
