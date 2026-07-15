# Stage 5: Distributed registration integration

Estimate: 3-5 engineer-days.

## Goal

Replace production use of the local registration store with a distributed store
so a registration accepted by one platform machine is listed by every machine in
the same deployment cluster.

## Instructions

1. Add only the distributed-map operations required by registration to
   `platform/internal/distdata`:

   - Atomic get-and-put or equivalent upsert.
   - Get by key for verification and decoding.
   - Cluster-wide key scan.
   - Map close where required by the backend.

   Keep serialization and registration semantics outside the generic
   distributed-data wrapper.

2. Implement a distributed `registration.Store` adapter. Use a fixed map name
   owned by the registration package. Encode the composite key in one canonical,
   versioned, collision-free string form. Do not use the advertised name or role
   in the key.

3. Serialize values as an explicit versioned JSON record or another stable
   backend-neutral representation. Return errors with map name and composite key
   context, but do not expose internal topology or peer addresses through public
   HTTP errors.

4. Preserve Stage 2 behavior across the distributed backend:

   - First key insertion is created.
   - A changed advertised name or role is updated.
   - An exact retry is unchanged.
   - The accepted registration is returned.
   - List is globally visible and sorted by `unit_type`, then `unit_id`.
   - An empty distributed map returns `[]`.

5. Handle distributed races explicitly. Two simultaneous registrations for the
   same key must not corrupt the value or emit two `created` results. Use the
   backend atomic primitive and add a concurrent integration test. Last accepted
   update wins for informational fields.

6. During list, close iterators on every path and attach key context to corrupt
   record errors. Decide and document fail-fast behavior: one corrupt internal
   record fails the request with 500 rather than silently omitting state.

7. Change production runtime composition to inject the distributed registration
   store into the HTTP registration service after distribution is ready. Keep the
   in-memory implementation for unit tests. Remove any configuration switch that
   would let production silently fall back to process-local registration.

8. Keep event ownership at the API operation that accepted the change. A create
   or update emits once on the accepting machine after the distributed write
   succeeds. A read on another machine does not re-emit the event. Exact retries
   remain event-free.

9. Add multi-member integration tests that start members on dynamic ports and
   prove:

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

11. Document consistency and failure semantics in the registration and
    distribution package docs:

    - State is in memory and not replayed after full shutdown.
    - Results can be temporarily unavailable while the cluster changes.
    - The first use case has no leases or expiry.
    - The API exposes one project-wide logical registry, regardless of which
      machine serves the request.

12. Run focused registration, distribution, race, HTTP, and multi-machine
    scenario tests, then run `task all`.

## Acceptance

- A successful registration is retrievable from another platform machine.
- Upsert and list semantics match the local implementation.
- Registration events occur once on the accepting node for actual changes.
- Production has no process-local fallback.
- No authentication, authorization, lease, persistence, or redundancy behavior
  is added.
- `task all` passes.

## Risks and controls

- A cluster-wide scan may be expensive at large scale. It is acceptable for the
  first use case. Record pagination or indexing as backlog only after measured
  need.
- Atomic upsert support may return the old value in a backend-specific format.
  Hide that detail in the distributed adapter and test create/update races.
- A distribution partition can make list fail. Return a bounded 500 response
  rather than stale process-local data.
