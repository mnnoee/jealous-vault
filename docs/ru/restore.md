# Восстановление

[English](../restore.md) · Русский

Раздел нужен, чтобы вернуть данные и не перезаписать не тот каталог.

## Снапшоты

```sh
jealous-vault snapshots --config /etc/jealous-vault/config.toml
jealous-vault snapshots --all
```

`--all` показывает снапшоты всех хостов в общем репозитории. По умолчанию фильтр по `host` из
конфига.

Оценка размера без записи:

```sh
jealous-vault restore --estimate --snapshot latest
jealous-vault restore --estimate --json --snapshot latest
```

## Restore файлов

```sh
sudo jealous-vault restore \
  --snapshot latest \
  --target /srv/restore
```

Команда восстанавливает всю файловую систему снапшота в указанный каталог. Пути внутри target
сохраняют структуру: `/etc/hosts` окажется в `/srv/restore/etc/hosts`.

Отдельные пути:

```sh
sudo jealous-vault restore \
  --snapshot latest \
  --target /srv/restore \
  --include /etc \
  --include /var/www
```

Поведение при существующих файлах:

```sh
--overwrite always       # по умолчанию
--overwrite never
--overwrite if-changed
```

Для restore в `/` или другой системный каталог добавьте `--yes`:

```sh
sudo jealous-vault restore --snapshot latest --target / --yes
```

Проверка содержимого после restore:

```sh
sudo jealous-vault restore --snapshot latest --target /srv/restore --verify
```

Перед таким restore остановите сервисы и убедитесь, что у вас есть консольный доступ. Лучше
восстанавливать в отдельный каталог и затем переносить данные.

## Test-restore

Основной способ проверки:

```sh
jealous-vault test-restore --config /etc/jealous-vault/config.toml
```

Он:

- берёт latest снапшот;
- проверяет лимиты `max_size`/`min_free`;
- восстанавливает во временный каталог;
- проверяет содержимое;
- удаляет каталог, если не указан `--keep`;
- пишет state/history/audit.

Полезные флаги:

```sh
--snapshot ID
--level files|service
--max-size 5GB
--min-free 10GB
--target /srv/verify
--keep
--no-notify
```

`--level service` требует `verify.post_hook`; хук запускается после restore.

## Mount

Read-only просмотр снапшота через FUSE:

```sh
sudo jealous-vault mount --config /etc/jealous-vault/config.toml
```

Нужен `fusermount`/FUSE. Это удобно, когда нужно найти файл, не разворачивая весь снапшот. Точка
монтирования и поведение зависят от restic; после работы размонтируйте.

## Рецепты

Именованные наборы параметров в `[restore.recipes.*]`:

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

## Восстановление БД

Дампы лежат в снапшоте в каталоге `[backup.dumps].dir`, например:

```text
/var/lib/jealous-vault/dumps/dump-20261119-.../postgres-app.dump
/var/lib/jealous-vault/dumps/dump-20261119-.../mysql-app.sql
```

Сначала восстановите файлы:

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

Проверка:

```sh
psql -d app_restore -c 'SELECT count(*) FROM ...'
mariadb -N -e 'SELECT count(*) FROM app_restore....'
```

Для большой БД сравните не только count, но и контрольные суммы/`sum(id)`/ `sum(length(...))`.

## Restore VM/CT (Proxmox)

Нужны `[proxmox]` и снапшот с архивами `vzdump`:

```sh
sudo jealous-vault restore-vm --config /etc/jealous-vault/config.toml \
  100 latest
```

Первый аргумент - VMID, второй - ID снапшота или `latest`.

Флаги:

```sh
--target DIR      куда временно распаковать архив
--storage NAME    override proxmox.storage
--force           перезаписать существующую VM/CT
--keep            оставить временный каталог
```

`restore-vm` распаковывает архив из снапшота и вызывает `qmrestore`/ `pct restore`. Кластер,
миграции и Windows VM требуют отдельной проверки.

## Безопасность restore

- Всегда указывайте отдельный `--target`, если не делаете осознанный restore поверх системы.
- `--yes` - только для `/` и системных каталогов.
- Остановите БД/сервисы перед восстановлением их файлов.
- Проверьте свободное место: снапшот может быть больше, чем кажется.
- Не восстанавливайте поверх работающей БД; используйте дамп.
- Для unprivileged LXC device-node и SELinux xattr могут не создаться; это ограничение контейнера,
  не данных.

## Проверка на практике

Проверено на больших БД: 20 млн строк.

| СУБД       | Размер    | Дамп  | Upload           | Restore                                   | Сверка            |
| ---------- | --------- | ----- | ---------------- | ----------------------------------------- | ----------------- |
| PostgreSQL | ~22 ГБ    | 8m18s | 9m49s / 20.8 ГиБ | download 5m56s + verify, `pg_restore` 22m | count/sum совпали |
| MariaDB    | ~21.8 ГиБ | 5m04s | 9m30s / 19.9 ГиБ | stream load 27m57s                        | count/sum совпали |

Это не гарантия для сотен гигабайт, но показывает, что дамп/upload/restore работают на десятках
гигабайт и что данные не теряются.

## Типовые проблемы

- Если target не пустой, используйте `--overwrite` или отдельный каталог.
- Если restore в `/` отклонён, добавьте `--yes` после проверки.
- `mknod: operation not permitted`: unprivileged LXC не может создать device-node; исключите его или
  используйте привилегированный CT.
- `xattr: operation not permitted`: например, `security.selinux`; используйте `--exclude-xattr`,
  если он доступен, или привилегированный CT.
- `permission denied` при verify: файлы восстановлены с ограниченными правами; проверяйте от root
  или в каталоге с корректными правами.
- Не хватает места: проверьте `--estimate`, `min_free`, кэш restic.
