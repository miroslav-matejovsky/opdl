package api_test

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/danielgtaylor/huma/v2/adapters/humago"
	"github.com/stretchr/testify/require"

	"github.com/miroslav-matejovsky/opdl/platform/api"
)

// stubHandlers is an in-memory Handlers implementation for boundary tests. It
// records proposals and can be told to fail the journal, so the HTTP behavior
// can be exercised without the registration domain or the event transport.
type stubHandlers struct {
	created    []api.RegistrationRequest
	proposals  map[string]api.Registration
	journalErr error
}

func newStub() *stubHandlers {
	return &stubHandlers{proposals: map[string]api.Registration{}}
}

func (s *stubHandlers) handlers() api.Handlers {
	return api.Handlers{
		Create: func(_ context.Context, req api.RegistrationRequest) (api.ProposalAccepted, error) {
			if s.journalErr != nil {
				return api.ProposalAccepted{}, s.journalErr
			}
			s.created = append(s.created, req)
			id := "p-1"
			s.proposals[id] = api.Registration{
				ProposalID:             id,
				UnitType:               req.UnitType,
				UnitID:                 req.UnitID,
				UnitTypeNameAdvertised: req.UnitTypeNameAdvertised,
				Role:                   req.Role,
				Status:                 api.RegistrationStatusPending,
			}
			return api.ProposalAccepted{ProposalID: id}, nil
		},
		List: func() []api.Registration {
			out := make([]api.Registration, 0, len(s.proposals))
			for _, r := range s.proposals {
				out = append(out, r)
			}
			return out
		},
		Get: func(id string) (api.Registration, bool) {
			r, ok := s.proposals[id]
			return r, ok
		},
		Conflicts: func() ([]api.RegistrationConflict, error) {
			return []api.RegistrationConflict{}, nil
		},
	}
}

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

func TestServesRegistrationLifecycle(t *testing.T) {
	srv := serve(t, newStub().handlers())

	response := do(t, http.MethodPost, srv.URL+"/registrations",
		[]byte(`{"unit_type":7,"unit_id":42,"unit_type_name_advertised":"Billing","role":"Master"}`))
	require.Equal(t, http.StatusAccepted, response.StatusCode)
	var accepted api.ProposalAccepted
	decode(t, response, &accepted)
	require.Equal(t, "p-1", accepted.ProposalID)

	response = do(t, http.MethodGet, srv.URL+"/registrations", nil)
	require.Equal(t, http.StatusOK, response.StatusCode)
	var list []api.Registration
	decode(t, response, &list)
	require.Len(t, list, 1)

	response = do(t, http.MethodGet, srv.URL+"/registrations/p-1", nil)
	require.Equal(t, http.StatusOK, response.StatusCode)
	var view api.Registration
	decode(t, response, &view)
	require.Equal(t, api.RegistrationStatusPending, view.Status)

	response = do(t, http.MethodGet, srv.URL+"/registrations/conflicts", nil)
	require.Equal(t, http.StatusOK, response.StatusCode)
	var conflicts []api.RegistrationConflict
	decode(t, response, &conflicts)
	require.Empty(t, conflicts)
}

func TestReportsUnavailableJournalAs503(t *testing.T) {
	stub := newStub()
	stub.journalErr = api.ErrJournalUnavailable
	srv := serve(t, stub.handlers())

	response := do(t, http.MethodPost, srv.URL+"/registrations",
		[]byte(`{"unit_type":1,"unit_id":2,"unit_type_name_advertised":"Worker"}`))
	require.Equal(t, http.StatusServiceUnavailable, response.StatusCode)

	var problem struct {
		Detail string `json:"detail"`
		Status int    `json:"status"`
	}
	decode(t, response, &problem)
	require.Equal(t, "journal_unavailable", problem.Detail)
	require.Equal(t, http.StatusServiceUnavailable, problem.Status)
}

func TestReturnsNotFoundForUnknownProposal(t *testing.T) {
	srv := serve(t, newStub().handlers())

	response := do(t, http.MethodGet, srv.URL+"/registrations/unknown", nil)
	require.Equal(t, http.StatusNotFound, response.StatusCode)
	var problem struct {
		Detail string `json:"detail"`
	}
	decode(t, response, &problem)
	require.Equal(t, "registration_not_found", problem.Detail)
}

// TestRejectsSchemaViolations documents that huma validates the request against
// the generated schema before the handler runs: an out-of-range integer and an
// unknown field are both refused as 422 without reaching the handler.
func TestRejectsSchemaViolations(t *testing.T) {
	stub := newStub()
	srv := serve(t, stub.handlers())

	for _, body := range []string{
		`{"unit_type":256,"unit_id":1,"unit_type_name_advertised":"Worker"}`,
		`{"unit_type":1,"unit_id":1,"unit_type_name_advertised":"Worker","machine":"spoofed"}`,
	} {
		response := do(t, http.MethodPost, srv.URL+"/registrations", []byte(body))
		require.Equal(t, http.StatusUnprocessableEntity, response.StatusCode, body)
		require.NoError(t, response.Body.Close())
	}
	require.Empty(t, stub.created, "a schema violation never reaches the handler")
}

func TestServesHealthEndpoints(t *testing.T) {
	srv := serve(t, newStub().handlers())

	response := do(t, http.MethodGet, srv.URL+"/health", nil)
	require.Equal(t, http.StatusOK, response.StatusCode)
	var health api.HealthResponse
	decode(t, response, &health)
	require.Equal(t, api.HealthStatusHealthy, health.Status)
	require.Equal(t, "Primary", health.InstanceID)

	response = do(t, http.MethodGet, srv.URL+"/health/live", nil)
	require.Equal(t, http.StatusOK, response.StatusCode)
	var live api.HealthLiveResponse
	decode(t, response, &live)
	require.Equal(t, api.HealthStatusHealthy, live.Status)

	response = do(t, http.MethodGet, srv.URL+"/health/ready", nil)
	require.Equal(t, http.StatusOK, response.StatusCode)
	var ready api.HealthReadyResponse
	decode(t, response, &ready)
	require.Equal(t, api.HealthStatusHealthy, ready.Status)

	response = do(t, http.MethodGet, srv.URL+"/health/ha", nil)
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
	for _, id := range []string{"registerUnit", "listRegistrations", "listRegistrationConflicts", "getRegistrationStatus", "getHealth", "getHealthLive", "getHealthReady", "getHealthHA"} {
		require.Contains(t, yaml, "operationId: "+id)
	}
	require.NotContains(t, yaml, "$schema", "the schema-link hook is cleared in Config")
}

func do(t *testing.T, method, url string, body []byte) *http.Response {
	t.Helper()
	request, err := http.NewRequestWithContext(t.Context(), method, url, bytes.NewReader(body))
	require.NoError(t, err)
	if body != nil {
		request.Header.Set("Content-Type", "application/json")
	}
	response, err := http.DefaultClient.Do(request)
	require.NoError(t, err)
	return response
}

func decode(t *testing.T, response *http.Response, target any) {
	t.Helper()
	defer func() { _ = response.Body.Close() }()
	require.NoError(t, json.NewDecoder(response.Body).Decode(target))
}
