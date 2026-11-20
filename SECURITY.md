# Security

Website: [jealousmoon.com/projects/vault](https://jealousmoon.com/projects/vault/) · [Russian version](https://jealousmoon.com/ru/proekty/vault/).

English · [Русский](SECURITY.ru.md)

## Supported versions

| Version                | Support           |
| ---------------------- | ----------------- |
| latest stable release  | yes               |
| previous minor release | critical fixes    |
| prerelease/test builds | no, test use only |

Check the installed version:

```sh
jealous-vault version
```

## Reporting a vulnerability

**Do not open a public issue.** Email **vault@jealousmoon.com** with the subject `[SECURITY] JealousVault`, or use a private security advisory in Forgejo if it is available. Personal fallback: **me@mnnoee.ru**.

Include:

- JealousVault version, OS and restic version;
- a config without secrets (remove passwords, keys and tokens);
- reproduction steps and expected/actual behaviour;
- possible impact: data read, write, RCE, secret leak, denial of service;
- a contact address for the reply.

Do not send real passwords, private keys or backup contents.

## What happens next

- I acknowledge receipt within **3 business days**;
- I give an initial assessment and a fix plan within **7 business days**;
- I coordinate disclosure after the fix is released;
- I credit the reporter in the release notes unless they prefer otherwise.

There is no bug bounty.

## Scope

In scope:

- the agent binary and its CLI;
- config parsing, restore, dump and retention;
- systemd/OpenRC/runit/cron units;
- secret handling and leaks into backups or logs;
- S3/SFTP/local repository code in the agent;
- alerts and error handling.

Out of scope:

- vulnerabilities in restic, the OS, systemd or the S3 provider;
- a compromised root account or physical access to the host;
- weak passwords or keys chosen by the operator;
- social engineering;
- denial of service caused by the operator's own data volume without an agent bug.

## Security model

- restic encrypts the repository; without the password the data cannot be restored.
- Passwords and keys live in `0600` files outside the repository; the config is also `0600`.
- `/etc/jealous-vault` is excluded from backups by default.
- The agent runs as root because it reads system paths; run it only on a trusted host.
- There are no inbound ports: the agent is only a restic and alerting client.
- Secrets are kept out of process arguments where possible; the MySQL password goes through a temporary `--defaults-extra-file`.
- Alerts contain hosts, paths, error classes and stderr fragments; make sure the outbound channel is trusted.

## Hardening

Recommended:

- use a dedicated repository password, never reuse it;
- give S3/SFTP keys the minimum permissions;
- run `doctor` after changes;
- schedule `check` and `test-restore`;
- restrict access to `/etc/jealous-vault` and the logs;
- keep a copy of the repository password outside the host.

## Safe harbor

I will not pursue researchers who act in good faith, stay within their own data and test systems, do not harm availability and do not expose other people's secrets. If you are unsure, write before starting the test.
