# Configuration

English · [Русский](ru/configuration.md)

This section explains how to change the config without breaking backup or restore.

The config is TOML, usually `/etc/jealous-vault/config.toml`, mode `0600`. Secrets never go into the config: only paths to files.

`jealous-vault init` writes a working, commented config. Below is a map of the sections and the rules that matter when editing it by hand.

## Minimal config

```toml
host = "host1"

[repository]
url = "s3:https://s3.example.com/backup/host1"
password_file = "/etc/jealous-vault/restic.pass"
env_file = "/etc/jealous-vault/restic.env"

[backup]
profile = "host"
paths = ["/etc", "/home", "/var/www"]
excludes = ["/tmp", "/var/cache", "/etc/jealous-vault"]

[retention]
keep_last = 3
keep_daily = 7
keep_weekly = 4
keep_monthly = 6
prune = true

[schedule]
on_calendar = "*-*-* 03:30:00"
randomized_delay = "1h"
```

## Top level

| Key             | Default                              | Meaning                          |
| --------------- | ------------------------------------ | -------------------------------- |
| `host`          | hostname                             | host name in snapshots           |
| `job`           | `default`                            | job name, `[a-z0-9][a-z0-9_-]*`  |
| `state_file`    | `/var/lib/jealous-vault/state.json`  | last result                      |
| `history_file`  | next to state                        | JSONL history                    |
| `lock_file`     | next to state                        | local lock against parallel runs |
| `alerts_file`   | next to state                        | alert deduplication windows      |
| `audit_file`    | `/var/log/jealous-vault/audit.jsonl` | action journal                   |
| `restic_binary` | `restic`                             | restic path/name                 |

For jobs `db`, `usr` or `opt`, the agent places default paths under `/var/lib/jealous-vault/<job>/` so jobs do not collide. You can set them explicitly.

## `[repository]`

```toml
[repository]
url = "s3:https://s3.example.com/backup/host1"
password_file = "/etc/jealous-vault/restic.pass"
env_file = "/etc/jealous-vault/restic.env"
cache_dir = "/var/cache/jealous-vault"
```

| Key                | Meaning                                       |
| ------------------ | --------------------------------------------- |
| `url`              | restic URL: `s3:`, `sftp:`, `rest:`, `local:` |
| `password_file`    | repository password, `0600`                   |
| `env_file`         | KEY=VALUE for S3/cloud, `0600`                |
| `cache_dir`        | restic index cache                            |
| `sftp_key_file`    | private SFTP key, `0600`                      |
| `sftp_known_hosts` | known_hosts                                   |
| `sftp_config`      | private ssh_config; generated automatically   |

Example S3 env file:

```ini
AWS_ACCESS_KEY_ID=...
AWS_SECRET_ACCESS_KEY=...
AWS_DEFAULT_REGION=us-east-1
```

For S3 with a self-signed certificate, use a trusted CA; restic does not disable TLS verification by default.

## `[backup]`

| Key               | Default | Meaning                                         |
| ----------------- | ------- | ----------------------------------------------- |
| `profile`         | `host`  | `host`, `vm-dumps`, `custom`                    |
| `paths`           | profile | absolute paths                                  |
| `excludes`        | profile | exclude patterns                                |
| `tags`            | `[]`    | snapshot tags                                   |
| `pre_hooks`       | `[]`    | commands before backup; failure cancels the run |
| `post_hooks`      | `[]`    | commands after backup; failure is a warning     |
| `one_file_system` | `true`  | do not cross filesystems                        |
| `timeout`         | `6h`    | total backup + retention timeout                |
| `attempt_timeout` | `2h`    | one restic run timeout                          |
| `attempts`        | `3`     | number of attempts                              |
| `retry_delay`     | `1m`    | pause between attempts                          |
| `limit_upload`    | `0`     | KiB/s, 0 = unlimited                            |
| `compression`     | `auto`  | `auto`, `off`, `max`                            |
| `pack_size`       | `0`     | MiB, 0 = restic default                         |

Profiles:

- `host`: `/etc`, `/var/www` and system paths; on Proxmox also `/etc/pve`;
- `vm-dumps`: only `/var/lib/vz/dump`;
- `custom`: no defaults; everything comes from `paths`.

