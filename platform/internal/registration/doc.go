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
//   - store is the state on fabric collections shared by every machine of the
//     site. It retains immutable proposals and repairable current views. See
//     records.go for the storage vocabulary and keys.
//
// # Target contender model
//
// Olric Create is one-winner only while fabric membership is stable. A member
// join can temporarily let a different proposal overwrite a current view, so a
// registration must not use one successful Create as its only proof of a unique
// claim. This package retains every distinct proposal under its fingerprint.
// The reconciler then groups those contenders by unit key and derives one winner:
//
//   - An accepted proposal observed before any competitor is the incumbent and
//     remains the winner.
//   - If no contender is accepted, the earliest platform-observed request time
//     wins.
//   - Equal request times use the proposal fingerprint as a deterministic
//     tie-break.
//
// The request time comes from the receiving platform machine. Machines do not
// coordinate clocks, so "first" is best effort for a true cross-machine race;
// this package makes no linearizable global first-writer claim. That trade-off is
// deliberate for the site's static topology and low expected contention.
//
// Until all contenders are visible, a proposal can be pending or briefly appear
// accepted. Once membership is stable and the contenders have been scanned, the
// winning proposal is the site's registration and every loser is rejected with
// reason registration_key_conflict. Reconciliation repairs current request and
// accepted views to match that winner. A pass remains a correction: repeated,
// overlapping, and restarted passes derive the same final state from retained
// records. This corrects the known join limitation after membership stabilizes.
//
// # Create-only
//
// In the target model, each proposal is immutable and is never removed or
// altered. An identical repeat is an idempotent retry; a different proposal that
// is already visible is refused with 409. During the membership-change window,
// a different proposal can be accepted provisionally and is later rejected by
// reconciliation. The key is site-local and never scoped by machine. Explicit
// removal remains a later use case.
//
// Without a caller identity, a byte-for-byte identical second unit on one
// machine is indistinguishable from a retry and is answered as one. That is an
// explicit limitation of this phase: nothing in a request says who is asking.
//
// # Querying and reporting conflicts
//
// Stage 5 makes GET /registrations list every retained proposal, including
// rejected losers, and adds GET /registrations/conflicts to group the contenders
// for one key and identify the winner and losers. It is a domain query rather
// than a health endpoint: a resolved registration conflict does not make the
// process unavailable. Notifications, acknowledgement, retention, and removal
// are not part of this release.
//
// # What this package states
//
// The domain events are declared and listed in events.go, and each is recorded
// at the transition that owns it, by the instance that owns that transition: a
// first claim is requested, each instance's own acceptance is confirmed, the
// origin's commit is accepted, an instance's refusal is rejected, and a refused
// claim is conflict. Reconciliation can make a provisional transition visible before
// reconciliation corrects it, so events are not the conflict-reporting
// mechanism. Stage 5's query API is authoritative once contenders have
// converged. A recording failure surfaces to the caller as the operation's
// error, and the store is not rolled back to match it.
//
// # Not in this phase
//
// State is in memory and is not replayed after a full-site shutdown. There is no
// authentication, authorization, removal, lease, heartbeat, quorum, timeout, or
// persistence. A site's fabric is its own: cross-site uniqueness is not
// enforced, and is not a goal.
package registration
