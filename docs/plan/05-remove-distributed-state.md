# Stage 5: Remove distributed state

## Outcome

Delete every remaining Olric and shared in-memory state path. Finish with NATS
events as the only service coordination mechanism and node-local projections as
the only query state.

Complexity: Medium.

Estimated time: 2-4 engineering days.

Depends on: [Stage 4](04-runtime-cutover.md).

## Work

1. Delete `platform/internal/fabric/olric` and
   `platform/internal/fabric/memory`.
2. Delete the state-oriented `fabric.Fabric`, `Collection`, entry, membership,
   and collection contract tests. Do not rename these map operations into the
   new Event Fabric.
3. Delete registration store records, collection names, reconciliation loops,
   join-race regression tests, and state repair logic.
4. Remove Olric configuration and socket overrides from platform configuration,
   examples, scenario harnesses, and documentation.
5. Remove `github.com/olric-data/olric`, run `go mod tidy`, and confirm that its
   indirect dependency tree is gone.
6. Remove the JSONL and no-op event sinks if Stage 4 left them unused. Event
   publication must not be optional in a running service.
7. Rename remaining state-fabric terminology in logs, lifecycle events,
   descriptors, scenario names, and documentation to Event Fabric terminology.
8. Replace tests that used a shared-memory transport with:
   - direct pure projection and handler unit tests; or
   - integration tests using an isolated real embedded NATS server.
9. Search the repository for stale state assumptions, including `olric`,
   `fabric.Collection`, `registration-contenders`, `reconcile_interval`, and
   `[fabric.olric]`.
10. Run `task all` and resolve every format, lint, unit, conformance, generation,
    SDK, build, and scenario failure.

## Final verification

- Stop a node, publish registration events elsewhere, restart it, and verify it
  catches up and builds the same projection.
- Restart every node from retained JetStream storage and verify the site state
  is reconstructed without an Olric or JSONL source.
- Deliver the same event more than once and verify all projections and resulting
  decisions remain unchanged.
- Race two proposals and verify every node selects the same journal-order
  winner.
- Interrupt a handler before and after publishing its result and verify safe
  redelivery.
- Fill or make the JetStream data directory unavailable and verify writes or
  startup fail clearly.

## Exit criteria

- Repository search finds no Olric dependency, adapter, configuration, test, or
  documentation.
- There is no implementation of a shared in-memory distributed-state fabric.
- No service coordinates through map create, swap, get, enumeration, polling,
  or repair.
- Domain packages depend only on narrow Event Fabric publisher and handler
  contracts plus local projection interfaces.
- All supported state can be rebuilt from retained NATS events.
- `task all` passes.

## Open questions

- Should the old `internal/fabric` directory be deleted completely or retained
  only as a temporary forwarding package? The POC recommendation is complete
  deletion because backward compatibility is not required.
- Should local projection snapshots be added now? The recommendation is no.
  Measure replay first and add snapshots only when startup time requires them.
- Is a JSONL audit subscriber needed for operators after cutover? It is not part
  of authoritative state and should be planned separately if required.
- What repository check should prevent Olric or distributed key-value state from
  returning later?

## Risks

- Removing old integration tests can reduce coverage if their observable
  behavior is not recreated with NATS scenarios. Map each deleted scenario to a
  replacement before deletion.
- An indirect Olric dependency can remain in `go.sum` if module cleanup is not
  run at every affected module level.
- Stale documentation can lead new code back toward shared state. Update package
  docs and architecture docs in the same change as deletion.
- Snapshot work introduced during cleanup can recreate synchronization and
  consistency complexity. Keep snapshots local and deferred.

