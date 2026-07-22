# Stage 10: define the canonical event model

Effort: M, about 1 to 2 days. Complexity: Medium.

## Goal

Add the single target event contract to `internal/events`. Keep it independent
from local storage, NATS, and Event Fabric delivery.

This stage is additive. The old wrappers remain temporarily so later stages can
migrate one pipeline at a time.

## Target types

Define:

- `Event`, requiring only `EventType() Type`;
- `Envelope`, the only target serialized wrapper;
- `Origin`, containing deployment and process identity;
- `Severity`, with `info`, `warn`, and `error` values;
- optional `Versioned`, `Severe`, `Tagged`, and `Identified` interfaces.

`Identified` uses domain terminology such as `StableID() string`. It must not use
transport terminology such as deduplication key or message ID.

## Event type convention

Validate every type as `platform.<source>.<fact>`:

- exactly three non-empty tokens;
- `platform` is the fixed prefix;
- source and fact contain only stable lower-case tokens and underscores;
- fact is a completed fact, not a command.

The envelope's `Source` is derived from the middle token. Concrete events do not
implement a repetitive `Source()` method.

Existing typed events already fit this form. Operational event names will be
normalized when their owning package is migrated.

## Default behavior

- schema version defaults to `1`;
- severity defaults to `info`;
- tags default to absent;
- stable identity defaults to absent;
- tags are trimmed, deduplicated, sorted, and omitted when empty;
- timestamps are UTC;
- JSON payload must be valid and non-empty;
- all required origin fields are validated.

Events implement optional interfaces only when they differ from a default. A
normal informational event therefore needs one metadata method only:

```go
func (Accepted) EventType() events.Type { return TypeAccepted }
```

## Files

- Update `platform/internal/events/event.go` or split the model into focused
  files while keeping package documentation in `doc.go`.
- Add or update table-driven tests in `platform/internal/events`.
- Do not change business packages or adapters yet.

## Tests

Cover:

- valid and invalid event types;
- default schema and severity;
- optional interface overrides;
- stable identity validation;
- tag normalization;
- envelope JSON round trip;
- missing origin and payload fields;
- invalid JSON payload;
- UTC occurrence time.

## Acceptance criteria

- The canonical types compile without importing `operations`, `eventfabric`, or
  NATS.
- A normal event definition requires only `EventType`.
- No new generic metadata or payload map exists.
- The old wrappers are marked temporary in internal comments, not deprecated as
  public compatibility APIs.
- Run focused `internal/events` tests, then `task all` during implementation.
