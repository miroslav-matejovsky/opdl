package deployment_test

import (
	"encoding/json"
	"os"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/miroslav-matejovsky/opdl/distribution/catalog"
	deploymentcontract "github.com/miroslav-matejovsky/opdl/distribution/deployment"
)

func validDescriptor() deploymentcontract.Descriptor {
	return deploymentcontract.Descriptor{
		Platform:        "acme-opdl",
		Project:         "customer-a",
		Environment:     "production",
		Site:            "north",
		Machine:         "sensor-01",
		Role:            "sensor-node",
		Features:        deploymentcontract.Features{OPCUA: true},
		EnabledServices: []string{catalog.ServiceSensor, catalog.ServiceOPCUAAdapter},
		Database:        deploymentcontract.Database{Provider: "postgres", Host: "db", Port: 5432},
	}
}

func TestDescriptorValidateOK(t *testing.T) {
	require.NoError(t, validDescriptor().Validate())
}

func TestDescriptorValidateUnknownService(t *testing.T) {
	d := validDescriptor()
	d.EnabledServices = []string{"not-a-service"}
	require.ErrorContains(t, d.Validate(), "unknown service")
}

func TestDescriptorValidateFeatureGate(t *testing.T) {
	d := validDescriptor()
	// opcua-adapter requires the opcua feature; turn it off.
	d.Features = deploymentcontract.Features{}
	require.ErrorContains(t, d.Validate(), "requires feature")
}

func TestDescriptorValidateDuplicateService(t *testing.T) {
	d := validDescriptor()
	d.EnabledServices = []string{catalog.ServiceSensor, catalog.ServiceSensor}
	require.ErrorContains(t, d.Validate(), "more than once")
}

func TestDescriptorValidateMissingIdentity(t *testing.T) {
	d := validDescriptor()
	d.Project = ""
	require.ErrorContains(t, d.Validate(), "project is required")
}

func TestAssignmentIDFallsBackToKind(t *testing.T) {
	d := validDescriptor()
	// No explicit assignment: the kind is the stable id.
	require.Equal(t, catalog.ServiceSensor, d.AssignmentID(catalog.ServiceSensor))

	// Explicit assignment wins.
	d.Services = []deploymentcontract.ServiceConfig{{Name: catalog.ServiceSensor, Assignment: "sensor-a"}}
	require.Equal(t, "sensor-a", d.AssignmentID(catalog.ServiceSensor))
}

func TestDescriptorValidateDuplicateAssignment(t *testing.T) {
	d := validDescriptor()
	d.Services = []deploymentcontract.ServiceConfig{
		{Name: catalog.ServiceSensor, Assignment: "dup"},
		{Name: catalog.ServiceOPCUAAdapter, Assignment: "dup"},
	}
	require.ErrorContains(t, d.Validate(), "used more than once")
}

func TestDescriptorValidateSingleActivePolicy(t *testing.T) {
	d := validDescriptor()
	d.Services = []deploymentcontract.ServiceConfig{{
		Name: catalog.ServiceSensor,
		Redundancy: deploymentcontract.RedundancyPolicy{
			Mode:            deploymentcontract.RedundancySingleActive,
			Group:           "sensors",
			Scope:           "site",
			Members:         []string{"sensor-01", "sensor-02"},
			LeaseDuration:   "5s",
			RetryInitial:    "100ms",
			RetryMax:        "1s",
			FailoverTimeout: "10s",
			DrainTimeout:    "1s",
		},
	}}
	require.NoError(t, d.Validate())

	d.Services[0].Redundancy.Members = []string{"sensor-01"}
	require.ErrorContains(t, d.Validate(), "at least two members")
}

func TestDescriptorHostsAuthorityRoundTrip(t *testing.T) {
	d := validDescriptor()
	d.HostsAuthority = true
	raw, err := json.Marshal(d)
	require.NoError(t, err)

	var back deploymentcontract.Descriptor
	require.NoError(t, json.Unmarshal(raw, &back))
	require.True(t, back.HostsAuthority)
	require.NoError(t, back.Validate())
}

