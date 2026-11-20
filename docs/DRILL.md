# Restore drill

English · [Русский](ru/DRILL.md)

A backup is only useful if it can be restored. `test-restore` proves that files come back; `jealous-vault drill` goes further: it restores into a scratch directory, checks a service and a database, writes a structured report, records it in history and can send an alert.

Use a drill before a release, after a storage change, or on a schedule.

## Levels

| Level     | What it does                                                             | Requires                         |
| --------- | ------------------------------------------------------------------------ | -------------------------------- |
| `files`   | restores the latest snapshot into a scratch directory and verifies files | nothing                          |
| `service` | restores files, then runs a service check                                | `drill.service_hook`             |
| `db`      | restores the dump directory, then loads it into a scratch database       | `drill.db_script`, dumps enabled |
| `full`    | files + service + database                                               | both hooks                       |

`files` uses `restic restore --verify`, so file contents are checked against the repository, not just the directory listing.

## Quick start

```sh
sudo jealous-vault drill --level files
sudo jealous-vault drill --level service
sudo jealous-vault drill --level db --json
sudo jealous-vault drill --level full
```

Common flags:

```sh
--snapshot latest|ID
--target /var/lib/jealous-vault/drill/my-run
--keep
--yes
--no-notify
--json
```

The target is a scratch directory. The command never writes into `/` or over a live database unless you explicitly configure it to.

## Configuration

```toml
[drill]
enabled = false                 # off by default
level = "files"                 # files, service, db or full
schedule = "Sun *-*-* 06:00:00"
# target = "/var/lib/jealous-vault/drill"
max_size = "10GB"
min_free = "20GB"
timeout = "2h"
keep = false

# Required for service and full. Runs after the restore.
service_hook = ""

# Required for db and full. Runs after the dump files are restored.
db_script = ""
db_timeout = "30m"
```

`drill` is **disabled by default**. A manual `jealous-vault drill` works without enabling it; `enabled = true` is needed only for the scheduled timer. This keeps the feature out of the way for people who do not want it.

`target` defaults to a timestamped directory under `/var/lib/jealous-vault/drill`. A target outside that directory may need `--yes` because it is a system path; the same rule as `restore` applies.

`max_size` and `min_free` turn an impossible drill into `skipped` instead of filling the disk.

## Hooks and scripts

A hook is a shell command. It runs as root with these variables:

```text
JM_EVENT=drill
JM_PHASE=service|db
JM_STATUS=success
JM_HOST=<host from config>
JM_SNAPSHOT=<snapshot id>
JM_DRILL_LEVEL=files|service|db|full
JM_DRILL_TARGET=<scratch directory>
JM_DRILL_DUMP_DIR=<restored dump directory, db/full only>
```

The exit code decides the result: `0` is success, anything else is a failure. stdout and stderr are captured in the JSON report. Do not put passwords on the command line; read them from a `0600` file.

For `db` and `full`, the restored dump directory is:

```text
$JM_DRILL_TARGET/<backup.dumps.dir without the leading slash>
```

If `[backup.dumps]` is disabled or the dump directory is not in the snapshot, the `db` step fails with a clear message.

