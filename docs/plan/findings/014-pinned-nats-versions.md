# F014: NATS versions are deliberately pinned

Category: Dependency management  
Severity: Medium  
Status: Accepted  
Source: Stage 2

## Finding

The adapter uses `nats-server/v2 v2.11.9` and `nats.go v1.46.1` instead of
automatically following the newest releases. Deployment topology relies on
measured server behavior from this line.

## Resolution

Exact versions make builds reproducible and keep the tested embedded server and
client APIs together.

## Recommendation

Upgrade both dependencies in one isolated change. Run single-node, three-storage
cluster, restart, replay, deduplication, capacity, and quorum tests before
accepting the bump.
