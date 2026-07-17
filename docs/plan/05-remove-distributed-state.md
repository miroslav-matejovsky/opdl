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
  documentation.
- There is no implementation of a shared in-memory distributed-state fabric.
- No service coordinates through map create, swap, get, enumeration, polling,
  or repair.
- Domain packages depend only on narrow Event Fabric publisher and handler
  contracts plus local projection interfaces.
- All supported state can be rebuilt from retained NATS events.
- `task all` passes.

## Open questions and recommendations

- Delete `internal/fabric` or keep a forwarding package?
  Recommendation: delete it completely. No compatibility consumer remains, and
  a forwarding package would preserve the wrong state-oriented vocabulary.
- Add local projection snapshots now?
  Recommendation: no. Record replay duration and journal size in scenarios.
  Propose snapshots only when a measured startup objective cannot be met by full
  replay. Any future snapshot is node-local cache data, never shared state.
- Keep a JSONL audit subscriber?
  Recommendation: no in this migration. NATS is the single event source. Add a
  separately deployed projector later only if operators define an audit-file
  requirement.
- What prevents the old architecture from returning?
  Recommendation: extend the existing architecture checks so only
  `internal/eventfabric/nats` and `internal/app` may import NATS packages, and
  domain packages may import only `internal/eventfabric`. Add a small repository
  conformance test that rejects `olric-data` in module files. Do not enforce a
  broad ban on maps or local in-memory projections.

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
