# Current state

## Journal event model

`internal/events` defines:

- `Event`, the typed payload interface;
- `Meta`, the event metadata;
- `Record`, the serialized wrapper containing `Meta` and JSON `Data`;
- event ID generation and wrapper stamping.

The wrapper contains ID, type, schema version, occurrence time, source, machine
identity, causal links, tags, and typed JSON payload.

Registration event structs are in `internal/registration/catalog.go`. Event
Fabric lifecycle event structs are in `internal/eventfabric/lifecycle.go`.

## Operational event model

`internal/operations` defines a second serialized wrapper named `Event`.
`Recorder.Emit` constructs it from an event type string, level, component,
message, and `map[string]any` attributes.

The wrapper contains timestamp, type, level, component, deployment identity,
process role, PID, message, and untyped attributes.

Operational event definitions are spread across call sites in:

- `internal/app`;
- `internal/redundancy`;
- `internal/eventfabric/nats`.

## Problems

- The same fact has two possible wrapper shapes.
- Identity and timestamps use different fields and semantics.
- Operational events are not typed.
- `map[string]any` allows payload shape changes without compiler errors.
- Event definitions cannot be discovered from each package's `events.go`.
- Storage code participates in building the journal wrapper.
- A consumer must know which event model produced a JSON object before decoding
  it.

## Required result

- One `events.Event` payload interface.
- One canonical serialized wrapper.
- One wrapping and validation implementation.
- Typed payload structs for all retained operational events.
- An `events.go` file in every package that owns events.
- Local and distributed writers receive the same canonical wrapper.
- Writer-specific delivery metadata, such as a journal sequence, stays outside
  the wrapper.
