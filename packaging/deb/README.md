# Debian/Ubuntu package

English · [Русский](README.ru.md)

The package installs `/usr/bin/jealous-vault` and its documentation. It does **not** depend on `restic`: restic is a `Suggests`, so the package manager will not install it automatically. The package also does not create `/etc/jealous-vault` and does not enable any service; that stays an explicit operator action.

## Build

```sh
./build/deb.sh 0.1.1 amd64 dist/jealous-vault-linux-amd64
./build/deb.sh 0.1.1 arm64 dist/jealous-vault-linux-arm64
```

The script uses `dpkg-deb` and needs the matching Linux binary in `dist/`. It runs in the Forgejo CI after the normal build.

## Install

```sh
sudo apt-get install restic
sudo dpkg -i dist/jealous-vault_0.1.1_amd64.deb
sudo apt-get install -f
```

If `jealous-vault` is already installed from a binary, the package replaces `/usr/bin/jealous-vault` only; a copy in `/usr/local/bin` is left alone.

Then create a config and units:

```sh
sudo jealous-vault init --repo s3:https://s3.example.com/backup/host1 \
  --paths /etc,/home,/var/www --profile host
sudo jealous-vault install --prefix /usr/bin
sudo jealous-vault doctor
```

`--prefix /usr/bin` matters: the package binary is `/usr/bin/jealous-vault`, and `install` writes units that point at the binary it is given. Without the flag, `install` would copy a second binary to `/usr/local/bin`.

## Upgrade

```sh
sudo dpkg -i dist/jealous-vault_0.1.1_amd64.deb
```

The config, state, repository password and installed units are not touched. Check the new version with `jealous-vault version`, then run `jealous-vault doctor`.

## Remove

Remove the units and timers for every job before removing the package:

```sh
sudo jealous-vault uninstall --prefix /usr/bin --config /etc/jealous-vault/config.toml
sudo apt-get remove jealous-vault
```

The package does not delete `/etc/jealous-vault` or repository data. Remove them manually only after you have verified that a restore still works.

## What the package does not do

- It does not install or upgrade restic.
- It does not create a config, a password or a repository.
- It does not enable timers.
- It does not remove your config or backups on uninstall.
