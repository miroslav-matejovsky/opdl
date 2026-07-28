// Package eventfabric is the site level's event distribution contract: one
// ordered stream of the facts a whole site agrees on, published by any machine
// and read by every machine.
//
// It is the contract only. No implementation lives here yet, and which one
// arrives depends on how a site distributes events at all, which is decided in
// the step 06 ADR: a single-machine site (step 07) can back it with a file,
// while a multi-machine site (step 08) needs something between hosts.
//
// # Who writes and who reads
//
//	instance | write-only               | one process appends, nobody reads back
//	machine  | one writer, one reader   | the Active instance appends, the Passive instance reads
//	site     | many writers, many readers | every machine publishes, every machine reads
//
// The site is the level with more than one writer, which is why it is the level
// that has to define order. Two machines can state a fact at the same moment,
// and the fabric decides which came first.
//
// # What the contract is, and why it is this small
//
// It is exactly what internal/site/registration already needs, and nothing
// else:
//
//   - publish one site-scoped fact. That half is events.Publisher, restated
//     nowhere: a publisher is a publisher, and registration is handed the one
//     the runtime composed.
//   - replay what the stream retained and then follow it live, in one stream,
//     in order, with the sequence that order is expressed as.
//   - at-least-once, with a durable per-consumer position so a process that
//     restarts resumes where its own handler stopped rather than where some
//     other reader did.
//
// Nothing here names a subject, stream, consumer type, or connection. The
// contract was drawn from what registration folds and how its handler reacts,
// so an implementation owes it delivery and ordering and nothing about the
// shape of a transport. If the ADR concludes that durable named consumers are
// the wrong model, Consumer is what it amends.
//
// # Sequence is delivery, not fact
//
// A Sequence is the fabric's order, assigned when it accepted an event. It is
// never on the envelope and never part of an identity: two deliveries of one
// fact are the same fact, which is what makes at-least-once tolerable and what
// registration's deterministic proposal and decision IDs rely on. Compare
// eventstore.Position, which is the same stance one level down.
package eventfabric
