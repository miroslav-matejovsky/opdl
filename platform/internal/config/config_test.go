package config_test

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/miroslav-matejovsky/opdl/platform/internal/config"
)

const validSections = `
read_header_timeout = "5s"
shutdown_timeout = "10s"
instance_dir = "/var/lib/opdl/instance"
lag_bound = "30s"
[event_fabric.nats]
data_dir = "/var/lib/opdl/nats"
startup_timeout = "30s"
catch_up_timeout = "25s"
`
const validBaseConfig = `address = "127.0.0.1:9090"` + "\n" + validSections

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
	require.Equal(t, "127.0.0.1:9090", cfg.Address())
}

func TestLoadReadsEventFabricSettings(t *testing.T) {
	cfg, err := config.Load(writeConfig(t, `address = "127.0.0.1:9090"
read_header_timeout = "5s"
shutdown_timeout = "10s"
instance_dir = "/var/lib/opdl/instance"
lag_bound = "30s"
[event_fabric.nats]
data_dir = " /var/lib/opdl/nats "
startup_timeout = "30s"
catch_up_timeout = "25s"
`))
	require.NoError(t, err)

	nats := cfg.EventFabric().Nats
	require.Equal(t, "/var/lib/opdl/nats", nats.DataDir, "surrounding space is trimmed")
	require.Equal(t, "30s", nats.StartupTimeout)
	require.Equal(t, "25s", nats.CatchUpTimeout)
}

// TestLoadAcceptsUnusableDataDir documents that a configured path is not checked
// here. Only writing to it proves it is usable, so the Event Fabric adapter
// validates it by probing at startup, before it binds a listener.
func TestLoadAcceptsUnusableDataDir(t *testing.T) {
	cfg, err := config.Load(writeConfig(t, `address = "127.0.0.1:9090"
read_header_timeout = "5s"
shutdown_timeout = "10s"
instance_dir = "/var/lib/opdl/instance"
lag_bound = "30s"
[event_fabric.nats]
data_dir = "\\\\no-such-host\\share"
startup_timeout = "30s"
catch_up_timeout = "25s"
`))
	require.NoError(t, err)
	require.Equal(t, `\\no-such-host\share`, cfg.EventFabric().Nats.DataDir)
}

// TestLoadReadsCredentialsFromTheirOwnFile checks the secret is composed in but
// never lands in the settings the startup summary renders.
func TestLoadReadsCredentialsFromTheirOwnFile(t *testing.T) {
	secrets := writeFile(t, "creds.toml", "username = \"opdl\"\npassword = \"s3cret\"\n")
	cfg, err := config.Load(writeConfig(t, validBaseConfig+"credentials_file = "+quote(secrets)+"\n"))
	require.NoError(t, err)

	username, password := cfg.Credentials()
	require.Equal(t, "opdl", username)
	require.Equal(t, "s3cret", password)
	require.NotContains(t, cfg.Summary(), "s3cret", "a secret never reaches the startup block")
	require.Contains(t, cfg.Summary(), "credentials_file="+filepath.ToSlash(secrets),
		"the block names the file instead")
}

func TestLoadWithoutCredentialsFileIsUnauthenticated(t *testing.T) {
	cfg, err := config.Load(writeConfig(t, validBaseConfig))
	require.NoError(t, err)

	username, password := cfg.Credentials()
	require.Empty(t, username)
	require.Empty(t, password)
	require.Contains(t, cfg.Summary(), "credentials_file=(none: loopback only)")
}

