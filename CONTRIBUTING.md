# Contributing

AI Limit Notifier is source-available under the PolyForm Shield License 1.0.0. The project owner also intends to preserve the ability to grant separate permissions, company-specific licenses, and custom commercial arrangements in the future.

To avoid creating unclear ownership or alternative-licensing rights while that process is being established, **substantial external code contributions are not accepted for merge yet unless an explicit contributor agreement / CLA approved by the project owner is in place**.

You are welcome to:

- open bug reports;
- describe compatibility problems;
- suggest features;
- provide reproducible test cases and test results;
- review code;
- report documentation mistakes;
- report security issues through the security contact process once published;
- open a draft pull request for discussion, understanding that substantial third-party code should not be merged until contribution rights are settled.

## Why this rule exists

The public software license and contribution rights are different legal questions. A third-party contributor normally retains copyright in their contribution unless they grant additional rights.

The project wants to avoid a future situation where outside contributions make it impossible for the project owner to grant a company an alternative license, an exception to the public license, or company-specific rights for the combined product.

Before normal external code contributions are enabled, the project will publish a CLA or equivalent contributor-rights process that preserves both:

1. the contributor's rights and attribution; and
2. the project owner's ability to distribute the complete project under the public license and, where appropriate, under separately negotiated terms.

## Development expectations

When contributions are accepted in the future, they must preserve the project's core security/runtime invariants:

- zero model calls solely for monitoring;
- zero model-context injection for monitoring;
- no local persistence of monitored usage/history/cache/runtime logs during normal operation;
- no upload of Claude/OpenAI credentials, prompts, model responses, project files, or terminal contents;
- no hosted-server-to-device remote execution channel;
- no silent browser/screen scraping or credential-copying fallback;
- missing provider data must remain unknown, never fake zero.

The repository owner currently develops the product directly on `main`. `main` must remain buildable and tested. See `SECURITY.md`, `docs/PROJECT_STATUS.md`, `docs/RELEASE_CRITERIA.md`, and `docs/DECISIONS.md` for the current implementation priorities and release gates.
