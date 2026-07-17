# Stage 4: Runtime cutover

## Outcome

Make NATS Event Fabric the platform's only runtime coordination path. Update
deployment descriptors, configuration, lifecycle, APIs, SDKs, and scenarios in
one breaking cutover.

Complexity: High.

Estimated time: 3-5 engineering days.

Depends on: [Stage 3](03-registration-projections.md).

## Work

1. Replace descriptor `Fabric` topology with Event Fabric or NATS site topology.
   Update builder and platform descriptor models together and keep conformance
   tests directional.
2. Replace `[fabric.olric]` and `events_dir` configuration with explicit Event
   Fabric settings for client, cluster, monitoring, data directory, startup,
   catch-up, drain, and shutdown. Keep identity and peer membership derived from
   the embedded descriptor.
3. Compose runtime startup in this order:
   - validate all configuration and writable storage paths;
   - start and connect the embedded NATS server;
   - create or validate the site journal;
   - start durable service handlers;
   - rebuild local projections through a captured high-water sequence;
   - attach live projection delivery;
   - start the HTTP API.
4. Reverse ownership on shutdown:
   - stop HTTP intake and drain requests;
   - stop new handler work and finish active event processing;
   - stop projectors;
   - drain Event Fabric publications and subscriptions;
   - close NATS and its storage.
5. Replace `platform.fabric.started` and `stopped` with Event Fabric lifecycle
   events. Publish `started` only after the journal is usable and catch-up has
   completed.
6. Remove the JSONL recorder from runtime composition. If local audit files are
   still useful, implement them later as an independent Event Fabric projector,
   not as a second write in domain operations.
7. Regenerate the OpenAPI description and .NET SDK for the asynchronous
   registration contract.
8. Rewrite scenarios to observe public APIs and NATS-backed outcomes. Do not
   inspect Olric membership or depend on local event files.
9. Add black-box scenarios for offline delivery, restart replay, conflict order,
   NATS startup failure, and graceful shutdown.
10. Update root, architecture, registration, platform, scenario, and package
    documentation to describe only the target model.

## Exit criteria

- A built platform binary starts its Event Fabric from descriptor topology and
  runtime socket or storage overrides.
- Two platform processes exchange events and converge their projections without
  a distributed map.
- A restarted process rebuilds its query state before becoming ready.
- Registration HTTP and generated SDK tests pass against the new asynchronous
  contract.
- No production runtime path starts Olric or writes local JSONL domain events.

## Open questions

- Must all expected NATS peers be reachable before the HTTP API becomes ready,
  or is journal quorum sufficient? The answer must match the replica decision
  from Stage 1.
- Where is the default JetStream data directory on a deployed machine, and who
  owns backup, capacity, and cleanup?
- Should monitoring use the NATS monitoring endpoint directly or expose a small
  OPDL readiness view?
- How should a client discover that a local projection is lagging: readiness,
  response metadata, or both?
- Are NATS client, route, cluster, and monitoring ports part of the generated
  deployment contract?

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

