# Установка и обновление

[English](../install.md) · Русский

Раздел нужен, чтобы поставить агент из релиза и обновить его без потери конфига, state и юнитов.

## Релизные ассеты

В релизе лежат:

| Файл                                | Назначение                         |
| ----------------------------------- | ---------------------------------- |
| `jealous-vault-linux-amd64`         | бинарник для x86-64                |
| `jealous-vault-linux-arm64`         | бинарник для arm64                 |
| `jealous-vault_<version>_amd64.deb` | пакет Debian/Ubuntu, x86-64        |
| `jealous-vault_<version>_arm64.deb` | пакет Debian/Ubuntu, arm64         |
| `THIRD-PARTY-LICENSES.md`           | лицензии зависимостей              |
| `SHA256SUMS`                        | контрольные суммы                  |
| `SHA256SUMS.sig`                    | отсоединённая подпись `SHA256SUMS` |

Подпись проверяется ключом из `build/allowed_signers` в репозитории. Пример для тега `0.1.1`:

```sh
VERSION=0.1.1
BASE="https://jealousmoon.com/projects/vault/releases/download/$VERSION"

mkdir jv-release && cd jv-release
for f in jealous-vault-linux-amd64 jealous-vault-linux-arm64 \
         THIRD-PARTY-LICENSES.md SHA256SUMS SHA256SUMS.sig; do
  curl -fLO "$BASE/$f"
done

curl -fLo allowed_signers \
  "https://jealousmoon.com/projects/vault/allowed_signers"

sha256sum -c SHA256SUMS
ssh-keygen -Y verify -f allowed_signers -I release \
  -n jealous-vault-release -s SHA256SUMS.sig < SHA256SUMS
```

Ожидаемый результат:

```text
jealous-vault-linux-amd64: OK
jealous-vault-linux-arm64: OK
THIRD-PARTY-LICENSES.md: OK
Good "jealous-vault-release" signature for release with ED25519 key ...
```

`curl -k` допустим только в лаборатории с приватным CA; в публичной поставке TLS должен проверяться нормально.

## Установка

```sh
sudo install -m 0755 jealous-vault-linux-amd64 /usr/local/bin/jealous-vault
sudo jealous-vault version
```

Нужен restic 0.18.x/0.19.x:

```sh
restic version
```

`doctor` предупредит, если версия restic вне проверенных серий.

### Пакет Debian/Ubuntu

Forgejo release workflow собирает `jealous-vault_<version>_amd64.deb` и `jealous-vault_<version>_arm64.deb`. Пакет **не** зависит от restic: restic указан в `Suggests`, поэтому ставьте его отдельно. Пакет также не создаёт конфиг и не включает таймеры.

```sh
sudo apt-get install restic
sudo dpkg -i jealous-vault_0.1.1_amd64.deb
sudo apt-get install -f

sudo jealous-vault init \
  --repo s3:https://s3.example.com/backup/host1 \
  --paths /etc,/home,/var/www \
  --profile host
sudo jealous-vault install --prefix /usr/bin
sudo jealous-vault doctor
```

`--prefix /usr/bin` важен, потому что бинарник пакета лежит в `/usr/bin/jealous-vault`. Детали сборки пакета и удаление - в [packaging/deb/README.ru.md](../../packaging/deb/README.ru.md).

## Первичная настройка

```sh
sudo jealous-vault init \
  --config /etc/jealous-vault/config.toml \
  --host "$(hostname -s)" \
  --repo s3:https://s3.example.com/backup/host1 \
  --password-file /etc/jealous-vault/restic.pass \
  --paths /etc,/home,/var/www \
  --profile host
```

Что делает `init`:

- пишет конфиг с режимом `0600`;
- генерирует пароль репозитория, если файла ещё нет;
- инициализирует restic-репозиторий.

`--no-repo-init` позволяет только записать конфиг и пароль, а репозиторий инициализировать позже:

```sh
sudo jealous-vault repo init --config /etc/jealous-vault/config.toml
```

Для S3/SFTP заранее положите креды:

