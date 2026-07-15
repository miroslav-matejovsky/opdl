package registration

import (
	"context"
	"errors"
	"fmt"
	"net"
	"strings"
	"time"

	"github.com/miroslav-matejovsky/opdl/platform/api"
	"github.com/miroslav-matejovsky/opdl/platform/internal/events"
	"github.com/miroslav-matejovsky/opdl/platform/internal/fabric"
)

// Rejection reasons are bounded machine-readable codes stating why one platform
// instance refused a proposal. They are a published contract: they appear on the
// status endpoint and in rejected events, so a client can act on them without
// parsing prose.
const (
	// ReasonUnsupportedVersion means the record was written by an encoding this
	// instance does not understand.
	ReasonUnsupportedVersion = "unsupported_record_version"
	// ReasonInvalidProposal means the proposal's own fields are not valid.
	ReasonInvalidProposal = "invalid_proposal"
	// ReasonFingerprintMismatch means the record's fingerprint is not the one
	// its content produces, so the record cannot be trusted to be what it says.
	ReasonFingerprintMismatch = "fingerprint_mismatch"
	// ReasonUnknownOrigin means the proposal claims an origin that is not an
	// expected platform instance of this site.
	ReasonUnknownOrigin = "unknown_origin"
	// ReasonAcceptedKeyConflict means the key already holds a different accepted
	// registration.
	ReasonAcceptedKeyConflict = "accepted_key_conflict"
)

// DefaultInterval is how often a reconciler scans the site when the platform's
// configuration does not say. It is a latency choice, not a correctness one:
// scanning more often accepts requests sooner, and scanning never would leave
// every request pending forever.
const DefaultInterval = time.Second

// Reconciler is one platform instance's part in site-wide registration.
//
// Every instance runs exactly one. It answers for itself and commits for the
// requests it originated, and it does both by looking at the site's current
// state rather than by being told: nothing is delivered to it, no instance
// coordinates the others, and there is no leader.
//
// # What a pass does
//
// For each request in the site it records this instance's decision, once, under
// the proposal's fingerprint. For each request this instance originated, it then
// commits the registration if every expected instance has accepted that exact
// proposal. A request that is already committed is settled for good and is left
// alone, so a pass costs what the site has still to decide rather than what it
// has ever registered.
//
// # Why it can repeat itself safely
//
// With stable membership, create-if-absent makes a repeated pass, a pass that
// overlaps a status lookup, and a restarted process reach the same state and
// state their facts once. A member join can violate that Create behavior. The
// contender model described in the package documentation restores convergence by
// deriving the final state from retained proposals instead of one Create result.
type Reconciler struct {
	store    *store
	self     fabric.Member
	members  []fabric.Member
	recorder Recorder
}

// Reconcile runs one pass over every registration request in the site.
//
// A request that fails is reported, and the pass continues to the rest: one
// unreadable record must not be able to stop the site from accepting everything
// else. Every failure is returned, joined, rather than reduced to the first.
func (r *Reconciler) Reconcile(ctx context.Context) error {
	requests, err := r.store.allRequests(ctx)
	if err != nil {
		return err
	}
	var errs []error
	for _, request := range requests {
		if err := ctx.Err(); err != nil {
			// A canceled scan stops now and says so. Whatever it has already
			// written stands: every write is idempotent, so the next pass
			// resumes rather than repairs.
			errs = append(errs, err)
			break
		}
		if err := r.advanceRequest(ctx, request); err != nil {
			errs = append(errs, fmt.Errorf("registration: reconcile %s: %w", request.key(), err))
		}
	}
	return errors.Join(errs...)
}

// Run reconciles once per interval until ctx is done, reporting each failed pass
// to report and returning nil when asked to stop.
//
// A failed pass does not stop the loop. The next pass sees the same site and may
// well succeed, whereas a platform that has stopped reconciling has stopped
// registering anything, silently. Stopping is therefore reserved for being told
// to stop.
//
// It does not run an initial pass: startup owns that, so a failure to reconcile
// before the API opens is a startup failure rather than something the loop
// swallows.
func (r *Reconciler) Run(ctx context.Context, interval time.Duration, report func(error)) error {
	if interval <= 0 {
		return fmt.Errorf("registration: reconcile interval %s is not positive", interval)
	}
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return nil
		case <-ticker.C:
			if err := r.Reconcile(ctx); err != nil && report != nil && ctx.Err() == nil {
				report(err)
			}
		}
	}
}

