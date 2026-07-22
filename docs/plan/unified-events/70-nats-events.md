# Stage 70: define and migrate NATS adapter events

Effort: L, about 2 to 3 days. Complexity: Medium-High.

## Goal

Replace NATS adapter operational event strings and attribute maps with typed
payloads in `platform/internal/eventfabric/nats/events.go`.

## Event catalog

Define typed events for the existing facts:

- server starting and ready;
- client connected, connect retry, disconnected, reconnected, closed, and
  asynchronous error;
- journal ready, retry, and recovered;
- projector started, reset, attach retry, and stopped;
- handler started, reset, attach retry, and stopped;
- consumer heartbeat missed;
- Event Fabric stopping and stopped.

Use `platform.nats.<fact>` types. Keep NATS-specific data in these payloads
because this package owns the adapter facts. Do not put NATS fields in the common
envelope.

## Payloads

Replace generic maps with explicit fields such as:

- client and cluster addresses;
- server list and routes;
- redacted connected server;
- journal and handler names;
- attempt count;
- elapsed duration;
- next sequence;
- stream sequence;
- hosts-storage and replica values;
- subscription subject when available;
- error string.

Use separate event types where payload meaning differs. Do not preserve a helper
that accepts arbitrary event type and component strings for projector and handler
loop termination.

## Severity

- retries, unexpected disconnects, resets, and missed heartbeats are warn;
- asynchronous errors and failed loop termination are error;
- planned shutdown, successful connection, readiness, and recovery are info.

When one event can be info or error depending on an optional failure, prefer two
typed events if they state different facts. If the fact is genuinely the same,
allow the payload to implement severity from its state and cover it with tests.

## Callback behavior

NATS callbacks continue to use the best-effort local recorder. They must not
publish their own health events through the NATS journal they are diagnosing.
The common wrapper does not change this reliability rule.

## Tests

- Table-test the complete NATS event catalog.
- Verify redacted server values remain redacted.
- Verify retry, disconnect, and failure severities.
- Verify callback event recording remains safe during shutdown.
- Verify no NATS callback passes raw type strings or maps to the recorder.

## Acceptance criteria

- `internal/eventfabric/nats/events.go` is the complete adapter event catalog.
- NATS call sites emit typed payloads only.
- NATS-specific data exists only in NATS payloads or adapter code.
- Local event recording remains independent from the NATS connection.
- Run NATS unit and integration tests, then `task all` during implementation.
