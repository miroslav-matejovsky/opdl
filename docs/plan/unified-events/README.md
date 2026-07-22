# Unified platform event model

Status: option A selected. Detailed plan ready for review. No implementation has
started.

## Goal

Replace the two existing event models with one canonical model:

- `internal/events.Event` with `events.Meta` and `events.Record`;
- `internal/operations.Event` written by `operations.Recorder`.

After the refactor:

- the platform has one event payload contract;
- the platform has one serialized event wrapper;
- every package that defines events keeps all of its event structs in
  `events.go`;
- local JSONL, stderr, NATS, and future stores or distributors consume the same
  wrapper;
- storage and distribution behavior is not part of the event model.

## Documents

1. [Current state](00-current-state.md) describes the duplication.
2. [Canonical model options](01-architecture-options.md) records the selected
   wrapper shape.

## Target usage

Business code constructs only its typed payload:

```go
receipt, err := publisher.Publish(ctx, NewAccepted(proposal))
observer.Record(ctx, SiteOpening{Active: true})
```

Business code does not supply IDs, timestamps, deployment or process identity,
source, schema defaults, severity defaults, causal links, JSON, subjects, file
paths, or generic attribute maps.

The common event factory populates that data. Event definitions contain only the
metadata that differs from the defaults, next to their typed payload structs in
the owning package's `events.go`.

## Detailed stages

| Stage | File | Outcome | Effort | Complexity |
| --- | --- | --- | --- | --- |
| 10 | [Canonical model](10-canonical-model.md) | Add the canonical envelope and payload contract | M | Medium |
| 20 | [Envelope factory](20-envelope-factory.md) | Populate common metadata and causal context automatically | M | High |
| 30 | [Domain event catalogs](30-domain-event-catalogs.md) | Move existing typed events into owner `events.go` files | S-M | Low-Medium |
| 40 | [Journal pipeline](40-journal-pipeline.md) | Make Event Fabric and NATS carry completed envelopes | L | High |
| 50 | [Application events](50-application-events.md) | Replace app string/map events with typed payloads | L | Medium-High |
| 60 | [Redundancy events](60-redundancy-events.md) | Replace redundancy string/map events with typed payloads | M | Medium |
| 70 | [NATS events](70-nats-events.md) | Replace adapter string/map events with typed payloads | L | Medium-High |
| 80 | [Remove duplicate model](80-remove-duplicate-model.md) | Delete `operations.Event` and the old emit API | M | Medium |
| 90 | [Documentation and validation](90-documentation-validation.md) | Reconcile docs and run the complete gate | S-M | Low-Medium |

Effort scale: S is up to one engineer-day, M is one to three days, and L is
three to five days.

## POC rule

Backward compatibility is not required. Do not add deprecated aliases, dual JSON
formats, event conversion layers, or readers for the old wrappers. Temporary
internal scaffolding is allowed only while the workspace is migrated and must be
removed by stage 80.
