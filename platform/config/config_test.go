package config_test

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/miroslav-matejovsky/opdl/platform/config"
)

const validSections = `
read_header_timeout = "5s"
shutdown_timeout = "10s"
lag_bound = "30s"
`

// validBaseConfig is a complete configuration file. It sets no API address and no
// runtime directory: both are an instance's own, both come from the descriptor,
// and a file that sets either is rejected. See TestLoadRejectsInstanceSettings.
const validBaseConfig = validSections

// writeFile writes contents into a temp dir under name and returns its path.
func writeFile(t *testing.T, name, contents string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), name)
	require.NoError(t, os.WriteFile(path, []byte(contents), 0o600))
	return path
}

// writeConfig writes a TOML configuration file into a temp dir and returns its
// path.
func writeConfig(t *testing.T, contents string) string {
	t.Helper()
	return writeFile(t, "config.toml", contents)
}

func TestLoadComposesDescriptorAndAddress(t *testing.T) {
	cfg, err := config.Load(writeConfig(t, validBaseConfig))
	require.NoError(t, err)

	d := cfg.Descriptor()
	require.Equal(t, "opdl", d.Platform)
	require.Equal(t, "mock", d.Machine)
	require.Equal(t, []string{"core-services"}, d.Services)

	// The address is the instance's, carried on its own descriptor record.
	// Config exposes it: the runtime reads its own instance's record.
	require.Equal(t, "127.0.0.1:8080", d.Instances.Primary.APIAddress)
	require.Empty(t, d.Instances.Standby.APIAddress, "the mock machine deploys no standby")
}

func TestLoadMissingFileFails(t *testing.T) {
	_, err := config.Load(filepath.Join(t.TempDir(), "absent.toml"))
	require.ErrorContains(t, err, "does not exist")
}

func TestLoadMissingRequiredSettingsFails(t *testing.T) {
	tests := []struct {
		name     string
		contents string
		err      string
	}{
		{
			name: "missing read_header_timeout",
			contents: `shutdown_timeout = "10s"
lag_bound = "30s"`,
			err: "read_header_timeout is required",
		},
		{
			name: "missing shutdown_timeout",
			contents: `read_header_timeout = "5s"
lag_bound = "30s"`,
			err: "shutdown_timeout is required",
		},
		{
			name: "missing lag_bound",
			contents: `read_header_timeout = "5s"
shutdown_timeout = "10s"`,
			err: "lag_bound is required",
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			_, err := config.Load(writeConfig(t, test.contents))
			require.ErrorContains(t, err, test.err)
		})
	}
}

func TestLoadRejectsUnusableDurations(t *testing.T) {
	_, err := config.Load(writeConfig(t, `read_header_timeout = "invalid"
shutdown_timeout = "10s"
lag_bound = "30s"`))
	require.ErrorContains(t, err, `read_header_timeout "invalid"`)

	_, err = config.Load(writeConfig(t, `read_header_timeout = "5s"
shutdown_timeout = "10s"
lag_bound = "0s"`))
	require.ErrorContains(t, err, "duration must be positive")
}

// TestLoadReadsLagBound checks the required projection lag bound: a positive
// duration is read, while absent, zero, and negative values are refused.
func TestLoadReadsLagBound(t *testing.T) {
	// withLagBound renders a valid config carrying the given top-level lag_bound.
	withLagBound := func(bound string) string {
		return `read_header_timeout = "5s"
shutdown_timeout = "10s"
lag_bound = ` + bound + `
`
	}

	set, err := config.Load(writeConfig(t, withLagBound(`"3s"`)))
	require.NoError(t, err)
	require.Equal(t, 3*time.Second, set.LagBound())

	_, err = config.Load(writeConfig(t, withLagBound(`"-1s"`)))
	require.ErrorContains(t, err, "lag_bound")
	require.ErrorContains(t, err, "must be positive")

	_, err = config.Load(writeConfig(t, withLagBound(`"0s"`)))
	require.ErrorContains(t, err, "must be positive")
}

// TestLoadRejectsUnknownKeys checks a configuration file that sets something
// this schema does not define fails at load time.
func TestLoadRejectsUnknownKeys(t *testing.T) {
	t.Run("unknown top-level key", func(t *testing.T) {
		_, err := config.Load(writeConfig(t, validBaseConfig+"totally_made_up = 1\n"))
		require.ErrorContains(t, err, "unknown key")
		require.ErrorContains(t, err, "totally_made_up")
	})

	t.Run("removed event_fabric section", func(t *testing.T) {
		_, err := config.Load(writeConfig(t, validBaseConfig+"[event_fabric.nats]\nstartup_timeout = \"30s\"\n"))
		require.ErrorContains(t, err, "unknown key")
		require.ErrorContains(t, err, "event_fabric")
	})
}

func TestLoadRejectsMalformedFile(t *testing.T) {
	_, err := config.Load(writeConfig(t, `[invalid`))
	require.ErrorContains(t, err, "invalid configuration file")
}

// TestLoadRejectsInstanceSettings checks a configuration file cannot state
// anything a single instance binds or writes.
func TestLoadRejectsInstanceSettings(t *testing.T) {
	tests := map[string]string{
		"address":      `address = "127.0.0.1:9090"` + "\n",
		"instance_dir": `instance_dir = "/var/lib/opdl/instance"` + "\n",
		"runtime_dir":  `runtime_dir = "/var/lib/opdl/instance/primary"` + "\n",
	}
	for key, setting := range tests {
		t.Run(key, func(t *testing.T) {
			_, err := config.Load(writeConfig(t, setting+validSections))
			require.ErrorContains(t, err, "unknown key")
			require.ErrorContains(t, err, key)
		})
	}
}

func TestSummaryShowsConfiguration(t *testing.T) {
	cfg, err := config.Load(writeConfig(t, validBaseConfig))
	require.NoError(t, err)

	s := cfg.Summary(false)
	require.Contains(t, s, "platform configuration (machine=mock)")
	require.Contains(t, s, "deployment descriptor")
	require.Contains(t, s, "peers        mock/primary (127.0.0.1)")
	require.Contains(t, s, "lease        (not deployed)")
	require.Contains(t, s, "read_header_timeout 5s")
	require.Contains(t, s, "shutdown_timeout    10s")
	require.Contains(t, s, "lag_bound           30s")
	require.Contains(t, s, "data_dir     .data/platform/primary")
	require.Contains(t, s, "event_storage .data/journal/primary")
}

// TestSummaryNamesBothInstancesAndMarksThisOne checks a startup block states
// where each instance serves and which one printed it.
func TestSummaryNamesBothInstancesAndMarksThisOne(t *testing.T) {
	cfg, err := config.Load(writeConfig(t, validBaseConfig))
	require.NoError(t, err)

	require.Contains(t, cfg.Summary(false), "primary=127.0.0.1:8080 (this instance)")
	require.Contains(t, cfg.Summary(false), "standby=(not deployed)")
	require.NotContains(t, cfg.Summary(true), "primary=127.0.0.1:8080 (this instance)",
		"the marker follows the role the block was rendered for")
}
