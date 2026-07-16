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
	EventsDir         string                      `toml:"events_dir"`
	ReadHeaderTimeout string                      `toml:"read_header_timeout"`
	ShutdownTimeout   string                      `toml:"shutdown_timeout"`
	Fabric            FabricSettings              `toml:"fabric"`
	Registration      Registration                `toml:"registration"`
	Instances         map[string]InstanceOverride `toml:"instances"`
}

// InstanceOverride contains emergency socket overrides for one named platform
// process. Deployment endpoints come from the embedded descriptor when fields
// are omitted.
type InstanceOverride struct {
	APIAddress *string                `toml:"api_address"`
	Fabric     InstanceFabricOverride `toml:"fabric"`
}

// InstanceFabricOverride contains adapter-specific socket overrides.
type InstanceFabricOverride struct {
	Olric InstanceFabricOlricOverride `toml:"olric"`
}

// InstanceFabricOlricOverride moves one instance's Olric sockets or join seeds.
type InstanceFabricOlricOverride struct {
	ClientAddress     *string  `toml:"client_address"`
	MemberlistAddress *string  `toml:"memberlist_address"`
	Join              []string `toml:"join"`
}

// Registration carries the runtime settings of the registration use case.
type Registration struct {
	// ReconcileInterval overrides how often this platform instance scans the
	// site's registration requests, as a Go duration such as "500ms".
	ReconcileInterval string `toml:"reconcile_interval"`
}

// FabricSettings carries common adapter runtime settings from TOML.
type FabricSettings struct {
	Olric FabricOlricSettings `toml:"olric"`
}

// FabricOlricSettings contains common Olric lifecycle settings.
//
// Start and shutdown durations are common to all instances. Socket overrides
// live under instances.<name>.fabric.olric so production corrections cannot
// accidentally move the sibling process.
type FabricOlricSettings struct {
	// StartTimeout bounds the readiness duration at startup, such as "30s".
	StartTimeout string `toml:"start_timeout"`
	// ShutdownGrace bounds the teardown grace duration at shutdown, such as "10s".
	ShutdownGrace string `toml:"shutdown_grace"`
}

// Fabric is one platform instance's effective adapter configuration.
type Fabric struct {
	Olric FabricOlric
}

// FabricOlric is one instance's effective Olric configuration after descriptor
// endpoints and controlled overrides are composed.
type FabricOlric struct {
	ClientAddress     string
	MemberlistAddress string
	Join              []string
	StartTimeout      string
	ShutdownGrace     string
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
	metadata, err := toml.Decode(string(data), &f)
	if err != nil {
		return file{}, fmt.Errorf("invalid configuration file %s: %w", path, err)
	}
	if undecoded := metadata.Undecoded(); len(undecoded) > 0 {
		return file{}, fmt.Errorf("configuration file %s: unknown setting %s", path, undecoded[0])
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
	for name, instance := range f.Instances {
		trimString(instance.APIAddress)
		trimString(instance.Fabric.Olric.ClientAddress)
		trimString(instance.Fabric.Olric.MemberlistAddress)
		f.Instances[name] = instance
	}
	return f, nil
}

func trimString(value *string) {
	if value != nil {
		*value = strings.TrimSpace(*value)
	}
}
