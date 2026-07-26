package config_test

import (
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/miroslav-matejovsky/opdl/platform/config"
)

// Load reads the descriptor compiled into the binary and nothing else, so these
// tests exercise it against the neutral mock in config/deployment.json: a
// standby-less machine with one deployed Primary Instance.

func TestLoadReadsTheEmbeddedDescriptor(t *testing.T) {
	cfg, err := config.Load()
	require.NoError(t, err)

	d := cfg.Descriptor()
	require.Equal(t, "opdl", d.Platform)
	require.Equal(t, "mock", d.Machine)
	require.Equal(t, []string{"core-services"}, d.Services)

	// The address is the instance's, carried on its own descriptor record.
	require.Equal(t, "127.0.0.1:8080", d.Instances.Primary.APIAddress)
	require.Empty(t, d.Instances.Standby.APIAddress, "the mock machine deploys no standby")
}

// TestLoadParsesTheInstanceTimeouts checks the listener timeouts are read from
// the running instance's own record rather than from anything machine-wide.
func TestLoadParsesTheInstanceTimeouts(t *testing.T) {
	cfg, err := config.Load()
	require.NoError(t, err)

	require.Equal(t, 5*time.Second, cfg.ReadHeaderTimeout(false))
	require.Equal(t, 10*time.Second, cfg.ShutdownTimeout(false))
}

// TestLoadHasNoLagBoundWithoutALease checks a machine that deploys no Standby
// Instance carries no lease, and therefore no failover bound: it trades
// ownership with nobody.
func TestLoadHasNoLagBoundWithoutALease(t *testing.T) {
	cfg, err := config.Load()
	require.NoError(t, err)

	require.Nil(t, cfg.Descriptor().Lease)
	require.Zero(t, cfg.LagBound())
}

func TestSummaryShowsConfiguration(t *testing.T) {
	cfg, err := config.Load()
	require.NoError(t, err)

	s := cfg.Summary(false)
	require.Contains(t, s, "platform configuration (machine=mock)")
	require.Contains(t, s, "deployment descriptor")
	require.Contains(t, s, "lease        (not deployed)")
	require.Contains(t, s, "read_header_timeout 5s")
	require.Contains(t, s, "shutdown_timeout    10s")
	require.Contains(t, s, "data_dir     .data/platform/primary")
}

// TestSummaryNamesBothInstancesAndMarksThisOne checks a startup block states
// where each instance serves and which one printed it.
func TestSummaryNamesBothInstancesAndMarksThisOne(t *testing.T) {
	cfg, err := config.Load()
	require.NoError(t, err)

	require.Contains(t, cfg.Summary(false), "primary=127.0.0.1:8080 (this instance)")
	require.Contains(t, cfg.Summary(false), "standby=(not deployed)")
	require.NotContains(t, cfg.Summary(true), "primary=127.0.0.1:8080 (this instance)",
		"the marker follows the role the block was rendered for")
}
