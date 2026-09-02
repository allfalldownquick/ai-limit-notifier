# AI-Assisted Installation

AI Limit Notifier supports an installation workflow where Claude Code or Codex acts as a machine-specific installation assistant.

The purpose is not to make the monitoring runtime depend on an AI model. The AI session is used only once to inspect the current machine, adapt documented installation steps, explain changes, obtain user approval, perform the installation, and verify the result.

## Why this exists

Developer machines differ significantly:

- Claude Code may run natively, in WSL, or on Linux/macOS;
- Codex may run in a different environment from Claude Code;
- multiple WSL distributions may exist;
- PATHs, service managers, file permissions, install locations and prerequisites differ;
- an otherwise supported setup may need a small environment-specific fix.

A coding agent already running on the machine can inspect those differences more accurately than a long static troubleshooting guide.

## Trust model

AI-assisted installation is optional. Users can always use a normal installer or build from source.

The coding agent must use a two-stage model:

1. **Read-only inspection and review.**
2. **Installation only after explicit user approval.**

The agent is allowed to adapt documented installation steps to the machine, but it is not allowed to silently broaden the security model.

## What the coding agent may adapt

After approval, the installation assistant may perform machine-specific work such as:

- choose the supported Windows/WSL/Linux/macOS target that actually contains Claude Code or Codex;
- create AI Limit Notifier's dedicated installation/configuration directories;
- choose another supported installation path when the default is unavailable;
- install documented prerequisites that are missing;
- resolve the AI Limit Notifier binary's PATH/discovery problem;
- choose the correct WSL distribution;
- create the documented systemd/Windows service/autostart entry;
- apply documented Claude Code integration configuration;
- fix ownership/permissions for AI Limit Notifier's own files;
- resolve ordinary path, package or service conflicts without weakening security;
- rerun diagnostics and make a narrow fix if the first installation attempt exposes a machine-specific issue.

## Changes that require another approval

Even after the initial installation approval, the coding agent must stop again if an unexpected issue would require:

- administrator/root/sudo/UAC privileges not already disclosed in the approved plan;
- disabling or weakening antivirus, firewall, OS security, sandboxing or access controls;
- modifying unrelated repositories/projects;
- changing provider authentication/credential files beyond the project's documented read-only integration needs;
- changing shell profiles or global environment configuration when a project-local alternative exists;
- installing an undocumented third-party binary/service;
- opening inbound network ports;
- adding any server-to-device command/control mechanism;
- changing unrelated system services;
- destructive cleanup or deletion outside AI Limit Notifier's own files.

## Unsupported environment behavior

The assistant must fail closed.

If the installed Claude/Codex environment has no supported read-only usage source, it must say that the configuration is unsupported rather than inventing a fallback.

Forbidden fallback examples:

- screen/pixel scraping as a hidden primary integration;
- browser session/cookie extraction;
- uploading provider credentials;
- triggering model requests merely to discover usage;
- installing a remote shell;
- patching Claude/Codex binaries without an explicit supported integration design.

## Required pre-install report

Before any persistent change, the coding agent should report:

```text
Detected environment
- OS: ...
- Claude Code: ...
- Codex: ...

Compatibility
- Claude adapter: supported / unsupported
- Codex adapter: supported / unsupported

Proposed persistent changes
- install directory: ...
- config/credential location: ...
- service/autostart: ...
- Claude/Codex config changes: ...
- prerequisites: ...

Privileges
- administrator/root required: yes/no

Data sent after installation
- provider
- usage percentages
- reset timestamps
- device authentication metadata

Not sent
- prompts
- model responses
- project/source files
- terminal contents
- Claude/OpenAI credentials

Proceed with installation? [yes/no]
```

## Required post-install verification

When available, the installed CLI should expose stable commands the coding agent can use rather than reverse-engineering runtime state:

```text
ai-limit-notifier detect
ai-limit-notifier doctor
ai-limit-notifier show-payload
ai-limit-notifier status
ai-limit-notifier uninstall --plan
```

Expected roles:

- `detect`: show supported local provider installations and selected adapters;
- `doctor`: verify service/config/connectivity without exposing secrets;
- `show-payload`: show a redacted/representative or current normalized payload before transmission;
- `status`: show whether monitoring is active and linked;
- `uninstall --plan`: show exactly what removal would change before removal.

The coding agent should also compare the actual installation against the plan and list all created/modified persistent locations.

## Runtime independence

After installation, the Claude Code/Codex session can be closed.

Normal monitoring must not require:

- Claude Code or Codex to answer a prompt;
- a persistent chat/session;
- MCP context injection;
- project instructions;
- model tokens.

The monitoring runtime remains a separate local process using supported read-only provider metadata interfaces.

## Recommended user-facing flow

```text
README / website
      |
      v
Copy "Install with Claude Code / Codex" prompt
      |
      v
Open NEW coding-agent session
      |
      v
Read-only machine + repository review
      |
      v
Compatibility + installation plan
      |
      v
User approves
      |
      v
Machine-specific installation / narrow fixes
      |
      v
Doctor + show-payload + verification
      |
      v
Telegram pairing
      |
      v
Close AI session; notifier runs independently
```
