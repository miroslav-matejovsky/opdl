package registration

import (
	"context"
	"sync"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/miroslav-matejovsky/opdl/platform/api"
)

type heldCoordinator struct {
	mu      sync.Mutex
	confirm func(context.Context, string, *string) error
}

func (c *heldCoordinator) Trigger(_ context.Context, _ Key, confirm func(context.Context, string, *string) error) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.confirm = confirm
	return nil
}

func (c *heldCoordinator) confirmWith(t *testing.T, status string, reason *string) {
	t.Helper()
	c.mu.Lock()
	confirm := c.confirm
	c.mu.Unlock()
	require.NotNil(t, confirm)
	require.NoError(t, confirm(context.Background(), status, reason))
}

func TestServiceProjectsPendingThenAccepted(t *testing.T) {
	coordinator := &heldCoordinator{}
	service, err := NewService(Location{Machine: "node-a", IP: "127.0.0.1"}, coordinator)
	require.NoError(t, err)

	request := api.RegistrationRequest{UnitType: 7, UnitID: 42, UnitTypeNameAdvertised: "Billing"}
	result, err := service.Create(context.Background(), request)
	require.NoError(t, err)
	require.Equal(t, CreateResultNew, result)

	key := Key{UnitType: 7, UnitID: 42}
	pending, found, err := service.Get(context.Background(), key)
	require.NoError(t, err)
	require.True(t, found)
	require.Equal(t, api.RegistrationStatusPending, pending.Status)
	require.Equal(t, api.RegistrationStatusPending, pending.PlatformInstances[0].Status)

	coordinator.confirmWith(t, api.RegistrationStatusAccepted, nil)
	accepted, found, err := service.Get(context.Background(), key)
	require.NoError(t, err)
	require.True(t, found)
	require.Equal(t, api.RegistrationStatusAccepted, accepted.Status)
	require.Equal(t, api.RegistrationStatusAccepted, accepted.PlatformInstances[0].Status)
	require.Equal(t, "node-a", accepted.Machine)
	require.Equal(t, "127.0.0.1", accepted.IP)
}

func TestServiceKeepsRejectedRequestVisible(t *testing.T) {
	coordinator := &heldCoordinator{}
	service, err := NewService(Location{Machine: "node-a", IP: "127.0.0.1"}, coordinator)
	require.NoError(t, err)
	create(t, service, api.RegistrationRequest{
		UnitType: 1, UnitID: 2, UnitTypeNameAdvertised: "Worker",
	})

	reason := "unit_not_supported"
	coordinator.confirmWith(t, api.RegistrationStatusRejected, &reason)

	registrations, err := service.List(context.Background())
	require.NoError(t, err)
	require.Len(t, registrations, 1)
	require.Equal(t, api.RegistrationStatusRejected, registrations[0].Status)
	require.Equal(t, &reason, registrations[0].Reason)
	require.Equal(t, api.RegistrationStatusRejected, registrations[0].PlatformInstances[0].Status)
}

func TestServiceCreateIsIdempotentAndRejectsImmutableDifferences(t *testing.T) {
	role := api.RoleMaster
	request := api.RegistrationRequest{UnitType: 1, UnitID: 2, UnitTypeNameAdvertised: "Worker", Role: &role}
	store := newMemoryStore()
	first, err := newService(Location{Machine: "node-a", IP: "127.0.0.1"}, &heldCoordinator{}, store)
	require.NoError(t, err)
	second, err := newService(Location{Machine: "node-b", IP: "127.0.0.2"}, &heldCoordinator{}, store)
	require.NoError(t, err)

	result, err := first.Create(context.Background(), request)
	require.NoError(t, err)
	require.Equal(t, CreateResultNew, result)
	result, err = first.Create(context.Background(), request)
	require.NoError(t, err)
	require.Equal(t, CreateResultRetry, result)

	_, err = first.Create(context.Background(), api.RegistrationRequest{UnitType: 1, UnitID: 2, UnitTypeNameAdvertised: "Other", Role: &role})
	require.ErrorIs(t, err, ErrConflict)
	_, err = second.Create(context.Background(), request)
	require.ErrorIs(t, err, ErrConflict)

	view, found, err := first.Get(context.Background(), Key{UnitType: 1, UnitID: 2})
	require.NoError(t, err)
	require.True(t, found)
	require.Equal(t, "Worker", view.UnitTypeNameAdvertised)
	require.Equal(t, "node-a", view.Machine)
}

