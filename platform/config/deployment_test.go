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
	primaryJSON  = `"primary":{"data_dir":".data/platform/primary","api_address":"127.0.0.1:8080",` + timeoutsJSON + `}`
	standbyJSON  = `"standby":{"data_dir":".data/platform/standby","api_address":"127.0.0.1:8081",` + timeoutsJSON + `}`
	timeoutsJSON = `"api_read_header_timeout":"5s","api_shutdown_timeout":"10s"`
	leaseJSON    = `"lease":{"file":"D:/opdl/lease",` + leaseTimingsJSON + `}`
	// leaseTimingsJSON are the timings a valid lease states, without the file.
	leaseTimingsJSON = `"duration":"15s","renewal_interval":"5s","health_check_interval":"2s","failback_stabilization":"30s","lag_bound":"30s"`

	// standbyless is a machine that deploys only a Primary Instance: no standby
	// record, and therefore no lease.
	standbyless = `{` + primaryJSON + `}`
	// redundant is a machine that deploys both instances and the lease they
	// contend for.
	redundant = `{` + primaryJSON + `,` + standbyJSON + `,` + leaseJSON + `}`
)

// TestDescriptorRequiresExplicitDecisions checks the descriptor decoder rejects
// JSON that leaves a decision to a zero value.
func TestDescriptorRequiresExplicitDecisions(t *testing.T) {
	tests := map[string]struct {
		json string
		err  string
	}{
		"missing primary": {json: `{` + standbyJSON + `,` + leaseJSON + `}`, err: "primary is required"},
		"null primary":    {json: `{"primary":null}`, err: "primary is required"},
		"missing primary data dir": {
			json: `{"primary":{"api_address":"127.0.0.1:8080",` + timeoutsJSON + `}}`,
			err:  "primary.data_dir is required",
		},
		"missing primary read header timeout": {
			json: `{"primary":{"data_dir":".data","api_shutdown_timeout":"10s"}}`,
			err:  "primary.api_read_header_timeout is required",
		},
		"bad primary shutdown timeout": {
			json: `{"primary":{"data_dir":".data","api_read_header_timeout":"5s","api_shutdown_timeout":"soon"}}`,
			err:  "primary.api_shutdown_timeout",
		},
		"missing standby data dir": {
			json: `{` + primaryJSON + `,"standby":{"api_address":"127.0.0.1:8081",` + timeoutsJSON + `},` + leaseJSON + `}`,
			err:  "standby.data_dir is required",
		},
		"lease without a standby": {
			json: `{` + primaryJSON + `,` + leaseJSON + `}`,
			err:  "lease is set but no standby is deployed",
		},
		"missing lease with a standby": {
			json: `{` + primaryJSON + `,` + standbyJSON + `}`,
			err:  "lease is required",
		},
		"null lease with a standby": {
			json: `{` + primaryJSON + `,` + standbyJSON + `,"lease":null}`,
			err:  "lease is required",
		},
		"missing lease file": {
			json: `{` + primaryJSON + `,` + standbyJSON + `,"lease":{` + leaseTimingsJSON + `}}`,
			err:  "lease.file is required",
		},
		"bad lease duration": {
			json: `{` + primaryJSON + `,` + standbyJSON + `,"lease":{"file":"D:/opdl/lease","duration":"soon","renewal_interval":"5s","health_check_interval":"2s","failback_stabilization":"30s","lag_bound":"30s"}}`,
			err:  "lease.duration",
		},
		"missing lease lag bound": {
			json: `{` + primaryJSON + `,` + standbyJSON + `,"lease":{"file":"D:/opdl/lease","duration":"15s","renewal_interval":"5s","health_check_interval":"2s","failback_stabilization":"30s"}}`,
			err:  "lease.lag_bound is required",
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
	require.Equal(t, "30s", descriptor.Lease.LagBound)
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
