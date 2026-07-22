# Canonical event model decision

Decision: option A is selected.

All options keep event payload structs in each owning package's `events.go`.
They differ only in how the single serialized wrapper represents metadata.

## Option A: structured superset wrapper

Use one wrapper containing the metadata needed by both current models. Replace
generic operational attributes with typed event payload fields.

Illustrative shape:

```go
type Envelope struct {
    ID            string          `json:"id"`
    Type          Type            `json:"type"`
    SchemaVersion int             `json:"schema_version"`
    OccurredAt    time.Time       `json:"occurred_at"`
    Source        string          `json:"source"`
    Severity      Severity        `json:"severity"`
    Origin        Origin          `json:"origin"`
    CausationID   string          `json:"causation_id,omitempty"`
    CorrelationID string          `json:"correlation_id,omitempty"`
    Tags          []string        `json:"tags,omitempty"`
    StableID      string          `json:"stable_id,omitempty"`
    Data          json.RawMessage `json:"data"`
}

type Origin struct {
    Project        string `json:"project"`
    Environment    string `json:"environment"`
    Site           string `json:"site"`
    Machine        string `json:"machine"`
    MachineProfile string `json:"machine_profile"`
    ProcessRole    string `json:"process_role"`
    PID            int    `json:"pid"`
}
```

`Source` is derived from the validated `platform.<source>.<fact>` event type.
`SchemaVersion` defaults to `1`. `Severity` defaults to `info`. Event payloads
override only non-default values through small optional interfaces. `StableID`
is the domain-stable identity of a fact when one exists; adapters may use it for
idempotency without asking the payload for transport-specific data.

Human-readable messages are not part of the wrapper. Specific event structs
carry the typed data needed to understand the fact.

Pros:

- all common metadata is typed and consistent;
- no generic metadata map is needed;
- local and distributed records have the same JSON shape;
- process identity is available for operational events;
- existing journal metadata maps directly to the new wrapper.

Cons:

- domain events also carry process role and PID;
- the wrapper changes when a new truly common metadata field is required.

## Option B: minimal wrapper with operational context in payloads

Keep the current journal metadata as the wrapper. Put severity, process role,
PID, and other operational context into each operational event payload.

Pros:

- the wrapper remains small;
- domain events do not carry process-specific metadata;
- operational and domain-specific fields are clearly separated.

Cons:

- every operational event repeats the same fields;
- filtering by severity or process requires decoding event-specific payloads;
- event constructors need access to process identity;
- common metadata can become inconsistent between event types.

## Option C: minimal wrapper with an extensible metadata map

Keep the current journal metadata and add a generic metadata map for severity,
process role, PID, and future fields.

Pros:

- the wrapper can gain metadata without structural changes;
- operational payloads do not repeat common context.

Cons:

- field names and values are not type safe;
- validation becomes convention based;
- this preserves the main weakness of operational attributes;
- different writers can produce incompatible metadata for the same concept.

## Selected approach

Option A is selected.

It gives every event one complete, typed, self-describing wrapper. It removes
both `operations.Event` and the `Meta` plus `Record` split. It also removes
untyped operational attributes without moving infrastructure choices into event
payloads.

The canonical model should follow these rules:

1. `internal/events` defines `Event`, `Envelope`, `Origin`, `Type`, `Severity`,
   stamping, validation, and encoding.
2. `Envelope` is the only serialized wrapper.
3. Concrete events remain small typed payload structs owned by their package.
4. Each owning package lists its complete event catalog in `events.go`.
5. `Event` requires only `EventType`. Source and common defaults are derived.
   Optional interfaces declare a non-default schema version, severity, tags, or
   stable identity.
6. One platform-level stamper creates the envelope before a storage or
   distribution adapter receives it.
7. Adapters may add delivery data outside the envelope. They do not change the
   event occurrence.
8. `operations` becomes a local envelope writer. It no longer defines an event
   model.

## Expected package changes

- `internal/events`: replace `Meta` and `Record` with `Envelope`; add structured
  origin and severity.
- `internal/registration/events.go`: move the registration event catalog from
  `catalog.go`.
- `internal/eventfabric/events.go`: move lifecycle event structs from
  `lifecycle.go`.
- `internal/app/events.go`: define typed process, API, site, status, projection,
  and standby events currently emitted as strings and maps.
- `internal/redundancy/events.go`: define typed ownership and activation events.
- `internal/eventfabric/nats/events.go`: define typed NATS adapter events.
- `internal/operations`: serialize the canonical envelope to stderr and JSONL.
- `internal/eventfabric`: carry the canonical envelope in deliveries.

Options B and C remain here only as the decision history. Detailed work follows
option A.
