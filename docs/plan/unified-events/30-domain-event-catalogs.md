# Stage 30: organize the existing typed event catalogs

Effort: S-M, about 0.5 to 1 day. Complexity: Low-Medium.

## Goal

Apply the `events.go` convention to the existing registration and Event Fabric
lifecycle events without changing their behavior.

## Registration

Create `platform/internal/registration/events.go` containing:

- event type constants;
- `Proposed`, `Confirmed`, `Rejected`, and `Accepted` payload structs;
- event metadata methods;
- event constructors.

Move non-event identifier data and hashing helpers from `catalog.go` into a
focused file such as `identifiers.go`. Keep domain constants used only by event
payloads in `events.go`. Delete `catalog.go` when it is empty.

Replace `DedupID` with the canonical `StableID` method. `Rejected` declares warn
severity or its warning tag in `events.go`. Normal events use default severity
and schema version unless an explicit version is required by the contract.

## Event Fabric lifecycle

Create `platform/internal/eventfabric/events.go` containing:

- `TypeReady` and `TypeStopping`;
- `Ready` and `Stopping` payload structs;
- constructors and event metadata methods.

Move non-event `Info` to the main fabric contract or a focused `info.go`. Delete
`lifecycle.go` when empty.

## Documentation convention

Each `events.go` begins with a package-local catalog comment listing every event
type and its meaning. Event structs and exported fields remain documented.

Do not introduce a central catalog of concrete events in `internal/events`.

## Tests

- Update existing catalog tests to use the canonical interfaces.
- Verify stable IDs are unchanged for identical registration facts.
- Verify event types, schema versions, severity, and tags.
- Verify constructors still create the same typed payload data.

## Acceptance criteria

- All existing typed event structs are in owner `events.go` files.
- `internal/events` owns no concrete platform event.
- Event definitions contain no storage or distribution terms.
- Registration behavior is unchanged.
- Run registration and Event Fabric unit tests, then `task all` during
  implementation.
