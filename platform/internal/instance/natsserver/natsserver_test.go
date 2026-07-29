package natsserver_test

import (
	"net"
	"testing"
	"time"

	"github.com/nats-io/nats.go"
	"github.com/stretchr/testify/require"

	"github.com/miroslav-matejovsky/opdl/platform/internal/instance/natsserver"
)

const testClusterName = "north"

// How long a routed pair is given to start carrying messages, and how often the
// attempt is repeated. Two servers on one host settle in well under this; the
// margin is for a loaded build agent, and a pair that never settles is a broken
// cluster rather than a slow one.
const (
	routeSettleTimeout  = 10 * time.Second
	routeSettleInterval = 200 * time.Millisecond
)

// connectInProcess opens a NATS client onto one of these servers.
//
// The platform's own client lives at the site level, and this is deliberately
// not it: what is under test here is that two servers carry a message between
// them, so the test uses the plainest client there is rather than the one whose
// behavior it would then also be asserting.
func connectInProcess(t *testing.T, server *natsserver.Server) *nats.Conn {
	t.Helper()
	conn, err := nats.Connect("", nats.InProcessServer(server))
	require.NoError(t, err)
	t.Cleanup(conn.Close)
	return conn
}

// freeLoopbackAddress reserves a loopback port and releases it, so the server
// under test binds an address nothing else on the build agent is using.
//
// The window between releasing and binding is a race in principle. In practice
// the ephemeral range the kernel hands out here is not reused that fast, and the
// alternative — a fixed port — is a test that fails whenever a developer has a
// broker running.
func freeLoopbackAddress(t *testing.T) string {
	t.Helper()
	listener := listen(t, "127.0.0.1:0")
	address := listener.Addr().String()
	require.NoError(t, listener.Close())
	return address
}

// listen binds a loopback address for a test, through the context-aware
// listener the repo's lint requires everywhere.
func listen(t *testing.T, address string) net.Listener {
	t.Helper()
	var config net.ListenConfig
	listener, err := config.Listen(t.Context(), "tcp", address)
	require.NoError(t, err)
	return listener
}

func startTestServer(t *testing.T, name string) *natsserver.Server {
	t.Helper()
	server, err := natsserver.Start(natsserver.Config{
		Name:           name,
		ClusterName:    testClusterName,
		ClusterAddress: freeLoopbackAddress(t),
	})
	require.NoError(t, err)
	t.Cleanup(server.Close)
	return server
}

func TestAStartedServerBindsItsClusterAddressAndAcceptsAnInProcessConnection(t *testing.T) {
	address := freeLoopbackAddress(t)

	server, err := natsserver.Start(natsserver.Config{
		Name:           "node-a-primary",
		ClusterName:    testClusterName,
		ClusterAddress: address,
	})
	require.NoError(t, err)
	t.Cleanup(server.Close)

	// The route listener is bound: a peer server has somewhere to connect.
	var dialer net.Dialer
	peer, err := dialer.DialContext(t.Context(), "tcp", address)
	require.NoError(t, err)
	require.NoError(t, peer.Close())

	// And the connection the platform's own client uses never touches it.
	inProcess, err := server.InProcessConn()
	require.NoError(t, err)
	require.NotNil(t, inProcess)
	require.NoError(t, inProcess.Close())
}

func TestCloseReleasesTheClusterAddress(t *testing.T) {
	address := freeLoopbackAddress(t)
	server, err := natsserver.Start(natsserver.Config{
		Name:           "node-a-primary",
		ClusterName:    testClusterName,
		ClusterAddress: address,
	})
	require.NoError(t, err)

	server.Close()

	// Close waits for the shutdown to finish, so the port is free the moment it
	// returns rather than at some point after it.
	require.NoError(t, listen(t, address).Close())
}

func TestTwoServersRunTogetherOnOneHost(t *testing.T) {
	// A machine runs both of its instances at once, each with its own broker on
	// its own cluster port.
	primary := startTestServer(t, "node-a-primary")
	standby := startTestServer(t, "node-a-standby")

	for _, server := range []*natsserver.Server{primary, standby} {
		conn, err := server.InProcessConn()
		require.NoError(t, err)
		require.NoError(t, conn.Close())
	}
}

