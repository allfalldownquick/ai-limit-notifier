# Hosted Protocol v1

This document fixes the narrow client/server boundary for the first hosted beta. It is intentionally data-only and does not provide a server-to-device command channel.

Implementation may refine field names, but must preserve these security properties.

## Actors

- **Telegram user** — starts the project bot and owns the hosted account/delivery destination.
- **Local agent** — runs on the user's supported machine and reads normalized provider usage locally.
- **Hosted API** — authenticates devices, persists state, schedules notifications.
- **Telegram bot worker** — handles bot interaction and sends notifications.

## Pairing

### 1. User requests a pairing code

The user opens the project Telegram bot and chooses to connect a device.

The server creates a random, short-lived, single-use pairing code associated with that Telegram user.

Requirements:

- short expiry (target: 10 minutes);
- single use;
- guessing/rate-limit protection;
- code must not itself become the long-lived device credential;
- server should store only what it needs to validate the temporary code safely.

### 2. Agent exchanges the code

Conceptual request:

```http
POST /api/v1/pair
Content-Type: application/json
```

```json
{
  "code": "K7F4-X2QM-JH",
  "client_version": "0.1.0-beta.1",
  "platform": "linux-wsl-amd64"
}
```

Implementation note (P4): the code is 10 characters of Crockford Base32 (`0-9`, `A-Z` minus `I`/`L`/`O`/`U`, so no character is ambiguous with a digit or another letter), formatted `XXXX-XXXX-XX` — exactly 50 bits of entropy. The server never stores the plaintext code, only `HMAC-SHA256(pairing_secret, normalized_code)`, where `pairing_secret` is a separate server-side secret (`AI_LIMIT_NOTIFIER_PAIRING_SECRET`) that never touches the database — a bare hash would be offline-brute-forceable given only a database copy, since a human-enterable code has far less entropy than a device token. Consuming a code and issuing its device are one atomic transaction (a conditional `UPDATE ... WHERE consumed_at IS NULL`, the same claim pattern the scheduler uses for event delivery); two concurrent redemptions of the same code always resolve to exactly one winner. Unknown, expired, already-consumed, and "lost the race" all return the identical response, so the endpoint is not a code-guessing oracle.

The server verifies the code and returns a newly generated device identity/credential exactly for that linked device.

Conceptual response:

```json
{
  "linked": true,
  "device_id": "dev_...",
  "device_token": "..."
}
```

The long-lived token is shown/returned once and then stored locally as static link configuration. It is not a Claude/OpenAI credential.

Server requirements:

- generate a high-entropy device token;
- persist only an appropriately protected/hash-derived representation needed for authentication;
- bind the device to exactly one hosted user;
- mark the pairing code consumed atomically;
- never return Telegram bot tokens or provider credentials.

## Device authentication

For v0.1, HTTPS plus a high-entropy per-device bearer credential is sufficient if the server also applies strict schema validation, rate limiting, revocation, and safe logging.

Conceptual header:

```http
Authorization: Bearer <device-token>
```

The token must never appear in application logs.

If a device token is stolen, its protocol capability is intentionally narrow: it may submit forged usage metadata for that linked device until revoked. It must not grant access to shell execution, other users, Telegram bot ownership, provider credentials, or arbitrary Telegram messaging.

## Usage submission

Conceptual request:

```http
POST /api/v1/usage
Authorization: Bearer <device-token>
Content-Type: application/json
```

```json
{
  "schema_version": 1,
  "provider": "codex",
  "observed_at": "2026-09-02T18:15:00Z",
  "five_hour": {
    "used_percent": 83,
    "reset_at": "2026-09-02T20:00:00Z"
  },
  "weekly": {
    "used_percent": 57,
    "reset_at": "2026-09-08T08:12:00Z"
  }
}
```

Both windows are optional individually, but at least one window must be present. Missing means unknown, never zero.

Allowed data classes:

