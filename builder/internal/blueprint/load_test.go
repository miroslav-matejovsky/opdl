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
	require.Equal(t, []string{"sensor-services"}, m1.Services)
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

	require.Equal(t, "control-room", p.Sites[1].Name)
	require.Len(t, p.Sites[1].Machines, 3)
}

func TestLoadNoHCLFiles(t *testing.T) {
	_, err := blueprint.Load(t.TempDir())
	require.ErrorContains(t, err, "no .hcl files found")
}