// advance runs one reconciliation for a single request key, so a caller that
// cares about one registration does not pay for a scan of the site. A key with
// no request is not an error: there is simply nothing to advance.
func (r *Reconciler) advance(ctx context.Context, key Key) error {
	request, found, err := r.store.request(ctx, key)
	if err != nil || !found {
		return err
	}
	return r.advanceRequest(ctx, request)
}

// advanceRequest records this instance's decision about request and, when this
// instance is the request's origin, commits it if the site has agreed.
//
// Only the origin commits. Any instance could compute the same answer from the
// same records, but one writer per request keeps the commit boring: there is
// nothing to reconcile between committers, and the accepted record's create
// would be the tie-break anyway.
func (r *Reconciler) advanceRequest(ctx context.Context, request requestRecord) error {
	accepted, committed, err := r.store.accepted(ctx, request.key())
	if err != nil {
		return err
	}
	// A committed proposal is settled for good: every expected instance accepted
	// it, the origin committed it, and registration is create-only, so no later
	// pass can reach a different answer. Leaving it alone is what keeps a pass
	// proportional to what is still undecided rather than to everything the site
	// has ever registered.
	if committed && accepted.Fingerprint == request.Fingerprint {
		return nil
	}
	if err := r.confirm(ctx, request, committed); err != nil {
		return err
	}
	if request.OriginMachine != r.self.Machine {
		return nil
	}
	return r.commit(ctx, request)
}

// confirm records this instance's decision about request, once. conflicting
// reports that the key already holds an accepted registration of some other
// proposal.
//
// The decision is stored under the proposal's fingerprint, so it says what this
// instance thinks of this exact data and can never be read as approving
// anything else. Creating it is the transition: an instance that had already
// decided records nothing and states nothing, which is what makes a rescan and a
// restart quiet.
func (r *Reconciler) confirm(ctx context.Context, request requestRecord, conflicting bool) error {
	status := api.RegistrationStatusAccepted
	var reason *string
	if problem := r.validate(request, conflicting); problem != "" {
		status, reason = api.RegistrationStatusRejected, &problem
	}

	created, err := r.store.createConfirmation(ctx, confirmationRecord{
		Version:     recordVersion,
		UnitType:    request.UnitType,
		UnitID:      request.UnitID,
		Fingerprint: request.Fingerprint,
		Machine:     r.self.Machine,
		Status:      status,
		Reason:      copyString(reason),
	})
	if err != nil || !created {
		return err
	}

	if status == api.RegistrationStatusRejected {
		return r.record(ctx, Rejected{
			UnitType:         request.UnitType,
			UnitID:           request.UnitID,
			OriginMachine:    request.OriginMachine,
			RejectingMachine: r.self.Machine,
			Reason:           *reason,
		})
	}
	return r.record(ctx, Confirmed{
		UnitType:          request.UnitType,
		UnitID:            request.UnitID,
		OriginMachine:     request.OriginMachine,
		ConfirmingMachine: r.self.Machine,
	})
}

// validate decides whether this instance can accept request, returning a bounded
// reason or "" when it can. conflicting reports that the key already holds an
// accepted registration of some other proposal.
//
// Every instance validates independently and reaches the same answer from the
// same record, because the answer is a function of the record and of this site's
// static deployment. Nothing here consults who is currently reachable: a peer
// being down is not a reason to refuse a proposal. That determinism is why one
// instance refusing while the others accept is not a state a healthy site can
// reach, and why aggregation still has to handle it if it ever does.
func (r *Reconciler) validate(request requestRecord, conflicting bool) string {
	if request.Version != recordVersion {
		return ReasonUnsupportedVersion
	}
	if err := validateProposal(request); err != nil {
		return ReasonInvalidProposal
	}
	if request.Fingerprint != fingerprintOf(request) {
		return ReasonFingerprintMismatch
	}
	if !r.knownOrigin(request.location()) {
		return ReasonUnknownOrigin
	}
	// A key that already holds a different accepted registration cannot take
	// this one: registration is create-only, so the accepted record wins and the
	// proposal is refused rather than left pending forever.
	//
	// The current design cannot produce this: a key's request record is created
	// once and is what gets committed, so a key's accepted record is always this
	// proposal's. It is checked anyway, because an instance deciding on stored
	// data should say what it found rather than assume how it got there.
	if conflicting {
		return ReasonAcceptedKeyConflict
	}
	return ""
}

