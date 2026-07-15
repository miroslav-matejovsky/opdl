package registration

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"time"

	"github.com/miroslav-matejovsky/opdl/platform/api"
	"github.com/miroslav-matejovsky/opdl/platform/internal/events"
	"github.com/miroslav-matejovsky/opdl/platform/internal/fabric"
)

// ErrConflict reports an attempt to claim a registration key that is already
// held by a different proposal.
var ErrConflict = errors.New("registration key conflict")

// Recorder records the facts a registration produces. The service and the
// reconciler each record at the transition that owns the fact, so an event
// exists if and only if the transition happened. events.Recorder and
// events.NopRecorder implement it.
type Recorder interface {
	// Record stores one event or returns an error with context.
	Record(ctx context.Context, event events.Event) error
}

// CreateResult distinguishes a newly created request from an exact retry.
type CreateResult string

const (
	// CreateResultNew reports that a request claimed its key for the first time.
	CreateResultNew CreateResult = "new"
	// CreateResultRetry reports an exact idempotent retry of an existing request.
	CreateResultRetry CreateResult = "retry"
)

// Service is the registration use case as the public API calls it: it takes
// requests, and it answers what the site currently holds.
//
// It decides nothing. Taking a request writes a proposal and says so; whether
// that proposal becomes a registration is the site's answer, reached by every
// instance's Reconciler, and this type only reports it. The split is deliberate:
// a client's call must not be able to accept its own registration.
type Service struct {
	store      *store
	location   Location
	members    []fabric.Member
	reconciler *Reconciler
	recorder   Recorder
	now        func() time.Time
}

// Open builds the registration service and reconciler one platform process runs.
//
// Both are returned because the runtime owns both: the service is what the HTTP
// API calls, and the reconciler is what the runtime must run an initial pass of
// before opening that API and must stop before closing the fabric. They share
// one view of the site's collections, and the identity they work as is the
// fabric's own local member, so a machine can only ever answer as itself.
func Open(f fabric.Fabric, recorder Recorder) (*Service, *Reconciler, error) {
	return openWithClock(f, recorder, time.Now)
}

// openWithClock builds registration with now as its platform-observed clock.
// It is kept internal so tests can make contender selection deterministic.
func openWithClock(f fabric.Fabric, recorder Recorder, now func() time.Time) (*Service, *Reconciler, error) {
	if recorder == nil {
		return nil, nil, errors.New("registration: recorder is required")
	}
	if now == nil {
		return nil, nil, errors.New("registration: clock is required")
	}
	store, err := openStore(f)
	if err != nil {
		return nil, nil, err
	}

	members := f.Members()
	if len(members) == 0 {
		return nil, nil, errors.New("registration: the fabric expects no members")
	}
	// The expected membership is the acceptance set, so a member the platform
	// could not recognize a request from is a deployment that cannot register
	// anything. That is a startup failure, not a per-request surprise.
	for _, member := range members {
		if err := validateLocation(Location{Machine: member.Machine, IP: member.IP}); err != nil {
			return nil, nil, fmt.Errorf("registration: expected member %q: %w", member.Machine, err)
		}
	}
	self, ok := fabric.Self(members)
	if !ok {
		return nil, nil, errors.New("registration: the fabric has no local member")
	}

	reconciler := &Reconciler{store: store, self: self, members: members, recorder: recorder, now: now}
	service := &Service{
		store:      store,
		location:   Location{Machine: self.Machine, IP: self.IP},
		members:    members,
		reconciler: reconciler,
		recorder:   recorder,
		now:        now,
	}
	return service, reconciler, nil
}

