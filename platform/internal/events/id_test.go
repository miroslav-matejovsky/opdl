package events

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestNewEventIDIsUniqueAndTimeOrdered(t *testing.T) {
	previous, err := newEventID()
	require.NoError(t, err)
	require.Len(t, previous, 37)

	for range 100 {
		current, err := newEventID()
		require.NoError(t, err)
		require.NotEqual(t, previous, current)
		require.LessOrEqual(t, strings.Split(previous, "-")[0], strings.Split(current, "-")[0])
		previous = current
	}
}
