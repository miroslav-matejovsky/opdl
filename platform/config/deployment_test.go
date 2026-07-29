package config_test

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/miroslav-matejovsky/opdl/platform/config"
)

// The fixtures below are the minimal JSON that decodes: every field the platform
// requires to be stated, and nothing else. Tests remove or corrupt one part at a
// time rather than building a valid descriptor from scratch each time.
const (
	// machineJSON is the machine's own record: the shared event store every
	// machine states, standby or not.
	machineJSON = `"machine_events_file":".data/platform/machine-events.jsonl"`
	primaryJSON = `"primary":{` + primaryFilesJSON + `,"api_address":"127.0.0.1:8080",` + instanceExtrasJSON + `}`
	standbyJSON = `"standby":{` + standbyFilesJSON + `,"api_address":"127.0.0.1:8081",` + instanceExtrasJSON + `}`
	// instanceExtrasJSON is what every instance record carries beyond its files
	// and its API address: the listener timeouts, and the embedded event fabric
	// broker the instance runs. Tests that remove one of these spell the rest
	// out inline instead.
	instanceExtrasJSON = `"api_read_header_timeout":"5s","api_shutdown_timeout":"10s",` + natsJSON
	// natsJSON is one instance's complete embedded event fabric record.
	natsJSON = `"nats":{"server_name":"mock-primary","cluster_name":"local","cluster_address":"127.0.0.1:6222"}`
	// The local files an instance owns. Each is stated on its own instance record,
	// so a descriptor missing either is a startup failure rather than an instance
	// that opens a path nothing authored.
	primaryFilesJSON = `"events_file":".data/platform/primary/events.jsonl","state_file":".data/platform/primary/state.json","log_file":".data/platform/primary/platform.log"`
	standbyFilesJSON = `"events_file":".data/platform/standby/events.jsonl","state_file":".data/platform/standby/state.json","log_file":".data/platform/standby/platform.log"`
	leaseJSON        = `"lease":{"file":"D:/opdl/lease",` + leaseTimingsJSON + `}`
	// leaseTimingsJSON are the timings a valid lease states, without the file.
	leaseTimingsJSON = `"duration":"15s","renewal_interval":"5s","health_check_interval":"2s","failback_stabilization":"30s"`

	// standbyless is a machine that deploys only a Primary Instance: no standby
	// record, and therefore no lease.
	standbyless = `{` + machineJSON + `,` + primaryJSON + `}`
	// redundant is a machine that deploys both instances and the lease they
	// contend for.
	redundant = `{` + machineJSON + `,` + primaryJSON + `,` + standbyJSON + `,` + leaseJSON + `}`
)

