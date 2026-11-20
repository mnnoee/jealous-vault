<p align="center">
  <img src="docs/assets/logo.svg" width="112" alt="JealousVault">
</p>

<h1 align="center">JealousVault</h1>

<p align="center">
  <b>Шифрованные резервные копии Linux с частичной поддержкой Proxmox. Предсказуемое восстановление.</b><br>
  Один бинарник, restic внутри, системные таймеры и понятные команды.
</p>

<p align="center">
  <a href="https://jealousmoon.com/projects/vault/">jealousmoon.com/projects/vault</a> ·
  <a href="https://jealousmoon.com/ru/proekty/vault/">/ru/proekty/vault</a>
</p>

<p align="center">
  <a href="README.md">English</a> · Русский
</p>

<p align="center">
  <img alt="License" src="https://img.shields.io/badge/license-Apache--2.0-blue">
  <img alt="Go" src="https://img.shields.io/badge/Go-1.27-00ADD8?logo=go&logoColor=white">
  <img alt="restic" src="https://img.shields.io/badge/restic-0.18%20%7C%200.19-4B5563">
  <img alt="Status" src="https://img.shields.io/badge/status-beta-F59E0B">
</p>

---

## С чего начать

| Задача                           | Документ                                                                |
| -------------------------------- | ----------------------------------------------------------------------- |
| Установка или обновление         | [docs/ru/install.md](docs/ru/install.md)                                |
| Восстановление файлов, БД или VM | [docs/ru/restore.md](docs/ru/restore.md)                                |
| Платформы и проверенные лимиты   | [docs/ru/platforms-and-limits.md](docs/ru/platforms-and-limits.md)      |
| Исправить сбой                   | [docs/ru/troubleshooting.md](docs/ru/troubleshooting.md)                |
| Стабильность и изменения         | [STABILITY.ru.md](STABILITY.ru.md) · [CHANGELOG.ru.md](CHANGELOG.ru.md) |
| Границы MVP и расширений         | [docs/ru/mvp.md](docs/ru/mvp.md)                                        |

## Зачем

`restic` отлично шифрует и дедуплицирует данные, но оставляет оператору много ручной работы:
расписание, ретенция, проверки, алерты, дампы БД, восстановление и повторяемые юниты. JealousVault
собирает это в один агент с конфигом и командами, которые можно положить в runbook.

- Репозиторий restic шифрует данные; пароль хранится отдельно.
- S3, SFTP, локальный диск и REST: без собственного сервера и вендор-лока.
- PostgreSQL и MariaDB/MySQL дампятся перед backup, а не копируются во время работы.
- `check`, `test-restore`, `freshness`, `doctor`, `history` проверяют целостность, восстановление и
  свежесть, а не только пишут снапшот.
- `restore` возвращает файлы и каталоги, включая дампы БД для `pg_restore`/`mariadb`;
  `restore-vm` частично покрывает VM/CT на Proxmox.
- Несколько job на хосте: у каждого свой конфиг, state, cache, пароль и юниты.

## Быстрый старт

```sh
VERSION=0.1.1
BASE="https://jealousmoon.com/projects/vault/releases/download/$VERSION"

curl -fLO "$BASE/jealous-vault-linux-amd64"
curl -fLO "$BASE/SHA256SUMS"
curl -fLO "$BASE/SHA256SUMS.sig"
sha256sum -c --ignore-missing SHA256SUMS

sudo install -m 0755 jealous-vault-linux-amd64 /usr/local/bin/jealous-vault

sudo jealous-vault init \
  --repo s3:https://s3.example.com/backup/host1 \
  --paths /etc,/home,/var/www \
  --profile host

sudo jealous-vault install
sudo jealous-vault doctor
sudo jealous-vault backup
```

Проверка подписи и требования - в [install.md](docs/ru/install.md).

## Что внутри

