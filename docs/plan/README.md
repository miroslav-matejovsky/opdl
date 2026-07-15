# Client and service registration plan

This plan delivers the first real OPDL platform use case: clients and services
register a unit identity through REST, and any platform machine can return the
distributed registration list.

## Scope

The first release provides:

- `POST /registrations` to request registration of one unit.
- `GET /registrations/{unit_type}/{unit_id}/status` to check a request on the
  same platform machine that received it.
- `GET /registrations` to return every registration request and its current
  process state as JSON.
- A unique registration key composed of `unit_type` and `unit_id`.
- Informational `unit_type_name_advertised` and optional `role` fields.
- Server-derived `machine` and `ip` fields identifying the platform machine that
  owns the registration.
- Distribution of registration state through a platform fabric whose peer
  bootstrap is derived from the deployment topology.
- Generated OpenAPI artifacts and a generated .NET SDK for all three operations.
- Platform events for registration phases, conflicts, and fabric lifecycle.
- Single-machine and multi-machine black-box scenarios that use the SDK, REST,
  and event records to verify behavior.

Authentication and authorization are intentionally absent. The endpoints are
anonymous until a later platform phase adds both concerns across the public API.

## Fixed first-release decisions

These decisions make the stage instructions executable without adding design
work during implementation:

- A `POST` request accepts only `unit_type`, `unit_id`,
  `unit_type_name_advertised`, and `role`.
- Registration views returned by status and list add origin `machine`, origin
  `ip`, overall `status`, and per-platform-instance status. POST has no success
  body.
- `machine` and `ip` come from the receiving platform instance's embedded
  deployment descriptor through `config.Descriptor()`. They never come from the
  request, the HTTP listener address, or a client-supplied header.
- `unit_type` is an integer from 0 through 255.
- `unit_id` is an integer from 0 through 65535.
- `unit_type_name_advertised` is required and must not be blank.
- `role` may be absent or `null` in a request. It is omitted from a response when
  absent. A present value must be exactly `Master` or `Slave`.
- `POST /registrations` creates a registration request and returns
  `202 Accepted` with no success body. It does not mean the unit is registered.
- The client checks `GET /registrations/{unit_type}/{unit_id}/status` on the same
  platform machine. Status is `pending`, `accepted`, or `rejected` and contains
  no generated request id.
- The composite `(unit_type, unit_id)` key locates the request. A deterministic
  internal content fingerprint may correlate confirmations, but it is never part
  of the public API and is not a client request id.
- A request becomes `accepted` only after every platform instance declared in the
  site's embedded fabric topology has recorded acceptance through the fabric.
  Current connectivity or member count must not reduce the required set.
- `GET /registrations` returns pending, accepted, and rejected requests. Each item
  has the same overall state returned by the single-key status endpoint.
- Each returned item contains `platform_instances`, sorted by machine. It includes
  every expected site platform instance with its descriptor machine/IP, state
  `pending`, `accepted`, or `rejected`, and an optional bounded reason. A missing
  confirmation appears as pending.
- The origin `machine` and `ip` still identify where the client submitted the
  request. They are separate from the platform-instance progress array.
- Repeating the exact request through the same platform machine is idempotent. It
  returns `202`; the status endpoint reports the existing state and no duplicate
  request event is produced.
- Registration is create-only. For an existing pending or accepted key, only an
  exact match of advertised name, role, machine, and IP is an idempotent retry.
  Any mismatch returns `409 Conflict`, preserves existing state, and emits
  `platform.registration.conflict` tagged `warning`.
- Conflict therefore includes a second service requesting the same key on the
  same machine with a different advertised name or role, as well as a request
  from another machine or IP.
- Without a client request id, authentication, or another service identity, a
  byte-for-byte identical second service on the same machine is indistinguishable
  from an idempotent retry. This is an explicit first-phase limitation.
- Changing metadata or moving a registration requires explicit removal first.
  Removal, update, and relocation workflows are deferred to a later phase.
- `GET /registrations` returns a top-level JSON array. It returns `[]` when
  no request exists. Results are sorted by `machine`, `unit_type`, then
  `unit_id`. Each item's `platform_instances` are sorted by machine.
