package config_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/miroslav-matejovsky/opdl/platform/internal/config"
)

// writeConfig writes a TOML configuration file into a temp dir and returns its
// path.
func writeConfig(t *testing.T, contents string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "config.toml")
	require.NoError(t, os.WriteFile(path, []byte(contents), 0o644))
	return path
}

func TestLoadComposesDescriptorAndAddress(t *testing.T) {
	cfg, err := config.Load(writeConfig(t, `address = "127.0.0.1:9090"`))
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
		{name: "absent disables recording", contents: `address = "127.0.0.1:9090"`, want: ""},
		{name: "empty disables recording", contents: `events_dir = ""`, want: ""},
		{name: "blank disables recording", contents: `events_dir = "   "`, want: ""},
		{name: "configured directory", contents: `events_dir = "/var/log/opdl"`, want: "/var/log/opdl"},
		{name: "surrounding space is trimmed", contents: `events_dir = " /var/log/opdl "`, want: "/var/log/opdl"},
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
	cfg, err := config.Load(writeConfig(t, `events_dir = "\\\\no-such-host\\share"`))
	require.NoError(t, err)
	require.Equal(t, `\\no-such-host\share`, cfg.EventsDir())
}

func TestLoadMissingFileFallsBackToDefaultAddress(t *testing.T) {
	cfg, err := config.Load(filepath.Join(t.TempDir(), "absent.toml"))
	require.NoError(t, err)
	require.Equal(t, "127.0.0.1:8080", cfg.Address())
}

func TestLoadEmptyAddressFallsBackToDefault(t *testing.T) {
	cfg, err := config.Load(writeConfig(t, `address = ""`))
	require.NoError(t, err)
	require.Equal(t, "127.0.0.1:8080", cfg.Address())
}

func TestLoadRejectsMalformedFile(t *testing.T) {
	_, err := config.Load(writeConfig(t, `[invalid`))
	require.ErrorContains(t, err, "invalid configuration file")
}

func TestLoadRejectsAddressWithoutPort(t *testing.T) {
	_, err := config.Load(writeConfig(t, `address = "127.0.0.1"`))
	require.ErrorContains(t, err, "invalid address")
}

func TestLoadRejectsPortOutOfRange(t *testing.T) {
	_, err := config.Load(writeConfig(t, `address = "127.0.0.1:70000"`))
	require.ErrorContains(t, err, "out of range")
}

func TestSummaryShowsDescriptorAndAddress(t *testing.T) {
	cfg, err := config.Load(writeConfig(t, `address = "127.0.0.1:9090"`))
	require.NoError(t, err)

	s := cfg.Summary()
	require.Contains(t, s, "platform configuration (machine=mock)")
	require.Contains(t, s, "deployment descriptor")
	require.Contains(t, s, "address      127.0.0.1:9090")
	require.Contains(t, s, "events_dir   (disabled)")
}

func TestSummaryShowsConfiguredEventsDir(t *testing.T) {
	cfg, err := config.Load(writeConfig(t, `events_dir = "/var/log/opdl"`))
	require.NoError(t, err)
	require.Contains(t, cfg.Summary(), "events_dir   /var/log/opdl")
}

// TestLoadReadsOptionalReconcileInterval checks the setting is carried through
// as written. What makes an interval usable is the registration package's
// business, so it is validated where it is composed rather than here.
func TestLoadReadsOptionalReconcileInterval(t *testing.T) {
	cfg, err := config.Load(writeConfig(t, "[registration]\nreconcile_interval = \" 250ms \""))
	require.NoError(t, err)
	require.Equal(t, config.Registration{ReconcileInterval: "250ms"}, cfg.Registration())
	require.Contains(t, cfg.Summary(), "registration reconcile_interval=250ms")
}

// TestLoadDefaultsTheReconcileInterval checks an absent setting stays absent, so
// composition applies its own default rather than a blank.
func TestLoadDefaultsTheReconcileInterval(t *testing.T) {
	cfg, err := config.Load(writeConfig(t, ""))
	require.NoError(t, err)
	require.Empty(t, cfg.Registration().ReconcileInterval)
	require.Contains(t, cfg.Summary(), "registration (defaults)")
}