```ini
# /etc/jealous-vault/restic.env, режим 0600
AWS_ACCESS_KEY_ID=...
AWS_SECRET_ACCESS_KEY=...
AWS_DEFAULT_REGION=us-east-1
```

и добавьте в конфиг:

```toml
[repository]
env_file = "/etc/jealous-vault/restic.env"
```

Для SFTP:

```toml
[repository]
sftp_key_file = "/etc/jealous-vault/ssh/id_ed25519"
sftp_known_hosts = "/etc/jealous-vault/known_hosts"
```

Агент генерирует отдельный `ssh_config`, системный `~/.ssh/config` не используется.

## Сервисы и таймеры

```sh
sudo jealous-vault install --config /etc/jealous-vault/config.toml
sudo jealous-vault doctor --config /etc/jealous-vault/config.toml
sudo jealous-vault backup --config /etc/jealous-vault/config.toml
```

`install`:

- копирует бинарник в `/usr/local/bin/jealous-vault`;
- пишет юниты backup/check/freshness/verify и, если включено, copy/proxmox;
- включает и запускает таймеры;
- добавляет logrotate.

Проверка:

```sh
systemctl list-timers 'jealous-vault*'
systemctl list-units --failed | grep jealous-vault || true
```

Для cron/OpenRC/runit:

```sh
sudo jealous-vault install --config /etc/jealous-vault/config.toml --init cron
```

Без systemd `install` создаёт запись планировщика, а `tick` решает, что пора запускать. Для собственного цикла:

```sh
jealous-vault tick --config /etc/jealous-vault/config.toml
```

`tick` можно вызывать каждые 15 минут; он сам проверит backup/check/ freshness по расписанию.

## Несколько job

Каждый job - отдельный конфиг, state, cache, пароль и набор юнитов:

```sh
sudo jealous-vault init --config /etc/jealous-vault/usr.toml \
  --job usr --repo ... --paths /usr --profile custom
sudo jealous-vault install --config /etc/jealous-vault/usr.toml
```

Имя job должно соответствовать `[a-z0-9][a-z0-9_-]*`. Юниты получают суффикс `-usr`, `-opt`, `-etc` и т.п.

## Обновление

1. Скачать и проверить новые ассеты.
2. Заменить бинарник.
3. Повторно выполнить `install`.
4. Проверить `doctor`, `status`, `snapshots`.

```sh
sudo install -m 0755 jealous-vault-linux-amd64 /usr/local/bin/jealous-vault
sudo jealous-vault install --config /etc/jealous-vault/config.toml
sudo jealous-vault doctor --config /etc/jealous-vault/config.toml
sudo jealous-vault status --config /etc/jealous-vault/config.toml
```

Обновление сохраняет:

- конфиг;
- state и history;
- старые снапшоты;
- имена юнитов и таймеров.

Новый бинарник читает конфиг старой версии. Обратное не гарантируется: старый бинарник может не знать новых ключей конфига. Поэтому после обновления не откатывайтесь на старую версию без бэкапа конфига.

Проверено: установка и обновление на чистом Debian 13 LXC; конфиг не изменился, число юнитов сохранилось, старый снапшот остался, новый backup прошёл.

## Удаление

```sh
sudo jealous-vault uninstall --config /etc/jealous-vault/config.toml
```

Для каждого job - отдельно. `uninstall` останавливает таймеры и удаляет юниты, но **не удаляет**:

- репозиторий и снапшоты;
- конфиг;
- state/history;
- бинарник.

Если сервис больше не нужен, конфиг и локальное состояние удаляются вручную. Перед удалением пароля репозитория убедитесь, что он есть в другом месте: без него данные не восстановить.

## Платформы

- systemd: основной вариант, `install` без флагов.
- cron: `--init cron`, `tick` каждые 15 минут.
- OpenRC: `--init openrc`.
- runit: `--init runit` или `tick` из run-скрипта.
- FreeBSD: rc.d/cron, пути и юниты отличаются; проверено через `tick`.
- Termux: есть runsv/runit (`termux-services`) и `tick` + `termux-job-scheduler`, см. `platforms-and-limits.md`.
- NixOS: работает вручную; пакета/модуля пока нет.
