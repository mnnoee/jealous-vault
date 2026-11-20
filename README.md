<p align="center">
  <img src="docs/assets/logo.svg" width="112" alt="JealousVault">
</p>

<h1 align="center">JealousVault</h1>

<p align="center">
  <b>Encrypted backups for Linux, with partial Proxmox support. Predictable restores.</b><br>
  One binary, restic inside, system timers and explicit commands.
</p>

<p align="center">
  <a href="https://jealousmoon.com/projects/vault/">jealousmoon.com/projects/vault</a> ·
  <a href="https://jealousmoon.com/ru/proekty/vault/">/ru/proekty/vault</a>
</p>

<p align="center">
  <a href="README.ru.md">Русский</a> · English
</p>

<p align="center">
  <img alt="License" src="https://img.shields.io/badge/license-Apache--2.0-blue">
  <img alt="Go" src="https://img.shields.io/badge/Go-1.27-00ADD8?logo=go&logoColor=white">
  <img alt="restic" src="https://img.shields.io/badge/restic-0.18%20%7C%200.19-4B5563">
  <img alt="Status" src="https://img.shields.io/badge/status-beta-F59E0B">
</p>

---

## Start here

| Task                               | Document                                                     |
| ---------------------------------- | ------------------------------------------------------------ |
| Install or upgrade                 | [docs/install.md](docs/install.md)                           |
| Restore files, databases or VMs    | [docs/restore.md](docs/restore.md)                           |
| Platform support and tested limits | [docs/platforms-and-limits.md](docs/platforms-and-limits.md) |
| Fix a failure                      | [docs/troubleshooting.md](docs/troubleshooting.md)           |
| Stability and changes              | [STABILITY.md](STABILITY.md) · [CHANGELOG.md](CHANGELOG.md)  |
| Scope, MVP and extensions          | [docs/mvp.md](docs/mvp.md)                                   |

## Why

restic handles encryption, deduplication and repository format. It still leaves the operator to wire
schedules, retention, checks, alerts, database dumps, restores and repeatable unit files.
JealousVault puts those pieces behind one config and a set of commands that fit a runbook.

- restic repositories encrypt the data; the password lives separately.
- S3, SFTP, local paths and REST: no vendor lock and no server of its own.
- PostgreSQL and MariaDB/MySQL are dumped before backup instead of being copied while running.
- `check`, `test-restore`, `freshness`, `doctor` and `history` test integrity, restores and
  freshness, not just write a snapshot.
- `restore` brings back files, directories and databases; `restore-vm` covers VM/CT on Proxmox
  partially.
- Multiple jobs on one host: each has its own config, state, cache, password and units.

## Quick start

```sh
VERSION=0.1.0
BASE="https://jealousmoon.com/projects/vault/releases/download/$VERSION"

curl -fLO "$BASE/jealous-vault-linux-amd64"
curl -fLO "$BASE/SHA256SUMS"
curl -fLO "$BASE/SHA256SUMS.sig"
sha256sum -c --ignore-missing SHA256SUMS

sudo install -m 0755 jealous-vault-linux-amd64 /usr/local/bin/jealous-vault

sudo jealous-vault init \
  --repo s3:https://s3.example.com/backup/host1 \
  --paths /etc,/home,/var/www \
  --profile host

sudo jealous-vault install
sudo jealous-vault doctor
sudo jealous-vault backup
```

Signature verification and requirements: [docs/install.md](docs/install.md).

## What is inside

| Area          | What it does                                                     |
| ------------- | ---------------------------------------------------------------- |
| Backup        | `jealous-vault backup`, systemd/cron/`tick`, retention and prune |
| Checks        | `check`, `test-restore`, `doctor`, `freshness`                   |
| Restore       | `restore`, `restore-vm`, `mount`, named recipes                  |
| Databases     | `pg_dump`, `mysqldump`/`mariadb-dump`, a separate dump-only job  |
| Alerts        | Telegram (including forum topics), email, webhooks, healthchecks |
| Multiple jobs | separate config, state, cache, password and units per job        |
| Manual escape | `export-restic-env`, `recovery-kit`, documented restic commands  |
| Proxmox       | partial support: `vzdump`, `restore-vm`                          |

## Platforms

| Platform                                 | Status                                     |
| ---------------------------------------- | ------------------------------------------ |
| Debian / Ubuntu / Fedora / Arch / Alpine | supported, systemd                         |
| Artix / OpenRC / runit / cron            | supported through `tick`                   |
| FreeBSD                                  | supported through `tick`/cron              |
| Termux (Android)                         | manual: runsv/runit or JobScheduler        |
| NixOS                                    | works manually; no package/module yet      |
| Proxmox VE 8/9                           | partial support: `vzdump` and `restore-vm` |

## Documentation

| Section                                              | What it covers                                        |
| ---------------------------------------------------- | ----------------------------------------------------- |
| [Install and upgrade](docs/install.md)               | release assets, signature, `init`, `install`, upgrade |
| [Configuration](docs/configuration.md)               | every TOML section and examples                       |
| [Operations](docs/operations.md)                     | timers, check/verify, retention, multiple jobs, audit |
| [Restore](docs/restore.md)                           | files, databases, VM/CT, verification                 |
| [Platforms and limits](docs/platforms-and-limits.md) | Termux, NixOS, Proxmox, large databases, copy         |
| [Troubleshooting](docs/troubleshooting.md)           | locks, S3/SFTP, dumps, restores, state                |

The Russian documentation is under [docs/ru/](docs/ru/README.md).

## Security

- restic encrypts the repository; the password is kept separately with mode `0600`.
- Configs and secrets live in `/etc/jealous-vault` and are excluded from backups.
- The agent does not listen for inbound connections; alerts are outbound only.
- Vulnerabilities: [SECURITY.md](SECURITY.md), contact `vault@jealousmoon.com` (fallback:
  `me@mnnoee.ru`).
- Machine-readable contact: [`.well-known/security.txt`](.well-known/security.txt).

## Status

Beta. The core works on real Linux hosts, S3/SFTP, PostgreSQL and MariaDB. Restores of all
repositories into a separate LXC are verified. Large databases are tested at 20 GiB; hundreds of GiB
and a Proxmox cluster are still ahead. NixOS works with manual setup; no package yet.

Tested: 6 real hosts; 20 LXC at the same time into one S3 storage, about 1.16 GiB of unique data per
container and about 22 GiB total. Not tested: more than 6 real hosts, more than 20 simultaneous LXC,
databases in the hundreds of GiB, a Proxmox cluster.

What the beta does and does not cover, and what the MVP still needs:
[docs/mvp.md](docs/mvp.md).

## Ecosystem

JealousVault is an open project, not a gated product. Its ecosystem is open to independent
developers, third-party extensions and alternative implementations. Commercial extensions are
welcome, and their authors retain ownership of their respective work. See
[ECOSYSTEM.md](ECOSYSTEM.md).

## Contributing

- [Contributing](CONTRIBUTING.md)
- [Maintainers](MAINTAINERS.md)
- [Ecosystem](ECOSYSTEM.md)
- [Commercial extension CLA](CLA.md)
- [Code of conduct](CODE_OF_CONDUCT.md)
- [Security policy](SECURITY.md)
- [Stability contract](STABILITY.md) · [Changelog](CHANGELOG.md)
- [MVP and scope](docs/mvp.md) · [Architecture decisions](docs/adr/README.md)
- Documentation: [docs/README.md](docs/README.md)

## License

Apache License 2.0. See [LICENSE](LICENSE) and [NOTICE](NOTICE).
