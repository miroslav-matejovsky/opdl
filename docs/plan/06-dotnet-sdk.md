# Stage 6: .NET SDK redundancy layer

Estimate: 6 person-days.

Complexity: medium.

## Objective

Give .NET consumers one supported client construction path that uses both
platform endpoints and performs bounded automatic failover. Consumers should
not select primary or secondary for individual API calls.

## Recommended design

Keep `sdk-dotnet/src/Opdl.Sdk/Client` generated. Add hand-written SDK code
outside that directory:

- immutable machine endpoint options containing required primary and optional
  secondary base URI;
- a factory that returns the normal generated `PlatformClient` backed by a
  redundant HTTP transport;
- a transport handler that selects the first endpoint, clones a replayable
  request, and retries once on the alternate endpoint when policy permits;
- internal endpoint health/cooldown state only if tests show immediate repeated
  attempts against a dead endpoint create unacceptable latency.

The initial first-attempt policy should be thread-safe round-robin. A
primary-only endpoint set naturally sends all requests to primary.

## Implementation steps

1. Define a small public options type with XML documentation and early
   validation. Require absolute HTTP or HTTPS URIs, reject fragments and query
   strings in base URIs, normalize trailing slashes, and reject duplicate
   endpoints.
2. Ensure the endpoint pair is documented as two instances of the same machine.
   Do not fail over across machine identities because that changes registration
   origin semantics.
3. Implement request routing below the generated client so every current and
   future generated operation receives the same behavior. Do not duplicate all
   generated request builders in a facade unless the transport approach proves
   infeasible.
4. Clone headers, method, URI path/query, options needed by Kiota, and buffered
   content for a possible retry. Dispose failed responses and cloned requests
   correctly.
5. Honor the Stage 0 retry policy exactly. Check caller cancellation before
   failover. Distinguish a caller-canceled token from a transport timeout.
6. Preserve the original exception when no alternate endpoint exists. When both
   attempts fail, throw one documented SDK exception that identifies attempted
   endpoints and retains both failure details without leaking request bodies.
7. Keep the retry count at one. Do not add unbounded loops, sleeps, background
   health probes, or a general resilience framework in the initial version.
8. Make first-attempt selection and any health state safe for concurrent calls.
   Do not mutate Kiota adapter `BaseUrl` per request.
9. Provide dependency injection registration only if the repository already has
   or explicitly adopts a Microsoft DI dependency. Otherwise keep construction
   dependency-free and show a singleton client in documentation.
10. Update SDK README examples to use the redundant factory. Put raw generated
    adapter construction in an advanced single-endpoint section.

## Retry safety

The platform must make same-machine POST replay safe before this stage ships.
The SDK must buffer the small registration JSON body before sending it. A POST
that reached primary but lost its response can then be replayed to secondary as
the same machine-scoped proposal.

Do not retry:

- validation, conflict, authentication, or other 4xx domain responses except
  the explicitly approved transient 408;
- caller cancellation;
- a body that cannot be replayed safely;
- a request to a second machine;
- more than once.

## Tests

- First attempts are distributed across both endpoints over repeated calls.
- Either endpoint can be first; the alternate receives one retry after every
  approved transient failure type.
- 400, 404, 409, 500, and caller cancellation are returned without failover
  unless Stage 0 explicitly changes the list.
- A POST whose first endpoint stores the request and then drops the response is
  retried without creating a conflict or duplicate contender.
- Request path, query, headers, and JSON body are identical after URI authority
  replacement.
- Concurrent calls do not race endpoint selection or alter another request's
  URI.
- Primary-only options work without retry bookkeeping errors.
- Both-endpoint failure reports both attempts and preserves inner errors.
- Generated client regeneration never deletes or overwrites the hand-written
  redundancy layer.
- End-to-end tests stop either process while the same SDK client continues to
  operate.

## Unresolved detail

Endpoint discovery is not recommended initially. The SDK should receive the
primary and optional secondary addresses from the consuming service's deployed
configuration. Adding a discovery endpoint creates bootstrap and trust
questions and is not required when deployment already knows both descriptor
addresses.

If consumers currently receive only one URL, Stage 0 must identify how the
second URL reaches their configuration. Do not make the SDK guess ports.

## Exit criteria

- The documented default SDK path uses both active endpoints.
- Consumers make calls through one generated `PlatformClient` without choosing
  an instance per call.
- Retry behavior is bounded, cancellation-aware, and covered at transport and
  end-to-end levels.
- Generated and hand-written source boundaries are enforced.
- `task all` passes.
