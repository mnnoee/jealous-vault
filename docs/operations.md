# Operations

English · [Русский](ru/operations.md)

This section explains what runs on a schedule and what to do when something fails.

## Daily commands

| Command                          | What it does                                      |
| -------------------------------- | ------------------------------------------------- |
| `jealous-vault status`           | last backup/check/verify, freshness, lock         |
| `jealous-vault history`          | recent runs from the JSONL history                |
| `jealous-vault snapshots`        | snapshots in the repository                       |
| `jealous-vault doctor`           | config, restic, repository, paths, timers, alerts |
| `jealous-vault backup`           | run a backup now                                  |
| `jealous-vault backup --dry-run` | show what would be backed up                      |
| `jealous-vault check`            | verify repository integrity                       |
| `jealous-vault test-restore`     | test restore with verification                    |
| `jealous-vault freshness`        | check the age of the last backup                  |
| `jealous-vault repo unlock`      | remove stale restic locks                         |

All commands accept `--config`; most accept `--json` and `--verbose`.

`status` exits non-zero when the last backup failed or is older than `--max-age` (25h by default).
This is useful for monitoring.

## Backup lifecycle

1. Pre-hooks (`backup.pre_hooks`). A failure cancels the backup.
2. Database dumps (`[backup.dumps]`), when enabled.
3. `restic backup` with paths and excludes.
4. `restic forget`/`prune` according to `[retention]`.
5. Cleanup of old dump directories.
6. Post-hooks (`backup.post_hooks`); a failure is a warning.
7. Write state/history/audit.
8. Alerts on failure; on success only when `notify_on_success` is enabled.

Backup is idempotent: a repeated run creates a new snapshot, while restic deduplication keeps the
repository from growing by the same data.

## Timers

`install` creates:

| Unit                            | When                                  |
| ------------------------------- | ------------------------------------- |
| `jealous-vault-backup.timer`    | `schedule.on_calendar` + jitter       |
| `jealous-vault-check.timer`     | weekly                                |
| `jealous-vault-verify.timer`    | `verify.schedule`                     |
| `jealous-vault-freshness.timer` | `freshness.schedule`                  |
| `jealous-vault-copy.timer`      | `copy.schedule`, when copy is enabled |
| `jealous-vault-proxmox.timer`   | `proxmox.schedule`, when enabled      |

Check:

```sh
systemctl list-timers 'jealous-vault*'
systemctl status jealous-vault-backup.timer
systemctl list-units --failed | grep jealous-vault || true
```

If a timer did not fire:

```sh
systemctl cat jealous-vault-backup.timer
journalctl -u jealous-vault-backup.service --since today
systemctl is-enabled jealous-vault-backup.timer
```

`RandomizedDelaySec` is normal: the run can move by `randomized_delay`.

## Check

```sh
jealous-vault check --config /etc/jealous-vault/config.toml
jealous-vault check --read-data-subset 10%
```

`check` verifies repository structure and snapshots. `read_data_subset` also reads part of the pack
files; set the percentage in the config or with a flag.

The timer starts check, and the result goes to state/history. A failure sends an alert.

## Verify / test-restore

```sh
jealous-vault test-restore --config /etc/jealous-vault/config.toml
jealous-vault test-restore --snapshot latest --level files --keep
```

`test-restore`:

- selects a snapshot (latest for this host by default);
- checks `max_size` and free space;
- restores into a separate directory;
- verifies the contents (`restic --verify`);
- removes the temporary directory unless `--keep` is set;
- writes state/history/audit and sends an alert on failure.

If retention later deletes the verified snapshot, `doctor` warns about it. This is expected: the
`last_verify` record keeps the ID, but the snapshot is gone. Run `test-restore` again to refresh the
check.

For a service check:

```toml
[verify]
level = "service"
post_hook = "systemctl restart myapp && curl -fsS http://127.0.0.1:8080/health"
```

## Freshness

`freshness` checks that the last successful backup is not older than `max_age`. If the backup timer
is disabled, `respect_backup_timer=true` reports a timer problem instead of a stale snapshot. For
Termux/non-systemd hosts, set `respect_backup_timer=false`.

## Retention

The `keep_last/daily/weekly/monthly` policy runs after a successful backup. `prune=true` frees space
but rewrites the repository and can take time.

Examples:

```toml
# Minimal space
[retention]
keep_last = 2
keep_daily = 3
keep_weekly = 2
keep_monthly = 1
prune = true

# Long history
[retention]
keep_last = 7
keep_daily = 14
keep_weekly = 8
keep_monthly = 12
prune = true
```

Before changing the policy, check that the snapshots you need will not be removed:

```sh
jealous-vault snapshots --all
jealous-vault repo stats
```

## Multiple jobs

A host can have several independent jobs. Each has:

- its own config `/etc/jealous-vault/<job>.toml`;
- its own state `/var/lib/jealous-vault/<job>/`;
- its own cache and lock;
- its own password/repository;
- units with the `-<job>` suffix.

Example: `/usr`, `/opt` and `/etc` in separate repositories. The upside is independent retention and
alerts; the downside is no deduplication between repositories.

Install and remove per config:

```sh
jealous-vault install --config /etc/jealous-vault/usr.toml
jealous-vault uninstall --config /etc/jealous-vault/usr.toml
```

## Locks

The local lock (`lock_file`) prevents two processes of the same job from running together. If a
process was killed, the lock can remain; `backup`/`check` clear stale locks. For restic use:

```sh
jealous-vault repo unlock --config /etc/jealous-vault/config.toml
```

Do not delete restic lock files by hand until you are sure no process is running. For an SFTP
repository, lock files must belong to the repository user; root-owned files break the next backup.

## Audit and history

- `audit_file`: JSONL of key actions: backup, restore, check, verify, repo init/unlock, uninstall.
- `history_file`: JSONL of runs with duration, status and snapshot ID.
- `alerts_file`: alert deduplication windows.

These are local journals; they do not replace external monitoring, but they show what ran and when.

## Logs

- systemd: `journalctl -u jealous-vault-backup.service`.
- CLI: `--verbose` for debugging.
- logrotate is installed at `/etc/logrotate.d/jealous-vault`.
- On failure, run `doctor`, then `status`/`history`, then read the unit journal.

## Regular maintenance

Monthly or quarterly:

```sh
jealous-vault doctor
jealous-vault check --read-data-subset 5%
jealous-vault test-restore
jealous-vault snapshots --all
```

Yearly:

- make sure the repository password and release verification keys are available outside the host;
- test a restore on a clean machine;
- rotate S3/SFTP keys;
- review retention.

## Monitoring

Minimum set:

- `status`/`freshness` on a schedule, with a non-zero exit on problems;
- Telegram/email/webhook alerts;
- `doctor` after config changes;
- `systemctl list-units --failed`.

Prometheus metrics and JSON logs are not available yet; most commands support `--json`.
