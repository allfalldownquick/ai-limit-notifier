# Continue AI Limit Notifier development with Claude Code or Codex

Copy the prompt below into a **new coding-agent session opened in the repository**.

```text
Continue development of this repository as the implementation agent for AI Limit Notifier.

Repository:
https://github.com/allfalldownquick/ai-limit-notifier

Work with the CURRENT `main` branch. Do not create or switch to a development branch unless I explicitly ask you to. The goal is to turn `main` into a real, tested product, not to create more planning documents.

First, DO NOT CHANGE ANYTHING.

Read and inspect:
- README.md
- SECURITY.md
- docs/ARCHITECTURE.md
- docs/DECISIONS.md
- docs/PROJECT_STATUS.md
- docs/RELEASE_CRITERIA.md
- docs/INSTALLER_CONTRACT.md
- docs/AI_ASSISTED_INSTALL.md
- the current Go source and tests

Then inspect the repository state and run the existing tests/checks that are possible in this environment.

Before coding, give me a short report containing:
1. what is actually implemented now;
2. what is documentation/specification only;
3. the first incomplete priority from docs/PROJECT_STATUS.md;
4. any contradiction you found between code and documented security/product decisions;
5. exactly what you propose to implement next and how you will verify it.

Do not reopen settled product decisions unless real implementation evidence proves one is impossible or unsafe.

NON-NEGOTIABLE RUNTIME/SECURITY RULES:
- Monitoring must not create an LLM/model request merely to learn usage/reset limits.
- Monitoring must add zero prompts/instructions to Claude Code or Codex model context.
- The local application must not persist monitored usage/history/cache/runtime state/log files during normal monitoring.
- Static install-time binary/config/service/device credential files are allowed, but they must not be periodically rewritten as usage state.
- Claude/OpenAI credentials, prompts, model responses, terminal contents and project files must never be uploaded to the AI Limit Notifier server.
- The hosted server must not have a server-to-device remote shell/exec/update-command channel.
- The device API must never accept arbitrary Telegram text, arbitrary destinations, shell commands, local paths or arbitrary URLs.
- Missing provider data means unknown, never zero.
- Do not use screen scraping/browser scraping/credential copying as a silent fallback.
- If a provider/environment has no safe verified reader, report it unsupported rather than weakening these constraints.

PRODUCT SCOPE FOR NOW:
- Ignore advertising.
- Do not spend time on monetization UI.
- Hosted beta is free.
- Telegram Stars billing can remain a later configurable boundary, but it must not block the first working beta.
- Focus on Claude Code + Codex, WSL/Linux first, private Telegram chat first.

DEVELOPMENT ORDER:
Follow docs/PROJECT_STATUS.md. The highest priority is proving the real Claude Code and Codex usage readers on the actual machine without model calls and without local runtime persistence. Do not build a large server/UI on top of guessed provider behavior.

WHEN IMPLEMENTING:
- verify current provider behavior/source instead of assuming undocumented fields;
- keep changes small enough to test;
- add/update tests with behavior changes;
- run gofmt, go test ./..., go vet ./... when applicable;
- inspect diffs for credentials/secrets and unintended writes;
- keep `main` buildable;
- update docs/PROJECT_STATUS.md only when implementation evidence changes project status;
- do not mark a feature complete merely because its interface or documentation exists.

REAL-MACHINE VALIDATION:
This repository intentionally requires real-environment proof for provider adapters. When you need information from my installed Claude Code/Codex/WSL that cannot safely be inferred, run read-only diagnostics if possible. If a privileged or persistent change is required, explain it and ask first. Never fake a successful validation.

CODE REVIEW:
Before calling a milestone complete, review your own diff specifically for:
- remote-execution paths;
- command injection;
- unsafe shell construction;
- secrets in logs/errors;
- credential copying;
- arbitrary URL/request behavior;
- path traversal/unsafe permissions;
- local runtime persistence;
- duplicate notification/race/restart behavior;
- partial provider data incorrectly interpreted as zero.

Do not stop at writing a plan after the initial report. Once the next step is verified and I approve any necessary privileged/persistent machine changes, continue implementing and testing it toward the release criteria.
```

The intent of this prompt is to make a fresh Claude Code/Codex session pick up the repository from evidence, not from old chat context.