// Create takes a registration request and returns whether it claimed its key or
// repeated an existing claim. It does not register anything: the request is a
// proposal, and the site decides.
//
// The origin is this machine's own descriptor identity, never the client's
// claim. An identical repeat is idempotent, and every other difference on a key
// that is already visible is a conflict, whether it came from this machine or
// another one. A concurrent contender may be retained provisionally and later
// lose deterministic reconciliation.
//
// Only a first claim records a requested event: a retry changes no state and so
// states nothing, and a request that fails validation was never stored. A
// conflict records its warning before the error reaches the caller, because the
// refused attempt leaves no other trace of itself anywhere.
//
// Without a caller identity, an identical second unit registering the same key
// from the same machine is indistinguishable from a retry, and is answered as
// one. That is a known limitation of this phase: nothing in the request tells
// the platform who is asking.
func (s *Service) Create(ctx context.Context, request api.RegistrationRequest) (CreateResult, error) {
	if err := validateRequest(request); err != nil {
		return "", err
	}
	candidate := requestRecord{
		Version:                recordVersion,
		UnitType:               request.UnitType,
		UnitID:                 request.UnitID,
		UnitTypeNameAdvertised: request.UnitTypeNameAdvertised,
		Role:                   copyString(request.Role),
		OriginMachine:          s.location.Machine,
		OriginIP:               s.location.IP,
		ObservedAt:             s.now().UTC(),
	}
	candidate.Fingerprint = fingerprintOf(candidate)

	result, existing, err := s.store.createRequest(ctx, candidate)
	if err != nil {
		if !errors.Is(err, ErrConflict) {
			return "", err
		}
		if recordErr := s.record(ctx, Conflict{
			UnitType:  candidate.UnitType,
			UnitID:    candidate.UnitID,
			Existing:  eventFields(existing),
			Attempted: eventFields(candidate),
			Reason:    ReasonKeyConflict,
		}); recordErr != nil {
			return "", recordErr
		}
		return "", err
	}
	if result == CreateResultRetry {
		return result, nil
	}
	return result, s.record(ctx, Requested{
		UnitType:               candidate.UnitType,
		UnitID:                 candidate.UnitID,
		UnitTypeNameAdvertised: candidate.UnitTypeNameAdvertised,
		Role:                   roleValue(candidate.Role),
		Machine:                candidate.OriginMachine,
		IP:                     candidate.OriginIP,
	})
}

// Get returns a registration view, and reports not found unless this machine is
// the request's origin. A client checks its request where it made it: another
// machine holds the same state but is not who was asked.
//
// It reconciles the key before answering, so a client polling its own request
// drives it forward instead of waiting for a scheduled pass. That is why a read
// here can write: it can record this instance's confirmation and commit the
// registration. It cannot report accepted early, though, because accepted is
// still the immutable acceptance marker existing and nothing else.
func (s *Service) Get(ctx context.Context, key Key) (api.Registration, bool, error) {
	if err := s.reconciler.advance(ctx, key); err != nil {
		return api.Registration{}, false, err
	}
	contenders, err := s.store.contenders(ctx, key)
	if err != nil {
		return api.Registration{}, false, err
	}
	request, found := contenderFromOrigin(contenders, s.location)
	if !found {
		return api.Registration{}, false, nil
	}
	view, err := s.project(ctx, request)
	if err != nil {
		return api.Registration{}, false, err
	}
	return view, true, nil
}

// contenderFromOrigin returns the deterministic first contender submitted to
// one platform origin. Without caller identity, two distinct concurrent
// proposals from the same origin remain indistinguishable to the status route.
func contenderFromOrigin(contenders []requestRecord, location Location) (requestRecord, bool) {
	for _, contender := range contenders {
		if contender.location() == location {
			return contender, true
		}
	}
	return requestRecord{}, false
}

