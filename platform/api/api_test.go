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
			Summary:     "Request unit registration",
			RequestBody: &api.RequestBody{Type: api.RegistrationRequest{}, Required: true},
			Responses: []api.Response{
				{Status: http.StatusAccepted},
				{Status: http.StatusBadRequest, Body: api.Error{}},
				{Status: http.StatusConflict, Body: api.Error{}},
				{Status: http.StatusInternalServerError, Body: api.Error{}},
			},
		},
		{
			Method:      http.MethodGet,
			Path:        "/registrations/{unit_type}/{unit_id}/status",
			OperationID: "getRegistrationStatus",
			Summary:     "Get registration request status",
			PathParameters: []api.PathParameter{
				{Name: "unit_type", Type: uint8(0), Required: true},
				{Name: "unit_id", Type: uint16(0), Required: true},
			},
			Responses: []api.Response{
				{Status: http.StatusOK, Body: api.Registration{}},
				{Status: http.StatusNotFound, Body: api.Error{}},
				{Status: http.StatusInternalServerError, Body: api.Error{}},
			},
		},
		{
			Method:      http.MethodGet,
			Path:        "/registrations",
			OperationID: "listRegistrations",
			Summary:     "List registration requests",
			Responses: []api.Response{
				{Status: http.StatusOK, Body: []api.Registration{}},
				{Status: http.StatusInternalServerError, Body: api.Error{}},
			},
		},
	}, c.Operations)
}
