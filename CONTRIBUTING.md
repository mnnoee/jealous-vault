# Contributing

English · [Русский](CONTRIBUTING.ru.md)

Thanks for taking the time to help. JealousVault is a solo, best-effort project; small, focused contributions are easier to review and ship than large rewrites.

## Before you start

- Open an issue before a large change, a new integration or a refactor. It saves work if the change is out of scope.
- Report security issues privately as described in [SECURITY.md](SECURITY.md), never in a public issue.
- Check [docs/mvp.md](docs/mvp.md): during the MVP phase, fixes and tests for the supported path come before new backends, schedulers and alert channels.
- Read [STABILITY.md](STABILITY.md) before changing the CLI, config, state format or unit names.

## Developer Certificate of Origin

Every commit must be signed off:

```sh
git commit -s -m "docs: fix the restore example"
```

The sign-off adds:

```text
Signed-off-by: Your Name <you@example.com>
```

By signing off, you confirm that you wrote the contribution or have the right to submit it, and that it may be distributed under the project licence.

The DCO confirms provenance. It is **not** a CLA and does not transfer any additional rights.

## Licence of contributions

Pull requests to this repository are accepted under the **Apache License 2.0**, inbound = outbound:

- you retain the copyright to your contribution;
- your contribution is distributed under Apache-2.0, like the rest of the project;
- you do **not** grant mnnoee any additional proprietary or relicensing rights for an ordinary pull request;
- the project does not need to own your code to use it, and Apache-2.0 already permits commercial use, forks and compatible products.

If mnnoee later invites you to include your code in a **commercial extension**, that is a separate written agreement, described in [CLA.md](CLA.md). It applies only to the code you explicitly submit for that commercial extension. Ordinary pull requests to this repository are never covered by it, and a later agreement does not revoke the licence you already granted for earlier contributions.

## Plugins and extensions

- You can write a plugin or extension under any licence you choose, including GPL-3.0.
- Commercial plugins are welcome, and you retain ownership of your work.
- A plugin is a separate work. It must not claim to be official or endorsed by JealousVault without permission, and it must not misrepresent its origin.
- If you want the project to distribute or sell your plugin, discuss a separate agreement first.
- The public product is not reduced to make room for paid extensions. See [ECOSYSTEM.md](ECOSYSTEM.md).

## What to change

Welcome:

- bug fixes with a reproduction or a test;
- documentation fixes, especially install, restore and troubleshooting;
- tests for edge cases: locks, state corruption, S3/SFTP failures, retention, restore;
- platform notes with evidence: distribution, version, init system, result;
- small usability fixes that follow the existing style.

Discuss first:

- a new storage backend, scheduler or alert channel;
- a new command or a breaking config change;
- a large refactor or a new dependency.

Out of scope for this repository:

- a server, daemon or web UI in this repository;
- fleet management, SSO, RBAC, compliance reports;
- Windows support;
- removing a capability from the public product, or gating it behind a commercial extension.

Paid extensions and commercial support live in separate repositories or agreements. They do not belong in this repository.

## Checks

Run before opening a pull request:

```sh
gofmt -l .
go vet ./...
go test ./...
```

The CI also runs `govulncheck` and a dependency licence check. New dependencies must use a licence from the allowed list: Apache-2.0, MIT, BSD-2-Clause, BSD-3-Clause, ISC or Unlicense.

## Documentation

- User-facing documentation is bilingual: English in `docs/`, Russian in `docs/ru/`.
- Keep the same structure in both languages and update both in the same change.
- Command examples must work with the current release tag and must not contain real secrets.
- Do not add screenshots of real hosts, repositories or credentials.

## Commits and pull requests

- One logical change per commit; keep the diff reviewable.
- Use an imperative subject with a prefix used in the history, for example `docs:`, `restic:`, `systemd:`, `ci:`.
- Do not commit generated binaries, `.temp/`, passwords, keys, tokens or real backup contents.
- Describe what changed, why, and how you tested it.
- Note user-visible changes for `CHANGELOG.md`; the maintainer adds the entry when merging.

## Review

- First review target: 14 business days, best effort.
- The maintainer may ask for a smaller scope, a test or a documentation update.
- A pull request may be closed if it conflicts with the MVP scope or the extension policy.
- Response targets and the bus factor are in [MAINTAINERS.md](MAINTAINERS.md).