A tested PostgreSQL recipe is in [restore.md](restore.md#postgresql-scratch-database).

## Report

Human output:

```text
drill full success
  snapshot: 0123456789abcdef...
  target:   /var/lib/jealous-vault/drill/20261121-060000
  [ok] restore: snapshot restored (2.1s)
  [ok] service: service hook passed (0.0s)
  [ok] db: database check passed (0.2s)
```

`--json`:

```json
{
  "level": "full",
  "snapshot": "0123456789abcdef...",
  "target": "/var/lib/jealous-vault/drill/20261121-060000",
  "status": "success",
  "duration_seconds": 4.2,
  "steps": [
    { "name": "restore", "status": "success", "duration_seconds": 2.1 },
    { "name": "service", "status": "success", "duration_seconds": 0.0 },
    {
      "name": "db",
      "status": "success",
      "output": "rows=1000",
      "duration_seconds": 0.2
    }
  ]
}
```

The same run is written to `history.jsonl` with `kind: "drill"`, the level and the step summary. `jealous-vault status` shows `last drill`.

## Schedule

With `enabled = true`, `jealous-vault install` creates the drill timer on systemd, a cron entry, or an OpenRC weekly periodic job. Example:

```toml
[drill]
enabled = true
level = "full"
schedule = "Sun *-*-* 06:00:00"
service_hook = "/etc/jealous-vault/drill-service.sh"
db_script = "/etc/jealous-vault/drill-postgres.sh"
```

```sh
sudo jealous-vault install --config /etc/jealous-vault/config.toml
```

On systemd the units are `jealous-vault-drill.service` and `jealous-vault-drill.timer`. On cron the entry is written into the generated cron file. On OpenRC the job runs weekly through `/etc/periodic/weekly`. On sysvinit/runit `install` does not schedule a drill; add cron or run it manually.

If you need a unit separate from `install`, use:

```ini
# /etc/systemd/system/jealous-vault-drill.service
[Unit]
Description=JealousVault restore drill
Wants=network-online.target
After=network-online.target

[Service]
Type=oneshot
ExecStart=/usr/local/bin/jealous-vault drill --config /etc/jealous-vault/config.toml
Nice=15
IOSchedulingClass=best-effort
IOSchedulingPriority=7
```

```ini
# /etc/systemd/system/jealous-vault-drill.timer
[Unit]
Description=JealousVault restore drill timer

[Timer]
OnCalendar=Sun *-*-* 06:00:00
Persistent=true
AccuracySec=1m
Unit=jealous-vault-drill.service

[Install]
WantedBy=timers.target
```

```sh
sudo systemctl daemon-reload
sudo systemctl enable --now jealous-vault-drill.timer
```

Cron:

```cron
17 6 * * 0 root /usr/local/bin/jealous-vault drill --config /etc/jealous-vault/config.toml
```

Drill takes the same local lock as `backup` and `check`, so on one host they cannot overlap. If another host holds a repository lock, restic waits up to 30 minutes instead of failing at once. A drill still uses bandwidth; a quiet window is better, but schedules do not have to be exact.

## Notifications

`drill` uses the existing `[notify]` channels and sends an event named `drill`. Failures and `skipped` runs are sent by default. Successful runs are sent only when `notify_on_success = true`.

Example failure:

```text
drill db failed
  step db: exit status 1: boom
  snapshot 0123456789abcdef...
```

## When a drill fails

1. Read the JSON report; `steps[].output` contains the hook or database client output.
2. Check `history --json` for `kind: drill` and the level.
3. Run `doctor` and `test-restore` to separate a repository problem from a DB/service problem.
4. Fix the script or the scratch database, then rerun the same level.
5. Do not delete the repository or the dump directory to "clean up" a failed drill.

A `skipped` drill is not a data failure: the snapshot or free space exceeded the configured limits. Increase `max_size`/`min_free` or run on a larger scratch host.

## Safety

- Restore into a scratch directory or a scratch database, never over production.
- `--yes` is required for a whole-snapshot restore into a system directory such as `/srv`, `/home` or `/var`.
- A `db_script` runs as root. Keep it `0700`, read passwords from `0600` files, and do not print them.
- Keep the dump directory and its password material out of the drill target when you copy reports or logs.

## Limitations

- There is no built-in PostgreSQL/MySQL driver: the operator supplies `db_script`. This keeps the agent independent of database clients and lets the script encode the exact row/sum check.
- `drill` does not restore Proxmox VM/CT archives; use `restore-vm` for that.
- On sysvinit/runit `install` does not create a drill schedule; use cron or run it manually.
- The report is local. There is no hosted evidence store in the public product.
