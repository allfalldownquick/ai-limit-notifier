# Security Model

AI Limit Notifier is designed so that compromise of the hosted notification service does not imply remote control of user machines.

## Trust boundaries

### Local device

The local agent may read provider-supplied usage metadata needed to obtain rate-limit percentages and reset timestamps. Provider authentication material must remain on the local device and must never be included in requests to the AI Limit Notifier server.

### Hosted server

The hosted server may store:

- internal user/device identifiers;
- Telegram delivery identifiers;
- normalized usage percentages;
- reset timestamps;
- notification state;
- subscription/billing state;
- hashes or otherwise appropriately protected device authentication credentials.

It must not store Claude/OpenAI account credentials, prompts, model responses, project source code or terminal contents.

## No remote execution

The device protocol is outbound-only from agent to server. There is intentionally no server-to-agent command channel.

The server API must not contain generic fields or endpoints such as:

- `command`;
- `shell`;
- `exec`;
- arbitrary `url` fetches;
- arbitrary local file/path reads;
- arbitrary Telegram `message` bodies supplied by devices.

A stolen device credential should at worst allow an attacker to submit forged usage metadata for that device until the credential is revoked. It must not grant shell access, Telegram bot ownership, access to provider credentials or access to other users.

## Device API requirements

- HTTPS only in production.
- One revocable credential per linked device.
- Strict request schema and small request-body limit.
- Provider enum allowlist.
- Usage percentage range checks.
- Reset timestamp sanity checks.
- Rate limiting.
- Server-side idempotency/deduplication.
- Replay-resistant request design before public hosted launch.
- Secrets must never be written to application logs.

## Local runtime write policy

The agent must not write usage state, history, caches or runtime logs to local storage.

Allowed persistent changes are install-time configuration only, for example:

- the application binary;
- systemd/Windows service registration;
- Claude Code status-line configuration when required;
- a static device credential/configuration needed to reconnect after reboot.

These files must not be periodically rewritten merely to track usage. Transient usage state belongs in RAM.

Note that an operating system can independently page process memory or maintain system-level journals. The project's guarantee is that the agent itself performs no local runtime persistence of monitored usage.

## Telegram

Telegram bot credentials belong only on the server/self-host instance. Local monitoring agents never receive the Telegram bot token.

Devices also never choose arbitrary Telegram destinations. A linked user's active destination is resolved by the server.

## Updates

The hosted API must not be able to push executable commands or binaries to clients.

Public releases should eventually provide checksums and, before automatic updates are enabled, a verifiable release-signing mechanism. An update source must be independently authenticated rather than trusted merely because the hosted API returned a download URL.

## Reporting vulnerabilities

Until a private security contact is published, please avoid opening public issues containing credentials, tokens, exploit payloads against the hosted service, or other sensitive details.