On Proxmox `/etc/pve` is a separate FUSE mount. With `one_file_system=true`, list it in `paths` explicitly or its contents will be skipped.

## `[backup.dumps]`

Consistent database dumps before restic:

```toml
[backup.dumps]
dir = "/var/lib/jealous-vault/dumps"
keep = 3

[backup.dumps.postgres]
enabled = true
databases = ["app"]
host = "127.0.0.1"
port = 5432
user = "backup"
password_file = "/etc/jealous-vault/pg.pass"
binary = "pg_dump"

[backup.dumps.mysql]
enabled = true
host = "localhost"
databases = ["app"]
user = "backup"
password_file = "/etc/jealous-vault/mysql.pass"
binary = "mysqldump"
```

- The dump directory is added to restic automatically.
- After a successful backup, the newest `keep` directories remain.
- A dump failure cancels the backup: an incomplete set does not reach the repository.
- PostgreSQL: `pg_dump --format=custom`; restore with `pg_restore`.
- MySQL/MariaDB: `mysqldump --single-transaction --routines --events --triggers`; restore with `mysql`/`mariadb` from SQL.
- `--single-transaction` gives consistency for InnoDB, not for MyISAM. MyISAM tables are not locked by default.
- MariaDB 12.3 warns that `mysqldump` is deprecated; set `binary = "mariadb-dump"` to silence it.

MySQL/MariaDB grants (minimum):

```sql
CREATE USER 'backup'@'localhost' IDENTIFIED BY '<password>';
GRANT SHOW CREATE ROUTINE ON *.* TO 'backup'@'localhost';
GRANT SELECT, SHOW VIEW, TRIGGER, EVENT, LOCK TABLES
  ON `app`.* TO 'backup'@'localhost';
```

If the client connects over TCP, create the same user for `127.0.0.1`. Prefer `host = "localhost"` (unix socket) for a local database.

The agent passes the MySQL password through a temporary `--defaults-extra-file` and isolates `HOME` so `/root/.my.cnf` cannot override it; global option files still work.

## `[retention]`

```toml
[retention]
keep_last = 3
keep_daily = 7
keep_weekly = 4
keep_monthly = 6
keep_within = ""
keep_tag = []
prune = true
```

`forget` runs after a successful backup. `prune=true` physically removes unused blocks; it is slower but frees space. `keep_within` accepts values such as `"1y5m7d"`. `keep_tag` keeps snapshots with the listed tags.

## `[schedule]`

```toml
[schedule]
on_calendar = "*-*-* 03:30:00"
randomized_delay = "1h"
```

`on_calendar` uses systemd calendar syntax. `randomized_delay` spreads hosts so they do not hit the repository at once. Stagger multiple jobs.

`install` creates timers for:

- backup: `on_calendar`;
- check: weekly;
- verify: weekly (`verify.schedule`);
- freshness: several times a day (`freshness.schedule`);
- copy/proxmox: when enabled.

## `[freshness]`

```toml
[freshness]
max_age = "25h"
respect_backup_timer = true
schedule = "*-*-* 00,06,12,18:15:00"
```

Checks the age of the last successful backup. If the backup timer is disabled, `respect_backup_timer=true` reports a timer problem instead of a stale snapshot. For Termux or other non-systemd hosts, set it to `false`.

## `[check]`

```toml
[check]
read_data_subset = "10%"
timeout = "2h"
```

Runs `restic check`; `read_data_subset` reads part of the data (for example `5%`) to catch bit rot without reading the whole repository.

## `[verify]`

```toml
[verify]
enabled = true
level = "files"          # files or service
max_size = "10GB"
min_free = "20GB"
timeout = "2h"
schedule = "Sun *-*-* 05:00:00"
post_hook = ""
```

`test-restore` restores the latest snapshot into a separate directory and verifies files. `max_size`/`min_free` skip a snapshot that is too large or when space is low. `level="service"` requires `post_hook` and checks a service after restore.

If retention later deletes the verified snapshot, `doctor` warns that the snapshot is no longer in the repository and suggests running `test-restore` again. `status` shows the local verify record; use `doctor` for diagnostics.

## `[drill]`

```toml
[drill]
enabled = false
level = "files"          # files, service, db or full
schedule = "Sun *-*-* 06:00:00"
# target = "/var/lib/jealous-vault/drill"
max_size = "10GB"
min_free = "20GB"
timeout = "2h"
keep = false
service_hook = ""
db_script = ""
db_timeout = "30m"
```

