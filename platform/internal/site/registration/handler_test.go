package registration

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/miroslav-matejovsky/opdl/platform/api"
)

func TestNewHandlerReturnsNotImplemented(t *testing.T) {
	_, err := NewHandler(anyPublisher(t), NewProjection(), locations()[1], locations())
	require.ErrorIs(t, err, api.ErrNotImplemented)
}
