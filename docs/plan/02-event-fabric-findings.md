# Stage 2 findings: remaining work and decisions

Findings resolved by Stages 3 and 4 were removed. Registration projections now
ignore events outside their domain while rejecting unknown registration events,
and all registration facts provide stable Event Fabric deduplication identities.
Stage 4 took ownership of catch-up orchestration, composed the adapter into the
runtime, and integration-tested a two-machine site — correcting the storage
topology in the process, which also ended the "monitoring binds on every node"
finding: only a storage node binds anything now. See
[04-runtime-cutover-findings.md](04-runtime-cutover-findings.md).

## 1. Pinned NATS versions favor the mature line

The adapter uses `nats-server/v2 v2.11.9` and `nats.go v1.46.1`. These versions
support the embedded server and modern JetStream APIs used here. A version bump
should be a separate dependency change with the adapter integration tests run
against it.

Stage 4 now depends on one measured internal behavior of this line: NATS sizes a
JetStream metadata group from a server's configured routes rather than from the
servers that enable JetStream. A bump should re-check that, because the site's
whole storage topology is shaped around it.
