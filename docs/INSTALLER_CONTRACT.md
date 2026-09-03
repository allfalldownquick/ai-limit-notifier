# Installer Contract

This document defines the stable interface that normal installers and AI-assisted installation should share.

The coding agent may adapt *how* the documented installer is reached on a particular machine, but it should not invent a different product architecture.

## Design goals

- one predictable installation result regardless of whether setup was started manually, by an installer UI, or by Claude Code/Codex;
- detect supported environments automatically;
- make all persistent changes inspectable before installation;
- make removal equally inspectable;
- keep provider credentials local;
- avoid model calls during monitoring;
- avoid local runtime persistence of usage/history/cache/log state;
- never create a remote-execution channel.

## Stable CLI surface

The following commands are the target public contract. Some commands may be introduced incrementally during development, but once released they should remain automation-friendly.

### `ai-limit-notifier detect`

Read-only environment inspection.

Must report, without modifying the machine:

- host OS/platform;
- whether execution is native Windows, WSL, Linux, or macOS;
- detected WSL distributions when relevant;
- detected Claude Code installations;
- detected Codex installations;
- supported adapter candidates;
- unsupported/ambiguous cases;
- prerequisites that are missing.

Exit behavior should distinguish:

- supported and ready;
- supported but prerequisite/fix required;
- unsupported;
- detection error.

### `ai-limit-notifier install --plan`

Read-only installation plan.

Must print the intended persistent changes, including:

- binary location;
- project configuration/device credential location;
- service/autostart registration;
- Claude/Codex integration changes;
- directories to create;
- missing prerequisites;
- privilege requirements.

It must not make those changes.

### `ai-limit-notifier install`

Apply the documented plan after user approval.

Requirements:

- idempotent where practical;
- refuse unsupported configurations rather than applying unsafe fallbacks;
- do not overwrite unrelated configuration blindly;
- preserve and minimally patch supported Claude/Codex configuration;
- print every persistent change it applies;
- do not silently enable inbound listeners or remote command execution;
- fail with actionable diagnostics.

### `ai-limit-notifier link <PAIRING_CODE>`

Link this installation to the hosted/self-hosted account without requiring Telegram bot credentials on the client.

The pairing code is short-lived. The resulting device credential may be stored as static install-time configuration so the device remains linked after reboot, but monitoring state must not be periodically persisted locally.

**Implemented (P4).** `--server-url` is optional (falls back to `AI_LIMIT_NOTIFIER_SERVER_URL`, then nothing). The saved config (`server_url`, `device_id`, `device_token`, `schema_version`) lives at `$XDG_CONFIG_HOME/ai-limit-notifier/config.json` (falling back to `~/.config/ai-limit-notifier/config.json`), written atomically with a 0700 directory and 0600 file. `monitor` reads it automatically afterward — a plain `ai-limit-notifier monitor` with no flags works once `link` has run, per the documented CLI-flag > environment > saved-config precedence (the device token itself is never a CLI flag, only environment or the saved config, so it never appears in `ps`/shell history). Tested for: correct permissions, atomic replacement, no leftover credential file on a failed link, an existing config left untouched by a failed relink, and no redirect ever followed.

### `ai-limit-notifier doctor`

Read-only post-install diagnostics.

Must verify as applicable:

- selected provider adapters;
- provider usage source availability;
- service status;
- server connectivity;
- authentication state of the notifier device credential without printing the credential;
- runtime write policy configuration;
- whether the current setup can obtain usage without a model request.

It must redact secrets.

### `ai-limit-notifier show-payload`

Show the normalized data that would be sent to the server, without sending it.

The output must make data minimization auditable. It must not include provider access tokens, refresh tokens, prompts, responses, source files, paths to user projects, terminal contents, cookies, or Telegram bot tokens.

### `ai-limit-notifier status`

Show concise current state for humans/AI installation assistants, for example:

```text
Agent: running
Linked: yes
Claude Code: supported / active
Codex: supported / active
Local runtime persistence: disabled
Server: reachable
```

### `ai-limit-notifier uninstall --plan`

Read-only removal plan listing exactly what will be removed or reverted.

### `ai-limit-notifier uninstall`

Remove only AI Limit Notifier-owned installation state and revert documented integration edits.

It must not remove Claude Code, Codex, WSL, Git, user projects, provider credentials, or unrelated services/configuration.

## AI-assisted troubleshooting contract

When the normal installer reports a correctable environment issue, a coding agent may make a narrow machine-specific fix after disclosing it.

Examples of acceptable fixes:

- install a documented prerequisite/package;
- create the dedicated application directory;
- select the correct WSL distro;
- use a documented alternate install path;
- fix permissions on AI Limit Notifier-owned files;
- make the installed binary discoverable without broad unrelated shell changes;
- repair/recreate the project's documented service entry;
- retry installation after a transient package/service/path failure.

The installer should produce machine-readable or clear human-readable diagnostics so an AI assistant does not need to guess.

## Do not turn the coding agent into the runtime

The installation session may inspect and execute installation commands, but the final monitoring architecture must remain:

```text
Claude/Codex metadata source
        |
        v
AI Limit Notifier local agent
        |
        | strict outbound HTTPS data protocol
        v
Hosted/self-hosted server
        |
        v
Telegram
```

The coding agent is not a daemon, scheduler, monitor, data relay, or remote-control endpoint.
