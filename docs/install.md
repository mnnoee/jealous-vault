# Install and upgrade

English · [Русский](ru/install.md)

This section covers installing the agent from a release and upgrading it without losing the config,
state or units.

## Release assets

A release contains:

| File                        | Purpose             |
| --------------------------- | ------------------- |
| `jealous-vault-linux-amd64` | binary for x86-64   |
| `jealous-vault-linux-arm64` | binary for arm64    |
| `THIRD-PARTY-LICENSES.md`   | dependency licenses |
| `SHA256SUMS`                | checksums           |
| `SHA256SUMS.sig`            | detached signature  |

The signature is verified with `build/allowed_signers` from the repository. For tag `v0.1.0`:

```sh
VERSION=v0.1.0
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

Expected result:

```text
jealous-vault-linux-amd64: OK
jealous-vault-linux-arm64: OK
THIRD-PARTY-LICENSES.md: OK
Good "jealous-vault-release" signature for release with ED25519 key ...
```

`curl -k` is acceptable only in a lab with a private CA; public releases must use normal TLS
verification.

## Install

```sh
sudo install -m 0755 jealous-vault-linux-amd64 /usr/local/bin/jealous-vault
sudo jealous-vault version
```

You need restic 0.18.x or 0.19.x:

```sh
restic version
```

`doctor` warns when the restic version is outside the tested range.

## Initial setup

```sh
sudo jealous-vault init \
  --config /etc/jealous-vault/config.toml \
  --host "$(hostname -s)" \
  --repo s3:https://s3.example.com/backup/host1 \
  --password-file /etc/jealous-vault/restic.pass \
  --paths /etc,/home,/var/www \
  --profile host
```

`init`:

- writes the config with mode `0600`;
- generates the repository password if the file does not exist;
- initializes the restic repository.

`--no-repo-init` writes only the config and password:

```sh
sudo jealous-vault repo init --config /etc/jealous-vault/config.toml
```

For S3/SFTP, put credentials in place first:

```ini
# /etc/jealous-vault/restic.env, mode 0600
AWS_ACCESS_KEY_ID=...
AWS_SECRET_ACCESS_KEY=...
AWS_DEFAULT_REGION=us-east-1
```

Then reference it:

```toml
[repository]
env_file = "/etc/jealous-vault/restic.env"
```

For SFTP:

```toml
[repository]
sftp_key_file = "/etc/jealous-vault/ssh/id_ed25519"
sftp_known_hosts = "/etc/jealous-vault/known_hosts"
```

The agent generates a private `ssh_config`; it does not use the system `~/.ssh/config`.

## Services and timers

```sh
sudo jealous-vault install --config /etc/jealous-vault/config.toml
sudo jealous-vault doctor --config /etc/jealous-vault/config.toml
sudo jealous-vault backup --config /etc/jealous-vault/config.toml
```

`install`:

- copies the binary to `/usr/local/bin/jealous-vault`;
- writes backup/check/freshness/verify units, plus copy/proxmox when enabled;
- enables and starts the timers;
- installs logrotate.

Check the result:

```sh
systemctl list-timers 'jealous-vault*'
systemctl list-units --failed | grep jealous-vault || true
```

For cron/OpenRC/runit:

```sh
sudo jealous-vault install --config /etc/jealous-vault/config.toml --init cron
```

Without systemd, `install` writes a scheduler entry and `tick` decides what is due. For a custom
loop:

```sh
jealous-vault tick --config /etc/jealous-vault/config.toml
```

Run `tick` every 15 minutes; it checks backup/check/freshness schedules itself.

## Multiple jobs

Each job has its own config, state, cache, password and units:

```sh
sudo jealous-vault init --config /etc/jealous-vault/usr.toml \
  --job usr --repo ... --paths /usr --profile custom
sudo jealous-vault install --config /etc/jealous-vault/usr.toml
```

Job names must match `[a-z0-9][a-z0-9_-]*`. Units get a suffix such as `-usr`, `-opt` or `-etc`.

## Upgrade

1. Download and verify the new assets.
2. Replace the binary.
3. Run `install` again.
4. Check `doctor`, `status` and `snapshots`.

```sh
sudo install -m 0755 jealous-vault-linux-amd64 /usr/local/bin/jealous-vault
sudo jealous-vault install --config /etc/jealous-vault/config.toml
sudo jealous-vault doctor --config /etc/jealous-vault/config.toml
sudo jealous-vault status --config /etc/jealous-vault/config.toml
```

Upgrade keeps:

- the config;
- state and history;
- old snapshots;
- unit and timer names.

A new binary reads an older config. The reverse is not guaranteed: an older binary may not know new
config keys. Do not downgrade without a copy of the config.

Tested: install and upgrade on a clean Debian 13 LXC; the config stayed the same, the number of
units stayed the same, old snapshots remained and a new backup succeeded.

## Uninstall

```sh
sudo jealous-vault uninstall --config /etc/jealous-vault/config.toml
```

Run it for every job. `uninstall` stops timers and removes units but **does not delete**:

- the repository and snapshots;
- the config;
- state/history;
- the binary.

Remove the config and local state manually when the service is no longer needed. Keep the repository
password somewhere else first: without it the data cannot be restored.

## Platforms

- systemd: the main option, `install` without flags.
- cron: `--init cron`, `tick` every 15 minutes.
- OpenRC: `--init openrc`.
- runit: `--init runit` or `tick` from a run script.
- FreeBSD: rc.d/cron; paths and units differ. Tested through `tick`.
- Termux: runsv/runit through `termux-services`, or `tick` + `termux-job-scheduler`; see
  [platforms-and-limits.md](platforms-and-limits.md).
- NixOS: works with manual setup; no package/module yet.
