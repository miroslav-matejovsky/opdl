package eventfabric_test

import (
	"context"
	"errors"
	"net"
	"testing"
	"time"

	"github.com/nats-io/nats-server/v2/server"
	"github.com/stretchr/testify/require"

	"github.com/miroslav-matejovsky/opdl/platform/internal/site/eventfabric"
)

// brokerReadyTimeout bounds how long a test waits for its broker to come up. It
// is a broker with no configuration to speak of, so anything near this is a
// build agent in trouble rather than a slow start.
const brokerReadyTimeout = 5 * time.Second

// startBroker runs an embedded NATS broker for a test to connect to.
//
// It builds one here rather than taking the instance level's natsserver
// package, because that is the point of the interface under test: the site's
// client works against anything that hands it an in-process connection, and a
// test that reached for the instance's package would prove the pairing instead
// of the contract. The instance's own package is tested where it lives.
func startBroker(t *testing.T) *server.Server {
	t.Helper()
	broker, err := server.NewServer(&server.Options{
		ServerName: "test-broker",
		Host:       "127.0.0.1",
		Port:       server.RANDOM_PORT,
		NoSigs:     true,
		NoLog:      true,
	})
	require.NoError(t, err)
	broker.Start()
	require.True(t, broker.ReadyForConnections(brokerReadyTimeout))
	t.Cleanup(func() {
		broker.Shutdown()
		broker.WaitForShutdown()
	})
	return broker
}

func connect(t *testing.T, broker eventfabric.InProcessConnProvider) *eventfabric.Client {
	t.Helper()
	client, err := eventfabric.Connect(broker, "opdl-platform-node-a-primary")
	require.NoError(t, err)
	t.Cleanup(client.Close)
	return client
}

func TestCheckPassesWhenTheBrokerCarriesAMessage(t *testing.T) {
	client := connect(t, startBroker(t))

	require.NoError(t, client.Check(t.Context()))
	// It is a round trip, not a one-off handshake, so it holds on repetition.
	require.NoError(t, client.Check(t.Context()))
}

func TestCheckFailsOnceTheBrokerIsGone(t *testing.T) {
	broker := startBroker(t)
	client := connect(t, broker)
	require.NoError(t, client.Check(t.Context()))

	broker.Shutdown()
	broker.WaitForShutdown()

	// This is what the health endpoint reports on: the fabric has stopped
	// carrying messages, whatever the connection object thinks of itself.
	require.Error(t, client.Check(t.Context()))
}

func TestCheckFailsWhenItsContextIsAlreadyDone(t *testing.T) {
	client := connect(t, startBroker(t))

	ctx, cancel := context.WithCancel(t.Context())
	cancel()

	// A health request that was abandoned must not leave the endpoint waiting on
	// a round trip nobody is reading.
	require.Error(t, client.Check(ctx))
}

func TestConcurrentChecksDoNotReadEachOthersMessages(t *testing.T) {
	client := connect(t, startBroker(t))

	const checks = 8
	done := make(chan error, checks)
	for range checks {
		go func() { done <- client.Check(t.Context()) }()
	}
	for range checks {
		require.NoError(t, <-done)
	}
}

func TestConnectRefusesAnIncompleteRequest(t *testing.T) {
	broker := startBroker(t)

	tests := []struct {
		name   string
		broker eventfabric.InProcessConnProvider
		client string
	}{
		{name: "no broker", broker: nil, client: "opdl-platform-node-a-primary"},
		{name: "no connection name", broker: broker, client: "  "},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			client, err := eventfabric.Connect(tc.broker, tc.client)
			require.Error(t, err)
			require.Nil(t, client)
		})
	}
}

func TestConnectFailsWhenTheBrokerWillNotHandOutAConnection(t *testing.T) {
	client, err := eventfabric.Connect(refusingBroker{}, "opdl-platform-node-a-primary")
	require.Error(t, err)
	require.Nil(t, client)
}

// refusingBroker stands in for a broker that is not accepting connections, so
// the failure Connect reports can be asserted without one that is half started.
type refusingBroker struct{}

func (refusingBroker) InProcessConn() (net.Conn, error) {
	return nil, errors.New("the broker is not accepting connections")
}
