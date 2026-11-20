# JealousVault

Encrypted backups for Linux hosts, with partial Proxmox support, through restic. One static binary,
system timers, integrity checks and predictable restore commands.

This index helps find the right section for a task: install, configuration, operations, restore or
troubleshooting.

Website: [jealousmoon.com/projects/vault](https://jealousmoon.com/projects/vault/) ·
[Russian version](https://jealousmoon.com/ru/proekty/vault/).

English · [Русский](ru/README.md)

## Features

- restic repositories: S3, SFTP, local path, REST.
- Full host backup or a custom set of paths; profiles `host`, `vm-dumps`, `custom`.
- Consistent PostgreSQL and MariaDB/MySQL dumps before restic.
- Retention, `check`, test restore (`test-restore`) and freshness checks.
- Alerts: Telegram (including forum topics), email, JSON webhooks, healthchecks.io-compatible ping.
- Multiple independent jobs on one host: separate config, state, cache, password, repository and
  units.
- Partial Proxmox support: `vzdump` and `restore-vm` for VM/CT archives.

## Documentation

| File                                               | What it covers                                                |
| -------------------------------------------------- | ------------------------------------------------------------- |
| [install.md](install.md)                           | installation from release assets, signature, upgrade, removal |
| [configuration.md](configuration.md)               | every TOML section and examples                               |
| [operations.md](operations.md)                     | timers, check/verify, retention, alerts, multiple jobs        |
| [restore.md](restore.md)                           | restoring files, databases, VM/CT and verifying them          |
| [platforms-and-limits.md](platforms-and-limits.md) | Linux, FreeBSD, Termux, NixOS, Proxmox, limits                |
| [troubleshooting.md](troubleshooting.md)           | common errors and diagnostics                                 |

Russian documentation: [ru/README.md](ru/README.md).

## Project documents

| File                                     | What it covers                                              |
| ---------------------------------------- | ----------------------------------------------------------- |
| [mvp.md](mvp.md)                         | MVP criteria, public product, commercial extensions         |
| [../STABILITY.md](../STABILITY.md)       | public API, deprecation policy, breaking changes            |
| [../CHANGELOG.md](../CHANGELOG.md)       | release history                                             |
| [../MAINTAINERS.md](../MAINTAINERS.md)   | maintainer model, response targets, bus factor              |
| [../CONTRIBUTING.md](../CONTRIBUTING.md) | DCO, checks, review expectations                            |
| [../ECOSYSTEM.md](../ECOSYSTEM.md)       | plugins, commercial extensions, alternative implementations |
| [../CLA.md](../CLA.md)                   | commercial extension contributor licence agreement          |
| [adr/README.md](adr/README.md)           | architecture decision records                               |

## Status

Beta. The core works on real Linux hosts, S3 and SFTP, PostgreSQL and MariaDB. Restores of all
repositories into a separate LXC are verified.

Tested:

- installation and upgrade from release assets: config and units survive, old snapshots remain;
- PostgreSQL and MariaDB at 20 million rows / about 20 GiB: dump, upload, restore from the
  repository, `count/sum` match;
- 6 real hosts;
- 20 LXC at the same time into one S3 storage, about 1.16 GiB of unique data per container and about
  22 GiB total;
- network interruption, stale locks, multiple jobs, Termux.

Not tested:

- databases in the hundreds of GiB;
- a Proxmox cluster, Windows VMs and large `vzdump` archives;
- more than 6 real hosts;
- more than 20 simultaneous LXC;
- the full matrix of all notification channels failing at once.

NixOS works with manual setup; a package or module is not planned yet.
