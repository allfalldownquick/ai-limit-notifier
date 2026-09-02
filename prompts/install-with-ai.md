# Install AI Limit Notifier with Claude Code or Codex

Copy the prompt below into a **new Claude Code or Codex session**.

The coding agent is allowed to adapt installation steps to the current machine, but it must inspect first, explain the plan, and ask before making persistent or privileged changes.

---

```text
I want to install AI Limit Notifier from the official repository:

https://github.com/allfalldownquick/ai-limit-notifier

Act as a cautious installation assistant for this specific computer.

IMPORTANT: Start in READ-ONLY inspection mode. Do not install, edit, delete, move, create persistent files, enable services, change shell profiles, change Claude/Codex configuration, install packages, or request administrator/root privileges until you have completed the inspection and I explicitly approve your installation plan.

Phase 1 — inspect this machine

1. Detect the operating environment:
   - Windows version, if applicable;
   - Linux distribution, if applicable;
   - whether WSL is installed and which distributions are available;
   - whether this session itself is running in Windows, WSL, Linux, or macOS.

2. Locate Claude Code and Codex installations that are actually usable by this user. Determine whether each one is:
   - native Windows;
   - inside WSL;
   - native Linux;
   - macOS;
   - desktop-app-only / otherwise unsupported.

3. Inspect the current AI Limit Notifier repository/release documentation relevant to this environment, including at minimum:
   - README.md;
   - SECURITY.md;
   - docs/ARCHITECTURE.md;
   - docs/AI_ASSISTED_INSTALL.md;
   - docs/INSTALLER_CONTRACT.md;
   - the installer or release artifacts you intend to use.

4. Review the relevant client and networking code before installation. Confirm whether the current version still satisfies these properties:
   - monitoring does not create Claude/Codex model requests;
   - prompts, model responses, project files, repository contents and terminal contents are not uploaded;
   - Claude/OpenAI provider credentials are not uploaded to the AI Limit Notifier server;
   - the hosted protocol has no remote shell / arbitrary command execution path;
   - runtime usage/history/cache/log state is not persisted locally by the agent;
   - the outgoing device payload is limited to the documented usage/reset metadata and required device authentication metadata.

5. Determine whether this computer is currently supported. If it is not supported, stop and explain exactly why. Do not invent browser scraping, credential extraction, hidden model calls, screen scraping, or other unsafe fallbacks.

Phase 2 — produce an installation plan

Before changing anything, show me a concise plan containing:

- which Claude Code/Codex installation(s) you found;
- which AI Limit Notifier adapter(s) would be used;
- where the AI Limit Notifier binary/files would be installed;
- any directory that needs to be created;
- any missing prerequisite/package/library/tool that needs to be installed;
- any service/autostart entry that would be created;
- any Claude Code or Codex configuration that would be changed;
- whether administrator/root/sudo/UAC permission will be required;
- every persistent file/configuration location the installer is expected to create or modify;
- how the installation can be completely removed;
- what data will be sent to the server after installation.

If there is an environment-specific problem, propose the smallest safe fix. Examples include:

- creating the project's dedicated installation/configuration directory;
- installing a documented prerequisite that is genuinely missing;
- fixing PATH discovery for the installed AI Limit Notifier binary;
- selecting the correct WSL distribution;
- correcting file ownership/permissions for the project's own files;
- creating the documented systemd/Windows service entry;
- resolving a port/path/service conflict in a way that does not weaken system security;
- choosing a supported installation location when the default is unavailable.

Do not modify unrelated projects, repositories, shell configuration, security controls, antivirus/firewall settings, provider credentials, or system-wide settings unless the official installation genuinely requires it and you explain why first.

Then STOP and ask me whether to proceed.

Phase 3 — installation, only after my explicit approval

After I approve the plan:

1. Prefer the official AI Limit Notifier installer/release mechanism for this version.
2. Verify release checksums/signatures when the project provides them.
3. Apply only the approved persistent changes.
4. You may adapt paths and install missing documented prerequisites for this machine when necessary.
5. If a new unexpected problem appears that requires an additional persistent, privileged, security-sensitive, or unrelated system change, STOP and ask me before doing it.
6. Never copy or upload Claude/OpenAI credentials to the AI Limit Notifier server.
7. Never add a remote-execution mechanism or workaround unsupported environments by scraping screens/browser sessions.
8. Let me personally approve UAC/sudo/root prompts when required.

Phase 4 — verify the installation

After installation:

- run the project's built-in environment/detection check when available;
- run `ai-limit-notifier doctor` when available;
- run `ai-limit-notifier show-payload` when available;
- verify Claude Code and Codex still operate normally;
- verify the installed service/agent is using the intended environment;
- verify runtime monitoring does not create local usage/history/cache/log files;
- show me the exact categories of data the agent can send;
- clearly list every file/configuration/service that was created or modified;
- tell me how to uninstall/revert those changes.

If Telegram/device pairing is not yet complete, tell me exactly what to do next in the AI Limit Notifier Telegram bot and ask only for the short pairing code when required.

Do not claim the installation is secure merely because you performed the review. Report what you actually verified, anything you could not verify, and any remaining risks or unsupported behavior.
```

---

The AI session is only used to inspect, adapt, install, and verify the software. AI Limit Notifier monitoring itself must continue to operate without model calls or prompt/context overhead.
