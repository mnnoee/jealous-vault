# Restore drill

[English](../DRILL.md) · Русский

Backup полезен только тогда, когда из него можно восстановиться. `test-restore` доказывает, что файлы возвращаются; `jealous-vault drill` идёт дальше: восстанавливает в scratch-каталог, проверяет сервис и базу, пишет структурированный отчёт, записывает его в history и может отправить алерт.

Запускайте drill перед релизом, после смены хранилища или по расписанию.

## Уровни

| Уровень   | Что делает                                                            | Что нужно                         |
| --------- | --------------------------------------------------------------------- | --------------------------------- |
| `files`   | восстанавливает последний снапшот в scratch-каталог и проверяет файлы | ничего                            |
| `service` | восстанавливает файлы и запускает проверку сервиса                    | `drill.service_hook`              |
| `db`      | восстанавливает каталог дампов и загружает его в scratch-базу         | `drill.db_script`, дампы включены |
| `full`    | files + service + database                                            | оба хука                          |

`files` использует `restic restore --verify`, поэтому проверяется содержимое файлов, а не только листинг каталога.

## Быстрый старт

```sh
sudo jealous-vault drill --level files
sudo jealous-vault drill --level service
sudo jealous-vault drill --level db --json
sudo jealous-vault drill --level full
```

Основные флаги:

```sh
--snapshot latest|ID
--target /var/lib/jealous-vault/drill/my-run
--keep
--yes
--no-notify
--json
```

Target - scratch-каталог. Команда не пишет в `/` и не трогает живую базу, если это явно не настроено.

## Конфигурация

```toml
[drill]
enabled = false                 # по умолчанию выключено
level = "files"                 # files, service, db или full
schedule = "Sun *-*-* 06:00:00"
# target = "/var/lib/jealous-vault/drill"
max_size = "10GB"
min_free = "20GB"
timeout = "2h"
keep = false

# Нужен для service и full. Запускается после restore.
service_hook = ""

# Нужен для db и full. Запускается после восстановления дампов.
db_script = ""
db_timeout = "30m"
```

`drill` **выключен по умолчанию**. Ручной `jealous-vault drill` работает без включения; `enabled = true` нужен только для таймера. Так функция не мешает тем, кому она не нужна.

`target` по умолчанию - каталог с timestamp внутри `/var/lib/jealous-vault/drill`. Target вне этого каталога может потребовать `--yes`, потому что это системный путь; правило то же, что у `restore`.

`max_size` и `min_free` превращают невозможный drill в `skipped`, а не заполняют диск.

## Хуки и скрипты

Хук - это shell-команда. Он запускается от root с переменными:

```text
JM_EVENT=drill
JM_PHASE=service|db
JM_STATUS=success
JM_HOST=<host из конфига>
JM_SNAPSHOT=<id снапшота>
JM_DRILL_LEVEL=files|service|db|full
JM_DRILL_TARGET=<scratch-каталог>
JM_DRILL_DUMP_DIR=<восстановленный каталог дампов, только db/full>
```

Код выхода решает результат: `0` - успех, остальное - ошибка. stdout и stderr попадают в JSON-отчёт. Не передавайте пароли в командной строке; читайте их из файла `0600`.

Для `db` и `full` восстановленный каталог дампов:

```text
$JM_DRILL_TARGET/<backup.dumps.dir без ведущего слэша>
```

Если `[backup.dumps]` выключен или каталога дампов нет в снапшоте, шаг `db` падает с понятной ошибкой.

Проверенный рецепт PostgreSQL - в [restore.md](restore.md#postgresql-scratch-database).

## Отчёт

Human-вывод:

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

Тот же запуск пишется в `history.jsonl` с `kind: "drill"`, уровнем и сводкой шагов. `jealous-vault status` показывает `last drill`.

## Расписание

При `enabled = true` команда `jealous-vault install` создаёт drill-таймер в systemd, запись в cron или weekly-задание OpenRC. Пример:

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

В systemd это `jealous-vault-drill.service` и `jealous-vault-drill.timer`. В cron запись попадает в сгенерированный cron-файл. В OpenRC задание идёт weekly через `/etc/periodic/weekly`. На sysvinit/runit `install` не планирует drill; добавьте cron или запускайте вручную.

Если нужен отдельный unit вне `install`:

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

Drill берёт тот же локальный lock, что `backup` и `check`, поэтому на одном хосте они не пересекаются. Если репозиторий заблокирован другим хостом, restic ждёт до 30 минут вместо мгновенной ошибки. Drill всё равно занимает канал; спокойное окно лучше, но идеально совмещать расписания не нужно.

## Уведомления

`drill` использует существующие каналы `[notify]` и отправляет событие `drill`. Ошибки и `skipped` отправляются по умолчанию. Успешные запуски - только при `notify_on_success = true`.

Пример ошибки:

```text
drill db failed
  step db: exit status 1: boom
  snapshot 0123456789abcdef...
```

## Если drill упал

1. Прочитайте JSON-отчёт; `steps[].output` содержит вывод хука или клиента БД.
2. Посмотрите `history --json` на `kind: drill` и уровень.
3. Запустите `doctor` и `test-restore`, чтобы отделить проблему репозитория от проблемы БД/сервиса.
4. Исправьте скрипт или scratch-базу и повторите тот же уровень.
5. Не удаляйте репозиторий или каталог дампов, чтобы «почистить» неудачный drill.

`skipped` - это не потеря данных: снапшот или свободное место вышли за лимиты. Увеличьте `max_size`/`min_free` или запустите на хосте с большим scratch-диском.

## Безопасность

- Восстанавливайте в scratch-каталог или scratch-базу, никогда поверх production.
- `--yes` нужен для whole-snapshot restore в системный каталог вроде `/srv`, `/home` или `/var`.
- `db_script` работает от root. Держите его `0700`, читайте пароли из файлов `0600` и не печатайте их.
- Не копируйте каталог дампов и пароли в отчёты и логи.

## Ограничения

- Встроенного драйвера PostgreSQL/MySQL нет: оператор даёт `db_script`. Это оставляет агент независимым от клиентов БД и позволяет закодировать точную проверку строк/сумм.
- `drill` не восстанавливает архивы Proxmox VM/CT; для этого есть `restore-vm`.
- На sysvinit/runit `install` не создаёт расписание drill; используйте cron или ручной запуск.
- Отчёт локальный. Хостингового evidence-хранилища в публичном продукте нет.
