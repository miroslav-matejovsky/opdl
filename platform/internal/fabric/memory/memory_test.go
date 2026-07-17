package memory_test

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/miroslav-matejovsky/opdl/platform/deployment"
	"github.com/miroslav-matejovsky/opdl/platform/internal/fabric"
	"github.com/miroslav-matejovsky/opdl/platform/internal/fabric/memory"
)

// TestSameMachineInstancesShareCollections checks the in-process adapter models a
// redundant machine: its primary and secondary open independent fabric handles
// over one Site's shared collections, each marking its own instance as Self, and
// what one writes the other reads.
func TestSameMachineInstancesShareCollections(t *testing.T) {
	ctx := t.Context()
	descriptor := deployment.Descriptor{
		Site: "north", Machine: "node-a", IP: "10.0.0.1",
		PlatformInstances: []deployment.PlatformInstance{
			{Name: "primary", APIAddress: "10.0.0.1:8080", FabricClientAddress: "10.0.0.1:3320", FabricMemberlistAddress: "10.0.0.1:3322"},
			{Name: "secondary", APIAddress: "10.0.0.1:8081", FabricClientAddress: "10.0.0.1:3321", FabricMemberlistAddress: "10.0.0.1:3323"},
		},
	}

	shared := memory.NewSite()
	primary := shared.Open(descriptor, "primary")
	secondary := shared.Open(descriptor, "secondary")
	t.Cleanup(func() { _ = primary.Close(context.Background()); _ = secondary.Close(context.Background()) })

	// Each handle sees the same two members but calls its own instance Self.
	primarySelf, ok := fabric.Self(primary.Members())
	require.True(t, ok)
	require.Equal(t, "node-a/primary", primarySelf.ID())
	secondarySelf, ok := fabric.Self(secondary.Members())
	require.True(t, ok)
	require.Equal(t, "node-a/secondary", secondarySelf.ID())

	// One collection, two instances of one machine: what the primary writes, the
	// secondary reads.
	fromPrimary, err := primary.Collection("units")
	require.NoError(t, err)
	fromSecondary, err := secondary.Collection("units")
	require.NoError(t, err)

	created, err := fromPrimary.Create(ctx, "7/42", []byte("registered"))
	require.NoError(t, err)
	require.True(t, created)

	value, found, err := fromSecondary.Get(ctx, "7/42")
	require.NoError(t, err)
	require.True(t, found, "a value the primary wrote must be readable on the secondary")
	require.Equal(t, []byte("registered"), value)

	// The two never connected, so each honestly reports a site it cannot reach the
	// other half of, exactly as the adapter documents.
	state, err := primary.State(ctx)
	require.NoError(t, err)
	require.Equal(t, fabric.StateDisconnected, state)
}
