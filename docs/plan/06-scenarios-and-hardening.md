# Stage 6: Multi-machine SDK scenarios and hardening

Estimate: 4-6 engineer-days.

## Goal

Complete consumer-facing and black-box proof of the first use case. The final
scenario must drive built platform binaries through the generated .NET SDK,
verify the all-instance registration barrier and cross-machine process overview,
and use platform events as evidence.

## Instructions

1. Review all generated OpenAPI and .NET artifacts after Stage 5. Confirm the
   contract contains only:

   - `POST /registrations`.
   - `GET /registrations/{unit_type}/{unit_id}/status`.
   - `GET /registrations`.

   Verify numeric bounds, required fields, optional role, separate request and
   response schemas, bounded path parameters, status enum, origin machine/IP,
   nested platform-instance progress, POST 202, JSON content types, operation ids,
   and the top-level list array.

2. Finalize the .NET end-to-end tests around the generated client. Accept two
   platform base URLs from the scenario environment. Through SDK calls:

   - Start with node A while node B is still offline.
   - List from node A and verify the initial empty array.
   - POST a role-less request on node A and verify the call completes with 202,
     not a registration response.
   - Fetch status by unit key on node A and verify `pending`.
   - List on node A and verify the request is visible as pending. Its
     `platform_instances` must eventually show A accepted and offline B pending.
   - After the Go harness starts node B, eventually observe `accepted` by polling
     status on node A only.
   - List from both nodes and verify overall accepted, node A's origin machine/IP,
     and accepted instance entries for both A and B.
   - Verify status lookup on node B returns 404 for node A's request.
   - Repeat the exact POST on node A and verify status remains accepted.
   - POST the same key with a different advertised name on node A and require the
     documented 409 conflict.
   - POST the same key on node B and require 409 conflict.
   - Verify both lists still contain one unchanged accepted view and both instance
     confirmations.
   - Pass cancellation tokens to every async operation.

   Keep validation error coverage in Go HTTP tests unless Kiota exposes a useful
   generated error model that adds real consumer value.

3. Refactor `scenarios/dotnet_sdk_e2e_test.go` into a reusable two-node harness
   if Stage 4 and Stage 5 setup has become duplicated. The harness must:

   - Build both machine packages through the builder CLI.
   - Allocate collision-free API and fabric adapter ports.
   - Write one runtime config and events directory per machine.
   - Start node A first with isolated captured output while keeping node B
     intentionally offline for the pending assertion.
   - Start node B only after the SDK test signals that pending behavior is proven,
     using a deterministic file handshake rather than sleep: pass a temporary
     control directory to the .NET process, have the C# test create a
     `pending-observed` marker after its pending and empty-list assertions, and
     start node B when the Go harness observes that marker.
   - Start the .NET test process asynchronously so the Go harness can service the
     marker handshake while C# continues polling status.
   - Wait on observable readiness rather than sleep.
   - Stop processes and join output-copy goroutines before reading buffers.
   - Always clean up both processes when startup or the SDK test fails.

4. Pass both live base URLs to the .NET test process. Require the test summary to
   report executed passing tests, not skipped tests. Keep the generated client as
   the only API access path inside the C# test.

5. After the SDK operations complete, parse both synchronously flushed JSONL
   files before process cleanup. Assert:

   - One `platform.registration.requested` on node A.
   - One matching `platform.registration.confirmed` from node A and one from node
     B.
   - `platform.registration.accepted` occurs only after both confirmations.
   - Two `platform.registration.conflict` events tagged `warning`: one for the
     changed same-machine payload and one for the cross-machine attempt.
   - No extra requested, confirmed, or accepted event for the exact retry.
   - Event payloads contain the correct unit key, origin machine/IP, confirming
     machines, and bounded conflict details.
   - The final API list projection matches the confirmation events for A and B.
   - Event node envelopes match the blueprint topology.
   - Both nodes emitted `platform.fabric.started`.

   Force-stop child processes during cleanup where the operating system cannot
   portably deliver a graceful interrupt. Verify `platform.fabric.stopped`
   and dependency close order in in-process lifecycle tests instead.

6. Add failure diagnostics to the scenario harness. On failure, report both
   process outputs, both event file paths or contents, allocated addresses, and
   the .NET test output. Keep success output quiet.

7. Run the scenario repeatedly and with the Go race detector on focused packages
   where possible. Remove timing assumptions revealed by repetition. Use bounded
   `Eventually` polling and include the last observed error in failures.

8. Update documentation in the same change:

   - Root `README.md`: architecture and end-to-end request flow.
   - `api-specifications/README.md`: registration contract generation.
   - `sdk-dotnet/README.md`: current layout and three-operation usage example.
   - Platform package docs: runtime composition, registration, fabric, and
     events.
   - Scenario package docs: two-node SDK and event verification.
   - Deployment docs: topology-derived addresses and runtime overrides.

9. Remove stale mock root-status endpoint files, generated models, tests,
   comments, and documentation. Search the repository for `platform is running`,
   `mock endpoint`, and old root-route assumptions without removing the new
   registration request-status model. Do not remove unrelated mock deployment
   data required for a clean standalone build.

10. Review dependency and architecture boundaries:

    - `platform/api` contains only public contract descriptions and wire models.
    - HTTP depends on the registration use case, not directly on Olric.
    - Registration depends on narrow request, confirmation, active-registration,
      and fabric collection interfaces, not on the Olric adapter.
    - Fabric defines platform distribution semantics.
    - Only `platform/internal/fabric/olric` owns Olric and memberlist details.
    - Scenarios import no internal Go packages from platform or builder.
    - The .NET SDK remains generated and has no hand-written generated-file
      changes.

11. Run the complete final verification:

    - Focused Go tests for all changed packages.
    - Focused `go test -race` where supported.
    - API generation and clean diff review.
    - `dotnet build sdk-dotnet/Opdl.Sdk.slnx`.
    - The two-node scenario several times.
    - `task all` as the final command.

## Acceptance

- The generated .NET SDK registers on one built machine and lists from another.
- POST creates a request, status and list expose pending while an expected node is
  offline, and both expose accepted only after every expected node confirms.
- Every list item shows origin machine/IP and deterministic per-instance progress,
  including missing and rejecting machines for non-accepted requests.
- Returned machine and IP identify the request origin and remain immutable while
  the composite key stays unique across machines.
- Same-machine changed data and cross-machine reuse both return 409, emit
  warning-tagged conflict events, and leave the record unchanged.
- Events, not process logs, prove request, confirmation, acceptance, and conflict
  behavior per topology node.
- Exact retries are idempotent and do not duplicate change events.
- All public documentation describes registration rather than the mock status
  endpoint.
- Deferred authentication, authorization, persistence, leases, and redundancy
  remain clearly out of scope.
- `task all` passes.

## Final delivery statement

At this point the first use case is complete: clients and services can request a
bounded unit identity through anonymous REST, confirm acceptance by unit key on
the origin machine after every site platform instance accepts it, and retrieve
the same registration-process overview from every machine in the fabric.
Generated SDK consumers use all three operations, and black-box scenarios verify
state and events across the deployment topology.