// knownOrigin reports whether location is an expected platform instance of this
// site, at the IP the deployment says it has.
//
// The origin is compared against the descriptor, not against anything observed:
// a request may only originate where the site says a platform instance is.
func (r *Reconciler) knownOrigin(location Location) bool {
	for _, member := range r.members {
		if member.Machine == location.Machine {
			return member.IP == location.IP
		}
	}
	return false
}

// commit creates the accepted registration once every expected platform
// instance has accepted this exact proposal.
//
// The acceptance set is the site's static expected membership, including this
// instance. It is not a quorum, and it is not who is currently connected: a
// request waits for an instance that is down rather than being accepted without
// it. That is the whole promise of the two-phase design, and weakening it here
// would be the only way to break it.
//
// With stable membership, creating the record is the commit and its existence
// means accepted. A membership transition can falsely create a competing record;
// contender reconciliation will make this projection match the selected winner.
func (r *Reconciler) commit(ctx context.Context, request requestRecord) error {
	decisions, err := r.store.decisions(ctx, request, r.members)
	if err != nil {
		return err
	}
	for _, member := range r.members {
		decision, decided := decisions[member.Machine]
		if !decided || decision.Status != api.RegistrationStatusAccepted {
			// Not yet, or never: an expected instance that has not decided
			// keeps the request pending, and one that refused keeps it rejected
			// for good. Neither is an error, and neither is retried into
			// acceptance.
			return nil
		}
	}

	created, err := r.store.createAccepted(ctx, acceptedFrom(request))
	if err != nil || !created {
		return err
	}
	return r.record(ctx, Accepted{
		UnitType:               request.UnitType,
		UnitID:                 request.UnitID,
		UnitTypeNameAdvertised: request.UnitTypeNameAdvertised,
		Role:                   roleValue(request.Role),
		Machine:                request.OriginMachine,
		IP:                     request.OriginIP,
	})
}

// record states one fact, reporting a failure with context rather than
// swallowing it.
func (r *Reconciler) record(ctx context.Context, event events.Event) error {
	if err := r.recorder.Record(ctx, event); err != nil {
		return fmt.Errorf("registration: record %s: %w", event.EventType(), err)
	}
	return nil
}

// validateProposal checks a proposal's own fields are ones the platform accepts.
// It is the same check the request passed on the way in, applied again to the
// stored record: an instance decides on what the site holds, not on what some
// other instance says it validated.
func validateProposal(request requestRecord) error {
	if err := validateRequest(api.RegistrationRequest{
		UnitType:               request.UnitType,
		UnitID:                 request.UnitID,
		UnitTypeNameAdvertised: request.UnitTypeNameAdvertised,
		Role:                   request.Role,
	}); err != nil {
		return err
	}
	return validateLocation(request.location())
}

// validateRequest checks the fields a client supplies. UnitType and UnitID need
// no range check: their Go types are the range.
func validateRequest(request api.RegistrationRequest) error {
	if strings.TrimSpace(request.UnitTypeNameAdvertised) == "" {
		return errors.New("registration: unit type name advertised is blank")
	}
	if request.Role != nil && *request.Role != api.RoleMaster && *request.Role != api.RoleSlave {
		return fmt.Errorf("registration: role %q is invalid", *request.Role)
	}
	return nil
}

// validateLocation checks a platform origin is a usable descriptor identity.
func validateLocation(location Location) error {
	if strings.TrimSpace(location.Machine) == "" {
		return errors.New("registration: location machine is blank")
	}
	if net.ParseIP(location.IP) == nil {
		return fmt.Errorf("registration: location IP %q is invalid", location.IP)
	}
	return nil
}
