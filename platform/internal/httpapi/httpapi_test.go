package httpapi_test

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/miroslav-matejovsky/opdl/platform/api"
	"github.com/miroslav-matejovsky/opdl/platform/internal/httpapi"
)

// problemDetails is the subset of huma's RFC 9457 error body these tests read.
// huma serves errors as application/problem+json; the stable machine code the
// platform sets is carried in detail.
type problemDetails struct {
	Status   int           `json:"status"`
	Title    string        `json:"title"`
	Detail   string        `json:"detail"`
	Instance *api.Instance `json:"instance,omitempty"`
}

// testInstance is what an Active instance reports about itself in these tests.
func testInstance() api.Instance {
	return api.Instance{
		Machine:     "node-a",
		Role:        "primary",
		State:       api.InstanceStateActive,
		Address:     "127.0.0.1:8080",
		PeerAddress: "127.0.0.1:8081",
	}
}

// testPassiveInstance is the same machine's Standby Instance while it is Passive,
// pointing back at the Primary Instance that holds ownership.
func testPassiveInstance() api.Instance {
	return api.Instance{
		Machine:     "node-a",
		Role:        "standby",
		State:       api.InstanceStatePassive,
		Address:     "127.0.0.1:8081",
		PeerAddress: "127.0.0.1:8080",
	}
}

// testSiteView is the service view both surfaces answer with in these tests.
//
// Its one unit is on the machine's neighbour rather than on node-a. That keeps
// this file's subject to what the two surfaces serve: a unit on node-a would
// also drive the platform's own service monitor check, which api's own tests
// cover.
func testSiteView() api.ServiceHealthResponse {
	return api.ServiceHealthResponse{
		Project:        "customer-a",
		Environment:    "production",
		Site:           "north",
		Machine:        "node-a",
		GeneratedAtUTC: time.Now().UTC().Format(time.RFC3339),
		Summary:        api.ServiceHealthSummary{Healthy: 1},
		Services: []api.ServiceHealthUnit{{
			Machine:           "node-b",
			MachineProfile:    "local-server",
			Service:           "alarm-service",
			ServiceRole:       "master",
			Status:            api.ServiceStatusHealthy,
			ExpectedObservers: []string{"primary"},
		}},
		Distribution: api.ServiceHealthDistribution{State: api.DistributionConnected},
	}
}

// deps composes what an instance answers from in these tests: its identity, its
// start time, and the site view it holds. It has no lease and no event fabric,
// which is what a boundary test serves — the runtime supplies both.
func deps(instance func() api.Instance) api.Deps {
	return api.Deps{Instance: instance, Started: time.Now(), ServiceHealth: testSiteView}
}

// TestHandlerServesHealthEndpoints checks that both an Active and a Passive
// instance answer every health endpoint, and that each answer reports the
// instance's own live identity rather than a fabricated one: the Active instance
// owns its lease, the Passive instance does not, and neither invents a lease
// expiration the runtime has no lease subsystem to give.
func TestHandlerServesHealthEndpoints(t *testing.T) {
	activeSrv := httptest.NewServer(httpapi.NewActiveHandler(deps(testInstance)))
	defer activeSrv.Close()

	passiveSrv := httptest.NewServer(httpapi.NewPassiveHandler(deps(testPassiveInstance)))
	defer passiveSrv.Close()

	cases := []struct {
		name      string
		srv       *httptest.Server
		instance  api.Instance
		leaseHeld string
	}{
		{"active", activeSrv, testInstance(), api.LeaseStateOwned},
		{"passive", passiveSrv, testPassiveInstance(), api.LeaseStateUnowned},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var health api.HealthResponse
			decodeGet(t, tc.srv.URL+"/health", &health)
			require.Equal(t, api.HealthStatusHealthy, health.Status)
			require.Equal(t, tc.instance.Role, health.Role)
			require.Equal(t, tc.instance.State, health.RuntimeState)
			require.NotEmpty(t, health.Uptime, "uptime is reported from the process start time")

			var live api.HealthLiveResponse
			decodeGet(t, tc.srv.URL+"/health/live", &live)
			require.Equal(t, api.HealthStatusHealthy, live.Status)

			var ready api.HealthReadyResponse
			decodeGet(t, tc.srv.URL+"/health/ready", &ready)
			require.Equal(t, api.HealthStatusHealthy, ready.Status)

			var ha api.HealthHAResponse
			decodeGet(t, tc.srv.URL+"/health/ha", &ha)
			require.Equal(t, tc.instance.Role, ha.Role)
			require.Equal(t, tc.instance.State, ha.RuntimeState)
			require.Equal(t, tc.leaseHeld, ha.LeaseState)
			require.Nil(t, ha.LeaseExpirationUTC, "there is no lease subsystem to expire yet")

			// Both surfaces serve the site view. It needs neither Primary
			// Ownership nor a projection — every instance builds the whole thing
			// in memory — so an operator gets the same answer from whichever
			// instance they reached, which is the point of asking a Passive one.
			var services api.ServiceHealthResponse
			decodeGet(t, tc.srv.URL+api.PathHealthServices, &services)
			require.Equal(t, "north", services.Site)
			require.Len(t, services.Services, 1)
			require.Equal(t, api.ServiceStatusHealthy, services.Services[0].Status)
		})
	}
}

