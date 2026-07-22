# Stage 60: define and migrate redundancy events

Effort: M, about 1 to 2 days. Complexity: Medium.

## Goal

Replace redundancy-owned operational event strings and attribute maps with typed
payloads in `platform/internal/redundancy/events.go`.

## Event catalog

Define typed events for:

- ownership lock opened;
- ownership waiting;
- ownership acquired;
- activation started;
- activation failed;
- activation completed.

Use `platform.redundancy.<fact>` event types.

## Payloads

Use explicit fields for:

- Windows object name;
- whether the object already existed;
- whether ownership was abandoned;
- activation kind;
- activation duration;
- failure details.

Do not repeat deployment identity, process role, PID, timestamp, source, or
severity. Do not expose the old generic operations attribute constants.

An abandoned ownership acquisition declares warn severity. Activation failure
declares error. Normal ownership and activation transitions use default info.

## Emission ownership

Keep events at the transition that knows the fact. Where app currently reports a
failure returned before redundancy can construct a transition, keep that failure
as an app event. Do not move behavior across package boundaries only to move an
event definition.

## Tests

- Verify type, source derivation, severity, and payload JSON for every event.
- Update ownership tests to use the typed recorder path.
- Verify abandoned and ordinary acquisitions remain distinguishable.
- Verify activation failure preserves the original error string and duration.

## Acceptance criteria

- `internal/redundancy/events.go` is the complete redundancy event catalog.
- No redundancy `Recorder.Emit` call or attributes map remains.
- Ownership behavior and Windows mutex handling are unchanged.
- Run redundancy tests, then `task all` during implementation.
