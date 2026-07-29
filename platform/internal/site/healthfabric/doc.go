// Package healthfabric carries service health observations between the
// platform instances of one site.
//
// It is the site's second NATS client, beside site/eventfabric, and it is
// deliberately a different one. The event fabric carries facts that are meant
// to be durable and replayable; this carries current state that expires and is
// never written down. Sharing a connection would make the two look like one
// thing, and the first change to either would have to reason about both.
//
// # Why every attempt is published
//
// Delivery is at-most-once and there is no replay. A message that is lost is
// lost, and a subscriber that connected a moment ago has missed everything said
// before it arrived. Publishing the current state after every probe rather than
// only on a change is what repairs both: the next message says everything the
// lost one did, and a late subscriber is current within one probe interval
// without anyone having to replay anything to it.
//
// That is also why nothing here is retained, queued durably, or stored in
// JetStream. A health result that arrived late enough to need replay is a
// result that has expired.
//
// # Publishing never waits
//
// Probing must not be able to stall because the site is unreachable. Publish
// hands the observation to a bounded buffer and returns, and a broker that is
// slow or gone delays nothing on the probe side.
//
// The buffer holds the latest observation per service rather than a queue of
// them, which is what keeps it bounded by the machine's service count instead
// of by how long the outage lasted. Superseding an unsent observation loses
// nothing: the newer one is a complete statement of the same service's current
// state, which is exactly what the older one was. How often it happens is
// counted, because a machine that is constantly superseding is a machine whose
// reports are not getting out.
//
// # Fencing without agreement about time
//
// Every message carries the sender's process-start epoch and a sequence within
// it. A receiver applies one only when it is newer than what that observer's
// slot holds, so duplicates and late arrivals cannot undo newer state, and a
// restarted sender's first message supersedes everything its previous
// incarnation said. The epoch is the process-start count and not the activation
// count: ownership moving does not make an observation newer.
//
// Timestamps travel as diagnostic detail and decide nothing. Freshness is the
// receiver's, measured from arrival, because the machines of a site do not
// share a clock.
package healthfabric
