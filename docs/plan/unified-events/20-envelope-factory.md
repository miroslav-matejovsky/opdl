# Stage 20: add automatic envelope construction

Effort: M, about 1 to 2 days. Complexity: High.

## Goal

Create one helper that turns a typed payload into a complete envelope. Keep all
technical metadata out of business call sites.

## Factory

Add an `events.Factory` composed once by the application runtime. Its production
constructor receives the deployment descriptor and process role. It captures the
fixed origin and supplies PID, clock, and ID generation internally.

The main operation is conceptually:

```go
func (f Factory) Wrap(ctx context.Context, event Event) (Envelope, error)
```

`Wrap` must populate:

- unique occurrence ID;
- validated event type;
- source derived from event type;
- schema version or its default;
- UTC occurrence time;
- project, environment, site, machine, and machine profile;
- process role and PID;
- severity or its default;
- normalized tags;
- stable identity when declared;
- causation and correlation from context;
- encoded typed payload.

It validates the completed envelope before returning it.

## Causation helper

Move causal metadata ownership to `internal/events`. Provide one narrowly
documented infrastructure helper that attaches a causing envelope to a context.

Rules:

- causation ID is the causing envelope's occurrence ID;
- correlation ID is inherited when present;
- otherwise correlation ID starts with the causing occurrence ID;
- a normal command context has neither field;
- business handlers do not call the helper themselves.

Event Fabric will attach the cause before invoking a handler in stage 40. The
publisher will pass the handler context to `Factory.Wrap`.

## Testability

Keep clock and ID generation replaceable inside the package for deterministic
unit tests. Do not expose clock, PID, or ID parameters in business-facing emit
methods.

Tests outside `internal/events` should assert observable validity rather than
constructing envelope metadata by hand. Add small test helpers only where many
tests need a valid envelope.

## Error handling

Return contextual errors for invalid event metadata, payload encoding, and final
envelope validation. Do not log from the factory. The required publisher or
best-effort local recorder owns the appropriate failure behavior.

## Files

- Add the factory and origin construction to `platform/internal/events`.
- Move ID generation behind the factory while retaining the existing generator.
- Add causation context helpers to `platform/internal/events`.
- Add deterministic factory tests.

## Acceptance criteria

- One call wraps a typed payload without technical arguments from business code.
- Two events receive distinct occurrence IDs and timestamps from the factory.
- Origin is identical for events emitted by the same runtime.
- Cause and correlation flow through a handler context automatically in tests.
- No adapter-specific type appears in the factory.
- Run focused `internal/events` tests, then `task all` during implementation.
