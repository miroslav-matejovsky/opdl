package registration

import (
	"context"
	"errors"
	"sync"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/miroslav-matejovsky/opdl/platform/api"
	"github.com/miroslav-matejovsky/opdl/platform/internal/events"
)

// recordingRecorder captures what the service records, in order, so a test can
// assert behavior by the facts the service reports rather than by its calls.
type recordingRecorder struct {
	mu        sync.Mutex
	recorded  []events.Event
	failAfter int
	err       error
}

func (r *recordingRecorder) Record(_ context.Context, event events.Event) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.err != nil && len(r.recorded) >= r.failAfter {
		return r.err
	}
	r.recorded = append(r.recorded, event)
	return nil
}

func (r *recordingRecorder) events() []events.Event {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]events.Event(nil), r.recorded...)
}

// types returns the recorded event types in order.
func (r *recordingRecorder) types() []events.Type {
	recorded := r.events()
	types := make([]events.Type, 0, len(recorded))
	for _, event := range recorded {
		types = append(types, event.EventType())
	}
	return types
}

// newRecordingService builds a service on one machine that accepts every
// request as the only expected platform instance.
func newRecordingService(t *testing.T) (*Service, *recordingRecorder) {
	t.Helper()
	recorder := &recordingRecorder{}
	service, err := NewService(Location{Machine: "node-a", IP: "127.0.0.1"}, SingleInstanceCoordinator{}, recorder)
	require.NoError(t, err)
	return service, recorder
}

func TestCreateRecordsRequestedConfirmedThenAccepted(t *testing.T) {
	service, recorder := newRecordingService(t)
	role := api.RoleMaster

	_, err := service.Create(context.Background(), api.RegistrationRequest{
		UnitType: 7, UnitID: 42, UnitTypeNameAdvertised: "Scenario service", Role: &role,
	})
	require.NoError(t, err)

	require.Equal(t, []events.Event{
		Requested{
			UnitType: 7, UnitID: 42, UnitTypeNameAdvertised: "Scenario service", Role: "Master",
			Machine: "node-a", IP: "127.0.0.1",
		},
		Confirmed{
			UnitType: 7, UnitID: 42, OriginMachine: "node-a", ConfirmingMachine: "node-a",
		},
		Accepted{
			UnitType: 7, UnitID: 42, UnitTypeNameAdvertised: "Scenario service", Role: "Master",
			Machine: "node-a", IP: "127.0.0.1",
		},
	}, recorder.events())
}

func TestCreateRecordsNothingWhenValidationFails(t *testing.T) {
	service, recorder := newRecordingService(t)

	_, err := service.Create(context.Background(), api.RegistrationRequest{UnitType: 1, UnitID: 2, UnitTypeNameAdvertised: " "})
	require.Error(t, err)

	invalidRole := "master"
	_, err = service.Create(context.Background(), api.RegistrationRequest{
		UnitType: 1, UnitID: 2, UnitTypeNameAdvertised: "Worker", Role: &invalidRole,
	})
	require.Error(t, err)

	require.Empty(t, recorder.events(), "a request that was never persisted is not a fact")
}

func TestCreateExactRetryRecordsNoDuplicatePhaseEvent(t *testing.T) {
	service, recorder := newRecordingService(t)
	request := api.RegistrationRequest{UnitType: 7, UnitID: 42, UnitTypeNameAdvertised: "Scenario service"}

	first, err := service.Create(context.Background(), request)
	require.NoError(t, err)
	require.Equal(t, CreateResultNew, first)
	afterFirst := recorder.types()

	retry, err := service.Create(context.Background(), request)
	require.NoError(t, err)
	require.Equal(t, CreateResultRetry, retry)

	require.Equal(t, afterFirst, recorder.types(), "an exact retry changes nothing, so it reports nothing")
	require.Equal(t, []events.Type{
		TypeRequested,
		TypeConfirmed,
		TypeAccepted,
	}, recorder.types())
}