| Возможность    | Как это выглядит                                                       |
| -------------- | ---------------------------------------------------------------------- |
| Backup         | `jealous-vault backup`, systemd/cron/`tick`, ретенция и prune          |
| Проверка       | `check`, `test-restore`, `doctor`, `freshness`                         |
| Восстановление | `restore`, `restore-vm`, `mount`, named recipes                        |
| Базы данных    | `pg_dump`, `mysqldump`/`mariadb-dump`, отдельный job только для дампов |
| Алерты         | Telegram (включая forum topics), email, webhooks, healthchecks         |
| Несколько job  | свои конфиг, state, cache, пароль и юниты на каждый job                |
| Ручной выход   | `export-restic-env`, `recovery-kit`, документированные команды restic  |
| Proxmox        | частичная поддержка: `vzdump`, `restore-vm`                            |

## Платформы

| Платформа                                | Статус                                       |
| ---------------------------------------- | -------------------------------------------- |
| Debian / Ubuntu / Fedora / Arch / Alpine | поддерживается, systemd                      |
| Artix / OpenRC / runit / cron            | поддерживается через `tick`                  |
| FreeBSD                                  | поддерживается через `tick`/cron             |
| Termux (Android)                         | вручную: runsv/runit или JobScheduler        |
| NixOS                                    | работает вручную; пакета/модуля пока нет     |
| Proxmox VE 8/9                           | частичная поддержка: `vzdump` и `restore-vm` |

## Документация

| Раздел                                                     | О чём                                                |
| ---------------------------------------------------------- | ---------------------------------------------------- |
| [Установка и обновление](docs/ru/install.md)               | релизные ассеты, подпись, `init`, `install`, upgrade |
| [Конфигурация](docs/ru/configuration.md)                   | все секции TOML и примеры                            |
| [Эксплуатация](docs/ru/operations.md)                      | таймеры, check/verify, retention, multi-job, аудит   |
| [Восстановление](docs/ru/restore.md)                       | файлы, БД, VM/CT, проверка                           |
| [Платформы и ограничения](docs/ru/platforms-and-limits.md) | Termux, NixOS, Proxmox, большие БД, copy             |
| [Диагностика](docs/ru/troubleshooting.md)                  | локи, S3/SFTP, дампы, restore, state                 |

## Безопасность

- Репозиторий шифруется restic; пароль хранится отдельно, режим `0600`.
- Конфиги и секреты лежат в `/etc/jealous-vault` и исключены из backup.
- Агент не слушает входящие порты; алерты только исходящие.
- Уязвимости: [SECURITY.md](SECURITY.ru.md), контакт `vault@jealousmoon.com` (резерв:
  `me@mnnoee.ru`).
- Машиночитаемый контакт: [`.well-known/security.txt`](.well-known/security.txt).

## Статус

Проект в beta. На живых хостах, S3/SFTP, PostgreSQL и MariaDB ядро работает. Проверено
восстановление всех репозиториев в отдельный LXC. Большие БД проверены на 20 ГиБ; сотни ГиБ и
кластер Proxmox - впереди. NixOS работает при ручной установке, пакета пока нет.

Что beta покрывает и не покрывает и чего ещё требует MVP: [docs/ru/mvp.md](docs/ru/mvp.md).

## Экосистема

JealousVault - открытый проект, а не продукт за шлагбаумом. Его экосистема открыта для независимых
разработчиков, сторонних расширений и альтернативных реализаций. Коммерческие расширения
приветствуются, а права на свою работу остаются у их авторов. См.
[ECOSYSTEM.ru.md](ECOSYSTEM.ru.md).

## Участие

- [Как участвовать](CONTRIBUTING.ru.md)
- [Мейнтейнеры](MAINTAINERS.ru.md)
- [Экосистема](ECOSYSTEM.ru.md)
- [CLA для коммерческих расширений](CLA.ru.md)
- [Кодекс поведения](CODE_OF_CONDUCT.ru.md)
- [Политика безопасности](SECURITY.ru.md)
- [Контракт стабильности](STABILITY.ru.md) · [Changelog](CHANGELOG.ru.md)
- [MVP и границы](docs/ru/mvp.md) · [Архитектурные решения](docs/adr/ru/README.md)
- Документация: [docs/README.md](docs/ru/README.md)

## Лицензия

Apache License 2.0. См. [LICENSE](LICENSE) и [NOTICE](NOTICE).
