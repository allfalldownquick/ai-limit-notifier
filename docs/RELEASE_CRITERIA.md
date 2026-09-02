# v0.1 Release Criteria

`v0.1` means the product actually works for its declared supported environment. It is not a documentation-only milestone.

Do not publish a stable/usable release until every required item below is verified.

## Provider collection

- [x] Claude Code usage/reset data is read on a real supported WSL/Linux installation without creating a model request. Proven on Claude Code 2.1.258 / Ubuntu 26.04 / WSL2; see `docs/REAL_MACHINE_VALIDATION.md`. (Production adapter implementation with version-aware fail-closed handling still required — see P0 exit condition in `docs/PROJECT_STATUS.md`.)
- [x] Codex usage/reset data is read on a real supported WSL/Linux installation without creating a model turn/request. Proven on Codex CLI 0.152.1 / Ubuntu 26.04 / WSL2; see `docs/REAL_MACHINE_VALIDATION.md`.
- [ ] 5-hour and weekly windows are normalized correctly when present across every provider declared supported by v0.1.
- [ ] Missing/partial windows remain unknown and never become fake zero values.
- [ ] Provider credentials never leave the local machine through AI Limit Notifier.

## Local runtime guarantees

- [x] Monitoring does not create/update usage state, history, cache, or application runtime-log files locally. Verified by filesystem diff before/after running `detect`/`doctor`/`show-payload`/`status` against real providers on this machine: zero files created/modified anywhere outside intentional source edits.
- [ ] Install-time static files are documented separately from runtime behavior. (No installer exists yet to document.)
- [x] Monitoring adds zero prompts/model context and makes zero LLM calls for the purpose of checking limits. Verified for Codex (`initialize` + `account/rateLimits/read` only, no thread/turn/prompt method) and for Claude (statusLine capture is passive; the CLI's own `--claude-stdin` path makes no model call at all).
- [ ] CPU/network polling is bounded and measured under normal use. (No polling loop exists yet — P2.)

## Installation and diagnostics

- [x] `detect` reports supported providers/environment accurately. Run on this real machine (Ubuntu 26.04/WSL2): reports OS/WSL, codex/claude binary + version, statusLine configuration; exit code distinguishes ready/needs-fix/unsupported.
- [ ] `install --plan` describes every persistent change before applying it. (Not implemented — no installer exists yet.)
- [ ] `install` performs only approved/documented changes. (Not implemented.)
- [x] `doctor` verifies integrations, connectivity, permissions, and runtime invariants without exposing secrets. Run on this real machine; exercises the live Codex reader and Claude statusLine detection, reports "model requests consumed by this check: 0".
- [x] `show-payload` displays the exact normalized data class that can leave the machine and contains no credentials/prompts/project data. Verified against real Codex live data and a real captured Claude statusLine payload; output is structurally limited to `provider`/`five_hour`/`weekly` `used_percent`/`reset_at`.
- [x] `status` works when one provider/window is unavailable. Each provider check is independently wrapped so a Codex or Claude failure prints as `unavailable: <reason>` for that provider only.
- [ ] `uninstall --plan` describes cleanup.
- [ ] `uninstall` removes the service/agent and project-owned integration changes without damaging Claude Code, Codex, WSL, Git, or user projects.

## Server security

- [ ] Production device endpoint requires HTTPS.
- [ ] Each device has a unique revocable credential.
- [ ] Stored device credentials are appropriately protected; raw secrets are not written to logs.
- [ ] API schema is strict and rejects unknown/arbitrary control fields.
- [ ] Request body size is bounded.
- [ ] Provider, percentage, and reset timestamp validation is enforced server-side.
- [ ] Replay/idempotency behavior is tested.
- [ ] Per-device/API rate limits are enforced.
- [ ] There is no server-to-device remote-execution command channel.
- [ ] A stolen device key cannot select arbitrary Telegram recipients or arbitrary Telegram text.

## Durable scheduling

- [ ] API acknowledges an event only after durable persistence.
- [ ] Same provider/window/reset timestamp is idempotent.
- [ ] 80 -> 90 -> 100 does not produce duplicate reset messages for one window.
- [ ] Server restart reloads pending notifications.
- [ ] Overdue unsent notifications are recovered after restart.
- [ ] Telegram failures use bounded retry/backoff and respect `retry_after`/rate limiting.
- [ ] Delivery semantics and the rare crash/duplicate edge case are documented rather than claiming impossible exact-once delivery.

## Notification behavior

- [ ] Default reset threshold is 80% used.
- [ ] Reset notification is scheduled for `reset_at + 1 minute` by default.
- [ ] Same-kind Claude/Codex resets within the configured combine window can produce one useful combined message.
- [ ] Covered combined events do not send a second message after restart.
- [ ] Private Telegram chat delivery works end to end while the local PC is offline at send time.

## Pairing

- [ ] `/start` onboarding is understandable without reading architecture documentation.
- [ ] Pairing codes expire, are single-use, and are protected against guessing abuse.
- [ ] Pairing never requires pasting Claude/OpenAI credentials into Telegram.
- [ ] Device revocation immediately prevents future authenticated submissions from that device credential.

## Code quality

- [ ] `go test ./...` passes.
- [ ] `go vet ./...` passes.
- [ ] repository is `gofmt` clean.
- [ ] CI runs these checks on `main`.
- [ ] security-focused review covers network boundaries, secrets, installer privileges, path handling, command execution, and log redaction.
- [ ] real-machine smoke test is performed after the final relevant changes.

## Public release honesty

- [ ] README lists only environments that were actually tested as supported.
- [ ] Internal/unstable provider interfaces are labeled as compatibility-sensitive where applicable.
- [ ] No claim such as "zero disk writes" is made more broadly than the real guarantee: zero **application local runtime persistence of monitored usage/state**.
- [ ] No claim of absolute/exact-once Telegram delivery is made.
- [ ] Known limitations and uninstall instructions are published.

## Later, not blocking v0.1

These are deliberately not release blockers for the first working beta:

- native Windows provider adapters;
- Telegram channel destination;
- paid Telegram Stars subscriptions;
- ads/sponsored messages;
- elaborate web dashboard;
- advanced hosted history/analytics;
- many additional AI providers.

The first beta should solve one problem reliably before expanding scope.