// List returns every registration request in the site, pending, accepted, and
// rejected alike, in deterministic origin-machine, unit-type, unit-ID order.
//
// It answers on any machine, unlike Get: the list is the site's state, and every
// machine holds it. It enumerates requests rather than registrations, so a
// request that has not been accepted, or never will be, is visible rather than
// missing.
//
// It reconciles nothing. A list is a question about the site, and answering one
// question about every request must not turn into deciding every request.
func (s *Service) List(ctx context.Context) ([]api.Registration, error) {
	requests, err := s.store.allRequests(ctx)
	if err != nil {
		return nil, err
	}
	registrations := make([]api.Registration, 0, len(requests))
	for _, request := range requests {
		view, err := s.project(ctx, request)
		if err != nil {
			return nil, err
		}
		registrations = append(registrations, view)
	}
	sort.Slice(registrations, func(i, j int) bool {
		if registrations[i].Machine != registrations[j].Machine {
			return registrations[i].Machine < registrations[j].Machine
		}
		if registrations[i].UnitType != registrations[j].UnitType {
			return registrations[i].UnitType < registrations[j].UnitType
		}
		return registrations[i].UnitID < registrations[j].UnitID
	})
	return registrations, nil
}

// Conflicts returns every unit key with multiple retained proposals. Each
// result is resolved from the same immutable contender history as List: the
// selected winner is returned separately and every other proposal is an
// effective registration_key_conflict rejection.
//
// Results are ordered by unit key. Losers retain contender order, which is
// observed time then fingerprint, so all machines present the same result after
// they have seen the same contenders.
func (s *Service) Conflicts(ctx context.Context) ([]api.RegistrationConflict, error) {
	contenders, err := s.store.allRequests(ctx)
	if err != nil {
		return nil, err
	}
	byKey := make(map[Key][]requestRecord)
	for _, contender := range contenders {
		byKey[contender.key()] = append(byKey[contender.key()], contender)
	}
	keys := make([]Key, 0, len(byKey))
	for key, group := range byKey {
		if len(group) > 1 {
			keys = append(keys, key)
		}
	}
	sort.Slice(keys, func(i, j int) bool { return keys[i].String() < keys[j].String() })

	conflicts := make([]api.RegistrationConflict, 0, len(keys))
	for _, key := range keys {
		group := byKey[key]
		winner, _, err := s.store.winner(ctx, group)
		if err != nil {
			return nil, err
		}
		winnerView, err := s.projectContenders(ctx, winner, group)
		if err != nil {
			return nil, err
		}
		losers := make([]api.Registration, 0, len(group)-1)
		for _, contender := range group {
			if contender.Fingerprint == winner.Fingerprint {
				continue
			}
			view, err := s.projectContenders(ctx, contender, group)
			if err != nil {
				return nil, err
			}
			losers = append(losers, view)
		}
		conflicts = append(conflicts, api.RegistrationConflict{
			UnitType:         key.UnitType,
			UnitID:           key.UnitID,
			ResolutionStatus: api.RegistrationConflictResolutionResolved,
			Winner:           winnerView,
			Losers:           losers,
		})
	}
	return conflicts, nil
}

// project derives one registration view from a stored proposal and the site's
// current state. Status and List both use it, so the two can never disagree
// about a request.
//
// The instance projection is built from the site's whole expected membership,
// not from the confirmations that happen to exist: an instance that has not
// answered is the interesting case, and it appears as pending rather than not at
// all.
func (s *Service) project(ctx context.Context, request requestRecord) (api.Registration, error) {
	contenders, err := s.store.contenders(ctx, request.key())
	if err != nil {
		return api.Registration{}, err
	}
	return s.projectContenders(ctx, request, contenders)
}

