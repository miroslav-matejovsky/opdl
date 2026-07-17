package api_test

import (
	"net/http"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/miroslav-matejovsky/opdl/platform/api"
)

func TestDescribeReportsRegistrationOperations(t *testing.T) {
	c := api.Describe()

	require.NotEmpty(t, c.Title)
	require.NotEmpty(t, c.Version)
	require.Equal(t, []api.Operation{
		{
			Method:      http.MethodPost,
			Path:        "/registrations",
			OperationID: "registerUnit",
			Summary:     "Propose a unit registration",
			RequestBody: &api.RequestBody{Type: api.RegistrationRequest{}, Required: true},
			Responses: []api.Response{
				{Status: http.StatusAccepted, Body: api.ProposalAccepted{}},
				{Status: http.StatusBadRequest, Body: api.Error{}},
				{Status: http.StatusServiceUnavailable, Body: api.Error{}},
			},
		},
		{
			Method:      http.MethodGet,
			Path:        "/registrations/conflicts",
			OperationID: "listRegistrationConflicts",
			Summary:     "List resolved registration conflicts",
			Responses: []api.Response{
				{Status: http.StatusOK, Body: []api.RegistrationConflict{}},
				{Status: http.StatusInternalServerError, Body: api.Error{}},
			},
		},
		{
			Method:      http.MethodGet,
			Path:        "/registrations/{proposal_id}",
			OperationID: "getRegistrationStatus",
			Summary:     "Get a registration proposal's status",
			PathParameters: []api.PathParameter{
				{Name: "proposal_id", Type: "", Required: true},
			},
			Responses: []api.Response{
				{Status: http.StatusOK, Body: api.Registration{}},
				{Status: http.StatusNotFound, Body: api.Error{}},
			},
		},
		{
			Method:      http.MethodGet,
			Path:        "/registrations",
			OperationID: "listRegistrations",
			Summary:     "List registration proposals",
			Responses: []api.Response{
				{Status: http.StatusOK, Body: []api.Registration{}},
			},
		},
	}, c.Operations)
}
