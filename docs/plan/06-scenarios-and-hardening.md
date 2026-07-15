# Stage 6: Multi-machine SDK scenarios and hardening

Estimate: 3-5 engineer-days.

## Goal

Complete consumer-facing and black-box proof of the first use case. The final
scenario must drive built platform binaries through the generated .NET SDK,
verify cross-machine distribution, and use platform events as evidence.

## Instructions

1. Review all generated OpenAPI and .NET artifacts after Stage 5. Confirm the
   contract contains only:

   - `POST /registrations`.
   - `GET /registrations`.

   Verify numeric bounds, required fields, optional role, both successful POST
   statuses, JSON content types, operation ids, and the top-level list array.

2. Finalize the .NET end-to-end tests around the generated client. Accept two
   platform base URLs from the scenario environment. Through SDK calls:

   - List from node A and verify the initial empty array.
   - Register a role-less unit on node A.
   - Eventually list it on node B.
   - Register a second unit with `Master` on node B.
   - Eventually list both on node A in deterministic order.
   - Update the first unit to `Slave` and a new advertised name.
   - Verify the update from the other node.
   - Repeat the exact update and verify the operation remains successful.
   - Pass cancellation tokens to every async operation.

   Keep validation error coverage in Go HTTP tests unless Kiota exposes a useful
   generated error model that adds real consumer value.

3. Refactor `scenarios/dotnet_sdk_e2e_test.go` into a reusable two-node harness
   if Stage 4 and Stage 5 setup has become duplicated. The harness must:

   - Build both machine packages through the builder CLI.
   - Allocate collision-free API and distribution ports.
   - Write one runtime config and events directory per machine.
   - Start processes with isolated captured output.
   - Wait on observable readiness rather than sleep.
   - Stop processes and join output-copy goroutines before reading buffers.
   - Always clean up both processes when startup or the SDK test fails.

4. Pass both live base URLs to the .NET test process. Require the test summary to
   report executed passing tests, not skipped tests. Keep the generated client as
   the only API access path inside the C# test.

5. After the SDK operations complete, parse both synchronously flushed JSONL
   files before process cleanup. Assert:

   - One `platform.registration.created` on node A for the first key.
   - One `platform.registration.created` on node B for the second key.
   - One `platform.registration.updated` on the node that accepted the update.
   - No extra registration event for the exact retry.
   - Event payload fields match the final accepted request at each transition.
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
   - `sdk-dotnet/README.md`: current layout and two operation usage example.
   - Platform package docs: runtime composition, registration, fabric, and
     events.
   - Scenario package docs: two-node SDK and event verification.
   - Deployment docs: topology-derived addresses and runtime overrides.

9. Remove stale status endpoint files, generated models, tests, comments, and
   documentation. Search the repository for `Status`, `platform is running`,
   `mock endpoint`, and old root-route assumptions. Do not remove unrelated mock
   deployment data required for a clean standalone build.

10. Review dependency and architecture boundaries:

    - `platform/api` contains only public contract descriptions and wire models.
    - HTTP depends on the registration use case, not directly on Olric.
    - Registration depends on a narrow store and fabric collection interface,
      not on runtime startup or the Olric adapter.
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
- Events, not process logs, prove create and update behavior per topology node.
- Exact retries are idempotent and do not duplicate change events.
- All public documentation describes registration rather than the mock status
  endpoint.
- Deferred authentication, authorization, persistence, leases, and redundancy
  remain clearly out of scope.
- `task all` passes.

## Final delivery statement

At this point the first use case is complete: clients and services can register a
bounded unit identity through anonymous REST, every platform machine sees the
same distributed registry, generated SDK consumers can use both operations, and
black-box scenarios verify state and events across the deployment topology.
