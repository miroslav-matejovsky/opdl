// Package events is the platform's domain event mechanism: the one event
// contract, the one serialized wrapper, and the stamping that connects them. It
// owns no events of its own.
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
// # Event types
//
// Every event type is platform.<source>.<fact>: the fixed platform prefix, the
// subsystem that owns the fact, and what happened, e.g.
// "platform.registration.accepted". Type.Validate enforces the shape, and each
// token is lower-case words joined by single underscores so one fact has
// exactly one spelling for its whole life.
//
// The fact reads as something completed, not as something requested:
// "accepted", not "accept"; "site_opened", not "open_site". No check can
// enforce that, so it is a review rule.
//
// The type is the single declaration of both the kind and its source. Source is
// derived from the middle token, so an event never restates it.
//
// # Declaring an event
//
// Each package that produces events declares them in its own events.go, with
// the package's full list documented at the top of that file. There is no
// central catalog: an event belongs to the domain that can state the fact, and
// keeping the two together is what stops the catalog from drifting away from
// the behavior it claims to describe.
//
// An event is a small struct of payload fields implementing Event, which is one
// method. Everything else is derived or defaulted, so a normal informational
// event is declared in full by:
//
//	func (Accepted) EventType() events.Type { return TypeAccepted }
//
// The defaults are: schema version DefaultSchemaVersion, severity
// DefaultSeverity, no tags, and no stable identity. An event that differs from
// one of them implements the matching optional interface, and nothing more:
//
//   - Versioned declares a payload schema version other than the default,
//     which an event does when its payload encoding evolves.
//   - Severe declares a severity, which an event does when the fact deserves
//     more than routine attention.
//   - Tagged declares markers such as TagWarning.
//   - Identified declares the fact's domain-stable identity, meaning the value
//     that makes a restatement of the same fact the same fact.
//
// # Envelope
//
// Emitters supply only the payload. The envelope is stamped for them, and
// Envelope is the platform's only serialized wrapper: local files, the site
// journal, and any later distributor carry the same JSON shape, so a reader
// never has to work out which writer produced an object before decoding it.
//
// It carries the occurrence identity and time, the type, the derived source,
// the schema version, the severity, the origin, the optional causal links,
// tags, and stable identity, and the payload as raw JSON. Envelope.Validate
// checks all of it before an event leaves the process.
//
// The envelope deliberately carries no transport ordering. A shared journal
// orders events when it accepts them; that sequence is a property of the
// delivery, not of the immutable fact, and lives on the Event Fabric's receipt
// and delivery rather than in the envelope.
//
// # Stamping
//
// Factory is the one stamper. The runtime composes it once with NewFactory,
// from the deployment descriptor the platform booted with and the local role of
// the process, and Wrap turns a payload into a validated envelope:
//
//	envelope, err := factory.Wrap(ctx, Accepted{ProposalID: id})
//
// Business code passes a payload and a context and nothing else. It never
// supplies identity, clocks, IDs, JSON, or defaults, which is what makes an
// origin unforgeable and every envelope consistent: an emitter cannot state a
// machine it is not, because it never states one at all.
//
// Wrap reports an error rather than stamping a partial envelope. The caller
// decides what that means, because this package cannot: a required publication
// fails the operation it belongs to, while a best-effort local record does not.
// Nothing here logs.
//
// # Causal links
//
// CausationID and CorrelationID say which event's handling produced this one
// and which workflow both belong to. They are infrastructure, carried on the
// context: the Event Fabric calls WithCause once before it invokes a handler,
// and Wrap reads the links from the handler's context.
//
// Business handlers never attach or read them. A handler states facts, and what
// caused it to run is not one of them.
//
// # Origin is on every envelope
//
// Which process an event came from is an Origin: the deployment identity down
// to the machine, plus the local process role and PID. It is constant for a
// whole process run and is stamped onto every envelope. Carrying it per event
// is what lets a shared journal pool the events of every node in one ordered
// stream and still attribute each fact to its writer.
//
// The deployment fields name a machine, which is a domain identity. The process
// role and PID are operational identity: they say which of that machine's
// processes wrote the event and belong in an operator's view only. Domain
// behavior stays machine-scoped, so registration counts one voter per machine
// however many processes it runs. Folding the role into domain identity would
// turn one machine into two voters, which is why the two halves are documented
// apart even though they travel together.
//
// # Storage
//
// This package does not store events. It turns an event into a complete,
// self-describing envelope, and the Event Fabric appends it to the site
// journal — synchronously, so a fact is retained before the operation that
// caused it returns. The journal is the platform's only event storage, and a
// running service has exactly one event source: the retained site journal.
//
// # Migration
//
// legacy.go holds the previous wrapper (Node, Meta, Record, StampRecord, and
// the exported NewID they need) while the pipelines are moved onto Envelope and
// Factory one at a time. It is temporary scaffolding, not a compatibility
// surface: nothing new is built on it and it is deleted once the migration
// completes. See docs/plan/unified-events.
package events
