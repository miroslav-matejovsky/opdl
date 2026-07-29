# Backlog: simultaneous redundant-instance startup

**Priority**: P1

**Effort**: Small to medium

**Value**: High

## Gap

`redundancy/FailoverAndFailback` now starts the Primary first so its exact event
counts are deterministic. Before that change, Primary and Standby could race for
an empty lease and the Standby could become the first owner. The scenario
comment allowed either winner, but its fixed activation counts did not.

The deterministic scenario is correct for failover and failback. It no longer
tests the real machine-start condition where Windows may start both services at
nearly the same time.

This is a redundancy gap discovered while validating service-health load. It is
not a service-health behavior.

## Recommendation

Add a separate redundancy scenario that starts Primary and Standby
concurrently. Accept either as the first lease owner and derive later
expectations from the observed winner.

## Required assertions

- Exactly one instance owns the lease at any instant.
- Both instances never serve Active simultaneously.
- The losing instance remains Passive and healthy.
- Lease and event records describe one valid acquisition order without
  requiring a particular first winner.
- Preferred-primary policy eventually returns ownership to Primary when
  Standby won first.
- Restarting the machine repeats the safety properties without relying on
  startup timing.
- Service-health workers, when present, run in both instances regardless of
  which one wins.

## Acceptance

- The existing deterministic failover/failback scenario stays unchanged.
- The new scenario passes repeatedly without sleeps or fixed activation counts.
- Assertions tolerate both valid first-owner outcomes but no split ownership.
- `task all` passes.
