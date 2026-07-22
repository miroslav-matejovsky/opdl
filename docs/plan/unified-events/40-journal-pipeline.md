# Stage 40: carry canonical envelopes through Event Fabric

Effort: L, about 3 to 4 days. Complexity: High.

## Goal

Move envelope construction before the NATS adapter. Event Fabric deliveries and
NATS storage then carry the same completed canonical wrapper used everywhere
else.

## Split publication from append

Keep the business-facing publisher simple:

```go
Publish(ctx context.Context, event events.Event) (Receipt, error)
```

Implement it as a small Event Fabric publisher composed from:

- the shared `events.Factory`;
- an internal appender that accepts `events.Envelope`.

Change the adapter-facing operation conceptually to:

```go
Append(ctx context.Context, envelope events.Envelope) (Receipt, error)
```

The NATS fabric implements append. It no longer receives a payload interface and
does not generate IDs, read clocks, encode payload structs, or set causal fields.

## Delivery

Change `eventfabric.Delivery` to carry `events.Envelope`. Keep journal sequence
on `Delivery`, not in `Envelope`.

Projectors and handlers continue to receive the ordered delivery. Update field
names consistently so code reads `delivery.Envelope.Type` rather than referring
to the removed record wrapper.

## Automatic handler causation

Before Event Fabric invokes `Handler.Handle`, attach the delivery envelope as the
cause in the context. A handler then publishes with the context it already
received:

```go
return publisher.Publish(ctx, consequence)
```

Remove explicit causal-context construction from registration business code.

## Encoding and validation

Move canonical envelope validation and JSON encoding/decoding into
`internal/events`. NATS uses those helpers. Event Fabric retains only routing and
delivery validation that is not part of the event itself.

Route derivation uses `Envelope.Type`. NATS uses `Envelope.StableID` when present
and occurrence `Envelope.ID` otherwise.

## Composition

Create one factory during app startup and pass it to:

- the Event Fabric publisher;
- the local operations recorder.

Registration receives only the business-facing publisher. It does not receive
the factory or appender.

## Removal in this stage

After all journal usages compile against `Envelope`, delete:

- `events.Meta`;
- `events.Record`;
- `events.StampRecord`;
- `events.NewID`, which stage 20 left exported only for hand-built records;
- adapter-side stamping;
- the old explicit causal-context API;
- `registration.Rejected.Tags`, which restates its warn severity only while the
  stored record carries tags but no severity.

Stage 30 already deleted the old Event Fabric `Identified` interface: dedup
would otherwise have stopped working when `DedupID` became `StableID`.

Backward-compatible JSON decoding is not added.

## Tests

- Update publisher fakes to accept typed payloads at the business boundary.
- Update appender fakes to accept completed envelopes.
- Update projector and handler fixtures to build canonical deliveries through
  focused test helpers.
- Prove NATS stores and replays an unchanged envelope.
- Prove receipt ID equals the accepted envelope ID.
- Prove stable identity deduplication still returns the original occurrence ID.
- Prove handler consequences receive causation and correlation automatically.
- Prove malformed envelopes fail before append.

## Acceptance criteria

- NATS never constructs or mutates an envelope.
- Business publication still requires durable journal acceptance.
- Delivery sequence remains outside the event wrapper.
- Registration handler code contains no causal plumbing.
- Only `events.Envelope` exists on the journal path.
- Run Event Fabric, NATS, registration, and app tests, then `task all` during
  implementation.
