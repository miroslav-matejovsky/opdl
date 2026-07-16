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
	Address string `toml:"address"`
	// EventsDir is optional: empty disables event recording.
	EventsDir string `toml:"events_dir"`
	// Fabric is optional: every field falls back to the descriptor's topology.
	Fabric Fabric `toml:"fabric"`
	// Registration is optional: every field falls back to a built-in default.
	Registration Registration `toml:"registration"`
}

// Registration carries the runtime settings of the registration use case.
type Registration struct {
	// ReconcileInterval overrides how often this platform instance scans the
	// site's registration requests, as a Go duration such as "500ms". Empty
	// keeps the built-in default.
	//
	// It is a latency setting, not a correctness one. Every registration
	// decision is idempotent and derived from the site's state, so a shorter
	// interval accepts requests sooner and a longer one costs less; neither
	// changes what the site decides.
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
	// StartTimeout overrides the readiness bound, as a Go duration such as
	// "45s". Empty keeps the adapter's default.
	StartTimeout string `toml:"start_timeout"`
}

// loadFile reads and validates the TOML configuration file. A file that does not
// exist yields the defaults; a file that exists must be well-formed and, if it
// sets an address, carry a valid host:port.
func loadFile(path string) (file, error) {
	f := file{Address: defaultAddress}
	data, err := os.ReadFile(path)
	if errors.Is(err, fs.ErrNotExist) {
		return f, nil
	}
	if err != nil {
		return file{}, fmt.Errorf("read configuration file %s: %w", path, err)
	}
	if err := toml.Unmarshal(data, &f); err != nil {
		return file{}, fmt.Errorf("invalid configuration file %s: %w", path, err)
	}
	if strings.TrimSpace(f.Address) == "" {
		f.Address = defaultAddress
	}
	if err := validateAddress(f.Address); err != nil {
		return file{}, fmt.Errorf("configuration file %s: %w", path, err)
	}
	// An events directory is not validated here. A path is only known to be
	// usable once it is opened, so the runtime validates it by constructing the
	// sink at startup rather than trusting a check that could go stale.
	f.EventsDir = strings.TrimSpace(f.EventsDir)
	// Fabric overrides are not validated here either: what makes an address
	// usable is the adapter's business, so the composed adapter configuration is
	// validated at startup, before any listener opens.
	f.Fabric.Olric.ClientAddress = strings.TrimSpace(f.Fabric.Olric.ClientAddress)
	f.Fabric.Olric.MemberlistAddress = strings.TrimSpace(f.Fabric.Olric.MemberlistAddress)
	f.Fabric.Olric.StartTimeout = strings.TrimSpace(f.Fabric.Olric.StartTimeout)
	f.Registration.ReconcileInterval = strings.TrimSpace(f.Registration.ReconcileInterval)
	return f, nil
}
