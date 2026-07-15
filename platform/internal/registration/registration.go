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
}

// NewService creates a local registration service with isolated in-memory state.
func NewService(location Location, coordinator Coordinator) (*Service, error) {
	return newService(location, coordinator, newMemoryStore())
}

func newService(location Location, coordinator Coordinator, store *memoryStore) (*Service, error) {
	if err := validateLocation(location); err != nil {
		return nil, err
	}
	if coordinator == nil {
		return nil, errors.New("registration: coordinator is required")
	}
	if store == nil {
		return nil, errors.New("registration: store is required")
	}
	return &Service{location: location, coordinator: coordinator, store: store}, nil
}

// Create validates and persists a request. A matching existing request is an
// idempotent retry; every immutable mismatch returns ErrConflict.
func (s *Service) Create(ctx context.Context, request api.RegistrationRequest) (CreateResult, error) {
	if err := validateRequest(request); err != nil {
		return "", err
	}
	key := Key{UnitType: request.UnitType, UnitID: request.UnitID}
	result, err := s.store.create(ctx, record{key: key, request: copyRequest(request), location: s.location})
	if err != nil {
		return "", err
	}
	if result == CreateResultRetry {
		return result, nil
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
func (s *Service) Confirm(ctx context.Context, key Key, status string, reason *string) error {
	if status != api.RegistrationStatusAccepted && status != api.RegistrationStatusRejected {
		return fmt.Errorf("registration: invalid confirmation status %q", status)
	}
	if err := validateReason(reason); err != nil {
		return err
	}
	return s.store.confirm(ctx, key, confirmation{location: s.location, status: status, reason: copyString(reason)})
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

func (s *memoryStore) create(ctx context.Context, candidate record) (CreateResult, error) {
	if err := ctx.Err(); err != nil {
		return "", err
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
	return CreateResultNew, nil
}

func compareRecord(existing, candidate record) (CreateResult, error) {
	if existing.location == candidate.location && existing.request.UnitTypeNameAdvertised == candidate.request.UnitTypeNameAdvertised && sameString(existing.request.Role, candidate.request.Role) {
		return CreateResultRetry, nil
	}
	return "", ErrConflict
}

func (s *memoryStore) confirm(ctx context.Context, key Key, result confirmation) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.pending[key]; !ok {
		if _, accepted := s.accepted[key]; accepted {
			return nil
		}
		return nil
	}
	if s.confirmations[key] == nil {
		s.confirmations[key] = map[string]confirmation{}
	}
	if existing, ok := s.confirmations[key][result.location.Machine]; ok && existing.status == api.RegistrationStatusRejected {
		return nil
	}
	s.confirmations[key][result.location.Machine] = result
	if result.status == api.RegistrationStatusAccepted {
		s.accepted[key] = s.pending[key]
		delete(s.pending, key)
	}
	return nil
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
