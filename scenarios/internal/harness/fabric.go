package harness

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/miroslav-matejovsky/opdl/scenarios/internal/logscan"
)

// FabricConfig is one effective Event Fabric configuration a process reported at
// startup, before it bound or connected anything.
type FabricConfig struct {
	// Endpoint is the machine's own client address from the descriptor. It is
	// reported whether or not this process binds it, which is what lets a standby
	// and the active process it follows be compared.
	Endpoint string
	// Binds reports whether this process opens the machine's NATS listener.
	Binds   bool
	Cluster string
	Servers string
	Routes  string
	Storage bool
}

// fabricConfigPrefix is the runtime's effective-configuration log line.
const fabricConfigPrefix = "platform: event fabric configuration "

// ParseFabricConfigs extracts every effective Event Fabric configuration a
// process reported, in the order it reported them.
//
// A scenario reads this from the process output rather than from a status file
// because it is the only place the composed endpoints appear before anything is
// bound. That ordering is what distinguishes a client-only standby from an
// active storage server, and a promotion from a fresh start.
func ParseFabricConfigs(t *testing.T, role, logs string) []FabricConfig {
	t.Helper()
	var configs []FabricConfig
	for _, fields := range logscan.Fields(logs, fabricConfigPrefix) {
		configs = append(configs, FabricConfig{
			Endpoint: fields["endpoint"],
			Binds:    fields["binds"] == "true",
			Cluster:  fields["cluster"],
			Servers:  fields["servers"],
			Routes:   fields["routes"],
			Storage:  fields["storage"] == "true",
		})
	}
	require.NotEmptyf(t, configs, "%s never reported its effective Event Fabric configuration:\n%s", role, logs)
	return configs
}
