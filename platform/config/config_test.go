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
[event_fabric.nats]
startup_timeout = "30s"
catch_up_timeout = "25s"
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

func TestLoadReadsEventFabricSettings(t *testing.T) {
	cfg, err := config.Load(writeConfig(t, `read_header_timeout = "5s"
shutdown_timeout = "10s"
lag_bound = "30s"
[event_fabric.nats]
startup_timeout = "30s"
catch_up_timeout = "25s"
`))
	require.NoError(t, err)

	nats := cfg.EventFabric().Nats
	require.Equal(t, "30s", nats.StartupTimeout)
	require.Equal(t, "25s", nats.CatchUpTimeout)
}

// TestLoadRejectsADataDirectory guards the tier boundary the journal's storage
// moved across.
//
// It used to be this file's only real setting. It is now authored per instance in
// the blueprint, because each instance runs its own Event Fabric server and two
// servers cannot open one JetStream store, so a single machine-level path could
// not name what either instance opens.
//
// A file carrying the old key must fail rather than be ignored. Ignoring it would
// leave an operator looking at a directory nothing writes to, believing they had
// placed the journal, while both instances used paths from the descriptor.
func TestLoadRejectsADataDirectory(t *testing.T) {
	_, err := config.Load(writeConfig(t, `read_header_timeout = "5s"
shutdown_timeout = "10s"
lag_bound = "30s"
[event_fabric.nats]
data_dir = "D:/opdl/nats"
startup_timeout = "30s"
catch_up_timeout = "25s"
`))
	require.ErrorContains(t, err, "unknown key(s)")
	require.ErrorContains(t, err, "event_fabric.nats.data_dir")
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
	require.NotContains(t, cfg.Summary(false), "s3cret", "a secret never reaches the startup block")
	require.Contains(t, cfg.Summary(false), "credentials_file="+filepath.ToSlash(secrets),
		"the block names the file instead")
}

func TestLoadWithoutCredentialsFileIsUnauthenticated(t *testing.T) {
	cfg, err := config.Load(writeConfig(t, validBaseConfig))
	require.NoError(t, err)

	username, password := cfg.Credentials()
	require.Empty(t, username)
	require.Empty(t, password)
	require.Contains(t, cfg.Summary(false), "credentials_file=(none: loopback only)")
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
			name: "missing read_header_timeout",
			contents: `shutdown_timeout = "10s"
lag_bound = "30s"
[event_fabric.nats]
startup_timeout = "30s"
catch_up_timeout = "25s"`,
			err: "read_header_timeout is required",
		},
		{
			name: "missing shutdown_timeout",
			contents: `read_header_timeout = "5s"
[event_fabric.nats]
startup_timeout = "30s"
catch_up_timeout = "25s"`,
			err: "shutdown_timeout is required",
		},
		{
			name: "missing lag_bound",
			contents: `read_header_timeout = "5s"
shutdown_timeout = "10s"
[event_fabric.nats]
startup_timeout = "30s"
catch_up_timeout = "25s"`,
			err: "lag_bound is required",
		},
		{
			name: "missing startup_timeout",
			contents: `read_header_timeout = "5s"
shutdown_timeout = "10s"
lag_bound = "30s"
[event_fabric.nats]
catch_up_timeout = "25s"`,
			err: "[event_fabric.nats] startup_timeout is required",
		},
		{
			name: "missing catch_up_timeout",
			contents: `read_header_timeout = "5s"
shutdown_timeout = "10s"
lag_bound = "30s"
[event_fabric.nats]
startup_timeout = "30s"`,
			err: "[event_fabric.nats] catch_up_timeout is required",
		},
		{
			name: "the whole event fabric section is missing",
			contents: `read_header_timeout = "5s"
shutdown_timeout = "10s"
lag_bound = "30s"`,
			err: "[event_fabric.nats] startup_timeout is required",
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
	_, err := config.Load(writeConfig(t, `read_header_timeout = "5s"
shutdown_timeout = "10s"
lag_bound = "30s"
[event_fabric.nats]
startup_timeout = "soon"
catch_up_timeout = "25s"
`))
	require.ErrorContains(t, err, `[event_fabric.nats] startup_timeout "soon"`)

	_, err = config.Load(writeConfig(t, `read_header_timeout = "5s"
shutdown_timeout = "10s"
lag_bound = "30s"
[event_fabric.nats]
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
		return `read_header_timeout = "5s"
shutdown_timeout = "10s"
lag_bound = ` + bound + `
[event_fabric.nats]
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

// TestLoadRejectsInstanceSettings checks a configuration file cannot state
// anything a single instance binds or writes.
//
// Both settings used to live here, and both were read by a machine's two
// instances from one file: an address here is one both of them would bind, and a
// runtime directory here is one they would both write into. They now come from
// each instance's own descriptor record, and the unknown-key rule is what turns a
// stale file into a startup failure rather than a setting that is quietly
// ignored while the platform reports a healthy start.
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
	require.Contains(t, s, "lock         (not deployed)")
	require.Contains(t, s, "read_header_timeout 5s")
	require.Contains(t, s, "shutdown_timeout    10s")
	require.Contains(t, s, "lag_bound           30s")
	require.Contains(t, s, "data_dir     .data/platform/primary")
	require.Contains(t, s, "jetstream_store_dir .data/journal/primary")
	require.Contains(t, s, "startup_timeout=30s")
	require.Contains(t, s, "catch_up_timeout=25s")
}

// TestSummaryNamesBothInstancesAndMarksThisOne checks a startup block states
// where each instance serves and which one printed it.
//
// A machine's two instances read one descriptor and one configuration file, so
// without the marker their startup blocks would be identical and an operator
// holding one log could not tell which process it came from.
func TestSummaryNamesBothInstancesAndMarksThisOne(t *testing.T) {
	cfg, err := config.Load(writeConfig(t, validBaseConfig))
	require.NoError(t, err)

	require.Contains(t, cfg.Summary(false), "primary=127.0.0.1:8080 (this instance)")
	require.Contains(t, cfg.Summary(false), "standby=(not deployed)")
	require.NotContains(t, cfg.Summary(true), "primary=127.0.0.1:8080 (this instance)",
		"the marker follows the role the block was rendered for")
}

// quote renders a path as a TOML basic string. A Windows path is full of
// backslashes, which TOML reads as escapes.
func quote(path string) string {
	return `"` + filepath.ToSlash(path) + `"`
}
