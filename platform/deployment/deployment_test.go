package deployment_test

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/miroslav-matejovsky/opdl/platform/deployment"
)

func TestDescriptorRequiresExplicitSlotsPolicy(t *testing.T) {
	tests := map[string]struct {
		json string
		err  string
	}{
		"missing slots":                {json: `{}`, err: "slots is required"},
		"null slots":                   {json: `{"slots":null}`, err: "slots is required"},
		"missing primary slot":         {json: `{"slots":{}}`, err: "slots.primary is required"},
		"null primary slot":            {json: `{"slots":{"primary":null}}`, err: "slots.primary is required"},
		"explicit primary only":        {json: `{"slots":{"primary":{}}}`},
		"explicit primary and standby": {json: `{"slots":{"primary":{},"standby":{}}}`},
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
			require.Equal(t, test.json == `{"slots":{"primary":{},"standby":{}}}`, descriptor.Slots.Standby != nil)
		})
	}
}
