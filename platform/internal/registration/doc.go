// Package registration owns the registration use case: taking a client's
// request to register a unit, deciding it across the machines of a site, and
// projecting the result into the public API models.
//
// It has no HTTP or deployment-loading concerns. It is given a fabric and a
// recorder; its identity, its expected membership, and the origin it stamps on a
// request all come from the fabric's own local member, so a machine can only ever
// answer as itself and a client cannot claim to be somewhere it is not.
//
// # Registration is a site-wide decision, not a local one
//
// A POST creates a proposal and nothing more. The request becomes a registration
// only once every platform instance in the site's static deployment topology,
// the origin included, has recorded acceptance of that exact proposal.
//
// That boundary is the whole point of the design. It is not a quorum and not
// current live membership: the acceptance set comes from the deployment
// descriptor, so an expected machine that is down keeps a request pending rather
// than being dropped from the vote. There is no timeout, no expiry, and no
// forced acceptance. A request can stay pending indefinitely, and that is the
// promise being kept rather than a failure to keep one.
//
// # How it is put together
//
//   - Service takes requests and answers questions. It decides nothing: a
//     client's call must not be able to accept its own registration.
//   - Reconciler is one instance's part in the decision. It answers for itself
//     about every request in the site, and commits the ones this instance
//     originated once the site has agreed. Every instance runs exactly one,
//     nothing is delivered to it, and there is no leader.
//   - store is the state, on three fabric collections shared by the site. See
//     records.go for what is stored and why it is keyed the way it is.
//
// # Why repeating itself is safe
//
// Every write is create-if-absent, and every decision is derived from the site's
// current state rather than from a step in a sequence. A reconciliation pass is
// therefore a correction, not a transition: passes may repeat, overlap a status
// lookup, or resume after a restart, and all reach the same state. This is what
// lets an unreliable schedule be enough, and it is why no pass has to happen for
// the site to stay consistent.
//
// Creating the accepted registration record is the single commit point. There is
// no transaction across the three collections, and none is needed: status is
// accepted if and only if that record exists, so acceptance cannot race the
// confirmations that granted it.
//
// # Create-only
//
// A registration key is claimed once and never updated, moved, or removed. An
// identical repeat is an idempotent retry; every other difference on a claimed
// key is a conflict, including a different advertised name or role from the same
// machine. The key is unique across the whole site fabric and is never scoped by
// machine. Changing a registration will require explicit removal, which is a
// later use case.
//
// Without a caller identity, a byte-for-byte identical second unit on one
// machine is indistinguishable from a retry and is answered as one. That is an
// explicit limitation of this phase: nothing in a request says who is asking.
//
// # What this package states
//
// The domain events are declared and listed in events.go, and each is recorded
// at the transition that owns it, by the instance that owns that transition: a
// first claim is requested, each instance's own acceptance is confirmed, the
// origin's commit is accepted, an instance's refusal is rejected, and a refused
// claim is conflict. Transitions that do not happen produce no event, so a
// retry, a validation failure, and a repeated scan are all silent, which is what
// makes the events evidence of behavior rather than of calls. A recording
// failure surfaces to the caller as the operation's error, and the store is not
// rolled back to match it.
//
// # Not in this phase
//
// State is in memory and is not replayed after a full-site shutdown. There is no
// authentication, authorization, removal, lease, heartbeat, quorum, timeout, or
// persistence. A site's fabric is its own: cross-site uniqueness is not
// enforced, and is not a goal.
package registration
