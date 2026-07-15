# Stage 4: Topology-derived distribution foundation

Estimate: 4-6 engineer-days.

## Goal

Add one embedded distributed-data member per platform machine and derive its
default cluster bootstrap from the authored topology. Do not move registration
state into it until the lifecycle and topology contract are tested.

## Instructions

1. Review these old implementation files before coding:

   - `_opdl-v1/platform/internal/distdata/distdata.go`
   - `_opdl-v1/docs/roadmap/03-distributed-fabric.md`
   - `_opdl-v1/docs/roadmap/04-cleanup-static-topology.md`
   - `_opdl-v1/distribution/deployment`

   Retain only embedded Olric startup, shutdown, distributed-map access, member
   inspection, and configuration validation. Do not copy the old fabric,
   Pub/Sub, cluster, authority, registry, chaos, security, or redundancy layers.

2. Extend the builder and platform deployment descriptor contracts with a
   resolved distribution section. It must identify:

   - This machine's distribution client bind and advertise address.
   - This machine's Olric memberlist bind and advertise address.
   - Deterministically ordered memberlist join addresses for other machines in
     the same project and environment.
   - Enough machine identity beside each peer address to produce useful
     validation errors.

   Derive these values from the complete blueprint topology in
   `builder/internal/resolve`. Use port `3320` for the Olric client address and
   port `3322` for Olric memberlist, matching the proven `_opdl-v1` defaults.
   Document both ports. Do not add new HCL fields for the first use case unless
   topology data cannot determine a value.

3. Strengthen topology validation before derivation:

   - Machine IP addresses must be unique within one project when fixed platform
     ports are derived from them.
   - A machine must not list itself as a join peer.
   - Distribution addresses must be valid host and port pairs.
   - Join peers must remain inside the descriptor's project and environment.
   - Output ordering must be deterministic regardless of HCL declaration order.

4. Mirror the descriptor change in `builder/deployment` and
   `platform/deployment`. Update descriptor validation, conformance signatures,
   round-trip tests, embedded mock deployment JSON, builder plan output, package
   metadata tests, and all test fixtures. Keep producer and consumer types
   separate as they are now.

5. Extend runtime JSON configuration with optional distribution address and join
   overrides. Overrides exist for local development and scenarios that run
   several machines on one host. Production defaults still come from the
   descriptor. Validate the composed effective configuration before opening any
   listeners.

6. Create a minimal `platform/internal/distdata` package:

   - A narrow `Config` type.
   - `Start` with context and bounded readiness timeout.
   - `Stop` with context.
   - Member count or member snapshot for readiness tests and events.
   - Construction of a named distributed map, without exposing Olric types to
     registration code.
   - Clear errors for bind, join, readiness timeout, and unexpected member exit.

   Pin the selected Olric version in `platform/go.mod`. Start from the version
   already proven in `_opdl-v1` unless an incompatibility with the current Go
   version requires a documented change.

7. Configure the embedded map for the first-use-case constraints:

   - In-memory storage only.
   - No expiry.
   - One platform distribution member per machine.
   - No per-machine primary/secondary service instance.
   - No application-level redundancy, election, or fencing.

   Document whether Olric partitions or copies data internally. Do not describe
   backend partition ownership as platform service redundancy.

8. Wire distribution lifecycle into `platform/cmd/main.go`:

   - Resolve config and open the event sink first.
   - Start distribution before accepting public HTTP traffic.
   - Emit `platform.distribution.started` after readiness.
   - On shutdown, stop HTTP intake, stop distribution, emit the stopped event,
     then close the event sink.
   - If distribution startup fails, do not start the public API.

9. Add tests for descriptor derivation, conformance, config override precedence,
   invalid addresses, startup cancellation, clean stop, and single-member map
   access. Use dynamic ports in integration tests. Do not use fixed sleeps.

10. Add a two-machine scenario blueprint using distinct loopback IP addresses.
    Build both machine packages and start both with isolated API addresses and
    event directories. For this stage, verify only that both distribution
    members become ready and emit `platform.distribution.started`. Keep the
    registration scenario on the in-memory store until Stage 5.

11. Update the root architecture overview and deployment package docs with the
    descriptor-derived bootstrap, runtime overrides, ports, startup order, and
    current no-redundancy constraint.

12. Run focused builder, descriptor conformance, config, distribution, and
    scenario tests, then run `task all`.

## Acceptance

- Every built machine receives deterministic distribution bootstrap derived from
  the project topology.
- A standalone deployment starts as a one-member distribution cluster.
- Two scenario machines form one cluster without dynamic discovery.
- Public HTTP does not start when distribution is unavailable.
- No old platform subsystems beyond the narrow distributed-data concepts are
  copied.
- Per-machine service redundancy remains absent.
- `task all` passes.

## Risks and controls

- Fixed topology IPs may not be bindable on a development host. Runtime overrides
  must be explicit and covered by tests.
- Distributed integration tests can leak listeners. Register cleanup immediately
  after each member starts and use bounded contexts.
- Olric logs can make scenario output noisy. Route them through controlled
  platform logging at an appropriate level.
