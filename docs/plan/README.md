# Client and service registration plan

This plan delivers the first real OPDL platform use case: clients and services
register a unit identity through REST, and any platform machine can return the
distributed registration list.

## Scope

The first release provides:

- `POST /registrations` to register or update one unit.
- `GET /registrations` to return all registrations as JSON.
- A unique registration key composed of `unit_type` and `unit_id`.
- Informational `unit_type_name_advertised` and optional `role` fields.
- `role` values limited to `Master` and `Slave` when present.
- Distribution of registration state across platform machines derived from the
  deployment topology.
- Generated OpenAPI artifacts and a generated .NET SDK for both operations.
- Platform events for registration changes and distribution lifecycle.
- Single-machine and multi-machine black-box scenarios that use the SDK, REST,
  and event records to verify behavior.

Authentication and authorization are intentionally absent. The endpoints are
anonymous until a later platform phase adds both concerns across the public API.

## Fixed first-release decisions

These decisions make the stage instructions executable without adding design
work during implementation:

- JSON uses `unit_type`, `unit_id`, `unit_type_name_advertised`, and `role`.
- `unit_type` is an integer from 0 through 255.
- `unit_id` is an integer from 0 through 65535.
- `unit_type_name_advertised` is required and must not be blank.
- `role` may be absent or `null` in a request. It is omitted from a response when
  absent. A present value must be exactly `Master` or `Slave`.
- `POST /registrations` is an idempotent upsert by the composite key. It returns
  `201 Created` for a new key and `200 OK` for an existing key. Repeating the
  exact same body succeeds without producing another change event.
- Updating an existing key may change only the advertised name and role. The key
  remains the identity.
- `GET /registrations` returns a top-level JSON array. It returns `[]` when
  empty. Results are sorted by `unit_type`, then `unit_id`.
- Registration keys are project-wide. The public record contains only the four
  requested fields. The machine that accepted a request is present in the event
  envelope, not in the registration response.
- The first distribution backend is an embedded Olric distributed map, reduced
  from `_opdl-v1/platform/internal/distdata` to the operations required here.
  Do not port Pub/Sub, locks, chaos integration, the old registry, authority,
  leases, service catalog, fabric, or redundancy code.
- Registration data is in memory. Full-platform shutdown loses it. Persistence
  is a later use case.
- One platform process and one distribution member run per deployment machine.
  Per-machine platform service redundancy is not implemented.
- Events are compact JSONL records written synchronously to a configured
  directory and flushed per record. No public events REST endpoint is added in
  this use case.

## Stage sequence and estimate

Estimates are focused engineer time. They include code, tests, documentation,
generated artifacts, and review fixes. They exclude external review wait time.

| Stage | File | Estimate | Depends on |
| --- | --- | ---: | --- |
| 1. Contract generation foundation | [01-contract-generation-foundation.md](01-contract-generation-foundation.md) | 2-3 days | None |
| 2. Local registration vertical slice | [02-local-registration-vertical-slice.md](02-local-registration-vertical-slice.md) | 4-6 days | Stage 1 |
| 3. Platform events | [03-platform-events.md](03-platform-events.md) | 3-4 days | Stage 2 |
| 4. Topology-derived distribution foundation | [04-topology-distribution-foundation.md](04-topology-distribution-foundation.md) | 4-6 days | Stage 3 |
| 5. Distributed registration integration | [05-distributed-registration.md](05-distributed-registration.md) | 3-5 days | Stage 4 |
| 6. Multi-machine SDK scenarios and hardening | [06-scenarios-and-hardening.md](06-scenarios-and-hardening.md) | 3-5 days | Stage 5 |
| **Total** | | **19-29 days** | |

Stages should land in order. Each stage must leave `task all` passing. Generated
OpenAPI and SDK changes belong in the stage that changes the contract, not in a
later cleanup.

## Deferred work

Do not expand these stages to include:

- Authentication, service identity credentials, or authorization policy.
- Registration leases, heartbeats, deregistration, or expiry.
- Durable storage, restart replay, backup, or disaster recovery.
- Per-machine primary/secondary platform service instances.
- Active/passive role election or fencing.
- Dynamic topology discovery.
- Cross-project registration sharing.
- Pagination or filtering of the registration list.
- A general message fabric, service catalog, or capability advertisement.

Record newly discovered work outside this scope in the root `.todo` only when it
blocks completion. Otherwise place follow-up items in `docs/backlog`.
