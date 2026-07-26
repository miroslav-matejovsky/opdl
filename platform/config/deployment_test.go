package config_test

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/miroslav-matejovsky/opdl/platform/config"
)

// completeDescriptor is the minimal JSON that decodes: every field the platform
// requires to be stated, and nothing else. Tests remove one part of it at a time
// rather than building a valid descriptor from scratch each time.
//
// It is a one-machine site whose machine deploys only a Primary Instance, so the
// membership is the single peer that machine contributes.
const completeDescriptor = `{
  "instances": {
    "primary": {
      "disabled": false,
      "data_dir": ".data/platform/primary",
      "api_address": "127.0.0.1:8080",
      "api_read_header_timeout": "5s",
      "api_shutdown_timeout": "10s"
    },
    "standby": {"disabled": true}
  }
}`

// The listener timeouts are part of every deployed instance record here, so a
// test that removes some other required field fails on that field rather than on
// a timeout it never meant to drop.
const instanceTimeoutsJSON = `"api_read_header_timeout":"5s","api_shutdown_timeout":"10s"`
const primaryInstanceJSON = `"primary":{"disabled":false,"data_dir":".data/platform/primary",` + instanceTimeoutsJSON + `}`
const standbyInstanceJSON = `"standby":{"disabled":false,"data_dir":".data/platform/standby",` + instanceTimeoutsJSON + `}`
const instances = `"instances":{` + primaryInstanceJSON + `,"standby":{"disabled":true}}`
const standbyEnabledInstances = `"instances":{` + primaryInstanceJSON + `,` + standbyInstanceJSON + `}`

// leaseTimingsJSON are the lease timings a valid lease states, without the file.
// Cases below drop or corrupt one part at a time rather than restating all five.
const leaseTimingsJSON = `"duration":"15s","renewal_interval":"5s","health_check_interval":"2s","failback_stabilization":"30s","lag_bound":"30s"`

