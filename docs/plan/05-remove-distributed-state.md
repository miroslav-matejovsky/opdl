# Stage 5: Remove distributed state

Status: Complete (2026-07-17).

## Completion notes

The obsolete `internal/fabric` contract, memory adapter, Olric adapter, JSONL
sink, recorder, tests, configuration vocabulary, and module dependency tree are
deleted. Event ID generation and envelope stamping remain in `internal/events`
because the NATS publisher uses them directly.

A focused real-NATS test verifies that a full `DiscardNew` journal rejects new
writes instead of deleting replay history. Strict `go-arch-lint` rules deny
unlisted vendors, deep-scan dependency injection, allow NATS only in its adapter,
and prevent runtime components from depending on the reserved state-fabric
boundary.

All implementation findings, including resolved decisions and remaining risks,
are in the [migration findings catalog](findings/README.md).

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
2. Delete the rest of `platform/internal/fabric`, including state-oriented
   `Fabric`, `Collection`, entry, membership, lifecycle events, and collection
   contract tests. Do not rename these map operations into the new Event Fabric.
3. Delete `platform/internal/registration/store.go`, `reconciler.go`, storage
   records and keys from `records.go`, `contenders_olric_test.go`, and old
   reconcile-focused tests after Stage 3 replacements pass.
4. Remove `FabricOlric`, `[fabric.olric]`, `reconcile_interval`, Olric socket
   overrides, and `events_dir` from configuration, examples, scenario harnesses,
   generated test fixtures, and documentation.
5. Remove `github.com/olric-data/olric` from `platform/go.mod`, run
   `go mod tidy` in every Go module through the repository task, and confirm that
   Olric and its adapter-only indirect dependency tree are gone from `go.sum`.
6. Remove the JSONL and no-op event sinks if Stage 4 left them unused. Event
   publication must not be optional in a running service.
7. Rename remaining state-fabric terminology in logs, lifecycle events,
   descriptors, scenario names, and documentation to Event Fabric terminology.
   Do not blindly replace the word `fabric`; keep it where it means the new
   Event Fabric and remove it where it means distributed maps.
8. Replace tests that used a shared-memory transport with:
   - direct pure projection and handler unit tests; or
   - integration tests using an isolated real embedded NATS server.
9. Run these repository searches and require no obsolete result outside this
   migration plan:

   ```powershell
   rg -n -i -g "!docs/plan/**" "olric|olric-data" .
   rg -n -g "!docs/plan/**" "fabric\.Collection|registration-contenders|reconcile_interval" .
   rg -n -g "!docs/plan/**" "\[fabric\.olric\]|events_dir" .
   rg -n "internal/fabric" platform scenarios conformance-tests builder
   ```

10. Add an architecture rule that domain packages cannot import the NATS client,
    NATS server, or `internal/eventfabric/nats`. Only runtime composition may
    import the adapter; domains depend on the core Event Fabric interfaces.
11. Run `task all` and resolve every format, lint, unit, conformance, generation,
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
  documentation outside this historical migration plan.
- There is no implementation of a shared in-memory distributed-state fabric.
- No service coordinates through map create, swap, get, enumeration, polling,
  or repair.
- Domain packages depend only on narrow Event Fabric publisher and handler
  contracts plus local projection interfaces.
- All supported state can be rebuilt from retained NATS events.
- `task all` passes.

## Findings

Stage 5 decisions and risks are cataloged individually as F008, F018, F022,
F023, and F024 in the [migration findings catalog](findings/README.md).