// decodeGet issues a GET, asserts 200, and decodes the JSON body into target.
func decodeGet(t *testing.T, url string, target any) {
	t.Helper()
	resp := do(t, http.MethodGet, url, nil, "")
	defer func() { _ = resp.Body.Close() }()
	require.Equal(t, http.StatusOK, resp.StatusCode)
	require.NoError(t, json.NewDecoder(resp.Body).Decode(target))
}

func do(t *testing.T, method, url string, body []byte, contentType string) *http.Response {
	t.Helper()
	request, err := http.NewRequestWithContext(t.Context(), method, url, bytes.NewReader(body))
	require.NoError(t, err)
	if contentType != "" {
		request.Header.Set("Content-Type", contentType)
	}
	response, err := http.DefaultClient.Do(request)
	require.NoError(t, err)
	return response
}

// TestPassiveHandlerAnswersForItself checks the one thing a Passive instance can
// answer, and the reason it binds a listener at all: what it is, what it is
// doing, and where the other instance is.
//
// None of it comes from the journal, so it is answerable while the instance's
// projection is still catching up and while it never will.
func TestPassiveHandlerAnswersForItself(t *testing.T) {
	srv := httptest.NewServer(httpapi.NewPassiveHandler(deps(testPassiveInstance)))
	defer srv.Close()

	response := do(t, http.MethodGet, srv.URL+api.PathInstance, nil, "")
	defer func() { _ = response.Body.Close() }()
	require.Equal(t, http.StatusOK, response.StatusCode)

	var instance api.Instance
	require.NoError(t, json.NewDecoder(response.Body).Decode(&instance))
	require.Equal(t, "standby", instance.Role, "the role is fixed at build time")
	require.Equal(t, api.InstanceStatePassive, instance.State, "the state is what changes")
	require.Equal(t, "127.0.0.1:8081", instance.Address)
	require.Equal(t, "127.0.0.1:8080", instance.PeerAddress, "and where to go instead")
}

// TestPassiveHandlerStillReportsUnknownPathsAsNotFound checks the refusal is
// scoped to the operations that exist.
//
// A Passive instance that answered 503 to everything would tell a caller with a
// typo that the platform is temporarily unavailable, and they would retry
// forever. Refusing is about who serves an operation, not about whether it exists.
func TestPassiveHandlerStillReportsUnknownPathsAsNotFound(t *testing.T) {
	srv := httptest.NewServer(httpapi.NewPassiveHandler(deps(testPassiveInstance)))
	defer srv.Close()

	response := do(t, http.MethodGet, srv.URL+"/no-such-operation", nil, "")
	defer func() { _ = response.Body.Close() }()
	require.Equal(t, http.StatusNotFound, response.StatusCode)
}

