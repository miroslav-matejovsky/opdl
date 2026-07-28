package api_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

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
