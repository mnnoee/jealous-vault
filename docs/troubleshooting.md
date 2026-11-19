# Troubleshooting

English · [Русский](ru/troubleshooting.md)

This section helps find the cause of a failure quickly and avoid making it worse.

## Start here

```sh
jealous-vault doctor --config /etc/jealous-vault/config.toml
jealous-vault status --config /etc/jealous-vault/config.toml
jealous-vault history --limit 20 --config /etc/jealous-vault/config.toml
journalctl -u jealous-vault-backup.service --since today
```

`doctor` checks the config, permissions, restic, repository, paths, timers, alerts and S3. It does
not change data.

## Repository is locked

```text
unable to create lock in backend: ... permission denied
repository is already locked exclusively by PID ...
```

1. Check whether a backup/check is running: `ps -eo pid,args | grep -E 'restic|jealous-vault'`.
2. If the process is alive, wait.
3. If the process was killed: `jealous-vault repo unlock --config ...`.

For an SFTP repository, lock files are created by the repository user. Root-owned files in `locks/`
(for example after a manual `restic` run as root) break the next backup: the SFTP user cannot read
them. Remove the stale lock and check ownership:

```sh
ls -la /path/to/repo/locks
find /path/to/repo -user root -ls
```

Do not run local restic commands against an SFTP repository as root; use the repository owner.

## S3

| Symptom                 | What to check                    |
| ----------------------- | -------------------------------- |
| `InvalidAccessKeyId`    | keys, project/bucket, `env_file` |
| `NoSuchBucket`          | bucket name, region, endpoint    |
| DNS/timeout             | resolution, network, firewall    |
| TLS error               | CA, `https`, corporate proxy     |
| `SignatureDoesNotMatch` | region, path-style, secret       |

`env_file` contains KEY=VALUE only, mode `0600`; extra keys may be rejected. S3-compatible storage
often needs path-style; restic chooses it from the endpoint.

Check access:

```sh
jealous-vault doctor
jealous-vault repo stats
```

## SFTP

- Private key: `0600`, not group/other readable.
- `known_hosts` must contain the host key.
- `sftp_key_file`/`sftp_known_hosts` work only with an `sftp:` URL.
- The agent generates a private `ssh_config`; the system `~/.ssh/config` is not used.
- Server `authorized_keys` may restrict `from=` or `command="internal-sftp"`; connect from an
  allowed IP.
- `Permission denied` on lock: see the lock/ownership section.

## Database dumps

### `Access denied for user ...`

- Check `user`, `host`, `port`, `password_file`.
- For `host = "127.0.0.1"` the user needs a grant for `'user'@'127.0.0.1'`; for `host = "localhost"`
  a grant for `'user'@'localhost'` (unix socket).
- `--routines` needs `SHOW CREATE ROUTINE` (MariaDB) or the equivalent.
- `--events`/`--triggers` need the matching privileges.

### `/root/.my.cnf` overrides the password

The MySQL client reads `~/.my.cnf` after `--defaults-extra-file`. If it contains another password,
the dump uses that one. The agent isolates `HOME`, but manual runs still need care. Check:

```sh
sudo -n grep -E '^(user|password|host)' /root/.my.cnf
```

### `mysqldump` is deprecated

MariaDB 12.3 warns and suggests `mariadb-dump`. The dump still works, but you can set:

```toml
[backup.dumps.mysql]
binary = "mariadb-dump"
```

### MyISAM

`--single-transaction` does not give consistency for MyISAM. Such tables need locks or a write
pause.

### Long dump

- Check `pg_size_pretty(pg_total_relation_size(...))` / `information_schema.tables`.
- Make sure the `backup.dumps.dir` disk is not full.
- Large databases: 20 GiB is tested; hundreds of GiB need separate planning.
- Do not set `attempts` too high for a multi-minute dump.

## Timers

```sh
systemctl list-timers 'jealous-vault*'
systemctl cat jealous-vault-backup.timer
systemctl is-enabled jealous-vault-backup.timer
systemctl reset-failed jealous-vault-backup.service
```

- `RandomizedDelaySec` moves the run; this is normal.
- After editing units: `systemctl daemon-reload`.
- If the timer is disabled, `freshness` reports a timer problem when `respect_backup_timer=true`.
- Without systemd, use `tick`/cron; make sure `tick` is actually invoked (log/systemd/cron).

## Upgrade and releases

- Download the binary, `SHA256SUMS`, `SHA256SUMS.sig`.
- Verify with `sha256sum -c` and `ssh-keygen -Y verify`.
- In a lab with a private CA, `curl -k` is acceptable only together with signature verification; a
  public release must use normal TLS.
- An older binary may not know new config keys. Upgrade forward; keep a copy of the config before a
  rollback.
- `install` after an upgrade regenerates units but does not touch state or snapshots.

## Restore

- `refusing to restore ... without --yes`: the target is `/` or a system directory; check it and add
  `--yes`.
- `mknod ... operation not permitted`: unprivileged LXC; exclude the device node.
- `xattr ... operation not permitted`: SELinux/xattr; exclude the xattr or use a privileged CT.
- `permission denied` during `--verify`: the restore used restricted permissions; verify as root.
- Not enough space: `--estimate`, `verify.min_free`, clear the cache.
- For databases, do not restore files over a running DB; use the dump.

## State and last_verify

- On a damaged `state.json`, the agent moves it to `state.json.corrupt-*` and continues from empty
  state. The file remains for inspection.
- `last_verify` stores a snapshot ID. If retention removed it, `status` keeps showing the old verify
  (it is a local record) while `doctor` warns that the snapshot is no longer in the repository. Run
  `test-restore` to refresh it.
- `doctor` queries the repository for diagnostics; this is expected.

## Disk and cache

- Dumps: `backup.dumps.dir`, `keep`.
- restic cache: `repository.cache_dir`.
- Temporary verify target: next to state.
- `restic prune` may need extra space temporarily.
- Check `df -h` before a large restore or dump.

## Audit and history

- `audit_file`: what was done: backup/restore/check/verify/repo.
- `history_file`: statuses and durations.
- If history is empty after upgrading from an older release, check whether that release had the
  `history` command; new releases write JSONL.

## Termux and NixOS

- Termux: see [platforms-and-limits.md](platforms-and-limits.md); check `tick.log`, time/UTC,
  JobScheduler and access to `/storage/emulated/0`.
- NixOS: works manually; no package/module yet and none planned for the near future.
