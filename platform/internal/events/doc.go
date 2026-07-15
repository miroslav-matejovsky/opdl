// Package events is the platform's domain event mechanism: the envelope, the
// recorder, and the sink contract. It owns no events of its own.
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
//   - internal/registration: requested, confirmed, accepted, rejected, conflict.
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
//   - Sequence: a monotonic counter within one process run. It restarts at 1 on
//     every run, by design, and totally orders events within one timestamp.
//   - OccurredAt: when the fact happened, in UTC.
//   - Source: the subsystem that emitted the event.
//   - Tags: optional sorted, duplicate-free markers, omitted when empty.
//
// The envelope carries no node identity. Which process an event is about is a
// Node, and it is constant for a whole process run, so a sink is free to state
// it once instead of on every record: the jsonl sink names its file after the
// node. A sink that pools events from several nodes must carry Node per record
// instead.
//
// # Storage
//
// A Recorder stamps an event and appends it to a Sink. Recording is
// synchronous: Record returns only once the sink has accepted and flushed the
// record. A scenario that has read an HTTP response has therefore already been
// able to observe every event that response produced. Package jsonl is the
// file-backed Sink; NopRecorder discards everything and is used when no events
// directory is configured.
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