// TestDescriptorRequiresExplicitDecisions checks the descriptor decoder rejects
// JSON that leaves a decision to a zero value.
func TestDescriptorRequiresExplicitDecisions(t *testing.T) {
	tests := map[string]struct {
		json string
		err  string
	}{
		"missing primary": {json: `{` + machineJSON + `,` + standbyJSON + `,` + leaseJSON + `}`, err: "primary is required"},
		"null primary":    {json: `{` + machineJSON + `,"primary":null}`, err: "primary is required"},
		"missing primary events file": {
			json: `{` + machineJSON + `,"primary":{"state_file":".data/state.json","api_address":"127.0.0.1:8080",` + instanceExtrasJSON + `}}`,
			err:  "primary.events_file is required",
		},
		"missing primary state file": {
			json: `{` + machineJSON + `,"primary":{"events_file":".data/events.jsonl","log_file":".data/platform.log","api_address":"127.0.0.1:8080",` + instanceExtrasJSON + `}}`,
			err:  "primary.state_file is required",
		},
		"missing primary log file": {
			json: `{` + machineJSON + `,"primary":{"events_file":".data/events.jsonl","state_file":".data/state.json","api_address":"127.0.0.1:8080",` + instanceExtrasJSON + `}}`,
			err:  "primary.log_file is required",
		},
		"missing primary read header timeout": {
			json: `{` + machineJSON + `,"primary":{` + primaryFilesJSON + `,"api_shutdown_timeout":"10s",` + natsJSON + `}}`,
			err:  "primary.api_read_header_timeout is required",
		},
		"bad primary shutdown timeout": {
			json: `{` + machineJSON + `,"primary":{` + primaryFilesJSON + `,"api_read_header_timeout":"5s","api_shutdown_timeout":"soon",` + natsJSON + `}}`,
			err:  "primary.api_shutdown_timeout",
		},
		// The embedded broker is required on every deployed instance, and an
		// omitted field would decode as an unnamed server on no address rather
		// than as an omission, so each part is guarded on its own.
		"missing primary nats": {
			json: `{` + machineJSON + `,"primary":{` + primaryFilesJSON + `,"api_address":"127.0.0.1:8080","api_read_header_timeout":"5s","api_shutdown_timeout":"10s"}}`,
			err:  "primary.nats is required",
		},
		"null primary nats": {
			json: `{` + machineJSON + `,"primary":{` + primaryFilesJSON + `,"api_address":"127.0.0.1:8080","api_read_header_timeout":"5s","api_shutdown_timeout":"10s","nats":null}}`,
			err:  "primary.nats is required",
		},
		"missing primary nats server name": {
			json: `{` + machineJSON + `,"primary":{` + primaryFilesJSON + `,"api_address":"127.0.0.1:8080","api_read_header_timeout":"5s","api_shutdown_timeout":"10s","nats":{"cluster_name":"local","cluster_address":"127.0.0.1:6222"}}}`,
			err:  "primary.nats.server_name is required",
		},
		"missing primary nats cluster name": {
			json: `{` + machineJSON + `,"primary":{` + primaryFilesJSON + `,"api_address":"127.0.0.1:8080","api_read_header_timeout":"5s","api_shutdown_timeout":"10s","nats":{"server_name":"mock-primary","cluster_address":"127.0.0.1:6222"}}}`,
			err:  "primary.nats.cluster_name is required",
		},
		"missing primary nats cluster address": {
			json: `{` + machineJSON + `,"primary":{` + primaryFilesJSON + `,"api_address":"127.0.0.1:8080","api_read_header_timeout":"5s","api_shutdown_timeout":"10s","nats":{"server_name":"mock-primary","cluster_name":"local"}}}`,
			err:  "primary.nats.cluster_address is required",
		},
		"missing standby nats": {
			json: `{` + machineJSON + `,` + primaryJSON + `,"standby":{` + standbyFilesJSON + `,"api_address":"127.0.0.1:8081","api_read_header_timeout":"5s","api_shutdown_timeout":"10s"},` + leaseJSON + `}`,
			err:  "standby.nats is required",
		},
		"missing standby events file": {
			json: `{` + machineJSON + `,` + primaryJSON + `,"standby":{"state_file":".data/standby/state.json","log_file":".data/standby/platform.log","api_address":"127.0.0.1:8081",` + instanceExtrasJSON + `},` + leaseJSON + `}`,
			err:  "standby.events_file is required",
		},
		"missing standby state file": {
			json: `{` + machineJSON + `,` + primaryJSON + `,"standby":{"events_file":".data/standby/events.jsonl","log_file":".data/standby/platform.log","api_address":"127.0.0.1:8081",` + instanceExtrasJSON + `},` + leaseJSON + `}`,
			err:  "standby.state_file is required",
		},
		"missing standby log file": {
			json: `{` + machineJSON + `,` + primaryJSON + `,"standby":{"events_file":".data/standby/events.jsonl","state_file":".data/standby/state.json","api_address":"127.0.0.1:8081",` + instanceExtrasJSON + `},` + leaseJSON + `}`,
			err:  "standby.log_file is required",
		},
		"lease without a standby": {
			json: `{` + machineJSON + `,` + primaryJSON + `,` + leaseJSON + `}`,
			err:  "lease is set but no standby is deployed",
		},
		"missing lease with a standby": {
			json: `{` + machineJSON + `,` + primaryJSON + `,` + standbyJSON + `}`,
			err:  "lease is required",
		},
		"null lease with a standby": {
			json: `{` + machineJSON + `,` + primaryJSON + `,` + standbyJSON + `,"lease":null}`,
			err:  "lease is required",
		},
		"missing lease file": {
			json: `{` + machineJSON + `,` + primaryJSON + `,` + standbyJSON + `,"lease":{` + leaseTimingsJSON + `}}`,
			err:  "lease.file is required",
		},
		"bad lease duration": {
			json: `{` + machineJSON + `,` + primaryJSON + `,` + standbyJSON + `,"lease":{"file":"D:/opdl/lease","duration":"soon","renewal_interval":"5s","health_check_interval":"2s","failback_stabilization":"30s"}}`,
			err:  "lease.duration",
		},
		"missing lease failback stabilization": {
			json: `{` + machineJSON + `,` + primaryJSON + `,` + standbyJSON + `,"lease":{"file":"D:/opdl/lease","duration":"15s","renewal_interval":"5s","health_check_interval":"2s"}}`,
			err:  "lease.failback_stabilization is required",
		},
		"missing machine events file": {
			json: `{` + primaryJSON + `}`,
			err:  "machine_events_file is required",
		},
		"null machine events file": {
			json: `{"machine_events_file":null,` + primaryJSON + `}`,
			err:  "machine_events_file is required",
		},
		"standby-less machine": {json: standbyless},
		"redundant machine":    {json: redundant},
	}
	for name, test := range tests {
		t.Run(name, func(t *testing.T) {
			var descriptor config.Descriptor
			err := json.Unmarshal([]byte(test.json), &descriptor)
			if test.err != "" {
				require.ErrorContains(t, err, test.err)
				return
			}
			require.NoError(t, err)
		})
	}
}

