package config

import (
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestEventStorageSettingsValidatesLagBound(t *testing.T) {
	empty := file{}
	_, err := eventStorageSettings("config.toml", empty)
	require.ErrorContains(t, err, "lag_bound is required")

	stated := file{LagBound: "not-a-duration"}
	_, err = eventStorageSettings("config.toml", stated)
	require.ErrorContains(t, err, "invalid lag_bound")

	stated = file{LagBound: "-5s"}
	_, err = eventStorageSettings("config.toml", stated)
	require.ErrorContains(t, err, "duration must be positive")

	complete := file{LagBound: "30s"}
	lagBound, err := eventStorageSettings("config.toml", complete)
	require.NoError(t, err)
	require.Equal(t, 30*time.Second, lagBound)
}
