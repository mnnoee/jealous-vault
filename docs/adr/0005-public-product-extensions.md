# ADR-0005: Public product with independent extensions

English · [Русский](ru/0005-public-product-extensions.md)

- Status: accepted
- Date: 2026-11-20

## Context

JealousVault is maintained by one person and needs to remain sustainable. The code is Apache-2.0, and that licence is irreversible: anything published in this repository stays open. Users of a backup tool are unusually sensitive to paywalls around restore and data safety.

An open-core model with a paid tier for core capabilities would create an artificial boundary and put trust at risk. At the same time, extensions, integrations, hosted services and support are natural things to offer commercially, and third-party developers may want to build plugins under their own licences.

## Decision

The public repository contains the complete product under Apache-2.0, with no feature gates. It is not a crippled freemium edition.

Commercial work is additive and separate:

- extensions, plugins, chat integrations, a central web UI, hosted services and support;
- separate repositories and separate licences;
- authors retain ownership of their work, including third-party plugins under GPL-3.0 or another licence;
- the public product is never reduced to make a commercial extension look better.

Contributions to the public repository require only a DCO sign-off and are accepted under Apache-2.0. They do not grant additional proprietary rights.

A separate written agreement, [CLA.md](../../CLA.md), is used only when mnnoee accepts a contributor's code into a commercial extension. It names mnnoee as the licensee, includes a copyright and patent licence, defines what counts as the Contribution, and states that later agreements do not revoke earlier grants.

## Consequences

- The project does not need to own contributors' code to use it; Apache-2.0 already permits commercial use, forks and compatible products.
- The paid offer must be genuinely additional work, not a withheld part of the public product.
- Competitors may fork the public product and offer their own support or extensions. That is accepted; the project competes on quality, not on a gate.
- The ecosystem can include GPL-3.0 plugins and alternative implementations. The documented CLI, config and JSON output are the stable integration surface.
- A CLA is needed only for commercial-extension contributors, so the friction for ordinary pull requests stays low.
- The name and logo need clear rules so that third-party work does not imply official endorsement.

## Alternatives considered

- **Open core with paid core capabilities**: rejected because it creates an artificial boundary, invites trust problems around restore and security, and makes the public product feel incomplete.
- **Closed source**: rejected; it removes auditability, the manual restic escape hatch and the ability to verify that restores work.
- **Donations only**: compatible with the licence, but not a reliable way to fund support and integration work.
- **CLA for every contribution**: rejected; it adds unnecessary friction, and Apache-2.0 already allows the project and others to use the code commercially.
