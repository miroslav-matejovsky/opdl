# Stage 2: Instance-aware fabric

Estimate: 6 person-days.

## Objective

Make fabric membership distinguish `machine/primary` from
`machine/secondary` while preserving the existing collection abstraction. Prove
that acknowledged state survives loss of one platform process.

## Implementation steps

1. Extend `internal/fabric.Member` with the closed platform instance identity.
   Define one canonical member ID, such as `machine/instance`, for keys, logs,
   comparisons, and diagnostics. Keep machine and instance available as
   separate fields.
2. Change `MembersFromDescriptor` to return every configured instance in the
   site. Mark only the process-selected local instance as `Self`. Sort by
   machine and then primary before secondary.
3. Keep the `Fabric` and `Collection` capability surface unchanged unless a
   concrete requirement proves otherwise. `Collection`, `Members`, `State`, and
   `Close` remain the semantic boundary.
4. Change Olric default configuration to consume explicit descriptor endpoints.
   Remove fixed-port derivation as a production source. Retain address helpers
   only where useful for builder resolution or validation.
5. Map live Olric members by explicit client endpoint to canonical platform
   instance ID. A primary and secondary on one IP must never overwrite each
   other in address or machine-keyed maps.
6. Build join seeds from every other expected platform instance, including the
   sibling on the same machine. Exclude only the selected local instance.
7. Update reachability and state calculation to count instances. State is
   operational fabric reachability and must not be reused as registration
   voting policy.
8. Update the in-memory adapter and shared contract fixtures so two instances
   of one machine can open independent fabric handles over shared collections.
9. Run the Stage 0 Olric durability spike. Configure the minimum supported
   replica setting and any write/read quorum settings needed for one-process
   loss. Capture actual behavior for graceful leave, hard kill, restart, and
   membership rebalance.
10. If Olric cannot meet the approved acknowledged-state guarantee, stop this
    stage. Record the observed failure and revise the architecture and estimate.
    Do not hide the gap behind retry logic.
11. Update fabric lifecycle event payloads and logs to name machine and instance
    separately. Keep adapter metadata and collection semantics unchanged.
12. Update `doc.go` files and the measured membership limitation. Explicitly
    state what replication does and does not survive.

## Tests

- A primary-only one-machine site has one member and is connected by itself.
- A default one-machine site has two expected members with unique IDs and one
  `Self` per process.
- A mixed site with one redundant and one primary-only machine derives three
  identical ordered members from every descriptor.
- Both same-machine Olric members bind explicit, distinct endpoints and share a
  collection.
- Live-member mapping reports both instances instead of collapsing by machine.
- An unexpected member is ignored by expected-membership views.
- Hard-killing either instance does not lose a write acknowledged before the
  kill, within the guarantee approved in Stage 0.
- Restarting the same instance identity rejoins and reads current state.
- Existing create/swap ownership, cancellation, enumeration, and close contract
  suites continue to pass.
- The known create-if-absent join regression remains covered with the expanded
  membership.

## Fabric change limit

This stage is semantically significant but intentionally narrow. It may change
member identity, descriptor-to-adapter configuration, replication settings, and
tests. It must not add leader election, platform failover methods, service
routing, publish/subscribe, transactions, shared memory, or service roles to the
fabric API.

## Unresolved implementation detail

Olric replica placement may protect against any one member loss without placing
a copy specifically on the sibling instance. That is sufficient for the
recommended process-loss guarantee while the site remains running. It is not a
guarantee that each machine locally owns every value. If local paired placement
is required, Olric may be the wrong backend and the scope must be revised.

## Exit criteria

- Fabric membership uniquely identifies both instances of a machine.
- The collection interface remains materially unchanged.
- Descriptor endpoints, not hard-coded runtime ports, drive production Olric
  configuration.
- The approved single-process-loss state guarantee has an automated test.
- Limitations are documented without implying whole-machine durability.
- `task all` passes.
