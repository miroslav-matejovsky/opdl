# Stage 1: Instance and fencing contract

## Outcome

Define the process identities, lifecycle states, active ownership, and failure
rules before changing descriptors or runtime composition.

Complexity: Medium.

Estimated time: 2-3 engineering days.

## Work

1. Define two process roles: the preferred primary and the optional standby. A
   process role is not a machine, service, or registration voter.
2. Define the lifecycle states used by runtime code and local diagnostics:
   `starting`, `standby`, `activating`, `active`, `stopping`, and `failed`.
3. Freeze the ownership rule: only the process holding the exclusive active
   fence may bind the public API, start durable domain handlers, publish machine
   lifecycle readiness, or host the embedded NATS server.
4. Specify an `internal/redundancy` boundary with a small consumer-owned contract:
   acquire or wait for the machine fence, release it after active resources
   close, report the current process state, and stop on context cancellation.
5. Define the fence path from project, environment, site, and machine under a
   configured local runtime directory. Process roles must not produce different
   fence paths.
6. Add process-role identity to operational lifecycle payloads, logs, and local status.
   Keep the event envelope node, proposal, decision, and durable-handler
   identities machine-scoped so two processes never become two domain voters.
7. Define shutdown semantics:
   - process crash releases the OS lock automatically;
   - graceful active shutdown closes HTTP, handlers, projector, and NATS before
     releasing the fence;
   - cancelling a standby ends its fence wait without promotion;
   - full machine shutdown stops the primary service and then the standby
     service; bounded standby promotion during that interval is allowed.
8. Update package documentation for `events`, `eventfabric`, `registration`, and
   `app` with the final invariants. Keep the documentation self-contained.

## Exit criteria

- One document and tested core types use the same machine, primary, standby, active,
  and fence terminology.
- Domain identities remain machine-scoped.
- Every active-only capability has an explicit fence ownership rule.
- There is no timeout lease, distributed election, or active-active path.

## Open questions and recommendations

- Should the primary always be preferred?
  Recommendation: yes. Deployment starts it first. After failover, it reclaims
  active ownership through graceful handover when it returns.
- Should an unhealthy live active be preempted after a timeout?
  Recommendation: no. The service manager must terminate it. Time-based lock
  stealing cannot fence a paused process when it resumes.
- Is strict zero-downtime listener handoff required now?
  Recommendation: no for the POC. Guarantee drained accepted requests and
  bounded promotion. Record the listener gap in scenarios before designing a
  proxy or socket-activation boundary.

## Risks

- Treating process-role identity as machine identity would duplicate registration votes.
- Releasing the fence before active resources close can allow two publishers or
  two NATS servers to overlap.
- A lock placed on a network filesystem may not provide the required process
  death and exclusivity semantics.
