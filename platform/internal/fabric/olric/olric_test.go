package olric_test

import (
	"context"
	"net"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/miroslav-matejovsky/opdl/platform/deployment"
	"github.com/miroslav-matejovsky/opdl/platform/internal/fabric"
	fabricolric "github.com/miroslav-matejovsky/opdl/platform/internal/fabric/olric"
)

// These tests start real Olric members and bind real sockets, so they are
// skipped under -short and always use dynamic ports.

const site = "north"

func requireIntegration(t *testing.T) {
	t.Helper()
	if testing.Short() {
		t.Skip("skipping olric integration test in -short mode")
	}
}

// freePort reserves a loopback port and releases it for a member.
func freePort(t *testing.T) string {
	t.Helper()
	var listen net.ListenConfig
	listener, err := listen.Listen(t.Context(), "tcp", "127.0.0.1:0")
	require.NoError(t, err)
	address := listener.Addr().String()
	require.NoError(t, listener.Close())
	return address
}

// open starts a member for topology with cfg and closes it when the test ends.
func open(t *testing.T, descriptor deployment.Descriptor, cfg fabricolric.Config) *fabricolric.Fabric {
	t.Helper()
	if cfg.ShutdownGrace == 0 {
		cfg.ShutdownGrace = 10 * time.Second
	}
	ctx, cancel := context.WithTimeout(t.Context(), 60*time.Second)
	defer cancel()
	f, err := fabricolric.Open(ctx, descriptor, cfg)
	require.NoError(t, err)
	t.Cleanup(func() {
		closeCtx, closeCancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer closeCancel()
		require.NoError(t, f.Close(closeCtx))
	})
	return f
}

// TestTwoMembersFormOneFabricAndShareState is the whole point of the adapter:
// two machines of one site, each knowing only its descriptor, meet and share a
// collection. Both members use their real topology IPs on the default ports, as
// a deployment would.
func TestTwoMembersFormOneFabricAndShareState(t *testing.T) {
	requireIntegration(t)
	ctx := t.Context()

	// Distinct loopback addresses stand in for two machines, so the derived
	// production addresses do not collide and no override is needed.
	nodeA := deployment.Descriptor{
		Site: site, Machine: "node-a", IP: "127.0.0.1",
		EventFabric: deployment.EventFabric{Peers: []deployment.EventFabricPeer{{Site: site, Machine: "node-b", IP: "127.0.0.2"}}},
	}
	nodeB := deployment.Descriptor{
		Site: site, Machine: "node-b", IP: "127.0.0.2",
		EventFabric: deployment.EventFabric{Peers: []deployment.EventFabricPeer{{Site: site, Machine: "node-a", IP: "127.0.0.1"}}},
	}

	configFor := func(descriptor deployment.Descriptor) fabricolric.Config {
		cfg, err := fabricolric.DefaultConfig(descriptor)
		require.NoError(t, err)
		return cfg
	}

	first := open(t, nodeA, configFor(nodeA))
	second := open(t, nodeB, configFor(nodeB))

	// The joiner sees the whole site immediately. The member that started first
	// was alone at its own readiness and converges, so it is polled rather than
	// asserted at an instant.
	require.Eventually(t, func() bool {
		reachable, err := second.Reachable(ctx)
		return err == nil && len(reachable) == 2
	}, 30*time.Second, 100*time.Millisecond, "the second member never saw the first")
	require.Eventually(t, func() bool {
		state, err := first.State(ctx)
		return err == nil && state == fabric.StateConnected
	}, 30*time.Second, 100*time.Millisecond, "the first member never saw the second join")

	// Both resolve live members back to descriptor identities.
	reachable, err := second.Reachable(ctx)
	require.NoError(t, err)
	require.Equal(t, []fabric.Member{
		{Site: site, Machine: "node-a", IP: "127.0.0.1"},
		{Site: site, Machine: "node-b", IP: "127.0.0.2", Self: true},
	}, reachable)

	// One collection, two machines: what one writes, the other reads.
	fromA, err := first.Collection("units")
	require.NoError(t, err)
	fromB, err := second.Collection("units")
	require.NoError(t, err)

	created, err := fromA.Create(ctx, "7/42", []byte("registered"))
	require.NoError(t, err)
	require.True(t, created)

	value, found, err := fromB.Get(ctx, "7/42")
	require.NoError(t, err)
	require.True(t, found, "a value written on one machine must be readable on the other")
	require.Equal(t, []byte("registered"), value)

	// Create is atomic across machines, not just within one.
	created, err = fromB.Create(ctx, "7/42", []byte("again"))
	require.NoError(t, err)
	require.False(t, created, "the key is taken site-wide, not per machine")
}

