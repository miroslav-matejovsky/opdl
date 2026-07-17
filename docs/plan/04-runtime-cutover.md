# Stage 4: Runtime cutover

Status: Complete. Findings, corrected decisions, and deferred work are in
[04-runtime-cutover-findings.md](04-runtime-cutover-findings.md).

## Completion notes

The cutover is done and `task all` passes. What differs from the work below:

- **The storage topology changed.** Work item 3 assumed the accepted "Core NATS
  nodes route to the storage nodes" design. It does not work: NATS sizes a
  JetStream metadata group from a server's routes, not from the servers holding
  storage, so a Core NATS node in the cluster makes the group inquorate. The
  site's cluster is now exactly its storage nodes, and every other machine is a
  client of them. This is finding 1, and it is the reason the two-machine
  scenario passes.
- **Readiness and shutdown moved out of the adapter** (finding 2), which work
  item 5 required anyway: `ready` now carries the server, journal, storage role,
  replicas, and applied high-water sequence, and is stated by composition after
  catch-up rather than by the transport when it connected.
- **Work item 9 is partly deferred.** Offline delivery, restart replay, conflict
  order, and NATS startup failure have scenarios. Graceful shutdown does not: a
  portable child interrupt does not exist on Windows, and the ordering is covered
  in-process (finding 5).
- **No health endpoint was added.** The open questions recommend one; the node's
  startup gate makes it unnecessary for readiness but not for live lag
  (finding 8).

## Outcome

Make NATS Event Fabric the platform's only runtime coordination path. Update
deployment descriptors, configuration, lifecycle, APIs, SDKs, and scenarios in
one breaking cutover.

Complexity: High.

Estimated time: 3-5 engineering days.

Depends on: [Stage 3](03-registration-projections.md).

## Work

1. Rename descriptor `Fabric` to `EventFabric` and `FabricPeer` to
   `EventFabricPeer` in both `builder/deployment` and `platform/deployment`.
   Retain only peer site, machine, and IP. Update blueprint resolution and
   deployment conformance tests in the same change.
2. Replace `[fabric.olric]`, `events_dir`, and registration
   `reconcile_interval` in `platform/config.toml` and `internal/config` with the
   `[event_fabric.nats]` shape from Stage 2. Update config validation and startup
   summary tests. Never print the credential value or file contents.
3. Replace `newRecorder`, `startFabric`, `olricConfig`, `startReconciler`, and
   `stopFabric` in `internal/app` with Event Fabric composition. Compose runtime
   startup in this order:
   - validate all configuration and writable storage paths;
   - start and connect the embedded NATS server;
   - create or validate the site journal;
   - start the continuous node-wide projection runner, capture a high-water
     sequence, and wait until that sequence is applied by every registered
     domain projection;
   - start durable service handlers and let them process retained work;
   - capture a second high-water sequence after the initial handler backlog is
     empty and wait for projectors to apply it;
   - publish `platform.event_fabric.ready` and wait for its receipt sequence to
     be applied;
   - start the HTTP API.
   Bound the complete readiness sequence with `catch_up_timeout`. Report the
   projector sequence and each handler's pending count when it times out.
4. Reverse ownership on shutdown in `serve`:
   - stop HTTP intake and drain requests;
   - stop new handler work and finish active event processing;
   - publish `platform.event_fabric.stopping`;
   - stop projectors;
   - drain Event Fabric publications and subscriptions;
   - close NATS and its storage.
5. Replace `platform.fabric.started` and `stopped` with
   `platform.event_fabric.ready` and `platform.event_fabric.stopping`. Include
   server name, journal name, storage role, replicas, and applied high-water
   sequence in `ready`. Do not claim a completed close through the transport
   being closed.
6. Remove the JSONL recorder from runtime composition. If local audit files are
   still useful, implement them later as an independent Event Fabric projector,
   not as a second write in domain operations.
7. Regenerate the OpenAPI description and .NET SDK for the asynchronous
   registration contract.
8. Rename `two_machine_fabric_test.go` to an Event Fabric scenario. Update the
   scenario harness to allocate client, cluster, and monitoring ports plus a
   separate JetStream data directory per node. Observe public readiness and
   projected registration state. Do not inspect Olric membership or local event
   files.
9. Add black-box scenarios for offline delivery, restart replay, conflict order,
   NATS startup failure, and graceful shutdown.
10. Update `README.md`, `docs/01-architecture.md`, `docs/02-registration.md`,
    `platform/README.md`, `scenarios/doc.go`, and affected package docs to
    describe only the target model.

## Exit criteria

- A built platform binary starts its Event Fabric from descriptor topology and
  runtime socket or storage overrides.
- Two platform processes exchange events and converge their projections without
  a distributed map.
- A restarted process rebuilds its query state before becoming ready.
- Registration HTTP and generated SDK tests pass against the new asynchronous
  contract.
- No production runtime path starts Olric or writes local JSONL domain events.

## Open questions and recommendations

- Must every NATS peer be reachable for readiness?
  Recommendation: require a writable journal, functioning JetStream metadata,
  handler attachment, and projection catch-up. Do not require every configured
  peer. Registration still remains pending when an expected decision node is
  offline. This separates transport readiness from domain completion.
- Where does JetStream store data and who owns it?
  Recommendation: require `data_dir` in runtime configuration. The platform
  creates node-specific subdirectories and never automatically deletes them.
  Site operations own disk capacity and backup. Document that copied data must
  not be restored into two live servers with the same server identity.
- How is NATS monitored?
  Recommendation: expose a small OPDL readiness response containing journal
  writable state, projector applied/high-water sequence, handler pending counts,
  and last error. Keep the NATS monitoring listener on loopback for diagnostics.
- How does an API client see projection lag?
  Recommendation: remove the node from readiness when startup catch-up fails or
  live lag exceeds a configured bound. Include `applied_sequence` in the health
  response, not in every domain response. Add domain response metadata only if
  a real client needs bounded freshness later.
- Do NATS ports belong in deployment descriptors?
  Recommendation: no. Keep descriptors transport-neutral with peer identities
  and IPs. Ports are Event Fabric adapter constants with runtime overrides for
  co-located scenarios.

## Risks

- A lifecycle race can lose the gap between replay and live subscription. Test
  publication during catch-up.
- Starting handlers before projection catch-up can emit consequences based on
  incomplete local state. Handler dependencies and startup gates must be clear.
- File permissions or a full JetStream disk can prevent the entire platform
  from starting or accepting commands. Fail early with actionable errors.
- Generated API and SDK changes can make scenario failures noisy. Change the
  API source, regenerate artifacts, then update consumers in one stage.
- Removing local JSONL affects existing tests and observability. Replace each
  use with a public projection or an explicit NATS observer.
