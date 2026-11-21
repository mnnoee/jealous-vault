# Конфигурация

[English](../configuration.md) · Русский

Раздел нужен, чтобы править конфиг и не сломать backup или restore.

Конфиг - TOML. По умолчанию `/etc/jealous-vault/config.toml`, режим `0600`. Секреты не пишутся в конфиг: только пути к файлам.

`jealous-vault init` создаёт рабочий конфиг с комментариями. Ниже - карта секций и правила, которые важно знать при ручном редактировании.

## Минимальный конфиг

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

## Верхний уровень

| Ключ            | По умолчанию                         | Значение                                |
| --------------- | ------------------------------------ | --------------------------------------- |
| `host`          | hostname                             | имя хоста в снапшотах                   |
| `job`           | `default`                            | имя job, `[a-z0-9][a-z0-9_-]*`          |
| `state_file`    | `/var/lib/jealous-vault/state.json`  | последний результат                     |
| `history_file`  | рядом с state                        | JSONL-история                           |
| `lock_file`     | рядом с state                        | локальный lock от параллельных запусков |
| `alerts_file`   | рядом с state                        | окна дедупликации алертов               |
| `audit_file`    | `/var/log/jealous-vault/audit.jsonl` | журнал действий                         |
| `restic_binary` | `restic`                             | путь/имя restic                         |

Для job `db`/`usr`/`opt` агент автоматически раскладывает значения по умолчанию в `/var/lib/jealous-vault/<job>/`, чтобы jobs не пересекались. Можно задать явно.

## `[repository]`

```toml
[repository]
url = "s3:https://s3.example.com/backup/host1"
password_file = "/etc/jealous-vault/restic.pass"
env_file = "/etc/jealous-vault/restic.env"
cache_dir = "/var/cache/jealous-vault"
```

| Ключ               | Значение                                             |
| ------------------ | ---------------------------------------------------- |
| `url`              | restic URL: `s3:`, `sftp:`, `rest:`, `local:`        |
| `password_file`    | пароль репозитория, `0600`                           |
| `env_file`         | KEY=VALUE для S3/облака, `0600`                      |
| `cache_dir`        | индекс restic                                        |
| `sftp_key_file`    | приватный ключ SFTP, `0600`                          |
| `sftp_known_hosts` | known_hosts; обязателен, если следующий флаг не true |
| `sftp_accept_new_host_key` | доверять неизвестному ключу хоста при первом подключении (по умолчанию `false`) |
| `sftp_config`      | изолированный ssh_config; генерируется автоматически |

Пример S3-файла:

```ini
AWS_ACCESS_KEY_ID=...
AWS_SECRET_ACCESS_KEY=...
AWS_DEFAULT_REGION=us-east-1
```

Для S3 с самоподписанным сертификатом используйте доверенный CA; `restic` не отключает проверку TLS по умолчанию.

## `[backup]`

| Ключ              | По умолчанию | Значение                                  |
| ----------------- | ------------ | ----------------------------------------- |
| `profile`         | `host`       | `host`, `vm-dumps`, `custom`              |
| `paths`           | профиль      | абсолютные пути                           |
| `excludes`        | профиль      | шаблоны исключений                        |
| `tags`            | `[]`         | теги снапшота                             |
| `pre_hooks`       | `[]`         | команды до backup; ошибка отменяет backup |
| `post_hooks`      | `[]`         | команды после; ошибка = warning           |
| `one_file_system` | `true`       | не переходить на другие ФС                |
| `timeout`         | `6h`         | общий таймаут backup с ретенцией          |
| `attempt_timeout` | `2h`         | таймаут одной попытки restic              |
| `attempts`        | `3`          | число попыток                             |
| `retry_delay`     | `1m`         | пауза между попытками                     |
| `limit_upload`    | `0`          | КиБ/с, 0 = без лимита                     |
| `compression`     | `auto`       | `auto`, `off`, `max`                      |
| `pack_size`       | `0`          | МиБ, 0 = restic default                   |

Профили:

- `host` - `/etc`, `/var/www`, системные каталоги; на Proxmox ещё `/etc/pve`;
- `vm-dumps` - только `/var/lib/vz/dump`;
- `custom` - ничего не добавляет, всё задаётся в `paths`.

На Proxmox `/etc/pve` - отдельный FUSE-моунт; при `one_file_system=true` его нужно указывать в `paths` явно, иначе содержимое не попадёт в снапшот.

## `[backup.dumps]`

Согласованные дампы БД перед restic:

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

- Каталог дампов добавляется в restic автоматически.
- После успешного backup остаётся `keep` последних каталогов.
- Ошибка дампа отменяет backup: в репозиторий не уйдёт неполный набор.
- PostgreSQL: `pg_dump --format=custom`; восстанавливается `pg_restore`.
- MySQL/MariaDB: `mysqldump --single-transaction --routines --events --triggers`; восстанавливается `mysql`/`mariadb` из SQL.
- `--single-transaction` гарантирует согласованность InnoDB. Для MyISAM нужны другие меры; по умолчанию таблицы MyISAM не блокируются.
- MariaDB 12.3 предупреждает, что `mysqldump` устарел; можно указать `binary = "mariadb-dump"`.

Гранты для MySQL/MariaDB (минимум):

```sql
CREATE USER 'backup'@'localhost' IDENTIFIED BY '<password>';
GRANT SHOW CREATE ROUTINE ON *.* TO 'backup'@'localhost';
GRANT SELECT, SHOW VIEW, TRIGGER, EVENT, LOCK TABLES
  ON `app`.* TO 'backup'@'localhost';
```