func TestServiceValidatesInputAndOriginLookup(t *testing.T) {
	coordinator := &heldCoordinator{}
	service, err := NewService(Location{Machine: "node-a", IP: "127.0.0.1"}, coordinator)
	require.NoError(t, err)

	_, err = service.Create(context.Background(), api.RegistrationRequest{UnitTypeNameAdvertised: " "})
	require.ErrorContains(t, err, "blank")
	invalidRole := "master"
	_, err = service.Create(context.Background(), api.RegistrationRequest{UnitTypeNameAdvertised: "Worker", Role: &invalidRole})
	require.ErrorContains(t, err, "role")

	_, err = NewService(Location{Machine: "", IP: "127.0.0.1"}, SingleInstanceCoordinator{})
	require.ErrorContains(t, err, "machine")
	_, err = NewService(Location{Machine: "node-a", IP: "invalid"}, SingleInstanceCoordinator{})
	require.ErrorContains(t, err, "IP")

	create(t, service, api.RegistrationRequest{UnitType: 1, UnitID: 2, UnitTypeNameAdvertised: "Worker"})
	other, err := newService(Location{Machine: "node-b", IP: "127.0.0.2"}, SingleInstanceCoordinator{}, service.store)
	require.NoError(t, err)
	_, found, err := other.Get(context.Background(), Key{UnitType: 1, UnitID: 2})
	require.NoError(t, err)
	require.False(t, found)
}

func TestServiceListsDeterministically(t *testing.T) {
	store := newMemoryStore()
	first, err := newService(Location{Machine: "node-b", IP: "127.0.0.2"}, &heldCoordinator{}, store)
	require.NoError(t, err)
	second, err := newService(Location{Machine: "node-a", IP: "127.0.0.1"}, &heldCoordinator{}, store)
	require.NoError(t, err)
	create(t, first, api.RegistrationRequest{UnitType: 2, UnitID: 1, UnitTypeNameAdvertised: "B"})
	create(t, second, api.RegistrationRequest{UnitType: 2, UnitID: 2, UnitTypeNameAdvertised: "A"})
	create(t, second, api.RegistrationRequest{UnitType: 1, UnitID: 9, UnitTypeNameAdvertised: "A"})

	registrations, err := first.List(context.Background())
	require.NoError(t, err)
	require.Equal(t, []api.Registration{
		{UnitType: 1, UnitID: 9, UnitTypeNameAdvertised: "A", Machine: "node-a", IP: "127.0.0.1", Status: api.RegistrationStatusPending, PlatformInstances: []api.PlatformInstanceRegistrationStatus{{Machine: "node-a", IP: "127.0.0.1", Status: api.RegistrationStatusPending}}},
		{UnitType: 2, UnitID: 2, UnitTypeNameAdvertised: "A", Machine: "node-a", IP: "127.0.0.1", Status: api.RegistrationStatusPending, PlatformInstances: []api.PlatformInstanceRegistrationStatus{{Machine: "node-a", IP: "127.0.0.1", Status: api.RegistrationStatusPending}}},
		{UnitType: 2, UnitID: 1, UnitTypeNameAdvertised: "B", Machine: "node-b", IP: "127.0.0.2", Status: api.RegistrationStatusPending, PlatformInstances: []api.PlatformInstanceRegistrationStatus{{Machine: "node-b", IP: "127.0.0.2", Status: api.RegistrationStatusPending}}},
	}, registrations)
}

func TestServiceAcceptsBothRoles(t *testing.T) {
	for _, role := range []string{api.RoleMaster, api.RoleSlave} {
		t.Run(role, func(t *testing.T) {
			coordinator := &heldCoordinator{}
			service, err := NewService(Location{Machine: "node-a", IP: "127.0.0.1"}, coordinator)
			require.NoError(t, err)
			create(t, service, api.RegistrationRequest{UnitType: 1, UnitID: 2, UnitTypeNameAdvertised: "Worker", Role: &role})

			registration, found, err := service.Get(context.Background(), Key{UnitType: 1, UnitID: 2})
			require.NoError(t, err)
			require.True(t, found)
			require.Equal(t, &role, registration.Role)
		})
	}
}

func TestServiceConcurrentCreatesPreserveOneImmutableRequest(t *testing.T) {
	service, err := NewService(Location{Machine: "node-a", IP: "127.0.0.1"}, &heldCoordinator{})
	require.NoError(t, err)

	requests := []api.RegistrationRequest{
		{UnitType: 1, UnitID: 2, UnitTypeNameAdvertised: "First"},
		{UnitType: 1, UnitID: 2, UnitTypeNameAdvertised: "Second"},
	}
	errors := make(chan error, 32)
	var group sync.WaitGroup
	for i := 0; i < cap(errors); i++ {
		group.Add(1)
		go func(index int) {
			defer group.Done()
			_, createErr := service.Create(context.Background(), requests[index%len(requests)])
			errors <- createErr
		}(i)
	}
	group.Wait()
	close(errors)

	var successful int
	for createErr := range errors {
		if createErr == nil {
			successful++
			continue
		}
		require.ErrorIs(t, createErr, ErrConflict)
	}
	require.Greater(t, successful, 0)

	registration, found, err := service.Get(context.Background(), Key{UnitType: 1, UnitID: 2})
	require.NoError(t, err)
	require.True(t, found)
	require.Contains(t, []string{"First", "Second"}, registration.UnitTypeNameAdvertised)
}

func create(t *testing.T, service *Service, request api.RegistrationRequest) {
	t.Helper()
	_, err := service.Create(context.Background(), request)
	require.NoError(t, err)
}
