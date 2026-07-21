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
type file struct {
	Address           string `toml:"address"`
	ReadHeaderTimeout string `toml:"read_header_timeout"`
	ShutdownTimeout   string `toml:"shutdown_timeout"`
	// InstanceDir is the local runtime directory holding this machine's fence
	// and per-process status files. It is required, is shared by both processes of
	// a machine, and must be on a local filesystem. It is not the journal store:
	// it carries local coordination and diagnostics, not site history.
	InstanceDir string `toml:"instance_dir"`
	// LagBound bounds how long a process's projection may lag the journal before it
	// stops being ready to take over, and before an active process stops serving rather than
	// answering from a stale view. It is required and must be positive.
	LagBound    string      `toml:"lag_bound"`
	Operations  Operations  `toml:"operations"`
	EventFabric EventFabric `toml:"event_fabric"`
}

// Operations configures local operational event retention. Structured events
// are always written to the process error stream; EventDir optionally retains
// the same records as JSONL for incident analysis.
type Operations struct {
	EventDir string `toml:"event_dir"`
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
// Only DataDir is a site's own decision: the journal's storage is the one thing
// a machine cannot derive from its descriptor, and site operations own the disk
// it lives on. Everything else is an override that exists for development hosts
// where the deployment's real addresses are not bindable, and for scenarios that
// run several machines on one host.
//
// The overrides move sockets and storage and nothing else. None of them changes
// which machine this is: identity, and therefore a registration's machine and
// IP, come from the embedded descriptor alone and are never configurable at a
// site.
type EventFabricNats struct {
	// DataDir is the directory the JetStream file store lives in. It is required.
	// The platform creates a node-specific subdirectory under it and never
	// deletes it: capacity and backup belong to site operations.
	DataDir string `toml:"data_dir"`
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
	f.Address = strings.TrimSpace(f.Address)
	if f.Address == "" {
		return file{}, fmt.Errorf("configuration file %s: address is required", path)
	}
	if err := validateAddress(f.Address); err != nil {
		return file{}, fmt.Errorf("configuration file %s: %w", path, err)
	}
	f.ReadHeaderTimeout = strings.TrimSpace(f.ReadHeaderTimeout)
	if f.ReadHeaderTimeout == "" {
		return file{}, fmt.Errorf("configuration file %s: read_header_timeout is required", path)
	}
	f.ShutdownTimeout = strings.TrimSpace(f.ShutdownTimeout)
	if f.ShutdownTimeout == "" {
		return file{}, fmt.Errorf("configuration file %s: shutdown_timeout is required", path)
	}
	f.InstanceDir = strings.TrimSpace(f.InstanceDir)
	if f.InstanceDir == "" {
		return file{}, fmt.Errorf("configuration file %s: instance_dir is required", path)
	}
	f.LagBound = strings.TrimSpace(f.LagBound)
	if f.LagBound == "" {
		return file{}, fmt.Errorf("configuration file %s: lag_bound is required", path)
	}
	f.Operations.EventDir = strings.TrimSpace(f.Operations.EventDir)
	nats := &f.EventFabric.Nats
	nats.DataDir = strings.TrimSpace(nats.DataDir)
	if nats.DataDir == "" {
		return file{}, fmt.Errorf("configuration file %s: [event_fabric.nats] data_dir is required", path)
	}
	nats.StartupTimeout = strings.TrimSpace(nats.StartupTimeout)
	if nats.StartupTimeout == "" {
		return file{}, fmt.Errorf("configuration file %s: [event_fabric.nats] startup_timeout is required", path)
	}
	nats.CatchUpTimeout = strings.TrimSpace(nats.CatchUpTimeout)
	if nats.CatchUpTimeout == "" {
		return file{}, fmt.Errorf("configuration file %s: [event_fabric.nats] catch_up_timeout is required", path)
	}
	// The data directory is not probed here. A path is only known to be usable
	// once it is written, so the adapter validates it by probing at startup
	// rather than trusting a check that could go stale.
	//
	// Socket overrides are not required or validated here either: what makes an
	// address usable is the adapter's business, so the composed adapter
	// configuration is validated at startup, before any listener opens.
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