// TestOpenStartsAloneWhenPeersAreDown checks a site can boot in any order: the
// machine that starts first must not fail because nobody answered.
func TestOpenStartsAloneWhenPeersAreDown(t *testing.T) {
	requireIntegration(t)
	descriptor := deployment.Descriptor{
		Site: site, Machine: "node-a", IP: "127.0.0.1",
		EventFabric: deployment.EventFabric{Peers: []deployment.EventFabricPeer{{Site: site, Machine: "node-b", IP: "127.0.0.2"}}},
	}
	f := open(t, descriptor, fabricolric.Config{
		ClientAddress:     freePort(t),
		MemberlistAddress: freePort(t),
		Join:              []string{freePort(t)},
		StartTimeout:      60 * time.Second,
	})

	// It is up and usable, and it still expects the peer that never came.
	require.Len(t, f.Members(), 2)
	state, err := f.State(t.Context())
	require.NoError(t, err)
	require.Equal(t, fabric.StateDisconnected, state)
}

// TestOpenValidatesBeforeBinding checks a bad configuration fails without
// leaving a listener behind.
func TestOpenValidatesBeforeBinding(t *testing.T) {
	_, err := fabricolric.Open(t.Context(), deployment.Descriptor{Site: site, Machine: "node-a", IP: "127.0.0.1"}, fabricolric.Config{
		ClientAddress:     "not-an-address",
		MemberlistAddress: "127.0.0.1:0",
		StartTimeout:      time.Second,
		ShutdownGrace:     time.Second,
	})
	require.ErrorContains(t, err, "client address")
}

// TestOpenHonorsCanceledContext checks startup is bounded by its caller.
func TestOpenHonorsCanceledContext(t *testing.T) {
	requireIntegration(t)
	ctx, cancel := context.WithCancel(t.Context())
	cancel()

	_, err := fabricolric.Open(ctx, deployment.Descriptor{Site: site, Machine: "node-a", IP: "127.0.0.1"}, fabricolric.Config{
		ClientAddress:     freePort(t),
		MemberlistAddress: freePort(t),
		StartTimeout:      60 * time.Second,
		ShutdownGrace:     10 * time.Second,
	})
	require.ErrorIs(t, err, context.Canceled)
}

// TestOverridesMoveSocketsWithoutChangingIdentity checks the escape hatch for
// hosts where the topology addresses are not bindable: the member listens
// elsewhere but is still the machine the descriptor says it is.
func TestOverridesMoveSocketsWithoutChangingIdentity(t *testing.T) {
	requireIntegration(t)
	descriptor := deployment.Descriptor{Site: site, Machine: "node-a", IP: "10.99.99.99"}
	f := open(t, descriptor, fabricolric.Config{
		ClientAddress:     freePort(t),
		MemberlistAddress: freePort(t),
		StartTimeout:      60 * time.Second,
	})

	// Identity is the descriptor's, not the socket's.
	require.Equal(t, []fabric.Member{
		{Site: site, Machine: "node-a", IP: "10.99.99.99", Self: true},
	}, f.Members())

	// A member that moved still recognizes itself, so a one-member site with an
	// override reads as connected rather than as missing.
	state, err := f.State(t.Context())
	require.NoError(t, err)
	require.Equal(t, fabric.StateConnected, state)
}
