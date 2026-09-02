# Validate AI Limit Notifier on a real Claude Code / Codex machine

Copy the prompt below into a **new Claude Code or Codex coding-agent session opened in the local clone of this repository**.

This prompt is deliberately validation-first. The goal is to prove the real provider data sources before building the monitoring agent around assumptions.

```text
I want you to perform the P0 real-machine validation for AI Limit Notifier.

Repository:
https://github.com/allfalldownquick/ai-limit-notifier

Work with the CURRENT local checkout of `main`. Do not create or switch branches unless I explicitly ask you to.

PRIMARY GOAL
Prove, on this actual computer, whether AI Limit Notifier can read Claude Code and Codex 5-hour / weekly usage and reset metadata with:

- zero model requests created solely for monitoring;
- zero prompts/instructions added to model context for monitoring;
- no upload of Claude/OpenAI credentials, prompts, model responses, terminal contents, or project files;
- no AI Limit Notifier runtime persistence of monitored usage/history/cache/log state;
- missing provider data treated as unknown, never as zero.

Do NOT claim success from documentation alone. I need real local evidence from the installed versions on this machine.

PHASE 0 — REPOSITORY AND SAFETY CHECK

Before changing anything:

1. Confirm the repository root and current branch/commit.
2. Read:
   - README.md
   - SECURITY.md
   - docs/ARCHITECTURE.md
   - docs/DECISIONS.md
   - docs/PROJECT_STATUS.md
   - docs/RELEASE_CRITERIA.md
   - docs/PROTOCOL_V1.md
   - docs/INSTALLER_CONTRACT.md
3. Inspect the current Go source/tests.
4. Run existing non-destructive checks that are possible (`gofmt` check, `go test ./...`, `go vet ./...`).
5. Report any contradiction between code and documented security/runtime guarantees before continuing.

PHASE 1 — READ-ONLY MACHINE INVENTORY

Do not make persistent changes yet.

Determine and report:

- host OS/version;
- whether this session is Windows, WSL, native Linux, or another environment;
- WSL distro/version if applicable;
- installed `claude` location and version;
- installed `codex` location and version;
- whether either command is a wrapper/symlink and where it resolves;
- relevant local config locations, but DO NOT print secrets/tokens/cookies;
- whether systemd/user services are available in this environment;
- whether tools useful for read-only verification (for example `strace`, `lsof`, process inspection tools) are already available.

Do not install packages merely to make the inspection easier. If an additional diagnostic tool would materially improve proof, explain why and ask me before installing it.

PHASE 2 — CODEX READ-ONLY RATE-LIMIT VALIDATION

Investigate the installed Codex implementation/version, not an assumed version.

Preferred hypothesis to test:
- local `codex app-server`;
- initialize its protocol as required;
- call the read-only rate-limit method such as `account/rateLimits/read` if that method exists in this installed version.

Requirements:

1. Verify the exact method/interface from the installed binary/source/schema/help where possible.
2. Do NOT create a thread, turn, prompt, completion, chat request, or other inference request merely to inspect limits.
3. Capture the actual response structure while REDACTING any secrets or unrelated account data.
4. Identify which returned fields correspond to:
   - 5-hour usage or remaining percentage;
   - 5-hour reset timestamp;
   - weekly usage or remaining percentage;
   - weekly reset timestamp.
5. Determine whether percentages are `used` or `remaining/left` and document the normalization needed.
6. Verify whether partial/missing windows can occur.
7. Inspect process/network behavior enough to distinguish a local metadata/rate-limit read from a model inference request.
8. Record compatibility-sensitive details: Codex version and protocol/method names used.

If this installed Codex does not expose a safe read-only interface, STOP that provider investigation and report it unsupported for this version. Do not fall back to browser scraping, screen scraping, credential extraction, or a tiny model request.

PHASE 3 — CLAUDE CODE RATE-LIMIT VALIDATION

Investigate the installed Claude Code implementation/version, not an assumed version.

Preferred hypothesis to test:
- Claude Code `statusLine` input exposes rate-limit information that can be captured by a local command without creating an additional model request.

First inspect the existing Claude Code configuration and documentation/help available locally. Do NOT overwrite the user's existing status line/configuration.

If validating `statusLine` requires a persistent configuration change:

1. Show me the exact current relevant configuration (with secrets redacted).
2. Show the exact temporary/project-owned change you propose.
3. Explain how you will preserve and restore any existing user status-line behavior.
4. Ask for my explicit approval BEFORE changing it.

After approval, if needed:

- use the smallest possible capture command/program;
- capture only the status JSON needed to understand the rate-limit fields;
- do not persist monitored usage/history as application state;
- temporary diagnostic output may be printed to the terminal for this explicit validation session, but do not create recurring runtime logs/state files;
- restore any temporary configuration after validation unless the user explicitly asks to keep it.

Determine from real data whether Claude Code exposes:

- 5-hour used percentage;
- 5-hour reset timestamp;
- weekly/seven-day used percentage;
- weekly/seven-day reset timestamp;
- partial/missing window behavior.

Also determine whether observing this data itself creates any extra model request/context usage. The desired integration may observe metadata produced by normal Claude Code activity, but it must not generate an AI request solely to check limits.

If no safe verified source exists in this installed Claude Code version, report it unsupported rather than weakening the security/runtime requirements.

PHASE 4 — RUNTIME-WRITE / SIDE-EFFECT PROOF

The project's promise is `zero local runtime writes by the AI Limit Notifier application`, NOT `zero physical disk writes by the OS or provider applications`.

For each proposed provider reader:

1. Identify every file the AI Limit Notifier PoC itself creates/modifies during normal monitoring.
2. The expected answer is none for monitored usage/history/cache/runtime logs.
3. Distinguish clearly between:
   - existing Claude/Codex normal writes;
   - explicit one-time install/config changes;
   - temporary diagnostics performed only for this validation;
   - writes caused by the proposed AI Limit Notifier runtime.
4. Where practical, use filesystem/process tracing or before/after inspection to support the conclusion.
5. Do not delete or alter unrelated provider/user files as part of this proof.

PHASE 5 — NORMALIZED SNAPSHOT PROOF

Using only real fields observed on this machine, map provider data into the repository's internal model.

Expected conceptual output:

{
  "provider": "codex or claude",
  "five_hour": {
    "used_percent": 0-100,
    "reset_at": "RFC3339 timestamp"
  },
  "weekly": {
    "used_percent": 0-100,
    "reset_at": "RFC3339 timestamp"
  }
}

Rules:

- Do not fabricate a missing window.
- Codex `left/remaining` values must be normalized to `used_percent` only if the real interface proves that semantic.
- Preserve exact reset timestamps before presentation formatting.
- Do not include account IDs, credentials, prompts, project paths, or unrelated metadata in the normalized payload.

PHASE 6 — REPORT BEFORE IMPLEMENTATION

Before writing production provider adapters, give me a concise report for each provider:

Provider: Claude Code / Codex
Installed version:
Environment:
Safe reader found: YES / NO
Interface used:
Creates model request for monitoring: YES / NO / NOT PROVEN
Adds model context for monitoring: YES / NO / NOT PROVEN
5-hour fields available: YES / NO / PARTIAL
Weekly fields available: YES / NO / PARTIAL
Percentage semantic: used / remaining / unknown
Reset timestamp available: YES / NO / PARTIAL
AI Limit Notifier runtime writes required: NONE / describe
Provider credentials uploaded: NO / NOT PROVEN
Compatibility risks:
Evidence/commands used:

Then state one of:

A. BOTH PROVIDERS PROVEN — propose the smallest production adapter implementation.
B. ONE PROVIDER PROVEN — implement only the proven provider and leave the other explicitly unsupported/pending.
C. NEITHER PROVEN — do not build guessed adapters; explain exactly what blocks us.

Do not mark P0 complete merely because a command ran. P0 is complete only when the actual fields, semantics, side effects and no-model-call property are sufficiently demonstrated.

PHASE 7 — IMPLEMENT ONLY AFTER EVIDENCE

If at least one provider is proven and there is no unresolved security contradiction, you may propose implementation in `main`.

Before editing, tell me:

- files/packages you intend to add/change;
- how the adapter remains read-only;
- how runtime monitored state remains RAM-only;
- how you will test parsing/normalization without embedding real account data;
- how `doctor` and `show-payload` will expose evidence safely.

Then wait for my approval before production implementation if the validation required any unexpected architecture/security change. Ordinary implementation consistent with the existing documented architecture may proceed when I explicitly tell you to continue.

WHEN SAVING VALIDATION RESULTS

After the real-machine validation is complete, update the repository documentation with sanitized facts that are useful to future development:

- provider versions tested;
- verified local interface/method names;
- verified field semantics;
- supported/unsupported status;
- known compatibility risks;
- commands/procedure needed to reproduce the validation without secrets.

Never commit:

- access tokens;
- cookies;
- Authorization headers;
- account secrets;
- private prompts/responses;
- unrelated project contents;
- raw diagnostic captures containing sensitive data.

Run repository checks again after any committed code/documentation change and report the final `main` commit/status.
```

## What success looks like

The validation is successful only if the coding agent can show real local evidence that at least one provider exposes the required rate-limit metadata without creating a monitoring inference request and without requiring AI Limit Notifier runtime usage-state persistence.

The next implementation should be based on those observed fields, not on guessed provider schemas.
