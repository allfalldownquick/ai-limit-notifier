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

**Status: implemented, tested, and proven locally on this real machine** (real Codex data, real HTTP, real SQLite file, a real process-level restart). Not yet deployed anywhere — see the deployment plan at the end of the P3 session for the VPS step, which is still pending explicit approval.

**Build requirement: Go 1.25.0 or newer.** `go.mod`'s `go` directive moved from `1.23.0` to `1.25.0` when `modernc.org/sqlite` was added. This isn't a one-off "latest picked something newer than needed": every current `modernc.org/sqlite` release from v1.48.0 onward — including v1.58.0, the one this repo uses — declares `go 1.25.0` as its own floor (checked directly against the module proxy), largely because its `modernc.org/libc` dependency carries the same floor. An older driver release does exist with a lower requirement (v1.38.0 needs only `go 1.23.0`), but pinning our durability layer to an increasingly stale, unpatched SQLite driver just to avoid a Go version bump is the wrong tradeoff — so `go 1.25.0` is kept deliberately, not accidentally.

- `cmd/ai-limit-server`: `serve` (API + scheduler) and `bootstrap-device` (local/test-only user+device creation — requires direct access to the DB file, so it is not a network-reachable backdoor).
- `internal/server/store`: SQLite via `modernc.org/sqlite` (pure Go, no CGO/system SQLite dependency). Every pooled connection — not just the first one `sql.Open` happens to hand out — gets `foreign_keys=1`, `busy_timeout=5000`, `journal_mode=WAL`, `synchronous=FULL`, via modernc.org/sqlite's per-connection DSN pragmas rather than a one-shot `db.Exec` (PRAGMAs are connection-scoped state; verified with real, simultaneously-held pooled connections, including that foreign-key violations are rejected on every one of them, not just an early one). `synchronous=FULL` is a deliberate durability-over-throughput choice for v0.1's low request volume — every commit is fsync'd before the API acknowledges `persisted:true`. A small versioned migration runner; parameterized queries only (no string-built SQL anywhere). `UpsertPendingEvent` implements the full idempotency/combine rule set from `docs/PROTOCOL_V1.md` in one transaction: retry-safe, in-place reset_at rollover, never recreating an already-sent/covered window, and linking same-user/same-window-kind/different-provider events within a configurable combine window (default 10 minutes) — never across `five_hour`/`weekly`. Delivery uses an atomic claim (`ClaimEvent`, a conditional `UPDATE ... WHERE status='pending'`) so two concurrent `Tick` calls — including two separate `ai-limit-server` processes sharing one database file — can never both deliver the same event; a claim whose owner crashed (never reaching `MarkSent`/a failure record) is recovered after a bounded staleness window, which is also the one place a rare duplicate delivery can occur (the documented at-least-once tradeoff, deliberately proven by a test rather than hidden).
- `internal/server/auth`: `crypto/rand`-generated device tokens, `crypto/sha256` hash stored (never the raw token), `crypto/subtle` constant-time comparison.
- `internal/server/api`: the strict `POST /api/v1/usage` endpoint plus `GET /healthz` (unauthenticated) and `GET /api/v1/status` (authenticated) from `docs/PROTOCOL_V1.md`. Strict JSON (`DisallowUnknownFields` + trailing-content rejection), bounded body (8 KiB via `http.MaxBytesReader`), schema/provider allowlists, `used_percent`/timestamp plausibility bounds (reusing `internal/domain`'s already-tested validation, plus stricter server-side horizon/staleness checks), a bounded concurrency limiter, and `http.Server` timeouts/`MaxHeaderBytes`. A durable ack (`accepted`+`persisted`) is only ever returned after the SQLite write commits. Rate limiting (`golang.org/x/time/rate`) is per-device (bounded by provisioned device count) and per-IP; the per-IP limiter map is itself bounded — a TTL sweep evicts idle addresses and a hard cap makes it fail closed for a brand-new address rather than grow without bound, since a public endpoint can be hit from an unbounded number of distinct addresses regardless of beta scale.
- `internal/server/scheduler`: polls `store.DueEvents` (SQLite is the sole source of truth — a fresh process just re-queries the same table, so restart recovery is not a special code path) and delivers through the `delivery.Delivery` interface with bounded exponential backoff, honoring a transport's `RetryableError.After` (e.g. Telegram's `retry_after`). A window is only marked `sent` after a confirmed successful send. Notification wording is deliberately cautious ("...should be available again now", not "...has reset") since the server never re-queries the provider after `send_at` to confirm the window actually rolled over — it only knows `send_at` arrived. A combined notification (two providers, one window kind, close `reset_at`s) is rendered as one message naming both providers; the covered half is never sent separately, including after a restart.
- `internal/server/delivery`: the `Delivery` interface, `FakeDelivery` (used by every test and by `serve` itself whenever `AI_LIMIT_NOTIFIER_TELEGRAM_BOT_TOKEN` is unset — wrapped in a thin stdout-logging decorator so the no-bot-token path stays observable), and a real `TelegramDelivery` (bot token from env only, never logged, one HTTP attempt per call, honors `retry_after`). `TelegramDelivery` is tested only against a local fake HTTP server — no production bot token has been used anywhere in this session.
- `internal/sink`: the production `agent.Sink` the local P2 agent uses — `sink.New` refuses non-HTTPS endpoints unless the host is loopback (so local testing stays possible without weakening the production requirement), sends only the four normalized fields, never logs the bearer token, and requires a durable `accepted`+`persisted` acknowledgement before treating a send as successful. It also never follows an HTTP redirect (`CheckRedirect` returns `http.ErrUseLastResponse`): a 3xx is treated as a failure rather than transparently chased, so the bearer token can never reach a redirect target on another host or over plain HTTP (tested). `cmd/ai-limit-notifier monitor` gained `--server-url`; the device token is read only from `AI_LIMIT_NOTIFIER_DEVICE_TOKEN` (never a CLI flag, so it never appears in `ps`/shell history).
- Real local proof (`internal/integration`'s `TestLocalRealAgentToServerToSQLiteToFakeDelivery`, plus a separate-process demonstration using the built binaries): a real Codex read on this machine, submitted over real HTTP into a real SQLite file, surviving a genuine close/reopen of the store (and, separately, a real OS-process `SIGTERM`+relaunch of `ai-limit-server`) before `send_at` arrived, recovered by the scheduler afterward, and delivered exactly once with no duplicate. Filesystem diff before/after showed no persistent file created anywhere by AI Limit Notifier outside the server's own configured SQLite path; the local agent's own zero-runtime-write guarantee (P2) was reconfirmed unaffected.
- Not yet done inside P3's own scope: a real Telegram bot token has never been exercised (by design — P4 owns pairing/onboarding, and the owner's production bot token must not be used yet); `install`/deployment automation for the server binary itself.

