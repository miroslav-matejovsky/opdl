# Step 10 - Producer and storage contracts

Effort: 0.5-1 day

Complexity: Medium

## Goal

Introduce the producer-facing API, backend API, and synchronous fan-out without
changing existing producers yet.

## Changes

1. Add the producer interface to `platform/internal/events`:

   ```go
   type Publisher interface {
       Publish(context.Context, Event) error
   }
   ```

2. Add `platform/internal/events/storage` with package documentation and this
   backend interface:

   ```go
   type Backend interface {
       Store(context.Context, events.Envelope) error
       Close(context.Context) error
   }
   ```

3. Add a concrete fan-out publisher in `storage`. Its constructor accepts the
   process `events.Factory` and an ordered, non-empty backend list. It returns a
   value that implements `events.Publisher` and owns backend shutdown.

4. Make `Publish` perform one `Factory.Wrap`, then call every backend with that
   exact envelope. Continue after failures and combine them with `errors.Join`.
   Each backend must add its own useful error prefix such as `jsonl:` or `nats:`.

5. Keep the common API small. Do not add backend names, health, replay, receipt,
   sequence, configuration, or query methods.

6. Close backends in reverse construction order. Attempt every close and return
   all errors. Define and test idempotent close behavior and publication after
   close.

7. Document ordering precisely. Sequential `Publish` calls reach each backend
   in order. Concurrent calls have no defined relative order. Each backend is
   responsible for making its own writes safe for concurrent use.

## Tests

- A typed event is stamped once.
- Every backend receives an equal envelope, including ID, timestamp, origin,
  causation, correlation, tags, stable identity, and raw payload.
- Backends are called in configured order.
- A failed backend does not prevent later backends from being attempted.
- Multiple backend failures remain discoverable with `errors.Is`.
- Invalid events reach no backend.
- Cancellation reaches backend calls.
- Close uses reverse order, joins errors, and is idempotent.
- Publish after close returns a stable closed error.

## Completion criteria

- New packages have `doc.go` files.
- All exported APIs are documented.
- Existing runtime behavior remains unchanged because no producer uses the new
  publisher yet.
- Focused `events` and `events/storage` tests pass.

