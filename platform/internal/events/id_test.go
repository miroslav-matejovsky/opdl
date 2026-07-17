package events

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestNewIDIsUniqueAndTimeOrdered(t *testing.T) {
	previous := NewID()
	require.Len(t, previous, 37)

	for range 100 {
		current := NewID()
		require.NotEqual(t, previous, current)
		require.LessOrEqual(t, strings.Split(previous, "-")[0], strings.Split(current, "-")[0])
		previous = current
	}
}
