# v0.1 Release Criteria

`v0.1` means the product actually works for its declared supported environment. It is not a documentation-only milestone.

Do not publish a stable/usable release until every required item below is verified.

## Provider collection

- [ ] Claude Code usage/reset data is read on a real supported WSL/Linux installation without creating a model request.
- [ ] Codex usage/reset data is read on a real supported WSL/Linux installation without creating a model turn/request.
- [ ] 5-hour and weekly windows are normalized correctly when present.
- [ ] Missing/partial windows remain unknown and never become fake zero values.
- [ ] Provider credentials never leave the local machine through AI Limit Notifier.

## Local runtime guarantees

- [ ] Monitoring does not create/update usage state, history, cache, or application runtime-log files locally.
- [ ] Install-time static files are documented separately from runtime behavior.
- [ ] Monitoring adds zero prompts/model context and makes zero LLM calls for the purpose of checking limits.
- [ ] CPU/network polling is bounded and measured under normal use.

## Installation and diagnostics

- [ ] `detect` reports supported providers/environment accurately.
- [ ] `install --plan` describes every persistent change before applying it.
- [ ] `install` performs only approved/documented changes.
- [ ] `doctor` verifies integrations, connectivity, permissions, and runtime invariants without exposing secrets.
- [ ] `show-payload` displays the exact normalized data class that can leave the machine and contains no credentials/prompts/project data.
- [ ] `status` works when one provider/window is unavailable.
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
