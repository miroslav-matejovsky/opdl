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
	require.True(t, p.Features.Chaos)

	require.Len(t, p.Sites, 2)
	require.Equal(t, "north", p.Sites[0].Name)
	require.Len(t, p.Sites[0].Machines, 2)

	m1 := p.Sites[0].Machines[0]
	require.Equal(t, "sensor", m1.Name)
	require.Equal(t, "sensor-node", m1.Role)
	require.Equal(t, "10.0.1.10", m1.IP)
	require.Equal(t, []string{"sensor-services"}, m1.Services)
	require.NotNil(t, m1.Platform)
	require.NotNil(t, m1.Platform.Nats)
	require.Equal(t, 4222, m1.Platform.Nats.ClientPort)
	require.Equal(t, 6222, m1.Platform.Nats.ClusterPort)
	// The sensor opts out of a local standby process; local-server opts in. Both
	// state the decision, so the fixture proves each value survives loading rather
	// than only the one that matches the zero value.
	require.NotNil(t, m1.Platform.Standby)
	require.True(t, m1.Platform.Standby.Disabled)

	m2 := p.Sites[0].Machines[1]
	require.NotNil(t, m2.Platform)
	require.NotNil(t, m2.Platform.Nats)
	require.Equal(t, 4222, m2.Platform.Nats.ClientPort)
	require.NotNil(t, m2.Platform.Standby)
	require.False(t, m2.Platform.Standby.Disabled)

	require.Equal(t, "control-room", p.Sites[1].Name)
	require.Len(t, p.Sites[1].Machines, 3)
}

func TestLoadNoHCLFiles(t *testing.T) {
	_, err := blueprint.Load(t.TempDir())
	require.ErrorContains(t, err, "no .hcl files found")
}
