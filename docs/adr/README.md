# Architecture decision records

English · [Русский](ru/README.md)

An ADR records a decision that is expensive to reverse, together with the context and the
consequences. It is not a tutorial and not a promise: it explains why the code looks the way it does
so that the next change starts from the same reasoning.

These records are maintained in English and Russian. English is the primary version; the Russian
translation is in [ru/](ru/README.md). They are engineering notes for the maintainer, not user
documentation; user documentation is bilingual as well.

| ADR                                       | Decision                                   | Status   |
| ----------------------------------------- | ------------------------------------------ | -------- |
| [0001](0001-toml-config.md)               | TOML for configuration                     | accepted |
| [0002](0002-separate-run-state.md)        | Run state in separate local files          | accepted |
| [0003](0003-no-daemon.md)                 | No server and no long-running daemon       | accepted |
| [0004](0004-tick-mode.md)                 | `tick` for systems without systemd         | accepted |
| [0005](0005-public-product-extensions.md) | Public product with independent extensions | accepted |

## When to write one

Write an ADR when a change:

- alters the storage format, config format or public CLI in a way that is hard to undo;
- adds or removes a process, service or dependency with a security or operational impact;
- changes the licence boundary or the public/commercial split;
- chooses one design where a reasonable alternative existed.

Do not write an ADR for a bug fix, a new flag or a documentation change.

## Template

```markdown
# ADR-000N: Title

- Status: proposed | accepted | superseded by ADR-000M
- Date: YYYY-MM-DD

## Context

What forces are at play, and what constraint makes this decision necessary?

## Decision

What was chosen, in one or two paragraphs.

## Consequences

What becomes easier, what becomes harder, and what is now explicitly out of scope.

## Alternatives considered

What else was plausible, and why was it rejected?
```
