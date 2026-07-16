package config_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/miroslav-matejovsky/opdl/platform/internal/config"
)

const validSections = `
read_header_timeout = "5s"
shutdown_timeout = "10s"
[registration]
reconcile_interval = "250ms"
[fabric.olric]
start_timeout = "30s"
shutdown_grace = "10s"
`
const validBaseConfig = `address = "127.0.0.1:9090"` + "\n" + validSections

// writeConfig writes a TOML configuration file into a temp dir and returns its
// path.
func writeConfig(t *testing.T, contents string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "config.toml")
	require.NoError(t, os.WriteFile(path, []byte(contents), 0o644))
	return path
}

func TestLoadComposesDescriptorAndAddress(t *testing.T) {
	cfg, err := config.Load(writeConfig(t, validBaseConfig))
	require.NoError(t, err)

	d := cfg.Descriptor()
	require.Equal(t, "opdl", d.Platform)
	require.Equal(t, "mock", d.Machine)
	require.Equal(t, []string{"core-services"}, d.Services)
	require.Equal(t, "127.0.0.1:9090", cfg.Address())
}

func TestLoadReadsOptionalEventsDir(t *testing.T) {
	tests := []struct {
		name     string
		contents string
		want     string
	}{
		{name: "absent disables recording", contents: validBaseConfig, want: ""},
		{name: "empty disables recording", contents: `address = "127.0.0.1:9090"` + "\n" + `events_dir = ""` + "\n" + validSections, want: ""},
		{name: "blank disables recording", contents: `address = "127.0.0.1:9090"` + "\n" + `events_dir = "   "` + "\n" + validSections, want: ""},
		{name: "configured directory", contents: `address = "127.0.0.1:9090"` + "\n" + `events_dir = "/var/log/opdl"` + "\n" + validSections, want: "/var/log/opdl"},
		{name: "surrounding space is trimmed", contents: `address = "127.0.0.1:9090"` + "\n" + `events_dir = " /var/log/opdl "` + "\n" + validSections, want: "/var/log/opdl"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			cfg, err := config.Load(writeConfig(t, test.contents))
			require.NoError(t, err)
			require.Equal(t, test.want, cfg.EventsDir())
		})
	}
}

// TestLoadAcceptsUnwritableEventsDir documents that a configured path is not
// checked here. Only opening it proves it is usable, so the runtime validates
// it by constructing the sink at startup.
func TestLoadAcceptsUnwritableEventsDir(t *testing.T) {
	cfg, err := config.Load(writeConfig(t, `address = "127.0.0.1:9090"`+"\n"+`events_dir = "\\\\no-such-host\\share"`+"\n"+validSections))
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
		{
			name: "missing address",
			contents: `read_header_timeout = "5s"
shutdown_timeout = "10s"
[registration]
reconcile_interval = "1s"
[fabric.olric]
start_timeout = "30s"
shutdown_grace = "10s"`,
			err: "address is required",
		},
		{
			name: "missing read_header_timeout",
			contents: `address = "127.0.0.1:8080"
shutdown_timeout = "10s"
[registration]
reconcile_interval = "1s"
[fabric.olric]
start_timeout = "30s"
shutdown_grace = "10s"`,
			err: "read_header_timeout is required",
		},
		{
			name: "missing shutdown_timeout",
			contents: `address = "127.0.0.1:8080"
read_header_timeout = "5s"
[registration]
reconcile_interval = "1s"
[fabric.olric]
start_timeout = "30s"
shutdown_grace = "10s"`,
			err: "shutdown_timeout is required",
		},
		{
			name: "missing reconcile_interval",
			contents: `address = "127.0.0.1:8080"
read_header_timeout = "5s"
shutdown_timeout = "10s"
[fabric.olric]
start_timeout = "30s"
shutdown_grace = "10s"`,
			err: "[registration] reconcile_interval is required",
		},
		{
			name: "missing start_timeout",
			contents: `address = "127.0.0.1:8080"
read_header_timeout = "5s"
shutdown_timeout = "10s"
[registration]
reconcile_interval = "1s"
[fabric.olric]
shutdown_grace = "10s"`,
			err: "[fabric.olric] start_timeout is required",
		},
		{
			name: "missing shutdown_grace",
			contents: `address = "127.0.0.1:8080"
read_header_timeout = "5s"
shutdown_timeout = "10s"
[registration]
reconcile_interval = "1s"
[fabric.olric]
start_timeout = "30s"`,
			err: "[fabric.olric] shutdown_grace is required",
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			_, err := config.Load(writeConfig(t, test.contents))
			require.ErrorContains(t, err, test.err)
		})
	}
}

func TestLoadRejectsMalformedFile(t *testing.T) {
	_, err := config.Load(writeConfig(t, `[invalid`))
	require.ErrorContains(t, err, "invalid configuration file")
}

func TestLoadRejectsAddressWithoutPort(t *testing.T) {
	_, err := config.Load(writeConfig(t, `address = "127.0.0.1"`+"\n"+validSections))
	require.ErrorContains(t, err, "invalid address")
}

func TestLoadRejectsPortOutOfRange(t *testing.T) {
	_, err := config.Load(writeConfig(t, `address = "127.0.0.1:70000"`+"\n"+validSections))
	require.ErrorContains(t, err, "out of range")
}

func TestSummaryShowsConfiguration(t *testing.T) {
	cfg, err := config.Load(writeConfig(t, validBaseConfig))
	require.NoError(t, err)

	s := cfg.Summary()
	require.Contains(t, s, "platform configuration (machine=mock)")
	require.Contains(t, s, "deployment descriptor")
	require.Contains(t, s, "address             127.0.0.1:9090")
	require.Contains(t, s, "events_dir          (disabled)")
	require.Contains(t, s, "read_header_timeout 5s")
	require.Contains(t, s, "shutdown_timeout    10s")
	require.Contains(t, s, "registration        reconcile_interval=250ms")
}
