# Stage 2: Instance-aware fabric

Estimate: 4 person-days.

Complexity: medium.

## Objective

Make fabric membership distinguish `machine/primary` from
`machine/secondary` while preserving the existing collection abstraction.
Keep fabric reachability separate from registration voting and durable local
storage.

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
9. Update fabric lifecycle event payloads and logs to name machine and instance
   separately.
10. Update `doc.go` files and the measured membership limitation. Explicitly
    state that fabric reachability and membership do not imply durable state.

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
- Existing create/swap ownership, cancellation, enumeration, and close contract
  suites continue to pass.
- The known create-if-absent join regression remains covered with the expanded
  membership.

## Fabric change limit

This stage may change member identity, descriptor-to-adapter configuration, and
tests. It must not add durable storage, leader election, platform failover
methods, service routing, publish/subscribe, transactions, shared memory, or
service roles to the fabric API.

## Estimate boundary

The estimate covers member identity, explicit endpoint configuration, live
member mapping, adapter contract updates, tests, and documentation. Durable
state is implemented separately in Stage 3.

## Exit criteria

- Fabric membership uniquely identifies both instances of a machine.
- The collection interface remains materially unchanged.
- Descriptor endpoints, not hard-coded runtime ports, drive production Olric
  configuration.
- Fabric limitations are documented without implying state durability from
  membership or reachability.
- `task all` passes.
