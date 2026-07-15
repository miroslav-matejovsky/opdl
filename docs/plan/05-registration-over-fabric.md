# Stage 5: Registration over the platform fabric

Estimate: 3-5 engineer-days.

## Goal

Implement the registration store over the platform fabric so a registration
accepted by one platform machine is listed by every machine in the same fabric.
Registration must remain independent of the Olric adapter.

## Instructions

1. Confirm Stage 4's fabric collection contract contains only the operations
   registration requires:

   - Atomic swap with the previous value.
   - Get by key.
   - Collection-wide entry enumeration.
   - Context cancellation and close behavior.

   If an operation is missing, extend the backend-neutral collection contract and
   both adapters together. Do not add an Olric-specific escape hatch.

2. Implement `registration.FabricStore` against the fabric collection interface.
   Use a fixed collection name owned by the registration package. Encode the
   composite key in one canonical, versioned, collision-free string form. Do not
   use the advertised name or role in the key.

3. Serialize values as an explicit versioned JSON record or another stable
   backend-neutral representation. Return errors with collection name and
   composite key context, but do not expose fabric topology, peer addresses, or
   adapter errors through public HTTP responses.

4. Preserve Stage 2 behavior across the distributed backend:

   - First key insertion is created.
   - A changed advertised name or role is updated.
   - An exact retry is unchanged.
   - The accepted registration is returned.
   - List is globally visible and sorted by `unit_type`, then `unit_id`.
   - An empty fabric collection returns `[]`.

5. Handle fabric races explicitly. Two simultaneous registrations for the
   same key must not corrupt the value or emit two `created` results. Use the
   fabric's atomic swap contract and add concurrent adapter integration tests.
   Last accepted update wins for informational fields.

6. During list, copy returned entry data before decoding and attach key context to
   corrupt record errors. Decide and document fail-fast behavior: one corrupt
   internal record fails the request with 500 rather than silently omitting
   state. Backend iterator ownership remains inside the adapter.

7. Change production runtime composition to open the registration collection
   after the fabric is ready, construct `registration.FabricStore`, and inject it
   into the HTTP registration service. Keep the process-local registration store
   for focused tests. Remove any configuration switch that would let production
   silently fall back when the fabric is unavailable.

8. Keep event ownership at the API operation that accepted the change. A create
   or update emits once on the accepting machine after the distributed write
   succeeds. A read on another machine does not re-emit the event. Exact retries
   remain event-free.

9. Run the registration store contract tests against the process-local and
   memory-fabric stores. Add multi-member Olric fabric integration tests on
   dynamic ports and prove:

   - Write on member A, get and list on member B.
   - Write on member B, list both records on member A.
   - Composite-key uniqueness across unit types.
   - Metadata update visibility across members.
   - Deterministic sort order.
   - Concurrent same-key registration behavior.
   - Shutdown returns errors instead of hanging in-flight operations.

10. Expand the two-machine Go scenario. Register through node A with raw REST,
    retrieve through node B, update through node B, and retrieve the update
    through node A. Assert created and updated events on the respective accepting
    machines.

11. Document consistency and failure semantics in the registration and fabric
    package docs:

    - State is in memory and not replayed after full shutdown.
    - Results can be temporarily unavailable while fabric membership changes.
    - The first use case has no leases or expiry.
    - The API exposes one project-wide logical registry, regardless of which
      machine serves the request.

12. Run focused registration, fabric, race, HTTP, and multi-machine
    scenario tests, then run `task all`.

## Acceptance

- A successful registration is retrievable from another platform machine through
  the fabric.
- Upsert and list semantics match the local implementation.
- Registration and HTTP packages contain no Olric imports or backend types.
- Registration events occur once on the accepting node for actual changes.
- Production has no process-local fallback.
- No authentication, authorization, lease, persistence, or redundancy behavior
  is added.
- `task all` passes.

## Risks and controls

- Fabric-wide enumeration may be expensive at large scale. It is acceptable for
  the first use case. Record pagination or indexing as backlog only after
  measured need.
- Atomic swap may return the old value in a backend-specific format. The Olric
  adapter must normalize it to the fabric byte contract and test update races.
- A fabric partition can make list fail. Return a bounded 500 response
  rather than stale process-local data.
