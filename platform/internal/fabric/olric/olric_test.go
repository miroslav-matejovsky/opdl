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

// open starts a primary member for topology with cfg and closes it when the test
// ends.
func open(t *testing.T, descriptor deployment.Descriptor, cfg fabricolric.Config) *fabricolric.Fabric {
	t.Helper()
	return openInstance(t, descriptor, "primary", cfg)
}

// openInstance starts the named instance's member for topology with cfg and
// closes it when the test ends.
func openInstance(t *testing.T, descriptor deployment.Descriptor, selfInstance string, cfg fabricolric.Config) *fabricolric.Fabric {
	t.Helper()
	if cfg.ShutdownGrace == 0 {
		cfg.ShutdownGrace = 10 * time.Second
	}
	ctx, cancel := context.WithTimeout(t.Context(), 60*time.Second)
	defer cancel()
	f, err := fabricolric.Open(ctx, descriptor, selfInstance, cfg)
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
		PlatformInstances: primaryInstances("127.0.0.1"),
		Fabric:            deployment.Fabric{Peers: []deployment.FabricPeer{primaryPeer("node-b", "127.0.0.2")}},
	}
	nodeB := deployment.Descriptor{
		Site: site, Machine: "node-b", IP: "127.0.0.2",
		PlatformInstances: primaryInstances("127.0.0.2"),
		Fabric:            deployment.Fabric{Peers: []deployment.FabricPeer{primaryPeer("node-a", "127.0.0.1")}},
	}

	configFor := func(descriptor deployment.Descriptor) fabricolric.Config {
		cfg, err := fabricolric.DefaultConfig(descriptor, "primary")
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
		{Site: site, Machine: "node-a", Instance: "primary", IP: "127.0.0.1"},
		{Site: site, Machine: "node-b", Instance: "primary", IP: "127.0.0.2", Self: true},
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
		PlatformInstances: primaryInstances("127.0.0.1"),
		Fabric:            deployment.Fabric{Peers: []deployment.FabricPeer{primaryPeer("node-b", "127.0.0.2")}},
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
	_, err := fabricolric.Open(t.Context(), deployment.Descriptor{Site: site, Machine: "node-a", IP: "127.0.0.1"}, "primary", fabricolric.Config{
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

	_, err := fabricolric.Open(ctx, deployment.Descriptor{Site: site, Machine: "node-a", IP: "127.0.0.1"}, "primary", fabricolric.Config{
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
		{Site: site, Machine: "node-a", Instance: "primary", IP: "10.99.99.99", Self: true},
	}, f.Members())

	// A member that moved still recognizes itself, so a one-member site with an
	// override reads as connected rather than as missing.
	state, err := f.State(t.Context())
	require.NoError(t, err)
	require.Equal(t, fabric.StateConnected, state)
}

// redundantInstances is a machine's primary and secondary on one IP, each with
// its own explicit ports, as a redundant deployment describes them.
func redundantInstances(ip string) []deployment.PlatformInstance {
	return []deployment.PlatformInstance{
		{Name: "primary", APIAddress: ip + ":8080", FabricClientAddress: ip + ":3320", FabricMemberlistAddress: ip + ":3322"},
		{Name: "secondary", APIAddress: ip + ":8081", FabricClientAddress: ip + ":3321", FabricMemberlistAddress: ip + ":3323"},
	}
}

// TestSameMachineInstancesFormOneFabric is the redundant machine's case: one
// machine runs its primary and secondary as two members that bind distinct
// endpoints, meet each other, and share a collection. Live membership reports
// both instead of collapsing the two into one machine.
func TestSameMachineInstancesFormOneFabric(t *testing.T) {
	requireIntegration(t)
	ctx := t.Context()

	descriptor := deployment.Descriptor{
		Site: site, Machine: "node-a", IP: "127.0.0.1",
		PlatformInstances: redundantInstances("127.0.0.1"),
	}
	primaryCfg, err := fabricolric.DefaultConfig(descriptor, "primary")
	require.NoError(t, err)
	secondaryCfg, err := fabricolric.DefaultConfig(descriptor, "secondary")
	require.NoError(t, err)

	// The two instances bind their own explicit endpoints and seed from each
	// other, never from themselves.
	require.NotEqual(t, primaryCfg.ClientAddress, secondaryCfg.ClientAddress)
	require.NotEqual(t, primaryCfg.MemberlistAddress, secondaryCfg.MemberlistAddress)
	require.Equal(t, []string{secondaryCfg.MemberlistAddress}, primaryCfg.Join)
	require.Equal(t, []string{primaryCfg.MemberlistAddress}, secondaryCfg.Join)

	primary := openInstance(t, descriptor, "primary", primaryCfg)
	secondary := openInstance(t, descriptor, "secondary", secondaryCfg)

	// The secondary seeds from the primary, so once it is ready it already sees
	// both members of the machine, reported as two distinct instances.
	require.Eventually(t, func() bool {
		reachable, err := secondary.Reachable(ctx)
		return err == nil && len(reachable) == 2
	}, 30*time.Second, 100*time.Millisecond, "the secondary never saw the primary")
	require.Equal(t, []fabric.Member{
		{Site: site, Machine: "node-a", Instance: "primary", IP: "127.0.0.1"},
		{Site: site, Machine: "node-a", Instance: "secondary", IP: "127.0.0.1", Self: true},
	}, mustReachable(ctx, t, secondary), "both instances of one machine are reported, not collapsed by machine")

	// One collection, two instances of one machine: what one writes, the other reads.
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
}

// TestReachableIgnoresUnexpectedMembers checks a member that is not part of the
// site never inflates the expected-membership view, even after it joins at the
// memberlist level.
func TestReachableIgnoresUnexpectedMembers(t *testing.T) {
	requireIntegration(t)
	ctx := t.Context()

	// A solo machine: its descriptor lists no peer, so its expected membership is
	// itself alone.
	client, memberlist := freePort(t), freePort(t)
	solo := deployment.Descriptor{
		Site: site, Machine: "node-a", IP: "127.0.0.1",
		PlatformInstances: []deployment.PlatformInstance{{Name: "primary", APIAddress: "127.0.0.1:8080", FabricClientAddress: client, FabricMemberlistAddress: memberlist}},
	}
	first := open(t, solo, fabricolric.Config{ClientAddress: client, MemberlistAddress: memberlist, StartTimeout: 60 * time.Second})

	// A stray member from another site seeds from the solo machine and joins its
	// memberlist cluster. Nothing in the solo machine's descriptor expects it.
	open(t, deployment.Descriptor{Site: "other", Machine: "node-z", IP: "127.0.0.1"}, fabricolric.Config{
		ClientAddress:     freePort(t),
		MemberlistAddress: freePort(t),
		Join:              []string{memberlist},
		StartTimeout:      60 * time.Second,
	})

	// The stray is unrecognized, so the solo machine's reachability stays itself
	// alone and it never reads as more than a connected site of one.
	require.Never(t, func() bool {
		reachable, err := first.Reachable(ctx)
		return err != nil || len(reachable) != 1
	}, 3*time.Second, 100*time.Millisecond, "an unexpected member leaked into the expected-membership view")
	require.Equal(t, []fabric.Member{
		{Site: site, Machine: "node-a", Instance: "primary", IP: "127.0.0.1", Self: true},
	}, mustReachable(ctx, t, first))
	state, err := first.State(ctx)
	require.NoError(t, err)
	require.Equal(t, fabric.StateConnected, state)
}

// mustReachable returns a member's current reachability.
func mustReachable(ctx context.Context, t *testing.T, f *fabricolric.Fabric) []fabric.Member {
	t.Helper()
	reachable, err := f.Reachable(ctx)
	require.NoError(t, err)
	return reachable
}
