# `utils/testnet`

## Functionality

Reserve a requested number of distinct ephemeral IPv4 loopback TCP addresses for
tests, then release all listeners so the system under test can bind them.

## Extraction source and consumers

- Consolidate `freeAddresses` in `scenarios/harness_test.go` and `freeAddrs` in
  `platform/internal/eventfabric/nats/nats_test.go`.
- Keep NATS configuration and scenario socket roles in their current packages.
- Use the package only from tests unless a production use case appears.

## Package boundary

Accept a context and count. Return a reservation that exposes addresses and a
single idempotent release operation. Hold every listener until the reservation
is complete so returned addresses are distinct. Return errors instead of taking
a `testing.T` or importing `testify`.

Document the unavoidable race after release: the operating system may assign a
released port to another process before the consumer binds it. This helper
guarantees distinct selection, not exclusive ownership after release. Callers
that can accept an already-open listener should keep using a listener directly.

## Tests and documentation

- Reject non-positive counts.
- Return the requested number of distinct loopback host-port addresses.
- Keep listeners unavailable to a second bind until release.
- Release every listener after success, partial allocation failure, and context
  cancellation.
- Make repeated release safe.
- Verify callers can bind all addresses after release.

## Refactoring steps

1. Create and test `utils/testnet`.
2. Replace the NATS test helper.
3. Replace the scenario helper.
4. Keep the role-to-address assignment local to each consumer.

## Completion criteria

Both duplicate helpers are removed, callers retain their existing socket
behavior, allocation failures leak no listeners, and `task all` passes.