Если клиент ходит по TCP, создайте такого же пользователя для `127.0.0.1`. Рекомендуется `host = "localhost"` (unix-сокет) для локальной БД.

Агент передаёт пароль MySQL через временный `--defaults-extra-file` и изолирует `HOME`, чтобы `/root/.my.cnf` не перебил пароль; глобальные option-файлы при этом работают.

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

`forget` запускается после успешного backup. `prune=true` физически удаляет ненужные блоки; это медленнее, но освобождает место. `keep_within` - например `"1y5m7d"`. `keep_tag` сохраняет снапшоты с указанными тегами.

## `[schedule]`

```toml
[schedule]
on_calendar = "*-*-* 03:30:00"
randomized_delay = "1h"
```

`on_calendar` - systemd calendar syntax. `randomized_delay` добавляет джиттер, чтобы много хостов не били в репозиторий одновременно. Для нескольких job разносите время.

Таймеры ставятся `install`:

- backup - `on_calendar`;
- check - еженедельно;
- verify - еженедельно (`verify.schedule`);
- freshness - несколько раз в день (`freshness.schedule`);
- copy/proxmox - если включены.

## `[freshness]`

```toml
[freshness]
max_age = "25h"
respect_backup_timer = true
schedule = "*-*-* 00,06,12,18:15:00"
```

Проверяет возраст последнего успешного backup. Если backup-таймер выключен, `respect_backup_timer=true` сообщает о проблеме таймера, а не о «старом» снапшоте. Для Termux/systemd-less ставьте `false`.

## `[check]`

```toml
[check]
read_data_subset = "10%"
timeout = "2h"
```

`restic check`; `read_data_subset` читает часть данных (например `5%`), чтобы ловить bit rot без полного чтения репозитория.

## `[verify]`

```toml
[verify]
enabled = true
level = "files"          # files или service
max_size = "10GB"
min_free = "20GB"
timeout = "2h"
schedule = "Sun *-*-* 05:00:00"
post_hook = ""
```

`test-restore` восстанавливает последний снапшот в отдельный каталог и проверяет файлы. `max_size`/`min_free` пропускают слишком большой снапшот или при нехватке места. `level="service"` требует `post_hook` и проверяет сервис после restore.

Если проверенный снапшот потом удалит retention, `doctor` предупредит: «snapshot ... is no longer in the repository; run test-restore again». `status` показывает локальную запись последнего verify; для диагностики используйте `doctor`.

## `[drill]`

```toml
[drill]
enabled = false
level = "files"          # files, service, db или full
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

`drill` восстанавливает снапшот в scratch-каталог и может дополнительно проверить сервис и scratch-базу. По умолчанию выключен; ручной `jealous-vault drill` работает без включения. `enabled = true` заставляет `install` создать drill-таймер в systemd, cron или OpenRC.

`service_hook` нужен для `service` и `full`; `db_script` - для `db` и `full`. Оба являются shell-командами и получают переменные `JM_DRILL_*`. Полный контракт, формат отчёта и примеры PostgreSQL/MySQL - в [DRILL.md](DRILL.md).

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
mode = "local"           # local или smtp
from = "backup@example.com"
to = ["ops@example.com"]
host = "smtp.example.com"
port = 587
username = ""
password_file = "/etc/jealous-vault/smtp.pass"
security = "starttls"    # tls, starttls, none
timeout = "15s"
```

- Telegram: `chat_id` или `chat_id:thread_id` для forum topic.
- HTML-разметка: host/time/duration жирным, error/warning в `<pre>`, class/code/detail в `<code>`; значения экранируются.
- `webhooks` - JSON POST; `ping_url` - healthchecks.io-стиль.
- `notify_on_success=false` - только проблемы. Дедупликация подавляет одинаковые алерты в окне; `0` отключает.
- Секреты - только в файлах `0600`; email-пароль через `password_file` или `password_env`.

## `[copy]`

Второй репозиторий для 3-2-1:

```toml
[copy]
enabled = true
schedule = "*-*-* 05:00:00"
repository = "s3:https://other.example.com/backup/host1"
password_file = "/etc/jealous-vault/copy.pass"
env_file = "/etc/jealous-vault/copy.env"
new_only = true
```

Пароль копии должен отличаться от основного. `copy` запускается отдельным таймером и копирует снапшоты через `restic copy`.

Ограничение: restic использует один набор backend-креденшелов на процесс. Копирование S3→S3 между разными провайдерами с разными ключами напрямую может не работать. Варианты: локальный источник на хосте с репозиторием или raw-зеркало репозитория через rclone. Для SFTP/локального источника `copy` работает штатно.

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

`vzdump` складывает архивы в `dump_dir`, затем restic бэкапит каталог. `restore-vm` возвращает VM/CT из снапшота. Нужны PVE 8/9 и `qmrestore`/ `pct restore`. Кластер, Windows VM и большие диски проверены не полностью.

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

Рецепт - именованный набор параметров:

```sh
jealous-vault restore --recipe postgres --snapshot latest --yes
```

Хуки позволяют остановить/запустить сервис вокруг restore.

## Несколько job

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

Установка:

```sh
sudo jealous-vault install --config /etc/jealous-vault/usr.toml
```

Юниты получат суффикс `-usr`. State/history/cache/lock по умолчанию будут в `/var/lib/jealous-vault/usr/`, чтобы не пересекаться с `default`.

## Секреты

- Конфиг, пароли, env-файлы, SSH-ключи - `0600`, владелец root.
- `/etc/jealous-vault` исключён из backup по умолчанию.
- Пароль репозитория храните отдельно от репозитория: без него данные не восстановить.
- Не передавайте пароли в аргументах команд: используйте файлы.
- `doctor` проверяет права и доступность секретов.
