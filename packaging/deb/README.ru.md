# Пакет Debian/Ubuntu

[English](README.md) · Русский

Пакет ставит `/usr/bin/jealous-vault` и документацию. Он **не** зависит от `restic`: restic указан в `Suggests`, поэтому пакетный менеджер не установит его автоматически. Пакет также не создаёт `/etc/jealous-vault` и не включает сервисы: это остаётся явным действием оператора.

## Сборка

```sh
./build/deb.sh 0.1.2 amd64 dist/jealous-vault-linux-amd64
./build/deb.sh 0.1.2 arm64 dist/jealous-vault-linux-arm64
```

Скрипт использует `dpkg-deb` и требует соответствующий Linux-бинарник в `dist/`. В Forgejo CI он запускается после обычной сборки.

## Установка

```sh
sudo apt-get install restic
sudo dpkg -i dist/jealous-vault_0.1.2_amd64.deb
sudo apt-get install -f
```

Если `jealous-vault` уже установлен из бинарника, пакет заменит только `/usr/bin/jealous-vault`; копия в `/usr/local/bin` останется нетронутой.

Затем создайте конфиг и юниты:

```sh
sudo jealous-vault init --repo s3:https://s3.example.com/backup/host1 \
  --paths /etc,/home,/var/www --profile host
sudo jealous-vault install --prefix /usr/bin
sudo jealous-vault doctor
```

`--prefix /usr/bin` важен: бинарник пакета лежит в `/usr/bin/jealous-vault`, а `install` пишет юниты на тот бинарник, который ему указали. Без флага `install` скопирует второй бинарник в `/usr/local/bin`.

## Обновление

```sh
sudo dpkg -i dist/jealous-vault_0.1.2_amd64.deb
```

Конфиг, state, пароль репозитория и установленные юниты не затрагиваются. Проверьте версию через `jealous-vault version`, затем запустите `jealous-vault doctor`.

## Удаление

Перед удалением пакета уберите юниты и таймеры для каждого job:

```sh
sudo jealous-vault uninstall --prefix /usr/bin --config /etc/jealous-vault/config.toml
sudo apt-get remove jealous-vault
```

Пакет не удаляет `/etc/jealous-vault` и данные репозитория. Удаляйте их вручную только после того, как убедились, что restore работает.

## Чего пакет не делает

- Не устанавливает и не обновляет restic.
- Не создаёт конфиг, пароль или репозиторий.
- Не включает таймеры.
- Не удаляет конфиг или бэкапы при удалении.
