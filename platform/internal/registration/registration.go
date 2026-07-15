package registration

import (
	"context"
	"errors"
	"fmt"
	"net"
	"sort"
	"strings"
	"sync"

	"github.com/miroslav-matejovsky/opdl/platform/api"
	"github.com/miroslav-matejovsky/opdl/platform/internal/events"
)

const maxReasonLength = 64

var (
	// ErrConflict reports an attempt to create an existing registration with
	// different immutable request or origin fields.
	ErrConflict = errors.New("registration key conflict")
)

// Key is the immutable, site-local identity of one registration request.
type Key struct {
	// UnitType is the unit type identifier.
	UnitType uint8
	// UnitID is the unit identifier within UnitType.
	UnitID uint16
}

// Location is the trusted platform descriptor identity where a registration
// request originates.
type Location struct {
	// Machine is the immutable descriptor machine name.
	Machine string
	// IP is the immutable descriptor IP address.
	IP string
}

// Coordinator starts registration confirmation after a request is persisted.
// The service provides confirm so coordinators cannot alter request data.
type Coordinator interface {
	Trigger(context.Context, Key, func(context.Context, string, *string) error) error
}

// SingleInstanceCoordinator accepts each request as the only expected platform
// instance. Tests can inject a coordinator that holds the supplied confirmation
// callback to keep a new registration visibly pending.
type SingleInstanceCoordinator struct{}

// Trigger writes this platform instance's accepted confirmation.
func (SingleInstanceCoordinator) Trigger(ctx context.Context, _ Key, confirm func(context.Context, string, *string) error) error {
	return confirm(ctx, api.RegistrationStatusAccepted, nil)
}

// Recorder records the facts a registration produces. The service records at
// the state transition that owns each fact, so an event exists if and only if
// the transition happened. events.Recorder and events.NopRecorder implement it.
type Recorder interface {
	// Record stores one event or returns an error with context.
	Record(ctx context.Context, event events.Event) error
}

// CreateResult distinguishes a newly persisted request from an exact retry.
type CreateResult string

const (
	// CreateResultNew reports that a request was persisted for the first time.
	CreateResultNew CreateResult = "new"
	// CreateResultRetry reports an exact idempotent retry of an existing request.
	CreateResultRetry CreateResult = "retry"
)

// Service creates, confirms, and projects registration requests.
type Service struct {
	location    Location
	store       *memoryStore
	coordinator Coordinator
	recorder    Recorder
}

// NewService creates a local registration service with isolated in-memory state.
func NewService(location Location, coordinator Coordinator, recorder Recorder) (*Service, error) {
	return newService(location, coordinator, recorder, newMemoryStore())
}

func newService(location Location, coordinator Coordinator, recorder Recorder, store *memoryStore) (*Service, error) {
	if err := validateLocation(location); err != nil {
		return nil, err
	}
	if coordinator == nil {
		return nil, errors.New("registration: coordinator is required")
	}
	if recorder == nil {
		return nil, errors.New("registration: recorder is required")
	}
	if store == nil {
		return nil, errors.New("registration: store is required")
	}
	return &Service{location: location, coordinator: coordinator, recorder: recorder, store: store}, nil
}

