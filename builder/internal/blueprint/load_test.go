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
	// The sensor machine opts out of warm standby through its platform subsection.
	require.NotNil(t, m1.Platform)
	require.NotNil(t, m1.Platform.WarmStandby)
	require.False(t, *m1.Platform.WarmStandby)

	// The local-server machine has no platform block, so its policy stays default.
	require.Nil(t, p.Sites[0].Machines[1].Platform)

	require.Equal(t, "control-room", p.Sites[1].Name)
	require.Len(t, p.Sites[1].Machines, 3)
}

func TestLoadNoHCLFiles(t *testing.T) {
	_, err := blueprint.Load(t.TempDir())
	require.ErrorContains(t, err, "no .hcl files found")
}
