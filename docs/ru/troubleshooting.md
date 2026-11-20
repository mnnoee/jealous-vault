# Диагностика

[English](../troubleshooting.md) · Русский

Раздел нужен, чтобы быстро найти причину сбоя и не сделать хуже.

## С чего начать

```sh
jealous-vault doctor --config /etc/jealous-vault/config.toml
jealous-vault status --config /etc/jealous-vault/config.toml
jealous-vault history --limit 20 --config /etc/jealous-vault/config.toml
journalctl -u jealous-vault-backup.service --since today
```

`doctor` проверяет конфиг, права, restic, репозиторий, пути, таймеры, алерты и S3. Он не меняет данные.

## Репозиторий заблокирован

```text
unable to create lock in backend: ... permission denied
repository is already locked exclusively by PID ...
```

1. Проверьте, не идёт ли backup/check: `ps -eo pid,args | grep -E 'restic|jealous-vault'`.
2. Если процесс жив - дождитесь.
3. Если процесс убит: `jealous-vault repo unlock --config ...`.

Для SFTP-репозитория lock-файлы создаются пользователем репозитория. Root-овые файлы в `locks/` (например, после ручного `restic` под root) ломают следующий backup: пользователь SFTP не может их прочитать. Удалите stale-lock и проверьте владельца:

```sh
ls -la /path/to/repo/locks
find /path/to/repo -user root -ls
```

Не запускайте локальные restic-команды против SFTP-репозитория под root; используйте пользователя-владельца репозитория.

## S3

| Симптом                 | Что проверить                     |
| ----------------------- | --------------------------------- |
| `InvalidAccessKeyId`    | ключи, проект/бакет, `env_file`   |
| `NoSuchBucket`          | имя бакета, регион, endpoint      |
| DNS/timeout             | резолвинг, сеть, firewall         |
| TLS error               | CA, `https`, корпоративный прокси |
| `SignatureDoesNotMatch` | регион, path-style, секрет        |

`env_file` - только KEY=VALUE, режим `0600`; лишние ключи могут быть отклонены. Для S3-совместимых хранилищ часто нужен path-style; restic определяет его по endpoint.

Проверка доступа:

```sh
jealous-vault doctor
jealous-vault repo stats
```

## SFTP

- Приватный ключ - `0600`, не group/other.
- `known_hosts` должен содержать ключ хоста.
- `sftp_key_file`/`sftp_known_hosts` работают только с `sftp:` URL.
- Агент генерирует изолированный `ssh_config`; системный `~/.ssh/config` не используется.
- В `authorized_keys` на сервере могут быть ограничения `from=`, `command="internal-sftp"`; клиент должен ходить с разрешённого IP.
- `Permission denied` при lock - см. раздел про lock и владельца файлов.

## Дампы БД

### `Access denied for user ...`

- Проверьте `user`, `host`, `port`, `password_file`.
- Для `host = "127.0.0.1"` нужен грант `'user'@'127.0.0.1'`; для `host = "localhost"` - `'user'@'localhost'` (unix-сокет).
- `--routines` требует `SHOW CREATE ROUTINE` (MariaDB) или эквивалент.
- `--events`/`--triggers` требуют соответствующих прав.

### `/root/.my.cnf` перебивает пароль

MySQL-клиент читает `~/.my.cnf` после `--defaults-extra-file`. Если там другой пароль, дамп уйдёт с ним. Агент изолирует `HOME`, но при ручном запуске учитывайте это. Проверка:

```sh
sudo -n grep -E '^(user|password|host)' /root/.my.cnf
```

### `mysqldump` deprecated

MariaDB 12.3 предупреждает и предлагает `mariadb-dump`. Дамп работает, но можно указать:

```toml
[backup.dumps.mysql]
binary = "mariadb-dump"
```

### MyISAM

`--single-transaction` не даёт согласованности для MyISAM. Для таких таблиц нужны блокировки или остановка записи.

### Долгий дамп

- Проверьте `pg_size_pretty(pg_total_relation_size(...))` / `information_schema.tables`.
- Убедитесь, что диск для `backup.dumps.dir` не заполнен.
- Большие БД: 20 ГиБ проверены; сотни ГиБ требуют отдельного планирования.
- Не ставьте `attempts` слишком большим для многоминутного дампа.

## Таймеры

```sh
systemctl list-timers 'jealous-vault*'
systemctl cat jealous-vault-backup.timer
systemctl is-enabled jealous-vault-backup.timer
systemctl reset-failed jealous-vault-backup.service
```

- `RandomizedDelaySec` смещает запуск; это нормально.
- После ручного изменения юнитов: `systemctl daemon-reload`.
- Если таймер выключен, `freshness` при `respect_backup_timer=true` сообщит о проблеме таймера.
- Без systemd используйте `tick`/cron; проверьте, что `tick` действительно вызывается (лог/`systemd`/cron).

## Обновление и релизы

- Скачивайте бинарник, `SHA256SUMS`, `SHA256SUMS.sig`.
- Проверяйте `sha256sum -c` и `ssh-keygen -Y verify`.
- В лаборатории с приватным CA `curl -k` допустим только вместе с проверкой подписи; в публичной поставке TLS должен проверяться.
- Старый бинарник может не понимать новые ключи конфига. Обновляйтесь вперёд; перед откатом сохраните конфиг.
- `install` после обновления перегенерирует юниты, но не трогает state и снапшоты.

## Restore

- `refusing to restore ... without --yes` - target `/` или системный каталог; проверьте и добавьте `--yes`.
- `mknod ... operation not permitted` - unprivileged LXC; исключите device-node.
- `xattr ... operation not permitted` - SELinux/xattr; исключите xattr или используйте привилегированный CT.
- `permission denied` при `--verify` - restore выполнён с ограниченными правами; проверяйте от root.
- Не хватает места - `--estimate`, `verify.min_free`, очистка кэша.
- Для БД не восстанавливайте файлы поверх работающей СУБД; используйте дамп.

## State и last_verify

- При повреждении `state.json` агент перемещает его в `state.json.corrupt-*` и продолжает с пустого состояния. Файл остаётся для разбора.
- `last_verify` хранит ID снапшота. Если retention его удалил, `status` продолжит показывать старый verify (это локальная запись), а `doctor` выдаст warning `snapshot ... is no longer in the repository`. Запустите `test-restore`, чтобы обновить проверку.
- `doctor` не ходит в репозиторий без необходимости? Он проверяет доступ и список снапшотов; для диагностики это нормально.

## Диск и кэш

- Дампы: `backup.dumps.dir`, `keep`.
- Кэш restic: `repository.cache_dir`.
- Временный target verify: рядом со state.
- `restic prune` может занимать много места временно.
- Проверяйте `df -h` перед большим restore/дампом.

## Audit и history

- `audit_file` - что делали: backup/restore/check/verify/repo.
- `history_file` - статусы и длительности.
- Если history пуст после обновления со старого релиза, проверьте, умеет ли старый бинарник команду `history`; новые релизы пишут JSONL.

## Termux и NixOS

- Termux: см. `platforms-and-limits.md`; проверьте `tick.log`, время/UTC, JobScheduler, доступ к `/storage/emulated/0`.
- NixOS: работает вручную; готового пакета/модуля пока нет и в планах его нет.
