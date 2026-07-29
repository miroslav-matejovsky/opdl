# Backlog: live health-route partition and recovery

**Priority**: P1

**Effort**: Medium

**Value**: High

## Gap

Current health scenarios use multiple routed embedded NATS brokers and prove
cross-instance convergence. Observer-loss coverage stops a platform process.
It does not cut only the NATS route while both brokers, both platform processes,
and all service probes remain running.

The following behavior is therefore inferred rather than verified:

- remote observations expire during a route partition;
- local probes and local observations continue;
- platform ownership remains independent of the partition; and
- restored routes deliver new snapshots and converge without replay.

## Recommendation

Run this scenario in a dedicated Windows integration environment that can
temporarily block the route ports, for example with a narrowly scoped Windows
Firewall rule. The rule must target only the selected NATS route connection and
must be removed by test cleanup.

Do not add a production transport switch or persistence only to create a test
seam. If privileged network control is unavailable in normal CI, run this as a
separate privileged integration job.

## Required scenario

1. Start at least two machines with all platform instances and services healthy.
2. Wait until every instance reports `distribution.state = Connected`.
3. Block route traffic between two live broker groups without stopping either
   broker or platform process.
4. Prove local services continue to be probed and local observer ages reset.
5. Prove remote observers become stale and distribution becomes `Partial` or
   `Isolated` as topology requires.
6. Prove platform health, readiness, lease renewal, and ownership are unchanged.
7. Restore the route.
8. Prove every instance converges from new periodic reports within one maximum
   probe interval plus bounded propagation time.
9. Prove no health-result persistence or replay was used.

## Acceptance

- The partition affects only route traffic.
- Both sides remain live and independently observable during the partition.
- Staleness and distribution state match the documented API contract.
- Recovery converges without restarting any process.
- Cleanup restores the host firewall and ports even when an assertion fails.
- `task all` remains green; the privileged scenario may run in a separate
  explicit gate.
