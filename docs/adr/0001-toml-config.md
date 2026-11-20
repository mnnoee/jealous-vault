# ADR-0001: TOML for configuration

English · [Русский](ru/0001-toml-config.md)

- Status: accepted
- Date: 2026-11-20

## Context

The agent is configured by an operator on a server, often over SSH, and the file is reviewed in a diff. It needs comments, nested sections, arrays and predictable escaping. A wrong setting can silently stop a backup, so unknown keys must be rejected instead of ignored. There is no UI and no central configuration service.

## Decision

Configuration is TOML, parsed with `github.com/BurntSushi/toml`. The file lives at `/etc/jealous-vault/config.toml`, with one file per job. Decoding is strict: any key that does not map to a field is an error. Durations and sizes stay human-readable strings in the file and are parsed and validated at load time.

## Consequences

- The config is readable, commentable and diff-friendly.
- A typo fails loudly instead of disabling a backup silently.
- Adding a key is easy; renaming or removing one requires a deprecation window and, eventually, a migration command.
- There is no schema version yet, so an older binary rejects a newer config. That is acceptable while the project is pre-1.0, but `config migrate` is part of the MVP.

## Alternatives considered

- **YAML**: indentation errors and implicit typing are too easy to get wrong in a file that controls backups.
- **JSON**: no comments, noisy diffs, and the operator cannot leave notes next to a path.
- **Flags and environment variables only**: unmanageable for dozens of settings, hooks and multiple jobs.
- **A central config service**: adds a server and a network dependency to a host-local tool.