// TestLoadRejectsAnUnusableCredentialsFile checks a configured secret that
// cannot be used fails at startup rather than when a listener refuses a
// connection, and that the failure never quotes what it read.
func TestLoadRejectsAnUnusableCredentialsFile(t *testing.T) {
	t.Run("missing file", func(t *testing.T) {
		_, err := config.Load(writeConfig(t, validBaseConfig+`credentials_file = "/no/such/creds.toml"`+"\n"))
		require.ErrorContains(t, err, "credentials file")
		require.ErrorContains(t, err, "does not exist")
	})

	t.Run("malformed file does not leak its contents", func(t *testing.T) {
		secrets := writeFile(t, "creds.toml", "username = \"opdl\"\npassword = s3cret\n")
		_, err := config.Load(writeConfig(t, validBaseConfig+"credentials_file = "+quote(secrets)+"\n"))
		require.ErrorContains(t, err, "not valid TOML")
		require.NotContains(t, err.Error(), "s3cret")
	})

	t.Run("missing password", func(t *testing.T) {
		secrets := writeFile(t, "creds.toml", "username = \"opdl\"\n")
		_, err := config.Load(writeConfig(t, validBaseConfig+"credentials_file = "+quote(secrets)+"\n"))
		require.ErrorContains(t, err, "password is required")
	})

	t.Run("missing username", func(t *testing.T) {
		secrets := writeFile(t, "creds.toml", "password = \"s3cret\"\n")
		_, err := config.Load(writeConfig(t, validBaseConfig+"credentials_file = "+quote(secrets)+"\n"))
		require.ErrorContains(t, err, "username is required")
	})
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
instance_dir = "/var/lib/opdl/instance"
lag_bound = "30s"
[event_fabric.nats]
data_dir = "/var/lib/opdl/nats"
startup_timeout = "30s"
catch_up_timeout = "25s"`,
			err: "address is required",
		},
		{
			name: "missing read_header_timeout",
			contents: `address = "127.0.0.1:8080"
shutdown_timeout = "10s"
instance_dir = "/var/lib/opdl/instance"
lag_bound = "30s"
[event_fabric.nats]
data_dir = "/var/lib/opdl/nats"
startup_timeout = "30s"
catch_up_timeout = "25s"`,
			err: "read_header_timeout is required",
		},
		{
			name: "missing shutdown_timeout",
			contents: `address = "127.0.0.1:8080"
read_header_timeout = "5s"
[event_fabric.nats]
data_dir = "/var/lib/opdl/nats"
startup_timeout = "30s"
catch_up_timeout = "25s"`,
			err: "shutdown_timeout is required",
		},
		{
			name: "missing lag_bound",
			contents: `address = "127.0.0.1:8080"
read_header_timeout = "5s"
shutdown_timeout = "10s"
instance_dir = "/var/lib/opdl/instance"
[event_fabric.nats]
data_dir = "/var/lib/opdl/nats"
startup_timeout = "30s"
catch_up_timeout = "25s"`,
			err: "lag_bound is required",
		},
		{
			name: "missing data_dir",
			contents: `address = "127.0.0.1:8080"
read_header_timeout = "5s"
shutdown_timeout = "10s"
instance_dir = "/var/lib/opdl/instance"
lag_bound = "30s"
[event_fabric.nats]
startup_timeout = "30s"
catch_up_timeout = "25s"`,
			err: "[event_fabric.nats] data_dir is required",
		},
		{
			name: "missing startup_timeout",
			contents: `address = "127.0.0.1:8080"
read_header_timeout = "5s"
shutdown_timeout = "10s"
instance_dir = "/var/lib/opdl/instance"
lag_bound = "30s"
[event_fabric.nats]
data_dir = "/var/lib/opdl/nats"
catch_up_timeout = "25s"`,
			err: "[event_fabric.nats] startup_timeout is required",
		},
		{
			name: "missing catch_up_timeout",
			contents: `address = "127.0.0.1:8080"
read_header_timeout = "5s"
shutdown_timeout = "10s"
instance_dir = "/var/lib/opdl/instance"
lag_bound = "30s"
[event_fabric.nats]
data_dir = "/var/lib/opdl/nats"
startup_timeout = "30s"`,
			err: "[event_fabric.nats] catch_up_timeout is required",
		},
		{
			name: "the whole event fabric section is missing",
			contents: `address = "127.0.0.1:8080"
read_header_timeout = "5s"
shutdown_timeout = "10s"
instance_dir = "/var/lib/opdl/instance"
lag_bound = "30s"`,
			err: "[event_fabric.nats] data_dir is required",
		},
		{
			name: "missing instance_dir",
			contents: `address = "127.0.0.1:8080"
read_header_timeout = "5s"
shutdown_timeout = "10s"
[event_fabric.nats]
data_dir = "/var/lib/opdl/nats"
startup_timeout = "30s"
catch_up_timeout = "25s"`,
			err: "instance_dir is required",
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
	_, err := config.Load(writeConfig(t, `address = "127.0.0.1:9090"
read_header_timeout = "5s"
shutdown_timeout = "10s"
instance_dir = "/var/lib/opdl/instance"
lag_bound = "30s"
[event_fabric.nats]
data_dir = "/var/lib/opdl/nats"
startup_timeout = "soon"
catch_up_timeout = "25s"
`))
	require.ErrorContains(t, err, `[event_fabric.nats] startup_timeout "soon"`)

	_, err = config.Load(writeConfig(t, `address = "127.0.0.1:9090"
read_header_timeout = "5s"
shutdown_timeout = "10s"
instance_dir = "/var/lib/opdl/instance"
lag_bound = "30s"
[event_fabric.nats]
data_dir = "/var/lib/opdl/nats"
startup_timeout = "30s"
catch_up_timeout = "0s"
`))
	require.ErrorContains(t, err, "duration must be positive")
}

// TestLoadReadsLagBound checks the required projection lag bound: a positive
// duration is read, while absent, zero, and negative values are refused.
func TestLoadReadsLagBound(t *testing.T) {
	// withLagBound renders a valid config carrying the given top-level lag_bound.
	withLagBound := func(bound string) string {
		return `address = "127.0.0.1:9090"
read_header_timeout = "5s"
shutdown_timeout = "10s"
instance_dir = "/var/lib/opdl/instance"
lag_bound = ` + bound + `
[event_fabric.nats]
data_dir = "/var/lib/opdl/nats"
startup_timeout = "30s"
catch_up_timeout = "25s"
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

// TestLoadReadsInstanceDir checks the required local runtime directory is read
// and trimmed.
func TestLoadReadsInstanceDir(t *testing.T) {
	cfg, err := config.Load(writeConfig(t, validBaseConfig))
	require.NoError(t, err)
	require.Equal(t, "/var/lib/opdl/instance", cfg.InstanceDir())
}

// TestLoadRejectsUnknownKeys checks a configuration file that sets something
// this schema does not define fails at load time.
//
// The obsolete socket overrides are the case that matters. They were silently
// ignored while the decoder tolerated unknown keys, so a machine kept using the
// deployment's addresses while its configuration file said otherwise and the
// platform reported a healthy startup either way. The failure that produced was
// slow to diagnose precisely because nothing said the setting had been dropped.
func TestLoadRejectsUnknownKeys(t *testing.T) {
	tests := map[string]struct {
		extra string
		want  string
	}{
		"obsolete nats client address": {
			extra: "client_address = \"127.0.0.1:4222\"\n",
			want:  "event_fabric.nats.client_address",
		},
		"obsolete nats cluster address": {
			extra: "cluster_address = \"127.0.0.1:6222\"\n",
			want:  "event_fabric.nats.cluster_address",
		},
		"obsolete nats monitor address": {
			extra: "monitor_address = \"127.0.0.1:8222\"\n",
			want:  "event_fabric.nats.monitor_address",
		},
		"obsolete nats routes": {
			extra: "routes = [\"127.0.0.2:6222\"]\n",
			want:  "event_fabric.nats.routes",
		},
		"obsolete nats servers": {
			extra: "servers = [\"127.0.0.1:4222\"]\n",
			want:  "event_fabric.nats.servers",
		},
	}
	for name, test := range tests {
		t.Run(name, func(t *testing.T) {
			_, err := config.Load(writeConfig(t, validBaseConfig+test.extra))
			require.ErrorContains(t, err, "unknown key")
			require.ErrorContains(t, err, test.want)
		})
	}

	t.Run("unknown top-level key", func(t *testing.T) {
		_, err := config.Load(writeConfig(t, validBaseConfig+"totally_made_up = 1\n"))
		require.ErrorContains(t, err, "unknown key")
		require.ErrorContains(t, err, "totally_made_up")
	})

	t.Run("every unknown key is named", func(t *testing.T) {
		_, err := config.Load(writeConfig(t, validBaseConfig+"client_address = \"127.0.0.1:4222\"\nmonitor_address = \"127.0.0.1:8222\"\n"))
		require.ErrorContains(t, err, "event_fabric.nats.client_address")
		require.ErrorContains(t, err, "event_fabric.nats.monitor_address")
	})
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
	require.Contains(t, s, "event_fabric one-member site")
	require.Contains(t, s, "address             127.0.0.1:9090")
	require.Contains(t, s, "read_header_timeout 5s")
	require.Contains(t, s, "shutdown_timeout    10s")
	require.Contains(t, s, "instance_dir        /var/lib/opdl/instance")
	require.Contains(t, s, "lag_bound           30s")
	require.Contains(t, s, "data_dir=/var/lib/opdl/nats")
	require.Contains(t, s, "startup_timeout=30s")
	require.Contains(t, s, "catch_up_timeout=25s")
}

// quote renders a path as a TOML basic string. A Windows path is full of
// backslashes, which TOML reads as escapes.
func quote(path string) string {
	return `"` + filepath.ToSlash(path) + `"`
}