// projectContenders derives a registration view using one supplied contender
// group. Conflict queries pass their enumerated group through this helper so the
// reported winner and loser views agree even if another write becomes visible
// after their scan.
func (s *Service) projectContenders(ctx context.Context, request requestRecord, contenders []requestRecord) (api.Registration, error) {
	winner, accepted, err := s.store.winner(ctx, contenders)
	if err != nil {
		return api.Registration{}, err
	}
	if winner.Fingerprint != request.Fingerprint {
		reason := ReasonKeyConflict
		instances := make([]api.PlatformInstanceRegistrationStatus, 0, len(s.members))
		for _, member := range s.members {
			instances = append(instances, api.PlatformInstanceRegistrationStatus{
				Machine: member.Machine,
				IP:      member.IP,
				Status:  api.RegistrationStatusRejected,
				Reason:  copyString(&reason),
			})
		}
		return api.Registration{
			UnitType:               request.UnitType,
			UnitID:                 request.UnitID,
			UnitTypeNameAdvertised: request.UnitTypeNameAdvertised,
			Role:                   copyString(request.Role),
			Machine:                request.OriginMachine,
			IP:                     request.OriginIP,
			Status:                 api.RegistrationStatusRejected,
			Reason:                 &reason,
			PlatformInstances:      instances,
		}, nil
	}

	decisions, err := s.store.decisions(ctx, request, s.members)
	if err != nil {
		return api.Registration{}, err
	}
	// Expected members are ordered by machine, so the instance projection is
	// too, and the reason chosen below is the first rejection in that order.
	instances := make([]api.PlatformInstanceRegistrationStatus, 0, len(s.members))
	for _, member := range s.members {
		instance := api.PlatformInstanceRegistrationStatus{
			Machine: member.Machine,
			IP:      member.IP,
			Status:  api.RegistrationStatusPending,
		}
		if decision, decided := decisions[member.Machine]; decided {
			instance.Status = decision.Status
			if decision.Status == api.RegistrationStatusRejected {
				instance.Reason = copyString(decision.Reason)
			}
		}
		instances = append(instances, instance)
	}

	if accepted {
		return api.Registration{
			UnitType:               request.UnitType,
			UnitID:                 request.UnitID,
			UnitTypeNameAdvertised: request.UnitTypeNameAdvertised,
			Role:                   copyString(request.Role),
			Machine:                request.OriginMachine,
			IP:                     request.OriginIP,
			Status:                 api.RegistrationStatusAccepted,
			PlatformInstances:      instances,
		}, nil
	}

	status, reason := overall(instances)
	return api.Registration{
		UnitType:               request.UnitType,
		UnitID:                 request.UnitID,
		UnitTypeNameAdvertised: request.UnitTypeNameAdvertised,
		Role:                   copyString(request.Role),
		Machine:                request.OriginMachine,
		IP:                     request.OriginIP,
		Status:                 status,
		Reason:                 reason,
		PlatformInstances:      instances,
	}, nil
}

// overall derives a request's status from what the expected instances have
// decided: rejected once any of them has refused it, and pending until then.
//
// Accepted is deliberately not derivable here. A request is accepted because its
// immutable acceptance marker exists, not because the confirmations look complete, so
// this cannot report acceptance and cannot race the commit that grants it.
func overall(instances []api.PlatformInstanceRegistrationStatus) (status string, reason *string) {
	for _, instance := range instances {
		if instance.Status == api.RegistrationStatusRejected {
			return api.RegistrationStatusRejected, copyString(instance.Reason)
		}
	}
	return api.RegistrationStatusPending, nil
}

// record states one fact. A failure is returned with context rather than
// swallowed, and it replaces whatever the operation would otherwise have
// returned, including ErrConflict: a platform that cannot report what it did is
// failing, and saying so beats answering as if nothing happened. The store is
// not rolled back to match, so a transition can outlive its lost event.
func (s *Service) record(ctx context.Context, event events.Event) error {
	if err := s.recorder.Record(ctx, event); err != nil {
		return fmt.Errorf("registration: record %s: %w", event.EventType(), err)
	}
	return nil
}

// eventFields projects a stored proposal onto the immutable fields a
// registration key is claimed on.
func eventFields(request requestRecord) Fields {
	return Fields{
		UnitTypeNameAdvertised: request.UnitTypeNameAdvertised,
		Role:                   roleValue(request.Role),
		Machine:                request.OriginMachine,
		IP:                     request.OriginIP,
	}
}

// roleValue flattens the optional role for an event payload: an unset role is
// the empty string, which the payload omits.
func roleValue(role *string) string {
	if role == nil {
		return ""
	}
	return *role
}
