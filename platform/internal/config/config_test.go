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
	require.Equal(t, 8080, cfg.Port())
}

func TestSummaryShowsDescriptorAndRuntime(t *testing.T) {
	cfg, err := config.Load()
	require.NoError(t, err)

	s := cfg.Summary()
	require.Contains(t, s, "platform configuration (machine=mock)")
	require.Contains(t, s, "deployment descriptor")
	require.Contains(t, s, "runtime parameters")
	require.Contains(t, s, "port")
}