// TestRoutedServersFormOneCluster is the site's fabric in miniature: two
// instances, each running its own server, joined into one cluster by the routes
// the descriptor gave them.
//
// It is asserted by publishing rather than by counting routes, because a route
// that is connected but carries nothing is exactly the failure worth catching. A
// message crossing from one member's in-process client to the other's is the
// whole claim the deployment makes about the fabric.
//
// The routes are mutual, as the builder resolves them: every member carries
// every other member, so whichever server starts first the cluster still forms.
func TestRoutedServersFormOneCluster(t *testing.T) {
	addressA := freeLoopbackAddress(t)
	addressB := freeLoopbackAddress(t)

	serverA, err := natsserver.Start(natsserver.Config{
		Name:           "node-a-primary",
		ClusterName:    testClusterName,
		ClusterAddress: addressA,
		Routes:         []string{"nats://" + addressB},
	})
	require.NoError(t, err)
	t.Cleanup(serverA.Close)

	serverB, err := natsserver.Start(natsserver.Config{
		Name:           "node-a-standby",
		ClusterName:    testClusterName,
		ClusterAddress: addressB,
		Routes:         []string{"nats://" + addressA},
	})
	require.NoError(t, err)
	t.Cleanup(serverB.Close)

	publisher := connectInProcess(t, serverA)
	subscriber := connectInProcess(t, serverB)

	subscription, err := subscriber.SubscribeSync("site.facts")
	require.NoError(t, err)
	require.NoError(t, subscriber.Flush())

	// The route is dialed in the background and the subscriber's interest has to
	// reach the other member, so an early publish is dropped rather than
	// queued. Publishing until one arrives is what waits for the cluster.
	var delivered []byte
	require.Eventually(t, func() bool {
		if err := publisher.Publish("site.facts", []byte("a site fact")); err != nil {
			return false
		}
		if err := publisher.Flush(); err != nil {
			return false
		}
		message, err := subscription.NextMsg(routeSettleInterval)
		if err != nil {
			return false
		}
		delivered = message.Data
		return true
	}, routeSettleTimeout, routeSettleInterval,
		"a message published on one member should reach a subscriber on the other")
	require.Equal(t, []byte("a site fact"), delivered)
}

// TestAServerWithNoRoutesStillComesUp checks the site that deploys one instance.
// It has no peer to dial, which is a cluster of one rather than a failure.
func TestAServerWithNoRoutesStillComesUp(t *testing.T) {
	server := startTestServer(t, "solo-primary")
	conn, err := server.InProcessConn()
	require.NoError(t, err)
	require.NoError(t, conn.Close())
}

// TestAServerComesUpBeforeItsPeer checks that an unreachable peer does not hold
// up a start.
//
// Both instances of a machine are launched at once and a site's machines boot
// independently, so every member routes to peers that are not listening yet. A
// server that waited for them would make the first one started fail.
func TestAServerComesUpBeforeItsPeer(t *testing.T) {
	// Nothing is listening here: the address was reserved and released.
	absentPeer := freeLoopbackAddress(t)

	server, err := natsserver.Start(natsserver.Config{
		Name:           "node-a-primary",
		ClusterName:    testClusterName,
		ClusterAddress: freeLoopbackAddress(t),
		Routes:         []string{"nats://" + absentPeer},
	})
	require.NoError(t, err)
	t.Cleanup(server.Close)

	conn, err := server.InProcessConn()
	require.NoError(t, err)
	require.NoError(t, conn.Close())
}

func TestAnUnusableConfigurationIsRefusedBeforeAnythingIsBound(t *testing.T) {
	tests := []struct {
		name   string
		config natsserver.Config
	}{
		{
			name:   "no name",
			config: natsserver.Config{Name: "  ", ClusterName: testClusterName, ClusterAddress: "127.0.0.1:6222"},
		},
		{
			name:   "no cluster name",
			config: natsserver.Config{Name: "node-a-primary", ClusterAddress: "127.0.0.1:6222"},
		},
		{
			name:   "no cluster address",
			config: natsserver.Config{Name: "node-a-primary", ClusterName: testClusterName},
		},
		{
			name:   "cluster address is not host:port",
			config: natsserver.Config{Name: "node-a-primary", ClusterName: testClusterName, ClusterAddress: "127.0.0.1"},
		},
		{
			name:   "cluster address has no usable host",
			config: natsserver.Config{Name: "node-a-primary", ClusterName: testClusterName, ClusterAddress: "not-an-ip:6222"},
		},
		{
			name:   "cluster port is out of range",
			config: natsserver.Config{Name: "node-a-primary", ClusterName: testClusterName, ClusterAddress: "127.0.0.1:99999"},
		},
		{
			name: "a route is not a url",
			config: natsserver.Config{
				Name: "node-a-primary", ClusterName: testClusterName,
				ClusterAddress: "127.0.0.1:6222", Routes: []string{"nats://%zz"},
			},
		},
		{
			name: "a route is a bare address",
			config: natsserver.Config{
				Name: "node-a-primary", ClusterName: testClusterName,
				ClusterAddress: "127.0.0.1:6222", Routes: []string{"127.0.0.1:6223"},
			},
		},
		{
			name: "a route is dialed as something other than a route",
			config: natsserver.Config{
				Name: "node-a-primary", ClusterName: testClusterName,
				ClusterAddress: "127.0.0.1:6222", Routes: []string{"tcp://127.0.0.1:6223"},
			},
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			server, err := natsserver.Start(tc.config)
			require.Error(t, err)
			require.Nil(t, server)
		})
	}
}
