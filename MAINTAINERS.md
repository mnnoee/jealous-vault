# Maintainers

English · [Русский](MAINTAINERS.ru.md)

## Current maintainer

JealousVault is maintained by **mnnoee**.

- Project: <https://jealousmoon.com/projects/vault/>
- General contact: `me@mnnoee.ru`
- Security reports: `vault@jealousmoon.com` (fallback `me@mnnoee.ru`), see [SECURITY.md](SECURITY.md)
- Development remote: Forgejo; release remote: GitHub

This is a **solo, best-effort project**. There is no company behind it, no on-call rotation and no
guaranteed response time.

## Response expectations

These are targets, not a service-level agreement.

| Channel                            | Target                                              |
| ---------------------------------- | --------------------------------------------------- |
| Security report                    | acknowledge in 3 business days, assess in 7         |
| Bug report with a reproduction     | triage within 10 business days                      |
| Pull request                       | first review within 14 business days                |
| Feature request                    | no SLA; may be closed as out of scope               |
| Question about a broken production | best effort; paid support is planned, not available |

Security timelines are defined in [SECURITY.md](SECURITY.md) and take precedence for reports sent
there.

## Bus factor

The bus factor is **1**.

If the maintainer becomes unavailable:

- the code stays available under Apache-2.0 and may be forked;
- existing signed releases remain verifiable with `build/allowed_signers`;
- there is no guarantee of fixes, releases or security patches from this project;
- the website and package mirrors may stop receiving updates.

Do not treat JealousVault as the only copy of your data. Keep an independent copy of the repository
password, follow 3-2-1, and run `test-restore` regularly. The tool is designed so that a restic
repository remains readable without the agent.

## Decisions

- Architecture decisions are recorded as ADRs in [docs/adr/](docs/adr/README.md).
- The maintainer has the final say, with data safety and security first.
- The public product is complete and Apache-2.0; commercial work is additive: extensions, plugins,
  hosted services, support and custom development. The boundary is in [docs/mvp.md](docs/mvp.md) and
  [ECOSYSTEM.md](ECOSYSTEM.md).
- Contributions to the public repository need only a DCO sign-off and are accepted under Apache-2.0.
  A separate [CLA.md](CLA.md) applies only to code accepted into a commercial extension.
- Breaking changes follow [STABILITY.md](STABILITY.md).

## How to help

- Report bugs with the version, OS, restic version, a config without secrets and reproduction steps.
- Send security issues privately, not as public issues.
- Improve documentation, tests and platform notes.
- Run `test-restore` on your own data and report what did not work.
- Contribute under [CONTRIBUTING.md](CONTRIBUTING.md).

## Funding

The project is currently unfunded. The public product is not gated: paid extensions, plugins,
support, consulting and custom development fund the work. Commercial extension authors retain
ownership of their work. The policy is in [ECOSYSTEM.md](ECOSYSTEM.md); there is no bug bounty.
