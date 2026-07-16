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
	Address           string       `toml:"address"`
	EventsDir         string       `toml:"events_dir"`
	ReadHeaderTimeout string       `toml:"read_header_timeout"`
	ShutdownTimeout   string       `toml:"shutdown_timeout"`
	Fabric            Fabric       `toml:"fabric"`
	Registration      Registration `toml:"registration"`
}

// Registration carries the runtime settings of the registration use case.
type Registration struct {
	// ReconcileInterval overrides how often this platform instance scans the
	// site's registration requests, as a Go duration such as "500ms".
	ReconcileInterval string `toml:"reconcile_interval"`
}

// Fabric carries per-adapter runtime overrides for the platform fabric. It is
// keyed by adapter because the settings are adapter-specific by nature; the
// fabric abstraction itself has nothing to configure.
type Fabric struct {
	// Olric configures the embedded Olric adapter.
	Olric FabricOlric `toml:"olric"`
}

// FabricOlric are the Olric adapter's runtime overrides.
//
// They exist for development hosts where the deployment's real addresses are not
// bindable, and for scenarios that run several machines on one host. Production
// needs none of them: the adapter derives everything from the descriptor.
//
// These settings move sockets and nothing else. None of them changes which
// machine this is: identity, and therefore a registration's machine and IP, come
// from the embedded descriptor alone and are never configurable at a site.
type FabricOlric struct {
	// ClientAddress overrides the member's client host:port.
	ClientAddress string `toml:"client_address"`
	// MemberlistAddress overrides the member's membership host:port.
	MemberlistAddress string `toml:"memberlist_address"`
	// Join overrides the memberlist addresses of the peers to seed from. An
	// explicit empty list is not an override; omit the field to keep the peers
	// the descriptor derived.
	Join []string `toml:"join"`
	// StartTimeout bounds the readiness duration at startup, such as "30s".
	StartTimeout string `toml:"start_timeout"`
	// ShutdownGrace bounds the teardown grace duration at shutdown, such as "10s".
	ShutdownGrace string `toml:"shutdown_grace"`
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
	if err := toml.Unmarshal(data, &f); err != nil {
		return file{}, fmt.Errorf("invalid configuration file %s: %w", path, err)
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
	f.Registration.ReconcileInterval = strings.TrimSpace(f.Registration.ReconcileInterval)
	if f.Registration.ReconcileInterval == "" {
		return file{}, fmt.Errorf("configuration file %s: [registration] reconcile_interval is required", path)
	}
	f.Fabric.Olric.StartTimeout = strings.TrimSpace(f.Fabric.Olric.StartTimeout)
	if f.Fabric.Olric.StartTimeout == "" {
		return file{}, fmt.Errorf("configuration file %s: [fabric.olric] start_timeout is required", path)
	}
	f.Fabric.Olric.ShutdownGrace = strings.TrimSpace(f.Fabric.Olric.ShutdownGrace)
	if f.Fabric.Olric.ShutdownGrace == "" {
		return file{}, fmt.Errorf("configuration file %s: [fabric.olric] shutdown_grace is required", path)
	}
	// An events directory is not validated here. A path is only known to be
	// usable once it is opened, so the runtime validates it by constructing the
	// sink at startup rather than trusting a check that could go stale.
	f.EventsDir = strings.TrimSpace(f.EventsDir)
	// Fabric socket overrides are not required or validated here: what makes an
	// address usable is the adapter's business, so the composed adapter
	// configuration is validated at startup, before any listener opens.
	f.Fabric.Olric.ClientAddress = strings.TrimSpace(f.Fabric.Olric.ClientAddress)
	f.Fabric.Olric.MemberlistAddress = strings.TrimSpace(f.Fabric.Olric.MemberlistAddress)
	return f, nil
}
