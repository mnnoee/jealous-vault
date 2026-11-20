# MVP and product scope

English · [Русский](ru/mvp.md)

Status: planning document, last updated 2026-11-20. It describes the target, not what `0.1.0`
already does. Current features are in [README.md](../README.md); tested limits are in
[platforms-and-limits.md](platforms-and-limits.md).

## Product frame

JealousVault is an open project, not a gated product.

- The public product is the complete backup tool, licensed under Apache-2.0. It is not a crippled
  freemium edition and it has no feature gates.
- Commercial work is additive: extensions, plugins, integrations, hosted services, support and
  custom development. It does not remove capabilities from the public product.
- Anyone may fork the public product, run it on any number of hosts, or build a compatible
  implementation. The licence allows it; the project does not endorse it.
- The project is maintained by one person, so the public product must stay small enough to finish,
  test and support.

Success for a backup tool is narrow and strict: one scenario must work, and the restore must be
proven. A capability that cannot be restored is not a capability.

## The public product

| Area                | Included                                                                       |
| ------------------- | ------------------------------------------------------------------------------ |
| One host            | backup, check, prune, restore                                                  |
| Schedulers          | systemd, cron, OpenRC, runit, `tick`                                           |
| Storage             | local, SFTP, S3-compatible (including Backblaze B2), REST                      |
| Databases           | PostgreSQL, MySQL/MariaDB                                                      |
| Basic alerts        | email, Telegram, JSON webhook, healthchecks-style ping                         |
| Verification        | `doctor`, `history`, `test-restore`, lock handling, audit journal              |
| Manual escape hatch | `export-restic-env`, recovery kit, documented restic commands, config examples |
| Documentation       | install, configuration, operations, restore, limits, troubleshooting           |

The public product is never reduced to make a commercial extension look better. Restore, integrity
checks, security fixes, basic alerts and documentation stay in it.

## Commercial add-ons

These are separate products or services. They may live in separate repositories with separate
licences, and their authors retain ownership of their work.

| Area                 | Examples                                                 |
| -------------------- | -------------------------------------------------------- |
| Central management   | web UI and status for a fleet of hosts                   |
| Chat integrations    | Slack and Discord connectors                             |
| Hosted services      | alert relay, managed repository hosting                  |
| Compliance           | PDF/CSV reports and evidence export                      |
| Fleet drills         | restore drills across many hosts with one evidence store |
| Enterprise access    | SSO, RBAC, audit export, ticketing integrations          |
| Support              | priority support, consulting, custom development         |
| Commercial licensing | paid licences for project-owned extensions               |

A commercial add-on may use the documented CLI, config and JSON output. It must not be required for
the public product to work.

## Ecosystem rules

The public policy is in [ECOSYSTEM.md](../ECOSYSTEM.md). In short:

- a third-party plugin may use any licence, including GPL-3.0;
- plugin authors retain ownership of their work;
- commercial plugins are welcome, but must not claim official status without permission;
- contributions to the public repository need a DCO sign-off and are accepted under Apache-2.0,
  with no additional proprietary grant ([CONTRIBUTING.md](../CONTRIBUTING.md));
- code accepted into a commercial extension is covered by a separate written agreement
  ([CLA.md](../CLA.md)), only when both parties explicitly agree.

## MVP definition

For a backup tool, MVP means: **one happy path works end to end, and the restore is proven.**

Golden path:

```text
S3 (primary) or local (tests)
  -> PostgreSQL dump
  -> systemd timer
  -> Telegram alert
  -> restore into a clean host
  -> count/sum diff matches
```

The path must be polished. Five backends working a little are worse than one backend working
predictably.

### Acceptance criteria

1. One golden path is documented and tested: S3 or local, PostgreSQL, systemd, Telegram, restore,
   diff.
2. A restore integration test runs in CI with a real restic binary, not a fake.
3. Installation is one command on two distributions: `deb` plus `AUR` or `rpm`.
4. The config schema is versioned and `config migrate` upgrades an old file.
5. `export-restic-env` (or an equivalent one-liner) gives a manual escape hatch without the agent.
6. A stranger can go install -> configure -> backup -> restore -> troubleshooting without asking the
   maintainer.
7. Limits are explicit: what is tested, what is not, and on which versions. The canonical document
   is [platforms-and-limits.md](platforms-and-limits.md), linked from the top of the README.

### Current state against the criteria

| Criterion               | In 0.1.0                                                             | Gap                                              |
| ----------------------- | -------------------------------------------------------------------- | ------------------------------------------------ |
| Golden path             | Works manually on real hosts, S3 and PostgreSQL                      | Not packaged; not a single verified install path |
| Restore test in CI      | Unit tests use a fake restic; restores were verified manually in LXC | No real-restic restore job in CI                 |
| One-command install     | Binary asset, `jealous-vault install`, and a `.deb` package          | No `rpm`/`AUR` package                           |
| Versioned config schema | Strict TOML, unknown keys rejected                                   | No schema version, no `config migrate`           |
| Manual escape hatch     | `export-restic-env` prints variables, commands and dump handling     | Covered                                          |
| Stranger can finish     | Documentation covers the path                                        | Not yet tested with an outsider                  |
| Explicit limits         | [platforms-and-limits.md](platforms-and-limits.md)                   | Must stay linked from the README and updated     |

## Not in MVP

- a second storage backend beyond the golden path;
- a second scheduler beyond systemd for the golden path;
- a second alert channel beyond Telegram for the golden path;
- a central web UI, fleet management or a server;
- Slack/Discord integrations and other commercial extensions;
- SSO, RBAC, compliance reports;
- Windows support;
- a Proxmox cluster and very large `vzdump` archives;
- managed hosting.

These are not rejected forever. They are postponed until the golden path and its restore proof are
boring. Extensions live outside this repository and do not block the public product.

## Rule for new features

Ask one question: **does this strengthen the golden path or the restore proof?**

- If yes, it can enter the MVP.
- If no, it waits until the MVP criteria are met.
- If it is an extension or a commercial feature, it does not enter this repository at all; it goes
  to a separate repository with its own licence.

Every new integration is code that one person has to test, document and fix. The default answer is
no.

## Evidence

An MVP claim needs evidence, not intention:

- a CI run or a reproducible test;
- the host, distribution and restic version;
- the exact restore result, for example `count/sum` before and after;
- a note in the changelog when the behaviour changes.

"Should work" is not evidence.
