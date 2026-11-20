# ADR-0002: Run state in separate local files

English · [Русский](ru/0002-separate-run-state.md)

- Status: accepted
- Date: 2026-11-20

## Context

The agent needs to remember the last run, the last successful backup, check and verify results, a
short history, the alert deduplication windows and a local lock. The repository belongs to restic
and must stay a standard restic repository, readable without JealousVault. A database would add a
service, a schema and another failure mode to a tool that already runs as root.

## Decision

Keep bookkeeping in small local files under `/var/lib/jealous-vault/` and the audit journal under
`/var/log/jealous-vault/`:

- `state.json` for the last results, written atomically with mode `0600`;
- `history.jsonl` and `audit.jsonl` as append-only JSONL;
- `backup.lock` for `flock` and holder diagnostics;
- `alerts.json` for deduplication windows.

A corrupt state file is moved aside as `state.json.corrupt-<timestamp>` and treated as empty, so a
damaged bookkeeping file cannot stop future backups.

## Consequences

- No database, no daemon and no extra port.
- The repository remains portable: losing the local state does not lose backup data.
- State is per host and per job; there is no cross-host query. Fleet status is a separate, future
  product.
- Upgrades may rewrite `state.json`; it is owned by the agent and not a user database.
- History rotation and retention are simple file operations, not a retention policy in a DB.

## Alternatives considered

- **SQLite**: a dependency and a schema without a real query need; still a local file to corrupt.
- **Store state in the restic repository**: mixes agent metadata with backup data and breaks the
  promise that the repository stays a plain restic repository.
- **PostgreSQL/MySQL**: another service to install, secure and monitor on every host.
- **No state at all**: no freshness check, no deduplication and no `status`/`history`.
