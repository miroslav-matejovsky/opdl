package deployment_test

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/miroslav-matejovsky/opdl/platform/deployment"
)

func TestDescriptorRequiresExplicitInstancePolicy(t *testing.T) {
	tests := map[string]struct {
		json string
		err  string
	}{
		"missing instances":       {json: `{}`, err: "instances is required"},
		"null instances":          {json: `{"instances":null}`, err: "instances is required"},
		"missing warm standby":    {json: `{"instances":{}}`, err: "instances.warm_standby is required"},
		"null warm standby":       {json: `{"instances":{"warm_standby":null}}`, err: "instances.warm_standby is required"},
		"invalid warm standby":    {json: `{"instances":{"warm_standby":"yes"}}`, err: "invalid instances.warm_standby"},
		"explicit false accepted": {json: `{"instances":{"warm_standby":false}}`},
		"explicit true accepted":  {json: `{"instances":{"warm_standby":true}}`},
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
			require.Equal(t, test.json == `{"instances":{"warm_standby":true}}`, descriptor.Instances.WarmStandby)
		})
	}
}
