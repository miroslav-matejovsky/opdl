package deployment_test

import (
	"encoding/json"
	"os"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/miroslav-matejovsky/opdl/distribution/deployment"
)

func validDescriptor() deployment.Descriptor {
	return deployment.Descriptor{
		Environment: "production",
		Project:     "customer-a",
		Site:        "north",
		Machine:     "sensor-01",
		Role:        "sensor-node",
		Features:    deployment.Features{Chaos: true},
		Services:    []string{"core-services"},
	}
}

func TestDescriptorValidateOK(t *testing.T) {
	require.NoError(t, validDescriptor().Validate())
}

func TestDescriptorValidateMissingEnvironment(t *testing.T) {
	d := validDescriptor()
	d.Environment = ""
	require.ErrorContains(t, d.Validate(), "environment is required")
}

func TestDescriptorValidateMissingProject(t *testing.T) {
	d := validDescriptor()
	d.Project = ""
	require.ErrorContains(t, d.Validate(), "project is required")
}

func TestDescriptorValidateMissingSite(t *testing.T) {
	d := validDescriptor()
	d.Site = ""
	require.ErrorContains(t, d.Validate(), "site is required")
}

func TestDescriptorValidateMissingMachine(t *testing.T) {
	d := validDescriptor()
	d.Machine = ""
	require.ErrorContains(t, d.Validate(), "machine is required")
}

func TestDescriptorValidateMissingRole(t *testing.T) {
	d := validDescriptor()
	d.Role = ""
	require.ErrorContains(t, d.Validate(), "role is required")
}

func TestDescriptorValidateEmptyServices(t *testing.T) {
	d := validDescriptor()
	d.Services = nil
	require.ErrorContains(t, d.Validate(), "at least one service is required")
}

func TestDescriptorValidateDuplicateService(t *testing.T) {
	d := validDescriptor()
	d.Services = []string{"core-services", "core-services"}
	require.ErrorContains(t, d.Validate(), "assigned more than once")
}

func TestFeatures(t *testing.T) {
	f := deployment.Features{Chaos: true}
	require.True(t, f.Chaos)
	require.False(t, f.Redundancy)
}

func TestDescriptorSummary(t *testing.T) {
	summary := validDescriptor().Summary()
	require.Contains(t, summary, "project=customer-a")
	require.Contains(t, summary, "env=production")
	require.Contains(t, summary, "site=north")
	require.Contains(t, summary, "machine=sensor-01")
	require.Contains(t, summary, "role=sensor-node")
	require.Contains(t, summary, "features=[chaos]")
	require.Contains(t, summary, "services=[core-services]")
}

func TestDescriptorJSONDecodeAndValidate(t *testing.T) {
	data, err := os.ReadFile("testdata/descriptor.json")
	require.NoError(t, err)

	var d deployment.Descriptor
	require.NoError(t, json.Unmarshal(data, &d))

	require.Equal(t, "production", d.Environment)
	require.Equal(t, "customer-a", d.Project)
	require.Equal(t, "north", d.Site)
	require.Equal(t, "sensor-01", d.Machine)
	require.Equal(t, "sensor-node", d.Role)

	require.False(t, d.Features.Redundancy)
	require.True(t, d.Features.Chaos)

	require.Equal(t, []string{"core-services"}, d.Services)

	require.NoError(t, d.Validate())
}

func TestDescriptorJSONRoundTrip(t *testing.T) {
	src, err := os.ReadFile("testdata/descriptor.json")
	require.NoError(t, err)

	var d1 deployment.Descriptor
	require.NoError(t, json.Unmarshal(src, &d1))

	data, err := json.Marshal(d1)
	require.NoError(t, err)

	var d2 deployment.Descriptor
	require.NoError(t, json.Unmarshal(data, &d2))
	require.Equal(t, d1, d2)
}

func TestDescriptorJSONValidationFailures(t *testing.T) {
	tests := []struct {
		name    string
		json    string
		errText string
	}{
		{
			name: "missing role in JSON",
			json: `{
			  "environment": "production",
			  "project": "customer-a",
			  "site": "north",
			  "machine": "sensor-01",
			  "role": "",
			  "services": ["core-services"]
			}`,
			errText: "role is required",
		},
		{
			name: "empty services in JSON",
			json: `{
			  "environment": "production",
			  "project": "customer-a",
			  "site": "north",
			  "machine": "sensor-01",
			  "role": "sensor-node",
			  "services": []
			}`,
			errText: "at least one service is required",
		},
		{
			name: "duplicate service in JSON",
			json: `{
			  "environment": "production",
			  "project": "customer-a",
			  "site": "north",
			  "machine": "sensor-01",
			  "role": "sensor-node",
			  "services": ["core-services", "core-services"]
			}`,
			errText: "assigned more than once",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			var d deployment.Descriptor
			require.NoError(t, json.Unmarshal([]byte(tc.json), &d))
			require.ErrorContains(t, d.Validate(), tc.errText)
		})
	}
}
