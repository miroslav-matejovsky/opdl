package config_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/miroslav-matejovsky/opdl/platform/internal/config"
)

// writeConfig writes a JSON configuration file into a temp dir and returns its
// path.
func writeConfig(t *testing.T, contents string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "config.json")
	require.NoError(t, os.WriteFile(path, []byte(contents), 0o644))
	return path
}

func TestLoadComposesDescriptorAndAddress(t *testing.T) {
	cfg, err := config.Load(writeConfig(t, `{"address": "127.0.0.1:9090"}`))
	require.NoError(t, err)

	d := cfg.Descriptor()
	require.Equal(t, "opdl", d.Platform)
	require.Equal(t, "mock", d.Machine)
	require.Equal(t, []string{"core-services"}, d.Services)
	require.Equal(t, "127.0.0.1:9090", cfg.Address())
}

func TestLoadMissingFileFallsBackToDefaultAddress(t *testing.T) {
	cfg, err := config.Load(filepath.Join(t.TempDir(), "absent.json"))
	require.NoError(t, err)
	require.Equal(t, "127.0.0.1:8080", cfg.Address())
}

func TestLoadEmptyAddressFallsBackToDefault(t *testing.T) {
	cfg, err := config.Load(writeConfig(t, `{"address": ""}`))
	require.NoError(t, err)
	require.Equal(t, "127.0.0.1:8080", cfg.Address())
}

func TestLoadRejectsMalformedFile(t *testing.T) {
	_, err := config.Load(writeConfig(t, `{`))
	require.ErrorContains(t, err, "invalid configuration file")
}

func TestLoadRejectsAddressWithoutPort(t *testing.T) {
	_, err := config.Load(writeConfig(t, `{"address": "127.0.0.1"}`))
	require.ErrorContains(t, err, "invalid address")
}

func TestLoadRejectsPortOutOfRange(t *testing.T) {
	_, err := config.Load(writeConfig(t, `{"address": "127.0.0.1:70000"}`))
	require.ErrorContains(t, err, "out of range")
}

func TestSummaryShowsDescriptorAndAddress(t *testing.T) {
	cfg, err := config.Load(writeConfig(t, `{"address": "127.0.0.1:9090"}`))
	require.NoError(t, err)

	s := cfg.Summary()
	require.Contains(t, s, "platform configuration (machine=mock)")
	require.Contains(t, s, "deployment descriptor")
	require.Contains(t, s, "address      127.0.0.1:9090")
}
