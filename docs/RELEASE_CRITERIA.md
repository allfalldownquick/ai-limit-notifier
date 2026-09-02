# v0.1 Release Criteria

`v0.1` means the product actually works for its declared supported environment. It is not a documentation-only milestone.

Do not publish a stable/usable release until every required item below is verified.

## Provider collection

- [x] Claude Code usage/reset data is read on a real supported WSL/Linux installation without creating a model request. Proven on Claude Code 2.1.258 / Ubuntu 26.04 / WSL2; see `docs/REAL_MACHINE_VALIDATION.md`. (Production adapter implementation with version-aware fail-closed handling still required — see P0 exit condition in `docs/PROJECT_STATUS.md`.)
- [x] Codex usage/reset data is read on a real supported WSL/Linux installation without creating a model turn/request. Proven on Codex CLI 0.152.1 / Ubuntu 26.04 / WSL2; see `docs/REAL_MACHINE_VALIDATION.md`.
- [x] 5-hour and weekly windows are normalized correctly when present across every provider declared supported by v0.1. Proven for Codex (real live read) and Claude (real observed payload via the wrapper); unit-tested for both adapters' window-kind/duration mapping.
- [x] Missing/partial windows remain unknown and never become fake zero values. Unit-tested at every layer that can drop a field: both provider adapters (`windowFrom`/`ParsePayload`), and `internal/agent.Core.Observe` (a nil window never fires, and never blocks the other window firing).
- [x] Provider credentials never leave the local machine through AI Limit Notifier. No code path in this repository reads, stores, or transmits Claude/OpenAI credentials — there is no credential-handling code at all, only the four normalized rate-limit fields (structurally enforced by the narrow parsing structs; see the "no accidental raw metadata propagation" tests).

## Local runtime guarantees

