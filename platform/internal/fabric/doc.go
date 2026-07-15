// Package fabric is the platform's distribution boundary: the one abstraction
// through which platform code shares state across the machines of a site.
//
// A consumer depends on the Fabric interface and never on a backend. Which
// backend carries the data, how members find each other, and what the wire looks
// like are decided by runtime composition, not by the code using it. That is the
// point of the boundary: the backend is replaceable, and no domain package has
// to change when it is replaced. Package olric is today's production adapter and
// package memory is the in-process one; neither is the platform's API.
//
// # The only capability is a named collection
//
// A Collection is a named map of string keys to byte values, shared by the
// members of one site. That is deliberately the whole first release: it is what
// distributed registration needs, and a smaller surface is a smaller promise to
// keep across adapters. There is no publish, subscribe, request, or handler
// here yet, and no query, index, or transaction. Values are opaque bytes; the
// fabric never interprets them, so encoding stays the caller's decision.
//
// # Semantics every adapter owes a caller
//
// These are platform promises, not descriptions of a backend. An adapter that
// cannot keep one is not a valid adapter, and the shared contract test suite
// exists to hold every adapter to them.
//
//   - Byte ownership. Create and Swap copy what they are given, so a caller may
//     reuse or modify its slice the moment the call returns. Get, Swap, and
//     Entries return copies the caller owns and may modify freely; mutating one
//     never changes what the fabric holds.
//   - Per-key atomicity. Create and Swap are atomic for one key: concurrent
//     Creates of the same key produce exactly one winner, and a Swap reports the
//     value it actually replaced. There is no atomicity across two keys and no
//     transaction: two keys can never be written as one unit. See the known
//     limitation below: the Olric adapter does not keep this promise for a
//     moment after a member joins.
//   - Weakly consistent enumeration. Entries reports the keys it observes while
//     it runs. It is not a snapshot: a write concurrent with an enumeration may
//     or may not appear, and different keys may be observed at different
//     instants. A caller must not infer that a key is absent site-wide from its
//     absence in one enumeration.
//   - Stable membership. Members is the expected membership of the site, taken
//     from the deployment descriptor the machine was built with. It is fixed for
//     the process's whole life and identical on every member of the site. It
//     does not shrink when a member is unreachable and does not grow when
//     something unexpected connects: it answers "who belongs here", while State
//     answers "who is reachable now". Nothing about it is discovered at runtime.
//   - State. Connected means every expected member is reachable; a site of one
//     is connected on its own. Degraded means some but not all are reachable.
//     Disconnected means no peer is reachable.
//   - Calls after close. Every method returns ErrClosed once Close returns, and
//     a Collection obtained earlier is closed with its Fabric. Close is
//     idempotent.
//   - Context cancellation. A canceled context fails the call with the context's
//     error. Whether a canceled write took effect is not defined: a caller that
//     must know re-reads the key.
//
// # Known limitation: create-if-absent does not survive a join
//
// The per-key atomicity above is a promise this package makes and the Olric
// adapter currently breaks. For a brief window after a member joins, while Olric
// moves partition fragments to it, Create can report an existing key as absent,
// win, and overwrite the value that was there. Get stays correct throughout, so
// the fabric can contradict itself: a caller may read a key and then
// successfully create it.
//
// The adapter uses Olric's API correctly. Olric is an AP store whose atomic
// operations hold "when the cluster is stable", and a join is when it is not, so
// this is a gap between what this package promises and what that backend can
// give. It is not reproduced by the memory adapter, and the contract suite does
// not catch it because every case there runs against one stable member.
//
// A caller that needs a key claimed exactly once, as registration does, is
// exposed only when a machine joins a site that already holds data. See
// docs/backlog/fabric.md for the measurements, the reproduction, and why there
// is no cheap fix. Do not read the promise above as currently true on Olric.
//
// # No redundancy
//
// A machine runs exactly one fabric member, and the platform provides no
// election, fencing, or failover on top of it. If a backend replicates or
// partitions data internally, that is the backend's business and not a platform
// guarantee: nothing here promises a value survives losing a machine. Do not
// read backend replication as service redundancy.
package fabric
