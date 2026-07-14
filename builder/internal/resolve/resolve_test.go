package resolve_test

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/miroslav-matejovsky/opdl/builder/internal/blueprint"
	"github.com/miroslav-matejovsky/opdl/builder/internal/resolve"
)

func project() *blueprint.Project {
	return &blueprint.Project{
		Name:        "customer-a",
		Environment: "production",
		Features:    blueprint.Features{Chaos: true, Redundancy: true},
		Sites: []blueprint.Site{{
			Name: "north",
			Machines: []blueprint.Machine{{
				Name:     "sensor",
				Role:     "sensor-node",
				IP:       "10.0.1.10",
				Services: []string{"sensor-services"},
			}},
		}},
	}
}

func TestBuildProducesMachineDescriptors(t *testing.T) {
	plan, err := resolve.Build(project(), "acme-opdl")
	require.NoError(t, err)
	require.Equal(t, "customer-a", plan.Project)
	require.Len(t, plan.Machines, 1)

	m := plan.Machines[0]
	require.Equal(t, "acme-opdl", m.Platform)
	require.Equal(t, "production", m.Environment)
	require.Equal(t, "north", m.Site)
	require.Equal(t, "sensor", m.Machine)
	require.Equal(t, "sensor-node", m.Role)
	require.Equal(t, "10.0.1.10", m.IP)
	require.Equal(t, []string{"sensor-services"}, m.Services)
	require.True(t, m.Features.Chaos)
	require.True(t, m.Features.Redundancy)
}

func TestBuildValidatesDescriptors(t *testing.T) {
	p := project()
	p.Sites[0].Machines[0].IP = "not-an-ip"
	_, err := resolve.Build(p, "acme-opdl")
	require.ErrorContains(t, err, "not a valid IP address")
}

func TestBuildRequiresPlatformName(t *testing.T) {
	_, err := resolve.Build(project(), "")
	require.ErrorContains(t, err, "platform is required")
}
