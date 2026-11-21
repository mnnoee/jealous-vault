# Platforms and limits

English · [Русский](ru/platforms-and-limits.md)

This section helps choose a platform and know what works and what does not.

## Linux

Static binaries for `linux/amd64` and `linux/arm64`, no cgo. Tested:

- Debian 12/13, Ubuntu 24.04;
- Fedora;
- Arch Linux;
- Alpine;
- Artix (OpenRC and s6);
- Void, Devuan, Alma, Rocky, openSUSE, Gentoo in the LXC matrix.

You need restic 0.18.x or 0.19.x. If the version is newer, `doctor` shows a warning, but backup keeps working. Restore, drill and test-restore refuse a series older than 0.18.

Schedulers:

- systemd: the main option, `install` without flags;
- cron: `--init cron`, `tick` every 15 minutes;
- OpenRC: `--init openrc`;
- runit: `--init runit` or `tick` from a run script.

## FreeBSD

Tested: root, init, backup, check, restore, `tick`, `/etc/crontab`. Paths and units differ from Linux; use `tick`/cron.

## Termux (Android)

Termux has no systemd, but it has runsv/runit through `termux-services`. The tested setup uses:

- `jealous-vault tick` on a schedule;
- `termux-job-scheduler` with `persisted=true`, every 15 minutes;
- a separate `jv-tick.sh` that calls `tick` and writes a log.

`tick` decides what is due: backup, check, freshness.

Notes:

- no root; only shared storage `/storage/emulated/0` is readable;
- `Android/data` and `Android/obb` may be unavailable; exclude them;
- `$HOME` in Termux hits xattrs under `/data/data`, so a normal backup of the home directory does not work; use proot or shared storage;
- the process may run in UTC: `on_calendar = 03:30` fires at 03:30 UTC. Set `TZ` in the `tick` script if you need local time;
- the phone may be offline or in Doze; `JobScheduler` does not guarantee exact timing.

Tested on Android 12/Termux arm64: backup, check, restore a file, `tick` and JobScheduler.

## NixOS

NixOS works reliably with manual setup. There is no ready package or module yet, and none is planned for the near future. For now use `tick`/cron or your own wrapper.

## Proxmox

Partial support, not the main platform:

- `vzdump` collects VM/CT archives into a separate directory;
- restic backs up the archives;
- `restore-vm` extracts them and calls `qmrestore`/`pct restore`.

Tested on PVE 9.2.2 with VMs and CTs. Not tested:

- clusters and migrations between nodes;
- Windows VMs and unusual disk controllers;
- very large disks and multiple storage targets at once.

For a normal Linux host the Proxmox section is simply disabled.

## Storage

- S3: RustFS, Backblaze B2 and compatible services; path-style and virtual-host.
- SFTP: dedicated key and `known_hosts`, private `ssh_config`.
- local: a directory or disk.
- REST: a restic REST server.

restic always encrypts the repository. The password is kept separately; without it the data cannot be restored.

## Databases

- PostgreSQL: `pg_dump --format=custom`, restore with `pg_restore`.
- MariaDB/MySQL: `mysqldump`/`mariadb-dump` with `--single-transaction`, `--routines`, `--events`, `--triggers`.

Tested:

- PostgreSQL 20 million rows, table about 22 GB: dump 8m18s, upload 9m49s, restore from the repository plus `pg_restore` 22m, count/sum match;
- MariaDB 20 million rows, about 21.8 GiB: dump 5m04s, upload 9m30s, stream load 27m57s, count/sum match.

Limits:

- `--single-transaction` gives consistency for InnoDB, not MyISAM;
- hundreds of gigabytes and hour-long dumps are not tested;
- with `host=127.0.0.1` the user needs a TCP grant; prefer `host=localhost`;
- `/root/.my.cnf` can override `password_file`; the agent isolates `HOME`, but remember this for manual runs;
- MariaDB 12.3 considers `mysqldump` deprecated; `binary = "mariadb-dump"` removes the warning.

## Copy / 3-2-1

`[copy]` copies snapshots to a second repository through `restic copy`. The copy password must differ from the primary one.

restic uses one set of backend credentials per process. S3 to S3 between different providers with different keys may not work directly. Options:

- a local source on the storage host;
- a raw repository mirror through rclone;
- identical credentials (not recommended).

## Scale and resilience

Tested:

- 6 real hosts;
- 20 LXC at the same time into one S3 storage: about 1.16 GiB of unique data per container, about 22 GiB in the repository;
- several jobs on one host;
- network interruption: a short cut recovers, a long one fails cleanly and retry succeeds;
- stale locks and repeated runs;
- Telegram, email, webhooks, forum topics;
- restoring all repositories into a separate LXC with `--verify` and SHA-256.

Not tested:

- more than 6 real hosts;
- more than 20 simultaneous LXC;
- very large databases (hundreds of GiB);
- a Proxmox cluster;
- the full matrix of all notification channels failing at once.

## Config compatibility on upgrade

A new binary reads a config from an older version. The reverse is not guaranteed: an older binary may not know new keys. Upgrade forward; after an upgrade the config, units and old snapshots are kept.

Therefore:

- upgrade forward;
- keep a copy of the config before a rollback;
- run `doctor` after an upgrade.

## Known limits

- NixOS works manually; no package/module yet.
- Termux works manually, with time and storage caveats.
- Proxmox is partial; clusters, Windows and huge disks are untested.
- Large databases are tested at 20 GiB, not hundreds.
- `status` shows the local `last_verify`; if retention deleted the snapshot, `doctor` warns and `status` does not (by design: status does not query the repository).
- On a damaged `state.json`, the agent moves it to `state.json.corrupt-*` and starts from an empty state. This is recovery, not a failure; the damaged file remains for inspection.
- No Prometheus metrics; most commands support JSON output.
- No packages, website or public TLS URL yet.
