# Migration findings catalog

This catalog contains every distinct finding recovered from the Stage 1 through
Stage 5 implementation notes. Each finding has one owner file with its category,
severity, current status, resolution, and recommendation. Resolved findings stay
here because they document constraints that future changes must preserve.

## Severity

| Severity | Meaning |
| --- | --- |
| Critical | Can lose the source of truth, break consistency, or prevent required production availability. |
| High | Can stop service progress, serve stale results, or create a major operational gap. |
| Medium | Creates bounded architectural, maintenance, or performance risk. |
| Low | Has limited operational effect or is mainly a contract and test clarity concern. |

Statuses are `Resolved`, `Accepted`, `Accepted for POC`, `Mitigated`,
`Mitigated by deployment`, `Risk accepted`, and `Open`. Accepted findings are
deliberate constraints, not missing work. Open findings require a feature or test
before the capability named in their recommendation can be claimed.

## Deployment baseline

| Deployment | Minimum requirements | Accepted limitations |
| --- | --- | --- |
| Local POC | One machine, loopback NATS, writable persistent journal directory. | No process or storage failure tolerance. |
| Multi-machine POC | One or two machines, persistent storage on the first machine by sorted name, storage node starts first, all expected machines online for registration progress. | Loss of the storage node stops the site. An offline expected machine leaves proposals pending. |
| Resilient journal | At least three machines, stable storage on the first three machines by sorted name, three replicas, credentials on non-loopback addresses. | Tolerates one journal node loss. It still has no service-process failover. |
| Production target | Resilient journal baseline plus resolution of F019, F021, and F025. | No production availability claim should be made before those findings close. |

## Attention summary

| Attention | Count | Meaning |
| --- | ---: | --- |
| Act before production | 3 | Production availability or correctness cannot be claimed until these close. |
| Act when triggered by scope or measurements | 3 | No action now. Implement only when the named trigger occurs. |
| Enforce during deployment and upgrades | 5 | These are operating rules, not missing code. |
| Understand and accept | 6 | Deliberate constraints with no current implementation work. |
| No attention required | 9 | Resolved guardrails retained only for design history. |

## 1. Act before production

These need the most attention. Resolve them before describing OPDL as
production-ready or tolerant of a machine failure.

| Priority | Finding | Why it needs attention |
| --- | --- | --- |
| P0 | [F025: Journal replication is not service redundancy](025-service-redundancy.md) | A machine has no fenced standby. Its service and decisions stop when its process is unavailable. |
| P0 | [F019: Live projection lag is not exposed](019-live-readiness-and-lag.md) | A slow projector can keep serving stale state without becoming unready. |
| P0 | [F021: Three-storage cluster behavior needs a focused test](021-multi-storage-cluster-coverage.md) | The topology used for one-node journal fault tolerance lacks a focused failure and rejoin test. |

## 2. Act only when triggered

These are real gaps, but implementing them now would be premature.

| Finding | Trigger for action |
| --- | --- |
| [F007: Rejected claims do not release a key](007-rejected-claim-key-release.md) | Removal or re-registration enters product scope. |
| [F009: Projection routing supports one stateful domain](009-projection-domain-routing.md) | A second stateful domain is introduced. |
| [F022: Local projection snapshots are deferred](022-local-projection-snapshots.md) | Measured replay time exceeds a defined startup objective. |

## 3. Enforce during deployment and upgrades

These need operator and release attention. They do not require new code now.

| Finding | Required rule |
| --- | --- |
| [F006: Registration captures a static expected-machine set](006-static-expected-machine-set.md) | Treat the descriptor topology as part of proposal identity. Topology changes require new proposals. |
| [F013: The NATS cluster must contain only storage nodes](013-storage-only-nats-cluster.md) | Use at least three storage machines for one-node journal failure tolerance. Do not add non-storage servers to the cluster. |
| [F014: NATS versions are deliberately pinned](014-pinned-nats-versions.md) | Upgrade the server and client together and re-run topology and replay tests. |
| [F020: Non-storage nodes depend on storage-node boot order](020-non-storage-startup-dependency.md) | Start storage nodes first. One- and two-machine sites accept a single storage dependency. |
| [F026: All-machine confirmation blocks while any machine is offline](026-all-machine-confirmation-availability.md) | Keep every expected machine available when registration progress is required. |

## 4. Understand and accept

These are deliberate contracts or bounded test limitations. They need awareness,
not implementation work.

- [F002: Nested node identity](002-nested-node-identity.md)
- [F003: Schema version applies to payloads](003-payload-schema-version.md)
- [F004: Event and route namespaces differ](004-event-route-namespaces.md)
- [F011: Invalid history and invalid proposals differ](011-invalid-history-policy.md)
- [F012: Historical platform-instance IP may be empty](012-historical-instance-ip.md)
- [F017: Graceful shutdown lacks a portable black-box test](017-graceful-shutdown-scenario.md)

## 5. No attention required

These findings are resolved. Keep their decisions unless a future design change
explicitly reopens them.

- [F001: Staged cutover boundaries](001-staged-cutover-boundaries.md)
- [F005: Handler publications need causal links](005-causal-event-links.md)
- [F008: Journal order replaces local sequence and JSONL order](008-journal-order-and-jsonl-retirement.md)
- [F010: Transport deduplication is not correctness](010-stable-deduplication-identities.md)
- [F015: Readiness belongs to runtime composition](015-readiness-ownership-and-catch-up.md)
- [F016: Cancelled loops looked like failures](016-cancelled-loop-shutdown.md)
- [F018: Journal capacity must fail writes visibly](018-journal-capacity-rejection.md)
- [F023: JSONL is not a second audit source](023-jsonl-audit-export.md)
- [F024: Architecture regression guards](024-architecture-regression-guards.md)
