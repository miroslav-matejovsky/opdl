# Step 09: record service-activation constraints

| | |
| --- | --- |
| Complexity | Low |
| Effort | 1-2 person-days |
| Depends on | 06 |

## Goal

Write a design note that keeps future Master/Slave service activation compatible
with the hierarchy. This step adds no activation code.

## Constraints to record

1. Activation facts are site-scoped. No fourth event scope is needed.
2. Service Master/Slave is separate from platform Primary/Standby.
3. Activation reads registered service units through a narrow registration query
   contract. It does not reach into projection internals.
4. Activation needs a new site-level liveness mechanism. Registration has no
   leases, heartbeats, or quorum.
5. A platform failover does not change service roles. Site-visible identities
   are machine-scoped, not process-role-scoped.
6. The site-distribution decision may support more than one site use case, but
   activation must not force speculative changes into registration.

## Actions

1. Create `docs/drafts/service-activation.md`.
2. Check each constraint against the accepted distribution ADR and implemented
   registration boundary.
3. Record only required amendments and open product questions: election policy,
   manual override, liveness, and observability.

## Acceptance criteria

- Each constraint has a concrete verdict with package references.
- Any required change is filed against a specific plan step or backlog item.
- No production code changes are included.
