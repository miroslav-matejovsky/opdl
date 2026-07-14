package config

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestParseAppliesPortDefault(t *testing.T) {
	cfg, err := parse([]byte(`{"machine":"m","runtime":{}}`))
	require.NoError(t, err)
	require.Equal(t, defaultPort, cfg.Port())
}

func TestParseKeepsExplicitPort(t *testing.T) {
	cfg, err := parse([]byte(`{"machine":"m","runtime":{"port":9090}}`))
	require.NoError(t, err)
	require.Equal(t, 9090, cfg.Port())
}

func TestParseRejectsMalformedJSON(t *testing.T) {
	_, err := parse([]byte(`{`))
	require.ErrorContains(t, err, "invalid platform configuration")
}
