# Stage 1 findings: discovered issues and inconsistencies

These are the unknowns, inconsistencies, and deferred decisions surfaced while
implementing [Stage 1](01-event-model.md). They are recorded for analysis before
the later stages act on them. None blocks Stage 1; each is either a decision made
and worth confirming, or an open question the plan leaves to a later stage.

## Sequencing inconsistencies in the plan

### 1. The registration catalog cannot be fully replaced in Stage 1

Stage 1 work item 8 says to "replace the current registration storage algorithm
and event catalog with this event flow." That cannot be a literal replacement in
Stage 1:

- There is no Event Fabric to publish through until Stage 2.
- The Olric-backed registration runtime (`store.go`, `reconciler.go`,
  `events.go`) must keep working until Stage 5, and it emits `Requested`,
  `Confirmed`, `Accepted`, `Rejected`, and `Conflict` with different payloads.
- A single Go package cannot declare two `Confirmed` types.

Resolution taken: the new catalog and the pure projection live in a sibling
package, `platform/internal/registration/eventmodel`, defined and fully tested,
while the live Olric flow is untouched. Stage 1 therefore *defines* the
replacement; Stage 3 *promotes* it into the `registration` package and deletes
the collection-based store, per Stage 3 work items 1, 8, and 13.

Follow-up: Stage 3 must delete `eventmodel` as a separate package when it merges
the model into `registration`, so registration events do not end up with two
homes.

### 2. `Meta.Sequence` is removed before its replacement has a producer

Work item 3 removes `Meta.Sequence` and adds `eventfabric.Receipt.Sequence` and
`eventfabric.Delivery.Sequence` "for the JetStream site sequence." The JetStream
sequence has no producer until the Stage 2 adapter exists. Between Stage 1 and
Stage 2 no journal sequence is populated anywhere.

Resolution taken: the live JSONL path now relies on append order and the
time-ordered event ID for ordering, which is strictly stronger than a
per-process counter for a single file. The one scenario that asserted
`accepted.Sequence > requested.Sequence` now asserts recorded position instead.
This is an accepted transitional gap, not a regression: no reader needed the
numeric field that file order does not already provide.

### 3. Receipt, Delivery, and routing are claimed by both Stage 1 and Stage 2

Stage 1 (items 3-4) and Stage 2 (item 2) both place `Receipt`, `Delivery`, and
route construction in `eventfabric`. Stage 1 has now defined the envelope
validation, the route and journal naming, the `Receipt`/`Delivery`/`State`
types, and the `Publisher`/`Fabric`/`Projector`/`Handler` interfaces. Stage 2
adds the NATS adapter, the error values it needs, and the contract tests. The
interface signatures follow Stage 2's stated shape; Stage 2 explicitly reserves
the right to refine them, so treat them as the frozen intent, not the final Go.

## Decisions made that are worth confirming

### 4. Node identity is a nested object, not five top-level fields

Work item 2 lists "project, environment, site, machine, and role identity" as
record fields. They are modeled as a nested `node` object that reuses
`events.Node`, rather than five top-level envelope fields. This keeps the
identity cohesive and reuses the existing type. Confirm this is acceptable for
the journal wire contract before external readers depend on it.

### 5. Schema version is the payload schema version, declared per event

Work item 2 lists "schema version" without saying whose. It is implemented as
the *payload* schema version, declared per event through an optional `Versioned`
interface (default `1`), mirroring the existing `Tagged` pattern. The
alternative reading is a single envelope-format version. If the intended meaning
is the envelope format, this needs revisiting; the current choice lets each event
type evolve its payload independently, which is what a replayable journal needs.

### 6. Causation and correlation are set by the publisher, not the recorder

Work item 2 requires "causation ID and optional correlation ID" on every record.
Causation is a property of the handling context — the ID of the event a handler
was processing when it produced a new one — not of the payload. So the envelope
carries both fields, but the plain `events.Recorder` leaves them empty; the
Event Fabric publisher will set them from the delivery a handler is processing in
Stages 2-3.

Two consequences to note:

- In Stage 1 nothing populates these fields; they exist and round-trip, but are
  always empty until handler-driven publication lands.
- "On every record" cannot be literal for causation: an event that begins a
  chain has no cause, so its causation ID is correctly empty. The requirement is
  read as "on every record that has a cause."

### 7. Event types are `platform.*`, routes are `opdl.*`

Event types keep the existing `platform.<domain>.<fact>` convention, but routes
are `opdl.<site-scope>.event.<domain>.<fact>` and the journal is
`OPDL_<UPPER_SITE_SCOPE>_EVENTS`. The route parser strips the `platform` prefix
and the route builder adds the `opdl` transport namespace. This matches the plan
(item 4 parses `platform.*`; the README route format is `opdl.*`), but the dual
prefix is a subtlety: the product line is named `platform` in event types and
`opdl` in the transport namespace and descriptor (`Descriptor.Platform` is
`"opdl"`). Worth a deliberate confirmation so the two prefixes are not later
"unified" by mistake.

## Open questions left to later stages

### 8. Which machines are "expected" is still the whole platform set

The proposal captures an ordered expected-machine set, and that set is part of
the `proposal_id`, so a historical decision cannot change when a later binary has
a different descriptor. What that set *contains* — every platform machine versus
only service-hosting machines — is a Stage 3 open question that depends on a
possible builder change. Stage 1 keeps the current "every expected machine"
rule; the set's membership is not yet resolved.

### 9. Key release after a rejected selected proposal is unspecified

The projection follows the Stage 3 rule: a node rejecting the claiming proposal
marks it rejected but does not release the key. There is deliberately no event or
reducer path that frees a claimed key. Key release is named as "a separate future
domain event" in Stage 3 but is not designed anywhere. Until it is, a unit key
whose selected proposal was rejected stays claimed and cannot be re-registered.
This needs a decision before registration removal or re-registration is a goal.

### 10. The JSONL sink is now doubly redundant on node identity

Node identity is now stamped on every record *and* encoded in the JSONL file
name. The file name is decorative for identity once the record carries the node.
Stage 2 states the JSONL path is not retained as a second source of truth, so
this redundancy disappears with the sink. Noted only so the redundancy is not
mistaken for a requirement.
