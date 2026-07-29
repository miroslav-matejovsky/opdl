package blueprint_test

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/miroslav-matejovsky/opdl/builder/internal/blueprint"
)

func TestLoad(t *testing.T) {
	p, err := blueprint.Load("testdata")
	require.NoError(t, err)

	require.Equal(t, "customer-a", p.Name)
	require.Equal(t, "production", p.Environment)

	require.Len(t, p.Sites, 2)
	require.Equal(t, "north", p.Sites[0].Name)
	require.Len(t, p.Sites[0].Machines, 2)

	m1 := p.Sites[0].Machines[0]
	require.Equal(t, "sensor", m1.Name)
	require.Equal(t, "sensor-node", m1.MachineProfile)
	require.Equal(t, "10.0.1.10", m1.IP)
	require.Equal(t, []string{"sensor-services"}, m1.ServiceNames())
	require.Equal(t, blueprint.HealthCheck{
		Type:     "http",
		Port:     9101,
		Path:     "/health",
		Interval: "10s",
		Timeout:  "2s",
		Retries:  3,
	}, m1.Services[0].HealthCheck)
	require.NotNil(t, m1.Primary)
	require.NotNil(t, m1.Standby)
	// The sensor opts out of a local standby process; local-server opts in. Both
	// state the decision, so the fixture proves each value survives loading rather
	// than only the one that matches the zero value.
	require.True(t, m1.Standby.Disabled)

	m2 := p.Sites[0].Machines[1]
	require.NotNil(t, m2.Primary)
	require.NotNil(t, m2.Standby)
	require.False(t, m2.Standby.Disabled)
	// A machine hosts as many services as it needs, in authored order.
	require.Equal(t, []string{"core-services", "alarm-service"}, m2.ServiceNames())

	require.Equal(t, "control-room", p.Sites[1].Name)
	require.Len(t, p.Sites[1].Machines, 3)

	// The integration machine probes its outward-facing services on slower terms
	// than a local one. The fixture carries both, so loading proves a machine's
	// own timings survive rather than only the ones every machine repeats.
	integration := p.Sites[1].Machines[2]
	require.Equal(t, []string{"integration-services"}, integration.ServiceNames())
	require.Equal(t, blueprint.HealthCheck{
		Type:     "http",
		Port:     9101,
		Path:     "/health/ready",
		Interval: "30s",
		Timeout:  "5s",
		Retries:  5,
	}, integration.Services[0].HealthCheck)
}

func TestLoadNoHCLFiles(t *testing.T) {
	_, err := blueprint.Load(t.TempDir())
	require.ErrorContains(t, err, "no .hcl files found")
}
