# Pluggable event storage implementation plan

Status: accepted design, ready for implementation.

This directory is temporary. Remove it after all steps are complete and the
final validation passes. Do not link it from permanent project documentation.

## Accepted design

- Producers depend on `events.Publisher` and publish only a typed
  `events.Event`.
- `events.Publisher.Publish` returns only an error. It exposes no storage
  receipt or journal sequence.
- `events/storage.Backend` accepts an already stamped `events.Envelope`.
- One publisher stamps each event once and synchronously sends the same envelope
  to every configured backend.
- JSONL is mandatory and is always the first backend.
- All configured backends are required. Fan-out attempts every backend and
  returns all failures. It does not provide transactions or rollback.
- Event Fabric remains the ordered distribution, replay, projection, handler,
  and health capability.
- NATS implements both `storage.Backend` and `eventfabric.Fabric`. It is
  registered once.
- Backwards compatibility is not required.

## Accepted blueprint shape

Use `jetstream_store_dir` for the NATS-specific directory. This is more precise
than `data_dir`, `nats_data_dir`, or `store_dir`: it names the NATS subsystem
that owns the files and explains why the directory must not be shared.

```hcl
platform {
  data_dir = "D:/opdl/customer-a/north/sensor/primary"

  event_storage {
    eventfabric {
      nats {
        client_port         = 4222
        cluster_port        = 6222
        jetstream_store_dir = "D:/opdl/customer-a/north/sensor/primary/eventfabric/nats"
      }
    }
  }
}
```

`platform.data_dir` is the instance's general persistent data root. The JSONL
backend derives this path:

```text
<platform.data_dir>/events/events.jsonl
```

The `events` subdirectory prevents event files from colliding with other data
the platform may store under the root later.

`jetstream_store_dir` is explicit. It may be inside `platform.data_dir`, as in
the example, or on another volume. The builder validates that two deployed
instances on one machine do not share either their platform data root or their
JetStream store.

A deployed standby uses the same structure inside its `standby` block, with its
own `data_dir`, ports, and `jetstream_store_dir`.

## Target package layout

```text
platform/internal/events
  Event, Envelope, Factory, Publisher

platform/internal/events/storage
  Backend, synchronous fan-out publisher

platform/internal/events/storage/jsonl
  mandatory local Backend

platform/internal/events/storage/eventfabric
  Fabric, Delivery, Projector, Handler, health and ordering contracts

platform/internal/events/storage/nats
  storage.Backend and eventfabric.Fabric implementation
```

## Plan order

| Step | Work | Effort | Complexity |
| --- | --- | --- | --- |
| 10 | Producer and storage contracts | 0.5-1 day | Medium |
| 20 | Mandatory JSONL backend | 0.5-1 day | Medium |
| 30 | Blueprint and descriptor storage model | 1-1.5 days | High |
| 40 | Move Event Fabric contracts | 0.5-1 day | Medium |
| 50 | Move and adapt the NATS backend | 1-2 days | High |
| 60 | Compose and manage all backends | 1-1.5 days | High |
| 70 | Migrate every event producer | 1-2 days | High |
| 80 | Remove legacy paths and update documentation | 0.5-1 day | Medium |
| 90 | Full validation and plan removal | 0.5 day | Low |

Estimated total: 6.5-11.5 developer days. The range includes test and generated
contract updates, but not deployment migration because compatibility is out of
scope.

## Steps

- [10 - Producer and storage contracts](10-contracts.md)
- [20 - Mandatory JSONL backend](20-jsonl-backend.md)
- [30 - Blueprint and descriptor storage model](30-blueprint.md)
- [40 - Move Event Fabric contracts](40-eventfabric.md)
- [50 - Move and adapt the NATS backend](50-nats-backend.md)
- [60 - Runtime composition](60-runtime-composition.md)
- [70 - Producer migration](70-producer-migration.md)
- [80 - Legacy removal and documentation](80-cleanup.md)
- [90 - Validation and plan removal](90-validation.md)

