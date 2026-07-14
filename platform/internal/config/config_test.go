package config_test

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/miroslav-matejovsky/opdl/platform/internal/config"
)

func TestLoadEmbeddedConfig(t *testing.T) {
	cfg, err := config.Load()
	require.NoError(t, err)

	d := cfg.Descriptor()
	require.Equal(t, "opdl", d.Platform)
	require.Equal(t, "mock", d.Machine)
	require.Equal(t, []string{"core-services"}, d.Services)
}

func TestSummaryShowsDescriptor(t *testing.T) {
	cfg, err := config.Load()
	require.NoError(t, err)

	s := cfg.Summary()
	require.Contains(t, s, "platform configuration (machine=mock)")
	require.Contains(t, s, "deployment descriptor")
}
