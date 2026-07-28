package api_test

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/danielgtaylor/huma/v2/adapters/humago"
	"github.com/stretchr/testify/require"

	"github.com/miroslav-matejovsky/opdl/platform/api"
)

func serve(t *testing.T, h api.Handlers) *httptest.Server {
	t.Helper()
	mux := http.NewServeMux()
	cfg := api.Config()
	cfg.OpenAPIPath, cfg.DocsPath, cfg.SchemasPath = "", "", ""
	api.Register(humago.New(mux, cfg), h)
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	return srv
}

func TestServesHealthEndpoints(t *testing.T) {
	srv := serve(t, api.Handlers{})

	response := get(t, srv.URL+"/health")
	require.Equal(t, http.StatusOK, response.StatusCode)
	var health api.HealthResponse
	decode(t, response, &health)
	require.Equal(t, api.HealthStatusHealthy, health.Status)
	require.Equal(t, "Primary", health.InstanceID)

	response = get(t, srv.URL+"/health/live")
	require.Equal(t, http.StatusOK, response.StatusCode)
	var live api.HealthLiveResponse
	decode(t, response, &live)
	require.Equal(t, api.HealthStatusHealthy, live.Status)

	response = get(t, srv.URL+"/health/ready")
	require.Equal(t, http.StatusOK, response.StatusCode)
	var ready api.HealthReadyResponse
	decode(t, response, &ready)
	require.Equal(t, api.HealthStatusHealthy, ready.Status)

	response = get(t, srv.URL+"/health/ha")
	require.Equal(t, http.StatusOK, response.StatusCode)
	var ha api.HealthHAResponse
	decode(t, response, &ha)
	require.Equal(t, api.InstanceStateActive, ha.RuntimeState)
	require.Equal(t, api.LeaseStateOwned, ha.LeaseState)
}

// testIdentity is the instance the health handlers under test report on.
func testIdentity() api.Instance {
	return api.Instance{
		Machine: "node-a",
		Role:    api.InstanceRolePrimary,
		State:   api.InstanceStateActive,
		Address: "127.0.0.1:8080",
	}
}

// health serves the handlers NewHealth builds for a runtime whose event fabric
// answers with fabricErr.
func health(t *testing.T, fabricErr error) *httptest.Server {
	t.Helper()
	check := func(context.Context) error { return fabricErr }
	return serve(t, api.NewHealth(testIdentity, time.Now(), nil, check))
}

func TestAWorkingEventFabricIsReportedHealthy(t *testing.T) {
	var response api.HealthResponse
	decode(t, get(t, health(t, nil).URL+"/health"), &response)

	require.Equal(t, api.HealthStatusHealthy, response.Status)
	require.Equal(t, api.HealthStatusHealthy, response.Checks[api.HealthCheckEventFabric])
}

func TestAFailingEventFabricDegradesTheInstanceRatherThanFailingIt(t *testing.T) {
	var response api.HealthResponse
	decode(t, get(t, health(t, errors.New("nothing came back")).URL+"/health"), &response)

	// Unhealthy here is what a Passive instance promotes through, and moving
	// Primary Ownership would not fix a broken fabric: the other instance runs
	// its own broker. So the instance says what is wrong with it and stays the
	// machine's serving instance.
	require.Equal(t, api.HealthStatusDegraded, response.Status)
	require.Equal(t, api.HealthStatusUnhealthy, response.Checks[api.HealthCheckEventFabric])
	require.NotEqual(t, api.HealthStatusUnhealthy, response.Status)
}

func TestAnInstanceWithNoEventFabricWiredReportsNoFabricCheck(t *testing.T) {
	// Spec generation and the boundary tests serve the shape with no runtime
	// behind it. Claiming a healthy fabric there would report on something that
	// is not running.
	var response api.HealthResponse
	decode(t, get(t, serve(t, api.NewHealth(testIdentity, time.Now(), nil, nil)).URL+"/health"), &response)

	require.Equal(t, api.HealthStatusHealthy, response.Status)
	require.NotContains(t, response.Checks, api.HealthCheckEventFabric)
}

func TestLivenessIgnoresTheEventFabric(t *testing.T) {
	// Liveness answers whether this process is running. A dependency it happens
	// to hold has no say in that, and a liveness probe that failed on one would
	// have the process restarted for something a restart does not fix.
	srv := health(t, errors.New("nothing came back"))

	var live api.HealthLiveResponse
	decode(t, get(t, srv.URL+"/health/live"), &live)
	require.Equal(t, api.HealthStatusHealthy, live.Status)
}

// TestOpenAPIYAMLIsDowngraded anchors OpenAPIYAML and confirms it produces the
// downgraded 3.0.3 document with the platform's operations.
func TestOpenAPIYAMLIsDowngraded(t *testing.T) {
	doc, err := api.OpenAPIYAML()
	require.NoError(t, err)
	yaml := string(doc)
	require.Contains(t, yaml, "openapi: 3.0.3")
	for _, id := range []string{"getInstance", "getHealth", "getHealthLive", "getHealthReady", "getHealthHA"} {
		require.Contains(t, yaml, "operationId: "+id)
	}
	require.NotContains(t, yaml, "$schema", "the schema-link hook is cleared in Config")
}

func get(t *testing.T, url string) *http.Response {
	t.Helper()
	request, err := http.NewRequestWithContext(t.Context(), http.MethodGet, url, http.NoBody)
	require.NoError(t, err)
	response, err := http.DefaultClient.Do(request)
	require.NoError(t, err)
	return response
}

func decode(t *testing.T, response *http.Response, target any) {
	t.Helper()
	defer func() { _ = response.Body.Close() }()
	require.NoError(t, json.NewDecoder(response.Body).Decode(target))
}