`drill` restores a snapshot into a scratch directory and can also check a service and a scratch database. It is disabled by default; a manual `jealous-vault drill` works without enabling it. `enabled = true` makes `install` create the drill timer on systemd, cron or OpenRC.

`service_hook` is required for `service` and `full`; `db_script` is required for `db` and `full`. Both are shell commands and receive `JM_DRILL_*` variables. The full contract, report format and PostgreSQL/MySQL examples are in [DRILL.md](DRILL.md).

## `[notify]`

```toml
[notify]
webhooks = []
ping_url = ""
freshness_ping_url = ""
telegram_token = ""
telegram_chat_ids = ["-1001234567890:42"]
notify_on_success = false
timeout = "10s"

[notify.dedup]
backup = "3h"
freshness = "6h"

[notify.email]
enabled = false
mode = "local"           # local or smtp
from = "backup@example.com"
to = ["ops@example.com"]
host = "smtp.example.com"
port = 587
username = ""
password_file = "/etc/jealous-vault/smtp.pass"
security = "starttls"    # tls, starttls, none
timeout = "15s"
```

- Telegram: `chat_id` or `chat_id:thread_id` for a forum topic.
- HTML: host/time/duration in bold, errors/warnings in `<pre>`, class/code/detail in `<code>`; values are escaped.
- `webhooks` sends JSON POST; `ping_url` is healthchecks.io-style.
- `notify_on_success=false` means problems only. Deduplication suppresses identical alerts inside the window; `0` disables it.
- Secrets belong in `0600` files; the email password comes from `password_file` or `password_env`.

## `[copy]`

A second repository for 3-2-1:

```toml
[copy]
enabled = true
schedule = "*-*-* 05:00:00"
repository = "s3:https://other.example.com/backup/host1"
password_file = "/etc/jealous-vault/copy.pass"
env_file = "/etc/jealous-vault/copy.env"
new_only = true
```

The copy password must differ from the primary one. A separate timer runs `restic copy`.

restic uses one set of backend credentials per process. Copying S3 to S3 between different providers with different keys may not work directly. Options: use a local source on the storage host, or mirror the repository raw with rclone. SFTP/local sources work through `copy`.

## `[proxmox]`

```toml
[proxmox]
enabled = true
schedule = "*-*-* 02:00:00"
dump_dir = "/var/lib/vz/dump/jealous-vault"
mode = "snapshot"        # snapshot, suspend, stop
compress = "zstd"        # zstd, gzip, lzo, none
keep_last = 1
vms = []
all = true
storage = "local-lvm"
```

`vzdump` writes archives to `dump_dir`, then restic backs up the directory. `restore-vm` restores a VM/CT from a snapshot. Needs PVE 8/9 and `qmrestore`/`pct restore`. Clusters, Windows VMs and large disks are not fully tested.

## `[restore]`

```toml
[restore]
pre_hooks = []
post_hooks = []

[restore.recipes.postgres]
service = "postgresql"
target = "/"
includes = ["/var/lib/postgresql", "/etc/postgresql"]
overwrite = "always"
verify = true
pre_hook = ""
post_hook = ""
```

A recipe is a named set of restore parameters:

```sh
jealous-vault restore --recipe postgres --snapshot latest --yes
```

Hooks can stop and start services around the restore.

## Multiple jobs

```toml
# /etc/jealous-vault/usr.toml
host = "host1"
job = "usr"

[repository]
url = "s3:https://s3.example.com/backup/host1-usr"
password_file = "/etc/jealous-vault/usr.pass"
env_file = "/etc/jealous-vault/restic.env"

[backup]
profile = "custom"
paths = ["/usr"]
```

Install:

```sh
sudo jealous-vault install --config /etc/jealous-vault/usr.toml
```

Units get the `-usr` suffix. State/history/cache/lock default to `/var/lib/jealous-vault/usr/` so they do not collide with `default`.

## Secrets

- Config, passwords, env files and SSH keys: `0600`, owned by root.
- `/etc/jealous-vault` is excluded from backups by default.
- Keep the repository password separate from the repository: without it the data cannot be restored.
- Do not pass passwords on command lines; use files.
- `doctor` checks permissions and secret availability.
