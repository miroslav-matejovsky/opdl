// Package events is the platform's domain event mechanism: the envelope and its
// stamping. It owns no events of its own.
//
// # Domain events, not logs
//
// A platform event is a domain event in the DDD sense: a fact that has already
// happened, stated in the past tense, that other parts of the system and the
// people operating it can rely on. It is not a log line. The difference is not
// cosmetic and drives how they are written:
//
//   - An event is owned by the domain that produced it, not by an observer. It
//     is recorded at the state transition that owns the fact, so an event
//     exists if and only if that fact happened. An attempt that changes nothing
//     records nothing; a retry that repeats a request records nothing new.
//   - An event is a published contract. Its type name, payload fields, and
//     meaning are read from outside the process by operators, scenarios, and
//     eventually other machines. Changing one is changing an external API.
//   - An event is self-contained. It carries the data a reader needs to
//     understand the fact, so a reader never has to query the platform to
//     interpret it. Where that means repeating a few fields, they are repeated.
//   - An event is immutable and complete when recorded. Nothing amends it later.
//
// Event names read as facts: platform.<area>.<fact>, past tense, e.g.
// "platform.registration.accepted".
//
// # Where the events are
//
// Each package that produces events declares them in its own events.go, with
// the package's full list documented at the top of that file. There is no
// central catalog: an event belongs to the domain that can state the fact, and
// keeping the two together is what stops the catalog from drifting away from
// the behavior it claims to describe. Today that is:
//
//   - internal/registration: proposed, confirmed, rejected, accepted.
//   - internal/eventfabric: ready, stopping.
//
// A package declares an event by implementing Event: a small struct of payload
// fields that knows its own Type and the Source subsystem it comes from. An
// event that marks an operational anomaly also implements Tagged, usually with
// TagWarning.
//
// # Envelope
//
// Emitters supply only the payload. The Recorder stamps the envelope (Meta) and
// stores the two together as one Record:
//
//   - ID: unique per occurrence and time-ordered.
//   - Type: the event's stable dotted kind.
//   - SchemaVersion: the version of the payload schema, positive on every
//     record. An event declares its own with the Versioned interface, otherwise
//     it is DefaultSchemaVersion. It lets a reader that replays a journal reject
//     a payload encoding it does not understand.
//   - OccurredAt: when the fact happened, in UTC.
//   - Source: the subsystem that emitted the event.
//   - Node: the deployment identity of the process that stated the fact.
//   - CausationID and CorrelationID: the causal links between events, set by the
//     Event Fabric when a handler's reaction produces a new event and empty on
//     an event the plain recorder stamps.
//   - Tags: optional sorted, duplicate-free markers, omitted when empty.
//
// The envelope deliberately carries no transport ordering. A shared journal
// orders events when it accepts them; that sequence is a property of the
// delivery, not of the immutable fact, and lives on the Event Fabric's receipt
// and delivery rather than in the record.
//
// # Node identity is on every record
//
// Which process an event is about is a Node. It is constant for a whole process
// run, so the Recorder is told this machine's Node once and stamps it onto
// every record. Carrying it per record is what lets a shared journal pool the
// events of every node in one ordered stream and still attribute each fact to
// its origin.
//
// # Storage
//
// This package does not store events. StampRecord turns an event into a
// complete, self-describing record, and the Event Fabric appends it to the site
// journal — synchronously, so a fact is retained before the operation that
// caused it returns. The journal is the platform's only event storage.
//
// The Recorder, Sink, and jsonl file sink predate the journal and no runtime
// path uses them; see docs/plan/05-remove-distributed-state.md.
//
// # Limitations
//
// Event delivery is best effort for this phase. A Record call that fails after
// the emitting domain has already changed its state returns an error with
// context; there is no rollback across a store and a sink, and the caller is
// expected to surface the failure rather than retry. Recording holds a lock
// across the sink write, so events are serialized against each other; the
// volume this stage emits does not justify a queue. There is no publish or
// subscribe side: events are recorded for readers outside the process, and
// nothing in the platform reacts to them yet.
package events
