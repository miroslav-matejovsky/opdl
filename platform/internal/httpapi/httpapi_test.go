package httpapi_test

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/miroslav-matejovsky/opdl/platform/api"
	"github.com/miroslav-matejovsky/opdl/platform/deployment"
	"github.com/miroslav-matejovsky/opdl/platform/internal/events"
	"github.com/miroslav-matejovsky/opdl/platform/internal/fabric/memory"
	"github.com/miroslav-matejovsky/opdl/platform/internal/httpapi"
	"github.com/miroslav-matejovsky/opdl/platform/internal/registration"
)

// TestHandlerServesARequestFromPendingToAccepted is the API's side of the
// two-phase story: 202 does not mean registered, the status endpoint says
// pending while an expected machine has not answered, and it turns to accepted
// once that machine does.
func TestHandlerServesARequestFromPendingToAccepted(t *testing.T) {
	site := newSite(t, "node-a", "node-b")
	srv := httptest.NewServer(httpapi.NewHandler(site.start("node-a")))
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
	require.Zero(t, response.ContentLength, "202 says the request was taken, not that anything was registered")

	// node-b is expected and is not running, so the site cannot accept this.
	response = do(t, http.MethodGet, srv.URL+"/registrations/7/42/status", nil, "")
	defer func() { _ = response.Body.Close() }()
	require.Equal(t, http.StatusOK, response.StatusCode)
	view := decodeRegistration(t, response)
	require.Equal(t, api.RegistrationStatusPending, view.Status)
	require.Equal(t, api.RegistrationStatusAccepted, instanceStatus(t, view, "node-a").Status)
	require.Equal(t, api.RegistrationStatusPending, instanceStatus(t, view, "node-b").Status)

	response = do(t, http.MethodGet, srv.URL+"/registrations", nil, "")
	defer func() { _ = response.Body.Close() }()
	var registrations []api.Registration
	require.NoError(t, json.NewDecoder(response.Body).Decode(&registrations))
	require.Len(t, registrations, 1, "a pending request is listed like any other")
	require.Equal(t, api.RegistrationStatusPending, registrations[0].Status)

	// Start the machine the site was waiting for and let it answer.
	site.start("node-b")
	site.reconcile()

	response = do(t, http.MethodGet, srv.URL+"/registrations/7/42/status", nil, "")
	defer func() { _ = response.Body.Close() }()
	view = decodeRegistration(t, response)
	require.Equal(t, api.RegistrationStatusAccepted, view.Status)
	require.Equal(t, "node-a", view.Machine)
	require.Equal(t, "127.0.0.1", view.IP)
	require.Equal(t, api.RoleMaster, *view.Role)
	require.Equal(t, api.RegistrationStatusAccepted, instanceStatus(t, view, "node-a").Status)
	require.Equal(t, api.RegistrationStatusAccepted, instanceStatus(t, view, "node-b").Status)
}

// TestHandlerReturnsNotFoundAwayFromTheOrigin checks the status endpoint is the
// origin's: another machine of the site holds the same request and still answers
// 404, because it is not who the client asked.
func TestHandlerReturnsNotFoundAwayFromTheOrigin(t *testing.T) {
	site := newSite(t, "node-a", "node-b")
	origin := httptest.NewServer(httpapi.NewHandler(site.start("node-a")))
	defer origin.Close()
	other := httptest.NewServer(httpapi.NewHandler(site.start("node-b")))
	defer other.Close()

	response := do(t, http.MethodPost, origin.URL+"/registrations", []byte(`{"unit_type": 7, "unit_id": 42, "unit_type_name_advertised": "Billing"}`), "application/json")
	require.Equal(t, http.StatusAccepted, response.StatusCode)
	require.NoError(t, response.Body.Close())
	site.reconcile()

	response = do(t, http.MethodGet, other.URL+"/registrations/7/42/status", nil, "")
	defer func() { _ = response.Body.Close() }()
	require.Equal(t, http.StatusNotFound, response.StatusCode)

	// The list, unlike the status, is the site's state and answers anywhere.
	response = do(t, http.MethodGet, other.URL+"/registrations", nil, "")
	defer func() { _ = response.Body.Close() }()
	var registrations []api.Registration
	require.NoError(t, json.NewDecoder(response.Body).Decode(&registrations))
	require.Len(t, registrations, 1)
	require.Equal(t, "node-a", registrations[0].Machine)
	require.Equal(t, api.RegistrationStatusAccepted, registrations[0].Status)
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

// site is the handler tests' deployment: the machines of one site sharing one
// in-process fabric, so a test can serve one machine's API while deciding which
// of the machines it waits for are running.
type site struct {
	t       *testing.T
	shared  *memory.Site
	peers   []deployment.FabricPeer
	running []*registration.Reconciler
}

// newSite declares a site of machines and starts none of them. Machine i is at
// 127.0.0.(i+1).
func newSite(t *testing.T, machines ...string) *site {
	t.Helper()
	s := &site{t: t, shared: memory.NewSite()}
	for i, machine := range machines {
		s.peers = append(s.peers, deployment.FabricPeer{
			Site: "local", Machine: machine, IP: fmt.Sprintf("127.0.0.%d", i+1),
		})
	}
	return s
}

// start brings one expected machine up and returns the service its HTTP API
// would serve.
func (s *site) start(machine string) *registration.Service {
	s.t.Helper()
	descriptor := deployment.Descriptor{Site: "local"}
	for _, peer := range s.peers {
		if peer.Machine == machine {
			descriptor.Machine, descriptor.IP = peer.Machine, peer.IP
			continue
		}
		descriptor.Fabric.Peers = append(descriptor.Fabric.Peers, peer)
	}
	require.NotEmpty(s.t, descriptor.Machine, "%s is not a machine of this site", machine)

	f := s.shared.Open(descriptor)
	service, reconciler, err := registration.Open(f, events.NopRecorder{})
	require.NoError(s.t, err)
	s.t.Cleanup(func() { _ = f.Close(context.Background()) })
	s.running = append(s.running, reconciler)
	return service
}

// reconcile lets the site settle, which is what its schedulers do on their own.
func (s *site) reconcile() {
	s.t.Helper()
	for range 2 {
		for _, reconciler := range s.running {
			require.NoError(s.t, reconciler.Reconcile(context.Background()))
		}
	}
}

// newService builds the registration a one-machine site's platform serves. Its
// own answer is the whole site's, so a request it takes is accepted as soon as
// anything looks at it, which is all these tests need from the domain.
func newService(t *testing.T) *registration.Service {
	t.Helper()
	return newSite(t, "node-a").start("node-a")
}

// instanceStatus returns one platform instance's entry in a registration view.
func instanceStatus(t *testing.T, view api.Registration, machine string) api.PlatformInstanceRegistrationStatus {
	t.Helper()
	for _, instance := range view.PlatformInstances {
		if instance.Machine == machine {
			return instance
		}
	}
	require.FailNow(t, "no platform instance entry", "machine %s not in %v", machine, view.PlatformInstances)
	return api.PlatformInstanceRegistrationStatus{}
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