// Create validates and persists a request. A matching existing request is an
// idempotent retry; every immutable mismatch returns ErrConflict.
//
// Only a first persist records a requested event: an exact retry changes no
// state and so produces no event, and a request that fails validation is never
// persisted and produces none either. A conflict records its warning before the
// error reaches the caller, because the rejected attempt leaves no other trace.
func (s *Service) Create(ctx context.Context, request api.RegistrationRequest) (CreateResult, error) {
	if err := validateRequest(request); err != nil {
		return "", err
	}
	key := Key{UnitType: request.UnitType, UnitID: request.UnitID}
	candidate := record{key: key, request: copyRequest(request), location: s.location}
	result, existing, err := s.store.create(ctx, candidate)
	if err != nil {
		if !errors.Is(err, ErrConflict) {
			return "", err
		}
		if recordErr := s.record(ctx, Conflict{
			UnitType:  key.UnitType,
			UnitID:    key.UnitID,
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
	if err := s.record(ctx, Requested{
		UnitType:               key.UnitType,
		UnitID:                 key.UnitID,
		UnitTypeNameAdvertised: candidate.request.UnitTypeNameAdvertised,
		Role:                   roleValue(candidate.request.Role),
		Machine:                candidate.location.Machine,
		IP:                     candidate.location.IP,
	}); err != nil {
		return "", err
	}
	if err := s.coordinator.Trigger(ctx, key, func(confirmCtx context.Context, status string, reason *string) error {
		return s.Confirm(confirmCtx, key, status, reason)
	}); err != nil {
		return "", fmt.Errorf("registration: coordinate %d/%d: %w", key.UnitType, key.UnitID, err)
	}
	return result, nil
}

// Confirm records this platform instance's result for a pending request. A
// rejected request remains visible; acceptance promotes it to the accepted store.
//
// Events follow the state change, not the call: a confirmation that changes
// nothing, because the request is unknown, already accepted, or already
// rejected by this instance, records nothing. Acceptance by the last expected
// instance records the instance's confirmation and then the request's
// acceptance, in that order.
func (s *Service) Confirm(ctx context.Context, key Key, status string, reason *string) error {
	if status != api.RegistrationStatusAccepted && status != api.RegistrationStatusRejected {
		return fmt.Errorf("registration: invalid confirmation status %q", status)
	}
	if err := validateReason(reason); err != nil {
		return err
	}
	outcome, err := s.store.confirm(ctx, key, confirmation{location: s.location, status: status, reason: copyString(reason)})
	if err != nil {
		return err
	}
	if !outcome.confirmed {
		return nil
	}
	if status == api.RegistrationStatusRejected {
		return s.record(ctx, Rejected{
			UnitType:         key.UnitType,
			UnitID:           key.UnitID,
			OriginMachine:    outcome.record.location.Machine,
			RejectingMachine: s.location.Machine,
			Reason:           reasonValue(reason),
		})
	}
	if err := s.record(ctx, Confirmed{
		UnitType:          key.UnitType,
		UnitID:            key.UnitID,
		OriginMachine:     outcome.record.location.Machine,
		ConfirmingMachine: s.location.Machine,
	}); err != nil {
		return err
	}
	if !outcome.accepted {
		return nil
	}
	return s.record(ctx, Accepted{
		UnitType:               key.UnitType,
		UnitID:                 key.UnitID,
		UnitTypeNameAdvertised: outcome.record.request.UnitTypeNameAdvertised,
		Role:                   roleValue(outcome.record.request.Role),
		Machine:                outcome.record.location.Machine,
		IP:                     outcome.record.location.IP,
	})
}

// record stores one event. A failure is returned with context rather than
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

// Get returns a registration view only when this service owns the request's
// origin location. Other platform machines must return not found.
func (s *Service) Get(ctx context.Context, key Key) (api.Registration, bool, error) {
	record, confirmations, found, err := s.store.find(ctx, key)
	if err != nil || !found || record.location != s.location {
		return api.Registration{}, false, err
	}
	return project(record, confirmations), true, nil
}

// List returns every registration request in deterministic origin-machine, unit
// type, and unit ID order.
func (s *Service) List(ctx context.Context) ([]api.Registration, error) {
	records, err := s.store.list(ctx)
	if err != nil {
		return nil, err
	}
	registrations := make([]api.Registration, 0, len(records))
	for _, item := range records {
		registrations = append(registrations, project(item.record, item.confirmations))
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

func validateLocation(location Location) error {
	if strings.TrimSpace(location.Machine) == "" {
		return errors.New("registration: location machine is blank")
	}
	if net.ParseIP(location.IP) == nil {
		return fmt.Errorf("registration: location IP %q is invalid", location.IP)
	}
	return nil
}

func validateRequest(request api.RegistrationRequest) error {
	if strings.TrimSpace(request.UnitTypeNameAdvertised) == "" {
		return errors.New("registration: unit type name advertised is blank")
	}
	if request.Role != nil && *request.Role != api.RoleMaster && *request.Role != api.RoleSlave {
		return fmt.Errorf("registration: role %q is invalid", *request.Role)
	}
	return nil
}

func validateReason(reason *string) error {
	if reason == nil {
		return nil
	}
	if *reason == "" || len(*reason) > maxReasonLength {
		return errors.New("registration: reason length is invalid")
	}
	for _, r := range *reason {
		if (r < 'a' || r > 'z') && (r < '0' || r > '9') && r != '_' && r != '-' && r != '.' {
			return errors.New("registration: reason is not machine-readable")
		}
	}
	return nil
}

type record struct {
	key      Key
	request  api.RegistrationRequest
	location Location
}

type confirmation struct {
	location Location
	status   string
	reason   *string
}

type listedRecord struct {
	record        record
	confirmations []confirmation
}

// confirmOutcome is what a confirmation changed in the store. The service needs
// it to record events at transitions only, and it carries the stored record so
// the caller reports the committed data rather than the client's copy.
type confirmOutcome struct {
	// confirmed reports that this instance's decision was newly stored.
	confirmed bool
	// accepted reports that the request became accepted by this decision.
	accepted bool
	// record is the stored request the decision applies to.
	record record
}

type memoryStore struct {
	mu            sync.RWMutex
	pending       map[Key]record
	accepted      map[Key]record
	confirmations map[Key]map[string]confirmation
}

func newMemoryStore() *memoryStore {
	return &memoryStore{
		pending:       map[Key]record{},
		accepted:      map[Key]record{},
		confirmations: map[Key]map[string]confirmation{},
	}
}

// create stores candidate unless its key is taken. On ErrConflict it returns the
// stored record that holds the key, so the caller can report both sides of the
// conflict; the stored record is never modified.
func (s *memoryStore) create(ctx context.Context, candidate record) (CreateResult, record, error) {
	if err := ctx.Err(); err != nil {
		return "", record{}, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if existing, ok := s.pending[candidate.key]; ok {
		return compareRecord(existing, candidate)
	}
	if existing, ok := s.accepted[candidate.key]; ok {
		return compareRecord(existing, candidate)
	}
	s.pending[candidate.key] = candidate
	return CreateResultNew, candidate, nil
}

func compareRecord(existing, candidate record) (CreateResult, record, error) {
	if existing.location == candidate.location && existing.request.UnitTypeNameAdvertised == candidate.request.UnitTypeNameAdvertised && sameString(existing.request.Role, candidate.request.Role) {
		return CreateResultRetry, existing, nil
	}
	return "", existing, ErrConflict
}

// confirm stores one instance's decision for a pending request and reports what
// changed. A decision on an unknown or already accepted request, or a second
// decision from an instance that already rejected it, changes nothing.
func (s *memoryStore) confirm(ctx context.Context, key Key, result confirmation) (confirmOutcome, error) {
	if err := ctx.Err(); err != nil {
		return confirmOutcome{}, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	pending, ok := s.pending[key]
	if !ok {
		return confirmOutcome{}, nil
	}
	if s.confirmations[key] == nil {
		s.confirmations[key] = map[string]confirmation{}
	}
	if existing, ok := s.confirmations[key][result.location.Machine]; ok && existing.status == api.RegistrationStatusRejected {
		return confirmOutcome{}, nil
	}
	s.confirmations[key][result.location.Machine] = result
	outcome := confirmOutcome{confirmed: true, record: pending}
	if result.status == api.RegistrationStatusAccepted {
		s.accepted[key] = pending
		delete(s.pending, key)
		outcome.accepted = true
	}
	return outcome, nil
}

func (s *memoryStore) find(ctx context.Context, key Key) (record, []confirmation, bool, error) {
	if err := ctx.Err(); err != nil {
		return record{}, nil, false, err
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	if item, ok := s.pending[key]; ok {
		return item, copiedConfirmations(s.confirmations[key]), true, nil
	}
	if item, ok := s.accepted[key]; ok {
		return item, copiedConfirmations(s.confirmations[key]), true, nil
	}
	return record{}, nil, false, nil
}

func (s *memoryStore) list(ctx context.Context) ([]listedRecord, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	items := make([]listedRecord, 0, len(s.pending)+len(s.accepted))
	for key, item := range s.pending {
		items = append(items, listedRecord{record: item, confirmations: copiedConfirmations(s.confirmations[key])})
	}
	for key, item := range s.accepted {
		items = append(items, listedRecord{record: item, confirmations: copiedConfirmations(s.confirmations[key])})
	}
	return items, nil
}

func copiedConfirmations(source map[string]confirmation) []confirmation {
	result := make([]confirmation, 0, len(source))
	for _, item := range source {
		result = append(result, confirmation{location: item.location, status: item.status, reason: copyString(item.reason)})
	}
	return result
}

func project(record record, confirmations []confirmation) api.Registration {
	instance := api.PlatformInstanceRegistrationStatus{
		Machine: record.location.Machine,
		IP:      record.location.IP,
		Status:  api.RegistrationStatusPending,
	}
	status := api.RegistrationStatusPending
	for _, confirmation := range confirmations {
		if confirmation.location == record.location {
			instance.Status = confirmation.status
			instance.Reason = copyString(confirmation.reason)
			status = confirmation.status
			break
		}
	}
	instances := []api.PlatformInstanceRegistrationStatus{instance}
	sort.Slice(instances, func(i, j int) bool { return instances[i].Machine < instances[j].Machine })
	return api.Registration{
		UnitType:               record.request.UnitType,
		UnitID:                 record.request.UnitID,
		UnitTypeNameAdvertised: record.request.UnitTypeNameAdvertised,
		Role:                   copyString(record.request.Role),
		Machine:                record.location.Machine,
		IP:                     record.location.IP,
		Status:                 status,
		Reason:                 copyString(instance.Reason),
		PlatformInstances:      instances,
	}
}

// registrationFields projects a stored record onto the immutable fields a key
// conflict is decided on.
func eventFields(item record) Fields {
	return Fields{
		UnitTypeNameAdvertised: item.request.UnitTypeNameAdvertised,
		Role:                   roleValue(item.request.Role),
		Machine:                item.location.Machine,
		IP:                     item.location.IP,
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

// reasonValue flattens an optional bounded reason for an event payload.
func reasonValue(reason *string) string {
	if reason == nil {
		return ""
	}
	return *reason
}

func copyRequest(request api.RegistrationRequest) api.RegistrationRequest {
	request.Role = copyString(request.Role)
	return request
}

func copyString(value *string) *string {
	if value == nil {
		return nil
	}
	valueCopy := *value
	return &valueCopy
}

func sameString(left, right *string) bool {
	if left == nil || right == nil {
		return left == nil && right == nil
	}
	return *left == *right
}