// TestDescriptorRequiresExplicitDecisions checks the descriptor decoder rejects
// JSON that leaves a decision to a zero value.
func TestDescriptorRequiresExplicitDecisions(t *testing.T) {
	tests := map[string]struct {
		json string
		err  string
	}{
		"missing instances": {json: `{}`, err: "instances is required"},
		"null instances":    {json: `{"instances":null}`, err: "instances is required"},
		"missing primary instance": {
			json: `{"instances":{"standby":{"disabled":true}}}`,
			err:  "instances.primary is required",
		},
		"null primary instance": {
			json: `{"instances":{"primary":null,"standby":{"disabled":true}}}`,
			err:  "instances.primary is required",
		},
		"missing standby instance": {
			json: `{"instances":{` + primaryInstanceJSON + `}}`,
			err:  "instances.standby is required",
		},
		"null standby instance": {
			json: `{"instances":{` + primaryInstanceJSON + `,"standby":null}}`,
			err:  "instances.standby is required",
		},
		"missing primary disabled": {
			json: `{"instances":{"primary":{},"standby":{"disabled":true}}}`,
			err:  "instances.primary.disabled is required",
		},
		"missing standby disabled": {
			json: `{"instances":{` + primaryInstanceJSON + `,"standby":{}}}`,
			err:  "instances.standby.disabled is required",
		},
		"lease when standby disabled": {
			json: `{` + instances + `,"lease":{"file":"D:/opdl/lease",` + leaseTimingsJSON + `}}`,
			err:  "lease is set but instances.standby.disabled is true",
		},
		"missing lease when standby enabled": {
			json: `{` + standbyEnabledInstances + `}`,
			err:  "lease is required",
		},
		"null lease when standby enabled": {
			json: `{` + standbyEnabledInstances + `,"lease":null}`,
			err:  "lease is required",
		},
		"missing lease file when standby enabled": {
			json: `{` + standbyEnabledInstances + `,"lease":{` + leaseTimingsJSON + `}}`,
			err:  "lease.file is required",
		},
		"bad lease duration when standby enabled": {
			json: `{` + standbyEnabledInstances + `,"lease":{"file":"D:/opdl/lease","duration":"soon","renewal_interval":"5s","health_check_interval":"2s","failback_stabilization":"30s","lag_bound":"30s"}}`,
			err:  "lease.duration",
		},
		"missing lease lag bound when standby enabled": {
			json: `{` + standbyEnabledInstances + `,"lease":{"file":"D:/opdl/lease","duration":"15s","renewal_interval":"5s","health_check_interval":"2s","failback_stabilization":"30s"}}`,
			err:  "lease.lag_bound is required",
		},
		"missing instance read header timeout": {
			json: `{"instances":{"primary":{"disabled":false,"data_dir":".data/platform/primary","api_shutdown_timeout":"10s"},"standby":{"disabled":true}}}`,
			err:  "instances.primary.api_read_header_timeout is required",
		},
		"bad instance shutdown timeout": {
			json: `{"instances":{"primary":{"disabled":false,"data_dir":".data/platform/primary","api_read_header_timeout":"5s","api_shutdown_timeout":"soon"},"standby":{"disabled":true}}}`,
			err:  "instances.primary.api_shutdown_timeout",
		},
		"complete": {json: completeDescriptor},
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

// TestDescriptorDecodesTopology checks a complete descriptor decodes into the
// values the runtime composes from: both instance decisions, and the endpoints the
// deployed instance binds.
func TestDescriptorDecodesTopology(t *testing.T) {
	var descriptor config.Descriptor
	require.NoError(t, json.Unmarshal([]byte(completeDescriptor), &descriptor))

	require.False(t, descriptor.Instances.Primary.Disabled)
	require.True(t, descriptor.Instances.Standby.Disabled)

	primary := descriptor.Instances.Primary
	require.Equal(t, "127.0.0.1:8080", primary.APIAddress)

	// An instance that is not deployed carries no endpoint, so the runtime cannot
	// mistake a resolved address for one that will ever be bound.
	require.Empty(t, descriptor.Instances.Standby.APIAddress)
}

// TestDescriptorStandbyEnabledDecodes checks an enabled standby decodes as
// enabled, with its own endpoints. It is the counterpart of the disabled fixture:
// the field is a bool, so a decoder that dropped it would still satisfy one of
// the two cases.
func TestDescriptorStandbyEnabledDecodes(t *testing.T) {
	var descriptor config.Descriptor
	enabled := `{
	  "instances": {
	    "primary": {
	      "disabled": false,
	      "data_dir": ".data/platform/primary",
	      "api_address": "127.0.0.1:8080",
	      "api_read_header_timeout": "5s",
	      "api_shutdown_timeout": "10s"
	    },
	    "standby": {
	      "disabled": false,
	      "data_dir": ".data/platform/standby",
	      "api_address": "127.0.0.1:8081",
	      "api_read_header_timeout": "5s",
	      "api_shutdown_timeout": "10s"
	    }
	  },
	  "lease": {"file": "D:/opdl/node/lease", "duration": "15s", "renewal_interval": "5s", "health_check_interval": "2s", "failback_stabilization": "30s", "lag_bound": "30s"}
	}`
	require.NoError(t, json.Unmarshal([]byte(enabled), &descriptor))
	require.False(t, descriptor.Instances.Standby.Disabled)
	require.Equal(t, "127.0.0.1:8081", descriptor.Instances.Standby.APIAddress)
}

// TestInstancesGetSelectsByRole checks the accessor the runtime uses to find its
// own record.
func TestInstancesGetSelectsByRole(t *testing.T) {
	var descriptor config.Descriptor
	require.NoError(t, json.Unmarshal([]byte(completeDescriptor), &descriptor))

	require.False(t, descriptor.Instances.Get(config.RolePrimary).Disabled)
	require.True(t, descriptor.Instances.Get(config.RoleStandby).Disabled)
	require.Equal(t, config.RolePrimary, config.Role(false))
	require.Equal(t, config.RoleStandby, config.Role(true))
}
