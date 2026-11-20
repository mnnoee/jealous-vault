# JealousVault

Шифрованные резервные копии Linux-хостов с частичной поддержкой Proxmox через restic. Один статический бинарник, системные таймеры, проверка целостности, восстановление предсказуемыми командами.

Этот индекс нужен, чтобы быстро найти раздел под задачу: установку, конфиг, эксплуатацию, restore или диагностику.

Сайт: [jealousmoon.com/projects/vault](https://jealousmoon.com/projects/vault/) · [русская версия](https://jealousmoon.com/ru/proekty/vault/).

[English](../README.md) · Русский

## Что умеет

- restic-репозитории: S3, SFTP, локальный путь, REST.
- Полный бэкап хоста или отдельный набор путей; профили `host`, `vm-dumps`, `custom`.
- Согласованные дампы PostgreSQL и MariaDB/MySQL перед restic.
- Ретенция, `check`, тестовый restore (`test-restore`), проверка свежести.
- Алерты: Telegram (включая forum topics), email, JSON-webhooks, healthchecks.io-совместимый ping.
- Несколько независимых job на одном хосте: свои конфиг, state, cache, пароль, репозиторий и юниты.
- Proxmox: частичная поддержка `vzdump` и `restore-vm` для VM/CT-архивов.
- Восстановление файлов, каталогов, БД и целых VM/CT.

## Быстрый старт

```sh
VERSION=0.1.0
BASE="https://jealousmoon.com/projects/vault/releases/download/$VERSION"

curl -fLO "$BASE/jealous-vault-linux-amd64"
curl -fLO "$BASE/SHA256SUMS"
curl -fLO "$BASE/SHA256SUMS.sig"
sha256sum -c SHA256SUMS
# проверка подписи - см. install.md

sudo install -m 0755 jealous-vault-linux-amd64 /usr/local/bin/jealous-vault

sudo jealous-vault init \
  --repo s3:https://s3.example.com/backup/host1 \
  --paths /etc,/home,/var/www \
  --profile host

sudo jealous-vault install
sudo jealous-vault doctor
sudo jealous-vault backup
```

`init` создаёт `/etc/jealous-vault/config.toml` и файл пароля репозитория. `install` ставит сервисы и таймеры. `doctor` проверяет окружение и репозиторий.

## Требования

- Linux amd64/arm64 (статический бинарник) или FreeBSD.
- restic **0.18.x или 0.19.x** в `PATH`.
- root для чтения системных путей и установки юнитов.
- systemd, OpenRC, cron, runit или собственный `tick`-цикл.
- Исходящий доступ к репозиторию; входящие порты не нужны.
- Корректные CA-сертификаты для HTTPS/S3.

## Документация

| Файл                                               | О чём                                                              |
| -------------------------------------------------- | ------------------------------------------------------------------ |
| [install.md](install.md)                           | установка с релизных ассетов, проверка подписи, upgrade, удаление  |
| [configuration.md](configuration.md)               | все секции конфига и примеры                                       |
| [operations.md](operations.md)                     | ежедневная эксплуатация: таймеры, check, verify, retention, алерты |
| [restore.md](restore.md)                           | восстановление файлов, БД, VM/CT и проверка                        |
| [DRILL.md](DRILL.md)                               | restore drill: files, service, db, full; отчёты, расписание        |
| [platforms-and-limits.md](platforms-and-limits.md) | платформы, Termux, NixOS, Proxmox, ограничения                     |
| [troubleshooting.md](troubleshooting.md)           | типовые ошибки и диагностика                                       |

## Статус

Beta. На живых Linux-хостах, S3, SFTP, PostgreSQL и MariaDB ядро работает. Проверено восстановление всех репозиториев в отдельный LXC.

Проверено отдельно:

- установка и обновление с релизных ассетов: конфиг и юниты сохраняются, старые снапшоты остаются;
- PostgreSQL и MariaDB по 20 млн строк / ~20 ГиБ: дамп, upload, restore из репозитория, сверка `count/sum` - совпало;
- 6 реальных хостов и 20 LXC одновременно в одно S3-хранилище: по ~1.16 ГиБ уникальных данных на контейнер, всего ~22 ГиБ; сеть, блокировки, multi-job, Termux.

До массового запуска не хватает:

- проверки БД на сотнях ГиБ;
- проверки Proxmox-кластера, Windows VM и больших `vzdump`;
- пакетов, сайта и публичного TLS-URL.

## Документы проекта

| Файл                                                                 | О чём                                                       |
| -------------------------------------------------------------------- | ----------------------------------------------------------- |
| [mvp.md](mvp.md)                                                     | критерии MVP, публичный продукт, расширения                 |
| [../../STABILITY.ru.md](../../STABILITY.ru.md)                       | публичный API, политика deprecation, breaking changes       |
| [../../CHANGELOG.ru.md](../../CHANGELOG.ru.md)                       | история релизов                                             |
| [../../MAINTAINERS.ru.md](../../MAINTAINERS.ru.md)                   | мейнтейнеры, сроки ответа, bus factor                       |
| [../../CONTRIBUTING.ru.md](../../CONTRIBUTING.ru.md)                 | DCO, проверки, ожидания по review                           |
| [../../ECOSYSTEM.ru.md](../../ECOSYSTEM.ru.md)                       | плагины, коммерческие расширения, альтернативные реализации |
| [../../CLA.ru.md](../../CLA.ru.md)                                   | соглашение о вкладе в коммерческое расширение               |
| [../../packaging/deb/README.ru.md](../../packaging/deb/README.ru.md) | сборка и установка пакета Debian/Ubuntu                     |
| [../adr/ru/README.md](../adr/ru/README.md)                           | архитектурные решения                                       |

Документация на английском: [../README.md](../README.md).