- [x] Monitoring does not create/update usage state, history, cache, or application runtime-log files locally. Verified by filesystem diff before/after running `detect`/`doctor`/`show-payload`/`status` against real providers on this machine: zero files created/modified anywhere outside intentional source edits.
- [ ] Install-time static files are documented separately from runtime behavior. (No installer exists yet to document.)
- [x] Monitoring adds zero prompts/model context and makes zero LLM calls for the purpose of checking limits. Verified for Codex (`initialize` + `account/rateLimits/read` only, no thread/turn/prompt method) and for Claude (statusLine capture is passive; the CLI's own `--claude-stdin` path makes no model call at all).
- [x] CPU/network polling is bounded and measured under normal use. `PollCodex` clamps to a 30s minimum interval regardless of configuration (unit-tested), and the Claude side is push-only (no polling at all). Not yet measured over a long production run — only a short real-machine smoke test.

## Installation and diagnostics

- [x] `detect` reports supported providers/environment accurately. Run on this real machine (Ubuntu 26.04/WSL2): reports OS/WSL, codex/claude binary + version, statusLine configuration; exit code distinguishes ready/needs-fix/unsupported.
- [x] `install --plan` describes every persistent change before applying it. Implemented for the one persistent change that exists so far (the Claude statusLine wrapper's `~/.claude/settings.json` edit): read-only, shows current vs. proposed `statusLine.command`. Run on this real machine. Does not yet cover a full binary/service installer (P5).
- [ ] `install` performs only approved/documented changes. (Not implemented.)
- [x] `doctor` verifies integrations, connectivity, permissions, and runtime invariants without exposing secrets. Run on this real machine; exercises the live Codex reader and Claude statusLine detection, reports "model requests consumed by this check: 0".
- [x] `show-payload` displays the exact normalized data class that can leave the machine and contains no credentials/prompts/project data. Verified against real Codex live data and a real captured Claude statusLine payload; output is structurally limited to `provider`/`five_hour`/`weekly` `used_percent`/`reset_at`.
- [x] `status` works when one provider/window is unavailable. Each provider check is independently wrapped so a Codex or Claude failure prints as `unavailable: <reason>` for that provider only.
- [ ] `uninstall --plan` describes cleanup.
- [ ] `uninstall` removes the service/agent and project-owned integration changes without damaging Claude Code, Codex, WSL, Git, or user projects.

## Server security

- [ ] Production device endpoint requires HTTPS. The server binary itself listens plain HTTP by design (TLS is meant to be terminated by a reverse proxy in front of it); this is a deployment-time property that hasn't been deployed/verified yet — see the P3 deployment plan.
- [x] Each device has a unique revocable credential. `devices.token_hash` is `UNIQUE`; revocation is idempotent and immediately rejects future auth. Tested (`TestDeviceAuthLifecycle`).
- [x] Stored device credentials are appropriately protected; raw secrets are not written to logs. Only a SHA-256 hash is persisted; the raw token is returned once at creation. Tested that it never appears in an error response body (`TestTokenNeverAppearsInErrorBody`); reviewed that no log statement anywhere prints a device or bot token (the one place a raw token is printed — `bootstrap-device`'s stdout — is the intentional, explicit, one-time local/test credential handoff, not a log).
- [x] API schema is strict and rejects unknown/arbitrary control fields. `DisallowUnknownFields` + trailing-content rejection; no `map[string]any` anywhere in the request types. Tested, including that device-supplied `chat_id`/`message`/`command`/`url`/`path` fields are all rejected outright (`TestUsageCannotSupplyTelegramDestinationOrCommands`).
- [x] Request body size is bounded. `http.MaxBytesReader` at 8 KiB; tested (413).
- [x] Provider, percentage, and reset timestamp validation is enforced server-side. Reuses `internal/domain`'s tested validation plus server-specific horizon/staleness bounds; tested for invalid provider, invalid schema version, invalid percent (negative and >100), invalid/absurd timestamps.
- [x] Replay/idempotency behavior is tested. Store-level (`TestUpsertPendingEvent*`) and API-level (`TestUsageRetrySamePayloadIsIdempotent`).
- [x] Per-device/API rate limits are enforced. `golang.org/x/time/rate`, keyed by device ID post-auth and by TCP peer address pre-auth (never `X-Forwarded-For` — no trusted-proxy model exists yet, see the P3 deployment plan for the caveat this implies behind a reverse proxy). Tested (`TestUsageDeviceRateLimitEnforced`, `TestUsageIPRateLimitEnforcedOnBadAuth`). The per-IP limiter map is bounded (TTL eviction + hard cap, fails closed for a brand-new address once reached) rather than growing per distinct address forever; tested (`TestIPLimiterEvictsStaleEntries`, `TestIPLimiterHardCapFailsClosedForNewAddresses`).
- [x] Every pooled SQLite connection — not just the first one the pool happens to hand out — enforces `foreign_keys` and `busy_timeout`, applied via per-connection DSN pragmas rather than a one-shot `Exec`. Tested against genuinely concurrent, simultaneously-held pooled connections, including that a foreign-key violation is rejected on every one of them (`TestEveryPooledConnectionGetsPragmas`, `TestForeignKeysEnforcedOnEveryPooledConnection`). Durability policy is explicit and deliberate: WAL + `synchronous=FULL` (every commit fsync'd before `persisted:true` is returned), chosen for v0.1's low request volume over the write-throughput a looser setting would buy.
- [x] There is no server-to-device remote-execution command channel. No such endpoint, field, or response shape exists anywhere in the API.
- [x] A stolen device key cannot select arbitrary Telegram recipients or arbitrary Telegram text. Destination comes only from server-side `users.telegram_chat_id`; text is built only in `scheduler.buildMessage` from persisted enum-like fields. Tested.
- [x] The client HTTPS sink never follows an HTTP redirect, so the device bearer token can never reach a redirect target on a different host or over plain HTTP. `CheckRedirect` returns `http.ErrUseLastResponse`; a 3xx is treated as a failed submission. Tested with a redirect target that fails the test if it is ever contacted at all (`TestSendRejectsSameHostRedirect`, `TestSendRejectsRedirectToDifferentHost`, `TestSendRejectsRedirectToPlainHTTPTarget`, `TestSendNeverSendsBearerTokenToRedirectTarget`).

## Durable scheduling

- [x] API acknowledges an event only after durable persistence. `UpsertPendingEvent` commits before the handler responds; a failed write returns 500 without `accepted`/`persisted`. Tested.
- [x] Same provider/window/reset timestamp is idempotent. Tested at both the store and API layer.
- [x] 80 -> 90 -> 100 does not produce duplicate reset messages for one window. Tested (`TestObserveSameResetWindowNeverDuplicates` at the P2 agent layer; `TestUpsertPendingEventIdempotentRetry`/`TestUsageRetrySamePayloadIsIdempotent` at the server layer for repeated submissions against one `reset_at`).
- [x] Server restart reloads pending notifications. Proven at the Go-test level (`internal/integration`'s real-Codex/real-HTTP/real-SQLite test, closing and reopening the store) and at the OS-process level (a real `ai-limit-server` binary was `SIGTERM`'d and relaunched against the same SQLite file before `send_at`, and delivered correctly afterward — see the P3 report).
- [x] Overdue unsent notifications are recovered after restart. Same evidence as above: `send_at` was already in the past relative to the fast-forwarded/real clock at the moment recovery was checked, in both the unit test and the real-process demo.
- [x] Telegram failures use bounded retry/backoff and respect `retry_after`/rate limiting. `TelegramDelivery` parses `retry_after`; the scheduler honors it via `RetryableError.After`, otherwise applies bounded exponential backoff. Tested against a local fake Telegram server (never the real API).
- [x] Two concurrent delivery attempts for the same event cannot both succeed. `ClaimEvent` is an atomic conditional `UPDATE`, proven under real concurrent goroutines against a real shared SQLite file (`TestClaimEventOnlyOneWinnerUnderRealConcurrency`) and at the scheduler layer with two independent `Store`+`Scheduler` instances sharing one file, standing in for two server processes (`TestTwoConcurrentSchedulersDeliverOnce`). A claim abandoned by a crash (stuck in `sending`) is recovered after a bounded staleness window (`TestClaimEventRecoversStaleSending`, `TestTickRecoversStaleSendingEvent`); the resulting rare duplicate if the crash landed between a successful send and `MarkSent` is deliberately proven, not hidden (`TestCrashBetweenSendSuccessAndMarkSentCanDuplicate`).
- [x] Delivery semantics and the rare crash/duplicate edge case are documented rather than claiming impossible exact-once delivery. Documented in `internal/server/scheduler`'s and `internal/server/delivery`'s package comments and in `docs/PROTOCOL_V1.md`'s existing at-least-once section.

## Notification behavior

- [x] Default reset threshold is 80% used. `domain.DefaultScheduleThreshold = 80`, used as `monitor`'s default and the server's `UpsertPendingEvent` gate; unit-tested (79.9% never fires, 80% fires exactly once) at both the P2 agent layer and the P3 server layer.
- [x] Reset notification is scheduled for `reset_at + 1 minute` by default. `store.UpsertPendingEvent` computes `send_at = reset_at + 1m` unconditionally; tested to the second boundary (`TestSendAtIsExactlyResetAtPlusOneMinute`).
- [x] Same-kind Claude/Codex resets within the configured combine window can produce one useful combined message. Tested at the store layer (`TestCombineWithinWindow`, `TestNoCombineOutsideWindow`, `TestNoCombineAcrossWindowKinds`) and the scheduler layer against the actual rendered `FakeDelivery` payload, not just DB status (`TestTickCombinesMessageForCoveredEvent`, `TestCombinedNotificationNotResentAfterRestart`) — e.g. `"Your Codex and Claude 5-hour usage limits should be available again now."`. Wording is deliberately cautious ("should be available again now", never "has reset") since the server never re-verifies with the provider after `send_at`; pinned by template tests (`TestBuildMessage*`).
- [x] Covered combined events do not send a second message after restart. `TestCoveredEventNeverResendsAfterRestart` closes and reopens the store after covering and confirms the covered event never becomes due.
- [ ] Private Telegram chat delivery works end to end while the local PC is offline at send time. The mechanism doesn't depend on the local PC at send time by construction (delivery only reads server-side SQLite state), but this has not been exercised against a real Telegram bot/chat — deliberately, since P4 owns pairing/onboarding and the project's real bot token must not be used yet.

## Pairing

- [ ] `/start` onboarding is understandable without reading architecture documentation.
- [ ] Pairing codes expire, are single-use, and are protected against guessing abuse.
- [ ] Pairing never requires pasting Claude/OpenAI credentials into Telegram.
- [ ] Device revocation immediately prevents future authenticated submissions from that device credential.

## Code quality

- [x] `go test ./...` passes.
- [x] `go vet ./...` passes.
- [x] repository is `gofmt` clean.
- [x] CI runs these checks on `main` (GitHub Actions, verified green on the P0/P1 commit).
- [ ] security-focused review covers network boundaries, secrets, installer privileges, path handling, command execution, and log redaction. P3's server surface got one dedicated pass covering exactly this checklist (SQL injection, auth bypass/timing, plaintext tokens, log redaction, permissive JSON, arbitrary Telegram fields, command execution/SSRF/path traversal, race conditions via `-race`, DoS/timeouts/body limits, reverse-proxy trust assumptions — see the P3 report). P1/P2 code was reviewed incrementally as each piece was built (e.g. the `sh -c` command-string boundary in the statusLine wrapper, the Unix socket's 0600 permission and stale-vs-live bind check) but no single consolidated pass has covered the whole repository at once.
- [x] real-machine smoke test is performed after the final relevant changes. Both the P1 and P2 rounds were run against the real installed Codex/Claude Code on this machine (see `docs/REAL_MACHINE_VALIDATION.md`).

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
