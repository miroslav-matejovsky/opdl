# Stage 4: Platform fabric foundation and Olric adapter

Estimate: 5-7 engineer-days.

## Goal

Define the platform fabric as the abstraction for cross-machine distribution.
Provide an in-memory adapter for contract tests and an embedded Olric adapter for
production. Derive production peer bootstrap from the authored topology. Do not
move registration state into the fabric until the abstraction, adapters, and
lifecycle are tested.

## Instructions

1. Read these old implementation files and notes before coding:

   - `_opdl-v1/platform/internal/distdata/doc.go`
   - `_opdl-v1/platform/internal/distdata/distdata.go`
   - `_opdl-v1/platform/internal/fabric/doc.go`
   - `_opdl-v1/platform/internal/fabric/fabric.go`
   - `_opdl-v1/docs/roadmap/03-distributed-fabric.md`
   - `_opdl-v1/docs/roadmap/04-cleanup-static-topology.md`
   - `_opdl-v1/docs/roadmap/01-architecture-boundaries.md`

   Treat the `distdata` deprecation as an architectural constraint. Do not create
   a new `distdata` package and do not let registration import Olric. Retain the
   v1 fabric principle that consumers depend on a transport-neutral platform
   capability while runtime composition owns the concrete backend.

2. Create `platform/internal/fabric` with package documentation and a minimal,
   backend-neutral API. The first capability is a named distributed collection:

   - Open a collection by stable name.
   - Atomically swap one byte value by string key and report the previous value
     and whether it existed.
   - Read one value by key.
   - Enumerate the current key/value entries without promising collection-wide
     snapshot isolation.
   - Report `connected`, `degraded`, or `disconnected` state.
   - Drain and close with context.

   Define exact ownership and copy semantics for byte slices, per-key atomicity,
   weakly consistent enumeration, calls after close, and context cancellation.
   Do not expose Olric types, DMaps, iterators, memberlist, or network addresses
   through these interfaces. Do not add Publish, Subscribe, Request, or Handle
   yet.

3. Add an in-process memory adapter under `platform/internal/fabric/memory`. It
   must implement the same collection and lifecycle contracts with concurrency
   safety. Use it for fast unit tests and interface contract tests, not as a
   production fallback.

4. Implement the production adapter under `platform/internal/fabric/olric`.
   Use v1 `distdata.go` only as a reference for proven startup and shutdown
   details. The adapter owns:

   - Embedded Olric construction and readiness timeout.
   - Olric client and memberlist configuration.
   - Translation between fabric collections and DMap operations.
   - Iterator closure and byte-value copying.
   - Member inspection and connection-state calculation.
   - Backend error wrapping and shutdown.

   Pin the selected Olric version in `platform/go.mod`. Start with the version
   proven in `_opdl-v1` unless the current Go version requires a documented
   change. No package outside `fabric/olric` may import Olric.

5. Extend the builder and platform deployment descriptor contracts with a
   resolved fabric topology section. It must identify:

   - This machine's fabric identity and IP address.
   - Deterministically ordered fabric peers in the same project and environment.
   - Peer site, machine, and IP fields for validation and diagnostics.

   Keep the descriptor transport-neutral. The Olric adapter derives its default
   client address on port `3320` and memberlist address on port `3322` from each
   topology IP, matching the proven v1 defaults. Do not add new HCL fields for
   the first use case unless topology data cannot determine a value.

6. Strengthen topology validation before derivation:

   - Machine IP addresses must be unique within one project when fixed platform
     ports are derived from them.
   - A machine must not list itself as a join peer.
   - Fabric peers must remain inside the descriptor's project and environment.
   - Derived Olric client and memberlist addresses must be valid host and port
     pairs.
   - Output ordering must be deterministic regardless of HCL declaration order.

7. Mirror the descriptor change in `builder/deployment` and
   `platform/deployment`. Update descriptor validation, conformance signatures,
   round-trip tests, embedded mock deployment JSON, builder plan output, package
   metadata tests, and all test fixtures. Keep producer and consumer types
   separate as they are now.

8. Extend runtime JSON configuration with optional adapter settings under a
   clearly named `fabric.olric` section: client address, memberlist address, join
   addresses, and readiness timeout. These overrides exist for local development
   and scenarios that run several machines on one host. Production defaults are
   derived from the fabric topology descriptor. Only runtime composition and the
   Olric adapter may consume this configuration. Validate the composed adapter
   configuration before opening listeners.

9. Configure the Olric adapter for the first-use-case constraints:

   - In-memory storage only.
   - No expiry.
   - One platform fabric member per machine.
   - No per-machine primary/secondary service instance.
   - No application-level redundancy, election, or fencing.

   Document whether Olric partitions or copies data internally. These are adapter
   details, not fabric guarantees. Do not describe backend partition ownership as
   platform service redundancy.

10. Wire fabric lifecycle into `platform/cmd/main.go`:

   - Resolve config and open the event sink first.
   - Construct the Olric adapter behind the `fabric.Fabric` interface.
   - Start the fabric before accepting public HTTP traffic.
   - Emit `platform.fabric.started` after readiness, including adapter name only
     as operational metadata.
   - On shutdown, stop HTTP intake, drain and close the fabric, emit
     `platform.fabric.stopped`, then close the event sink.
   - If fabric startup fails, do not start the public API.

11. Write one reusable adapter contract test suite and run it against the memory
    and Olric adapters. Cover atomic swap, get, enumeration, missing keys,
    concurrent same-key access, cancellation, calls after close, state, and
    cleanup. Add focused tests for descriptor derivation, conformance, config
    precedence, invalid addresses, startup cancellation, and clean stop. Use
    dynamic ports in Olric integration tests. Do not use fixed sleeps.

12. Add a two-machine scenario blueprint using distinct loopback IP addresses.
    Build both machine packages and start both with isolated API addresses and
    event directories. For this stage, verify only that both Olric adapters form
    one fabric and emit `platform.fabric.started`. Keep registration on its
    process-local store until Stage 5.

13. Update the root architecture overview and package docs with:

    - Fabric as the stable distribution boundary.
    - Named collections as the only first-release capability.
    - Olric as a replaceable adapter, not the platform API.
    - Descriptor-derived bootstrap, runtime adapter overrides, and ports.
    - Startup order and the current no-redundancy constraint.
    - The v1 `distdata` package is deprecated and intentionally not ported.

14. Run focused builder, descriptor conformance, config, fabric adapter, and
    scenario tests, then run `task all`.

## Acceptance

- Registration and HTTP code cannot import Olric or its configuration types.
- Both memory and Olric implementations pass the same fabric contract tests.
- Every built machine receives deterministic fabric topology derived from the
  project topology.
- A standalone deployment starts as a one-member fabric.
- Two scenario machines form one fabric without dynamic discovery.
- Public HTTP does not start when the production fabric is unavailable.
- No new `distdata` package or dependency is introduced.
- Per-machine service redundancy remains absent.
- `task all` passes.

## Risks and controls

- Fixed topology IPs may not be bindable on a development host. Runtime overrides
  must be explicit and covered by tests.
- A key/value-shaped fabric can become a leaky Olric wrapper. Specify platform
  semantics on the fabric interface and prove them with the memory adapter.
- Fabric integration tests can leak listeners. Register cleanup immediately
  after each member starts and use bounded contexts.
- Olric logs can make scenario output noisy. Route them through controlled
  platform logging at an appropriate level.