func TestCreateKeyConflictRecordsWarningAndPreservesState(t *testing.T) {
	recorder := &recordingRecorder{}
	store := newMemoryStore()
	owner, err := newService(Location{Machine: "node-a", IP: "127.0.0.1"}, &heldCoordinator{}, recorder, store)
	require.NoError(t, err)
	intruder, err := newService(Location{Machine: "node-b", IP: "127.0.0.2"}, &heldCoordinator{}, recorder, store)
	require.NoError(t, err)

	role := api.RoleMaster
	_, err = owner.Create(context.Background(), api.RegistrationRequest{
		UnitType: 7, UnitID: 42, UnitTypeNameAdvertised: "First", Role: &role,
	})
	require.NoError(t, err)

	attempt := api.RegistrationRequest{UnitType: 7, UnitID: 42, UnitTypeNameAdvertised: "Second"}
	_, err = intruder.Create(context.Background(), attempt)
	require.ErrorIs(t, err, ErrConflict)

	recorded := recorder.events()
	require.Len(t, recorded, 2)
	require.Equal(t, Conflict{
		UnitType: 7, UnitID: 42,
		Existing:  Fields{UnitTypeNameAdvertised: "First", Role: "Master", Machine: "node-a", IP: "127.0.0.1"},
		Attempted: Fields{UnitTypeNameAdvertised: "Second", Machine: "node-b", IP: "127.0.0.2"},
		Reason:    ReasonKeyConflict,
	}, recorded[1])
	require.Equal(t, []string{events.TagWarning}, recorded[1].(events.Tagged).Tags())

	// The stored request is untouched by the rejected attempt.
	view, found, err := owner.Get(context.Background(), Key{UnitType: 7, UnitID: 42})
	require.NoError(t, err)
	require.True(t, found)
	require.Equal(t, "First", view.UnitTypeNameAdvertised)
	require.Equal(t, "node-a", view.Machine)

	// Every rejected occurrence is reported, not just the first.
	_, err = intruder.Create(context.Background(), attempt)
	require.ErrorIs(t, err, ErrConflict)
	require.Equal(t, []events.Type{
		TypeRequested,
		TypeConflict,
		TypeConflict,
	}, recorder.types())
}

func TestConfirmRejectionRecordsWarningInsteadOfAcceptance(t *testing.T) {
	recorder := &recordingRecorder{}
	coordinator := &heldCoordinator{}
	service, err := NewService(Location{Machine: "node-a", IP: "127.0.0.1"}, coordinator, recorder)
	require.NoError(t, err)
	_, err = service.Create(context.Background(), api.RegistrationRequest{UnitType: 1, UnitID: 2, UnitTypeNameAdvertised: "Worker"})
	require.NoError(t, err)

	reason := "unit_not_supported"
	coordinator.confirmWith(t, api.RegistrationStatusRejected, &reason)

	recorded := recorder.events()
	require.Len(t, recorded, 2)
	require.Equal(t, Rejected{
		UnitType: 1, UnitID: 2, OriginMachine: "node-a", RejectingMachine: "node-a", Reason: reason,
	}, recorded[1])
	require.Equal(t, []string{events.TagWarning}, recorded[1].(events.Tagged).Tags())
}

