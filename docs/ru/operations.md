# Эксплуатация

[English](../operations.md) · Русский

Раздел нужен, чтобы понимать, что запускается по расписанию и что делать при сбое.

## Ежедневные команды

| Команда                           | Что делает                                             |
| --------------------------------- | ------------------------------------------------------ |
| `jealous-vault status`            | последний backup/check/verify, свежесть, lock          |
| `jealous-vault history`           | последние запуски из JSONL                             |
| `jealous-vault snapshots`         | снапшоты в репозитории                                 |
| `jealous-vault doctor`            | конфиг, restic, репозиторий, пути, таймеры, алерты     |
| `jealous-vault backup`            | запуск backup вручную                                  |
| `jealous-vault backup --dry-run`  | показать, что попадёт в backup                         |
| `jealous-vault check`             | проверка целостности репозитория                       |
| `jealous-vault test-restore`      | тестовый restore с проверкой                           |
| `jealous-vault freshness`         | проверка возраста backup                               |
| `jealous-vault repo unlock`       | удалить stale-lock restic                              |
| `jealous-vault export-restic-env` | вывести RESTIC\_\* переменные и готовую команду restic |

Все команды принимают `--config`, большинство - `--json` и `--verbose`.

`status` возвращает ненулевой код, если последний backup failed или старше `--max-age` (по умолчанию
25h). Это удобно для мониторинга.

## Ручной выход через restic

`export-restic-env` (алиас `print-restic-command`) выводит переменные репозитория и команды restic
для текущего job:

```sh
sudo jealous-vault export-restic-env --config /etc/jealous-vault/config.toml
sudo jealous-vault export-restic-env --config /etc/jealous-vault/config.toml --json
```

В выводе:

- `RESTIC_REPOSITORY`, `RESTIC_PASSWORD_FILE` и `RESTIC_CACHE_DIR`;
- строка `source` для `repository.env_file`, чтобы credentials S3 и других бэкендов загрузились,
  но не печатались;
- команда `restic backup` с host, excludes, tags и путями из конфига;
- `restic forget` с политикой ретенции и примеры `restic snapshots`/`restic restore`.

Печатаются пути к секретам, а не их содержимое. Команда не требует установленного restic и не
обращается к репозиторию. Используйте её, когда агент недоступен, перед обновлением или как
аварийный путь для оператора, которому передали хост.

## Жизненный цикл backup

1. Pre-hooks (`backup.pre_hooks`). Ошибка отменяет backup.
2. Дампы БД (`[backup.dumps]`), если включены.
3. `restic backup` с путями и исключениями.
4. `restic forget`/`prune` по `[retention]`.
5. Очистка старых каталогов дампов.
6. Post-hooks (`backup.post_hooks`); ошибка - warning.
7. Запись state/history/audit.
8. Алерты (при ошибке; при успехе - если `notify_on_success`).

Backup идемпотентен: повторный запуск создаёт новый снапшот, дедупликация restic не даёт данным
разрастись.

## Таймеры

`install` создаёт:

| Юнит                            | Когда                              |
| ------------------------------- | ---------------------------------- |
| `jealous-vault-backup.timer`    | `schedule.on_calendar` + джиттер   |
| `jealous-vault-check.timer`     | еженедельно                        |
| `jealous-vault-verify.timer`    | `verify.schedule`                  |
| `jealous-vault-freshness.timer` | `freshness.schedule`               |
| `jealous-vault-copy.timer`      | `copy.schedule`, если copy включён |
| `jealous-vault-proxmox.timer`   | `proxmox.schedule`, если включён   |

Проверка:

```sh
systemctl list-timers 'jealous-vault*'
systemctl status jealous-vault-backup.timer
systemctl list-units --failed | grep jealous-vault || true
```

Если таймер не сработал:

```sh
systemctl cat jealous-vault-backup.timer
journalctl -u jealous-vault-backup.service --since today
systemctl is-enabled jealous-vault-backup.timer
```

`RandomizedDelaySec` - это нормально: запуск может сместиться на величину `randomized_delay`.

## Check

```sh
jealous-vault check --config /etc/jealous-vault/config.toml
jealous-vault check --read-data-subset 10%
```

`check` проверяет структуру репозитория и снапшоты. `read_data_subset` дополнительно читает часть
pack-файлов; процент задаётся в конфиге или флагом.

Таймер запускает check, результат пишется в state/history. При ошибке приходит алерт.

## Verify / test-restore

```sh
jealous-vault test-restore --config /etc/jealous-vault/config.toml
jealous-vault test-restore --snapshot latest --level files --keep
```

`test-restore`:

