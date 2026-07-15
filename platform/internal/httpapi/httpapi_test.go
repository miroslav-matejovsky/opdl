package httpapi_test

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/miroslav-matejovsky/opdl/platform/api"
	"github.com/miroslav-matejovsky/opdl/platform/internal/httpapi"
	"github.com/miroslav-matejovsky/opdl/platform/internal/registration"
)

func TestHandlerCreatesPendingRegistrationThenListsAndGetsIt(t *testing.T) {
	service, coordinator := newHeldService(t)
	srv := httptest.NewServer(httpapi.NewHandler(service))
	defer srv.Close()

	response := do(t, http.MethodGet, srv.URL+"/registrations", nil, "")
	defer func() { _ = response.Body.Close() }()
	require.Equal(t, http.StatusOK, response.StatusCode)
	var empty []api.Registration
	require.NoError(t, json.NewDecoder(response.Body).Decode(&empty))
	require.Empty(t, empty)

	response = do(t, http.MethodPost, srv.URL+"/registrations", []byte(`{"unit_type": 7, "unit_id": 42, "unit_type_name_advertised": "Billing", "role": "Master"}`), "application/json")
	defer func() { _ = response.Body.Close() }()
	require.Equal(t, http.StatusAccepted, response.StatusCode)

	response = do(t, http.MethodGet, srv.URL+"/registrations/7/42/status", nil, "")
	defer func() { _ = response.Body.Close() }()
	require.Equal(t, http.StatusOK, response.StatusCode)
	view := decodeRegistration(t, response)
	require.Equal(t, api.RegistrationStatusPending, view.Status)
	require.Equal(t, api.RegistrationStatusPending, view.PlatformInstances[0].Status)

	response = do(t, http.MethodGet, srv.URL+"/registrations", nil, "")
	defer func() { _ = response.Body.Close() }()
	var registrations []api.Registration
	require.NoError(t, json.NewDecoder(response.Body).Decode(&registrations))
	require.Len(t, registrations, 1)
	require.Equal(t, api.RegistrationStatusPending, registrations[0].Status)

	coordinator.confirmWith(t, api.RegistrationStatusAccepted, nil)
	response = do(t, http.MethodGet, srv.URL+"/registrations/7/42/status", nil, "")
	defer func() { _ = response.Body.Close() }()
	view = decodeRegistration(t, response)
	require.Equal(t, api.RegistrationStatusAccepted, view.Status)
	require.Equal(t, "node-a", view.Machine)
	require.Equal(t, "127.0.0.1", view.IP)
	require.Equal(t, api.RegistrationStatusAccepted, view.PlatformInstances[0].Status)
	require.Equal(t, api.RoleMaster, *view.Role)
}

type heldCoordinator struct {
	mu       sync.Mutex
	callback func(context.Context, string, *string) error
}

func (c *heldCoordinator) Trigger(_ context.Context, _ registration.Key, confirm func(context.Context, string, *string) error) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.callback = confirm
	return nil
}

func (c *heldCoordinator) confirmWith(t *testing.T, status string, reason *string) {
	t.Helper()
	c.mu.Lock()
	confirm := c.callback
	c.mu.Unlock()
	require.NotNil(t, confirm)
	require.NoError(t, confirm(context.Background(), status, reason))
}

func TestHandlerRejectsInvalidOrSpoofedRequests(t *testing.T) {
	srv := httptest.NewServer(httpapi.NewHandler(newService(t)))
	defer srv.Close()

	for _, body := range []string{
		`{"unit_type": 256, "unit_id": 1, "unit_type_name_advertised": "Worker"}`,
		`{"unit_type": -1, "unit_id": 1, "unit_type_name_advertised": "Worker"}`,
		`{"unit_type": 1, "unit_id": 1, "unit_type_name_advertised": " "}`,
		`{"unit_type": 1, "unit_id": 1, "unit_type_name_advertised": "Worker", "role": "master"}`,
		`{"unit_type": 1, "unit_id": 1, "unit_type_name_advertised": "Worker", "machine": "spoofed"}`,
		`{"unit_type": 1, "unit_id": 1, "unit_type_name_advertised": "Worker"}{}`,
	} {
		response := do(t, http.MethodPost, srv.URL+"/registrations", []byte(body), "application/json")
		require.Equal(t, http.StatusBadRequest, response.StatusCode, body)
		require.Equal(t, "application/json", response.Header.Get("Content-Type"))
		require.NoError(t, response.Body.Close())
	}

	response := do(t, http.MethodPost, srv.URL+"/registrations", []byte(`{}`), "text/plain")
	defer func() { _ = response.Body.Close() }()
	require.Equal(t, http.StatusBadRequest, response.StatusCode)
}

