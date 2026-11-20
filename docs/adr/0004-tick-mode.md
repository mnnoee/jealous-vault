# ADR-0004: `tick` for systems without systemd

English · [Русский](ru/0004-tick-mode.md)

- Status: accepted
- Date: 2026-11-20

## Context

systemd is the main target, but the project also supports FreeBSD, Artix/OpenRC, runit and Termux. Those systems either lack systemd timers or make per-task scheduling awkward. Writing a separate cron line for backup, check, verify and freshness multiplies configuration and drifts from the TOML schedule. On Termux, Android may delay or kill a scheduled process, so the agent cannot assume an exact start time.

## Decision

Provide `jealous-vault tick`, a single idempotent command that reads the config, compares the configured calendars with the last runs and executes whatever is due: backup, check, verify or freshness. `install --init cron|openrc|runit` writes one scheduler entry for `tick` where that is the natural model. On systemd, native timers remain the default.

## Consequences

- Minimal systems need one scheduler entry, not four.
- The same due-date logic works on FreeBSD, OpenRC, runit and Termux.
- Granularity is coarser: a 15-minute tick cannot start a job at an exact second. That is acceptable for backups and is documented.
- `tick` must be safe to run concurrently with a timer; the local lock and the due-date check handle that.
- Termux timezone and Doze behaviour remain platform caveats, documented in `docs/platforms-and-limits.md`.

## Alternatives considered

- **One cron line per task**: more configuration, easy to desynchronise from the TOML schedule.
- **Only systemd**: excludes FreeBSD, OpenRC, runit and Termux.
- **A small custom daemon**: contradicts ADR-0003 and adds a process to minimal systems.