func TestConfirmRecordsNothingWithoutAStateChange(t *testing.T) {
	tests := []struct {
		name string
		// create reports whether the request exists before the confirmation.
		create bool
		// settle drives the request into its resting state.
		settle func(t *testing.T, coordinator *heldCoordinator)
	}{
		{
			name:   "unknown request",
			create: false,
			settle: func(*testing.T, *heldCoordinator) {},
		},
		{
			name:   "already accepted request",
			create: true,
			settle: func(t *testing.T, coordinator *heldCoordinator) {
				coordinator.confirmWith(t, api.RegistrationStatusAccepted, nil)
			},
		},
		{
			name:   "instance that already rejected",
			create: true,
			settle: func(t *testing.T, coordinator *heldCoordinator) {
				reason := "unit_not_supported"
				coordinator.confirmWith(t, api.RegistrationStatusRejected, &reason)
			},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			recorder := &recordingRecorder{}
			coordinator := &heldCoordinator{}
			service, err := NewService(Location{Machine: "node-a", IP: "127.0.0.1"}, coordinator, recorder)
			require.NoError(t, err)
			if test.create {
				_, err = service.Create(context.Background(), api.RegistrationRequest{UnitType: 1, UnitID: 2, UnitTypeNameAdvertised: "Worker"})
				require.NoError(t, err)
			}
			test.settle(t, coordinator)
			settled := recorder.types()

			require.NoError(t, service.Confirm(context.Background(), Key{UnitType: 1, UnitID: 2}, api.RegistrationStatusAccepted, nil))
			require.Equal(t, settled, recorder.types())
		})
	}
}

func TestCreateReportsRecordingFailureWithContext(t *testing.T) {
	recorder := &recordingRecorder{err: errors.New("sink is gone")}
	service, err := NewService(Location{Machine: "node-a", IP: "127.0.0.1"}, SingleInstanceCoordinator{}, recorder)
	require.NoError(t, err)

	// The store has already accepted the request when recording fails. The
	// failure is surfaced, not swallowed, and the request is not rolled back.
	_, err = service.Create(context.Background(), api.RegistrationRequest{UnitType: 1, UnitID: 2, UnitTypeNameAdvertised: "Worker"})
	require.ErrorContains(t, err, "registration: record platform.registration.requested")
	require.ErrorContains(t, err, "sink is gone")
	require.NotErrorIs(t, err, ErrConflict, "a recording failure must not read as a client conflict")

	_, found, err := service.Get(context.Background(), Key{UnitType: 1, UnitID: 2})
	require.NoError(t, err)
	require.True(t, found)
}

// TestCreateConflictReportsRecordingFailureInsteadOfConflict pins a deliberate
// trade-off: when the conflict warning cannot be recorded, the caller is told
// the platform failed rather than being handed a clean 409, because the
// rejected attempt would otherwise leave no trace at all.
func TestCreateConflictReportsRecordingFailureInsteadOfConflict(t *testing.T) {
	// Fails only once the first request's three events are recorded.
	recorder := &recordingRecorder{err: errors.New("sink is gone"), failAfter: 3}
	service, err := NewService(Location{Machine: "node-a", IP: "127.0.0.1"}, SingleInstanceCoordinator{}, recorder)
	require.NoError(t, err)
	_, err = service.Create(context.Background(), api.RegistrationRequest{UnitType: 7, UnitID: 42, UnitTypeNameAdvertised: "First"})
	require.NoError(t, err)

	_, err = service.Create(context.Background(), api.RegistrationRequest{UnitType: 7, UnitID: 42, UnitTypeNameAdvertised: "Second"})
	require.ErrorContains(t, err, "registration: record platform.registration.conflict")
	require.ErrorContains(t, err, "sink is gone")
	require.NotErrorIs(t, err, ErrConflict)
}

func TestConfirmReportsRecordingFailureWithContext(t *testing.T) {
	recorder := &recordingRecorder{err: errors.New("sink is gone"), failAfter: 1}
	coordinator := &heldCoordinator{}
	service, err := NewService(Location{Machine: "node-a", IP: "127.0.0.1"}, coordinator, recorder)
	require.NoError(t, err)
	_, err = service.Create(context.Background(), api.RegistrationRequest{UnitType: 1, UnitID: 2, UnitTypeNameAdvertised: "Worker"})
	require.NoError(t, err)

	confirm := coordinator.confirmFunc(t)
	err = confirm(context.Background(), api.RegistrationStatusAccepted, nil)
	require.ErrorContains(t, err, "registration: record platform.registration.confirmed")
	require.ErrorContains(t, err, "sink is gone")
}
