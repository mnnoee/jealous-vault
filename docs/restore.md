# Restore

English · [Русский](ru/restore.md)

This section covers returning data without overwriting the wrong directory.

## Snapshots

```sh
jealous-vault snapshots --config /etc/jealous-vault/config.toml
jealous-vault snapshots --all
```

`--all` shows snapshots from every host in a shared repository. By default the filter uses the
`host` from the config.

Estimate without writing:

```sh
jealous-vault restore --estimate --snapshot latest
jealous-vault restore --estimate --json --snapshot latest
```

## Restore files

```sh
sudo jealous-vault restore \
  --snapshot latest \
  --target /srv/restore --yes
```

The command restores the whole snapshot filesystem into the target. Paths keep their structure:
`/etc/hosts` becomes `/srv/restore/etc/hosts`.

`/srv` is a system directory, so a whole-snapshot restore needs `--yes` even when the target is a
subdirectory. The same applies to `/etc`, `/home`, `/var`, `/opt` and other top-level system
directories. A restore with `--include` is treated as selective and does not need `--yes`.

Selected paths:

```sh
sudo jealous-vault restore \
  --snapshot latest \
  --target /srv/restore \
  --include /etc \
  --include /var/www
```

Behaviour for existing files:

```sh
--overwrite always       # default
--overwrite never
--overwrite if-changed
```

For a restore into `/` or another system directory, add `--yes`:

```sh
sudo jealous-vault restore --snapshot latest --target / --yes
```

Verify after restore:

```sh
sudo jealous-vault restore --snapshot latest --target /srv/restore --yes --verify
```

Stop services and make sure you have console access before restoring over a live system. Prefer a
separate directory and copy data into place afterwards.

## Test restore

The main verification command:

```sh
jealous-vault test-restore --config /etc/jealous-vault/config.toml
```

It:

- selects the latest snapshot;
- checks `max_size`/`min_free`;
- restores into a temporary directory;
- verifies the contents;
- removes the directory unless `--keep` is set;
- writes state/history/audit.

Useful flags:

```sh
--snapshot ID
--level files|service
--max-size 5GB
--min-free 10GB
--target /srv/verify
--keep
--no-notify
```

`--level service` requires `verify.post_hook`; the hook runs after restore.

## Mount

Read-only snapshot access through FUSE:

```sh
sudo jealous-vault mount --config /etc/jealous-vault/config.toml
```

This needs `fusermount`/FUSE. It is useful for finding a file without unpacking the whole snapshot.
The mount point and behaviour depend on restic; unmount when finished.

## Recipes

Named restore parameters in `[restore.recipes.*]`:

```toml
[restore.recipes.postgres]
service = "postgresql"
target = "/"
includes = ["/var/lib/postgresql", "/etc/postgresql"]
overwrite = "always"
verify = true
pre_hook = "systemctl stop postgresql"
post_hook = "systemctl start postgresql"
```

```sh
sudo jealous-vault restore --recipe postgres --snapshot latest --yes
```

## Restoring databases

Dumps live in the snapshot under `[backup.dumps].dir`, for example:

```text
/var/lib/jealous-vault/dumps/dump-20261119-.../postgres-app.dump
/var/lib/jealous-vault/dumps/dump-20261119-.../mysql-app.sql
```

Restore the files first:

```sh
sudo jealous-vault restore --snapshot latest \
  --target /srv/db-restore --include /var/lib/jealous-vault/dumps
```

PostgreSQL:

```sh
createdb -O app app_restore
pg_restore --no-owner --no-acl -d app_restore \
  /srv/db-restore/var/lib/jealous-vault/dumps/dump-.../postgres-app.dump
```

MySQL/MariaDB:

```sh
mariadb -e 'CREATE DATABASE app_restore'
mariadb app_restore < \
  /srv/db-restore/var/lib/jealous-vault/dumps/dump-.../mysql-app.sql
```

Check:

```sh
psql -d app_restore -c 'SELECT count(*) FROM ...'
mariadb -N -e 'SELECT count(*) FROM app_restore....'
```

For a large database, compare more than count: checksums, `sum(id)` or `sum(length(...))`.

## Restore VM/CT (Proxmox)

Partial support: `[proxmox]` and a snapshot with `vzdump` archives are required.

```sh
sudo jealous-vault restore-vm --config /etc/jealous-vault/config.toml \
  100 latest
```

The first argument is the VMID, the second is a snapshot ID or `latest`.

Flags:

```sh
--target DIR      temporary directory for archive extraction
--storage NAME    override proxmox.storage
--force           overwrite an existing VM/CT
--keep            keep the temporary directory
```

`restore-vm` extracts the archive from the snapshot and calls `qmrestore`/`pct restore`. Clusters,
migrations and Windows VMs need separate testing.

## Restore safety

- Use a dedicated `--target` unless you intentionally restore over the system.
- Use `--yes` only for `/` and system directories.
- Stop databases/services before restoring their files.
- Check free space: the snapshot can be larger than it looks.
- Do not restore over a running database; use the dump instead.
- In an unprivileged LXC, device nodes and SELinux xattrs may not be created; this is a container
  limitation, not data loss.

## Tested in practice

Tested on large databases: 20 million rows.

| Database   | Size      | Dump  | Upload           | Restore                                   | Verification    |
| ---------- | --------- | ----- | ---------------- | ----------------------------------------- | --------------- |
| PostgreSQL | ~22 GB    | 8m18s | 9m49s / 20.8 GiB | download 5m56s + verify, `pg_restore` 22m | count/sum match |
| MariaDB    | ~21.8 GiB | 5m04s | 9m30s / 19.9 GiB | stream load 27m57s                        | count/sum match |

This is not a guarantee for hundreds of gigabytes, but it shows that dump/upload/restore work at
tens of gigabytes without losing data.

## Common problems

- If `target` is not empty, use `--overwrite` or another directory.
- If restore into `/` is refused, add `--yes` after checking the target.
- `mknod: operation not permitted`: an unprivileged LXC cannot create a device node; exclude it or
  use a privileged CT.
- `xattr: operation not permitted`: for example `security.selinux`; use `--exclude-xattr` if
  available, or a privileged CT.
- `permission denied` during verify: files were restored with restricted permissions; verify as root
  or in a directory with suitable permissions.
- Not enough space: check `--estimate`, `min_free` and the restic cache.
