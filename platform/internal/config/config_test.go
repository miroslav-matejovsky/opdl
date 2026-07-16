package config_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/miroslav-matejovsky/opdl/platform/deployment"
	"github.com/miroslav-matejovsky/opdl/platform/internal/config"
)

const validBaseConfig = `read_header_timeout = "5s"
shutdown_timeout = "10s"
[registration]
reconcile_interval = "250ms"
[fabric.olric]
start_timeout = "30s"
shutdown_grace = "10s"
`

// writeConfig writes a TOML configuration file into a temp dir and returns its
// path.
func writeConfig(t *testing.T, contents string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "config.toml")
	require.NoError(t, os.WriteFile(path, []byte(contents), 0o644))
	return path
}

func TestLoadUsesDescriptorEndpointsWithoutOverrides(t *testing.T) {
	cfg, err := config.Load(writeConfig(t, validBaseConfig))
	require.NoError(t, err)

	d := cfg.Descriptor()
	require.Equal(t, "opdl", d.Platform)
	require.Equal(t, "mock", d.Machine)
	require.Equal(t, []string{"core-services"}, d.Services)

	primary, err := cfg.Instance(deployment.PlatformInstancePrimary)
	require.NoError(t, err)
	require.Equal(t, "127.0.0.1:8080", primary.Address())
	require.Equal(t, "127.0.0.1:3320", primary.Fabric().Olric.ClientAddress)
	require.Equal(t, "127.0.0.1:3322", primary.Fabric().Olric.MemberlistAddress)

	secondary, err := cfg.Instance(deployment.PlatformInstanceSecondary)
	require.NoError(t, err)
	require.Equal(t, "127.0.0.1:8081", secondary.Address())
}

func TestLoadAppliesOnlyNamedInstanceOverrides(t *testing.T) {
	cfg, err := config.Load(writeConfig(t, validBaseConfig+`
[instances.primary]
api_address = "127.0.0.1:9090"
[instances.primary.fabric.olric]
client_address = "127.0.0.1:4320"
memberlist_address = "127.0.0.1:4322"
join = []
`))
	require.NoError(t, err)

	primary, err := cfg.Instance(deployment.PlatformInstancePrimary)
	require.NoError(t, err)
	require.Equal(t, "127.0.0.1:9090", primary.Address())
	require.Equal(t, "127.0.0.1:4320", primary.Fabric().Olric.ClientAddress)
	require.Equal(t, "127.0.0.1:4322", primary.Fabric().Olric.MemberlistAddress)
	require.NotNil(t, primary.Fabric().Olric.Join)
	require.Empty(t, primary.Fabric().Olric.Join)

	secondary, err := cfg.Instance(deployment.PlatformInstanceSecondary)
	require.NoError(t, err)
	require.Equal(t, "127.0.0.1:8081", secondary.Address())
	require.Equal(t, "127.0.0.1:3321", secondary.Fabric().Olric.ClientAddress)
}

func TestLoadReadsOptionalEventsDir(t *testing.T) {
	tests := []struct {
		name string
		line string
		want string
	}{
		{name: "absent disables recording"},
		{name: "empty disables recording", line: `events_dir = ""` + "\n"},
		{name: "blank disables recording", line: `events_dir = "   "` + "\n"},
		{name: "configured directory", line: `events_dir = "/var/log/opdl"` + "\n", want: "/var/log/opdl"},
		{name: "surrounding space is trimmed", line: `events_dir = " /var/log/opdl "` + "\n", want: "/var/log/opdl"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			cfg, err := config.Load(writeConfig(t, test.line+validBaseConfig))
			require.NoError(t, err)
			require.Equal(t, test.want, cfg.EventsDir())
		})
	}
}

// TestLoadAcceptsUnwritableEventsDir documents that a configured path is not
// checked here. Only opening it proves it is usable.
func TestLoadAcceptsUnwritableEventsDir(t *testing.T) {
	cfg, err := config.Load(writeConfig(t, `events_dir = '\\no-such-host\share'`+"\n"+validBaseConfig))
	require.NoError(t, err)
	require.Equal(t, `\\no-such-host\share`, cfg.EventsDir())
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
		{"missing read_header_timeout", `shutdown_timeout = "10s"
[registration]
reconcile_interval = "1s"
[fabric.olric]
start_timeout = "30s"
shutdown_grace = "10s"`, "read_header_timeout is required"},
		{"missing shutdown_timeout", `read_header_timeout = "5s"
[registration]
reconcile_interval = "1s"
[fabric.olric]
start_timeout = "30s"
shutdown_grace = "10s"`, "shutdown_timeout is required"},
		{"missing reconcile_interval", `read_header_timeout = "5s"
shutdown_timeout = "10s"
[fabric.olric]
start_timeout = "30s"
shutdown_grace = "10s"`, "[registration] reconcile_interval is required"},
		{"missing start_timeout", `read_header_timeout = "5s"
shutdown_timeout = "10s"
[registration]
reconcile_interval = "1s"
[fabric.olric]
shutdown_grace = "10s"`, "[fabric.olric] start_timeout is required"},
		{"missing shutdown_grace", `read_header_timeout = "5s"
shutdown_timeout = "10s"
[registration]
reconcile_interval = "1s"
[fabric.olric]
start_timeout = "30s"`, "[fabric.olric] shutdown_grace is required"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			_, err := config.Load(writeConfig(t, test.contents))
			require.ErrorContains(t, err, test.err)
		})
	}
}

func TestLoadRejectsInvalidConfiguration(t *testing.T) {
	tests := []struct {
		name     string
		contents string
		err      string
	}{
		{"malformed file", `[invalid`, "invalid configuration file"},
		{"old top-level address", `address = "127.0.0.1:9090"` + "\n" + validBaseConfig, "unknown setting address"},
		{"address without port", validBaseConfig + "\n[instances.primary]\napi_address = \"127.0.0.1\"", "invalid address"},
		{"explicit empty address", validBaseConfig + "\n[instances.primary]\napi_address = \"\"", "non-blank host:port"},
		{"port out of range", validBaseConfig + "\n[instances.primary]\napi_address = \"127.0.0.1:70000\"", "out of range"},
		{"unknown instance", validBaseConfig + "\n[instances.tertiary]\napi_address = \"127.0.0.1:9000\"", `override "tertiary" is not present`},
		{"override collision", validBaseConfig + "\n[instances.secondary]\napi_address = \"127.0.0.1:8080\"", "already used"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			_, err := config.Load(writeConfig(t, test.contents))
			require.ErrorContains(t, err, test.err)
		})
	}
}

func TestInstanceRejectsNameAbsentFromDescriptor(t *testing.T) {
	cfg, err := config.Load(writeConfig(t, validBaseConfig))
	require.NoError(t, err)
	_, err = cfg.Instance("tertiary")
	require.ErrorContains(t, err, `platform instance "tertiary" is not present`)
}

func TestSummaryShowsDescriptorFirstConfiguration(t *testing.T) {
	cfg, err := config.Load(writeConfig(t, validBaseConfig))
	require.NoError(t, err)

	s := cfg.Summary()
	require.Contains(t, s, "platform configuration (machine=mock)")
	require.Contains(t, s, "deployment descriptor")
	require.Contains(t, s, "instance     primary api=127.0.0.1:8080")
	require.Contains(t, s, "instance     secondary api=127.0.0.1:8081")
	require.Contains(t, s, "events_dir          (disabled)")
	require.Contains(t, s, "read_header_timeout 5s")
	require.Contains(t, s, "shutdown_timeout    10s")
	require.Contains(t, s, "registration        reconcile_interval=250ms")
}