- выбирает снапшот (по умолчанию latest для этого host);
- проверяет `max_size` и свободное место;
- восстанавливает в отдельный каталог;
- проверяет содержимое (`--verify` restic);
- удаляет временный каталог, если не указан `--keep`;
- пишет state/history/audit и шлёт алерт при ошибке.

Если проверенный снапшот позже удалит retention, `doctor` предупредит. Это нормально: запись
`last_verify` хранит ID, а снапшота уже нет. Запустите `test-restore` ещё раз, чтобы обновить
проверку.

Для проверки сервиса:

```toml
[verify]
level = "service"
post_hook = "systemctl restart myapp && curl -fsS http://127.0.0.1:8080/health"
```

## Freshness

`freshness` проверяет, что последний успешный backup не старше `max_age`. Если backup-таймер
выключен, при `respect_backup_timer=true` сообщает о проблеме таймера, а не о старом снапшоте. Для
Termux/без systemd ставьте `respect_backup_timer=false`.

## Ретенция

Политика `keep_last/daily/weekly/monthly` применяется после успешного backup. `prune=true`
освобождает место, но переписывает репозиторий и может быть долгим.

Примеры:

```toml
# Минимум места
[retention]
keep_last = 2
keep_daily = 3
keep_weekly = 2
keep_monthly = 1
prune = true

# Долгая история
[retention]
keep_last = 7
keep_daily = 14
keep_weekly = 8
keep_monthly = 12
prune = true
```

Перед изменением политики проверьте, что нужные снапшоты не будут удалены:

```sh
jealous-vault snapshots --all
jealous-vault repo stats
```

## Multi-job

Один хост может иметь несколько независимых job. У каждого:

- свой конфиг `/etc/jealous-vault/<job>.toml`;
- свой state `/var/lib/jealous-vault/<job>/`;
- свой cache и lock;
- свой пароль/репозиторий;
- свои юниты с суффиксом `-<job>`.

Пример: `/usr`, `/opt`, `/etc` в разных репозиториях. Плюс - независимые retention и алерты; минус -
нет дедупликации между репозиториями.

Устанавливайте и удаляйте job по конфигу:

```sh
jealous-vault install --config /etc/jealous-vault/usr.toml
jealous-vault uninstall --config /etc/jealous-vault/usr.toml
```

## Проверки doctor

`doctor` - главный инструмент поддержки. Он проверяет конфиг и права на секреты, restic, доступ к
репозиторию, пути, таймеры, формат state/history и профиль backup. Он предупреждает, когда:

- state или history старше формата версии 1;
- unit таймера установлен, но выключен, или включён, но не активен;
- `backup.profile` не `host`, `vm-dumps` или `custom`.

Он падает, когда конфиг, пароль, env или ключ отсутствует или читается группой/остальными. Это
сделано намеренно: агент отказывается работать с выставленным секретом.

## Блокировки

Локальный lock (`lock_file`) не даёт двум процессам одного job работать одновременно. Если процесс
убит, lock может остаться; `backup`/`check` умеют очищать stale-lock. Для restic используйте:

```sh
jealous-vault repo unlock --config /etc/jealous-vault/config.toml
```

Не удаляйте lock-файлы restic вручную, пока не убедились, что процесс не работает. Для
SFTP-репозитория lock-файлы должны принадлежать пользователю репозитория; root-овые файлы ломают
следующий backup.

## Audit и history

- `audit_file` - JSONL ключевых действий: backup, restore, check, verify, repo init/unlock,
  uninstall.
- `history_file` - JSONL запусков с длительностью, статусом, snapshot ID.
- `alerts_file` - окна дедупликации.

Это локальные журналы; они не заменяют внешний мониторинг, но позволяют понять, что и когда
запускалось.

## Логи

- systemd: `journalctl -u jealous-vault-backup.service`.
- CLI: `--verbose` для отладки.
- logrotate ставится в `/etc/logrotate.d/jealous-vault`.
- При ошибке сначала `doctor`, затем `status`/`history`, затем журнал юнита.

## Регулярное обслуживание

Раз в месяц/квартал:

```sh
jealous-vault doctor
jealous-vault check --read-data-subset 5%
jealous-vault test-restore
jealous-vault snapshots --all
```

Раз в год:

- проверить, что пароль репозитория и `allowed_signers`/релизные ключи доступны вне хоста;
- проверить restore на чистой машине;
- проверить ротацию S3/SFTP-ключей;
- пересмотреть retention.

## Мониторинг

Минимальный набор:

- `status`/`freshness` по расписанию с ненулевым кодом при проблеме;
- алерты Telegram/email/webhook;
- `doctor` после изменений конфига;
- `systemctl list-units --failed`.

Метрик Prometheus и JSON-логов пока нет; `--json` есть у большинства команд.
