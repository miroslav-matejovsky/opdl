# Step 08: extend site distribution to multiple machines

| | |
| --- | --- |
| Complexity | High |
| Effort | 8-12 person-days, re-estimate after step 06 |
| Depends on | 06, 07 |

## Goal

Site-scoped facts reach every machine in a site. Machines converge after any
offline interval, and registration returns the same answers from every caught-up
machine.

## Current gaps

- A descriptor contains only the local machine.
- `app.topology` returns only that machine.
- The blueprint has no site distribution configuration.
- There is no network transport, authentication, or multi-machine scenario.

## Work

1. Resolve the site's expected machine list and selected distribution
   configuration from the blueprint into every machine descriptor. Users must
   not author a duplicate topology list.
2. Implement the ADR's multi-machine adapter behind the same publication and
   consumer contracts proven in step 07.
3. Keep durable consumer identity machine-scoped. Primary and standby processes
   must not count as separate registration voters or handlers.
4. Make startup readiness mean caught up with the site before domain operations
   are served.
5. Define and secure every non-loopback endpoint. Existing platform HTTP APIs
   remain loopback-only.
6. Add Windows scenarios for:
   - two-machine acceptance;
   - concurrent conflicting proposals from different origins;
   - a machine offline while facts are published, then converging after return;
   - local primary/standby failover during site traffic; and
   - restart from durable site state.

## Acceptance criteria

- Every caught-up machine derives the same conflict winner.
- An offline expected machine keeps a proposal pending and is named in the
  projected view.
- The returning machine receives everything required to converge.
- Site transport failure does not prevent local ownership transfer.
- Failover does not create a second machine vote or durable handler identity.
- The multi-machine scenarios pass without changing the single-machine
  behavior from step 07.

## Risks

- Network security and durable recovery are part of the feature, not follow-up
  polish.
- A two-node consensus group cannot tolerate one node loss. Replica policy must
  follow the accepted ADR rather than assumptions left from the removed NATS
  design.
