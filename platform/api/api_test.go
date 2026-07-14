package api_test

import (
	"net/http"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/miroslav-matejovsky/opdl/platform/api"
)

func TestDescribeReportsStatusOperation(t *testing.T) {
	c := api.Describe()

	require.NotEmpty(t, c.Title)
	require.NotEmpty(t, c.Version)
	require.Len(t, c.Operations, 1)

	op := c.Operations[0]
	require.Equal(t, http.MethodGet, op.Method)
	require.Equal(t, "/", op.Path)
	require.Equal(t, http.StatusOK, op.SuccessStatus)
	require.IsType(t, api.Status{}, op.SuccessBody)
}