func TestHandlerReturnsConflictAndMethodErrors(t *testing.T) {
	srv := httptest.NewServer(httpapi.NewHandler(newService(t)))
	defer srv.Close()

	response := do(t, http.MethodPost, srv.URL+"/registrations", []byte(`{"unit_type": 1, "unit_id": 2, "unit_type_name_advertised": "Worker"}`), "application/json")
	defer func() { _ = response.Body.Close() }()
	require.Equal(t, http.StatusAccepted, response.StatusCode)
	response = do(t, http.MethodPost, srv.URL+"/registrations", []byte(`{"unit_type": 1, "unit_id": 2, "unit_type_name_advertised": "Other"}`), "application/json")
	defer func() { _ = response.Body.Close() }()
	require.Equal(t, http.StatusConflict, response.StatusCode)
	var conflict api.Error
	require.NoError(t, json.NewDecoder(response.Body).Decode(&conflict))
	require.Equal(t, "registration_key_conflict", conflict.Code)

	response = do(t, http.MethodDelete, srv.URL+"/registrations", nil, "")
	defer func() { _ = response.Body.Close() }()
	require.Equal(t, http.StatusMethodNotAllowed, response.StatusCode)
	require.Equal(t, "GET, POST", response.Header.Get("Allow"))
	response = do(t, http.MethodPost, srv.URL+"/registrations/1/2/status", nil, "")
	defer func() { _ = response.Body.Close() }()
	require.Equal(t, http.StatusMethodNotAllowed, response.StatusCode)
	require.Equal(t, http.MethodGet, response.Header.Get("Allow"))
}

func TestHandlerReturnsNotFoundForUnknownOrInvalidStatusPath(t *testing.T) {
	srv := httptest.NewServer(httpapi.NewHandler(newService(t)))
	defer srv.Close()

	for _, path := range []string{"/registrations/1/2/status", "/registrations/256/2/status", "/registrations/1/2", "/"} {
		response := do(t, http.MethodGet, srv.URL+path, nil, "")
		require.Equal(t, http.StatusNotFound, response.StatusCode, path)
		require.NoError(t, response.Body.Close())
	}
}

func newService(t *testing.T) *registration.Service {
	t.Helper()
	service, err := registration.NewService(registration.Location{Machine: "node-a", IP: "127.0.0.1"}, registration.SingleInstanceCoordinator{})
	require.NoError(t, err)
	return service
}

func newHeldService(t *testing.T) (*registration.Service, *heldCoordinator) {
	t.Helper()
	coordinator := &heldCoordinator{}
	service, err := registration.NewService(registration.Location{Machine: "node-a", IP: "127.0.0.1"}, coordinator)
	require.NoError(t, err)
	return service, coordinator
}

func do(t *testing.T, method, url string, body []byte, contentType string) *http.Response {
	t.Helper()
	request, err := http.NewRequestWithContext(context.Background(), method, url, bytes.NewReader(body))
	require.NoError(t, err)
	if contentType != "" {
		request.Header.Set("Content-Type", contentType)
	}
	response, err := http.DefaultClient.Do(request)
	require.NoError(t, err)
	return response
}

func decodeRegistration(t *testing.T, response *http.Response) api.Registration {
	t.Helper()
	var view api.Registration
	require.NoError(t, json.NewDecoder(response.Body).Decode(&view))
	return view
}
