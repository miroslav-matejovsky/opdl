# Stage 50: define and migrate application events

Effort: L, about 3 days. Complexity: Medium-High.

## Goal

Replace application-owned operational event strings and attribute maps with
typed payloads in `platform/internal/app/events.go`.

## Event catalog

Define typed events for the current application facts, grouped in one file:

- process started and stopped;
- status directory creation failed;
- ownership lock open failed;
- API listen failed, listening, active, and stopped;
- standby waiting, open retry, and ready;
- projection lag exceeded and projection caught up;
- status write failed;
- site opening, open failed, ready, stopping, and stopped;
- background Event Fabric loop stopped.

Use normalized types in `platform.app.<fact>` form. Exact fact tokens should
remain close to the current names, for example `platform.app.process_started`.

## Payload design

Replace every attribute map with explicit fields. Examples:

- failures carry `Error string` plus the relevant typed path, address, phase, or
  duration field;
- API events carry address and instance state;
- projection events carry applied sequence, high-water sequence, phase, lag
  bound, and duration where relevant;
- process events carry configuration or operations-file data that is not already
  in the envelope;
- site events carry active or ready state and duration where relevant.

Do not repeat origin, process role, PID, timestamp, source, severity, or type in
payloads. The factory supplies them.

Normal transitions use default info severity. Retry and degradation events
declare warn. Failures declare error in `events.go`.

## Recorder API used by call sites

Add or use a typed local method shaped as:

```go
observer.Record(ctx, SiteOpening{Active: active})
```

It accepts only context and an `events.Event`. It does not accept type, level,
component, message, or attributes.

The recorder needs the `events.Factory` to stamp what it writes. Stage 40
composes that factory in `internal/app` and passes it to the Event Fabric
publisher only; this stage passes the same instance to the recorder.

## Readability rules

- Prefer direct struct literals for payloads with one or two obvious fields.
- Add constructors only when they validate, normalize, or derive meaningful
  event data.
- Do not create a constructor that only copies parameters into fields.
- Keep event metadata methods beside the payload type.
- Use domain field names, not generic `Value`, `Details`, or `Attributes` names.

## Tests

- Test event metadata and non-default severity as a table.
- Update app tests to decode canonical envelopes from the local recorder.
- Verify representative failure payloads retain error details and relevant
  context.
- Verify call sites contain no raw event type strings or attribute maps.

## Acceptance criteria

- `internal/app` owns a complete documented `events.go` catalog.
- Application call sites emit typed payloads only.
- Common metadata is absent from payload structs.
- No application `Recorder.Emit` call remains.
- Run app unit and integration tests, then `task all` during implementation.