- provider enum;
- normalized usage percentages;
- provider reset timestamps;
- observation timestamp;
- protocol/client metadata required for compatibility and diagnostics.

Explicitly forbidden device-supplied fields/data classes:

- arbitrary Telegram message text;
- arbitrary Telegram destination/chat ID;
- shell/exec/command strings;
- local filesystem paths;
- arbitrary URLs for the server or client to fetch/execute;
- prompts/model responses;
- terminal contents;
- project source code;
- Claude/OpenAI account credentials.

## Validation

The hosted API must reject rather than sanitize unexpected control data.

Minimum checks:

- authenticated, non-revoked device;
- bounded request size;
- known schema version;
- known provider enum;
- `0 <= used_percent <= 100` and finite numeric values;
- UTC/RFC3339-compatible timestamps;
- plausible reset horizon and stale-window rejection policy;
- at least one valid usage window;
- unknown JSON fields rejected for the strict device endpoint;
- per-device rate limit.

## Persistence and acknowledgement

A successful usage response means the server has durably persisted the state/event necessary to survive a process restart.

Conceptual response:

```json
{
  "accepted": true,
  "persisted": true
}
```

The local agent keeps retry state only in RAM. If no positive durable acknowledgement is received, it may retry with exponential backoff while alive.

After a local restart, the agent may submit the current provider snapshot again. The server must deduplicate scheduling using stable event semantics such as linked user/device + provider + window kind + reset timestamp.

Implementation note (P3): the idempotency/combine key is scoped to the **user**, not the device — `(user_id, provider, window_kind, reset_at)`. Two devices belonging to the same user reporting the same provider's same window produce one notification, not two, since the user only wants one Telegram message per real reset window regardless of which machine reported it.

## Scheduling

Default reset-notification rule:

- create a durable reset notification when normalized usage reaches the configured threshold (default `80% used`);
- schedule for `reset_at + 1 minute`;
- repeat snapshots for the same reset window must not create duplicate scheduled notifications;
- if a provider changes the reset timestamp, update the authoritative pending event carefully rather than blindly creating another notification.

Equivalent Claude/Codex windows whose resets are within the configured combine interval (default 10 minutes) may be covered by one combined notification. The later event must record that it was covered so a server restart does not send it again.

## Telegram delivery semantics

For v0.1, prefer **at-least-once** delivery semantics with a very rare duplicate being acceptable over silently losing a notification.

Reason: Telegram `sendMessage` and local persistence cannot form one atomic transaction. A process can theoretically crash after Telegram accepted a message but before the server persisted `sent=true`.

The implementation must:

- rate-limit outbound Telegram sends;
- honor Telegram retry/backoff signals;
- persist pending/sending/sent/covered state;
- document the rare-duplicate tradeoff;
- never allow a device payload to choose arbitrary message content/destinations.

## Device revocation

The Telegram account owner must be able to disconnect/revoke a linked device server-side.

After revocation:

- the device token no longer authenticates;
- future submissions are rejected;
- no server-to-device cleanup command is sent;
- local uninstall/relink is a separate user-initiated action.

Implementation note (P4): revocation is a bot command, `/revoke <device-id>`, alongside `/devices` (lists only the requesting Telegram user's own devices — id, active/revoked, linked date; never a token) and `/start` (issues a pairing code). A user can only revoke a device resolved to belong to their own `telegram_user_id`; attempting to revoke another user's device id returns the same response as a nonexistent one, so `/revoke` can't be used to probe for other users' device ids.

## Status/health

A narrow health/connectivity endpoint may exist for `doctor`, but it must return only service/protocol status. It must not become a command/update channel.

Example classes:

- API reachable;
- credential valid/revoked;
- supported protocol version;
- server timestamp.

It must not return shell commands, executable blobs, or arbitrary URLs to execute.

## Hosted vs self-hosted

The same protocol should work against:

- the project-operated hosted endpoint; or
- a user-controlled self-hosted server.

Changing server endpoint must not grant the server broader local privileges.
