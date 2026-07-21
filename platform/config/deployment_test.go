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
      "api_address": "127.0.0.1:8080",
      "nats": {
        "client_address": "127.0.0.1:4222",
        "cluster_address": "127.0.0.1:6222",
        "routes": [],
        "servers": ["127.0.0.1:4222"]
      }
    },
    "standby": {"disabled": true}
  },
  "peers": [
    {
      "site": "north",
      "machine": "node",
      "role": "primary",
      "ip": "127.0.0.1",
      "api_address": "127.0.0.1:8080",
      "nats": {"client_address": "127.0.0.1:4222", "cluster_address": "127.0.0.1:6222"}
    }
  ]
}`

// instances is the instances block every fixture below shares, so a test that
// removes one part of the descriptor states only the part it removes.
const instances = `"instances":{"primary":{"disabled":false},"standby":{"disabled":true}}`

// TestDescriptorRequiresExplicitDecisions checks the descriptor decoder rejects
// JSON that leaves a decision to a zero value.
//
// Each guarded field has a usable zero: an omitted disabled decodes as false, an
// omitted peers list decodes as a site of one, so a registration would need no
// confirmation but its own. Without these checks a truncated or stale descriptor
// would produce a running machine with a topology nobody wrote, which is strictly
// worse than a startup error.
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
			json: `{"instances":{"primary":{"disabled":false}}}`,
			err:  "instances.standby is required",
		},
		"null standby instance": {
			json: `{"instances":{"primary":{"disabled":false},"standby":null}}`,
			err:  "instances.standby is required",
		},
		"missing primary disabled": {
			json: `{"instances":{"primary":{},"standby":{"disabled":true}}}`,
			err:  "instances.primary.disabled is required",
		},
		"missing standby disabled": {
			json: `{"instances":{"primary":{"disabled":false},"standby":{}}}`,
			err:  "instances.standby.disabled is required",
		},
		"lock when standby disabled": {
			json: `{` + instances + `,"lock":{"windows_mutex":"Global\\opdl-test"}}`,
			err:  "lock is set but instances.standby.disabled is true",
		},
		"missing lock when standby enabled": {
			json: `{"instances":{"primary":{"disabled":false},"standby":{"disabled":false}}}`,
			err:  "lock is required",
		},
		"null lock when standby enabled": {
			json: `{"instances":{"primary":{"disabled":false},"standby":{"disabled":false}},"lock":null}`,
			err:  "lock is required",
		},
		"missing lock windows_mutex when standby enabled": {
			json: `{"instances":{"primary":{"disabled":false},"standby":{"disabled":false}},"lock":{}}`,
			err:  "lock.windows_mutex is required",
		},
		"missing peers": {
			json: `{` + instances + `}`,
			err:  "peers is required",
		},
		"null peers": {
			json: `{` + instances + `,"peers":null}`,
			err:  "peers is required",
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
// values the runtime composes from: both instance decisions, the endpoints the
// deployed instance binds, and the site's membership.
func TestDescriptorDecodesTopology(t *testing.T) {
	var descriptor config.Descriptor
	require.NoError(t, json.Unmarshal([]byte(completeDescriptor), &descriptor))

	require.False(t, descriptor.Instances.Primary.Disabled)
	require.True(t, descriptor.Instances.Standby.Disabled)

	primary := descriptor.Instances.Primary
	require.Equal(t, "127.0.0.1:8080", primary.APIAddress)
	require.NotNil(t, primary.Nats)
	require.Equal(t, "127.0.0.1:4222", primary.Nats.ClientAddress)
	require.Equal(t, "127.0.0.1:6222", primary.Nats.ClusterAddress)
	require.Empty(t, primary.Nats.Routes)
	require.Equal(t, []string{"127.0.0.1:4222"}, primary.Nats.Servers)

	// An instance that is not deployed carries no endpoint, so the runtime cannot
	// mistake a resolved address for one that will ever be bound.
	require.Empty(t, descriptor.Instances.Standby.APIAddress)
	require.Nil(t, descriptor.Instances.Standby.Nats)

	require.Len(t, descriptor.Peers, 1)
	require.Equal(t, config.RolePrimary, descriptor.Peers[0].Role)
	require.Equal(t, "node", descriptor.Peers[0].Machine)
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
	      "api_address": "127.0.0.1:8080",
	      "nats": {"client_address": "127.0.0.1:4222", "cluster_address": "127.0.0.1:6222", "routes": ["127.0.0.1:6322"], "servers": ["127.0.0.1:4222", "127.0.0.1:4322"]}
	    },
	    "standby": {
	      "disabled": false,
	      "api_address": "127.0.0.1:8081",
	      "nats": {"client_address": "127.0.0.1:4322", "cluster_address": "127.0.0.1:6322", "routes": ["127.0.0.1:6222"], "servers": ["127.0.0.1:4322", "127.0.0.1:4222"]}
	    }
	  },
	  "lock": {"windows_mutex": "Global\\opdl-customer-a-north-sensor"},
	  "peers": [
	    {"site":"north","machine":"node","role":"primary","ip":"127.0.0.1","api_address":"127.0.0.1:8080","nats":{"client_address":"127.0.0.1:4222","cluster_address":"127.0.0.1:6222"}},
	    {"site":"north","machine":"node","role":"standby","ip":"127.0.0.1","api_address":"127.0.0.1:8081","nats":{"client_address":"127.0.0.1:4322","cluster_address":"127.0.0.1:6322"}}
	  ]
	}`
	require.NoError(t, json.Unmarshal([]byte(enabled), &descriptor))
	require.False(t, descriptor.Instances.Standby.Disabled)

	// The two instances of one machine are two members of the site, each with its
	// own endpoints, and each routes to the other.
	require.Len(t, descriptor.Peers, 2)
	require.Equal(t, "127.0.0.1:8081", descriptor.Instances.Standby.APIAddress)
	require.Equal(t, []string{"127.0.0.1:6322"}, descriptor.Instances.Primary.Nats.Routes)
	require.Equal(t, []string{"127.0.0.1:6222"}, descriptor.Instances.Standby.Nats.Routes)
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
