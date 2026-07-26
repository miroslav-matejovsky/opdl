// Package events is the platform's event mechanism: the one event contract, the
// one serialized wrapper, and the stamping that connects them. It owns no events
// of its own.
//
// # Facts, not logs
//
// A platform event is a fact that has already happened, stated in the past
// tense, that other parts of the system and the people operating it can rely on.
// It is not a log line. The difference is not cosmetic and drives how they are
// written:
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
// Some facts are domain events in the DDD sense — a registration was accepted —
// and some are facts about a process — a client lost its connection. They are
// one model. What differs is not the shape but the destination and what a
// failure means, which is the next section.
//
// # Where an event goes
//
// A producer states a fact through one Publisher and knows nothing else. It
// passes a payload and a context; the publisher stamps the envelope and stores
// it. Which backends exist, in what order, and what they are is composition's
// business, and a producer cannot see it or choose it.
//
// A domain fact goes to a publisher that reaches the site journal, and a failure
// fails the operation that caused it: an event nobody retained is a fact that
// did not happen as far as the site is concerned.
//
// A fact about one process goes to a publisher that reaches only the local
// record. It has to: connection loss, journal unavailability, and projection
// failure are exactly the conditions an operator needs to see, so describing
// them must not depend on the journal being reachable.
//
// Every publisher carries this package's Envelope to every backend, so an
// operator decodes one shape wherever an event is read, and no backend has any
// say in what an event is.
//
// # When a publication fails
//
// Publish returns an error and nothing else. What that error means is the
// caller's to decide, and the platform applies one policy:
//
//   - a domain operation propagates the failure;
//   - a startup or shutdown path returns or joins it, because a process that
//     could not state what it did has not started or stopped cleanly;
//   - a path that cannot return one — a background loop, a transport callback —
//     states through BestEffort, whose Diagnostic reports the failure to the
//     process error stream;
//   - nothing discards one silently, and no failure is restated as an event
//     through the pipeline that just failed.
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
// delivery, not of the immutable fact, and lives on the Event Fabric's delivery
// rather than in the envelope. A producer never sees it: Publish returns an
// error and nothing else.
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
// decides what that means, because this package cannot. Required paths return or
// join it; callbacks that cannot return use BestEffort to report it to stderr.
// Nothing here logs by itself.
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
// self-describing envelope; the storage subpackage fans that envelope out to the
// backends composition configured, which is always the mandatory local JSONL
// record first and, for a publisher that reaches the site, the Event Fabric
// after it.
//
// The journal is the platform's only event storage in the sense that matters to
// behavior: a running service reconstructs state from the retained site journal
// and from nothing else. Local files are for the operator of one process and are
// never read back by the platform.
//
// Encode and Decode are the one JSON representation of an envelope. Encode
// validates before it writes, so an event nothing could replay never reaches
// storage.
package events