func TestFeaturesEnabled(t *testing.T) {
	f := deploymentcontract.Features{OPCUA: true, Historian: true, Chaos: true}
	require.True(t, f.Enabled(catalog.FeatureOPCUA))
	require.True(t, f.Enabled(catalog.FeatureHistorian))
	require.True(t, f.Enabled(catalog.FeatureChaos))
	require.False(t, f.Enabled(catalog.FeatureAlarms))
	require.False(t, f.Enabled("nonsense"))
}

func TestDescriptorJSONDecodeAndValidate(t *testing.T) {
	data, err := os.ReadFile("testdata/descriptor.json")
	require.NoError(t, err)

	var d deploymentcontract.Descriptor
	require.NoError(t, json.Unmarshal(data, &d))

	require.Equal(t, "acme-opdl", d.Platform)
	require.Equal(t, "customer-a", d.Project)
	require.Equal(t, "production", d.Environment)
	require.Equal(t, "north", d.Site)
	require.Equal(t, "sensor-01", d.Machine)
	require.Equal(t, "sensor-node", d.Role)

	require.True(t, d.Features.Enabled(catalog.FeatureOPCUA))
	require.True(t, d.Features.Enabled(catalog.FeatureRecording))
	require.True(t, d.Features.Enabled(catalog.FeatureChaos))
	require.False(t, d.Features.Enabled(catalog.FeatureAlarms))

	require.Equal(t, []string{"sensor-service", "opcua-adapter"}, d.EnabledServices)

	cfg, ok := d.LookupService("opcua-adapter")
	require.True(t, ok)
	require.Equal(t, "opc.tcp://plc-north-01:4840", cfg.Endpoint)
	require.Equal(t, "2", cfg.Namespace)
	require.Equal(t, "1s", cfg.Interval)

	require.Equal(t, "postgres", d.Database.Provider)
	require.Equal(t, "db.customer-a.local", d.Database.Host)
	require.Equal(t, 5432, d.Database.Port)

	require.Len(t, d.Runtime.Parameters, 2)
	require.NoError(t, d.Validate())
}

func TestDescriptorJSONRoundTrip(t *testing.T) {
	src, err := os.ReadFile("testdata/descriptor.json")
	require.NoError(t, err)

	var d1 deploymentcontract.Descriptor
	require.NoError(t, json.Unmarshal(src, &d1))

	data, err := json.Marshal(d1)
	require.NoError(t, err)

	var d2 deploymentcontract.Descriptor
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
			name: "unknown service in JSON",
			json: `{
			  "platform": "acme-opdl",
			  "project": "customer-a",
			  "environment": "production",
			  "site": "north",
			  "machine": "sensor-01",
			  "role": "sensor-node",
			  "enabled_services": ["unknown-service"],
			  "database": {"provider": "postgres", "host": "db", "port": 5432}
			}`,
			errText: "unknown service",
		},
		{
			name: "service feature gate not enabled in JSON",
			json: `{
			  "platform": "acme-opdl",
			  "project": "customer-a",
			  "environment": "production",
			  "site": "north",
			  "machine": "sensor-01",
			  "role": "sensor-node",
			  "features": {"opcua": false},
			  "enabled_services": ["opcua-adapter"],
			  "database": {"provider": "postgres", "host": "db", "port": 5432}
			}`,
			errText: "requires feature",
		},
		{
			name: "service config for non-enabled service in JSON",
			json: `{
			  "platform": "acme-opdl",
			  "project": "customer-a",
			  "environment": "production",
			  "site": "north",
			  "machine": "sensor-01",
			  "role": "sensor-node",
			  "enabled_services": ["sensor-service"],
			  "services": [{"name": "opcua-adapter", "endpoint": "opc.tcp://plc:4840"}],
			  "database": {"provider": "postgres", "host": "db", "port": 5432}
			}`,
			errText: "not an enabled service",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			var d deploymentcontract.Descriptor
			require.NoError(t, json.Unmarshal([]byte(tc.json), &d))
			require.ErrorContains(t, d.Validate(), tc.errText)
		})
	}
}
