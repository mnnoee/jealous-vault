# ADR-0003: No server and no long-running daemon

English · [Русский](ru/0003-no-daemon.md)

- Status: accepted
- Date: 2026-11-20

## Context

A backup agent runs as root and reads system paths. A long-running daemon would add an inbound socket, a request parser, authentication, privilege separation and a supervisor to every host. The operating system already provides a scheduler, logging, restart policy and resource limits. restic already provides the repository format, encryption and deduplication.

## Decision

JealousVault is a one-shot CLI. It is started by systemd, OpenRC, cron, runit or `tick`, does its work and exits. There is no server, no listening port and no agent protocol. Alerts are outbound only. The storage engine is restic, invoked as a child process, so the repository stays compatible with plain restic.

## Consequences

- Small attack surface: no network service to authenticate or patch.
- Easy debugging: the same command works interactively and from a timer.
- No central fleet view, no push from a server and no remote trigger beyond SSH or the scheduler. Fleet management is a future paid product in a separate repository.
- The scheduler owns concurrency, logging and restart behaviour; the agent only handles its own lock and retries.
- A compromised root account on the host can read the repository password. That is inherent to a host-local backup agent and is documented in the security model.

## Alternatives considered

- **Daemon with an HTTP API**: adds a remote attack surface and a service to secure on every host.
- **Central server pushing jobs**: requires connectivity, credentials and a server; fails the single-host, offline-friendly goal.
- **Custom repository format**: loses restic compatibility, encryption maturity and the manual escape hatch.
