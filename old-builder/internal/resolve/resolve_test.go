package resolve_test

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/miroslav-matejovsky/opdl/builder/internal/resolve"
	"github.com/miroslav-matejovsky/opdl/distribution/catalog"
	deploymentcontract "github.com/miroslav-matejovsky/opdl/distribution/deployment"
	topology "github.com/miroslav-matejovsky/opdl/distribution/topology"
)

func project() *topology.Project {
	return &topology.Project{
		Name:        "customer-a",
		Environment: "production",
		Database:    topology.Database{Provider: "postgres", Host: "db", Port: 5432},
		Features:    topology.Features{OPCUA: true, Historian: true, Chaos: true},
		Sites: []topology.Site{{
			Name: "north",
			Machines: []topology.Machine{{
				Name:     "sensor-01",
				Role:     "sensor-node",
				Services: []string{"sensor-service", "opcua-adapter"},
				Assignments: []topology.ServiceAssignment{
					{Name: "opcua-adapter", Endpoint: "opc.tcp://plc:4840"},
				},
			}},
		}},
	}
}

func TestBuildProducesMachineDescriptors(t *testing.T) {
	plan, err := resolve.Build(project(), "acme-opdl")
	require.NoError(t, err)
	require.Len(t, plan.Machines, 1)

	m := plan.Machines[0]
	require.Equal(t, "sensor-01", m.Machine)
	require.Equal(t, "acme-opdl", m.Platform)
	require.Equal(t, []string{"sensor-service", "opcua-adapter"}, m.EnabledServices)

	// The authored chaos feature is carried into the per-machine descriptor.
	require.True(t, m.Features.Chaos)
	require.True(t, m.Features.Enabled(catalog.FeatureChaos))

	sc, ok := m.LookupService("opcua-adapter")
	require.True(t, ok)
	require.Equal(t, "opc.tcp://plc:4840", sc.Endpoint)

	// The standard runtime policy is stamped on.
	require.NotEmpty(t, m.Runtime.Parameters)
	eff, err := deploymentcontract.Resolve(m, deploymentcontract.RuntimeConfig{})
	require.NoError(t, err)
	require.Equal(t, "127.0.0.1:8080", eff.ParamOr(deploymentcontract.ParamHTTPAddr, "x"))
	_, hasClusterBind := eff.Param(deploymentcontract.ParamClusterBindAddr)
	require.True(t, hasClusterBind)
	_, hasDataBind := eff.Param(deploymentcontract.ParamDataBindAddr)
	require.True(t, hasDataBind)
}

func TestBuildFailsFeatureGate(t *testing.T) {
	p := project()
	p.Features.OPCUA = false // opcua-adapter now ungated
	_, err := resolve.Build(p, "acme-opdl")
	require.ErrorContains(t, err, "requires feature")
}

func TestBuildResolvesSingleActivePolicyIntoDescriptorAndRoster(t *testing.T) {
	p := project()
	p.Sites[0].Machines = []topology.Machine{
		{
			Name: "sensor-01", Role: "sensor-node", Services: []string{catalog.ServiceSensor},
			Assignments: []topology.ServiceAssignment{{Name: catalog.ServiceSensor, Redundancy: &topology.RedundancyPolicy{
				Mode: "single-active", Group: "sensors", Scope: "site", Members: []string{"sensor-01", "sensor-02"},
				LeaseDuration: "5s", RetryInitial: "100ms", RetryMax: "1s", FailoverTimeout: "10s", DrainTimeout: "1s",
			}}},
		},
		{
			Name: "sensor-02", Role: "sensor-node", Services: []string{catalog.ServiceSensor},
			Assignments: []topology.ServiceAssignment{{Name: catalog.ServiceSensor, Redundancy: &topology.RedundancyPolicy{
				Mode: "single-active", Group: "sensors", Scope: "site", Members: []string{"sensor-01", "sensor-02"},
				LeaseDuration: "5s", RetryInitial: "100ms", RetryMax: "1s", FailoverTimeout: "10s", DrainTimeout: "1s",
			}}},
		},
	}
	plan, err := resolve.Build(p, "acme-opdl")
	require.NoError(t, err)
	require.Equal(t, deploymentcontract.RedundancySingleActive, plan.Machines[0].Services[0].Redundancy.Mode)
	require.Equal(t, "sensors", plan.Rosters["north"].Services[0].Redundancy.Group)
}