// TestPassiveHandlerServingModeMatrix tests every method × path combination for
// ModePassive. It proves Passive serves exactly GET/HEAD on health and instance
// endpoints (200), reports 404 for GET/HEAD on any other path since the platform
// has no domain operation, and refuses every non-GET/HEAD write request with 503
// regardless of path: the structural guard does not need a path to exist to
// refuse writing to it.
func TestPassiveHandlerServingModeMatrix(t *testing.T) {
	srv := httptest.NewServer(httpapi.NewPassiveHandler(deps(testPassiveInstance)))
	defer srv.Close()

	methods := []string{
		http.MethodGet,
		http.MethodHead,
		http.MethodPost,
		http.MethodPut,
		http.MethodDelete,
		http.MethodPatch,
	}

	paths := []string{
		"/health",
		"/health/live",
		"/health/ready",
		"/health/ha",
		"/health/services",
		"/instance",
		"/no-such-operation",
	}

	for _, method := range methods {
		for _, path := range paths {
			t.Run(method+" "+path, func(t *testing.T) {
				resp := do(t, method, srv.URL+path, []byte(`{}`), "application/json")
				defer func() { _ = resp.Body.Close() }()

				isHealthOrInstance := path == "/instance" || path == "/health" ||
					path == "/health/live" || path == "/health/ready" || path == "/health/ha" ||
					path == "/health/services"

				if (method == http.MethodGet || method == http.MethodHead) && isHealthOrInstance {
					require.Equal(t, http.StatusOK, resp.StatusCode)
					return
				}

				if (method == http.MethodGet || method == http.MethodHead) && !isHealthOrInstance {
					require.Equal(t, http.StatusNotFound, resp.StatusCode)
					return
				}

				require.Equal(t, http.StatusServiceUnavailable, resp.StatusCode)
				require.Equal(t, "application/problem+json", resp.Header.Get("Content-Type"))

				// A HEAD response carries headers only, so the problem body is
				// checked on every other method.
				if method != http.MethodHead {
					var problem problemDetails
					require.NoError(t, json.NewDecoder(resp.Body).Decode(&problem))
					require.Equal(t, "instance_passive", problem.Title)
				}
			})
		}
	}
}

// testSoloInstance is the only instance of a machine that deploys no standby. It
// holds ownership and has no peer to point at.
func testSoloInstance() api.Instance {
	return api.Instance{
		Machine: "node-a",
		Role:    "primary",
		State:   api.InstanceStateActive,
		Address: "127.0.0.1:8080",
	}
}

// TestActiveHandlerReportsASoloInstanceAsActive checks a machine with no standby
// still reports itself active and healthy: it holds Primary Ownership and there
// is nothing wrong with it, even though it has no domain operation to serve.
func TestActiveHandlerReportsASoloInstanceAsActive(t *testing.T) {
	srv := httptest.NewServer(httpapi.NewActiveHandler(deps(testSoloInstance)))
	defer srv.Close()

	response := do(t, http.MethodGet, srv.URL+api.PathInstance, nil, "")
	defer func() { _ = response.Body.Close() }()
	require.Equal(t, http.StatusOK, response.StatusCode)

	var instance api.Instance
	require.NoError(t, json.NewDecoder(response.Body).Decode(&instance))
	require.Equal(t, api.InstanceStateActive, instance.State,
		"an instance with no peer is not passive; it owns the machine and serves what it can")
	require.Empty(t, instance.PeerAddress)
}

// TestActiveHandlerStillReportsUnknownPathsAsNotFound checks the refusal is
// scoped to the operations that exist, for the same reason the Passive one is.
func TestActiveHandlerStillReportsUnknownPathsAsNotFound(t *testing.T) {
	srv := httptest.NewServer(httpapi.NewActiveHandler(deps(testSoloInstance)))
	defer srv.Close()

	response := do(t, http.MethodGet, srv.URL+"/no-such-operation", nil, "")
	defer func() { _ = response.Body.Close() }()
	require.Equal(t, http.StatusNotFound, response.StatusCode)
}

// TestActiveHandlerAnswersTheSameInstanceOperation checks both instances serve
// the identity operation on one path with one shape, so an operator asks the same
// question of either and the specification describes one endpoint.
func TestActiveHandlerAnswersTheSameInstanceOperation(t *testing.T) {
	srv := httptest.NewServer(httpapi.NewActiveHandler(deps(testInstance)))
	defer srv.Close()

	response := do(t, http.MethodGet, srv.URL+api.PathInstance, nil, "")
	defer func() { _ = response.Body.Close() }()
	require.Equal(t, http.StatusOK, response.StatusCode)

	var instance api.Instance
	require.NoError(t, json.NewDecoder(response.Body).Decode(&instance))
	require.Equal(t, api.InstanceStateActive, instance.State)
	require.Equal(t, "primary", instance.Role)
}