- List enumeration is not a collection-wide transaction. A list concurrent with
  writes may observe entries from immediately before or after those writes, but
  every returned entry must be complete and valid.
- `(unit_type, unit_id)` is the only unique key across the whole site-local
  platform fabric. `machine` and `ip` are stored metadata and never participate
  in identity or uniqueness.
- Fabric data is shared only within one site. `site` is therefore unnecessary in
  each registration record. Cross-site sharing and uniqueness enforcement are
  not part of this use case.
- `platform/internal/fabric` is the abstraction for cross-machine platform
  distribution. Registration depends only on its narrow named-collection
  capability and never on Olric, memberlist, transport endpoints, or backend
  iterators.
- The first fabric implementation is an embedded Olric adapter under the fabric
  package. `_opdl-v1/platform/internal/distdata/doc.go` explicitly deprecates the
  old `distdata` package, so do not port it or make new code depend on it. Reuse
  only the proven Olric lifecycle and map-operation ideas inside the adapter.
- The first fabric increment does not copy the broad v1 messaging fabric. It
  provides only lifecycle, static member identity, connection state, atomic
  named-collection operations, and enumeration required by registration and its
  confirmations. Pub/Sub, request/response, filters, locks, chaos integration,
  the old registry, authority, leases, service catalog, and redundancy remain
  outside this use case.
- Each platform instance runs a small registration reconciler. It immediately
  scans pending requests at startup and periodically thereafter, validates each
  request, and records its own confirmation in the fabric. The interval is
  configurable and injected in tests. No fixed sleeps are used.
- There is no quorum, timeout, or automatic rejection for an unavailable
  platform instance. The request remains pending until every expected instance
  confirms or one explicitly rejects it.
- Registration data is in memory. Full-platform shutdown loses it. Persistence
  is a later use case.
- One platform process and one fabric member run per deployment machine.
  Per-machine platform service redundancy is not implemented.
- Events are compact JSONL records written synchronously to a configured
  directory and flushed per record. No public events REST endpoint is added in
  this use case.

## Stage sequence and estimate

Estimates are focused engineer time. They include code, tests, documentation,
generated artifacts, and review fixes. They exclude external review wait time.

| Stage | File | Estimate | Depends on |
| --- | --- | ---: | --- |
| 1. Contract generation foundation | [01-contract-generation-foundation.md](01-contract-generation-foundation.md) | 3-5 days | None |
| 2. Local registration vertical slice | [02-local-registration-vertical-slice.md](02-local-registration-vertical-slice.md) | 5-7 days | Stage 1 |
| 3. Platform events | [03-platform-events.md](03-platform-events.md) | 3-5 days | Stage 2 |
| 4. Platform fabric foundation and Olric adapter | [04-platform-fabric-foundation.md](04-platform-fabric-foundation.md) | 7-10 days | Stage 3 |
| 5. Two-phase registration over the fabric | [05-registration-over-fabric.md](05-registration-over-fabric.md) | 6-9 days | Stage 4 |
| 6. Multi-machine SDK scenarios and hardening | [06-scenarios-and-hardening.md](06-scenarios-and-hardening.md) | 4-6 days | Stage 5 |
| **Total** | | **28-42 days** | |

Stages should land in order. Each stage must leave `task all` passing. Generated
OpenAPI and SDK changes belong in the stage that changes the contract, not in a
later cleanup.

## Deferred work

Do not expand these stages to include:

- Authentication, service identity credentials, or authorization policy.
- Registration leases, heartbeats, explicit removal, relocation, or expiry.
- Confirmation quorum, request expiry, automatic timeout, or forced acceptance
  while an expected platform instance is unavailable.
- Durable storage, restart replay, backup, or disaster recovery.
- Per-machine primary/secondary platform service instances.
- Active/passive role election or fencing.
- Dynamic topology discovery.
- Cross-project registration sharing.
- Cross-site registration sharing or uniqueness enforcement.
- Pagination or filtering of the registration list.
- Fabric Pub/Sub, request/response, streaming, service catalog, or capability
  advertisement.

Record newly discovered work outside this scope in the root `.todo` only when it
blocks completion. Otherwise place follow-up items in `docs/backlog`.
