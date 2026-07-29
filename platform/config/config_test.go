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
	require.Equal(t, []config.Service{{
		Name: "core-services",
		Role: "master",
		HealthCheck: config.HealthCheck{
			Type:     "http",
			Port:     9101,
			Path:     "/health",
			Interval: "10s",
			Timeout:  "2s",
			Retries:  3,
		},
	}}, d.Services, "the machine carries the probe policy for what it hosts")

	// The address is the instance's, carried on its own descriptor record.
	require.Equal(t, "127.0.0.1:8080", d.Primary.APIAddress)
	require.Nil(t, d.Standby, "the mock machine deploys no standby")
	require.False(t, d.HasStandby())
}

// TestLoadReadsTheSiteHealthInventory checks the remote half of the health
// contract survives into the runtime: what units exist at the site, who is
// expected to report on each, and how long a report stays fresh.
//
// The mock machine deploys one instance, so its own unit expects one observer.
// That is what makes a machine with no standby different from one whose standby
// has gone quiet: the first never expected a second report.
func TestLoadReadsTheSiteHealthInventory(t *testing.T) {
	cfg, err := config.Load()
	require.NoError(t, err)

	require.Equal(t, []config.SiteService{{
		Machine:        "mock",
		MachineProfile: "all-in-one",
		Service:        "core-services",
		ServiceRole:    "master",
		ObserverRoles:  []string{"primary"},
		FreshFor:       "22s",
	}}, cfg.Descriptor().SiteServices)
}

// TestHealthCheckDerivesFreshness checks the freshness a policy implies: two
// intervals plus one timeout. Every machine of a site derives the same value for
// the same unit, which is what lets receivers expire a report at the same age
// without agreeing on a clock.
func TestHealthCheckDerivesFreshness(t *testing.T) {
	fresh, err := config.HealthCheck{Interval: "30s", Timeout: "5s"}.FreshFor()
	require.NoError(t, err)
	require.Equal(t, 65*time.Second, fresh)

	_, err = config.HealthCheck{Interval: "soon", Timeout: "5s"}.FreshFor()
	require.Error(t, err)

	_, err = config.HealthCheck{Interval: "2562047h47m16.854775807s", Timeout: "1ns"}.FreshFor()
	require.ErrorContains(t, err, "overflows a duration")
}

// TestLoadParsesTheInstanceTimeouts checks the listener timeouts are read from
// the running instance's own record rather than from anything machine-wide.
func TestLoadParsesTheInstanceTimeouts(t *testing.T) {
	cfg, err := config.Load()
	require.NoError(t, err)

	require.Equal(t, 5*time.Second, cfg.ReadHeaderTimeout(false))
	require.Equal(t, 10*time.Second, cfg.ShutdownTimeout(false))
}

// TestLoadHasNoLeaseWithoutAStandby checks a machine that deploys no Standby
// Instance carries no lease: it trades ownership with nobody.
func TestLoadHasNoLeaseWithoutAStandby(t *testing.T) {
	cfg, err := config.Load()
	require.NoError(t, err)

	require.Nil(t, cfg.Descriptor().Lease)
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
	require.Contains(t, s, "events_file  .data/platform/primary/events.jsonl")
	require.Contains(t, s, "state_file   .data/platform/primary/state.json")
	require.Contains(t, s, "log_file     .data/platform/primary/platform.log")
	require.Contains(t, s, "http://127.0.0.1:9101/health",
		"the startup summary names the machine endpoint the platform will probe")
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
