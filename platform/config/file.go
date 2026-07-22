package config

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"strings"

	"github.com/BurntSushi/toml"
)

// file is the schema of the platform's TOML configuration file. It carries the
// settings a user may set without touching the embedded deployment descriptor.
// Neither the API address nor the runtime directory is here. Both are an
// instance's own, both are resolved onto the instance's descriptor record, and a
// file that still sets either fails at rejectUnknownKeys rather than being
// quietly ignored.
type file struct {
	ReadHeaderTimeout string `toml:"read_header_timeout"`
	ShutdownTimeout   string `toml:"shutdown_timeout"`
	// LagBound bounds how long a process's projection may lag the journal before it
	// stops being ready to take over, and before an active process stops serving rather than
	// answering from a stale view. It is required and must be positive.
	LagBound    string      `toml:"lag_bound"`
	EventFabric EventFabric `toml:"event_fabric"`
}

// EventFabric carries per-adapter runtime settings for the Event Fabric. It is
// keyed by adapter because the settings are adapter-specific by nature; the
// Event Fabric abstraction itself has nothing to configure.
type EventFabric struct {
	// Nats configures the embedded NATS JetStream adapter.
	Nats EventFabricNats `toml:"nats"`
}

// EventFabricNats are the NATS adapter's runtime settings.
//
// Only CredentialsFile is a site's own decision: a site holds its own transport
// secret, at its own path and its own permissions, and a machine cannot carry one
// in a descriptor that is compiled in and readable by anyone holding the binary.
// The timeouts are bounds a site may need to widen on slow hardware.
//
// The journal's storage is deliberately not here. It moved to the deployment
// descriptor when each instance gained its own Event Fabric server: a machine's
// two instances need two stores, and a single machine-level setting cannot
// express that without the runtime deriving per-instance paths from it. It is
// authored per instance in the blueprint as jetstream_store_dir. A
// configuration file that still sets event_fabric.nats.data_dir fails at
// rejectUnknownKeys, which is the intended outcome rather than an oversight:
// the path it names would not be the path either instance opened.
//
// None of these changes which machine this is: identity, and therefore a
// registration's machine and IP, comes from the embedded descriptor alone and is
// never configurable at a site.
type EventFabricNats struct {
	// CredentialsFile is the path of a TOML file holding the site's NATS
	// username and password. It is required for a deployment that binds any
	// non-loopback address; the adapter enforces that once the addresses are
	// composed. The value is never logged.
	CredentialsFile string `toml:"credentials_file"`
	// StartupTimeout bounds the embedded server's readiness for connections, such
	// as "30s".
	StartupTimeout string `toml:"startup_timeout"`
	// CatchUpTimeout bounds the complete startup readiness sequence: projection
	// replay to the captured high-water mark, handler backlog, and the ready
	// event, such as "30s".
	CatchUpTimeout string `toml:"catch_up_timeout"`
}

// credentials is the schema of the file CredentialsFile points at. It is a
// separate file so a site can hold it at different permissions from the runtime
// configuration, and so the configuration a platform prints at startup never has
// a secret in it to redact.
type credentials struct {
	Username string `toml:"username"`
	Password string `toml:"password"`
}

// loadFile reads and validates the TOML configuration file. No defaults are
// allowed: a file that does not exist or omits required fields yields an error.
func loadFile(path string) (file, error) {
	data, err := os.ReadFile(path)
	if errors.Is(err, fs.ErrNotExist) {
		return file{}, fmt.Errorf("read configuration file %s: file does not exist", path)
	}
	if err != nil {
		return file{}, fmt.Errorf("read configuration file %s: %w", path, err)
	}
	var f file
	meta, err := toml.Decode(string(data), &f)
	if err != nil {
		return file{}, fmt.Errorf("invalid configuration file %s: %w", path, err)
	}
	if err := rejectUnknownKeys(path, meta); err != nil {
		return file{}, err
	}
	f.ReadHeaderTimeout = strings.TrimSpace(f.ReadHeaderTimeout)
	if f.ReadHeaderTimeout == "" {
		return file{}, fmt.Errorf("configuration file %s: read_header_timeout is required", path)
	}
	f.ShutdownTimeout = strings.TrimSpace(f.ShutdownTimeout)
	if f.ShutdownTimeout == "" {
		return file{}, fmt.Errorf("configuration file %s: shutdown_timeout is required", path)
	}
	f.LagBound = strings.TrimSpace(f.LagBound)
	if f.LagBound == "" {
		return file{}, fmt.Errorf("configuration file %s: lag_bound is required", path)
	}
	nats := &f.EventFabric.Nats
	nats.StartupTimeout = strings.TrimSpace(nats.StartupTimeout)
	if nats.StartupTimeout == "" {
		return file{}, fmt.Errorf("configuration file %s: [event_fabric.nats] startup_timeout is required", path)
	}
	nats.CatchUpTimeout = strings.TrimSpace(nats.CatchUpTimeout)
	if nats.CatchUpTimeout == "" {
		return file{}, fmt.Errorf("configuration file %s: [event_fabric.nats] catch_up_timeout is required", path)
	}
	// Socket overrides are not required or validated here: what makes an address
	// usable is the adapter's business, so the composed adapter configuration is
	// validated at startup, before any listener opens. The instance's data
	// directory is probed the same way and for the same reason, and it now arrives
	// from the descriptor rather than from here.
	nats.CredentialsFile = strings.TrimSpace(nats.CredentialsFile)
	return f, nil
}

// rejectUnknownKeys fails a configuration file that sets a key this schema does
// not define.
//
// The strictness is the point. A key the decoder does not recognise is silently
// dropped by default, and a setting that is silently dropped looks exactly like
// one that was applied: the platform starts, reports a healthy configuration,
// and behaves as though the file had never mentioned it. That is how a NATS
// socket override left over from an earlier schema turns into a machine
// connecting to an address nobody wrote down. Deployment topology comes from the
// descriptor, so an obsolete override here has no correct interpretation and
// must not be treated as one.
//
// The error names every unknown key rather than only the first, so a stale file
// is fixed in one pass.
func rejectUnknownKeys(path string, meta toml.MetaData) error {
	undecoded := meta.Undecoded()
	if len(undecoded) == 0 {
		return nil
	}
	keys := make([]string, 0, len(undecoded))
	for _, key := range undecoded {
		keys = append(keys, key.String())
	}
	return fmt.Errorf("configuration file %s: unknown key(s): %s", path, strings.Join(keys, ", "))
}

// loadCredentials reads the site's NATS username and password from path. The
// error never quotes the file's content: a malformed secret file must report
// where it is, not what is in it.
func loadCredentials(path string) (credentials, error) {
	data, err := os.ReadFile(path)
	if errors.Is(err, fs.ErrNotExist) {
		return credentials{}, fmt.Errorf("read credentials file %s: file does not exist", path)
	}
	if err != nil {
		return credentials{}, fmt.Errorf("read credentials file %s: %w", path, err)
	}
	var c credentials
	if err := toml.Unmarshal(data, &c); err != nil {
		return credentials{}, fmt.Errorf("invalid credentials file %s: not valid TOML", path)
	}
	c.Username = strings.TrimSpace(c.Username)
	c.Password = strings.TrimSpace(c.Password)
	if c.Username == "" {
		return credentials{}, fmt.Errorf("credentials file %s: username is required", path)
	}
	if c.Password == "" {
		return credentials{}, fmt.Errorf("credentials file %s: password is required", path)
	}
	return c, nil
}
