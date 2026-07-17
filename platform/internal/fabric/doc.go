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
//   - Stable-membership per-key atomicity. While the site's membership is not
//     changing, Create and Swap are atomic for one key: concurrent Creates of
//     the same key produce exactly one winner, and a Swap reports the value it
//     actually replaced. There is no atomicity across two keys and no
//     transaction: two keys can never be written as one unit. A caller must not
//     extend the stable-membership Create guarantee across a member join; see
//     the membership-change limitation below.
//   - Weakly consistent enumeration. Entries reports the keys it observes while
//     it runs. It is not a snapshot: a write concurrent with an enumeration may
//     or may not appear, and different keys may be observed at different
//     instants. A caller must not infer that a key is absent site-wide from its
//     absence in one enumeration.
//   - Stable membership. Members is the expected membership of the site, taken
//     from the deployment descriptor the machine was built with. A member is one
//     platform instance: a redundant machine contributes two members, its
//     primary and its secondary, told apart by Member.Instance and by the
//     canonical machine/instance ID. Membership is fixed for the process's whole
//     life and identical on every member of the site. It does not shrink when a
//     member is unreachable and does not grow when something unexpected connects:
//     it answers "who belongs here", while State answers "who is reachable now".
//     Nothing about it is discovered at runtime.
//   - State. Connected means every expected member, counting each instance, is
//     reachable; a site of one is connected on its own. Degraded means some but
//     not all are reachable. Disconnected means no peer is reachable. State is
//     operational reachability only: it is not a registration voting policy, and
//     a caller must not read it as one.
//   - Calls after close. Every method returns ErrClosed once Close returns, and
//     a Collection obtained earlier is closed with its Fabric. Close is
//     idempotent.
//   - Context cancellation. A canceled context fails the call with the context's
//     error. Whether a canceled write took effect is not defined: a caller that
//     must know re-reads the key.
//
// # Membership-change limitation
//
// During a member join, Olric can briefly report an existing key as absent to
// Create, allow a false winner, and overwrite the value that was there. Get was
// observed to remain correct in the reproduced window, but callers must not use
// that observation to recover a global create-if-absent guarantee. The fabric
// can therefore contradict a caller that reads a key and then successfully
// creates it.
//
// The adapter uses Olric's API correctly. Olric is an AP store whose atomic
// operations hold "when the cluster is stable", and a join is when it is not.
// This package therefore promises Create's one-winner behavior only for stable
// membership. The shared contract suite covers that stable case; it deliberately
// does not claim to exercise a membership transition.
//
// A caller that needs a site-wide unique claim must preserve every contender and
// reconcile them after membership stabilizes. Registration implements that
// model: an accepted incumbent wins; otherwise the first platform-observed
// contender wins with a deterministic fingerprint tie-break. See
// docs/01-architecture.md for the measurements and rationale. Do not read
// Create as a linearizable site-wide claim primitive.
//
// # Membership and reachability are not durable state
//
// A redundant machine runs two fabric members, its primary and its secondary,
// but the fabric still provides no election, fencing, or failover: the two are
// distinct members that happen to share a machine, not a leader and a standby.
// Membership says who belongs to the site and State says who is reachable now;
// neither says anything about what any member durably holds. A member being
// expected, or even reachable, does not mean a value it once stored still
// exists: this is an in-memory distribution layer, and durability of
// registration state is a separate responsibility that does not derive from
// fabric membership or reachability. If a backend replicates or partitions data
// internally, that is the backend's business and not a platform guarantee.
// Nothing here promises a value survives losing a member, and backend
// replication must not be read as service redundancy.
package fabric
