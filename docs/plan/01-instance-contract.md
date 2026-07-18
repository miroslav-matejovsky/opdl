# Stage 1: Instance and fencing contract

## Outcome

Define the process identities, lifecycle states, active ownership, and failure
rules before changing descriptors or runtime composition.

Complexity: Medium.

Estimated time: 2-3 engineering days.

## Work

1. Define two stable local slots, `a` and `b`. A slot is process identity, not a
   machine, service, registration voter, or permanent primary.
2. Define the lifecycle states used by runtime code and local diagnostics:
   `starting`, `standby`, `activating`, `active`, `stopping`, and `failed`.
3. Freeze the ownership rule: only the process holding the exclusive active
   fence may bind the public API, start durable domain handlers, publish machine
   lifecycle readiness, or host the embedded NATS server.
4. Specify an `internal/instance` boundary with a small consumer-owned contract:
   acquire or wait for the machine fence, release it after active resources
   close, report the current slot state, and stop on context cancellation.
5. Define the fence path from project, environment, site, and machine under a
   configured local runtime directory. Slot names must not produce different
   fence paths.
6. Add slot identity to operational event node identity and logs. Keep proposal,
   decision, and durable-handler identities machine-scoped so two slots never
   become two domain voters.
7. Define shutdown semantics:
   - process crash releases the OS lock automatically;
   - graceful active shutdown closes HTTP, handlers, projector, and NATS before
     releasing the fence;
   - cancelling a standby ends its fence wait without promotion;
   - full machine shutdown stops slot `b` before slot `a`.
8. Update package documentation for `events`, `eventfabric`, `registration`, and
   `app` with the final invariants. Keep the documentation self-contained.

## Exit criteria

- One document and tested core types use the same machine, slot, active, standby,
  and fence terminology.
- Domain identities remain machine-scoped.
- Every active-only capability has an explicit fence ownership rule.
- There is no timeout lease, distributed election, or active-active path.

## Open questions and recommendations

- Should slot `a` always become active first?
  Recommendation: no. Let the first healthy process acquiring the lock become
  active. Fixed preference delays recovery when the preferred slot is absent.
- Should an unhealthy live active be preempted after a timeout?
  Recommendation: no. The service manager must terminate it. Time-based lock
  stealing cannot fence a paused process when it resumes.
- Is strict zero-downtime listener handoff required now?
  Recommendation: no for the POC. Guarantee drained accepted requests and
  bounded promotion. Record the listener gap in scenarios before designing a
  proxy or socket-activation boundary.

## Risks

- Treating slot identity as machine identity would duplicate registration votes.
- Releasing the fence before active resources close can allow two publishers or
  two NATS servers to overlap.
- A lock placed on a network filesystem may not provide the required process
  death and exclusivity semantics.