The device API must never accept arbitrary shell commands, URLs, local paths, Telegram text, or destinations. Verified: the wire request struct has no field capable of expressing any of those, and a strict decoder rejects any attempt to add one (unit-tested).

### P4 — Telegram private-chat onboarding

**Status: implemented, tested (192 tests passing, `-race` clean), and proven in a full local end-to-end run with a real bot worker, a real pairing exchange, real `link`/`monitor` subprocesses, a real Codex read, and a simulated server restart between ACK and delivery.** No real Telegram bot token has been used anywhere — the only faked component in every test, including the full E2E, is the Telegram HTTP endpoint itself. Not yet deployed anywhere.

`/start -> one-time pairing code -> local link -> device appears connected` is implemented exactly as scoped:

- `internal/server/auth`: pairing codes are 10 Crockford Base32 characters (`XXXX-XXXX-XX`, no `I`/`L`/`O`/`U`), ~50 bits of entropy, generated via one unbiased random byte per character (`byte & 0x1F`, safe because 256 is an exact multiple of 32). The stored verifier is `HMAC-SHA256(pairing_secret, normalized_code)` — never a bare hash of the code, since a human-enterable ~50-bit secret is offline-brute-forceable from a database copy alone the way a 256-bit device token isn't; `pairing_secret` is a separate server config value that never touches the database.
- `internal/server/store`: `RedeemPairingCode` validates, consumes, and issues a device in one transaction; consuming is an atomic conditional `UPDATE ... WHERE consumed_at IS NULL` (the same claim pattern P3's scheduler uses), proven to have exactly one winner under real concurrent goroutines racing the same code. Unknown/expired/already-consumed/lost-the-race all return the identical `auth.ErrInvalidPairingCode` — no code-guessing oracle. `users.telegram_user_id` (a partial-unique-indexed column, since pre-P4 bootstrap users have none) is the only identity ever used; a Telegram username is never read or stored anywhere in this codebase.
- `internal/server/telegrambot`: a long-polling bot worker (`getUpdates`, not a webhook — no new public HTTPS endpoint needed for this beta), offset durably persisted in SQLite (`telegram_bot_state`) so a restart resumes rather than reprocessing or permanently skipping updates. Only private-chat messages are handled; groups/supergroups/channels are silently ignored. Three commands: `/start` (invalidates any still-active previous code, issues a fresh one, sends it with the exact `ai-limit-notifier link CODE` instruction — never a device token), `/devices` (lists only the requesting user's own devices — id, active/revoked, linked date — never a token), `/revoke <device-id>` (only the caller's own device; another user's device id gets the identical "not found" response a nonexistent one would).
- `internal/server/api`: `POST /api/v1/pair` — unauthenticated by nature, protected by the code's own entropy/TTL/one-use plus a per-IP rate limiter. Strict JSON (unknown/trailing fields rejected, so a device payload structurally cannot supply a Telegram id, chat id, destination, text, command, or URL), bounded body, a strict `platform` identifier pattern rather than a hardcoded enum (so new platforms don't need a server change), sanitized errors. `POST /api/v1/usage` applies no percentage threshold of its own (removed, along with `Server.Threshold` and `ai-limit-server serve --threshold`) — the notification threshold is now purely a local, per-device setting (`ai-limit-notifier config threshold N`); a schema-valid, authenticated submission always creates/updates the durable event, since the device only ever sends one after crossing its own configured threshold.
- Trusted-proxy support (`--trusted-proxy CIDR`, repeatable): `X-Forwarded-For` is ignored entirely unless the immediate TCP peer is in an explicit allowlist, matching a single local reverse proxy (Caddy) in front of the server; still empty (trust nothing) by default. When trusted, the **last** comma-separated entry is used — the one Caddy itself appended — never the first, since a reverse proxy appends its own observed peer address rather than replacing whatever `X-Forwarded-For` a client already sent it; trusting the first entry would let anyone connecting directly to Caddy spoof it. (Caught and fixed during pre-commit review of this exact invariant — the original implementation used the first entry.) Tested: direct-client-with-fake-XFF ignored, trusted-proxy-with-genuine-single-hop-XFF extracted, a spoofed leading entry ignored in favor of the proxy-appended trailing one, untrusted-proxy-XFF ignored, malformed-trailing-entry falls back to the peer (never to an earlier untrusted entry).
- `internal/localconfig` + `ai-limit-notifier link CODE`: the local agent's only persistent write remains static link configuration (`server_url`/`device_id`/`device_token` — never usage/history/provider data), atomically written (temp file + fsync + rename) with a 0700 directory and 0600 file, resolved via XDG convention. `link`'s HTTP client follows the same strict rules as the P3 `HTTPSink` (HTTPS required unless loopback, no redirects followed, bounded timeout/response). `monitor` now resolves its server URL and device token through a documented precedence (CLI flag > env > saved config for the URL; env > saved config for the token, never a CLI flag) — after `link`, plain `ai-limit-notifier monitor` works with no manual token copying, exactly the P4 goal.
- `bootstrap-device` remains an explicitly local/administrative mechanism (it requires direct access to the server's database file) — normal onboarding is `/start` -> `link` from here on.

Private chat is the first required delivery mode. Channel binding can follow after this path is stable (unchanged from the original P4 scope).

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
