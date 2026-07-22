# Unified platform event model

Status: complete. All nine stages are implemented and `task all` passes.

## Result

The platform has one event model and one serialized wrapper:

- `events.Event` is the payload contract, and it is one method, `EventType`.
- `events.Envelope` is the only serialized wrapper. Local output and the site
  journal carry the same shape, so a reader never selects a model before
  decoding.
- `events.Factory` stamps every envelope, once per process, from the deployment
  descriptor and the instance role.
- Concrete events live in the owning package's `events.go`. There are five:
  `registration`, `eventfabric`, `app`, `redundancy`, and `eventfabric/nats`.
- Business code constructs a typed payload and calls `Publish` or `Record`. It
  supplies no IDs, timestamps, identity, source, severity, causal links, JSON,
  subjects, or attribute maps.

`operations.Event`, `Recorder.Emit`, `events.Meta`, `events.Record`,
`StampRecord`, and adapter-side stamping are gone, with no compatibility layer.

## Documents

1. [Current state](00-current-state.md) describes the duplication that was
   removed.
2. [Canonical model options](01-architecture-options.md) records the selected
   wrapper shape.

## Stages

| Stage | File | Outcome |
| --- | --- | --- |
| 10 | [Canonical model](10-canonical-model.md) | `Event`, `Envelope`, `Origin`, `Type`, `Severity` and the optional interfaces |
| 20 | [Envelope factory](20-envelope-factory.md) | `Factory.Wrap` and context-carried causal links |
| 30 | [Domain event catalogs](30-domain-event-catalogs.md) | registration and Event Fabric catalogs moved into `events.go` |
| 40 | [Journal pipeline](40-journal-pipeline.md) | publisher and appender split; deliveries carry envelopes |
| 50 | [Application events](50-application-events.md) | 20 typed `platform.app.<fact>` events; `Recorder.Record` |
| 60 | [Redundancy events](60-redundancy-events.md) | 6 typed `platform.redundancy.<fact>` events |
| 70 | [NATS events](70-nats-events.md) | 22 typed `platform.nats.<fact>` events |
| 80 | [Remove duplicate model](80-remove-duplicate-model.md) | the second wrapper and its API deleted |
| 90 | [Documentation and validation](90-documentation-validation.md) | docs reconciled, full gate green |

## Where the invariants are documented

`platform/internal/events/doc.go` is the reference: payload ownership, event type
convention, defaults, envelope fields, stamping, causal links, and origin. The
architecture overview states the same invariants at system level in
[docs/01-architecture.md](../../01-architecture.md).
