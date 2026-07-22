# Pluggable event storage proposal

Status: proposed for discussion. This is not an implementation plan.

## Goal

Event producers should publish a typed `events.Event`. They should not know
whether the resulting envelope is written to JSONL, distributed through the
Event Fabric, or stored by another backend added later.

Every process must store every event in local JSONL under its blueprint
`data_dir`. A blueprint may also enable other backends. When it does, the same
canonical `events.Envelope` is sent to every configured backend.

## Current problems

- `operations` is a local JSONL writer with a broad and misleading name.
- Local events use `operations.Recorder`, while site events use
  `eventfabric.Publisher`.
- Producers therefore choose storage and failure behavior themselves.
- The NATS implementation combines storage, distribution, replay, projection,
  handler delivery, and transport lifecycle.
- `data_dir` currently means the NATS JetStream directory. It cannot also be the
  JSONL directory without defining a safe layout below it.

## Recommended direction

1. Put the producer-facing `Publisher` in `internal/events`.
2. Put a small `Backend` interface and the fan-out publisher in
   `internal/events/storage`.
3. Implement mandatory local storage in `internal/events/storage/jsonl`.
4. Keep Event Fabric as the abstract ordered distribution and replay
   capability.
5. Implement the Event Fabric with NATS in `internal/events/storage/nats`. The
   NATS type implements both the common storage write interface and the richer
   Event Fabric interface. It is registered once, not once as NATS and again as
   Event Fabric.
6. Treat each instance's blueprint `data_dir` as its storage root. Derive the
   JSONL and NATS directories below it.
7. Start with synchronous fan-out. It is sufficient for the POC and does not
   introduce an outbox, checkpoints, or background retry workers.

This recommendation intentionally does not create a concrete
`storage/eventfabric.Backend` wrapper around a concrete `storage/nats.Backend`.
That wrapper would only forward the same envelope and would make it possible to
register the same destination twice.

## Proposed package layout

```text
platform/internal/events
  Event, Envelope, Factory, Publisher

platform/internal/events/storage
  Backend, fan-out publisher

platform/internal/events/storage/jsonl
  mandatory local Backend

platform/internal/events/storage/eventfabric
  Fabric, Delivery, Projector, Handler, health and ordering contracts

platform/internal/events/storage/nats
  NATS implementation of storage.Backend and eventfabric.Fabric
```

The path `storage/eventfabric` describes a capability, not a second physical
destination. The path `storage/nats` describes the current implementation of
that capability.

## Documents

- [Producer and storage contracts](01-contracts.md)
- [Package layout alternatives](02-package-layout.md)
- [Blueprint alternatives](03-blueprint.md)
- [Multi-backend delivery semantics](04-delivery-semantics.md)

## Decisions needed before a detailed plan

- Accept or reject `events/storage` and the `Backend` name.
- Decide whether Event Fabric is an abstract capability implemented by NATS, or
  a concrete wrapper around a separate NATS backend.
- Accept or reject `data_dir` as an instance storage root with derived child
  directories.
- Accept synchronous fan-out for the POC, including its partial-write behavior.
- Decide whether producer-facing publication should return no storage position.

