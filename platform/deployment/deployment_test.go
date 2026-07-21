package deployment_test

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/miroslav-matejovsky/opdl/platform/deployment"
)

// completeDescriptor is the minimal JSON that decodes: every field the platform
// requires to be stated, and nothing else. Tests remove one part of it at a time
// rather than building a valid descriptor from scratch each time.
const completeDescriptor = `{
  "slots": {"primary": {"disabled": false}, "standby": {"disabled": true}},
  "event_fabric": {
    "nats": {
      "client_address": "127.0.0.1:4222",
      "cluster_address": "127.0.0.1:6222",
      "routes": [],
      "servers": ["127.0.0.1:4222"]
    },
    "peers": []
  }
}`

// TestDescriptorRequiresExplicitDecisions checks the descriptor decoder rejects
// JSON that leaves a decision to a zero value.
//
// Each guarded field has a usable zero: an omitted disabled decodes as false, an
// omitted nats block decodes as a machine with no journal to reach. Without
// these checks a truncated or stale descriptor would produce a running machine
// with a topology nobody wrote, which is strictly worse than a startup error.
func TestDescriptorRequiresExplicitDecisions(t *testing.T) {
	tests := map[string]struct {
		json string
		err  string
	}{
		"missing slots":        {json: `{}`, err: "slots is required"},
		"null slots":           {json: `{"slots":null}`, err: "slots is required"},
		"missing primary slot": {json: `{"slots":{"standby":{"disabled":true}}}`, err: "slots.primary is required"},
		"null primary slot": {
			json: `{"slots":{"primary":null,"standby":{"disabled":true}}}`,
			err:  "slots.primary is required",
		},
		"missing standby slot": {
			json: `{"slots":{"primary":{"disabled":false}}}`,
			err:  "slots.standby is required",
		},
		"null standby slot": {
			json: `{"slots":{"primary":{"disabled":false},"standby":null}}`,
			err:  "slots.standby is required",
		},
		"missing primary disabled": {
			json: `{"slots":{"primary":{},"standby":{"disabled":true}}}`,
			err:  "slots.primary.disabled is required",
		},
		"missing standby disabled": {
			json: `{"slots":{"primary":{"disabled":false},"standby":{}}}`,
			err:  "slots.standby.disabled is required",
		},
		"missing event_fabric": {
			json: `{"slots":{"primary":{"disabled":false},"standby":{"disabled":true}}}`,
			err:  "event_fabric is required",
		},
		"missing event_fabric nats": {
			json: `{"slots":{"primary":{"disabled":false},"standby":{"disabled":true}},"event_fabric":{"peers":[]}}`,
			err:  "event_fabric.nats is required",
		},
		"complete": {json: completeDescriptor},
	}
	for name, test := range tests {
		t.Run(name, func(t *testing.T) {
			var descriptor deployment.Descriptor
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
// values the runtime composes from, including both slot decisions.
func TestDescriptorDecodesTopology(t *testing.T) {
	var descriptor deployment.Descriptor
	require.NoError(t, json.Unmarshal([]byte(completeDescriptor), &descriptor))

	require.False(t, descriptor.Slots.Primary.Disabled)
	require.True(t, descriptor.Slots.Standby.Disabled)
	require.Equal(t, "127.0.0.1:4222", descriptor.EventFabric.Nats.ClientAddress)
	require.Equal(t, "127.0.0.1:6222", descriptor.EventFabric.Nats.ClusterAddress)
	require.Empty(t, descriptor.EventFabric.Nats.Routes)
	require.Equal(t, []string{"127.0.0.1:4222"}, descriptor.EventFabric.Nats.Servers)
}

// TestDescriptorStandbyEnabledDecodes checks an enabled standby decodes as
// enabled. It is the counterpart of the disabled fixture: the field is a bool,
// so a decoder that dropped it would still satisfy one of the two cases.
func TestDescriptorStandbyEnabledDecodes(t *testing.T) {
	var descriptor deployment.Descriptor
	enabled := `{
	  "slots": {"primary": {"disabled": false}, "standby": {"disabled": false}},
	  "event_fabric": {"nats": {"client_address": "127.0.0.1:4222", "cluster_address": "127.0.0.1:6222", "routes": [], "servers": ["127.0.0.1:4222"]}, "peers": []}
	}`
	require.NoError(t, json.Unmarshal([]byte(enabled), &descriptor))
	require.False(t, descriptor.Slots.Standby.Disabled)
}
