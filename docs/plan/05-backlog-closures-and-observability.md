# Stage 5: Close related topology backlog and improve failover evidence

## Outcome

Use this initiative to close the three-storage-node topology proof and advance
the Windows failover investigation. Leave unrelated backlog items unchanged.

## Complexity and estimate

- Complexity: High
- Estimate: 1.5 to 2.5 developer days
- Dependencies: Stages 3 and 4
- Primary areas: Event Fabric integration tests, scenarios, runtime lifecycle
  logs, `docs/backlog`

## Backlog audit

### Event Fabric: prove the three-storage-node failure topology

Status for this initiative: Include and close when its acceptance criteria pass.

This is directly relevant because it proves why `cluster_port` remains in the
blueprint and confirms that only the selected three machines bind cluster
listeners.

Add a focused integration scenario with at least three machines in one site.
Prefer four machines so the test also proves that the fourth machine is
client-only.

The scenario must:

1. Build all machines from one rendered blueprint.
2. Start the three selected storage machines and the client-only fourth machine.
3. Verify the first three machines by sorted name report storage with three
   replicas.
4. Verify only those three have non-empty route lists.
5. Verify the fourth machine reports no server or cluster listener ownership.
6. Publish through one machine and replay or query through another.
7. Stop one storage machine.
8. Verify the remaining site can still accept and project new events.
9. Restart the stopped storage machine with its existing data directory.
10. Verify it rejoins, catches up, and returns the same projected state.

Avoid sleeps. Use API readiness, status files, journal sequence progress, and
observable registration state as handshakes.

After the scenario passes in the required validation gate:

- remove the completed item from `docs/backlog/event-fabric.md`;
- remove or update its row in `docs/backlog/README.md`;
- update `docs/01-architecture.md` to point at the scenario as evidence;
- remove an empty backlog file only if it has no remaining subsystem context.

### Local redundancy: investigate the Windows forced-kill promotion gap

Status for this initiative: Instrument now. Close only after repeated Windows
and Linux evidence identifies the slow phase.

Add stable elapsed-time logs at these boundaries:

- fence wait begins and fence is acquired;
- standby client composition close begins and ends;
- embedded server start begins and becomes ready;
- JetStream journal open or recovery completes;
- projector first catch-up completes;
- retained handler drain completes;
- readiness publication is projected;
- public API listener binds;
- active resources close and fence releases.

Use a process-local monotonic start time and durations. Do not put wall-clock
timestamps into domain events. Do not expose credentials or event payloads.

Update the warm standby scenario to print a phase table when the markers are
available. Preserve the existing overall catch-up, promotion, listener gap,
handover, and memory measurements.

Run repeated samples during implementation on Windows and Linux CI if both are
available. The backlog item can be removed only when evidence shows which phase
accounts for the approximately 30 second forced-kill gap and the architecture
documentation records the result. If cross-platform samples are unavailable,
rewrite the backlog item to the remaining evidence collection step instead of
claiming it is resolved.

### API contract and registration backlog

Status for this initiative: Leave unchanged.

Closed OpenAPI value sets and registration key release have no dependency on
the NATS endpoint or standby contract. Do not mix them into this change.

## Platform-owned observability

The NATS HTTP monitor removal must not reduce runtime correctness checks.
Confirm that the existing Event Fabric `State` and redundancy status files cover:

- connection availability;
- applied journal sequence;
- current high-water sequence;
- caught-up state;
- lag duration;
- promotability;
- last runtime error;
- active, standby, activating, stopping, and failed lifecycle state.

Document those files as the supported local monitoring surface. If a required
NATS health signal is missing, add it to `eventfabric.State` and status output
only when it is transport-neutral and actionable.

Do not add a public NATS-compatible monitor. A later remote operational API must
be platform-owned, authenticated, authorized, and designed as its own contract.
If that API is required but not implemented here, add a focused backlog item
that names the missing consumer and signal.

## Documentation updates

Update:

- `docs/01-architecture.md` for the shared endpoint and actual listener matrix;
- `platform/README.md` for fence-owned shared NATS endpoints;
- `platform/internal/eventfabric/nats/doc.go` for client versus cluster roles and
  disabled monitor behavior;
- `platform/internal/redundancy/doc.go` for shared endpoint transfer ordering;
- `scenarios/doc.go` for the three-storage-node proof and port-source behavior;
- backlog index and completed items according to the rules above.

Remove the obsolete statement that ports 4222, 6222, and 8222 are all fixed and
derived. Document authored client and cluster ports and the absence of a monitor
listener.

## Exit criteria

- The three-storage-node failure scenario passes and its backlog item is closed.
- The fourth machine in that scenario is proven client-only.
- Failover phase instrumentation is available in diagnostics.
- The redundancy backlog accurately states either the identified cause or the
  remaining cross-platform evidence needed.
- Local status documentation replaces reliance on NATS HTTP monitoring.
- Unrelated backlog items are unchanged.