// TestDescriptorDecodesAStandbylessMachine checks the shape a machine with no
// redundancy decodes into: a primary record, and nothing where the peer and the
// lease would be.
func TestDescriptorDecodesAStandbylessMachine(t *testing.T) {
	var descriptor config.Descriptor
	require.NoError(t, json.Unmarshal([]byte(standbyless), &descriptor))

	require.Equal(t, "127.0.0.1:8080", descriptor.Primary.APIAddress)
	require.False(t, descriptor.HasStandby())
	require.Nil(t, descriptor.Standby)
	require.Nil(t, descriptor.Lease)
}

// TestDescriptorDecodesARedundantMachine is the counterpart: a standby that is
// deployed decodes with its own endpoints and the lease the two instances
// contend for.
func TestDescriptorDecodesARedundantMachine(t *testing.T) {
	var descriptor config.Descriptor
	require.NoError(t, json.Unmarshal([]byte(redundant), &descriptor))

	require.True(t, descriptor.HasStandby())
	require.Equal(t, "127.0.0.1:8081", descriptor.Standby.APIAddress)
	require.NotNil(t, descriptor.Lease)
	require.Equal(t, "30s", descriptor.Lease.FailbackStabilization)
}

// TestDescriptorDecodesTheEventFabricRoutes checks the peers an instance's
// embedded server dials survive the descriptor, and that having none decodes as
// having none.
//
// The routes are the one part of the fabric record with no required check on
// them, because a site that deploys a single instance correctly carries an empty
// list. That makes it the part worth decoding explicitly: a truncated list is
// indistinguishable from a lone instance, so this pins what each shape means.
func TestDescriptorDecodesTheEventFabricRoutes(t *testing.T) {
	const routed = `{` + machineJSON + `,"primary":{` + primaryFilesJSON +
		`,"api_address":"127.0.0.1:8080","api_read_header_timeout":"5s","api_shutdown_timeout":"10s"` +
		`,"nats":{"server_name":"mock-primary","cluster_name":"local","cluster_address":"10.0.1.10:6222"` +
		`,"routes":["nats://10.0.1.10:6223","nats://10.0.1.11:6222"]}}}`

	var descriptor config.Descriptor
	require.NoError(t, json.Unmarshal([]byte(routed), &descriptor))
	require.Equal(t, "10.0.1.10:6222", descriptor.Primary.NATS.ClusterAddress,
		"the cluster address is on the machine's ip, not on loopback: the site's cluster spans machines")
	require.Equal(t, []string{"nats://10.0.1.10:6223", "nats://10.0.1.11:6222"}, descriptor.Primary.NATS.Routes)

	// The mock descriptor is a site of one instance, so it carries no routes at
	// all and is still a complete record.
	var alone config.Descriptor
	require.NoError(t, json.Unmarshal([]byte(standbyless), &alone))
	require.Empty(t, alone.Primary.NATS.Routes)
}

// TestDescriptorInstanceSelectsByRole checks the accessor the runtime uses to
// find its own record and its peer's, including the peer that does not exist.
func TestDescriptorInstanceSelectsByRole(t *testing.T) {
	var redundantMachine config.Descriptor
	require.NoError(t, json.Unmarshal([]byte(redundant), &redundantMachine))
	require.Equal(t, "127.0.0.1:8080", redundantMachine.Instance(config.RolePrimary).APIAddress)
	require.Equal(t, "127.0.0.1:8081", redundantMachine.Instance(config.RoleStandby).APIAddress)

	var alone config.Descriptor
	require.NoError(t, json.Unmarshal([]byte(standbyless), &alone))
	require.Empty(t, alone.Instance(config.RoleStandby).APIAddress,
		"an instance the machine does not deploy has no address to read")

	require.Equal(t, config.RolePrimary, config.Role(false))
	require.Equal(t, config.RoleStandby, config.Role(true))
}
